package comprehension

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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

func hitsOf(paths ...string) []LexicalHit {
	out := make([]LexicalHit, len(paths))
	for i, p := range paths {
		out[i] = LexicalHit{Path: p}
	}

	return out
}

var _ = Describe("lexical tokens", func() {
	It("splits identifiers", func() {
		got := LexicalTokens("SunShadow day_clock HTTPServer bt::client::Ambience G713 the shadows")
		want := []string{
			"sun", "shadow", "sunshadow", "day", "clock", "dayclock", "http", "server", "httpserver",
			"bt", "client", "ambience", "g713", "shadow",
		}

		Expect(got).To(Equal(want))
	})

	It("drops noise", func() {
		got := LexicalTokens("a 42 of x the designer")
		Expect(got).To(BeEmpty(), "single letters, numbers and stopwords must not index")
	})

	DescribeTable("classifies test paths",
		func(path string, want bool) {
			Expect(IsLexicalTestPath(path)).To(Equal(want), "IsLexicalTestPath(%q)", path)
		},
		Entry(nil, "core/tests/test_sim.cpp", true),
		Entry(nil, "internal/foo/bar_test.go", true),
		Entry(nil, "scripts/test_affected.sh", true),
		Entry(nil, "core/sim/sim.cpp", false),
		Entry(nil, "client/latest_notes.cpp", false),
	)
})

var _ = Describe("lexical ranking", func() {
	It("demotes test files", func() {
		corpus := lexicalCorpusOf(map[string]map[string]int{
			"core/sim/ambience.cpp":          {"ambience": 2, "wind": 1},
			"core/tests/test_ambience.cpp":   {"ambience": 2, "wind": 1},
			"core/sim/unrelated.cpp":         {"other": 3},
			"core/sim/another_unrelated.cpp": {"misc": 3},
		})

		hits := RankLexical(corpus, []string{"ambience", "wind"}, nil)
		Expect(hits).To(HaveLen(2))
		Expect(hits[0].Path).To(Equal("core/sim/ambience.cpp"), "the source file first, hits = %+v", hits)

		// The demotion factor is part of the measured ranker (x0.5), so the
		// test pins the number, not the constant.
		Expect(hits[1].Score/hits[0].Score).To(BeNumerically("~", 0.5, 1e-9), "identical test file must score x0.5 of the source")

		// A test file that matches far better than the source still wins: the
		// demotion scales the score, it does not bury the file.
		strong := lexicalCorpusOf(map[string]map[string]int{
			"core/sim/ambience.cpp":        {"ambience": 1, "filler": 9},
			"core/tests/test_ambience.cpp": {"ambience": 6, "wind": 4},
			"core/sim/other.cpp":           {"other": 3},
		})
		strongHits := RankLexical(strong, []string{"ambience", "wind"}, nil)
		Expect(strongHits[0].Path).To(Equal("core/tests/test_ambience.cpp"), "a much stronger test match must still lead, got %+v", strongHits)
	})

	It("filters by scope without changing scores", func() {
		corpus := lexicalCorpusOf(map[string]map[string]int{
			"billing/a.go": {"refund": 1, "filler": 4},
			"billing/b.go": {"billing": 6},
			"other/c.go":   {"refund": 3},
			"other/d.go":   {"misc": 2},
		})

		all := RankLexical(corpus, []string{"refund"}, nil)
		scoped := RankLexical(corpus, []string{"refund"}, func(path string) bool { return pathInScope(path, "billing") })

		Expect(scoped).To(HaveLen(1))
		Expect(scoped[0].Path).To(Equal("billing/a.go"), "scoped hits = %+v", scoped)

		for _, hit := range all {
			if hit.Path == "billing/a.go" {
				Expect(hit.Score).To(Equal(scoped[0].Score), "scope changed a score")
			}
		}
	})
})

var _ = Describe("lexical query and pack scope", func() {
	It("ignores scope in query terms", func() {
		plain := queryTerms(QueryOptions{Query: "refund policy"})
		scoped := queryTerms(QueryOptions{Query: "refund policy", Path: "billing/engine", Scope: "billing"})

		Expect(slices.Equal(plain, scoped)).To(BeTrue(), "scope leaked into query terms: %v vs %v", scoped, plain)
	})

	// A directory named only by the scope must not pull in its files: the task
	// says "refund", billing/b.go never mentions it, so it stays out of the pack
	// whether or not the pack is scoped to billing/.
	It("treats pack scope as a filter, not a term", func() {
		root := GinkgoT().TempDir()

		for path, content := range map[string]string{
			"go.mod":          "module example.com/scoped\n\ngo 1.22\n",
			"billing/a.go":    "package billing\n\n// Refund returns money.\nfunc Refund() int { return 1 }\n",
			"billing/b.go":    "package billing\n\nfunc Ledger() int { return 2 }\n",
			"shipping/c.go":   "package shipping\n\nfunc Ship() int { return 3 }\n",
			"shipping/d.go":   "package shipping\n\n// Refund of postage.\nfunc Refund() int { return 4 }\n// Refund again\n",
			"shipping/e_x.go": "package shipping\n\nfunc Pad() int { return 5 }\n",
		} {
			full := filepath.Join(root, filepath.FromSlash(path))
			Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
			Expect(os.WriteFile(full, []byte(content), 0o644)).To(Succeed())
		}

		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred())

		paths := func(opts PackOptions) []string {
			opts.Root, opts.Task, opts.Now = root, "refund", testTime()

			pack, packErr := Pack(context.Background(), opts)
			Expect(packErr).NotTo(HaveOccurred())

			var out []string
			for _, file := range pack.CandidateFiles {
				out = append(out, file.Path)
			}

			return out
		}

		unscoped := paths(PackOptions{})
		scoped := paths(PackOptions{Path: "billing"})

		Expect(scoped).To(Equal([]string{"billing/a.go"}), "scoped to billing/ the only refund file is a.go (b.go must not ride on its directory name)")
		Expect(unscoped).NotTo(ContainElement("billing/b.go"), "billing/b.go never mentions the task")
		Expect(unscoped).To(HaveLen(2), "unscoped pack should retrieve every refund file")
	})
})

var _ = Describe("fusing ranked lists", func() {
	It("uses reciprocal rank", func() {
		bm25 := rankedList{Source: "bm25", Hits: append(hitsOf("a", "b", "c"), LexicalHit{Path: "d", Terms: []string{"t"}})}
		graph := rankedList{Source: "graph", Hits: hitsOf("c", "x", "a")}

		got := fuseRankedLists(bm25, graph)

		var order []string
		for _, h := range got {
			order = append(order, h.Path)
		}

		// a: 1/61+1/63, c: 1/63+1/61 (tie, by path), then b 1/62, x 1/62 (tie), d 1/64.
		Expect(order).To(Equal([]string{"a", "c", "b", "x", "d"}))
		Expect(got[0].Score).To(BeNumerically("~", 1.0/61+1.0/63, 1e-12), "k=60, equal weights")
		Expect(got[4].Path).To(Equal("d"))
		Expect(got[4].Terms).To(HaveLen(1), "matched terms must survive fusion, got %+v", got[4])
	})

	It("cuts at the fusion depth", func() {
		var long []string
		for i := 0; i < fusionDepth+5; i++ {
			long = append(long, string(rune('a'+i)))
		}

		Expect(fuseRankedLists(rankedList{Hits: hitsOf(long...)})).To(HaveLen(fusionDepth))
	})
})

var _ = Describe("reading lexical text", func() {
	It("skips oversize and missing files", func() {
		root := GinkgoT().TempDir()

		Expect(os.WriteFile(filepath.Join(root, "small.go"), []byte("package small"), 0o644)).To(Succeed())

		big := filepath.Join(root, "big.go")
		Expect(os.WriteFile(big, nil, 0o644)).To(Succeed())
		Expect(os.Truncate(big, LexicalMaxFileBytes+1)).To(Succeed()) // sparse: nothing is written

		text, err := readLexicalText(root, "small.go")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(text)).To(Equal("package small"))

		text, err = readLexicalText(root, "big.go")
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(BeNil(), "a file over LexicalMaxFileBytes must not be read")

		text, err = readLexicalText(root, "gone.go")
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(BeNil(), "a missing file indexes by path only")
	})
})
