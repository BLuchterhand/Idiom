package agent

import (
	"slices"

)

// runnerTurnState holds the per-turn phase tracking, recovery state,
// tool-hiding maps, and allowance counters that were previously 30+
// local variables inside Runner.Run(). Extracting them into a named
// type makes the state machine explicit and shrinkable.
type runnerTurnState struct {
	// Read-only delegated phase
	delegatedReadOnlyAnswerReady        bool
	delegatedReadOnlyAnswerReadyNudged  bool
	delegatedReadOnlyPostReadyFollowups int
	delegatedReadOnlyDiminishingNudgeStage int
	postChildPacketActive               bool
	postChildFollowupReads              int
	postChildDiminishingNudgeStage      int
	postReadinessProbeBudget            int

	// Plan-then-execute phase
	planThenExecuteReadinessNudged               bool
	planThenExecuteCorrectionAfterRefreshNudged  bool
	planThenExecuteUnreadSourceNudgedFor         string
	planThenExecuteReadySourceRefreshes          int
	planThenExecuteNativeSubmitPlanNoCallFallbacks int
	planThenExecuteNativeSubmitPlanDisabled      bool

	// Write / recovery state
	implementationWriteOnlyRecoveryActive bool
	requiredPathCompletionRecoveryActive  bool
	writeDescriptionRepairActive          bool
	writeDescriptionDetourRejections      int
	writeDescriptionRepairPath            string
	scopedHiddenNonWriteRecoveryUsed      bool
	scratchpadCloseoutNonWriteRejections  int

	// Exact-anchor write state
	exactAnchorWriteOnlyActive bool
	exactAnchorWriteFailures   int

	// Post-completion verification counter — counts non-write tool
	// requests after all required edit paths are written. Once it
	// crosses the cutoff the runner force-answers.
	postCompletionVerificationCalls int

	// Hidden-tool maps — tools gated out for the remainder of the turn.
	implementationWriteOnlyHiddenTools map[string]bool
	turnHiddenTools                    map[string]bool
	exactAnchorHiddenTools             map[string]bool
	implementationScopedHiddenTools    map[string]bool

	// Recovery read allowances — one bounded read per required edit
	// path during write-only recovery.
	recoveryReadAllowanceUsed map[string]bool
}

func newRunnerTurnState(r *Runner) *runnerTurnState {
	ts := &runnerTurnState{
		delegatedReadOnlyAnswerReady: slices.Contains(r.RuntimeBrief.Constraints, "answer_readiness_reached"),
		postReadinessProbeBudget:     postReadinessProbeBudgetForBrief(r.RuntimeBrief),

		implementationWriteOnlyHiddenTools: make(map[string]bool),
		turnHiddenTools:                    make(map[string]bool),
		exactAnchorHiddenTools:             make(map[string]bool),
		implementationScopedHiddenTools:    make(map[string]bool),
		recoveryReadAllowanceUsed:          make(map[string]bool),
	}
	ts.exactAnchorWriteOnlyActive = exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, nil)
	return ts
}

