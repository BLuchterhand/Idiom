package workspace

import (
	"encoding/json"
	"strings"
)

const contextTelemetryPathSampleLimit = 5

func ToolTelemetryFields(tool string, output string) map[string]any {
	fields := make(map[string]any)
	for key, value := range ContextTelemetryFields(tool, output) {
		fields[key] = value
	}

	for key, value := range HarnessFeedbackTelemetryFields(tool, output) {
		fields[key] = value
	}

	for key, value := range ReadOnlyCapabilityTelemetryFields(tool, output) {
		fields[key] = value
	}

	if len(fields) == 0 {
		return nil
	}

	return fields
}

func ContextTelemetryFields(tool string, output string) map[string]any {
	tool = strings.TrimSpace(tool)
	if (tool != ToolNameDiscoveryQuery && !strings.HasPrefix(tool, "context_")) || strings.TrimSpace(output) == "" {
		return nil
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}

	fields := make(map[string]any)
	copyTelemetryString(payload, fields, "index_id", "context_index_id")
	copyTelemetryString(payload, fields, "store_kind", "context_store_kind")
	copyTelemetryString(payload, fields, "resolution", "context_resolution")
	copyTelemetryString(payload, fields, "artifact_path", "context_artifact_path")
	copyTelemetryString(payload, fields, "collector_version", "context_collector_version")
	copyTelemetryString(payload, fields, "current_collector_version", "context_current_collector_version")
	copyTelemetryString(payload, fields, "expanded_scope", "context_expanded_scope")
	copyTelemetryString(payload, fields, "partial_refresh_reason", "context_partial_refresh_reason")
	copyTelemetryString(payload, fields, "full_rebuild_reason", "context_full_rebuild_reason")
	copyTelemetryBool(payload, fields, "fresh", "context_fresh")
	copyTelemetryBool(payload, fields, "indexed", "context_indexed")
	copyTelemetryBool(payload, fields, "success", "context_trace_success")
	copyTelemetryBool(payload, fields, "collector_version_mismatch", "context_collector_version_mismatch")
	copyTelemetryBool(payload, fields, "incremental", "context_incremental")
	copyTelemetryNumber(payload, fields, "files_indexed", "context_files_indexed")
	copyTelemetryNumber(payload, fields, "symbols_indexed", "context_symbols_indexed")
	copyTelemetryNumber(payload, fields, "edges_indexed", "context_edges_indexed")
	copyTelemetryNumber(payload, fields, "stale_symbols", "context_stale_symbols")
	copyTelemetryNumber(payload, fields, "stale_file_count", "context_stale_file_count")
	copyTelemetryNumber(payload, fields, "missing_file_count", "context_missing_file_count")
	copyTelemetryNumber(payload, fields, "stale_path_count", "context_stale_path_count")
	copyTelemetryNumber(payload, fields, "refreshed_stale_symbols", "context_refreshed_stale_symbols")
	copyTelemetryNumber(payload, fields, "reused_indexed_files", "context_reused_indexed_files")
	copyTelemetryArrayLen(payload, fields, "stale_files", "context_stale_files")
	copyTelemetryArrayLen(payload, fields, "stale_symbol_samples", "context_stale_symbol_samples")
	copyTelemetryArrayLen(payload, fields, "missing_files", "context_missing_files")
	copyTelemetryArrayLen(payload, fields, "dependency_refresh_files", "context_dependency_refresh_files")
	copyTelemetryArrayLen(payload, fields, "nodes", "context_nodes")
	copyTelemetryArrayLen(payload, fields, "edges", "context_edges")
	copyTelemetryArrayLen(payload, fields, "candidate_files", "context_candidate_files")
	copyTelemetryArrayLen(payload, fields, "candidate_symbols", "context_candidate_symbols")
	copyTelemetryArrayLen(payload, fields, "impact_scope", "context_impact_scope")
	copyTelemetryArrayLen(payload, fields, "tests", "context_tests")
	copyTelemetryArrayLen(payload, fields, "relationships", "context_relationships")
	copyTelemetryArrayLen(payload, fields, "collector_reports", "context_collector_reports")
	copyTelemetryArrayLen(payload, fields, "collector_coverage", "context_collector_coverage")
	copyTelemetryArrayLen(payload, fields, "events", "context_trace_events")
	copyTelemetryArrayLen(payload, fields, "executed_files", "context_trace_executed_files")
	copyTelemetryArrayLen(payload, fields, "failing_anchors", "context_trace_failing_anchors")
	copyTelemetryArrayLen(payload, fields, "unresolved_gaps", "context_unresolved_gaps")
	copyTelemetryArrayLen(payload, fields, "unresolved_gap_details", "context_unresolved_gap_details")
	addStorageBackendTelemetry(payload, fields)
	addCollectorReportTelemetry(payload, fields)
	addCollectorCoverageTelemetry(payload, fields)
	addContextAutoRefreshTelemetry(payload, fields)
	addContextFreshnessTelemetry(payload, fields)
	addContextStaleEvidenceTelemetry(payload, fields)
	addContextCacheTelemetry(payload, fields)
	addContextConfidenceTelemetry(payload, fields)
	addContextProvenanceTelemetry(payload, fields)
	addContextNodeKindTelemetry(payload, fields)

	if len(fields) == 0 {
		return nil
	}

	return fields
}

func HarnessFeedbackTelemetryFields(tool string, output string) map[string]any {
	tool = strings.TrimSpace(tool)
	if tool != "harness_feedback" && tool != "idiom_feedback" {
		return nil
	}

	if strings.TrimSpace(output) == "" {
		return nil
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}

	fields := make(map[string]any)
	copyTelemetryString(payload, fields, "feedback_id", "feedback_id")
	copyTelemetryString(payload, fields, "category", "feedback_category")
	copyTelemetryString(payload, fields, "component", "feedback_component")
	copyTelemetryString(payload, fields, "confidence", "feedback_confidence")
	copyTelemetryString(payload, fields, "severity", "feedback_severity")
	copyTelemetryString(payload, fields, "path", "feedback_path")
	copyTelemetryString(payload, fields, "task_id", "feedback_task_id")
	copyTelemetryStringLimited(payload, fields, "expected", "feedback_expected", 500)
	copyTelemetryStringLimited(payload, fields, "observed", "feedback_observed", 500)
	copyTelemetryStringLimited(payload, fields, "workaround", "feedback_workaround", 500)
	copyTelemetryStringLimited(payload, fields, "impact", "feedback_impact", 500)
	copyTelemetryStringLimited(payload, fields, "suggested_fix", "feedback_suggested_fix", 500)
	copyTelemetryArrayLen(payload, fields, "evidence", "feedback_evidence_count")

	if len(fields) == 0 {
		return nil
	}

	return fields
}

func ReadOnlyCapabilityTelemetryFields(tool string, output string) map[string]any {
	tool = strings.TrimSpace(tool)
	if tool != readOnlyCapabilityToolName || strings.TrimSpace(output) == "" {
		return nil
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}

	fields := make(map[string]any)
	copyTelemetryString(payload, fields, "schema_version", "capability_schema_version")
	copyTelemetryString(payload, fields, "requested_capability", "capability_requested")
	copyTelemetryString(payload, fields, "capability", "capability_resolved")
	copyTelemetryBool(payload, fields, "routed", "capability_routed")
	copyTelemetryString(payload, fields, "routed_tool", "capability_routed_tool")
	copyTelemetryString(payload, fields, "fallback_reason", "capability_fallback_source_reason")
	copyTelemetryBool(payload, fields, "resolver_runtime_enabled", "capability_resolver_runtime_enabled")
	copyTelemetryBool(payload, fields, "direct_tool_fallback", "capability_direct_tool_fallback")
	copyTelemetryArrayLen(payload, fields, "available_adapters", "capability_available_adapters")
	copyTelemetryArrayLen(payload, fields, "unavailable_adapters", "capability_unavailable_adapters")

	status, ok := payload["status"].(map[string]any)
	if ok {
		addReadOnlyCapabilityStatusTelemetry(status, fields)
	} else {
		addReadOnlyCapabilityDerivedStatusTelemetry(payload, fields)
	}

	if fields["capability_fallback_reason"] == "capability_unmapped" {
		fields["capability_unmapped"] = true
	}

	if fields["capability_fallback_reason"] == "capability_deferred" {
		fields["capability_deferred"] = true
	}

	if fields["capability_fallback_state"] != nil && fields["capability_fallback_state"] != "none" {
		fields["capability_fallback"] = true
	}

	if len(fields) == 0 {
		return nil
	}

	return fields
}

func addReadOnlyCapabilityStatusTelemetry(status map[string]any, fields map[string]any) {
	copyTelemetryString(status, fields, "state", "capability_status_state")
	copyTelemetryString(status, fields, "precision_tier", "capability_precision_tier")
	copyTelemetryString(status, fields, "confidence", "capability_confidence")
	copyTelemetryArrayLen(status, fields, "unresolved_gaps", "capability_unresolved_gaps")

	if selected, ok := status["selected_adapter"].(map[string]any); ok {
		copyTelemetryString(selected, fields, "tool", "capability_selected_adapter")
		copyTelemetryString(selected, fields, "capability", "capability_selected_adapter_capability")
		copyTelemetryString(selected, fields, "language", "capability_selected_adapter_language")
		copyTelemetryString(selected, fields, "source", "capability_selected_adapter_source")
		copyTelemetryString(selected, fields, "provenance", "capability_selected_adapter_provenance")
	}

	if fallback, ok := status["fallback"].(map[string]any); ok {
		copyTelemetryString(fallback, fields, "state", "capability_fallback_state")
		copyTelemetryString(fallback, fields, "reason", "capability_fallback_reason")
		copyTelemetryString(fallback, fields, "source_reason", "capability_fallback_source_reason")
	}

	if absence, ok := status["absence_confidence"].(map[string]any); ok {
		copyTelemetryString(absence, fields, "level", "capability_absence_confidence")
		copyTelemetryString(absence, fields, "reason", "capability_absence_reason")
		copyTelemetryString(absence, fields, "precision_tier", "capability_absence_precision_tier")
		copyTelemetryString(absence, fields, "source", "capability_absence_source")
	}
}

func addReadOnlyCapabilityDerivedStatusTelemetry(payload map[string]any, fields map[string]any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	var decoded ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal(data, &decoded); err != nil {
		return
	}

	underlying := readOnlyCapabilityUnderlyingAsToolResult(decoded.UnderlyingResult)
	fallback := readOnlyCapabilityFallbackStatusFor(decoded.FallbackReason)
	absence := readOnlyCapabilityAbsenceConfidenceFor(underlying)
	precisionTier := readOnlyCapabilityStatusPrecisionTier(underlying, decoded.UnderlyingResult)
	statusState := readOnlyCapabilityStatusState(decoded.Routed, fallback, absence)

	if statusState != "" {
		fields["capability_status_state"] = statusState
	}

	if precisionTier != "" {
		fields["capability_precision_tier"] = precisionTier
	}

	if confidence := readOnlyCapabilityRouteConfidence(precisionTier, fallback, absence, underlying); confidence != "" {
		fields["capability_confidence"] = confidence
	}

	if selected := readOnlyCapabilitySelectedAdapterStatus(decoded.RoutedAdapter); selected != nil {
		fields["capability_selected_adapter"] = selected.Tool
		if selected.Capability != "" {
			fields["capability_selected_adapter_capability"] = selected.Capability
		}

		if selected.Language != "" {
			fields["capability_selected_adapter_language"] = selected.Language
		}

		if selected.Source != "" {
			fields["capability_selected_adapter_source"] = selected.Source
		}

		if selected.Provenance != "" {
			fields["capability_selected_adapter_provenance"] = selected.Provenance
		}
	}

	if fallback.State != "" {
		fields["capability_fallback_state"] = fallback.State
	}

	if fallback.Reason != "" {
		fields["capability_fallback_reason"] = fallback.Reason
	}

	if fallback.SourceReason != "" {
		fields["capability_fallback_source_reason"] = fallback.SourceReason
	}

	if absence != nil {
		fields["capability_absence_confidence"] = absence.Level
		fields["capability_absence_reason"] = absence.Reason

		if absence.PrecisionTier != "" {
			fields["capability_absence_precision_tier"] = absence.PrecisionTier
		}

		if absence.Source != "" {
			fields["capability_absence_source"] = absence.Source
		}
	}

	fields["capability_unresolved_gaps"] = len(readOnlyCapabilityStatusGaps(decoded, fallback, absence))
}

func readOnlyCapabilityUnderlyingAsToolResult(underlying *ReadOnlyCapabilityUnderlyingResult) *ToolResult {
	if underlying == nil {
		return nil
	}

	return &ToolResult{
		Tool:          underlying.Tool,
		Summary:       underlying.Summary,
		Output:        underlying.Output,
		Error:         underlying.Error,
		PrecisionTier: underlying.PrecisionTier,
		Relationships: append([]ToolRelationship(nil), underlying.Relationships...),
	}
}

func addContextAutoRefreshTelemetry(payload map[string]any, fields map[string]any) {
	value, ok := payload["auto_refresh"].(map[string]any)
	if !ok {
		return
	}

	copyTelemetryBool(value, fields, "attempted", "context_auto_refresh_attempted")
	copyTelemetryString(value, fields, "scope", "context_auto_refresh_scope")
	copyTelemetryString(value, fields, "language", "context_auto_refresh_language")
	copyTelemetryString(value, fields, "reason", "context_auto_refresh_reason")
	copyTelemetryString(value, fields, "skipped_reason", "context_auto_refresh_skipped_reason")
	copyTelemetryString(value, fields, "error", "context_auto_refresh_error")
	copyTelemetryNumber(value, fields, "files_indexed", "context_auto_refresh_files_indexed")
	copyTelemetryNumber(value, fields, "symbols_indexed", "context_auto_refresh_symbols_indexed")
	copyTelemetryNumber(value, fields, "edges_indexed", "context_auto_refresh_edges_indexed")
	copyTelemetryString(value, fields, "partial_refresh_reason", "context_auto_refresh_partial_refresh_reason")
	copyTelemetryString(value, fields, "full_rebuild_reason", "context_auto_refresh_full_rebuild_reason")
	copyTelemetryBool(value, fields, "fresh_after", "context_auto_refresh_fresh_after")
	copyTelemetryArrayLen(value, fields, "refreshed_files", "context_auto_refresh_refreshed_files")
}

func addContextFreshnessTelemetry(payload map[string]any, fields map[string]any) {
	value, ok := payload["freshness"].(map[string]any)
	if !ok {
		value, ok = payload["context_freshness"].(map[string]any)
	}

	if !ok {
		return
	}

	copyTelemetryString(value, fields, "policy", "context_freshness_policy")
	copyTelemetryString(value, fields, "operation", "context_freshness_operation")
	copyTelemetryString(value, fields, "index_id", "context_freshness_index_id")
	copyTelemetryString(value, fields, "store_kind", "context_freshness_store_kind")
	copyTelemetryString(value, fields, "generated_at", "context_freshness_generated_at")
	copyTelemetryString(value, fields, "scope", "context_freshness_scope")
	copyTelemetryString(value, fields, "reason", "context_freshness_reason")
	copyTelemetryString(value, fields, "blocker", "context_freshness_blocker")
	copyTelemetryBool(value, fields, "fresh_for_scope", "context_fresh_for_scope")
	copyTelemetryBool(value, fields, "global_fresh", "context_global_fresh")
	copyTelemetryBool(value, fields, "budget_exceeded", "context_freshness_budget_exceeded")
	copyTelemetryNumber(value, fields, "checked_path_count", "context_freshness_checked_path_count")
	copyTelemetryNumber(value, fields, "stale_path_count", "context_freshness_stale_path_count")
	copyTelemetryNumber(value, fields, "missing_path_count", "context_freshness_missing_path_count")
	copyTelemetryArrayLen(value, fields, "checked_paths", "context_freshness_checked_paths")
	copyTelemetryArrayLen(value, fields, "stale_paths", "context_freshness_stale_paths")
	copyTelemetryArrayLen(value, fields, "missing_paths", "context_freshness_missing_paths")
	copyTelemetryStringArraySample(value, fields, "stale_paths", "context_freshness_stale_path_samples", contextTelemetryPathSampleLimit)
	copyTelemetryStringArraySample(value, fields, "missing_paths", "context_freshness_missing_path_samples", contextTelemetryPathSampleLimit)

	state := contextFreshnessState(value)
	if state != "" {
		fields["context_freshness_state"] = state
	}

	if state == "blocked" {
		fields["context_freshness_blocked"] = true
	}

	refresh, ok := value["refresh"].(map[string]any)
	if !ok {
		return
	}

	copyTelemetryBool(refresh, fields, "attempted", "context_freshness_refresh_attempted")
	copyTelemetryString(refresh, fields, "scope", "context_freshness_refresh_scope")
	copyTelemetryString(refresh, fields, "reason", "context_freshness_refresh_reason")
	copyTelemetryString(refresh, fields, "skipped_reason", "context_freshness_refresh_skipped_reason")
	copyTelemetryString(refresh, fields, "error", "context_freshness_refresh_error")
	copyTelemetryString(refresh, fields, "full_rebuild_reason", "context_freshness_full_rebuild_reason")
	copyTelemetryNumber(refresh, fields, "files_indexed", "context_freshness_refresh_files_indexed")
	copyTelemetryArrayLen(refresh, fields, "refreshed_files", "context_freshness_refresh_refreshed_files")
}

func addContextStaleEvidenceTelemetry(payload map[string]any, fields map[string]any) {
	value, ok := payload["stale_evidence"].(map[string]any)
	if !ok {
		value, ok = payload["context_stale_evidence"].(map[string]any)
	}

	if !ok {
		return
	}

	copyTelemetryString(value, fields, "state", "context_stale_evidence_state")
	copyTelemetryString(value, fields, "policy", "context_stale_evidence_policy")
	copyTelemetryString(value, fields, "reason", "context_stale_evidence_reason")
	copyTelemetryBool(value, fields, "fresh_for_scope", "context_stale_evidence_fresh_for_scope")
	copyTelemetryBool(value, fields, "global_fresh", "context_stale_evidence_global_fresh")
	copyTelemetryNumber(value, fields, "stale_path_count", "context_stale_evidence_stale_path_count")
	copyTelemetryNumber(value, fields, "missing_path_count", "context_stale_evidence_missing_path_count")

	state, _ := value["state"].(string)
	switch strings.TrimSpace(state) {
	case "blocked":
		fields["context_freshness_state"] = "blocked"
		fields["context_stale_use_blocked"] = true
	case "reported":
		fields["context_freshness_state"] = "reported"
		delete(fields, "context_freshness_blocked")

		fields["context_stale_use_reported"] = true
	case "allowed_stale":
		fields["context_freshness_state"] = "allowed_stale"
		delete(fields, "context_freshness_blocked")

		fields["context_stale_use_allowed"] = true
	case "fresh":
		fields["context_freshness_state"] = "fresh"
		delete(fields, "context_freshness_blocked")

		fields["context_stale_use_fresh"] = true
	}
}

func addContextCacheTelemetry(payload map[string]any, fields map[string]any) {
	value, ok := payload["cache"].(map[string]any)
	if !ok {
		contract, contractOK := payload["contract"].(map[string]any)
		if contractOK {
			value, ok = contract["cache"].(map[string]any)
		}
	}

	if !ok {
		return
	}

	copyTelemetryString(value, fields, "state", "context_cache_state")
	copyTelemetryString(value, fields, "source", "context_cache_source")
	copyTelemetryString(value, fields, "reason", "context_cache_reason")
	copyTelemetryString(value, fields, "cache_key", "context_cache_key")
	copyTelemetryString(value, fields, "pack_id", "context_cache_pack_id")
	copyTelemetryString(value, fields, "workspace_state_fingerprint", "context_cache_workspace_state_fingerprint")
	copyTelemetryString(value, fields, "ranker_weights_fingerprint", "context_cache_ranker_weights_fingerprint")
	copyTelemetryString(value, fields, "runtime_traces_fingerprint", "context_cache_runtime_traces_fingerprint")
	copyTelemetryString(value, fields, "freshness_policy", "context_cache_freshness_policy")
	copyTelemetryBool(value, fields, "read_through_refresh", "context_cache_read_through_refresh")
	copyTelemetryBool(value, fields, "fallback", "context_cache_fallback")

	invalidation, ok := value["invalidation"].(map[string]any)
	if !ok {
		return
	}

	copyTelemetryString(invalidation, fields, "policy", "context_cache_invalidation_policy")
	copyTelemetryString(invalidation, fields, "state", "context_cache_invalidation_state")
	copyTelemetryString(invalidation, fields, "stale_result", "context_cache_stale_result")
	copyTelemetryArrayLen(invalidation, fields, "sources", "context_cache_invalidation_sources")
}

func contextFreshnessState(freshness map[string]any) string {
	fresh, ok := freshness["fresh_for_scope"].(bool)
	if !ok {
		return ""
	}

	if fresh {
		return "fresh"
	}

	policy, _ := freshness["policy"].(string)
	switch strings.TrimSpace(policy) {
	case "report_only":
		return "reported"
	case "allow_stale":
		return "allowed_stale"
	default:
		return "blocked"
	}
}

func addContextConfidenceTelemetry(payload map[string]any, fields map[string]any) {
	value, ok := payload["confidence"].(map[string]any)
	if !ok {
		value, ok = payload["context_confidence"].(map[string]any)
	}

	if !ok {
		return
	}

	copyTelemetryString(value, fields, "level", "context_confidence_level")
	copyTelemetryString(value, fields, "source", "context_confidence_source")
	copyTelemetryString(value, fields, "reason", "context_confidence_reason")
}

func addContextProvenanceTelemetry(payload map[string]any, fields map[string]any) {
	value, ok := payload["provenance"].(map[string]any)
	if !ok {
		value, ok = payload["context_provenance"].(map[string]any)
	}

	if !ok {
		return
	}

	copyTelemetryString(value, fields, "source", "context_provenance_source")
	copyTelemetryString(value, fields, "operation", "context_provenance_operation")
	copyTelemetryString(value, fields, "store_kind", "context_provenance_store_kind")
	copyTelemetryString(value, fields, "index_id", "context_provenance_index_id")
	copyTelemetryString(value, fields, "generated_at", "context_provenance_generated_at")
	copyTelemetryString(value, fields, "collector_version", "context_provenance_collector_version")
	copyTelemetryString(value, fields, "current_collector_version", "context_provenance_current_collector_version")
	copyTelemetryString(value, fields, "artifact_path", "context_provenance_artifact_path")
}

func addContextNodeKindTelemetry(payload map[string]any, fields map[string]any) {
	nodes, ok := payload["nodes"].([]any)
	if !ok {
		return
	}

	summaries := 0
	modelAuthored := 0

	for _, item := range nodes {
		node, ok := item.(map[string]any)
		if !ok {
			continue
		}

		if kind, _ := node["kind"].(string); kind != "summary" {
			continue
		}

		summaries++

		properties, _ := node["properties"].(map[string]any)

		if authored, _ := properties["model_authored"].(bool); authored {
			modelAuthored++
		}
	}

	if summaries > 0 {
		fields["context_summary_nodes"] = summaries
	}

	if modelAuthored > 0 {
		fields["context_model_summary_nodes"] = modelAuthored
	}
}

func copyTelemetryString(source map[string]any, target map[string]any, sourceKey string, targetKey string) {
	value, ok := source[sourceKey].(string)
	if ok && strings.TrimSpace(value) != "" {
		target[targetKey] = value
	}
}

func copyTelemetryStringLimited(source map[string]any, target map[string]any, sourceKey string, targetKey string, limit int) {
	value, ok := source[sourceKey].(string)
	if !ok {
		return
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return
	}

	if limit > 0 && len(value) > limit {
		value = value[:limit]
	}

	target[targetKey] = value
}

func copyTelemetryBool(source map[string]any, target map[string]any, sourceKey string, targetKey string) {
	value, ok := source[sourceKey].(bool)
	if ok {
		target[targetKey] = value
	}
}

func copyTelemetryNumber(source map[string]any, target map[string]any, sourceKey string, targetKey string) {
	switch value := source[sourceKey].(type) {
	case float64:
		target[targetKey] = int(value)
	case int:
		target[targetKey] = value
	}
}

func copyTelemetryArrayLen(source map[string]any, target map[string]any, sourceKey string, targetKey string) {
	values, ok := source[sourceKey].([]any)
	if ok {
		target[targetKey] = len(values)
	}
}

func copyTelemetryStringArraySample(source map[string]any, target map[string]any, sourceKey string, targetKey string, limit int) {
	values, ok := source[sourceKey].([]any)
	if !ok {
		return
	}

	samples := make([]string, 0, min(len(values), max(limit, 0)))
	for _, value := range values {
		if limit > 0 && len(samples) >= limit {
			break
		}

		text, ok := value.(string)
		if !ok {
			continue
		}

		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		samples = append(samples, text)
	}

	if len(samples) > 0 {
		target[targetKey] = samples
	}
}

func addStorageBackendTelemetry(source map[string]any, target map[string]any) {
	values, ok := source["storage_backends"].([]any)
	if !ok {
		return
	}

	statuses := make([]string, 0, len(values))
	incompatible := 0

	for _, value := range values {
		backend, ok := value.(map[string]any)
		if !ok {
			continue
		}

		kind, _ := backend["kind"].(string)
		status, _ := backend["status"].(string)

		if kind != "" && status != "" {
			statuses = append(statuses, kind+":"+status)
		}

		if compatible, ok := backend["compatible"].(bool); ok && !compatible {
			incompatible++
		}

		if active, ok := backend["active"].(bool); ok && active && kind != "" {
			target["context_active_backend"] = kind
			if status != "" {
				target["context_active_backend_status"] = status
			}
		}
	}

	if len(statuses) > 0 {
		target["context_storage_backends"] = statuses
	}

	target["context_incompatible_backends"] = incompatible
}

func addCollectorReportTelemetry(source map[string]any, target map[string]any) {
	values, ok := source["collector_reports"].([]any)
	if !ok {
		return
	}

	names := make([]string, 0, len(values))
	unavailable := 0
	warnings := 0
	diagnostics := 0
	normalizedRelationshipEdges := 0

	for _, value := range values {
		report, ok := value.(map[string]any)
		if !ok {
			continue
		}

		name, _ := report["name"].(string)
		if name != "" {
			names = append(names, name)
		}

		if available, ok := report["available"].(bool); ok && !available {
			unavailable++
		}

		if rawWarnings, ok := report["warnings"].([]any); ok {
			warnings += len(rawWarnings)
		}

		switch value := report["diagnostics"].(type) {
		case float64:
			diagnostics += int(value)
		case int:
			diagnostics += value
		}

		if properties, ok := report["properties"].(map[string]any); ok {
			switch value := properties["normalized_relationship_edges"].(type) {
			case float64:
				normalizedRelationshipEdges += int(value)
			case int:
				normalizedRelationshipEdges += value
			}
		}
	}

	if len(names) > 0 {
		target["context_collector_report_names"] = names
	}

	target["context_collector_unavailable"] = unavailable
	target["context_collector_warnings"] = warnings
	target["context_enrichment_diagnostics"] = diagnostics
	target["context_normalized_relationship_edges"] = normalizedRelationshipEdges
}

func addCollectorCoverageTelemetry(source map[string]any, target map[string]any) {
	values, ok := source["collector_coverage"].([]any)
	if !ok {
		return
	}

	names := make([]string, 0, len(values))
	for _, value := range values {
		name, ok := value.(string)
		if ok && strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}

	if len(names) > 0 {
		target["context_collector_coverage_names"] = names
	}
}
