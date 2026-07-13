package llama

import "context"

//go:generate go tool mockgen -source=interface.go -destination=llamamocks/mock_llm_client.go -package=llamamocks -typed

type LLMClient interface {
	CompleteChat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error)
	CompleteChatMessage(ctx context.Context, messages []ChatMessage, opts ChatOptions) (ChatMessage, error)
	StreamChat(ctx context.Context, messages []ChatMessage, opts ChatOptions, onDelta func(string) error) error
	ListModels(ctx context.Context) ([]ModelInfo, error)
	Backend() string
	SupportsNativeTools() bool
}

var _ LLMClient = (*Client)(nil)
