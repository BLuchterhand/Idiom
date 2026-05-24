package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"idiom/internal/autonomy"
	"idiom/internal/comprehension"
	"idiom/internal/discovery"
	"idiom/internal/eval"
	"idiom/internal/util"
)

const (
	harnessAuditDefaultLogLimit = 80
	harnessAuditMaxLogLimit     = 5000
)

type harnessAuditReport struct {
	GeneratedAt             string                       `json:"generated_at"`
	WorkspaceRoot           string                       `json:"workspace_root,omitempty"`
	Purpose                 string                       `json:"purpose"`
	Findings                []harnessAuditFinding        `json:"findings,omitempty"`
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

// harnessAuditContextCache surfaces graph_views (context_pack cache)
// state so audits can measure whether the cache is doing useful work.
// The eval suite's "fewer broad reads / fewer repeated searches"
// success criteria are otherwise unmeasurable.
type harnessAuditContextCache struct {
	Rows                                  int            `json:"rows"`
	Fresh                                 int            `json:"fresh"`
	Stale                                 int            `json:"stale"`
	NonCurrent                            int            `json:"non_current"`
	CurrentRows                           int            `json:"current_rows"`
	CurrentFresh                          int            `json:"current_fresh"`
	CurrentStale                          int            `json:"current_stale"`
	HistoricalRows                        int            `json:"historical_rows"`
	CurrentIndexID                        string         `json:"current_index_id,omitempty"`
	BytesStored                           int64          `json:"bytes_stored"`
	OldestCreated                         string         `json:"oldest_created,omitempty"`
	NewestCreated                         string         `json:"newest_created,omitempty"`
	WorkspaceStateKeyedRows               int            `json:"workspace_state_keyed_rows"`
	ContractRows                          int            `json:"contract_rows"`
	UnresolvedGapRows                     int            `json:"unresolved_gap_rows"`
	FreshUnresolvedGapRows                int            `json:"fresh_unresolved_gap_rows"`
	StaleUnresolvedGapRows                int            `json:"stale_unresolved_gap_rows"`
	CurrentUnresolvedGapRows              int            `json:"current_unresolved_gap_rows"`
	CurrentFreshUnresolvedGapRows         int            `json:"current_fresh_unresolved_gap_rows"`
	UnresolvedGapReasonCounts             map[string]int `json:"unresolved_gap_reason_counts,omitempty"`
	CurrentUnresolvedGapReasonCounts      map[string]int `json:"current_unresolved_gap_reason_counts,omitempty"`
	CurrentFreshUnresolvedGapReasonCounts map[string]int `json:"current_fresh_unresolved_gap_reason_counts,omitempty"`
	SessionHits                           int64          `json:"session_hits"`
	SessionMisses                         int64          `json:"session_misses"`
	SessionWrites                         int64          `json:"session_writes"`
	FreshnessHits                         int64          `json:"freshness_hits"`
	StalePreventions                      int64          `json:"stale_preventions"`
	StaleRowMisses                        int64          `json:"stale_row_misses"`
	HitRate                               float64        `json:"hit_rate,omitempty"`
	StaleRatio                            float64        `json:"stale_ratio,omitempty"`
	CurrentStaleRatio                     float64        `json:"current_stale_ratio,omitempty"`
}

type harnessAuditFreshnessHealth struct {
	ReadThroughAttempts        int64            `json:"read_through_attempts"`
	Blockers                   int64            `json:"blockers"`
	BlockersByReason           map[string]int64 `json:"blockers_by_reason,omitempty"`
	StaleUseBlocked            int64            `json:"stale_use_blocked"`
	StaleUseReported           int64            `json:"stale_use_reported"`
	StaleUseAllowed            int64            `json:"stale_use_allowed"`
	StaleUseFresh              int64            `json:"stale_use_fresh"`
	StaleUseStateCounts        map[string]int64 `json:"stale_use_state_counts,omitempty"`
	StaleCachedViewPreventions int64            `json:"stale_cached_view_preventions"`
	StaleRowMisses             int64            `json:"stale_row_misses"`
	RefreshFailures            int64            `json:"refresh_failures"`
	RootRebuilds               int64            `json:"root_rebuilds"`
	AllowStaleInvocations      int64            `json:"allow_stale_invocations"`
	TelemetryReadThrough       int64            `json:"telemetry_read_through_results,omitempty"`
	TelemetryBlockers          int64            `json:"telemetry_blockers,omitempty"`
	TelemetryBudgetExceeded    int64            `json:"telemetry_budget_exceeded,omitempty"`
	TelemetryRefreshAttempts   int64            `json:"telemetry_refresh_attempts,omitempty"`
	TelemetryRefreshFailures   int64            `json:"telemetry_refresh_failures,omitempty"`
	TelemetryRefreshFiles      int64            `json:"telemetry_refresh_files_indexed,omitempty"`
	TelemetryAllowStale        int64            `json:"telemetry_allow_stale_invocations,omitempty"`
	TelemetryPolicyCounts      map[string]int64 `json:"telemetry_policy_counts,omitempty"`
	TelemetryReasonCounts      map[string]int64 `json:"telemetry_reason_counts,omitempty"`
}

// harnessAuditRankerLearning surfaces the state of the per-workspace
// learnable ranker weights and the learning log so audits can recommend
// where to wire additional trigger sites. This is the "audit checking" layer
// over the eval and CLI triggers; future automated recommendations read
// here.
type harnessAuditRankerLearning struct {
	WeightsPath            string                            `json:"weights_path"`
	WeightsFileExists      bool                              `json:"weights_file_exists"`
	WeightCount            int                               `json:"weight_count"`
	MaxMultiplier          float64                           `json:"max_multiplier,omitempty"`
	MinMultiplier          float64                           `json:"min_multiplier,omitempty"`
	LearningLogPath        string                            `json:"learning_log_path"`
	LearningLogExists      bool                              `json:"learning_log_exists"`
	LearningSummary        *comprehension.LearningLogSummary `json:"learning_summary,omitempty"`
	WiredSources           []string                          `json:"wired_sources,omitempty"`
	DormantSources         []string                          `json:"dormant_sources,omitempty"`
	TriggerRecommendations []harnessAuditRankerTrigger       `json:"trigger_recommendations,omitempty"`
}

// harnessAuditRankerTrigger describes one candidate trigger site for the
// learning loop. The audit lists every known candidate, marks whether it has
// been observed in the learning log, and explains how it would be wired.
type harnessAuditRankerTrigger struct {
	Name        string `json:"name"`
	Status      string `json:"status"` // wired | dormant | partial
	Component   string `json:"component,omitempty"`
	Description string `json:"description"`
	Suggested   string `json:"suggested,omitempty"`
}

type harnessAuditFinding struct {
	ID             string   `json:"id"`
	Severity       string   `json:"severity"`
	Evidence       string   `json:"evidence"`
	Recommendation string   `json:"recommendation,omitempty"`
	Tools          []string `json:"tools,omitempty"`
}

type harnessAuditToolSurface struct {
	ManifestToolCount             int                            `json:"manifest_tool_count"`
	SchemaToolCount               int                            `json:"schema_tool_count"`
	ExportedSchemaToolCount       int                            `json:"exported_schema_tool_count"`
	DriftFindingCount             int                            `json:"drift_finding_count"`
	DuplicateManifestTools        []string                       `json:"duplicate_manifest_tools,omitempty"`
	ManifestWithoutSchema         []string                       `json:"manifest_without_schema,omitempty"`
	SchemaWithoutManifest         []string                       `json:"schema_without_manifest,omitempty"`
	ManifestWithoutExportedSchema []string                       `json:"manifest_without_exported_schema,omitempty"`
	ExportedSchemaWithoutManifest []string                       `json:"exported_schema_without_manifest,omitempty"`
	SchemaWithoutExportedSchema   []string                       `json:"schema_without_exported_schema,omitempty"`
	ExportedSchemaWithoutSchema   []string                       `json:"exported_schema_without_schema,omitempty"`
	InvalidSchemas                []string                       `json:"invalid_schemas,omitempty"`
	ExportError                   string                         `json:"export_error,omitempty"`
	SuggestionToolCount           int                            `json:"suggestion_tool_count"`
	SuggestionMissingTools        []string                       `json:"suggestion_missing_tools,omitempty"`
	SuggestionWithoutManifest     []string                       `json:"suggestion_without_manifest,omitempty"`
	StaleUseBlockerCoverage       []harnessAuditStaleUseCoverage `json:"stale_use_blocker_coverage,omitempty"`
	StaleUseBlockerMissingTools   []string                       `json:"stale_use_blocker_missing_tools,omitempty"`
	StaleUseReportOnlyTools       []string                       `json:"stale_use_report_only_tools,omitempty"`
	CapabilityRegistry            *ReadOnlyCapabilityCoverage    `json:"capability_registry,omitempty"`
}

type harnessAuditStaleUseCoverage struct {
	Tool                string `json:"tool"`
	State               string `json:"state"`
	DefaultPolicy       string `json:"default_policy"`
	SchemaErrorClass    bool   `json:"schema_error_class"`
	RuntimeBlocker      bool   `json:"runtime_blocker"`
	ReportOnly          bool   `json:"report_only,omitempty"`
	LiveFallbackOnStale bool   `json:"live_fallback_on_stale,omitempty"`
}

type harnessAuditLanguage struct {
	Language     string                     `json:"language"`
	Active       bool                       `json:"active"`
	Extensions   []string                   `json:"extensions,omitempty"`
	Capabilities []harnessAuditLanguageTool `json:"capabilities"`
	Gaps         []string                   `json:"gaps,omitempty"`
}

type harnessAuditLanguageTool struct {
	Capability string `json:"capability"`
	Tool       string `json:"tool,omitempty"`
	InManifest bool   `json:"in_manifest"`
	HasSchema  bool   `json:"has_schema"`
}

type harnessAuditBackend struct {
	Name      string   `json:"name"`
	Binaries  []string `json:"binaries"`
	Available bool     `json:"available"`
	Path      string   `json:"path,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	Impact    string   `json:"impact,omitempty"`
	Active    bool     `json:"active,omitempty"`
}

type harnessAuditLogBehavior struct {
	LogLimit           int                             `json:"log_limit"`
	LogsConsidered     int                             `json:"logs_considered"`
	LogsScanned        int                             `json:"logs_scanned"`
	Sources            []harnessAuditLogSource         `json:"sources,omitempty"`
	Warnings           []string                        `json:"warnings,omitempty"`
	Aggregate          eval.PerfAggregate              `json:"aggregate"`
	TopPatterns        []eval.PerfPatternHit           `json:"top_patterns,omitempty"`
	TopTools           []eval.PerfToolMix              `json:"top_tools,omitempty"`
	WorstOffenders     []eval.PerfWorstOffender        `json:"worst_offenders,omitempty"`
	MalformedOutput    *harnessAuditMalformedOutput    `json:"malformed_output,omitempty"`
	LaneHistory        *harnessAuditLaneHistory        `json:"lane_history,omitempty"`
	InProgressSessions []harnessAuditInProgressSession `json:"in_progress_sessions,omitempty"`
	IncompleteSessions []harnessAuditIncompleteSession `json:"incomplete_sessions,omitempty"`
}

type harnessAuditMalformedOutput struct {
	LogsHit                int            `json:"logs_hit,omitempty"`
	EventCounts            map[string]int `json:"event_counts,omitempty"`
	ValidationResultCounts map[string]int `json:"validation_result_counts,omitempty"`
	FailureClassCounts     map[string]int `json:"failure_class_counts,omitempty"`
	CompileOutcomeCounts   map[string]int `json:"compile_outcome_counts,omitempty"`
	RecoveryOutcomeCounts  map[string]int `json:"recovery_outcome_counts,omitempty"`
	SchemaVersionCounts    map[string]int `json:"schema_version_counts,omitempty"`
}

type harnessAuditLaneHistory struct {
	Path            string                       `json:"path"`
	Exists          bool                         `json:"exists"`
	Runs            int                          `json:"runs"`
	ByMode          map[string]int               `json:"by_mode,omitempty"`
	ByFinalState    map[string]int               `json:"by_final_state,omitempty"`
	ByExitCode      map[string]int               `json:"by_exit_code,omitempty"`
	TotalModelCalls int                          `json:"total_model_calls,omitempty"`
	TotalToolCalls  int                          `json:"total_tool_calls,omitempty"`
	TotalTokens     int                          `json:"total_tokens,omitempty"`
	TotalDurationMS int                          `json:"total_duration_ms,omitempty"`
	Recent          []harnessAuditLaneHistoryRun `json:"recent,omitempty"`
	Warnings        []string                     `json:"warnings,omitempty"`
}

type harnessAuditLaneHistoryRun struct {
	Timestamp    string   `json:"ts,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Brief        string   `json:"brief,omitempty"`
	ExitCode     int      `json:"exit_code"`
	FinalState   string   `json:"final_state,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	QueuePool    string   `json:"queue_pool,omitempty"`
	ModelCalls   int      `json:"model_calls,omitempty"`
	ToolCalls    int      `json:"tool_calls,omitempty"`
	TotalTokens  int      `json:"total_tokens,omitempty"`
	DurationMS   int      `json:"duration_ms,omitempty"`
	StopReason   string   `json:"stop_reason,omitempty"`
	Degradations []string `json:"degradations,omitempty"`
}

type harnessAuditLogSource struct {
	Pattern  string `json:"pattern"`
	Matched  int    `json:"matched"`
	Selected int    `json:"selected"`
	Capped   bool   `json:"capped,omitempty"`
}

type harnessAuditInProgressSession struct {
	TelemetryLog    string `json:"telemetry_log,omitempty"`
	ConversationLog string `json:"conversation_log,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Command         string `json:"command,omitempty"`
	PID             int    `json:"pid,omitempty"`
}

type harnessAuditIncompleteSession struct {
	TelemetryLog    string   `json:"telemetry_log,omitempty"`
	ConversationLog string   `json:"conversation_log,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	Command         string   `json:"command,omitempty"`
	Missing         []string `json:"missing"`
}

type harnessAuditPlanningQuality struct {
	FeedbackTotal       int                 `json:"feedback_total"`
	PlanningFeedback    int                 `json:"planning_feedback"`
	VerifiedFeedback    int                 `json:"verified_feedback,omitempty"`
	HighSeverity        int                 `json:"high_severity,omitempty"`
	PlanningCategories  map[string]int      `json:"planning_categories,omitempty"`
	TopComponents       []harnessAuditCount `json:"top_components,omitempty"`
	RecommendedSurfaces []string            `json:"recommended_surfaces,omitempty"`
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

var harnessAuditStandardCapabilities = []Capability{
	CapOverview,
	CapEntrypoints,
	CapFindSymbol,
	CapWorkspaceSymbol,
	CapDefinition,
	CapReferences,
	CapCallsFrom,
	CapCallsInto,
	CapReadSymbol,
	CapRelatedFiles,
	CapImplementations,
	CapDiagnostics,
	CapFileOutline,
}

func (m *Manager) harnessAudit(req ToolRequest) ToolResult {
	purpose := normalizeHarnessAuditPurpose(req.Purpose)
	report := harnessAuditReport{
		GeneratedAt:   time.Now().Format(time.RFC3339),
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
		report.ConsolidationCandidates = append(report.ConsolidationCandidates, staticConsolidationCandidates(m.cfg.Root)...)
	}

	if purpose == "all" || purpose == "behavior" || purpose == "planning_quality" {
		behavior, contractCoverage, findings, questions, err := m.auditLogBehavior(req)
		if err != nil {
			return ToolResult{Tool: "harness_audit", Summary: "Harness audit rejected", Error: err.Error()}
		}
		report.LogBehavior = behavior
		report.ContractCoverage = contractCoverage
		report.Findings = append(report.Findings, findings...)
		report.Findings = append(report.Findings, contractCoverageFindings(contractCoverage)...)
		report.ImprovementQuestions = append(report.ImprovementQuestions, questions...)
		if purpose == "all" || purpose == "planning_quality" {
			report.PlanningQuality = planningQualityFromFeedback(behavior.Aggregate.Feedback)
			report.Findings = append(report.Findings, planningQualityFindings(report.PlanningQuality)...)
			report.ImprovementQuestions = append(report.ImprovementQuestions, planningQualityQuestions(report.PlanningQuality)...)
		}
	}
	if purpose == "all" || purpose == "behavior" || purpose == "planning_quality" {
		learning := buildRankerLearningAudit(m.cfg.Root)
		report.RankerLearning = learning
		report.Findings = append(report.Findings, rankerLearningFindings(learning)...)
		report.ImprovementQuestions = append(report.ImprovementQuestions, rankerLearningQuestions(learning)...)
	}
	if purpose == "all" || purpose == "behavior" {
		cache := buildContextCacheAudit(m.cfg.Root)
		if cache != nil {
			report.ContextCache = cache
			report.Findings = append(report.Findings, contextCacheFindings(cache)...)
		}
		freshness := buildFreshnessHealthAudit(logBehaviorContextStats(report.LogBehavior))
		report.FreshnessHealth = &freshness
		report.Findings = append(report.Findings, freshnessHealthFindings(&freshness)...)
	}

	report.ImprovementQuestions = append(report.ImprovementQuestions, staticImprovementQuestions(report)...)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "harness_audit", Summary: "Harness audit failed", Error: err.Error()}
	}

	return ToolResult{
		Tool:    "harness_audit",
		Summary: harnessAuditSummary(report),
		Output:  string(data),
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

func (m *Manager) auditToolSurface() harnessAuditToolSurface {
	manifest := toolManifestToolsAcrossBuiltInSurfaces()
	schemaDefs := BuildToolDefinitions(nil)
	export, exportErr := BuildToolSchemaExport(nil)
	return auditToolSurfaceFromSources(manifest, schemaDefs, builtinToolNamesForSuggest(), export, exportErr)
}

func auditToolSurfaceFromSources(manifest []string, schemaDefs []ToolDefinition, suggestions []string, export ToolSchemaExport, exportErr error) harnessAuditToolSurface {
	manifestSet := auditSet(manifest)
	schemaNames, invalidSchemas := auditSchemaDefinitionNames(schemaDefs)
	schemaSet := auditSet(schemaNames)
	suggestSet := auditSet(suggestions)
	exportNames := auditExportedToolNames(export)
	exportSet := auditSet(exportNames)
	coverage, missingBlockers, reportOnlyTools := auditStaleUseBlockerCoverage(export)

	surface := harnessAuditToolSurface{
		ManifestToolCount:             len(manifestSet),
		SchemaToolCount:               len(schemaSet),
		ExportedSchemaToolCount:       len(exportSet),
		DuplicateManifestTools:        auditDuplicates(manifest),
		ManifestWithoutSchema:         auditSetDiff(manifestSet, schemaSet),
		SchemaWithoutManifest:         auditSetDiff(schemaSet, manifestSet),
		ManifestWithoutExportedSchema: auditSetDiff(manifestSet, exportSet),
		ExportedSchemaWithoutManifest: auditSetDiff(exportSet, manifestSet),
		SchemaWithoutExportedSchema:   auditSetDiff(schemaSet, exportSet),
		ExportedSchemaWithoutSchema:   auditSetDiff(exportSet, schemaSet),
		InvalidSchemas:                invalidSchemas,
		SuggestionToolCount:           len(suggestSet),
		SuggestionMissingTools:        auditSetDiff(manifestSet, suggestSet),
		SuggestionWithoutManifest:     auditSetDiff(suggestSet, manifestSet),
		StaleUseBlockerCoverage:       coverage,
		StaleUseBlockerMissingTools:   missingBlockers,
		StaleUseReportOnlyTools:       reportOnlyTools,
	}
	capabilityCoverage := ReadOnlyCapabilityCoverageForTools(manifest)
	surface.CapabilityRegistry = &capabilityCoverage
	if exportErr != nil {
		surface.ExportError = exportErr.Error()
	}
	surface.DriftFindingCount = toolSurfaceDriftFindingCount(surface)
	return surface
}

func auditSchemaDefinitionNames(defs []ToolDefinition) ([]string, []string) {
	names := make([]string, 0, len(defs))
	invalid := make([]string, 0)
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
		if !json.Valid([]byte(def.ParametersJSON)) {
			invalid = append(invalid, name)
		}
	}
	slices.Sort(invalid)
	return names, invalid
}

func auditExportedToolNames(export ToolSchemaExport) []string {
	names := make([]string, 0, len(export.Tools))
	for _, tool := range export.Tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func auditStaleUseBlockerCoverage(export ToolSchemaExport) ([]harnessAuditStaleUseCoverage, []string, []string) {
	exported := make(map[string]ExportedToolSchema, len(export.Tools))
	for _, tool := range export.Tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			exported[name] = tool
		}
	}
	var coverage []harnessAuditStaleUseCoverage
	var missing []string
	var reportOnly []string
	for _, expectation := range staleUseCoverageExpectations() {
		tool, ok := exported[expectation.Tool]
		row := expectation
		if ok && slices.Contains(tool.Policy.ErrorClasses, "stale_context") {
			row.SchemaErrorClass = true
		}
		if !ok || !row.SchemaErrorClass || (!row.RuntimeBlocker && !row.ReportOnly && !row.LiveFallbackOnStale) {
			missing = append(missing, row.Tool)
		}
		if row.ReportOnly {
			reportOnly = append(reportOnly, row.Tool)
		}
		coverage = append(coverage, row)
	}
	slices.Sort(missing)
	slices.Sort(reportOnly)
	return coverage, missing, reportOnly
}

func staleUseCoverageExpectations() []harnessAuditStaleUseCoverage {
	return []harnessAuditStaleUseCoverage{
		{Tool: "context_status", State: "reported", DefaultPolicy: string(comprehension.FreshnessReportOnly), ReportOnly: true},
		{Tool: "context_query", State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: "context_zoom", State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: "context_pack", State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: "context_impact", State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: "discovery_query", State: "reported", DefaultPolicy: string(comprehension.FreshnessReadThrough), LiveFallbackOnStale: true},
	}
}

func toolSurfaceDriftFindingCount(surface harnessAuditToolSurface) int {
	capabilityUnsafeCount := 0
	if surface.CapabilityRegistry != nil {
		capabilityUnsafeCount = surface.CapabilityRegistry.UnsafeMappedAdapterCount
	}
	return len(surface.DuplicateManifestTools) +
		len(surface.ManifestWithoutSchema) +
		len(surface.SchemaWithoutManifest) +
		len(surface.ManifestWithoutExportedSchema) +
		len(surface.ExportedSchemaWithoutManifest) +
		len(surface.SchemaWithoutExportedSchema) +
		len(surface.ExportedSchemaWithoutSchema) +
		len(surface.InvalidSchemas) +
		len(surface.SuggestionMissingTools) +
		len(surface.SuggestionWithoutManifest) +
		len(surface.StaleUseBlockerMissingTools) +
		capabilityUnsafeCount +
		boolToAuditCount(surface.ExportError != "")
}

func boolToAuditCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (m *Manager) auditLanguageTooling() []harnessAuditLanguage {
	manifest := auditSet(ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	}))
	defs := BuildToolDefinitions(nil)
	schemas := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		schemas[def.Name] = struct{}{}
	}
	active := make(map[string]bool)
	for _, lang := range m.ActiveLanguages() {
		active[lang.Name] = true
	}

	out := make([]harnessAuditLanguage, 0, len(LanguageRegistry()))
	for _, lang := range LanguageRegistry() {
		row := harnessAuditLanguage{
			Language:   lang.Name,
			Active:     active[lang.Name],
			Extensions: append([]string(nil), lang.Extensions...),
		}
		for _, cap := range harnessAuditStandardCapabilities {
			tool := lang.Tool(cap)
			item := harnessAuditLanguageTool{
				Capability: string(cap),
				Tool:       tool,
			}
			if tool == "" {
				row.Gaps = append(row.Gaps, "missing tool for capability "+string(cap))
			} else {
				_, item.InManifest = manifest[tool]
				_, item.HasSchema = schemas[tool]
				if !item.InManifest {
					row.Gaps = append(row.Gaps, tool+" missing from manifest")
				}
				if !item.HasSchema {
					row.Gaps = append(row.Gaps, tool+" missing schema")
				}
			}
			row.Capabilities = append(row.Capabilities, item)
		}
		out = append(out, row)
	}
	return out
}

func (m *Manager) auditBackendAvailability(languages []harnessAuditLanguage) []harnessAuditBackend {
	active := make(map[string]bool)
	for _, lang := range languages {
		active[lang.Language] = lang.Active
	}
	backends := []harnessAuditBackend{
		{
			Name:     "content_search",
			Binaries: contentSearchBackendCandidates(),
			Tools:    []string{"search_content"},
			Impact:   "search_content needs ripgrep or GNU grep; without it, generic text search is unavailable.",
			Active:   true,
		},
		{
			Name:     "git",
			Binaries: []string{"git"},
			Tools:    []string{"git_status", "git_diff", "history_range", "history_symbol", "blame_range"},
			Impact:   "history and blame tools require git.",
			Active:   true,
		},
		{
			Name:     "ast_grep",
			Binaries: []string{"ast-grep", "sg"},
			Tools:    []string{"structural_search"},
			Impact:   "structural_search is unavailable without ast-grep.",
			Active:   true,
		},
		{
			Name:     "universal_ctags",
			Binaries: []string{"ctags"},
			Tools:    []string{"ctags_symbols"},
			Impact:   "ctags_symbols is the fallback symbol index for uncovered file types.",
			Active:   true,
		},
		{
			Name:     "semgrep",
			Binaries: []string{"semgrep"},
			Tools:    []string{"semgrep_scan"},
			Impact:   "security_review discovery packets degrade when semgrep is absent.",
			Active:   true,
		},
		{
			Name:     "joern",
			Binaries: []string{"joern-scan"},
			Tools:    []string{"joern_scan"},
			Impact:   "heavy code-property-graph scanning is unavailable without joern-scan.",
			Active:   true,
		},
		{
			Name:     "gopls",
			Binaries: []string{"gopls"},
			Tools:    []string{"go_definition", "go_references", "go_calls_from", "go_calls_into", "go_implementations"},
			Impact:   "Go tools still have syntax-index fallbacks, but implementation queries and highest precision require gopls.",
			Active:   active["go"],
		},
		{
			Name:     "clangd",
			Binaries: []string{"clangd"},
			Tools:    []string{"cpp_definition", "cpp_references", "cpp_calls_from", "cpp_calls_into"},
			Impact:   "C/C++ precision improves when clangd is available.",
			Active:   active["cpp"],
		},
		{
			Name:     "python_lsp",
			Binaries: []string{"pyright-langserver", "pylsp"},
			Tools:    []string{"python_definition", "python_references", "python_diagnostics"},
			Impact:   "Python location and diagnostics precision improves with pyright or pylsp.",
			Active:   active["python"],
		},
		{
			Name:     "typescript_language_server",
			Binaries: []string{"typescript-language-server"},
			Tools:    []string{"ts_definition", "ts_references", "ts_diagnostics"},
			Impact:   "TypeScript/JavaScript precision improves with typescript-language-server.",
			Active:   active["typescript"],
		},
		{
			Name:     "rust_analyzer",
			Binaries: []string{"rust-analyzer"},
			Tools:    []string{"rust_definition", "rust_references", "rust_diagnostics"},
			Impact:   "Rust precision improves with rust-analyzer; fallbacks use the syntactic index or rg.",
			Active:   active["rust"],
		},
		{
			Name:     "jdtls",
			Binaries: []string{"jdtls"},
			Tools:    []string{"java_definition", "java_references", "java_diagnostics"},
			Impact:   "Java precision improves with Eclipse JDT-LS.",
			Active:   active["java"],
		},
		{
			Name:     "madge",
			Binaries: []string{"madge"},
			Tools:    []string{"dependency_graph"},
			Impact:   "TypeScript dependency_graph falls back to local import extraction when madge is absent.",
			Active:   active["typescript"],
		},
		{
			Name:     "pydeps",
			Binaries: []string{"pydeps"},
			Tools:    []string{"dependency_graph"},
			Impact:   "Python dependency_graph falls back to local import extraction when pydeps is absent.",
			Active:   active["python"],
		},
	}
	for i := range backends {
		if backends[i].Name == "content_search" {
			if backend, err := resolveContentSearchBackend(); err == nil {
				backends[i].Available = true
				backends[i].Path = backend.bin
			}
			continue
		}
		if backends[i].Name == "universal_ctags" {
			if path := auditUniversalCtagsPath(); path != "" {
				backends[i].Available = true
				backends[i].Path = path
			}
			continue
		}
		if path := firstAvailableBinary(backends[i].Binaries...); path != "" {
			backends[i].Available = true
			backends[i].Path = path
		}
	}
	backends = append(backends, m.auditContextGraphBackend())
	return backends
}

func (m *Manager) auditContextGraphBackend() harnessAuditBackend {
	backend := harnessAuditBackend{
		Name:     "context_graph_sqlite",
		Tools:    []string{"context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "context_trace_pack", "discovery_query"},
		Impact:   "Context graph tools use the embedded SQLite graph when present; discovery_query falls back to live probes when the graph is stale, collector-version-mismatched, missing, or incompatible.",
		Active:   true,
		Binaries: []string{"embedded:modernc.org/sqlite"},
	}
	status, err := comprehension.Status(m.cfg.Root)
	if err != nil {
		backend.Impact = backend.Impact + " context_status currently errors: " + err.Error()
		return backend
	}
	for _, storage := range status.StorageBackends {
		if storage.Active {
			backend.Path = storage.Path
			backend.Available = status.Indexed && storage.Compatible
			backend.Impact = fmt.Sprintf("%s Active backend %s is %s.", backend.Impact, storage.Kind, storage.Status)
			return backend
		}
	}
	backend.Available = false
	if !status.Indexed {
		backend.Impact = backend.Impact + " No context index is currently present."
	}
	return backend
}

func auditUniversalCtagsPath() string {
	path := firstAvailableBinary("ctags")
	if path == "" {
		return ""
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	if !strings.Contains(strings.ToLower(string(output)), "universal ctags") {
		return ""
	}
	return path
}

func (m *Manager) auditLogBehavior(req ToolRequest) (*harnessAuditLogBehavior, *autonomy.EventAuditReport, []harnessAuditFinding, []harnessAuditQuestion, error) {
	limit := harnessAuditLogLimit(req.Limit)
	paths, sources, warnings, err := m.auditLogPaths(req, limit)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	reports := make([]eval.PerfReport, 0, len(paths))
	for _, path := range paths {
		report, err := eval.AnalyzeLog(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", m.displayPath(path), err))
			continue
		}
		report.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		report.LogPath = m.displayPath(path)
		reports = append(reports, report)
	}
	incompleteSessions, inProgressSessions, lifecycleWarnings := m.auditIncompleteImplementSessions(paths)
	warnings = append(warnings, lifecycleWarnings...)
	contractCoverage, contractWarnings := m.auditContractEventCoverage(paths)
	warnings = append(warnings, contractWarnings...)
	malformedOutput, malformedWarnings := auditMalformedOutputStages(paths)
	for _, warning := range malformedWarnings {
		warnings = append(warnings, fmt.Sprintf("malformed output stages: %s", warning))
	}
	aggregate := eval.Aggregate(reports)
	behavior := &harnessAuditLogBehavior{
		LogLimit:           limit,
		LogsConsidered:     len(paths),
		LogsScanned:        len(reports),
		Sources:            sources,
		Warnings:           warnings,
		Aggregate:          aggregate,
		TopPatterns:        firstAuditPatterns(aggregate.PatternHitRate, 8),
		TopTools:           firstAuditTools(aggregate.ToolMix, 10),
		WorstOffenders:     aggregate.WorstOffenders,
		MalformedOutput:    malformedOutput,
		LaneHistory:        buildHarnessAuditLaneHistory(m.cfg.Root, 8),
		InProgressSessions: inProgressSessions,
		IncompleteSessions: incompleteSessions,
	}
	findings := logBehaviorFindings(aggregate, incompleteSessions)
	findings = append(findings, contractCoverageWarningFindings(contractWarnings)...)
	return behavior, contractCoverage, findings, logBehaviorQuestions(aggregate), nil
}

var malformedOutputAuditRoles = map[string]struct{}{
	"tool_request_normalizer":             {},
	"malformed_output_detected":           {},
	"artifact_distillation_started":       {},
	"artifact_distillation_result":        {},
	"edit_intent_validated":               {},
	"patch_compile_started":               {},
	"patch_compile_result":                {},
	"executable_artifact_validated":       {},
	"malformed_output_recovery_exhausted": {},
}

func auditMalformedOutputStages(paths []string) (*harnessAuditMalformedOutput, []string) {
	out := &harnessAuditMalformedOutput{
		EventCounts:            map[string]int{},
		ValidationResultCounts: map[string]int{},
		FailureClassCounts:     map[string]int{},
		CompileOutcomeCounts:   map[string]int{},
		RecoveryOutcomeCounts:  map[string]int{},
		SchemaVersionCounts:    map[string]int{},
	}
	var warnings []string
	logsHit := map[string]struct{}{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			role, content, ok := auditRunEventRoleAndContent([]byte(line))
			if !ok {
				continue
			}
			if _, tracked := malformedOutputAuditRoles[role]; !tracked {
				continue
			}
			out.EventCounts[role]++
			logsHit[path] = struct{}{}
			auditMalformedOutputContent(out, role, content)
		}
	}
	out.LogsHit = len(logsHit)
	if out.LogsHit == 0 {
		return nil, warnings
	}
	pruneEmptyAuditCounts(out)
	return out, warnings
}

func auditRunEventRoleAndContent(line []byte) (string, string, bool) {
	var row map[string]any
	if err := json.Unmarshal(line, &row); err != nil {
		return "", "", false
	}
	role := auditString(row["role"])
	content := auditString(row["content"])
	if role == "" {
		if data, ok := row["data"].(map[string]any); ok {
			role = auditString(data["role"])
			content = auditString(data["content"])
		}
	}
	if role == "" {
		return "", "", false
	}
	return role, content, true
}

func auditMalformedOutputContent(out *harnessAuditMalformedOutput, role string, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return
	}
	validationKeys := []string{"validation_result"}
	if role == "edit_intent_validated" {
		validationKeys = append(validationKeys, "result")
	}
	for _, key := range validationKeys {
		if value := auditString(payload[key]); value != "" {
			out.ValidationResultCounts[value]++
			break
		}
	}
	for _, key := range []string{"failure_class", "parser_failure_class", "error_code"} {
		if value := auditString(payload[key]); value != "" {
			out.FailureClassCounts[value]++
			break
		}
	}
	if role == "patch_compile_started" || role == "patch_compile_result" {
		if value := auditString(payload["compile_outcome"]); value != "" {
			out.CompileOutcomeCounts[value]++
		}
	}
	if role == "malformed_output_recovery_exhausted" {
		if value := auditString(payload["recovery_outcome"]); value != "" {
			out.RecoveryOutcomeCounts[value]++
		}
	}
	if value := auditString(payload["schema_version"]); value != "" {
		out.SchemaVersionCounts[value]++
	}
}

func auditString(value any) string {
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

func pruneEmptyAuditCounts(out *harnessAuditMalformedOutput) {
	if len(out.EventCounts) == 0 {
		out.EventCounts = nil
	}
	if len(out.ValidationResultCounts) == 0 {
		out.ValidationResultCounts = nil
	}
	if len(out.FailureClassCounts) == 0 {
		out.FailureClassCounts = nil
	}
	if len(out.CompileOutcomeCounts) == 0 {
		out.CompileOutcomeCounts = nil
	}
	if len(out.RecoveryOutcomeCounts) == 0 {
		out.RecoveryOutcomeCounts = nil
	}
	if len(out.SchemaVersionCounts) == 0 {
		out.SchemaVersionCounts = nil
	}
}

func buildHarnessAuditLaneHistory(root string, recentLimit int) *harnessAuditLaneHistory {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	out := &harnessAuditLaneHistory{
		Path:         ".idiom/lane-history.jsonl",
		ByMode:       map[string]int{},
		ByFinalState: map[string]int{},
		ByExitCode:   map[string]int{},
	}
	path := filepath.Join(root, ".idiom", "lane-history.jsonl")
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			out.Warnings = append(out.Warnings, fmt.Sprintf("read lane history: %v", err))
		}
		return out
	}
	out.Exists = true
	var recent []harnessAuditLaneHistoryRun
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var run harnessAuditLaneHistoryRun
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			out.Warnings = append(out.Warnings, "skipped malformed lane-history row")
			continue
		}
		out.Runs++
		if run.Mode != "" {
			out.ByMode[run.Mode]++
		}
		if run.FinalState != "" {
			out.ByFinalState[run.FinalState]++
		}
		out.ByExitCode[fmt.Sprintf("%d", run.ExitCode)]++
		out.TotalModelCalls += run.ModelCalls
		out.TotalToolCalls += run.ToolCalls
		out.TotalTokens += run.TotalTokens
		out.TotalDurationMS += run.DurationMS
		run.Brief = harnessAuditDisplayPath(root, run.Brief)
		recent = append(recent, run)
	}
	if recentLimit <= 0 {
		recentLimit = 8
	}
	if len(recent) > recentLimit {
		recent = recent[len(recent)-recentLimit:]
	}
	out.Recent = recent
	return out
}

func harnessAuditDisplayPath(root, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	if rel, ok := harnessAuditRelativePath(root, path); ok {
		return rel
	}
	realRoot, rootErr := filepath.EvalSymlinks(root)
	realPath, pathErr := filepath.EvalSymlinks(path)
	if rootErr == nil && pathErr == nil {
		if rel, ok := harnessAuditRelativePath(realRoot, realPath); ok {
			return rel
		}
	}
	return filepath.ToSlash(path)
}

func harnessAuditRelativePath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (m *Manager) auditContractEventCoverage(paths []string) (*autonomy.EventAuditReport, []string) {
	var warnings []string
	var events []autonomy.Event
	for _, path := range paths {
		pathEvents, pathWarnings := readHarnessAuditAutonomyEvents(path)
		for _, warning := range pathWarnings {
			warnings = append(warnings, fmt.Sprintf("%s contract coverage: %s", m.displayPath(path), warning))
		}
		events = append(events, pathEvents...)
	}
	if len(events) == 0 {
		return nil, warnings
	}
	events = dedupeAndSortHarnessAuditAutonomyEvents(events)
	report := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	return &report, warnings
}

func dedupeAndSortHarnessAuditAutonomyEvents(events []autonomy.Event) []autonomy.Event {
	if len(events) < 2 {
		return events
	}
	out := make([]autonomy.Event, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		eventID := strings.TrimSpace(event.EventID)
		if eventID != "" {
			if _, ok := seen[eventID]; ok {
				continue
			}
			seen[eventID] = struct{}{}
		}
		out = append(out, event)
	}
	slices.SortStableFunc(out, func(a, b autonomy.Event) int {
		if !a.OccurredAt.Equal(b.OccurredAt) {
			if a.OccurredAt.IsZero() {
				return 1
			}
			if b.OccurredAt.IsZero() {
				return -1
			}
			if a.OccurredAt.Before(b.OccurredAt) {
				return -1
			}
			return 1
		}
		if cmp := strings.Compare(a.ContractID, b.ContractID); cmp != 0 {
			return cmp
		}
		if a.Sequence < b.Sequence {
			return -1
		}
		if a.Sequence > b.Sequence {
			return 1
		}
		if cmp := strings.Compare(string(a.Type), string(b.Type)); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.EventID, b.EventID)
	})
	return out
}

type harnessAuditLogFacts struct {
	Command                 string
	WorkspaceRoot           string
	SessionID               string
	DebugLogPath            string
	PID                     int
	HasProcessStart         bool
	HasProcessExit          bool
	HasModelSession         bool
	HasModelSessionCloseout bool
	HasSessionSummary       bool
	IsImplementConversation bool
}

func (m *Manager) auditIncompleteImplementSessions(paths []string) ([]harnessAuditIncompleteSession, []harnessAuditInProgressSession, []string) {
	var incomplete []harnessAuditIncompleteSession
	var inProgress []harnessAuditInProgressSession
	var warnings []string
	type auditedLog struct {
		path        string
		displayPath string
		facts       harnessAuditLogFacts
	}
	logs := make([]auditedLog, 0, len(paths))
	activeConversationLogs := make(map[string]harnessAuditInProgressSession)
	telemetryByConversationLog := make(map[string]auditedLog)

	for _, path := range paths {
		facts, err := readHarnessAuditLogFacts(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", m.displayPath(path), err))
			continue
		}
		displayPath := m.displayPath(path)
		log := auditedLog{path: path, displayPath: displayPath, facts: facts}
		logs = append(logs, log)
		conversationPath := ""
		if facts.HasModelSession {
			conversationPath = m.auditConversationPathFromTelemetry(facts)
			if conversationPath != "" {
				if _, ok := telemetryByConversationLog[filepath.Clean(conversationPath)]; !ok {
					telemetryByConversationLog[filepath.Clean(conversationPath)] = log
				}
				if displayConversation := m.displayPath(conversationPath); displayConversation != "" {
					if _, ok := telemetryByConversationLog[displayConversation]; !ok {
						telemetryByConversationLog[displayConversation] = log
					}
				}
			}
		}
		if facts.HasProcessStart && facts.HasModelSession && !facts.HasProcessExit && processIDAppearsRunning(facts.PID) {
			if conversationPath != "" {
				session := harnessAuditInProgressSession{
					TelemetryLog:    displayPath,
					ConversationLog: m.displayPath(conversationPath),
					SessionID:       strings.TrimSpace(facts.SessionID),
					Command:         strings.TrimSpace(facts.Command),
					PID:             facts.PID,
				}
				activeConversationLogs[filepath.Clean(conversationPath)] = session
				activeConversationLogs[session.ConversationLog] = session
			}
		}
	}

	seenIncomplete := make(map[string]struct{})
	seenInProgress := make(map[string]struct{})
	appendInProgress := func(session harnessAuditInProgressSession) {
		key := session.TelemetryLog + "|" + session.ConversationLog
		if _, ok := seenInProgress[key]; ok {
			return
		}
		seenInProgress[key] = struct{}{}
		inProgress = append(inProgress, session)
	}
	lookupTelemetryForConversation := func(conversationPath string, conversationFacts harnessAuditLogFacts) (auditedLog, bool) {
		prefix := harnessAuditSessionTimestampPrefix(conversationFacts.SessionID)
		if prefix == "" {
			prefix = harnessAuditSessionTimestampPrefix(strings.TrimSuffix(filepath.Base(conversationPath), filepath.Ext(conversationPath)))
		}
		if prefix == "" {
			return auditedLog{}, false
		}
		prefixes := []string{prefix}
		if len(prefix) > len("20060102-1504") {
			prefixes = append(prefixes, prefix[:len("20060102-1504")])
		}
		prefixes = util.UniqueNonEmpty(prefixes)
		displayConversationPath := m.displayPath(conversationPath)
		var candidates []string
		for _, base := range util.UniqueNonEmpty([]string{m.cfg.CWD, m.cfg.Root}) {
			for _, dir := range []string{
				filepath.Join(base, "logs", "telemetry"),
				filepath.Join(base, ".idiom", "logs", "telemetry"),
			} {
				for _, candidatePrefix := range prefixes {
					matches, err := filepath.Glob(filepath.Join(dir, candidatePrefix+"*.jsonl"))
					if err != nil {
						warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: telemetry lookup failed: %v", displayConversationPath, err))
						continue
					}
					candidates = append(candidates, matches...)
				}
			}
		}
		candidates = util.UniqueNonEmpty(candidates)
		sortAuditLogPaths(candidates)
		cleanConversationPath := filepath.Clean(conversationPath)
		for _, candidate := range candidates {
			candidateFacts, err := readHarnessAuditLogFacts(candidate)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", m.displayPath(candidate), err))
				continue
			}
			if !candidateFacts.HasModelSession {
				continue
			}
			candidateConversationPath := m.auditConversationPathFromTelemetry(candidateFacts)
			sameConversation := candidateConversationPath != "" &&
				(filepath.Clean(candidateConversationPath) == cleanConversationPath ||
					m.displayPath(candidateConversationPath) == displayConversationPath)
			sameSession := strings.TrimSpace(conversationFacts.SessionID) != "" &&
				strings.TrimSpace(conversationFacts.SessionID) == strings.TrimSpace(candidateFacts.SessionID)
			if !sameConversation && !sameSession {
				continue
			}
			return auditedLog{path: candidate, displayPath: m.displayPath(candidate), facts: candidateFacts}, true
		}
		return auditedLog{}, false
	}

	for _, log := range logs {
		facts := log.facts
		displayPath := log.displayPath
		if facts.HasProcessStart && strings.EqualFold(strings.TrimSpace(facts.Command), "implement") {
			if !facts.HasProcessExit && processIDAppearsRunning(facts.PID) {
				session := harnessAuditInProgressSession{
					TelemetryLog: displayPath,
					SessionID:    strings.TrimSpace(facts.SessionID),
					Command:      "implement",
					PID:          facts.PID,
				}
				if facts.HasModelSession {
					if conversationPath := m.auditConversationPathFromTelemetry(facts); conversationPath != "" {
						session.ConversationLog = m.displayPath(conversationPath)
					}
				}
				appendInProgress(session)
				continue
			}
			session := harnessAuditIncompleteSession{
				TelemetryLog: displayPath,
				SessionID:    strings.TrimSpace(facts.SessionID),
				Command:      "implement",
			}
			if !facts.HasProcessExit {
				session.Missing = append(session.Missing, "process_exit")
			}
			if facts.HasModelSession {
				if conversationPath := m.auditConversationPathFromTelemetry(facts); conversationPath != "" {
					session.ConversationLog = m.displayPath(conversationPath)
					conversationFacts, err := readHarnessAuditLogFacts(conversationPath)
					if err != nil {
						warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", session.ConversationLog, err))
					} else if !conversationFacts.HasSessionSummary && !facts.HasModelSessionCloseout {
						session.Missing = append(session.Missing, "session_summary")
					}
				} else if strings.TrimSpace(facts.DebugLogPath) != "" {
					warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: debug log path %q is outside the audited workspace root or does not exist", displayPath, facts.DebugLogPath))
				} else if !facts.HasModelSessionCloseout {
					session.Missing = append(session.Missing, "session_summary")
				}
			}
			if len(session.Missing) > 0 {
				key := session.TelemetryLog + "|" + session.ConversationLog
				if _, ok := seenIncomplete[key]; !ok {
					seenIncomplete[key] = struct{}{}
					incomplete = append(incomplete, session)
				}
			}
			continue
		}
		if facts.IsImplementConversation && !facts.HasSessionSummary {
			activeSession, ok := activeConversationLogs[filepath.Clean(log.path)]
			if !ok {
				activeSession, ok = activeConversationLogs[displayPath]
			}
			if ok {
				if strings.TrimSpace(activeSession.Command) == "" {
					activeSession.Command = "tui"
				}
				appendInProgress(activeSession)
				continue
			}
			telemetryLog, ok := telemetryByConversationLog[filepath.Clean(log.path)]
			if !ok {
				telemetryLog, ok = telemetryByConversationLog[displayPath]
			}
			if !ok {
				telemetryLog, ok = lookupTelemetryForConversation(log.path, facts)
			}
			if ok {
				telemetryFacts := telemetryLog.facts
				if telemetryFacts.HasProcessStart && !telemetryFacts.HasProcessExit && processIDAppearsRunning(telemetryFacts.PID) {
					appendInProgress(harnessAuditInProgressSession{
						TelemetryLog:    telemetryLog.displayPath,
						ConversationLog: displayPath,
						SessionID:       util.FirstNonEmpty(strings.TrimSpace(facts.SessionID), strings.TrimSpace(telemetryFacts.SessionID)),
						Command:         util.FirstNonEmpty(strings.TrimSpace(telemetryFacts.Command), "tui"),
						PID:             telemetryFacts.PID,
					})
					continue
				}
				missing := make([]string, 0, 2)
				if telemetryFacts.HasProcessStart && !telemetryFacts.HasProcessExit {
					missing = append(missing, "process_exit")
				}
				if !telemetryFacts.HasModelSessionCloseout {
					missing = append(missing, "session_summary")
				}
				if len(missing) == 0 {
					continue
				}
				session := harnessAuditIncompleteSession{
					TelemetryLog:    telemetryLog.displayPath,
					ConversationLog: displayPath,
					SessionID:       util.FirstNonEmpty(strings.TrimSpace(facts.SessionID), strings.TrimSpace(telemetryFacts.SessionID)),
					Command:         util.FirstNonEmpty(strings.TrimSpace(telemetryFacts.Command), "tui"),
					Missing:         missing,
				}
				key := session.TelemetryLog + "|" + session.ConversationLog
				if _, ok := seenIncomplete[key]; !ok {
					seenIncomplete[key] = struct{}{}
					incomplete = append(incomplete, session)
				}
				continue
			}
			session := harnessAuditIncompleteSession{
				ConversationLog: displayPath,
				SessionID:       strings.TrimSpace(facts.SessionID),
				Command:         "implement",
				Missing:         []string{"session_summary"},
			}
			key := session.TelemetryLog + "|" + session.ConversationLog
			if _, ok := seenIncomplete[key]; !ok {
				seenIncomplete[key] = struct{}{}
				incomplete = append(incomplete, session)
			}
		}
	}

	sort.Slice(incomplete, func(i, j int) bool {
		left := firstNonEmptyAuditSessionPath(incomplete[i])
		right := firstNonEmptyAuditSessionPath(incomplete[j])
		return left < right
	})
	sort.Slice(inProgress, func(i, j int) bool {
		left := firstNonEmptyAuditInProgressSessionPath(inProgress[i])
		right := firstNonEmptyAuditInProgressSessionPath(inProgress[j])
		return left < right
	})
	return incomplete, inProgress, warnings
}

func (m *Manager) auditConversationPathFromTelemetry(facts harnessAuditLogFacts) string {
	path := strings.TrimSpace(facts.DebugLogPath)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.cfg.CWD, path)
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	if m.pathWithinWorkspace(path) || auditPathWithinRoot(path, facts.WorkspaceRoot) {
		return path
	}
	return ""
}

func harnessAuditSessionTimestampPrefix(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if idx := strings.Index(sessionID, "."); idx > 0 {
		sessionID = sessionID[:idx]
	}
	if len(sessionID) != len("20060102-150405") || sessionID[8] != '-' {
		return ""
	}
	for i, r := range sessionID {
		if i == 8 {
			continue
		}
		if r < '0' || r > '9' {
			return ""
		}
	}
	return sessionID
}

func readHarnessAuditLogFacts(path string) (harnessAuditLogFacts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return harnessAuditLogFacts{}, err
	}
	var facts harnessAuditLogFacts
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		eventType := auditStringField(record, "type")
		role := auditStringField(record, "role")
		switch eventType {
		case "process_start":
			facts.HasProcessStart = true
			facts.Command = util.FirstNonEmpty(facts.Command, auditStringField(record, "command"))
			facts.WorkspaceRoot = util.FirstNonEmpty(facts.WorkspaceRoot, auditStringField(record, "workspace_root"))
			facts.PID = auditIntField(record, "pid")
		case "session_start":
			facts.Command = util.FirstNonEmpty(facts.Command, auditStringField(record, "command"))
			facts.WorkspaceRoot = util.FirstNonEmpty(facts.WorkspaceRoot, auditStringField(record, "workspace_root"))
		case "process_exit":
			facts.HasProcessExit = true
		case "model_session":
			facts.HasModelSession = true
			facts.SessionID = util.FirstNonEmpty(facts.SessionID, auditStringField(record, "session_id"))
			facts.DebugLogPath = util.FirstNonEmpty(facts.DebugLogPath, auditStringField(record, "debug_log_path"))
		case "session_summary":
			facts.HasSessionSummary = true
		case "model_session_closeout", "implement_session_closeout":
			facts.HasModelSessionCloseout = true
			facts.SessionID = util.FirstNonEmpty(facts.SessionID, auditStringField(record, "session_id"))
			facts.DebugLogPath = util.FirstNonEmpty(facts.DebugLogPath, auditStringField(record, "debug_log_path"))
		}
		if role == "control_rails" || eventType == "control_rails" {
			if auditControlRailsIndicateImplementation(record) {
				facts.IsImplementConversation = true
			}
		}
		if eventType == "session_start" && strings.EqualFold(auditStringField(record, "command"), "implement") {
			facts.IsImplementConversation = true
		}
	}
	if facts.SessionID == "" {
		facts.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return facts, nil
}

func readHarnessAuditAutonomyEvents(path string) ([]autonomy.Event, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var events []autonomy.Event
	var warnings []string
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lineEvents, lineWarnings := decodeHarnessAuditAutonomyEvents([]byte(line))
		for _, warning := range lineWarnings {
			warnings = append(warnings, fmt.Sprintf("line %d: %s", lineNumber+1, warning))
		}
		events = append(events, lineEvents...)
	}
	return events, warnings
}

func decodeHarnessAuditAutonomyEvents(line []byte) ([]autonomy.Event, []string) {
	var events []autonomy.Event
	var warnings []string
	if auditRawAutonomyEvent(line) {
		event, err := unmarshalHarnessAuditAutonomyEvent(line)
		if err != nil {
			warnings = append(warnings, err.Error())
		} else {
			events = append(events, event)
		}
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil {
		return events, warnings
	}
	for _, key := range []string{"autonomy_event", "contract_event", "event"} {
		raw, ok := record[key]
		if !ok || !auditRawAutonomyEvent(raw) {
			continue
		}
		event, err := unmarshalHarnessAuditAutonomyEvent(raw)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		events = append(events, event)
	}
	if auditStringFromRaw(record["role"]) == "autonomy_event" {
		var content string
		if err := json.Unmarshal(record["content"], &content); err == nil && auditRawAutonomyEvent([]byte(content)) {
			event, err := unmarshalHarnessAuditAutonomyEvent([]byte(content))
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("content: %v", err))
			} else {
				events = append(events, event)
			}
		}
	}
	return events, warnings
}

func auditRawAutonomyEvent(raw []byte) bool {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.SchemaVersion == autonomy.EventSchemaVersion
}

func auditStringFromRaw(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func unmarshalHarnessAuditAutonomyEvent(raw []byte) (autonomy.Event, error) {
	var event autonomy.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return autonomy.Event{}, err
	}
	event.Normalize()
	if err := event.Validate(); err != nil {
		return autonomy.Event{}, err
	}
	return event, nil
}

func processIDAppearsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func auditControlRailsIndicateImplementation(record map[string]any) bool {
	if strings.Contains(strings.ToLower(auditStringField(record, "content")), "implementation_result") {
		return true
	}
	for _, key := range []string{"contract", "task_family", "lane", "permission_profile"} {
		value := strings.ToLower(auditStringField(record, key))
		if strings.Contains(value, "implementation") || strings.Contains(value, "implement") {
			return true
		}
	}
	return false
}

func auditPathWithinRoot(path string, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !strings.HasPrefix(rel, `..\`)
}

func auditStringField(record map[string]any, key string) string {
	if value, ok := record[key]; ok {
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func auditIntField(record map[string]any, key string) int {
	value, ok := record[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	}
	return 0
}

func firstNonEmptyAuditInProgressSessionPath(session harnessAuditInProgressSession) string {
	if strings.TrimSpace(session.TelemetryLog) != "" {
		return strings.TrimSpace(session.TelemetryLog)
	}
	return strings.TrimSpace(session.ConversationLog)
}

func firstNonEmptyAuditSessionPath(session harnessAuditIncompleteSession) string {
	if strings.TrimSpace(session.TelemetryLog) != "" {
		return strings.TrimSpace(session.TelemetryLog)
	}
	return strings.TrimSpace(session.ConversationLog)
}

func (m *Manager) auditLogPaths(req ToolRequest, limit int) ([]string, []harnessAuditLogSource, []string, error) {
	var warnings []string
	var sources []harnessAuditLogSource
	var paths []string
	defaultScope := false
	switch {
	case strings.TrimSpace(req.Glob) != "":
		pattern := strings.TrimSpace(req.Glob)
		matches, err := m.auditGlob(pattern)
		if err != nil {
			return nil, nil, nil, err
		}
		sources = append(sources, harnessAuditLogSource{Pattern: pattern, Matched: len(matches), Selected: len(matches)})
		paths = append(paths, matches...)
	case strings.TrimSpace(req.Path) != "":
		path := strings.TrimSpace(req.Path)
		if hasAuditGlobMagic(path) {
			matches, err := m.auditGlob(path)
			if err != nil {
				return nil, nil, nil, err
			}
			sources = append(sources, harnessAuditLogSource{Pattern: path, Matched: len(matches), Selected: len(matches)})
			paths = append(paths, matches...)
			break
		}
		target, err := m.resolvePath(path, req.Workdir)
		if err != nil {
			return nil, nil, nil, err
		}
		info, err := os.Stat(target)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("audit log path %q: %w", path, err)
		}
		if info.IsDir() {
			matches, err := auditJSONLFilesUnder(target)
			if err != nil {
				return nil, nil, nil, err
			}
			sources = append(sources, harnessAuditLogSource{Pattern: path, Matched: len(matches), Selected: len(matches)})
			paths = append(paths, matches...)
		} else {
			sources = append(sources, harnessAuditLogSource{Pattern: path, Matched: 1, Selected: 1})
			paths = append(paths, target)
		}
	default:
		defaultScope = true
		for _, pattern := range []string{
			".idiom/logs/conversations/*.jsonl",
			"logs/conversations/*.jsonl",
			".idiom/logs/e2e/*.jsonl",
			"logs/e2e/*.jsonl",
			"logs/telemetry/*.jsonl",
		} {
			matches, err := m.auditDefaultJSONLGlob(pattern)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			matches = auditUniqueExistingFiles(matches)
			sortAuditLogPaths(matches)
			source := harnessAuditLogSource{Pattern: pattern, Matched: len(matches), Selected: len(matches)}
			if len(matches) > limit {
				warnings = append(warnings, fmt.Sprintf("log scan capped at %d newest file(s) out of %d matched for %s", limit, len(matches), pattern))
				matches = matches[:limit]
				source.Selected = len(matches)
				source.Capped = true
			}
			sources = append(sources, source)
			paths = append(paths, matches...)
		}
	}
	paths = auditUniqueExistingFiles(paths)
	sortAuditLogPaths(paths)
	if len(paths) == 0 {
		warnings = append(warnings, "no JSONL behavior logs matched the audit scope")
		return paths, sources, warnings, nil
	}
	if len(paths) > limit && !defaultScope {
		warnings = append(warnings, fmt.Sprintf("log scan capped at %d newest file(s) out of %d matched", limit, len(paths)))
		paths = paths[:limit]
		if len(sources) > 0 {
			sources[len(sources)-1].Selected = len(paths)
			sources[len(sources)-1].Capped = true
		}
	}
	return paths, sources, warnings, nil
}

func (m *Manager) auditDefaultJSONLGlob(pattern string) ([]string, error) {
	const suffix = "/*.jsonl"
	if !strings.HasSuffix(strings.ToLower(pattern), suffix) {
		return m.auditGlob(pattern)
	}
	dirPattern := pattern[:len(pattern)-len(suffix)]
	if strings.TrimSpace(dirPattern) == "" {
		return m.auditGlob(pattern)
	}
	if hasAuditGlobMagic(dirPattern) {
		return m.auditGlob(pattern)
	}
	dir, err := m.resolvePath(dirPattern, "")
	if err != nil {
		return nil, fmt.Errorf("audit glob %q: %w", pattern, err)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit glob %q: %w", pattern, err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths, nil
}

func (m *Manager) auditGlob(pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	var absPattern string
	if filepath.IsAbs(pattern) {
		absPattern = filepath.Clean(pattern)
	} else {
		absPattern = filepath.Clean(filepath.Join(m.cfg.CWD, pattern))
	}
	matches, err := filepath.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("audit glob %q: %w", pattern, err)
	}
	for _, match := range matches {
		if err := m.ensureAuditPathInWorkspace(match); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func (m *Manager) ensureAuditPathInWorkspace(path string) error {
	rel, err := filepath.Rel(m.cfg.Root, filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve audit path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("audit path %q escapes workspace root", path)
	}
	return nil
}

func auditJSONLFilesUnder(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && discovery.ShouldSkipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func harnessAuditLogLimit(limit int) int {
	if limit <= 0 {
		return harnessAuditDefaultLogLimit
	}
	if limit > harnessAuditMaxLogLimit {
		return harnessAuditMaxLogLimit
	}
	return limit
}

func hasAuditGlobMagic(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func auditUniqueExistingFiles(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if _, dup := seen[path]; dup {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func sortAuditLogPaths(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		infoI, errI := os.Stat(paths[i])
		infoJ, errJ := os.Stat(paths[j])
		if errI == nil && errJ == nil && !infoI.ModTime().Equal(infoJ.ModTime()) {
			return infoI.ModTime().After(infoJ.ModTime())
		}
		return paths[i] < paths[j]
	})
}

func toolSurfaceFindings(surface harnessAuditToolSurface) []harnessAuditFinding {
	var findings []harnessAuditFinding
	if len(surface.ManifestWithoutSchema) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "manifest_without_schema",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d exported tool(s) have no function schema", len(surface.ManifestWithoutSchema)),
			Recommendation: "Add schemas or remove the tools from the exported manifest.",
			Tools:          surface.ManifestWithoutSchema,
		})
	}
	if len(surface.SchemaWithoutManifest) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "schema_without_manifest",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d schema tool(s) are not listed in the exported manifest", len(surface.SchemaWithoutManifest)),
			Recommendation: "Add them to the manifest or delete stale schemas.",
			Tools:          surface.SchemaWithoutManifest,
		})
	}
	if len(surface.ManifestWithoutExportedSchema) > 0 || len(surface.ExportedSchemaWithoutManifest) > 0 || len(surface.SchemaWithoutExportedSchema) > 0 || len(surface.ExportedSchemaWithoutSchema) > 0 || surface.ExportError != "" {
		tools := append(append([]string(nil), surface.ManifestWithoutExportedSchema...), surface.ExportedSchemaWithoutManifest...)
		tools = append(tools, surface.SchemaWithoutExportedSchema...)
		tools = append(tools, surface.ExportedSchemaWithoutSchema...)
		tools = util.UniqueNonEmpty(tools)
		evidence := fmt.Sprintf("%d exported-schema drift item(s) detected", len(tools))
		if surface.ExportError != "" {
			evidence = "schema export failed: " + surface.ExportError
		}
		findings = append(findings, harnessAuditFinding{
			ID:             "exported_schema_drift",
			Severity:       "high",
			Evidence:       evidence,
			Recommendation: "reason=exported_schema_drift source=manifest_schema_export",
			Tools:          tools,
		})
	}
	if len(surface.InvalidSchemas) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "invalid_tool_schema",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d tool schema(s) are not valid JSON", len(surface.InvalidSchemas)),
			Recommendation: "Fix the hand-authored JSON schema strings before advertising these tools.",
			Tools:          surface.InvalidSchemas,
		})
	}
	if len(surface.SuggestionMissingTools) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "unsupported_tool_suggestion_gap",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d manifest tool(s) are absent from typo suggestions", len(surface.SuggestionMissingTools)),
			Recommendation: "Generate typo suggestions from the manifest inventory.",
			Tools:          surface.SuggestionMissingTools,
		})
	}
	if len(surface.SuggestionWithoutManifest) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "stale_tool_suggestion",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d suggested tool(s) are absent from the manifest", len(surface.SuggestionWithoutManifest)),
			Recommendation: "reason=stale_tool_suggestion source=unsupported_tool_recovery",
			Tools:          surface.SuggestionWithoutManifest,
		})
	}
	if len(surface.StaleUseBlockerMissingTools) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "stale_use_blocker_gap",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d graph-backed tool(s) lack stale-context blocker/report coverage", len(surface.StaleUseBlockerMissingTools)),
			Recommendation: "reason=stale_use_blocker_gap source=tool_surface",
			Tools:          surface.StaleUseBlockerMissingTools,
		})
	}
	if surface.CapabilityRegistry != nil && surface.CapabilityRegistry.UnsafeMappedAdapterCount > 0 {
		tools := unsafeCapabilityAdapterTools(surface.CapabilityRegistry.UnsafeMappedAdapters)
		findings = append(findings, harnessAuditFinding{
			ID:             "unsafe_read_only_capability_mapping",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d read-only capability adapter mapping(s) have non-read-only schema side effects", surface.CapabilityRegistry.UnsafeMappedAdapterCount),
			Recommendation: "Defer mixed/unknown adapters from the read-only capability vocabulary or model them outside the read-only-only registry.",
			Tools:          tools,
		})
	}
	return findings
}

func unsafeCapabilityAdapterTools(adapters []ReadOnlyCapabilityUnsafeAdapterSpec) []string {
	tools := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		if tool := strings.TrimSpace(adapter.Tool); tool != "" {
			tools = append(tools, tool)
		}
	}
	return util.UniqueNonEmpty(tools)
}

func backendFindings(backends []harnessAuditBackend) []harnessAuditFinding {
	var findings []harnessAuditFinding
	for _, backend := range backends {
		if backend.Available || !backend.Active {
			continue
		}
		severity := "medium"
		if backend.Name == "content_search" || backend.Name == "git" {
			severity = "high"
		}
		findings = append(findings, harnessAuditFinding{
			ID:             "backend_unavailable_" + backend.Name,
			Severity:       severity,
			Evidence:       fmt.Sprintf("%s not found on PATH; checked %s", backend.Name, strings.Join(backend.Binaries, ", ")),
			Recommendation: backend.Impact,
			Tools:          backend.Tools,
		})
	}
	return findings
}

func logBehaviorFindings(aggregate eval.PerfAggregate, incompleteSessions []harnessAuditIncompleteSession) []harnessAuditFinding {
	var findings []harnessAuditFinding
	for _, pattern := range aggregate.PatternHitRate {
		if pattern.LogsHit == 0 {
			continue
		}
		severity := "low"
		if pattern.HitRate >= 0.5 || pattern.TotalCount >= 5 {
			severity = "medium"
		}
		findings = append(findings, harnessAuditFinding{
			ID:       "behavior_" + pattern.Name,
			Severity: severity,
			Evidence: fmt.Sprintf("%s appeared in %d log(s), %d total hit(s)", pattern.Name, pattern.LogsHit, pattern.TotalCount),
		})
	}
	if aggregate.Feedback != nil && aggregate.Feedback.Total > 0 {
		severity := "low"
		if aggregate.Feedback.HighSeverity > 0 {
			severity = "high"
		} else if aggregate.Feedback.MediumSeverity > 0 || aggregate.Feedback.Total >= 3 {
			severity = "medium"
		}
		findings = append(findings, harnessAuditFinding{
			ID:             "agent_harness_feedback_reported",
			Severity:       severity,
			Evidence:       fmt.Sprintf("%d feedback event(s), %d verified, top category %s, top component %s", aggregate.Feedback.Total, aggregate.Feedback.Verified, auditTopCount(aggregate.Feedback.ByCategory), auditTopCount(aggregate.Feedback.ByComponent)),
			Recommendation: "Triage structured feedback before adding scenario-specific prompt text; prefer generic tool, telemetry, or contract fixes.",
			Tools:          []string{"harness_feedback", "harness_audit"},
		})
	}
	if aggregate.LocalOps != nil {
		if aggregate.LocalOps.QueueDegraded > 0 || aggregate.LocalOps.ContextBudgetDegraded > 0 || aggregate.LocalOps.ClassifierDegraded > 0 || aggregate.LocalOps.DoctorDegradedProfiles > 0 || aggregate.LocalOps.DoctorBlockedProfiles > 0 {
			severity := "low"
			if aggregate.LocalOps.DoctorBlockedProfiles > 0 {
				severity = "high"
			} else if aggregate.LocalOps.QueueDegraded > 0 || aggregate.LocalOps.ContextBudgetDegraded > 0 {
				severity = "medium"
			}
			findings = append(findings, harnessAuditFinding{
				ID:             "local_ops_degraded_mode_reported",
				Severity:       severity,
				Evidence:       fmt.Sprintf("queue_degraded=%d context_budget_degraded=%d classifier_degraded=%d doctor_degraded=%d doctor_blocked=%d", aggregate.LocalOps.QueueDegraded, aggregate.LocalOps.ContextBudgetDegraded, aggregate.LocalOps.ClassifierDegraded, aggregate.LocalOps.DoctorDegradedProfiles, aggregate.LocalOps.DoctorBlockedProfiles),
				Recommendation: "Use doctor readiness and queue/status telemetry to decide whether to keep degraded local operation report-only or add profile gates.",
				Tools:          []string{"harness_audit", "idiom doctor", "idiom queue"},
			})
		}
	}
	if len(incompleteSessions) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "incomplete_implement_session_lifecycle",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d implementation-backed session(s) missing process/session lifecycle event(s)", len(incompleteSessions)),
			Recommendation: "Inspect termination reasons and keep lifecycle closeout generic: every implementation-backed run should end with process_exit telemetry and a session_summary conversation event.",
			Tools:          []string{"harness_audit"},
		})
	}
	return findings
}

func contractCoverageWarningFindings(warnings []string) []harnessAuditFinding {
	invalidEvents := 0
	for _, warning := range warnings {
		if strings.Contains(warning, "invalid autonomy event") {
			invalidEvents++
		}
	}
	if invalidEvents == 0 {
		return nil
	}
	return []harnessAuditFinding{{
		ID:             "contract_event_invalid",
		Severity:       "medium",
		Evidence:       fmt.Sprintf("%d invalid autonomy event(s) skipped during contract coverage audit", invalidEvents),
		Recommendation: "Fix the event emitter or migration path before using contract coverage as release evidence.",
		Tools:          []string{"harness_audit"},
	}}
}

func contractCoverageFindings(report *autonomy.EventAuditReport) []harnessAuditFinding {
	if report == nil {
		return nil
	}
	findings := make([]harnessAuditFinding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		findings = append(findings, harnessAuditFinding{
			ID:       finding.ID,
			Severity: finding.Severity,
			Evidence: finding.Evidence,
			Tools:    []string{"harness_audit"},
		})
	}
	return findings
}

func planningQualityFromFeedback(feedback *eval.FeedbackStats) *harnessAuditPlanningQuality {
	report := &harnessAuditPlanningQuality{
		PlanningCategories: make(map[string]int),
	}
	if feedback == nil {
		return report
	}
	report.FeedbackTotal = feedback.Total
	report.PlanningFeedback = feedback.PlanningQualityTotal
	report.VerifiedFeedback = feedback.Verified
	report.HighSeverity = feedback.HighSeverity
	for category, count := range feedback.ByPlanningCategory {
		report.PlanningCategories[category] = count
	}
	report.TopComponents = auditTopCounts(feedback.ByComponent, 6)
	report.RecommendedSurfaces = planningQualityRecommendedSurfaces(report)
	if len(report.PlanningCategories) == 0 {
		report.PlanningCategories = nil
	}
	return report
}

func planningQualityFindings(report *harnessAuditPlanningQuality) []harnessAuditFinding {
	if report == nil || report.PlanningFeedback == 0 {
		return nil
	}
	severity := "medium"
	if report.HighSeverity > 0 {
		severity = "high"
	}
	return []harnessAuditFinding{{
		ID:             "planning_quality_feedback_reported",
		Severity:       severity,
		Evidence:       fmt.Sprintf("%d planning-quality feedback event(s); top planning category %s", report.PlanningFeedback, auditTopCount(report.PlanningCategories)),
		Recommendation: "Triage planning-quality feedback into context impact ranking, packet schema, plan audit, budget, or replay-eval improvements before adding prompt text.",
		Tools:          []string{"harness_feedback", "harness_audit", "context_impact", "architect plan-audit"},
	}}
}

func planningQualityQuestions(report *harnessAuditPlanningQuality) []harnessAuditQuestion {
	if report == nil || report.PlanningFeedback == 0 {
		return nil
	}
	return []harnessAuditQuestion{{
		Area:      "planning_quality",
		Question:  "Which planning-quality feedback category should become the next deterministic packet, ranking, audit, or eval contract?",
		Evidence:  fmt.Sprintf("%d planning-quality feedback event(s); recommended surfaces: %s", report.PlanningFeedback, strings.Join(report.RecommendedSurfaces, ", ")),
		Candidate: "Prefer a typed context_impact, planning_v2, plan-audit, budget, or replay-eval improvement over another strategy prompt.",
	}}
}

func planningQualityRecommendedSurfaces(report *harnessAuditPlanningQuality) []string {
	if report == nil {
		return nil
	}
	var surfaces []string
	if report.PlanningCategories["packet_quality"] > 0 || report.PlanningCategories["planning_quality"] > 0 {
		surfaces = append(surfaces, "architect packet --schema planning_v2")
	}
	if report.PlanningCategories["impact_ranking"] > 0 || report.PlanningCategories["missing_affordance"] > 0 || report.PlanningCategories["bad_scope_rank"] > 0 || report.PlanningCategories["read_loop"] > 0 {
		surfaces = append(surfaces, "context_impact")
	}
	if report.PlanningCategories["plan_audit_gap"] > 0 || report.PlanningCategories["edit_readiness_failure"] > 0 || report.PlanningCategories["unsupported_plan_claim"] > 0 || report.PlanningCategories["validation_gap"] > 0 {
		surfaces = append(surfaces, "architect plan-audit")
	}
	if report.PlanningCategories["budget_gap"] > 0 || report.PlanningCategories["packet_bloat"] > 0 {
		surfaces = append(surfaces, "architect budget")
	}
	if report.PlanningCategories["replay_eval_gap"] > 0 {
		surfaces = append(surfaces, "architect replay-compare")
	}
	if report.PlanningCategories["stale_fact"] > 0 || report.PlanningCategories["backend_gap"] > 0 || report.PlanningCategories["language_precision_gap"] > 0 {
		surfaces = append(surfaces, "context_status", "context_pack")
	}
	if len(surfaces) == 0 && report.PlanningFeedback > 0 {
		surfaces = append(surfaces, "harness_feedback")
	}
	return util.UniqueNonEmpty(surfaces)
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

func staticConsolidationCandidates(root string) []harnessAuditQuestion {
	candidates := []harnessAuditQuestion{
		{
			Area:      "language_tooling",
			Question:  "Can repeated per-language capability schemas and examples be derived from LanguageRegistry?",
			Evidence:  "Go, C/C++, Python, TypeScript, Rust, and Java expose the same standard capability matrix with language-prefixed tools.",
			Candidate: "Generate conformance checks and manifest examples from the capability registry before generating implementation code.",
		},
	}
	if fileExists(filepath.Join(root, "cmd", "idiom-analyze", "main.go")) &&
		fileExists(filepath.Join(root, "cmd", "idiom-perf-eval", "main.go")) {
		candidates = append(candidates, harnessAuditQuestion{
			Area:      "behavior_logs",
			Question:  "Should legacy log-shape mining and perf anti-pattern reporting converge behind one audit command?",
			Evidence:  "Both cmd/idiom-analyze and cmd/idiom-perf-eval inspect JSONL behavior logs for improvement opportunities.",
			Candidate: "Keep specialized CLIs while harness_audit becomes the shared evidence packet for agents.",
		})
	}
	return candidates
}

func logBehaviorQuestions(aggregate eval.PerfAggregate) []harnessAuditQuestion {
	if aggregate.LogCount == 0 {
		return []harnessAuditQuestion{{
			Area:      "behavior_logs",
			Question:  "Which behavior logs should be captured to make harness improvement evidence-based?",
			Evidence:  "No JSONL logs were scanned.",
			Candidate: "Capture representative tasks with JSONL logging, then inspect the behavior-audit signal again.",
		}}
	}
	var questions []harnessAuditQuestion
	if hit := auditPatternHit(aggregate, "zero_hit_search"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "discovery_tooling",
			Question:  "Do repeated zero-hit searches point to missing query normalization, better symbol fallback, or a dedicated question tool?",
			Evidence:  fmt.Sprintf("zero_hit_search hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Cluster the raw failed patterns before adding a new tool; if they share shape, add one bounded deterministic probe.",
		})
	}
	if hit := auditPatternHit(aggregate, "dir_walk_streak"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "discovery_behavior",
			Question:  "Should broad directory walking surface a discovery-packet or language-overview capability earlier?",
			Evidence:  fmt.Sprintf("dir_walk_streak hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Prefer nudges and manifest shaping before hard-blocking safe reads.",
		})
	}
	if hit := auditPatternHit(aggregate, "rejection_retry"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "control_rails",
			Question:  "Are rejection packets clear enough, or should the harness hide the rejected tool/shape sooner?",
			Evidence:  fmt.Sprintf("rejection_retry hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Keep at least one safe model-directed path open; hide only repeated wasteful shapes.",
		})
	}
	if hit := auditPatternHit(aggregate, "malformed_edit_args"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "tool_schema",
			Question:  "Do write tools need stricter schemas, clearer examples, or host-side argument repair?",
			Evidence:  fmt.Sprintf("malformed_edit_args hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Tighten schema/examples first; add repair only for unambiguous transformations.",
		})
	}
	if hit := auditPatternHit(aggregate, "completion_gate_reopened_discovery"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "completion_recovery",
			Question:  "Did completion-gate recovery leave read/discovery looking like the normal next step after a required write was missing?",
			Evidence:  fmt.Sprintf("completion_gate_reopened_discovery hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Expose the recovery contract outcomes explicitly: required-artifact write, recorded uncertainty, or concrete blocker.",
		})
	}
	if hit := auditPatternHit(aggregate, "required_paths_blocked_without_write"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "write_contract",
			Question:  "Which route or recovery packet allowed an implementation run to end before any required-path write attempt?",
			Evidence:  fmt.Sprintf("required_paths_blocked_without_write hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Inspect the preceding completion_gate_recovery and next_action_state events before changing prompts; this is usually a contract-surface issue.",
		})
	}
	if hit := auditPatternHit(aggregate, "named_gap_read_walk"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "evidence_closure",
			Question:  "Which named edit-safety gap stayed open across repeated reads instead of becoming a plan, a write, or a blocker?",
			Evidence:  fmt.Sprintf("named_gap_read_walk hit %d read(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Audit whether the packet already contained the answer, whether a terminal/source-satisfied transition was missed, or whether the gap needs a deterministic closeout affordance.",
		})
	}
	if aggregate.Feedback != nil && aggregate.Feedback.Total > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "agent_feedback",
			Question:  "Which reported idiom harness/tool/prompt gap should be fixed before adding more task-specific guidance?",
			Evidence:  fmt.Sprintf("%d feedback event(s); top category %s; top component %s", aggregate.Feedback.Total, auditTopCount(aggregate.Feedback.ByCategory), auditTopCount(aggregate.Feedback.ByComponent)),
			Candidate: "Cluster by component and fix the smallest generic contract that removes the workaround.",
		})
	}
	if hit := auditPatternHit(aggregate, "prompt_surface_overlap"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "prompt_surface",
			Question:  "Is degraded behavior coming from overlapping prompt ownership rather than missing guidance?",
			Evidence:  fmt.Sprintf("prompt_surface_overlap hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Move repeated obligations into typed runtime briefs, deterministic checks, or one canonical prompt owner; delete duplicate prose before adding a new rule.",
		})
	}
	genericReads := auditToolCalls(aggregate, "list_files", "search_files", "search_content", "read_file")
	languageReads := auditToolPrefixCalls(aggregate, "go_", "cpp_", "python_", "ts_", "rust_", "java_")
	if genericReads >= 10 && genericReads > languageReads*2 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "language_tool_selection",
			Question:  "Are agents missing structured language-aware routes, or are those routes failing and causing fallback to generic reads?",
			Evidence:  fmt.Sprintf("generic read/search calls=%d, language-aware route calls=%d", genericReads, languageReads),
			Candidate: "Sample logs with high generic-read share and inspect precision_tier/unavailable results before adding tools.",
		})
	}
	return questions
}

func harnessAuditSummary(report harnessAuditReport) string {
	parts := []string{fmt.Sprintf("Ran harness audit (%s)", report.Purpose)}
	if report.ToolSurface != nil && report.ToolSurface.ManifestToolCount > 0 {
		parts = append(parts, fmt.Sprintf("tool_surface=%d tools/%d drift", report.ToolSurface.ManifestToolCount, report.ToolSurface.DriftFindingCount))
		if report.ToolSurface.CapabilityRegistry != nil {
			coverage := report.ToolSurface.CapabilityRegistry
			parts = append(parts, fmt.Sprintf("capability_coverage=%d/%d routed=%d disabled=%d fallback_reasons=%d unmapped=%d unsafe=%d deferred=%d", coverage.MappedReadOnlyAdapterCount, coverage.ReadOnlyAdapterCount, coverage.RoutedCapabilityCount, coverage.DisabledCapabilityCount, coverage.FallbackReasonCount, coverage.UnmappedAdapterCount, coverage.UnsafeMappedAdapterCount, coverage.DeferredAdapterCount))
		}
	}
	if report.LogBehavior != nil {
		parts = append(parts, fmt.Sprintf("logs=%d scanned", report.LogBehavior.LogsScanned))
	}
	if report.ContractCoverage != nil {
		parts = append(parts, fmt.Sprintf("contract_events=%d", report.ContractCoverage.TotalEvents))
	}
	if report.RankerLearning != nil && report.RankerLearning.LearningSummary != nil {
		learning := report.RankerLearning.LearningSummary
		parts = append(parts, fmt.Sprintf("ranker_learning_events=%d accepted=%d rejected=%d reasons=%d", learning.TotalEvents, learning.AcceptedCount, learning.RejectedCount, len(learning.ReasonCounts)))
	}
	if len(report.Findings) > 0 {
		parts = append(parts, fmt.Sprintf("findings=%d", len(report.Findings)))
	}
	if len(report.ImprovementQuestions) > 0 {
		parts = append(parts, fmt.Sprintf("questions=%d", len(report.ImprovementQuestions)))
	}
	return strings.Join(parts, "; ")
}

func firstAuditPatterns(in []eval.PerfPatternHit, n int) []eval.PerfPatternHit {
	if len(in) <= n {
		return append([]eval.PerfPatternHit(nil), in...)
	}
	return append([]eval.PerfPatternHit(nil), in[:n]...)
}

func firstAuditTools(in []eval.PerfToolMix, n int) []eval.PerfToolMix {
	if len(in) <= n {
		return append([]eval.PerfToolMix(nil), in...)
	}
	return append([]eval.PerfToolMix(nil), in[:n]...)
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

func auditPatternHit(aggregate eval.PerfAggregate, name string) eval.PerfPatternHit {
	for _, hit := range aggregate.PatternHitRate {
		if hit.Name == name {
			return hit
		}
	}
	return eval.PerfPatternHit{Name: name}
}

func auditToolCalls(aggregate eval.PerfAggregate, names ...string) int {
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	total := 0
	for _, tool := range aggregate.ToolMix {
		if _, ok := want[tool.Tool]; ok {
			total += tool.Calls
		}
	}
	return total
}

func auditToolPrefixCalls(aggregate eval.PerfAggregate, prefixes ...string) int {
	total := 0
	for _, tool := range aggregate.ToolMix {
		for _, prefix := range prefixes {
			if strings.HasPrefix(tool.Tool, prefix) {
				total += tool.Calls
				break
			}
		}
	}
	return total
}

func auditSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func auditSetDiff(left map[string]struct{}, right map[string]struct{}) []string {
	var out []string
	for value := range left {
		if _, ok := right[value]; !ok {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

func auditDuplicates(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	dups := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			dups[value] = struct{}{}
			continue
		}
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(dups))
	for value := range dups {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

// rankerLearningKnownTriggers is the canonical list of trigger sites the
// audit knows about. Each entry has a stable name, the rough component that
// would host it, and a one-line description so future automated
// recommendations have something to point at.
var rankerLearningKnownTriggers = []harnessAuditRankerTrigger{
	{
		Name:        "eval",
		Component:   "internal/eval/swe_suite.go",
		Description: "SWE eval groundedness oracle boost/penalize bands",
	},
	{
		Name:        "cli",
		Component:   "cmd/idiom/ranker.go",
		Description: "manual `idiom ranker boost|penalize|reset` invocations",
	},
	{
		Name:        "verified_edit",
		Component:   "internal/agent/runner.go",
		Description: "boost files surfaced before a verified edit; not yet wired",
	},
	{
		Name:        "harness_feedback",
		Component:   "internal/workspace/harness_feedback.go",
		Description: "self-reported relevance feedback from the agent; not yet wired",
	},
	{
		Name:        "audit_watch",
		Component:   "cmd/idiom/audit.go",
		Description: "autonomous review packets folding learning updates; not yet wired",
	},
	{
		Name:        comprehension.LearningSourceRecoveryReconciliation,
		Component:   "cmd/idiom/recover_packet.go",
		Description: "verified recovery reconciliation outcomes recorded as evidence-only learning events",
	},
}

// buildRankerLearningAudit reads the persisted ranker weights and learning
// log and returns the audit projection used by harness_audit.
func buildRankerLearningAudit(root string) *harnessAuditRankerLearning {
	out := &harnessAuditRankerLearning{
		WeightsPath:     filepath.Join(root, ".idiom", "ranker", "weights.json"),
		LearningLogPath: filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"),
	}
	if _, err := os.Stat(out.WeightsPath); err == nil {
		out.WeightsFileExists = true
	}
	if _, err := os.Stat(out.LearningLogPath); err == nil {
		out.LearningLogExists = true
	}
	weights, _ := comprehension.LoadLearnedWeights(root)
	out.WeightCount = len(weights)
	if out.WeightCount > 0 {
		var minMult, maxMult float64
		first := true
		for _, v := range weights {
			if first {
				minMult, maxMult = v, v
				first = false
				continue
			}
			if v < minMult {
				minMult = v
			}
			if v > maxMult {
				maxMult = v
			}
		}
		out.MinMultiplier = minMult
		out.MaxMultiplier = maxMult
	}
	entries, _ := comprehension.ReadLearningLog(root)
	if len(entries) > 0 {
		summary := comprehension.SummarizeLearningLog(entries)
		out.LearningSummary = &summary
	}
	out.WiredSources, out.DormantSources, out.TriggerRecommendations = classifyRankerTriggers(out.LearningSummary)
	return out
}

// classifyRankerTriggers labels every known trigger as wired (observed in
// the log), partial (some signal but missing for >30 days), or dormant
// (never observed). The harness audit uses this to surface concrete
// next-step suggestions for the learning loop.
func classifyRankerTriggers(summary *comprehension.LearningLogSummary) (wired, dormant []string, triggers []harnessAuditRankerTrigger) {
	bySource := map[string]int{}
	if summary != nil {
		bySource = summary.BySource
	}
	for _, base := range rankerLearningKnownTriggers {
		t := base
		count := bySource[t.Name]
		switch {
		case count > 0:
			t.Status = "wired"
			wired = append(wired, t.Name)
		default:
			t.Status = "dormant"
			dormant = append(dormant, t.Name)
		}
		t.Suggested = rankerTriggerSuggestion(t.Name, t.Status, count)
		triggers = append(triggers, t)
	}
	return wired, dormant, triggers
}

func rankerTriggerSuggestion(name, status string, count int) string {
	if status == "wired" {
		return fmt.Sprintf("observed %d entry(ies) from %s; keep monitoring decay and weight drift", count, name)
	}
	switch name {
	case "eval":
		return "SWE oracle Groundedness coverage on at least one task would produce eval-driven boost/penalize signals"
	case "cli":
		return "manual ranker CLI signals can seed boost/penalize evidence or override automated decisions"
	case "verified_edit":
		return "verified-edit signals can cover files that appeared in recent context packets after verification clears a write"
	case "harness_feedback":
		return "harness_feedback relevance categories can feed learned weights from self-reported useful and misleading files"
	case "audit_watch":
		return "audit watch review packets can convert low-groundedness session summaries and surfaced files into learned-weight evidence"
	case comprehension.LearningSourceRecoveryReconciliation:
		return "successful `idiom recover reconcile` runs record verified recovery outcomes without mutating learned weights"
	default:
		return "no signal observed yet"
	}
}

func rankerLearningFindings(learning *harnessAuditRankerLearning) []harnessAuditFinding {
	if learning == nil {
		return nil
	}
	var findings []harnessAuditFinding
	if !learning.WeightsFileExists && !learning.LearningLogExists {
		findings = append(findings, harnessAuditFinding{
			ID:             "ranker_learning_idle",
			Severity:       "info",
			Evidence:       "ranker learning is idle: no .idiom/ranker/weights.json or learning_log.jsonl present; expected before an eval, CLI, feedback, or recovery trigger records evidence",
			Recommendation: "no action needed unless learned ranker state was expected; run or wire an observed trigger source before expecting weight signals",
		})
		return findings
	}
	if learningSummaryMostlyMutableNoop(learning.LearningSummary) {
		findings = append(findings, harnessAuditFinding{
			ID:             "ranker_learning_mostly_noop",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d of %d learning entries landed in the no-op band", learning.LearningSummary.NoOpCount, learning.LearningSummary.TotalEntries),
			Recommendation: "raise oracle MinScore so high-quality runs cross the 0.75 boost threshold, or supply richer Groundedness ContextFiles",
		})
	}
	if len(learning.DormantSources) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "ranker_learning_dormant_sources",
			Severity:       "info",
			Evidence:       fmt.Sprintf("trigger sites with no observed activity: %v", learning.DormantSources),
			Recommendation: "see RankerLearning.trigger_recommendations for per-source suggestions",
		})
	}
	return findings
}

func learningSummaryMostlyMutableNoop(summary *comprehension.LearningLogSummary) bool {
	if summary == nil || summary.TotalEntries == 0 {
		return false
	}
	evidenceOnly := 0
	if summary.BySource != nil {
		evidenceOnly += summary.BySource[comprehension.LearningSourceRecoveryReconciliation]
	}
	mutableEntries := summary.TotalEntries - evidenceOnly
	if mutableEntries <= 0 {
		return false
	}
	mutableNoOps := summary.NoOpCount - evidenceOnly
	if mutableNoOps < 0 {
		mutableNoOps = 0
	}
	return mutableNoOps*4 > mutableEntries*3
}

func rankerLearningQuestions(learning *harnessAuditRankerLearning) []harnessAuditQuestion {
	if learning == nil {
		return nil
	}
	var questions []harnessAuditQuestion
	for _, t := range learning.TriggerRecommendations {
		if t.Status == "wired" {
			continue
		}
		questions = append(questions, harnessAuditQuestion{
			Area:      "ranker_learning",
			Question:  fmt.Sprintf("Should the %s trigger be wired to feed the learning loop?", t.Name),
			Evidence:  t.Description,
			Candidate: t.Component,
		})
	}
	return questions
}

// buildContextCacheAudit reads graph_views statistics from the
// comprehension SQLite store and combines them with the in-process
// hit/miss counters. Returns nil when no SQLite store exists yet (no
// `context_index` build has run), so an idle workspace doesn't surface
// a misleading "0 rows" finding.
func buildContextCacheAudit(root string) *harnessAuditContextCache {
	stats, err := comprehension.ReadViewStats(root)
	if err != nil {
		return nil
	}
	cache := &harnessAuditContextCache{
		Rows:                                  stats.Rows,
		Fresh:                                 stats.Fresh,
		Stale:                                 stats.Stale,
		NonCurrent:                            stats.NonCurrent,
		CurrentRows:                           stats.CurrentRows,
		CurrentFresh:                          stats.CurrentFresh,
		CurrentStale:                          stats.CurrentStale,
		HistoricalRows:                        stats.HistoricalRows,
		CurrentIndexID:                        stats.CurrentIndexID,
		BytesStored:                           stats.BytesStored,
		OldestCreated:                         stats.OldestCreated,
		NewestCreated:                         stats.NewestCreated,
		WorkspaceStateKeyedRows:               stats.WorkspaceStateKeyedRows,
		ContractRows:                          stats.ContractRows,
		UnresolvedGapRows:                     stats.UnresolvedGapRows,
		FreshUnresolvedGapRows:                stats.FreshUnresolvedGapRows,
		StaleUnresolvedGapRows:                stats.StaleUnresolvedGapRows,
		CurrentUnresolvedGapRows:              stats.CurrentUnresolvedGapRows,
		CurrentFreshUnresolvedGapRows:         stats.CurrentFreshUnresolvedGapRows,
		UnresolvedGapReasonCounts:             stats.UnresolvedGapReasonCounts,
		CurrentUnresolvedGapReasonCounts:      stats.CurrentUnresolvedGapReasonCounts,
		CurrentFreshUnresolvedGapReasonCounts: stats.CurrentFreshUnresolvedGapReasonCounts,
		SessionHits:                           stats.Hits,
		SessionMisses:                         stats.Misses,
		SessionWrites:                         stats.Writes,
		FreshnessHits:                         stats.FreshnessHits,
		StalePreventions:                      stats.StalePreventions,
		StaleRowMisses:                        stats.StaleRowMisses,
	}
	if total := stats.Hits + stats.Misses; total > 0 {
		cache.HitRate = float64(stats.Hits) / float64(total)
	}
	if stats.Rows > 0 {
		cache.StaleRatio = float64(stats.Stale) / float64(stats.Rows)
	}
	if stats.CurrentRows > 0 {
		cache.CurrentStaleRatio = float64(stats.CurrentStale) / float64(stats.CurrentRows)
	}
	return cache
}

func buildFreshnessHealthAudit(logContext *eval.ContextStats) harnessAuditFreshnessHealth {
	stats := comprehension.FreshnessHealthStats()
	_, stalePreventions, staleRowMisses := comprehension.ViewFreshnessCounters()
	health := harnessAuditFreshnessHealth{
		ReadThroughAttempts:        stats.ReadThroughAttempts,
		Blockers:                   stats.Blockers,
		BlockersByReason:           stats.BlockersByReason,
		StaleCachedViewPreventions: stalePreventions,
		StaleRowMisses:             staleRowMisses,
		RefreshFailures:            stats.RefreshFailures,
		RootRebuilds:               stats.RootRebuilds,
		AllowStaleInvocations:      stats.AllowStaleInvocations,
	}
	if logContext != nil {
		health.TelemetryReadThrough = int64(logContext.FreshnessReadThroughResults)
		health.TelemetryBlockers = int64(logContext.FreshnessBlockers)
		health.TelemetryBudgetExceeded = int64(logContext.FreshnessBudgetExceeded)
		health.TelemetryRefreshAttempts = int64(logContext.FreshnessRefreshAttempts)
		health.TelemetryRefreshFailures = int64(logContext.FreshnessRefreshFailures)
		health.TelemetryRefreshFiles = int64(logContext.FreshnessRefreshFilesIndexed)
		health.TelemetryAllowStale = int64(logContext.FreshnessPolicyCounts["allow_stale"])
		health.TelemetryPolicyCounts = auditIntCountsToInt64(logContext.FreshnessPolicyCounts)
		health.TelemetryReasonCounts = auditIntCountsToInt64(logContext.FreshnessReasonCounts)
		health.StaleUseBlocked = int64(logContext.StaleUseBlocked)
		health.StaleUseReported = int64(logContext.StaleUseReported)
		health.StaleUseAllowed = int64(logContext.StaleUseAllowed)
		health.StaleUseFresh = int64(logContext.StaleUseFresh)
		health.StaleUseStateCounts = auditIntCountsToInt64(logContext.StaleUseStateCounts)
	}
	return health
}

func logBehaviorContextStats(logBehavior *harnessAuditLogBehavior) *eval.ContextStats {
	if logBehavior == nil {
		return nil
	}
	return logBehavior.Aggregate.Context
}

func auditIntCountsToInt64(source map[string]int) map[string]int64 {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]int64, len(source))
	for key, value := range source {
		out[key] = int64(value)
	}
	return out
}

func freshnessHealthFindings(health *harnessAuditFreshnessHealth) []harnessAuditFinding {
	if health == nil {
		return nil
	}
	var findings []harnessAuditFinding
	if health.AllowStaleInvocations+health.TelemetryAllowStale > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "freshness_allow_stale_invoked",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d in-process and %d log-derived allow_stale freshness invocation(s) observed", health.AllowStaleInvocations, health.TelemetryAllowStale),
			Recommendation: "reason=allow_stale_invoked model_facing_policies=read_through,require_fresh",
		})
	}
	if health.RefreshFailures+health.TelemetryRefreshFailures > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "freshness_refresh_failures",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d in-process and %d log-derived freshness refresh failure(s) observed", health.RefreshFailures, health.TelemetryRefreshFailures),
			Recommendation: "reason=refresh_failed source=freshness_health",
		})
	}
	if health.TelemetryBlockers > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "freshness_log_blockers",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d log-derived freshness blocker(s) observed", health.TelemetryBlockers),
			Recommendation: "reason=freshness_blockers_observed source=log_behavior.aggregate.context",
		})
	}
	if health.StaleUseBlocked > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "stale_use_blockers_observed",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d log-derived stale-use blocker(s) observed", health.StaleUseBlocked),
			Recommendation: "reason=stale_use_blocked source=log_behavior.aggregate.context",
		})
	}
	return findings
}

// contextCacheFindings surfaces low-effort signals about cache health.
// Historical stale rows are informational cache history; current-index
// stale rows are stronger evidence that active cache rehydration will miss.
func contextCacheFindings(cache *harnessAuditContextCache) []harnessAuditFinding {
	if cache == nil {
		return nil
	}
	var findings []harnessAuditFinding
	if cache.Rows == 0 && cache.SessionMisses == 0 && cache.SessionWrites == 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_idle",
			Severity:       "info",
			Evidence:       "graph_views table is empty and no Pack() calls have run this session",
			Recommendation: "reason=context_cache_idle rows=0 session_misses=0 session_writes=0",
		})
		return findings
	}
	if cache.CurrentRows > 0 && cache.CurrentStaleRatio > 0.5 {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_mostly_stale",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d of %d current-index graph_views rows are stale (%.0f%%)", cache.CurrentStale, cache.CurrentRows, cache.CurrentStaleRatio*100),
			Recommendation: fmt.Sprintf("reason=context_cache_mostly_stale current_stale_ratio=%.2f stale_result=cache_miss_recompute", cache.CurrentStaleRatio),
		})
	} else if cache.Stale > 0 && cache.StaleRatio > 0.5 {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_historical_stale_rows",
			Severity:       "info",
			Evidence:       fmt.Sprintf("%d stale historical graph_views row(s) retained; current-index rows: fresh=%d stale=%d", cache.Stale, cache.CurrentFresh, cache.CurrentStale),
			Recommendation: fmt.Sprintf("reason=context_cache_historical_stale_rows stale_ratio=%.2f active_cache_state=current_fresh=%d,current_stale=%d", cache.StaleRatio, cache.CurrentFresh, cache.CurrentStale),
		})
	}
	if cache.Rows > 0 && cache.ContractRows < cache.Rows {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_contract_gap",
			Severity:       "info",
			Evidence:       fmt.Sprintf("%d of %d graph_views rows expose the graph-view result contract", cache.ContractRows, cache.Rows),
			Recommendation: fmt.Sprintf("reason=context_cache_contract_gap contract_rows=%d rows=%d", cache.ContractRows, cache.Rows),
		})
	}
	if cache.CurrentFreshUnresolvedGapRows > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_unresolved_gaps",
			Severity:       "info",
			Evidence:       fmt.Sprintf("%d current fresh graph_views row(s) report unresolved context evidence gaps%s", cache.CurrentFreshUnresolvedGapRows, auditCountSummarySuffix(cache.CurrentFreshUnresolvedGapReasonCounts, 3)),
			Recommendation: fmt.Sprintf("reason=context_cache_unresolved_gaps current_fresh_rows=%d gap_reasons=%s", cache.CurrentFreshUnresolvedGapRows, auditCountSummary(cache.CurrentFreshUnresolvedGapReasonCounts, 3)),
		})
	} else if cache.UnresolvedGapRows > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_historical_unresolved_gaps",
			Severity:       "info",
			Evidence:       fmt.Sprintf("%d stale or historical graph_views row(s) retain unresolved context evidence gaps%s", cache.UnresolvedGapRows, auditCountSummarySuffix(cache.UnresolvedGapReasonCounts, 3)),
			Recommendation: fmt.Sprintf("reason=context_cache_historical_unresolved_gaps rows=%d gap_reasons=%s", cache.UnresolvedGapRows, auditCountSummary(cache.UnresolvedGapReasonCounts, 3)),
		})
	}
	if cache.SessionHits+cache.SessionMisses >= 5 && cache.HitRate < 0.1 {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_low_hit_rate",
			Severity:       "info",
			Evidence:       fmt.Sprintf("%d hits / %d misses (%.0f%% hit rate) this session", cache.SessionHits, cache.SessionMisses, cache.HitRate*100),
			Recommendation: fmt.Sprintf("reason=context_cache_low_hit_rate hit_rate=%.2f cache_key_includes=task", cache.HitRate),
		})
	}
	return findings
}

func auditCountSummarySuffix(values map[string]int, limit int) string {
	summary := auditCountSummary(values, limit)
	if summary == "" {
		return ""
	}
	return "; reasons=" + summary
}

func auditCountSummary(values map[string]int, limit int) string {
	counts := auditTopCounts(values, limit)
	if len(counts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", count.Name, count.Count))
	}
	return strings.Join(parts, ",")
}
