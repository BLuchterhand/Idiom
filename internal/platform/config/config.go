package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"idiom/internal/platform/textutil"
)

type Config struct {
	AppName         string          `json:"app_name"`
	LlamaServer     LlamaServer     `json:"llama_server"`
	Implementer     *LlamaServer    `json:"implementer,omitempty"`
	Chat            Chat            `json:"chat"`
	TUI             TUI             `json:"tui,omitempty"`
	Workspace       Workspace       `json:"workspace"`
	MCP             []MCPServer     `json:"mcp,omitempty"`
	RouteClassifier RouteClassifier `json:"route_classifier"`
}

type LoadOptions struct {
	DisableGlobalConfigFallback          bool
	AllowProcessHomeGlobalConfigFallback bool
}

var processHomeForGlobalConfigFallback = initialProcessHomeForGlobalConfigFallback()

func initialProcessHomeForGlobalConfigFallback() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Clean(strings.TrimSpace(home))
}

// TUI holds terminal-UI behaviour flags.
type TUI struct {
	// EvidencePanel enables collapsible evidence containers beneath each
	// agent response in the main viewport. When false, compact mode
	// behaviour is unchanged (spinner-only with no inline tool visibility).
	EvidencePanel bool `json:"evidence_panel"`
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
	APIKeyEnv           string   `json:"api_key_env,omitempty"`
	Model               string   `json:"model"`
	ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	Verbosity           string   `json:"verbosity,omitempty"`
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
	model := r.ServerModel
	if strings.TrimSpace(model) == "" {
		model = r.Model
	}

	return LlamaServer{
		Kind:            r.Kind,
		Binary:          r.Binary,
		Host:            r.Host,
		Port:            r.Port,
		Model:           model,
		BaseURL:         r.BaseURL,
		APIKeyEnv:       r.APIKeyEnv,
		ReasoningEffort: r.ReasoningEffort,
		Verbosity:       r.Verbosity,
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
	BackendLlama           = "llama"
	BackendMLX             = "mlx"
	BackendMiniMax         = "minimax"
	BackendOpenAIResponses = "openai_responses"
	BackendCodex           = "codex"
	BackendZAI             = "zai"
	BackendGLM             = "glm"
	BackendDeepSeek        = "deepseek"
	BackendAnthropic       = "anthropic"
)

// LlamaServer describes an inference backend the harness can talk to. The
// historic name is retained because most call sites predate hosted providers.
// Local backends ("llama" and "mlx") can be launched by the harness. Hosted
// backends ("codex"/"openai_responses", "zai", "glm", "minimax",
// "deepseek", and "anthropic") are client-only and use API-key
// authentication from environment variables.
type LlamaServer struct {
	Kind            string   `json:"kind,omitempty"`
	Binary          string   `json:"binary"`
	Host            string   `json:"host"`
	Port            int      `json:"port"`
	Model           string   `json:"model"`
	DisplayName     string   `json:"display_name,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	APIKeyEnv       string   `json:"api_key_env,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	Verbosity       string   `json:"verbosity,omitempty"`
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
	return textutil.EqualTrimFold(l.Kind, BackendMLX)
}

func (l LlamaServer) IsOpenAIResponses() bool {
	switch strings.ToLower(strings.TrimSpace(l.Kind)) {
	case BackendOpenAIResponses, BackendCodex:
		return true
	default:
		return false
	}
}

func (l LlamaServer) IsZAI() bool {
	return textutil.EqualTrimFold(l.Kind, BackendZAI)
}

func (l LlamaServer) IsGLM() bool {
	return textutil.EqualTrimFold(l.Kind, BackendGLM)
}

func (l LlamaServer) IsMiniMax() bool {
	return textutil.EqualTrimFold(l.Kind, BackendMiniMax)
}

func (l LlamaServer) IsDeepSeek() bool {
	return textutil.EqualTrimFold(l.Kind, BackendDeepSeek)
}

func (l LlamaServer) IsAnthropic() bool {
	return textutil.EqualTrimFold(l.Kind, BackendAnthropic)
}

func (l LlamaServer) IsHosted() bool {
	return l.IsOpenAIResponses() || l.IsZAI() || l.IsGLM() || l.IsMiniMax() || l.IsDeepSeek() || l.IsAnthropic()
}

func (l LlamaServer) IsLaunchable() bool {
	return !l.IsHosted()
}

func (l LlamaServer) UsesModelPath() bool {
	return !l.IsMLX() && !l.IsHosted()
}

func (l LlamaServer) EndpointBaseURL() string {
	if baseURL := strings.TrimRight(strings.TrimSpace(l.BaseURL), "/"); baseURL != "" {
		return baseURL
	}

	switch {
	case l.IsOpenAIResponses():
		return "https://api.openai.com"
	case l.IsZAI():
		return "https://api.z.ai/api/paas/v4"
	case l.IsGLM():
		return "https://api.z.ai/api/coding/paas/v4"
	case l.IsMiniMax():
		return "https://api.minimax.io/v1"
	case l.IsDeepSeek():
		return "https://api.deepseek.com"
	case l.IsAnthropic():
		return "https://api.anthropic.com"
	default:
		return fmt.Sprintf("http://%s:%d", l.Host, l.Port)
	}
}

func (l LlamaServer) APIKeyEnvName() string {
	if env := strings.TrimSpace(l.APIKeyEnv); env != "" {
		return env
	}

	switch {
	case l.IsOpenAIResponses():
		return "OPENAI_API_KEY"
	case l.IsZAI():
		return "ZAI_API_KEY"
	case l.IsGLM():
		return "ZAI_API_KEY"
	case l.IsMiniMax():
		return "MINIMAX_API_KEY"
	case l.IsDeepSeek():
		return "DEEPSEEK_API_KEY"
	case l.IsAnthropic():
		return "ANTHROPIC_API_KEY"
	default:
		return ""
	}
}

type Chat struct {
	AutoStartServer         bool     `json:"auto_start_server"`
	SaveDir                 string   `json:"save_dir"`
	DebugLogDir             string   `json:"debug_log_dir"`
	DebugLogMode            string   `json:"debug_log_mode"`
	SystemPrompt            string   `json:"system_prompt"`
	SystemPromptFiles       []string `json:"system_prompt_files"`
	AllowMissingPrompts     bool     `json:"allow_missing_prompts,omitempty"`
	ProfilesDir             string   `json:"profiles_dir"`
	DefaultProfiles         []string `json:"default_profiles"`
	ProjectRoot             string   `json:"project_root"`
	ProjectPromptCandidates []string `json:"project_prompt_candidates,omitempty"`
	ProjectPromptFiles      []string `json:"project_prompt_files"`
	AttachInstructionFiles  bool     `json:"attach_instruction_files"`
	// ModelFamily controls model-specific prompt compatibility overlays.
	// Empty/"auto" infers from the active model id. Use "generic" or "none"
	// to suppress auto overlays when an alias looks misleading.
	ModelFamily            string  `json:"model_family,omitempty"`
	Preset                 string  `json:"preset"`
	Temperature            float64 `json:"temperature"`
	TopP                   float64 `json:"top_p"`
	TopK                   int     `json:"top_k"`
	MinP                   float64 `json:"min_p"`
	PresencePenalty        float64 `json:"presence_penalty"`
	RepetitionPenalty      float64 `json:"repetition_penalty"`
	EnableThinking         *bool   `json:"enable_thinking,omitempty"`
	PreserveThinking       bool    `json:"preserve_thinking"`
	MaxTokens              int     `json:"max_tokens"`
	MaxHistory             int     `json:"max_history"`
	MaxToolSteps           int     `json:"max_tool_steps"`
	MaxContextMessages     int     `json:"max_context_messages"`
	MaxContextChars        int     `json:"max_context_chars"`
	MaxModelCalls          int     `json:"max_model_calls"`
	MaxDurationSeconds     int     `json:"max_duration_seconds"`
	RequestTimeoutSeconds  int     `json:"request_timeout_seconds"`
	ServerStartTimeoutSecs int     `json:"server_start_timeout_seconds"`
	ImplementationDispatch string  `json:"implementation_dispatch,omitempty"`
	// AgentMode selects which built-in agent profile drives the session.
	// OpenCode-shaped: the user picks build/plan/general; the harness
	// never classifies a prompt to pick one on their behalf. Empty falls
	// back to "build" (full capability). See internal/runtime/modepolicy/modes.go.
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
	// SessionID is a runtime-only identifier for the current
	// idiom session. Not persisted to config files (`json:"-"`); set
	// by lane / plan-execute / implement entry points so workspace
	// tools can stamp their artifacts (plan files, conversation
	// logs) with an unambiguous owner. Lets concurrent dispatches in
	// the same workspace avoid stomping on each other when looking
	// up "the latest plan." Empty string = no session attribution
	// (legacy behavior).
	SessionID string `json:"-"`
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
	// the model's input parameters. See internal/capabilities/workspace/external_tool.go
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
	ReadOnly       bool            `json:"read_only,omitempty"`
	Discovery      bool            `json:"discovery,omitempty"`
}

// ToolRule is a glob-style permission rule for a specific tool. Pattern
// semantics are described in internal/capabilities/workspace/tool_rules.go.
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
			Model:           "models/local-model.gguf",
			CtxSize:         262144,
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
			MaxToolSteps:           0,
			MaxContextMessages:     40,
			MaxContextChars:        200000,
			MaxModelCalls:          0,
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
	return LoadWithOptions(path, LoadOptions{})
}

func LoadWithOptions(path string, opts LoadOptions) (Config, error) {
	// Try the specified config file first.
	data, err := os.ReadFile(path)
	if err == nil {
		return loadConfigFromData(data, path, "")
	}
	if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	// Local config missing — try ~/.idiom/config.json.
	if home, homeErr := os.UserHomeDir(); homeErr == nil && shouldUseGlobalConfigFallback(home, opts) {
		globalPath := filepath.Join(home, ".idiom", "config.json")
		data, err = os.ReadFile(globalPath)
		if err == nil {
			return loadConfigFromData(data, globalPath, "global ")
		}
		if !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("read global config: %w", err)
		}
	}

	// No config file found anywhere — return compiled defaults.
	cfg := Default()
	cfg.applyWorkspaceDefaults()
	cfg.canonicalizeWorkspacePaths()

	return cfg, nil
}

func shouldUseGlobalConfigFallback(home string, opts LoadOptions) bool {
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "" {
		return false
	}

	if opts.DisableGlobalConfigFallback ||
		strings.TrimSpace(os.Getenv("IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK")) != "" {
		return false
	}

	if isGoTestProcess() &&
		!opts.AllowProcessHomeGlobalConfigFallback &&
		strings.TrimSpace(os.Getenv("IDIOM_ALLOW_PROCESS_HOME_GLOBAL_CONFIG_FALLBACK")) == "" &&
		home == filepath.Clean(strings.TrimSpace(processHomeForGlobalConfigFallback)) {
		return false
	}

	return true
}

func isGoTestProcess() bool {
	executable := strings.TrimSpace(os.Args[0])
	return strings.HasSuffix(executable, ".test")
}

// loadConfigFromData unmarshals config data, resolves paths relative to
// sourcePath's directory, applies runtime/workspace defaults, and validates.
// label is inserted into error messages (e.g. "global ") — pass "" for local.
func loadConfigFromData(data []byte, sourcePath, label string) (Config, error) {
	cfg := Default()

	if unknown, err := UnknownJSONKeys(data, Config{}); err != nil {
		return Config{}, fmt.Errorf("inspect %sconfig keys: %w", label, err)
	} else if len(unknown) > 0 {
		return Config{}, fmt.Errorf("unknown %sconfig key(s): %s", label, strings.Join(unknown, ", "))
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %sconfig: %w", label, err)
	}

	baseDir := filepath.Dir(sourcePath)
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
	slices.Sort(unknown)

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

		if c.Implementer.IsLaunchable() && c.LlamaServer.IsLaunchable() && c.Implementer.Port == c.LlamaServer.Port && c.Implementer.Host == c.LlamaServer.Host {
			return fmt.Errorf("implementer.port must differ from llama_server.port on the same host")
		}
	}

	if k := strings.TrimSpace(c.RouteClassifier.Kind); k != "" && !isKnownBackendKind(k) {
		return fmt.Errorf("route_classifier.kind must be one of %s (got %q)", backendKindList(), c.RouteClassifier.Kind)
	}

	if c.Chat.SaveDir == "" {
		return fmt.Errorf("chat.save_dir is required")
	}

	if c.Chat.MaxTokens <= 0 {
		return fmt.Errorf("chat.max_tokens must be positive")
	}

	if c.Chat.DebugLogMode != "" && !slices.Contains([]string{"redacted", "full"}, c.Chat.DebugLogMode) {
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

	if trimmed := strings.TrimSpace(c.Chat.ModelFamily); trimmed != "" {
		known := map[string]struct{}{"auto": {}, "qwen": {}, "generic": {}, "none": {}}
		if _, ok := known[strings.ToLower(trimmed)]; !ok {
			return fmt.Errorf("chat.model_family must be one of auto, qwen, generic, none (got %q)", trimmed)
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
	return c.LlamaServer.EndpointBaseURL()
}

// ImplementerBaseURL returns the HTTP URL for the implementer llama-server,
// or an empty string when no implementer is configured. The app uses an
// empty return to mean "no secondary server — child tasks run on the
// primary server like they always have".
func (c Config) ImplementerBaseURL() string {
	if c.Implementer == nil {
		return ""
	}

	return c.Implementer.EndpointBaseURL()
}

func (c *Config) resolvePaths(baseDir string) {
	if c.LlamaServer.UsesModelPath() {
		c.LlamaServer.Model = resolvePath(baseDir, c.LlamaServer.Model)
	}

	c.LlamaServer.LogFile = resolvePath(baseDir, c.LlamaServer.LogFile)
	if c.Implementer != nil {
		if c.Implementer.UsesModelPath() {
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

		if c.Implementer.IsHosted() {
			if c.Implementer.BaseURL == "" && c.LlamaServer.IsHosted() {
				c.Implementer.BaseURL = c.LlamaServer.BaseURL
			}

			if c.Implementer.APIKeyEnv == "" && c.LlamaServer.IsHosted() {
				c.Implementer.APIKeyEnv = c.LlamaServer.APIKeyEnv
			}
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

	slices.Sort(names)

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
	chat.EnableThinking = clonePtr(preset.EnableThinking)
	chat.PreserveThinking = preset.PreserveThinking

	if preset.MaxTokens > 0 {
		chat.MaxTokens = preset.MaxTokens
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func knownBackendKinds() []string {
	return []string{
		BackendLlama,
		BackendMLX,
		BackendMiniMax,
		BackendOpenAIResponses,
		BackendCodex,
		BackendZAI,
		BackendGLM,
		BackendDeepSeek,
		BackendAnthropic,
	}
}

func isKnownBackendKind(kind string) bool {
	return slices.Contains(knownBackendKinds(), strings.ToLower(strings.TrimSpace(kind)))
}

func backendKindList() string {
	return strings.Join(knownBackendKinds(), ", ")
}

// applyBackendRuntimeDefaults fills launch/runtime defaults for local
// backends. Hosted backends are client-only, so they do not get llama.cpp or
// mlx_lm.server launch defaults. If parent is non-nil, llama reasoning fields
// inherit from it when unset (used for the implementer block).
func applyBackendRuntimeDefaults(b *LlamaServer, parent *LlamaServer, defaultLogFile string) {
	if b.IsHosted() {
		return
	}

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
		b.CtxSize = 262144
	}

	if b.Reasoning == "" {
		if parent != nil && parent.Reasoning != "" {
			b.Reasoning = parent.Reasoning
		}
	}

	if b.ReasoningFormat == "" {
		if parent != nil && parent.ReasoningFormat != "" {
			b.ReasoningFormat = parent.ReasoningFormat
		}
	}

	if b.ReasoningBudget == 0 {
		if parent != nil {
			b.ReasoningBudget = parent.ReasoningBudget
		}
	}
}

func validateBackend(name string, b LlamaServer) error {
	kind := strings.TrimSpace(b.Kind)
	if kind != "" && !isKnownBackendKind(kind) {
		return fmt.Errorf("%s.kind must be one of %s (got %q)", name, backendKindList(), b.Kind)
	}

	if b.IsHosted() {
		if b.Model == "" {
			return fmt.Errorf("%s.model is required", name)
		}

		if b.EndpointBaseURL() == "" {
			return fmt.Errorf("%s.base_url is required", name)
		}

		if b.APIKeyEnvName() == "" {
			return fmt.Errorf("%s.api_key_env is required", name)
		}

		return nil
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

		if b.Reasoning != "" && !slices.Contains([]string{"on", "off", "auto"}, b.Reasoning) {
			return fmt.Errorf("%s.reasoning must be one of on, off, auto", name)
		}

		if b.ReasoningFormat != "" && !slices.Contains([]string{"auto", "none", "deepseek", "deepseek-legacy"}, b.ReasoningFormat) {
			return fmt.Errorf("%s.reasoning_format must be one of auto, none, deepseek, deepseek-legacy", name)
		}
	}

	return nil
}
