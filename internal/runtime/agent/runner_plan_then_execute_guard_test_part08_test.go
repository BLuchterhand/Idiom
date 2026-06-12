package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestPlanExecutePostReadinessRecoveryAllowsCandidateAnchorRefresh(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-360 (total lines: 360)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `internal/workflows/productworkflow/coverage_gate.go: old_text not found in current source`, `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        4096,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results)

	if opts.ToolChoice != "required" {
		t.Fatalf("candidate anchor-refresh recovery should force a parser-readable tool request, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 2 || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "search_content") || chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("candidate anchor-refresh recovery should expose only source-body read tools, got %#v", opts.Tools)
	}

	if opts.MaxTokens > planThenExecuteAnchorRefreshMaxTokens {
		t.Fatalf("candidate anchor-refresh recovery should cap retry budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecutePostReadinessRecoveryAllowsSyntaxPreflightRefresh(t *testing.T) {
	thinkingOn := true
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        4096,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 80-125"},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-80"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: preflight syntax validation failed for cmd/idiom/product_workflow.go at line 100: ./cmd/idiom/product_workflow.go:100:6: expected '(', found runProductValidateArtifact",
		},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("syntax-refresh recovery should force a parser-readable tool request, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 2 || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "search_content") || chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("syntax-refresh recovery should expose only source-body read tools, got %#v", opts.Tools)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("syntax-refresh recovery should force visible/tool output over hidden thinking, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecutePostReadinessRecoveryForcesCorrectedSubmitPlanAfterAnchorRefresh(t *testing.T) {
	thinkingOn := true
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        4096,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "read_file", Summary: "Read f2 internal/workflows/productworkflow/coverage_gate.go lines 1-20"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("corrected-plan recovery should require submit_plan after anchor refresh, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("corrected-plan recovery should expose only submit_plan after anchor refresh, got %#v", opts.Tools)
	}
}

func TestPlanExecuteRuntimeRecoverySequencePreservesNewFilePlannerBudget(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Create internal/contracts/writerroute/decision.go",
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision.go"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/contracts/writerroute/decision.go", Error: "path is a NEW FILE and does not exist yet"}}
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:         "auto",
	}, brief, results)

	opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptions(opts, brief, results)

	if opts.MaxTokens != 16384 {
		t.Fatalf("real recovery option sequence should preserve new-file planner budget, got %d", opts.MaxTokens)
	}
}

func TestReasoningSubmitPlanRejectsOldTextOnlyJSONEdit(t *testing.T) {
	req, err := workspace.DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","edits":[{"path":"a.txt","old_text":"old"}]}`))
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON: %v", err)
	}

	if reasoningSubmitPlanHasConcreteEdit(req) {
		t.Fatal("old_text-only JSON submit_plan must not be treated as a concrete visible edit")
	}
}

func TestPlanExecuteCandidateWriteOnlyRecoveryDoesNotForceWithoutReadEvidence(t *testing.T) {
	opts := planThenExecuteCandidateWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		MaxTokens:  2048,
		Tools:      []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "context_pack", Summary: "candidate packet"}})

	if opts.ToolChoice != "auto" {
		t.Fatalf("candidate recovery without a source read should not force tool choice, got %q", opts.ToolChoice)
	}

	if opts.MaxTokens != 2048 {
		t.Fatalf("candidate recovery without a source read should preserve budget, got %d", opts.MaxTokens)
	}
}

func TestReadPathsFromResultsCountsCodeNavigateRouteSummary(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1382-1501",
				"",
				"1382:func runProductTDD(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 3209-3230",
		},
	}

	paths := readPathsFromResults(results)
	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("read paths missing %q: %#v", want, paths)
		}
	}
}

func TestReadPathsFromResultsDoesNotCountRouteSummaryWithoutSourceBody(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1382-1501",
		}, "\n"),
	}}

	paths := readPathsFromResults(results)
	if _, ok := paths["cmd/idiom/product_workflow.go"]; ok {
		t.Fatalf("route_summary without source body should not satisfy source readiness: %#v", paths)
	}
}

const syntheticPolicyGuardApplyObjective = "We are continuing TDD completion work from docs/design-prd-adr-brief-research-workflow.md. Implement one bounded topic only: policy guard apply must reject predictable staging temp symlinks before durable writes. Focus on the policy guard apply path, not the surrounding workflow boilerplate. Add regression tests proving staging temp symlinks are rejected without partial mutation. Use repo discovery first, make the minimal code changes, and run the focused tests."

func syntheticPolicyGuardApplyContextPackOutput() string {
	return `{"candidate_files":[{"path":"internal/policy/guard.go"},{"path":"internal/policy/guard_apply.go"}]}`
}

func syntheticPolicyGuardApplySearchOutput() string {
	return strings.Join([]string{
		`internal/policy/guard.go:34: // policy guard planning metadata`,
		`cmd/idiom/policy.go:65: idiom policy guard --apply updates workflow summaries`,
		`internal/policy/guard_apply.go:88: tmpPath := stagingPath + ".tmp"`,
		`internal/policy/guard_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
	}, "\n")
}

func TestLiteralRecoverySearchPatternPrioritizesTopicOverWorkflowBoilerplate(t *testing.T) {
	got := literalRecoverySearchPattern(syntheticPolicyGuardApplyObjective)
	for _, want := range []string{"policy", "guard", "apply", "temp"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recovery search pattern missing %q: %q", want, got)
		}
	}

	for _, notWant := range []string{"tdd", "prd", "adr", "brief", "research", "workflow"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("recovery search pattern should not spend early terms on workflow boilerplate %q: %q", notWant, got)
		}
	}
}

func TestCompletionGateRecoveryPromptKeepsUnreadDiscoveredCandidateReadVisible(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         syntheticPolicyGuardApplyObjective,
		},
	}

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  syntheticPolicyGuardApplyContextPackOutput(),
		},
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output:  syntheticPolicyGuardApplySearchOutput(),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/policy/guard.go lines 1-180 (total lines: 180)",
			Output:  "type PolicyGuardPlan struct{}",
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)

	if !ok {
		t.Fatalf("expected candidate-scope recovery prompt")
	}

	if hideReads {
		t.Fatalf("candidate-scope recovery should keep bounded reads visible when other surfaced candidate paths remain unread:\n%s", prompt)
	}

	for _, want := range []string{
		"Allowed/candidate paths are not coverage obligations",
		"submit_plan",
		"Read candidate path(s): internal/policy/guard.go",
		"read one different unread candidate path",
		"internal/policy/guard_apply.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteCandidateScopeReadinessDoesNotDeferForUnreadBetterCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         syntheticPolicyGuardApplyObjective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  syntheticPolicyGuardApplyContextPackOutput(),
		},
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output:  syntheticPolicyGuardApplySearchOutput(),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/policy/guard.go lines 1-180",
			Output:  "type PolicyGuardPlan struct{}",
		},
	}
	got := planThenExecuteReadyToSubmitNudge(brief, results)

	if got == "" {
		t.Fatal("unscoped candidate evidence should create submit-ready guidance without requiring every ranked candidate")
	}

	for _, want := range []string{
		"internal/policy/guard.go",
		"Allowed/candidate paths are not coverage obligations",
		"One bounded source-read/search for a different candidate path",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("readiness nudge missing %q:\n%s", want, got)
		}
	}
}

func TestPlanExecuteCandidateScopeReadinessDoesNotDeferForUnreadAllowedPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		Contract:         "implementation_result",
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-20"},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		if !strings.Contains(got, "PLANNER READINESS REACHED") {
			t.Fatalf("allowed candidate evidence should close discovery without reading every allowed path:\n%s", got)
		}

		if strings.Contains(got, "pkg/feature_test.go") {
			t.Fatalf("nudge should not promote unread allowed paths to coverage obligations:\n%s", got)
		}

		return
	}

	t.Fatal("expected candidate-scope readiness after one allowed candidate source read")
}

func TestPlanThenExecuteVisibleBriefKeepsSubmitPlanContract(t *testing.T) {
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"implementation_packet_write_ready"},
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan", "edit_file", "apply_patch", "harness_feedback"},
		RequiredTools:     []string{"submit_plan"},
		ReadinessGate:     []string{"submit_plan_called_or_blocker_reported"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, nil)

	if !slices.Contains(brief.AllowedTools, "submit_plan") {
		t.Fatalf("plan-execute planner surface must keep submit_plan, got %#v", brief.AllowedTools)
	}

	if !slices.Contains(brief.AllowedTools, "harness_feedback") {
		t.Fatalf("plan-execute planner surface should not be narrowed by implementation scoped-edit filters, got %#v", brief.AllowedTools)
	}

	if strings.Join(brief.RequiredTools, "\n") != "submit_plan" {
		t.Fatalf("plan-execute planner required tools = %#v, want submit_plan only", brief.RequiredTools)
	}
}

func TestPlanThenExecutePlannerSurfaceKeepsProductWorkflowBrowsers(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	manager.SetPlanThenExecutePlannerMode(true)

	runner := &Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"recovery_inspect", "recovery_status", "product_efforts", "product_status", "read_file", "submit_plan"},
			RequiredTools:     []string{"submit_plan"},
		},
	}

	allowed := runner.advertisedAllowedTools(manager.AllowedTools())
	for _, tool := range []string{"recovery_inspect", "recovery_status", "product_efforts", "product_status"} {
		if !slices.Contains(allowed, tool) {
			t.Fatalf("planner surface lost %s; allowed=%#v", tool, allowed)
		}

		if summary, errorText, reject := currentToolSurfaceRejection(workspace.ToolRequest{Tool: tool}, allowed); reject {
			t.Fatalf("%s should be callable in plan-execute planner surface, summary=%q error=%q allowed=%#v", tool, summary, errorText, allowed)
		}
	}

	if slices.Contains(allowed, "recovery_decompose") {
		t.Fatalf("planner surface must not expose recovery_decompose because it writes recovery packets, allowed=%#v", allowed)
	}
}

func TestPlanThenExecuteControlRailsKeepSubmitPlanContract(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan", "edit_file", "apply_patch"},
			RequiredTools:     []string{"submit_plan"},
			ReadinessGate:     []string{"submit_plan_called_or_blocker_reported"},
			SearchPolicy:      []string{"submit_plan_when_edit_set_is_supported"},
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	rails := runner.controlRails(nil)
	if !slices.Contains(rails.RequiredTools, "submit_plan") {
		t.Fatalf("control rails must require submit_plan for planner phase, got %#v", rails.RequiredTools)
	}

	if slices.Contains(rails.RequiredTools, "edit_file_or_apply_patch") {
		t.Fatalf("control rails must not advertise per-edit write requirement in planner phase: %#v", rails.RequiredTools)
	}

	if !slices.Contains(rails.ReadinessGate, "submit_plan_or_source_satisfied_or_blocker_reported") {
		t.Fatalf("control rails must keep submit_plan readiness gate, got %#v", rails.ReadinessGate)
	}

	if !slices.Contains(rails.SearchPolicy, "required_existing_paths_need_source_body_before_submit_plan") {
		t.Fatalf("control rails must advertise source-body-before-submit_plan policy, got %#v", rails.SearchPolicy)
	}

	if slices.Contains(rails.SearchPolicy, "trust_preloaded_current_state_anchors") {
		t.Fatalf("control rails must not imply preloaded anchors satisfy source-body evidence, got %#v", rails.SearchPolicy)
	}

	if !slices.Contains(rails.FinalCritic, "submit_plan_called") ||
		!slices.Contains(rails.FinalCritic, "source_satisfied_or_blocker_only_with_per_path_evidence") {

		t.Fatalf("control rails must keep submit_plan final critic, got %#v", rails.FinalCritic)
	}
}

func TestPlanThenExecuteRequiredPathHintsUseSubmitPlanLanguage(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, nil)

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan") {
		t.Fatalf("planner-phase hints must name submit_plan, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}

	if !strings.Contains(joined, "read required_edit_path a.txt with source-body evidence before submit_plan") {
		t.Fatalf("planner-phase hints must frontload unread required-path source evidence, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}

	if !strings.Contains(joined, "submit_plan before every existing required_edit_path has satisfied its source-body obligation") {
		t.Fatalf("planner-phase hints must reject premature submit_plan, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}

	if !strings.Contains(joined, "source-satisfied") {
		t.Fatalf("planner-phase hints must name source-satisfied closeout, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}

	if strings.Contains(joined, "write capability") || strings.Contains(joined, "first required-path write") {
		t.Fatalf("planner-phase hints must not use generic write/edit closeout language: %s", joined)
	}
}

func TestPlanThenExecuteRequiredPathHintsSwitchToSubmitPlanAfterSourceEvidence(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)", Output: "current a"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)", Output: "current b"},
	})

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan") || !strings.Contains(joined, "source_satisfied_paths") {
		t.Fatalf("planner-phase hints should require submit_plan once required paths are read, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}

	if strings.Contains(joined, "read required_edit_path a.txt with source-body evidence before submit_plan") {
		t.Fatalf("planner-phase hints should not keep source-read-first language after every required path is read: %s", joined)
	}
}

func TestPlanThenExecuteRequiredPathHintsClearStaleSourceReadLanguageAfterReadiness(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		RequiredNext: []string{
			"read required_edit_path a.txt with source-body evidence before submit_plan; after every existing required_edit_path has satisfied its source-body obligation, submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths",
		},
		InvalidActions: []string{
			"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
			"new orientation read/search before the next unread required_edit_path source-body read",
		},
		SearchPolicy: []string{
			"progress through unread required_edit_paths one source-body read at a time before submit_plan",
			"keep this unrelated policy",
		},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)", Output: "current a"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read B in b.txt lines 1-10\n1: current b"},
	})

	joined := strings.Join(append(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), brief.SearchPolicy...), "\n")
	if !strings.Contains(joined, "submit_plan") || !strings.Contains(joined, "source_satisfied_paths") {
		t.Fatalf("planner-phase hints should require submit_plan once source evidence is complete, got %s", joined)
	}

	for _, stale := range []string{
		"read required_edit_path a.txt",
		"submit_plan before every existing required_edit_path has satisfied",
		"new orientation read/search before the next unread required_edit_path",
		"progress through unread required_edit_paths",
	} {
		if strings.Contains(joined, stale) {
			t.Fatalf("planner-phase hints retained stale source-read language %q: %s", stale, joined)
		}
	}

	if !strings.Contains(joined, "keep this unrelated policy") {
		t.Fatalf("planner-phase hint cleanup should preserve unrelated policy, got %s", joined)
	}
}

func TestPlanExecuteReadinessRecoverySyncsTextualToolSurfaceWithSubmitPlanOnly(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	mgr.SetPlanThenExecutePlannerMode(true)

	if !slices.Contains(mgr.AllowedTools(), "harness_feedback") {
		t.Fatalf("test requires harness_feedback to be present in the broad planner surface")
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"harness_feedback","category":"tooling","component":"plan-execute","observed":"stale textual allow-list admitted a side channel","expected":"submit_plan only","impact":"planner recovery can drift away from the required visible action","confidence":"high"}`,
		`{"tool":"submit_plan","rationale":"Replace both required fixture files.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after b\n","purpose":"replace b fixture"}]}`,
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var executedFeedback bool

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools:     buildLlamaTools(mgr.BuildToolDefinitions(mgr.AllowedTools())),
	}, nil, func(event ToolRequestEvent) {
		if event.Request.Tool == "harness_feedback" {
			executedFeedback = true
		}
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if executedFeedback {
		t.Fatalf("harness_feedback executed after readiness; textual allow-list should have been synced to submit_plan only")
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful plan after rejecting side-channel tool, got:\n%s", answer)
	}
}

func TestPlanThenExecuteRequiredPathHintsDoNotReadTerminalMissingPath(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"new_file.go"},
	}, []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "read_file failed for new_file.go",
			Error:   `path "new_file.go" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.`,
		},
	})

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan") || !strings.Contains(joined, "source_satisfied_paths") {
		t.Fatalf("terminal missing-file evidence should move planner toward submit_plan/question, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}

	if strings.Contains(joined, "read required_edit_path new_file.go with source-body evidence before submit_plan") {
		t.Fatalf("terminal missing-file evidence should not keep read-first language: %s", joined)
	}
}

func TestPlanThenExecuteMissingNewFilePathsBecomeSubmitReadyWithoutMoreReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{
			"internal/viewer/doc1.go",
			"internal/viewer/doc2.go",
			"internal/viewer/doc3.go",
			"internal/viewer/doc4.go",
			"internal/viewer/doc5.go",
		},
	}

	var results []workspace.ToolResult

	for _, path := range brief.RequiredEditPaths {
		results = append(results, workspace.ToolResult{
			Tool:    "read_file",
			Summary: "read_file failed for " + path,
			Error:   `path "` + path + `" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.`,
		})
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results); len(unread) != 0 {
		t.Fatalf("missing required new files should not remain source-body reads after first missing-file proof, got %#v", unread)
	}

	if !planThenExecuteSubmitPlanOnlyReady(brief, results) {
		t.Fatal("all missing required new files should transition to submit-ready/blocker contract")
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); !strings.Contains(got, "PLANNER READINESS REACHED") {
		t.Fatalf("missing required new files should get terminal submit-ready nudge, got:\n%s", got)
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("Mode = %q, want submit_ready; states=%#v", state.Mode, state.RequiredPathStates)
	}

	for _, got := range state.RequiredPathStates {
		if !strings.HasSuffix(got, ":"+string(planThenExecuteRequiredPathNewFileBodyRequired)) {
			t.Fatalf("required path state = %q, want new_file_body_required", got)
		}
	}

	runner := &Runner{RuntimeBrief: brief}
	if req, raw, ok := runner.deterministicPlanExecuteRequiredSourceRead(results); ok {
		t.Fatalf("missing new-file paths should not schedule another source-body read, got req=%#v raw=%s", req, raw)
	}
}

func TestPlanThenExecuteMissingSubmitPlanGateNamesSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	got, rejected := runner.mutationCompletionRejection("Done.", nil)
	if !rejected {
		t.Fatalf("planner phase without submit_plan must be rejected")
	}

	if !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("planner-phase rejection must name submit_plan, got %#v", got)
	}

	if !strings.Contains(got.Error, "source-satisfied") {
		t.Fatalf("planner-phase rejection must offer source-satisfied closeout, got %#v", got)
	}

	if strings.Contains(got.Error, "no successful write tool result") {
		t.Fatalf("planner-phase rejection must not use generic write-tool language: %#v", got)
	}
}

func TestPlanThenExecuteMissingSubmitPlanGateWithoutRequiredPathsNamesCandidateScope(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	got, rejected := runner.mutationCompletionRejection("Done.", nil)
	if !rejected {
		t.Fatalf("planner phase without submit_plan must be rejected")
	}

	if !strings.Contains(got.Error, "candidate/allowed edit path") {
		t.Fatalf("planner-phase rejection without required paths should name candidate scope, got %#v", got)
	}

	if strings.Contains(got.Error, "every required_edit_paths entry") {
		t.Fatalf("planner-phase rejection without required paths should not imply required path coverage, got %#v", got)
	}
}

func TestPlanThenExecuteCandidateCompletionRejectsGenericBlockedProse(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read file.", Output: `{"path":"cmd/idiom/product_workflow.go","content":"before\n"}`},
		{Tool: "submit_plan", Summary: "Plan rejected", Error: "old_text not found in cmd/idiom/product_workflow.go"},
	}

	got, rejected := runner.mutationCompletionRejection("The planner is blocked because candidate source is already ready but no plan was submitted.", results)
	if !rejected {
		t.Fatalf("candidate plan-execute must reject generic blocked prose without submit_plan, got %#v", got)
	}

	if !strings.Contains(got.Error, "submit_plan") || !strings.Contains(got.Error, "<<QUESTION>>") {
		t.Fatalf("candidate rejection should name the exact planner closeout contract, got %#v", got)
	}
}

func TestPlanThenExecuteCandidateCompletionAllowsTypedBlocker(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		},
	}

	if got, rejected := runner.mutationCompletionRejection("Decision: block\n\nExact blocker: current source anchor for cmd/idiom/product_workflow.go could not be recovered after the rejected submit_plan.", nil); rejected {
		t.Fatalf("typed candidate blocker should satisfy planner closeout, got %#v", got)
	}

	if got, rejected := runner.mutationCompletionRejection("<<QUESTION>>: exact blocker: current source anchor for cmd/idiom/product_workflow.go could not be recovered after the rejected submit_plan.", nil); rejected {
		t.Fatalf("question-style candidate blocker should satisfy planner closeout, got %#v", got)
	}
}

func TestPlanThenExecuteCandidateCompletionRejectsMentionedDecisionBlock(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go"},
		},
	}

	got, rejected := runner.mutationCompletionRejection("I know I could use Decision: block, but I will keep investigating.", nil)
	if !rejected {
		t.Fatalf("mentioning Decision: block in prose must not satisfy typed blocker closeout, got %#v", got)
	}
}

func TestMutationCompletionGateStillRejectsImplementationLaneWithoutEdits(t *testing.T) {
	// Sanity check: my plan-execute exemption must not weaken the gate
	// for normal implementation lanes. A regular implementation that
	// finalizes without any write tool result (and no submit_plan)
	// should still be rejected.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got, rejected := runner.mutationCompletionRejection("Done.", nil)

	if !rejected {
		t.Fatalf("implementation lane with no edits must be rejected; got %#v", got)
	}
}

func TestCompletionGateRecoveryPromptForUnscopedImplementationNoWrite(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "Ranked context packet produced.", Output: `{"candidate_files":[{"path":"candidate/path.go"}]}`},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for direct implementation with no write")
	}

	if hideReads {
		t.Fatalf("direct implementation recovery should leave bounded reads visible")
	}

	for _, want := range []string{
		"before any successful workspace write",
		"No required_edit_paths list is active",
		"target-selection failure",
		"parser-readable action",
		"not prose",
		"edit_file",
		"write_file",
		"append_file",
		"apply_patch",
		"Do not call a write tool until",
		"one visible JSON object",
		"Qwen key-value `tool:` format",
		"Function-call write syntax is not executable",
		"previous `context_pack` did not establish an exact edit target",
		"`search_content`",
		"named_gap",
		"why_not_answering",
		"<<QUESTION>>",
		"Do not repeat a final answer",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("direct implementation recovery prompt missing %q: %q", want, prompt)
		}
	}

	if strings.Contains(prompt, "submit_plan") {
		t.Fatalf("direct implementation recovery must not point at planner-phase submit_plan: %q", prompt)
	}

	for _, forbidden := range []string{"Current evidence paths", `"path":"candidate/path.go"`} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("context_pack-only recovery should not treat packet candidates as concrete targets (%q): %q", forbidden, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptForUnscopedImplementationNoWriteBecomesTerminalAfterRepeatedGate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-120 (total lines: 332)",
			Output:  "1:package app\n2:func renderMessages() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/model_state.go lines 1-120 (total lines: 1456)",
			Output:  "1:package app\n2:type modelState struct{}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatal("expected terminal no-write recovery prompt")
	}

	if !hideReads {
		t.Fatal("repeated no-write completion after concrete source evidence should hide reads")
	}

	for _, want := range []string{
		"multiple no-write final answers",
		"Read/search/discovery tools are hidden",
		"parser-readable write tool call",
		"internal/surfaces/tui/app/model_state.go",
		"`<<QUESTION>>:`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("terminal no-write recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, notWant := range []string{"If one edit-safety fact", "`read_file`", "`search_content`"} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("terminal no-write recovery prompt should not invite more discovery with %q:\n%s", notWant, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptForDirectRequiredPathImplementationNoWrite(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-220 (total lines: 220)"},
		{Tool: "context_query", Summary: "Answered query for ValidatePRDTDDObligationCrossLinks"},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for direct required-path implementation")
	}

	if hideReads {
		t.Fatalf("semantic required-path implementation recovery should leave bounded reads visible")
	}

	for _, want := range []string{
		"required edit path(s) remain unchanged",
		"internal/workflows/productworkflow/coverage_gate.go",
		"internal/workflows/productworkflow/coverage_gate_test.go",
		"parser-readable action",
		"not prose",
		"Hidden-only reasoning is not completion evidence",
		"edit_file",
		"write_file",
		"append_file",
		"apply_patch",
		"Prefer the next unread source-body request now",
		`"tool":"read_file"`,
		`"path":"internal/workflows/productworkflow/coverage_gate_test.go"`,
		"named_gap",
		"why_not_answering",
		"semantic/context lookup",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("direct required-path recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, "submit_plan") {
		t.Fatalf("direct implementation recovery must not point at planner-phase submit_plan:\n%s", prompt)
	}
}

func TestCompletionGateRecoveryPromptUsesSearchContentEvidencePath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, _, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 24 matching line(s).",
			Output:  "internal/surfaces/tui/app/rendering.go:50: if banner := m.errorBanner(); banner != \"\" {\ninternal/frontier/locate.go:12: banner",
		},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}

	for _, want := range []string{
		"Current evidence paths",
		"internal/surfaces/tui/app/rendering.go",
		`"tool":"read_file"`,
		`"path":"internal/surfaces/tui/app/rendering.go"`,
		"Do not call `context_pack` again",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("search-content recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, `"tool":"context_pack"`) {
		t.Fatalf("search-content recovery should not request context_pack:\n%s", prompt)
	}
}

func TestCompletionGateRecoveryPromptFallsBackAfterContextPackFailure(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, _, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "context_pack failed", Error: "index unavailable"},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}

	for _, want := range []string{
		"do not call `context_pack` again",
		"bounded fallback discovery phase",
		"`search_files`",
		"`search_content`",
		"If a literal phrase missed, broaden",
		"`read_file`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("context fallback recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, `"tool":"context_pack"`) {
		t.Fatalf("context_pack failure recovery should not ask for another context_pack call:\n%s", prompt)
	}
}
