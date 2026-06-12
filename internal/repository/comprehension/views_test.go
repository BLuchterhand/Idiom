package comprehension

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestViewKeyIDDeterministic(t *testing.T) {
	k1 := ViewKey{
		IndexID: "idx-abc",
		Task:    "wire packet cache",
		Mode:    "plan",
		Query: ViewQueryInputs{
			Path:           "internal/repository/comprehension",
			Limit:          8,
			ForbiddenPaths: []string{"/tmp", ".idiom"},
			RequiredTests:  []string{"go test ./...", "go test ./internal/..."},
		},
	}
	k2 := ViewKey{
		IndexID: "idx-abc",
		Task:    "wire packet cache",
		Mode:    "plan",
		Query: ViewQueryInputs{
			Path:           "internal/repository/comprehension",
			Limit:          8,
			ForbiddenPaths: []string{".idiom", "/tmp"},                          // reordered
			RequiredTests:  []string{"go test ./internal/...", "go test ./..."}, // reordered
		},
	}

	if k1.ID() != k2.ID() {
		t.Fatalf("equivalent keys produced different IDs: %q vs %q", k1.ID(), k2.ID())
	}

	k3 := k1

	k3.Task = "different task"

	if k3.ID() == k1.ID() {
		t.Fatalf("changing task should change ID; both produced %q", k1.ID())
	}
}

func TestViewKeyIDIgnoresGenerationTimestamp(t *testing.T) {
	// Regression: pre-2026-05-10, contextPackID hashed generated_at into
	// the pack_id, so identical inputs always missed any cache. The new
	// content-addressed key must NOT carry generation time anywhere.
	k := ViewKey{IndexID: "idx", Task: "t", Mode: "plan"}
	first := k.ID()
	second := k.ID()

	if first != second {
		t.Fatalf("ID() should be pure; got %q then %q", first, second)
	}
}

func TestSaveAndLoadViewRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/viewroundtrip\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "a.go"), "package viewroundtrip\n\nfunc A() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "b.go"), "package viewroundtrip\n\nfunc B() string { return \"b\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	key := ViewKey{IndexID: build.IndexID, Task: "round trip", Mode: "plan"}
	original := PackResult{
		PackID:  key.ID(),
		IndexID: build.IndexID,
		Root:    root,
		Task:    "round trip",
		Mode:    "plan",
		CandidateFiles: []PackFile{
			{Path: "a.go", Role: "editable"},
			{Path: "b.go", Role: "read_only_evidence"},
		},
	}

	if err := SaveView(root, key, original); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	got, hit, err := LoadView(root, key)
	if err != nil {
		t.Fatalf("LoadView: %v", err)
	}

	if !hit {
		t.Fatalf("expected cache hit after SaveView")
	}

	if got.IndexID != original.IndexID || got.Task != original.Task {
		t.Fatalf("round-trip diverged: %+v", got)
	}

	if len(got.CandidateFiles) != 2 {
		t.Fatalf("CandidateFiles lost: %d", len(got.CandidateFiles))
	}

	// LoadViewByID must work the same way for the executor side.
	got2, hit2, err := LoadViewByID(root, key.ID())
	if err != nil {
		t.Fatalf("LoadViewByID: %v", err)
	}

	if !hit2 || got2.Task != original.Task {
		t.Fatalf("LoadViewByID expected hit with task %q, got hit=%v task=%q", original.Task, hit2, got2.Task)
	}
}

func TestSaveViewRecoversFreelistCorruptSQLiteStore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/viewrecover\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "a.go"), "package viewrecover\n\nfunc A() string { return \"a\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	key := ViewKey{IndexID: build.IndexID, Task: "recover view write", Mode: "plan"}
	result := PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "recover view write",
		Mode:           "plan",
		CandidateFiles: []PackFile{{Path: "a.go", Role: "editable"}},
	}
	corruptSQLiteFreelistCount(t, root)

	if err := SaveView(root, key, result); err != nil {
		t.Fatalf("SaveView should recover corrupt SQLite store and retry: %v", err)
	}

	got, hit, err := LoadView(root, key)
	if err != nil {
		t.Fatalf("LoadView after recovery: %v", err)
	}

	if !hit || got.Task != result.Task {
		_, status, statusErr := LoadViewByIDStatus(root, key.ID())
		t.Fatalf("expected recovered cache hit for task %q, got hit=%v task=%q status=%#v status_err=%v", result.Task, hit, got.Task, status, statusErr)
	}
}

func TestLoadViewMissOnEmpty(t *testing.T) {
	root := t.TempDir()
	key := ViewKey{IndexID: "absent", Task: "nothing", Mode: "plan"}

	_, hit, err := LoadView(root, key)
	if err != nil {
		t.Fatalf("LoadView on empty workspace should be miss-no-error, got %v", err)
	}

	if hit {
		t.Fatalf("expected miss on empty workspace")
	}

	_, hit2, err := LoadViewByID(root, "feedfacecafebeef")
	if err != nil || hit2 {
		t.Fatalf("LoadViewByID on empty workspace should miss, got hit=%v err=%v", hit2, err)
	}
}

func TestMarkViewsStaleByPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/staleviews\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "alpha.go"), "package staleviews\n\nfunc Alpha() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "beta.go"), "package staleviews\n\nfunc Beta() string { return \"b\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	keyA := ViewKey{IndexID: build.IndexID, Task: "task-a", Mode: "plan"}
	resA := PackResult{
		PackID:         keyA.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "alpha.go"}},
	}
	keyB := ViewKey{IndexID: build.IndexID, Task: "task-b", Mode: "plan"}
	resB := PackResult{
		PackID:         keyB.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "beta.go"}},
	}

	if err := SaveView(root, keyA, resA); err != nil {
		t.Fatalf("save A: %v", err)
	}

	if err := SaveView(root, keyB, resB); err != nil {
		t.Fatalf("save B: %v", err)
	}

	staled, err := MarkViewsStale(root, []string{"alpha.go"})
	if err != nil {
		t.Fatalf("MarkViewsStale: %v", err)
	}

	if staled != 1 {
		t.Fatalf("expected 1 stale row, got %d", staled)
	}

	// A should miss (stale); B should still hit.
	if _, hit, _ := LoadView(root, keyA); hit {
		t.Fatalf("alpha view should be stale and miss")
	}

	if _, hit, _ := LoadView(root, keyB); !hit {
		t.Fatalf("beta view should still hit")
	}
}

func TestMarkAllViewsStale(t *testing.T) {
	root := t.TempDir()

	for i, task := range []string{"one", "two", "three"} {
		key := ViewKey{IndexID: "i", Task: task, Mode: "plan"}
		res := PackResult{
			PackID:         key.ID(),
			IndexID:        "i",
			CandidateFiles: []PackFile{{Path: filepath.Join("d", task+".go")}},
		}

		if err := SaveView(root, key, res); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	staled, err := MarkAllViewsStale(root)
	if err != nil {
		t.Fatalf("MarkAllViewsStale: %v", err)
	}

	if staled != 3 {
		t.Fatalf("expected 3 stale rows, got %d", staled)
	}

	stats, err := ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}

	if stats.Rows != 3 || stats.Stale != 3 || stats.Fresh != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestReadViewStatsIncludesSessionCounters(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/viewstats\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "x.go"), "package viewstats\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	key := ViewKey{IndexID: build.IndexID, Task: "t", Mode: "plan"}
	if err := SaveView(root, key, PackResult{PackID: key.ID(), IndexID: build.IndexID, CandidateFiles: []PackFile{{Path: "x.go"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, hit, _ := LoadView(root, key); !hit {
		t.Fatalf("expected hit")
	}

	if _, hit, _ := LoadView(root, ViewKey{IndexID: "i", Task: "missing"}); hit {
		t.Fatalf("expected miss")
	}

	stats, err := ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}

	if stats.Hits != 1 || stats.Misses != 1 || stats.Writes != 1 {
		t.Fatalf("expected hits=1 misses=1 writes=1, got %+v", stats)
	}
}

func TestReadViewStatsReportsNonCurrentRows(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/viewstatscurrent\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "x.go"), "package viewstatscurrent\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	current := ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan"}
	if err := SaveView(root, current, PackResult{PackID: current.ID(), IndexID: build.IndexID, CandidateFiles: []PackFile{{Path: "x.go"}}}); err != nil {
		t.Fatalf("save current: %v", err)
	}

	old := ViewKey{IndexID: "old-index", Task: "old", Mode: "plan"}
	if err := SaveView(root, old, PackResult{PackID: old.ID(), IndexID: "old-index", CandidateFiles: []PackFile{{Path: "x.go"}}}); err != nil {
		t.Fatalf("save old: %v", err)
	}

	stats, err := ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}

	if stats.CurrentIndexID != build.IndexID || stats.Rows != 2 || stats.NonCurrent != 1 {
		t.Fatalf("expected one non-current row against current index %s, got %+v", build.IndexID, stats)
	}
}

func TestReadViewStatsSeparatesCurrentFreshRowsFromHistoricalStaleGaps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/viewstatsgaps\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "x.go"), "package viewstatsgaps\n\nfunc X() string { return \"x\" }\n")
	writeFile(t, filepath.Join(root, "old.go"), "package viewstatsgaps\n\nfunc Old() string { return \"old\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	current := ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan"}
	currentResult := PackResult{
		PackID:         current.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "x.go"}},
		UnresolvedGapDetails: []GraphUnresolvedGap{{
			Reason: "stale_runtime_trace",
			State:  "stale",
		}},
	}
	AttachPackResultContract(&currentResult)

	if err := SaveView(root, current, currentResult); err != nil {
		t.Fatalf("save current: %v", err)
	}

	old := ViewKey{IndexID: "old-index", Task: "old", Mode: "plan"}
	oldResult := PackResult{
		PackID:         old.ID(),
		IndexID:        "old-index",
		CandidateFiles: []PackFile{{Path: "old.go"}},
		UnresolvedGapDetails: []GraphUnresolvedGap{{
			Reason: "no_graph_matches",
			State:  "unavailable",
		}},
	}
	AttachPackResultContract(&oldResult)

	if err := SaveView(root, old, oldResult); err != nil {
		t.Fatalf("save old: %v", err)
	}

	if staled, err := MarkViewsStale(root, []string{"old.go"}); err != nil || staled != 1 {
		t.Fatalf("MarkViewsStale staled=%d err=%v", staled, err)
	}

	stats, err := ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}

	if stats.Rows != 2 || stats.Fresh != 1 || stats.Stale != 1 || stats.NonCurrent != 1 {
		t.Fatalf("expected one fresh current row and one stale historical row, got %+v", stats)
	}

	if stats.CurrentRows != 1 || stats.CurrentFresh != 1 || stats.CurrentStale != 0 || stats.HistoricalRows != 1 {
		t.Fatalf("current/historical counters did not isolate active rows: %+v", stats)
	}

	if stats.UnresolvedGapRows != 2 || stats.FreshUnresolvedGapRows != 1 || stats.StaleUnresolvedGapRows != 1 || stats.CurrentFreshUnresolvedGapRows != 1 {
		t.Fatalf("unresolved gap freshness counters missing: %+v", stats)
	}

	if stats.UnresolvedGapReasonCounts["stale_runtime_trace"] != 1 ||
		stats.UnresolvedGapReasonCounts["no_graph_matches"] != 1 ||
		stats.CurrentFreshUnresolvedGapReasonCounts["stale_runtime_trace"] != 1 ||
		stats.CurrentFreshUnresolvedGapReasonCounts["no_graph_matches"] != 0 {

		t.Fatalf("unresolved gap reason counts should distinguish current fresh rows: %+v", stats)
	}
}

func TestPackHitsCacheOnIdenticalSecondCall(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/cache\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	opts := PackOptions{
		Root:          root,
		Task:          "exercise the cache",
		Mode:          "plan",
		RequiredTests: []string{"go test ./..."},
	}

	first, err := Pack(opts)
	if err != nil {
		t.Fatalf("first Pack: %v", err)
	}

	second, err := Pack(opts)
	if err != nil {
		t.Fatalf("second Pack: %v", err)
	}

	if first.PackID != second.PackID {
		t.Fatalf("identical opts should produce identical pack_id: %q vs %q", first.PackID, second.PackID)
	}

	hits, _, writes := ViewCacheCounters()
	if hits == 0 {
		t.Fatalf("expected at least one cache hit on second Pack call, got hits=%d writes=%d", hits, writes)
	}

	if writes != 1 {
		t.Fatalf("expected exactly one cache write (first Pack), got %d", writes)
	}
}

// TestPackCacheHitRecordsRediscoveryAvoidance verifies that a
// graph_views cache hit increments estimatedRediscoveryTokensAvoided
// (Atom 6).
func TestPackCacheHitRecordsRediscoveryAvoidance(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/rediscovery\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "y.go"), "package pkg\n\nfunc Y() int { return 1 }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	opts := PackOptions{
		Root:          root,
		Task:          "exercise the rediscovery counter",
		Mode:          "plan",
		RequiredTests: []string{"go test ./..."},
	}
	// First Pack: cache miss → write.
	_, err := Pack(opts)
	if err != nil {
		t.Fatalf("first Pack: %v", err)
	}

	// Second Pack: cache hit.
	_, err = Pack(opts)
	if err != nil {
		t.Fatalf("second Pack: %v", err)
	}

	_, rediscoveryAvoided := TokenSavingsCounters()
	if rediscoveryAvoided <= 0 {
		t.Errorf("expected estimatedRediscoveryTokensAvoided > 0 after cache hit, got %d", rediscoveryAvoided)
	}

	t.Logf("estimatedRediscoveryTokensAvoided=%d from cache hit", rediscoveryAvoided)

	// Verify cache counters are consistent.
	hits, misses, _ := ViewCacheCounters()
	if hits == 0 {
		t.Error("expected at least one cache hit")
	}

	if misses == 0 {
		t.Log("note: no cache misses recorded (expected on fresh workspace)")
	}
}

func TestPackExposesGraphViewContractCacheAndRankerReasons(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/contract\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "alpha.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "beta.go"), "package pkg\n\nfunc Beta() string { return \"b\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change Alpha", Path: "pkg", Mode: "plan"}

	first, err := Pack(opts)
	if err != nil {
		t.Fatalf("first Pack: %v", err)
	}

	if first.Contract.SchemaVersion != GraphViewResultContractVersion || first.Contract.Cache.State != "miss_recompute" || first.Cache.State != "miss_recompute" {
		t.Fatalf("expected first packet contract/cache miss metadata, got contract=%#v cache=%#v", first.Contract, first.Cache)
	}

	if first.Contract.Provenance.Operation != "context_pack" || first.Contract.StaleEvidence.State != "fresh" {
		t.Fatalf("expected contract to mirror provenance/freshness, got %#v", first.Contract)
	}

	if !cacheInvalidationHasSource(first.Cache.Invalidation.Sources, "ranker_weights") || !cacheInvalidationHasSource(first.Cache.Invalidation.Sources, "runtime_traces") {
		t.Fatalf("expected ranker/runtime invalidation sources, got %#v", first.Cache.Invalidation.Sources)
	}

	alpha := packFileByPath(first.CandidateFiles, "pkg/alpha.go")
	if alpha == nil || !rankerReasonsContain(alpha.RankerReasons, "role_floor") || !rankerReasonsContain(alpha.RankerReasons, "direct_task_match") {
		t.Fatalf("expected ranker reasons for alpha candidate, got %#v", alpha)
	}

	second, err := Pack(opts)
	if err != nil {
		t.Fatalf("second Pack: %v", err)
	}

	if second.PackID != first.PackID || second.Cache.State != "hit" || second.Contract.Cache.State != "hit" {
		t.Fatalf("expected second packet cache hit for same pack_id, first=%q second=%q cache=%#v contract=%#v", first.PackID, second.PackID, second.Cache, second.Contract.Cache)
	}
}

func TestReadViewStatsTreatsMismatchedWorkspaceStateAsHistorical(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/workspacestate\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	current := ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan"}
	currentResult := PackResult{
		PackID:         current.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}
	AttachPackResultContract(&currentResult)

	if err := SaveView(root, current, currentResult); err != nil {
		t.Fatalf("save current: %v", err)
	}

	oldWorkspace := ViewKey{
		IndexID: build.IndexID,
		Task:    "old workspace state",
		Mode:    "plan",
		Query:   ViewQueryInputs{WorkspaceStateFingerprint: "old-workspace-state"},
	}
	oldResult := PackResult{
		PackID:         oldWorkspace.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
		UnresolvedGapDetails: []GraphUnresolvedGap{{
			Reason:  "stale_runtime_trace",
			State:   "stale",
			TraceID: "old-trace",
		}},
	}
	AttachPackResultContract(&oldResult)

	if err := SaveView(root, oldWorkspace, oldResult); err != nil {
		t.Fatalf("save old workspace state: %v", err)
	}

	stats, err := ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}

	if stats.Rows != 2 || stats.CurrentRows != 1 || stats.HistoricalRows != 1 || stats.NonCurrent != 1 {
		t.Fatalf("workspace-state mismatch should make row historical: %+v", stats)
	}

	if stats.CurrentFreshUnresolvedGapRows != 0 || stats.CurrentFreshUnresolvedGapReasonCounts["stale_runtime_trace"] != 0 {
		t.Fatalf("historical workspace-state gaps must not count as current fresh gaps: %+v", stats)
	}
}

func TestReadViewStatsTreatsLegacyEmptyWorkspaceStateAsHistoricalWhenCurrentStateIsKeyed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/legacyworkspacestate\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	legacy := ViewKey{IndexID: build.IndexID, Task: "legacy", Mode: "plan"}
	legacyResult := PackResult{
		PackID:         legacy.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
		UnresolvedGapDetails: []GraphUnresolvedGap{{
			Reason:  "stale_runtime_trace",
			State:   "stale",
			TraceID: "legacy-trace",
		}},
	}
	AttachPackResultContract(&legacyResult)

	if err := SaveView(root, legacy, legacyResult); err != nil {
		t.Fatalf("save legacy: %v", err)
	}

	if err := SaveLearnedWeights(root, map[string]float64{"pkg/x.go": 1.5}); err != nil {
		t.Fatalf("SaveLearnedWeights: %v", err)
	}

	currentFP := ComprehensionStateFingerprint(root)
	if currentFP == "" {
		t.Fatal("expected non-empty current workspace-state fingerprint")
	}

	current := ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: currentFP}}
	currentResult := PackResult{
		PackID:         current.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}
	AttachPackResultContract(&currentResult)

	if err := SaveView(root, current, currentResult); err != nil {
		t.Fatalf("save current: %v", err)
	}

	stats, err := ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}

	if stats.Rows != 2 || stats.CurrentRows != 1 || stats.HistoricalRows != 1 || stats.NonCurrent != 1 {
		t.Fatalf("legacy empty workspace fingerprint should be historical once current state is keyed: %+v", stats)
	}

	if stats.CurrentFreshUnresolvedGapRows != 0 || stats.CurrentFreshUnresolvedGapReasonCounts["stale_runtime_trace"] != 0 {
		t.Fatalf("legacy empty workspace-state gaps must not count as current fresh gaps: %+v", stats)
	}
}

func TestReadViewStatsCountsContractCoverageForCurrentRows(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/currentcontract\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	legacy := ViewKey{IndexID: build.IndexID, Task: "legacy no contract", Mode: "plan"}
	if err := SaveView(root, legacy, PackResult{
		PackID:         legacy.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}); err != nil {
		t.Fatalf("save legacy no-contract row: %v", err)
	}

	if err := SaveLearnedWeights(root, map[string]float64{"pkg/x.go": 1.5}); err != nil {
		t.Fatalf("SaveLearnedWeights: %v", err)
	}

	currentFP := ComprehensionStateFingerprint(root)
	current := ViewKey{IndexID: build.IndexID, Task: "current contract", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: currentFP}}
	currentResult := PackResult{
		PackID:         current.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}
	AttachPackResultContract(&currentResult)

	if err := SaveView(root, current, currentResult); err != nil {
		t.Fatalf("save current contract row: %v", err)
	}

	stats, err := ReadViewStats(root)
	if err != nil {
		t.Fatalf("ReadViewStats: %v", err)
	}

	if stats.Rows != 2 || stats.ContractRows != 1 || stats.CurrentRows != 1 || stats.CurrentContractRows != 1 {
		t.Fatalf("historical no-contract row should not reduce current contract coverage: %+v", stats)
	}
}

func TestPackCacheKeyChangesForPersistedWeightsAndRuntimeTraces(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/invalidation\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "alpha.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change Alpha", Path: "pkg", Mode: "plan"}

	base, err := Pack(opts)
	if err != nil {
		t.Fatalf("base Pack: %v", err)
	}

	if err := SaveLearnedWeights(root, map[string]float64{"pkg/alpha.go": 1.5}); err != nil {
		t.Fatalf("SaveLearnedWeights: %v", err)
	}

	weighted, err := Pack(opts)
	if err != nil {
		t.Fatalf("weighted Pack: %v", err)
	}

	if weighted.PackID == base.PackID {
		t.Fatalf("ranker weight changes must produce a new cache key; both were %q", base.PackID)
	}

	if weighted.Cache.WorkspaceStateFingerprint == "" || weighted.Cache.RankerWeightsFingerprint == "" || weighted.Cache.RuntimeTracesFingerprint != "" || weighted.Cache.State != "miss_recompute" {
		t.Fatalf("expected keyed workspace-state miss after weight change, got %#v", weighted.Cache)
	}

	weightedRanker := cacheInvalidationSourceByName(weighted.Cache.Invalidation.Sources, "ranker_weights")
	weightedRuntime := cacheInvalidationSourceByName(weighted.Cache.Invalidation.Sources, "runtime_traces")

	if weightedRanker == nil || weightedRanker.Fingerprint != weighted.Cache.RankerWeightsFingerprint || weightedRanker.Fingerprint == weighted.Cache.WorkspaceStateFingerprint {
		t.Fatalf("expected ranker source to carry its own component fingerprint, source=%#v cache=%#v", weightedRanker, weighted.Cache)
	}

	if weightedRuntime == nil || weightedRuntime.Fingerprint != "" || weightedRuntime.State != "baseline_keyed" {
		t.Fatalf("expected runtime source to remain baseline for ranker-only change, got %#v", weightedRuntime)
	}

	tracesDir := filepath.Join(root, ".idiom", "comprehension", "traces")
	if err := os.MkdirAll(tracesDir, 0o755); err != nil {
		t.Fatalf("mkdir traces: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tracesDir, "trace-1.json"), []byte(`{"trace_id":"trace-1","generated_at":"2026-05-13T00:00:00Z","command":"go test ./...","fresh":true}`), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}

	traced, err := Pack(opts)
	if err != nil {
		t.Fatalf("traced Pack: %v", err)
	}

	if traced.PackID == weighted.PackID {
		t.Fatalf("runtime trace changes must produce a new cache key; both were %q", traced.PackID)
	}

	if traced.Cache.WorkspaceStateFingerprint == "" || traced.Cache.RankerWeightsFingerprint == "" || traced.Cache.RuntimeTracesFingerprint == "" || traced.Cache.State != "miss_recompute" {
		t.Fatalf("expected keyed workspace-state miss after trace change, got %#v", traced.Cache)
	}

	tracedRuntime := cacheInvalidationSourceByName(traced.Cache.Invalidation.Sources, "runtime_traces")
	if tracedRuntime == nil || tracedRuntime.Fingerprint != traced.Cache.RuntimeTracesFingerprint || tracedRuntime.Fingerprint == traced.Cache.WorkspaceStateFingerprint {
		t.Fatalf("expected runtime source to carry its own component fingerprint, source=%#v cache=%#v", tracedRuntime, traced.Cache)
	}

	if traced.Cache.RankerWeightsFingerprint == traced.Cache.RuntimeTracesFingerprint {
		t.Fatalf("ranker and runtime component fingerprints should be independently attributed, got %#v", traced.Cache)
	}
}

func TestPackInvalidationAttributionIdentifiesRuntimeOnlyState(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/runtimeonly\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "alpha.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change Alpha", Path: "pkg", Mode: "plan"}

	base, err := Pack(opts)
	if err != nil {
		t.Fatalf("base Pack: %v", err)
	}

	writeRuntimeTraceForTest(t, root, "trace-runtime-only", time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC))

	traced, err := Pack(opts)
	if err != nil {
		t.Fatalf("traced Pack: %v", err)
	}

	if traced.PackID == base.PackID {
		t.Fatalf("runtime trace changes must produce a new cache key; both were %q", traced.PackID)
	}

	rankerSource := cacheInvalidationSourceByName(traced.Cache.Invalidation.Sources, "ranker_weights")
	runtimeSource := cacheInvalidationSourceByName(traced.Cache.Invalidation.Sources, "runtime_traces")

	if traced.Cache.RankerWeightsFingerprint != "" || rankerSource == nil || rankerSource.Fingerprint != "" || rankerSource.State != "baseline_keyed" {
		t.Fatalf("expected ranker source to remain baseline for runtime-only state, source=%#v cache=%#v", rankerSource, traced.Cache)
	}

	if traced.Cache.RuntimeTracesFingerprint == "" || runtimeSource == nil || runtimeSource.Fingerprint != traced.Cache.RuntimeTracesFingerprint || runtimeSource.State != "keyed" {
		t.Fatalf("expected runtime source to identify runtime-only state, source=%#v cache=%#v", runtimeSource, traced.Cache)
	}
}

func TestPackCacheHitSurvivesNoopFullRebuild(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/nooprebuild\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("initial Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"}

	first, err := Pack(opts)
	if err != nil {
		t.Fatalf("first Pack: %v", err)
	}

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("noop Build: %v", err)
	}

	second, err := Pack(opts)
	if err != nil {
		t.Fatalf("second Pack: %v", err)
	}

	if first.PackID != second.PackID {
		t.Fatalf("noop rebuild should preserve pack id, got %q then %q", first.PackID, second.PackID)
	}

	hits, _, writes := ViewCacheCounters()
	if hits == 0 || writes != 1 {
		t.Fatalf("expected cache hit after noop rebuild and no rewrite, hits=%d writes=%d", hits, writes)
	}
}

func TestPackCacheKeyIncludesLearnedWeightsOverride(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/overrideweights\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")
	writeFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc Beta() string { return \"b\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	base := PackOptions{Root: root, Task: "update package", Path: "pkg", Mode: "plan"}
	first := base

	first.LearnedWeights = map[string]float64{"pkg/a.go": 2}

	firstPack, err := Pack(first)
	if err != nil {
		t.Fatalf("first Pack: %v", err)
	}

	second := base

	second.LearnedWeights = map[string]float64{"pkg/b.go": 2}

	secondPack, err := Pack(second)
	if err != nil {
		t.Fatalf("second Pack: %v", err)
	}

	if firstPack.PackID == secondPack.PackID {
		t.Fatalf("learned weight overrides must produce distinct pack IDs; both were %q", firstPack.PackID)
	}
}

func TestLoadViewByIDRefusesStaleReferencedPath(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/stalepacket\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "x.go")
	writeFile(t, sourcePath, "package pkg\n\nfunc X() string { return \"x\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	pack, err := Pack(PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	writeFile(t, sourcePath, "package pkg\n\nfunc X() string { return \"changed\" }\n")

	if _, hit, err := LoadViewByID(root, pack.PackID); err != nil {
		t.Fatalf("LoadViewByID: %v", err)
	} else if hit {
		t.Fatalf("stale referenced path should miss instead of rehydrating cached packet")
	}
}

func TestLoadViewByIDStatusReportsExplicitStaleRows(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/stalerow\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	key := ViewKey{IndexID: build.IndexID, Task: "change X", Mode: "plan", Query: ViewQueryInputs{Path: "pkg/x.go"}}
	if err := SaveView(root, key, PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "change X",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []PackFile{{Path: "pkg/x.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	if n, err := MarkAllViewsStale(root); err != nil || n != 1 {
		t.Fatalf("MarkAllViewsStale n=%d err=%v", n, err)
	}

	got, status, err := LoadViewByIDStatus(root, key.ID())
	if err != nil {
		t.Fatalf("LoadViewByIDStatus: %v", err)
	}

	if status.State != ViewLoadStateStale || status.Reason != "stale_row" {
		t.Fatalf("expected explicit stale-row status, got status=%#v result=%#v", status, got)
	}

	if got.Fresh || got.PackID != key.ID() || len(got.CandidateFiles) != 1 {
		t.Fatalf("expected stale result with packet contents preserved, got %#v", got)
	}

	if _, hit, err := LoadViewByID(root, key.ID()); err != nil || hit {
		t.Fatalf("LoadViewByID should still expose stale row as a cache miss, hit=%v err=%v", hit, err)
	}
}

func TestLoadViewByIDStatusReportsNonCurrentRows(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/noncurrentrow\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	key := ViewKey{IndexID: "old-index", Task: "change X", Mode: "plan", Query: ViewQueryInputs{Path: "pkg/x.go"}}
	if err := SaveView(root, key, PackResult{
		PackID:         key.ID(),
		IndexID:        "old-index",
		Root:           root,
		Task:           "change X",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []PackFile{{Path: "pkg/x.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	got, status, err := LoadViewByIDStatus(root, key.ID())
	if err != nil {
		t.Fatalf("LoadViewByIDStatus: %v", err)
	}

	if status.State != ViewLoadStateStale || status.Reason != "non_current_index" || status.CurrentIndexID != build.IndexID {
		t.Fatalf("expected non-current stale status against %s, got status=%#v result=%#v", build.IndexID, status, got)
	}

	if got.Fresh || got.IndexID != "old-index" || len(got.CandidateFiles) != 1 {
		t.Fatalf("expected stale result with old packet contents preserved, got %#v", got)
	}
}

func TestLoadViewByIDAllowsUnrelatedStalePath(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/scopedpacket\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	otherPath := filepath.Join(root, "other", "other.go")
	writeFile(t, otherPath, "package other\n\nfunc Other() string { return \"old\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	pack, err := Pack(PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	writeFile(t, otherPath, "package other\n\nfunc Other() string { return \"new\" }\n")

	got, hit, err := LoadViewByID(root, pack.PackID)
	if err != nil {
		t.Fatalf("LoadViewByID: %v", err)
	}

	if !hit {
		t.Fatalf("unrelated stale path should not invalidate scoped cached packet")
	}

	if got.Freshness == nil || !got.Freshness.FreshForScope || got.Freshness.GlobalFresh {
		t.Fatalf("expected scoped freshness with globally stale repo, got %#v", got.Freshness)
	}
}

func TestPackUsesCachedViewWhenOnlyUnrelatedPathIsStale(t *testing.T) {
	ResetViewCacheCounters()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/scopedpack\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	otherPath := filepath.Join(root, "other", "other.go")
	writeFile(t, otherPath, "package other\n\nfunc Other() string { return \"old\" }\n")

	if _, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"}

	first, err := Pack(opts)
	if err != nil {
		t.Fatalf("first Pack: %v", err)
	}

	writeFile(t, otherPath, "package other\n\nfunc Other() string { return \"new\" }\n")

	second, err := Pack(opts)
	if err != nil {
		t.Fatalf("second Pack: %v", err)
	}

	if second.PackID != first.PackID {
		t.Fatalf("expected cached pack id %q, got %q", first.PackID, second.PackID)
	}

	hits, _, writes := ViewCacheCounters()
	if hits == 0 || writes != 1 {
		t.Fatalf("expected scoped cache hit without rewrite, hits=%d writes=%d", hits, writes)
	}

	if second.Freshness == nil || !second.Freshness.FreshForScope || second.Freshness.GlobalFresh {
		t.Fatalf("expected scoped freshness hit with globally stale repo, got %#v", second.Freshness)
	}
}

func TestSaveViewPersistsFreshnessMetadata(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/viewmeta\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(t.Context(), BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	key := ViewKey{IndexID: build.IndexID, Task: "meta", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp"}}
	if err := SaveView(root, key, PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var indexID, task, mode, workspaceFingerprint, refsJSON, fingerprintsJSON string

	if err := db.QueryRow(`SELECT index_id, task, mode, workspace_state_fingerprint, referenced_paths_json, source_fingerprints_json FROM graph_views WHERE id = ?`, key.ID()).Scan(&indexID, &task, &mode, &workspaceFingerprint, &refsJSON, &fingerprintsJSON); err != nil {
		t.Fatalf("read graph_views metadata: %v", err)
	}

	if indexID != build.IndexID || task != "meta" || mode != "plan" || workspaceFingerprint != "fp" {
		t.Fatalf("metadata mismatch: index=%q task=%q mode=%q workspace=%q", indexID, task, mode, workspaceFingerprint)
	}

	if !strings.Contains(refsJSON, "pkg/x.go") || !strings.Contains(fingerprintsJSON, "pkg/x.go") {
		t.Fatalf("expected referenced paths and source fingerprints in metadata, refs=%s fingerprints=%s", refsJSON, fingerprintsJSON)
	}
}

func TestChangedPathsBetween(t *testing.T) {
	prev := Graph{Nodes: []Node{
		{Kind: "file", Path: "a.go", Fingerprint: "fa1"},
		{Kind: "file", Path: "b.go", Fingerprint: "fb1"},
		{Kind: "file", Path: "removed.go", Fingerprint: "fr1"},
		{Kind: "symbol", Path: "a.go"}, // non-file kind ignored
	}}
	curr := Graph{Nodes: []Node{
		{Kind: "file", Path: "a.go", Fingerprint: "fa1"}, // unchanged
		{Kind: "file", Path: "b.go", Fingerprint: "fb2"}, // mutated
		{Kind: "file", Path: "added.go", Fingerprint: "fnew"},
	}}
	changed := ChangedPathsBetween(prev, curr)
	want := map[string]bool{"b.go": true, "removed.go": true, "added.go": true}

	if len(changed) != len(want) {
		t.Fatalf("unexpected change set: %v", changed)
	}

	for _, p := range changed {
		if !want[p] {
			t.Fatalf("unexpected path %q in change set", p)
		}
	}
}

func TestComprehensionStateFingerprintChangesWhenWeightsChange(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom", "ranker"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	weightsPath := filepath.Join(root, ".idiom", "ranker", "weights.json")
	if err := os.WriteFile(weightsPath, []byte(`{"a.go": 1.5}`), 0o644); err != nil {
		t.Fatalf("seed weights: %v", err)
	}

	first := ComprehensionStateFingerprint(root)
	if first == "" {
		t.Fatalf("expected non-empty fingerprint when weights file exists")
	}

	// Sleep so mtime advances even on coarse-grained filesystems.
	time.Sleep(20 * time.Millisecond)

	if err := os.WriteFile(weightsPath, []byte(`{"a.go": 2.0}`), 0o644); err != nil {
		t.Fatalf("rewrite weights: %v", err)
	}

	second := ComprehensionStateFingerprint(root)
	if second == first {
		t.Fatalf("fingerprint must change when weights.json mtime/size changes; both were %q", first)
	}
}

func TestComprehensionStateFingerprintEmptyWithoutWorkspaceState(t *testing.T) {
	root := t.TempDir()
	if got := ComprehensionStateFingerprint(root); got != "" {
		t.Fatalf("expected empty workspace state fingerprint without weights or traces, got %q", got)
	}
}

func TestComprehensionStateFingerprintChangesWhenTraceAdded(t *testing.T) {
	root := t.TempDir()
	tracesDir := filepath.Join(root, ".idiom", "comprehension", "traces")

	if err := os.MkdirAll(tracesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	first := ComprehensionStateFingerprint(root)

	time.Sleep(20 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(tracesDir, "abc123.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("seed trace: %v", err)
	}

	second := ComprehensionStateFingerprint(root)
	if second == first {
		t.Fatalf("fingerprint must change when a trace file is added; both were %q", first)
	}
}

func TestRuntimeTraceStateFingerprintUsesBoundedManifest(t *testing.T) {
	root := t.TempDir()
	tracesDir := filepath.Join(root, ".idiom", "comprehension", "traces")

	if err := os.MkdirAll(tracesDir, 0o755); err != nil {
		t.Fatalf("mkdir traces: %v", err)
	}

	baseTime := time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC)

	var manifest traceManifest

	manifest.SchemaVersion = runtimeTraceManifestSchema
	manifest.UpdatedAt = baseTime.Format(time.RFC3339)

	for i := 0; i < 100; i++ {
		traceID := fmt.Sprintf("trace-%03d", i)
		generatedAt := baseTime.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		writeFile(t, filepath.Join(tracesDir, traceID+".json"), fmt.Sprintf(`{"trace_id":%q,"artifact_path":%q,"generated_at":%q,"command":"go test ./...","fresh":true}`+"\n", traceID, filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json")), generatedAt))

		if i >= 100-runtimeTraceRecentLimit {
			manifest.Traces = append(manifest.Traces, traceManifestEntry{
				TraceID:      traceID,
				ArtifactPath: filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json")),
				GeneratedAt:  generatedAt,
				Fingerprint:  fmt.Sprintf("fp-%03d", i),
			})
		}
	}

	writeTraceManifestForTest(t, root, manifest)

	state := runtimeTraceStateFingerprint(root, runtimeTraceRecentLimit)
	if state.Fingerprint == "" || state.Source != "trace_manifest" || state.Considered != runtimeTraceRecentLimit || state.Total != runtimeTraceRecentLimit {
		t.Fatalf("expected bounded manifest fingerprint state, got %#v", state)
	}

	before := state.Fingerprint

	time.Sleep(20 * time.Millisecond)
	writeFile(t, filepath.Join(tracesDir, "trace-000.json"), `{"trace_id":"trace-000","generated_at":"2026-05-13T00:00:00Z","command":"changed old trace","fresh":true}`+"\n")

	afterOldTraceChange := runtimeTraceStateFingerprint(root, runtimeTraceRecentLimit)
	if afterOldTraceChange.Fingerprint != before || afterOldTraceChange.Considered != runtimeTraceRecentLimit {
		t.Fatalf("old retained traces outside the manifest horizon must not affect fingerprint work, before=%#v after=%#v", state, afterOldTraceChange)
	}

	manifest.Traces[0].Fingerprint = "changed-recent-fingerprint"
	writeTraceManifestForTest(t, root, manifest)

	afterRecentManifestChange := runtimeTraceStateFingerprint(root, runtimeTraceRecentLimit)
	if afterRecentManifestChange.Fingerprint == before {
		t.Fatalf("recent manifest entries inside the horizon must affect the fingerprint")
	}
}

func TestViewKeyIDIncorporatesWorkspaceStateFingerprint(t *testing.T) {
	base := ViewKey{IndexID: "i", Task: "t", Mode: "plan"}
	withFP := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp1"}}
	other := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp2"}}

	if base.ID() == withFP.ID() {
		t.Fatalf("adding workspace_state_fingerprint must change the ID")
	}

	if withFP.ID() == other.ID() {
		t.Fatalf("different workspace_state_fingerprint values must produce different IDs")
	}

	withRanker := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp", RankerWeightsFingerprint: "ranker"}}
	withRuntime := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp", RuntimeTracesFingerprint: "runtime"}}

	if withRanker.ID() == withRuntime.ID() {
		t.Fatalf("ranker/runtime component fingerprints must produce distinct IDs")
	}
}

func packFileByPath(files []PackFile, path string) *PackFile {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}

	return nil
}

func rankerReasonsContain(reasons []RankerReason, reason string) bool {
	for _, item := range reasons {
		if item.Reason == reason {
			return true
		}
	}

	return false
}

func cacheInvalidationHasSource(sources []GraphViewInvalidationSource, source string) bool {
	for _, item := range sources {
		if item.Source == source {
			return true
		}
	}

	return false
}

func cacheInvalidationSourceByName(sources []GraphViewInvalidationSource, source string) *GraphViewInvalidationSource {
	for i := range sources {
		if sources[i].Source == source {
			return &sources[i]
		}
	}

	return nil
}

func writeRuntimeTraceForTest(t *testing.T, root string, traceID string, generatedAt time.Time) {
	t.Helper()

	result := TraceResult{
		TraceID:      traceID,
		ArtifactPath: filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json")),
		Root:         root,
		Command:      "go test ./...",
		GeneratedAt:  generatedAt.UTC().Format(time.RFC3339),
		Success:      true,
		Fresh:        true,
	}
	if err := saveTrace(root, result); err != nil {
		t.Fatalf("save trace: %v", err)
	}
}

func writeTraceManifestForTest(t *testing.T, root string, manifest traceManifest) {
	t.Helper()

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal trace manifest: %v", err)
	}

	path := traceManifestPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir trace manifest dir: %v", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write trace manifest: %v", err)
	}
}
