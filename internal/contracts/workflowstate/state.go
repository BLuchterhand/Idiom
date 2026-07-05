package workflowstate

type WorkflowID string

const (
	WorkflowDirectAnswer        WorkflowID = "direct_answer"
	WorkflowPlanExecutePlanner  WorkflowID = "plan_execute_planner"
	WorkflowPlanExecuteExecutor WorkflowID = "plan_execute_executor"
	WorkflowImplement           WorkflowID = "implement"
	WorkflowRecovery            WorkflowID = "recovery"
	WorkflowSidecarToolCall     WorkflowID = "sidecar_tool_call"
	WorkflowProductDispatch     WorkflowID = "product_workflow_dispatch"
	WorkflowAtomIntake          WorkflowID = "atom_intake"
	WorkflowPromptContextPacket WorkflowID = "prompt_context_packet"
)

type TerminalState string

const (
	TerminalStateComplete           TerminalState = "complete"
	TerminalStateSourceSatisfied    TerminalState = "source-satisfied"
	TerminalStateBlocked            TerminalState = "blocked"
	TerminalStateStaleNeedsRebase   TerminalState = "stale-needs-rebase"
	TerminalStateInvalidScope       TerminalState = "invalid-scope"
	TerminalStateMissingScope       TerminalState = "missing-scope"
	TerminalStateBlockedDirtyFiles  TerminalState = "blocked-dirty-files"
	TerminalStateFailedRefineDraft  TerminalState = "failed-refine-draft"
	TerminalStateDirtyScope         TerminalState = "dirty-scope"
	TerminalStateVerificationFailed TerminalState = "verification-failed"
	TerminalStateRecoveryOpen       TerminalState = "recovery-open"
)

type Classification struct {
	WorkflowID    WorkflowID    `json:"workflow_id"`
	TerminalState TerminalState `json:"terminal_state"`
	Dispatchable  bool          `json:"dispatchable"`
	Terminal      bool          `json:"terminal"`
	Reason        string        `json:"reason,omitempty"`
	Blockers      []Blocker     `json:"blockers,omitempty"`
	Transitions   []Transition  `json:"transitions,omitempty"`
}

type Blocker struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type Transition struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
}
