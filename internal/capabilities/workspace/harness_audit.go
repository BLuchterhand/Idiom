package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"idiom/internal/contracts/autonomy"
)

type harnessAuditReport struct {
	GeneratedAt             string                       `json:"generated_at"`
	Build                   harnessAuditBuildIdentity    `json:"build"`
	WorkspaceRoot           string                       `json:"workspace_root,omitempty"`
	Purpose                 string                       `json:"purpose"`
	Findings                []harnessAuditFinding        `json:"findings,omitempty"`
	HistoricalFindings      []harnessAuditFinding        `json:"historical_findings,omitempty"`
	ToolSurface             *harnessAuditToolSurface     `json:"tool_surface,omitempty"`
	LanguageTooling         []harnessAuditLanguage       `json:"language_tooling,omitempty"`
	BackendAvailability     []harnessAuditBackend        `json:"backend_availability,omitempty"`
	LogBehavior             *harnessAuditLogBehavior     `json:"log_behavior,omitempty"`
	ContractCoverage        *autonomy.EventAuditReport   `json:"contract_coverage,omitempty"`
	PlanningQuality         *harnessAuditPlanningQuality `json:"planning_quality,omitempty"`
	RankerLearning          *harnessAuditRankerLearning  `json:"ranker_learning,omitempty"`
	ContextCache            *harnessAuditContextCache    `json:"context_cache,omitempty"`
	FreshnessHealth         *harnessAuditFreshnessHealth `json:"freshness_health,omitempty"`
	ImprovementQuestions    []harnessAuditQuestion       `json:"improvement_questions,omitempty"`
	ConsolidationCandidates []harnessAuditQuestion       `json:"consolidation_candidates,omitempty"`
}

type harnessAuditBuildIdentity struct {
	Version          string `json:"version"`
	Commit           string `json:"commit,omitempty"`
	Date             string `json:"date,omitempty"`
	SourceOrDevBuild bool   `json:"source_or_dev_build"`
}

var harnessAuditBuildIdentityMu sync.RWMutex
var harnessAuditBuildIdentityCurrent = harnessAuditBuildIdentity{
	Version:          "dev",
	SourceOrDevBuild: true,
}

func SetHarnessAuditBuildIdentity(version string, commit string, date string) {
	identity := harnessAuditBuildIdentity{
		Version: strings.TrimSpace(version),
		Commit:  strings.TrimSpace(commit),
		Date:    strings.TrimSpace(date),
	}
	if identity.Version == "" {
		identity.Version = "dev"
	}
	identity.SourceOrDevBuild = identity.Version == "dev" || identity.Commit == ""

	harnessAuditBuildIdentityMu.Lock()
	defer harnessAuditBuildIdentityMu.Unlock()
	harnessAuditBuildIdentityCurrent = identity
}

func currentHarnessAuditBuildIdentity() harnessAuditBuildIdentity {
	harnessAuditBuildIdentityMu.RLock()
	defer harnessAuditBuildIdentityMu.RUnlock()

	return harnessAuditBuildIdentityCurrent
}

func HarnessAuditBuildIdentityMetadata() map[string][]string {
	identity := currentHarnessAuditBuildIdentity()
	out := map[string][]string{
		"build_version":       {identity.Version},
		"source_or_dev_build": {fmt.Sprint(identity.SourceOrDevBuild)},
	}
	if strings.TrimSpace(identity.Commit) != "" {
		out["build_commit"] = []string{identity.Commit}
	}
	if strings.TrimSpace(identity.Date) != "" {
		out["build_date"] = []string{identity.Date}
	}

	return out
}

type harnessAuditFinding struct {
	ID             string                             `json:"id"`
	Severity       string                             `json:"severity"`
	Evidence       string                             `json:"evidence"`
	Recommendation string                             `json:"recommendation,omitempty"`
	Tools          []string                           `json:"tools,omitempty"`
	EventIDs       []string                           `json:"event_ids,omitempty"`
	ContractIDs    []string                           `json:"contract_ids,omitempty"`
	DetailCount    int                                `json:"detail_count,omitempty"`
	Details        []autonomy.EventAuditFindingDetail `json:"details,omitempty"`
}

type harnessAuditCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type harnessAuditQuestion struct {
	Area      string `json:"area"`
	Question  string `json:"question"`
	Evidence  string `json:"evidence,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

func (m *Manager) harnessAudit(ctx context.Context, req ToolRequest) ToolResult {
	purpose := normalizeHarnessAuditPurpose(req.Purpose)
	report := harnessAuditReport{
		GeneratedAt:   time.Now().Format(time.RFC3339),
		Build:         currentHarnessAuditBuildIdentity(),
		WorkspaceRoot: m.cfg.Root,
		Purpose:       purpose,
	}

	if purpose == "all" || purpose == "tool_surface" {
		toolSurface := m.auditToolSurface()

		report.ToolSurface = &toolSurface
		report.LanguageTooling = m.auditLanguageTooling()
		report.BackendAvailability = m.auditBackendAvailability(report.LanguageTooling)
		report.Findings = append(report.Findings, toolSurfaceFindings(toolSurface)...)
		report.Findings = append(report.Findings, backendFindings(report.BackendAvailability)...)
		report.ConsolidationCandidates = append(report.ConsolidationCandidates, staticConsolidationCandidates()...)
	}

	if purpose == "all" || purpose == "behavior" || purpose == "planning_quality" {
		behavior, contractCoverage, findings, questions, err := m.auditLogBehavior(req)
		if err != nil {
			return ToolResult{Tool: "harness_audit", Summary: "Harness audit rejected", Error: err.Error()}
		}

		report.LogBehavior = behavior
		report.ContractCoverage = contractCoverage
		report.Findings = append(report.Findings, findings...)
		report.ImprovementQuestions = append(report.ImprovementQuestions, questions...)

		if purpose == "all" || purpose == "planning_quality" {
			report.PlanningQuality = planningQualityFromFeedback(behavior.Aggregate.Feedback)
			report.Findings = append(report.Findings, planningQualityFindings(report.PlanningQuality)...)
			report.ImprovementQuestions = append(report.ImprovementQuestions, planningQualityQuestions(report.PlanningQuality)...)
		}
	}

	if purpose == "all" || purpose == "behavior" || purpose == "planning_quality" {
		learning := buildRankerLearningAudit(ctx, m.cfg.Root)

		report.RankerLearning = learning
		report.Findings = append(report.Findings, rankerLearningFindings(learning)...)
		if rankerLearningSuggestsChanges(learning) {
			report.ImprovementQuestions = append(report.ImprovementQuestions, rankerLearningQuestions(learning)...)
		}
	}

	if purpose == "all" || purpose == "behavior" {
		cache := buildContextCacheAudit(ctx, m.cfg.Root)
		if cache != nil {
			report.ContextCache = cache
			report.Findings = append(report.Findings, contextCacheFindings(cache)...)
		}

		freshness := buildFreshnessHealthAudit(
			logBehaviorContextStats(report.LogBehavior),
			logBehaviorFreshnessResolution(report.LogBehavior),
			logBehaviorContextStatsHistoricalOnly(report.LogBehavior),
		)

		report.FreshnessHealth = &freshness
		report.Findings = append(report.Findings, freshnessHealthFindings(&freshness)...)
	}

	report.Findings, report.HistoricalFindings = splitHistoricalHarnessAuditFindings(report.Findings)
	report.ImprovementQuestions = append(report.ImprovementQuestions, staticImprovementQuestions(report)...)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "harness_audit", Summary: "Harness audit failed", Error: err.Error()}
	}

	return ToolResult{
		Tool:    "harness_audit",
		Summary: harnessAuditSummary(report),
		Output:  string(data),
		Facts:   exposedCapabilityEvidence(m.ToolSurfaceSnapshot().AllowedTools(), "harness_audit"),
	}
}

func normalizeHarnessAuditPurpose(purpose string) string {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case "", "all", "audit":
		return "all"
	case "tool", "tools", "tooling", "tool_surface", "surface", "static":
		return "tool_surface"
	case "behavior", "behaviour", "logs", "log_behavior", "efficiency", "perf":
		return "behavior"
	case "planning_quality", "planning-quality", "planning", "planner_quality":
		return "planning_quality"
	default:
		return "all"
	}
}

func auditString(value any) string {
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

func auditStringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return uniqueSortedHarnessAuditStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if text := auditString(item); text != "" {
				out = append(out, text)
			}
		}
		return uniqueSortedHarnessAuditStrings(out)
	case string:
		if text := strings.TrimSpace(v); text != "" {
			return []string{text}
		}
	}
	return nil
}

func firstNonEmptyHarnessAuditString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}

	return ""
}

func uniqueSortedHarnessAuditStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)

	return out
}

func uniqueSortedHarnessAuditInts(values []int) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Ints(out)

	return out
}

func staticImprovementQuestions(report harnessAuditReport) []harnessAuditQuestion {
	var questions []harnessAuditQuestion

	if report.ToolSurface != nil &&
		len(report.ToolSurface.ManifestWithoutSchema) == 0 &&
		len(report.ToolSurface.SchemaWithoutManifest) == 0 &&
		len(report.ToolSurface.InvalidSchemas) == 0 &&
		report.ToolSurface.ManifestToolCount > 0 {

		questions = append(questions, harnessAuditQuestion{
			Area:      "tool_surface",
			Question:  "Should tool manifest, schema, and recovery inventories be generated from one source of truth?",
			Evidence:  fmt.Sprintf("%d manifest tools and %d schema tools currently align", report.ToolSurface.ManifestToolCount, report.ToolSurface.SchemaToolCount),
			Candidate: "Keep the audit as the drift detector first; generate only after repeated drift shows up.",
		})
	}

	activeLanguages := 0

	for _, lang := range report.LanguageTooling {
		if lang.Active {
			activeLanguages++
		}
	}

	if len(report.LanguageTooling) > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "language_tooling",
			Question:  "Which language capabilities are only syntactic fallbacks in practice, and do they need LSP setup, better fallbacks, or clearer docs?",
			Evidence:  fmt.Sprintf("%d language families registered; %d active in this workspace", len(report.LanguageTooling), activeLanguages),
			Candidate: "Use behavior logs plus precision_tier results to prioritize one language/backend at a time.",
		})
	}

	return questions
}

func auditTopCounts(values map[string]int, limit int) []harnessAuditCount {
	out := make([]harnessAuditCount, 0, len(values))
	for name, count := range values {
		if strings.TrimSpace(name) == "" || count <= 0 {
			continue
		}

		out = append(out, harnessAuditCount{Name: name, Count: count})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}

		return out[i].Count > out[j].Count
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out
}

func staticConsolidationCandidates() []harnessAuditQuestion {
	return []harnessAuditQuestion{{
		Area:      "language_tooling",
		Question:  "Can repeated per-language capability schemas and examples be derived from LanguageRegistry?",
		Evidence:  "Go, C/C++, Python, TypeScript, Rust, and Java expose the same standard capability matrix with language-prefixed tools.",
		Candidate: "Generate conformance checks and manifest examples from the capability registry before generating implementation code.",
	}}
}

func harnessAuditSummary(report harnessAuditReport) string {
	parts := []string{fmt.Sprintf("Ran harness audit (%s)", report.Purpose)}
	if report.ToolSurface != nil && report.ToolSurface.ManifestToolCount > 0 {
		parts = append(parts, fmt.Sprintf("tool_surface=%d tools/%d drift", report.ToolSurface.ManifestToolCount, report.ToolSurface.DriftFindingCount))
		if report.ToolSurface.CapabilityRegistry != nil {
			coverage := report.ToolSurface.CapabilityRegistry

			parts = append(parts, fmt.Sprintf("capability_coverage=%d/%d direct=%d explained=%d routed=%d disabled=%d fallback_reasons=%d unmapped=%d unsafe=%d", coverage.MappedReadOnlyAdapterCount, coverage.ReadOnlyAdapterCount, coverage.DirectOnlyAdapterCount, coverage.ExplainedAdapterCount, coverage.RoutedCapabilityCount, coverage.DisabledCapabilityCount, coverage.FallbackReasonCount, coverage.UnmappedAdapterCount, coverage.UnsafeMappedAdapterCount))
		}
	}

	if report.LogBehavior != nil {
		parts = append(parts, fmt.Sprintf("logs=%d scanned", report.LogBehavior.LogsScanned))
		if report.LogBehavior.ChildLifecycle != nil {
			child := report.LogBehavior.ChildLifecycle
			parts = append(parts, fmt.Sprintf("child_lifecycle=%d records/%d invocations/%d terminals/%d acceptances", child.Records, child.Invocations, child.Terminals, child.Acceptances))
		}
		if report.LogBehavior.IntentLoop != nil {
			intentLoop := report.LogBehavior.IntentLoop
			parts = append(parts, fmt.Sprintf("intent_loop=%d starts/%d cycle_completes/%d completions/%d gaps", intentLoop.Starts, intentLoop.CycleCompletes, intentLoop.Completions, intentLoop.TotalReportedGaps))
		}
	}

	if report.ContractCoverage != nil {
		parts = append(parts, fmt.Sprintf("contract_events=%d", report.ContractCoverage.TotalEvents))

		gate := report.ContractCoverage.GateCoverage

		if gate.DecisionEvents > 0 {
			missingGateFields := len(gate.MissingPackIDRefs) + len(gate.MissingEnforcementModeRefs) + len(gate.MissingSafeAlternativesRefs)

			parts = append(parts, fmt.Sprintf("gate_decisions=%d packs=%d enforcement_modes=%d observed_facts=%d safe_alternatives=%d missing_gate_fields=%d", gate.DecisionEvents, len(gate.DecisionCountsByPackID), len(gate.DecisionCountsByEnforcementMode), gate.EventsWithObservedFacts, gate.EventsWithSafeAlternatives, missingGateFields))
		}
	}

	if report.RankerLearning != nil && report.RankerLearning.LearningSummary != nil {
		learning := report.RankerLearning.LearningSummary

		parts = append(parts, fmt.Sprintf("ranker_learning_events=%d accepted=%d rejected=%d reasons=%d", learning.TotalEvents, learning.AcceptedCount, learning.RejectedCount, len(learning.ReasonCounts)))
	}

	if report.ContextCache != nil {
		parts = append(parts, fmt.Sprintf("context_cache_rows=%d hits=%d hit_rate=%.0f%% tokens_saved=%d rediscovery_avoided=%d", report.ContextCache.Rows, report.ContextCache.SessionHits, report.ContextCache.HitRate*100, report.ContextCache.EstimatedFullTokensSaved, report.ContextCache.EstimatedRediscoveryTokensAvoided))
	}

	if len(report.Findings) > 0 {
		parts = append(parts, fmt.Sprintf("findings=%d", len(report.Findings)))
	}

	if len(report.HistoricalFindings) > 0 {
		parts = append(parts, fmt.Sprintf("historical_findings=%d", len(report.HistoricalFindings)))
	}

	if len(report.ImprovementQuestions) > 0 {
		parts = append(parts, fmt.Sprintf("questions=%d", len(report.ImprovementQuestions)))
	}

	return strings.Join(parts, "; ")
}

func auditTopCount(values map[string]int) string {
	bestKey := "none"
	bestValue := 0

	for key, value := range values {
		if value > bestValue || value == bestValue && key < bestKey {
			bestKey = key
			bestValue = value
		}
	}

	if bestValue == 0 {
		return "none"
	}

	return fmt.Sprintf("%s=%d", bestKey, bestValue)
}
