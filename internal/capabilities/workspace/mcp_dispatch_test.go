package workspace

//go:generate go tool mockgen -destination=mock_mcp_transport_test.go -package=workspace -mock_names=Transport=MockMCPTransport -typed idiom/internal/capabilities/mcp Transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"idiom/internal/capabilities/mcp"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

type mcpCallObservation struct {
	Name      string
	Arguments json.RawMessage
}

// test-double: scripted-protocol-peer
type mcpProtocolScript struct {
	Tool          mcp.Tool
	CallReplyText string
}

func registerMockMCP(name string, script mcpProtocolScript) (*mcp.Registry, <-chan mcpCallObservation) {
	ginkgo.GinkgoHelper()

	inbox := make(chan mcp.TransportMessage, 4)
	calls := make(chan mcpCallObservation, 1)
	closed := make(chan struct{})
	var closeOnce sync.Once
	transport := NewMockMCPTransport(gomock.NewController(ginkgo.GinkgoT()))
	transport.EXPECT().Send(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, payload []byte) error {
			response, err := mcpFixtureResponse(ctx, payload, script, calls)
			if err != nil {
				return err
			}
			if len(response) == 0 {
				return nil
			}
			select {
			case inbox <- mcp.TransportMessage{Payload: response}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-closed:
				return io.ErrClosedPipe
			}
		},
	)
	transport.EXPECT().Receive(gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context) (mcp.TransportMessage, error) {
			select {
			case message := <-inbox:
				return message, nil
			case <-ctx.Done():
				return mcp.TransportMessage{}, ctx.Err()
			case <-closed:
				return mcp.TransportMessage{}, io.EOF
			}
		},
	)
	transport.EXPECT().Close().Times(1).DoAndReturn(func() error {
		closeOnce.Do(func() { close(closed) })
		return nil
	})

	client := mcp.NewClient(transport)
	reg := mcp.NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gomega.Expect(reg.RegisterExisting(ctx, name, client)).To(gomega.Succeed())
	ginkgo.DeferCleanup(func() { reg.Close() })

	return reg, calls
}

func mcpFixtureResponse(ctx context.Context, payload []byte, script mcpProtocolScript, calls chan<- mcpCallObservation) ([]byte, error) {
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, fmt.Errorf("decode MCP fixture request: %w", err)
	}
	if request.JSONRPC != "2.0" {
		return nil, fmt.Errorf("MCP fixture received JSON-RPC version %q", request.JSONRPC)
	}
	if request.Method == "notifications/initialized" {
		return nil, nil
	}

	var result any
	switch request.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": mcp.ProtocolVersion}
	case "tools/list":
		result = map[string]any{"tools": []mcp.Tool{script.Tool}}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, fmt.Errorf("decode MCP fixture tool call: %w", err)
		}
		select {
		case calls <- mcpCallObservation{Name: params.Name, Arguments: append(json.RawMessage(nil), params.Arguments...)}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		content, err := json.Marshal(map[string]string{"type": "text", "text": script.CallReplyText})
		if err != nil {
			return nil, fmt.Errorf("encode MCP fixture content: %w", err)
		}
		result = mcp.ToolCallResult{Content: []json.RawMessage{content}}
	default:
		return nil, fmt.Errorf("MCP fixture received unsupported method %q", request.Method)
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode MCP fixture result: %w", err)
	}
	return json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: "2.0", ID: request.ID, Result: resultJSON})
}

var _ = ginkgo.Describe("mcp dispatch", func() {
	ginkgo.It("manager build tool definitions_no registry returns builtins only", func() {
		mgr := NewManager(config.Workspace{})
		got := mgr.ToolSurfaceSnapshot().BuildToolDefinitions(nil)
		want := BuildToolDefinitions(nil)
		gomega.Expect(got).To(gomega.HaveLen(len(want)), fmt.Sprintf("with no registry, manager returned %d defs, package returned %d", len(got), len(want)))

		for _, def := range got {
			gomega.Expect(strings.HasPrefix(def.Name, "mcp:")).To(gomega.BeFalse(), fmt.Sprintf("unexpected mcp:* tool with no registry: %q", def.Name))

		}
	})
	ginkgo.It("manager build tool definitions_appends mcp tools", func() {
		reg, _ := registerMockMCP("exa", mcpProtocolScript{Tool: mcp.Tool{
			Name:        "web_search_exa",
			Description: "Search the web",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		}})

		mgr := NewManager(config.Workspace{})
		mgr.toolSurface.SetMCPRegistry(reg)

		defs := mgr.ToolSurfaceSnapshot().BuildToolDefinitions(nil)

		var found *ToolDefinition

		for i := range defs {
			if defs[i].Name == "mcp:exa/web_search_exa" {
				found = &defs[i]
				break
			}
		}
		gomega.Expect(found).NotTo(gomega.BeNil(), fmt.Sprintf("did not find mcp:exa/web_search_exa among %d defs", len(defs)))
		gomega.Expect(strings.HasPrefix(found.Description, "[mcp:exa] ")).To(gomega.BeTrue(), fmt.Sprintf("description should start with provenance prefix, got %q", found.Description))
		gomega.Expect(found.Description).To(gomega.ContainSubstring("Search the web"), fmt.Sprintf("description should contain upstream text, got %q", found.Description))
		gomega.Expect(found.ParametersJSON).To(gomega.ContainSubstring(`"query"`), fmt.Sprintf("parameters should pass upstream inputSchema through, got %q", found.ParametersJSON))

	})
	ginkgo.It("manager build tool definitions_allowed filters mcp tools", func() {
		reg, _ := registerMockMCP("exa", mcpProtocolScript{Tool: mcp.Tool{Name: "web_search_exa", Description: "Search"}})

		mgr := NewManager(config.Workspace{})
		mgr.toolSurface.SetMCPRegistry(reg)

		defs := mgr.ToolSurfaceSnapshot().BuildToolDefinitions([]string{"read_file"})
		for _, def := range defs {
			gomega.Expect(strings.HasPrefix(def.Name, "mcp:")).To(gomega.BeFalse(), fmt.Sprintf("allowed list excluded mcp tools, but %q slipped through", def.Name))

		}

		defs = mgr.ToolSurfaceSnapshot().BuildToolDefinitions([]string{"mcp:exa/web_search_exa"})
		gomega.Expect(defs).To(gomega.HaveLen(1), fmt.Sprintf("expected 1 def with allow=[mcp:exa/web_search_exa], got %d", len(defs)))
		gomega.Expect(defs[0].Name).To(gomega.Equal("mcp:exa/web_search_exa"), fmt.Sprintf("got %q", defs[0].Name))

	})
	ginkgo.It("manager allowed tools_appends mcp names", func() {
		reg, _ := registerMockMCP("exa", mcpProtocolScript{Tool: mcp.Tool{Name: "web_search_exa"}})

		mgr := NewManager(config.Workspace{})
		before := mgr.ToolSurfaceSnapshot().AllowedTools()
		mgr.toolSurface.SetMCPRegistry(reg)

		after := mgr.ToolSurfaceSnapshot().AllowedTools()
		gomega.Expect(after).To(gomega.HaveLen(len(before)+1), fmt.Sprintf("expected exactly one new tool added, got %d → %d", len(before), len(after)))
		gomega.Expect(after[len(after)-1]).To(gomega.Equal("mcp:exa/web_search_exa"), fmt.Sprintf("last tool = %q, want mcp:exa/web_search_exa", after[len(after)-1]))

	})
})

var

// MCP tools must not be classified as workspace narrowing — they are
// external retrieval, not workspace inspection. The current switch in
// IsNarrowingRequest is a hard-coded allow-list keyed on built-in tool
// names; this test pins the behavior so a future maintainer who
// rewrites the switch (e.g. to a prefix check) does not silently
// upgrade mcp:* tools to "narrowing" and unlock the broad-inspection
// gate prematurely.
_ = ginkgo.Describe("mcp dispatch", func() {
	ginkgo.It("mcp tools are not narrowing", func() {
		req := ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"hello"}`)}
		gomega.Expect(IsNarrowingRequest(req)).To(gomega.BeFalse(), fmt.Sprintf("IsNarrowingRequest(mcp:*) = true, want false (external retrieval is not workspace narrowing)"))

		mgr := NewManager(config.Workspace{})
		gomega.Expect(mgr.ShouldClearBroadInspectionLock(req, ToolResult{Tool: req.Tool, Summary: "ok"})).To(gomega.BeFalse(), fmt.Sprintf("ShouldClearBroadInspectionLock(mcp:*) = true, want false"))

	})
	ginkgo.It("manager execute_dispatches mcp call", func() {
		reg, calls := registerMockMCP("fake", mcpProtocolScript{
			Tool:          mcp.Tool{Name: "echo", Description: "x", InputSchema: json.RawMessage(`{"type":"object"}`)},
			CallReplyText: "hello back",
		})

		mgr := NewManager(config.Workspace{})
		mgr.toolSurface.SetMCPRegistry(reg)

		req := ToolRequest{Tool: "mcp:fake/echo", Args: []byte(`{"text":"hello"}`)}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		defer cancel()

		result := mgr.Execute(ctx, req)
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("unexpected error: %s", result.Error))
		gomega.Expect(result.Tool).To(gomega.Equal("mcp:fake/echo"), fmt.Sprintf("Tool = %q", result.Tool))
		gomega.Expect(result.Output).To(gomega.Equal("hello back"), fmt.Sprintf("Output = %q, want %q", result.Output, "hello back"))
		gomega.Expect(result.Summary).To(gomega.Equal("MCP mcp:fake/echo"), fmt.Sprintf("Summary = %q", result.Summary))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.CapabilityEvidence).To(gomega.ContainElement(toolfacts.CapabilityEvidence{
			Tool: "mcp:fake/echo", Status: toolfacts.CapabilityEvidenceVerified, Source: "mcp_dispatch",
		}))
		call := <-calls
		gomega.Expect(call.Name).To(gomega.Equal("echo"))
		gomega.Expect(call.Arguments).To(gomega.MatchJSON(`{"text":"hello"}`))

	})
	ginkgo.It("manager execute attaches typed web source identity without changing capability evidence", func() {
		reg, _ := registerMockMCP("exa", mcpProtocolScript{
			Tool:          mcp.Tool{Name: "web_fetch_exa", Description: "Fetch", InputSchema: json.RawMessage(`{"type":"object"}`)},
			CallReplyText: `{"url":"https://example.com/article","title":"Example article","text":"body"}`,
		})

		mgr := NewManager(config.Workspace{})
		mgr.toolSurface.SetMCPRegistry(reg)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		result := mgr.Execute(ctx, ToolRequest{Tool: "mcp:exa/web_fetch_exa", Args: []byte(`{"url":"https://example.com/article"}`)})
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.CapabilityEvidence).To(gomega.ContainElement(toolfacts.CapabilityEvidence{
			Tool: "mcp:exa/web_fetch_exa", Status: toolfacts.CapabilityEvidenceVerified, Source: "mcp_dispatch",
		}))
		gomega.Expect(result.Facts.ExternalWebSources).To(gomega.Equal([]toolfacts.ExternalWebSourceEvidence{{
			Kind:         toolfacts.ExternalWebSourceBody,
			URL:          "https://example.com/article",
			CanonicalURL: "https://example.com/article",
			Title:        "Example article",
		}}))
	})
	ginkgo.It("manager execute_mcp without registry returns error", func() {
		mgr := NewManager(config.Workspace{})
		req := ToolRequest{Tool: "mcp:fake/echo", Args: []byte(`{}`)}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		defer cancel()

		result := mgr.Execute(ctx, req)
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected error, got none"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("registry not configured"), fmt.Sprintf("Error = %q, want mention of registry", result.Error))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.CapabilityEvidence).To(gomega.ContainElement(toolfacts.CapabilityEvidence{
			Tool: "mcp:fake/echo", Status: toolfacts.CapabilityEvidenceUnavailable, Source: "mcp_dispatch",
		}))

	})
	ginkgo.It("manager execute_mcp unregistered tool returns error", func() {
		reg, _ := registerMockMCP("fake", mcpProtocolScript{
			Tool:          mcp.Tool{Name: "echo", Description: "x", InputSchema: json.RawMessage(`{"type":"object"}`)},
			CallReplyText: "hi",
		})
		mgr := NewManager(config.Workspace{})
		mgr.toolSurface.SetMCPRegistry(reg)

		req := ToolRequest{Tool: "mcp:other/unknown", Args: []byte(`{}`)}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		defer cancel()

		result := mgr.Execute(ctx, req)
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected error for unregistered tool"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("not registered"), fmt.Sprintf("Error = %q, want 'not registered'", result.Error))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.CapabilityEvidence).To(gomega.ContainElement(toolfacts.CapabilityEvidence{
			Tool: "mcp:other/unknown", Status: toolfacts.CapabilityEvidenceUnavailable, Source: "mcp_dispatch",
		}))

	})
})
var _ = ginkgo.Describe("tool policy input mcp pulls query then url", func() {

	cases := []struct {
		name string
		args string
		want string
	}{
		{"query takes priority", `{"query":"go 1.24","url":"https://example.com/"}`, "go 1.24"},
		{"falls through to url", `{"url":"https://example.com/p"}`, "https://example.com/p"},
		{"empty when neither present", `{"foo":"bar"}`, ""},
		{"non-string query ignored", `{"query":42,"url":"https://example.com/"}`, "https://example.com/"},
		{"malformed arguments ignored", `{"query":`, ""},
		{"empty args returns empty", "", ""},
	}
	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			req := ToolRequest{Tool: "mcp:exa/web_search_exa"}
			if tc.args != "" {
				req.Args = []byte(tc.args)
			}

			got := ToolPolicyInput(req).PatternTarget
			gomega.Expect(got).To(gomega.Equal(tc.want), fmt.Sprintf("got %q, want %q", got, tc.want))

		})
	}
})
var _ = ginkgo.Describe("mcp dispatch", func() {
	ginkgo.It("tool policy match_mcp glob fallback", func() {
		rules := map[string][]toolpolicy.Rule{
			"mcp:*": {{Pattern: "*", Decision: toolpolicy.Ask}},
		}
		req := ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"go 1.24"}`)}
		got := toolpolicy.Match(ToolPolicyInput(req), rules)
		gomega.Expect(got).To(gomega.Equal(toolpolicy.Ask), fmt.Sprintf("got %q, want %q", got, toolpolicy.Ask))

	})
	ginkgo.It("tool policy match_mcp specific beats glob", func() {
		rules := map[string][]toolpolicy.Rule{
			"mcp:exa/web_search_exa": {{Pattern: "*", Decision: toolpolicy.Allow}},
			"mcp:*":                  {{Pattern: "*", Decision: toolpolicy.Ask}},
		}
		req := ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"x"}`)}
		got := toolpolicy.Match(ToolPolicyInput(req), rules)
		gomega.Expect(got).To(gomega.Equal(toolpolicy.Allow), fmt.Sprintf("got %q, want %q (specific rule should win)", got, toolpolicy.Allow))

	})
	ginkgo.It("tool policy match_mcp pattern against url", func() {
		rules := map[string][]toolpolicy.Rule{
			"mcp:fetch/fetch": {
				{Pattern: "https://github.com/**", Decision: toolpolicy.Allow},
				{Pattern: "*", Decision: toolpolicy.Ask},
			},
		}
		allowed := ToolRequest{Tool: "mcp:fetch/fetch", Args: []byte(`{"url":"https://github.com/sst/opencode"}`)}

		if got := toolpolicy.Match(ToolPolicyInput(allowed), rules); got != toolpolicy.Allow {
			ginkgo.Fail(fmt.Sprintf("github URL: got %q, want allow", got))
		}

		other := ToolRequest{Tool: "mcp:fetch/fetch", Args: []byte(`{"url":"https://random.example/"}`)}
		if got := toolpolicy.Match(ToolPolicyInput(other), rules); got != toolpolicy.Ask {
			ginkgo.Fail(fmt.Sprintf("non-github URL: got %q, want ask", got))
		}
	})
	ginkgo.It("manager build tool definitions_empty description falls back", func() {
		reg, _ := registerMockMCP("x", mcpProtocolScript{Tool: mcp.Tool{Name: "tool_no_desc"}})

		mgr := NewManager(config.Workspace{})
		mgr.toolSurface.SetMCPRegistry(reg)

		defs := mgr.ToolSurfaceSnapshot().BuildToolDefinitions(nil)
		for _, def := range defs {
			if def.Name == "mcp:x/tool_no_desc" {
				gomega.Expect(def.Description).To(gomega.ContainSubstring("MCP tool from x"), fmt.Sprintf("expected fallback description, got %q", def.Description))
				gomega.Expect(def.ParametersJSON).To(gomega.Equal(`{"type":"object","properties":{}}`), fmt.Sprintf("expected default params, got %q", def.ParametersJSON))

				return
			}
		}
		ginkgo.Fail(fmt.Sprintf("did not find mcp:x/tool_no_desc"))
	})
})
