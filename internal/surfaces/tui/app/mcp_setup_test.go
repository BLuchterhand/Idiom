package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

// fakeMCPServer responds to the three method calls the registry makes
// during RegisterExisting: initialize, tools/list, and the unanswered
// notifications/initialized. CallTool isn't exercised by setup itself.
func fakeMCPServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "text/event-stream")

		switch {
		case strings.Contains(string(body), `"method":"initialize"`):
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		case strings.Contains(string(body), `"method":"tools/list"`):
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"echo back"}]}}`)
		default:
			// notifications/initialized — no reply.
		}
	}))
}

var _ = ginkgo.It("setup mcp registry empty returns nil", func() {
	reg, fails := mcp.SetupRegistry(context.Background(), nil, false)
	gomega.Expect(reg).To(gomega.BeNil(), fmt.Sprintf("expected nil registry, got %v", reg))

	gomega.Expect(len(fails)).To(gomega.Equal(0), fmt.Sprintf("expected no failures, got %v", fails))
})

var _ = ginkgo.It("setup mcp registry registers http server", func() {
	srv := fakeMCPServer()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// httptest binds to 127.0.0.1; allowPrivate=true required to pass SSRF.
	reg, fails := mcp.SetupRegistry(ctx, []config.MCPServer{
		{Name: "fake", URL: srv.URL, TimeoutSeconds: 5},
	}, true)
	gomega.Expect(reg).NotTo(gomega.BeNil(), fmt.Sprintf("expected registry, got nil"))

	defer reg.Close()

	gomega.Expect(len(fails)).To(gomega.Equal(0), fmt.Sprintf("expected no failures, got %v", fails))

	tools := reg.Tools()
	{
		_, ok := tools["mcp:fake/echo"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected mcp:fake/echo in registry, got %v", tools))
	}
})

var _ = ginkgo.It("setup mcp registry ssrf blocked reports failure", func() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reg, fails := mcp.SetupRegistry(ctx, []config.MCPServer{
		{Name: "blocked", URL: "http://127.0.0.1:9/"},
	}, false)
	gomega.Expect(reg).NotTo(gomega.BeNil(), fmt.Sprintf("expected non-nil registry even when all servers fail"))

	defer reg.Close()

	gomega.Expect(len(fails)).To(gomega.Equal(1), fmt.Sprintf("expected 1 failure, got %v", fails))

	gomega.Expect(fails[0]).To(gomega.ContainSubstring("blocked"), fmt.Sprintf("expected failure mentioning server name and SSRF reason, got %q", fails[0]))
	gomega.Expect(fails[0]).To(gomega.ContainSubstring("loopback"), fmt.Sprintf("expected failure mentioning server name and SSRF reason, got %q", fails[0]))
})

var _ = ginkgo.It("setup mcp registry one fail does not block others", func() {
	srv := fakeMCPServer()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reg, fails := mcp.SetupRegistry(ctx, []config.MCPServer{
		{Name: "blocked", URL: "http://127.0.0.1:9/"},
		{Name: "fake", URL: srv.URL, TimeoutSeconds: 5},
	}, true) // allowPrivate so the httptest URL passes; the closed-port one still fails on connect
	gomega.Expect(reg).NotTo(gomega.BeNil(), fmt.Sprintf("expected registry"))

	defer reg.Close()

	gomega.Expect(len(fails)).To(gomega.Equal(1), fmt.Sprintf("expected exactly 1 failure (the blocked one), got %d: %v", len(fails), fails))

	{
		_, ok := reg.Tools()["mcp:fake/echo"]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected mcp:fake/echo to be registered even though sibling failed"))
	}
})
