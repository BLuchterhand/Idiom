package eval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/evidence/eval"
)

func evalCLITestRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from cwd")
		}
		dir = parent
	}
}

func TestRunPrintsMetricsJSON(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversation.jsonl")
	writeQwenEvalLog(t, logPath)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-log", logPath, "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"last_user":"review routing"`, `"budget_ledgers"`, `"route_ambiguities"`, `"control_rails"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected JSON output to contain %q, got %s", want, out)
		}
	}
}

func TestRunWritesSuggestedScenario(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversation.jsonl")
	scenarioPath := filepath.Join(dir, "suggested.json")
	writeQwenEvalLog(t, logPath)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-log", logPath, "-write-scenario", scenarioPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(scenarioPath)
	if err != nil {
		t.Fatalf("read suggested scenario: %v", err)
	}
	for _, want := range []string{`"name": "suggested"`, `"require_budget_ledger"`, `"require_route_ambiguity"`, `"require_control_rails"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected suggested scenario to contain %q, got %s", want, string(data))
		}
	}
}

func TestRunReturnsFailureForScenarioMismatch(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversation.jsonl")
	scenarioPath := filepath.Join(dir, "scenario.json")
	writeQwenEvalLog(t, logPath)
	scenario := `{
  "name": "wrong-prompt",
  "prompt_contains": ["different prompt"]
}
`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-log", logPath, "-scenario", scenarioPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "FAIL wrong-prompt") || !strings.Contains(stdout.String(), "last user prompt does not contain") {
		t.Fatalf("expected failure report, got stdout=%s", stdout.String())
	}
}

func TestRunPrintsLocalizationCorpusReport(t *testing.T) {
	corpusPath := filepath.Join(evalCLITestRepoRoot(t), "testdata", "localization", "recent-implementation-corpus.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-localization-corpus", corpusPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"Localization corpus: PASS recent-implementation-localization tasks=5 experimental=true",
		"Sources: conversation=1, telemetry=5",
		"Redaction: corpus_redacted=1, redacted=5",
		"Expected anchors: files=",
		"Benchmark metrics: experimental=true manual_corpus_review_required=true",
		"Top-k edit-path coverage: k=1",
		"Broad-read count:",
		"Zero-hit search count:",
		"Candidate/path waste:",
		"Competitive conditions:",
		"- raw_search_read status=not_available",
		"- aider_repo_map status=not_available external=true",
		"Metric definitions:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected localization report to contain %q, got %s", want, stdout.String())
		}
	}
}

func TestRunPrintsLocalizationCorpusJSON(t *testing.T) {
	corpusPath := filepath.Join(evalCLITestRepoRoot(t), "testdata", "localization", "recent-implementation-corpus.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-localization-corpus", corpusPath, "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{`"total_tasks": 5`, `"source_coverage"`, `"redaction_status"`, `"top_k_edit_path_coverage"`, `"broad_read_count"`, `"candidate_path_waste"`, `"competitive_conditions"`, `"definitions"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected JSON localization report to contain %q, got %s", want, stdout.String())
		}
	}
}

func TestRunPrintsUnifiedLocalizationReport(t *testing.T) {
	corpusPath := filepath.Join(evalCLITestRepoRoot(t), "testdata", "localization", "recent-implementation-corpus.json")
	dir := t.TempDir()
	logPath := filepath.Join(dir, "verify.jsonl")
	writeVerificationEvalLog(t, logPath)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-localization-corpus", corpusPath, "-localization-report", "-log", logPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"Localization report: PASS recent-implementation-localization schema=localization_unified_report.v1",
		"Outcome: samples=5",
		"Trajectory: baseline=not_available",
		"Competitive conditions:",
		"- external_agent_native status=not_available external=true",
		"Cache/freshness:",
		"Ranker:",
		"Verification: state=available available=true status=passed checks=1",
		"Audit metrics: joins=",
		"Product-success gates:",
		"- icp status=partial",
		"- client_demo_proof status=not_available",
		"Client/product: generic=true client_specific_branches=false",
		"Client classifications:",
		"- finance label=\"finance\" status=not_available samples=0 reason=classification_sample_missing",
		"- healthcare_life_sciences label=\"healthcare/life sciences\" status=not_available samples=0 reason=classification_sample_missing",
		"- public_sector label=\"public-sector\" status=not_available samples=0 reason=classification_sample_missing",
		"- enterprise_saas label=\"enterprise SaaS\" status=not_available samples=0 reason=classification_sample_missing",
		"- tooling_vendor label=\"tooling-vendor\" status=not_available samples=0 reason=classification_sample_missing",
		"- local_model_private_code label=\"local-model/private-code\" status=not_available samples=0 reason=classification_sample_missing",
		"- open_source label=\"open-source\" status=not_available samples=0 reason=classification_sample_missing",
		"Privacy: policy=aggregate_counts_relative_paths_no_bodies_or_prompts prompt_text=false file_bodies=false log_excerpts=false secrets=false",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected unified localization report to contain %q, got %s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "PROMPT_SENTINEL BODY_SENTINEL") || strings.Contains(stdout.String(), "BODY_SENTINEL") {
		t.Fatalf("unified localization report echoed raw sentinel text: %s", stdout.String())
	}
}

func TestRunPrintsUnifiedLocalizationReportJSON(t *testing.T) {
	corpusPath := filepath.Join(evalCLITestRepoRoot(t), "testdata", "localization", "recent-implementation-corpus.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-localization-corpus", corpusPath, "-localization-report", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{`"schema_version": "localization_unified_report.v1"`, `"competitive_conditions"`, `"baseline": "aider_repo_map"`, `"cache_freshness"`, `"ranker"`, `"verification"`, `"audit_metrics"`, `"client_product"`, `"classification_rows"`, `"product_success_gates"`, `"gate": "icp"`, `"gate": "competitive_proof"`, `"gate": "local_product_evidence_export"`, `"prompt_text_included": false`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected unified JSON report to contain %q, got %s", want, stdout.String())
		}
	}
}

func TestRunLocalizationReportSuppressesUnknownCompetitiveClientClassification(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "adversarial-localization-corpus.json")
	writeCompetitiveClientClassificationCorpus(t, corpusPath, []string{"customer-acme-session"})

	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "text", args: []string{"-localization-corpus", corpusPath, "-localization-report"}},
		{name: "json", args: []string{"-localization-corpus", corpusPath, "-localization-report", "-json"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected failed report exit for invalid classification evidence, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			out := stdout.String()
			for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
				if strings.Contains(out, forbidden) || strings.Contains(stderr.String(), forbidden) {
					t.Fatalf("classification output leaked %q stdout=%s stderr=%s", forbidden, out, stderr.String())
				}
			}
			for _, want := range []string{"competitive_condition_client_classification_invalid", "competitive_proof"} {
				if !strings.Contains(out, want) {
					t.Fatalf("expected bounded competitive classification signal %q, got %s", want, out)
				}
			}
			if !strings.Contains(out, "invalid_client_classifications") {
				t.Fatalf("expected bounded invalid classification count, got %s", out)
			}
			if strings.Contains(out, "- competitive_proof status=pass") || strings.Contains(out, `"reason": "competitive_conditions_covered"`) {
				t.Fatalf("competitive_proof should not pass for poisoned classification evidence: %s", out)
			}
		})
	}
}

func TestRunPrintsConfiguredLocalizationTopK(t *testing.T) {
	corpusPath := filepath.Join(evalCLITestRepoRoot(t), "testdata", "localization", "recent-implementation-corpus.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-localization-corpus", corpusPath, "-localization-top-k", "5,1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Top-k edit-path coverage: k=1") || !strings.Contains(out, " k=5 ") {
		t.Fatalf("expected configured top-k output, got %s", out)
	}
	if strings.Contains(out, " k=3 ") {
		t.Fatalf("did not expect default k=3 when top-k configured, got %s", out)
	}
}

func TestRunRejectsInvalidLocalizationTopK(t *testing.T) {
	corpusPath := filepath.Join(evalCLITestRepoRoot(t), "testdata", "localization", "recent-implementation-corpus.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-localization-corpus", corpusPath, "-localization-top-k", "0"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid -localization-top-k") {
		t.Fatalf("expected invalid top-k error, got %s", stderr.String())
	}
}

func TestRunRequiresLogPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "missing required -log path") {
		t.Fatalf("expected missing log error, got %s", stderr.String())
	}
}

func writeQwenEvalLog(t *testing.T, path string) {
	t.Helper()
	const log = `{"type":"message","role":"user","content":"review routing"}
{"type":"message","role":"route_plan","content":"initial=review reason=review_request contract=review_findings thoroughness=thorough"}
{"type":"message","role":"budget_ledger","content":"{\"lane\":\"review\",\"used_model_calls\":2,\"total_evidence_paths\":1,\"stop_reason\":\"ready_after_retry\",\"budget_exhausted\":false}"}
{"type":"message","role":"route_ambiguity","content":"{\"selected_lane\":\"review\",\"reason\":\"review_request\",\"signals\":[\"review\"],\"ambiguous\":false}"}
{"type":"message","role":"control_rails","content":"{\"lane\":\"review\",\"contract\":\"review_findings\",\"task_family\":\"review\",\"permission_profile\":\"read_only_workspace\",\"required_evidence\":[\"candidate_path\"],\"required_tools\":[\"workspace_inspection\"],\"forbidden_tools\":[\"edit_file\"],\"readiness_gate\":[\"required_evidence_present\"],\"search_policy\":[\"rank_candidates\"],\"verification_contract\":\"not_required_for_read_only_lane\",\"stop_conditions\":[\"required_evidence_satisfied\"],\"final_critic\":[\"findings_first\"],\"failure_to_eval\":false}"}
{"type":"message","role":"assistant","content":"No bounded finding."}
`
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func writeVerificationEvalLog(t *testing.T, path string) {
	t.Helper()
	const log = `{"type":"message","role":"user","content":"PROMPT_SENTINEL BODY_SENTINEL"}
{"type":"message","role":"verification_plan","content":"{\"commands\":[\"go test ./...\"],\"uses_full_suite\":true,\"coverage\":\"full-suite\",\"confidence\":\"high\",\"risk_level\":\"medium\"}"}
{"type":"tool_result","agent":"implement_verify","summary":"go test ./... passed","error":""}
{"type":"message","role":"verification_feedback","content":"{\"commands\":[\"go test ./...\"],\"failed_commands\":[],\"confidence\":\"high\"}"}
`
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatalf("write verification log: %v", err)
	}
}

func writeCompetitiveClientClassificationCorpus(t *testing.T, path string, classifications []string) {
	t.Helper()
	broadReads := 0
	zeroHitSearches := 0
	corpus := eval.LocalizationCorpus{
		Name:         "adversarial-localization",
		Version:      1,
		Experimental: true,
		SourceWindow: eval.LocalizationSourceWindow{
			StartedAt:   "2026-05-12T00:00:00Z",
			EndedAt:     "2026-05-13T00:00:00Z",
			SourceRoots: []string{"logs/telemetry"},
		},
		PrivacyReview: eval.LocalizationPrivacyReview{
			Status:                   "redacted",
			ReviewedAt:               "2026-05-13T00:00:00Z",
			SensitiveContentExcluded: true,
		},
		Tasks: []eval.LocalizationCorpusTask{{
			ID:                "competitive-client-classification",
			Name:              "Competitive Client Classification",
			Summary:           "Exercises competitive-condition client classification privacy boundaries.",
			Themes:            []string{"competitive_conditions"},
			ClientProductArea: "finance",
			SourceRefs: []eval.LocalizationSourceRef{{
				Kind:     "telemetry",
				Ref:      "fixture",
				LogPath:  "logs/telemetry/fixture.jsonl",
				Digest:   "sha256:fixture",
				Evidence: []string{"sanitized fixture"},
			}},
			Expected: eval.LocalizationExpectedAnchors{
				Files:   []eval.LocalizationExpectedFile{{Path: "go.mod"}},
				Symbols: []eval.LocalizationExpectedSymbol{{Path: "go.mod", Name: "module"}},
				Tests:   []eval.LocalizationExpectedTest{{Path: "go.mod", Name: "fixture"}},
			},
			Benchmark: &eval.LocalizationBenchmarkInput{
				ContextPacket: &eval.LocalizationContextPacketEvidence{
					Files: []eval.LocalizationCandidateFile{{Path: "go.mod"}},
				},
				Trajectory: &eval.LocalizationTrajectoryEvidence{
					BroadReadCount:     &broadReads,
					ZeroHitSearchCount: &zeroHitSearches,
				},
				CompetitiveConditions: competitiveConditionsForCommandTest("go.mod", classifications),
			},
			Privacy: eval.LocalizationTaskPrivacy{
				Status:                   "redacted",
				SensitiveContentExcluded: true,
			},
			SensitiveExclusionNote: "Excluded prompt text, file bodies, transcript excerpts, secrets, and customer-identifying fields.",
			Provenance: eval.LocalizationTaskProvenance{
				SelectionReason: "sanitized fixture",
				SanitizedFields: []string{"classification"},
				SourceCoverage:  []string{"telemetry"},
			},
		}},
	}
	data, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		t.Fatalf("marshal corpus: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
}

func competitiveConditionsForCommandTest(expectedPath string, classifications []string) []eval.LocalizationCompetitiveConditionInput {
	conditions := make([]eval.LocalizationCompetitiveConditionInput, 0, 6)
	for _, baseline := range []string{"raw_search_read", "generated_baseline_discovery_report", "generated_current_discovery_report", "context_pack"} {
		conditions = append(conditions, eval.LocalizationCompetitiveConditionInput{
			Baseline:              baseline,
			Status:                "measured",
			CandidateFiles:        []eval.LocalizationCandidateFile{{Path: expectedPath}},
			Budget:                &eval.LocalizationCompetitiveBudgetEvidence{ToolCallBudget: 8, WallClockBudgetMS: 60000, UsedToolCalls: 3, ElapsedMS: 1200},
			CommandID:             baseline + "_fixture",
			ArtifactPath:          "testdata/localization/competitive/" + baseline + ".json",
			ClientClassifications: append([]string(nil), classifications...),
		})
	}
	for _, baseline := range []string{"aider_repo_map", "external_agent_native"} {
		conditions = append(conditions, eval.LocalizationCompetitiveConditionInput{
			Baseline:              baseline,
			Status:                "infeasible",
			CommandID:             baseline + "_probe",
			ArtifactPath:          "testdata/localization/competitive/" + baseline + ".json",
			InfeasibilityReason:   "tool_unavailable",
			ClaimImpact:           "claim_weakened_until_external_run",
			ClientClassifications: append([]string(nil), classifications...),
		})
	}
	return conditions
}
