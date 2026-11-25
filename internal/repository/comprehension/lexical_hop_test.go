package comprehension

import (
	"slices"
	"testing"
)

func withPolicies(t *testing.T, policies map[string]languagePolicy, def languagePolicy) {
	t.Helper()

	savedPolicies, savedDefault := languagePolicies, defaultPolicy
	languagePolicies, defaultPolicy = policies, def

	t.Cleanup(func() { languagePolicies, defaultPolicy = savedPolicies, savedDefault })
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

func TestHopLexicalFollowsAllowedKindsOfTheSeedLanguage(t *testing.T) {
	withPolicies(t, map[string]languagePolicy{
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

	if want := []string{"a.go", "b.go", "x.cpp", "x.h"}; !slices.Equal(hopPaths(got), want) {
		t.Fatalf("hop order = %v, want %v (calls from go, pairs only from cpp: y.cpp is an import)", hopPaths(got), want)
	}

	for _, h := range got {
		if h.Path == "b.go" && (h.Via != "a.go" || h.Score != 0.5) {
			t.Fatalf("b.go = %+v, want via a.go at alpha 0.5 of the normalized seed", h)
		}
	}
}

func TestHopLexicalOffByDefaultAndScoped(t *testing.T) {
	withPolicies(t, map[string]languagePolicy{"go": {Hop: hopNone}}, languagePolicy{})

	hits := []LexicalHit{{Path: "a.go", Score: 4}}
	if got := HopLexical(hits, testHopGraph(), func(string) string { return "go" }, always); len(got) != 1 {
		t.Fatalf("a language with hop off must not expand, got %v", hopPaths(got))
	}

	withPolicies(t, map[string]languagePolicy{"go": {Hop: hopAll}}, languagePolicy{})

	got := HopLexical(hits, testHopGraph(), func(string) string { return "go" }, func(p string) bool { return p != "b.go" })
	if len(got) != 1 {
		t.Fatalf("a neighbour outside the scope must not be pulled in, got %v", hopPaths(got))
	}
}

func TestHopNeighbourNeverOutranksItsSeed(t *testing.T) {
	withPolicies(t, map[string]languagePolicy{"go": {Hop: hopAll}}, languagePolicy{})

	got := HopLexical([]LexicalHit{{Path: "a.go", Score: 4}, {Path: "b.go", Score: 4}}, testHopGraph(), func(string) string { return "go" }, always)
	if got[0].Path != "a.go" || got[1].Path != "b.go" {
		// b.go is its own seed: max(own, own+0.5*seed) lifts both equally, ties by path.
		t.Fatalf("order = %v", hopPaths(got))
	}
}

func TestChannelPolicyFiltersGraphList(t *testing.T) {
	withPolicies(t, map[string]languagePolicy{"cpp": {Channel: false}}, languagePolicy{Channel: true})

	ranked := []PackFile{{Path: "a.go"}, {Path: "x.cpp"}, {Path: "b.go"}}
	lang := func(p string) string {
		if p == "x.cpp" {
			return "cpp"
		}

		return "go"
	}

	if got := hopPaths(graphRankedHits(ranked, always, lang)); !slices.Equal(got, []string{"a.go", "b.go"}) {
		t.Fatalf("channel list = %v, cpp must be excluded", got)
	}
}

func TestBuildHopGraphLinksEdgesAndHeaderPairs(t *testing.T) {
	nodes := []Node{
		{ID: "f1", Kind: "file", Path: "core/x.cpp"}, {ID: "f2", Kind: "file", Path: "core/x.h"},
		{ID: "s1", Kind: "func", Path: "core/x.cpp"}, {ID: "s2", Kind: "func", Path: "core/z.cpp"},
	}
	byID := map[string]Node{}

	for _, n := range nodes {
		byID[n.ID] = n
	}

	hg := buildHopGraph(Graph{Nodes: nodes, Edges: []Edge{{Kind: "calls", SourceID: "s1", TargetID: "s2"}, {Kind: "defines", SourceID: "f1", TargetID: "s1"}}}, byID)

	if hg["core/x.cpp"]["core/z.cpp"] != hopCalls || hg["core/z.cpp"]["core/x.cpp"] != hopCalls {
		t.Fatalf("call edge must link both ways: %v", hg)
	}

	if hg["core/x.cpp"]["core/x.h"] != hopPairs {
		t.Fatalf("x.cpp and x.h share a stem: %v", hg)
	}

	if len(hg["core/x.h"]) != 1 {
		t.Fatalf("a defines edge is not a hop relation: %v", hg)
	}
}

// The shipped table is the dev-split selection; changing it needs a new
// dev comparison, so the test pins it.
func TestShippedLanguagePolicies(t *testing.T) {
	if p := policyFor("go"); p.Hop != hopCalls || !p.Channel {
		t.Fatalf("go policy = %+v", p)
	}

	if p := policyFor("c++"); p.Hop != hopAll || p.Channel {
		t.Fatalf("cpp policy = %+v", p)
	}

	if p := policyFor("shell"); p.Hop != hopNone || p.Channel {
		t.Fatalf("default policy = %+v", p)
	}
}
