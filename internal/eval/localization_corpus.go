package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LocalizationCorpus struct {
	Name          string                    `json:"name"`
	Version       int                       `json:"version"`
	Experimental  bool                      `json:"experimental"`
	SourceWindow  LocalizationSourceWindow  `json:"source_window"`
	PrivacyReview LocalizationPrivacyReview `json:"privacy_review"`
	Tasks         []LocalizationCorpusTask  `json:"tasks"`
}

type LocalizationSourceWindow struct {
	StartedAt   string   `json:"started_at"`
	EndedAt     string   `json:"ended_at"`
	SourceRoots []string `json:"source_roots"`
	Note        string   `json:"note,omitempty"`
}

type LocalizationPrivacyReview struct {
	Status                   string   `json:"status"`
	ReviewedAt               string   `json:"reviewed_at"`
	Reviewer                 string   `json:"reviewer,omitempty"`
	SensitiveContentExcluded bool     `json:"sensitive_content_excluded"`
	ExcludedContentClasses   []string `json:"excluded_content_classes,omitempty"`
	Note                     string   `json:"note,omitempty"`
}

type LocalizationCorpusTask struct {
	ID                     string                      `json:"id"`
	Name                   string                      `json:"name"`
	Summary                string                      `json:"summary"`
	Themes                 []string                    `json:"themes,omitempty"`
	ClientProductArea      string                      `json:"client_product_area,omitempty"`
	SourceRefs             []LocalizationSourceRef     `json:"source_refs"`
	Expected               LocalizationExpectedAnchors `json:"expected"`
	Benchmark              *LocalizationBenchmarkInput `json:"benchmark,omitempty"`
	Privacy                LocalizationTaskPrivacy     `json:"privacy"`
	SensitiveExclusionNote string                      `json:"sensitive_exclusion_note,omitempty"`
	Provenance             LocalizationTaskProvenance  `json:"provenance"`
}

type LocalizationSourceRef struct {
	Kind       string   `json:"kind"`
	Ref        string   `json:"ref"`
	LogPath    string   `json:"log_path,omitempty"`
	ObservedAt string   `json:"observed_at,omitempty"`
	Digest     string   `json:"digest,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

type LocalizationExpectedAnchors struct {
	Files   []LocalizationExpectedFile   `json:"files"`
	Symbols []LocalizationExpectedSymbol `json:"symbols,omitempty"`
	Tests   []LocalizationExpectedTest   `json:"tests,omitempty"`
}

type LocalizationExpectedFile struct {
	Path      string `json:"path"`
	Rationale string `json:"rationale,omitempty"`
}

type LocalizationExpectedSymbol struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

type LocalizationExpectedTest struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

type LocalizationBenchmarkInput struct {
	ContextPacket         *LocalizationContextPacketEvidence      `json:"context_packet,omitempty"`
	Trajectory            *LocalizationTrajectoryEvidence         `json:"trajectory,omitempty"`
	CompetitiveConditions []LocalizationCompetitiveConditionInput `json:"competitive_conditions,omitempty"`
}

type LocalizationContextPacketEvidence struct {
	Files   []LocalizationCandidateFile   `json:"files,omitempty"`
	Symbols []LocalizationCandidateSymbol `json:"symbols,omitempty"`
	Tests   []LocalizationCandidateTest   `json:"tests,omitempty"`
}

type LocalizationCandidateFile struct {
	Path string `json:"path"`
}

type LocalizationCandidateSymbol struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

type LocalizationCandidateTest struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

type LocalizationTrajectoryEvidence struct {
	BroadReadCount     *int `json:"broad_read_count,omitempty"`
	ZeroHitSearchCount *int `json:"zero_hit_search_count,omitempty"`
}

type LocalizationCompetitiveConditionInput struct {
	Baseline              string                                 `json:"baseline"`
	Status                string                                 `json:"status"`
	CandidateFiles        []LocalizationCandidateFile            `json:"candidate_files,omitempty"`
	Budget                *LocalizationCompetitiveBudgetEvidence `json:"budget,omitempty"`
	CommandID             string                                 `json:"command_id,omitempty"`
	ArtifactPath          string                                 `json:"artifact_path,omitempty"`
	VerificationMethod    string                                 `json:"verification_method,omitempty"`
	InfeasibilityReason   string                                 `json:"infeasibility_reason,omitempty"`
	ClaimImpact           string                                 `json:"claim_impact,omitempty"`
	ClientClassifications []string                               `json:"client_classifications,omitempty"`
}

type LocalizationCompetitiveBudgetEvidence struct {
	TokenBudget       int `json:"token_budget,omitempty"`
	ToolCallBudget    int `json:"tool_call_budget,omitempty"`
	WallClockBudgetMS int `json:"wall_clock_budget_ms,omitempty"`
	UsedToolCalls     int `json:"used_tool_calls,omitempty"`
	ElapsedMS         int `json:"elapsed_ms,omitempty"`
}

type LocalizationTaskPrivacy struct {
	Status                   string   `json:"status"`
	TranscriptIncluded       bool     `json:"transcript_included"`
	FileBodiesIncluded       bool     `json:"file_bodies_included"`
	PromptTextIncluded       bool     `json:"prompt_text_included"`
	SecretsIncluded          bool     `json:"secrets_included"`
	SensitiveContentExcluded bool     `json:"sensitive_content_excluded"`
	ReviewNotes              []string `json:"review_notes,omitempty"`
}

type LocalizationTaskProvenance struct {
	SelectionReason string   `json:"selection_reason"`
	SanitizedFields []string `json:"sanitized_fields,omitempty"`
	SourceCoverage  []string `json:"source_coverage,omitempty"`
}

type LocalizationCorpusReport struct {
	Name               string                      `json:"name"`
	Passed             bool                        `json:"passed"`
	Experimental       bool                        `json:"experimental"`
	TotalTasks         int                         `json:"total_tasks"`
	SourceCoverage     map[string]int              `json:"source_coverage,omitempty"`
	ThemeCoverage      map[string]int              `json:"theme_coverage,omitempty"`
	RedactionStatus    map[string]int              `json:"redaction_status,omitempty"`
	ClientProductAreas map[string]int              `json:"client_product_areas,omitempty"`
	ExpectedFiles      int                         `json:"expected_files"`
	ExpectedSymbols    int                         `json:"expected_symbols"`
	ExpectedTests      int                         `json:"expected_tests"`
	Benchmark          LocalizationBenchmarkReport `json:"benchmark"`
	Findings           []string                    `json:"findings,omitempty"`
}

func LoadLocalizationCorpus(path string) (LocalizationCorpus, error) {
	corpus, err := ReadLocalizationCorpus(path)
	if err != nil {
		return corpus, err
	}
	if err := ValidateLocalizationCorpus(corpus); err != nil {
		return corpus, err
	}
	return corpus, nil
}

func ReadLocalizationCorpus(path string) (LocalizationCorpus, error) {
	var corpus LocalizationCorpus
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return corpus, fmt.Errorf("read localization corpus: %w", err)
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		return corpus, fmt.Errorf("decode localization corpus: %w", err)
	}
	return corpus, nil
}

func ValidateLocalizationCorpus(corpus LocalizationCorpus) error {
	findings := LintLocalizationCorpus(corpus)
	if len(findings) > 0 {
		return fmt.Errorf("localization corpus %q validation failed: %s", corpus.Name, strings.Join(findings, "; "))
	}
	return nil
}

func LintLocalizationCorpus(corpus LocalizationCorpus) []string {
	findings := make([]string, 0)
	add := func(format string, args ...any) {
		findings = append(findings, fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(corpus.Name) == "" {
		add("missing name")
	}
	if corpus.Version <= 0 {
		add("version must be positive")
	}
	if !corpus.Experimental {
		add("corpus must be marked experimental")
	}
	if strings.TrimSpace(corpus.SourceWindow.StartedAt) == "" || strings.TrimSpace(corpus.SourceWindow.EndedAt) == "" {
		add("source window must include started_at and ended_at")
	}
	for _, value := range []string{corpus.SourceWindow.StartedAt, corpus.SourceWindow.EndedAt, corpus.PrivacyReview.ReviewedAt} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			add("timestamp %q must be RFC3339", value)
		}
	}
	if len(corpus.SourceWindow.SourceRoots) == 0 {
		add("source window must name source roots")
	}
	if !localizationStatusIsRedacted(corpus.PrivacyReview.Status) {
		add("privacy review status must be redacted")
	}
	if !corpus.PrivacyReview.SensitiveContentExcluded {
		add("privacy review must confirm sensitive content exclusion")
	}
	if len(corpus.Tasks) == 0 {
		add("corpus has no tasks")
	}
	seen := make(map[string]struct{}, len(corpus.Tasks))
	for i, task := range corpus.Tasks {
		lintLocalizationTask(i, task, seen, &findings)
	}
	return findings
}

func lintLocalizationTask(index int, task LocalizationCorpusTask, seen map[string]struct{}, findings *[]string) {
	add := func(format string, args ...any) {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			id = fmt.Sprintf("<task %d>", index)
		}
		*findings = append(*findings, fmt.Sprintf("%s: %s", id, fmt.Sprintf(format, args...)))
	}
	id := strings.TrimSpace(task.ID)
	if id == "" {
		add("missing id")
	} else if _, ok := seen[id]; ok {
		add("duplicate id")
	} else {
		seen[id] = struct{}{}
	}
	if strings.TrimSpace(task.Name) == "" {
		add("missing name")
	}
	if strings.TrimSpace(task.Summary) == "" {
		add("missing summary")
	}
	if len(task.Themes) == 0 {
		add("missing themes")
	}
	if strings.TrimSpace(task.ClientProductArea) == "" {
		add("missing client_product_area")
	}
	if len(task.SourceRefs) == 0 {
		add("missing source_refs")
	}
	for _, ref := range task.SourceRefs {
		if strings.TrimSpace(ref.Kind) == "" {
			add("source ref missing kind")
		}
		if strings.TrimSpace(ref.Ref) == "" {
			add("source ref missing ref")
		}
		if strings.TrimSpace(ref.LogPath) != "" {
			if err := validateLocalizationRelativePath(ref.LogPath); err != nil {
				add("source ref path %q is invalid: %v", ref.LogPath, err)
			}
		}
		if strings.TrimSpace(ref.ObservedAt) != "" {
			if _, err := time.Parse(time.RFC3339, ref.ObservedAt); err != nil {
				add("source ref observed_at %q must be RFC3339", ref.ObservedAt)
			}
		}
		if digest := strings.TrimSpace(ref.Digest); digest != "" && !strings.HasPrefix(digest, "sha256:") {
			add("source ref digest %q must use sha256 prefix", digest)
		}
	}
	if len(task.Expected.Files) == 0 {
		add("expected files must not be empty")
	}
	for _, file := range task.Expected.Files {
		if err := validateLocalizationRelativePath(file.Path); err != nil {
			add("expected file path %q is invalid: %v", file.Path, err)
		}
	}
	for _, symbol := range task.Expected.Symbols {
		if err := validateLocalizationRelativePath(symbol.Path); err != nil {
			add("expected symbol path %q is invalid: %v", symbol.Path, err)
		}
		if strings.TrimSpace(symbol.Name) == "" {
			add("expected symbol in %q missing name", symbol.Path)
		}
	}
	for _, test := range task.Expected.Tests {
		if err := validateLocalizationRelativePath(test.Path); err != nil {
			add("expected test path %q is invalid: %v", test.Path, err)
		}
	}
	if task.Benchmark != nil {
		lintLocalizationBenchmark(task.Benchmark, add)
	}
	if !localizationStatusIsRedacted(task.Privacy.Status) {
		add("privacy status must be redacted")
	}
	if task.Privacy.TranscriptIncluded || task.Privacy.FileBodiesIncluded || task.Privacy.PromptTextIncluded || task.Privacy.SecretsIncluded {
		add("privacy flags must not include transcript, file bodies, prompt text, or secrets")
	}
	if !task.Privacy.SensitiveContentExcluded {
		add("privacy must confirm sensitive content exclusion")
	}
	if strings.TrimSpace(task.SensitiveExclusionNote) == "" {
		add("missing sensitive exclusion note")
	}
	if strings.TrimSpace(task.Provenance.SelectionReason) == "" {
		add("missing provenance selection reason")
	}
	if len(task.Provenance.SanitizedFields) == 0 {
		add("missing provenance sanitized fields")
	}
	if len(task.Provenance.SourceCoverage) == 0 {
		add("missing provenance source coverage")
	}
}

func lintLocalizationBenchmark(benchmark *LocalizationBenchmarkInput, add func(string, ...any)) {
	if benchmark.ContextPacket != nil {
		for _, file := range benchmark.ContextPacket.Files {
			if err := validateLocalizationRelativePath(file.Path); err != nil {
				add("context packet candidate file path %q is invalid: %v", file.Path, err)
			}
		}
		for _, symbol := range benchmark.ContextPacket.Symbols {
			if err := validateLocalizationRelativePath(symbol.Path); err != nil {
				add("context packet candidate symbol path %q is invalid: %v", symbol.Path, err)
			}
			if strings.TrimSpace(symbol.Name) == "" {
				add("context packet candidate symbol in %q missing name", symbol.Path)
			}
		}
		for _, test := range benchmark.ContextPacket.Tests {
			if err := validateLocalizationRelativePath(test.Path); err != nil {
				add("context packet candidate test path %q is invalid: %v", test.Path, err)
			}
		}
	}
	if benchmark.Trajectory != nil {
		if benchmark.Trajectory.BroadReadCount != nil && *benchmark.Trajectory.BroadReadCount < 0 {
			add("trajectory broad_read_count must be non-negative")
		}
		if benchmark.Trajectory.ZeroHitSearchCount != nil && *benchmark.Trajectory.ZeroHitSearchCount < 0 {
			add("trajectory zero_hit_search_count must be non-negative")
		}
	}
	for _, condition := range benchmark.CompetitiveConditions {
		lintLocalizationCompetitiveCondition(condition, add)
	}
}

func lintLocalizationCompetitiveCondition(condition LocalizationCompetitiveConditionInput, add func(string, ...any)) {
	baseline := normalizeLocalizationCompetitiveBaseline(condition.Baseline)
	status := normalizeLocalizationCompetitiveStatus(condition.Status)
	if baseline == "" {
		add("competitive condition missing baseline")
	} else if !localizationCompetitiveBaselineKnown(baseline) {
		add("competitive condition baseline %q is not recognized", condition.Baseline)
	}
	if status == "" {
		add("competitive condition %q missing status", condition.Baseline)
	} else if !localizationCompetitiveStatusKnown(status) {
		add("competitive condition %q status %q is not recognized", condition.Baseline, condition.Status)
	}
	for _, file := range condition.CandidateFiles {
		if err := validateLocalizationRelativePath(file.Path); err != nil {
			add("competitive condition %q candidate file path %q is invalid: %v", condition.Baseline, file.Path, err)
		}
	}
	if condition.Budget != nil {
		if condition.Budget.TokenBudget < 0 || condition.Budget.ToolCallBudget < 0 || condition.Budget.WallClockBudgetMS < 0 || condition.Budget.UsedToolCalls < 0 || condition.Budget.ElapsedMS < 0 {
			add("competitive condition %q budget values must be non-negative", condition.Baseline)
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "command_id", value: condition.CommandID},
		{name: "verification_method", value: condition.VerificationMethod},
		{name: "infeasibility_reason", value: condition.InfeasibilityReason},
		{name: "claim_impact", value: condition.ClaimImpact},
	} {
		if err := validateLocalizationBoundedToken(field.name, field.value); err != nil {
			add("competitive condition %q %s is invalid: %v", condition.Baseline, field.name, err)
		}
	}
	if strings.TrimSpace(condition.ArtifactPath) != "" {
		if err := validateLocalizationRelativePath(condition.ArtifactPath); err != nil {
			add("competitive condition %q artifact path %q is invalid: %v", condition.Baseline, condition.ArtifactPath, err)
		}
	}
	for _, classification := range condition.ClientClassifications {
		if normalizeClientClassification(classification) == "" {
			add("competitive condition %q client classification is not one of the approved bounded classes", condition.Baseline)
		}
	}
	switch status {
	case localizationCompetitiveStatusMeasured:
		if len(condition.CandidateFiles) == 0 {
			add("competitive condition %q measured status requires candidate_files", condition.Baseline)
		}
		if !localizationCompetitiveConditionHasBudget(condition) {
			add("competitive condition %q measured status requires budget evidence", condition.Baseline)
		}
		if !localizationCompetitiveConditionHasReproducibility(condition) {
			add("competitive condition %q measured status requires command_id or artifact_path", condition.Baseline)
		}
		if !localizationCompetitiveConditionHasVerificationMethod(condition) {
			add("competitive condition %q measured status requires verification_method", condition.Baseline)
		}
	case localizationCompetitiveStatusInfeasible:
		if strings.TrimSpace(condition.InfeasibilityReason) == "" {
			add("competitive condition %q infeasible status requires infeasibility_reason", condition.Baseline)
		}
		if strings.TrimSpace(condition.ClaimImpact) == "" {
			add("competitive condition %q infeasible status requires claim_impact", condition.Baseline)
		}
		if !localizationCompetitiveConditionHasReproducibility(condition) {
			add("competitive condition %q infeasible status requires command_id or artifact_path", condition.Baseline)
		}
		if !localizationCompetitiveConditionHasVerificationMethod(condition) {
			add("competitive condition %q infeasible status requires verification_method", condition.Baseline)
		}
	}
}

func LintLocalizationCorpusPaths(corpus LocalizationCorpus, root string) []string {
	findings := make([]string, 0)
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	for _, task := range corpus.Tasks {
		for _, file := range task.Expected.Files {
			if !localizationPathExists(root, file.Path) {
				findings = append(findings, fmt.Sprintf("%s: expected file missing: %s", task.ID, file.Path))
			}
		}
		for _, symbol := range task.Expected.Symbols {
			if !localizationPathExists(root, symbol.Path) {
				findings = append(findings, fmt.Sprintf("%s: expected symbol path missing: %s", task.ID, symbol.Path))
			}
		}
		for _, test := range task.Expected.Tests {
			if !localizationPathExists(root, test.Path) {
				findings = append(findings, fmt.Sprintf("%s: expected test path missing: %s", task.ID, test.Path))
			}
		}
	}
	return findings
}

func AnalyzeLocalizationCorpus(corpus LocalizationCorpus) LocalizationCorpusReport {
	return AnalyzeLocalizationCorpusWithOptions(corpus, DefaultLocalizationBenchmarkOptions())
}

func AnalyzeLocalizationCorpusWithOptions(corpus LocalizationCorpus, options LocalizationBenchmarkOptions) LocalizationCorpusReport {
	report := LocalizationCorpusReport{
		Name:               corpus.Name,
		Passed:             true,
		Experimental:       corpus.Experimental,
		TotalTasks:         len(corpus.Tasks),
		SourceCoverage:     make(map[string]int),
		ThemeCoverage:      make(map[string]int),
		RedactionStatus:    make(map[string]int),
		ClientProductAreas: make(map[string]int),
		Benchmark:          AnalyzeLocalizationBenchmark(corpus, options),
	}
	for _, task := range corpus.Tasks {
		for _, ref := range task.SourceRefs {
			if key := normalizedLocalizationKey(ref.Kind); key != "" {
				report.SourceCoverage[key]++
			}
		}
		for _, theme := range task.Themes {
			if key := normalizedLocalizationKey(theme); key != "" {
				report.ThemeCoverage[key]++
			}
		}
		if status := normalizedLocalizationKey(task.Privacy.Status); status != "" {
			report.RedactionStatus[status]++
		}
		if area := normalizedLocalizationKey(task.ClientProductArea); area != "" {
			report.ClientProductAreas[area]++
		}
		report.ExpectedFiles += len(task.Expected.Files)
		report.ExpectedSymbols += len(task.Expected.Symbols)
		report.ExpectedTests += len(task.Expected.Tests)
	}
	if status := normalizedLocalizationKey(corpus.PrivacyReview.Status); status != "" {
		report.RedactionStatus["corpus_"+status]++
	}
	report.Findings = LintLocalizationCorpus(corpus)
	if len(report.Findings) > 0 {
		report.Passed = false
	}
	return report
}

func EvaluateLocalizationCorpus(corpus LocalizationCorpus, root string) LocalizationCorpusReport {
	return EvaluateLocalizationCorpusWithOptions(corpus, root, DefaultLocalizationBenchmarkOptions())
}

func EvaluateLocalizationCorpusWithOptions(corpus LocalizationCorpus, root string, options LocalizationBenchmarkOptions) LocalizationCorpusReport {
	report := AnalyzeLocalizationCorpusWithOptions(corpus, options)
	pathFindings := LintLocalizationCorpusPaths(corpus, root)
	if len(pathFindings) > 0 {
		report.Findings = append(report.Findings, pathFindings...)
		report.Passed = false
	}
	return report
}

func EvaluateLocalizationCorpusFile(path string, root string) (LocalizationCorpusReport, error) {
	return EvaluateLocalizationCorpusFileWithOptions(path, root, DefaultLocalizationBenchmarkOptions())
}

func EvaluateLocalizationCorpusFileWithOptions(path string, root string, options LocalizationBenchmarkOptions) (LocalizationCorpusReport, error) {
	corpus, err := ReadLocalizationCorpus(path)
	if err != nil {
		return LocalizationCorpusReport{}, err
	}
	return EvaluateLocalizationCorpusWithOptions(corpus, root, options), nil
}

func validateLocalizationRelativePath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("empty path")
	}
	if filepath.IsAbs(trimmed) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path must stay within the workspace")
	}
	if strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return fmt.Errorf("path must not contain traversal")
	}
	return nil
}

func localizationPathExists(root string, path string) bool {
	if err := validateLocalizationRelativePath(path); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(root, filepath.Clean(path)))
	return err == nil
}

func localizationStatusIsRedacted(status string) bool {
	status = normalizedLocalizationKey(status)
	return status == "redacted" || status == "redacted_reviewed"
}

func normalizedLocalizationKey(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "_", "-", "_", "/", "_")
	return replacer.Replace(value)
}

func validateLocalizationBoundedToken(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > 96 {
		return fmt.Errorf("%s must be 96 characters or fewer", field)
	}
	if localizationContainsSensitiveMarker(value) {
		return fmt.Errorf("%s contains sensitive or raw-content marker", field)
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '_', '-', '.', ':':
			continue
		default:
			return fmt.Errorf("%s must use bounded identifier characters", field)
		}
	}
	return nil
}

func localizationContainsSensitiveMarker(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"raw_prompt",
		"raw_transcript",
		"file_body",
		"private_key",
		"begin_private_key",
		"password",
		"api_key",
		"token=",
		"secret=",
		"customer_",
		"customer-",
		"patient",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
