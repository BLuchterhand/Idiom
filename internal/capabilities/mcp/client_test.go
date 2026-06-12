package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// Fix 8 (minimal MCP client) — exercises docs/opencode-fixes-design.md §8.

// fakeTransport is an in-memory pair of channels that simulates the stdio
// frames the MCP client uses. A test installs a responder goroutine that
// reads client requests and writes back the canned responses.
type fakeTransport struct {
	incoming chan []byte
	outgoing chan []byte
	closed   bool
	mu       sync.Mutex
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		incoming: make(chan []byte, 16),
		outgoing: make(chan []byte, 16),
	}
}

func (t *fakeTransport) Send(ctx context.Context, payload []byte) error {
	t.mu.Lock()

	closed := t.closed
	t.mu.Unlock()

	if closed {
		return io.ErrClosedPipe
	}

	select {
	case t.outgoing <- append([]byte(nil), payload...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *fakeTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case payload, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}

		return payload, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *fakeTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.closed = true
	close(t.incoming)

	return nil
}

// respondInitializeThenTools wires a fake MCP server that answers the
// handshake, an ignored notifications/initialized frame, a tools/list
// request, and a subsequent tools/call.
func respondInitializeThenTools(t *testing.T, transport *fakeTransport, tools []Tool, callResponder func(name string, args json.RawMessage) ToolCallResult) {
	t.Helper()

	go func() {
		for payload := range transport.outgoing {
			var req rawRequest

			if err := json.Unmarshal(payload, &req); err != nil {
				t.Errorf("fake server: bad JSON-RPC frame: %v", err)

				return
			}

			if req.Method == "notifications/initialized" {
				continue
			}

			var result json.RawMessage

			switch req.Method {
			case "initialize":
				result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"0.0.1"}}`)
			case "tools/list":
				raw, err := json.Marshal(map[string]any{"tools": tools})
				if err != nil {
					t.Errorf("fake server: marshal tools: %v", err)

					return
				}

				result = raw
			case "tools/call":
				var params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}

				_ = json.Unmarshal(req.Params, &params)

				resp := callResponder(params.Name, params.Arguments)

				raw, err := json.Marshal(resp)
				if err != nil {
					t.Errorf("fake server: marshal call: %v", err)

					return
				}

				result = raw
			default:
				transport.incoming <- mustMarshal(rawResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &rawError{Code: -32601, Message: "unknown method"},
				})

				continue
			}

			transport.incoming <- mustMarshal(rawResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
		}
	}()
}

func respondInitializeToolsAndResources(t *testing.T, transport *fakeTransport) {
	t.Helper()

	go func() {
		for payload := range transport.outgoing {
			var req rawRequest

			if err := json.Unmarshal(payload, &req); err != nil {
				t.Errorf("fake server: bad JSON-RPC frame: %v", err)

				return
			}

			if req.Method == "notifications/initialized" {
				continue
			}

			var result json.RawMessage

			switch req.Method {
			case "initialize":
				result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{"resources":{}},"serverInfo":{"name":"fake","version":"0.0.1"}}`)
			case "tools/list":
				result = json.RawMessage(`{"tools":[{"name":"context_status"}]}`)
			case "resources/list":
				result = json.RawMessage(`{"resources":[{"uri":"idiom://workspace/root","name":"workspace_root","mimeType":"application/json"}]}`)
			case "resources/read":
				result = json.RawMessage(`{"contents":[{"uri":"idiom://workspace/root","mimeType":"application/json","text":"{\"metadata_only\":true}"}]}`)
			default:
				transport.incoming <- mustMarshal(rawResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &rawError{Code: -32601, Message: "unknown method"},
				})

				continue
			}

			transport.incoming <- mustMarshal(rawResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
		}
	}()
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return b
}

func TestClientInitializeListCallRoundTrip(t *testing.T) {
	transport := newFakeTransport()
	respondInitializeThenTools(t, transport,
		[]Tool{{Name: "echo", Description: "echo args back"}},
		func(name string, args json.RawMessage) ToolCallResult {
			if name != "echo" {
				t.Errorf("unexpected tool name %q", name)
			}

			return ToolCallResult{
				Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"hello"}`)},
			}
		},
	)

	client := NewClient(transport)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := client.Initialize(ctx, "idiom-test"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	result, err := client.CallTool(ctx, "echo", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if len(result.Content) != 1 || !containsBytes(result.Content[0], "hello") {
		t.Fatalf("unexpected CallTool result: %+v", result)
	}
}

func TestClientResourcesListAndReadRoundTrip(t *testing.T) {
	transport := newFakeTransport()
	respondInitializeToolsAndResources(t, transport)

	client := NewClient(transport)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := client.Initialize(ctx, "idiom-test"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	if len(resources) != 1 || resources[0].URI != "idiom://workspace/root" || resources[0].Name != "workspace_root" {
		t.Fatalf("unexpected resources: %+v", resources)
	}

	read, err := client.ReadResource(ctx, "idiom://workspace/root")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}

	if len(read.Contents) != 1 || read.Contents[0].URI != "idiom://workspace/root" || !containsBytes(json.RawMessage(read.Contents[0].Text), "metadata_only") {
		t.Fatalf("unexpected resource read: %+v", read)
	}
}

func TestRegistryNamespaceAndDispatch(t *testing.T) {
	transport := newFakeTransport()
	respondInitializeThenTools(t, transport,
		[]Tool{
			{Name: "search", Description: "search the thing"},
			{Name: "summarize"},
		},
		func(name string, _ json.RawMessage) ToolCallResult {
			return ToolCallResult{Content: []json.RawMessage{json.RawMessage(`{"ok":true,"tool":"` + name + `"}`)}}
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClient(transport)
	registry := NewRegistry()

	if err := registry.RegisterExisting(ctx, "docs", client); err != nil {
		t.Fatalf("RegisterExisting: %v", err)
	}

	defer registry.Close()

	if !registry.Has("mcp:docs/search") {
		t.Fatal("expected qualified tool mcp:docs/search to be registered")
	}

	if registry.Has("search") {
		t.Fatal("unqualified tool names should not be registered")
	}

	result, err := registry.Call(ctx, "mcp:docs/summarize", nil)
	if err != nil {
		t.Fatalf("registry.Call: %v", err)
	}

	if len(result.Content) == 0 || !containsBytes(result.Content[0], "summarize") {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestRegistryRejectsUnknownQualifiedTool(t *testing.T) {
	registry := NewRegistry()

	_, err := registry.Call(context.Background(), "mcp:unknown/tool", nil)
	if err == nil {
		t.Fatal("expected error when calling unregistered MCP tool")
	}

	if !errors.Is(err, err) { // sanity: non-nil error
		t.Fatal("expected real error")
	}
}

func TestParseQualifiedToolName(t *testing.T) {
	server, tool, ok := ParseQualifiedToolName("mcp:docs/summarize")
	if !ok || server != "docs" || tool != "summarize" {
		t.Fatalf("unexpected parse: server=%q tool=%q ok=%v", server, tool, ok)
	}

	if _, _, ok := ParseQualifiedToolName("plain_tool"); ok {
		t.Fatal("non-MCP prefix must not parse")
	}

	if _, _, ok := ParseQualifiedToolName("mcp:only-server"); ok {
		t.Fatal("missing tool part must not parse")
	}
}

func containsBytes(raw json.RawMessage, needle string) bool {
	return len(raw) > 0 && indexOf(string(raw), needle) >= 0
}

func indexOf(haystack string, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}

	return -1
}
