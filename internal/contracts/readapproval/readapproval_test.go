package readapproval

import (
	"testing"
	"time"
)

func TestOutcomeString(t *testing.T) {
	cases := []struct {
		outcome Outcome
		want    string
	}{
		{OutcomeApproved, "approved"},
		{OutcomePromptRequired, "prompt_required"},
		{OutcomeDenied, "denied"},
		{Outcome(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.outcome.String(); got != c.want {
			t.Errorf("Outcome(%d).String() = %q, want %q", c.outcome, got, c.want)
		}
	}
}

func TestSessionDecisionsRecordAndLookup(t *testing.T) {
	decisions := NewSessionDecisions()

	if _, ok := decisions.Lookup("docs/foo.md"); ok {
		t.Fatal("empty session decisions should not contain any path")
	}

	decisions.Record(Decision{
		Outcome: OutcomeApproved,
		Reason:  ReasonOperatorApprovedLive,
		Source:  SourceOperator,
		At:      time.Now(),
		Path:    "docs/foo.md",
	})

	got, ok := decisions.Lookup("docs/foo.md")
	if !ok {
		t.Fatal("approved decision must be retrievable")
	}
	if got.Outcome != OutcomeApproved {
		t.Fatalf("expected OutcomeApproved, got %v", got.Outcome)
	}

	decisions.Record(Decision{
		Outcome: OutcomeDenied,
		Reason:  ReasonOperatorDeniedLive,
		Source:  SourceOperator,
		At:      time.Now(),
		Path:    "docs/foo.md",
	})

	got, ok = decisions.Lookup("docs/foo.md")
	if !ok {
		t.Fatal("denied decision must be retrievable")
	}
	if got.Outcome != OutcomeDenied {
		t.Fatalf("re-recording as denied must override approved state, got %v", got.Outcome)
	}
}

func TestSessionDecisionsPathSets(t *testing.T) {
	decisions := NewSessionDecisions()
	decisions.Record(Decision{Outcome: OutcomeApproved, Path: "a.md"})
	decisions.Record(Decision{Outcome: OutcomeApproved, Path: "b.md"})
	decisions.Record(Decision{Outcome: OutcomeDenied, Path: "c.md"})

	approved := decisions.ApprovedPathSet()
	if len(approved) != 2 {
		t.Fatalf("expected 2 approved paths, got %d", len(approved))
	}
	if _, ok := approved["a.md"]; !ok {
		t.Errorf("approved set missing a.md: %v", approved)
	}
	if _, ok := approved["b.md"]; !ok {
		t.Errorf("approved set missing b.md: %v", approved)
	}

	denied := decisions.DeniedPathSet()
	if len(denied) != 1 {
		t.Fatalf("expected 1 denied path, got %d", len(denied))
	}
}
