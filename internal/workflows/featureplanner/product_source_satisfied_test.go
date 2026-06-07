package featureplanner

import "testing"

func TestProductSourceSatisfiedCloseoutBuildsTerminalStateAndEvent(t *testing.T) {
	state := FeatureState{
		SessionID: "session-1",
		Status:    FeatureStateDispatching,
		PlanningLoop: PlanningLoop{
			State:           FeatureStateDispatching,
			OpenQuestionIDs: []string{"q-1"},
		},
		EffortID:            "state-effort",
		OpenQuestions:       []PlannerQuestion{{ID: "q-1", Status: "open"}},
		OpenRecoveryPackets: []string{"recovery.json"},
		Blockers:            []string{"stale blocker"},
		ActiveRuns: []ActiveRun{{
			RunID:         "run-1",
			AtomID:        "atom-1",
			LaneSessionID: "lane-1",
			State:         "running",
		}},
	}
	brief := ProductWorkflowBrief{
		EffortID:         "brief-effort",
		EffortRoot:       " /tmp/effort ",
		StatusBucket:     "closed",
		CurrentPhase:     "source-satisfied-closeout",
		CurrentState:     "closed",
		NextValidActions: []string{"inspect_closed_effort"},
	}

	next, event := ProductSourceSatisfiedCloseout(state, brief, " requested-effort ")

	if next.Status != FeatureStateSourceSatisfied || next.PlanningLoop.State != FeatureStateSourceSatisfied {
		t.Fatalf("expected source-satisfied state, got status=%q loop=%q", next.Status, next.PlanningLoop.State)
	}
	if next.EffortID != "requested-effort" || next.EffortRoot != "/tmp/effort" {
		t.Fatalf("unexpected effort identity: id=%q root=%q", next.EffortID, next.EffortRoot)
	}
	if len(next.Blockers) != 0 || len(next.OpenQuestions) != 0 || len(next.OpenRecoveryPackets) != 0 || len(next.PlanningLoop.OpenQuestionIDs) != 0 || len(next.ActiveRuns) != 0 {
		t.Fatalf("closeout should clear terminal-incompatible state: %#v", next)
	}
	if event.Type != EventProductSourceSatisfied || event.SessionID != "session-1" || event.FeatureTo != FeatureStateSourceSatisfied {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if len(event.ActiveRunDeltas) != 1 || event.ActiveRunDeltas[0].Op != "fold_terminal" || event.ActiveRunDeltas[0].AfterState != FeatureStateSourceSatisfied {
		t.Fatalf("expected active run fold delta, got %#v", event.ActiveRunDeltas)
	}
	if event.Fields["effort_id"] != "brief-effort" || event.Fields["current_phase"] != "source-satisfied-closeout" {
		t.Fatalf("unexpected event fields: %#v", event.Fields)
	}
}
