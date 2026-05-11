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

func TestRunnerRequestBudgetAllowsOneCorrectedRetryAfterMissingNamedGap(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	req := workspace.ToolRequest{
		Tool:     "read_file",
		Path:     "internal/agent/runner.go",
		NamedGap: "inspect the exact branch that delegates the child task",
	}
	fingerprint := "read_file|internal/agent/runner.go"
	state.rejectedFingerprints[fingerprint] = "Post-readiness follow-up missing named gap"

	decision := state.fingerprintDecision(RunPolicy{}, req, fingerprint)
	if decision.Handled {
		t.Fatalf("expected corrected retry to be allowed, got %#v", decision)
	}
	if _, ok := state.rejectedFingerprints[fingerprint]; ok {
		t.Fatalf("expected recoverable rejection fingerprint to be cleared after corrected retry")
	}
}

func TestRunnerRequestBudgetStillBlocksRejectedRetryWithoutNamedGap(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	req := workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/agent/runner.go",
	}
	fingerprint := "read_file|internal/agent/runner.go"
	state.rejectedFingerprints[fingerprint] = "Post-readiness follow-up missing named gap"

	decision := state.fingerprintDecision(RunPolicy{}, req, fingerprint)
	if !decision.Handled || decision.ForceAnswerReason != "rejection_retry_blocked" {
		t.Fatalf("expected unchanged retry to stay blocked, got %#v", decision)
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

// TestRunnerRequestBudgetReadBudgetIsNoLongerEnforced validates the
// no-bandaid memory: readBudgetDecision used to force-finish lanes
// after a fixed post-child read count. That cap fired prematurely
// on mutation parents that legitimately needed several anchor-
// verification reads after a child:explorer packet (see #101). The
// structural enforcers — MaxToolSteps, doom_loop_guard, completion
// gate — are the real closure contract; the per-call budget cap was
// the bandaid the project memory said to remove. This test locks in
// the removal so a future re-introduction is caught.
func TestRunnerRequestBudgetReadBudgetIsNoLongerEnforced(t *testing.T) {
	state := newRunnerRequestBudgetState(1)
	state.readOnlyChildTaskSatisfied = true
	state.noteReadClassRequest(workspace.ToolRequest{Tool: "read_file", Path: "README.md"})
	state.noteReadClassRequest(workspace.ToolRequest{Tool: "read_file", Path: "README.md"})

	decision := state.readBudgetDecision(RunPolicy{}, workspace.ToolRequest{Tool: "read_file", Path: "README.md"})
	if decision.Handled {
		t.Fatalf("readBudgetDecision should no longer force-finish on post-child reads; got %#v", decision)
	}
	if decision.RejectStopReason != "" {
		t.Fatalf("expected empty stop reason, got %q", decision.RejectStopReason)
	}
}

func TestRunnerRequestBudgetBlocksUnrelatedPostChildDiscovery(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	state.readOnlyChildTaskSatisfied = true
	state.noteReadOnlyChildPacket([]string{"internal/agent/discovery_redirect.go"})

	decision := state.explorationDecision(
		&Runner{},
		workspace.ToolRequest{Tool: "search_files", Pattern: "*harness*", Path: "."},
		nil,
	)
	if !decision.Handled || decision.RejectSummary != "Post-child unrelated discovery blocked" {
		t.Fatalf("expected unrelated post-child search to be blocked, got %#v", decision)
	}
	if !strings.Contains(decision.RejectError, "discovery_redirect.go") {
		t.Fatalf("expected rejection to mention packet evidence paths, got %#v", decision)
	}
}

func TestRunnerRequestBudgetAllowsPacketAnchoredPostChildDiscovery(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	state.readOnlyChildTaskSatisfied = true
	state.noteReadOnlyChildPacket([]string{"internal/workspace/discovery_query.go"})

	decision := state.explorationDecision(
		&Runner{},
		workspace.ToolRequest{Tool: "search_files", Pattern: "*discovery*", Path: "."},
		nil,
	)
	if decision.Handled {
		t.Fatalf("expected packet-anchored post-child discovery to remain allowed, got %#v", decision)
	}
}

// TestRunnerRequestBudgetNudgesPostChildRereadsButDoesNotBlock locks
// in the no-bandaid memory: post-child reread used to nudge on the
// first revisit and HARD-BLOCK on the second. The block fired
// prematurely on mutation parents that legitimately re-read the
// same file at different ranges to verify multiple anchors (see
// #101's wedge chain). doom_loop_guard catches exact-duplicate
// reads; MaxToolSteps caps total budget; completion_gate enforces
// mutation closure. The nudge stays as a soft signal but the hard
// block was removed.
func TestRunnerRequestBudgetNudgesPostChildRereadsButDoesNotBlock(t *testing.T) {
	state := newRunnerRequestBudgetState(4)
	state.readOnlyChildTaskSatisfied = true
	state.noteReadOnlyChildPacket([]string{"internal/agent/discovery_redirect.go"})
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/discovery_redirect.go"}

	state.noteReadClassRequest(req)

	decision := state.explorationDecision(&Runner{}, req, nil)
	if !decision.Handled || decision.Nudge == "" || decision.EventRole != "post_child_reread_nudge" {
		t.Fatalf("expected first post-child reread to nudge, got %#v", decision)
	}

	state.noteReadClassRequest(req)

	decision = state.explorationDecision(&Runner{}, req, nil)
	if decision.Handled {
		t.Fatalf("expected repeated post-child reread to be allowed (no hard block); got %#v", decision)
	}
	if decision.RejectStopReason != "" {
		t.Fatalf("expected no stop reason on repeated post-child reread, got %q", decision.RejectStopReason)
	}
}
