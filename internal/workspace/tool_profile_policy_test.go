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

func TestToolAllowedForDiscoveryProfileDeniesContextToolsThatWriteArtifacts(t *testing.T) {
	for _, tool := range []string{"context_pack", "context_impact", "discovery_query"} {
		decision := ToolAllowedForProfile(tool, ToolProfileDiscovery)
		if decision.Allowed {
			t.Fatalf("%s should be denied for discovery profile because it writes local artifacts", tool)
		}
		if decision.Code != "profile_denied" || !strings.Contains(decision.Reason, "context artifacts") {
			t.Fatalf("%s denial should name local artifact writes, got %#v", tool, decision)
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

func TestNormalizeToolProfileRejectsUnknownProfile(t *testing.T) {
	if _, err := NormalizeToolProfile("verify"); err == nil {
		t.Fatal("expected unknown profile to be rejected")
	}
}
