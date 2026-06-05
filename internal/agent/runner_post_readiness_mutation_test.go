package agent

import (
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// TestPostReadinessGapScopeForBriefSeparatesParentContracts covers the core
// discovery invariant: read-only readiness and child packets are advisory
// follow-up guidance, not permission gates. Mutation source-refresh contracts
// are owned by postReadinessToolContractForBrief instead.
func TestPostReadinessToolContractForBriefSeparatesParentContracts(t *testing.T) {
	mutationBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"some/output.md"},
	}
	readOnlyBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "investigation",
		PermissionProfile: "read_only_workspace",
	}

	if contract := postReadinessToolContractForBrief(mutationBrief, nil, true, false); !contract.RequireSourceRefreshContract {
		t.Fatalf("mutation source-refresh contract should be explicit, got %#v", contract)
	}

	if contract := postReadinessToolContractForBrief(readOnlyBrief, nil, true, false); contract.RequireSourceRefreshContract || !contract.ReadinessAdvisory {
		t.Fatalf("read-only parent readiness should be advisory, got %#v", contract)
	}

	if contract := postReadinessToolContractForBrief(mutationBrief, nil, false, true); !contract.RequireSourceRefreshContract {
		t.Fatalf("mutation child-packet source-refresh contract should stay explicit, got %#v", contract)
	}

	if contract := postReadinessToolContractForBrief(readOnlyBrief, nil, false, true); contract.RequireSourceRefreshContract || !contract.ReadinessAdvisory {
		t.Fatalf("read-only child packet should be advisory, got %#v", contract)
	}

	// Write-class requests do not change the selected post-readiness contract.
	writeReq := workspace.ToolRequest{Tool: "write_file", Path: "x.md"}
	if !workspace.IsWorkspaceWriteTool(writeReq.Tool) {
		t.Fatalf("test setup should use a write-class tool")
	}
}

func TestPostReadinessReadRequestWithoutNamedGapRemainsAdvisory(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	}
	req := workspace.ToolRequest{
		Tool:   "code_navigate",
		Path:   "internal/transcript/debuglog.go",
		Symbol: "DebugLogger.sanitizeValue",
	}

	if gap := postReadinessGap(req); gap != "" {
		t.Fatalf("test setup should omit post-readiness gap fields, got %q", gap)
	}
	if contract := postReadinessToolContractForBrief(brief, nil, true, false); contract.RequireSourceRefreshContract || !contract.ReadinessAdvisory {
		t.Fatalf("post-readiness source read should remain advisory, got %#v", contract)
	}
}
