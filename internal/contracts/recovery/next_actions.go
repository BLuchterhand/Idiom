// Package recovery defines stable recovery contract vocabulary shared by
// workflow recovery records and lane-specific terminal adapters.
package recovery

import "strings"

type NextAction string

const (
	NextActionReviewDecomposition NextAction = "review_decomposition"
	NextActionAnswerBlocker       NextAction = "answer_blocker"
	NextActionRetryPlanExecute    NextAction = "retry_plan_execute"
	NextActionInspectFailure      NextAction = "inspect_failure"
	NextActionHumanIntervention   NextAction = "human_intervention"
	NextActionNone                NextAction = "none"
)

func ParseNextAction(action string) (NextAction, bool) {
	value := NextAction(action)
	for _, known := range NextActions() {
		if value == known || canonicalToken(string(value)) == canonicalToken(string(known)) {
			return known, true
		}
	}

	return "", false
}

func NextActions() []NextAction {
	return []NextAction{
		NextActionReviewDecomposition,
		NextActionAnswerBlocker,
		NextActionRetryPlanExecute,
		NextActionInspectFailure,
		NextActionHumanIntervention,
		NextActionNone,
	}
}

func canonicalToken(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "_")
}
