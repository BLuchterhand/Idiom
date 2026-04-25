package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const loadingModelRetryDelay = 500 * time.Millisecond

// Backend identifiers for Client. Mirrored from config.BackendLlama /
// config.BackendMLX to avoid an import cycle; keep values in sync.
const (
	BackendLlama = "llama"
	BackendMLX   = "mlx"
)

type Client struct {
	baseURL string
	backend string
	http    *http.Client
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
	MaxTokens         int
	Tools             []Tool
	ToolChoice        string
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
	// chat_template_kwargs is a llama.cpp server extension for Jinja-templated
	// chat templates (e.g. Qwen3's enable_thinking / preserve_thinking hooks).
	// mlx_lm.server ignores the field, and other backends may reject unknown
	// request keys outright — so omit it entirely unless we're talking to
	// llama.cpp.
	if backend != "" && backend != BackendLlama {
		return nil
	}
	if opts.EnableThinking == nil && !opts.PreserveThinking {
		return nil
	}
	return &chatTemplateKwargs{
		EnableThinking:   cloneBoolPtr(opts.EnableThinking),
		PreserveThinking: opts.PreserveThinking,
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
	case "", BackendLlama:
		c.backend = BackendLlama
	default:
		c.backend = BackendLlama
	}
	return c
}

// Backend reports the backend kind configured on this client.
func (c *Client) Backend() string {
	if c.backend == "" {
		return BackendLlama
	}
	return c.backend
}

func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
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

func (c *Client) StreamChat(ctx context.Context, messages []ChatMessage, opts ChatOptions, onDelta func(string) error) error {
	if opts.Model == "" {
		opts.Model = "local-model"
	}

	payload := chatRequest{
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

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
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

func (c *Client) CompleteChatMessage(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error) {
	if opts.Model == "" {
		opts.Model = "local-model"
	}

	payload := chatRequest{
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

	data, err := json.Marshal(payload)
	if err != nil {
		return ChatMessage{}, fmt.Errorf("marshal request: %w", err)
	}

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(data))
		if err != nil {
			return ChatMessage{}, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return ChatMessage{}, err
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
