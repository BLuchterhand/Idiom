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
