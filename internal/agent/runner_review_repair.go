package agent

import (
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/plancontract"
	"idiom/internal/workspace"
)

func (r *Runner) planThenExecuteReviewRepairCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return workspace.ToolResult{}, false
	}
	if successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolResult{}, false
	}
	if planThenExecuteReviewRepairAnswerDeclaresBlocker(reply) {
		return workspace.ToolResult{}, false
	}
	if plancontract.ReviewRepairAnswerDeclaresPass(reply) && plancontract.ReviewRepairTextHasChecklist(reply) {
		if failed := planThenExecuteReviewRepairFailedPhaseObligations(r.RuntimeBrief); len(failed) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Review-repair completion gate rejected contradictory pass",
				Error:   "review-repair phase evidence still has failed obligations " + quotedRunnerStrings(failed) + "; choose a corrective submit_plan inside allowed paths or `<<QUESTION>>:` with the exact blocker instead of `Decision: pass`.",
			}, true
		}
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error: strings.Join([]string{
			"review-repair must close with exactly one of: a successful submit_plan corrective plan,",
			"`Decision: pass` with a `Verification Checklist`,",
			"or `<<QUESTION>>:` naming the exact blocker.",
			"Do not stop with process narration or an intent to inspect.",
		}, " "),
	}, true
}

func planThenExecuteReviewRepairFailedPhaseObligations(brief workerruntime.WorkerRuntimeBrief) []string {
	if brief.Readiness == nil {
		return nil
	}
	out := make([]string, 0)
	for _, obligation := range brief.Readiness.OpenObligations {
		obligation = strings.TrimSpace(obligation)
		switch {
		case obligation == "patch_hygiene_failed":
			out = append(out, obligation)
		case strings.HasPrefix(obligation, "verification_failed:"),
			strings.HasPrefix(obligation, "verification_error:"),
			strings.HasPrefix(obligation, "source_sanity_failed:"),
			strings.HasPrefix(obligation, "source_sanity_error:"),
			strings.HasPrefix(obligation, "submitted_edit_failed:"):
			out = append(out, obligation)
		}
	}
	return out
}

func planThenExecuteReviewRepairAnswerDeclaresBlocker(answer string) bool {
	return planThenExecuteAnswerDeclaresTypedBlocker(answer)
}
