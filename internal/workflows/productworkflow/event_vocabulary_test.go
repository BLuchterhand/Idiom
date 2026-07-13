package productworkflow

import (
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("event vocabulary", func() {
	ginkgo.It("ProductWorkflowBehavioralCoverageEventCoversPromotionStates", func() {
		for _, tt := range []struct {
			state string
			event ProductWorkflowEventType
		}{
			{BehavioralCoveragePromotionStateProposed, ProductWorkflowEventBehavioralCoverageProposed},
			{BehavioralCoveragePromotionStatePrepared, ProductWorkflowEventBehavioralCoveragePrepared},
			{BehavioralCoveragePromotionStateReviewed, ProductWorkflowEventBehavioralCoverageReviewed},
			{BehavioralCoveragePromotionStateAccepted, ProductWorkflowEventBehavioralCoverageAccepted},
			{BehavioralCoveragePromotionStateRejected, ProductWorkflowEventBehavioralCoverageRejected},
			{BehavioralCoveragePromotionStateSuperseded, ProductWorkflowEventBehavioralCoverageSuperseded},
		} {
			event, ok := ProductWorkflowBehavioralCoverageEvent(tt.state)
			gomega.Expect(!ok).To(gomega.BeFalse(), fmt.Sprintf("state %q has no declared event", tt.state))
			gomega.Expect(event != tt.event).To(gomega.BeFalse(), fmt.Sprintf("state %q mapped to event %q, want %q", tt.state, event, tt.event))
			gomega.Expect(!KnownProductWorkflowBehavioralCoverageEvent(event)).To(gomega.BeFalse(), fmt.Sprintf("event %q for state %q is not known", event, tt.state))
			gomega.Expect(!strings.HasPrefix(string(event), string(ProductWorkflowEventSourceBehavioralCoverage)+"_")).To(gomega.BeFalse(), fmt.Sprintf("event %q should use behavioral coverage prefix", event))

		}
		{

			event, ok := ProductWorkflowBehavioralCoverageEvent("surprise")
			gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("unknown state mapped to event %q", event))
		}
		gomega.Expect(KnownProductWorkflowBehavioralCoverageEvent(ProductWorkflowEventType("behavioral_coverage_surprise"))).To(gomega.BeFalse(), fmt.Sprint("unknown behavioral coverage event should not be known"))

	})
	ginkgo.It("ParseBehavioralCoveragePromotionEventsRejectsUnknownBehavioralEvent", func() {
		_, err := ParseBehavioralCoveragePromotionEventsJSONL([]byte(`{"event":"behavioral_coverage_surprise","event_id":"evt-1","state":"surprise","timestamp":"2026-06-26T00:00:00Z","source":"test"}` + "\n"))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown behavioral coverage event to be rejected"))
		gomega.Expect(!strings.Contains(err.Error(), "unknown behavioral coverage event")).To(gomega.BeFalse(), fmt.Sprintf("unexpected error: %v", err))

	})
})
