package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/eval"
	"idiom/internal/telemetry"
)

const localOpsBenchmarkSchemaVersion = "idiom.local_ops_benchmark.v1"

type localOpsBenchmarkReport struct {
	SchemaVersion string                   `json:"schema_version"`
	GeneratedAt   string                   `json:"generated_at"`
	Command       string                   `json:"command"`
	ConfigPath    string                   `json:"config_path"`
	Profiles      []localOpsProfileReport  `json:"profiles"`
	Summary       localOpsBenchmarkSummary `json:"summary"`
	Replay        localOpsReplayReport     `json:"replay"`
	Privacy       localOpsPrivacyReport    `json:"privacy"`
	Telemetry     *localOpsTelemetryReport `json:"telemetry,omitempty"`
	Blockers      []localOpsBlocker        `json:"blockers,omitempty"`
}

type localOpsProfileReport struct {
	Name      string                   `json:"name"`
	State     string                   `json:"state"`
	Smoke     localOpsSmokeReport      `json:"smoke"`
	Capacity  localOpsCapacityReport   `json:"capacity"`
	Readiness readinessProfileEvidence `json:"readiness"`
	Blockers  []localOpsBlocker        `json:"blockers,omitempty"`
}

type readinessProfileEvidence struct {
	Enforcement          string            `json:"enforcement"`
	RequiredForLaunch    bool              `json:"required_for_launch"`
	TaskFamily           string            `json:"task_family"`
	Contract             string            `json:"contract"`
	PermissionProfile    string            `json:"permission_profile,omitempty"`
	RoleAliases          map[string]string `json:"role_aliases,omitempty"`
	HardFailures         int               `json:"hard_failures"`
	SoftDegradations     int               `json:"soft_degradations"`
	UnavailableChecks    int               `json:"unavailable_checks"`
	BackendState         string            `json:"backend_state,omitempty"`
	QueueState           string            `json:"queue_state,omitempty"`
	ContextBudgetState   string            `json:"context_budget_state,omitempty"`
	RouteClassifierState string            `json:"route_classifier_state,omitempty"`
}

type localOpsSmokeReport struct {
	Status       string            `json:"status"`
	Passed       int               `json:"passed"`
	Total        int               `json:"total"`
	PassRate     float64           `json:"pass_rate"`
	Signals      map[string]string `json:"signals,omitempty"`
	Unsupported  []localOpsBlocker `json:"unsupported,omitempty"`
	Degradations []localOpsBlocker `json:"degradations,omitempty"`
}

type localOpsCapacityReport struct {
	Status             string            `json:"status"`
	ContextTokens      int               `json:"context_tokens,omitempty"`
	MaxOutputTokens    int               `json:"max_output_tokens,omitempty"`
	PromptBudgetTokens int               `json:"prompt_budget_tokens,omitempty"`
	QueuePools         int               `json:"queue_pools,omitempty"`
	QueueInFlight      int               `json:"queue_in_flight,omitempty"`
	QueueWaiting       int               `json:"queue_waiting,omitempty"`
	QueueIdle          int               `json:"queue_idle,omitempty"`
	Limits             []localOpsLimit   `json:"limits,omitempty"`
	Blockers           []localOpsBlocker `json:"blockers,omitempty"`
}

type localOpsLimit struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Value  int    `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type localOpsReplayReport struct {
	Status    string                         `json:"status"`
	LogCount  int                            `json:"log_count"`
	LogIDs    []string                       `json:"log_ids,omitempty"`
	Aggregate *localOpsReplayAggregateReport `json:"aggregate,omitempty"`
	Signals   localOpsReplaySignals          `json:"signals,omitempty"`
	Blockers  []localOpsBlocker              `json:"blockers,omitempty"`
}

type localOpsReplaySignals struct {
	ToolCalls            int `json:"tool_calls,omitempty"`
	RepeatedToolSignals  int `json:"repeated_tool_signals,omitempty"`
	MalformedToolSignals int `json:"malformed_tool_signals,omitempty"`
	BudgetExhausted      int `json:"budget_exhausted,omitempty"`
	QueueDegraded        int `json:"queue_degraded,omitempty"`
	BackendEvents        int `json:"backend_events,omitempty"`
}

type localOpsReplayAggregateReport struct {
	LogCount             int                     `json:"log_count"`
	MedianToolResults    int                     `json:"median_tool_results"`
	MaxToolResults       int                     `json:"max_tool_results"`
	MedianPromptTokens   int                     `json:"median_prompt_tokens"`
	MaxPromptTokens      int                     `json:"max_prompt_tokens"`
	MedianDurationMillis int                     `json:"median_duration_millis"`
	TotalRejectedActions int                     `json:"total_rejected_actions"`
	PatternHitRate       []eval.PerfPatternHit   `json:"pattern_hit_rate,omitempty"`
	ToolMix              []eval.PerfToolMix      `json:"tool_mix,omitempty"`
	Context              *eval.ContextStats      `json:"context,omitempty"`
	Feedback             *eval.FeedbackStats     `json:"feedback,omitempty"`
	LocalOps             *eval.LocalOpsStats     `json:"local_ops,omitempty"`
	StopReasonCounts     map[string]int          `json:"stop_reason_counts,omitempty"`
	WorstOffenders       []localOpsWorstOffender `json:"worst_offenders,omitempty"`
}

type localOpsWorstOffender struct {
	LogID              string `json:"log_id"`
	AntiPatternScore   int    `json:"anti_pattern_score"`
	ToolResults        int    `json:"tool_results"`
	LogExcerptRedacted bool   `json:"log_excerpt_redacted,omitempty"`
}

type localOpsBenchmarkSummary struct {
	ProfileCount     int     `json:"profile_count"`
	SmokePassRate    float64 `json:"smoke_pass_rate"`
	SmokePassed      int     `json:"smoke_passed"`
	SmokeTotal       int     `json:"smoke_total"`
	CapacityBlocked  int     `json:"capacity_blocked"`
	ReplayLogCount   int     `json:"replay_log_count"`
	BlockerCount     int     `json:"blocker_count"`
	DegradationCount int     `json:"degradation_count"`
}

type localOpsPrivacyReport struct {
	MetadataOnly           bool     `json:"metadata_only"`
	RawPromptCaptured      bool     `json:"raw_prompt_captured"`
	RawFileBodyCaptured    bool     `json:"raw_file_body_captured"`
	SecretCaptureAllowed   bool     `json:"secret_capture_allowed"`
	RedactedFields         []string `json:"redacted_fields"`
	LocalArtifactDirectory string   `json:"local_artifact_directory,omitempty"`
}

type localOpsTelemetryReport struct {
	Path string `json:"path,omitempty"`
	ID   string `json:"id,omitempty"`
}

type localOpsBlocker struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
	Remedy string `json:"remedy,omitempty"`
}

type localOpsBenchmarkOptions struct {
	ConfigPath string
	Profile    string
	ReplayLog  string
	ReplayGlob string
	OutputPath string
	JSON       bool
}

func runLocalOpsMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printLocalOpsUsage(stderr)
		return 0
	}
	logCLIPhase(rec, "local_ops_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "benchmark":
		return runLocalOpsBenchmark(args[1:], stdout, stderr, rec)
	default:
		fmt.Fprintf(stderr, "local-ops: unknown subcommand %q\n", args[0])
		printLocalOpsUsage(stderr)
		return 2
	}
}

func runLocalOpsBenchmark(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	opts := localOpsBenchmarkOptions{}
	fs := flag.NewFlagSet("idiom local-ops benchmark", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", ".idiom/config.json", "path to project config")
	fs.StringVar(&opts.Profile, "profile", "all", "readiness profile to benchmark: all, discovery, plan, implement, eval, or sidecar")
	fs.StringVar(&opts.ReplayLog, "replay-log", "", "single metadata-only conversation JSONL log to summarize")
	fs.StringVar(&opts.ReplayGlob, "replay-glob", "", "glob of metadata-only conversation JSONL logs to summarize")
	fs.StringVar(&opts.OutputPath, "output", "", "optional path for the JSON report")
	fs.BoolVar(&opts.JSON, "json", false, "emit structured JSON report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if opts.ReplayLog != "" && opts.ReplayGlob != "" {
		fmt.Fprintln(stderr, "local-ops benchmark: --replay-log and --replay-glob are mutually exclusive")
		return 2
	}

	report, err := buildLocalOpsBenchmarkReport(opts, rec)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if isReadinessProfileSelectionError(err) {
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

func buildLocalOpsBenchmarkReport(opts localOpsBenchmarkOptions, rec *telemetry.Recorder) (localOpsBenchmarkReport, error) {
	profiles, err := buildDoctorReadinessProfiles(opts.ConfigPath, opts.Profile, nil)
	if err != nil {
		return localOpsBenchmarkReport{}, err
	}
	report := localOpsBenchmarkReport{
		SchemaVersion: localOpsBenchmarkSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Command:       "idiom local-ops benchmark",
		ConfigPath:    filepath.Clean(opts.ConfigPath),
		Privacy: localOpsPrivacyReport{
			MetadataOnly:         true,
			RawPromptCaptured:    false,
			RawFileBodyCaptured:  false,
			SecretCaptureAllowed: false,
			RedactedFields:       []string{"user_prompt", "assistant_content", "tool_raw_args", "file_body", "secret_value"},
		},
	}
	if rec != nil && strings.TrimSpace(rec.Path()) != "" {
		report.Telemetry = &localOpsTelemetryReport{Path: filepath.Clean(rec.Path()), ID: localOpsStableID(rec.Path())}
	}
	if strings.TrimSpace(opts.OutputPath) != "" {
		report.Privacy.LocalArtifactDirectory = filepath.Dir(filepath.Clean(opts.OutputPath))
	}
	for _, profile := range profiles {
		report.Profiles = append(report.Profiles, buildLocalOpsProfileReport(profile))
	}
	replay, err := buildLocalOpsReplayReport(opts.ReplayLog, opts.ReplayGlob)
	if err != nil {
		return localOpsBenchmarkReport{}, err
	}
	report.Replay = replay
	report.Blockers = collectLocalOpsBlockers(report.Profiles, replay)
	report.Summary = summarizeLocalOpsBenchmark(report)
	return report, nil
}

func buildLocalOpsProfileReport(profile readinessProfileReport) localOpsProfileReport {
	out := localOpsProfileReport{
		Name:  profile.Name,
		State: profile.State,
		Readiness: readinessProfileEvidence{
			Enforcement:          profile.Enforcement,
			RequiredForLaunch:    profile.RequiredForLaunch,
			TaskFamily:           profile.TaskFamily,
			Contract:             profile.Contract,
			PermissionProfile:    profile.PermissionProfile,
			RoleAliases:          profile.RoleAliases,
			HardFailures:         len(profile.HardFailures),
			SoftDegradations:     len(profile.SoftDegradations),
			UnavailableChecks:    len(profile.Unavailable),
			BackendState:         profile.Metrics.BackendState,
			QueueState:           profile.Metrics.QueueState,
			ContextBudgetState:   profile.Metrics.ContextBudgetState,
			RouteClassifierState: profile.Metrics.ClassifierState,
		},
	}
	out.Smoke = buildLocalOpsSmokeReport(profile)
	out.Capacity = buildLocalOpsCapacityReport(profile)
	out.Blockers = append(out.Blockers, out.Smoke.Unsupported...)
	out.Blockers = append(out.Blockers, out.Capacity.Blockers...)
	return out
}

func buildLocalOpsSmokeReport(profile readinessProfileReport) localOpsSmokeReport {
	total := len(profile.Gates)
	passed := 0
	smoke := localOpsSmokeReport{
		Status:  profile.State,
		Total:   total,
		Signals: map[string]string{},
	}
	for _, gate := range profile.Gates {
		smoke.Signals[gate.Name] = gate.Status
		if gate.Status == "pass" {
			passed++
			continue
		}
		blocker := localOpsBlocker{Scope: profile.Name + "/" + gate.Name, Reason: gate.Reason, Remedy: gate.Remedy}
		if gate.Status == "blocked" {
			smoke.Unsupported = append(smoke.Unsupported, blocker)
		} else {
			smoke.Degradations = append(smoke.Degradations, blocker)
		}
	}
	smoke.Passed = passed
	if total > 0 {
		smoke.PassRate = float64(passed) / float64(total)
	}
	if len(smoke.Unsupported) > 0 {
		smoke.Status = "blocked"
	} else if len(smoke.Degradations) > 0 {
		smoke.Status = "degraded"
	} else {
		smoke.Status = "pass"
	}
	return smoke
}

func buildLocalOpsCapacityReport(profile readinessProfileReport) localOpsCapacityReport {
	capacity := localOpsCapacityReport{
		Status:             "pass",
		ContextTokens:      profile.Metrics.ContextTokens,
		MaxOutputTokens:    profile.Metrics.MaxOutputTokens,
		PromptBudgetTokens: profile.Metrics.ContextTokens - profile.Metrics.MaxOutputTokens,
		QueuePools:         profile.Metrics.QueuePools,
		QueueInFlight:      profile.Metrics.QueueInFlight,
		QueueWaiting:       profile.Metrics.QueueWaiting,
		QueueIdle:          profile.Metrics.QueueIdle,
	}
	capacity.Limits = append(capacity.Limits,
		localOpsLimit{Name: "context_tokens", State: profile.Metrics.ContextBudgetState, Value: profile.Metrics.ContextTokens},
		localOpsLimit{Name: "max_output_tokens", State: profile.Metrics.ContextBudgetState, Value: profile.Metrics.MaxOutputTokens},
		localOpsLimit{Name: "queue_pools", State: profile.Metrics.QueueState, Value: profile.Metrics.QueuePools},
	)
	if capacity.PromptBudgetTokens < 0 {
		capacity.PromptBudgetTokens = 0
	}
	if profile.Metrics.ContextBudgetState != "pass" {
		capacity.Status = "blocked"
		capacity.Blockers = append(capacity.Blockers, localOpsBlocker{
			Scope:  profile.Name + "/context_budget",
			Reason: "context budget is " + firstNonEmptyCLIString(profile.Metrics.ContextBudgetState, "unavailable"),
			Remedy: localOpsRemedyForCheck(profile.Checks, "context budget"),
		})
	}
	if profile.Metrics.QueueState != "pass" {
		if capacity.Status != "blocked" {
			capacity.Status = "degraded"
		}
		capacity.Blockers = append(capacity.Blockers, localOpsBlocker{
			Scope:  profile.Name + "/queue_capacity",
			Reason: "queue capacity evidence is " + firstNonEmptyCLIString(profile.Metrics.QueueState, "unavailable"),
			Remedy: localOpsRemedyForCheck(profile.Checks, "queue admission"),
		})
	}
	if profile.Metrics.ContextTokens <= 0 {
		capacity.Limits = append(capacity.Limits, localOpsLimit{Name: "context_sweep", State: "unsupported", Reason: "configured context window unavailable"})
	} else {
		capacity.Limits = append(capacity.Limits, localOpsLimit{Name: "context_sweep", State: "bounded", Value: profile.Metrics.ContextTokens, Reason: "default harness smoke records configured limit; live sweeps require explicit backend runs"})
	}
	return capacity
}

func buildLocalOpsReplayReport(logPath string, logsGlob string) (localOpsReplayReport, error) {
	paths, err := localOpsReplayPaths(logPath, logsGlob)
	if err != nil {
		return localOpsReplayReport{}, err
	}
	if len(paths) == 0 {
		return localOpsReplayReport{
			Status: "unsupported",
			Blockers: []localOpsBlocker{{
				Scope:  "replay",
				Reason: "no replay logs supplied",
				Remedy: "pass --replay-log logs/conversations/<session>.jsonl or --replay-glob for metadata-only replay signals",
			}},
		}, nil
	}
	reports := make([]eval.PerfReport, 0, len(paths))
	replay := localOpsReplayReport{Status: "pass"}
	for _, path := range paths {
		report, err := eval.AnalyzeLog(path)
		if err != nil {
			replay.Status = "degraded"
			replay.Blockers = append(replay.Blockers, localOpsBlocker{
				Scope:  "replay/log_" + localOpsStableID(path),
				Reason: localOpsReplayFailureReason(err),
				Remedy: "provide a readable, parseable JSONL conversation or telemetry log",
			})
			continue
		}
		report.SessionID = localOpsStableID(path)
		reports = append(reports, report)
		replay.LogIDs = append(replay.LogIDs, localOpsStableID(path))
		replay.Signals.ToolCalls += report.Tools.Total
		replay.Signals.RepeatedToolSignals += antiPatternCount(report, "repeated")
		replay.Signals.MalformedToolSignals += antiPatternCount(report, "malformed")
		if strings.Contains(strings.ToLower(report.StopReason), "budget") {
			replay.Signals.BudgetExhausted++
		}
		if report.LocalOps != nil {
			replay.Signals.QueueDegraded += report.LocalOps.QueueDegraded
			replay.Signals.BackendEvents += report.LocalOps.BackendEvents
		}
	}
	if len(reports) == 0 {
		replay.Status = "blocked"
		replay.Blockers = append(replay.Blockers, localOpsBlocker{Scope: "replay", Reason: "no replay logs could be analyzed"})
		return replay, nil
	}
	aggregate := eval.Aggregate(reports)
	replay.Aggregate = sanitizeLocalOpsReplayAggregate(aggregate)
	replay.LogCount = len(reports)
	sort.Strings(replay.LogIDs)
	return replay, nil
}

func sanitizeLocalOpsReplayAggregate(aggregate eval.PerfAggregate) *localOpsReplayAggregateReport {
	safe := &localOpsReplayAggregateReport{
		LogCount:             aggregate.LogCount,
		MedianToolResults:    aggregate.MedianToolResults,
		MaxToolResults:       aggregate.MaxToolResults,
		MedianPromptTokens:   aggregate.MedianPromptTokens,
		MaxPromptTokens:      aggregate.MaxPromptTokens,
		MedianDurationMillis: aggregate.MedianDurationMillis,
		TotalRejectedActions: aggregate.TotalRejectedActions,
		PatternHitRate:       aggregate.PatternHitRate,
		ToolMix:              aggregate.ToolMix,
		Context:              aggregate.Context,
		Feedback:             aggregate.Feedback,
		LocalOps:             aggregate.LocalOps,
		StopReasonCounts:     aggregate.StopReasonCounts,
	}
	for _, offender := range aggregate.WorstOffenders {
		logID := strings.TrimSpace(offender.SessionID)
		if logID == "" {
			logID = localOpsStableID(offender.LogPath)
		}
		safe.WorstOffenders = append(safe.WorstOffenders, localOpsWorstOffender{
			LogID:              logID,
			AntiPatternScore:   offender.AntiPatternScore,
			ToolResults:        offender.ToolResults,
			LogExcerptRedacted: offender.LogExcerptRedacted,
		})
	}
	return safe
}

func localOpsReplayFailureReason(err error) string {
	if err == nil {
		return "replay_log_unavailable"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "open log"), strings.Contains(message, "permission"), strings.Contains(message, "no such file"):
		return "replay_log_unreadable"
	case strings.Contains(message, "decode log record"), strings.Contains(message, "scan log"):
		return "replay_log_unparseable"
	default:
		return "replay_log_unavailable"
	}
}

func localOpsReplayPaths(logPath string, logsGlob string) ([]string, error) {
	if strings.TrimSpace(logPath) != "" {
		return []string{filepath.Clean(logPath)}, nil
	}
	if strings.TrimSpace(logsGlob) == "" {
		return nil, nil
	}
	paths, err := filepath.Glob(logsGlob)
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func antiPatternCount(report eval.PerfReport, needle string) int {
	needle = strings.ToLower(strings.TrimSpace(needle))
	total := 0
	for _, pattern := range report.AntiPatterns {
		if strings.Contains(strings.ToLower(pattern.Name), needle) {
			total += pattern.Count
		}
	}
	return total
}

func localOpsRemedyForCheck(checks []readinessCheck, name string) string {
	for _, check := range checks {
		if check.Name == name {
			return check.Remedy
		}
	}
	return ""
}

func collectLocalOpsBlockers(profiles []localOpsProfileReport, replay localOpsReplayReport) []localOpsBlocker {
	var blockers []localOpsBlocker
	for _, profile := range profiles {
		blockers = append(blockers, profile.Blockers...)
	}
	blockers = append(blockers, replay.Blockers...)
	return blockers
}

func summarizeLocalOpsBenchmark(report localOpsBenchmarkReport) localOpsBenchmarkSummary {
	summary := localOpsBenchmarkSummary{ProfileCount: len(report.Profiles), ReplayLogCount: report.Replay.LogCount, BlockerCount: len(report.Blockers)}
	for _, profile := range report.Profiles {
		summary.SmokePassed += profile.Smoke.Passed
		summary.SmokeTotal += profile.Smoke.Total
		summary.DegradationCount += len(profile.Smoke.Degradations)
		if profile.Capacity.Status == "blocked" {
			summary.CapacityBlocked++
		}
	}
	if summary.SmokeTotal > 0 {
		summary.SmokePassRate = float64(summary.SmokePassed) / float64(summary.SmokeTotal)
	}
	return summary
}

func logLocalOpsBenchmarkTelemetry(rec *telemetry.Recorder, report localOpsBenchmarkReport) {
	logCLIPhase(rec, "local_ops_benchmark", map[string]any{
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

func writeLocalOpsBenchmarkReport(path string, report localOpsBenchmarkReport) error {
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

func encodeLocalOpsBenchmarkJSON(stdout io.Writer, report localOpsBenchmarkReport) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return 1
	}
	return 0
}

func printLocalOpsBenchmarkText(stdout io.Writer, report localOpsBenchmarkReport) {
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

func localOpsStableID(value string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(value)))
	return hex.EncodeToString(sum[:8])
}
