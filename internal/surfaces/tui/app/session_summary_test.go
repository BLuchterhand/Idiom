package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
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

func TestModelCloseSessionSummaryIncludesProviderCacheUsage(t *testing.T) {
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
	model.logRunEvent(agent.RunEvent{
		Role:    "model_call",
		Content: `{"step":1,"label":"main_loop","prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":5,"prompt_cache_accounting_known":true,"reasoning_tokens":2}`,
	})

	logPath := model.DebugLogPath()
	model.Close()

	records := readAppSessionSummaryRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("expected one session_summary, got %d in %#v", len(records), records)
	}

	summary := records[0]
	if summary["prompt_cache_accounting_known"] != true ||
		summary["prompt_cache_hit_tokens"] != float64(8) ||
		summary["prompt_cache_miss_tokens"] != float64(5) ||
		summary["reasoning_tokens"] != float64(2) {

		t.Fatalf("session_summary missing provider cache usage: %#v", summary)
	}

	if got, _ := summary["prompt_cache_hit_rate"].(float64); got < 0.61 || got > 0.62 {
		t.Fatalf("prompt_cache_hit_rate = %v, want about 0.615: %#v", got, summary)
	}
}

func TestModelCloseSessionSummaryEmitsZeroCacheHitRate(t *testing.T) {
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
	model.logRunEvent(agent.RunEvent{
		Role:    "model_call",
		Content: `{"step":1,"label":"main_loop","prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":13,"prompt_cache_accounting_known":true}`,
	})

	logPath := model.DebugLogPath()
	model.Close()

	records := readAppSessionSummaryRecords(t, logPath)
	if len(records) != 1 {
		t.Fatalf("expected one session_summary, got %d in %#v", len(records), records)
	}

	summary := records[0]
	if summary["prompt_cache_accounting_known"] != true ||
		summary["prompt_cache_hit_tokens"] != float64(0) ||
		summary["prompt_cache_miss_tokens"] != float64(13) {

		t.Fatalf("session_summary missing known cache miss accounting: %#v", summary)
	}

	if got, ok := summary["prompt_cache_hit_rate"].(float64); !ok || got != 0 {
		t.Fatalf("prompt_cache_hit_rate = %#v, want emitted zero", summary["prompt_cache_hit_rate"])
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
