package eval

import (
	"encoding/json"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"strings"
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

var _ = ginkgo.It("analyze budget ledger fields", func() {
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

	gomega.Expect(rep.ModelCalls).To(gomega.BeEquivalentTo(7), fmt.Sprintf("budget ledger not parsed: %+v", rep))
	gomega.Expect(rep.ToolResults).To(gomega.BeEquivalentTo(6), fmt.Sprintf("budget ledger not parsed: %+v", rep))
	gomega.Expect(rep.PromptTokens).To(gomega.BeEquivalentTo(12345), fmt.Sprintf("budget ledger not parsed: %+v", rep))
	gomega.Expect(rep.DurationMillis).To(gomega.BeEquivalentTo(9000), fmt.Sprintf("budget ledger not parsed: %+v", rep))

	gomega.Expect(rep.StopReason).To(gomega.BeEquivalentTo("model_final"), fmt.Sprintf("stop reason: want model_final, got %q", rep.StopReason))

	gomega.Expect(rep.UserPrompt).To(gomega.BeEquivalentTo("how does auth work"), fmt.Sprintf("user prompt: want 'how does auth work', got %q", rep.UserPrompt))
})
var _ = ginkgo.Describe("path stem normalization", func() {
	cases := []struct {
		path string
		want string
	}{
		{path: ` internal\evidence\eval\perf.go `, want: "internal/evidence"},
		{path: "/README.md/", want: "README.md"},
		{path: "  ", want: ""},
	}
	for _, tt := range cases {
		tt := tt
		ginkgo.It(tt.path, func() {
			{
				got := pathStemOf(tt.path)
				gomega.Expect(got).To(gomega.BeEquivalentTo(tt.want), fmt.Sprintf("pathStemOf(%q) = %q, want %q", tt.path, got, tt.want))
			}
		})
	}
})
var _ = ginkgo.It("detect dir walk streak", func() {
	records := buildSynth("", nil, []synthStep{
		{tool: "list_files", action: "list_files:.", raw: `{"path":"."}`, summary: "Listed 10 path(s)."},
		{tool: "list_files", action: "list_files:a", raw: `{"path":"a"}`, summary: "Listed 5 path(s)."},
		{tool: "list_files", action: "list_files:b", raw: `{"path":"b"}`, summary: "Listed 3 path(s)."},
		{tool: "read_file", action: "read_file:x.go", raw: `{"path":"x.go"}`, summary: "Read x.go"},
	})
	rep := analyzeRecords("test", records)

	gomega.Expect(hasPattern(rep, "dir_walk_streak")).To(gomega.BeTrue(), fmt.Sprintf("expected dir_walk_streak, got: %+v", rep.AntiPatterns))
})
var _ = ginkgo.It("analyze log ingests context telemetry fields", func() {
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

	gomega.Expect(rep.Context).NotTo(gomega.BeNil(), fmt.Sprintf("expected context telemetry summary"))

	gomega.Expect(rep.Context.ToolResults).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected context freshness summary: %#v", rep.Context))
	gomega.Expect(rep.Context.FreshResults).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context freshness summary: %#v", rep.Context))
	gomega.Expect(rep.Context.StaleResults).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context freshness summary: %#v", rep.Context))

	gomega.Expect(rep.Context.MaxFilesIndexed).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected context graph maxima: %#v", rep.Context))
	gomega.Expect(rep.Context.MaxSymbolsIndexed).To(gomega.BeEquivalentTo(7), fmt.Sprintf("unexpected context graph maxima: %#v", rep.Context))
	gomega.Expect(rep.Context.MaxEdgesIndexed).To(gomega.BeEquivalentTo(11), fmt.Sprintf("unexpected context graph maxima: %#v", rep.Context))

	gomega.Expect(rep.Context.NodesReturned).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected context query counts: %#v", rep.Context))
	gomega.Expect(rep.Context.EdgesReturned).To(gomega.BeEquivalentTo(4), fmt.Sprintf("unexpected context query counts: %#v", rep.Context))

	gomega.Expect(rep.Context.StoreKindCounts["sqlite-graph-v1"]).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected context backend counts: %#v", rep.Context))
	gomega.Expect(rep.Context.ActiveBackendCounts["sqlite-graph-v1"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context backend counts: %#v", rep.Context))

	gomega.Expect(rep.Context.CollectorReports).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected context collector telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.CollectorCoverage).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected context collector telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.CollectorUnavailable).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context collector telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.CollectorVersionMismatches).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context collector telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.EnrichmentDiagnostics).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected context collector telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.CollectorReportNameCounts["python_lsp"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context collector telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.CollectorCoverageNameCounts["python_index"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context collector telemetry: %#v", rep.Context))

	gomega.Expect(rep.Context.MaxStaleSymbols).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected context stale/relationship telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.StaleSymbolSamples).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context stale/relationship telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.RefreshedStaleSymbols).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context stale/relationship telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.NormalizedRelationshipEdges).To(gomega.BeEquivalentTo(4), fmt.Sprintf("unexpected context stale/relationship telemetry: %#v", rep.Context))

	gomega.Expect(rep.Context.IncrementalRefreshes).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context incremental/summary telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.ReusedIndexedFiles).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected context incremental/summary telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.DependencyRefreshFiles).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context incremental/summary telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.SummaryNodesReturned).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context incremental/summary telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.ModelSummaryNodesReturned).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context incremental/summary telemetry: %#v", rep.Context))

	gomega.Expect(rep.Context.AutoRefreshAttempts).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context auto-refresh telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.AutoRefreshFilesIndexed).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context auto-refresh telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.AutoRefreshRefreshedFiles).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context auto-refresh telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.AutoRefreshFreshAfter).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context auto-refresh telemetry: %#v", rep.Context))
	gomega.Expect(rep.Context.AutoRefreshReasonCounts["small_stale_file_set"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected context auto-refresh telemetry: %#v", rep.Context))

	agg := Aggregate([]PerfReport{rep})
	gomega.Expect(agg.Context).NotTo(gomega.BeNil(), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.ToolResults).To(gomega.BeEquivalentTo(2), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.StorageBackendStatusCounts["sqlite-graph-v1:active"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.CollectorReportNameCounts["workspace_tool_enrichment"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.CollectorCoverageNameCounts["language_environment"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.CollectorVersionMismatches).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.NormalizedRelationshipEdges).To(gomega.BeEquivalentTo(4), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.IncrementalRefreshes).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.ReusedIndexedFiles).To(gomega.BeEquivalentTo(2), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.DependencyRefreshFiles).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.SummaryNodesReturned).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.ModelSummaryNodesReturned).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.AutoRefreshAttempts).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
	gomega.Expect(agg.Context.AutoRefreshReasonCounts["small_stale_file_set"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve context telemetry: %#v", agg.Context))
})
var _ = ginkgo.It("analyze log distinguishes stale evidence states", func() {
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

	gomega.Expect(rep.Context).NotTo(gomega.BeNil(), fmt.Sprintf("expected context telemetry summary"))

	gomega.Expect(rep.Context.FreshnessBlockers).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected only blocked stale evidence to count as freshness blocker, got %#v", rep.Context))
	gomega.Expect(rep.Context.FreshnessBudgetExceeded).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected only blocked stale evidence to count as freshness blocker, got %#v", rep.Context))

	gomega.Expect(rep.Context.StaleUseBlocked).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counters: %#v", rep.Context))
	gomega.Expect(rep.Context.StaleUseReported).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counters: %#v", rep.Context))
	gomega.Expect(rep.Context.StaleUseAllowed).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counters: %#v", rep.Context))
	gomega.Expect(rep.Context.StaleUseFresh).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counters: %#v", rep.Context))

	gomega.Expect(rep.Context.StaleUseStateCounts["blocked"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counts: %#v", rep.Context.StaleUseStateCounts))
	gomega.Expect(rep.Context.StaleUseStateCounts["reported"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counts: %#v", rep.Context.StaleUseStateCounts))
	gomega.Expect(rep.Context.StaleUseStateCounts["allowed_stale"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counts: %#v", rep.Context.StaleUseStateCounts))
	gomega.Expect(rep.Context.StaleUseStateCounts["fresh"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected stale-evidence state counts: %#v", rep.Context.StaleUseStateCounts))
})
var _ = ginkgo.It("analyze log ingests harness feedback telemetry fields", func() {
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

	gomega.Expect(rep.Feedback).NotTo(gomega.BeNil(), fmt.Sprintf("expected feedback telemetry summary"))

	gomega.Expect(rep.Feedback.Total).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected feedback summary: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.Verified).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback summary: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.Inferred).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback summary: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.MediumSeverity).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback summary: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.HighSeverity).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback summary: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.EvidenceItems).To(gomega.BeEquivalentTo(3), fmt.Sprintf("unexpected feedback summary: %#v", rep.Feedback))

	gomega.Expect(rep.Feedback.ByCategory["tool_gap"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback buckets: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.ByCategory["impact_ranking"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback buckets: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.ByPlanningCategory["impact_ranking"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback buckets: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.PlanningQualityTotal).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback buckets: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.ByComponent["context_pack"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback buckets: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.ByConfidence["verified"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback buckets: %#v", rep.Feedback))
	gomega.Expect(rep.Feedback.BySeverity["high"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected feedback buckets: %#v", rep.Feedback))

	agg := Aggregate([]PerfReport{rep})
	gomega.Expect(agg.Feedback).NotTo(gomega.BeNil(), fmt.Sprintf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback))
	gomega.Expect(agg.Feedback.Total).To(gomega.BeEquivalentTo(2), fmt.Sprintf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback))
	gomega.Expect(agg.Feedback.HighSeverity).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback))
	gomega.Expect(agg.Feedback.PlanningQualityTotal).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback))
	gomega.Expect(agg.Feedback.ByCategory["impact_ranking"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback))
	gomega.Expect(agg.Feedback.ByComponent["prompt/base"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve feedback telemetry: %#v", agg.Feedback))
})
var _ = ginkgo.It("analyze log ingests local ops telemetry fields", func() {
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
	gomega.Expect(rep.LocalOps).NotTo(gomega.BeNil(), fmt.Sprint("expected local ops telemetry summary"))

	gomega.Expect(rep.LocalOps.BackendConfigured).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected local ops telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.ContextBudgetDegraded).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected local ops telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.ClassifierDegraded).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected local ops telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.QueueDegraded).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected local ops telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.QueueMaxWaitMillis).To(gomega.BeEquivalentTo(42), fmt.Sprintf("unexpected local ops telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.ImplementCloseouts).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected local ops telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.DoctorDegradedProfiles).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected local ops telemetry: %#v", rep.LocalOps))

	agg := Aggregate([]PerfReport{rep})
	gomega.Expect(agg.LocalOps).NotTo(gomega.BeNil(), fmt.Sprintf("aggregate did not preserve local ops telemetry: %#v", agg.LocalOps))
	gomega.Expect(agg.LocalOps.QueueDegraded).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve local ops telemetry: %#v", agg.LocalOps))
	gomega.Expect(agg.LocalOps.ImplementCloseouts).To(gomega.BeEquivalentTo(1), fmt.Sprintf("aggregate did not preserve local ops telemetry: %#v", agg.LocalOps))
})
var _ = ginkgo.It("analyze log counts plan execute queue events", func() {
	rep := analyzeRecords("test", []Record{{
		Type: "idiom.plan_execute.queue_unavailable",
		Data: map[string]any{"state": "unavailable", "pool": "qwen-coder", "wait_ms": 17, "degraded": true},
	}})
	gomega.Expect(rep.LocalOps).NotTo(gomega.BeNil(), fmt.Sprint("expected local ops telemetry summary"))
	gomega.Expect(rep.LocalOps.QueueEvents).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected queue telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.QueueDegraded).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected queue telemetry: %#v", rep.LocalOps))
	gomega.Expect(rep.LocalOps.QueueMaxWaitMillis).To(gomega.BeEquivalentTo(17), fmt.Sprintf("unexpected queue telemetry: %#v", rep.LocalOps))
})
var _ = ginkgo.It("detect prompt surface overlap", func() {
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

	gomega.Expect(ap).NotTo(gomega.BeNil(), fmt.Sprintf("expected prompt_surface_overlap"))

	gomega.Expect(ap.Count).To(gomega.BeEquivalentTo(2), fmt.Sprintf("prompt_surface_overlap count: want 2, got %d", ap.Count))
})
var _ = ginkgo.It("detect completion gate reopened discovery", func() {
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

	gomega.Expect(ap).NotTo(gomega.BeNil(), fmt.Sprintf("expected completion_gate_reopened_discovery, got %#v", rep.AntiPatterns))

	gomega.Expect(ap.Count).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected completion gate discovery pattern: %#v", ap))
	gomega.Expect(strings.Join(ap.Examples, "\n")).To(gomega.ContainSubstring("read_file native/src/example.cpp"), fmt.Sprintf("unexpected completion gate discovery pattern: %#v", ap))
})
var _ = ginkgo.It("detect completion gate reopened discovery ignores immediate write", func() {
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

	gomega.Expect(hasPattern(rep, "completion_gate_reopened_discovery")).To(gomega.BeFalse(), fmt.Sprintf("immediate write should not count as reopened discovery: %#v", rep.AntiPatterns))
})
var _ = ginkgo.It("detect completion gate reopened discovery ignores required path source read", func() {
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

	gomega.Expect(hasPattern(rep, "completion_gate_reopened_discovery")).To(gomega.BeFalse(), fmt.Sprintf("required-path source read should not count as reopened discovery: %#v", rep.AntiPatterns))
})
var _ = ginkgo.It("detect redirect storm distinguishes soft guidance from hard reject", func() {
	softOnly := analyzeRecords("test", []Record{
		{Type: "run_event", Data: map[string]any{"role": "discovery_redirect", "content": "read_file -> go (go_file_outline)"}},
		{Type: "run_event", Data: map[string]any{"role": "discovery_redirect", "content": "read_file -> go (go_file_outline)"}},
	})
	gomega.Expect(hasPattern(softOnly, "redirect_storm")).To(gomega.BeFalse(), fmt.Sprintf("two soft redirects should not be a storm: %#v", softOnly.AntiPatterns))

	hardReject := analyzeRecords("test", []Record{
		{Type: "run_event", Data: map[string]any{"role": "discovery_redirect", "content": "read_file -> go (go_file_outline) HARD_REJECT"}},
	})
	gomega.Expect(hasPattern(hardReject, "redirect_storm")).To(gomega.BeTrue(), fmt.Sprintf("hard redirect should be reported: %#v", hardReject.AntiPatterns))
})
var _ = ginkgo.It("detect required paths blocked without write", func() {
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

	gomega.Expect(hasPattern(rep, "required_paths_blocked_without_write")).To(gomega.BeTrue(), fmt.Sprintf("expected required_paths_blocked_without_write, got %#v", rep.AntiPatterns))
})
var _ = ginkgo.It("dir walk streak does not fire on two listings", func() {
	records := buildSynth("", nil, []synthStep{
		{tool: "list_files", action: "list_files:.", summary: "Listed 10 path(s)."},
		{tool: "list_files", action: "list_files:a", summary: "Listed 5 path(s)."},
		{tool: "search_content", action: "search_content:.:foo:_", summary: "Found 3 matching line(s)."},
	})
	rep := analyzeRecords("test", records)

	gomega.Expect(hasPattern(rep, "dir_walk_streak")).To(gomega.BeFalse(), fmt.Sprintf("dir_walk_streak should not fire on just 2 list_files"))
})
var _ = ginkgo.It("detect zero hit search", func() {
	records := buildSynth("", nil, []synthStep{
		{tool: "search_files", action: "search_files:_:foo*:_", raw: `{"pattern":"foo*"}`, summary: `No file matches for "foo*"`},
		{tool: "go_find_symbol", action: "go_find_symbol:bar", raw: `{"pattern":"bar"}`, summary: `No Go symbols matched "bar"`},
	})
	rep := analyzeRecords("test", records)
	ap := findPattern(rep, "zero_hit_search")

	gomega.Expect(ap).NotTo(gomega.BeNil(), fmt.Sprintf("expected zero_hit_search pattern"))

	gomega.Expect(ap.Count).To(gomega.BeEquivalentTo(2), fmt.Sprintf("zero_hit_search count: want 2, got %d", ap.Count))
})
var _ = ginkgo.It("direct tool telemetry does not create behavior anti patterns", func() {
	records := []Record{
		{Type: "session_start", Data: map[string]any{"command": "tool:go_read_symbol"}},
		{Type: "process_start", Data: map[string]any{"command": "tool:go_read_symbol"}},
		{Type: "tool_request", Data: map[string]any{"tool": "go_read_symbol", "action": "go_read_symbol:missing"}},
		{Type: "tool_result", Data: map[string]any{"tool": "go_read_symbol", "summary": `No Go symbols matched "missing"`}},
		{Type: "process_exit", Data: map[string]any{"command": "tool:go_read_symbol", "exit_code": 0}},
	}

	rep := analyzeRecords("direct-tool", records)
	gomega.Expect(rep.ToolResults).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected direct tool stats to remain available, got %+v", rep))
	gomega.Expect(rep.Tools.ByName["go_read_symbol"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected direct tool stats to remain available, got %+v", rep))

	gomega.Expect(rep.AntiPatterns).To(gomega.HaveLen(0), fmt.Sprintf("expected direct tool telemetry to skip model behavior patterns, got %+v", rep.AntiPatterns))
	gomega.Expect(rep.AntiPatternScore).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected direct tool telemetry to skip model behavior patterns, got %+v", rep.AntiPatterns))
})
var _ = ginkgo.It("analyze modern summary records", func() {
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "implement feature"}},
		{Type: "model_call", Data: map[string]any{"prompt_tokens": 100, "completion_tokens": 10}},
		{Type: "tool_request", Data: map[string]any{"tool": "read_file", "action": "read_file:a.go"}},
		{Type: "tool_result", Data: map[string]any{"tool": "read_file", "summary": "Read a.go"}},
		{Type: "turn_summary", Data: map[string]any{"used_model_calls": 2, "used_tool_results": 1, "prompt_tokens": 200, "completion_tokens": 20, "duration_ms": 3000, "stop_reason": "model_final"}},
		{Type: "session_summary", Data: map[string]any{"model_calls": 2, "tool_calls": 1, "prompt_tokens": 200, "completion_tokens": 20, "duration_ms": 3001}},
	}

	rep := analyzeRecords("modern-session", records)
	gomega.Expect(rep.ModelCalls).To(gomega.BeEquivalentTo(2), fmt.Sprintf("modern summary records not parsed: %+v", rep))
	gomega.Expect(rep.ToolResults).To(gomega.BeEquivalentTo(1), fmt.Sprintf("modern summary records not parsed: %+v", rep))
	gomega.Expect(rep.PromptTokens).To(gomega.BeEquivalentTo(200), fmt.Sprintf("modern summary records not parsed: %+v", rep))
	gomega.Expect(rep.CompletionTokens).To(gomega.BeEquivalentTo(20), fmt.Sprintf("modern summary records not parsed: %+v", rep))
	gomega.Expect(rep.DurationMillis).To(gomega.BeEquivalentTo(3001), fmt.Sprintf("modern summary records not parsed: %+v", rep))

	gomega.Expect(rep.StopReason).To(gomega.BeEquivalentTo("model_final"), fmt.Sprintf("stop reason: want model_final, got %q", rep.StopReason))
})
var _ = ginkgo.It("detect rejection retry", func() {
	raw := `{"tool":"list_files","path":"docs"}`
	records := buildSynth("", nil, []synthStep{
		{tool: "list_files", action: "list_files:docs", raw: raw, summary: "Chained list_files rejected", err: "gate fired"},
		{tool: "list_files", action: "list_files:docs", raw: raw, summary: "Rejected again", err: "same"},
	})
	rep := analyzeRecords("test", records)

	gomega.Expect(hasPattern(rep, "rejection_retry")).To(gomega.BeTrue(), fmt.Sprintf("expected rejection_retry, got: %+v", rep.AntiPatterns))
})
var _ = ginkgo.It("detect rejection retry from unpaired rejected results", func() {
	raw := `{"tool":"code_navigate","path":"a.go","capability":"file_outline"}`
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "edit a.go"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "code_navigate", "action": "code_navigate:a.go", "raw": raw, "summary": "Implementation target write-ready tool rejected", "error": "rejected"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "code_navigate", "action": "code_navigate:a.go", "raw": raw, "summary": "Implementation target write-ready tool rejected", "error": "rejected"}},
	}

	rep := analyzeRecords("test", records)
	gomega.Expect(hasPattern(rep, "rejection_retry")).To(gomega.BeTrue(), fmt.Sprintf("expected unpaired rejected tool_result records to feed rejection_retry, got: %+v", rep.AntiPatterns))

	gomega.Expect(rep.RejectedActions).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected two logical rejected actions, got %d", rep.RejectedActions))
})
var _ = ginkgo.It("detect same tool repeat only at three or more", func() {
	raw := `{"tool":"read_file","path":"x.go"}`
	records := buildSynth("", nil, []synthStep{
		{tool: "read_file", action: "read_file:x.go", raw: raw, summary: "Read 1"},
		{tool: "read_file", action: "read_file:x.go", raw: raw, summary: "Read 2"},
	})
	rep := analyzeRecords("test", records)

	gomega.Expect(hasPattern(rep, "same_tool_repeat")).To(gomega.BeFalse(), fmt.Sprintf("same_tool_repeat should not fire at 2 identical calls"))

	// Now three.
	records = append(records,
		Record{Type: "tool_request", Data: map[string]any{"tool": "read_file", "action": "read_file:x.go", "raw": raw}},
		Record{Type: "tool_result", Data: map[string]any{"tool": "read_file", "summary": "Read 3"}},
	)
	rep = analyzeRecords("test", records)

	gomega.Expect(hasPattern(rep, "same_tool_repeat")).To(gomega.BeTrue(), fmt.Sprintf("same_tool_repeat should fire at 3 identical calls"))
})
var _ = ginkgo.It("detect same tool repeat ignores recovered mutation no write loop", func() {
	raw := `{"tool":"read_file","path":"missing.html","named_gap":"source before write"}`
	records := buildSynth("", nil, []synthStep{
		{tool: "read_file", action: "read_file:missing.html", raw: raw, summary: "Failed to resolve file target", err: "missing"},
		{tool: "read_file", action: "read_file:missing.html", raw: raw, summary: "Failed to resolve file target (replayed same blocker)", err: "missing"},
		{tool: "read_file", action: "read_file:missing.html", raw: raw, summary: "Mutation write required after repeated non-write request", err: "repeated"},
	})
	records = append(records, Record{Type: "run_event", Data: map[string]any{
		"role":    "mutation_no_write_loop_recovery",
		"content": "read_file was repeated 3 times before any successful workspace write",
	}})

	rep := analyzeRecords("test", records)
	gomega.Expect(hasPattern(rep, "same_tool_repeat")).To(gomega.BeFalse(), fmt.Sprintf("recovered mutation loop should not remain a current same_tool_repeat finding: %+v", rep.AntiPatterns))
})
var _ = ginkgo.It("detect named gap read walk", func() {
	records := buildSynth("", nil, []synthStep{
		{tool: "read_file", action: "read_file:src/item.cpp", raw: `{"path":"src/item.cpp","named_gap":"find_insert_anchor","start_line":1,"end_line":50}`, summary: "Read src/item.cpp lines 1-50"},
		{tool: "read_file", action: "read_file:src/item.cpp", raw: `{"path":"src/item.cpp","named_gap":"find_insert_anchor","start_line":50,"end_line":100}`, summary: "Read src/item.cpp lines 50-100"},
		{tool: "read_file", action: "read_file:src/item.cpp", raw: `{"path":"src/item.cpp","named_gap":"find_insert_anchor","start_line":100,"end_line":150}`, summary: "Read src/item.cpp lines 100-150"},
	})
	rep := analyzeRecords("test", records)
	ap := findPattern(rep, "named_gap_read_walk")

	gomega.Expect(ap).NotTo(gomega.BeNil(), fmt.Sprintf("expected named_gap_read_walk, got %#v", rep.AntiPatterns))

	gomega.Expect(ap.Count).To(gomega.BeEquivalentTo(3), fmt.Sprintf("count=%d, want 3", ap.Count))
})
var _ = ginkgo.It("detect named gap read walk ignores recovered mutation no write loop", func() {
	records := buildSynth("", nil, []synthStep{
		{tool: "read_file", action: "read_file:missing.html", raw: `{"path":"missing.html","named_gap":"source before write","start_line":1,"end_line":50}`, summary: "Failed to resolve file target", err: "missing"},
		{tool: "read_file", action: "read_file:missing.html", raw: `{"path":"missing.html","named_gap":"source before write","start_line":1,"end_line":50}`, summary: "Failed to resolve file target (replayed same blocker)", err: "missing"},
		{tool: "read_file", action: "read_file:missing.html", raw: `{"path":"missing.html","named_gap":"source before write","start_line":1,"end_line":50}`, summary: "Mutation write required after repeated non-write request", err: "repeated"},
	})
	records = append(records, Record{Type: "run_event", Data: map[string]any{
		"role":    "mutation_no_write_loop_recovery",
		"content": "read_file was repeated 3 times before any successful workspace write",
	}})

	rep := analyzeRecords("test", records)
	gomega.Expect(hasPattern(rep, "named_gap_read_walk")).To(gomega.BeFalse(), fmt.Sprintf("recovered mutation loop should not remain a current named_gap_read_walk finding: %+v", rep.AntiPatterns))
})
var _ = ginkgo.It("aggregate ranks by log hits", func() {
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

	gomega.Expect(agg.LogCount).To(gomega.BeEquivalentTo(4), fmt.Sprintf("log count: want 4, got %d", agg.LogCount))

	gomega.Expect(agg.PatternHitRate).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected pattern rows"))

	gomega.Expect(agg.PatternHitRate[0].Name).To(gomega.BeEquivalentTo("zero_hit_search"), fmt.Sprintf("expected zero_hit_search leading with 3 logs hit, got %+v", agg.PatternHitRate[0]))
	gomega.Expect(agg.PatternHitRate[0].LogsHit).To(gomega.BeEquivalentTo(3), fmt.Sprintf("expected zero_hit_search leading with 3 logs hit, got %+v", agg.PatternHitRate[0]))
})
var _ = ginkgo.It("aggregate redacts worst offender prompt", func() {
	agg := Aggregate([]PerfReport{{
		LogPath:          "logs/conversations/session.jsonl",
		SessionID:        "session",
		UserPrompt:       "private customer prompt with secret",
		ToolResults:      12,
		AntiPatternScore: 5,
		Tools:            ToolCounts{ByName: map[string]int{}},
	}})
	gomega.Expect(agg.WorstOffenders).To(gomega.HaveLen(1), fmt.Sprintf("expected one worst offender, got %#v", agg.WorstOffenders))

	gomega.Expect(agg.WorstOffenders[0].LogExcerptRedacted).To(gomega.BeTrue(), fmt.Sprintf("expected redaction marker, got %#v", agg.WorstOffenders[0]))

	encoded, err := json.Marshal(agg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal aggregate: %v", err))

	gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("user_prompt"), fmt.Sprintf("aggregate leaked prompt data: %s", string(encoded)))
	gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("private customer prompt"), fmt.Sprintf("aggregate leaked prompt data: %s", string(encoded)))
	gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("secret"), fmt.Sprintf("aggregate leaked prompt data: %s", string(encoded)))
})

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
