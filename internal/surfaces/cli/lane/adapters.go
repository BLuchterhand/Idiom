package lane

import (
	"strings"

	"idiom/internal/evidence/lanehistory"
)

func laneRunFinalState(exitCode int, degradations []laneDegradation, out []byte) string {
	if exitCode != 0 {
		return lanehistory.FinalStateFailed
	}

	if len(degradations) > 0 {
		return lanehistory.FinalStateDegraded
	}

	if hasReviewRepairDegradedEvent(out) {
		return lanehistory.FinalStateDegraded
	}

	if hasSourceSatisfiedEvent(out) {
		return lanehistory.FinalStateSourceSatisfied
	}

	return lanehistory.FinalStateSuccess
}

func laneDegradationKinds(degradations []laneDegradation) []string {
	if len(degradations) == 0 {
		return nil
	}

	kinds := make([]string, 0, len(degradations))
	for _, degradation := range degradations {
		if strings.TrimSpace(degradation.kind) == "" {
			continue
		}

		kinds = append(kinds, degradation.kind)
	}

	if len(kinds) == 0 {
		return nil
	}

	return kinds
}
