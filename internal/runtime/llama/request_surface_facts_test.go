package llama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

type requestSurfaceRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip requestSurfaceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return roundTrip(req)
}

var _ = ginkgo.It("preserves pre-adaptation tool facts for MLX requests", func() {
	type capturedRequest struct {
		header string
		body   map[string]any
		err    error
	}
	var captured capturedRequest
	client := NewClient("http://model.invalid", time.Second).
		WithBackend(config.BackendMLX).
		WithRequestSurfaceFacts(true)
	client.http = &http.Client{Transport: requestSurfaceRoundTripper(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		decodeErr := json.NewDecoder(req.Body).Decode(&body)
		captured = capturedRequest{header: req.Header.Get(requestSurfaceFactHeader), body: body, err: decodeErr}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]
			}`)),
		}, nil
	})}

	_, err := client.CompleteChatMessage(context.Background(), nil, ChatOptions{
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "edit_file"}},
			{Type: "function", Function: ToolFunction{Name: "source_sanity"}},
		},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(captured.err).NotTo(gomega.HaveOccurred())
	gomega.Expect(captured.body).NotTo(gomega.HaveKey("tools"))

	var fact requestSurfaceFact
	gomega.Expect(json.Unmarshal([]byte(captured.header), &fact)).To(gomega.Succeed(), "header %q", captured.header)
	gomega.Expect(fact.Transport).To(gomega.Equal("textual"))
	gomega.Expect(fact.AllowedToolCount).To(gomega.Equal(2))
	gomega.Expect(fact.AllowedToolNames).To(gomega.HaveExactElements("edit_file", "source_sanity"))
})
