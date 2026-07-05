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

	canonicalProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	if cfg.Workspace.Root != canonicalProjectRoot {
		t.Fatalf("expected workspace root %q, got %q", canonicalProjectRoot, cfg.Workspace.Root)
	}

	if cfg.Workspace.CWD != canonicalProjectRoot {
		t.Fatalf("expected cwd %q, got %q", canonicalProjectRoot, cfg.Workspace.CWD)
	}

	if got := cfg.Chat.ProjectPromptFiles[0]; got != filepath.Join(canonicalProjectRoot, "AGENTS.md") {
		t.Fatalf("expected project prompt under project root, got %q", got)
	}

	if cfg.Chat.DebugLogMode != "redacted" || cfg.Chat.MaxToolSteps != 0 || cfg.Chat.MaxOperationalModelCalls != 0 {
		t.Fatalf("expected chat defaults, got mode=%q steps=%d operational_model_calls=%d", cfg.Chat.DebugLogMode, cfg.Chat.MaxToolSteps, cfg.Chat.MaxOperationalModelCalls)
	}

	if cfg.Chat.AttachInstructionFiles {
		t.Fatal("expected instruction attachment disabled by default")
	}
}

func TestGlobalConfigFallbackPolicyIsExplicit(t *testing.T) {
	home := t.TempDir()

	if !shouldUseGlobalConfigFallback(home, LoadOptions{}) {
		t.Fatal("default load policy should allow global config fallback")
	}

	if shouldUseGlobalConfigFallback(home, LoadOptions{DisableGlobalConfigFallback: true}) {
		t.Fatal("explicit load option should disable global config fallback")
	}

	t.Setenv("IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK", "1")
	if shouldUseGlobalConfigFallback(home, LoadOptions{}) {
		t.Fatal("explicit environment policy should disable global config fallback")
	}
}

func TestGlobalConfigFallbackRejectsProcessHomeDuringTests(t *testing.T) {
	home := t.TempDir()
	originalHome := processHomeForGlobalConfigFallback
	processHomeForGlobalConfigFallback = filepath.Clean(home)
	t.Cleanup(func() { processHomeForGlobalConfigFallback = originalHome })

	if shouldUseGlobalConfigFallback(home, LoadOptions{}) {
		t.Fatal("default test load policy should reject process-home global config fallback")
	}

	if !shouldUseGlobalConfigFallback(home, LoadOptions{AllowProcessHomeGlobalConfigFallback: true}) {
		t.Fatal("explicit load option should allow process-home global config fallback")
	}

	t.Setenv("IDIOM_ALLOW_PROCESS_HOME_GLOBAL_CONFIG_FALLBACK", "1")
	if !shouldUseGlobalConfigFallback(home, LoadOptions{}) {
		t.Fatal("explicit environment policy should allow process-home global config fallback")
	}
}

func TestLoadDoesNotUseProcessHomeGlobalFallbackDuringTests(t *testing.T) {
	home := t.TempDir()
	globalDir := filepath.Join(home, ".idiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.json"), []byte(`{"app_name":"process-home-fixture"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalHome := processHomeForGlobalConfigFallback
	processHomeForGlobalConfigFallback = filepath.Clean(home)
	t.Cleanup(func() { processHomeForGlobalConfigFallback = originalHome })
	t.Setenv("HOME", home)

	cfg, err := Load(filepath.Join(t.TempDir(), "missing-config.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.AppName == "process-home-fixture" {
		t.Fatal("plain Load loaded process-home global config during test")
	}
}

func TestLoadAllowsSyntheticHomeGlobalFallbackDuringTests(t *testing.T) {
	originalHome := processHomeForGlobalConfigFallback
	processHomeForGlobalConfigFallback = filepath.Clean(t.TempDir())
	t.Cleanup(func() { processHomeForGlobalConfigFallback = originalHome })

	home := t.TempDir()
	globalDir := filepath.Join(home, ".idiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.json"), []byte(`{"app_name":"synthetic-home-fixture"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := Load(filepath.Join(t.TempDir(), "missing-config.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.AppName != "synthetic-home-fixture" {
		t.Fatalf("Load did not use synthetic HOME fallback, app_name=%q", cfg.AppName)
	}
}

func TestLoadWithOptionsCanDisableGlobalConfigFallback(t *testing.T) {
	home := t.TempDir()
	globalDir := filepath.Join(home, ".idiom")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.json"), []byte(`{"app_name":"global-fixture"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("HOME", home)

	cfg, err := LoadWithOptions(filepath.Join(t.TempDir(), "missing-config.json"), LoadOptions{
		DisableGlobalConfigFallback: true,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions: %v", err)
	}

	if cfg.AppName == "global-fixture" {
		t.Fatal("disabled fallback still loaded the global config")
	}
}

func TestLoadHonorsAttachInstructionFilesFalse(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "chat": {
    "attach_instruction_files": false
  },
  "workspace": {
    "root": ".",
    "cwd": "."
  }
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Chat.AttachInstructionFiles {
		t.Fatal("expected attach_instruction_files=false to disable dynamic instruction attachment")
	}
}

func TestLoadHonorsAttachInstructionFilesTrue(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "chat": {
    "attach_instruction_files": true
  },
  "workspace": {
    "root": ".",
    "cwd": "."
  }
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Chat.AttachInstructionFiles {
		t.Fatal("expected attach_instruction_files=true to enable dynamic instruction attachment")
	}
}

func TestLoadExternalToolDiscoveryContractFlags(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "workspace": {
    "root": ".",
    "cwd": ".",
    "external_tools": [
      {
        "name": "trace_code",
        "description": "Trace source relationships",
        "command": "true",
        "read_only": true,
        "discovery": true
      }
    ]
  }
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Workspace.ExternalTools) != 1 {
		t.Fatalf("expected one external tool, got %#v", cfg.Workspace.ExternalTools)
	}

	tool := cfg.Workspace.ExternalTools[0]
	if !tool.ReadOnly || !tool.Discovery {
		t.Fatalf("expected read_only/discovery flags to load, got %#v", tool)
	}
}

func TestLoadRejectsUnknownConfigKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "chat": {
    "preset": "custom",
    "made_up": true
  },
  "workspace": {
    "root": ".",
    "cwd": "."
  }
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "chat.made_up") {
		t.Fatalf("expected unknown config key error, got %v", err)
	}
}

func TestLoadAcceptsModeledRouteClassifierAliasAndCommentKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "_comment": "allowed JSON comment field",
  "chat": {
    "preset": "custom",
    "max_tokens": 16
  },
  "route_classifier": {
    "enabled": true,
    "always_on": true
  },
  "_mcp": [
    {"name": "disabled", "url": "https://example.invalid"}
  ],
  "workspace": {
    "root": ".",
    "cwd": "."
  }
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.RouteClassifier.AlwaysOn {
		t.Fatal("expected always_on alias to be modeled")
	}
}

func TestCanonicalizeExistingPathResolvesPhysicalPath(t *testing.T) {
	dir := t.TempDir()
	projectRoot := filepath.Join(dir, "project")

	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	linkRoot := filepath.Join(dir, "project-link")
	if err := os.Symlink(projectRoot, linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	got := CanonicalizeExistingPath(linkRoot)

	want, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	if got != want {
		t.Fatalf("canonical path: want %q, got %q", want, got)
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

	cfg = Default()
	cfg.Chat.ModelFamily = "qwen-ish"

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "chat.model_family") {
		t.Fatalf("expected model_family validation error, got %v", err)
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
    "model": "Qwen/Qwen3.6-35B-A3B"
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

	if cfg.LlamaServer.Model != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("expected HF id to be preserved (not joined with baseDir), got %q", cfg.LlamaServer.Model)
	}

	if !cfg.LlamaServer.IsMLX() {
		t.Fatal("expected IsMLX() to be true")
	}
}

func TestLoadAcceptsHostedBackendsWithoutLocalLaunchFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "llama_server": {
    "kind": "codex",
    "model": "gpt-5-codex",
    "reasoning_effort": "high"
  },
  "implementer": {
    "kind": "zai",
    "model": "glm-5.1",
    "base_url": "https://api.z.ai/api/paas/v4",
    "api_key_env": "ZAI_API_KEY"
  },
  "chat": {"preset": "custom", "max_tokens": 16}
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.LlamaServer.IsHosted() || !cfg.LlamaServer.IsOpenAIResponses() {
		t.Fatalf("expected hosted codex planner, got %#v", cfg.LlamaServer)
	}

	if cfg.LlamaServer.Model != "gpt-5-codex" {
		t.Fatalf("hosted model id should not be path-resolved, got %q", cfg.LlamaServer.Model)
	}

	if cfg.LlamaServer.EndpointBaseURL() != "https://api.openai.com" || cfg.LlamaServer.APIKeyEnvName() != "OPENAI_API_KEY" {
		t.Fatalf("unexpected codex endpoint/auth defaults: base=%q env=%q", cfg.LlamaServer.EndpointBaseURL(), cfg.LlamaServer.APIKeyEnvName())
	}

	if cfg.Implementer == nil || !cfg.Implementer.IsZAI() {
		t.Fatalf("expected hosted ZAI implementer, got %#v", cfg.Implementer)
	}

	if cfg.Implementer.EndpointBaseURL() != "https://api.z.ai/api/paas/v4" || cfg.Implementer.APIKeyEnvName() != "ZAI_API_KEY" {
		t.Fatalf("unexpected ZAI endpoint/auth: base=%q env=%q", cfg.Implementer.EndpointBaseURL(), cfg.Implementer.APIKeyEnvName())
	}

	if cfg.ImplementerBaseURL() != "https://api.z.ai/api/paas/v4" {
		t.Fatalf("unexpected hosted ImplementerBaseURL %q", cfg.ImplementerBaseURL())
	}
}

func TestLoadAcceptsGLMHostedBackendDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "llama_server": {
    "kind": "glm",
    "model": "glm-5.2",
    "reasoning_effort": "max"
  },
  "chat": {"preset": "custom", "max_tokens": 16}
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.LlamaServer.IsHosted() || !cfg.LlamaServer.IsGLM() {
		t.Fatalf("expected hosted GLM planner, got %#v", cfg.LlamaServer)
	}

	if cfg.LlamaServer.Model != "glm-5.2" {
		t.Fatalf("hosted GLM model id should not be path-resolved, got %q", cfg.LlamaServer.Model)
	}

	if cfg.LlamaServer.EndpointBaseURL() != "https://api.z.ai/api/coding/paas/v4" || cfg.LlamaServer.APIKeyEnvName() != "ZAI_API_KEY" {
		t.Fatalf("unexpected GLM endpoint/auth defaults: base=%q env=%q", cfg.LlamaServer.EndpointBaseURL(), cfg.LlamaServer.APIKeyEnvName())
	}
}

func TestLoadAcceptsAnthropicHostedBackendDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	data := `{
  "llama_server": {
    "kind": "anthropic",
    "model": "claude-sonnet-4-6"
  },
  "chat": {"preset": "custom", "max_tokens": 16}
}`

	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.LlamaServer.IsHosted() || !cfg.LlamaServer.IsAnthropic() {
		t.Fatalf("expected hosted Anthropic planner, got %#v", cfg.LlamaServer)
	}

	if cfg.LlamaServer.Model != "claude-sonnet-4-6" {
		t.Fatalf("hosted Anthropic model id should not be path-resolved, got %q", cfg.LlamaServer.Model)
	}

	if cfg.LlamaServer.EndpointBaseURL() != "https://api.anthropic.com" || cfg.LlamaServer.APIKeyEnvName() != "ANTHROPIC_API_KEY" {
		t.Fatalf("unexpected Anthropic endpoint/auth defaults: base=%q env=%q", cfg.LlamaServer.EndpointBaseURL(), cfg.LlamaServer.APIKeyEnvName())
	}
}

func TestValidateRejectsUnknownBackendKind(t *testing.T) {
	cfg := Default()

	cfg.LlamaServer.Kind = "nope"

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "llama_server.kind must be one of") {
		t.Fatalf("expected kind validation error, got %v", err)
	}
}

func TestMCPServerValidate(t *testing.T) {
	cases := []struct {
		name       string
		server     MCPServer
		wantErrSub string // empty = expect success
	}{
		{
			name:   "stdio ok",
			server: MCPServer{Name: "exa", Command: "npx", Args: []string{"-y", "x"}},
		},
		{
			name:   "http ok",
			server: MCPServer{Name: "exa", URL: "https://mcp.exa.ai/mcp"},
		},
		{
			name:   "http with headers and timeout ok",
			server: MCPServer{Name: "exa", URL: "https://mcp.exa.ai/mcp", Headers: map[string]string{"X-Api-Key": "x"}, TimeoutSeconds: 25},
		},
		{
			name:       "missing name",
			server:     MCPServer{URL: "https://example.com/"},
			wantErrSub: "name is required",
		},
		{
			name:       "both stdio and http",
			server:     MCPServer{Name: "x", Command: "foo", URL: "https://example.com/"},
			wantErrSub: "both command and url",
		},
		{
			name:       "neither stdio nor http",
			server:     MCPServer{Name: "x"},
			wantErrSub: "either command (stdio) or url (http)",
		},
		{
			name:       "negative timeout",
			server:     MCPServer{Name: "x", URL: "https://example.com/", TimeoutSeconds: -1},
			wantErrSub: "timeout_seconds",
		},
		{
			name:       "stdio with headers",
			server:     MCPServer{Name: "x", Command: "foo", Headers: map[string]string{"k": "v"}},
			wantErrSub: "headers only apply to http",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.server.Validate()
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}

			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSub, err.Error())
			}
		})
	}
}

func TestConfigValidatePropagatesMCPError(t *testing.T) {
	cfg := Default()

	cfg.Chat.SaveDir = t.TempDir()
	cfg.MCP = []MCPServer{{Name: "broken"}} // neither command nor url

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected validation error")
	}

	if !strings.Contains(err.Error(), "mcp[0]") {
		t.Fatalf("expected error to mention mcp[0], got %q", err.Error())
	}
}

func TestMCPServerHelpers(t *testing.T) {
	stdio := MCPServer{Name: "x", Command: "foo"}
	http := MCPServer{Name: "y", URL: "https://example.com/"}

	if !stdio.IsStdio() || stdio.IsHTTP() {
		t.Fatalf("stdio classification wrong: stdio=%v http=%v", stdio.IsStdio(), stdio.IsHTTP())
	}

	if http.IsStdio() || !http.IsHTTP() {
		t.Fatalf("http classification wrong: stdio=%v http=%v", http.IsStdio(), http.IsHTTP())
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

	if cfg.LlamaServer.CtxSize != 262144 {
		t.Fatalf("expected default ctx_size 262144, got %d", cfg.LlamaServer.CtxSize)
	}
}
