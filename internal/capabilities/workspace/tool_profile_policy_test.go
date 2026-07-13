package workspace

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("tool profile policy", func() {
	ginkgo.It("tool allowed for discovery profile allows read only discovery tool", func() {
		decision := ToolAllowedForProfile("context_status", ToolProfileDiscovery)
		gomega.Expect(decision.Allowed).To(gomega.BeTrue(), fmt.Sprintf("context_status should be allowed for discovery profile: %#v", decision))

	})
	ginkgo.It("tool request allowed for discovery profile denies refresh capable context status", func() {
		decision := ToolRequestAllowedForProfile(ToolRequest{
			Tool:    "context_status",
			Refresh: true,
		}, ToolProfileDiscovery)
		gomega.Expect(decision.Allowed).To(gomega.BeFalse(), "context_status refresh request should be denied for discovery profile")
		gomega.Expect(decision.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected refresh denial: %#v", decision))
		gomega.Expect(decision.Reason).To(gomega.ContainSubstring("refresh"), fmt.Sprintf("unexpected refresh denial: %#v", decision))

	})
	ginkgo.It("tool request allowed for discovery profile allows context status report only", func() {
		for _, req := range []ToolRequest{
			{Tool: "context_status"},
			{Tool: "context_status", FreshnessPolicy: "report_only"},
			{Tool: "context_status", FreshnessPolicy: "allow_stale"},
		} {
			decision := ToolRequestAllowedForProfile(req, ToolProfileDiscovery)
			gomega.Expect(decision.Allowed).To(gomega.BeTrue(), fmt.Sprintf("context_status report-only request should be allowed: req=%#v decision=%#v", req, decision))

		}
	})
	ginkgo.It("tool request allowed for discovery profile requires report only graph reads", func() {
		denied := ToolRequestAllowedForProfile(ToolRequest{Tool: "context_query", Query: "callers of Run"}, ToolProfileDiscovery)
		gomega.Expect(denied.Allowed).To(gomega.BeFalse(), "context_query default read-through request should be denied for discovery profile")
		gomega.Expect(denied.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("unexpected context_query denial: %#v", denied))
		gomega.Expect(denied.Reason).To(gomega.ContainSubstring("freshness_policy"), fmt.Sprintf("unexpected context_query denial: %#v", denied))

		allowed := ToolRequestAllowedForProfile(ToolRequest{Tool: "context_query", Query: "callers of Run", FreshnessPolicy: "report_only"}, ToolProfileDiscovery)
		gomega.Expect(allowed.Allowed).To(gomega.BeTrue(), fmt.Sprintf("context_query report-only request should be allowed: %#v", allowed))

	})
	ginkgo.It("tool allowed for discovery profile denies descriptor local artifact tools", func() {
		for _, tool := range []string{"context_pack", "context_impact", "discovery_query"} {
			gomega.Expect(builtInToolSurfaceDiscoveryLocalArtifacts(tool)).To(gomega.BeTrue(), fmt.Sprintf("%s must remain descriptor-declared as a local artifact writer", tool))

		}

		localArtifactTools := discoveryLocalArtifactToolsFromCatalog()
		gomega.Expect(localArtifactTools).NotTo(gomega.BeEmpty(), "test setup expected at least one descriptor-declared local artifact tool")

		for _, tool := range localArtifactTools {
			decision := ToolAllowedForProfile(tool, ToolProfileDiscovery)
			gomega.Expect(decision.Allowed).To(gomega.BeFalse(), fmt.Sprintf("%s should be denied for discovery profile because descriptor metadata says it writes local artifacts", tool))
			gomega.Expect(decision.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("%s denial should name local artifact writes, got %#v", tool, decision))
			gomega.Expect(decision.Reason).To(gomega.ContainSubstring("context artifacts"), fmt.Sprintf("%s denial should name local artifact writes, got %#v", tool, decision))

		}
	})
	ginkgo.It("discovery profile local artifact query projects tool surface metadata", func() {
		for _, spec := range builtInToolSurfaceCatalog().specsSnapshot() {
			if got := builtInToolSurfaceDiscoveryLocalArtifacts(spec.Tool); got != spec.DiscoveryLocalArtifacts {
				ginkgo.Fail(fmt.Sprintf("%s local artifact query = %v, want descriptor metadata %v", spec.Tool, got, spec.DiscoveryLocalArtifacts))
			}
		}
	})
	ginkgo.It("tool allowed for mcp discovery profile allows descriptor local artifact tools", func() {
		localArtifactTools := discoveryLocalArtifactToolsFromCatalog()
		gomega.Expect(localArtifactTools).NotTo(gomega.BeEmpty(), "test setup expected at least one descriptor-declared local artifact tool")

		for _, tool := range localArtifactTools {
			decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery)
			gomega.Expect(decision.Allowed).To(gomega.BeTrue(), fmt.Sprintf("%s should be allowed for mcp_discovery profile, got %#v", tool, decision))

		}
	})
})

func discoveryLocalArtifactToolsFromCatalog() []string {
	var tools []string
	for _, spec := range builtInToolSurfaceCatalog().specsSnapshot() {
		if spec.DiscoveryLocalArtifacts {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}

var _ = ginkgo.Describe("tool profile policy", func() {
	ginkgo.It("tool allowed for mcp discovery profile denies mutation shell and unknown effects", func() {
		for _, tool := range []string{"write_file", "apply_patch", "shell", "context_trace_pack", "project_external"} {
			decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery)
			gomega.Expect(decision.Allowed).To(gomega.BeFalse(), fmt.Sprintf("%s should be denied for mcp_discovery profile", tool))
			gomega.Expect(decision.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("%s denial should be structured, got %#v", tool, decision))
			gomega.Expect(decision.Reason).NotTo(gomega.BeEmpty(), fmt.Sprintf("%s denial should be structured, got %#v", tool, decision))

		}
	})
	ginkgo.It("tool allowed for mcp discovery profile allows read only discovery tools", func() {
		for _, tool := range []string{
			"context_status", "context_query", "context_zoom",
			"discovery_query", "structural_search", "dependency_graph",
			"history_range", "history_symbol", "blame_range",
			"ctags_symbols", "code_navigate", "harness_audit",
			"recovery_inspect", "recovery_status",
		} {
			decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery)
			gomega.Expect(decision.Allowed).To(gomega.BeTrue(), fmt.Sprintf("%s should be allowed for mcp_discovery profile, got %#v", tool, decision))

		}
	})
	ginkgo.It("tool allowed for discovery profile denies mutation shell and unknown effects", func() {
		for _, tool := range []string{"write_file", "apply_patch", "shell", "context_trace_pack", "project_external"} {
			decision := ToolAllowedForProfile(tool, ToolProfileDiscovery)
			gomega.Expect(decision.Allowed).To(gomega.BeFalse(), fmt.Sprintf("%s should be denied for discovery profile", tool))
			gomega.Expect(decision.Code).To(gomega.Equal("profile_denied"), fmt.Sprintf("%s denial should be structured, got %#v", tool, decision))
			gomega.Expect(decision.Reason).NotTo(gomega.BeEmpty(), fmt.Sprintf("%s denial should be structured, got %#v", tool, decision))

		}
	})
	ginkgo.It("tool profile denied result is stable and does not echo request values", func() {
		privatePath := "/home/example/.ssh/id_ed25519"
		secretSentinel := "P75_POLICY_SECRET_SENTINEL_DO_NOT_LEAK"
		decision := ToolRequestAllowedForProfile(ToolRequest{
			Tool:    "write_file",
			Path:    privatePath,
			Content: secretSentinel,
		}, ToolProfileDiscovery)
		gomega.Expect(decision.Allowed).To(gomega.BeFalse(), "write_file should be denied for discovery profile")

		result := ToolProfileDeniedResult("write_file", ToolProfileDiscovery, decision)
		gomega.Expect(result.Tool).To(gomega.Equal("write_file"), fmt.Sprintf("unexpected denial result identity: %#v", result))
		gomega.Expect(result.Summary).To(gomega.Equal("Tool request denied by discovery profile"), fmt.Sprintf("unexpected denial result identity: %#v", result))

		for _, want := range []string{"profile_denied", "read_only_profile"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("denial result should contain stable %q marker: %#v", want, result))

		}

		for _, forbidden := range []string{privatePath, secretSentinel} {
			gomega.Expect(result.Summary).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("denial result leaked request value %q: %#v", forbidden, result))
			gomega.Expect(result.Error).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("denial result leaked request value %q: %#v", forbidden, result))
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("denial result leaked request value %q: %#v", forbidden, result))

		}
	})
	ginkgo.It("normalize tool profile rejects unknown profile", func() {
		if _, err := NormalizeToolProfile("verify"); err == nil {
			ginkgo.Fail("expected unknown profile to be rejected")
		}
	})
})
