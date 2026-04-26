package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// ServerConfig describes one MCP stdio server the registry should spawn.
// Mirrors the user-facing config schema defined in internal/config.
type ServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     []string
}

// Registry owns a set of running MCP clients and exposes their tools via a
// flattened namespace (mcp:<server>/<tool>).
//
// The registry holds its own background context (cancelled by Close)
// that is passed to transport startup and the per-client readLoop. The
// caller's setup context is used only for the handshake calls
// (Initialize, ListTools) so a short setup timeout does not later kill
// the long-lived reader.
type Registry struct {
	mu      sync.RWMutex
	clients map[string]*Client
	tools   map[string]registeredTool

	bgCtx    context.Context
	bgCancel context.CancelFunc
}

type registeredTool struct {
	Server string
	Tool   Tool
}

// NewRegistry returns an empty registry. Call Spawn for each server, then
// ListTools / CallTool to use them.
func NewRegistry() *Registry {
	bg, cancel := context.WithCancel(context.Background())
	return &Registry{
		clients:  make(map[string]*Client),
		tools:    make(map[string]registeredTool),
		bgCtx:    bg,
		bgCancel: cancel,
	}
}

// Spawn starts one MCP stdio server and discovers its tools. If the server
// fails to start or initialize, the error is returned and no tools are
// registered. Callers may ignore the error to keep the app running with
// fewer MCP tools rather than bailing out entirely.
//
// ctx scopes the handshake (initialize, tools/list); the underlying
// child process and reader use the registry's own background context so
// they outlive any short-lived setup timeout the caller passes here.
func (r *Registry) Spawn(ctx context.Context, cfg ServerConfig) error {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return fmt.Errorf("mcp server %q command is required", name)
	}
	transport, err := StartStdio(r.bgCtx, cfg.Command, cfg.Args, cfg.Env)
	if err != nil {
		return fmt.Errorf("mcp %s start: %w", name, err)
	}
	client := NewClient(transport)
	if err := client.Start(r.bgCtx); err != nil {
		_ = transport.Close()
		return fmt.Errorf("mcp %s reader: %w", name, err)
	}
	if err := client.Initialize(ctx, "idiom"); err != nil {
		_ = client.Close()
		return fmt.Errorf("mcp %s initialize: %w", name, err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("mcp %s tools/list: %w", name, err)
	}
	r.register(name, client, tools)
	return nil
}

// RegisterExisting lets tests or non-stdio transports attach a pre-built
// Client directly. ctx applies only to the handshake; the client's
// readLoop is bound to the registry's background context.
func (r *Registry) RegisterExisting(ctx context.Context, name string, client *Client) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("mcp server name is required")
	}
	if err := client.Start(r.bgCtx); err != nil {
		return err
	}
	if err := client.Initialize(ctx, "idiom"); err != nil {
		return err
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		return err
	}
	r.register(name, client, tools)
	return nil
}

func (r *Registry) register(name string, client *Client, tools []Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[name] = client
	for _, tool := range tools {
		r.tools[QualifiedToolName(name, tool.Name)] = registeredTool{Server: name, Tool: tool}
	}
}

// QualifiedToolName produces the namespaced tool id used when the model
// invokes an MCP tool. Prefix `mcp:` matches OpenCode's convention and also
// lets workspace.Manager dispatch cleanly.
func QualifiedToolName(server string, tool string) string {
	return "mcp:" + strings.TrimSpace(server) + "/" + strings.TrimSpace(tool)
}

// ParseQualifiedToolName is the inverse of QualifiedToolName.
func ParseQualifiedToolName(name string) (server string, tool string, ok bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(name), "mcp:")
	if trimmed == name {
		return "", "", false
	}
	idx := strings.IndexByte(trimmed, '/')
	if idx <= 0 || idx >= len(trimmed)-1 {
		return "", "", false
	}
	return trimmed[:idx], trimmed[idx+1:], true
}

// Tools snapshots the current namespaced tool catalog.
func (r *Registry) Tools() map[string]Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Tool, len(r.tools))
	for k, v := range r.tools {
		out[k] = v.Tool
	}
	return out
}

// Has reports whether a qualified tool name is registered.
func (r *Registry) Has(qualified string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[qualified]
	return ok
}

// Call dispatches a namespaced tool call to the owning client.
func (r *Registry) Call(ctx context.Context, qualified string, arguments json.RawMessage) (ToolCallResult, error) {
	server, tool, ok := ParseQualifiedToolName(qualified)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("mcp: %q is not a qualified MCP tool", qualified)
	}
	r.mu.RLock()
	client := r.clients[server]
	_, known := r.tools[qualified]
	r.mu.RUnlock()
	if client == nil || !known {
		return ToolCallResult{}, fmt.Errorf("mcp: server %q not registered for tool %q", server, tool)
	}
	return client.CallTool(ctx, tool, arguments)
}

// Close tears down every registered client and cancels the registry's
// background context. Safe to call with a nil Registry. Idempotent.
func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	if r.bgCancel != nil {
		r.bgCancel()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, client := range r.clients {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.clients = make(map[string]*Client)
	r.tools = make(map[string]registeredTool)
	return firstErr
}
