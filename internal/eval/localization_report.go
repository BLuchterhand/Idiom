package eval

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"idiom/internal/comprehension"
)

const LocalizationUnifiedReportSchemaVersion = "localization_unified_report.v1"

var localizationClientClassifications = []string{
	"finance",
	"healthcare_life_sciences",
	"public_sector",
	"enterprise_saas",
	"tooling_vendor",
	"local_model_private_code",
	"open_source",
}

type LocalizationUnifiedReportOptions struct {
	Benchmark           LocalizationBenchmarkOptions
	WorkspaceRoot       string
	VerificationLogPath string
	Now                 time.Time
}

type LocalizationUnifiedReport struct {
	SchemaVersion  string                           `json:"schema_version"`
	GeneratedAt    string                           `json:"generated_at"`
	Name           string                           `json:"name"`
	Passed         bool                             `json:"passed"`
	Experimental   bool                             `json:"experimental"`
	Outcome        LocalizationOutcomeReport        `json:"outcome"`
	Trajectory     LocalizationTrajectoryReport     `json:"trajectory"`
	CacheFreshness LocalizationCacheFreshnessReport `json:"cache_freshness"`
	Ranker         LocalizationRankerReport         `json:"ranker"`
	Verification   LocalizationVerificationReport   `json:"verification"`
	AuditMetrics   LocalizationAuditMetricsReport   `json:"audit_metrics"`
	ClientProduct  LocalizationClientProductReport  `json:"client_product"`
	ProductSuccess []LocalizationProductSuccessGate `json:"product_success_gates"`
	Privacy        LocalizationReportPrivacy        `json:"privacy"`
	Findings       []string                         `json:"findings,omitempty"`
}

type LocalizationOutcomeReport struct {
	SampleCount           int                                      `json:"sample_count"`
	ExpectedFiles         int                                      `json:"expected_files"`
	ExpectedSymbols       int                                      `json:"expected_symbols"`
	ExpectedTests         int                                      `json:"expected_tests"`
	TopKEditPathCoverage  []LocalizationTopKCoverageMetric         `json:"top_k_edit_path_coverage"`
	BroadReadCount        LocalizationCountMetric                  `json:"broad_read_count"`
	ZeroHitSearchCount    LocalizationCountMetric                  `json:"zero_hit_search_count"`
	CandidatePathWaste    LocalizationCandidatePathWasteMetric     `json:"candidate_path_waste"`
	CompetitiveConditions []LocalizationCompetitiveConditionMetric `json:"competitive_conditions"`
	SourceCoverage        map[string]int                           `json:"source_coverage,omitempty"`
	ThemeCoverage         map[string]int                           `json:"theme_coverage,omitempty"`
	RedactionStatus       map[string]int                           `json:"redaction_status,omitempty"`
}

type LocalizationTrajectoryReport struct {
	Baseline LocalizationAvailability       `json:"baseline"`
	Current  LocalizationTrajectorySnapshot `json:"current"`
	Delta    LocalizationAvailability       `json:"delta"`
}

type LocalizationTrajectorySnapshot struct {
	Available          bool                                 `json:"available"`
	State              string                               `json:"state"`
	Reason             string                               `json:"reason,omitempty"`
	BroadReadCount     LocalizationCountMetric              `json:"broad_read_count"`
	ZeroHitSearchCount LocalizationCountMetric              `json:"zero_hit_search_count"`
	CandidatePathWaste LocalizationCandidatePathWasteMetric `json:"candidate_path_waste"`
}

type LocalizationAvailability struct {
	Available bool   `json:"available"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

type LocalizationCacheFreshnessReport struct {
	Available                 bool   `json:"available"`
	State                     string `json:"state"`
	Reason                    string `json:"reason,omitempty"`
	Indexed                   bool   `json:"indexed"`
	Fresh                     bool   `json:"fresh"`
	IndexIDPresent            bool   `json:"index_id_present"`
	StoreKind                 string `json:"store_kind,omitempty"`
	CollectorVersionMismatch  bool   `json:"collector_version_mismatch,omitempty"`
	StaleFileCount            int    `json:"stale_file_count"`
	MissingFileCount          int    `json:"missing_file_count"`
	StalePathCount            int    `json:"stale_path_count"`
	Rows                      int    `json:"rows"`
	FreshRows                 int    `json:"fresh_rows"`
	StaleRows                 int    `json:"stale_rows"`
	NonCurrentRows            int    `json:"non_current_rows"`
	WorkspaceStateKeyedRows   int    `json:"workspace_state_keyed_rows"`
	ContractRows              int    `json:"contract_rows"`
	UnresolvedGapRows         int    `json:"unresolved_gap_rows"`
	SessionHits               int64  `json:"session_hits"`
	SessionMisses             int64  `json:"session_misses"`
	SessionWrites             int64  `json:"session_writes"`
	FreshnessHits             int64  `json:"freshness_hits"`
	StalePreventions          int64  `json:"stale_preventions"`
	StaleRowMisses            int64  `json:"stale_row_misses"`
	FreshnessProvenanceSource string `json:"freshness_provenance_source,omitempty"`
}

type LocalizationRankerReport struct {
	Available          bool                               `json:"available"`
	State              string                             `json:"state"`
	Reason             string                             `json:"reason,omitempty"`
	WeightsPresent     bool                               `json:"weights_present"`
	LearningLogPresent bool                               `json:"learning_log_present"`
	WeightCount        int                                `json:"weight_count"`
	LearningSummary    *LocalizationLearningSummaryReport `json:"learning_summary,omitempty"`
}

type LocalizationLearningSummaryReport struct {
	TotalEntries         int            `json:"total_entries"`
	TotalEvents          int            `json:"total_events"`
	BoostCount           int            `json:"boost_count"`
	PenalizeCount        int            `json:"penalize_count"`
	NoOpCount            int            `json:"no_op_count"`
	ResetCount           int            `json:"reset_count"`
	RestoreCount         int            `json:"restore_count"`
	AcceptedCount        int            `json:"accepted_count"`
	RejectedCount        int            `json:"rejected_count"`
	BySource             map[string]int `json:"by_source,omitempty"`
	StateCounts          map[string]int `json:"state_counts,omitempty"`
	ReasonCounts         map[string]int `json:"reason_counts,omitempty"`
	AcceptedReasonCounts map[string]int `json:"accepted_reason_counts,omitempty"`
	RejectedReasonCounts map[string]int `json:"rejected_reason_counts,omitempty"`
}

type LocalizationVerificationReport struct {
	Available                 bool   `json:"available"`
	State                     string `json:"state"`
	Reason                    string `json:"reason,omitempty"`
	Status                    string `json:"status"`
	CheckCount                int    `json:"check_count"`
	Passed                    bool   `json:"passed"`
	Failed                    bool   `json:"failed"`
	VerificationPlanCount     int    `json:"verification_plan_count"`
	VerificationFeedbackCount int    `json:"verification_feedback_count"`
	FullSuitePlanObserved     bool   `json:"full_suite_plan_observed,omitempty"`
}

type LocalizationAuditMetricsReport struct {
	Available             bool                                `json:"available"`
	State                 string                              `json:"state"`
	Reason                string                              `json:"reason,omitempty"`
	JoinCoverage          LocalizationAuditJoinCoverage       `json:"join_coverage"`
	ContextCache          LocalizationAuditContextCacheMetric `json:"context_cache"`
	FreshnessHealth       LocalizationFreshnessHealthMetric   `json:"freshness_health"`
	RankerLearning        LocalizationAuditRankerMetric       `json:"ranker_learning"`
	BehaviorFindingCount  int                                 `json:"behavior_finding_count"`
	BehaviorPatternCounts map[string]int                      `json:"behavior_pattern_counts,omitempty"`
}

type LocalizationAuditJoinCoverage struct {
	Outcome        bool `json:"outcome"`
	Trajectory     bool `json:"trajectory"`
	CacheFreshness bool `json:"cache_freshness"`
	Ranker         bool `json:"ranker"`
	Verification   bool `json:"verification"`
	AuditMetrics   bool `json:"audit_metrics"`
}

type LocalizationAuditContextCacheMetric struct {
	Rows           int `json:"rows"`
	FreshRows      int `json:"fresh_rows"`
	StaleRows      int `json:"stale_rows"`
	NonCurrentRows int `json:"non_current_rows"`
	ContractRows   int `json:"contract_rows"`
}

type LocalizationFreshnessHealthMetric struct {
	ReadThroughAttempts        int64            `json:"read_through_attempts"`
	Blockers                   int64            `json:"blockers"`
	StaleCachedViewPreventions int64            `json:"stale_cached_view_preventions"`
	StaleRowMisses             int64            `json:"stale_row_misses"`
	RefreshFailures            int64            `json:"refresh_failures"`
	RootRebuilds               int64            `json:"root_rebuilds"`
	AllowStaleInvocations      int64            `json:"allow_stale_invocations"`
	BlockersByReason           map[string]int64 `json:"blockers_by_reason,omitempty"`
}

type LocalizationAuditRankerMetric struct {
	WeightCount     int `json:"weight_count"`
	TotalEvents     int `json:"total_events"`
	AcceptedCount   int `json:"accepted_count"`
	RejectedCount   int `json:"rejected_count"`
	ReasonCountKeys int `json:"reason_count_keys"`
}

type LocalizationClientProductReport struct {
	Areas                   map[string]int                        `json:"areas,omitempty"`
	RequiredClassifications []string                              `json:"required_classifications"`
	ClassificationRows      []LocalizationClientClassificationRow `json:"classification_rows"`
	CoveredClassifications  []string                              `json:"covered_classifications,omitempty"`
	MissingClassifications  []string                              `json:"missing_classifications,omitempty"`
	GenericSupport          bool                                  `json:"generic_support"`
	ClientSpecificBranches  bool                                  `json:"client_specific_branches"`
}

type LocalizationClientClassificationRow struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	SampleCount int    `json:"sample_count"`
	Evidence    string `json:"evidence,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type LocalizationProductSuccessGate struct {
	Gate     string `json:"gate"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type LocalizationReportPrivacy struct {
	PromptTextIncluded     bool     `json:"prompt_text_included"`
	FileBodiesIncluded     bool     `json:"file_bodies_included"`
	LogExcerptsIncluded    bool     `json:"log_excerpts_included"`
	SecretsIncluded        bool     `json:"secrets_included"`
	EvidencePolicy         string   `json:"evidence_policy"`
	ExcludedContentClasses []string `json:"excluded_content_classes"`
}

func EvaluateLocalizationUnifiedReportFile(path string, root string, options LocalizationUnifiedReportOptions) (LocalizationUnifiedReport, error) {
	corpus, err := ReadLocalizationCorpus(path)
	if err != nil {
		return LocalizationUnifiedReport{}, err
	}
	if strings.TrimSpace(root) != "" && strings.TrimSpace(options.WorkspaceRoot) == "" {
		options.WorkspaceRoot = root
	}
	return BuildLocalizationUnifiedReport(corpus, options), nil
}

func BuildLocalizationUnifiedReport(corpus LocalizationCorpus, options LocalizationUnifiedReportOptions) LocalizationUnifiedReport {
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	benchmarkOptions := options.Benchmark
	if len(benchmarkOptions.TopK) == 0 {
		benchmarkOptions = DefaultLocalizationBenchmarkOptions()
	}
	corpusReport := AnalyzeLocalizationCorpusWithOptions(corpus, benchmarkOptions)
	if strings.TrimSpace(options.WorkspaceRoot) != "" {
		pathFindings := LintLocalizationCorpusPaths(corpus, options.WorkspaceRoot)
		if len(pathFindings) > 0 {
			corpusReport.Findings = append(corpusReport.Findings, pathFindings...)
			corpusReport.Passed = false
		}
	}
	cache := buildLocalizationCacheFreshnessReport(options.WorkspaceRoot)
	ranker := buildLocalizationRankerReport(options.WorkspaceRoot)
	verification := buildLocalizationVerificationReport(options.VerificationLogPath)
	outcome := buildLocalizationOutcomeReport(corpusReport)
	trajectory := buildLocalizationTrajectoryReport(corpusReport.Benchmark)
	audit := buildLocalizationAuditMetricsReport(cache, ranker, verification, options.VerificationLogPath)
	clientProduct := buildLocalizationClientProductReport(corpusReport.ClientProductAreas)
	privacy := buildLocalizationReportPrivacy()
	return LocalizationUnifiedReport{
		SchemaVersion:  LocalizationUnifiedReportSchemaVersion,
		GeneratedAt:    options.Now.UTC().Format(time.RFC3339Nano),
		Name:           corpusReport.Name,
		Passed:         corpusReport.Passed,
		Experimental:   corpusReport.Experimental,
		Outcome:        outcome,
		Trajectory:     trajectory,
		CacheFreshness: cache,
		Ranker:         ranker,
		Verification:   verification,
		AuditMetrics:   audit,
		ClientProduct:  clientProduct,
		ProductSuccess: buildLocalizationProductSuccessGates(outcome, trajectory, cache, ranker, verification, audit, clientProduct, privacy, corpusReport.Passed),
		Privacy:        privacy,
		Findings:       corpusReport.Findings,
	}
}

func buildLocalizationOutcomeReport(report LocalizationCorpusReport) LocalizationOutcomeReport {
	return LocalizationOutcomeReport{
		SampleCount:           report.TotalTasks,
		ExpectedFiles:         report.ExpectedFiles,
		ExpectedSymbols:       report.ExpectedSymbols,
		ExpectedTests:         report.ExpectedTests,
		TopKEditPathCoverage:  append([]LocalizationTopKCoverageMetric(nil), report.Benchmark.TopKEditPathCoverage...),
		BroadReadCount:        report.Benchmark.BroadReadCount,
		ZeroHitSearchCount:    report.Benchmark.ZeroHitSearchCount,
		CandidatePathWaste:    report.Benchmark.CandidatePathWaste,
		CompetitiveConditions: append([]LocalizationCompetitiveConditionMetric(nil), report.Benchmark.CompetitiveConditions...),
		SourceCoverage:        cloneIntMap(report.SourceCoverage),
		ThemeCoverage:         cloneIntMap(report.ThemeCoverage),
		RedactionStatus:       cloneIntMap(report.RedactionStatus),
	}
}

func buildLocalizationTrajectoryReport(benchmark LocalizationBenchmarkReport) LocalizationTrajectoryReport {
	current := LocalizationTrajectorySnapshot{
		Available:          benchmark.BroadReadCount.Available || benchmark.ZeroHitSearchCount.Available || benchmark.CandidatePathWaste.Available,
		State:              "available",
		BroadReadCount:     benchmark.BroadReadCount,
		ZeroHitSearchCount: benchmark.ZeroHitSearchCount,
		CandidatePathWaste: benchmark.CandidatePathWaste,
	}
	if !current.Available {
		current.State = "not_available"
		current.Reason = "trajectory_input_missing"
	}
	return LocalizationTrajectoryReport{
		Baseline: LocalizationAvailability{Available: false, State: "not_available", Reason: "baseline_not_recorded"},
		Current:  current,
		Delta:    LocalizationAvailability{Available: false, State: "not_available", Reason: "baseline_not_recorded"},
	}
}

func buildLocalizationCacheFreshnessReport(root string) LocalizationCacheFreshnessReport {
	if strings.TrimSpace(root) == "" {
		return LocalizationCacheFreshnessReport{Available: false, State: "not_available", Reason: "workspace_root_not_available"}
	}
	report := LocalizationCacheFreshnessReport{Available: true, State: "available"}
	status, err := comprehension.Status(root)
	if err != nil {
		report.Available = false
		report.State = "not_available"
		report.Reason = "context_status_error"
	} else {
		report.Indexed = status.Indexed
		report.Fresh = status.Fresh
		report.IndexIDPresent = strings.TrimSpace(status.IndexID) != ""
		report.StoreKind = status.StoreKind
		report.CollectorVersionMismatch = status.CollectorVersionMismatch
		report.StaleFileCount = status.StaleFileCount
		report.MissingFileCount = status.MissingFileCount
		report.StalePathCount = status.StalePathCount
		report.FreshnessProvenanceSource = status.Provenance.Source
		if !status.Indexed {
			report.State = "not_available"
			report.Reason = "context_index_missing"
		}
	}
	stats, err := comprehension.ReadViewStats(root)
	if err != nil {
		if report.Reason == "" {
			report.Reason = "context_cache_stats_error"
		}
		return report
	}
	report.Rows = stats.Rows
	report.FreshRows = stats.Fresh
	report.StaleRows = stats.Stale
	report.NonCurrentRows = stats.NonCurrent
	report.WorkspaceStateKeyedRows = stats.WorkspaceStateKeyedRows
	report.ContractRows = stats.ContractRows
	report.UnresolvedGapRows = stats.UnresolvedGapRows
	report.SessionHits = stats.Hits
	report.SessionMisses = stats.Misses
	report.SessionWrites = stats.Writes
	report.FreshnessHits = stats.FreshnessHits
	report.StalePreventions = stats.StalePreventions
	report.StaleRowMisses = stats.StaleRowMisses
	return report
}

func buildLocalizationRankerReport(root string) LocalizationRankerReport {
	if strings.TrimSpace(root) == "" {
		return LocalizationRankerReport{Available: false, State: "not_available", Reason: "workspace_root_not_available"}
	}
	report := LocalizationRankerReport{Available: true, State: "available"}
	weights, err := comprehension.LoadLearnedWeights(root)
	if err != nil {
		report.Available = false
		report.State = "not_available"
		report.Reason = "ranker_weights_error"
		return report
	}
	report.WeightCount = len(weights)
	report.WeightsPresent = report.WeightCount > 0 || localizationPathExistsOnDisk(localizationRankerWeightsPath(root))
	report.LearningLogPresent = localizationPathExistsOnDisk(localizationLearningLogPath(root))
	entries, err := comprehension.ReadLearningLog(root)
	if err != nil {
		report.Available = false
		report.State = "not_available"
		report.Reason = "ranker_learning_log_error"
		return report
	}
	if len(entries) > 0 {
		summary := localizationLearningSummaryReport(comprehension.SummarizeLearningLog(entries))
		report.LearningSummary = &summary
	}
	if !report.WeightsPresent && !report.LearningLogPresent {
		report.Available = false
		report.State = "not_available"
		report.Reason = "ranker_learning_not_observed"
	}
	return report
}

func localizationLearningSummaryReport(summary comprehension.LearningLogSummary) LocalizationLearningSummaryReport {
	return LocalizationLearningSummaryReport{
		TotalEntries:         summary.TotalEntries,
		TotalEvents:          summary.TotalEvents,
		BoostCount:           summary.BoostCount,
		PenalizeCount:        summary.PenalizeCount,
		NoOpCount:            summary.NoOpCount,
		ResetCount:           summary.ResetCount,
		RestoreCount:         summary.RestoreCount,
		AcceptedCount:        summary.AcceptedCount,
		RejectedCount:        summary.RejectedCount,
		BySource:             cloneIntMap(summary.BySource),
		StateCounts:          cloneIntMap(summary.StateCounts),
		ReasonCounts:         cloneIntMap(summary.ReasonCounts),
		AcceptedReasonCounts: cloneIntMap(summary.AcceptedReasonCounts),
		RejectedReasonCounts: cloneIntMap(summary.RejectedReasonCounts),
	}
}

func buildLocalizationVerificationReport(logPath string) LocalizationVerificationReport {
	if strings.TrimSpace(logPath) == "" {
		return LocalizationVerificationReport{Available: false, State: "not_available", Reason: "verification_log_not_provided", Status: "not_available"}
	}
	records, err := LoadRecords(logPath)
	if err != nil {
		return LocalizationVerificationReport{Available: false, State: "not_available", Reason: "verification_log_unreadable", Status: "not_available"}
	}
	metrics := Analyze(records)
	status := VerificationStatus(metrics)
	report := LocalizationVerificationReport{
		Available:                 true,
		State:                     "available",
		Status:                    status,
		CheckCount:                metrics.VerificationChecks,
		Passed:                    metrics.VerificationPassed,
		Failed:                    metrics.VerificationFailed,
		VerificationPlanCount:     len(metrics.VerificationPlans),
		VerificationFeedbackCount: len(metrics.VerificationFeedback),
	}
	for _, plan := range metrics.VerificationPlans {
		if plan.UsesFullSuite {
			report.FullSuitePlanObserved = true
			break
		}
	}
	return report
}

func buildLocalizationAuditMetricsReport(cache LocalizationCacheFreshnessReport, ranker LocalizationRankerReport, verification LocalizationVerificationReport, logPath string) LocalizationAuditMetricsReport {
	metrics := LocalizationAuditMetricsReport{
		Available: true,
		State:     "available",
		JoinCoverage: LocalizationAuditJoinCoverage{
			Outcome:        true,
			Trajectory:     true,
			CacheFreshness: cache.Available,
			Ranker:         ranker.Available,
			Verification:   verification.Available,
			AuditMetrics:   true,
		},
		ContextCache: LocalizationAuditContextCacheMetric{
			Rows:           cache.Rows,
			FreshRows:      cache.FreshRows,
			StaleRows:      cache.StaleRows,
			NonCurrentRows: cache.NonCurrentRows,
			ContractRows:   cache.ContractRows,
		},
		FreshnessHealth: buildLocalizationFreshnessHealthMetric(),
		RankerLearning: LocalizationAuditRankerMetric{
			WeightCount: ranker.WeightCount,
		},
	}
	if ranker.LearningSummary != nil {
		metrics.RankerLearning.TotalEvents = ranker.LearningSummary.TotalEvents
		metrics.RankerLearning.AcceptedCount = ranker.LearningSummary.AcceptedCount
		metrics.RankerLearning.RejectedCount = ranker.LearningSummary.RejectedCount
		metrics.RankerLearning.ReasonCountKeys = len(ranker.LearningSummary.ReasonCounts)
	}
	if strings.TrimSpace(logPath) != "" {
		if perf, err := AnalyzeLog(logPath); err == nil {
			metrics.BehaviorPatternCounts = map[string]int{}
			for _, pattern := range perf.AntiPatterns {
				if pattern.Name == "" || pattern.Count <= 0 {
					continue
				}
				metrics.BehaviorPatternCounts[pattern.Name] += pattern.Count
				metrics.BehaviorFindingCount += pattern.Count
			}
		}
	}
	return metrics
}

func buildLocalizationFreshnessHealthMetric() LocalizationFreshnessHealthMetric {
	stats := comprehension.FreshnessHealthStats()
	_, stalePreventions, staleRowMisses := comprehension.ViewFreshnessCounters()
	return LocalizationFreshnessHealthMetric{
		ReadThroughAttempts:        stats.ReadThroughAttempts,
		Blockers:                   stats.Blockers,
		StaleCachedViewPreventions: stalePreventions,
		StaleRowMisses:             staleRowMisses,
		RefreshFailures:            stats.RefreshFailures,
		RootRebuilds:               stats.RootRebuilds,
		AllowStaleInvocations:      stats.AllowStaleInvocations,
		BlockersByReason:           cloneInt64Map(stats.BlockersByReason),
	}
}

func buildLocalizationClientProductReport(areas map[string]int) LocalizationClientProductReport {
	report := LocalizationClientProductReport{
		Areas:                   cloneIntMap(areas),
		RequiredClassifications: append([]string(nil), localizationClientClassifications...),
		GenericSupport:          true,
		ClientSpecificBranches:  false,
	}
	normalizedCounts := make(map[string]int, len(areas))
	for area, count := range areas {
		if normalized := normalizeClientClassification(area); normalized != "" {
			normalizedCounts[normalized] += count
		}
	}
	for _, classification := range localizationClientClassifications {
		count := normalizedCounts[classification]
		row := LocalizationClientClassificationRow{
			ID:          classification,
			Label:       localizationClientClassificationLabel(classification),
			SampleCount: count,
		}
		if count > 0 {
			row.Status = "pass"
			row.Reason = "classification_sample_present"
			row.Evidence = fmt.Sprintf("sample_count=%d", count)
			report.CoveredClassifications = append(report.CoveredClassifications, classification)
			report.ClassificationRows = append(report.ClassificationRows, row)
			continue
		}
		row.Status = "not_available"
		row.Reason = "classification_sample_missing"
		row.Evidence = "required_classification_enumerated"
		report.MissingClassifications = append(report.MissingClassifications, classification)
		report.ClassificationRows = append(report.ClassificationRows, row)
	}
	return report
}

func buildLocalizationProductSuccessGates(
	outcome LocalizationOutcomeReport,
	trajectory LocalizationTrajectoryReport,
	cache LocalizationCacheFreshnessReport,
	ranker LocalizationRankerReport,
	verification LocalizationVerificationReport,
	audit LocalizationAuditMetricsReport,
	clientProduct LocalizationClientProductReport,
	privacy LocalizationReportPrivacy,
	corpusPassed bool,
) []LocalizationProductSuccessGate {
	covered, missing := localizationClientCoverageCounts(clientProduct)
	return []LocalizationProductSuccessGate{
		localizationICPGate(clientProduct, covered, missing),
		localizationActivationGate(outcome, corpusPassed),
		localizationRetainedUseGate(cache, ranker),
		localizationIntegrationProofGate(cache, verification, audit),
		localizationSecurityPrivacyGate(privacy),
		localizationCompetitiveProofGate(outcome, trajectory),
		localizationClientDemoProofGate(clientProduct, covered, missing),
		localizationPackagingSupportGate(),
		localizationLocalEvidenceExportGate(outcome, cache, audit, privacy),
	}
}

func localizationICPGate(clientProduct LocalizationClientProductReport, covered int, missing int) LocalizationProductSuccessGate {
	evidence := fmt.Sprintf("required_classifications=%d; covered=%d; missing=%d; rows=%d", len(clientProduct.RequiredClassifications), covered, missing, len(clientProduct.ClassificationRows))
	if len(clientProduct.ClassificationRows) == 0 {
		return localizationGate("icp", "ICP", "not_available", evidence, "classification_rows_missing")
	}
	if missing == 0 {
		return localizationGate("icp", "ICP", "pass", evidence, "all_required_classifications_have_samples")
	}
	return localizationGate("icp", "ICP", "partial", evidence, "classification_statuses_enumerate_missing_coverage")
}

func localizationActivationGate(outcome LocalizationOutcomeReport, corpusPassed bool) LocalizationProductSuccessGate {
	evidence := fmt.Sprintf("sample_count=%d; expected_files=%d; expected_symbols=%d; expected_tests=%d", outcome.SampleCount, outcome.ExpectedFiles, outcome.ExpectedSymbols, outcome.ExpectedTests)
	if outcome.SampleCount == 0 {
		return localizationGate("activation", "Activation", "not_available", evidence, "localization_corpus_empty")
	}
	if !corpusPassed {
		return localizationGate("activation", "Activation", "partial", evidence, "corpus_findings_present")
	}
	return localizationGate("activation", "Activation", "pass", evidence, "localization_corpus_available")
}

func localizationRetainedUseGate(cache LocalizationCacheFreshnessReport, ranker LocalizationRankerReport) LocalizationProductSuccessGate {
	cacheReason := localizationCacheDegradationReason(cache)
	cacheSignal := cache.Available && cache.Fresh && cacheReason == ""
	rankerSignal := ranker.Available && (ranker.WeightsPresent || ranker.LearningLogPresent || ranker.LearningSummary != nil)
	evidence := fmt.Sprintf("cache_available=%t; cache_fresh=%t; fresh_rows=%d; cache_stale_paths=%d; cache_stale=%d; cache_non_current=%d; ranker_state=%s; ranker_events=%d", cache.Available, cache.Fresh, cache.FreshRows, cache.StalePathCount, cache.StaleRows, cache.NonCurrentRows, ranker.State, localizationRankerEventCount(ranker))
	if cacheReason != "" {
		if rankerSignal {
			return localizationGate("retained_use", "Retained use", "partial", evidence, cacheReason)
		}
		return localizationGate("retained_use", "Retained use", "not_available", evidence, cacheReason)
	}
	switch {
	case cacheSignal && rankerSignal:
		return localizationGate("retained_use", "Retained use", "pass", evidence, "cache_and_ranker_learning_available")
	case cacheSignal || rankerSignal:
		return localizationGate("retained_use", "Retained use", "partial", evidence, "retained_use_signal_partial")
	default:
		return localizationGate("retained_use", "Retained use", "not_available", evidence, "fresh_cache_or_ranker_learning_not_available")
	}
}

func localizationIntegrationProofGate(cache LocalizationCacheFreshnessReport, verification LocalizationVerificationReport, audit LocalizationAuditMetricsReport) LocalizationProductSuccessGate {
	cacheReason := localizationCacheDegradationReason(cache)
	evidence := fmt.Sprintf("audit_state=%s; cache_state=%s; cache_rows=%d; cache_stale_paths=%d; cache_stale=%d; cache_non_current=%d; verification_state=%s", audit.State, cache.State, cache.Rows, cache.StalePathCount, cache.StaleRows, cache.NonCurrentRows, verification.State)
	if cacheReason != "" {
		if audit.Available || cache.Available || verification.Available {
			return localizationGate("integration_proof", "Integration proof", "partial", evidence, cacheReason)
		}
		return localizationGate("integration_proof", "Integration proof", "not_available", evidence, cacheReason)
	}
	if audit.Available && cache.Available {
		return localizationGate("integration_proof", "Integration proof", "pass", evidence, "report_joins_audit_and_cache_evidence")
	}
	if audit.Available || cache.Available || verification.Available {
		return localizationGate("integration_proof", "Integration proof", "partial", evidence, "integration_evidence_partial")
	}
	return localizationGate("integration_proof", "Integration proof", "not_available", evidence, "integration_evidence_not_available")
}

func localizationSecurityPrivacyGate(privacy LocalizationReportPrivacy) LocalizationProductSuccessGate {
	evidence := fmt.Sprintf("policy=%s; prompt_text=%t; file_bodies=%t; log_excerpts=%t; secrets=%t", privacy.EvidencePolicy, privacy.PromptTextIncluded, privacy.FileBodiesIncluded, privacy.LogExcerptsIncluded, privacy.SecretsIncluded)
	if privacy.PromptTextIncluded || privacy.FileBodiesIncluded || privacy.LogExcerptsIncluded || privacy.SecretsIncluded {
		return localizationGate("security_privacy", "Security/privacy", "fail", evidence, "raw_content_flagged")
	}
	return localizationGate("security_privacy", "Security/privacy", "pass", evidence, "raw_content_excluded")
}

func localizationCompetitiveProofGate(outcome LocalizationOutcomeReport, trajectory LocalizationTrajectoryReport) LocalizationProductSuccessGate {
	topKAvailable := localizationAnyTopKAvailable(outcome.TopKEditPathCoverage)
	allBenchmarkSignals := topKAvailable && outcome.BroadReadCount.Available && outcome.ZeroHitSearchCount.Available && outcome.CandidatePathWaste.Available
	anyBenchmarkSignal := topKAvailable || outcome.BroadReadCount.Available || outcome.ZeroHitSearchCount.Available || outcome.CandidatePathWaste.Available || trajectory.Current.Available
	conditionSummary := localizationCompetitiveConditionSummary(outcome.CompetitiveConditions)
	evidence := fmt.Sprintf("top_k_available=%t; broad_reads_available=%t; zero_hit_available=%t; candidate_waste_available=%t; trajectory_available=%t; competitive_conditions=%d; competitive_covered=%d; competitive_external_covered=%d; competitive_missing_tasks=%d; competitive_measured_tasks=%d; competitive_infeasible_tasks=%d; competitive_invalid_client_classifications=%d",
		topKAvailable,
		outcome.BroadReadCount.Available,
		outcome.ZeroHitSearchCount.Available,
		outcome.CandidatePathWaste.Available,
		trajectory.Current.Available,
		conditionSummary.TotalConditions,
		conditionSummary.CoveredConditions,
		conditionSummary.ExternalCoveredConditions,
		conditionSummary.MissingTasks,
		conditionSummary.MeasuredTasks,
		conditionSummary.InfeasibleTasks,
		conditionSummary.InvalidClientClasses,
	)
	if allBenchmarkSignals && conditionSummary.AllRequiredCovered {
		return localizationGate("competitive_proof", "Competitive proof", "pass", evidence, "competitive_conditions_covered")
	}
	if anyBenchmarkSignal || conditionSummary.AnyConditionEvidence {
		reason := "competitive_conditions_partial"
		if conditionSummary.TotalConditions == 0 {
			reason = "competitive_conditions_missing"
		} else if conditionSummary.InvalidClientClasses > 0 {
			reason = "competitive_condition_client_classification_invalid"
		}
		return localizationGate("competitive_proof", "Competitive proof", "partial", evidence, reason)
	}
	return localizationGate("competitive_proof", "Competitive proof", "not_available", evidence, "localization_efficiency_metrics_missing")
}

func localizationClientDemoProofGate(clientProduct LocalizationClientProductReport, covered int, missing int) LocalizationProductSuccessGate {
	evidence := fmt.Sprintf("classification_rows=%d; covered=%d; missing=%d", len(clientProduct.ClassificationRows), covered, missing)
	if len(clientProduct.ClassificationRows) == 0 {
		return localizationGate("client_demo_proof", "Client demo proof", "not_available", evidence, "classification_rows_missing")
	}
	if missing == 0 {
		return localizationGate("client_demo_proof", "Client demo proof", "pass", evidence, "all_required_client_samples_available")
	}
	if covered > 0 {
		return localizationGate("client_demo_proof", "Client demo proof", "partial", evidence, "client_sample_coverage_partial")
	}
	return localizationGate("client_demo_proof", "Client demo proof", "not_available", evidence, "client_classification_samples_missing")
}

func localizationPackagingSupportGate() LocalizationProductSuccessGate {
	return localizationGate("packaging_support", "Packaging/support", "pass", "schema=localization_unified_report.v1; text_output_supported=true; json_output_supported=true", "deterministic_cli_report_schema_available")
}

func localizationLocalEvidenceExportGate(outcome LocalizationOutcomeReport, cache LocalizationCacheFreshnessReport, audit LocalizationAuditMetricsReport, privacy LocalizationReportPrivacy) LocalizationProductSuccessGate {
	cacheReason := localizationCacheDegradationReason(cache)
	evidence := fmt.Sprintf("schema=%s; sample_count=%d; audit_metrics=%t; cache_rows=%d; cache_stale_paths=%d; cache_stale=%d; cache_non_current=%d; evidence_policy=%s", LocalizationUnifiedReportSchemaVersion, outcome.SampleCount, audit.Available, cache.Rows, cache.StalePathCount, cache.StaleRows, cache.NonCurrentRows, privacy.EvidencePolicy)
	if privacy.PromptTextIncluded || privacy.FileBodiesIncluded || privacy.LogExcerptsIncluded || privacy.SecretsIncluded {
		return localizationGate("local_product_evidence_export", "Local product-evidence export", "fail", evidence, "unsafe_evidence_export_flags")
	}
	if cacheReason != "" {
		if audit.Available || outcome.SampleCount > 0 {
			return localizationGate("local_product_evidence_export", "Local product-evidence export", "partial", evidence, cacheReason)
		}
		return localizationGate("local_product_evidence_export", "Local product-evidence export", "not_available", evidence, cacheReason)
	}
	if audit.Available {
		return localizationGate("local_product_evidence_export", "Local product-evidence export", "pass", evidence, "bounded_json_report_export_available")
	}
	return localizationGate("local_product_evidence_export", "Local product-evidence export", "partial", evidence, "bounded_json_report_without_audit_metrics")
}

func localizationCacheDegradationReason(cache LocalizationCacheFreshnessReport) string {
	if !cache.Available {
		return "cache_metrics_not_available"
	}
	reasons := make([]string, 0, 3)
	if !cache.Fresh || cache.StalePathCount > 0 || cache.StaleRows > 0 {
		reasons = append(reasons, "cache_stale")
	}
	if cache.Rows == 0 {
		reasons = append(reasons, "cache_rows_missing")
	}
	if cache.NonCurrentRows > 0 {
		reasons = append(reasons, "cache_non_current")
	}
	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, ",")
}

func localizationGate(gate string, label string, status string, evidence string, reason string) LocalizationProductSuccessGate {
	return LocalizationProductSuccessGate{
		Gate:     gate,
		Label:    label,
		Status:   status,
		Evidence: evidence,
		Reason:   reason,
	}
}

func localizationClientCoverageCounts(report LocalizationClientProductReport) (int, int) {
	covered := 0
	missing := 0
	for _, row := range report.ClassificationRows {
		if row.SampleCount > 0 {
			covered++
			continue
		}
		missing++
	}
	return covered, missing
}

func localizationRankerEventCount(ranker LocalizationRankerReport) int {
	if ranker.LearningSummary == nil {
		return 0
	}
	return ranker.LearningSummary.TotalEvents
}

func localizationAnyTopKAvailable(metrics []LocalizationTopKCoverageMetric) bool {
	for _, metric := range metrics {
		if metric.Available {
			return true
		}
	}
	return false
}

type localizationCompetitiveConditionGateSummary struct {
	TotalConditions           int
	CoveredConditions         int
	ExternalCoveredConditions int
	MissingTasks              int
	MeasuredTasks             int
	InfeasibleTasks           int
	InvalidClientClasses      int
	AllRequiredCovered        bool
	AnyConditionEvidence      bool
}

func localizationCompetitiveConditionSummary(conditions []LocalizationCompetitiveConditionMetric) localizationCompetitiveConditionGateSummary {
	summary := localizationCompetitiveConditionGateSummary{AllRequiredCovered: len(conditions) > 0}
	requiredExternal := 0
	for _, condition := range conditions {
		if !condition.Required {
			continue
		}
		summary.TotalConditions++
		if condition.External {
			requiredExternal++
		}
		summary.MissingTasks += condition.MissingTasks
		summary.MeasuredTasks += condition.MeasuredTasks
		summary.InfeasibleTasks += condition.InfeasibleTasks
		summary.InvalidClientClasses += condition.InvalidClientClasses
		if condition.MeasuredTasks > 0 || condition.InfeasibleTasks > 0 {
			summary.AnyConditionEvidence = true
		}
		coveredTasks := condition.MeasuredTasks + condition.InfeasibleTasks
		conditionCovered := condition.TotalTasks > 0 && coveredTasks == condition.TotalTasks && condition.MissingTasks == 0 && len(condition.FailureReasons) == 0
		if conditionCovered {
			summary.CoveredConditions++
			if condition.External {
				summary.ExternalCoveredConditions++
			}
			continue
		}
		summary.AllRequiredCovered = false
	}
	if summary.TotalConditions == 0 {
		summary.AllRequiredCovered = false
	}
	if requiredExternal > 0 && summary.ExternalCoveredConditions < requiredExternal {
		summary.AllRequiredCovered = false
	}
	return summary
}

func buildLocalizationReportPrivacy() LocalizationReportPrivacy {
	return LocalizationReportPrivacy{
		PromptTextIncluded:     false,
		FileBodiesIncluded:     false,
		LogExcerptsIncluded:    false,
		SecretsIncluded:        false,
		EvidencePolicy:         "aggregate_counts_relative_paths_no_bodies_or_prompts",
		ExcludedContentClasses: []string{"prompt_text", "file_bodies", "log_excerpts", "secrets", "protected_contents"},
	}
}

func localizationClientClassificationLabel(classification string) string {
	switch classification {
	case "healthcare_life_sciences":
		return "healthcare/life sciences"
	case "public_sector":
		return "public-sector"
	case "enterprise_saas":
		return "enterprise SaaS"
	case "tooling_vendor":
		return "tooling-vendor"
	case "local_model_private_code":
		return "local-model/private-code"
	case "open_source":
		return "open-source"
	default:
		return classification
	}
}

func normalizeClientClassification(value string) string {
	key := normalizedLocalizationKey(value)
	switch key {
	case "finance":
		return "finance"
	case "healthcare", "healthcare_life_sciences", "life_sciences", "healthcare_and_life_sciences":
		return "healthcare_life_sciences"
	case "public_sector", "public_sector_government", "government":
		return "public_sector"
	case "enterprise_saas", "saas", "enterprise":
		return "enterprise_saas"
	case "tooling_vendor", "developer_tooling", "devtools":
		return "tooling_vendor"
	case "local_model_private_code", "private_code", "local_model", "local_models":
		return "local_model_private_code"
	case "open_source", "opensource", "oss":
		return "open_source"
	default:
		return ""
	}
}

func cloneIntMap(source map[string]int) map[string]int {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]int, len(source))
	for key, value := range source {
		if key = strings.TrimSpace(key); key != "" {
			out[key] = value
		}
	}
	return out
}

func cloneInt64Map(source map[string]int64) map[string]int64 {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]int64, len(source))
	for key, value := range source {
		if key = strings.TrimSpace(key); key != "" {
			out[key] = value
		}
	}
	return out
}

func localizationPathExistsOnDisk(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func localizationRankerWeightsPath(root string) string {
	return root + string(os.PathSeparator) + ".idiom" + string(os.PathSeparator) + "ranker" + string(os.PathSeparator) + "weights.json"
}

func localizationLearningLogPath(root string) string {
	return root + string(os.PathSeparator) + ".idiom" + string(os.PathSeparator) + "ranker" + string(os.PathSeparator) + "learning_log.jsonl"
}
