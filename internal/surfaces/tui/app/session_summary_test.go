package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func TestModelCloseWritesSessionSummaryForEmptySession(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir:           filepath.Join(root, "logs", "conversations"),
			RequestTimeoutSeconds: 1,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	}

	model := New(cfg)
	logPath := model.DebugLogPath()
	model.Close()
	model.Close()

	records := readAppSessionSummaryRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("expected exactly one session_summary, got %d in %#v", len(records), records)
	}
	summary := records[0]
	if summary["model_calls"] != float64(0) || summary["tool_calls"] != float64(0) || summary["turns"] != float64(0) {
		t.Fatalf("empty session_summary should preserve zero counters, got %#v", summary)
	}
}

func readAppSessionSummaryRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	var summaries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if record["type"] == "session_summary" {
			summaries = append(summaries, record)
		}
	}
	return summaries
}
