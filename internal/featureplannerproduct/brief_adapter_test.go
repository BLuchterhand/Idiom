package featureplannerproduct

import (
	"testing"

	"idiom/internal/productworkflow"
)

func TestBriefFromImplementationHandoffCarriesGrounding(t *testing.T) {
	handoff := productworkflow.ImplementationHandoff{
		EffortID:             "effort-1",
		TaskID:               "task-1",
		EffortRoot:           "started/effort-1",
		ArtifactRootPath:     "started/effort-1",
		StatusBucket:         "started",
		CurrentPhase:         "phase-4",
		CurrentState:         "briefing",
		NextValidActions:     []string{"feature-planner"},
		CoverageTier:         "behavior_standard",
		CoverageGatePassed:   false,
		CoverageBlockers:     []string{"expected all 1 TDD obligations to have verification evidence"},
		CoverageBlockerKinds: []string{"coverage_tdd_evidence_missing"},
		AcceptedADRIDs:       []string{"ADR-1"},
		AcceptedADRPaths:     []string{"adrs/1.md"},
		FilesToModify:        []string{"internal/example.go"},
		ContextEvidencePaths: []string{"internal/context.go"},
		RequiredBehavior:     []string{"do the thing"},
		Tests:                []string{"go test ./..."},
		Acceptance:           []string{"accepted"},
		Constraints:          []string{"scoped"},
		Decisions:            []string{"use existing API"},
		Blockers:             nil,
		PRDRequirements: []productworkflow.ImplementationHandoffPRDRequirement{{
			ID:          "REQ-1",
			Priority:    "P0",
			Statement:   "requirement",
			Testability: "automated",
			Metric:      "metric",
			Obligations: []string{"TDD-1"},
		}},
		TDDObligations: []productworkflow.ImplementationHandoffTDDObligation{{
			ID:             "TDD-1",
			Behavior:       "behavior",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}

	brief := BriefFromImplementationHandoff(handoff)

	if brief == nil {
		t.Fatal("expected feature planner brief")
	}
	if brief.EffortID != handoff.EffortID || brief.TaskID != handoff.TaskID {
		t.Fatalf("brief identity mismatch: brief=%+v handoff=%+v", brief, handoff)
	}
	if brief.StatusBucket != handoff.StatusBucket || brief.CurrentPhase != handoff.CurrentPhase || brief.CurrentState != handoff.CurrentState {
		t.Fatalf("brief workflow state mismatch: brief=%+v handoff=%+v", brief, handoff)
	}
	if len(brief.NextValidActions) != len(handoff.NextValidActions) {
		t.Fatalf("brief next valid actions mismatch: brief=%+v handoff=%+v", brief, handoff)
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
	if !brief.AllowsFeaturePlanDispatch() {
		t.Fatalf("typed TDD-only pending evidence should allow feature-planner dispatch: %+v", brief)
	}
}
