// Command idiom-localization-eval builds localization eval cases from git
// history, runs them in exact mode against an idiom binary, and compares runs
// with the paired gates. See internal/evidence/eval/localization_eval_*.go.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"idiom/internal/evidence/eval"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	var err error

	switch os.Args[1] {
	case "build-cases":
		err = buildCases(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	default:
		usage()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "idiom-localization-eval:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: idiom-localization-eval build-cases|run|compare [flags]")
	os.Exit(2)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func buildCases(args []string) error {
	fs := flag.NewFlagSet("build-cases", flag.ExitOnError)
	repo := fs.String("repo", ".", "git repository")
	config := fs.String("config", "", "case file whose corpus, builder and localization sections seed the build")
	out := fs.String("out", "", "case file to write")
	_ = fs.Parse(args)

	if *config == "" || *out == "" {
		return fmt.Errorf("-config and -out are required")
	}

	file, err := readSeed(*config)
	if err != nil {
		return err
	}

	file.Cases, err = eval.BuildLocalizationEvalCases(*repo, file.Config)
	if err != nil {
		return err
	}

	file.Schema = 1
	counts := map[string]int{}

	for _, c := range file.Cases {
		counts[c.Split]++
	}

	fmt.Printf("%d cases (dev %d, holdout %d)\n", len(file.Cases), counts["dev"], counts["holdout"])

	return writeJSON(*out, file)
}

// readSeed reads a case file without requiring cases (a builder config only).
func readSeed(path string) (eval.LocalizationEvalCaseFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return eval.LocalizationEvalCaseFile{}, err
	}

	var file eval.LocalizationEvalCaseFile

	return file, json.Unmarshal(data, &file)
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	repo := fs.String("repo", ".", "git repository the cases came from")
	cases := fs.String("cases", "", "case file")
	bin := fs.String("idiom-bin", "idiom", "idiom binary")
	cache := fs.String("cache", "", "index cache directory")
	cacheGB := fs.Int64("cache-max-gb", 20, "index cache size cap")
	subset := fs.String("subset", "all", "dev, holdout or all")
	only := fs.String("only", "", "comma-separated case ids")
	seed := fs.Bool("seed", false, "seed each index from the nearest cached tree (faster, tail ranks may differ from a cold index); default is a cold index per case")
	format := fs.String("format", "", "context_impact output format under test: empty (default JSON) or compact")
	timed := fs.Int("timed-calls", 3, "timed calls per case after one warm-up")
	out := fs.String("out", "", "result file")
	_ = fs.Parse(args)

	if *cases == "" || *cache == "" || *out == "" {
		return fmt.Errorf("-cases, -cache and -out are required")
	}

	file, err := eval.ReadLocalizationEvalCaseFile(*cases)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	opts := eval.LocalizationEvalRunOptions{Repo: *repo, IdiomBin: *bin, CacheDir: *cache, CacheMaxBytes: *cacheGB << 30,
		Subset: *subset, TimedCalls: *timed, NoSeed: !*seed, Format: *format, Log: os.Stderr}
	if *only != "" {
		opts.OnlyIDs = strings.Split(*only, ",")
	}

	result, err := eval.RunLocalizationEval(ctx, file, opts)
	if werr := writeJSON(*out, result); werr != nil {
		return werr
	}

	return err
}

func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	before := fs.String("before", "", "earlier result file")
	after := fs.String("after", "", "later result file")
	metric := fs.String("metric", "recall_capped", "metric name")
	split := fs.String("split", "dev", "dev, holdout or all")
	minMean := fs.Float64("min-mean", 0, "target mean delta")
	floor := fs.Float64("floor", 0, "lower bound must exceed this (-0.01 for no-regression phases)")
	_ = fs.Parse(args)

	b, err := readResult(*before)
	if err != nil {
		return err
	}

	a, err := readResult(*after)
	if err != nil {
		return err
	}

	confidence := eval.LocalizationEvalDevConfidence
	if *split == "holdout" {
		confidence = eval.LocalizationEvalHoldoutConfidence
	}

	spec := eval.EffectiveLocalizationEvalGateSpec(a.Gate, eval.LocalizationEvalGateSpec{
		Metric: *metric, MinMean: *minMean, Confidence: confidence, LowerBoundFloor: *floor,
	})
	verdict := eval.EvaluateLocalizationEvalGate(spec, eval.PairLocalizationEvalDeltas(b, a, *metric, *split))

	data, _ := json.MarshalIndent(verdict, "", " ")
	fmt.Println(string(data))

	if !verdict.Pass {
		os.Exit(1)
	}

	return nil
}

func readResult(path string) (eval.LocalizationEvalRunResult, error) {
	var r eval.LocalizationEvalRunResult

	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}

	return r, json.Unmarshal(data, &r)
}
