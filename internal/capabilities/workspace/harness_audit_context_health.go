package workspace

import (
	"context"
	"fmt"
	"strings"

	"idiom/internal/evidence/eval"
	"idiom/internal/repository/comprehension"
)

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
	CurrentContractRows                   int            `json:"current_contract_rows"`
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
	// Token/cost savings metrics (Atom 6).
	EstimatedFullTokensSaved          int64 `json:"estimated_full_tokens_saved"`
	EstimatedRediscoveryTokensAvoided int64 `json:"estimated_rediscovery_tokens_avoided"`
}

type harnessAuditFreshnessHealth struct {
	ReadThroughAttempts              int64            `json:"read_through_attempts"`
	Blockers                         int64            `json:"blockers"`
	BlockersByReason                 map[string]int64 `json:"blockers_by_reason,omitempty"`
	StaleUseBlocked                  int64            `json:"stale_use_blocked"`
	ResolvedStaleUseBlocked          int64            `json:"resolved_stale_use_blocked,omitempty"`
	StaleUseReported                 int64            `json:"stale_use_reported"`
	StaleUseAllowed                  int64            `json:"stale_use_allowed"`
	StaleUseFresh                    int64            `json:"stale_use_fresh"`
	StaleUseStateCounts              map[string]int64 `json:"stale_use_state_counts,omitempty"`
	StaleCachedViewPreventions       int64            `json:"stale_cached_view_preventions"`
	StaleRowMisses                   int64            `json:"stale_row_misses"`
	RefreshFailures                  int64            `json:"refresh_failures"`
	RootRebuilds                     int64            `json:"root_rebuilds"`
	AllowStaleInvocations            int64            `json:"allow_stale_invocations"`
	TelemetryReadThrough             int64            `json:"telemetry_read_through_results,omitempty"`
	TelemetryBlockers                int64            `json:"telemetry_blockers,omitempty"`
	TelemetryBudgetExceeded          int64            `json:"telemetry_budget_exceeded,omitempty"`
	TelemetryRefreshAttempts         int64            `json:"telemetry_refresh_attempts,omitempty"`
	TelemetryRefreshFailures         int64            `json:"telemetry_refresh_failures,omitempty"`
	TelemetryResolvedRefreshFailures int64            `json:"telemetry_resolved_refresh_failures,omitempty"`
	TelemetryRefreshFiles            int64            `json:"telemetry_refresh_files_indexed,omitempty"`
	TelemetryAllowStale              int64            `json:"telemetry_allow_stale_invocations,omitempty"`
	TelemetryResolvedBlockers        int64            `json:"telemetry_resolved_blockers,omitempty"`
	TelemetryLatestState             string           `json:"telemetry_latest_state,omitempty"`
	TelemetryHistoricalOnly          bool             `json:"telemetry_historical_only,omitempty"`
	TelemetryPolicyCounts            map[string]int64 `json:"telemetry_policy_counts,omitempty"`
	TelemetryReasonCounts            map[string]int64 `json:"telemetry_reason_counts,omitempty"`
}

type harnessAuditFreshnessWindow struct {
	LatestState               string `json:"latest_state,omitempty"`
	UnresolvedBlockers        int    `json:"unresolved_blockers,omitempty"`
	UnresolvedStaleUse        int    `json:"unresolved_stale_use,omitempty"`
	UnresolvedRefreshFailures int    `json:"unresolved_refresh_failures,omitempty"`
	ResolvedBlockers          int    `json:"resolved_blockers,omitempty"`
	ResolvedStaleUse          int    `json:"resolved_stale_use,omitempty"`
	ResolvedRefreshFailures   int    `json:"resolved_refresh_failures,omitempty"`
	FreshResultsAfterBlocker  int    `json:"fresh_results_after_blocker,omitempty"`
}

// buildContextCacheAudit reads graph_views statistics from the
// comprehension SQLite store and combines them with the in-process
// hit/miss counters. Returns nil when no SQLite store exists yet (no
// `context_index` build has run), so an idle workspace doesn't surface
// a misleading "0 rows" finding.
func buildContextCacheAudit(ctx context.Context, root string) *harnessAuditContextCache {
	stats, err := comprehension.ReadViewStats(ctx, root)
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
		CurrentContractRows:                   stats.CurrentContractRows,
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
		EstimatedFullTokensSaved:              stats.EstimatedFullTokensSaved,
		EstimatedRediscoveryTokensAvoided:     stats.EstimatedRediscoveryTokensAvoided,
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

func buildFreshnessHealthAudit(logContext *eval.ContextStats, resolution *harnessAuditFreshnessWindow, telemetryHistoricalOnly bool) harnessAuditFreshnessHealth {
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
		health.TelemetryHistoricalOnly = telemetryHistoricalOnly
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

	if resolution != nil {
		health.TelemetryBlockers = int64(resolution.UnresolvedBlockers)
		health.TelemetryResolvedBlockers = int64(resolution.ResolvedBlockers)
		health.TelemetryRefreshFailures = int64(resolution.UnresolvedRefreshFailures)
		health.TelemetryResolvedRefreshFailures = int64(resolution.ResolvedRefreshFailures)
		health.StaleUseBlocked = int64(resolution.UnresolvedStaleUse)
		health.ResolvedStaleUseBlocked = int64(resolution.ResolvedStaleUse)
		health.TelemetryLatestState = resolution.LatestState
	}

	return health
}

func logBehaviorContextStats(logBehavior *harnessAuditLogBehavior) *eval.ContextStats {
	if logBehavior == nil {
		return nil
	}

	if logBehavior.CurrentAggregate.LogCount > 0 {
		return logBehavior.CurrentAggregate.Context
	}

	return logBehavior.Aggregate.Context
}

func logBehaviorContextStatsHistoricalOnly(logBehavior *harnessAuditLogBehavior) bool {
	return logBehavior != nil && logBehavior.CurrentAggregate.LogCount == 0 && logBehavior.Aggregate.LogCount > 0
}

func logBehaviorFreshnessResolution(logBehavior *harnessAuditLogBehavior) *harnessAuditFreshnessWindow {
	if logBehavior == nil {
		return nil
	}

	return logBehavior.FreshnessResolution
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

	telemetryAllowStale := health.TelemetryAllowStale
	telemetryRefreshFailures := health.TelemetryRefreshFailures

	if health.TelemetryHistoricalOnly {
		telemetryAllowStale = 0
		telemetryRefreshFailures = 0
	}

	if health.AllowStaleInvocations+telemetryAllowStale > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "freshness_allow_stale_invoked",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d in-process and %d current log-derived allow_stale freshness invocation(s) observed", health.AllowStaleInvocations, telemetryAllowStale),
			Recommendation: "reason=allow_stale_invoked model_facing_policies=read_through,require_fresh",
		})
	}

	if health.RefreshFailures+telemetryRefreshFailures > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "freshness_refresh_failures",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d in-process and %d current log-derived freshness refresh failure(s) observed", health.RefreshFailures, telemetryRefreshFailures),
			Recommendation: "reason=refresh_failed source=freshness_health",
		})
	}

	if !health.TelemetryHistoricalOnly && health.TelemetryBlockers > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "freshness_log_blockers",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d current log-derived freshness blocker(s) observed", health.TelemetryBlockers),
			Recommendation: "reason=freshness_blockers_observed source=log_behavior.current_aggregate.context",
		})
	}

	if !health.TelemetryHistoricalOnly && health.StaleUseBlocked > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "stale_use_blockers_observed",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d current log-derived stale-use blocker(s) observed", health.StaleUseBlocked),
			Recommendation: "reason=stale_use_blocked source=log_behavior.current_aggregate.context",
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

	if cache.CurrentRows > 0 && cache.CurrentContractRows < cache.CurrentRows {
		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_contract_gap",
			Severity:       "info",
			Evidence:       fmt.Sprintf("%d of %d current graph_views row(s) expose the graph-view result contract", cache.CurrentContractRows, cache.CurrentRows),
			Recommendation: fmt.Sprintf("reason=context_cache_contract_gap current_contract_rows=%d current_rows=%d", cache.CurrentContractRows, cache.CurrentRows),
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

	// Surface accumulated token/cost savings from compact projection and
	// cache-hit rediscovery avoidance. These counters are per-session
	// aggregates; the finding reports them so operators can observe whether
	// the caching and projection efficiency mechanisms are producing value.
	if cache.EstimatedFullTokensSaved > 0 || cache.EstimatedRediscoveryTokensAvoided > 0 {
		evidence := ""
		rec := ""

		if cache.EstimatedFullTokensSaved > 0 && cache.EstimatedRediscoveryTokensAvoided > 0 {
			evidence = fmt.Sprintf("compact projection saved ~%d tokens; cache hits avoided ~%d tokens of rediscovery", cache.EstimatedFullTokensSaved, cache.EstimatedRediscoveryTokensAvoided)
			rec = fmt.Sprintf("reason=context_cache_savings tokens_saved=%d rediscovery_avoided=%d hit_rate=%.2f", cache.EstimatedFullTokensSaved, cache.EstimatedRediscoveryTokensAvoided, cache.HitRate)
		} else if cache.EstimatedFullTokensSaved > 0 {
			evidence = fmt.Sprintf("compact projection saved ~%d tokens vs full JSON marshalling", cache.EstimatedFullTokensSaved)
			rec = fmt.Sprintf("reason=context_cache_savings tokens_saved=%d", cache.EstimatedFullTokensSaved)
		} else {
			evidence = fmt.Sprintf("cache hits avoided ~%d tokens of rediscovery", cache.EstimatedRediscoveryTokensAvoided)
			rec = fmt.Sprintf("reason=context_cache_savings rediscovery_avoided=%d hit_rate=%.2f", cache.EstimatedRediscoveryTokensAvoided, cache.HitRate)
		}

		findings = append(findings, harnessAuditFinding{
			ID:             "context_cache_savings",
			Severity:       "info",
			Evidence:       evidence,
			Recommendation: rec,
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
