package productevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/evidence/lanehistory"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/localops"
)

func TestSanitizeReleaseEvidenceTelemetryArgsRedactsPathFlags(t *testing.T) {
	got := SanitizeReleaseEvidenceTelemetryArgs([]string{
		"--project-root=/private/repo",
		"--baseline-dir", "/private/baseline",
		"--sidecar-smoke", "",
		"--mode", "summary",
	})
	rendered := strings.Join(got, " ")

	for _, forbidden := range []string{"/private/repo", "/private/baseline"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("path flag leaked %q in %#v", forbidden, got)
		}
	}

	if !strings.Contains(rendered, "<redacted_release_evidence_path id=") || !strings.Contains(rendered, "--sidecar-smoke <empty>") {
		t.Fatalf("expected redacted release evidence path args, got %#v", got)
	}

	if !strings.Contains(rendered, "--mode summary") {
		t.Fatalf("non-path flags should be preserved, got %#v", got)
	}
}

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

	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())
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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.10"); gate.Status != "evidence_present" {
		t.Fatalf("complete competitive benchmark fixture should satisfy P9.10 release readiness: %#v", gate)
	} else if gate.Counts["required_external_baselines"] != 2 ||
		gate.Counts["covered_external_baselines"] != 2 ||
		len(gate.Blockers) != 0 ||
		!stringSliceContains(gate.EvidenceIDs, "competitive_baseline:aider_repo_map") {

		t.Fatalf("P9.10 release readiness should expose structured gate data: %#v", gate)
	}

	if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.11"); gate.Status != "evidence_present" {
		t.Fatalf("complete client demo fixture should satisfy P9.11 release readiness: %#v", gate)
	} else if gate.Counts["covered_classifications"] != 7 ||
		gate.Counts["runnable_command_rows"] != 7 ||
		len(gate.Blockers) != 0 ||
		!stringSliceContains(gate.EvidenceIDs, "p7.10-enterprise-saas") {

		t.Fatalf("P9.11 release readiness should expose structured gate data: %#v", gate)
	}

	assertReleaseEvidenceClassifications(t, report.ClientReadiness)

	if forbidden := localops.ForbiddenEvidenceLeak(string(body)); forbidden != "" {
		t.Fatalf("release evidence leaked forbidden class %q: %s", forbidden, string(body))
	}
}

func TestReleaseEvidenceSummarizesOpenRecoveryRollup(t *testing.T) {
	root := t.TempDir()
	stageProductEvidenceRecoveryPacket(t, root, "release-open-recovery")

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot: root,
		CurrentDir:  "current",
	})
	if report.Recovery.Status != "partial" ||
		report.Recovery.OpenPacketCount != 1 ||
		report.Recovery.CandidateAtomCount == 0 ||
		report.Recovery.RemainingAtomCount == 0 {

		t.Fatalf("expected open recovery rollup in release evidence, got %#v", report.Recovery)
	}

	if report.Status == "pass" || report.Status == "blocked" {
		t.Fatalf("open recovery should be release-impact partial evidence, got status=%q blockers=%#v", report.Status, report.Blockers)
	}

	if !releaseEvidenceHasBlockerReason(report.Blockers, "recovery", "open recovery packets") {
		t.Fatalf("release blockers should include open recovery impact, got %#v", report.Blockers)
	}
}

func TestReleaseEvidenceRecoveredRollupDoesNotAddRecoveryBlocker(t *testing.T) {
	root := t.TempDir()
	stageProductEvidenceRecoveredPacket(t, root, "release-recovered")

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot: root,
		CurrentDir:  "current",
	})
	if report.Recovery.Status != "pass" ||
		report.Recovery.RecoveredPacketCount != 1 ||
		report.Recovery.OpenPacketCount != 0 ||
		report.Recovery.RemainingAtomCount != 0 {

		t.Fatalf("expected recovered recovery rollup in release evidence, got %#v", report.Recovery)
	}

	if releaseEvidenceHasBlocker(report.Blockers, "recovery") {
		t.Fatalf("recovered packet should not add a release recovery blocker: %#v", report.Blockers)
	}
}

func TestP911ReleaseEvidenceRequiresExecutableCommandValidation(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	p911MutateTranscriptByClass(t, root, "enterprise-saas", func(row map[string]any) {
		command := row["commands"].([]any)[0].(map[string]any)

		command["id"] = "mismatched-demo-command"
	})
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root, CurrentDir: "current"})
	gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.11")

	if gate.Status != "needs_evidence" || gate.Counts["runnable_command_rows"] == 7 || gate.Counts["command_id_mismatch_rows"] == 0 {
		t.Fatalf("release P9.11 must fail closed on command ID mismatch: %#v", gate)
	}

	if !p711TestBlockerContains(gate.Blockers, "command ID parity mismatch") {
		t.Fatalf("release P9.11 command parity blocker not exposed: %#v", gate)
	}
}

func TestP91ReleaseEvidenceMarksPartialWhenEvidenceMissing(t *testing.T) {
	root := t.TempDir()
	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{}, releaseEvidenceOptions{
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
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	var localOps localops.BenchmarkReport

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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	var localOps localops.BenchmarkReport

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
	localOps.Quickstart.FailureReason = localops.ActivationFailureReason{}
	localOps.Quickstart.DocsGap = localops.ActivationDocsGap{}
	localOps.Quickstart.Cleanup = localops.ActivationCleanupEvidence{Status: "pass", ExitStatus: "pass"}
	writeP711JSON(t, localOpsPath, localOps)

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	var localOps localops.BenchmarkReport

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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	var localOps localops.BenchmarkReport

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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	product := buildProductEvidenceExportReport(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	release := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	product := buildProductEvidenceExportReport(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot: root,
	})
	release := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
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

	if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.10"); gate.Status != "needs_evidence" {
		t.Fatalf("P9.10 release readiness must fail closed without competitive evidence: %#v", gate)
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

	report := buildReleaseEvidenceBundle(root, lanehistory.Summary{}, releaseEvidenceOptions{
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

func TestReleaseEvidencePrivacyBlockRecomputesIdentityAfterRedaction(t *testing.T) {
	report := releaseEvidenceBundle{
		SchemaVersion: releaseEvidenceSchemaVersion,
		Status:        "pass",
		BundleID:      "release-evidence-old",
		ArtifactHash:  "old",
		Privacy: releaseEvidencePrivacy{
			MetadataOnly:        true,
			ForbiddenFieldCheck: "pass",
		},
		Metadata: map[string]any{
			"debug_path": "/home/example/private-repo",
		},
	}
	blockReleaseEvidencePrivacy(&report, "absolute_path")

	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal release evidence: %v", err)
	}

	if strings.Contains(string(body), "/home/example/private-repo") {
		t.Fatalf("privacy-blocked bundle should redact forbidden values: %s", string(body))
	}

	if report.Status != "blocked" || report.Privacy.ForbiddenFieldCheck != "failed:absolute_path" {
		t.Fatalf("privacy block not reflected in report: %#v", report)
	}

	if want := releaseEvidenceHash(report); report.ArtifactHash != want || report.BundleID != "release-evidence-"+want {
		t.Fatalf("privacy-blocked identity should describe final body, got hash=%q bundle=%q want=%q", report.ArtifactHash, report.BundleID, want)
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
	id := localops.SafeToken(strings.Fields(command)[0])
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

// --- Utility function unit tests ---

func TestReleaseEvidenceSliceContains(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		target string
		want   bool
	}{
		{"found", []string{"a", "b", "c"}, "b", true},
		{"not found", []string{"a", "b", "c"}, "d", false},
		{"empty slice", nil, "a", false},
		{"exact match only", []string{"abc"}, "ab", false},
		{"first element", []string{"first", "second"}, "first", true},
		{"last element", []string{"first", "second"}, "second", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := releaseEvidenceSliceContains(tt.values, tt.target)
			if got != tt.want {
				t.Fatalf("releaseEvidenceSliceContains(%#v, %q) = %v, want %v", tt.values, tt.target, got, tt.want)
			}
		})
	}
}

func TestReleaseCommandsMatch(t *testing.T) {
	tests := []struct {
		actual   string
		expected string
		want     bool
	}{
		{"idiom tool context_status", "idiom tool context_status", true},
		{"idiom tool context_status", "context_status", true},
		{"context_status", "idiom tool context_status", true},
		{"different", "other", false},
		{"UPPER", "upper", true},
		{"  padded  ", "padded", true},
	}
	for _, tt := range tests {
		got := releaseCommandsMatch(tt.actual, tt.expected)
		if got != tt.want {
			t.Fatalf("releaseCommandsMatch(%q, %q) = %v, want %v", tt.actual, tt.expected, got, tt.want)
		}
	}
}

func TestReleaseMetadataValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{" simple ", "simple"},
		{"`quoted`", "quoted"},
		{"  `backtick`  ", "backtick"},
		{" plain ", "plain"},
		{"", ""},
	}
	for _, tt := range tests {
		got := releaseMetadataValue(tt.input)
		if got != tt.want {
			t.Fatalf("releaseMetadataValue(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestReleaseStatusFromExit(t *testing.T) {
	tests := []struct {
		exit    string
		blocker string
		want    string
	}{
		{"0", "", releaseEvidenceStatusPass},
		{"1", "", releaseEvidenceStatusFail},
		{"", "", releaseEvidenceStatusPartial},
		{"0", "blocked reason", releaseEvidenceStatusBlocked},
		{"1", "safety", releaseEvidenceStatusBlocked},
	}
	for _, tt := range tests {
		got := releaseStatusFromExit(tt.exit, tt.blocker)
		if got != tt.want {
			t.Fatalf("releaseStatusFromExit(%q, %q) = %q, want %q", tt.exit, tt.blocker, got, tt.want)
		}
	}
}

func TestReleaseArtifactKind(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"something.stdout.json", "stdout_json"},
		{"something.stderr.txt", "stderr_text"},
		{"config.json", "json"},
		{"readme.txt", "text"},
		{"unknown.bin", "artifact"},
	}
	for _, tt := range tests {
		got := releaseArtifactKind(tt.name)
		if got != tt.want {
			t.Fatalf("releaseArtifactKind(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestReleaseSafeCommand(t *testing.T) {
	t.Run("empty returns empty", func(t *testing.T) {
		got := releaseSafeCommand("")
		if got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("safe command passes through", func(t *testing.T) {
		got := releaseSafeCommand("idiom tool context_status")
		if got != "idiom tool context_status" {
			t.Fatalf("expected unchanged safe command, got %q", got)
		}
	})

	t.Run("multiline command is redacted to command_id", func(t *testing.T) {
		got := releaseSafeCommand("idiom tool\nevil")
		if !strings.HasPrefix(got, "command_id:") {
			t.Fatalf("expected command_id prefix for multiline, got %q", got)
		}
	})
}

func TestReleaseProductSignal(t *testing.T) {
	signals := []productEvidenceSignalSummary{
		{ID: "activation", Status: "pass"},
		{ID: "retained_use", Status: "partial"},
	}

	t.Run("found signal", func(t *testing.T) {
		got := releaseProductSignal(signals, "activation")
		if got.Status != "pass" {
			t.Fatalf("expected pass signal, got %#v", got)
		}
	})

	t.Run("missing signal returns empty", func(t *testing.T) {
		got := releaseProductSignal(signals, "missing")
		if got.ID != "" {
			t.Fatalf("expected empty signal for missing ID, got %#v", got)
		}
	})

	t.Run("empty list returns empty", func(t *testing.T) {
		got := releaseProductSignal(nil, "any")
		if got.ID != "" {
			t.Fatalf("expected empty signal, got %#v", got)
		}
	})
}

func TestReleaseSafeToken(t *testing.T) {
	t.Run("integer string returns the integer", func(t *testing.T) {
		got := releaseSafeToken("42")
		if got != "42" {
			t.Fatalf("expected '42', got %q", got)
		}
	})

	t.Run("negative integer passes through as int", func(t *testing.T) {
		got := releaseSafeToken("-1")
		if got != "-1" {
			t.Fatalf("expected '-1', got %q", got)
		}
	})

	t.Run("non-integer is safe-tokened", func(t *testing.T) {
		got := releaseSafeToken("some value")
		if got == "some value" {
			t.Fatalf("non-integer should be safe-tokened, got %q", got)
		}
	})
}

func TestReleaseEvidenceCompactBlockers(t *testing.T) {
	t.Run("empty returns empty", func(t *testing.T) {
		got := releaseEvidenceCompactBlockers(nil)
		if len(got) != 0 {
			t.Fatalf("expected empty, got %#v", got)
		}
	})

	t.Run("unique entries preserved", func(t *testing.T) {
		blockers := []releaseEvidenceBlocker{
			{Section: "s1", Reason: "r1"},
			{Section: "s2", Reason: "r2"},
		}
		got := releaseEvidenceCompactBlockers(blockers)

		if len(got) != 2 {
			t.Fatalf("expected 2 unique entries, got %d", len(got))
		}
	})

	t.Run("duplicates removed", func(t *testing.T) {
		blockers := []releaseEvidenceBlocker{
			{Section: "s1", Reason: "r1", Evidence: "e1"},
			{Section: "s1", Reason: "r1", Evidence: "e1"},
			{Section: "s2", Reason: "r2"},
		}
		got := releaseEvidenceCompactBlockers(blockers)

		if len(got) != 2 {
			t.Fatalf("expected 2 after dedup, got %d: %#v", len(got), got)
		}
	})
}

func TestReleaseEvidencePath(t *testing.T) {
	root := t.TempDir()

	t.Run("empty path returns empty ref", func(t *testing.T) {
		ref := releaseEvidencePath(root, "")
		if ref.PathID != "" {
			t.Fatalf("expected empty path ref, got %#v", ref)
		}
	})

	t.Run("non-empty path gets PathID", func(t *testing.T) {
		ref := releaseEvidencePath(root, "/some/path")
		if ref.PathID == "" {
			t.Fatal("expected non-empty PathID")
		}
	})

	t.Run("path under root gets relative path", func(t *testing.T) {
		subPath := filepath.Join(root, "subdir", "file.json")
		if err := os.MkdirAll(filepath.Dir(subPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := os.WriteFile(subPath, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		ref := releaseEvidencePath(root, subPath)
		if ref.RelativePath == "" {
			t.Fatalf("expected relative path for path under root, got %#v", ref)
		}

		if filepath.IsAbs(ref.RelativePath) {
			t.Fatalf("relative path should not be absolute: %q", ref.RelativePath)
		}
	})
}

func TestReleaseEvidencePathFlag(t *testing.T) {
	tests := []struct {
		flag string
		want bool
	}{
		{"--project-root", true},
		{"--output", true},
		{"--baseline-dir", true},
		{"--current-dir", true},
		{"--local-ops-report", true},
		{"--validation-records", true},
		{"--benchmark-report", true},
		{"--sidecar-smoke", true},
		{"--mode", false},
		{"--help", false},
		{"", false},
	}
	for _, tt := range tests {
		got := releaseEvidencePathFlag(tt.flag)
		if got != tt.want {
			t.Fatalf("releaseEvidencePathFlag(%q) = %v, want %v", tt.flag, got, tt.want)
		}
	}
}

func TestRedactedReleaseEvidencePathArg(t *testing.T) {
	t.Run("empty returns <empty>", func(t *testing.T) {
		got := redactedReleaseEvidencePathArg("")
		if got != "<empty>" {
			t.Fatalf("expected <empty>, got %q", got)
		}
	})

	t.Run("path gets redacted with stable id", func(t *testing.T) {
		got := redactedReleaseEvidencePathArg("/private/data")
		if !strings.HasPrefix(got, "<redacted_release_evidence_path id=") {
			t.Fatalf("expected redacted prefix, got %q", got)
		}

		if strings.Contains(got, "/private/data") {
			t.Fatalf("raw path must not appear: %q", got)
		}
	})
}

func TestReleaseActivationApprovedEnvironmentClass(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"clean-install", true},
		{"clean-setup", true},
		{"source-build", false},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		got := releaseActivationApprovedEnvironmentClass(tt.value)
		if got != tt.want {
			t.Fatalf("releaseActivationApprovedEnvironmentClass(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestReleaseActivationFailureReasonExplicit(t *testing.T) {
	noFailure := localops.ActivationFailureReason{
		Status:       "none",
		PrimaryClass: "none",
		Reason:       "",
		RetryAllowed: false,
	}
	if !releaseActivationFailureReasonExplicit(noFailure) {
		t.Fatal("explicit 'no failure' should return true")
	}

	withStatus := localops.ActivationFailureReason{Status: "failed"}
	if releaseActivationFailureReasonExplicit(withStatus) {
		t.Fatal("non-none status should return false")
	}

	withRetry := localops.ActivationFailureReason{
		Status:       "none",
		PrimaryClass: "none",
		RetryAllowed: true,
	}
	if releaseActivationFailureReasonExplicit(withRetry) {
		t.Fatal("retry allowed should return false")
	}
}

func TestReleaseActivationDocsGapExplicit(t *testing.T) {
	t.Run("none status without recorded gap", func(t *testing.T) {
		gap := localops.ActivationDocsGap{
			Status:      "none",
			GapRecorded: false,
		}
		if !releaseActivationDocsGapExplicit(gap) {
			t.Fatal("none status without gap should return true")
		}
	})

	t.Run("none status with recorded gap is invalid", func(t *testing.T) {
		gap := localops.ActivationDocsGap{
			Status:      "none",
			GapRecorded: true,
		}
		if releaseActivationDocsGapExplicit(gap) {
			t.Fatal("none + recorded gap should return false")
		}
	})

	t.Run("recorded status with all fields", func(t *testing.T) {
		gap := localops.ActivationDocsGap{
			Status:            "recorded",
			GapRecorded:       true,
			DocPath:           "docs/gap.md",
			CommandLabel:      "test",
			ExpectedOutput:    "output",
			ActualOutput:      "actual",
			Severity:          "low",
			BlockedActivation: false,
		}
		if !releaseActivationDocsGapExplicit(gap) {
			t.Fatal("fully explicit recorded gap should return true")
		}
	})

	t.Run("recorded status with blocked activation is invalid", func(t *testing.T) {
		gap := localops.ActivationDocsGap{
			Status:            "recorded",
			GapRecorded:       true,
			DocPath:           "docs/gap.md",
			CommandLabel:      "test",
			ExpectedOutput:    "output",
			ActualOutput:      "actual",
			Severity:          "low",
			BlockedActivation: true,
		}
		if releaseActivationDocsGapExplicit(gap) {
			t.Fatal("recorded + blocked should return false")
		}
	})

	t.Run("unknown status returns false", func(t *testing.T) {
		gap := localops.ActivationDocsGap{Status: "unknown"}
		if releaseActivationDocsGapExplicit(gap) {
			t.Fatal("unknown status should return false")
		}
	})
}

func TestReleaseActivationAbsentLocalState(t *testing.T) {
	tests := []struct {
		mode      string
		remaining string
		want      bool
	}{
		{"remove-local-state", "comprehension_state_absent", true},
		{"uninstall", "local_state_absent", true},
		{"clean-uninstall", "state_absent", true},
		{"remove-local-state", "context_graph_retained_by_policy", false},
		{"retain-artifacts", "comprehension_state_absent", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got := releaseActivationAbsentLocalState(tt.mode, tt.remaining)
		if got != tt.want {
			t.Fatalf("releaseActivationAbsentLocalState(%q, %q) = %v, want %v", tt.mode, tt.remaining, got, tt.want)
		}
	}
}

func TestReleaseActivationReviewedRetainedStatePolicy(t *testing.T) {
	tests := []struct {
		policy string
		want   bool
	}{
		{"reviewed_retained_state_policy", true},
		{"reviewed_context_graph_retention_policy", true},
		{"reviewed_local_state_retention_policy", true},
		{"unreviewed", false},
		{"", false},
	}
	for _, tt := range tests {
		got := releaseActivationReviewedRetainedStatePolicy(tt.policy)
		if got != tt.want {
			t.Fatalf("releaseActivationReviewedRetainedStatePolicy(%q) = %v, want %v", tt.policy, got, tt.want)
		}
	}
}

func TestReleaseClientReadinessStatus(t *testing.T) {
	t.Run("pass with no missing stays pass", func(t *testing.T) {
		got := releaseClientReadinessStatus("pass", nil)
		if got != "pass" {
			t.Fatalf("expected pass, got %q", got)
		}
	})

	t.Run("pass with missing evidence downgrades to needs_evidence", func(t *testing.T) {
		got := releaseClientReadinessStatus("pass", []string{"missing_proof"})
		if got != "needs_evidence" {
			t.Fatalf("expected needs_evidence, got %q", got)
		}
	})

	t.Run("blocked stays blocked", func(t *testing.T) {
		got := releaseClientReadinessStatus("blocked", []string{"missing"})
		if got != "blocked" {
			t.Fatalf("expected blocked, got %q", got)
		}
	})
}

func TestReleaseDeltaStatus(t *testing.T) {
	// releaseDeltaStatus uses the evidence-status vocabulary ("present"/"needs_evidence"),
	// not direction labels. "Delta" refers to the domain concept (baseline vs current
	// comparison), but the status field reports whether the comparison evidence itself is
	// present (both values non-zero) or needs evidence (either value is zero/missing).
	// This is consistent with the rest of the evidence report's status vocabulary.
	tests := []struct {
		baseline int
		current  int
		want     string
	}{
		{5, 5, "present"},
		{5, 6, "present"},
		{5, 4, "present"},
		{0, 5, "needs_evidence"},
		{5, 0, "needs_evidence"},
		{0, 0, "needs_evidence"},
	}
	for _, tt := range tests {
		got := releaseDeltaStatus(tt.baseline, tt.current)
		if got != tt.want {
			t.Fatalf("releaseDeltaStatus(%d, %d) = %q, want %q", tt.baseline, tt.current, got, tt.want)
		}
	}
}

func TestReleaseBenchmarkSectionStatus(t *testing.T) {
	// Section-level blockers cause "partial", not "blocked". The top-level
	// releaseEvidenceStatus reserves "blocked" for fundamental validity failures
	// (privacy violations, malformed artifacts). Section-level evidence gaps
	// (missing benchmarks, incomplete data) are "partial" — evidence present but
	// insufficient to close the gate. This is consistent across all sections.
	t.Run("present with pass and no blockers", func(t *testing.T) {
		got := releaseBenchmarkSectionStatus(true, "pass", nil)
		if got != "pass" {
			t.Fatalf("expected pass, got %q", got)
		}
	})

	t.Run("present with non-pass returns partial", func(t *testing.T) {
		got := releaseBenchmarkSectionStatus(true, "fail", nil)
		if got != "partial" {
			t.Fatalf("expected partial for non-pass local-ops, got %q", got)
		}
	})

	t.Run("not present", func(t *testing.T) {
		got := releaseBenchmarkSectionStatus(false, "", nil)
		if got != "needs_evidence" {
			t.Fatalf("expected needs_evidence, got %q", got)
		}
	})

	t.Run("blockers cause partial not blocked", func(t *testing.T) {
		blockers := []releaseEvidenceBlocker{{Section: "benchmark", Reason: "missing"}}
		got := releaseBenchmarkSectionStatus(true, "pass", blockers)

		if got != "partial" {
			t.Fatalf("expected partial with blockers, got %q", got)
		}
	})
}

func TestReleaseArchitecturePrimitives(t *testing.T) {
	t.Run("finance includes protected_path_posture", func(t *testing.T) {
		got := releaseArchitecturePrimitives("finance")
		if !releaseEvidenceSliceContains(got, "protected_path_posture") {
			t.Fatalf("finance missing protected_path_posture: %#v", got)
		}

		if !releaseEvidenceSliceContains(got, "control_mapping") {
			t.Fatalf("finance missing control_mapping: %#v", got)
		}
	})

	t.Run("tooling-vendor includes schema_parity", func(t *testing.T) {
		got := releaseArchitecturePrimitives("tooling-vendor")
		if !releaseEvidenceSliceContains(got, "schema_parity") {
			t.Fatalf("tooling-vendor missing schema_parity: %#v", got)
		}
	})

	t.Run("local-model includes degraded_mode_reporting", func(t *testing.T) {
		got := releaseArchitecturePrimitives("local-model")
		if !releaseEvidenceSliceContains(got, "degraded_mode_reporting") {
			t.Fatalf("local-model missing degraded_mode_reporting: %#v", got)
		}
	})

	t.Run("enterprise-saas defaults to context_freshness", func(t *testing.T) {
		got := releaseArchitecturePrimitives("enterprise-saas")
		if !releaseEvidenceSliceContains(got, "context_freshness") {
			t.Fatalf("enterprise-saas missing context_freshness: %#v", got)
		}
	})

	t.Run("all classes contain common primitives", func(t *testing.T) {
		for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
			got := releaseArchitecturePrimitives(class)
			for _, common := range []string{"local_sidecar_contract", "metadata_only_evidence", "profile_gates", "redaction_contract", "reviewable_artifacts"} {
				if !releaseEvidenceSliceContains(got, common) {
					t.Fatalf("class %q missing common primitive %q: %#v", class, common, got)
				}
			}
		}
	})
}

func TestReleaseP912ReadinessStatus(t *testing.T) {
	t.Run("no clients is needs_evidence", func(t *testing.T) {
		got := releaseP912ReadinessStatus(nil, "evidence_present")
		if got != "needs_evidence" {
			t.Fatalf("expected needs_evidence, got %q", got)
		}
	})

	t.Run("P9.11 not present is needs_evidence", func(t *testing.T) {
		clients := []releaseEvidenceClientReadiness{{Status: "pass"}}
		got := releaseP912ReadinessStatus(clients, "needs_evidence")

		if got != "needs_evidence" {
			t.Fatalf("expected needs_evidence, got %q", got)
		}
	})

	t.Run("client with non-pass status fails", func(t *testing.T) {
		clients := []releaseEvidenceClientReadiness{
			{Status: "pass"},
			{Status: "needs_evidence"},
		}
		got := releaseP912ReadinessStatus(clients, "evidence_present")

		if got != "needs_evidence" {
			t.Fatalf("expected needs_evidence for non-pass client, got %q", got)
		}
	})

	t.Run("client with missing evidence fails", func(t *testing.T) {
		clients := []releaseEvidenceClientReadiness{
			{Status: "pass", MissingEvidence: []string{"missing"}},
		}
		got := releaseP912ReadinessStatus(clients, "evidence_present")

		if got != "needs_evidence" {
			t.Fatalf("expected needs_evidence for missing evidence, got %q", got)
		}
	})

	t.Run("all clients pass and P9.11 present succeeds", func(t *testing.T) {
		clients := []releaseEvidenceClientReadiness{
			{Status: "pass"},
			{Status: "pass"},
		}
		got := releaseP912ReadinessStatus(clients, "evidence_present")

		if got != "evidence_present" {
			t.Fatalf("expected evidence_present, got %q", got)
		}
	})
}

func TestP96ActivationStatusFromBlockers(t *testing.T) {
	// Three states modeled by this function:
	//   "pass"    — no blockers; used by buildReleaseActivationSection for the
	//               activation section's own status.
	//   "blocked" — one of three specific privacy/redaction/smoke-failure reasons
	//               match; these are hard blockers that invalidate the evidence.
	//   "needs_evidence" — other blockers exist but aren't privacy-level;
	//               the evidence is incomplete but not fundamentally invalid.
	// Generic (non-specific) blocker reasons intentionally stay "needs_evidence"
	// rather than "blocked" — only the three enumerated privacy/safety reasons
	// warrant blocking.
	t.Run("no blockers is pass", func(t *testing.T) {
		got := p96ActivationStatusFromBlockers(nil)
		if got != "pass" {
			t.Fatalf("expected pass, got %q", got)
		}
	})

	t.Run("specific blocker reasons cause blocked", func(t *testing.T) {
		reasons := []string{
			"privacy-safe activation evidence failed",
			"privacy/redaction status is not release-safe",
			"quickstart activation smoke did not pass",
		}
		for _, reason := range reasons {
			blockers := []releaseEvidenceBlocker{{Reason: reason}}
			got := p96ActivationStatusFromBlockers(blockers)

			if got != "blocked" {
				t.Fatalf("expected blocked for reason %q, got %q", reason, got)
			}
		}
	})

	t.Run("generic blocker reason is needs_evidence", func(t *testing.T) {
		blockers := []releaseEvidenceBlocker{{Section: "activation", Reason: "incomplete"}}
		got := p96ActivationStatusFromBlockers(blockers)

		if got != "needs_evidence" {
			t.Fatalf("expected needs_evidence for generic blocker, got %q", got)
		}
	})
}

func TestReleaseEvidenceBuildInfoWithDeps(t *testing.T) {
	deps := Dependencies{
		Version: "1.0.0",
		Commit:  "abc123",
		Date:    "2026-05-15",
	}

	info := releaseEvidenceBuildInfoWithDeps(deps)
	if info.Version != "1.0.0" {
		t.Fatalf("version: got %q, want 1.0.0", info.Version)
	}

	if info.Commit != "abc123" {
		t.Fatalf("commit: got %q, want abc123", info.Commit)
	}

	if info.Date != "2026-05-15" {
		t.Fatalf("date: got %q, want 2026-05-15", info.Date)
	}

	if info.Go == "" || info.OSArch == "" {
		t.Fatalf("Go/OSArch should be auto-populated: %#v", info)
	}
}

func TestReleaseEvidenceHash(t *testing.T) {
	bundle1 := releaseEvidenceBundle{
		SchemaVersion: releaseEvidenceSchemaVersion,
		Status:        "pass",
	}
	bundle2 := releaseEvidenceBundle{
		SchemaVersion: releaseEvidenceSchemaVersion,
		Status:        "pass",
	}
	hash1 := releaseEvidenceHash(bundle1)
	hash2 := releaseEvidenceHash(bundle2)

	if hash1 != hash2 {
		t.Fatalf("identical bundles should produce same hash: %q vs %q", hash1, hash2)
	}

	if hash1 == "" {
		t.Fatal("hash should not be empty")
	}
}

func TestReleaseP96GateLinkage(t *testing.T) {
	t.Run("pass activation closes gate", func(t *testing.T) {
		row := productEvidenceReleaseGateLinkage{Gate: "P9.6", Status: "needs_evidence"}
		activation := releaseEvidenceActivationSection{Status: "pass"}
		result := releaseP96GateLinkage(row, activation)

		if result.Status != "evidence_present" {
			t.Fatalf("expected evidence_present, got %q", result.Status)
		}
	})

	t.Run("blocked activation blocks gate", func(t *testing.T) {
		row := productEvidenceReleaseGateLinkage{Gate: "P9.6", Status: "needs_evidence"}
		activation := releaseEvidenceActivationSection{Status: "blocked"}
		result := releaseP96GateLinkage(row, activation)

		if result.Status != "blocked" {
			t.Fatalf("expected blocked, got %q", result.Status)
		}
	})

	t.Run("other activation status needs evidence", func(t *testing.T) {
		row := productEvidenceReleaseGateLinkage{Gate: "P9.6", Status: "needs_evidence"}
		activation := releaseEvidenceActivationSection{Status: "partial"}
		result := releaseP96GateLinkage(row, activation)

		if result.Status != "needs_evidence" {
			t.Fatalf("expected needs_evidence, got %q", result.Status)
		}
	})
}

func TestReleaseP912GateLinkage(t *testing.T) {
	t.Run("already evidence_present with matching release status stays", func(t *testing.T) {
		row := productEvidenceReleaseGateLinkage{
			Gate:     "P9.12",
			Status:   "evidence_present",
			Counts:   map[string]int{},
			Blockers: nil,
		}
		result := releaseP912GateLinkage(row, "evidence_present")

		if result.Status != "evidence_present" {
			t.Fatalf("expected evidence_present, got %q", result.Status)
		}
	})

	t.Run("evidence_present with needs_evidence release downgrades", func(t *testing.T) {
		row := productEvidenceReleaseGateLinkage{
			Gate:     "P9.12",
			Status:   "evidence_present",
			Counts:   map[string]int{},
			Blockers: nil,
		}
		result := releaseP912GateLinkage(row, "needs_evidence")

		if result.Status != "needs_evidence" {
			t.Fatalf("expected needs_evidence, got %q", result.Status)
		}

		if result.Counts["release_client_readiness_blockers"] != 1 {
			t.Fatalf("expected release_client_readiness_blockers count, got %#v", result.Counts)
		}
	})

	t.Run("non evidence_present row remains unchanged", func(t *testing.T) {
		row := productEvidenceReleaseGateLinkage{
			Gate:   "P9.12",
			Status: "needs_evidence",
			Counts: map[string]int{},
		}
		result := releaseP912GateLinkage(row, "evidence_present")

		if result.Status != "needs_evidence" {
			t.Fatalf("expected needs_evidence to stay, got %q", result.Status)
		}
	})
}

func TestReleaseEvidenceDeltas(t *testing.T) {
	baseline := releaseEvidenceInputBundle{
		CommandCount:  4,
		ArtifactCount: 8,
	}
	current := releaseEvidenceInputBundle{
		CommandCount:  7,
		ArtifactCount: 8,
	}

	deltas := releaseEvidenceDeltas(baseline, current)
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}

	commandDelta := deltas[0]
	if commandDelta.Metric != "command_count" || commandDelta.Baseline != 4 || commandDelta.Current != 7 || commandDelta.Status != "present" {
		t.Fatalf("unexpected command delta: %#v", commandDelta)
	}

	artifactDelta := deltas[1]
	if artifactDelta.Metric != "artifact_count" || artifactDelta.Baseline != 8 || artifactDelta.Current != 8 || artifactDelta.Status != "present" {
		t.Fatalf("unexpected artifact delta: %#v", artifactDelta)
	}
}

func TestReleaseGateLinkageByGate(t *testing.T) {
	rows := []productEvidenceReleaseGateLinkage{
		{Gate: "P9.6", Status: "evidence_present"},
		{Gate: "P9.7", Status: "needs_evidence"},
	}

	t.Run("found gate returns match", func(t *testing.T) {
		got := releaseGateLinkageByGate(rows, "P9.6")
		if got.Status != "evidence_present" {
			t.Fatalf("expected evidence_present, got %#v", got)
		}
	})

	t.Run("missing gate returns needs_evidence default", func(t *testing.T) {
		got := releaseGateLinkageByGate(rows, "P9.99")
		if got.Status != "needs_evidence" {
			t.Fatalf("expected needs_evidence default, got %#v", got)
		}

		if len(got.Blockers) == 0 || !strings.Contains(got.Blockers[0], "P9.99") {
			t.Fatalf("expected blocker mentioning missing gate, got %#v", got.Blockers)
		}
	})
}
