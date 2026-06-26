package app

import (
	"path/filepath"
	"testing"

	"idiom/internal/platform/config"
)

func TestNewSuppressesDefaultDebugLoggerDuringTests(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := config.Default()
	cfg.Chat.DebugLogDir = filepath.Join("logs", "conversations")

	model := New(cfg)
	defer model.Close()

	if model.debugLogger != nil || model.DebugLogPath() != "" {
		t.Fatalf("default test debug logger should be suppressed, logger=%v path=%q", model.debugLogger, model.DebugLogPath())
	}
	if matches, _ := filepath.Glob(filepath.Join("logs", "conversations", "*.jsonl")); len(matches) != 0 {
		t.Fatalf("default test debug logger should not create conversation logs: %#v", matches)
	}
}

func TestNewSuppressesEmptyDebugLoggerDuringTests(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := config.Default()
	cfg.Chat.DebugLogDir = ""

	model := New(cfg)
	defer model.Close()

	if model.debugLogger != nil || model.DebugLogPath() != "" {
		t.Fatalf("empty test debug logger should be suppressed, logger=%v path=%q", model.debugLogger, model.DebugLogPath())
	}
	if matches, _ := filepath.Glob(filepath.Join("logs", "conversations", "*.jsonl")); len(matches) != 0 {
		t.Fatalf("empty test debug logger should not fall back to default logs: %#v", matches)
	}
}

func TestNewKeepsExplicitDebugLoggerDuringTests(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.DebugLogDir = t.TempDir()

	model := New(cfg)
	defer model.Close()

	if model.debugLogger == nil || model.DebugLogPath() == "" {
		t.Fatal("explicit test debug log dir should still create a logger")
	}
	if !filepath.IsAbs(model.DebugLogPath()) {
		t.Fatalf("explicit debug log path should be absolute, got %q", model.DebugLogPath())
	}
}
