package agent

import (
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
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
	if !strings.Contains(got, "<<QUESTION>>") {
		t.Fatalf("nudge must offer the QUESTION escape hatch; got %q", got)
	}
	if !strings.Contains(got, "Do NOT issue another read") {
		t.Fatalf("nudge must explicitly forbid further reads; got %q", got)
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

func TestPlanThenExecutePlannerReadBudgetScalesWithPathCount(t *testing.T) {
	cases := []struct{ paths, want int }{
		{1, 8},   // 3*1+5
		{4, 17},  // 3*4+5
		{8, 29},  // 3*8+5
		{16, 32}, // 3*16+5 = 53 → capped at 32
		{0, planThenExecuteReadBudgetCeiling},
	}
	for _, tc := range cases {
		got := planThenExecutePlannerReadBudget(tc.paths)
		if got != tc.want {
			t.Errorf("planThenExecutePlannerReadBudget(%d) = %d, want %d", tc.paths, got, tc.want)
		}
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
	got, decorated := canonicalMutationBlockerFinalAnswer(brief, results)
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
	got, decorated := canonicalMutationBlockerFinalAnswer(brief, nil)
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
		Output:  "plan_path: /tmp/plans/x.plan.json\nedits: 2\nschema_version: idiom.plan.v1",
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
	for _, tool := range []string{"edit_file", "apply_patch", "read_file", "code_navigate", "submit_plan"} {
		got := implementationScopedEditAllowedTools([]string{tool})
		if len(got) != 1 || got[0] != tool {
			t.Fatalf("scoped-edit allowlist should pass %q through, got %v", tool, got)
		}
	}
	for _, tool := range []string{"shell", "git_status", "task", "search_content"} {
		got := implementationScopedEditAllowedTools([]string{tool})
		if len(got) != 0 {
			t.Fatalf("scoped-edit allowlist should drop %q, got %v", tool, got)
		}
	}
}
