package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/evidence/eval"
)

// baselineConfigs are the fixed (unfitted) reference rankers.
var baselineConfigs = []eval.LocalizationBaselineConfig{
	{Name: "bm25", WContent: 1},
	{Name: "bm25-graph", WContent: 1, Alpha: 0.5, Seeds: 10},
	{Name: "docs", WDocs: 1},
	{Name: "rrf", RRF: true, WContent: 1, WPath: 1, WDocs: 1},
	{Name: "rrf-idiom", RRF: true, WContent: 1, WPath: 1, WDocs: 1, WIdiom: 1},
	{Name: "bm25-idiom", RRF: true, WContent: 1, WIdiom: 1},
	{Name: "bm25-notests", WContent: 1, TestWeight: 0.5},
	{Name: "bm25-notests-graph", WContent: 1, TestWeight: 0.5, Alpha: 0.5, Seeds: 10},
	{Name: "bm25-notests-pairs", WContent: 1, TestWeight: 0.5, Alpha: 0.5, Seeds: 10, PairsOnly: true},
	{Name: "hybrid-idiom", WContent: 1, TestWeight: 0.5, Alpha: 0.5, Seeds: 10, FuseIdiom: 1},
	{Name: "bm25-notests-imports", WContent: 1, TestWeight: 0.5, Alpha: 0.5, Seeds: 10, ImportsOnly: true},
}

// baseline scores the reference rankers, fits the grid on dev only, writes
// one result file per ranker (pairable with `compare`) and the ceiling.
func baseline(args []string) error {
	fs := flag.NewFlagSet("baseline", flag.ExitOnError)
	repo := fs.String("repo", ".", "git repository the cases came from")
	cases := fs.String("cases", "", "case file")
	cache := fs.String("cache", "", "index cache of an idiom run over the same cases")
	codeMaps := fs.String("codemaps", "", "directory of <case id>.md code maps (optional)")
	idiomResult := fs.String("idiom-result", "", "idiom run result to fuse (optional)")
	outDir := fs.String("out-dir", "", "directory for result files")
	rare := fs.Float64("rare-fraction", 0.02, "ceiling: a term is rare in at most this fraction of files")
	_ = fs.Parse(args)

	if *cases == "" || *cache == "" || *outDir == "" {
		return fmt.Errorf("-cases, -cache and -out-dir are required")
	}

	file, err := eval.ReadLocalizationEvalCaseFile(*cases)
	if err != nil {
		return err
	}

	loaded, err := eval.LoadLocalizationBaselineCases(file, eval.LocalizationBaselineOptions{
		Repo: *repo, CacheDir: *cache, CodeMapDir: *codeMaps, Log: os.Stderr,
	})
	if err != nil {
		return err
	}

	idiom, err := eval.ReadLocalizationIdiomRanked(*idiomResult)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}

	for _, cfg := range baselineConfigs {
		res := eval.ScoreLocalizationBaseline(file.Corpus, loaded, cfg, idiom)
		if err := writeJSON(filepath.Join(*outDir, cfg.Name+".json"), res); err != nil {
			return err
		}

		printSummary(cfg.Name, res)
	}

	var grid strings.Builder

	best, bestRes := eval.LocalizationBaselineConfig{}, eval.LocalizationEvalRunResult{}
	bestKey := [2]float64{-1, -1}

	for _, cfg := range eval.LocalizationBaselineGrid() {
		res := eval.ScoreLocalizationBaseline(file.Corpus, loaded, cfg, idiom)
		dev := res.Summary[0]
		fmt.Fprintf(&grid, "%s\t%.4f\t%.4f\t%.4f\n", cfg.Name, dev.RecallCapped8, dev.Hit8, dev.MRR8)

		if key := [2]float64{dev.RecallCapped8, dev.MRR8}; key[0] > bestKey[0] || (key[0] == bestKey[0] && key[1] > bestKey[1]) {
			best, bestRes, bestKey = cfg, res, key
		}
	}

	if err := os.WriteFile(filepath.Join(*outDir, "grid-dev.tsv"), []byte(grid.String()), 0o644); err != nil {
		return err
	}

	bestRes.Mode = "baseline:fitted-on-dev " + best.Name
	if err := writeJSON(filepath.Join(*outDir, "fitted.json"), bestRes); err != nil {
		return err
	}

	printSummary("fitted ("+best.Name+")", bestRes)

	ceiling := eval.ComputeLocalizationCeiling(loaded, *rare)

	return writeJSON(filepath.Join(*outDir, "ceiling.json"), ceiling)
}

func printSummary(name string, res eval.LocalizationEvalRunResult) {
	for _, s := range res.Summary {
		fmt.Printf("%-40s %-8s n=%-3d rc8 %.3f hit8 %.3f mrr %.3f\n", name, s.Split, s.Cases, s.RecallCapped8, s.Hit8, s.MRR8)
	}
}
