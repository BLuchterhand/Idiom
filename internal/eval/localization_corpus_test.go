package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadRecentImplementationLocalizationCorpus(t *testing.T) {
	corpusPath := filepath.Join("..", "..", "testdata", "localization", "recent-implementation-corpus.json")
	corpus, err := LoadLocalizationCorpus(corpusPath)
	if err != nil {
		t.Fatalf("LoadLocalizationCorpus: %v", err)
	}
	report := EvaluateLocalizationCorpus(corpus, filepath.Join("..", ".."))
	if !report.Passed {
		t.Fatalf("expected corpus report to pass, findings=%v", report.Findings)
	}
	if report.TotalTasks != 5 {
		t.Fatalf("expected 5 tasks, got %d", report.TotalTasks)
	}
	for _, want := range []string{"telemetry", "conversation"} {
		if report.SourceCoverage[want] == 0 {
			t.Fatalf("expected source coverage for %q, got %#v", want, report.SourceCoverage)
		}
	}
	for _, want := range []string{"graph_freshness_cache", "capability_routing", "prompt_recovery_text", "audit_telemetry", "docs_plan_tracking"} {
		if report.ThemeCoverage[want] == 0 {
			t.Fatalf("expected theme coverage for %q, got %#v", want, report.ThemeCoverage)
		}
	}
	if report.RedactionStatus["redacted"] != report.TotalTasks || report.RedactionStatus["corpus_redacted"] != 1 {
		t.Fatalf("unexpected redaction status: %#v", report.RedactionStatus)
	}
	if report.ExpectedFiles == 0 || report.ExpectedSymbols == 0 || report.ExpectedTests == 0 {
		t.Fatalf("expected non-empty anchors, got files=%d symbols=%d tests=%d", report.ExpectedFiles, report.ExpectedSymbols, report.ExpectedTests)
	}
}

func TestLocalizationCorpusPrivacyScan(t *testing.T) {
	corpusPath := filepath.Join("..", "..", "testdata", "localization", "recent-implementation-corpus.json")
	data, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
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
		if strings.Contains(body, forbidden) {
			t.Fatalf("corpus contains forbidden privacy marker %q", forbidden)
		}
	}
	if regexp.MustCompile(`sk-[A-Za-z0-9]{8,}`).MatchString(body) {
		t.Fatal("corpus contains a secret-shaped sk token")
	}
}

func TestValidateLocalizationCorpusRejectsWeakPrivacy(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	corpus.Tasks[0].Privacy.PromptTextIncluded = true
	if err := ValidateLocalizationCorpus(corpus); err == nil {
		t.Fatal("expected validation to reject included prompt text")
	}
}

func TestEvaluateLocalizationCorpusReportsMissingExpectedPath(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	corpus.Tasks[0].Expected.Files[0].Path = "missing/file.go"
	report := EvaluateLocalizationCorpus(corpus, t.TempDir())
	if report.Passed {
		t.Fatal("expected missing expected path to fail report")
	}
	if !strings.Contains(strings.Join(report.Findings, "\n"), "expected file missing") {
		t.Fatalf("expected missing file finding, got %v", report.Findings)
	}
}

func TestLocalizationBenchmarkMetricsUseExpectedAnchors(t *testing.T) {
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

	if len(report.Benchmark.TopKEditPathCoverage) != 2 {
		t.Fatalf("expected two top-k metrics, got %#v", report.Benchmark.TopKEditPathCoverage)
	}
	top1 := report.Benchmark.TopKEditPathCoverage[0]
	if top1.K != 1 || top1.Numerator != 1 || top1.Denominator != 3 || !top1.Available {
		t.Fatalf("unexpected top-1 coverage: %#v", top1)
	}
	top3 := report.Benchmark.TopKEditPathCoverage[1]
	if top3.K != 3 || top3.Numerator != 2 || top3.Denominator != 3 || !top3.Available {
		t.Fatalf("unexpected top-3 coverage: %#v", top3)
	}
	if report.Benchmark.BroadReadCount.Numerator != 2 || report.Benchmark.BroadReadCount.Denominator != 2 || !report.Benchmark.BroadReadCount.Available {
		t.Fatalf("unexpected broad-read metric: %#v", report.Benchmark.BroadReadCount)
	}
	if report.Benchmark.ZeroHitSearchCount.Numerator != 1 || report.Benchmark.ZeroHitSearchCount.Denominator != 2 || !report.Benchmark.ZeroHitSearchCount.Available {
		t.Fatalf("unexpected zero-hit metric: %#v", report.Benchmark.ZeroHitSearchCount)
	}
	waste := report.Benchmark.CandidatePathWaste
	if waste.CandidateWasteNumerator != 7 || waste.CandidateWasteDenominator != 11 {
		t.Fatalf("unexpected candidate waste: %#v", waste)
	}
	if waste.PathWasteNumerator != 3 || waste.PathWasteDenominator != 5 {
		t.Fatalf("unexpected path waste: %#v", waste)
	}
}

func TestLocalizationBenchmarkReportsMissingAndPartialInputs(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	report := AnalyzeLocalizationCorpus(corpus)
	if report.Benchmark.TopKEditPathCoverage[0].Available {
		t.Fatalf("expected top-k unavailable without context packet candidates: %#v", report.Benchmark.TopKEditPathCoverage[0])
	}
	if !strings.Contains(strings.Join(report.Benchmark.TopKEditPathCoverage[0].FailureReasons, ","), "context_packet_file_candidates_missing") {
		t.Fatalf("expected context packet failure reason, got %#v", report.Benchmark.TopKEditPathCoverage[0].FailureReasons)
	}
	if report.Benchmark.BroadReadCount.Available || !strings.Contains(strings.Join(report.Benchmark.BroadReadCount.FailureReasons, ","), "trajectory_input_missing") {
		t.Fatalf("expected missing trajectory reason, got %#v", report.Benchmark.BroadReadCount)
	}

	corpus.Tasks = append(corpus.Tasks, localizationBenchmarkTaskForTest("task-two", []string{"b.go"}, []LocalizationCandidateFile{{Path: "b.go"}}, nil, nil))
	report = AnalyzeLocalizationCorpusWithOptions(corpus, LocalizationBenchmarkOptions{TopK: []int{1}})
	failures := strings.Join(report.Benchmark.TopKEditPathCoverage[0].FailureReasons, ",")
	if !strings.Contains(failures, "partial_context_packet_candidates") {
		t.Fatalf("expected partial context packet failure reason, got %#v", report.Benchmark.TopKEditPathCoverage[0])
	}
}

func TestLocalizationBenchmarkReportsCompetitiveConditions(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"a.go"}, []LocalizationCandidateFile{{Path: "a.go"}}, ptrInt(0), ptrInt(0))
	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("a.go", localizationClientClassifications)
	corpus.Tasks = []LocalizationCorpusTask{task}

	report := AnalyzeLocalizationCorpusWithOptions(corpus, LocalizationBenchmarkOptions{TopK: []int{1}})
	if len(report.Benchmark.CompetitiveConditions) != len(localizationCompetitiveBaselineDefinitions) {
		t.Fatalf("expected one competitive metric per required baseline, got %#v", report.Benchmark.CompetitiveConditions)
	}
	raw := localizationCompetitiveConditionByBaselineForTest(report.Benchmark.CompetitiveConditions, "raw_search_read")
	if raw.Status != "measured" || raw.MeasuredTasks != 1 || raw.BudgetedTasks != 1 || raw.ReproducibleTasks != 1 || raw.CommandedTasks != 1 || raw.ArtifactedTasks != 1 || raw.VerifiedTasks != 1 || raw.MissingTasks != 0 {
		t.Fatalf("raw search/read condition should be measured, budgeted, and reproducible: %#v", raw)
	}
	if len(raw.TopKEditPathCoverage) != 1 || raw.TopKEditPathCoverage[0].Numerator != 1 || raw.TopKEditPathCoverage[0].Denominator != 1 {
		t.Fatalf("raw search/read condition should expose top-k coverage, got %#v", raw.TopKEditPathCoverage)
	}
	if len(raw.ClientCoverage) != len(localizationClientClassifications) {
		t.Fatalf("competitive condition should carry all client classifications, got %#v", raw.ClientCoverage)
	}
	aider := localizationCompetitiveConditionByBaselineForTest(report.Benchmark.CompetitiveConditions, "aider_repo_map")
	if aider.Status != "documented_infeasible" || aider.InfeasibleTasks != 1 || aider.CommandedTasks != 1 || aider.ArtifactedTasks != 1 || aider.VerifiedTasks != 1 || aider.InfeasibilityReasons["tool_unavailable"] != 1 || aider.ClaimImpacts["claim_weakened_until_external_run"] != 1 {
		t.Fatalf("Aider-style condition should record bounded infeasibility, got %#v", aider)
	}
	if len(aider.FailureReasons) != 0 {
		t.Fatalf("documented external infeasibility should not be treated as missing evidence, got %#v", aider.FailureReasons)
	}
	if strings.Contains(strings.Join(report.Benchmark.MissingInputs, ","), "competitive") {
		t.Fatalf("complete competitive conditions should not be reported missing, got %#v", report.Benchmark.MissingInputs)
	}
}

func TestLocalizationBenchmarkConstrainsCompetitiveConditionClientClassifications(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	task := localizationBenchmarkTaskForTest("competitive-task", []string{"a.go"}, []LocalizationCandidateFile{{Path: "a.go"}}, ptrInt(0), ptrInt(0))
	task.Benchmark.CompetitiveConditions = localizationCompetitiveConditionsForTest("a.go", []string{"finance", "Healthcare and Life Sciences", "customer-acme-session"})
	corpus.Tasks = []LocalizationCorpusTask{task}

	report := AnalyzeLocalizationCorpusWithOptions(corpus, LocalizationBenchmarkOptions{TopK: []int{1}})
	raw := localizationCompetitiveConditionByBaselineForTest(report.Benchmark.CompetitiveConditions, "raw_search_read")
	if raw.ClientCoverage["finance"] != 1 || raw.ClientCoverage["healthcare_life_sciences"] != 1 {
		t.Fatalf("approved client classifications should be retained, got %#v", raw.ClientCoverage)
	}
	if _, ok := raw.ClientCoverage["customer_acme_session"]; ok {
		t.Fatalf("unknown customer-like classification leaked into coverage: %#v", raw.ClientCoverage)
	}
	if raw.InvalidClientClasses != 1 {
		t.Fatalf("expected one bounded invalid-classification signal, got %#v", raw)
	}
	if raw.Status == "measured" || !strings.Contains(strings.Join(raw.FailureReasons, ","), localizationFailureCompetitiveClientClasses) {
		t.Fatalf("poisoned classification should make competitive condition partial with bounded reason, got %#v", raw)
	}
	if !strings.Contains(strings.Join(report.Benchmark.MissingInputs, ","), "competitive_condition_client_classification") {
		t.Fatalf("missing inputs should include bounded client-classification signal, got %#v", report.Benchmark.MissingInputs)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("report leaked customer-like classification %q: %s", forbidden, string(data))
		}
	}
	if err := ValidateLocalizationCorpus(corpus); err == nil {
		t.Fatal("expected corpus validation to reject unapproved competitive client classification")
	} else {
		message := err.Error()
		if !strings.Contains(message, "approved bounded classes") {
			t.Fatalf("expected bounded validation reason, got %v", err)
		}
		for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
			if strings.Contains(message, forbidden) {
				t.Fatalf("validation leaked customer-like classification %q: %v", forbidden, err)
			}
		}
	}
}

func TestValidateLocalizationCorpusRejectsInvalidBenchmarkEvidence(t *testing.T) {
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
	err := ValidateLocalizationCorpus(corpus)
	if err == nil {
		t.Fatal("expected invalid benchmark evidence to fail validation")
	}
	for _, want := range []string{"context packet candidate file path", "broad_read_count must be non-negative"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected validation error to contain %q, got %v", want, err)
		}
	}
}

func TestValidateLocalizationCorpusRejectsBackslashTraversalEvidencePath(t *testing.T) {
	corpus := validLocalizationCorpusForTest()
	corpus.Tasks[0].Benchmark = &LocalizationBenchmarkInput{
		ContextPacket: &LocalizationContextPacketEvidence{
			Files: []LocalizationCandidateFile{{Path: `..\outside.go`}},
		},
	}
	err := ValidateLocalizationCorpus(corpus)
	if err == nil {
		t.Fatal("expected backslash traversal evidence path to fail validation")
	}
	if !strings.Contains(err.Error(), "context packet candidate file path") {
		t.Fatalf("expected candidate path validation error, got %v", err)
	}
}

func TestValidateLocalizationCorpusRejectsInvalidCompetitiveConditionEvidence(t *testing.T) {
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
	err := ValidateLocalizationCorpus(corpus)
	if err == nil {
		t.Fatal("expected invalid competitive condition evidence to fail validation")
	}
	if !strings.Contains(err.Error(), "infeasibility_reason") || !strings.Contains(err.Error(), "sensitive or raw-content marker") {
		t.Fatalf("expected sensitive-marker validation error, got %v", err)
	}
}

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
