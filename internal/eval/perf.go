package eval

// perf.go scans a conversation JSONL log for efficiency signals. It is
// intentionally descriptive, not prescriptive: every anti-pattern named
// here is something we've seen the agent actually do in live logs.
// Adding a new pattern is cheap; the payoff is that aggregate runs over
// a behavior suite surface the highest-frequency slack so we know which
// harness change will actually move the needle.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolCounts breaks down the tool mix used in a single turn. The
// categories are coarse: total per tool name plus a few "kinds" rolled
// up (go_* and cpp_* semantic tools, doc-class reads). Everything else
// lands in Other — the goal is to notice skew, not bucket perfectly.
type ToolCounts struct {
	ListFiles         int            `json:"list_files,omitempty"`
	ReadFile          int            `json:"read_file,omitempty"`
	SearchContent     int            `json:"search_content,omitempty"`
	SearchFiles       int            `json:"search_files,omitempty"`
	GoSemantic        int            `json:"go_semantic,omitempty"`
	CppSemantic       int            `json:"cpp_semantic,omitempty"`
	Edit              int            `json:"edit,omitempty"`
	Shell             int            `json:"shell,omitempty"`
	Other             int            `json:"other,omitempty"`
	Total             int            `json:"total,omitempty"`
	ByName            map[string]int `json:"by_name,omitempty"`
	DocClassReads     int            `json:"doc_class_reads,omitempty"`
	SourceClassReads  int            `json:"source_class_reads,omitempty"`
	DistinctReadPaths int            `json:"distinct_read_paths,omitempty"`
}

// AntiPattern is one kind of inefficiency observed in a log. Count is
// how many times it fired; Examples are short, human-readable pointers
// so you can click back to the moment it happened.
type AntiPattern struct {
	Name        string   `json:"name"`
	Count       int      `json:"count"`
	Description string   `json:"description,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// PerfReport is the per-log rollup. Intentionally flat so it marshals
// to a single line in aggregate output; ByName on ToolCounts is the
// one place detail lives.
type PerfReport struct {
	LogPath          string         `json:"log_path"`
	SessionID        string         `json:"session_id,omitempty"`
	UserPrompt       string         `json:"-"`
	PromptRedacted   bool           `json:"prompt_redacted,omitempty"`
	ModelCalls       int            `json:"model_calls"`
	ToolResults      int            `json:"tool_results"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	DurationMillis   int            `json:"duration_millis"`
	RejectedActions  int            `json:"rejected_actions"`
	StopReason       string         `json:"stop_reason,omitempty"`
	Tools            ToolCounts     `json:"tools"`
	Context          *ContextStats  `json:"context,omitempty"`
	Feedback         *FeedbackStats `json:"feedback,omitempty"`
	LocalOps         *LocalOpsStats `json:"local_ops,omitempty"`
	AntiPatterns     []AntiPattern  `json:"anti_patterns,omitempty"`
	AntiPatternScore int            `json:"anti_pattern_score"`
	FinalAnswerChars int            `json:"final_answer_chars,omitempty"`
}

type LocalOpsStats struct {
	BackendEvents           int            `json:"backend_events,omitempty"`
	BackendConfigured       int            `json:"backend_configured,omitempty"`
	QueueEvents             int            `json:"queue_events,omitempty"`
	QueueDegraded           int            `json:"queue_degraded,omitempty"`
	QueueMaxWaitMillis      int            `json:"queue_max_wait_ms,omitempty"`
	ContextBudgetEvents     int            `json:"context_budget_events,omitempty"`
	ContextBudgetDegraded   int            `json:"context_budget_degraded,omitempty"`
	ClassifierEvents        int            `json:"classifier_events,omitempty"`
	ClassifierDegraded      int            `json:"classifier_degraded,omitempty"`
	ImplementCloseouts      int            `json:"implement_closeouts,omitempty"`
	DoctorReadinessProfiles int            `json:"doctor_readiness_profiles,omitempty"`
	DoctorDegradedProfiles  int            `json:"doctor_degraded_profiles,omitempty"`
	DoctorBlockedProfiles   int            `json:"doctor_blocked_profiles,omitempty"`
	ByQueueState            map[string]int `json:"by_queue_state,omitempty"`
	ByContextBudgetState    map[string]int `json:"by_context_budget_state,omitempty"`
	ByClassifierState       map[string]int `json:"by_classifier_state,omitempty"`
	ByBackendState          map[string]int `json:"by_backend_state,omitempty"`
	ByReadinessProfileState map[string]int `json:"by_readiness_profile_state,omitempty"`
}

type ContextStats struct {
	ToolResults                  int            `json:"tool_results,omitempty"`
	IndexedResults               int            `json:"indexed_results,omitempty"`
	FreshResults                 int            `json:"fresh_results,omitempty"`
	StaleResults                 int            `json:"stale_results,omitempty"`
	IncompatibleBackendResults   int            `json:"incompatible_backend_results,omitempty"`
	TraceFailures                int            `json:"trace_failures,omitempty"`
	MaxFilesIndexed              int            `json:"max_files_indexed,omitempty"`
	MaxSymbolsIndexed            int            `json:"max_symbols_indexed,omitempty"`
	MaxEdgesIndexed              int            `json:"max_edges_indexed,omitempty"`
	MaxStaleSymbols              int            `json:"max_stale_symbols,omitempty"`
	RefreshedStaleSymbols        int            `json:"refreshed_stale_symbols,omitempty"`
	StaleSymbolSamples           int            `json:"stale_symbol_samples,omitempty"`
	IncrementalRefreshes         int            `json:"incremental_refreshes,omitempty"`
	ReusedIndexedFiles           int            `json:"reused_indexed_files,omitempty"`
	DependencyRefreshFiles       int            `json:"dependency_refresh_files,omitempty"`
	AutoRefreshAttempts          int            `json:"auto_refresh_attempts,omitempty"`
	AutoRefreshSkips             int            `json:"auto_refresh_skips,omitempty"`
	AutoRefreshFailures          int            `json:"auto_refresh_failures,omitempty"`
	AutoRefreshFreshAfter        int            `json:"auto_refresh_fresh_after,omitempty"`
	AutoRefreshFilesIndexed      int            `json:"auto_refresh_files_indexed,omitempty"`
	AutoRefreshRefreshedFiles    int            `json:"auto_refresh_refreshed_files,omitempty"`
	FreshnessReadThroughResults  int            `json:"freshness_read_through_results,omitempty"`
	FreshnessBlockers            int            `json:"freshness_blockers,omitempty"`
	FreshnessBudgetExceeded      int            `json:"freshness_budget_exceeded,omitempty"`
	FreshnessRefreshAttempts     int            `json:"freshness_refresh_attempts,omitempty"`
	FreshnessRefreshFailures     int            `json:"freshness_refresh_failures,omitempty"`
	FreshnessRefreshFilesIndexed int            `json:"freshness_refresh_files_indexed,omitempty"`
	StaleUseBlocked              int            `json:"stale_use_blocked,omitempty"`
	StaleUseReported             int            `json:"stale_use_reported,omitempty"`
	StaleUseAllowed              int            `json:"stale_use_allowed,omitempty"`
	StaleUseFresh                int            `json:"stale_use_fresh,omitempty"`
	NodesReturned                int            `json:"nodes_returned,omitempty"`
	EdgesReturned                int            `json:"edges_returned,omitempty"`
	SummaryNodesReturned         int            `json:"summary_nodes_returned,omitempty"`
	ModelSummaryNodesReturned    int            `json:"model_summary_nodes_returned,omitempty"`
	CandidateFiles               int            `json:"candidate_files,omitempty"`
	CandidateSymbols             int            `json:"candidate_symbols,omitempty"`
	ImpactScope                  int            `json:"impact_scope,omitempty"`
	TestsSuggested               int            `json:"tests_suggested,omitempty"`
	UnresolvedGaps               int            `json:"unresolved_gaps,omitempty"`
	CollectorReports             int            `json:"collector_reports,omitempty"`
	CollectorCoverage            int            `json:"collector_coverage,omitempty"`
	CollectorUnavailable         int            `json:"collector_unavailable,omitempty"`
	CollectorWarnings            int            `json:"collector_warnings,omitempty"`
	CollectorVersionMismatches   int            `json:"collector_version_mismatches,omitempty"`
	EnrichmentDiagnostics        int            `json:"enrichment_diagnostics,omitempty"`
	NormalizedRelationshipEdges  int            `json:"normalized_relationship_edges,omitempty"`
	ActiveBackendCounts          map[string]int `json:"active_backend_counts,omitempty"`
	ActiveBackendStatusCounts    map[string]int `json:"active_backend_status_counts,omitempty"`
	StoreKindCounts              map[string]int `json:"store_kind_counts,omitempty"`
	StorageBackendStatusCounts   map[string]int `json:"storage_backend_status_counts,omitempty"`
	CollectorReportNameCounts    map[string]int `json:"collector_report_name_counts,omitempty"`
	CollectorCoverageNameCounts  map[string]int `json:"collector_coverage_name_counts,omitempty"`
	AutoRefreshReasonCounts      map[string]int `json:"auto_refresh_reason_counts,omitempty"`
	AutoRefreshSkippedCounts     map[string]int `json:"auto_refresh_skipped_counts,omitempty"`
	FreshnessPolicyCounts        map[string]int `json:"freshness_policy_counts,omitempty"`
	FreshnessReasonCounts        map[string]int `json:"freshness_reason_counts,omitempty"`
	StaleUseStateCounts          map[string]int `json:"stale_use_state_counts,omitempty"`
}

type FeedbackStats struct {
	Total                int            `json:"total,omitempty"`
	Verified             int            `json:"verified,omitempty"`
	Inferred             int            `json:"inferred,omitempty"`
	HighSeverity         int            `json:"high_severity,omitempty"`
	MediumSeverity       int            `json:"medium_severity,omitempty"`
	LowSeverity          int            `json:"low_severity,omitempty"`
	EvidenceItems        int            `json:"evidence_items,omitempty"`
	PlanningQualityTotal int            `json:"planning_quality_total,omitempty"`
	ByCategory           map[string]int `json:"by_category,omitempty"`
	ByComponent          map[string]int `json:"by_component,omitempty"`
	ByConfidence         map[string]int `json:"by_confidence,omitempty"`
	BySeverity           map[string]int `json:"by_severity,omitempty"`
	ByPlanningCategory   map[string]int `json:"by_planning_category,omitempty"`
}

// AnalyzeLog reads a JSONL conversation log from disk and produces one
// PerfReport. Parse errors propagate; missing optional events do not.
func AnalyzeLog(path string) (PerfReport, error) {
	records, err := LoadRecords(path)
	if err != nil {
		return PerfReport{}, err
	}
	return analyzeRecords(path, records), nil
}

// toolStep is the analyzer's view of one tool_request (+ its paired
// tool_result). Kept inside the eval package so detectors can share
// the shape without re-declaring it.
type toolStep struct {
	index     int
	tool      string
	action    string
	rawArgs   string
	target    string
	isReject  bool
	rejectWhy string
	hasError  bool
	errText   string
}

func analyzeRecords(path string, records []Record) PerfReport {
	rep := PerfReport{
		LogPath: path,
		Tools:   ToolCounts{ByName: make(map[string]int)},
	}

	var steps []toolStep
	agentBehavior := false
	// Walk records once, collecting what every pattern needs.
	for _, rec := range records {
		ingestLocalOpsTelemetry(&rep, rec.Type, rec.Data)
		switch rec.Type {
		case "session_start":
			if v, ok := rec.Data["workspace_root"].(string); ok && v != "" {
				_ = v
			}
			// The log file name is the session id surrogate.
		case "message":
			role, _ := rec.Data["role"].(string)
			content, _ := rec.Data["content"].(string)
			if strings.TrimSpace(role) != "" {
				agentBehavior = true
			}
			switch role {
			case "user":
				if rep.UserPrompt == "" {
					rep.UserPrompt = firstLine(content)
				}
			case "assistant":
				// Keep only the last assistant message as the final.
				rep.FinalAnswerChars = len(content)
			case "budget_ledger":
				parseBudgetLedgerIntoPerfReport(content, &rep)
			}
		case "user_turn":
			agentBehavior = true
			if rep.UserPrompt == "" {
				if prompt, ok := rec.Data["prompt"].(string); ok {
					rep.UserPrompt = firstLine(prompt)
				}
			}
		case "assistant_turn":
			agentBehavior = true
			if chars := intField(rec.Data, "chars"); chars > 0 {
				rep.FinalAnswerChars = chars
			}
		case "model_call":
			agentBehavior = true
			rep.ModelCalls++
			rep.PromptTokens += intField(rec.Data, "prompt_tokens")
			rep.CompletionTokens += intField(rec.Data, "completion_tokens")
		case "turn_summary":
			agentBehavior = true
			parseSummaryRecordIntoPerfReport(rec.Data, &rep)
		case "session_summary":
			agentBehavior = true
			parseSessionSummaryIntoPerfReport(rec.Data, &rep)
		case "tool_request":
			tool, _ := rec.Data["tool"].(string)
			action, _ := rec.Data["action"].(string)
			raw, _ := rec.Data["raw"].(string)
			steps = append(steps, toolStep{
				index:   len(steps) + 1,
				tool:    tool,
				action:  action,
				rawArgs: raw,
				target:  targetFromAction(action),
			})
		case "tool_result":
			// Pair to the most recent request (same order).
			tool, _ := rec.Data["tool"].(string)
			errMsg, _ := rec.Data["error"].(string)
			summary, _ := rec.Data["summary"].(string)
			if len(steps) > 0 && steps[len(steps)-1].tool == tool {
				idx := len(steps) - 1
				if errMsg != "" {
					steps[idx].hasError = true
					steps[idx].errText = errMsg
					// A "rejection" specifically means a pre-execute
					// guard fired — the summary shape is well-known
					// ("... rejected" or "retry blocked"). Other
					// errors are tool-execution failures (invalid
					// args, missing paths, workspace errors) which
					// we track separately so detectors can target them.
					lowered := strings.ToLower(summary)
					if strings.Contains(lowered, "rejected") || strings.Contains(lowered, "retry blocked") || strings.Contains(lowered, "preempted") {
						steps[idx].isReject = true
						steps[idx].rejectWhy = summary
						rep.RejectedActions++
					}
				}
			}
			countTool(&rep.Tools, tool, summary)
			ingestContextTelemetry(&rep, rec.Data)
			ingestFeedbackTelemetry(&rep, rec.Data)
		case "summary":
			if rep.DurationMillis == 0 {
				rep.DurationMillis = intField(rec.Data, "elapsed_ms")
			}
			if rep.ToolResults == 0 {
				rep.ToolResults = intField(rec.Data, "tool_results")
			}
			if rep.FinalAnswerChars == 0 {
				rep.FinalAnswerChars = intField(rec.Data, "answer_chars")
			}
		}
	}

	// Prefer the budget_ledger's tool-result count when it was present;
	// fall back to the number of tool_request events otherwise. Older
	// logs predate budget_ledger.
	if rep.ToolResults == 0 {
		rep.ToolResults = len(steps)
	}

	// Anti-pattern detectors. Each is independent; all run on the same
	// step list so adding a new one is local. Direct CLI tool telemetry is
	// still useful for tool/context statistics, but it is not model behavior.
	if agentBehavior {
		detectDirWalk(&rep, steps)
		detectZeroHitSearch(&rep, records)
		detectTruncatedSearch(&rep, records)
		detectRedirectStorm(&rep, records)
		detectSameToolRepeat(&rep, steps)
		detectNamedGapReadWalk(&rep, steps)
		detectDocWander(&rep, steps)
		detectPromptSurfaceOverlap(&rep, records)
		detectRejectionRetry(&rep, steps)
		detectMalformedEditArgs(&rep, records)
		detectToolErrorStreak(&rep, steps, records)
		detectCompletionGateReopenedDiscovery(&rep, records)
		detectRequiredPathsBlockedWithoutWrite(&rep, records)
	}

	score := 0
	for _, ap := range rep.AntiPatterns {
		score += ap.Count
	}
	rep.AntiPatternScore = score

	// If a budget_ledger record wasn't present, infer from messages.
	if rep.ModelCalls == 0 {
		for _, rec := range records {
			if rec.Type == "message" {
				role, _ := rec.Data["role"].(string)
				if role == "budget_ledger" {
					if content, ok := rec.Data["content"].(string); ok {
						parseBudgetLedgerIntoPerfReport(content, &rep)
					}
				}
			}
		}
	}
	rep.PromptRedacted = rep.UserPrompt != ""

	return rep
}

func ingestContextTelemetry(rep *PerfReport, data map[string]any) {
	tool := stringField(data, "tool")
	if !strings.HasPrefix(tool, "context_") && stringField(data, "context_store_kind") == "" && stringField(data, "context_active_backend") == "" {
		return
	}
	if rep.Context == nil {
		rep.Context = &ContextStats{
			ActiveBackendCounts:         make(map[string]int),
			ActiveBackendStatusCounts:   make(map[string]int),
			StoreKindCounts:             make(map[string]int),
			StorageBackendStatusCounts:  make(map[string]int),
			CollectorReportNameCounts:   make(map[string]int),
			CollectorCoverageNameCounts: make(map[string]int),
			AutoRefreshReasonCounts:     make(map[string]int),
			AutoRefreshSkippedCounts:    make(map[string]int),
			FreshnessPolicyCounts:       make(map[string]int),
			FreshnessReasonCounts:       make(map[string]int),
			StaleUseStateCounts:         make(map[string]int),
		}
	}
	ctx := rep.Context
	ctx.ToolResults++
	if boolField(data, "context_indexed") {
		ctx.IndexedResults++
	}
	if _, ok := data["context_fresh"]; ok {
		if boolField(data, "context_fresh") {
			ctx.FreshResults++
		} else {
			ctx.StaleResults++
		}
	}
	if intField(data, "context_incompatible_backends") > 0 {
		ctx.IncompatibleBackendResults++
	}
	if _, ok := data["context_trace_success"]; ok && !boolField(data, "context_trace_success") {
		ctx.TraceFailures++
	}
	ctx.MaxFilesIndexed = max(ctx.MaxFilesIndexed, intField(data, "context_files_indexed"))
	ctx.MaxSymbolsIndexed = max(ctx.MaxSymbolsIndexed, intField(data, "context_symbols_indexed"))
	ctx.MaxEdgesIndexed = max(ctx.MaxEdgesIndexed, intField(data, "context_edges_indexed"))
	ctx.MaxStaleSymbols = max(ctx.MaxStaleSymbols, intField(data, "context_stale_symbols"))
	ctx.RefreshedStaleSymbols += intField(data, "context_refreshed_stale_symbols")
	ctx.StaleSymbolSamples += intField(data, "context_stale_symbol_samples")
	if boolField(data, "context_incremental") {
		ctx.IncrementalRefreshes++
	}
	ctx.ReusedIndexedFiles += intField(data, "context_reused_indexed_files")
	ctx.DependencyRefreshFiles += intField(data, "context_dependency_refresh_files")
	if _, ok := data["context_auto_refresh_attempted"]; ok {
		if boolField(data, "context_auto_refresh_attempted") {
			ctx.AutoRefreshAttempts++
		} else if stringField(data, "context_auto_refresh_skipped_reason") != "" {
			ctx.AutoRefreshSkips++
		}
	}
	if stringField(data, "context_auto_refresh_error") != "" {
		ctx.AutoRefreshFailures++
	}
	if boolField(data, "context_auto_refresh_fresh_after") {
		ctx.AutoRefreshFreshAfter++
	}
	ctx.AutoRefreshFilesIndexed += intField(data, "context_auto_refresh_files_indexed")
	ctx.AutoRefreshRefreshedFiles += intField(data, "context_auto_refresh_refreshed_files")
	if stringField(data, "context_freshness_policy") != "" {
		incrementStringCount(ctx.FreshnessPolicyCounts, stringField(data, "context_freshness_policy"))
		if stringField(data, "context_freshness_policy") == "read_through" {
			ctx.FreshnessReadThroughResults++
		}
	}
	if boolField(data, "context_freshness_blocked") {
		ctx.FreshnessBlockers++
	}
	if boolField(data, "context_freshness_budget_exceeded") {
		ctx.FreshnessBudgetExceeded++
	}
	if boolField(data, "context_freshness_refresh_attempted") {
		ctx.FreshnessRefreshAttempts++
	}
	if stringField(data, "context_freshness_refresh_error") != "" {
		ctx.FreshnessRefreshFailures++
	}
	ctx.FreshnessRefreshFilesIndexed += intField(data, "context_freshness_refresh_files_indexed")
	incrementStringCount(ctx.FreshnessReasonCounts, stringField(data, "context_freshness_reason"))
	incrementStringCount(ctx.StaleUseStateCounts, stringField(data, "context_stale_evidence_state"))
	if boolField(data, "context_stale_use_blocked") {
		ctx.StaleUseBlocked++
	}
	if boolField(data, "context_stale_use_reported") {
		ctx.StaleUseReported++
	}
	if boolField(data, "context_stale_use_allowed") {
		ctx.StaleUseAllowed++
	}
	if boolField(data, "context_stale_use_fresh") {
		ctx.StaleUseFresh++
	}
	ctx.NodesReturned += intField(data, "context_nodes")
	ctx.EdgesReturned += intField(data, "context_edges")
	ctx.SummaryNodesReturned += intField(data, "context_summary_nodes")
	ctx.ModelSummaryNodesReturned += intField(data, "context_model_summary_nodes")
	ctx.CandidateFiles += intField(data, "context_candidate_files")
	ctx.CandidateSymbols += intField(data, "context_candidate_symbols")
	ctx.ImpactScope += intField(data, "context_impact_scope")
	ctx.TestsSuggested += intField(data, "context_tests")
	ctx.UnresolvedGaps += intField(data, "context_unresolved_gaps")
	ctx.CollectorReports += intField(data, "context_collector_reports")
	ctx.CollectorCoverage += intField(data, "context_collector_coverage")
	ctx.CollectorUnavailable += intField(data, "context_collector_unavailable")
	ctx.CollectorWarnings += intField(data, "context_collector_warnings")
	if boolField(data, "context_collector_version_mismatch") {
		ctx.CollectorVersionMismatches++
	}
	ctx.EnrichmentDiagnostics += intField(data, "context_enrichment_diagnostics")
	ctx.NormalizedRelationshipEdges += intField(data, "context_normalized_relationship_edges")
	incrementStringCount(ctx.ActiveBackendCounts, stringField(data, "context_active_backend"))
	incrementStringCount(ctx.ActiveBackendStatusCounts, stringField(data, "context_active_backend_status"))
	incrementStringCount(ctx.StoreKindCounts, stringField(data, "context_store_kind"))
	for _, status := range stringSliceField(data, "context_storage_backends") {
		incrementStringCount(ctx.StorageBackendStatusCounts, status)
	}
	for _, name := range stringSliceField(data, "context_collector_report_names") {
		incrementStringCount(ctx.CollectorReportNameCounts, name)
	}
	for _, name := range stringSliceField(data, "context_collector_coverage_names") {
		incrementStringCount(ctx.CollectorCoverageNameCounts, name)
	}
	incrementStringCount(ctx.AutoRefreshReasonCounts, stringField(data, "context_auto_refresh_reason"))
	incrementStringCount(ctx.AutoRefreshSkippedCounts, stringField(data, "context_auto_refresh_skipped_reason"))
}

func ingestFeedbackTelemetry(rep *PerfReport, data map[string]any) {
	tool := stringField(data, "tool")
	category := stringField(data, "feedback_category")
	if tool != "harness_feedback" && tool != "idiom_feedback" && category == "" {
		return
	}
	if rep.Feedback == nil {
		rep.Feedback = newFeedbackStats()
	}
	feedback := rep.Feedback
	feedback.Total++
	feedback.EvidenceItems += intField(data, "feedback_evidence_count")
	confidence := stringField(data, "feedback_confidence")
	severity := stringField(data, "feedback_severity")
	incrementStringCount(feedback.ByCategory, category)
	incrementStringCount(feedback.ByComponent, stringField(data, "feedback_component"))
	incrementStringCount(feedback.ByConfidence, confidence)
	incrementStringCount(feedback.BySeverity, severity)
	if feedbackCategoryIsPlanningQuality(category) {
		feedback.PlanningQualityTotal++
		incrementStringCount(feedback.ByPlanningCategory, category)
	}
	switch confidence {
	case "verified":
		feedback.Verified++
	case "inferred":
		feedback.Inferred++
	}
	switch severity {
	case "high":
		feedback.HighSeverity++
	case "medium":
		feedback.MediumSeverity++
	case "low":
		feedback.LowSeverity++
	}
}

func ingestLocalOpsTelemetry(rep *PerfReport, eventType string, data map[string]any) {
	if data == nil {
		return
	}
	localOpsPayload, _ := data["local_ops"].(map[string]any)
	hasLocalOps := localOpsPayload != nil
	isQueue := eventType == "implement_queue" || eventType == "queue_admission" || strings.HasPrefix(eventType, "idiom.implement.queue_")
	isDoctor := eventType == "doctor_readiness_profile"
	isCloseout := eventType == "implement_session_closeout"
	if !hasLocalOps && !isQueue && !isDoctor && !isCloseout {
		return
	}
	if rep.LocalOps == nil {
		rep.LocalOps = newLocalOpsStats()
	}
	stats := rep.LocalOps
	if hasLocalOps {
		stats.BackendEvents++
		if boolField(localOpsPayload, "backend_configured") {
			stats.BackendConfigured++
		}
		incrementStringCount(stats.ByBackendState, stringField(localOpsPayload, "backend_kind"))
		stats.ContextBudgetEvents++
		contextState := stringField(localOpsPayload, "context_budget_state")
		incrementStringCount(stats.ByContextBudgetState, contextState)
		if contextState != "" && contextState != "pass" {
			stats.ContextBudgetDegraded++
		}
		stats.ClassifierEvents++
		classifierState := stringField(localOpsPayload, "classifier_state")
		incrementStringCount(stats.ByClassifierState, classifierState)
		if classifierState != "configured" && classifierState != "pass" && classifierState != "" {
			stats.ClassifierDegraded++
		}
	}
	if isQueue {
		stats.QueueEvents++
		state := stringField(data, "state")
		incrementStringCount(stats.ByQueueState, state)
		if boolField(data, "degraded") || (state != "" && state != "granted" && state != "acquired" && state != "disabled" && state != "nested_same_pool") {
			stats.QueueDegraded++
		}
		stats.QueueMaxWaitMillis = max(stats.QueueMaxWaitMillis, intField(data, "wait_ms"))
	}
	if isDoctor {
		stats.DoctorReadinessProfiles++
		state := stringField(data, "state")
		incrementStringCount(stats.ByReadinessProfileState, state)
		if boolField(data, "degraded") || state == "degraded" {
			stats.DoctorDegradedProfiles++
		}
		if boolField(data, "blocked") || state == "blocked" {
			stats.DoctorBlockedProfiles++
		}
		incrementStringCount(stats.ByBackendState, stringField(data, "backend_state"))
		incrementStringCount(stats.ByQueueState, stringField(data, "queue_state"))
		incrementStringCount(stats.ByContextBudgetState, stringField(data, "context_budget_state"))
		incrementStringCount(stats.ByClassifierState, stringField(data, "route_classifier_state"))
	}
	if isCloseout {
		stats.ImplementCloseouts++
	}
}

func newLocalOpsStats() *LocalOpsStats {
	return &LocalOpsStats{
		ByQueueState:            make(map[string]int),
		ByContextBudgetState:    make(map[string]int),
		ByClassifierState:       make(map[string]int),
		ByBackendState:          make(map[string]int),
		ByReadinessProfileState: make(map[string]int),
	}
}

func newFeedbackStats() *FeedbackStats {
	return &FeedbackStats{
		ByCategory:         make(map[string]int),
		ByComponent:        make(map[string]int),
		ByConfidence:       make(map[string]int),
		BySeverity:         make(map[string]int),
		ByPlanningCategory: make(map[string]int),
	}
}

func feedbackCategoryIsPlanningQuality(category string) bool {
	switch strings.TrimSpace(category) {
	case "planning_quality", "packet_quality", "impact_ranking", "plan_audit_gap", "budget_gap", "replay_eval_gap",
		"missing_affordance", "bad_scope_rank", "stale_fact", "backend_gap", "packet_bloat", "read_loop",
		"edit_readiness_failure", "unsupported_plan_claim", "validation_gap", "language_precision_gap":
		return true
	default:
		return false
	}
}

func incrementStringCount(counts map[string]int, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		counts[value]++
	}
}

func stringSliceField(data map[string]any, key string) []string {
	if data == nil {
		return nil
	}
	switch typed := data[key].(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if ok && strings.TrimSpace(value) != "" {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func parseBudgetLedgerIntoPerfReport(content string, rep *PerfReport) {
	var payload struct {
		UsedModelCalls   int    `json:"used_model_calls"`
		UsedToolResults  int    `json:"used_tool_results"`
		PromptTokens     int    `json:"prompt_tokens"`
		CompletionTokens int    `json:"completion_tokens"`
		DurationMillis   int    `json:"duration_millis"`
		RejectedActions  int    `json:"rejected_actions"`
		StopReason       string `json:"stop_reason"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return
	}
	rep.ModelCalls = payload.UsedModelCalls
	rep.ToolResults = payload.UsedToolResults
	rep.PromptTokens = payload.PromptTokens
	rep.CompletionTokens = payload.CompletionTokens
	rep.DurationMillis = payload.DurationMillis
	if payload.RejectedActions > 0 {
		rep.RejectedActions = payload.RejectedActions
	}
	rep.StopReason = payload.StopReason
}

func parseSummaryRecordIntoPerfReport(data map[string]any, rep *PerfReport) {
	if v := intField(data, "used_model_calls"); v > 0 {
		rep.ModelCalls = v
	}
	if v := intField(data, "used_tool_results"); v > 0 {
		rep.ToolResults = v
	}
	if v := intField(data, "prompt_tokens"); v > 0 {
		rep.PromptTokens = v
	}
	if v := intField(data, "completion_tokens"); v > 0 {
		rep.CompletionTokens = v
	}
	if v := intField(data, "duration_millis"); v > 0 {
		rep.DurationMillis = v
	} else if v := intField(data, "duration_ms"); v > 0 {
		rep.DurationMillis = v
	}
	if v := intField(data, "rejected_actions"); v > 0 {
		rep.RejectedActions = v
	}
	if stopReason, _ := data["stop_reason"].(string); strings.TrimSpace(stopReason) != "" {
		rep.StopReason = stopReason
	}
}

func parseSessionSummaryIntoPerfReport(data map[string]any, rep *PerfReport) {
	if v := intField(data, "model_calls"); v > 0 {
		rep.ModelCalls = v
	}
	if v := intField(data, "tool_calls"); v > 0 {
		rep.ToolResults = v
	}
	if v := intField(data, "prompt_tokens"); v > 0 {
		rep.PromptTokens = v
	}
	if v := intField(data, "completion_tokens"); v > 0 {
		rep.CompletionTokens = v
	}
	if v := intField(data, "duration_millis"); v > 0 {
		rep.DurationMillis = v
	} else if v := intField(data, "duration_ms"); v > 0 {
		rep.DurationMillis = v
	}
}

func countTool(t *ToolCounts, tool, summary string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	t.Total++
	t.ByName[tool]++
	switch tool {
	case "list_files":
		t.ListFiles++
	case "read_file":
		t.ReadFile++
		if strings.Contains(strings.ToLower(summary), ".md") ||
			strings.Contains(strings.ToLower(summary), "readme") ||
			strings.Contains(strings.ToLower(summary), ".txt") {
			t.DocClassReads++
		} else {
			t.SourceClassReads++
		}
	case "search_content", "rg", "grep":
		t.SearchContent++
	case "search_files":
		t.SearchFiles++
	case "shell":
		t.Shell++
	case "edit_file", "write_file", "append_file", "apply_patch":
		t.Edit++
	default:
		switch {
		case strings.HasPrefix(tool, "go_"):
			t.GoSemantic++
		case strings.HasPrefix(tool, "cpp_"):
			t.CppSemantic++
		default:
			t.Other++
		}
	}
}

func targetFromAction(action string) string {
	// actions are shaped like "read_file:path/to/file" or
	// "search_content:.:pattern:_" — the second colon-delimited field
	// is the most useful "target" for dedupe / examples.
	if action == "" {
		return ""
	}
	parts := strings.SplitN(action, ":", 3)
	if len(parts) < 2 {
		return parts[0]
	}
	return parts[1]
}

// ---- detectors ----

func detectDirWalk(rep *PerfReport, steps []toolStep) {
	// Any run of 3+ consecutive list_files without a content tool in
	// between counts as a dir-walk streak. One streak per walk, even
	// if it's 6 long — the metric is "how often did this happen",
	// not "how many calls did it cost".
	streaks := 0
	var examples []string
	run := 0
	runStart := 0
	for i, s := range steps {
		if s.tool == "list_files" {
			if run == 0 {
				runStart = s.index
			}
			run++
			continue
		}
		if run >= 3 {
			streaks++
			if len(examples) < 3 {
				examples = append(examples, fmt.Sprintf("steps %d-%d (%d list_files)", runStart, steps[i-1].index, run))
			}
		}
		run = 0
	}
	if run >= 3 {
		streaks++
		if len(examples) < 3 {
			examples = append(examples, fmt.Sprintf("steps %d-%d (%d list_files)", runStart, steps[len(steps)-1].index, run))
		}
	}
	if streaks > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "dir_walk_streak",
			Count:       streaks,
			Description: "≥3 consecutive list_files calls without a content tool between them",
			Examples:    examples,
		})
	}
}

func detectZeroHitSearch(rep *PerfReport, records []Record) {
	// A search_files or search_content result with "no matches" shape.
	// These are often invented filenames — the interesting signal is
	// that the model guessed instead of searching content.
	needles := []string{"no file matches", "no matches", "no content matches", "no go symbols matched", "no c++ symbols matched"}
	count := 0
	var examples []string
	for _, rec := range records {
		if rec.Type != "tool_result" {
			continue
		}
		tool, _ := rec.Data["tool"].(string)
		summary, _ := rec.Data["summary"].(string)
		lowered := strings.ToLower(summary)
		for _, n := range needles {
			if strings.Contains(lowered, n) {
				count++
				if len(examples) < 3 {
					examples = append(examples, fmt.Sprintf("%s: %s", tool, summary))
				}
				break
			}
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "zero_hit_search",
			Count:       count,
			Description: "search_files/search_content returned no results — model guessed an identifier or filename",
			Examples:    examples,
		})
	}
}

func detectTruncatedSearch(rep *PerfReport, records []Record) {
	count := 0
	var examples []string
	for _, rec := range records {
		if rec.Type != "tool_result" {
			continue
		}
		summary, _ := rec.Data["summary"].(string)
		if strings.Contains(strings.ToLower(summary), "showing first") {
			count++
			if len(examples) < 3 {
				examples = append(examples, summary)
			}
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "search_content_truncated",
			Count:       count,
			Description: "search_content returned more hits than the display cap — pattern was too broad",
			Examples:    examples,
		})
	}
}

func detectRedirectStorm(rep *PerfReport, records []Record) {
	redirects := 0
	for _, rec := range records {
		role, _ := rec.Data["role"].(string)
		if role == "discovery_redirect" {
			redirects++
		}
	}
	if redirects >= 2 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "redirect_storm",
			Count:       redirects,
			Description: "multiple discovery_redirect events — model kept reaching for raw read_file on code files",
		})
	}
}

func detectSameToolRepeat(rep *PerfReport, steps []toolStep) {
	counts := make(map[string]int)
	for _, s := range steps {
		key := s.tool + "|" + s.rawArgs
		counts[key]++
	}
	worst := 0
	var worstKey string
	for k, c := range counts {
		if c > worst {
			worst = c
			worstKey = k
		}
	}
	if worst >= 3 {
		display := worstKey
		if len(display) > 80 {
			display = display[:80] + "..."
		}
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "same_tool_repeat",
			Count:       worst,
			Description: "one tool-call fingerprint was submitted ≥3 times in the turn",
			Examples:    []string{display},
		})
	}
}

func detectNamedGapReadWalk(rep *PerfReport, steps []toolStep) {
	type readWalk struct {
		count int
		first int
		last  int
	}
	walks := make(map[string]readWalk)
	for _, s := range steps {
		if s.tool != "read_file" {
			continue
		}
		gap := perfRawJSONStringField(s.rawArgs, "named_gap")
		if gap == "" {
			continue
		}
		path := firstNonEmptyPerfString(perfRawJSONStringField(s.rawArgs, "path"), s.target)
		if path == "" {
			continue
		}
		key := path + "\x00" + gap
		walk := walks[key]
		if walk.count == 0 {
			walk.first = s.index
		}
		walk.count++
		walk.last = s.index
		walks[key] = walk
	}
	total := 0
	var examples []string
	for key, walk := range walks {
		if walk.count < 3 {
			continue
		}
		total += walk.count
		if len(examples) < 3 {
			parts := strings.SplitN(key, "\x00", 2)
			path := parts[0]
			gap := ""
			if len(parts) == 2 {
				gap = parts[1]
			}
			examples = append(examples, fmt.Sprintf("steps %d-%d: %d reads of %s for named_gap=%s", walk.first, walk.last, walk.count, path, gap))
		}
	}
	if total > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "named_gap_read_walk",
			Count:       total,
			Description: "read_file walked the same path for the same named_gap ≥3 times; the model kept reopening one edit-safety question instead of closing or blocking",
			Examples:    examples,
		})
	}
}

func detectDocWander(rep *PerfReport, steps []toolStep) {
	// Heuristic: 3+ read_file calls on .md / README-ish paths with
	// <=1 read on source. Indicates the model answered from docs
	// instead of looking at the code.
	docs := 0
	src := 0
	var docExamples []string
	for _, s := range steps {
		if s.tool != "read_file" {
			continue
		}
		t := strings.ToLower(s.target)
		if strings.HasSuffix(t, ".md") || strings.Contains(t, "readme") || strings.HasSuffix(t, ".txt") {
			docs++
			if len(docExamples) < 3 {
				docExamples = append(docExamples, fmt.Sprintf("step %d: %s", s.index, s.target))
			}
		} else {
			src++
		}
	}
	if docs >= 3 && src <= 1 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "doc_wander",
			Count:       docs,
			Description: "3+ doc reads with ≤1 source read — the answer is coming from docs, not the code",
			Examples:    docExamples,
		})
	}
}

func detectPromptSurfaceOverlap(rep *PerfReport, records []Record) {
	count := 0
	var examples []string
	addExample := func(example string) {
		count++
		if len(examples) < 3 {
			examples = append(examples, example)
		}
	}
	for _, rec := range records {
		switch rec.Type {
		case "session_start", "context_update":
			sources := stringSliceField(rec.Data, "context_sources")
			if len(sources) >= 8 {
				addExample(fmt.Sprintf("%s has %d prompt/context sources", rec.Type, len(sources)))
			}
		case "message":
			role := stringField(rec.Data, "role")
			if role != "control_rails" {
				continue
			}
			content := stringField(rec.Data, "content")
			var payload struct {
				DynamicInstructions bool `json:"dynamic_instruction_attachment"`
			}
			if json.Unmarshal([]byte(content), &payload) == nil && payload.DynamicInstructions {
				addExample("control_rails reports dynamic instruction attachment enabled")
			}
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "prompt_surface_overlap",
			Count:       count,
			Description: "large or dynamic prompt surfaces can cause overlapping guidance; audit ownership before adding more prompt text",
			Examples:    examples,
		})
	}
}

// detectMalformedEditArgs flags the specific failure shape that live
// logs surface: edit_file called with invalid line ranges (start > end)
// or missing old_text. Neither fails pre-execute guards (the request
// parses fine), but workspace execution errors out. Observed in a live
// run as a 60+ tool-call stall on a single doc-edit task.
func detectMalformedEditArgs(rep *PerfReport, records []Record) {
	needles := []string{
		"start_line must be",
		"old_text is required",
		"start_line/end_line",
	}
	count := 0
	var examples []string
	for _, rec := range records {
		if rec.Type != "tool_result" {
			continue
		}
		tool, _ := rec.Data["tool"].(string)
		if tool != "edit_file" && tool != "write_file" && tool != "append_file" {
			continue
		}
		errMsg, _ := rec.Data["error"].(string)
		if errMsg == "" {
			continue
		}
		lowered := strings.ToLower(errMsg)
		matched := false
		for _, n := range needles {
			if strings.Contains(lowered, n) {
				matched = true
				break
			}
		}
		if matched {
			count++
			if len(examples) < 3 {
				examples = append(examples, fmt.Sprintf("%s: %s", tool, firstLine(errMsg)))
			}
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "malformed_edit_args",
			Count:       count,
			Description: "edit_file failed with invalid line ranges or missing required fields — model's tool call shape was wrong, not a guard rejection",
			Examples:    examples,
		})
	}
}

// detectToolErrorStreak flags ≥3 consecutive tool_results with
// non-empty errors on the same (tool, path-stem). Distinct from
// rejection_retry: these are tool-execution failures, not guard
// rejections, and the model's args vary between attempts. The signal
// is "model is stuck trying to do something to X and failing each
// time with different broken inputs."
func detectToolErrorStreak(rep *PerfReport, steps []toolStep, records []Record) {
	// We need the path stem per step; derive from action target.
	var examples []string
	streaks := 0
	run := 0
	runStart := 0
	lastKey := ""
	for i, s := range steps {
		if !s.hasError || s.isReject {
			if run >= 3 {
				streaks++
				if len(examples) < 3 {
					examples = append(examples, fmt.Sprintf("steps %d-%d (%d failures on %s)", runStart, steps[i-1].index, run, lastKey))
				}
			}
			run = 0
			lastKey = ""
			continue
		}
		key := s.tool + "|" + pathStemOf(s.target)
		if key == lastKey && key != "" {
			run++
		} else {
			run = 1
			runStart = s.index
			lastKey = key
		}
	}
	if run >= 3 {
		streaks++
		if len(examples) < 3 {
			examples = append(examples, fmt.Sprintf("steps %d-%d (%d failures on %s)", runStart, steps[len(steps)-1].index, run, lastKey))
		}
	}
	if streaks > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "tool_error_streak",
			Count:       streaks,
			Description: "≥3 consecutive tool failures on the same tool+target — model is churning on invalid args or a stuck state",
			Examples:    examples,
		})
	}
}

func pathStemOf(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func detectRejectionRetry(rep *PerfReport, steps []toolStep) {
	// How often did a rejected tool get retried with identical args
	// immediately after? That's a pure waste signal: the model
	// ignored the rejection message.
	retries := 0
	var examples []string
	for i := 1; i < len(steps); i++ {
		if steps[i-1].isReject && steps[i].rawArgs == steps[i-1].rawArgs {
			retries++
			if len(examples) < 3 {
				examples = append(examples, fmt.Sprintf("step %d retried %s after rejection", steps[i].index, steps[i].tool))
			}
		}
	}
	if retries > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "rejection_retry",
			Count:       retries,
			Description: "rejected tool call was retried with identical arguments on the next step",
			Examples:    examples,
		})
	}
}

func detectCompletionGateReopenedDiscovery(rep *PerfReport, records []Record) {
	reopened := 0
	var examples []string
	armedStep := 0
	for _, rec := range records {
		switch rec.Type {
		case "tool_result":
			tool := stringField(rec.Data, "tool")
			if tool == "completion_gate" && completionGateNoWriteRejection(rec.Data) {
				armedStep = intField(rec.Data, "step")
				if armedStep == 0 {
					armedStep = 1
				}
				continue
			}
			if armedStep > 0 && workspaceWriteToolName(tool) && stringField(rec.Data, "error") == "" {
				armedStep = 0
			}
		case "tool_request":
			if armedStep == 0 {
				continue
			}
			tool := stringField(rec.Data, "tool")
			if workspaceWriteToolName(tool) {
				armedStep = 0
				continue
			}
			if !toolLooksLikeReadOrDiscovery(tool) {
				continue
			}
			reopened++
			if len(examples) < 3 {
				target := firstNonEmptyPerfString(stringField(rec.Data, "path"), targetFromAction(stringField(rec.Data, "action")), stringField(rec.Data, "tool"))
				examples = append(examples, fmt.Sprintf("after completion gate step %d: %s %s", armedStep, tool, target))
			}
			armedStep = 0
		}
	}
	if reopened > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "completion_gate_reopened_discovery",
			Count:       reopened,
			Description: "completion gate rejected a no-write final answer, then the next model action reopened read/discovery instead of writing or blocking",
			Examples:    examples,
		})
	}
}

func detectRequiredPathsBlockedWithoutWrite(rep *PerfReport, records []Record) {
	count := 0
	var examples []string
	for _, rec := range records {
		if rec.Type != "completion_status" {
			continue
		}
		reason := strings.ToLower(firstNonEmptyPerfString(stringField(rec.Data, "reason"), stringField(rec.Data, "blocker_kind")))
		if !strings.Contains(reason, "required_edit_paths_remaining") {
			continue
		}
		if boolField(rec.Data, "edit_attempted") {
			continue
		}
		count++
		if len(examples) < 3 {
			examples = append(examples, fmt.Sprintf("session blocked with %s and edit_attempted=false", reason))
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "required_paths_blocked_without_write",
			Count:       count,
			Description: "implementation run ended with required edit paths remaining and no recorded edit attempt",
			Examples:    examples,
		})
	}
}

func completionGateNoWriteRejection(data map[string]any) bool {
	text := strings.ToLower(strings.TrimSpace(
		stringField(data, "summary") + " " +
			stringField(data, "error") + " " +
			stringField(data, "content"),
	))
	return strings.Contains(text, "no successful write") ||
		strings.Contains(text, "workspace change") ||
		strings.Contains(text, "required edit path") ||
		strings.Contains(text, "remain unwritten")
}

func workspaceWriteToolName(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "edit_file", "write_file", "append_file", "apply_patch", "delegate_edit", "mkdir", "submit_plan":
		return true
	default:
		return false
	}
}

func toolLooksLikeReadOrDiscovery(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	switch tool {
	case "read_file", "list_files", "search_files", "search_content",
		"code_navigate", "structural_search", "dependency_graph",
		"history_range", "history_symbol", "blame_range",
		"ctags_symbols", "semgrep_scan", "joern_scan",
		"lookup", "trace_code", "gdscript_trace", "boundary",
		"ast_grep":
		return true
	}
	if strings.HasPrefix(tool, "context_") {
		return true
	}
	for _, suffix := range []string{
		"_overview",
		"_entrypoints",
		"_find_symbol",
		"_workspace_symbol",
		"_definition",
		"_references",
		"_calls_from",
		"_calls_into",
		"_read_symbol",
		"_related_files",
		"_implementations",
		"_diagnostics",
		"_file_outline",
	} {
		if strings.HasSuffix(tool, suffix) {
			return true
		}
	}
	return false
}

func firstNonEmptyPerfString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func perfRawJSONStringField(raw string, key string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || key == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	return stringField(payload, key)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

// ---- aggregation ----

// PerfAggregate summarizes a slice of PerfReports. It's the lens used
// to find the next optimization target: which anti-pattern shows up in
// the most logs? Which tool dominates the mix? Median/max numbers
// surface tails without anecdotal cherry-picking.
type PerfAggregate struct {
	LogCount             int                    `json:"log_count"`
	MedianToolResults    int                    `json:"median_tool_results"`
	MaxToolResults       int                    `json:"max_tool_results"`
	MedianPromptTokens   int                    `json:"median_prompt_tokens"`
	MaxPromptTokens      int                    `json:"max_prompt_tokens"`
	MedianDurationMillis int                    `json:"median_duration_millis"`
	TotalRejectedActions int                    `json:"total_rejected_actions"`
	PatternHitRate       []PerfPatternHit       `json:"pattern_hit_rate"`
	ToolMix              []PerfToolMix          `json:"tool_mix"`
	Context              *ContextStats          `json:"context,omitempty"`
	Feedback             *FeedbackStats         `json:"feedback,omitempty"`
	LocalOps             *LocalOpsStats         `json:"local_ops,omitempty"`
	StopReasonCounts     map[string]int         `json:"stop_reason_counts,omitempty"`
	WorstOffenders       []PerfWorstOffender    `json:"worst_offenders,omitempty"`
	Extras               map[string]interface{} `json:"extras,omitempty"`
}

type PerfPatternHit struct {
	Name       string  `json:"name"`
	LogsHit    int     `json:"logs_hit"`
	TotalCount int     `json:"total_count"`
	HitRate    float64 `json:"hit_rate"`
}

type PerfToolMix struct {
	Tool      string  `json:"tool"`
	Calls     int     `json:"calls"`
	Share     float64 `json:"share"`
	AvgPerLog float64 `json:"avg_per_log"`
}

type PerfWorstOffender struct {
	LogPath            string `json:"log_path"`
	SessionID          string `json:"session_id,omitempty"`
	AntiPatternScore   int    `json:"anti_pattern_score"`
	ToolResults        int    `json:"tool_results"`
	LogExcerptRedacted bool   `json:"log_excerpt_redacted,omitempty"`
}

// Aggregate combines per-log reports into one summary.
func Aggregate(reports []PerfReport) PerfAggregate {
	agg := PerfAggregate{
		LogCount:         len(reports),
		StopReasonCounts: make(map[string]int),
	}
	if len(reports) == 0 {
		return agg
	}

	toolResults := make([]int, 0, len(reports))
	// Many older logs predate the budget_ledger event — they have no
	// token or duration data. Feeding zeros into the medians would
	// pull them toward zero and mask the actual distribution, so we
	// skip zeros for those two channels.
	promptTokens := make([]int, 0, len(reports))
	durations := make([]int, 0, len(reports))
	patternLogs := make(map[string]int)
	patternCounts := make(map[string]int)
	toolCalls := make(map[string]int)
	totalCalls := 0
	for _, r := range reports {
		toolResults = append(toolResults, r.ToolResults)
		if r.PromptTokens > 0 {
			promptTokens = append(promptTokens, r.PromptTokens)
		}
		if r.DurationMillis > 0 {
			durations = append(durations, r.DurationMillis)
		}
		agg.TotalRejectedActions += r.RejectedActions
		if r.StopReason != "" {
			agg.StopReasonCounts[r.StopReason]++
		}
		seen := make(map[string]bool)
		for _, ap := range r.AntiPatterns {
			patternCounts[ap.Name] += ap.Count
			if !seen[ap.Name] {
				seen[ap.Name] = true
				patternLogs[ap.Name]++
			}
		}
		for tool, calls := range r.Tools.ByName {
			toolCalls[tool] += calls
			totalCalls += calls
		}
		mergeContextStats(&agg.Context, r.Context)
		mergeFeedbackStats(&agg.Feedback, r.Feedback)
		mergeLocalOpsStats(&agg.LocalOps, r.LocalOps)
	}
	agg.MedianToolResults = median(toolResults)
	agg.MaxToolResults = maxOf(toolResults)
	agg.MedianPromptTokens = median(promptTokens)
	agg.MaxPromptTokens = maxOf(promptTokens)
	agg.MedianDurationMillis = median(durations)

	for name, logsHit := range patternLogs {
		agg.PatternHitRate = append(agg.PatternHitRate, PerfPatternHit{
			Name:       name,
			LogsHit:    logsHit,
			TotalCount: patternCounts[name],
			HitRate:    float64(logsHit) / float64(len(reports)),
		})
	}
	sort.Slice(agg.PatternHitRate, func(i, j int) bool {
		return agg.PatternHitRate[i].LogsHit > agg.PatternHitRate[j].LogsHit
	})

	for tool, calls := range toolCalls {
		share := 0.0
		if totalCalls > 0 {
			share = float64(calls) / float64(totalCalls)
		}
		agg.ToolMix = append(agg.ToolMix, PerfToolMix{
			Tool:      tool,
			Calls:     calls,
			Share:     share,
			AvgPerLog: float64(calls) / float64(len(reports)),
		})
	}
	sort.Slice(agg.ToolMix, func(i, j int) bool {
		return agg.ToolMix[i].Calls > agg.ToolMix[j].Calls
	})

	// Worst-offender list: top 5 logs by anti-pattern score.
	offenders := append([]PerfReport(nil), reports...)
	sort.Slice(offenders, func(i, j int) bool {
		return offenders[i].AntiPatternScore > offenders[j].AntiPatternScore
	})
	keep := 5
	if len(offenders) < keep {
		keep = len(offenders)
	}
	for _, r := range offenders[:keep] {
		if r.AntiPatternScore == 0 {
			break
		}
		agg.WorstOffenders = append(agg.WorstOffenders, PerfWorstOffender{
			LogPath:            r.LogPath,
			SessionID:          r.SessionID,
			AntiPatternScore:   r.AntiPatternScore,
			ToolResults:        r.ToolResults,
			LogExcerptRedacted: r.UserPrompt != "",
		})
	}

	return agg
}

func mergeContextStats(target **ContextStats, source *ContextStats) {
	if source == nil {
		return
	}
	if *target == nil {
		*target = &ContextStats{
			ActiveBackendCounts:         make(map[string]int),
			ActiveBackendStatusCounts:   make(map[string]int),
			StoreKindCounts:             make(map[string]int),
			StorageBackendStatusCounts:  make(map[string]int),
			CollectorReportNameCounts:   make(map[string]int),
			CollectorCoverageNameCounts: make(map[string]int),
			AutoRefreshReasonCounts:     make(map[string]int),
			AutoRefreshSkippedCounts:    make(map[string]int),
			FreshnessPolicyCounts:       make(map[string]int),
			FreshnessReasonCounts:       make(map[string]int),
			StaleUseStateCounts:         make(map[string]int),
		}
	}
	dst := *target
	dst.ToolResults += source.ToolResults
	dst.IndexedResults += source.IndexedResults
	dst.FreshResults += source.FreshResults
	dst.StaleResults += source.StaleResults
	dst.IncompatibleBackendResults += source.IncompatibleBackendResults
	dst.TraceFailures += source.TraceFailures
	dst.MaxFilesIndexed = max(dst.MaxFilesIndexed, source.MaxFilesIndexed)
	dst.MaxSymbolsIndexed = max(dst.MaxSymbolsIndexed, source.MaxSymbolsIndexed)
	dst.MaxEdgesIndexed = max(dst.MaxEdgesIndexed, source.MaxEdgesIndexed)
	dst.MaxStaleSymbols = max(dst.MaxStaleSymbols, source.MaxStaleSymbols)
	dst.RefreshedStaleSymbols += source.RefreshedStaleSymbols
	dst.StaleSymbolSamples += source.StaleSymbolSamples
	dst.IncrementalRefreshes += source.IncrementalRefreshes
	dst.ReusedIndexedFiles += source.ReusedIndexedFiles
	dst.DependencyRefreshFiles += source.DependencyRefreshFiles
	dst.AutoRefreshAttempts += source.AutoRefreshAttempts
	dst.AutoRefreshSkips += source.AutoRefreshSkips
	dst.AutoRefreshFailures += source.AutoRefreshFailures
	dst.AutoRefreshFreshAfter += source.AutoRefreshFreshAfter
	dst.AutoRefreshFilesIndexed += source.AutoRefreshFilesIndexed
	dst.AutoRefreshRefreshedFiles += source.AutoRefreshRefreshedFiles
	dst.FreshnessReadThroughResults += source.FreshnessReadThroughResults
	dst.FreshnessBlockers += source.FreshnessBlockers
	dst.FreshnessBudgetExceeded += source.FreshnessBudgetExceeded
	dst.FreshnessRefreshAttempts += source.FreshnessRefreshAttempts
	dst.FreshnessRefreshFailures += source.FreshnessRefreshFailures
	dst.FreshnessRefreshFilesIndexed += source.FreshnessRefreshFilesIndexed
	dst.StaleUseBlocked += source.StaleUseBlocked
	dst.StaleUseReported += source.StaleUseReported
	dst.StaleUseAllowed += source.StaleUseAllowed
	dst.StaleUseFresh += source.StaleUseFresh
	dst.NodesReturned += source.NodesReturned
	dst.EdgesReturned += source.EdgesReturned
	dst.SummaryNodesReturned += source.SummaryNodesReturned
	dst.ModelSummaryNodesReturned += source.ModelSummaryNodesReturned
	dst.CandidateFiles += source.CandidateFiles
	dst.CandidateSymbols += source.CandidateSymbols
	dst.ImpactScope += source.ImpactScope
	dst.TestsSuggested += source.TestsSuggested
	dst.UnresolvedGaps += source.UnresolvedGaps
	dst.CollectorReports += source.CollectorReports
	dst.CollectorCoverage += source.CollectorCoverage
	dst.CollectorUnavailable += source.CollectorUnavailable
	dst.CollectorWarnings += source.CollectorWarnings
	dst.CollectorVersionMismatches += source.CollectorVersionMismatches
	dst.EnrichmentDiagnostics += source.EnrichmentDiagnostics
	dst.NormalizedRelationshipEdges += source.NormalizedRelationshipEdges
	mergeStringCounts(dst.ActiveBackendCounts, source.ActiveBackendCounts)
	mergeStringCounts(dst.ActiveBackendStatusCounts, source.ActiveBackendStatusCounts)
	mergeStringCounts(dst.StoreKindCounts, source.StoreKindCounts)
	mergeStringCounts(dst.StorageBackendStatusCounts, source.StorageBackendStatusCounts)
	mergeStringCounts(dst.CollectorReportNameCounts, source.CollectorReportNameCounts)
	mergeStringCounts(dst.CollectorCoverageNameCounts, source.CollectorCoverageNameCounts)
	mergeStringCounts(dst.AutoRefreshReasonCounts, source.AutoRefreshReasonCounts)
	mergeStringCounts(dst.AutoRefreshSkippedCounts, source.AutoRefreshSkippedCounts)
	mergeStringCounts(dst.FreshnessPolicyCounts, source.FreshnessPolicyCounts)
	mergeStringCounts(dst.FreshnessReasonCounts, source.FreshnessReasonCounts)
	mergeStringCounts(dst.StaleUseStateCounts, source.StaleUseStateCounts)
}

func mergeFeedbackStats(target **FeedbackStats, source *FeedbackStats) {
	if source == nil {
		return
	}
	if *target == nil {
		*target = newFeedbackStats()
	}
	dst := *target
	dst.Total += source.Total
	dst.Verified += source.Verified
	dst.Inferred += source.Inferred
	dst.HighSeverity += source.HighSeverity
	dst.MediumSeverity += source.MediumSeverity
	dst.LowSeverity += source.LowSeverity
	dst.EvidenceItems += source.EvidenceItems
	dst.PlanningQualityTotal += source.PlanningQualityTotal
	mergeStringCounts(dst.ByCategory, source.ByCategory)
	mergeStringCounts(dst.ByComponent, source.ByComponent)
	mergeStringCounts(dst.ByConfidence, source.ByConfidence)
	mergeStringCounts(dst.BySeverity, source.BySeverity)
	mergeStringCounts(dst.ByPlanningCategory, source.ByPlanningCategory)
}

func mergeLocalOpsStats(target **LocalOpsStats, source *LocalOpsStats) {
	if source == nil {
		return
	}
	if *target == nil {
		*target = newLocalOpsStats()
	}
	dst := *target
	dst.BackendEvents += source.BackendEvents
	dst.BackendConfigured += source.BackendConfigured
	dst.QueueEvents += source.QueueEvents
	dst.QueueDegraded += source.QueueDegraded
	dst.QueueMaxWaitMillis = max(dst.QueueMaxWaitMillis, source.QueueMaxWaitMillis)
	dst.ContextBudgetEvents += source.ContextBudgetEvents
	dst.ContextBudgetDegraded += source.ContextBudgetDegraded
	dst.ClassifierEvents += source.ClassifierEvents
	dst.ClassifierDegraded += source.ClassifierDegraded
	dst.ImplementCloseouts += source.ImplementCloseouts
	dst.DoctorReadinessProfiles += source.DoctorReadinessProfiles
	dst.DoctorDegradedProfiles += source.DoctorDegradedProfiles
	dst.DoctorBlockedProfiles += source.DoctorBlockedProfiles
	mergeStringCounts(dst.ByQueueState, source.ByQueueState)
	mergeStringCounts(dst.ByContextBudgetState, source.ByContextBudgetState)
	mergeStringCounts(dst.ByClassifierState, source.ByClassifierState)
	mergeStringCounts(dst.ByBackendState, source.ByBackendState)
	mergeStringCounts(dst.ByReadinessProfileState, source.ByReadinessProfileState)
}

func mergeStringCounts(target map[string]int, source map[string]int) {
	for key, value := range source {
		target[key] += value
	}
}

func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func maxOf(values []int) int {
	best := 0
	for _, v := range values {
		if v > best {
			best = v
		}
	}
	return best
}
