package lane

import "testing"

func TestLaneEventVocabulary(t *testing.T) {
	for _, eventType := range []EventType{EventStart, EventRunLedger, EventDegraded, EventAtomIntake} {
		if !KnownEventType(eventType) {
			t.Fatalf("KnownEventType(%q) = false", eventType)
		}
		if got := EventName(eventType); got != EventPrefix+string(eventType) {
			t.Fatalf("EventName(%q) = %q", eventType, got)
		}
	}

	if KnownEventType(EventType("surprise")) {
		t.Fatal("KnownEventType accepted an undeclared lane event")
	}
}
