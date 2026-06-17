package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"strings"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent/agentutil"
)

func reviewRepairTerminalOutcomeRequiredState(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return false
	}

	summary := strings.TrimSpace(result.Summary)

	return strings.EqualFold(summary, "Review-repair terminal outcome required") ||
		strings.HasPrefix(summary, "Review-repair completion gate rejected")
}

func reviewRepairTerminalValidNextActions() []string {
	return []string{
		"`Decision: pass` with a `Verification Checklist`",
		"a corrective `submit_plan` whose rationale starts with `Verification Checklist:`",
		"`<<QUESTION>>:` naming the exact blocker that prevents a terminal review-repair outcome",
	}
}

func reviewRepairTerminalInvalidNextActions() []string {
	return []string{
		"read/search/discovery/context tools after the review-repair closeout gate",
		"running commands after the review-repair closeout gate",
		"planning, summarizing, or adding prose before or after the terminal outcome",
	}
}

func applyScratchpadDeliverableCloseoutBias(state runnerNextActionState, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !scratchpadDeliverableCloseoutReady(brief, results) {
		return state
	}

	target := strings.Join(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results)), ", ")
	if target == "" {
		target = "the required deliverable"
	}

	state.Constraints = util.Merge(state.Constraints, []string{
		"scratchpad_deliverable_closeout_ready",
		"non_source_deliverable_path_unwritten",
	})
	state.EvidenceStatus = util.Merge(state.EvidenceStatus, []string{
		fmt.Sprintf("non-source deliverable still unwritten: %s; gathered evidence and accepted gaps should be recorded in the deliverable", target),
	})
	state.NextValidActions = []string{
		fmt.Sprintf("deliverable-write capability for %s with the corrected content, recording source uncertainty as Source verification pending or Scope Correction Required", target),
		"answer <<QUESTION>> only if the deliverable cannot be written from the gathered evidence plus recorded uncertainty",
	}
	state.InvalidNextActions = util.Merge(state.InvalidNextActions, []string{
		"new source discovery after completion gate rejected a prose-only non-source deliverable",
		"repairing an optional evidence lookup instead of recording the gap in the scratchpad deliverable",
		"final answer without write_file for the required non-source deliverable",
	})

	return state
}

func scratchpadDeliverableCloseoutReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !briefIsScratchpadDeliverableLane(brief) {
		return false
	}

	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) == 0 {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "completion_gate" {
			continue
		}

		lower := strings.ToLower(strings.TrimSpace(result.Summary + " " + result.Error))
		if strings.Contains(lower, "no successful write") ||
			strings.Contains(lower, "no successful write tool") ||
			strings.Contains(lower, "workspace change") ||
			strings.Contains(lower, "remain unwritten") {

			return true
		}
	}

	return false
}

func (r *Runner) scratchpadDeliverableCloseoutNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, target string, reject bool) {
	if r == nil || !scratchpadDeliverableCloseoutReady(r.RuntimeBrief, results) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", "", false
	}

	missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results))

	target = "the required deliverable"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	summary = "Scratchpad deliverable closeout blocked non-write tool"
	errorText = fmt.Sprintf("%s is not available after the completion gate rejected a prose-only response for non-source deliverable path(s): %s. The available contract outcomes are deliverable-write capability with full content or <<QUESTION>> with the exact blocker.", tool, target)
	prompt = fmt.Sprintf("The completion gate already determined that %s is still unwritten. Do not perform additional reads, searches, discovery, planning, or optional evidence repair from this recovery state. The deliverable-write contract records missing/stale/ambiguous evidence as `Source verification pending:` or `## Scope Correction Required`, or answers with <<QUESTION>> if the deliverable is impossible.", target)

	return summary, errorText, prompt, target, true
}

func (r *Runner) scratchpadDeliverableSourceDiscoveryRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !briefIsScratchpadDeliverableLane(r.RuntimeBrief) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", false
	}

	if len(r.remainingRequiredEditPaths(results)) == 0 || !r.scopedReadClassTool(req) {
		return "", "", "", false
	}

	if scratchpadDeliverablePrewriteReadAllowed(req) {
		return "", "", "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required deliverable"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "read/discovery tool"
	}

	summary = "Scratchpad deliverable source discovery redirected to write"
	errorText = fmt.Sprintf("%s is not available before writing non-source scratchpad deliverable path(s): %s. Read required scratchpad inputs only; record missing or uncertain source evidence in the deliverable instead of reopening source discovery.", tool, target)
	prompt = fmt.Sprintf("This lane is a non-source scratchpad writer/reviewer phase for %s. Source discovery is closed here: the deliverable-write contract uses the preloaded evidence already in the brief, records missing/stale/ambiguous facts as `Source verification pending:` or `## Scope Correction Required`, or answers with <<QUESTION>> and the exact blocker if the deliverable cannot be written from the scratchpad inputs plus recorded uncertainty.", target)

	return summary, errorText, prompt, true
}

func scratchpadDeliverablePrewriteReadAllowed(req workspace.ToolRequest) bool {
	path := agentutil.NormalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	return writePathIsNonSource(path)
}

func scratchpadFailedEvidenceFeedbackApplies(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if !briefIsScratchpadDeliverableLane(brief) || strings.TrimSpace(result.Error) == "" {
		return false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return false
	}

	return len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) > 0
}
