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
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	reply, err := client.CompleteChat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{
		Model:             "model",
		Temperature:       0.7,
		TopP:              0.8,
		TopK:              20,
		MinP:              0.1,
		PresencePenalty:   1.2,
		RepetitionPenalty: 1.1,
		EnableThinking:    &enableThinking,
		MaxTokens:         32,
	})
	if err != nil {
		t.Fatalf("CompleteChat: %v", err)
	}
	if reply != "hello" {
		t.Fatalf("expected reply hello, got %q", reply)
	}
	if got.Model != "model" || got.Messages[0].Content != "hi" || got.Stream {
		t.Fatalf("unexpected request payload: %#v", got)
	}
	if got.ChatTemplateKwargs == nil || got.ChatTemplateKwargs.EnableThinking == nil || *got.ChatTemplateKwargs.EnableThinking {
		t.Fatalf("expected enable_thinking=false in kwargs: %#v", got.ChatTemplateKwargs)
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
