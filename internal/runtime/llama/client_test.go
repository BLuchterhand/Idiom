package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
)

func TestCompleteChatSendsOptionsAndParsesResponse(t *testing.T) {
	enableThinking := false

	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChat: %v", err)
	}

	if reply.Content != "hello" || reply.ReasoningContent != "hidden chain" {
		t.Fatalf("expected reply with preserved reasoning, got %#v", reply)
	}

	if reply.Usage.PromptTokens != 7 || reply.Usage.CompletionTokens != 3 || reply.Usage.TotalTokens != 10 {
		t.Fatalf("expected usage to be parsed, got %#v", reply.Usage)
	}

	if got.Model != "model" || got.Messages[0].Content != "hi" || got.Messages[0].ReasoningContent != "prior hidden" || got.Stream {
		t.Fatalf("unexpected request payload: %#v", got)
	}

	if got.ChatTemplateKwargs == nil || got.ChatTemplateKwargs.EnableThinking == nil || *got.ChatTemplateKwargs.EnableThinking {
		t.Fatalf("expected enable_thinking=false in kwargs: %#v", got.ChatTemplateKwargs)
	}

	if !got.ChatTemplateKwargs.PreserveThinking {
		t.Fatalf("expected preserve_thinking=true in kwargs: %#v", got.ChatTemplateKwargs)
	}
}

func TestNormalizeHostedToolsDisambiguatesSanitizedNameCollisions(t *testing.T) {
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
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized tool names = %v, want %v", got, want)
		}
		if reverse[got[i]] != []string{"mcp:read/file", "mcp_read/file", "mcp_read_file_2"}[i] {
			t.Fatalf("reverse mapping for %q = %q", got[i], reverse[got[i]])
		}
	}
}

func TestCompleteChatKeepsToolNameMappingRequestScoped(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
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
		if got.err != nil {
			t.Fatalf("request %d failed: %v", got.index, got.err)
		}
		if len(got.message.ToolCalls) != 1 || got.message.ToolCalls[0].Function.Name != originalNames[got.index] {
			t.Fatalf("request %d tool calls = %#v, want name %q", got.index, got.message.ToolCalls, originalNames[got.index])
		}
	}
}

func TestCompleteChatParsesHostedCacheUsageAndReasoningTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":13,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":5,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":2},"total_tokens":16}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model: "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if reply.Usage.PromptTokens != 13 ||
		reply.Usage.PromptCacheHitTokens != 8 ||
		reply.Usage.PromptCacheMissTokens != 5 ||
		!reply.Usage.PromptCacheAccountingKnown ||
		reply.Usage.CompletionTokens != 3 ||
		reply.Usage.ReasoningTokens != 2 ||
		reply.Usage.TotalTokens != 16 {

		t.Fatalf("unexpected usage: %#v", reply.Usage)
	}
}

func TestCompleteChatPreservesKnownZeroCacheUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":0,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":0,"completion_tokens":1,"total_tokens":1}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model: "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if !reply.Usage.PromptCacheAccountingKnown ||
		reply.Usage.PromptCacheHitTokens != 0 ||
		reply.Usage.PromptCacheMissTokens != 0 {

		t.Fatalf("expected known zero cache usage, got %#v", reply.Usage)
	}
}

func TestCompleteChatLeavesCacheUsageUnknownWhenOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model: "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if reply.Usage.PromptCacheAccountingKnown ||
		reply.Usage.PromptCacheHitTokens != 0 ||
		reply.Usage.PromptCacheMissTokens != 0 ||
		reply.Usage.ReasoningTokens != 0 {

		t.Fatalf("expected omitted cache usage to remain unknown, got %#v", reply.Usage)
	}
}

func TestCompleteChatReportsHTTPAndEmptyChoiceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	if _, err := client.CompleteChat(context.Background(), nil, ChatOptions{}); err == nil {
		t.Fatal("expected HTTP error")
	} else if got := failures.CodeOf(err); got != failures.InvalidArgument {
		t.Fatalf("HTTP error code = %v, want %v", got, failures.InvalidArgument)
	}

	emptyChoices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer emptyChoices.Close()

	client = NewClient(emptyChoices.URL, time.Second)
	if _, err := client.CompleteChat(context.Background(), nil, ChatOptions{}); err == nil {
		t.Fatal("expected empty choices error")
	} else if got := failures.CodeOf(err); got != failures.Internal {
		t.Fatalf("empty choices code = %v, want %v", got, failures.Internal)
	}
}

func TestCompleteChatCapturesFinishReasonFromBothBackends(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend string
		body    string
		want    string
	}{
		{"llama-stop", config.BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "stop"},
		{"llama-length", config.BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"length"}]}`, "length"},
		{"mlx-stop", config.BackendMLX, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "stop"},
		{"mlx-length", config.BackendMLX, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"length"}]}`, "length"},
		{"omitted", config.BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))

			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(tc.backend)

			reply, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{Model: "m"})
			if err != nil {
				t.Fatalf("CompleteChatMessage: %v", err)
			}

			if reply.FinishReason != tc.want {
				t.Fatalf("backend=%s: expected finish_reason=%q, got %q", tc.backend, tc.want, reply.FinishReason)
			}
		})
	}
}

func TestMLXBackendOmitsDefaultChatTemplateKwargs(t *testing.T) {
	enableThinking := true

	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX)
	if _, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Model:            "mlx-model",
		EnableThinking:   &enableThinking,
		PreserveThinking: true,
		ThinkingBudget:   2048,
		MaxTokens:        16,
	}); err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if got.ChatTemplateKwargs != nil {
		t.Fatalf("expected default MLX request to omit chat_template_kwargs, got %#v", got.ChatTemplateKwargs)
	}
}

func TestMLXBackendSendsExplicitChatTemplateKwargs(t *testing.T) {
	enableThinking := false

	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX)
	if _, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Model:              "mlx-model",
		EnableThinking:     &enableThinking,
		PreserveThinking:   true,
		ThinkingBudget:     2048,
		ChatTemplateKwargs: true,
		MaxTokens:          16,
	}); err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if got.ChatTemplateKwargs == nil {
		t.Fatalf("expected chat_template_kwargs for MLX backend")
	}

	if got.ChatTemplateKwargs.EnableThinking == nil || *got.ChatTemplateKwargs.EnableThinking {
		t.Fatalf("expected enable_thinking=false in MLX kwargs, got %#v", got.ChatTemplateKwargs)
	}

	if !got.ChatTemplateKwargs.PreserveThinking {
		t.Fatalf("expected preserve_thinking=true in MLX kwargs, got %#v", got.ChatTemplateKwargs)
	}

	if got.ChatTemplateKwargs.ThinkingBudget != 2048 {
		t.Fatalf("expected thinking_budget=2048 in MLX kwargs, got %#v", got.ChatTemplateKwargs)
	}
}

func TestMLXBackendIncludesNativeToolDefinitions(t *testing.T) {
	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if len(got.Tools) > 0 || got.ToolChoice != "" {
		t.Fatalf("MLX requests should NOT pass native tools to the backend, got tools=%#v choice=%q", got.Tools, got.ToolChoice)
	}
}

func TestMLXBackendCanForceSingleNativeToolSurface(t *testing.T) {
	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "submit_plan" || got.ToolChoice != "required" {
		t.Fatalf("forced MLX terminal request should preserve one native tool, got tools=%#v choice=%q", got.Tools, got.ToolChoice)
	}

	if len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Function.Name != "submit_plan" {
		t.Fatalf("expected parsed native tool call, got %#v", reply.ToolCalls)
	}
}

func TestLlamaBackendKeepsNativeToolDefinitions(t *testing.T) {
	var got chatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if len(got.Tools) != 1 || got.ToolChoice != "auto" {
		t.Fatalf("llama requests should preserve native tools, got tools=%#v choice=%q", got.Tools, got.ToolChoice)
	}
}

func TestZAIBackendUsesHostedChatEndpointAuthAndThinking(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "zai-test-key")

	enableThinking := false

	var (
		got  map[string]any
		auth string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if auth != "Bearer zai-test-key" {
		t.Fatalf("expected bearer auth, got %q", auth)
	}

	if reply.Content != "ok" || reply.ReasoningContent != "hidden" || reply.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected reply: %#v", reply)
	}

	if got["model"] != "glm-5.1" || got["stream"] != false {
		t.Fatalf("unexpected model/stream payload: %#v", got)
	}

	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("expected messages in payload, got %#v", got["messages"])
	}

	if _, ok := messages[0].(map[string]any)["reasoning_content"]; ok {
		t.Fatalf("hosted chat payload should not resend reasoning_content, got %#v", messages[0])
	}

	if _, ok := got["top_k"]; ok {
		t.Fatalf("hosted ZAI payload should omit local top_k, got %#v", got)
	}

	if _, ok := got["chat_template_kwargs"]; ok {
		t.Fatalf("hosted ZAI payload should omit chat_template_kwargs, got %#v", got)
	}

	thinking, ok := got["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("expected thinking disabled payload, got %#v", got["thinking"])
	}

	if tools, ok := got["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("expected native tool definitions, got %#v", got["tools"])
	}
}

func TestGLMBackendUsesCodingEndpointAuthThinkingAndReasoningEffort(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "glm-test-key")

	enableThinking := true

	var (
		got  map[string]any
		auth string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if auth != "Bearer glm-test-key" {
		t.Fatalf("expected bearer auth, got %q", auth)
	}

	if reply.Content != "ok" || reply.ReasoningContent != "hidden" {
		t.Fatalf("unexpected reply: %#v", reply)
	}

	if got["model"] != "glm-5.2" || got["stream"] != false || got["reasoning_effort"] != "max" {
		t.Fatalf("unexpected GLM payload basics: %#v", got)
	}

	thinking, ok := got["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("expected GLM thinking enabled payload, got %#v", got["thinking"])
	}

	if _, ok := got["top_k"]; ok {
		t.Fatalf("hosted GLM payload should omit local top_k, got %#v", got)
	}
}

func TestDeepSeekBackendSendsThinkingToggleWithRequiredToolChoice(t *testing.T) {
	enableThinking := false

	var got map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if got["model"] != "deepseek-v4-flash" || got["tool_choice"] != "required" {
		t.Fatalf("unexpected DeepSeek request model/tool_choice: %#v", got)
	}

	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one DeepSeek native tool definition, got %#v", got["tools"])
	}

	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected tool object, got %#v", tools[0])
	}

	function, ok := tool["function"].(map[string]any)
	if !ok || function["name"] != "submit_plan" {
		t.Fatalf("expected submit_plan tool definition, got %#v", tool)
	}

	thinking, ok := got["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("expected DeepSeek thinking disabled payload, got %#v", got["thinking"])
	}
}

func TestOpenAIResponsesBackendConvertsToolsAndParsesOutput(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-test-key")

	var (
		got  map[string]any
		auth string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if auth != "Bearer openai-test-key" {
		t.Fatalf("expected bearer auth, got %q", auth)
	}

	if reply.Content != "Need the file." || len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected responses reply: %#v", reply)
	}

	if reply.Usage.PromptTokens != 11 || reply.Usage.CompletionTokens != 7 || reply.Usage.TotalTokens != 18 {
		t.Fatalf("unexpected usage: %#v", reply.Usage)
	}

	if got["model"] != "gpt-5-codex" || got["store"] != false || got["max_output_tokens"].(float64) != 64 {
		t.Fatalf("unexpected responses payload basics: %#v", got)
	}

	reasoning, ok := got["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("expected reasoning effort, got %#v", got["reasoning"])
	}

	text, ok := got["text"].(map[string]any)
	if !ok || text["verbosity"] != "low" {
		t.Fatalf("expected text verbosity, got %#v", got["text"])
	}

	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 || tools[0].(map[string]any)["name"] != "read_file" {
		t.Fatalf("expected Responses function tools, got %#v", got["tools"])
	}

	input, ok := got["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("expected converted input items, got %#v", got["input"])
	}

	if input[1].(map[string]any)["type"] != "function_call" || input[2].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("expected function_call/function_call_output input items, got %#v", got["input"])
	}
}

func TestAnthropicBackendUsesMessagesEndpointAuthAndNativeToolBlocks(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")

	var (
		got              map[string]any
		apiKey           string
		anthropicVersion string
		auth             string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		apiKey = r.Header.Get("x-api-key")
		anthropicVersion = r.Header.Get("anthropic-version")
		auth = r.Header.Get("Authorization")

		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if apiKey != "anthropic-test-key" || anthropicVersion != "2023-06-01" || auth != "" {
		t.Fatalf("unexpected Anthropic auth headers: x-api-key=%q anthropic-version=%q authorization=%q", apiKey, anthropicVersion, auth)
	}

	if got["model"] != "claude-sonnet" || got["max_tokens"] != float64(128) || got["system"] != "Use tools carefully." {
		t.Fatalf("unexpected Anthropic request basics: %#v", got)
	}

	if messages, ok := got["messages"].([]any); !ok || len(messages) != 1 {
		t.Fatalf("expected one non-system message, got %#v", got["messages"])
	}

	if tools, ok := got["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("expected Anthropic tool definition, got %#v", got["tools"])
	}

	if reply.Content != "Need the file." || reply.FinishReason != "tool_use" || reply.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected Anthropic reply: %#v", reply)
	}

	if len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Function.Name != "read_file" || reply.ToolCalls[0].Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected Anthropic tool call: %#v", reply.ToolCalls)
	}
}

func TestBackendSupportsNativeTools(t *testing.T) {
	if BackendSupportsNativeTools(config.BackendMLX) {
		t.Fatal("MLX should NOT support native tools")
	}

	if !BackendSupportsNativeTools(config.BackendLlama) {
		t.Fatal("llama backend should keep native tools")
	}

	if !BackendSupportsNativeTools(config.BackendCodex) || !BackendSupportsNativeTools(config.BackendZAI) || !BackendSupportsNativeTools(config.BackendGLM) || !BackendSupportsNativeTools(config.BackendAnthropic) {
		t.Fatal("hosted backends should keep native tools")
	}

	if !NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama).SupportsNativeTools() {
		t.Fatal("llama client should report native tool support")
	}

	if NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX).SupportsNativeTools() {
		t.Fatal("MLX client should NOT report native tool support")
	}
}

func TestBackendStructuredOutputCapabilitiesAreConservative(t *testing.T) {
	for _, tc := range []struct {
		backend         string
		nativeTools     bool
		jsonObject      bool
		thinkingControl bool
	}{
		{config.BackendLlama, true, true, true},
		{config.BackendMLX, false, true, true},
		{config.BackendCodex, true, true, false},
		{config.BackendZAI, true, true, true},
		{config.BackendGLM, true, true, true},
		{config.BackendDeepSeek, true, true, true},
		{config.BackendAnthropic, true, false, false},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			caps := BackendStructuredOutputCapabilities(tc.backend)
			if caps.NativeTools != tc.nativeTools {
				t.Fatalf("NativeTools = %v, want %v", caps.NativeTools, tc.nativeTools)
			}

			if caps.JSONObject != tc.jsonObject {
				t.Fatalf("JSONObject = %v, want %v", caps.JSONObject, tc.jsonObject)
			}

			if caps.ThinkingControls != tc.thinkingControl {
				t.Fatalf("ThinkingControls = %v, want %v", caps.ThinkingControls, tc.thinkingControl)
			}

			if caps.StrictJSONSchema || caps.Grammar || caps.RequestTimeoutTelemetry {
				t.Fatalf("capabilities should not claim unenforced strict schema/grammar/timeout telemetry: %#v", caps)
			}
		})
	}
}

func TestWithBackendNormalizesValue(t *testing.T) {
	c := NewClient("http://example.invalid", time.Second)
	if c.Backend() != config.BackendLlama {
		t.Fatalf("expected default backend llama, got %q", c.Backend())
	}

	c.WithBackend("MLX")

	if c.Backend() != config.BackendMLX {
		t.Fatalf("expected mlx after WithBackend(MLX), got %q", c.Backend())
	}

	c.WithBackend("codex")

	if c.Backend() != config.BackendCodex {
		t.Fatalf("expected codex after WithBackend(codex), got %q", c.Backend())
	}

	c.WithBackend("zai")

	if c.Backend() != config.BackendZAI {
		t.Fatalf("expected zai after WithBackend(zai), got %q", c.Backend())
	}

	c.WithBackend("glm")

	if c.Backend() != config.BackendGLM {
		t.Fatalf("expected glm after WithBackend(glm), got %q", c.Backend())
	}

	c.WithBackend("unknown")

	if c.Backend() != config.BackendLlama {
		t.Fatalf("expected fallback to llama for unknown backend, got %q", c.Backend())
	}
}

func TestShouldRetryLoadingModel(t *testing.T) {
	if !shouldRetryLoadingModel(http.StatusServiceUnavailable, "loading model") {
		t.Fatal("expected loading model 503 to retry")
	}

	if !shouldRetryLoadingModel(http.StatusServiceUnavailable, "unavailable_error: loading") {
		t.Fatal("expected unavailable_error 503 to retry")
	}

	if shouldRetryLoadingModel(http.StatusBadRequest, "loading model") {
		t.Fatal("400 should not retry")
	}
}

func TestRepairMessageToolPairing_OrphanedToolCalls(t *testing.T) {
	t.Run("orphaned tool_call gets synthetic tool response", func(t *testing.T) {
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
		if len(repaired) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(repaired))
		}

		last := repaired[len(repaired)-1]
		if last.Role != "tool" {
			t.Fatalf("expected last message role 'tool', got %q", last.Role)
		}
		if last.ToolCallID != "call_1" {
			t.Fatalf("expected ToolCallID 'call_1', got %q", last.ToolCallID)
		}
		if last.Content == "" {
			t.Fatal("expected non-empty synthetic content")
		}
	})

	t.Run("mixed paired and orphaned calls", func(t *testing.T) {
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
		if len(repaired) != 5 {
			t.Fatalf("expected 5 messages, got %d", len(repaired))
		}

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

		if realTool == nil {
			t.Fatal("missing real tool response for call_1")
		}
		if realTool.Content != "file contents here" {
			t.Fatalf("real tool response content mismatch: %q", realTool.Content)
		}
		if syntheticTool == nil {
			t.Fatal("missing synthetic tool response for orphaned call_2")
		}
		if syntheticTool.Content == "" {
			t.Fatal("synthetic tool response has empty content")
		}
	})

	t.Run("no orphaned calls is a no-op", func(t *testing.T) {
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
		if len(repaired) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(repaired))
		}
	})

	t.Run("no tool_calls in messages is unchanged", func(t *testing.T) {
		messages := []ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there!"},
		}
		repaired := repairMessageToolPairing(messages)
		if len(repaired) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(repaired))
		}
	})

	t.Run("empty tool_call ID is skipped", func(t *testing.T) {
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
		if len(repaired) != 3 {
			t.Fatalf("expected 3 messages (empty IDs skipped), got %d", len(repaired))
		}
	})
}

func TestCompleteChatMessageRepairsOrphanedToolCallsBeforeRequest(t *testing.T) {
	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
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
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	if len(got.Messages) != 4 {
		t.Fatalf("messages = %#v, want synthetic tool response inserted", got.Messages)
	}
	if got.Messages[2].Role != "tool" || got.Messages[2].ToolCallID != "call_dangling_1" {
		t.Fatalf("message after assistant tool_calls must be paired tool response, got %#v", got.Messages)
	}
	if got.Messages[3].Role != "system" {
		t.Fatalf("force-answer system message should remain after pairing repair, got %#v", got.Messages)
	}
}
