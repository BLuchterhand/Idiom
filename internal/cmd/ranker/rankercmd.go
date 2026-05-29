package ranker
import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/telemetry"
	"idiom/internal/util"
)

type ConfigLoader func(path string) (config.Config, error)
type PhaseLogger func(rec *telemetry.Recorder, eventType string, fields map[string]any)

type Deps struct {
	LoadConfig ConfigLoader
	LogPhase   PhaseLogger
}

type nudgeOptions struct {
	Config string
	Note   string
}

func Run(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Deps) int {
	deps = deps.withDefaults()
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printUsage(stderr)
		return 0
	}
	deps.LogPhase(rec, "ranker_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "boost":
		return runNudge(args[1:], stdout, stderr, rec, deps, "boost")
	case "penalize":
		return runNudge(args[1:], stdout, stderr, rec, deps, "penalize")
	case "reset":
		return runReset(args[1:], stdout, stderr, rec, deps)
	case "rollback":
		return runRollback(args[1:], stdout, stderr, rec, deps)
	case "show":
		return runShow(args[1:], stdout, stderr, rec, deps)
	default:
		fmt.Fprintf(stderr, "unknown ranker command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func (deps Deps) withDefaults() Deps {
	if deps.LoadConfig == nil {
		deps.LoadConfig = loadConfig
	}
	if deps.LogPhase == nil {
		deps.LogPhase = func(*telemetry.Recorder, string, map[string]any) {}
	}
	return deps
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom ranker boost    [--config path] [--note text] <file> [<file> ...]")
	fmt.Fprintln(stderr, "  idiom ranker penalize [--config path] [--note text] <file> [<file> ...]")
	fmt.Fprintln(stderr, "  idiom ranker reset    [--config path]")
	fmt.Fprintln(stderr, "  idiom ranker rollback [--config path] --event event_id")
	fmt.Fprintln(stderr, "  idiom ranker show     [--config path] [--json]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Manually nudge per-file ranker weights stored under .idiom/ranker/weights.json.")
	fmt.Fprintln(stderr, "Boost and penalize each apply one decay-then-nudge cycle to weights.json.")
	fmt.Fprintln(stderr, "Reset clears weights.json; the learning_log.jsonl history is preserved.")
	fmt.Fprintln(stderr, "Rollback restores weights from a prior verified learning event log entry.")
	fmt.Fprintln(stderr, "Show prints current weights and recent learning-log entries.")
}

func runNudge(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Deps, action string) int {
	opts := nudgeOptions{Config: "config.json"}
	fs := flag.NewFlagSet("idiom ranker "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Config, "config", opts.Config, "path to config file")
	fs.StringVar(&opts.Note, "note", "", "optional note recorded in the learning log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := util.UniqueNonEmpty(fs.Args())
	if len(files) == 0 {
		fmt.Fprintf(stderr, "ranker %s: at least one file path is required\n", action)
		return 2
	}
	cfg, err := deps.LoadConfig(opts.Config)
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
	deps.LogPhase(rec, "ranker_"+action, map[string]any{"files": len(files)})
	return printWeights(stdout, root, weights)
}

func runReset(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Deps) int {
	opts := nudgeOptions{Config: "config.json"}
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
	cfg, err := deps.LoadConfig(opts.Config)
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
	deps.LogPhase(rec, "ranker_reset", nil)
	fmt.Fprintln(stdout, "ranker weights cleared")
	return 0
}

func runRollback(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Deps) int {
	opts := struct {
		Config  string
		EventID string
	}{Config: "config.json"}
	fs := flag.NewFlagSet("idiom ranker rollback", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Config, "config", opts.Config, "path to config file")
	fs.StringVar(&opts.EventID, "event", "", "learning event id to restore from")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "ranker rollback: unexpected positional argument %q\n", fs.Arg(0))
		return 2
	}
	if strings.TrimSpace(opts.EventID) == "" {
		fmt.Fprintln(stderr, "ranker rollback: --event is required")
		return 2
	}
	cfg, err := deps.LoadConfig(opts.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root := strings.TrimSpace(cfg.Workspace.Root)
	if root == "" {
		fmt.Fprintln(stderr, "workspace root not configured; cannot roll back ranker weights")
		return 1
	}
	weights, entry, err := comprehension.RestoreLearnedWeightsFromLearningLog(root, opts.EventID)
	if err != nil {
		fmt.Fprintf(stderr, "ranker rollback failed: %v\n", err)
		return 1
	}
	deps.LogPhase(rec, "ranker_rollback", map[string]any{"event_id": opts.EventID, "restore_event_id": entry.EventID})
	fmt.Fprintf(stdout, "ranker weights restored from learning event %s\n", opts.EventID)
	return printWeights(stdout, root, weights)
}

func runShow(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Deps) int {
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
	cfg, err := deps.LoadConfig(opts.Config)
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
	deps.LogPhase(rec, "ranker_show", map[string]any{"weight_count": len(weights), "log_entries": summary.TotalEntries})
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
	return printWeights(stdout, root, weights)
}

func printWeights(stdout io.Writer, root string, weights map[string]float64) int {
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

func loadConfig(path string) (config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := config.Default()
		if wd, e := os.Getwd(); e == nil {
			wd = config.CanonicalizeExistingPath(wd)
			cfg.Workspace.Root = wd
			cfg.Workspace.CWD = wd
		}
		return cfg, nil
	}
	return config.Load(path)
}
