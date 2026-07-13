package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

var _ = ginkgo.Describe("discovery query", func() {
	ginkgo.It("discovery query impact analysis writes layered artifact", func() {
		root := seedDiscoveryQueryGoRepo()
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:     "discovery_query",
			Purpose:  "impact_analysis",
			Path:     "service/service.go",
			Pattern:  "Target",
			Language: "go",
			MaxDepth: 2,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("discovery_query returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"artifact_path"`), fmt.Sprintf("expected compact discovery query summary, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"impact_analysis"`), fmt.Sprintf("expected compact discovery query summary, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"evidence"`), fmt.Sprintf("expected visible summary to include compact evidence excerpts, got:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"output_excerpt"`), fmt.Sprintf("expected visible summary to include compact evidence excerpts, got:\n%s", result.Output))

		packet := readDiscoveryQueryPacket(root, result.Output)
		gomega.Expect(packet.Purpose).To(gomega.Equal("impact_analysis"), fmt.Sprintf("unexpected packet header: %#v", packet))
		gomega.Expect(packet.Target).To(gomega.Equal("service/service.go"), fmt.Sprintf("unexpected packet header: %#v", packet))
		gomega.Expect(packet.Pattern).To(gomega.Equal("Target"), fmt.Sprintf("unexpected packet header: %#v", packet))

		for _, want := range []string{"file_outline", "text_anchor_search", "dependency_graph", "go_workspace_symbol", "go_references", "go_calls_from", "go_calls_into", "related_files", "history_symbol", "blame_range"} {
			gomega.Expect(packetHasLayer(packet, want)).To(gomega.BeTrue(), fmt.Sprintf("expected discovery query layer %q, got layers %#v", want, packetLayerNames(packet)))

		}
		gomega.Expect(strings.HasPrefix(packet.ArtifactPath, ".idiom/discovery/queries/")).To(gomega.BeTrue(), fmt.Sprintf("artifact should live under hidden discovery query dir, got %q", packet.ArtifactPath))

		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(packet.ArtifactPath))); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected artifact to exist: %v", err))
		}

		if exclude, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude")); err == nil && !strings.Contains(string(exclude), ".idiom/") {
			ginkgo.Fail(fmt.Sprintf("expected discovery artifacts to be locally ignored, got:\n%s", string(exclude)))
		}
	})
	ginkgo.It("discovery query uses fresh context index for orientation", func() {
		root := seedDiscoveryQueryGoRepo()
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:     "discovery_query",
			Purpose:  "impact_analysis",
			Path:     ".",
			Pattern:  "Target",
			Language: "go",
			MaxDepth: 2,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("discovery_query returned error: %v\n%s", result.Error, result.Output))

		packet := readDiscoveryQueryPacket(root, result.Output)
		gomega.Expect(packet.ContextIndexID).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected context index/view metadata in artifact, got %#v", packet))
		gomega.Expect(packet.ContextViews).To(gomega.HaveLen(1), fmt.Sprintf("expected context index/view metadata in artifact, got %#v", packet))
		gomega.Expect(packetHasLayer(packet, "context_pack")).To(gomega.BeTrue(), fmt.Sprintf("expected context_pack layer, got layers %#v", packetLayerNames(packet)))
		gomega.Expect(packetHasLayer(packet, "target_listing")).To(gomega.BeFalse(), fmt.Sprintf("fresh context graph should replace broad target listing, got layers %#v", packetLayerNames(packet)))

		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(packet.ContextViews[0]))); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected context view artifact to exist: %v", err))
		}
	})
	ginkgo.It("discovery query builds missing context index for orientation", func() {
		root := seedDiscoveryQueryGoRepo()
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:     "discovery_query",
			Purpose:  "impact_analysis",
			Path:     ".",
			Pattern:  "Target",
			Language: "go",
			MaxDepth: 2,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("discovery_query returned error: %v\n%s", result.Error, result.Output))

		packet := readDiscoveryQueryPacket(root, result.Output)
		gomega.Expect(packet.ContextFreshness).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing context index to rebuild through freshness gate, got %#v", packet.ContextFreshness))
		gomega.Expect(packet.ContextFreshness.FreshForScope).To(gomega.BeTrue(), fmt.Sprintf("expected missing context index to rebuild through freshness gate, got %#v", packet.ContextFreshness))
		gomega.Expect(packet.ContextFreshness.Refresh).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing context index to rebuild through freshness gate, got %#v", packet.ContextFreshness))
		gomega.Expect(packet.ContextFreshness.Refresh.Reason).To(gomega.Equal("missing_index"), fmt.Sprintf("expected missing context index to rebuild through freshness gate, got %#v", packet.ContextFreshness))
		gomega.Expect(packetHasLayer(packet, "context_pack")).To(gomega.BeTrue(), fmt.Sprintf("rebuilt context graph should generate context_pack layer, got layers %#v", packetLayerNames(packet)))
		gomega.Expect(packetHasLayer(packet, "target_listing")).To(gomega.BeFalse(), fmt.Sprintf("fresh context graph should replace broad target listing, got layers %#v", packetLayerNames(packet)))

	})
	ginkgo.It("discovery query refreshes unbounded stale context before graph orientation", func() {
		root := seedDiscoveryQueryGoRepo()
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		writeWorkspaceFile(filepath.Join(root, "service", "service.go"), `package service

func Target(v string) string {
	return v + "changed"
}
`)

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:     "discovery_query",
			Purpose:  "impact_analysis",
			Path:     ".",
			Pattern:  "Target",
			Language: "go",
			MaxDepth: 2,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("discovery_query returned error: %v\n%s", result.Error, result.Output))

		packet := readDiscoveryQueryPacket(root, result.Output)
		gomega.Expect(packet.ContextIndexID).NotTo(gomega.Equal(""), fmt.Sprintf("expected context index id to remain visible, got %#v", packet))
		gomega.Expect(packet.ContextFreshness).NotTo(gomega.BeNil(), fmt.Sprintf("expected discovery_query to refresh stale context before graph backing, got %#v", packet.ContextFreshness))
		gomega.Expect(packet.ContextFreshness.FreshForScope).To(gomega.BeTrue(), fmt.Sprintf("expected discovery_query to refresh stale context before graph backing, got %#v", packet.ContextFreshness))
		gomega.Expect(packet.ContextFreshness.Refresh).NotTo(gomega.BeNil(), fmt.Sprintf("expected discovery_query to refresh stale context before graph backing, got %#v", packet.ContextFreshness))
		gomega.Expect(packet.ContextFreshness.Refresh.Attempted).To(gomega.BeTrue(), fmt.Sprintf("expected discovery_query to refresh stale context before graph backing, got %#v", packet.ContextFreshness))
		gomega.Expect(packetHasLayer(packet, "context_pack")).To(gomega.BeTrue(), fmt.Sprintf("refreshed context graph should generate context_pack layer, got layers %#v", packetLayerNames(packet)))
		gomega.Expect(packetHasLayer(packet, "target_listing")).To(gomega.BeFalse(), fmt.Sprintf("refreshed context graph should replace live target listing, got layers %#v", packetLayerNames(packet)))

	})
	ginkgo.It("discovery query auto refreshes bounded stale context", func() {
		root := seedDiscoveryQueryGoRepo()
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})

		if result := mgr.Execute(context.Background(), ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		writeWorkspaceFile(filepath.Join(root, "service", "service.go"), `package service

func Target(v string) string {
	return v + "changed"
}
`)

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:     "discovery_query",
			Purpose:  "impact_analysis",
			Path:     "service",
			Pattern:  "Target",
			Language: "go",
			MaxDepth: 2,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("discovery_query returned error: %v\n%s", result.Error, result.Output))

		packet := readDiscoveryQueryPacket(root, result.Output)
		gomega.Expect(packetHasLayer(packet, "context_pack")).To(gomega.BeTrue(), fmt.Sprintf("bounded stale context should attempt context_pack refresh, got layers %#v", packetLayerNames(packet)))
		gomega.Expect(packetHasLayer(packet, "target_listing")).To(gomega.BeFalse(), fmt.Sprintf("bounded stale context should refresh and use context graph orientation, got layers %#v", packetLayerNames(packet)))
		gomega.Expect(packetGapsContain(packet, "fresh context graph supplied target orientation")).To(gomega.BeTrue(), fmt.Sprintf("expected refreshed context orientation gap, got %#v", packet.Gaps))

	})
	ginkgo.It("discovery query security review runs security recipe without external tools", func() {
		root := seedDiscoveryQueryGoRepo()
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			EnableShell:           true,
			EnableWrites:          true,
		})
		ginkgo.GinkgoT().Setenv("PATH", "")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:     "discovery_query",
			Purpose:  "security_review",
			Path:     ".",
			Language: "go",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("discovery_query returned error: %v\n%s", result.Error, result.Output))

		packet := readDiscoveryQueryPacket(root, result.Output)
		for _, want := range []string{"target_listing", "dependency_graph", "security_text_sweep", "semgrep_scan", "joern_scan"} {
			gomega.Expect(packetHasLayer(packet, want)).To(gomega.BeTrue(), fmt.Sprintf("expected security discovery query layer %q, got layers %#v", want, packetLayerNames(packet)))

		}
		gomega.Expect(packetLayerHasError(packet, "semgrep_scan")).To(gomega.BeTrue(), fmt.Sprintf("expected unavailable external security scans to be recorded as layer errors, got %#v", packet.Layers))
		gomega.Expect(packetLayerHasError(packet, "joern_scan")).To(gomega.BeTrue(), fmt.Sprintf("expected unavailable external security scans to be recorded as layer errors, got %#v", packet.Layers))

	})
	ginkgo.It("discovery query rejects unknown purpose", func() {
		mgr := NewManager(testWorkspaceConfig())
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "discovery_query", Purpose: "wander", Path: "."})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected unknown purpose rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("purpose must be"), fmt.Sprintf("expected unknown purpose rejection, got %#v", result))

	})
	ginkgo.It("discovery query checkpoints artifact after completed layer", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "service.go"), "package main\nfunc Target() {}\n")
		mgr := NewManager(config.Workspace{
			Root:           root,
			CWD:            root,
			MaxOutputBytes: 65536,
			MaxFileBytes:   65536,
		})

		runner := &discoveryQueryRunner{
			manager: mgr,
			ctx:     context.Background(),
			seen:    make(map[string]struct{}),
			packet: discoveryQueryPacket{
				SchemaVersion: discoveryQuerySchemaVersion,
				ID:            "checkpoint-test",
				Purpose:       DiscoveryQueryPurposeImpactAnalysis,
				Target:        ".",
				ArtifactPath:  ".idiom/discovery/queries/checkpoint-test.json",
			},
		}

		if result, ok := runner.addLayerResult(DiscoveryLayerTargetListing, ToolRequest{Tool: ToolNameListFiles, Path: "."}); !ok || result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("expected first layer to complete, ok=%t result=%#v", ok, result))
		}

		packet := readDiscoveryQueryPacket(root, runner.summaryJSON())
		gomega.Expect(packetHasLayer(packet, DiscoveryLayerTargetListing)).To(gomega.BeTrue(), fmt.Sprintf("checkpoint artifact should preserve completed layers: %#v", packet.Layers))
		gomega.Expect(packet.Status).To(gomega.Equal(""), fmt.Sprintf("completed checkpoint should not classify a long-running query as failure/interruption: %#v", packet))

	})
	ginkgo.It("discovery query facts rank text anchor candidates", func() {
		requireContentSearchBackend()

		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "internal", "runtime", "agent", "runner.go"), "package agent\nfunc TargetSourceOnly() {}\n")
		writeWorkspaceFile(filepath.Join(root, "internal", "runtime", "agent", "runner_test.go"), "package agent\nfunc TestRunner() {}\n")
		writeWorkspaceFile(filepath.Join(root, "internal", "runtime", "modepolicy", "modes.go"), "package modepolicy\n")
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
		})
		runner := &discoveryQueryRunner{
			manager: mgr,
			ctx:     context.Background(),
			seen:    make(map[string]struct{}),
			packet: discoveryQueryPacket{
				SchemaVersion: discoveryQuerySchemaVersion,
				ID:            "facts-test",
				Purpose:       DiscoveryQueryPurposeArchitectureBoundary,
				Target:        ".",
				ArtifactPath:  ".idiom/discovery/queries/facts-test.json",
			},
		}

		if result, ok := runner.addLayerResult(DiscoveryLayerTargetListing, ToolRequest{Tool: ToolNameListFiles, Path: "internal", Recursive: true, MaxDepth: 4}); !ok || result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("target listing layer failed, ok=%t result=%#v", ok, result))
		}
		if result, ok := runner.addLayerResult(DiscoveryLayerTextAnchorSearch, ToolRequest{Tool: ToolNameSearchContent, Path: ".", Pattern: "TargetSourceOnly"}); !ok || result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("text anchor layer failed, ok=%t result=%#v", ok, result))
		}

		facts := runner.toolResultFacts()
		gomega.Expect(facts).NotTo(gomega.BeNil(), "discovery_query facts were nil")
		gomega.Expect(facts.DiscoveryStrength).To(gomega.Equal(toolfacts.DiscoveryStrengthSourceOwner), fmt.Sprintf("discovery_query facts should identify source-owner candidates, got %#v", facts))
		gomega.Expect(facts.SourceOwnerFound).To(gomega.BeTrue(), fmt.Sprintf("discovery_query facts should identify source-owner candidates, got %#v", facts))
		gomega.Expect(facts.ArtifactIDs).To(gomega.ConsistOf("facts-test", ".idiom/discovery/queries/facts-test.json"))

		if got, want := facts.CandidatePaths[0], "internal/runtime/agent/runner.go"; got != want {
			ginkgo.Fail(fmt.Sprintf("expected text-anchor source file to rank first, got paths=%#v ranked=%#v raw=%#v", facts.CandidatePaths, facts.RankedDiscoveryCandidates, runner.candidates))
		}
		if got := facts.RankedDiscoveryCandidates[0]; got.Path != "internal/runtime/agent/runner.go" || got.LayerName != DiscoveryLayerTextAnchorSearch {
			ginkgo.Fail(fmt.Sprintf("ranked discovery candidate = %#v, want text-anchor runner.go first", got))
		}
		gomega.Expect(slices.Contains(facts.CandidatePaths, ".idiom/discovery/queries/facts-test.json")).To(gomega.BeFalse(), fmt.Sprintf("discovery artifact should not become a source candidate: %#v", facts.CandidatePaths))

	})
	ginkgo.It("discovery query records skipped layer after parent cancellation", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "service.go"), "package main\nfunc Target() {}\n")
		mgr := NewManager(config.Workspace{
			Root:           root,
			CWD:            root,
			MaxOutputBytes: 65536,
			MaxFileBytes:   65536,
		})

		ctx, cancel := context.WithCancel(context.Background())
		runner := &discoveryQueryRunner{
			manager: mgr,
			ctx:     ctx,
			seen:    make(map[string]struct{}),
			packet: discoveryQueryPacket{
				SchemaVersion: discoveryQuerySchemaVersion,
				ID:            "parent-canceled-test",
				Purpose:       DiscoveryQueryPurposeImpactAnalysis,
				Target:        ".",
				ArtifactPath:  ".idiom/discovery/queries/parent-canceled-test.json",
			},
		}

		if result, ok := runner.addLayerResult(DiscoveryLayerTargetListing, ToolRequest{Tool: ToolNameListFiles, Path: "."}); !ok || result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("expected first layer to complete, ok=%t result=%#v", ok, result))
		}
		cancel()
		if _, ok := runner.addLayerResult(DiscoveryLayerTextAnchorSearch, ToolRequest{Tool: ToolNameSearchContent, Path: ".", Pattern: "Target"}); ok {
			ginkgo.Fail("layer scheduled after parent cancellation should be skipped")
		}
		gomega.Expect(runner.writeArtifact()).To(gomega.Succeed())

		packet := readDiscoveryQueryPacket(root, runner.summaryJSON())
		gomega.Expect(packet.Status).To(gomega.Equal("partial_interrupted"), fmt.Sprintf("expected parent interruption metadata, got %#v", packet))
		gomega.Expect(packet.TimeoutOwner).To(gomega.Equal("parent_context"), fmt.Sprintf("expected parent interruption metadata, got %#v", packet))
		gomega.Expect(packetHasLayer(packet, DiscoveryLayerTargetListing)).To(gomega.BeTrue(), fmt.Sprintf("interrupted artifact should preserve completed layers: %#v", packet.Layers))
		gomega.Expect(slices.Contains(packet.SkippedLayers, DiscoveryLayerTextAnchorSearch)).To(gomega.BeTrue(), fmt.Sprintf("interrupted artifact should record skipped layer, got %#v", packet.SkippedLayers))
		gomega.Expect(packetGapsContain(packet, "canceled")).To(gomega.BeTrue(), fmt.Sprintf("interrupted artifact should explain parent cancellation gap, got %#v", packet.Gaps))

	})
	ginkgo.It("discovery query normalizes architecture usefulness aliases", func() {
		for _, purpose := range []string{
			"tool_usefulness",
			"discovery_tools",
			"usefulness",
			"Run a deterministic layered discovery recipe to identify subagent definitions and orchestration patterns.",
		} {
			if got := NormalizeDiscoveryQueryPurpose(purpose); got != "architecture_boundary" {
				ginkgo.Fail(fmt.Sprintf("NormalizeDiscoveryQueryPurpose(%q) = %q, want architecture_boundary", purpose, got))
			}
		}
	})
	ginkgo.It("discovery query language coverage follows registry", func() {
		for _, lang := range semanticlanguage.LanguageRegistry() {
			glob := globForDiscoveryLanguage(lang.Name)
			gomega.Expect(glob).NotTo(gomega.Equal(""), fmt.Sprintf("%s should have a discovery glob", lang.Name))

			for _, ext := range lang.Extensions {
				suffix := strings.TrimPrefix(ext, ".")
				gomega.Expect(glob).To(gomega.ContainSubstring(suffix), fmt.Sprintf("%s discovery glob %q missing extension %q", lang.Name, glob, ext))

			}
		}

		for _, alias := range []string{"tsx", ".mts", "d.cts", "jsx", "mjs", "javascript"} {
			if got := normalizeDiscoveryQueryLanguage(alias); got != "typescript" {
				ginkgo.Fail(fmt.Sprintf("normalizeDiscoveryQueryLanguage(%q) = %q, want typescript", alias, got))
			}
		}
	})
})

func seedDiscoveryQueryGoRepo() string {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/discoveryquery\n\ngo 1.22\n")
	writeWorkspaceFile(filepath.Join(root, "service", "service.go"), `package service

func Target(v string) string {
	return helper(v)
}

func helper(v string) string {
	return v
}
`)
	writeWorkspaceFile(filepath.Join(root, "service", "service_test.go"), `package service

import "testing"

func TestTarget(t *testing.T) {
	if Target("x") != "x" {
		t.Fatal("bad target")
	}
}
`)

	if _, err := exec.LookPath("git"); err == nil {
		runTestCommand(root, "git", "init")
		runTestCommand(root, "git", "config", "user.email", "test@example.com")
		runTestCommand(root, "git", "config", "user.name", "Test User")
		runTestCommand(root, "git", "add", ".")
		runTestCommand(root, "git", "commit", "-m", "initial")
	}

	return root
}

func readDiscoveryQueryPacket(root string, summaryText string) discoveryQueryPacket {
	ginkgo.GinkgoHelper()

	var summary discoveryQuerySummary
	gomega.Expect(json.Unmarshal([]byte(summaryText), &summary)).To(gomega.Succeed())

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(summary.ArtifactPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read artifact: %v", err))

	var packet discoveryQueryPacket
	gomega.Expect(json.Unmarshal(data, &packet)).To(gomega.Succeed())

	return packet
}

func packetHasLayer(packet discoveryQueryPacket, name string) bool {
	return slices.Contains(packetLayerNames(packet), name)
}

func packetLayerHasError(packet discoveryQueryPacket, name string) bool {
	for _, layer := range packet.Layers {
		if layer.Name == name && strings.TrimSpace(layer.Error) != "" {
			return true
		}
	}

	return false
}

func packetLayerNames(packet discoveryQueryPacket) []string {
	names := make([]string, 0, len(packet.Layers))
	for _, layer := range packet.Layers {
		names = append(names, layer.Name)
	}

	return names
}

func packetGapsContain(packet discoveryQueryPacket, text string) bool {
	for _, gap := range packet.Gaps {
		if strings.Contains(gap, text) {
			return true
		}
	}

	return false
}
