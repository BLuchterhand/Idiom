package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"idiom/internal/platform/config"
)

func TestConfigureToolSurfaceListDiscoveryDoesNotInitializeMCPWithoutAllowRule(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "mcp should not be contacted", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Workspace.AllowPrivateNetwork = true
	cfg.MCP = []config.MCPServer{{
		Name:           "exa",
		URL:            srv.URL,
		TimeoutSeconds: 5,
	}}

	setup := ConfigureToolSurface(context.Background(), cfg, ConfiguredToolSurfaceOptions{
		Profile: ToolProfileDiscovery,
		List:    true,
	})
	defer setup.Close()

	if got := calls.Load(); got != 0 {
		t.Fatalf("discovery list without MCP allow rule initialized MCP %d time(s)", got)
	}
	for _, tool := range setup.ListToolNamesForProfile(ToolProfileDiscovery) {
		if strings.HasPrefix(tool, "mcp:") {
			t.Fatalf("discovery list without allow rule advertised MCP tool %q", tool)
		}
	}
}

func TestConfigureToolSurfaceListDiscoveryAdvertisesAllowedMCP(t *testing.T) {
	srv := fakeMCPServer(t, "web_search_exa", "Search the web", "")
	defer srv.Close()

	cfg := config.Default()
	cfg.Workspace.AllowPrivateNetwork = true
	cfg.MCP = []config.MCPServer{{
		Name:           "exa",
		URL:            srv.URL,
		TimeoutSeconds: 5,
	}}
	cfg.Workspace.ToolRules = map[string][]config.ToolRule{
		"mcp:exa/web_search_exa": {{Pattern: "*", Decision: "allow"}},
	}

	setup := ConfigureToolSurface(context.Background(), cfg, ConfiguredToolSurfaceOptions{
		Profile: ToolProfileDiscovery,
		List:    true,
	})
	defer setup.Close()

	if !slices.Contains(setup.ListToolNamesForProfile(ToolProfileDiscovery), "mcp:exa/web_search_exa") {
		t.Fatalf("discovery list did not advertise explicitly allowed MCP tool")
	}
}

func TestConfigureToolSurfaceListFiltersExternalDiscoveryTools(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	for _, name := range []string{"lookup.sh", "mutate.sh"} {
		if err := os.WriteFile(filepath.Join(root, "scripts", name), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
			t.Fatalf("write script %s: %v", name, err)
		}
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.ExternalTools = []config.ExternalToolDef{
		{Name: "lookup_tool", Command: "./scripts/lookup.sh", ReadOnly: true, Discovery: true},
		{Name: "mutating_tool", Command: "./scripts/mutate.sh", ReadOnly: false, Discovery: true},
	}

	setup := ConfigureToolSurface(context.Background(), cfg, ConfiguredToolSurfaceOptions{
		Profile: ToolProfileDiscovery,
		List:    true,
	})
	defer setup.Close()

	discoveryTools := setup.ListToolNamesForProfile(ToolProfileDiscovery)
	if !slices.Contains(discoveryTools, "lookup_tool") {
		t.Fatalf("discovery list missing read-only external tool: %#v", discoveryTools)
	}
	if slices.Contains(discoveryTools, "mutating_tool") {
		t.Fatalf("discovery list advertised mutating external tool: %#v", discoveryTools)
	}

	defaultTools := setup.ListToolNamesForProfile(ToolProfileDefault)
	if !slices.Contains(defaultTools, "lookup_tool") || !slices.Contains(defaultTools, "mutating_tool") {
		t.Fatalf("default list should advertise available external tools: %#v", defaultTools)
	}
}
