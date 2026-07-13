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

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/mcpidentity"
)

// test-double: scripted-protocol-peer
// fakeTransport is an in-memory protocol peer. It deliberately preserves the
// transport's channel and framing behavior instead of mocking method calls.
type fakeTransport struct {
	incoming chan []byte
	outgoing chan []byte
	closed   bool
	mu       sync.Mutex
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{incoming: make(chan []byte, 16), outgoing: make(chan []byte, 16)}
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

func respondInitializeThenTools(transport *fakeTransport, tools []Tool, callResponder func(string, json.RawMessage) ToolCallResult) <-chan error {
	errors := make(chan error, 1)
	go func() {
		for payload := range transport.outgoing {
			var req rawRequest
			if err := json.Unmarshal(payload, &req); err != nil {
				reportFakeServerError(errors, fmt.Errorf("bad JSON-RPC frame: %w", err))
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
					reportFakeServerError(errors, fmt.Errorf("marshal tools: %w", err))
					return
				}
				result = raw
			case "tools/call":
				var params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if err := json.Unmarshal(req.Params, &params); err != nil {
					reportFakeServerError(errors, fmt.Errorf("decode tool call: %w", err))
					return
				}
				raw, err := json.Marshal(callResponder(params.Name, params.Arguments))
				if err != nil {
					reportFakeServerError(errors, fmt.Errorf("marshal tool result: %w", err))
					return
				}
				result = raw
			default:
				transport.incoming <- mustMarshal(rawResponse{JSONRPC: "2.0", ID: req.ID, Error: &rawError{Code: -32601, Message: "unknown method"}})
				continue
			}
			transport.incoming <- mustMarshal(rawResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
		}
	}()
	return errors
}

func respondInitializeToolsAndResources(transport *fakeTransport) <-chan error {
	errors := make(chan error, 1)
	go func() {
		for payload := range transport.outgoing {
			var req rawRequest
			if err := json.Unmarshal(payload, &req); err != nil {
				reportFakeServerError(errors, fmt.Errorf("bad JSON-RPC frame: %w", err))
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
				transport.incoming <- mustMarshal(rawResponse{JSONRPC: "2.0", ID: req.ID, Error: &rawError{Code: -32601, Message: "unknown method"}})
				continue
			}
			transport.incoming <- mustMarshal(rawResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
		}
	}()
	return errors
}

func reportFakeServerError(errors chan<- error, err error) {
	select {
	case errors <- err:
	default:
	}
}

func expectNoFakeServerError(errors <-chan error) {
	ginkgo.GinkgoHelper()
	select {
	case err := <-errors:
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	default:
	}
}

func mustMarshal(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

var _ = ginkgo.Describe("MCP client", func() {
	ginkgo.It("round-trips initialize, tool discovery, and a tool call", func(ctx ginkgo.SpecContext) {
		transport := newFakeTransport()
		called := make(chan string, 1)
		serverErrors := respondInitializeThenTools(transport, []Tool{{Name: "echo", Description: "echo args back"}}, func(name string, _ json.RawMessage) ToolCallResult {
			called <- name
			return ToolCallResult{Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"hello"}`)}}
		})
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)

		gomega.Expect(client.Start(callCtx)).To(gomega.Succeed())
		gomega.Expect(client.Initialize(callCtx, "idiom-test")).To(gomega.Succeed())
		tools, err := client.ListTools(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(tools).To(gomega.ConsistOf(gomega.HaveField("Name", "echo")))
		result, err := client.CallTool(callCtx, "echo", json.RawMessage(`{"x":1}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(called).To(gomega.Receive(gomega.Equal("echo")))
		gomega.Expect(result.Content).To(gomega.ConsistOf(gomega.WithTransform(func(raw json.RawMessage) bool {
			return containsBytes(raw, "hello")
		}, gomega.BeTrue())))
		expectNoFakeServerError(serverErrors)
	})

	ginkgo.It("removes canceled calls from pending state", func() {
		transport := newFakeTransport()
		client := NewClient(transport)
		readerContext, stopReader := context.WithCancel(context.Background())
		ginkgo.DeferCleanup(stopReader)
		ginkgo.DeferCleanup(client.Close)
		gomega.Expect(client.Start(readerContext)).To(gomega.Succeed())
		for i := 0; i < 8; i++ {
			callContext, cancelCall := context.WithCancel(context.Background())
			cancelCall()
			_, err := client.call(callContext, "test/cancel", map[string]any{"index": i})
			gomega.Expect(err).To(gomega.MatchError(context.Canceled))
		}
		client.pendingMu.Lock()
		pendingCount := len(client.pending)
		client.pendingMu.Unlock()
		gomega.Expect(pendingCount).To(gomega.BeZero())
	})

	ginkgo.It("fails and removes a pending call when the response reader terminates", func() {
		transport := newFakeTransport()
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		gomega.Expect(client.Start(context.Background())).To(gomega.Succeed())
		callDone := make(chan error, 1)
		go func() {
			_, err := client.call(context.Background(), "test/wait", nil)
			callDone <- err
		}()
		gomega.Eventually(transport.outgoing).WithTimeout(2 * time.Second).Should(gomega.Receive())
		gomega.Expect(transport.Close()).To(gomega.Succeed())
		var callErr error
		gomega.Eventually(callDone).WithTimeout(2 * time.Second).Should(gomega.Receive(&callErr))
		gomega.Expect(callErr).To(gomega.MatchError(gomega.And(gomega.ContainSubstring("response reader stopped"), gomega.ContainSubstring("EOF"))))
		gomega.Expect(errors.Is(callErr, io.EOF)).To(gomega.BeTrue())
		client.pendingMu.Lock()
		pendingCount := len(client.pending)
		client.pendingMu.Unlock()
		gomega.Expect(pendingCount).To(gomega.BeZero())
	})

	ginkgo.It("fails a pending call when the client closes", func() {
		transport := newFakeTransport()
		client := NewClient(transport)
		gomega.Expect(client.Start(context.Background())).To(gomega.Succeed())
		callDone := make(chan error, 1)
		go func() {
			_, err := client.call(context.Background(), "test/wait", nil)
			callDone <- err
		}()
		gomega.Eventually(transport.outgoing).WithTimeout(2 * time.Second).Should(gomega.Receive())
		gomega.Expect(client.Close()).To(gomega.Succeed())
		var callErr error
		gomega.Eventually(callDone).WithTimeout(2 * time.Second).Should(gomega.Receive(&callErr))
		gomega.Expect(errors.Is(callErr, io.ErrClosedPipe)).To(gomega.BeTrue())
	})

	ginkgo.It("round-trips resource discovery and reading", func(ctx ginkgo.SpecContext) {
		transport := newFakeTransport()
		serverErrors := respondInitializeToolsAndResources(transport)
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		gomega.Expect(client.Start(callCtx)).To(gomega.Succeed())
		gomega.Expect(client.Initialize(callCtx, "idiom-test")).To(gomega.Succeed())

		resources, err := client.ListResources(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(resources).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("URI", "idiom://workspace/root"),
			gomega.HaveField("Name", "workspace_root"),
		)))
		read, err := client.ReadResource(callCtx, "idiom://workspace/root")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(read.Contents).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("URI", "idiom://workspace/root"),
			gomega.HaveField("Text", gomega.ContainSubstring("metadata_only")),
		)))
		expectNoFakeServerError(serverErrors)
	})

	ginkgo.It("round-trips newline-delimited frames and environment through a real stdio child", ginkgo.Label("integration"), func(ctx ginkgo.SpecContext) {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		transport, err := StartStdio(callCtx, os.Args[0], []string{"-test.run=TestMCPStdioHelperProcess", "--"}, []string{
			"GO_WANT_MCP_STDIO_HELPER=1",
			"IDIOM_MCP_STDIO_HELPER_SCENARIO=echo",
			"IDIOM_MCP_ENV=env-ok",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(transport.Close)
		gomega.Expect(transport.Send(callCtx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))).To(gomega.Succeed())
		got, err := transport.Receive(callCtx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(got.Payload)).To(gomega.And(gomega.ContainSubstring(`"echo":true`), gomega.ContainSubstring(`"env":"env-ok"`)))
	})
})

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_STDIO_HELPER") != "1" {
		return
	}
	if os.Getenv("IDIOM_MCP_STDIO_HELPER_SCENARIO") == "resilience" {
		if err := serveStdioResiliencePeer(
			os.Getenv("IDIOM_MCP_PEER_MODE"),
			os.Getenv("IDIOM_MCP_PEER_GENERATION"),
		); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		os.Exit(0)
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

var _ = ginkgo.Describe("MCP registry", func() {
	ginkgo.It("namespaces tools by server and dispatches qualified calls", func(ctx ginkgo.SpecContext) {
		transport := newFakeTransport()
		serverErrors := respondInitializeThenTools(transport, []Tool{{Name: "search", Description: "search the thing"}, {Name: "summarize"}}, func(name string, _ json.RawMessage) ToolCallResult {
			return ToolCallResult{Content: []json.RawMessage{json.RawMessage(`{"ok":true,"tool":"` + name + `"}`)}}
		})
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		client := NewClient(transport)
		registry := NewRegistry()
		ginkgo.DeferCleanup(registry.Close)
		gomega.Expect(registry.RegisterExisting(callCtx, " docs ", client)).To(gomega.Succeed())
		gomega.Expect(registry.Has("mcp:docs/search")).To(gomega.BeTrue())
		gomega.Expect(registry.Has("search")).To(gomega.BeFalse())
		result, err := registry.Call(callCtx, "mcp:docs/summarize", nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.Content).To(gomega.ContainElement(gomega.WithTransform(func(raw json.RawMessage) bool {
			return containsBytes(raw, "summarize")
		}, gomega.BeTrue())))
		expectNoFakeServerError(serverErrors)
	})

	ginkgo.It("atomically admits one of two concurrent registrations for the same server", func(ctx ginkgo.SpecContext) {
		transports := []*fakeTransport{newFakeTransport(), newFakeTransport()}
		toolNames := []string{"search", "summarize"}
		clients := make([]*Client, len(transports))
		serverErrors := make([]<-chan error, len(transports))
		for i, transport := range transports {
			name := toolNames[i]
			serverErrors[i] = respondInitializeThenTools(transport, []Tool{{Name: name}}, func(called string, _ json.RawMessage) ToolCallResult {
				return ToolCallResult{Content: []json.RawMessage{json.RawMessage(`{"tool":"` + called + `"}`)}}
			})
			clients[i] = NewClient(transport)
			client := clients[i]
			ginkgo.DeferCleanup(client.Close)
		}
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		registry := NewRegistry()
		ginkgo.DeferCleanup(registry.Close)

		errs := make([]error, len(clients))
		var wg sync.WaitGroup
		for i, client := range clients {
			wg.Add(1)
			go func(index int, candidate *Client) {
				defer wg.Done()
				errs[index] = registry.RegisterExisting(callCtx, "docs", candidate)
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
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			}
		}
		gomega.Expect(registered).To(gomega.Equal(1))
		gomega.Expect(rejected).To(gomega.Equal(1))
		gomega.Expect(registry.Tools()).To(gomega.HaveLen(1))
		gomega.Expect(registry.Has("mcp:docs/search")).NotTo(gomega.Equal(registry.Has("mcp:docs/summarize")))
		for _, errors := range serverErrors {
			expectNoFakeServerError(errors)
		}
	})

	ginkgo.It("rejects duplicate tool names without partially mutating the registry", func(ctx ginkgo.SpecContext) {
		transport := newFakeTransport()
		serverErrors := respondInitializeThenTools(transport, []Tool{{Name: "search"}, {Name: " search "}}, func(string, json.RawMessage) ToolCallResult {
			return ToolCallResult{}
		})
		client := NewClient(transport)
		ginkgo.DeferCleanup(client.Close)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ginkgo.DeferCleanup(cancel)
		registry := NewRegistry()
		ginkgo.DeferCleanup(registry.Close)
		err := registry.RegisterExisting(callCtx, "docs", client)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(`duplicate tool "search"`)))
		gomega.Expect(registry.Tools()).To(gomega.BeEmpty())
		expectNoFakeServerError(serverErrors)
	})

	ginkgo.It("rejects an unknown qualified tool", func() {
		registry := NewRegistry()
		_, err := registry.Call(context.Background(), "mcp:unknown/tool", nil)
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("parses qualified tool names without accepting plain or incomplete names", func() {
		server, tool, ok := mcpidentity.ParseQualifiedToolName("mcp:docs/summarize")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(server).To(gomega.Equal("docs"))
		gomega.Expect(tool).To(gomega.Equal("summarize"))
		_, _, ok = mcpidentity.ParseQualifiedToolName("plain_tool")
		gomega.Expect(ok).To(gomega.BeFalse())
		_, _, ok = mcpidentity.ParseQualifiedToolName("mcp:only-server")
		gomega.Expect(ok).To(gomega.BeFalse())
	})
})

func containsBytes(raw json.RawMessage, needle string) bool {
	return len(raw) > 0 && strings.Contains(string(raw), needle)
}
