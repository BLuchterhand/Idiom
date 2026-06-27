package planexecute

import (
	"strings"
	"testing"

	"idiom/internal/contracts/workflowstate"
)

func TestPlanExecuteExactPlanEditsRequiresDispatchableStateBeforeHeadingScan(t *testing.T) {
	prompt := "# Atom\n\n## Required Edits\n\npkg/feature.go\n\nOld text:\n```go\nreturn 1\n```\n\nNew text:\n```go\nreturn 2\n```\n"
	edits, reason := planExecuteExactPlanEditsForWorkspaceWithDispatchState(prompt, []string{"pkg/feature.go"}, "", PlanExecuteDispatchState{
		WorkflowID:       workflowstate.WorkflowPlanExecutePlanner,
		State:            workflowstate.StateTerminal,
		ExpectedArtifact: planExecuteExpectedArtifactExactPlan,
		Reason:           "missing-scope",
	})

	if len(edits) != 0 {
		t.Fatalf("non-dispatchable state should not parse heading edits: %#v", edits)
	}
	if reason != "missing-scope" {
		t.Fatalf("reason = %q, want typed non-dispatch reason", reason)
	}
}

func TestPlanExecuteExactPlanEditsUsesLegacyHeadingOnlyWhenDispatchable(t *testing.T) {
	prompt := "# Atom\n\n## Required Edits\n\npkg/feature.go\n\nOld text:\n```go\nreturn 1\n```\n\nNew text:\n```go\nreturn 2\n```\n"
	edits, reason := planExecuteExactPlanEditsForWorkspaceWithDispatchState(prompt, []string{"pkg/feature.go"}, "", PlanExecuteDispatchState{
		WorkflowID:            workflowstate.WorkflowPlanExecutePlanner,
		State:                 workflowstate.StateDispatchable,
		Dispatchable:          true,
		ExpectedArtifact:      planExecuteExpectedArtifactExactPlan,
		LegacyHeadingFallback: true,
	})

	if reason != "" || len(edits) != 1 {
		t.Fatalf("dispatchable legacy heading parse failed edits=%#v reason=%q", edits, reason)
	}
	if edits[0].Path != "pkg/feature.go" ||
		!strings.Contains(edits[0].OldText, "return 1") ||
		!strings.Contains(edits[0].NewText, "return 2") {

		t.Fatalf("unexpected parsed edit: %#v", edits[0])
	}
}

func TestPlanExecuteExactPlanDispatchStateCarriesTerminalStateForMissingScope(t *testing.T) {
	state := planExecuteExactPlanDispatchState(nil)

	if state.State != workflowstate.StateTerminal {
		t.Fatalf("state = %q, want terminal", state.State)
	}
	if state.TerminalState != workflowstate.TerminalStateMissingScope {
		t.Fatalf("terminal_state = %q, want missing-scope", state.TerminalState)
	}
	if state.Dispatchable {
		t.Fatal("missing required paths must not be dispatchable")
	}
}
