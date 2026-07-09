package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"idiom/internal/contracts/mcpidentity"
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

func (t *fakeTransport) Receive(ctx context.Context) (TransportMessage, error) {
	select {
	case payload, ok := <-t.incoming:
		if !ok {
			return TransportMessage{}, io.EOF
		}

		return TransportMessage{Payload: payload}, nil
	case <-ctx.Done():
		return TransportMessage{}, ctx.Err()
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

func TestClientCanceledCallsDoNotAccumulatePendingState(t *testing.T) {
	transport := newFakeTransport()
	client := NewClient(transport)
	readerContext, stopReader := context.WithCancel(context.Background())
	defer stopReader()
	if err := client.Start(readerContext); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	for i := 0; i < 8; i++ {
		callContext, cancelCall := context.WithCancel(context.Background())
		cancelCall()
		if _, err := client.call(callContext, "test/cancel", map[string]any{"index": i}); !errors.Is(err, context.Canceled) {
			t.Fatalf("call %d error = %v, want context cancellation", i, err)
		}
	}

	client.pendingMu.Lock()
	pendingCount := len(client.pending)
	client.pendingMu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("pending calls after cancellation = %d, want 0", pendingCount)
	}
}

func TestClientReaderTerminationFailsPendingCall(t *testing.T) {
	transport := newFakeTransport()
	client := NewClient(transport)
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	callDone := make(chan error, 1)
	go func() {
		_, err := client.call(context.Background(), "test/wait", nil)
		callDone <- err
	}()

	select {
	case <-transport.outgoing:
	case <-time.After(2 * time.Second):
		t.Fatal("pending call did not send its request")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("close transport: %v", err)
	}

	select {
	case err := <-callDone:
		if err == nil || !strings.Contains(err.Error(), "response reader stopped") || !errors.Is(err, io.EOF) {
			t.Fatalf("pending call error = %v, want wrapped reader EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call remained blocked after reader termination")
	}

	client.pendingMu.Lock()
	pendingCount := len(client.pending)
	client.pendingMu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("pending calls after reader termination = %d, want 0", pendingCount)
	}
}

func TestClientCloseFailsPendingCall(t *testing.T) {
	transport := newFakeTransport()
	client := NewClient(transport)
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	callDone := make(chan error, 1)
	go func() {
		_, err := client.call(context.Background(), "test/wait", nil)
		callDone <- err
	}()

	select {
	case <-transport.outgoing:
	case <-time.After(2 * time.Second):
		t.Fatal("pending call did not send its request")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-callDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("pending call error = %v, want closed pipe", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call remained blocked after client close")
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

func TestStartStdioRoundTripsFramesAndEnv(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	transport, err := StartStdio(ctx, os.Args[0], []string{"-test.run=TestMCPStdioHelperProcess", "--"}, []string{
		"GO_WANT_MCP_STDIO_HELPER=1",
		"IDIOM_MCP_ENV=env-ok",
	})
	if err != nil {
		t.Fatalf("StartStdio failed: %v", err)
	}
	defer transport.Close()

	if err := transport.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	got, err := transport.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}

	if !containsBytes(json.RawMessage(got.Payload), `"echo":true`) || !containsBytes(json.RawMessage(got.Payload), `"env":"env-ok"`) {
		t.Fatalf("unexpected stdio response: %s", got.Payload)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_STDIO_HELPER") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{"echo":true,"env":%q}}`+"\n", os.Getenv("IDIOM_MCP_ENV"))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scan stdin: %v\n", err)
		os.Exit(3)
	}

	os.Exit(0)
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

	if err := registry.RegisterExisting(ctx, " docs ", client); err != nil {
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

func TestRegistryRejectsConcurrentDuplicateServerRegistrationWithoutMixingTools(t *testing.T) {
	transports := []*fakeTransport{newFakeTransport(), newFakeTransport()}
	toolNames := []string{"search", "summarize"}
	clients := make([]*Client, len(transports))

	for i, transport := range transports {
		name := toolNames[i]
		respondInitializeThenTools(t, transport, []Tool{{Name: name}}, func(called string, _ json.RawMessage) ToolCallResult {
			return ToolCallResult{Content: []json.RawMessage{json.RawMessage(`{"tool":"` + called + `"}`)}}
		})
		clients[i] = NewClient(transport)
		defer clients[i].Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	registry := NewRegistry()
	defer registry.Close()

	errs := make([]error, len(clients))
	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Add(1)
		go func(index int, candidate *Client) {
			defer wg.Done()
			errs[index] = registry.RegisterExisting(ctx, "docs", candidate)
		}(i, client)
	}
	wg.Wait()

	registered := 0
	rejected := 0
	for _, err := range errs {
		switch {
		case err == nil:
			registered++
		case strings.Contains(err.Error(), `mcp server "docs" is already registered`):
			rejected++
		default:
			t.Fatalf("unexpected registration error: %v", err)
		}
	}
	if registered != 1 || rejected != 1 {
		t.Fatalf("registration outcomes = success:%d duplicate:%d, want 1 each; errors=%v", registered, rejected, errs)
	}

	tools := registry.Tools()
	if len(tools) != 1 {
		t.Fatalf("registered tools = %#v, want one server-owned catalog", tools)
	}
	if registry.Has("mcp:docs/search") == registry.Has("mcp:docs/summarize") {
		t.Fatalf("registry mixed or lost duplicate-server catalogs: %#v", tools)
	}
}

func TestRegistryRejectsDuplicateToolNamesAtomically(t *testing.T) {
	transport := newFakeTransport()
	respondInitializeThenTools(t, transport, []Tool{{Name: "search"}, {Name: " search "}}, func(string, json.RawMessage) ToolCallResult {
		return ToolCallResult{}
	})
	client := NewClient(transport)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	registry := NewRegistry()
	defer registry.Close()

	err := registry.RegisterExisting(ctx, "docs", client)
	if err == nil || !strings.Contains(err.Error(), `duplicate tool "search"`) {
		t.Fatalf("RegisterExisting error = %v, want duplicate-tool rejection", err)
	}
	if tools := registry.Tools(); len(tools) != 0 {
		t.Fatalf("duplicate tool registration partially mutated registry: %#v", tools)
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
	server, tool, ok := mcpidentity.ParseQualifiedToolName("mcp:docs/summarize")
	if !ok || server != "docs" || tool != "summarize" {
		t.Fatalf("unexpected parse: server=%q tool=%q ok=%v", server, tool, ok)
	}

	if _, _, ok := mcpidentity.ParseQualifiedToolName("plain_tool"); ok {
		t.Fatal("non-MCP prefix must not parse")
	}

	if _, _, ok := mcpidentity.ParseQualifiedToolName("mcp:only-server"); ok {
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
