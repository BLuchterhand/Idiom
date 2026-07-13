package lanehistory

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Lane history", func() {
	var (
		root        string
		appendEvent func(Event)
	)

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		appendEvent = func(event Event) {
			Expect(Append(root, event)).To(Succeed())
		}
	})

	It("appends dispatches and summarizes their dimensions", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0})
		appendEvent(Event{Mode: "implement", Brief: "b.md", ExitCode: 1})
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0})

		summary, err := Summarize(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(3))
		Expect(summary.ByMode).To(HaveKeyWithValue("plan-execute", 2))
		Expect(summary.ByMode).To(HaveKeyWithValue("implement", 1))
		Expect(summary.ByExitCode).To(HaveKeyWithValue(0, 2))
		Expect(summary.ByExitCode).To(HaveKeyWithValue(1, 1))
		Expect(summary.BriefsByPath).To(HaveKeyWithValue("a.md", 2))
	})

	It("returns an empty summary when no history file exists", func() {
		summary, err := Summarize(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(BeZero())
	})

	It("skips malformed history lines", func() {
		appendEvent(Event{Mode: "implement", Brief: "x.md", ExitCode: 0})
		path := filepath.Join(root, ".idiom", "lane-history.jsonl")
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		Expect(err).NotTo(HaveOccurred())
		_, err = file.WriteString("this is not json\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(file.Close()).To(Succeed())

		summary, err := Summarize(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(1))
	})

	It("detects refine-drafted, direct, and unknown brief provenance", func() {
		refineDrafted := filepath.Join(root, "refine.md")
		Expect(os.WriteFile(refineDrafted, []byte("<!-- idiom:refine-drafted title=foo iter=2 reason=ship-as-is -->\n# Brief body\n"), 0o644)).To(Succeed())
		claudeDirect := filepath.Join(root, "claude.md")
		Expect(os.WriteFile(claudeDirect, []byte("# Direct brief, no marker\n"), 0o644)).To(Succeed())

		for path, expected := range map[string]string{
			refineDrafted:                     "refine-drafted",
			claudeDirect:                      "claude-direct",
			"":                                "unknown",
			filepath.Join(root, "missing.md"): "unknown",
		} {
			Expect(DetectBriefProvenance(path)).To(Equal(expected), "path %q", path)
		}
	})

	It("prints an explicit zero-dispatch summary", func() {
		var output bytes.Buffer
		Expect(PrintSummary(root, &output, Filter{})).To(Succeed())
		Expect(output.String()).To(ContainSubstring("0 dispatch"))
	})

	It("filters out events older than the requested start time", func() {
		now := time.Now().UTC()
		appendEvent(Event{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0})
		appendEvent(Event{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1})
		appendEvent(Event{Timestamp: now.Add(-10 * time.Minute).Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0})

		summary, err := SummarizeWithFilter(root, Filter{Since: now.Add(-time.Hour)})
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(2))
	})

	It("filters to failed dispatches", func() {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		appendEvent(Event{Timestamp: now, Mode: "implement", Brief: "a.md", ExitCode: 0})
		appendEvent(Event{Timestamp: now, Mode: "plan-execute", Brief: "b.md", ExitCode: 1})
		appendEvent(Event{Timestamp: now, Mode: "refine", Brief: "c.md", ExitCode: 0})

		summary, err := SummarizeWithFilter(root, Filter{FailedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(1))
		Expect(summary.ByExitCode[0]).To(BeZero())
		Expect(summary.ByExitCode).To(HaveKeyWithValue(1, 1))
	})

	It("combines time and failure filters", func() {
		now := time.Now().UTC()
		appendEvent(Event{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0})
		appendEvent(Event{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1})
		appendEvent(Event{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0})
		appendEvent(Event{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "implement", Brief: "d.md", ExitCode: 2})

		summary, err := SummarizeWithFilter(root, Filter{Since: now.Add(-time.Hour), FailedOnly: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(1))
		Expect(summary.ByExitCode).To(HaveKeyWithValue(2, 1))
	})

	It("produces the original summary for an empty filter", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0})
		appendEvent(Event{Mode: "implement", Brief: "b.md", ExitCode: 1})
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0})

		original, err := Summarize(root)
		Expect(err).NotTo(HaveOccurred())
		filtered, err := SummarizeWithFilter(root, Filter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(filtered.Total).To(Equal(original.Total))
		Expect(filtered.ByMode).To(Equal(original.ByMode))
		Expect(filtered.ByExitCode).To(Equal(original.ByExitCode))
		Expect(filtered.BriefsByPath).To(Equal(original.BriefsByPath))
	})

	It("filters dispatches by mode", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0})
		appendEvent(Event{Mode: "plan-execute", Brief: "b.md", ExitCode: 1})
		appendEvent(Event{Mode: "implement", Brief: "c.md", ExitCode: 0})
		appendEvent(Event{Mode: "refine", Brief: "d.md", ExitCode: 0})

		summary, err := SummarizeWithFilter(root, Filter{ModeFilter: "plan-execute"})
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(2))
		Expect(summary.ByMode["implement"]).To(BeZero())
		Expect(summary.ByMode["refine"]).To(BeZero())
	})

	It("requires events to satisfy time, failure, and mode filters together", func() {
		now := time.Now().UTC()
		appendEvent(Event{Mode: "plan-execute", ExitCode: 1, Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano)})
		appendEvent(Event{Mode: "plan-execute", ExitCode: 0, Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano)})
		appendEvent(Event{Mode: "implement", ExitCode: 1, Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano)})
		appendEvent(Event{Mode: "plan-execute", ExitCode: 1, Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano)})

		summary, err := SummarizeWithFilter(root, Filter{
			Since:      now.Add(-time.Hour),
			FailedOnly: true,
			ModeFilter: "plan-execute",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(1))
	})

	It("aggregates token usage only for events carrying token data", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0, PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200})
		appendEvent(Event{Mode: "implement", Brief: "b.md", ExitCode: 1, PromptTokens: 5000, CompletionTokens: 800, TotalTokens: 5800})
		appendEvent(Event{Mode: "plan-execute", Brief: "c.md", ExitCode: 0})

		summary, err := Summarize(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.Total).To(Equal(3))
		Expect(summary.EventsWithTokens).To(Equal(2))
		Expect(summary.TotalPromptTokens).To(Equal(6000))
		Expect(summary.TotalCompletionTokens).To(Equal(1000))
		Expect(summary.TotalTokens).To(Equal(7000))
	})

	It("aggregates run metrics and final states", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0, FinalState: "success", ModelCalls: 3, ToolCalls: 2, DurationMS: 1000})
		appendEvent(Event{Mode: "plan-execute", Brief: "b.md", ExitCode: 4, FinalState: "degraded", ModelCalls: 5, ToolCalls: 6, DurationMS: 3000, Degradations: []string{"no_edit_attempt"}})

		summary, err := Summarize(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.ByFinalState).To(HaveKeyWithValue("success", 1))
		Expect(summary.ByFinalState).To(HaveKeyWithValue("degraded", 1))
		Expect(summary.EventsWithRunMetrics).To(Equal(2))
		Expect(summary.TotalModelCalls).To(Equal(8))
		Expect(summary.TotalToolCalls).To(Equal(8))
		Expect(summary.TotalDurationMS).To(Equal(4000))
	})

	It("omits timestamps from run-ledger event fields", func() {
		fields := EventFields(Event{
			Timestamp:    "2026-05-13T00:00:00Z",
			Mode:         "plan-execute",
			Brief:        "atom.md",
			ExitCode:     0,
			FinalState:   "success",
			SessionID:    "session-1",
			QueuePool:    "qwen-35b",
			ModelCalls:   1,
			ToolCalls:    2,
			TotalTokens:  300,
			Degradations: []string{"x"},
		})
		Expect(fields).NotTo(HaveKey("ts"))
		Expect(fields).To(HaveKeyWithValue("mode", "plan-execute"))
		Expect(fields).To(HaveKeyWithValue("final_state", "success"))
		Expect(fields).To(HaveKeyWithValue("queue_pool", "qwen-35b"))
	})

	It("reports zero token aggregates when no event has token data", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0})
		summary, err := Summarize(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(summary.EventsWithTokens).To(BeZero())
		Expect(summary.TotalTokens).To(BeZero())
	})

	It("prints token aggregates when token data exists", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0, PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200})
		var output bytes.Buffer
		Expect(PrintSummary(root, &output, Filter{})).To(Succeed())
		for _, expected := range []string{"token usage", "prompt: 1000", "completion: 200", "total: 1200"} {
			Expect(output.String()).To(ContainSubstring(expected))
		}
	})

	It("omits token output when no token data exists", func() {
		appendEvent(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0})
		var output bytes.Buffer
		Expect(PrintSummary(root, &output, Filter{})).To(Succeed())
		Expect(output.String()).NotTo(ContainSubstring("token usage"))
	})
})
