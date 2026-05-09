package comprehension

import (
	"math"
	"testing"
)

func TestDetectIntentMapsCommonPhrasings(t *testing.T) {
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
		if got != tc.want {
			t.Fatalf("DetectIntent(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestPathPenaltyHardZerosVendoredPaths(t *testing.T) {
	w := DefaultRankerWeights()
	if got := PathPenalty("node_modules/foo/index.js", w); got != 0 {
		t.Fatalf("vendored path penalty = %v, want 0", got)
	}
	if got := PathPenalty("internal/agent/runner.go", w); got != 1.0 {
		t.Fatalf("plain path penalty = %v, want 1.0", got)
	}
	if got := PathPenalty("docs/architect-packets.md", w); got != w.PenaltyDocs {
		t.Fatalf("docs penalty = %v, want %v", got, w.PenaltyDocs)
	}
	if got := PathPenalty("internal/eval/swe_suite_test.go", w); got != w.PenaltyTest {
		t.Fatalf("test penalty = %v, want %v", got, w.PenaltyTest)
	}
	if got := PathPenalty("dist/bundle.js", w); got != w.PenaltyGenerated {
		t.Fatalf("generated penalty = %v, want %v", got, w.PenaltyGenerated)
	}
}

func TestIntentTestRelaxesTestPathPenalty(t *testing.T) {
	w := WeightsForIntent(IntentTest)
	got := PathPenalty("internal/eval/swe_suite_test.go", w)
	if got != 1.0 {
		t.Fatalf("test-intent test path penalty = %v, want 1.0 (no demotion)", got)
	}
}

func TestIsNameHubFlagsConventionalUtilityFiles(t *testing.T) {
	if !IsNameHub("src/utils.ts") {
		t.Fatalf("utils.ts should be a name hub")
	}
	if !IsNameHub("packages/core/src/index.ts") {
		t.Fatalf("index.ts should be a name hub")
	}
	if IsNameHub("internal/agent/runner.go") {
		t.Fatalf("runner.go should not be a name hub")
	}
}

func TestBuildFileGraphProjectsSymbolEdgesOntoFiles(t *testing.T) {
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
	if len(fg.Forward["a.go"]) != 2 {
		t.Fatalf("a.go forward = %v, want b.go and c.go", fg.Forward["a.go"])
	}
	if _, ok := fg.Reverse["b.go"]["a.go"]; !ok {
		t.Fatalf("reverse edge a.go -> b.go missing")
	}
	if _, ok := fg.Forward["a.go"]["a.go"]; ok {
		t.Fatalf("self-loop should have been dropped")
	}
}

func TestFanoutHubsThresholdsAtFraction(t *testing.T) {
	fg := FileGraph{
		Files:   map[string]struct{}{"a": {}, "b": {}, "c": {}, "d": {}, "e": {}, "f": {}, "g": {}, "h": {}, "i": {}, "j": {}},
		Reverse: map[string]map[string]struct{}{},
	}
	// "hub" is imported by 5 of 10 files (50%) -> hub
	fg.Reverse["hub"] = map[string]struct{}{"a": {}, "b": {}, "c": {}, "d": {}, "e": {}}
	// "minor" only by 1 -> not hub
	fg.Reverse["minor"] = map[string]struct{}{"a": {}}
	hubs := FanoutHubs(fg, 0.2)
	if _, ok := hubs["hub"]; !ok {
		t.Fatalf("hub missing from fanout result: %v", hubs)
	}
	if _, ok := hubs["minor"]; ok {
		t.Fatalf("minor should not be flagged as hub")
	}
}

func TestHopBoostPrefersOneHopOverTwoHop(t *testing.T) {
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

	if got := HopBoost("helper.go", fg, matched, w); got != w.Hop1Boost {
		t.Fatalf("helper.go boost = %v, want %v", got, w.Hop1Boost)
	}
	if got := HopBoost("caller.go", fg, matched, w); got != w.Hop2Boost {
		t.Fatalf("caller.go boost = %v, want %v", got, w.Hop2Boost)
	}
	if got := HopBoost("unrelated.go", fg, matched, w); got != 0 {
		t.Fatalf("unrelated.go boost = %v, want 0", got)
	}
	if got := HopBoost("target.go", fg, matched, w); got != 0 {
		t.Fatalf("matched file should not boost itself, got %v", got)
	}
}

func TestRankPackFilesAppliesAllSignals(t *testing.T) {
	graph := &Graph{
		Nodes: []Node{
			{ID: "core", Path: "internal/runner.go"},
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
		{Path: "internal/runner.go", Role: "editable"},
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
	if ranked[0].Path != "internal/runner.go" {
		t.Fatalf("expected runner.go top-ranked, got %v", ranked[0])
	}
	if ranked[1].Path != "internal/helper.go" {
		t.Fatalf("expected helper.go second (1-hop neighbor), got %v", ranked[1])
	}
	// Vendored file must have a zero score so it cannot be surfaced.
	for _, file := range ranked {
		if file.Path == "node_modules/lib/index.js" && file.Score != 0 {
			t.Fatalf("vendored file score = %v, want 0", file.Score)
		}
	}
}

func TestRankPackFilesAppliesLearnedWeights(t *testing.T) {
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
	if ranked[0].Path != "b.go" {
		t.Fatalf("expected boosted b.go on top, got %v", ranked[0])
	}
	if math.Abs(ranked[0].Score-ranked[1].Score*2.5) > 0.01 {
		t.Fatalf("learned multiplier did not scale score: %v vs %v", ranked[0].Score, ranked[1].Score)
	}
}
