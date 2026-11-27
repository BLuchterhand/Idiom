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
	"time"

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
	case "build-usage-cases":
		err = buildUsageCases(os.Args[2:])
	case "case-scores":
		err = caseScores(os.Args[2:])
	case "compare-scores":
		err = compareScores(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	case "baseline":
		err = baseline(os.Args[2:])
	default:
		usage()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "idiom-localization-eval:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: idiom-localization-eval build-cases|build-usage-cases|case-scores|compare-scores|run|compare|baseline [flags]")
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

// buildUsageCases builds cases from agent session transcripts (the seed's
// builder.usage section) and splits them by time.
func buildUsageCases(args []string) error {
	fs := flag.NewFlagSet("build-usage-cases", flag.ExitOnError)
	repo := fs.String("repo", ".", "git repository the sessions worked in")
	config := fs.String("config", "", "seed case file with a builder.usage section")
	sinceFlag := fs.String("since", "", "RFC 3339 time; sessions and queries before it are ignored")
	cutFlag := fs.String("split-cut", "", "RFC 3339 dev/hold-out cut; empty derives it from holdout_fraction")
	baseMode := fs.String("base", eval.UsageBaseMain, "base commit source: main or branch")
	mainRef := fs.String("main-ref", "", "overrides builder.usage.main_ref (default "+eval.UsageDefaultMainRef+")")
	worktreeList := fs.String("worktree-list", "", "file holding `git worktree list --porcelain` of the agents' checkout (branch mode)")
	out := fs.String("out", "", "case file to write")
	_ = fs.Parse(args)

	if *config == "" || *out == "" || *sinceFlag == "" || fs.NArg() == 0 {
		return fmt.Errorf("-config, -since, -out and at least one transcript directory are required")
	}

	file, err := readSeed(*config)
	if err != nil {
		return err
	}

	if file.Config.Usage == nil {
		return fmt.Errorf("%s has no builder.usage section", *config)
	}

	if *mainRef != "" {
		file.Config.Usage.MainRef = *mainRef
	}

	opts, err := usageBuildOptions(*sinceFlag, *cutFlag, *baseMode, *worktreeList, *file.Config.Usage)
	if err != nil {
		return err
	}

	cases, cut, funnel, err := eval.BuildLocalizationUsageCases(*repo, fs.Args(), file.Config, *file.Config.Usage, opts)
	if err != nil {
		return err
	}

	file.Schema, file.Cases, file.SplitCut = 1, cases, cut.UTC().Format(time.RFC3339)
	counts := map[string]int{}

	for _, c := range cases {
		counts[c.Split]++
	}

	fmt.Printf("%d cases (dev %d, holdout %d), cut %s, base %s\n", len(cases), counts["dev"], counts["holdout"], file.SplitCut, opts.BaseMode)
	fmt.Printf("funnel %+v\n", funnel)

	return writeJSON(*out, file)
}

func usageBuildOptions(since, cut, baseMode, worktreeList string, usage eval.LocalizationUsageConfig) (eval.UsageBuildOptions, error) {
	opts := eval.UsageBuildOptions{BaseMode: baseMode}

	var err error
	if opts.Since, err = time.Parse(time.RFC3339, since); err != nil {
		return opts, fmt.Errorf("-since: %w", err)
	}

	if cut != "" {
		if opts.SplitCut, err = time.Parse(time.RFC3339, cut); err != nil {
			return opts, fmt.Errorf("-split-cut: %w", err)
		}
	}

	switch baseMode {
	case eval.UsageBaseMain:
		return opts, nil
	case eval.UsageBaseBranch:
	default:
		return opts, fmt.Errorf("-base %q: want main or branch", baseMode)
	}

	if worktreeList != "" {
		data, err := os.ReadFile(worktreeList)
		if err != nil {
			return opts, err
		}

		if opts.WorktreeBranches, err = eval.UsageWorktreeBranches(string(data), usage); err != nil {
			return opts, err
		}
	}

	return opts, nil
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
	history := fs.String("history", "", "JSON object (or a file holding one) for the history section of each checkout's .idiom/config.json; {\"mode\":\"off\"} is the no-channel arm")
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
	if *history != "" {
		if opts.History, err = readHistoryFlag(*history); err != nil {
			return err
		}
	}

	if *only != "" {
		opts.OnlyIDs = strings.Split(*only, ",")
	}

	result, err := eval.RunLocalizationEval(ctx, file, opts)
	if werr := writeJSON(*out, result); werr != nil {
		return werr
	}

	return err
}

// readHistoryFlag takes inline JSON or the path of a JSON file.
func readHistoryFlag(value string) (map[string]any, error) {
	raw := []byte(value)
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		var err error
		if raw, err = os.ReadFile(value); err != nil {
			return nil, err
		}
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("-history: %w", err)
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}

func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	before := fs.String("before", "", "earlier result file")
	after := fs.String("after", "", "later result file")
	metric := fs.String("metric", "recall_capped", "metric name")
	split := fs.String("split", "dev", "dev, holdout or all")
	minMean := fs.Float64("min-mean", 0, "target mean delta")
	noReg := fs.Bool("no-regression", false, "guard against loss only (implied by a no_regression corpus)")
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
		Metric: *metric, MinMean: *minMean, Confidence: confidence, LowerBoundFloor: *floor, NoRegression: *noReg,
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
