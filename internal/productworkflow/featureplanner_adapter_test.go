package productworkflow

import "testing"

func TestFeaturePlannerBriefFromImplementationHandoffCarriesGrounding(t *testing.T) {
	handoff := BuildImplementationHandoff(implementationHandoffInput(t.TempDir()))
	brief := FeaturePlannerBriefFromImplementationHandoff(handoff)

	if brief == nil {
		t.Fatal("expected feature planner brief")
	}
	if brief.EffortID != handoff.EffortID || brief.TaskID != handoff.TaskID {
		t.Fatalf("brief identity mismatch: brief=%+v handoff=%+v", brief, handoff)
	}
	if len(brief.FilesToModify) == 0 {
		t.Fatalf("expected files_to_modify from handoff, got %+v", brief)
	}
	if len(brief.RequiredBehavior) == 0 || len(brief.Tests) == 0 || len(brief.Acceptance) == 0 || len(brief.Constraints) == 0 || len(brief.Decisions) == 0 {
		t.Fatalf("expected full implementation grounding, got %+v", brief)
	}
	if len(brief.PRDRequirements) != len(handoff.PRDRequirements) || len(brief.TDDObligations) != len(handoff.TDDObligations) {
		t.Fatalf("expected artifact refs to round-trip, brief=%+v handoff=%+v", brief, handoff)
	}
}
