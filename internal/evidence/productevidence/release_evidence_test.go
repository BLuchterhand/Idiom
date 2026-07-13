package productevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/evidence/lanehistory"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/localops"
)

var _ = ginkgo.Describe("release evidence", func() {
	ginkgo.It("sanitize release evidence telemetry args redacts path flags", func() {
		got := SanitizeReleaseEvidenceTelemetryArgs([]string{
			"--project-root=/private/repo",
			"--baseline-dir", "/private/baseline",
			"--sidecar-smoke", "",
			"--mode", "summary",
		})
		rendered := strings.Join(got, " ")

		for _, forbidden := range []string{"/private/repo", "/private/baseline"} {
			gomega.Expect(rendered).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("path flag leaked %q in %#v", forbidden, got))

		}
		gomega.Expect(rendered).To(gomega.ContainSubstring("<redacted_release_evidence_path id="), fmt.Sprintf("expected redacted release evidence path args, got %#v", got))
		gomega.Expect(rendered).To(gomega.ContainSubstring("--sidecar-smoke <empty>"), fmt.Sprintf("expected redacted release evidence path args, got %#v", got))
		gomega.Expect(rendered).To(gomega.ContainSubstring("--mode summary"), fmt.Sprintf("non-path flags should be preserved, got %#v", got))

	})
	ginkgo.It("p91 release evidence bundle includes required sections and classifications", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		stageReleaseEvidenceCommandBundle(root, "baseline", releaseEvidenceBaselineCommands())
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())
		writeP711JSON(filepath.Join(root, "testdata", "sidecar", "discovery-smoke.transcript.json"), map[string]any{
			"schema_version": "idiom.sidecar_smoke_transcript.v1",
			"name":           "p9.1-smoke",
		})

		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())
		_, _, err := comprehension.ApplyVerifiedRankerLearning(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
			Source:   "eval",
			Action:   "boost",
			Files:    []string{"release-evidence.go"},
			Verified: true,
			Reason:   comprehension.LearningReasonVerifiedFeedback,
			VerificationEvidence: &comprehension.VerificationEvidence{
				EditPaths:           []string{"release-evidence.go"},
				VerificationCommand: "go test ./internal/evidence/productevidence",
				VerificationPassed:  true,
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:        root,
			BaselineDir:        "baseline",
			CurrentDir:         "current",
			LocalOpsReport:     localOpsPath,
			ValidationRecords:  recordsPath,
			BenchmarkReport:    benchmarkPath,
			SidecarSmokeReport: filepath.Join("testdata", "sidecar", "discovery-smoke.transcript.json"),
		}, p711TestDemoDependencies)

		body, err := json.Marshal(report)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal release evidence: %v", err))
		gomega.Expect(report.SchemaVersion).To(gomega.Equal(releaseEvidenceSchemaVersion), fmt.Sprintf("release evidence missing identity fields: %#v", report))
		gomega.Expect(report.BundleID).NotTo(gomega.BeEmpty(), fmt.Sprintf("release evidence missing identity fields: %#v", report))
		gomega.Expect(report.ArtifactHash).NotTo(gomega.BeEmpty(), fmt.Sprintf("release evidence missing identity fields: %#v", report))
		gomega.Expect(report.Status).To(gomega.Equal("pass"), fmt.Sprintf("complete fixtures should produce pass bundle, got status=%q blockers=%#v", report.Status, report.Blockers))
		gomega.Expect(report.BaselineCurrent.Baseline.CommandCount).NotTo(gomega.Equal(0), fmt.Sprintf("baseline/current command evidence missing: %#v", report.BaselineCurrent))
		gomega.Expect(report.BaselineCurrent.Current.CommandCount).NotTo(gomega.Equal(0), fmt.Sprintf("baseline/current command evidence missing: %#v", report.BaselineCurrent))
		gomega.Expect(report.Validation.Status).To(gomega.Equal("pass"), fmt.Sprintf("validation section missing commands/statuses: %#v", report.Validation))
		gomega.Expect(report.Validation.Commands).NotTo(gomega.BeEmpty(), fmt.Sprintf("validation section missing commands/statuses: %#v", report.Validation))
		gomega.Expect(report.AuditSecurity.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("audit/security section missing current audit proof: %#v", report.AuditSecurity))
		gomega.Expect(report.AuditSecurity.HarnessAuditInputs).NotTo(gomega.BeEmpty(), fmt.Sprintf("audit/security section missing current audit proof: %#v", report.AuditSecurity))
		gomega.Expect(report.BenchmarkLocalOps.Status).To(gomega.Equal("pass"), fmt.Sprintf("benchmark/local-ops section incomplete: %#v", report.BenchmarkLocalOps))
		gomega.Expect(report.BenchmarkLocalOps.LocalOps.SmokeTotal).NotTo(gomega.Equal(0), fmt.Sprintf("benchmark/local-ops section incomplete: %#v", report.BenchmarkLocalOps))
		gomega.Expect(report.BenchmarkLocalOps.CompetitiveSignals).NotTo(gomega.BeEmpty(), fmt.Sprintf("benchmark/local-ops section incomplete: %#v", report.BenchmarkLocalOps))
		gomega.Expect(report.SidecarIntegration.Status).To(gomega.Equal("pass"), fmt.Sprintf("sidecar section should include all required integration paths: %#v", report.SidecarIntegration))
		gomega.Expect(releaseEvidenceHasIntegrationPath(report.SidecarIntegration.Paths, "shell_ci_script")).To(gomega.BeTrue(), fmt.Sprintf("sidecar section should include all required integration paths: %#v", report.SidecarIntegration))
		gomega.Expect(releaseEvidenceHasIntegrationPath(report.SidecarIntegration.Paths, "external_coding_agent_workflow")).To(gomega.BeTrue(), fmt.Sprintf("sidecar section should include all required integration paths: %#v", report.SidecarIntegration))
		gomega.Expect(releaseEvidenceHasIntegrationPath(report.SidecarIntegration.Paths, "mcp_stdio")).To(gomega.BeTrue(), fmt.Sprintf("sidecar section should include all required integration paths: %#v", report.SidecarIntegration))
		gomega.Expect(report.ProductEvidence.Status).To(gomega.Equal("pass"), fmt.Sprintf("product evidence section should consume P7/P8 inputs: %#v", report.ProductEvidence))
		gomega.Expect(report.ProductEvidence.SignalCompleteness.Status).To(gomega.Equal("complete"), fmt.Sprintf("product evidence section should consume P7/P8 inputs: %#v", report.ProductEvidence))

		if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.7"); gate.Status != "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("complete P9.7 validation fixture should satisfy release readiness: %#v", gate))
		} else if gate.Counts["complete_records"] == 0 || gate.Counts["primary_icp_records"] != 3 ||
			gate.Counts["daily_user_activation_records"] != 3 || len(gate.EvidenceIDs) != 3 || len(gate.Blockers) != 0 {
			ginkgo.Fail(fmt.Sprintf("complete P9.7 release readiness should expose structured gate data: %#v", gate))
		}

		if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.8"); gate.Status != "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("complete integration fixture should satisfy P9.8 release readiness: %#v", gate))
		} else if gate.Counts["executable_required_paths"] != 3 ||
			gate.Counts["mcp_stdio_parity_paths"] != 1 ||
			len(gate.Blockers) != 0 ||
			!stringSliceContains(gate.EvidenceIDs, "p7.8-mcp-stdio") {
			ginkgo.Fail(fmt.Sprintf("P9.8 release readiness should expose structured gate data: %#v", gate))
		}

		if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.10"); gate.Status != "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("complete competitive benchmark fixture should satisfy P9.10 release readiness: %#v", gate))
		} else if gate.Counts["required_external_baselines"] != 2 ||
			gate.Counts["covered_external_baselines"] != 2 ||
			len(gate.Blockers) != 0 ||
			!stringSliceContains(gate.EvidenceIDs, "competitive_baseline:aider_repo_map") {
			ginkgo.Fail(fmt.Sprintf("P9.10 release readiness should expose structured gate data: %#v", gate))
		}

		if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.11"); gate.Status != "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("complete client demo fixture should satisfy P9.11 release readiness: %#v", gate))
		} else if gate.Counts["covered_classifications"] != 7 ||
			gate.Counts["runnable_command_rows"] != 7 ||
			len(gate.Blockers) != 0 ||
			!stringSliceContains(gate.EvidenceIDs, "p7.10-enterprise-saas") {
			ginkgo.Fail(fmt.Sprintf("P9.11 release readiness should expose structured gate data: %#v", gate))
		}

		assertReleaseEvidenceClassifications(report.ClientReadiness)

		if forbidden := localops.ForbiddenEvidenceLeak(string(body)); forbidden != "" {
			ginkgo.Fail(fmt.Sprintf("release evidence leaked forbidden class %q: %s", forbidden, string(body)))
		}
	})
	ginkgo.It("release evidence summarizes open recovery rollup", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageProductEvidenceRecoveryPacket(root, "release-open-recovery")

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot: root,
			CurrentDir:  "current",
		}, p711TestDemoDependencies)
		gomega.Expect(report.Recovery.Status).To(gomega.Equal("partial"), fmt.Sprintf("expected open recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.OpenPacketCount).To(gomega.Equal(1), fmt.Sprintf("expected open recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.CandidateAtomCount).NotTo(gomega.Equal(0), fmt.Sprintf("expected open recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.RemainingAtomCount).NotTo(gomega.Equal(0), fmt.Sprintf("expected open recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(report.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("open recovery should be release-impact partial evidence, got status=%q blockers=%#v", report.Status, report.Blockers))
		gomega.Expect(report.Status).NotTo(gomega.Equal("blocked"), fmt.Sprintf("open recovery should be release-impact partial evidence, got status=%q blockers=%#v", report.Status, report.Blockers))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.Blockers, "recovery", "open recovery packets")).To(gomega.BeTrue(), fmt.Sprintf("release blockers should include open recovery impact, got %#v", report.Blockers))

	})
	ginkgo.It("release evidence recovered rollup does not add recovery blocker", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageProductEvidenceRecoveredPacket(root, "release-recovered")

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot: root,
			CurrentDir:  "current",
		}, p711TestDemoDependencies)
		gomega.Expect(report.Recovery.Status).To(gomega.Equal("pass"), fmt.Sprintf("expected recovered recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.RecoveredPacketCount).To(gomega.Equal(1), fmt.Sprintf("expected recovered recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.OpenPacketCount).To(gomega.Equal(0), fmt.Sprintf("expected recovered recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.RemainingAtomCount).To(gomega.Equal(0), fmt.Sprintf("expected recovered recovery rollup in release evidence, got %#v", report.Recovery))
		gomega.Expect(releaseEvidenceHasBlocker(report.Blockers, "recovery")).To(gomega.BeFalse(), fmt.Sprintf("recovered packet should not add a release recovery blocker: %#v", report.Blockers))

	})
	ginkgo.It("p911 release evidence requires executable command validation", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		p911MutateTranscriptByClass(root, "enterprise-saas", func(row map[string]any) {
			command := row["commands"].([]any)[0].(map[string]any)

			command["id"] = "mismatched-demo-command"
		})
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root, CurrentDir: "current"}, p711TestDemoDependencies)
		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.11")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("release P9.11 must fail closed on command ID mismatch: %#v", gate))
		gomega.Expect(gate.Counts["runnable_command_rows"]).NotTo(gomega.Equal(7), fmt.Sprintf("release P9.11 must fail closed on command ID mismatch: %#v", gate))
		gomega.Expect(gate.Counts["command_id_mismatch_rows"]).NotTo(gomega.Equal(0), fmt.Sprintf("release P9.11 must fail closed on command ID mismatch: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "command ID parity mismatch")).To(gomega.BeTrue(), fmt.Sprintf("release P9.11 command parity blocker not exposed: %#v", gate))

	})
	ginkgo.It("p91 release evidence marks partial when evidence missing", func() {
		root := ginkgo.GinkgoT().TempDir()
		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{}, releaseEvidenceOptions{
			ProjectRoot:        root,
			BaselineDir:        "missing-baseline",
			CurrentDir:         "missing-current",
			SidecarSmokeReport: "missing-smoke.json",
		}, p711TestDemoDependencies)
		gomega.Expect(report.Status).To(gomega.Equal("partial"), fmt.Sprintf("missing evidence should produce partial status, got %q", report.Status))

		for _, section := range []string{"baseline", "current", "validation", "benchmark_local_ops", "sidecar_integration", "product_evidence"} {
			gomega.Expect(releaseEvidenceHasBlocker(report.Blockers, section)).To(gomega.BeTrue(), fmt.Sprintf("expected blocker for %q, got %#v", section, report.Blockers))

		}
		gomega.Expect(report.BaselineCurrent.Baseline.Status).To(gomega.Equal("missing"), fmt.Sprintf("baseline/current missing status not recorded: %#v", report.BaselineCurrent))
		gomega.Expect(report.BaselineCurrent.Current.Status).To(gomega.Equal("missing"), fmt.Sprintf("baseline/current missing status not recorded: %#v", report.BaselineCurrent))
		gomega.Expect(report.Validation.Status).To(gomega.Equal("partial"), fmt.Sprintf("validation should be partial when current command artifacts are absent: %#v", report.Validation))

		assertReleaseEvidenceClassifications(report.ClientReadiness)
	})
	ginkgo.It("p913 release evidence downgrades client rows when validation records missing", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:     root,
			LocalOpsReport:  localOpsPath,
			BenchmarkReport: benchmarkPath,
		}, p711TestDemoDependencies)

		for _, row := range report.ClientReadiness {
			gomega.Expect(releaseEvidenceStringSliceContains(row.MissingEvidence, "validation record")).To(gomega.BeTrue(), fmt.Sprintf("classification %q should list missing validation evidence: %#v", row.Classification, row))
			gomega.Expect(row.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("classification %q with missing evidence must not pass: %#v", row.Classification, row))
			gomega.Expect(releaseEvidenceHasClientReadinessBlocker(report.Blockers, row.Classification)).To(gomega.BeTrue(), fmt.Sprintf("classification %q missing per-class blocker: %#v", row.Classification, report.Blockers))

		}
	})
	ginkgo.It("p913 release readiness needs evidence when client validation missing", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:     root,
			LocalOpsReport:  localOpsPath,
			BenchmarkReport: benchmarkPath,
		}, p711TestDemoDependencies)

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.12")
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("P9.12 must not be evidence_present while client validation evidence is missing: %#v", report.ReleaseReadiness))
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.12 should need evidence when client validation records are absent, got %#v", gate))

		p97 := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.7")
		gomega.Expect(p97.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.7 should need validation evidence when records are absent, got %#v", p97))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.Blockers, "release_readiness", "P9.7")).To(gomega.BeTrue(), fmt.Sprintf("release blockers should include P9.7 demand evidence gap: %#v", report.Blockers))

	})
	ginkgo.It("p96 release readiness needs measured clean install and cleanup evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)

		var localOps localops.BenchmarkReport

		data, err := os.ReadFile(localOpsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read local ops report: %v", err))
		gomega.Expect(json.Unmarshal(data, &localOps)).To(gomega.Succeed())

		localOps.Quickstart.EnvironmentClass = "source-build"
		localOps.Quickstart.DurationSource = "deterministic_command_step_seconds"
		localOps.Quickstart.SetupInstallReadiness.InstallSetupPath = "source-build-or-local-binary"
		localOps.Quickstart.SetupInstallReadiness.ReleaseInstallProof = "not_claimed"
		writeP711JSON(localOpsPath, localOps)

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:    root,
			LocalOpsReport: localOpsPath,
		}, p711TestDemoDependencies)
		gomega.Expect(report.ActivationOnboarding.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("source-build deterministic activation evidence must fail closed for P9.6: %#v", report.ActivationOnboarding))
		gomega.Expect(report.ActivationOnboarding.Classification).To(gomega.Equal("no-go"), fmt.Sprintf("source-build deterministic activation evidence must fail closed for P9.6: %#v", report.ActivationOnboarding))

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.6 must not be evidence_present without measured clean-install proof: %#v", gate))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "release binary clean-install proof")).To(gomega.BeTrue(), fmt.Sprintf("activation section missing clean-install blocker: %#v", report.ActivationOnboarding.Blockers))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "measured wall-clock")).To(gomega.BeTrue(), fmt.Sprintf("activation section missing measured timing blocker: %#v", report.ActivationOnboarding.Blockers))

	})
	ginkgo.It("p96 release activation malformed mandatory fields fail closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)

		var localOps localops.BenchmarkReport

		data, err := os.ReadFile(localOpsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read local ops report: %v", err))
		gomega.Expect(json.Unmarshal(data, &localOps)).To(gomega.Succeed())

		localOps.Quickstart.EnvironmentClass = ""
		localOps.Quickstart.SetupInstallReadiness.InstallSetupPath = ""
		localOps.Quickstart.SetupInstallReadiness.ReleaseInstallProof = ""
		localOps.Quickstart.FailureReason = localops.ActivationFailureReason{}
		localOps.Quickstart.DocsGap = localops.ActivationDocsGap{}
		localOps.Quickstart.Cleanup = localops.ActivationCleanupEvidence{Status: "pass", ExitStatus: "pass"}
		writeP711JSON(localOpsPath, localOps)

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:    root,
			LocalOpsReport: localOpsPath,
		}, p711TestDemoDependencies)
		gomega.Expect(report.ActivationOnboarding.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("malformed activation evidence must fail closed: %#v", report.ActivationOnboarding))
		gomega.Expect(report.ActivationOnboarding.Classification).NotTo(gomega.Equal("pass"), fmt.Sprintf("malformed activation evidence must fail closed: %#v", report.ActivationOnboarding))

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("malformed activation evidence must not satisfy P9.6: %#v", gate))

		for _, fragment := range []string{
			"environment class",
			"clean-install proof or scoped clean-setup proof",
			"failure reason semantics",
			"docs gap semantics",
			"cleanup evidence missing",
		} {
			gomega.Expect(releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", fragment)).To(gomega.BeTrue(), fmt.Sprintf("activation blockers missing %q: %#v", fragment, report.ActivationOnboarding.Blockers))

		}
	})
	ginkgo.It("p96 release readiness needs validation records for strict activation", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:    root,
			CurrentDir:     "current",
			LocalOpsReport: localOpsPath,
		}, p711TestDemoDependencies)
		gomega.Expect(report.ActivationOnboarding.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("activation readiness must fail closed without validation records: %#v", report.ActivationOnboarding))
		gomega.Expect(report.ActivationOnboarding.Classification).To(gomega.Equal("no-go"), fmt.Sprintf("activation readiness must fail closed without validation records: %#v", report.ActivationOnboarding))

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("strict activation without validation records must not close P9.6: %#v", gate))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "current validation records")).To(gomega.BeTrue(), fmt.Sprintf("activation section missing validation-record blocker: %#v", report.ActivationOnboarding.Blockers))

	})
	ginkgo.It("p96 release activation retained state policy must be reviewed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())
		stageP711LearningEvidence(root, "p96-retained-state")

		var localOps localops.BenchmarkReport

		data, err := os.ReadFile(localOpsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read local ops report: %v", err))
		gomega.Expect(json.Unmarshal(data, &localOps)).To(gomega.Succeed())

		localOps.Quickstart.Cleanup.Mode = "remove-smoke-artifacts"
		localOps.Quickstart.Cleanup.RemainingState = "context_graph_retained_by_policy"
		localOps.Quickstart.Cleanup.RetainedStatePolicy = "quickstart smoke retains local context graph by policy"
		writeP711JSON(localOpsPath, localOps)

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			CurrentDir:        "current",
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("unreviewed retained-state policy must not close P9.6: gate=%#v activation=%#v", gate, report.ActivationOnboarding))
		gomega.Expect(report.ActivationOnboarding.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("unreviewed retained-state policy must not close P9.6: gate=%#v activation=%#v", gate, report.ActivationOnboarding))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "reviewed retained-state semantics")).To(gomega.BeTrue(), fmt.Sprintf("activation section missing retained-state policy blocker: %#v", report.ActivationOnboarding.Blockers))

	})
	ginkgo.It("p96 release activation zero ranked packet fails closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())
		stageP711LearningEvidence(root, "p96-zero-packet")

		var localOps localops.BenchmarkReport

		data, err := os.ReadFile(localOpsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read local ops report: %v", err))
		gomega.Expect(json.Unmarshal(data, &localOps)).To(gomega.Succeed())

		localOps.Quickstart.FirstUsefulPacket.CandidateFileCount = 0
		localOps.Quickstart.FirstUsefulPacket.CandidateSymbolCount = 0
		localOps.Quickstart.FirstUsefulPacket.CandidateTestCount = 0
		localOps.Quickstart.FirstUsefulPacket.UsefulEvidence = []string{"candidate_files:0", "candidate_symbols:0", "candidate_tests:0"}
		writeP711JSON(localOpsPath, localOps)

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			CurrentDir:        "current",
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.6")
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("zero-ranked first packet must not close P9.6: gate=%#v activation=%#v", gate, report.ActivationOnboarding))
		gomega.Expect(report.ActivationOnboarding.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("zero-ranked first packet must not close P9.6: gate=%#v activation=%#v", gate, report.ActivationOnboarding))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.ActivationOnboarding.Blockers, "activation_onboarding", "first useful packet")).To(gomega.BeTrue(), fmt.Sprintf("activation section missing first-packet blocker: %#v", report.ActivationOnboarding.Blockers))

	})
	ginkgo.It("p97 release and product evidence readiness agree", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())
		stageP711LearningEvidence(root, "p97-agreement")

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)

		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			CurrentDir:        "current",
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)

		productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.7")
		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.7")
		gomega.Expect(productGate.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("product/release P9.7 readiness should agree and pass for complete fixture: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(releaseGate.Status).To(gomega.Equal(productGate.Status), fmt.Sprintf("product/release P9.7 readiness should agree and pass for complete fixture: product=%#v release=%#v", productGate, releaseGate))

		for _, key := range []string{"validation_records", "complete_records", "primary_icp_records", "primary_contexts", "daily_user_activation_records", "retained_use_records", "credible_primary_adoption_signals"} {
			gomega.Expect(productGate.Counts[key]).To(gomega.Equal(releaseGate.Counts[key]), fmt.Sprintf("product/release P9.7 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate))

		}
		gomega.Expect(strings.Join(productGate.Blockers, "|")).To(gomega.Equal(strings.Join(releaseGate.Blockers, "|")), fmt.Sprintf("product/release P9.7 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(strings.Join(productGate.EvidenceIDs, "|")).To(gomega.Equal(strings.Join(releaseGate.EvidenceIDs, "|")), fmt.Sprintf("product/release P9.7 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))

	})
	ginkgo.It("p98 release and product evidence readiness agree", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot: root,
		}, p711TestDemoDependencies)

		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot: root,
			CurrentDir:  "current",
		}, p711TestDemoDependencies)

		productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.8")
		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.8")
		gomega.Expect(productGate.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("product/release P9.8 readiness should agree and pass for complete fixture: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(releaseGate.Status).To(gomega.Equal(productGate.Status), fmt.Sprintf("product/release P9.8 readiness should agree and pass for complete fixture: product=%#v release=%#v", productGate, releaseGate))

		for _, key := range []string{"proof_rows", "executable_required_paths", "executable_required_path_shell_ci_script", "executable_required_path_external_coding_agent_workflow", "executable_required_path_mcp_stdio", "mcp_stdio_parity_paths"} {
			gomega.Expect(productGate.Counts[key]).To(gomega.Equal(releaseGate.Counts[key]), fmt.Sprintf("product/release P9.8 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate))

		}
		gomega.Expect(strings.Join(productGate.Blockers, "|")).To(gomega.Equal(strings.Join(releaseGate.Blockers, "|")), fmt.Sprintf("product/release P9.8 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(strings.Join(productGate.EvidenceIDs, "|")).To(gomega.Equal(strings.Join(releaseGate.EvidenceIDs, "|")), fmt.Sprintf("product/release P9.8 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))

	})
	ginkgo.It("p98 release readiness needs evidence when integration proofs incomplete", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		writeP711IntegrationProofs(root, []map[string]any{
			p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
			p711IntegrationProofWithMCPParityOnPath("p7.8-external-coding-agent", "external_coding_agent_workflow"),
		})

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot: root,
		}, p711TestDemoDependencies)

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.8")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.8 must not pass when MCP parity is on the wrong path: %#v", gate))
		gomega.Expect(gate.Counts["executable_required_paths"]).NotTo(gomega.BeNumerically(">=", 3), fmt.Sprintf("P9.8 release readiness should expose missing-path counts and blockers: %#v", gate))
		gomega.Expect(gate.Blockers).NotTo(gomega.BeEmpty(), fmt.Sprintf("P9.8 release readiness should expose missing-path counts and blockers: %#v", gate))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.Blockers, "release_readiness", "P9.8")).To(gomega.BeTrue(), fmt.Sprintf("release blockers should include P9.8 integration evidence gap: %#v", report.Blockers))

	})
	ginkgo.It("p97 release retained use without credible adoption signal does not pass", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())

		records := p97PrimaryRecords("none", "none")
		recordsPath := filepath.Join(root, "validation-records.json")
		writeP711JSON(recordsPath, records)

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			CurrentDir:        "current",
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)

		gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.7")
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("P9.7 must not pass without credible paid/internal-platform adoption signal: %#v", gate))
		gomega.Expect(gate.Evidence).To(gomega.ContainSubstring("adoption:0"), fmt.Sprintf("P9.7 evidence should expose missing adoption count: %#v", gate))
		gomega.Expect(gate.Counts["credible_primary_adoption_signals"]).To(gomega.Equal(0), fmt.Sprintf("P9.7 release readiness should expose structured adoption blocker data: %#v", gate))
		gomega.Expect(gate.Blockers).NotTo(gomega.BeEmpty(), fmt.Sprintf("P9.7 release readiness should expose structured adoption blocker data: %#v", gate))
		gomega.Expect(gate.EvidenceIDs).To(gomega.HaveLen(3), fmt.Sprintf("P9.7 release readiness should expose structured adoption blocker data: %#v", gate))

	})
	ginkgo.It("p914 release bundle downgrades every p912 object when client validation missing", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:     root,
			LocalOpsReport:  localOpsPath,
			BenchmarkReport: benchmarkPath,
		}, p711TestDemoDependencies)
		gomega.Expect(releaseEvidenceAnyClientMissingEvidence(report.ClientReadiness)).To(gomega.BeTrue(), fmt.Sprintf("fixture should contain client rows with missing validation evidence: %#v", report.ClientReadiness))

		body, err := json.Marshal(report)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal release evidence: %v", err))

		var decoded any
		gomega.Expect(json.Unmarshal(body, &decoded)).To(gomega.Succeed())

		statuses := releaseEvidenceP912Statuses(decoded)
		gomega.Expect(len(statuses) < 2).To(gomega.BeFalse(), fmt.Sprintf("expected top-level and embedded P9.12 objects, got statuses=%#v body=%s", statuses, string(body)))

		for _, status := range statuses {
			gomega.Expect(status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("no P9.12 object may be evidence_present while client evidence is missing: statuses=%#v body=%s", statuses, string(body)))

		}
	})
	ginkgo.It("p913 release benchmark needs competitive evidence with passing local ops", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)
		gomega.Expect(report.BenchmarkLocalOps.LocalOps.Status).To(gomega.Equal("pass"), fmt.Sprintf("fixture should keep local-ops replay passing: %#v", report.BenchmarkLocalOps.LocalOps))
		gomega.Expect(report.BenchmarkLocalOps.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("benchmark section must not pass without competitive benchmark evidence: %#v", report.BenchmarkLocalOps))
		gomega.Expect(releaseEvidenceHasBlockerReason(report.BenchmarkLocalOps.Blockers, "benchmark_local_ops", "competitive benchmark")).To(gomega.BeTrue(), fmt.Sprintf("benchmark section missing competitive evidence blocker: %#v", report.BenchmarkLocalOps.Blockers))

		if gate := releaseEvidenceReadinessGate(report.ReleaseReadiness, "P9.10"); gate.Status != "needs_evidence" {
			ginkgo.Fail(fmt.Sprintf("P9.10 release readiness must fail closed without competitive evidence: %#v", gate))
		}
	})
	ginkgo.It("p91 release evidence preserves privacy", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		current := filepath.Join(root, "current")
		gomega.Expect(os.MkdirAll(current, 0o755)).To(gomega.Succeed())

		metadata := "# sanitized\n\n" +
			"Command: `idiom tool context_status --profile discovery`\n\n" +
			"Exit status: `0`\n\n" +
			"Stdout artifact: `01-context-status.stdout.json`\n\n" +
			"Stderr artifact: `01-context-status.stderr.txt`\n\n" +
			"Failure/blocker: none recorded.\n"
		gomega.Expect(os.WriteFile(filepath.Join(current, "01-context-status.metadata.md"), []byte(metadata), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(current, "01-context-status.stdout.json"), []byte(`{"status":"pass","metadata_only":true}`), 0o644)).To(gomega.Succeed())

		report := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{}, releaseEvidenceOptions{
			ProjectRoot: root,
			CurrentDir:  "current",
		}, p711TestDemoDependencies)

		body, err := json.Marshal(report)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal release evidence: %v", err))

		for _, forbidden := range []string{root} {
			gomega.Expect(string(body)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("release evidence leaked %q: %s", forbidden, string(body)))

		}
		gomega.Expect(report.Privacy.ForbiddenFieldCheck).To(gomega.Equal("pass"), fmt.Sprintf("privacy contract should remain metadata-only/pass: %#v", report.Privacy))
		gomega.Expect(report.Privacy.MetadataOnly).To(gomega.BeTrue(), fmt.Sprintf("privacy contract should remain metadata-only/pass: %#v", report.Privacy))

	})
	ginkgo.It("release evidence privacy block recomputes identity after redaction", func() {
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
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal release evidence: %v", err))
		gomega.Expect(string(body)).NotTo(gomega.ContainSubstring("/home/example/private-repo"), fmt.Sprintf("privacy-blocked bundle should redact forbidden values: %s", string(body)))
		gomega.Expect(report.Status).To(gomega.Equal("blocked"), fmt.Sprintf("privacy block not reflected in report: %#v", report))
		gomega.Expect(report.Privacy.ForbiddenFieldCheck).To(gomega.Equal("failed:absolute_path"), fmt.Sprintf("privacy block not reflected in report: %#v", report))

		if want := releaseEvidenceHash(report); report.ArtifactHash != want || report.BundleID != "release-evidence-"+want {
			ginkgo.Fail(fmt.Sprintf("privacy-blocked identity should describe final body, got hash=%q bundle=%q want=%q", report.ArtifactHash, report.BundleID, want))
		}
	})
	ginkgo.It("run release evidence writes parseable json output", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		stageReleaseEvidenceCommandBundle(root, "baseline", releaseEvidenceBaselineCommands())
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())
		writeP711JSON(filepath.Join(root, "testdata", "sidecar", "discovery-smoke.transcript.json"), map[string]any{
			"schema_version": "idiom.sidecar_smoke_transcript.v1",
		})

		outputPath := filepath.Join(root, ".idiom", "release", "bundle.json")

		var stdout, stderr bytes.Buffer

		code := RunReleaseMainWithTelemetry([]string{
			"evidence",
			"--project-root", root,
			"--baseline-dir", "baseline",
			"--current-dir", "current",
			"--local-ops-report", localOpsPath,
			"--validation-records", recordsPath,
			"--sidecar-smoke", filepath.Join("testdata", "sidecar", "discovery-smoke.transcript.json"),
			"--output", outputPath,
		}, &stdout, &stderr, nil, p711TestDemoDependencies)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("release evidence command failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		data, err := os.ReadFile(outputPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read output: %v", err))

		var report releaseEvidenceBundle
		gomega.Expect(json.Unmarshal(data, &report)).To(gomega.Succeed())
		gomega.Expect(report.BundleID).NotTo(gomega.BeEmpty(), fmt.Sprintf("output missing release identity: %#v", report))
		gomega.Expect(report.SchemaVersion).To(gomega.Equal(releaseEvidenceSchemaVersion), fmt.Sprintf("output missing release identity: %#v", report))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(outputPath), fmt.Sprintf("CLI should not echo raw output path stdout=%s stderr=%s", stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring(outputPath), fmt.Sprintf("CLI should not echo raw output path stdout=%s stderr=%s", stdout.String(), stderr.String()))

	})
	// --- Utility function unit tests ---
	ginkgo.It("release commands match", func() {

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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseCommandsMatch(%q, %q) = %v, want %v", tt.actual, tt.expected, got, tt.want))

		}
	})
	ginkgo.It("release metadata value", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseMetadataValue(%q) = %q, want %q", tt.input, got, tt.want))

		}
	})
	ginkgo.It("release status from exit", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseStatusFromExit(%q, %q) = %q, want %q", tt.exit, tt.blocker, got, tt.want))

		}
	})
	ginkgo.It("release artifact kind", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseArtifactKind(%q) = %q, want %q", tt.name, got, tt.want))

		}
	})
	ginkgo.Describe("release evidence path", func() {
		ginkgo.It("returns an empty reference for an empty path", func() {
			root := ginkgo.GinkgoT().TempDir()
			ref := releaseEvidencePath(root, "")
			gomega.Expect(ref.PathID).To(gomega.Equal(""), fmt.Sprintf("expected empty path ref, got %#v", ref))

		})

		ginkgo.It("assigns a path ID to a non-empty path", func() {
			root := ginkgo.GinkgoT().TempDir()
			ref := releaseEvidencePath(root, "/some/path")
			gomega.Expect(ref.PathID).NotTo(gomega.Equal(""), "expected non-empty PathID")

		})

		ginkgo.It("records a relative path for artifacts under the project root", func() {
			root := ginkgo.GinkgoT().TempDir()
			subPath := filepath.Join(root, "subdir", "file.json")
			gomega.Expect(os.MkdirAll(filepath.Dir(subPath), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(subPath, []byte("{}"), 0o644)).To(gomega.Succeed())

			ref := releaseEvidencePath(root, subPath)
			gomega.Expect(ref.RelativePath).NotTo(gomega.Equal(""), fmt.Sprintf("expected relative path for path under root, got %#v", ref))
			gomega.Expect(filepath.IsAbs(ref.RelativePath)).To(gomega.BeFalse(), fmt.Sprintf("relative path should not be absolute: %q", ref.RelativePath))

		})
	})
	ginkgo.It("release evidence path flag", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseEvidencePathFlag(%q) = %v, want %v", tt.flag, got, tt.want))

		}
	})
	ginkgo.It("release activation approved environment class", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseActivationApprovedEnvironmentClass(%q) = %v, want %v", tt.value, got, tt.want))

		}
	})
	ginkgo.It("release activation failure reason explicit", func() {
		noFailure := localops.ActivationFailureReason{
			Status:       "none",
			PrimaryClass: "none",
			Reason:       "",
			RetryAllowed: false,
		}
		gomega.Expect(releaseActivationFailureReasonExplicit(noFailure)).To(gomega.BeTrue(), "explicit 'no failure' should return true")

		withStatus := localops.ActivationFailureReason{Status: "failed"}
		gomega.Expect(releaseActivationFailureReasonExplicit(withStatus)).To(gomega.BeFalse(), "non-none status should return false")

		withRetry := localops.ActivationFailureReason{
			Status:       "none",
			PrimaryClass: "none",
			RetryAllowed: true,
		}
		gomega.Expect(releaseActivationFailureReasonExplicit(withRetry)).To(gomega.BeFalse(), "retry allowed should return false")

	})
	ginkgo.It("release activation absent local state", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseActivationAbsentLocalState(%q, %q) = %v, want %v", tt.mode, tt.remaining, got, tt.want))

		}
	})
	ginkgo.It("release activation reviewed retained state policy", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseActivationReviewedRetainedStatePolicy(%q) = %v, want %v", tt.policy, got, tt.want))

		}
	})
	ginkgo.It("release delta status", func() {
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
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseDeltaStatus(%d, %d) = %q, want %q", tt.baseline, tt.current, got, tt.want))

		}
	})
	ginkgo.It("release evidence build info with deps", func() {

		deps := Dependencies{
			Version: "1.0.0",
			Commit:  "abc123",
			Date:    "2026-05-15",
		}

		info := releaseEvidenceBuildInfoWithDeps(deps)
		gomega.Expect(info.Version).To(gomega.Equal("1.0.0"), fmt.Sprintf("version: got %q, want 1.0.0", info.Version))
		gomega.Expect(info.Commit).To(gomega.Equal("abc123"), fmt.Sprintf("commit: got %q, want abc123", info.Commit))
		gomega.Expect(info.Date).To(gomega.Equal("2026-05-15"), fmt.Sprintf("date: got %q, want 2026-05-15", info.Date))
		gomega.Expect(info.Go).NotTo(gomega.BeEmpty(), fmt.Sprintf("Go/OSArch should be auto-populated: %#v", info))
		gomega.Expect(info.OSArch).NotTo(gomega.BeEmpty(), fmt.Sprintf("Go/OSArch should be auto-populated: %#v", info))

	})
	ginkgo.It("release evidence hash", func() {
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
		gomega.Expect(hash1).To(gomega.Equal(hash2), fmt.Sprintf("identical bundles should produce same hash: %q vs %q", hash1, hash2))
		gomega.Expect(hash1).NotTo(gomega.Equal(""), "hash should not be empty")

	})
	ginkgo.It("release evidence deltas", func() {
		baseline := releaseEvidenceInputBundle{
			CommandCount:  4,
			ArtifactCount: 8,
		}
		current := releaseEvidenceInputBundle{
			CommandCount:  7,
			ArtifactCount: 8,
		}

		deltas := releaseEvidenceDeltas(baseline, current)
		gomega.Expect(deltas).To(gomega.HaveLen(2), fmt.Sprintf("expected 2 deltas, got %d", len(deltas)))

		commandDelta := deltas[0]
		gomega.Expect(commandDelta.Metric).To(gomega.Equal("command_count"), fmt.Sprintf("unexpected command delta: %#v", commandDelta))
		gomega.Expect(commandDelta.Baseline).To(gomega.Equal(4), fmt.Sprintf("unexpected command delta: %#v", commandDelta))
		gomega.Expect(commandDelta.Current).To(gomega.Equal(7), fmt.Sprintf("unexpected command delta: %#v", commandDelta))
		gomega.Expect(commandDelta.Status).To(gomega.Equal("present"), fmt.Sprintf("unexpected command delta: %#v", commandDelta))

		artifactDelta := deltas[1]
		gomega.Expect(artifactDelta.Metric).To(gomega.Equal("artifact_count"), fmt.Sprintf("unexpected artifact delta: %#v", artifactDelta))
		gomega.Expect(artifactDelta.Baseline).To(gomega.Equal(8), fmt.Sprintf("unexpected artifact delta: %#v", artifactDelta))
		gomega.Expect(artifactDelta.Current).To(gomega.Equal(8), fmt.Sprintf("unexpected artifact delta: %#v", artifactDelta))
		gomega.Expect(artifactDelta.Status).To(gomega.Equal("present"), fmt.Sprintf("unexpected artifact delta: %#v", artifactDelta))

	})
})

func stageReleaseEvidenceCommandBundle(root string, dir string, commands []string) {
	ginkgo.GinkgoHelper()

	fullDir := filepath.Join(root, dir)
	gomega.Expect(os.MkdirAll(fullDir, 0o755)).To(gomega.Succeed())

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
		gomega.Expect(os.WriteFile(filepath.Join(fullDir, name+".metadata.md"), []byte(metadata), 0o644)).To(gomega.Succeed())

		writeP711JSON(filepath.Join(fullDir, stdoutName), map[string]any{"command_id": name, "status": "pass"})
		gomega.Expect(os.WriteFile(filepath.Join(fullDir, stderrName), []byte("(no stderr)\n"), 0o644)).To(gomega.Succeed())

	}
}

func stageP711LearningEvidence(root string, resultID string) {
	ginkgo.GinkgoHelper()
	_, err := comprehension.RecordVerifiedRankerEvidence(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
		Source:   "eval",
		Files:    []string{"release-evidence.go"},
		ResultID: resultID,
		Verified: true,
		Reason:   comprehension.LearningReasonVerifiedFeedback,
		Evidence: comprehension.LearningEvidenceRef{Kind: comprehension.LearningReasonVerifiedFeedback, Verified: true},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

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

func assertReleaseEvidenceClassifications(rows []releaseEvidenceClientReadiness) {
	ginkgo.GinkgoHelper()

	seen := map[string]releaseEvidenceClientReadiness{}
	for _, row := range rows {
		seen[row.Classification] = row
	}

	for _, class := range releaseEvidenceClientClassifications {
		row, ok := seen[class]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing client readiness row %q", class))
		gomega.Expect(row.ArchitecturePrimitives).NotTo(gomega.BeEmpty(), fmt.Sprintf("client row %q missing architecture primitives: %#v", class, row))

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

var _ = ginkgo.Describe("release evidence slice contains", func() {

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
		ginkgo.It(tt.name, func() {
			got := releaseEvidenceSliceContains(tt.values, tt.target)
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("releaseEvidenceSliceContains(%#v, %q) = %v, want %v", tt.values, tt.target, got, tt.want))

		})
	}
})
var _ = ginkgo.Describe("release safe command", func() {
	ginkgo.It("empty returns empty", func() {
		got := releaseSafeCommand("")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("expected empty, got %q", got))

	})
	ginkgo.It("safe command passes through", func() {
		got := releaseSafeCommand("idiom tool context_status")
		gomega.Expect(got).To(gomega.Equal("idiom tool context_status"), fmt.Sprintf("expected unchanged safe command, got %q", got))

	})
	ginkgo.It("multiline command is redacted to command_id", func() {
		got := releaseSafeCommand("idiom tool\nevil")
		gomega.Expect(strings.HasPrefix(got, "command_id:")).To(gomega.BeTrue(), fmt.Sprintf("expected command_id prefix for multiline, got %q", got))

	})
})
var _ = ginkgo.Describe("release product signal", func() {

	signals := []productEvidenceSignalSummary{
		{ID: "activation", Status: "pass"},
		{ID: "retained_use", Status: "partial"},
	}
	ginkgo.It("found signal", func() {
		got := releaseProductSignal(signals, "activation")
		gomega.Expect(got.Status).To(gomega.Equal("pass"), fmt.Sprintf("expected pass signal, got %#v", got))

	})
	ginkgo.It("missing signal returns empty", func() {
		got := releaseProductSignal(signals, "missing")
		gomega.Expect(got.ID).To(gomega.Equal(""), fmt.Sprintf("expected empty signal for missing ID, got %#v", got))

	})
	ginkgo.It("empty list returns empty", func() {
		got := releaseProductSignal(nil, "any")
		gomega.Expect(got.ID).To(gomega.Equal(""), fmt.Sprintf("expected empty signal, got %#v", got))

	})
})
var _ = ginkgo.Describe("release safe token", func() {
	ginkgo.It("integer string returns the integer", func() {
		got := releaseSafeToken("42")
		gomega.Expect(got).To(gomega.Equal("42"), fmt.Sprintf("expected '42', got %q", got))

	})
	ginkgo.It("negative integer passes through as int", func() {
		got := releaseSafeToken("-1")
		gomega.Expect(got).To(gomega.Equal("-1"), fmt.Sprintf("expected '-1', got %q", got))

	})
	ginkgo.It("non-integer is safe-tokened", func() {
		got := releaseSafeToken("some value")
		gomega.Expect(got).NotTo(gomega.Equal("some value"), fmt.Sprintf("non-integer should be safe-tokened, got %q", got))

	})
})
var _ = ginkgo.Describe("release evidence compact blockers", func() {
	ginkgo.It("empty returns empty", func() {
		got := releaseEvidenceCompactBlockers(nil)
		gomega.Expect(got).To(gomega.BeEmpty(), fmt.Sprintf("expected empty, got %#v", got))

	})
	ginkgo.It("unique entries preserved", func() {
		blockers := []releaseEvidenceBlocker{
			{Section: "s1", Reason: "r1"},
			{Section: "s2", Reason: "r2"},
		}
		got := releaseEvidenceCompactBlockers(blockers)
		gomega.Expect(got).To(gomega.HaveLen(2), fmt.Sprintf("expected 2 unique entries, got %d", len(got)))

	})
	ginkgo.It("duplicates removed", func() {
		blockers := []releaseEvidenceBlocker{
			{Section: "s1", Reason: "r1", Evidence: "e1"},
			{Section: "s1", Reason: "r1", Evidence: "e1"},
			{Section: "s2", Reason: "r2"},
		}
		got := releaseEvidenceCompactBlockers(blockers)
		gomega.Expect(got).To(gomega.HaveLen(2), fmt.Sprintf("expected 2 after dedup, got %d: %#v", len(got), got))

	})
})
var _ = ginkgo.Describe("redacted release evidence path arg", func() {
	ginkgo.It("empty returns <empty>", func() {
		got := redactedReleaseEvidencePathArg("")
		gomega.Expect(got).To(gomega.Equal("<empty>"), fmt.Sprintf("expected <empty>, got %q", got))

	})
	ginkgo.It("path gets redacted with stable id", func() {
		got := redactedReleaseEvidencePathArg("/private/data")
		gomega.Expect(strings.HasPrefix(got, "<redacted_release_evidence_path id=")).To(gomega.BeTrue(), fmt.Sprintf("expected redacted prefix, got %q", got))
		gomega.Expect(got).NotTo(gomega.ContainSubstring("/private/data"), fmt.Sprintf("raw path must not appear: %q", got))

	})
})
var _ = ginkgo.Describe("release activation docs gap explicit", func() {
	ginkgo.It("none status without recorded gap", func() {
		gap := localops.ActivationDocsGap{
			Status:      "none",
			GapRecorded: false,
		}
		gomega.Expect(releaseActivationDocsGapExplicit(gap)).To(gomega.BeTrue(), "none status without gap should return true")

	})
	ginkgo.It("none status with recorded gap is invalid", func() {
		gap := localops.ActivationDocsGap{
			Status:      "none",
			GapRecorded: true,
		}
		gomega.Expect(releaseActivationDocsGapExplicit(gap)).To(gomega.BeFalse(), "none + recorded gap should return false")

	})
	ginkgo.It("recorded status with all fields", func() {
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
		gomega.Expect(releaseActivationDocsGapExplicit(gap)).To(gomega.BeTrue(), "fully explicit recorded gap should return true")

	})
	ginkgo.It("recorded status with blocked activation is invalid", func() {
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
		gomega.Expect(releaseActivationDocsGapExplicit(gap)).To(gomega.BeFalse(), "recorded + blocked should return false")

	})
	ginkgo.It("unknown status returns false", func() {
		gap := localops.ActivationDocsGap{Status: "unknown"}
		gomega.Expect(releaseActivationDocsGapExplicit(gap)).To(gomega.BeFalse(), "unknown status should return false")

	})
})
var _ = ginkgo.Describe("release client readiness status", func() {
	ginkgo.It("pass with no missing stays pass", func() {
		got := releaseClientReadinessStatus("pass", nil)
		gomega.Expect(got).To(gomega.Equal("pass"), fmt.Sprintf("expected pass, got %q", got))

	})
	ginkgo.It("pass with missing evidence downgrades to needs_evidence", func() {
		got := releaseClientReadinessStatus("pass", []string{"missing_proof"})
		gomega.Expect(got).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence, got %q", got))

	})
	ginkgo.It("blocked stays blocked", func() {
		got := releaseClientReadinessStatus("blocked", []string{"missing"})
		gomega.Expect(got).To(gomega.Equal("blocked"), fmt.Sprintf("expected blocked, got %q", got))

	})
})
var _ = ginkgo.Describe("release benchmark section status", func() {
	// Section-level blockers cause "partial", not "blocked". The top-level
	// releaseEvidenceStatus reserves "blocked" for fundamental validity failures
	// (privacy violations, malformed artifacts). Section-level evidence gaps
	// (missing benchmarks, incomplete data) are "partial" — evidence present but
	// insufficient to close the gate. This is consistent across all sections.
	ginkgo.It("present with pass and no blockers", func() {
		got := releaseBenchmarkSectionStatus(true, "pass", nil)
		gomega.Expect(got).To(gomega.Equal("pass"), fmt.Sprintf("expected pass, got %q", got))

	})
	ginkgo.It("present with non-pass returns partial", func() {
		got := releaseBenchmarkSectionStatus(true, "fail", nil)
		gomega.Expect(got).To(gomega.Equal("partial"), fmt.Sprintf("expected partial for non-pass local-ops, got %q", got))

	})
	ginkgo.It("not present", func() {
		got := releaseBenchmarkSectionStatus(false, "", nil)
		gomega.Expect(got).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence, got %q", got))

	})
	ginkgo.It("blockers cause partial not blocked", func() {
		blockers := []releaseEvidenceBlocker{{Section: "benchmark", Reason: "missing"}}
		got := releaseBenchmarkSectionStatus(true, "pass", blockers)
		gomega.Expect(got).To(gomega.Equal("partial"), fmt.Sprintf("expected partial with blockers, got %q", got))

	})
})
var _ = ginkgo.Describe("release architecture primitives", func() {
	ginkgo.It("finance includes protected_path_posture", func() {
		got := releaseArchitecturePrimitives("finance")
		gomega.Expect(releaseEvidenceSliceContains(got, "protected_path_posture")).To(gomega.BeTrue(), fmt.Sprintf("finance missing protected_path_posture: %#v", got))
		gomega.Expect(releaseEvidenceSliceContains(got, "control_mapping")).To(gomega.BeTrue(), fmt.Sprintf("finance missing control_mapping: %#v", got))

	})
	ginkgo.It("tooling-vendor includes schema_parity", func() {
		got := releaseArchitecturePrimitives("tooling-vendor")
		gomega.Expect(releaseEvidenceSliceContains(got, "schema_parity")).To(gomega.BeTrue(), fmt.Sprintf("tooling-vendor missing schema_parity: %#v", got))

	})
	ginkgo.It("local-model includes degraded_mode_reporting", func() {
		got := releaseArchitecturePrimitives("local-model")
		gomega.Expect(releaseEvidenceSliceContains(got, "degraded_mode_reporting")).To(gomega.BeTrue(), fmt.Sprintf("local-model missing degraded_mode_reporting: %#v", got))

	})
	ginkgo.It("enterprise-saas defaults to context_freshness", func() {
		got := releaseArchitecturePrimitives("enterprise-saas")
		gomega.Expect(releaseEvidenceSliceContains(got, "context_freshness")).To(gomega.BeTrue(), fmt.Sprintf("enterprise-saas missing context_freshness: %#v", got))

	})
	ginkgo.It("all classes contain common primitives", func() {
		for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
			got := releaseArchitecturePrimitives(class)
			for _, common := range []string{"local_sidecar_contract", "metadata_only_evidence", "profile_gates", "redaction_contract", "reviewable_artifacts"} {
				gomega.Expect(releaseEvidenceSliceContains(got, common)).To(gomega.BeTrue(), fmt.Sprintf("class %q missing common primitive %q: %#v", class, common, got))

			}
		}
	})
})
var _ = ginkgo.Describe("release p912 readiness status", func() {
	ginkgo.It("no clients is needs_evidence", func() {
		got := releaseP912ReadinessStatus(nil, "evidence_present")
		gomega.Expect(got).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence, got %q", got))

	})
	ginkgo.It("P9.11 not present is needs_evidence", func() {
		clients := []releaseEvidenceClientReadiness{{Status: "pass"}}
		got := releaseP912ReadinessStatus(clients, "needs_evidence")
		gomega.Expect(got).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence, got %q", got))

	})
	ginkgo.It("client with non-pass status fails", func() {
		clients := []releaseEvidenceClientReadiness{
			{Status: "pass"},
			{Status: "needs_evidence"},
		}
		got := releaseP912ReadinessStatus(clients, "evidence_present")
		gomega.Expect(got).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence for non-pass client, got %q", got))

	})
	ginkgo.It("client with missing evidence fails", func() {
		clients := []releaseEvidenceClientReadiness{
			{Status: "pass", MissingEvidence: []string{"missing"}},
		}
		got := releaseP912ReadinessStatus(clients, "evidence_present")
		gomega.Expect(got).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence for missing evidence, got %q", got))

	})
	ginkgo.It("all clients pass and P9.11 present succeeds", func() {
		clients := []releaseEvidenceClientReadiness{
			{Status: "pass"},
			{Status: "pass"},
		}
		got := releaseP912ReadinessStatus(clients, "evidence_present")
		gomega.Expect(got).To(gomega.Equal("evidence_present"), fmt.Sprintf("expected evidence_present, got %q", got))

	})
})
var _ = ginkgo.Describe("p96 activation status from blockers", func() {
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
	ginkgo.It("no blockers is pass", func() {
		got := p96ActivationStatusFromBlockers(nil)
		gomega.Expect(got).To(gomega.Equal("pass"), fmt.Sprintf("expected pass, got %q", got))

	})
	ginkgo.It("specific blocker reasons cause blocked", func() {
		reasons := []string{
			"privacy-safe activation evidence failed",
			"privacy/redaction status is not release-safe",
			"quickstart activation smoke did not pass",
		}
		for _, reason := range reasons {
			blockers := []releaseEvidenceBlocker{{Reason: reason}}
			got := p96ActivationStatusFromBlockers(blockers)
			gomega.Expect(got).To(gomega.Equal("blocked"), fmt.Sprintf("expected blocked for reason %q, got %q", reason, got))

		}
	})
	ginkgo.It("generic blocker reason is needs_evidence", func() {
		blockers := []releaseEvidenceBlocker{{Section: "activation", Reason: "incomplete"}}
		got := p96ActivationStatusFromBlockers(blockers)
		gomega.Expect(got).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence for generic blocker, got %q", got))

	})
})
var _ = ginkgo.Describe("release p96 gate linkage", func() {
	ginkgo.It("pass activation closes gate", func() {
		row := productEvidenceReleaseGateLinkage{Gate: "P9.6", Status: "needs_evidence"}
		activation := releaseEvidenceActivationSection{Status: "pass"}
		result := releaseP96GateLinkage(row, activation)
		gomega.Expect(result.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("expected evidence_present, got %q", result.Status))

	})
	ginkgo.It("blocked activation blocks gate", func() {
		row := productEvidenceReleaseGateLinkage{Gate: "P9.6", Status: "needs_evidence"}
		activation := releaseEvidenceActivationSection{Status: "blocked"}
		result := releaseP96GateLinkage(row, activation)
		gomega.Expect(result.Status).To(gomega.Equal("blocked"), fmt.Sprintf("expected blocked, got %q", result.Status))

	})
	ginkgo.It("other activation status needs evidence", func() {
		row := productEvidenceReleaseGateLinkage{Gate: "P9.6", Status: "needs_evidence"}
		activation := releaseEvidenceActivationSection{Status: "partial"}
		result := releaseP96GateLinkage(row, activation)
		gomega.Expect(result.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence, got %q", result.Status))

	})
})
var _ = ginkgo.Describe("release p912 gate linkage", func() {
	ginkgo.It("already evidence_present with matching release status stays", func() {
		row := productEvidenceReleaseGateLinkage{
			Gate:     "P9.12",
			Status:   "evidence_present",
			Counts:   map[string]int{},
			Blockers: nil,
		}
		result := releaseP912GateLinkage(row, "evidence_present")
		gomega.Expect(result.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("expected evidence_present, got %q", result.Status))

	})
	ginkgo.It("evidence_present with needs_evidence release downgrades", func() {
		row := productEvidenceReleaseGateLinkage{
			Gate:     "P9.12",
			Status:   "evidence_present",
			Counts:   map[string]int{},
			Blockers: nil,
		}
		result := releaseP912GateLinkage(row, "needs_evidence")
		gomega.Expect(result.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence, got %q", result.Status))
		gomega.Expect(result.Counts["release_client_readiness_blockers"]).To(gomega.Equal(1), fmt.Sprintf("expected release_client_readiness_blockers count, got %#v", result.Counts))

	})
	ginkgo.It("non evidence_present row remains unchanged", func() {
		row := productEvidenceReleaseGateLinkage{
			Gate:   "P9.12",
			Status: "needs_evidence",
			Counts: map[string]int{},
		}
		result := releaseP912GateLinkage(row, "evidence_present")
		gomega.Expect(result.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence to stay, got %q", result.Status))

	})
})
var _ = ginkgo.Describe("release gate linkage by gate", func() {

	rows := []productEvidenceReleaseGateLinkage{
		{Gate: "P9.6", Status: "evidence_present"},
		{Gate: "P9.7", Status: "needs_evidence"},
	}
	ginkgo.It("found gate returns match", func() {
		got := releaseGateLinkageByGate(rows, "P9.6")
		gomega.Expect(got.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("expected evidence_present, got %#v", got))

	})
	ginkgo.It("missing gate returns needs_evidence default", func() {
		got := releaseGateLinkageByGate(rows, "P9.99")
		gomega.Expect(got.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("expected needs_evidence default, got %#v", got))
		gomega.Expect(got.Blockers).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected blocker mentioning missing gate, got %#v", got.Blockers))
		gomega.Expect(got.Blockers[0]).To(gomega.ContainSubstring("P9.99"), fmt.Sprintf("expected blocker mentioning missing gate, got %#v", got.Blockers))

	})
})
