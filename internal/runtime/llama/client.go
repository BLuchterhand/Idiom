package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/platform/util"
)

const loadingModelRetryDelay = 500 * time.Millisecond

type Client struct {
	baseURL         string
	backend         string
	modelName       string
	apiKeyEnv       string
	reasoningEffort string
	verbosity       string
	toolNameReverse map[string]string
	http            *http.Client
}

type ChatMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
	Usage            Usage      `json:"-"`
	// FinishReason carries the OpenAI-compatible `finish_reason` from the
	// upstream backend (llama.cpp and mlx_lm.server both populate it).
	// Common values: "stop" (model emitted EOS / stop-sequence),
	// "length" (hit max_tokens), "tool_calls" (response is a tool call),
	// "content_filter" (filtered). Empty when the backend omits it.
	// Diagnostic only — callers may log it to distinguish a self-stop from
	// a budget cutoff. Not serialized in chat history.
	FinishReason string `json:"-"`
}

// ToolCall mirrors OpenAI function-calling `message.tool_calls[i]`.
// Arguments is the raw JSON string the model emitted; callers decide how
// to unmarshal it into a typed tool request.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool defines a callable function in the OpenAI function-calling shape.
// ParametersJSON is the raw JSON schema string; it's passed through as-is.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ChatOptions struct {
	Model             string
	Temperature       float64
	TopP              float64
	TopK              int
	MinP              float64
	PresencePenalty   float64
	RepetitionPenalty float64
	EnableThinking    *bool
	PreserveThinking  bool
	// ThinkingBudget caps tokens spent inside <think>...</think>.
	// 0 = uncapped. See chatTemplateKwargs.ThinkingBudget for why.
	ThinkingBudget int
	// ChatTemplateKwargs forces request-level template kwargs for backends
	// where server-start defaults normally own those knobs. llama.cpp always
	// receives kwargs when present; MLX receives them only when this is true.
	ChatTemplateKwargs bool
	ModelThinkingFloor bool
	MaxTokens          int
	Tools              []Tool
	ToolChoice         string
	// ForceNativeTools lets a caller opt into the OpenAI tools array on
	// backends that are normally kept in Idiom textual-tool mode. Use only
	// for tightly scoped single-tool terminal turns; broad MLX tool surfaces
	// remain textual because its parser has historically failed closed.
	ForceNativeTools bool
	// ResponseFormatJSON asks OpenAI-compatible chat-completions backends to
	// constrain visible content to one JSON object. Use for textual-tool
	// terminal turns where Idiom owns parsing and native tool transport is not
	// reliable.
	ResponseFormatJSON bool
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type ModelInfo struct {
	ID string `json:"id"`
}

type modelsResponse struct {
	Data []ModelInfo `json:"data"`
}

type chatRequest struct {
	Model              string              `json:"model"`
	Messages           []ChatMessage       `json:"messages"`
	Stream             bool                `json:"stream"`
	Temperature        float64             `json:"temperature"`
	TopP               float64             `json:"top_p"`
	TopK               int                 `json:"top_k,omitempty"`
	MinP               float64             `json:"min_p,omitempty"`
	PresencePenalty    float64             `json:"presence_penalty,omitempty"`
	RepeatPenalty      float64             `json:"repeat_penalty,omitempty"`
	RepetitionPenalty  float64             `json:"repetition_penalty,omitempty"`
	ChatTemplateKwargs *chatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
	MaxTokens          int                 `json:"max_tokens"`
	Tools              []Tool              `json:"tools,omitempty"`
	ToolChoice         string              `json:"tool_choice,omitempty"`
	ResponseFormat     any                 `json:"response_format,omitempty"`
}

type chatTemplateKwargs struct {
	EnableThinking   *bool `json:"enable_thinking,omitempty"`
	PreserveThinking bool  `json:"preserve_thinking,omitempty"`
	// ThinkingBudget caps how many tokens the model spends inside the
	// <think>...</think> block before being forced to emit its actual
	// response. Without this cap, Qwen3.6-35B-A3B has a documented
	// endless-reasoning-loop wedge mode (HF discussion #20) where it
	// keeps thinking until context runs out, producing zero tool calls.
	// 0 = no cap (legacy behavior). Recommended: 4096-8192 for
	// agentic harnesses where forward progress matters more than
	// reasoning depth on hard problems.
	//
	// MLX caveat: this is forwarded as a chat_template_kwarg to the
	// Qwen3 template. Whether MLX's bundled template version actually
	// honors `thinking_budget` (vs silently ignoring it) is unverified
	// — depends on the template baked into the model directory's
	// tokenizer_config.json. If template doesn't honor it, this is a
	// no-op (no harm, no benefit). vLLM's `--reasoning-budget` server
	// flag is a different mechanism we don't have on MLX.
	ThinkingBudget int `json:"thinking_budget,omitempty"`
}

// chatResponse captures both llama.cpp's `reasoning_content` field and MLX's
// `reasoning` alias. Upstream code reads the unified Message.ReasoningContent;
// decoding resolves MLX's shorthand into the same slot via the parsed helper.
//
// FinishReason is captured per OpenAI's chat-completion spec. Both llama.cpp
// and mlx_lm.server emit it ("stop" / "length" / "tool_calls" /
// "content_filter"). Empty when omitted. Surfaced through ChatMessage so
// callers can log self-stop vs budget cutoff.
type chatResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Choices []struct {
		Message struct {
			Role             string     `json:"role"`
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content,omitempty"`
			Reasoning        string     `json:"reasoning,omitempty"`
			ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage Usage `json:"usage,omitempty"`
}

type chatStreamChunk struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
		} `json:"delta"`
		Message struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func buildChatTemplateKwargs(opts ChatOptions, backend string) *chatTemplateKwargs {
	// chat_template_kwargs is supported by llama.cpp and current mlx_lm.server
	// for Jinja-templated chat templates (for example Qwen3's enable_thinking
	// and preserve_thinking hooks). Other OpenAI-compatible backends may reject
	// unknown request keys, so keep the field scoped to verified backends.
	switch backend {
	case "", config.BackendLlama:
		// Existing llama.cpp behavior: request-level options are the contract.
	case config.BackendMLX:
		// MLX deployments commonly encode the model's default thinking mode in
		// server-start --chat-template-args. Only send per-request kwargs when
		// a caller intentionally overrides those defaults; otherwise a generic
		// config preset can accidentally flip a dedicated "nothink" server back
		// into thinking mode.
		if !opts.ChatTemplateKwargs {
			return nil
		}
	default:
		return nil
	}
	if opts.EnableThinking == nil && !opts.PreserveThinking && opts.ThinkingBudget <= 0 {
		return nil
	}
	return &chatTemplateKwargs{
		EnableThinking:   util.ClonePtr(opts.EnableThinking),
		PreserveThinking: opts.PreserveThinking,
		ThinkingBudget:   opts.ThinkingBudget,
	}
}

func normalizeChatRequestForBackend(req *chatRequest, opts ChatOptions, backend string) {
	if req == nil {
		return
	}
	if !BackendSupportsNativeTools(backend) && !opts.ForceNativeTools {
		// mlx_lm.server owns its own Qwen XML tool parser when a native
		// OpenAI `tools` array is present. Current MLX parser failures can
		// close the HTTP connection before idiom receives a structured model
		// response, so keep tool-call parsing in idiom's JSON protocol for
		// MLX and let the backend generate plain assistant content.
		req.Tools = nil
		req.ToolChoice = ""
	}
}

func BackendSupportsNativeTools(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case config.BackendMLX:
		return false
	default:
		return true
	}
}

// StructuredOutputCapabilities is an explicit capability report for the
// backend features Idiom can rely on for contract-shaped outputs. It is
// intentionally conservative: unsupported strict schema or grammar fields stay
// false until a backend adapter enforces them.
type StructuredOutputCapabilities struct {
	NativeTools             bool `json:"native_tools"`
	JSONObject              bool `json:"json_object"`
	StrictJSONSchema        bool `json:"strict_json_schema"`
	Grammar                 bool `json:"grammar"`
	ThinkingControls        bool `json:"thinking_controls"`
	RequestTimeoutTelemetry bool `json:"request_timeout_telemetry"`
}

func BackendStructuredOutputCapabilities(backend string) StructuredOutputCapabilities {
	kind := strings.ToLower(strings.TrimSpace(backend))
	caps := StructuredOutputCapabilities{
		NativeTools: BackendSupportsNativeTools(kind),
		JSONObject:  true,
	}
	switch kind {
	case "", config.BackendLlama:
		caps.ThinkingControls = true
	case config.BackendMLX:
		caps.ThinkingControls = true
	case config.BackendZAI:
		caps.ThinkingControls = true
	}
	return caps
}

func isOpenAIResponsesBackend(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case config.BackendOpenAIResponses, config.BackendCodex:
		return true
	default:
		return false
	}
}

func isHostedChatBackend(backend string) bool {
	b := strings.ToLower(strings.TrimSpace(backend))
	return b == config.BackendZAI || b == config.BackendMiniMax || b == config.BackendDeepSeek
}

func backendRequiresAuth(backend string) bool {
	return isOpenAIResponsesBackend(backend) || isHostedChatBackend(backend)
}

func defaultAPIKeyEnv(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case config.BackendOpenAIResponses, config.BackendCodex:
		return "OPENAI_API_KEY"
	case config.BackendZAI:
		return "ZAI_API_KEY"
	case config.BackendMiniMax:
		return "MINIMAX_API_KEY"
	case config.BackendDeepSeek:
		return "DEEPSEEK_API_KEY"
	default:
		return ""
	}
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		backend: config.BackendLlama,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

// WithBackend sets the backend kind this client is talking to. Defaults to
// llama; call with config.BackendMLX when the endpoint is mlx_lm.server (or another
// OpenAI-compatible server that doesn't understand llama.cpp extensions like
// chat_template_kwargs). Returns the same client for chaining at call sites.
func (c *Client) WithBackend(backend string) *Client {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case config.BackendMLX:
		c.backend = config.BackendMLX
	case config.BackendMiniMax:
		c.backend = config.BackendMiniMax
	case config.BackendOpenAIResponses:
		c.backend = config.BackendOpenAIResponses
	case config.BackendCodex:
		c.backend = config.BackendCodex
	case config.BackendZAI:
		c.backend = config.BackendZAI
	case config.BackendDeepSeek:
		c.backend = config.BackendDeepSeek
	case "", config.BackendLlama:
		c.backend = config.BackendLlama
	default:
		c.backend = config.BackendLlama
	}
	return c
}

func (c *Client) WithAPIKeyEnv(env string) *Client {
	c.apiKeyEnv = strings.TrimSpace(env)
	return c
}

func (c *Client) WithReasoningEffort(effort string) *Client {
	c.reasoningEffort = strings.TrimSpace(effort)
	return c
}

func (c *Client) WithVerbosity(verbosity string) *Client {
	c.verbosity = strings.TrimSpace(verbosity)
	return c
}

func (c *Client) WithModel(model string) *Client {
	c.modelName = strings.TrimSpace(model)
	return c
}

// Backend reports the backend kind configured on this client.
func (c *Client) Backend() string {
	if c.backend == "" {
		return config.BackendLlama
	}
	return c.backend
}

func (c *Client) SupportsNativeTools() bool {
	if c == nil {
		return true
	}
	return BackendSupportsNativeTools(c.Backend())
}

func (c *Client) modelsURL() string {
	if c.Backend() == config.BackendZAI {
		return c.baseURL + "/models"
	}
	return c.baseURL + "/v1/models"
}

func (c *Client) chatCompletionsURL() string {
	if c.Backend() == config.BackendZAI {
		return c.baseURL + "/chat/completions"
	}
	return c.baseURL + "/v1/chat/completions"
}

func (c *Client) responsesURL() string {
	return c.baseURL + "/v1/responses"
}

func (c *Client) applyAuth(req *http.Request) error {
	if c == nil || req == nil || !backendRequiresAuth(c.Backend()) {
		return nil
	}
	env := strings.TrimSpace(c.apiKeyEnv)
	if env == "" {
		env = defaultAPIKeyEnv(c.Backend())
	}
	key := strings.TrimSpace(os.Getenv(env))
	if key == "" {
		return llmAuthMissingError(env, c.Backend())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	return nil
}

func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.modelsURL(), nil)
	if err != nil {
		return nil, err
	}
	if err := c.applyAuth(req); err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, llmTransportError("list models transport failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, llmHTTPStatusError("list models failed", resp.StatusCode, string(body))
	}

	var parsed modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, llmDecodeError("decode model list", err)
	}
	return parsed.Data, nil
}

type hostedChatPayload struct {
	Model           string            `json:"model"`
	Messages        []ChatMessage     `json:"messages"`
	Stream          bool              `json:"stream"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Temperature     float64           `json:"temperature,omitempty"`
	TopP            float64           `json:"top_p,omitempty"`
	PresencePenalty float64           `json:"presence_penalty,omitempty"`
	Tools           any               `json:"tools,omitempty"`
	ToolNameReverse map[string]string `json:"-"`
	ToolChoice      string            `json:"tool_choice,omitempty"`
	Thinking        any               `json:"thinking,omitempty"`
	ResponseFormat  any               `json:"response_format,omitempty"`
}

// normalizeHostedTools maps tool function names that contain characters
// unsupported by some hosted APIs (notably DeepSeek requires
// ^[a-zA-Z0-9_-]+$). Returns sanitized tools and a reverse map for
// mapping tool-call responses back to original names.
func normalizeHostedTools(tools []Tool) ([]Tool, map[string]string) {
	if len(tools) == 0 {
		return tools, nil
	}
	reverse := make(map[string]string)
	normalized := make([]Tool, len(tools))
	for i, t := range tools {
		orig := t.Function.Name
		sanitized := strings.NewReplacer(":", "_", "/", "_").Replace(orig)
		if sanitized != orig {
			reverse[sanitized] = orig
		}
		normalized[i] = Tool{
			Type: t.Type,
			Function: ToolFunction{
				Name:        sanitized,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		}
	}
	return normalized, reverse
}

func hostedChatRequestPayload(messages []ChatMessage, opts ChatOptions, stream bool, backend string) any {
	payload := hostedChatPayload{
		Model:    opts.Model,
		Messages: hostedChatMessages(repairMessageToolPairing(messages)),
		Stream:   stream,
	}
	if opts.MaxTokens > 0 {
		payload.MaxTokens = opts.MaxTokens
	}
	if opts.Temperature > 0 {
		payload.Temperature = opts.Temperature
	}
	if opts.TopP > 0 {
		payload.TopP = opts.TopP
	}
	if opts.PresencePenalty != 0 {
		payload.PresencePenalty = opts.PresencePenalty
	}
	if len(opts.Tools) > 0 && BackendSupportsNativeTools(backend) {
		normalized, rev := normalizeHostedTools(opts.Tools)
		payload.Tools = normalized
		payload.ToolNameReverse = rev
	}
	if opts.ToolChoice != "" && BackendSupportsNativeTools(backend) {
		payload.ToolChoice = opts.ToolChoice
	}
	if util.EqualTrimFold(backend, config.BackendZAI) && opts.EnableThinking != nil {
		mode := "disabled"
		if *opts.EnableThinking {
			mode = "enabled"
		}
		payload.Thinking = map[string]string{"type": mode}
	}
	if opts.ResponseFormatJSON {
		payload.ResponseFormat = map[string]string{"type": "json_object"}
	}
	return payload
}

// repairMessageToolPairing ensures every assistant message with tool_calls
// has matching role:"tool" responses. Hosted APIs (DeepSeek) reject
// requests where tool_calls entries are unpaired. This guard injects
// synthetic tool error messages for any orphaned tool_calls.
func repairMessageToolPairing(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return messages
	}
	// Build a lookup: tool_call_id -> tool response message index
	toolRespIdx := make(map[string]int)
	for i, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			toolRespIdx[msg.ToolCallID] = i
		}
	}
	// Build the repaired array: for each assistant with tool_calls,
	// emit the assistant, then immediately emit all matching tool
	// responses, then emit other messages normally (skipping tool
	// responses that were already emitted).
	emittedTool := make(map[int]bool)
	repaired := make([]ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			if msg.Role == "tool" {
				continue // tool responses are emitted after their assistant
			}
			repaired = append(repaired, msg)
			continue
		}
		// Emit the assistant message.
		repaired = append(repaired, msg)
		// Emit all matching tool responses immediately after.
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if ti, ok := toolRespIdx[tc.ID]; ok && !emittedTool[ti] {
				repaired = append(repaired, messages[ti])
				emittedTool[ti] = true
			} else if !ok {
				// Orphaned tool_call with no matching tool response.
				// Strict OpenAI-protocol backends (DeepSeek) require a
				// role:"tool" message after every tool_calls entry.
				// Inject a synthetic error so the request is protocol-
				// compliant. Happens when a gate halts execution before
				// the tool runs (budget gate, completion gate, etc.).
				repaired = append(repaired, ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    fmt.Sprintf("tool %q execution skipped: session halted before tool ran", tc.Function.Name),
				})
			}
		}
	}
	return repaired
}

func hostedChatMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ChatMessage, len(messages))
	for i, msg := range messages {
		msg.ReasoningContent = ""
		out[i] = msg
	}
	return out
}

func (c *Client) StreamChat(ctx context.Context, messages []ChatMessage, opts ChatOptions, onDelta func(string) error) error {
	if opts.Model == "" {
		if isHostedChatBackend(c.Backend()) || isOpenAIResponsesBackend(c.Backend()) {
			opts.Model = c.modelName
		}
		if opts.Model == "" {
			opts.Model = "local-model"
		}
	}
	if isOpenAIResponsesBackend(c.Backend()) {
		return llmUnsupportedError(fmt.Sprintf("streaming is not implemented for %s backend", c.Backend()))
	}

	var payload any
	if isHostedChatBackend(c.Backend()) {
		payload = hostedChatRequestPayload(messages, opts, true, c.Backend())
		if hp, ok := payload.(hostedChatPayload); ok {
			c.toolNameReverse = hp.ToolNameReverse
		}
	} else {
		messages = repairMessageToolPairing(messages)
		reqPayload := chatRequest{
			Model:              opts.Model,
			Messages:           messages,
			Stream:             true,
			Temperature:        opts.Temperature,
			TopP:               opts.TopP,
			TopK:               opts.TopK,
			MinP:               opts.MinP,
			PresencePenalty:    opts.PresencePenalty,
			RepeatPenalty:      opts.RepetitionPenalty,
			RepetitionPenalty:  opts.RepetitionPenalty,
			ChatTemplateKwargs: buildChatTemplateKwargs(opts, c.backend),
			MaxTokens:          opts.MaxTokens,
			Tools:              opts.Tools,
			ToolChoice:         opts.ToolChoice,
		}
		if opts.ResponseFormatJSON {
			reqPayload.ResponseFormat = map[string]string{"type": "json_object"}
		}
		normalizeChatRequestForBackend(&reqPayload, opts, c.backend)
		payload = reqPayload
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return llmDecodeError("marshal request", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatCompletionsURL(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.applyAuth(req); err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return llmTransportError("chat request transport failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return llmHTTPStatusError("chat request failed", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		chunk := strings.TrimPrefix(line, "data: ")
		if chunk == "[DONE]" {
			return nil
		}

		var parsed chatStreamChunk
		if err := json.Unmarshal([]byte(chunk), &parsed); err != nil {
			return llmDecodeError("decode stream chunk", err)
		}

		if parsed.Error != nil {
			return llmAPIError(parsed.Error.Message)
		}
		if len(parsed.Choices) == 0 {
			continue
		}

		delta := parsed.Choices[0].Delta.Content
		if delta == "" {
			delta = parsed.Choices[0].Message.Content
		}
		delta = stripSpecialTokens(delta)

		if delta != "" {
			if err := onDelta(delta); err != nil {
				return err
			}
		}

		if parsed.Choices[0].FinishReason != nil {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return llmTransportError("read stream", err)
	}
	return nil
}

func (c *Client) CompleteChat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error) {
	reply, err := c.CompleteChatMessage(ctx, messages, opts)
	if err != nil {
		return "", err
	}
	return reply.Content, nil
}

type responsesRequest struct {
	Model           string              `json:"model"`
	Input           []map[string]any    `json:"input"`
	Store           bool                `json:"store"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	ToolChoice      string              `json:"tool_choice,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	TopP            *float64            `json:"top_p,omitempty"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
	Text            *responsesText      `json:"text,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesText struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type responsesResponse struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
		Code    string `json:"code,omitempty"`
	} `json:"error,omitempty"`
	Status            string `json:"status,omitempty"`
	OutputText        string `json:"output_text,omitempty"`
	IncompleteDetails *struct {
		Reason string `json:"reason,omitempty"`
	} `json:"incomplete_details,omitempty"`
	Output []struct {
		Type      string `json:"type"`
		ID        string `json:"id,omitempty"`
		CallID    string `json:"call_id,omitempty"`
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
		Role      string `json:"role,omitempty"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content,omitempty"`
	} `json:"output,omitempty"`
	Usage struct {
		InputTokens  int `json:"input_tokens,omitempty"`
		OutputTokens int `json:"output_tokens,omitempty"`
		TotalTokens  int `json:"total_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

func responsesInputFromMessages(messages []ChatMessage) []map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "tool":
			item := map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			}
			input = append(input, item)
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" || len(msg.ToolCalls) == 0 {
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": msg.Content,
				})
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = "call_" + call.Function.Name
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      call.Function.Name,
					"arguments": call.Function.Arguments,
				})
			}
		default:
			if role == "" {
				role = "user"
			}
			input = append(input, map[string]any{
				"role":    role,
				"content": msg.Content,
			})
		}
	}
	return input
}

func responsesToolsFromChatTools(tools []Tool) []responsesTool {
	out := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		out = append(out, responsesTool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return out
}

func (c *Client) completeResponsesChatMessage(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error) {
	temp := opts.Temperature
	topP := opts.TopP
	payload := responsesRequest{
		Model:           opts.Model,
		Input:           responsesInputFromMessages(messages),
		Store:           false,
		Tools:           responsesToolsFromChatTools(opts.Tools),
		ToolChoice:      opts.ToolChoice,
		MaxOutputTokens: opts.MaxTokens,
	}
	if opts.Temperature > 0 {
		payload.Temperature = &temp
	}
	if opts.TopP > 0 {
		payload.TopP = &topP
	}
	if c.reasoningEffort != "" {
		payload.Reasoning = &responsesReasoning{Effort: c.reasoningEffort}
	}
	if c.verbosity != "" {
		payload.Text = &responsesText{Verbosity: c.verbosity}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return ChatMessage{}, llmDecodeError("marshal responses request", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.responsesURL(), bytes.NewReader(data))
	if err != nil {
		return ChatMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.applyAuth(req); err != nil {
		return ChatMessage{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ChatMessage{}, llmTransportError("responses request transport failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return ChatMessage{}, llmHTTPStatusError("responses request failed", resp.StatusCode, string(body))
	}

	var parsed responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatMessage{}, llmDecodeError("decode responses response", err)
	}
	if parsed.Error != nil {
		return ChatMessage{}, llmAPIError(parsed.Error.Message)
	}

	var content strings.Builder
	var toolCalls []ToolCall
	role := "assistant"
	for _, item := range parsed.Output {
		switch item.Type {
		case "message":
			if item.Role != "" {
				role = item.Role
			}
			for _, part := range item.Content {
				if part.Text != "" {
					content.WriteString(part.Text)
				}
			}
		case "function_call":
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				callID = item.ID
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   callID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}
	if content.Len() == 0 && parsed.OutputText != "" {
		content.WriteString(parsed.OutputText)
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if parsed.IncompleteDetails != nil && strings.TrimSpace(parsed.IncompleteDetails.Reason) != "" {
		finishReason = strings.TrimSpace(parsed.IncompleteDetails.Reason)
	}
	return ChatMessage{
		Role:      role,
		Content:   stripSpecialTokens(content.String()),
		ToolCalls: remapToolCalls(toolCalls, c.toolNameReverse),
		Usage: Usage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
		FinishReason: finishReason,
	}, nil
}

// remapToolCalls applies a reverse name mapping to tool calls returned
// by a hosted API, undoing the sanitization applied in normalizeHostedTools.
func remapToolCalls(calls []ToolCall, reverse map[string]string) []ToolCall {
	if len(reverse) == 0 || len(calls) == 0 {
		return calls
	}
	out := make([]ToolCall, len(calls))
	for i, call := range calls {
		out[i] = call
		if orig, ok := reverse[call.Function.Name]; ok {
			out[i].Function.Name = orig
		}
	}
	return out
}

func (c *Client) CompleteChatMessage(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error) {
	if opts.Model == "" {
		if isHostedChatBackend(c.Backend()) || isOpenAIResponsesBackend(c.Backend()) {
			opts.Model = c.modelName
		}
		if opts.Model == "" {
			opts.Model = "local-model"
		}
	}
	if isOpenAIResponsesBackend(c.Backend()) {
		return c.completeResponsesChatMessage(ctx, messages, opts)
	}

	var payload any
	if isHostedChatBackend(c.Backend()) {
		payload = hostedChatRequestPayload(messages, opts, false, c.Backend())
		if hp, ok := payload.(hostedChatPayload); ok {
			c.toolNameReverse = hp.ToolNameReverse
		}
	} else {
		messages = repairMessageToolPairing(messages)
		reqPayload := chatRequest{
			Model:              opts.Model,
			Messages:           messages,
			Stream:             false,
			Temperature:        opts.Temperature,
			TopP:               opts.TopP,
			TopK:               opts.TopK,
			MinP:               opts.MinP,
			PresencePenalty:    opts.PresencePenalty,
			RepeatPenalty:      opts.RepetitionPenalty,
			RepetitionPenalty:  opts.RepetitionPenalty,
			ChatTemplateKwargs: buildChatTemplateKwargs(opts, c.backend),
			MaxTokens:          opts.MaxTokens,
			Tools:              opts.Tools,
			ToolChoice:         opts.ToolChoice,
		}
		if opts.ResponseFormatJSON {
			reqPayload.ResponseFormat = map[string]string{"type": "json_object"}
		}
		normalizeChatRequestForBackend(&reqPayload, opts, c.backend)
		payload = reqPayload
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return ChatMessage{}, llmDecodeError("marshal request", err)
	}

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatCompletionsURL(), bytes.NewReader(data))
		if err != nil {
			return ChatMessage{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		if err := c.applyAuth(req); err != nil {
			return ChatMessage{}, err
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return ChatMessage{}, llmTransportError("chat request transport failed", err)
		}

		if resp.StatusCode >= http.StatusBadRequest {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
			_ = resp.Body.Close()
			bodyText := strings.TrimSpace(string(body))
			if shouldRetryLoadingModel(resp.StatusCode, bodyText) {
				if err := sleepWithContext(ctx, loadingModelRetryDelay); err != nil {
					return ChatMessage{}, err
				}
				continue
			}
			return ChatMessage{}, llmHTTPStatusError("chat request failed", resp.StatusCode, bodyText)
		}

		var parsed chatResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		_ = resp.Body.Close()
		if err != nil {
			return ChatMessage{}, llmDecodeError("decode chat response", err)
		}
		if parsed.Error != nil {
			if shouldRetryLoadingModel(http.StatusServiceUnavailable, parsed.Error.Message) {
				if err := sleepWithContext(ctx, loadingModelRetryDelay); err != nil {
					return ChatMessage{}, err
				}
				continue
			}
			return ChatMessage{}, llmAPIError(parsed.Error.Message)
		}
		if len(parsed.Choices) == 0 {
			return ChatMessage{}, llmMalformedResponseError("chat response did not contain any choices")
		}

		choice := parsed.Choices[0]
		message := choice.Message
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "assistant"
		}
		reasoning := message.ReasoningContent
		if reasoning == "" {
			reasoning = message.Reasoning
		}
		return ChatMessage{
			Role:             role,
			Content:          stripSpecialTokens(message.Content),
			ReasoningContent: reasoning,
			ToolCalls:        remapToolCalls(message.ToolCalls, c.toolNameReverse),
			Usage:            parsed.Usage,
			FinishReason:     strings.TrimSpace(choice.FinishReason),
		}, nil
	}
}

func shouldRetryLoadingModel(statusCode int, body string) bool {
	if statusCode != http.StatusServiceUnavailable {
		return false
	}
	body = strings.ToLower(body)
	return strings.Contains(body, "loading model") || strings.Contains(body, "unavailable_error")
}

var specialTokenPattern = regexp.MustCompile(`<\|(?:mask_start|mask_end|mask_?start|mask_?end|endoftext|im_start|im_end|fim_prefix|fim_middle|fim_suffix|repo_name)[^>]*>`)

func stripSpecialTokens(s string) string {
	return specialTokenPattern.ReplaceAllString(s, "")
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
