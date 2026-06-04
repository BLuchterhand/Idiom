package agent

import (
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestDiscoveryConsumptionCandidateOnlyContextQueryAllowsFollowup(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_query", Query: "callers of runner discovery state", Resolution: "symbol"},
		workspace.ToolResult{
			Tool:    "context_query",
			Summary: "Found 2 candidate owner(s); source owner not established",
			Output:  `{"source_owner_found":false,"candidates":[{"path":"internal/agent/runner_execution_state.go"},{"path":"internal/agent/runner.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	if !state.pendingDiscoveryConsumption || state.pendingDiscoveryStrength != discoveryConsumptionCandidateOnly {
		t.Fatalf("expected candidate-only pending discovery, got pending=%v strength=%v", state.pendingDiscoveryConsumption, state.pendingDiscoveryStrength)
	}

	for _, req := range []workspace.ToolRequest{
		{Tool: "context_query", Query: "which state field owns discovery consumption", Resolution: "symbol"},
		{Tool: "search_content", Path: ".", Pattern: "pendingDiscoveryConsumption"},
		{Tool: "context_pack", Task: "runner discovery consumption state"},
	} {
		if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, directAnswerReadOnlyBrief()) {
			t.Fatalf("candidate-only context_query should preserve corrective follow-up: %#v", req)
		}
	}
}

func TestDiscoveryConsumptionStrongContextPackStillBlocksBroadRediscovery(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "runner discovery state"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/agent/runner_execution_state.go"]},"candidate_files":[{"path":"internal/agent/runner_execution_state.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	if state.pendingDiscoveryStrength != discoveryConsumptionSourceOwner {
		t.Fatalf("expected source-owner pending discovery after strong context_pack, got %v", state.pendingDiscoveryStrength)
	}
	state.applyResult(
		workspace.ToolRequest{Tool: "context_query", Query: "related runner discovery candidates", Resolution: "symbol"},
		workspace.ToolResult{
			Tool:    "context_query",
			Summary: "Found candidate-only related paths",
			Output:  `{"source_owner_found":false,"candidates":[{"path":"internal/agent/runner.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	if state.pendingDiscoveryStrength != discoveryConsumptionSourceOwner {
		t.Fatalf("candidate-only context_query downgraded strong pending discovery to %v", state.pendingDiscoveryStrength)
	}

	for _, req := range []workspace.ToolRequest{
		{Tool: "context_pack", Task: "runner discovery consumption"},
		{Tool: "context_query", Query: "runner discovery consumption", Resolution: "symbol"},
		{Tool: "search_files", Path: ".", Pattern: "discovery"},
	} {
		if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, directAnswerReadOnlyBrief()) {
			t.Fatalf("strong context_pack should still require source-body consumption before broad rediscovery: %#v", req)
		}
	}
}

func TestDiscoveryConsumptionSourceBodyReadClearsPendingDiscovery(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "runner discovery state"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/agent/runner_execution_state.go"]},"candidate_files":[{"path":"internal/agent/runner_execution_state.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "context_pack", Task: "runner discovery consumption"}, directAnswerReadOnlyBrief()) {
		t.Fatal("expected strong discovery to block broad rediscovery before source body evidence")
	}

	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner_execution_state.go", StartLine: 1, EndLine: 40},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner_execution_state.go lines 1-40",
			Output:  "package agent\n\ntype runnerExecutionState struct {}\n",
		},
		"",
		nil,
		nil,
	)

	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "context_pack", Task: "runner discovery consumption"}, directAnswerReadOnlyBrief()) {
		t.Fatal("source-body read should clear pending discovery consumption")
	}
	if state.pendingDiscoveryConsumption {
		t.Fatal("source-body read should clear pending discovery state")
	}
}

func directAnswerReadOnlyBrief() workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
}
