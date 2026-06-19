package workflowstate

type StateID string

const (
	StateIntake       StateID = "intake"
	StateDispatchable StateID = "dispatchable"
	StatePlanning     StateID = "planning"
	StateExecuting    StateID = "executing"
	StateReviewing    StateID = "reviewing"
	StateInspecting   StateID = "inspecting"
	StateDecomposing  StateID = "decomposing"
	StatePromoting    StateID = "promoting"
	StateReconciling  StateID = "reconciling"
	StateCallingTool  StateID = "calling-tool"
	StateAwaitingTool StateID = "awaiting-tool"
	StateTerminal     StateID = "terminal"
)

type MachineDefinition struct {
	WorkflowID     WorkflowID        `json:"workflow_id"`
	InitialState   StateID           `json:"initial_state"`
	States         []StateID         `json:"states"`
	Transitions    []StateTransition `json:"transitions"`
	TerminalStates []TerminalState   `json:"terminal_states"`
}

type StateTransition struct {
	From StateID `json:"from"`
	To   StateID `json:"to"`
	On   string  `json:"on"`
}

func Definitions() []MachineDefinition {
	return []MachineDefinition{
		DirectAnswerDefinition(),
		PlanExecutePlannerDefinition(),
		PlanExecuteExecutorDefinition(),
		ImplementDefinition(),
		RecoveryDefinition(),
		SidecarToolCallDefinition(),
		ProductDispatchDefinition(),
		AtomIntakeDefinition(),
		PromptContextPacketDefinition(),
	}
}

func DirectAnswerDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowDirectAnswer,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked},
	}
}

func PlanExecutePlannerDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowPlanExecutePlanner,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateDispatchable, StatePlanning, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateDispatchable, On: "gates-pass"},
			{From: StateDispatchable, To: StatePlanning, On: "dispatch"},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateSourceSatisfied)},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateInvalidScope)},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateMissingScope)},
			{From: StatePlanning, To: StateTerminal, On: string(TerminalStateSourceSatisfied)},
			{From: StatePlanning, To: StateTerminal, On: string(TerminalStateBlocked)},
			{From: StatePlanning, To: StateTerminal, On: string(TerminalStateStaleNeedsRebase)},
			{From: StatePlanning, To: StateTerminal, On: string(TerminalStateRecoveryOpen)},
		},
		TerminalStates: []TerminalState{
			TerminalStateSourceSatisfied,
			TerminalStateBlocked,
			TerminalStateStaleNeedsRebase,
			TerminalStateInvalidScope,
			TerminalStateMissingScope,
			TerminalStateRecoveryOpen,
		},
	}
}

func PlanExecuteExecutorDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowPlanExecuteExecutor,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateExecuting, StateReviewing, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateExecuting, On: "dispatch"},
			{From: StateExecuting, To: StateReviewing, On: "edits-applied"},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateVerificationFailed)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateBlocked)},
			{From: StateReviewing, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateReviewing, To: StateTerminal, On: string(TerminalStateRecoveryOpen)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateVerificationFailed, TerminalStateRecoveryOpen},
	}
}

func ImplementDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowImplement,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateExecuting, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateExecuting, On: "dispatch"},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateDirtyScope)},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateInvalidScope)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateVerificationFailed)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateDirtyScope, TerminalStateInvalidScope, TerminalStateVerificationFailed},
	}
}

func RecoveryDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowRecovery,
		InitialState: StateInspecting,
		States:       []StateID{StateInspecting, StateDecomposing, StatePromoting, StateReconciling, StateTerminal},
		Transitions: []StateTransition{
			{From: StateInspecting, To: StateDecomposing, On: "decompose"},
			{From: StateDecomposing, To: StatePromoting, On: "promote"},
			{From: StatePromoting, To: StateReconciling, On: "dispatch-complete"},
			{From: StateReconciling, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateInspecting, To: StateTerminal, On: string(TerminalStateDirtyScope)},
			{From: StateInspecting, To: StateTerminal, On: string(TerminalStateInvalidScope)},
			{From: StateInspecting, To: StateTerminal, On: string(TerminalStateBlocked)},
			{From: StateDecomposing, To: StateTerminal, On: string(TerminalStateRecoveryOpen)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateDirtyScope, TerminalStateInvalidScope, TerminalStateRecoveryOpen},
	}
}

func SidecarToolCallDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowSidecarToolCall,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateCallingTool, StateAwaitingTool, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateCallingTool, On: "authorized"},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateInvalidScope)},
			{From: StateCallingTool, To: StateAwaitingTool, On: "dispatched"},
			{From: StateAwaitingTool, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateAwaitingTool, To: StateTerminal, On: string(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateInvalidScope},
	}
}

func ProductDispatchDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowProductDispatch,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateDispatchable, StateExecuting, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateDispatchable, On: "gates-pass"},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateMissingScope)},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateDirtyScope)},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateInvalidScope)},
			{From: StateDispatchable, To: StateExecuting, On: "dispatch"},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateStaleNeedsRebase)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateInvalidScope)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateRecoveryOpen)},
			{From: StateExecuting, To: StateTerminal, On: string(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateMissingScope, TerminalStateDirtyScope, TerminalStateInvalidScope, TerminalStateStaleNeedsRebase, TerminalStateRecoveryOpen},
	}
}

func AtomIntakeDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowAtomIntake,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateDispatchable, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateDispatchable, On: "scope-available"},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateMissingScope)},
			{From: StateDispatchable, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateDispatchable, To: StateTerminal, On: string(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateMissingScope},
	}
}

func PromptContextPacketDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowPromptContextPacket,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateDispatchable, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateDispatchable, On: "structured-packet"},
			{From: StateIntake, To: StateDispatchable, On: "legacy-heading-fallback"},
			{From: StateDispatchable, To: StateTerminal, On: string(TerminalStateComplete)},
			{From: StateIntake, To: StateTerminal, On: string(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked},
	}
}
