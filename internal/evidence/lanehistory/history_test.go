package lanehistory

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendAndSummarizeLaneHistory(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 1: %v", err)
	}

	if err := Append(root, Event{Mode: "implement", Brief: "b.md", ExitCode: 1}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 3 (re-dispatch): %v", err)
	}

	s, err := Summarize(root)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if s.Total != 3 {
		t.Fatalf("total = %d, want 3", s.Total)
	}

	if s.ByMode["plan-execute"] != 2 {
		t.Fatalf("plan-execute count = %d, want 2", s.ByMode["plan-execute"])
	}

	if s.ByMode["implement"] != 1 {
		t.Fatalf("implement count = %d, want 1", s.ByMode["implement"])
	}

	if s.ByExitCode[0] != 2 {
		t.Fatalf("exit 0 count = %d, want 2", s.ByExitCode[0])
	}

	if s.ByExitCode[1] != 1 {
		t.Fatalf("exit 1 count = %d, want 1", s.ByExitCode[1])
	}

	if s.BriefsByPath["a.md"] != 2 {
		t.Fatalf("a.md count = %d, want 2", s.BriefsByPath["a.md"])
	}
}

func TestSummarizeLaneHistoryMissingFileReturnsEmpty(t *testing.T) {
	root := t.TempDir()

	s, err := Summarize(root)
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}

	if s.Total != 0 {
		t.Fatalf("total = %d, want 0", s.Total)
	}
}

func TestSummarizeLaneHistorySkipsMalformedLines(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Event{Mode: "implement", Brief: "x.md", ExitCode: 0}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Inject a malformed line directly into the file.
	path := filepath.Join(root, ".idiom", "lane-history.jsonl")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for malformed inject: %v", err)
	}
	defer f.Close()

	if _, err := f.WriteString("this is not json\n"); err != nil {
		t.Fatalf("write malformed: %v", err)
	}

	s, err := Summarize(root)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if s.Total != 1 {
		t.Fatalf("total = %d, want 1 (malformed line skipped)", s.Total)
	}
}

func TestSummarizeLaneHistoryByMode(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 1: %v", err)
	}

	if err := Append(root, Event{Mode: "plan-execute", Brief: "b.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	if err := Append(root, Event{Mode: "implement", Brief: "c.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 3: %v", err)
	}

	s, err := SummarizeByMode(root, "plan-execute")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	if s.Total != 2 {
		t.Fatalf("total = %d, want 2", s.Total)
	}

	if s.ByMode["plan-execute"] != 2 {
		t.Fatalf("plan-execute count = %d, want 2", s.ByMode["plan-execute"])
	}

	if s.ByMode["implement"] != 0 {
		t.Fatalf("implement count = %d, want 0 (filtered out)", s.ByMode["implement"])
	}
}

func TestDetectBriefProvenance(t *testing.T) {
	root := t.TempDir()
	refineDrafted := filepath.Join(root, "refine.md")

	if err := os.WriteFile(refineDrafted, []byte("<!-- idiom:refine-drafted title=foo iter=2 reason=ship-as-is -->\n# Brief body\n"), 0o644); err != nil {
		t.Fatalf("write refine: %v", err)
	}

	claudeDirect := filepath.Join(root, "claude.md")
	if err := os.WriteFile(claudeDirect, []byte("# Direct brief, no marker\n"), 0o644); err != nil {
		t.Fatalf("write direct: %v", err)
	}

	cases := map[string]string{
		refineDrafted:                     "refine-drafted",
		claudeDirect:                      "claude-direct",
		"":                                "unknown",
		filepath.Join(root, "missing.md"): "unknown",
	}
	for path, want := range cases {
		got := DetectBriefProvenance(path)
		if got != want {
			t.Errorf("DetectBriefProvenance(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestPrintLaneHistorySummaryEmpty(t *testing.T) {
	root := t.TempDir()

	var buf bytes.Buffer

	if err := PrintSummary(root, &buf, Filter{}); err != nil {
		t.Fatalf("print: %v", err)
	}

	if !strings.Contains(buf.String(), "0 dispatch") {
		t.Fatalf("expected '0 dispatch' in output, got: %s", buf.String())
	}
}

func TestSummarizeLaneHistoryWithFilterSince(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	evt1 := Event{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0}
	evt2 := Event{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1}
	evt3 := Event{Timestamp: now.Add(-10 * time.Minute).Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0}

	for _, evt := range []Event{evt1, evt2, evt3} {
		if err := Append(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	filter := Filter{Since: now.Add(-1 * time.Hour)}

	s, err := SummarizeWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize with since: %v", err)
	}

	if s.Total != 2 {
		t.Fatalf("total = %d, want 2 (dropped 2h event)", s.Total)
	}
}

func TestSummarizeLaneHistoryWithFilterFailed(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	evt1 := Event{Timestamp: now.Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0}
	evt2 := Event{Timestamp: now.Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1}
	evt3 := Event{Timestamp: now.Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0}

	for _, evt := range []Event{evt1, evt2, evt3} {
		if err := Append(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	filter := Filter{FailedOnly: true}

	s, err := SummarizeWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize with failed: %v", err)
	}

	if s.Total != 1 {
		t.Fatalf("total = %d, want 1 (dropped 2 successes)", s.Total)
	}

	if s.ByExitCode[0] != 0 {
		t.Fatalf("exit 0 count = %d, want 0", s.ByExitCode[0])
	}

	if s.ByExitCode[1] != 1 {
		t.Fatalf("exit 1 count = %d, want 1", s.ByExitCode[1])
	}
}

func TestSummarizeLaneHistoryWithFilterCombined(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	evt1 := Event{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "implement", Brief: "a.md", ExitCode: 0}
	evt2 := Event{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), Mode: "plan-execute", Brief: "b.md", ExitCode: 1}
	evt3 := Event{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "refine", Brief: "c.md", ExitCode: 0}
	evt4 := Event{Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Mode: "implement", Brief: "d.md", ExitCode: 2}

	for _, evt := range []Event{evt1, evt2, evt3, evt4} {
		if err := Append(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	filter := Filter{Since: now.Add(-1 * time.Hour), FailedOnly: true}

	s, err := SummarizeWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize with combined: %v", err)
	}

	if s.Total != 1 {
		t.Fatalf("total = %d, want 1 (only evt4)", s.Total)
	}

	if s.ByExitCode[2] != 1 {
		t.Fatalf("exit 2 count = %d, want 1", s.ByExitCode[2])
	}
}

func TestSummarizeLaneHistoryWithFilterNoFilterEqualsOriginal(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 1: %v", err)
	}

	if err := Append(root, Event{Mode: "implement", Brief: "b.md", ExitCode: 1}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append 3: %v", err)
	}

	sOrig, err := Summarize(root)
	if err != nil {
		t.Fatalf("summarize original: %v", err)
	}

	sFiltered, err := SummarizeWithFilter(root, Filter{})
	if err != nil {
		t.Fatalf("summarize with zero filter: %v", err)
	}

	if sOrig.Total != sFiltered.Total {
		t.Fatalf("total: original=%d, filtered=%d", sOrig.Total, sFiltered.Total)
	}

	for mode, n := range sOrig.ByMode {
		if sFiltered.ByMode[mode] != n {
			t.Fatalf("ByMode[%q]: original=%d, filtered=%d", mode, n, sFiltered.ByMode[mode])
		}
	}

	for code, n := range sOrig.ByExitCode {
		if sFiltered.ByExitCode[code] != n {
			t.Fatalf("ByExitCode[%d]: original=%d, filtered=%d", code, n, sFiltered.ByExitCode[code])
		}
	}

	for path, n := range sOrig.BriefsByPath {
		if sFiltered.BriefsByPath[path] != n {
			t.Fatalf("BriefsByPath[%q]: original=%d, filtered=%d", path, n, sFiltered.BriefsByPath[path])
		}
	}
}

func TestSummarizeLaneHistoryWithFilterModeFilter(t *testing.T) {
	root := t.TempDir()
	mustAppend := func(mode, brief string, exit int) {
		t.Helper()

		if err := Append(root, Event{Mode: mode, Brief: brief, ExitCode: exit}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	mustAppend("plan-execute", "a.md", 0)
	mustAppend("plan-execute", "b.md", 1)
	mustAppend("implement", "c.md", 0)
	mustAppend("refine", "d.md", 0)

	filter := Filter{ModeFilter: "plan-execute"}

	s, err := SummarizeWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if s.Total != 2 {
		t.Fatalf("Total = %d, want 2 (filtered to plan-execute)", s.Total)
	}

	if s.ByMode["implement"] != 0 || s.ByMode["refine"] != 0 {
		t.Fatalf("non-plan-execute modes should be filtered out: %+v", s.ByMode)
	}
}

func TestSummarizeLaneHistoryWithFilterCombinesAllThree(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	mustAppend := func(mode string, exit int, ago time.Duration) {
		t.Helper()

		evt := Event{Mode: mode, ExitCode: exit, Timestamp: now.Add(-ago).Format(time.RFC3339Nano)}
		if err := Append(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	mustAppend("plan-execute", 1, 2*time.Hour)    // wrong since, right mode+failed
	mustAppend("plan-execute", 0, 30*time.Minute) // wrong failed, right since+mode
	mustAppend("implement", 1, 30*time.Minute)    // wrong mode, right since+failed
	mustAppend("plan-execute", 1, 30*time.Minute) // matches all

	filter := Filter{
		Since:      now.Add(-1 * time.Hour),
		FailedOnly: true,
		ModeFilter: "plan-execute",
	}

	s, err := SummarizeWithFilter(root, filter)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if s.Total != 1 {
		t.Fatalf("expected exactly 1 event matching all 3 filters, got %d", s.Total)
	}
}

func TestSummarizeLaneHistoryAggregatesTokens(t *testing.T) {
	root := t.TempDir()
	mustAppend := func(evt Event) {
		t.Helper()

		if err := Append(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	mustAppend(Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0, PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200})
	mustAppend(Event{Mode: "implement", Brief: "b.md", ExitCode: 1, PromptTokens: 5000, CompletionTokens: 800, TotalTokens: 5800})
	// Third dispatch ran without --json — no token data. Should not
	// count toward EventsWithTokens but still increments Total.
	mustAppend(Event{Mode: "plan-execute", Brief: "c.md", ExitCode: 0})

	s, err := Summarize(root)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if s.Total != 3 {
		t.Fatalf("Total = %d, want 3", s.Total)
	}

	if s.EventsWithTokens != 2 {
		t.Fatalf("EventsWithTokens = %d, want 2 (third dispatch had no tokens)", s.EventsWithTokens)
	}

	if s.TotalPromptTokens != 6000 {
		t.Fatalf("TotalPromptTokens = %d, want 6000", s.TotalPromptTokens)
	}

	if s.TotalCompletionTokens != 1000 {
		t.Fatalf("TotalCompletionTokens = %d, want 1000", s.TotalCompletionTokens)
	}

	if s.TotalTokens != 7000 {
		t.Fatalf("TotalTokens = %d, want 7000", s.TotalTokens)
	}
}

func TestSummarizeLaneHistoryAggregatesRunMetricsAndFinalState(t *testing.T) {
	root := t.TempDir()
	events := []Event{
		{Mode: "plan-execute", Brief: "a.md", ExitCode: 0, FinalState: "success", ModelCalls: 3, ToolCalls: 2, DurationMS: 1000},
		{Mode: "plan-execute", Brief: "b.md", ExitCode: 4, FinalState: "degraded", ModelCalls: 5, ToolCalls: 6, DurationMS: 3000, Degradations: []string{"no_edit_attempt"}},
	}

	for _, evt := range events {
		if err := Append(root, evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	s, err := Summarize(root)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if s.ByFinalState["success"] != 1 || s.ByFinalState["degraded"] != 1 {
		t.Fatalf("ByFinalState = %#v", s.ByFinalState)
	}

	if s.EventsWithRunMetrics != 2 || s.TotalModelCalls != 8 || s.TotalToolCalls != 8 || s.TotalDurationMS != 4000 {
		t.Fatalf("run metrics not aggregated: %#v", s)
	}
}

func TestLaneHistoryEventFieldsOmitsTimestampForRunLedgerEvent(t *testing.T) {
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
	if _, ok := fields["ts"]; ok {
		t.Fatalf("run ledger event fields should not include ts: %#v", fields)
	}

	if fields["mode"] != "plan-execute" || fields["final_state"] != "success" || fields["queue_pool"] != "qwen-35b" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
}

func TestSummarizeLaneHistoryNoTokensReturnsZeros(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append: %v", err)
	}

	s, err := Summarize(root)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if s.EventsWithTokens != 0 || s.TotalTokens != 0 {
		t.Fatalf("expected zero token aggregates, got EventsWithTokens=%d TotalTokens=%d", s.EventsWithTokens, s.TotalTokens)
	}
}

func TestPrintLaneHistorySummaryIncludesTokenSection(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0, PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200}); err != nil {
		t.Fatalf("append: %v", err)
	}

	var buf bytes.Buffer

	if err := PrintSummary(root, &buf, Filter{}); err != nil {
		t.Fatalf("print: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"token usage", "prompt: 1000", "completion: 200", "total: 1200"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in summary output, got:\n%s", want, out)
		}
	}
}

func TestPrintLaneHistorySummaryOmitsTokenSectionWhenNoData(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Event{Mode: "plan-execute", Brief: "a.md", ExitCode: 0}); err != nil {
		t.Fatalf("append: %v", err)
	}

	var buf bytes.Buffer

	if err := PrintSummary(root, &buf, Filter{}); err != nil {
		t.Fatalf("print: %v", err)
	}

	if strings.Contains(buf.String(), "token usage") {
		t.Fatalf("expected no token section when all events lack tokens, got:\n%s", buf.String())
	}
}
