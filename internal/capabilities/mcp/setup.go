package mcp

import (
	"context"
	"fmt"
	"time"

	"idiom/internal/platform/config"
)

// SetupRegistry constructs a Registry from configured MCP servers.
//
// A broken server is reported in the returned failures slice but does not stop
// sibling servers from registering. Callers decide whether those failures are
// terminal for their surface.
func SetupRegistry(ctx context.Context, servers []config.MCPServer, allowPrivate bool) (*Registry, []string) {
	if len(servers) == 0 {
		return nil, nil
	}

	reg := NewRegistry()

	var failures []string

	for _, srv := range servers {
		if err := registerConfiguredServer(ctx, reg, srv, allowPrivate); err != nil {
			failures = append(failures, fmt.Sprintf("mcp %q: %v", srv.Name, err))
		}
	}

	return reg, failures
}

func registerConfiguredServer(ctx context.Context, reg *Registry, srv config.MCPServer, allowPrivate bool) error {
	if srv.IsHTTP() {
		timeout := time.Duration(srv.TimeoutSeconds) * time.Second

		transport, err := StartHTTP(srv.URL, srv.Headers, timeout, allowPrivate)
		if err != nil {
			return err
		}

		client := NewClient(transport)
		if err := reg.RegisterExisting(ctx, srv.Name, client); err != nil {
			_ = client.Close()
			return err
		}

		return nil
	}

	return reg.Spawn(ctx, ServerConfig{
		Name:    srv.Name,
		Command: srv.Command,
		Args:    srv.Args,
		Env:     srv.Env,
	})
}
