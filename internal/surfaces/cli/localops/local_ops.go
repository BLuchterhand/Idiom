package localops

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/localops"
	"idiom/internal/runtime/readiness"
	"idiom/internal/surfaces/cli/config"
)

type BenchmarkOptions struct {
	ConfigPath        string
	Profile           string
	ReplayLog         string
	ReplayGlob        string
	OutputPath        string
	QuickstartSmoke   bool
	QuickstartTask    string
	QuickstartCleanup string
	JSON              bool
}

type localOpsBenchmarkOptions = BenchmarkOptions

type Dependencies struct {
	Version    string
	Commit     string
	LoadConfig func(string) (config.Config, error)
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, depOverrides ...Dependencies) int {
	deps := resolveDependencies(depOverrides...)
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printLocalOpsUsage(stderr)
		return 0
	}
	logLocalOpsPhase(rec, "local_ops_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "benchmark":
		return runLocalOpsBenchmark(args[1:], stdout, stderr, rec, deps)
	default:
		fmt.Fprintf(stderr, "local-ops: unknown subcommand %q\n", args[0])
		printLocalOpsUsage(stderr)
		return 2
	}
}

func runLocalOpsBenchmark(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Dependencies) int {
	opts := localOpsBenchmarkOptions{}
	fs := flag.NewFlagSet("idiom local-ops benchmark", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", ".idiom/config.json", "path to project config")
	fs.StringVar(&opts.Profile, "profile", "all", "readiness profile to benchmark: all, discovery, plan, implement, eval, or sidecar")
	fs.StringVar(&opts.ReplayLog, "replay-log", "", "single metadata-only conversation JSONL log to summarize")
	fs.StringVar(&opts.ReplayGlob, "replay-glob", "", "glob of metadata-only conversation JSONL logs to summarize")
	fs.StringVar(&opts.OutputPath, "output", "", "optional path for the JSON report")
	fs.BoolVar(&opts.QuickstartSmoke, "quickstart-smoke", false, "run deterministic CLI-only quickstart activation smoke evidence")
	fs.StringVar(&opts.QuickstartTask, "quickstart-task", "localize first useful Idiom quickstart packet", "task string for quickstart context_pack smoke")
	fs.StringVar(&opts.QuickstartCleanup, "quickstart-cleanup", "report-only", "quickstart cleanup mode: report-only, remove-smoke-artifacts, or remove-local-state")
	fs.BoolVar(&opts.JSON, "json", false, "emit structured JSON report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if opts.ReplayLog != "" && opts.ReplayGlob != "" {
		fmt.Fprintln(stderr, "local-ops benchmark: --replay-log and --replay-glob are mutually exclusive")
		return 2
	}

	report, err := buildLocalOpsBenchmarkReportWithDeps(opts, rec, deps)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if readiness.IsProfileSelectionError(err) {
			return 2
		}
		return 1
	}
	logLocalOpsBenchmarkTelemetry(rec, report)
	if opts.OutputPath != "" {
		if err := writeLocalOpsBenchmarkReport(opts.OutputPath, report); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if opts.JSON {
		return encodeLocalOpsBenchmarkJSON(stdout, report)
	}
	printLocalOpsBenchmarkText(stdout, report)
	return 0
}

func BuildBenchmarkReport(opts BenchmarkOptions, rec *telemetry.Recorder, depOverrides ...Dependencies) (localops.BenchmarkReport, error) {
	return buildLocalOpsBenchmarkReportWithDeps(opts, rec, resolveDependencies(depOverrides...))
}

func buildLocalOpsBenchmarkReport(opts localOpsBenchmarkOptions, rec *telemetry.Recorder) (localops.BenchmarkReport, error) {
	return buildLocalOpsBenchmarkReportWithDeps(opts, rec, resolveDependencies())
}

func buildLocalOpsBenchmarkReportWithDeps(opts BenchmarkOptions, rec *telemetry.Recorder, deps Dependencies) (localops.BenchmarkReport, error) {
	telemetryPath := ""
	if rec != nil {
		telemetryPath = rec.Path()
	}
	return localops.BuildBenchmarkReport(localops.BenchmarkOptions{
		ConfigPath:        opts.ConfigPath,
		Profile:           opts.Profile,
		ReplayLog:         opts.ReplayLog,
		ReplayGlob:        opts.ReplayGlob,
		OutputPath:        opts.OutputPath,
		TelemetryPath:     telemetryPath,
		QuickstartSmoke:   opts.QuickstartSmoke,
		QuickstartTask:    opts.QuickstartTask,
		QuickstartCleanup: opts.QuickstartCleanup,
		Version:           deps.Version,
		Commit:            deps.Commit,
	}, localops.QuickstartDeps{LoadConfig: deps.LoadConfig})
}

func logLocalOpsBenchmarkTelemetry(rec *telemetry.Recorder, report localops.BenchmarkReport) {
	logLocalOpsPhase(rec, "local_ops_benchmark", map[string]any{
		"schema_version":          report.SchemaVersion,
		"profile_count":           report.Summary.ProfileCount,
		"smoke_pass_rate":         report.Summary.SmokePassRate,
		"smoke_passed":            report.Summary.SmokePassed,
		"smoke_total":             report.Summary.SmokeTotal,
		"capacity_blocked":        report.Summary.CapacityBlocked,
		"replay_status":           report.Replay.Status,
		"replay_log_count":        report.Replay.LogCount,
		"blocker_count":           report.Summary.BlockerCount,
		"metadata_only_contract":  true,
		"contains_prompt_content": false,
		"contains_file_bodies":    false,
	})
}

func writeLocalOpsBenchmarkReport(path string, report localops.BenchmarkReport) error {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal local-ops benchmark report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write local-ops benchmark report: %w", err)
	}
	return nil
}

func encodeLocalOpsBenchmarkJSON(stdout io.Writer, report localops.BenchmarkReport) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return 1
	}
	return 0
}

func printLocalOpsBenchmarkText(stdout io.Writer, report localops.BenchmarkReport) {
	fmt.Fprintf(stdout, "local-ops benchmark: profiles=%d smoke_pass_rate=%.2f replay=%s logs=%d blockers=%d\n", report.Summary.ProfileCount, report.Summary.SmokePassRate, report.Replay.Status, report.Replay.LogCount, report.Summary.BlockerCount)
	for _, profile := range report.Profiles {
		fmt.Fprintf(stdout, "- %s: smoke=%s capacity=%s state=%s\n", profile.Name, profile.Smoke.Status, profile.Capacity.Status, profile.State)
	}
	if len(report.Blockers) > 0 {
		fmt.Fprintln(stdout, "blockers:")
		for _, blocker := range report.Blockers {
			fmt.Fprintf(stdout, "- %s: %s\n", blocker.Scope, blocker.Reason)
		}
	}
}

func printLocalOpsUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: idiom local-ops <subcommand> [flags]")
	fmt.Fprintln(stderr, "subcommands:")
	fmt.Fprintln(stderr, "  benchmark          emit local smoke, capacity, and replay benchmark evidence")
}

func resolveDependencies(overrides ...Dependencies) Dependencies {
	deps := Dependencies{LoadConfig: cmdconfig.Load}
	if len(overrides) > 0 {
		merged := overrides[0]
		deps.Version = merged.Version
		deps.Commit = merged.Commit
		if merged.LoadConfig != nil {
			deps.LoadConfig = merged.LoadConfig
		}
	}
	return deps
}

func logLocalOpsPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	if rec == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	_ = rec.Log(eventType, fields)
}
