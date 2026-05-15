package llama

import (
	"time"

	"idiom/internal/config"
)

// NewClientForServer builds a chat client from the shared backend spec used by
// the app and lane runners. Local backends get plain HTTP clients; hosted
// backends get endpoint, API-key-env, and provider option wiring in one place.
func NewClientForServer(spec config.LlamaServer, timeout time.Duration) *Client {
	return NewClient(spec.EndpointBaseURL(), timeout).
		WithBackend(spec.Kind).
		WithAPIKeyEnv(spec.APIKeyEnvName()).
		WithReasoningEffort(spec.ReasoningEffort).
		WithVerbosity(spec.Verbosity)
}
