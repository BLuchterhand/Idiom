package llama

import (
	"os"
	"strings"
	"time"

	"idiom/internal/platform/config"
)

// NewClientForServer builds a chat client from the shared backend spec used by
// the app and lane runners. Local backends get plain HTTP clients; hosted
// backends get endpoint, API-key-env, and provider option wiring in one place.
func NewClientForServer(spec config.LlamaServer, timeout time.Duration) *Client {
	client := NewClient(spec.EndpointBaseURL(), timeout).
		WithBackend(spec.Kind).
		WithModel(spec.Model).
		WithAPIKeyEnv(spec.APIKeyEnvName()).
		WithReasoningEffort(spec.ReasoningEffort).
		WithVerbosity(spec.Verbosity)
	client.WithRequestSurfaceFacts(strings.TrimSpace(os.Getenv("IDIOM_SCRIPTED_REQUEST_SURFACE_FACTS")) == "1")
	return client
}
