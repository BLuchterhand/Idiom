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

func TestP711ProductEvidenceExportCompletenessPrivacyAndClassCoverage(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)
	recordsPath := writeP711ValidationRecords(t, root)
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

	report := buildProductEvidenceExportReport(root, laneHistorySummary{Total: 2}, productEvidenceExportOptions{
		ProjectRoot:       root,
		LocalOpsReport:    localOpsPath,
		ValidationRecords: recordsPath,
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
	if got := p711TestSignal(reparsed.Signals, "learning"); got.Status != "pass" || got.Counts["accepted"] == 0 {
		t.Fatalf("learning signal should summarize sanitized ranker learning: %#v", got)
	}
	if reparsed.Privacy.MetadataOnly != true || reparsed.Privacy.ForbiddenFieldCheck != "pass" ||
		reparsed.Privacy.RawPromptCaptured || reparsed.Privacy.RawFileBodyCaptured ||
		reparsed.Privacy.SecretCaptured || reparsed.Privacy.PrivatePathCaptured {
		t.Fatalf("privacy contract should be safe by default: %#v", reparsed.Privacy)
	}
	if finding := localOpsForbiddenEvidenceLeak(string(body)); finding != "" {
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

func TestP711ProductEvidenceKeepsActivationUsefulWithoutOverclaimingP96(t *testing.T) {
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
	localOps.Quickstart.Cleanup.Mode = "remove-smoke-artifacts"
	localOps.Quickstart.Cleanup.RemainingState = "context_graph_retained_by_policy"
	localOps.Quickstart.Cleanup.RetainedStatePolicy = "quickstart smoke retains local context graph by policy"
	writeP711JSON(t, localOpsPath, localOps)

	report := buildProductEvidenceExportReport(root, laneHistorySummary{Total: 1}, productEvidenceExportOptions{
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
	if forbidden := localOpsForbiddenEvidenceLeak(string(body)); forbidden != "" {
		t.Fatalf("product evidence export leaked forbidden evidence class %q: %s", forbidden, string(body))
	}
}

func TestP96ProductEvidenceStrictActivationWithoutValidationRecordsNeedsEvidence(t *testing.T) {
	root := t.TempDir()
	stageP711ProductEvidenceFixtures(t, root)
	localOpsPath := writeP711LocalOpsReport(t, root)

	report := buildProductEvidenceExportReport(root, laneHistorySummary{Total: 1}, productEvidenceExportOptions{
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

	report := buildProductEvidenceExportReport(root, laneHistorySummary{Total: 1}, productEvidenceExportOptions{
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

			report := buildProductEvidenceExportReport(root, laneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
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

			report := buildProductEvidenceExportReport(root, laneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
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

			report := buildProductEvidenceExportReport(root, laneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
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

			report := buildProductEvidenceExportReport(root, laneHistorySummary{}, productEvidenceExportOptions{
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

			report := buildProductEvidenceExportReport(root, laneHistorySummary{}, productEvidenceExportOptions{
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
		if !localOpsContainsAbsolutePath(value) {
			t.Fatalf("expected embedded absolute path to be detected in %q", value)
		}
	}
	if localOpsContainsAbsolutePath("docs=https://example.com/idiom/path") {
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
	report := buildProductEvidenceExportReport(root, laneHistorySummary{Total: 1}, productEvidenceExportOptions{
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

func TestP718TopLevelStatusReflectsMissingSignals(t *testing.T) {
	root := t.TempDir()
	report := buildProductEvidenceExportReport(root, laneHistorySummary{}, productEvidenceExportOptions{ProjectRoot: root})
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

func assertP711CoverageForEveryClassification(t *testing.T, rows []p711ClassCoverage, minValidationRecords int) {
	t.Helper()
	seen := map[string]p711ClassCoverage{}
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

func p711TestHasSignal(signals []p711SignalSummary, id string) bool {
	return p711TestSignal(signals, id).ID != ""
}

func p711TestSignal(signals []p711SignalSummary, id string) p711SignalSummary {
	for _, signal := range signals {
		if signal.ID == id {
			return signal
		}
	}
	return p711SignalSummary{}
}

func writeP711LocalOpsReport(t *testing.T, root string) string {
	t.Helper()
	report := localOpsBenchmarkReport{
		SchemaVersion: localOpsBenchmarkSchemaVersion,
		GeneratedAt:   "2026-05-14T00:00:00Z",
		Command:       "idiom local-ops benchmark",
		Summary: localOpsBenchmarkSummary{
			ProfileCount:   1,
			SmokePassed:    3,
			SmokeTotal:     3,
			SmokePassRate:  1,
			ReplayLogCount: 1,
		},
		Replay: localOpsReplayReport{Status: "pass", LogCount: 1, LogIDs: []string{"log_id:p711"}},
		Privacy: localOpsPrivacyReport{
			MetadataOnly:         true,
			RawPromptCaptured:    false,
			RawFileBodyCaptured:  false,
			SecretCaptureAllowed: false,
		},
		Quickstart: &localOpsQuickstartSmoke{
			SmokeID:                    "p711-quickstart-smoke",
			Status:                     "pass",
			ClientClassification:       "open-source",
			EnvironmentClass:           "clean-install",
			AssistanceClass:            "none",
			DurationSource:             "measured_wall_clock",
			TimeToFirstPacketSeconds:   2,
			TimeToTrustEvidenceSeconds: 6,
			SetupInstallReadiness: localOpsActivationSetupEvidence{
				Status:              "pass",
				InstallSetupPath:    "release-binary",
				ReleaseInstallProof: "pass",
			},
			FirstUsefulPacket: localOpsActivationPacketEvidence{
				Status:               "pass",
				CommandID:            "p77-context-pack",
				PacketID:             "pack:p711",
				IndexID:              "index:p711",
				FreshnessState:       "fresh",
				CandidateFileCount:   2,
				CandidateSymbolCount: 1,
				CandidateTestCount:   1,
			},
			FreshnessCheck: localOpsActivationFreshnessEvidence{Status: "pass", CommandID: "p77-freshness-check", State: "fresh", FreshForScope: true, GlobalFresh: true},
			AuditReport: localOpsActivationAuditEvidence{
				Status:             "pass",
				CommandID:          "p77-harness-audit",
				AuditReportID:      "audit:p711",
				AuditEventIDs:      []string{"audit-event:p711"},
				HighFindingCount:   0,
				MediumFindingCount: 0,
				FindingCount:       0,
			},
			SuggestedVerification: localOpsActivationVerification{
				Status:            "pass",
				SuggestedCommands: []string{"go test ./..."},
			},
			ProductEvidenceSafeOutput: localOpsActivationProductEvidence{
				Status:              "pass",
				ExportID:            "p77-activation-p711",
				ForbiddenFieldCheck: "pass",
				RedactionStatus:     "redacted",
			},
			FailureReason: localOpsActivationFailureReason{Status: "none", PrimaryClass: "none", RetryAllowed: false},
			DocsGap:       localOpsActivationDocsGap{Status: "none", GapRecorded: false},
			PrivacyRedactionStatus: localOpsActivationPrivacyEvidence{
				Status:              "pass",
				ForbiddenFieldCheck: "pass",
			},
			Cleanup: localOpsActivationCleanupEvidence{
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

func p711TestSourceRef(refs []p711SourceReference, id string) p711SourceReference {
	for _, ref := range refs {
		if ref.ID == id {
			return ref
		}
	}
	return p711SourceReference{}
}

func p711TestReleaseGate(rows []p711ReleaseGateLinkage, gate string) p711ReleaseGateLinkage {
	for _, row := range rows {
		if row.Gate == gate {
			return row
		}
	}
	return p711ReleaseGateLinkage{}
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
	var controls []map[string]any
	controls = append(controls, map[string]any{"id": "redaction_no_body_capture"})
	var securityRows []map[string]any
	var transcripts []map[string]any
	for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		commandID := "p76-" + class
		securityRows = append(securityRows, map[string]any{
			"classification":  class,
			"stance":          p711DefaultStance(class),
			"p7_6_command_id": commandID,
			"limitation":      "proof only",
		})
		transcripts = append(transcripts, map[string]any{
			"id":              "p7.10-" + class,
			"classification":  class,
			"stance":          p711DefaultStance(class),
			"p7_6_command_id": commandID,
			"mvp_limitation":  "proof only",
			"commands":        []map[string]any{{"id": commandID, "exit_code": 0}},
		})
	}
	writeP711JSON(t, filepath.Join(root, "docs", "security-privacy-posture.matrix.json"), map[string]any{
		"artifact_id":                    "p7.9-security-privacy-posture",
		"required_controls":              controls,
		"client_classification_coverage": securityRows,
	})
	writeP711JSON(t, filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json"), map[string]any{
		"schema_version": "idiom.client_demo_transcripts.v1",
		"name":           "p7.10-client-demo-transcripts",
		"transcripts":    transcripts,
	})
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
