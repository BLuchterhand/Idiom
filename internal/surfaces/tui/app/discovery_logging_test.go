package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/evidence/transcript"
	"idiom/internal/runtime/agent"
)

func TestLogRunEventPromotesChildToolTelemetryToTypedRecords(t *testing.T) {
	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  t.TempDir(),
		Mode: transcript.DebugLogModeFull,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}
	model := &Model{debugLogger: logger}

	requestPayload := mustMarshalJSON(t, agent.ToolRequestEvent{
		Step:  1,
		Agent: "child:explorer",
		Raw:   `{"tool":"structural_search","pattern":"if $X { $Y }","language":"go","path":"internal"}`,
		Request: workspace.ToolRequest{
			Tool:     "structural_search",
			Path:     "internal",
			Pattern:  "if $X { $Y }",
			Language: "go",
		},
		Mode:      workspace.ModeNormal,
		Narrowing: true,
		Action:    "structural_search:internal:if $x { $y }:go",
	})
	model.logRunEvent(agent.RunEvent{Role: "child_tool_request", Content: requestPayload})

	resultPayload := mustMarshalJSON(t, agent.ToolResultEvent{
		Step:  1,
		Agent: "child:explorer",
		Request: workspace.ToolRequest{
			Tool:     "structural_search",
			Path:     "internal",
			Pattern:  "if $X { $Y }",
			Language: "go",
		},
		Result: workspace.ToolResult{
			Tool:    "structural_search",
			Summary: "Ran structural search under internal",
			Output:  "[]",
		},
	})
	model.logRunEvent(agent.RunEvent{Role: "child_tool_result", Content: resultPayload})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records := readAppDebugLogRecords(t, logger.Path())
	if len(records) != 2 {
		t.Fatalf("expected typed child request/result records, got %#v", records)
	}
	if records[0]["type"] != "tool_request" || records[0]["agent"] != "child:explorer" || records[0]["discovery_class"] != "ast_search" {
		t.Fatalf("unexpected child tool_request record: %#v", records[0])
	}
	if records[1]["type"] != "tool_result" || records[1]["agent"] != "child:explorer" || records[1]["discovery_class"] != "ast_search" {
		t.Fatalf("unexpected child tool_result record: %#v", records[1])
	}
	if model.sessionAgg.discoveryClasses["ast_search"] != 1 {
		t.Fatalf("expected session aggregate to count child discovery class, got %#v", model.sessionAgg.discoveryClasses)
	}
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(data)
}

func readAppDebugLogRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var records []map[string]any
	for _, line := range splitNonEmptyLines(string(data)) {
		record := make(map[string]any)
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func splitNonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
