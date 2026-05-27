package agent

import (
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func mutationRunner() *Runner {
	return &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			RequiredEditPaths: []string{"some/output.md"},
		},
	}
}

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

func TestRunnerRequestBudgetDoesNotForceNoToolsForTerminalArtifactRetry(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	req := workspace.ToolRequest{Tool: "read_file", Path: "README.md"}
	fingerprint := "read_file|README.md"
	state.fingerprint.rejected[fingerprint] = "Plan-execute submit_plan required"
	state.noteFingerprint(fingerprint)
	state.noteFingerprint(fingerprint)
	state.noteFingerprint(fingerprint)

	decision := state.fingerprintDecision(RunPolicy{FingerprintNudgeAt: 2}, req, fingerprint)
	if !decision.Handled {
		t.Fatal("expected terminal artifact retry to be handled")
	}
	if decision.ForceAnswerReason != "" || decision.ForceAnswerPrompt != "" {
		t.Fatalf("terminal artifact retry must preserve submit_plan surface, got %#v", decision)
	}
	if decision.Nudge == "" || decision.EventRole != "terminal_artifact_retry_recovery" {
		t.Fatalf("expected terminal artifact repair nudge, got %#v", decision)
	}
	if !strings.Contains(decision.Nudge, "submit_plan") || strings.Contains(decision.Nudge, "Tools are now disabled") {
		t.Fatalf("nudge should require submit_plan without no-tools closeout: %q", decision.Nudge)
	}

	early := state.earlyRejectedFingerprintDecision(req, fingerprint)
	if !early.Handled || early.ForceAnswerReason != "" || !strings.Contains(early.Nudge, "terminal plan artifact") {
		t.Fatalf("early repeated rejection should also preserve terminal artifact recovery, got %#v", early)
	}
}

func TestRunnerRequestBudgetEarlyBlocksRepeatedRangeLessReadRetry(t *testing.T) {
	for _, summary := range []string{
		"Repeated range-less read_file redirected to bounded source request",
		"Repeated range-less read_file rejected",
	} {
		t.Run(summary, func(t *testing.T) {
			state := newRunnerRequestBudgetState(2)
			req := workspace.ToolRequest{Tool: "read_file", Path: "README.md"}
			fingerprint := "read_file|README.md"
			state.fingerprint.rejected[fingerprint] = summary

			// First retry: nudge (nudge-then-block pattern)
			decision := state.earlyRejectedFingerprintDecision(req, fingerprint)
			if !decision.Handled || decision.Nudge == "" {
				t.Fatalf("expected nudge on first retry, got %#v", decision)
			}

			// Second retry: blocked
			decision = state.earlyRejectedFingerprintDecision(req, fingerprint)
			if !decision.Handled || decision.ForceAnswerReason != "rejection_retry_blocked" {
				t.Fatalf("expected block on second retry, got %#v", decision)
			}
		})
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
	state.fingerprint.rejected[fingerprint] = "Post-readiness follow-up missing named gap"

	decision := state.fingerprintDecision(RunPolicy{}, req, fingerprint)
	if decision.Handled {
		t.Fatalf("expected corrected retry to be allowed, got %#v", decision)
	}
	if _, ok := state.fingerprint.rejected[fingerprint]; ok {
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
	state.fingerprint.rejected[fingerprint] = "Post-readiness follow-up missing named gap"

	// First retry without named gap: nudge (nudge-then-block pattern)
	decision := state.fingerprintDecision(RunPolicy{}, req, fingerprint)
	if !decision.Handled || decision.Nudge == "" {
		t.Fatalf("expected nudge on first retry without named gap, got %#v", decision)
	}

	// Second retry: blocked
	decision = state.fingerprintDecision(RunPolicy{}, req, fingerprint)
	if !decision.Handled || decision.ForceAnswerReason != "rejection_retry_blocked" {
		t.Fatalf("expected block on second retry without named gap, got %#v", decision)
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

// TestRunnerRequestBudgetMutationParentSuppressesPostChildBlocks
// locks in the same #101 mutation-parent rule for the post-child
// broad-inspection and unrelated-discovery rejections: when the
// parent's deliverable is a WRITE (not an answer), the child's
// evidence packet is a hint, not a hard wall. The parent may need
// to broaden or search outside the packet's evidence paths to find
// callsites or related symbols for its RequiredEditPaths. The
// completion gate owns mutation closure; doom_loop_guard +
// MaxToolSteps own the runaway-exploration ceiling.
func TestRunnerRequestBudgetMutationParentSuppressesPostChildBlocks(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	state.readOnlyChildTaskSatisfied = true
	state.noteReadOnlyChildPacket([]string{"internal/agent/discovery_redirect.go"})

	// Mutation parent doing a broad inspection after child packet — allowed.
	decision := state.explorationDecision(
		mutationRunner(),
		workspace.ToolRequest{Tool: "list_files", Path: "."},
		nil,
	)
	if decision.Handled {
		t.Fatalf("mutation parent post-child broad inspection should be allowed, got %#v", decision)
	}

	// Mutation parent doing an unrelated discovery search after child packet — allowed.
	decision = state.explorationDecision(
		mutationRunner(),
		workspace.ToolRequest{Tool: "search_files", Pattern: "*harness*", Path: "."},
		nil,
	)
	if decision.Handled {
		t.Fatalf("mutation parent post-child unrelated discovery should be allowed, got %#v", decision)
	}

	// Read-only parent — both blocks still fire (regression guard).
	decision = state.explorationDecision(
		&Runner{},
		workspace.ToolRequest{Tool: "search_files", Pattern: "*harness*", Path: "."},
		nil,
	)
	if !decision.Handled || decision.RejectSummary != "Post-child unrelated discovery blocked" {
		t.Fatalf("read-only parent should still block unrelated post-child search, got %#v", decision)
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

// TestWriteReadyNudgeActiveDefersRejectionRetry verifies that when
// writeReadyNudgeActive is true, a rejection retry returns a nudge
// instead of force-answering, deferring to the write-ready escalation.
func TestWriteReadyNudgeActiveDefersRejectionRetry(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	state.writeReadyNudgeActive = true
	req := workspace.ToolRequest{Tool: "search_content", Path: "internal/app"}
	fingerprint := "search_content|internal/app"
	state.fingerprint.rejected[fingerprint] = "Chained search rejected"
	state.fingerprint.rejectedRetries = make(map[string]int)

	decision := state.rejectedFingerprintDecision(req, fingerprint)
	if !decision.Handled {
		t.Fatal("expected rejection retry to be handled")
	}
	if decision.ForceAnswerReason != "" {
		t.Fatalf("expected nudge (not force-answer) when writeReadyNudgeActive, got ForceAnswerReason=%q", decision.ForceAnswerReason)
	}
	if decision.EventRole != "rejection_retry_deferred_to_write_ready" {
		t.Fatalf("expected rejection_retry_deferred_to_write_ready event, got %q", decision.EventRole)
	}
	if decision.Nudge == "" {
		t.Fatal("expected a nudge message when writeReadyNudgeActive is true")
	}
	t.Logf("nudge: %s", decision.Nudge)
}

// TestRejectionRetryNudgeThenBlock verifies the nudge-then-block pattern:
// first retry of a rejected fingerprint gets a nudge, second gets force-answered.
func TestRejectionRetryNudgeThenBlock(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	req := workspace.ToolRequest{Tool: "list_files", Path: "."}
	fingerprint := "list_files|."
	state.fingerprint.rejected[fingerprint] = "Broad inspection blocked"
	state.fingerprint.rejectedRetries = make(map[string]int)

	// First retry: nudge
	d1 := state.rejectedFingerprintDecision(req, fingerprint)
	if !d1.Handled || d1.Nudge == "" {
		t.Fatalf("expected nudge on first retry, got %#v", d1)
	}
	if d1.ForceAnswerReason != "" {
		t.Fatalf("expected nudge (not force-answer) on first retry, got ForceAnswerReason=%q", d1.ForceAnswerReason)
	}
	t.Log("first retry: nudge")

	// Second retry: blocked
	d2 := state.rejectedFingerprintDecision(req, fingerprint)
	if !d2.Handled || d2.ForceAnswerReason != "rejection_retry_blocked" {
		t.Fatalf("expected force-answer on second retry, got %#v", d2)
	}
	t.Log("second retry: blocked")
}

// TestNoteWriteProgressClearsWriteReadyNudgeAndRejectionState verifies
// that noteWriteProgress clears writeReadyNudgeActive and both
// rejected/rejectedRetries maps.
func TestNoteWriteProgressClearsWriteReadyNudgeAndRejectionState(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	state.writeReadyNudgeActive = true
	state.fingerprint.rejected["fp"] = "test rejection"
	state.fingerprint.rejectedRetries = map[string]int{"fp": 3}

	state.noteWriteProgress()

	if state.writeReadyNudgeActive {
		t.Fatal("writeReadyNudgeActive should be false after noteWriteProgress")
	}
	if _, ok := state.fingerprint.rejected["fp"]; ok {
		t.Fatal("rejected fingerprints should be cleared after noteWriteProgress")
	}
	if _, ok := state.fingerprint.rejectedRetries["fp"]; ok {
		t.Fatal("rejectedRetries should be cleared after noteWriteProgress")
	}
}
