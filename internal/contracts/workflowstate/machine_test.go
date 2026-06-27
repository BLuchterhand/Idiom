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
		TerminalStateBlockedDirtyFiles,
		TerminalStateFailedRefineDraft,
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

func TestValidateStateProjectionRejectsIncompleteTerminalProjection(t *testing.T) {
	if err := ValidateStateProjection(WorkflowPlanExecutePlanner, StateTerminal, "", false); err == nil {
		t.Fatalf("terminal projection without terminal state should fail")
	}
}

func TestValidateStateProjectionRejectsDispatchableTerminalProjection(t *testing.T) {
	if err := ValidateStateProjection(WorkflowPlanExecutePlanner, StateTerminal, TerminalStateBlocked, true); err == nil {
		t.Fatalf("terminal projection marked dispatchable should fail")
	}
}

func TestValidateStateProjectionRejectsTerminalStateOnNonterminalProjection(t *testing.T) {
	if err := ValidateStateProjection(WorkflowRecovery, StateInspecting, TerminalStateRecoveryOpen, false); err == nil {
		t.Fatalf("nonterminal projection with terminal state should fail")
	}
}

func TestValidateStateProjectionCoversDispatchableInvariants(t *testing.T) {
	tests := []struct {
		name          string
		workflowID    WorkflowID
		state         StateID
		terminalState TerminalState
		dispatchable  bool
		wantErr       bool
	}{
		{
			name:         "valid dispatchable state",
			workflowID:   WorkflowPlanExecutePlanner,
			state:        StateDispatchable,
			dispatchable: true,
		},
		{
			name:         "dispatchable state missing flag",
			workflowID:   WorkflowPlanExecutePlanner,
			state:        StateDispatchable,
			dispatchable: false,
			wantErr:      true,
		},
		{
			name:         "non dispatchable state with flag",
			workflowID:   WorkflowRecovery,
			state:        StateInspecting,
			dispatchable: true,
			wantErr:      true,
		},
		{
			name:         "unknown workflow",
			workflowID:   WorkflowID("unknown"),
			state:        StateDispatchable,
			dispatchable: true,
			wantErr:      true,
		},
		{
			name:         "unknown state",
			workflowID:   WorkflowRecovery,
			state:        StateID("unknown"),
			dispatchable: false,
			wantErr:      true,
		},
		{
			name:          "invalid terminal for workflow",
			workflowID:    WorkflowPlanExecutePlanner,
			state:         StateTerminal,
			terminalState: TerminalStateDirtyScope,
			dispatchable:  false,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStateProjection(tt.workflowID, tt.state, tt.terminalState, tt.dispatchable)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateStateProjection() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTransitionAcceptsKnownTransition(t *testing.T) {
	to, err := ValidateTransition(WorkflowPlanExecuteExecutor, StateExecuting, TransitionEventEditsApplied)
	if err != nil {
		t.Fatalf("ValidateTransition returned error: %v", err)
	}
	if to != StateReviewing {
		t.Fatalf("transition target = %q, want %q", to, StateReviewing)
	}
}

func TestValidateTransitionRejectsUnknownTransition(t *testing.T) {
	if _, err := ValidateTransition(WorkflowPlanExecuteExecutor, StateExecuting, TransitionEvent("reviewed-ish")); err == nil {
		t.Fatalf("unknown transition should fail")
	}
}

func TestMachineDefinitionsHaveStableTransitionGraphs(t *testing.T) {
	for _, definition := range Definitions() {
		t.Run(string(definition.WorkflowID), func(t *testing.T) {
			states := map[StateID]bool{}
			for _, state := range definition.States {
				if states[state] {
					t.Fatalf("duplicate state %q", state)
				}
				states[state] = true
			}

			terminalStates := map[TerminalState]bool{}
			for _, terminal := range definition.TerminalStates {
				if terminalStates[terminal] {
					t.Fatalf("duplicate terminal state %q", terminal)
				}
				terminalStates[terminal] = true
			}

			transitionKeys := map[string]bool{}
			outgoing := map[StateID]int{}
			incoming := map[StateID]int{}
			terminalTransitions := map[TerminalState]bool{}
			for _, transition := range definition.Transitions {
				key := string(transition.From) + "\x00" + string(transition.On)
				if transitionKeys[key] {
					t.Fatalf("duplicate transition from %q on %q", transition.From, transition.On)
				}
				transitionKeys[key] = true
				outgoing[transition.From]++
				incoming[transition.To]++
				if transition.To == StateTerminal {
					terminalTransitions[TerminalState(transition.On)] = true
				}
			}

			for _, state := range definition.States {
				if state == StateTerminal {
					continue
				}
				if state != definition.InitialState && incoming[state] == 0 {
					t.Fatalf("state %q is unreachable", state)
				}
				if outgoing[state] == 0 {
					t.Fatalf("nonterminal state %q has no outgoing transition", state)
				}
			}

			reachable := map[StateID]bool{definition.InitialState: true}
			changed := true
			for changed {
				changed = false
				for _, transition := range definition.Transitions {
					if !reachable[transition.From] || reachable[transition.To] {
						continue
					}
					reachable[transition.To] = true
					changed = true
				}
			}
			for _, state := range definition.States {
				if !reachable[state] {
					t.Fatalf("state %q is not reachable from initial state %q", state, definition.InitialState)
				}
			}

			for terminal := range terminalStates {
				if !terminalTransitions[terminal] {
					t.Fatalf("terminal state %q has no transition to terminal node", terminal)
				}
			}
		})
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
