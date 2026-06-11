package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugLoggerRedactedModeMasksPromptAndToolPayloads(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:           dir,
		Mode:          DebugLogModeRedacted,
		WorkspaceRoot: "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogSessionStart(SessionStart{
		AppName: "Idiom",
		PromptSources: []PromptSource{{
			Kind:        "baseline",
			DisplayName: "base.md",
			Path:        "/tmp/workspace/prompts/base.md",
		}},
		SystemPrompt:  "hidden system prompt body",
		WorkspaceRoot: "/tmp/workspace",
		WorkspaceCWD:  "/tmp/workspace/internal/surfaces/tui/app",
	}); err != nil {
		t.Fatalf("LogSessionStart: %v", err)
	}
	raw := `{"tool":"edit_file","path":"/tmp/workspace/config.json","content":"top secret patch","command":"TOKEN=abc123 go test ./..."}`
	if err := logger.LogToolRequest(1, "implement", raw, "edit_file", "normal", false, true, "edit_file:config.json"); err != nil {
		t.Fatalf("LogToolRequest: %v", err)
	}
	if err := logger.LogMessage("user", "api_key=secret123 path=/tmp/workspace/config.json"); err != nil {
		t.Fatalf("LogMessage: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	if got := records[0]["system_prompt"]; got != nil {
		t.Fatalf("expected redacted mode to omit system_prompt, got %#v", got)
	}
	if digest := records[0]["system_prompt_digest"]; digest == nil || !strings.HasPrefix(digest.(string), "sha256:") {
		t.Fatalf("expected system prompt digest in redacted mode, got %#v", digest)
	}
	if records[0]["workspace_root"] != "." {
		t.Fatalf("expected workspace root to be redacted to '.', got %#v", records[0]["workspace_root"])
	}
	sourceRecords, ok := records[0]["prompt_sources"].([]any)
	if !ok || len(sourceRecords) != 1 {
		t.Fatalf("expected prompt source details, got %#v", records[0]["prompt_sources"])
	}
	sourceRecord, _ := sourceRecords[0].(map[string]any)
	if sourceRecord["path"] != "./prompts/base.md" {
		t.Fatalf("expected prompt source path to be workspace-relative, got %#v", sourceRecord["path"])
	}
	rawField, _ := records[1]["raw"].(string)
	if strings.Contains(rawField, "top secret patch") || strings.Contains(rawField, "go test ./...") {
		t.Fatalf("expected redacted tool payload, got %q", rawField)
	}
	if !strings.Contains(rawField, "redacted 16 chars") || !strings.Contains(rawField, "redacted shell command") {
		t.Fatalf("expected redacted markers in tool payload, got %q", rawField)
	}
	msgContent, _ := records[2]["content"].(string)
	if strings.Contains(msgContent, "secret123") || strings.Contains(msgContent, "/tmp/workspace") {
		t.Fatalf("expected redacted message content, got %q", msgContent)
	}
}

func TestDebugLoggerFullModePreservesPromptAndToolPayloads(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:           dir,
		Mode:          DebugLogModeFull,
		WorkspaceRoot: "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogSessionStart(SessionStart{
		AppName:       "Idiom",
		SystemPrompt:  "hidden system prompt body",
		WorkspaceRoot: "/tmp/workspace",
		WorkspaceCWD:  "/tmp/workspace/internal/surfaces/tui/app",
	}); err != nil {
		t.Fatalf("LogSessionStart: %v", err)
	}
	raw := `{"tool":"shell","command":"go test ./...","path":"/tmp/workspace/config.json"}`
	if err := logger.LogToolRequest(1, "implement", raw, "shell", "normal", false, true, "shell:go"); err != nil {
		t.Fatalf("LogToolRequest: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	if records[0]["system_prompt"] != "hidden system prompt body" {
		t.Fatalf("expected full mode to preserve system prompt, got %#v", records[0]["system_prompt"])
	}
	rawField, _ := records[1]["raw"].(string)
	if !strings.Contains(rawField, "go test ./...") || !strings.Contains(rawField, "/tmp/workspace/config.json") {
		t.Fatalf("expected full mode to preserve raw tool payload, got %q", rawField)
	}
}

func TestDebugLoggerModeAndWrapperRecords(t *testing.T) {
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:           t.TempDir(),
		Mode:          DebugLogModeFull,
		WorkspaceRoot: "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if logger.Mode() != DebugLogModeFull {
		t.Fatalf("Mode = %q, want full", logger.Mode())
	}
	logger.SetMode("")
	if logger.Mode() != DebugLogModeRedacted {
		t.Fatalf("empty SetMode should reset to redacted, got %q", logger.Mode())
	}
	logger.SetMode(DebugLogModeFull)

	if err := logger.LogContext([]string{"base.md"}, "system prompt", "normal", "medium"); err != nil {
		t.Fatalf("LogContext: %v", err)
	}
	if err := logger.LogToolResult(2, "main", "read_file", "Read /tmp/workspace/main.go", ""); err != nil {
		t.Fatalf("LogToolResult: %v", err)
	}
	if err := logger.LogInputSubmission("hello"); err != nil {
		t.Fatalf("LogInputSubmission: %v", err)
	}
	if err := logger.LogError("runner", "failed"); err != nil {
		t.Fatalf("LogError: %v", err)
	}
	if err := logger.LogServer(true, "model", true, ""); err != nil {
		t.Fatalf("LogServer: %v", err)
	}
	if err := logger.LogArtifact("report", "/tmp/workspace/report.json"); err != nil {
		t.Fatalf("LogArtifact: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	wantTypes := []string{"context_update", "tool_result", "input_submission", "error", "server", "artifact"}
	if len(records) != len(wantTypes) {
		t.Fatalf("records length = %d, want %d: %#v", len(records), len(wantTypes), records)
	}
	for i, want := range wantTypes {
		if records[i]["type"] != want {
			t.Fatalf("record %d type = %#v, want %q in %#v", i, records[i]["type"], want, records)
		}
	}
	if records[0]["system_prompt"] != "system prompt" {
		t.Fatalf("full mode LogContext should preserve prompt: %#v", records[0])
	}
}

func TestDebugLoggerLogsPhaseEvents(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:           dir,
		Mode:          DebugLogModeRedacted,
		WorkspaceRoot: "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogPhase("implement", "verify", "active", "Running check 1/2: go build ./... inside /tmp/workspace"); err != nil {
		t.Fatalf("LogPhase: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	if len(records) != 1 {
		t.Fatalf("expected one phase record, got %d", len(records))
	}
	if records[0]["type"] != "phase" || records[0]["lane"] != "implement" || records[0]["phase"] != "verify" || records[0]["state"] != "active" {
		t.Fatalf("unexpected phase record: %#v", records[0])
	}
	detail, _ := records[0]["detail"].(string)
	if strings.Contains(detail, "/tmp/workspace") {
		t.Fatalf("expected redacted phase detail, got %q", detail)
	}
}

func TestDebugLoggerRecordsDiscoveryBalanceMetadata(t *testing.T) {
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: DebugLogModeFull,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogToolRequestDetail(ToolRequestDetail{
		Step:           1,
		Agent:          "main",
		Raw:            `{"tool":"structural_search","pattern":"if $X { $Y }"}`,
		Tool:           "structural_search",
		WorkspaceMode:  "normal",
		Narrowing:      true,
		Action:         "structural_search:.:if $x { $y }:go",
		DiscoveryClass: "ast_search",
		DiscoveryScope: "targeted",
		DiscoveryCost:  "medium",
	}); err != nil {
		t.Fatalf("LogToolRequestDetail: %v", err)
	}
	if err := logger.LogToolResultDetail(ToolResultDetail{
		Step:           1,
		Agent:          "main",
		Tool:           "structural_search",
		Summary:        "Ran structural search",
		DiscoveryClass: "ast_search",
		DiscoveryScope: "targeted",
		DiscoveryCost:  "medium",
		ExtraFields: map[string]any{
			"context_store_kind":     "sqlite-graph-v1",
			"context_files_indexed":  3,
			"non_context_debug_data": "ignored",
		},
	}); err != nil {
		t.Fatalf("LogToolResultDetail: %v", err)
	}
	if err := logger.LogSessionSummary(SessionSummary{
		Turns:            1,
		ModelCalls:       1,
		ToolCalls:        1,
		DiscoveryByClass: []DiscoveryAggregate{{Name: "ast_search", Calls: 1}},
		DiscoveryByCost:  []DiscoveryAggregate{{Name: "medium", Calls: 1}},
	}); err != nil {
		t.Fatalf("LogSessionSummary: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	if records[0]["discovery_class"] != "ast_search" || records[0]["discovery_scope"] != "targeted" || records[0]["discovery_cost"] != "medium" {
		t.Fatalf("tool_request missing discovery metadata: %#v", records[0])
	}
	if records[1]["discovery_class"] != "ast_search" || records[1]["discovery_scope"] != "targeted" || records[1]["discovery_cost"] != "medium" {
		t.Fatalf("tool_result missing discovery metadata: %#v", records[1])
	}
	if records[1]["context_store_kind"] != "sqlite-graph-v1" || records[1]["context_files_indexed"].(float64) != 3 || records[1]["non_context_debug_data"] != nil {
		t.Fatalf("tool_result missing sanitized context metadata: %#v", records[1])
	}
	if records[2]["discovery_by_class"] == nil || records[2]["discovery_by_cost"] == nil {
		t.Fatalf("session_summary missing discovery aggregates: %#v", records[2])
	}
}

func TestDebugLoggerSuppressesDuplicateAssistantSummaryMessages(t *testing.T) {
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: DebugLogModeFull,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogMessage("architecture_summary", "same grounded answer"); err != nil {
		t.Fatalf("LogMessage architecture_summary: %v", err)
	}
	if err := logger.LogPhase("architecture", "complete", "done", "phase records must not break message dedupe"); err != nil {
		t.Fatalf("LogPhase: %v", err)
	}
	if err := logger.LogMessage("control_rails_enforcement", `{"passed":true}`); err != nil {
		t.Fatalf("LogMessage control_rails_enforcement: %v", err)
	}
	if err := logger.LogMessage("assistant", "same grounded answer"); err != nil {
		t.Fatalf("LogMessage duplicate assistant: %v", err)
	}
	if err := logger.LogMessage("assistant", "different final answer"); err != nil {
		t.Fatalf("LogMessage different assistant: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	assistantMessages := 0
	for _, record := range records {
		if record["type"] == "message" && record["role"] == "assistant" {
			assistantMessages++
			if record["content"] != "different final answer" {
				t.Fatalf("unexpected assistant message record: %#v", record)
			}
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("expected only the non-duplicate assistant message, got %d records: %#v", assistantMessages, records)
	}
}

func TestDebugLoggerRunEventDoesNotWriteMessageRecord(t *testing.T) {
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: DebugLogModeFull,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogRunEvent("completion_gate_recovery", "internal recovery prompt"); err != nil {
		t.Fatalf("LogRunEvent: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	if len(records) != 1 {
		t.Fatalf("expected one record, got %#v", records)
	}
	if records[0]["type"] != "run_event" || records[0]["role"] != "completion_gate_recovery" {
		t.Fatalf("internal runner event should be typed as run_event, got %#v", records[0])
	}
}

func TestDebugLoggerLogsContextFillTelemetry(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:           dir,
		Mode:          DebugLogModeRedacted,
		WorkspaceRoot: "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogSessionStart(SessionStart{
		AppName:       "Idiom",
		WorkspaceRoot: "/tmp/workspace",
		CtxSize:       196608,
	}); err != nil {
		t.Fatalf("LogSessionStart: %v", err)
	}
	if err := logger.LogToolResultDetail(ToolResultDetail{
		Step:        2,
		Agent:       "main",
		Tool:        "read_file",
		Summary:     "Read file",
		OutputChars: 8400,
		OutputLines: 200,
		Path:        "/tmp/workspace/internal/surfaces/tui/app/app.go",
		StartLine:   1,
		EndLine:     200,
	}); err != nil {
		t.Fatalf("LogToolResultDetail: %v", err)
	}
	if err := logger.LogModelCall(ModelCall{
		Step:                       3,
		Agent:                      "main",
		PromptTokens:               12000,
		CompletionTokens:           350,
		TotalTokens:                12350,
		PromptCacheHitTokens:       9000,
		PromptCacheMissTokens:      3000,
		PromptCacheAccountingKnown: true,
		ReasoningTokens:            40,
		CtxSize:                    196608,
		MessageCount:               14,
		RoleChars:                  map[string]int{"system": 4500, "user": 800, "assistant": 1200, "tool": 9000},
		ChosenTool:                 "edit_file",
		DurationMillis:             2400,
	}); err != nil {
		t.Fatalf("LogModelCall: %v", err)
	}
	if err := logger.LogTurnSummary(TurnSummary{
		Lane:                       "main",
		UsedModelCalls:             3,
		UsedToolResults:            6,
		PromptTokens:               20000,
		CompletionTokens:           600,
		TotalTokens:                20600,
		PromptCacheHitTokens:       15000,
		PromptCacheMissTokens:      5000,
		PromptCacheAccountingKnown: true,
		ReasoningTokens:            70,
		DurationMillis:             8000,
		StopReason:                 "completed",
	}); err != nil {
		t.Fatalf("LogTurnSummary: %v", err)
	}
	if err := logger.LogSessionSummary(SessionSummary{
		Turns:                      2,
		ModelCalls:                 6,
		ToolCalls:                  12,
		PromptTokens:               40000,
		CompletionTokens:           1200,
		TotalTokens:                41200,
		PromptCacheHitTokens:       30000,
		PromptCacheMissTokens:      10000,
		PromptCacheAccountingKnown: true,
		PromptCacheHitRate:         0.75,
		ReasoningTokens:            120,
		PeakPromptTokens:           22000,
		CtxSize:                    196608,
		PeakCtxPct:                 float64(22000) / float64(196608),
		Compactions:                1,
		TopToolsByOutput:           []ToolOutputAggregate{{Tool: "read_file", Calls: 8, OutputChars: 60000}},
		DurationMillis:             30000,
	}); err != nil {
		t.Fatalf("LogSessionSummary: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readDebugLogRecords(t, logger.Path())
	byType := map[string]map[string]any{}
	for _, rec := range records {
		t, _ := rec["type"].(string)
		byType[t] = rec
	}
	if got, _ := byType["session_start"]["ctx_size"].(float64); got != 196608 {
		t.Fatalf("session_start.ctx_size = %v, want 196608", byType["session_start"]["ctx_size"])
	}
	tr := byType["tool_result"]
	if tr["output_chars"] == nil || tr["output_lines"] == nil || tr["path"] == nil || tr["start_line"] == nil {
		t.Fatalf("tool_result missing detail fields: %#v", tr)
	}
	path, _ := tr["path"].(string)
	if strings.Contains(path, "/tmp/workspace") {
		t.Fatalf("tool_result.path not redacted: %q", path)
	}
	mc := byType["model_call"]
	if mc["ctx_pct"] == nil {
		t.Fatalf("model_call missing ctx_pct: %#v", mc)
	}
	pct, _ := mc["ctx_pct"].(float64)
	if pct <= 0 || pct >= 1 {
		t.Fatalf("model_call.ctx_pct out of range: %v", pct)
	}
	rc, ok := mc["role_chars"].(map[string]any)
	if !ok || rc["tool"] == nil {
		t.Fatalf("model_call missing role_chars: %#v", mc)
	}
	if mc["chosen_tool"] != "edit_file" {
		t.Fatalf("model_call.chosen_tool = %v", mc["chosen_tool"])
	}
	if mc["prompt_cache_accounting_known"] != true ||
		mc["prompt_cache_hit_tokens"] != float64(9000) ||
		mc["prompt_cache_miss_tokens"] != float64(3000) ||
		mc["reasoning_tokens"] != float64(40) {
		t.Fatalf("model_call missing cache usage: %#v", mc)
	}
	ts := byType["turn_summary"]
	if ts == nil || ts["used_model_calls"] == nil {
		t.Fatalf("turn_summary missing: %#v", ts)
	}
	if ts["prompt_cache_accounting_known"] != true ||
		ts["prompt_cache_hit_tokens"] != float64(15000) ||
		ts["prompt_cache_miss_tokens"] != float64(5000) ||
		ts["reasoning_tokens"] != float64(70) {
		t.Fatalf("turn_summary missing cache usage: %#v", ts)
	}
	ss := byType["session_summary"]
	if ss == nil || ss["peak_ctx_pct"] == nil || ss["top_tools_by_output"] == nil {
		t.Fatalf("session_summary missing fields: %#v", ss)
	}
	if ss["prompt_cache_accounting_known"] != true ||
		ss["prompt_cache_hit_tokens"] != float64(30000) ||
		ss["prompt_cache_miss_tokens"] != float64(10000) ||
		ss["prompt_cache_hit_rate"] != float64(0.75) ||
		ss["reasoning_tokens"] != float64(120) {
		t.Fatalf("session_summary missing cache usage: %#v", ss)
	}
}

func readDebugLogRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read log %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		record := make(map[string]any)
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal record %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func TestDebugLoggerFilenameIncludesSessionIDWhenSet(t *testing.T) {
	// SessionID in the filename lets us attribute logs to a specific
	// idiom run when concurrent sessions write logs in the same
	// directory at near-identical timestamps.
	dir := t.TempDir()
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:       dir,
		Mode:      DebugLogModeRedacted,
		SessionID: "abc123def456",
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	path := logger.Path()
	if !strings.HasSuffix(path, "-abc123def456.jsonl") {
		t.Fatalf("expected filename to end with -abc123def456.jsonl, got %s", path)
	}
}

func TestDebugLoggerFilenameOmitsSessionIDWhenEmpty(t *testing.T) {
	// Backward compatibility: existing callers that don't pass
	// SessionID get the legacy timestamp-only filename.
	dir := t.TempDir()
	logger, err := NewDebugLogger(DebugLoggerOptions{
		Dir:  dir,
		Mode: DebugLogModeRedacted,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	name := filepath.Base(logger.Path())
	// Format is YYYYMMDD-HHMMSS.mmm.jsonl — exactly one hyphen,
	// not two. A session-stamped filename would have two hyphens.
	if strings.Count(name, "-") != 1 {
		t.Fatalf("expected legacy single-hyphen filename, got %s", name)
	}
}
