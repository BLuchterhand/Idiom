package productworkflow

import (
	"strings"
	"testing"
)

func TestProductWorkflowBehavioralCoverageEventCoversPromotionStates(t *testing.T) {
	for _, state := range []string{
		BehavioralCoveragePromotionStateProposed,
		BehavioralCoveragePromotionStatePrepared,
		BehavioralCoveragePromotionStateReviewed,
		BehavioralCoveragePromotionStateAccepted,
		BehavioralCoveragePromotionStateRejected,
		BehavioralCoveragePromotionStateSuperseded,
	} {
		event, ok := ProductWorkflowBehavioralCoverageEvent(state)
		if !ok {
			t.Fatalf("state %q has no declared event", state)
		}
		if !KnownProductWorkflowBehavioralCoverageEvent(event) {
			t.Fatalf("event %q for state %q is not known", event, state)
		}
		if !strings.HasPrefix(string(event), string(ProductWorkflowEventSourceBehavioralCoverage)+"_") {
			t.Fatalf("event %q should use behavioral coverage prefix", event)
		}
	}

	if event, ok := ProductWorkflowBehavioralCoverageEvent("surprise"); ok {
		t.Fatalf("unknown state mapped to event %q", event)
	}
}

func TestParseBehavioralCoveragePromotionEventsRejectsUnknownBehavioralEvent(t *testing.T) {
	_, err := ParseBehavioralCoveragePromotionEventsJSONL([]byte(`{"event":"behavioral_coverage_surprise","event_id":"evt-1","state":"surprise","timestamp":"2026-06-26T00:00:00Z","source":"test"}` + "\n"))
	if err == nil {
		t.Fatal("expected unknown behavioral coverage event to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown behavioral coverage event") {
		t.Fatalf("unexpected error: %v", err)
	}
}
