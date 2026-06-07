package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestDiscoveryConsumptionCandidateOnlyContextQueryAllowsFollowup(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_query", Query: "callers of runner discovery state", Resolution: "symbol"},
		workspace.ToolResult{
			Tool:    "context_query",
			Summary: "Found 2 candidate owner(s); source owner not established",
			Output:  `{"source_owner_found":false,"candidates":[{"path":"internal/runtime/agent/runner_execution_state.go"},{"path":"internal/runtime/agent/runner.go"}]}`,
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

func TestDiscoveryConsumptionWeakContextPackAllowsCorrectiveFollowup(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "TUI message history key handling"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/runtime/agent/runner.go"]},"candidate_files":[{"path":"internal/runtime/agent/runner.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	if !state.pendingDiscoveryConsumption || state.pendingDiscoveryStrength != discoveryConsumptionCandidateOnly {
		t.Fatalf("expected weak context_pack to be candidate-only, got pending=%v strength=%v", state.pendingDiscoveryConsumption, state.pendingDiscoveryStrength)
	}
	for _, req := range []workspace.ToolRequest{
		{Tool: "context_query", Query: "what handles TUI key input for conversation messages", Resolution: "symbol", IncludeEvidence: true},
		{Tool: "search_files", Path: ".", Pattern: "update_handlers"},
		{Tool: "context_pack", Task: "TUI message history key handling"},
	} {
		if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, directAnswerReadOnlyBrief()) {
			t.Fatalf("weak context_pack should allow corrective follow-up: %#v", req)
		}
	}
}

func TestReadOnlyWeakDiscoveryNextActionNamesMissingSourceOwner(t *testing.T) {
	brief := directAnswerReadOnlyBrief()
	brief.Objective = "what would it take to enable arrow-key history recall in the TUI chat box?"
	brief.AllowedTools = []string{"context_pack", "context_query", "search_content", "read_file", "code_navigate"}
	req := workspace.ToolRequest{Tool: "context_pack", Task: "implement arrow-key history recall in TUI chat input", Mode: "plan"}
	result := workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote view with candidate file(s)",
		Output:  `{"quality":{"localization_confidence":"low","source_owner_found":false,"reasons":["related_evidence_without_source_owner"]},"candidate_files":[{"path":"internal/runtime/agent/runner.go"},{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
	}

	state := buildRunnerNextActionStateWithAllowedTools("main", 1, "tool_result", req, result, []workspace.ToolResult{result}, false, brief, brief.AllowedTools)
	if len(state.EvidencePaths) != 0 {
		t.Fatalf("candidate-only discovery must not be promoted to source-body evidence paths: %#v", state.EvidencePaths)
	}
	joinedStatus := strings.Join(state.EvidenceStatus, "\n")
	if !strings.Contains(joinedStatus, "candidate-only") || !strings.Contains(joinedStatus, "source owner") {
		t.Fatalf("weak discovery status should name the missing source-owner evidence, got %#v", state.EvidenceStatus)
	}
	joinedNext := strings.Join(state.NextValidActions, "\n")
	for _, want := range []string{
		"source owner was not established by context_pack",
		"materially different source-owner or source-body probe",
		"exact lexical/symbol/source-owner probe",
	} {
		if !strings.Contains(joinedNext, want) {
			t.Fatalf("weak discovery next-action guidance missing %q: %#v", want, state.NextValidActions)
		}
	}
	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(joinedInvalid, "exact replay of the same weak discovery request") {
		t.Fatalf("weak discovery should mark exact replay invalid, got %#v", state.InvalidNextActions)
	}
}

func TestReadOnlySourceOwnerDiscoveryNextActionRequiresSourceBody(t *testing.T) {
	brief := directAnswerReadOnlyBrief()
	brief.Objective = "explain discovery consumption state"
	brief.AllowedTools = []string{"context_pack", "context_query", "search_content", "read_file", "code_navigate"}
	req := workspace.ToolRequest{Tool: "context_pack", Task: "runner discovery consumption"}
	result := workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote view with 1 candidate file(s)",
		Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/runtime/agent/runner_execution_state.go"]},"candidate_files":[{"path":"internal/runtime/agent/runner_execution_state.go"}]}`,
	}

	state := buildRunnerNextActionStateWithAllowedTools("main", 1, "tool_result", req, result, []workspace.ToolResult{result}, false, brief, brief.AllowedTools)
	if len(state.EvidencePaths) != 0 {
		t.Fatalf("source-owner discovery still must not become source-body evidence paths: %#v", state.EvidencePaths)
	}
	joinedNext := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedNext, "inspect one returned path or handle with source-body evidence") {
		t.Fatalf("source-owner discovery should direct source-body consumption, got %#v", state.NextValidActions)
	}
	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(joinedInvalid, "repeated broad or identical rediscovery") {
		t.Fatalf("source-owner discovery should warn against broad rediscovery, got %#v", state.InvalidNextActions)
	}
}

func TestReadOnlyCompletionGateCandidateOnlyRecoveryNamesSourceBody(t *testing.T) {
	brief := directAnswerReadOnlyBrief()
	brief.Objective = "What does ShortLabel return?"
	brief.AllowedTools = []string{"search_content", "read_file", "code_navigate"}
	runner := &Runner{RuntimeBrief: brief}
	result, rejected := runner.readOnlyEvidenceCompletionRejection("ShortLabel returns Save changes.", []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
	}})
	if !rejected {
		t.Fatal("expected candidate-only read-only answer to be rejected")
	}
	for _, want := range []string{
		"Candidate/search/context paths are not source-body evidence",
		"internal/labels/labels.go",
		"Inspect one candidate with source-body evidence",
	} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("candidate-only recovery missing %q:\n%s", want, result.Error)
		}
	}
	if strings.Contains(result.Error, "Use one targeted read/search/discovery tool") {
		t.Fatalf("candidate-only recovery should not fall back to generic discovery advice:\n%s", result.Error)
	}

	if _, rejected := runner.readOnlyEvidenceCompletionRejection("<<QUESTION>>: Cannot answer until source-body evidence is available.", []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
	}}); rejected {
		t.Fatal("candidate-only recovery must not reject an explicit blocker terminal outcome")
	}
}

func TestDiscoveryConsumptionStrongContextPackStillBlocksBroadRediscovery(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "runner discovery state"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/runtime/agent/runner_execution_state.go"]},"candidate_files":[{"path":"internal/runtime/agent/runner_execution_state.go"}]}`,
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
			Output:  `{"source_owner_found":false,"candidates":[{"path":"internal/runtime/agent/runner.go"}]}`,
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
		{Tool: "context_query", Path: ".", Query: "runner discovery consumption", Resolution: "symbol"},
		{Tool: "context_query", Query: "runner discovery consumption", Resolution: "symbol", IncludeEvidence: true},
		{Tool: "context_query", Query: "runner discovery consumption", Resolution: "symbol", Pattern: "runner"},
		{Tool: "context_query", Query: "runner discovery consumption", Resolution: "symbol", Symbol: "runner"},
		{Tool: "search_files", Path: ".", Pattern: "discovery"},
	} {
		if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, directAnswerReadOnlyBrief()) {
			t.Fatalf("strong context_pack should still require source-body consumption before broad rediscovery: %#v", req)
		}
	}

	refinement := workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "what handles TUI key input for conversation messages",
		Resolution:      "symbol",
		IncludeEvidence: true,
	}
	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(refinement, directAnswerReadOnlyBrief()) {
		t.Fatal("unscoped source-owner query prose should still require source-body consumption after strong context_pack")
	}

	rationaleOnlyRefinement := workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "what handles TUI key input for conversation messages",
		Resolution:      "symbol",
		IncludeEvidence: true,
		WhyNotAnswering: "need source owner for TUI key input conversation message handling",
	}
	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(rationaleOnlyRefinement, directAnswerReadOnlyBrief()) {
		t.Fatal("why_not_answering alone should not scope source-owner refinement after strong context_pack")
	}

	namedGapRefinement := workspace.ToolRequest{
		Tool:       "context_query",
		Query:      "TUI key input for conversation messages",
		Resolution: "symbol",
		NamedGap:   "source owner for TUI key input conversation message handling",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(namedGapRefinement, directAnswerReadOnlyBrief()) {
		t.Fatal("named-gap source-owner refinement should remain available after strong context_pack")
	}

	pathScopedRefinement := workspace.ToolRequest{
		Tool:       "context_query",
		Path:       "internal/runtime/agent/runner_execution_state.go",
		Query:      "runner discovery consumption",
		Resolution: "symbol",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(pathScopedRefinement, directAnswerReadOnlyBrief()) {
		t.Fatal("path-scoped graph refinement should remain available after strong context_pack")
	}
}

func TestDiscoveryConsumptionSourceBodyReadClearsPendingDiscovery(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "runner discovery state"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/runtime/agent/runner_execution_state.go"]},"candidate_files":[{"path":"internal/runtime/agent/runner_execution_state.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "context_pack", Task: "runner discovery consumption"}, directAnswerReadOnlyBrief()) {
		t.Fatal("expected strong discovery to block broad rediscovery before source body evidence")
	}

	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner_execution_state.go", StartLine: 1, EndLine: 40},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_execution_state.go lines 1-40",
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

func TestDiscoveryConsumptionFailedGuessedReadDoesNotReopenBroadRediscovery(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "runner discovery state"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/runtime/agent/runner_execution_state.go"]},"candidate_files":[{"path":"internal/runtime/agent/runner_execution_state.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/not_real.go", StartLine: 1, EndLine: 40},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "File not found",
			Error:   "file not found: internal/runtime/agent/not_real.go",
		},
		"",
		nil,
		nil,
	)
	if !state.pendingDiscoveryFailedAttempt {
		t.Fatal("failed guessed read should mark the pending discovery attempt as failed")
	}
	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "context_pack", Task: "runner discovery consumption"}, directAnswerReadOnlyBrief()) {
		t.Fatal("failed guessed read after strong packet must not reopen broad rediscovery")
	}
}

func directAnswerReadOnlyBrief() workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
}
