package eval

import (
	"encoding/json"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var _ = ginkgo.It("read recent implementation localization corpus", func() {
	repoRoot := evalTestRepoRoot()
	corpusPath := filepath.Join(repoRoot, "testdata", "localization", "recent-implementation-corpus.json")

	corpus, err := ReadLocalizationCorpus(corpusPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadLocalizationCorpus: %v", err))

	{
		findings := LintLocalizationCorpus(corpus)
		gomega.Expect(findings).To(gomega.BeEmpty(), fmt.Sprintf("localization corpus lint failed: %v", findings))
	}

	report := EvaluateLocalizationCorpusWithOptions(corpus, repoRoot, DefaultLocalizationBenchmarkOptions())
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected corpus report to pass, findings=%v", report.Findings))

	gomega.Expect(report.TotalTasks).To(gomega.BeEquivalentTo(5), fmt.Sprintf("expected 5 tasks, got %d", report.TotalTasks))

	for _, want := range []string{"telemetry", "conversation"} {
		gomega.Expect(report.SourceCoverage[want]).NotTo(gomega.BeEquivalentTo(0), fmt.Sprintf("expected source coverage for %q, got %#v", want, report.SourceCoverage))
	}

	for _, want := range []string{"graph_freshness_cache", "capability_routing", "prompt_recovery_text", "audit_telemetry", "docs_plan_tracking"} {
		gomega.Expect(report.ThemeCoverage[want]).NotTo(gomega.BeEquivalentTo(0), fmt.Sprintf("expected theme coverage for %q, got %#v", want, report.ThemeCoverage))
	}

	gomega.Expect(report.RedactionStatus["redacted"]).To(gomega.BeEquivalentTo(report.TotalTasks), fmt.Sprintf("unexpected redaction status: %#v", report.RedactionStatus))
	gomega.Expect(report.RedactionStatus["corpus_redacted"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected redaction status: %#v", report.RedactionStatus))

	gomega.Expect(report.ExpectedFiles).NotTo(gomega.BeEquivalentTo(0), fmt.Sprintf("expected non-empty anchors, got files=%d symbols=%d tests=%d", report.ExpectedFiles, report.ExpectedSymbols, report.ExpectedTests))
	gomega.Expect(report.ExpectedSymbols).NotTo(gomega.BeEquivalentTo(0), fmt.Sprintf("expected non-empty anchors, got files=%d symbols=%d tests=%d", report.ExpectedFiles, report.ExpectedSymbols, report.ExpectedTests))
	gomega.Expect(report.ExpectedTests).NotTo(gomega.BeEquivalentTo(0), fmt.Sprintf("expected non-empty anchors, got files=%d symbols=%d tests=%d", report.ExpectedFiles, report.ExpectedSymbols, report.ExpectedTests))
})
var _ = ginkgo.It("localization corpus privacy scan", func() {
	corpusPath := evalTestdataPath("localization", "recent-implementation-corpus.json")

	data, err := os.ReadFile(corpusPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read corpus: %v", err))

	body := string(data)
	for _, forbidden := range []string{
		"BEGIN PRIVATE KEY",
		"Authorization:",
		"Bearer ",
		"AKIA",
		"ghp_",
		"raw_prompt",
		"raw_transcript",
		"file_body_excerpt",
	} {
		gomega.Expect(body).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("corpus contains forbidden privacy marker %q", forbidden))
	}

	gomega.Expect(regexp.MustCompile(`sk-[A-Za-z0-9]{8,}`).MatchString(body)).To(gomega.BeFalse(), fmt.Sprint("corpus contains a secret-shaped sk token"))
})
var _ = ginkgo.It("lint localization corpus rejects weak privacy", func() {
	corpus := validLocalizationCorpusForTest()

	corpus.Tasks[0].Privacy.PromptTextIncluded = true

	{
		findings := LintLocalizationCorpus(corpus)
		gomega.Expect(findings).NotTo(gomega.HaveLen(0), fmt.Sprint("expected lint to reject included prompt text"))
	}
})
var _ = ginkgo.It("evaluate localization corpus reports missing expected path", func() {
	t := ginkgo.GinkgoT()
	corpus := validLocalizationCorpusForTest()

	corpus.Tasks[0].Expected.Files[0].Path = "missing/file.go"

	report := EvaluateLocalizationCorpusWithOptions(corpus, t.TempDir(), DefaultLocalizationBenchmarkOptions())

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected missing expected path to fail report"))

	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("expected file missing"), fmt.Sprintf("expected missing file finding, got %v", report.Findings))
})
var _ = ginkgo.It("evaluate localization corpus file with options loads and lints", func() {
	t := ginkgo.GinkgoT()
	corpus := validLocalizationCorpusForTest()
	path := filepath.Join(t.TempDir(), "corpus.json")

	data, err := json.Marshal(corpus)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal corpus: %v", err))

	gomega.Expect(os.WriteFile(path, data, 0o644)).To(gomega.Succeed(), "write corpus")

	report, err := EvaluateLocalizationCorpusFileWithOptions(path, evalTestRepoRoot(), LocalizationBenchmarkOptions{TopK: []int{1}})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("EvaluateLocalizationCorpusFileWithOptions: %v", err))

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("unexpected corpus report: %#v", report))
	gomega.Expect(report.TotalTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected corpus report: %#v", report))
	gomega.Expect(report.Benchmark.TopKEditPathCoverage).To(gomega.HaveLen(1), fmt.Sprintf("unexpected corpus report: %#v", report))
})
var _ = ginkgo.It("localization benchmark metrics use expected anchors", func() {
	corpus := validLocalizationCorpusForTest()

	corpus.Tasks = []LocalizationCorpusTask{
		localizationBenchmarkTaskForTest("task-one", []string{"a.go", "b.go"}, []LocalizationCandidateFile{
			{Path: "a.go"},
			{Path: "noise.go"},
			{Path: "b.go"},
		}, ptrInt(2), ptrInt(1)),
		localizationBenchmarkTaskForTest("task-two", []string{"c.go"}, []LocalizationCandidateFile{
			{Path: "noise.go"},
			{Path: "other.go"},
		}, ptrInt(0), ptrInt(0)),
	}

	report := AnalyzeLocalizationCorpusWithOptions(corpus, LocalizationBenchmarkOptions{TopK: []int{1, 3}})

	gomega.Expect(report.Benchmark.TopKEditPathCoverage).To(gomega.HaveLen(2), fmt.Sprintf("expected two top-k metrics, got %#v", report.Benchmark.TopKEditPathCoverage))

	top1 := report.Benchmark.TopKEditPathCoverage[0]
	gomega.Expect(top1.K).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected top-1 coverage: %#v", top1))
	gomega.Expect(top1.Numerator).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected top-1 coverage: %#v", top1))
	gomega.Expect(top1.Denominator).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected top-1 coverage: %#v", top1))
	gomega.Expect(top1.Available).To(gomega.BeTrue(), fmt.Sprintf("unexpected top-1 coverage: %#v", top1))

	top3 := report.Benchmark.TopKEditPathCoverage[1]
	gomega.Expect(top3.K).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected top-3 coverage: %#v", top3))
	gomega.Expect(top3.Numerator).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected top-3 coverage: %#v", top3))
	gomega.Expect(top3.Denominator).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected top-3 coverage: %#v", top3))
	gomega.Expect(top3.Available).To(gomega.BeTrue(), fmt.Sprintf("unexpected top-3 coverage: %#v", top3))

	gomega.Expect(report.Benchmark.BroadReadCount.Numerator).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected broad-read metric: %#v", report.Benchmark.BroadReadCount))
	gomega.Expect(report.Benchmark.BroadReadCount.Denominator).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected broad-read metric: %#v", report.Benchmark.BroadReadCount))
	gomega.Expect(report.Benchmark.BroadReadCount.Available).To(gomega.BeTrue(), fmt.Sprintf("unexpected broad-read metric: %#v", report.Benchmark.BroadReadCount))

	gomega.Expect(report.Benchmark.ZeroHitSearchCount.Numerator).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected zero-hit metric: %#v", report.Benchmark.ZeroHitSearchCount))
	gomega.Expect(report.Benchmark.ZeroHitSearchCount.Denominator).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected zero-hit metric: %#v", report.Benchmark.ZeroHitSearchCount))
	gomega.Expect(report.Benchmark.ZeroHitSearchCount.Available).To(gomega.BeTrue(), fmt.Sprintf("unexpected zero-hit metric: %#v", report.Benchmark.ZeroHitSearchCount))

	waste := report.Benchmark.CandidatePathWaste
	gomega.Expect(waste.CandidateWasteNumerator).To(gomega.BeEquivalentTo(7), fmt.Sprintf("unexpected candidate waste: %#v", waste))
	gomega.Expect(waste.CandidateWasteDenominator).To(gomega.BeEquivalentTo(11), fmt.Sprintf("unexpected candidate waste: %#v", waste))

	gomega.Expect(waste.PathWasteNumerator).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected path waste: %#v", waste))
	gomega.Expect(waste.PathWasteDenominator).To(gomega.BeEquivalentTo(5), fmt.Sprintf("unexpected path waste: %#v", waste))
})
var _ = ginkgo.It("localization benchmark reports missing and partial inputs", func() {
	corpus := validLocalizationCorpusForTest()
	report := AnalyzeLocalizationCorpusWithOptions(corpus, DefaultLocalizationBenchmarkOptions())

	gomega.Expect(report.Benchmark.TopKEditPathCoverage[0].Available).To(gomega.BeFalse(), fmt.Sprintf("expected top-k unavailable without context packet candidates: %#v", report.Benchmark.TopKEditPathCoverage[0]))

	gomega.Expect(strings.Join(report.Benchmark.TopKEditPathCoverage[0].FailureReasons, ",")).To(gomega.ContainSubstring("context_packet_file_candidates_missing"), fmt.Sprintf("expected context packet failure reason, got %#v", report.Benchmark.TopKEditPathCoverage[0].FailureReasons))

	gomega.Expect(report.Benchmark.BroadReadCount.Available).To(gomega.BeFalse(), fmt.Sprintf("expected missing trajectory reason, got %#v", report.Benchmark.BroadReadCount))
	gomega.Expect(strings.Join(report.Benchmark.BroadReadCount.FailureReasons, ",")).To(gomega.ContainSubstring("trajectory_input_missing"), fmt.Sprintf("expected missing trajectory reason, got %#v", report.Benchmark.BroadReadCount))

	corpus.Tasks = append(corpus.Tasks, localizationBenchmarkTaskForTest("task-two", []string{"b.go"}, []LocalizationCandidateFile{{Path: "b.go"}}, nil, nil))
	report = AnalyzeLocalizationCorpusWithOptions(corpus, LocalizationBenchmarkOptions{TopK: []int{1}})

	failures := strings.Join(report.Benchmark.TopKEditPathCoverage[0].FailureReasons, ",")

	gomega.Expect(failures).To(gomega.ContainSubstring("partial_context_packet_candidates"), fmt.Sprintf("expected partial context packet failure reason, got %#v", report.Benchmark.TopKEditPathCoverage[0]))
})
var _ = ginkgo.It("localization benchmark reports competitive conditions", func() {
	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"a.go"}, []LocalizationCandidateFile{{Path: "a.go"}}, ptrInt(0), ptrInt(0))

	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("a.go", localizationClientClassifications)
	corpus.Tasks = []LocalizationCorpusTask{task}

	report := AnalyzeLocalizationCorpusWithOptions(corpus, LocalizationBenchmarkOptions{TopK: []int{1}})
	gomega.Expect(report.Benchmark.CompetitiveConditions).To(gomega.HaveLen(len(localizationCompetitiveBaselineDefinitions)), fmt.Sprintf("expected one competitive metric per required baseline, got %#v", report.Benchmark.CompetitiveConditions))

	raw := localizationCompetitiveConditionByBaselineForTest(report.Benchmark.CompetitiveConditions, "raw_search_read")
	gomega.Expect(raw.Status).To(gomega.BeEquivalentTo("measured"), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))
	gomega.Expect(raw.MeasuredTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))
	gomega.Expect(raw.BudgetedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))
	gomega.Expect(raw.ReproducibleTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))
	gomega.Expect(raw.CommandedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))
	gomega.Expect(raw.ArtifactedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))
	gomega.Expect(raw.VerifiedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))
	gomega.Expect(raw.MissingTasks).To(gomega.BeEquivalentTo(0), fmt.Sprintf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw))

	gomega.Expect(raw.TopKEditPathCoverage).To(gomega.HaveLen(1), fmt.Sprintf("raw search/read condition should expose top-k coverage, got %#v", raw.TopKEditPathCoverage))
	gomega.Expect(raw.TopKEditPathCoverage[0].Numerator).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should expose top-k coverage, got %#v", raw.TopKEditPathCoverage))
	gomega.Expect(raw.TopKEditPathCoverage[0].Denominator).To(gomega.BeEquivalentTo(1), fmt.Sprintf("raw search/read condition should expose top-k coverage, got %#v", raw.TopKEditPathCoverage))

	gomega.Expect(raw.ClientCoverage).To(gomega.HaveLen(len(localizationClientClassifications)), fmt.Sprintf("competitive condition should carry all client classifications, got %#v", raw.ClientCoverage))

	aider := localizationCompetitiveConditionByBaselineForTest(report.Benchmark.CompetitiveConditions, "aider_repo_map")
	gomega.Expect(aider.Status).To(gomega.BeEquivalentTo("documented_infeasible"), fmt.Sprintf("Aider-style condition should record bounded infeasibility, got %#v", aider))
	gomega.Expect(aider.InfeasibleTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("Aider-style condition should record bounded infeasibility, got %#v", aider))
	gomega.Expect(aider.CommandedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("Aider-style condition should record bounded infeasibility, got %#v", aider))
	gomega.Expect(aider.ArtifactedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("Aider-style condition should record bounded infeasibility, got %#v", aider))
	gomega.Expect(aider.VerifiedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("Aider-style condition should record bounded infeasibility, got %#v", aider))
	gomega.Expect(aider.InfeasibilityReasons["tool_unavailable"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("Aider-style condition should record bounded infeasibility, got %#v", aider))
	gomega.Expect(aider.ClaimImpacts["claim_weakened_until_external_run"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("Aider-style condition should record bounded infeasibility, got %#v", aider))

	gomega.Expect(aider.FailureReasons).To(gomega.BeEmpty(), fmt.Sprintf("documented external infeasibility should not be treated as missing evidence, got %#v", aider.FailureReasons))

	gomega.Expect(strings.Join(report.Benchmark.MissingInputs, ",")).NotTo(gomega.ContainSubstring("competitive"), fmt.Sprintf("complete competitive conditions should not be reported missing, got %#v", report.Benchmark.MissingInputs))
})
var _ = ginkgo.It("localization competitive baseline id helpers", func() {
	all := LocalizationCompetitiveBaselineIDs()
	external := RequiredLocalizationExternalCompetitiveBaselineIDs()

	gomega.Expect(all).NotTo(gomega.BeEmpty(), fmt.Sprint("expected competitive baseline IDs"))

	gomega.Expect(external).NotTo(gomega.BeEmpty(), fmt.Sprint("expected external competitive baseline IDs"))

	for _, want := range []string{"raw_search_read", "context_pack", "aider_repo_map"} {
		gomega.Expect(containsStringFold(all, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in all baseline IDs: %#v", want, all))
	}

	gomega.Expect(containsStringFold(external, "aider_repo_map")).To(gomega.BeTrue(), fmt.Sprintf("expected aider_repo_map to be an external baseline: %#v", external))

	gomega.Expect(containsStringFold(external, "context_pack")).To(gomega.BeFalse(), fmt.Sprintf("context_pack is internal and should not be required external baseline: %#v", external))
})
var _ = ginkgo.It("localization benchmark constrains competitive condition client classifications", func() {
	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"a.go"}, []LocalizationCandidateFile{{Path: "a.go"}}, ptrInt(0), ptrInt(0))

	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("a.go", []string{"finance", "Healthcare and Life Sciences", "customer-acme-session"})
	corpus.Tasks = []LocalizationCorpusTask{task}

	report := AnalyzeLocalizationCorpusWithOptions(corpus, LocalizationBenchmarkOptions{TopK: []int{1}})
	raw := localizationCompetitiveConditionByBaselineForTest(report.Benchmark.CompetitiveConditions, "raw_search_read")

	gomega.Expect(raw.ClientCoverage["finance"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("approved client classifications should be retained, got %#v", raw.ClientCoverage))
	gomega.Expect(raw.ClientCoverage["healthcare_life_sciences"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("approved client classifications should be retained, got %#v", raw.ClientCoverage))

	{
		_, ok := raw.ClientCoverage["customer_acme_session"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("unknown customer-like classification leaked into coverage: %#v", raw.ClientCoverage))
	}

	gomega.Expect(raw.InvalidClientClasses).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected one bounded invalid-classification signal, got %#v", raw))

	gomega.Expect(raw.Status).NotTo(gomega.BeEquivalentTo("measured"), fmt.Sprintf("poisoned classification should make competitive condition partial with bounded reason, got %#v", raw))
	gomega.Expect(strings.Join(raw.FailureReasons, ",")).To(gomega.ContainSubstring(localizationFailureCompetitiveClientClasses), fmt.Sprintf("poisoned classification should make competitive condition partial with bounded reason, got %#v", raw))

	gomega.Expect(strings.Join(report.Benchmark.MissingInputs, ",")).To(gomega.ContainSubstring("competitive_condition_client_classification"), fmt.Sprintf("missing inputs should include bounded client-classification signal, got %#v", report.Benchmark.MissingInputs))

	data, err := json.Marshal(report)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal report: %v", err))

	for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("report leaked customer-like classification %q: %s", forbidden, string(data)))
	}

	message := strings.Join(LintLocalizationCorpus(corpus), "\n")
	gomega.Expect(message).To(gomega.ContainSubstring("approved bounded classes"), fmt.Sprintf("expected bounded lint reason, got %v", message))

	for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
		gomega.Expect(message).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("lint leaked customer-like classification %q: %v", forbidden, message))
	}
})
var _ = ginkgo.It("lint localization corpus rejects invalid benchmark evidence", func() {
	corpus := validLocalizationCorpusForTest()
	negative := -1

	corpus.Tasks[0].Benchmark = &LocalizationBenchmarkInput{
		ContextPacket: &LocalizationContextPacketEvidence{
			Files: []LocalizationCandidateFile{{Path: "../outside.go"}},
		},
		Trajectory: &LocalizationTrajectoryEvidence{
			BroadReadCount: &negative,
		},
	}

	findings := strings.Join(LintLocalizationCorpus(corpus), "\n")

	for _, want := range []string{"context packet candidate file path", "broad_read_count must be non-negative"} {
		gomega.Expect(findings).To(gomega.ContainSubstring(want), fmt.Sprintf("expected lint finding to contain %q, got %v", want, findings))
	}
})
var _ = ginkgo.It("lint localization corpus rejects backslash traversal evidence path", func() {
	corpus := validLocalizationCorpusForTest()

	corpus.Tasks[0].Benchmark = &LocalizationBenchmarkInput{
		ContextPacket: &LocalizationContextPacketEvidence{
			Files: []LocalizationCandidateFile{{Path: `..\outside.go`}},
		},
	}

	findings := strings.Join(LintLocalizationCorpus(corpus), "\n")

	gomega.Expect(findings).To(gomega.ContainSubstring("context packet candidate file path"), fmt.Sprintf("expected candidate path lint finding, got %v", findings))
})
var _ = ginkgo.It("lint localization corpus rejects invalid competitive condition evidence", func() {
	corpus := validLocalizationCorpusForTest()

	corpus.Tasks[0].Benchmark = &LocalizationBenchmarkInput{
		CompetitiveConditions: []LocalizationCompetitiveConditionInput{{
			Baseline:            "aider_repo_map",
			Status:              "infeasible",
			CommandID:           "aider_probe",
			InfeasibilityReason: "raw_prompt",
			ClaimImpact:         "claim_weakened_until_external_run",
		}},
	}

	findings := strings.Join(LintLocalizationCorpus(corpus), "\n")

	gomega.Expect(findings).To(gomega.ContainSubstring("infeasibility_reason"), fmt.Sprintf("expected sensitive-marker lint finding, got %v", findings))
	gomega.Expect(findings).To(gomega.ContainSubstring("sensitive or raw-content marker"), fmt.Sprintf("expected sensitive-marker lint finding, got %v", findings))
})
var _ = ginkgo.It("localization relative path allows telemetry tool colon filenames", func() {
	path, err := normalizeLocalizationRelativePath("logs/telemetry/20260513-080646.439-tool:context_pack-59782.jsonl")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected telemetry filename colon to be allowed: %v", err))

	gomega.Expect(path).To(gomega.BeEquivalentTo("logs/telemetry/20260513-080646.439-tool:context_pack-59782.jsonl"), fmt.Sprintf("unexpected normalized path %q", path))
})
var _ = ginkgo.It("localization relative path rejects scheme or drive colon", func() {
	for _, path := range []string{"file:logs/telemetry/sample.jsonl", "C:/logs/telemetry/sample.jsonl"} {
		{
			_, err := normalizeLocalizationRelativePath(path)
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected %q to be rejected", path))
		}
	}
})

func validLocalizationCorpusForTest() LocalizationCorpus {
	return LocalizationCorpus{
		Name:         "test-localization",
		Version:      1,
		Experimental: true,
		SourceWindow: LocalizationSourceWindow{
			StartedAt:   "2026-05-12T00:00:00Z",
			EndedAt:     "2026-05-13T00:00:00Z",
			SourceRoots: []string{"logs/telemetry"},
		},
		PrivacyReview: LocalizationPrivacyReview{
			Status:                   "redacted",
			ReviewedAt:               "2026-05-13T00:00:00Z",
			SensitiveContentExcluded: true,
		},
		Tasks: []LocalizationCorpusTask{{
			ID:                "task",
			Name:              "Task",
			Summary:           "Locate implementation anchors.",
			Themes:            []string{"graph"},
			ClientProductArea: "benchmarks",
			SourceRefs: []LocalizationSourceRef{{
				Kind:       "telemetry",
				Ref:        "sample",
				LogPath:    "logs/telemetry/sample.jsonl",
				ObservedAt: "2026-05-13T00:00:00Z",
				Digest:     "sha256:0123456789abcdef",
			}},
			Expected: LocalizationExpectedAnchors{
				Files: []LocalizationExpectedFile{{
					Path: "go.mod",
				}},
				Symbols: []LocalizationExpectedSymbol{{
					Path: "go.mod",
					Name: "module",
				}},
				Tests: []LocalizationExpectedTest{{
					Path: "go.mod",
					Name: "module",
				}},
			},
			Privacy: LocalizationTaskPrivacy{
				Status:                   "redacted",
				SensitiveContentExcluded: true,
			},
			SensitiveExclusionNote: "Only sanitized metadata is included.",
			Provenance: LocalizationTaskProvenance{
				SelectionReason: "Covers recent localization work.",
				SanitizedFields: []string{"timestamp", "tool", "path"},
				SourceCoverage:  []string{"telemetry"},
			},
		}},
	}
}

func localizationBenchmarkTaskForTest(id string, expectedFiles []string, candidateFiles []LocalizationCandidateFile, broadReads *int, zeroHitSearches *int) LocalizationCorpusTask {
	task := validLocalizationCorpusForTest().Tasks[0]

	task.ID = id
	task.Name = id
	task.Expected.Files = make([]LocalizationExpectedFile, 0, len(expectedFiles))

	for _, path := range expectedFiles {
		task.Expected.Files = append(task.Expected.Files, LocalizationExpectedFile{Path: path})
	}

	task.Expected.Symbols = []LocalizationExpectedSymbol{{
		Path: expectedFiles[0],
		Name: "DoThing",
	}}
	task.Expected.Tests = []LocalizationExpectedTest{{
		Path: strings.TrimSuffix(expectedFiles[0], ".go") + "_test.go",
		Name: "TestDoThing",
	}}
	task.Benchmark = &LocalizationBenchmarkInput{
		ContextPacket: &LocalizationContextPacketEvidence{
			Files: candidateFiles,
			Symbols: []LocalizationCandidateSymbol{
				{Path: "noise.go", Name: "Nope"},
				{Path: expectedFiles[0], Name: "DoThing"},
			},
			Tests: []LocalizationCandidateTest{
				{Path: "noise_test.go", Name: "TestNoise"},
			},
		},
		Trajectory: &LocalizationTrajectoryEvidence{
			BroadReadCount:     broadReads,
			ZeroHitSearchCount: zeroHitSearches,
		},
	}

	return task
}

func localizationCompetitiveConditionsForTest(expectedPath string, classifications []string) []LocalizationCompetitiveConditionInput {
	measured := []string{
		"raw_search_read",
		"generated_baseline_discovery_report",
		"generated_current_discovery_report",
		"context_pack",
	}
	conditions := make([]LocalizationCompetitiveConditionInput, 0, len(localizationCompetitiveBaselineDefinitions))

	for _, baseline := range measured {
		conditions = append(conditions, LocalizationCompetitiveConditionInput{
			Baseline:              baseline,
			Status:                "measured",
			CandidateFiles:        []LocalizationCandidateFile{{Path: expectedPath}},
			Budget:                &LocalizationCompetitiveBudgetEvidence{ToolCallBudget: 8, WallClockBudgetMS: 60000, UsedToolCalls: 3, ElapsedMS: 1200},
			CommandID:             baseline + "_fixture",
			ArtifactPath:          "testdata/localization/competitive/" + baseline + ".json",
			VerificationMethod:    "fixture_replay",
			ClientClassifications: append([]string(nil), classifications...),
		})
	}

	for _, baseline := range []string{"aider_repo_map", "external_agent_native"} {
		conditions = append(conditions, LocalizationCompetitiveConditionInput{
			Baseline:              baseline,
			Status:                "infeasible",
			CommandID:             baseline + "_probe",
			ArtifactPath:          "testdata/localization/competitive/" + baseline + ".json",
			VerificationMethod:    "fixture_probe",
			InfeasibilityReason:   "tool_unavailable",
			ClaimImpact:           "claim_weakened_until_external_run",
			ClientClassifications: append([]string(nil), classifications...),
		})
	}

	return conditions
}

func localizationCompetitiveConditionByBaselineForTest(conditions []LocalizationCompetitiveConditionMetric, baseline string) LocalizationCompetitiveConditionMetric {
	for _, condition := range conditions {
		if condition.Baseline == baseline {
			return condition
		}
	}

	return LocalizationCompetitiveConditionMetric{}
}

func ptrInt(value int) *int {
	return &value
}
