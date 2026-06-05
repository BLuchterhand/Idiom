package agent

import (
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// TestPostReadinessGapScopeForBriefSeparatesParentContracts covers the core
// discovery invariant: mutation readiness keeps its explicit source-refresh
// surface, while answerable read-only evidence contracts to the final-answer
// surface instead of reopening discovery.
func TestPostReadinessToolContractForBriefSeparatesParentContracts(t *testing.T) {
	mutationBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"some/output.md"},
	}
	readOnlyBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "investigation",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	}
	readOnlyResults := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/transcript/debuglog.go lines 1-80 (total lines: 120)",
			Output:  "1:package transcript\n2:type DebugLogger struct { mode DebugLogMode }\n3:func (l *DebugLogger) sanitizeText(text string) string { if l.mode == DebugLogModeFull { return text }; return redactedBlob(text) }\n",
		},
	}

	if contract := postReadinessToolContractForBrief(mutationBrief, nil, true, false); !contract.RequireSourceRefreshContract {
		t.Fatalf("mutation source-refresh contract should be explicit, got %#v", contract)
	}

	if contract := postReadinessToolContractForBrief(readOnlyBrief, readOnlyResults, true, false); contract.RequireSourceRefreshContract || !contract.AnswerOnly {
		t.Fatalf("answerable read-only parent readiness should select final-answer surface, got %#v", contract)
	}

	if contract := postReadinessToolContractForBrief(mutationBrief, nil, false, true); !contract.RequireSourceRefreshContract {
		t.Fatalf("mutation child-packet source-refresh contract should stay explicit, got %#v", contract)
	}

	childPacket := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       readOnlyBrief.Objective,
		Question:        readOnlyBrief.Objective,
		SourceBodyPaths: []string{"internal/transcript/debuglog.go"},
		InspectedPaths:  []string{"internal/transcript/debuglog.go"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/transcript/debuglog.go", Text: "`DebugLogger.sanitizeText` returns text unchanged in full mode and redacts through `redactedBlob` otherwise."},
		},
	})
	if contract := postReadinessToolContractForBrief(readOnlyBrief, []workspace.ToolResult{childPacket}, false, true); contract.RequireSourceRefreshContract || !contract.AnswerOnly {
		t.Fatalf("answerable read-only child packet should select final-answer surface, got %#v", contract)
	}

	// Write-class requests do not change the selected post-readiness contract.
	writeReq := workspace.ToolRequest{Tool: "write_file", Path: "x.md"}
	if !workspace.IsWorkspaceWriteTool(writeReq.Tool) {
		t.Fatalf("test setup should use a write-class tool")
	}
}

func TestPostReadinessReadinessWithoutAnswerableEvidenceRemainsAdvisory(t *testing.T) {
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
		t.Fatalf("readiness without answerable evidence should remain advisory, got %#v", contract)
	}
}
