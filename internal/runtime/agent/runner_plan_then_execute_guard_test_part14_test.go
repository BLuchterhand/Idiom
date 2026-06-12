package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestPlanThenExecuteCandidatePostGateDoesNotTreatContextPackAsScopeBoundary(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_evidence.go"},{"path":"internal/workflows/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read p912EvaluatePriorGateEvidence in cmd/idiom/product_evidence.go lines 1-120\n1:func p912EvaluatePriorGateEvidence() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "file_outline",
		Path:       "cmd/idiom/product_workflow.go",
	}, brief, results)
	if reject {
		t.Fatal("context-pack candidates are evidence hints, not an open-candidate scope boundary")
	}
}

func TestPlanThenExecuteCandidatePostGateAllowsTypedCandidateRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_workflow.go"},{"path":"internal/workflows/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 900-1080\n900:func runProductTDD() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   180,
	}, brief, results)

	if reject {
		t.Fatal("post-gate bounded read of a typed candidate should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsRejectedAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-360 (total lines: 360)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `internal/workflows/productworkflow/coverage_gate.go: old_text not found in current source`.",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
		Capability: "read_symbol",
		Pattern:    "EvaluateCoverageGate",
	}, brief, results)
	if reject {
		t.Fatal("candidate-only invalid-anchor rejection should allow one bounded source refresh before another submit_plan")
	}
}

func TestPlanThenExecuteCandidatePostReadinessNamesMissingAnchorCapability(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-360 (total lines: 360)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}

	summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "code_navigate",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatal("code_navigate anchor refresh without capability should be rejected with a targeted correction")
	}

	if summary != "Anchor refresh capability required" {
		t.Fatalf("unexpected summary %q", summary)
	}

	for _, want := range []string{"source-body capability", "read_symbol", "concrete symbol/pattern", "invalid edit anchor"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("targeted rejection missing %q:\n%s", want, errorText)
		}
	}

	if strings.Contains(errorText, `{"tool":"code_navigate"`) {
		t.Fatalf("targeted rejection must not include copyable JSON request:\n%s", errorText)
	}

	if strings.Contains(errorText, "Allowed paths are not required coverage") {
		t.Fatalf("missing capability should not use the generic candidate-ready rejection:\n%s", errorText)
	}
}

func TestPlanThenExecuteCandidatePostReadinessDoesNotPromoteAllowedPathsToSubmitCoverage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:            "a.txt",
			InsertAfterLine: 1,
			Content:         "added\n",
			Purpose:         "add needed behavior",
		}},
	})

	if got.Intercepted {
		t.Fatalf("allowed candidate scope must constrain paths without requiring coverage for every allowed path: %#v", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeStandsDownAfterSubmit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("nudge must stand down once submit_plan succeeded; got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeIgnoresOtherLanes(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("nudge must only fire under plan_then_execute_planner; got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeRecognizesLanguageSymbolReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"native/src/foo.cpp"},
	}
	results := []workspace.ToolResult{
		// cpp_read_symbol summary shape: "Read <symbol> in <path> lines A-B"
		{Tool: "cpp_read_symbol", Summary: "Read MyClass::method in native/src/foo.cpp lines 10-30"},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got == "" {
		t.Fatalf("nudge should recognize language-symbol read summaries as covering the path")
	}
}

func TestCanonicalMutationBlockerFinalAnswerExemptsPlanExecute(t *testing.T) {
	// Decorator-side exemption: for plan-execute planner phase, the
	// model's clean "plan submitted" answer must not be rewritten into
	// a "required edit paths remain unchanged" blocker just because the
	// planner phase has no changed_paths. The executor phase (separate
	// process) produces the file changes.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "submit_plan", Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them."},
	}
	got, decorated := canonicalMutationBlockerFinalAnswer(brief, results, "Plan submitted with one edit.")

	if decorated {
		t.Fatalf("decorator must NOT synthesize a blocker for plan-execute with a successful submit_plan; got %q", got)
	}
}

func TestCanonicalMutationBlockerFinalAnswerStillFiresForRegularImplementation(t *testing.T) {
	// Sanity check: the plan-execute exemption must not weaken the
	// decorator for normal implementation lanes.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt"},
	}
	got, decorated := canonicalMutationBlockerFinalAnswer(brief, nil, "Done.")

	if !decorated {
		t.Fatalf("regular implementation lane with no edits should be decorated to a blocker; got %q", got)
	}

	if !strings.Contains(got, "Required edit path") {
		t.Fatalf("blocker should reference required edit paths; got %q", got)
	}
}

func TestPlanThenExecuteShortCircuitAnswerFiresAfterSuccessfulSubmit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them.",
		Output:  "plan_path: /tmp/plans/x.plan.json\nedits: 2\nschema_version: " + workspace.PlanArtifactSchemaVersion,
	}
	answer, ok := planThenExecuteShortCircuitAnswer(brief, req, result)

	if !ok {
		t.Fatalf("short-circuit must fire after successful submit_plan; got !ok")
	}

	if !strings.Contains(answer, "Plan submitted with 2 edit(s)") {
		t.Fatalf("synthetic answer must include the result summary; got %q", answer)
	}

	if !strings.Contains(answer, "executor phase") {
		t.Fatalf("synthetic answer must reference the executor phase; got %q", answer)
	}
}

func TestPlanThenExecuteShortCircuitSkipsErrors(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	failure := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "submit_plan requires at least one edit in the edits array",
	}

	if _, ok := planThenExecuteShortCircuitAnswer(brief, req, failure); ok {
		t.Fatalf("short-circuit must NOT fire on a failed submit_plan — let the runner recover normally")
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSynthesizeSourceSatisfiedAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		Objective:         "reject temp symlinks and add regression tests proving no partial mutation",
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "internal/workflows/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
			{Path: "internal/workflows/productworkflow/migration_apply_test.go", OldText: "same", NewText: "same", Purpose: "test"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; edits[1]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, nil); ok {
		t.Fatalf("rejected no-op submit_plan must remain an invalid attempt, got synthesized answer:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedSynthesizesAnswerWithRequiredEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision_test.go"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The required test file already contains the complete, correct test implementations, satisfying the source-satisfied condition with per-path evidence.",
		Edits:     []workspace.PlanEdit{},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `internal/contracts/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/contracts/writerroute/decision_test.go lines 1-80 (total lines: 80)",
			Output:  "package writerroute\n\nfunc TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("empty submit_plan must remain invalid even with source-satisfied rationale:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedAcceptsExistingFileRationale(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision_test.go"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The existing decision_test.go already implements the required normalization, EventFields, and JSON mapping tests matching the spec, so the file satisfies the objective.",
		Edits:     []workspace.PlanEdit{},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `internal/contracts/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/contracts/writerroute/decision_test.go lines 1-200 (total lines: 200)",
			Output:  "package writerroute\n\nfunc TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("existing-file rationale inside empty submit_plan must remain invalid:\n%s", answer)
	}
}

func TestPlanExecuteRunnerRedirectsEmptySubmitPlanSourceSatisfiedToTerminalAnswer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"submit_plan","decision":"source-satisfied","rationale":"The current source already contains the requested final state and is source-satisfied; no changes are needed.","edits":[]}`,
		"Decision: source-satisfied\n- a.txt: current source already contains the requested final state.",
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

	var (
		sawShortCircuit     bool
		sawTerminalRedirect bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_source_satisfied_short_circuit" {
				sawShortCircuit = true
			}

			if event.Role == "plan_then_execute_source_satisfied_terminal_required" {
				sawTerminalRedirect = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("runner should require an explicit terminal answer after source-satisfied submit_plan, got %d request(s)", requests)
	}

	if sawShortCircuit {
		t.Fatal("empty submit_plan must not short-circuit as source-satisfied")
	}

	if !sawTerminalRedirect {
		t.Fatal("source-satisfied submit_plan rejection should trigger terminal-shape recovery")
	}

	for _, want := range []string{"Decision: source-satisfied", "a.txt"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q:\n%s", want, answer)
		}
	}
}

func TestPlanExecuteRunnerRedirectsSourceSatisfiedAfterNoopSubmitPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"submit_plan","rationale":"No-op mistake for already-satisfied path.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"before\n","purpose":"leave existing behavior"}]}`,
		`{"tool":"submit_plan","decision":"source-satisfied","rationale":"The current source already contains the requested final state and is source-satisfied; no changes are needed.","edits":[]}`,
		"Decision: source-satisfied\n- a.txt: current source already contains the requested final state.",
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

	var sawTerminalRedirect bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_source_satisfied_terminal_required" {
				sawTerminalRedirect = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("runner should require an explicit terminal answer after source-satisfied submit_plan, got %d request(s)", requests)
	}

	if !sawTerminalRedirect {
		t.Fatal("source-satisfied submit_plan rejection should trigger terminal-shape recovery after no-op submit_plan")
	}

	for _, want := range []string{"Decision: source-satisfied", "a.txt"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q:\n%s", want, answer)
		}
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedRequiresAnchorReadyEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision_test.go"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The current source is source-satisfied and no changes are needed.",
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `internal/contracts/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/contracts/writerroute/decision_test.go lines 1-20 (total lines: 120)",
			Output:  "package writerroute\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("body-window evidence must not synthesize source-satisfied closeout:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedSkipsRequiredSnippetBriefs(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision_test.go"},
		RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
			{Path: "internal/contracts/writerroute/decision_test.go", Content: "func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}"},
		},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The required test file already contains the complete, correct test implementations, satisfying the source-satisfied condition with per-path evidence.",
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `internal/contracts/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/contracts/writerroute/decision_test.go lines 1-80 (total lines: 80)",
			Output:  "package writerroute\n\nfunc TestDifferentBehavior(t *testing.T) {}\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("empty submit_plan repair must not bypass required_edit_snippets:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanWithoutSourceSatisfiedIntentStaysRejected(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "I could not decide the edit shape.",
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `a.txt`.",
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.txt lines 1-1 (total lines: 1)", Output: "before\n"}}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("empty submit_plan without source-satisfied intent must not synthesize closeout:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanNegatedSourceSatisfiedRationaleStaysRejected(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `a.txt`.",
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.txt lines 1-1 (total lines: 1)", Output: "before\n"}}

	for _, rationale := range []string{
		"The current source is not source-satisfied because it does not contain the requested final state.",
		"The current source is not yet source-satisfied; it still needs edits to reach the requested final state.",
		"The current source is not yet source-satisfied; no changes were chosen.",
		"The required path source-satisfied check requires edits before the final state is complete.",
	} {
		req := workspace.ToolRequest{
			Tool:      "submit_plan",
			Rationale: rationale,
		}
		if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
			t.Fatalf("negated source-satisfied rationale must not synthesize closeout for %q:\n%s", rationale, answer)
		}
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSourceSatisfyUnvalidatedRequiredPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Objective:  "fix product coverage gate regressions and tests",
		RequiredEditPaths: []string{
			"cmd/idiom/product_workflow.go",
			"cmd/idiom/product_workflow_test.go",
			"internal/workflows/productworkflow/coverage_gate.go",
			"internal/workflows/productworkflow/coverage_gate_test.go",
		},
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "before", NewText: "after", Purpose: "source"},
			{Path: "internal/workflows/productworkflow/coverage_gate.go", OldText: "same", NewText: "same", Purpose: "source"},
			{Path: "cmd/idiom/product_workflow_test.go", OldText: "before", NewText: "after", Purpose: "test"},
			{Path: "internal/workflows/productworkflow/coverage_gate_test.go", OldText: "before", NewText: "after", Purpose: "test"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[1]: edit would not change internal/workflows/productworkflow/coverage_gate.go; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f3 internal/workflows/productworkflow/coverage_gate.go lines 1-337"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProduct in cmd/idiom/product_workflow.go lines 1-80"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProduct in cmd/idiom/product_workflow_test.go lines 1-80"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverage in internal/workflows/productworkflow/coverage_gate_test.go lines 1-80"},
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("single no-op rejected edit must not close all required paths as source-satisfied, got:\n%s", answer)
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSourceSatisfyMissingRequestedTestEdit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Objective:  "reject temp symlinks and add regression tests proving no partial mutation",
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "internal/workflows/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, nil); ok {
		t.Fatalf("source-only no-op must not close a request that asked for tests, got:\n%s", answer)
	}
}

func TestReviewRepairCompletionGateRecoveryWithSourceSanityFailureAllowsBoundedEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"source_sanity_failed:internal/workflows/productworkflow/coverage_gate.go"},
			},
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
	})

	if !ok {
		t.Fatal("review-repair completion rejection should produce a phase-specific recovery prompt")
	}

	if hideReads {
		t.Fatal("source_sanity repair should allow one bounded evidence read before terminal closeout")
	}

	for _, want := range []string{
		"source_sanity_failed:internal/workflows/productworkflow/coverage_gate.go",
		"one bounded source-read action",
		"existing type shape",
		"corrective `submit_plan`",
		"`Decision: pass` is invalid",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair source_sanity prompt missing %q:\n%s", want, prompt)
		}
	}

	_, hideReads, ok = runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
	})
	if !ok {
		t.Fatal("repeated review-repair completion rejection should still produce recovery")
	}

	if !hideReads {
		t.Fatal("repeated review-repair closeout rejection should return to terminal-only recovery")
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSynthesizeFromReadTestEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The objective is to add regression tests proving no partial mutation; the existing test already covers it.",
		Edits: []workspace.PlanEdit{
			{Path: "internal/workflows/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 internal/workflows/productworkflow/migration_apply_test.go lines 1-537"},
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("read test evidence must not turn a rejected no-op plan into success:\n%s", answer)
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSynthesizeWhenObjectiveSummaryIsSparse(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "source no-op",
		Edits: []workspace.PlanEdit{
			{Path: "internal/workflows/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 internal/workflows/productworkflow/migration_apply_test.go lines 1-537"},
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("sparse objective no-op must not turn a rejected no-op plan into success:\n%s", answer)
	}
}

func TestPlanThenExecuteShortCircuitSkipsNonPlanExecuteLanes(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	result := workspace.ToolResult{Tool: "submit_plan", Summary: "ok"}

	if _, ok := planThenExecuteShortCircuitAnswer(brief, req, result); ok {
		t.Fatalf("short-circuit must only fire under plan_then_execute_planner; got ok")
	}
}

func TestPlanThenExecuteShortCircuitSkipsOtherTools(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	req := workspace.ToolRequest{Tool: "read_file"}
	result := workspace.ToolResult{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"}

	if _, ok := planThenExecuteShortCircuitAnswer(brief, req, result); ok {
		t.Fatalf("short-circuit must only fire on submit_plan; got ok for %q", req.Tool)
	}
}

func TestReviewRepairDoesNotAppendExternalTools(t *testing.T) {
	if runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		PermissionProfile: "plan_submission_review",
	}) {

		t.Fatalf("review-repair must keep the exact phase tool surface and not append project externals")
	}

	if !runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
	}) {

		t.Fatalf("planner phase should still inherit project discovery externals")
	}

	if runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "verification_only",
		PermissionProfile: "read_only_verification_shell",
	}) {

		t.Fatalf("verification-only lanes must not widen their shell/check surface with project externals")
	}
}

func TestReviewRepairInitialTextualSurfaceAllowsTargetedProbeTools(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		PermissionProfile: "plan_submission_review",
		AllowedTools:      []string{"read_file", "code_navigate", "context_status", "context_query", "context_zoom", "context_pack", "submit_plan"},
	}
	got := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, nil)
	assertContains(t, got, "read_file")
	assertContains(t, got, "code_navigate")
	assertContains(t, got, "context_status")
	assertContains(t, got, "submit_plan")
	assertNotContains(t, got, "context_pack")

	terminal := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error:   "review-repair must close",
	}})
	assertEqualSet(t, terminal, []string{"submit_plan"})
}

func TestImplementationScopedEditAllowsSubmitPlan(t *testing.T) {
	// The scoped-edit tool surface filter trims the brief's
	// allowed_tools to write tools + read_file + code_navigate. Without
	// an explicit case, submit_plan is filtered out (it isn't in
	// IsWorkspaceWriteTool's list because it doesn't directly mutate
	// files), which leaves the planner phase with only read tools and
	// no way to submit a plan. The first three plan-execute Colony runs
	// failed for a chain of related reasons, this filter being the
	// last layer.
	if !implementationScopedEditToolAllowed("submit_plan") {
		t.Fatalf("scoped-edit surface must allow submit_plan; otherwise plan-execute lanes lose their write tool")
	}

	if !implementationPreWriteToolAllowed("submit_plan") {
		t.Fatalf("pre-write surface must allow submit_plan; otherwise exact-anchor pre-write hides the planner's write")
	}

	for _, tool := range []string{"edit_file", "apply_patch", "read_file", "code_navigate", "submit_plan", "context_query", "cpp_find_symbol", "cpp_definition", "source_sanity"} {
		got := implementationScopedEditAllowedToolsWithRegistry([]string{tool}, nil)
		if len(got) != 1 || got[0] != tool {
			t.Fatalf("scoped-edit allowlist should pass %q through, got %v", tool, got)
		}
	}

	for _, tool := range []string{"shell", "git_status", "task", "search_content", "list_files", "context_index"} {
		got := implementationScopedEditAllowedToolsWithRegistry([]string{tool}, nil)
		if len(got) != 0 {
			t.Fatalf("scoped-edit allowlist should drop %q, got %v", tool, got)
		}
	}

	reg, err := workspace.NewExternalToolRegistry([]workspace.ExternalTool{
		{Name: "trace_code", Command: "true", ReadOnly: true, Discovery: true},
		{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: false},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}

	got := implementationScopedEditAllowedToolsWithRegistry([]string{"trace_code", "queue_run", "edit_file"}, reg)
	want := []string{"trace_code", "edit_file"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scoped-edit allowlist should keep declared read-only discovery externals only\nwant %v\n got %v", want, got)
	}
}

func TestPlanExecuteSubmitPlanCountsAsWriteDuringWriteOnlyRecovery(t *testing.T) {
	r := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}}
	if !r.writeOnlyRecoveryToolIsWrite("submit_plan") {
		t.Fatal("plan-execute write-only recovery must treat submit_plan as the planner write contract")
	}

	reviewRepair := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecuteReviewRepairTaskFamily}}
	if !reviewRepair.writeOnlyRecoveryToolIsWrite("submit_plan") {
		t.Fatal("review-repair terminal recovery must treat submit_plan as the corrective-plan contract")
	}

	normal := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}}
	if normal.writeOnlyRecoveryToolIsWrite("submit_plan") {
		t.Fatal("submit_plan should not be treated as a normal implementation write tool outside plan-execute planner phase")
	}
}

func TestPlanExecuteRecoveryRejectsNonSubmitPlanWithoutTerminalWriteOnlyBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		"Done.",
		`{"tool":"search_content","path":".","pattern":"before"}`,
		"<<QUESTION>>: exact blocker: no safe edit body is available for a.txt or b.txt.",
	}

	var (
		requests              int
		sawSubmitPlanRequired bool
	)

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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_submit_plan_required" {
				sawSubmitPlanRequired = true
			}

			if event.Role == "completion_status" && strings.Contains(event.Content, "write_only_recovery_required_paths_remaining") {
				t.Fatalf("plan-execute recovery should not terminate on first non-submit_plan tool: %s", event.Content)
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected recovery to continue after non-submit_plan tool, got %d request(s)", requests)
	}

	if !sawSubmitPlanRequired {
		t.Fatalf("expected plan_then_execute_submit_plan_required event")
	}

	if !strings.Contains(answer, "exact blocker") {
		t.Fatalf("expected final blocker answer after recovery, got:\n%s", answer)
	}
}
