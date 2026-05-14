package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/comprehension"
)

func TestP91ReleaseEvidenceBundleIncludesRequiredSectionsAndClassifications(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "baseline", releaseEvidenceBaselineCommands())
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())
	writeP711JSON(t, filepath.Join(root, "testdata", "sidecar", "discovery-smoke.transcript.json"), map[string]any{
		"schema_version": "idiom.sidecar_smoke_transcript.v1",
		"name":           "p9.1-smoke",
	})
	benchmarkPath := filepath.Join(root, "testdata", "benchmarks", "localization.json")
	writeP711JSON(t, benchmarkPath, map[string]any{"schema_version": "idiom.localization_benchmark.v1", "status": "pass"})
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    "eval",
		Action:    "boost",
		State:     "accepted",
		Reason:    "grounded",
		Evidence:  &comprehension.LearningEvidenceRef{Kind: "eval_result", Verified: true, ResultID: "result:p91"},
	}); err != nil {
		t.Fatalf("append learning log: %v", err)
	}

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:        root,
		BaselineDir:        "baseline",
		CurrentDir:         "current",
		LocalOpsReport:     localOpsPath,
		ValidationRecords:  recordsPath,
		BenchmarkReport:    benchmarkPath,
		SidecarSmokeReport: filepath.Join("testdata", "sidecar", "discovery-smoke.transcript.json"),
	})
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal release evidence: %v", err)
	}
	if report.SchemaVersion != releaseEvidenceSchemaVersion || report.BundleID == "" || report.ArtifactHash == "" {
		t.Fatalf("release evidence missing identity fields: %#v", report)
	}
	if report.Status != "pass" {
		t.Fatalf("complete fixtures should produce pass bundle, got status=%q blockers=%#v", report.Status, report.Blockers)
	}
	if report.BaselineCurrent.Baseline.CommandCount == 0 || report.BaselineCurrent.Current.CommandCount == 0 {
		t.Fatalf("baseline/current command evidence missing: %#v", report.BaselineCurrent)
	}
	if report.Validation.Status != "pass" || len(report.Validation.Commands) == 0 {
		t.Fatalf("validation section missing commands/statuses: %#v", report.Validation)
	}
	if report.AuditSecurity.Status != "evidence_present" || len(report.AuditSecurity.HarnessAuditInputs) == 0 {
		t.Fatalf("audit/security section missing current audit proof: %#v", report.AuditSecurity)
	}
	if report.BenchmarkLocalOps.Status != "pass" || report.BenchmarkLocalOps.LocalOps.SmokeTotal == 0 || len(report.BenchmarkLocalOps.CompetitiveSignals) == 0 {
		t.Fatalf("benchmark/local-ops section incomplete: %#v", report.BenchmarkLocalOps)
	}
	if report.SidecarIntegration.Status != "pass" || !releaseEvidenceHasIntegrationPath(report.SidecarIntegration.Paths, "shell_ci_script") ||
		!releaseEvidenceHasIntegrationPath(report.SidecarIntegration.Paths, "external_coding_agent_workflow") ||
		!releaseEvidenceHasIntegrationPath(report.SidecarIntegration.Paths, "mcp_stdio") {
		t.Fatalf("sidecar section should include all required integration paths: %#v", report.SidecarIntegration)
	}
	if report.ProductEvidence.Status != "pass" || report.ProductEvidence.SignalCompleteness.Status != "complete" {
		t.Fatalf("product evidence section should consume P7/P8 inputs: %#v", report.ProductEvidence)
	}
	if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.7"); gate.Status != "evidence_present" {
		t.Fatalf("complete P9.7 validation fixture should satisfy release readiness: %#v", gate)
	} else if gate.Counts["complete_records"] == 0 || gate.Counts["primary_icp_records"] != 3 ||
		gate.Counts["daily_user_activation_records"] != 3 || len(gate.EvidenceIDs) != 3 || len(gate.Blockers) != 0 {
		t.Fatalf("complete P9.7 release readiness should expose structured gate data: %#v", gate)
	}
	if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.8"); gate.Status != "evidence_present" {
		t.Fatalf("complete integration fixture should satisfy P9.8 release readiness: %#v", gate)
	} else if gate.Counts["executable_required_paths"] != 3 ||
		gate.Counts["mcp_stdio_parity_paths"] != 1 ||
		len(gate.Blockers) != 0 ||
		!stringSliceContains(gate.EvidenceIDs, "p7.8-mcp-stdio") {
		t.Fatalf("P9.8 release readiness should expose structured gate data: %#v", gate)
	}
	assertReleaseEvidenceClassifications(t, report.ClientReadiness)
	if forbidden := localOpsForbiddenEvidenceLeak(string(body)); forbidden != "" {
		t.Fatalf("release evidence leaked forbidden class %q: %s", forbidden, string(body))
	}
}

func TestP91ReleaseEvidenceMarksPartialWhenEvidenceMissing(t *testing.T) {
	root := t.TempDir()
	report := buildReleaseEvidenceBundle(root, laneHistorySummary{}, releaseEvidenceOptions{
		ProjectRoot:        root,
		BaselineDir:        "missing-baseline",
		CurrentDir:         "missing-current",
		SidecarSmokeReport: "missing-smoke.json",
	})
	if report.Status != "partial" {
		t.Fatalf("missing evidence should produce partial status, got %q", report.Status)
	}
	for _, section := range []string{"baseline", "current", "validation", "benchmark_local_ops", "sidecar_integration", "product_evidence"} {
		if !releaseEvidenceHasBlocker(report.Blockers, section) {
			t.Fatalf("expected blocker for %q, got %#v", section, report.Blockers)
		}
	}
	if report.BaselineCurrent.Baseline.Status != "missing" || report.BaselineCurrent.Current.Status != "missing" {
		t.Fatalf("baseline/current missing status not recorded: %#v", report.BaselineCurrent)
	}
	if report.Validation.Status != "partial" {
		t.Fatalf("validation should be partial when current command artifacts are absent: %#v", report.Validation)
	}
	assertReleaseEvidenceClassifications(t, report.ClientReadiness)
}

func TestP913ReleaseEvidenceDowngradesClientRowsWhenValidationRecordsMissing(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	benchmarkPath := filepath.Join(root, "testdata", "benchmarks", "localization.json")
	writeP711JSON(t, benchmarkPath, map[string]any{"schema_version": "idiom.localization_benchmark.v1", "status": "pass"})

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:     root,
		LocalOpsReport:  localOpsPath,
		BenchmarkReport: benchmarkPath,
	})

	for _, row := range report.ClientReadiness {
		if !releaseEvidenceStringSliceContains(row.MissingEvidence, "validation record") {
			t.Fatalf("classification %q should list missing validation evidence: %#v", row.Classification, row)
		}
		if row.Status == "pass" {
			t.Fatalf("classification %q with missing evidence must not pass: %#v", row.Classification, row)
		}
		if !releaseEvidenceHasClientReadinessBlocker(report.Blockers, row.Classification) {
			t.Fatalf("classification %q missing per-class blocker: %#v", row.Classification, report.Blockers)
		}
	}
}

func TestP913ReleaseReadinessNeedsEvidenceWhenClientValidationMissing(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	benchmarkPath := filepath.Join(root, "testdata", "benchmarks", "localization.json")
	writeP711JSON(t, benchmarkPath, map[string]any{"schema_version": "idiom.localization_benchmark.v1", "status": "pass"})

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:     root,
		LocalOpsReport:  localOpsPath,
		BenchmarkReport: benchmarkPath,
	})
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.12")
	if gate.Status == "evidence_present" {
		t.Fatalf("P9.12 must not be evidence_present while client validation evidence is missing: %#v", report.ReleaseReadiness)
	}
	if gate.Status != "needs_evidence" {
		t.Fatalf("P9.12 should need evidence when client validation records are absent, got %#v", gate)
	}
	p97 := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.7")
	if p97.Status != "needs_evidence" {
		t.Fatalf("P9.7 should need validation evidence when records are absent, got %#v", p97)
	}
	if !releaseEvidenceHasBlockerReason(report.Blockers, "release_readiness", "P9.7") {
		t.Fatalf("release blockers should include P9.7 demand evidence gap: %#v", report.Blockers)
	}
}

func TestP96ReleaseReadinessNeedsMeasuredCleanInstallAndCleanupEvidence(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	var localOps localOpsBenchmarkReport
	data, err := os.ReadFile(localOpsPath)
	if err != nil {
		t.Fatalf("read local ops report: %v", err)
	}
	if err := json.Unmarshal(data, &localOps); err != nil {
		t.Fatalf("parse local ops report: %v", err)
	}
	localOps.Quickstart.EnvironmentClass = "source-build"
	localOps.Quickstart.DurationSource = "deterministic_command_step_seconds"
	localOps.Quickstart.SetupInstallReadiness.InstallSetupPath = "source-build-or-local-binary"
	localOps.Quickstart.SetupInstallReadiness.ReleaseInstallProof = "not_claimed"
	writeP711JSON(t, localOpsPath, localOps)

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:    root,
		LocalOpsReport: localOpsPath,
	})
	if report.ActivationOnboarding.Status != "needs_evidence" || report.ActivationOnboarding.Classification != "no-go" {
		t.Fatalf("source-build deterministic activation evidence must fail closed for P9.6: %#v", report.ActivationOnboarding)
	}
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
	if gate.Status != "needs_evidence" {
		t.Fatalf("P9.6 must not be evidence_present without measured clean-install proof: %#v", gate)
	}
	if !releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "release binary clean-install proof") {
		t.Fatalf("activation section missing clean-install blocker: %#v", report.ActivationOnboarding.Blockers)
	}
	if !releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "measured wall-clock") {
		t.Fatalf("activation section missing measured timing blocker: %#v", report.ActivationOnboarding.Blockers)
	}
}

func TestP96ReleaseActivationMalformedMandatoryFieldsFailClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	var localOps localOpsBenchmarkReport
	data, err := os.ReadFile(localOpsPath)
	if err != nil {
		t.Fatalf("read local ops report: %v", err)
	}
	if err := json.Unmarshal(data, &localOps); err != nil {
		t.Fatalf("parse local ops report: %v", err)
	}
	localOps.Quickstart.EnvironmentClass = ""
	localOps.Quickstart.SetupInstallReadiness.InstallSetupPath = ""
	localOps.Quickstart.SetupInstallReadiness.ReleaseInstallProof = ""
	localOps.Quickstart.FailureReason = localOpsActivationFailureReason{}
	localOps.Quickstart.DocsGap = localOpsActivationDocsGap{}
	localOps.Quickstart.Cleanup = localOpsActivationCleanupEvidence{Status: "pass", ExitStatus: "pass"}
	writeP711JSON(t, localOpsPath, localOps)

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:    root,
		LocalOpsReport: localOpsPath,
	})
	if report.ActivationOnboarding.Status == "pass" || report.ActivationOnboarding.Classification == "pass" {
		t.Fatalf("malformed activation evidence must fail closed: %#v", report.ActivationOnboarding)
	}
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
	if gate.Status == "evidence_present" {
		t.Fatalf("malformed activation evidence must not satisfy P9.6: %#v", gate)
	}
	for _, fragment := range []string{
		"environment class",
		"clean-install proof or scoped clean-setup proof",
		"failure reason semantics",
		"docs gap semantics",
		"cleanup evidence missing",
	} {
		if !releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", fragment) {
			t.Fatalf("activation blockers missing %q: %#v", fragment, report.ActivationOnboarding.Blockers)
		}
	}
}

func TestP96ReleaseReadinessNeedsValidationRecordsForStrictActivation(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:    root,
		CurrentDir:     "current",
		LocalOpsReport: localOpsPath,
	})
	if report.ActivationOnboarding.Status != "needs_evidence" || report.ActivationOnboarding.Classification != "no-go" {
		t.Fatalf("activation readiness must fail closed without validation records: %#v", report.ActivationOnboarding)
	}
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
	if gate.Status != "needs_evidence" {
		t.Fatalf("strict activation without validation records must not close P9.6: %#v", gate)
	}
	if !releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "current validation records") {
		t.Fatalf("activation section missing validation-record blocker: %#v", report.ActivationOnboarding.Blockers)
	}
}

func TestP96ReleaseActivationRetainedStatePolicyMustBeReviewed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())
	stageP711LearningEvidence(t, root, "p96-retained-state")
	var localOps localOpsBenchmarkReport
	data, err := os.ReadFile(localOpsPath)
	if err != nil {
		t.Fatalf("read local ops report: %v", err)
	}
	if err := json.Unmarshal(data, &localOps); err != nil {
		t.Fatalf("parse local ops report: %v", err)
	}
	localOps.Quickstart.Cleanup.Mode = "remove-smoke-artifacts"
	localOps.Quickstart.Cleanup.RemainingState = "context_graph_retained_by_policy"
	localOps.Quickstart.Cleanup.RetainedStatePolicy = "quickstart smoke retains local context graph by policy"
	writeP711JSON(t, localOpsPath, localOps)

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		CurrentDir:        "current",
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
	if gate.Status == "evidence_present" || report.ActivationOnboarding.Status == "pass" {
		t.Fatalf("unreviewed retained-state policy must not close P9.6: gate=%#v activation=%#v", gate, report.ActivationOnboarding)
	}
	if !releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "reviewed retained-state semantics") {
		t.Fatalf("activation section missing retained-state policy blocker: %#v", report.ActivationOnboarding.Blockers)
	}
}

func TestP96ReleaseActivationZeroRankedPacketFailsClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())
	stageP711LearningEvidence(t, root, "p96-zero-packet")
	var localOps localOpsBenchmarkReport
	data, err := os.ReadFile(localOpsPath)
	if err != nil {
		t.Fatalf("read local ops report: %v", err)
	}
	if err := json.Unmarshal(data, &localOps); err != nil {
		t.Fatalf("parse local ops report: %v", err)
	}
	localOps.Quickstart.FirstUsefulPacket.CandidateFileCount = 0
	localOps.Quickstart.FirstUsefulPacket.CandidateSymbolCount = 0
	localOps.Quickstart.FirstUsefulPacket.CandidateTestCount = 0
	localOps.Quickstart.FirstUsefulPacket.UsefulEvidence = []string{"candidate_files:0", "candidate_symbols:0", "candidate_tests:0"}
	writeP711JSON(t, localOpsPath, localOps)

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		CurrentDir:        "current",
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
	if gate.Status == "evidence_present" || report.ActivationOnboarding.Status == "pass" {
		t.Fatalf("zero-ranked first packet must not close P9.6: gate=%#v activation=%#v", gate, report.ActivationOnboarding)
	}
	if !releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "first useful packet") {
		t.Fatalf("activation section missing first-packet blocker: %#v", report.ActivationOnboarding.Blockers)
	}
}

func TestP97ReleaseAndProductEvidenceReadinessAgree(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())
	stageP711LearningEvidence(t, root, "p97-agreement")

	product := buildProductEvidenceExportReport(root, laneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	release := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		CurrentDir:        "current",
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.7")
	releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.7")
	if productGate.Status != "evidence_present" || releaseGate.Status != productGate.Status {
		t.Fatalf("product/release P9.7 readiness should agree and pass for complete fixture: product=%#v release=%#v", productGate, releaseGate)
	}
	for _, key := range []string{"validation_records", "complete_records", "primary_icp_records", "primary_contexts", "daily_user_activation_records", "retained_use_records", "credible_primary_adoption_signals"} {
		if productGate.Counts[key] != releaseGate.Counts[key] {
			t.Fatalf("product/release P9.7 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate)
		}
	}
	if strings.Join(productGate.Blockers, "|") != strings.Join(releaseGate.Blockers, "|") ||
		strings.Join(productGate.EvidenceIDs, "|") != strings.Join(releaseGate.EvidenceIDs, "|") {
		t.Fatalf("product/release P9.7 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate)
	}
}

func TestP98ReleaseAndProductEvidenceReadinessAgree(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())

	product := buildProductEvidenceExportReport(root, laneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot: root,
	})
	release := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot: root,
		CurrentDir:  "current",
	})
	productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.8")
	releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.8")
	if productGate.Status != "evidence_present" || releaseGate.Status != productGate.Status {
		t.Fatalf("product/release P9.8 readiness should agree and pass for complete fixture: product=%#v release=%#v", productGate, releaseGate)
	}
	for _, key := range []string{"proof_rows", "executable_required_paths", "executable_required_path_shell_ci_script", "executable_required_path_external_coding_agent_workflow", "executable_required_path_mcp_stdio", "mcp_stdio_parity_paths"} {
		if productGate.Counts[key] != releaseGate.Counts[key] {
			t.Fatalf("product/release P9.8 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate)
		}
	}
	if strings.Join(productGate.Blockers, "|") != strings.Join(releaseGate.Blockers, "|") ||
		strings.Join(productGate.EvidenceIDs, "|") != strings.Join(releaseGate.EvidenceIDs, "|") {
		t.Fatalf("product/release P9.8 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate)
	}
}

func TestP98ReleaseReadinessNeedsEvidenceWhenIntegrationProofsIncomplete(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	writeP711IntegrationProofs(t, root, []map[string]any{
		p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
		p711IntegrationProofWithMCPParityOnPath("p7.8-external-coding-agent", "external_coding_agent_workflow"),
	})
	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot: root,
	})
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.8")
	if gate.Status != "needs_evidence" {
		t.Fatalf("P9.8 must not pass when MCP parity is on the wrong path: %#v", gate)
	}
	if gate.Counts["executable_required_paths"] >= 3 || len(gate.Blockers) == 0 {
		t.Fatalf("P9.8 release readiness should expose missing-path counts and blockers: %#v", gate)
	}
	if !releaseEvidenceHasBlockerReason(report.Blockers, "release_readiness", "P9.8") {
		t.Fatalf("release blockers should include P9.8 integration evidence gap: %#v", report.Blockers)
	}
}

func TestP97ReleaseRetainedUseWithoutCredibleAdoptionSignalDoesNotPass(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())
	records := p97PrimaryRecords("none", "none")
	recordsPath := filepath.Join(root, "validation-records.json")
	writeP711JSON(t, recordsPath, records)

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		CurrentDir:        "current",
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.7")
	if gate.Status == "evidence_present" {
		t.Fatalf("P9.7 must not pass without credible paid/internal-platform adoption signal: %#v", gate)
	}
	if !strings.Contains(gate.Evidence, "adoption:0") {
		t.Fatalf("P9.7 evidence should expose missing adoption count: %#v", gate)
	}
	if gate.Counts["credible_primary_adoption_signals"] != 0 || len(gate.Blockers) == 0 || len(gate.EvidenceIDs) != 3 {
		t.Fatalf("P9.7 release readiness should expose structured adoption blocker data: %#v", gate)
	}
}

func TestP914ReleaseBundleDowngradesEveryP912ObjectWhenClientValidationMissing(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	benchmarkPath := filepath.Join(root, "testdata", "benchmarks", "localization.json")
	writeP711JSON(t, benchmarkPath, map[string]any{"schema_version": "idiom.localization_benchmark.v1", "status": "pass"})

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:     root,
		LocalOpsReport:  localOpsPath,
		BenchmarkReport: benchmarkPath,
	})
	if !releaseEvidenceAnyClientMissingEvidence(report.ClientReadiness) {
		t.Fatalf("fixture should contain client rows with missing validation evidence: %#v", report.ClientReadiness)
	}

	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal release evidence: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal release evidence: %v", err)
	}
	statuses := releaseEvidenceP912Statuses(decoded)
	if len(statuses) < 2 {
		t.Fatalf("expected top-level and embedded P9.12 objects, got statuses=%#v body=%s", statuses, string(body))
	}
	for _, status := range statuses {
		if status == "evidence_present" {
			t.Fatalf("no P9.12 object may be evidence_present while client evidence is missing: statuses=%#v body=%s", statuses, string(body))
		}
	}
}

func TestP913ReleaseBenchmarkNeedsCompetitiveEvidenceWithPassingLocalOps(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})

	if report.BenchmarkLocalOps.LocalOps.Status != "pass" {
		t.Fatalf("fixture should keep local-ops replay passing: %#v", report.BenchmarkLocalOps.LocalOps)
	}
	if report.BenchmarkLocalOps.Status == "pass" {
		t.Fatalf("benchmark section must not pass without competitive benchmark evidence: %#v", report.BenchmarkLocalOps)
	}
	if !releaseEvidenceHasBlockerReason(report.BenchmarkLocalOps.Blockers, "benchmark_local_ops", "competitive benchmark") {
		t.Fatalf("benchmark section missing competitive evidence blocker: %#v", report.BenchmarkLocalOps.Blockers)
	}
}

func TestP91ReleaseEvidencePreservesPrivacy(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	current := filepath.Join(root, "current")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("mkdir current: %v", err)
	}
	metadata := "# sanitized\n\n" +
		"Command: `idiom tool context_status --profile discovery`\n\n" +
		"Exit status: `0`\n\n" +
		"Stdout artifact: `01-context-status.stdout.json`\n\n" +
		"Stderr artifact: `01-context-status.stderr.txt`\n\n" +
		"Failure/blocker: none recorded.\n"
	if err := os.WriteFile(filepath.Join(current, "01-context-status.metadata.md"), []byte(metadata), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(current, "01-context-status.stdout.json"), []byte(`{"status":"pass","metadata_only":true}`), 0o644); err != nil {
		t.Fatalf("write fixture artifact: %v", err)
	}

	report := buildReleaseEvidenceBundle(root, laneHistorySummary{}, releaseEvidenceOptions{
		ProjectRoot: root,
		CurrentDir:  "current",
	})
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal release evidence: %v", err)
	}
	for _, forbidden := range []string{root} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("release evidence leaked %q: %s", forbidden, string(body))
		}
	}
	if report.Privacy.ForbiddenFieldCheck != "pass" || !report.Privacy.MetadataOnly {
		t.Fatalf("privacy contract should remain metadata-only/pass: %#v", report.Privacy)
	}
}

func TestRunReleaseEvidenceWritesParseableJSONOutput(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "baseline", releaseEvidenceBaselineCommands())
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())
	writeP711JSON(t, filepath.Join(root, "testdata", "sidecar", "discovery-smoke.transcript.json"), map[string]any{
		"schema_version": "idiom.sidecar_smoke_transcript.v1",
	})
	outputPath := filepath.Join(root, ".idiom", "release", "bundle.json")

	var stdout, stderr bytes.Buffer
	code := runReleaseMainWithTelemetry([]string{
		"evidence",
		"--project-root", root,
		"--baseline-dir", "baseline",
		"--current-dir", "current",
		"--local-ops-report", localOpsPath,
		"--validation-records", recordsPath,
		"--sidecar-smoke", filepath.Join("testdata", "sidecar", "discovery-smoke.transcript.json"),
		"--output", outputPath,
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("release evidence command failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var report releaseEvidenceBundle
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("release output should parse: %v\n%s", err, string(data))
	}
	if report.BundleID == "" || report.SchemaVersion != releaseEvidenceSchemaVersion {
		t.Fatalf("output missing release identity: %#v", report)
	}
	if strings.Contains(stdout.String(), outputPath) || strings.Contains(stderr.String(), outputPath) {
		t.Fatalf("CLI should not echo raw output path stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func stageReleaseEvidenceCommandBundle(t *testing.T, root string, dir string, commands []string) {
	t.Helper()
	fullDir := filepath.Join(root, dir)
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatalf("mkdir release bundle: %v", err)
	}
	for i, command := range commands {
		name := releaseEvidenceCommandArtifactName(i, command)
		stdoutName := name + ".stdout.json"
		stderrName := name + ".stderr.txt"
		metadata := "# " + name + "\n\n" +
			"Command: `" + command + "`\n\n" +
			"Working directory: `repo-relative:.`\n\n" +
			"Started at UTC: `2026-05-14T00:00:00Z`\n\n" +
			"Ended at UTC: `2026-05-14T00:00:01Z`\n\n" +
			"Exit status: `0`\n\n" +
			"Stdout artifact: `" + stdoutName + "`\n\n" +
			"Stderr artifact: `" + stderrName + "`\n\n" +
			"Failure/blocker: none recorded.\n"
		if err := os.WriteFile(filepath.Join(fullDir, name+".metadata.md"), []byte(metadata), 0o644); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
		writeP711JSON(t, filepath.Join(fullDir, stdoutName), map[string]any{"command_id": name, "status": "pass"})
		if err := os.WriteFile(filepath.Join(fullDir, stderrName), []byte("(no stderr)\n"), 0o644); err != nil {
			t.Fatalf("write stderr: %v", err)
		}
	}
}

func stageP711LearningEvidence(t *testing.T, root string, resultID string) {
	t.Helper()
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    "test",
		Action:    "boost",
		State:     "accepted",
		Reason:    "grounded",
		Evidence:  &comprehension.LearningEvidenceRef{Kind: "eval_result", Verified: true, ResultID: "result:" + resultID},
	}); err != nil {
		t.Fatalf("append learning log: %v", err)
	}
}

func releaseEvidenceBaselineCommands() []string {
	return []string{
		"idiom tool context_status",
		`idiom tool harness_audit --input '{"purpose":"tool_surface"}'`,
		`idiom tool harness_audit --input '{"purpose":"behavior"}'`,
		"go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json",
	}
}

func releaseEvidenceCurrentCommands() []string {
	return append(releaseEvidenceBaselineCommands(),
		"go test ./...",
		"go test ./cmd/idiom -count=1",
		"git diff --check",
	)
}

func releaseEvidenceCommandArtifactName(index int, command string) string {
	id := p711SafeToken(strings.Fields(command)[0])
	if strings.Contains(command, "harness_audit") && strings.Contains(command, "tool_surface") {
		id = "harness-audit-tool-surface"
	}
	if strings.Contains(command, "harness_audit") && strings.Contains(command, "behavior") {
		id = "harness-audit-behavior"
	}
	return strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%02d-%s", index+1, id), "_", "-"), ".", "-")
}

func assertReleaseEvidenceClassifications(t *testing.T, rows []releaseEvidenceClientReadiness) {
	t.Helper()
	seen := map[string]releaseEvidenceClientReadiness{}
	for _, row := range rows {
		seen[row.Classification] = row
	}
	for _, class := range releaseEvidenceClientClassifications {
		row, ok := seen[class]
		if !ok {
			t.Fatalf("missing client readiness row %q", class)
		}
		if len(row.ArchitecturePrimitives) == 0 {
			t.Fatalf("client row %q missing architecture primitives: %#v", class, row)
		}
	}
}

func releaseEvidenceHasIntegrationPath(rows []releaseEvidenceIntegrationPath, path string) bool {
	for _, row := range rows {
		if row.Path == path && row.Status == "pass" {
			return true
		}
	}
	return false
}

func releaseEvidenceHasBlocker(blockers []releaseEvidenceBlocker, section string) bool {
	for _, blocker := range blockers {
		if blocker.Section == section {
			return true
		}
	}
	return false
}

func releaseEvidenceHasBlockerReason(blockers []releaseEvidenceBlocker, section string, reasonFragment string) bool {
	for _, blocker := range blockers {
		if blocker.Section == section && strings.Contains(blocker.Reason, reasonFragment) {
			return true
		}
	}
	return false
}

func releaseEvidenceHasClientReadinessBlocker(blockers []releaseEvidenceBlocker, class string) bool {
	for _, blocker := range blockers {
		if blocker.Section == "client_readiness" && blocker.Evidence == class {
			return true
		}
	}
	return false
}

func releaseEvidenceReadinessGate(rows []releaseEvidenceGateReadiness, gate string) releaseEvidenceGateReadiness {
	for _, row := range rows {
		if row.Gate == gate {
			return row
		}
	}
	return releaseEvidenceGateReadiness{}
}

func releaseEvidenceStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func releaseEvidenceAnyClientMissingEvidence(rows []releaseEvidenceClientReadiness) bool {
	for _, row := range rows {
		if len(row.MissingEvidence) > 0 {
			return true
		}
	}
	return false
}

func releaseEvidenceP912Statuses(value any) []string {
	var statuses []string
	switch typed := value.(type) {
	case map[string]any:
		if gate, _ := typed["gate"].(string); gate == "P9.12" {
			status, _ := typed["status"].(string)
			statuses = append(statuses, status)
		}
		for _, child := range typed {
			statuses = append(statuses, releaseEvidenceP912Statuses(child)...)
		}
	case []any:
		for _, child := range typed {
			statuses = append(statuses, releaseEvidenceP912Statuses(child)...)
		}
	}
	return statuses
}
