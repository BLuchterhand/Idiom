package featureplanner

import (
	"encoding/json"
	"testing"
)

func TestProductWorkflowBrief_JSONRoundTrip(t *testing.T) {
	brief := ProductWorkflowBrief{
		EffortID: "test-effort-1",
		TaskID:   "impl-0001",
		PRDRequirements: []PRDRequirementBrief{
			{ID: "REQ-001", Priority: "P0", Statement: "Do the thing", Testability: "automated", Obligations: []string{"TDD-1"}},
		},
		AcceptedADRIDs:   []string{"ADR-001"},
		AcceptedADRPaths: []string{"adrs/001-test.md"},
		TDDObligations: []TDDObligationBrief{
			{ID: "TDD-1", Behavior: "test passes", Command: "go test ./...", RequiredBefore: "implementation", Status: "open"},
		},
		CoverageTier:       "behavior_standard",
		CoverageGatePassed: true,
		CoverageBlockers:   []string{},
		FilesToModify:      []string{"internal/example/thing.go"},
		RequiredBehavior:   []string{"thing must work"},
		Tests:              []string{"go test ./internal/example/..."},
		Acceptance:         []string{"Given thing, when used, then it works"},
		Constraints:        []string{"no new dependencies"},
		Decisions:          []string{"use map not slice"},
		Blockers:           []string{},
		ArtifactRootPath:   "started/test-effort",
	}

	data, err := json.Marshal(brief)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var roundTripped ProductWorkflowBrief

	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if roundTripped.EffortID != brief.EffortID {
		t.Errorf("EffortID: got %q want %q", roundTripped.EffortID, brief.EffortID)
	}

	if roundTripped.TaskID != brief.TaskID {
		t.Errorf("TaskID: got %q want %q", roundTripped.TaskID, brief.TaskID)
	}

	if len(roundTripped.PRDRequirements) != len(brief.PRDRequirements) {
		t.Fatalf("PRDRequirements length: got %d want %d", len(roundTripped.PRDRequirements), len(brief.PRDRequirements))
	}

	if roundTripped.PRDRequirements[0].ID != "REQ-001" {
		t.Errorf("PRDRequirements[0].ID: got %q want REQ-001", roundTripped.PRDRequirements[0].ID)
	}

	if len(roundTripped.TDDObligations) != 1 {
		t.Fatalf("TDDObligations length: got %d want 1", len(roundTripped.TDDObligations))
	}

	if roundTripped.TDDObligations[0].ID != "TDD-1" {
		t.Errorf("TDDObligations[0].ID: got %q want TDD-1", roundTripped.TDDObligations[0].ID)
	}

	if !roundTripped.CoverageGatePassed {
		t.Error("CoverageGatePassed should be true")
	}
}

func TestProductWorkflowBrief_MinimalRoundTrip(t *testing.T) {
	brief := ProductWorkflowBrief{
		EffortID:     "minimal",
		CoverageTier: "local_low",
	}

	data, err := json.Marshal(brief)
	if err != nil {
		t.Fatalf("marshal minimal: %v", err)
	}

	var roundTripped ProductWorkflowBrief

	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal minimal: %v", err)
	}

	if roundTripped.EffortID != "minimal" {
		t.Errorf("EffortID: got %q want minimal", roundTripped.EffortID)
	}
}

func TestProductWorkflowBrief_AllowsDispatchForPendingImplementationTDDEvidence(t *testing.T) {
	brief := ProductWorkflowBrief{
		CoverageGatePassed: false,
		CoverageBlockers:   []string{"expected all 1 TDD obligations to have verification evidence"},
		CoverageBlockerKinds: []string{
			"coverage_tdd_evidence_missing",
		},
		TDDObligations: []TDDObligationBrief{{
			ID:             "TDD-1",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}

	if !brief.AllowsFeaturePlanDispatch() {
		t.Fatal("expected pending implementation TDD verification to allow feature plan dispatch")
	}
}

func TestProductWorkflowBrief_TypedCoverageBlockerKindsOverrideProse(t *testing.T) {
	brief := ProductWorkflowBrief{
		CoverageGatePassed: false,
		CoverageBlockers:   []string{"TDD verification evidence text in an unrelated blocker"},
		CoverageBlockerKinds: []string{
			"coverage_evidence_acceptance_incomplete",
		},
		TDDObligations: []TDDObligationBrief{{
			ID:             "TDD-1",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}

	if brief.AllowsFeaturePlanDispatch() {
		t.Fatal("typed non-TDD coverage blocker must keep feature plan dispatch blocked")
	}
}

func TestProductWorkflowBrief_RejectsAmbiguousTDDEvidenceDispatch(t *testing.T) {
	tests := []struct {
		name       string
		obligation TDDObligationBrief
	}{
		{
			name:       "missing command",
			obligation: TDDObligationBrief{ID: "TDD-1", RequiredBefore: "implementation", Status: "planned"},
		},
		{
			name:       "close only obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "close", Status: "planned"},
		},
		{
			name:       "failed obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "implementation", Status: "failed"},
		},
		{
			name:       "blocked obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "implementation", Status: "blocked"},
		},
		{
			name:       "already passed obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "implementation", Status: "passed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			brief := ProductWorkflowBrief{
				CoverageGatePassed: false,
				CoverageBlockers:   []string{"expected all 1 TDD obligations to have verification evidence"},
				TDDObligations:     []TDDObligationBrief{tt.obligation},
			}

			if brief.AllowsFeaturePlanDispatch() {
				t.Fatalf("expected %s to keep feature plan dispatch blocked", tt.name)
			}
		})
	}
}
