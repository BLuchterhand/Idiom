package comprehension

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Dense channel tests run against a fake embedder: a bag of words over a
// small vocabulary, so "similar" means shared words and every vector is exact.
// The suite never talks to a real ollama.

func init() { denseEmbedderFactory = func() DenseEmbedder { return nil } }

var denseVocab = []string{"alpha", "beta", "gamma", "delta", "water", "fire", "river", "stone"}

type fakeEmbedder struct {
	mu       sync.Mutex
	model    DenseModel
	modelErr error
	embedErr error
	texts    []string // every text embedded, in order
	queries  int
}

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{model: DenseModel{Name: "fake", Digest: "d1"}}
}

func (f *fakeEmbedder) Model(context.Context) (DenseModel, error) { return f.model, f.modelErr }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string, query bool) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.embedErr != nil {
		return nil, f.embedErr
	}

	out := make([][]float32, len(texts))

	for i, text := range texts {
		if query {
			f.queries++
		} else {
			f.texts = append(f.texts, text)
		}

		v := make([]float32, len(denseVocab)+1)
		lower := strings.ToLower(text)

		for j, w := range denseVocab {
			v[j] = float32(strings.Count(lower, w))
		}

		sum := sha256.Sum256([]byte(text)) // makes distinct texts distinct
		v[len(denseVocab)] = float32(sum[0]) / 4096
		normalizeVector(v)
		out[i] = v
	}

	return out, nil
}

type fakeHost interface {
	Setenv(key, value string)
	Cleanup(func())
}

// useFake installs f as the embedder and disables the shared cache.
func useFake(t fakeHost, f DenseEmbedder) {

	old, oldEnabled := denseEmbedderFactory, denseEnabled
	denseEmbedderFactory = func() DenseEmbedder { return f }
	denseEnabled = func() bool { return true }

	t.Setenv(denseCacheDirEnv, "off")
	t.Cleanup(func() { denseEmbedderFactory, denseEnabled = old, oldEnabled })
}

// denseRepo builds a small graph store (file nodes only) and returns the root.
func denseRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for p, c := range files {
		denseWrite(filepath.Join(root, p), c)
	}

	return root
}

func denseGraphWith(t *testing.T, root string, files map[string]string) {
	t.Helper()

	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		if mkErr := os.MkdirAll(filepath.Dir(sqliteGraphPath(root)), 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}

		if db, err = openSQLite(sqliteGraphPath(root)); err != nil {
			t.Fatal(err)
		}
	}
	defer db.Close()

	for _, stmt := range []string{`DROP TABLE IF EXISTS graph_nodes`, `CREATE TABLE graph_nodes (id TEXT, kind TEXT, path TEXT, fingerprint TEXT)`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	for p, c := range files {
		sum := sha256.Sum256([]byte(c))
		if _, err := db.Exec(`INSERT INTO graph_nodes VALUES(?, 'file', ?, ?)`, "file://"+p, p, string(sum[:])); err != nil {
			t.Fatal(err)
		}
	}
}

func syncDense(t *testing.T, root string, f DenseEmbedder) DenseSyncReport {
	t.Helper()

	report, err := SyncDenseIndex(context.Background(), root, f)
	if err != nil {
		t.Fatal(err)
	}

	return report
}

func denseDocPaths(t *testing.T, root string) []string {
	t.Helper()

	store, err := openDenseStore(denseStorePath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	have, _ := store.docPaths()
	out := make([]string, 0, len(have))

	for p := range have {
		out = append(out, p)
	}

	slices.Sort(out)

	return out
}

func TestDenseSyncSurvivesEditRenameAndDelete(t *testing.T) {
	files := map[string]string{"a.go": "alpha alpha beta", "b.go": "water river", "c.go": "fire stone", "bin.dat": "x\x00y"}
	root := denseRepo(t, files)
	f := newFakeEmbedder()
	useFake(t, f)

	denseGraphWith(t, root, files)

	if r := syncDense(t, root, f); r.Embedded != 3 || r.Skipped != 1 {
		t.Fatalf("cold sync: %+v, want 3 embedded and the binary file skipped", r)
	}

	before := len(f.texts)

	if r := syncDense(t, root, f); r.Embedded != 0 || len(f.texts) != before {
		t.Fatalf("unchanged tree must not re-embed: %+v", r)
	}

	// edit a.go, rename c.go to d.go, delete b.go
	files["a.go"] = "alpha gamma"
	files["d.go"] = files["c.go"]

	delete(files, "c.go")
	delete(files, "b.go")

	for _, p := range []string{"b.go", "c.go"} {
		_ = os.Remove(filepath.Join(root, p))
	}

	denseWrite(filepath.Join(root, "a.go"), files["a.go"])
	denseWrite(filepath.Join(root, "d.go"), files["d.go"])
	denseGraphWith(t, root, files)

	r := syncDense(t, root, f)
	if r.Embedded != 2 || r.Removed != 3 {
		t.Fatalf("edit+rename+delete: %+v, want a.go and d.go re-embedded, a, b, c dropped", r)
	}

	if got := denseDocPaths(t, root); !slices.Equal(got, []string{"a.go", "d.go"}) {
		t.Fatalf("docs = %v", got)
	}

	// a cold build of the final tree stores the same rows
	cold := denseRepo(t, files)
	denseGraphWith(t, cold, files)
	syncDense(t, cold, newFakeEmbedder())

	if a, b := denseRows(t, root), denseRows(t, cold); !slices.Equal(a, b) {
		t.Fatalf("incremental %v != cold %v", a, b)
	}
}

func denseRows(t *testing.T, root string) []string {
	t.Helper()

	store, err := openDenseStore(denseStorePath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rows, err := store.db.Query(`SELECT d.path, d.vec_key, hex(v.vec) FROM dense_docs d JOIN dense_vectors v USING(vec_key) ORDER BY d.path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var p, k, v string
		if err := rows.Scan(&p, &k, &v); err != nil {
			t.Fatal(err)
		}

		out = append(out, p+"|"+k+"|"+v)
	}

	return out
}

func TestDenseModelChangeDiscardsVectors(t *testing.T) {
	files := map[string]string{"a.go": "alpha"}
	root := denseRepo(t, files)
	f := newFakeEmbedder()
	useFake(t, f)
	denseGraphWith(t, root, files)
	syncDense(t, root, f)

	f.model.Digest = "d2"

	if r := syncDense(t, root, f); r.Embedded != 1 {
		t.Fatalf("a new model digest must re-embed everything: %+v", r)
	}

	if out := denseHits(context.Background(), root, "alpha", nil); len(out.Hits) != 1 {
		t.Fatalf("channel must run against the new model: %+v", out)
	}

	f.model.Digest = "d3" // query side sees a newer model than the store

	if out := denseHits(context.Background(), root, "alpha", nil); len(out.Hits) != 0 || !strings.Contains(out.Note, "re-run context_index") {
		t.Fatalf("stale model must be skipped with a note: %+v", out)
	}
}

func TestDenseFallbackNeverFailsTheQuery(t *testing.T) {
	files := map[string]string{"a.go": "alpha"}
	root := denseRepo(t, files)
	f := newFakeEmbedder()
	useFake(t, f)
	denseGraphWith(t, root, files)

	// no index yet
	if out := denseHits(context.Background(), root, "alpha", nil); len(out.Hits) != 0 || out.Note == "" {
		t.Fatalf("missing index: %+v", out)
	}

	syncDense(t, root, f)

	f.modelErr = errors.Join(errDenseUnavailable, errors.New("model qwen is not installed"))

	out := denseHits(context.Background(), root, "alpha", nil)
	if len(out.Hits) != 0 || !strings.Contains(out.Note, "dense channel skipped") || !out.Transient {
		t.Fatalf("missing model: %+v", out)
	}

	f.modelErr = nil
	f.embedErr = errors.New("connection refused")

	if out := denseHits(context.Background(), root, "never seen task", nil); len(out.Hits) != 0 || !out.Transient {
		t.Fatalf("embed failure: %+v", out)
	}

	// sync failure is reported, not fatal, and leaves the old store usable
	if _, err := SyncDenseIndex(context.Background(), root, f); err != nil {
		t.Fatalf("an unchanged tree needs no embedding: %v", err)
	}

	denseEmbedderFactory = func() DenseEmbedder { return nil }

	if out := denseHits(context.Background(), root, "alpha", nil); out.Note != "" || out.Hits != nil {
		t.Fatalf("disabled channel must be silent: %+v", out)
	}
}

func TestDenseRankIsDeterministicAndCachesQueries(t *testing.T) {
	files := map[string]string{
		"x/water.go": "water river water", "x/fire.go": "fire stone", "x/twin1.go": "alpha", "x/twin2.go": "alpha",
	}
	root := denseRepo(t, files)
	f := newFakeEmbedder()
	useFake(t, f)
	denseGraphWith(t, root, files)
	syncDense(t, root, f)

	first := denseHits(context.Background(), root, "river water", nil)
	if len(first.Hits) == 0 || first.Hits[0].Path != "x/water.go" {
		t.Fatalf("water task must rank the water file first: %+v", first.Hits)
	}

	again := denseHits(context.Background(), root, "river water", nil)
	if !slices.Equal(paths(first.Hits), paths(again.Hits)) || f.queries != 1 {
		t.Fatalf("second identical query must hit the cache (embeds=%d) and repeat the order", f.queries)
	}

	// alpha twins differ only by path text in the fake (distinct hashes), but
	// an exact tie must break by path.
	tied := []LexicalHit{{Path: "b", Score: 1}, {Path: "a", Score: 1}}
	slices.SortFunc(tied, func(x, y LexicalHit) int { return strings.Compare(x.Path, y.Path) })

	only := denseHits(context.Background(), root, "river water", func(p string) bool { return strings.HasSuffix(p, "fire.go") })
	if len(only.Hits) != 1 || only.Hits[0].Path != "x/fire.go" {
		t.Fatalf("admit must filter: %+v", only.Hits)
	}
}

func paths(hits []LexicalHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Path
	}

	return out
}

func TestDenseTieBreaksByPath(t *testing.T) {
	root := t.TempDir()

	store, err := openDenseStore(denseStorePath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	v := []float32{1, 0}
	for _, p := range []string{"b.go", "a.go", "c.go"} {
		w := denseWant{p, "fp"}
		if err := store.putVectorAndDoc(w, "k-"+p, v); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := store.rank(v, nil)
	if err != nil || !slices.Equal(paths(hits), []string{"a.go", "b.go", "c.go"}) {
		t.Fatalf("equal scores must order by path: %v %v", paths(hits), err)
	}
}

func TestSharedBlobCacheSpares(t *testing.T) {
	files := map[string]string{"a.go": "alpha", "b.go": "beta"}
	f := newFakeEmbedder()
	useFake(t, f)
	t.Setenv(denseCacheDirEnv, t.TempDir())

	one := denseRepo(t, files)
	denseGraphWith(t, one, files)
	syncDense(t, one, f)

	two := denseRepo(t, files) // a second worktree of the same content
	denseGraphWith(t, two, files)

	if r := syncDense(t, two, f); r.Embedded != 0 || r.Reused != 2 {
		t.Fatalf("a fresh index of known content must reuse cached vectors: %+v", r)
	}
}

func TestFuseDenseWeightScalesAndDrops(t *testing.T) {
	bm25 := rankedList{Source: "bm25", Hits: hitsOf("a", "b")}
	dense := rankedList{Source: "dense", Hits: hitsOf("b", "a", "z"), Weight: func(p string) float64 {
		switch p {
		case "b":
			return 2
		case "z":
			return 0
		}

		return 0.5
	}}

	got := fuseRankedLists(bm25, dense)
	scores := map[string]float64{}

	for _, h := range got {
		scores[h.Path] = h.Score
	}

	if _, ok := scores["z"]; ok {
		t.Fatal("a weight of 0 must drop the hit")
	}

	if want := 1.0/62 + 2.0/61; scores["b"] < want-1e-12 || scores["b"] > want+1e-12 {
		t.Fatalf("b = %v, want %v", scores["b"], want)
	}

	if want := 1.0/61 + 0.5/62; scores["a"] < want-1e-12 || scores["a"] > want+1e-12 {
		t.Fatalf("a = %v, want %v", scores["a"], want)
	}

	if got[0].Path != "b" || !slices.Contains(got[0].Sources, "dense") {
		t.Fatalf("weighted dense must lead and be named: %+v", got[0])
	}
}

func denseWrite(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func TestDensePolicyOffSkipsSync(t *testing.T) {
	files := map[string]string{"a.go": "alpha"}
	root := denseRepo(t, files)
	f := newFakeEmbedder()
	useFake(t, f)
	denseGraphWith(t, root, files)

	denseEnabled = denseEnabledByPolicy // the shipped policy: no language uses the channel

	if denseEnabled() {
		t.Skip("a language enables the dense channel; the off-by-policy case is not applicable")
	}

	syncDenseIndexBestEffort(root)

	if _, err := os.Stat(denseStorePath(root)); err == nil || len(f.texts) != 0 {
		t.Fatal("a policy with no dense weight must not embed or create a store")
	}
}
