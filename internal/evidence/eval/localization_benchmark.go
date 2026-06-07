package eval

import (
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/platform/util"
)

const (
	localizationFailureNoExpectedEditPaths          = "expected_edit_paths_missing"
	localizationFailureContextPacketCandidates      = "context_packet_candidates_missing"
	localizationFailureContextPacketFileCandidates  = "context_packet_file_candidates_missing"
	localizationFailurePartialContextPacket         = "partial_context_packet_candidates"
	localizationFailureTrajectoryInput              = "trajectory_input_missing"
	localizationFailurePartialTrajectoryInput       = "partial_trajectory_input"
	localizationFailureCompetitiveConditions        = "competitive_conditions_missing"
	localizationFailurePartialCompetitiveCondition  = "partial_competitive_conditions"
	localizationFailureCompetitiveCandidates        = "competitive_condition_candidate_files_missing"
	localizationFailurePartialCompetitiveCandidates = "partial_competitive_condition_candidates"
	localizationFailureCompetitiveBudget            = "competitive_budget_missing"
	localizationFailureCompetitiveReproducibility   = "competitive_reproducibility_missing"
	localizationFailureCompetitiveCommand           = "competitive_command_id_missing"
	localizationFailureCompetitiveArtifact          = "competitive_artifact_missing"
	localizationFailureCompetitiveVerification      = "competitive_verification_method_missing"
	localizationFailureCompetitiveClientClasses     = "competitive_condition_client_classification_invalid"
)

const (
	localizationCompetitiveStatusMeasured   = "measured"
	localizationCompetitiveStatusInfeasible = "infeasible"
)

type localizationCompetitiveBaselineDefinition struct {
	ID       string
	Label    string
	External bool
}

var localizationCompetitiveBaselineDefinitions = []localizationCompetitiveBaselineDefinition{
	{ID: "raw_search_read", Label: "raw search/read baseline"},
	{ID: "generated_baseline_discovery_report", Label: "generated baseline discovery report"},
	{ID: "generated_current_discovery_report", Label: "generated current discovery report"},
	{ID: "context_pack", Label: "context_pack"},
	{ID: "aider_repo_map", Label: "Aider-style repo-map baseline", External: true},
	{ID: "external_agent_native", Label: "external-agent native exploration baseline", External: true},
}

type LocalizationBenchmarkOptions struct {
	TopK []int
}

type LocalizationBenchmarkReport struct {
	Experimental               bool                                     `json:"experimental"`
	ManualCorpusReviewRequired bool                                     `json:"manual_corpus_review_required"`
	MissingInputs              []string                                 `json:"missing_inputs,omitempty"`
	TopKEditPathCoverage       []LocalizationTopKCoverageMetric         `json:"top_k_edit_path_coverage"`
	BroadReadCount             LocalizationCountMetric                  `json:"broad_read_count"`
	ZeroHitSearchCount         LocalizationCountMetric                  `json:"zero_hit_search_count"`
	CandidatePathWaste         LocalizationCandidatePathWasteMetric     `json:"candidate_path_waste"`
	CompetitiveConditions      []LocalizationCompetitiveConditionMetric `json:"competitive_conditions"`
	Definitions                []LocalizationMetricDefinition           `json:"definitions"`
}

type LocalizationTopKCoverageMetric struct {
	K              int      `json:"k"`
	Available      bool     `json:"available"`
	Numerator      int      `json:"numerator"`
	Denominator    int      `json:"denominator"`
	Rate           float64  `json:"rate"`
	TasksWithInput int      `json:"tasks_with_input"`
	TotalTasks     int      `json:"total_tasks"`
	FailureReasons []string `json:"failure_reasons,omitempty"`
}

type LocalizationCountMetric struct {
	Available      bool     `json:"available"`
	Numerator      int      `json:"numerator"`
	Denominator    int      `json:"denominator"`
	TotalTasks     int      `json:"total_tasks"`
	FailureReasons []string `json:"failure_reasons,omitempty"`
}

type LocalizationCandidatePathWasteMetric struct {
	Available                   bool     `json:"available"`
	CandidateWasteNumerator     int      `json:"candidate_waste_numerator"`
	CandidateWasteDenominator   int      `json:"candidate_waste_denominator"`
	CandidateWasteRate          float64  `json:"candidate_waste_rate"`
	PathWasteNumerator          int      `json:"path_waste_numerator"`
	PathWasteDenominator        int      `json:"path_waste_denominator"`
	PathWasteRate               float64  `json:"path_waste_rate"`
	TasksWithContextPacketInput int      `json:"tasks_with_context_packet_input"`
	TotalTasks                  int      `json:"total_tasks"`
	FailureReasons              []string `json:"failure_reasons,omitempty"`
}

type LocalizationMetricDefinition struct {
	Name           string   `json:"name"`
	Numerator      string   `json:"numerator"`
	Denominator    string   `json:"denominator"`
	FailureReasons []string `json:"failure_reasons"`
}

type LocalizationCompetitiveConditionMetric struct {
	Baseline             string                           `json:"baseline"`
	Label                string                           `json:"label"`
	Status               string                           `json:"status"`
	Required             bool                             `json:"required"`
	External             bool                             `json:"external"`
	TotalTasks           int                              `json:"total_tasks"`
	MeasuredTasks        int                              `json:"measured_tasks"`
	InfeasibleTasks      int                              `json:"infeasible_tasks"`
	MissingTasks         int                              `json:"missing_tasks"`
	BudgetedTasks        int                              `json:"budgeted_tasks"`
	ReproducibleTasks    int                              `json:"reproducible_tasks"`
	CommandedTasks       int                              `json:"commanded_tasks"`
	ArtifactedTasks      int                              `json:"artifacted_tasks"`
	VerifiedTasks        int                              `json:"verified_tasks"`
	InvalidClientClasses int                              `json:"invalid_client_classifications,omitempty"`
	ClientCoverage       map[string]int                   `json:"client_coverage,omitempty"`
	InfeasibilityReasons map[string]int                   `json:"infeasibility_reasons,omitempty"`
	ClaimImpacts         map[string]int                   `json:"claim_impacts,omitempty"`
	TopKEditPathCoverage []LocalizationTopKCoverageMetric `json:"top_k_edit_path_coverage,omitempty"`
	FailureReasons       []string                         `json:"failure_reasons,omitempty"`
}

func DefaultLocalizationBenchmarkOptions() LocalizationBenchmarkOptions {
	return LocalizationBenchmarkOptions{TopK: DefaultLocalizationTopK()}
}

func DefaultLocalizationTopK() []int {
	return []int{1, 3, 5}
}

func NormalizeLocalizationTopK(values []int) []int {
	if len(values) == 0 {
		return DefaultLocalizationTopK()
	}
	seen := make(map[int]struct{}, len(values))
	normalized := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return DefaultLocalizationTopK()
	}
	slices.Sort(normalized)
	return normalized
}

func AnalyzeLocalizationBenchmark(corpus LocalizationCorpus, options LocalizationBenchmarkOptions) LocalizationBenchmarkReport {
	topK := NormalizeLocalizationTopK(options.TopK)
	report := LocalizationBenchmarkReport{
		Experimental:               true,
		ManualCorpusReviewRequired: true,
		Definitions:                localizationMetricDefinitions(),
	}
	for _, k := range topK {
		report.TopKEditPathCoverage = append(report.TopKEditPathCoverage, localizationTopKCoverage(corpus, k))
	}
	report.BroadReadCount = localizationTrajectoryCount(corpus, func(trajectory *LocalizationTrajectoryEvidence) *int {
		return trajectory.BroadReadCount
	})
	report.ZeroHitSearchCount = localizationTrajectoryCount(corpus, func(trajectory *LocalizationTrajectoryEvidence) *int {
		return trajectory.ZeroHitSearchCount
	})
	report.CandidatePathWaste = localizationCandidatePathWaste(corpus)
	report.CompetitiveConditions = localizationCompetitiveConditionMetrics(corpus, topK)
	report.MissingInputs = localizationBenchmarkMissingInputs(report)
	return report
}

func localizationTopKCoverage(corpus LocalizationCorpus, k int) LocalizationTopKCoverageMetric {
	metric := LocalizationTopKCoverageMetric{
		K:          k,
		TotalTasks: len(corpus.Tasks),
	}
	fileCandidateTasks := 0
	missingFileCandidates := 0
	for _, task := range corpus.Tasks {
		expectedFiles := localizationExpectedFileSet(task)
		metric.Denominator += len(expectedFiles)
		if len(expectedFiles) == 0 {
			continue
		}
		candidates := localizationCandidateFilePaths(task)
		if len(candidates) == 0 {
			missingFileCandidates++
			continue
		}
		fileCandidateTasks++
		covered := make(map[string]struct{}, len(expectedFiles))
		for _, candidate := range firstLocalizationCandidates(candidates, k) {
			if _, ok := expectedFiles[candidate]; ok {
				covered[candidate] = struct{}{}
			}
		}
		metric.Numerator += len(covered)
	}
	metric.TasksWithInput = fileCandidateTasks
	metric.Rate = localizationRate(metric.Numerator, metric.Denominator)
	metric.Available = metric.Denominator > 0 && fileCandidateTasks > 0
	if metric.Denominator == 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailureNoExpectedEditPaths)
	}
	if fileCandidateTasks == 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailureContextPacketFileCandidates)
	} else if missingFileCandidates > 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailurePartialContextPacket)
	}
	return metric
}

func localizationTrajectoryCount(corpus LocalizationCorpus, value func(*LocalizationTrajectoryEvidence) *int) LocalizationCountMetric {
	metric := LocalizationCountMetric{
		TotalTasks: len(corpus.Tasks),
	}
	for _, task := range corpus.Tasks {
		if task.Benchmark == nil || task.Benchmark.Trajectory == nil {
			continue
		}
		count := value(task.Benchmark.Trajectory)
		if count == nil {
			continue
		}
		metric.Numerator += *count
		metric.Denominator++
	}
	metric.Available = metric.Denominator > 0
	if metric.Denominator == 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailureTrajectoryInput)
	} else if metric.Denominator < metric.TotalTasks {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailurePartialTrajectoryInput)
	}
	return metric
}

func localizationCandidatePathWaste(corpus LocalizationCorpus) LocalizationCandidatePathWasteMetric {
	metric := LocalizationCandidatePathWasteMetric{
		TotalTasks: len(corpus.Tasks),
	}
	for _, task := range corpus.Tasks {
		contextPacket := localizationContextPacket(task)
		if contextPacket == nil {
			continue
		}
		metric.TasksWithContextPacketInput++
		expectedFiles := localizationExpectedFileSet(task)
		expectedSymbols := localizationExpectedSymbolSet(task)
		expectedTestExact, expectedTestPaths := localizationExpectedTestSets(task)

		for _, candidate := range localizationUniqueCandidateFiles(contextPacket.Files) {
			metric.PathWasteDenominator++
			metric.CandidateWasteDenominator++
			if _, ok := expectedFiles[candidate]; !ok {
				metric.PathWasteNumerator++
				metric.CandidateWasteNumerator++
			}
		}
		for _, candidate := range localizationUniqueCandidateSymbols(contextPacket.Symbols) {
			metric.CandidateWasteDenominator++
			if _, ok := expectedSymbols[candidate]; !ok {
				metric.CandidateWasteNumerator++
			}
		}
		for _, candidate := range localizationUniqueCandidateTests(contextPacket.Tests) {
			metric.CandidateWasteDenominator++
			if !localizationExpectedTestContains(expectedTestExact, expectedTestPaths, candidate) {
				metric.CandidateWasteNumerator++
			}
		}
	}
	metric.CandidateWasteRate = localizationRate(metric.CandidateWasteNumerator, metric.CandidateWasteDenominator)
	metric.PathWasteRate = localizationRate(metric.PathWasteNumerator, metric.PathWasteDenominator)
	metric.Available = metric.CandidateWasteDenominator > 0 || metric.PathWasteDenominator > 0
	if metric.TasksWithContextPacketInput == 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailureContextPacketCandidates)
	} else if metric.TasksWithContextPacketInput < metric.TotalTasks {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailurePartialContextPacket)
	}
	return metric
}

func localizationCompetitiveConditionMetrics(corpus LocalizationCorpus, topK []int) []LocalizationCompetitiveConditionMetric {
	metrics := make([]LocalizationCompetitiveConditionMetric, 0, len(localizationCompetitiveBaselineDefinitions))
	for _, definition := range localizationCompetitiveBaselineDefinitions {
		metric := LocalizationCompetitiveConditionMetric{
			Baseline:             definition.ID,
			Label:                definition.Label,
			Required:             true,
			External:             definition.External,
			TotalTasks:           len(corpus.Tasks),
			ClientCoverage:       make(map[string]int),
			InfeasibilityReasons: make(map[string]int),
			ClaimImpacts:         make(map[string]int),
		}
		for _, task := range corpus.Tasks {
			condition, ok := localizationCompetitiveCondition(task, definition.ID)
			if !ok {
				metric.MissingTasks++
				continue
			}
			switch normalizeLocalizationCompetitiveStatus(condition.Status) {
			case localizationCompetitiveStatusMeasured:
				metric.MeasuredTasks++
				if localizationCompetitiveConditionHasBudget(condition) {
					metric.BudgetedTasks++
				}
				if localizationCompetitiveConditionHasReproducibility(condition) {
					metric.ReproducibleTasks++
				}
				if strings.TrimSpace(condition.CommandID) != "" {
					metric.CommandedTasks++
				}
				if strings.TrimSpace(condition.ArtifactPath) != "" {
					metric.ArtifactedTasks++
				}
				if localizationCompetitiveConditionHasVerificationMethod(condition) {
					metric.VerifiedTasks++
				}
			case localizationCompetitiveStatusInfeasible:
				metric.InfeasibleTasks++
				if localizationCompetitiveConditionHasReproducibility(condition) {
					metric.ReproducibleTasks++
				}
				if strings.TrimSpace(condition.CommandID) != "" {
					metric.CommandedTasks++
				}
				if strings.TrimSpace(condition.ArtifactPath) != "" {
					metric.ArtifactedTasks++
				}
				if localizationCompetitiveConditionHasVerificationMethod(condition) {
					metric.VerifiedTasks++
				}
				if reason := normalizedLocalizationKey(condition.InfeasibilityReason); reason != "" {
					metric.InfeasibilityReasons[reason]++
				}
				if impact := normalizedLocalizationKey(condition.ClaimImpact); impact != "" {
					metric.ClaimImpacts[impact]++
				}
			default:
				metric.MissingTasks++
			}
			for _, classification := range condition.ClientClassifications {
				if normalized := normalizeClientClassification(classification); normalized != "" {
					metric.ClientCoverage[normalized]++
					continue
				}
				metric.InvalidClientClasses++
			}
		}
		for _, k := range topK {
			metric.TopKEditPathCoverage = append(metric.TopKEditPathCoverage, localizationCompetitiveTopKCoverage(corpus, definition.ID, k))
		}
		metric.Status = localizationCompetitiveConditionStatus(metric)
		metric.FailureReasons = localizationCompetitiveConditionFailureReasons(metric)
		if len(metric.ClientCoverage) == 0 {
			metric.ClientCoverage = nil
		}
		if len(metric.InfeasibilityReasons) == 0 {
			metric.InfeasibilityReasons = nil
		}
		if len(metric.ClaimImpacts) == 0 {
			metric.ClaimImpacts = nil
		}
		metrics = append(metrics, metric)
	}
	return metrics
}

func localizationCompetitiveTopKCoverage(corpus LocalizationCorpus, baseline string, k int) LocalizationTopKCoverageMetric {
	metric := LocalizationTopKCoverageMetric{
		K:          k,
		TotalTasks: len(corpus.Tasks),
	}
	missingCandidateTasks := 0
	for _, task := range corpus.Tasks {
		condition, ok := localizationCompetitiveCondition(task, baseline)
		if !ok || normalizeLocalizationCompetitiveStatus(condition.Status) != localizationCompetitiveStatusMeasured {
			continue
		}
		expectedFiles := localizationExpectedFileSet(task)
		metric.Denominator += len(expectedFiles)
		if len(expectedFiles) == 0 {
			continue
		}
		candidates := localizationUniqueCandidateFiles(condition.CandidateFiles)
		if len(candidates) == 0 {
			missingCandidateTasks++
			continue
		}
		metric.TasksWithInput++
		covered := make(map[string]struct{}, len(expectedFiles))
		for _, candidate := range firstLocalizationCandidates(candidates, k) {
			if _, ok := expectedFiles[candidate]; ok {
				covered[candidate] = struct{}{}
			}
		}
		metric.Numerator += len(covered)
	}
	metric.Rate = localizationRate(metric.Numerator, metric.Denominator)
	metric.Available = metric.Denominator > 0 && metric.TasksWithInput > 0
	if metric.Denominator == 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailureCompetitiveCandidates)
	} else if metric.TasksWithInput == 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailureCompetitiveCandidates)
	} else if missingCandidateTasks > 0 {
		metric.FailureReasons = append(metric.FailureReasons, localizationFailurePartialCompetitiveCandidates)
	}
	return metric
}

func localizationCompetitiveConditionStatus(metric LocalizationCompetitiveConditionMetric) string {
	covered := metric.MeasuredTasks + metric.InfeasibleTasks
	if metric.TotalTasks == 0 || covered == 0 {
		return "not_available"
	}
	if metric.MissingTasks > 0 {
		return "partial"
	}
	if metric.InvalidClientClasses > 0 {
		return "partial"
	}
	if metric.MeasuredTasks > 0 && metric.InfeasibleTasks == 0 {
		return localizationCompetitiveStatusMeasured
	}
	if metric.MeasuredTasks == 0 && metric.InfeasibleTasks > 0 {
		return "documented_infeasible"
	}
	return "mixed_measured_infeasible"
}

func localizationCompetitiveConditionFailureReasons(metric LocalizationCompetitiveConditionMetric) []string {
	reasons := make([]string, 0, 4)
	if metric.TotalTasks == 0 || metric.MeasuredTasks+metric.InfeasibleTasks == 0 {
		reasons = append(reasons, localizationFailureCompetitiveConditions)
	} else if metric.MissingTasks > 0 {
		reasons = append(reasons, localizationFailurePartialCompetitiveCondition)
	}
	if metric.MeasuredTasks > 0 && metric.BudgetedTasks < metric.MeasuredTasks {
		reasons = append(reasons, localizationFailureCompetitiveBudget)
	}
	coveredTasks := metric.MeasuredTasks + metric.InfeasibleTasks
	if coveredTasks > 0 && metric.ReproducibleTasks < coveredTasks {
		reasons = append(reasons, localizationFailureCompetitiveReproducibility)
	}
	if coveredTasks > 0 && metric.CommandedTasks < coveredTasks {
		reasons = append(reasons, localizationFailureCompetitiveCommand)
	}
	if coveredTasks > 0 && metric.ArtifactedTasks < coveredTasks {
		reasons = append(reasons, localizationFailureCompetitiveArtifact)
	}
	if coveredTasks > 0 && metric.VerifiedTasks < coveredTasks {
		reasons = append(reasons, localizationFailureCompetitiveVerification)
	}
	if metric.InvalidClientClasses > 0 {
		reasons = append(reasons, localizationFailureCompetitiveClientClasses)
	}
	if metric.MeasuredTasks > 0 {
		for _, coverage := range metric.TopKEditPathCoverage {
			for _, reason := range coverage.FailureReasons {
				if reason == localizationFailureCompetitiveCandidates || reason == localizationFailurePartialCompetitiveCandidates {
					reasons = append(reasons, reason)
					return util.UniqueNonEmpty(reasons)
				}
			}
		}
	}
	return util.UniqueNonEmpty(reasons)
}

func localizationCompetitiveCondition(task LocalizationCorpusTask, baseline string) (LocalizationCompetitiveConditionInput, bool) {
	if task.Benchmark == nil {
		return LocalizationCompetitiveConditionInput{}, false
	}
	for _, condition := range task.Benchmark.CompetitiveConditions {
		if normalizeLocalizationCompetitiveBaseline(condition.Baseline) == baseline {
			return condition, true
		}
	}
	return LocalizationCompetitiveConditionInput{}, false
}

func normalizeLocalizationCompetitiveBaseline(value string) string {
	key := normalizedLocalizationKey(value)
	switch key {
	case "raw_search_read", "raw_read_search", "raw_search", "raw_read":
		return "raw_search_read"
	case "generated_baseline_discovery_report", "baseline_discovery_report", "baseline_report":
		return "generated_baseline_discovery_report"
	case "generated_current_discovery_report", "current_discovery_report", "current_report":
		return "generated_current_discovery_report"
	case "context_pack", "context_packet":
		return "context_pack"
	case "aider_repo_map", "aider_style_repo_map", "repo_map":
		return "aider_repo_map"
	case "external_agent_native", "agent_native", "external_agent":
		return "external_agent_native"
	default:
		return key
	}
}

func normalizeLocalizationCompetitiveStatus(value string) string {
	key := normalizedLocalizationKey(value)
	switch key {
	case localizationCompetitiveStatusMeasured, "available", "ran":
		return localizationCompetitiveStatusMeasured
	case localizationCompetitiveStatusInfeasible, "documented_infeasible", "blocked":
		return localizationCompetitiveStatusInfeasible
	default:
		return key
	}
}

func localizationCompetitiveBaselineKnown(value string) bool {
	value = normalizeLocalizationCompetitiveBaseline(value)
	for _, definition := range localizationCompetitiveBaselineDefinitions {
		if definition.ID == value {
			return true
		}
	}
	return false
}

func localizationCompetitiveStatusKnown(value string) bool {
	switch normalizeLocalizationCompetitiveStatus(value) {
	case localizationCompetitiveStatusMeasured, localizationCompetitiveStatusInfeasible:
		return true
	default:
		return false
	}
}

func localizationCompetitiveConditionHasBudget(condition LocalizationCompetitiveConditionInput) bool {
	if condition.Budget == nil {
		return false
	}
	return condition.Budget.TokenBudget > 0 || condition.Budget.ToolCallBudget > 0 || condition.Budget.WallClockBudgetMS > 0
}

func localizationCompetitiveConditionHasReproducibility(condition LocalizationCompetitiveConditionInput) bool {
	return strings.TrimSpace(condition.CommandID) != "" || strings.TrimSpace(condition.ArtifactPath) != ""
}

func localizationCompetitiveConditionHasVerificationMethod(condition LocalizationCompetitiveConditionInput) bool {
	return strings.TrimSpace(condition.VerificationMethod) != ""
}

func RequiredLocalizationExternalCompetitiveBaselineIDs() []string {
	var out []string
	for _, definition := range localizationCompetitiveBaselineDefinitions {
		if definition.External {
			out = append(out, definition.ID)
		}
	}
	return out
}

func LocalizationCompetitiveBaselineIDs() []string {
	out := make([]string, 0, len(localizationCompetitiveBaselineDefinitions))
	for _, definition := range localizationCompetitiveBaselineDefinitions {
		out = append(out, definition.ID)
	}
	return out
}

func localizationContextPacket(task LocalizationCorpusTask) *LocalizationContextPacketEvidence {
	if task.Benchmark == nil {
		return nil
	}
	if task.Benchmark.ContextPacket == nil {
		return nil
	}
	contextPacket := task.Benchmark.ContextPacket
	if len(contextPacket.Files) == 0 && len(contextPacket.Symbols) == 0 && len(contextPacket.Tests) == 0 {
		return nil
	}
	return contextPacket
}

func localizationExpectedFileSet(task LocalizationCorpusTask) map[string]struct{} {
	files := make(map[string]struct{}, len(task.Expected.Files))
	for _, file := range task.Expected.Files {
		if path := localizationAnchorPath(file.Path); path != "" {
			files[path] = struct{}{}
		}
	}
	return files
}

func localizationExpectedSymbolSet(task LocalizationCorpusTask) map[string]struct{} {
	symbols := make(map[string]struct{}, len(task.Expected.Symbols))
	for _, symbol := range task.Expected.Symbols {
		if key := localizationNamedAnchorKey(symbol.Path, symbol.Name); key != "" {
			symbols[key] = struct{}{}
		}
	}
	return symbols
}

func localizationExpectedTestSets(task LocalizationCorpusTask) (map[string]struct{}, map[string]struct{}) {
	exact := make(map[string]struct{}, len(task.Expected.Tests))
	paths := make(map[string]struct{}, len(task.Expected.Tests))
	for _, test := range task.Expected.Tests {
		path := localizationAnchorPath(test.Path)
		if path == "" {
			continue
		}
		if strings.TrimSpace(test.Name) == "" {
			paths[path] = struct{}{}
			continue
		}
		exact[localizationNamedAnchorKey(test.Path, test.Name)] = struct{}{}
	}
	return exact, paths
}

func localizationExpectedTestContains(exact map[string]struct{}, paths map[string]struct{}, candidate string) bool {
	if _, ok := exact[candidate]; ok {
		return true
	}
	path, _, _ := strings.Cut(candidate, "#")
	_, ok := paths[path]
	return ok
}

func localizationCandidateFilePaths(task LocalizationCorpusTask) []string {
	contextPacket := localizationContextPacket(task)
	if contextPacket == nil {
		return nil
	}
	return localizationUniqueCandidateFiles(contextPacket.Files)
}

func localizationUniqueCandidateFiles(files []LocalizationCandidateFile) []string {
	seen := make(map[string]struct{}, len(files))
	paths := make([]string, 0, len(files))
	for _, file := range files {
		path := localizationAnchorPath(file.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func localizationUniqueCandidateSymbols(symbols []LocalizationCandidateSymbol) []string {
	seen := make(map[string]struct{}, len(symbols))
	keys := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		key := localizationNamedAnchorKey(symbol.Path, symbol.Name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func localizationUniqueCandidateTests(tests []LocalizationCandidateTest) []string {
	seen := make(map[string]struct{}, len(tests))
	keys := make([]string, 0, len(tests))
	for _, test := range tests {
		key := localizationTestAnchorKey(test.Path, test.Name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func firstLocalizationCandidates(candidates []string, k int) []string {
	if k >= len(candidates) {
		return candidates
	}
	return candidates[:k]
}

func localizationNamedAnchorKey(path string, name string) string {
	path = localizationAnchorPath(path)
	name = strings.TrimSpace(name)
	if path == "" || name == "" {
		return ""
	}
	return path + "#" + name
}

func localizationTestAnchorKey(path string, name string) string {
	path = localizationAnchorPath(path)
	if path == "" {
		return ""
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return path
	}
	return path + "#" + name
}

func localizationAnchorPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func localizationRate(numerator int, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func localizationBenchmarkMissingInputs(report LocalizationBenchmarkReport) []string {
	seen := make(map[string]struct{})
	add := func(value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
	}
	for _, metric := range report.TopKEditPathCoverage {
		for _, reason := range metric.FailureReasons {
			switch reason {
			case localizationFailureContextPacketCandidates, localizationFailureContextPacketFileCandidates, localizationFailurePartialContextPacket:
				add("context_packet_candidates")
			}
		}
	}
	for _, reason := range report.CandidatePathWaste.FailureReasons {
		switch reason {
		case localizationFailureContextPacketCandidates, localizationFailurePartialContextPacket:
			add("context_packet_candidates")
		}
	}
	for _, reason := range report.BroadReadCount.FailureReasons {
		switch reason {
		case localizationFailureTrajectoryInput, localizationFailurePartialTrajectoryInput:
			add("trajectory_broad_read_count")
		}
	}
	for _, reason := range report.ZeroHitSearchCount.FailureReasons {
		switch reason {
		case localizationFailureTrajectoryInput, localizationFailurePartialTrajectoryInput:
			add("trajectory_zero_hit_search_count")
		}
	}
	for _, condition := range report.CompetitiveConditions {
		for _, reason := range condition.FailureReasons {
			switch reason {
			case localizationFailureCompetitiveConditions, localizationFailurePartialCompetitiveCondition, localizationFailureCompetitiveCandidates, localizationFailurePartialCompetitiveCandidates:
				add("competitive_conditions")
			case localizationFailureCompetitiveBudget:
				add("competitive_condition_budget")
			case localizationFailureCompetitiveReproducibility:
				add("competitive_condition_reproducibility")
			case localizationFailureCompetitiveClientClasses:
				add("competitive_condition_client_classification")
			}
		}
	}
	inputs := make([]string, 0, len(seen))
	for input := range seen {
		inputs = append(inputs, input)
	}
	slices.Sort(inputs)
	return inputs
}

func localizationMetricDefinitions() []LocalizationMetricDefinition {
	return []LocalizationMetricDefinition{
		{
			Name:        "top_k_edit_path_coverage",
			Numerator:   "expected file edit paths present in the first k sanitized context packet file candidates for the matching task",
			Denominator: "unique expected file edit paths across localization corpus tasks",
			FailureReasons: []string{
				localizationFailureNoExpectedEditPaths,
				localizationFailureContextPacketFileCandidates,
				localizationFailurePartialContextPacket,
			},
		},
		{
			Name:        "broad_read_count",
			Numerator:   "broad read or broad discovery events counted from sanitized trajectory evidence",
			Denominator: "tasks with reviewed trajectory evidence for broad read count",
			FailureReasons: []string{
				localizationFailureTrajectoryInput,
				localizationFailurePartialTrajectoryInput,
			},
		},
		{
			Name:        "zero_hit_search_count",
			Numerator:   "search events returning zero hits counted from sanitized trajectory evidence",
			Denominator: "tasks with reviewed trajectory evidence for zero-hit search count",
			FailureReasons: []string{
				localizationFailureTrajectoryInput,
				localizationFailurePartialTrajectoryInput,
			},
		},
		{
			Name:        "candidate_path_waste",
			Numerator:   "candidate_waste counts sanitized file, symbol, and test candidates not matching expected anchors; path_waste counts sanitized file candidates not matching expected edit paths",
			Denominator: "candidate_waste uses all sanitized candidates; path_waste uses sanitized file candidates",
			FailureReasons: []string{
				localizationFailureContextPacketCandidates,
				localizationFailurePartialContextPacket,
			},
		},
		{
			Name:        "competitive_conditions",
			Numerator:   "required competitive baseline tasks measured under a budget or documented infeasible with bounded reason and reproducibility evidence",
			Denominator: "required competitive baselines across localization corpus tasks",
			FailureReasons: []string{
				localizationFailureCompetitiveConditions,
				localizationFailurePartialCompetitiveCondition,
				localizationFailureCompetitiveCandidates,
				localizationFailurePartialCompetitiveCandidates,
				localizationFailureCompetitiveBudget,
				localizationFailureCompetitiveReproducibility,
				localizationFailureCompetitiveClientClasses,
			},
		},
	}
}
