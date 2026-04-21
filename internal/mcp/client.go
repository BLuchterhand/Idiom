// Package mcp implements the smallest viable subset of the Model Context
// Protocol client surface that qwen-bubbles needs: stdio transport, tool
// discovery via tools/list, and tool execution via tools/call. No SSE/HTTP
// transport, no resources, no prompts, no sampling, no notifications. This
// lands Fix 8 from docs/opencode-fixes-design.md §8.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
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

// Transport is the narrow interface the client needs from a stdio pair, so
// tests can inject an in-memory pipe without starting a real process.
type Transport interface {
	Send(ctx context.Context, payload []byte) error
	Receive(ctx context.Context) ([]byte, error)
	Close() error
}

// Client speaks JSON-RPC 2.0 MCP over a Transport. Initialize + ListTools +
// CallTool is the entire surface.
type Client struct {
	transport Transport
	nextID    atomic.Int64

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[int64]chan rawResponse

	once       sync.Once
	startErr   error
	readerDone chan struct{}
}

// NewClient wraps a transport. Call Start once before Initialize.
func NewClient(transport Transport) *Client {
	return &Client{
		transport:  transport,
		pending:    make(map[int64]chan rawResponse),
		readerDone: make(chan struct{}),
	}
}

// Start launches the reader goroutine. Safe to call multiple times.
func (c *Client) Start(ctx context.Context) error {
	c.once.Do(func() {
		go c.readLoop(ctx)
	})
	return c.startErr
}

func (c *Client) readLoop(ctx context.Context) {
	defer close(c.readerDone)
	for {
		payload, err := c.transport.Receive(ctx)
		if err != nil {
			return
		}
		var resp rawResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			// Ignore garbage; the protocol doesn't define recovery so just
			// drop the frame and keep going.
			continue
		}
		if resp.ID == 0 {
			// Notifications have no ID; we don't handle any today.
			continue
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.pendingMu.Unlock()
		if ok {
			ch <- resp
		}
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
	ch := make(chan rawResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	c.writeMu.Lock()
	sendErr := c.transport.Send(ctx, payload)
	c.writeMu.Unlock()
	if sendErr != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("send %s: %w", method, sendErr)
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: server error %d: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
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

// Close tears down the transport. The read loop exits when Receive returns
// an error, which the transport close must produce.
func (c *Client) Close() error {
	if c.transport == nil {
		return nil
	}
	err := c.transport.Close()
	select {
	case <-c.readerDone:
	case <-time.After(2 * time.Second):
	}
	return err
}

// StdioTransport speaks MCP's newline-delimited JSON-RPC over a child
// process's stdio pair. One frame per line.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	closer io.Closer
}

// StartStdio launches command with args and returns a Transport connected
// to its stdio. The caller's context is attached to the cmd so killing it
// shuts the server down.
func StartStdio(ctx context.Context, command string, args []string, env []string) (*StdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start %s: %w", command, err)
	}
	return &StdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 1<<16),
		closer: multiCloser{stdin, stdout},
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
func (t *StdioTransport) Receive(ctx context.Context) ([]byte, error) {
	_ = ctx
	line, err := t.stdout.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return line, nil
}

// Close tears down stdio and waits briefly for the child to exit.
func (t *StdioTransport) Close() error {
	if t == nil {
		return nil
	}
	_ = t.closer.Close()
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_, _ = t.cmd.Process.Wait()
	}
	return nil
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var firstErr error
	for _, c := range m {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
