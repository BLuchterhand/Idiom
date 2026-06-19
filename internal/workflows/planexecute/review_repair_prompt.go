package planexecute

import "strings"

func planExecuteReviewRepairRejectedPassRetryPrompt(basePrompt string, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "phase evidence contradicted the pass decision"
	}

	return strings.Join([]string{
		basePrompt,
		"",
		"## Rejected Previous Review Outcome",
		"",
		"The previous review-repair answer used `Decision: pass`, but the phase evidence rejected that pass decision: " + truncatePlanExecuteText(reason, 2000) + ".",
		"",
		"For this retry, `Decision: pass` is not an allowed terminal outcome. Choose exactly one visible outcome:",
		"- Corrective plan: call `submit_plan` with only the executable corrective edits needed to resolve the rejected checklist item. The rationale must start with `Verification Checklist:`.",
		"- Blocker: return `<<QUESTION>>:` with the exact failed checklist item if it cannot be repaired inside Allowed Repair Paths.",
		"",
		"Do not repeat `Decision: pass`. Do not add prose before or after the corrective plan or blocker.",
	}, "\n")
}

func planExecuteReviewRepairInvalidOutcomeRetryPrompt(basePrompt string, answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "(empty answer)"
	}

	return strings.Join([]string{
		basePrompt,
		"",
		"## Rejected Previous Review Outcome",
		"",
		"The previous review-repair answer did not produce a machine-readable terminal outcome. It was rejected because review-repair must close with one of the explicit outcome shapes.",
		"",
		"Rejected answer excerpt:",
		"```text",
		truncatePlanExecuteText(answer, 2000),
		"```",
		"",
		"For this retry, choose exactly one visible outcome:",
		"- Corrective plan: call `submit_plan` with only the executable corrective edits needed to resolve failed or open checklist items. The rationale must start with `Verification Checklist:`.",
		"- Pass: answer `Decision: pass` with a `Verification Checklist` only if the Phase Evidence Bundle has no failed or open obligations.",
		"- Blocker: return `<<QUESTION>>:` with the exact failed or open checklist item if it cannot be repaired inside Allowed Repair Paths.",
		"",
		"Do not answer with a read-only explanation, source inventory, summary, or prose outside the chosen terminal shape.",
	}, "\n")
}
