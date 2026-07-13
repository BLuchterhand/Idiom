package productevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/evidence/lanehistory"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/localops"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.Describe("product evidence", func() {
	ginkgo.It("sanitize product evidence telemetry args redacts path flags", func() {
		got := SanitizeProductEvidenceTelemetryArgs([]string{
			"--project-root", "/private/repo",
			"--output=/tmp/product.json",
			"--benchmark-report", "",
			"--mode", "summary",
		})
		rendered := strings.Join(got, " ")

		for _, forbidden := range []string{"/private/repo", "/tmp/product.json"} {
			gomega.Expect(rendered).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("path flag leaked %q in %#v", forbidden, got))

		}
		gomega.Expect(rendered).To(gomega.ContainSubstring("<redacted_product_evidence_path id="), fmt.Sprintf("expected redacted product evidence path args, got %#v", got))
		gomega.Expect(rendered).To(gomega.ContainSubstring("--benchmark-report <empty>"), fmt.Sprintf("expected redacted product evidence path args, got %#v", got))
		gomega.Expect(rendered).To(gomega.ContainSubstring("--mode summary"), fmt.Sprintf("non-path flags should be preserved, got %#v", got))

	})
	ginkgo.It("p711 product evidence export completeness privacy and class coverage", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())
		_, _, err := comprehension.ApplyManualRankerNudge(ginkgo.GinkgoT().Context(), root, "boost", []string{"cmd/idiom/product_evidence.go"}, "", "p711-product-evidence")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 2}, productEvidenceExportOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
			BenchmarkReport:   benchmarkPath,
		}, p711TestDemoDependencies)

		body, err := json.Marshal(report)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal product evidence: %v", err))

		var reparsed productEvidenceExportReport
		gomega.Expect(json.Unmarshal(body, &reparsed)).To(gomega.Succeed())
		gomega.Expect(reparsed.SignalCompleteness.Status).To(gomega.Equal("complete"), fmt.Sprintf("expected complete P7.11 signal matrix, got %#v", reparsed.SignalCompleteness))
		gomega.Expect(reparsed.SignalCompleteness.CompleteCount).To(gomega.Equal(reparsed.SignalCompleteness.RequiredCount), fmt.Sprintf("expected complete P7.11 signal matrix, got %#v", reparsed.SignalCompleteness))

		for _, id := range []string{"activation", "retained_use", "integration_proof", "benchmark", "audit", "learning"} {
			gomega.Expect(p711TestHasSignal(reparsed.Signals, id)).To(gomega.BeTrue(), fmt.Sprintf("export missing signal %q: %#v", id, reparsed.Signals))

		}

		if got := p711TestSignal(reparsed.Signals, "activation"); got.Status != "pass" || got.Counts["candidate_files"] == 0 {
			ginkgo.Fail(fmt.Sprintf("activation signal should summarize quickstart evidence: %#v", got))
		}

		if got := p711TestSignal(reparsed.Signals, "retained_use"); got.Status != "pass" || got.Counts["adoption_signals"] == 0 {
			ginkgo.Fail(fmt.Sprintf("retained-use signal should summarize validation records: %#v", got))
		}

		if linkage := p711TestReleaseGate(reparsed.ReleaseGateLinkage, "P9.7"); linkage.Status != "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("P9.7 linkage should require and accept complete primary-ICP demand evidence: %#v", linkage))
		} else if linkage.Counts["complete_records"] != 9 || linkage.Counts["primary_icp_records"] != 3 ||
			linkage.Counts["daily_user_activation_records"] != 3 || linkage.Counts["retained_use_records"] == 0 ||
			len(linkage.EvidenceIDs) != 3 || len(linkage.Blockers) != 0 {
			ginkgo.Fail(fmt.Sprintf("P9.7 linkage should expose structured counts and evidence IDs: %#v", linkage))
		}
		gomega.Expect(reparsed.Status).To(gomega.Equal("pass"), fmt.Sprintf("top-level status should reflect complete signal evidence, got %q", reparsed.Status))

		integration := p711TestSignal(reparsed.Signals, "integration_proof")
		gomega.Expect(integration.Status).To(gomega.Equal("pass"), fmt.Sprintf("integration signal should include executable MCP stdio parity: %#v", integration))
		gomega.Expect(integration.Counts["mcp_stdio_parity_paths"]).To(gomega.Equal(1), fmt.Sprintf("integration signal should include executable MCP stdio parity: %#v", integration))
		gomega.Expect(stringSliceContains(integration.EvidenceIDs, "p7.8-mcp-stdio")).To(gomega.BeTrue(), fmt.Sprintf("integration signal should include executable MCP stdio parity: %#v", integration))

		if linkage := p711TestReleaseGate(reparsed.ReleaseGateLinkage, "P9.8"); linkage.Status != "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("P9.8 linkage should consume integration proof parity: %#v", reparsed.ReleaseGateLinkage))
		} else if linkage.Counts["executable_required_paths"] != 3 ||
			linkage.Counts["mcp_stdio_parity_paths"] != 1 ||
			len(linkage.Blockers) != 0 ||
			!stringSliceContains(linkage.EvidenceIDs, "p7.8-mcp-stdio") {
			ginkgo.Fail(fmt.Sprintf("P9.8 linkage should expose structured counts and evidence IDs: %#v", linkage))
		}

		if linkage := p711TestReleaseGate(reparsed.ReleaseGateLinkage, "P9.10"); linkage.Status != "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("P9.10 linkage should consume external competitive benchmark coverage: %#v", linkage))
		} else if linkage.Counts["required_external_baselines"] != 2 ||
			linkage.Counts["covered_external_baselines"] != 2 ||
			len(linkage.Blockers) != 0 ||
			!stringSliceContains(linkage.EvidenceIDs, "competitive_baseline:aider_repo_map") {
			ginkgo.Fail(fmt.Sprintf("P9.10 linkage should expose structured counts and evidence IDs: %#v", linkage))
		}

		if got := p711TestSignal(reparsed.Signals, "learning"); got.Status != "pass" || got.Counts["accepted"] == 0 {
			ginkgo.Fail(fmt.Sprintf("learning signal should summarize sanitized ranker learning: %#v", got))
		}
		gomega.Expect(reparsed.Privacy.MetadataOnly).To(gomega.BeTrue(), fmt.Sprintf("privacy contract should be safe by default: %#v", reparsed.Privacy))
		gomega.Expect(reparsed.Privacy.ForbiddenFieldCheck).To(gomega.Equal("pass"), fmt.Sprintf("privacy contract should be safe by default: %#v", reparsed.Privacy))
		gomega.Expect(reparsed.Privacy.RawPromptCaptured).To(gomega.BeFalse(), fmt.Sprintf("privacy contract should be safe by default: %#v", reparsed.Privacy))
		gomega.Expect(reparsed.Privacy.RawFileBodyCaptured).To(gomega.BeFalse(), fmt.Sprintf("privacy contract should be safe by default: %#v", reparsed.Privacy))
		gomega.Expect(reparsed.Privacy.SecretCaptured).To(gomega.BeFalse(), fmt.Sprintf("privacy contract should be safe by default: %#v", reparsed.Privacy))
		gomega.Expect(reparsed.Privacy.PrivatePathCaptured).To(gomega.BeFalse(), fmt.Sprintf("privacy contract should be safe by default: %#v", reparsed.Privacy))

		if finding := localops.ForbiddenEvidenceLeak(string(body)); finding != "" {
			ginkgo.Fail(fmt.Sprintf("product evidence export leaked forbidden evidence class %q: %s", finding, string(body)))
		}

		for _, forbidden := range []string{
			root,
			"/Users/",
			"/tmp/",
			"CustomerX",
			"sk_live",
			"RAW_PROMPT_BODY",
			"RAW_FILE_BODY",
			"func LeakedBody",
		} {
			gomega.Expect(string(body)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("product evidence export leaked %q: %s", forbidden, string(body)))

		}

		assertP711CoverageForEveryClassification(reparsed.ClassCoverage, 1)
	})
	ginkgo.It("product evidence summarizes recovery rollups", func() {
		root := ginkgo.GinkgoT().TempDir()
		packet := stageProductEvidenceRecoveryPacket(root, "open-recovery")

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)

		body, err := json.Marshal(report)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal product evidence: %v", err))
		gomega.Expect(report.Recovery.Status).To(gomega.Equal("partial"), fmt.Sprintf("expected open recovery rollup, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.FailureRecordCount).To(gomega.Equal(1), fmt.Sprintf("expected open recovery rollup, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.DecompositionPacketCount).To(gomega.Equal(1), fmt.Sprintf("expected open recovery rollup, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.OpenPacketCount).To(gomega.Equal(1), fmt.Sprintf("expected open recovery rollup, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.CandidateAtomCount).To(gomega.Equal(len(packet.CandidateAtoms)), fmt.Sprintf("expected open recovery rollup, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.RemainingAtomCount).To(gomega.Equal(len(packet.CandidateAtoms)), fmt.Sprintf("expected open recovery rollup, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.RedactionStatus).To(gomega.Equal("redacted"), fmt.Sprintf("recovery rollup should remain metadata-only/redacted, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.MetadataOnly).To(gomega.BeTrue(), fmt.Sprintf("recovery rollup should remain metadata-only/redacted, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.ForbiddenFieldCheck).To(gomega.Equal("pass"), fmt.Sprintf("recovery rollup should remain metadata-only/redacted, got %#v", report.Recovery))
		gomega.Expect(report.Recovery.FailureClasses[string(recoverycontract.FailureClassExecutorEdit)]).To(gomega.Equal(1), fmt.Sprintf("recovery rollup missing failure class or next action: %#v", report.Recovery))
		gomega.Expect(report.Recovery.NextActions[string(recoverycontract.NextActionReviewDecomposition)]).To(gomega.Equal(1), fmt.Sprintf("recovery rollup missing failure class or next action: %#v", report.Recovery))

		if ref := p711TestSourceRef(report.SourceReferences, "recovery_artifacts"); ref.Status != "present" {
			ginkgo.Fail(fmt.Sprintf("recovery source reference missing: %#v", report.SourceReferences))
		}
		gomega.Expect(string(body)).NotTo(gomega.ContainSubstring("executor failed while editing a private branch"), fmt.Sprintf("product evidence leaked recovery failure summary: %s", string(body)))

	})
	ginkgo.It("product evidence blocks invalid recovery evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		packet := stageProductEvidenceRecoveryPacket(root, "stale-reconciliation")
		reconciliationPath := filepath.Join(root, ".idiom", "recovery", packet.FailureID, "reconciliation.json")
		gomega.Expect(os.WriteFile(reconciliationPath, []byte(`{"schema_version":"wrong"}`), 0o644)).To(gomega.Succeed())

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gomega.Expect(report.Status).To(gomega.Equal("blocked"), fmt.Sprintf("invalid recovery reconciliation should block product evidence, got status=%q recovery=%#v", report.Status, report.Recovery))
		gomega.Expect(report.Recovery.Status).To(gomega.Equal("blocked"), fmt.Sprintf("invalid recovery reconciliation should block product evidence, got status=%q recovery=%#v", report.Status, report.Recovery))
		gomega.Expect(report.Recovery.MalformedArtifactCount).To(gomega.Equal(1), fmt.Sprintf("invalid recovery reconciliation should block product evidence, got status=%q recovery=%#v", report.Status, report.Recovery))
		gomega.Expect(p711TestBlockerContains(report.Recovery.Blockers, "reconciliation_decode_failed")).To(gomega.BeTrue(), fmt.Sprintf("expected invalid reconciliation blocker, got %#v", report.Recovery.Blockers))

	})
	ginkgo.It("p711 product evidence keeps activation useful without overclaiming p96", func() {
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
		localOps.Quickstart.Cleanup.Mode = "remove-smoke-artifacts"
		localOps.Quickstart.Cleanup.RemainingState = "context_graph_retained_by_policy"
		localOps.Quickstart.Cleanup.RetainedStatePolicy = "quickstart smoke retains local context graph by policy"
		writeP711JSON(localOpsPath, localOps)

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:    root,
			LocalOpsReport: localOpsPath,
		}, p711TestDemoDependencies)

		activation := p711TestSignal(report.Signals, "activation")
		gomega.Expect(activation.Status).To(gomega.Equal("pass"), fmt.Sprintf("source-build quickstart activation should remain useful product evidence: %#v", activation))
		gomega.Expect(activation.Completeness).To(gomega.Equal("complete"), fmt.Sprintf("source-build quickstart activation should remain useful product evidence: %#v", activation))
		gomega.Expect(activation.Counts["candidate_files"]).NotTo(gomega.Equal(0), fmt.Sprintf("source-build quickstart activation should remain useful product evidence: %#v", activation))
		gomega.Expect(report.ProductSuccessGates.Activation).To(gomega.Equal("evidence_present"), fmt.Sprintf("product activation signal should remain present without being release-ready: %#v", report.ProductSuccessGates))

		if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.6"); linkage.Status != "needs_evidence" {
			ginkgo.Fail(fmt.Sprintf("P9.6 linkage must not overclaim source-build deterministic smoke: %#v", report.ReleaseGateLinkage))
		}

		body, err := json.Marshal(report)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal product evidence: %v", err))

		if forbidden := localops.ForbiddenEvidenceLeak(string(body)); forbidden != "" {
			ginkgo.Fail(fmt.Sprintf("product evidence export leaked forbidden evidence class %q: %s", forbidden, string(body)))
		}
	})
	ginkgo.It("p96 product evidence strict activation without validation records needs evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:    root,
			LocalOpsReport: localOpsPath,
		}, p711TestDemoDependencies)

		activation := p711TestSignal(report.Signals, "activation")
		gomega.Expect(activation.Status).To(gomega.Equal("pass"), fmt.Sprintf("fixture should keep activation useful while validation records are absent: %#v", activation))
		gomega.Expect(activation.Completeness).To(gomega.Equal("complete"), fmt.Sprintf("fixture should keep activation useful while validation records are absent: %#v", activation))

		linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.6")
		gomega.Expect(linkage.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("standalone product export must not close P9.6 without current validation proof: %#v", linkage))
		gomega.Expect(linkage.Evidence).To(gomega.ContainSubstring("cannot close P9.6"), fmt.Sprintf("P9.6 linkage should explain standalone product limitation: %#v", linkage))

	})
	ginkgo.It("p96 product evidence zero ranked packet does not pass activation or p96", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)

		var localOps localops.BenchmarkReport

		data, err := os.ReadFile(localOpsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read local ops report: %v", err))
		gomega.Expect(json.Unmarshal(data, &localOps)).To(gomega.Succeed())

		localOps.Quickstart.FirstUsefulPacket.CandidateFileCount = 0
		localOps.Quickstart.FirstUsefulPacket.CandidateSymbolCount = 0
		localOps.Quickstart.FirstUsefulPacket.CandidateTestCount = 0
		localOps.Quickstart.FirstUsefulPacket.UsefulEvidence = []string{"candidate_files:0", "candidate_symbols:0", "candidate_tests:0"}
		writeP711JSON(localOpsPath, localOps)

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:    root,
			LocalOpsReport: localOpsPath,
		}, p711TestDemoDependencies)

		activation := p711TestSignal(report.Signals, "activation")
		gomega.Expect(activation.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("zero-ranked packet evidence must not pass activation signal: %#v", activation))
		gomega.Expect(activation.Completeness).NotTo(gomega.Equal("complete"), fmt.Sprintf("zero-ranked packet evidence must not pass activation signal: %#v", activation))
		gomega.Expect(activation.Counts["useful_evidence_count"]).To(gomega.Equal(0), fmt.Sprintf("zero-ranked packet should not report useful evidence counts: %#v", activation.Counts))

		if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.6"); linkage.Status == "evidence_present" {
			ginkgo.Fail(fmt.Sprintf("zero-ranked packet evidence must not close P9.6: %#v", linkage))
		}
	})
	ginkgo.It("p911 product evidence current transcript fixture passes", func() {
		repoRoot := p711TestRepoRoot()
		report := buildProductEvidenceExportReportWithDeps(repoRoot, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: repoRoot}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
		gomega.Expect(gate.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("current P9.11 fixture should pass runnable demo gate: %#v", gate))
		gomega.Expect(gate.Counts["covered_classifications"]).To(gomega.Equal(7), fmt.Sprintf("P9.11 current fixture should expose complete counts: %#v", gate))
		gomega.Expect(gate.Counts["runnable_command_rows"]).To(gomega.Equal(7), fmt.Sprintf("P9.11 current fixture should expose complete counts: %#v", gate))
		gomega.Expect(gate.Counts["expected_output_rows"]).To(gomega.Equal(7), fmt.Sprintf("P9.11 current fixture should expose complete counts: %#v", gate))
		gomega.Expect(gate.Counts["blockers"]).To(gomega.Equal(0), fmt.Sprintf("P9.11 current fixture should expose complete counts: %#v", gate))

		for _, id := range []string{"p7.10_client_demo_transcripts", "p7.10-enterprise-saas-source-sanity"} {
			gomega.Expect(stringSliceContains(gate.EvidenceIDs, id)).To(gomega.BeTrue(), fmt.Sprintf("P9.11 evidence IDs missing %q: %#v", id, gate))

		}
	})
	ginkgo.It("p911 product evidence missing and duplicate classification fail closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")

		var set map[string]any

		p711ReadTestJSON(path, &set)

		transcripts := set["transcripts"].([]any)

		set["transcripts"] = append(transcripts[1:], transcripts[1])
		writeP711JSON(path, set)

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.11 must fail closed on missing and duplicate classifications: %#v", gate))
		gomega.Expect(gate.Counts["missing_classifications"]).NotTo(gomega.Equal(0), fmt.Sprintf("P9.11 must fail closed on missing and duplicate classifications: %#v", gate))
		gomega.Expect(gate.Counts["duplicate_classifications"]).NotTo(gomega.Equal(0), fmt.Sprintf("P9.11 must fail closed on missing and duplicate classifications: %#v", gate))

		for _, want := range []string{"missing client demo classification", "duplicate client demo classification"} {
			gomega.Expect(p711TestBlockerContains(gate.Blockers, want)).To(gomega.BeTrue(), fmt.Sprintf("P9.11 blocker %q not exposed: %#v", want, gate))

		}
	})
	ginkgo.It("p911 product and release evidence reject unsafe command after safe replay", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		p911MutateTranscriptByClass(root, "enterprise-saas", func(row map[string]any) {
			commands := row["commands"].([]any)
			first := commands[0].(map[string]any)

			commands = append(commands, map[string]any{
				"id":        first["id"],
				"command":   "idiom backend status",
				"argv":      []string{"backend", "status"},
				"exit_code": 0,
				"json":      true,
				"expected_output_shape": map[string]any{
					"stable_fields": []string{"status"},
				},
			})
			row["commands"] = commands
		})
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root, CurrentDir: "current"}, p711TestDemoDependencies)
		productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.11")
		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.11")

		for label, gate := range map[string]struct {
			Status   string
			Counts   map[string]int
			Blockers []string
		}{
			"product": {
				Status:   productGate.Status,
				Counts:   productGate.Counts,
				Blockers: productGate.Blockers,
			},
			"release": {
				Status:   releaseGate.Status,
				Counts:   releaseGate.Counts,
				Blockers: releaseGate.Blockers,
			},
		} {
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("%s P9.11 must reject unsafe command appended after safe replay: %#v", label, gate))
			gomega.Expect(gate.Counts["runnable_command_rows"]).NotTo(gomega.Equal(7), fmt.Sprintf("%s P9.11 must reject unsafe command appended after safe replay: %#v", label, gate))
			gomega.Expect(gate.Counts["declared_command_rows"]).To(gomega.Equal(8), fmt.Sprintf("%s P9.11 must reject unsafe command appended after safe replay: %#v", label, gate))
			gomega.Expect(gate.Counts["command_execution_blockers"]).NotTo(gomega.Equal(0), fmt.Sprintf("%s P9.11 must reject unsafe command appended after safe replay: %#v", label, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, "unsafe top-level subcommand")).To(gomega.BeTrue(), fmt.Sprintf("%s P9.11 unsafe appended-command blocker not exposed: %#v", label, gate))

		}
	})
	ginkgo.It("p911 discovery profile denied side effect replay does not write", func() {
		root := ginkgo.GinkgoT().TempDir()
		commandID := "public-sector-offline-side-effect-denial"
		demo := p711DemoTranscript{
			Classification: "public-sector",
			P76CommandID:   commandID,
		}
		command := p711DemoCommand{
			ID:       commandID,
			Command:  "idiom tool shell -config $IDIOM_CONFIG --profile discovery --caller-id pubsec-ci --request-id p710-pub --input '{\"command\":\"printf touched > network-side-effect.txt\"}'",
			Argv:     []string{"tool", "shell", "-config", "$IDIOM_CONFIG", "--profile", "discovery", "--caller-id", "pubsec-ci", "--request-id", "p710-pub", "--input", "{\"command\":\"printf touched > network-side-effect.txt\"}"},
			ExitCode: 1,
			JSON:     true,
			ExpectedOutputShape: p711ExpectedOutputShape{
				StableFields: []string{"Tool", "Error", "sidecar.error.code"},
				JSONString: map[string]string{
					"Tool": "shell",
				},
				StdoutExcludes: []string{"touched"},
			},
		}

		if blockers := p911DemoCommandShapeBlockers(demo, command); len(blockers) != 0 {
			ginkgo.Fail(fmt.Sprintf("discovery-profile denied side-effect command should satisfy safe shape: %#v", blockers))
		}

		if blockers := p911ExecuteDemoCommandAtRootWithDeps(root, demo, command, p711TestDemoDependencies); len(blockers) != 0 {
			ginkgo.Fail(fmt.Sprintf("discovery-profile denied side-effect replay should pass as denied evidence: %#v", blockers))
		}

		if _, err := os.Stat(filepath.Join(root, "network-side-effect.txt")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("denied discovery-profile shell replay created side-effect marker, stat err=%v", err))
		}
	})
	ginkgo.It("p911 product evidence malformed transcript artifact fails closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")

		data, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read transcript fixture: %v", err))

		data = bytes.Replace(data, []byte(`"transcripts":`), []byte(`"unexpected_field": true, "transcripts":`), 1)
		gomega.Expect(os.WriteFile(path, data, 0o644)).To(gomega.Succeed())

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.11 must fail closed on malformed transcript artifact: %#v", gate))
		gomega.Expect(gate.Counts["malformed_transcripts"]).NotTo(gomega.Equal(0), fmt.Sprintf("P9.11 must fail closed on malformed transcript artifact: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "artifact is malformed")).To(gomega.BeTrue(), fmt.Sprintf("P9.11 malformed-artifact blocker not exposed: %#v", gate))

	})
	ginkgo.It("p911 product evidence overclaim fails closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		p911MutateTranscriptByClass(root, "healthcare", func(row map[string]any) {
			row["release_claim_impact"] = "HIPAA certification and regulated sales readiness are supported by this demo."
		})

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.11 must fail closed on regulated/commercial overclaim: %#v", gate))
		gomega.Expect(gate.Counts["regulated_or_commercial_overclaims"]).NotTo(gomega.Equal(0), fmt.Sprintf("P9.11 must fail closed on regulated/commercial overclaim: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "unsupported regulated or commercial readiness")).To(gomega.BeTrue(), fmt.Sprintf("P9.11 overclaim blocker not exposed: %#v", gate))

	})
	ginkgo.It("p911 product and release evidence structured status agree", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root, CurrentDir: "current"}, p711TestDemoDependencies)
		productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.11")
		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.11")
		gomega.Expect(productGate.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("product/release P9.11 readiness should agree for complete fixture: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(releaseGate.Status).To(gomega.Equal(productGate.Status), fmt.Sprintf("product/release P9.11 readiness should agree for complete fixture: product=%#v release=%#v", productGate, releaseGate))

		for _, key := range []string{"required_classifications", "covered_classifications", "runnable_command_rows", "expected_output_rows", "blockers"} {
			gomega.Expect(productGate.Counts[key]).To(gomega.Equal(releaseGate.Counts[key]), fmt.Sprintf("product/release P9.11 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate))

		}
		gomega.Expect(strings.Join(productGate.Blockers, "|")).To(gomega.Equal(strings.Join(releaseGate.Blockers, "|")), fmt.Sprintf("product/release P9.11 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(strings.Join(productGate.EvidenceIDs, "|")).To(gomega.Equal(strings.Join(releaseGate.EvidenceIDs, "|")), fmt.Sprintf("product/release P9.11 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))

	})
	ginkgo.It("p910 product evidence needs external competitive baselines", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		path := writeP910CompetitiveBenchmarkReport(root, []map[string]any{p910MeasuredCondition("aider_repo_map")})

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:     root,
			BenchmarkReport: path,
		}, p711TestDemoDependencies)

		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.10")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.10 should fail closed when an external baseline is missing: %#v", gate))
		gomega.Expect(gate.Counts["missing_external_baselines"]).To(gomega.Equal(1), fmt.Sprintf("P9.10 should fail closed when an external baseline is missing: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "missing external competitive baseline")).To(gomega.BeTrue(), fmt.Sprintf("P9.10 missing-baseline blocker not exposed: %#v", gate))

	})
	ginkgo.It("p910 product evidence rejects self referential only evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		path := writeP910CompetitiveBenchmarkReport(root, []map[string]any{
			p910MeasuredCondition("raw_search_read"),
			p910MeasuredCondition("context_pack"),
		})

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:     root,
			BenchmarkReport: path,
		}, p711TestDemoDependencies)

		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.10")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.10 should reject self-referential-only benchmark evidence: %#v", gate))
		gomega.Expect(gate.Counts["covered_external_baselines"]).To(gomega.Equal(0), fmt.Sprintf("P9.10 should reject self-referential-only benchmark evidence: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "self-referential only")).To(gomega.BeTrue(), fmt.Sprintf("P9.10 self-referential blocker not exposed: %#v", gate))

	})
	ginkgo.It("p910 product and release json include zero count keys for default corpus", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP910DefaultCorpusFixture(root)

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root}, p711TestDemoDependencies)

		productBody, err := json.Marshal(product)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal product evidence: %v", err))

		releaseBody, err := json.Marshal(release)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal release evidence: %v", err))

		productCounts := p910JSONCountsFromProduct(productBody)
		releaseCounts := p910JSONCountsFromReleaseReadiness(releaseBody)
		releaseCompetitiveCounts := p910JSONCountsFromReleaseCompetitiveCoverage(releaseBody)

		for _, counts := range []map[string]int{productCounts, releaseCounts, releaseCompetitiveCounts} {
			assertP910RequiredCountKeys(counts)

			for _, key := range []string{
				"covered_external_baselines",
				"measured_external_baselines",
				"documented_infeasible_external_baselines",
				"missing_external_baselines",
				"malformed_baseline_rows",
				"self_referential_baselines",
				"target_client_coverage_baselines",
			} {
				gomega.Expect(counts[key]).To(gomega.Equal(0), fmt.Sprintf("default corpus P9.10 count %q should be explicit zero, got counts=%#v", key, counts))

			}
		}
		gomega.Expect(reflect.DeepEqual(productCounts, releaseCounts)).To(gomega.BeTrue(), fmt.Sprintf("P9.10 product/release JSON counts diverged: product=%#v release=%#v competitive=%#v", productCounts, releaseCounts, releaseCompetitiveCounts))
		gomega.Expect(reflect.DeepEqual(productCounts, releaseCompetitiveCounts)).To(gomega.BeTrue(), fmt.Sprintf("P9.10 product/release JSON counts diverged: product=%#v release=%#v competitive=%#v", productCounts, releaseCounts, releaseCompetitiveCounts))

	})
	ginkgo.It("p910 product evidence rejects malformed baseline row", func() {
		assertP910ConditionNeedsEvidence(
			p910MeasuredCondition("unsupported_tool"),
			"malformed or unsupported competitive baseline row",
		)
	})
	ginkgo.It("p910 product evidence rejects unsupported client classification", func() {
		assertP910ConditionNeedsEvidence(
			p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
				"invalid_client_classifications": 1,
			}),
			"unsupported client classifications",
		)
	})
	ginkgo.It("p910 product evidence rejects missing command id", func() {
		assertP910ConditionNeedsEvidence(
			p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
				"commanded_tasks": 0,
			}),
			"missing command ID evidence",
		)
	})
	ginkgo.It("p910 product evidence rejects missing artifact evidence", func() {
		assertP910ConditionNeedsEvidence(
			p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
				"artifacted_tasks": 0,
			}),
			"missing artifact evidence",
		)
	})
	ginkgo.It("p910 product evidence rejects missing verification method", func() {
		assertP910ConditionNeedsEvidence(
			p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
				"verified_tasks": 0,
			}),
			"missing verification method evidence",
		)
	})
	ginkgo.It("p910 product evidence rejects no go status", func() {
		assertP910ConditionNeedsEvidence(
			p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
				"status": "no_go",
			}),
			"stale, no-go, incomplete, or unsupported status",
		)
	})
	ginkgo.It("p910 product evidence rejects documented infeasibility missing claim impact", func() {
		assertP910ConditionNeedsEvidence(
			p910ConditionPatch(p910InfeasibleCondition("aider_repo_map"), map[string]any{
				"claim_impacts": map[string]int{},
			}),
			"claim impact missing",
		)
	})
	ginkgo.It("p910 product and release evidence structured status agree", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
			BenchmarkReport:   benchmarkPath,
		}, p711TestDemoDependencies)

		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
			BenchmarkReport:   benchmarkPath,
		}, p711TestDemoDependencies)

		productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.10")
		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.10")
		gomega.Expect(productGate.Status).To(gomega.Equal(releaseGate.Status), fmt.Sprintf("P9.10 product/release structured fields diverged: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(productGate.Counts["covered_external_baselines"]).To(gomega.Equal(releaseGate.Counts["covered_external_baselines"]), fmt.Sprintf("P9.10 product/release structured fields diverged: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(productGate.Blockers).To(gomega.HaveLen(len(releaseGate.Blockers)), fmt.Sprintf("P9.10 product/release structured fields diverged: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(release.BenchmarkLocalOps.CompetitiveCoverage.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("release benchmark section should carry P9.10 coverage: %#v", release.BenchmarkLocalOps.CompetitiveCoverage))

	})
	ginkgo.It("run product evidence export writes parseable json output", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		outputPath := filepath.Join(root, ".idiom", "product-evidence", "export.json")

		var stdout, stderr bytes.Buffer

		code := RunProductEvidenceMainWithTelemetry([]string{
			"export",
			"--project-root", root,
			"--local-ops-report", localOpsPath,
			"--validation-records", recordsPath,
			"--output", outputPath,
		}, &stdout, &stderr, nil, p711TestDemoDependencies)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("product evidence export failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		data, err := os.ReadFile(outputPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read product evidence output: %v", err))

		var report productEvidenceExportReport
		gomega.Expect(json.Unmarshal(data, &report)).To(gomega.Succeed())
		gomega.Expect(report.ExportID).NotTo(gomega.BeEmpty(), fmt.Sprintf("output missing export identity/gate linkage: %#v", report))
		gomega.Expect(report.ProductSuccessGates.LocalProductEvidenceExport).To(gomega.Equal("pass"), fmt.Sprintf("output missing export identity/gate linkage: %#v", report))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(outputPath), fmt.Sprintf("CLI output should not echo raw output path stdout=%s stderr=%s", stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring(outputPath), fmt.Sprintf("CLI output should not echo raw output path stdout=%s stderr=%s", stdout.String(), stderr.String()))

	})
	ginkgo.It("p719 absolute path scanner detects embedded separators", func() {
		for _, value := range []string{
			"repo=/home/example/Documents/GitHub/acme",
			"path:/tmp/idiom-private",
			"/private/var/tmp/idiom-private",
			"/var/folders/zz/idiom-private",
		} {
			gomega.Expect(localops.ContainsAbsolutePath(value)).To(gomega.BeTrue(), fmt.Sprintf("expected embedded absolute path to be detected in %q", value))

		}
		gomega.Expect(localops.ContainsAbsolutePath("docs=https://example.com/idiom/path")).To(gomega.BeFalse(), fmt.Sprintf("http URLs should not be treated as local absolute paths"))

	})
	ginkgo.It("p718 retained use counts only qualifying records", func() {
		records := []p711ValidationRecord{
			{
				RecordID:             "record-good",
				ClientClassification: "enterprise-saas",
				EvidenceStatus:       "complete",
				RetainedUseStatus:    "observed",
				AdoptionSignalType:   "internal_sponsor",
				RedactionStatus:      "redacted",
				RetainedUseEvidence:  "saved_packet_reused",
				SubstrateValueStatus: "verification_review_reused",
			},
			{
				RecordID:             "record-disqualified",
				ClientClassification: "open-source",
				EvidenceStatus:       "disqualified",
				RetainedUseStatus:    "observed",
				AdoptionSignalType:   "paid_pilot",
				RedactionStatus:      "redacted",
				RetainedUseEvidence:  "saved_packet_reused",
				SubstrateValueStatus: "verification_review_reused",
			},
			{
				RecordID:             "record-missing-class",
				EvidenceStatus:       "complete",
				RetainedUseStatus:    "observed",
				AdoptionSignalType:   "internal_sponsor",
				RedactionStatus:      "redacted",
				RetainedUseEvidence:  "saved_packet_reused",
				SubstrateValueStatus: "verification_review_reused",
			},
		}
		retained := p711RetainedUseSignal(records)
		gomega.Expect(retained.Status).To(gomega.Equal("pass"), fmt.Sprintf("retained-use signal should count only qualifying records: %#v", retained))
		gomega.Expect(retained.Counts["retained_use_observed"]).To(gomega.Equal(1), fmt.Sprintf("retained-use signal should count only qualifying records: %#v", retained))
		gomega.Expect(retained.Counts["adoption_signals"]).To(gomega.Equal(1), fmt.Sprintf("retained-use signal should count only qualifying records: %#v", retained))
		gomega.Expect(retained.Counts["qualifying_records"]).To(gomega.Equal(1), fmt.Sprintf("unexpected qualifying counts: %#v", retained.Counts))
		gomega.Expect(retained.Counts["non_qualifying_records"]).To(gomega.Equal(2), fmt.Sprintf("unexpected qualifying counts: %#v", retained.Counts))
		gomega.Expect(strings.Join(retained.EvidenceIDs, ",")).To(gomega.Equal("record-good"), fmt.Sprintf("non-qualifying record IDs should not be exported as evidence: %#v", retained.EvidenceIDs))

		rows := buildP711ClassCoverage(p711DemoTranscriptSet{}, p711SecurityPosture{}, p711IntegrationProofSet{}, records)
		for _, row := range rows {
			switch row.Classification {
			case "enterprise-saas":
				if row.ValidationRecordCount != 1 {
					ginkgo.Fail(fmt.Sprintf("qualifying class should be counted: %#v", row))
				}
			case "open-source":
				if row.ValidationRecordCount != 0 {
					ginkgo.Fail(fmt.Sprintf("disqualified class should not be counted: %#v", row))
				}
			}
		}
	})
	ginkgo.It("p97 product evidence missing validation records needs evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot: root,
		}, p711TestDemoDependencies)

		linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.7")
		gomega.Expect(linkage.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("missing validation records must keep P9.7 needs_evidence: %#v", linkage))
		gomega.Expect(linkage.Evidence).To(gomega.ContainSubstring("complete:0"), fmt.Sprintf("P9.7 linkage should expose missing complete-record count: %#v", linkage))
		gomega.Expect(linkage.Counts["complete_records"]).To(gomega.Equal(0), fmt.Sprintf("P9.7 linkage should expose structured missing-evidence counts and blockers: %#v", linkage))
		gomega.Expect(linkage.Blockers).NotTo(gomega.BeEmpty(), fmt.Sprintf("P9.7 linkage should expose structured missing-evidence counts and blockers: %#v", linkage))

	})
	ginkgo.It("p97 product evidence retained use without credible adoption does not pass", func() {
		records := p97PrimaryRecords("none", "none")
		gate := p97ValidationGate(records)
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("retained use without credible paid/internal-platform signal must not satisfy P9.7: %#v", gate))
		gomega.Expect(gate.Counts["retained_use_records"]).NotTo(gomega.Equal(0), fmt.Sprintf("fixture should isolate missing adoption signal: %#v", gate))
		gomega.Expect(gate.Counts["credible_primary_adoption_signals"]).To(gomega.Equal(0), fmt.Sprintf("fixture should isolate missing adoption signal: %#v", gate))

	})
	ginkgo.It("p97 product evidence open source records do not count as primary paid demand", func() {
		var records []p711ValidationRecord

		for i := 0; i < 5; i++ {
			record := p97Record("open-source", fmt.Sprintf("oss-%d", i), "context-oss")

			record.AdoptionSignalType = "paid_pilot"
			record.BuyerBudgetSignal = "budget_owner"
			record.AdoptPaySponsorScore = 3
			records = append(records, record)
		}

		gate := p97ValidationGate(records)
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("open-source records must not replace enterprise-saas paid demand: %#v", gate))
		gomega.Expect(gate.Counts["primary_icp_records"]).To(gomega.Equal(0), fmt.Sprintf("open-source records must not replace enterprise-saas paid demand: %#v", gate))
		gomega.Expect(strings.Join(gate.Blockers, "|")).To(gomega.ContainSubstring("open-source/supporting records"), fmt.Sprintf("expected open-source demand blocker: %#v", gate.Blockers))

	})
	ginkgo.It("p97 product evidence unsafe disqualified withdrawn records do not count", func() {
		records := p97PrimaryRecords("paid_pilot", "budget_owner")

		records[0].EvidenceStatus = "disqualified"
		records[1].EvidenceStatus = "withdrawn"
		records[2].RedactionStatus = "fail"

		gate := p97ValidationGate(records)
		gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("unsafe/disqualified/withdrawn records must fail closed: %#v", gate))
		gomega.Expect(gate.Counts["non_qualifying_records"]).To(gomega.Equal(3), fmt.Sprintf("non-qualifying records should not count toward primary ICP: %#v", gate.Counts))
		gomega.Expect(gate.Counts["primary_icp_records"]).To(gomega.Equal(0), fmt.Sprintf("non-qualifying records should not count toward primary ICP: %#v", gate.Counts))

	})
	ginkgo.Describe("p97 product evidence retained use requires substrate value", func() {
		for _, value := range []string{"demo_liked", "chat_ui_interest", "failed"} {
			ginkgo.It(value+"_evidence", func() {
				records := p97PrimaryRecords("paid_pilot", "budget_owner")

				records[0].RetainedUseEvidence = value
				records[1].RetainedUseEvidence = value
				records[2].RetainedUseEvidence = value
				records[3].RetainedUseEvidence = value
				records[4].RetainedUseEvidence = value

				gate := p97ValidationGate(records)
				gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("non-substrate retained-use evidence %q must not satisfy P9.7: %#v", value, gate))
				gomega.Expect(gate.Counts["retained_use_records"]).To(gomega.Equal(0), fmt.Sprintf("non-substrate retained-use evidence %q must not satisfy P9.7: %#v", value, gate))

			})
			ginkgo.It(value+"_status", func() {
				records := p97PrimaryRecords("paid_pilot", "budget_owner")

				records[0].SubstrateValueStatus = value
				records[1].SubstrateValueStatus = value
				records[2].SubstrateValueStatus = value
				records[3].SubstrateValueStatus = value
				records[4].SubstrateValueStatus = value

				gate := p97ValidationGate(records)
				gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("non-substrate value status %q must not satisfy P9.7: %#v", value, gate))
				gomega.Expect(gate.Counts["retained_use_records"]).To(gomega.Equal(0), fmt.Sprintf("non-substrate value status %q must not satisfy P9.7: %#v", value, gate))

			})
		}
	})
	ginkgo.It("p97 product evidence sufficient primary icp records can pass", func() {
		records := p97PrimaryRecords("paid_pilot", "budget_owner")
		gate := p97ValidationGate(records)
		gomega.Expect(gate.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("sufficient primary ICP retained-use adoption evidence should satisfy P9.7: %#v", gate))
		gomega.Expect(gate.Counts["complete_records"]).To(gomega.Equal(5), fmt.Sprintf("unexpected P9.7 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["primary_icp_records"]).To(gomega.Equal(3), fmt.Sprintf("unexpected P9.7 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["primary_contexts"]).To(gomega.Equal(2), fmt.Sprintf("unexpected P9.7 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["retained_use_records"]).NotTo(gomega.Equal(0), fmt.Sprintf("unexpected P9.7 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["credible_primary_adoption_signals"]).NotTo(gomega.Equal(0), fmt.Sprintf("unexpected P9.7 counts: %#v", gate.Counts))

	})
	ginkgo.It("p912 product evidence current valid thesis status and counts", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
			BenchmarkReport:   benchmarkPath,
		}, p711TestDemoDependencies)

		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.12")
		gomega.Expect(gate.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("complete P0.11 thesis plus prior gate evidence should satisfy P9.12: %#v", gate))

		for _, key := range []string{
			"required_fields",
			"required_fields_present",
			"required_fields_with_evidence_ids",
			"classification_stances_required",
			"classification_stances_covered",
			"classification_stances_with_evidence",
			"packaging_support_complete",
			"pricing_signal_quality_hypothesis",
			"unsupported_admin_request_count",
			"decision_evidence_ids",
			"decision_continue",
			"prior_gate_evidence_present",
			"blockers",
		} {
			if _, ok := gate.Counts[key]; !ok {
				ginkgo.Fail(fmt.Sprintf("P9.12 count %q missing: %#v", key, gate.Counts))
			}
		}
		gomega.Expect(gate.Counts["required_fields_present"]).To(gomega.Equal(gate.Counts["required_fields"]), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["classification_stances_covered"]).To(gomega.Equal(7), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["classification_stances_with_evidence"]).To(gomega.Equal(7), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["unsupported_admin_request_count"]).To(gomega.Equal(7), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["privacy_blockers"]).To(gomega.Equal(0), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["admin_count_blockers"]).To(gomega.Equal(0), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["decision_continue"]).To(gomega.Equal(1), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["prior_gate_evidence_present"]).To(gomega.Equal(5), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))
		gomega.Expect(gate.Counts["blockers"]).To(gomega.Equal(0), fmt.Sprintf("unexpected P9.12 counts: %#v", gate.Counts))

		for _, id := range []string{"p0.11-packaging-support-thesis", "p0.11-icp", "p7.11-product-evidence-export", "p0.11-class-enterprise-saas"} {
			gomega.Expect(stringSliceContains(gate.EvidenceIDs, id)).To(gomega.BeTrue(), fmt.Sprintf("P9.12 evidence IDs missing %q: %#v", id, gate.EvidenceIDs))

		}
	})
	ginkgo.It("p912 product evidence missing required field fails closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		p912MutateThesis(root, func(thesis map[string]any) {
			delete(thesis, "pricing")
		})

		gate := p912ProductGateForCompleteEvidence(root)
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("missing P9.12 required field must fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "required packaging/support field missing or empty: pricing")).To(gomega.BeTrue(), fmt.Sprintf("missing P9.12 required field must fail closed: %#v", gate))

	})
	ginkgo.It("p912 product evidence blank structured field fails closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		p912MutateThesis(root, func(thesis map[string]any) {
			support := thesis["support"].(map[string]any)

			support["boundary"] = " "
		})

		gate := p912ProductGateForCompleteEvidence(root)
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("blank P9.12 structured field must fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "required packaging/support field missing or empty: support")).To(gomega.BeTrue(), fmt.Sprintf("blank P9.12 structured field must fail closed: %#v", gate))

	})
	ginkgo.It("p912 product evidence unsupported admin count mismatch fails closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		p912MutateThesis(root, func(thesis map[string]any) {
			admin := thesis["admin"].(map[string]any)

			admin["unsupported_request_count"] = 99
		})

		gate := p912ProductGateForCompleteEvidence(root)
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.12 unsupported-admin mismatch must fail closed with list-derived count: %#v", gate))
		gomega.Expect(gate.Counts["unsupported_admin_request_count"]).To(gomega.Equal(7), fmt.Sprintf("P9.12 unsupported-admin mismatch must fail closed with list-derived count: %#v", gate))
		gomega.Expect(gate.Counts["admin_count_blockers"]).To(gomega.Equal(1), fmt.Sprintf("P9.12 unsupported-admin mismatch must fail closed with list-derived count: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "unsupported_admin_request_count mismatch: thesis=99 actual=7")).To(gomega.BeTrue(), fmt.Sprintf("P9.12 unsupported-admin mismatch must fail closed with list-derived count: %#v", gate))

	})
	ginkgo.It("p912 product evidence requires all seven classification stances", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		p912MutateThesis(root, func(thesis map[string]any) {
			rows := thesis["classification_stances"].([]any)

			thesis["classification_stances"] = rows[:len(rows)-1]
		})

		gate := p912ProductGateForCompleteEvidence(root)
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.12 must require all seven classification stances: %#v", gate))
		gomega.Expect(gate.Counts["classification_stances_covered"]).To(gomega.Equal(6), fmt.Sprintf("P9.12 must require all seven classification stances: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "missing packaging classification stance:")).To(gomega.BeTrue(), fmt.Sprintf("P9.12 must require all seven classification stances: %#v", gate))

	})
	ginkgo.It("p912 product and release evidence structured status agree", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
			BenchmarkReport:   benchmarkPath,
		}, p711TestDemoDependencies)

		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
			BenchmarkReport:   benchmarkPath,
		}, p711TestDemoDependencies)

		productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.12")
		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.12")
		gomega.Expect(productGate.Status).To(gomega.Equal(releaseGate.Status), fmt.Sprintf("product/release P9.12 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(productGate.ClaimImpact).To(gomega.Equal(releaseGate.ClaimImpact), fmt.Sprintf("product/release P9.12 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(reflect.DeepEqual(productGate.Counts, releaseGate.Counts)).To(gomega.BeTrue(), fmt.Sprintf("product/release P9.12 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(reflect.DeepEqual(productGate.Blockers, releaseGate.Blockers)).To(gomega.BeTrue(), fmt.Sprintf("product/release P9.12 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(reflect.DeepEqual(productGate.EvidenceIDs, releaseGate.EvidenceIDs)).To(gomega.BeTrue(), fmt.Sprintf("product/release P9.12 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate))

	})
	ginkgo.It("p718 top level status reflects missing signals", func() {
		root := ginkgo.GinkgoT().TempDir()
		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gomega.Expect(report.Status).To(gomega.Equal("partial"), fmt.Sprintf("missing evidence should produce partial top-level status, got status=%q completeness=%#v", report.Status, report.SignalCompleteness))
		gomega.Expect(report.SignalCompleteness.Status).To(gomega.Equal("partial"), fmt.Sprintf("missing evidence should produce partial top-level status, got status=%q completeness=%#v", report.Status, report.SignalCompleteness))

	})
	ginkgo.It("p99 product evidence needs evidence when posture missing or malformed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
		gomega.Expect(os.Remove(matrixPath)).To(gomega.Succeed())

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("missing posture matrix should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "missing posture matrix")).To(gomega.BeTrue(), fmt.Sprintf("missing posture matrix should fail closed: %#v", gate))

		writeP711Text(matrixPath, "{not-json")

		report = buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate = p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("malformed posture matrix should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "malformed posture matrix JSON")).To(gomega.BeTrue(), fmt.Sprintf("malformed posture matrix should fail closed: %#v", gate))

	})
	ginkgo.It("p99 product evidence rejects compliance overclaim and regulated sales readiness", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		writeP711Text(filepath.Join(root, "docs", "security-privacy-posture.md"), "Reviewer Command\nIdiom is HIPAA compliant.\n")

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("compliance overclaim should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "overclaim")).To(gomega.BeTrue(), fmt.Sprintf("compliance overclaim should fail closed: %#v", gate))

		stageP711ProductEvidenceFixtures(root)

		matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")

		var matrix map[string]any

		data, err := os.ReadFile(matrixPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read matrix: %v", err))
		gomega.Expect(json.Unmarshal(data, &matrix)).To(gomega.Succeed())

		rows := matrix["client_classification_coverage"].([]any)
		row := rows[0].(map[string]any)

		row["classification"] = "finance"
		row["stance"] = "regulated sales ready"
		row["limitation"] = "ready for finance sales"

		writeP711JSON(matrixPath, matrix)

		report = buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate = p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("regulated sales-readiness row should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "regulated client row")).To(gomega.BeTrue(), fmt.Sprintf("regulated sales-readiness row should fail closed: %#v", gate))

	})
	ginkgo.Describe("p99 product evidence assertion-aware overclaim handling", func() {
		for _, phrase := range []string{
			"Idiom provides HIPAA attestation.",
			"Idiom has HIPAA attestation, release remains no-go.",
			"Idiom has NIST conformance.",
			"Idiom has NIST certification.",
			"HIPAA certification.",
			"Idiom has MCP conformance.",
			"MCP certification.",
			"Idiom has formal approval.",
			"Idiom has formal approval, readiness remains no-go.",
			"Idiom has regulated sales readiness.",
		} {
			ginkgo.It(p711SafeID(phrase, "phrase"), func() {
				root := ginkgo.GinkgoT().TempDir()
				stageP711ProductEvidenceFixtures(root)
				writeP711Text(filepath.Join(root, "docs", "security-privacy-posture.md"), phrase)

				report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
				gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
				gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("positive overclaim %q should fail closed: %#v", phrase, gate))
				gomega.Expect(p711TestBlockerContains(gate.Blockers, "overclaim")).To(gomega.BeTrue(), fmt.Sprintf("positive overclaim %q should fail closed: %#v", phrase, gate))

			})
		}

		ginkgo.It("allows phrase-local negated limitations", func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			writeP711Text(filepath.Join(root, "docs", "security-privacy-posture.md"), strings.Join([]string{
				"Reviewer Command",
				"No HIPAA compliance claim.",
				"HIPAA attestation remains no-go until reviewed.",
				"Does not prove MCP conformance.",
				"Does not prove regulated sales readiness, formal approval, or MCP conformance.",
				"Do not claim FINRA compliance or regulated sales readiness before review.",
				"No release material asserts regulated readiness or formal approval beyond evidence.",
				"No NIST certification claim.",
				"MCP conformance remains no-go until reviewed.",
				"NIST certification remains no-go until reviewed.",
				"This is not a NIST attestation.",
				"No formal approval is claimed.",
				"No regulated sales readiness claim.",
			}, "\n"))

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
			gomega.Expect(p711TestBlockerContains(gate.Blockers, "overclaim")).To(gomega.BeFalse(), fmt.Sprintf("negated limitations should not be treated as overclaims: %#v", gate))
		})

	})
	ginkgo.It("p99 product evidence privacy blocked export needs evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		path := filepath.Join(root, "validation-records.json")
		writeP711JSON(path, []map[string]any{{
			"record_id":             "record-enterprise-saas",
			"client_classification": "enterprise-saas",
			"evidence_status":       "complete",
			"retained_use_status":   "observed",
			"adoption_signal_type":  "internal_sponsor",
			"redaction_status":      "redacted",
			"raw_prompt":            "RAW_PROMPT_BODY",
		}})

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{
			ProjectRoot:       root,
			ValidationRecords: path,
		}, p711TestDemoDependencies)

		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(report.Status).To(gomega.Equal("blocked"), fmt.Sprintf("fixture should block product export privacy: status=%q privacy=%#v", report.Status, report.Privacy))
		gomega.Expect(report.Privacy.ForbiddenFieldCheck).To(gomega.Equal("failed:validation_records"), fmt.Sprintf("fixture should block product export privacy: status=%q privacy=%#v", report.Status, report.Privacy))
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("privacy-blocked product export should block P9.9 with structured blockers: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "product evidence export status is privacy-blocked")).To(gomega.BeTrue(), fmt.Sprintf("privacy-blocked product export should block P9.9 with structured blockers: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "validation records failed privacy validation")).To(gomega.BeTrue(), fmt.Sprintf("privacy-blocked product export should block P9.9 with structured blockers: %#v", gate))

		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{}, releaseEvidenceOptions{
			ProjectRoot:       root,
			ValidationRecords: path,
		}, p711TestDemoDependencies)

		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.9")
		gomega.Expect(releaseGate.Status).To(gomega.Equal(gate.Status), fmt.Sprintf("privacy-blocked product/release P9.9 should remain aligned: product=%#v release=%#v", gate, releaseGate))
		gomega.Expect(releaseGate.Counts["product_privacy_blockers"]).To(gomega.Equal(gate.Counts["product_privacy_blockers"]), fmt.Sprintf("privacy-blocked product/release P9.9 should remain aligned: product=%#v release=%#v", gate, releaseGate))
		gomega.Expect(p711TestBlockerContains(releaseGate.Blockers, "product evidence export status is privacy-blocked")).To(gomega.BeTrue(), fmt.Sprintf("privacy-blocked product/release P9.9 should remain aligned: product=%#v release=%#v", gate, releaseGate))

	})
	ginkgo.It("p99 product evidence contradictory report markers fail closed", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		writeP711Text(filepath.Join(root, "docs", "release.md"), p99ReleaseFixtureText("Decision: no-go\nReviewer signoff: GO"))

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("contradictory report markers should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "conflicting GO/no-go markers")).To(gomega.BeTrue(), fmt.Sprintf("contradictory report markers should fail closed: %#v", gate))

	})
	ginkgo.It("p99 product evidence requires structured source verification method", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)
		writeP711Text(filepath.Join(root, "docs", "security-privacy-posture.md"), "Reviewer Command\n\nSource anchors checked on 2026-05-14 UTC.\n\nProduct threat model:\nP7.9 P8.4 P9.1 P9.3 P9.9\n\nData-flow map:\n\nPrivacy and retention posture:\n\nControl mapping evidence:\n\nDownstream go/no-go gates:\n")

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("missing structured source verification should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "structured source verification evidence")).To(gomega.BeTrue(), fmt.Sprintf("missing structured source verification should fail closed: %#v", gate))

		writeP711Text(filepath.Join(root, "docs", "security-privacy-posture.md"), "Reviewer Command\n\n"+p99PlanFixtureText(false))

		report = buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate = p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("unreachable source without method should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "unreachable source evidence missing verification method")).To(gomega.BeTrue(), fmt.Sprintf("unreachable source without method should fail closed: %#v", gate))

	})
	ginkgo.It("p99 product evidence needs evidence when control evidence or tiebacks missing", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")

		var matrix map[string]any

		data, err := os.ReadFile(matrixPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read matrix: %v", err))
		gomega.Expect(json.Unmarshal(data, &matrix)).To(gomega.Succeed())

		controls := matrix["required_controls"].([]any)
		first := controls[0].(map[string]any)

		first["evidence"] = []any{}
		matrix["release_gate_tiebacks"] = []string{"P7.9"}
		writeP711JSON(matrixPath, matrix)

		report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
		gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
		gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("missing control evidence/tiebacks should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "missing executable evidence")).To(gomega.BeTrue(), fmt.Sprintf("missing control evidence/tiebacks should fail closed: %#v", gate))
		gomega.Expect(p711TestBlockerContains(gate.Blockers, "missing release-gate tieback: P8.4")).To(gomega.BeTrue(), fmt.Sprintf("missing control evidence/tiebacks should fail closed: %#v", gate))

	})
	ginkgo.It("p99 product and release evidence structured status agree", func() {
		root := ginkgo.GinkgoT().TempDir()
		stageP711ProductEvidenceFixtures(root)

		localOpsPath := writeP711LocalOpsReport(root)
		recordsPath := writeP711ValidationRecords(root)
		stageReleaseEvidenceCommandBundle(root, "baseline", releaseEvidenceBaselineCommands())
		stageReleaseEvidenceCommandBundle(root, "current", releaseEvidenceCurrentCommands())

		product := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
			ProjectRoot:       root,
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)

		release := buildReleaseEvidenceBundleWithDeps(root, lanehistory.Summary{Total: 1}, releaseEvidenceOptions{
			ProjectRoot:       root,
			BaselineDir:       "baseline",
			CurrentDir:        "current",
			LocalOpsReport:    localOpsPath,
			ValidationRecords: recordsPath,
		}, p711TestDemoDependencies)

		productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.9")
		releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.9")
		gomega.Expect(productGate.Status).To(gomega.Equal(releaseGate.Status), fmt.Sprintf("product/release P9.9 status should agree: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(productGate.Status).To(gomega.Equal("evidence_present"), fmt.Sprintf("product/release P9.9 status should agree: product=%#v release=%#v", productGate, releaseGate))

		for _, key := range []string{"mapped_required_controls", "mapped_client_rows", "mapped_gate_tiebacks", "blockers"} {
			gomega.Expect(productGate.Counts[key]).To(gomega.Equal(releaseGate.Counts[key]), fmt.Sprintf("product/release P9.9 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate))

		}
		gomega.Expect(productGate.Blockers).To(gomega.BeEmpty(), fmt.Sprintf("P9.9 structured fields incomplete: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(releaseGate.Blockers).To(gomega.BeEmpty(), fmt.Sprintf("P9.9 structured fields incomplete: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(stringSliceContains(productGate.EvidenceIDs, "p7.9-security-privacy-posture")).To(gomega.BeTrue(), fmt.Sprintf("P9.9 structured fields incomplete: product=%#v release=%#v", productGate, releaseGate))
		gomega.Expect(stringSliceContains(releaseGate.EvidenceIDs, "release-checklist")).To(gomega.BeTrue(), fmt.Sprintf("P9.9 structured fields incomplete: product=%#v release=%#v", productGate, releaseGate))

	})
})
var _ = ginkgo.Describe("p911 product evidence required transcript fields fail closed", func() {
	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantBlocker string
	}{
		{name: "command", mutate: func(row map[string]any) { row["commands"] = []map[string]any{} }, wantBlocker: "missing runnable command"},
		{name: "expected output", mutate: func(row map[string]any) {
			row["commands"].([]any)[0].(map[string]any)["expected_output_shape"] = map[string]any{}
		}, wantBlocker: "missing expected output"},
		{name: "evidence fields", mutate: func(row map[string]any) { row["evidence_fields"] = []string{} }, wantBlocker: "missing evidence fields"},
		{name: "blank evidence field", mutate: func(row map[string]any) {
			row["evidence_fields"] = []string{"Tool", " "}
		}, wantBlocker: "blank client demo required list item"},
		{name: "blank expected output field", mutate: func(row map[string]any) {
			shape := row["commands"].([]any)[0].(map[string]any)["expected_output_shape"].(map[string]any)

			shape["stable_fields"] = []string{"Tool", ""}
		}, wantBlocker: "blank client demo required list item"},
		{name: "MVP limitation", mutate: func(row map[string]any) { row["mvp_limitation"] = "" }, wantBlocker: "missing MVP limitation"},
		{name: "final state", mutate: func(row map[string]any) { row["final_state_expansion"] = "" }, wantBlocker: "missing final-state expansion"},
		{name: "privacy posture", mutate: func(row map[string]any) { row["privacy_posture"] = "" }, wantBlocker: "missing privacy/no-forbidden-field posture"},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			p911MutateFirstTranscript(root, tc.mutate)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.11 must fail closed for %s: %#v", tc.name, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, tc.wantBlocker)).To(gomega.BeTrue(), fmt.Sprintf("P9.11 blocker %q not exposed: %#v", tc.wantBlocker, gate))

		})
	}
})
var _ = ginkgo.Describe("p911 product evidence rejects framework specific regulated readiness overclaims", func() {

	cases := []struct {
		name   string
		class  string
		phrase string
	}{
		{name: "fedramp_ready", class: "public-sector", phrase: "FedRAMP ready"},
		{name: "fedramp_readiness", class: "public-sector", phrase: "FedRAMP readiness"},
		{name: "fedramp_approved", class: "public-sector", phrase: "FedRAMP approved"},
		{name: "fedramp_approval", class: "public-sector", phrase: "FedRAMP approval"},
		{name: "fedramp_authorization", class: "public-sector", phrase: "FedRAMP authorization"},
		{name: "hipaa_ready", class: "healthcare", phrase: "HIPAA ready"},
		{name: "hipaa_readiness", class: "healthcare", phrase: "HIPAA readiness"},
		{name: "hipaa_approved", class: "healthcare", phrase: "HIPAA approved"},
		{name: "hipaa_approval", class: "healthcare", phrase: "HIPAA approval"},
		{name: "finra_ready", class: "finance", phrase: "FINRA ready"},
		{name: "finra_readiness", class: "finance", phrase: "FINRA readiness"},
		{name: "nist_ready", class: "public-sector", phrase: "NIST ready"},
		{name: "nist_readiness", class: "public-sector", phrase: "NIST readiness"},
		{name: "nist_approved", class: "public-sector", phrase: "NIST approved"},
		{name: "nist_approval", class: "public-sector", phrase: "NIST approval"},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			p911MutateTranscriptByClass(root, tc.class, func(row map[string]any) {
				row["release_claim_impact"] = "This demo is " + tc.phrase + " for regulated review."
			})

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.11 must fail closed for %q: %#v", tc.phrase, gate))
			gomega.Expect(gate.Counts["regulated_or_commercial_overclaims"]).NotTo(gomega.Equal(0), fmt.Sprintf("P9.11 must fail closed for %q: %#v", tc.phrase, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, "unsupported regulated or commercial readiness")).To(gomega.BeTrue(), fmt.Sprintf("P9.11 regulated-readiness blocker should name %q: %#v", tc.phrase, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, strings.ToLower(tc.phrase))).To(gomega.BeTrue(), fmt.Sprintf("P9.11 regulated-readiness blocker should name %q: %#v", tc.phrase, gate))

		})
	}
})
var _ = ginkgo.Describe("p911 product evidence allows phrase local approval negations", func() {

	for _, phrase := range []string{
		"no FedRAMP approval claim",
		"FedRAMP approval remains no-go",
		"no HIPAA approved claim",
		"NIST approval remains no-go until reviewed",
	} {
		ginkgo.It(p711SafeID(phrase, "phrase"), func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			p911MutateTranscriptByClass(root, "public-sector", func(row map[string]any) {
				row["release_claim_impact"] = "Proof-only demo limitation: " + phrase + "."
			})

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			gomega.Expect(gate.Counts["regulated_or_commercial_overclaims"]).To(gomega.Equal(0), fmt.Sprintf("P9.11 should allow phrase-local approval negation %q: %#v", phrase, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, "unsupported regulated or commercial readiness")).To(gomega.BeFalse(), fmt.Sprintf("P9.11 should allow phrase-local approval negation %q: %#v", phrase, gate))

		})
	}
})
var _ = ginkgo.Describe("p911 product evidence requires executable command validation", func() {

	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantBlocker string
		wantCount   string
	}{
		{
			name: "shaped_nonexistent_command",
			mutate: func(row map[string]any) {
				command := row["commands"].([]any)[0].(map[string]any)

				command["command"] = "idiom tool does_not_exist --input '{}'"
				command["argv"] = []string{"tool", "does_not_exist", "--input", "{}"}
			},
			wantBlocker: "command executable validation failed",
			wantCount:   "command_execution_blockers",
		},
		{
			name: "command_argv_mismatch",
			mutate: func(row map[string]any) {
				command := row["commands"].([]any)[0].(map[string]any)

				command["command"] = "idiom tool source_sanity -config $IDIOM_CONFIG --caller-id ci-agent --request-id p710-saas --trace-id trace:p710-saas --input '{\"path\":\"pkg/payments/other.go\"}'"
			},
			wantBlocker: "command/argv parity mismatch",
			wantCount:   "command_execution_blockers",
		},
		{
			name: "unsafe_top_level_subcommand",
			mutate: func(row map[string]any) {
				command := row["commands"].([]any)[0].(map[string]any)

				command["command"] = "idiom backend status"
				command["argv"] = []string{"backend", "status"}
			},
			wantBlocker: "unsafe top-level subcommand",
			wantCount:   "command_execution_blockers",
		},
		{
			name: "default_profile_shell",
			mutate: func(row map[string]any) {
				command := row["commands"].([]any)[0].(map[string]any)

				command["command"] = "idiom tool shell -config $IDIOM_CONFIG --caller-id ci-agent --request-id p710-saas --input '{\"command\":\"printf unsafe > network-side-effect.txt\"}'"
				command["argv"] = []string{"tool", "shell", "-config", "$IDIOM_CONFIG", "--caller-id", "ci-agent", "--request-id", "p710-saas", "--input", "{\"command\":\"printf unsafe > network-side-effect.txt\"}"}
			},
			wantBlocker: "outside discovery-profile denial",
			wantCount:   "command_execution_blockers",
		},
		{
			name: "default_profile_write_file",
			mutate: func(row map[string]any) {
				command := row["commands"].([]any)[0].(map[string]any)

				command["command"] = "idiom tool write_file -config $IDIOM_CONFIG --caller-id ci-agent --request-id p710-saas --input '{\"path\":\"unsafe.txt\",\"content\":\"unsafe\"}'"
				command["argv"] = []string{"tool", "write_file", "-config", "$IDIOM_CONFIG", "--caller-id", "ci-agent", "--request-id", "p710-saas", "--input", "{\"path\":\"unsafe.txt\",\"content\":\"unsafe\"}"}
			},
			wantBlocker: "outside discovery-profile denial",
			wantCount:   "command_execution_blockers",
		},
		{
			name: "unsafe_local_ops_cleanup",
			mutate: func(row map[string]any) {
				command := row["commands"].([]any)[0].(map[string]any)

				command["command"] = "idiom local-ops benchmark --json --config $IDIOM_CONFIG --profile sidecar --quickstart-smoke --quickstart-task 'localize RetryPayment helper' --quickstart-cleanup remove-local-state"
				command["argv"] = []string{"local-ops", "benchmark", "--json", "--config", "$IDIOM_CONFIG", "--profile", "sidecar", "--quickstart-smoke", "--quickstart-task", "localize RetryPayment helper", "--quickstart-cleanup", "remove-local-state"}
			},
			wantBlocker: "unsafe local-ops cleanup",
			wantCount:   "command_execution_blockers",
		},
		{
			name: "mismatched_command_id",
			mutate: func(row map[string]any) {
				command := row["commands"].([]any)[0].(map[string]any)

				command["id"] = "mismatched-demo-command"
			},
			wantBlocker: "command ID parity mismatch",
			wantCount:   "command_id_mismatch_rows",
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			p911MutateTranscriptByClass(root, "enterprise-saas", tc.mutate)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.11 must fail closed for %s: %#v", tc.name, gate))
			gomega.Expect(gate.Counts["runnable_command_rows"]).NotTo(gomega.Equal(7), fmt.Sprintf("P9.11 must fail closed for %s: %#v", tc.name, gate))
			gomega.Expect(gate.Counts[tc.wantCount]).NotTo(gomega.Equal(0), fmt.Sprintf("P9.11 must fail closed for %s: %#v", tc.name, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, tc.wantBlocker)).To(gomega.BeTrue(), fmt.Sprintf("P9.11 blocker %q not exposed: %#v", tc.wantBlocker, gate))

		})
	}
})

func assertP910ConditionNeedsEvidence(condition map[string]any, wantBlocker string) {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	stageP711ProductEvidenceFixtures(root)

	path := writeP910CompetitiveBenchmarkReport(root, []map[string]any{
		condition,
		p910InfeasibleCondition("external_agent_native"),
	})

	report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:     root,
		BenchmarkReport: path,
	}, p711TestDemoDependencies)

	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.10")
	gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.10 should fail closed: %#v", gate))
	gomega.Expect(p711TestBlockerContains(gate.Blockers, wantBlocker)).To(gomega.BeTrue(), fmt.Sprintf("P9.10 blocker %q not exposed: %#v", wantBlocker, gate))

}

var _ = ginkgo.Describe("p810 integration proof requires distinct executable paths", func() {

	for _, tc := range []struct {
		name   string
		proofs []map[string]any
	}{
		{
			name: "missing_shell_ci_path",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
		},
		{
			name: "missing_external_agent_path",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
		},
		{
			name: "missing_mcp_stdio_path",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
			},
		},
		{
			name: "duplicate_and_unrelated_executables_do_not_fill_missing_path",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-shell-ci-duplicate", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-unrelated-workflow", "editor_plugin_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
		},
		{
			name: "mcp_stdio_missing_parity_fields",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofWithoutMCPParityFixture("p7.8-mcp-stdio"),
			},
		},
		{
			name: "mcp_parity_fields_on_wrong_path_do_not_satisfy_mcp",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
				p711IntegrationProofWithMCPParityOnPath("p7.8-external-coding-agent", "external_coding_agent_workflow"),
			},
		},
	} {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			writeP711IntegrationProofs(root, tc.proofs)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			integration := p711TestSignal(report.Signals, "integration_proof")
			gomega.Expect(integration.Status).To(gomega.Equal("partial"), fmt.Sprintf("incomplete integration proof should stay partial: %#v", integration))
			gomega.Expect(integration.Completeness).To(gomega.Equal("partial"), fmt.Sprintf("incomplete integration proof should stay partial: %#v", integration))

			if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.8"); linkage.Status != "needs_evidence" {
				ginkgo.Fail(fmt.Sprintf("P9.8 should need evidence for incomplete integration paths: %#v", report.ReleaseGateLinkage))
			} else if len(linkage.Blockers) == 0 {
				ginkgo.Fail(fmt.Sprintf("P9.8 linkage should expose blockers for incomplete proofs: %#v", linkage))
			}
			gomega.Expect(report.ProductSuccessGates.IntegrationProof).To(gomega.Equal("needs_evidence"), fmt.Sprintf("product integration gate should need evidence, got %q", report.ProductSuccessGates.IntegrationProof))

		})
	}
})
var _ = ginkgo.Describe("p811 integration proof rejects malformed rows", func() {

	for _, tc := range []struct {
		name                       string
		proofs                     []map[string]any
		wantMCPParityPaths         int
		wantShellRequiredPathCount int
	}{
		{
			name: "duplicate_ids_do_not_satisfy_required_paths_or_mcp_parity",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-duplicated-proof", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-duplicated-proof", "mcp_stdio", true, false),
			},
			wantMCPParityPaths: 0,
		},
		{
			name: "missing_id_does_not_satisfy_required_path",
			proofs: []map[string]any{
				p711IntegrationProofFixture("", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
			wantMCPParityPaths: 1,
		},
		{
			name: "blocked_status_does_not_satisfy_required_path",
			proofs: []map[string]any{
				p711IntegrationProofWithStatus("p7.8-shell-ci", "shell_ci_script", "blocked"),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
			wantMCPParityPaths: 1,
		},
		{
			name: "deferred_status_does_not_satisfy_required_path",
			proofs: []map[string]any{
				p711IntegrationProofWithStatus("p7.8-shell-ci", "shell_ci_script", "deferred"),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
			wantMCPParityPaths: 1,
		},
		{
			name: "unknown_status_does_not_satisfy_required_path",
			proofs: []map[string]any{
				p711IntegrationProofWithStatus("p7.8-shell-ci", "shell_ci_script", "unknown"),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
			wantMCPParityPaths: 1,
		},
	} {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			writeP711IntegrationProofs(root, tc.proofs)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			assertP811IntegrationNeedsEvidence(report)

			integration := p711TestSignal(report.Signals, "integration_proof")
			gomega.Expect(integration.Counts["mcp_stdio_parity_paths"]).To(gomega.Equal(tc.wantMCPParityPaths), fmt.Sprintf("unexpected MCP parity count for malformed proof rows: %#v", integration))
			gomega.Expect(integration.Counts["executable_required_path_shell_ci_script"]).To(gomega.Equal(tc.wantShellRequiredPathCount), fmt.Sprintf("malformed shell/CI row must not satisfy required-path count: %#v", integration))

		})
	}
})
var _ = ginkgo.Describe("p812 integration proof requires explicit safe privacy semantics", func() {

	for _, tc := range []struct {
		name                  string
		proofs                []map[string]any
		wantP98Status         string
		wantSignalStatus      string
		wantMCPParityPaths    int
		wantMissingPrivacy    int
		wantIncompletePrivacy int
		wantPrivacyBlockers   int
	}{
		{
			name: "missing_privacy_object_does_not_satisfy_mcp_required_path_or_parity",
			proofs: []map[string]any{
				p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofWithoutPrivacy("p7.8-mcp-stdio", "mcp_stdio"),
			},
			wantP98Status:         "needs_evidence",
			wantSignalStatus:      "partial",
			wantMCPParityPaths:    0,
			wantMissingPrivacy:    1,
			wantIncompletePrivacy: 1,
		},
		{
			name: "omitted_privacy_boolean_does_not_satisfy_required_path",
			proofs: []map[string]any{
				p711IntegrationProofWithOmittedPrivacyField("p7.8-shell-ci", "shell_ci_script", "private_paths"),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
			wantP98Status:         "needs_evidence",
			wantSignalStatus:      "partial",
			wantMCPParityPaths:    1,
			wantIncompletePrivacy: 1,
		},
		{
			name: "unsafe_privacy_value_blocks_integration_evidence",
			proofs: []map[string]any{
				p711IntegrationProofWithPrivacyValue("p7.8-shell-ci", "shell_ci_script", "raw_prompts", true),
				p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
				p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
			},
			wantP98Status:       "blocked",
			wantSignalStatus:    "blocked",
			wantMCPParityPaths:  1,
			wantPrivacyBlockers: 1,
		},
	} {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			writeP711IntegrationProofs(root, tc.proofs)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			integration := p711TestSignal(report.Signals, "integration_proof")
			gomega.Expect(integration.Status).To(gomega.Equal(tc.wantSignalStatus), fmt.Sprintf("unexpected integration signal status: got %q want %q: %#v", integration.Status, tc.wantSignalStatus, integration))

			if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.8"); linkage.Status != tc.wantP98Status {
				ginkgo.Fail(fmt.Sprintf("unexpected P9.8 status: got %q want %q: %#v", linkage.Status, tc.wantP98Status, report.ReleaseGateLinkage))
			}
			gomega.Expect(report.ProductSuccessGates.IntegrationProof).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("privacy-invalid integration proof must not produce evidence_present gate"))
			gomega.Expect(integration.Counts["mcp_stdio_parity_paths"]).To(gomega.Equal(tc.wantMCPParityPaths), fmt.Sprintf("unexpected MCP parity count for privacy-invalid rows: %#v", integration))
			gomega.Expect(integration.Counts["missing_privacy"]).To(gomega.Equal(tc.wantMissingPrivacy), fmt.Sprintf("unexpected missing privacy count: %#v", integration))
			gomega.Expect(integration.Counts["incomplete_privacy"]).To(gomega.Equal(tc.wantIncompletePrivacy), fmt.Sprintf("unexpected incomplete privacy count: %#v", integration))
			gomega.Expect(integration.Counts["privacy_blockers"]).To(gomega.Equal(tc.wantPrivacyBlockers), fmt.Sprintf("unexpected privacy blocker count: %#v", integration))

		})
	}
})
var _ = ginkgo.Describe("p718 validation records reject unknown and forbidden evidence", func() {

	for _, tc := range []struct {
		name    string
		records []map[string]any
	}{
		{
			name: "unsafe_unknown_fields",
			records: []map[string]any{{
				"record_id":             "record-enterprise-saas",
				"client_classification": "enterprise-saas",
				"evidence_status":       "complete",
				"retained_use_status":   "observed",
				"adoption_signal_type":  "internal_sponsor",
				"redaction_status":      "redacted",
				"raw_prompt":            "RAW_PROMPT_BODY",
			}},
		},
		{
			name: "forbidden_values",
			records: []map[string]any{{
				"record_id":             "record-enterprise-saas",
				"client_classification": "enterprise-saas",
				"evidence_status":       "complete",
				"retained_use_status":   "observed",
				"adoption_signal_type":  "internal_sponsor",
				"redaction_status":      "redacted",
				"raw_file_body":         "func LeakedBody() {}",
				"note":                  "/workspace/CustomerX-private-repo secret token=abc123 logs/telemetry/session.jsonl",
			}},
		},
		{
			name: "benign_unknown_fields",
			records: []map[string]any{{
				"record_id":             "record-enterprise-saas",
				"client_classification": "enterprise-saas",
				"evidence_status":       "complete",
				"retained_use_status":   "observed",
				"adoption_signal_type":  "internal_sponsor",
				"redaction_status":      "redacted",
				"reviewer_note":         "approved summary",
			}},
		},
	} {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)

			path := filepath.Join(root, "validation-records.json")
			writeP711JSON(path, tc.records)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{
				ProjectRoot:       root,
				ValidationRecords: path,
			}, p711TestDemoDependencies)
			gomega.Expect(report.Status).To(gomega.Equal("blocked"), fmt.Sprintf("unsafe validation records should block export evidence, got status=%q privacy=%#v", report.Status, report.Privacy))
			gomega.Expect(report.Privacy.ForbiddenFieldCheck).To(gomega.Equal("failed:validation_records"), fmt.Sprintf("unsafe validation records should block export evidence, got status=%q privacy=%#v", report.Status, report.Privacy))
			gomega.Expect(p711TestSourceRef(report.SourceReferences, "p0.8_validation_records").Status).To(gomega.Equal("blocked"), fmt.Sprintf("validation record source should be blocked: %#v", report.SourceReferences))

			if got := p711TestSignal(report.Signals, "retained_use"); got.Counts["validation_records"] != 0 || got.Status == "pass" {
				ginkgo.Fail(fmt.Sprintf("blocked validation records must not be counted: %#v", got))
			}

			body, err := json.Marshal(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal report: %v", err))

			for _, forbidden := range []string{"RAW_PROMPT_BODY", "func LeakedBody", "/Users/", "CustomerX", "token=abc123", "logs/telemetry"} {
				gomega.Expect(string(body)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("blocked export leaked %q: %s", forbidden, string(body)))

			}
		})
	}
})
var _ = ginkgo.Describe("p719 validation records reject embedded absolute paths in allowed fields", func() {

	for _, tc := range []struct {
		name                string
		retainedUseStatus   string
		adoptionSignalType  string
		forbiddenSubstrings []string
	}{
		{
			name:                "repo_equals_users_path",
			retainedUseStatus:   "observed",
			adoptionSignalType:  "repo=/home/example/Documents/GitHub/acme",
			forbiddenSubstrings: []string{"/home/example/Documents/GitHub/acme"},
		},
		{
			name:                "path_colon_tmp_path",
			retainedUseStatus:   "path:/tmp/idiom-private",
			adoptionSignalType:  "internal_sponsor",
			forbiddenSubstrings: []string{"/tmp/idiom-private"},
		},
		{
			name:                "private_var_path",
			retainedUseStatus:   "observed",
			adoptionSignalType:  "/private/var/tmp/idiom-private",
			forbiddenSubstrings: []string{"/private/var/tmp/idiom-private"},
		},
		{
			name:                "var_folders_path",
			retainedUseStatus:   "/var/folders/zz/idiom-private",
			adoptionSignalType:  "internal_sponsor",
			forbiddenSubstrings: []string{"/var/folders/zz/idiom-private"},
		},
	} {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)

			path := filepath.Join(root, "validation-records.json")
			writeP711JSON(path, []map[string]any{{
				"record_id":             "record-enterprise-saas",
				"client_classification": "enterprise-saas",
				"evidence_status":       "complete",
				"retained_use_status":   tc.retainedUseStatus,
				"adoption_signal_type":  tc.adoptionSignalType,
				"redaction_status":      "redacted",
			}})

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{
				ProjectRoot:       root,
				ValidationRecords: path,
			}, p711TestDemoDependencies)
			gomega.Expect(report.Status).To(gomega.Equal("blocked"), fmt.Sprintf("embedded absolute path should machine-block validation records, status=%q privacy=%#v", report.Status, report.Privacy))
			gomega.Expect(report.Privacy.RedactionStatus).To(gomega.Equal("blocked"), fmt.Sprintf("embedded absolute path should machine-block validation records, status=%q privacy=%#v", report.Status, report.Privacy))
			gomega.Expect(report.Privacy.ForbiddenFieldCheck).To(gomega.Equal("failed:validation_records"), fmt.Sprintf("embedded absolute path should machine-block validation records, status=%q privacy=%#v", report.Status, report.Privacy))
			gomega.Expect(p711TestSourceRef(report.SourceReferences, "p0.8_validation_records").Status).To(gomega.Equal("blocked"), fmt.Sprintf("validation record source should be blocked: %#v", report.SourceReferences))

			retained := p711TestSignal(report.Signals, "retained_use")
			for _, key := range []string{"validation_records", "qualifying_records", "retained_use_observed", "adoption_signals"} {
				gomega.Expect(retained.Counts[key]).To(gomega.Equal(0), fmt.Sprintf("blocked validation records must not contribute %q counts: %#v", key, retained))

			}

			for _, row := range report.ClassCoverage {
				gomega.Expect(row.ValidationRecordCount).To(gomega.Equal(0), fmt.Sprintf("blocked validation records must not contribute class counts: %#v", row))

			}

			body, err := json.Marshal(report)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal report: %v", err))

			for _, forbidden := range tc.forbiddenSubstrings {
				gomega.Expect(string(body)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("blocked export leaked embedded path %q: %s", forbidden, string(body)))

			}
		})
	}
})
var _ = ginkgo.Describe("p718 absent adoption signals are not counted", func() {

	for _, absent := range []string{"", "none", "not_available", "not-applicable", "n/a", "N/A"} {
		ginkgo.It(absent, func() {
			records := []p711ValidationRecord{{
				RecordID:             "record-good",
				ClientClassification: "enterprise-saas",
				EvidenceStatus:       "complete",
				RetainedUseStatus:    "observed",
				AdoptionSignalType:   absent,
				RedactionStatus:      "redacted",
				RetainedUseEvidence:  "saved_packet_reused",
				SubstrateValueStatus: "verification_review_reused",
			}}
			retained := p711RetainedUseSignal(records)
			gomega.Expect(retained.Counts["adoption_signals"]).To(gomega.Equal(0), fmt.Sprintf("absent adoption signal %q should not be counted: %#v", absent, retained))

		})
	}
})
var _ = ginkgo.Describe("p97 product evidence daily user activation requires valid status", func() {

	for _, status := range []string{"pass", "observed", "attempted", "completed", "succeeded"} {
		ginkgo.It("accepts_"+status, func() {
			records := p97PrimaryRecords("paid_pilot", "budget_owner")
			for i := range records {
				records[i].DailyUserActivationStatus = status
			}

			gate := p97ValidationGate(records)
			gomega.Expect(gate.Counts["daily_user_activation_records"]).To(gomega.Equal(3), fmt.Sprintf("valid daily-user activation status %q should count for primary records: %#v", status, gate))

		})
	}

	for _, status := range []string{"failed", "blocked", "interest_only"} {
		ginkgo.It("rejects_"+status, func() {
			records := p97PrimaryRecords("paid_pilot", "budget_owner")
			for i := range records {
				records[i].DailyUserActivationStatus = status
			}

			gate := p97ValidationGate(records)
			gomega.Expect(gate.Status).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("invalid daily-user activation status %q must not satisfy P9.7: %#v", status, gate))
			gomega.Expect(gate.Counts["daily_user_activation_records"]).To(gomega.Equal(0), fmt.Sprintf("invalid daily-user activation status %q must not satisfy P9.7: %#v", status, gate))

		})
	}
})
var _ = ginkgo.Describe("p912 product evidence support admin and commercial overclaims fail closed", func() {

	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantBlocker string
	}{
		{
			name: "admin_readiness",
			mutate: func(thesis map[string]any) {
				admin := thesis["admin"].(map[string]any)

				admin["enterprise_surface_limits"] = []any{"Admin readiness is complete for billing and SSO."}
			},
			wantBlocker: "unsupported commercial/admin overclaim",
		},
		{
			name: "final_state_support",
			mutate: func(thesis map[string]any) {
				packaging := thesis["packaging"].(map[string]any)

				packaging["final_state"] = "Final-state enterprise support is available now."
			},
			wantBlocker: "unsupported commercial/admin overclaim",
		},
		{
			name: "paid_demand",
			mutate: func(thesis map[string]any) {
				pricing := thesis["pricing"].(map[string]any)

				pricing["hypothesis"] = "Paid demand proven by open-source activation."
			},
			wantBlocker: "unsupported commercial/admin overclaim",
		},
		{
			name: "release_readiness",
			mutate: func(thesis map[string]any) {
				decision := thesis["decision"].(map[string]any)

				decision["claim_impact"] = "Production release readiness is proven."
			},
			wantBlocker: "unsupported commercial/admin overclaim",
		},
		{
			name: "compliance",
			mutate: func(thesis map[string]any) {
				rows := thesis["classification_stances"].([]any)
				row := rows[0].(map[string]any)

				row["support_admin_boundary"] = "HIPAA compliance is included."
			},
			wantBlocker: "regulated/compliance overclaim",
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			p912MutateThesis(root, tc.mutate)

			gate := p912ProductGateForCompleteEvidence(root)
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.12 overclaim %s must fail closed: %#v", tc.name, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, tc.wantBlocker)).To(gomega.BeTrue(), fmt.Sprintf("P9.12 overclaim %s must fail closed: %#v", tc.name, gate))

		})
	}
})
var _ = ginkgo.Describe("p912 product evidence full thesis privacy markers fail closed", func() {

	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantBlocker string
	}{
		{
			name: "support_boundary_private_path",
			mutate: func(thesis map[string]any) {
				support := thesis["support"].(map[string]any)

				support["boundary"] = "Support requires reproducer notes from /workspace/CustomerX-private-repo."
			},
			wantBlocker: "support.boundary: private_path",
		},
		{
			name: "pricing_hypothesis_secret",
			mutate: func(thesis map[string]any) {
				pricing := thesis["pricing"].(map[string]any)

				pricing["hypothesis"] = "Design-partner pilot captured secret token=abc123 in pricing notes."
			},
			wantBlocker: "pricing.hypothesis: secret_value",
		},
		{
			name: "classification_stance_raw_task",
			mutate: func(thesis map[string]any) {
				rows := thesis["classification_stances"].([]any)
				row := rows[1].(map[string]any)

				row["go_no_go_evidence"] = "GO because raw task capture from CustomerX was reviewed."
			},
			wantBlocker: "classification_stances[1].go_no_go_evidence: raw_task",
		},
		{
			name: "classification_stance_customer_identifier",
			mutate: func(thesis map[string]any) {
				rows := thesis["classification_stances"].([]any)
				row := rows[2].(map[string]any)

				row["support_admin_boundary"] = "Integration triage includes CustomerX-specific repository notes."
			},
			wantBlocker: "classification_stances[2].support_admin_boundary: customer_identifier",
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			p912MutateThesis(root, tc.mutate)

			gate := p912ProductGateForCompleteEvidence(root)
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.12 privacy marker %s must fail closed: %#v", tc.name, gate))
			gomega.Expect(gate.Counts["privacy_blockers"]).NotTo(gomega.Equal(0), fmt.Sprintf("P9.12 privacy marker %s must fail closed: %#v", tc.name, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, tc.wantBlocker)).To(gomega.BeTrue(), fmt.Sprintf("P9.12 privacy marker %s must fail closed: %#v", tc.name, gate))

		})
	}
})
var _ = ginkgo.Describe("p912 product evidence decision missing or unsupported fails closed", func() {

	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantBlocker string
	}{
		{
			name: "missing_decision",
			mutate: func(thesis map[string]any) {
				delete(thesis, "decision")
			},
			wantBlocker: "required packaging/support field missing or empty: decision",
		},
		{
			name: "unsupported_outcome",
			mutate: func(thesis map[string]any) {
				decision := thesis["decision"].(map[string]any)

				decision["outcome"] = "maybe"
			},
			wantBlocker: "decision outcome must be continue, pivot, or no_go",
		},
		{
			name: "unsupported_continue",
			mutate: func(thesis map[string]any) {
				decision := thesis["decision"].(map[string]any)

				decision["outcome"] = "continue"
			},
			wantBlocker: "continue decision is unsupported by prior gate evidence",
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			p912MutateThesis(root, tc.mutate)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.12")
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("P9.12 decision case %s must fail closed: %#v", tc.name, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, tc.wantBlocker)).To(gomega.BeTrue(), fmt.Sprintf("P9.12 decision case %s must fail closed: %#v", tc.name, gate))

		})
	}
})

func p97PrimaryRecords(adoptionSignal string, buyerBudgetSignal string) []p711ValidationRecord {
	records := []p711ValidationRecord{
		p97Record("enterprise-saas", "primary-a", "context-a"),
		p97Record("enterprise-saas", "primary-b", "context-b"),
		p97Record("enterprise-saas", "primary-c", "context-b"),
		p97Record("open-source", "oss-support", "context-oss"),
		p97Record("tooling-vendor", "vendor-support", "context-vendor"),
	}
	for i := range records {
		records[i].AdoptionSignalType = adoptionSignal
		records[i].BuyerBudgetSignal = buyerBudgetSignal

		if records[i].ClientClassification == "enterprise-saas" {
			records[i].AdoptPaySponsorScore = 2
		}
	}

	return records
}

func p97Record(class string, id string, context string) p711ValidationRecord {
	return p711ValidationRecord{
		RecordID:                  id,
		ClientClassification:      class,
		EvidenceStatus:            "complete",
		RetainedUseStatus:         "observed",
		AdoptionSignalType:        "internal_platform_sponsor",
		RedactionStatus:           "redacted",
		ParticipantContextID:      context,
		PrimaryICPFit:             "yes",
		ActiveAgentUse:            "yes",
		RepoWorkflowAvailable:     "representative",
		BlockedWorkflowStatus:     "observed",
		DailyUserActivationStatus: "observed",
		RetainedUseEvidence:       "saved_packet_reused",
		SubstrateValueStatus:      "verification_review_reused",
		BuyerBudgetSignal:         "platform_owner",
		DisqualifierStatus:        "none",
		ActivationScore:           2,
		TrustSecurityScore:        2,
		WorkflowValueScore:        2,
		AdoptPaySponsorScore:      2,
	}
}

func assertP711CoverageForEveryClassification(rows []productEvidenceClassCoverage, minValidationRecords int) {
	ginkgo.GinkgoHelper()

	seen := map[string]productEvidenceClassCoverage{}
	for _, row := range rows {
		if _, exists := seen[row.Classification]; exists {
			ginkgo.Fail(fmt.Sprintf("duplicate classification coverage row for %q", row.Classification))
		}

		seen[row.Classification] = row
	}

	for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		row, ok := seen[class]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing classification coverage row for %q", class))
		gomega.Expect(row.Status).To(gomega.Equal("pass"), fmt.Sprintf("classification %q missing executable coverage fields: %#v", class, row))
		gomega.Expect(row.P76CommandID).NotTo(gomega.BeEmpty(), fmt.Sprintf("classification %q missing executable coverage fields: %#v", class, row))
		gomega.Expect(row.ClientDemoTranscriptID).NotTo(gomega.BeEmpty(), fmt.Sprintf("classification %q missing executable coverage fields: %#v", class, row))
		gomega.Expect(row.SecurityPostureCoverage).To(gomega.BeTrue(), fmt.Sprintf("classification %q missing executable coverage fields: %#v", class, row))
		gomega.Expect(row.ValidationRecordCount < minValidationRecords).To(gomega.BeFalse(), fmt.Sprintf("classification %q missing validation-record example coverage: %#v", class, row))

	}
}

func p711TestHasSignal(signals []productEvidenceSignalSummary, id string) bool {
	return p711TestSignal(signals, id).ID != ""
}

func p711TestSignal(signals []productEvidenceSignalSummary, id string) productEvidenceSignalSummary {
	for _, signal := range signals {
		if signal.ID == id {
			return signal
		}
	}

	return productEvidenceSignalSummary{}
}

func writeP711LocalOpsReport(root string) string {
	ginkgo.GinkgoHelper()

	report := localops.BenchmarkReport{
		SchemaVersion: localops.BenchmarkSchemaVersion,
		GeneratedAt:   "2026-05-14T00:00:00Z",
		Command:       "idiom local-ops benchmark",
		Summary: localops.BenchmarkSummary{
			ProfileCount:   1,
			SmokePassed:    3,
			SmokeTotal:     3,
			SmokePassRate:  1,
			ReplayLogCount: 1,
		},
		Replay: localops.ReplayReport{Status: "pass", LogCount: 1, LogIDs: []string{"log_id:p711"}},
		Privacy: localops.PrivacyReport{
			MetadataOnly:         true,
			RawPromptCaptured:    false,
			RawFileBodyCaptured:  false,
			SecretCaptureAllowed: false,
		},
		Quickstart: &localops.QuickstartSmoke{
			SmokeID:                    "p711-quickstart-smoke",
			Status:                     "pass",
			ClientClassification:       "open-source",
			EnvironmentClass:           "clean-install",
			AssistanceClass:            "none",
			DurationSource:             "measured_wall_clock",
			TimeToFirstPacketSeconds:   2,
			TimeToTrustEvidenceSeconds: 6,
			SetupInstallReadiness: localops.ActivationSetupEvidence{
				Status:              "pass",
				InstallSetupPath:    "release-binary",
				ReleaseInstallProof: "pass",
			},
			FirstUsefulPacket: localops.ActivationPacketEvidence{
				Status:               "pass",
				CommandID:            "p77-context-pack",
				PacketID:             "pack:p711",
				IndexID:              "index:p711",
				FreshnessState:       "fresh",
				CandidateFileCount:   2,
				CandidateSymbolCount: 1,
				CandidateTestCount:   1,
			},
			FreshnessCheck: localops.ActivationFreshnessEvidence{Status: "pass", CommandID: "p77-freshness-check", State: "fresh", FreshForScope: true, GlobalFresh: true},
			AuditReport: localops.ActivationAuditEvidence{
				Status:             "pass",
				CommandID:          "p77-harness-audit",
				AuditReportID:      "audit:p711",
				AuditEventIDs:      []string{"audit-event:p711"},
				HighFindingCount:   0,
				MediumFindingCount: 0,
				FindingCount:       0,
			},
			SuggestedVerification: localops.ActivationVerification{
				Status:            "pass",
				SuggestedCommands: []string{"go test ./..."},
			},
			ProductEvidenceSafeOutput: localops.ActivationProductEvidence{
				Status:              "pass",
				ExportID:            "p77-activation-p711",
				ForbiddenFieldCheck: "pass",
				RedactionStatus:     "redacted",
			},
			FailureReason: localops.ActivationFailureReason{Status: "none", PrimaryClass: "none", RetryAllowed: false},
			DocsGap:       localops.ActivationDocsGap{Status: "none", GapRecorded: false},
			PrivacyRedactionStatus: localops.ActivationPrivacyEvidence{
				Status:              "pass",
				ForbiddenFieldCheck: "pass",
			},
			Cleanup: localops.ActivationCleanupEvidence{
				Status:               "pass",
				CommandID:            "p77-cleanup",
				Mode:                 "remove-local-state",
				ExitStatus:           "pass",
				RemovedArtifactCount: 1,
				RemainingState:       "comprehension_state_absent",
				EvidenceIDs:          []string{"cleanup:p711"},
			},
		},
	}
	path := filepath.Join(root, ".idiom", "local-ops", "report.json")
	writeP711JSON(path, report)

	return path
}

func writeP910CompetitiveBenchmarkReport(root string, conditions []map[string]any) string {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, "testdata", "benchmarks", "localization-competitive.json")
	writeP711JSON(path, map[string]any{
		"schema_version":         "idiom.localization_benchmark.v1",
		"status":                 "pass",
		"competitive_conditions": conditions,
	})

	return path
}

func stageP910DefaultCorpusFixture(root string) {
	ginkgo.GinkgoHelper()

	sourcePath := filepath.Join(p711TestRepoRoot(), "testdata", "localization", "recent-implementation-corpus.json")

	data, err := os.ReadFile(sourcePath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read default localization corpus fixture: %v", err))

	path := filepath.Join(root, "testdata", "localization", "recent-implementation-corpus.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, data, 0o644)).To(gomega.Succeed())

}

func p910JSONCountsFromProduct(body []byte) map[string]int {
	ginkgo.GinkgoHelper()

	var report struct {
		ReleaseGateLinkage []struct {
			Gate   string         `json:"gate"`
			Counts map[string]int `json:"counts"`
		} `json:"release_gate_linkage"`
	}
	gomega.Expect(json.Unmarshal(body, &report)).To(gomega.Succeed())

	for _, gate := range report.ReleaseGateLinkage {
		if gate.Gate == "P9.10" {
			return gate.Counts
		}
	}
	ginkgo.Fail(fmt.Sprintf("product evidence JSON missing P9.10 release gate: %s", string(body)))

	return nil
}

func p910JSONCountsFromReleaseReadiness(body []byte) map[string]int {
	ginkgo.GinkgoHelper()

	var report struct {
		ReleaseReadiness []struct {
			Gate   string         `json:"gate"`
			Counts map[string]int `json:"counts"`
		} `json:"release_readiness"`
	}
	gomega.Expect(json.Unmarshal(body, &report)).To(gomega.Succeed())

	for _, gate := range report.ReleaseReadiness {
		if gate.Gate == "P9.10" {
			return gate.Counts
		}
	}
	ginkgo.Fail(fmt.Sprintf("release evidence JSON missing P9.10 readiness gate: %s", string(body)))

	return nil
}

func p910JSONCountsFromReleaseCompetitiveCoverage(body []byte) map[string]int {
	ginkgo.GinkgoHelper()

	var report struct {
		BenchmarkLocalOps struct {
			CompetitiveCoverage struct {
				Counts map[string]int `json:"counts"`
			} `json:"competitive_coverage"`
		} `json:"benchmark_local_ops"`
	}
	gomega.Expect(json.Unmarshal(body, &report)).To(gomega.Succeed())

	return report.BenchmarkLocalOps.CompetitiveCoverage.Counts
}

func assertP910RequiredCountKeys(counts map[string]int) {
	ginkgo.GinkgoHelper()

	for _, key := range []string{
		"required_external_baselines",
		"baseline_rows",
		"covered_external_baselines",
		"measured_external_baselines",
		"documented_infeasible_external_baselines",
		"missing_external_baselines",
		"malformed_baseline_rows",
		"self_referential_baselines",
		"external_baseline_rows",
		"external_measured_tasks",
		"external_infeasible_tasks",
		"external_missing_tasks",
		"external_invalid_client_classifications",
		"target_client_coverage_baselines",
	} {
		if _, ok := counts[key]; !ok {
			ginkgo.Fail(fmt.Sprintf("P9.10 JSON counts missing key %q: %#v", key, counts))
		}
	}
}

func p910CoveredCompetitiveConditions() []map[string]any {
	return []map[string]any{
		p910MeasuredCondition("aider_repo_map"),
		p910InfeasibleCondition("external_agent_native"),
	}
}

func p910MeasuredCondition(baseline string) map[string]any {
	return map[string]any{
		"baseline":           baseline,
		"label":              baseline,
		"status":             "measured",
		"required":           true,
		"external":           baseline == "aider_repo_map" || baseline == "external_agent_native",
		"total_tasks":        1,
		"measured_tasks":     1,
		"missing_tasks":      0,
		"budgeted_tasks":     1,
		"reproducible_tasks": 1,
		"commanded_tasks":    1,
		"artifacted_tasks":   1,
		"verified_tasks":     1,
		"client_coverage":    map[string]int{"enterprise_saas": 1, "open_source": 1, "tooling_vendor": 1},
		"top_k_edit_path_coverage": []map[string]any{{
			"k":                1,
			"available":        true,
			"numerator":        1,
			"denominator":      1,
			"rate":             1,
			"tasks_with_input": 1,
			"total_tasks":      1,
		}},
	}
}

func p910InfeasibleCondition(baseline string) map[string]any {
	return map[string]any{
		"baseline":              baseline,
		"label":                 baseline,
		"status":                "documented_infeasible",
		"required":              true,
		"external":              true,
		"total_tasks":           1,
		"infeasible_tasks":      1,
		"missing_tasks":         0,
		"reproducible_tasks":    1,
		"commanded_tasks":       1,
		"artifacted_tasks":      1,
		"verified_tasks":        1,
		"client_coverage":       map[string]int{"enterprise_saas": 1, "open_source": 1, "tooling_vendor": 1},
		"infeasibility_reasons": map[string]int{"tool_unavailable": 1},
		"claim_impacts":         map[string]int{"claim_weakened_until_external_run": 1},
	}
}

func p910ConditionPatch(condition map[string]any, patch map[string]any) map[string]any {
	out := make(map[string]any, len(condition)+len(patch))
	for key, value := range condition {
		out[key] = value
	}

	for key, value := range patch {
		out[key] = value
	}

	return out
}

func writeP711ValidationRecords(root string) string {
	ginkgo.GinkgoHelper()

	classes := []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"}
	records := make([]map[string]any, 0, len(classes)+2)

	for _, class := range classes {
		record := p711ValidationRecordFixture("record-"+class, class)
		if class == "enterprise-saas" {
			record["participant_context_id"] = "context-platform-a"
			record["adoption_signal_type"] = "internal_platform_sponsor"
			record["buyer_budget_signal"] = "platform_owner"
			record["adopt_pay_sponsor_score"] = 2
		}

		records = append(records, record)
	}

	extraPrimary := p711ValidationRecordFixture("record-enterprise-saas-b", "enterprise-saas")

	extraPrimary["participant_context_id"] = "context-platform-b"
	extraPrimary["adoption_signal_type"] = "budget_owner"
	extraPrimary["buyer_budget_signal"] = "budget_discussion"
	extraPrimary["adopt_pay_sponsor_score"] = 3
	records = append(records, extraPrimary)

	thirdPrimary := p711ValidationRecordFixture("record-enterprise-saas-c", "enterprise-saas")

	thirdPrimary["participant_context_id"] = "context-platform-b"
	thirdPrimary["adoption_signal_type"] = "none"
	thirdPrimary["buyer_budget_signal"] = "none"
	thirdPrimary["adopt_pay_sponsor_score"] = 1
	records = append(records, thirdPrimary)

	path := filepath.Join(root, "validation-records.json")
	writeP711JSON(path, records)

	return path
}

func p711ValidationRecordFixture(recordID string, class string) map[string]any {
	return map[string]any{
		"record_id":                    recordID,
		"client_classification":        class,
		"evidence_status":              "complete",
		"retained_use_status":          "observed",
		"adoption_signal_type":         "internal_sponsor",
		"redaction_status":             "redacted",
		"participant_context_id":       "context-" + class,
		"primary_icp_fit":              "yes",
		"active_agent_use":             "yes",
		"repo_workflow_available":      "representative",
		"blocked_workflow_status":      "observed",
		"daily_user_activation_status": "observed",
		"retained_use_evidence":        "saved_packet_reused",
		"substrate_value_status":       "verification_review_reused",
		"buyer_budget_signal":          "internal_sponsor",
		"disqualifier_status":          "none",
		"activation_score":             2,
		"trust_security_score":         2,
		"workflow_value_score":         2,
		"adopt_pay_sponsor_score":      2,
	}
}

var _ = ginkgo.Describe("p99 product evidence rejects posture matrix overclaims", func() {

	cases := []struct {
		name    string
		mutate  func(map[string]any)
		pattern string
	}{
		{
			name: "control_claim_mcp_conformance",
			mutate: func(matrix map[string]any) {
				controls := matrix["required_controls"].([]any)
				control := controls[0].(map[string]any)

				control["claim"] = "Idiom has MCP conformance."
			},
			pattern: "mcp conformance",
		},
		{
			name: "client_limitation_hipaa_certification",
			mutate: func(matrix map[string]any) {
				rows := matrix["client_classification_coverage"].([]any)
				row := rows[3].(map[string]any)

				row["limitation"] = "HIPAA certification."
			},
			pattern: "hipaa certification",
		},
	}

	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)

			matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")

			var matrix map[string]any

			data, err := os.ReadFile(matrixPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read matrix: %v", err))
			gomega.Expect(json.Unmarshal(data, &matrix)).To(gomega.Succeed())

			tc.mutate(matrix)
			writeP711JSON(matrixPath, matrix)

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("matrix overclaim %q should fail closed with structured blocker: %#v", tc.pattern, gate))
			gomega.Expect(gate.Counts["matrix_overclaim_blockers"]).NotTo(gomega.Equal(0), fmt.Sprintf("matrix overclaim %q should fail closed with structured blocker: %#v", tc.pattern, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, "posture matrix field")).To(gomega.BeTrue(), fmt.Sprintf("matrix overclaim %q should fail closed with structured blocker: %#v", tc.pattern, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, tc.pattern)).To(gomega.BeTrue(), fmt.Sprintf("matrix overclaim %q should fail closed with structured blocker: %#v", tc.pattern, gate))

		})
	}
})
var _ = ginkgo.Describe("p99 product evidence rejects hollow source verification records", func() {

	cases := []struct {
		name    string
		records string
		blocker string
	}{
		{
			name: "empty_source_id",
			records: "- source_id:\n" +
				"  checked_date: 2026-05-14\n" +
				"  verification_method: browser/manual source check\n",
			blocker: "source_id is empty",
		},
		{
			name: "empty_checked_date",
			records: "- source_id: nist-ai-rmf\n" +
				"  checked_date:\n" +
				"  verification_method: browser/manual source check\n",
			blocker: "checked_date is empty",
		},
		{
			name: "empty_verification_method",
			records: "- source_id: nist-ai-rmf\n" +
				"  checked_date: 2026-05-14\n" +
				"  verification_method:\n",
			blocker: "verification_method is empty",
		},
		{
			name: "invalid_checked_date",
			records: "- source_id: nist-ai-rmf\n" +
				"  checked_date: 2026/05/14\n" +
				"  verification_method: browser/manual source check\n",
			blocker: "checked_date must be YYYY-MM-DD",
		},
		{
			name: "missing_per_record_unreachable_method",
			records: "- source_id: reachable-source\n" +
				"  checked_date: 2026-05-14\n" +
				"  source_status: reachable\n" +
				"  verification_method: browser/manual source check\n" +
				"  unreachable_source_verification_method: fallback recorded on this record\n" +
				"- source_id: blocked-source\n" +
				"  checked_date: 2026-05-14\n" +
				"  source_status: unreachable_403\n" +
				"  verification_method: browser/manual source check\n",
			blocker: "unreachable source evidence missing verification method for source_id: blocked-source",
		},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			root := ginkgo.GinkgoT().TempDir()
			stageP711ProductEvidenceFixtures(root)
			writeP711Text(filepath.Join(root, "docs", "security-privacy-posture.md"), "Reviewer Command\n\n"+p99PlanFixtureWithSourceRecords(tc.records))

			report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{}, productEvidenceExportOptions{ProjectRoot: root}, p711TestDemoDependencies)
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
			gomega.Expect(gate.Status).To(gomega.Equal("needs_evidence"), fmt.Sprintf("source verification blocker %q missing: %#v", tc.blocker, gate))
			gomega.Expect(p711TestBlockerContains(gate.Blockers, tc.blocker)).To(gomega.BeTrue(), fmt.Sprintf("source verification blocker %q missing: %#v", tc.blocker, gate))

		})
	}
})

func p711TestSourceRef(refs []productEvidenceSourceReference, id string) productEvidenceSourceReference {
	for _, ref := range refs {
		if ref.ID == id {
			return ref
		}
	}

	return productEvidenceSourceReference{}
}

func p711TestReleaseGate(rows []productEvidenceReleaseGateLinkage, gate string) productEvidenceReleaseGateLinkage {
	for _, row := range rows {
		if row.Gate == gate {
			return row
		}
	}

	return productEvidenceReleaseGateLinkage{}
}

func p711TestBlockerContains(blockers []string, needle string) bool {
	for _, blocker := range blockers {
		if strings.Contains(blocker, needle) {
			return true
		}
	}

	return false
}

func stageProductEvidenceRecoveryPacket(root string, sessionID string) recoverycontract.DecompositionPacket {
	ginkgo.GinkgoHelper()

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

	record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         sessionID,
		ProgressStatePath: ".idiom/plan-execute/" + sessionID + "/state.json",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "executor failed while editing a private branch",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/recovery_fixture.go"},
		AllowedEditPaths:  []string{"internal/surfaces/tui/app/recovery_fixture.go"},
		ChangedPaths:      []string{"internal/surfaces/tui/app/recovery_fixture.go"},
		TotalEdits:        1,
	}, now)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write recovery failure record: %v", err))

	packet, _, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, now)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write recovery packet: %v", err))
	gomega.Expect(packet.CandidateAtoms).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected candidate atom in recovery packet: %#v", packet))

	return packet
}

func stageProductEvidenceRecoveredPacket(root string, sessionID string) recoverycontract.DecompositionPacket {
	ginkgo.GinkgoHelper()

	packet := stageProductEvidenceRecoveryPacket(root, sessionID)

	var (
		completed []string
		commands  []recoverycontract.ReconciliationCommandResult
	)

	for _, atom := range packet.CandidateAtoms {
		completed = append(completed, atom.ID)
		for _, command := range atom.RequiredTests {
			commands = append(commands, recoverycontract.ReconciliationCommandResult{
				Command:  command,
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			})
		}
	}

	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 12, 5, 0, 0, time.UTC),
		CompletedAtomIDs: completed,
		Verification: recoverycontract.ReconciliationVerification{
			Status:   recoverycontract.ReconciliationVerificationPass,
			Commands: commands,
		},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build recovery reconciliation: %v", err))

	if _, _, err := recovery.WriteReconciliation(root, reconciliation, ""); err != nil {
		ginkgo.Fail(fmt.Sprintf("write recovery reconciliation: %v", err))
	}

	return packet
}

func assertP811IntegrationNeedsEvidence(report productEvidenceExportReport) {
	ginkgo.GinkgoHelper()

	integration := p711TestSignal(report.Signals, "integration_proof")
	gomega.Expect(integration.Completeness).NotTo(gomega.Equal("complete"), fmt.Sprintf("malformed integration proofs must not complete the signal: %#v", integration))
	gomega.Expect(integration.Status).NotTo(gomega.Equal("pass"), fmt.Sprintf("malformed integration proofs must not complete the signal: %#v", integration))

	if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.8"); linkage.Status == "evidence_present" {
		ginkgo.Fail(fmt.Sprintf("P9.8 should not be evidence_present for malformed proof rows: %#v", report.ReleaseGateLinkage))
	}
	gomega.Expect(report.ProductSuccessGates.IntegrationProof).NotTo(gomega.Equal("evidence_present"), fmt.Sprintf("product integration gate should need evidence for malformed proof rows"))

}

func stageP711ProductEvidenceFixtures(root string) {
	ginkgo.GinkgoHelper()

	writeP711IntegrationProofs(root, []map[string]any{
		p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
		p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
		p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
	})

	var (
		securityRows []map[string]any
		transcripts  []map[string]any
	)

	controlIDs := p99RequiredControlIDs()

	for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		commandID := "p76-" + class

		securityRows = append(securityRows, map[string]any{
			"classification":      class,
			"stance":              p711DefaultStance(class),
			"p7_6_command_id":     commandID,
			"covered_control_ids": controlIDs,
			"limitation":          "proof-only reviewed control fixture",
		})
		transcripts = append(transcripts, p911TranscriptFixture(class, commandID))
	}

	stageP99PostureGateFixtures(root, securityRows)
	writeP912PackagingSupportThesis(root, "continue")
	writeP711JSON(filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json"), map[string]any{
		"schema_version": "idiom.client_demo_transcripts.v1",
		"name":           "p7.10-client-demo-transcripts",
		"shared_contract": map[string]any{
			"sidecar_schema_version": "idiom.tool.v1",
		},
		"transcripts": transcripts,
	})
}

func p911TranscriptFixture(class string, commandID string) map[string]any {
	stance := p711DefaultStance(class)
	if class == "enterprise-saas" {
		stance = "Primary buyer-facing enterprise SaaS local sidecar trust demo"
	}

	if class == "open-source" {
		stance = "Adoption wedge and proof pool"
	}

	mvp := "MVP limitation: proof-only local sidecar demo; no paid demand, support readiness, or release readiness claim."
	if p99RegulatedClass(class) {
		mvp = "Proof-only: no regulated compliance, certification, attestation, sales, or release readiness claim."
	}

	releaseImpact := "Supports demo-proof only; final support, paid demand, and release readiness remain gated by later evidence."
	if class == "enterprise-saas" {
		releaseImpact = "Supports the primary enterprise-SaaS buyer-facing demo story only; paid demand and release readiness remain gated by P9.7/P9.12."
	}

	return map[string]any{
		"id":                        "p7.10-" + class,
		"classification":            class,
		"classification_label":      class,
		"stance":                    stance,
		"p7_6_command_id":           commandID,
		"value_claim":               "A reviewer can inspect local sidecar contract evidence for " + class + " without exposing raw content.",
		"selling_point":             "Idiom sells local metadata-only trust evidence, not regulated or commercial readiness.",
		"generic_primitives":        []string{"tool_sidecar_envelope", "context_pack"},
		"shared_security_contracts": []string{"redaction_no_raw_prompt_or_file_body", "side_effect_policy_read_only_discovery"},
		"evidence_fields":           []string{"Tool", "sidecar.schema_version", "sidecar.audit_event_ids"},
		"mvp_limitation":            mvp,
		"final_state_expansion":     "Final-state expansion can add reviewed packaging, support boundaries, admin policy, and retention evidence after later gates pass.",
		"privacy_posture":           "Metadata-only demo posture: no raw prompt, no raw file body, no secrets, no private paths, and no customer identifiers are expected.",
		"pass_no_go_criteria": map[string]any{
			"pass":  []string{"The command emits structured metadata-only evidence."},
			"no_go": []string{"The demo leaks raw content or implies readiness beyond evidence."},
		},
		"release_claim_impact": releaseImpact,
		"commands": []map[string]any{{
			"id":        commandID,
			"command":   "idiom tool write_file -config $IDIOM_CONFIG --profile discovery --input '{\"path\":\"demo.txt\",\"content\":\"redacted-demo-marker\"}'",
			"argv":      []string{"tool", "write_file", "-config", "$IDIOM_CONFIG", "--profile", "discovery", "--input", "{\"path\":\"demo.txt\",\"content\":\"redacted-demo-marker\"}"},
			"exit_code": 1,
			"json":      true,
			"expected_output_shape": map[string]any{
				"stable_fields": []string{"Tool"},
				"json_string": map[string]string{
					"Tool": "write_file",
				},
				"stdout_excludes": []string{"redacted-demo-marker"},
			},
		}},
	}
}

func stageP99PostureGateFixtures(root string, securityRows []map[string]any) {
	ginkgo.GinkgoHelper()

	var controls []map[string]any

	for _, id := range p99RequiredControlIDs() {
		controls = append(controls, map[string]any{
			"id":              id,
			"claim":           "reviewed " + id,
			"mvp_limitations": "fixture evidence only",
			"evidence": []map[string]any{{
				"kind":     "go_test",
				"command":  "go test ./cmd/idiom -run TestP99",
				"path":     "cmd/idiom/p9_9_security_privacy_gate_test.go",
				"check_id": "fixture-" + id,
			}},
		})
	}

	writeP711JSON(filepath.Join(root, "docs", "security-privacy-posture.matrix.json"), map[string]any{
		"schema_version":                 "idiom.security_privacy_posture.v1",
		"artifact_id":                    "p7.9-security-privacy-posture",
		"required_controls":              controls,
		"client_classification_coverage": securityRows,
		"forbidden_evidence_fields":      p99ForbiddenEvidenceFields(),
		"release_gate_tiebacks":          p99RequiredTiebacks(),
	})
	writeP711Text(filepath.Join(root, "docs", "security-privacy-posture.md"), "# Security And Privacy Posture\n\nReviewer Command\n\nThis fixture is not a certification or attestation.\n\n"+p99PlanFixtureText(true))
	writeP711Text(filepath.Join(root, "docs", "release.md"), p99ReleaseFixtureText("Reviewer signoff: GO"))
}

func writeP912PackagingSupportThesis(root string, outcome string) {
	ginkgo.GinkgoHelper()

	classRows := []map[string]any{
		p912ClassThesisRow("enterprise-saas", "primary paid beachhead", "Primary paid beachhead local CLI sidecar package for platform teams.", "Final-state platform package can add policy and evidence management after proof.", "Limited async platform-team support; no SSO, fleet policy, billing admin, hosted dashboard, or managed deployment.", "GO only with P9.7 buyer signal, P9.8 integration, P9.11 demos, and P9.12 decision evidence."),
		p912ClassThesisRow("open-source", "adoption wedge", "Adoption wedge and proof pool with quickstart, fixture, local install, cleanup docs, and export examples.", "Community package can remain low-friction while feeding docs, demos, and retained-use evidence.", "No paid-enterprise demand credit unless a record independently satisfies primary ICP criteria.", "GO for activation, docs, and demo proof; NO-GO as paid proof without enterprise-SaaS buyer evidence."),
		p912ClassThesisRow("tooling-vendor", "secondary integration path", "Secondary integration path with schema/profile docs, CLI transcript, no-strategy-leakage examples, and MCP parity.", "Partner package can add conformance tests and compatibility guarantees after substrate proof.", "Integration-question triage only; no custom embedding or vendor roadmap commitment.", "GO as pivot or expansion only with vendor sponsor signal and parity evidence."),
		p912ClassThesisRow("local-model", "secondary enabling segment", "Secondary enabling segment with readiness docs, local smoke fixture, capacity evidence, and no-hosted-telemetry proof.", "Local-operations package can add backend readiness and offline-friendly evidence management.", "Support covers harness readiness, not model tuning as a service.", "GO when P6/P9 readiness and activation evidence are strong."),
		p912ClassThesisRow("finance", "deferred proof-only row", "Deferred proof-only row with local evidence, protected-path posture, audit controls, and finance demo limitations.", "Regulated package can add control mapping and customer-owned review after controls pass.", "No formal attestation, no regulated sales claim, and no compliance claim in MVP.", "GO for proof-only demos with controls; NO-GO for sales claims before P9.9 evidence."),
		p912ClassThesisRow("healthcare", "deferred proof-only row", "Deferred proof-only row with local/privacy posture and healthcare demo limitations.", "Regulated package can add retention, redaction, and customer-owned review after controls pass.", "No e-PHI handling, no raw prompts, no raw file bodies, and no HIPAA claim.", "GO for non-sensitive proof; NO-GO if value requires hosted telemetry or formal compliance before controls."),
		p912ClassThesisRow("public-sector", "deferred proof-only row", "Deferred proof-only row with offline-friendly commands, reproducible evidence, checksums, and policy-limit demos.", "Self-hosted package can add supply-chain artifacts and admin policy after proof.", "No FedRAMP claim, no air-gapped support, and no procurement package in MVP.", "GO for executable local proof; NO-GO if adoption requires certification, fleet admin, or managed support before substrate proof."),
	}
	decisionTrigger := "continue evidence IDs show P0.11 and P9.7 through P9.11 evidence can support bounded packaging."
	claimImpact := "Continue evidence gathering for primary enterprise-SaaS packaging; paid claims are limited to P9.7 evidence and no production release claim is made."

	if outcome == "pivot" {
		decisionTrigger = "pivot trigger: primary enterprise-SaaS budget ownership failed while secondary sidecar evidence remained strong."
		claimImpact = "Pivot decision limits claims to the named sidecar subset and does not claim primary enterprise-SaaS paid demand."
	}

	if outcome == "no_go" {
		decisionTrigger = "no-go trigger: prior gates are incomplete for commercial packaging."
		claimImpact = "No-go decision blocks commercial and production claims until the missing gate evidence is supplied."
	}

	writeP711JSON(filepath.Join(root, "docs", "packaging-support-thesis.json"), map[string]any{
		"schema_version": "idiom.packaging_support_thesis.v1",
		"artifact_id":    "p0.11-packaging-support-thesis",
		"icp": map[string]any{
			"primary_buyer":       "enterprise-saas",
			"adoption_wedge":      "open-source",
			"secondary":           []string{"tooling-vendor", "local-model"},
			"deferred_proof_only": []string{"finance", "healthcare", "public-sector"},
			"buyer":               "platform, DevEx, DevSecOps, or AI-enablement owner",
			"daily_user":          "developer or reviewer using several coding agents on merge-facing repo work",
			"evidence_ids":        []string{"p0.7-icp", "p0.11-icp"},
		},
		"packaging": map[string]any{
			"mvp":          "Local CLI sidecar, fixture/demo pack, CI/script output, product-evidence export, install/cleanup docs, and limited platform-team support.",
			"final_state":  "Enterprise platform package may add policy-as-code, evidence management, procurement artifacts, and support tiers after local substrate proof.",
			"evidence_ids": []string{"p0.11-mvp-packaging", "p7.11-local-product-evidence-export"},
		},
		"pricing": map[string]any{
			"hypothesis":     "Unquoted design-partner or internal-platform pilot tied to repo/team/caller evidence value, not token resale or chat seats.",
			"signal_quality": "hypothesis",
			"evidence_ids":   []string{"p0.11-pricing-hypothesis", "p9.7-demand-gate"},
		},
		"support": map[string]any{
			"boundary":     "Limited async support for installation blockers, reproducible activation failures, docs gaps, privacy-safe export issues, and sidecar integration questions.",
			"level":        "limited_async_design_partner",
			"evidence_ids": []string{"p0.11-support-boundary"},
		},
		"admin": map[string]any{
			"non_goals":                 []string{"sso", "rbac", "billing_admin", "fleet_rollout", "hosted_dashboard", "managed_deployment", "formal_compliance_attestation"},
			"enterprise_surface_limits": []string{"repo_local_configuration", "environment_variables", "profile_files", "redaction_retention_settings", "machine_readable_output"},
			"unsupported_requests":      []string{"24x7_sla", "dedicated_support_engineer", "custom_compliance_attestation", "managed_deployment", "bespoke_feature_development", "fleet_policy_console", "hosted_admin_dashboard"},
			"unsupported_request_count": 7,
			"evidence_ids":              []string{"p0.11-admin-non-goals"},
		},
		"local_evidence_export": map[string]any{
			"status":          "required local metadata-only artifact with IDs, hashes, counts, blockers, classifications, and gate links.",
			"privacy_posture": "metadata-only local export; no raw prompt, raw file body, secret, credential, private path, customer identifier, or hosted telemetry requirement.",
			"evidence_ids":    []string{"p7.11-product-evidence-export", "p9.12-local-export-link"},
		},
		"classification_stances": classRows,
		"decision": map[string]any{
			"outcome":      outcome,
			"trigger":      decisionTrigger,
			"claim_impact": claimImpact,
			"evidence_ids": []string{"p0.11-packaging-support-thesis", "p9.7-demand-gate", "p9.8-integration-gate", "p9.9-security-privacy-gate", "p9.10-competitive-gate", "p9.11-demo-gate"},
		},
	})
}

func p912ClassThesisRow(class string, stance string, mvp string, finalState string, boundary string, goNoGo string) map[string]any {
	return map[string]any{
		"classification":         class,
		"packaging_stance":       stance,
		"mvp_packaging_stance":   mvp,
		"final_state_hypothesis": finalState,
		"support_admin_boundary": boundary,
		"go_no_go_evidence":      goNoGo,
		"evidence_ids":           []string{"p0.11-class-" + class, "p7.10-" + class},
	}
}

func p912MutateThesis(root string, mutate func(map[string]any)) {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, "docs", "packaging-support-thesis.json")

	var thesis map[string]any

	p711ReadTestJSON(path, &thesis)
	mutate(thesis)
	writeP711JSON(path, thesis)
}

func p912ProductGateForCompleteEvidence(root string) productEvidenceReleaseGateLinkage {
	ginkgo.GinkgoHelper()

	localOpsPath := writeP711LocalOpsReport(root)
	recordsPath := writeP711ValidationRecords(root)
	benchmarkPath := writeP910CompetitiveBenchmarkReport(root, p910CoveredCompetitiveConditions())
	report := buildProductEvidenceExportReportWithDeps(root, lanehistory.Summary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	}, p711TestDemoDependencies)

	return p711TestReleaseGate(report.ReleaseGateLinkage, "P9.12")
}

func p99PlanFixtureText(includeUnreachableMethod bool) string {
	unreachableMethod := ""
	if includeUnreachableMethod {
		unreachableMethod = "  unreachable_source_verification_method: browser/manual source check after CLI received 403\n"
	}

	return p99PlanFixtureWithSourceRecords("- source_id: hhs-hipaa-security-rule\n" +
		"  checked_date: 2026-05-14\n" +
		"  source_status: unreachable_403\n" +
		"  verification_method: browser/manual source check\n" +
		unreachableMethod)
}

func p99PlanFixtureWithSourceRecords(records string) string {
	return "### P0.9 Product Security And Privacy Posture\n\n" +
		"Source anchors checked on 2026-05-14 UTC.\n\n" +
		"Product threat model:\nP7.9 P8.4 P9.1 P9.3 P9.9\n\n" +
		"Data-flow map:\n\n" +
		"Privacy and retention posture:\n\n" +
		"Control mapping evidence:\n\n" +
		"Source verification evidence:\n" +
		records +
		"\nDownstream go/no-go gates:\n"
}

func p99ReleaseFixtureText(decision string) string {
	return "# Release\n\n" +
		"## P9.9 Security Privacy Gate\n\n" +
		decision + "\n\n" +
		"Threat model\n\n" +
		"Data-flow\n\n" +
		"Privacy\n\n" +
		"Control mapping\n\n" +
		"## Reproduction Commands\n"
}

func writeP711IntegrationProofs(root string, proofs []map[string]any) {
	ginkgo.GinkgoHelper()

	writeP711JSON(filepath.Join(root, "testdata", "sidecar", "integration-proofs.json"), map[string]any{
		"schema_version": "idiom.sidecar_integration_proofs.v1",
		"name":           "p7.8-sidecar-integration-proofs",
		"proofs":         proofs,
	})
}

func p711IntegrationProofFixture(id string, path string, executable bool, placeholder bool) map[string]any {
	fixture := map[string]any{
		"id":     id,
		"path":   path,
		"status": "test_validated",
		"readiness": map[string]bool{
			"executable_evidence":  executable,
			"reviewed_placeholder": placeholder,
		},
		"privacy": map[string]bool{
			"raw_prompts":                  false,
			"file_bodies":                  false,
			"private_paths":                false,
			"credentials":                  false,
			"private_repo_or_customer_ids": false,
		},
	}
	if path == "mcp_stdio" {
		fixture["replay_fixture"] = "testdata/sidecar/mcp-stdio-integration-proof.json"
		fixture["product_evidence_export_parity"] = true

		for _, field := range p98RequiredMCPParityReadinessFields {
			fixture["readiness"].(map[string]bool)[field] = true
		}
	}

	return fixture
}

func p711IntegrationProofWithMCPParityOnPath(id string, path string) map[string]any {
	fixture := p711IntegrationProofFixture(id, path, true, false)

	fixture["replay_fixture"] = "testdata/sidecar/mcp-stdio-integration-proof.json"
	fixture["product_evidence_export_parity"] = true

	for _, field := range p98RequiredMCPParityReadinessFields {
		fixture["readiness"].(map[string]bool)[field] = true
	}

	return fixture
}

func p711IntegrationProofWithoutMCPParityFixture(id string) map[string]any {
	fixture := p711IntegrationProofFixture(id, "mcp_stdio", true, false)
	delete(fixture, "replay_fixture")

	fixture["product_evidence_export_parity"] = false

	readiness := fixture["readiness"].(map[string]bool)

	for _, field := range p98RequiredMCPParityReadinessFields {
		delete(readiness, field)
	}

	return fixture
}

func p711IntegrationProofWithStatus(id string, path string, status string) map[string]any {
	fixture := p711IntegrationProofFixture(id, path, true, false)

	fixture["status"] = status

	return fixture
}

func p711IntegrationProofWithoutPrivacy(id string, path string) map[string]any {
	fixture := p711IntegrationProofFixture(id, path, true, false)
	delete(fixture, "privacy")

	return fixture
}

func p711IntegrationProofWithOmittedPrivacyField(id string, path string, field string) map[string]any {
	fixture := p711IntegrationProofFixture(id, path, true, false)
	delete(fixture["privacy"].(map[string]bool), field)

	return fixture
}

func p711IntegrationProofWithPrivacyValue(id string, path string, field string, value bool) map[string]any {
	fixture := p711IntegrationProofFixture(id, path, true, false)

	fixture["privacy"].(map[string]bool)[field] = value

	return fixture
}

func writeP711JSON(path string, value any) {
	ginkgo.GinkgoHelper()

	data, err := json.MarshalIndent(value, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal fixture: %v", err))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, append(data, '\n'), 0o644)).To(gomega.Succeed())

}

func p711ReadTestJSON(path string, value any) {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read json %s: %v", path, err))
	gomega.Expect(json.Unmarshal(data, value)).To(gomega.Succeed())

}

func p911MutateFirstTranscript(root string, mutate func(map[string]any)) {
	ginkgo.GinkgoHelper()

	p911MutateTranscriptByClass(root, "finance", mutate)
}

func p911MutateTranscriptByClass(root string, class string, mutate func(map[string]any)) {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")

	var set map[string]any

	p711ReadTestJSON(path, &set)

	transcripts, ok := set["transcripts"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("transcript fixture missing transcripts array: %#v", set))

	for _, item := range transcripts {
		row, ok := item.(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("transcript row is not object: %#v", item))

		if row["classification"] == class {
			mutate(row)
			writeP711JSON(path, set)

			return
		}
	}
	ginkgo.Fail(fmt.Sprintf("transcript fixture missing class %q", class))
}

func writeP711Text(path string, value string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(value), 0o644)).To(gomega.Succeed())

}
