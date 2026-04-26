package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/mcp"
)

// fakeMCPServer answers initialize + tools/list with a single canned tool.
// CallTool isn't exercised here — these tests cover the LLM-facing
// surface (BuildToolDefinitions / AllowedTools), not dispatch.
func fakeMCPServer(t *testing.T, toolName, description, schema string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.Contains(string(body), `"method":"initialize"`):
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		case strings.Contains(string(body), `"method":"tools/list"`):
			// When schema is empty, omit inputSchema entirely so the
			// fan-in's "no schema → default object" fallback is
			// exercised. Otherwise pass the literal JSON through.
			if schema == "" {
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[{\"name\":%q,\"description\":%q}]}}\n",
					toolName, description)
			} else {
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[{\"name\":%q,\"description\":%q,\"inputSchema\":%s}]}}\n",
					toolName, description, schema)
			}
		}
	}))
}

func registerFakeMCP(t *testing.T, name string, srv *httptest.Server) *mcp.Registry {
	t.Helper()
	tr, err := mcp.StartHTTP(srv.URL, nil, 5*time.Second, true)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	client := mcp.NewClient(tr)
	reg := mcp.NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.RegisterExisting(ctx, name, client); err != nil {
		t.Fatalf("RegisterExisting: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg
}

func TestManagerBuildToolDefinitions_NoRegistryReturnsBuiltinsOnly(t *testing.T) {
	mgr := NewManager(config.Workspace{})
	got := mgr.BuildToolDefinitions(nil)
	want := BuildToolDefinitions(nil)
	if len(got) != len(want) {
		t.Fatalf("with no registry, manager returned %d defs, package returned %d", len(got), len(want))
	}
	for _, def := range got {
		if strings.HasPrefix(def.Name, "mcp:") {
			t.Fatalf("unexpected mcp:* tool with no registry: %q", def.Name)
		}
	}
}

func TestManagerBuildToolDefinitions_AppendsMCPTools(t *testing.T) {
	srv := fakeMCPServer(t, "web_search_exa", "Search the web", `{"type":"object","properties":{"query":{"type":"string"}}}`)
	defer srv.Close()
	reg := registerFakeMCP(t, "exa", srv)

	mgr := NewManager(config.Workspace{})
	mgr.SetMCPRegistry(reg)

	defs := mgr.BuildToolDefinitions(nil)
	var found *ToolDefinition
	for i := range defs {
		if defs[i].Name == "mcp:exa/web_search_exa" {
			found = &defs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("did not find mcp:exa/web_search_exa among %d defs", len(defs))
	}
	if !strings.HasPrefix(found.Description, "[mcp:exa] ") {
		t.Fatalf("description should start with provenance prefix, got %q", found.Description)
	}
	if !strings.Contains(found.Description, "Search the web") {
		t.Fatalf("description should contain upstream text, got %q", found.Description)
	}
	if !strings.Contains(found.ParametersJSON, `"query"`) {
		t.Fatalf("parameters should pass upstream inputSchema through, got %q", found.ParametersJSON)
	}
}

func TestManagerBuildToolDefinitions_AllowedFiltersMCPTools(t *testing.T) {
	srv := fakeMCPServer(t, "web_search_exa", "Search", "")
	defer srv.Close()
	reg := registerFakeMCP(t, "exa", srv)

	mgr := NewManager(config.Workspace{})
	mgr.SetMCPRegistry(reg)

	defs := mgr.BuildToolDefinitions([]string{"read_file"})
	for _, def := range defs {
		if strings.HasPrefix(def.Name, "mcp:") {
			t.Fatalf("allowed list excluded mcp tools, but %q slipped through", def.Name)
		}
	}

	defs = mgr.BuildToolDefinitions([]string{"mcp:exa/web_search_exa"})
	if len(defs) != 1 {
		t.Fatalf("expected 1 def with allow=[mcp:exa/web_search_exa], got %d", len(defs))
	}
	if defs[0].Name != "mcp:exa/web_search_exa" {
		t.Fatalf("got %q", defs[0].Name)
	}
}

func TestManagerAllowedTools_AppendsMCPNames(t *testing.T) {
	srv := fakeMCPServer(t, "web_search_exa", "", "")
	defer srv.Close()
	reg := registerFakeMCP(t, "exa", srv)

	mgr := NewManager(config.Workspace{})
	before := mgr.AllowedTools()

	mgr.SetMCPRegistry(reg)
	after := mgr.AllowedTools()

	if len(after) != len(before)+1 {
		t.Fatalf("expected exactly one new tool added, got %d → %d", len(before), len(after))
	}
	if after[len(after)-1] != "mcp:exa/web_search_exa" {
		t.Fatalf("last tool = %q, want mcp:exa/web_search_exa", after[len(after)-1])
	}
}

// fakeMCPCallServer also handles tools/call so dispatch can be exercised.
// The handler parses the JSON-RPC id off each request and echoes it back
// in the reply so the test does not have to predict the client's ID
// allocation order.
func fakeMCPCallServer(t *testing.T, toolName, callReplyText string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var probe struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &probe)
		w.Header().Set("Content-Type", "text/event-stream")
		switch probe.Method {
		case "initialize":
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"protocolVersion\":\"2024-11-05\"}}\n", probe.ID)
		case "tools/list":
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[{\"name\":%q,\"description\":\"x\",\"inputSchema\":{\"type\":\"object\"}}]}}\n",
				probe.ID, toolName)
		case "tools/call":
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":%q}]}}\n",
				probe.ID, callReplyText)
		}
	}))
}

// MCP tools must not be classified as workspace narrowing — they are
// external retrieval, not workspace inspection. The current switch in
// IsNarrowingRequest is a hard-coded allow-list keyed on built-in tool
// names; this test pins the behavior so a future maintainer who
// rewrites the switch (e.g. to a prefix check) does not silently
// upgrade mcp:* tools to "narrowing" and unlock the broad-inspection
// gate prematurely.
func TestMCPToolsAreNotNarrowing(t *testing.T) {
	req := ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"hello"}`)}
	if IsNarrowingRequest(req) {
		t.Fatalf("IsNarrowingRequest(mcp:*) = true, want false (external retrieval is not workspace narrowing)")
	}
	mgr := NewManager(config.Workspace{})
	if mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: req.Tool, Summary: "ok"}) {
		t.Fatalf("ShouldClearBroadInspectionLock(mcp:*) = true, want false")
	}
}

func TestManagerExecute_DispatchesMCPCall(t *testing.T) {
	srv := fakeMCPCallServer(t, "echo", "hello back")
	defer srv.Close()
	reg := registerFakeMCP(t, "fake", srv)

	mgr := NewManager(config.Workspace{})
	mgr.SetMCPRegistry(reg)

	req := ToolRequest{Tool: "mcp:fake/echo", Args: []byte(`{"text":"hello"}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := mgr.Execute(ctx, req)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Tool != "mcp:fake/echo" {
		t.Fatalf("Tool = %q", result.Tool)
	}
	if result.Output != "hello back" {
		t.Fatalf("Output = %q, want %q", result.Output, "hello back")
	}
	if result.Summary != "MCP mcp:fake/echo" {
		t.Fatalf("Summary = %q", result.Summary)
	}
}

func TestManagerExecute_MCPWithoutRegistryReturnsError(t *testing.T) {
	mgr := NewManager(config.Workspace{})
	req := ToolRequest{Tool: "mcp:fake/echo", Args: []byte(`{}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := mgr.Execute(ctx, req)
	if result.Error == "" {
		t.Fatalf("expected error, got none")
	}
	if !strings.Contains(result.Error, "registry not configured") {
		t.Fatalf("Error = %q, want mention of registry", result.Error)
	}
}

func TestManagerExecute_MCPUnregisteredToolReturnsError(t *testing.T) {
	srv := fakeMCPCallServer(t, "echo", "hi")
	defer srv.Close()
	reg := registerFakeMCP(t, "fake", srv)
	mgr := NewManager(config.Workspace{})
	mgr.SetMCPRegistry(reg)

	req := ToolRequest{Tool: "mcp:other/unknown", Args: []byte(`{}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := mgr.Execute(ctx, req)
	if result.Error == "" {
		t.Fatalf("expected error for unregistered tool")
	}
	if !strings.Contains(result.Error, "not registered") {
		t.Fatalf("Error = %q, want 'not registered'", result.Error)
	}
}

func TestExtractToolRuleTarget_MCPPullsQueryThenURL(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"query takes priority", `{"query":"go 1.24","url":"https://example.com/"}`, "go 1.24"},
		{"falls through to url", `{"url":"https://example.com/p"}`, "https://example.com/p"},
		{"empty when neither present", `{"foo":"bar"}`, ""},
		{"non-string query ignored", `{"query":42,"url":"https://example.com/"}`, "https://example.com/"},
		{"empty args returns empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := ToolRequest{Tool: "mcp:exa/web_search_exa"}
			if tc.args != "" {
				req.Args = []byte(tc.args)
			}
			got := extractToolRuleTarget(req.Tool, req)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMatchToolRule_MCPGlobFallback(t *testing.T) {
	rules := map[string][]ToolRule{
		"mcp:*": {{Pattern: "*", Decision: ToolRuleAsk}},
	}
	req := ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"go 1.24"}`)}
	got := MatchToolRule(req.Tool, req, rules)
	if got != ToolRuleAsk {
		t.Fatalf("got %q, want %q", got, ToolRuleAsk)
	}
}

func TestMatchToolRule_MCPSpecificBeatsGlob(t *testing.T) {
	rules := map[string][]ToolRule{
		"mcp:exa/web_search_exa": {{Pattern: "*", Decision: ToolRuleAllow}},
		"mcp:*":                  {{Pattern: "*", Decision: ToolRuleAsk}},
	}
	req := ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"x"}`)}
	got := MatchToolRule(req.Tool, req, rules)
	if got != ToolRuleAllow {
		t.Fatalf("got %q, want %q (specific rule should win)", got, ToolRuleAllow)
	}
}

func TestMatchToolRule_MCPPatternAgainstURL(t *testing.T) {
	rules := map[string][]ToolRule{
		"mcp:fetch/fetch": {
			{Pattern: "https://github.com/**", Decision: ToolRuleAllow},
			{Pattern: "*", Decision: ToolRuleAsk},
		},
	}
	allowed := ToolRequest{Tool: "mcp:fetch/fetch", Args: []byte(`{"url":"https://github.com/sst/opencode"}`)}
	if got := MatchToolRule(allowed.Tool, allowed, rules); got != ToolRuleAllow {
		t.Fatalf("github URL: got %q, want allow", got)
	}
	other := ToolRequest{Tool: "mcp:fetch/fetch", Args: []byte(`{"url":"https://random.example/"}`)}
	if got := MatchToolRule(other.Tool, other, rules); got != ToolRuleAsk {
		t.Fatalf("non-github URL: got %q, want ask", got)
	}
}

func TestManagerBuildToolDefinitions_EmptyDescriptionFallsBack(t *testing.T) {
	srv := fakeMCPServer(t, "tool_no_desc", "", "")
	defer srv.Close()
	reg := registerFakeMCP(t, "x", srv)

	mgr := NewManager(config.Workspace{})
	mgr.SetMCPRegistry(reg)

	defs := mgr.BuildToolDefinitions(nil)
	for _, def := range defs {
		if def.Name == "mcp:x/tool_no_desc" {
			if !strings.Contains(def.Description, "MCP tool from x") {
				t.Fatalf("expected fallback description, got %q", def.Description)
			}
			if def.ParametersJSON != `{"type":"object","properties":{}}` {
				t.Fatalf("expected default params, got %q", def.ParametersJSON)
			}
			return
		}
	}
	t.Fatalf("did not find mcp:x/tool_no_desc")
}
