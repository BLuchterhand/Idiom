package workspace

import (
	"strings"
	"testing"
)

func TestToolAllowedForDiscoveryProfileAllowsReadOnlyDiscoveryTool(t *testing.T) {
	decision := ToolAllowedForProfile("context_status", ToolProfileDiscovery)
	if !decision.Allowed {
		t.Fatalf("context_status should be allowed for discovery profile: %#v", decision)
	}
}

func TestToolRequestAllowedForDiscoveryProfileDeniesRefreshCapableContextStatus(t *testing.T) {
	decision := ToolRequestAllowedForProfile(ToolRequest{
		Tool:    "context_status",
		Refresh: true,
	}, ToolProfileDiscovery)
	if decision.Allowed {
		t.Fatal("context_status refresh request should be denied for discovery profile")
	}
	if decision.Code != "profile_denied" || !strings.Contains(decision.Reason, "refresh") {
		t.Fatalf("unexpected refresh denial: %#v", decision)
	}
}

func TestToolRequestAllowedForDiscoveryProfileAllowsContextStatusReportOnly(t *testing.T) {
	for _, req := range []ToolRequest{
		{Tool: "context_status"},
		{Tool: "context_status", FreshnessPolicy: "report_only"},
		{Tool: "context_status", FreshnessPolicy: "allow_stale"},
	} {
		decision := ToolRequestAllowedForProfile(req, ToolProfileDiscovery)
		if !decision.Allowed {
			t.Fatalf("context_status report-only request should be allowed: req=%#v decision=%#v", req, decision)
		}
	}
}

func TestToolRequestAllowedForDiscoveryProfileRequiresReportOnlyGraphReads(t *testing.T) {
	denied := ToolRequestAllowedForProfile(ToolRequest{Tool: "context_query", Query: "callers of Run"}, ToolProfileDiscovery)
	if denied.Allowed {
		t.Fatal("context_query default read-through request should be denied for discovery profile")
	}
	if denied.Code != "profile_denied" || !strings.Contains(denied.Reason, "freshness_policy") {
		t.Fatalf("unexpected context_query denial: %#v", denied)
	}

	allowed := ToolRequestAllowedForProfile(ToolRequest{Tool: "context_query", Query: "callers of Run", FreshnessPolicy: "report_only"}, ToolProfileDiscovery)
	if !allowed.Allowed {
		t.Fatalf("context_query report-only request should be allowed: %#v", allowed)
	}
}

func TestToolAllowedForDiscoveryProfileDeniesDescriptorLocalArtifactTools(t *testing.T) {
	for _, tool := range []string{"context_pack", "context_impact", "discovery_query"} {
		if !discoveryProfileToolWritesLocalContextArtifacts(tool) {
			t.Fatalf("%s must remain descriptor-declared as a local artifact writer", tool)
		}
	}
	localArtifactTools := builtInToolSurfaceDiscoveryLocalArtifactTools()
	if len(localArtifactTools) == 0 {
		t.Fatal("test setup expected at least one descriptor-declared local artifact tool")
	}
	for _, tool := range localArtifactTools {
		decision := ToolAllowedForProfile(tool, ToolProfileDiscovery)
		if decision.Allowed {
			t.Fatalf("%s should be denied for discovery profile because descriptor metadata says it writes local artifacts", tool)
		}
		if decision.Code != "profile_denied" || !strings.Contains(decision.Reason, "context artifacts") {
			t.Fatalf("%s denial should name local artifact writes, got %#v", tool, decision)
		}
	}
}

func TestDiscoveryProfileLocalArtifactQueryProjectsToolSurfaceMetadata(t *testing.T) {
	for _, spec := range builtInToolSurfaceSpecs {
		if got := discoveryProfileToolWritesLocalContextArtifacts(spec.Tool); got != spec.DiscoveryLocalArtifacts {
			t.Fatalf("%s local artifact query = %v, want descriptor metadata %v", spec.Tool, got, spec.DiscoveryLocalArtifacts)
		}
	}
}

func TestToolAllowedForMCPDiscoveryProfileAllowsDescriptorLocalArtifactTools(t *testing.T) {
	localArtifactTools := builtInToolSurfaceDiscoveryLocalArtifactTools()
	if len(localArtifactTools) == 0 {
		t.Fatal("test setup expected at least one descriptor-declared local artifact tool")
	}
	for _, tool := range localArtifactTools {
		decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery)
		if !decision.Allowed {
			t.Fatalf("%s should be allowed for mcp_discovery profile, got %#v", tool, decision)
		}
	}
}

func TestToolAllowedForMCPDiscoveryProfileDeniesMutationShellAndUnknownEffects(t *testing.T) {
	for _, tool := range []string{"write_file", "apply_patch", "shell", "context_trace_pack", "project_external"} {
		decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery)
		if decision.Allowed {
			t.Fatalf("%s should be denied for mcp_discovery profile", tool)
		}
		if decision.Code != "profile_denied" || decision.Reason == "" {
			t.Fatalf("%s denial should be structured, got %#v", tool, decision)
		}
	}
}

func TestToolAllowedForMCPDiscoveryProfileAllowsReadOnlyDiscoveryTools(t *testing.T) {
	for _, tool := range []string{
		"context_status", "context_query", "context_zoom",
		"discovery_query", "structural_search", "dependency_graph",
		"history_range", "history_symbol", "blame_range",
		"ctags_symbols", "code_navigate", "harness_audit",
		"recovery_inspect", "recovery_status",
	} {
		decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery)
		if !decision.Allowed {
			t.Fatalf("%s should be allowed for mcp_discovery profile, got %#v", tool, decision)
		}
	}
}

func TestToolAllowedForDiscoveryProfileDeniesMutationShellAndUnknownEffects(t *testing.T) {
	for _, tool := range []string{"write_file", "apply_patch", "shell", "context_trace_pack", "project_external"} {
		decision := ToolAllowedForProfile(tool, ToolProfileDiscovery)
		if decision.Allowed {
			t.Fatalf("%s should be denied for discovery profile", tool)
		}
		if decision.Code != "profile_denied" || decision.Reason == "" {
			t.Fatalf("%s denial should be structured, got %#v", tool, decision)
		}
	}
}

func TestToolProfileDeniedResultIsStableAndDoesNotEchoRequestValues(t *testing.T) {
	privatePath := "/Users/example/.ssh/id_ed25519"
	secretSentinel := "P75_POLICY_SECRET_SENTINEL_DO_NOT_LEAK"
	decision := ToolRequestAllowedForProfile(ToolRequest{
		Tool:    "write_file",
		Path:    privatePath,
		Content: secretSentinel,
	}, ToolProfileDiscovery)
	if decision.Allowed {
		t.Fatal("write_file should be denied for discovery profile")
	}

	result := ToolProfileDeniedResult("write_file", ToolProfileDiscovery, decision)
	if result.Tool != "write_file" || result.Summary != "Tool request denied by discovery profile" {
		t.Fatalf("unexpected denial result identity: %#v", result)
	}
	for _, want := range []string{"profile_denied", "read_only_profile"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("denial result should contain stable %q marker: %#v", want, result)
		}
	}
	for _, forbidden := range []string{privatePath, secretSentinel} {
		if strings.Contains(result.Summary, forbidden) || strings.Contains(result.Error, forbidden) || strings.Contains(result.Output, forbidden) {
			t.Fatalf("denial result leaked request value %q: %#v", forbidden, result)
		}
	}
}

func TestNormalizeToolProfileRejectsUnknownProfile(t *testing.T) {
	if _, err := NormalizeToolProfile("verify"); err == nil {
		t.Fatal("expected unknown profile to be rejected")
	}
}
