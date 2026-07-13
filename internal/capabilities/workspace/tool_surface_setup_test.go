package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
)

const (
	workspaceMCPStdioHelperEnv      = "GO_WANT_WORKSPACE_MCP_STDIO_HELPER"
	workspaceMCPStdioScenarioEnv    = "IDIOM_WORKSPACE_MCP_STDIO_SCENARIO"
	workspaceMCPStdioStartMarkerEnv = "IDIOM_WORKSPACE_MCP_STDIO_START_MARKER"
)

var _ = ginkgo.Describe("tool surface setup", func() {
	ginkgo.It("configure tool surface list discovery does not initialize mcp without allow rule", func() {
		startMarker := filepath.Join(ginkgo.GinkgoT().TempDir(), "mcp-started")
		cfg := config.Default()
		cfg.MCP = []config.MCPServer{workspaceMCPStdioFixtureConfig("unexpected-start", startMarker)}

		setup := ConfigureToolSurface(context.Background(), cfg, ConfiguredToolSurfaceOptions{
			Profile: ToolProfileDiscovery,
			List:    true,
		})
		defer setup.Close()

		_, markerErr := os.Stat(startMarker)
		gomega.Expect(markerErr).To(gomega.MatchError(os.ErrNotExist), "discovery list without an MCP allow rule started the configured MCP peer")
		gomega.Expect(setup.MCPRegistry).To(gomega.BeNil())
		gomega.Expect(setup.MCPFailures).To(gomega.BeEmpty())
		for _, tool := range setup.ListToolNamesForProfile(ToolProfileDiscovery) {
			gomega.Expect(strings.HasPrefix(tool, "mcp:")).To(gomega.BeFalse(), fmt.Sprintf("discovery list without allow rule advertised MCP tool %q", tool))

		}
	})
	ginkgo.It("configure tool surface list discovery advertises allowed mcp", func() {
		cfg := config.Default()
		cfg.MCP = []config.MCPServer{workspaceMCPStdioFixtureConfig("serve-tool", "")}
		cfg.Workspace.ToolRules = map[string][]config.ToolRule{
			"mcp:exa/web_search_exa": {{Pattern: "*", Decision: "allow"}},
		}

		setup := ConfigureToolSurface(context.Background(), cfg, ConfiguredToolSurfaceOptions{
			Profile: ToolProfileDiscovery,
			List:    true,
		})
		defer setup.Close()
		gomega.Expect(slices.Contains(setup.ListToolNamesForProfile(ToolProfileDiscovery), "mcp:exa/web_search_exa")).To(gomega.BeTrue(), fmt.Sprintf("discovery list did not advertise explicitly allowed MCP tool"))

	})
	ginkgo.It("configure tool surface list filters external discovery tools", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())

		for _, name := range []string{"lookup.sh", "mutate.sh"} {
			gomega.Expect(os.WriteFile(filepath.Join(root, "scripts", name), []byte("#!/bin/sh\necho ok\n"), 0o755)).To(gomega.Succeed())

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
		gomega.Expect(slices.Contains(discoveryTools, "lookup_tool")).To(gomega.BeTrue(), fmt.Sprintf("discovery list missing read-only external tool: %#v", discoveryTools))
		gomega.Expect(slices.Contains(discoveryTools, "mutating_tool")).To(gomega.BeFalse(), fmt.Sprintf("discovery list advertised mutating external tool: %#v", discoveryTools))

		defaultTools := setup.ListToolNamesForProfile(ToolProfileDefault)
		gomega.Expect(defaultTools).To(gomega.ContainElement("lookup_tool"), fmt.Sprintf("default list should advertise available external tools: %#v", defaultTools))
		gomega.Expect(defaultTools).To(gomega.ContainElement("mutating_tool"), fmt.Sprintf("default list should advertise available external tools: %#v", defaultTools))

	})
})

func workspaceMCPStdioFixtureConfig(scenario, startMarker string) config.MCPServer {
	ginkgo.GinkgoHelper()

	executable, err := os.Executable()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	environment := []string{
		workspaceMCPStdioHelperEnv + "=1",
		workspaceMCPStdioScenarioEnv + "=" + scenario,
	}
	if startMarker != "" {
		environment = append(environment, workspaceMCPStdioStartMarkerEnv+"="+startMarker)
	}
	return config.MCPServer{
		Name:    "exa",
		Command: executable,
		Args:    []string{"-test.run=^TestWorkspaceMCPStdioHelperProcess$"},
		Env:     environment,
	}
}

// TestWorkspaceMCPStdioHelperProcess serves the config-driven MCP integration
// specs through the production stdio transport without opening a loopback socket.
func TestWorkspaceMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv(workspaceMCPStdioHelperEnv) != "1" {
		return
	}
	if marker := os.Getenv(workspaceMCPStdioStartMarkerEnv); marker != "" {
		if err := os.WriteFile(marker, []byte("started\n"), 0o600); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "write MCP start marker: %v\n", err)
			os.Exit(3)
		}
	}
	if os.Getenv(workspaceMCPStdioScenarioEnv) == "unexpected-start" {
		return
	}
	if err := serveWorkspaceMCPStdioPeer(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
}

func serveWorkspaceMCPStdioPeer() error {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int64  `json:"id"`
			Method  string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return fmt.Errorf("decode workspace MCP request: %w", err)
		}
		if request.JSONRPC != "2.0" {
			return fmt.Errorf("workspace MCP peer received JSON-RPC version %q", request.JSONRPC)
		}
		if request.Method == "notifications/initialized" {
			continue
		}

		var result json.RawMessage
		switch request.Method {
		case "initialize":
			result = json.RawMessage(`{"protocolVersion":"` + mcp.ProtocolVersion + `"}`)
		case "tools/list":
			result = json.RawMessage(`{"tools":[{"name":"web_search_exa","description":"Search the web"}]}`)
		default:
			return fmt.Errorf("workspace MCP peer received unsupported method %q", request.Method)
		}
		if err := encoder.Encode(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Result  json.RawMessage `json:"result"`
		}{JSONRPC: "2.0", ID: request.ID, Result: result}); err != nil {
			return fmt.Errorf("encode workspace MCP response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read workspace MCP request: %w", err)
	}
	return nil
}
