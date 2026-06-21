package readapproval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestNoOpDeciderReturnsErrNoDecider(t *testing.T) {
	d := NoOpDecider{}
	decision, err := d.DecideRead(context.Background(), Request{SessionID: "s1", Path: "docs/foo.md"})
	if !errors.Is(err, ErrNoDecider) {
		t.Fatalf("expected ErrNoDecider, got %v", err)
	}
	if decision.Outcome != OutcomeDenied {
		t.Fatalf("expected OutcomeDenied, got %v", decision.Outcome)
	}
	if decision.Reason != ReasonNoDeciderWired {
		t.Fatalf("expected ReasonNoDeciderWired, got %q", decision.Reason)
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

func TestFileCacheLookupMissesForUnknownSession(t *testing.T) {
	cache := NewFileCache(t.TempDir())
	if _, ok := cache.Lookup("missing-session", "docs/foo.md"); ok {
		t.Fatal("unknown session must not return a cached decision")
	}
}

func TestFileCacheRoundTrip(t *testing.T) {
	cache := NewFileCache(t.TempDir())
	sessionID := "session-1"

	decision := Decision{
		Outcome: OutcomeApproved,
		Reason:  ReasonOperatorApprovedLive,
		Source:  SourceOperator,
		At:      time.Now().UTC().Truncate(time.Second),
		Path:    "docs/design-intent-loop-mutation-cycle.md",
	}

	if err := cache.Record(sessionID, decision); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, ok := cache.Lookup(sessionID, decision.Path)
	if !ok {
		t.Fatal("recorded decision must be retrievable")
	}
	if got.Outcome != decision.Outcome || got.Path != decision.Path {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, decision)
	}
}

func TestFileCachePersistsAcrossInstances(t *testing.T) {
	root := t.TempDir()
	sessionID := "session-2"

	cache1 := NewFileCache(root)
	decision := Decision{
		Outcome: OutcomeApproved,
		Reason:  ReasonOperatorApprovedLive,
		Source:  SourceOperator,
		At:      time.Now().UTC().Truncate(time.Second),
		Path:    "docs/persistent.md",
	}
	if err := cache1.Record(sessionID, decision); err != nil {
		t.Fatalf("Record: %v", err)
	}

	statPath := filepath.Join(root, "sessions", sessionID, "read-approvals.json")
	if _, err := os.Stat(statPath); err != nil {
		t.Fatalf("expected cache file at %s: %v", statPath, err)
	}

	cache2 := NewFileCache(root)
	got, ok := cache2.Lookup(sessionID, decision.Path)
	if !ok {
		t.Fatal("decision must survive cache restart")
	}
	if got.Outcome != decision.Outcome {
		t.Fatalf("persisted outcome mismatch: got %v, want %v", got.Outcome, decision.Outcome)
	}
	if got.Path != decision.Path {
		t.Fatalf("persisted path mismatch: got %q, want %q", got.Path, decision.Path)
	}
}

func TestFileCacheApprovedDeniedPathLists(t *testing.T) {
	cache := NewFileCache(t.TempDir())
	sessionID := "session-3"

	for _, path := range []string{"docs/c.md", "docs/a.md", "docs/b.md"} {
		if err := cache.Record(sessionID, Decision{
			Outcome: OutcomeApproved,
			Path:    path,
			Source:  SourceOperator,
		}); err != nil {
			t.Fatalf("Record %s: %v", path, err)
		}
	}
	if err := cache.Record(sessionID, Decision{
		Outcome: OutcomeDenied,
		Path:    "docs/x.md",
		Source:  SourceOperator,
	}); err != nil {
		t.Fatalf("Record denied: %v", err)
	}

	approved := cache.ApprovedPaths(sessionID)
	wantApproved := []string{"docs/a.md", "docs/b.md", "docs/c.md"}
	if len(approved) != len(wantApproved) {
		t.Fatalf("approved list = %v, want %v", approved, wantApproved)
	}
	for i, p := range approved {
		if p != wantApproved[i] {
			t.Errorf("approved[%d] = %q, want %q", i, p, wantApproved[i])
		}
	}

	denied := cache.DeniedPaths(sessionID)
	if len(denied) != 1 || denied[0] != "docs/x.md" {
		t.Fatalf("denied list = %v, want [docs/x.md]", denied)
	}
}

func TestFileCacheSessionsAreIndependent(t *testing.T) {
	cache := NewFileCache(t.TempDir())

	if err := cache.Record("session-a", Decision{
		Outcome: OutcomeApproved,
		Path:    "docs/shared.md",
	}); err != nil {
		t.Fatalf("Record session-a: %v", err)
	}
	if err := cache.Record("session-b", Decision{
		Outcome: OutcomeDenied,
		Path:    "docs/shared.md",
	}); err != nil {
		t.Fatalf("Record session-b: %v", err)
	}

	if got, ok := cache.Lookup("session-a", "docs/shared.md"); !ok || got.Outcome != OutcomeApproved {
		t.Fatalf("session-a expected approved, got %+v ok=%v", got, ok)
	}
	if got, ok := cache.Lookup("session-b", "docs/shared.md"); !ok || got.Outcome != OutcomeDenied {
		t.Fatalf("session-b expected denied, got %+v ok=%v", got, ok)
	}
}

func TestFileCacheRejectsEmptyInputs(t *testing.T) {
	cache := NewFileCache(t.TempDir())

	if err := cache.Record("", Decision{Outcome: OutcomeApproved, Path: "docs/x.md"}); err == nil {
		t.Fatal("expected error recording with empty session id")
	}
	if err := cache.Record("session-x", Decision{Outcome: OutcomeApproved, Path: ""}); err == nil {
		t.Fatal("expected error recording with empty path")
	}
}
