package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("sends local chat options and parses the response", func() {
	enableThinking := false

	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(r.URL.Path).To(Equal("/v1/chat/completions"))
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","reasoning_content":"hidden chain","content":"hello"}}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second)

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi", ReasoningContent: "prior hidden"}}, ChatOptions{
		Model:             "model",
		Temperature:       0.7,
		TopP:              0.8,
		TopK:              20,
		MinP:              0.1,
		PresencePenalty:   1.2,
		RepetitionPenalty: 1.1,
		EnableThinking:    &enableThinking,
		PreserveThinking:  true,
		MaxTokens:         32,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(reply.Content).To(Equal("hello"))
	Expect(reply.ReasoningContent).To(Equal("hidden chain"))
	Expect(reply.Usage.PromptTokens).To(Equal(7))
	Expect(reply.Usage.CompletionTokens).To(Equal(3))
	Expect(reply.Usage.TotalTokens).To(Equal(10))
	Expect(got.Model).To(Equal("model"))
	Expect(got.Messages).To(HaveLen(1))
	Expect(got.Messages[0].Content).To(Equal("hi"))
	Expect(got.Messages[0].ReasoningContent).To(Equal("prior hidden"))
	Expect(got.Stream).To(BeFalse())
	Expect(got.ChatTemplateKwargs).NotTo(BeNil())
	Expect(got.ChatTemplateKwargs.EnableThinking).NotTo(BeNil())
	Expect(*got.ChatTemplateKwargs.EnableThinking).To(BeFalse())
	Expect(got.ChatTemplateKwargs.PreserveThinking).To(BeTrue())
})

var _ = It("disambiguates colliding sanitized hosted tool names", func() {
	tools, reverse := normalizeHostedTools([]Tool{
		{Type: "function", Function: ToolFunction{Name: "mcp:read/file"}},
		{Type: "function", Function: ToolFunction{Name: "mcp_read/file"}},
		{Type: "function", Function: ToolFunction{Name: "mcp_read_file_2"}},
	})

	got := []string{
		tools[0].Function.Name,
		tools[1].Function.Name,
		tools[2].Function.Name,
	}
	want := []string{"mcp_read_file", "mcp_read_file_2", "mcp_read_file_2_2"}
	Expect(got).To(Equal(want))
	Expect(reverse).To(Equal(map[string]string{
		"mcp_read_file":     "mcp:read/file",
		"mcp_read_file_2":   "mcp_read/file",
		"mcp_read_file_2_2": "mcp_read_file_2",
	}))
})

var _ = It("keeps hosted tool-name mappings request scoped", func() {
	GinkgoT().Setenv("DEEPSEEK_API_KEY", "test-key")
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []Tool `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Tools) != 1 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		arrived <- struct{}{}
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "call-1",
						"type": "function",
						"function": map[string]any{
							"name":      request.Tools[0].Function.Name,
							"arguments": `{}`,
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second).WithBackend(config.BackendDeepSeek)
	type result struct {
		index   int
		message ChatMessage
		err     error
	}
	results := make(chan result, 2)
	originalNames := []string{"mcp:first/tool", "mcp:second/tool"}
	for index, name := range originalNames {
		go func() {
			message, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
				Model: "deepseek-chat",
				Tools: []Tool{{Type: "function", Function: ToolFunction{Name: name}}},
			})
			results <- result{index: index, message: message, err: err}
		}()
	}

	<-arrived
	<-arrived
	close(release)
	for range originalNames {
		got := <-results
		Expect(got.err).NotTo(HaveOccurred(), "request %d", got.index)
		Expect(got.message.ToolCalls).To(HaveLen(1), "request %d", got.index)
		Expect(got.message.ToolCalls[0].Function.Name).To(Equal(originalNames[got.index]), "request %d", got.index)
	}
})

var _ = It("parses hosted cache usage and reasoning tokens", func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":13,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":5,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":2},"total_tokens":16}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model: "deepseek-v4-flash",
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(reply.Usage.PromptTokens).To(Equal(13))
	Expect(reply.Usage.PromptCacheHitTokens).To(Equal(8))
	Expect(reply.Usage.PromptCacheMissTokens).To(Equal(5))
	Expect(reply.Usage.PromptCacheAccountingKnown).To(BeTrue())
	Expect(reply.Usage.CompletionTokens).To(Equal(3))
	Expect(reply.Usage.ReasoningTokens).To(Equal(2))
	Expect(reply.Usage.TotalTokens).To(Equal(16))
})

var _ = It("preserves known zero cache usage", func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":0,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":0,"completion_tokens":1,"total_tokens":1}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model: "deepseek-v4-flash",
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(reply.Usage.PromptCacheAccountingKnown).To(BeTrue())
	Expect(reply.Usage.PromptCacheHitTokens).To(BeZero())
	Expect(reply.Usage.PromptCacheMissTokens).To(BeZero())
})

var _ = It("leaves cache usage unknown when the backend omits it", func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model: "deepseek-v4-flash",
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(reply.Usage.PromptCacheAccountingKnown).To(BeFalse())
	Expect(reply.Usage.PromptCacheHitTokens).To(BeZero())
	Expect(reply.Usage.PromptCacheMissTokens).To(BeZero())
	Expect(reply.Usage.ReasoningTokens).To(BeZero())
})

var _ = It("classifies HTTP and empty-choice errors", func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	_, err := client.CompleteChat(context.Background(), nil, ChatOptions{})
	Expect(err).To(HaveOccurred())
	Expect(failures.CodeOf(err)).To(Equal(failures.InvalidArgument))

	emptyChoices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer emptyChoices.Close()

	client = NewClient(emptyChoices.URL, time.Second)
	_, err = client.CompleteChat(context.Background(), nil, ChatOptions{})
	Expect(err).To(HaveOccurred())
	Expect(failures.CodeOf(err)).To(Equal(failures.Internal))
})

var _ = Describe("local finish reasons", func() {
	type finishReasonCase struct {
		name    string
		backend string
		body    string
		want    string
	}
	for _, tc := range []finishReasonCase{
		{"llama-stop", config.BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "stop"},
		{"llama-length", config.BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"length"}]}`, "length"},
		{"mlx-stop", config.BackendMLX, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "stop"},
		{"mlx-length", config.BackendMLX, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"length"}]}`, "length"},
		{"omitted", config.BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, ""},
	} {
		tc := tc
		It(tc.name, func() {
			body := tc.body
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))

			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(tc.backend)

			reply, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.FinishReason).To(Equal(tc.want), "backend %s", tc.backend)
		})
	}
})

var _ = It("omits default MLX chat-template kwargs", func() {
	enableThinking := true

	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX)
	_, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Model:            "mlx-model",
		EnableThinking:   &enableThinking,
		PreserveThinking: true,
		ThinkingBudget:   2048,
		MaxTokens:        16,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(got.ChatTemplateKwargs).To(BeNil())
})

var _ = It("sends explicitly requested MLX chat-template kwargs", func() {
	enableThinking := false

	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX)
	_, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Model:              "mlx-model",
		EnableThinking:     &enableThinking,
		PreserveThinking:   true,
		ThinkingBudget:     2048,
		ChatTemplateKwargs: true,
		MaxTokens:          16,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(got.ChatTemplateKwargs).NotTo(BeNil())
	Expect(got.ChatTemplateKwargs.EnableThinking).NotTo(BeNil())
	Expect(*got.ChatTemplateKwargs.EnableThinking).To(BeFalse())
	Expect(got.ChatTemplateKwargs.PreserveThinking).To(BeTrue())
	Expect(got.ChatTemplateKwargs.ThinkingBudget).To(Equal(2048))
})

var _ = It("suppresses native MLX tool definitions by default", func() {
	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"tool\":\"read_file\",\"path\":\"README.md\"}"}}]}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX)

	_, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Model:      "mlx-model",
		MaxTokens:  32,
		ToolChoice: "auto",
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "read_file",
				Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(got.Tools).To(BeEmpty())
	Expect(got.ToolChoice).To(BeEmpty())
})

var _ = It("can force one native MLX terminal tool surface", func() {
	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"submit_plan","arguments":"{\"rationale\":\"ok\",\"edits\":[]}"}}]},"finish_reason":"tool_calls"}]}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX)

	reply, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Model:            "mlx-model",
		MaxTokens:        32,
		ToolChoice:       "required",
		ForceNativeTools: true,
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "submit_plan",
				Parameters: json.RawMessage(`{"type":"object","properties":{"rationale":{"type":"string"},"edits":{"type":"array"}}}`),
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(got.Tools).To(HaveLen(1))
	Expect(got.Tools[0].Function.Name).To(Equal("submit_plan"))
	Expect(got.ToolChoice).To(Equal("required"))
	Expect(reply.ToolCalls).To(HaveLen(1))
	Expect(reply.ToolCalls[0].Function.Name).To(Equal("submit_plan"))
})

var _ = It("keeps native tool definitions for llama", func() {
	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendLlama)

	_, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Model:      "llama-model",
		MaxTokens:  32,
		ToolChoice: "auto",
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "read_file",
				Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(got.Tools).To(HaveLen(1))
	Expect(got.ToolChoice).To(Equal("auto"))
})

var _ = It("uses ZAI hosted endpoint auth and thinking controls", func() {
	GinkgoT().Setenv("ZAI_API_KEY", "zai-test-key")

	enableThinking := false

	var (
		got  map[string]any
		auth string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(r.URL.Path).To(Equal("/chat/completions"))
		auth = r.Header.Get("Authorization")
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","reasoning_content":"hidden","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendZAI).WithAPIKeyEnv("ZAI_API_KEY")

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "assistant", Content: "prior", ReasoningContent: "do not resend"}, {Role: "user", Content: "hi"}}, ChatOptions{
		Model:          "glm-5.1",
		Temperature:    1.0,
		EnableThinking: &enableThinking,
		MaxTokens:      128,
		ToolChoice:     "auto",
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "read_file",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(auth).To(Equal("Bearer zai-test-key"))
	Expect(reply.Content).To(Equal("ok"))
	Expect(reply.ReasoningContent).To(Equal("hidden"))
	Expect(reply.Usage.TotalTokens).To(Equal(5))
	Expect(got).To(HaveKeyWithValue("model", "glm-5.1"))
	Expect(got).To(HaveKeyWithValue("stream", false))

	messages, ok := got["messages"].([]any)
	Expect(ok).To(BeTrue())
	Expect(messages).To(HaveLen(2))
	firstMessage, ok := messages[0].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(firstMessage).NotTo(HaveKey("reasoning_content"))
	Expect(got).NotTo(HaveKey("top_k"))
	Expect(got).NotTo(HaveKey("chat_template_kwargs"))

	thinking, ok := got["thinking"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(thinking).To(HaveKeyWithValue("type", "disabled"))
	tools, ok := got["tools"].([]any)
	Expect(ok).To(BeTrue())
	Expect(tools).To(HaveLen(1))
})

var _ = It("uses GLM coding endpoint auth, thinking, and reasoning effort", func() {
	GinkgoT().Setenv("ZAI_API_KEY", "glm-test-key")

	enableThinking := true

	var (
		got  map[string]any
		auth string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(r.URL.Path).To(Equal("/chat/completions"))
		auth = r.Header.Get("Authorization")
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","reasoning_content":"hidden","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).
		WithBackend(config.BackendGLM).
		WithAPIKeyEnv("ZAI_API_KEY").
		WithReasoningEffort("max")

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model:          "glm-5.2",
		EnableThinking: &enableThinking,
		MaxTokens:      128,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(auth).To(Equal("Bearer glm-test-key"))
	Expect(reply.Content).To(Equal("ok"))
	Expect(reply.ReasoningContent).To(Equal("hidden"))
	Expect(got).To(HaveKeyWithValue("model", "glm-5.2"))
	Expect(got).To(HaveKeyWithValue("stream", false))
	Expect(got).To(HaveKeyWithValue("reasoning_effort", "max"))

	thinking, ok := got["thinking"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(thinking).To(HaveKeyWithValue("type", "enabled"))
	Expect(got).NotTo(HaveKey("top_k"))
})

var _ = It("sends DeepSeek thinking controls with required tool choice", func() {
	enableThinking := false

	var got map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(r.URL.Path).To(Equal("/v1/chat/completions"))
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	_, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "submit a plan"}}, ChatOptions{
		Model:          "deepseek-v4-flash",
		EnableThinking: &enableThinking,
		ToolChoice:     "required",
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "submit_plan",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(got).To(HaveKeyWithValue("model", "deepseek-v4-flash"))
	Expect(got).To(HaveKeyWithValue("tool_choice", "required"))

	tools, ok := got["tools"].([]any)
	Expect(ok).To(BeTrue())
	Expect(tools).To(HaveLen(1))

	tool, ok := tools[0].(map[string]any)
	Expect(ok).To(BeTrue())

	function, ok := tool["function"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(function).To(HaveKeyWithValue("name", "submit_plan"))

	thinking, ok := got["thinking"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(thinking).To(HaveKeyWithValue("type", "disabled"))
})

var _ = It("converts tools and parses output through the OpenAI Responses protocol", func() {
	GinkgoT().Setenv("OPENAI_API_KEY", "openai-test-key")

	var (
		got  map[string]any
		auth string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(r.URL.Path).To(Equal("/v1/responses"))
		auth = r.Header.Get("Authorization")
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{
			"output": [
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Need the file."}]},
				{"type":"function_call","call_id":"call_read","name":"read_file","arguments":"{\"path\":\"README.md\"}"}
			],
			"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}
		}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).
		WithBackend(config.BackendCodex).
		WithAPIKeyEnv("OPENAI_API_KEY").
		WithReasoningEffort("high").
		WithVerbosity("low")

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
		{Role: "system", Content: "You are concise."},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:   "call_prior",
			Type: "function",
			Function: ToolCallFunc{
				Name:      "lookup",
				Arguments: `{"query":"x"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_prior", Content: "prior output"},
		{Role: "user", Content: "inspect readme"},
	}, ChatOptions{
		Model:      "gpt-5-codex",
		MaxTokens:  64,
		ToolChoice: "auto",
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(auth).To(Equal("Bearer openai-test-key"))
	Expect(reply.Content).To(Equal("Need the file."))
	Expect(reply.ToolCalls).To(HaveLen(1))
	Expect(reply.ToolCalls[0].Function.Name).To(Equal("read_file"))
	Expect(reply.Usage.PromptTokens).To(Equal(11))
	Expect(reply.Usage.CompletionTokens).To(Equal(7))
	Expect(reply.Usage.TotalTokens).To(Equal(18))
	Expect(got).To(HaveKeyWithValue("model", "gpt-5-codex"))
	Expect(got).To(HaveKeyWithValue("store", false))
	Expect(got).To(HaveKeyWithValue("max_output_tokens", float64(64)))

	reasoning, ok := got["reasoning"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(reasoning).To(HaveKeyWithValue("effort", "high"))

	text, ok := got["text"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(text).To(HaveKeyWithValue("verbosity", "low"))

	tools, ok := got["tools"].([]any)
	Expect(ok).To(BeTrue())
	Expect(tools).To(HaveLen(1))
	tool, ok := tools[0].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(tool).To(HaveKeyWithValue("name", "read_file"))

	input, ok := got["input"].([]any)
	Expect(ok).To(BeTrue())
	Expect(input).To(HaveLen(4))
	functionCall, ok := input[1].(map[string]any)
	Expect(ok).To(BeTrue())
	functionOutput, ok := input[2].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(functionCall).To(HaveKeyWithValue("type", "function_call"))
	Expect(functionOutput).To(HaveKeyWithValue("type", "function_call_output"))
})

var _ = It("uses Anthropic messages auth and native tool blocks", func() {
	GinkgoT().Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")

	var (
		got              map[string]any
		apiKey           string
		anthropicVersion string
		auth             string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(r.URL.Path).To(Equal("/v1/messages"))
		apiKey = r.Header.Get("x-api-key")
		anthropicVersion = r.Header.Get("anthropic-version")
		auth = r.Header.Get("Authorization")

		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())

		_, _ = w.Write([]byte(`{
			"role": "assistant",
			"content": [
				{"type": "text", "text": "Need the file."},
				{"type": "tool_use", "id": "toolu_1", "name": "read_file", "input": {"path": "README.md"}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 8, "output_tokens": 5}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).
		WithBackend(config.BackendAnthropic).
		WithAPIKeyEnv("ANTHROPIC_API_KEY")

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
		{Role: "system", Content: "Use tools carefully."},
		{Role: "user", Content: "read README"},
	}, ChatOptions{
		Model:      "claude-sonnet",
		MaxTokens:  128,
		ToolChoice: "auto",
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(apiKey).To(Equal("anthropic-test-key"))
	Expect(anthropicVersion).To(Equal("2023-06-01"))
	Expect(auth).To(BeEmpty())
	Expect(got).To(HaveKeyWithValue("model", "claude-sonnet"))
	Expect(got).To(HaveKeyWithValue("max_tokens", float64(128)))
	Expect(got).To(HaveKeyWithValue("system", "Use tools carefully."))
	messages, ok := got["messages"].([]any)
	Expect(ok).To(BeTrue())
	Expect(messages).To(HaveLen(1))
	tools, ok := got["tools"].([]any)
	Expect(ok).To(BeTrue())
	Expect(tools).To(HaveLen(1))
	Expect(reply.Content).To(Equal("Need the file."))
	Expect(reply.FinishReason).To(Equal("tool_use"))
	Expect(reply.Usage.TotalTokens).To(Equal(13))
	Expect(reply.ToolCalls).To(HaveLen(1))
	Expect(reply.ToolCalls[0].Function.Name).To(Equal("read_file"))
	Expect(reply.ToolCalls[0].Function.Arguments).To(MatchJSON(`{"path":"README.md"}`))
})

var _ = It("reports native-tool support by backend", func() {
	Expect(BackendSupportsNativeTools(config.BackendMLX)).To(BeFalse())
	Expect(BackendSupportsNativeTools(config.BackendLlama)).To(BeTrue())
	Expect(BackendSupportsNativeTools(config.BackendCodex)).To(BeTrue())
	Expect(BackendSupportsNativeTools(config.BackendZAI)).To(BeTrue())
	Expect(BackendSupportsNativeTools(config.BackendGLM)).To(BeTrue())
	Expect(BackendSupportsNativeTools(config.BackendAnthropic)).To(BeTrue())
	Expect(NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama).SupportsNativeTools()).To(BeTrue())
	Expect(NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX).SupportsNativeTools()).To(BeFalse())
})

var _ = Describe("structured-output capabilities", func() {
	type structuredOutputCase struct {
		backend         string
		nativeTools     bool
		jsonObject      bool
		thinkingControl bool
	}
	for _, tc := range []structuredOutputCase{
		{config.BackendLlama, true, true, true},
		{config.BackendMLX, false, true, true},
		{config.BackendCodex, true, true, false},
		{config.BackendZAI, true, true, true},
		{config.BackendGLM, true, true, true},
		{config.BackendDeepSeek, true, true, true},
		{config.BackendAnthropic, true, false, false},
	} {
		tc := tc
		It("stays conservative for "+tc.backend, func() {
			caps := BackendStructuredOutputCapabilities(tc.backend)
			Expect(caps.NativeTools).To(Equal(tc.nativeTools))
			Expect(caps.JSONObject).To(Equal(tc.jsonObject))
			Expect(caps.ThinkingControls).To(Equal(tc.thinkingControl))
			Expect(caps.StrictJSONSchema).To(BeFalse())
			Expect(caps.Grammar).To(BeFalse())
			Expect(caps.RequestTimeoutTelemetry).To(BeFalse())
		})
	}
})

var _ = It("normalizes backend values", func() {
	c := NewClient("http://example.invalid", time.Second)
	Expect(c.Backend()).To(Equal(config.BackendLlama))

	c.WithBackend("MLX")
	Expect(c.Backend()).To(Equal(config.BackendMLX))

	c.WithBackend("codex")
	Expect(c.Backend()).To(Equal(config.BackendCodex))

	c.WithBackend("zai")
	Expect(c.Backend()).To(Equal(config.BackendZAI))

	c.WithBackend("glm")
	Expect(c.Backend()).To(Equal(config.BackendGLM))

	c.WithBackend("unknown")
	Expect(c.Backend()).To(Equal(config.BackendLlama))
})

var _ = It("retries only transient model-loading responses", func() {
	Expect(shouldRetryLoadingModel(http.StatusServiceUnavailable, "loading model")).To(BeTrue())
	Expect(shouldRetryLoadingModel(http.StatusServiceUnavailable, "unavailable_error: loading")).To(BeTrue())
	Expect(shouldRetryLoadingModel(http.StatusBadRequest, "loading model")).To(BeFalse())
})

var _ = Describe("repairMessageToolPairing", func() {
	It("adds a synthetic response for an orphaned tool call", func() {
		messages := []ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "call_1", Function: ToolCallFunc{Name: "read_file"}},
				},
			},
		}
		repaired := repairMessageToolPairing(messages)
		Expect(repaired).To(HaveLen(4))
		last := repaired[len(repaired)-1]
		Expect(last.Role).To(Equal("tool"))
		Expect(last.ToolCallID).To(Equal("call_1"))
		Expect(last.Content).NotTo(BeEmpty())
	})

	It("keeps real responses and adds only missing responses for mixed calls", func() {
		messages := []ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "call_1", Function: ToolCallFunc{Name: "read_file"}},
					{ID: "call_2", Function: ToolCallFunc{Name: "search_content"}},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "file contents here"},
		}
		repaired := repairMessageToolPairing(messages)
		Expect(repaired).To(HaveLen(5))

		var realTool, syntheticTool *ChatMessage
		for i := range repaired {
			if repaired[i].Role != "tool" {
				continue
			}
			switch repaired[i].ToolCallID {
			case "call_1":
				realTool = &repaired[i]
			case "call_2":
				syntheticTool = &repaired[i]
			}
		}

		Expect(realTool).NotTo(BeNil())
		Expect(realTool.Content).To(Equal("file contents here"))
		Expect(syntheticTool).NotTo(BeNil())
		Expect(syntheticTool.Content).NotTo(BeEmpty())
	})

	It("does not add messages when all calls are paired", func() {
		messages := []ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "call_1", Function: ToolCallFunc{Name: "read_file"}},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "result"},
		}
		repaired := repairMessageToolPairing(messages)
		Expect(repaired).To(HaveLen(4))
	})

	It("leaves history without tool calls unchanged", func() {
		messages := []ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there!"},
		}
		repaired := repairMessageToolPairing(messages)
		Expect(repaired).To(Equal(messages))
	})

	It("skips tool calls with empty IDs", func() {
		messages := []ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "", Function: ToolCallFunc{Name: "unknown"}},
				},
			},
		}
		repaired := repairMessageToolPairing(messages)
		Expect(repaired).To(HaveLen(3))
	})
})

var _ = It("repairs orphaned tool calls before sending a request", func() {
	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		Expect(json.NewDecoder(r.Body).Decode(&got)).To(Succeed())
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"final answer"}}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	_, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
		{Role: "user", Content: "test"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:       "call_dangling_1",
			Type:     "function",
			Function: ToolCallFunc{Name: "search_content", Arguments: `{"pattern":"test"}`},
		}}},
		{Role: "system", Content: "Tools are now disabled. Answer from gathered evidence."},
	}, ChatOptions{Model: "model"})
	Expect(err).NotTo(HaveOccurred())
	Expect(got.Messages).To(HaveLen(4))
	Expect(got.Messages[2].Role).To(Equal("tool"))
	Expect(got.Messages[2].ToolCallID).To(Equal("call_dangling_1"))
	Expect(got.Messages[3].Role).To(Equal("system"))
})
