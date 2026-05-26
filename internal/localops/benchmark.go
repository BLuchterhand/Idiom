package localops

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/readiness"
)

type BenchmarkOptions struct {
	ConfigPath        string
	Profile           string
	ReplayLog         string
	ReplayGlob        string
	OutputPath        string
	TelemetryPath     string
	QuickstartSmoke   bool
	QuickstartTask    string
	QuickstartCleanup string
	Version           string
	Commit            string
}

func BuildBenchmarkReport(opts BenchmarkOptions, quickstartDeps QuickstartDeps) (BenchmarkReport, error) {
	profiles, err := readiness.BuildProfiles(opts.ConfigPath, opts.Profile, nil)
	if err != nil {
		return BenchmarkReport{}, err
	}
	report := BenchmarkReport{
		SchemaVersion: BenchmarkSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Command:       "idiom local-ops benchmark",
		ConfigPath:    PathEvidence(opts.ConfigPath),
		Privacy: PrivacyReport{
			MetadataOnly:         true,
			RawPromptCaptured:    false,
			RawFileBodyCaptured:  false,
			SecretCaptureAllowed: false,
			RedactedFields:       []string{"user_prompt", "assistant_content", "tool_raw_args", "file_body", "secret_value"},
		},
	}
	if strings.TrimSpace(opts.TelemetryPath) != "" {
		report.Telemetry = &TelemetryReport{Path: PathEvidence(opts.TelemetryPath), ID: StableID(opts.TelemetryPath)}
	}
	if strings.TrimSpace(opts.OutputPath) != "" {
		report.Privacy.LocalArtifactDirectory = PathEvidence(filepath.Dir(filepath.Clean(opts.OutputPath)))
	}
	for _, profile := range profiles {
		report.Profiles = append(report.Profiles, BuildProfileReport(profile))
	}
	replay, err := BuildReplayReport(opts.ReplayLog, opts.ReplayGlob)
	if err != nil {
		return BenchmarkReport{}, err
	}
	report.Replay = replay
	if opts.QuickstartSmoke {
		quickstart := BuildQuickstartSmokeReport(QuickstartOptions{
			ConfigPath: opts.ConfigPath,
			Profile:    opts.Profile,
			Task:       opts.QuickstartTask,
			Cleanup:    opts.QuickstartCleanup,
			Version:    opts.Version,
			Commit:     opts.Commit,
		}, quickstartDeps)
		report.Quickstart = &quickstart
		FinalizeQuickstartProductEvidence(report.Quickstart)
	}
	report.Blockers = CollectBlockers(report.Profiles, replay)
	if report.Quickstart != nil && report.Quickstart.Status != "pass" {
		report.Blockers = append(report.Blockers, Blocker{
			Scope:  "quickstart_smoke",
			Reason: firstNonEmpty(report.Quickstart.FailureReason.Reason, report.Quickstart.Status),
		})
	}
	report.Summary = SummarizeBenchmark(report)
	FinalizeBenchmarkPrivacy(&report)
	return report, nil
}

func FinalizeBenchmarkPrivacy(report *BenchmarkReport) {
	if report == nil {
		return
	}
	body, err := json.Marshal(report)
	if err != nil {
		return
	}
	forbidden := ForbiddenEvidenceLeak(string(body))
	if forbidden == "" {
		return
	}
	RedactForbiddenStrings(report)
	report.Privacy.MetadataOnly = true
	report.Privacy.RawPromptCaptured = false
	report.Privacy.RawFileBodyCaptured = false
	report.Privacy.SecretCaptureAllowed = false
	report.Blockers = append(report.Blockers, Blocker{
		Scope:  "privacy",
		Reason: "benchmark report failed forbidden evidence scan",
		Remedy: "inspect metadata-only inputs and rerun with redacted paths and log labels",
	})
	report.Summary = SummarizeBenchmark(*report)
}
