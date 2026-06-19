package planexecute

import "strings"

func reviewAnswerDeclaresBlocker(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") || strings.Contains(lower, "decision: block") || strings.Contains(lower, "blocker")
}

func reviewRepairBlockerSummary(answer string) string {
	return planExecuteBlockerSummary(answer)
}

func reviewRepairBlockerFailureReason(answer string) string {
	return planExecuteBlockerFailureReason(answer)
}

func planExecuteAnswerDeclaresBlocker(answer string) bool {
	answer = strings.TrimSpace(answer)

	lower := strings.ToLower(answer)

	return strings.Contains(answer, "<<QUESTION>>") ||
		strings.Contains(lower, "decision: block") ||
		strings.HasPrefix(lower, "blocker:") ||
		strings.Contains(lower, "\nblocker:")
}

func planExecuteBlockerSummary(answer string) string {
	if explicit := explicitBlockerLine(answer); explicit != "" {
		return truncateBlockerLine(explicit)
	}

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		return truncateBlockerLine(line)
	}

	return "planner stopped with a blocker"
}

func explicitBlockerLine(answer string) string {
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(line, "<<QUESTION>>"):
			return line
		case strings.HasPrefix(lower, "blocker:"):
			return line
		case strings.HasPrefix(lower, "decision: block"):
			return line
		}
	}

	return ""
}

func truncateBlockerLine(line string) string {
	const max = 240

	if len(line) > max {
		return line[:max] + "..."
	}

	return line
}

func planExecuteBlockerFailureReason(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return planExecuteBlockerSummary(answer)
	}

	return sanitizeProgressReason(answer)
}
