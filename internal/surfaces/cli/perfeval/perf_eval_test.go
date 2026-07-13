package perfeval

import (
	"bytes"
	"encoding/json"

	"idiom/internal/evidence/eval"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Performance evaluation output", func() {
	ginkgo.It("includes context graph telemetry in aggregate output", func() {
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

		gomega.Expect(out.String()).To(gomega.SatisfyAll(
			gomega.ContainSubstring("context graph:"),
			gomega.ContainSubstring("incremental=1"),
			gomega.ContainSubstring("dependency_refresh_files=2"),
			gomega.ContainSubstring("auto=1"),
			gomega.ContainSubstring("fresh_after=1"),
			gomega.ContainSubstring("refreshed_files=2"),
			gomega.ContainSubstring("summary_nodes=4"),
			gomega.ContainSubstring("model_summary_nodes=1"),
			gomega.ContainSubstring("version_mismatches=1"),
			gomega.ContainSubstring("normalized_relationship_edges=6"),
			gomega.ContainSubstring("sqlite-graph-v1=3"),
			gomega.ContainSubstring("summary_index=2"),
		))
	})

	ginkgo.It("includes harness feedback telemetry in aggregate output", func() {
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

		gomega.Expect(out.String()).To(gomega.SatisfyAll(
			gomega.ContainSubstring("feedback:"),
			gomega.ContainSubstring("total=2"),
			gomega.ContainSubstring("verified=1"),
			gomega.ContainSubstring("evidence_items=3"),
			gomega.ContainSubstring("tool_gap=1"),
			gomega.ContainSubstring("context_pack=2"),
		))
	})

	ginkgo.It("redacts the user prompt from a report", func() {
		report := eval.PerfReport{
			LogPath:    "logs/conversations/session.jsonl",
			UserPrompt: "private customer prompt with secret",
			Tools:      eval.ToolCounts{ByName: map[string]int{}},
		}

		var out bytes.Buffer
		printReport(report, &out)

		gomega.Expect(out.String()).To(gomega.ContainSubstring("user:         [redacted]"))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("private customer prompt"))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("secret"))
	})

	ginkgo.It("redacts the worst offender prompt from aggregate output", func() {
		agg := eval.Aggregate([]eval.PerfReport{{
			LogPath:          "logs/conversations/session.jsonl",
			SessionID:        "session",
			UserPrompt:       "private customer prompt with secret",
			AntiPatternScore: 5,
			ToolResults:      3,
			Tools:            eval.ToolCounts{ByName: map[string]int{}},
		}})

		var out bytes.Buffer
		printAggregate(agg, 10, &out)

		gomega.Expect(out.String()).To(gomega.ContainSubstring("prompt excerpt redacted"))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("user_prompt"))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("private customer prompt"))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("secret"))
	})

	ginkgo.It("redacts the report user prompt from JSON output", func() {
		reports := []eval.PerfReport{{
			LogPath:        "logs/conversations/session.jsonl",
			UserPrompt:     "private customer prompt with secret",
			PromptRedacted: true,
			Tools:          eval.ToolCounts{ByName: map[string]int{}},
		}}

		var out bytes.Buffer
		gomega.Expect(emitJSON(reports, false, &out)).To(gomega.Equal(0))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("user_prompt"))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("private customer prompt"))
		gomega.Expect(out.String()).NotTo(gomega.ContainSubstring("secret"))

		var payload struct {
			PromptRedacted bool `json:"prompt_redacted"`
		}
		gomega.Expect(json.Unmarshal(out.Bytes(), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.PromptRedacted).To(gomega.BeTrue())
	})
})
