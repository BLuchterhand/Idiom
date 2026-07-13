package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("toolcmd", func() {
	ginkgo.It("tool list includes configured mcp web search", func() {
		srv := fakeToolMCPServer()
		defer srv.Close()

		cfgPath := writeToolMCPConfig(srv.URL)

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{"list", "--config", cfgPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("tool list exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("mcp:exa/web_search_exa"), fmt.Sprintf("expected configured MCP web search in list, got:\n%s", stdout.String()))

	})
	ginkgo.It("tool discovery list does not initialize mcp without explicit allow rule", func() {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			http.Error(w, "mcp should not be contacted for denied discovery list", http.StatusInternalServerError)
		}))
		defer srv.Close()

		cfgPath := writeToolMCPConfigWithoutRules(srv.URL)

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{"list", "--config", cfgPath, "--profile", "discovery"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("tool list exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		if got := calls.Load(); got != 0 {
			ginkgo.Fail(fmt.Sprintf("discovery list without MCP allow rules should not initialize MCP, got %d request(s)", got))
		}
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("mcp:"), fmt.Sprintf("discovery list should not advertise denied MCP tools:\n%s", stdout.String()))

	})
	ginkgo.It("direct tool output includes timeout owner", func() {
		root := ginkgo.GinkgoT().TempDir()
		cfg := config.Default()
		cfg.Workspace.Root = root
		cfg.Workspace.CWD = root

		data, err := json.Marshal(cfg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

		cfgPath := filepath.Join(root, "config.json")
		gomega.Expect(os.WriteFile(cfgPath, data, 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{"pwd", "--config", cfgPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("tool pwd exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		var payload struct {
			TimeoutOwner string `json:"timeout_owner"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.TimeoutOwner).To(gomega.Equal("workspace_tool_timeout_with_cli_margin"), fmt.Sprintf("timeout_owner = %q, want workspace_tool_timeout_with_cli_margin", payload.TimeoutOwner))

	})
	ginkgo.It("direct built in tool does not initialize configured mcp", func() {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			http.Error(w, "mcp should not be contacted", http.StatusInternalServerError)
		}))
		defer srv.Close()

		cfgPath := writeToolMCPConfigWithoutRules(srv.URL)

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{"pwd", "--config", cfgPath, "--profile", "discovery"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("tool pwd exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		if got := calls.Load(); got != 0 {
			ginkgo.Fail(fmt.Sprintf("built-in direct tool should not initialize MCP servers, got %d request(s)", got))
		}
	})
	ginkgo.It("discovery profile denies configured mcp without explicit rule", func() {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			http.Error(w, "mcp should not be contacted before policy denial", http.StatusInternalServerError)
		}))
		defer srv.Close()

		cfgPath := writeToolMCPConfigWithoutRules(srv.URL)

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{
			"mcp:exa/web_search_exa",
			"--config", cfgPath,
			"--profile", "discovery",
			"--input", `{"query":"idiom web search"}`,
		}, &stdout, &stderr, nil)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("MCP discovery call without explicit rule should fail stdout=%s stderr=%s", stdout.String(), stderr.String()))

		if got := calls.Load(); got != 0 {
			ginkgo.Fail(fmt.Sprintf("denied MCP direct tool should not initialize MCP servers, got %d request(s)", got))
		}
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("profile_denied"), fmt.Sprintf("stdout should contain profile denial, got:\n%s", stdout.String()))

	})
	ginkgo.It("tool discovery profile executes configured mcp web search", func() {
		srv := fakeToolMCPServer()
		defer srv.Close()

		cfgPath := writeToolMCPConfig(srv.URL)

		var stdout, stderr bytes.Buffer
		code := RunWithTelemetry([]string{
			"mcp:exa/web_search_exa",
			"--config", cfgPath,
			"--profile", "discovery",
			"--input", `{"query":"idiom web search"}`,
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("tool call exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("search result for idiom web search"), fmt.Sprintf("expected MCP web-search output, got:\n%s", stdout.String()))

	})
})

func fakeToolMCPServer() *httptest.Server {

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()

		body, _ := io.ReadAll(r.Body)

		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			ginkgo.Fail(fmt.Sprintf("fake MCP server: bad JSON-RPC request: %v", err))
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var result any

		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": mcp.ProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake-exa", "version": "0.0.1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "web_search_exa",
				"description": "Search the web through Exa",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"query": map[string]any{"type": "string"}},
					"required":   []string{"query"},
				},
			}}}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)

			var args struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal(params.Arguments, &args)

			result = map[string]any{"content": []map[string]any{{
				"type": "text",
				"text": fmt.Sprintf("search result for %s", args.Query),
			}}}
		case "notifications/initialized":
			return
		default:
			ginkgo.Fail(fmt.Sprintf("fake MCP server: unexpected method %q", req.Method))
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
}

func writeToolMCPConfig(url string) string {

	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.AllowPrivateNetwork = true
	cfg.MCP = []config.MCPServer{{
		Name:           "exa",
		URL:            url,
		TimeoutSeconds: 5,
	}}
	cfg.Workspace.ToolRules = map[string][]config.ToolRule{
		"mcp:exa/web_search_exa": {{Pattern: "*", Decision: "allow"}},
	}

	path := filepath.Join(root, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal config: %v", err))
	gomega.Expect(os.WriteFile(path, data, 0o644)).To(gomega.Succeed())

	return path
}

func writeToolMCPConfigWithoutRules(url string) string {

	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.AllowPrivateNetwork = true
	cfg.MCP = []config.MCPServer{{
		Name:           "exa",
		URL:            url,
		TimeoutSeconds: 5,
	}}

	path := filepath.Join(root, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal config: %v", err))
	gomega.Expect(os.WriteFile(path, data, 0o644)).To(gomega.Succeed())

	return path
}
