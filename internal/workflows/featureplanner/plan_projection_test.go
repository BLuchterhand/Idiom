package featureplanner

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("select feature plan projection candidate prefers rejected plan during repair", func() {
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
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected displayable projection candidate"))
	gomega.Expect(candidate.Plan.FeatureID).To(gomega.Equal("rejected"), fmt.Sprintf("candidate = %+v, want rejected plan during repair", candidate))
	gomega.Expect(candidate.RejectedCandidate.PlanHash).To(gomega.Equal("sha256:rejected"), fmt.Sprintf("candidate = %+v, want rejected plan during repair", candidate))

})
var _ = ginkgo.It("select feature plan projection candidate uses active plan when accepted", func() {
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
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected displayable projection candidate"))
	gomega.Expect(candidate.Plan.FeatureID).To(gomega.Equal("active"), fmt.Sprintf("candidate = %+v, want active accepted plan", candidate))
	gomega.Expect(candidate.RejectedCandidate.PlanHash).To(gomega.Equal(""), fmt.Sprintf("candidate = %+v, want active accepted plan", candidate))

})
var _ = ginkgo.It("active feature plan proposed paths combines question and plan paths", func() {
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
	gomega.Expect(len(got)).To(gomega.Equal(len(want)), fmt.Sprintf("paths = %+v, want %+v", got, want))

	for i := range want {
		gomega.Expect(got[i]).To(gomega.Equal(want[i]), fmt.Sprintf("paths = %+v, want %+v", got, want))

	}
})
var _ = ginkgo.It("feature plan traceability review includes BDD variants and tests", func() {
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
	gomega.Expect(len(review.Behaviors)).To(gomega.Equal(1), fmt.Sprintf("behaviors = %+v, want one", review.Behaviors))

	behavior := review.Behaviors[0]
	for _, want := range []string{"BDD-1"} {
		gomega.Expect(containsString(behavior.Variants, want)).To(gomega.BeTrue(), fmt.Sprintf("variants = %+v, missing %q", behavior.Variants, want))

	}
	gomega.Expect(containsString(behavior.Invariants, "INV-1")).To(gomega.BeTrue(), fmt.Sprintf("traceability behavior missing expected details: %+v", behavior))
	gomega.Expect(containsString(behavior.TestCases, "TDD-1")).To(gomega.BeTrue(), fmt.Sprintf("traceability behavior missing expected details: %+v", behavior))
	gomega.Expect(containsString(behavior.TestFiles, "tests/test_memory.py")).To(gomega.BeTrue(), fmt.Sprintf("traceability behavior missing expected details: %+v", behavior))
	gomega.Expect(containsString(behavior.Commands, "pytest tests/test_memory.py")).To(gomega.BeTrue(), fmt.Sprintf("traceability behavior missing expected details: %+v", behavior))
	gomega.Expect(containsString(behavior.AtomTitles, "Memory core")).To(gomega.BeTrue(), fmt.Sprintf("traceability behavior missing expected details: %+v", behavior))
	gomega.Expect(containsString(behavior.Surfaces, "SURF-DOMAIN: domain_logic [accepted]")).To(gomega.BeTrue(), fmt.Sprintf("traceability behavior missing expected details: %+v", behavior))
	gomega.Expect(len(review.Surfaces)).To(gomega.Equal(2), fmt.Sprintf("surface review count = %d, want 2: %+v", len(review.Surfaces), review.Surfaces))
	gomega.Expect(review.Surfaces[0].Status).To(gomega.Equal("accepted"), fmt.Sprintf("in-scope surface should be accepted and obligation-linked: %+v", review.Surfaces[0]))
	gomega.Expect(review.Surfaces[0].MissingObligations).To(gomega.BeFalse(), fmt.Sprintf("in-scope surface should be accepted and obligation-linked: %+v", review.Surfaces[0]))

})
var _ = ginkgo.It("feature plan traceability review flags in scope surface without obligation", func() {
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
	gomega.Expect(review.Surfaces).To(gomega.HaveLen(1), fmt.Sprintf("expected in-scope evidence-only surface to be flagged: %+v", review.Surfaces))
	gomega.Expect(review.Surfaces[0].MissingObligations).To(gomega.BeTrue(), fmt.Sprintf("expected in-scope evidence-only surface to be flagged: %+v", review.Surfaces))
	gomega.Expect(review.Behaviors).To(gomega.HaveLen(1), fmt.Sprintf("expected missing surface obligation to appear as a weak behavior row: %+v", review.Behaviors))
	gomega.Expect(review.Behaviors[0].MissingTests).To(gomega.BeTrue(), fmt.Sprintf("expected missing surface obligation to appear as a weak behavior row: %+v", review.Behaviors))

})

func validProjectionPlan(id string, objective string) FeaturePlan {
	return FeaturePlan{
		FeatureID: id,
		TopLevelScope: TopLevelScope{
			Objective: objective,
		},
	}
}
