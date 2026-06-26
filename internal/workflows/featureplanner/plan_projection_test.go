package featureplanner

import "testing"

func TestSelectFeaturePlanProjectionCandidatePrefersRejectedPlanDuringRepair(t *testing.T) {
	active := validProjectionPlan("active", "Active objective")
	rejected := validProjectionPlan("rejected", "Rejected objective")
	state := FeatureState{
		Status: FeatureStatePlanningRepair,
		Plan:   active,
		RejectedPlanCandidates: []RejectedPlanCandidate{{
			Plan:     rejected,
			PlanHash: "sha256:rejected",
		}},
	}

	candidate, ok := SelectFeaturePlanProjectionCandidate(state)
	if !ok {
		t.Fatal("expected displayable projection candidate")
	}
	if candidate.Plan.FeatureID != "rejected" || candidate.RejectedCandidate.PlanHash != "sha256:rejected" {
		t.Fatalf("candidate = %+v, want rejected plan during repair", candidate)
	}
}

func TestSelectFeaturePlanProjectionCandidateUsesActivePlanWhenAccepted(t *testing.T) {
	active := validProjectionPlan("active", "Active objective")
	rejected := validProjectionPlan("rejected", "Rejected objective")
	state := FeatureState{
		Status: FeatureStateAwaitingApproval,
		Plan:   active,
		RejectedPlanCandidates: []RejectedPlanCandidate{{
			Plan:     rejected,
			PlanHash: "sha256:rejected",
		}},
	}

	candidate, ok := SelectFeaturePlanProjectionCandidate(state)
	if !ok {
		t.Fatal("expected displayable projection candidate")
	}
	if candidate.Plan.FeatureID != "active" || candidate.RejectedCandidate.PlanHash != "" {
		t.Fatalf("candidate = %+v, want active accepted plan", candidate)
	}
}

func TestActiveFeaturePlanProposedPathsCombinesQuestionAndPlanPaths(t *testing.T) {
	plan := validProjectionPlan("active", "Active objective")
	plan.Atoms = []FeatureAtomPlan{{
		RequiredEditPaths: []string{"src/brain.py", "tests/test_brain.py"},
		AllowedEditPaths:  []string{"docs/brain.md"},
		TestFirst: TestFirstPlan{
			TestArtifactPaths: []string{"tests/test_brain.py"},
		},
		PlannedTDDSnapshot: PlannedTDDSnapshot{
			TestArtifactPaths: []string{"tests/test_brain.py", "tests/test_agent.py"},
		},
	}}
	state := FeatureState{OpenQuestions: []PlannerQuestion{{
		Status:        "open",
		AffectedPaths: []string{"pyproject.toml", "src/brain.py"},
	}, {
		Status:        "answered",
		AffectedPaths: []string{"ignored.txt"},
	}}}

	got := ActiveFeaturePlanProposedPaths(state, plan)
	want := []string{"pyproject.toml", "src/brain.py", "tests/test_brain.py", "docs/brain.md", "tests/test_agent.py"}
	if len(got) != len(want) {
		t.Fatalf("paths = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths = %+v, want %+v", got, want)
		}
	}
}

func TestFeaturePlanTraceabilityReviewIncludesBDDVariantsAndTests(t *testing.T) {
	plan := validProjectionPlan("feature", "Active objective")
	plan.TDDContract.Obligations = []TDDObligation{{
		ID:       "TDD-1",
		Behavior: "Memory persists across turns",
		Command:  "pytest tests/test_memory.py",
	}}
	plan.BehavioralCoverageDraft = &BehavioralCoverageDraft{
		SurfaceInventory: []CoverageDraftSurfaceDecision{{
			ID:          "SURF-DOMAIN",
			Surface:     "domain_logic",
			Status:      "in_scope",
			Rationale:   "memory behavior is in scope",
			Obligations: []string{"TDD-1"},
		}, {
			ID:        "SURF-API",
			Surface:   "api_transport",
			Status:    "not_required",
			Rationale: "no external transport boundary is requested",
		}},
		Scenarios: []CoverageDraftScenario{{
			ID:          "BDD-1",
			Obligations: []string{"TDD-1"},
		}},
		Invariants: []CoverageDraftInvariant{{
			ID:          "INV-1",
			Obligations: []string{"TDD-1"},
		}},
		TestObligations: []CoverageDraftTestObligation{{
			ID:       "TDD-1",
			Behavior: "Memory persists across turns",
			Command:  "pytest tests/test_memory.py",
		}},
	}
	plan.Atoms = []FeatureAtomPlan{{
		Title:          "Memory core",
		SDDRefs:        []string{"SDD-1"},
		TDDObligations: []string{"TDD-1"},
		TestFirst: TestFirstPlan{
			Commands:          []string{"pytest tests/test_memory.py"},
			TestArtifactPaths: []string{"tests/test_memory.py"},
		},
	}}

	review := FeaturePlanTraceabilityReviewForPlan(plan)
	if len(review.Behaviors) != 1 {
		t.Fatalf("behaviors = %+v, want one", review.Behaviors)
	}
	behavior := review.Behaviors[0]
	for _, want := range []string{"BDD-1"} {
		if !containsString(behavior.Variants, want) {
			t.Fatalf("variants = %+v, missing %q", behavior.Variants, want)
		}
	}
	if !containsString(behavior.Invariants, "INV-1") ||
		!containsString(behavior.TestCases, "TDD-1") ||
		!containsString(behavior.TestFiles, "tests/test_memory.py") ||
		!containsString(behavior.Commands, "pytest tests/test_memory.py") ||
		!containsString(behavior.AtomTitles, "Memory core") ||
		!containsString(behavior.Surfaces, "SURF-DOMAIN: domain_logic [accepted]") {
		t.Fatalf("traceability behavior missing expected details: %+v", behavior)
	}
	if len(review.Surfaces) != 2 {
		t.Fatalf("surface review count = %d, want 2: %+v", len(review.Surfaces), review.Surfaces)
	}
	if review.Surfaces[0].Status != "accepted" || review.Surfaces[0].MissingObligations {
		t.Fatalf("in-scope surface should be accepted and obligation-linked: %+v", review.Surfaces[0])
	}
}

func TestFeaturePlanTraceabilityReviewFlagsInScopeSurfaceWithoutObligation(t *testing.T) {
	plan := validProjectionPlan("feature", "Active objective")
	plan.BehavioralCoverageDraft = &BehavioralCoverageDraft{
		SurfaceInventory: []CoverageDraftSurfaceDecision{{
			ID:           "SURF-PERSISTENCE",
			Surface:      "persistence",
			Status:       "in_scope",
			Rationale:    "durable behavior is requested",
			EvidenceRefs: []string{"docs/design.md#persistence"},
		}},
	}

	review := FeaturePlanTraceabilityReviewForPlan(plan)
	if len(review.Surfaces) != 1 || !review.Surfaces[0].MissingObligations {
		t.Fatalf("expected in-scope evidence-only surface to be flagged: %+v", review.Surfaces)
	}
	if len(review.Behaviors) != 1 || !review.Behaviors[0].MissingTests {
		t.Fatalf("expected missing surface obligation to appear as a weak behavior row: %+v", review.Behaviors)
	}
}

func validProjectionPlan(id string, objective string) FeaturePlan {
	return FeaturePlan{
		FeatureID: id,
		TopLevelScope: TopLevelScope{
			Objective: objective,
		},
	}
}
