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
	got := completionGateRecoveryChatOptions(llama.ChatOptions{MaxTokens: 16384}, brief)
	if got.MaxTokens != 16384 {
		t.Fatalf("plan-execute recovery must preserve submit_plan budget, got %d", got.MaxTokens)
	}
	got = completionGateRecoveryChatOptions(llama.ChatOptions{}, brief)
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
	results := []workspace.ToolResult{{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."}}
	if got, rejected := runner.completionGateRejection("Plan submitted with 1 edit.", results, 2, nil, false); rejected {
		t.Fatalf("review-repair submitted corrective plan should be accepted, got %#v", got)
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
		"```tool",
		`{"tool":"submit_plan"`,
		`{"tool":"read_file"`,
		"`submit_plan`",
		"one bounded read/search on a candidate path",
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
}

func TestPlanExecuteCandidateScopeRecoveryDisablesThinking(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		ModelThinkingFloor: false,
		MaxTokens:          16384,
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("candidate-scope recovery should disable thinking, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatalf("candidate-scope recovery should not preserve hidden reasoning")
	}
	if !opts.ChatTemplateKwargs {
		t.Fatalf("candidate-scope recovery should force request-level chat_template_kwargs")
	}
	if opts.MaxTokens != 2048 {
		t.Fatalf("candidate-scope recovery MaxTokens = %d, want 2048", opts.MaxTokens)
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
