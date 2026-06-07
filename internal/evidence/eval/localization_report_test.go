package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/repository/comprehension"
)

func TestBuildLocalizationUnifiedReportCombinesEvidenceSurfaces(t *testing.T) {
	root := t.TempDir()
	writeLocalizationReportFile(t, filepath.Join(root, "go.mod"), "module example.com/localreport\n\ngo 1.22\n")
	writeLocalizationReportFile(t, filepath.Join(root, "x.go"), "package localreport\n\nfunc X() string { return \"x\" }\n")
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "localize report", Mode: "plan"}
	if err := comprehension.SaveView(root, key, comprehension.PackResult{PackID: key.ID(), IndexID: build.IndexID, CandidateFiles: []comprehension.PackFile{{Path: "go.mod"}}}); err != nil {
		t.Fatalf("SaveView current: %v", err)
	}
	oldKey := comprehension.ViewKey{IndexID: "old-index", Task: "old report", Mode: "plan"}
	if err := comprehension.SaveView(root, oldKey, comprehension.PackResult{PackID: oldKey.ID(), IndexID: "old-index", CandidateFiles: []comprehension.PackFile{{Path: "go.mod"}}}); err != nil {
		t.Fatalf("SaveView old: %v", err)
	}
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		EventID: "ranker_accept",
		Source:  "eval",
		Action:  "boost",
		State:   "accepted",
		Reason:  "swe_groundedness_oracle",
		Files:   []string{"go.mod"},
	}); err != nil {
		t.Fatalf("append accepted: %v", err)
	}
	if err := comprehension.AppendLearningLog(root, comprehension.LearningLogEntry{
		EventID: "ranker_reject",
		Source:  "eval",
		Action:  "no_op",
		State:   "rejected",
		Reason:  "unverified_evidence",
	}); err != nil {
		t.Fatalf("append rejected: %v", err)
	}
	logPath := filepath.Join(root, "verify.jsonl")
	writeLocalizationVerificationLog(t, logPath)

	report := BuildLocalizationUnifiedReport(localizationClientClassificationCorpusForTest(), LocalizationUnifiedReportOptions{
		WorkspaceRoot:       root,
		VerificationLogPath: logPath,
		Benchmark:           LocalizationBenchmarkOptions{TopK: []int{1}},
		Now:                 time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})

	if report.SchemaVersion != LocalizationUnifiedReportSchemaVersion || !report.Passed {
		t.Fatalf("unexpected report identity: %#v", report)
	}
	if report.Outcome.SampleCount != len(localizationClientClassifications) || len(report.Outcome.TopKEditPathCoverage) != 1 {
		t.Fatalf("outcome missing corpus metrics: %#v", report.Outcome)
	}
	if report.Trajectory.Baseline.State != "not_available" || !report.Trajectory.Current.Available || report.Trajectory.Delta.State != "not_available" {
		t.Fatalf("trajectory should expose neutral baseline/delta and current evidence: %#v", report.Trajectory)
	}
	if !report.CacheFreshness.Available || !report.CacheFreshness.Fresh || report.CacheFreshness.Rows != 2 || report.CacheFreshness.NonCurrentRows != 1 {
		t.Fatalf("cache/freshness missing row freshness evidence: %#v", report.CacheFreshness)
	}
	if report.Ranker.LearningSummary == nil || report.Ranker.LearningSummary.TotalEvents != 2 || report.Ranker.LearningSummary.AcceptedCount != 1 || report.Ranker.LearningSummary.RejectedCount != 1 {
		t.Fatalf("ranker learning summary missing accepted/rejected evidence: %#v", report.Ranker)
	}
	if !report.Verification.Available || report.Verification.Status != "passed" || report.Verification.CheckCount != 1 || !report.Verification.FullSuitePlanObserved {
		t.Fatalf("verification summary missing bounded status: %#v", report.Verification)
	}
	if !report.AuditMetrics.JoinCoverage.Outcome || !report.AuditMetrics.JoinCoverage.CacheFreshness || !report.AuditMetrics.JoinCoverage.Ranker || !report.AuditMetrics.JoinCoverage.Verification {
		t.Fatalf("audit join coverage missing report sections: %#v", report.AuditMetrics.JoinCoverage)
	}
	if len(report.ClientProduct.MissingClassifications) != 0 || !report.ClientProduct.GenericSupport || report.ClientProduct.ClientSpecificBranches {
		t.Fatalf("client classification support is incomplete or branched: %#v", report.ClientProduct)
	}
	if len(report.ClientProduct.ClassificationRows) != len(localizationClientClassifications) {
		t.Fatalf("client classification rows should cover every required class: %#v", report.ClientProduct.ClassificationRows)
	}
	for _, row := range report.ClientProduct.ClassificationRows {
		if row.Status != "pass" || row.SampleCount != 1 || row.Reason != "classification_sample_present" {
			t.Fatalf("expected covered client classification row, got %#v", row)
		}
	}
	if len(report.ProductSuccess) != 9 {
		t.Fatalf("expected all product-success gates, got %#v", report.ProductSuccess)
	}
	for _, want := range []string{
		"icp",
		"activation",
		"retained_use",
		"integration_proof",
		"security_privacy",
		"competitive_proof",
		"client_demo_proof",
		"packaging_support",
		"local_product_evidence_export",
	} {
		gate := localizationGateByIDForTest(report.ProductSuccess, want)
		if gate.Gate == "" {
			t.Fatalf("missing product-success gate %q in %#v", want, report.ProductSuccess)
		}
		if gate.Status == "" || gate.Reason == "" || gate.Evidence == "" {
			t.Fatalf("gate %q should include status, reason, and evidence: %#v", want, gate)
		}
	}
	if gate := localizationGateByIDForTest(report.ProductSuccess, "icp"); gate.Status != "pass" {
		t.Fatalf("expected ICP to pass when every required class has a sample: %#v", gate)
	}
	if gate := localizationGateByIDForTest(report.ProductSuccess, "client_demo_proof"); gate.Status != "pass" {
		t.Fatalf("expected client demo proof to pass when every required class has a sample: %#v", gate)
	}
	if gate := localizationGateByIDForTest(report.ProductSuccess, "competitive_proof"); gate.Status != "pass" || gate.Reason != "competitive_conditions_covered" {
		t.Fatalf("expected competitive proof to pass only with covered competitive conditions, got %#v", gate)
	}
	for _, id := range []string{"retained_use", "integration_proof", "local_product_evidence_export"} {
		gate := localizationGateByIDForTest(report.ProductSuccess, id)
		if gate.Status == "pass" || gate.Reason != "cache_non_current" || !strings.Contains(gate.Evidence, "cache_non_current=1") {
			t.Fatalf("expected gate %q to downgrade from same-report non-current cache evidence, got %#v", id, gate)
		}
	}
	if report.Privacy.PromptTextIncluded || report.Privacy.FileBodiesIncluded || report.Privacy.LogExcerptsIncluded || report.Privacy.SecretsIncluded {
		t.Fatalf("privacy flags must exclude raw content: %#v", report.Privacy)
	}
}

func TestEvaluateLocalizationUnifiedReportFileSetsWorkspaceRoot(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	path := filepath.Join(t.TempDir(), "corpus.json")
	data, err := json.Marshal(corpus)
	if err != nil {
		t.Fatalf("marshal corpus: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	report, err := EvaluateLocalizationUnifiedReportFile(path, evalTestRepoRoot(t), LocalizationUnifiedReportOptions{
		Benchmark: LocalizationBenchmarkOptions{TopK: []int{1}},
		Now:       time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("EvaluateLocalizationUnifiedReportFile: %v", err)
	}
	if report.Name != corpus.Name || report.Outcome.SampleCount != 1 {
		t.Fatalf("unexpected report identity: %#v", report)
	}
	if !report.CacheFreshness.Available {
		t.Fatalf("root argument should populate workspace-root-backed cache report: %#v", report.CacheFreshness)
	}
}

func TestLocalizationUnifiedReportExplainsMissingCurrentCorpusClientSamples(t *testing.T) {
	corpusPath := evalTestdataPath(t, "localization", "recent-implementation-corpus.json")
	corpus, err := ReadLocalizationCorpus(corpusPath)
	if err != nil {
		t.Fatalf("ReadLocalizationCorpus: %v", err)
	}
	report := BuildLocalizationUnifiedReport(corpus, LocalizationUnifiedReportOptions{
		Benchmark: LocalizationBenchmarkOptions{TopK: []int{1}},
		Now:       time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})

	if len(report.ClientProduct.ClassificationRows) != len(localizationClientClassifications) {
		t.Fatalf("expected one row per required classification, got %#v", report.ClientProduct.ClassificationRows)
	}
	for _, row := range report.ClientProduct.ClassificationRows {
		if row.Status != "not_available" || row.SampleCount != 0 || row.Reason != "classification_sample_missing" {
			t.Fatalf("expected explicit missing-classification row, got %#v", row)
		}
		if row.Label == "" || row.Evidence != "required_classification_enumerated" {
			t.Fatalf("expected useful bounded row evidence, got %#v", row)
		}
	}
	if gate := localizationGateByIDForTest(report.ProductSuccess, "icp"); gate.Status != "partial" || gate.Reason != "classification_statuses_enumerate_missing_coverage" {
		t.Fatalf("expected ICP to be partial with explicit missing coverage reason, got %#v", gate)
	}
	if gate := localizationGateByIDForTest(report.ProductSuccess, "client_demo_proof"); gate.Status != "not_available" || gate.Reason != "client_classification_samples_missing" {
		t.Fatalf("expected client-demo proof to explain missing classification samples, got %#v", gate)
	}
}

func TestLocalizationProductSuccessGatesDegradeStaleAndNonCurrentCacheEvidence(t *testing.T) {
	tests := []struct {
		name       string
		cache      LocalizationCacheFreshnessReport
		wantReason string
	}{
		{
			name: "stale and non-current rows",
			cache: LocalizationCacheFreshnessReport{
				Available:      true,
				State:          "available",
				Indexed:        true,
				Fresh:          true,
				Rows:           6,
				FreshRows:      2,
				StaleRows:      4,
				NonCurrentRows: 5,
			},
			wantReason: "cache_stale,cache_non_current",
		},
		{
			name: "stale rows",
			cache: LocalizationCacheFreshnessReport{
				Available: true,
				State:     "available",
				Indexed:   true,
				Fresh:     true,
				Rows:      6,
				FreshRows: 2,
				StaleRows: 4,
			},
			wantReason: "cache_stale",
		},
		{
			name:       "cache metrics unavailable",
			cache:      LocalizationCacheFreshnessReport{State: "not_available", Reason: "workspace_root_not_available"},
			wantReason: "cache_metrics_not_available",
		},
		{
			name: "not fresh with no cache rows",
			cache: LocalizationCacheFreshnessReport{
				Available:      true,
				State:          "available",
				Indexed:        true,
				Fresh:          false,
				StalePathCount: 3,
			},
			wantReason: "cache_stale,cache_rows_missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gates := buildLocalizationProductSuccessGates(
				LocalizationOutcomeReport{SampleCount: 7},
				LocalizationTrajectoryReport{},
				tt.cache,
				LocalizationRankerReport{
					Available:          true,
					State:              "available",
					LearningLogPresent: true,
					LearningSummary:    &LocalizationLearningSummaryReport{TotalEvents: 1},
				},
				LocalizationVerificationReport{
					Available:  true,
					State:      "available",
					Status:     "passed",
					CheckCount: 1,
					Passed:     true,
				},
				LocalizationAuditMetricsReport{
					Available: true,
					State:     "available",
					ContextCache: LocalizationAuditContextCacheMetric{
						Rows:           tt.cache.Rows,
						FreshRows:      tt.cache.FreshRows,
						StaleRows:      tt.cache.StaleRows,
						NonCurrentRows: tt.cache.NonCurrentRows,
					},
				},
				LocalizationClientProductReport{},
				buildLocalizationReportPrivacy(),
				true,
			)

			for _, id := range []string{"retained_use", "integration_proof", "local_product_evidence_export"} {
				gate := localizationGateByIDForTest(gates, id)
				if gate.Gate == "" {
					t.Fatalf("missing product-success gate %q in %#v", id, gates)
				}
				if gate.Status == "pass" {
					t.Fatalf("gate %q passed despite degraded cache evidence: %#v", id, gate)
				}
				if gate.Reason != tt.wantReason {
					t.Fatalf("gate %q should expose degraded cache reason %q, got %#v", id, tt.wantReason, gate)
				}
				if !strings.Contains(gate.Evidence, "cache_stale=") || !strings.Contains(gate.Evidence, "cache_non_current=") {
					t.Fatalf("gate %q should include bounded cache row evidence, got %#v", id, gate)
				}
			}
		})
	}
}

func TestLocalizationCompetitiveProofGateRequiresCompetitiveConditions(t *testing.T) {
	outcome := LocalizationOutcomeReport{
		SampleCount:          1,
		TopKEditPathCoverage: []LocalizationTopKCoverageMetric{{K: 1, Available: true, Numerator: 1, Denominator: 1}},
		BroadReadCount:       LocalizationCountMetric{Available: true, Numerator: 1, Denominator: 1},
		ZeroHitSearchCount:   LocalizationCountMetric{Available: true, Numerator: 0, Denominator: 1},
		CandidatePathWaste:   LocalizationCandidatePathWasteMetric{Available: true, CandidateWasteDenominator: 1, PathWasteDenominator: 1},
	}
	gate := localizationCompetitiveProofGate(outcome, LocalizationTrajectoryReport{Current: LocalizationTrajectorySnapshot{Available: true}})
	if gate.Status == "pass" || gate.Reason != "competitive_conditions_missing" {
		t.Fatalf("expected competitive proof to reject self-referential metrics without condition coverage, got %#v", gate)
	}

	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"go.mod"}, []LocalizationCandidateFile{{Path: "go.mod"}}, ptrInt(0), ptrInt(0))
	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("go.mod", localizationClientClassifications)
	corpus.Tasks = []LocalizationCorpusTask{task}
	report := BuildLocalizationUnifiedReport(corpus, LocalizationUnifiedReportOptions{Now: time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC)})
	gate = localizationGateByIDForTest(report.ProductSuccess, "competitive_proof")
	if gate.Status != "pass" || gate.Reason != "competitive_conditions_covered" || !strings.Contains(gate.Evidence, "competitive_external_covered=2") {
		t.Fatalf("expected measured/documented external competitive conditions to satisfy the gate, got %#v", gate)
	}
}

func TestLocalizationCompetitiveProofGateRejectsPoisonedClientClassification(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"go.mod"}, []LocalizationCandidateFile{{Path: "go.mod"}}, ptrInt(0), ptrInt(0))
	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("go.mod", []string{"customer-acme-session"})
	corpus.Tasks = []LocalizationCorpusTask{task}

	report := BuildLocalizationUnifiedReport(corpus, LocalizationUnifiedReportOptions{Now: time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC)})
	gate := localizationGateByIDForTest(report.ProductSuccess, "competitive_proof")
	if gate.Status == "pass" || gate.Reason != "competitive_condition_client_classification_invalid" {
		t.Fatalf("expected competitive proof to reject poisoned client classification, got %#v", gate)
	}
	if !strings.Contains(gate.Evidence, "competitive_invalid_client_classifications=6") {
		t.Fatalf("expected bounded invalid-classification evidence, got %#v", gate)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("unified report leaked customer-like classification %q: %s", forbidden, string(data))
		}
	}
}

func TestLocalizationUnifiedReportDoesNotEchoRawPromptOrBodyText(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "verify.jsonl")
	const sentinel = "PROMPT_SENTINEL BEGIN OPENSSH PRIVATE KEY BODY_SENTINEL"
	if err := os.WriteFile(logPath, []byte(`{"type":"message","role":"user","content":"`+sentinel+`"}
{"type":"tool_result","agent":"implement_verify","summary":"ok","error":""}
`), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	report := BuildLocalizationUnifiedReport(validLocalizationCorpusForTest(), LocalizationUnifiedReportOptions{
		WorkspaceRoot:       root,
		VerificationLogPath: logPath,
		Now:                 time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"PROMPT_SENTINEL", "BEGIN OPENSSH PRIVATE KEY", "BODY_SENTINEL"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("unified report echoed forbidden raw content %q: %s", forbidden, string(data))
		}
	}
}

func localizationGateByIDForTest(gates []LocalizationProductSuccessGate, id string) LocalizationProductSuccessGate {
	for _, gate := range gates {
		if gate.Gate == id {
			return gate
		}
	}
	return LocalizationProductSuccessGate{}
}

func localizationClientClassificationCorpusForTest() LocalizationCorpus {
	corpus := validLocalizationCorpusForTest()
	corpus.Tasks = nil
	for _, classification := range localizationClientClassifications {
		task := localizationBenchmarkTaskForTest("task-"+classification, []string{"go.mod"}, []LocalizationCandidateFile{{Path: "go.mod"}}, ptrInt(0), ptrInt(0))
		task.ClientProductArea = classification
		task.Expected.Symbols[0].Path = "go.mod"
		task.Expected.Tests[0].Path = "go.mod"
		task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("go.mod", []string{classification})
		corpus.Tasks = append(corpus.Tasks, task)
	}
	return corpus
}

func writeLocalizationVerificationLog(t *testing.T, path string) {
	t.Helper()
	const log = `{"type":"message","role":"verification_plan","content":"{\"commands\":[\"go test ./...\"],\"uses_full_suite\":true,\"coverage\":\"full-suite\",\"confidence\":\"high\",\"risk_level\":\"medium\"}"}
{"type":"tool_result","agent":"implement_verify","summary":"go test ./... passed","error":""}
{"type":"message","role":"verification_feedback","content":"{\"commands\":[\"go test ./...\"],\"failed_commands\":[],\"confidence\":\"high\"}"}
`
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func writeLocalizationReportFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
