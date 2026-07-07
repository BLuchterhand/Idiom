package workspace

import (
	"slices"
	"testing"
)

func TestConcreteRequiredToolsProjectsSymbolicWorkspaceRequirements(t *testing.T) {
	historyTools := ConcreteRequiredTools(RequiredToolGitHistoryLookup)
	if !slices.Equal(historyTools, []string{ToolNameHistoryRange, ToolNameHistorySymbol}) {
		t.Fatalf("history required-tool projection = %#v", historyTools)
	}

	writeTools := ConcreteRequiredTools(RequiredToolEditFileOrApplyPatch)
	wantWriteTools := WorkspacePerEditWriteToolNames()
	if !slices.Equal(writeTools, wantWriteTools) {
		t.Fatalf("edit required-tool projection = %#v, want per-edit workspace writes %#v", writeTools, wantWriteTools)
	}

	verifyTools := ConcreteRequiredTools(RequiredToolSourceSanityOrVerificationShell)
	if !slices.Equal(verifyTools, []string{ToolNameSourceSanity, ToolNameShell}) {
		t.Fatalf("verification required-tool projection = %#v", verifyTools)
	}

	boundedEvidenceTools := ConcreteRequiredTools(RequiredToolBoundedWorkspaceEvidence)
	wantBoundedEvidenceTools := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteCandidateSource)
	if !slices.Equal(boundedEvidenceTools, wantBoundedEvidenceTools) {
		t.Fatalf("bounded evidence projection = %#v, want candidate-source phase %#v", boundedEvidenceTools, wantBoundedEvidenceTools)
	}
}

func TestConcreteRequiredToolsPassesConcreteToolNamesThrough(t *testing.T) {
	if got := ConcreteRequiredTools(ToolNameSubmitPlan); !slices.Equal(got, []string{ToolNameSubmitPlan}) {
		t.Fatalf("concrete submit_plan projection = %#v", got)
	}

	if got := ConcreteRequiredTools(" "); len(got) != 0 {
		t.Fatalf("blank required tool projection = %#v", got)
	}
}
