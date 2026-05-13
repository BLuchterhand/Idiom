package comprehension

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Intent classifies what the user is trying to do with the query so the ranker
// can shift weights between path, symbol, graph, and role signals.
type Intent string

const (
	IntentSearch    Intent = "search"
	IntentDebug     Intent = "debug"
	IntentExplain   Intent = "explain"
	IntentRefactor  Intent = "refactor"
	IntentReview    Intent = "review"
	IntentTest      Intent = "test"
	IntentIntegrate Intent = "integrate"
	IntentNavigate  Intent = "navigate"
)

// RankerWeights holds the additive and multiplicative coefficients applied
// when scoring a candidate file. All fields are positive; the role floor is
// the lower bound, signal bonuses add on top, and the demotion/penalty pair
// multiplies the result.
type RankerWeights struct {
	RoleEditable      float64
	RoleTest          float64
	RoleEvidence      float64
	RoleDefault       float64
	RequiredEditPath  float64 // additive bonus for caller-declared edit paths
	MatchBoost        float64 // additive bonus for files that matched task terms; keeps matches above their pure neighbors
	PathMatch         float64
	Hop1Boost         float64
	Hop2Boost         float64
	HubDemotion       float64 // multiplier applied to detected hub files (<1.0)
	StaleDemotion     float64 // multiplier applied to stale files (<1.0)
	PenaltyTest       float64
	PenaltyGenerated  float64
	PenaltyDocs       float64
	PenaltyVendored   float64 // hard floor for node_modules / vendor / .venv
	HubFanoutFraction float64 // share-of-files threshold for fanout hub detection
}

// DefaultRankerWeights returns sigmap-flavored defaults tuned to keep
// idiom's existing role-based ordering as the backbone while letting graph
// adjacency and path shape break ties and demote noise.
func DefaultRankerWeights() RankerWeights {
	return RankerWeights{
		RoleEditable:      3.0,
		RoleTest:          2.0,
		RoleEvidence:      1.5,
		RoleDefault:       0.5,
		RequiredEditPath:  4.0,
		MatchBoost:        1.2,
		PathMatch:         0.8,
		Hop1Boost:         0.4,
		Hop2Boost:         0.15,
		HubDemotion:       0.6,
		StaleDemotion:     0.85,
		PenaltyTest:       0.4,
		PenaltyGenerated:  0.3,
		PenaltyDocs:       0.2,
		PenaltyVendored:   0.0,
		HubFanoutFraction: 0.2,
	}
}

// WeightsForIntent applies intent-specific overrides on top of the default
// weights. The shifts mirror sigmap's intent table: explain leans on path and
// symbol matches, integrate leans on graph adjacency, test relaxes the test
// path penalty, and so on.
func WeightsForIntent(intent Intent) RankerWeights {
	w := DefaultRankerWeights()
	switch intent {
	case IntentDebug:
		w.RoleEditable = 3.4
		w.PathMatch = 0.6
	case IntentExplain:
		w.PathMatch = 1.0
		w.Hop1Boost = 0.55
		w.Hop2Boost = 0.2
	case IntentRefactor:
		w.RoleEditable = 3.6
		w.PathMatch = 0.6
	case IntentReview:
		w.PathMatch = 1.1
		w.RoleEvidence = 1.7
	case IntentTest:
		w.PenaltyTest = 1.0
		w.RoleTest = 2.6
	case IntentIntegrate:
		w.Hop1Boost = 0.7
		w.Hop2Boost = 0.3
		w.PathMatch = 1.1
	case IntentNavigate:
		w.PathMatch = 1.2
	}
	return w
}

var (
	// Verb stems allow common gerund/past-tense suffixes (refactor → refactoring,
	// debug → debugging) so the classifier doesn't miss the intent on the most
	// natural phrasings.
	intentDebugPattern     = regexp.MustCompile(`(?i)\b(debug(ging|ged|s)?|bug|broken|crash(ing|ed|es)?|panic|error|stack ?trace|fail(s|ed|ing|ure|ures)?|wrong|fix(es|ed|ing)?)\b`)
	intentExplainPattern   = regexp.MustCompile(`(?i)\b(explain(ing|ed|s)?|how does|what does|walk me|describ(e|ing|ed)|why does|why is)\b`)
	intentRefactorPattern  = regexp.MustCompile(`(?i)\b(refactor(ing|ed|s)?|renam(e|ing|ed)|extract(ing|ed|s)?|split(ting)?|simplif(y|ying|ied)|tid(y|ying)|clean ?up|restructur(e|ing|ed))\b`)
	intentReviewPattern    = regexp.MustCompile(`(?i)\b(review(ing|ed|s)?|audit(ing|ed|s)?|smell|find a bug|find one bug|critiqu(e|ing|ed)|lint(ing|ed)?)\b`)
	intentTestPattern      = regexp.MustCompile(`(?i)\b(test(s|ing|ed)?|spec(s)?|coverage|unit test|integration test|e2e)\b`)
	intentIntegratePattern = regexp.MustCompile(`(?i)\b(integrat(e|ing|ed)|hook (it|this) up|wire (it|this) up|connect(ing|ed|s)?|plumb(ing|ed|s)?)\b`)
	intentNavigatePattern  = regexp.MustCompile(`(?i)\b(where (is|are)|find (the|a) file|locat(e|ing|ed)|jump to|navigat(e|ing|ed))\b`)
)

// DetectIntent classifies a free-form query into the intent buckets the ranker
// knows how to bias for. Order matters: more specific patterns win first; the
// fall-through is search.
func DetectIntent(query string) Intent {
	q := strings.TrimSpace(query)
	if q == "" {
		return IntentSearch
	}
	switch {
	case intentReviewPattern.MatchString(q):
		return IntentReview
	case intentDebugPattern.MatchString(q):
		return IntentDebug
	case intentRefactorPattern.MatchString(q):
		return IntentRefactor
	case intentTestPattern.MatchString(q):
		return IntentTest
	case intentIntegratePattern.MatchString(q):
		return IntentIntegrate
	case intentExplainPattern.MatchString(q):
		return IntentExplain
	case intentNavigatePattern.MatchString(q):
		return IntentNavigate
	default:
		return IntentSearch
	}
}

var (
	hubNamePattern = regexp.MustCompile(`(?i)/(utils?|helpers?|shared|common|constants?|types?|interfaces?|index)\.(go|ts|tsx|js|jsx|py|rb|java|kt|rs|cs|cpp|cc|c|h|hpp|swift|scala|php)$`)
	hubFolderEnd   = regexp.MustCompile(`(?i)/(index|init)\.(go|ts|tsx|js|jsx|py|rb)$`)
	pathTestRE     = regexp.MustCompile(`(?i)(^|/)(tests?|__tests__|spec|specs|e2e|testdata|fixtures?)(/|$)|_test\.(go|py|ts|tsx|js|jsx|rs|java)$|(\.test|\.spec)\.(ts|tsx|js|jsx|py|rb)$`)
	pathGenRE      = regexp.MustCompile(`(?i)(^|/)(dist|build|out|gen|generated|\.next|\.nuxt|target|coverage)(/|$)`)
	pathDocsRE     = regexp.MustCompile(`(?i)(^|/)(docs?|readme|changelog|license)(\.[a-z]+)?$|(^|/)(docs?)(/|$)`)
	pathVendoredRE = regexp.MustCompile(`(?i)(^|/)(node_modules|vendor|third_party|\.venv|venv|\.git)(/|$)`)
)

// PathPenalty returns the multiplicative penalty for path shape. Hard zero for
// vendored paths so they cannot be surfaced; lighter multipliers for tests,
// generated code, and docs. The IntentTest weights set PenaltyTest to 1.0 so
// test queries do not fight the penalty.
func PathPenalty(p string, w RankerWeights) float64 {
	clean := path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	switch {
	case pathVendoredRE.MatchString(clean):
		return w.PenaltyVendored
	case pathTestRE.MatchString(clean):
		return w.PenaltyTest
	case pathGenRE.MatchString(clean):
		return w.PenaltyGenerated
	case pathDocsRE.MatchString(clean):
		return w.PenaltyDocs
	}
	return 1.0
}

// IsNameHub flags conventional utility-bucket files by name. These tend to be
// imported by many things even when irrelevant to a specific task and benefit
// from explicit demotion.
func IsNameHub(p string) bool {
	clean := strings.ReplaceAll(p, "\\", "/")
	return hubNamePattern.MatchString(clean) || hubFolderEnd.MatchString(clean)
}

// FileGraph adapts the symbol/package-level Graph into a file→file adjacency
// view, which is what the ranker needs for hop boosts and fanout-based hub
// detection.
type FileGraph struct {
	Forward map[string]map[string]struct{} // file -> files it depends on
	Reverse map[string]map[string]struct{} // file -> files that depend on it
	Files   map[string]struct{}
}

// BuildFileGraph projects the comprehension graph onto file-level edges by
// resolving each edge endpoint to the file that owns it. Symbol-to-symbol
// edges become file-to-file edges; self-loops are dropped.
func BuildFileGraph(graph *Graph) FileGraph {
	out := FileGraph{
		Forward: make(map[string]map[string]struct{}),
		Reverse: make(map[string]map[string]struct{}),
		Files:   make(map[string]struct{}),
	}
	if graph == nil {
		return out
	}
	owners := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.Path != "" {
			owners[node.ID] = node.Path
			out.Files[node.Path] = struct{}{}
		}
	}
	for _, edge := range graph.Edges {
		src, ok := owners[edge.SourceID]
		if !ok {
			continue
		}
		dst, ok := owners[edge.TargetID]
		if !ok {
			continue
		}
		if src == dst {
			continue
		}
		if out.Forward[src] == nil {
			out.Forward[src] = make(map[string]struct{})
		}
		out.Forward[src][dst] = struct{}{}
		if out.Reverse[dst] == nil {
			out.Reverse[dst] = make(map[string]struct{})
		}
		out.Reverse[dst][src] = struct{}{}
	}
	return out
}

// FanoutHubs returns the set of files whose reverse-import fanout exceeds
// fraction * total-files-in-graph. Mirrors sigmap's hub detection: utility
// files imported by a large share of the codebase get demoted so they don't
// dominate every query.
func FanoutHubs(fg FileGraph, fraction float64) map[string]struct{} {
	if fraction <= 0 {
		return nil
	}
	total := len(fg.Files)
	if total == 0 {
		return nil
	}
	threshold := int(float64(total) * fraction)
	if threshold < 2 {
		threshold = 2
	}
	hubs := make(map[string]struct{})
	for file, deps := range fg.Reverse {
		if len(deps) >= threshold {
			hubs[file] = struct{}{}
		}
	}
	return hubs
}

// MatchedFilePaths returns the set of file paths represented by a set of
// matched node IDs. Useful for translating "which symbols matched the query"
// into "which files matched" for hop-boost lookups.
func MatchedFilePaths(graph *Graph, matchedIDs map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	if graph == nil {
		return out
	}
	for _, node := range graph.Nodes {
		if node.Path == "" {
			continue
		}
		if _, ok := matchedIDs[node.ID]; ok {
			out[node.Path] = struct{}{}
		}
	}
	return out
}

// HopBoost returns the additive bonus for a candidate file based on its
// shortest-path distance to any matched file. 1-hop neighbors get the full
// boost; 2-hop neighbors get a decayed boost.
func HopBoost(file string, fg FileGraph, matchedFiles map[string]struct{}, w RankerWeights) float64 {
	if _, isMatch := matchedFiles[file]; isMatch {
		return 0
	}
	if hop1Neighbor(file, fg, matchedFiles) {
		return w.Hop1Boost
	}
	if hop2Neighbor(file, fg, matchedFiles) {
		return w.Hop2Boost
	}
	return 0
}

func hop1Neighbor(file string, fg FileGraph, matchedFiles map[string]struct{}) bool {
	for neighbor := range fg.Forward[file] {
		if _, ok := matchedFiles[neighbor]; ok {
			return true
		}
	}
	for neighbor := range fg.Reverse[file] {
		if _, ok := matchedFiles[neighbor]; ok {
			return true
		}
	}
	return false
}

func hop2Neighbor(file string, fg FileGraph, matchedFiles map[string]struct{}) bool {
	visited := map[string]struct{}{file: {}}
	for neighbor := range fg.Forward[file] {
		visited[neighbor] = struct{}{}
	}
	for neighbor := range fg.Reverse[file] {
		visited[neighbor] = struct{}{}
	}
	for hop1 := range visited {
		if hop1 == file {
			continue
		}
		for hop2 := range fg.Forward[hop1] {
			if _, seen := visited[hop2]; seen {
				continue
			}
			if _, ok := matchedFiles[hop2]; ok {
				return true
			}
		}
		for hop2 := range fg.Reverse[hop1] {
			if _, seen := visited[hop2]; seen {
				continue
			}
			if _, ok := matchedFiles[hop2]; ok {
				return true
			}
		}
	}
	return false
}

// RankInputs aggregates everything the ranker needs to score a candidate
// file. The caller is responsible for tokenising the query and resolving
// matched node IDs to file paths.
type RankInputs struct {
	Files          []PackFile
	Graph          *Graph
	MatchedIDs     map[string]struct{}
	Intent         Intent
	LearnedWeights map[string]float64
	QueryTokens    []string
	RequiredPaths  map[string]struct{}
}

// RankPackFiles assigns a Score to every input file by combining role floor,
// path-token match, hop-boost, hub demotion, stale demotion, path-shape
// penalty, and the persisted learned multiplier. Returns a copy of the slice
// sorted by Score descending.
func RankPackFiles(in RankInputs) []PackFile {
	weights := WeightsForIntent(in.Intent)
	fg := BuildFileGraph(in.Graph)
	matchedFiles := MatchedFilePaths(in.Graph, in.MatchedIDs)
	fanoutHubs := FanoutHubs(fg, weights.HubFanoutFraction)

	scored := make([]PackFile, len(in.Files))
	for i, file := range in.Files {
		base := roleFloor(file.Role, weights)
		reasons := []RankerReason{{
			Reason:     "role_floor",
			State:      firstNonEmpty(file.Role, "default"),
			ScoreDelta: roundScore(base),
		}}
		bonus := 0.0
		_, isMatch := matchedFiles[file.Path]
		_, isRequired := in.RequiredPaths[file.Path]
		if isRequired {
			bonus += weights.RequiredEditPath
			reasons = append(reasons, RankerReason{Reason: "required_edit_path", State: "declared", ScoreDelta: roundScore(weights.RequiredEditPath)})
		}
		if isMatch {
			// A direct task-term match must outrank pure neighbors; otherwise
			// a 1-hop neighbor of the match (which gets Hop1Boost) silently
			// wins on tie-broken role scores.
			bonus += weights.MatchBoost
			reasons = append(reasons, RankerReason{Reason: "direct_task_match", State: "matched", ScoreDelta: roundScore(weights.MatchBoost)})
		}
		if pathMatchTokens(file.Path, in.QueryTokens) {
			bonus += weights.PathMatch
			reasons = append(reasons, RankerReason{Reason: "path_token_match", State: "matched", ScoreDelta: roundScore(weights.PathMatch)})
		}
		hopBoost := HopBoost(file.Path, fg, matchedFiles, weights)
		if hopBoost > 0 {
			reasons = append(reasons, RankerReason{Reason: "graph_hop", State: graphHopState(file.Path, fg, matchedFiles), ScoreDelta: roundScore(hopBoost)})
		}
		bonus += hopBoost
		score := base + bonus
		_, fanoutHub := fanoutHubs[file.Path]
		isHub := fanoutHub || IsNameHub(file.Path)
		if isHub && !isMatch && !isRequired {
			score *= weights.HubDemotion
			reasons = append(reasons, RankerReason{Reason: "hub_demotion", State: hubState(file.Path, fanoutHub), Multiplier: weights.HubDemotion})
		}
		if file.Stale {
			score *= weights.StaleDemotion
			reasons = append(reasons, RankerReason{Reason: "stale_demotion", State: "stale", Multiplier: weights.StaleDemotion})
		}
		pathPenalty := 1.0
		if !isRequired {
			pathPenalty = PathPenalty(file.Path, weights)
			if pathPenalty != 1.0 {
				reasons = append(reasons, RankerReason{Reason: "path_penalty", State: pathPenaltyState(file.Path), Multiplier: pathPenalty})
			}
		}
		score *= pathPenalty
		if mult, ok := in.LearnedWeights[file.Path]; ok && mult > 0 {
			score *= mult
			reasons = append(reasons, RankerReason{Reason: "learned_weight", State: "applied", Multiplier: roundScore(mult)})
		}
		file.Score = roundScore(score)
		file.Hub = isHub
		file.RankerReasons = reasons
		scored[i] = file
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if packRoleRank(scored[i].Role) != packRoleRank(scored[j].Role) {
			return packRoleRank(scored[i].Role) > packRoleRank(scored[j].Role)
		}
		if scored[i].Stale != scored[j].Stale {
			return !scored[i].Stale
		}
		return scored[i].Path < scored[j].Path
	})
	return scored
}

func graphHopState(file string, fg FileGraph, matchedFiles map[string]struct{}) string {
	if hop1Neighbor(file, fg, matchedFiles) {
		return "hop1"
	}
	if hop2Neighbor(file, fg, matchedFiles) {
		return "hop2"
	}
	return "none"
}

func hubState(file string, fanoutHub bool) string {
	if fanoutHub && IsNameHub(file) {
		return "fanout_and_name"
	}
	if fanoutHub {
		return "fanout"
	}
	return "name"
}

func pathPenaltyState(p string) string {
	clean := path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	switch {
	case pathVendoredRE.MatchString(clean):
		return "vendored"
	case pathTestRE.MatchString(clean):
		return "test"
	case pathGenRE.MatchString(clean):
		return "generated"
	case pathDocsRE.MatchString(clean):
		return "docs"
	default:
		return "none"
	}
}

func roleFloor(role string, w RankerWeights) float64 {
	switch role {
	case "editable":
		return w.RoleEditable
	case "test":
		return w.RoleTest
	case "read_only_evidence":
		return w.RoleEvidence
	default:
		return w.RoleDefault
	}
}

func pathMatchTokens(p string, tokens []string) bool {
	if len(tokens) == 0 || p == "" {
		return false
	}
	lower := strings.ToLower(p)
	for _, tok := range tokens {
		t := strings.TrimSpace(strings.ToLower(tok))
		if len(t) < 3 {
			continue
		}
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

func roundScore(score float64) float64 {
	if score < 0 {
		score = 0
	}
	// Keep four decimals so JSON output is stable across runs.
	return float64(int64(score*10000+0.5)) / 10000
}
