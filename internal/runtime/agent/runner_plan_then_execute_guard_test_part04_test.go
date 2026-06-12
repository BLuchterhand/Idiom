package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func TestReviewRepairDoomLoopTerminalCloseoutSynthesizesPassAfterCloseoutGate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			ChangedPaths:      []string{"internal/labels/labels.go"},
			AllowedEditPaths:  []string{"internal/labels/labels.go"},
			Objective: reviewRepairPhaseEvidenceObjectiveForTest(
				"schema: idiom.phase_evidence.v1",
				"phase: review_repair",
				"changed_paths: internal/labels/labels.go",
				"allowed_repair_paths: internal/labels/labels.go",
				"edit_set_obligations: (none)",
				"",
				"submitted_plan_state:",
				"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
				"source_sanity:",
				"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
				"verification:",
				"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
				"patch_hygiene: status=passed",
			),
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error:   "review-repair must close with Decision: pass, submit_plan, or <<QUESTION>>.",
	}}
	answer, reason, exhausted, ok := runner.reviewRepairDoomLoopTerminalCloseout(results)

	if !ok {
		t.Fatal("expected review-repair doom-loop closeout after terminal gate")
	}

	if reason != "review_repair_terminal_doom_loop_pass" || exhausted {
		t.Fatalf("unexpected closeout metadata reason=%q exhausted=%v", reason, exhausted)
	}

	if !strings.HasPrefix(answer, "Decision: pass\n\nVerification Checklist:") {
		t.Fatalf("expected deterministic pass, got:\n%s", answer)
	}
}

func TestReviewRepairDoomLoopTerminalCloseoutRequiresCloseoutGate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	if answer, reason, _, ok := runner.reviewRepairDoomLoopTerminalCloseout(nil); ok {
		t.Fatalf("must not synthesize review closeout before completion gate, answer=%q reason=%q", answer, reason)
	}
}

func TestReviewRepairDoomLoopTerminalCloseoutPreservesFailedObligationBlocker(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"verification_failed:go test ./..."},
			},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected contradictory pass",
		Error:   "verification failed",
	}}
	answer, reason, exhausted, ok := runner.reviewRepairDoomLoopTerminalCloseout(results)

	if !ok {
		t.Fatal("expected failed-obligation blocker after terminal gate")
	}

	if reason != "review_repair_terminal_doom_loop_blocker" || !exhausted {
		t.Fatalf("unexpected closeout metadata reason=%q exhausted=%v", reason, exhausted)
	}

	if !strings.HasPrefix(answer, "<<QUESTION>>:") || !strings.Contains(answer, "verification_failed:go test ./...") {
		t.Fatalf("failed obligations must stay blocker-only, got:\n%s", answer)
	}

	if strings.Contains(answer, "Decision: pass") {
		t.Fatalf("failed obligations must not become a pass:\n%s", answer)
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
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
	}
	answer := "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed."

	if got, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		t.Fatalf("source-satisfied planner answer with read evidence should pass, got %#v", got)
	}

	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		t.Fatalf("source-satisfied planner answer should not be converted to blocker, got kind=%q reason=%q", kind, reason)
	}
}

func TestPlanThenExecuteHarnessDiagnosticDoesNotSatisfyRequiredPathBlockerEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "autonomy_event",
			Summary: `{"type":"gate_decision","gate":{"gate":"completion_gate","safe_alternatives":["<<QUESTION>>:","Decision: source-satisfied"],"observed_facts":["path_ref:target_refs=a.txt:pending","path_ref:target_refs=b.txt:pending"]}}`,
		},
	}

	unread := planThenExecuteUnreadRequiredPaths(brief, results)
	for _, path := range []string{"a.txt", "b.txt"} {
		if _, ok := unread[path]; !ok {
			t.Fatalf("harness diagnostic must not satisfy required source coverage for %s: %#v", path, unread)
		}

		if planThenExecuteRequiredPathState(brief, results, path) != planThenExecuteRequiredPathExistingSourceBodyRequired {
			t.Fatalf("harness diagnostic must not become blocker evidence for %s", path)
		}
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
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 pkg/feature_test.go lines 1-10 (total lines: 10)"},
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

	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)

	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with missing required paths")
	}

	if !hideReads {
		t.Fatalf("planner-phase recovery should hide reads and leave submit_plan as the write surface")
	}

	for _, want := range []string{
		"your next response must be a parser-readable `submit_plan` request, not prose",
		"top-level `edits` as a JSON array, not a quoted string",
		"insert_after_line",
		"For small insertions/appends, use `insert_after_line`",
		"Keep edit bodies as small as executable",
		"prefer exact `old_text`/`new_text` for small unique replacements",
		"large additions anchored to unchanged source lines",
		"explicit replacement edit shapes",
		"replace_all:true",
		"empty `old_text`",
		"`new_text` as the full file body",
		"no substantial inline body or explicit replacement shape",
		"one-sentence `purpose`",
		"Every required path already has source-body evidence",
		"read/search/discovery tools are hidden now",
		"Do not call read_file",
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

func TestCompletionGateRecoveryPromptPreservesSubmitPlanRejectionDetails(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[0]:invalid_edit_shape:insert_after_line content exceeds inline submit_plan limit (4096 bytes); content_ref is not supported for insert_after_line in this schema",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok || !hideReads {
		t.Fatalf("expected submit_plan completion recovery with hidden reads, ok=%v hide=%v prompt=%s", ok, hideReads, prompt)
	}

	for _, want := range []string{
		"Previous rejected submit_plan drafts do not count as coverage",
		"Last submit_plan rejection",
		"Next submit_plan coverage checklist",
		"a.txt=must-have-valid-edit-or-source_satisfied_paths-entry",
		"b.txt=must-have-valid-edit-or-source_satisfied_paths-entry",
		"Do not repeat `insert_after_line`",
		"`content_ref`",
		"start_line",
		"end_line",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rejected submit_plan recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRequiredPathCompletionRecoveryRejectionPreservesSubmitPlanDetails(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[0]:invalid_edit_shape:insert_after_line content exceeds inline submit_plan limit (4096 bytes); content_ref is not supported for insert_after_line in this schema",
		},
	}

	result, rejected := runner.requiredPathCompletionRecoveryRejection("Done.", results)
	if !rejected {
		t.Fatal("expected non-specific completion to be rejected")
	}

	for _, want := range []string{
		"Previous rejected submit_plan drafts do not count as coverage",
		"Last submit_plan rejection",
		"Next submit_plan coverage checklist",
		"Do not repeat `insert_after_line`",
		"start_line",
		"end_line",
	} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("completion rejection missing %q:\n%s", want, result.Error)
		}
	}
}

func TestCompletionGateRecoveryPromptPointsPlanExecuteAtFirstRequiredReadWhenNoEvidence(t *testing.T) {
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

	if hideReads {
		t.Fatalf("required-path recovery without source evidence must keep source reads visible")
	}

	for _, want := range []string{
		"source-body evidence",
		"Unread required path: a.txt",
		"Do not draft `submit_plan` from outline/search hits",
		"read_file",
		"code_navigate",
		"Hidden reasoning is not executable evidence",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q: %q", want, prompt)
		}
	}

	for _, notWant := range []string{
		"Every required path already has source-body evidence",
		"read/search/discovery tools are hidden now",
		"Your next response must be a parser-readable `submit_plan` request",
	} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("recovery prompt should not claim submit_plan readiness before source evidence (%q): %q", notWant, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptPrioritizesUnreadRequiredSource(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
		{Tool: "search_content", Summary: "Found 2 matching line(s).", Output: "b.go:12:func TestB(t *testing.T)"},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}

	if hideReads {
		t.Fatalf("unread required source recovery must keep source-body reads visible")
	}

	for _, want := range []string{
		"source-body evidence",
		"Unread required path: b.go",
		"code_navigate",
		"read_symbol",
		"start_line",
		"Do not draft `submit_plan` from outline/search hits",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestDeterministicPlanExecuteRequiredSourceReadStartsFreshPlanner(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	req, raw, ok := runner.deterministicPlanExecuteRequiredSourceRead(nil)
	if !ok {
		t.Fatalf("expected deterministic source read for fresh required-path planner")
	}

	if req.Tool != "read_file" || req.Path != "a.txt" {
		t.Fatalf("unexpected recovery request: %#v", req)
	}

	if req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("fresh deterministic source read should be bounded to avoid outline-only redirects, got lines %d-%d", req.StartLine, req.EndLine)
	}

	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"path":"a.txt"`) {
		t.Fatalf("raw request should be parser-readable JSON, got %q", raw)
	}

	req, _, ok = runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
	})
	if !ok || req.Tool != "read_file" || req.Path != "a.txt" {
		t.Fatalf("expected deterministic recovery to advance the shallow required-path read, got %#v ok=%v", req, ok)
	}

	if req.StartLine != 11 || req.EndLine != 190 {
		t.Fatalf("advanced deterministic source read should continue the same path, got lines %d-%d", req.StartLine, req.EndLine)
	}

	req, _, ok = runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Redirected a.txt to code_navigate", Output: "a.txt requires file_outline"},
	})
	if !ok || req.Tool != "read_file" || req.Path != "b.txt" {
		t.Fatalf("expected deterministic recovery to skip attempted path and advance, got %#v ok=%v", req, ok)
	}

	singlePathRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}

	req, _, ok = singlePathRunner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Redirected a.txt to code_navigate", Output: "a.txt requires file_outline"},
	})

	if ok {
		t.Fatalf("deterministic recovery should not repeat a required path already attempted; got %#v", req)
	}
}

func TestDeterministicPlanExecuteRequiredSourceReadFollowsGenericCodeWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	req, _, ok := runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-180 (file has 2892 lines total)",
			Output:  "1:package main",
		},
	})
	if !ok || req.Tool != "code_navigate" || req.Capability != "file_outline" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("generic code header read should deterministically advance to file_outline, got %#v ok=%v", req, ok)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsRepeatedGenericRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow.go lines 1-180 (total lines: 2893)",
		Output:  "1:package main",
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1,
		EndLine:   180,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Capability != "file_outline" ||
		decision.Request.Path != "cmd/idiom/product_workflow.go" {

		t.Fatalf("repeated generic required-path read should redirect to source follow-up, got %#v", decision.Request)
	}
}

func TestRepeatedRangeLessReadFileAllowsFullBodyReplay(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read a.go lines 1-3 (total lines: 3)",
		Output:  "1:package a\n2:\n3:func A() {}\n",
	}}
	summary, errorText, reject := runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "a.go",
	}, results)

	if reject {
		t.Fatalf("repeated range-less read_file after a full read should remain model-owned before broader loop guards, got %q: %s", summary, errorText)
	}

	_, _, reject = runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   2,
	}, results)
	if reject {
		t.Fatal("explicit ranged reads must remain available")
	}
}

func TestPlanThenExecuteMalformedSymbolReadRedirectsToBoundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
	}
	redirected, raw, ok := runner.planThenExecuteMalformedSymbolReadRedirect(req, nil, []string{"read_file", "code_navigate"})

	if !ok {
		t.Fatal("expected malformed symbol read to redirect")
	}

	if redirected.Tool != "read_file" || redirected.Path != req.Path || redirected.StartLine != 1 || redirected.EndLine != 180 {
		t.Fatalf("redirected request = %#v", redirected)
	}

	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"start_line":1`) {
		t.Fatalf("redirect raw missing bounded read shape: %s", raw)
	}
}

func TestPlanThenExecuteRepeatedSymbolMissRedirectsToModelSelectedBoundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Zero-hit retry blocked",
		Error:   "this exact code_navigate call already returned no results earlier this turn. Repeating it will not find new matches.",
	}}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
		Symbol:     "CoverageGateResult",
		Pattern:    "CoverageGateResult",
	}

	redirected, raw, ok := runner.planThenExecuteRepeatedSymbolReadFallbackRedirect(req, results, []string{"read_file", "code_navigate"})
	if !ok {
		t.Fatal("expected repeated symbol miss to fall back to bounded source read on the model-selected path")
	}

	if redirected.Tool != "read_file" || redirected.Path != req.Path || redirected.StartLine != 1 || redirected.EndLine != 180 {
		t.Fatalf("redirected request = %#v", redirected)
	}

	if !strings.Contains(raw, `"named_gap":"bounded source window after semantic symbol lookup failed"`) {
		t.Fatalf("redirect raw missing named gap: %s", raw)
	}
}

func TestRepeatedRangeLessReadFileDoesNotMatchPathMentionedInBody(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read other.go lines 1-3 (total lines: 3)",
		Output:  "1:package other\n2:// TODO: inspect a.go next\n3:func Other() {}\n",
	}}
	_, _, reject := runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "a.go",
	}, results)

	if reject {
		t.Fatal("body text mentioning a path must not count as typed evidence that the requested path was already fully read")
	}
}

func TestRepeatedRangeLessReadFileAfterOutlineReturnsSourceBodyShape(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
			Output:  "The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
				"declarations:",
				"- buildAuditResult lines 313-354 tags: core",
				"- runProductWorkflow lines 1396-1671 tags: implementation",
			}, "\n"),
		},
	}
	_, errorText, reject := runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, results)

	if !reject {
		t.Fatal("expected repeated range-less read after outline redirect to be rejected")
	}

	for _, want := range []string{
		"already redirected to outline evidence",
		`"tool":"code_navigate"`,
		`"capability":"read_symbol"`,
		`"pattern":"runProductWorkflow"`,
	} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("outline-repeat rejection missing %q:\n%s", want, errorText)
		}
	}

	if strings.Contains(errorText, `"tool":"read_file","path":"cmd/idiom/product_workflow.go"`) &&
		!strings.Contains(errorText, `"start_line"`) {

		t.Fatalf("outline-repeat rejection must not return a range-less full-file read shape:\n%s", errorText)
	}
}

func TestRepeatedRangeLessReadFileAfterOutlineRedirectsToSourceBodyShape(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
			Output:  "The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
				"declarations:",
				"- buildAuditResult lines 313-354 tags: core",
				"- runProductWorkflow lines 1396-1671 tags: implementation",
			}, "\n"),
		},
	}
	redirected, raw, ok := runner.repeatedRangeLessReadFileRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, results, []string{"read_file", "code_navigate"})

	if !ok {
		t.Fatal("expected repeated range-less read after outline redirect to be rewritten")
	}

	if redirected.Tool != "code_navigate" || redirected.Capability != "read_symbol" || redirected.Pattern != "runProductWorkflow" {
		t.Fatalf("unexpected redirected request: %#v", redirected)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"read_symbol"`) {
		t.Fatalf("redirect raw should be parser-readable bounded source-body request, got %s", raw)
	}
}

func TestRepeatedRangeLessReadFileRedirectDoesNotRewriteAfterConcreteSource(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-3 (total lines: 3)",
			Output:  "1:package main\n2:\n3:func runProductWorkflow() {}\n",
		},
	}
	_, _, ok := runner.repeatedRangeLessReadFileRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, results, []string{"read_file", "code_navigate"})

	if ok {
		t.Fatal("full source evidence should keep duplicate range-less reads on the rejection path")
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectAllowsAdvancingRange(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-180 (total lines: 338)",
		Output:  "1:package productworkflow",
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 181,
		EndLine:   360,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != req.Tool || decision.Request.Path != req.Path || decision.Request.StartLine != req.StartLine || decision.Request.EndLine != req.EndLine {
		t.Fatalf("bounded follow-up range should be allowed as advancing evidence, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsLargeGoRangeToOutline(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow_test.go lines 1-300 (total lines: 6870)",
		Output:  "1:package main",
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow_test.go",
		StartLine: 301,
		EndLine:   600,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Path != "cmd/idiom/product_workflow_test.go" ||
		decision.Request.Capability != "file_outline" {

		t.Fatalf("large Go range follow-up should redirect to outline before broad scanning, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsGenericRangeAfterOutline(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement product TDD coverage gate behavior",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
			"declarations:",
			"- buildAuditResult lines 313-354 tags: core",
			"- runProductTDD lines 1396-1671 tags: implementation",
		}, "\n"),
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1,
		EndLine:   100,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Path != "cmd/idiom/product_workflow.go" ||
		decision.Request.Capability != "read_symbol" ||
		decision.Request.Pattern != "runProductTDD" {

		t.Fatalf("generic range after outline should redirect to symbol source, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsSatisfiedPathDriftToPendingPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement product TDD coverage gate behavior",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "cmd/idiom/product_workflow.go"},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read ValidateCoverage in internal/workflows/productworkflow/coverage_gate.go lines 20-80\n\n20:func ValidateCoverage() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-180 (total lines: 2893)",
			Output:  "1:package main",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
				"declarations:",
				"- buildAuditResult lines 313-354 tags: core",
				"- runProductTDD lines 1396-1671 tags: implementation",
			}, "\n"),
		},
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   300,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Path != "cmd/idiom/product_workflow.go" ||
		decision.Request.Capability != "read_symbol" ||
		decision.Request.Pattern != "runProductTDD" {

		t.Fatalf("drift to already-satisfied required path should redirect to pending path follow-up, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectPreservesInvalidAnchorRefresh(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`.",
		},
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != req.Tool || decision.Request.Path != req.Path || decision.Request.StartLine != req.StartLine || decision.Request.EndLine != req.EndLine {
		t.Fatalf("bounded invalid-anchor refresh should not be redirected, got %#v", decision.Request)
	}
}
