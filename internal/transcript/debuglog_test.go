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
		AppName:       "Qwen Bubbles",
		SystemPrompt:  "hidden system prompt body",
		WorkspaceRoot: "/tmp/workspace",
		WorkspaceCWD:  "/tmp/workspace/internal/app",
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
		AppName:       "Qwen Bubbles",
		SystemPrompt:  "hidden system prompt body",
		WorkspaceRoot: "/tmp/workspace",
		WorkspaceCWD:  "/tmp/workspace/internal/app",
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
