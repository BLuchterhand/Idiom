// Package mcp implements the smallest viable subset of the Model Context
// Protocol client surface that idiom needs: stdio and HTTP/SSE transports,
// tool discovery via tools/list, tool execution via tools/call, and metadata
// resource discovery via resources/list and resources/read. No prompts,
// sampling, or notifications beyond initialized. Fix 8 from
// docs/opencode-fixes-design.md §8 landed the stdio piece; the HTTP transport
// (StartHTTP, see http.go) is documented in docs/web-search-tool-design.md and
// is what hosted servers like https://mcp.exa.ai/mcp use.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"idiom/internal/platform/commandexec"
)

// ProtocolVersion is the MCP spec revision this client negotiates.
const ProtocolVersion = "2024-11-05"

// Tool describes a tool exposed by an MCP server.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolCallResult is the return value of CallTool. Content is preserved as
// raw JSON so callers can render it without this package speaking every
// content variant the protocol allows.
type ToolCallResult struct {
	Content []json.RawMessage `json:"content,omitempty"`
	IsError bool              `json:"isError,omitempty"`
}

// rawRequest and rawResponse are the JSON-RPC 2.0 envelopes used by MCP.
type rawRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rawResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rawError       `json:"error,omitempty"`
}

type rawError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type pendingCallResult struct {
	response rawResponse
	err      error
}

// TransportMessage is one inbound transport event. RequestError is scoped to
// RequestID and does not terminate unrelated calls; a non-nil error returned by
// Receive means the transport itself is no longer usable.
type TransportMessage struct {
	Payload      []byte
	RequestID    int64
	RequestError error
}

// Transport is the narrow interface the client needs from a stdio pair, so
// tests can inject an in-memory pipe without starting a real process.
type Transport interface {
	Send(ctx context.Context, payload []byte) error
	Receive(ctx context.Context) (TransportMessage, error)
	Close() error
}

// Client speaks JSON-RPC 2.0 MCP over a Transport. Initialize + ListTools +
// CallTool is the entire surface.
type Client struct {
	transport Transport
	nextID    atomic.Int64

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[int64]chan pendingCallResult
	readErr   error

	once       sync.Once
	started    atomic.Bool
	readerDone chan struct{}
}

// NewClient wraps a transport. Call Start once before Initialize.
func NewClient(transport Transport) *Client {
	return &Client{
		transport:  transport,
		pending:    make(map[int64]chan pendingCallResult),
		readerDone: make(chan struct{}),
	}
}

// Start launches the reader goroutine. Safe to call multiple times.
func (c *Client) Start(ctx context.Context) error {
	c.once.Do(func() {
		c.started.Store(true)
		go c.readLoop(ctx)
	})

	return nil
}

func (c *Client) readLoop(ctx context.Context) {
	defer close(c.readerDone)

	for {
		message, err := c.transport.Receive(ctx)
		if err != nil {
			c.failPending(fmt.Errorf("mcp response reader stopped: %w", err))
			return
		}
		if message.RequestError != nil {
			if ch, ok := c.takePending(message.RequestID); ok {
				ch <- pendingCallResult{err: message.RequestError}
			}
			continue
		}

		var resp rawResponse

		if err := json.Unmarshal(message.Payload, &resp); err != nil {
			// Ignore garbage; the protocol doesn't define recovery so just
			// drop the frame and keep going.
			continue
		}

		if resp.ID == 0 {
			// Notifications have no ID; we don't handle any today.
			continue
		}

		if ch, ok := c.takePending(resp.ID); ok {
			ch <- pendingCallResult{response: resp}
		}
	}
}

func (c *Client) registerPending(id int64, ch chan pendingCallResult) error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	if c.readErr != nil {
		return c.readErr
	}
	c.pending[id] = ch
	return nil
}

func (c *Client) takePending(id int64) (chan pendingCallResult, bool) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	return ch, ok
}

func (c *Client) removePending(id int64, ch chan pendingCallResult) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	if current, ok := c.pending[id]; ok && current == ch {
		delete(c.pending, id)
	}
}

func (c *Client) failPending(err error) {
	if err == nil {
		err = io.EOF
	}

	c.pendingMu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	terminalErr := c.readErr
	pending := c.pending
	c.pending = make(map[int64]chan pendingCallResult)
	c.pendingMu.Unlock()

	for _, ch := range pending {
		ch <- pendingCallResult{err: terminalErr}
	}
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	var paramsJSON json.RawMessage

	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal %s params: %w", method, err)
		}

		paramsJSON = raw
	}

	payload, err := json.Marshal(rawRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", method, err)
	}

	ch := make(chan pendingCallResult, 1)
	if err := c.registerPending(id, ch); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	c.writeMu.Lock()

	sendErr := c.transport.Send(ctx, payload)
	c.writeMu.Unlock()

	if sendErr != nil {
		c.removePending(id, ch)

		return nil, fmt.Errorf("send %s: %w", method, sendErr)
	}

	select {
	case result := <-ch:
		if result.err != nil {
			return nil, fmt.Errorf("%s: %w", method, result.err)
		}
		resp := result.response
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: server error %d: %s", method, resp.Error.Code, resp.Error.Message)
		}

		return resp.Result, nil
	case <-ctx.Done():
		c.removePending(id, ch)
		return nil, ctx.Err()
	}
}

// Initialize performs the MCP handshake. Returns the server's reported
// protocol version string (ignored here) or an error.
func (c *Client) Initialize(ctx context.Context, clientName string) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": "0.1.0",
		},
		"capabilities": map[string]any{},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return err
	}

	// "initialized" is a notification — MCP spec requires it after the
	// handshake but the server doesn't reply. We use Send directly so we
	// don't wait on a response.
	note, err := json.Marshal(rawRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
	if err != nil {
		return err
	}

	c.writeMu.Lock()

	sendErr := c.transport.Send(ctx, note)
	c.writeMu.Unlock()

	return sendErr
}

// ListTools returns the tool catalog the server currently advertises.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var reply struct {
		Tools []Tool `json:"tools"`
	}

	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}

	return reply.Tools, nil
}

// CallTool invokes a named tool with the given JSON-shaped arguments.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (ToolCallResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}
	if arguments == nil {
		params["arguments"] = map[string]any{}
	}

	raw, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return ToolCallResult{}, err
	}

	var reply ToolCallResult

	if err := json.Unmarshal(raw, &reply); err != nil {
		return ToolCallResult{}, fmt.Errorf("decode tools/call: %w", err)
	}

	return reply, nil
}

// ListResources returns the resources/list catalog currently advertised.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	raw, err := c.call(ctx, "resources/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var reply struct {
		Resources []Resource `json:"resources"`
	}

	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("decode resources/list: %w", err)
	}

	return reply.Resources, nil
}

// ReadResource fetches a resource by URI. Servers may return metadata-only
// payloads; this client preserves the protocol shape without interpreting it.
func (c *Client) ReadResource(ctx context.Context, uri string) (ResourceReadResult, error) {
	raw, err := c.call(ctx, "resources/read", ResourceReadParams{URI: uri})
	if err != nil {
		return ResourceReadResult{}, err
	}

	var reply ResourceReadResult

	if err := json.Unmarshal(raw, &reply); err != nil {
		return ResourceReadResult{}, fmt.Errorf("decode resources/read: %w", err)
	}

	return reply, nil
}

// Close tears down the transport. The read loop exits when Receive returns
// an error, which the transport close must produce.
func (c *Client) Close() error {
	if c.transport == nil {
		return nil
	}

	c.failPending(io.ErrClosedPipe)
	err := c.transport.Close()
	if !c.started.Load() {
		return err
	}
	select {
	case <-c.readerDone:
		return err
	case <-time.After(2 * time.Second):
		return errors.Join(err, errors.New("timed out waiting for MCP response reader shutdown"))
	}
}

// StdioTransport speaks MCP's newline-delimited JSON-RPC over a child
// process's stdio pair. One frame per line.
type StdioTransport struct {
	process *commandexec.Process
	stdin   io.WriteCloser
	stdout  *bufio.Reader
}

// StartStdio launches command with args and returns a Transport connected
// to its stdio. The caller's context is attached to the cmd so killing it
// shuts the server down.
func StartStdio(ctx context.Context, command string, args []string, env []string) (*StdioTransport, error) {
	process, err := commandexec.Start(ctx, commandexec.Spec{
		Name: command,
		Args: append([]string(nil), args...),
		Env:  append([]string(nil), env...),
	})
	if err != nil {
		return nil, err
	}

	return &StdioTransport{
		process: process,
		stdin:   process.Stdin(),
		stdout:  bufio.NewReaderSize(process.Stdout(), 1<<16),
	}, nil
}

// Send writes a JSON-RPC frame followed by a newline.
func (t *StdioTransport) Send(ctx context.Context, payload []byte) error {
	_ = ctx

	if _, err := t.stdin.Write(payload); err != nil {
		return err
	}

	if _, err := t.stdin.Write([]byte("\n")); err != nil {
		return err
	}

	return nil
}

// Receive reads one newline-terminated JSON-RPC frame.
func (t *StdioTransport) Receive(ctx context.Context) (TransportMessage, error) {
	_ = ctx

	line, err := t.stdout.ReadBytes('\n')

	if err != nil && len(line) == 0 {
		return TransportMessage{}, err
	}

	return TransportMessage{Payload: line}, nil
}

// Close tears down stdio and waits briefly for the child to exit.
func (t *StdioTransport) Close() error {
	if t == nil {
		return nil
	}

	if t.process != nil {
		return t.process.Close()
	}

	return nil
}
