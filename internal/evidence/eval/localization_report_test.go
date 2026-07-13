package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.It("build localization unified report combines evidence surfaces", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeLocalizationReportFile(filepath.Join(root, "go.mod"), "module example.com/localreport\n\ngo 1.22\n")
	writeLocalizationReportFile(filepath.Join(root, "x.go"), "package localreport\n\nfunc X() string { return \"x\" }\n")

	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "localize report", Mode: "plan"}
	gomega.Expect(comprehension.SaveView(root, key, comprehension.PackResult{PackID: key.ID(), IndexID: build.IndexID, CandidateFiles: []comprehension.PackFile{{Path: "go.mod"}}})).To(gomega.Succeed(), "save current view")

	oldKey := comprehension.ViewKey{IndexID: "old-index", Task: "old report", Mode: "plan"}
	gomega.Expect(comprehension.SaveView(root, oldKey, comprehension.PackResult{PackID: oldKey.ID(), IndexID: "old-index", CandidateFiles: []comprehension.PackFile{{Path: "go.mod"}}})).To(gomega.Succeed(), "save old view")

	_, err = comprehension.RecordVerifiedRankerEvidence(t.Context(), root, comprehension.VerifiedRankerLearningRequest{
		Source:   "eval",
		Files:    []string{"go.mod"},
		Verified: true,
		Reason:   "swe_groundedness_oracle",
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "append accepted feedback")

	_, _, err = comprehension.ApplyVerifiedRankerLearning(t.Context(), root, comprehension.VerifiedRankerLearningRequest{
		Source: "eval",
		Action: "no_op",
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "append rejected feedback")

	logPath := filepath.Join(root, "verify.jsonl")
	writeLocalizationVerificationLog(logPath)

	report := BuildLocalizationUnifiedReport(localizationClientClassificationCorpusForTest(), LocalizationUnifiedReportOptions{
		WorkspaceRoot:       root,
		VerificationLogPath: logPath,
		Benchmark:           LocalizationBenchmarkOptions{TopK: []int{1}},
		Now:                 time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})

	gomega.Expect(report.SchemaVersion).To(gomega.BeEquivalentTo(LocalizationUnifiedReportSchemaVersion), fmt.Sprintf("unexpected report identity: %#v", report))
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("unexpected report identity: %#v", report))

	gomega.Expect(localizationClientClassifications).To(gomega.HaveLen(report.Outcome.SampleCount), fmt.Sprintf("outcome missing corpus metrics: %#v", report.Outcome))
	gomega.Expect(report.Outcome.TopKEditPathCoverage).To(gomega.HaveLen(1), fmt.Sprintf("outcome missing corpus metrics: %#v", report.Outcome))

	gomega.Expect(report.Trajectory.Baseline.State).To(gomega.BeEquivalentTo("not_available"), fmt.Sprintf("trajectory should expose neutral baseline/delta and current evidence: %#v", report.Trajectory))
	gomega.Expect(report.Trajectory.Current.Available).To(gomega.BeTrue(), fmt.Sprintf("trajectory should expose neutral baseline/delta and current evidence: %#v", report.Trajectory))
	gomega.Expect(report.Trajectory.Delta.State).To(gomega.BeEquivalentTo("not_available"), fmt.Sprintf("trajectory should expose neutral baseline/delta and current evidence: %#v", report.Trajectory))

	gomega.Expect(report.CacheFreshness.Available).To(gomega.BeTrue(), fmt.Sprintf("cache/freshness missing row freshness evidence: %#v", report.CacheFreshness))
	gomega.Expect(report.CacheFreshness.Fresh).To(gomega.BeTrue(), fmt.Sprintf("cache/freshness missing row freshness evidence: %#v", report.CacheFreshness))
	gomega.Expect(report.CacheFreshness.Rows).To(gomega.BeEquivalentTo(2), fmt.Sprintf("cache/freshness missing row freshness evidence: %#v", report.CacheFreshness))
	gomega.Expect(report.CacheFreshness.NonCurrentRows).To(gomega.BeEquivalentTo(2), fmt.Sprintf("ranker audit events must make pre-generation cache rows non-current: %#v", report.CacheFreshness))

	gomega.Expect(report.Ranker.LearningSummary).NotTo(gomega.BeNil(), fmt.Sprintf("ranker learning summary missing accepted/rejected evidence: %#v", report.Ranker))
	gomega.Expect(report.Ranker.LearningSummary.TotalEvents).To(gomega.BeEquivalentTo(2), fmt.Sprintf("ranker learning summary missing accepted/rejected evidence: %#v", report.Ranker))
	gomega.Expect(report.Ranker.LearningSummary.AcceptedCount).To(gomega.BeEquivalentTo(1), fmt.Sprintf("ranker learning summary missing accepted/rejected evidence: %#v", report.Ranker))
	gomega.Expect(report.Ranker.LearningSummary.RejectedCount).To(gomega.BeEquivalentTo(1), fmt.Sprintf("ranker learning summary missing accepted/rejected evidence: %#v", report.Ranker))

	gomega.Expect(report.Verification.Available).To(gomega.BeTrue(), fmt.Sprintf("verification summary missing bounded status: %#v", report.Verification))
	gomega.Expect(report.Verification.Status).To(gomega.BeEquivalentTo("passed"), fmt.Sprintf("verification summary missing bounded status: %#v", report.Verification))
	gomega.Expect(report.Verification.CheckCount).To(gomega.BeEquivalentTo(1), fmt.Sprintf("verification summary missing bounded status: %#v", report.Verification))
	gomega.Expect(report.Verification.FullSuitePlanObserved).To(gomega.BeTrue(), fmt.Sprintf("verification summary missing bounded status: %#v", report.Verification))

	gomega.Expect(report.AuditMetrics.JoinCoverage.Outcome).To(gomega.BeTrue(), fmt.Sprintf("audit join coverage missing report sections: %#v", report.AuditMetrics.JoinCoverage))
	gomega.Expect(report.AuditMetrics.JoinCoverage.CacheFreshness).To(gomega.BeTrue(), fmt.Sprintf("audit join coverage missing report sections: %#v", report.AuditMetrics.JoinCoverage))
	gomega.Expect(report.AuditMetrics.JoinCoverage.Ranker).To(gomega.BeTrue(), fmt.Sprintf("audit join coverage missing report sections: %#v", report.AuditMetrics.JoinCoverage))
	gomega.Expect(report.AuditMetrics.JoinCoverage.Verification).To(gomega.BeTrue(), fmt.Sprintf("audit join coverage missing report sections: %#v", report.AuditMetrics.JoinCoverage))

	gomega.Expect(report.ClientProduct.MissingClassifications).To(gomega.HaveLen(0), fmt.Sprintf("client classification support is incomplete or branched: %#v", report.ClientProduct))
	gomega.Expect(report.ClientProduct.GenericSupport).To(gomega.BeTrue(), fmt.Sprintf("client classification support is incomplete or branched: %#v", report.ClientProduct))
	gomega.Expect(report.ClientProduct.ClientSpecificBranches).To(gomega.BeFalse(), fmt.Sprintf("client classification support is incomplete or branched: %#v", report.ClientProduct))

	gomega.Expect(report.ClientProduct.ClassificationRows).To(gomega.HaveLen(len(localizationClientClassifications)), fmt.Sprintf("client classification rows should cover every required class: %#v", report.ClientProduct.ClassificationRows))

	for _, row := range report.ClientProduct.ClassificationRows {
		gomega.Expect(row.Status).To(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected covered client classification row, got %#v", row))
		gomega.Expect(row.SampleCount).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected covered client classification row, got %#v", row))
		gomega.Expect(row.Reason).To(gomega.BeEquivalentTo("classification_sample_present"), fmt.Sprintf("expected covered client classification row, got %#v", row))
	}

	gomega.Expect(report.ProductSuccess).To(gomega.HaveLen(9), fmt.Sprintf("expected all product-success gates, got %#v", report.ProductSuccess))

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
		gomega.Expect(gate.Gate).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("missing product-success gate %q in %#v", want, report.ProductSuccess))

		gomega.Expect(gate.Status).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("gate %q should include status, reason, and evidence: %#v", want, gate))
		gomega.Expect(gate.Reason).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("gate %q should include status, reason, and evidence: %#v", want, gate))
		gomega.Expect(gate.Evidence).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("gate %q should include status, reason, and evidence: %#v", want, gate))
	}

	{
		gate := localizationGateByIDForTest(report.ProductSuccess, "icp")
		gomega.Expect(gate.Status).To(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected ICP to pass when every required class has a sample: %#v", gate))
	}

	{
		gate := localizationGateByIDForTest(report.ProductSuccess, "client_demo_proof")
		gomega.Expect(gate.Status).To(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected client demo proof to pass when every required class has a sample: %#v", gate))
	}

	{
		gate := localizationGateByIDForTest(report.ProductSuccess, "competitive_proof")
		gomega.Expect(gate.Status).To(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected competitive proof to pass only with covered competitive conditions, got %#v", gate))
		gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo("competitive_conditions_covered"), fmt.Sprintf("expected competitive proof to pass only with covered competitive conditions, got %#v", gate))
	}

	for _, id := range []string{"retained_use", "integration_proof", "local_product_evidence_export"} {
		gate := localizationGateByIDForTest(report.ProductSuccess, id)
		gomega.Expect(gate.Status).NotTo(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected gate %q to downgrade from same-report non-current cache evidence, got %#v", id, gate))
		gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo("cache_non_current"), fmt.Sprintf("expected gate %q to downgrade from same-report non-current cache evidence, got %#v", id, gate))
		gomega.Expect(gate.Evidence).To(gomega.ContainSubstring("cache_non_current=2"), fmt.Sprintf("expected gate %q to downgrade from same-report non-current cache evidence, got %#v", id, gate))
	}

	gomega.Expect(report.Privacy.PromptTextIncluded).To(gomega.BeFalse(), fmt.Sprintf("privacy flags must exclude raw content: %#v", report.Privacy))
	gomega.Expect(report.Privacy.FileBodiesIncluded).To(gomega.BeFalse(), fmt.Sprintf("privacy flags must exclude raw content: %#v", report.Privacy))
	gomega.Expect(report.Privacy.LogExcerptsIncluded).To(gomega.BeFalse(), fmt.Sprintf("privacy flags must exclude raw content: %#v", report.Privacy))
	gomega.Expect(report.Privacy.SecretsIncluded).To(gomega.BeFalse(), fmt.Sprintf("privacy flags must exclude raw content: %#v", report.Privacy))
})
var _ = ginkgo.It("evaluate localization unified report file sets workspace root", func() {
	t := ginkgo.GinkgoT()
	corpus := validLocalizationCorpusForTest()
	path := filepath.Join(t.TempDir(), "corpus.json")

	data, err := json.Marshal(corpus)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal corpus: %v", err))

	gomega.Expect(os.WriteFile(path, data, 0o644)).To(gomega.Succeed(), "write corpus")

	report, err := EvaluateLocalizationUnifiedReportFile(path, evalTestRepoRoot(), LocalizationUnifiedReportOptions{
		Benchmark: LocalizationBenchmarkOptions{TopK: []int{1}},
		Now:       time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("EvaluateLocalizationUnifiedReportFile: %v", err))

	gomega.Expect(report.Name).To(gomega.BeEquivalentTo(corpus.Name), fmt.Sprintf("unexpected report identity: %#v", report))
	gomega.Expect(report.Outcome.SampleCount).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected report identity: %#v", report))

	gomega.Expect(report.CacheFreshness.Available).To(gomega.BeTrue(), fmt.Sprintf("root argument should populate workspace-root-backed cache report: %#v", report.CacheFreshness))
})
var _ = ginkgo.It("localization unified report explains missing current corpus client samples", func() {
	corpusPath := evalTestdataPath("localization", "recent-implementation-corpus.json")

	corpus, err := ReadLocalizationCorpus(corpusPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadLocalizationCorpus: %v", err))

	report := BuildLocalizationUnifiedReport(corpus, LocalizationUnifiedReportOptions{
		Benchmark: LocalizationBenchmarkOptions{TopK: []int{1}},
		Now:       time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})

	gomega.Expect(report.ClientProduct.ClassificationRows).To(gomega.HaveLen(len(localizationClientClassifications)), fmt.Sprintf("expected one row per required classification, got %#v", report.ClientProduct.ClassificationRows))

	for _, row := range report.ClientProduct.ClassificationRows {
		gomega.Expect(row.Status).To(gomega.BeEquivalentTo("not_available"), fmt.Sprintf("expected explicit missing-classification row, got %#v", row))
		gomega.Expect(row.SampleCount).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected explicit missing-classification row, got %#v", row))
		gomega.Expect(row.Reason).To(gomega.BeEquivalentTo("classification_sample_missing"), fmt.Sprintf("expected explicit missing-classification row, got %#v", row))

		gomega.Expect(row.Label).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("expected useful bounded row evidence, got %#v", row))
		gomega.Expect(row.Evidence).To(gomega.BeEquivalentTo("required_classification_enumerated"), fmt.Sprintf("expected useful bounded row evidence, got %#v", row))
	}

	{
		gate := localizationGateByIDForTest(report.ProductSuccess, "icp")
		gomega.Expect(gate.Status).To(gomega.BeEquivalentTo("partial"), fmt.Sprintf("expected ICP to be partial with explicit missing coverage reason, got %#v", gate))
		gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo("classification_statuses_enumerate_missing_coverage"), fmt.Sprintf("expected ICP to be partial with explicit missing coverage reason, got %#v", gate))
	}

	{
		gate := localizationGateByIDForTest(report.ProductSuccess, "client_demo_proof")
		gomega.Expect(gate.Status).To(gomega.BeEquivalentTo("not_available"), fmt.Sprintf("expected client-demo proof to explain missing classification samples, got %#v", gate))
		gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo("client_classification_samples_missing"), fmt.Sprintf("expected client-demo proof to explain missing classification samples, got %#v", gate))
	}
})
var _ = ginkgo.Describe("localization product success gates with degraded cache evidence", func() {
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
		tt := tt
		ginkgo.It(tt.name, func() {
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
				gomega.Expect(gate.Gate).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("missing product-success gate %q in %#v", id, gates))

				gomega.Expect(gate.Status).NotTo(gomega.BeEquivalentTo("pass"), fmt.Sprintf("gate %q passed despite degraded cache evidence: %#v", id, gate))

				gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo(tt.wantReason), fmt.Sprintf("gate %q should expose degraded cache reason %q, got %#v", id, tt.wantReason, gate))

				gomega.Expect(gate.Evidence).To(gomega.ContainSubstring("cache_stale="), fmt.Sprintf("gate %q should include bounded cache row evidence, got %#v", id, gate))
				gomega.Expect(gate.Evidence).To(gomega.ContainSubstring("cache_non_current="), fmt.Sprintf("gate %q should include bounded cache row evidence, got %#v", id, gate))
			}
		})
	}
})
var _ = ginkgo.It("localization competitive proof gate requires competitive conditions", func() {
	outcome := LocalizationOutcomeReport{
		SampleCount:          1,
		TopKEditPathCoverage: []LocalizationTopKCoverageMetric{{K: 1, Available: true, Numerator: 1, Denominator: 1}},
		BroadReadCount:       LocalizationCountMetric{Available: true, Numerator: 1, Denominator: 1},
		ZeroHitSearchCount:   LocalizationCountMetric{Available: true, Numerator: 0, Denominator: 1},
		CandidatePathWaste:   LocalizationCandidatePathWasteMetric{Available: true, CandidateWasteDenominator: 1, PathWasteDenominator: 1},
	}
	gate := localizationCompetitiveProofGate(outcome, LocalizationTrajectoryReport{Current: LocalizationTrajectorySnapshot{Available: true}})

	gomega.Expect(gate.Status).NotTo(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected competitive proof to reject self-referential metrics without condition coverage, got %#v", gate))
	gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo("competitive_conditions_missing"), fmt.Sprintf("expected competitive proof to reject self-referential metrics without condition coverage, got %#v", gate))

	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"go.mod"}, []LocalizationCandidateFile{{Path: "go.mod"}}, ptrInt(0), ptrInt(0))

	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("go.mod", localizationClientClassifications)
	corpus.Tasks = []LocalizationCorpusTask{task}

	report := BuildLocalizationUnifiedReport(corpus, LocalizationUnifiedReportOptions{Now: time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC)})

	gate = localizationGateByIDForTest(report.ProductSuccess, "competitive_proof")

	gomega.Expect(gate.Status).To(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected measured/documented external competitive conditions to satisfy the gate, got %#v", gate))
	gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo("competitive_conditions_covered"), fmt.Sprintf("expected measured/documented external competitive conditions to satisfy the gate, got %#v", gate))
	gomega.Expect(gate.Evidence).To(gomega.ContainSubstring("competitive_external_covered=2"), fmt.Sprintf("expected measured/documented external competitive conditions to satisfy the gate, got %#v", gate))
})
var _ = ginkgo.It("localization competitive proof gate rejects poisoned client classification", func() {
	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"go.mod"}, []LocalizationCandidateFile{{Path: "go.mod"}}, ptrInt(0), ptrInt(0))

	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("go.mod", []string{"customer-acme-session"})
	corpus.Tasks = []LocalizationCorpusTask{task}

	report := BuildLocalizationUnifiedReport(corpus, LocalizationUnifiedReportOptions{Now: time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC)})
	gate := localizationGateByIDForTest(report.ProductSuccess, "competitive_proof")

	gomega.Expect(gate.Status).NotTo(gomega.BeEquivalentTo("pass"), fmt.Sprintf("expected competitive proof to reject poisoned client classification, got %#v", gate))
	gomega.Expect(gate.Reason).To(gomega.BeEquivalentTo("competitive_condition_client_classification_invalid"), fmt.Sprintf("expected competitive proof to reject poisoned client classification, got %#v", gate))

	gomega.Expect(gate.Evidence).To(gomega.ContainSubstring("competitive_invalid_client_classifications=6"), fmt.Sprintf("expected bounded invalid-classification evidence, got %#v", gate))

	data, err := json.Marshal(report)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal report: %v", err))

	for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("unified report leaked customer-like classification %q: %s", forbidden, string(data)))
	}
})
var _ = ginkgo.It("localization unified report does not echo raw prompt or body text", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	logPath := filepath.Join(root, "verify.jsonl")

	const sentinel = "PROMPT_SENTINEL BEGIN OPENSSH PRIVATE KEY BODY_SENTINEL"

	gomega.Expect(os.WriteFile(logPath, []byte(`{"type":"message","role":"user","content":"`+sentinel+`"}
{"type":"tool_result","agent":"implement_verify","summary":"ok","error":""}
`), 0o644)).To(gomega.Succeed(), "write log")

	report := BuildLocalizationUnifiedReport(validLocalizationCorpusForTest(), LocalizationUnifiedReportOptions{
		WorkspaceRoot:       root,
		VerificationLogPath: logPath,
		Now:                 time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
	})

	data, err := json.Marshal(report)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal: %v", err))

	for _, forbidden := range []string{"PROMPT_SENTINEL", "BEGIN OPENSSH PRIVATE KEY", "BODY_SENTINEL"} {
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("unified report echoed forbidden raw content %q: %s", forbidden, string(data)))
	}
})

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

func writeLocalizationVerificationLog(path string) {
	ginkgo.GinkgoHelper()

	const log = `{"type":"message","role":"verification_plan","content":"{\"commands\":[\"go test ./...\"],\"uses_full_suite\":true,\"coverage\":\"full-suite\",\"confidence\":\"high\",\"risk_level\":\"medium\"}"}
{"type":"tool_result","agent":"implement_verify","summary":"go test ./... passed","error":""}
{"type":"message","role":"verification_feedback","content":"{\"commands\":[\"go test ./...\"],\"failed_commands\":[],\"confidence\":\"high\"}"}
`

	gomega.Expect(os.WriteFile(path, []byte(log), 0o644)).To(gomega.Succeed(), "write localization verification log")
}

func writeLocalizationReportFile(path string, body string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed(), "create localization report directory")

	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed(), "write localization report file")
}
