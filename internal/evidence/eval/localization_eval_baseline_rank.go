package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
)

// rrfK is the reciprocal rank fusion constant of Cormack, Clarke and
// Buettcher (SIGIR 2009); 60 is their published default, not fitted here.
const rrfK = 60

// LocalizationBaselineConfig is one ranker. Scores of the content, path and
// docs fields are max-normalized per case and mixed linearly (or fused by
// reciprocal rank when RRF is set); then each file gains Alpha times the best
// score among its graph neighbours in the top Seeds files.
type LocalizationBaselineConfig struct {
	Name        string
	WContent    float64
	WPath       float64
	WDocs       float64
	WIdiom      float64 // RRF only: weight of idiom's own ranked list
	Alpha       float64
	Seeds       int
	TestWeight  float64 // multiplies scores of files matching the test regex
	RRF         bool
	PairsOnly   bool // graph step follows header/implementation pairs only
	ImportsOnly bool // graph step follows import/include edges and pairs, not calls
	// FuseIdiom, when positive, fuses the final lexical+graph ranking with
	// idiom's own ranked list by reciprocal rank (idiom weighted FuseIdiom).
	FuseIdiom float64
}

// LocalizationBaselineOptions configures a baseline run.
type LocalizationBaselineOptions struct {
	Repo        string
	CacheDir    string // idiom index cache of a previous run (same cases)
	CodeMapDir  string // optional: <case id>.md code maps generated at each base
	IdiomResult string // optional: idiom run result whose ranked lists RRF may fuse
	Log         io.Writer
}

// LoadLocalizationBaselineCases reads every case's corpus, graph and code map.
func LoadLocalizationBaselineCases(file LocalizationEvalCaseFile, opts LocalizationBaselineOptions) ([]*baselineCase, error) {
	out := make([]*baselineCase, len(file.Cases))
	errs := make([]error, len(file.Cases))

	var wg sync.WaitGroup

	sem := make(chan struct{}, max(1, runtime.NumCPU()/2))

	for i, c := range file.Cases {
		wg.Add(1)

		go func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			indexDir, err := findCachedIndex(opts.Repo, opts.CacheDir, c.Base)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", c.ID, err)
				return
			}

			codeMap := ""
			if opts.CodeMapDir != "" {
				codeMap = filepath.Join(opts.CodeMapDir, c.ID+".md")
			}

			out[i], errs[i] = loadBaselineCase(opts.Repo, indexDir, codeMap, c, file.Config)
			if opts.Log != nil {
				fmt.Fprintf(opts.Log, "loaded %s (%d files)\n", c.ID, len(out[i].files))
			}
		}()
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

func findCachedIndex(repo, cacheDir, base string) (string, error) {
	tree, err := gitOut(repo, "rev-parse", base+"^{tree}")
	if err != nil {
		return "", err
	}

	matches, _ := filepath.Glob(filepath.Join(cacheDir, "*", strings.TrimSpace(tree)[:12]+"-*"))
	if len(matches) == 0 {
		return "", fmt.Errorf("no cached index for tree %s", strings.TrimSpace(tree)[:12])
	}

	sort.Strings(matches)

	return matches[0], nil
}

// rank ranks one case under cfg.
func (bc *baselineCase) rank(cfg LocalizationBaselineConfig, idiomRanked []string) []string {
	n := len(bc.files)
	base := make([]float64, n)

	fields := []struct {
		w float64
		s []float64
	}{
		{cfg.WContent, bc.content.scores(bc.query)},
		{cfg.WPath, bc.path.scores(bc.query)},
		{cfg.WDocs, bc.docs.scores(bc.query)},
	}

	for _, f := range fields {
		if f.w == 0 {
			continue
		}

		if cfg.RRF {
			for r, file := range bc.rankScores(f.s) {
				base[bc.index[file]] += f.w / float64(rrfK+r+1)
			}

			continue
		}

		if m := maxScore(f.s); m > 0 {
			for i, v := range f.s {
				base[i] += f.w * v / m
			}
		}
	}

	if cfg.RRF && cfg.WIdiom > 0 {
		for r, file := range idiomRanked {
			if i, ok := bc.index[file]; ok {
				base[i] += cfg.WIdiom / float64(rrfK+r+1)
			}
		}
	}

	if cfg.TestWeight > 0 && cfg.TestWeight != 1 {
		for i := range base {
			if bc.isTest[i] {
				base[i] *= cfg.TestWeight
			}
		}
	}

	final := slices.Clone(base)

	if cfg.Alpha > 0 && cfg.Seeds > 0 {
		for _, seedFile := range firstN(bc.rankScores(base), cfg.Seeds) {
			s := bc.index[seedFile]

			adj := bc.adj
			if cfg.PairsOnly {
				adj = bc.pairs
			} else if cfg.ImportsOnly {
				adj = bc.imports
			}

			for nb := range adj[s] {
				final[nb] = max(final[nb], base[nb]+cfg.Alpha*base[s])
			}
		}
	}

	ranked := bc.rankScores(final)
	if cfg.FuseIdiom <= 0 {
		return ranked
	}

	// Both lists are cut to the depth idiom runs keep, so membership in the
	// lexical tail does not count as agreement.
	fused := make([]float64, n)
	for r, file := range firstN(ranked, localizationEvalRankedKept) {
		fused[bc.index[file]] += 1 / float64(rrfK+r+1)
	}

	for r, file := range firstN(idiomRanked, localizationEvalRankedKept) {
		if i, ok := bc.index[file]; ok {
			fused[i] += cfg.FuseIdiom / float64(rrfK+r+1)
		}
	}

	return bc.rankScores(fused)
}

func firstN(s []string, n int) []string {
	return s[:min(n, len(s))]
}

// ScoreLocalizationBaseline runs cfg over every case and returns a result in
// the idiom run format, so `compare` pairs it with any idiom run.
func ScoreLocalizationBaseline(corpus string, cases []*baselineCase, cfg LocalizationBaselineConfig, idiom map[string][]string) LocalizationEvalRunResult {
	result := LocalizationEvalRunResult{Schema: localizationEvalSchema, Corpus: corpus, Mode: "baseline:" + cfg.Name, Limit: "k=8"}

	for _, bc := range cases {
		ranked := bc.rank(cfg, idiom[bc.c.ID])
		cr := LocalizationEvalCaseResult{ID: bc.c.ID, Split: bc.c.Split, Deterministic: true, Fresh: true}
		cr.Ranked = firstN(ranked, localizationEvalRankedKept)
		cr.Metrics8, _ = ComputeLocalizationEvalMetrics(ranked, bc.c.Gold, LocalizationEvalGatedK)
		cr.Metrics5, _ = ComputeLocalizationEvalMetrics(ranked, bc.c.Gold, LocalizationEvalSecondaryK)
		result.Cases = append(result.Cases, cr)
	}

	result.Summary = SummarizeLocalizationEval(result.Cases)

	return result
}

// ReadLocalizationIdiomRanked reads the ranked lists of an idiom run result.
func ReadLocalizationIdiomRanked(path string) (map[string][]string, error) {
	r := map[string][]string{}
	if path == "" {
		return r, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var res LocalizationEvalRunResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}

	for _, c := range res.Cases {
		r[c.ID] = c.Ranked
	}

	return r, nil
}

// LocalizationBaselineGrid is the fitted family: path and docs weights, graph
// strength (all edges or header pairs only) and test demotion, with content BM25 fixed at weight 1.
func LocalizationBaselineGrid() []LocalizationBaselineConfig {
	var out []LocalizationBaselineConfig

	for _, wp := range []float64{0, 0.25, 0.5, 1} {
		for _, wd := range []float64{0, 0.25, 0.5, 1} {
			for _, a := range []float64{0, 0.25, 0.5, 0.75} {
				for _, t := range []float64{1, 0.5} {
					for _, pairs := range []bool{false, true} {
						out = append(out, LocalizationBaselineConfig{
							Name:     fmt.Sprintf("fit wp=%.2f wd=%.2f a=%.2f t=%.1f pairs=%t", wp, wd, a, t, pairs),
							WContent: 1, WPath: wp, WDocs: wd, Alpha: a, Seeds: 10, TestWeight: t, PairsOnly: pairs,
						})
					}
				}
			}
		}
	}

	return out
}

// LocalizationCeiling counts, over gold files, how many an index at base can
// reach lexically. Literal: the file shares any query term (path or text).
// Rare: it shares a term in at most RareFraction of files. Hop: it is one
// graph edge from a file that matches (literal or rare).
type LocalizationCeiling struct {
	Split          string  `json:"split"`
	Cases          int     `json:"cases"`
	GoldFiles      int     `json:"gold_files"`
	NotIndexed     int     `json:"not_indexed"`
	NoTerm         int     `json:"no_shared_term"`
	NoTermNoHop    int     `json:"no_term_no_hop"`
	NoRare         int     `json:"no_rare_term"`
	NoRareNoHop    int     `json:"no_rare_no_hop"`
	CasesAllDark   int     `json:"cases_no_gold_reachable_rare_or_hop"`
	RareFraction   float64 `json:"rare_fraction"`
	QueryTermsMean float64 `json:"query_terms_mean"`
}

// ComputeLocalizationCeiling measures the reachability ceiling by split.
func ComputeLocalizationCeiling(cases []*baselineCase, rareFraction float64) []LocalizationCeiling {
	var out []LocalizationCeiling

	for _, split := range []string{"dev", "holdout", "all"} {
		lc := LocalizationCeiling{Split: split, RareFraction: rareFraction}

		for _, bc := range cases {
			if split != "all" && bc.c.Split != split {
				continue
			}

			lc.Cases++
			terms := uniqueStrings(bc.query)
			lc.QueryTermsMean += float64(len(terms))

			rare := map[string]bool{}
			for _, t := range terms {
				df := bc.content.docFreq(t)
				rare[t] = df > 0 && float64(df) <= rareFraction*float64(len(bc.files))
			}

			matches := func(i int, rareOnly bool) bool {
				for t, c := range bc.content.tf[i] {
					if c > 0 && (!rareOnly || rare[t]) {
						return true
					}
				}

				return false
			}

			hop := func(i int, rareOnly bool) bool {
				for nb := range bc.adj[i] {
					if matches(nb, rareOnly) {
						return true
					}
				}

				return false
			}

			reachable := 0

			for _, g := range bc.c.Gold {
				lc.GoldFiles++

				i, ok := bc.index[g]
				if !ok {
					lc.NotIndexed++
					continue
				}

				if !matches(i, false) {
					lc.NoTerm++
					if !hop(i, false) {
						lc.NoTermNoHop++
					}
				}

				if matches(i, true) || hop(i, true) {
					reachable++
				} else {
					lc.NoRareNoHop++
				}

				if !matches(i, true) {
					lc.NoRare++
				}
			}

			if reachable == 0 {
				lc.CasesAllDark++
			}
		}

		if lc.Cases > 0 {
			lc.QueryTermsMean /= float64(lc.Cases)
		}

		out = append(out, lc)
	}

	return out
}
