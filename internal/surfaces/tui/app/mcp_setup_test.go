package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
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

func TestSetupMCPRegistry_EmptyReturnsNil(t *testing.T) {
	reg, fails := mcp.SetupRegistry(context.Background(), nil, false)
	if reg != nil {
		t.Fatalf("expected nil registry, got %v", reg)
	}

	if len(fails) != 0 {
		t.Fatalf("expected no failures, got %v", fails)
	}
}

func TestSetupMCPRegistry_RegistersHTTPServer(t *testing.T) {
	srv := fakeMCPServer()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// httptest binds to 127.0.0.1; allowPrivate=true required to pass SSRF.
	reg, fails := mcp.SetupRegistry(ctx, []config.MCPServer{
		{Name: "fake", URL: srv.URL, TimeoutSeconds: 5},
	}, true)
	if reg == nil {
		t.Fatalf("expected registry, got nil")
	}

	defer reg.Close()

	if len(fails) != 0 {
		t.Fatalf("expected no failures, got %v", fails)
	}

	tools := reg.Tools()
	if _, ok := tools["mcp:fake/echo"]; !ok {
		t.Fatalf("expected mcp:fake/echo in registry, got %v", tools)
	}
}

func TestSetupMCPRegistry_SSRFBlockedReportsFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reg, fails := mcp.SetupRegistry(ctx, []config.MCPServer{
		{Name: "blocked", URL: "http://127.0.0.1:9/"},
	}, false)
	if reg == nil {
		t.Fatalf("expected non-nil registry even when all servers fail")
	}

	defer reg.Close()

	if len(fails) != 1 {
		t.Fatalf("expected 1 failure, got %v", fails)
	}

	if !strings.Contains(fails[0], "blocked") || !strings.Contains(fails[0], "loopback") {
		t.Fatalf("expected failure mentioning server name and SSRF reason, got %q", fails[0])
	}
}

func TestSetupMCPRegistry_OneFailDoesNotBlockOthers(t *testing.T) {
	srv := fakeMCPServer()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reg, fails := mcp.SetupRegistry(ctx, []config.MCPServer{
		{Name: "blocked", URL: "http://127.0.0.1:9/"},
		{Name: "fake", URL: srv.URL, TimeoutSeconds: 5},
	}, true) // allowPrivate so the httptest URL passes; the closed-port one still fails on connect
	if reg == nil {
		t.Fatalf("expected registry")
	}

	defer reg.Close()

	if len(fails) != 1 {
		t.Fatalf("expected exactly 1 failure (the blocked one), got %d: %v", len(fails), fails)
	}

	if _, ok := reg.Tools()["mcp:fake/echo"]; !ok {
		t.Fatalf("expected mcp:fake/echo to be registered even though sibling failed")
	}
}
