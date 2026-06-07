package featureplanner

import (
	"fmt"
	"strings"
)

// ApplyProductSourceSatisfiedCloseout folds a product workflow effort that is
// already closed/source-satisfied into the feature-planner terminal state.
func ApplyProductSourceSatisfiedCloseout(state *FeatureState) {
	if state == nil {
		return
	}
	state.Status = FeatureStateSourceSatisfied
	state.PlanningLoop.State = FeatureStateSourceSatisfied
	state.Blockers = nil
	state.OpenQuestions = nil
	state.PlanningLoop.OpenQuestionIDs = nil
	state.OpenRecoveryPackets = nil
}

func ProductSourceSatisfiedCloseout(state FeatureState, brief ProductWorkflowBrief, requestedEffortID string) (FeatureState, Event) {
	state.EffortRoot = strings.TrimSpace(brief.EffortRoot)
	state.EffortID = firstNonEmptyTrimmed(requestedEffortID, brief.EffortID, state.EffortID)
	activeRunDeltas := make([]ActiveRunDelta, 0, len(state.ActiveRuns))
	for _, run := range state.ActiveRuns {
		activeRunDeltas = append(activeRunDeltas, ActiveRunDelta{
			Op:            "fold_terminal",
			AtomID:        strings.TrimSpace(run.AtomID),
			LaneSessionID: strings.TrimSpace(firstNonEmptyTrimmed(run.LaneSessionID, run.RunID)),
			BeforeState:   strings.TrimSpace(run.State),
			AfterState:    FeatureStateSourceSatisfied,
		})
	}
	event := Event{
		Type:            EventProductSourceSatisfied,
		SessionID:       state.SessionID,
		Message:         productSourceSatisfiedMessage(brief),
		FeatureTo:       FeatureStateSourceSatisfied,
		ActiveRunDeltas: activeRunDeltas,
		Fields: map[string]any{
			"effort_id":          strings.TrimSpace(brief.EffortID),
			"effort_root":        strings.TrimSpace(brief.EffortRoot),
			"status_bucket":      strings.TrimSpace(brief.StatusBucket),
			"current_phase":      strings.TrimSpace(brief.CurrentPhase),
			"current_state":      strings.TrimSpace(brief.CurrentState),
			"next_valid_actions": append([]string(nil), brief.NextValidActions...),
		},
	}
	ApplyProductSourceSatisfiedCloseout(&state)
	state.ActiveRuns = nil
	state.DerivedNextValidActions = DeriveNextValidActions(state)
	return state, event
}

func productSourceSatisfiedMessage(brief ProductWorkflowBrief) string {
	return fmt.Sprintf("product workflow effort %s is already closed/source-satisfied (%s/%s); dispatch skipped", strings.TrimSpace(brief.EffortID), strings.TrimSpace(brief.CurrentPhase), strings.TrimSpace(brief.CurrentState))
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
