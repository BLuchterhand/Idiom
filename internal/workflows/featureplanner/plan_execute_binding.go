package featureplanner

import "strings"

func PlanExecuteSessionIDFromImplementorPrompt(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(strings.ToLower(key)) != "plan-execute session id" {
			continue
		}

		sessionID := strings.TrimSpace(value)
		if validPlanExecuteSessionID(sessionID) {
			return sessionID
		}
	}

	return ""
}

func validPlanExecuteSessionID(sessionID string) bool {
	if sessionID == "" || len(sessionID) > 128 || strings.Contains(sessionID, "..") {
		return false
	}

	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}

	return true
}
