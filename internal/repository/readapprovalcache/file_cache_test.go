package readapprovalcache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"idiom/internal/contracts/readapproval"
)

func TestFileCacheLookupMissesForUnknownSession(t *testing.T) {
	cache := NewFileCache(t.TempDir())
	if _, ok, err := cache.Lookup("missing-session", "docs/foo.md"); err != nil || ok {
		t.Fatal("unknown session must not return a cached decision")
	}
}

func TestFileCacheRoundTrip(t *testing.T) {
	cache := NewFileCache(t.TempDir())
	sessionID := "session-1"

	decision := readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
		At:      time.Now().UTC().Truncate(time.Second),
		Path:    "docs/design-intent-loop-mutation-cycle.md",
	}

	if err := cache.Record(sessionID, decision); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, ok, err := cache.Lookup(sessionID, decision.Path)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
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
	decision := readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
		At:      time.Now().UTC().Truncate(time.Second),
		Path:    "docs/persistent.md",
	}
	if err := cache1.Record(sessionID, decision); err != nil {
		t.Fatalf("Record: %v", err)
	}

	statPath := filepath.Join(root, ".idiom", "read-approvals", "sessions", sessionID, "read-approvals.json")
	if _, err := os.Stat(statPath); err != nil {
		t.Fatalf("expected cache file at %s: %v", statPath, err)
	}

	cache2 := NewFileCache(root)
	got, ok, err := cache2.Lookup(sessionID, decision.Path)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
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

func TestFileCacheRecordFailureDoesNotPublishMemoryDecision(t *testing.T) {
	root := t.TempDir()
	sessionID := "persist-failure-session"
	cache := NewFileCache(root)

	existing := readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
		Path:    "docs/existing.md",
	}
	if err := cache.Record(sessionID, existing); err != nil {
		t.Fatalf("Record existing: %v", err)
	}

	if err := os.Remove(cache.sessionFile(sessionID)); err != nil {
		t.Fatalf("remove cache file: %v", err)
	}
	if err := os.Mkdir(cache.sessionFile(sessionID), 0o700); err != nil {
		t.Fatalf("mkdir blocking target: %v", err)
	}

	unpersisted := readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
		Path:    "docs/unpersisted.md",
	}
	if err := cache.Record(sessionID, unpersisted); err == nil {
		t.Fatal("Record must fail when persistence target is a directory")
	}

	if got, ok, err := cache.Lookup(sessionID, unpersisted.Path); err != nil || ok {
		t.Fatalf("Lookup returned unpersisted decision: got=%+v ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := cache.Lookup(sessionID, existing.Path); err != nil || !ok || got.Outcome != existing.Outcome {
		t.Fatalf("existing decision should remain in memory: got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestFileCachePersistsStableFileDecisionShape(t *testing.T) {
	root := t.TempDir()
	sessionID := "shape-session"
	cache := NewFileCache(root)
	decision := readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
		At:      time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC),
		Path:    "docs/shape.md",
	}
	if err := cache.Record(sessionID, decision); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var raw map[string]any
	data, err := os.ReadFile(cache.sessionFile(sessionID))
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal cache file: %v", err)
	}
	approved, ok := raw["approved"].(map[string]any)
	if !ok {
		t.Fatalf("approved map missing from cache file: %#v", raw)
	}
	entry, ok := approved[decision.Path].(map[string]any)
	if !ok {
		t.Fatalf("approved entry missing from cache file: %#v", approved)
	}
	if entry["outcome"] != readapproval.OutcomeApproved.String() || entry["path"] != decision.Path {
		t.Fatalf("stable file decision fields missing: %#v", entry)
	}
	if _, ok := entry["Outcome"]; ok {
		t.Fatalf("cache leaked Go struct field Outcome: %#v", entry)
	}
}

func TestFileCacheApprovedDeniedPathLists(t *testing.T) {
	cache := NewFileCache(t.TempDir())
	sessionID := "session-3"

	for _, path := range []string{"docs/c.md", "docs/a.md", "docs/b.md"} {
		if err := cache.Record(sessionID, readapproval.Decision{
			Outcome: readapproval.OutcomeApproved,
			Path:    path,
			Source:  readapproval.SourceOperator,
		}); err != nil {
			t.Fatalf("Record %s: %v", path, err)
		}
	}
	if err := cache.Record(sessionID, readapproval.Decision{
		Outcome: readapproval.OutcomeDenied,
		Path:    "docs/x.md",
		Source:  readapproval.SourceOperator,
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

	if err := cache.Record("session-a", readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Path:    "docs/shared.md",
	}); err != nil {
		t.Fatalf("Record session-a: %v", err)
	}
	if err := cache.Record("session-b", readapproval.Decision{
		Outcome: readapproval.OutcomeDenied,
		Path:    "docs/shared.md",
	}); err != nil {
		t.Fatalf("Record session-b: %v", err)
	}

	if got, ok, err := cache.Lookup("session-a", "docs/shared.md"); err != nil || !ok || got.Outcome != readapproval.OutcomeApproved {
		t.Fatalf("session-a expected approved, got %+v ok=%v", got, ok)
	}
	if got, ok, err := cache.Lookup("session-b", "docs/shared.md"); err != nil || !ok || got.Outcome != readapproval.OutcomeDenied {
		t.Fatalf("session-b expected denied, got %+v ok=%v", got, ok)
	}
}

func TestFileCacheRejectsEmptyInputs(t *testing.T) {
	cache := NewFileCache(t.TempDir())

	if err := cache.Record("", readapproval.Decision{Outcome: readapproval.OutcomeApproved, Path: "docs/x.md"}); err == nil {
		t.Fatal("expected error recording with empty session id")
	}
	if err := cache.Record("session-x", readapproval.Decision{Outcome: readapproval.OutcomeApproved, Path: ""}); err == nil {
		t.Fatal("expected error recording with empty path")
	}
}

func TestFileCacheRejectsUnsafeSessionIDs(t *testing.T) {
	cache := NewFileCache(t.TempDir())

	for _, sessionID := range []string{"../escape", "nested/session", `nested\session`, "/absolute", "..", "."} {
		if err := cache.Record(sessionID, readapproval.Decision{Outcome: readapproval.OutcomeApproved, Path: "docs/x.md"}); err == nil {
			t.Fatalf("Record(%q) expected invalid session id error", sessionID)
		}
		if _, ok, err := cache.Lookup(sessionID, "docs/x.md"); err == nil || ok {
			t.Fatalf("Lookup(%q) must reject invalid session id, ok=%v err=%v", sessionID, ok, err)
		}
		if got := cache.ApprovedPaths(sessionID); len(got) != 0 {
			t.Fatalf("ApprovedPaths(%q) = %v, want empty", sessionID, got)
		}
	}
}

func TestFileCacheRejectsUnsupportedSchemaOnLoad(t *testing.T) {
	root := t.TempDir()
	sessionID := "schema-session"
	cache := NewFileCache(root)

	writeReadApprovalRecordForTest(t, cache, sessionID, fileRecord{
		Schema:  "future.schema.v2",
		Session: sessionID,
		Approved: map[string]fileDecision{
			"docs/x.md": {Outcome: readapproval.OutcomeApproved.String(), Path: "docs/x.md"},
		},
	})

	if _, ok, err := cache.Lookup(sessionID, "docs/x.md"); err == nil || ok {
		t.Fatalf("unsupported schema must return an error and no decision, ok=%v err=%v", ok, err)
	}
}

func TestFileCacheRejectsSessionMismatchOnLoad(t *testing.T) {
	root := t.TempDir()
	sessionID := "requested-session"
	cache := NewFileCache(root)

	writeReadApprovalRecordForTest(t, cache, sessionID, fileRecord{
		Schema:  fileRecordSchemaVersion,
		Session: "other-session",
		Approved: map[string]fileDecision{
			"docs/x.md": {Outcome: readapproval.OutcomeApproved.String(), Path: "docs/x.md"},
		},
	})

	if _, ok, err := cache.Lookup(sessionID, "docs/x.md"); err == nil || ok {
		t.Fatalf("session-mismatched cache must return an error and no decision, ok=%v err=%v", ok, err)
	}
}

func TestFileCacheRejectsInconsistentApprovedOutcomeOnLoad(t *testing.T) {
	root := t.TempDir()
	sessionID := "inconsistent-session"
	cache := NewFileCache(root)

	writeReadApprovalRecordForTest(t, cache, sessionID, fileRecord{
		Schema:  fileRecordSchemaVersion,
		Session: sessionID,
		Approved: map[string]fileDecision{
			"docs/x.md": {Outcome: readapproval.OutcomeDenied.String(), Path: "docs/x.md"},
		},
	})

	if _, ok, err := cache.Lookup(sessionID, "docs/x.md"); err == nil || ok {
		t.Fatalf("approved bucket with denied outcome must return an error and no decision, ok=%v err=%v", ok, err)
	}
}

func TestFileCacheRejectsPathMismatchOnLoad(t *testing.T) {
	root := t.TempDir()
	sessionID := "path-mismatch-session"
	cache := NewFileCache(root)

	writeReadApprovalRecordForTest(t, cache, sessionID, fileRecord{
		Schema:  fileRecordSchemaVersion,
		Session: sessionID,
		Approved: map[string]fileDecision{
			"docs/x.md": {Outcome: readapproval.OutcomeApproved.String(), Path: "docs/y.md"},
		},
	})

	if _, ok, err := cache.Lookup(sessionID, "docs/x.md"); err == nil || ok {
		t.Fatalf("path-mismatched cache entry must return an error and no decision, ok=%v err=%v", ok, err)
	}
}

func writeReadApprovalRecordForTest(t *testing.T, cache *FileCache, sessionID string, record fileRecord) {
	t.Helper()

	if err := os.MkdirAll(cache.sessionDir(sessionID), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(cache.sessionFile(sessionID), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestFileCacheConcurrentRecordLookupAndLists(t *testing.T) {
	cache := NewFileCache(t.TempDir())
	const sessionID = "concurrent-session"

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("file-%02d.go", i)
			if err := cache.Record(sessionID, readapproval.Decision{
				Outcome: readapproval.OutcomeApproved,
				Path:    path,
				Reason:  "test",
				At:      time.Now(),
			}); err != nil {
				t.Errorf("Record(%s): %v", path, err)
				return
			}
			if _, ok, err := cache.Lookup(sessionID, path); err != nil || !ok {
				t.Errorf("Lookup(%s) missed err=%v", path, err)
			}
			_ = cache.ApprovedPaths(sessionID)
			_ = cache.DeniedPaths(sessionID)
		}()
	}
	wg.Wait()

	if got := len(cache.ApprovedPaths(sessionID)); got != 32 {
		t.Fatalf("approved paths = %d, want 32", got)
	}
}
