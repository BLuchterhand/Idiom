package llama

import (
	"encoding/json"

	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func mustChatAdapter(backend string) chatCompletionsBackendAdapter {
	adapter, ok := backendAdapterFor(backend).(chatCompletionsBackendAdapter)
	ExpectWithOffset(1, ok).To(BeTrue(), "backend %q should implement the chat-completions protocol", backend)
	return adapter
}

func mustMessagesAdapter(backend string) messagesBackendAdapter {
	adapter, ok := backendAdapterFor(backend).(messagesBackendAdapter)
	ExpectWithOffset(1, ok).To(BeTrue(), "backend %q should implement the messages protocol", backend)
	return adapter
}

var _ = Describe("Backend adapter registry", func() {
	type adapterCase struct {
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
	}

	for _, tc := range []adapterCase{
		{config.BackendLlama, config.BackendLlama, backendProtocolChatCompletions, "/v1/models", "/v1/chat/completions", "", false, "", true, true},
		{config.BackendMLX, config.BackendMLX, backendProtocolChatCompletions, "/v1/models", "/v1/chat/completions", "", false, "", false, true},
		{config.BackendZAI, config.BackendZAI, backendProtocolChatCompletions, "/models", "/chat/completions", "", true, "ZAI_API_KEY", true, true},
		{config.BackendGLM, config.BackendGLM, backendProtocolChatCompletions, "/models", "/chat/completions", "", true, "ZAI_API_KEY", true, true},
		{config.BackendDeepSeek, config.BackendDeepSeek, backendProtocolChatCompletions, "/v1/models", "/v1/chat/completions", "", true, "DEEPSEEK_API_KEY", true, true},
		{config.BackendCodex, config.BackendCodex, backendProtocolResponses, "/v1/models", "", "", true, "OPENAI_API_KEY", true, false},
		{config.BackendAnthropic, config.BackendAnthropic, backendProtocolMessages, "/v1/models", "", "/v1/messages", true, "ANTHROPIC_API_KEY", true, false},
	} {
		tc := tc
		It("owns the protocol, endpoints, auth, and capabilities for "+tc.backend, func() {
			adapter := backendAdapterFor(tc.backend)
			caps := adapter.StructuredOutputCapabilities()

			Expect(adapter.Kind()).To(Equal(tc.wantKind))
			Expect(adapter.Protocol()).To(Equal(tc.wantProtocol))
			Expect(adapter.ModelsPath()).To(Equal(tc.wantModelsPath))
			Expect(adapter.RequiresAuth()).To(Equal(tc.wantAuth))
			Expect(adapter.DefaultAPIKeyEnv()).To(Equal(tc.wantEnv))
			Expect(caps.NativeTools).To(Equal(tc.wantNativeTools))
			Expect(caps.ThinkingControls).To(Equal(tc.wantThinkingCaps))

			chatAdapter, isChat := adapter.(chatCompletionsBackendAdapter)
			if tc.wantProtocol == backendProtocolChatCompletions {
				Expect(isChat).To(BeTrue())
				Expect(chatAdapter.ChatCompletionsPath()).To(Equal(tc.wantChatPath))
			} else {
				Expect(isChat).To(BeFalse())
			}

			messagesAdapter, isMessages := adapter.(messagesBackendAdapter)
			if tc.wantProtocol == backendProtocolMessages {
				Expect(isMessages).To(BeTrue())
				Expect(messagesAdapter.MessagesPath()).To(Equal(tc.wantMessagesPath))
			} else {
				Expect(isMessages).To(BeFalse())
			}
		})
	}

	It("keeps unknown-backend capabilities conservative", func() {
		caps := BackendStructuredOutputCapabilities("unknown")
		Expect(caps.NativeTools).To(BeTrue())
		Expect(caps.JSONObject).To(BeTrue())
		Expect(caps.ThinkingControls).To(BeFalse())
		Expect(caps.StrictJSONSchema).To(BeFalse())
		Expect(caps.Grammar).To(BeFalse())
		Expect(caps.RequestTimeoutTelemetry).To(BeFalse())
		Expect(NewClient("http://example.invalid", 0).WithBackend("unknown").Backend()).To(Equal(config.BackendLlama))
	})
})

var _ = Describe("Anthropic adapter", func() {
	It("owns native message and tool-block mapping", func() {
		adapter := mustMessagesAdapter(config.BackendAnthropic)
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
		Expect(ok).To(BeTrue())
		Expect(req.Model).To(Equal("claude-sonnet"))
		Expect(req.MaxTokens).To(Equal(1024))
		Expect(req.System).To(Equal("system prompt"))
		Expect(req.TopK).To(Equal(20))
		Expect(req.ToolChoice).NotTo(BeNil())
		Expect(req.ToolChoice.Type).To(Equal("any"))
		Expect(req.Tools).To(HaveLen(1))
		Expect(req.Tools[0].Name).To(Equal("mcp_read_file"))
		Expect(payload.ToolNameReverse).To(HaveKeyWithValue("mcp_read_file", "mcp:read/file"))
		Expect(req.Messages).To(HaveLen(3))

		assistant := req.Messages[1]
		Expect(assistant.Role).To(Equal("assistant"))
		Expect(assistant.Content).To(HaveLen(2))
		Expect(assistant.Content[1].Type).To(Equal("tool_use"))
		Expect(assistant.Content[1].Name).To(Equal("mcp_read_file"))

		toolResult := req.Messages[2]
		Expect(toolResult.Role).To(Equal("user"))
		Expect(toolResult.Content).To(HaveLen(1))
		Expect(toolResult.Content[0].Type).To(Equal("tool_result"))
		Expect(toolResult.Content[0].ToolUseID).To(Equal("toolu_1"))
	})

	It("parses native text, tool-use, and usage data", func() {
		adapter := mustMessagesAdapter(config.BackendAnthropic)
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
		Expect(err).NotTo(HaveOccurred())
		Expect(reply.Content).To(Equal("Need the file."))
		Expect(reply.FinishReason).To(Equal("tool_use"))
		Expect(reply.ToolCalls).To(HaveLen(1))
		Expect(reply.ToolCalls[0].ID).To(Equal("toolu_1"))
		Expect(reply.ToolCalls[0].Function.Name).To(Equal("mcp:read/file"))
		Expect(reply.ToolCalls[0].Function.Arguments).To(MatchJSON(`{"path":"README.md"}`))
		Expect(reply.Usage).To(Equal(Usage{
			PromptTokens:               11,
			CompletionTokens:           7,
			TotalTokens:                18,
			PromptCacheHitTokens:       3,
			PromptCacheMissTokens:      0,
			PromptCacheAccountingKnown: true,
		}))
	})
})

var _ = Describe("Backend-owned request history", func() {
	It("normalizes hosted reasoning history and tool names", func() {
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

		deepseek := mustChatAdapter(config.BackendDeepSeek).ChatCompletionsPayload(messages, ChatOptions{
			Model: "deepseek-v4-flash",
			Tools: tools,
		}, false, backendRequestSettings{})
		deepseekPayload, ok := deepseek.Body.(hostedChatPayload)
		Expect(ok).To(BeTrue())
		Expect(deepseekPayload.Messages[0].ReasoningContent).To(BeEmpty())
		Expect(deepseekPayload.Messages[1].ReasoningContent).To(Equal("keep only for deepseek"))
		deepseekTools, ok := deepseekPayload.Tools.([]Tool)
		Expect(ok).To(BeTrue())
		Expect(deepseekTools).To(HaveLen(1))
		Expect(deepseekTools[0].Function.Name).To(Equal("mcp_read_file"))
		Expect(deepseek.ToolNameReverse).To(HaveKeyWithValue("mcp_read_file", "mcp:read/file"))

		zai := mustChatAdapter(config.BackendZAI).ChatCompletionsPayload(messages, ChatOptions{
			Model: "glm-5.1",
			Tools: tools,
		}, false, backendRequestSettings{ReasoningEffort: "max"})
		zaiPayload, ok := zai.Body.(hostedChatPayload)
		Expect(ok).To(BeTrue())
		for _, message := range zaiPayload.Messages {
			Expect(message.ReasoningContent).To(BeEmpty())
		}
		Expect(zaiPayload.ReasoningEffort).To(Equal("max"))
	})

	It("preserves only DeepSeek assistant reasoning when thinking is disabled", func() {
		thinkingOff := false
		messages := []ChatMessage{
			{Role: "system", Content: "system", ReasoningContent: "strip system"},
			{Role: "user", Content: "user", ReasoningContent: "strip user"},
			{Role: "assistant", Content: "prior", ReasoningContent: "must pass back"},
			{Role: "tool", Content: "tool", ReasoningContent: "strip tool"},
		}
		opts := ChatOptions{EnableThinking: &thinkingOff, PreserveThinking: false}

		got := NewClient("http://example.invalid", 0).
			WithBackend(config.BackendDeepSeek).
			ChatMessagesForRequest(messages, opts)
		Expect(got).To(HaveLen(3), "orphaned tool messages should be repaired")
		Expect(got[0].ReasoningContent).To(BeEmpty())
		Expect(got[1].ReasoningContent).To(BeEmpty())
		Expect(got[2].ReasoningContent).To(Equal("must pass back"))

		defaultBackend := NewClient("http://example.invalid", 0).
			WithBackend(config.BackendLlama).
			ChatMessagesForRequest(messages, opts)
		for _, message := range defaultBackend {
			Expect(message.ReasoningContent).To(BeEmpty())
		}
	})

	It("merges incremental system guidance for MLX templates", func() {
		messages := []ChatMessage{
			{Role: "user", Content: "inspect the project"},
			{Role: "system", Content: "keep the answer concise"},
			{Role: "assistant", Content: "I will inspect it"},
			{Role: "system", Content: "cite the relevant paths"},
		}

		got := NewClient("http://example.invalid", 0).
			WithBackend(config.BackendMLX).
			ChatMessagesForRequest(messages, ChatOptions{PreserveThinking: true})

		Expect(got).To(HaveLen(3))
		Expect(got[0].Role).To(Equal("system"))
		Expect(got[0].Content).To(Equal("keep the answer concise\n\ncite the relevant paths"))
		Expect(got[1].Role).To(Equal("user"))
		Expect(got[2].Role).To(Equal("assistant"))
	})

	It("uses the same reasoning policy for an assistant history message", func() {
		opts := ChatOptions{PreserveThinking: false}
		message := ChatMessage{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "must pass back",
		}

		got := PrepareAssistantHistoryMessageForBackend(message, opts, config.BackendDeepSeek)
		Expect(got.ReasoningContent).To(Equal("must pass back"))
		got = PrepareAssistantHistoryMessageForBackend(message, opts, config.BackendLlama)
		Expect(got.ReasoningContent).To(BeEmpty())
	})

	It("owns local thinking kwargs and native-tool suppression", func() {
		enableThinking := false
		tools := []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "read_file",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}}

		mlx := mustChatAdapter(config.BackendMLX).ChatCompletionsPayload(nil, ChatOptions{
			Model:          "qwen",
			EnableThinking: &enableThinking,
			Tools:          tools,
			ToolChoice:     "auto",
		}, false, backendRequestSettings{})
		mlxReq, ok := mlx.Body.(chatRequest)
		Expect(ok).To(BeTrue())
		Expect(mlxReq.ChatTemplateKwargs).To(BeNil())
		Expect(mlxReq.Tools).To(BeEmpty())
		Expect(mlxReq.ToolChoice).To(BeEmpty())

		llamaPayload := mustChatAdapter(config.BackendLlama).ChatCompletionsPayload(nil, ChatOptions{
			Model:          "llama",
			EnableThinking: &enableThinking,
			Tools:          tools,
			ToolChoice:     "auto",
		}, false, backendRequestSettings{})
		llamaReq, ok := llamaPayload.Body.(chatRequest)
		Expect(ok).To(BeTrue())
		Expect(llamaReq.ChatTemplateKwargs).NotTo(BeNil())
		Expect(llamaReq.ChatTemplateKwargs.EnableThinking).NotTo(BeNil())
		Expect(*llamaReq.ChatTemplateKwargs.EnableThinking).To(BeFalse())
		Expect(llamaReq.Tools).To(HaveLen(1))
		Expect(llamaReq.ToolChoice).To(Equal("auto"))
	})
})
