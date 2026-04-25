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

func TestLoadAcceptsImplementerServerBlock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "llama_server": {
    "binary": "llama-server",
    "host": "127.0.0.1",
    "port": 8080,
    "model": "planner.gguf"
  },
  "implementer": {
    "host": "127.0.0.1",
    "port": 8081,
    "model": "implementer.gguf",
    "ctx_size": 131072
  },
  "chat": {"preset": "qwen3-coding-preserve", "max_tokens": 16}
}`
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Implementer == nil {
		t.Fatal("expected Implementer to be populated")
	}
	if cfg.Implementer.Binary != "llama-server" {
		t.Fatalf("expected implementer.binary to default from llama_server, got %q", cfg.Implementer.Binary)
	}
	if cfg.Implementer.Reasoning != "auto" || cfg.Implementer.ReasoningFormat != "deepseek" {
		t.Fatalf("expected implementer reasoning defaults to inherit from llama_server, got reasoning=%q format=%q",
			cfg.Implementer.Reasoning, cfg.Implementer.ReasoningFormat)
	}
	if cfg.Implementer.LogFile == "" {
		t.Fatal("expected implementer.log_file default")
	}
	if cfg.ImplementerBaseURL() != "http://127.0.0.1:8081" {
		t.Fatalf("unexpected ImplementerBaseURL %q", cfg.ImplementerBaseURL())
	}
}

func TestValidateRejectsImplementerPortCollision(t *testing.T) {
	cfg := Default()
	impl := cfg.LlamaServer
	impl.Model = "implementer.gguf"
	cfg.Implementer = &impl
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "implementer.port must differ") {
		t.Fatalf("expected port-collision validation error, got %v", err)
	}
}

func TestDefaultConfigHasNoImplementerByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Implementer != nil {
		t.Fatalf("expected Default() to omit Implementer, got %+v", cfg.Implementer)
	}
	if cfg.ImplementerBaseURL() != "" {
		t.Fatalf("expected ImplementerBaseURL empty when unset, got %q", cfg.ImplementerBaseURL())
	}
}

func TestLoadAcceptsMLXBackendWithoutPathResolution(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "llama_server": {
    "kind": "mlx",
    "binary": "mlx_lm.server",
    "host": "127.0.0.1",
    "port": 8080,
    "model": "mlx-community/Qwen3.6-27B-MLX-4bit"
  },
  "chat": {"preset": "qwen3-coding-preserve", "max_tokens": 16}
}`
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LlamaServer.Kind != "mlx" {
		t.Fatalf("expected kind=mlx, got %q", cfg.LlamaServer.Kind)
	}
	if cfg.LlamaServer.Model != "mlx-community/Qwen3.6-27B-MLX-4bit" {
		t.Fatalf("expected HF id to be preserved (not joined with baseDir), got %q", cfg.LlamaServer.Model)
	}
	if !cfg.LlamaServer.IsMLX() {
		t.Fatal("expected IsMLX() to be true")
	}
}

func TestValidateRejectsUnknownBackendKind(t *testing.T) {
	cfg := Default()
	cfg.LlamaServer.Kind = "nope"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "llama_server.kind must be one of") {
		t.Fatalf("expected kind validation error, got %v", err)
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
	if cfg.LlamaServer.CtxSize != 196608 {
		t.Fatalf("expected default ctx_size 196608, got %d", cfg.LlamaServer.CtxSize)
	}
}
