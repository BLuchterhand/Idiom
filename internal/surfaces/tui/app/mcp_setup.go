package app

import (
	"context"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
)

// setupMCPRegistry constructs an mcp.Registry from a slice of
// configured servers. Stdio entries are spawned via the registry's
// Spawn helper; HTTP entries are wrapped in HTTPTransport, which runs
// the SSRF guard before the first request.
//
// Returns the registry plus a slice of human-readable failure messages
// (one per server that did not register). A single broken server does
// not abort startup — the agent still runs with whatever did register.
//
// Returns (nil, nil) when servers is empty, so callers can pass the
// raw cfg.MCP slice without extra branching.
func setupMCPRegistry(ctx context.Context, servers []config.MCPServer, allowPrivate bool) (*mcp.Registry, []string) {
	return mcp.SetupRegistry(ctx, servers, allowPrivate)
}
