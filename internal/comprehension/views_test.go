package comprehension

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestViewKeyIDDeterministic(t *testing.T) {
	k1 := ViewKey{
		IndexID: "idx-abc",
		Task:    "wire packet cache",
		Mode:    "plan",
		Query: ViewQueryInputs{
			Path:           "internal/comprehension",
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
			Path:           "internal/comprehension",
			Limit:          8,
			ForbiddenPaths: []string{".idiom", "/tmp"}, // reordered
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
	key := ViewKey{IndexID: "idx-xyz", Task: "round trip", Mode: "plan"}
	original := PackResult{
		PackID:  key.ID(),
		IndexID: "idx-xyz",
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
	keyA := ViewKey{IndexID: "i", Task: "task-a", Mode: "plan"}
	resA := PackResult{
		PackID:         keyA.ID(),
		IndexID:        "i",
		CandidateFiles: []PackFile{{Path: "alpha.go"}},
	}
	keyB := ViewKey{IndexID: "i", Task: "task-b", Mode: "plan"}
	resB := PackResult{
		PackID:         keyB.ID(),
		IndexID:        "i",
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
	key := ViewKey{IndexID: "i", Task: "t", Mode: "plan"}
	if err := SaveView(root, key, PackResult{PackID: key.ID(), IndexID: "i"}); err != nil {
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
}
