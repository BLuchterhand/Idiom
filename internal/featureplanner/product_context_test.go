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

