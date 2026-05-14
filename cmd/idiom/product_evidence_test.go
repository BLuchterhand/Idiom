package main

import (
	"bytes"
	"encoding/json"
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
	if reparsed.SignalCompleteness.Status != "partial" || reparsed.SignalCompleteness.CompleteCount < 5 {
		t.Fatalf("expected mostly complete P7.11 signal matrix with explicit partials, got %#v", reparsed.SignalCompleteness)
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
			TimeToFirstPacketSeconds:   2,
			TimeToTrustEvidenceSeconds: 6,
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
			ProductEvidenceSafeOutput: localOpsActivationProductEvidence{
				Status:              "pass",
				ExportID:            "p77-activation-p711",
				ForbiddenFieldCheck: "pass",
				RedactionStatus:     "redacted",
			},
			PrivacyRedactionStatus: localOpsActivationPrivacyEvidence{
				Status:              "pass",
				ForbiddenFieldCheck: "pass",
			},
			Cleanup: localOpsActivationCleanupEvidence{Status: "pass", CommandID: "p77-cleanup", ExitStatus: "pass"},
		},
	}
	path := filepath.Join(root, ".idiom", "local-ops", "report.json")
	writeP711JSON(t, path, report)
	return path
}

func writeP711ValidationRecords(t *testing.T, root string) string {
	t.Helper()
	var records []map[string]any
	for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		records = append(records, map[string]any{
			"record_id":             "record-" + class,
			"client_classification": class,
			"evidence_status":       "complete",
			"retained_use_status":   "observed",
			"adoption_signal_type":  "internal_sponsor",
			"redaction_status":      "redacted",
			"raw_prompt":            "RAW_PROMPT_BODY /Users/ben/CustomerX sk_live_demo",
			"raw_file_body":         "func LeakedBody() {}",
		})
	}
	path := filepath.Join(root, "validation-records.json")
	writeP711JSON(t, path, records)
	return path
}

func stageP711ProductEvidenceFixtures(t *testing.T, root string) {
	t.Helper()
	writeP711JSON(t, filepath.Join(root, "testdata", "sidecar", "integration-proofs.json"), map[string]any{
		"schema_version": "idiom.sidecar_integration_proofs.v1",
		"name":           "p7.8-sidecar-integration-proofs",
		"proofs": []map[string]any{
			p711IntegrationProofFixture("p7.8-shell-ci", "shell_ci_script", true, false),
			p711IntegrationProofFixture("p7.8-external-coding-agent", "external_coding_agent_workflow", true, false),
			p711IntegrationProofFixture("p7.8-mcp-stdio-placeholder", "mcp_stdio_placeholder", false, true),
		},
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

func p711IntegrationProofFixture(id string, path string, executable bool, placeholder bool) map[string]any {
	return map[string]any{
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
