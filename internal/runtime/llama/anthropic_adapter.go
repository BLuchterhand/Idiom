package llama

import (
	"encoding/json"
	"strings"

	"idiom/internal/platform/config"
)

type anthropicAdapter struct{}

func (a anthropicAdapter) Kind() string { return config.BackendAnthropic }

func (a anthropicAdapter) Protocol() backendProtocol { return backendProtocolMessages }

func (a anthropicAdapter) ModelsPath() string { return "/v1/models" }

func (a anthropicAdapter) MessagesPath() string { return "/v1/messages" }

func (a anthropicAdapter) RequiresAuth() bool { return true }

func (a anthropicAdapter) DefaultAPIKeyEnv() string { return "ANTHROPIC_API_KEY" }

func (a anthropicAdapter) AuthHeaders(apiKey string) map[string]string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}

	return map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": anthropicVersion,
	}
}

func (a anthropicAdapter) StructuredOutputCapabilities() StructuredOutputCapabilities {
	return StructuredOutputCapabilities{
		NativeTools: true,
	}
}

func (a anthropicAdapter) MessagesPayload(messages []ChatMessage, opts ChatOptions, stream bool, _ backendRequestSettings) backendChatPayload {
	tools, reverse := anthropicToolsFromChatTools(opts.Tools)
	system, anthropicMessages := anthropicMessagesFromChat(messages)

	temp := opts.Temperature
	topP := opts.TopP

	payload := anthropicMessagesRequest{
		Model:     opts.Model,
		MaxTokens: opts.MaxTokens,
		Messages:  anthropicMessages,
		System:    system,
		Stream:    stream,
		Tools:     tools,
	}

	if opts.Temperature > 0 {
		payload.Temperature = &temp
	}

	if opts.TopP > 0 {
		payload.TopP = &topP
	}

	if opts.TopK > 0 {
		payload.TopK = opts.TopK
	}

	if choice := anthropicToolChoice(opts.ToolChoice); choice != nil {
		payload.ToolChoice = choice
	}

	return backendChatPayload{Body: payload, ToolNameReverse: reverse}
}

func (a anthropicAdapter) DecodeMessagesResponse(data []byte, reverse map[string]string) (ChatMessage, error) {
	var parsed anthropicMessagesResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ChatMessage{}, llmDecodeError("decode messages response", err)
	}

	if parsed.Error != nil {
		return ChatMessage{}, llmAPIError(parsed.Error.Message)
	}

	role := strings.TrimSpace(parsed.Role)
	if role == "" {
		role = "assistant"
	}

	var (
		content   strings.Builder
		toolCalls []ToolCall
	)

	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			arguments := "{}"
			if block.Input != nil {
				if encoded, err := json.Marshal(block.Input); err == nil {
					arguments = string(encoded)
				}
			}

			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      block.Name,
					Arguments: arguments,
				},
			})
		}
	}

	finishReason := strings.TrimSpace(parsed.StopReason)
	if finishReason == "" {
		finishReason = "stop"
	}

	return ChatMessage{
		Role:      role,
		Content:   stripSpecialTokens(content.String()),
		ToolCalls: remapToolCalls(toolCalls, reverse),
		Usage: Usage{
			PromptTokens:               parsed.Usage.InputTokens,
			CompletionTokens:           parsed.Usage.OutputTokens,
			TotalTokens:                parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
			PromptCacheHitTokens:       intPtrValue(parsed.Usage.CacheReadInputTokens),
			PromptCacheMissTokens:      intPtrValue(parsed.Usage.CacheCreationInputTokens),
			PromptCacheAccountingKnown: parsed.Usage.CacheReadInputTokens != nil || parsed.Usage.CacheCreationInputTokens != nil,
		},
		FinishReason: finishReason,
	}, nil
}

type anthropicMessagesRequest struct {
	Model       string                  `json:"model"`
	MaxTokens   int                     `json:"max_tokens"`
	Messages    []anthropicMessage      `json:"messages"`
	System      string                  `json:"system,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
	Temperature *float64                `json:"temperature,omitempty"`
	TopP        *float64                `json:"top_p,omitempty"`
	TopK        int                     `json:"top_k,omitempty"`
	Tools       []anthropicTool         `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoiceDef `json:"tool_choice,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolChoiceDef struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type anthropicMessagesResponse struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
	} `json:"error,omitempty"`
	Role       string                  `json:"role,omitempty"`
	Content    []anthropicContentBlock `json:"content,omitempty"`
	StopReason string                  `json:"stop_reason,omitempty"`
	Usage      struct {
		InputTokens              int  `json:"input_tokens,omitempty"`
		OutputTokens             int  `json:"output_tokens,omitempty"`
		CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
		CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

func intPtrValue(value *int) int {
	if value == nil {
		return 0
	}

	return *value
}

func anthropicMessagesFromChat(messages []ChatMessage) (string, []anthropicMessage) {
	var systemParts []string
	out := make([]anthropicMessage, 0, len(messages))

	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system", "developer":
			if text := strings.TrimSpace(msg.Content); text != "" {
				systemParts = append(systemParts, text)
			}
		case "assistant":
			out = append(out, anthropicAssistantMessage(msg))
		case "tool":
			out = append(out, anthropicToolResultMessage(msg))
		default:
			if role == "" {
				role = "user"
			}

			out = append(out, anthropicMessage{
				Role:    role,
				Content: anthropicTextBlocks(msg.Content),
			})
		}
	}

	return strings.Join(systemParts, "\n"), out
}

func anthropicAssistantMessage(msg ChatMessage) anthropicMessage {
	blocks := anthropicTextBlocks(msg.Content)
	for _, call := range msg.ToolCalls {
		if strings.TrimSpace(call.Function.Name) == "" {
			continue
		}

		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  hostedToolNameReplacer.Replace(call.Function.Name),
			Input: anthropicToolInput(call.Function.Arguments),
		})
	}

	return anthropicMessage{
		Role:    "assistant",
		Content: blocks,
	}
}

func anthropicToolResultMessage(msg ChatMessage) anthropicMessage {
	return anthropicMessage{
		Role: "user",
		Content: []anthropicContentBlock{{
			Type:      "tool_result",
			ToolUseID: msg.ToolCallID,
			Content:   msg.Content,
		}},
	}
}

func anthropicTextBlocks(content string) []anthropicContentBlock {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	return []anthropicContentBlock{{
		Type: "text",
		Text: content,
	}}
}

func anthropicToolsFromChatTools(tools []Tool) ([]anthropicTool, map[string]string) {
	normalized, reverse := normalizeHostedTools(tools)
	out := make([]anthropicTool, 0, len(normalized))

	for _, tool := range normalized {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}

		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}

		schema := tool.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}

		out = append(out, anthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: schema,
		})
	}

	return out, reverse
}

func anthropicToolInput(arguments string) any {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return map[string]any{}
	}

	var decoded any
	if err := json.Unmarshal([]byte(arguments), &decoded); err == nil {
		return decoded
	}

	return map[string]any{"arguments": arguments}
}

func anthropicToolChoice(choice string) *anthropicToolChoiceDef {
	choice = strings.TrimSpace(choice)
	switch choice {
	case "":
		return nil
	case "auto", "none":
		return &anthropicToolChoiceDef{Type: choice}
	case "required":
		return &anthropicToolChoiceDef{Type: "any"}
	default:
		return &anthropicToolChoiceDef{Type: "tool", Name: hostedToolNameReplacer.Replace(choice)}
	}
}
