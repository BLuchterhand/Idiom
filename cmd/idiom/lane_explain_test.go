package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildLaneExplainSummaryParsesSyntheticLog(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"session_start","session_id":"abc123","ts":"2026-05-11T10:00:00Z"}`,
		`{"type":"tool_result","tool":"read_file","ts":"2026-05-11T10:00:05Z"}`,
		`{"type":"tool_result","tool":"read_file","error":"hidden by contract","ts":"2026-05-11T10:00:06Z"}`,
		`{"type":"tool_result","tool":"completion_gate","error":"required paths remain unchanged","ts":"2026-05-11T10:00:10Z"}`,
		`{"type":"completion_status","status":"blocked","blocker_kind":"required_paths_remaining"}`,
		`{"type":"turn_summary","stop_reason":"completion_gate_required_paths_remaining_repeated","budget_exhausted":true}`,
		`{"type":"session_summary","model_calls":3,"tool_calls":3,"turns":1,"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500,"peak_prompt_tokens":800,"ctx_size":8192,"duration_ms":10000,"ts":"2026-05-11T10:00:10Z"}`,
		`{"type":"final","content":"<<QUESTION>>: missing exact anchor"}`,
	}, "\n")

	s := buildLaneExplainSummary([]byte(log))

	if s.SessionID != "abc123" {
		t.Errorf("SessionID = %q, want abc123", s.SessionID)
	}
	if s.ModelCalls != 3 || s.ToolCalls != 3 || s.Turns != 1 {
		t.Errorf("call counts = %d/%d/%d, want 3/3/1", s.ModelCalls, s.ToolCalls, s.Turns)
	}
	if s.TotalTokens != 1500 || s.PromptTokens != 1000 || s.CompletionTokens != 500 {
		t.Errorf("tokens = %d/%d/%d, want 1500/1000/500", s.TotalTokens, s.PromptTokens, s.CompletionTokens)
	}
	if s.CtxSize != 8192 || s.PeakPromptTokens != 800 {
		t.Errorf("ctx/peak = %d/%d, want 8192/800", s.CtxSize, s.PeakPromptTokens)
	}
	if s.StopReason != "completion_gate_required_paths_remaining_repeated" || !s.BudgetExhausted {
		t.Errorf("stop_reason=%q budget_exhausted=%v", s.StopReason, s.BudgetExhausted)
	}
	if len(s.ToolRejections) != 2 {
		t.Errorf("ToolRejections len = %d, want 2: %v", len(s.ToolRejections), s.ToolRejections)
	}
	if len(s.ToolsByCount) == 0 || s.ToolsByCount[0].Tool != "read_file" || s.ToolsByCount[0].Count != 2 {
		t.Errorf("ToolsByCount[0] = %+v, want read_file=2", s.ToolsByCount[0])
	}
	if !strings.Contains(s.Question, "<<QUESTION>>:") {
		t.Errorf("Question not captured: %q", s.Question)
	}
	if s.CompletionStatus["status"] != "blocked" {
		t.Errorf("CompletionStatus[status] = %v, want blocked", s.CompletionStatus["status"])
	}
}

func TestBuildLaneExplainSummaryIgnoresMalformedAndUnknownTypes(t *testing.T) {
	log := strings.Join([]string{
		`not json`,
		`{"type":"some_future_event","field":"value"}`,
		`{"type":"session_summary","total_tokens":42}`,
		``,
		`{"type":"session_summary"`, // truncated
	}, "\n")

	s := buildLaneExplainSummary([]byte(log))
	if s.TotalTokens != 42 {
		t.Errorf("malformed lines should be ignored; TotalTokens=%d, want 42", s.TotalTokens)
	}
}

func TestSelectLaneExplainSessionPicksNewestByDefault(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, ".idiom", "logs", "conversations")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Lexicographic ordering of YYYYMMDD-HHMMSS-prefixed filenames
	// is chronological. The newest wins.
	for _, name := range []string{
		"20260510-100000.000-aaaa.jsonl",
		"20260511-080000.000-bbbb.jsonl",
		"20260511-100000.000-cccc.jsonl",
	} {
		if err := os.WriteFile(filepath.Join(logsDir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	got, err := selectLaneExplainSession(logsDir, "", "", dir)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !strings.HasSuffix(got, "20260511-100000.000-cccc.jsonl") {
		t.Errorf("got %q, want suffix cccc", got)
	}
}

func TestSelectLaneExplainSessionFiltersBySessionID(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, ".idiom", "logs", "conversations")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{
		"20260510-100000.000-aaaa.jsonl",
		"20260511-080000.000-bbbb.jsonl",
	} {
		if err := os.WriteFile(filepath.Join(logsDir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	got, err := selectLaneExplainSession(logsDir, "aaaa", "", dir)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !strings.HasSuffix(got, "-aaaa.jsonl") {
		t.Errorf("got %q, want suffix aaaa.jsonl", got)
	}
}

func TestBriefTimestampToFilenamePrefixHandlesCommonFormats(t *testing.T) {
	cases := map[string]string{
		"2026-05-11T08:20:22.789-04:00":    "20260511-082022",
		"2026-05-11T08:20:22Z":             "20260511-082022",
		"2026-05-11T08:20:22.194349-04:00": "20260511-082022",
		"":                                 "",
		"not a timestamp":                  "",
	}
	for in, want := range cases {
		got := briefTimestampToFilenamePrefix(in)
		if got != want {
			t.Errorf("briefTimestampToFilenamePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderLaneExplainOmitsEmptySections(t *testing.T) {
	s := laneExplainSummary{
		SessionID:    "minimal",
		PromptTokens: 100,
		TotalTokens:  100,
		ToolsByCount: nil, // no tools section
	}
	var buf bytes.Buffer
	renderLaneExplain(s, &buf)
	out := buf.String()
	if !strings.Contains(out, "session=minimal") {
		t.Fatalf("missing session header: %s", out)
	}
	if strings.Contains(out, "tools called") {
		t.Fatalf("empty tools section should be omitted: %s", out)
	}
	if strings.Contains(out, "tool rejections") {
		t.Fatalf("empty rejections section should be omitted: %s", out)
	}
	if strings.Contains(out, "errors:") {
		t.Fatalf("empty errors section should be omitted: %s", out)
	}
}

func TestRunLaneExplainNoLogsReturnsErrorExit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".idiom", "logs", "conversations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	opts := laneOptions{Mode: "explain", ProjectRoot: dir}
	var stdout, stderr bytes.Buffer
	exit := runLaneExplain(opts, &stdout, &stderr)
	if exit != 1 {
		t.Fatalf("expected exit=1 when no logs present, got %d", exit)
	}
	if !strings.Contains(stderr.String(), "no matching session log found") {
		t.Fatalf("expected 'no matching session' message, got: %s", stderr.String())
	}
}
