package llama

import (
	"encoding/json"
	"testing"

	"idiom/internal/platform/config"
)

func mustChatAdapter(t *testing.T, backend string) chatCompletionsBackendAdapter {
	t.Helper()

	adapter, ok := backendAdapterFor(backend).(chatCompletionsBackendAdapter)
	if !ok {
		t.Fatalf("backend %q does not implement chat-completions adapter", backend)
	}

	return adapter
}

func mustMessagesAdapter(t *testing.T, backend string) messagesBackendAdapter {
	t.Helper()

	adapter, ok := backendAdapterFor(backend).(messagesBackendAdapter)
	if !ok {
		t.Fatalf("backend %q does not implement messages adapter", backend)
	}

	return adapter
}

func TestBackendAdapterRegistryOwnsProtocolsEndpointsAuthAndCapabilities(t *testing.T) {
	for _, tc := range []struct {
		backend          string
		wantKind         string
		wantProtocol     backendProtocol
		wantModelsPath   string
		wantChatPath     string
		wantMessagesPath string
		wantAuth         bool
		wantEnv          string
		wantNativeTools  bool
		wantThinkingCaps bool
	}{
		{config.BackendLlama, config.BackendLlama, backendProtocolChatCompletions, "/v1/models", "/v1/chat/completions", "", false, "", true, true},
		{config.BackendMLX, config.BackendMLX, backendProtocolChatCompletions, "/v1/models", "/v1/chat/completions", "", false, "", false, true},
		{config.BackendZAI, config.BackendZAI, backendProtocolChatCompletions, "/models", "/chat/completions", "", true, "ZAI_API_KEY", true, true},
		{config.BackendGLM, config.BackendGLM, backendProtocolChatCompletions, "/models", "/chat/completions", "", true, "ZAI_API_KEY", true, true},
		{config.BackendDeepSeek, config.BackendDeepSeek, backendProtocolChatCompletions, "/v1/models", "/v1/chat/completions", "", true, "DEEPSEEK_API_KEY", true, true},
		{config.BackendCodex, config.BackendCodex, backendProtocolResponses, "/v1/models", "", "", true, "OPENAI_API_KEY", true, false},
		{config.BackendAnthropic, config.BackendAnthropic, backendProtocolMessages, "/v1/models", "", "/v1/messages", true, "ANTHROPIC_API_KEY", true, false},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			adapter := backendAdapterFor(tc.backend)
			caps := adapter.StructuredOutputCapabilities()

			if adapter.Kind() != tc.wantKind ||
				adapter.Protocol() != tc.wantProtocol ||
				adapter.ModelsPath() != tc.wantModelsPath ||
				adapter.RequiresAuth() != tc.wantAuth ||
				adapter.DefaultAPIKeyEnv() != tc.wantEnv ||
				caps.NativeTools != tc.wantNativeTools ||
				caps.ThinkingControls != tc.wantThinkingCaps {

				t.Fatalf("adapter mismatch: kind=%q protocol=%q models=%q auth=%v env=%q caps=%#v",
					adapter.Kind(), adapter.Protocol(), adapter.ModelsPath(),
					adapter.RequiresAuth(), adapter.DefaultAPIKeyEnv(), caps)
			}

			chatAdapter, isChat := adapter.(chatCompletionsBackendAdapter)
			if tc.wantProtocol == backendProtocolChatCompletions {
				if !isChat {
					t.Fatalf("backend %q should implement chat-completions adapter", tc.backend)
				}

				if got := chatAdapter.ChatCompletionsPath(); got != tc.wantChatPath {
					t.Fatalf("chat adapter path = %q, want %q", got, tc.wantChatPath)
				}
			} else if isChat {
				t.Fatalf("non-chat adapter unexpectedly implements chat-completions: %#v", adapter)
			}

			messagesAdapter, isMessages := adapter.(messagesBackendAdapter)
			if tc.wantProtocol == backendProtocolMessages {
				if !isMessages {
					t.Fatalf("backend %q should implement messages adapter", tc.backend)
				}

				if got := messagesAdapter.MessagesPath(); got != tc.wantMessagesPath {
					t.Fatalf("messages adapter path = %q, want %q", got, tc.wantMessagesPath)
				}
			} else if isMessages {
				t.Fatalf("non-messages adapter unexpectedly implements messages: %#v", adapter)
			}
		})
	}
}

func TestUnknownBackendCapabilityReportStaysConservative(t *testing.T) {
	caps := BackendStructuredOutputCapabilities("unknown")

	if !caps.NativeTools || !caps.JSONObject {
		t.Fatalf("unknown backend should keep legacy native-tools/json-object defaults, got %#v", caps)
	}

	if caps.ThinkingControls || caps.StrictJSONSchema || caps.Grammar || caps.RequestTimeoutTelemetry {
		t.Fatalf("unknown backend should not claim unenforced capabilities, got %#v", caps)
	}

	if got := NewClient("http://example.invalid", 0).WithBackend("unknown").Backend(); got != config.BackendLlama {
		t.Fatalf("client backend fallback = %q, want %q", got, config.BackendLlama)
	}
}

func TestAnthropicAdapterOwnsNativeMessageMapping(t *testing.T) {
	adapter := mustMessagesAdapter(t, config.BackendAnthropic)
	payload := adapter.MessagesPayload([]ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "read the file"},
		{
			Role:    "assistant",
			Content: "I'll read it.",
			ToolCalls: []ToolCall{{
				ID:   "toolu_1",
				Type: "function",
				Function: ToolCallFunc{
					Name:      "mcp:read/file",
					Arguments: `{"path":"README.md"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "toolu_1", Content: "contents"},
	}, ChatOptions{
		Model:      "claude-sonnet",
		MaxTokens:  1024,
		TopK:       20,
		ToolChoice: "required",
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "mcp:read/file",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}},
	}, false, backendRequestSettings{})

	req, ok := payload.Body.(anthropicMessagesRequest)
	if !ok {
		t.Fatalf("expected anthropic messages request, got %#v", payload.Body)
	}

	if req.Model != "claude-sonnet" || req.MaxTokens != 1024 || req.System != "system prompt" || req.TopK != 20 {
		t.Fatalf("unexpected Anthropic request basics: %#v", req)
	}

	if req.ToolChoice == nil || req.ToolChoice.Type != "any" {
		t.Fatalf("required tool choice should map to Anthropic any, got %#v", req.ToolChoice)
	}

	if len(req.Tools) != 1 || req.Tools[0].Name != "mcp_read_file" {
		t.Fatalf("expected sanitized Anthropic tool definition, got %#v", req.Tools)
	}

	if payload.ToolNameReverse["mcp_read_file"] != "mcp:read/file" {
		t.Fatalf("expected reverse tool-name map, got %#v", payload.ToolNameReverse)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("expected system to be hoisted out of messages, got %#v", req.Messages)
	}

	assistant := req.Messages[1]
	if assistant.Role != "assistant" || len(assistant.Content) != 2 ||
		assistant.Content[1].Type != "tool_use" ||
		assistant.Content[1].Name != "mcp_read_file" {

		t.Fatalf("expected assistant text and tool_use blocks, got %#v", assistant)
	}

	toolResult := req.Messages[2]
	if toolResult.Role != "user" || len(toolResult.Content) != 1 ||
		toolResult.Content[0].Type != "tool_result" ||
		toolResult.Content[0].ToolUseID != "toolu_1" {

		t.Fatalf("expected tool result user block, got %#v", toolResult)
	}
}

func TestAnthropicAdapterParsesNativeTextToolUseAndUsage(t *testing.T) {
	adapter := mustMessagesAdapter(t, config.BackendAnthropic)
	reply, err := adapter.DecodeMessagesResponse([]byte(`{
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Need the file."},
			{"type": "tool_use", "id": "toolu_1", "name": "mcp_read_file", "input": {"path": "README.md"}}
		],
		"stop_reason": "tool_use",
		"usage": {
			"input_tokens": 11,
			"output_tokens": 7,
			"cache_read_input_tokens": 3,
			"cache_creation_input_tokens": 0
		}
	}`), map[string]string{"mcp_read_file": "mcp:read/file"})
	if err != nil {
		t.Fatalf("DecodeMessagesResponse: %v", err)
	}

	if reply.Content != "Need the file." || reply.FinishReason != "tool_use" {
		t.Fatalf("unexpected Anthropic text/finish: %#v", reply)
	}

	if len(reply.ToolCalls) != 1 ||
		reply.ToolCalls[0].ID != "toolu_1" ||
		reply.ToolCalls[0].Function.Name != "mcp:read/file" ||
		reply.ToolCalls[0].Function.Arguments != `{"path":"README.md"}` {

		t.Fatalf("unexpected Anthropic tool call: %#v", reply.ToolCalls)
	}

	if reply.Usage.PromptTokens != 11 ||
		reply.Usage.CompletionTokens != 7 ||
		reply.Usage.TotalTokens != 18 ||
		reply.Usage.PromptCacheHitTokens != 3 ||
		reply.Usage.PromptCacheMissTokens != 0 ||
		!reply.Usage.PromptCacheAccountingKnown {

		t.Fatalf("unexpected Anthropic usage: %#v", reply.Usage)
	}
}

func TestHostedAdaptersOwnReasoningHistoryAndToolNameNormalization(t *testing.T) {
	tools := []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:       "mcp:read/file",
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
	}}
	messages := []ChatMessage{
		{Role: "user", Content: "hi", ReasoningContent: "strip user"},
		{Role: "assistant", Content: "prior", ReasoningContent: "keep only for deepseek"},
	}

	deepseek := mustChatAdapter(t, config.BackendDeepSeek).ChatCompletionsPayload(messages, ChatOptions{
		Model: "deepseek-v4-flash",
		Tools: tools,
	}, false, backendRequestSettings{})

	deepseekPayload, ok := deepseek.Body.(hostedChatPayload)
	if !ok {
		t.Fatalf("expected hosted DeepSeek payload, got %#v", deepseek.Body)
	}

	if got := deepseekPayload.Messages[0].ReasoningContent; got != "" {
		t.Fatalf("DeepSeek should strip non-assistant reasoning_content, got %q", got)
	}

	if got := deepseekPayload.Messages[1].ReasoningContent; got != "keep only for deepseek" {
		t.Fatalf("DeepSeek should preserve assistant reasoning_content, got %q", got)
	}

	deepseekTools, ok := deepseekPayload.Tools.([]Tool)
	if !ok || len(deepseekTools) != 1 || deepseekTools[0].Function.Name != "mcp_read_file" {
		t.Fatalf("expected sanitized hosted tool name, got %#v", deepseekPayload.Tools)
	}

	if deepseek.ToolNameReverse["mcp_read_file"] != "mcp:read/file" {
		t.Fatalf("expected reverse hosted tool-name map, got %#v", deepseek.ToolNameReverse)
	}

	zai := mustChatAdapter(t, config.BackendZAI).ChatCompletionsPayload(messages, ChatOptions{
		Model: "glm-5.1",
		Tools: tools,
	}, false, backendRequestSettings{ReasoningEffort: "max"})

	zaiPayload, ok := zai.Body.(hostedChatPayload)
	if !ok {
		t.Fatalf("expected hosted ZAI payload, got %#v", zai.Body)
	}

	for _, msg := range zaiPayload.Messages {
		if msg.ReasoningContent != "" {
			t.Fatalf("non-DeepSeek hosted adapters should strip reasoning_content, got %#v", zaiPayload.Messages)
		}
	}

	if zaiPayload.ReasoningEffort != "max" {
		t.Fatalf("ZAI adapter should carry configured reasoning_effort, got %q", zaiPayload.ReasoningEffort)
	}
}

func TestLocalAdaptersOwnThinkingKwargsAndToolSuppression(t *testing.T) {
	enableThinking := false
	tools := []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:       "read_file",
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
	}}

	mlx := mustChatAdapter(t, config.BackendMLX).ChatCompletionsPayload(nil, ChatOptions{
		Model:          "qwen",
		EnableThinking: &enableThinking,
		Tools:          tools,
		ToolChoice:     "auto",
	}, false, backendRequestSettings{})

	mlxReq, ok := mlx.Body.(chatRequest)
	if !ok {
		t.Fatalf("expected local chat request, got %#v", mlx.Body)
	}

	if mlxReq.ChatTemplateKwargs != nil {
		t.Fatalf("MLX adapter should omit implicit chat_template_kwargs, got %#v", mlxReq.ChatTemplateKwargs)
	}

	if len(mlxReq.Tools) != 0 || mlxReq.ToolChoice != "" {
		t.Fatalf("MLX adapter should strip native tools by default, got tools=%#v choice=%q", mlxReq.Tools, mlxReq.ToolChoice)
	}

	llamaPayload := mustChatAdapter(t, config.BackendLlama).ChatCompletionsPayload(nil, ChatOptions{
		Model:          "llama",
		EnableThinking: &enableThinking,
		Tools:          tools,
		ToolChoice:     "auto",
	}, false, backendRequestSettings{})

	llamaReq, ok := llamaPayload.Body.(chatRequest)
	if !ok {
		t.Fatalf("expected local chat request, got %#v", llamaPayload.Body)
	}

	if llamaReq.ChatTemplateKwargs == nil || llamaReq.ChatTemplateKwargs.EnableThinking == nil || *llamaReq.ChatTemplateKwargs.EnableThinking {
		t.Fatalf("llama adapter should preserve request-level thinking kwargs, got %#v", llamaReq.ChatTemplateKwargs)
	}

	if len(llamaReq.Tools) != 1 || llamaReq.ToolChoice != "auto" {
		t.Fatalf("llama adapter should keep native tools, got tools=%#v choice=%q", llamaReq.Tools, llamaReq.ToolChoice)
	}
}
