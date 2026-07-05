//go:build integration

package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
)

// TestLive_SetupMCPRegistryAgainstUserConfig drives mcp.SetupRegistry
// against the repo's real config.json. Doubly gated:
//
//   - `//go:build integration` keeps it out of `go test ./...`. Run with
//     `go test -tags=integration ./internal/surfaces/tui/app/`.
//   - IDIOM_LIVE_MCP=1 is required even with the build tag, so the test
//     never accidentally hits the network in CI configurations that
//     enable integration tags.
//
// Useful as a one-shot "did my MCP wiring work" smoke after editing
// config.json or when bringing up a new MCP server. Logs every
// registered tool so you can eyeball the catalog.
func TestLive_SetupMCPRegistryAgainstUserConfig(t *testing.T) {
	if os.Getenv("IDIOM_LIVE_MCP") == "" {
		t.Skip("set IDIOM_LIVE_MCP=1 to run the live MCP setup smoke test")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	cfgPath := filepath.Join(repoRoot, "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", cfgPath, err)
	}
	if len(cfg.MCP) == 0 {
		t.Skip("config.json has no mcp entries to exercise")
	}
	t.Logf("attempting to register %d MCP server(s)", len(cfg.MCP))
	for _, srv := range cfg.MCP {
		switch {
		case srv.IsHTTP():
			t.Logf("  - %q (http) %s", srv.Name, srv.URL)
		case srv.IsStdio():
			t.Logf("  - %q (stdio) %s %v", srv.Name, srv.Command, srv.Args)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	reg, fails := mcp.SetupRegistry(ctx, cfg.MCP, cfg.Workspace.AllowPrivateNetwork)
	if reg != nil {
		defer reg.Close()
	}
	for _, msg := range fails {
		t.Logf("failure: %s", msg)
	}
	if reg == nil {
		t.Fatal("registry is nil even though config has MCP entries")
	}

	tools := reg.Tools()
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("registered %d tool(s):", len(names))
	for _, name := range names {
		desc := tools[name].Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		t.Logf("  - %s — %s", name, desc)
	}

	if len(fails) > 0 {
		t.Fatalf("%d MCP setup failure(s) — see logs above", len(fails))
	}
	if len(names) == 0 {
		t.Fatal("no tools registered")
	}
}
