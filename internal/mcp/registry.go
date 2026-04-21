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
type Registry struct {
	mu      sync.RWMutex
	clients map[string]*Client
	tools   map[string]registeredTool
}

type registeredTool struct {
	Server string
	Tool   Tool
}

// NewRegistry returns an empty registry. Call Spawn for each server, then
// ListTools / CallTool to use them.
func NewRegistry() *Registry {
	return &Registry{
		clients: make(map[string]*Client),
		tools:   make(map[string]registeredTool),
	}
}

// Spawn starts one MCP stdio server and discovers its tools. If the server
// fails to start or initialize, the error is returned and no tools are
// registered. Callers may ignore the error to keep the app running with
// fewer MCP tools rather than bailing out entirely.
func (r *Registry) Spawn(ctx context.Context, cfg ServerConfig) error {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return fmt.Errorf("mcp server %q command is required", name)
	}
	transport, err := StartStdio(ctx, cfg.Command, cfg.Args, cfg.Env)
	if err != nil {
		return fmt.Errorf("mcp %s start: %w", name, err)
	}
	client := NewClient(transport)
	if err := client.Start(ctx); err != nil {
		_ = transport.Close()
		return fmt.Errorf("mcp %s reader: %w", name, err)
	}
	if err := client.Initialize(ctx, "qwen-bubbles"); err != nil {
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
// Client directly.
func (r *Registry) RegisterExisting(ctx context.Context, name string, client *Client) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("mcp server name is required")
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	if err := client.Initialize(ctx, "qwen-bubbles"); err != nil {
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

// Close tears down every registered client. Safe to call with a nil
// Registry.
func (r *Registry) Close() error {
	if r == nil {
		return nil
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
