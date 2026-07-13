package featureplanner

import (
	"fmt"
	featureplannercontract "idiom/internal/contracts/featureplanner"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("product source satisfied closeout builds terminal state and event", func() {
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
		TaskID:           "brief-task",
		EffortRoot:       " /tmp/effort ",
		StatusBucket:     "closed",
		CurrentPhase:     "source-satisfied-closeout",
		CurrentState:     "closed",
		NextValidActions: []string{"inspect_closed_effort"},
	}

	next, event := ProductSourceSatisfiedCloseout(state, brief, " requested-effort ")
	gomega.Expect(next.Status).To(gomega.Equal(FeatureStateSourceSatisfied), fmt.Sprintf("expected source-satisfied state, got status=%q loop=%q", next.Status, next.PlanningLoop.State))
	gomega.Expect(next.PlanningLoop.State).To(gomega.Equal(FeatureStateSourceSatisfied), fmt.Sprintf("expected source-satisfied state, got status=%q loop=%q", next.Status, next.PlanningLoop.State))
	gomega.Expect(next.EffortID).To(gomega.Equal("requested-effort"), fmt.Sprintf("unexpected effort identity: id=%q root=%q", next.EffortID, next.EffortRoot))
	gomega.Expect(next.TaskID).To(gomega.Equal("brief-task"))
	gomega.Expect(next.EffortRoot).To(gomega.Equal("/tmp/effort"), fmt.Sprintf("unexpected effort identity: id=%q root=%q", next.EffortID, next.EffortRoot))
	gomega.Expect(next.Blockers).To(gomega.HaveLen(0), fmt.Sprintf("closeout should clear terminal-incompatible state: %#v", next))
	gomega.Expect(next.OpenQuestions).To(gomega.HaveLen(0), fmt.Sprintf("closeout should clear terminal-incompatible state: %#v", next))
	gomega.Expect(next.OpenRecoveryPackets).To(gomega.HaveLen(0), fmt.Sprintf("closeout should clear terminal-incompatible state: %#v", next))
	gomega.Expect(next.PlanningLoop.OpenQuestionIDs).To(gomega.HaveLen(0), fmt.Sprintf("closeout should clear terminal-incompatible state: %#v", next))
	gomega.Expect(next.ActiveRuns).To(gomega.HaveLen(0), fmt.Sprintf("closeout should clear terminal-incompatible state: %#v", next))
	gomega.Expect(event.Type).To(gomega.Equal(featureplannercontract.EventName(featureplannercontract.EventProductSourceSatisfied)), fmt.Sprintf("unexpected event identity: %#v", event))
	gomega.Expect(event.SessionID).To(gomega.Equal("session-1"), fmt.Sprintf("unexpected event identity: %#v", event))
	gomega.Expect(event.FeatureTo).To(gomega.Equal(FeatureStateSourceSatisfied), fmt.Sprintf("unexpected event identity: %#v", event))
	gomega.Expect(event.ActiveRunDeltas).To(gomega.HaveLen(1), fmt.Sprintf("expected active run fold delta, got %#v", event.ActiveRunDeltas))
	gomega.Expect(event.ActiveRunDeltas[0].Op).To(gomega.Equal("fold_terminal"), fmt.Sprintf("expected active run fold delta, got %#v", event.ActiveRunDeltas))
	gomega.Expect(event.ActiveRunDeltas[0].AfterState).To(gomega.Equal(FeatureStateSourceSatisfied), fmt.Sprintf("expected active run fold delta, got %#v", event.ActiveRunDeltas))
	gomega.Expect(event.Fields["effort_id"]).To(gomega.Equal("brief-effort"), fmt.Sprintf("unexpected event fields: %#v", event.Fields))
	gomega.Expect(event.Fields["task_id"]).To(gomega.Equal("brief-task"))
	gomega.Expect(event.Fields["current_phase"]).To(gomega.Equal("source-satisfied-closeout"), fmt.Sprintf("unexpected event fields: %#v", event.Fields))

})
var _ = ginkgo.It("product source satisfied closeout preserves an established task identity", func() {
	state := FeatureState{TaskID: "established-task"}
	brief := ProductWorkflowBrief{TaskID: "current-brief-task"}

	next, _ := ProductSourceSatisfiedCloseout(state, brief, "")
	gomega.Expect(next.TaskID).To(gomega.Equal("established-task"))
})
