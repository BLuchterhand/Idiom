package llama

import "context"

type LLMClient interface {
	CompleteChat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error)
	CompleteChatMessage(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error)
	StreamChat(ctx context.Context, messages []ChatMessage, opts ChatOptions, onDelta func(string) error) error
	ListModels(ctx context.Context) ([]ModelInfo, error)
	Backend() string
	SupportsNativeTools() bool
}

var _ LLMClient = (*Client)(nil)
