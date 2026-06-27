package planexecute

import (
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

func executorEditOutcome(result ExecutorEditResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return "FAILED: " + result.Error
	}

	return string(plancontract.PhaseEvidenceStatusOK)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}

	return ""
}
