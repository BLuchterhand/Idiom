package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
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
		"```tool",
		`"tool":"submit_plan"`,
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
	if got.MaxTokens != 16384 {
		t.Fatalf("plan-execute recovery must preserve submit_plan budget, got %d", got.MaxTokens)
	}
	got = completionGateRecoveryChatOptions(llama.ChatOptions{}, brief, nil)
	if got.MaxTokens != 16384 {
		t.Fatalf("plan-execute recovery should default to planner-sized budget, got %d", got.MaxTokens)
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
	if !strings.Contains(got.Result.Error, "corrected plan") {
		t.Fatalf("rejection should be recoverable, got %q", got.Result.Error)
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

func TestPlanThenExecuteRequiredPathDiscoveryRejectsBroadReadBeforeRequiredSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "what calls productWorkflow",
		Resolution:      "symbol",
		IncludeEvidence: true,
	}, brief, nil)
	if !reject {
		t.Fatalf("broad/semantic discovery should be rejected until required source is read")
	}
	if summary != "Required edit path source read required" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if !strings.Contains(errorText, "cmd/idiom/product_workflow.go") {
		t.Fatalf("rejection should point at first unread required path, got %q", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryAllowsUnreadRequiredPathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	_, _, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, nil)
	if reject {
		t.Fatalf("read_file on an unread required path must be allowed")
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsAlreadyReadPathUntilRemainingRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n",
	}}
	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatalf("repeat discovery on an already-read required path should be rejected while another required path is unread")
	}
	if !strings.Contains(errorText, "internal/productworkflow/coverage_gate.go") {
		t.Fatalf("rejection should point at remaining unread required path, got %q", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryDoesNotCountFileOutlineAsSourceRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output:  "route_summary: Outlined 65 top-level declaration(s) in cmd/idiom/product_workflow.go. Includes declaration tags and suggested move batches in the output.\n",
	}}
	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if reject {
		t.Fatalf("file_outline should orient but not satisfy source-read coverage; follow-up source read should remain allowed: %s", errorText)
	}
}

func TestPlanThenExecutePostReadinessDiscoveryRejectsMoreSearch(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f4 internal/productworkflow/coverage_gate.go lines 1-272",
		},
	}
	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "coverage gate",
	}, brief, results)
	if !reject {
		t.Fatal("post-readiness read/search should be rejected")
	}
	if summary != "Plan submission required" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if !strings.Contains(errorText, "Submit one `submit_plan`") {
		t.Fatalf("rejection should force plan submission, got %q", errorText)
	}
}

func TestPlanThenExecutePostReadinessDiscoveryRejectsAfterBadSubmitPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f4 internal/productworkflow/coverage_gate.go lines 1-272",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must include at least one edit entry for every required_edit_paths entry; missing required edit path(s): `internal/productworkflow/coverage_gate.go`; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}
	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatal("post-submit-plan recovery search should be rejected")
	}
	if summary != "Corrected submit_plan required" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if !strings.Contains(errorText, "previous submit_plan was rejected") || !strings.Contains(errorText, "at most one bounded `read_file`") {
		t.Fatalf("rejection should point back to corrected submit_plan, got %q", errorText)
	}
	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, results)
	if reject {
		t.Fatal("one bounded read_file on the stale required-path anchor should be allowed")
	}
}

func TestPlanThenExecutePostReadinessDiscoveryAllowsUnreadPathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n",
	}}
	_, _, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/coverage_gate.go",
	}, brief, results)
	if reject {
		t.Fatal("required path reads should remain allowed before planner readiness")
	}
}

func TestPlanThenExecutePlannerToolGuardRejectsShellAndVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	for _, tool := range []string{"shell", "git_diff", "git_status", "source_sanity", "affected_suites", "context_trace_pack", "task", "task_ledger", "pwd"} {
		got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: tool})
		if !got.Intercepted {
			t.Fatalf("planner phase must reject %q, got %#v", tool, got)
		}
		if !strings.Contains(got.Result.Error, "source-satisfied") {
			t.Fatalf("rejection should name the source-satisfied closeout, got %q", got.Result.Error)
		}
	}
}

func TestPlanThenExecuteClampedEOFReadRejectsRepeatedOutOfRangeWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f4 cmd/idiom/product_workflow.go lines 2705-2805 (requested range 3700-3800 was clamped to file bounds; file has 2805 lines total)",
		Output:  "2705:tail",
	}}
	summary, errorText, reject := runner.planThenExecuteClampedEOFReadRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 3800,
		EndLine:   3900,
	}, results)
	if !reject {
		t.Fatal("expected repeated read_file beyond known EOF to be rejected")
	}
	if summary != "Out-of-range read_file rejected" || !strings.Contains(errorText, "submit_plan") || !strings.Contains(errorText, "2805 lines total") {
		t.Fatalf("rejection should explain the EOF fact and valid planner closeouts, summary=%q error=%q", summary, errorText)
	}
}

func TestPlanThenExecuteClampedEOFReadAllowsUsefulBoundedWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read target.txt lines 1-3 (requested range 1-30 was clamped to file bounds; file has 3 lines total)",
		Output:  "1:a",
	}}
	_, _, reject := runner.planThenExecuteClampedEOFReadRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "target.txt",
		StartLine: 2,
		EndLine:   3,
	}, results)
	if reject {
		t.Fatal("a bounded read that starts before known EOF can still add evidence")
	}
}

func TestPlanThenExecuteHardRedirectDoesNotHideReadFileWithRequiredAnchors(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	if runner.shouldHideToolAfterDiscoveryHardRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/main.go",
	}) {
		t.Fatal("plan-execute planner must keep read_file available for later exact-anchor reads on required edit paths")
	}
	if !runner.shouldHideToolAfterDiscoveryHardRedirect(workspace.ToolRequest{
		Tool: "search_content",
		Path: "cmd/idiom/main.go",
	}) {
		t.Fatal("non-anchor read tools can still be hidden after hard redirect")
	}
}

func TestPlanThenExecutePlannerToolGuardDoesNotAffectReviewRepair(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecuteReviewRepairTaskFamily,
		},
	}
	if got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: "git_diff"}); got.Intercepted {
		t.Fatalf("review-repair may inspect diff evidence; planner-only guard must stand down, got %#v", got)
	}
}

func TestReviewRepairCompletionGateRejectsNonDecisionFinalAnswer(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	got, rejected := runner.completionGateRejection("Let me trace the actual implementation to verify the tests against real code.", nil, 1, nil, false)
	if !rejected {
		t.Fatal("review-repair must reject process narration as a final answer")
	}
	if got.Tool != "completion_gate" || !strings.Contains(got.Error, "Decision: pass") || !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("review-repair rejection should name the valid closeout shapes, got %#v", got)
	}
}

func TestReviewRepairCompletionGateAcceptsPassChecklistOrSubmittedPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	passAnswer := "Decision: pass\n\nVerification Checklist:\n- Required behavior is satisfied.\n- Scope safety checked."
	if got, rejected := runner.completionGateRejection(passAnswer, nil, 1, nil, false); rejected {
		t.Fatalf("review-repair pass checklist should be accepted, got %#v", got)
	}
	pathOnlyChecklist := "Decision: pass\n\nVerification Checklist:\n- `widget_config.json`\n- `widget_notes.md`\n"
	if got, rejected := runner.completionGateRejection(pathOnlyChecklist, nil, 1, nil, false); rejected {
		t.Fatalf("review-repair pass checklist must not be rejected by the generic read-only explanation gate, got %#v", got)
	}
	results := []workspace.ToolResult{{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."}}
	if got, rejected := runner.completionGateRejection("Plan submitted with 1 edit.", results, 2, nil, false); rejected {
		t.Fatalf("review-repair submitted corrective plan should be accepted, got %#v", got)
	}
}

func TestReviewRepairCompletionCloseoutDoesNotInventMissingWrites(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			RequiredEditPaths: []string{"widget_config.json", "widget_notes.md"},
		},
	}
	answer := runner.completionGateRepeatedBlockerAnswer(workspace.ToolResult{
		Tool:  "completion_gate",
		Error: "review-repair must close with Decision: pass or a corrective plan",
	}, nil)
	if strings.Contains(answer, "Required edit paths not changed") || strings.Contains(answer, "no successful write was recorded") {
		t.Fatalf("review-repair closeout should not describe allowed repair scope as missing writes:\n%s", answer)
	}
	if !strings.Contains(answer, "review-repair must close") {
		t.Fatalf("closeout should preserve the actual completion blocker, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanGuardSuppressesMalformedPassPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	got := runner.preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior passes.",
		Edits:     []workspace.PlanEdit{{Purpose: "verification pass"}},
	})
	if !got.Intercepted {
		t.Fatal("review-repair malformed/no-path submit_plan should be suppressed before workspace schema errors")
	}
	if got.Result.Tool != "review_repair_noop_plan" || strings.TrimSpace(got.Result.Error) != "" {
		t.Fatalf("noop review plan should be a non-error nudge, got %#v", got.Result)
	}
	if _, ok := planThenExecuteShortCircuitAnswer(runner.RuntimeBrief, workspace.ToolRequest{Tool: "submit_plan"}, got.Result); ok {
		t.Fatal("suppressed review-repair noop plan must not look like a successful corrective submit_plan")
	}
}

func TestReviewRepairNoopSubmitPlanGuardSuppressesAlreadyAppliedPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "widget_config.json"), []byte("{\n  \"labels\": [\"alpha\", \"beta\"]\n}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	got := runner.preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "widget_config.json",
			OldText: "\"labels\": [\"alpha\"]",
			NewText: "\"labels\": [\"alpha\", \"beta\"]",
			Purpose: "stale original edit",
		}},
	})
	if !got.Intercepted {
		t.Fatal("review-repair stale submit_plan should be suppressed before submit_plan validation rejects it")
	}
	if got.Result.Tool != "review_repair_noop_plan" || strings.Contains(strings.ToLower(got.Result.Summary), "rejected") {
		t.Fatalf("stale review submit_plan should become a non-error pass nudge, got %#v", got.Result)
	}
}

func TestPlanThenExecuteWriteGuardInactiveOutsidePlannerPhase(t *testing.T) {
	// Same edit_file call that gets rejected during the planner phase
	// must pass through cleanly under any other lane shape.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"},
	}
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"})
	if got.Intercepted {
		t.Fatalf("guard must stand down outside the planner phase, got %#v", got)
	}
}

func TestMutationCompletionGateAcceptsSubmitPlanInPlannerPhase(t *testing.T) {
	// Plan-execute planner phase: submit_plan succeeded but no files
	// were mutated (the executor phase mutates files in a separate
	// process). The completion gate must NOT reject the answer for
	// "no successful write tool result is present" — submit_plan IS
	// the successful write here.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "submit_plan", Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them."},
	}
	got, rejected := runner.mutationCompletionRejection("Plan submitted with 2 edits.", results)
	if rejected {
		t.Fatalf("planner phase with successful submit_plan must not be rejected by mutation gate; got %#v", got)
	}
}

func TestMutationCompletionGateAcceptsPlannerSourceSatisfiedWithReadEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10"},
	}
	answer := "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed."
	if got, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		t.Fatalf("source-satisfied planner answer with read evidence should pass, got %#v", got)
	}
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		t.Fatalf("source-satisfied planner answer should not be converted to blocker, got kind=%q reason=%q", kind, reason)
	}
}

func TestMutationCompletionGateAcceptsUnscopedPlannerSourceSatisfiedWithReadEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 pkg/feature_test.go lines 1-10"},
	}
	answer := "Decision: source-satisfied\n- pkg/feature.go: requested final state is already present.\n- pkg/feature_test.go: current test coverage already contains the regression."
	if got, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		t.Fatalf("unscoped source-satisfied planner answer with read evidence should pass, got %#v", got)
	}
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		t.Fatalf("unscoped source-satisfied planner answer should not be converted to blocker, got kind=%q reason=%q", kind, reason)
	}
}

func TestPlanThenExecuteSourceSatisfiedAnswerCoversPathsRequiresExplicitDecision(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{
			name:   "hyphenated explicit decision",
			answer: "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   true,
		},
		{
			name:   "spaced explicit decision",
			answer: "Decision: source satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   true,
		},
		{
			name:   "already present final state",
			answer: "Decision: source-satisfied\n- a.txt: requested final state is already present.\n- b.txt: current source already contains the requested behavior.",
			want:   true,
		},
		{
			name:   "question mentioning accepted remedy",
			answer: "<<QUESTION>>: Completion gate rejected before submit_plan.\n- a.txt: no successful edit was recorded; provide submit_plan, Decision: source-satisfied evidence, or a path-specific blocker.\n- b.txt: same blocker.",
			want:   false,
		},
		{
			name:   "blocker mentioning accepted remedy",
			answer: "Blocked: a.txt and b.txt need submit_plan or Decision: source-satisfied evidence before closeout.",
			want:   false,
		},
		{
			name:   "punctuated decision is not exact",
			answer: "Decision: source-satisfied:\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   false,
		},
		{
			name:   "legacy implicit already-present wording",
			answer: "- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanThenExecuteSourceSatisfiedAnswerCoversPaths(tt.answer, []string{"a.txt", "b.txt"})
			if got != tt.want {
				t.Fatalf("PlanThenExecuteSourceSatisfiedAnswerCoversPaths() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanThenExecuteSourceSatisfiedEvidencePathsRequiresExplicitDecision(t *testing.T) {
	answer := "Decision: source-satisfied\n- internal/a.go: no changes needed.\n- internal/a_test.go: no changes needed."
	paths := PlanThenExecuteSourceSatisfiedEvidencePaths(answer)
	want := []string{"internal/a.go", "internal/a_test.go"}
	if !slices.Equal(paths, want) {
		t.Fatalf("evidence paths = %#v, want %#v", paths, want)
	}
	blocker := "<<QUESTION>>: provide Decision: source-satisfied evidence for internal/a.go"
	if paths := PlanThenExecuteSourceSatisfiedEvidencePaths(blocker); len(paths) != 0 {
		t.Fatalf("blocker text must not produce source-satisfied evidence paths: %#v", paths)
	}
}

func TestMutationCompletionGateRejectsPlannerSourceSatisfiedWithoutPathEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
	}
	answer := "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed."
	got, rejected := runner.mutationCompletionRejection(answer, results)
	if !rejected {
		t.Fatalf("source-satisfied answer without evidence for every required path must be rejected")
	}
	if !strings.Contains(got.Error, "source-satisfied") {
		t.Fatalf("rejection should explain the accepted source-satisfied shape, got %#v", got)
	}
}

func TestCompletionGateRecoveryPromptPointsPlanExecuteAtSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with missing required paths")
	}
	if !hideReads {
		t.Fatalf("planner-phase recovery should hide reads and leave submit_plan as the write surface")
	}
	for _, want := range []string{
		"your next response must be a `submit_plan` tool call, not prose",
		"explicit replacement edit shapes",
		"replace_all:true",
		"empty `old_text`",
		"`new_text` as the full file body",
		"no substantial inline body or explicit replacement shape",
		"one-sentence `purpose`",
		"source-satisfied",
		"a.txt",
		"b.txt",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "edit_file") || strings.Contains(prompt, "apply_patch") {
		t.Fatalf("planner-phase recovery must not point at per-edit tools: %q", prompt)
	}
}

func TestCompletionGateRecoveryPromptPointsCandidateScopeAtToolAction(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with candidate scope")
	}
	if hideReads {
		t.Fatalf("candidate-scope recovery must keep reads visible so the planner can gather missing anchors")
	}
	for _, want := range []string{
		"candidate/allowed scope",
		"No required_edit_paths list is active",
		"not the same prose",
		"parser-readable action",
		"Do not include analysis before or after",
		"hidden reasoning",
		"ignored by the tool parser",
		"`submit_plan`",
		"one bounded read/search on the highest-confidence candidate path",
		"named missing edit-safety fact",
		"source-satisfied",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("candidate-scope recovery prompt missing %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "edit_file") || strings.Contains(prompt, "apply_patch") {
		t.Fatalf("planner-phase recovery must not point at per-edit tools: %q", prompt)
	}
	for _, forbidden := range []string{"candidate/path.go", "exact current text", "replacement text", "why these candidate path edits satisfy the objective"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("candidate-scope recovery must not include copyable placeholder %q:\n%s", forbidden, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptHidesReadsAfterCandidateScopeRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "replace the model path in the top-right banner with the alias",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f10 internal/app/rendering.go lines 1-180",
			Output:  "rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.modelID, \"unknown\"))",
		},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with candidate scope")
	}
	if !hideReads {
		t.Fatalf("candidate-scope recovery should hide reads once candidate source has already been read")
	}
	for _, want := range []string{
		"Read candidate path(s): internal/app/rendering.go",
		"Read/search tools are hidden",
		"Call `submit_plan` using exact source anchors",
		"Do not request another read",
		"Actions written only in hidden reasoning are ignored",
		"Do not copy schema examples or placeholder text",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("post-read candidate recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"candidate/path.go", "exact current text", "replacement text"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("post-read candidate recovery must not include copyable placeholder %q:\n%s", forbidden, prompt)
		}
	}
}

func TestDeterministicNoWriteRecoveryReadsRuntimeTargetRef(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "replace the model path in the top-right banner with the alias",
			TargetRefs:        []string{"internal/app/rendering.go", "docs/banner.md"},
		},
	}

	req, _, ok := runner.deterministicNoWriteRecoveryTargetRead(nil)
	if !ok {
		t.Fatalf("expected deterministic target read for candidate-scope planner")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" || req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("unexpected recovery read request: %#v", req)
	}
}

func TestPlanExecuteCandidateScopeRecoveryPreservesThinking(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		ModelThinkingFloor: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, nil)

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("candidate-scope recovery should preserve thinking mode, got %#v", opts.EnableThinking)
	}
	if !opts.PreserveThinking {
		t.Fatalf("candidate-scope recovery should preserve hidden reasoning policy")
	}
	if opts.ChatTemplateKwargs {
		t.Fatalf("candidate-scope recovery should not force request-level chat_template_kwargs")
	}
	if opts.MaxTokens != 4096 {
		t.Fatalf("candidate-scope recovery MaxTokens = %d, want 4096", opts.MaxTokens)
	}
	if opts.ToolChoice != "" {
		t.Fatalf("candidate-scope recovery should not force backend required-tool mode, got %q", opts.ToolChoice)
	}
}

func TestPlanExecuteCandidateScopeRecoveryDropsPriorThinkingAfterRead(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/app/rendering.go lines 1-120"}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("post-read plan-execute recovery should keep current-response thinking available, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatalf("post-read plan-execute recovery should stop preserving prior hidden reasoning")
	}
	if opts.ChatTemplateKwargs {
		t.Fatalf("post-read plan-execute recovery should not force request-level thinking kwargs")
	}
	if opts.ToolChoice != "" {
		t.Fatalf("post-read plan-execute recovery should not force backend required-tool mode, got %q", opts.ToolChoice)
	}
}

func TestPlanExecuteRequiredPathPostReadRecoveryCapsPlannerBudget(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1530-1671"}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("post-read required-path recovery should keep current-response thinking available, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatalf("post-read required-path recovery should stop preserving prior hidden reasoning")
	}
	if opts.ChatTemplateKwargs {
		t.Fatalf("post-read required-path recovery should not force request-level thinking kwargs")
	}
	if opts.MaxTokens != 8192 {
		t.Fatalf("post-read required-path recovery MaxTokens = %d, want 8192", opts.MaxTokens)
	}
	if opts.ToolChoice != "" {
		t.Fatalf("post-read required-path recovery should not force backend required-tool mode, got %q", opts.ToolChoice)
	}
}

func TestPlanExecuteRequiredPathPostReadRecoveryKeepsLargeBudgetForNewFile(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Create internal/writerroute/decision.go",
		RequiredEditPaths: []string{"internal/writerroute/decision.go"},
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/writerroute/decision.go", Error: "path is a NEW FILE and does not exist yet"}})

	if opts.MaxTokens != 16384 {
		t.Fatalf("new-file post-read recovery MaxTokens = %d, want 16384", opts.MaxTokens)
	}
}

func TestPlanExecuteReadyToSubmitCapsCloseoutBudget(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/app/app.go", "internal/agent/runner.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/app/app.go lines 1-56"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read Runner.Run in internal/agent/runner.go lines 88-210"},
	})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("ready-to-submit closeout should force visible/tool output over hidden thinking, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatalf("ready-to-submit closeout should not preserve hidden reasoning")
	}
	if opts.MaxTokens != 2048 {
		t.Fatalf("ready-to-submit closeout MaxTokens = %d, want 2048", opts.MaxTokens)
	}
}

func TestPlanExecuteRequiredPathSymbolReadRecoveryCapsPlannerBudget(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n",
	}})

	if opts.PreserveThinking {
		t.Fatalf("symbol-read required-path recovery should stop preserving prior hidden reasoning")
	}
	if opts.MaxTokens != 8192 {
		t.Fatalf("symbol-read required-path recovery MaxTokens = %d, want 8192", opts.MaxTokens)
	}
}

func TestPlanExecuteCandidateWriteOnlyRecoveryForcesVisibleSubmitPlanAfterRead(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteCandidateWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/app/rendering.go lines 1-120"}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("write-only plan-execute recovery should keep current-response thinking available, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatalf("write-only plan-execute recovery should drop prior hidden reasoning")
	}
	if opts.ChatTemplateKwargs {
		t.Fatalf("write-only plan-execute recovery should not force request-level thinking kwargs")
	}
	if opts.ToolChoice != "required" {
		t.Fatalf("write-only plan-execute recovery should force a native tool call, got %q", opts.ToolChoice)
	}
	if opts.MaxTokens != 16384 {
		t.Fatalf("write-only plan-execute recovery should preserve planner budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecuteRequiredWriteOnlyRecoveryForcesVisibleSubmitPlan(t *testing.T) {
	thinkingOn := true
	opts := planThenExecuteRequiredWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol"}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("required-path recovery should keep current-response thinking available, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatalf("required-path recovery should drop prior hidden reasoning")
	}
	if opts.ChatTemplateKwargs {
		t.Fatalf("required-path recovery should not force request-level thinking kwargs")
	}
	if opts.ToolChoice != "required" {
		t.Fatalf("required-path recovery should force a native submit_plan call, got %q", opts.ToolChoice)
	}
	if opts.MaxTokens != 16384 {
		t.Fatalf("required-path recovery should preserve planner budget, got %d", opts.MaxTokens)
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

const planExecuteStatusMigrationApplyTUIObjective = "We are continuing TDD completion work from docs/design-prd-adr-brief-research-workflow.md. Implement one bounded topic only: status migration apply must harden predictable temp-path writes against pre-existing symlinks. Focus on the existing product workflow status migration apply path. Add regression tests proving pre-existing workflow/index/events temp symlinks are rejected without partial durable mutation. Use repo discovery first, make the minimal code changes, and run the focused tests."

func TestLiteralRecoverySearchPatternPrioritizesTopicOverWorkflowBoilerplate(t *testing.T) {
	got := literalRecoverySearchPattern(planExecuteStatusMigrationApplyTUIObjective)
	for _, want := range []string{"status", "migration", "apply", "temp"} {
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

func TestCompletionGateRecoveryPromptKeepsReadsVisibleForUnreadBetterCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         planExecuteStatusMigrationApplyTUIObjective,
		},
	}

	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output: strings.Join([]string{
				`internal/productworkflow/migration.go:34: // status migration planning metadata`,
				`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
				`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
				`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/productworkflow/migration.go lines 1-180",
			Output:  "type StatusMigrationPlan struct{}",
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatalf("expected candidate-scope recovery prompt")
	}
	if hideReads {
		t.Fatalf("candidate-scope recovery should keep reads visible while better candidate remains unread:\n%s", prompt)
	}
	for _, want := range []string{
		"higher-confidence candidate path(s)",
		"internal/productworkflow/migration_apply.go",
		"Read candidate path(s): internal/productworkflow/migration.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteCandidateScopeReadinessDefersForUnreadBetterCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         planExecuteStatusMigrationApplyTUIObjective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output: strings.Join([]string{
				`internal/productworkflow/migration.go:34: // status migration planning metadata`,
				`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
				`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
				`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/productworkflow/migration.go lines 1-180",
			Output:  "type StatusMigrationPlan struct{}",
		},
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("readiness should defer while higher-confidence candidate remains unread:\n%s", got)
	}
	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f107 internal/productworkflow/migration_apply.go lines 1-180",
		Output:  "tmpPath := workflowPath + \".tmp\"",
	})
	if got := planThenExecuteReadyToSubmitNudge(brief, results); !strings.Contains(got, "PLANNER READINESS REACHED") {
		t.Fatalf("readiness should apply after ranked candidates are read, got:\n%s", got)
	}
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
	if !slices.Contains(rails.FinalCritic, "submit_plan_called_or_source_satisfied") {
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
	if !strings.Contains(joined, "source-satisfied") {
		t.Fatalf("planner-phase hints must name source-satisfied closeout, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}
	if strings.Contains(joined, "write capability") || strings.Contains(joined, "first required-path write") {
		t.Fatalf("planner-phase hints must not use generic write/edit closeout language: %s", joined)
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
			Output:  "internal/app/rendering.go:50: if banner := m.errorBanner(); banner != \"\" {\ninternal/frontier/locate.go:12: banner",
		},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}
	for _, want := range []string{
		"Current evidence paths",
		"internal/app/rendering.go",
		`"tool":"read_file"`,
		`"path":"internal/app/rendering.go"`,
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

func TestCompletionGateRecoveryPromptFallsBackAfterEmptyContextPack(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, _, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "0 candidates", Output: `{"candidate_files":[],"candidate_symbols":[],"tests":[]}`},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}
	if !strings.Contains(prompt, "bounded fallback discovery phase") {
		t.Fatalf("empty context_pack should trigger fallback discovery:\n%s", prompt)
	}
	if strings.Contains(prompt, `"tool":"context_pack"`) {
		t.Fatalf("empty context_pack recovery should not ask for another context_pack call:\n%s", prompt)
	}
}

func TestCompletionGateRecoveryChatOptionsPreservesDirectMutationModelMode(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "context_pack", Summary: "context"}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("direct mutation recovery should preserve thinking mode, got %#v", opts.EnableThinking)
	}
	if !opts.PreserveThinking {
		t.Fatalf("direct mutation recovery should preserve hidden-reasoning policy")
	}
	if opts.ChatTemplateKwargs {
		t.Fatalf("direct mutation recovery should not force request-level chat_template_kwargs")
	}
	if opts.MaxTokens != 2048 {
		t.Fatalf("direct mutation recovery MaxTokens = %d, want 2048", opts.MaxTokens)
	}
	if opts.ToolChoice != "auto" {
		t.Fatalf("direct mutation recovery should preserve existing tool choice, got %q", opts.ToolChoice)
	}
}

func TestCompletionGateRecoveryChatOptionsForcesVisibleToolAfterConcreteRead(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read internal/app/rendering.go", Output: "rightInfo := statusStyle.Render(...) "}})

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("post-read mutation recovery should keep current-response thinking available, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatalf("post-read mutation recovery should not preserve hidden reasoning")
	}
	if opts.ChatTemplateKwargs {
		t.Fatalf("post-read mutation recovery should not force request-level chat_template_kwargs")
	}
	if opts.ToolChoice != "auto" {
		t.Fatalf("post-read mutation recovery should preserve existing tool choice, got %q", opts.ToolChoice)
	}
	if opts.MaxTokens != 1024 {
		t.Fatalf("post-read mutation recovery MaxTokens = %d, want 1024", opts.MaxTokens)
	}
}

func TestSalvageToolRequestFromEmptyReasoning(t *testing.T) {
	runner := &Runner{}
	req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content:          "",
		ReasoningContent: "I should inspect it.\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"internal/app/rendering.go\",\"start_line\":1,\"end_line\":180,\"named_gap\":\"render branch\",\"why_not_answering\":\"need source\"}\n```",
	}, nil)
	if !ok {
		t.Fatalf("expected reasoning tool request to be salvaged")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningAllowsPlanExecuteSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "I have the anchor now.\n\n```tool\n" +
			`{"tool":"submit_plan","rationale":"Replace the banner model path with the configured alias.","edits":[{"path":"internal/app/rendering.go","old_text":"rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.modelID, \"unknown\"))","new_text":"rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.currentAliasForRole(\"writer\"), \"unknown\"))","purpose":"Display the model alias in the TUI banner instead of the raw model path."}]}` +
			"\n```",
	}, nil)
	if !ok {
		t.Fatalf("expected hidden-reasoning submit_plan to be salvaged during plan-execute planner phase")
	}
	if req.Tool != "submit_plan" || len(req.Edits) != 1 || req.Edits[0].Path != "internal/app/rendering.go" {
		t.Fatalf("unexpected salvaged submit_plan: %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsSubmitPlanOutsidePlanSubmission(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation",
		},
	}
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "```tool\n" +
			`{"tool":"submit_plan","rationale":"Plan.","edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace text"}]}` +
			"\n```",
	}, nil); ok {
		t.Fatalf("submit_plan salvage must stay scoped to plan-submission phases, got %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsSubmitPlanPlaceholders(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "```tool\n" +
			`{"tool":"submit_plan","rationale":"why these candidate path edits satisfy the objective","edits":[{"path":"candidate/path.go","old_text":"exact current text","new_text":"replacement text","purpose":"one sentence"}]}` +
			"\n```",
	}, nil); ok {
		t.Fatalf("placeholder submit_plan request should not be salvaged: %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsPlaceholders(t *testing.T) {
	runner := &Runner{}
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content:          "",
		ReasoningContent: "```tool\n{\"tool\":\"edit_file\",\"path\":\"candidate/path.go\",\"old_text\":\"exact current text\",\"new_text\":\"replacement text\"}\n```",
	}, nil); ok {
		t.Fatalf("placeholder request should not be salvaged: %#v", req)
	}
}

func TestSalvageToolRequestFromReasoningRejectsMutatingTools(t *testing.T) {
	runner := &Runner{}
	for _, reasoning := range []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"a.txt\",\"old_text\":\"old\",\"new_text\":\"new\"}\n```",
		"```tool\n{\"tool\":\"write_file\",\"path\":\"a.txt\",\"content\":\"new\"}\n```",
		"```tool\n{\"tool\":\"append_file\",\"path\":\"a.txt\",\"content\":\"new\"}\n```",
		"```tool\n{\"tool\":\"apply_patch\",\"content\":\"*** Begin Patch\\n*** Update File: a.txt\\n@@\\n-old\\n+new\\n*** End Patch\\n\"}\n```",
	} {
		if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
			Content:          "",
			ReasoningContent: reasoning,
		}, nil); ok {
			t.Fatalf("hidden-reasoning mutating request should not be salvaged: %#v", req)
		}
	}
}

func TestDeterministicNoWriteRecoveryReadsSearchContentCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line.",
		Output:  "internal/app/rendering.go:50: banner",
	}})
	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" {
		t.Fatalf("unexpected recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryRanksUISourceOverSchemasAndComments(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with the model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found matching lines.",
		Output: strings.Join([]string{
			`f33 internal/workspace/tool_schemas.go:120: "model alias"`,
			`f6 internal/app/approval.go:51:// invariant that the banner always matches a real request`,
			`f8 internal/app/rendering.go:50: if banner := m.errorBanner(); banner != "" {`,
			`f8 internal/app/rendering.go:51: footerLines = append(footerLines, errorStyle.Render(banner))`,
			`f3 internal/frontier/locate.go:169: if ContainsAny(normalized, "top", "header", "banner") {`,
		}, "\n"),
	}})
	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" {
		t.Fatalf("unexpected ranked recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryPrefersObjectiveSpecificApplyPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         planExecuteStatusMigrationApplyTUIObjective,
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found matching lines.",
		Output: strings.Join([]string{
			`internal/productworkflow/migration.go:34: // status migration planning metadata`,
			`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
			`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
			`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
		}, "\n"),
	}})
	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}
	if req.Tool != "read_file" || req.Path != "internal/productworkflow/migration_apply.go" {
		t.Fatalf("unexpected ranked recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryReadsUnreadBetterCandidateAfterFirstRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         planExecuteStatusMigrationApplyTUIObjective,
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output: strings.Join([]string{
				`internal/productworkflow/migration.go:34: // status migration planning metadata`,
				`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
				`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
				`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/productworkflow/migration.go lines 1-180",
			Output:  "type StatusMigrationPlan struct{}",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryUnreadCandidateRead(results)
	if !ok {
		t.Fatalf("expected deterministic unread-candidate read recovery")
	}
	if req.Tool != "read_file" || req.Path != "internal/productworkflow/migration_apply.go" {
		t.Fatalf("unexpected unread-candidate recovery request: %#v raw=%s", req, raw)
	}
	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f107 internal/productworkflow/migration_apply.go lines 1-180",
		Output:  "tmpPath := workflowPath + \".tmp\"",
	})
	if req, _, ok := runner.deterministicNoWriteRecoveryUnreadCandidateRead(results); ok {
		t.Fatalf("did not expect recovery after ranked candidates are read, got %#v", req)
	}
}

func TestDeterministicNoWriteRecoveryKeepsNonGoSourcePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the banner model path with the actual model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found matching lines.",
		Output: strings.Join([]string{
			`f1 web/src/components/Banner.tsx:42: <HeaderBanner modelAlias={activeAlias} />`,
			`f2 docs/banner.md:4: banner model alias notes`,
		}, "\n"),
	}})
	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}
	if req.Tool != "read_file" || req.Path != "web/src/components/Banner.tsx" {
		t.Fatalf("unexpected non-Go recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoverySearchesAfterContextPackOnly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with the model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoverySearch([]workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "packet",
		Output:  `{"candidate_files":[{"path":"internal/app/model_popup.go"}]}`,
	}})
	if !ok {
		t.Fatalf("expected deterministic search recovery")
	}
	if req.Tool != "search_content" || !strings.Contains(req.Pattern, "banner") {
		t.Fatalf("unexpected recovery search request: %#v raw=%s", req, raw)
	}
	if strings.Contains(req.Pattern, "model alias") || strings.Contains(req.Pattern, "alias") {
		t.Fatalf("recovery search should avoid broad replacement terms, got pattern %q", req.Pattern)
	}
}

func TestDeterministicNoWriteRecoverySearchesAfterFailedContextPack(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with the model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoverySearch([]workspace.ToolResult{{
		Tool:  "context_pack",
		Error: "context index unavailable",
	}})
	if !ok {
		t.Fatalf("expected deterministic search recovery after failed context_pack")
	}
	if req.Tool != "search_content" || !strings.Contains(req.Pattern, "banner") {
		t.Fatalf("unexpected recovery search request after failed context_pack: %#v raw=%s", req, raw)
	}
}

func TestUnscopedContextPackCandidateReadRecoveryRejectsUngroundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "replace the model path in the top-right banner with model alias",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	summary, errText, prompt, rejected := runner.unscopedContextPackCandidateReadRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/model_popup.go"},
		[]workspace.ToolResult{{Tool: "context_pack", Summary: "packet", Output: `{"candidate_files":[{"path":"internal/app/model_popup.go"}]}`}},
	)
	if !rejected {
		t.Fatalf("expected context-pack-only read to be rejected")
	}
	for _, want := range []string{
		"Context packet candidate read rejected",
		"context_pack has not established an exact edit target",
		`"tool":"search_content"`,
		"banner",
	} {
		if !strings.Contains(summary+"\n"+errText+"\n"+prompt, want) {
			t.Fatalf("expected recovery text to contain %q, got summary=%q err=%q prompt=%q", want, summary, errText, prompt)
		}
	}
}

func TestCompletionGateRetryLimitAllowsTwoUnscopedNoWriteRecoveries(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{Tool: "search_content", Summary: "No content matches"}}

	if got := runner.completionGateRetryLimit(results); got != 2 {
		t.Fatalf("unscoped no-write recovery retry limit = %d, want 2", got)
	}
	if !runner.unscopedNoWriteCompletionRecoveryApplies(results) {
		t.Fatalf("expected unscoped no-write recovery to apply")
	}
}

func TestCompletionGateRejectsUnresolvedProcessIntent(t *testing.T) {
	got, rejected := unresolvedProcessIntentCompletionRejection("I'll search the codebase and inspect the TUI next.")
	if !rejected {
		t.Fatalf("expected process narration to be rejected")
	}
	if got.Tool != "completion_gate" {
		t.Fatalf("rejection should be attributed to completion gate, got %#v", got)
	}
	if !strings.Contains(got.Error, "Issue the needed tool call now") {
		t.Fatalf("rejection should point at action, got %q", got.Error)
	}

	if _, rejected := unresolvedProcessIntentCompletionRejection("I cannot proceed because the workspace is read-only."); rejected {
		t.Fatalf("concrete blocker answers should not be classified as unresolved process intent")
	}
}

func TestFinalAnswerBlockerKindExemptsPlannerPhaseWithSubmitPlan(t *testing.T) {
	// Post-answer blocker check: in the plan-execute planner phase,
	// successful submit_plan IS the deliverable — the executor phase
	// (run by the orchestrator after this runner exits) produces the
	// actual file changes. Without this exemption the blocker check
	// sees zero changed_paths and overrides the model's final answer
	// with a "required_edit_paths_remaining" narrative, even after the
	// model submitted a correct plan.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "submit_plan", Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them."},
	}
	kind, reason := runner.finalAnswerBlockerKind("Plan submitted with 2 edits.", results)
	if kind != "" || reason != "" {
		t.Fatalf("planner phase with successful submit_plan must not trigger the blocker check; got kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindStillRejectsImplementationLaneWithoutEdits(t *testing.T) {
	// Sanity: the plan-execute exemption must not weaken the post-answer
	// blocker for normal implementation lanes.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	kind, reason := runner.finalAnswerBlockerKind("Done.", nil)
	if kind != "required_edit_paths_remaining" || reason != "required_edit_paths_remaining" {
		t.Fatalf("implementation lane with no edits must trigger blocker; got kind=%q reason=%q", kind, reason)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeFiresWhenAllPathsRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10"},
	}
	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatalf("expected nudge once every required path has been read; got empty string")
	}
	if !strings.Contains(got, "submit_plan") {
		t.Fatalf("nudge must point at submit_plan; got %q", got)
	}
	if !strings.Contains(got, "whole-file/new-file shape") {
		t.Fatalf("nudge must include the new-file/whole-file submit_plan shape; got %q", got)
	}
	if !strings.Contains(got, "substantial inline body or explicit replacement edit shape") {
		t.Fatalf("nudge must explain required new-file body evidence; got %q", got)
	}
	if !strings.Contains(got, "<<QUESTION>>") {
		t.Fatalf("nudge must offer the QUESTION escape hatch; got %q", got)
	}
	if !strings.Contains(got, "source-satisfied") {
		t.Fatalf("nudge must offer the source-satisfied closeout; got %q", got)
	}
	if !strings.Contains(got, "Only read again if you can name one exact new edit-safety fact") {
		t.Fatalf("nudge must keep follow-up reads gap-driven; got %q", got)
	}
}

func TestPlanThenExecuteExplicitSourceSatisfiedAnswerAfterRequiredReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Smoke test without changing files and answer source-satisfied with evidence.",
		RequiredEditPaths: []string{"internal/app/app.go", "internal/agent/runner.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/app/app.go lines 1-56"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read Runner.Run in internal/agent/runner.go lines 88-210"},
	}

	answer, ok := planThenExecuteExplicitSourceSatisfiedAnswer(brief, results)
	if !ok {
		t.Fatalf("expected explicit source-satisfied no-write answer")
	}
	for _, want := range []string{
		"Decision: source-satisfied",
		"internal/app/app.go",
		"Read f1 internal/app/app.go lines 1-56",
		"internal/agent/runner.go",
		"Code navigation routed read_symbol via go_read_symbol",
		"Changed paths: none",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q:\n%s", want, answer)
		}
	}
}

func TestPlanThenExecuteExplicitSourceSatisfiedAnswerRequiresNoWriteObjective(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Implement the feature.",
		RequiredEditPaths: []string{"internal/app/app.go"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/app/app.go lines 1-56"}}

	if answer, ok := planThenExecuteExplicitSourceSatisfiedAnswer(brief, results); ok {
		t.Fatalf("unexpected source-satisfied short-circuit for mutation objective:\n%s", answer)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeWaitsForFullCoverage(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10"},
		// c.txt has not been read — the nudge must wait.
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("nudge must NOT fire while a required path is unread; got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeFiresForCandidateScopeRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/app/rendering.go lines 1-120"},
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatalf("expected candidate-scope readiness nudge after a read")
	}
	for _, want := range []string{
		"at least one candidate edit path has been read",
		"internal/app/rendering.go",
		"submit_plan",
		"Decision: source-satisfied",
		"<<QUESTION>>",
		"Hidden reasoning is not executable evidence",
		"Only read/search again if you can name one exact new edit-safety fact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("candidate-scope nudge missing %q: %q", want, got)
		}
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

func TestPlanThenExecuteNoopSubmitPlanSynthesizesSourceSatisfiedAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		Objective:         "reject temp symlinks and add regression tests proving no partial mutation",
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
			{Path: "internal/productworkflow/migration_apply_test.go", OldText: "same", NewText: "same", Purpose: "test"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}

	answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, nil)
	if !ok {
		t.Fatal("no-op submit_plan rejection should synthesize source-satisfied closeout")
	}
	if !strings.HasPrefix(answer, "Decision: source-satisfied") {
		t.Fatalf("answer should use exact source-satisfied decision, got:\n%s", answer)
	}
	for _, want := range []string{"internal/productworkflow/migration_apply.go", "internal/productworkflow/migration_apply_test.go", "no changes needed"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q:\n%s", want, answer)
		}
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
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
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

func TestPlanThenExecuteNoopSubmitPlanAddsReadTestEvidenceForSourceSatisfiedAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The objective is to add regression tests proving no partial mutation; the existing test already covers it.",
		Edits: []workspace.PlanEdit{
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 internal/productworkflow/migration_apply_test.go lines 1-537"},
	}

	answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results)
	if !ok {
		t.Fatal("read test evidence should allow source-satisfied closeout for test-required no-op plan")
	}
	if !strings.Contains(answer, "internal/productworkflow/migration_apply_test.go") {
		t.Fatalf("source-satisfied answer should name read test evidence:\n%s", answer)
	}
}

func TestPlanThenExecuteNoopSubmitPlanIncludesReadTestEvidenceEvenWhenObjectiveSummaryIsSparse(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "source no-op",
		Edits: []workspace.PlanEdit{
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 internal/productworkflow/migration_apply_test.go lines 1-537"},
	}

	answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results)
	if !ok {
		t.Fatal("sparse objective no-op should still synthesize source-satisfied closeout")
	}
	if !strings.Contains(answer, "internal/productworkflow/migration_apply_test.go") {
		t.Fatalf("source-satisfied answer should preserve read test evidence for coverage checks:\n%s", answer)
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
		got := implementationScopedEditAllowedTools([]string{tool})
		if len(got) != 1 || got[0] != tool {
			t.Fatalf("scoped-edit allowlist should pass %q through, got %v", tool, got)
		}
	}
	for _, tool := range []string{"shell", "git_status", "task", "search_content", "list_files", "context_index"} {
		got := implementationScopedEditAllowedTools([]string{tool})
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
	normal := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}}
	if normal.writeOnlyRecoveryToolIsWrite("submit_plan") {
		t.Fatal("submit_plan should not be treated as a normal implementation write tool outside plan-execute planner phase")
	}
}

func TestImplementationScopedSurfaceActiveForRequiredPathMutation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/review.md"},
	}
	if !implementationScopedEditSurfaceActive(brief, nil) {
		t.Fatalf("required-path mutation packets should use scoped discovery/edit tools even without target-packet anchors")
	}
	brief.RequiredEditPaths = nil
	if implementationScopedEditSurfaceActive(brief, nil) {
		t.Fatalf("scoped implementation surface must stay inactive without required edit paths")
	}
}

func TestScopedImplementationDiscoveryLookupAllowsSemanticAndDeclaredProjectTools(t *testing.T) {
	reg, err := workspace.NewExternalToolRegistry([]workspace.ExternalTool{
		{Name: "lookup", Command: "true", ReadOnly: true, Discovery: true},
		{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: false},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
	}
	runner.Workspace.SetExternalToolsRegistry(reg)

	allowed := []workspace.ToolRequest{
		{Tool: "cpp_find_symbol", Pattern: "FormativeEventType"},
		{Tool: "context_query", Query: "FormativeEventType owner", Resolution: "symbol"},
		{Tool: "lookup", Query: "FormativeEventType"},
		{Tool: "code_navigate", Capability: "file_outline", Path: "native/src/simulation/creatures/formative_events.h"},
	}
	for _, req := range allowed {
		if !runner.scopedImplementationDiscoveryLookupAllowed(req, []string{"native/src/simulation/creatures/formative_events.h"}) {
			t.Fatalf("expected scoped discovery lookup to allow %#v", req)
		}
	}
	rejected := []workspace.ToolRequest{
		{Tool: "search_content", Pattern: "FormativeEventType"},
		{Tool: "queue_run", Query: "FormativeEventType"},
		{Tool: "semgrep_scan", Path: "."},
		{Tool: "cpp_find_symbol", Pattern: "*"},
		{Tool: "code_navigate", Capability: "file_outline", Path: "native/src/core/components.h"},
		{Tool: "structural_search", Pattern: "enum class FormativeEventType", Path: "native/src"},
	}
	for _, req := range rejected {
		if runner.scopedImplementationDiscoveryLookupAllowed(req, []string{"native/src/simulation/creatures/formative_events.h"}) {
			t.Fatalf("expected scoped discovery lookup to reject %#v", req)
		}
	}
}

func TestDocDeliverableRecoveryNudgesScopeCorrectionAfterOutOfScopeRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{"native/src/simulation/creatures/formative_events.h"},
		},
	}
	req := workspace.ToolRequest{
		Tool:    "structural_search",
		Path:    "native/src",
		Pattern: "enum class FormativeEventType",
	}
	_, _, prompt, hideReads, rejected := runner.writeReadyPacketReadWithoutGapRecovery(req, nil)
	if !rejected {
		t.Fatalf("expected out-of-scope read to be rejected")
	}
	if hideReads {
		t.Fatalf("doc deliverable out-of-scope recovery should not activate write-only read hiding")
	}
	for _, want := range []string{"Treat this as scope evidence", "scope-correction", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestDocDeliverableCompletionRecoveryRecordsEvidenceGapWithoutHidingReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatalf("expected completion recovery prompt")
	}
	if hideReads {
		t.Fatalf("doc deliverable completion recovery should not hide reads")
	}
	for _, want := range []string{"non-source deliverable", "deliverable-write contract", "Source verification pending", "<<QUESTION>>"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestDocDeliverableCompletionRecoveryStateClosesTowardWriteOrBlocker(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "write_file", "code_navigate"},
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
	}
	result := workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}
	state := buildRunnerNextActionState("main", 3, "completion_gate_rejected", workspace.ToolRequest{Tool: "completion_gate"}, result, []workspace.ToolResult{result}, false, brief)

	joinedValid := strings.Join(state.NextValidActions, "\n")
	for _, want := range []string{"deliverable-write capability", ".idiom/refine/example/iter-2/draft.md", "<<QUESTION>>"} {
		if !strings.Contains(joinedValid, want) {
			t.Fatalf("expected next valid actions to close toward %q, got %#v", want, state.NextValidActions)
		}
	}
	if strings.Contains(joinedValid, "code_navigate") || strings.Contains(joinedValid, "read_file") {
		t.Fatalf("post-gate scratchpad closeout should not advertise new discovery as a normal next action: %#v", state.NextValidActions)
	}
	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(joinedInvalid, "new source discovery") {
		t.Fatalf("expected invalid actions to call out discovery after prose-only completion, got %#v", state.InvalidNextActions)
	}
	joinedEvidence := strings.Join(state.EvidenceStatus, "\n")
	if !strings.Contains(joinedEvidence, "non-source deliverable still unwritten") {
		t.Fatalf("expected evidence status to carry closeout state, got %#v", state.EvidenceStatus)
	}
}

func TestScratchpadDeliverableCloseoutRejectsNonWriteTools(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}}
	summary, errorText, prompt, target, reject := runner.scratchpadDeliverableCloseoutNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "native/src/example.cpp"},
		results,
	)
	if !reject {
		t.Fatalf("post-completion scratchpad closeout should reject non-write tools")
	}
	joined := summary + " " + errorText + " " + prompt + " " + target
	for _, want := range []string{"Scratchpad deliverable closeout", "read_file", ".idiom/refine/example/iter-2/draft.md", "deliverable-write capability", "Source verification pending", "<<QUESTION>>"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got summary=%q error=%q prompt=%q target=%q", want, summary, errorText, prompt, target)
		}
	}
	if _, _, _, _, reject := runner.scratchpadDeliverableCloseoutNonWriteRecovery(
		workspace.ToolRequest{Tool: "write_file", Path: ".idiom/refine/example/iter-2/draft.md"},
		results,
	); reject {
		t.Fatalf("write_file must remain available for scratchpad closeout")
	}
}

func TestScratchpadDeliverablePrewriteRejectsSourceDiscoveryButAllowsScratchpadReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths: []string{
				".idiom/refine/example/iter-1/draft.md",
				".idiom/refine/example/iter-1/review.md",
				"native/src/simulation/creatures/formative_events.h",
			},
		},
	}

	summary, errorText, prompt, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "native/src/simulation/creatures/formative_events.h"},
		nil,
	)
	if !reject {
		t.Fatalf("expected source read to be redirected to scratchpad write")
	}
	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"Scratchpad deliverable source discovery", "deliverable-write contract", "Source verification pending", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got %q", want, joined)
		}
	}

	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "context_query", Query: "FormativeEventType"},
		nil,
	); !reject {
		t.Fatalf("expected broad/pathless discovery to be redirected")
	}
	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: ".idiom/refine/example/iter-1/review.md"},
		nil,
	); reject {
		t.Fatalf("scratchpad input reads should remain available")
	}
	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "write_file", Path: ".idiom/refine/example/iter-2/draft.md"},
		nil,
	); reject {
		t.Fatalf("write_file must remain available for scratchpad deliverables")
	}
}

func TestScopedImplementationReadScopeDoesNotExpandFromDynamicEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{"native/src/simulation/creatures/formative_events.h"},
		},
	}
	req := workspace.ToolRequest{
		Tool:     "read_file",
		Path:     "native/src/core/components.h",
		NamedGap: "context query mentioned this candidate path",
	}
	results := []workspace.ToolResult{{
		Tool:   "context_query",
		Output: "candidate: native/src/core/components.h:42 AppraisalResult",
	}}
	_, _, prompt, _, rejected := runner.writeReadyPacketReadWithoutGapRecovery(req, results)
	if !rejected {
		t.Fatalf("dynamic tool-result evidence must not expand scratchpad read scope")
	}
	if !strings.Contains(prompt, "scope evidence") {
		t.Fatalf("expected scope evidence prompt, got %q", prompt)
	}
}

func TestScratchpadDeliverableFailedEvidenceNudgesDraftedGap(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
		},
	}
	prompt, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		[]workspace.ToolResult{{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"}},
	)
	if !ok {
		t.Fatalf("expected failed optional evidence to nudge scratchpad gap recording")
	}
	for _, want := range []string{"treat that as an evidence fact", "rather than repairing the lookup", "next action should be either writing", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestScratchpadDeliverableFailureFeedbackNarrowsToWriteOrQuestion(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "write_file"},
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
	}
	req := workspace.ToolRequest{
		Tool:    "read_file",
		Path:    "native/src/core/components.h",
		Pattern: "AppraisalResult",
	}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Scoped implementation read rejected",
		Error:   "outside the required edit paths and explicit evidence paths",
	}
	packet := buildRunnerFailureFeedbackPacket("main", 3, "tool_rejected", req, result, []workspace.ToolResult{result}, false, brief)
	joinedRequired := strings.Join(packet.RequiredNext, "\n")
	if !strings.Contains(joinedRequired, "record the failed") {
		t.Fatalf("expected required_next to record failed evidence, got %#v", packet.RequiredNext)
	}
	joinedValid := strings.Join(packet.ValidNext, "\n")
	for _, want := range []string{"write the non-source deliverable", "<<QUESTION>>"} {
		if !strings.Contains(joinedValid, want) {
			t.Fatalf("expected valid_next to contain %q, got %#v", want, packet.ValidNext)
		}
	}
	joinedEvidence := strings.Join(packet.EvidenceStatus, "\n")
	for _, want := range []string{"closed_negative_evidence", "read_file", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(joinedEvidence, want) {
			t.Fatalf("expected evidence_status to contain %q, got %#v", want, packet.EvidenceStatus)
		}
	}
}

func TestScratchpadDeliverableFailedEvidenceKeepsOriginalFailure(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
	}
	result, ok := scratchpadDeliverableFailedEvidenceResult(brief, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		{Tool: "read_file", Summary: "Scratchpad evidence follow-up rejected", Error: "A previous optional evidence lookup failed"},
	})
	if !ok {
		t.Fatalf("expected original failed evidence result")
	}
	if result.Tool != "code_navigate" || strings.Contains(result.Error, "previous optional evidence") {
		t.Fatalf("expected original failure, got %#v", result)
	}
}

func TestScratchpadDeliverableFailedEvidenceIgnoresSourceImplementation(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"native/src/example.cpp"},
		},
	}
	if _, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		nil,
	); ok {
		t.Fatalf("source implementation failures should keep the normal recovery path")
	}
}
