package workflowstate

import "testing"

func TestDefinitionsCoverPhaseSixWorkflows(t *testing.T) {
	definitions := Definitions()
	want := []WorkflowID{
		WorkflowDirectAnswer,
		WorkflowPlanExecutePlanner,
		WorkflowPlanExecuteExecutor,
		WorkflowImplement,
		WorkflowRecovery,
		WorkflowSidecarToolCall,
		WorkflowProductDispatch,
		WorkflowAtomIntake,
		WorkflowPromptContextPacket,
	}

	byID := map[WorkflowID]MachineDefinition{}
	for _, definition := range definitions {
		if definition.WorkflowID == "" {
			t.Fatalf("definition missing workflow id: %#v", definition)
		}
		if _, exists := byID[definition.WorkflowID]; exists {
			t.Fatalf("duplicate workflow definition for %q", definition.WorkflowID)
		}
		byID[definition.WorkflowID] = definition
	}

	for _, id := range want {
		definition, ok := byID[id]
		if !ok {
			t.Fatalf("missing workflow definition %q", id)
		}
		assertMachineDefinitionWellFormed(t, definition)
	}
}

func TestDefinitionsDeclareEmittedPhaseSixProjectionStates(t *testing.T) {
	assertWorkflowDeclaresState(t, WorkflowPlanExecutePlanner, StateDispatchable)
	assertWorkflowDeclaresState(t, WorkflowAtomIntake, StateDispatchable)
	assertWorkflowDeclaresState(t, WorkflowPromptContextPacket, StateDispatchable)
	assertWorkflowDeclaresState(t, WorkflowRecovery, StateInspecting)
}

func TestDefinitionsDeclarePhaseSixTerminalStates(t *testing.T) {
	required := []TerminalState{
		TerminalStateComplete,
		TerminalStateSourceSatisfied,
		TerminalStateBlocked,
		TerminalStateStaleNeedsRebase,
		TerminalStateInvalidScope,
		TerminalStateMissingScope,
		TerminalStateDirtyScope,
		TerminalStateVerificationFailed,
		TerminalStateRecoveryOpen,
	}
	seen := map[TerminalState]bool{}
	for _, definition := range Definitions() {
		for _, terminal := range definition.TerminalStates {
			seen[terminal] = true
		}
	}

	for _, terminal := range required {
		if !seen[terminal] {
			t.Fatalf("terminal state %q is not declared by any workflow definition", terminal)
		}
	}
}

func assertWorkflowDeclaresState(t *testing.T, workflow WorkflowID, state StateID) {
	t.Helper()

	for _, definition := range Definitions() {
		if definition.WorkflowID != workflow {
			continue
		}
		for _, candidate := range definition.States {
			if candidate == state {
				return
			}
		}
		t.Fatalf("%s does not declare state %s; states=%#v", workflow, state, definition.States)
	}

	t.Fatalf("missing workflow definition %s", workflow)
}

func assertMachineDefinitionWellFormed(t *testing.T, definition MachineDefinition) {
	t.Helper()

	if definition.InitialState == "" {
		t.Fatalf("%s missing initial state", definition.WorkflowID)
	}
	if len(definition.States) == 0 {
		t.Fatalf("%s missing states", definition.WorkflowID)
	}
	if len(definition.TerminalStates) == 0 {
		t.Fatalf("%s missing terminal states", definition.WorkflowID)
	}

	states := map[StateID]bool{}
	for _, state := range definition.States {
		states[state] = true
	}
	if !states[definition.InitialState] {
		t.Fatalf("%s initial state %q is not declared in states %#v", definition.WorkflowID, definition.InitialState, definition.States)
	}
	if !states[StateTerminal] {
		t.Fatalf("%s must declare terminal state node", definition.WorkflowID)
	}

	for _, transition := range definition.Transitions {
		if !states[transition.From] || !states[transition.To] || transition.On == "" {
			t.Fatalf("%s has invalid transition %#v with states %#v", definition.WorkflowID, transition, definition.States)
		}
	}
}
