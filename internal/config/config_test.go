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
	if !cfg.RouteClassifier.Enabled || !cfg.RouteClassifier.AlwaysOn || cfg.RouteClassifier.MaxTokens != 512 || cfg.RouteClassifier.TimeoutSeconds != 20 || cfg.RouteClassifier.StartTimeoutSeconds != 120 {
		t.Fatalf("expected route classifier defaults, got %#v", cfg.RouteClassifier)
	}
	if cfg.RouteClassifier.BaseURL != "" || cfg.RouteClassifier.Model != "" {
		t.Fatalf("expected route classifier endpoint/model to default to active chat endpoint/model, got %#v", cfg.RouteClassifier)
	}
	if cfg.RouteClassifier.AutoStartServer {
		t.Fatalf("expected classifier auto-start to be opt-in by default, got %#v", cfg.RouteClassifier)
	}
}

func TestValidateRejectsUnsafeOrInvalidConfig(t *testing.T) {
	cfg := Default()
	cfg.Chat.Preset = "not-a-preset"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not a known built-in preset") {
		t.Fatalf("expected preset validation error, got %v", err)
	}

	cfg = Default()
	cfg.RouteClassifier.MaxTokens = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "route_classifier.max_tokens") {
		t.Fatalf("expected route classifier max token validation error, got %v", err)
	}

	cfg = Default()
	cfg.RouteClassifier.TimeoutSeconds = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "route_classifier.timeout_seconds") {
		t.Fatalf("expected route classifier timeout validation error, got %v", err)
	}

	cfg = Default()
	cfg.RouteClassifier.StartTimeoutSeconds = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "route_classifier.start_timeout_seconds") {
		t.Fatalf("expected route classifier start timeout validation error, got %v", err)
	}

	cfg = Default()
	cfg.RouteClassifier.AutoStartServer = true
	cfg.RouteClassifier.ServerModel = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "route_classifier.server_model") {
		t.Fatalf("expected route classifier server model validation error, got %v", err)
	}

	cfg = Default()
	cfg.RouteClassifier.AutoStartServer = true
	cfg.RouteClassifier.ServerModel = "classifier.gguf"
	cfg.RouteClassifier.BaseURL = ""
	if got := cfg.RouteClassifierBaseURL(); got != "http://127.0.0.1:11437" {
		t.Fatalf("expected derived classifier URL, got %q", got)
	}
	serverCfg := cfg.RouteClassifierServerConfig()
	if serverCfg.Model != "classifier.gguf" || serverCfg.Reasoning != "off" || serverCfg.ReasoningFormat != "none" || !serverCfg.Jinja || serverCfg.CtxSize != 8192 {
		t.Fatalf("expected classifier server config, got %#v", serverCfg)
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

func TestDefaultConfigPreservesQwenThinking(t *testing.T) {
	cfg := Default()
	if cfg.Chat.Preset != "qwen3-coding-preserve" {
		t.Fatalf("expected preserve preset by default, got %q", cfg.Chat.Preset)
	}
	if cfg.Chat.EnableThinking == nil || !*cfg.Chat.EnableThinking {
		t.Fatalf("expected thinking enabled by default, got %#v", cfg.Chat.EnableThinking)
	}
	if !cfg.Chat.PreserveThinking {
		t.Fatal("expected preserve_thinking enabled by default")
	}
}
