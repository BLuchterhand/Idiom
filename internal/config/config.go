package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Config struct {
	AppName     string      `json:"app_name"`
	LlamaServer LlamaServer `json:"llama_server"`
	Chat        Chat        `json:"chat"`
	Workspace   Workspace   `json:"workspace"`
}

type LlamaServer struct {
	Binary          string   `json:"binary"`
	Host            string   `json:"host"`
	Port            int      `json:"port"`
	Model           string   `json:"model"`
	CtxSize         int      `json:"ctx_size"`
	Threads         int      `json:"threads"`
	GPULayers       int      `json:"gpu_layers"`
	Jinja           bool     `json:"jinja"`
	FlashAttention  bool     `json:"flash_attention"`
	Reasoning       string   `json:"reasoning"`
	ReasoningFormat string   `json:"reasoning_format"`
	ReasoningBudget int      `json:"reasoning_budget"`
	LogFile         string   `json:"log_file"`
	ExtraArgs       []string `json:"extra_args"`
}

type Chat struct {
	AutoStartServer        bool     `json:"auto_start_server"`
	SaveDir                string   `json:"save_dir"`
	DebugLogDir            string   `json:"debug_log_dir"`
	DebugLogMode           string   `json:"debug_log_mode"`
	SystemPrompt           string   `json:"system_prompt"`
	SystemPromptFiles      []string `json:"system_prompt_files"`
	ProfilesDir            string   `json:"profiles_dir"`
	DefaultProfiles        []string `json:"default_profiles"`
	ProjectRoot            string   `json:"project_root"`
	ProjectPromptFiles     []string `json:"project_prompt_files"`
	Preset                 string   `json:"preset"`
	Temperature            float64  `json:"temperature"`
	TopP                   float64  `json:"top_p"`
	TopK                   int      `json:"top_k"`
	MinP                   float64  `json:"min_p"`
	PresencePenalty        float64  `json:"presence_penalty"`
	RepetitionPenalty      float64  `json:"repetition_penalty"`
	EnableThinking         *bool    `json:"enable_thinking,omitempty"`
	PreserveThinking       bool     `json:"preserve_thinking"`
	MaxTokens              int      `json:"max_tokens"`
	MaxHistory             int      `json:"max_history"`
	MaxToolSteps           int      `json:"max_tool_steps"`
	RequestTimeoutSeconds  int      `json:"request_timeout_seconds"`
	ServerStartTimeoutSecs int      `json:"server_start_timeout_seconds"`
}

type Workspace struct {
	Root                  string `json:"root"`
	CWD                   string `json:"cwd"`
	Shell                 string `json:"shell"`
	CommandTimeoutSeconds int    `json:"command_timeout_seconds"`
	MaxOutputBytes        int    `json:"max_output_bytes"`
	MaxFileBytes          int    `json:"max_file_bytes"`
	MaxDirectoryEntries   int    `json:"max_directory_entries"`
	EnableShell           bool   `json:"enable_shell"`
	EnableWrites          bool   `json:"enable_writes"`
}

type ChatPreset struct {
	Name              string
	Temperature       float64
	TopP              float64
	TopK              int
	MinP              float64
	PresencePenalty   float64
	RepetitionPenalty float64
	EnableThinking    *bool
	PreserveThinking  bool
	MaxTokens         int
}

func Default() Config {
	return Config{
		AppName: "Qwen Bubbles",
		LlamaServer: LlamaServer{
			Binary:          "llama-server",
			Host:            "127.0.0.1",
			Port:            8080,
			Model:           "models/qwen3.6-35b-a3b/Qwen3.6-35B-A3B-Q4_K_M.gguf",
			CtxSize:         65536,
			Threads:         defaultThreads(),
			GPULayers:       0,
			Jinja:           true,
			FlashAttention:  true,
			Reasoning:       "auto",
			ReasoningFormat: "deepseek",
			ReasoningBudget: -1,
			LogFile:         "logs/llama-server.log",
			ExtraArgs:       []string{},
		},
		Chat: Chat{
			AutoStartServer:        true,
			SaveDir:                "transcripts",
			DebugLogDir:            "logs/conversations",
			DebugLogMode:           "redacted",
			SystemPrompt:           "",
			SystemPromptFiles:      []string{"prompts/base.md"},
			ProfilesDir:            "prompts/profiles",
			DefaultProfiles:        []string{"coding"},
			ProjectRoot:            "",
			ProjectPromptFiles:     []string{},
			Preset:                 "qwen3-coding",
			Temperature:            0.6,
			TopP:                   0.95,
			TopK:                   20,
			MinP:                   0.0,
			PresencePenalty:        0.0,
			RepetitionPenalty:      1.0,
			EnableThinking:         boolPtr(true),
			PreserveThinking:       false,
			MaxTokens:              8192,
			MaxHistory:             20,
			MaxToolSteps:           16,
			RequestTimeoutSeconds:  900,
			ServerStartTimeoutSecs: 240,
		},
		Workspace: Workspace{
			Root:                  "",
			CWD:                   "",
			Shell:                 "/bin/zsh",
			CommandTimeoutSeconds: 120,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   400,
			EnableShell:           true,
			EnableWrites:          true,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	baseDir := filepath.Dir(path)
	cfg.resolvePaths(baseDir)
	cfg.applyRuntimeDefaults()
	cfg.applyWorkspaceDefaults()

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.LlamaServer.Binary == "" {
		return fmt.Errorf("llama_server.binary is required")
	}
	if c.LlamaServer.Host == "" {
		return fmt.Errorf("llama_server.host is required")
	}
	if c.LlamaServer.Port <= 0 {
		return fmt.Errorf("llama_server.port must be positive")
	}
	if c.LlamaServer.Model == "" {
		return fmt.Errorf("llama_server.model is required")
	}
	if c.LlamaServer.CtxSize <= 0 {
		return fmt.Errorf("llama_server.ctx_size must be positive")
	}
	if c.LlamaServer.Reasoning != "" && !containsString([]string{"on", "off", "auto"}, c.LlamaServer.Reasoning) {
		return fmt.Errorf("llama_server.reasoning must be one of on, off, auto")
	}
	if c.LlamaServer.ReasoningFormat != "" && !containsString([]string{"auto", "none", "deepseek", "deepseek-legacy"}, c.LlamaServer.ReasoningFormat) {
		return fmt.Errorf("llama_server.reasoning_format must be one of auto, none, deepseek, deepseek-legacy")
	}
	if c.Chat.SaveDir == "" {
		return fmt.Errorf("chat.save_dir is required")
	}
	if c.Chat.MaxTokens <= 0 {
		return fmt.Errorf("chat.max_tokens must be positive")
	}
	if c.Chat.DebugLogMode != "" && !containsString([]string{"redacted", "full"}, c.Chat.DebugLogMode) {
		return fmt.Errorf("chat.debug_log_mode must be one of redacted, full")
	}
	if c.Chat.Preset != "" && c.Chat.Preset != "custom" {
		if _, ok := lookupBuiltinChatPreset(c.Chat.Preset); !ok {
			return fmt.Errorf("chat.preset %q is not a known built-in preset", c.Chat.Preset)
		}
	}
	if c.Chat.TopK < 0 {
		return fmt.Errorf("chat.top_k must be non-negative")
	}
	if c.Chat.MaxToolSteps <= 0 {
		return fmt.Errorf("chat.max_tool_steps must be positive")
	}
	if c.Chat.RequestTimeoutSeconds <= 0 {
		return fmt.Errorf("chat.request_timeout_seconds must be positive")
	}
	if c.Chat.ServerStartTimeoutSecs <= 0 {
		return fmt.Errorf("chat.server_start_timeout_seconds must be positive")
	}
	if c.Workspace.CommandTimeoutSeconds <= 0 {
		return fmt.Errorf("workspace.command_timeout_seconds must be positive")
	}
	if c.Workspace.MaxOutputBytes <= 0 {
		return fmt.Errorf("workspace.max_output_bytes must be positive")
	}
	if c.Workspace.MaxFileBytes <= 0 {
		return fmt.Errorf("workspace.max_file_bytes must be positive")
	}
	if c.Workspace.MaxDirectoryEntries <= 0 {
		return fmt.Errorf("workspace.max_directory_entries must be positive")
	}
	if c.Workspace.Root != "" {
		rel, err := filepath.Rel(c.Workspace.Root, c.Workspace.CWD)
		if err != nil {
			return fmt.Errorf("workspace.cwd must be inside workspace.root: %w", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("workspace.cwd must be inside workspace.root")
		}
	}
	return nil
}

func (c Config) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", c.LlamaServer.Host, c.LlamaServer.Port)
}

func (c *Config) resolvePaths(baseDir string) {
	c.LlamaServer.Model = resolvePath(baseDir, c.LlamaServer.Model)
	c.LlamaServer.LogFile = resolvePath(baseDir, c.LlamaServer.LogFile)
	c.Chat.SaveDir = resolvePath(baseDir, c.Chat.SaveDir)
	c.Chat.DebugLogDir = resolvePath(baseDir, c.Chat.DebugLogDir)
	c.Chat.SystemPromptFiles = resolvePathSlice(baseDir, c.Chat.SystemPromptFiles)
	c.Chat.ProfilesDir = resolvePath(baseDir, c.Chat.ProfilesDir)
	if c.Chat.ProjectRoot != "" {
		c.Chat.ProjectRoot = resolvePath(baseDir, c.Chat.ProjectRoot)
	}
	c.Chat.ProjectPromptFiles = resolveProjectPathSlice(baseDir, c.Chat.ProjectRoot, c.Chat.ProjectPromptFiles)
	if c.Workspace.Root != "" {
		c.Workspace.Root = resolvePath(baseDir, c.Workspace.Root)
	}
	if c.Workspace.CWD != "" {
		if c.Workspace.Root != "" && !filepath.IsAbs(c.Workspace.CWD) {
			c.Workspace.CWD = filepath.Clean(filepath.Join(c.Workspace.Root, c.Workspace.CWD))
		} else {
			c.Workspace.CWD = resolvePath(baseDir, c.Workspace.CWD)
		}
	}
}

func (c *Config) applyRuntimeDefaults() {
	if c.AppName == "" {
		c.AppName = "Qwen Bubbles"
	}
	if c.LlamaServer.Threads <= 0 {
		c.LlamaServer.Threads = defaultThreads()
	}
	if c.LlamaServer.CtxSize <= 0 {
		c.LlamaServer.CtxSize = 65536
	}
	if c.LlamaServer.Reasoning == "" {
		c.LlamaServer.Reasoning = "auto"
	}
	if c.LlamaServer.ReasoningFormat == "" {
		c.LlamaServer.ReasoningFormat = "deepseek"
	}
	if c.LlamaServer.ReasoningBudget == 0 {
		c.LlamaServer.ReasoningBudget = -1
	}
	if c.LlamaServer.LogFile == "" {
		c.LlamaServer.LogFile = "logs/llama-server.log"
	}
	if c.Chat.SaveDir == "" {
		c.Chat.SaveDir = "transcripts"
	}
	if c.Chat.DebugLogDir == "" {
		c.Chat.DebugLogDir = "logs/conversations"
	}
	if c.Chat.DebugLogMode == "" {
		c.Chat.DebugLogMode = "redacted"
	}
	if len(c.Chat.SystemPromptFiles) == 0 {
		c.Chat.SystemPromptFiles = []string{"prompts/base.md"}
	}
	if c.Chat.ProfilesDir == "" {
		c.Chat.ProfilesDir = "prompts/profiles"
	}
	if len(c.Chat.DefaultProfiles) == 0 {
		c.Chat.DefaultProfiles = []string{"coding"}
	}
	if c.Chat.Preset == "" {
		c.Chat.Preset = "qwen3-coding"
	}
	if c.Chat.TopK == 0 {
		c.Chat.TopK = 20
	}
	if c.Chat.RepetitionPenalty == 0 {
		c.Chat.RepetitionPenalty = 1.0
	}
	if c.Chat.MaxHistory <= 0 {
		c.Chat.MaxHistory = 20
	}
	if c.Chat.MaxToolSteps <= 0 {
		c.Chat.MaxToolSteps = 16
	}
	if c.Chat.RequestTimeoutSeconds <= 0 {
		c.Chat.RequestTimeoutSeconds = 900
	}
	if c.Chat.ServerStartTimeoutSecs <= 0 {
		c.Chat.ServerStartTimeoutSecs = 240
	}
	if c.Chat.Preset != "custom" {
		if preset, ok := lookupBuiltinChatPreset(c.Chat.Preset); ok {
			applyChatPreset(&c.Chat, preset)
		}
	}
	if c.Workspace.Shell == "" {
		c.Workspace.Shell = "/bin/zsh"
	}
	if c.Workspace.CommandTimeoutSeconds <= 0 {
		c.Workspace.CommandTimeoutSeconds = 120
	}
	if c.Workspace.MaxOutputBytes <= 0 {
		c.Workspace.MaxOutputBytes = 65536
	}
	if c.Workspace.MaxFileBytes <= 0 {
		c.Workspace.MaxFileBytes = 65536
	}
	if c.Workspace.MaxDirectoryEntries <= 0 {
		c.Workspace.MaxDirectoryEntries = 400
	}
}

func (c *Config) applyWorkspaceDefaults() {
	launchDir := strings.TrimSpace(os.Getenv("QWEN_BUBBLES_SPAWN_DIR"))
	if launchDir == "" {
		if wd, err := os.Getwd(); err == nil {
			launchDir = wd
		}
	}
	if c.Workspace.Root == "" {
		c.Workspace.Root = launchDir
	}
	if c.Workspace.CWD == "" {
		c.Workspace.CWD = c.Workspace.Root
	}
	if c.Chat.ProjectRoot == "" {
		c.Chat.ProjectRoot = c.Workspace.Root
	}
}

func resolvePath(baseDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func resolvePathSlice(baseDir string, paths []string) []string {
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved = append(resolved, resolvePath(baseDir, path))
	}
	return resolved
}

func resolveProjectPathSlice(baseDir, projectRoot string, paths []string) []string {
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		if filepath.IsAbs(path) {
			resolved = append(resolved, path)
			continue
		}
		if projectRoot != "" {
			resolved = append(resolved, filepath.Clean(filepath.Join(projectRoot, path)))
			continue
		}
		resolved = append(resolved, resolvePath(baseDir, path))
	}
	return resolved
}

func defaultThreads() int {
	n := runtime.NumCPU() - 2
	if n < 1 {
		return 1
	}
	return n
}

func BuiltinChatPresets() map[string]ChatPreset {
	return map[string]ChatPreset{
		"qwen3-coding": {
			Name:              "qwen3-coding",
			Temperature:       0.6,
			TopP:              0.95,
			TopK:              20,
			MinP:              0.0,
			PresencePenalty:   0.0,
			RepetitionPenalty: 1.0,
			EnableThinking:    boolPtr(true),
			PreserveThinking:  false,
			MaxTokens:         8192,
		},
		"qwen3-coding-preserve": {
			Name:              "qwen3-coding-preserve",
			Temperature:       0.6,
			TopP:              0.95,
			TopK:              20,
			MinP:              0.0,
			PresencePenalty:   0.0,
			RepetitionPenalty: 1.0,
			EnableThinking:    boolPtr(true),
			PreserveThinking:  true,
			MaxTokens:         8192,
		},
		"qwen3-general": {
			Name:              "qwen3-general",
			Temperature:       1.0,
			TopP:              0.95,
			TopK:              20,
			MinP:              0.0,
			PresencePenalty:   1.5,
			RepetitionPenalty: 1.0,
			EnableThinking:    boolPtr(true),
			PreserveThinking:  false,
			MaxTokens:         8192,
		},
		"qwen3-direct": {
			Name:              "qwen3-direct",
			Temperature:       0.7,
			TopP:              0.8,
			TopK:              20,
			MinP:              0.0,
			PresencePenalty:   1.5,
			RepetitionPenalty: 1.0,
			EnableThinking:    boolPtr(false),
			PreserveThinking:  false,
			MaxTokens:         4096,
		},
		"qwen3-direct-reasoning": {
			Name:              "qwen3-direct-reasoning",
			Temperature:       1.0,
			TopP:              0.95,
			TopK:              20,
			MinP:              0.0,
			PresencePenalty:   1.5,
			RepetitionPenalty: 1.0,
			EnableThinking:    boolPtr(false),
			PreserveThinking:  false,
			MaxTokens:         4096,
		},
	}
}

func BuiltinChatPresetNames() []string {
	presets := BuiltinChatPresets()
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ApplyBuiltinChatPreset(chat *Chat, name string) error {
	preset, ok := lookupBuiltinChatPreset(name)
	if !ok {
		return fmt.Errorf("unknown preset %q", name)
	}
	applyChatPreset(chat, preset)
	return nil
}

func lookupBuiltinChatPreset(name string) (ChatPreset, bool) {
	preset, ok := BuiltinChatPresets()[strings.TrimSpace(name)]
	return preset, ok
}

func applyChatPreset(chat *Chat, preset ChatPreset) {
	chat.Preset = preset.Name
	chat.Temperature = preset.Temperature
	chat.TopP = preset.TopP
	chat.TopK = preset.TopK
	chat.MinP = preset.MinP
	chat.PresencePenalty = preset.PresencePenalty
	chat.RepetitionPenalty = preset.RepetitionPenalty
	chat.EnableThinking = cloneBoolPtr(preset.EnableThinking)
	chat.PreserveThinking = preset.PreserveThinking
	if preset.MaxTokens > 0 {
		chat.MaxTokens = preset.MaxTokens
	}
}

func boolPtr(v bool) *bool {
	b := v
	return &b
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	return boolPtr(*v)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
