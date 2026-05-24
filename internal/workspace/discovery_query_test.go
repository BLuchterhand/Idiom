package workspace

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestDiscoveryQueryImpactAnalysisWritesLayeredArtifact(t *testing.T) {
	root := seedDiscoveryQueryGoRepo(t)
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
	if result.Error != "" {
		t.Fatalf("discovery_query returned error: %v\n%s", result.Error, result.Output)
	}
	if !strings.Contains(result.Output, `"artifact_path"`) || !strings.Contains(result.Output, `"impact_analysis"`) {
		t.Fatalf("expected compact discovery query summary, got:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, `"evidence"`) || !strings.Contains(result.Output, `"output_excerpt"`) {
		t.Fatalf("expected visible summary to include compact evidence excerpts, got:\n%s", result.Output)
	}

	packet := readDiscoveryQueryPacket(t, root, result.Output)
	if packet.Purpose != "impact_analysis" || packet.Target != "service/service.go" || packet.Pattern != "Target" {
		t.Fatalf("unexpected packet header: %#v", packet)
	}
	for _, want := range []string{"file_outline", "text_anchor_search", "dependency_graph", "go_workspace_symbol", "go_references", "go_calls_from", "go_calls_into", "related_files", "history_symbol", "blame_range"} {
		if !packetHasLayer(packet, want) {
			t.Fatalf("expected discovery query layer %q, got layers %#v", want, packetLayerNames(packet))
		}
	}
	if !strings.HasPrefix(packet.ArtifactPath, ".idiom/discovery/queries/") {
		t.Fatalf("artifact should live under hidden discovery query dir, got %q", packet.ArtifactPath)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(packet.ArtifactPath))); err != nil {
		t.Fatalf("expected artifact to exist: %v", err)
	}
	if exclude, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude")); err == nil && !strings.Contains(string(exclude), ".idiom/") {
		t.Fatalf("expected discovery artifacts to be locally ignored, got:\n%s", string(exclude))
	}
}

func TestDiscoveryQueryUsesFreshContextIndexForOrientation(t *testing.T) {
	root := seedDiscoveryQueryGoRepo(t)
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
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:     "discovery_query",
		Purpose:  "impact_analysis",
		Path:     ".",
		Pattern:  "Target",
		Language: "go",
		MaxDepth: 2,
	})
	if result.Error != "" {
		t.Fatalf("discovery_query returned error: %v\n%s", result.Error, result.Output)
	}
	packet := readDiscoveryQueryPacket(t, root, result.Output)
	if packet.ContextIndexID == "" || len(packet.ContextViews) != 1 {
		t.Fatalf("expected context index/view metadata in artifact, got %#v", packet)
	}
	if !packetHasLayer(packet, "context_pack") {
		t.Fatalf("expected context_pack layer, got layers %#v", packetLayerNames(packet))
	}
	if packetHasLayer(packet, "target_listing") {
		t.Fatalf("fresh context graph should replace broad target listing, got layers %#v", packetLayerNames(packet))
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(packet.ContextViews[0]))); err != nil {
		t.Fatalf("expected context view artifact to exist: %v", err)
	}
}

func TestDiscoveryQueryBuildsMissingContextIndexForOrientation(t *testing.T) {
	root := seedDiscoveryQueryGoRepo(t)
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
	if result.Error != "" {
		t.Fatalf("discovery_query returned error: %v\n%s", result.Error, result.Output)
	}
	packet := readDiscoveryQueryPacket(t, root, result.Output)
	if packet.ContextFreshness == nil || !packet.ContextFreshness.FreshForScope || packet.ContextFreshness.Refresh == nil || packet.ContextFreshness.Refresh.Reason != "missing_index" {
		t.Fatalf("expected missing context index to rebuild through freshness gate, got %#v", packet.ContextFreshness)
	}
	if !packetHasLayer(packet, "context_pack") {
		t.Fatalf("rebuilt context graph should generate context_pack layer, got layers %#v", packetLayerNames(packet))
	}
	if packetHasLayer(packet, "target_listing") {
		t.Fatalf("fresh context graph should replace broad target listing, got layers %#v", packetLayerNames(packet))
	}
}

func TestDiscoveryQueryRefreshesUnboundedStaleContextBeforeGraphOrientation(t *testing.T) {
	root := seedDiscoveryQueryGoRepo(t)
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
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service

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
	if result.Error != "" {
		t.Fatalf("discovery_query returned error: %v\n%s", result.Error, result.Output)
	}
	packet := readDiscoveryQueryPacket(t, root, result.Output)
	if packet.ContextIndexID == "" {
		t.Fatalf("expected context index id to remain visible, got %#v", packet)
	}
	if packet.ContextFreshness == nil || !packet.ContextFreshness.FreshForScope || packet.ContextFreshness.Refresh == nil || !packet.ContextFreshness.Refresh.Attempted {
		t.Fatalf("expected discovery_query to refresh stale context before graph backing, got %#v", packet.ContextFreshness)
	}
	if !packetHasLayer(packet, "context_pack") {
		t.Fatalf("refreshed context graph should generate context_pack layer, got layers %#v", packetLayerNames(packet))
	}
	if packetHasLayer(packet, "target_listing") {
		t.Fatalf("refreshed context graph should replace live target listing, got layers %#v", packetLayerNames(packet))
	}
}

func TestDiscoveryQueryAutoRefreshesBoundedStaleContext(t *testing.T) {
	root := seedDiscoveryQueryGoRepo(t)
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
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}
	writeWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service

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
	if result.Error != "" {
		t.Fatalf("discovery_query returned error: %v\n%s", result.Error, result.Output)
	}
	packet := readDiscoveryQueryPacket(t, root, result.Output)
	if !packetHasLayer(packet, "context_pack") {
		t.Fatalf("bounded stale context should attempt context_pack refresh, got layers %#v", packetLayerNames(packet))
	}
	if packetHasLayer(packet, "target_listing") {
		t.Fatalf("bounded stale context should refresh and use context graph orientation, got layers %#v", packetLayerNames(packet))
	}
	if !packetGapsContain(packet, "fresh context graph supplied target orientation") {
		t.Fatalf("expected refreshed context orientation gap, got %#v", packet.Gaps)
	}
}

func TestDiscoveryQuerySecurityReviewRunsSecurityRecipeWithoutExternalTools(t *testing.T) {
	root := seedDiscoveryQueryGoRepo(t)
	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		EnableShell:           true,
		EnableWrites:          true,
	})
	t.Setenv("PATH", "")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:     "discovery_query",
		Purpose:  "security_review",
		Path:     ".",
		Language: "go",
	})
	if result.Error != "" {
		t.Fatalf("discovery_query returned error: %v\n%s", result.Error, result.Output)
	}
	packet := readDiscoveryQueryPacket(t, root, result.Output)
	for _, want := range []string{"target_listing", "dependency_graph", "security_text_sweep", "semgrep_scan", "joern_scan"} {
		if !packetHasLayer(packet, want) {
			t.Fatalf("expected security discovery query layer %q, got layers %#v", want, packetLayerNames(packet))
		}
	}
	if !packetLayerHasError(packet, "semgrep_scan") || !packetLayerHasError(packet, "joern_scan") {
		t.Fatalf("expected unavailable external security scans to be recorded as layer errors, got %#v", packet.Layers)
	}
}

func TestDiscoveryQueryRejectsUnknownPurpose(t *testing.T) {
	mgr := NewManager(testWorkspaceConfig())
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "discovery_query", Purpose: "wander", Path: "."})
	if result.Error == "" || !strings.Contains(result.Error, "purpose must be") {
		t.Fatalf("expected unknown purpose rejection, got %#v", result)
	}
}

func TestDiscoveryQueryNormalizesArchitectureUsefulnessAliases(t *testing.T) {
	for _, purpose := range []string{"tool_usefulness", "discovery_tools", "usefulness"} {
		if got := normalizeDiscoveryQueryPurpose(purpose); got != "architecture_boundary" {
			t.Fatalf("normalizeDiscoveryQueryPurpose(%q) = %q, want architecture_boundary", purpose, got)
		}
	}
}

func TestDiscoveryQueryLanguageCoverageFollowsRegistry(t *testing.T) {
	for _, lang := range LanguageRegistry() {
		glob := globForDiscoveryLanguage(lang.Name)
		if glob == "" {
			t.Fatalf("%s should have a discovery glob", lang.Name)
		}
		for _, ext := range lang.Extensions {
			suffix := strings.TrimPrefix(ext, ".")
			if !strings.Contains(glob, suffix) {
				t.Fatalf("%s discovery glob %q missing extension %q", lang.Name, glob, ext)
			}
		}
	}

	for _, alias := range []string{"tsx", ".mts", "d.cts", "jsx", "mjs", "javascript"} {
		if got := normalizeDiscoveryQueryLanguage(alias); got != "typescript" {
			t.Fatalf("normalizeDiscoveryQueryLanguage(%q) = %q, want typescript", alias, got)
		}
	}
}

func seedDiscoveryQueryGoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/discoveryquery\n\ngo 1.22\n")
	writeWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service

func Target(v string) string {
	return helper(v)
}

func helper(v string) string {
	return v
}
`)
	writeWorkspaceFile(t, filepath.Join(root, "service", "service_test.go"), `package service

import "testing"

func TestTarget(t *testing.T) {
	if Target("x") != "x" {
		t.Fatal("bad target")
	}
}
`)
	if _, err := exec.LookPath("git"); err == nil {
		runTestCommand(t, root, "git", "init")
		runTestCommand(t, root, "git", "config", "user.email", "test@example.com")
		runTestCommand(t, root, "git", "config", "user.name", "Test User")
		runTestCommand(t, root, "git", "add", ".")
		runTestCommand(t, root, "git", "commit", "-m", "initial")
	}
	return root
}

func readDiscoveryQueryPacket(t *testing.T, root string, summaryText string) discoveryQueryPacket {
	t.Helper()
	var summary discoveryQuerySummary
	if err := json.Unmarshal([]byte(summaryText), &summary); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, summaryText)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(summary.ArtifactPath)))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var packet discoveryQueryPacket
	if err := json.Unmarshal(data, &packet); err != nil {
		t.Fatalf("decode artifact: %v\n%s", err, string(data))
	}
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
