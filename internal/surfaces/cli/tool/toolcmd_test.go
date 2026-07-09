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
	"strings"
	"sync/atomic"
	"testing"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
)

func TestToolListIncludesConfiguredMCPWebSearch(t *testing.T) {
	srv := fakeToolMCPServer(t)
	defer srv.Close()

	cfgPath := writeToolMCPConfig(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := RunWithTelemetry([]string{"list", "--config", cfgPath}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("tool list exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	if !strings.Contains(stdout.String(), "mcp:exa/web_search_exa") {
		t.Fatalf("expected configured MCP web search in list, got:\n%s", stdout.String())
	}
}

func TestToolDiscoveryListDoesNotInitializeMCPWithoutExplicitAllowRule(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "mcp should not be contacted for denied discovery list", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := writeToolMCPConfigWithoutRules(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := RunWithTelemetry([]string{"list", "--config", cfgPath, "--profile", "discovery"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("tool list exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("discovery list without MCP allow rules should not initialize MCP, got %d request(s)", got)
	}
	if strings.Contains(stdout.String(), "mcp:") {
		t.Fatalf("discovery list should not advertise denied MCP tools:\n%s", stdout.String())
	}
}

func TestDirectToolOutputIncludesTimeoutOwner(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithTelemetry([]string{"pwd", "--config", cfgPath}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("tool pwd exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	var payload struct {
		TimeoutOwner string `json:"timeout_owner"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode tool output: %v\n%s", err, stdout.String())
	}
	if payload.TimeoutOwner != "workspace_tool_timeout_with_cli_margin" {
		t.Fatalf("timeout_owner = %q, want workspace_tool_timeout_with_cli_margin", payload.TimeoutOwner)
	}
}

func TestDirectBuiltInToolDoesNotInitializeConfiguredMCP(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "mcp should not be contacted", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := writeToolMCPConfigWithoutRules(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := RunWithTelemetry([]string{"pwd", "--config", cfgPath, "--profile", "discovery"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("tool pwd exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("built-in direct tool should not initialize MCP servers, got %d request(s)", got)
	}
}

func TestDiscoveryProfileDeniesConfiguredMCPWithoutExplicitRule(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "mcp should not be contacted before policy denial", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := writeToolMCPConfigWithoutRules(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := RunWithTelemetry([]string{
		"mcp:exa/web_search_exa",
		"--config", cfgPath,
		"--profile", "discovery",
		"--input", `{"query":"idiom web search"}`,
	}, &stdout, &stderr, nil)
	if code == 0 {
		t.Fatalf("MCP discovery call without explicit rule should fail stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("denied MCP direct tool should not initialize MCP servers, got %d request(s)", got)
	}
	if !strings.Contains(stdout.String(), "profile_denied") {
		t.Fatalf("stdout should contain profile denial, got:\n%s", stdout.String())
	}
}

func TestToolDiscoveryProfileExecutesConfiguredMCPWebSearch(t *testing.T) {
	srv := fakeToolMCPServer(t)
	defer srv.Close()

	cfgPath := writeToolMCPConfig(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := RunWithTelemetry([]string{
		"mcp:exa/web_search_exa",
		"--config", cfgPath,
		"--profile", "discovery",
		"--input", `{"query":"idiom web search"}`,
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("tool call exited %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	if !strings.Contains(stdout.String(), "search result for idiom web search") {
		t.Fatalf("expected MCP web-search output, got:\n%s", stdout.String())
	}
}

func fakeToolMCPServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("fake MCP server: bad JSON-RPC request: %v", err)
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
			t.Errorf("fake MCP server: unexpected method %q", req.Method)
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

func writeToolMCPConfig(t *testing.T, url string) string {
	t.Helper()

	root := t.TempDir()
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
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func writeToolMCPConfigWithoutRules(t *testing.T, url string) string {
	t.Helper()

	root := t.TempDir()
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
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
