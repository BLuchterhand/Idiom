package comprehension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("view key ID deterministic", func() {
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
	Expect(k1.ID()).To(BeEquivalentTo(k2.ID()), "equivalent keys produced different IDs: %q vs %q", k1.ID(), k2.ID())

	k3 := k1

	k3.Task = "different task"
	Expect(k3.ID()).NotTo(BeEquivalentTo(k1.ID()), "changing task should change ID; both produced %q", k1.ID())
})
var _ = It("view key ID ignores generation timestamp", func() {
	// Regression: pre-2026-05-10, contextPackID hashed generated_at into
	// the pack_id, so identical inputs always missed any cache. The new
	// content-addressed key must NOT carry generation time anywhere.
	k := ViewKey{IndexID: "idx", Task: "t", Mode: "plan"}
	first := k.ID()
	second := k.ID()
	Expect(first).To(BeEquivalentTo(second), "ID() should be pure; got %q then %q", first, second)
})
var _ = It("save and load view round trip", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/viewroundtrip\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "a.go"), "package viewroundtrip\n\nfunc A() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "b.go"), "package viewroundtrip\n\nfunc B() string { return \"b\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

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
	{
		err := SaveView(root, key, original)
		Expect(err).NotTo(HaveOccurred(), "SaveView: %v", err)
	}

	got, hit, err := LoadView(context.Background(), root, key)
	Expect(err).NotTo(HaveOccurred(), "LoadView: %v", err)
	Expect(hit).To(BeTrue(), "expected cache hit after SaveView")
	Expect(got.IndexID).To(BeEquivalentTo(original.IndexID), "round-trip diverged: %+v", got)
	Expect(got.Task).To(BeEquivalentTo(original.Task), "round-trip diverged: %+v", got)
	Expect(len(got.CandidateFiles)).To(BeEquivalentTo(2), "CandidateFiles lost: %d", len(got.CandidateFiles))

	// LoadViewByIDStatus is the executor rehydration contract.
	got2, status2, err := LoadViewByIDStatus(root, key.ID())
	Expect(err).NotTo(HaveOccurred(), "LoadViewByIDStatus: %v", err)
	Expect(status2.State).To(BeEquivalentTo(ViewLoadStateFresh), "LoadViewByIDStatus expected fresh hit with task %q, got status=%#v task=%q", original.Task, status2, got2.Task)
	Expect(got2.Task).To(BeEquivalentTo(original.Task), "LoadViewByIDStatus expected fresh hit with task %q, got status=%#v task=%q", original.Task, status2, got2.Task)
})
var _ = It("save view recovers freelist corrupt SQLite store", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/viewrecover\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "a.go"), "package viewrecover\n\nfunc A() string { return \"a\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	key := ViewKey{IndexID: build.IndexID, Task: "recover view write", Mode: "plan"}
	result := PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "recover view write",
		Mode:           "plan",
		CandidateFiles: []PackFile{{Path: "a.go", Role: "editable"}},
	}
	corruptSQLiteFreelistCount(root)
	{
		err := SaveView(root, key, result)
		Expect(err).NotTo(HaveOccurred(), "SaveView should recover corrupt SQLite store and retry: %v", err)
	}

	got, hit, err := LoadView(context.Background(), root, key)
	Expect(err).NotTo(HaveOccurred(), "LoadView after recovery: %v", err)
	Expect(hit).To(BeTrue(), "expected recovered cache hit for task %q", result.Task)
	Expect(got.Task).To(Equal(result.Task))
})
var _ = It("load view miss on empty", func() {
	root := GinkgoT().TempDir()
	key := ViewKey{IndexID: "absent", Task: "nothing", Mode: "plan"}

	_, hit, err := LoadView(context.Background(), root, key)
	Expect(err).NotTo(HaveOccurred(), "LoadView on empty workspace should be miss-no-error, got %v", err)
	Expect(hit).To(BeFalse(), "expected miss on empty workspace")

	_, status2, err := LoadViewByIDStatus(root, "feedfacecafebeef")
	Expect(err).To(BeNil(), "LoadViewByIDStatus on empty workspace should miss, got status=%#v err=%v", status2, err)
	Expect(status2.State).To(BeEquivalentTo(ViewLoadStateMissing), "LoadViewByIDStatus on empty workspace should miss, got status=%#v err=%v", status2, err)
})
var _ = It("mark views stale by path", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/staleviews\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "alpha.go"), "package staleviews\n\nfunc Alpha() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "beta.go"), "package staleviews\n\nfunc Beta() string { return \"b\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

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
	{
		err := SaveView(root, keyA, resA)
		Expect(err).NotTo(HaveOccurred(), "save A: %v", err)
	}
	{
		err := SaveView(root, keyB, resB)
		Expect(err).NotTo(HaveOccurred(), "save B: %v", err)
	}

	staled, err := MarkViewsStale(root, []string{"alpha.go"})
	Expect(err).NotTo(HaveOccurred(), "MarkViewsStale: %v", err)
	Expect(staled).To(BeEquivalentTo(1), "expected 1 stale row, got %d", staled)
	{
		// A should miss (stale); B should still hit.
		_, hit, _ := LoadView(context.Background(), root, keyA)
		Expect(hit).To(BeFalse(), "alpha view should be stale and miss")
	}
	{
		_, hit, _ := LoadView(context.Background(), root, keyB)
		Expect(hit).To(BeTrue(), "beta view should still hit")
	}
})
var _ = It("mark all views stale", func() {
	root := GinkgoT().TempDir()

	for i, task := range []string{"one", "two", "three"} {
		key := ViewKey{IndexID: "i", Task: task, Mode: "plan"}
		res := PackResult{
			PackID:         key.ID(),
			IndexID:        "i",
			CandidateFiles: []PackFile{{Path: filepath.Join("d", task+".go")}},
		}
		{
			err := SaveView(root, key, res)
			Expect(err).NotTo(HaveOccurred(), "save %d: %v", i, err)
		}
	}

	staled, err := MarkAllViewsStale(root)
	Expect(err).NotTo(HaveOccurred(), "MarkAllViewsStale: %v", err)
	Expect(staled).To(BeEquivalentTo(3), "expected 3 stale rows, got %d", staled)

	stats, err := ReadViewStats(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadViewStats: %v", err)
	Expect(stats.Rows).To(BeEquivalentTo(3), "unexpected stats: %+v", stats)
	Expect(stats.Stale).To(BeEquivalentTo(3), "unexpected stats: %+v", stats)
	Expect(stats.Fresh).To(BeEquivalentTo(0), "unexpected stats: %+v", stats)
})
var _ = It("read view stats includes session counters", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/viewstats\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "x.go"), "package viewstats\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	key := ViewKey{IndexID: build.IndexID, Task: "t", Mode: "plan"}
	{
		err := SaveView(root, key, PackResult{PackID: key.ID(), IndexID: build.IndexID, CandidateFiles: []PackFile{{Path: "x.go"}}})
		Expect(err).NotTo(HaveOccurred(), "save: %v", err)
	}
	{
		_, hit, _ := LoadView(context.Background(), root, key)
		Expect(hit).To(BeTrue(), "expected hit")
	}
	{
		_, hit, _ := LoadView(context.Background(), root, ViewKey{IndexID: "i", Task: "missing"})
		Expect(hit).To(BeFalse(), "expected miss")
	}

	stats, err := ReadViewStats(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadViewStats: %v", err)
	Expect(stats.Hits).To(BeEquivalentTo(1), "expected hits=1 misses=1 writes=1, got %+v", stats)
	Expect(stats.Misses).To(BeEquivalentTo(1), "expected hits=1 misses=1 writes=1, got %+v", stats)
	Expect(stats.Writes).To(BeEquivalentTo(1), "expected hits=1 misses=1 writes=1, got %+v", stats)
})
var _ = It("read view stats reports non current rows", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/viewstatscurrent\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "x.go"), "package viewstatscurrent\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	current := ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan"}
	{
		err := SaveView(root, current, PackResult{PackID: current.ID(), IndexID: build.IndexID, CandidateFiles: []PackFile{{Path: "x.go"}}})
		Expect(err).NotTo(HaveOccurred(), "save current: %v", err)
	}

	old := ViewKey{IndexID: "old-index", Task: "old", Mode: "plan"}
	{
		err := SaveView(root, old, PackResult{PackID: old.ID(), IndexID: "old-index", CandidateFiles: []PackFile{{Path: "x.go"}}})
		Expect(err).NotTo(HaveOccurred(), "save old: %v", err)
	}

	stats, err := ReadViewStats(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadViewStats: %v", err)
	Expect(stats.CurrentIndexID).To(BeEquivalentTo(build.IndexID), "expected one non-current row against current index %s, got %+v", build.IndexID, stats)
	Expect(stats.Rows).To(BeEquivalentTo(2), "expected one non-current row against current index %s, got %+v", build.IndexID, stats)
	Expect(stats.NonCurrent).To(BeEquivalentTo(1), "expected one non-current row against current index %s, got %+v", build.IndexID, stats)
})
var _ = It("read view stats separates current fresh rows from historical stale gaps", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/viewstatsgaps\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "x.go"), "package viewstatsgaps\n\nfunc X() string { return \"x\" }\n")
	writeFile(filepath.Join(root, "old.go"), "package viewstatsgaps\n\nfunc Old() string { return \"old\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

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
	{
		err := SaveView(root, current, currentResult)
		Expect(err).NotTo(HaveOccurred(), "save current: %v", err)
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
	{
		err := SaveView(root, old, oldResult)
		Expect(err).NotTo(HaveOccurred(), "save old: %v", err)
	}
	{
		staled, err := MarkViewsStale(root, []string{"old.go"})
		Expect(err).To(BeNil(), "MarkViewsStale staled=%d err=%v", staled, err)
		Expect(staled).To(BeEquivalentTo(1), "MarkViewsStale staled=%d err=%v", staled, err)
	}

	stats, err := ReadViewStats(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadViewStats: %v", err)
	Expect(stats.Rows).To(BeEquivalentTo(2), "expected one fresh current row and one stale historical row, got %+v", stats)
	Expect(stats.Fresh).To(BeEquivalentTo(1), "expected one fresh current row and one stale historical row, got %+v", stats)
	Expect(stats.Stale).To(BeEquivalentTo(1), "expected one fresh current row and one stale historical row, got %+v", stats)
	Expect(stats.NonCurrent).To(BeEquivalentTo(1), "expected one fresh current row and one stale historical row, got %+v", stats)
	Expect(stats.CurrentRows).To(BeEquivalentTo(1), "current/historical counters did not isolate active rows: %+v", stats)
	Expect(stats.CurrentFresh).To(BeEquivalentTo(1), "current/historical counters did not isolate active rows: %+v", stats)
	Expect(stats.CurrentStale).To(BeEquivalentTo(0), "current/historical counters did not isolate active rows: %+v", stats)
	Expect(stats.HistoricalRows).To(BeEquivalentTo(1), "current/historical counters did not isolate active rows: %+v", stats)
	Expect(stats.UnresolvedGapRows).To(BeEquivalentTo(2), "unresolved gap freshness counters missing: %+v", stats)
	Expect(stats.FreshUnresolvedGapRows).To(BeEquivalentTo(1), "unresolved gap freshness counters missing: %+v", stats)
	Expect(stats.StaleUnresolvedGapRows).To(BeEquivalentTo(1), "unresolved gap freshness counters missing: %+v", stats)
	Expect(stats.CurrentFreshUnresolvedGapRows).To(BeEquivalentTo(1), "unresolved gap freshness counters missing: %+v", stats)
	Expect(stats.UnresolvedGapReasonCounts["stale_runtime_trace"]).To(BeEquivalentTo(1), "unresolved gap reason counts should distinguish current fresh rows: %+v", stats)
	Expect(stats.UnresolvedGapReasonCounts["no_graph_matches"]).To(BeEquivalentTo(1), "unresolved gap reason counts should distinguish current fresh rows: %+v", stats)
	Expect(stats.CurrentFreshUnresolvedGapReasonCounts["stale_runtime_trace"]).To(BeEquivalentTo(1), "unresolved gap reason counts should distinguish current fresh rows: %+v", stats)
	Expect(stats.CurrentFreshUnresolvedGapReasonCounts["no_graph_matches"]).To(BeEquivalentTo(0), "unresolved gap reason counts should distinguish current fresh rows: %+v", stats)
})
var _ = It("pack hits cache on identical second call", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/cache\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	opts := PackOptions{
		Root:          root,
		Task:          "exercise the cache",
		Mode:          "plan",
		RequiredTests: []string{"go test ./..."},
	}

	first, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "first Pack: %v", err)

	second, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "second Pack: %v", err)
	Expect(first.PackID).To(BeEquivalentTo(second.PackID), "identical opts should produce identical pack_id: %q vs %q", first.PackID, second.PackID)

	hits, _, writes := ViewCacheCounters()
	Expect(hits).NotTo(BeEquivalentTo(0), "expected at least one cache hit on second Pack call, got hits=%d writes=%d", hits, writes)
	Expect(writes).To(BeEquivalentTo(1), "expected exactly one cache write (first Pack), got %d", writes)
})

var _ = It("pack fails closed on malformed persisted ranker weights before cache reuse", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/strict-ranker-pack\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")
	_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred())

	opts := PackOptions{Root: root, Task: "rank x.go without hiding corrupted learned state", Mode: "plan"}
	_, err = Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred())

	weightsPath := filepath.Join(root, rankerStateWeightsPath)
	corrupt := []byte("not-json\n")
	Expect(os.WriteFile(weightsPath, corrupt, 0o644)).To(Succeed())

	_, err = Pack(context.Background(), opts)
	Expect(err).To(MatchError(ContainSubstring("load ranker weights for context pack")))
	persisted, readErr := os.ReadFile(weightsPath)
	Expect(readErr).NotTo(HaveOccurred())
	Expect(persisted).To(Equal(corrupt))
})

// TestPackCacheHitRecordsRediscoveryAvoidance verifies that a
// graph_views cache hit increments estimatedRediscoveryTokensAvoided
// (Atom 6).
var _ = It("pack cache hit records rediscovery avoidance", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/rediscovery\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")
	writeFile(filepath.Join(root, "pkg", "y.go"), "package pkg\n\nfunc Y() int { return 1 }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	opts := PackOptions{
		Root:          root,
		Task:          "exercise the rediscovery counter",
		Mode:          "plan",
		RequiredTests: []string{"go test ./..."},
	}
	// First Pack: cache miss → write.
	_, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "first Pack: %v", err)

	// Second Pack: cache hit.
	_, err = Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "second Pack: %v", err)

	_, rediscoveryAvoided := TokenSavingsCounters()
	Expect(rediscoveryAvoided <= 0).To(BeFalse(), "expected estimatedRediscoveryTokensAvoided > 0 after cache hit, got %d", rediscoveryAvoided)

	GinkgoWriter.Printf("estimatedRediscoveryTokensAvoided=%d from cache hit", rediscoveryAvoided)

	// Verify cache counters are consistent.
	hits, misses, _ := ViewCacheCounters()
	Expect(hits).NotTo(BeEquivalentTo(0), "expected at least one cache hit")

	if misses == 0 {
		GinkgoWriter.Println("note: no cache misses recorded (expected on fresh workspace)")
	}
})
var _ = It("pack exposes graph view contract cache and ranker reasons", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/contract\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "alpha.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "pkg", "beta.go"), "package pkg\n\nfunc Beta() string { return \"b\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change Alpha", Path: "pkg", Mode: "plan"}

	first, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "first Pack: %v", err)
	Expect(first.Contract.SchemaVersion).To(BeEquivalentTo(GraphViewResultContractVersion), "expected first packet contract/cache miss metadata, got contract=%#v cache=%#v", first.Contract, first.Cache)
	Expect(first.Contract.Cache.State).To(BeEquivalentTo("miss_recompute"), "expected first packet contract/cache miss metadata, got contract=%#v cache=%#v", first.Contract, first.Cache)
	Expect(first.Cache.State).To(BeEquivalentTo("miss_recompute"), "expected first packet contract/cache miss metadata, got contract=%#v cache=%#v", first.Contract, first.Cache)
	Expect(first.Contract.Provenance.Operation).To(BeEquivalentTo("context_pack"), "expected contract to mirror provenance/freshness, got %#v", first.Contract)
	Expect(first.Contract.StaleEvidence.State).To(BeEquivalentTo("fresh"), "expected contract to mirror provenance/freshness, got %#v", first.Contract)
	Expect(cacheInvalidationHasSource(first.Cache.Invalidation.Sources, "ranker_weights")).To(BeTrue(), "expected ranker/runtime invalidation sources, got %#v", first.Cache.Invalidation.Sources)
	Expect(cacheInvalidationHasSource(first.Cache.Invalidation.Sources, "runtime_traces")).To(BeTrue(), "expected ranker/runtime invalidation sources, got %#v", first.Cache.Invalidation.Sources)

	alpha := packFileByPath(first.CandidateFiles, "pkg/alpha.go")
	Expect(alpha).NotTo(BeNil(), "expected ranker reasons for alpha candidate, got %#v", alpha)
	Expect(rankerReasonsContain(alpha.RankerReasons, "role_floor")).To(BeTrue(), "expected ranker reasons for alpha candidate, got %#v", alpha)
	Expect(rankerReasonsContain(alpha.RankerReasons, "direct_task_match")).To(BeTrue(), "expected ranker reasons for alpha candidate, got %#v", alpha)

	second, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "second Pack: %v", err)
	Expect(second.PackID).To(BeEquivalentTo(first.PackID), "expected second packet cache hit for same pack_id, first=%q second=%q cache=%#v contract=%#v", first.PackID, second.PackID, second.Cache, second.Contract.Cache)
	Expect(second.Cache.State).To(BeEquivalentTo("hit"), "expected second packet cache hit for same pack_id, first=%q second=%q cache=%#v contract=%#v", first.PackID, second.PackID, second.Cache, second.Contract.Cache)
	Expect(second.Contract.Cache.State).To(BeEquivalentTo("hit"), "expected second packet cache hit for same pack_id, first=%q second=%q cache=%#v contract=%#v", first.PackID, second.PackID, second.Cache, second.Contract.Cache)
})
var _ = It("read view stats treats mismatched workspace state as historical", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/workspacestate\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	current := ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan"}
	currentResult := PackResult{
		PackID:         current.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}
	AttachPackResultContract(&currentResult)
	{
		err := SaveView(root, current, currentResult)
		Expect(err).NotTo(HaveOccurred(), "save current: %v", err)
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
	{
		err := SaveView(root, oldWorkspace, oldResult)
		Expect(err).NotTo(HaveOccurred(), "save old workspace state: %v", err)
	}

	stats, err := ReadViewStats(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadViewStats: %v", err)
	Expect(stats.Rows).To(BeEquivalentTo(2), "workspace-state mismatch should make row historical: %+v", stats)
	Expect(stats.CurrentRows).To(BeEquivalentTo(1), "workspace-state mismatch should make row historical: %+v", stats)
	Expect(stats.HistoricalRows).To(BeEquivalentTo(1), "workspace-state mismatch should make row historical: %+v", stats)
	Expect(stats.NonCurrent).To(BeEquivalentTo(1), "workspace-state mismatch should make row historical: %+v", stats)
	Expect(stats.CurrentFreshUnresolvedGapRows).To(BeEquivalentTo(0), "historical workspace-state gaps must not count as current fresh gaps: %+v", stats)
	Expect(stats.CurrentFreshUnresolvedGapReasonCounts["stale_runtime_trace"]).To(BeEquivalentTo(0), "historical workspace-state gaps must not count as current fresh gaps: %+v", stats)
})
var _ = It("read view stats treats legacy empty workspace state as historical when current state is keyed", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/legacyworkspacestate\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

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
	{
		err := SaveView(root, legacy, legacyResult)
		Expect(err).NotTo(HaveOccurred(), "save legacy: %v", err)
	}
	_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"pkg/x.go"}, "", "legacy-workspace-state")
	Expect(err).NotTo(HaveOccurred(), "apply learned-weights fixture: %v", err)

	currentState, err := ComprehensionStateFingerprints(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	currentFP := currentState.WorkspaceStateFingerprint
	Expect(currentFP).NotTo(BeEquivalentTo(""), "expected non-empty current workspace-state fingerprint")

	current := ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: currentFP}}
	currentResult := PackResult{
		PackID:         current.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}
	AttachPackResultContract(&currentResult)
	{
		err := SaveView(root, current, currentResult)
		Expect(err).NotTo(HaveOccurred(), "save current: %v", err)
	}

	stats, err := ReadViewStats(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadViewStats: %v", err)
	Expect(stats.Rows).To(BeEquivalentTo(2), "legacy empty workspace fingerprint should be historical once current state is keyed: %+v", stats)
	Expect(stats.CurrentRows).To(BeEquivalentTo(1), "legacy empty workspace fingerprint should be historical once current state is keyed: %+v", stats)
	Expect(stats.HistoricalRows).To(BeEquivalentTo(1), "legacy empty workspace fingerprint should be historical once current state is keyed: %+v", stats)
	Expect(stats.NonCurrent).To(BeEquivalentTo(1), "legacy empty workspace fingerprint should be historical once current state is keyed: %+v", stats)
	Expect(stats.CurrentFreshUnresolvedGapRows).To(BeEquivalentTo(0), "legacy empty workspace-state gaps must not count as current fresh gaps: %+v", stats)
	Expect(stats.CurrentFreshUnresolvedGapReasonCounts["stale_runtime_trace"]).To(BeEquivalentTo(0), "legacy empty workspace-state gaps must not count as current fresh gaps: %+v", stats)
})
var _ = It("read view stats counts contract coverage for current rows", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/currentcontract\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	legacy := ViewKey{IndexID: build.IndexID, Task: "legacy no contract", Mode: "plan"}
	{
		err := SaveView(root, legacy, PackResult{
			PackID:         legacy.ID(),
			IndexID:        build.IndexID,
			CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
		})
		Expect(err).NotTo(HaveOccurred(), "save legacy no-contract row: %v", err)
	}
	_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"pkg/x.go"}, "", "current-contract-state")
	Expect(err).NotTo(HaveOccurred(), "apply learned-weights fixture: %v", err)

	currentState, err := ComprehensionStateFingerprints(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	currentFP := currentState.WorkspaceStateFingerprint
	current := ViewKey{IndexID: build.IndexID, Task: "current contract", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: currentFP}}
	currentResult := PackResult{
		PackID:         current.ID(),
		IndexID:        build.IndexID,
		CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
	}
	AttachPackResultContract(&currentResult)
	{
		err := SaveView(root, current, currentResult)
		Expect(err).NotTo(HaveOccurred(), "save current contract row: %v", err)
	}

	stats, err := ReadViewStats(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "ReadViewStats: %v", err)
	Expect(stats.Rows).To(BeEquivalentTo(2), "historical no-contract row should not reduce current contract coverage: %+v", stats)
	Expect(stats.ContractRows).To(BeEquivalentTo(1), "historical no-contract row should not reduce current contract coverage: %+v", stats)
	Expect(stats.CurrentRows).To(BeEquivalentTo(1), "historical no-contract row should not reduce current contract coverage: %+v", stats)
	Expect(stats.CurrentContractRows).To(BeEquivalentTo(1), "historical no-contract row should not reduce current contract coverage: %+v", stats)
})
var _ = It("pack cache key changes for persisted weights and runtime traces", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/invalidation\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "alpha.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change Alpha", Path: "pkg", Mode: "plan"}

	base, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "base Pack: %v", err)
	_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"pkg/alpha.go"}, "", "cache-key-ranker-change")
	Expect(err).NotTo(HaveOccurred(), "apply learned-weights fixture: %v", err)

	weighted, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "weighted Pack: %v", err)
	rankerSnapshot, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	Expect(weighted.PackID).NotTo(BeEquivalentTo(base.PackID), "ranker weight changes must produce a new cache key; both were %q", base.PackID)
	Expect(weighted.Cache.WorkspaceStateFingerprint).NotTo(BeEquivalentTo(""), "expected keyed workspace-state miss after weight change, got %#v", weighted.Cache)
	Expect(weighted.Cache.RankerWeightsFingerprint).NotTo(BeEquivalentTo(""), "expected keyed workspace-state miss after weight change, got %#v", weighted.Cache)
	Expect(weighted.Cache.RankerWeightsFingerprint).To(Equal(rankerSnapshot.Generation), "Pack currentness must use the ranker owner's lock-consistent generation")
	Expect(weighted.Cache.RuntimeTracesFingerprint).To(BeEquivalentTo(""), "expected keyed workspace-state miss after weight change, got %#v", weighted.Cache)
	Expect(weighted.Cache.State).To(BeEquivalentTo("miss_recompute"), "expected keyed workspace-state miss after weight change, got %#v", weighted.Cache)

	weightedRanker := cacheInvalidationSourceByName(weighted.Cache.Invalidation.Sources, "ranker_weights")
	weightedRuntime := cacheInvalidationSourceByName(weighted.Cache.Invalidation.Sources, "runtime_traces")
	Expect(weightedRanker).NotTo(BeNil(), "expected ranker source to carry its own component fingerprint, source=%#v cache=%#v", weightedRanker, weighted.Cache)
	Expect(weightedRanker.Fingerprint).To(BeEquivalentTo(weighted.Cache.RankerWeightsFingerprint), "expected ranker source to carry its own component fingerprint, source=%#v cache=%#v", weightedRanker, weighted.Cache)
	Expect(weightedRanker.Fingerprint).NotTo(BeEquivalentTo(weighted.Cache.WorkspaceStateFingerprint), "expected ranker source to carry its own component fingerprint, source=%#v cache=%#v", weightedRanker, weighted.Cache)
	Expect(weightedRuntime).NotTo(BeNil(), "expected runtime source to remain baseline for ranker-only change, got %#v", weightedRuntime)
	Expect(weightedRuntime.Fingerprint).To(BeEquivalentTo(""), "expected runtime source to remain baseline for ranker-only change, got %#v", weightedRuntime)
	Expect(weightedRuntime.State).To(BeEquivalentTo("baseline_keyed"), "expected runtime source to remain baseline for ranker-only change, got %#v", weightedRuntime)

	tracesDir := filepath.Join(root, ".idiom", "comprehension", "traces")
	{
		err := os.MkdirAll(tracesDir, 0o755)
		Expect(err).NotTo(HaveOccurred(), "mkdir traces: %v", err)
	}
	{
		err := os.WriteFile(filepath.Join(tracesDir, "trace-1.json"), []byte(`{"trace_id":"trace-1","generated_at":"2026-05-13T00:00:00Z","command":"go test ./...","fresh":true}`), 0o644)
		Expect(err).NotTo(HaveOccurred(), "write trace: %v", err)
	}

	traced, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "traced Pack: %v", err)
	Expect(traced.PackID).NotTo(BeEquivalentTo(weighted.PackID), "runtime trace changes must produce a new cache key; both were %q", traced.PackID)
	Expect(traced.Cache.WorkspaceStateFingerprint).NotTo(BeEquivalentTo(""), "expected keyed workspace-state miss after trace change, got %#v", traced.Cache)
	Expect(traced.Cache.RankerWeightsFingerprint).NotTo(BeEquivalentTo(""), "expected keyed workspace-state miss after trace change, got %#v", traced.Cache)
	Expect(traced.Cache.RuntimeTracesFingerprint).NotTo(BeEquivalentTo(""), "expected keyed workspace-state miss after trace change, got %#v", traced.Cache)
	Expect(traced.Cache.State).To(BeEquivalentTo("miss_recompute"), "expected keyed workspace-state miss after trace change, got %#v", traced.Cache)

	tracedRuntime := cacheInvalidationSourceByName(traced.Cache.Invalidation.Sources, "runtime_traces")
	Expect(tracedRuntime).NotTo(BeNil(), "expected runtime source to carry its own component fingerprint, source=%#v cache=%#v", tracedRuntime, traced.Cache)
	Expect(tracedRuntime.Fingerprint).To(BeEquivalentTo(traced.Cache.RuntimeTracesFingerprint), "expected runtime source to carry its own component fingerprint, source=%#v cache=%#v", tracedRuntime, traced.Cache)
	Expect(tracedRuntime.Fingerprint).NotTo(BeEquivalentTo(traced.Cache.WorkspaceStateFingerprint), "expected runtime source to carry its own component fingerprint, source=%#v cache=%#v", tracedRuntime, traced.Cache)
	Expect(traced.Cache.RankerWeightsFingerprint).NotTo(BeEquivalentTo(traced.Cache.RuntimeTracesFingerprint), "ranker and runtime component fingerprints should be independently attributed, got %#v", traced.Cache)
})

var _ = It("pack propagates request cancellation while waiting for ranker state authority", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/packcancel\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "owner.go"), "package pkg\n\nfunc Owner() string { return \"owner\" }\n")
	_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred())

	store, err := openRankerStateStore(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(store.Close)

	ctx, cancel := context.WithCancel(GinkgoT().Context())
	DeferCleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, packErr := Pack(ctx, PackOptions{Root: root, Task: "change Owner", Path: "pkg"})
		done <- packErr
	}()
	Consistently(done).ShouldNot(Receive(), "Pack must wait on the held ranker authority")
	cancel()
	var packErr error
	Eventually(done).Should(Receive(&packErr))
	Expect(errors.Is(packErr, context.Canceled)).To(BeTrue())
})
var _ = It("pack invalidation attribution identifies runtime only state", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/runtimeonly\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "alpha.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change Alpha", Path: "pkg", Mode: "plan"}

	base, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "base Pack: %v", err)

	writeRuntimeTraceForTest(root, "trace-runtime-only", time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC))

	traced, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "traced Pack: %v", err)
	Expect(traced.PackID).NotTo(BeEquivalentTo(base.PackID), "runtime trace changes must produce a new cache key; both were %q", traced.PackID)

	rankerSource := cacheInvalidationSourceByName(traced.Cache.Invalidation.Sources, "ranker_weights")
	runtimeSource := cacheInvalidationSourceByName(traced.Cache.Invalidation.Sources, "runtime_traces")
	Expect(traced.Cache.RankerWeightsFingerprint).To(BeEquivalentTo(""), "expected ranker source to remain baseline for runtime-only state, source=%#v cache=%#v", rankerSource, traced.Cache)
	Expect(rankerSource).NotTo(BeNil(), "expected ranker source to remain baseline for runtime-only state, source=%#v cache=%#v", rankerSource, traced.Cache)
	Expect(rankerSource.Fingerprint).To(BeEquivalentTo(""), "expected ranker source to remain baseline for runtime-only state, source=%#v cache=%#v", rankerSource, traced.Cache)
	Expect(rankerSource.State).To(BeEquivalentTo("baseline_keyed"), "expected ranker source to remain baseline for runtime-only state, source=%#v cache=%#v", rankerSource, traced.Cache)
	Expect(traced.Cache.RuntimeTracesFingerprint).NotTo(BeEquivalentTo(""), "expected runtime source to identify runtime-only state, source=%#v cache=%#v", runtimeSource, traced.Cache)
	Expect(runtimeSource).NotTo(BeNil(), "expected runtime source to identify runtime-only state, source=%#v cache=%#v", runtimeSource, traced.Cache)
	Expect(runtimeSource.Fingerprint).To(BeEquivalentTo(traced.Cache.RuntimeTracesFingerprint), "expected runtime source to identify runtime-only state, source=%#v cache=%#v", runtimeSource, traced.Cache)
	Expect(runtimeSource.State).To(BeEquivalentTo("keyed"), "expected runtime source to identify runtime-only state, source=%#v cache=%#v", runtimeSource, traced.Cache)
})
var _ = It("pack cache hit survives no-op full rebuild", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/nooprebuild\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "initial Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"}

	first, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "first Pack: %v", err)
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "noop Build: %v", err)
	}

	second, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "second Pack: %v", err)
	Expect(first.PackID).To(BeEquivalentTo(second.PackID), "noop rebuild should preserve pack id, got %q then %q", first.PackID, second.PackID)

	hits, _, writes := ViewCacheCounters()
	Expect(hits).NotTo(BeEquivalentTo(0), "expected cache hit after noop rebuild and no rewrite, hits=%d writes=%d", hits, writes)
	Expect(writes).To(BeEquivalentTo(1), "expected cache hit after noop rebuild and no rewrite, hits=%d writes=%d", hits, writes)
})
var _ = It("pack cache key includes learned weights override", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/overrideweights\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc Alpha() string { return \"a\" }\n")
	writeFile(filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc Beta() string { return \"b\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	base := PackOptions{Root: root, Task: "update package", Path: "pkg", Mode: "plan"}
	first := base

	first.LearnedWeights = map[string]float64{"pkg/a.go": 2}

	firstPack, err := Pack(context.Background(), first)
	Expect(err).NotTo(HaveOccurred(), "first Pack: %v", err)

	second := base

	second.LearnedWeights = map[string]float64{"pkg/b.go": 2}

	secondPack, err := Pack(context.Background(), second)
	Expect(err).NotTo(HaveOccurred(), "second Pack: %v", err)
	Expect(firstPack.PackID).NotTo(BeEquivalentTo(secondPack.PackID), "learned weight overrides must produce distinct pack IDs; both were %q", firstPack.PackID)
})
var _ = It("load view by ID refuses stale referenced path", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/stalepacket\n\ngo 1.22\n")

	sourcePath := filepath.Join(root, "pkg", "x.go")
	writeFile(sourcePath, "package pkg\n\nfunc X() string { return \"x\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"})
	Expect(err).NotTo(HaveOccurred(), "Pack: %v", err)

	writeFile(sourcePath, "package pkg\n\nfunc X() string { return \"changed\" }\n")

	_, status, err := LoadViewByIDStatus(root, pack.PackID)
	Expect(err).NotTo(HaveOccurred(), "LoadViewByIDStatus: %v", err)
	Expect(status.State).To(Equal(ViewLoadStateStale), "stale referenced path should not rehydrate a cached packet: %#v", status)
})
var _ = It("load view by ID status reports explicit stale rows", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/stalerow\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	key := ViewKey{IndexID: build.IndexID, Task: "change X", Mode: "plan", Query: ViewQueryInputs{Path: "pkg/x.go"}}
	{
		err := SaveView(root, key, PackResult{
			PackID:         key.ID(),
			IndexID:        build.IndexID,
			Root:           root,
			Task:           "change X",
			Mode:           "plan",
			Fresh:          true,
			CandidateFiles: []PackFile{{Path: "pkg/x.go", Role: "editable"}},
		})
		Expect(err).NotTo(HaveOccurred(), "SaveView: %v", err)
	}
	{
		n, err := MarkAllViewsStale(root)
		Expect(err).To(BeNil(), "MarkAllViewsStale n=%d err=%v", n, err)
		Expect(n).To(BeEquivalentTo(1), "MarkAllViewsStale n=%d err=%v", n, err)
	}

	got, status, err := LoadViewByIDStatus(root, key.ID())
	Expect(err).NotTo(HaveOccurred(), "LoadViewByIDStatus: %v", err)
	Expect(status.State).To(BeEquivalentTo(ViewLoadStateStale), "expected explicit stale-row status, got status=%#v result=%#v", status, got)
	Expect(status.Reason).To(BeEquivalentTo("stale_row"), "expected explicit stale-row status, got status=%#v result=%#v", status, got)
	Expect(got.Fresh).To(BeFalse(), "expected stale result with packet contents preserved, got %#v", got)
	Expect(got.PackID).To(BeEquivalentTo(key.ID()), "expected stale result with packet contents preserved, got %#v", got)
	Expect(len(got.CandidateFiles)).To(BeEquivalentTo(1), "expected stale result with packet contents preserved, got %#v", got)
	{
		_, staleStatus, err := LoadViewByIDStatus(root, key.ID())
		Expect(err).To(BeNil(), "LoadViewByIDStatus should keep reporting stale row, status=%#v err=%v", staleStatus, err)
		Expect(staleStatus.State).To(BeEquivalentTo(ViewLoadStateStale), "LoadViewByIDStatus should keep reporting stale row, status=%#v err=%v", staleStatus, err)
	}
})
var _ = It("load view by ID status reports non current rows", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/noncurrentrow\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	key := ViewKey{IndexID: "old-index", Task: "change X", Mode: "plan", Query: ViewQueryInputs{Path: "pkg/x.go"}}
	{
		err := SaveView(root, key, PackResult{
			PackID:         key.ID(),
			IndexID:        "old-index",
			Root:           root,
			Task:           "change X",
			Mode:           "plan",
			Fresh:          true,
			CandidateFiles: []PackFile{{Path: "pkg/x.go", Role: "editable"}},
		})
		Expect(err).NotTo(HaveOccurred(), "SaveView: %v", err)
	}

	got, status, err := LoadViewByIDStatus(root, key.ID())
	Expect(err).NotTo(HaveOccurred(), "LoadViewByIDStatus: %v", err)
	Expect(status.State).To(BeEquivalentTo(ViewLoadStateStale), "expected non-current stale status against %s, got status=%#v result=%#v", build.IndexID, status, got)
	Expect(status.Reason).To(BeEquivalentTo("non_current_index"), "expected non-current stale status against %s, got status=%#v result=%#v", build.IndexID, status, got)
	Expect(status.CurrentIndexID).To(BeEquivalentTo(build.IndexID), "expected non-current stale status against %s, got status=%#v result=%#v", build.IndexID, status, got)
	Expect(got.Fresh).To(BeFalse(), "expected stale result with old packet contents preserved, got %#v", got)
	Expect(got.IndexID).To(BeEquivalentTo("old-index"), "expected stale result with old packet contents preserved, got %#v", got)
	Expect(len(got.CandidateFiles)).To(BeEquivalentTo(1), "expected stale result with old packet contents preserved, got %#v", got)
})
var _ = It("load view by ID allows unrelated stale path", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/scopedpacket\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	otherPath := filepath.Join(root, "other", "other.go")
	writeFile(otherPath, "package other\n\nfunc Other() string { return \"old\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	pack, err := Pack(context.Background(), PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"})
	Expect(err).NotTo(HaveOccurred(), "Pack: %v", err)

	writeFile(otherPath, "package other\n\nfunc Other() string { return \"new\" }\n")

	got, status, err := LoadViewByIDStatus(root, pack.PackID)
	Expect(err).NotTo(HaveOccurred(), "LoadViewByIDStatus: %v", err)
	Expect(status.State).To(BeEquivalentTo(ViewLoadStateFresh), "unrelated stale path should not invalidate scoped cached packet, got %#v", status)
	Expect(got.Freshness).NotTo(BeNil(), "expected scoped freshness with globally stale repo, got %#v", got.Freshness)
	Expect(got.Freshness.FreshForScope).To(BeTrue(), "expected scoped freshness with globally stale repo, got %#v", got.Freshness)
	Expect(got.Freshness.GlobalFresh).To(BeFalse(), "expected scoped freshness with globally stale repo, got %#v", got.Freshness)
})
var _ = It("pack uses cached view when only unrelated path is stale", func() {
	resetViewCacheCounters()

	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/scopedpack\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	otherPath := filepath.Join(root, "other", "other.go")
	writeFile(otherPath, "package other\n\nfunc Other() string { return \"old\" }\n")
	{
		_, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred(), "Build: %v", err)
	}

	opts := PackOptions{Root: root, Task: "change X", Path: "pkg/x.go", Mode: "plan"}

	first, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "first Pack: %v", err)

	writeFile(otherPath, "package other\n\nfunc Other() string { return \"new\" }\n")

	second, err := Pack(context.Background(), opts)
	Expect(err).NotTo(HaveOccurred(), "second Pack: %v", err)
	Expect(second.PackID).To(BeEquivalentTo(first.PackID), "expected cached pack id %q, got %q", first.PackID, second.PackID)

	hits, _, writes := ViewCacheCounters()
	Expect(hits).NotTo(BeEquivalentTo(0), "expected scoped cache hit without rewrite, hits=%d writes=%d", hits, writes)
	Expect(writes).To(BeEquivalentTo(1), "expected scoped cache hit without rewrite, hits=%d writes=%d", hits, writes)
	Expect(second.Freshness).NotTo(BeNil(), "expected scoped freshness hit with globally stale repo, got %#v", second.Freshness)
	Expect(second.Freshness.FreshForScope).To(BeTrue(), "expected scoped freshness hit with globally stale repo, got %#v", second.Freshness)
	Expect(second.Freshness.GlobalFresh).To(BeFalse(), "expected scoped freshness hit with globally stale repo, got %#v", second.Freshness)
})
var _ = It("save view persists freshness metadata", func() {
	root := GinkgoT().TempDir()
	writeFile(filepath.Join(root, "go.mod"), "module example.com/viewmeta\n\ngo 1.22\n")
	writeFile(filepath.Join(root, "pkg", "x.go"), "package pkg\n\nfunc X() string { return \"x\" }\n")

	build, err := Build(GinkgoT().Context(), BuildOptions{Root: root, Language: "go"})
	Expect(err).NotTo(HaveOccurred(), "Build: %v", err)

	key := ViewKey{IndexID: build.IndexID, Task: "meta", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp"}}
	{
		err := SaveView(root, key, PackResult{
			PackID:         key.ID(),
			IndexID:        build.IndexID,
			CandidateFiles: []PackFile{{Path: "pkg/x.go"}},
		})
		Expect(err).NotTo(HaveOccurred(), "SaveView: %v", err)
	}

	db, err := openSQLite(sqliteGraphPath(root))
	Expect(err).NotTo(HaveOccurred(), "open sqlite: %v", err)

	defer db.Close()

	var indexID, task, mode, workspaceFingerprint, refsJSON, fingerprintsJSON string
	{
		err := db.QueryRow(`SELECT index_id, task, mode, workspace_state_fingerprint, referenced_paths_json, source_fingerprints_json FROM graph_views WHERE id = ?`, key.ID()).Scan(&indexID, &task, &mode, &workspaceFingerprint, &refsJSON, &fingerprintsJSON)
		Expect(err).NotTo(HaveOccurred(), "read graph_views metadata: %v", err)
	}
	Expect(indexID).To(BeEquivalentTo(build.IndexID), "metadata mismatch: index=%q task=%q mode=%q workspace=%q", indexID, task, mode, workspaceFingerprint)
	Expect(task).To(BeEquivalentTo("meta"), "metadata mismatch: index=%q task=%q mode=%q workspace=%q", indexID, task, mode, workspaceFingerprint)
	Expect(mode).To(BeEquivalentTo("plan"), "metadata mismatch: index=%q task=%q mode=%q workspace=%q", indexID, task, mode, workspaceFingerprint)
	Expect(workspaceFingerprint).To(BeEquivalentTo("fp"), "metadata mismatch: index=%q task=%q mode=%q workspace=%q", indexID, task, mode, workspaceFingerprint)
	Expect(refsJSON).To(ContainSubstring("pkg/x.go"), "expected referenced paths and source fingerprints in metadata, refs=%s fingerprints=%s", refsJSON, fingerprintsJSON)
	Expect(fingerprintsJSON).To(ContainSubstring("pkg/x.go"), "expected referenced paths and source fingerprints in metadata, refs=%s fingerprints=%s", refsJSON, fingerprintsJSON)
})
var _ = It("changed paths between", func() {
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
	Expect(len(changed)).To(BeEquivalentTo(len(want)), "unexpected change set: %v", changed)

	for _, p := range changed {
		Expect(want[p]).To(BeTrue(), "unexpected path %q in change set", p)
	}
})
var _ = It("comprehension state fingerprint changes when weights change", func() {
	root := GinkgoT().TempDir()
	_, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"a.go"}, "", "fingerprint-first")
	Expect(err).NotTo(HaveOccurred())

	firstState, err := ComprehensionStateFingerprints(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	first := firstState.WorkspaceStateFingerprint
	Expect(first).NotTo(BeEquivalentTo(""), "expected non-empty fingerprint when weights file exists")

	_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"a.go"}, "", "fingerprint-second")
	Expect(err).NotTo(HaveOccurred())

	secondState, err := ComprehensionStateFingerprints(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	second := secondState.WorkspaceStateFingerprint
	Expect(second).NotTo(BeEquivalentTo(first), "owner generation must change when ranker state changes; both were %q", first)
})
var _ = It("comprehension state fingerprint empty without workspace state", func() {
	root := GinkgoT().TempDir()
	{
		state, err := ComprehensionStateFingerprints(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		got := state.WorkspaceStateFingerprint
		Expect(got).To(BeEquivalentTo(""), "expected empty workspace state fingerprint without weights or traces, got %q", got)
	}
})
var _ = It("comprehension state fingerprint changes when trace added", func() {
	root := GinkgoT().TempDir()
	tracesDir := filepath.Join(root, ".idiom", "comprehension", "traces")
	{
		err := os.MkdirAll(tracesDir, 0o755)
		Expect(err).NotTo(HaveOccurred(), "mkdir: %v", err)
	}

	firstState, err := ComprehensionStateFingerprints(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	first := firstState.WorkspaceStateFingerprint

	time.Sleep(20 * time.Millisecond)
	{
		err := os.WriteFile(filepath.Join(tracesDir, "abc123.json"), []byte(`{}`), 0o644)
		Expect(err).NotTo(HaveOccurred(), "seed trace: %v", err)
	}

	secondState, err := ComprehensionStateFingerprints(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred())
	second := secondState.WorkspaceStateFingerprint
	Expect(second).NotTo(BeEquivalentTo(first), "fingerprint must change when a trace file is added; both were %q", first)
})
var _ = It("runtime trace state fingerprint uses bounded manifest", func() {
	root := GinkgoT().TempDir()
	tracesDir := filepath.Join(root, ".idiom", "comprehension", "traces")
	{
		err := os.MkdirAll(tracesDir, 0o755)
		Expect(err).NotTo(HaveOccurred(), "mkdir traces: %v", err)
	}

	baseTime := time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC)

	var manifest traceManifest

	manifest.SchemaVersion = runtimeTraceManifestSchema
	manifest.UpdatedAt = baseTime.Format(time.RFC3339)

	for i := 0; i < 100; i++ {
		traceID := fmt.Sprintf("trace-%03d", i)
		generatedAt := baseTime.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		writeFile(filepath.Join(tracesDir, traceID+".json"), fmt.Sprintf(`{"trace_id":%q,"artifact_path":%q,"generated_at":%q,"command":"go test ./...","fresh":true}`+"\n", traceID, filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json")), generatedAt))

		if i >= 100-runtimeTraceRecentLimit {
			manifest.Traces = append(manifest.Traces, traceManifestEntry{
				TraceID:      traceID,
				ArtifactPath: filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json")),
				GeneratedAt:  generatedAt,
				Fingerprint:  fmt.Sprintf("fp-%03d", i),
			})
		}
	}

	writeTraceManifestForTest(root, manifest)

	state := runtimeTraceStateFingerprint(root, runtimeTraceRecentLimit)
	Expect(state.Fingerprint).NotTo(BeEquivalentTo(""), "expected bounded manifest fingerprint state, got %#v", state)
	Expect(state.Source).To(BeEquivalentTo("trace_manifest"), "expected bounded manifest fingerprint state, got %#v", state)
	Expect(state.Considered).To(BeEquivalentTo(runtimeTraceRecentLimit), "expected bounded manifest fingerprint state, got %#v", state)
	Expect(state.Total).To(BeEquivalentTo(runtimeTraceRecentLimit), "expected bounded manifest fingerprint state, got %#v", state)

	before := state.Fingerprint

	time.Sleep(20 * time.Millisecond)
	writeFile(filepath.Join(tracesDir, "trace-000.json"), `{"trace_id":"trace-000","generated_at":"2026-05-13T00:00:00Z","command":"changed old trace","fresh":true}`+"\n")

	afterOldTraceChange := runtimeTraceStateFingerprint(root, runtimeTraceRecentLimit)
	Expect(afterOldTraceChange.Fingerprint).To(BeEquivalentTo(before), "old retained traces outside the manifest horizon must not affect fingerprint work, before=%#v after=%#v", state, afterOldTraceChange)
	Expect(afterOldTraceChange.Considered).To(BeEquivalentTo(runtimeTraceRecentLimit), "old retained traces outside the manifest horizon must not affect fingerprint work, before=%#v after=%#v", state, afterOldTraceChange)

	manifest.Traces[0].Fingerprint = "changed-recent-fingerprint"
	writeTraceManifestForTest(root, manifest)

	afterRecentManifestChange := runtimeTraceStateFingerprint(root, runtimeTraceRecentLimit)
	Expect(afterRecentManifestChange.Fingerprint).NotTo(BeEquivalentTo(before), "recent manifest entries inside the horizon must affect the fingerprint")
})
var _ = It("view key ID incorporates workspace state fingerprint", func() {
	base := ViewKey{IndexID: "i", Task: "t", Mode: "plan"}
	withFP := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp1"}}
	other := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp2"}}
	Expect(base.ID()).NotTo(BeEquivalentTo(withFP.ID()), "adding workspace_state_fingerprint must change the ID")
	Expect(withFP.ID()).NotTo(BeEquivalentTo(other.ID()), "different workspace_state_fingerprint values must produce different IDs")

	withRanker := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp", RankerWeightsFingerprint: "ranker"}}
	withRuntime := ViewKey{IndexID: "i", Task: "t", Mode: "plan", Query: ViewQueryInputs{WorkspaceStateFingerprint: "fp", RuntimeTracesFingerprint: "runtime"}}
	Expect(withRanker.ID()).NotTo(BeEquivalentTo(withRuntime.ID()), "ranker/runtime component fingerprints must produce distinct IDs")
})

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

func writeRuntimeTraceForTest(root string, traceID string, generatedAt time.Time) {
	GinkgoHelper()

	result := TraceResult{
		TraceID:      traceID,
		ArtifactPath: filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json")),
		Root:         root,
		Command:      "go test ./...",
		GeneratedAt:  generatedAt.UTC().Format(time.RFC3339),
		Success:      true,
		Fresh:        true,
	}
	{
		err := saveTrace(root, result)
		Expect(err).NotTo(HaveOccurred(), "save trace: %v", err)
	}
}

func writeTraceManifestForTest(root string, manifest traceManifest) {
	GinkgoHelper()

	data, err := json.MarshalIndent(manifest, "", "  ")
	Expect(err).NotTo(HaveOccurred(), "marshal trace manifest: %v", err)

	path := traceManifestPath(root)
	{
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		Expect(err).NotTo(HaveOccurred(), "mkdir trace manifest dir: %v", err)
	}
	{
		err := os.WriteFile(path, append(data, '\n'), 0o644)
		Expect(err).NotTo(HaveOccurred(), "write trace manifest: %v", err)
	}
}
