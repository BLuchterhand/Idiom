package featureplanner

import (
	"strings"

	"idiom/internal/platform/sessionid"
)

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
	return sessionid.Valid(sessionID)
}
