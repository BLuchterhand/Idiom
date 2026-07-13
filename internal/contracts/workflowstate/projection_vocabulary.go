package workflowstate

import "fmt"

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

type projectionVocabulary struct {
	WorkflowID     WorkflowID      `json:"workflow_id"`
	States         []StateID       `json:"states"`
	TerminalStates []TerminalState `json:"terminal_states"`
}

func projectionVocabularies() []projectionVocabulary {
	return []projectionVocabulary{
		directAnswerProjectionVocabulary(),
		planExecutePlannerProjectionVocabulary(),
		planExecuteExecutorProjectionVocabulary(),
		implementProjectionVocabulary(),
		recoveryProjectionVocabulary(),
		sidecarToolCallProjectionVocabulary(),
		productDispatchProjectionVocabulary(),
		atomIntakeProjectionVocabulary(),
		promptContextPacketProjectionVocabulary(),
	}
}

func directAnswerProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID:     WorkflowDirectAnswer,
		States:         []StateID{StateIntake, StateTerminal},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked},
	}
}

func planExecutePlannerProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID: WorkflowPlanExecutePlanner,
		States:     []StateID{StateIntake, StateDispatchable, StatePlanning, StateTerminal},
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

func planExecuteExecutorProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID:     WorkflowPlanExecuteExecutor,
		States:         []StateID{StateIntake, StateExecuting, StateReviewing, StateTerminal},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateVerificationFailed, TerminalStateRecoveryOpen},
	}
}

func implementProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID:     WorkflowImplement,
		States:         []StateID{StateIntake, StateExecuting, StateTerminal},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateDirtyScope, TerminalStateInvalidScope, TerminalStateVerificationFailed},
	}
}

func recoveryProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID:     WorkflowRecovery,
		States:         []StateID{StateInspecting, StateDecomposing, StatePromoting, StateReconciling, StateTerminal},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateDirtyScope, TerminalStateInvalidScope, TerminalStateRecoveryOpen},
	}
}

func sidecarToolCallProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID:     WorkflowSidecarToolCall,
		States:         []StateID{StateIntake, StateCallingTool, StateAwaitingTool, StateTerminal},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateInvalidScope},
	}
}

func productDispatchProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID:     WorkflowProductDispatch,
		States:         []StateID{StateIntake, StateDispatchable, StateExecuting, StateTerminal},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked, TerminalStateMissingScope, TerminalStateDirtyScope, TerminalStateInvalidScope, TerminalStateStaleNeedsRebase, TerminalStateRecoveryOpen},
	}
}

func atomIntakeProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID: WorkflowAtomIntake,
		States:     []StateID{StateIntake, StateDispatchable, StateTerminal},
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

func promptContextPacketProjectionVocabulary() projectionVocabulary {
	return projectionVocabulary{
		WorkflowID:     WorkflowPromptContextPacket,
		States:         []StateID{StateIntake, StateDispatchable, StateTerminal},
		TerminalStates: []TerminalState{TerminalStateComplete, TerminalStateBlocked},
	}
}

func ValidateTerminalProjection(workflowID WorkflowID, terminalState TerminalState) error {
	vocabulary, ok := projectionVocabularyByWorkflowID(workflowID)
	if !ok {
		return fmt.Errorf("unknown workflow id %q", workflowID)
	}
	for _, candidate := range vocabulary.TerminalStates {
		if candidate == terminalState {
			return nil
		}
	}
	return fmt.Errorf("terminal state %q is not valid for workflow %q", terminalState, workflowID)
}

func ValidateStateProjection(workflowID WorkflowID, state StateID, terminalState TerminalState, dispatchable bool) error {
	vocabulary, ok := projectionVocabularyByWorkflowID(workflowID)
	if !ok {
		return fmt.Errorf("unknown workflow id %q", workflowID)
	}
	if !stateInProjectionVocabulary(vocabulary, state) {
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

func MustValidateStateProjection(workflowID WorkflowID, state StateID, terminalState TerminalState, dispatchable bool) {
	if err := ValidateStateProjection(workflowID, state, terminalState, dispatchable); err != nil {
		panic(err)
	}
}

func projectionVocabularyByWorkflowID(workflowID WorkflowID) (projectionVocabulary, bool) {
	for _, vocabulary := range projectionVocabularies() {
		if vocabulary.WorkflowID == workflowID {
			return vocabulary, true
		}
	}
	return projectionVocabulary{}, false
}

func stateInProjectionVocabulary(vocabulary projectionVocabulary, state StateID) bool {
	for _, candidate := range vocabulary.States {
		if candidate == state {
			return true
		}
	}
	return false
}
