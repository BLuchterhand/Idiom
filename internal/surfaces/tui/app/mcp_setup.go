package app

import (
	"context"
	"fmt"
	"time"

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
	if len(servers) == 0 {
		return nil, nil
	}

	reg := mcp.NewRegistry()

	var failures []string

	for _, srv := range servers {
		if err := registerMCPServer(ctx, reg, srv, allowPrivate); err != nil {
			failures = append(failures, fmt.Sprintf("mcp %q: %v", srv.Name, err))
		}
	}

	return reg, failures
}

func registerMCPServer(ctx context.Context, reg *mcp.Registry, srv config.MCPServer, allowPrivate bool) error {
	if srv.IsHTTP() {
		timeout := time.Duration(srv.TimeoutSeconds) * time.Second

		transport, err := mcp.StartHTTP(srv.URL, srv.Headers, timeout, allowPrivate)
		if err != nil {
			return err
		}

		client := mcp.NewClient(transport)
		if err := reg.RegisterExisting(ctx, srv.Name, client); err != nil {
			_ = client.Close()
			return err
		}

		return nil
	}

	return reg.Spawn(ctx, mcp.ServerConfig{
		Name:    srv.Name,
		Command: srv.Command,
		Args:    srv.Args,
		Env:     srv.Env,
	})
}
