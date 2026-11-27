package comprehension

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func rankedHitsOf(paths ...string) []LexicalHit {
	out := make([]LexicalHit, len(paths))
	for i, p := range paths {
		out[i] = LexicalHit{Path: p, Score: 1 / float64(i+1)}
	}

	return out
}

func TestBackfillKeepsHeadAndFillsSlots(t *testing.T) {
	base := rankedHitsOf("b1", "b2", "b3", "b4", "b5", "b6", "b7", "b8", "b9")
	history := rankedHitsOf("h1", "b2", "h2", "h3", "h4")

	got := paths(backfillOrder(base, history, 5))
	want := []string{"b1", "b2", "b3", "b4", "b5", "h1", "h2", "h3", "b6", "b7", "b8", "b9", "h4"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

// Backfill cannot lower any rank metric for a case whose gold is already in
// the kept head: the head is identical, in the same order.
func TestBackfillHeadIsUntouchedForEveryKeep(t *testing.T) {
	base := rankedHitsOf("b1", "b2", "b3", "b4", "b5", "b6", "b7", "b8", "b9")
	history := rankedHitsOf("h1", "h2", "h3", "b9", "h4", "h5", "h6", "h7", "h8")

	for keep := 0; keep <= 9; keep++ {
		out := backfillOrder(base, history, keep)

		if !reflect.DeepEqual(paths(out[:keep]), paths(base[:keep])) {
			t.Errorf("keep %d: head moved: %v", keep, paths(out[:keep]))
		}

		for i := 1; i < len(out); i++ {
			if out[i].Score > out[i-1].Score {
				t.Errorf("keep %d: score rises at %d (%v then %v)", keep, i, out[i-1].Score, out[i].Score)
			}
		}
	}

	if got := backfillOrder(base, history, 8); !reflect.DeepEqual(paths(got[:8]), paths(base[:8])) {
		t.Errorf("keep 8 must leave the top 8 identical, got %v", paths(got[:8]))
	}
}

// With no history section the channel reads nothing and the ranking is the
// plain fusion; any mode but backfill or fuse is off.
func TestPastWorkIsOffByDefault(t *testing.T) {
	dir := t.TempDir()

	if cfg, err := LoadHistoryConfig(dir); err != nil || cfg.enabled() {
		t.Fatalf("missing config must be off, got %+v, %v", cfg, err)
	}

	write := func(body string) {
		path := filepath.Join(dir, ".idiom", "config.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, body := range []string{`{"history":{}}`, `{"history":{"mode":"off","ledger_path":"x"}}`, `{"history":{"mode":"bogus"}}`} {
		write(body)

		cfg, _ := LoadHistoryConfig(dir)
		if cfg.enabled() || historyFingerprint(dir) != "" {
			t.Errorf("%s must be off", body)
		}

		if h := LoadChangeHistory(context.Background(), dir, cfg); len(h.Commits)+len(h.Items) != 0 {
			t.Errorf("%s read history", body)
		}
	}

	base := []rankedList{{Source: "bm25", Hits: rankedHitsOf("a", "b")}, {Source: "graph", Hits: rankedHitsOf("b", "c")}}
	ctx := &packContext{root: dir, requestContext: context.Background()}

	if got, want := fuseWithPastWork(ctx, allowed, base), fuseRankedLists(base...); !reflect.DeepEqual(got, want) {
		t.Fatalf("default ranking differs from plain fusion:\n%v\n%v", got, want)
	}
}

func TestHistoryCacheMatchesGitAndFollowsHead(t *testing.T) {
	r := newFixtureRepo(t)
	r.commit(1, "first", "a.go")
	r.commit(2, "second", "b.go", "a.go")

	cached, headTime := cachedRawCommits(context.Background(), r.root) // cold: runs git, writes the cache
	fresh, freshTime := readRawCommits(context.Background(), r.root)

	if !reflect.DeepEqual(cached, fresh) || headTime != freshTime {
		t.Fatalf("cold read differs from git")
	}

	if _, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(historyCacheRelPath))); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	warm, warmTime := cachedRawCommits(context.Background(), r.root)
	if !reflect.DeepEqual(warm, fresh) || warmTime != freshTime {
		t.Fatal("warm read differs from git")
	}

	r.commit(3, "third", "c.go")

	if next, _ := cachedRawCommits(context.Background(), r.root); len(next) != len(fresh)+1 {
		t.Fatalf("a new commit must miss the cache: %d commits, want %d", len(next), len(fresh)+1)
	}
}
