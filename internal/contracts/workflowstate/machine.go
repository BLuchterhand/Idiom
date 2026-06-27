package workflowstate

import "fmt"

type StateID string
type TransitionEvent string

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

const (
	TransitionEventGatesPass             TransitionEvent = "gates-pass"
	TransitionEventDispatch              TransitionEvent = "dispatch"
	TransitionEventEditsApplied          TransitionEvent = "edits-applied"
	TransitionEventDecompose             TransitionEvent = "decompose"
	TransitionEventPromote               TransitionEvent = "promote"
	TransitionEventDispatchComplete      TransitionEvent = "dispatch-complete"
	TransitionEventAuthorized            TransitionEvent = "authorized"
	TransitionEventDispatched            TransitionEvent = "dispatched"
	TransitionEventScopeAvailable        TransitionEvent = "scope-available"
	TransitionEventStructuredPacket      TransitionEvent = "structured-packet"
	TransitionEventLegacyHeadingFallback TransitionEvent = "legacy-heading-fallback"
)

type MachineDefinition struct {
	WorkflowID     WorkflowID        `json:"workflow_id"`
	InitialState   StateID           `json:"initial_state"`
	States         []StateID         `json:"states"`
	Transitions    []StateTransition `json:"transitions"`
	TerminalStates []TerminalState   `json:"terminal_states"`
}

type StateTransition struct {
	From StateID         `json:"from"`
	To   StateID         `json:"to"`
	On   TransitionEvent `json:"on"`
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
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
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
			{From: StateIntake, To: StateDispatchable, On: TransitionEventGatesPass},
			{From: StateDispatchable, To: StatePlanning, On: TransitionEventDispatch},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateSourceSatisfied)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateInvalidScope)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateMissingScope)},
			{From: StatePlanning, To: StateTerminal, On: terminalTransitionEvent(TerminalStateSourceSatisfied)},
			{From: StatePlanning, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
			{From: StatePlanning, To: StateTerminal, On: terminalTransitionEvent(TerminalStateVerificationFailed)},
			{From: StatePlanning, To: StateTerminal, On: terminalTransitionEvent(TerminalStateStaleNeedsRebase)},
			{From: StatePlanning, To: StateTerminal, On: terminalTransitionEvent(TerminalStateRecoveryOpen)},
		},
		TerminalStates: []TerminalState{
			TerminalStateSourceSatisfied,
			TerminalStateBlocked,
			TerminalStateVerificationFailed,
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
			{From: StateIntake, To: StateExecuting, On: TransitionEventDispatch},
			{From: StateExecuting, To: StateReviewing, On: TransitionEventEditsApplied},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateVerificationFailed)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
			{From: StateReviewing, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateReviewing, To: StateTerminal, On: terminalTransitionEvent(TerminalStateRecoveryOpen)},
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
			{From: StateIntake, To: StateExecuting, On: TransitionEventDispatch},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateDirtyScope)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateInvalidScope)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateVerificationFailed)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
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
			{From: StateInspecting, To: StateDecomposing, On: TransitionEventDecompose},
			{From: StateDecomposing, To: StatePromoting, On: TransitionEventPromote},
			{From: StatePromoting, To: StateReconciling, On: TransitionEventDispatchComplete},
			{From: StateReconciling, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateInspecting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateDirtyScope)},
			{From: StateInspecting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateInvalidScope)},
			{From: StateInspecting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
			{From: StateDecomposing, To: StateTerminal, On: terminalTransitionEvent(TerminalStateRecoveryOpen)},
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
			{From: StateIntake, To: StateCallingTool, On: TransitionEventAuthorized},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateInvalidScope)},
			{From: StateCallingTool, To: StateAwaitingTool, On: TransitionEventDispatched},
			{From: StateAwaitingTool, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateAwaitingTool, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
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
			{From: StateIntake, To: StateDispatchable, On: TransitionEventGatesPass},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateMissingScope)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateDirtyScope)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateInvalidScope)},
			{From: StateDispatchable, To: StateExecuting, On: TransitionEventDispatch},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateStaleNeedsRebase)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateInvalidScope)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateRecoveryOpen)},
			{From: StateExecuting, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
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
			{From: StateIntake, To: StateDispatchable, On: TransitionEventScopeAvailable},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateMissingScope)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateSourceSatisfied)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateStaleNeedsRebase)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateInvalidScope)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlockedDirtyFiles)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateFailedRefineDraft)},
			{From: StateDispatchable, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateDispatchable, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{
			TerminalStateComplete,
			TerminalStateBlocked,
			TerminalStateMissingScope,
			TerminalStateSourceSatisfied,
			TerminalStateStaleNeedsRebase,
			TerminalStateInvalidScope,
			TerminalStateBlockedDirtyFiles,
			TerminalStateFailedRefineDraft,
		},
	}
}

func PromptContextPacketDefinition() MachineDefinition {
	return MachineDefinition{
		WorkflowID:   WorkflowPromptContextPacket,
		InitialState: StateIntake,
		States:       []StateID{StateIntake, StateDispatchable, StateTerminal},
		Transitions: []StateTransition{
			{From: StateIntake, To: StateDispatchable, On: TransitionEventStructuredPacket},
			{From: StateIntake, To: StateDispatchable, On: TransitionEventLegacyHeadingFallback},
			{From: StateDispatchable, To: StateTerminal, On: terminalTransitionEvent(TerminalStateComplete)},
			{From: StateIntake, To: StateTerminal, On: terminalTransitionEvent(TerminalStateBlocked)},
		},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked},
	}
}

func terminalTransitionEvent(state TerminalState) TransitionEvent {
	return TransitionEvent(state)
}

func ValidateTransition(workflowID WorkflowID, from StateID, event TransitionEvent) (StateID, error) {
	definition, ok := definitionByWorkflowID(workflowID)
	if !ok {
		return "", fmt.Errorf("unknown workflow id %q", workflowID)
	}
	for _, transition := range definition.Transitions {
		if transition.From == from && transition.On == event {
			return transition.To, nil
		}
	}
	return "", fmt.Errorf("transition %q from state %q is not valid for workflow %q", event, from, workflowID)
}

func ValidateTerminalProjection(workflowID WorkflowID, terminalState TerminalState) error {
	definition, ok := definitionByWorkflowID(workflowID)
	if !ok {
		return fmt.Errorf("unknown workflow id %q", workflowID)
	}
	for _, candidate := range definition.TerminalStates {
		if candidate == terminalState {
			return nil
		}
	}
	return fmt.Errorf("terminal state %q is not valid for workflow %q", terminalState, workflowID)
}

func ValidateStateProjection(workflowID WorkflowID, state StateID, terminalState TerminalState, dispatchable bool) error {
	definition, ok := definitionByWorkflowID(workflowID)
	if !ok {
		return fmt.Errorf("unknown workflow id %q", workflowID)
	}
	if !stateInDefinition(definition, state) {
		return fmt.Errorf("state %q is not valid for workflow %q", state, workflowID)
	}
	switch {
	case state == StateTerminal:
		if terminalState == "" {
			return fmt.Errorf("terminal workflow %q missing terminal state", workflowID)
		}
		if dispatchable {
			return fmt.Errorf("terminal workflow %q cannot be dispatchable", workflowID)
		}
		return ValidateTerminalProjection(workflowID, terminalState)
	case terminalState != "":
		return fmt.Errorf("nonterminal workflow %q state %q cannot set terminal state %q", workflowID, state, terminalState)
	case state == StateDispatchable && !dispatchable:
		return fmt.Errorf("workflow %q dispatchable state must be marked dispatchable", workflowID)
	case state != StateDispatchable && dispatchable:
		return fmt.Errorf("workflow %q state %q cannot be marked dispatchable", workflowID, state)
	default:
		return nil
	}
}

func MustValidateTerminalProjection(workflowID WorkflowID, terminalState TerminalState) {
	if err := ValidateTerminalProjection(workflowID, terminalState); err != nil {
		panic(err)
	}
}

func MustValidateStateProjection(workflowID WorkflowID, state StateID, terminalState TerminalState, dispatchable bool) {
	if err := ValidateStateProjection(workflowID, state, terminalState, dispatchable); err != nil {
		panic(err)
	}
}

func definitionByWorkflowID(workflowID WorkflowID) (MachineDefinition, bool) {
	for _, definition := range Definitions() {
		if definition.WorkflowID == workflowID {
			return definition, true
		}
	}
	return MachineDefinition{}, false
}

func stateInDefinition(definition MachineDefinition, state StateID) bool {
	for _, candidate := range definition.States {
		if candidate == state {
			return true
		}
	}
	return false
}
