package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"idiom/internal/eval"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
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
	Quickstart    *localOpsQuickstartSmoke `json:"quickstart_smoke,omitempty"`
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

type localOpsQuickstartSmoke struct {
	SmokeID                    string                              `json:"smoke_id"`
	Status                     string                              `json:"status"`
	ClientClassification       string                              `json:"client_classification"`
	EnvironmentClass           string                              `json:"environment_class"`
	AssistanceClass            string                              `json:"assistance_class"`
	DurationSource             string                              `json:"duration_source"`
	TimeToFirstPacketSeconds   float64                             `json:"time_to_first_packet_seconds"`
	TimeToTrustEvidenceSeconds float64                             `json:"time_to_trust_evidence_seconds"`
	SetupInstallReadiness      localOpsActivationSetupEvidence     `json:"setup_install_readiness"`
	FirstUsefulPacket          localOpsActivationPacketEvidence    `json:"first_useful_packet"`
	FreshnessCheck             localOpsActivationFreshnessEvidence `json:"freshness_check"`
	AuditReport                localOpsActivationAuditEvidence     `json:"audit_report"`
	SuggestedVerification      localOpsActivationVerification      `json:"suggested_verification"`
	ProductEvidenceSafeOutput  localOpsActivationProductEvidence   `json:"product_evidence_safe_output"`
	FailureReason              localOpsActivationFailureReason     `json:"failure_reason"`
	DocsGap                    localOpsActivationDocsGap           `json:"docs_gap"`
	PrivacyRedactionStatus     localOpsActivationPrivacyEvidence   `json:"privacy_redaction_status"`
	Cleanup                    localOpsActivationCleanupEvidence   `json:"cleanup"`
	Commands                   []localOpsActivationCommand         `json:"commands"`
}

type localOpsActivationCommand struct {
	CommandID      string  `json:"command_id"`
	Label          string  `json:"label"`
	ExitStatus     string  `json:"exit_status"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	ErrorClass     string  `json:"error_class,omitempty"`
}

type localOpsActivationSetupEvidence struct {
	Status              string `json:"status"`
	CommandID           string `json:"command_id"`
	InstallSetupPath    string `json:"install_setup_path"`
	BinaryVersion       string `json:"binary_version"`
	GitCommit           string `json:"git_commit,omitempty"`
	OSArch              string `json:"os_arch"`
	ReleaseInstallProof string `json:"release_install_proof"`
	ReadinessProfile    string `json:"readiness_profile"`
}

type localOpsActivationPacketEvidence struct {
	Status               string                         `json:"status"`
	CommandID            string                         `json:"command_id"`
	Tool                 string                         `json:"tool"`
	Task                 localOpsActivationTaskEvidence `json:"task"`
	PacketID             string                         `json:"packet_id,omitempty"`
	IndexID              string                         `json:"index_id,omitempty"`
	FreshnessState       string                         `json:"freshness_state,omitempty"`
	CandidateFileCount   int                            `json:"candidate_file_count"`
	CandidateSymbolCount int                            `json:"candidate_symbol_count"`
	CandidateTestCount   int                            `json:"candidate_test_count"`
	UsefulEvidence       []string                       `json:"useful_evidence,omitempty"`
	BlockerReason        string                         `json:"blocker_reason,omitempty"`
}

type localOpsActivationTaskEvidence struct {
	TaskID          string   `json:"task_id"`
	EvidenceType    string   `json:"evidence_type"`
	RedactionStatus string   `json:"redaction_status"`
	RawTaskCaptured bool     `json:"raw_task_captured"`
	ContentClasses  []string `json:"content_classes,omitempty"`
}

type localOpsActivationFreshnessEvidence struct {
	Status         string `json:"status"`
	CommandID      string `json:"command_id"`
	Tool           string `json:"tool"`
	IndexID        string `json:"index_id,omitempty"`
	State          string `json:"state,omitempty"`
	FreshForScope  bool   `json:"fresh_for_scope"`
	GlobalFresh    bool   `json:"global_fresh"`
	StalePathCount int    `json:"stale_path_count"`
	BlockerReason  string `json:"blocker_reason,omitempty"`
}

type localOpsActivationAuditEvidence struct {
	Status             string   `json:"status"`
	CommandID          string   `json:"command_id"`
	Tool               string   `json:"tool"`
	Purpose            string   `json:"purpose"`
	AuditReportID      string   `json:"audit_report_id,omitempty"`
	AuditEventIDs      []string `json:"audit_event_ids,omitempty"`
	HighFindingCount   int      `json:"high_finding_count"`
	MediumFindingCount int      `json:"medium_finding_count"`
	FindingCount       int      `json:"finding_count"`
	BlockerReason      string   `json:"blocker_reason,omitempty"`
}

type localOpsActivationVerification struct {
	Status            string   `json:"status"`
	CommandID         string   `json:"command_id"`
	SuggestedLabels   []string `json:"suggested_labels,omitempty"`
	SuggestedCommands []string `json:"suggested_commands,omitempty"`
	WasRun            bool     `json:"was_run"`
	NotRunReason      string   `json:"not_run_reason,omitempty"`
	FailureReason     string   `json:"failure_reason,omitempty"`
}

type localOpsActivationProductEvidence struct {
	Status                 string   `json:"status"`
	ArtifactHash           string   `json:"artifact_hash,omitempty"`
	ExportID               string   `json:"export_id,omitempty"`
	FieldList              []string `json:"field_list,omitempty"`
	ForbiddenFieldCheck    string   `json:"forbidden_field_check"`
	RedactionStatus        string   `json:"redaction_status"`
	P7_11Linkage           string   `json:"p7_11_linkage"`
	P9_6Linkage            string   `json:"p9_6_linkage"`
	HostedTelemetryDefault bool     `json:"hosted_telemetry_default"`
}

type localOpsActivationFailureReason struct {
	Status       string `json:"status"`
	PrimaryClass string `json:"primary_class"`
	Reason       string `json:"reason,omitempty"`
	RetryAllowed bool   `json:"retry_allowed"`
}

type localOpsActivationDocsGap struct {
	Status            string `json:"status"`
	GapRecorded       bool   `json:"gap_recorded"`
	DocPath           string `json:"doc_path,omitempty"`
	CommandLabel      string `json:"command_label,omitempty"`
	ExpectedOutput    string `json:"expected_output,omitempty"`
	ActualOutput      string `json:"actual_output,omitempty"`
	Severity          string `json:"severity,omitempty"`
	BlockedActivation bool   `json:"blocked_activation,omitempty"`
}

type localOpsActivationPrivacyEvidence struct {
	Status                  string   `json:"status"`
	RedactionReviewer       string   `json:"redaction_reviewer"`
	ForbiddenFieldCheck     string   `json:"forbidden_field_check"`
	RawPromptCaptured       bool     `json:"raw_prompt_captured"`
	RawFileBodyCaptured     bool     `json:"raw_file_body_captured"`
	SecretCaptured          bool     `json:"secret_captured"`
	PrivatePathCaptured     bool     `json:"private_path_captured"`
	PrivateRepoURLCaptured  bool     `json:"private_repo_url_captured"`
	UnredactedLogCaptured   bool     `json:"unredacted_log_captured"`
	HostedTelemetryRequired bool     `json:"hosted_telemetry_required"`
	RedactedFields          []string `json:"redacted_fields,omitempty"`
}

type localOpsActivationCleanupEvidence struct {
	Status               string   `json:"status"`
	CommandID            string   `json:"command_id"`
	Mode                 string   `json:"mode"`
	ExitStatus           string   `json:"exit_status"`
	RemovedArtifactCount int      `json:"removed_artifact_count"`
	RetainedStatePolicy  string   `json:"retained_state_policy,omitempty"`
	RemainingState       string   `json:"remaining_state"`
	EvidenceIDs          []string `json:"evidence_ids,omitempty"`
	BlockerReason        string   `json:"blocker_reason,omitempty"`
}

type localOpsBlocker struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
	Remedy string `json:"remedy,omitempty"`
}

type localOpsBenchmarkOptions struct {
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
		ConfigPath:    localOpsPathEvidence(opts.ConfigPath),
		Privacy: localOpsPrivacyReport{
			MetadataOnly:         true,
			RawPromptCaptured:    false,
			RawFileBodyCaptured:  false,
			SecretCaptureAllowed: false,
			RedactedFields:       []string{"user_prompt", "assistant_content", "tool_raw_args", "file_body", "secret_value"},
		},
	}
	if rec != nil && strings.TrimSpace(rec.Path()) != "" {
		report.Telemetry = &localOpsTelemetryReport{Path: localOpsPathEvidence(rec.Path()), ID: localOpsStableID(rec.Path())}
	}
	if strings.TrimSpace(opts.OutputPath) != "" {
		report.Privacy.LocalArtifactDirectory = localOpsPathEvidence(filepath.Dir(filepath.Clean(opts.OutputPath)))
	}
	for _, profile := range profiles {
		report.Profiles = append(report.Profiles, buildLocalOpsProfileReport(profile))
	}
	replay, err := buildLocalOpsReplayReport(opts.ReplayLog, opts.ReplayGlob)
	if err != nil {
		return localOpsBenchmarkReport{}, err
	}
	report.Replay = replay
	if opts.QuickstartSmoke {
		quickstart := buildLocalOpsQuickstartSmokeReport(opts)
		report.Quickstart = &quickstart
		finalizeLocalOpsQuickstartProductEvidence(report.Quickstart)
	}
	report.Blockers = collectLocalOpsBlockers(report.Profiles, replay)
	if report.Quickstart != nil && report.Quickstart.Status != "pass" {
		report.Blockers = append(report.Blockers, localOpsBlocker{
			Scope:  "quickstart_smoke",
			Reason: firstNonEmptyCLIString(report.Quickstart.FailureReason.Reason, report.Quickstart.Status),
		})
	}
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
		blocker := localOpsBlocker{Scope: profile.Name + "/" + gate.Name, Reason: localOpsSafeText(gate.Reason), Remedy: localOpsSafeText(gate.Remedy)}
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
			Reason: localOpsSafeText("context budget is " + firstNonEmptyCLIString(profile.Metrics.ContextBudgetState, "unavailable")),
			Remedy: localOpsSafeText(localOpsRemedyForCheck(profile.Checks, "context budget")),
		})
	}
	if profile.Metrics.QueueState != "pass" {
		if capacity.Status != "blocked" {
			capacity.Status = "degraded"
		}
		capacity.Blockers = append(capacity.Blockers, localOpsBlocker{
			Scope:  profile.Name + "/queue_capacity",
			Reason: localOpsSafeText("queue capacity evidence is " + firstNonEmptyCLIString(profile.Metrics.QueueState, "unavailable")),
			Remedy: localOpsSafeText(localOpsRemedyForCheck(profile.Checks, "queue admission")),
		})
	}
	if profile.Metrics.ContextTokens <= 0 {
		capacity.Limits = append(capacity.Limits, localOpsLimit{Name: "context_sweep", State: "unsupported", Reason: "configured context window unavailable"})
	} else {
		capacity.Limits = append(capacity.Limits, localOpsLimit{Name: "context_sweep", State: "bounded", Value: profile.Metrics.ContextTokens, Reason: "default harness smoke records configured limit; live sweeps require explicit backend runs"})
	}
	return capacity
}

func buildLocalOpsQuickstartSmokeReport(opts localOpsBenchmarkOptions) localOpsQuickstartSmoke {
	task := strings.TrimSpace(opts.QuickstartTask)
	if task == "" {
		task = "localize first useful Idiom quickstart packet"
	}
	smoke := localOpsQuickstartSmoke{
		SmokeID:              localOpsStableID(strings.Join([]string{"p7.7", task, opts.QuickstartCleanup}, "|")),
		Status:               "pass",
		ClientClassification: "open-source",
		EnvironmentClass:     "source-build",
		AssistanceClass:      "none",
		DurationSource:       "deterministic_command_step_seconds",
		SetupInstallReadiness: localOpsActivationSetupEvidence{
			Status:              "pass",
			CommandID:           "p77-setup-readiness",
			InstallSetupPath:    "source-build-or-local-binary",
			BinaryVersion:       version,
			GitCommit:           commit,
			OSArch:              runtime.GOOS + "/" + runtime.GOARCH,
			ReleaseInstallProof: "not_claimed",
			ReadinessProfile:    firstNonEmptyCLIString(opts.Profile, "all"),
		},
		SuggestedVerification: localOpsActivationVerification{
			Status:       "pass",
			CommandID:    "p77-suggested-verification",
			WasRun:       false,
			NotRunReason: "quickstart smoke records suggested verification; execution belongs to the caller or release gate",
		},
		ProductEvidenceSafeOutput: localOpsActivationProductEvidence{
			Status:                 "pending",
			ForbiddenFieldCheck:    "pending",
			RedactionStatus:        "pending",
			P7_11Linkage:           "deferred_to_p7.11_export",
			P9_6Linkage:            "activation_report_input",
			HostedTelemetryDefault: false,
			FieldList: []string{
				"setup_install_readiness",
				"first_useful_packet",
				"freshness_check",
				"audit_report",
				"suggested_verification",
				"product_evidence_safe_output",
				"failure_reason",
				"docs_gap",
				"privacy_redaction_status",
				"cleanup",
				"time_to_first_packet_seconds",
				"time_to_trust_evidence_seconds",
			},
		},
		FailureReason: localOpsActivationFailureReason{Status: "none", PrimaryClass: "none", RetryAllowed: false},
		DocsGap:       localOpsActivationDocsGap{Status: "none", GapRecorded: false},
		PrivacyRedactionStatus: localOpsActivationPrivacyEvidence{
			Status:                  "pass",
			RedactionReviewer:       "automated_local_ops_smoke",
			ForbiddenFieldCheck:     "pass",
			RawPromptCaptured:       false,
			RawFileBodyCaptured:     false,
			SecretCaptured:          false,
			PrivatePathCaptured:     false,
			PrivateRepoURLCaptured:  false,
			UnredactedLogCaptured:   false,
			HostedTelemetryRequired: false,
			RedactedFields:          []string{"workspace_root", "artifact_path", "config_path", "telemetry_path", "raw_output", "quickstart_task"},
		},
	}
	smoke.Commands = append(smoke.Commands, localOpsActivationCommand{
		CommandID:      "p77-setup-readiness",
		Label:          "install/setup readiness",
		ExitStatus:     "pass",
		ElapsedSeconds: 1,
	})

	cfg, err := loadToolConfig(opts.ConfigPath)
	if err != nil {
		smoke.recordQuickstartFailure("setup", "load_config_failed", err)
		smoke.Cleanup = localOpsQuickstartCleanupEvidence("", opts.QuickstartCleanup, "", cfg.Workspace.Root, false)
		return smoke
	}
	root := cfg.Workspace.Root
	if strings.TrimSpace(root) == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			root = wd
		}
	}
	mgr := workspace.NewManager(cfg.Workspace)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	packetReq := workspace.ToolRequest{Tool: "context_pack", Task: task, Mode: "plan", Limit: 4}
	packetResult := mgr.Execute(ctx, packetReq)
	smoke.Commands = append(smoke.Commands, localOpsCommandEvidence("p77-context-pack", "first useful context_pack", packetResult, 2))
	packetOutput := decodeLocalOpsJSONObject(packetResult.Output)
	smoke.FirstUsefulPacket = localOpsPacketEvidence(packetResult, packetOutput, task)
	if smoke.FirstUsefulPacket.Status != "pass" {
		smoke.recordQuickstartFailure("context_pack", smoke.FirstUsefulPacket.BlockerReason, nil)
	}
	smoke.TimeToFirstPacketSeconds = 2

	statusResult := mgr.Execute(ctx, workspace.ToolRequest{Tool: "context_status", Limit: 20})
	smoke.Commands = append(smoke.Commands, localOpsCommandEvidence("p77-freshness-check", "freshness check", statusResult, 3))
	statusOutput := decodeLocalOpsJSONObject(statusResult.Output)
	smoke.FreshnessCheck = localOpsFreshnessEvidence(statusResult, statusOutput)
	if smoke.FreshnessCheck.Status != "pass" {
		smoke.recordQuickstartFailure("freshness", smoke.FreshnessCheck.BlockerReason, nil)
	}

	auditResult := mgr.Execute(ctx, workspace.ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
	smoke.Commands = append(smoke.Commands, localOpsCommandEvidence("p77-harness-audit", "audit report", auditResult, 4))
	auditOutput := decodeLocalOpsJSONObject(auditResult.Output)
	smoke.AuditReport = localOpsAuditEvidence(auditResult, auditOutput)
	if smoke.AuditReport.Status != "pass" {
		smoke.recordQuickstartFailure("audit", smoke.AuditReport.BlockerReason, nil)
	}

	smoke.SuggestedVerification.SuggestedCommands = localOpsSuggestedVerificationCommands(packetOutput)
	if len(smoke.SuggestedVerification.SuggestedCommands) == 0 {
		smoke.SuggestedVerification.SuggestedCommands = []string{"go test ./..."}
	}
	for _, command := range smoke.SuggestedVerification.SuggestedCommands {
		smoke.SuggestedVerification.SuggestedLabels = append(smoke.SuggestedVerification.SuggestedLabels, localOpsVerificationLabel(command))
	}
	smoke.Commands = append(smoke.Commands, localOpsActivationCommand{
		CommandID:      "p77-suggested-verification",
		Label:          "suggested verification",
		ExitStatus:     "not_run",
		ElapsedSeconds: 5,
	})

	smoke.Cleanup = localOpsQuickstartCleanupEvidence(localOpsStringField(packetOutput, "artifact_path"), opts.QuickstartCleanup, smoke.FirstUsefulPacket.PacketID, root, true)
	smoke.Commands = append(smoke.Commands, localOpsActivationCommand{
		CommandID:      smoke.Cleanup.CommandID,
		Label:          "cleanup / clean-uninstall evidence",
		ExitStatus:     smoke.Cleanup.ExitStatus,
		ElapsedSeconds: 6,
	})
	if smoke.Cleanup.Status == "blocked" {
		smoke.recordQuickstartFailure("cleanup", smoke.Cleanup.BlockerReason, nil)
	}
	smoke.TimeToTrustEvidenceSeconds = 6
	if smoke.Status == "pass" && (smoke.FirstUsefulPacket.Status != "pass" || smoke.FreshnessCheck.Status != "pass" || smoke.AuditReport.Status != "pass" || smoke.Cleanup.Status == "blocked") {
		smoke.recordQuickstartFailure("insufficient-evidence", "quickstart evidence incomplete", nil)
	}
	return smoke
}

func (smoke *localOpsQuickstartSmoke) recordQuickstartFailure(class string, reason string, err error) {
	if smoke.Status == "pass" {
		smoke.Status = "blocked"
	}
	if strings.TrimSpace(reason) == "" && err != nil {
		reason = err.Error()
	}
	reason = localOpsSafeText(firstNonEmptyCLIString(reason, "quickstart evidence unavailable"))
	smoke.FailureReason = localOpsActivationFailureReason{
		Status:       "blocked",
		PrimaryClass: class,
		Reason:       reason,
		RetryAllowed: false,
	}
	smoke.DocsGap = localOpsActivationDocsGap{
		Status:            "recorded",
		GapRecorded:       true,
		DocPath:           "README.md#quick-start",
		CommandLabel:      class,
		ExpectedOutput:    "machine-readable quickstart activation evidence",
		ActualOutput:      reason,
		Severity:          "medium",
		BlockedActivation: true,
	}
}

func localOpsCommandEvidence(commandID string, label string, result workspace.ToolResult, elapsedSeconds float64) localOpsActivationCommand {
	status := "pass"
	errorClass := ""
	if strings.TrimSpace(result.Error) != "" {
		status = "blocked"
		errorClass = "tool_error"
	}
	return localOpsActivationCommand{
		CommandID:      commandID,
		Label:          label,
		ExitStatus:     status,
		ElapsedSeconds: elapsedSeconds,
		ErrorClass:     errorClass,
	}
}

func localOpsPacketEvidence(result workspace.ToolResult, output map[string]any, task string) localOpsActivationPacketEvidence {
	packet := localOpsActivationPacketEvidence{
		Status:    "pass",
		CommandID: "p77-context-pack",
		Tool:      "context_pack",
		Task:      localOpsTaskEvidence(task),
		PacketID:  localOpsStringField(output, "pack_id"),
		IndexID:   localOpsStringField(output, "index_id"),
	}
	packet.CandidateFileCount = len(localOpsArrayField(output, "candidate_files"))
	packet.CandidateSymbolCount = len(localOpsArrayField(output, "candidate_symbols"))
	packet.CandidateTestCount = len(localOpsArrayField(output, "tests"))
	packet.FreshnessState = localOpsNestedString(output, "stale_evidence", "state")
	if packet.FreshnessState == "" {
		if localOpsBoolField(output, "fresh") {
			packet.FreshnessState = "fresh"
		}
	}
	if packet.PacketID == "" || packet.IndexID == "" || packet.FreshnessState == "" {
		packet.Status = "blocked"
		packet.BlockerReason = "packet missing packet_id, index_id, or freshness state"
	}
	if strings.TrimSpace(result.Error) != "" {
		packet.Status = "blocked"
		packet.BlockerReason = localOpsSafeText(result.Error)
	}
	packet.UsefulEvidence = append(packet.UsefulEvidence,
		fmt.Sprintf("candidate_files:%d", packet.CandidateFileCount),
		fmt.Sprintf("candidate_symbols:%d", packet.CandidateSymbolCount),
		fmt.Sprintf("candidate_tests:%d", packet.CandidateTestCount),
	)
	if packet.CandidateFileCount == 0 && packet.CandidateSymbolCount == 0 && packet.CandidateTestCount == 0 {
		packet.Status = "blocked"
		packet.BlockerReason = "packet returned no ranked files, symbols, or tests"
	}
	return packet
}

func localOpsFreshnessEvidence(result workspace.ToolResult, output map[string]any) localOpsActivationFreshnessEvidence {
	freshness := localOpsActivationFreshnessEvidence{
		Status:         "pass",
		CommandID:      "p77-freshness-check",
		Tool:           "context_status",
		IndexID:        localOpsStringField(output, "index_id"),
		State:          localOpsStringField(output, "state"),
		FreshForScope:  localOpsBoolField(output, "fresh_for_scope"),
		GlobalFresh:    localOpsBoolField(output, "global_fresh"),
		StalePathCount: localOpsIntField(output, "stale_path_count"),
	}
	if freshness.State == "" {
		if localOpsBoolField(output, "fresh") {
			freshness.State = "fresh"
		}
	}
	if strings.TrimSpace(result.Error) != "" {
		freshness.Status = "blocked"
		freshness.BlockerReason = localOpsSafeText(result.Error)
		return freshness
	}
	if freshness.State == "" {
		freshness.Status = "blocked"
		freshness.BlockerReason = "freshness state unavailable"
	}
	return freshness
}

func localOpsAuditEvidence(result workspace.ToolResult, output map[string]any) localOpsActivationAuditEvidence {
	audit := localOpsActivationAuditEvidence{
		Status:        "pass",
		CommandID:     "p77-harness-audit",
		Tool:          "harness_audit",
		Purpose:       firstNonEmptyCLIString(localOpsStringField(output, "purpose"), "behavior"),
		AuditReportID: localOpsStableID(result.Output),
	}
	findings := localOpsArrayField(output, "findings")
	audit.FindingCount = len(findings)
	for i, finding := range findings {
		item, ok := finding.(map[string]any)
		if !ok {
			continue
		}
		if id := localOpsStringField(item, "id"); id != "" && len(audit.AuditEventIDs) < 8 {
			audit.AuditEventIDs = append(audit.AuditEventIDs, localOpsStableID(id))
		} else if len(audit.AuditEventIDs) < 8 {
			audit.AuditEventIDs = append(audit.AuditEventIDs, fmt.Sprintf("finding_%d", i+1))
		}
		switch strings.ToLower(localOpsStringField(item, "severity")) {
		case "high":
			audit.HighFindingCount++
		case "medium":
			audit.MediumFindingCount++
		}
	}
	if strings.TrimSpace(result.Error) != "" {
		audit.Status = "blocked"
		audit.BlockerReason = localOpsSafeText(result.Error)
	}
	return audit
}

func localOpsSuggestedVerificationCommands(output map[string]any) []string {
	var commands []string
	for _, item := range localOpsArrayField(output, "tests") {
		test, ok := item.(map[string]any)
		if !ok {
			continue
		}
		command := localOpsSafeCommand(localOpsStringField(test, "command"))
		if command == "" {
			continue
		}
		commands = append(commands, command)
		if len(commands) == 3 {
			break
		}
	}
	return commands
}

func localOpsVerificationLabel(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "verification"
	}
	if len(fields) >= 3 && fields[0] == "go" && fields[1] == "test" {
		return "go-test"
	}
	return strings.ReplaceAll(fields[0], "-", "_")
}

func localOpsQuickstartCleanupEvidence(artifactPath string, mode string, packetID string, root string, allowRemove bool) localOpsActivationCleanupEvidence {
	cleanup := localOpsActivationCleanupEvidence{
		Status:         "pass",
		CommandID:      "p77-cleanup",
		Mode:           firstNonEmptyCLIString(strings.TrimSpace(mode), "report-only"),
		ExitStatus:     "pass",
		RemainingState: "explicitly_retained_or_absent",
	}
	switch cleanup.Mode {
	case "report-only":
		cleanup.Status = "retained_by_policy"
		cleanup.ExitStatus = "not_run"
		cleanup.RetainedStatePolicy = "quickstart smoke records cleanup command evidence without deleting existing local repo memory"
		if packetID != "" {
			cleanup.EvidenceIDs = append(cleanup.EvidenceIDs, localOpsStableID(packetID))
		}
	case "remove-smoke-artifacts":
		if !allowRemove {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = "cleanup requested before smoke artifact was created"
			return cleanup
		}
		if err := localOpsRemoveContextViewArtifact(root, artifactPath); err != nil {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = localOpsSafeText(err.Error())
			return cleanup
		}
		cleanup.RemovedArtifactCount = 1
		cleanup.RemainingState = "context_graph_retained_by_policy"
	case "remove-local-state":
		if !allowRemove {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = "cleanup requested before local state was created"
			return cleanup
		}
		if strings.TrimSpace(root) == "" {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = "workspace root unavailable for local-state cleanup"
			return cleanup
		}
		if err := os.RemoveAll(filepath.Join(root, ".idiom", "comprehension")); err != nil {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = localOpsSafeText(err.Error())
			return cleanup
		}
		cleanup.RemovedArtifactCount = 1
		cleanup.RemainingState = "comprehension_state_absent"
		cleanup.EvidenceIDs = append(cleanup.EvidenceIDs, localOpsStableID(filepath.Join(root, ".idiom", "comprehension")))
	default:
		cleanup.Status = "blocked"
		cleanup.ExitStatus = "blocked"
		cleanup.BlockerReason = "unknown cleanup mode"
	}
	return cleanup
}

func localOpsRemoveContextViewArtifact(root string, artifactPath string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(artifactPath) == "" {
		return fmt.Errorf("context view artifact unavailable")
	}
	cleanRoot := filepath.Clean(root)
	cleanArtifact := filepath.Clean(artifactPath)
	if !filepath.IsAbs(cleanArtifact) {
		cleanArtifact = filepath.Join(cleanRoot, cleanArtifact)
	}
	rel, err := filepath.Rel(cleanRoot, cleanArtifact)
	if err != nil {
		return fmt.Errorf("context view artifact outside workspace")
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("context view artifact outside workspace")
	}
	relSlash := filepath.ToSlash(rel)
	if !strings.HasPrefix(relSlash, ".idiom/comprehension/views/") || !strings.HasSuffix(relSlash, ".json") {
		return fmt.Errorf("context view artifact not removable by quickstart cleanup")
	}
	if err := os.Remove(cleanArtifact); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func finalizeLocalOpsQuickstartProductEvidence(smoke *localOpsQuickstartSmoke) {
	if smoke == nil {
		return
	}
	snapshot := *smoke
	snapshot.ProductEvidenceSafeOutput.ArtifactHash = ""
	snapshot.ProductEvidenceSafeOutput.ExportID = ""
	body, err := json.Marshal(snapshot)
	if err != nil {
		smoke.ProductEvidenceSafeOutput.Status = "blocked"
		smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck = "blocked"
		return
	}
	hash := sha256.Sum256(body)
	smoke.ProductEvidenceSafeOutput.ArtifactHash = hex.EncodeToString(hash[:8])
	smoke.ProductEvidenceSafeOutput.ExportID = "p77-activation-" + smoke.ProductEvidenceSafeOutput.ArtifactHash
	forbidden := localOpsForbiddenEvidenceLeak(string(body))
	if forbidden != "" {
		smoke.Status = "blocked"
		smoke.ProductEvidenceSafeOutput.Status = "blocked"
		smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck = "failed:" + forbidden
		smoke.PrivacyRedactionStatus.Status = "blocked"
		smoke.PrivacyRedactionStatus.ForbiddenFieldCheck = "failed:" + forbidden
		smoke.FailureReason = localOpsActivationFailureReason{
			Status:       "blocked",
			PrimaryClass: "privacy_export",
			Reason:       "forbidden field detected in quickstart evidence",
			RetryAllowed: false,
		}
		return
	}
	smoke.ProductEvidenceSafeOutput.Status = "pass"
	smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck = "pass"
	smoke.ProductEvidenceSafeOutput.RedactionStatus = localOpsProductRedactionStatus(*smoke)
	smoke.PrivacyRedactionStatus.Status = "pass"
	smoke.PrivacyRedactionStatus.ForbiddenFieldCheck = "pass"
}

func decodeLocalOpsJSONObject(raw string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func localOpsStringField(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func localOpsNestedString(values map[string]any, parent string, key string) string {
	child, ok := values[parent].(map[string]any)
	if !ok {
		return ""
	}
	return localOpsStringField(child, key)
}

func localOpsBoolField(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}

func localOpsIntField(values map[string]any, key string) int {
	value, ok := values[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func localOpsArrayField(values map[string]any, key string) []any {
	value, ok := values[key]
	if !ok {
		return nil
	}
	typed, ok := value.([]any)
	if !ok {
		return nil
	}
	return typed
}

func localOpsSafeCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if strings.Contains(command, "\n") || strings.Contains(command, "\r") {
		return ""
	}
	parts := strings.Fields(command)
	for i, part := range parts {
		if localOpsCommandTokenNeedsRedaction(part) {
			parts[i] = "[redacted]"
		}
	}
	return strings.Join(parts, " ")
}

func localOpsPathEvidence(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return "path_id:" + localOpsStableID(path)
}

func localOpsSafeText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	parts := strings.Fields(value)
	for i, part := range parts {
		trimmed := strings.Trim(part, ".,:;()[]{}\"'")
		if localOpsTokenNeedsRedaction(trimmed) {
			parts[i] = "[redacted:" + localOpsStableID(trimmed) + "]"
		}
	}
	return strings.Join(parts, " ")
}

var (
	localOpsSecretValuePattern = regexp.MustCompile(`(?i)\b(secret|token|credential|password|api[_-]?key)\b\s*[:= ]\s*\S+`)
	localOpsCustomerPattern    = regexp.MustCompile(`(?i)\bcustomer[a-z0-9_-]*\b|\b[a-z0-9_-]*customer[a-z0-9_-]*\b`)
	localOpsPrivateRepoPattern = regexp.MustCompile(`(?i)\bprivate\s+(repo|repository|workspace|project|customer)\b|\b[a-z0-9_-]*private[a-z0-9_-]*(repo|repository|workspace|project|customer)[a-z0-9_-]*\b|\b[a-z0-9_-]*(repo|repository|workspace|project|customer)[a-z0-9_-]*private[a-z0-9_-]*\b`)
)

func localOpsTaskEvidence(task string) localOpsActivationTaskEvidence {
	task = strings.TrimSpace(task)
	if task == "" {
		task = "localize first useful Idiom quickstart packet"
	}
	classes := localOpsSensitiveStringClasses(task)
	if len(classes) == 0 {
		classes = []string{"general_task_text"}
	}
	status := "hashed"
	if len(classes) > 1 || classes[0] != "general_task_text" {
		status = "redacted"
	}
	return localOpsActivationTaskEvidence{
		TaskID:          "task_id:" + localOpsStableID(task),
		EvidenceType:    "hash_and_classification",
		RedactionStatus: status,
		RawTaskCaptured: false,
		ContentClasses:  classes,
	}
}

func localOpsProductRedactionStatus(smoke localOpsQuickstartSmoke) string {
	if smoke.FirstUsefulPacket.Task.RedactionStatus == "redacted" {
		return "redacted"
	}
	if len(smoke.PrivacyRedactionStatus.RedactedFields) > 0 {
		return "redacted"
	}
	return "pass"
}

func localOpsSensitiveStringClasses(value string) []string {
	seen := map[string]bool{}
	var classes []string
	add := func(class string) {
		if !seen[class] {
			seen[class] = true
			classes = append(classes, class)
		}
	}
	if localOpsContainsAbsolutePath(value) {
		add("absolute_path")
	}
	if localOpsSecretValuePattern.MatchString(value) {
		add("secret_marker")
	}
	if localOpsCustomerPattern.MatchString(value) || localOpsPrivateRepoPattern.MatchString(value) {
		add("sensitive_identifier")
	}
	return classes
}

func localOpsTokenNeedsRedaction(token string) bool {
	token = strings.TrimSpace(strings.Trim(token, ".,:;()[]{}\"'"))
	if token == "" {
		return false
	}
	lower := strings.ToLower(token)
	return filepath.IsAbs(token) ||
		strings.Contains(token, "/") ||
		strings.Contains(token, "\\") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "password") ||
		localOpsCustomerPattern.MatchString(token) ||
		localOpsPrivateRepoPattern.MatchString(token)
}

func localOpsCommandTokenNeedsRedaction(token string) bool {
	token = strings.TrimSpace(strings.Trim(token, ".,:;()[]{}\"'"))
	if token == "" {
		return false
	}
	lower := strings.ToLower(token)
	return filepath.IsAbs(token) ||
		strings.Contains(token, "\\") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "password") ||
		localOpsCustomerPattern.MatchString(token) ||
		localOpsPrivateRepoPattern.MatchString(token)
}

func localOpsForbiddenEvidenceLeak(body string) string {
	var value any
	if err := json.Unmarshal([]byte(body), &value); err == nil {
		return localOpsForbiddenEvidenceValue(value)
	}
	return localOpsForbiddenStringEvidence(body)
}

func localOpsForbiddenEvidenceValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if boolValue, ok := typed[key].(bool); ok && boolValue && localOpsForbiddenBooleanEvidenceKey(key) {
				return key
			}
			if forbidden := localOpsForbiddenEvidenceValue(typed[key]); forbidden != "" {
				return forbidden
			}
		}
	case []any:
		for _, item := range typed {
			if forbidden := localOpsForbiddenEvidenceValue(item); forbidden != "" {
				return forbidden
			}
		}
	case string:
		return localOpsForbiddenStringEvidence(typed)
	}
	return ""
}

func localOpsForbiddenBooleanEvidenceKey(key string) bool {
	switch key {
	case "raw_prompt_captured",
		"raw_file_body_captured",
		"secret_captured",
		"secret_capture_allowed",
		"private_path_captured",
		"private_repo_url_captured",
		"unredacted_log_captured",
		"hosted_telemetry_required",
		"raw_task_captured":
		return true
	default:
		return false
	}
}

func localOpsForbiddenStringEvidence(value string) string {
	if localOpsContainsAbsolutePath(value) {
		return "absolute_path"
	}
	if localOpsSecretValuePattern.MatchString(value) {
		return "secret_value"
	}
	if localOpsCustomerPattern.MatchString(value) {
		return "customer_identifier"
	}
	if localOpsPrivateRepoPattern.MatchString(value) {
		return "private_identifier"
	}
	return ""
}

func localOpsContainsAbsolutePath(value string) bool {
	for _, field := range strings.Fields(value) {
		token := strings.Trim(field, ".,:;()[]{}\"'")
		if len(token) > 1 && filepath.IsAbs(token) {
			return true
		}
	}
	return false
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
				Remedy: localOpsSafeText("provide a readable, parseable JSONL conversation or telemetry log"),
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
