package comprehension

import (
	"path"
	"slices"
	"strings"
)

// Graph hop and channel policy. Pure lexical retrieval misses files that
// changed together with a matched file but share no query term with the
// task. The hop pulls them in as graph neighbours of the best lexical
// matches. Which edge kinds help depends on the language of the matched
// file, and so does whether the symbol/path channel joins the fusion; both
// are discrete per-language choices recorded in languagePolicies, chosen on
// the development split of the localization eval (docs in the commit), not
// fitted weights.

const (
	hopSeeds = 10  // best lexical matches that expand
	hopAlpha = 0.5 // share of a seed's normalized score a neighbour inherits
)

// hopKinds is a set of file-to-file relations a seed may follow.
type hopKinds uint8

const (
	hopPairs   hopKinds = 1 << iota // header and implementation with one stem
	hopImports                      // import and include edges
	hopCalls                        // call edges
	hopTests                        // test edges
	hopExtends                      // inheritance edges

	hopNone hopKinds = 0
	hopAll           = hopPairs | hopImports | hopCalls | hopTests | hopExtends
)

// languagePolicy is how one language takes part in retrieval.
type languagePolicy struct {
	// Hop is what a matched file of this language expands along.
	Hop hopKinds
	// Channel admits files of this language from the graph symbol/path
	// channel into the fusion.
	Channel bool
	// Weight scales the BM25 score of files of this language; zero means 1.
	// It demotes kinds of file a task rarely names, as test files are.
	Weight float64
	// Class names a document class with its own BM25 statistics; empty is
	// the source class.
	Class string
}

// languagePolicies are the dev-split choices; defaultPolicy covers any other
// language. See the selection record in the commit message.
//
//   - go: the call graph pulls in changed-together files (dev recall .387 to
//     .447); the symbol/path channel stays on (off: .335).
//   - cpp: all relations hop and the channel is off (dev recall .330 to .402;
//     the channel on the C++ pool cost .056 against BM25 alone).
//   - everything else: no hop, channel off (no dev difference on Go, a gain
//     on the mixed BenTech tree).
//   - content, shader, powershell (files a localization config adds to the
//     index): score x0.5. Unweighted they displaced source files on BenTech
//     dev (MRR .450 to .433); x0.5 gives .407 / .765 / .454 against .402 /
//     .745 / .450 without them, x0.25 loses recall (.399).
var languagePolicies = map[string]languagePolicy{
	"go":            {Hop: hopCalls, Channel: true},
	"cpp":           {Hop: hopAll, Channel: false},
	languageContent: {Weight: extraFileWeight},
	languageShader:  {Weight: extraFileWeight},
	"powershell":    {Weight: extraFileWeight},
}

const (
	extraFileWeight = 0.5
	classExtra      = "extra" // files a localization config adds beyond source code
)

// languageClass is the statistics class of a file of the given language.
func languageClass(language string) string {
	return policyFor(language).Class
}

var defaultPolicy = languagePolicy{Hop: hopNone, Channel: false}

func policyFor(language string) languagePolicy {
	if p, ok := languagePolicies[normalizeLanguage(language)]; ok {
		return p
	}

	return defaultPolicy
}

// languageWeight is the score multiplier for a file of the given language.
func languageWeight(language string) float64 {
	if w := policyFor(language).Weight; w > 0 {
		return w
	}

	return 1
}

var edgeHopKind = map[string]hopKinds{"imports": hopImports, "calls": hopCalls, "tests": hopTests, "extends": hopExtends}

// hopGraph is the file-level neighbourhood: for each file, its neighbours
// and the relations that join them.
type hopGraph map[string]map[string]hopKinds

func buildHopGraph(g Graph, nodesByID map[string]Node) hopGraph {
	out := hopGraph{}
	link := func(a, b string, kind hopKinds) {
		if a == b || a == "" || b == "" {
			return
		}

		for _, p := range [][2]string{{a, b}, {b, a}} {
			if out[p[0]] == nil {
				out[p[0]] = map[string]hopKinds{}
			}

			out[p[0]][p[1]] |= kind
		}
	}

	for _, edge := range g.Edges {
		kind, ok := edgeHopKind[edge.Kind]
		if !ok {
			continue
		}

		link(nodesByID[edge.SourceID].Path, nodesByID[edge.TargetID].Path, kind)
	}

	byStem := map[string][]string{}

	for _, node := range g.Nodes {
		if node.Kind == "file" && node.Path != "" {
			stem := strings.TrimSuffix(node.Path, path.Ext(node.Path))
			byStem[stem] = append(byStem[stem], node.Path)
		}
	}

	for _, group := range byStem {
		for i := range group {
			for _, other := range group[i+1:] {
				link(group[i], other, hopPairs)
			}
		}
	}

	return out
}

// HopLexical adds graph neighbours of the best lexical hits. A neighbour
// scores the larger of its own score and its own plus hopAlpha times the
// seed's, with every score normalized by the top hit, so it ranks near the
// seed that pulled it in. languageOf gives a file's language; include is the
// scope filter. Hits are returned best first.
func HopLexical(hits []LexicalHit, hg hopGraph, languageOf func(string) string, include func(string) bool) []LexicalHit {
	if len(hits) == 0 || len(hg) == 0 {
		return hits
	}

	top := hits[0].Score
	if top <= 0 {
		return hits
	}

	score := make(map[string]float64, len(hits))
	byPath := make(map[string]LexicalHit, len(hits))

	for _, hit := range hits {
		score[hit.Path] = hit.Score / top
		byPath[hit.Path] = hit
	}

	final := make(map[string]float64, len(score))
	for p, s := range score {
		final[p] = s
	}

	via := map[string]string{}

	for _, seed := range hits[:min(hopSeeds, len(hits))] {
		allowed := policyFor(languageOf(seed.Path)).Hop
		if allowed == hopNone {
			continue
		}

		for nb, kinds := range hg[seed.Path] {
			if kinds&allowed == 0 || !include(nb) {
				continue
			}

			if cand := score[nb] + hopAlpha*score[seed.Path]; cand > final[nb] {
				final[nb] = cand
				via[nb] = seed.Path
			}
		}
	}

	out := make([]LexicalHit, 0, len(final))

	for p, s := range final {
		hit := byPath[p]
		hit.Path, hit.Score = p, s

		if v, ok := via[p]; ok {
			hit.Via = v
		}

		out = append(out, hit)
	}

	slices.SortFunc(out, func(a, b LexicalHit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return strings.Compare(a.Path, b.Path)
		}
	})

	return out
}
