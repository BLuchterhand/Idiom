package eval

import (
	"encoding/json"
	"testing"
)

// Helper: build a synthetic record slice from a simple step script so
// tests read as a storyboard rather than hand-assembled JSON blobs.
type synthStep struct {
	tool    string
	action  string
	raw     string
	summary string
	err     string // non-empty means this step rejects
	role    string // non-empty means emit a role-only message instead of a tool_request
}

func buildSynth(userPrompt string, ledger map[string]any, steps []synthStep) []Record {
	var records []Record
	records = append(records, Record{
		Type: "message",
		Data: map[string]any{"role": "user", "content": userPrompt},
	})
	for _, s := range steps {
		if s.role != "" {
			records = append(records, Record{
				Type: "message",
				Data: map[string]any{"role": s.role, "content": ""},
			})
			continue
		}
		records = append(records, Record{
			Type: "tool_request",
			Data: map[string]any{"tool": s.tool, "action": s.action, "raw": s.raw},
		})
		records = append(records, Record{
			Type: "tool_result",
			Data: map[string]any{"tool": s.tool, "summary": s.summary, "error": s.err},
		})
	}
	if ledger != nil {
		data, _ := json.Marshal(ledger)
		records = append(records, Record{
			Type: "message",
			Data: map[string]any{"role": "budget_ledger", "content": string(data)},
		})
	}
	return records
}

func TestAnalyzeBudgetLedgerFields(t *testing.T) {
	records := buildSynth(
		"how does auth work",
		map[string]any{
			"used_model_calls":  7,
			"used_tool_results": 6,
			"prompt_tokens":     12345,
			"completion_tokens": 678,
			"duration_millis":   9000,
			"rejected_actions":  2,
			"stop_reason":       "model_final",
		},
		[]synthStep{
			{tool: "read_file", action: "read_file:auth.go", raw: `{"path":"auth.go"}`, summary: "Read auth.go"},
		},
	)
	rep := analyzeRecords("test", records)
	if rep.ModelCalls != 7 || rep.ToolResults != 6 || rep.PromptTokens != 12345 || rep.DurationMillis != 9000 {
		t.Fatalf("budget ledger not parsed: %+v", rep)
	}
	if rep.StopReason != "model_final" {
		t.Fatalf("stop reason: want model_final, got %q", rep.StopReason)
	}
	if rep.UserPrompt != "how does auth work" {
		t.Fatalf("user prompt: want 'how does auth work', got %q", rep.UserPrompt)
	}
}

func TestDetectDirWalkStreak(t *testing.T) {
	records := buildSynth("", nil, []synthStep{
		{tool: "list_files", action: "list_files:.", raw: `{"path":"."}`, summary: "Listed 10 path(s)."},
		{tool: "list_files", action: "list_files:a", raw: `{"path":"a"}`, summary: "Listed 5 path(s)."},
		{tool: "list_files", action: "list_files:b", raw: `{"path":"b"}`, summary: "Listed 3 path(s)."},
		{tool: "read_file", action: "read_file:x.go", raw: `{"path":"x.go"}`, summary: "Read x.go"},
	})
	rep := analyzeRecords("test", records)
	if !hasPattern(rep, "dir_walk_streak") {
		t.Fatalf("expected dir_walk_streak, got: %+v", rep.AntiPatterns)
	}
}

func TestAnalyzeLogIngestsContextTelemetryFields(t *testing.T) {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"tool": "context_status", "action": "context_status"}},
		{Type: "tool_result", Data: map[string]any{
			"tool":                                 "context_status",
			"summary":                              "Context index is fresh",
			"error":                                "",
			"context_index_id":                     "idx1",
			"context_store_kind":                   "sqlite-graph-v1",
			"context_active_backend":               "sqlite-graph-v1",
			"context_active_backend_status":        "active",
			"context_storage_backends":             []any{"json-graph-v1:export", "sqlite-graph-v1:active"},
			"context_indexed":                      true,
			"context_fresh":                        true,
			"context_files_indexed":                3,
			"context_symbols_indexed":              7,
			"context_edges_indexed":                11,
			"context_stale_symbols":                2,
			"context_stale_symbol_samples":         1,
			"context_refreshed_stale_symbols":      1,
			"context_incompatible_backends":        0,
			"context_incremental":                  true,
			"context_reused_indexed_files":         2,
			"context_dependency_refresh_files":     1,
			"context_auto_refresh_attempted":       true,
			"context_auto_refresh_reason":          "small_stale_file_set",
			"context_auto_refresh_files_indexed":   1,
			"context_auto_refresh_refreshed_files": 1,
			"context_auto_refresh_fresh_after":     true,
			"context_collector_reports":            2,
			"context_collector_coverage":           3,
			"context_collector_report_names": []any{
				"python_lsp",
				"workspace_tool_enrichment",
			},
			"context_collector_coverage_names": []any{
				"python_index",
				"language_environment",
			},
			"context_collector_unavailable":         1,
			"context_collector_warnings":            1,
			"context_collector_version_mismatch":    true,
			"context_enrichment_diagnostics":        3,
			"context_normalized_relationship_edges": 4,
		}},
		{Type: "tool_request", Data: map[string]any{"tool": "context_query", "action": "context_query"}},
		{Type: "tool_result", Data: map[string]any{
			"tool":                        "context_query",
			"summary":                     "Context query returned nodes",
			"error":                       "",
			"context_store_kind":          "sqlite-graph-v1",
			"context_fresh":               false,
			"context_nodes":               2,
			"context_edges":               4,
			"context_summary_nodes":       1,
			"context_model_summary_nodes": 1,
		}},
	}
	rep := analyzeRecords("test", records)
	if rep.Context == nil {
		t.Fatalf("expected context telemetry summary")
	}
	if rep.Context.ToolResults != 2 || rep.Context.FreshResults != 1 || rep.Context.StaleResults != 1 {
		t.Fatalf("unexpected context freshness summary: %#v", rep.Context)
	}
	if rep.Context.MaxFilesIndexed != 3 || rep.Context.MaxSymbolsIndexed != 7 || rep.Context.MaxEdgesIndexed != 11 {
		t.Fatalf("unexpected context graph maxima: %#v", rep.Context)
	}
	if rep.Context.NodesReturned != 2 || rep.Context.EdgesReturned != 4 {
		t.Fatalf("unexpected context query counts: %#v", rep.Context)
	}
	if rep.Context.StoreKindCounts["sqlite-graph-v1"] != 2 || rep.Context.ActiveBackendCounts["sqlite-graph-v1"] != 1 {
		t.Fatalf("unexpected context backend counts: %#v", rep.Context)
	}
	if rep.Context.CollectorReports != 2 || rep.Context.CollectorCoverage != 3 || rep.Context.CollectorUnavailable != 1 || rep.Context.CollectorVersionMismatches != 1 || rep.Context.EnrichmentDiagnostics != 3 || rep.Context.CollectorReportNameCounts["python_lsp"] != 1 || rep.Context.CollectorCoverageNameCounts["python_index"] != 1 {
		t.Fatalf("unexpected context collector telemetry: %#v", rep.Context)
	}
	if rep.Context.MaxStaleSymbols != 2 || rep.Context.StaleSymbolSamples != 1 || rep.Context.RefreshedStaleSymbols != 1 || rep.Context.NormalizedRelationshipEdges != 4 {
		t.Fatalf("unexpected context stale/relationship telemetry: %#v", rep.Context)
	}
	if rep.Context.IncrementalRefreshes != 1 || rep.Context.ReusedIndexedFiles != 2 || rep.Context.DependencyRefreshFiles != 1 || rep.Context.SummaryNodesReturned != 1 || rep.Context.ModelSummaryNodesReturned != 1 {
		t.Fatalf("unexpected context incremental/summary telemetry: %#v", rep.Context)
	}
	if rep.Context.AutoRefreshAttempts != 1 || rep.Context.AutoRefreshFilesIndexed != 1 || rep.Context.AutoRefreshRefreshedFiles != 1 || rep.Context.AutoRefreshFreshAfter != 1 || rep.Context.AutoRefreshReasonCounts["small_stale_file_set"] != 1 {
		t.Fatalf("unexpected context auto-refresh telemetry: %#v", rep.Context)
	}

	agg := Aggregate([]PerfReport{rep})
	if agg.Context == nil || agg.Context.ToolResults != 2 || agg.Context.StorageBackendStatusCounts["sqlite-graph-v1:active"] != 1 || agg.Context.CollectorReportNameCounts["workspace_tool_enrichment"] != 1 || agg.Context.CollectorCoverageNameCounts["language_environment"] != 1 || agg.Context.CollectorVersionMismatches != 1 || agg.Context.NormalizedRelationshipEdges != 4 || agg.Context.IncrementalRefreshes != 1 || agg.Context.ReusedIndexedFiles != 2 || agg.Context.DependencyRefreshFiles != 1 || agg.Context.SummaryNodesReturned != 1 || agg.Context.ModelSummaryNodesReturned != 1 || agg.Context.AutoRefreshAttempts != 1 || agg.Context.AutoRefreshReasonCounts["small_stale_file_set"] != 1 {
		t.Fatalf("aggregate did not preserve context telemetry: %#v", agg.Context)
	}
}

func TestAnalyzeLogIngestsHarnessFeedbackTelemetryFields(t *testing.T) {
	records := []Record{
		{Type: "tool_request", Data: map[string]any{"tool": "harness_feedback", "action": "harness_feedback"}},
		{Type: "tool_result", Data: map[string]any{
			"tool":                    "harness_feedback",
			"summary":                 "Recorded tool_gap feedback for context_pack",
			"error":                   "",
			"feedback_category":       "tool_gap",
			"feedback_component":      "context_pack",
			"feedback_confidence":     "verified",
			"feedback_severity":       "medium",
			"feedback_evidence_count": 2,
		}},
		{Type: "tool_request", Data: map[string]any{"tool": "idiom_feedback", "action": "idiom_feedback"}},
		{Type: "tool_result", Data: map[string]any{
			"tool":                    "idiom_feedback",
			"summary":                 "Recorded prompt_gap feedback for prompt/base",
			"error":                   "",
			"feedback_category":       "prompt_gap",
			"feedback_component":      "prompt/base",
			"feedback_confidence":     "inferred",
			"feedback_severity":       "high",
			"feedback_evidence_count": 1,
		}},
	}
	rep := analyzeRecords("test", records)
	if rep.Feedback == nil {
		t.Fatalf("expected feedback telemetry summary")
	}
	if rep.Feedback.Total != 2 || rep.Feedback.Verified != 1 || rep.Feedback.Inferred != 1 || rep.Feedback.MediumSeverity != 1 || rep.Feedback.HighSeverity != 1 || rep.Feedback.EvidenceItems != 3 {
		t.Fatalf("unexpected feedback summary: %#v", rep.Feedback)
	}
	if rep.Feedback.ByCategory["tool_gap"] != 1 || rep.Feedback.ByComponent["context_pack"] != 1 || rep.Feedback.ByConfidence["verified"] != 1 || rep.Feedback.BySeverity["high"] != 1 {
		t.Fatalf("unexpected feedback buckets: %#v", rep.Feedback)
	}

	agg := Aggregate([]PerfReport{rep})
	if agg.Feedback == nil || agg.Feedback.Total != 2 || agg.Feedback.HighSeverity != 1 || agg.Feedback.ByCategory["prompt_gap"] != 1 || agg.Feedback.ByComponent["prompt/base"] != 1 {
		t.Fatalf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback)
	}
}

func TestDetectPromptSurfaceOverlap(t *testing.T) {
	records := []Record{
		{Type: "session_start", Data: map[string]any{
			"context_sources": []any{
				"prompts/base.md",
				"prompts/profiles/coding.md",
				"AGENTS.md",
				".codex/project-context.md",
				".idiom/discovery/context.md",
				".codex/rules/qwen-known-traps.md",
				".codex/rules/qwen-loop-protocol.md",
				".codex/rules/qwen-brief-skeleton.md",
			},
		}},
		{Type: "message", Data: map[string]any{
			"role":    "control_rails",
			"content": `{"contract":"implementation_result","dynamic_instruction_attachment":true}`,
		}},
	}
	rep := analyzeRecords("test", records)
	ap := findPattern(rep, "prompt_surface_overlap")
	if ap == nil {
		t.Fatalf("expected prompt_surface_overlap")
	}
	if ap.Count != 2 {
		t.Fatalf("prompt_surface_overlap count: want 2, got %d", ap.Count)
	}
}

func TestDirWalkStreakDoesNotFireOnTwoListings(t *testing.T) {
	records := buildSynth("", nil, []synthStep{
		{tool: "list_files", action: "list_files:.", summary: "Listed 10 path(s)."},
		{tool: "list_files", action: "list_files:a", summary: "Listed 5 path(s)."},
		{tool: "search_content", action: "search_content:.:foo:_", summary: "Found 3 matching line(s)."},
	})
	rep := analyzeRecords("test", records)
	if hasPattern(rep, "dir_walk_streak") {
		t.Fatalf("dir_walk_streak should not fire on just 2 list_files")
	}
}

func TestDetectZeroHitSearch(t *testing.T) {
	records := buildSynth("", nil, []synthStep{
		{tool: "search_files", action: "search_files:_:foo*:_", raw: `{"pattern":"foo*"}`, summary: `No file matches for "foo*"`},
		{tool: "go_find_symbol", action: "go_find_symbol:bar", raw: `{"pattern":"bar"}`, summary: `No Go symbols matched "bar"`},
	})
	rep := analyzeRecords("test", records)
	ap := findPattern(rep, "zero_hit_search")
	if ap == nil {
		t.Fatalf("expected zero_hit_search pattern")
	}
	if ap.Count != 2 {
		t.Fatalf("zero_hit_search count: want 2, got %d", ap.Count)
	}
}

func TestDetectRejectionRetry(t *testing.T) {
	raw := `{"tool":"list_files","path":"docs"}`
	records := buildSynth("", nil, []synthStep{
		{tool: "list_files", action: "list_files:docs", raw: raw, summary: "Chained list_files rejected", err: "gate fired"},
		{tool: "list_files", action: "list_files:docs", raw: raw, summary: "Rejected again", err: "same"},
	})
	rep := analyzeRecords("test", records)
	if !hasPattern(rep, "rejection_retry") {
		t.Fatalf("expected rejection_retry, got: %+v", rep.AntiPatterns)
	}
}

func TestDetectSameToolRepeatOnlyAtThreeOrMore(t *testing.T) {
	raw := `{"tool":"read_file","path":"x.go"}`
	records := buildSynth("", nil, []synthStep{
		{tool: "read_file", action: "read_file:x.go", raw: raw, summary: "Read 1"},
		{tool: "read_file", action: "read_file:x.go", raw: raw, summary: "Read 2"},
	})
	rep := analyzeRecords("test", records)
	if hasPattern(rep, "same_tool_repeat") {
		t.Fatalf("same_tool_repeat should not fire at 2 identical calls")
	}
	// Now three.
	records = append(records,
		Record{Type: "tool_request", Data: map[string]any{"tool": "read_file", "action": "read_file:x.go", "raw": raw}},
		Record{Type: "tool_result", Data: map[string]any{"tool": "read_file", "summary": "Read 3"}},
	)
	rep = analyzeRecords("test", records)
	if !hasPattern(rep, "same_tool_repeat") {
		t.Fatalf("same_tool_repeat should fire at 3 identical calls")
	}
}

func TestAggregateRanksByLogHits(t *testing.T) {
	mkRep := func(patterns ...string) PerfReport {
		r := PerfReport{Tools: ToolCounts{ByName: map[string]int{}}}
		for _, p := range patterns {
			r.AntiPatterns = append(r.AntiPatterns, AntiPattern{Name: p, Count: 1})
		}
		return r
	}
	reports := []PerfReport{
		mkRep("zero_hit_search"),
		mkRep("zero_hit_search", "dir_walk_streak"),
		mkRep("zero_hit_search"),
		mkRep("dir_walk_streak"),
	}
	agg := Aggregate(reports)
	if agg.LogCount != 4 {
		t.Fatalf("log count: want 4, got %d", agg.LogCount)
	}
	if len(agg.PatternHitRate) == 0 {
		t.Fatalf("expected pattern rows")
	}
	if agg.PatternHitRate[0].Name != "zero_hit_search" || agg.PatternHitRate[0].LogsHit != 3 {
		t.Fatalf("expected zero_hit_search leading with 3 logs hit, got %+v", agg.PatternHitRate[0])
	}
}

func hasPattern(rep PerfReport, name string) bool {
	return findPattern(rep, name) != nil
}

func findPattern(rep PerfReport, name string) *AntiPattern {
	for i := range rep.AntiPatterns {
		if rep.AntiPatterns[i].Name == name {
			return &rep.AntiPatterns[i]
		}
	}
	return nil
}
