package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
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

func TestRunnerRequestBudgetSamePathFixationNudgesOnVariedRanges(t *testing.T) {
	state := newRunnerRequestBudgetState(2)

	for i := 0; i < samePathFixationNudgeAt-1; i++ {
		req := workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/app.go", StartLine: i*50 + 1, EndLine: (i + 1) * 50}
		if decision := state.samePathFixationDecision(req, false); decision.Handled {
			t.Fatalf("same-path nudge fired too early at request %d: %#v", i+1, decision)
		}
	}

	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/app.go", StartLine: 201, EndLine: 250}
	decision := state.samePathFixationDecision(req, true)

	if !decision.Handled || decision.Nudge == "" || decision.EventRole != "same_path_fixation_nudge" {
		t.Fatalf("expected same-path fixation nudge, got %#v", decision)
	}

	if !strings.Contains(decision.Nudge, "internal/surfaces/tui/app/app.go") ||
		!strings.Contains(decision.Nudge, "exact missing claim") ||
		!strings.Contains(decision.Nudge, "Do not request another adjacent range") {

		t.Fatalf("same-path nudge should name the path and bounded next action, got %q", decision.Nudge)
	}

	req = workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/app.go", StartLine: 251, EndLine: 300}
	decision = state.samePathFixationDecision(req, true)

	if decision.Handled || decision.HideTool != "" {
		t.Fatalf("ignored same-path nudge must not hide read tools, got %#v", decision)
	}
}

func TestRunnerRequestBudgetSamePathFixationSkipsDiagnosticLogs(t *testing.T) {
	state := newRunnerRequestBudgetState(2)

	for i := 0; i < samePathFixationNudgeAt+2; i++ {
		req := workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/20260604-192533.289.jsonl", StartLine: i*80 + 1, EndLine: (i + 1) * 80}
		if decision := state.samePathFixationDecision(req, true); decision.Handled {
			t.Fatalf("diagnostic log window %d should not trigger source same-path nudge: %#v", i+1, decision)
		}
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

func TestRunnerRequestBudgetSubmitCorrectionRetryDoesNotAdvertiseTerminalExceptions(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	req := workspace.ToolRequest{Tool: "read_file", Path: "README.md"}
	fingerprint := "read_file|README.md"

	state.fingerprint.rejected[fingerprint] = "Corrected submit_plan required"
	state.noteFingerprint(fingerprint)
	state.noteFingerprint(fingerprint)
	state.noteFingerprint(fingerprint)

	decision := state.fingerprintDecision(RunPolicy{FingerprintNudgeAt: 2}, req, fingerprint)
	if !decision.Handled {
		t.Fatal("expected submit-correction retry to be handled")
	}

	if !strings.Contains(decision.Nudge, "submit-correction mode") || !strings.Contains(decision.Nudge, "submit_plan") {
		t.Fatalf("expected submit-correction nudge, got %#v", decision)
	}

	for _, forbidden := range []string{"true terminal exception", "or use `Decision: source-satisfied` / `<<QUESTION>>:`"} {
		if strings.Contains(decision.Nudge, forbidden) {
			t.Fatalf("submit-correction retry nudge advertised stale exception %q: %s", forbidden, decision.Nudge)
		}
	}
}

func TestTerminalArtifactRejectedRetryNudgeAllowsExactBlockerWhenSubmitCorrectionExhausted(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-2 (total lines: 2)", Output: "1:before\n"},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "invalid edit anchor(s): `a.txt: old_text not found in current source`."},
		{Tool: "read_file", Summary: "Read f2 a.txt lines 1-2 (total lines: 2)", Output: "1:before\n"},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "invalid edit anchor(s): `a.txt: old_text not found in current source`."},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "invalid edit anchor(s): `a.txt: old_text not found in current source`."},
	}

	nudge := terminalArtifactRejectedRetryNudge("read_file", "Corrected submit_plan required", brief, results)
	if !strings.Contains(nudge, "<<QUESTION>>") || !strings.Contains(nudge, "missing body/anchor blocker") {
		t.Fatalf("exhausted submit-correction nudge should allow exact blocker:\n%s", nudge)
	}
	if strings.Contains(nudge, "Do not answer `<<QUESTION>>") {
		t.Fatalf("exhausted submit-correction nudge still forbids question:\n%s", nudge)
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
		Path:     "internal/runtime/agent/runner.go",
		NamedGap: "inspect the exact branch that delegates the child task",
	}
	fingerprint := "read_file|internal/runtime/agent/runner.go"

	state.fingerprint.rejected[fingerprint] = "Post-readiness follow-up missing named gap"

	decision := state.fingerprintDecision(RunPolicy{}, req, fingerprint)
	if decision.Handled {
		t.Fatalf("expected corrected retry to be allowed, got %#v", decision)
	}

	if _, ok := state.fingerprint.rejected[fingerprint]; ok {
		t.Fatalf("expected recoverable rejection fingerprint to be cleared after corrected retry")
	}
}

func TestRecoverablePostReadinessRejectionSummaryUsesContractNamespace(t *testing.T) {
	for _, summary := range []string{
		"Post-readiness named_gap restates the original question",
		"Post-readiness search is broader than the current evidence requires",
		"Post-readiness exact probe is not grounded in current evidence (replayed same blocker)",
		"Post-child follow-up gap is not packet-anchored",
	} {
		if !isRecoverablePostReadinessGapRejection(summary) {
			t.Fatalf("expected %q to be recoverable post-readiness rejection", summary)
		}
	}

	for _, summary := range []string{
		"Repeated dead-end action rejected",
		"Post-readiness named-gap contract is exhausted. Tools are now disabled.",
	} {
		if isRecoverablePostReadinessGapRejection(summary) {
			t.Fatalf("expected %q not to be recoverable post-readiness rejection", summary)
		}
	}
}

func TestRunnerRequestBudgetClearsLegacyMissingNamedGapRetryWithoutNamedGap(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	req := workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/runtime/agent/runner.go",
	}
	fingerprint := "read_file|internal/runtime/agent/runner.go"

	state.fingerprint.rejected[fingerprint] = "Post-readiness follow-up missing named gap"

	// Read-only post-readiness follow-ups are advisory now. The old
	// named_gap rejection summary must not enter the retry budget.
	decision := state.fingerprintDecision(RunPolicy{}, req, fingerprint)
	if decision.Handled {
		t.Fatalf("advisory post-readiness retry should not be budget-blocked, got %#v", decision)
	}

	if _, ok := state.fingerprint.rejected[fingerprint]; ok {
		t.Fatal("expected legacy missing named-gap rejection fingerprint to be cleared")
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

func TestRunnerRequestBudgetNudgesAndBlocksZeroHitSearchFamily(t *testing.T) {
	state := newRunnerRequestBudgetState(2)
	first := workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "pkey.KeyArrowUp"}
	state.noteZeroHitSearch(first, "search_content|.|pkey.KeyArrowUp", workspace.ToolResult{
		Tool:    "search_content",
		Summary: `No content matches for "pkey.KeyArrowUp"`,
	})

	second := workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "pkey.KeyArrowDown"}
	decision := state.fingerprintDecision(RunPolicy{}, second, "search_content|.|pkey.KeyArrowDown")

	if !decision.Handled || decision.EventRole != "zero_hit_family_nudge" || decision.Nudge == "" {
		t.Fatalf("expected same-family zero-hit nudge, got %#v", decision)
	}

	state.noteZeroHitSearch(second, "search_content|.|pkey.KeyArrowDown", workspace.ToolResult{
		Tool:    "search_content",
		Summary: `No content matches for "pkey.KeyArrowDown"`,
	})

	third := workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "key arrow"}

	decision = state.fingerprintDecision(RunPolicy{}, third, "search_content|.|key arrow")

	if !decision.Handled || decision.RejectSummary != "Zero-hit search family blocked" || decision.RejectStopReason != "zero_hit_family_budget_exhausted" {
		t.Fatalf("expected same-family zero-hit block, got %#v", decision)
	}
}

func TestRunnerRequestBudgetAllowsUnrelatedPostChildDiscovery(t *testing.T) {
	state := newRunnerRequestBudgetState(2)

	state.readOnlyChildTaskSatisfied = true
	state.noteReadOnlyChildPacket([]string{"internal/runtime/agent/discovery_redirect.go"})

	decision := state.explorationDecision(
		&Runner{},
		workspace.ToolRequest{Tool: "search_files", Pattern: "*harness*", Path: "."},
		nil,
	)
	if decision.Handled {
		t.Fatalf("post-child child packet must not hard-wall unrelated read-only search, got %#v", decision)
	}
}

// TestRunnerRequestBudgetPostChildPacketDoesNotHardWallParentDiscovery
// locks the north-star rule for post-child read/search: a child evidence
// packet is a hint, not a hard contract. The parent may broaden or search
// outside the packet's paths when the model decides more evidence is needed.
// The completion gate owns closure; doom_loop_guard + MaxToolSteps own the
// runaway-exploration ceiling.
func TestRunnerRequestBudgetPostChildPacketDoesNotHardWallParentDiscovery(t *testing.T) {
	state := newRunnerRequestBudgetState(2)

	state.readOnlyChildTaskSatisfied = true
	state.noteReadOnlyChildPacket([]string{"internal/runtime/agent/discovery_redirect.go"})

	for _, tc := range []struct {
		name   string
		runner *Runner
		req    workspace.ToolRequest
	}{
		{name: "read-only broad inspection", runner: &Runner{}, req: workspace.ToolRequest{Tool: "list_files", Path: "."}},
		{name: "read-only unrelated search", runner: &Runner{}, req: workspace.ToolRequest{Tool: "search_files", Pattern: "*harness*", Path: "."}},
		{name: "mutation broad inspection", runner: mutationRunner(), req: workspace.ToolRequest{Tool: "list_files", Path: "."}},
		{name: "mutation unrelated search", runner: mutationRunner(), req: workspace.ToolRequest{Tool: "search_files", Pattern: "*harness*", Path: "."}},
	} {
		decision := state.explorationDecision(tc.runner, tc.req, nil)
		if decision.Handled {
			t.Fatalf("%s should not be blocked by post-child packet, got %#v", tc.name, decision)
		}
	}
}

func TestRunnerRequestBudgetAllowsPacketAnchoredPostChildDiscovery(t *testing.T) {
	state := newRunnerRequestBudgetState(2)

	state.readOnlyChildTaskSatisfied = true
	state.noteReadOnlyChildPacket([]string{"internal/capabilities/workspace/discovery_query.go"})

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
	state.noteReadOnlyChildPacket([]string{"internal/runtime/agent/discovery_redirect.go"})

	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/discovery_redirect.go"}

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

	req := workspace.ToolRequest{Tool: "search_content", Path: "internal/surfaces/tui/app"}
	fingerprint := "search_content|internal/surfaces/tui/app"

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
