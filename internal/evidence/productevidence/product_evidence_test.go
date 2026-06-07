package productevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/localops"
	"idiom/internal/workflows/recovery"
)

func TestP711ProductEvidenceExportCompletenessPrivacyAndClassCoverage(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    "cli",
		Action:    "boost",
		State:     "accepted",
		Reason:    "grounded",
		Files:     []string{"cmd/idiom/product_evidence.go"},
		Evidence:  &comprehension.LearningEvidenceRef{Kind: "eval_result", Verified: true, ResultID: "result:p711"},
	}); err != nil {
		t.Fatalf("append learning log: %v", err)
	}

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 2}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	})
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal product evidence: %v", err)
	}
	var reparsed productEvidenceExportReport
	if err := json.Unmarshal(body, &reparsed); err != nil {
		t.Fatalf("product evidence JSON should parse: %v", err)
	}
	if reparsed.SignalCompleteness.Status != "complete" || reparsed.SignalCompleteness.CompleteCount != reparsed.SignalCompleteness.RequiredCount {
		t.Fatalf("expected complete P7.11 signal matrix, got %#v", reparsed.SignalCompleteness)
	}
	for _, id := range []string{"activation", "retained_use", "integration_proof", "benchmark", "audit", "learning"} {
		if !p711TestHasSignal(reparsed.Signals, id) {
			t.Fatalf("export missing signal %q: %#v", id, reparsed.Signals)
		}
	}
	if got := p711TestSignal(reparsed.Signals, "activation"); got.Status != "pass" || got.Counts["candidate_files"] == 0 {
		t.Fatalf("activation signal should summarize quickstart evidence: %#v", got)
	}
	if got := p711TestSignal(reparsed.Signals, "retained_use"); got.Status != "pass" || got.Counts["adoption_signals"] == 0 {
		t.Fatalf("retained-use signal should summarize validation records: %#v", got)
	}
	if linkage := p711TestReleaseGate(reparsed.ReleaseGateLinkage, "P9.7"); linkage.Status != "evidence_present" {
		t.Fatalf("P9.7 linkage should require and accept complete primary-ICP demand evidence: %#v", linkage)
	} else if linkage.Counts["complete_records"] != 9 || linkage.Counts["primary_icp_records"] != 3 ||
		linkage.Counts["daily_user_activation_records"] != 3 || linkage.Counts["retained_use_records"] == 0 ||
		len(linkage.EvidenceIDs) != 3 || len(linkage.Blockers) != 0 {
		t.Fatalf("P9.7 linkage should expose structured counts and evidence IDs: %#v", linkage)
	}
	if reparsed.Status != "pass" {
		t.Fatalf("top-level status should reflect complete signal evidence, got %q", reparsed.Status)
	}
	integration := p711TestSignal(reparsed.Signals, "integration_proof")
	if integration.Status != "pass" ||
		integration.Counts["mcp_stdio_parity_paths"] != 1 ||
		!stringSliceContains(integration.EvidenceIDs, "p7.8-mcp-stdio") {
		t.Fatalf("integration signal should include executable MCP stdio parity: %#v", integration)
	}
	if linkage := p711TestReleaseGate(reparsed.ReleaseGateLinkage, "P9.8"); linkage.Status != "evidence_present" {
		t.Fatalf("P9.8 linkage should consume integration proof parity: %#v", reparsed.ReleaseGateLinkage)
	} else if linkage.Counts["executable_required_paths"] != 3 ||
		linkage.Counts["mcp_stdio_parity_paths"] != 1 ||
		len(linkage.Blockers) != 0 ||
		!stringSliceContains(linkage.EvidenceIDs, "p7.8-mcp-stdio") {
		t.Fatalf("P9.8 linkage should expose structured counts and evidence IDs: %#v", linkage)
	}
	if linkage := p711TestReleaseGate(reparsed.ReleaseGateLinkage, "P9.10"); linkage.Status != "evidence_present" {
		t.Fatalf("P9.10 linkage should consume external competitive benchmark coverage: %#v", linkage)
	} else if linkage.Counts["required_external_baselines"] != 2 ||
		linkage.Counts["covered_external_baselines"] != 2 ||
		len(linkage.Blockers) != 0 ||
		!stringSliceContains(linkage.EvidenceIDs, "competitive_baseline:aider_repo_map") {
		t.Fatalf("P9.10 linkage should expose structured counts and evidence IDs: %#v", linkage)
	}
	if got := p711TestSignal(reparsed.Signals, "learning"); got.Status != "pass" || got.Counts["accepted"] == 0 {
		t.Fatalf("learning signal should summarize sanitized ranker learning: %#v", got)
	}
	if reparsed.Privacy.MetadataOnly != true || reparsed.Privacy.ForbiddenFieldCheck != "pass" ||
		reparsed.Privacy.RawPromptCaptured || reparsed.Privacy.RawFileBodyCaptured ||
		reparsed.Privacy.SecretCaptured || reparsed.Privacy.PrivatePathCaptured {
		t.Fatalf("privacy contract should be safe by default: %#v", reparsed.Privacy)
	}
	if finding := localops.ForbiddenEvidenceLeak(string(body)); finding != "" {
		t.Fatalf("product evidence export leaked forbidden evidence class %q: %s", finding, string(body))
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
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("product evidence export leaked %q: %s", forbidden, string(body))
		}
	}
	assertP711CoverageForEveryClassification(t, reparsed.ClassCoverage, 1)
}

func TestProductEvidenceSummarizesRecoveryRollups(t *testing.T) {
	root := t.TempDir()
	packet := stageProductEvidenceRecoveryPacket(t, root, "open-recovery")

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal product evidence: %v", err)
	}
	if report.Recovery.Status != "partial" ||
		report.Recovery.FailureRecordCount != 1 ||
		report.Recovery.DecompositionPacketCount != 1 ||
		report.Recovery.OpenPacketCount != 1 ||
		report.Recovery.CandidateAtomCount != len(packet.CandidateAtoms) ||
		report.Recovery.RemainingAtomCount != len(packet.CandidateAtoms) {
		t.Fatalf("expected open recovery rollup, got %#v", report.Recovery)
	}
	if report.Recovery.RedactionStatus != "redacted" || !report.Recovery.MetadataOnly || report.Recovery.ForbiddenFieldCheck != "pass" {
		t.Fatalf("recovery rollup should remain metadata-only/redacted, got %#v", report.Recovery)
	}
	if report.Recovery.FailureClasses[recovery.FailureClassExecutorEdit] != 1 ||
		report.Recovery.NextActions[recovery.NextActionReviewDecomposition] != 1 {
		t.Fatalf("recovery rollup missing failure class or next action: %#v", report.Recovery)
	}
	if ref := p711TestSourceRef(report.SourceReferences, "recovery_artifacts"); ref.Status != "present" {
		t.Fatalf("recovery source reference missing: %#v", report.SourceReferences)
	}
	if strings.Contains(string(body), "executor failed while editing a private branch") {
		t.Fatalf("product evidence leaked recovery failure summary: %s", string(body))
	}
}

func TestProductEvidenceBlocksInvalidRecoveryEvidence(t *testing.T) {
	root := t.TempDir()
	packet := stageProductEvidenceRecoveryPacket(t, root, "stale-reconciliation")
	reconciliationPath := filepath.Join(root, ".idiom", "recovery", packet.FailureID, "reconciliation.json")
	if err := os.WriteFile(reconciliationPath, []byte(`{"schema_version":"wrong"}`), 0o644); err != nil {
		t.Fatalf("write invalid reconciliation: %v", err)
	}

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	if report.Status != "blocked" || report.Recovery.Status != "blocked" || report.Recovery.MalformedArtifactCount != 1 {
		t.Fatalf("invalid recovery reconciliation should block product evidence, got status=%q recovery=%#v", report.Status, report.Recovery)
	}
	if !p711TestBlockerContains(report.Recovery.Blockers, "reconciliation_decode_failed") {
		t.Fatalf("expected invalid reconciliation blocker, got %#v", report.Recovery.Blockers)
	}
}

func TestP711ProductEvidenceKeepsActivationUsefulWithoutOverclaimingP96(t *testing.T) {
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
	localOps.Quickstart.Cleanup.Mode = "remove-smoke-artifacts"
	localOps.Quickstart.Cleanup.RemainingState = "context_graph_retained_by_policy"
	localOps.Quickstart.Cleanup.RetainedStatePolicy = "quickstart smoke retains local context graph by policy"
	writeP711JSON(t, localOpsPath, localOps)

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:    root,
		LocalOpsReport: localOpsPath,
	})
	activation := p711TestSignal(report.Signals, "activation")
	if activation.Status != "pass" || activation.Completeness != "complete" || activation.Counts["candidate_files"] == 0 {
		t.Fatalf("source-build quickstart activation should remain useful product evidence: %#v", activation)
	}
	if report.ProductSuccessGates.Activation != "evidence_present" {
		t.Fatalf("product activation signal should remain present without being release-ready: %#v", report.ProductSuccessGates)
	}
	if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.6"); linkage.Status != "needs_evidence" {
		t.Fatalf("P9.6 linkage must not overclaim source-build deterministic smoke: %#v", report.ReleaseGateLinkage)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal product evidence: %v", err)
	}
	if forbidden := localops.ForbiddenEvidenceLeak(string(body)); forbidden != "" {
		t.Fatalf("product evidence export leaked forbidden evidence class %q: %s", forbidden, string(body))
	}
}

func TestP96ProductEvidenceStrictActivationWithoutValidationRecordsNeedsEvidence(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:    root,
		LocalOpsReport: localOpsPath,
	})
	activation := p711TestSignal(report.Signals, "activation")
	if activation.Status != "pass" || activation.Completeness != "complete" {
		t.Fatalf("fixture should keep activation useful while validation records are absent: %#v", activation)
	}
	linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.6")
	if linkage.Status != "needs_evidence" {
		t.Fatalf("standalone product export must not close P9.6 without current validation proof: %#v", linkage)
	}
	if !strings.Contains(linkage.Evidence, "cannot close P9.6") {
		t.Fatalf("P9.6 linkage should explain standalone product limitation: %#v", linkage)
	}
}

func TestP96ProductEvidenceZeroRankedPacketDoesNotPassActivationOrP96(t *testing.T) {
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
	localOps.Quickstart.FirstUsefulPacket.CandidateFileCount = 0
	localOps.Quickstart.FirstUsefulPacket.CandidateSymbolCount = 0
	localOps.Quickstart.FirstUsefulPacket.CandidateTestCount = 0
	localOps.Quickstart.FirstUsefulPacket.UsefulEvidence = []string{"candidate_files:0", "candidate_symbols:0", "candidate_tests:0"}
	writeP711JSON(t, localOpsPath, localOps)

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:    root,
		LocalOpsReport: localOpsPath,
	})
	activation := p711TestSignal(report.Signals, "activation")
	if activation.Status == "pass" || activation.Completeness == "complete" {
		t.Fatalf("zero-ranked packet evidence must not pass activation signal: %#v", activation)
	}
	if activation.Counts["useful_evidence_count"] != 0 {
		t.Fatalf("zero-ranked packet should not report useful evidence counts: %#v", activation.Counts)
	}
	if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.6"); linkage.Status == "evidence_present" {
		t.Fatalf("zero-ranked packet evidence must not close P9.6: %#v", linkage)
	}
}

func TestP911ProductEvidenceCurrentTranscriptFixturePasses(t *testing.T) {
	repoRoot := p711TestRepoRoot(t)
	report := buildProductEvidenceExportReport(repoRoot, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: repoRoot})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
	if gate.Status != "evidence_present" {
		t.Fatalf("current P9.11 fixture should pass runnable demo gate: %#v", gate)
	}
	if gate.Counts["covered_classifications"] != 7 || gate.Counts["runnable_command_rows"] != 7 ||
		gate.Counts["expected_output_rows"] != 7 || gate.Counts["blockers"] != 0 {
		t.Fatalf("P9.11 current fixture should expose complete counts: %#v", gate)
	}
	for _, id := range []string{"p7.10_client_demo_transcripts", "p7.10-enterprise-saas-source-sanity"} {
		if !stringSliceContains(gate.EvidenceIDs, id) {
			t.Fatalf("P9.11 evidence IDs missing %q: %#v", id, gate)
		}
	}
}

func TestP911ProductEvidenceMissingAndDuplicateClassificationFailClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")
	var set map[string]any
	p711ReadTestJSON(t, path, &set)
	transcripts := set["transcripts"].([]any)
	set["transcripts"] = append(transcripts[1:], transcripts[1])
	writeP711JSON(t, path, set)

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
	if gate.Status != "needs_evidence" || gate.Counts["missing_classifications"] == 0 || gate.Counts["duplicate_classifications"] == 0 {
		t.Fatalf("P9.11 must fail closed on missing and duplicate classifications: %#v", gate)
	}
	for _, want := range []string{"missing client demo classification", "duplicate client demo classification"} {
		if !p711TestBlockerContains(gate.Blockers, want) {
			t.Fatalf("P9.11 blocker %q not exposed: %#v", want, gate)
		}
	}
}

func TestP911ProductEvidenceRequiredTranscriptFieldsFailClosed(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			p911MutateFirstTranscript(t, root, tc.mutate)
			report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			if gate.Status != "needs_evidence" {
				t.Fatalf("P9.11 must fail closed for %s: %#v", tc.name, gate)
			}
			if !p711TestBlockerContains(gate.Blockers, tc.wantBlocker) {
				t.Fatalf("P9.11 blocker %q not exposed: %#v", tc.wantBlocker, gate)
			}
		})
	}
}

func TestP911ProductEvidenceRejectsFrameworkSpecificRegulatedReadinessOverclaims(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			p911MutateTranscriptByClass(t, root, tc.class, func(row map[string]any) {
				row["release_claim_impact"] = "This demo is " + tc.phrase + " for regulated review."
			})

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			if gate.Status != "needs_evidence" || gate.Counts["regulated_or_commercial_overclaims"] == 0 {
				t.Fatalf("P9.11 must fail closed for %q: %#v", tc.phrase, gate)
			}
			if !p711TestBlockerContains(gate.Blockers, "unsupported regulated or commercial readiness") ||
				!p711TestBlockerContains(gate.Blockers, strings.ToLower(tc.phrase)) {
				t.Fatalf("P9.11 regulated-readiness blocker should name %q: %#v", tc.phrase, gate)
			}
		})
	}
}

func TestP911ProductEvidenceAllowsPhraseLocalApprovalNegations(t *testing.T) {
	for _, phrase := range []string{
		"no FedRAMP approval claim",
		"FedRAMP approval remains no-go",
		"no HIPAA approved claim",
		"NIST approval remains no-go until reviewed",
	} {
		t.Run(p711SafeID(phrase, "phrase"), func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			p911MutateTranscriptByClass(t, root, "public-sector", func(row map[string]any) {
				row["release_claim_impact"] = "Proof-only demo limitation: " + phrase + "."
			})

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			if gate.Counts["regulated_or_commercial_overclaims"] != 0 ||
				p711TestBlockerContains(gate.Blockers, "unsupported regulated or commercial readiness") {
				t.Fatalf("P9.11 should allow phrase-local approval negation %q: %#v", phrase, gate)
			}
		})
	}
}

func TestP911ProductEvidenceRequiresExecutableCommandValidation(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			p911MutateTranscriptByClass(t, root, "enterprise-saas", tc.mutate)

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
			if gate.Status != "needs_evidence" || gate.Counts["runnable_command_rows"] == 7 || gate.Counts[tc.wantCount] == 0 {
				t.Fatalf("P9.11 must fail closed for %s: %#v", tc.name, gate)
			}
			if !p711TestBlockerContains(gate.Blockers, tc.wantBlocker) {
				t.Fatalf("P9.11 blocker %q not exposed: %#v", tc.wantBlocker, gate)
			}
		})
	}
}

func TestP911ProductAndReleaseEvidenceRejectUnsafeCommandAfterSafeReplay(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	p911MutateTranscriptByClass(t, root, "enterprise-saas", func(row map[string]any) {
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
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())

	product := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	release := buildReleaseEvidenceBundle(root, LaneHistorySummary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root, CurrentDir: "current"})
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
		if gate.Status != "needs_evidence" || gate.Counts["runnable_command_rows"] == 7 ||
			gate.Counts["declared_command_rows"] != 8 || gate.Counts["command_execution_blockers"] == 0 {
			t.Fatalf("%s P9.11 must reject unsafe command appended after safe replay: %#v", label, gate)
		}
		if !p711TestBlockerContains(gate.Blockers, "unsafe top-level subcommand") {
			t.Fatalf("%s P9.11 unsafe appended-command blocker not exposed: %#v", label, gate)
		}
	}
}

func TestP911DiscoveryProfileDeniedSideEffectReplayDoesNotWrite(t *testing.T) {
	root := t.TempDir()
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
		t.Fatalf("discovery-profile denied side-effect command should satisfy safe shape: %#v", blockers)
	}
	if blockers := p911ExecuteDemoCommandAtRoot(root, demo, command); len(blockers) != 0 {
		t.Fatalf("discovery-profile denied side-effect replay should pass as denied evidence: %#v", blockers)
	}
	if _, err := os.Stat(filepath.Join(root, "network-side-effect.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied discovery-profile shell replay created side-effect marker, stat err=%v", err)
	}
}

func TestP911ProductEvidenceMalformedTranscriptArtifactFailsClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript fixture: %v", err)
	}
	data = bytes.Replace(data, []byte(`"transcripts":`), []byte(`"unexpected_field": true, "transcripts":`), 1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write malformed transcript fixture: %v", err)
	}

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
	if gate.Status != "needs_evidence" || gate.Counts["malformed_transcripts"] == 0 {
		t.Fatalf("P9.11 must fail closed on malformed transcript artifact: %#v", gate)
	}
	if !p711TestBlockerContains(gate.Blockers, "artifact is malformed") {
		t.Fatalf("P9.11 malformed-artifact blocker not exposed: %#v", gate)
	}
}

func TestP911ProductEvidenceOverclaimFailsClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	p911MutateTranscriptByClass(t, root, "healthcare", func(row map[string]any) {
		row["release_claim_impact"] = "HIPAA certification and regulated sales readiness are supported by this demo."
	})
	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.11")
	if gate.Status != "needs_evidence" || gate.Counts["regulated_or_commercial_overclaims"] == 0 {
		t.Fatalf("P9.11 must fail closed on regulated/commercial overclaim: %#v", gate)
	}
	if !p711TestBlockerContains(gate.Blockers, "unsupported regulated or commercial readiness") {
		t.Fatalf("P9.11 overclaim blocker not exposed: %#v", gate)
	}
}

func TestP911ProductAndReleaseEvidenceStructuredStatusAgree(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())
	product := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	release := buildReleaseEvidenceBundle(root, LaneHistorySummary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root, CurrentDir: "current"})
	productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.11")
	releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.11")
	if productGate.Status != "evidence_present" || releaseGate.Status != productGate.Status {
		t.Fatalf("product/release P9.11 readiness should agree for complete fixture: product=%#v release=%#v", productGate, releaseGate)
	}
	for _, key := range []string{"required_classifications", "covered_classifications", "runnable_command_rows", "expected_output_rows", "blockers"} {
		if productGate.Counts[key] != releaseGate.Counts[key] {
			t.Fatalf("product/release P9.11 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate)
		}
	}
	if strings.Join(productGate.Blockers, "|") != strings.Join(releaseGate.Blockers, "|") ||
		strings.Join(productGate.EvidenceIDs, "|") != strings.Join(releaseGate.EvidenceIDs, "|") {
		t.Fatalf("product/release P9.11 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate)
	}
}

func TestP910ProductEvidenceNeedsExternalCompetitiveBaselines(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	path := writeP910CompetitiveBenchmarkReport(t, root, []map[string]any{p910MeasuredCondition("aider_repo_map")})

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:     root,
		BenchmarkReport: path,
	})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.10")
	if gate.Status != "needs_evidence" || gate.Counts["missing_external_baselines"] != 1 {
		t.Fatalf("P9.10 should fail closed when an external baseline is missing: %#v", gate)
	}
	if !p711TestBlockerContains(gate.Blockers, "missing external competitive baseline") {
		t.Fatalf("P9.10 missing-baseline blocker not exposed: %#v", gate)
	}
}

func TestP910ProductEvidenceRejectsSelfReferentialOnlyEvidence(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	path := writeP910CompetitiveBenchmarkReport(t, root, []map[string]any{
		p910MeasuredCondition("raw_search_read"),
		p910MeasuredCondition("context_pack"),
	})

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:     root,
		BenchmarkReport: path,
	})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.10")
	if gate.Status != "needs_evidence" || gate.Counts["covered_external_baselines"] != 0 {
		t.Fatalf("P9.10 should reject self-referential-only benchmark evidence: %#v", gate)
	}
	if !p711TestBlockerContains(gate.Blockers, "self-referential only") {
		t.Fatalf("P9.10 self-referential blocker not exposed: %#v", gate)
	}
}

func TestP910ProductAndReleaseJSONIncludeZeroCountKeysForDefaultCorpus(t *testing.T) {
	root := t.TempDir()
	stageP910DefaultCorpusFixture(t, root)

	product := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
	release := buildReleaseEvidenceBundle(root, LaneHistorySummary{Total: 1}, releaseEvidenceOptions{ProjectRoot: root})

	productBody, err := json.Marshal(product)
	if err != nil {
		t.Fatalf("marshal product evidence: %v", err)
	}
	releaseBody, err := json.Marshal(release)
	if err != nil {
		t.Fatalf("marshal release evidence: %v", err)
	}

	productCounts := p910JSONCountsFromProduct(t, productBody)
	releaseCounts := p910JSONCountsFromReleaseReadiness(t, releaseBody)
	releaseCompetitiveCounts := p910JSONCountsFromReleaseCompetitiveCoverage(t, releaseBody)
	for _, counts := range []map[string]int{productCounts, releaseCounts, releaseCompetitiveCounts} {
		assertP910RequiredCountKeys(t, counts)
		for _, key := range []string{
			"covered_external_baselines",
			"measured_external_baselines",
			"documented_infeasible_external_baselines",
			"missing_external_baselines",
			"malformed_baseline_rows",
			"self_referential_baselines",
			"target_client_coverage_baselines",
		} {
			if counts[key] != 0 {
				t.Fatalf("default corpus P9.10 count %q should be explicit zero, got counts=%#v", key, counts)
			}
		}
	}
	if !reflect.DeepEqual(productCounts, releaseCounts) || !reflect.DeepEqual(productCounts, releaseCompetitiveCounts) {
		t.Fatalf("P9.10 product/release JSON counts diverged: product=%#v release=%#v competitive=%#v", productCounts, releaseCounts, releaseCompetitiveCounts)
	}
}

func TestP910ProductEvidenceRejectsMalformedBaselineRow(t *testing.T) {
	assertP910ConditionNeedsEvidence(t,
		p910MeasuredCondition("unsupported_tool"),
		"malformed or unsupported competitive baseline row",
	)
}

func TestP910ProductEvidenceRejectsUnsupportedClientClassification(t *testing.T) {
	assertP910ConditionNeedsEvidence(t,
		p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
			"invalid_client_classifications": 1,
		}),
		"unsupported client classifications",
	)
}

func TestP910ProductEvidenceRejectsMissingCommandID(t *testing.T) {
	assertP910ConditionNeedsEvidence(t,
		p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
			"commanded_tasks": 0,
		}),
		"missing command ID evidence",
	)
}

func TestP910ProductEvidenceRejectsMissingArtifactEvidence(t *testing.T) {
	assertP910ConditionNeedsEvidence(t,
		p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
			"artifacted_tasks": 0,
		}),
		"missing artifact evidence",
	)
}

func TestP910ProductEvidenceRejectsMissingVerificationMethod(t *testing.T) {
	assertP910ConditionNeedsEvidence(t,
		p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
			"verified_tasks": 0,
		}),
		"missing verification method evidence",
	)
}

func TestP910ProductEvidenceRejectsNoGoStatus(t *testing.T) {
	assertP910ConditionNeedsEvidence(t,
		p910ConditionPatch(p910MeasuredCondition("aider_repo_map"), map[string]any{
			"status": "no_go",
		}),
		"stale, no-go, incomplete, or unsupported status",
	)
}

func TestP910ProductEvidenceRejectsDocumentedInfeasibilityMissingClaimImpact(t *testing.T) {
	assertP910ConditionNeedsEvidence(t,
		p910ConditionPatch(p910InfeasibleCondition("aider_repo_map"), map[string]any{
			"claim_impacts": map[string]int{},
		}),
		"claim impact missing",
	)
}

func assertP910ConditionNeedsEvidence(t *testing.T, condition map[string]any, wantBlocker string) {
	t.Helper()
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	path := writeP910CompetitiveBenchmarkReport(t, root, []map[string]any{
		condition,
		p910InfeasibleCondition("external_agent_native"),
	})

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:     root,
		BenchmarkReport: path,
	})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.10")
	if gate.Status != "needs_evidence" {
		t.Fatalf("P9.10 should fail closed: %#v", gate)
	}
	if !p711TestBlockerContains(gate.Blockers, wantBlocker) {
		t.Fatalf("P9.10 blocker %q not exposed: %#v", wantBlocker, gate)
	}
}

func TestP910ProductAndReleaseEvidenceStructuredStatusAgree(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())

	product := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	})
	release := buildReleaseEvidenceBundle(root, LaneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	})
	productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.10")
	releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.10")
	if productGate.Status != releaseGate.Status ||
		productGate.Counts["covered_external_baselines"] != releaseGate.Counts["covered_external_baselines"] ||
		len(productGate.Blockers) != len(releaseGate.Blockers) {
		t.Fatalf("P9.10 product/release structured fields diverged: product=%#v release=%#v", productGate, releaseGate)
	}
	if release.BenchmarkLocalOps.CompetitiveCoverage.Status != "evidence_present" {
		t.Fatalf("release benchmark section should carry P9.10 coverage: %#v", release.BenchmarkLocalOps.CompetitiveCoverage)
	}
}

func TestRunProductEvidenceExportWritesParseableJSONOutput(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	outputPath := filepath.Join(root, ".idiom", "product-evidence", "export.json")

	var stdout, stderr bytes.Buffer
	code := runProductEvidenceMainWithTelemetry([]string{
		"export",
		"--project-root", root,
		"--local-ops-report", localOpsPath,
		"--validation-records", recordsPath,
		"--output", outputPath,
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("product evidence export failed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read product evidence output: %v", err)
	}
	var report productEvidenceExportReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("output JSON should parse: %v\n%s", err, string(data))
	}
	if report.ExportID == "" || report.ProductSuccessGates.LocalProductEvidenceExport != "pass" {
		t.Fatalf("output missing export identity/gate linkage: %#v", report)
	}
	if strings.Contains(stdout.String(), outputPath) || strings.Contains(stderr.String(), outputPath) {
		t.Fatalf("CLI output should not echo raw output path stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestP810IntegrationProofRequiresDistinctExecutablePaths(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			writeP711IntegrationProofs(t, root, tc.proofs)

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
			integration := p711TestSignal(report.Signals, "integration_proof")
			if integration.Status != "partial" || integration.Completeness != "partial" {
				t.Fatalf("incomplete integration proof should stay partial: %#v", integration)
			}
			if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.8"); linkage.Status != "needs_evidence" {
				t.Fatalf("P9.8 should need evidence for incomplete integration paths: %#v", report.ReleaseGateLinkage)
			} else if len(linkage.Blockers) == 0 {
				t.Fatalf("P9.8 linkage should expose blockers for incomplete proofs: %#v", linkage)
			}
			if report.ProductSuccessGates.IntegrationProof != "needs_evidence" {
				t.Fatalf("product integration gate should need evidence, got %q", report.ProductSuccessGates.IntegrationProof)
			}
		})
	}
}

func TestP811IntegrationProofRejectsMalformedRows(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			writeP711IntegrationProofs(t, root, tc.proofs)

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
			assertP811IntegrationNeedsEvidence(t, report)
			integration := p711TestSignal(report.Signals, "integration_proof")
			if integration.Counts["mcp_stdio_parity_paths"] != tc.wantMCPParityPaths {
				t.Fatalf("unexpected MCP parity count for malformed proof rows: %#v", integration)
			}
			if integration.Counts["executable_required_path_shell_ci_script"] != tc.wantShellRequiredPathCount {
				t.Fatalf("malformed shell/CI row must not satisfy required-path count: %#v", integration)
			}
		})
	}
}

func TestP812IntegrationProofRequiresExplicitSafePrivacySemantics(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			writeP711IntegrationProofs(t, root, tc.proofs)

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
			integration := p711TestSignal(report.Signals, "integration_proof")
			if integration.Status != tc.wantSignalStatus {
				t.Fatalf("unexpected integration signal status: got %q want %q: %#v", integration.Status, tc.wantSignalStatus, integration)
			}
			if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.8"); linkage.Status != tc.wantP98Status {
				t.Fatalf("unexpected P9.8 status: got %q want %q: %#v", linkage.Status, tc.wantP98Status, report.ReleaseGateLinkage)
			}
			if report.ProductSuccessGates.IntegrationProof == "evidence_present" {
				t.Fatalf("privacy-invalid integration proof must not produce evidence_present gate")
			}
			if integration.Counts["mcp_stdio_parity_paths"] != tc.wantMCPParityPaths {
				t.Fatalf("unexpected MCP parity count for privacy-invalid rows: %#v", integration)
			}
			if integration.Counts["missing_privacy"] != tc.wantMissingPrivacy {
				t.Fatalf("unexpected missing privacy count: %#v", integration)
			}
			if integration.Counts["incomplete_privacy"] != tc.wantIncompletePrivacy {
				t.Fatalf("unexpected incomplete privacy count: %#v", integration)
			}
			if integration.Counts["privacy_blockers"] != tc.wantPrivacyBlockers {
				t.Fatalf("unexpected privacy blocker count: %#v", integration)
			}
		})
	}
}

func TestP718ValidationRecordsRejectUnknownAndForbiddenEvidence(t *testing.T) {
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
				"note":                  "/Users/ben/Documents/GitHub/CustomerX-private-repo secret token=abc123 logs/telemetry/session.jsonl",
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			path := filepath.Join(root, "validation-records.json")
			writeP711JSON(t, path, tc.records)

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{
				ProjectRoot:       root,
				ValidationRecords: path,
			})
			if report.Status != "blocked" || report.Privacy.ForbiddenFieldCheck != "failed:validation_records" {
				t.Fatalf("unsafe validation records should block export evidence, got status=%q privacy=%#v", report.Status, report.Privacy)
			}
			if p711TestSourceRef(report.SourceReferences, "p0.8_validation_records").Status != "blocked" {
				t.Fatalf("validation record source should be blocked: %#v", report.SourceReferences)
			}
			if got := p711TestSignal(report.Signals, "retained_use"); got.Counts["validation_records"] != 0 || got.Status == "pass" {
				t.Fatalf("blocked validation records must not be counted: %#v", got)
			}
			body, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("marshal report: %v", err)
			}
			for _, forbidden := range []string{"RAW_PROMPT_BODY", "func LeakedBody", "/Users/", "CustomerX", "token=abc123", "logs/telemetry"} {
				if strings.Contains(string(body), forbidden) {
					t.Fatalf("blocked export leaked %q: %s", forbidden, string(body))
				}
			}
		})
	}
}

func TestP719ValidationRecordsRejectEmbeddedAbsolutePathsInAllowedFields(t *testing.T) {
	for _, tc := range []struct {
		name                string
		retainedUseStatus   string
		adoptionSignalType  string
		forbiddenSubstrings []string
	}{
		{
			name:                "repo_equals_users_path",
			retainedUseStatus:   "observed",
			adoptionSignalType:  "repo=/Users/ben/Documents/GitHub/acme",
			forbiddenSubstrings: []string{"/Users/ben/Documents/GitHub/acme"},
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			path := filepath.Join(root, "validation-records.json")
			writeP711JSON(t, path, []map[string]any{{
				"record_id":             "record-enterprise-saas",
				"client_classification": "enterprise-saas",
				"evidence_status":       "complete",
				"retained_use_status":   tc.retainedUseStatus,
				"adoption_signal_type":  tc.adoptionSignalType,
				"redaction_status":      "redacted",
			}})

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{
				ProjectRoot:       root,
				ValidationRecords: path,
			})
			if report.Status != "blocked" || report.Privacy.RedactionStatus != "blocked" || report.Privacy.ForbiddenFieldCheck != "failed:validation_records" {
				t.Fatalf("embedded absolute path should machine-block validation records, status=%q privacy=%#v", report.Status, report.Privacy)
			}
			if p711TestSourceRef(report.SourceReferences, "p0.8_validation_records").Status != "blocked" {
				t.Fatalf("validation record source should be blocked: %#v", report.SourceReferences)
			}
			retained := p711TestSignal(report.Signals, "retained_use")
			for _, key := range []string{"validation_records", "qualifying_records", "retained_use_observed", "adoption_signals"} {
				if retained.Counts[key] != 0 {
					t.Fatalf("blocked validation records must not contribute %q counts: %#v", key, retained)
				}
			}
			for _, row := range report.ClassCoverage {
				if row.ValidationRecordCount != 0 {
					t.Fatalf("blocked validation records must not contribute class counts: %#v", row)
				}
			}
			body, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("marshal report: %v", err)
			}
			for _, forbidden := range tc.forbiddenSubstrings {
				if strings.Contains(string(body), forbidden) {
					t.Fatalf("blocked export leaked embedded path %q: %s", forbidden, string(body))
				}
			}
		})
	}
}

func TestP719AbsolutePathScannerDetectsEmbeddedSeparators(t *testing.T) {
	for _, value := range []string{
		"repo=/Users/ben/Documents/GitHub/acme",
		"path:/tmp/idiom-private",
		"/private/var/tmp/idiom-private",
		"/var/folders/zz/idiom-private",
	} {
		if !localops.ContainsAbsolutePath(value) {
			t.Fatalf("expected embedded absolute path to be detected in %q", value)
		}
	}
	if localops.ContainsAbsolutePath("docs=https://example.com/idiom/path") {
		t.Fatalf("http URLs should not be treated as local absolute paths")
	}
}

func TestP718RetainedUseCountsOnlyQualifyingRecords(t *testing.T) {
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
	if retained.Status != "pass" || retained.Counts["retained_use_observed"] != 1 || retained.Counts["adoption_signals"] != 1 {
		t.Fatalf("retained-use signal should count only qualifying records: %#v", retained)
	}
	if retained.Counts["qualifying_records"] != 1 || retained.Counts["non_qualifying_records"] != 2 {
		t.Fatalf("unexpected qualifying counts: %#v", retained.Counts)
	}
	if strings.Join(retained.EvidenceIDs, ",") != "record-good" {
		t.Fatalf("non-qualifying record IDs should not be exported as evidence: %#v", retained.EvidenceIDs)
	}

	rows := buildP711ClassCoverage(p711DemoTranscriptSet{}, p711SecurityPosture{}, p711IntegrationProofSet{}, records)
	for _, row := range rows {
		switch row.Classification {
		case "enterprise-saas":
			if row.ValidationRecordCount != 1 {
				t.Fatalf("qualifying class should be counted: %#v", row)
			}
		case "open-source":
			if row.ValidationRecordCount != 0 {
				t.Fatalf("disqualified class should not be counted: %#v", row)
			}
		}
	}
}

func TestP718AbsentAdoptionSignalsAreNotCounted(t *testing.T) {
	for _, absent := range []string{"", "none", "not_available", "not-applicable", "n/a", "N/A"} {
		t.Run(absent, func(t *testing.T) {
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
			if retained.Counts["adoption_signals"] != 0 {
				t.Fatalf("absent adoption signal %q should not be counted: %#v", absent, retained)
			}
		})
	}
}

func TestP97ProductEvidenceMissingValidationRecordsNeedsEvidence(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot: root,
	})
	linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.7")
	if linkage.Status != "needs_evidence" {
		t.Fatalf("missing validation records must keep P9.7 needs_evidence: %#v", linkage)
	}
	if !strings.Contains(linkage.Evidence, "complete:0") {
		t.Fatalf("P9.7 linkage should expose missing complete-record count: %#v", linkage)
	}
	if linkage.Counts["complete_records"] != 0 || len(linkage.Blockers) == 0 {
		t.Fatalf("P9.7 linkage should expose structured missing-evidence counts and blockers: %#v", linkage)
	}
}

func TestP97ProductEvidenceRetainedUseWithoutCredibleAdoptionDoesNotPass(t *testing.T) {
	records := p97PrimaryRecords("none", "none")
	gate := p97ValidationGate(records)
	if gate.Status == "evidence_present" {
		t.Fatalf("retained use without credible paid/internal-platform signal must not satisfy P9.7: %#v", gate)
	}
	if gate.Counts["retained_use_records"] == 0 || gate.Counts["credible_primary_adoption_signals"] != 0 {
		t.Fatalf("fixture should isolate missing adoption signal: %#v", gate)
	}
}

func TestP97ProductEvidenceOpenSourceRecordsDoNotCountAsPrimaryPaidDemand(t *testing.T) {
	var records []p711ValidationRecord
	for i := 0; i < 5; i++ {
		record := p97Record("open-source", fmt.Sprintf("oss-%d", i), "context-oss")
		record.AdoptionSignalType = "paid_pilot"
		record.BuyerBudgetSignal = "budget_owner"
		record.AdoptPaySponsorScore = 3
		records = append(records, record)
	}
	gate := p97ValidationGate(records)
	if gate.Status == "evidence_present" || gate.Counts["primary_icp_records"] != 0 {
		t.Fatalf("open-source records must not replace enterprise-saas paid demand: %#v", gate)
	}
	if !strings.Contains(strings.Join(gate.Blockers, "|"), "open-source/supporting records") {
		t.Fatalf("expected open-source demand blocker: %#v", gate.Blockers)
	}
}

func TestP97ProductEvidenceUnsafeDisqualifiedWithdrawnRecordsDoNotCount(t *testing.T) {
	records := p97PrimaryRecords("paid_pilot", "budget_owner")
	records[0].EvidenceStatus = "disqualified"
	records[1].EvidenceStatus = "withdrawn"
	records[2].RedactionStatus = "fail"
	gate := p97ValidationGate(records)
	if gate.Status == "evidence_present" {
		t.Fatalf("unsafe/disqualified/withdrawn records must fail closed: %#v", gate)
	}
	if gate.Counts["non_qualifying_records"] != 3 || gate.Counts["primary_icp_records"] != 0 {
		t.Fatalf("non-qualifying records should not count toward primary ICP: %#v", gate.Counts)
	}
}

func TestP97ProductEvidenceRetainedUseRequiresSubstrateValue(t *testing.T) {
	for _, value := range []string{"demo_liked", "chat_ui_interest", "failed"} {
		t.Run(value+"_evidence", func(t *testing.T) {
			records := p97PrimaryRecords("paid_pilot", "budget_owner")
			records[0].RetainedUseEvidence = value
			records[1].RetainedUseEvidence = value
			records[2].RetainedUseEvidence = value
			records[3].RetainedUseEvidence = value
			records[4].RetainedUseEvidence = value
			gate := p97ValidationGate(records)
			if gate.Status == "evidence_present" || gate.Counts["retained_use_records"] != 0 {
				t.Fatalf("non-substrate retained-use evidence %q must not satisfy P9.7: %#v", value, gate)
			}
		})
		t.Run(value+"_status", func(t *testing.T) {
			records := p97PrimaryRecords("paid_pilot", "budget_owner")
			records[0].SubstrateValueStatus = value
			records[1].SubstrateValueStatus = value
			records[2].SubstrateValueStatus = value
			records[3].SubstrateValueStatus = value
			records[4].SubstrateValueStatus = value
			gate := p97ValidationGate(records)
			if gate.Status == "evidence_present" || gate.Counts["retained_use_records"] != 0 {
				t.Fatalf("non-substrate value status %q must not satisfy P9.7: %#v", value, gate)
			}
		})
	}
}

func TestP97ProductEvidenceDailyUserActivationRequiresValidStatus(t *testing.T) {
	for _, status := range []string{"pass", "observed", "attempted", "completed", "succeeded"} {
		t.Run("accepts_"+status, func(t *testing.T) {
			records := p97PrimaryRecords("paid_pilot", "budget_owner")
			for i := range records {
				records[i].DailyUserActivationStatus = status
			}
			gate := p97ValidationGate(records)
			if gate.Counts["daily_user_activation_records"] != 3 {
				t.Fatalf("valid daily-user activation status %q should count for primary records: %#v", status, gate)
			}
		})
	}
	for _, status := range []string{"failed", "blocked", "interest_only"} {
		t.Run("rejects_"+status, func(t *testing.T) {
			records := p97PrimaryRecords("paid_pilot", "budget_owner")
			for i := range records {
				records[i].DailyUserActivationStatus = status
			}
			gate := p97ValidationGate(records)
			if gate.Status == "evidence_present" || gate.Counts["daily_user_activation_records"] != 0 {
				t.Fatalf("invalid daily-user activation status %q must not satisfy P9.7: %#v", status, gate)
			}
		})
	}
}

func TestP97ProductEvidenceSufficientPrimaryICPRecordsCanPass(t *testing.T) {
	records := p97PrimaryRecords("paid_pilot", "budget_owner")
	gate := p97ValidationGate(records)
	if gate.Status != "evidence_present" {
		t.Fatalf("sufficient primary ICP retained-use adoption evidence should satisfy P9.7: %#v", gate)
	}
	if gate.Counts["complete_records"] != 5 || gate.Counts["primary_icp_records"] != 3 ||
		gate.Counts["primary_contexts"] != 2 || gate.Counts["retained_use_records"] == 0 ||
		gate.Counts["credible_primary_adoption_signals"] == 0 {
		t.Fatalf("unexpected P9.7 counts: %#v", gate.Counts)
	}
}

func TestP912ProductEvidenceCurrentValidThesisStatusAndCounts(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.12")
	if gate.Status != "evidence_present" {
		t.Fatalf("complete P0.11 thesis plus prior gate evidence should satisfy P9.12: %#v", gate)
	}
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
			t.Fatalf("P9.12 count %q missing: %#v", key, gate.Counts)
		}
	}
	if gate.Counts["required_fields_present"] != gate.Counts["required_fields"] ||
		gate.Counts["classification_stances_covered"] != 7 ||
		gate.Counts["classification_stances_with_evidence"] != 7 ||
		gate.Counts["unsupported_admin_request_count"] != 7 ||
		gate.Counts["privacy_blockers"] != 0 ||
		gate.Counts["admin_count_blockers"] != 0 ||
		gate.Counts["decision_continue"] != 1 ||
		gate.Counts["prior_gate_evidence_present"] != 5 ||
		gate.Counts["blockers"] != 0 {
		t.Fatalf("unexpected P9.12 counts: %#v", gate.Counts)
	}
	for _, id := range []string{"p0.11-packaging-support-thesis", "p0.11-icp", "p7.11-product-evidence-export", "p0.11-class-enterprise-saas"} {
		if !stringSliceContains(gate.EvidenceIDs, id) {
			t.Fatalf("P9.12 evidence IDs missing %q: %#v", id, gate.EvidenceIDs)
		}
	}
}

func TestP912ProductEvidenceMissingRequiredFieldFailsClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	p912MutateThesis(t, root, func(thesis map[string]any) {
		delete(thesis, "pricing")
	})
	gate := p912ProductGateForCompleteEvidence(t, root)
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "required packaging/support field missing or empty: pricing") {
		t.Fatalf("missing P9.12 required field must fail closed: %#v", gate)
	}
}

func TestP912ProductEvidenceBlankStructuredFieldFailsClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	p912MutateThesis(t, root, func(thesis map[string]any) {
		support := thesis["support"].(map[string]any)
		support["boundary"] = " "
	})
	gate := p912ProductGateForCompleteEvidence(t, root)
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "required packaging/support field missing or empty: support") {
		t.Fatalf("blank P9.12 structured field must fail closed: %#v", gate)
	}
}

func TestP912ProductEvidenceSupportAdminAndCommercialOverclaimsFailClosed(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			p912MutateThesis(t, root, tc.mutate)
			gate := p912ProductGateForCompleteEvidence(t, root)
			if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, tc.wantBlocker) {
				t.Fatalf("P9.12 overclaim %s must fail closed: %#v", tc.name, gate)
			}
		})
	}
}

func TestP912ProductEvidenceFullThesisPrivacyMarkersFailClosed(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantBlocker string
	}{
		{
			name: "support_boundary_private_path",
			mutate: func(thesis map[string]any) {
				support := thesis["support"].(map[string]any)
				support["boundary"] = "Support requires reproducer notes from /Users/ben/Documents/GitHub/CustomerX-private-repo."
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			p912MutateThesis(t, root, tc.mutate)
			gate := p912ProductGateForCompleteEvidence(t, root)
			if gate.Status != "needs_evidence" ||
				gate.Counts["privacy_blockers"] == 0 ||
				!p711TestBlockerContains(gate.Blockers, tc.wantBlocker) {
				t.Fatalf("P9.12 privacy marker %s must fail closed: %#v", tc.name, gate)
			}
		})
	}
}

func TestP912ProductEvidenceUnsupportedAdminCountMismatchFailsClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	p912MutateThesis(t, root, func(thesis map[string]any) {
		admin := thesis["admin"].(map[string]any)
		admin["unsupported_request_count"] = 99
	})
	gate := p912ProductGateForCompleteEvidence(t, root)
	if gate.Status != "needs_evidence" ||
		gate.Counts["unsupported_admin_request_count"] != 7 ||
		gate.Counts["admin_count_blockers"] != 1 ||
		!p711TestBlockerContains(gate.Blockers, "unsupported_admin_request_count mismatch: thesis=99 actual=7") {
		t.Fatalf("P9.12 unsupported-admin mismatch must fail closed with list-derived count: %#v", gate)
	}
}

func TestP912ProductEvidenceDecisionMissingOrUnsupportedFailsClosed(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			p912MutateThesis(t, root, tc.mutate)
			report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.12")
			if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, tc.wantBlocker) {
				t.Fatalf("P9.12 decision case %s must fail closed: %#v", tc.name, gate)
			}
		})
	}
}

func TestP912ProductEvidenceRequiresAllSevenClassificationStances(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	p912MutateThesis(t, root, func(thesis map[string]any) {
		rows := thesis["classification_stances"].([]any)
		thesis["classification_stances"] = rows[:len(rows)-1]
	})
	gate := p912ProductGateForCompleteEvidence(t, root)
	if gate.Status != "needs_evidence" ||
		gate.Counts["classification_stances_covered"] != 6 ||
		!p711TestBlockerContains(gate.Blockers, "missing packaging classification stance:") {
		t.Fatalf("P9.12 must require all seven classification stances: %#v", gate)
	}
}

func TestP912ProductAndReleaseEvidenceStructuredStatusAgree(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())

	product := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	})
	release := buildReleaseEvidenceBundle(root, LaneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	})
	productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.12")
	releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.12")
	if productGate.Status != releaseGate.Status ||
		productGate.ClaimImpact != releaseGate.ClaimImpact ||
		!reflect.DeepEqual(productGate.Counts, releaseGate.Counts) ||
		!reflect.DeepEqual(productGate.Blockers, releaseGate.Blockers) ||
		!reflect.DeepEqual(productGate.EvidenceIDs, releaseGate.EvidenceIDs) {
		t.Fatalf("product/release P9.12 structured gate data should agree: product=%#v release=%#v", productGate, releaseGate)
	}
}

func TestP718TopLevelStatusReflectsMissingSignals(t *testing.T) {
	root := t.TempDir()
	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	if report.Status != "partial" || report.SignalCompleteness.Status != "partial" {
		t.Fatalf("missing evidence should produce partial top-level status, got status=%q completeness=%#v", report.Status, report.SignalCompleteness)
	}
}

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

func assertP711CoverageForEveryClassification(t *testing.T, rows []productEvidenceClassCoverage, minValidationRecords int) {
	t.Helper()
	seen := map[string]productEvidenceClassCoverage{}
	for _, row := range rows {
		if _, exists := seen[row.Classification]; exists {
			t.Fatalf("duplicate classification coverage row for %q", row.Classification)
		}
		seen[row.Classification] = row
	}
	for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		row, ok := seen[class]
		if !ok {
			t.Fatalf("missing classification coverage row for %q", class)
		}
		if row.Status != "pass" || row.P76CommandID == "" || row.ClientDemoTranscriptID == "" || !row.SecurityPostureCoverage {
			t.Fatalf("classification %q missing executable coverage fields: %#v", class, row)
		}
		if row.ValidationRecordCount < minValidationRecords {
			t.Fatalf("classification %q missing validation-record example coverage: %#v", class, row)
		}
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

func writeP711LocalOpsReport(t *testing.T, root string) string {
	t.Helper()
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
	writeP711JSON(t, path, report)
	return path
}

func writeP910CompetitiveBenchmarkReport(t *testing.T, root string, conditions []map[string]any) string {
	t.Helper()
	path := filepath.Join(root, "testdata", "benchmarks", "localization-competitive.json")
	writeP711JSON(t, path, map[string]any{
		"schema_version":         "idiom.localization_benchmark.v1",
		"status":                 "pass",
		"competitive_conditions": conditions,
	})
	return path
}

func stageP910DefaultCorpusFixture(t *testing.T, root string) {
	t.Helper()
	sourcePath := filepath.Join(p711TestRepoRoot(t), "testdata", "localization", "recent-implementation-corpus.json")
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read default localization corpus fixture: %v", err)
	}
	path := filepath.Join(root, "testdata", "localization", "recent-implementation-corpus.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create default corpus fixture directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write default localization corpus fixture: %v", err)
	}
}

func p910JSONCountsFromProduct(t *testing.T, body []byte) map[string]int {
	t.Helper()
	var report struct {
		ReleaseGateLinkage []struct {
			Gate   string         `json:"gate"`
			Counts map[string]int `json:"counts"`
		} `json:"release_gate_linkage"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("parse product evidence JSON: %v\n%s", err, string(body))
	}
	for _, gate := range report.ReleaseGateLinkage {
		if gate.Gate == "P9.10" {
			return gate.Counts
		}
	}
	t.Fatalf("product evidence JSON missing P9.10 release gate: %s", string(body))
	return nil
}

func p910JSONCountsFromReleaseReadiness(t *testing.T, body []byte) map[string]int {
	t.Helper()
	var report struct {
		ReleaseReadiness []struct {
			Gate   string         `json:"gate"`
			Counts map[string]int `json:"counts"`
		} `json:"release_readiness"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("parse release evidence JSON: %v\n%s", err, string(body))
	}
	for _, gate := range report.ReleaseReadiness {
		if gate.Gate == "P9.10" {
			return gate.Counts
		}
	}
	t.Fatalf("release evidence JSON missing P9.10 readiness gate: %s", string(body))
	return nil
}

func p910JSONCountsFromReleaseCompetitiveCoverage(t *testing.T, body []byte) map[string]int {
	t.Helper()
	var report struct {
		BenchmarkLocalOps struct {
			CompetitiveCoverage struct {
				Counts map[string]int `json:"counts"`
			} `json:"competitive_coverage"`
		} `json:"benchmark_local_ops"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("parse release evidence JSON: %v\n%s", err, string(body))
	}
	return report.BenchmarkLocalOps.CompetitiveCoverage.Counts
}

func assertP910RequiredCountKeys(t *testing.T, counts map[string]int) {
	t.Helper()
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
			t.Fatalf("P9.10 JSON counts missing key %q: %#v", key, counts)
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

func writeP711ValidationRecords(t *testing.T, root string) string {
	t.Helper()
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
	writeP711JSON(t, path, records)
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

func TestP99ProductEvidenceNeedsEvidenceWhenPostureMissingOrMalformed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
	if err := os.Remove(matrixPath); err != nil {
		t.Fatalf("remove matrix: %v", err)
	}
	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "missing posture matrix") {
		t.Fatalf("missing posture matrix should fail closed: %#v", gate)
	}

	writeP711Text(t, matrixPath, "{not-json")
	report = buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate = p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "malformed posture matrix JSON") {
		t.Fatalf("malformed posture matrix should fail closed: %#v", gate)
	}
}

func TestP99ProductEvidenceRejectsComplianceOverclaimAndRegulatedSalesReadiness(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	writeP711Text(t, filepath.Join(root, "docs", "security-privacy-posture.md"), "Reviewer Command\nIdiom is HIPAA compliant.\n")

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "overclaim") {
		t.Fatalf("compliance overclaim should fail closed: %#v", gate)
	}

	stageP711ProductEvidenceFixtures(t, root)
	matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
	var matrix map[string]any
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("decode matrix: %v", err)
	}
	rows := matrix["client_classification_coverage"].([]any)
	row := rows[0].(map[string]any)
	row["classification"] = "finance"
	row["stance"] = "regulated sales ready"
	row["limitation"] = "ready for finance sales"
	writeP711JSON(t, matrixPath, matrix)

	report = buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate = p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "regulated client row") {
		t.Fatalf("regulated sales-readiness row should fail closed: %#v", gate)
	}
}

func TestP99ProductEvidenceBlocksAssertionAwareOverclaimsButAllowsNegatedLimitations(t *testing.T) {
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
		t.Run(p711SafeID(phrase, "phrase"), func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			writeP711Text(t, filepath.Join(root, "docs", "security-privacy-posture.md"), "Reviewer Command\n"+phrase+"\n")

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
			if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "overclaim") {
				t.Fatalf("positive overclaim %q should fail closed: %#v", phrase, gate)
			}
		})
	}

	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	writeP711Text(t, filepath.Join(root, "docs", "security-privacy-posture.md"), strings.Join([]string{
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
	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if p711TestBlockerContains(gate.Blockers, "overclaim") {
		t.Fatalf("negated limitations should not be treated as overclaims: %#v", gate)
	}
}

func TestP99ProductEvidenceRejectsPostureMatrixOverclaims(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
			var matrix map[string]any
			data, err := os.ReadFile(matrixPath)
			if err != nil {
				t.Fatalf("read matrix: %v", err)
			}
			if err := json.Unmarshal(data, &matrix); err != nil {
				t.Fatalf("decode matrix: %v", err)
			}
			tc.mutate(matrix)
			writeP711JSON(t, matrixPath, matrix)

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
			if gate.Status != "needs_evidence" ||
				gate.Counts["matrix_overclaim_blockers"] == 0 ||
				!p711TestBlockerContains(gate.Blockers, "posture matrix field") ||
				!p711TestBlockerContains(gate.Blockers, tc.pattern) {
				t.Fatalf("matrix overclaim %q should fail closed with structured blocker: %#v", tc.pattern, gate)
			}
		})
	}
}

func TestP99ProductEvidenceRejectsHollowSourceVerificationRecords(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageP711ProductEvidenceFixtures(t, root)
			writeP711Text(t, filepath.Join(root, "docs", "idiom-implementation-plan.md"), p99PlanFixtureWithSourceRecords(tc.records))

			report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
			gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
			if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, tc.blocker) {
				t.Fatalf("source verification blocker %q missing: %#v", tc.blocker, gate)
			}
		})
	}
}

func TestP99ProductEvidencePrivacyBlockedExportNeedsEvidence(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	path := filepath.Join(root, "validation-records.json")
	writeP711JSON(t, path, []map[string]any{{
		"record_id":             "record-enterprise-saas",
		"client_classification": "enterprise-saas",
		"evidence_status":       "complete",
		"retained_use_status":   "observed",
		"adoption_signal_type":  "internal_sponsor",
		"redaction_status":      "redacted",
		"raw_prompt":            "RAW_PROMPT_BODY",
	}})

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{
		ProjectRoot:       root,
		ValidationRecords: path,
	})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if report.Status != "blocked" || report.Privacy.ForbiddenFieldCheck != "failed:validation_records" {
		t.Fatalf("fixture should block product export privacy: status=%q privacy=%#v", report.Status, report.Privacy)
	}
	if gate.Status != "needs_evidence" ||
		!p711TestBlockerContains(gate.Blockers, "product evidence export status is privacy-blocked") ||
		!p711TestBlockerContains(gate.Blockers, "validation records failed privacy validation") {
		t.Fatalf("privacy-blocked product export should block P9.9 with structured blockers: %#v", gate)
	}

	release := buildReleaseEvidenceBundle(root, LaneHistorySummary{}, releaseEvidenceOptions{
		ProjectRoot:       root,
		ValidationRecords: path,
	})
	releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.9")
	if releaseGate.Status != gate.Status ||
		releaseGate.Counts["product_privacy_blockers"] != gate.Counts["product_privacy_blockers"] ||
		!p711TestBlockerContains(releaseGate.Blockers, "product evidence export status is privacy-blocked") {
		t.Fatalf("privacy-blocked product/release P9.9 should remain aligned: product=%#v release=%#v", gate, releaseGate)
	}
}

func TestP99ProductEvidenceContradictoryReportMarkersFailClosed(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	writeP711Text(t, filepath.Join(root, "docs", "release-gate-p9.9.md"), "# P9.9 Security Privacy Gate\n\nDecision: no-go\nReviewer signoff: GO\n\nThreat model\nData-flow\nPrivacy\nControl mapping\n\n## Reproduction Commands\n")

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "conflicting GO/no-go markers") {
		t.Fatalf("contradictory report markers should fail closed: %#v", gate)
	}
}

func TestP99ProductEvidenceRequiresStructuredSourceVerificationMethod(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	writeP711Text(t, filepath.Join(root, "docs", "idiom-implementation-plan.md"), "### P0.9 Product Security And Privacy Posture\n\nSource anchors checked on 2026-05-14 UTC.\n\nProduct threat model:\nP7.9 P8.4 P9.1 P9.3 P9.9\n\nData-flow map:\n\nPrivacy and retention posture:\n\nControl mapping evidence:\n\nDownstream go/no-go gates:\n")

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "structured source verification evidence") {
		t.Fatalf("missing structured source verification should fail closed: %#v", gate)
	}

	writeP711Text(t, filepath.Join(root, "docs", "idiom-implementation-plan.md"), p99PlanFixtureText(false))
	report = buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate = p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" || !p711TestBlockerContains(gate.Blockers, "unreachable source evidence missing verification method") {
		t.Fatalf("unreachable source without method should fail closed: %#v", gate)
	}
}

func TestP99ProductEvidenceNeedsEvidenceWhenControlEvidenceOrTiebacksMissing(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
	var matrix map[string]any
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("decode matrix: %v", err)
	}
	controls := matrix["required_controls"].([]any)
	first := controls[0].(map[string]any)
	first["evidence"] = []any{}
	matrix["release_gate_tiebacks"] = []string{"P7.9"}
	writeP711JSON(t, matrixPath, matrix)

	report := buildProductEvidenceExportReport(root, LaneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
	gate := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.9")
	if gate.Status != "needs_evidence" ||
		!p711TestBlockerContains(gate.Blockers, "missing executable evidence") ||
		!p711TestBlockerContains(gate.Blockers, "missing release-gate tieback: P8.4") {
		t.Fatalf("missing control evidence/tiebacks should fail closed: %#v", gate)
	}
}

func TestP99ProductAndReleaseEvidenceStructuredStatusAgree(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	stageReleaseEvidenceCommandBundle(t, root, "baseline", releaseEvidenceBaselineCommands())
	stageReleaseEvidenceCommandBundle(t, root, "current", releaseEvidenceCurrentCommands())

	product := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	release := buildReleaseEvidenceBundle(root, LaneHistorySummary{Total: 1}, releaseEvidenceOptions{
		ProjectRoot:       root,
		BaselineDir:       "baseline",
		CurrentDir:        "current",
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
	})
	productGate := p711TestReleaseGate(product.ReleaseGateLinkage, "P9.9")
	releaseGate := releaseEvidenceReadinessGate(release.ReleaseReadiness, "P9.9")
	if productGate.Status != releaseGate.Status || productGate.Status != "evidence_present" {
		t.Fatalf("product/release P9.9 status should agree: product=%#v release=%#v", productGate, releaseGate)
	}
	for _, key := range []string{"mapped_required_controls", "mapped_client_rows", "mapped_gate_tiebacks", "blockers"} {
		if productGate.Counts[key] != releaseGate.Counts[key] {
			t.Fatalf("product/release P9.9 count %q should agree: product=%#v release=%#v", key, productGate, releaseGate)
		}
	}
	if len(productGate.Blockers) != 0 || len(releaseGate.Blockers) != 0 ||
		!stringSliceContains(productGate.EvidenceIDs, "p7.9-security-privacy-posture") ||
		!stringSliceContains(releaseGate.EvidenceIDs, "release-gate-p9.9") {
		t.Fatalf("P9.9 structured fields incomplete: product=%#v release=%#v", productGate, releaseGate)
	}
}

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

func stageProductEvidenceRecoveryPacket(t *testing.T, root string, sessionID string) recovery.DecompositionPacket {
	t.Helper()
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
	if err != nil {
		t.Fatalf("write recovery failure record: %v", err)
	}
	packet, _, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, now)
	if err != nil {
		t.Fatalf("write recovery packet: %v", err)
	}
	if len(packet.CandidateAtoms) == 0 {
		t.Fatalf("expected candidate atom in recovery packet: %#v", packet)
	}
	return packet
}

func stageProductEvidenceRecoveredPacket(t *testing.T, root string, sessionID string) recovery.DecompositionPacket {
	t.Helper()
	packet := stageProductEvidenceRecoveryPacket(t, root, sessionID)
	var completed []string
	var commands []recovery.ReconciliationCommandResult
	for _, atom := range packet.CandidateAtoms {
		completed = append(completed, atom.ID)
		for _, command := range atom.RequiredTests {
			commands = append(commands, recovery.ReconciliationCommandResult{
				Command:  command,
				Status:   recovery.ReconciliationVerificationPass,
				ExitCode: 0,
			})
		}
	}
	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 12, 5, 0, 0, time.UTC),
		CompletedAtomIDs: completed,
		Verification: recovery.ReconciliationVerification{
			Status:   recovery.ReconciliationVerificationPass,
			Commands: commands,
		},
	})
	if err != nil {
		t.Fatalf("build recovery reconciliation: %v", err)
	}
	if _, _, err := recovery.WriteReconciliation(root, reconciliation, ""); err != nil {
		t.Fatalf("write recovery reconciliation: %v", err)
	}
	return packet
}

func assertP811IntegrationNeedsEvidence(t *testing.T, report productEvidenceExportReport) {
	t.Helper()
	integration := p711TestSignal(report.Signals, "integration_proof")
	if integration.Completeness == "complete" || integration.Status == "pass" {
		t.Fatalf("malformed integration proofs must not complete the signal: %#v", integration)
	}
	if linkage := p711TestReleaseGate(report.ReleaseGateLinkage, "P9.8"); linkage.Status == "evidence_present" {
		t.Fatalf("P9.8 should not be evidence_present for malformed proof rows: %#v", report.ReleaseGateLinkage)
	}
	if report.ProductSuccessGates.IntegrationProof == "evidence_present" {
		t.Fatalf("product integration gate should need evidence for malformed proof rows")
	}
}

func stageP711ProductEvidenceFixtures(t *testing.T, root string) {
	t.Helper()
	writeP711IntegrationProofs(t, root, []map[string]any{
		p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
		p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
		p711IntegrationProofFixture("p7.8-mcp-stdio", "mcp_stdio", true, false),
	})
	var securityRows []map[string]any
	var transcripts []map[string]any
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
	stageP99PostureGateFixtures(t, root, securityRows)
	writeP912PackagingSupportThesis(t, root, "continue")
	writeP711JSON(t, filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json"), map[string]any{
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

func stageP99PostureGateFixtures(t *testing.T, root string, securityRows []map[string]any) {
	t.Helper()
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
	writeP711JSON(t, filepath.Join(root, "docs", "security-privacy-posture.matrix.json"), map[string]any{
		"schema_version":                 "idiom.security_privacy_posture.v1",
		"artifact_id":                    "p7.9-security-privacy-posture",
		"required_controls":              controls,
		"client_classification_coverage": securityRows,
		"forbidden_evidence_fields":      p99ForbiddenEvidenceFields(),
		"release_gate_tiebacks":          p99RequiredTiebacks(),
	})
	writeP711Text(t, filepath.Join(root, "docs", "security-privacy-posture.md"), "# Security And Privacy Posture\n\nReviewer Command\n\nThis fixture is not a certification or attestation.\n")
	writeP711Text(t, filepath.Join(root, "docs", "idiom-implementation-plan.md"), p99PlanFixtureText(true))
	writeP711Text(t, filepath.Join(root, "docs", "release-gate-p9.3.md"), "# P9.3 Release Gate\n\nDecision: GO\n")
	writeP711Text(t, filepath.Join(root, "docs", "release-gate-p9.8.md"), "# P9.8 Release Gate\n\nDecision: GO\n")
	writeP711Text(t, filepath.Join(root, "docs", "release-decision-p9.2.md"), "# P9.2 Release Decision\n\nDecision: GO\n")
	writeP711Text(t, filepath.Join(root, "docs", "release-gate-p9.9.md"), "# P9.9 Security Privacy Gate\n\nReviewer signoff: GO\n\nThreat model\nData-flow\nPrivacy\nControl mapping\n\n## Reproduction Commands\n")
}

func writeP912PackagingSupportThesis(t *testing.T, root string, outcome string) {
	t.Helper()
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
	writeP711JSON(t, filepath.Join(root, "docs", "packaging-support-thesis.json"), map[string]any{
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

func p912MutateThesis(t *testing.T, root string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(root, "docs", "packaging-support-thesis.json")
	var thesis map[string]any
	p711ReadTestJSON(t, path, &thesis)
	mutate(thesis)
	writeP711JSON(t, path, thesis)
}

func p912ProductGateForCompleteEvidence(t *testing.T, root string) productEvidenceReleaseGateLinkage {
	t.Helper()
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
	benchmarkPath := writeP910CompetitiveBenchmarkReport(t, root, p910CoveredCompetitiveConditions())
	report := buildProductEvidenceExportReport(root, LaneHistorySummary{Total: 1}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
		BenchmarkReport:   benchmarkPath,
	})
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

func writeP711IntegrationProofs(t *testing.T, root string, proofs []map[string]any) {
	t.Helper()
	writeP711JSON(t, filepath.Join(root, "testdata", "sidecar", "integration-proofs.json"), map[string]any{
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

func writeP711JSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func p711ReadTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json %s: %v", path, err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("parse json %s: %v\n%s", path, err, string(data))
	}
}

func p911MutateFirstTranscript(t *testing.T, root string, mutate func(map[string]any)) {
	t.Helper()
	p911MutateTranscriptByClass(t, root, "finance", mutate)
}

func p911MutateTranscriptByClass(t *testing.T, root string, class string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")
	var set map[string]any
	p711ReadTestJSON(t, path, &set)
	transcripts, ok := set["transcripts"].([]any)
	if !ok {
		t.Fatalf("transcript fixture missing transcripts array: %#v", set)
	}
	for _, item := range transcripts {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("transcript row is not object: %#v", item)
		}
		if row["classification"] == class {
			mutate(row)
			writeP711JSON(t, path, set)
			return
		}
	}
	t.Fatalf("transcript fixture missing class %q", class)
}

func writeP711Text(t *testing.T, path string, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
