package workspace

import (
	"encoding/json"
	"strings"
)

func ToolTelemetryFields(tool string, output string) map[string]any {
	fields := make(map[string]any)
	for key, value := range ContextTelemetryFields(tool, output) {
		fields[key] = value
	}
	for key, value := range HarnessFeedbackTelemetryFields(tool, output) {
		fields[key] = value
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func ContextTelemetryFields(tool string, output string) map[string]any {
	if !strings.HasPrefix(strings.TrimSpace(tool), "context_") || strings.TrimSpace(output) == "" {
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
	copyTelemetryArrayLen(payload, fields, "tests", "context_tests")
	copyTelemetryArrayLen(payload, fields, "relationships", "context_relationships")
	copyTelemetryArrayLen(payload, fields, "collector_reports", "context_collector_reports")
	copyTelemetryArrayLen(payload, fields, "collector_coverage", "context_collector_coverage")
	copyTelemetryArrayLen(payload, fields, "events", "context_trace_events")
	copyTelemetryArrayLen(payload, fields, "executed_files", "context_trace_executed_files")
	copyTelemetryArrayLen(payload, fields, "failing_anchors", "context_trace_failing_anchors")
	copyTelemetryArrayLen(payload, fields, "unresolved_gaps", "context_unresolved_gaps")
	addStorageBackendTelemetry(payload, fields)
	addCollectorReportTelemetry(payload, fields)
	addCollectorCoverageTelemetry(payload, fields)
	addContextAutoRefreshTelemetry(payload, fields)
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
