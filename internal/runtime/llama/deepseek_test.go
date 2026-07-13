package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type reasoningFixture struct {
	name     string
	response string
	history  string
}

var _ = Describe("DeepSeek reasoning history", func() {
	for _, tc := range []reasoningFixture{
		{
			name:     "omitted",
			response: `{"choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			name:     "empty",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			name:     "normal",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"The user asked a simple question.","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "The user asked a simple question.",
		},
		{
			name:     "multiline",
			response: "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"reasoning_content\":\"Let me think about this.\\nFirst, I need to parse the request.\\nThen, I can formulate a response.\",\"content\":\"Hello\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}",
			history:  "Let me think about this.\nFirst, I need to parse the request.\nThen, I can formulate a response.",
		},
		{
			name:     "special characters",
			response: "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"reasoning_content\":\"Special: \\\"quotes\\\" \\\\backslash\\\\ <tag> unicode: ñ ∑ ∞\",\"content\":\"Hello\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}",
			history:  "Special: \"quotes\" \\backslash\\ <tag> unicode: ñ ∑ ∞",
		},
		{
			name:     "truncated",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"This reasoning was cut off because the model hit","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "This reasoning was cut off because the model hit",
		},
		{
			name:     "long",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"Step 1: Analyze. Step 2: Think deeper. Step 3: Consider alternatives. Step 4: Evaluate trade-offs. Step 5: Select best approach. Step 6: Verify correctness. Step 7: Prepare response.","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "Step 1: Analyze. Step 2: Think deeper. Step 3: Consider alternatives. Step 4: Evaluate trade-offs. Step 5: Select best approach. Step 6: Verify correctness. Step 7: Prepare response.",
		},
	} {
		tc := tc
		It("preserves "+tc.name+" reasoning content in a follow-up request", func() {
			enableThinking := true
			var gotRequest map[string]any
			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				callCount++
				Expect(json.NewDecoder(r.Body).Decode(&gotRequest)).To(Succeed())
				if callCount == 1 {
					_, _ = w.Write([]byte(tc.response))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"follow-up"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			}))
			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)
			reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "hi"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.ReasoningContent).To(Equal(tc.history))

			_, err = client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "prior"},
				{Role: "assistant", Content: reply.Content, ReasoningContent: reply.ReasoningContent},
				{Role: "user", Content: "follow-up"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			Expect(err).NotTo(HaveOccurred())

			messages, ok := gotRequest["messages"].([]any)
			Expect(ok).To(BeTrue())
			Expect(len(messages)).To(BeNumerically(">=", 2))
			assistant := requestMessageWithRole(messages, "assistant")
			Expect(assistant).NotTo(BeNil())
			reasoning, present := assistant["reasoning_content"]
			if tc.history == "" {
				if present {
					Expect(reasoning).To(BeEmpty())
				}
				return
			}
			Expect(present).To(BeTrue())
			Expect(reasoning).To(Equal(tc.history))
		})
	}

	for _, tc := range []reasoningFixture{
		{
			name:     "normal stream",
			response: "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Thinking step by step\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Answer\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n",
		},
		{
			name:     "multiline stream",
			response: "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Line one\\n\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Line two\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Done\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n",
		},
		{
			name:     "stream without reasoning",
			response: "data: {\"choices\":[{\"delta\":{\"content\":\"No reasoning\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n",
		},
	} {
		tc := tc
		It("does not invent reasoning history after a "+tc.name, func() {
			enableThinking := true
			var gotRequest map[string]any
			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				callCount++
				Expect(json.NewDecoder(r.Body).Decode(&gotRequest)).To(Succeed())
				if callCount == 1 {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(tc.response))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"follow-up"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			}))
			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)
			var streamedContent strings.Builder
			Expect(client.StreamChat(context.Background(), []ChatMessage{
				{Role: "user", Content: "hi"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			}, func(delta string) error {
				streamedContent.WriteString(delta)
				return nil
			})).To(Succeed())

			_, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "prior"},
				{Role: "assistant", Content: streamedContent.String()},
				{Role: "user", Content: "follow-up"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			Expect(err).NotTo(HaveOccurred())

			messages, ok := gotRequest["messages"].([]any)
			Expect(ok).To(BeTrue())
			assistant := requestMessageWithRole(messages, "assistant")
			Expect(assistant).NotTo(BeNil())
			if reasoning, present := assistant["reasoning_content"]; present {
				Expect(reasoning).To(BeEmpty())
			}
		})
	}

	It("strips reasoning from non-assistant messages", func() {
		enableThinking := true
		gotRequest := captureChatRequest([]ChatMessage{
			{Role: "system", Content: "be concise", ReasoningContent: "should be stripped"},
			{Role: "user", Content: "hello", ReasoningContent: "should be stripped too"},
			{Role: "assistant", Content: "prior answer", ReasoningContent: "preserved reasoning"},
		}, config.BackendDeepSeek, ChatOptions{
			Model:          "deepseek-v4-flash",
			EnableThinking: &enableThinking,
			MaxTokens:      32,
		})

		messages, ok := gotRequest["messages"].([]any)
		Expect(ok).To(BeTrue())
		Expect(len(messages)).To(BeNumerically(">=", 3))
		for index, expected := range []struct {
			role      string
			reasoning string
		}{
			{"system", ""},
			{"user", ""},
			{"assistant", "preserved reasoning"},
		} {
			message, ok := messages[index].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(message).To(HaveKeyWithValue("role", expected.role))
			reasoning, _ := message["reasoning_content"].(string)
			Expect(reasoning).To(Equal(expected.reasoning))
		}
	})

	It("preserves reasoning on an assistant tool-call message", func() {
		enableThinking := true
		gotRequest := captureChatRequest([]ChatMessage{
			{Role: "user", Content: "hi"},
			{
				Role:             "assistant",
				ReasoningContent: "I should call read_file",
				ToolCalls: []ToolCall{{
					ID:       "call_1",
					Function: ToolCallFunc{Name: "read_file", Arguments: `{"path":"x"}`},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "result"},
		}, config.BackendDeepSeek, ChatOptions{
			Model:          "deepseek-v4-flash",
			EnableThinking: &enableThinking,
			MaxTokens:      32,
		})

		messages, ok := gotRequest["messages"].([]any)
		Expect(ok).To(BeTrue())
		Expect(len(messages)).To(BeNumerically(">=", 3))
		assistant, ok := messages[1].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(assistant).To(HaveKeyWithValue("role", "assistant"))
		Expect(assistant).To(HaveKeyWithValue("reasoning_content", "I should call read_file"))
	})
})

var _ = Describe("Other backend reasoning history", func() {
	for _, backend := range []string{config.BackendZAI, config.BackendGLM, config.BackendMiniMax} {
		backend := backend
		It("strips reasoning from every message for "+backend, func() {
			enableThinking := true
			gotRequest := captureChatRequest([]ChatMessage{
				{Role: "user", Content: "hi", ReasoningContent: "should be stripped"},
				{Role: "assistant", Content: "prior", ReasoningContent: "should also be stripped"},
			}, backend, ChatOptions{
				Model:          backend + "-model",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			expectRequestReasoningToBeEmpty(gotRequest)
		})
	}

	for _, backend := range []string{config.BackendLlama, config.BackendMLX} {
		backend := backend
		It("preserves local reasoning when requested for "+backend, func() {
			gotRequest := captureChatRequest([]ChatMessage{
				{Role: "user", Content: "hi", ReasoningContent: "preserved"},
				{Role: "assistant", Content: "prior", ReasoningContent: "also preserved"},
			}, backend, ChatOptions{
				Model:            backend + "-model",
				PreserveThinking: true,
				MaxTokens:        32,
			})

			messages, ok := gotRequest["messages"].([]any)
			Expect(ok).To(BeTrue())
			Expect(len(messages)).To(BeNumerically(">=", 2))
			for _, raw := range messages {
				message, ok := raw.(map[string]any)
				Expect(ok).To(BeTrue())
				Expect(message).To(HaveKey("reasoning_content"))
				Expect(message["reasoning_content"]).NotTo(BeEmpty())
			}
		})

		It("strips local reasoning when preservation is disabled for "+backend, func() {
			gotRequest := captureChatRequest([]ChatMessage{
				{Role: "user", Content: "hi", ReasoningContent: "strip user"},
				{Role: "assistant", Content: "prior", ReasoningContent: "strip assistant"},
			}, backend, ChatOptions{
				Model:     backend + "-model",
				MaxTokens: 32,
			})
			expectRequestReasoningToBeEmpty(gotRequest)
		})
	}
})

func captureChatRequest(messages []ChatMessage, backend string, options ChatOptions) map[string]any {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(json.NewDecoder(r.Body).Decode(&gotRequest)).To(Succeed())
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	DeferCleanup(server.Close)

	client := NewClient(server.URL, time.Second).WithBackend(backend)
	_, err := client.CompleteChatMessage(context.Background(), messages, options)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return gotRequest
}

func requestMessageWithRole(messages []any, role string) map[string]any {
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if ok && message["role"] == role {
			return message
		}
	}
	return nil
}

func expectRequestReasoningToBeEmpty(request map[string]any) {
	messages, ok := request["messages"].([]any)
	ExpectWithOffset(1, ok).To(BeTrue())
	ExpectWithOffset(1, len(messages)).To(BeNumerically(">=", 2))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		ExpectWithOffset(1, ok).To(BeTrue())
		if reasoning, present := message["reasoning_content"]; present {
			ExpectWithOffset(1, reasoning).To(BeEmpty(), "role %v", message["role"])
		}
	}
}
