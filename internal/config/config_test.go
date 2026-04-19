package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesDefaultsAndResolvesWorkspacePaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	projectRoot := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := `{
  "llama_server": {
    "binary": "llama-server",
    "host": "127.0.0.1",
    "port": 8080,
    "model": "model.gguf"
  },
  "chat": {
    "project_root": "project",
    "project_prompt_files": ["AGENTS.md"],
    "preset": "custom",
    "max_tokens": 16
  },
  "workspace": {
    "root": "project",
    "cwd": ".",
    "enable_shell": true,
    "enable_writes": true
  }
}`
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workspace.Root != projectRoot {
		t.Fatalf("expected workspace root %q, got %q", projectRoot, cfg.Workspace.Root)
	}
	if cfg.Workspace.CWD != projectRoot {
		t.Fatalf("expected cwd %q, got %q", projectRoot, cfg.Workspace.CWD)
	}
	if got := cfg.Chat.ProjectPromptFiles[0]; got != filepath.Join(projectRoot, "AGENTS.md") {
		t.Fatalf("expected project prompt under project root, got %q", got)
	}
	if cfg.Chat.DebugLogMode != "redacted" || cfg.Chat.MaxToolSteps != 16 {
		t.Fatalf("expected chat defaults, got mode=%q steps=%d", cfg.Chat.DebugLogMode, cfg.Chat.MaxToolSteps)
	}
}

func TestValidateRejectsUnsafeOrInvalidConfig(t *testing.T) {
	cfg := Default()
	cfg.Chat.Preset = "not-a-preset"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not a known built-in preset") {
		t.Fatalf("expected preset validation error, got %v", err)
	}

	cfg = Default()
	cfg.Workspace.Root = "/tmp/root"
	cfg.Workspace.CWD = "/tmp/elsewhere"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "workspace.cwd must be inside workspace.root") {
		t.Fatalf("expected cwd containment error, got %v", err)
	}
}

func TestApplyBuiltinChatPresetClonesThinkingPointer(t *testing.T) {
	var chat Chat
	if err := ApplyBuiltinChatPreset(&chat, "qwen3-direct"); err != nil {
		t.Fatalf("ApplyBuiltinChatPreset: %v", err)
	}
	if chat.EnableThinking == nil || *chat.EnableThinking {
		t.Fatalf("expected direct preset to disable thinking, got %#v", chat.EnableThinking)
	}

	original := chat.EnableThinking
	if err := ApplyBuiltinChatPreset(&chat, "qwen3-coding"); err != nil {
		t.Fatalf("ApplyBuiltinChatPreset: %v", err)
	}
	if chat.EnableThinking == nil || !*chat.EnableThinking {
		t.Fatalf("expected coding preset to enable thinking, got %#v", chat.EnableThinking)
	}
	if original == chat.EnableThinking {
		t.Fatal("expected preset application to clone bool pointer")
	}
}
