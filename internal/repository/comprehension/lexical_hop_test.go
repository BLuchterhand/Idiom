package comprehension

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func withPolicies(policies map[string]languagePolicy, def languagePolicy) {
	savedPolicies, savedDefault := languagePolicies, defaultPolicy
	languagePolicies, defaultPolicy = policies, def

	DeferCleanup(func() { languagePolicies, defaultPolicy = savedPolicies, savedDefault })
}

func testHopGraph() hopGraph {
	return hopGraph{
		"a.go":  {"b.go": hopCalls},
		"b.go":  {"a.go": hopCalls},
		"x.cpp": {"x.h": hopPairs, "y.cpp": hopImports},
		"x.h":   {"x.cpp": hopPairs},
		"y.cpp": {"x.cpp": hopImports},
	}
}

func hopPaths(hits []LexicalHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Path)
	}

	return out
}

func always(string) bool { return true }

var _ = Describe("lexical hop expansion", func() {
	It("follows the allowed kinds of the seed language", func() {
		withPolicies(map[string]languagePolicy{
			"go":  {Hop: hopCalls},
			"cpp": {Hop: hopPairs},
		}, languagePolicy{})

		lang := func(p string) string {
			if p == "a.go" {
				return "go"
			}

			return "cpp"
		}

		got := HopLexical([]LexicalHit{{Path: "a.go", Score: 4}, {Path: "x.cpp", Score: 2}}, testHopGraph(), lang, always)

		Expect(hopPaths(got)).To(Equal([]string{"a.go", "b.go", "x.cpp", "x.h"}), "calls from go, pairs only from cpp: y.cpp is an import")

		for _, h := range got {
			if h.Path == "b.go" {
				Expect(h.Via).To(Equal("a.go"), "b.go = %+v", h)
				Expect(h.Score).To(Equal(0.5), "b.go at alpha 0.5 of the normalized seed: %+v", h)
			}
		}
	})

	It("is off by default and respects the scope", func() {
		withPolicies(map[string]languagePolicy{"go": {Hop: hopNone}}, languagePolicy{})

		hits := []LexicalHit{{Path: "a.go", Score: 4}}
		got := HopLexical(hits, testHopGraph(), func(string) string { return "go" }, always)
		Expect(got).To(HaveLen(1), "a language with hop off must not expand, got %v", hopPaths(got))

		withPolicies(map[string]languagePolicy{"go": {Hop: hopAll}}, languagePolicy{})

		got = HopLexical(hits, testHopGraph(), func(string) string { return "go" }, func(p string) bool { return p != "b.go" })
		Expect(got).To(HaveLen(1), "a neighbour outside the scope must not be pulled in, got %v", hopPaths(got))
	})

	It("never lets a neighbour outrank its seed", func() {
		withPolicies(map[string]languagePolicy{"go": {Hop: hopAll}}, languagePolicy{})

		got := HopLexical([]LexicalHit{{Path: "a.go", Score: 4}, {Path: "b.go", Score: 4}}, testHopGraph(), func(string) string { return "go" }, always)

		// b.go is its own seed: max(own, own+0.5*seed) lifts both equally, ties by path.
		Expect(got).To(HaveLen(2))
		Expect(got[0].Path).To(Equal("a.go"), "order = %v", hopPaths(got))
		Expect(got[1].Path).To(Equal("b.go"), "order = %v", hopPaths(got))
	})

	It("filters the graph list by channel policy", func() {
		withPolicies(map[string]languagePolicy{"cpp": {Channel: false}}, languagePolicy{Channel: true})

		ranked := []PackFile{{Path: "a.go"}, {Path: "x.cpp"}, {Path: "b.go"}}
		lang := func(p string) string {
			if p == "x.cpp" {
				return "cpp"
			}

			return "go"
		}

		Expect(hopPaths(graphRankedHits(ranked, always, lang))).To(Equal([]string{"a.go", "b.go"}), "cpp must be excluded")
	})

	It("links edges and header pairs when building the hop graph", func() {
		nodes := []Node{
			{ID: "f1", Kind: "file", Path: "core/x.cpp"}, {ID: "f2", Kind: "file", Path: "core/x.h"},
			{ID: "s1", Kind: "func", Path: "core/x.cpp"}, {ID: "s2", Kind: "func", Path: "core/z.cpp"},
		}
		byID := map[string]Node{}

		for _, n := range nodes {
			byID[n.ID] = n
		}

		hg := buildHopGraph(Graph{Nodes: nodes, Edges: []Edge{{Kind: "calls", SourceID: "s1", TargetID: "s2"}, {Kind: "defines", SourceID: "f1", TargetID: "s1"}}}, byID)

		Expect(hg["core/x.cpp"]["core/z.cpp"]).To(Equal(hopCalls), "call edge must link both ways: %v", hg)
		Expect(hg["core/z.cpp"]["core/x.cpp"]).To(Equal(hopCalls), "call edge must link both ways: %v", hg)
		Expect(hg["core/x.cpp"]["core/x.h"]).To(Equal(hopPairs), "x.cpp and x.h share a stem: %v", hg)
		Expect(hg["core/x.h"]).To(HaveLen(1), "a defines edge is not a hop relation: %v", hg)
	})

	// The shipped table is the dev-split selection; changing it needs a new
	// dev comparison, so the test pins it.
	It("pins the shipped language policies", func() {
		goPolicy := policyFor("go")
		Expect(goPolicy.Hop).To(Equal(hopCalls))
		Expect(goPolicy.Channel).To(BeTrue())

		cppPolicy := policyFor("c++")
		Expect(cppPolicy.Hop).To(Equal(hopAll))
		Expect(cppPolicy.Channel).To(BeFalse())

		defaultLangPolicy := policyFor("shell")
		Expect(defaultLangPolicy.Hop).To(Equal(hopNone))
		Expect(defaultLangPolicy.Channel).To(BeFalse())
	})
})
