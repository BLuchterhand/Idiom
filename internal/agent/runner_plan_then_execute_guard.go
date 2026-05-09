package agent

import (
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// PlanThenExecutePlannerTaskFamily is the runtime-brief task_family
// that marks the planner phase of `idiom plan-execute`. The runner
// uses it to forbid per-edit write tools (edit_file, write_file,
// append_file, apply_patch, delegate_edit) — the planner's only
// writes go through submit_plan. The executor phase, which runs in a
// separate process via the orchestrator in cmd/idiom/plan_execute.go,
// uses the workspace dispatch directly.
const PlanThenExecutePlannerTaskFamily = "plan_then_execute_planner"

// planThenExecutePlannerPhase reports whether the runner is currently
// servicing the planner half of plan-then-execute mode.
func (r *Runner) planThenExecutePlannerPhase() bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecutePlannerTaskFamily
}

// preflightPlanThenExecuteWriteGuard rejects per-edit write tools
// during the planner phase of plan-then-execute mode and steers the
// model at submit_plan instead. The system addendum the orchestrator
// injects is a soft instruction; this guard is the hard one — even
// if the model ignores the prompt, the tool call is intercepted and
// converted into a directional rejection.
//
// Returns Intercepted=false (and an empty result) for any other lane
// shape, so the cost when the guard is inactive is a single string
// comparison.
func (r *Runner) preflightPlanThenExecuteWriteGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlannerPhase() {
		return verificationInterceptResult{}
	}
	tool := strings.TrimSpace(req.Tool)
	if !planThenExecuteForbiddenWriteTool(tool) {
		return verificationInterceptResult{}
	}
	directive := "plan-then-execute planner phase: per-edit write tools are unavailable. " +
		"Enumerate every change in a single submit_plan call and stop. " +
		"The orchestrator's executor phase will dispatch each edit deterministically after you submit the plan."
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected per-edit write",
			Error:   directive,
		},
		Reason: "plan-then-execute planner phase: write tool " + tool + " redirected to submit_plan",
	}
}

// planThenExecuteForbiddenWriteTool lists the per-edit write tools
// the planner phase must not call. submit_plan is OUR tool, not a
// per-edit write — it captures the structured plan.
func planThenExecuteForbiddenWriteTool(tool string) bool {
	switch tool {
	case "edit_file", "write_file", "append_file", "apply_patch", "delegate_edit", "go_move_declarations", "mkdir":
		return true
	}
	return false
}

// successfulSubmitPlanResultCount returns the number of successful
// submit_plan tool results in the lane's recorded history. The
// completion gate uses this as the "the planner did its job" signal
// for plan-then-execute mode, replacing the usual changed-paths check
// (the planner's submit_plan call doesn't change files; the executor
// phase does, in a separate process).
func successfulSubmitPlanResultCount(results []workspace.ToolResult) int {
	count := 0
	for _, r := range results {
		if strings.TrimSpace(r.Tool) != "submit_plan" {
			continue
		}
		if strings.TrimSpace(r.Error) != "" {
			continue
		}
		count++
	}
	return count
}

// planThenExecuteReadyToSubmitNudge returns a system-message string
// the runner injects into the next turn when the planner phase has
// read every required_edit_path at least once and has not yet called
// submit_plan. The nudge converts the planner's verification urge
// into closeout pressure: instead of letting the model spend more
// reads narrowing anchors that already exist in the brief, it gets a
// directive saying the state is ready and the next action MUST be
// submit_plan or <<QUESTION>>.
//
// Returns "" when the nudge does not apply (wrong lane, plan already
// submitted, paths still unread, etc.). The runner emits the nudge
// at most once per turn-since-coverage; it is the caller's job to
// gate against re-injection on every subsequent tool result.
func planThenExecuteReadyToSubmitNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return ""
	}
	required := brief.RequiredEditPaths
	if len(required) == 0 {
		return ""
	}
	if successfulSubmitPlanResultCount(results) > 0 {
		// Plan already submitted — the gate-exemption path will close
		// the runner cleanly; nothing to nudge.
		return ""
	}
	read := readPathsFromResults(results)
	if len(read) == 0 {
		return ""
	}
	for _, path := range required {
		if _, ok := read[normalizeRunnerPath(path)]; !ok {
			return ""
		}
	}
	return strings.Join([]string{
		"PLANNER READINESS REACHED: every required_edit_path has been read at least once in this turn. State is sufficient to submit the plan.",
		"",
		"Next action MUST be one of:",
		"  1. `submit_plan` with the full ordered edits[] array (substring or line-range shape, plus a one-sentence purpose per edit), then a short final answer summarizing the planned edits.",
		"  2. `<<QUESTION>>:` with the exact path-specific blocker that prevents you from drafting the plan.",
		"",
		"Do NOT issue another read tool call. You already have anchors for every required path; additional reads will not improve the plan and will exhaust the read budget.",
	}, "\n")
}

// readPathsFromResults returns the set of paths that have been
// successfully read in this lane (read_file, code_navigate read_*,
// *_file_outline / *_read_symbol, etc.). Used by the planner readiness
// nudge to check whether every required path has been touched.
func readPathsFromResults(results []workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{}, len(results))
	for _, r := range results {
		if strings.TrimSpace(r.Error) != "" {
			continue
		}
		path := strings.TrimSpace(extractReadPathFromSummary(r.Summary))
		if path == "" {
			continue
		}
		paths[normalizeRunnerPath(path)] = struct{}{}
	}
	return paths
}

// extractReadPathFromSummary parses the canonical "Read ..." summary
// shapes that read_file and the language-symbol read tools emit to
// recover the path. Recognized shapes:
//
//   - "Read fXX path/to/file lines A-B"  (read_file)
//   - "Read <symbol> in path/to/file lines A-B"  (cpp_read_symbol /
//     go_read_symbol / etc.)
//
// Returns "" for anything else; the caller falls back to skipping
// that result. code_navigate's wrap summary ("Code navigation routed
// read_symbol via cpp_read_symbol") does not carry the path —
// callers that want to count code_navigate reads should also
// inspect the wrap's Output, but in practice plan-execute uses
// read_file as the dominant evidence-gathering tool.
func extractReadPathFromSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	fields := strings.Fields(summary)
	if len(fields) < 3 || fields[0] != "Read" {
		return ""
	}
	// "Read <symbol> in <path> ..." — language-symbol shape.
	if len(fields) >= 4 && fields[2] == "in" {
		return fields[3]
	}
	// "Read fXX path lines A-B" — read_file shape.
	return fields[2]
}

