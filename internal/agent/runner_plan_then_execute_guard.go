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

// PlanThenExecuteReviewRepairTaskFamily marks the post-executor
// action-capable review phase. It shares the submit_plan surface for
// corrective edits, but unlike the planner phase it may also close with a
// direct `Decision: pass` answer when the evidence checklist is satisfied.
const PlanThenExecuteReviewRepairTaskFamily = "plan_then_execute_review_repair"

// planThenExecutePlannerPhase reports whether the runner is currently
// servicing the planner half of plan-then-execute mode.
func (r *Runner) planThenExecutePlannerPhase() bool {
	if r == nil {
		return false
	}
	return planThenExecutePlannerBrief(r.RuntimeBrief)
}

func (r *Runner) planThenExecutePlanSubmissionPhase() bool {
	if r == nil {
		return false
	}
	switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func planThenExecutePlannerBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	return strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily
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
	if !r.planThenExecutePlanSubmissionPhase() {
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

// preflightPlanThenExecuteSubmitPlanCoverageGuard validates the
// planner's structured plan against the same required_edit_paths
// contract that normal write tools obey. submit_plan is not a
// per-edit write, so the normal write-scope guard cannot see the
// paths it will later cause the executor to mutate. This guard keeps
// the invariant deterministic while still giving the planner a normal
// recoverable tool error it can fix with a new submit_plan call.
func (r *Runner) preflightPlanThenExecuteSubmitPlanCoverageGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}
	required := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)
	if len(required) == 0 {
		return verificationInterceptResult{}
	}
	planned := make(map[string]struct{}, len(req.Edits))
	var outside []string
	for _, edit := range req.Edits {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			continue
		}
		planned[path] = struct{}{}
		if _, ok := required[path]; !ok {
			outside = append(outside, path)
		}
	}
	var missing []string
	for _, path := range r.RuntimeBrief.RequiredEditPaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := planned[normalized]; !ok {
			missing = append(missing, normalized)
		}
	}
	if len(missing) == 0 && len(outside) == 0 {
		return verificationInterceptResult{}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing required edit path(s): "+quotedRunnerStrings(missing))
	}
	if len(outside) > 0 {
		parts = append(parts, "path(s) outside required_edit_paths: "+quotedRunnerStrings(outside))
	}
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error: "submit_plan must include at least one edit entry for every required_edit_paths entry and no edits outside that scope; " +
				strings.Join(parts, "; ") +
				". Submit a corrected plan, or answer with <<QUESTION>> and name the exact blocker for each skipped required path.",
		},
		Reason: "plan-then-execute planner phase: submit_plan edit paths did not satisfy required_edit_paths",
	}
}

// preflightPlanThenExecutePlannerToolGuard keeps verification and shell
// affordances out of the planner phase. The planner can gather bounded
// source evidence and submit a plan, report source-satisfied evidence, or
// ask a blocker question; build/test/diff commands belong to the
// orchestrator/review side of the plan-execute workflow.
func (r *Runner) preflightPlanThenExecutePlannerToolGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlannerPhase() {
		return verificationInterceptResult{}
	}
	tool := strings.TrimSpace(req.Tool)
	if !planThenExecutePlannerForbiddenTool(tool) {
		return verificationInterceptResult{}
	}
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected verification tool",
			Error: "plan-then-execute planner phase does not run shell, git, build, test, source-sanity, delegation, or task-ledger commands. " +
				"Use submit_plan, answer `Decision: source-satisfied` with per-path evidence when the requested final state is already present, or answer <<QUESTION>> with the exact blocker.",
		},
		Reason: "plan-then-execute planner phase: verification/shell tool " + tool + " is outside the planner contract",
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

func planThenExecutePlannerForbiddenTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "shell", "git_diff", "git_status", "source_sanity", "affected_suites", "context_trace_pack", "task", "task_ledger", "pwd":
		return true
	default:
		return false
	}
}

// planThenExecuteShortCircuitAnswer synthesizes the planner-phase
// final answer from a successful submit_plan tool result, letting the
// runner exit immediately without spending another model call on a
// summary that's redundant with the persisted plan artifact.
//
// Returns ("", false) when the short-circuit doesn't apply (wrong
// lane, no successful submit_plan, etc.). When it returns a non-empty
// answer, the runner should call finish(answer, nil,
// "plan_submitted_short_circuit", false).
//
// Why this exists: the planner's natural step-2 output is a short
// "Plan submitted with N edits across M files: ..." paragraph. That
// summary is already implicit in the plan artifact (the executor
// phase will dispatch each edit and emit per-edit JSON events with
// path + status). Asking the model to retype it costs ~4s and ~200
// tokens for zero new information.
func planThenExecuteShortCircuitAnswer(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult) (string, bool) {
	taskFamily := strings.TrimSpace(brief.TaskFamily)
	if taskFamily != PlanThenExecutePlannerTaskFamily && taskFamily != PlanThenExecuteReviewRepairTaskFamily {
		return "", false
	}
	if strings.TrimSpace(req.Tool) != "submit_plan" {
		return "", false
	}
	if strings.TrimSpace(result.Error) != "" {
		return "", false
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "Plan submitted; the executor phase will dispatch the planned edits."
	}
	body := []string{
		summary,
		"",
		"The plan artifact is on disk under .idiom/plans/. The orchestrator's executor phase will dispatch each edit deterministically and emit per-edit telemetry. No further planner action required.",
	}
	if details := strings.TrimSpace(result.Output); details != "" {
		body = append(body, "", details)
	}
	return strings.Join(body, "\n"), true
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

// PlanThenExecuteSourceSatisfiedAnswerCoversPaths reports whether a planner
// answer explicitly claims the requested final source state is already present
// and names every required path. It intentionally checks the answer shape only;
// Runner.planThenExecuteSourceSatisfiedAnswerCovers adds tool/workspace
// evidence before the completion gate accepts the answer.
func PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer string, requiredPaths []string) bool {
	if !planThenExecuteSourceSatisfiedAnswerIntent(answer) {
		return false
	}
	required := normalizedRequiredEditPathSet(requiredPaths)
	if len(required) == 0 {
		return false
	}
	lowerAnswer := strings.ToLower(answer)
	for path := range required {
		if !strings.Contains(lowerAnswer, strings.ToLower(path)) {
			return false
		}
	}
	return true
}

func planThenExecuteSourceSatisfiedAnswerIntent(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}
	firstLine := trimmed
	if idx := strings.IndexAny(firstLine, "\r\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	firstLine = strings.ToLower(strings.TrimSpace(firstLine))
	if firstLine != "decision: source-satisfied" && firstLine != "decision: source satisfied" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return containsAny(lower, "no change", "no changes", "nothing to change", "no-op", "no op", "stale spec", "stale anchor", "stale old_text")
}

func (r *Runner) planThenExecuteSourceSatisfiedAnswerCovers(reply string, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}
	missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results))
	if !PlanThenExecuteSourceSatisfiedAnswerCoversPaths(reply, missing) {
		return false
	}
	if r.planThenExecuteRequiredEditSnippetsSourceSatisfied() {
		return true
	}
	return planThenExecuteSourceSatisfiedResultsCoverPaths(missing, results)
}

func planThenExecuteSourceSatisfiedResultsCoverPaths(requiredPaths []string, results []workspace.ToolResult) bool {
	read := readPathsFromResults(results)
	if len(read) == 0 {
		return false
	}
	for _, path := range requiredPaths {
		if _, ok := read[normalizeRunnerPath(path)]; !ok {
			return false
		}
	}
	return true
}

func (r *Runner) planThenExecuteRequiredEditSnippetsSourceSatisfied() bool {
	if r == nil || r.Workspace == nil {
		return false
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return false
	}
	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}
	contentCache := make(map[string]string)
	for _, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, nil)
		if len(candidates) == 0 {
			return false
		}
		if !requiredEditSnippetPresent(root, candidates, snippet.Content, contentCache) {
			return false
		}
	}
	return true
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
		"PLANNER READINESS REACHED: every required_edit_path has been read at least once in this turn. State is sufficient to submit the plan or close as source-satisfied.",
		"",
		"Next action MUST be one of:",
		"  1. `submit_plan` with the full ordered edits[] array (substring, line-range, or whole-file/new-file shape, plus a one-sentence purpose per edit), then a short final answer summarizing the planned edits. For a missing/new source or test file, the brief must provide a substantial inline body or explicit replacement edit shape.",
		"  2. `Decision: source-satisfied` naming every required path and the observed current-state evidence when the requested final state is already present.",
		"  3. `<<QUESTION>>:` with the exact path-specific blocker that prevents you from drafting the plan.",
		"",
		"Default to submitting now. Only read again if you can name one exact new edit-safety fact that is still missing after the required path reads.",
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
