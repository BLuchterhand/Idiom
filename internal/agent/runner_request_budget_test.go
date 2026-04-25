package agent

import (
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestRunnerRequestBudgetFingerprintDecisionWarnsThenBlocks(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	req := workspace.ToolRequest{Tool: "read_file", Path: "README.md"}
	fingerprint := "read_file|README.md"

	state.noteFingerprint(fingerprint)
	state.noteFingerprint(fingerprint)
	decision := state.fingerprintDecision(RunPolicy{FingerprintNudgeAt: 2}, req, fingerprint)
	if !decision.Handled || decision.Nudge == "" || decision.EventRole != "fingerprint_nudge" {
		t.Fatalf("expected fingerprint nudge decision, got %#v", decision)
	}

	state.noteFingerprint(fingerprint)
	decision = state.fingerprintDecision(RunPolicy{FingerprintNudgeAt: 2}, req, fingerprint)
	if !decision.Handled || decision.ForceAnswerReason != "fingerprint_hard_block_exhausted" {
		t.Fatalf("expected hard block after repeated warned fingerprint, got %#v", decision)
	}
}

func TestRunnerRequestBudgetExplorationDecisionNudgesWithoutForcingDelegation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			AllowedTools: []string{"task", "read_file", "search_files", "search_content"},
			Contract:     "grounded_explanation",
		},
	}
	state := newRunnerRequestBudgetState(2)
	state.readCallsSinceWrite = 4
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md", Output: "one"},
		{Tool: "search_files", Summary: "Matched 1 file", Output: "README.md"},
	}

	decision := state.explorationDecision(runner, workspace.ToolRequest{Tool: "search_content", Pattern: "route"}, results)
	if !decision.Handled || decision.Nudge == "" || decision.EventRole != "exploration_task_delegation_nudge" {
		t.Fatalf("expected exploration delegation nudge, got %#v", decision)
	}
	if !state.broadEvidenceDelegationRequired {
		t.Fatal("expected delegation requirement to latch after nudge")
	}

	decision = state.explorationDecision(runner, workspace.ToolRequest{Tool: "read_file", Path: "README.md"}, results)
	if decision.Handled {
		t.Fatalf("expected targeted follow-up read to remain model-directed after nudge, got %#v", decision)
	}

	decision = state.explorationDecision(runner, workspace.ToolRequest{Tool: "list_files", Path: "."}, results)
	if !decision.Handled || decision.RejectSummary != "Broad exploration after delegation nudge blocked" {
		t.Fatalf("expected only broad exploration to be blocked after nudge, got %#v", decision)
	}
}

func TestRunnerRequestBudgetReadBudgetBlocksAfterPostChildBudget(t *testing.T) {
	state := newRunnerRequestBudgetState(1)
	state.broadEvidenceDelegationRequired = true
	state.broadEvidenceDelegationSatisfied = true
	state.noteReadClassRequest()
	state.noteReadClassRequest()

	decision := state.readBudgetDecision(RunPolicy{}, workspace.ToolRequest{Tool: "read_file", Path: "README.md"})
	if !decision.Handled || decision.RejectSummary != "Post-child read budget exhausted" {
		t.Fatalf("expected post-child read budget exhaustion, got %#v", decision)
	}
	if !strings.Contains(decision.ForceAnswerPrompt, "child packet") {
		t.Fatalf("expected forced-answer guidance to mention child packet, got %#v", decision)
	}
}
