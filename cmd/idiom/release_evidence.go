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
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/telemetry"
)

const releaseEvidenceSchemaVersion = "idiom.release_evidence_bundle.v1"

var releaseEvidenceClientClassifications = []string{
	"finance",
	"healthcare",
	"public-sector",
	"enterprise-saas",
	"tooling-vendor",
	"local-model",
	"open-source",
}

type releaseEvidenceOptions struct {
	ProjectRoot        string
	OutputPath         string
	BaselineDir        string
	CurrentDir         string
	LocalOpsReport     string
	ValidationRecords  string
	BenchmarkReport    string
	SidecarSmokeReport string
}

type releaseEvidenceBundle struct {
	SchemaVersion        string                              `json:"schema_version"`
	GeneratedAt          string                              `json:"generated_at"`
	Status               string                              `json:"status"`
	BundleID             string                              `json:"bundle_id"`
	ArtifactHash         string                              `json:"artifact_hash"`
	Build                releaseEvidenceBuildMetadata        `json:"build"`
	Privacy              releaseEvidencePrivacy              `json:"privacy"`
	BaselineCurrent      releaseEvidenceBaselineCurrent      `json:"baseline_current"`
	Validation           releaseEvidenceValidationSection    `json:"validation"`
	AuditSecurity        releaseEvidenceAuditSecuritySection `json:"audit_security"`
	BenchmarkLocalOps    releaseEvidenceBenchmarkSection     `json:"benchmark_local_ops"`
	ActivationOnboarding releaseEvidenceActivationSection    `json:"activation_onboarding"`
	SidecarIntegration   releaseEvidenceSidecarSection       `json:"sidecar_integration"`
	ProductEvidence      releaseEvidenceProductSection       `json:"product_evidence"`
	ClientReadiness      []releaseEvidenceClientReadiness    `json:"client_readiness"`
	ReleaseReadiness     []releaseEvidenceGateReadiness      `json:"release_readiness"`
	Blockers             []releaseEvidenceBlocker            `json:"blockers,omitempty"`
	Limitations          []string                            `json:"limitations,omitempty"`
	Metadata             map[string]any                      `json:"metadata,omitempty"`
}

type releaseEvidenceBuildMetadata struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OSArch  string `json:"os_arch"`
}

type releaseEvidencePrivacy struct {
	MetadataOnly           bool     `json:"metadata_only"`
	ForbiddenFieldCheck    string   `json:"forbidden_field_check"`
	RawPromptsIncluded     bool     `json:"raw_prompts_included"`
	RawProtocolBodies      bool     `json:"raw_protocol_bodies_included"`
	RawFileBodiesIncluded  bool     `json:"raw_file_bodies_included"`
	AbsolutePathsIncluded  bool     `json:"absolute_private_paths_included"`
	SecretsIncluded        bool     `json:"secrets_tokens_credentials_included"`
	HostedTelemetryDefault bool     `json:"hosted_telemetry_default"`
	RedactedFields         []string `json:"redacted_fields"`
}

type releaseEvidenceBaselineCurrent struct {
	Baseline releaseEvidenceInputBundle `json:"baseline"`
	Current  releaseEvidenceInputBundle `json:"current"`
	Deltas   []releaseEvidenceDelta     `json:"deltas,omitempty"`
}

type releaseEvidenceInputBundle struct {
	Label         string                    `json:"label"`
	Status        string                    `json:"status"`
	Directory     releaseEvidencePathRef    `json:"directory"`
	CommandCount  int                       `json:"command_count"`
	ArtifactCount int                       `json:"artifact_count"`
	Commands      []releaseEvidenceCommand  `json:"commands,omitempty"`
	Artifacts     []releaseEvidenceArtifact `json:"artifacts,omitempty"`
	Blockers      []releaseEvidenceBlocker  `json:"blockers,omitempty"`
}

type releaseEvidenceCommand struct {
	ID             string                 `json:"id"`
	Command        string                 `json:"command"`
	Status         string                 `json:"status"`
	ExitStatus     string                 `json:"exit_status,omitempty"`
	StdoutArtifact releaseEvidencePathRef `json:"stdout_artifact,omitempty"`
	StderrArtifact releaseEvidencePathRef `json:"stderr_artifact,omitempty"`
	Blocker        string                 `json:"blocker,omitempty"`
}

type releaseEvidenceArtifact struct {
	ID           string                 `json:"id"`
	Kind         string                 `json:"kind"`
	Status       string                 `json:"status"`
	Path         releaseEvidencePathRef `json:"path"`
	ArtifactHash string                 `json:"artifact_hash,omitempty"`
}

type releaseEvidencePathRef struct {
	PathID       string `json:"path_id,omitempty"`
	RelativePath string `json:"relative_path,omitempty"`
}

type releaseEvidenceBlocker struct {
	Section  string `json:"section"`
	Reason   string `json:"reason"`
	Evidence string `json:"evidence,omitempty"`
}

type releaseEvidenceDelta struct {
	Metric   string `json:"metric"`
	Baseline int    `json:"baseline"`
	Current  int    `json:"current"`
	Status   string `json:"status"`
}

type releaseEvidenceValidationSection struct {
	Status   string                         `json:"status"`
	Commands []releaseEvidenceCommandStatus `json:"commands"`
	Blockers []releaseEvidenceBlocker       `json:"blockers,omitempty"`
}

type releaseEvidenceCommandStatus struct {
	ID       string   `json:"id"`
	Command  string   `json:"command"`
	Status   string   `json:"status"`
	Source   string   `json:"source,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
}

type releaseEvidenceAuditSecuritySection struct {
	Status                string                    `json:"status"`
	HarnessAuditInputs    []releaseEvidenceArtifact `json:"harness_audit_inputs,omitempty"`
	SecurityPostureSource p711SourceReference       `json:"security_posture_source"`
	SecurityPrivacyGate   string                    `json:"security_privacy_gate"`
	ProductAuditGate      string                    `json:"product_audit_gate"`
	HighFindingStatus     string                    `json:"high_finding_status"`
	Blockers              []releaseEvidenceBlocker  `json:"blockers,omitempty"`
}

type releaseEvidenceBenchmarkSection struct {
	Status              string                    `json:"status"`
	LocalOps            releaseEvidenceLocalOps   `json:"local_ops"`
	CompetitiveSignals  []releaseEvidenceArtifact `json:"competitive_signals,omitempty"`
	CompetitiveCoverage p711ReleaseGateLinkage    `json:"competitive_coverage"`
	Blockers            []releaseEvidenceBlocker  `json:"blockers,omitempty"`
}

type releaseEvidenceActivationSection struct {
	Status                      string                   `json:"status"`
	Classification              string                   `json:"classification"`
	ReportSource                p711SourceReference      `json:"report_source"`
	SmokeID                     string                   `json:"smoke_id,omitempty"`
	QuickstartStatus            string                   `json:"quickstart_status,omitempty"`
	ClientClassification        string                   `json:"client_classification,omitempty"`
	EnvironmentClass            string                   `json:"environment_class,omitempty"`
	InstallSetupPath            string                   `json:"install_setup_path,omitempty"`
	ReleaseInstallProof         string                   `json:"release_install_proof,omitempty"`
	AssistanceClass             string                   `json:"assistance_class,omitempty"`
	DurationSource              string                   `json:"duration_source,omitempty"`
	TimeToFirstPacketSeconds    float64                  `json:"time_to_first_packet_seconds,omitempty"`
	TimeToTrustEvidenceSeconds  float64                  `json:"time_to_trust_evidence_seconds,omitempty"`
	FirstUsefulPacketStatus     string                   `json:"first_useful_packet_status,omitempty"`
	FreshnessStatus             string                   `json:"freshness_status,omitempty"`
	AuditStatus                 string                   `json:"audit_status,omitempty"`
	SuggestedVerificationStatus string                   `json:"suggested_verification_status,omitempty"`
	ProductEvidenceSafeStatus   string                   `json:"product_evidence_safe_status,omitempty"`
	FailureReasonStatus         string                   `json:"failure_reason_status,omitempty"`
	DocsGapStatus               string                   `json:"docs_gap_status,omitempty"`
	CleanupStatus               string                   `json:"cleanup_status,omitempty"`
	CleanupMode                 string                   `json:"cleanup_mode,omitempty"`
	PrivacyStatus               string                   `json:"privacy_status,omitempty"`
	ForbiddenFieldCheck         string                   `json:"forbidden_field_check,omitempty"`
	ValidationStatus            string                   `json:"validation_status,omitempty"`
	EvidenceIDs                 []string                 `json:"evidence_ids,omitempty"`
	Blockers                    []releaseEvidenceBlocker `json:"blockers,omitempty"`
}

type releaseEvidenceLocalOps struct {
	Status           string                `json:"status"`
	ReportSource     p711SourceReference   `json:"report_source"`
	ProfileCount     int                   `json:"profile_count,omitempty"`
	SmokePassed      int                   `json:"smoke_passed,omitempty"`
	SmokeTotal       int                   `json:"smoke_total,omitempty"`
	ReplayLogCount   int                   `json:"replay_log_count,omitempty"`
	BlockerCount     int                   `json:"blocker_count,omitempty"`
	QuickstartStatus string                `json:"quickstart_status,omitempty"`
	Privacy          localOpsPrivacyReport `json:"privacy,omitempty"`
}

type releaseEvidenceSidecarSection struct {
	Status           string                           `json:"status"`
	SmokeTranscript  releaseEvidenceArtifact          `json:"smoke_transcript"`
	IntegrationProof p711SourceReference              `json:"integration_proof_source"`
	Paths            []releaseEvidenceIntegrationPath `json:"paths"`
	Blockers         []releaseEvidenceBlocker         `json:"blockers,omitempty"`
}

type releaseEvidenceIntegrationPath struct {
	Path          string                 `json:"path"`
	Status        string                 `json:"status"`
	EvidenceIDs   []string               `json:"evidence_ids,omitempty"`
	ReplayFixture releaseEvidencePathRef `json:"replay_fixture,omitempty"`
	Blocker       string                 `json:"blocker,omitempty"`
}

type releaseEvidenceProductSection struct {
	Status             string                   `json:"status"`
	ExportID           string                   `json:"export_id"`
	ArtifactHash       string                   `json:"artifact_hash"`
	SignalCompleteness p711SignalCompleteness   `json:"signal_completeness"`
	Signals            []p711SignalSummary      `json:"signals"`
	SourceReferences   []p711SourceReference    `json:"source_references"`
	ReleaseGateLinkage []p711ReleaseGateLinkage `json:"release_gate_linkage"`
}

type releaseEvidenceClientReadiness struct {
	Classification         string   `json:"classification"`
	Status                 string   `json:"status"`
	Stance                 string   `json:"stance"`
	ArchitecturePrimitives []string `json:"architecture_primitives"`
	EvidenceIDs            []string `json:"evidence_ids,omitempty"`
	MissingEvidence        []string `json:"missing_evidence,omitempty"`
	Limitations            []string `json:"limitations,omitempty"`
}

type releaseEvidenceGateReadiness struct {
	Gate        string         `json:"gate"`
	Status      string         `json:"status"`
	Evidence    string         `json:"evidence"`
	ClaimImpact string         `json:"claim_impact"`
	Counts      map[string]int `json:"counts,omitempty"`
	Blockers    []string       `json:"blockers,omitempty"`
	EvidenceIDs []string       `json:"evidence_ids,omitempty"`
}

func runReleaseMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 {
		printReleaseUsage(stderr)
		return 2
	}
	switch args[0] {
	case "evidence":
		return runReleaseEvidence(args[1:], stdout, stderr, rec)
	case "-h", "--help", "help":
		printReleaseUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "release: unknown subcommand %q\n", args[0])
		printReleaseUsage(stderr)
		return 2
	}
}

func runReleaseEvidence(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom release evidence", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := releaseEvidenceOptions{}
	fs.StringVar(&opts.ProjectRoot, "project-root", ".", "workspace root whose release evidence should be summarized")
	fs.StringVar(&opts.OutputPath, "output", "", "optional path to write the JSON bundle")
	fs.StringVar(&opts.BaselineDir, "baseline-dir", "docs/evidence/phase0-baseline-2026-05-13", "baseline evidence directory")
	fs.StringVar(&opts.CurrentDir, "current-dir", ".idiom/release/current", "current release evidence directory")
	fs.StringVar(&opts.LocalOpsReport, "local-ops-report", "", "optional local-ops benchmark JSON report")
	fs.StringVar(&opts.ValidationRecords, "validation-records", "", "optional privacy-safe P0.8 validation records JSON or JSONL")
	fs.StringVar(&opts.BenchmarkReport, "benchmark-report", "", "optional localization or competitive benchmark JSON report")
	fs.StringVar(&opts.SidecarSmokeReport, "sidecar-smoke", "testdata/sidecar/discovery-smoke.transcript.json", "sidecar smoke transcript JSON")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	root, err := filepath.Abs(opts.ProjectRoot)
	if err != nil {
		fmt.Fprintf(stderr, "release evidence: resolve project root: %v\n", err)
		return 1
	}
	summary, err := summarizeLaneHistory(root)
	if err != nil {
		fmt.Fprintf(stderr, "release evidence: summarize lane history: %v\n", err)
		return 1
	}
	report := buildReleaseEvidenceBundle(root, summary, opts)
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "release evidence: marshal bundle: %v\n", err)
		return 1
	}
	if forbidden := localOpsForbiddenEvidenceLeak(string(body)); forbidden != "" {
		report.Status = "blocked"
		report.Privacy.ForbiddenFieldCheck = "failed:" + forbidden
		report.Blockers = append(report.Blockers, releaseEvidenceBlocker{Section: "privacy", Reason: "release bundle failed forbidden evidence scan", Evidence: forbidden})
		body, err = json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "release evidence: marshal blocked bundle: %v\n", err)
			return 1
		}
	}
	body = append(body, '\n')
	if strings.TrimSpace(opts.OutputPath) != "" {
		if err := os.MkdirAll(filepath.Dir(filepath.Clean(opts.OutputPath)), 0o755); err != nil {
			fmt.Fprintf(stderr, "release evidence: create output directory: %v\n", err)
			return 1
		}
		if err := os.WriteFile(opts.OutputPath, body, 0o644); err != nil {
			fmt.Fprintf(stderr, "release evidence: write output: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "release evidence wrote bundle_id:%s\n", report.BundleID)
	} else {
		_, _ = stdout.Write(body)
	}
	logCLIPhase(rec, "release_evidence_bundle", map[string]any{
		"status":           report.Status,
		"bundle_id":        report.BundleID,
		"artifact_hash":    report.ArtifactHash,
		"blocker_count":    len(report.Blockers),
		"privacy_status":   report.Privacy.ForbiddenFieldCheck,
		"product_status":   report.ProductEvidence.Status,
		"sidecar_status":   report.SidecarIntegration.Status,
		"benchmark_status": report.BenchmarkLocalOps.Status,
	})
	return 0
}

func buildReleaseEvidenceBundle(root string, laneSummary laneHistorySummary, opts releaseEvidenceOptions) releaseEvidenceBundle {
	baseline := loadReleaseEvidenceInputBundle(root, opts.BaselineDir, "baseline")
	current := loadReleaseEvidenceInputBundle(root, opts.CurrentDir, "current")
	localOps, localOpsRef := loadP711LocalOpsReport(root, opts.LocalOpsReport)
	integration, integrationRef := loadP711IntegrationProofs(root)
	_, securityRef := loadP711SecurityPosture(root)
	product := buildProductEvidenceExportReport(root, laneSummary, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    opts.LocalOpsReport,
		ValidationRecords: opts.ValidationRecords,
		BenchmarkReport:   opts.BenchmarkReport,
	})
	clientReadiness := buildReleaseClientReadiness(product.ClassCoverage)
	validation := buildReleaseValidationSection(current, product)
	activation := buildReleaseActivationSection(localOps, localOpsRef, validation, clientReadiness)
	releaseGateLinkage := releaseGateLinkageFromProduct(product.ReleaseGateLinkage, clientReadiness, activation)
	report := releaseEvidenceBundle{
		SchemaVersion: releaseEvidenceSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Build:         releaseEvidenceBuildInfo(),
		Privacy: releaseEvidencePrivacy{
			MetadataOnly:           true,
			ForbiddenFieldCheck:    "pass",
			RawPromptsIncluded:     false,
			RawProtocolBodies:      false,
			RawFileBodiesIncluded:  false,
			AbsolutePathsIncluded:  false,
			SecretsIncluded:        false,
			HostedTelemetryDefault: false,
			RedactedFields:         []string{"project_root", "absolute_artifact_paths", "raw_command_args", "raw_prompt_bodies", "raw_protocol_bodies", "raw_file_bodies"},
		},
		BaselineCurrent: releaseEvidenceBaselineCurrent{
			Baseline: baseline,
			Current:  current,
			Deltas:   releaseEvidenceDeltas(baseline, current),
		},
		Validation:           validation,
		AuditSecurity:        buildReleaseAuditSecuritySection(current, product, securityRef),
		BenchmarkLocalOps:    buildReleaseBenchmarkSection(root, opts, localOps, localOpsRef, releaseGateLinkageByGate(releaseGateLinkage, "P9.10")),
		ActivationOnboarding: activation,
		SidecarIntegration:   buildReleaseSidecarSection(root, opts, integration, integrationRef),
		ProductEvidence: releaseEvidenceProductSection{
			Status:             product.Status,
			ExportID:           product.ExportID,
			ArtifactHash:       product.ArtifactHash,
			SignalCompleteness: product.SignalCompleteness,
			Signals:            product.Signals,
			SourceReferences:   product.SourceReferences,
			ReleaseGateLinkage: releaseGateLinkage,
		},
		ClientReadiness:  clientReadiness,
		ReleaseReadiness: releaseReadinessFromProduct(releaseGateLinkage, clientReadiness, activation),
		Limitations: []string{
			"P9.1 produces release evidence only; P9.2-P9.12 own go/no-go decisions and gate pass/fail signoff.",
			"P9.10 summarizes the default P4.6 localization corpus and any supplied competitive benchmark artifact.",
		},
		Metadata: map[string]any{
			"generated_by": "idiom release evidence",
		},
	}
	report.Blockers = collectReleaseEvidenceBlockers(report)
	report.Status = releaseEvidenceStatus(report)
	report.ArtifactHash = releaseEvidenceHash(report)
	report.BundleID = "release-evidence-" + report.ArtifactHash
	return report
}

func loadReleaseEvidenceInputBundle(root string, dir string, label string) releaseEvidenceInputBundle {
	path := resolveReleaseEvidencePath(root, dir)
	out := releaseEvidenceInputBundle{
		Label:     label,
		Status:    "missing",
		Directory: releaseEvidencePath(root, path),
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: label, Reason: "evidence directory not available", Evidence: out.Directory.PathID})
		return out
	}
	out.Status = "present"
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		full := filepath.Join(path, entry.Name())
		switch {
		case strings.HasSuffix(entry.Name(), ".metadata.md"):
			cmd := readReleaseCommandMetadata(root, path, full)
			out.Commands = append(out.Commands, cmd)
		case strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".txt"):
			artifact := releaseEvidenceArtifact{
				ID:           "artifact:" + localOpsStableID(full),
				Kind:         releaseArtifactKind(entry.Name()),
				Status:       "present",
				Path:         releaseEvidencePath(root, full),
				ArtifactHash: releaseEvidenceFileHash(full),
			}
			out.Artifacts = append(out.Artifacts, artifact)
		}
	}
	sort.Slice(out.Commands, func(i, j int) bool { return out.Commands[i].ID < out.Commands[j].ID })
	sort.Slice(out.Artifacts, func(i, j int) bool { return out.Artifacts[i].Path.RelativePath < out.Artifacts[j].Path.RelativePath })
	out.CommandCount = len(out.Commands)
	out.ArtifactCount = len(out.Artifacts)
	if out.CommandCount == 0 {
		out.Status = "partial"
		out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: label, Reason: "no command metadata artifacts found", Evidence: out.Directory.PathID})
	}
	return out
}

func readReleaseCommandMetadata(root string, dir string, path string) releaseEvidenceCommand {
	body, err := os.ReadFile(path)
	cmd := releaseEvidenceCommand{
		ID:     "command:" + localOpsStableID(path),
		Status: "missing",
	}
	if err != nil {
		cmd.Blocker = "metadata artifact could not be read"
		return cmd
	}
	lines := strings.Split(string(body), "\n")
	stdoutName := ""
	stderrName := ""
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = releaseMetadataValue(value)
		switch strings.TrimSpace(key) {
		case "Command":
			cmd.Command = releaseSafeCommand(value)
		case "Exit status":
			cmd.ExitStatus = releaseSafeToken(value)
		case "Stdout artifact":
			stdoutName = value
		case "Stderr artifact":
			stderrName = value
		case "Failure/blocker":
			if value != "" && value != "none recorded." && value != "none recorded" {
				cmd.Blocker = localOpsSafeText(value)
			}
		}
	}
	if cmd.Command == "" {
		cmd.Command = "command_id:" + localOpsStableID(string(body))
	}
	cmd.Status = releaseStatusFromExit(cmd.ExitStatus, cmd.Blocker)
	if stdoutName != "" {
		cmd.StdoutArtifact = releaseEvidencePath(root, filepath.Join(dir, stdoutName))
	}
	if stderrName != "" {
		cmd.StderrArtifact = releaseEvidencePath(root, filepath.Join(dir, stderrName))
	}
	return cmd
}

func buildReleaseValidationSection(current releaseEvidenceInputBundle, product productEvidenceExportReport) releaseEvidenceValidationSection {
	commands := []releaseEvidenceCommandStatus{
		releaseValidationCommand("context_status", "idiom tool context_status", current),
		releaseValidationCommand("harness_audit_tool_surface", `idiom tool harness_audit --input '{"purpose":"tool_surface"}'`, current),
		releaseValidationCommand("harness_audit_behavior", `idiom tool harness_audit --input '{"purpose":"behavior"}'`, current),
		releaseValidationCommand("swe_representative_suite", "go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json", current),
		releaseValidationCommand("go_test_all", "go test ./...", current),
		releaseValidationCommand("go_test_cmd_idiom", "go test ./cmd/idiom -count=1", current),
		releaseValidationCommand("git_diff_check", "git diff --check", current),
		{ID: "product_evidence_export", Command: "idiom product-evidence export", Status: product.Status, Source: "product_evidence"},
	}
	status := "pass"
	var blockers []releaseEvidenceBlocker
	for _, cmd := range commands {
		if cmd.Status == "pass" || cmd.Status == "evidence_present" {
			continue
		}
		status = "partial"
		blockers = append(blockers, releaseEvidenceBlocker{Section: "validation", Reason: "validation command evidence missing or incomplete", Evidence: cmd.ID})
	}
	return releaseEvidenceValidationSection{Status: status, Commands: commands, Blockers: blockers}
}

func releaseValidationCommand(id string, command string, current releaseEvidenceInputBundle) releaseEvidenceCommandStatus {
	for _, cmd := range current.Commands {
		if releaseCommandsMatch(cmd.Command, command) {
			status := "pass"
			if cmd.Status != "pass" {
				status = cmd.Status
			}
			return releaseEvidenceCommandStatus{ID: id, Command: command, Status: status, Source: cmd.ID, Blockers: p711Compact(cmd.Blocker)}
		}
	}
	return releaseEvidenceCommandStatus{ID: id, Command: command, Status: "missing", Blockers: []string{"current evidence artifact not supplied"}}
}

func buildReleaseAuditSecuritySection(current releaseEvidenceInputBundle, product productEvidenceExportReport, securityRef p711SourceReference) releaseEvidenceAuditSecuritySection {
	var artifacts []releaseEvidenceArtifact
	for _, artifact := range current.Artifacts {
		if strings.Contains(artifact.Path.RelativePath, "harness-audit") || strings.Contains(artifact.Path.RelativePath, "audit") {
			artifacts = append(artifacts, artifact)
		}
	}
	productAuditGate := p711GateFromSignal(product.Signals, "audit")
	status := productAuditGate
	var blockers []releaseEvidenceBlocker
	if len(artifacts) == 0 {
		status = "needs_evidence"
		blockers = append(blockers, releaseEvidenceBlocker{Section: "audit_security", Reason: "current harness audit artifacts not supplied"})
	}
	highFindingStatus := "not_available"
	if audit := releaseProductSignal(product.Signals, "audit"); audit.ID != "" && audit.Counts["quickstart_audit_findings"] == 0 {
		highFindingStatus = "no_quickstart_findings_recorded"
	}
	return releaseEvidenceAuditSecuritySection{
		Status:                status,
		HarnessAuditInputs:    artifacts,
		SecurityPostureSource: securityRef,
		SecurityPrivacyGate:   product.ProductSuccessGates.SecurityPrivacy,
		ProductAuditGate:      productAuditGate,
		HighFindingStatus:     highFindingStatus,
		Blockers:              blockers,
	}
}

func buildReleaseBenchmarkSection(root string, opts releaseEvidenceOptions, localOps *localOpsBenchmarkReport, ref p711SourceReference, competitiveCoverage p711ReleaseGateLinkage) releaseEvidenceBenchmarkSection {
	out := releaseEvidenceBenchmarkSection{
		Status:              "needs_evidence",
		CompetitiveCoverage: competitiveCoverage,
		LocalOps: releaseEvidenceLocalOps{
			Status:       "not_available",
			ReportSource: ref,
		},
	}
	localOpsPresent := false
	if localOps != nil {
		localOpsPresent = true
		out.LocalOps = releaseEvidenceLocalOps{
			Status:         localOps.Replay.Status,
			ReportSource:   ref,
			ProfileCount:   localOps.Summary.ProfileCount,
			SmokePassed:    localOps.Summary.SmokePassed,
			SmokeTotal:     localOps.Summary.SmokeTotal,
			ReplayLogCount: localOps.Summary.ReplayLogCount,
			BlockerCount:   localOps.Summary.BlockerCount,
			Privacy:        localOps.Privacy,
		}
		if localOps.Quickstart != nil {
			out.LocalOps.QuickstartStatus = localOps.Quickstart.Status
		}
		if localOps.Replay.Status != "pass" {
			out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: "benchmark_local_ops", Reason: "local-ops replay evidence did not pass", Evidence: localOps.Replay.Status})
		}
		if localOps.Summary.BlockerCount > 0 {
			out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: "benchmark_local_ops", Reason: "local-ops benchmark reported blockers", Evidence: strconv.Itoa(localOps.Summary.BlockerCount)})
		}
	} else {
		out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: "benchmark_local_ops", Reason: "local-ops benchmark report not supplied", Evidence: ref.ID})
	}
	if strings.TrimSpace(opts.BenchmarkReport) != "" {
		path := resolveReleaseEvidencePath(root, opts.BenchmarkReport)
		artifact := releaseEvidenceArtifact{
			ID:           "benchmark:" + localOpsStableID(path),
			Kind:         "benchmark_report",
			Status:       releaseFileStatus(path),
			Path:         releaseEvidencePath(root, path),
			ArtifactHash: releaseEvidenceFileHash(path),
		}
		out.CompetitiveSignals = append(out.CompetitiveSignals, artifact)
		if artifact.Status != "present" {
			out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: "benchmark_local_ops", Reason: "competitive benchmark artifact missing", Evidence: artifact.ID})
		}
	} else {
		out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: "benchmark_local_ops", Reason: "competitive benchmark artifact not supplied; P9.10 owns external-baseline gate"})
	}
	if competitiveCoverage.Status != "evidence_present" {
		for _, blocker := range competitiveCoverage.Blockers {
			out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: "benchmark_local_ops", Reason: "P9.10 competitive benchmark coverage incomplete", Evidence: blocker})
		}
	}
	out.Status = releaseBenchmarkSectionStatus(localOpsPresent, out.LocalOps.Status, out.Blockers)
	return out
}

func buildReleaseSidecarSection(root string, opts releaseEvidenceOptions, proofs p711IntegrationProofSet, ref p711SourceReference) releaseEvidenceSidecarSection {
	validity := p711ProofValidityFor(proofs)
	byPath := map[string][]p711IntegrationProof{}
	for _, proof := range proofs.Proofs {
		if validity.validProof(proof) {
			byPath[proof.Path] = append(byPath[proof.Path], proof)
		}
	}
	var paths []releaseEvidenceIntegrationPath
	status := "pass"
	var blockers []releaseEvidenceBlocker
	for _, required := range p711RequiredIntegrationPathTypes {
		row := releaseEvidenceIntegrationPath{Path: required, Status: "missing"}
		for _, proof := range byPath[required] {
			row.Status = "pass"
			row.EvidenceIDs = append(row.EvidenceIDs, proof.ID)
			if proof.ReplayFixture != "" {
				row.ReplayFixture = releaseEvidencePath(root, resolveReleaseEvidencePath(root, proof.ReplayFixture))
			}
		}
		if required == "mcp_stdio" {
			hasParity := false
			for _, proof := range proofs.Proofs {
				if p711MCPStdioProofHasParity(proof, validity) {
					hasParity = true
				}
			}
			if !hasParity {
				row.Status = "partial"
				row.Blocker = "MCP stdio replay parity evidence missing"
			}
		}
		if row.Status != "pass" {
			status = "partial"
			if row.Blocker == "" {
				row.Blocker = "required sidecar integration proof missing"
			}
			blockers = append(blockers, releaseEvidenceBlocker{Section: "sidecar_integration", Reason: row.Blocker, Evidence: required})
		}
		paths = append(paths, row)
	}
	smokePath := resolveReleaseEvidencePath(root, opts.SidecarSmokeReport)
	smoke := releaseEvidenceArtifact{
		ID:           "sidecar-smoke:" + localOpsStableID(smokePath),
		Kind:         "sidecar_smoke_transcript",
		Status:       releaseFileStatus(smokePath),
		Path:         releaseEvidencePath(root, smokePath),
		ArtifactHash: releaseEvidenceFileHash(smokePath),
	}
	if smoke.Status != "present" {
		status = "partial"
		blockers = append(blockers, releaseEvidenceBlocker{Section: "sidecar_integration", Reason: "sidecar smoke transcript not supplied", Evidence: smoke.Path.PathID})
	}
	return releaseEvidenceSidecarSection{Status: status, SmokeTranscript: smoke, IntegrationProof: ref, Paths: paths, Blockers: blockers}
}

func buildReleaseClientReadiness(rows []p711ClassCoverage) []releaseEvidenceClientReadiness {
	byClass := map[string]p711ClassCoverage{}
	for _, row := range rows {
		byClass[row.Classification] = row
	}
	out := make([]releaseEvidenceClientReadiness, 0, len(releaseEvidenceClientClassifications))
	for _, class := range releaseEvidenceClientClassifications {
		row := byClass[class]
		status := row.Status
		if status == "" {
			status = "missing"
		}
		var missing []string
		if row.ClientDemoTranscriptID == "" {
			missing = append(missing, "client demo transcript")
		}
		if !row.SecurityPostureCoverage {
			missing = append(missing, "security/privacy posture row")
		}
		if len(row.IntegrationProofIDs) == 0 {
			missing = append(missing, "sidecar integration proof IDs")
		}
		if row.ValidationRecordCount == 0 {
			missing = append(missing, "validation record")
		}
		status = releaseClientReadinessStatus(status, missing)
		out = append(out, releaseEvidenceClientReadiness{
			Classification:         class,
			Status:                 status,
			Stance:                 localOpsSafeText(firstNonEmptyCLIString(row.Stance, p711DefaultStance(class))),
			ArchitecturePrimitives: releaseArchitecturePrimitives(class),
			EvidenceIDs:            p711Compact(row.P76CommandID, row.ClientDemoTranscriptID),
			MissingEvidence:        missing,
			Limitations:            row.Limitations,
		})
	}
	return out
}

func releaseClientReadinessStatus(status string, missing []string) string {
	status = strings.TrimSpace(status)
	if len(missing) == 0 {
		if status == "" {
			return "missing"
		}
		return status
	}
	switch status {
	case "partial", "needs_evidence":
		return status
	case "blocked", "fail":
		return status
	default:
		return "needs_evidence"
	}
}

func collectReleaseEvidenceBlockers(report releaseEvidenceBundle) []releaseEvidenceBlocker {
	var blockers []releaseEvidenceBlocker
	blockers = append(blockers, report.BaselineCurrent.Baseline.Blockers...)
	blockers = append(blockers, report.BaselineCurrent.Current.Blockers...)
	blockers = append(blockers, report.Validation.Blockers...)
	blockers = append(blockers, report.AuditSecurity.Blockers...)
	blockers = append(blockers, report.BenchmarkLocalOps.Blockers...)
	blockers = append(blockers, report.ActivationOnboarding.Blockers...)
	blockers = append(blockers, report.SidecarIntegration.Blockers...)
	if report.ProductEvidence.Status != "pass" {
		blockers = append(blockers, releaseEvidenceBlocker{Section: "product_evidence", Reason: "product-evidence export is not complete", Evidence: report.ProductEvidence.Status})
	}
	for _, row := range report.ClientReadiness {
		if len(row.MissingEvidence) > 0 {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "client_readiness", Reason: "classification required evidence missing", Evidence: row.Classification})
			continue
		}
		if row.Status != "pass" {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "client_readiness", Reason: "classification evidence incomplete", Evidence: row.Classification})
		}
	}
	for _, gate := range report.ReleaseReadiness {
		if gate.Gate == "P9.7" && gate.Status != "evidence_present" {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "release_readiness", Reason: "P9.7 design-partner demand evidence incomplete", Evidence: gate.Status})
		}
		if gate.Gate == "P9.8" && gate.Status != "evidence_present" {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "release_readiness", Reason: "P9.8 integration proof evidence incomplete", Evidence: gate.Status})
		}
		if gate.Gate == "P9.9" && gate.Status != "evidence_present" {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "release_readiness", Reason: "P9.9 security/privacy posture evidence incomplete", Evidence: gate.Status})
		}
		if gate.Gate == "P9.10" && gate.Status != "evidence_present" {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "release_readiness", Reason: "P9.10 competitive benchmark evidence incomplete", Evidence: gate.Status})
		}
		if gate.Gate == "P9.11" && gate.Status != "evidence_present" {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "release_readiness", Reason: "P9.11 client demo transcript evidence incomplete", Evidence: gate.Status})
		}
		if gate.Gate == "P9.12" && gate.Status != "evidence_present" {
			blockers = append(blockers, releaseEvidenceBlocker{Section: "release_readiness", Reason: "P9.12 packaging/support decision evidence incomplete", Evidence: gate.Status})
		}
	}
	return releaseEvidenceCompactBlockers(blockers)
}

func releaseEvidenceStatus(report releaseEvidenceBundle) string {
	if report.Privacy.ForbiddenFieldCheck != "pass" || report.ProductEvidence.Status == "blocked" {
		return "blocked"
	}
	if len(report.Blockers) > 0 {
		return "partial"
	}
	return "pass"
}

func releaseEvidenceDeltas(baseline releaseEvidenceInputBundle, current releaseEvidenceInputBundle) []releaseEvidenceDelta {
	return []releaseEvidenceDelta{
		{Metric: "command_count", Baseline: baseline.CommandCount, Current: current.CommandCount, Status: releaseDeltaStatus(baseline.CommandCount, current.CommandCount)},
		{Metric: "artifact_count", Baseline: baseline.ArtifactCount, Current: current.ArtifactCount, Status: releaseDeltaStatus(baseline.ArtifactCount, current.ArtifactCount)},
	}
}

func releaseDeltaStatus(baseline int, current int) string {
	if baseline == 0 || current == 0 {
		return "needs_evidence"
	}
	return "present"
}

func releaseBenchmarkSectionStatus(localOpsPresent bool, localOpsStatus string, blockers []releaseEvidenceBlocker) string {
	if !localOpsPresent {
		return "needs_evidence"
	}
	if len(blockers) > 0 {
		return "partial"
	}
	if localOpsStatus == "pass" {
		return "pass"
	}
	return "partial"
}

func buildReleaseActivationSection(localOps *localOpsBenchmarkReport, ref p711SourceReference, validation releaseEvidenceValidationSection, clients []releaseEvidenceClientReadiness) releaseEvidenceActivationSection {
	out := releaseEvidenceActivationSection{
		Status:           "needs_evidence",
		Classification:   "no-go",
		ReportSource:     ref,
		ValidationStatus: validation.Status,
	}
	if localOps == nil || localOps.Quickstart == nil {
		out.Blockers = append(out.Blockers, releaseEvidenceBlocker{Section: "activation_onboarding", Reason: "quickstart activation smoke not supplied", Evidence: ref.ID})
		out.Blockers = append(out.Blockers, releaseActivationValidationBlockers(validation, clients)...)
		return out
	}
	q := localOps.Quickstart
	out.SmokeID = q.SmokeID
	out.QuickstartStatus = q.Status
	out.ClientClassification = q.ClientClassification
	out.EnvironmentClass = q.EnvironmentClass
	out.InstallSetupPath = q.SetupInstallReadiness.InstallSetupPath
	out.ReleaseInstallProof = q.SetupInstallReadiness.ReleaseInstallProof
	out.AssistanceClass = q.AssistanceClass
	out.DurationSource = q.DurationSource
	out.TimeToFirstPacketSeconds = q.TimeToFirstPacketSeconds
	out.TimeToTrustEvidenceSeconds = q.TimeToTrustEvidenceSeconds
	out.FirstUsefulPacketStatus = q.FirstUsefulPacket.Status
	out.FreshnessStatus = q.FreshnessCheck.Status
	out.AuditStatus = q.AuditReport.Status
	out.SuggestedVerificationStatus = q.SuggestedVerification.Status
	out.ProductEvidenceSafeStatus = q.ProductEvidenceSafeOutput.Status
	out.FailureReasonStatus = q.FailureReason.Status
	out.DocsGapStatus = q.DocsGap.Status
	out.CleanupStatus = q.Cleanup.Status
	out.CleanupMode = q.Cleanup.Mode
	out.PrivacyStatus = q.PrivacyRedactionStatus.Status
	out.ForbiddenFieldCheck = q.PrivacyRedactionStatus.ForbiddenFieldCheck
	out.EvidenceIDs = p711Compact(q.SmokeID, q.ProductEvidenceSafeOutput.ExportID, q.FirstUsefulPacket.PacketID, q.FirstUsefulPacket.IndexID, q.AuditReport.AuditReportID)

	out.Blockers = append(releaseActivationBlockers(q), releaseActivationValidationBlockers(validation, clients)...)
	out.Blockers = releaseEvidenceCompactBlockers(out.Blockers)
	if len(out.Blockers) == 0 {
		out.Status = "pass"
		out.Classification = "pass"
		return out
	}
	out.Status = p96ActivationStatusFromBlockers(out.Blockers)
	return out
}

func p96ActivationReleaseGateStatus(localOps *localOpsBenchmarkReport) string {
	if localOps == nil || localOps.Quickstart == nil {
		return "needs_evidence"
	}
	status := p96ActivationStatusFromBlockers(releaseActivationBlockers(localOps.Quickstart))
	if status == "blocked" {
		return "blocked"
	}
	return "needs_evidence"
}

func p96ActivationStatusFromBlockers(blockers []releaseEvidenceBlocker) string {
	if len(blockers) == 0 {
		return "pass"
	}
	for _, blocker := range blockers {
		switch blocker.Reason {
		case "privacy-safe activation evidence failed",
			"privacy/redaction status is not release-safe",
			"quickstart activation smoke did not pass":
			return "blocked"
		}
	}
	return "needs_evidence"
}

func releaseActivationBlockers(q *localOpsQuickstartSmoke) []releaseEvidenceBlocker {
	var blockers []releaseEvidenceBlocker
	add := func(reason string, evidence string) {
		blockers = append(blockers, releaseEvidenceBlocker{Section: "activation_onboarding", Reason: reason, Evidence: evidence})
	}
	if q == nil {
		add("quickstart activation smoke not supplied", "missing")
		return blockers
	}
	if q.Status != "pass" {
		add("quickstart activation smoke did not pass", firstNonEmptyCLIString(q.Status, "missing"))
	}
	if !releaseActivationApprovedEnvironmentClass(q.EnvironmentClass) {
		add("activation environment class missing or not approved", firstNonEmptyCLIString(q.EnvironmentClass, "missing"))
	}
	if q.SetupInstallReadiness.Status != "pass" {
		add("setup/source-build readiness did not pass", firstNonEmptyCLIString(q.SetupInstallReadiness.Status, "missing"))
	}
	if !releaseActivationApprovedSetupProof(q) {
		add("release binary clean-install proof or scoped clean-setup proof not supplied", firstNonEmptyCLIString(q.SetupInstallReadiness.ReleaseInstallProof, q.SetupInstallReadiness.InstallSetupPath, "missing"))
	}
	if q.DurationSource != "measured_wall_clock" {
		add("install-to-first-value timing is not measured wall-clock evidence", firstNonEmptyCLIString(q.DurationSource, "missing"))
	}
	if q.TimeToFirstPacketSeconds <= 0 || q.TimeToTrustEvidenceSeconds <= 0 || q.TimeToTrustEvidenceSeconds < q.TimeToFirstPacketSeconds {
		add("install-to-first-value timings missing or inconsistent", fmt.Sprintf("first:%g trust:%g", q.TimeToFirstPacketSeconds, q.TimeToTrustEvidenceSeconds))
	}
	if q.FirstUsefulPacket.Status != "pass" || q.FirstUsefulPacket.PacketID == "" || q.FirstUsefulPacket.IndexID == "" || q.FirstUsefulPacket.FreshnessState == "" ||
		!localOpsActivationPacketHasUsefulEvidence(q.FirstUsefulPacket) {
		add("first useful packet evidence incomplete", firstNonEmptyCLIString(q.FirstUsefulPacket.Status, "missing"))
	}
	if q.FreshnessCheck.Status != "pass" || q.FreshnessCheck.State == "" {
		add("freshness evidence incomplete", firstNonEmptyCLIString(q.FreshnessCheck.Status, "missing"))
	}
	if q.AuditReport.Status != "pass" || q.AuditReport.AuditReportID == "" {
		add("audit evidence incomplete", firstNonEmptyCLIString(q.AuditReport.Status, "missing"))
	}
	if q.SuggestedVerification.Status != "pass" || len(q.SuggestedVerification.SuggestedCommands) == 0 {
		add("suggested verification evidence incomplete", firstNonEmptyCLIString(q.SuggestedVerification.Status, "missing"))
	}
	if q.ProductEvidenceSafeOutput.Status != "pass" || q.ProductEvidenceSafeOutput.ForbiddenFieldCheck != "pass" {
		add("privacy-safe activation evidence failed", firstNonEmptyCLIString(q.ProductEvidenceSafeOutput.ForbiddenFieldCheck, q.ProductEvidenceSafeOutput.Status))
	}
	if q.PrivacyRedactionStatus.Status != "pass" || q.PrivacyRedactionStatus.ForbiddenFieldCheck != "pass" ||
		q.PrivacyRedactionStatus.RawPromptCaptured || q.PrivacyRedactionStatus.RawFileBodyCaptured ||
		q.PrivacyRedactionStatus.SecretCaptured || q.PrivacyRedactionStatus.PrivatePathCaptured ||
		q.PrivacyRedactionStatus.PrivateRepoURLCaptured || q.PrivacyRedactionStatus.UnredactedLogCaptured ||
		q.PrivacyRedactionStatus.HostedTelemetryRequired {
		add("privacy/redaction status is not release-safe", firstNonEmptyCLIString(q.PrivacyRedactionStatus.ForbiddenFieldCheck, q.PrivacyRedactionStatus.Status))
	}
	if !releaseActivationFailureReasonExplicit(q.FailureReason) {
		add("failure reason semantics missing or unresolved", firstNonEmptyCLIString(q.FailureReason.Status, q.FailureReason.PrimaryClass, "missing"))
	}
	if !releaseActivationDocsGapExplicit(q.DocsGap) {
		add("docs gap semantics missing or unresolved", firstNonEmptyCLIString(q.DocsGap.Status, "missing"))
	}
	if !releaseActivationCleanupSemanticsPass(q.Cleanup) {
		add("cleanup evidence missing full cleanup or reviewed retained-state semantics", firstNonEmptyCLIString(q.Cleanup.Status, q.Cleanup.ExitStatus, q.Cleanup.Mode, "missing"))
	}
	switch q.AssistanceClass {
	case "none", "docs-only", "operator typo correction":
	default:
		add("assistance class missing or exceeds release activation threshold", firstNonEmptyCLIString(q.AssistanceClass, "missing"))
	}
	return releaseEvidenceCompactBlockers(blockers)
}

func releaseActivationValidationBlockers(validation releaseEvidenceValidationSection, clients []releaseEvidenceClientReadiness) []releaseEvidenceBlocker {
	var blockers []releaseEvidenceBlocker
	if validation.Status != "pass" {
		blockers = append(blockers, releaseEvidenceBlocker{
			Section:  "activation_onboarding",
			Reason:   "current release validation evidence missing or incomplete",
			Evidence: firstNonEmptyCLIString(validation.Status, "missing"),
		})
	}
	missingValidationRecords := 0
	for _, row := range clients {
		if releaseEvidenceSliceContains(row.MissingEvidence, "validation record") {
			missingValidationRecords++
		}
	}
	if missingValidationRecords > 0 || len(clients) == 0 {
		evidence := "missing"
		if missingValidationRecords > 0 {
			evidence = fmt.Sprintf("missing_classification_records:%d", missingValidationRecords)
		}
		blockers = append(blockers, releaseEvidenceBlocker{
			Section:  "activation_onboarding",
			Reason:   "current validation records missing for activation readiness",
			Evidence: evidence,
		})
	}
	return releaseEvidenceCompactBlockers(blockers)
}

func releaseEvidenceSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func releaseActivationApprovedEnvironmentClass(value string) bool {
	switch strings.TrimSpace(value) {
	case "clean-install", "clean-setup":
		return true
	default:
		return false
	}
}

func releaseActivationApprovedSetupProof(q *localOpsQuickstartSmoke) bool {
	installPath := strings.TrimSpace(q.SetupInstallReadiness.InstallSetupPath)
	proof := strings.TrimSpace(q.SetupInstallReadiness.ReleaseInstallProof)
	switch installPath {
	case "release-binary":
		return proof == "pass"
	case "clean-setup", "documented-clean-setup", "source-build":
		return q.EnvironmentClass == "clean-setup" && proof == "scoped_clean_setup"
	default:
		return false
	}
}

func releaseActivationFailureReasonExplicit(reason localOpsActivationFailureReason) bool {
	return strings.TrimSpace(reason.Status) == "none" &&
		strings.TrimSpace(reason.PrimaryClass) == "none" &&
		strings.TrimSpace(reason.Reason) == "" &&
		!reason.RetryAllowed
}

func releaseActivationDocsGapExplicit(gap localOpsActivationDocsGap) bool {
	switch strings.TrimSpace(gap.Status) {
	case "none":
		return !gap.GapRecorded && !gap.BlockedActivation
	case "recorded":
		return gap.GapRecorded &&
			strings.TrimSpace(gap.DocPath) != "" &&
			strings.TrimSpace(gap.CommandLabel) != "" &&
			strings.TrimSpace(gap.ExpectedOutput) != "" &&
			strings.TrimSpace(gap.ActualOutput) != "" &&
			strings.TrimSpace(gap.Severity) != "" &&
			!gap.BlockedActivation
	default:
		return false
	}
}

func releaseActivationCleanupSemanticsPass(cleanup localOpsActivationCleanupEvidence) bool {
	if cleanup.Status != "pass" || cleanup.ExitStatus != "pass" || strings.TrimSpace(cleanup.Mode) == "" || strings.TrimSpace(cleanup.RemainingState) == "" {
		return false
	}
	if cleanup.RemovedArtifactCount <= 0 && len(cleanup.EvidenceIDs) == 0 {
		return false
	}
	remaining := strings.TrimSpace(cleanup.RemainingState)
	if releaseActivationAbsentLocalState(cleanup.Mode, remaining) {
		return true
	}
	return strings.Contains(remaining, "retained") && releaseActivationReviewedRetainedStatePolicy(cleanup.RetainedStatePolicy)
}

func releaseActivationAbsentLocalState(mode string, remaining string) bool {
	switch strings.TrimSpace(mode) {
	case "remove-local-state", "uninstall", "clean-uninstall":
	default:
		return false
	}
	switch strings.TrimSpace(remaining) {
	case "comprehension_state_absent", "local_state_absent", "state_absent":
		return true
	default:
		return false
	}
}

func releaseActivationReviewedRetainedStatePolicy(policy string) bool {
	switch strings.TrimSpace(policy) {
	case "reviewed_retained_state_policy",
		"reviewed_context_graph_retention_policy",
		"reviewed_local_state_retention_policy":
		return true
	default:
		return false
	}
}

func releaseReadinessFromProduct(rows []p711ReleaseGateLinkage, clients []releaseEvidenceClientReadiness, activation releaseEvidenceActivationSection) []releaseEvidenceGateReadiness {
	rows = releaseGateLinkageFromProduct(rows, clients, activation)
	out := make([]releaseEvidenceGateReadiness, 0, len(rows))
	for _, row := range rows {
		out = append(out, releaseEvidenceGateReadiness{
			Gate:        row.Gate,
			Status:      row.Status,
			Evidence:    row.Evidence,
			ClaimImpact: row.ClaimImpact,
			Counts:      row.Counts,
			Blockers:    row.Blockers,
			EvidenceIDs: row.EvidenceIDs,
		})
	}
	return out
}

func releaseGateLinkageByGate(rows []p711ReleaseGateLinkage, gate string) p711ReleaseGateLinkage {
	for _, row := range rows {
		if row.Gate == gate {
			return row
		}
	}
	return p711ReleaseGateLinkage{
		Gate:        gate,
		Status:      "needs_evidence",
		Evidence:    "release gate linkage missing",
		ClaimImpact: "release claims remain no-go until gate evidence is present",
		Blockers:    []string{"release gate linkage missing: " + gate},
	}
}

func releaseGateLinkageFromProduct(rows []p711ReleaseGateLinkage, clients []releaseEvidenceClientReadiness, activation releaseEvidenceActivationSection) []p711ReleaseGateLinkage {
	p911Status := "needs_evidence"
	for _, row := range rows {
		if row.Gate == "P9.11" {
			p911Status = row.Status
			break
		}
	}
	p912Status := releaseP912ReadinessStatus(clients, p911Status)
	out := make([]p711ReleaseGateLinkage, 0, len(rows)+1)
	foundP912 := false
	foundP96 := false
	for _, row := range rows {
		if row.Gate == "P9.6" {
			row = releaseP96GateLinkage(row, activation)
			foundP96 = true
		}
		if row.Gate == "P9.12" {
			row = releaseP912GateLinkage(row, p912Status)
			foundP912 = true
		}
		out = append(out, row)
	}
	if !foundP912 {
		out = append(out, p711ReleaseGateLinkage{
			Gate:        "P9.12",
			Status:      p912Status,
			Evidence:    "downgraded client readiness rows and limitations",
			ClaimImpact: "packaging/support thesis must cite downgraded classification statuses",
		})
	}
	if !foundP96 {
		out = append(out, releaseP96GateLinkage(p711ReleaseGateLinkage{
			Gate:        "P9.6",
			Status:      "needs_evidence",
			Evidence:    "activation quickstart smoke or blocker summary",
			ClaimImpact: "activation claims depend on pass status",
		}, activation))
	}
	return out
}

func releaseP912GateLinkage(row p711ReleaseGateLinkage, releaseStatus string) p711ReleaseGateLinkage {
	if row.Counts == nil {
		row.Counts = map[string]int{}
	}
	if row.Status != "evidence_present" {
		row.Counts["blockers"] = len(row.Blockers)
		return row
	}
	if releaseStatus == "evidence_present" {
		return row
	}
	row.Status = "needs_evidence"
	row.Evidence = strings.TrimSpace(row.Evidence + "; downgraded client readiness rows and limitations")
	row.ClaimImpact = "packaging/support thesis must cite downgraded classification statuses before release claims"
	row.Blockers = p711Compact(append(row.Blockers, "release client readiness or P9.11 evidence incomplete")...)
	row.Counts["release_client_readiness_blockers"]++
	row.Counts["blockers"] = len(row.Blockers)
	return row
}

func releaseP96GateLinkage(row p711ReleaseGateLinkage, activation releaseEvidenceActivationSection) p711ReleaseGateLinkage {
	if activation.Status == "pass" {
		row.Status = "evidence_present"
		row.Evidence = "measured activation/onboarding evidence with cleanup"
		row.ClaimImpact = "activation claims may cite measured clean-install evidence"
		return row
	}
	if activation.Status == "blocked" {
		row.Status = "blocked"
	} else {
		row.Status = "needs_evidence"
	}
	row.Evidence = "activation/onboarding evidence incomplete or downgraded"
	row.ClaimImpact = "release activation claims are no-go until measured install-to-first-value and cleanup evidence pass"
	return row
}

func releaseP912ReadinessStatus(clients []releaseEvidenceClientReadiness, p911Status string) string {
	if len(clients) == 0 || p911Status != "evidence_present" {
		return "needs_evidence"
	}
	for _, row := range clients {
		if row.Status != "pass" || len(row.MissingEvidence) > 0 {
			return "needs_evidence"
		}
	}
	return "evidence_present"
}

func releaseArchitecturePrimitives(class string) []string {
	common := []string{"local_sidecar_contract", "metadata_only_evidence", "profile_gates", "redaction_contract", "reviewable_artifacts"}
	switch class {
	case "finance", "healthcare", "public-sector":
		return append(common, "protected_path_posture", "control_mapping")
	case "tooling-vendor":
		return append(common, "schema_parity", "mcp_stdio_transport")
	case "local-model":
		return append(common, "local_ops_readiness", "degraded_mode_reporting")
	default:
		return append(common, "context_freshness", "verification_evidence")
	}
}

func releaseCommandsMatch(actual string, expected string) bool {
	actual = strings.ToLower(strings.TrimSpace(actual))
	expected = strings.ToLower(strings.TrimSpace(expected))
	return actual == expected || strings.Contains(actual, expected) || strings.Contains(expected, actual)
}

func releaseMetadataValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`")
	return strings.TrimSpace(value)
}

func releaseStatusFromExit(exit string, blocker string) string {
	if strings.TrimSpace(blocker) != "" {
		return "blocked"
	}
	switch strings.TrimSpace(exit) {
	case "0":
		return "pass"
	case "":
		return "partial"
	default:
		return "fail"
	}
}

func releaseFileStatus(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "present"
	}
	return "missing"
}

func releaseArtifactKind(name string) string {
	switch {
	case strings.HasSuffix(name, ".stdout.json"):
		return "stdout_json"
	case strings.HasSuffix(name, ".stderr.txt"):
		return "stderr_text"
	case strings.HasSuffix(name, ".json"):
		return "json"
	case strings.HasSuffix(name, ".txt"):
		return "text"
	default:
		return "artifact"
	}
}

func releaseEvidenceFileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return p711Hash(data)
}

func releaseEvidencePath(root string, path string) releaseEvidencePathRef {
	path = strings.TrimSpace(path)
	if path == "" {
		return releaseEvidencePathRef{}
	}
	ref := releaseEvidencePathRef{PathID: localOpsPathEvidence(path)}
	absRoot, rootErr := filepath.Abs(root)
	absPath, pathErr := filepath.Abs(path)
	if rootErr == nil && pathErr == nil {
		if rel, err := filepath.Rel(absRoot, absPath); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel) {
			ref.RelativePath = filepath.ToSlash(filepath.Clean(rel))
		}
	}
	return ref
}

func resolveReleaseEvidencePath(root string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func releaseSafeCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if strings.Contains(command, "\n") || strings.Contains(command, "\r") {
		return "command_id:" + localOpsStableID(command)
	}
	if forbidden := p711ForbiddenValidationString(command); forbidden != "" {
		return "command_id:" + localOpsStableID(command)
	}
	return command
}

func releaseProductSignal(signals []p711SignalSummary, id string) p711SignalSummary {
	for _, signal := range signals {
		if signal.ID == id {
			return signal
		}
	}
	return p711SignalSummary{}
}

func releaseSafeToken(value string) string {
	if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return strconv.Itoa(n)
	}
	return p711SafeToken(value)
}

func releaseEvidenceHash(report releaseEvidenceBundle) string {
	report.GeneratedAt = ""
	report.BundleID = ""
	report.ArtifactHash = ""
	body, err := json.Marshal(report)
	if err != nil {
		return localOpsStableID("release-evidence-bundle")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8])
}

func releaseEvidenceBuildInfo() releaseEvidenceBuildMetadata {
	resolvedCommit, resolvedDate := commit, date
	if resolvedCommit == "" || resolvedDate == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if resolvedCommit == "" {
						resolvedCommit = s.Value
					}
				case "vcs.time":
					if resolvedDate == "" {
						resolvedDate = s.Value
					}
				}
			}
		}
	}
	return releaseEvidenceBuildMetadata{
		Version: version,
		Commit:  resolvedCommit,
		Date:    resolvedDate,
		Go:      runtime.Version(),
		OSArch:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func releaseEvidenceCompactBlockers(blockers []releaseEvidenceBlocker) []releaseEvidenceBlocker {
	seen := map[string]bool{}
	var out []releaseEvidenceBlocker
	for _, blocker := range blockers {
		key := blocker.Section + "\x00" + blocker.Reason + "\x00" + blocker.Evidence
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, blocker)
	}
	return out
}

func sanitizeReleaseEvidenceTelemetryArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := strings.TrimSpace(out[i])
		if flag, value, ok := strings.Cut(arg, "="); ok {
			if releaseEvidencePathFlag(flag) {
				out[i] = flag + "=" + redactedReleaseEvidencePathArg(value)
			}
			continue
		}
		if releaseEvidencePathFlag(arg) && i+1 < len(out) {
			i++
			out[i] = redactedReleaseEvidencePathArg(out[i])
		}
	}
	return out
}

func releaseEvidencePathFlag(flag string) bool {
	switch flag {
	case "--project-root", "--output", "--baseline-dir", "--current-dir", "--local-ops-report", "--validation-records", "--benchmark-report", "--sidecar-smoke":
		return true
	default:
		return false
	}
}

func redactedReleaseEvidencePathArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	return "<redacted_release_evidence_path id=" + localOpsStableID(value) + ">"
}

func printReleaseUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: idiom release evidence [--project-root <root>] [--baseline-dir <dir>] [--current-dir <dir>] [--local-ops-report <report.json>] [--validation-records <records.json>] [--benchmark-report <report.json>] [--sidecar-smoke <transcript.json>] [--output <path>]")
}
