package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

type Config struct {
	AppName         string          `json:"app_name"`
	LlamaServer     LlamaServer     `json:"llama_server"`
	Implementer     *LlamaServer    `json:"implementer,omitempty"`
	Chat            Chat            `json:"chat"`
	Workspace       Workspace       `json:"workspace"`
	MCP             []MCPServer     `json:"mcp,omitempty"`
	RouteClassifier RouteClassifier `json:"route_classifier"`
}

// RouteClassifier describes the secondary (small) model used for lightweight
// tasks such as history summarization. Maps to the existing route_classifier
// block in config.json. When AutoStartServer is true, the harness will spawn
// llama-server with these settings on demand the first time a summary is
// requested and the endpoint is unreachable.
type RouteClassifier struct {
	Enabled             bool     `json:"enabled"`
	AlwaysOn            bool     `json:"always_on,omitempty"`
	BaseURL             string   `json:"base_url"`
	Model               string   `json:"model"`
	MaxTokens           int      `json:"max_tokens"`
	TimeoutSeconds      int      `json:"timeout_seconds"`
	StartTimeoutSeconds int      `json:"start_timeout_seconds"`
	AutoStartServer     bool     `json:"auto_start_server"`
	Kind                string   `json:"kind,omitempty"`
	Binary              string   `json:"binary"`
	Host                string   `json:"host"`
	Port                int      `json:"port"`
	ServerModel         string   `json:"server_model"`
	CtxSize             int      `json:"ctx_size"`
	Threads             int      `json:"threads"`
	GPULayers           int      `json:"gpu_layers"`
	LogFile             string   `json:"log_file"`
	ExtraArgs           []string `json:"extra_args"`
}

// LlamaServerSpec returns a LlamaServer struct derived from the
// RouteClassifier's server fields. Used to hand the classifier off to
// llama.NewServerController for on-demand launch.
func (r RouteClassifier) LlamaServerSpec() LlamaServer {
	return LlamaServer{
		Kind:            r.Kind,
		Binary:          r.Binary,
		Host:            r.Host,
		Port:            r.Port,
		Model:           r.ServerModel,
		CtxSize:         r.CtxSize,
		Threads:         r.Threads,
		GPULayers:       r.GPULayers,
		Jinja:           true,
		FlashAttention:  true,
		Reasoning:       "off",
		ReasoningFormat: "none",
		ReasoningBudget: 0,
		LogFile:         r.LogFile,
		ExtraArgs:       append([]string(nil), r.ExtraArgs...),
	}
}

// MCPServer describes one MCP server. Two transports are supported and
// they are mutually exclusive on a per-entry basis:
//
//   - **stdio**: set Command (+ optional Args, Env). The harness spawns the
//     process and speaks JSON-RPC over its stdio pair.
//   - **http**: set URL (+ optional Headers). The harness POSTs JSON-RPC
//     to the endpoint and reads the response as either application/json
//     or text/event-stream. Used for hosted MCP servers like Exa AI's
//     https://mcp.exa.ai/mcp.
//
// TimeoutSeconds is optional and applies only to the http transport;
// it sets the per-request HTTP client timeout. Zero falls back to the
// transport default (30 s).
type MCPServer struct {
	Name           string            `json:"name"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            []string          `json:"env,omitempty"`
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// IsHTTP reports whether the server is configured for HTTP transport.
func (m MCPServer) IsHTTP() bool {
	return strings.TrimSpace(m.URL) != ""
}

// IsStdio reports whether the server is configured for stdio transport.
func (m MCPServer) IsStdio() bool {
	return strings.TrimSpace(m.Command) != ""
}

// Backend kinds accepted by LlamaServer.Kind.
const (
	BackendLlama = "llama"
	BackendMLX   = "mlx"
)

// LlamaServer describes a local inference backend the harness can launch and
// talk to via an OpenAI-compatible HTTP API. Despite the name, two backend
// kinds are supported: "llama" (llama.cpp's llama-server, the default) and
// "mlx" (Apple MLX's mlx_lm.server on Apple Silicon). The llama-only fields
// below (Jinja, FlashAttention, Reasoning*) are ignored for kind=mlx.
type LlamaServer struct {
	Kind            string   `json:"kind,omitempty"`
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

// IsMLX reports whether this backend is an MLX-based server (mlx_lm.server or
// equivalent) rather than llama.cpp. Empty Kind defaults to llama.
func (l LlamaServer) IsMLX() bool {
	return strings.EqualFold(strings.TrimSpace(l.Kind), BackendMLX)
}

type Chat struct {
	AutoStartServer        bool     `json:"auto_start_server"`
	SaveDir                string   `json:"save_dir"`
	DebugLogDir            string   `json:"debug_log_dir"`
	DebugLogMode           string   `json:"debug_log_mode"`
	SystemPrompt           string   `json:"system_prompt"`
	SystemPromptFiles      []string `json:"system_prompt_files"`
	AllowMissingPrompts    bool     `json:"allow_missing_prompts,omitempty"`
	ProfilesDir            string   `json:"profiles_dir"`
	DefaultProfiles        []string `json:"default_profiles"`
	ProjectRoot            string   `json:"project_root"`
	ProjectPromptFiles     []string `json:"project_prompt_files"`
	AttachInstructionFiles bool     `json:"attach_instruction_files"`
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
	MaxContextMessages     int      `json:"max_context_messages"`
	MaxContextChars        int      `json:"max_context_chars"`
	MaxModelCalls          int      `json:"max_model_calls"`
	MaxDurationSeconds     int      `json:"max_duration_seconds"`
	RequestTimeoutSeconds  int      `json:"request_timeout_seconds"`
	ServerStartTimeoutSecs int      `json:"server_start_timeout_seconds"`
	// AgentMode selects which built-in agent profile drives the session.
	// OpenCode-shaped: the user picks build/plan/general; the harness
	// never classifies a prompt to pick one on their behalf. Empty falls
	// back to "build" (full capability). See internal/agent/modes.go.
	AgentMode string `json:"agent_mode,omitempty"`
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
	// ToolRules implements Fix 6 (per-tool permission granularity). Map key
	// is the tool name; each value is an ordered list of
	// {pattern, decision} rules evaluated first-match-wins. Omitting a tool
	// (or the whole section) preserves the legacy EnableShell/EnableWrites
	// behavior. See docs/opencode-fixes-design.md §6.
	ToolRules map[string][]ToolRule `json:"tool_rules,omitempty"`
	// AllowPrivateNetwork lets HTTP-transport MCP servers connect to
	// loopback, link-local, and RFC1918 addresses. Off by default; flip
	// only when registering an MCP server inside a corp intranet you
	// know is yours. See docs/web-search-tool-design.md §6 W6.
	AllowPrivateNetwork bool `json:"allow_private_network,omitempty"`

	// ExternalTools registers project-specific shell-invokable tools
	// the model can call by name (alongside built-ins and MCP). Each
	// entry has a Name, Description, Command, optional templated Args,
	// optional Cwd, optional TimeoutSeconds, and a JSON Schema for
	// the model's input parameters. See internal/workspace/external_tool.go
	// for invocation semantics + template syntax.
	//
	// Idiom is generic — these definitions live in the host project's
	// config. Nothing about idiom hard-codes a specific tool, command,
	// or project.
	ExternalTools []ExternalToolDef `json:"external_tools,omitempty"`

	// ExternalToolsDir, when non-empty, is scanned at startup for
	// per-tool JSON files (one ExternalToolDef per file). Loaded in
	// addition to ExternalTools. Path is resolved against the
	// workspace root if relative.
	ExternalToolsDir string `json:"external_tools_dir,omitempty"`
}

// ExternalToolDef mirrors workspace.ExternalTool at the config layer.
// Kept in this package to avoid import cycles between config and
// workspace; the manager copies these into workspace.ExternalTool at
// init time.
type ExternalToolDef struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Command        string          `json:"command"`
	Args           []string        `json:"args,omitempty"`
	Cwd            string          `json:"cwd,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
	InputSchema    json.RawMessage `json:"input_schema,omitempty"`
}

// ToolRule is a glob-style permission rule for a specific tool. Pattern
// semantics are described in internal/workspace/tool_rules.go.
type ToolRule struct {
	Pattern  string `json:"pattern"`
	Decision string `json:"decision"`
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
		AppName: "Idiom",
		LlamaServer: LlamaServer{
			Binary:          "llama-server",
			Host:            "127.0.0.1",
			Port:            8080,
			Model:           "models/qwen3.6-35b-a3b/Qwen3.6-35B-A3B-Q4_K_M.gguf",
			CtxSize:         196608,
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
			AttachInstructionFiles: false,
			Preset:                 "qwen3-coding-preserve",
			Temperature:            0.6,
			TopP:                   0.95,
			TopK:                   20,
			MinP:                   0.0,
			PresencePenalty:        0.0,
			RepetitionPenalty:      1.0,
			EnableThinking:         boolPtr(true),
			PreserveThinking:       true,
			MaxTokens:              8192,
			MaxHistory:             20,
			MaxToolSteps:           16,
			MaxContextMessages:     40,
			MaxContextChars:        200000,
			MaxModelCalls:          40,
			MaxDurationSeconds:     780,
			RequestTimeoutSeconds:  900,
			ServerStartTimeoutSecs: 240,
			AgentMode:              "build",
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

	if unknown, err := UnknownJSONKeys(data, Config{}); err != nil {
		return Config{}, fmt.Errorf("inspect config keys: %w", err)
	} else if len(unknown) > 0 {
		return Config{}, fmt.Errorf("unknown config key(s): %s", strings.Join(unknown, ", "))
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	baseDir := filepath.Dir(path)
	cfg.resolvePaths(baseDir)
	cfg.applyRuntimeDefaults()
	cfg.applyWorkspaceDefaults()
	cfg.canonicalizeWorkspacePaths()

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// UnknownJSONKeys returns JSON object keys that are not modeled by target's
// json tags. Keys beginning with "_" are treated as JSON-comment fields so
// example config files can carry disabled blocks such as "_mcp".
func UnknownJSONKeys(data []byte, target any) ([]string, error) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	var unknown []string
	collectUnknownJSONKeys(decoded, reflect.TypeOf(target), "", &unknown)
	sort.Strings(unknown)
	return unknown, nil
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

func collectUnknownJSONKeys(value any, typ reflect.Type, path string, unknown *[]string) {
	if typ == nil || value == nil {
		return
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == rawMessageType {
		return
	}
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		fields := jsonFieldTypes(typ)
		for key, child := range object {
			if strings.HasPrefix(key, "_") {
				continue
			}
			fieldType, ok := fields[key]
			if !ok {
				*unknown = append(*unknown, joinConfigPath(path, key))
				continue
			}
			collectUnknownJSONKeys(child, fieldType, joinConfigPath(path, key), unknown)
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return
		}
		for i, item := range items {
			collectUnknownJSONKeys(item, typ.Elem(), fmt.Sprintf("%s[%d]", path, i), unknown)
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		elem := typ.Elem()
		for key, child := range object {
			collectUnknownJSONKeys(child, elem, joinConfigPath(path, key), unknown)
		}
	}
}

func jsonFieldTypes(typ reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func joinConfigPath(base string, key string) string {
	key = strings.TrimSpace(key)
	if base == "" {
		return key
	}
	if key == "" {
		return base
	}
	return base + "." + key
}

func (c Config) Validate() error {
	if err := validateBackend("llama_server", c.LlamaServer); err != nil {
		return err
	}
	if c.Implementer != nil {
		if err := validateBackend("implementer", *c.Implementer); err != nil {
			return err
		}
		if c.Implementer.Port == c.LlamaServer.Port && c.Implementer.Host == c.LlamaServer.Host {
			return fmt.Errorf("implementer.port must differ from llama_server.port on the same host")
		}
	}
	if k := strings.TrimSpace(c.RouteClassifier.Kind); k != "" && k != BackendLlama && k != BackendMLX {
		return fmt.Errorf("route_classifier.kind must be one of llama, mlx (got %q)", c.RouteClassifier.Kind)
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
	if c.Chat.MaxToolSteps < 0 {
		return fmt.Errorf("chat.max_tool_steps must be non-negative (0 means unbounded)")
	}
	if c.Chat.MaxContextChars < 0 {
		return fmt.Errorf("chat.max_context_chars must be non-negative (0 disables compaction)")
	}
	if c.Chat.MaxContextMessages < 0 {
		return fmt.Errorf("chat.max_context_messages must be non-negative (0 disables compaction)")
	}
	if c.Chat.MaxModelCalls < 0 {
		return fmt.Errorf("chat.max_model_calls must be non-negative (0 disables the soft budget)")
	}
	if c.Chat.MaxDurationSeconds < 0 {
		return fmt.Errorf("chat.max_duration_seconds must be non-negative (0 disables the soft budget)")
	}
	if c.Chat.RequestTimeoutSeconds <= 0 {
		return fmt.Errorf("chat.request_timeout_seconds must be positive")
	}
	if c.Chat.ServerStartTimeoutSecs <= 0 {
		return fmt.Errorf("chat.server_start_timeout_seconds must be positive")
	}
	if trimmed := strings.TrimSpace(c.Chat.AgentMode); trimmed != "" {
		known := map[string]struct{}{"build": {}, "plan": {}, "general": {}}
		if _, ok := known[strings.ToLower(trimmed)]; !ok {
			return fmt.Errorf("chat.agent_mode must be one of build, plan, general (got %q)", trimmed)
		}
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
	for i, srv := range c.MCP {
		if err := srv.Validate(); err != nil {
			return fmt.Errorf("mcp[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate enforces the stdio-XOR-http invariant on a single MCP server
// entry. Both unset or both set is a config error; we do not silently
// pick one transport over the other.
func (m MCPServer) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("name is required")
	}
	hasStdio := m.IsStdio()
	hasHTTP := m.IsHTTP()
	switch {
	case hasStdio && hasHTTP:
		return fmt.Errorf("server %q sets both command and url; pick one", m.Name)
	case !hasStdio && !hasHTTP:
		return fmt.Errorf("server %q must set either command (stdio) or url (http)", m.Name)
	}
	if hasHTTP && m.TimeoutSeconds < 0 {
		return fmt.Errorf("server %q: timeout_seconds must be non-negative", m.Name)
	}
	if hasStdio && (len(m.Headers) > 0) {
		return fmt.Errorf("server %q: headers only apply to http transport", m.Name)
	}
	return nil
}

func (c Config) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", c.LlamaServer.Host, c.LlamaServer.Port)
}

// ImplementerBaseURL returns the HTTP URL for the implementer llama-server,
// or an empty string when no implementer is configured. The app uses an
// empty return to mean "no secondary server — child tasks run on the
// primary server like they always have".
func (c Config) ImplementerBaseURL() string {
	if c.Implementer == nil {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", c.Implementer.Host, c.Implementer.Port)
}

func (c *Config) resolvePaths(baseDir string) {
	if !c.LlamaServer.IsMLX() {
		c.LlamaServer.Model = resolvePath(baseDir, c.LlamaServer.Model)
	}
	c.LlamaServer.LogFile = resolvePath(baseDir, c.LlamaServer.LogFile)
	if c.Implementer != nil {
		if !c.Implementer.IsMLX() {
			c.Implementer.Model = resolvePath(baseDir, c.Implementer.Model)
		}
		c.Implementer.LogFile = resolvePath(baseDir, c.Implementer.LogFile)
	}
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
		c.AppName = "Idiom"
	}
	if c.LlamaServer.Kind == "" {
		c.LlamaServer.Kind = BackendLlama
	}
	applyBackendRuntimeDefaults(&c.LlamaServer, nil, "logs/llama-server.log")
	if c.Implementer != nil {
		if c.Implementer.Kind == "" {
			c.Implementer.Kind = BackendLlama
		}
		if c.Implementer.Binary == "" {
			c.Implementer.Binary = c.LlamaServer.Binary
		}
		if c.Implementer.Host == "" {
			c.Implementer.Host = c.LlamaServer.Host
		}
		applyBackendRuntimeDefaults(c.Implementer, &c.LlamaServer, "logs/implementer-server.log")
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
		c.Chat.Preset = "qwen3-coding-preserve"
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
	// MaxToolSteps: 0 means unbounded (OpenCode-aligned). Don't auto-bump.
	if c.Chat.MaxContextMessages < 0 {
		c.Chat.MaxContextMessages = 0
	}
	if c.Chat.MaxContextChars < 0 {
		c.Chat.MaxContextChars = 0
	}
	if c.Chat.MaxModelCalls < 0 {
		c.Chat.MaxModelCalls = 0
	}
	if c.Chat.MaxDurationSeconds < 0 {
		c.Chat.MaxDurationSeconds = 0
	}
	if c.Chat.RequestTimeoutSeconds <= 0 {
		c.Chat.RequestTimeoutSeconds = 900
	}
	if c.Chat.ServerStartTimeoutSecs <= 0 {
		c.Chat.ServerStartTimeoutSecs = 240
	}
	if strings.TrimSpace(c.Chat.AgentMode) == "" {
		c.Chat.AgentMode = "build"
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
	launchDir := strings.TrimSpace(os.Getenv("IDIOM_SPAWN_DIR"))
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

func (c *Config) canonicalizeWorkspacePaths() {
	c.Workspace.Root = CanonicalizeExistingPath(c.Workspace.Root)
	c.Workspace.CWD = CanonicalizeExistingPath(c.Workspace.CWD)
	c.Chat.ProjectRoot = CanonicalizeExistingPath(c.Chat.ProjectRoot)
	c.Chat.ProjectPromptFiles = canonicalizePathSlice(c.Chat.ProjectPromptFiles)
}

func canonicalizePathSlice(paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = CanonicalizeExistingPath(path)
	}
	return out
}

// CanonicalizeExistingPath returns the physical absolute spelling for an
// existing path. On case-insensitive filesystems this prevents logically
// equivalent roots such as "Idiom" and "idiom" from splitting telemetry,
// caches, or language-server workspace identities.
func CanonicalizeExistingPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if resolved, err := filepath.Abs(abs); err == nil {
			abs = resolved
		}
	}
	abs = filepath.Clean(abs)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return canonicalizeExistingPathCase(real)
	}
	current := abs
	var suffix []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
		if real, err := filepath.EvalSymlinks(current); err == nil {
			parts := append([]string{canonicalizeExistingPathCase(real)}, suffix...)
			return filepath.Join(parts...)
		}
	}
	return abs
}

func canonicalizeExistingPathCase(abs string) string {
	cleaned := filepath.Clean(abs)
	if cleaned == "." || cleaned == "" {
		return cleaned
	}
	volume := filepath.VolumeName(cleaned)
	rest := strings.TrimPrefix(cleaned[len(volume):], string(filepath.Separator))
	if rest == "" {
		if volume != "" {
			return volume + string(filepath.Separator)
		}
		return string(filepath.Separator)
	}
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		matched := part
		entries, err := os.ReadDir(current)
		if err != nil {
			return cleaned
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), part) {
				matched = entry.Name()
				break
			}
		}
		current = filepath.Join(current, matched)
	}
	return current
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

// applyBackendRuntimeDefaults fills llama-specific defaults when the backend
// kind is llama. For MLX backends, ctx_size / threads / reasoning flags are
// left zero (mlx_lm.server has no equivalents), but generic fields like the
// log file path still get defaulted. If parent is non-nil, llama reasoning
// fields inherit from it when unset (used for the implementer block).
func applyBackendRuntimeDefaults(b *LlamaServer, parent *LlamaServer, defaultLogFile string) {
	if b.LogFile == "" {
		b.LogFile = defaultLogFile
	}
	if b.IsMLX() {
		return
	}
	if b.Threads <= 0 {
		b.Threads = defaultThreads()
	}
	if b.CtxSize <= 0 {
		b.CtxSize = 196608
	}
	if b.Reasoning == "" {
		if parent != nil && parent.Reasoning != "" {
			b.Reasoning = parent.Reasoning
		} else {
			b.Reasoning = "auto"
		}
	}
	if b.ReasoningFormat == "" {
		if parent != nil && parent.ReasoningFormat != "" {
			b.ReasoningFormat = parent.ReasoningFormat
		} else {
			b.ReasoningFormat = "deepseek"
		}
	}
	if b.ReasoningBudget == 0 {
		if parent != nil {
			b.ReasoningBudget = parent.ReasoningBudget
		} else {
			b.ReasoningBudget = -1
		}
	}
}

func validateBackend(name string, b LlamaServer) error {
	kind := strings.TrimSpace(b.Kind)
	if kind != "" && kind != BackendLlama && kind != BackendMLX {
		return fmt.Errorf("%s.kind must be one of llama, mlx (got %q)", name, b.Kind)
	}
	if b.Binary == "" {
		return fmt.Errorf("%s.binary is required", name)
	}
	if b.Host == "" {
		return fmt.Errorf("%s.host is required", name)
	}
	if b.Port <= 0 {
		return fmt.Errorf("%s.port must be positive", name)
	}
	if b.Model == "" {
		return fmt.Errorf("%s.model is required", name)
	}
	if !b.IsMLX() {
		if b.CtxSize <= 0 {
			return fmt.Errorf("%s.ctx_size must be positive", name)
		}
		if b.Reasoning != "" && !containsString([]string{"on", "off", "auto"}, b.Reasoning) {
			return fmt.Errorf("%s.reasoning must be one of on, off, auto", name)
		}
		if b.ReasoningFormat != "" && !containsString([]string{"auto", "none", "deepseek", "deepseek-legacy"}, b.ReasoningFormat) {
			return fmt.Errorf("%s.reasoning_format must be one of auto, none, deepseek, deepseek-legacy", name)
		}
	}
	return nil
}
