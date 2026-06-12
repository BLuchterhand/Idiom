package agent

import (
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

func TestPlanThenExecuteWriteGuardRejectsEditFile(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{
		Tool: "edit_file",
		Path: "a.txt",
	})

	if !got.Intercepted {
		t.Fatalf("planner phase must reject edit_file; got %#v", got)
	}

	if got.Result.Tool != "edit_file" {
		t.Fatalf("rejection should keep the tool name (so the audit trail shows what was blocked), got %q", got.Result.Tool)
	}

	if !strings.Contains(got.Result.Error, "submit_plan") {
		t.Fatalf("rejection must point at submit_plan, got %q", got.Result.Error)
	}

	if !strings.Contains(got.Result.Error, "executor phase") {
		t.Fatalf("rejection should explain the executor will dispatch later, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteLoopBudgetRecoveryRequiresTerminalOutcome(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-10 (total lines: 20)",
		Output:  "1:package productworkflow\n2:\n3:func EvaluateCoverageGate() {}\n",
	}}
	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
		Capability: "definition",
		Pattern:    "EvaluateCoverageGate",
	}, results, "code_navigate repeated after warning", false)

	if !ok {
		t.Fatal("expected loop-budget recovery once source evidence makes terminal planning available")
	}

	if !recovery.TerminalOnly {
		t.Fatalf("single-candidate loop-budget recovery should force terminal outcome:\n%s", recovery.Prompt)
	}

	for _, want := range []string{"loop budget", "submit_plan", "Decision: source-satisfied", "<<QUESTION>>"} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, recovery.Prompt)
		}
	}

	if strings.Contains(recovery.Prompt, "perform one bounded read") {
		t.Fatalf("loop-budget terminal recovery must not ask for another read:\n%s", recovery.Prompt)
	}
}

func TestPlanThenExecuteWriteGuardRejectsAllPerEditWrites(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	for _, tool := range []string{
		"edit_file", "write_file", "append_file", "apply_patch",
		"delegate_edit", "go_move_declarations", "mkdir",
	} {
		got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: tool})
		if !got.Intercepted {
			t.Fatalf("planner phase must reject %q, got %#v", tool, got)
		}
	}
}

func TestPlanThenExecuteWriteGuardLetsSubmitPlanThrough(t *testing.T) {
	// submit_plan is the planner's ONE write — without this exemption
	// the guard would intercept it and the lane could never produce a
	// plan.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{
		Tool: "submit_plan",
	})

	if got.Intercepted {
		t.Fatalf("submit_plan must pass through during planner phase, got %#v", got)
	}
}

func TestMLXPlanThenExecuteAdvertisesTextualSubmitPlanProtocol(t *testing.T) {
	t.Skip("MLX now supports native tools; this validates the old textual protocol")

	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
	}
	brief := workerruntime.NormalizeRuntimeBrief(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"submit_plan", "read_file"},
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt"},
	})
	got := runner.textualToolProtocolPrompt(brief)

	for _, want := range []string{
		"# Textual Tool Call Protocol",
		"exactly one JSON object",
		"top-level `tool`, `rationale`, and `edits`",
		"`edits` must be an array",
		"never copy placeholders",
		"schema examples",
		"ellipses",
		"`existing code` comments",
		"no prose",
		"XML tags",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in textual protocol prompt:\n%s", want, got)
		}
	}

	if !strings.Contains(got, "submit_plan") || !strings.Contains(got, "read_file") {
		t.Fatalf("prompt should include required tool first and bounded allowed examples:\n%s", got)
	}

	for _, forbidden := range []string{"relative/file", "exact current substring", "replacement text", "one paragraph on the overall plan"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("textual protocol prompt must not include copyable placeholder %q:\n%s", forbidden, got)
		}
	}
}

func TestNativeToolBackendSkipsTextualToolProtocolPrompt(t *testing.T) {
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama),
	}
	brief := workerruntime.NormalizeRuntimeBrief(workerruntime.WorkerRuntimeBrief{
		AllowedTools:  []string{"submit_plan"},
		RequiredTools: []string{"submit_plan"},
	})

	if got := runner.textualToolProtocolPrompt(brief); got != "" {
		t.Fatalf("native-tool backend should not receive duplicate textual protocol prompt:\n%s", got)
	}
}

func TestPlanExecuteCompletionGateRecoveryKeepsSubmitPlanBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	got := completionGateRecoveryChatOptions(llama.ChatOptions{MaxTokens: 16384}, brief, nil)

	if got.MaxTokens != 768 {
		t.Fatalf("plan-execute source-read recovery should cap to one tool call, got %d", got.MaxTokens)
	}

	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"}}

	got = completionGateRecoveryChatOptions(llama.ChatOptions{MaxTokens: 16384}, brief, results)

	if got.MaxTokens != 4096 {
		t.Fatalf("plan-execute existing-file recovery should cap submit_plan budget, got %d", got.MaxTokens)
	}

	got = completionGateRecoveryChatOptions(llama.ChatOptions{}, brief, results)
	if got.MaxTokens != 4096 {
		t.Fatalf("plan-execute existing-file recovery should default to capped submit_plan budget, got %d", got.MaxTokens)
	}
}

func TestPlanExecuteSourceSatisfiedTerminalRecoveryChatOptionsHideSubmitPlanTool(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)", Output: "before\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}
	opts := llama.ChatOptions{
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice:         "required",
		ForceNativeTools:   true,
		ResponseFormatJSON: true,
	}

	got := planThenExecuteReadyToSubmitChatOptionsForBackend(opts, brief, results, true)
	if len(got.Tools) != 0 || strings.TrimSpace(got.ToolChoice) != "" || got.ForceNativeTools || got.ResponseFormatJSON {
		t.Fatalf("source-satisfied terminal recovery must not force submit_plan transport: %#v", got)
	}

	if got.EnableThinking == nil || *got.EnableThinking {
		t.Fatalf("source-satisfied terminal recovery should force visible answer mode: %#v", got.EnableThinking)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsMissingRequiredPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})

	if !got.Intercepted {
		t.Fatalf("submit_plan missing a required path must be rejected")
	}

	if !strings.Contains(got.Result.Error, "b.txt") {
		t.Fatalf("rejection should name the missing path, got %q", got.Result.Error)
	}

	details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(got.Result)
	if !ok {
		t.Fatalf("rejection should include typed coverage details, got output %q", got.Result.Output)
	}

	if len(details.MissingRequiredPaths) != 1 || details.MissingRequiredPaths[0] != "b.txt" {
		t.Fatalf("typed coverage details should name missing path, got %#v", details.MissingRequiredPaths)
	}

	if !strings.Contains(got.Result.Error, "corrected plan") {
		t.Fatalf("rejection should be recoverable, got %q", got.Result.Error)
	}

	for _, forbidden := range []string{"Decision: source-satisfied", "<<QUESTION>>"} {
		if strings.Contains(got.Result.Error, forbidden) {
			t.Fatalf("coverage rejection advertised stale terminal exit %q: %q", forbidden, got.Result.Error)
		}
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardEnforcesAllowedScopeWithoutCoverage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})

	if got.Intercepted {
		t.Fatalf("allowed_edit_paths are scope only, not coverage obligations: %#v", got)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "c.txt", OldText: "c", NewText: "C"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan outside allowed_edit_paths must be rejected")
	}

	if !strings.Contains(got.Result.Error, "outside allowed_edit_paths") || !strings.Contains(got.Result.Error, "c.txt") {
		t.Fatalf("allowed-scope rejection should name the outside path, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteUngroundedSubmitPlanRejectsUnscopedExistingFileEditWithoutAnchor(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:       "target.txt",
			ReplaceAll: true,
			NewText:    "new\n",
			Purpose:    "replace target",
		}},
	}, nil)

	if !got.Intercepted {
		t.Fatal("unscoped submit_plan must require source-body evidence for existing-file edits")
	}

	if !strings.Contains(got.Result.Error, "target.txt") || !strings.Contains(got.Result.Error, "source-body evidence") {
		t.Fatalf("grounding rejection should name the path and evidence requirement, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteUngroundedSubmitPlanAllowsSourceReadEdit(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "target.txt",
			OldText: "old\n",
			NewText: "new\n",
			Purpose: "update target",
		}},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read target.txt lines 1-1 (total lines: 1)",
		Output:  "old\n",
	}})

	if got.Intercepted {
		t.Fatalf("source-body evidence should ground the unscoped submit_plan edit, got %#v", got)
	}
}

func TestPlanThenExecuteScopedSubmitPlanRequiresGroundingBeyondPathScope(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:       "target.txt",
			ReplaceAll: true,
			NewText:    "new\n",
			Purpose:    "replace target",
		}},
	}, nil)

	if !got.Intercepted {
		t.Fatal("required path scope alone must not ground an existing-file submit_plan edit")
	}

	if !strings.Contains(got.Result.Error, "target.txt") {
		t.Fatalf("grounding rejection should name target.txt, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteScopedSubmitPlanExactAnchorGroundsEdit(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "target.txt",
			OldText: "old\n",
			NewText: "new\n",
			Purpose: "update target",
		}},
	}, nil)

	if got.Intercepted {
		t.Fatalf("exact current-source old_text anchor should ground scoped submit_plan edit, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCompleteNewFileBodyGroundsEdit(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"new_target.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:       "new_target.txt",
			ReplaceAll: true,
			NewText:    "created\n",
			Purpose:    "create target",
		}},
	}, nil)

	if got.Intercepted {
		t.Fatalf("complete new-file body should ground submit_plan edit, got %#v", got)
	}
}

func TestPlanThenExecuteUnscopedSubmitPlanCoverageGuardRejectsInvalidAnchor(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "target.txt",
			OldText: "missing\n",
			NewText: "new\n",
			Purpose: "update target",
		}},
	})

	if !got.Intercepted {
		t.Fatal("unscoped submit_plan must still validate executable current-source anchors")
	}

	if !strings.Contains(got.Result.Error, "old_text not found") {
		t.Fatalf("expected invalid-anchor rejection, got %q", got.Result.Error)
	}
}

func TestDeterministicPlanExecuteGroundingRecoveryReadsRejectedMissingSourcePath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   PlanThenExecutePlannerTaskFamily,
			Objective:    "expose read-only product workflow status and effort browsing through idiom tool direct-tool surface",
			AllowedTools: []string{"context_pack", "search_content", "product_efforts", "product_status"},
		},
	}
	req, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("I need more evidence.", []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "missing source-body evidence for `cmd/idiom/main.go`",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go","role":"editable"}]}`,
		},
	})

	if !ok {
		t.Fatal("expected deterministic grounding recovery after ungrounded submit_plan and context_pack")
	}

	if req.Tool != "read_file" || req.Path != "cmd/idiom/main.go" || req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("expected bounded source read for rejected missing-source path, got %#v", req)
	}
}

func TestDeterministicPlanExecuteGroundingRecoveryPrioritizesRejectedMissingSourcePathOverSearchHit(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			Objective:  "expose read-only product workflow status and effort browsing through idiom tool direct-tool surface",
		},
	}
	req, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("Still not ready.", []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "missing source-body evidence for `cmd/idiom/main.go`",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go","role":"editable"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Search results",
			Output:  "internal/capabilities/workspace/product_tools.go:12:func (m *Manager) productEfforts(req ToolRequest) ToolResult {\ninternal/runtime/agent/runner.go:10:plan-execute product workflow",
		},
	})

	if !ok {
		t.Fatal("expected deterministic grounding recovery to read the rejected missing-source path")
	}

	if req.Tool != "read_file" || req.Path != "cmd/idiom/main.go" {
		t.Fatalf("expected rejected missing-source path read before broad fallback, got %#v", req)
	}
}

func TestDeterministicPlanExecuteGroundingRecoveryDoesNotStealTerminalReply(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			Objective:  "expose read-only product workflow status and effort browsing through idiom tool direct-tool surface",
		},
	}
	_, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("Decision: source-satisfied\nPath: internal/capabilities/workspace/product_tools.go", []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "missing source-body evidence for `cmd/idiom/main.go`",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go","role":"editable"}]}`,
		},
	})

	if ok {
		t.Fatal("terminal source-satisfied replies should go to the completion gate, not deterministic read recovery")
	}
}

func TestDeterministicPlanExecuteAnchorRefreshOverridesTerminalReply(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Objective:         "retry rejected plan with refreshed anchors",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	req, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest(
		"<<QUESTION>>: exact blocker: a.txt and b.txt cannot be updated from the rejected submit_plan evidence.",
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 a.txt lines 80-120 (total lines: 120)",
				Output:  "80:before\n120:end\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f2 b.txt lines 1-20 (total lines: 20)",
				Output:  "1:before\n20:end\n",
			},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.txt`; invalid edit anchor(s): `a.txt: old_text not found in current source`.",
			},
		},
	)

	if !ok {
		t.Fatal("anchor-refresh terminal-shaped replies must still trigger deterministic source refresh")
	}

	if req.Tool != "read_file" || req.Path != "a.txt" || req.StartLine != 80 || req.EndLine != 120 {
		t.Fatalf("expected bounded reread of prior invalid-anchor range, got %#v", req)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsPlaceholderBodiesWithTypedPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.go",
			OldText: "func run() {\n}\n",
			NewText: "func run() {\n\t// ... existing code ...\n}\n",
			Purpose: "update run",
		}},
	})

	if !got.Intercepted {
		t.Fatalf("submit_plan placeholder body must be rejected")
	}

	if !strings.Contains(got.Result.Error, "placeholder edit body path") || !strings.Contains(got.Result.Error, "a.go") {
		t.Fatalf("rejection should name placeholder path, got %q", got.Result.Error)
	}

	details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(got.Result)
	if !ok {
		t.Fatalf("rejection should include typed coverage details, got output %q", got.Result.Output)
	}

	if len(details.PlaceholderEditBodies) != 1 || !strings.Contains(details.PlaceholderEditBodies[0], "a.go") {
		t.Fatalf("typed coverage details should name placeholder path, got %#v", details.PlaceholderEditBodies)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsEditWithoutPathExplicitly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{OldText: "a", NewText: "A", Purpose: "patch a required file"},
		},
	})

	if !got.Intercepted {
		t.Fatalf("submit_plan edit without path must be rejected")
	}

	for _, want := range []string{
		"edit entry missing `path`",
		"edits[0] patch a required file",
		"`edits` as a JSON array, not a quoted string",
		"complete executable source text",
		"not placeholders, ellipses, or prose",
	} {
		if !strings.Contains(got.Result.Error, want) {
			t.Fatalf("rejection should contain %q, got %q", want, got.Result.Error)
		}
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsOutsideRequiredPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "surprise.txt", OldText: "x", NewText: "X"},
		},
	})

	if !got.Intercepted {
		t.Fatalf("submit_plan outside required paths must be rejected")
	}

	if !strings.Contains(got.Result.Error, "surprise.txt") {
		t.Fatalf("rejection should name the outside path, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAcceptsFullCoverage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "b.txt", OldText: "b", NewText: "B"},
		},
	})

	if got.Intercepted {
		t.Fatalf("submit_plan with full required path coverage must pass, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardTreatsRequiredDirectoryAsScope(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}

		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{
				"cmd/idiom",
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "package main", NewText: "package main\n// updated"},
			{Path: "cmd/idiom/product_workflow_test.go", OldText: "package main", NewText: "package main\n// covered"},
		},
	})

	if got.Intercepted {
		t.Fatalf("submit_plan should not require an edit entry for the directory scope, got %#v", got)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "package main", NewText: "package main\n// updated"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan missing the concrete required test file must still be rejected")
	}

	details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(got.Result)
	if !ok {
		t.Fatalf("rejection should include typed coverage details, got output %q", got.Result.Output)
	}

	if slices.Contains(details.MissingRequiredPaths, "cmd/idiom") {
		t.Fatalf("directory scope must not be reported as missing file coverage, got %#v", details.MissingRequiredPaths)
	}

	if !slices.Contains(details.MissingRequiredPaths, "cmd/idiom/product_workflow_test.go") {
		t.Fatalf("missing concrete file coverage should still be reported, got %#v", details.MissingRequiredPaths)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAllowsDirectoryScopeChildrenOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "idiom"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/idiom: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/new_feature.go", Content: "package main\n"},
		},
	})

	if got.Intercepted {
		t.Fatalf("submit_plan child edit under directory scope should pass, got %#v", got)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "docs/out-of-scope.md", Content: "outside\n"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan outside directory scope must be rejected")
	}

	if !strings.Contains(got.Result.Error, "docs/out-of-scope.md") {
		t.Fatalf("outside-scope rejection should name the path, got %q", got.Result.Error)
	}
}

func TestPlanExecuteRemainingRequiredEditPathsExcludeDirectoryScope(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}

		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{
				"cmd/idiom",
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	}
	missing := runner.remainingRequiredEditPaths(nil)

	if slices.Contains(missing, "cmd/idiom") {
		t.Fatalf("directory scope must not appear as a remaining required file, got %#v", missing)
	}

	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if !slices.Contains(missing, want) {
			t.Fatalf("missing required file %q, got %#v", want, missing)
		}
	}
}

func TestPlanExecuteUnreadRequiredPathsExcludeParentDirectoryScope(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{
			"cmd/idiom",
			"cmd/idiom/product_workflow.go",
			"cmd/idiom/product_workflow_test.go",
		},
	}
	unread := planThenExecuteUnreadRequiredPaths(brief, nil)

	if _, ok := unread["cmd/idiom"]; ok {
		t.Fatalf("parent directory scope must not become a source-body obligation, got %#v", unread)
	}

	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if _, ok := unread[want]; !ok {
			t.Fatalf("missing unread required file %q, got %#v", want, unread)
		}
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAcceptsPlanArtifactEdits(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "A"}},
		},
	})

	if got.Intercepted {
		t.Fatalf("submit_plan plan_artifact.edits with full required path coverage must pass, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardValidatesPersistedPlanArtifactEditsOnly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "b.txt", OldText: "b", NewText: "B"},
		},
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "A"}},
		},
	})

	if !got.Intercepted {
		t.Fatalf("coverage guard must reject when persisted plan_artifact.edits misses a required path")
	}

	if !strings.Contains(got.Result.Error, "b.txt") {
		t.Fatalf("rejection should name the missing persisted path, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsMissingRequiredSnippet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				ID:      "required_edit_2",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old behavior", NewText: "old behavior\n#include \"only-one-edit\""},
		},
	})

	if !got.Intercepted {
		t.Fatalf("submit_plan missing a required final-state snippet must be rejected")
	}

	if !strings.Contains(got.Result.Error, "required_edit_2") || !strings.Contains(got.Result.Error, "required_edit_snippets") {
		t.Fatalf("rejection should name the missing required snippet, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAcceptsSnippetAlreadyPresentOrPlanned(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\nnew regression coverage\nfooter\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				ID:      "required_edit_1",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "footer", NewText: "footer updated"},
		},
	})

	if got.Intercepted {
		t.Fatalf("already-present required snippet should satisfy coverage, got %#v", got)
	}

	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\n"), 0o644); err != nil {
		t.Fatalf("reset a.txt: %v", err)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old behavior", NewText: "old behavior\nnew regression coverage"},
		},
	})
	if got.Intercepted {
		t.Fatalf("planned required snippet should satisfy coverage, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsPlanThatRemovesRequiredSnippet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\nnew regression coverage\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				ID:      "required_edit_1",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "new regression coverage\n", NewText: ""},
		},
	})

	if !got.Intercepted {
		t.Fatalf("plan removing a required final-state snippet must be rejected")
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsMissingOldTextAnchor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("package main\nfunc actual() {}\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.txt",
			OldText: "func actual lines 2-2 tags: core",
			NewText: "func replacement() {}",
		}},
	})

	if !got.Intercepted {
		t.Fatalf("plan with old_text absent from current source must be rejected")
	}

	if !strings.Contains(got.Result.Error, "old_text not found") {
		t.Fatalf("rejection should explain missing old_text anchor, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardInactiveOutsidePlannerPhase(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})

	if got.Intercepted {
		t.Fatalf("coverage guard must stand down outside planner phase, got %#v", got)
	}
}

func TestPlanThenExecuteWriteGuardLetsReadsThrough(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	for _, tool := range []string{"read_file", "list_files", "search_files", "search_content", "code_navigate"} {
		got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: tool, Path: "a.txt"})
		if got.Intercepted {
			t.Fatalf("read-class %q must pass through during planner phase, got %#v", tool, got)
		}
	}
}
