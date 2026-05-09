package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/comprehension"
	"idiom/internal/telemetry"
)

func runRankerMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runRankerMainWithTelemetry(args, stdout, stderr, nil)
}

func runRankerMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printRankerUsage(stderr)
		return 0
	}
	logCLIPhase(rec, "ranker_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "boost":
		return runRankerNudge(args[1:], stdout, stderr, rec, "boost")
	case "penalize":
		return runRankerNudge(args[1:], stdout, stderr, rec, "penalize")
	case "reset":
		return runRankerReset(args[1:], stdout, stderr, rec)
	case "show":
		return runRankerShow(args[1:], stdout, stderr, rec)
	default:
		fmt.Fprintf(stderr, "unknown ranker command: %s\n", args[0])
		printRankerUsage(stderr)
		return 2
	}
}

func printRankerUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom ranker boost    [--config path] [--note text] <file> [<file> ...]")
	fmt.Fprintln(stderr, "  idiom ranker penalize [--config path] [--note text] <file> [<file> ...]")
	fmt.Fprintln(stderr, "  idiom ranker reset    [--config path]")
	fmt.Fprintln(stderr, "  idiom ranker show     [--config path] [--json]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Manually nudge per-file ranker weights stored under .idiom/ranker/weights.json.")
	fmt.Fprintln(stderr, "Boost and penalize each apply one decay-then-nudge cycle to weights.json.")
	fmt.Fprintln(stderr, "Reset clears weights.json; the learning_log.jsonl history is preserved.")
	fmt.Fprintln(stderr, "Show prints current weights and recent learning-log entries.")
}

type rankerNudgeOptions struct {
	Config string
	Note   string
}

func runRankerNudge(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, action string) int {
	opts := rankerNudgeOptions{Config: "config.json"}
	fs := flag.NewFlagSet("idiom ranker "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Config, "config", opts.Config, "path to config file")
	fs.StringVar(&opts.Note, "note", "", "optional note recorded in the learning log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := uniqueRankerFiles(fs.Args())
	if len(files) == 0 {
		fmt.Fprintf(stderr, "ranker %s: at least one file path is required\n", action)
		return 2
	}
	cfg, err := loadToolConfig(opts.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root := strings.TrimSpace(cfg.Workspace.Root)
	if root == "" {
		fmt.Fprintln(stderr, "workspace root not configured; cannot persist ranker weights")
		return 1
	}
	updateOpts := comprehension.UpdateLearnedWeightsOptions{}
	switch action {
	case "boost":
		updateOpts.Boost = files
	case "penalize":
		updateOpts.Penalty = files
	default:
		fmt.Fprintf(stderr, "unknown ranker nudge action: %s\n", action)
		return 2
	}
	weights, err := comprehension.UpdateLearnedWeights(root, updateOpts)
	if err != nil {
		fmt.Fprintf(stderr, "ranker %s failed: %v\n", action, err)
		return 1
	}
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		Source: "cli",
		Action: action,
		Files:  files,
		Note:   opts.Note,
	}); err != nil {
		fmt.Fprintf(stderr, "ranker %s log append failed: %v\n", action, err)
		return 1
	}
	logCLIPhase(rec, "ranker_"+action, map[string]any{"files": len(files)})
	return printRankerWeights(stdout, root, weights)
}

func runRankerReset(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	opts := rankerNudgeOptions{Config: "config.json"}
	fs := flag.NewFlagSet("idiom ranker reset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Config, "config", opts.Config, "path to config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "ranker reset: unexpected positional argument %q\n", fs.Arg(0))
		return 2
	}
	cfg, err := loadToolConfig(opts.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root := strings.TrimSpace(cfg.Workspace.Root)
	if root == "" {
		fmt.Fprintln(stderr, "workspace root not configured; cannot reset ranker weights")
		return 1
	}
	if err := comprehension.SaveLearnedWeights(root, map[string]float64{}); err != nil {
		fmt.Fprintf(stderr, "ranker reset failed: %v\n", err)
		return 1
	}
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		Source: "cli",
		Action: "reset",
	}); err != nil {
		fmt.Fprintf(stderr, "ranker reset log append failed: %v\n", err)
		return 1
	}
	logCLIPhase(rec, "ranker_reset", nil)
	fmt.Fprintln(stdout, "ranker weights cleared")
	return 0
}

func runRankerShow(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	opts := struct {
		Config string
		JSON   bool
	}{Config: "config.json"}
	fs := flag.NewFlagSet("idiom ranker show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Config, "config", opts.Config, "path to config file")
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON instead of a text summary")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "ranker show: unexpected positional argument %q\n", fs.Arg(0))
		return 2
	}
	cfg, err := loadToolConfig(opts.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root := strings.TrimSpace(cfg.Workspace.Root)
	if root == "" {
		fmt.Fprintln(stderr, "workspace root not configured")
		return 1
	}
	weights, err := comprehension.LoadLearnedWeights(root)
	if err != nil {
		fmt.Fprintf(stderr, "ranker show failed: %v\n", err)
		return 1
	}
	logEntries, err := comprehension.ReadLearningLog(root)
	if err != nil {
		fmt.Fprintf(stderr, "ranker show log read failed: %v\n", err)
		return 1
	}
	summary := comprehension.SummarizeLearningLog(logEntries)
	logCLIPhase(rec, "ranker_show", map[string]any{"weight_count": len(weights), "log_entries": summary.TotalEntries})
	if opts.JSON {
		payload := map[string]any{
			"workspace_root": root,
			"weights":        weights,
			"log_summary":    summary,
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "ranker show encode failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	return printRankerWeights(stdout, root, weights)
}

func printRankerWeights(stdout io.Writer, root string, weights map[string]float64) int {
	fmt.Fprintf(stdout, "ranker weights at %s\n", filepath.Join(root, ".idiom", "ranker", "weights.json"))
	if len(weights) == 0 {
		fmt.Fprintln(stdout, "  (no entries — every file uses the baseline 1.0 multiplier)")
		return 0
	}
	keys := make([]string, 0, len(weights))
	for k := range weights {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool { return weights[keys[i]] > weights[keys[j]] })
	for _, key := range keys {
		fmt.Fprintf(stdout, "  %-50s %.3f\n", key, weights[key])
	}
	return 0
}

func uniqueRankerFiles(args []string) []string {
	seen := make(map[string]struct{}, len(args))
	out := make([]string, 0, len(args))
	for _, raw := range args {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
