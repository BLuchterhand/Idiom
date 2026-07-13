//go:build integration

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

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
var _ = ginkgo.Describe("Live MCP setup", ginkgo.Label("integration", "live"), func() {
	ginkgo.It("registers tools from the user's configured MCP servers", func() {
		if os.Getenv("IDIOM_LIVE_MCP") == "" {
			ginkgo.Skip(fmt.Sprint("set IDIOM_LIVE_MCP=1 to run the live MCP setup smoke test"))
		}
		repoRoot, err := filepath.Abs("../..")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		cfgPath := filepath.Join(repoRoot, "config.json")
		cfg, err := config.Load(cfgPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "config.Load(%s)", cfgPath)
		if len(cfg.MCP) == 0 {
			ginkgo.Skip(fmt.Sprint("config.json has no mcp entries to exercise"))
		}
		fmt.Fprintf(ginkgo.GinkgoWriter, "attempting to register %d MCP server(s)", len(cfg.MCP))
		for _, srv := range cfg.MCP {
			switch {
			case srv.IsHTTP():
				fmt.Fprintf(ginkgo.GinkgoWriter, "  - %q (http) %s", srv.Name, srv.URL)
			case srv.IsStdio():
				fmt.Fprintf(ginkgo.GinkgoWriter, "  - %q (stdio) %s %v", srv.Name, srv.Command, srv.Args)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		reg, fails := mcp.SetupRegistry(ctx, cfg.MCP, cfg.Workspace.AllowPrivateNetwork)
		if reg != nil {
			defer reg.Close()
		}
		for _, msg := range fails {
			fmt.Fprintf(ginkgo.GinkgoWriter, "failure: %s", msg)
		}
		gomega.Expect(reg).NotTo(gomega.BeNil(), "registry is nil even though config has MCP entries")

		tools := reg.Tools()
		names := make([]string, 0, len(tools))
		for name := range tools {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(ginkgo.GinkgoWriter, "registered %d tool(s):", len(names))
		for _, name := range names {
			desc := tools[name].Description
			if len(desc) > 80 {
				desc = desc[:77] + "..."
			}
			fmt.Fprintf(ginkgo.GinkgoWriter, "  - %s — %s", name, desc)
		}

		gomega.Expect(fails).To(gomega.BeEmpty(), "MCP setup failures are logged above")
		gomega.Expect(names).NotTo(gomega.BeEmpty(), "no tools registered")
	})
})
