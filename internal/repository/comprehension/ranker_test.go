package comprehension

import (
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("detect intent maps common phrasings", func() {
	cases := []struct {
		query string
		want  Intent
	}{
		{"Where is auth handled?", IntentNavigate},
		{"why is the login flow broken on Safari?", IntentDebug},
		{"refactor the runner to extract the budget logic", IntentRefactor},
		{"audit this repo for one bug", IntentReview},
		{"add unit test coverage for the planner", IntentTest},
		{"hook this up to the metrics pipeline", IntentIntegrate},
		{"explain how the route classifier feeds the runtime brief", IntentExplain},
		{"list packages that use the artifact decorator", IntentSearch},
	}
	for _, tc := range cases {
		got := DetectIntent(tc.query)
		Expect(got).To(BeEquivalentTo(tc.want), "DetectIntent(%q) = %q, want %q", tc.query, got, tc.want)
	}
})
var _ = It("path penalty hard zeros vendored paths", func() {
	w := DefaultRankerWeights()
	{
		got := PathPenalty("node_modules/foo/index.js", w)
		Expect(got).To(BeEquivalentTo(0), "vendored path penalty = %v, want 0", got)
	}
	{
		got := PathPenalty("internal/runtime/agent/runner.go", w)
		Expect(got).To(BeEquivalentTo(1.0), "plain path penalty = %v, want 1.0", got)
	}
	{
		got := PathPenalty("docs/architect-packets.md", w)
		Expect(got).To(BeEquivalentTo(w.PenaltyDocs), "docs penalty = %v, want %v", got, w.PenaltyDocs)
	}
	{
		got := PathPenalty("internal/evidence/eval/swe_suite_test.go", w)
		Expect(got).To(BeEquivalentTo(w.PenaltyTest), "test penalty = %v, want %v", got, w.PenaltyTest)
	}
	{
		got := PathPenalty("dist/bundle.js", w)
		Expect(got).To(BeEquivalentTo(w.PenaltyGenerated), "generated penalty = %v, want %v", got, w.PenaltyGenerated)
	}
})
var _ = It("intent test relaxes test path penalty", func() {
	w := WeightsForIntent(IntentTest)
	got := PathPenalty("internal/evidence/eval/swe_suite_test.go", w)
	Expect(got).To(BeEquivalentTo(1.0), "test-intent test path penalty = %v, want 1.0 (no demotion)", got)
})
var _ = It("is name hub flags conventional utility files", func() {
	Expect(IsNameHub("src/utils.ts")).To(BeTrue(), "utils.ts should be a name hub")
	Expect(IsNameHub("packages/core/src/index.ts")).To(BeTrue(), "index.ts should be a name hub")
	Expect(IsNameHub("internal/runtime/agent/runner.go")).To(BeFalse(), "runner.go should not be a name hub")
})
var _ = It("build file graph projects symbol edges onto files", func() {
	g := &Graph{
		Nodes: []Node{
			{ID: "n1", Path: "a.go"},
			{ID: "n2", Path: "b.go"},
			{ID: "n3", Path: "c.go"},
			{ID: "n4", Path: "a.go"}, // second symbol in same file
		},
		Edges: []Edge{
			{SourceID: "n1", TargetID: "n2"},
			{SourceID: "n4", TargetID: "n3"},
			{SourceID: "n1", TargetID: "n4"}, // same file - dropped
		},
	}
	fg := BuildFileGraph(g)
	Expect(len(fg.Forward["a.go"])).To(BeEquivalentTo(2), "a.go forward = %v, want b.go and c.go", fg.Forward["a.go"])
	{
		_, ok := fg.Reverse["b.go"]["a.go"]
		Expect(ok).To(BeTrue(), "reverse edge a.go -> b.go missing")
	}
	{
		_, ok := fg.Forward["a.go"]["a.go"]
		Expect(ok).To(BeFalse(), "self-loop should have been dropped")
	}
})
var _ = It("fanout hubs thresholds at fraction", func() {
	fg := FileGraph{
		Files:   map[string]struct{}{"a": {}, "b": {}, "c": {}, "d": {}, "e": {}, "f": {}, "g": {}, "h": {}, "i": {}, "j": {}},
		Reverse: map[string]map[string]struct{}{},
	}
	// "hub" is imported by 5 of 10 files (50%) -> hub
	fg.Reverse["hub"] = map[string]struct{}{"a": {}, "b": {}, "c": {}, "d": {}, "e": {}}
	// "minor" only by 1 -> not hub
	fg.Reverse["minor"] = map[string]struct{}{"a": {}}

	hubs := FanoutHubs(fg, 0.2)
	{
		_, ok := hubs["hub"]
		Expect(ok).To(BeTrue(), "hub missing from fanout result: %v", hubs)
	}
	{
		_, ok := hubs["minor"]
		Expect(ok).To(BeFalse(), "minor should not be flagged as hub")
	}
})
var _ = It("hop boost prefers one hop over two hop", func() {
	fg := FileGraph{
		Forward: map[string]map[string]struct{}{
			"caller.go":    {"helper.go": {}},
			"helper.go":    {"target.go": {}},
			"distant.go":   {"caller.go": {}},
			"unrelated.go": {"island.go": {}},
		},
		Reverse: map[string]map[string]struct{}{
			"helper.go": {"caller.go": {}},
			"target.go": {"helper.go": {}},
			"caller.go": {"distant.go": {}},
			"island.go": {"unrelated.go": {}},
		},
	}
	matched := map[string]struct{}{"target.go": {}}
	w := DefaultRankerWeights()
	{
		got := HopBoost("helper.go", fg, matched, w)
		Expect(got).To(BeEquivalentTo(w.Hop1Boost), "helper.go boost = %v, want %v", got, w.Hop1Boost)
	}
	{
		got := HopBoost("caller.go", fg, matched, w)
		Expect(got).To(BeEquivalentTo(w.Hop2Boost), "caller.go boost = %v, want %v", got, w.Hop2Boost)
	}
	{
		got := HopBoost("unrelated.go", fg, matched, w)
		Expect(got).To(BeEquivalentTo(0), "unrelated.go boost = %v, want 0", got)
	}
	{
		got := HopBoost("target.go", fg, matched, w)
		Expect(got).To(BeEquivalentTo(0), "matched file should not boost itself, got %v", got)
	}
})
var _ = It("rank pack files applies all signals", func() {
	graph := &Graph{
		Nodes: []Node{
			{ID: "core", Path: "internal/runtime/agent/runner.go"},
			{ID: "helper", Path: "internal/helper.go"},
			{ID: "vendored", Path: "node_modules/lib/index.js"},
			{ID: "doc", Path: "docs/runner.md"},
		},
		Edges: []Edge{
			{SourceID: "core", TargetID: "helper"},
		},
	}
	matched := map[string]struct{}{"core": {}}
	files := []PackFile{
		{Path: "internal/runtime/agent/runner.go", Role: "editable"},
		{Path: "internal/helper.go", Role: "editable"},
		{Path: "node_modules/lib/index.js", Role: "read_only_evidence"},
		{Path: "docs/runner.md", Role: "read_only_evidence"},
	}
	ranked := RankPackFiles(RankInputs{
		Files:       files,
		Graph:       graph,
		MatchedIDs:  matched,
		Intent:      IntentDebug,
		QueryTokens: []string{"runner"},
	})
	Expect(ranked[0].Path).To(BeEquivalentTo("internal/runtime/agent/runner.go"), "expected runner.go top-ranked, got %v", ranked[0])
	Expect(ranked[1].Path).To(BeEquivalentTo("internal/helper.go"), "expected helper.go second (1-hop neighbor), got %v", ranked[1])

	// Vendored file must have a zero score so it cannot be surfaced.
	for _, file := range ranked {
		if file.Path == "node_modules/lib/index.js" {
			Expect(file.Score).To(BeZero(), "vendored file score = %v, want 0", file.Score)
		}
	}
})
var _ = It("rank pack files applies learned weights", func() {
	files := []PackFile{
		{Path: "a.go", Role: "editable"},
		{Path: "b.go", Role: "editable"},
	}
	ranked := RankPackFiles(RankInputs{
		Files:          files,
		Graph:          &Graph{Nodes: []Node{{ID: "1", Path: "a.go"}, {ID: "2", Path: "b.go"}}},
		Intent:         IntentSearch,
		LearnedWeights: map[string]float64{"b.go": 2.5},
	})
	Expect(ranked[0].Path).To(BeEquivalentTo("b.go"), "expected boosted b.go on top, got %v", ranked[0])
	Expect(math.Abs(ranked[0].Score-ranked[1].Score*2.5) > 0.01).To(BeFalse(), "learned multiplier did not scale score: %v vs %v", ranked[0].Score, ranked[1].Score)
})
