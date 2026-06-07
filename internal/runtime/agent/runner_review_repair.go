package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/plan"
	"idiom/internal/runtime/workerruntime"
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

func (r *Runner) reviewRepairDoomLoopTerminalCloseout(results []workspace.ToolResult) (answer string, stopReason string, exhausted bool, ok bool) {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return "", "", false, false
	}
	if reviewRepairCompletionGateRejectionCount(results) == 0 {
		return "", "", false, false
	}
	answer = reviewRepairNoopSubmitPlanDeterministicCloseout(r.RuntimeBrief, workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error:   "review-repair repeated discovery after the terminal closeout gate instead of choosing `Decision: pass`, corrective `submit_plan`, or `<<QUESTION>>:`.",
	})
	if strings.HasPrefix(strings.TrimSpace(answer), "Decision: pass") {
		return answer, "review_repair_terminal_doom_loop_pass", false, true
	}
	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>:") {
		return answer, "review_repair_terminal_doom_loop_blocker", true, true
	}
	return "", "", false, false
}

func reviewRepairPhaseEvidencePacket(objective string) []string {
	evidence := reviewRepairPhaseEvidenceFromObjective(objective)
	if evidence == "" {
		return nil
	}
	return []string{"phase_evidence_bundle:\n" + evidence}
}

func reviewRepairTrustedPhaseEvidence(brief workerruntime.WorkerRuntimeBrief) string {
	if evidence := reviewRepairPhaseEvidenceFromObjective(brief.Objective); evidence != "" {
		return evidence
	}
	for _, packet := range brief.TargetPacket {
		packet = strings.TrimSpace(packet)
		if !strings.HasPrefix(packet, "phase_evidence_bundle:") {
			continue
		}
		evidence := strings.TrimSpace(strings.TrimPrefix(packet, "phase_evidence_bundle:"))
		if evidence == "" {
			continue
		}
		if len(evidence) > 12000 {
			evidence = evidence[:12000]
		}
		return evidence
	}
	return ""
}

func reviewRepairPhaseEvidenceFromObjective(objective string) string {
	const heading = "## Phase Evidence Bundle"
	idx := strings.Index(objective, heading)
	if idx < 0 {
		return ""
	}
	afterHeading := objective[idx+len(heading):]
	fenceStart := strings.Index(afterHeading, "```")
	if fenceStart < 0 {
		return ""
	}
	afterFence := afterHeading[fenceStart+len("```"):]
	if lineEnd := strings.Index(afterFence, "\n"); lineEnd >= 0 {
		afterFence = afterFence[lineEnd+1:]
	}
	fenceEnd := strings.Index(afterFence, "```")
	if fenceEnd < 0 {
		return ""
	}
	evidence := strings.TrimSpace(afterFence[:fenceEnd])
	if evidence == "" {
		return ""
	}
	if len(evidence) > 12000 {
		evidence = evidence[:12000]
	}
	return evidence
}

func planThenExecuteReviewRepairAnswerDeclaresBlocker(answer string) bool {
	return planThenExecuteAnswerDeclaresTypedBlocker(answer)
}
