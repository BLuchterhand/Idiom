package comprehension

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLexicalTokensSplitsIdentifiers(t *testing.T) {
	got := LexicalTokens("SunShadow day_clock HTTPServer bt::client::Ambience G713 the shadows")
	want := []string{
		"sun", "shadow", "sunshadow", "day", "clock", "dayclock", "http", "server", "httpserver",
		"bt", "client", "ambience", "g713", "shadow",
	}

	if !slices.Equal(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestLexicalTokensDropNoise(t *testing.T) {
	got := LexicalTokens("a 42 of x the designer")
	if len(got) != 0 {
		t.Fatalf("single letters, numbers and stopwords must not index, got %v", got)
	}
}

func TestIsLexicalTestPath(t *testing.T) {
	for path, want := range map[string]bool{
		"core/tests/test_sim.cpp":  true,
		"internal/foo/bar_test.go": true,
		"scripts/test_affected.sh": true,
		"core/sim/sim.cpp":         false,
		"client/latest_notes.cpp":  false,
	} {
		if got := IsLexicalTestPath(path); got != want {
			t.Errorf("IsLexicalTestPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func lexicalCorpusOf(docs map[string]map[string]int) LexicalCorpus {
	corpus := LexicalCorpus{Postings: map[string][]LexicalPosting{}}

	for path, terms := range docs {
		length := 0
		for _, tf := range terms {
			length += tf
		}

		corpus.Docs++
		corpus.TotalLen += length

		for term, tf := range terms {
			corpus.Postings[term] = append(corpus.Postings[term], LexicalPosting{Path: path, Length: length, TF: tf})
		}
	}

	return corpus
}

func TestRankLexicalDemotesTestFiles(t *testing.T) {
	corpus := lexicalCorpusOf(map[string]map[string]int{
		"core/sim/ambience.cpp":          {"ambience": 2, "wind": 1},
		"core/tests/test_ambience.cpp":   {"ambience": 2, "wind": 1},
		"core/sim/unrelated.cpp":         {"other": 3},
		"core/sim/another_unrelated.cpp": {"misc": 3},
	})

	hits := RankLexical(corpus, []string{"ambience", "wind"}, nil, nil)
	if len(hits) != 2 || hits[0].Path != "core/sim/ambience.cpp" {
		t.Fatalf("hits = %+v, want the source file first", hits)
	}

	// The demotion factor is part of the measured ranker (x0.5), so the
	// test pins the number, not the constant.
	if ratio := hits[1].Score / hits[0].Score; ratio < 0.5-1e-9 || ratio > 0.5+1e-9 {
		t.Fatalf("identical test file scored x%.3f of the source, want x0.5", ratio)
	}

	// A test file that matches far better than the source still wins: the
	// demotion scales the score, it does not bury the file.
	strong := lexicalCorpusOf(map[string]map[string]int{
		"core/sim/ambience.cpp":        {"ambience": 1, "filler": 9},
		"core/tests/test_ambience.cpp": {"ambience": 6, "wind": 4},
		"core/sim/other.cpp":           {"other": 3},
	})
	if hits := RankLexical(strong, []string{"ambience", "wind"}, nil, nil); hits[0].Path != "core/tests/test_ambience.cpp" {
		t.Fatalf("a much stronger test match must still lead, got %+v", hits)
	}
}

func TestRankLexicalScopeFiltersWithoutChangingScores(t *testing.T) {
	corpus := lexicalCorpusOf(map[string]map[string]int{
		"billing/a.go": {"refund": 1, "filler": 4},
		"billing/b.go": {"billing": 6},
		"other/c.go":   {"refund": 3},
		"other/d.go":   {"misc": 2},
	})

	all := RankLexical(corpus, []string{"refund"}, nil, nil)
	scoped := RankLexical(corpus, []string{"refund"}, func(path string) bool { return pathInScope(path, "billing") }, nil)

	if len(scoped) != 1 || scoped[0].Path != "billing/a.go" {
		t.Fatalf("scoped hits = %+v, want only billing/a.go", scoped)
	}

	for _, hit := range all {
		if hit.Path == "billing/a.go" && hit.Score != scoped[0].Score {
			t.Fatalf("scope changed a score: %v vs %v", hit.Score, scoped[0].Score)
		}
	}
}

func TestQueryTermsIgnoreScope(t *testing.T) {
	plain := queryTerms(QueryOptions{Query: "refund policy"})
	scoped := queryTerms(QueryOptions{Query: "refund policy", Path: "billing/engine", Scope: "billing"})

	if !slices.Equal(plain, scoped) {
		t.Fatalf("scope leaked into query terms: %v vs %v", scoped, plain)
	}
}

// A directory named only by the scope must not pull in its files: the task
// says "refund", billing/b.go never mentions it, so it stays out of the pack
// whether or not the pack is scoped to billing/.
func TestPackScopeIsAFilterNotATerm(t *testing.T) {
	root := t.TempDir()

	for path, content := range map[string]string{
		"go.mod":          "module example.com/scoped\n\ngo 1.22\n",
		"billing/a.go":    "package billing\n\n// Refund returns money.\nfunc Refund() int { return 1 }\n",
		"billing/b.go":    "package billing\n\nfunc Ledger() int { return 2 }\n",
		"shipping/c.go":   "package shipping\n\nfunc Ship() int { return 3 }\n",
		"shipping/d.go":   "package shipping\n\n// Refund of postage.\nfunc Refund() int { return 4 }\n// Refund again\n",
		"shipping/e_x.go": "package shipping\n\nfunc Pad() int { return 5 }\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatal(err)
	}

	paths := func(opts PackOptions) []string {
		opts.Root, opts.Task, opts.Now = root, "refund", testTime()

		pack, err := Pack(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}

		var out []string
		for _, file := range pack.CandidateFiles {
			out = append(out, file.Path)
		}

		return out
	}

	unscoped := paths(PackOptions{})
	scoped := paths(PackOptions{Path: "billing"})

	if !slices.Equal(scoped, []string{"billing/a.go"}) {
		t.Fatalf("scoped to billing/ the only refund file is a.go (b.go must not ride on its directory name), got %v", scoped)
	}

	if slices.Contains(unscoped, "billing/b.go") {
		t.Fatalf("billing/b.go never mentions the task and must not appear, got %v", unscoped)
	}

	if len(unscoped) != 2 {
		t.Fatalf("unscoped pack should retrieve every refund file, got %v", unscoped)
	}
}

func hitsOf(paths ...string) []LexicalHit {
	out := make([]LexicalHit, len(paths))
	for i, p := range paths {
		out[i] = LexicalHit{Path: p}
	}

	return out
}

func TestFuseRankedListsReciprocalRank(t *testing.T) {
	bm25 := rankedList{Source: "bm25", Hits: append(hitsOf("a", "b", "c"), LexicalHit{Path: "d", Terms: []string{"t"}})}
	graph := rankedList{Source: "graph", Hits: hitsOf("c", "x", "a")}

	got := fuseRankedLists(bm25, graph)

	var order []string
	for _, h := range got {
		order = append(order, h.Path)
	}

	// a: 1/61+1/63, c: 1/63+1/61 (tie, by path), then b 1/62, x 1/62 (tie), d 1/64.
	if want := []string{"a", "c", "b", "x", "d"}; !slices.Equal(order, want) {
		t.Fatalf("fused order = %v, want %v", order, want)
	}

	if want := 1.0/61 + 1.0/63; got[0].Score < want-1e-12 || got[0].Score > want+1e-12 {
		t.Fatalf("a scored %v, want %v (k=60, equal weights)", got[0].Score, want)
	}

	if got[4].Path != "d" || len(got[4].Terms) != 1 {
		t.Fatalf("matched terms must survive fusion, got %+v", got[4])
	}
}

func TestFuseRankedListsCutsAtDepth(t *testing.T) {
	var long []string
	for i := 0; i < fusionDepth+5; i++ {
		long = append(long, string(rune('a'+i)))
	}

	got := fuseRankedLists(rankedList{Hits: hitsOf(long...)})
	if len(got) != fusionDepth {
		t.Fatalf("fused %d files, want depth %d", len(got), fusionDepth)
	}
}

func TestReadLexicalTextSkipsOversizeAndMissingFiles(t *testing.T) {
	root := t.TempDir()

	small := filepath.Join(root, "small.go")
	if err := os.WriteFile(small, []byte("package small"), 0o644); err != nil {
		t.Fatal(err)
	}

	big := filepath.Join(root, "big.go")
	if err := os.WriteFile(big, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Truncate(big, LexicalMaxFileBytes+1); err != nil { // sparse: nothing is written
		t.Fatal(err)
	}

	if text, err := readLexicalText(root, "small.go"); err != nil || string(text) != "package small" {
		t.Fatalf("small file = %q, %v", text, err)
	}

	if text, err := readLexicalText(root, "big.go"); err != nil || text != nil {
		t.Fatalf("a file over %d bytes must not be read, got %d bytes, %v", LexicalMaxFileBytes, len(text), err)
	}

	if text, err := readLexicalText(root, "gone.go"); err != nil || text != nil {
		t.Fatalf("a missing file indexes by path only, got %v, %v", text, err)
	}
}
