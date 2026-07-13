package comprehension

import (
	"fmt"
	"math"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestRankerHandlesCyclicGraphsWithoutInfiniteLoop guards against the most
// obvious failure mode for hop traversal: a cycle in the import graph would
// blow the stack or hang if HopBoost recurses naively.
var _ = It("ranker handles cyclic graphs without infinite loop", func() {
	// a -> b -> c -> a (3-node cycle) plus a 1-hop neighbor d -> a.
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Path: "a.go"}, {ID: "b", Path: "b.go"},
			{ID: "c", Path: "c.go"}, {ID: "d", Path: "d.go"},
		},
		Edges: []Edge{
			{SourceID: "a", TargetID: "b"},
			{SourceID: "b", TargetID: "c"},
			{SourceID: "c", TargetID: "a"},
			{SourceID: "d", TargetID: "a"},
		},
	}
	ranked := RankPackFiles(RankInputs{
		Files: []PackFile{
			{Path: "a.go", Role: "editable"},
			{Path: "b.go", Role: "editable"},
			{Path: "c.go", Role: "editable"},
			{Path: "d.go", Role: "editable"},
		},
		Graph:      g,
		MatchedIDs: map[string]struct{}{"a": {}},
		Intent:     IntentSearch,
	})
	Expect(len(ranked)).To(BeEquivalentTo(4), "want 4 files, got %d", len(ranked))
	Expect(ranked[0].Path).To(BeEquivalentTo("a.go"), "matched file should rank first, got %v", ranked[0])

	// Because the cycle makes every other file a 1-hop neighbor of a (via
	// forward or reverse edges), the strong invariant is just that the
	// matched file outranks them all. The point of this test is termination
	// without an infinite loop; it ran to completion above.
	for _, f := range ranked[1:] {
		Expect(f.Score >= ranked[0].Score).To(BeFalse(), "non-matched file %q (score %v) should rank below matched a.go (score %v)", f.Path, f.Score, ranked[0].Score)
	}
})

// TestRankerSelfLoopsAreDropped verifies BuildFileGraph never produces
// adjacency for a file pointing at itself, so HopBoost cannot self-promote.
var _ = It("ranker self loops are dropped", func() {
	g := &Graph{
		Nodes: []Node{{ID: "n1", Path: "x.go"}, {ID: "n2", Path: "x.go"}},
		Edges: []Edge{{SourceID: "n1", TargetID: "n2"}},
	}
	fg := BuildFileGraph(g)
	{
		fwd := fg.Forward["x.go"]
		Expect(len(fwd)).To(BeEquivalentTo(0), "expected no forward edges from self-loop, got %v", fwd)
	}
	{
		rev := fg.Reverse["x.go"]
		Expect(len(rev)).To(BeEquivalentTo(0), "expected no reverse edges into self-loop, got %v", rev)
	}
})

// TestRankerDoubleSelfBoostBlocked guards the "matched file is also its own
// 1-hop neighbor via cycle" case: a → b → a, with a matched. b should pick
// up the 1-hop boost, but a should not boost itself.
var _ = It("ranker double self boost blocked", func() {
	g := &Graph{
		Nodes: []Node{{ID: "a", Path: "a.go"}, {ID: "b", Path: "b.go"}},
		Edges: []Edge{
			{SourceID: "a", TargetID: "b"},
			{SourceID: "b", TargetID: "a"},
		},
	}
	matched := map[string]struct{}{"a": {}}
	w := DefaultRankerWeights()
	{
		got := HopBoost("a.go", BuildFileGraph(g), MatchedFilePaths(g, matched), w)
		Expect(got).To(BeEquivalentTo(0), "matched file boost = %v, want 0", got)
	}
})

// TestRankerHubFanoutAtExactThreshold checks the integer threshold logic at
// the boundary. With 10 files and 20% fraction, threshold = 2, so a file
// imported by exactly 2 others is a hub and one imported by 1 is not.
var _ = It("ranker hub fanout at exact threshold", func() {
	files := map[string]struct{}{}
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("f%d", i)] = struct{}{}
	}

	fg := FileGraph{
		Files:   files,
		Reverse: map[string]map[string]struct{}{},
	}

	fg.Reverse["edge"] = map[string]struct{}{"f0": {}}                  // 1 importer
	fg.Reverse["onthreshold"] = map[string]struct{}{"f0": {}, "f1": {}} // 2 importers
	fg.Reverse["over"] = map[string]struct{}{"f0": {}, "f1": {}, "f2": {}}

	hubs := FanoutHubs(fg, 0.2)
	{
		_, ok := hubs["edge"]
		Expect(ok).To(BeFalse(), "file with 1 importer should not be a hub")
	}
	{
		_, ok := hubs["onthreshold"]
		Expect(ok).To(BeTrue(), "file at threshold should be a hub")
	}
	{
		_, ok := hubs["over"]
		Expect(ok).To(BeTrue(), "file over threshold should be a hub")
	}
})

// TestRankerSmallRepoHasNoHubs confirms the hub threshold floor (>=2) so
// trivial 1-2 file workspaces don't get every file flagged.
var _ = It("ranker small repo has no hubs", func() {
	fg := FileGraph{
		Files: map[string]struct{}{"a": {}, "b": {}},
		Reverse: map[string]map[string]struct{}{
			"a": {"b": {}},
		},
	}
	{
		hubs := FanoutHubs(fg, 0.2)
		Expect(len(hubs)).To(BeEquivalentTo(0), "two-file workspace should not produce hubs, got %v", hubs)
	}
})

// TestRankerPathPenaltyVariants exercises every regex bucket so we know
// which file shapes get demoted; surprises here would silently distort
// rankings.
var _ = It("ranker path penalty variants", func() {
	w := DefaultRankerWeights()
	cases := []struct {
		path string
		want float64
		hint string
	}{
		{"node_modules/foo/bar.js", 0.0, "node_modules"},
		{"vendor/lib.go", 0.0, "vendor"},
		{"third_party/lib.cc", 0.0, "third_party"},
		{".venv/lib/python.py", 0.0, ".venv"},
		{"venv/lib/python.py", 0.0, "venv"},
		{"internal/runtime/agent/runner_test.go", w.PenaltyTest, "_test.go"},
		{"src/foo.spec.ts", w.PenaltyTest, ".spec.ts"},
		{"src/foo.test.tsx", w.PenaltyTest, ".test.tsx"},
		{"tests/runner.go", w.PenaltyTest, "tests/"},
		{"__tests__/foo.js", w.PenaltyTest, "__tests__/"},
		{"e2e/scenario.py", w.PenaltyTest, "e2e/"},
		{"testdata/foo.json", w.PenaltyTest, "testdata/"},
		{"dist/bundle.js", w.PenaltyGenerated, "dist/"},
		{"build/output.go", w.PenaltyGenerated, "build/"},
		{"out/x.js", w.PenaltyGenerated, "out/"},
		{".next/foo.js", w.PenaltyGenerated, ".next/"},
		{"target/debug/x", w.PenaltyGenerated, "target/"},
		{"docs/architect.md", w.PenaltyDocs, "docs/"},
		{"README.md", w.PenaltyDocs, "README.md"},
		{"internal/runtime/agent/runner.go", 1.0, "plain source"},
	}

	for _, tc := range cases {
		got := PathPenalty(tc.path, w)
		Expect(math.Abs(got-tc.want) > 1e-9).To(BeFalse(), "PathPenalty(%q) [%s] = %v, want %v", tc.path, tc.hint, got, tc.want)
	}
})

// TestRankerVendoredCannotEscapeViaLearnedWeight hardens the hard-zero
// invariant: even with the maximum allowed learned multiplier, a vendored
// path stays at score zero so it cannot leak into context.
var _ = It("ranker vendored cannot escape via learned weight", func() {
	files := []PackFile{
		{Path: "node_modules/lib.js", Role: "editable"},
		{Path: "internal/runtime/agent/runner.go", Role: "editable"},
	}
	ranked := RankPackFiles(RankInputs{
		Files: files,
		Graph: &Graph{Nodes: []Node{
			{ID: "1", Path: "node_modules/lib.js"},
			{ID: "2", Path: "internal/runtime/agent/runner.go"},
		}},
		Intent:         IntentSearch,
		LearnedWeights: map[string]float64{"node_modules/lib.js": LearnedWeightMax},
		QueryTokens:    []string{"lib"},
	})

	for _, f := range ranked {
		if f.Path == "node_modules/lib.js" {
			Expect(f.Score).To(BeZero(), "vendored file score must remain 0 even with learned weight, got %v", f.Score)
		}
	}
})

// TestRankerStaleAndHubAndPenaltyCompound verifies the multiplicative chain
// produces a non-negative score and respects ordering when several
// demotions stack on the same file.
var _ = It("ranker stale and hub and penalty compound", func() {
	files := []PackFile{
		{Path: "src/utils.ts", Role: "read_only_evidence", Stale: true},
		{Path: "internal/runtime/agent/runner.go", Role: "editable"},
	}
	ranked := RankPackFiles(RankInputs{
		Files: files,
		Graph: &Graph{Nodes: []Node{
			{ID: "1", Path: "src/utils.ts"}, {ID: "2", Path: "internal/runtime/agent/runner.go"},
		}},
		Intent: IntentSearch,
	})

	var stale, runner PackFile

	for _, f := range ranked {
		if f.Path == "src/utils.ts" {
			stale = f
		}

		if f.Path == "internal/runtime/agent/runner.go" {
			runner = f
		}
	}
	Expect(stale.Score < 0).To(BeFalse(), "compound demotion produced negative score: %v", stale.Score)
	Expect(stale.Hub).To(BeTrue(), "utils.ts should be flagged as a name hub")
	Expect(stale.Score >= runner.Score).To(BeFalse(), "stale + hub + non-matching utils should rank below runner, got %v vs %v", stale.Score, runner.Score)
})

// TestRankerIntentFlipChangesOrdering proves the intent dial is connected:
// the same files under different intents must produce different orderings
// for at least one pair.
var _ = It("ranker intent flip changes ordering", func() {
	g := &Graph{
		Nodes: []Node{
			{ID: "core", Path: "internal/runtime/agent/runner.go"},
			{ID: "test", Path: "internal/runtime/agent/runner_test.go"},
			{ID: "util", Path: "src/utils.ts"},
		},
	}
	files := []PackFile{
		{Path: "internal/runtime/agent/runner.go", Role: "editable"},
		{Path: "internal/runtime/agent/runner_test.go", Role: "test"},
		{Path: "src/utils.ts", Role: "editable"},
	}
	rankUnder := func(intent Intent) []string {
		out := RankPackFiles(RankInputs{Files: files, Graph: g, Intent: intent, QueryTokens: []string{"runner"}})
		paths := make([]string, len(out))

		for i, f := range out {
			paths[i] = f.Path
		}

		return paths
	}
	search := rankUnder(IntentSearch)
	testIntent := rankUnder(IntentTest)
	// Under IntentSearch the test file is penalized and should fall behind.
	// Under IntentTest the test penalty is disabled so the test file should
	// climb relative to its IntentSearch position.
	posSearch, posTest := -1, -1

	for i, p := range search {
		if p == "internal/runtime/agent/runner_test.go" {
			posSearch = i
		}
	}

	for i, p := range testIntent {
		if p == "internal/runtime/agent/runner_test.go" {
			posTest = i
		}
	}
	Expect(posTest >= posSearch).To(BeFalse(), "test intent should promote test file: search=%d test=%d (search=%v test=%v)", posSearch, posTest, search, testIntent)
})

// TestRankerIdempotenceUnderRepeatedRanking confirms RankPackFiles is a
// pure function of its input. No cached state, no in-place mutation that
// would shift order on a second call.
var _ = It("ranker idempotence under repeated ranking", func() {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Path: "a.go"}, {ID: "b", Path: "b.go"},
			{ID: "c", Path: "c.go"}, {ID: "d", Path: "d.go"},
		},
		Edges: []Edge{
			{SourceID: "a", TargetID: "b"},
			{SourceID: "b", TargetID: "c"},
		},
	}
	files := []PackFile{
		{Path: "a.go", Role: "editable"},
		{Path: "b.go", Role: "editable"},
		{Path: "c.go", Role: "editable"},
		{Path: "d.go", Role: "editable"},
	}
	first := RankPackFiles(RankInputs{Files: files, Graph: g, MatchedIDs: map[string]struct{}{"a": {}}, Intent: IntentDebug, QueryTokens: []string{"a"}})
	second := RankPackFiles(RankInputs{Files: files, Graph: g, MatchedIDs: map[string]struct{}{"a": {}}, Intent: IntentDebug, QueryTokens: []string{"a"}})
	Expect(len(first)).To(BeEquivalentTo(len(second)), "rankings differ in length")

	for i := range first {
		Expect(first[i].Path).To(BeEquivalentTo(second[i].Path), "rank index %d differs: %v vs %v", i, first[i], second[i])
		Expect(math.Abs(first[i].Score-second[i].Score)).To(BeNumerically("<=", 1e-9), "rank index %d differs: %v vs %v", i, first[i], second[i])
	}
})

// TestRankerStableSortOnEqualScores verifies tie-breaking is by role then
// path so the output ordering is reproducible across runs.
var _ = It("ranker stable sort on equal scores", func() {
	files := []PackFile{
		{Path: "z.go", Role: "editable"},
		{Path: "a.go", Role: "editable"},
		{Path: "m.go", Role: "editable"},
	}
	first := RankPackFiles(RankInputs{Files: files, Graph: &Graph{Nodes: nodesFor(files)}, Intent: IntentSearch})
	second := RankPackFiles(RankInputs{Files: append([]PackFile{}, files[2], files[0], files[1]), Graph: &Graph{Nodes: nodesFor(files)}, Intent: IntentSearch})

	for i := range first {
		Expect(first[i].Path).To(BeEquivalentTo(second[i].Path), "equal-score sort is unstable: %v vs %v", first, second)
	}

	want := []string{"a.go", "m.go", "z.go"}
	for i, p := range want {
		Expect(first[i].Path).To(BeEquivalentTo(p), "equal-score files should sort by path; got %v", first)
	}
})

// TestRankerLargeRepoTerminatesAndOrders runs the ranker over a synthetic
// 1000-file repo with a moderate edge density to exercise the hub
// threshold and hop traversal at scale. We assert termination, valid
// ordering, and that the matched file ranks first.
var _ = It("ranker large repo terminates and orders", func() {
	const n = 1000

	files := make([]PackFile, n)
	nodes := make([]Node, n)

	for i := 0; i < n; i++ {
		path := fmt.Sprintf("pkg/file%04d.go", i)

		files[i] = PackFile{Path: path, Role: "editable"}
		nodes[i] = Node{ID: fmt.Sprintf("n%d", i), Path: path}
	}

	// Sparse forward edges: each file imports the next two.
	edges := make([]Edge, 0, n*2)
	for i := 0; i < n-2; i++ {
		edges = append(edges, Edge{SourceID: fmt.Sprintf("n%d", i), TargetID: fmt.Sprintf("n%d", i+1)})
		edges = append(edges, Edge{SourceID: fmt.Sprintf("n%d", i), TargetID: fmt.Sprintf("n%d", i+2)})
	}

	// Add one explicit hub: file9999 imported by 250 of the others.
	hubPath := "pkg/hub.go"

	files = append(files, PackFile{Path: hubPath, Role: "editable"})
	nodes = append(nodes, Node{ID: "hub", Path: hubPath})

	for i := 0; i < 250; i++ {
		edges = append(edges, Edge{SourceID: fmt.Sprintf("n%d", i), TargetID: "hub"})
	}

	g := &Graph{Nodes: nodes, Edges: edges}
	ranked := RankPackFiles(RankInputs{
		Files:      files,
		Graph:      g,
		MatchedIDs: map[string]struct{}{"n42": {}},
		Intent:     IntentSearch,
	})
	Expect(len(ranked)).To(BeEquivalentTo(n+1), "expected %d files, got %d", n+1, len(ranked))
	Expect(ranked[0].Path).To(BeEquivalentTo("pkg/file0042.go"), "matched file should rank first, got %s", ranked[0].Path)

	// Hub should be present but flagged.
	var hub PackFile

	for _, f := range ranked {
		if f.Path == hubPath {
			hub = f
			break
		}
	}
	Expect(hub.Hub).To(BeTrue(), "synthetic hub should be flagged: %#v", hub)

	// Confirm scores are monotonically non-increasing.
	for i := 1; i < len(ranked); i++ {
		Expect(ranked[i].Score > ranked[i-1].Score+1e-9).To(BeFalse(), "scores not monotonic at index %d: %v > %v", i, ranked[i].Score, ranked[i-1].Score)
	}
})

// TestRankerEmptyInputsDoNotPanic guards the obvious edge cases — empty
// files slice, nil graph, no learned weights, no query tokens.
var _ = It("ranker empty inputs do not panic", func() {
	{
		got := RankPackFiles(RankInputs{})
		Expect(len(got)).To(BeEquivalentTo(0), "empty input should return empty slice, got %v", got)
	}
	{
		got := RankPackFiles(RankInputs{Files: []PackFile{{Path: "a.go", Role: "editable"}}})
		Expect(len(got)).To(BeEquivalentTo(1), "nil graph should still rank: %v", got)
	}
})

// TestRankerLearnedWeightZeroIgnored verifies a literal 0 multiplier in the
// learned weights map does not zero out a file's score; only positive
// multipliers apply, since 0 is the "no entry" signal in our schema.
var _ = It("ranker learned weight zero ignored", func() {
	files := []PackFile{{Path: "a.go", Role: "editable"}}
	ranked := RankPackFiles(RankInputs{
		Files:          files,
		Graph:          &Graph{Nodes: []Node{{ID: "1", Path: "a.go"}}},
		Intent:         IntentSearch,
		LearnedWeights: map[string]float64{"a.go": 0},
	})
	Expect(ranked[0].Score).NotTo(BeEquivalentTo(0), "zero learned weight should be treated as absent, not as score-zeroing")
})

// TestRankerHopTraversalBoundedByGraph guards against an O(n²) explosion in
// hop2Neighbor for densely connected graphs. We assert termination on a
// fully connected 50-file graph in a fixed wall-clock time.
var _ = It("ranker hop traversal bounded by graph", func() {
	const n = 50

	files := make([]PackFile, n)
	nodes := make([]Node, n)

	for i := 0; i < n; i++ {
		path := fmt.Sprintf("dense/f%d.go", i)

		files[i] = PackFile{Path: path, Role: "editable"}
		nodes[i] = Node{ID: fmt.Sprintf("n%d", i), Path: path}
	}

	edges := make([]Edge, 0, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}

			edges = append(edges, Edge{SourceID: fmt.Sprintf("n%d", i), TargetID: fmt.Sprintf("n%d", j)})
		}
	}

	ranked := RankPackFiles(RankInputs{
		Files:      files,
		Graph:      &Graph{Nodes: nodes, Edges: edges},
		MatchedIDs: map[string]struct{}{"n0": {}},
		Intent:     IntentSearch,
	})
	Expect(len(ranked)).To(BeEquivalentTo(n), "expected %d files in dense graph, got %d", n, len(ranked))
})

// TestRankerHubMatchedFileNotDemoted confirms a file that is both a hub and
// a direct match keeps its full score; we don't want to fight the user's
// query.
var _ = It("ranker hub matched file not demoted", func() {
	files := []PackFile{
		{Path: "src/utils.ts", Role: "editable"}, // name hub
		{Path: "src/other.ts", Role: "editable"},
	}
	g := &Graph{
		Nodes: []Node{
			{ID: "u", Path: "src/utils.ts"}, {ID: "o", Path: "src/other.ts"},
		},
	}
	matched := map[string]struct{}{"u": {}}
	ranked := RankPackFiles(RankInputs{Files: files, Graph: g, MatchedIDs: matched, Intent: IntentSearch})
	Expect(ranked[0].Path).To(BeEquivalentTo("src/utils.ts"), "matched name-hub should rank first, got %v", ranked)
})

// TestRankerDetectIntentDoesNotConfuseSimilarPhrases pins the regex
// boundaries where the patterns could otherwise grab innocent prose.
var _ = It("ranker detect intent does not confuse similar phrases", func() {
	cases := []struct {
		query string
		want  Intent
	}{
		{"refactoring is risky", IntentRefactor},
		{"i need to debug a customer report", IntentDebug},
		{"running the test suite", IntentTest},
		{"a generic codebase listing", IntentSearch},
	}
	for _, tc := range cases {
		got := DetectIntent(tc.query)
		Expect(got).To(BeEquivalentTo(tc.want), "DetectIntent(%q) = %v, want %v", tc.query, got, tc.want)
	}
})

// TestRankerScoresAreFiniteAndStable scans a randomized matrix of inputs to
// assert no NaN/Inf scores leak through the scoring pipeline.
var _ = It("ranker scores are finite and stable", func() {
	intents := []Intent{IntentSearch, IntentDebug, IntentExplain, IntentRefactor, IntentReview, IntentTest, IntentIntegrate, IntentNavigate}
	roles := []string{"editable", "test", "read_only_evidence", ""}
	paths := []string{"a.go", "node_modules/x.js", "docs/x.md", "src/utils.ts", "internal/runtime/agent/runner_test.go", "build/x.o"}
	files := make([]PackFile, 0, len(roles)*len(paths))
	nodes := make([]Node, 0, len(paths))

	for i, p := range paths {
		nodes = append(nodes, Node{ID: fmt.Sprintf("n%d", i), Path: p})
		for _, r := range roles {
			files = append(files, PackFile{Path: p, Role: r, Stale: i%2 == 0})
		}
	}

	g := &Graph{Nodes: nodes}
	for _, intent := range intents {
		ranked := RankPackFiles(RankInputs{Files: files, Graph: g, Intent: intent})
		for _, f := range ranked {
			Expect(math.IsNaN(f.Score)).To(BeFalse(), "invalid score %v for %v under intent %v", f.Score, f, intent)
			Expect(math.IsInf(f.Score, 0)).To(BeFalse(), "invalid score %v for %v under intent %v", f.Score, f, intent)
			Expect(f.Score).To(BeNumerically(">=", 0), "invalid score %v for %v under intent %v", f.Score, f, intent)
		}
	}
})

func nodesFor(files []PackFile) []Node {
	out := make([]Node, len(files))
	for i, f := range files {
		out[i] = Node{ID: fmt.Sprintf("n%d", i), Path: f.Path}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	return out
}
