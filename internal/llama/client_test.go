package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestCompleteChatReportsHTTPAndEmptyChoiceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()
	client := NewClient(server.URL, time.Second)
	if _, err := client.CompleteChat(context.Background(), nil, ChatOptions{}); err == nil {
		t.Fatal("expected HTTP error")
	}

	emptyChoices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer emptyChoices.Close()
	client = NewClient(emptyChoices.URL, time.Second)
	if _, err := client.CompleteChat(context.Background(), nil, ChatOptions{}); err == nil {
		t.Fatal("expected empty choices error")
	}
}

func TestCompleteChatCapturesFinishReasonFromBothBackends(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend string
		body    string
		want    string
	}{
		{"llama-stop", BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "stop"},
		{"llama-length", BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"length"}]}`, "length"},
		{"mlx-stop", BackendMLX, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "stop"},
		{"mlx-length", BackendMLX, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"length"}]}`, "length"},
		{"omitted", BackendLlama, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, ""},
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

	client := NewClient(server.URL, time.Second).WithBackend(BackendMLX)
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

	client := NewClient(server.URL, time.Second).WithBackend(BackendMLX)
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

func TestWithBackendNormalizesValue(t *testing.T) {
	c := NewClient("http://example.invalid", time.Second)
	if c.Backend() != BackendLlama {
		t.Fatalf("expected default backend llama, got %q", c.Backend())
	}
	c.WithBackend("MLX")
	if c.Backend() != BackendMLX {
		t.Fatalf("expected mlx after WithBackend(MLX), got %q", c.Backend())
	}
	c.WithBackend("unknown")
	if c.Backend() != BackendLlama {
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
