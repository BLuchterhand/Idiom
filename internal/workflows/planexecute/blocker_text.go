package planexecute

import "strings"

func reviewAnswerDeclaresBlocker(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") || strings.Contains(lower, "decision: block") || strings.Contains(lower, "blocker")
}

func ReviewAnswerDeclaresBlocker(answer string) bool {
	return reviewAnswerDeclaresBlocker(answer)
}

func reviewRepairBlockerSummary(answer string) string {
	return planExecuteBlockerSummary(answer)
}

func planExecuteAnswerDeclaresBlocker(answer string) bool {
	answer = strings.TrimSpace(answer)

	lower := strings.ToLower(answer)

	return strings.Contains(answer, "<<QUESTION>>") ||
		strings.Contains(lower, "decision: block") ||
		strings.HasPrefix(lower, "blocker:") ||
		strings.Contains(lower, "\nblocker:")
}

func PlanExecuteAnswerDeclaresBlocker(answer string) bool {
	return planExecuteAnswerDeclaresBlocker(answer)
}

func planExecuteBlockerSummary(answer string) string {
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		const max = 240

		if len(line) > max {
			return line[:max] + "..."
		}

		return line
	}

	return "planner stopped with a blocker"
}

func PlanExecuteBlockerSummary(answer string) string {
	return planExecuteBlockerSummary(answer)
}
