package eval

import (
	"encoding/json"
	"strings"
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

func TestAnalyzeLogDistinguishesStaleEvidenceStates(t *testing.T) {
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool":                              "context_query",
			"context_freshness_policy":          "require_fresh",
			"context_freshness_blocked":         true,
			"context_stale_evidence_state":      "blocked",
			"context_stale_use_blocked":         true,
			"context_freshness_budget_exceeded": true,
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":                         "context_zoom",
			"context_freshness_policy":     "report_only",
			"context_stale_evidence_state": "reported",
			"context_stale_use_reported":   true,
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":                         "context_pack",
			"context_freshness_policy":     "allow_stale",
			"context_stale_evidence_state": "allowed_stale",
			"context_stale_use_allowed":    true,
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":                         "context_status",
			"context_freshness_policy":     "report_only",
			"context_stale_evidence_state": "fresh",
			"context_stale_use_fresh":      true,
		}},
	}
	rep := analyzeRecords("test", records)
	if rep.Context == nil {
		t.Fatalf("expected context telemetry summary")
	}
	if rep.Context.FreshnessBlockers != 1 || rep.Context.FreshnessBudgetExceeded != 1 {
		t.Fatalf("expected only blocked stale evidence to count as freshness blocker, got %#v", rep.Context)
	}
	if rep.Context.StaleUseBlocked != 1 || rep.Context.StaleUseReported != 1 || rep.Context.StaleUseAllowed != 1 || rep.Context.StaleUseFresh != 1 {
		t.Fatalf("unexpected stale-evidence state counters: %#v", rep.Context)
	}
	if rep.Context.StaleUseStateCounts["blocked"] != 1 || rep.Context.StaleUseStateCounts["reported"] != 1 || rep.Context.StaleUseStateCounts["allowed_stale"] != 1 || rep.Context.StaleUseStateCounts["fresh"] != 1 {
		t.Fatalf("unexpected stale-evidence state counts: %#v", rep.Context.StaleUseStateCounts)
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
			"feedback_category":       "impact_ranking",
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
	if rep.Feedback.ByCategory["tool_gap"] != 1 || rep.Feedback.ByCategory["impact_ranking"] != 1 || rep.Feedback.ByPlanningCategory["impact_ranking"] != 1 || rep.Feedback.PlanningQualityTotal != 1 || rep.Feedback.ByComponent["context_pack"] != 1 || rep.Feedback.ByConfidence["verified"] != 1 || rep.Feedback.BySeverity["high"] != 1 {
		t.Fatalf("unexpected feedback buckets: %#v", rep.Feedback)
	}

	agg := Aggregate([]PerfReport{rep})
	if agg.Feedback == nil || agg.Feedback.Total != 2 || agg.Feedback.HighSeverity != 1 || agg.Feedback.PlanningQualityTotal != 1 || agg.Feedback.ByCategory["impact_ranking"] != 1 || agg.Feedback.ByComponent["prompt/base"] != 1 {
		t.Fatalf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback)
	}
}

func TestAnalyzeLogIngestsLocalOpsTelemetryFields(t *testing.T) {
	records := []Record{
		{Type: "implement_start", Data: map[string]any{
			"local_ops": map[string]any{
				"backend_configured":        true,
				"backend_kind":              "mlx",
				"context_budget_state":      "degraded",
				"classifier_state":          "disabled_fallback",
				"classifier_enabled":        false,
				"backend_ctx_size":          262144,
				"context_budget_max_output": 8192,
			},
		}},
		{Type: "implement_queue", Data: map[string]any{"state": "degraded", "pool": "qwen-coder", "wait_ms": 42, "degraded": true}},
		{Type: "implement_session_closeout", Data: map[string]any{"session_id": "abc", "summary_event": "session_summary"}},
		{Type: "doctor_readiness_profile", Data: map[string]any{"profile": "implement", "state": "degraded", "degraded": true, "queue_state": "unavailable", "context_budget_state": "pass", "route_classifier_state": "degraded", "backend_state": "pass"}},
	}

	rep := analyzeRecords("test", records)
	if rep.LocalOps == nil {
		t.Fatal("expected local ops telemetry summary")
	}
	if rep.LocalOps.BackendConfigured != 1 || rep.LocalOps.ContextBudgetDegraded != 1 || rep.LocalOps.ClassifierDegraded != 1 || rep.LocalOps.QueueDegraded != 1 || rep.LocalOps.QueueMaxWaitMillis != 42 || rep.LocalOps.ImplementCloseouts != 1 || rep.LocalOps.DoctorDegradedProfiles != 1 {
		t.Fatalf("unexpected local ops telemetry: %#v", rep.LocalOps)
	}
	agg := Aggregate([]PerfReport{rep})
	if agg.LocalOps == nil || agg.LocalOps.QueueDegraded != 1 || agg.LocalOps.ImplementCloseouts != 1 {
		t.Fatalf("aggregate did not preserve local ops telemetry: %#v", agg.LocalOps)
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
		{Type: "run_event", Data: map[string]any{
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

func TestDetectCompletionGateReopenedDiscovery(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "write a refined brief"}},
		{Type: "model_call", Data: map[string]any{"prompt_tokens": 100, "completion_tokens": 20}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "completion_gate",
			"step":    3,
			"summary": "Mutation completion gate rejected final answer",
			"error":   "this task asks for a workspace change, but no successful write tool result is present",
		}},
		{Type: "tool_request", Data: map[string]any{
			"tool":   "read_file",
			"action": "read_file:native/src/example.cpp",
			"path":   "native/src/example.cpp",
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "read_file",
			"summary": "Read native/src/example.cpp",
			"error":   "",
		}},
	}
	rep := analyzeRecords("test", records)
	ap := findPattern(rep, "completion_gate_reopened_discovery")
	if ap == nil {
		t.Fatalf("expected completion_gate_reopened_discovery, got %#v", rep.AntiPatterns)
	}
	if ap.Count != 1 || !strings.Contains(strings.Join(ap.Examples, "\n"), "read_file native/src/example.cpp") {
		t.Fatalf("unexpected completion gate discovery pattern: %#v", ap)
	}
}

func TestDetectCompletionGateReopenedDiscoveryIgnoresImmediateWrite(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "write a refined brief"}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "completion_gate",
			"step":    3,
			"summary": "Mutation completion gate rejected final answer",
			"error":   "this task asks for a workspace change, but no successful write tool result is present",
		}},
		{Type: "tool_request", Data: map[string]any{
			"tool": "write_file",
			"path": ".idiom/refine/example/iter-2/draft.md",
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "write_file",
			"summary": "Wrote .idiom/refine/example/iter-2/draft.md",
			"error":   "",
		}},
	}
	rep := analyzeRecords("test", records)
	if hasPattern(rep, "completion_gate_reopened_discovery") {
		t.Fatalf("immediate write should not count as reopened discovery: %#v", rep.AntiPatterns)
	}
}

func TestDetectCompletionGateReopenedDiscoveryIgnoresRequiredPathSourceRead(t *testing.T) {
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool":    "completion_gate",
			"step":    3,
			"summary": "Plan-execute completion gate rejected final answer",
			"error":   "required edit path internal/runtime/agent/runner.go needs source evidence before submit_plan",
		}},
		{Type: "tool_request", Data: map[string]any{
			"tool": "read_file",
			"path": "internal/runtime/agent/runner.go",
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "read_file",
			"summary": "Read internal/runtime/agent/runner.go",
			"error":   "",
		}},
	}
	rep := analyzeRecords("test", records)
	if hasPattern(rep, "completion_gate_reopened_discovery") {
		t.Fatalf("required-path source read should not count as reopened discovery: %#v", rep.AntiPatterns)
	}
}

func TestDetectRedirectStormDistinguishesSoftGuidanceFromHardReject(t *testing.T) {
	softOnly := analyzeRecords("test", []Record{
		{Type: "run_event", Data: map[string]any{"role": "discovery_redirect", "content": "read_file -> go (go_file_outline)"}},
		{Type: "run_event", Data: map[string]any{"role": "discovery_redirect", "content": "read_file -> go (go_file_outline)"}},
	})
	if hasPattern(softOnly, "redirect_storm") {
		t.Fatalf("two soft redirects should not be a storm: %#v", softOnly.AntiPatterns)
	}

	hardReject := analyzeRecords("test", []Record{
		{Type: "run_event", Data: map[string]any{"role": "discovery_redirect", "content": "read_file -> go (go_file_outline) HARD_REJECT"}},
	})
	if !hasPattern(hardReject, "redirect_storm") {
		t.Fatalf("hard redirect should be reported: %#v", hardReject.AntiPatterns)
	}
}

func TestDetectRequiredPathsBlockedWithoutWrite(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "implement the brief"}},
		{Type: "completion_status", Data: map[string]any{
			"status":          "blocked",
			"reason":          "required_edit_paths_remaining",
			"blocker_kind":    "required_edit_paths_remaining",
			"edit_attempted":  false,
			"required_edits":  []any{"draft.md"},
			"changed_paths":   []any{},
			"user_visible":    true,
			"user_visible_id": "required_edit_paths_remaining",
		}},
	}
	rep := analyzeRecords("test", records)
	if !hasPattern(rep, "required_paths_blocked_without_write") {
		t.Fatalf("expected required_paths_blocked_without_write, got %#v", rep.AntiPatterns)
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

func TestDirectToolTelemetryDoesNotCreateBehaviorAntiPatterns(t *testing.T) {
	records := []Record{
		{Type: "session_start", Data: map[string]any{"command": "tool:go_read_symbol"}},
		{Type: "process_start", Data: map[string]any{"command": "tool:go_read_symbol"}},
		{Type: "tool_request", Data: map[string]any{"tool": "go_read_symbol", "action": "go_read_symbol:missing"}},
		{Type: "tool_result", Data: map[string]any{"tool": "go_read_symbol", "summary": `No Go symbols matched "missing"`}},
		{Type: "process_exit", Data: map[string]any{"command": "tool:go_read_symbol", "exit_code": 0}},
	}

	rep := analyzeRecords("direct-tool", records)
	if rep.ToolResults != 1 || rep.Tools.ByName["go_read_symbol"] != 1 {
		t.Fatalf("expected direct tool stats to remain available, got %+v", rep)
	}
	if len(rep.AntiPatterns) != 0 || rep.AntiPatternScore != 0 {
		t.Fatalf("expected direct tool telemetry to skip model behavior patterns, got %+v", rep.AntiPatterns)
	}
}

func TestAnalyzeModernSummaryRecords(t *testing.T) {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "implement feature"}},
		{Type: "model_call", Data: map[string]any{"prompt_tokens": 100, "completion_tokens": 10}},
		{Type: "tool_request", Data: map[string]any{"tool": "read_file", "action": "read_file:a.go"}},
		{Type: "tool_result", Data: map[string]any{"tool": "read_file", "summary": "Read a.go"}},
		{Type: "turn_summary", Data: map[string]any{"used_model_calls": 2, "used_tool_results": 1, "prompt_tokens": 200, "completion_tokens": 20, "duration_ms": 3000, "stop_reason": "model_final"}},
		{Type: "session_summary", Data: map[string]any{"model_calls": 2, "tool_calls": 1, "prompt_tokens": 200, "completion_tokens": 20, "duration_ms": 3001}},
	}

	rep := analyzeRecords("modern-session", records)
	if rep.ModelCalls != 2 || rep.ToolResults != 1 || rep.PromptTokens != 200 || rep.CompletionTokens != 20 || rep.DurationMillis != 3001 {
		t.Fatalf("modern summary records not parsed: %+v", rep)
	}
	if rep.StopReason != "model_final" {
		t.Fatalf("stop reason: want model_final, got %q", rep.StopReason)
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

func TestDetectRejectionRetryFromUnpairedRejectedResults(t *testing.T) {
	raw := `{"tool":"code_navigate","path":"a.go","capability":"file_outline"}`
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "edit a.go"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "code_navigate", "action": "code_navigate:a.go", "raw": raw, "summary": "Implementation target write-ready tool rejected", "error": "rejected"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "code_navigate", "action": "code_navigate:a.go", "raw": raw, "summary": "Implementation target write-ready tool rejected", "error": "rejected"}},
	}

	rep := analyzeRecords("test", records)
	if !hasPattern(rep, "rejection_retry") {
		t.Fatalf("expected unpaired rejected tool_result records to feed rejection_retry, got: %+v", rep.AntiPatterns)
	}
	if rep.RejectedActions != 2 {
		t.Fatalf("expected two logical rejected actions, got %d", rep.RejectedActions)
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

func TestDetectNamedGapReadWalk(t *testing.T) {
	records := buildSynth("", nil, []synthStep{
		{tool: "read_file", action: "read_file:src/item.cpp", raw: `{"path":"src/item.cpp","named_gap":"find_insert_anchor","start_line":1,"end_line":50}`, summary: "Read src/item.cpp lines 1-50"},
		{tool: "read_file", action: "read_file:src/item.cpp", raw: `{"path":"src/item.cpp","named_gap":"find_insert_anchor","start_line":50,"end_line":100}`, summary: "Read src/item.cpp lines 50-100"},
		{tool: "read_file", action: "read_file:src/item.cpp", raw: `{"path":"src/item.cpp","named_gap":"find_insert_anchor","start_line":100,"end_line":150}`, summary: "Read src/item.cpp lines 100-150"},
	})
	rep := analyzeRecords("test", records)
	ap := findPattern(rep, "named_gap_read_walk")
	if ap == nil {
		t.Fatalf("expected named_gap_read_walk, got %#v", rep.AntiPatterns)
	}
	if ap.Count != 3 {
		t.Fatalf("count=%d, want 3", ap.Count)
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

func TestAggregateRedactsWorstOffenderPrompt(t *testing.T) {
	agg := Aggregate([]PerfReport{{
		LogPath:          "logs/conversations/session.jsonl",
		SessionID:        "session",
		UserPrompt:       "private customer prompt with secret",
		ToolResults:      12,
		AntiPatternScore: 5,
		Tools:            ToolCounts{ByName: map[string]int{}},
	}})
	if len(agg.WorstOffenders) != 1 {
		t.Fatalf("expected one worst offender, got %#v", agg.WorstOffenders)
	}
	if !agg.WorstOffenders[0].LogExcerptRedacted {
		t.Fatalf("expected redaction marker, got %#v", agg.WorstOffenders[0])
	}
	encoded, err := json.Marshal(agg)
	if err != nil {
		t.Fatalf("marshal aggregate: %v", err)
	}
	if strings.Contains(string(encoded), "user_prompt") || strings.Contains(string(encoded), "private customer prompt") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("aggregate leaked prompt data: %s", string(encoded))
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
