package llama

import (
	"context"
	"testing"
)

func TestLLMClientInterface(t *testing.T) {
	var _ LLMClient = (*Client)(nil)
}

type mockClient struct {
	completeChatFn        func(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error)
	completeChatMessageFn func(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error)
	streamChatFn          func(ctx context.Context, messages []ChatMessage, opts ChatOptions, onDelta func(string) error) error
	listModelsFn          func(ctx context.Context) ([]ModelInfo, error)
	backendFn             func() string
	supportsNativeToolsFn func() bool
}

func (m *mockClient) CompleteChat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error) {
	if m.completeChatFn != nil {
		return m.completeChatFn(ctx, messages, opts)
	}
	return "", nil
}

func (m *mockClient) CompleteChatMessage(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error) {
	if m.completeChatMessageFn != nil {
		return m.completeChatMessageFn(ctx, messages, opts)
	}
	return ChatMessage{}, nil
}

func (m *mockClient) StreamChat(ctx context.Context, messages []ChatMessage, opts ChatOptions, onDelta func(string) error) error {
	if m.streamChatFn != nil {
		return m.streamChatFn(ctx, messages, opts, onDelta)
	}
	return nil
}

func (m *mockClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if m.listModelsFn != nil {
		return m.listModelsFn(ctx)
	}
	return nil, nil
}

func (m *mockClient) Backend() string {
	if m.backendFn != nil {
		return m.backendFn()
	}
	return ""
}

func (m *mockClient) SupportsNativeTools() bool {
	if m.supportsNativeToolsFn != nil {
		return m.supportsNativeToolsFn()
	}
	return true
}

func TestMockImplementsLLMClient(t *testing.T) {
	var _ LLMClient = &mockClient{}
}

func TestMockClientCompleteChat(t *testing.T) {
	client := &mockClient{
		completeChatFn: func(_ context.Context, _ []ChatMessage, _ ChatOptions) (string, error) {
			return "mock response", nil
		},
	}
	var lc LLMClient = client
	reply, err := lc.CompleteChat(context.Background(), nil, ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "mock response" {
		t.Fatalf("expected %q, got %q", "mock response", reply)
	}
}

func TestMockClientListModels(t *testing.T) {
	client := &mockClient{
		listModelsFn: func(_ context.Context) ([]ModelInfo, error) {
			return []ModelInfo{{ID: "test-model"}}, nil
		},
	}
	var lc LLMClient = client
	models, err := lc.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "test-model" {
		t.Fatalf("expected [test-model], got %v", models)
	}
}
