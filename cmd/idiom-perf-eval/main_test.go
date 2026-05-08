package main

import (
	"bytes"
	"strings"
	"testing"

	"idiom/internal/eval"
)

func TestPrintAggregateIncludesContextTelemetry(t *testing.T) {
	agg := eval.PerfAggregate{
		LogCount: 2,
		Context: &eval.ContextStats{
			ToolResults:                 3,
			IndexedResults:              1,
			FreshResults:                2,
			StaleResults:                1,
			MaxFilesIndexed:             10,
			MaxSymbolsIndexed:           20,
			MaxEdgesIndexed:             30,
			IncrementalRefreshes:        1,
			ReusedIndexedFiles:          7,
			DependencyRefreshFiles:      2,
			AutoRefreshAttempts:         1,
			AutoRefreshFreshAfter:       1,
			AutoRefreshFilesIndexed:     3,
			AutoRefreshRefreshedFiles:   2,
			SummaryNodesReturned:        4,
			ModelSummaryNodesReturned:   1,
			CollectorVersionMismatches:  1,
			NormalizedRelationshipEdges: 6,
			StoreKindCounts:             map[string]int{"sqlite-graph-v1": 3},
			CollectorReportNameCounts:   map[string]int{"summary_index": 2},
		},
	}
	var out bytes.Buffer
	printAggregate(agg, 10, &out)
	text := out.String()
	for _, want := range []string{
		"context graph:",
		"incremental=1",
		"dependency_refresh_files=2",
		"auto=1",
		"fresh_after=1",
		"refreshed_files=2",
		"summary_nodes=4",
		"model_summary_nodes=1",
		"version_mismatches=1",
		"normalized_relationship_edges=6",
		"sqlite-graph-v1=3",
		"summary_index=2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected perf aggregate output to contain %q, got:\n%s", want, text)
		}
	}
}

func TestPrintAggregateIncludesHarnessFeedbackTelemetry(t *testing.T) {
	agg := eval.PerfAggregate{
		LogCount: 1,
		Feedback: &eval.FeedbackStats{
			Total:         2,
			Verified:      1,
			Inferred:      1,
			HighSeverity:  1,
			LowSeverity:   1,
			EvidenceItems: 3,
			ByCategory:    map[string]int{"tool_gap": 1, "prompt_gap": 1},
			ByComponent:   map[string]int{"context_pack": 2},
			ByConfidence:  map[string]int{"verified": 1, "inferred": 1},
			BySeverity:    map[string]int{"high": 1, "low": 1},
		},
	}
	var out bytes.Buffer
	printAggregate(agg, 10, &out)
	text := out.String()
	for _, want := range []string{
		"feedback:",
		"total=2",
		"verified=1",
		"evidence_items=3",
		"tool_gap=1",
		"context_pack=2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected perf aggregate output to contain %q, got:\n%s", want, text)
		}
	}
}
