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
	"strings"
	"time"
)

const loadingModelRetryDelay = 500 * time.Millisecond

// Backend identifiers for Client. Mirrored from config backend constants;
// keep values in sync.
const (
	BackendLlama           = "llama"
	BackendMLX             = "mlx"
	BackendMiniMax         = "minimax"
	BackendOpenAIResponses = "openai_responses"
	BackendCodex           = "codex"
	BackendZAI             = "zai"
)

type Client struct {
	baseURL         string
	backend         string
	apiKeyEnv       string
	reasoningEffort string
	verbosity       string
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
	MaxTokens          int
	Tools              []Tool
	ToolChoice         string
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
	case "", BackendLlama:
		// Existing llama.cpp behavior: request-level options are the contract.
	case BackendMLX:
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
		EnableThinking:   cloneBoolPtr(opts.EnableThinking),
		PreserveThinking: opts.PreserveThinking,
		ThinkingBudget:   opts.ThinkingBudget,
	}
}

func normalizeChatRequestForBackend(req *chatRequest, backend string) {
	if req == nil {
		return
	}
	if !BackendSupportsNativeTools(backend) {
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
	case BackendMLX:
		return false
	default:
		return true
	}
}

func isOpenAIResponsesBackend(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case BackendOpenAIResponses, BackendCodex:
		return true
	default:
		return false
	}
}

func isHostedChatBackend(backend string) bool {
	b := strings.ToLower(strings.TrimSpace(backend))
	return b == BackendZAI || b == BackendMiniMax
}

func backendRequiresAuth(backend string) bool {
	return isOpenAIResponsesBackend(backend) || isHostedChatBackend(backend)
}

func defaultAPIKeyEnv(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case BackendOpenAIResponses, BackendCodex:
		return "OPENAI_API_KEY"
	case BackendZAI:
		return "ZAI_API_KEY"
	case BackendMiniMax:
		return "MINIMAX_API_KEY"
	default:
		return ""
	}
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		backend: BackendLlama,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

// WithBackend sets the backend kind this client is talking to. Defaults to
// llama; call with BackendMLX when the endpoint is mlx_lm.server (or another
// OpenAI-compatible server that doesn't understand llama.cpp extensions like
// chat_template_kwargs). Returns the same client for chaining at call sites.
func (c *Client) WithBackend(backend string) *Client {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case BackendMLX:
		c.backend = BackendMLX
	case BackendMiniMax:
		c.backend = BackendMiniMax
	case BackendOpenAIResponses:
		c.backend = BackendOpenAIResponses
	case BackendCodex:
		c.backend = BackendCodex
	case BackendZAI:
		c.backend = BackendZAI
	case "", BackendLlama:
		c.backend = BackendLlama
	default:
		c.backend = BackendLlama
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

// Backend reports the backend kind configured on this client.
func (c *Client) Backend() string {
	if c.backend == "" {
		return BackendLlama
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
	if c.Backend() == BackendZAI {
		return c.baseURL + "/models"
	}
	return c.baseURL + "/v1/models"
}

func (c *Client) chatCompletionsURL() string {
	if c.Backend() == BackendZAI {
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
		return fmt.Errorf("%s is required for %s backend", env, c.Backend())
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
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, fmt.Errorf("list models failed: %s", strings.TrimSpace(string(body)))
	}

	var parsed modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	return parsed.Data, nil
}

func hostedChatRequestPayload(messages []ChatMessage, opts ChatOptions, stream bool, backend string) map[string]any {
	payload := map[string]any{
		"model":    opts.Model,
		"messages": hostedChatMessages(messages),
		"stream":   stream,
	}
	if opts.MaxTokens > 0 {
		payload["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature > 0 {
		payload["temperature"] = opts.Temperature
	}
	if opts.TopP > 0 {
		payload["top_p"] = opts.TopP
	}
	if opts.PresencePenalty != 0 {
		payload["presence_penalty"] = opts.PresencePenalty
	}
	if len(opts.Tools) > 0 && BackendSupportsNativeTools(backend) {
		payload["tools"] = opts.Tools
	}
	if opts.ToolChoice != "" && BackendSupportsNativeTools(backend) {
		payload["tool_choice"] = opts.ToolChoice
	}
	if strings.EqualFold(strings.TrimSpace(backend), BackendZAI) && opts.EnableThinking != nil {
		mode := "disabled"
		if *opts.EnableThinking {
			mode = "enabled"
		}
		payload["thinking"] = map[string]string{"type": mode}
	}
	return payload
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
		opts.Model = "local-model"
	}
	if isOpenAIResponsesBackend(c.Backend()) {
		return fmt.Errorf("streaming is not implemented for %s backend", c.Backend())
	}

	var payload any
	if isHostedChatBackend(c.Backend()) {
		payload = hostedChatRequestPayload(messages, opts, true, c.Backend())
	} else {
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
		normalizeChatRequestForBackend(&reqPayload, c.backend)
		payload = reqPayload
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
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
		return fmt.Errorf("chat request transport failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return fmt.Errorf("chat request failed: %s", strings.TrimSpace(string(body)))
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
			return fmt.Errorf("decode stream chunk: %w", err)
		}

		if parsed.Error != nil {
			return fmt.Errorf("%s", parsed.Error.Message)
		}
		if len(parsed.Choices) == 0 {
			continue
		}

		delta := parsed.Choices[0].Delta.Content
		if delta == "" {
			delta = parsed.Choices[0].Message.Content
		}

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
		return fmt.Errorf("read stream: %w", err)
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
		return ChatMessage{}, fmt.Errorf("marshal responses request: %w", err)
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
		return ChatMessage{}, fmt.Errorf("responses request transport failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return ChatMessage{}, fmt.Errorf("responses request failed: %s", strings.TrimSpace(string(body)))
	}

	var parsed responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatMessage{}, fmt.Errorf("decode responses response: %w", err)
	}
	if parsed.Error != nil {
		return ChatMessage{}, fmt.Errorf("%s", parsed.Error.Message)
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
		Content:   content.String(),
		ToolCalls: toolCalls,
		Usage: Usage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
		FinishReason: finishReason,
	}, nil
}

func (c *Client) CompleteChatMessage(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error) {
	if opts.Model == "" {
		opts.Model = "local-model"
	}
	if isOpenAIResponsesBackend(c.Backend()) {
		return c.completeResponsesChatMessage(ctx, messages, opts)
	}

	var payload any
	if isHostedChatBackend(c.Backend()) {
		payload = hostedChatRequestPayload(messages, opts, false, c.Backend())
	} else {
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
		normalizeChatRequestForBackend(&reqPayload, c.backend)
		payload = reqPayload
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return ChatMessage{}, fmt.Errorf("marshal request: %w", err)
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
			return ChatMessage{}, fmt.Errorf("chat request transport failed: %w", err)
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
			return ChatMessage{}, fmt.Errorf("chat request failed: %s", bodyText)
		}

		var parsed chatResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		_ = resp.Body.Close()
		if err != nil {
			return ChatMessage{}, fmt.Errorf("decode chat response: %w", err)
		}
		if parsed.Error != nil {
			if shouldRetryLoadingModel(http.StatusServiceUnavailable, parsed.Error.Message) {
				if err := sleepWithContext(ctx, loadingModelRetryDelay); err != nil {
					return ChatMessage{}, err
				}
				continue
			}
			return ChatMessage{}, fmt.Errorf("%s", parsed.Error.Message)
		}
		if len(parsed.Choices) == 0 {
			return ChatMessage{}, fmt.Errorf("chat response did not contain any choices")
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
			Content:          message.Content,
			ReasoningContent: reasoning,
			ToolCalls:        message.ToolCalls,
			Usage:            parsed.Usage,
			FinishReason:     strings.TrimSpace(choice.FinishReason),
		}, nil
	}
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
}

func shouldRetryLoadingModel(statusCode int, body string) bool {
	if statusCode != http.StatusServiceUnavailable {
		return false
	}
	body = strings.ToLower(body)
	return strings.Contains(body, "loading model") || strings.Contains(body, "unavailable_error")
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
