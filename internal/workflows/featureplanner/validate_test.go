package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

const (
	validTDDSnapshotDigest    = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	validTDDSnapshotDigestTwo = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

var _ = ginkgo.It("validate plan requires TDD before implementation", func() {
	plan := validPlan()

	plan.Atoms[0].TestFirst.Commands = nil

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing TDD command error, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing test_first.commands"), fmt.Sprintf("expected missing TDD command error, got %v", err))

})
var _ = ginkgo.It("validate plan requires TDD artifact paths in required edit scope", func() {
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_test.go"}

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected demoted TDD test artifact path to reject feature plan"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("internal/labels/labels_test.go"), fmt.Sprintf("validation error should identify demoted TDD test path, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("must be covered by atom.required_edit_paths"), fmt.Sprintf("validation error should identify demoted TDD test path, got %v", err))

})
var _ = ginkgo.It("validate plan rejects demoted implementation paths required by plan and atom", func() {
	plan := validPlan()
	plan.Summary = "Implement CollapseEvent struct and dispatch helper, integrate into colony_sim_tick.cpp, and add focused unit tests."
	plan.TopLevelScope.Objective = "Introduce a CollapseEvent helper to encapsulate collapse dispatch logic, reducing coupling in colony_sim_tick.cpp."
	plan.TopLevelScope.SuccessCriteria = []string{
		"CollapseEvent struct and dispatch helper are implemented in native/src/simulation/world/collapse_event.cpp and declared in native/src/simulation/world/collapse_event.h.",
		"colony_sim_tick.cpp uses CollapseEvent::dispatch for collapse events.",
		"Unit tests in native/tests/test_collapse_event.cpp verify CollapseEvent behavior.",
	}
	plan.Atoms[0].Summary = "Create CollapseEvent struct in collapse_event.h and implementation in collapse_event.cpp with a static dispatch method."
	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = []string{
		"native/src/simulation/world/collapse_event.h",
		"native/src/core/colony_sim_tick.cpp",
	}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].Prompt = "First, check if the work is already satisfied. Otherwise, implement CollapseEvent struct in collapse_event.h and collapse_event.cpp with a static dispatch method, update colony_sim_tick.cpp to use it, and write unit tests in test_collapse_event.cpp."

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected demoted implementation paths to reject feature plan"))

	for _, want := range []string{
		`native/src/simulation/world/collapse_event.h`,
		`native/src/core/colony_sim_tick.cpp`,
		`required by both top-level success criteria and atom implementation text`,
	} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan keeps optional allowed implementation path allowed", func() {
	plan := validPlan()
	plan.TopLevelScope.SuccessCriteria = []string{
		"Primary behavior is implemented in internal/labels/labels.go.",
		"internal/labels/labels_extra.go may be updated if needed for shared helper cleanup.",
	}
	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go", "internal/labels/labels_test.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_extra.go"}
	plan.Atoms[0].Prompt = "Update internal/labels/labels.go. You may update internal/labels/labels_extra.go if needed."
	{

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("optional allowed path should remain valid: %v", err))
	}

})
var _ = ginkgo.It("new state stores bounded prompt summary for resume", func() {
	state := NewState("session", "Implement CollapseEvent helper.\n\nKeep it scoped.", "", 3, time.Unix(1, 0).UTC())
	gomega.Expect(state.PromptSummary).To(gomega.Equal("Implement CollapseEvent helper. Keep it scoped."), fmt.Sprintf("PromptSummary = %q", state.PromptSummary))

	longState := NewState("session", strings.Repeat("x", 2500), "", 3, time.Unix(1, 0).UTC())
	gomega.Expect(len([]rune(longState.PromptSummary))).To(gomega.Equal(2000), fmt.Sprintf("long prompt summary length = %d, want 2000", len([]rune(longState.PromptSummary))))

})
var _ = ginkgo.It("validate plan accepts product SDD refs", func() {
	plan := validProductContextPlan()
	{

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("product PRD/ADR refs should satisfy the generic SDD contract: %v", err))
	}

})
var _ = ginkgo.It("validate plan accepts standalone acceptance IDSDD refs", func() {
	plan := validPlan()
	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = []string{"SDD-COLLAPSE-EVENT-1"}
	{

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("standalone acceptance ID should satisfy atom sdd_refs: %v", err))
	}

})
var _ = ginkgo.It("validate plan rejects undeclared atom SDD refs", func() {
	plan := validPlan()
	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = []string{"REQ-001"}

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected undeclared sdd_refs rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("is not declared by sdd_contract"), fmt.Sprintf("expected undeclared sdd_refs rejection, got %v", err))

})
var _ = ginkgo.It("validate plan rejects blank atom SDD refs", func() {
	plan := validPlan()
	plan.Atoms[0].SDDRefs = []string{""}

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected blank sdd_refs rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing sdd_refs"), fmt.Sprintf("expected blank sdd_refs rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("must not be blank"), fmt.Sprintf("expected blank sdd_refs rejection, got %v", err))

})
var _ = ginkgo.It("validate durable state treats source satisfied as terminal", func() {
	state := NewState("source-satisfied-session", "already done", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStateSourceSatisfied
	state.PlanningLoop.State = FeatureStateSourceSatisfied
	state.OpenQuestions = []PlannerQuestion{{ID: "q-1", Status: "open", QuestionSummary: "stale question"}}

	err := ValidateDurableState(state)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("source-satisfied state should reject terminal-incompatible open questions, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("terminal feature state has open planner questions"), fmt.Sprintf("source-satisfied state should reject terminal-incompatible open questions, got %v", err))

})
var _ = ginkgo.Describe("validate durable state treats failed and planning stuck as terminal", func() {
	tests := []struct {
		name   string
		status string
		setup  func(*FeatureState)
		want   string
	}{
		{
			name:   "failed active run",
			status: FeatureStateFailed,
			setup: func(state *FeatureState) {
				state.ActiveRuns = []ActiveRun{{RunID: "run-1", State: ActiveRunStateRunning}}
			},
			want: "terminal feature state has active runs",
		},
		{
			name:   "planning stuck open question",
			status: FeatureStatePlanningStuck,
			setup: func(state *FeatureState) {
				state.OpenQuestions = []PlannerQuestion{{ID: "q-1", Status: "open", QuestionSummary: "stale question"}}
			},
			want: "terminal feature state has open planner questions",
		},
		{
			name:   "failed recovery packet",
			status: FeatureStateFailed,
			setup: func(state *FeatureState) {
				state.OpenRecoveryPackets = []string{".idiom/recovery/packets/failure-1.json"}
			},
			want: "terminal feature state has open recovery packets",
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			state := NewState("terminal-session", "terminal state", "", 3, time.Unix(1, 0).UTC())
			state.Status = tt.status
			state.PlanningLoop.State = tt.status
			tt.setup(&state)

			err := ValidateDurableState(state)
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected terminal invariant %q, got %v", tt.want, err))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(tt.want), fmt.Sprintf("expected terminal invariant %q, got %v", tt.want, err))

		})
	}
})
var _ = ginkgo.Describe("validate durable state rejects unknown active run state", func() {
	for _, stateName := range []string{"complete", "retry_plan_execute"} {
		ginkgo.It(stateName, func() {
			state := NewState("active-run-state-session", "active run state", "", 3, time.Unix(1, 0).UTC())
			state.ActiveRuns = []ActiveRun{{
				RunID:  "run-1",
				AtomID: "atom-1",
				State:  stateName,
			}}

			err := ValidateDurableState(state)
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unknown active run state rejection for %q, got %v", stateName, err))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(`active run 0 has unknown state "`+stateName+`"`), fmt.Sprintf("expected unknown active run state rejection for %q, got %v", stateName, err))

		})
	}
})
var _ = ginkgo.It("validate plan product workflow context rejects unknown refs", func() {
	plan := validPlan()

	plan.SDDContract.PRDRequirementRefs = []string{"REQ-404"}
	plan.SDDContract.ADRRefs = []string{"ADR-001"}
	plan.SDDContract.Refs = nil
	plan.Atoms[0].SDDRefs = []string{"REQ-404"}
	plan.TDDContract.Obligations[0].ID = "TDD-404"
	plan.Atoms[0].TDDObligations = []string{"TDD-404"}
	plan.Atoms[0].TestFirst.ObligationRefs = []string{"TDD-404"}

	brief := ProductWorkflowBrief{
		CoverageGatePassed: true,
		PRDRequirements:    []PRDRequirementBrief{{ID: "REQ-001"}},
		AcceptedADRIDs:     []string{"ADR-001"},
		TDDObligations:     []TDDObligationBrief{{ID: "TDD-001"}},
		FilesToModify:      []string{"internal/labels/labels.go"},
	}

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected product context validation error"))

	for _, want := range []string{"REQ-404", "TDD-404"} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan product workflow context allows pending TDD verification dispatch", func() {
	plan := validProductContextPlan()
	brief := validProductWorkflowBrief()

	brief.CoverageGatePassed = false
	brief.CoverageBlockers = []string{"expected all 1 TDD obligations to have verification evidence"}
	{

		err := ValidatePlanProductWorkflowContext(plan, brief, "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("pending TDD verification evidence should not block implementation dispatch: %v", err))
	}

})
var _ = ginkgo.It("validate plan product workflow context rejects non TDD coverage blocker", func() {
	plan := validProductContextPlan()
	brief := validProductWorkflowBrief()

	brief.CoverageGatePassed = false
	brief.CoverageBlockers = []string{"expected EARS requirements to map to evidence"}

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected non-TDD coverage blocker to reject feature plan"))

	for _, want := range []string{"coverage gate is blocked", "expected EARS requirements"} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan product workflow context rejects standalone coverage draft", func() {
	plan := validProductContextPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected product context to reject standalone behavioral coverage draft"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("behavioral_coverage_draft"), fmt.Sprintf("validation error should identify superseded standalone draft, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("superseded by product workflow coverage authority"), fmt.Sprintf("validation error should identify superseded standalone draft, got %v", err))

})
var _ = ginkgo.It("validate plan product workflow context rejects competing coverage gate result", func() {
	plan := validProductContextPlan()
	plan.CoverageGateResult = &CoverageGateResult{
		Status:       "pass",
		EffortID:     "planner-decided",
		CoverageTier: "contract_high",
		CheckedAt:    "2026-06-22T00:00:00Z",
	}
	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected product context to reject planner-supplied coverage gate result"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("must consume product workflow coverage authority"), fmt.Sprintf("validation error should identify competing coverage gate result, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("competing coverage_gate_result"), fmt.Sprintf("validation error should identify competing coverage gate result, got %v", err))

})
var _ = ginkgo.It("validate plan product workflow context requires coverage contract ref when brief provides one", func() {
	plan := validProductContextPlan()
	plan.CoverageContract = nil
	brief := validProductWorkflowBrief()
	brief.CoverageContract = &CoverageContractRef{
		ContractID:   "cov-1",
		Digest:       "sha256:abc",
		EventID:      "evt-cov-1",
		Status:       "pass",
		EffortID:     "effort-1",
		CoverageTier: "contract_high",
	}

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected product context to require coverage_contract ref"))
	gomega.Expect(strings.Contains(err.Error(), "missing coverage_contract ref")).To(gomega.BeTrue(), fmt.Sprintf("validation error should identify missing coverage_contract ref, got %v", err))

	plan.CoverageContract = brief.CoverageContract
	{
		err := ValidatePlanProductWorkflowContext(plan, brief, "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("matching coverage_contract ref should pass: %v", err))
	}

})
var _ = ginkgo.It("product brief requires coverage contract uses product workflow tiers", func() {
	tests := []struct {
		tier string
		want bool
	}{
		{tier: "documentation", want: false},
		{tier: "unit", want: false},
		{tier: "local_low", want: false},
		{tier: "behavior_standard", want: true},
		{tier: "contract_high", want: true},
		{tier: "formal_optional", want: true},
		{tier: " behavior_standard ", want: true},
		{tier: "BEHAVIOR_STANDARD", want: false},
	}

	for _, tt := range tests {
		brief := validProductWorkflowBrief()
		brief.CoverageTier = tt.tier
		brief.CoverageContract = nil
		{

			got := productBriefRequiresCoverageContract(brief)
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("productBriefRequiresCoverageContract(%q) = %v, want %v", tt.tier, got, tt.want))
		}

	}
})
var _ = ginkgo.It("validate plan product workflow context rejects empty coverage contract authority ref", func() {
	plan := validProductContextPlan()
	plan.CoverageContract = &CoverageContractRef{}
	brief := validProductWorkflowBrief()
	brief.CoverageContract = &CoverageContractRef{}

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected empty product workflow coverage_contract authority ref to reject"))

	for _, want := range []string{
		"product workflow coverage_contract missing contract_id",
		"product workflow coverage_contract missing digest",
		"product workflow coverage_contract missing event_id",
		"product workflow coverage_contract missing status",
		"product workflow coverage_contract missing effort_id",
		"product workflow coverage_contract missing coverage_tier",
	} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan product workflow context rejects mismatched coverage contract ref", func() {
	plan := validProductContextPlan()
	plan.CoverageContract = &CoverageContractRef{
		ContractID:   "cov-wrong",
		Digest:       "sha256:wrong",
		EventID:      "evt-wrong",
		Status:       "blocked",
		EffortID:     "effort-1",
		CoverageTier: "unit",
	}
	brief := validProductWorkflowBrief()
	brief.CoverageContract = &CoverageContractRef{
		ContractID:   "cov-1",
		Digest:       "sha256:abc",
		EventID:      "evt-cov-1",
		Status:       "pass",
		EffortID:     "effort-1",
		CoverageTier: "contract_high",
	}

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected mismatched coverage_contract ref to reject"))

	for _, want := range []string{"coverage_contract.contract_id", "coverage_contract.digest", "coverage_contract.event_id", "coverage_contract.status", "coverage_contract.coverage_tier"} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan product workflow context rejects planner authored accepted coverage", func() {
	plan := validProductContextPlan()
	plan.AcceptedCoverage = &AcceptedBehavioralCoverage{Authority: "model"}
	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected planner-authored accepted coverage to reject"))
	gomega.Expect(strings.Contains(err.Error(), "accepted_coverage is product workflow authority")).To(gomega.BeTrue(), fmt.Sprintf("expected accepted coverage authority error, got %v", err))

})
var _ = ginkgo.Describe("validate plan product workflow context rejects invalid brief accepted coverage authority", func() {
	tests := []struct {
		name   string
		mutate func(*AcceptedBehavioralCoverage)
		want   string
	}{
		{
			name: "wrong authority",
			mutate: func(coverage *AcceptedBehavioralCoverage) {
				coverage.Authority = "model"
			},
			want: "accepted_coverage.authority must be product_workflow",
		},
		{
			name: "blocked status",
			mutate: func(coverage *AcceptedBehavioralCoverage) {
				coverage.Status = "blocked"
			},
			want: "accepted_coverage status is not accepted",
		},
		{
			name: "mismatched contract",
			mutate: func(coverage *AcceptedBehavioralCoverage) {
				coverage.Contract.Digest = "sha256:wrong"
			},
			want: "coverage_contract.digest does not match",
		},
		{
			name: "mismatched tier",
			mutate: func(coverage *AcceptedBehavioralCoverage) {
				coverage.Tier = "unit"
			},
			want: "accepted_coverage.tier",
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			plan := validProductContextPlan()
			brief := validProductWorkflowBrief()
			brief.AcceptedCoverage = validAcceptedBehavioralCoverage()
			tt.mutate(brief.AcceptedCoverage)

			err := ValidatePlanProductWorkflowContext(plan, brief, "")
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected brief accepted coverage authority error containing %q, got %v", tt.want, err))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(tt.want), fmt.Sprintf("expected brief accepted coverage authority error containing %q, got %v", tt.want, err))

		})
	}
})
var _ = ginkgo.It("normalize plan product workflow context stamps accepted coverage authority", func() {
	plan := validProductContextPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()

	brief := validProductWorkflowBrief()
	brief.AcceptedCoverage = validAcceptedBehavioralCoverage()

	normalized := NormalizePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(normalized.BehavioralCoverageDraft).To(gomega.BeNil(), fmt.Sprintf("product normalization should clear superseded standalone draft: %+v", normalized.BehavioralCoverageDraft))
	gomega.Expect(normalized.AcceptedCoverage).NotTo(gomega.BeNil(), fmt.Sprint("product normalization should stamp accepted coverage from product workflow authority"))
	gomega.Expect(normalized.AcceptedCoverage.Authority).To(gomega.Equal("product_workflow"), fmt.Sprintf("unexpected accepted coverage stamp: %+v", normalized.AcceptedCoverage))
	gomega.Expect(normalized.AcceptedCoverage.Contract.ContractID).To(gomega.Equal("cov-1"), fmt.Sprintf("unexpected accepted coverage stamp: %+v", normalized.AcceptedCoverage))
	gomega.Expect(normalized.AcceptedCoverage.Scenarios).To(gomega.HaveLen(1), fmt.Sprintf("unexpected accepted coverage stamp: %+v", normalized.AcceptedCoverage))
	gomega.Expect(normalized.AcceptedCoverage.Scenarios[0].Then[0]).To(gomega.Equal("product behavior is rendered"), fmt.Sprintf("unexpected accepted coverage stamp: %+v", normalized.AcceptedCoverage))

})
var _ = ginkgo.It("validate plan standalone workspace context allows provisional coverage draft", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("standalone context should allow provisional behavioral coverage draft: %v", err))
	}

})
var _ = ginkgo.It("validate plan standalone workspace context requires provisional coverage draft", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = nil

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected standalone context to require behavioral coverage draft"))

	for _, want := range []string{"behavioral_coverage_draft", "BDD scenarios", "concrete TDD traceability"} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan standalone workspace context requires surface inventory", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	plan.BehavioralCoverageDraft.SurfaceInventory = nil

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing surface inventory to reject standalone coverage draft, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing generic behavior surface inventory"), fmt.Sprintf("expected missing surface inventory to reject standalone coverage draft, got %v", err))

})
var _ = ginkgo.It("validate plan standalone workspace context rejects in scope surface without TDD obligation", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	plan.BehavioralCoverageDraft.SurfaceInventory = []CoverageDraftSurfaceDecision{{
		ID:           "SURF-PERSISTENCE",
		Surface:      "persistence",
		Status:       "in_scope",
		Rationale:    "durable behavior is in scope",
		EvidenceRefs: []string{"docs/design.md#persistence"},
	}}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected evidence-only in-scope surface to reject standalone coverage draft, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("in-scope surface missing tdd_obligations"), fmt.Sprintf("expected evidence-only in-scope surface to reject standalone coverage draft, got %v", err))

})
var _ = ginkgo.It("validate plan standalone workspace context rejects open question surface before approval", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	plan.BehavioralCoverageDraft.SurfaceInventory = []CoverageDraftSurfaceDecision{{
		ID:           "SURF-API",
		Surface:      "api_transport",
		Status:       "open_question",
		Rationale:    "operator must choose whether HTTP transport is in scope",
		QuestionRefs: []string{"Q-API"},
	}}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected open question surface to block standalone approval, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unresolved open_question status"), fmt.Sprintf("expected open question surface to block standalone approval, got %v", err))

})
var _ = ginkgo.It("validate plan standalone workspace context rejects planned surface status", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	plan.BehavioralCoverageDraft.SurfaceInventory = []CoverageDraftSurfaceDecision{{
		ID:          "SURF-API",
		Surface:     "api_transport",
		Status:      "planned",
		Rationale:   "planned must not silently satisfy readiness",
		Obligations: []string{"TDD-1"},
	}}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected planned surface status to reject standalone coverage draft, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring(`unsupported status "planned"`), fmt.Sprintf("expected planned surface status to reject standalone coverage draft, got %v", err))

})
var _ = ginkgo.It("parse planner output normalizes flexible coverage concrete data", func() {
	plan := validPlan()
	body := mustPlanJSON(plan)
	var payload map[string]any
	{
		err := json.Unmarshal([]byte(body), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode generated plan: %v", err))
	}

	draft := payload["behavioral_coverage_draft"].(map[string]any)
	exampleRules := draft["example_rules"].([]any)
	firstRule := exampleRules[0].(map[string]any)
	examples := firstRule["examples"].([]any)
	examples[0].(map[string]any)["concrete_data"] = []any{"label", "short label"}
	scenarios := draft["scenarios"].([]any)
	scenarios[0].(map[string]any)["concrete_data"] = map[string]any{"fixture": []any{"draft.json"}, "count": float64(2)}
	mutated, err := json.Marshal(payload)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal mutated plan: %v", err))

	parsed, err := ParsePlannerOutput(string(mutated))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParsePlannerOutput should tolerate flexible concrete_data: %v", err))

	exampleData := parsed.Plan.BehavioralCoverageDraft.ExampleRules[0].Examples[0].ConcreteData
	gomega.Expect(exampleData["item_1"]).To(gomega.Equal("label"), fmt.Sprintf("array concrete_data not normalized: %#v", exampleData))
	gomega.Expect(exampleData["item_2"]).To(gomega.Equal("short label"), fmt.Sprintf("array concrete_data not normalized: %#v", exampleData))

	scenarioData := parsed.Plan.BehavioralCoverageDraft.Scenarios[0].ConcreteData
	gomega.Expect(scenarioData["fixture"]).To(gomega.Equal(`["draft.json"]`), fmt.Sprintf("object concrete_data values not normalized: %#v", scenarioData))
	gomega.Expect(scenarioData["count"]).To(gomega.Equal("2"), fmt.Sprintf("object concrete_data values not normalized: %#v", scenarioData))

})
var _ = ginkgo.It("validate plan standalone workspace context requires every TDD obligation mapped by coverage draft", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	plan.TDDContract.Obligations = append(plan.TDDContract.Obligations, TDDObligation{
		ID:             "TDD-UNMAPPED",
		Behavior:       "Unmapped behavior",
		Command:        "go test ./...",
		RequiredBefore: "implementation",
	})

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unmapped TDD obligation to reject standalone coverage draft"))

	for _, want := range []string{
		`TDD-UNMAPPED`,
		`behavioral_coverage_draft.scenarios`,
		`behavioral_coverage_draft.test_obligations`,
	} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan standalone workspace context allows cross cutting invariants", func() {
	plan := validGreenfieldBrainPlanForTest()
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("cross-cutting invariants should not require one invariant per TDD obligation: %v", err))
	}

})
var _ = ginkgo.It("run standalone rejects narrow TDD only plan before dispatch", func() {
	state := NewState("session", "Plan a greenfield memory MVP.", "", 3, time.Unix(1, 0).UTC())
	plan := validPlan()
	plan.BehavioralCoverageDraft = nil

	result, err := Run(state, RunOptions{
		Prompt:                "Plan a greenfield memory MVP.",
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		gomega.Expect(strings.Contains(prompt, "behavioral_coverage_draft")).To(gomega.BeTrue(), fmt.Sprintf("standalone planner prompt must advertise behavioral_coverage_draft contract:\n%s", prompt))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("narrow TDD-only plan should stop in planning repair, got %s", result.State.Status))
	gomega.Expect(len(result.State.Atoms)).To(gomega.Equal(0), fmt.Sprintf("narrow TDD-only plan must not compile atoms: %+v", result.State.Atoms))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("expected standalone context blocked event, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected standalone context blocked event, got %+v", result.Events))
	{

		got := strings.Join(result.State.Blockers, "\n")
		gomega.Expect(strings.Contains(got, "behavioral_coverage_draft")).To(gomega.BeTrue(), fmt.Sprintf("blocker should name missing behavioral coverage draft, got %q", got))
	}

})
var _ = ginkgo.Describe("validate plan standalone workspace context requires complete production readiness draft entries", func() {
	tests := []struct {
		name   string
		mutate func(*BehavioralCoverageDraft)
		want   string
	}{
		{
			name: "fuzz target missing generators",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.FuzzTargets = []CoverageDraftFuzzTarget{{ID: "FUZZ-1", Target: "ParsePlan", Invariants: []string{"no panic"}, TestRefs: []string{"FuzzParsePlan"}}}
			},
			want: "behavioral coverage fuzz_targets[0] accepted coverage missing target, generators, invariants, or test_refs",
		},
		{
			name: "rag eval missing threshold",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.RAGEvals = []CoverageDraftRAGEval{{ID: "RAG-1", DatasetRefs: []string{"evals/rag.jsonl"}, Metrics: map[string]float64{"groundedness": 0.97}, EvalRefs: []string{"TestRAG"}}}
			},
			want: "behavioral coverage rag_evals[0] accepted coverage missing dataset_refs, metrics, thresholds, or eval_refs",
		},
		{
			name: "security case missing expected",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.SecurityCases = []CoverageDraftSecurityCase{{ID: "SEC-1", Risk: "prompt injection", Attack: "ignore policy", TestRefs: []string{"TestSecurity"}}}
			},
			want: "behavioral coverage security_cases[0] accepted coverage missing risk, attack, expected outcome, or test_refs",
		},
		{
			name: "privacy control missing redaction refs",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.PrivacyControls = []CoverageDraftPrivacyControl{{ID: "PRIV-1", DataClass: "prompt", Policy: "redact", Retention: "structured only", TestRefs: []string{"TestPrivacy"}}}
			},
			want: "behavioral coverage privacy_controls[0] accepted coverage missing data_class, policy, redaction_refs, retention, or test_refs",
		},
		{
			name: "provider contract missing contract refs",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.ProviderContracts = []CoverageDraftProviderContract{{ID: "PACT-1", Provider: "fake-llm", Consumer: "planner", TestRefs: []string{"TestProvider"}}}
			},
			want: "behavioral coverage provider_contracts[0] accepted coverage missing provider, consumer, contract_refs, or test_refs",
		},
		{
			name: "observability missing required fields",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.Observability = []CoverageDraftObservability{{ID: "OBS-1", Signal: "planner_failures_total", InstrumentedPath: "internal/workflows/featureplanner", TestRefs: []string{"TestTelemetry"}}}
			},
			want: "behavioral coverage observability[0] accepted coverage missing signal, instrumented_path, required_fields, or test_refs",
		},
		{
			name: "performance missing measurements",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.PerformanceBounds = []CoverageDraftPerformanceBound{{ID: "PERF-1", Operation: "render", Bound: "bounded"}}
			},
			want: "behavioral coverage performance_bounds[0] accepted coverage missing operation, bound, or measurement_refs",
		},
		{
			name: "e2e missing scripts",
			mutate: func(draft *BehavioralCoverageDraft) {
				draft.E2EJourneys = []CoverageDraftE2EJourney{{ID: "E2E-1", Journey: "greenfield planning", Steps: []string{"start", "plan"}}}
			},
			want: "behavioral coverage e2e_journeys[0] accepted coverage missing journey, steps, or script_refs",
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			plan := validPlan()
			plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
			tt.mutate(plan.BehavioralCoverageDraft)

			err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected standalone draft validation error containing %q, got %v", tt.want, err))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(tt.want), fmt.Sprintf("expected standalone draft validation error containing %q, got %v", tt.want, err))

		})
	}
})
var _ = ginkgo.It("validate plan standalone workspace context uses canonical coverage contract", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	plan.BehavioralCoverageDraft.Invariants[0].PropertyBasedTestRefs = nil

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected canonical behavioral coverage validation to reject missing property-based test refs"))

	for _, want := range []string{
		"behavioral_coverage_draft violates canonical behavioral coverage contract",
		"behavioral coverage invariants[0] accepted coverage missing property_based_test_refs",
	} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("expected validation error containing %q, got %v", want, err))

	}
})
var _ = ginkgo.It("validate plan standalone workspace context rejects non provisional coverage authority", func() {
	plan := validPlan()
	plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	plan.BehavioralCoverageDraft.Authority = "product_workflow"

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected standalone coverage draft with product authority to reject"))
	gomega.Expect(strings.Contains(err.Error(), "standalone_provisional")).To(gomega.BeTrue(), fmt.Sprintf("validation error should identify provisional authority requirement, got %v", err))

})
var _ = ginkgo.It("run rejects product plan with unknown artifact refs", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	plan := validPlan()

	plan.SDDContract.PRDRequirementRefs = []string{"REQ-999"}
	plan.SDDContract.ADRRefs = []string{"ADR-001"}
	plan.Atoms[0].SDDRefs = []string{"REQ-999"}
	plan.TDDContract.Obligations[0].ID = "TDD-001"
	plan.Atoms[0].TDDObligations = []string{"TDD-001"}
	plan.Atoms[0].TestFirst.ObligationRefs = []string{"TDD-001"}
	plan.Atoms[0].TestFirst.PreExecutionEventIDs = []string{"evt-tdd-1"}
	plan.Atoms[0].PlannedTDDSnapshot.ObligationIDs = []string{"TDD-001"}

	brief := &ProductWorkflowBrief{
		EffortID:           "effort-unknown-ref",
		TaskID:             "task-unknown-ref",
		CoverageGatePassed: true,
		PRDRequirements:    []PRDRequirementBrief{{ID: "REQ-001"}},
		AcceptedADRIDs:     []string{"ADR-001"},
		TDDObligations:     []TDDObligationBrief{{ID: "TDD-001"}},
		FilesToModify:      []string{"internal/labels/labels.go"},
	}

	result, err := Run(state, RunOptions{Prompt: "build feature", ProductWorkflowBrief: brief}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)), fmt.Sprintf("implementor must not run after product context rejection"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair", result.State.Status))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected events: %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.product_context_blocked"), fmt.Sprintf("unexpected events: %+v", result.Events))

})
var _ = ginkgo.It("run with product context invalidates existing standalone coverage draft", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.Plan = validPlan()
	state.TaskID = "task-1"
	state.Plan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	brief := validProductWorkflowBrief()

	result, err := Run(state, RunOptions{
		Prompt:               "build feature",
		ProductWorkflowBrief: &brief,
	}, func(_ string, _ string) (string, error) {
		return `{"tool":"ask_planner_question","question_summary":"need source context","blocking_claim":"need source context","requested_decision":"provide source context","question_kind":"external_answer_required","authority_required":"accepted_source","required_before":"plan_approval","affected_fields":["source_localization"]}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Plan.BehavioralCoverageDraft).To(gomega.BeNil(), fmt.Sprintf("product context should invalidate existing standalone draft: %+v", result.State.Plan.BehavioralCoverageDraft))

})
var _ = ginkgo.It("run binds fresh product task identity before planner evidence is produced", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	brief := validProductWorkflowBrief()

	result, err := Run(state, RunOptions{
		Prompt:               "build feature",
		ProductWorkflowBrief: &brief,
	}, func(_ string, _ string) (string, error) {
		return `{"tool":"ask_planner_question","question_summary":"need source context","blocking_claim":"need source context","requested_decision":"provide source context","question_kind":"external_answer_required","authority_required":"accepted_source","required_before":"plan_approval","affected_fields":["source_localization"]}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.State.TaskID).To(gomega.Equal(brief.TaskID))
})
var _ = ginkgo.It("run rejects a different product task identity within the same effort", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	brief := validProductWorkflowBrief()
	state.EffortID = brief.EffortID
	state.TaskID = "other-task"

	_, err := Run(state, RunOptions{Prompt: "build feature", ProductWorkflowBrief: &brief}, func(_ string, _ string) (string, error) {
		ginkgo.Fail("planner must not run after product task identity diverges")

		return "", nil
	})
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(
		`product-bound feature planner state task_id "other-task" does not match product task_id "task-1"`,
	)))
})
var _ = ginkgo.It("run does not transplant a current brief task onto existing verification authority", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	brief := validProductWorkflowBrief()
	state.EffortID = brief.EffortID
	state.EffortRoot = "/tmp/existing-effort"
	state.VerificationClosures = []VerificationClosure{{EventID: "existing-evidence", AtomID: "existing-atom"}}

	_, err := Run(state, RunOptions{Prompt: "build feature", ProductWorkflowBrief: &brief}, func(_ string, _ string) (string, error) {
		ginkgo.Fail("planner must not run when durable evidence is missing task identity")

		return "", nil
	})
	gomega.Expect(err).To(gomega.MatchError("existing product-bound feature planner state is missing durable task_id"))
})
var _ = ginkgo.It("normalize product plan removes context only docs from allowed edit scope", func() {
	plan := validProductContextPlan()

	plan.Atoms[0].AllowedEditPaths = []string{
		"internal/labels/labels.go",
		"internal/labels/labels_test.go",
		"internal/labels/labels_extra.go",
		"adrs/ADR-draft.md",
		".planning/campaign-status.md",
	}

	brief := validProductWorkflowBrief()

	brief.AcceptedADRPaths = []string{"adrs/ADR-draft.md"}
	brief.FilesToModify = append(brief.FilesToModify, "internal/labels/labels_extra.go")

	normalized := NormalizePlanProductWorkflowContext(plan, brief, "")
	{
		err := ValidatePlanProductWorkflowContext(normalized, brief, "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("normalized plan should validate: %v", err))
	}

	allowed := normalized.Atoms[0].AllowedEditPaths
	for _, forbidden := range []string{"adrs/ADR-draft.md", ".planning/campaign-status.md"} {
		gomega.Expect(containsString(allowed, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("context-only doc %q leaked into allowed edit scope: %#v", forbidden, allowed))

	}

	required := normalized.Atoms[0].RequiredEditPaths
	for _, want := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go", "internal/labels/labels_extra.go"} {
		gomega.Expect(containsString(required, want)).To(gomega.BeTrue(), fmt.Sprintf("Files to modify path %q was not promoted to required scope: %#v", want, required))

	}

	for _, forbidden := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go", "internal/labels/labels_extra.go"} {
		gomega.Expect(containsString(allowed, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("required edit path %q leaked into allowed scope: %#v", forbidden, allowed))

	}
})
var _ = ginkgo.It("validate product plan rejects context evidence as edit scope", func() {
	plan := validProductContextPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go", "internal/context/context.go"}

	brief := validProductWorkflowBrief()

	brief.FilesToModify = []string{"internal/labels/labels.go"}
	brief.ContextEvidencePaths = []string{"internal/context/context.go"}

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected context evidence path in edit scope to reject product plan"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("internal/context/context.go"), fmt.Sprintf("validation error should identify context evidence as non-edit scope, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("files_to_modify"), fmt.Sprintf("validation error should identify context evidence as non-edit scope, got %v", err))

})
var _ = ginkgo.It("normalize product plan promotes grounded TDD artifacts to required scope", func() {
	plan := validProductContextPlan()

	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_test.go"}

	brief := validProductWorkflowBrief()

	normalized := NormalizePlanProductWorkflowContext(plan, brief, "")
	{
		err := ValidatePlanProductWorkflowContext(normalized, brief, "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("normalized plan should validate: %v", err))
	}

	required := normalized.Atoms[0].RequiredEditPaths
	for _, want := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go"} {
		gomega.Expect(containsString(required, want)).To(gomega.BeTrue(), fmt.Sprintf("required TDD edit path %q missing from required scope: %#v", want, required))

	}
	gomega.Expect(containsString(normalized.Atoms[0].AllowedEditPaths, "internal/labels/labels_test.go")).To(gomega.BeFalse(), fmt.Sprintf("promoted TDD edit path must not remain optional allowed scope: %#v", normalized.Atoms[0].AllowedEditPaths))

})
var _ = ginkgo.It("validate product plan uses explicit workspace root for package grounding", func() {
	root, plan, brief := workspaceRootProductPlanFixture()
	{

		err := ValidatePlanProductWorkflowContext(plan, brief, root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("explicit workspace root should ground sibling package scope: %v", err))
	}

	normalized := NormalizePlanProductWorkflowContext(plan, brief, root)
	gomega.Expect(containsString(normalized.Atoms[0].AllowedEditPaths, "pkg/generated_helper.go")).To(gomega.BeTrue(), fmt.Sprintf("expected sibling package scope to remain allowed, got %#v", normalized.Atoms[0].AllowedEditPaths))

})
var _ = ginkgo.It("run product plan uses workspace root for package grounding", func() {
	root, plan, brief := workspaceRootProductPlanFixture()
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:               "build feature",
		ProductWorkflowBrief: &brief,
		WorkspaceRoot:        root,
	}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
		}

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("state.Status = %q, want complete; blockers=%+v events=%+v", result.State.Status, result.State.Blockers, result.Events))

})

func workspaceRootProductPlanFixture() (string, FeaturePlan, ProductWorkflowBrief) {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "pkg/feature.go"), "package pkg\n")
	mkdirAndWriteFile(filepath.Join(root, "pkg/feature_test.go"), "package pkg\n")

	plan := validProductContextPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/feature.go", "pkg/feature_test.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"pkg/generated_helper.go"}
	plan.Atoms[0].TestFirst.Commands = []string{"go test ./pkg"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/feature_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"go test ./pkg"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/feature_test.go"}
	plan.TDDContract.Obligations[0].Command = "go test ./pkg"

	brief := validProductWorkflowBrief()

	brief.FilesToModify = []string{"pkg/feature.go", "pkg/feature_test.go"}
	brief.ContextEvidencePaths = []string{"pkg/feature.go"}
	brief.Tests = []string{"go test ./pkg"}
	brief.TDDObligations[0].Command = "go test ./pkg"

	return root, plan, brief
}

var _ = ginkgo.It("validate product plan requires files to modify coverage across atoms", func() {
	plan := validProductContextPlan()
	second := plan.Atoms[0]

	second.ID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels_test.go"}
	plan.Atoms = append(plan.Atoms, second)

	brief := validProductWorkflowBrief()

	brief.FilesToModify = append(brief.FilesToModify, "internal/labels/labels_extra.go")

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected missing Files to modify coverage to reject product plan"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("internal/labels/labels_extra.go"), fmt.Sprintf("validation error should identify missing required Files to modify coverage, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("atom.required_edit_paths"), fmt.Sprintf("validation error should identify missing required Files to modify coverage, got %v", err))

})
var _ = ginkgo.It("validate product plan rejects command not authorized by product TDD", func() {
	root, plan, brief := workspaceRootProductPlanFixture()

	plan.Atoms[0].TestFirst.Commands = []string{"cd native && cmake --build . && ctest -R test_fluid_brewery_cascade --output-on-failure"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanProductWorkflowContext(plan, brief, root)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected product TDD command provenance rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("not one of the source/product TDD commands"), fmt.Sprintf("expected product TDD command provenance rejection, got %v", err))

})
var _ = ginkgo.It("validate product plan rejects top level TDD command not authorized by product TDD", func() {
	root, plan, brief := workspaceRootProductPlanFixture()

	plan.TDDContract.Obligations[0].Command = "cd native && cmake --build . && ctest -R test_fluid_brewery_cascade --output-on-failure"

	err := ValidatePlanProductWorkflowContext(plan, brief, root)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected top-level product TDD command provenance rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("not the source/product TDD command"), fmt.Sprintf("expected top-level product TDD command provenance rejection, got %v", err))

})
var _ = ginkgo.It("validate product plan rejects demoted files to modify before normalization", func() {
	plan := validProductContextPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms[0].AllowedEditPaths = []string{"internal/labels/labels_test.go"}

	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected demoted Files to modify path to reject product plan"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("internal/labels/labels_test.go"), fmt.Sprintf("validation error should identify demoted Files to modify path, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("not omitted or demoted to allowed_edit_paths"), fmt.Sprintf("validation error should identify demoted Files to modify path, got %v", err))

})
var _ = ginkgo.It("validate product plan rejects files to modify allowed in another atom", func() {
	plan := validProductContextPlan()
	second := plan.Atoms[0]

	second.ID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels.go", "internal/labels/labels_test.go"}
	second.AllowedEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms = append(plan.Atoms, second)

	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected Files to modify path in allowed scope to reject product plan"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("internal/labels/labels.go"), fmt.Sprintf("validation error should identify Files to modify allowed-scope demotion, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("must not appear in atom.allowed_edit_paths"), fmt.Sprintf("validation error should identify Files to modify allowed-scope demotion, got %v", err))

})
var _ = ginkgo.It("build planner prompt hides submit shape when product brief has blockers", func() {
	brief := validProductWorkflowBrief()

	brief.Blockers = []string{"implementation-affecting claim lacks resolved evidence"}

	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), &brief)
	gomega.Expect(prompt).NotTo(gomega.ContainSubstring(`Required submit_feature_plan shape`), fmt.Sprintf("prompt must not advertise submit_feature_plan while product blockers are open:\n%s", prompt))
	gomega.Expect(prompt).NotTo(gomega.ContainSubstring(`"tool":"submit_feature_plan"`), fmt.Sprintf("prompt must not advertise submit_feature_plan while product blockers are open:\n%s", prompt))
	gomega.Expect(prompt).To(gomega.ContainSubstring("unresolved blockers"), fmt.Sprintf("prompt should direct blocker repair through planner question:\n%s", prompt))
	gomega.Expect(prompt).To(gomega.ContainSubstring("ask_planner_question"), fmt.Sprintf("prompt should direct blocker repair through planner question:\n%s", prompt))

})
var _ = ginkgo.It("build planner prompt product example uses non authoritative placeholders", func() {
	brief := validProductWorkflowBrief()

	brief.FilesToModify = []string{"internal/labels/labels.go"}
	brief.ContextEvidencePaths = []string{"internal/labels/labels.go"}
	brief.AcceptedADRPaths = []string{"docs/product-workflows/efforts/started/test/adrs/ADR-0001.md"}
	brief.RequiredBehavior = []string{"ShortLabel keeps product-approved text"}
	brief.Acceptance = []string{"ShortLabel is covered by TDD-1"}
	brief.Decisions = []string{"Use existing labels package"}

	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), &brief)

	for _, want := range []string{
		"Accepted ADR paths:",
		"Required behavior:",
		"Acceptance:",
		"Accepted decisions:",
		"Source/context evidence paths:",
		"required_before: implementation",
		`required_before to the exact scalar string "implementation"`,
		"planned_tdd_snapshot.obligation_ids must exactly match test_first.obligation_refs",
		"use-only-product-grounded-source-or-required-tdd-test-paths",
		"use-only-product-grounded-optional-edit-paths",
		"The schema example path placeholders are not authority",
		"Repo grounding is required:",
		"Every path listed under Files to modify is required implementation scope",
		"context evidence only",
		"allowed_edit_paths is still edit scope",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("product prompt missing %q:\n%s", want, prompt))

	}
	gomega.Expect(strings.Contains(prompt, "internal/example/source.go")).To(gomega.BeFalse(), fmt.Sprintf("product prompt must not contain copyable fake source paths:\n%s", prompt))
	gomega.Expect(strings.Contains(prompt, `"path_test.go"`)).To(gomega.BeFalse(), fmt.Sprintf("product prompt must not contain copyable fake test artifact paths:\n%s", prompt))

	exampleStart := strings.Index(prompt, "Required submit_feature_plan shape:")
	gomega.Expect(exampleStart < 0).To(gomega.BeFalse(), fmt.Sprintf("product prompt missing submit_feature_plan shape:\n%s", prompt))

	schemaExample := prompt[exampleStart:]
	gomega.Expect(strings.Contains(schemaExample, "go test ./...")).To(gomega.BeFalse(), fmt.Sprintf("product prompt schema example must not contain language-specific test command placeholders:\n%s", prompt))

	for _, forbidden := range []string{
		`"snapshot_id":"snap-tdd-`,
		`"snapshot_id":"placeholder`,
		`"snapshot_digest":"sha256:..."`,
		`"snapshot_digest":"sha256:placeholder`,
	} {
		gomega.Expect(strings.Contains(schemaExample, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("product prompt schema example contains validator-rejected TDD snapshot placeholder %q:\n%s", forbidden, prompt))

	}
})
var _ = ginkgo.It("build planner prompt renders accepted coverage as read only context", func() {
	brief := validProductWorkflowBrief()
	brief.AcceptedCoverage = validAcceptedBehavioralCoverage()

	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), &brief)

	for _, want := range []string{
		"Accepted coverage contract: id=cov-1 event=evt-cov-1 digest=sha256:abc status=pass",
		"Accepted Coverage - Requirements:",
		"Accepted coverage authority: product_workflow",
		"REQ-1: When product behavior is requested, the system shall render it. (event-driven)",
		"Accepted Coverage - Behavior Surface Inventory:",
		"SURF-1: domain_logic [in_scope] - accepted product behavior is in scope",
		"Surface SURF-1: domain_logic [in_scope] - accepted product behavior is in scope obligation: TDD-1",
		"SURF-2: api_transport [not_required] - no transport boundary changes",
		"Accepted Coverage - Rules & Examples:",
		"RULE-1: Accepted product coverage renders structured behavior. [requirement: REQ-1]",
		"Accepted Coverage - Scenarios:",
		"BDD-1: Given product workflow accepted coverage exists / When the plan is rendered / Then product behavior is rendered (concrete data)",
		"Accepted Coverage - Invariants:",
		"INV-1: accepted coverage remains product-owned",
		"Accepted Coverage - Failure Modes:",
		"FM-1: planner authors accepted coverage -> validator rejects accepted_coverage in submit_feature_plan (RPN 72)",
		"Accepted Coverage - State Model:",
		"SM-1 states: brief_loaded, plan_validated, plan_stamped",
		"Accepted Coverage - Fuzz & Property Coverage:",
		"Fuzz FUZZ-1: accepted coverage JSON parser [covered] test: FuzzAcceptedCoverageParser",
		"Accepted Coverage - Security, Privacy & RAG Evals:",
		"Security SEC-1: planner-authored coverage authority attack=model includes accepted_coverage in submit_feature_plan expected=plan is rejected [tested] test: TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage",
		"Privacy PRIV-1: raw prompt context policy=do not persist raw model-only context retention=structured fields only [tested] redaction: redaction/accepted_coverage_context",
		"RAG RAG-1 [measured] metric: groundedness=0.97",
		"Accepted Coverage - Provider Contracts:",
		"Provider contract PACT-1: product-workflow -> feature-planner [tested] contract: contracts/product_feature_planner.json",
		"Accepted Coverage - Observability:",
		"Observability OBS-1: feature_planner.accepted_coverage_context at internal/workflows/featureplanner/orchestrate.go [tested] required field: contract_id",
		"Accepted Coverage - Performance Bounds:",
		"Performance PERF-1: accepted coverage prompt rendering <= bounded deterministic context [tested] measurement: benchmarks/accepted_coverage_prompt.txt",
		"Accepted Coverage - E2E Journeys:",
		"E2E E2E-1: product accepted coverage reaches feature planner prompt [scripted] script: internal/workflows/featureplanner/validate_test.go",
		"Accepted Coverage - Test Obligations:",
		"TDD-1 - accepted coverage is shown without planner authorship (go test ./internal/workflows/featureplanner) [contract]",
		"Accepted Coverage - Verification Gates:",
		"VERIFY-1: contract (go test ./internal/workflows/featureplanner)",
		"Do not include behavioral_coverage_draft or accepted_coverage in product-workflow-backed submissions",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("product prompt missing accepted coverage context %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("accepted coverage presentation uses shared section labels and rows", func() {
	coverage := validAcceptedBehavioralCoverage()

	sections := AcceptedCoveragePresentation(coverage)
	requirements := CoverageRowsBySection(sections, CoverageSectionRequirements)
	gomega.Expect(slices.Contains(requirements, "REQ-1: When product behavior is requested, the system shall render it. (event-driven)")).To(gomega.BeTrue(), fmt.Sprintf("requirements projection missing accepted fact: %+v", requirements))

	scenarios := CoverageRowsBySection(sections, CoverageSectionScenarios)
	gomega.Expect(slices.Contains(scenarios, "BDD-1: Given product workflow accepted coverage exists / When the plan is rendered / Then product behavior is rendered (concrete data)")).To(gomega.BeTrue(), fmt.Sprintf("scenario projection missing accepted fact: %+v", scenarios))

	surfaces := CoverageRowsBySection(sections, CoverageSectionSurfaceInventory)
	gomega.Expect(slices.Contains(surfaces, "SURF-1: domain_logic [in_scope] - accepted product behavior is in scope")).To(gomega.BeTrue(), fmt.Sprintf("surface projection missing accepted fact: %+v", surfaces))

	labels := make([]string, 0, len(sections))
	for _, section := range sections {
		labels = append(labels, section.Label)
	}
	for _, want := range []string{
		CoverageSectionRequirements,
		CoverageSectionSurfaceInventory,
		CoverageSectionScenarios,
		CoverageSectionSecurityPrivacyRAGEvals,
		CoverageSectionVerificationGates,
	} {
		gomega.Expect(slices.Contains(labels, want)).To(gomega.BeTrue(), fmt.Sprintf("projection missing shared section label %q in %+v", want, labels))

	}
})
var _ = ginkgo.It("feature plan coverage presentation uses shared draft and accepted rows", func() {
	plan := FeaturePlan{
		TopLevelScope: TopLevelScope{Objective: "render behavior"},
		BehavioralCoverageDraft: &BehavioralCoverageDraft{
			Requirements: []CoverageDraftRequirement{{
				ID:        "REQ-DRAFT",
				Statement: "When a planner drafts coverage, the plan tab shall render it.",
				Pattern:   "event-driven",
			}},
			ExampleRules: []CoverageDraftExampleRule{{
				ID:            "RULE-DRAFT",
				RequirementID: "REQ-DRAFT",
				Rule:          "Drafted examples render through the same formatter.",
				Examples: []CoverageDraftExample{{
					ID:           "EX-DRAFT",
					Description:  "draft example",
					ConcreteData: map[string]string{"fixture": "draft.json"},
				}},
			}},
			Scenarios: []CoverageDraftScenario{{
				ID:           "BDD-DRAFT",
				Given:        []string{"draft coverage exists"},
				When:         []string{"the plan tab renders"},
				Then:         []string{"draft behavior is visible"},
				ConcreteData: map[string]string{"fixture": "draft.json"},
			}},
		},
		AcceptedCoverage: validAcceptedBehavioralCoverage(),
	}

	sections := FeaturePlanCoveragePresentation(FeatureState{}, "", plan, RejectedPlanCandidate{})
	requirements := CoverageRowsBySection(sections, CoverageSectionRequirements)
	for _, want := range []string{
		"REQ-DRAFT: When a planner drafts coverage, the plan tab shall render it. (event-driven)",
		"REQ-1: When product behavior is requested, the system shall render it. (event-driven)",
	} {
		gomega.Expect(slices.Contains(requirements, want)).To(gomega.BeTrue(), fmt.Sprintf("requirements missing shared row %q in %+v", want, requirements))

	}

	rules := CoverageRowsBySection(sections, CoverageSectionRulesAndExamples)
	gomega.Expect(slices.Contains(rules, "Example EX-DRAFT: draft example (concrete data)")).To(gomega.BeTrue(), fmt.Sprintf("rules/examples missing draft example row: %+v", rules))

	scenarios := CoverageRowsBySection(sections, CoverageSectionScenarios)
	gomega.Expect(slices.Contains(scenarios, "BDD-DRAFT: Given draft coverage exists / When the plan tab renders / Then draft behavior is visible (concrete data)")).To(gomega.BeTrue(), fmt.Sprintf("scenarios missing draft scenario row: %+v", scenarios))

	surfaces := CoverageRowsBySection(sections, CoverageSectionSurfaceInventory)
	gomega.Expect(slices.Contains(surfaces, "SURF-1: domain_logic [in_scope] - accepted product behavior is in scope")).To(gomega.BeTrue(), fmt.Sprintf("accepted surface missing shared row: %+v", surfaces))

})
var _ = ginkgo.It("build planner prompt standalone uses language neutral placeholders", func() {
	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), nil)
	schemaStart := strings.Index(prompt, "Required submit_feature_plan shape:")
	gomega.Expect(schemaStart < 0).To(gomega.BeFalse(), fmt.Sprintf("standalone prompt missing schema section:\n%s", prompt))

	schemaEnd := strings.Index(prompt[schemaStart:], "Set tdd_contract.obligations")
	gomega.Expect(schemaEnd < 0).To(gomega.BeFalse(), fmt.Sprintf("standalone prompt missing post-schema contract text:\n%s", prompt))

	schemaExample := prompt[schemaStart : schemaStart+schemaEnd]

	for _, forbidden := range []string{"internal/example/source.go", "internal/example/source_test.go", "path_test.go", "go test ./...", "cmake --build", "ctest", "pytest", "make test"} {
		gomega.Expect(strings.Contains(schemaExample, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("standalone schema example must not contain language-specific placeholder %q:\n%s", forbidden, schemaExample))

	}

	for _, want := range []string{"project/source-file.ext", "project/test-file.ext", "project-specific-test-command", "SDD-ACCEPTANCE-1"} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("standalone prompt missing neutral placeholder %q:\n%s", want, prompt))

	}

	for _, want := range []string{
		`required_before to the scalar string "implementation"`,
		"planned_tdd_snapshot.obligation_ids must exactly match test_first.obligation_refs",
		"include every test_first.test_artifact_paths entry in required_edit_paths",
		"Define stable standalone acceptance IDs",
		"use those IDs in atom.sdd_refs",
		"every in-scope surface must map to concrete TDD obligations",
		"evidence refs may support but not replace those obligations",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("standalone prompt missing schema invariant %q:\n%s", want, prompt))

	}

	for _, forbidden := range []string{
		`"snapshot_id":"snap-tdd-`,
		`"snapshot_id":"placeholder`,
		`"snapshot_digest":"sha256:..."`,
		`"snapshot_digest":"sha256:placeholder`,
	} {
		gomega.Expect(strings.Contains(prompt, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("standalone prompt schema example contains validator-rejected TDD snapshot placeholder %q:\n%s", forbidden, prompt))

	}
})
var _ = ginkgo.It("build planner prompt includes previous planning blockers", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningRepair
	state.PlanningLoop.State = FeatureStatePlanningRepair
	state.Blockers = []string{"atom 0 missing sdd_refs"}

	prompt := BuildPlannerPrompt("repair the feature plan", state, nil)

	for _, want := range []string{
		"Previous planner blockers that must be repaired before resubmission",
		"atom 0 missing sdd_refs",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("repair prompt missing blocker context %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("build planner prompt advertises source satisfied no edit gate", func() {
	prompt := BuildPlannerPrompt(
		"Please first check whether the existing source already satisfies this. If it is already satisfied, report source-satisfied and do not edit.",
		NewState("session", "check feature", "", 3, time.Unix(1, 0).UTC()),
		nil,
	)

	for _, want := range []string{
		"Valid tools: submit_feature_plan, source_satisfied, or ask_planner_question.",
		`"tool":"source_satisfied"`,
		`"anchors":[`,
		"path names alone are not proof",
		"preserve that gate",
		"Do not rewrite that request into unconditional implementation work.",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt missing source-satisfied contract %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("build planner prompt advertises typed planner question authority", func() {
	prompt := BuildPlannerPrompt(
		"Implement scoped atom.",
		NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC()),
		nil,
	)

	for _, want := range []string{
		`"tool":"ask_planner_question"`,
		`"affected_fields":["requirements_scope","verification_command"]`,
		`do not include affected_paths for non-path authority`,
		`affected_paths must contain the exact repo-relative paths`,
		`authority_required="accepted_source"`,
		`affected_fields=["source_localization"]`,
		`do not label source-evidence or requirements questions as new_file_authorization`,
		"paths inside requested_decision are explanatory text only and do not grant authority",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt missing typed planner-question authority contract %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("build planner prompt with workspace context lists established candidate files", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "src", "memory.py"), "class Memory:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_memory.py"), "def test_memory():\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "pyproject.toml"), "[project]\nname = \"brain\"\n")

	prompt := BuildPlannerPromptWithWorkspaceContext(
		"Update the plan for persistent memory and multi-repo support.",
		NewState("session", "build llm brain", "", 3, time.Unix(1, 0).UTC()),
		nil,
		root,
	)

	for _, want := range []string{
		"Standalone Workspace Context:",
		"Existing candidate source/test/build files available for localization:",
		"src/memory.py",
		"tests/test_memory.py",
		"pyproject.toml",
		"Localize required_edit_paths/test_artifact_paths from these candidates",
		"propose exact new paths for operator approval instead of asking which existing files to edit",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt missing workspace context %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("build planner prompt with workspace context tells greenfield planner to propose paths", func() {
	root := ginkgo.GinkgoT().TempDir()

	prompt := BuildPlannerPromptWithWorkspaceContext(
		"Work out an MVP for an LLM brain.",
		NewState("session", "build llm brain", "", 3, time.Unix(1, 0).UTC()),
		nil,
		root,
	)

	for _, want := range []string{
		"Workspace appears greenfield",
		"propose exact new repo-relative paths in the plan",
		"do not ask the operator which existing source files should be edited",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt missing greenfield context %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("build planner prompt forbids standalone verification and signature invention", func() {
	prompt := BuildPlannerPrompt(
		"Implement a source-localized atom.",
		NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC()),
		nil,
	)

	for _, want := range []string{
		"Do not invent project build systems or verification commands.",
		"ask_planner_question for the verification command",
		"Command names mentioned in this instruction are prohibited examples, not supplied project commands",
		"Do not encode guessed function signatures or call shapes in atom prompts.",
		"require exact signature/source verification first",
		"block on signature mismatch rather than inventing arguments",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt missing standalone invention guard %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("build planner prompt carries accepted source authority constraints", func() {
	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.AuthorityDeltas = []PlanAuthorityDelta{{
		QuestionID:     "q-source",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedPaths:  []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		SourceFacts: []plancontract.SourceAuthorityFact{{
			Path:      "native/src/simulation/economy/fluid_network_system.h",
			Symbol:    "FluidNetworkSystem::find_connected_cistern_with_volume",
			Signature: "EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const",
		}},
		Actual:    "accepted source fact approved for fluid network helper usage",
		Revision:  1,
		CreatedAt: time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}

	prompt := BuildPlannerPrompt("continue after accepted-source answer", state, nil)

	for _, want := range []string{
		"Accepted source authority constraints:",
		"signature: EntityID FluidNetworkSystem::find_connected_cistern_with_volume",
		"Use only these accepted source facts for helper signatures and source-localized call shapes.",
		"Remove or generalize any helper/method call in atom prompts that is not explicitly covered here",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt missing accepted-source constraint %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("build implementor prompt carries accepted source authority constraints", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Use FluidNetworkSystem::find_connected_cistern_with_volume(network_id, water_volume), but do not invent deduction helpers."
	deltas := []PlanAuthorityDelta{{
		QuestionID:     "q-source",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedPaths:  []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		SourceFacts: []plancontract.SourceAuthorityFact{{
			Path:      "native/src/simulation/economy/fluid_network_system.h",
			Symbol:    "FluidNetworkSystem::find_connected_cistern_with_volume",
			Signature: "EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const",
		}},
		Actual:    "Do not invent deduction helpers; describe cistern stored-volume deduction generically against existing source evidence.",
		Revision:  1,
		CreatedAt: time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}
	contract := CompilePlanAuthorityContract(plan, deltas)
	atom := FeatureAtomState{
		FeatureAtomPlan: plan.Atoms[0],
		State:           AtomStateImplementing,
		LastRunID:       "atom-1-run-1",
	}

	prompt := BuildImplementorPromptFromContract(atom, plan, contract)

	for _, want := range []string{
		"Accepted source authority constraints:",
		"These constraints are authoritative over the atom prose.",
		"signature: EntityID FluidNetworkSystem::find_connected_cistern_with_volume",
		"Do not invent deduction helpers",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("implementor prompt missing accepted-source constraint %q:\n%s", want, prompt))

	}
	gomega.Expect(strings.Index(prompt, "Accepted source authority constraints:") > strings.Index(prompt, "Atom implementation brief:")).To(gomega.BeFalse(), fmt.Sprintf("accepted-source constraints must precede atom prose:\n%s", prompt))

})
var _ = ginkgo.It("format state summary shows typed answer format for accepted source question", func() {
	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionSummary:   "Provide accepted source facts",
		AuthorityRequired: "accepted_source",
		Status:            "open",
	}}

	summary := FormatStateSummary(state, ".idiom/feature-planner/session/state.json")

	for _, want := range []string{
		"question: Provide accepted source facts",
		`answer_format: JSON object with answer, answer_source_kind="accepted_source", evidence_refs, and source_facts [{path, symbol, signature}]`,
		"next: answer_planner_question",
	} {
		gomega.Expect(strings.Contains(summary, want)).To(gomega.BeTrue(), fmt.Sprintf("state summary missing typed-answer guidance %q:\n%s", want, summary))

	}
})
var _ = ginkgo.It("validate plan rejects prompt path outside declared scope", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Edit internal/labels/labels.go and wire native/src/simulation/machines/machine.cpp for recipe start."

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected prompt path scope rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("native/src/simulation/machines/machine.cpp"), fmt.Sprintf("expected prompt path scope rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("outside required_edit_paths"), fmt.Sprintf("expected prompt path scope rejection, got %v", err))

})
var _ = ginkgo.It("validate plan allows prompt path declared by verification command", func() {
	plan := validPlan()
	command := "cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"
	plan.Atoms[0].Prompt = "Verify with: " + command
	plan.Atoms[0].TestFirst.Commands = []string{command}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{command}
	{

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prompt command paths declared by test commands should pass: %v", err))
	}

})
var _ = ginkgo.It("validate plan rejects guessed helper call without signature evidence", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Call find_connected_cistern_with_volume(eid) before posting fallback water haul."

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected guessed helper-call rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("find_connected_cistern_with_volume(eid)"), fmt.Sprintf("expected guessed helper-call rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("source-verified signature"), fmt.Sprintf("expected guessed helper-call rejection, got %v", err))

	plan.Atoms[0].Prompt = "Current source evidence: exact signature is EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required). Call it only with verified arguments."
	{
		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("model-authored signature prompt must not self-authorize helper calls, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("find_connected_cistern_with_volume"), fmt.Sprintf("model-authored signature prompt must not self-authorize helper calls, got %v", err))
	}

})
var _ = ginkgo.It("validate plan rejects model authored source verified signatures after marker", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Source-verified signatures:\n- MachineSystem::try_consume_inputs(EntityID machine_id, const Recipe& recipe)\n- EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const\n\nCall find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required)."
	{

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("model-authored signature marker must not satisfy helper calls, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("find_connected_cistern_with_volume"), fmt.Sprintf("model-authored signature marker must not satisfy helper calls, got %v", err))
	}

})
var _ = ginkgo.It("validate plan does not let signature marker self authorize later calls", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Source-verified signatures:\n- EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const\n- Call missing_helper(eid) before posting fallback water haul.\n\nCall missing_helper(eid) before posting fallback water haul."

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("signature marker must not authorize later prose calls, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing_helper(eid)"), fmt.Sprintf("signature marker must not authorize later prose calls, got %v", err))

})
var _ = ginkgo.It("validate plan does not treat negated signature marker as authority", func() {
	for _, prompt := range []string{
		"No source-verified signature: missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"unverified signature: missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"not verified signature: missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"source-verified signature is not missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
		"Current source evidence: exact signature is missing_helper(eid).\nCall missing_helper(eid) before posting fallback water haul.",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = prompt

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("negated signature marker must not authorize helper calls, got %v for prompt %q", err, prompt))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing_helper(eid)"), fmt.Sprintf("negated signature marker must not authorize helper calls, got %v for prompt %q", err, prompt))

	}
})
var _ = ginkgo.It("prompt signature declarations do not self authorize language neutral suffixes", func() {
	for _, prompt := range []string{
		"Source-verified signatures:\n- func renderLabel(name string) string\n\nCall renderLabel(name).",
		"Source-verified signatures:\n- fn helper(x: i32) -> bool\n\nCall helper(x).",
		"Source-verified signatures:\n- helper(x: string): boolean\n\nCall helper(x).",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = prompt
		{

			err := validatePlan(plan, plancontract.PlanExecutionContract{})
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("model-authored language-neutral signature must not authorize call for prompt %q", prompt))
		}

	}
})
var _ = ginkgo.It("validate plan does not treat bare call bullet as signature declaration", func() {
	for _, bullet := range []string{
		"- missing_helper(eid)",
		"- missing_helper(eid) before fallback",
		"- missing_helper(eid): before fallback",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = "Source-verified signatures:\n- EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const\n" + bullet + "\n\nCall missing_helper(eid) before posting fallback water haul."

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("bare call-shaped bullet must not authorize helper calls, got %v for bullet %q", err, bullet))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing_helper(eid)"), fmt.Sprintf("bare call-shaped bullet must not authorize helper calls, got %v for bullet %q", err, bullet))

	}
})
var _ = ginkgo.It("validate plan allows field descriptor parentheticals in atom prompt", func() {
	for _, prompt := range []string{
		"Use the source-verified helper, declared const(in native/src/simulation/economy/fluid_network_system.h), and deduct stored water.",
		"Deduct the required water from the existing stored volume field(CisternComponent) without editing components.h.",
		"Deduct the required water volume(CisternComponent stored volume field, do not edit components.h) without naming a new helper.",
		"Deduct from stored_volume(assume cistern component has stored_volume field; verify in source) after selecting a source-verified cistern.",
		"If a cistern EntityID is returned(using source-verified validity semantics, e.g., INVALID_ENTITY if present), preserve fallback behavior on no match.",
		"Deduct the required water volume using source-verified storage fields or methods(verify in native/src/core/components.h and fluid network source).",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = prompt
		plan.Atoms[0].EvidencePaths = append(plan.Atoms[0].EvidencePaths, "native/src/simulation/economy/fluid_network_system.h")
		plan.Atoms[0].EvidencePaths = append(plan.Atoms[0].EvidencePaths, "native/src/core/components.h")
		{

			err := validatePlan(plan, plancontract.PlanExecutionContract{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("field descriptor parenthetical should not require helper signature evidence, got %v for prompt %q", err, prompt))
		}

	}
})
var _ = ginkgo.It("validate plan still rejects invented deduction helper", func() {
	for _, prompt := range []string{
		"Call deduct_cistern_volume(cistern_id, required_water_volume) after selecting the cistern.",
		"Call volume(cistern_id) after selecting the cistern.",
	} {
		plan := validPlan()
		plan.Atoms[0].Prompt = prompt

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("invented helper should still require signature evidence for prompt %q", prompt))

	}
})
var _ = ginkgo.It("validate plan rejects guessed helper call despite unrelated source evidence", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Current source evidence: labels.go contains render_label(). Then call find_connected_cistern_with_volume(eid) before posting fallback water haul."

	err := validatePlan(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unrelated source evidence to not authorize guessed helper call, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("find_connected_cistern_with_volume(eid)"), fmt.Sprintf("expected unrelated source evidence to not authorize guessed helper call, got %v", err))

})
var _ = ginkgo.It("product source fact requires accepted source authority", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "Call find_connected_cistern_with_volume(eid) before posting fallback water haul."
	brief := validProductWorkflowBrief()
	brief.SourceFacts = []SourceFactBrief{{
		Path:      "internal/labels/labels.go",
		Symbol:    "find_connected_cistern_with_volume",
		Signature: "EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const",
	}}

	err := errors.Join(validateAtomPromptVerifiedCalls("atom 0", plan.Atoms[0], newAtomPromptGrounding(plan.Atoms[0], &brief))...)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("source fact without accepted authority should not satisfy helper call, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("find_connected_cistern_with_volume(eid)"), fmt.Sprintf("source fact without accepted authority should not satisfy helper call, got %v", err))

	brief.SourceFacts[0].AuthorityRef = "accepted-source:q-1"
	{
		err := errors.Join(validateAtomPromptVerifiedCalls("atom 0", plan.Atoms[0], newAtomPromptGrounding(plan.Atoms[0], &brief))...)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("accepted source fact should satisfy helper call: %v", err))
	}

})
var _ = ginkgo.It("feature planner does not treat fails red as helper call", func() {
	gomega.Expect(plannerCallNeedsSignatureEvidence("fails", "red")).To(gomega.BeFalse(), fmt.Sprint("plain prose fails(red) must not require source signature evidence"))
	gomega.Expect(plannerCallNeedsSignatureEvidence("fail", "reason")).To(gomega.BeTrue(), fmt.Sprint("real fail(reason) helper call should still require source signature evidence"))

})
var _ = ginkgo.It("feature planner does not treat parenthetical examples as helper calls", func() {
	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "water", args: "e.g., recipe.requiresWater()"},
		{name: "job", args: "i.e., return true without scheduling haul"},
		{name: "ID", args: "e.g., from machine component"},
	} {
		gomega.Expect(plannerCallNeedsSignatureEvidence(tc.name, tc.args)).To(gomega.BeFalse(), fmt.Sprintf("%s(%s) prose must not require source signature evidence", tc.name, tc.args))

	}
	gomega.Expect(plannerCallNeedsSignatureEvidence("find_connected_cistern_with_volume", "network_id, volume_required")).To(gomega.BeTrue(), fmt.Sprint("real helper call with concrete args must still require source signature evidence"))
	gomega.Expect(plannerCallNeedsSignatureEvidence("cover:", "1")).To(gomega.BeFalse(), fmt.Sprint("list prose cover:(1) must not require source signature evidence"))

})
var _ = ginkgo.It("validate plan allows parenthetical examples in atom prompt", func() {
	plan := validPlan()
	plan.Atoms[0].Prompt = "When recipe needs water(e.g., recipe.requiresWater()), skip the haul job(i.e., return true without scheduling haul) using the machine ID(e.g., from machine component)."
	{

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parenthetical examples must not be treated as helper calls: %v", err))
	}

})
var _ = ginkgo.It("normalize plan prompt declarations promotes evidence only paths", func() {
	plan := validPlan()
	plan.Atoms[0].EvidencePaths = nil
	plan.Atoms[0].Prompt = "Evidence-only path (do not edit): native/src/core/components.h"

	normalized := NormalizePlanPromptDeclarations(plan)
	gomega.Expect(slices.Contains(normalized.Atoms[0].EvidencePaths, "native/src/core/components.h")).To(gomega.BeTrue(), fmt.Sprintf("evidence-only prompt path was not promoted: %+v", normalized.Atoms[0].EvidencePaths))

})
var _ = ginkgo.It("build planner prompt renders source fact anchors", func() {
	brief := validProductWorkflowBrief()
	brief.SourceFacts = []SourceFactBrief{{
		Path:         "internal/labels/labels.go",
		Symbol:       "ShortLabel",
		Anchor:       `func ShortLabel() string`,
		AuthorityRef: "accepted-source:q-1",
	}}

	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), &brief)
	gomega.Expect(strings.Contains(prompt, "anchor=func ShortLabel() string")).To(gomega.BeTrue(), fmt.Sprintf("planner prompt must render source fact anchors:\n%s", prompt))

})
var _ = ginkgo.It("repo paths mentioned in text ignores labels and shorthand", func() {
	text := "Write/Update MachineSystem using fluid_network_system.h/.cpp, fluid_network_system.h/cpp, native/src/simulation/economy/fluid_network_system.h/cpp, and native/src/core/components.h."

	got := repoPathsMentionedInText(text)
	gomega.Expect(got).NotTo(gomega.ContainElement("Write/Update"), fmt.Sprintf("fake or shorthand paths should be ignored, got %#v", got))
	gomega.Expect(got).NotTo(gomega.ContainElement("fluid_network_system.h/.cpp"), fmt.Sprintf("fake or shorthand paths should be ignored, got %#v", got))
	gomega.Expect(got).NotTo(gomega.ContainElement("fluid_network_system.h/cpp"), fmt.Sprintf("fake or shorthand paths should be ignored, got %#v", got))

	for _, want := range []string{
		"native/src/core/components.h",
		"native/src/simulation/economy/fluid_network_system.h",
		"native/src/simulation/economy/fluid_network_system.cpp",
	} {
		gomega.Expect(slices.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("real repo path should be retained: want %q in %#v", want, got))

	}
})
var _ = ginkgo.It("validate atom prompt path scope expands qualified header source shorthand", func() {
	atom := FeatureAtomPlan{
		Prompt: "Modify native/src/simulation/economy/fluid_network_system.h/cpp.",
		RequiredEditPaths: []string{
			"native/src/simulation/economy/fluid_network_system.h",
			"native/src/simulation/economy/fluid_network_system.cpp",
		},
	}
	{

		errs := validateAtomPromptPathScope("atom 0", atom, newAtomPromptGrounding(atom, nil))
		gomega.Expect(len(errs)).To(gomega.Equal(0), fmt.Sprintf("scoped shorthand should pass validation, got %v", errs))
	}

	atom.RequiredEditPaths = nil
	errs := validateAtomPromptPathScope("atom 0", atom, newAtomPromptGrounding(atom, nil))
	gomega.Expect(len(errs)).To(gomega.Equal(2), fmt.Sprintf("unscoped shorthand should require both concrete paths, got %v", errs))

	joined := fmt.Sprint(errs)
	for _, want := range []string{
		`native/src/simulation/economy/fluid_network_system.h`,
		`native/src/simulation/economy/fluid_network_system.cpp`,
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("unscoped shorthand error missing %q: %s", want, joined))

	}
})
var _ = ginkgo.It("command referenced workspace paths uses command CWD", func() {
	command := "cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"
	got := commandReferencedWorkspacePaths(command)

	for _, want := range []string{"native/tests", "bin/colony_tests", "native/tests/bin/colony_tests"} {
		gomega.Expect(slices.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("command paths missing %q: %#v", want, got))

	}
})
var _ = ginkgo.It("validate standalone workspace uses accepted source grounding for FET33 c shape", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void MachineSystem::tick_normal(float dt) {}\n")
	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/economy/fluid_network_system.h"), "EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const;\n")
	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/economy/fluid_network_system.cpp"), "EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const { return INVALID_ENTITY; }\n")
	mkdirAndWriteFile(filepath.Join(root, "native/src/core/components.h"), "struct CisternComponent { int32_t stored; };\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/SConstruct"), "DefaultEnvironment()\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"brewery fluid cascade\") { CHECK(true); }\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\nexit 0\n")
	{
		err := os.Chmod(filepath.Join(root, "native/tests/bin/colony_tests"), 0755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("chmod colony_tests: %v", err))
	}

	command := "cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"
	plan := validPlan()
	plan.TopLevelScope.SuccessCriteria = []string{"FET 3.3c consumes water from a connected cistern before water hauling."}
	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].EvidencePaths = []string{
		"native/src/simulation/economy/fluid_network_system.h",
		"native/src/simulation/economy/fluid_network_system.cpp",
		"native/src/core/components.h",
	}
	plan.Atoms[0].TestFirst.Commands = []string{command}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{command}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].Prompt = "Write/Update FET 3.3c. Use fluid_network_system.h/.cpp context and native/src/core/components.h. Call find_connected_cistern_with_volume(network_id, volume_required) only after source verification."

	contract := StampPlanAuthorityContract(&plan, []PlanAuthorityDelta{{
		QuestionID:     "q-source",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		Expected:       "Need source-localized helper signature.",
		Actual:         "Accepted source: EntityID find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const in native/src/simulation/economy/fluid_network_system.h.",
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}})
	{

		err := validatePlan(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("base plan should require source signature before accepted-source grounding, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("find_connected_cistern_with_volume(network_id, volume_required)"), fmt.Sprintf("base plan should require source signature before accepted-source grounding, got %v", err))
	}
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root, Contract: contract})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("accepted source grounding should satisfy standalone workspace context: %v", err))
	}

})
var _ = ginkgo.It("parse planner output coerces singleton required before array", func() {
	raw := strings.Replace(mustPlanJSON(validPlan()), `"required_before":"implementation"`, `"required_before":["implementation"]`, 1)

	parsed, err := ParsePlannerOutput(raw)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParsePlannerOutput returned error: %v", err))
	{

		got := parsed.Plan.TDDContract.Obligations[0].RequiredBefore
		gomega.Expect(got).To(gomega.Equal("implementation"), fmt.Sprintf("RequiredBefore = %q, want implementation", got))
	}

})
var _ = ginkgo.It("parse planner output coerces scalar acceptance string", func() {
	raw := strings.Replace(
		mustPlanJSON(validPlan()),
		`"refs":["REQ-1"]`,
		`"refs":["REQ-1"],"acceptance":"REQ-1 metric: works.; REQ-1 testability: covered."`,
		1,
	)

	parsed, err := ParsePlannerOutput(raw)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParsePlannerOutput returned error: %v", err))

	want := []string{"REQ-1 metric: works.", "REQ-1 testability: covered."}
	{
		got := parsed.Plan.SDDContract.Acceptance
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("Acceptance = %#v, want %#v", got, want))
	}

})
var _ = ginkgo.It("parse planner output accepts question alias", func() {
	parsed, err := ParsePlannerOutput(`{"tool":"ask_planner_question","question":"Which source paths should be used for the tooltip?"}`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParsePlannerOutput returned error: %v", err))
	gomega.Expect(parsed.Kind).To(gomega.Equal(featureplannercontract.ToolAskPlannerQuestion), fmt.Sprintf("kind = %q, want ask_planner_question", parsed.Kind))
	gomega.Expect(parsed.Question.QuestionSummary).To(gomega.Equal("Which source paths should be used for the tooltip?"), fmt.Sprintf("question summary not populated from alias: %+v", parsed.Question))

})
var _ = ginkgo.It("validate planner question rejects path authority without affected paths", func() {
	question := PlannerQuestion{
		ID:                "q-new-file",
		QuestionSummary:   "Authorize new helper",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Missing helper requires operator approval.",
		RequestedDecision: "Approve creating pkg/new_helper.go.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		Status:            "open",
		AffectedFields:    []string{"new_file_authorization"},
	}

	err := ValidatePlannerQuestion(question)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected affected_paths validation error, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("path-level authority requires affected_paths"), fmt.Sprintf("expected affected_paths validation error, got %v", err))

})
var _ = ginkgo.It("apply planner output demotes unanchored path authority question", func() {
	state := NewState("session", "work out an MVP", "", 3, time.Unix(1, 0).UTC())
	parsed, err := ParsePlannerOutput(`{
		"tool": "ask_planner_question",
		"question_summary": "Define minimal MVP requirements",
		"blocking_claim": "A safe plan needs explicit runtime behavior before file creation can be proposed.",
		"requested_decision": "Choose the minimal runnable behavior and verification command for the MVP.",
		"question_kind": "external_answer_required",
		"authority_required": "operator_approval",
		"required_before": "plan_approval",
		"affected_fields": ["new_file_authorization", "verification_command"]
	}`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParsePlannerOutput returned error: %v", err))

	events := ApplyPlannerOutput(&state, parsed, time.Unix(2, 0).UTC())
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", state.Status, state.Blockers))
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("expected planner_question_opened event, got %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected planner_question_opened event, got %+v", events))
	gomega.Expect(len(state.Blockers)).To(gomega.Equal(0), fmt.Sprintf("unexpected blockers after demotion: %+v", state.Blockers))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", state.OpenQuestions))

	question := state.OpenQuestions[0]
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeFalse(), fmt.Sprintf("unanchored path authority should not survive intake: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "requirements_scope")).To(gomega.BeTrue(), fmt.Sprintf("question should remain a requirements question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "verification_command")).To(gomega.BeTrue(), fmt.Sprintf("question should remain a requirements question: %+v", question))

})
var _ = ginkgo.It("validate planner question source localization is not path authority", func() {
	sourceQuestion := PlannerQuestion{
		ID:                "q-source",
		QuestionSummary:   "Provide source-localized scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Need exact source evidence.",
		RequestedDecision: "Provide exact existing source/test paths.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
		AffectedFields:    []string{"source_localization"},
	}
	{

		err := ValidatePlannerQuestion(sourceQuestion)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("source localization should not require affected_paths: %v", err))
	}

	sourceQuestion.AffectedFields = []string{"new_file_authorization"}
	{
		err := ValidatePlannerQuestion(sourceQuestion)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("new-file authorization should require affected_paths, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("path-level authority requires affected_paths"), fmt.Sprintf("new-file authorization should require affected_paths, got %v", err))
	}

})
var _ = ginkgo.It("validate planner question rejects operator owned source file discovery", func() {
	question := PlannerQuestion{
		ID:                "q-source-paths",
		QuestionSummary:   "What existing source files or test files should be edited?",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "No existing source or test paths are available in the workspace context.",
		RequestedDecision: "Please provide the repo-relative paths of existing source files and test files that should be modified.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		Status:            "open",
		AffectedFields:    []string{"requirements_scope", "source_localization"},
	}

	err := ValidatePlannerQuestion(question)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected source discovery delegation rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("must not ask the operator to discover existing source or test paths"), fmt.Sprintf("expected source discovery delegation rejection, got %v", err))

})
var _ = ginkgo.It("apply planner output rejects source path discovery question before chat", func() {
	state := NewState("session", "build llm brain", "", 3, time.Unix(1, 0).UTC())
	parsed, err := ParsePlannerOutput(`{
		"tool": "ask_planner_question",
		"question_summary": "What existing source files or test files should be edited for persistent memory?",
		"blocking_claim": "No existing source or test paths are available in the workspace context.",
		"requested_decision": "Please provide the repo-relative paths of existing source files and test files that should be modified.",
		"question_kind": "external_answer_required",
		"authority_required": "operator_approval",
		"required_before": "plan_approval",
		"affected_fields": ["requirements_scope", "source_localization"]
	}`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParsePlannerOutput returned error: %v", err))

	events := ApplyPlannerOutput(&state, parsed, time.Unix(2, 0).UTC())
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair", state.Status))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(0), fmt.Sprintf("invalid source discovery question should not open chat question: %+v", state.OpenQuestions))
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("expected planner_question_rejected event, got %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.planner_question_rejected"), fmt.Sprintf("expected planner_question_rejected event, got %+v", events))
	gomega.Expect(strings.Contains(strings.Join(state.Blockers, "\n"), "must not ask the operator to discover existing source or test paths")).To(gomega.BeTrue(), fmt.Sprintf("state blockers missing source-discovery rejection: %+v", state.Blockers))

})
var _ = ginkgo.It("parse planner output accepts source satisfied closeout", func() {
	raw := `{"tool":"source_satisfied","summary":"tooltip already shows contributors","reason":"current helper contains the score breakdown","evidence_paths":["scripts/ui/ui_helpers.gd"],"test_paths":["tests/e2e/suites/ui_overhaul_suite.gd"],"anchors":[{"path":"scripts/ui/ui_helpers.gd","text":"Quality contributors:"}]}`

	parsed, err := ParsePlannerOutput(raw)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParsePlannerOutput returned error: %v", err))
	gomega.Expect(parsed.Kind).To(gomega.Equal(featureplannercontract.ToolSourceSatisfied), fmt.Sprintf("kind = %q, want source_satisfied", parsed.Kind))
	gomega.Expect(parsed.SourceSatisfied.Summary).To(gomega.Equal("tooltip already shows contributors"), fmt.Sprintf("unexpected source-satisfied payload: %+v", parsed.SourceSatisfied))
	gomega.Expect(parsed.SourceSatisfied.EvidencePaths).To(gomega.Equal([]string{"scripts/ui/ui_helpers.gd"}), fmt.Sprintf("unexpected source-satisfied payload: %+v", parsed.SourceSatisfied))

})
var _ = ginkgo.It("run source satisfied closeout skips atom dispatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "scripts/ui/ui_helpers.gd"), "func build_item_detail_tooltip():\n\tprint(\"Quality contributors:\")\n")
	mkdirAndWriteFile(filepath.Join(root, "tests/e2e/suites/ui_overhaul_suite.gd"), "extends RefCounted\n")

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("source-satisfied planner closeout must not dispatch an atom"))

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["scripts/ui/ui_helpers.gd"],"test_paths":["tests/e2e/suites/ui_overhaul_suite.gd"],"anchors":[{"path":"scripts/ui/ui_helpers.gd","text":"Quality contributors:"},{"path":"tests/e2e/suites/ui_overhaul_suite.gd","text":"extends RefCounted"}]}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateSourceSatisfied), fmt.Sprintf("state.Status = %q, want source-satisfied; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.State.Atoms).To(gomega.HaveLen(0), fmt.Sprintf("source-satisfied closeout should not create dispatch state: atoms=%+v active=%+v", result.State.Atoms, result.State.ActiveRuns))
	gomega.Expect(result.State.ActiveRuns).To(gomega.HaveLen(0), fmt.Sprintf("source-satisfied closeout should not create dispatch state: atoms=%+v active=%+v", result.State.Atoms, result.State.ActiveRuns))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("events = %+v, want planner source-satisfied event", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal(featureplannercontract.EventName(featureplannercontract.EventPlannerSourceSatisfied)), fmt.Sprintf("events = %+v, want planner source-satisfied event", result.Events))

})
var _ = ginkgo.It("run rejects source satisfied missing workspace evidence", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("invalid source-satisfied closeout must not dispatch an atom"))

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["missing.gd"],"anchors":[{"path":"missing.gd","text":"Quality contributors:"}]}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair", result.State.Status))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "missing.gd")).To(gomega.BeTrue(), fmt.Sprintf("blocker should name missing evidence path: %+v", result.State.Blockers))

})
var _ = ginkgo.It("run rejects source satisfied without anchors", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "scripts/ui/ui_helpers.gd"), "Quality contributors:\n")

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("invalid source-satisfied closeout must not dispatch an atom"))

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["scripts/ui/ui_helpers.gd"]}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair", result.State.Status))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "source_satisfied missing anchors")).To(gomega.BeTrue(), fmt.Sprintf("blocker should require anchors: %+v", result.State.Blockers))

})
var _ = ginkgo.It("run rejects source satisfied missing anchor text", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "scripts/ui/ui_helpers.gd"), "func build_item_detail_tooltip(): pass\n")

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "First check whether current source already satisfies this and do not edit if so.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("invalid source-satisfied closeout must not dispatch an atom"))

		return `{"tool":"source_satisfied","summary":"already present","evidence_paths":["scripts/ui/ui_helpers.gd"],"anchors":[{"path":"scripts/ui/ui_helpers.gd","text":"Quality contributors:"}]}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair", result.State.Status))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "anchor text not found")).To(gomega.BeTrue(), fmt.Sprintf("blocker should require present anchor text: %+v", result.State.Blockers))

})
var _ = ginkgo.It("run preserves source satisfied gate in dispatched atom prompt", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(filepath.Join(root, "scripts/ui/ui_helpers.gd"), "Quality contributors:\n")

	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"scripts/ui/ui_helpers.gd"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"scripts/ui/ui_helpers.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"scripts/ui/ui_helpers.gd"}
	plan.Atoms[0].Prompt = "Implement score breakdown logic in the item tooltip helper."

	state := NewState("session", "check first", "", 3, time.Unix(1, 0).UTC())

	var implementorPrompt string

	result, err := Run(state, RunOptions{
		Prompt:                "Please first check whether the existing areas already satisfy the atom. If it is already satisfied, report source-satisfied and do not edit.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerImplementor) {
			implementorPrompt = prompt
			return testPlanExecuteSourceSatisfiedJSONForPrompt(prompt), nil
		}

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers))

	for _, want := range []string{"Before editing, verify", "Decision: source-satisfied", "do not edit"} {
		gomega.Expect(strings.Contains(implementorPrompt, want)).To(gomega.BeTrue(), fmt.Sprintf("implementor prompt missing preserved gate %q:\n%s", want, implementorPrompt))

	}
	gomega.Expect(strings.Contains(result.State.Atoms[0].Prompt, "Decision: source-satisfied")).To(gomega.BeTrue(), fmt.Sprintf("persisted atom prompt did not preserve source-satisfied gate: %q", result.State.Atoms[0].Prompt))

})
var _ = ginkgo.It("validate plan product workflow context rejects ungrounded test artifacts", func() {
	plan := validProductContextPlan()

	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"path_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"path_test.go"}

	brief := validProductWorkflowBrief()

	err := ValidatePlanProductWorkflowContext(plan, brief, "")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected ungrounded test artifact validation error"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("test artifact path"), fmt.Sprintf("validation error should identify ungrounded test artifact path, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("path_test.go"), fmt.Sprintf("validation error should identify ungrounded test artifact path, got %v", err))

})
var _ = ginkgo.It("validate plan standalone workspace context rejects missing invented paths", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"src/ui/economy_masterwork_tooltip.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}

	mkdirAndWriteFile(filepath.Join(root, "tests/e2e/suites/ui_overhaul_suite.gd"), "extends RefCounted\n")

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot: root,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected missing standalone path validation error"))

	for _, want := range []string{
		"src/ui/economy_masterwork_tooltip.gd",
		"missing from the workspace",
		"not authorized as a new file",
	} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("validate plan standalone workspace context allows explicit new file and test creation", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	contract := CompilePlanAuthorityContract(plan, []PlanAuthorityDelta{{
		QuestionID:     "q-new-files",
		AuthorityKind:  "new_file_authorization",
		EvidenceRefs:   []string{"operator:q-new-files"},
		AffectedPaths:  []string{"pkg/new_helper.go", "pkg/new_helper_test.go"},
		AffectedFields: []string{"new_file_authorization"},
		Expected:       "new files require explicit typed authority",
		Actual:         "operator authorized the new helper and test files",
		Revision:       1,
		CreatedAt:      time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}})
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
			WorkspaceRoot: root,
			Contract:      contract,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("typed new-file/test authority should be allowed, got %v", err))
	}

})
var _ = ginkgo.It("compile plan authority contract does not authorize new file from path only delta", func() {
	plan := validPlan()
	contract := CompilePlanAuthorityContract(plan, []PlanAuthorityDelta{{
		QuestionID:    "q-new-files",
		AuthorityKind: "operator_approval",
		EvidenceRefs:  []string{"operator:q-new-files"},
		AffectedPaths: []string{"pkg/new_helper.go"},
		Expected:      "new files require explicit typed authority",
		Actual:        "operator approved text mentioning pkg/new_helper.go",
		Revision:      1,
		CreatedAt:     time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}})
	gomega.Expect(len(contract.AuthorizedNewFiles)).To(gomega.Equal(0), fmt.Sprintf("path-only authority delta must not authorize new files: %+v", contract.AuthorizedNewFiles))

})
var _ = ginkgo.It("validate plan standalone workspace context does not existence gate TDD metadata", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")

	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/labels/labels.go"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"internal/workflows/productworkflow/coverage_gate_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"internal/workflows/productworkflow/coverage_gate_test.go"}
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
			WorkspaceRoot: root,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("standalone validation should not require TDD artifact metadata to exist in target workspace: %v", err))
	}

})
var _ = ginkgo.It("run rejects standalone missing workspace path before dispatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"project/scripts/economy/score_breakdown.gd"}
	plan.Atoms[0].AllowedEditPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/e2e/suites/ui_overhaul_suite.gd"}

	mkdirAndWriteFile(filepath.Join(root, "tests/e2e/suites/ui_overhaul_suite.gd"), "extends RefCounted\n")

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Add score breakdown to the existing tooltip.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone workspace validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(len(result.State.Atoms)).To(gomega.Equal(0), fmt.Sprintf("invalid standalone plan should not compile atoms: %+v", result.State.Atoms))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("expected standalone context blocked event, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected standalone context blocked event, got %+v", result.Events))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "project/scripts/economy/score_breakdown.gd")).To(gomega.BeTrue(), fmt.Sprintf("blocker should name invented path: %+v", result.State.Blockers))

})
var _ = ginkgo.It("run opens localization question for prepared atom selection with invented paths", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Summary = "Implement Atom 1.1: Add CollapseEvent handling to ColonyService with TDD and SDD contracts."
	plan.TopLevelScope.Objective = "Implement CollapseEvent handling in ColonyService."
	plan.Atoms[0].Title = "Implement CollapseEvent handling in ColonyService"
	plan.Atoms[0].RequiredEditPaths = []string{
		"src/services/colony_service.py",
		"tests/services/test_colony_service.py",
	}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/services/test_colony_service.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/services/test_colony_service.py"}

	state := NewState("session", "pick a prepared atom", "", 3, time.Unix(1, 0).UTC())
	prompt := "As product owner: pick one genuinely unsatisfied, bounded Colony atom from the prepared next-atom candidates. Before dispatching implementation, verify current source and planning state. Favor small source-backed atoms with explicit tests."

	result, err := Run(state, RunOptions{
		Prompt:                prompt,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone workspace validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(2), fmt.Sprintf("expected rejected-plan then localization question events, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected rejected-plan then localization question events, got %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected rejected-plan then localization question events, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one localization question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("accepted_source"), fmt.Sprintf("localization question must not authorize invented files: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeFalse(), fmt.Sprintf("localization question must not authorize invented files: %+v", question))
	gomega.Expect(question.UserFacing).To(gomega.BeTrue(), fmt.Sprintf("explicit source-localization question should be marked user-facing: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "source_localization")).To(gomega.BeTrue(), fmt.Sprintf("localization question missing source_localization field: %+v", question))
	gomega.Expect(question.RequestedDecision).To(gomega.ContainSubstring("source-verified atom candidate"), fmt.Sprintf("question should request real source localization and preserve rejected paths diagnostically: %+v", question))
	gomega.Expect(question.RequestedDecision).To(gomega.ContainSubstring("src/services/colony_service.py"), fmt.Sprintf("question should request real source localization and preserve rejected paths diagnostically: %+v", question))

})
var _ = ginkgo.It("run opens new file approval question for standalone project plan in partial workspace", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/brain\n")
	mkdirAndWriteFile(filepath.Join(root, "README.md"), "# Brain\n")

	plan := validGreenfieldBrainPlanForTest()

	state := NewState("session", `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`, "", 2, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone workspace validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(2), fmt.Sprintf("expected rejected-plan then new-file approval question events, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected rejected-plan then new-file approval question events, got %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected rejected-plan then new-file approval question events, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("question authority = %q, want operator_approval: %+v", question.AuthorityRequired, question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeTrue(), fmt.Sprintf("question affected fields missing new_file_authorization: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "verification_command")).To(gomega.BeFalse(), fmt.Sprintf("new-file approval should not ask the operator to provide model-synthesized verification commands: %+v", question))

	for _, want := range []string{"src/brain.py", "tests/test_brain.py"} {
		gomega.Expect(containsString(question.AffectedPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("question affected paths missing %q: %+v", want, question))

	}
	gomega.Expect(strings.Contains(strings.ToLower(question.RequestedDecision), "what existing source files")).To(gomega.BeFalse(), fmt.Sprintf("question must not delegate source discovery to the operator: %+v", question))

})
var _ = ginkgo.It("run generic pick one MVP does not open source localization question", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/brain\n")
	mkdirAndWriteFile(filepath.Join(root, "README.md"), "# Brain\n")

	plan := validGreenfieldBrainPlanForTest()
	prompt := "Pick one MVP direction for this app."

	state := NewState("session", prompt, "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone workspace validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("generic MVP selection should request operator approval, not source localization: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeTrue(), fmt.Sprintf("generic MVP selection should request operator approval, not source localization: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "source_localization")).To(gomega.BeFalse(), fmt.Sprintf("generic MVP selection should request operator approval, not source localization: %+v", question))

})
var _ = ginkgo.It("run retries model repairable standalone validation and accepts corrected plan", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")
	mkdirAndWriteFile(filepath.Join(root, "pyproject.toml"), "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")

	rejectedPlan := runningExampleExistingPathPlanForTest()
	correctedPlan := runningExampleExistingPathPlanForTest()
	correctedPlan.BehavioralCoverageDraft = validBehavioralCoverageDraft()
	correctedPlan.Atoms[0].Prompt = "Add memory storage and retrieval behavior in the existing query handler and cover creation, retrieval, and empty-content behavior."

	state := NewState("session", `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`, "", 3, time.Unix(1, 0).UTC())
	var plannerPrompts []string
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			plannerPrompts = append(plannerPrompts, prompt)
			if len(plannerPrompts) == 1 {
				return mustPlanJSON(rejectedPlan), nil
			}
			return mustPlanJSON(correctedPlan), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPlanWithSourceSanity(correctedPlan, prompt), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(len(plannerPrompts)).To(gomega.Equal(2), fmt.Sprintf("planner calls = %d, want rejected plan + corrected plan", len(plannerPrompts)))
	gomega.Expect(plannerPrompts[1]).To(gomega.ContainSubstring("Previous planner blockers that must be repaired before resubmission"), fmt.Sprintf("retry prompt missing rejected-plan blocker context:\n%s", plannerPrompts[1]))
	gomega.Expect(plannerPrompts[1]).To(gomega.ContainSubstring("behavioral_coverage_draft"), fmt.Sprintf("retry prompt missing rejected-plan blocker context:\n%s", plannerPrompts[1]))
	gomega.Expect(len(result.State.RejectedPlanCandidates)).To(gomega.Equal(1), fmt.Sprintf("expected one rejected candidate, got %+v", result.State.RejectedPlanCandidates))
	gomega.Expect(len(result.State.Blockers)).To(gomega.Equal(0), fmt.Sprintf("corrected plan should clear stale planner blockers: %+v", result.State.Blockers))

	var eventTypes []string
	for _, event := range result.Events {
		eventTypes = append(eventTypes, event.Type)
	}
	for _, want := range []string{
		"idiom.feature_planner.standalone_context_blocked",
		"idiom.feature_planner.standalone_context_repair_retry",
		"idiom.feature_planner.plan_accepted",
	} {
		gomega.Expect(slices.Contains(eventTypes, want)).To(gomega.BeTrue(), fmt.Sprintf("missing event %q in %+v", want, eventTypes))

	}
})
var _ = ginkgo.It("classify standalone validation repair mixed coverage and pytest opens operator question", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")

	plan := runningExampleExistingPathPlanForTest()

	state := NewState("session", `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`, "", 2, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("verification authority must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(2), fmt.Sprintf("expected rejected-plan then verification question events, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected rejected-plan then verification question events, got %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected rejected-plan then verification question events, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("unexpected verification command question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "verification_command")).To(gomega.BeTrue(), fmt.Sprintf("unexpected verification command question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeFalse(), fmt.Sprintf("unexpected verification command question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "source_localization")).To(gomega.BeFalse(), fmt.Sprintf("unexpected verification command question: %+v", question))
	gomega.Expect(strings.Contains(question.RequestedDecision, "pytest tests/test_query_handler.py")).To(gomega.BeTrue(), fmt.Sprintf("verification question should preserve rejected command candidate: %+v", question))

})
var _ = ginkgo.It("classify standalone validation repair mixed coverage and source signature retries for generic request", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")
	mkdirAndWriteFile(filepath.Join(root, "pyproject.toml"), "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")

	plan := runningExampleExistingPathPlanForTest()
	plan.Atoms[0].Prompt = "Call create_memory(content) from llm_brain/query_handler.py before dispatch."

	state := NewState("session", `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`, "", 2, time.Unix(1, 0).UTC())
	plannerCalls := 0
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 2,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("source-signature repair must block before dispatch"))

		plannerCalls++

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningStuck), fmt.Sprintf("state.Status = %q, want planning-stuck; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(plannerCalls).To(gomega.Equal(2), fmt.Sprintf("planner calls = %d, want retry until budget", plannerCalls))

	var blockedEvents int
	for _, event := range result.Events {
		if event.Type == "idiom.feature_planner.standalone_context_blocked" {
			blockedEvents++
		}
	}
	gomega.Expect(blockedEvents).To(gomega.Equal(2), fmt.Sprintf("expected two rejected plans then planning_stuck, got %+v", result.Events))
	gomega.Expect(result.Events[len(result.Events)-1].Type).To(gomega.Equal("idiom.feature_planner.planning_stuck"), fmt.Sprintf("expected two rejected plans then planning_stuck, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(0), fmt.Sprintf("generic source-signature repair must not ask the operator for accepted source facts: %+v", result.State.OpenQuestions))

})
var _ = ginkgo.It("run does not retry partially classified standalone validation", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")
	mkdirAndWriteFile(filepath.Join(root, "pyproject.toml"), "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")

	plan := runningExampleExistingPathPlanForTest()
	plan.BehavioralCoverageDraft = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"../outside_test.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"../outside_test.py"}

	state := NewState("session", "Plan an LLM brain MVP.", "", 3, time.Unix(1, 0).UTC())
	plannerCalls := 0
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("partially classified standalone validation must block before dispatch"))

		plannerCalls++
		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(plannerCalls).To(gomega.Equal(1), fmt.Sprintf("planner calls = %d, want no retry for partially classified validation", plannerCalls))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("expected only standalone context rejection, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected only standalone context rejection, got %+v", result.Events))

})
var _ = ginkgo.It("run preserves retry events when next planner output is malformed", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")
	mkdirAndWriteFile(filepath.Join(root, "pyproject.toml"), "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")

	rejectedPlan := runningExampleExistingPathPlanForTest()
	state := NewState("session", "Plan an LLM brain MVP.", "", 3, time.Unix(1, 0).UTC())
	plannerCalls := 0
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		plannerCalls++
		if plannerCalls == 1 {
			return mustPlanJSON(rejectedPlan), nil
		}
		return "not json", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateMalformedOutput), fmt.Sprintf("state.Status = %q, want malformed-output", result.State.Status))

	wantEvents := []string{
		"idiom.feature_planner.standalone_context_blocked",
		"idiom.feature_planner.standalone_context_repair_retry",
		"idiom.feature_planner.malformed_structured_output",
	}
	{
		got := featurePlannerEventTypesForTest(result.Events)
		gomega.Expect(slices.Equal(got, wantEvents)).To(gomega.BeTrue(), fmt.Sprintf("event trail = %#v, want %#v", got, wantEvents))
	}

})
var _ = ginkgo.It("run preserves retry events when next plan violates locked contract", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")
	mkdirAndWriteFile(filepath.Join(root, "pyproject.toml"), "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")

	lockedPlan := runningExampleExistingPathPlanForTest()
	rejectedPlan := runningExampleExistingPathPlanForTest()
	contractRejectedPlan := runningExampleExistingPathPlanForTest()
	contractRejectedPlan.Atoms[0].RequiredEditPaths = []string{"llm_brain/renamed_handler.py"}

	state := NewState("session", "Repair the locked labels plan.", "", 3, time.Unix(1, 0).UTC())
	state.Plan = lockedPlan
	plannerCalls := 0
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		plannerCalls++
		if plannerCalls == 1 {
			return mustPlanJSON(rejectedPlan), nil
		}
		return mustPlanJSON(contractRejectedPlan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair; blockers=%+v", result.State.Status, result.State.Blockers))

	wantEvents := []string{
		"idiom.feature_planner.standalone_context_blocked",
		"idiom.feature_planner.standalone_context_repair_retry",
		"idiom.feature_planner.plan_contract_blocked",
	}
	{
		got := featurePlannerEventTypesForTest(result.Events)
		gomega.Expect(slices.Equal(got, wantEvents)).To(gomega.BeTrue(), fmt.Sprintf("event trail = %#v, want %#v", got, wantEvents))
	}

})
var _ = ginkgo.It("classify standalone validation repair prefers verification command for generic request", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")

	plan := runningExampleExistingPathPlanForTest()
	plan.Atoms[0].Prompt = "Call create_memory(content) from llm_brain/query_handler.py before dispatch."

	state := NewState("session", `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`, "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("verification authority must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("generic mixed repair should ask for operator verification command first: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "verification_command")).To(gomega.BeTrue(), fmt.Sprintf("generic mixed repair should ask for operator verification command first: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "source_localization")).To(gomega.BeFalse(), fmt.Sprintf("generic mixed repair should ask for operator verification command first: %+v", question))

})
var _ = ginkgo.It("standalone unverified helper call opens accepted source question", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n")

	plan := validPlan()
	plan.Atoms[0].Prompt = "Use accepted source localization: call find_connected_cistern_with_volume(network_id, volume_required) from internal/labels/labels.go before dispatch."
	plan.Atoms[0].EvidencePaths = []string{"internal/labels/labels.go"}

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                "Build the atom from accepted source localization and source verification. If helper signatures are not source-verified, ask for accepted source facts before dispatch.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone source-signature validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(2), fmt.Sprintf("expected rejected-plan then source question events, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected rejected-plan then source question events, got %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected rejected-plan then source question events, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("accepted_source"), fmt.Sprintf("signature question must request accepted source localization: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "source_localization")).To(gomega.BeTrue(), fmt.Sprintf("signature question must request accepted source localization: %+v", question))
	gomega.Expect(question.UserFacing).To(gomega.BeTrue(), fmt.Sprintf("explicit source-signature question should be marked user-facing: %+v", question))
	gomega.Expect(strings.Contains(question.RequestedDecision, "exact current helper signatures")).To(gomega.BeTrue(), fmt.Sprintf("question should request source-verified signatures: %+v", question))

})
var _ = ginkgo.It("validate standalone workspace rejects invented c make verification command", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/storage/stockpile_system.cpp"), "void scan() {}\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")
	mkdirAndWriteFile(filepath.Join(root, "native/SConstruct"), "# scons project\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/storage/stockpile_system.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native && cmake --build . && ctest -R test_fluid_brewery_cascade --output-on-failure"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invented CMake command rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("no CMake project evidence"), fmt.Sprintf("expected invented CMake command rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace rejects unsupported verification command family", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"custom-test-runner ./internal/labels"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unsupported command family rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unsupported command family"), fmt.Sprintf("expected unsupported command family rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace accepts repo backed s cons verification command", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"scons tests=yes target=template_debug"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("repo-backed scons command should pass: %v", err))
	}

})
var _ = ginkgo.It("validate standalone workspace accepts repo backed s cons and local test binary", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	{
		err := os.Chmod(filepath.Join(root, "native/tests/bin/colony_tests"), 0755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("chmod fixture binary: %v", err))
	}

	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("repo-backed scons plus local test binary command should pass: %v", err))
	}

})
var _ = ginkgo.It("validate standalone workspace rejects missing local test binary", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing local test binary rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unsupported command family"), fmt.Sprintf("expected missing local test binary rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace rejects non executable local command path", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected non-executable local command rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unsupported command family"), fmt.Sprintf("expected non-executable local command rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace accepts executable dot slash local command path", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	{
		err := os.Chmod(filepath.Join(root, "native/tests/bin/colony_tests"), 0755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("chmod fixture binary: %v", err))
	}

	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && ./bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("executable ./ local command should pass: %v", err))
	}

})
var _ = ginkgo.It("validate standalone workspace rejects non executable dot slash local command path", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "native/tests/SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/bin/colony_tests"), "#!/bin/sh\n")
	mkdirAndWriteFile(filepath.Join(root, "native/src/simulation/machines/machine.cpp"), "void tick_normal() {}\n")
	mkdirAndWriteFile(filepath.Join(root, "native/tests/test_fluid_brewery_cascade.cpp"), "TEST_CASE(\"cascade\") { CHECK(true); }\n")

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/machines/machine.cpp",
		"native/tests/test_fluid_brewery_cascade.cpp",
	}
	plan.Atoms[0].TestFirst.Commands = []string{"cd native/tests && scons -j10 && ./bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_fluid_brewery_cascade.cpp"}

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected non-executable ./ local command rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing or non-executable local command"), fmt.Sprintf("expected non-executable ./ local command rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace rejects invalid command directory", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"cd ../outside && scons -j10"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid cd rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("invalid workspace directory"), fmt.Sprintf("expected invalid cd rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace rejects absolute local command path", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "SConstruct"), "# scons project\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels_test.go"), "package labels\n\nimport \"testing\"\n\nfunc TestLabels(t *testing.T) { t.Fatal(\"red\") }\n")

	plan.Atoms[0].TestFirst.Commands = []string{"/tmp/colony_tests -sf=test_fluid_brewery_cascade.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = append([]string(nil), plan.Atoms[0].TestFirst.Commands...)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected absolute command path rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unsupported command family"), fmt.Sprintf("expected absolute command path rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace rejects skipped comment only red confirmed test", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels_test.go"), `package labels

// Tests are SKIP-marked pending FET 3.3c.
// TODO: assert cistern decrement.
`)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected skipped/comment-only red-confirmed test rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("comment-only"), fmt.Sprintf("expected skipped/comment-only red-confirmed test rejection, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("red-confirmed"), fmt.Sprintf("expected skipped/comment-only red-confirmed test rejection, got %v", err))

})
var _ = ginkgo.It("validate standalone workspace ignores assertion tokens in comments", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels.go"), "package labels\n")
	mkdirAndWriteFile(filepath.Join(root, "internal/labels/labels_test.go"), `package labels

// add t.Fatal here when this is implemented
`)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{WorkspaceRoot: root})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected comment assertion token to be ignored, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("comment-only"), fmt.Sprintf("expected comment assertion token to be ignored, got %v", err))

})
var _ = ginkgo.It("run does not treat ordinary candidate feature as localization repair", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Summary = "Add candidate scoring"
	plan.TopLevelScope.Objective = "Add candidate scoring"
	plan.Atoms[0].Title = "Add candidate scoring"
	plan.Atoms[0].RequiredEditPaths = []string{"src/recruiting/candidate_scoring.py"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/test_candidate_scoring.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/test_candidate_scoring.py"}

	state := NewState("session", "add candidate scoring", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Add candidate scoring to the recruiting workflow.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone workspace validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("expected standalone context blocked only, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected standalone context blocked only, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(0), fmt.Sprintf("ordinary candidate feature must not open localization question: %+v", result.State.OpenQuestions))

})
var _ = ginkgo.It("run greenfield skeleton opens operator question without leaking diagnostic summary", func() {
	root := ginkgo.GinkgoT().TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, ".planning"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .planning: %v", err))
	}
	{

		err := os.WriteFile(filepath.Join(root, ".planning", "previous-session.md"), []byte("# Feature Planner Candidate\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write prior plan tab artifact: %v", err))
	}

	plan := validPlan()

	plan.FeatureID = "llm-brain-bones"
	plan.Summary = "Lay out the LLM brain project bones"
	plan.TopLevelScope.Objective = "Establish the LLM brain project skeleton."
	plan.Atoms[0].RequiredEditPaths = []string{"src/brain_core/types.py"}
	plan.Atoms[0].AllowedEditPaths = []string{"tests/test_core_types.py"}
	plan.Atoms[0].TestFirst.Commands = []string{"pytest tests/test_core_types.py"}
	plan.Atoms[0].RequiredTests = []string{"pytest tests/test_core_types.py"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/test_core_types.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"pytest tests/test_core_types.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/test_core_types.py"}
	plan.Atoms[0].Prompt = "Create file src/brain_core/types.py and tests/test_core_types.py. Include __init__.py(empty) as a literal placeholder."

	state := NewState("session", "lay out project bones", "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                "Let's lay out the bones for this LLM brain project.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("greenfield authority must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(2), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.QuestionSummary).To(gomega.Equal("Approve or revise greenfield project scope"), fmt.Sprintf("unexpected greenfield authority question: %+v", question))
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("unexpected greenfield authority question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeTrue(), fmt.Sprintf("unexpected greenfield authority question: %+v", question))
	gomega.Expect(containsString(question.AffectedPaths, "src/brain_core/types.py")).To(gomega.BeTrue(), fmt.Sprintf("unexpected greenfield authority question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "verification_command")).To(gomega.BeFalse(), fmt.Sprintf("greenfield approval should not ask the operator to provide model-synthesized verification commands: %+v", question))

	summary := FormatStateSummary(result.State, ".idiom/feature-planner/session/state.json")
	for _, forbidden := range []string{
		"atom 0 edit path",
		"pytest tests/test_core_types.py",
		"has no pytest project evidence",
		"__init__.py(empty)",
	} {
		gomega.Expect(strings.Contains(summary, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("operator summary leaked internal diagnostic %q:\n%s", forbidden, summary))

	}
	for _, want := range []string{
		"feature-planner: planning-question-open",
		"question: Approve or revise greenfield project scope",
		"requested_decision:",
		"how_to_continue: reply with approval",
		"blockers: blocked by open planner question",
	} {
		gomega.Expect(strings.Contains(summary, want)).To(gomega.BeTrue(), fmt.Sprintf("operator summary missing %q:\n%s", want, summary))

	}
})
var _ = ginkgo.It("run greenfield MVP project opens operator question without leaking repair", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.FeatureID = "llm-brain-mvp"
	plan.Summary = "Work out an MVP for an LLM brain project."
	plan.TopLevelScope.Objective = "Establish the minimal LLM brain project structure."
	plan.Atoms[0].RequiredEditPaths = []string{"src/brain/knowledge_store.py", "tests/test_knowledge_store.py"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.Commands = []string{"pytest tests/test_knowledge_store.py"}
	plan.Atoms[0].RequiredTests = []string{"pytest tests/test_knowledge_store.py"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/test_knowledge_store.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"pytest tests/test_knowledge_store.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/test_knowledge_store.py"}
	plan.Atoms[0].Prompt = "Create src/brain/knowledge_store.py and tests/test_knowledge_store.py for the MVP."

	state := NewState("session", "llm brain mvp", "", 3, time.Unix(1, 0).UTC())
	result, err := Run(state, RunOptions{
		Prompt:                `Can you work out an MVP to an effective "llm brain" sort of project?`,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("greenfield MVP authority must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.Events).To(gomega.HaveLen(2), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	for _, want := range []string{"src/brain/knowledge_store.py", "tests/test_knowledge_store.py"} {
		gomega.Expect(containsString(question.AffectedPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("question missing affected path %q: %+v", want, question))

	}
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("unexpected greenfield MVP authority question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeTrue(), fmt.Sprintf("unexpected greenfield MVP authority question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "verification_command")).To(gomega.BeFalse(), fmt.Sprintf("greenfield MVP approval should not ask the operator to provide model-synthesized verification commands: %+v", question))

	summary := FormatStateSummary(result.State, ".idiom/feature-planner/session/state.json")
	for _, forbidden := range []string{
		"atom 0 edit path",
		"has no pytest project evidence",
		"retrieve_similar(",
		"missing standalone new-file authorization",
	} {
		gomega.Expect(strings.Contains(summary, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("operator summary leaked repair diagnostic %q:\n%s", forbidden, summary))

	}
	for _, want := range []string{
		"feature-planner: planning-question-open",
		"question: Approve or revise greenfield project scope",
		"blocked by open planner question",
	} {
		gomega.Expect(strings.Contains(summary, want)).To(gomega.BeTrue(), fmt.Sprintf("operator summary missing %q:\n%s", want, summary))

	}
})
var _ = ginkgo.It("run prefers explicit new file authorization over localization question", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}

	mkdirAndWriteFile(filepath.Join(root, "native/src/core/colony_sim_tick.cpp"), "// tick\n")

	state := NewState("session", "pick a prepared atom", "", 3, time.Unix(1, 0).UTC())
	prompt := "Pick one prepared next-atom candidate. If any required new source or test file is missing, ask me for explicit product-owner authorization with the exact affected paths before dispatching implementation."

	result, err := Run(state, RunOptions{
		Prompt:                prompt,
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone workspace validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("explicit new-file prompt should keep new-file authorization precedence: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeTrue(), fmt.Sprintf("explicit new-file prompt should keep new-file authorization precedence: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "source_localization")).To(gomega.BeFalse(), fmt.Sprintf("explicit new-file prompt should not become localization question: %+v", question))

})
var _ = ginkgo.It("run opens question for explicit missing new file authorization prompt", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}

	mkdirAndWriteFile(filepath.Join(root, "native/src/core/colony_sim_tick.cpp"), "// tick\n")

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Implement CollapseEvent. If any required new source or test file is missing, ask me for explicit product-owner authorization with the exact affected paths before dispatching implementation.",
		MaxPlanningIterations: 3,
		WorkspaceRoot:         root,
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("standalone workspace validation must block before dispatch"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(len(result.State.Atoms)).To(gomega.Equal(0), fmt.Sprintf("invalid standalone plan should not compile atoms: %+v", result.State.Atoms))
	gomega.Expect(result.Events).To(gomega.HaveLen(2), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.standalone_context_blocked"), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.planner_question_opened"), fmt.Sprintf("expected rejected-plan then question events, got %+v", result.Events))
	gomega.Expect(len(result.State.RejectedPlanCandidates)).To(gomega.Equal(1), fmt.Sprintf("expected rejected plan candidate, got %+v", result.State.RejectedPlanCandidates))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("expected one open question, got %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.Status).To(gomega.Equal("open"), fmt.Sprintf("unexpected authorization question: %+v", question))
	gomega.Expect(question.AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("unexpected authorization question: %+v", question))
	gomega.Expect(containsString(question.AffectedFields, "new_file_authorization")).To(gomega.BeTrue(), fmt.Sprintf("unexpected authorization question: %+v", question))
	gomega.Expect(containsString(question.AffectedPaths, "native/src/simulation/world/collapse_event.cpp")).To(gomega.BeTrue(), fmt.Sprintf("question should request exact missing path authorization only: %+v", question))
	gomega.Expect(containsString(question.AffectedPaths, "native/tests/test_collapse_event.cpp")).To(gomega.BeTrue(), fmt.Sprintf("question should request exact missing path authorization only: %+v", question))
	gomega.Expect(containsString(question.AffectedPaths, "native/src/core/colony_sim_tick.cpp")).To(gomega.BeFalse(), fmt.Sprintf("question should request exact missing path authorization only: %+v", question))

})
var _ = ginkgo.It("answer open question clears authorized new file blockers", func() {
	blocker := "atom 0 edit path \"native/src/simulation/world/collapse_event.cpp\" is missing from the workspace and is not authorized as a new file; use ask_planner_question or source-satisfied evidence with existing paths instead of inventing paths: missing standalone new-file authorization\natom 0 edit path \"native/tests/test_collapse_event.cpp\" is missing from the workspace and is not authorized as a new file; use ask_planner_question or source-satisfied evidence with existing paths instead of inventing paths: missing standalone new-file authorization\nmissing new-file authorization for: native/src/simulation/world/collapse_event.cpp, native/tests/test_collapse_event.cpp"
	other := "nested plan-execute reported failure details: planner_blocked"
	state := FeatureState{
		SessionID: "feature-session",
		Status:    FeatureStatePlanningQuestion,
		Blockers:  []string{blocker, other},
		Atoms: []FeatureAtomState{{
			FeatureAtomPlan: FeatureAtomPlan{ID: "atom-1"},
			State:           AtomStateRecoveryOpen,
			Blockers:        []string{blocker, other},
		}},
		OpenQuestions: []PlannerQuestion{{
			ID:                "q-001",
			QuestionSummary:   "Authorize new files",
			BlockingClaim:     "new files need approval",
			QuestionKind:      "external_answer_required",
			AuthorityRequired: "operator_approval",
			AffectedPaths:     []string{"native/src/simulation/world/collapse_event.cpp", "native/tests/test_collapse_event.cpp"},
			AffectedFields:    []string{"new_file_authorization"},
			Status:            "open",
		}},
	}

	events := answerOpenQuestion(&state, "Approved. You may create native/src/simulation/world/collapse_event.cpp and native/tests/test_collapse_event.cpp.")
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected answer events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal(featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionAnswered)), fmt.Sprintf("unexpected answer events: %+v", events))
	gomega.Expect(state.Blockers).NotTo(gomega.ContainElement(blocker), fmt.Sprintf("authorized new-file blocker should be cleared, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(state.Atoms[0].Blockers).NotTo(gomega.ContainElement(blocker), fmt.Sprintf("authorized new-file blocker should be cleared, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(state.Blockers).To(gomega.ContainElement(other), fmt.Sprintf("unrelated blockers should remain, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(state.Atoms[0].Blockers).To(gomega.ContainElement(other), fmt.Sprintf("unrelated blockers should remain, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers))

})
var _ = ginkgo.It("answer open question denial does not clear new file blockers", func() {
	blocker := "missing new-file authorization for: native/src/simulation/world/collapse_event.cpp, native/tests/test_collapse_event.cpp"
	state := FeatureState{
		SessionID: "feature-session",
		Status:    FeatureStatePlanningQuestion,
		Blockers:  []string{blocker},
		Atoms: []FeatureAtomState{{
			FeatureAtomPlan: FeatureAtomPlan{ID: "atom-1"},
			State:           AtomStateRecoveryOpen,
			Blockers:        []string{blocker},
		}},
		OpenQuestions: []PlannerQuestion{{
			ID:                "q-001",
			QuestionSummary:   "Authorize new files",
			BlockingClaim:     "new files need approval",
			QuestionKind:      "external_answer_required",
			AuthorityRequired: "operator_approval",
			AffectedPaths:     []string{"native/src/simulation/world/collapse_event.cpp", "native/tests/test_collapse_event.cpp"},
			AffectedFields:    []string{"new_file_authorization"},
			Status:            "open",
		}},
	}

	events := answerOpenQuestion(&state, "No, do not create native/src/simulation/world/collapse_event.cpp or native/tests/test_collapse_event.cpp.")
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected answer events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal(featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionAnswered)), fmt.Sprintf("unexpected answer events: %+v", events))
	gomega.Expect(state.Blockers).To(gomega.ContainElement(blocker), fmt.Sprintf("denied new-file blocker should remain, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(state.Atoms[0].Blockers).To(gomega.ContainElement(blocker), fmt.Sprintf("denied new-file blocker should remain, state=%#v atom=%#v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(deltaAuthorizesNewFile(state.AuthorityDeltas[0])).To(gomega.BeFalse(), fmt.Sprintf("denial answer must not compile as new-file authorization: %+v", state.AuthorityDeltas[0]))

})
var _ = ginkgo.It("answer open question treats blank status as open", func() {
	state := FeatureState{
		SessionID: "feature-session",
		Status:    FeatureStatePlanningQuestion,
		OpenQuestions: []PlannerQuestion{{
			ID:                "q-001",
			QuestionSummary:   "Authorize new files",
			BlockingClaim:     "new files need approval",
			QuestionKind:      "external_answer_required",
			AuthorityRequired: "operator_approval",
			AffectedPaths:     []string{"src/brain.py"},
			AffectedFields:    []string{"new_file_authorization"},
		}},
	}

	events := answerOpenQuestion(&state, "Approved. You may create src/brain.py.")
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("blank-status question should be answerable, got events=%+v state=%+v", events, state))
	gomega.Expect(events[0].Type).To(gomega.Equal(featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionAnswered)), fmt.Sprintf("blank-status question should be answerable, got events=%+v state=%+v", events, state))
	gomega.Expect(state.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("question status = %q, want answered", state.OpenQuestions[0].Status))

})
var _ = ginkgo.It("new file authorization denial phrases win over approval words", func() {
	base := PlanAuthorityDelta{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		AffectedPaths:  []string{"native/src/simulation/world/collapse_event.cpp"},
		AffectedFields: []string{"new_file_authorization"},
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}

	for _, actual := range []string{
		"No authorization is approved for these files.",
		"This is not allowed.",
		"No approval; do not create the files.",
		"Do not approve creating native/src/simulation/world/collapse_event.cpp.",
		"Don't approve creating native/src/simulation/world/collapse_event.cpp.",
		"Don't authorize native/src/simulation/world/collapse_event.cpp.",
	} {
		delta := base
		delta.Actual = actual
		gomega.Expect(deltaAuthorizesNewFile(delta)).To(gomega.BeFalse(), fmt.Sprintf("denial phrase compiled as authorization: %q", actual))

	}

	approved := base
	approved.Actual = "Approved. You may create exactly native/src/simulation/world/collapse_event.cpp."
	gomega.Expect(deltaAuthorizesNewFile(approved)).To(gomega.BeTrue(), fmt.Sprintf("explicit approval should compile as new-file authorization"))

})
var _ = ginkgo.It("new file authorization revision phrases do not authorize original paths", func() {
	base := PlanAuthorityDelta{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		AffectedPaths:  []string{"src/brain.py", "tests/test_brain.py"},
		AffectedFields: []string{"new_file_authorization", "verification_command"},
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}

	for _, actual := range []string{
		"Yes, use existing files instead.",
		"Yes, revise the plan to avoid creating new files.",
		"Approved, but change the paths before continuing.",
		"Ok, use a different project structure instead.",
	} {
		delta := base
		delta.Actual = actual
		gomega.Expect(deltaAuthorizesNewFile(delta)).To(gomega.BeFalse(), fmt.Sprintf("revision answer compiled as new-file authorization: %q", actual))

	}

	approved := base
	approved.Actual = "Approved. Use Python with pytest. Create src/brain.py and tests/test_brain.py."
	gomega.Expect(deltaAuthorizesNewFile(approved)).To(gomega.BeTrue(), fmt.Sprintf("approval with project setup should still authorize listed new files"))

})
var _ = ginkgo.It("apply implementor output preserves recovery packet for missing path failure", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{
		"tool":"plan_execute_completed",
		"lane_family":"plan-execute",
		"status":"failed",
		"phase":"planning",
		"failure_kind":"missing_edit_path_without_new_file_authorization",
		"failure_reason":"plan would create missing edit path without new-file authorization",
		"failure_id":"failure-123",
		"record_path":".idiom/recovery/failures/failure-123.json",
		"packet_path":".idiom/recovery/packets/failure-123.json",
		"next_valid_action":"review_decomposition",
		"progress_path":".idiom/plan-execute/sessions/pe.json"
	}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, "missing_edit_path_without_new_file_authorization")
	gomega.Expect(containsString(state.OpenRecoveryPackets, ".idiom/recovery/packets/failure-123.json")).To(gomega.BeTrue(), fmt.Sprintf("recovery packet path was not retained: %+v", state.OpenRecoveryPackets))

	fields := events[0].Fields
	for key, want := range map[string]any{
		"failure_id":        "failure-123",
		"record_path":       ".idiom/recovery/failures/failure-123.json",
		"packet_path":       ".idiom/recovery/packets/failure-123.json",
		"next_valid_action": "review_decomposition",
	} {
		{
			got := fields[key]
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("event field %s = %#v, want %#v; fields=%+v", key, got, want, fields))
		}

	}
})
var _ = ginkgo.It("build planner prompt advertises flat submit feature plan schema", func() {
	prompt := BuildPlannerPrompt("build feature", NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC()), nil)

	for _, want := range []string{
		`return the FeaturePlan object itself`,
		`Do not nest the plan under a plan, payload, arguments, or input field`,
		`"tool":"ask_planner_question"`,
		`"question_summary"`,
		`"blocking_claim"`,
		`"requested_decision"`,
		`do not include affected_paths for non-path authority`,
		`affected_paths must contain the exact repo-relative paths`,
		`"affected_fields"`,
		`paths inside requested_decision are explanatory text only and do not grant authority`,
		`do not return both arrays empty`,
		`"tool":"submit_feature_plan"`,
		`"top_level_scope"`,
		`"tdd_contract"`,
		`"planned_tdd_snapshot"`,
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt missing %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("run opens normalized planner question from question alias", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)), fmt.Sprintf("task family = %q, want planner", taskFamily))

		return `{"tool":"ask_planner_question","question":"Please provide tooltip source paths."}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("open questions = %+v", result.State.OpenQuestions))

	question := result.State.OpenQuestions[0]
	gomega.Expect(question.QuestionSummary).To(gomega.Equal("Please provide tooltip source paths."), fmt.Sprintf("question was not normalized: %+v", question))
	gomega.Expect(question.BlockingClaim).NotTo(gomega.Equal(""), fmt.Sprintf("question was not normalized: %+v", question))
	gomega.Expect(question.RequestedDecision).NotTo(gomega.Equal(""), fmt.Sprintf("question was not normalized: %+v", question))
	gomega.Expect(question.AuthorityRequired).NotTo(gomega.Equal(""), fmt.Sprintf("question was not normalized: %+v", question))
	gomega.Expect(question.RequiredBefore).NotTo(gomega.Equal(""), fmt.Sprintf("question was not normalized: %+v", question))

})
var _ = ginkgo.It("build implementor prompt rejects unsupported edit test claims", func() {
	plan := validPlan()

	plan.Atoms[0].Prompt = "Use exact old_text/new_text anchors from the atom brief."

	prompt := BuildImplementorPromptFromContract(FeatureAtomState{FeatureAtomPlan: plan.Atoms[0]}, plan, CompilePlanAuthorityContract(plan, nil))

	for _, want := range []string{
		`This atom brief is consumed by plan-execute`,
		`Plan-execute owns the terminal response protocol`,
		`do not emit feature-planner completion JSON`,
		`Do not claim files were edited or tests were run`,
		`unsupported edit/test claims are rejected as recovery-open`,
		`Requested verification commands for executor phase:`,
		"Required verification command: run `go test ./...` after edits.",
		`Atom implementation brief:`,
		`Use exact old_text/new_text anchors from the atom brief.`,
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("implementor prompt missing %q:\n%s", want, prompt))

	}

	for _, stale := range []string{
		`"tool":"question_for_planner"`,
		`You may answer with JSON`,
	} {
		gomega.Expect(strings.Contains(prompt, stale)).To(gomega.BeFalse(), fmt.Sprintf("implementor prompt retained feature-planner terminal surface %q:\n%s", stale, prompt))

	}
})
var _ = ginkgo.It("build implementor prompt carries typed plan new file authority", func() {
	plan := validPlan()
	contract := CompilePlanAuthorityContract(plan, []PlanAuthorityDelta{{
		QuestionID:     "q-new-file",
		AuthorityKind:  "new_file_authorization",
		EvidenceRefs:   []string{"operator:q-new-file"},
		AffectedPaths:  []string{"pkg/new_helper.go"},
		AffectedFields: []string{"new_file_authorization"},
		Expected:       "new helper requires explicit typed authority",
		Actual:         "operator authorized the helper",
		Revision:       1,
		CreatedAt:      time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}})
	prompt := BuildImplementorPromptFromContract(FeatureAtomState{FeatureAtomPlan: plan.Atoms[0]}, plan, contract)
	gomega.Expect(strings.Contains(prompt, "Typed plan execution contract:")).To(gomega.BeTrue(), fmt.Sprintf("implementor prompt missing typed plan execution contract:\n%s", prompt))
	{

		got := plancontract.AuthorizedNewFilePathsFromPrompt(prompt)
		gomega.Expect(slices.Equal(got, []string{"pkg/new_helper.go"})).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v", got))
	}

})
var _ = ginkgo.It("apply planner output accepts plan and queues atoms", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	parsed := ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}

	events := ApplyPlannerOutput(&state, parsed, time.Unix(2, 0).UTC())
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateDispatching), fmt.Sprintf("state.Status = %q, want dispatching", state.Status))
	gomega.Expect(state.Atoms).To(gomega.HaveLen(1), fmt.Sprintf("unexpected atoms: %+v", state.Atoms))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateQueued), fmt.Sprintf("unexpected atoms: %+v", state.Atoms))
	gomega.Expect(events).To(gomega.HaveLen(6), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.plan_accepted"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[4].Type).To(gomega.Equal("idiom.feature_planner.tdd_pre_execution_checked"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[len(events)-1].Type).To(gomega.Equal("idiom.feature_planner.dispatch_ready"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(state.TDDPreExecutionEvidence).To(gomega.HaveLen(1), fmt.Sprintf("expected pre-dispatch TDD evidence ledger, got %+v", state.TDDPreExecutionEvidence))
	gomega.Expect(state.TDDPreExecutionEvidence[0].EventID).To(gomega.Equal("evt-tdd-1"), fmt.Sprintf("expected pre-dispatch TDD evidence ledger, got %+v", state.TDDPreExecutionEvidence))

})
var _ = ginkgo.It("apply planner output stamps TDD snapshot identity", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	plan := validPlan()
	plan.Atoms[0].PlannedTDDSnapshot.SnapshotID = "model-authored-snapshot"
	plan.Atoms[0].PlannedTDDSnapshot.SnapshotDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())
	gomega.Expect(events).NotTo(gomega.HaveLen(0), fmt.Sprintf("expected accepted plan, status=%q events=%+v blockers=%v", state.Status, events, state.Blockers))
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateDispatching), fmt.Sprintf("expected accepted plan, status=%q events=%+v blockers=%v", state.Status, events, state.Blockers))

	gotSnapshot := state.Plan.Atoms[0].PlannedTDDSnapshot
	gomega.Expect(gotSnapshot.SnapshotID).NotTo(gomega.Equal("model-authored-snapshot"), fmt.Sprint("model-authored snapshot_id was not replaced by harness stamp"))
	gomega.Expect(gotSnapshot.SnapshotDigest).NotTo(gomega.Equal("sha256:1111111111111111111111111111111111111111111111111111111111111111"), fmt.Sprint("model-authored snapshot_digest was not replaced by harness stamp"))
	gomega.Expect(strings.HasPrefix(gotSnapshot.SnapshotID, "tdd-snap-session-atom-1")).To(gomega.BeTrue(), fmt.Sprintf("snapshot_id = %q, want harness-stamped session/atom id", gotSnapshot.SnapshotID))
	{

		reason := invalidPlannedTDDSnapshotDigestReason(gotSnapshot.SnapshotDigest)
		gomega.Expect(reason).To(gomega.Equal(""), fmt.Sprintf("harness snapshot_digest invalid: %s (%q)", reason, gotSnapshot.SnapshotDigest))
	}
	gomega.Expect(len(state.TDDPreExecutionEvidence)).To(gomega.Equal(1), fmt.Sprintf("expected one TDD evidence row, got %+v", state.TDDPreExecutionEvidence))
	gomega.Expect(state.TDDPreExecutionEvidence[0].SnapshotID).To(gomega.Equal(gotSnapshot.SnapshotID), fmt.Sprintf("evidence did not use harness-stamped snapshot identity, evidence=%+v snapshot=%+v", state.TDDPreExecutionEvidence[0], gotSnapshot))
	gomega.Expect(state.TDDPreExecutionEvidence[0].SnapshotDigest).To(gomega.Equal(gotSnapshot.SnapshotDigest), fmt.Sprintf("evidence did not use harness-stamped snapshot identity, evidence=%+v snapshot=%+v", state.TDDPreExecutionEvidence[0], gotSnapshot))

})
var _ = ginkgo.It("compute planned TDD snapshot digest is deterministic", func() {
	first := PlannedTDDSnapshot{
		ObligationIDs:                []string{"TDD-2", "TDD-1"},
		TestCommands:                 []string{"go test ./b", "go test ./a"},
		TestArtifactPaths:            []string{"b_test.go", "a_test.go"},
		ExpectedPreExecutionResults:  []string{"red-confirmed"},
		CreatedBeforeDispatchEventID: "evt-tdd-1",
	}
	second := PlannedTDDSnapshot{
		ObligationIDs:                []string{"TDD-1", "TDD-2"},
		TestCommands:                 []string{"go test ./a", "go test ./b"},
		TestArtifactPaths:            []string{"a_test.go", "b_test.go"},
		ExpectedPreExecutionResults:  []string{"red-confirmed"},
		CreatedBeforeDispatchEventID: "evt-tdd-1",
	}

	first.SnapshotID = "model-value-one"
	first.SnapshotDigest = "sha256:" + strings.Repeat("1", 64)
	second.SnapshotID = "model-value-two"
	second.SnapshotDigest = "sha256:" + strings.Repeat("2", 64)

	got := computePlannedTDDSnapshotDigest(first)
	gomega.Expect(got).To(gomega.Equal(computePlannedTDDSnapshotDigest(second)), fmt.Sprintf("digest should be stable across list order and snapshot identity fields"))
	{

		reason := invalidPlannedTDDSnapshotDigestReason(got)
		gomega.Expect(reason).To(gomega.Equal(""), fmt.Sprintf("digest is not valid sha256 format: %s (%q)", reason, got))
	}

})
var _ = ginkgo.It("apply planner output invalid durable plan does not replace committed plan", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	accepted := validPlan()
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: accepted}, time.Unix(2, 0).UTC())
	committedID := state.Plan.Atoms[0].ID

	invalid := validPlan()
	invalid.Atoms[0].ID = "invalid-candidate"
	invalid.Atoms[0].TestFirst.Commands = nil
	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: invalid}, time.Unix(3, 0).UTC())
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("expected blocked event, got %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.tdd_pre_execution_blocked"), fmt.Sprintf("expected blocked event, got %+v", events))
	gomega.Expect(state.Plan.Atoms[0].ID).To(gomega.Equal(committedID), fmt.Sprintf("invalid candidate replaced committed plan: got atom %q want %q", state.Plan.Atoms[0].ID, committedID))

})
var _ = ginkgo.It("apply planner output uses accepted source authority for durable prompt calls", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	state.AuthorityDeltas = []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedPaths:  []string{"native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		SourceFacts: []plancontract.SourceAuthorityFact{{
			Path:      "native/src/simulation/economy/fluid_network_system.h",
			Symbol:    "FluidNetworkSystem::find_connected_cistern_with_volume",
			Signature: "EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const",
		}},
		Expected:  "helper signature needs accepted source evidence",
		Actual:    "accepted source fact approved for fluid network helper usage",
		Revision:  1,
		CreatedAt: time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}}

	plan := validPlan()
	plan.Atoms[0].Prompt = "Use FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) before posting fallback water haul."

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, time.Unix(3, 0).UTC())
	if len(events) == 1 {
		gomega.Expect(events[0].Type).NotTo(gomega.Equal("idiom.feature_planner.tdd_pre_execution_blocked"), fmt.Sprintf("durable validation ignored accepted source authority: %+v", events))
	}
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateDispatching), fmt.Sprintf("status = %q, want dispatching; events=%+v blockers=%+v", state.Status, events, state.Blockers))

})
var _ = ginkgo.It("plan contract authority deltas carry typed source facts", func() {
	deltas := []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"./native/src/simulation/economy/fluid_network_system.h"},
		AffectedPaths:  []string{"./native/src/simulation/economy/fluid_network_system.h"},
		AffectedFields: []string{"source_localization"},
		SourceFacts: []plancontract.SourceAuthorityFact{{
			Path:      "./native/src/simulation/economy/fluid_network_system.h",
			Symbol:    "FluidNetworkSystem::find_connected_cistern_with_volume",
			Signature: "EntityID FluidNetworkSystem::find_connected_cistern_with_volume(uint32_t network_id, int32_t volume_required) const",
		}},
		Actual:    "approved",
		Revision:  1,
		CreatedAt: time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}}

	contractDeltas := planContractAuthorityDeltas(deltas)
	gomega.Expect(len(contractDeltas)).To(gomega.Equal(1), fmt.Sprintf("contract deltas = %+v, want one", contractDeltas))

	facts := contractDeltas[0].SourceFacts
	gomega.Expect(len(facts)).To(gomega.Equal(1), fmt.Sprintf("source facts were not carried into contract: %+v", contractDeltas[0]))
	gomega.Expect(facts[0].Path).To(gomega.Equal("native/src/simulation/economy/fluid_network_system.h"), fmt.Sprintf("source fact path = %q, want normalized path", facts[0].Path))
	gomega.Expect(facts[0].Signature).NotTo(gomega.Equal(""), fmt.Sprintf("source fact signature missing: %+v", facts[0]))
	gomega.Expect(facts[0].Signature).To(gomega.ContainSubstring("find_connected_cistern_with_volume"), fmt.Sprintf("source fact signature missing: %+v", facts[0]))

})
var _ = ginkgo.It("apply planner output rejects demoted implementation scope before dispatch", func() {
	state := NewState("session", "build collapse event helper", "", 1, time.Unix(1, 0).UTC())
	plan := validPlan()
	plan.Summary = "Implement CollapseEvent struct and dispatch helper, integrate into colony_sim_tick.cpp, and add focused unit tests."
	plan.TopLevelScope.SuccessCriteria = []string{
		"CollapseEvent helper is declared in native/src/simulation/world/collapse_event.h.",
		"colony_sim_tick.cpp uses CollapseEvent::dispatch for collapse events.",
	}
	plan.Atoms[0].RequiredEditPaths = []string{
		"native/src/simulation/world/collapse_event.cpp",
		"native/tests/test_collapse_event.cpp",
	}
	plan.Atoms[0].AllowedEditPaths = []string{
		"native/src/simulation/world/collapse_event.h",
		"native/src/core/colony_sim_tick.cpp",
	}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"native/tests/test_collapse_event.cpp"}
	plan.Atoms[0].Prompt = "Implement collapse_event.h declarations and update colony_sim_tick.cpp to use CollapseEvent::dispatch."

	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("expected durable plan rejection, got status=%s events=%+v", state.Status, events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.tdd_pre_execution_blocked"), fmt.Sprintf("expected durable plan rejection, got status=%s events=%+v", state.Status, events))
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("invalid demoted scope must not queue atoms, status=%s atoms=%+v", state.Status, state.Atoms))
	gomega.Expect(state.Atoms).To(gomega.HaveLen(0), fmt.Sprintf("invalid demoted scope must not queue atoms, status=%s atoms=%+v", state.Status, state.Atoms))

})
var _ = ginkgo.It("run dispatches atom after accepted TDD plan", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	calls := 0

	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		calls++

		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(validPlan()), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("calls = %d, want planner + implementor", calls))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("state.Status = %q, want complete", result.State.Status))
	gomega.Expect(result.Events).To(gomega.HaveLen(9), fmt.Sprintf("events = %+v, want plan review sequence, dispatch, complete", result.Events))
	gomega.Expect(result.Events[len(result.Events)-1].Type).To(gomega.Equal("idiom.feature_planner.release_ready"), fmt.Sprintf("events = %+v, want plan review sequence, dispatch, complete", result.Events))
	gomega.Expect(len(result.State.ActiveRuns)).To(gomega.Equal(0), fmt.Sprintf("active runs should be folded after terminal atom: %+v", result.State.ActiveRuns))

})
var _ = ginkgo.It("new file authorization approval ignores blocking claim no project files", func() {
	delta := PlanAuthorityDelta{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		AffectedPaths:  []string{"src/brain.py", "tests/test_brain.py"},
		AffectedFields: []string{"new_file_authorization", "verification_command"},
		Expected:       "The workspace has no project files, so the standalone feature planner cannot infer new files safely.",
		Actual:         "Approved. Use Python with pytest. Create src/brain.py and tests/test_brain.py.",
	}
	gomega.Expect(deltaAuthorizesNewFile(delta)).To(gomega.BeTrue(), fmt.Sprintf("operator approval should authorize new files even when the blocking claim says no project files: %+v", delta))

})
var _ = ginkgo.It("validate standalone workspace accepts greenfield planner verification command from approved new files", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validGreenfieldBrainPlanForTest()
	deltas := []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		EvidenceRefs:   []string{"planner_question:q-001"},
		AffectedPaths:  []string{"src/brain.py", "tests/test_brain.py"},
		AffectedFields: []string{"new_file_authorization"},
		Expected:       "The workspace has no project files, so the standalone feature planner cannot infer new files safely.",
		Actual:         "Approved. Use Python with pytest. Create src/brain.py and tests/test_brain.py.",
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}
	contract := StampPlanAuthorityContract(&plan, deltas)
	{

		err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
			WorkspaceRoot:   root,
			Contract:        contract,
			AuthorityDeltas: deltas,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("approved greenfield new files should authorize planner-synthesized pytest command for those files: %v", err))
	}

})
var _ = ginkgo.It("validate standalone workspace rejects verification command family approval", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := runningExampleExistingPathPlanForTest()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")

	deltas := []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		EvidenceRefs:   []string{"planner_question:q-001"},
		AffectedPaths:  []string{"tests/test_query_handler.py"},
		AffectedFields: []string{"verification_command"},
		Expected:       "Provide the exact verification command to use for this project.",
		Actual:         "Approved. Use pytest as the verification command.",
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}
	contract := StampPlanAuthorityContract(&plan, deltas)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot:   root,
		Contract:        contract,
		AuthorityDeltas: deltas,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("verification command family approval must not authorize exact rejected command"))
	{

		issues := StandaloneValidationIssues(err)
		gomega.Expect(standaloneValidationHasIssueKind(issues, "verification_command_unverified")).To(gomega.BeTrue(), fmt.Sprintf("expected typed verification issue, got err=%v issues=%+v", err, issues))
	}

})
var _ = ginkgo.It("validate standalone workspace rejects greenfield verification command outside approved new files", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validGreenfieldBrainPlanForTest()
	plan.Atoms[0].TestFirst.Commands = []string{"pytest tests/test_other.py"}
	plan.Atoms[0].RequiredTests = []string{"pytest tests/test_other.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"pytest tests/test_other.py"}

	deltas := []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		EvidenceRefs:   []string{"planner_question:q-001"},
		AffectedPaths:  []string{"src/brain.py", "tests/test_brain.py"},
		AffectedFields: []string{"new_file_authorization"},
		Expected:       "Approve greenfield project files.",
		Actual:         "Approved. Create src/brain.py and tests/test_brain.py.",
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}
	contract := StampPlanAuthorityContract(&plan, deltas)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot:   root,
		Contract:        contract,
		AuthorityDeltas: deltas,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("new-file approval must not authorize verification commands for unrelated test paths"))
	{

		issues := StandaloneValidationIssues(err)
		gomega.Expect(standaloneValidationHasIssueKind(issues, "verification_command_unverified")).To(gomega.BeTrue(), fmt.Sprintf("expected typed verification issue, got err=%v issues=%+v", err, issues))
	}

})
var _ = ginkgo.It("validate standalone workspace rejects revision mention of exact verification command", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := runningExampleExistingPathPlanForTest()
	mkdirAndWriteFile(filepath.Join(root, "llm_brain", "query_handler.py"), "class QueryHandler:\n    pass\n")
	mkdirAndWriteFile(filepath.Join(root, "tests", "test_query_handler.py"), "def test_placeholder():\n    assert True\n")

	deltas := []PlanAuthorityDelta{{
		QuestionID:     "q-001",
		AuthorityKind:  "operator_approval",
		EvidenceRefs:   []string{"planner_question:q-001"},
		AffectedPaths:  []string{"tests/test_query_handler.py"},
		AffectedFields: []string{"verification_command"},
		Expected:       "Provide the exact verification command to use for this project.",
		Actual:         "Use pytest -q instead of pytest tests/test_query_handler.py.",
		Revision:       1,
		CreatedAt:      time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}
	contract := StampPlanAuthorityContract(&plan, deltas)

	err := ValidatePlanStandaloneWorkspaceContext(plan, StandaloneWorkspaceContext{
		WorkspaceRoot:   root,
		Contract:        contract,
		AuthorityDeltas: deltas,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("revision mention of exact rejected command must not authorize stale candidate"))
	{

		issues := StandaloneValidationIssues(err)
		gomega.Expect(standaloneValidationHasIssueKind(issues, "verification_command_unverified")).To(gomega.BeTrue(), fmt.Sprintf("expected typed verification issue, got err=%v issues=%+v", err, issues))
	}

})
var _ = ginkgo.It("greenfield prompt classifiers allow API definitions and TDD prose", func() {
	gomega.Expect(plannerCallNeedsSignatureEvidence("initially", "red")).To(gomega.BeFalse(), fmt.Sprint("TDD status prose initially(red) must not require source signature evidence"))

	grounding := newAtomPromptGrounding(FeatureAtomPlan{}, nil)
	grounding.addAuthorizedNewFile("llm_brain/working_memory.py")
	prompt := "implement the WorkingMemory class in llm_brain/working_memory.py with methods store(step_id: str, output: str) and retrieve(step_id: str) -> str"
	storeStart := strings.Index(prompt, "store(")
	gomega.Expect(storeStart < 0).To(gomega.BeFalse(), fmt.Sprintf("test prompt missing store call: %s", prompt))
	gomega.Expect(plannerCallIsNewAPIDefinition(prompt, storeStart, "store(step_id: str, output: str)", grounding)).To(gomega.BeTrue(), fmt.Sprintf("authorized greenfield method definition should not require existing-source signature proof"))

	registerPrompt := "Add a register_tool(name, description, parameters, func) method for tool/function calling."
	registerStart := strings.Index(registerPrompt, "register_tool(")
	gomega.Expect(registerStart).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("authorized greenfield add-method prompt should not require existing-source signature proof"))
	gomega.Expect(plannerCallIsNewAPIDefinition(registerPrompt, registerStart, "register_tool(name, description, parameters, func)", grounding)).To(gomega.BeTrue(), fmt.Sprintf("authorized greenfield add-method prompt should not require existing-source signature proof"))

	existingGrounding := newAtomPromptGrounding(FeatureAtomPlan{
		RequiredEditPaths: []string{"llm_brain/query_handler.py"},
	}, nil)
	existingPrompt := "Implement memory storage and retrieval in llm_brain/query_handler.py. Add methods: create_memory(content) -> MemoryEntry."
	createStart := strings.Index(existingPrompt, "create_memory(")
	gomega.Expect(createStart).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("explicit existing-file add-method prompt should be treated as new API definition"))
	gomega.Expect(plannerCallIsNewAPIDefinition(existingPrompt, createStart, "create_memory(content)", existingGrounding)).To(gomega.BeTrue(), fmt.Sprintf("explicit existing-file add-method prompt should be treated as new API definition"))

	evidenceOnlyGrounding := newAtomPromptGrounding(FeatureAtomPlan{
		EvidencePaths: []string{"llm_brain/query_handler.py"},
	}, nil)
	callPrompt := "Use accepted source localization from llm_brain/query_handler.py, then call create_memory(content) before dispatch."
	callStart := strings.Index(callPrompt, "create_memory(")
	gomega.Expect(callStart < 0).To(gomega.BeFalse(), fmt.Sprintf("test prompt missing helper call: %s", callPrompt))
	gomega.Expect(plannerCallIsNewAPIDefinition(callPrompt, callStart, "create_memory(content)", evidenceOnlyGrounding)).To(gomega.BeFalse(), fmt.Sprintf("evidence-only scope must not exempt existing helper call from source signature proof"))

	grounding.addAuthorizedNewFile("llm_brain/providers/base.py")
	gomega.Expect(grounding.pathIsDeclaredDirectory("llm_brain/providers")).To(gomega.BeTrue(), fmt.Sprint("directory mention should be in scope when declared files are inside it"))

	paths := repoPathsMentionedInText("parse <tool>calculator</tool><input>2+2</input> as example data for tool/function calling")
	gomega.Expect(len(paths)).To(gomega.Equal(0), fmt.Sprintf("XML-like examples and slash-separated prose alternatives should not be treated as repo paths: %#v", paths))

})
var _ = ginkgo.It("run uses answered planner question for standalone new file authorization", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Authorize new files",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequestedDecision: "Approve creating the helper and test files.",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/new_helper.go", "pkg/new_helper_test.go"},
		AffectedFields:    []string{"new_file_authorization"},
	}}

	calls := 0
	var implementorPrompt string

	result, err := Run(state, RunOptions{
		Prompt:                "Implement scoped atom from the approved spec.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		calls++

		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(plan), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			implementorPrompt = prompt
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("calls = %d, want planner + implementor", calls))
	gomega.Expect(result.State.Status).NotTo(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("answered operator authorization should satisfy missing new-file validation, blockers=%+v", result.State.Blockers))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers))
	gomega.Expect(result.State.OpenQuestions).To(gomega.HaveLen(1), fmt.Sprintf("expected question to be answered, got %+v", result.State.OpenQuestions))
	gomega.Expect(result.State.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("expected question to be answered, got %+v", result.State.OpenQuestions))

	var derived *PlanAuthorityDelta
	for i := range result.State.AuthorityDeltas {
		if result.State.AuthorityDeltas[i].QuestionID == "q-001" {
			derived = &result.State.AuthorityDeltas[i]
			break
		}
	}
	gomega.Expect(derived).NotTo(gomega.BeNil(), fmt.Sprintf("expected typed planner-question authority delta, got %+v", result.State.AuthorityDeltas))

	want := []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	gomega.Expect(slices.Equal(derived.AffectedPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("derived affected paths = %#v, want %#v", derived.AffectedPaths, want))
	gomega.Expect(slices.Equal(derived.AffectedFields, []string{"new_file_authorization"})).To(gomega.BeTrue(), fmt.Sprintf("derived affected fields = %#v, want new_file_authorization", derived.AffectedFields))
	{

		got := plancontract.AuthorizedNewFilePathsFromPrompt(implementorPrompt)
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v, want %#v", got, want))
	}

})
var _ = ginkgo.It("run revalidates approved rejected greenfield candidate without planner drift", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validGreenfieldBrainPlanForTest()

	state := NewState("session", "Can you work out an MVP to an effective \"llm brain\" sort of project?", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.RejectedPlanCandidates = []RejectedPlanCandidate{{
		Plan:       plan,
		PlanHash:   featurePlanHash(plan),
		Diagnostic: "missing standalone new-file authorization",
		EventType:  "idiom.feature_planner.standalone_context_blocked",
		RejectedAt: time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}}
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Approve or revise greenfield project scope",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "The workspace has no project files, so the standalone feature planner cannot infer new files, project structure, or verification commands safely.",
		RequestedDecision: "Approve creating src/brain.py and tests/test_brain.py with the planned Python pytest project setup.",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/brain.py", "tests/test_brain.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}

	var calls []string
	result, err := Run(state, RunOptions{
		Prompt:                state.PromptSummary,
		ResumeAnswer:          "Approved. Use Python with pytest. Create src/brain.py and tests/test_brain.py.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		calls = append(calls, taskFamily)
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)), fmt.Sprintf("approved rejected candidate should be revalidated before planner regeneration; prompt:\n%s", prompt))

		return testPlanExecuteCompletedJSONForPlanWithSourceSanity(plan, prompt), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers))
	{

		got := result.State.Plan.TopLevelScope.Objective
		gomega.Expect(got).To(gomega.Equal(plan.TopLevelScope.Objective), fmt.Sprintf("approved candidate drifted objective = %q, want %q", got, plan.TopLevelScope.Objective))
	}
	gomega.Expect(slices.Equal(calls, []string{string(autonomy.TaskFamilyFeaturePlannerImplementor)})).To(gomega.BeTrue(), fmt.Sprintf("calls = %#v, want only implementor dispatch", calls))
	gomega.Expect(result.Events).NotTo(gomega.HaveLen(0), fmt.Sprintf("missing candidate revalidation event: %+v", result.Events))
	gomega.Expect(result.Events[1].Type).To(gomega.Equal("idiom.feature_planner.rejected_candidate_revalidated"), fmt.Sprintf("missing candidate revalidation event: %+v", result.Events))

})
var _ = ginkgo.It("run uses answered new file authorization with standalone acceptance ID", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")
	plan := validPlan()

	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = []string{"SDD-COLLAPSE-EVENT-1"}
	plan.Atoms[0].RequiredEditPaths = []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}

	state := newQuestionStateForNewFiles([]string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"})

	result, err := Run(state, RunOptions{
		Prompt:                "Implement CollapseEvent helper.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(plan), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPlanAndPrompt(plan, prompt), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("state.Status = %q, want complete; blockers=%+v", result.State.Status, result.State.Blockers))

})
var _ = ginkgo.It("run records rejected candidate for missing SDD refs after authorization", func() {
	root := ginkgo.GinkgoT().TempDir()
	mkdirAndWriteFile(filepath.Join(root, "go.mod"), "module example.com/test\n")
	plan := validPlan()

	plan.SDDContract.Refs = nil
	plan.SDDContract.Acceptance = []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"}
	plan.Atoms[0].SDDRefs = nil
	plan.Atoms[0].RequiredEditPaths = []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/collapse_event_test.go"}

	state := newQuestionStateForNewFiles([]string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"})

	result, err := Run(state, RunOptions{
		Prompt:                "Implement CollapseEvent helper.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprint("invalid plan must not dispatch implementor"))

		return mustPlanJSON(plan), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning-repair", result.State.Status))
	gomega.Expect(len(result.State.RejectedPlanCandidates)).To(gomega.Equal(1), fmt.Sprintf("rejected candidates = %+v, want one", result.State.RejectedPlanCandidates))

	rejected := result.State.RejectedPlanCandidates[0]
	gomega.Expect(rejected.Plan.Atoms[0].ID).To(gomega.Equal(plan.Atoms[0].ID), fmt.Sprintf("rejected candidate did not preserve submitted plan/hash: %+v", rejected))
	gomega.Expect(rejected.PlanHash).NotTo(gomega.Equal(""), fmt.Sprintf("rejected candidate did not preserve submitted plan/hash: %+v", rejected))
	gomega.Expect(strings.Contains(rejected.Diagnostic, "missing sdd_refs")).To(gomega.BeTrue(), fmt.Sprintf("diagnostic should name missing sdd_refs: %+v", rejected))

	lastEvent := result.Events[len(result.Events)-1]
	gomega.Expect(lastEvent.Type).To(gomega.Equal("idiom.feature_planner.tdd_pre_execution_blocked"), fmt.Sprintf("event should include rejected plan hash fields: %+v", result.Events))
	gomega.Expect(lastEvent.Fields["rejected_plan_sha256"]).NotTo(gomega.Equal(""), fmt.Sprintf("event should include rejected plan hash fields: %+v", result.Events))

})

func newQuestionStateForNewFiles(paths []string) FeatureState {
	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Authorize new files",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequestedDecision: "Approve creating the helper and test files.",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     paths,
		AffectedFields:    []string{"new_file_authorization"},
	}}
	return state
}

var _ = ginkgo.It("run does not compile requested decision paths as new file authorization", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"pkg/new_helper_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"pkg/new_helper_test.go"}

	state := NewState("session", "implement scoped atom", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Authorize new files",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequestedDecision: "Authorize creating missing files: pkg/new_helper.go and pkg/new_helper_test.go.",
		RequiredBefore:    "plan_approval",
	}}

	result, err := Run(state, RunOptions{
		Prompt:                "Approved.",
		ResumeAnswer:          "Approved.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(plan), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning_repair", result.State.Status))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file")).To(gomega.BeTrue(), fmt.Sprintf("expected new-file authorization blocker, got %+v", result.State.Blockers))

	for _, delta := range result.State.AuthorityDeltas {
		gomega.Expect(len(delta.AffectedPaths)).NotTo(gomega.BeNumerically(">", 0), fmt.Sprintf("requested_decision prose must not mint typed path authority, got %+v", result.State.AuthorityDeltas))
		gomega.Expect(delta.AffectedFields).NotTo(gomega.ContainElement("new_file_authorization"), fmt.Sprintf("requested_decision prose must not mint typed path authority, got %+v", result.State.AuthorityDeltas))

	}
})
var _ = ginkgo.It("run does not compile generic resume answer as new file authorization", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"project/scripts/economy/score_breakdown.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}

	state := NewState("session", "add score breakdown", "", 3, time.Unix(1, 0).UTC())
	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Clarify implementation approval",
		AuthorityRequired: "operator_approval",
		BlockingClaim:     "Missing files require operator authorization.",
		RequiredBefore:    "plan_approval",
	}}

	result, err := Run(state, RunOptions{
		Prompt:                "Continue.",
		ResumeAnswer:          "Approved to add score breakdown to the existing tooltip.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(plan), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning_repair", result.State.Status))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file")).To(gomega.BeTrue(), fmt.Sprintf("expected new-file authorization blocker, got %+v", result.State.Blockers))

	for _, delta := range result.State.AuthorityDeltas {
		gomega.Expect(delta.AffectedFields).NotTo(gomega.ContainElement("new_file_authorization"), fmt.Sprintf("generic resume answer must not mint path-level new-file authority, got %+v", result.State.AuthorityDeltas))
		gomega.Expect(len(delta.AffectedPaths)).NotTo(gomega.BeNumerically(">", 0), fmt.Sprintf("generic resume answer must not mint path-level new-file authority, got %+v", result.State.AuthorityDeltas))

	}
})
var _ = ginkgo.It("run rejects explicit operator prompt for standalone new file authorization", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"internal/greeting/greeting.go", "internal/greeting/greeting_test.go"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"internal/greeting/greeting_test.go"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"internal/greeting/greeting_test.go"}

	state := NewState("session", "add greeting package", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Add an internal/greeting package with Hello function.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
		Now:                   time.Unix(2, 0).UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(plan), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning_repair", result.State.Status))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file")).To(gomega.BeTrue(), fmt.Sprintf("expected new-file authorization blocker, got %+v", result.State.Blockers))
	gomega.Expect(len(result.State.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("operator prompt prose must not mint new-file authority, got %+v", result.State.AuthorityDeltas))

})
var _ = ginkgo.It("run rejects generic add prompt for standalone implementation new file", func() {
	root := ginkgo.GinkgoT().TempDir()
	plan := validPlan()

	plan.Atoms[0].RequiredEditPaths = []string{"project/scripts/economy/score_breakdown.gd"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"project/scripts/economy/score_breakdown_test.gd"}

	state := NewState("session", "add score breakdown", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{
		Prompt:                "Add score breakdown to the existing tooltip.",
		WorkspaceRoot:         root,
		MaxPlanningIterations: 3,
	}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(plan), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningRepair), fmt.Sprintf("state.Status = %q, want planning_repair", result.State.Status))
	gomega.Expect(strings.Contains(strings.Join(result.State.Blockers, "\n"), "not authorized as a new file")).To(gomega.BeTrue(), fmt.Sprintf("expected new-file authorization blocker, got %+v", result.State.Blockers))
	gomega.Expect(len(result.State.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("generic add prompt must not mint new-file authority, got %+v", result.State.AuthorityDeltas))

})
var _ = ginkgo.It("dispatch records plan execute lane family", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(prompt string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprintf("task family = %q, want feature_planner_implementor callback", taskFamily))

		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
	})
	gomega.Expect(events).To(gomega.HaveLen(3), fmt.Sprintf("events = %+v, want dispatch, complete, release-ready", events))
	gomega.Expect(events[2].Type).To(gomega.Equal("idiom.feature_planner.release_ready"), fmt.Sprintf("events = %+v, want dispatch, complete, release-ready", events))

	dispatch := events[0]
	gomega.Expect(dispatch.Type).To(gomega.Equal("idiom.feature_planner.atom_dispatched"), fmt.Sprintf("unexpected dispatch event: %+v", dispatch))
	gomega.Expect(dispatch.ActiveRunDeltas).To(gomega.HaveLen(1), fmt.Sprintf("unexpected dispatch event: %+v", dispatch))
	gomega.Expect(dispatch.ActiveRunDeltas[0].LaneFamily).To(gomega.Equal("plan-execute"), fmt.Sprintf("lane family = %q, want plan-execute", dispatch.ActiveRunDeltas[0].LaneFamily))

	complete := events[1]
	{
		got := fmt.Sprint(complete.Fields["lane_family"])
		gomega.Expect(got).To(gomega.Equal("plan-execute"), fmt.Sprintf("completion lane family = %q, want plan-execute; event=%+v", got, complete))
	}
	{

		got := fmt.Sprint(complete.Fields["nested_status"])
		gomega.Expect(got).To(gomega.Equal("completed"), fmt.Sprintf("nested status = %q, want completed; event=%+v", got, complete))
	}

})
var _ = ginkgo.It("dispatch uses plan execute safe run ID for unsafe atom ID", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	plan := validPlan()

	plan.Atoms[0].ID = "atoms/1.3c"
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	var prompt string

	events := DispatchReadyAtoms(&state, 1, func(gotPrompt string, _ string) (string, error) {
		prompt = gotPrompt
		return testPlanExecuteCompletedJSONForPrompt(gotPrompt), nil
	})
	gomega.Expect(events).To(gomega.HaveLen(3), fmt.Sprintf("events = %+v, want dispatch, complete, release-ready", events))
	gomega.Expect(events[2].Type).To(gomega.Equal("idiom.feature_planner.release_ready"), fmt.Sprintf("events = %+v, want dispatch, complete, release-ready", events))

	runID := events[0].ActiveRunDeltas[0].LaneSessionID
	gomega.Expect(runID).NotTo(gomega.Equal(""), fmt.Sprintf("run id = %q, want session-safe id", runID))
	gomega.Expect(strings.ContainsAny(runID, "/.")).To(gomega.BeFalse(), fmt.Sprintf("run id = %q, want session-safe id", runID))
	gomega.Expect(strings.Contains(prompt, "Plan-execute session id: "+runID)).To(gomega.BeTrue(), fmt.Sprintf("implementor prompt missing bound session id %q:\n%s", runID, prompt))
	gomega.Expect(state.Atoms[0].LastRunID).To(gomega.Equal(runID), fmt.Sprintf("atom LastRunID = %q, want %q", state.Atoms[0].LastRunID, runID))

})
var _ = ginkgo.It("dispatch rejects raw source satisfied without typed plan execute evidence", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprintf("task family = %q, want feature_planner_implementor callback", taskFamily))

		return "Decision: source-satisfied\n- internal/feature.go: already implemented.", nil
	})
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("state.Status = %q, want recovery-open; events=%+v", state.Status, events))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open", state.Atoms[0].State))
	gomega.Expect(events).To(gomega.HaveLen(2), fmt.Sprintf("events = %+v, want dispatch then unverified completion rejection", events))
	gomega.Expect(events[1].Type).To(gomega.Equal("idiom.feature_planner.unverified_completion_rejected"), fmt.Sprintf("events = %+v, want dispatch then unverified completion rejection", events))
	gomega.Expect(state.Atoms[0].Blockers).NotTo(gomega.HaveLen(0), fmt.Sprintf("missing typed plan-execute evidence blocker: %+v", state.Atoms[0].Blockers))
	gomega.Expect(state.Atoms[0].Blockers[0]).To(gomega.ContainSubstring("typed \"plan_execute_completed\""), fmt.Sprintf("missing typed plan-execute evidence blocker: %+v", state.Atoms[0].Blockers))

})
var _ = ginkgo.It("dispatch rejects raw prose without typed plan execute evidence", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprintf("task family = %q, want feature_planner_implementor callback", taskFamily))

		return "Implemented atom.", nil
	})
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("state.Status = %q, want recovery-open; events=%+v", state.Status, events))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open", state.Atoms[0].State))
	gomega.Expect(events).To(gomega.HaveLen(2), fmt.Sprintf("events = %+v, want dispatch then unverified completion rejection", events))
	gomega.Expect(events[1].Type).To(gomega.Equal("idiom.feature_planner.unverified_completion_rejected"), fmt.Sprintf("events = %+v, want dispatch then unverified completion rejection", events))
	gomega.Expect(state.Atoms[0].Blockers).NotTo(gomega.HaveLen(0), fmt.Sprintf("missing typed plan-execute evidence blocker: %+v", state.Atoms[0].Blockers))
	gomega.Expect(state.Atoms[0].Blockers[0]).To(gomega.ContainSubstring("raw text without typed \"plan_execute_completed\""), fmt.Sprintf("missing typed plan-execute evidence blocker: %+v", state.Atoms[0].Blockers))

})
var _ = ginkgo.It("dispatch rejects unsupported implementor JSON tool", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprintf("task family = %q, want feature_planner_implementor callback", taskFamily))

		return `{"tool":"plan_execute_complete","summary":"atom implemented"}`, nil
	})
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("state.Status = %q, want recovery-open; events=%+v", state.Status, events))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open", state.Atoms[0].State))
	gomega.Expect(state.Atoms[0].Blockers).NotTo(gomega.HaveLen(0), fmt.Sprintf("missing unsupported-tool blocker: %+v", state.Atoms[0].Blockers))
	gomega.Expect(state.Atoms[0].Blockers[0]).To(gomega.ContainSubstring("unsupported completion tool"), fmt.Sprintf("missing unsupported-tool blocker: %+v", state.Atoms[0].Blockers))

})
var _ = ginkgo.Describe("dispatch rejects malformed or empty implementor JSON", func() {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "malformed", raw: `{"summary":`, want: "malformed JSON completion envelope"},
		{name: "empty tool", raw: `{"summary":"implemented"}`, want: "JSON without a supported tool"},
		{name: "incomplete question summary", raw: `{"question_summary":"blocked"}`, want: "JSON without a supported tool"},
		{name: "incomplete blocking claim", raw: `{"blocking_claim":"needs decision"}`, want: "JSON without a supported tool"},
		{name: "empty tool question-shaped", raw: `{"question_summary":"blocked","blocking_claim":"needs decision"}`, want: "JSON without a supported tool"},
	}
	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
			ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

			events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
				gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprintf("task family = %q, want feature_planner_implementor callback", taskFamily))

				return tt.raw, nil
			})
			gomega.Expect(state.Status).To(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("state.Status = %q, want recovery-open; events=%+v", state.Status, events))
			gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open", state.Atoms[0].State))
			gomega.Expect(state.Atoms[0].Blockers).NotTo(gomega.HaveLen(0), fmt.Sprintf("missing blocker %q: %+v", tt.want, state.Atoms[0].Blockers))
			gomega.Expect(state.Atoms[0].Blockers[0]).To(gomega.ContainSubstring(tt.want), fmt.Sprintf("missing blocker %q: %+v", tt.want, state.Atoms[0].Blockers))

		})
	}
})
var _ = ginkgo.It("dispatch stops after blocked atom without synthetic sibling runs", func() {
	plan := validPlan()
	second := plan.Atoms[0]

	second.ID = "atom-2"
	second.AtomID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels2.go", "internal/labels/labels_test.go"}
	second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
	second.TestFirst.CoverageGateEventID = "evt-tdd-2"
	second.PlannedTDDSnapshot.SnapshotID = "snapshot-evidence-0002"
	second.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
	second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
	plan.Atoms = append(plan.Atoms, second)

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	var implementorCalls int

	events := DispatchReadyAtoms(&state, 5, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprintf("task family = %q, want feature_planner_implementor callback", taskFamily))

		implementorCalls++

		return `{"tool":"question_for_planner","question_summary":"first atom blocked","blocking_claim":"needs product decision"}`, nil
	})
	gomega.Expect(implementorCalls).To(gomega.Equal(1), fmt.Sprintf("expected dispatch to stop after first blocked child, got %d calls", implementorCalls))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateQuestionForPlanner), fmt.Sprintf("first atom state = %q, want question-for-planner", state.Atoms[0].State))
	gomega.Expect(state.Atoms[1].State).To(gomega.Equal(AtomStateQueued), fmt.Sprintf("sibling atom should remain queued, got %q", state.Atoms[1].State))

	for _, event := range events {
		gomega.Expect(event).NotTo(gomega.And(
			gomega.HaveField("AtomID", "atom-2"),
			gomega.HaveField("Type", "idiom.feature_planner.atom_dispatched"),
		), fmt.Sprintf("sibling atom must not be synthetically dispatched after blocker: %+v", events))

	}
})
var _ = ginkgo.It("dispatch stops after pre dispatch TDD blocker", func() {
	plan := validPlan()
	second := plan.Atoms[0]

	second.ID = "atom-2"
	second.AtomID = "atom-2"
	second.RequiredEditPaths = []string{"internal/labels/labels2.go", "internal/labels/labels_test.go"}
	second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
	second.TestFirst.CoverageGateEventID = "evt-tdd-2"
	second.PlannedTDDSnapshot.SnapshotID = "snapshot-evidence-0002"
	second.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
	second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
	plan.Atoms = append(plan.Atoms, second)

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	state.TDDPreExecutionEvidence = nil

	var implementorCalls int

	events := DispatchReadyAtoms(&state, 5, func(prompt string, _ string) (string, error) {
		implementorCalls++
		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
	})
	gomega.Expect(implementorCalls).To(gomega.Equal(0), fmt.Sprintf("implementor must not run after pre-dispatch TDD blocker, got %d call(s)", implementorCalls))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateBlockedMissingTDD), fmt.Sprintf("first atom state = %q, want blocked-missing-tdd", state.Atoms[0].State))
	gomega.Expect(state.Atoms[1].State).To(gomega.Equal(AtomStateQueued), fmt.Sprintf("sibling atom should remain queued, got %q", state.Atoms[1].State))

	for _, event := range events {
		gomega.Expect(event).NotTo(gomega.And(
			gomega.HaveField("AtomID", "atom-2"),
			gomega.HaveField("Type", "idiom.feature_planner.atom_dispatched"),
		), fmt.Sprintf("sibling atom must not dispatch after pre-dispatch blocker: %+v", events))

	}
})
var _ = ginkgo.It("dispatch does not complete plan execute question", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	events := DispatchReadyAtoms(&state, 1, func(_ string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerImplementor)), fmt.Sprintf("task family = %q, want feature_planner_implementor callback", taskFamily))

		return `{"tool":"question_for_planner","lane_family":"plan-execute","question_summary":"Plan-execute could not produce a plan artifact.","blocking_claim":"plan-execute did not complete the atom implementation contract"}`, nil
	})
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want planning-question-open; events=%+v", state.Status, events))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("open questions = %+v", state.OpenQuestions))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateQuestionForPlanner), fmt.Sprintf("atom state = %q, want question-for-planner", state.Atoms[0].State))

	for _, event := range events {
		gomega.Expect(event.Type).NotTo(gomega.Equal("idiom.feature_planner.atom_completed"), fmt.Sprintf("blocked plan-execute output must not complete atom: %+v", events))

	}
})
var _ = ginkgo.It("apply implementor output question prefix opens planner question", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	atom := &state.Atoms[0]

	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.ActiveRuns = []ActiveRun{{RunID: "atom-1-run-1", AtomID: atom.ID, State: ActiveRunStateRunning}}

	events := ApplyImplementorOutput(&state, atom, "<<QUESTION>>: Plan-execute could not produce a plan artifact.")
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("question prefix should open planner question, state=%q atom=%q events=%+v", state.Status, atom.State, events))
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateQuestionForPlanner), fmt.Sprintf("question prefix should open planner question, state=%q atom=%q events=%+v", state.Status, atom.State, events))
	gomega.Expect(state.OpenQuestions).To(gomega.HaveLen(1), fmt.Sprintf("unexpected open questions: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[0].QuestionSummary).To(gomega.ContainSubstring("Plan-execute"), fmt.Sprintf("unexpected open questions: %+v", state.OpenQuestions))

})
var _ = ginkgo.It("dispatch blocks when TDD evidence ledger is missing", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Plan = validPlan()
	state.Status = FeatureStateDispatching
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: state.Plan.Atoms[0],
		State:           AtomStateQueued,
	}}

	events := DispatchReadyAtoms(&state, 1, func(_ string, _ string) (string, error) {
		ginkgo.Fail(fmt.Sprint("implementor must not run without harness-owned TDD evidence"))

		return "", nil
	})
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateBlockedMissingTDD), fmt.Sprintf("atom state = %q, want blocked missing TDD", state.Atoms[0].State))
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.tdd_pre_execution_blocked"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(containsString(state.Blockers, "tdd_pre_execution_evidence.evt-tdd-1")).To(gomega.BeTrue(), fmt.Sprintf("missing ledger blocker not recorded: %+v", state.Blockers))

})
var _ = ginkgo.It("dispatch waits for atom dependencies", func() {
	plan := validPlan()
	dependent := plan.Atoms[0]

	dependent.ID = "atom-dependent"
	dependent.AtomID = "atom-dependent"
	dependent.Dependencies = []string{"atom-base"}
	dependent.TestFirst.PreExecutionEventIDs = []string{"evt-dependent"}
	dependent.TestFirst.CoverageGateEventID = "evt-dependent"
	dependent.PlannedTDDSnapshot.SnapshotID = "snapshot-dependent-0001"
	dependent.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigest
	dependent.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-dependent"

	base := plan.Atoms[0]

	base.ID = "atom-base"
	base.AtomID = "atom-base"
	base.TestFirst.PreExecutionEventIDs = []string{"evt-base"}
	base.TestFirst.CoverageGateEventID = "evt-base"
	base.PlannedTDDSnapshot.SnapshotID = "snapshot-base-0001"
	base.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
	base.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-base"
	plan.Atoms = []FeatureAtomPlan{dependent, base}

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: plan}, time.Unix(2, 0).UTC())

	var prompts []string

	events := DispatchReadyAtoms(&state, 1, func(prompt string, taskFamily string) (string, error) {
		prompts = append(prompts, prompt)
		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
	})
	gomega.Expect(prompts).To(gomega.HaveLen(1), fmt.Sprintf("expected only dependency atom to dispatch first, prompts=%#v", prompts))
	gomega.Expect(prompts[0]).To(gomega.ContainSubstring(`"id": "atom-base"`), fmt.Sprintf("expected only dependency atom to dispatch first, prompts=%#v", prompts))
	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(AtomStateQueued), fmt.Sprintf("dependent atom should remain queued until dependency completes: %+v", state.Atoms[0]))
	gomega.Expect(state.Atoms[1].State).To(gomega.Equal(AtomStateComplete), fmt.Sprintf("base atom should complete: %+v events=%+v", state.Atoms[1], events))

})
var _ = ginkgo.Describe("validate durable plan rejects invalid dependencies", func() {
	ginkgo.It("unknown", func() {
		plan := validPlan()

		plan.Atoms[0].Dependencies = []string{"missing"}

		err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unknown dependency error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("unknown atom"), fmt.Sprintf("expected unknown dependency error, got %v", err))

	})
	ginkgo.It("cycle", func() {
		plan := validPlan()
		second := plan.Atoms[0]

		second.ID = "atom-2"
		second.AtomID = "atom-2"
		second.TestFirst.PreExecutionEventIDs = []string{"evt-tdd-2"}
		second.TestFirst.CoverageGateEventID = "evt-tdd-2"
		second.PlannedTDDSnapshot.SnapshotID = "snapshot-evidence-0002"
		second.PlannedTDDSnapshot.SnapshotDigest = validTDDSnapshotDigestTwo
		second.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-2"
		plan.Atoms[0].Dependencies = []string{"atom-2"}
		second.Dependencies = []string{"atom-1"}
		plan.Atoms = append(plan.Atoms, second)

		err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected dependency cycle error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("dependency cycle"), fmt.Sprintf("expected dependency cycle error, got %v", err))

	})
})
var _ = ginkgo.It("validate durable plan accepts full SHA256 TDD snapshot digest", func() {
	plan := validPlan()

	plan.Atoms[0].PlannedTDDSnapshot.SnapshotDigest = "sha256:" + strings.Repeat("a", 64)
	{

		err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("valid lowercase SHA-256 TDD snapshot digest should validate: %v", err))
	}

})
var _ = ginkgo.Describe("validate durable plan rejects placeholder TDD snapshot values", func() {
	tests := []struct {
		name     string
		snapshot PlannedTDDSnapshot
		want     []string
	}{
		{
			name: "ellipsis digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:...",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "ellipsis placeholder"},
		},
		{
			name: "placeholder digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:placeholder0000000000000000000000000000000000000000000000000",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "placeholder token \"placeholder\""},
		},
		{
			name: "short digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:abc123",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "got 6 characters after sha256:"},
		},
		{
			name: "non hex digest",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-evidence-0001",
				SnapshotDigest: "sha256:" + strings.Repeat("0", 63) + "g",
			},
			want: []string{"planned_tdd_snapshot.snapshot_digest", "non-lowercase-hex"},
		},
		{
			name: "fake snapshot id",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snap-tdd-1",
				SnapshotDigest: validTDDSnapshotDigest,
			},
			want: []string{"planned_tdd_snapshot.snapshot_id", "snap-tdd-*"},
		},
		{
			name: "ellipsis snapshot id",
			snapshot: PlannedTDDSnapshot{
				SnapshotID:     "snapshot-...",
				SnapshotDigest: validTDDSnapshotDigest,
			},
			want: []string{"planned_tdd_snapshot.snapshot_id", "ellipsis placeholder"},
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			plan := validPlan()

			plan.Atoms[0].PlannedTDDSnapshot.SnapshotID = tt.snapshot.SnapshotID
			plan.Atoms[0].PlannedTDDSnapshot.SnapshotDigest = tt.snapshot.SnapshotDigest

			err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected placeholder TDD snapshot validation error"))

			for _, want := range tt.want {
				gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

			}
		})
	}
})
var _ = ginkgo.It("validate durable plan rejects mismatched TDD snapshot event ID", func() {
	plan := validPlan()

	plan.Atoms[0].PlannedTDDSnapshot.CreatedBeforeDispatchEventID = "evt-tdd-missing"

	err := ValidateDurablePlanWithContract(plan, plancontract.PlanExecutionContract{})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected mismatched TDD snapshot event validation error"))

	for _, want := range []string{"mismatched durable pre-dispatch TDD fields", "planned_tdd_snapshot.created_before_dispatch_event_id"} {
		gomega.Expect(strings.Contains(err.Error(), want)).To(gomega.BeTrue(), fmt.Sprintf("validation error missing %q: %v", want, err))

	}
})
var _ = ginkgo.It("run keeps implementor question as planner repair state", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	result, err := Run(state, RunOptions{Prompt: "build feature", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == string(autonomy.TaskFamilyFeaturePlannerPlanner) {
			return mustPlanJSON(validPlan()), nil
		}

		return `{"tool":"question_for_planner","question_summary":"Which label text should win?","blocking_claim":"ambiguous target"}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("state.Status = %q, want question-open", result.State.Status))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("open questions = %+v", result.State.OpenQuestions))
	gomega.Expect(result.State.Atoms[0].State).To(gomega.Equal(AtomStateQuestionForPlanner), fmt.Sprintf("atom state = %q", result.State.Atoms[0].State))
	gomega.Expect(result.State.OpenQuestions[0].QuestionKind).NotTo(gomega.Equal(""), fmt.Sprintf("planner question was not normalized: %+v", result.State.OpenQuestions[0]))
	gomega.Expect(result.State.OpenQuestions[0].AuthorityRequired).NotTo(gomega.Equal(""), fmt.Sprintf("planner question was not normalized: %+v", result.State.OpenQuestions[0]))

})
var _ = ginkgo.It("implementor atom JSON does not open planner question or complete", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	atom := &state.Atoms[0]

	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.ActiveRuns = []ActiveRun{{
		RunID:         "atom-1-run-1",
		AtomID:        atom.ID,
		LaneSessionID: "atom-1-run-1",
		State:         ActiveRunStateRunning,
	}}

	raw, err := json.Marshal(atom.FeatureAtomPlan)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal atom: %v", err))

	events := ApplyImplementorOutput(&state, atom, string(raw))
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open; events=%+v", atom.State, events))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(0), fmt.Sprintf("atom JSON should not open planner question: %+v", state.OpenQuestions))
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.unverified_completion_rejected"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(containsString(state.Continuation.CompletedAtoms, atom.ID)).To(gomega.BeFalse(), fmt.Sprintf("atom JSON echo must not complete the atom: %+v", state.Continuation))

})
var _ = ginkgo.It("apply implementor output rejects unobserved edit and test claims", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	atom := &state.Atoms[0]

	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.Status = FeatureStateDispatching
	state.ActiveRuns = []ActiveRun{{
		RunID:         "atom-1-run-1",
		AtomID:        atom.ID,
		LaneSessionID: "atom-1-run-1",
		State:         ActiveRunStateRunning,
	}}

	events := ApplyImplementorOutput(&state, atom, `Implemented & TDD-verified.

**Paths Edited**
- internal/labels/labels.go
- internal/labels/labels_test.go

**Verification**
- go test ./internal/labels/... passed locally`)
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open; events=%+v", atom.State, events))
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("feature status = %q, want recovery-open", state.Status))
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.unverified_completion_rejected"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(len(state.ActiveRuns)).To(gomega.Equal(0), fmt.Sprintf("active run should be folded after rejected completion: %+v", state.ActiveRuns))
	gomega.Expect(containsString(state.Continuation.CompletedAtoms, atom.ID)).To(gomega.BeFalse(), fmt.Sprintf("rejected atom must not be recorded complete: %+v", state.Continuation))
	gomega.Expect(containsString(state.Continuation.BlockedAtoms, atom.ID)).To(gomega.BeTrue(), fmt.Sprintf("rejected atom should be resumable as blocked: %+v", state.Continuation))

})
var _ = ginkgo.It("apply implementor output rejects missing observed verification commands", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtom(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	delete(payload, "verification_commands")

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(trustPlanExecuteCompletionForTest(payload)))

	assertRejectedPlanExecuteCompletion(state, atom, events, "missing observed verification command")
	gomega.Expect(state.ReleaseReadiness.Status).NotTo(gomega.Equal(ReleaseReadinessStatusReady), fmt.Sprintf("missing observed verification commands must not become release-ready: %+v", state.ReleaseReadiness))

})
var _ = ginkgo.It("observed verification commands use canonical evidence key", func() {
	atom := plancontract.AtomExecutionContract{
		RequiredTests: []plancontract.RequiredTest{{Command: "python -m pytest tests/test_brain.py"}},
		TDDObligations: []plancontract.TDDObligation{{
			Command: "python -m pytest tests/test_brain.py",
		}},
	}
	completion := planExecuteCompletion{
		VerificationCommands: []plancontract.VerificationCommand{{
			Argv: []string{"python", "-m", "pytest", "tests/test_brain.py"},
		}},
	}
	{

		missing := missingObservedVerificationCommands(atom, completion)
		gomega.Expect(len(missing) > 0).To(gomega.BeFalse(), fmt.Sprintf("canonical argv evidence should satisfy required command, missing %+v", missing))
	}

})
var _ = ginkgo.It("apply implementor output accepts observed plan execute completion", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	atom := &state.Atoms[0]

	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	contractID, protocolHash := stampCompletionTestAtomRuntime(&state, atom)
	state.ActiveRuns = []ActiveRun{{
		RunID:                "atom-1-run-1",
		AtomID:               atom.ID,
		LaneSessionID:        "atom-1-run-1",
		LaneFamily:           "plan-execute",
		InvocationContractID: contractID,
		RuntimeEnvelopeID:    protocolHash,
		State:                ActiveRunStateRunning,
	}}

	events := ApplyImplementorOutput(&state, atom, testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom))
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateComplete), fmt.Sprintf("atom state = %q, want complete; blockers=%+v", atom.State, atom.Blockers))
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("feature state = %q, want complete", state.Status))
	gomega.Expect(events).To(gomega.HaveLen(2), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.atom_completed"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[1].Type).To(gomega.Equal("idiom.feature_planner.release_ready"), fmt.Sprintf("unexpected events: %+v", events))
	{

		got := fmt.Sprint(events[0].Fields["progress_path"])
		gomega.Expect(got).To(gomega.Equal(".idiom/plan-execute/session/state.json"), fmt.Sprintf("progress_path = %q, want delegated plan-execute path", got))
	}
	{

		got := fmt.Sprint(events[0].Fields["nested_review_status"])
		gomega.Expect(got).To(gomega.Equal("skipped"), fmt.Sprintf("nested review status = %q, want skipped", got))
	}
	{

		got := events[0].Fields["review_repair_skipped"]
		gomega.Expect(got).To(gomega.Equal(true), fmt.Sprintf("review_repair_skipped = %v, want true", got))
	}
	gomega.Expect(len(state.VerificationClosures)).To(gomega.Equal(1), fmt.Sprintf("verification closures = %+v, want one closure", state.VerificationClosures))

	closure := state.VerificationClosures[0]
	gomega.Expect(closure.PlanArtifactID).To(gomega.Equal(state.Plan.Artifact.ArtifactID), fmt.Sprintf("closure not bound to approved plan artifact: closure=%+v artifact=%+v approval=%+v", closure, state.Plan.Artifact, state.Plan.Approval))
	gomega.Expect(closure.PlanDigest).To(gomega.Equal(state.Plan.Artifact.Digest), fmt.Sprintf("closure not bound to approved plan artifact: closure=%+v artifact=%+v approval=%+v", closure, state.Plan.Artifact, state.Plan.Approval))
	gomega.Expect(closure.ApprovalEventID).To(gomega.Equal(state.Plan.Approval.EventID), fmt.Sprintf("closure not bound to approved plan artifact: closure=%+v artifact=%+v approval=%+v", closure, state.Plan.Artifact, state.Plan.Approval))
	gomega.Expect(closure.Status).To(gomega.Equal(VerificationClosureStatusPassed), fmt.Sprintf("closure status = %q, want passed", closure.Status))
	gomega.Expect(len(closure.TDDCommandResults)).NotTo(gomega.Equal(0), fmt.Sprintf("closure missing TDD command results: %+v", closure))
	gomega.Expect(len(closure.Verification)).NotTo(gomega.Equal(0), fmt.Sprintf("closure missing typed verification checks: %+v", closure))
	{

		got := fmt.Sprint(events[0].Fields[featureplannercontract.EventFieldVerificationClosureEventID])
		gomega.Expect(got).To(gomega.Equal(closure.EventID), fmt.Sprintf("event closure id = %q, want %q", got, closure.EventID))
	}
	gomega.Expect(state.ReleaseReadiness.Status).To(gomega.Equal(ReleaseReadinessStatusReady), fmt.Sprintf("release readiness not bound to closed artifact: %+v", state.ReleaseReadiness))
	gomega.Expect(state.ReleaseReadiness.PlanArtifactID).To(gomega.Equal(state.Plan.Artifact.ArtifactID), fmt.Sprintf("release readiness not bound to closed artifact: %+v", state.ReleaseReadiness))
	gomega.Expect(state.ReleaseReadiness.PlanDigest).To(gomega.Equal(state.Plan.Artifact.Digest), fmt.Sprintf("release readiness not bound to closed artifact: %+v", state.ReleaseReadiness))
	gomega.Expect(state.ReleaseReadiness.ApprovalEventID).To(gomega.Equal(state.Plan.Approval.EventID), fmt.Sprintf("release readiness not bound to closed artifact: %+v", state.ReleaseReadiness))
	gomega.Expect(containsString(state.ReleaseReadiness.VerificationClosureEventIDs, closure.EventID)).To(gomega.BeTrue(), fmt.Sprintf("release readiness not bound to closed artifact: %+v", state.ReleaseReadiness))
	{

		got := fmt.Sprint(events[1].Fields[featureplannercontract.EventFieldReleaseStatus])
		gomega.Expect(got).To(gomega.Equal("ready"), fmt.Sprintf("release event status = %q, want ready; fields=%+v", got, events[1].Fields))
	}
	{

		got := fmt.Sprint(events[1].Fields[featureplannercontract.EventFieldPlanDigest])
		gomega.Expect(got).To(gomega.Equal(state.Plan.Artifact.Digest), fmt.Sprintf("release event digest = %q, want %q", got, state.Plan.Artifact.Digest))
	}

})
var _ = ginkgo.It("apply implementor output rejects nonzero plan execute exit code", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	payload["exit_code"] = 1
	trustPlanExecuteCompletionForTest(payload)

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute reported non-zero exit code")
})
var _ = ginkgo.It("apply implementor output rejects scope only plan execute completion", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	payload["scope_contract_id"] = "child-local-scope"
	payload["scope_contract_hash"] = "child-local-scope-hash"
	delete(payload, "contract_id")
	delete(payload, "contract_hash")
	delete(payload, "snapshot_hash")
	trustPlanExecuteCompletionForTest(payload)

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "plan-execute terminal completion is not correlated to the active plan contract snapshot")
})
var _ = ginkgo.It("apply implementor output rejects identityless plan execute completion", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	delete(payload, "contract_id")
	delete(payload, "contract_hash")
	delete(payload, "snapshot_hash")
	delete(payload, "scope_contract_id")
	delete(payload, "scope_contract_hash")
	trustPlanExecuteCompletionForTest(payload)

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "plan-execute terminal completion is not correlated to the active plan contract snapshot")
})
var _ = ginkgo.It("apply implementor output rejects conflicting parent plan execute identity", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	payload["contract_id"] = "wrong-parent-contract"
	payload["snapshot_hash"] = atom.RuntimeEnvelope.ProtocolHash

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "plan-execute terminal completion conflicts with the active plan contract snapshot")
})
var _ = ginkgo.It("apply implementor output rejects skipped review completion missing source sanity", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	delete(payload, "source_sanity")

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute terminal validation failed")
	gomega.Expect(strings.Contains(events[0].Message, "source_sanity_missing:internal/labels/labels.go")).To(gomega.BeTrue(), fmt.Sprintf("rejection did not name missing source_sanity: %+v", events[0]))

})
var _ = ginkgo.It("apply implementor output accepts legacy contract hash as snapshot alias", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	delete(payload, "snapshot_hash")

	payload["contract_hash"] = atom.RuntimeEnvelope.ProtocolHash
	trustPlanExecuteCompletionForTest(payload)

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateComplete), fmt.Sprintf("atom state = %q, want complete; blockers=%+v events=%+v", atom.State, atom.Blockers, events))
	{

		got := fmt.Sprint(events[0].Fields["snapshot_hash"])
		gomega.Expect(got).To(gomega.Equal(atom.RuntimeEnvelope.ProtocolHash), fmt.Sprintf("snapshot_hash = %q, want legacy contract_hash normalized to active snapshot; fields=%+v", got, events[0].Fields))
	}
	{

		_, ok := events[0].Fields["contract_hash"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("contract_hash should not remain a parallel authority field; fields=%+v", events[0].Fields))
	}

})
var _ = ginkgo.It("apply implementor output rejects conflicting legacy contract hash alias", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtomWithReviewSkipped(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	payload["snapshot_hash"] = atom.RuntimeEnvelope.ProtocolHash
	payload["contract_hash"] = "wrong-legacy-snapshot"

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "plan-execute terminal completion conflicts with the active plan contract snapshot")
})
var _ = ginkgo.It("apply implementor output accepts nested plan execute completion status", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, testNestedPlanExecuteCompletedJSONForAtom(*atom))
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateComplete), fmt.Sprintf("atom state = %q, want complete; blockers=%+v events=%+v", atom.State, atom.Blockers, events))
	gomega.Expect(events).To(gomega.HaveLen(2), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.atom_completed"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[1].Type).To(gomega.Equal("idiom.feature_planner.release_ready"), fmt.Sprintf("unexpected events: %+v", events))
	{

		got := fmt.Sprint(events[0].Fields["nested_status"])
		gomega.Expect(got).To(gomega.Equal("completed"), fmt.Sprintf("nested status = %q, want completed; fields=%+v", got, events[0].Fields))
	}
	{

		got := fmt.Sprint(events[0].Fields["nested_phase"])
		gomega.Expect(got).To(gomega.Equal("review_repair"), fmt.Sprintf("nested phase = %q, want review_repair; fields=%+v", got, events[0].Fields))
	}

})
var _ = ginkgo.It("apply implementor output rejects nested nonzero plan execute exit code", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testNestedPlanExecuteCompletedJSONForAtom(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	nested, ok := payload["plan_execute"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("payload missing nested plan_execute: %+v", payload))

	nested["exit_code"] = 1
	trustPlanExecuteCompletionForTest(nested)

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute reported non-zero exit code")
})
var _ = ginkgo.It("apply implementor output rejects missing acceptance evidence i ds", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testNestedPlanExecuteCompletedJSONForAtom(*atom)), &payload)

	nested := payload["plan_execute"].(map[string]any)
	delete(nested, "acceptance_evidence_ids")

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "missing acceptance_evidence_ids")
})
var _ = ginkgo.It("apply implementor output rejects untrusted acceptance evidence i ds", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testNestedPlanExecuteCompletedJSONForAtom(*atom)), &payload)

	nested := payload["plan_execute"].(map[string]any)

	nested["completion_source"] = "headless_plan_execute_returned"
	nested["acceptance_evidence_trusted"] = true

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "acceptance_evidence_ids are not backed by trusted plan-execute terminal evidence")
})
var _ = ginkgo.It("apply implementor output uses nested plan execute completion decision class", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","plan_execute":{"lane_family":"plan-execute","status":"completed","terminal_class":"recovery_required","outcome":"review_degraded","phase":"review_repair","review_status":"degraded","progress_path":".idiom/plan-execute/session/state.json"}}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute terminal class requires recovery")
	{

		got := fmt.Sprint(events[0].Fields["terminal_class"])
		gomega.Expect(got).To(gomega.Equal("recovery_required"), fmt.Sprintf("terminal_class = %q, want recovery_required; fields=%+v", got, events[0].Fields))
	}

})
var _ = ginkgo.It("apply implementor output rejects invalid nested plan execute terminal record", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}
	{

		err := json.Unmarshal([]byte(testNestedPlanExecuteCompletedJSONForAtom(*atom)), &payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode payload: %v", err))
	}

	nested, ok := payload["plan_execute"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("payload missing nested plan_execute: %+v", payload))

	nested["terminal_class"] = string(plancontract.PlanExecuteTerminalClassSuccess)
	nested["status"] = "not-a-plan-execute-status"
	nested["phase"] = "not-a-plan-execute-phase"
	nested["failure_kind"] = "not-a-plan-execute-failure"

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute terminal record invalid")
})
var _ = ginkgo.It("apply implementor output preserves plan execute source satisfied atom state", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, testPlanExecuteSourceSatisfiedJSONForAtom(*atom))
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateSourceSatisfied), fmt.Sprintf("atom state = %q, want source-satisfied; blockers=%+v events=%+v", atom.State, atom.Blockers, events))
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("feature state = %q, want complete", state.Status))
	gomega.Expect(events).To(gomega.HaveLen(2), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.atom_source_satisfied"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[1].Type).To(gomega.Equal("idiom.feature_planner.release_ready"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(state.VerificationClosures).To(gomega.HaveLen(1))
	expectedEvidence := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(atom.RequiredEditPaths))
	for _, path := range atom.RequiredEditPaths {
		expectedEvidence = append(expectedEvidence, plancontract.SourceSatisfiedEvidenceRef{
			Path: path,
			Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
		})
	}
	gomega.Expect(state.VerificationClosures[0].SourceSatisfiedEvidence).To(gomega.Equal(expectedEvidence))
	gomega.Expect(ValidateDurableState(state)).To(gomega.Succeed())
	{

		got := fmt.Sprint(events[0].Fields["outcome"])
		gomega.Expect(got).To(gomega.Equal("source_satisfied"), fmt.Sprintf("outcome = %q, want source_satisfied; fields=%+v", got, events[0].Fields))
	}
	{

		got := events[0].AtomTo
		gomega.Expect(got).To(gomega.Equal(AtomStateSourceSatisfied), fmt.Sprintf("event AtomTo = %q, want source-satisfied", got))
	}

})
var _ = ginkgo.It("apply implementor output rejects path only plan execute source satisfied", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	delete(payload, "source_satisfied_evidence")

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "source-satisfied completion missing typed source_satisfied_evidence refs")
})
var _ = ginkgo.It("apply implementor output rejects source satisfied with changed paths", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["changed_paths"] = atom.RequiredEditPaths

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "source-satisfied completion must not include changed_paths")
})
var _ = ginkgo.It("apply implementor output rejects source satisfied with failed terminal evidence", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["source_sanity"] = []map[string]any{{
		"kind":   "source_sanity",
		"path":   atom.RequiredEditPaths[0],
		"status": "failed",
		"error":  "source sanity failed",
	}}

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute terminal validation failed")
	gomega.Expect(strings.Contains(events[0].Message, "source_sanity_failed:"+atom.RequiredEditPaths[0])).To(gomega.BeTrue(), fmt.Sprintf("rejection did not name failed source_sanity path: %+v", events[0]))

})
var _ = ginkgo.It("apply implementor output rejects out of scope source satisfied path", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["source_satisfied_evidence_paths"] = append([]string{"pkg/out_of_scope.go"}, atom.RequiredEditPaths...)
	payload["source_satisfied_evidence"] = testPlanExecuteSourceSatisfiedEvidenceRefs([]string{"pkg/out_of_scope.go"})

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "source-satisfied completion includes paths outside required edit scope")
})
var _ = ginkgo.It("apply implementor output rejects source satisfied without required scope", func() {
	state, atom := implementingAtomForCompletionTest()

	state.Plan.Atoms[0].RequiredEditPaths = nil
	state.Plan.Atoms[0].AllowedEditPaths = []string{"pkg/allowed.go"}
	state.Plan.Atoms[0].TestFirst.TestArtifactPaths = nil
	state.Plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = nil
	StampPlanAuthorityContract(&state.Plan, state.AuthorityDeltas)

	atom.FeatureAtomPlan = state.Plan.Atoms[0]

	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["source_satisfied_evidence_paths"] = []string{"pkg/allowed.go"}
	payload["source_satisfied_evidence"] = testPlanExecuteSourceSatisfiedEvidenceRefs([]string{"pkg/allowed.go"})
	payload["required_edit_paths"] = []string{}

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "source-satisfied completion missing required edit scope")
})
var _ = ginkgo.It("apply implementor output rejects unsupported source satisfied evidence kind", func() {
	state, atom := implementingAtomForCompletionTest()
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(*atom)), &payload)
	payload["source_satisfied_evidence"] = []map[string]string{{"path": atom.RequiredEditPaths[0], "kind": "path_name_only"}}

	events := ApplyImplementorOutput(&state, atom, mustJSONMap(payload))

	assertRejectedPlanExecuteCompletion(state, atom, events, "source-satisfied completion missing typed source_satisfied_evidence refs")
})
var _ = ginkgo.It("apply implementor output accepts outcome only source satisfied completion", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, testPlanExecuteOutcomeOnlySourceSatisfiedJSONForAtom(*atom))
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateSourceSatisfied), fmt.Sprintf("atom state = %q, want source-satisfied; blockers=%+v events=%+v", atom.State, atom.Blockers, events))
	gomega.Expect(events).To(gomega.HaveLen(2), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.atom_source_satisfied"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[1].Type).To(gomega.Equal("idiom.feature_planner.release_ready"), fmt.Sprintf("unexpected events: %+v", events))
	{

		got := fmt.Sprint(events[0].Fields["nested_status"])
		gomega.Expect(got).To(gomega.Equal("source-satisfied"), fmt.Sprintf("nested_status = %q, want source-satisfied; fields=%+v", got, events[0].Fields))
	}

})
var _ = ginkgo.It("apply implementor output rejects plan execute completion without nested status", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","progress_path":".idiom/plan-execute/session/state.json","summary":"nested output without typed status"}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, "missing required nested plan-execute status")
	{

		got := fmt.Sprint(events[0].Fields["nested_status"])
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("nested_status = %q, want empty observable field", got))
	}

})
var _ = ginkgo.It("apply implementor output rejects degraded plan execute review", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"degraded","phase":"review_repair","review_status":"degraded","progress_path":".idiom/plan-execute/session/state.json","summary":"review repair degraded"}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute status is not success-shaped")
	{

		got := fmt.Sprint(events[0].Fields["nested_status"])
		gomega.Expect(got).To(gomega.Equal("degraded"), fmt.Sprintf("nested_status = %q, want degraded", got))
	}
	{

		got := fmt.Sprint(events[0].Fields["nested_review_status"])
		gomega.Expect(got).To(gomega.Equal("degraded"), fmt.Sprintf("nested_review_status = %q, want degraded", got))
	}

})
var _ = ginkgo.It("apply implementor output rejects plan execute model error", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","phase":"review_repair","review_status":"review_model_error","failure_kind":"review_model_error","failure_reason":"model returned malformed review"}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, `nested plan-execute terminal record invalid: invalid plan-execute terminal completion review status "review_model_error": review_model_error - model returned malformed review`)
	{

		got := fmt.Sprint(events[0].Fields["nested_failure_kind"])
		gomega.Expect(got).To(gomega.Equal("review_model_error"), fmt.Sprintf("nested_failure_kind = %q, want review_model_error", got))
	}
	gomega.Expect(containsString(state.Continuation.CompletedAtoms, atom.ID)).To(gomega.BeFalse(), fmt.Sprintf("model-error nested completion must not record completed atom: %+v", state.Continuation))

})
var _ = ginkgo.It("apply implementor output promotes nested plan execute failure detail to blocker", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: target.txt source evidence unavailable","progress_path":".idiom/plan-execute/sessions/pe.json"}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute reported failure details: planner_blocked - <<QUESTION>>: target.txt source evidence unavailable")
	gomega.Expect(atom.Blockers).NotTo(gomega.HaveLen(0), fmt.Sprintf("atom blocker should preserve nested failure detail, blockers=%+v", atom.Blockers))
	gomega.Expect(atom.Blockers[0]).To(gomega.ContainSubstring("target.txt source evidence unavailable"), fmt.Sprintf("atom blocker should preserve nested failure detail, blockers=%+v", atom.Blockers))
	{

		got := fmt.Sprint(events[0].Fields["nested_failure_kind"])
		gomega.Expect(got).To(gomega.Equal("planner_blocked"), fmt.Sprintf("nested_failure_kind = %q, want planner_blocked", got))
	}
	{

		got := fmt.Sprint(events[0].Fields["progress_path"])
		gomega.Expect(got).To(gomega.Equal(".idiom/plan-execute/sessions/pe.json"), fmt.Sprintf("progress_path = %q, want child progress state", got))
	}

})
var _ = ginkgo.It("apply implementor output preserves plan execute retry next action", func() {
	state, atom := implementingAtomForCompletionTest()
	state.ActiveRuns = []ActiveRun{{RunID: "atom-1-run-1", AtomID: atom.ID, LaneSessionID: "atom-1-run-1", State: ActiveRunStateRunning}}
	atom.LastRunID = "atom-1-run-1"

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.","next_valid_action":"retry_plan_execute","progress_path":".idiom/plan-execute/sessions/pe.json"}`)
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("retryable plan-execute completion should become retry-pending event, got state=%s atom=%s events=%+v", state.Status, atom.State, events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.plan_execute_retry_pending"), fmt.Sprintf("retryable plan-execute completion should become retry-pending event, got state=%s atom=%s events=%+v", state.Status, atom.State, events))
	gomega.Expect(state.Status).NotTo(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("retryable plan-execute completion must not open product recovery: state=%s atom=%s", state.Status, atom.State))
	gomega.Expect(atom.State).NotTo(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("retryable plan-execute completion must not open product recovery: state=%s atom=%s", state.Status, atom.State))
	gomega.Expect(state.Continuation.ResumeAction).To(gomega.Equal("retry_plan_execute"), fmt.Sprintf("continuation = %+v, want retry_plan_execute for %s", state.Continuation, atom.ID))
	gomega.Expect(state.Continuation.ResumeAtomID).To(gomega.Equal(atom.ID), fmt.Sprintf("continuation = %+v, want retry_plan_execute for %s", state.Continuation, atom.ID))
	gomega.Expect(len(state.ActiveRuns)).To(gomega.Equal(0), fmt.Sprintf("failed active run should be folded so durable reconciliation does not replay it: %+v", state.ActiveRuns))
	{

		got := fmt.Sprint(events[0].Fields["next_valid_action"])
		gomega.Expect(got).To(gomega.Equal("retry_plan_execute"), fmt.Sprintf("next_valid_action = %q, want retry_plan_execute; fields=%+v", got, events[0].Fields))
	}
	{

		got := fmt.Sprint(events[0].Fields["nested_failure_kind"])
		gomega.Expect(got).To(gomega.Equal("planner_blocked"), fmt.Sprintf("nested_failure_kind = %q, want planner_blocked", got))
	}

})
var _ = ginkgo.It("run resumes plan execute retry pending atom", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	state, atom := implementingAtomForCompletionTest()

	ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.","next_valid_action":"retry_plan_execute","progress_path":".idiom/plan-execute/sessions/pe.json"}`)

	var taskFamilies []string
	var prompts []string
	result, err := Run(state, RunOptions{Prompt: "resume", MaxAtoms: 1}, func(prompt string, taskFamily string) (string, error) {
		taskFamilies = append(taskFamilies, taskFamily)
		prompts = append(prompts, prompt)

		return `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: still refreshing","next_valid_action":"retry_plan_execute","progress_path":".idiom/plan-execute/sessions/pe-retry.json"}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(slices.Equal(taskFamilies, []string{string(autonomy.TaskFamilyFeaturePlannerImplementor)})).To(gomega.BeTrue(), fmt.Sprintf("task families = %#v, want only implementor retry dispatch", taskFamilies))
	gomega.Expect(prompts).To(gomega.HaveLen(1), fmt.Sprintf("retry dispatch should rebuild implementor prompt for atom %s, got %#v", atom.ID, prompts))
	gomega.Expect(prompts[0]).To(gomega.ContainSubstring(atom.ID), fmt.Sprintf("retry dispatch should rebuild implementor prompt for atom %s, got %#v", atom.ID, prompts))
	gomega.Expect(len(result.Events)).NotTo(gomega.BeNumerically("<", 2), fmt.Sprintf("events should start with retry resume then dispatch, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.plan_execute_retry_resumed"), fmt.Sprintf("events should start with retry resume then dispatch, got %+v", result.Events))
	gomega.Expect(result.State.Continuation.ResumeAction).To(gomega.Equal("retry_plan_execute"), fmt.Sprintf("retry failure should remain retry-pending after resumed attempt, continuation=%+v", result.State.Continuation))
	gomega.Expect(result.State.Continuation.ResumeAtomID).To(gomega.Equal(atom.ID), fmt.Sprintf("retry failure should remain retry-pending after resumed attempt, continuation=%+v", result.State.Continuation))

})
var _ = ginkgo.It("apply implementor output prefers nested review repair failure over planning fallback", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"plan_artifact_missing","failure_reason":"planning/no plan artifact","progress_path":".idiom/plan-execute/sessions/fallback.json","plan_execute":{"lane_family":"plan-execute","status":"blocked","phase":"review_repair","review_status":"blocked","failure_kind":"review_repair_blocked","failure_reason":"review repair remained blocked","progress_path":".idiom/plan-execute/sessions/active.json"}}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute reported failure details: review_repair_blocked - review repair remained blocked")
	{

		got := fmt.Sprint(events[0].Fields["nested_failure_kind"])
		gomega.Expect(got).To(gomega.Equal("review_repair_blocked"), fmt.Sprintf("nested_failure_kind = %q, want review_repair_blocked; fields=%+v", got, events[0].Fields))
	}
	{

		got := fmt.Sprint(events[0].Fields["nested_phase"])
		gomega.Expect(got).To(gomega.Equal("review_repair"), fmt.Sprintf("nested_phase = %q, want review_repair; fields=%+v", got, events[0].Fields))
	}
	{

		got := fmt.Sprint(events[0].Fields["progress_path"])
		gomega.Expect(got).To(gomega.Equal(".idiom/plan-execute/sessions/active.json"), fmt.Sprintf("progress_path = %q, want active progress state; fields=%+v", got, events[0].Fields))
	}
	gomega.Expect(events[0].Message).NotTo(gomega.ContainSubstring("plan_artifact_missing"), fmt.Sprintf("stale planning fallback leaked into rejection message: %q", events[0].Message))
	gomega.Expect(events[0].Message).NotTo(gomega.ContainSubstring("planning/no plan artifact"), fmt.Sprintf("stale planning fallback leaked into rejection message: %q", events[0].Message))

})
var _ = ginkgo.It("apply implementor output rejects implicit skipped review", func() {
	state, atom := implementingAtomForCompletionTest()

	events := ApplyImplementorOutput(&state, atom, `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"completed","phase":"complete","review_status":"skipped"}`)

	assertRejectedPlanExecuteCompletion(state, atom, events, "nested plan-execute skipped review is not explicitly marked by design")
})

func implementingAtomForCompletionTest() (FeatureState, *FeatureAtomState) {
	ginkgo.GinkgoHelper()

	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())

	atom := &state.Atoms[0]

	atom.State = AtomStateImplementing
	atom.LastRunID = "atom-1-run-1"
	state.Status = FeatureStateDispatching
	contractID, protocolHash := stampCompletionTestAtomRuntime(&state, atom)
	state.ActiveRuns = []ActiveRun{{
		RunID:                "atom-1-run-1",
		AtomID:               atom.ID,
		LaneSessionID:        "atom-1-run-1",
		LaneFamily:           "plan-execute",
		InvocationContractID: contractID,
		RuntimeEnvelopeID:    protocolHash,
		State:                ActiveRunStateRunning,
	}}

	return state, atom
}

func stampCompletionTestAtomRuntime(state *FeatureState, atom *FeatureAtomState) (string, string) {
	contract := StampPlanAuthorityContract(&state.Plan, state.AuthorityDeltas)
	atomContract, _ := planAuthorityContractAtom(contract, atom.ID)
	atom.RuntimeEnvelope.ContractID = atomContract.Scope.ContractID
	atom.RuntimeEnvelope.ProtocolHash = contract.ContractHash

	return atom.RuntimeEnvelope.ContractID, atom.RuntimeEnvelope.ProtocolHash
}

func assertRejectedPlanExecuteCompletion(state FeatureState, atom *FeatureAtomState, events []Event, wantMessage string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(atom.State).To(gomega.Equal(AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open; events=%+v", atom.State, events))
	gomega.Expect(state.Status).To(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("feature state = %q, want recovery-open", state.Status))
	gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.plan_execute_completion_rejected"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(strings.Contains(events[0].Message, wantMessage)).To(gomega.BeTrue(), fmt.Sprintf("message = %q, want %q", events[0].Message, wantMessage))
	gomega.Expect(len(state.ActiveRuns)).To(gomega.Equal(0), fmt.Sprintf("active run should be folded after rejected completion: %+v", state.ActiveRuns))
	gomega.Expect(containsString(state.Continuation.CompletedAtoms, atom.ID)).To(gomega.BeFalse(), fmt.Sprintf("rejected atom must not be recorded complete: %+v", state.Continuation))
	gomega.Expect(containsString(state.Continuation.BlockedAtoms, atom.ID)).To(gomega.BeTrue(), fmt.Sprintf("rejected atom should be blocked: %+v", state.Continuation))

}

var _ = ginkgo.It("run reconciles active runs before resume active run", func() {
	state, atom := implementingAtomForCompletionTest()

	result, err := Run(state, RunOptions{
		Prompt:                "resume",
		MaxPlanningIterations: 3,
		ReconcileActiveRuns: func(state *FeatureState) []Event {
			return ApplyImplementorOutput(state, &state.Atoms[0], `{"tool":"plan_execute_completed","lane_family":"plan-execute","status":"failed","phase":"planning","failure_kind":"planner_blocked","failure_reason":"<<QUESTION>>: target.txt source evidence unavailable","progress_path":".idiom/plan-execute/sessions/pe.json"}`)
		},
	}, func(_ string, taskFamily string) (string, error) {
		ginkgo.Fail(fmt.Sprintf("runner should not be called after active-run reconciliation, taskFamily=%s", taskFamily))

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateRecoveryOpen), fmt.Sprintf("feature state = %q, want recovery-open", result.State.Status))
	gomega.Expect(len(result.State.ActiveRuns)).To(gomega.Equal(0), fmt.Sprintf("active run should be folded by reconciliation: %+v", result.State.ActiveRuns))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("expected reconciled terminal event, got %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.plan_execute_completion_rejected"), fmt.Sprintf("expected reconciled terminal event, got %+v", result.Events))
	gomega.Expect(containsString(result.State.Continuation.BlockedAtoms, atom.ID)).To(gomega.BeTrue(), fmt.Sprintf("atom should be blocked after reconciliation: %+v", result.State.Continuation))

})
var _ = ginkgo.It("run resumes dispatching state without replacing plan", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	events := ApplyPlannerOutput(&state, ParsedPlannerOutput{Kind: featureplannercontract.ToolSubmitFeaturePlan, Plan: validPlan()}, time.Unix(2, 0).UTC())
	gomega.Expect(len(events)).NotTo(gomega.Equal(0), fmt.Sprint("expected initial plan events"))

	state.Atoms[0].State = AtomStateQueued

	result, err := Run(state, RunOptions{Prompt: "resume", MaxPlanningIterations: 3}, func(prompt string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).NotTo(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)), fmt.Sprint("resume from dispatching must not ask planner for a fresh plan"))

		return testPlanExecuteCompletedJSONForPrompt(prompt), nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStateComplete), fmt.Sprintf("status = %q, want complete", result.State.Status))
	gomega.Expect(result.State.Plan.Summary).To(gomega.Equal("Add feature"), fmt.Sprintf("plan was replaced: %+v", result.State.Plan))

})
var _ = ginkgo.It("run rejects plain answer for non operator question", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.Blockers = []string{
		`planner question "q-source" requires typed accepted_source authority answer`,
		"unrelated blocker remains",
	}
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionKind:      "missing_spec",
		QuestionSummary:   "Which accepted source authorizes this behavior?",
		BlockingClaim:     "Implementation needs accepted source authority.",
		RequestedDecision: "Provide the accepted source.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	result, err := Run(state, RunOptions{Prompt: "Just do it", ResumeAnswer: "Just do it"}, func(_ string, _ string) (string, error) {
		ginkgo.Fail(fmt.Sprint("plain answer without required authority must not continue to planner"))

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("status = %q, want planning question", result.State.Status))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected events: %+v", result.Events))
	gomega.Expect(result.Events[0].Type).To(gomega.Equal("idiom.feature_planner.planner_question_answer_rejected"), fmt.Sprintf("unexpected events: %+v", result.Events))

})
var _ = ginkgo.It("run plan revision supersedes required authority question", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.Blockers = []string{
		`planner question "q-source" requires typed accepted_source authority answer`,
		`atom 0 prompt includes concrete helper call "BuildThing(arg)" without source-verified signature evidence; require exact signature/source verification or ask_planner_question before dispatch`,
	}
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionKind:      "missing_spec",
		QuestionSummary:   "Which accepted source authorizes this behavior?",
		BlockingClaim:     "Implementation needs accepted source authority.",
		RequestedDecision: "Provide the accepted source.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	revision := "Can you add more detail to the plan for persistent memory and multi-repo support?"
	var plannerPrompt string
	result, err := Run(state, RunOptions{
		Prompt:             "build feature\n\nOperator plan revision instruction:\n" + revision,
		ResumeAnswer:       revision,
		ResumePlanRevision: true,
	}, func(prompt string, taskFamily string) (string, error) {
		gomega.Expect(taskFamily).To(gomega.Equal(string(autonomy.TaskFamilyFeaturePlannerPlanner)), fmt.Sprintf("unexpected task family %q", taskFamily))

		plannerPrompt = prompt
		return `{"tool":"ask_planner_question","question_summary":"Confirm persistent memory and multi-repo plan additions","blocking_claim":"The operator asked to revise the plan, so the revised MVP boundary needs confirmation before approval.","requested_decision":"Confirm the plan should add persistent memory storage, repository-scoped namespaces, and explicit multi-repo isolation tests.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["llm_brain/query_handler.py","tests/test_query_handler.py"],"affected_fields":["requirements_scope","test_obligations"]}`, nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(plannerPrompt).To(gomega.ContainSubstring("Operator plan revision instruction:"), fmt.Sprintf("planner prompt did not include revision instruction:\n%s", plannerPrompt))
	gomega.Expect(plannerPrompt).To(gomega.ContainSubstring(revision), fmt.Sprintf("planner prompt did not include revision instruction:\n%s", plannerPrompt))
	gomega.Expect(len(result.State.OpenQuestions)).To(gomega.Equal(2), fmt.Sprintf("expected old question plus revised planner question, got %+v", result.State.OpenQuestions))

	oldQuestion := result.State.OpenQuestions[0]
	gomega.Expect(oldQuestion.Status).To(gomega.Equal("answered"), fmt.Sprintf("old authority question was not superseded as a plan revision: %+v", oldQuestion))
	gomega.Expect(oldQuestion.Answer).To(gomega.Equal(revision), fmt.Sprintf("old authority question was not superseded as a plan revision: %+v", oldQuestion))
	gomega.Expect(oldQuestion.AnswerSourceKind).To(gomega.Equal("plan_revision"), fmt.Sprintf("old authority question was not superseded as a plan revision: %+v", oldQuestion))
	gomega.Expect(len(result.State.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("plan revision must not mint accepted-source authority: %+v", result.State.AuthorityDeltas))
	gomega.Expect(containsString(result.State.Blockers, `planner question "q-source" requires typed accepted_source authority answer`)).To(gomega.BeFalse(), fmt.Sprintf("superseded question blocker should be cleared: %+v", result.State.Blockers))
	gomega.Expect(containsString(result.State.Blockers, `atom 0 prompt includes concrete helper call "BuildThing(arg)" without source-verified signature evidence; require exact signature/source verification or ask_planner_question before dispatch`)).To(gomega.BeTrue(), fmt.Sprintf("source-fact blocker should remain until source facts or a revised plan resolves it: %+v", result.State.Blockers))
	gomega.Expect(result.State.OpenQuestions[1].Status).To(gomega.Equal("open"), fmt.Sprintf("revised planner question was not opened: %+v", result.State.OpenQuestions[1]))
	gomega.Expect(result.State.OpenQuestions[1].AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("revised planner question was not opened: %+v", result.State.OpenQuestions[1]))

	var sawSuperseded bool
	for _, event := range result.Events {
		if event.Type == featureplannercontract.EventName(featureplannercontract.EventPlannerQuestionSupersededByRevision) {
			sawSuperseded = true
			{
				_, ok := event.Fields["revision"]
				gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("supersession event should not include raw operator revision text: %+v", event.Fields))
			}

		}
	}
	gomega.Expect(sawSuperseded).To(gomega.BeTrue(), fmt.Sprintf("missing supersession event: %+v", result.Events))

})
var _ = ginkgo.It("run accepts typed answer for required authority", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.Blockers = []string{
		`atom 0 prompt includes concrete helper call "BuildThing(arg)" without source-verified signature evidence; require exact signature/source verification or ask_planner_question before dispatch`,
	}
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionKind:      "missing_spec",
		QuestionSummary:   "Which accepted source authorizes this behavior?",
		BlockingClaim:     "Implementation needs accepted source authority.",
		RequestedDecision: "Provide the accepted source.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	typedAnswer := `{"answer":"Use docs/feature.md acceptance criteria. BuildThing is the accepted helper signature.","answer_source_kind":"accepted_source","evidence_refs":["docs/feature.md#acceptance"],"source_facts":[{"path":"docs/feature.md","symbol":"BuildThing","signature":"Result BuildThing(Input arg)"}]}`
	var implementorPrompt string

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(validPlan()), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			implementorPrompt = prompt
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.OpenQuestions[0].AnswerSourceKind).To(gomega.Equal("accepted_source"), fmt.Sprintf("typed authority was not recorded: %+v", result.State.OpenQuestions[0]))
	gomega.Expect(result.State.OpenQuestions[0].EvidenceRefs).To(gomega.HaveLen(1), fmt.Sprintf("typed authority was not recorded: %+v", result.State.OpenQuestions[0]))
	gomega.Expect(containsString(result.State.Blockers, `planner question "q-source" requires typed accepted_source authority answer`)).To(gomega.BeFalse(), fmt.Sprintf("accepted typed answer should clear stale question blocker: %+v", result.State.Blockers))

	for _, blocker := range result.State.Blockers {
		gomega.Expect(strings.Contains(blocker, "BuildThing")).To(gomega.BeFalse(), fmt.Sprintf("accepted source facts should clear covered source signature blocker: %+v", result.State.Blockers))

	}

	for _, want := range []string{
		"Accepted source authority constraints:",
		"These constraints are authoritative over the atom prose.",
		"signature: Result BuildThing(Input arg)",
	} {
		gomega.Expect(strings.Contains(implementorPrompt, want)).To(gomega.BeTrue(), fmt.Sprintf("live implementor prompt missing accepted-source constraint %q:\n%s", want, implementorPrompt))

	}
	gomega.Expect(result.State.Status).NotTo(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("typed answer should allow planning to continue"))

})
var _ = ginkgo.It("clear answered accepted source blockers preserves uncovered helper calls", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())
	covered := `atom 0 prompt includes concrete helper call "BuildThing(arg)" without source-verified signature evidence; require exact signature/source verification or ask_planner_question before dispatch`
	uncovered := `atom 0 prompt includes concrete helper call "OtherHelper(x)" without source-verified signature evidence; require exact signature/source verification or ask_planner_question before dispatch`
	state.Blockers = []string{covered, uncovered, "unrelated blocker"}
	state.Atoms = []FeatureAtomState{{
		FeatureAtomPlan: validPlan().Atoms[0],
		Blockers:        []string{covered, uncovered, "atom unrelated blocker"},
	}}
	delta := PlanAuthorityDelta{
		QuestionID:     "q-source",
		AuthorityKind:  "accepted_source",
		EvidenceRefs:   []string{"docs/feature.md#acceptance"},
		AffectedFields: []string{"source_localization"},
		SourceFacts: []plancontract.SourceAuthorityFact{{
			Path:      "docs/feature.md",
			Symbol:    "BuildThing",
			Signature: "Result BuildThing(Input arg)",
		}},
		Revision:  1,
		CreatedAt: time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
	}

	clearAnsweredAcceptedSourceBlockers(&state, delta)
	gomega.Expect(containsString(state.Blockers, covered)).To(gomega.BeFalse(), fmt.Sprintf("covered source signature blocker should be cleared: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(containsString(state.Atoms[0].Blockers, covered)).To(gomega.BeFalse(), fmt.Sprintf("covered source signature blocker should be cleared: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(containsString(state.Blockers, uncovered)).To(gomega.BeTrue(), fmt.Sprintf("uncovered source signature blocker should remain: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(containsString(state.Atoms[0].Blockers, uncovered)).To(gomega.BeTrue(), fmt.Sprintf("uncovered source signature blocker should remain: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(containsString(state.Blockers, "unrelated blocker")).To(gomega.BeTrue(), fmt.Sprintf("unrelated blockers should remain: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(containsString(state.Atoms[0].Blockers, "atom unrelated blocker")).To(gomega.BeTrue(), fmt.Sprintf("unrelated blockers should remain: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))

})
var _ = ginkgo.It("run rejects accepted source typed answer without source facts", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionKind:      "missing_spec",
		QuestionSummary:   "Which accepted source authorizes this behavior?",
		BlockingClaim:     "Implementation needs accepted source authority.",
		RequestedDecision: "Provide the accepted source.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	typedAnswer := `{"answer":"Use docs/feature.md acceptance criteria.","answer_source_kind":"accepted_source","evidence_refs":["docs/feature.md#acceptance"]}`

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(_ string, _ string) (string, error) {
		ginkgo.Fail(fmt.Sprint("accepted_source answer without source_facts must not continue to planner"))

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("status = %q, want planning question", result.State.Status))
	gomega.Expect(len(result.State.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("missing source_facts must not mint authority deltas: %+v", result.State.AuthorityDeltas))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected rejection event: %+v", result.Events))
	gomega.Expect(result.Events[0].Message).To(gomega.ContainSubstring(`missing source_facts`), fmt.Sprintf("unexpected rejection event: %+v", result.Events))

})
var _ = ginkgo.It("run rejects typed answer with mismatched authority kind", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-source",
		QuestionKind:      "missing_spec",
		QuestionSummary:   "Which accepted source authorizes this behavior?",
		BlockingClaim:     "Implementation needs accepted source authority.",
		RequestedDecision: "Provide the accepted source.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	typedAnswer := `{"answer":"Use docs/feature.md acceptance criteria.","answer_source_kind":"accepted_source","authority_kind":"operator_approval","evidence_refs":["docs/feature.md#acceptance"]}`

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(_ string, _ string) (string, error) {
		ginkgo.Fail(fmt.Sprint("mismatched authority_kind must not continue to planner"))

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.Status).To(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("status = %q, want planning question", result.State.Status))
	gomega.Expect(len(result.State.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("mismatched authority_kind must not mint authority deltas: %+v", result.State.AuthorityDeltas))
	gomega.Expect(result.Events).To(gomega.HaveLen(1), fmt.Sprintf("unexpected rejection event: %+v", result.Events))
	gomega.Expect(result.Events[0].Message).To(gomega.ContainSubstring(`requires authority_kind "accepted_source"`), fmt.Sprintf("unexpected rejection event: %+v", result.Events))

})
var _ = ginkgo.It("clear answered planner question blockers preserves unrelated blockers", func() {
	state := FeatureState{
		Blockers: []string{
			`planner question "q-source" requires typed accepted_source authority answer`,
			`planner question "q-other" requires typed accepted_source authority answer`,
			"unrelated blocker remains",
		},
		Atoms: []FeatureAtomState{{
			Blockers: []string{
				`planner question "q-source" requires typed accepted_source authority answer`,
				"atom blocker remains",
			},
		}},
	}

	clearAnsweredPlannerQuestionBlockers(&state, "q-source")
	gomega.Expect(containsString(state.Blockers, `planner question "q-source" requires typed accepted_source authority answer`)).To(gomega.BeFalse(), fmt.Sprintf("answered question blocker was not cleared: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))
	gomega.Expect(containsString(state.Atoms[0].Blockers, `planner question "q-source" requires typed accepted_source authority answer`)).To(gomega.BeFalse(), fmt.Sprintf("answered question blocker was not cleared: state=%+v atom=%+v", state.Blockers, state.Atoms[0].Blockers))

	for _, want := range []string{
		`planner question "q-other" requires typed accepted_source authority answer`,
		"unrelated blocker remains",
	} {
		gomega.Expect(containsString(state.Blockers, want)).To(gomega.BeTrue(), fmt.Sprintf("unrelated state blocker %q should remain: %+v", want, state.Blockers))

	}
	gomega.Expect(containsString(state.Atoms[0].Blockers, "atom blocker remains")).To(gomega.BeTrue(), fmt.Sprintf("unrelated atom blocker should remain: %+v", state.Atoms[0].Blockers))

})
var _ = ginkgo.It("run accepts typed answer for atom local evidence question", func() {
	state := NewState("session", "build feature", "", 3, time.Unix(1, 0).UTC())

	state.Status = FeatureStatePlanningQuestion
	state.PlanningLoop.State = FeatureStatePlanningQuestion
	state.OpenQuestions = []PlannerQuestion{{
		ID:                "q-local",
		QuestionKind:      "missing_source_localization",
		QuestionSummary:   "Which source anchors prove this atom is current?",
		BlockingClaim:     "Planning needs exact local source evidence.",
		RequestedDecision: "Provide exact local source anchors.",
		AuthorityRequired: "atom_local_evidence",
		RequiredBefore:    "plan_approval",
		Status:            "open",
	}}

	typedAnswer := `{"answer":"Use the exact current source anchors.","answer_source_kind":"atom_local_evidence","evidence_refs":["native/src/system.cpp#anchor"]}`

	result, err := Run(state, RunOptions{Prompt: "continue", ResumeAnswer: typedAnswer}, func(prompt string, taskFamily string) (string, error) {
		switch taskFamily {
		case string(autonomy.TaskFamilyFeaturePlannerPlanner):
			return mustPlanJSON(validPlan()), nil
		case string(autonomy.TaskFamilyFeaturePlannerImplementor):
			return testPlanExecuteCompletedJSONForPrompt(prompt), nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", taskFamily))
		}

		return "", nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Run returned error: %v", err))
	gomega.Expect(result.State.OpenQuestions[0].AnswerSourceKind).To(gomega.Equal("atom_local_evidence"), fmt.Sprintf("typed atom-local evidence was not recorded: %+v", result.State.OpenQuestions[0]))
	gomega.Expect(result.State.OpenQuestions[0].EvidenceRefs).To(gomega.HaveLen(1), fmt.Sprintf("typed atom-local evidence was not recorded: %+v", result.State.OpenQuestions[0]))
	gomega.Expect(result.State.Status).NotTo(gomega.Equal(FeatureStatePlanningQuestion), fmt.Sprintf("typed atom-local evidence answer should allow planning to continue"))

})

func validPlan() FeaturePlan {
	return FeaturePlan{
		SchemaVersion: FeaturePlanSchemaVersion,
		FeatureID:     "feature-test",
		Tool:          string(featureplannercontract.ToolSubmitFeaturePlan),
		SourceTask: SourceTask{
			Kind:    "operator_prompt",
			Summary: "build feature",
		},
		Summary: "Add feature",
		TopLevelScope: TopLevelScope{
			Objective:       "Add feature",
			SuccessCriteria: []string{"works"},
		},
		SDDContract: SDDContract{
			Refs: []string{"REQ-1"},
		},
		TDDContract: TDDContract{
			CoverageTier:                 "contract_high",
			RequiredBeforeImplementation: true,
			Obligations: []TDDObligation{{
				ID:             "TDD-1",
				Behavior:       "Short label changes",
				Command:        "go test ./...",
				RequiredBefore: "implementation",
			}},
		},
		Atoms: []FeatureAtomPlan{{
			ID:                "atom-1",
			Title:             "Short label",
			Summary:           "Change label",
			RequiredEditPaths: []string{"internal/labels/labels.go", "internal/labels/labels_test.go"},
			SDDRefs:           []string{"REQ-1"},
			TestFirst: TestFirstPlan{
				ObligationRefs:             []string{"TDD-1"},
				Commands:                   []string{"go test ./..."},
				BaselineRequired:           true,
				TestArtifactPaths:          []string{"internal/labels/labels_test.go"},
				PreExecutionEventIDs:       []string{"evt-tdd-1"},
				ExpectedPreExecutionResult: "red-confirmed",
				CoverageGateEventID:        "evt-tdd-1",
			},
			PlannedTDDSnapshot: PlannedTDDSnapshot{
				SnapshotID:                   "snapshot-evidence-0001",
				SnapshotDigest:               validTDDSnapshotDigest,
				ObligationIDs:                []string{"TDD-1"},
				TestCommands:                 []string{"go test ./..."},
				TestArtifactPaths:            []string{"internal/labels/labels_test.go"},
				ExpectedPreExecutionResults:  []string{"red-confirmed"},
				CreatedBeforeDispatchEventID: "evt-tdd-1",
			},
		}},
		BehavioralCoverageDraft: validBehavioralCoverageDraft(),
	}
}

func validGreenfieldBrainPlanForTest() FeaturePlan {
	plan := validPlan()
	plan.FeatureID = "llm-brain-mvp"
	plan.SourceTask = SourceTask{Kind: "operator_prompt", Summary: "Build an LLM brain MVP."}
	plan.Summary = "MVP for an LLM Brain with memory and tool use."
	plan.TopLevelScope = TopLevelScope{
		Objective: "Build a minimal viable LLM Brain that processes prompts, maintains conversation history, and invokes a calculator tool.",
		SuccessCriteria: []string{
			"LLM Brain responds to a greeting.",
			"LLM Brain invokes a calculator tool for arithmetic.",
			"LLM Brain remembers the user's name across turns.",
		},
	}
	plan.SDDContract = SDDContract{
		Acceptance: []string{
			"SDD-ACCEPTANCE-1: LLM Brain responds to a simple greeting with a greeting.",
			"SDD-ACCEPTANCE-2: LLM Brain can call a calculator tool for arithmetic expressions and return the computed result.",
			"SDD-ACCEPTANCE-3: Conversation history is maintained and influences subsequent responses.",
		},
	}
	commands := []string{
		"pytest tests/test_brain.py -k test_greeting",
		"pytest tests/test_brain.py -k test_calculator_tool",
		"pytest tests/test_brain.py -k test_history_persistence",
	}
	plan.TDDContract.Obligations = []TDDObligation{
		{ID: "TDD-1", Behavior: "LLM Brain responds to user input using conversation history", Command: commands[0], RequiredBefore: "implementation"},
		{ID: "TDD-2", Behavior: "LLM Brain calls calculator tool for arithmetic expressions", Command: commands[1], RequiredBefore: "implementation"},
		{ID: "TDD-3", Behavior: "Conversation history is persisted across turns", Command: commands[2], RequiredBefore: "implementation"},
	}
	plan.Atoms = []FeatureAtomPlan{{
		ID:                "atom-brain-core",
		Title:             "Implement LLM Brain core with conversation history and tool calling",
		Summary:           "Create the main brain module, tests, memory behavior, and calculator tool routing.",
		Prompt:            "Check if the work is already satisfied: verify that file tests/test_brain.py exists and contains tests named test_greeting, test_calculator_tool, test_history_persistence that pass. If all pass, return source_satisfied. Otherwise, create tests/test_brain.py with those three tests and create src/brain.py with a class LLMBrain that maintains message history, a method respond(message) that returns a greeting for 'Hello', calls a calculator tool for arithmetic expressions, and remembers user name across turns.",
		RequiredEditPaths: []string{"src/brain.py", "tests/test_brain.py"},
		SDDRefs:           []string{"SDD-ACCEPTANCE-1", "SDD-ACCEPTANCE-2", "SDD-ACCEPTANCE-3"},
		TestFirst: TestFirstPlan{
			ObligationRefs:             []string{"TDD-1", "TDD-2", "TDD-3"},
			Commands:                   commands,
			BaselineRequired:           true,
			TestArtifactPaths:          []string{"tests/test_brain.py"},
			PreExecutionEventIDs:       []string{"evt-tdd-1"},
			ExpectedPreExecutionResult: "red-confirmed",
			CoverageGateEventID:        "evt-tdd-1",
		},
		PlannedTDDSnapshot: PlannedTDDSnapshot{
			SnapshotID:                   "snapshot-evidence-0001",
			SnapshotDigest:               validTDDSnapshotDigest,
			ObligationIDs:                []string{"TDD-1", "TDD-2", "TDD-3"},
			TestCommands:                 commands,
			TestArtifactPaths:            []string{"tests/test_brain.py"},
			ExpectedPreExecutionResults:  []string{"red-confirmed"},
			CreatedBeforeDispatchEventID: "evt-tdd-1",
		},
	}}
	plan.BehavioralCoverageDraft = &BehavioralCoverageDraft{
		Authority: "standalone_provisional",
		Status:    "draft",
		Tier:      "contract_high",
		Requirements: []CoverageDraftRequirement{
			{ID: "REQ-1", Pattern: "event-driven", Statement: "When the user sends a greeting, the brain shall return a greeting.", Refs: []string{"SDD-ACCEPTANCE-1"}, Obligations: []string{"TDD-1"}},
			{ID: "REQ-2", Pattern: "event-driven", Statement: "When the user sends arithmetic, the brain shall call the calculator tool.", Refs: []string{"SDD-ACCEPTANCE-2"}, Obligations: []string{"TDD-2"}},
			{ID: "REQ-3", Pattern: "stateful", Statement: "When the user shares a name, later turns shall use conversation history.", Refs: []string{"SDD-ACCEPTANCE-3"}, Obligations: []string{"TDD-3"}},
		},
		Scenarios: []CoverageDraftScenario{
			{ID: "BDD-1", RequirementID: "REQ-1", Given: []string{"no prior messages"}, When: []string{"the user sends Hello"}, Then: []string{"the system responds with a greeting"}, ConcreteData: map[string]string{"input": "Hello", "expected": "Hello"}, Obligations: []string{"TDD-1"}},
			{ID: "BDD-2", RequirementID: "REQ-2", Given: []string{"calculator tool is registered"}, When: []string{"the user asks What is 2+2?"}, Then: []string{"the calculator tool is invoked and the result 4 is returned"}, ConcreteData: map[string]string{"input": "What is 2+2?", "expected": "4"}, Obligations: []string{"TDD-2"}},
			{ID: "BDD-3", RequirementID: "REQ-3", Given: []string{"the user previously said My name is Alice"}, When: []string{"the user asks What is my name?"}, Then: []string{"the system answers Alice"}, ConcreteData: map[string]string{"name": "Alice"}, Obligations: []string{"TDD-3"}},
		},
		Invariants: []CoverageDraftInvariant{{
			ID:                    "INV-1",
			Statement:             "Conversation history is append-only after the first user message.",
			PropertyBasedTestRefs: []string{"TestHistoryAppendOnly"},
			Obligations:           []string{"TDD-3"},
		}},
		FailureModes: []CoverageDraftFailureMode{{
			ID:                 "FM-1",
			Mode:               "calculator tool cannot evaluate an expression",
			Mitigation:         "return a friendly error without crashing",
			RPN:                1,
			MitigationTestRefs: []string{"TestCalculatorToolErrorRecovery"},
			Obligations:        []string{"TDD-2"},
		}},
		SurfaceInventory: []CoverageDraftSurfaceDecision{
			{ID: "SURF-1", Surface: "domain_logic", Status: "in_scope", Rationale: "core prompt handling behavior", Obligations: []string{"TDD-1", "TDD-2", "TDD-3"}},
			{ID: "SURF-2", Surface: "api_transport", Status: "not_required", Rationale: "no external transport boundary is requested for this standalone MVP"},
			{ID: "SURF-3", Surface: "persistence", Status: "in_scope", Rationale: "conversation history is a requested behavior", Obligations: []string{"TDD-3"}},
			{ID: "SURF-4", Surface: "security", Status: "not_required", Rationale: "no authorization boundary is introduced by this increment"},
		},
		TestObligations: []CoverageDraftTestObligation{
			{ID: "TDD-1", Type: "unit", Behavior: "LLM Brain responds to a greeting", Owner: "featureplanner", Command: commands[0], RequiredBefore: "implementation"},
			{ID: "TDD-2", Type: "unit", Behavior: "LLM Brain calls calculator tool", Owner: "featureplanner", Command: commands[1], RequiredBefore: "implementation"},
			{ID: "TDD-3", Type: "unit", Behavior: "LLM Brain remembers name across turns", Owner: "featureplanner", Command: commands[2], RequiredBefore: "implementation"},
		},
		Verification: []CoverageDraftVerification{
			{ID: "VERIFY-1", Gate: "required test passes", Command: commands[0], Obligations: []string{"TDD-1"}},
			{ID: "VERIFY-2", Gate: "required test passes", Command: commands[1], Obligations: []string{"TDD-2"}},
			{ID: "VERIFY-3", Gate: "required test passes", Command: commands[2], Obligations: []string{"TDD-3"}},
		},
	}
	return plan
}

func runningExampleExistingPathPlanForTest() FeaturePlan {
	plan := validGreenfieldBrainPlanForTest()
	plan.FeatureID = "llm-brain-memory-mvp"
	plan.Summary = "MVP for LLM Brain: long-term agentic memory management with query handling and persistence."
	plan.TopLevelScope.Objective = "Implement an MVP of an LLM Brain that stores and retrieves agentic memory entries, supporting basic querying and persistence."
	plan.TopLevelScope.SuccessCriteria = []string{
		"Memory entries can be created with content and metadata.",
		"Memory entries can be retrieved by query matching.",
		"Persistence to a local file store works across sessions.",
		"All unit tests pass.",
	}
	plan.SDDContract.Acceptance = []string{"SDD-ACCEPTANCE-1: Memory entry creation and retrieval via query_handler."}
	plan.TDDContract.Obligations = []TDDObligation{{
		ID:             "TDD-1",
		Behavior:       "Memory entry creation and retrieval via query_handler.",
		Command:        "pytest tests/test_query_handler.py",
		RequiredBefore: "implementation",
	}}
	plan.Atoms = []FeatureAtomPlan{{
		ID:                "atom-1",
		Title:             "Implement memory storage and retrieval in query_handler",
		Summary:           "Add methods to create and retrieve memory entries with persistence.",
		Prompt:            "Implement memory storage and retrieval in llm_brain/query_handler.py. Add methods: create_memory(content) -> MemoryEntry, get_memory(id) -> MemoryEntry. Use a simple in-memory dict for storage, and add persistence to a JSON file 'memory_store.json'. In tests/test_query_handler.py, write tests for creation and retrieval, including edge cases like empty content. Run tests with 'pytest tests/test_query_handler.py' and ensure they pass.",
		RequiredEditPaths: []string{"llm_brain/query_handler.py", "tests/test_query_handler.py"},
		SDDRefs:           []string{"SDD-ACCEPTANCE-1"},
		TestFirst: TestFirstPlan{
			ObligationRefs:             []string{"TDD-1"},
			Commands:                   []string{"pytest tests/test_query_handler.py"},
			BaselineRequired:           true,
			TestArtifactPaths:          []string{"tests/test_query_handler.py"},
			PreExecutionEventIDs:       []string{"evt-tdd-1"},
			ExpectedPreExecutionResult: "red-confirmed",
			CoverageGateEventID:        "evt-tdd-1",
		},
		PlannedTDDSnapshot: PlannedTDDSnapshot{
			SnapshotID:                   "snapshot-evidence-0001",
			SnapshotDigest:               validTDDSnapshotDigest,
			ObligationIDs:                []string{"TDD-1"},
			TestCommands:                 []string{"pytest tests/test_query_handler.py"},
			TestArtifactPaths:            []string{"tests/test_query_handler.py"},
			ExpectedPreExecutionResults:  []string{"red-confirmed"},
			CreatedBeforeDispatchEventID: "evt-tdd-1",
		},
	}}
	plan.BehavioralCoverageDraft = &BehavioralCoverageDraft{
		Authority: "standalone_provisional",
		Status:    "draft",
		Tier:      "contract_high",
		Requirements: []CoverageDraftRequirement{{
			ID:          "REQ-1",
			Pattern:     "event-driven",
			Statement:   "When a memory entry is created, the system shall store it and allow retrieval by query.",
			Refs:        []string{"SDD-ACCEPTANCE-1"},
			Obligations: []string{"TDD-1"},
		}},
		ExampleRules: []CoverageDraftExampleRule{{
			ID:            "RULE-1",
			RequirementID: "REQ-1",
			Rule:          "Memory entries are stored with a unique ID, content, and timestamp.",
			Examples: []CoverageDraftExample{{
				ID:           "EX-1",
				Description:  "Create and retrieve a memory entry",
				ConcreteData: map[string]string{"input": `{"content": "test memory"}`, "expected": `{"content": "test memory"}`},
			}},
		}},
		Scenarios: []CoverageDraftScenario{{
			ID:            "BDD-1",
			RequirementID: "REQ-1",
			RuleID:        "RULE-1",
			Given:         []string{"A query_handler instance"},
			When:          []string{"A memory entry is created with content 'test memory'"},
			Then:          []string{"The entry is stored and can be retrieved by its ID"},
			ConcreteData:  map[string]string{"input": `{"content": "test memory"}`, "expected": `{"content": "test memory"}`},
			Obligations:   []string{"TDD-1"},
			Tags:          []string{"mvp"},
		}},
		Invariants: []CoverageDraftInvariant{{
			ID:                    "INV-1",
			Statement:             "All memory entries have a unique ID.",
			PropertyBasedTestRefs: []string{"TestMemoryIDsUnique"},
			Obligations:           []string{"TDD-1"},
		}},
		FailureModes: []CoverageDraftFailureMode{{
			ID:                 "FM-1",
			Mode:               "Empty content",
			Mitigation:         "Reject with error message",
			RPN:                1,
			MitigationTestRefs: []string{"TestEmptyContentRejected"},
			Obligations:        []string{"TDD-1"},
		}},
		SurfaceInventory: []CoverageDraftSurfaceDecision{
			{ID: "SURF-1", Surface: "domain_logic", Status: "in_scope", Rationale: "memory creation and retrieval are core domain behavior", Obligations: []string{"TDD-1"}},
			{ID: "SURF-2", Surface: "persistence", Status: "in_scope", Rationale: "local persistence is part of the requested MVP", Obligations: []string{"TDD-1"}},
			{ID: "SURF-3", Surface: "api_transport", Status: "not_required", Rationale: "no external API or transport boundary is requested"},
		},
		TestObligations: []CoverageDraftTestObligation{{
			ID:             "TDD-1",
			Type:           "unit",
			Behavior:       "Memory entry creation and retrieval via query_handler.",
			Owner:          "standalone feature planner",
			Command:        "pytest tests/test_query_handler.py",
			RequiredBefore: "implementation",
		}},
		Verification: []CoverageDraftVerification{{
			ID:          "VERIFY-1",
			Gate:        "required test command passes",
			Command:     "pytest tests/test_query_handler.py",
			Obligations: []string{"TDD-1"},
		}},
	}
	return plan
}

func mustPlanJSON(plan FeaturePlan) string {
	ginkgo.GinkgoHelper()

	body, err := json.Marshal(plan)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal plan: %v", err))

	return string(body)
}

func featurePlannerEventTypesForTest(events []Event) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func testPlanExecuteCompletedJSONForPrompt(prompt string) string {
	contractID, snapshotHash, paths := testPlanExecuteContractFromPrompt(prompt)
	sourceSanity := testSourceSanityEvidenceForPaths(paths)
	commands := testVerificationCommandsFromPrompt(prompt)
	payload := map[string]any{
		"tool":                        "plan_execute_completed",
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "complete",
		"review_status":               "completed",
		"completion_source":           "durable_plan_execute_terminal_completion",
		"contract_id":                 contractID,
		"snapshot_hash":               snapshotHash,
		"changed_paths":               paths,
		"required_edit_paths":         paths,
		"source_sanity":               sourceSanity,
		"verification":                testVerificationEvidenceForCommands(commands),
		"verification_commands":       testVerificationCommandsForCommands(commands),
		"acceptance_evidence_ids":     []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted": true,
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteCompletedJSONForPlanWithSourceSanity(plan FeaturePlan, prompt string) string {
	contractID, snapshotHash, paths := testPlanExecuteContractFromPrompt(prompt)
	sourceSanity := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		sourceSanity = append(sourceSanity, map[string]string{"kind": "source_sanity", "path": path, "status": "ok"})
	}
	contract := CompilePlanAuthorityContract(plan, nil)
	commands := testVerificationCommandsFromContract(contract)
	payload := map[string]any{
		"tool":                        "plan_execute_completed",
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "complete",
		"review_status":               "completed",
		"completion_source":           "durable_plan_execute_terminal_completion",
		"contract_id":                 contractID,
		"snapshot_hash":               snapshotHash,
		"changed_paths":               paths,
		"required_edit_paths":         paths,
		"source_sanity":               sourceSanity,
		"verification":                testVerificationEvidenceForCommands(commands),
		"verification_commands":       testVerificationCommandsForCommands(commands),
		"acceptance_evidence_ids":     requiredAcceptanceEvidenceIDs(contract.Atoms[0]),
		"acceptance_evidence_trusted": true,
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteCompletedJSONForPlanAndPrompt(plan FeaturePlan, prompt string) string {
	contractID, snapshotHash, paths := testPlanExecuteContractFromPrompt(prompt)
	sourceSanity := testSourceSanityEvidenceForPaths(paths)
	contract := CompilePlanAuthorityContract(plan, nil)
	commands := testVerificationCommandsFromContract(contract)

	payload := map[string]any{
		"tool":                        "plan_execute_completed",
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "complete",
		"review_status":               "completed",
		"completion_source":           "durable_plan_execute_terminal_completion",
		"contract_id":                 contractID,
		"snapshot_hash":               snapshotHash,
		"changed_paths":               paths,
		"required_edit_paths":         paths,
		"source_sanity":               sourceSanity,
		"verification":                testVerificationEvidenceForCommands(commands),
		"verification_commands":       testVerificationCommandsForCommands(commands),
		"acceptance_evidence_ids":     requiredAcceptanceEvidenceIDs(contract.Atoms[0]),
		"acceptance_evidence_trusted": true,
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteSourceSatisfiedJSONForPrompt(prompt string) string {
	contractID, snapshotHash, paths := testPlanExecuteContractFromPrompt(prompt)
	payload := map[string]any{
		"tool":                            "plan_execute_completed",
		"lane_family":                     "plan-execute",
		"status":                          "source-satisfied",
		"phase":                           "complete",
		"contract_id":                     contractID,
		"snapshot_hash":                   snapshotHash,
		"source_satisfied_evidence_paths": paths,
		"source_satisfied_evidence":       testPlanExecuteSourceSatisfiedEvidenceRefs(paths),
		"required_edit_paths":             paths,
		"acceptance_evidence_ids":         []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted":     true,
		"completion_source":               "durable_plan_execute_terminal_completion",
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteCompletedJSONForAtom(atom FeatureAtomState) string {
	sourceSanity := testSourceSanityEvidenceForPaths(atom.RequiredEditPaths)
	verification := testVerificationEvidenceForAtom(atom)
	payload := map[string]any{
		"tool":                        "plan_execute_completed",
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "complete",
		"review_status":               "completed",
		"completion_source":           "durable_plan_execute_terminal_completion",
		"contract_id":                 atom.RuntimeEnvelope.ContractID,
		"snapshot_hash":               atom.RuntimeEnvelope.ProtocolHash,
		"changed_paths":               atom.RequiredEditPaths,
		"required_edit_paths":         atom.RequiredEditPaths,
		"source_sanity":               sourceSanity,
		"verification":                verification,
		"verification_commands":       testVerificationCommandsForAtom(atom),
		"acceptance_evidence_ids":     []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted": true,
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testPlanExecuteCompletedJSONForAtomWithReviewSkipped(atom FeatureAtomState) string {
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteCompletedJSONForAtom(atom)), &payload)
	payload["review_status"] = "skipped"
	payload["review_repair_skipped"] = true
	payload["progress_path"] = ".idiom/plan-execute/session/state.json"
	payload["summary"] = "Edited files and ran required verification"

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func testNestedPlanExecuteCompletedJSONForAtom(atom FeatureAtomState) string {
	sourceSanity := testSourceSanityEvidenceForPaths(atom.RequiredEditPaths)
	verification := testVerificationEvidenceForAtom(atom)
	nested := trustPlanExecuteCompletionForTest(map[string]any{
		"lane_family":                 "plan-execute",
		"status":                      "completed",
		"phase":                       "review_repair",
		"review_status":               "completed",
		"progress_path":               ".idiom/plan-execute/session/state.json",
		"contract_id":                 atom.RuntimeEnvelope.ContractID,
		"snapshot_hash":               atom.RuntimeEnvelope.ProtocolHash,
		"changed_paths":               atom.RequiredEditPaths,
		"required_edit_paths":         atom.RequiredEditPaths,
		"source_sanity":               sourceSanity,
		"verification":                verification,
		"verification_commands":       testVerificationCommandsForAtom(atom),
		"acceptance_evidence_ids":     []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted": true,
		"completion_source":           "durable_plan_execute_terminal_completion",
	})

	return mustJSONMap(map[string]any{
		"tool":         "plan_execute_completed",
		"plan_execute": nested,
	})
}

func testSourceSanityEvidenceForPaths(paths []string) []map[string]string {
	sourceSanity := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, ".go") {
			sourceSanity = append(sourceSanity, map[string]string{"kind": "source_sanity", "path": path, "status": "ok"})
		}
	}

	return sourceSanity
}

func testVerificationEvidenceForAtom(atom FeatureAtomState) []map[string]string {
	var checks []map[string]string
	for _, command := range normalizedStringList(append(append([]string(nil), atom.RequiredTests...), atom.TestFirst.Commands...)) {
		checks = append(checks, map[string]string{"kind": "verification", "command": command, "status": "ok"})
	}

	return checks
}

func testVerificationCommandsFromPrompt(prompt string) []string {
	var commands []string
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")), "`")
		idx := strings.Index(line, "go test")
		if idx < 0 {
			continue
		}
		command := strings.Trim(line[idx:], "` ")
		if cut := strings.IndexAny(command, "`\";,"); cut >= 0 {
			command = strings.TrimSpace(command[:cut])
		}
		if cut := strings.Index(command, " after "); cut >= 0 {
			command = strings.TrimSpace(command[:cut])
		}
		commands = append(commands, command)
	}
	return normalizedStringList(commands)
}

func testVerificationCommandsFromContract(contract plancontract.PlanExecutionContract) []string {
	var commands []string
	for _, atom := range contract.Atoms {
		for _, test := range atom.RequiredTests {
			commands = append(commands, test.Command)
		}
		for _, obligation := range atom.TDDObligations {
			commands = append(commands, obligation.Command)
		}
	}
	return normalizedStringList(commands)
}

func testVerificationEvidenceForCommands(commands []string) []map[string]string {
	var checks []map[string]string
	for _, command := range normalizedStringList(commands) {
		checks = append(checks, map[string]string{"kind": "verification", "command": command, "status": "ok"})
	}
	return checks
}

func testVerificationCommandsForCommands(commands []string) []map[string]any {
	var out []map[string]any
	for _, command := range normalizedStringList(commands) {
		out = append(out, map[string]any{"command": command, "source": "featureplanner.test"})
	}
	return out
}

func testVerificationCommandsForAtom(atom FeatureAtomState) []map[string]any {
	var commands []map[string]any
	for _, command := range normalizedStringList(append(append([]string(nil), atom.RequiredTests...), atom.TestFirst.Commands...)) {
		commands = append(commands, map[string]any{"command": command, "source": "featureplanner.test"})
	}

	return commands
}

func testPlanExecuteSourceSatisfiedJSONForAtom(atom FeatureAtomState) string {
	payload := map[string]any{
		"tool":                            "plan_execute_completed",
		"lane_family":                     "plan-execute",
		"status":                          "source-satisfied",
		"phase":                           "complete",
		"progress_path":                   ".idiom/plan-execute/session/state.json",
		"contract_id":                     atom.RuntimeEnvelope.ContractID,
		"snapshot_hash":                   atom.RuntimeEnvelope.ProtocolHash,
		"source_satisfied_evidence_paths": atom.RequiredEditPaths,
		"source_satisfied_evidence":       testPlanExecuteSourceSatisfiedEvidenceRefs(atom.RequiredEditPaths),
		"required_edit_paths":             atom.RequiredEditPaths,
		"acceptance_evidence_ids":         []string{"req-1", "adr-1"},
		"acceptance_evidence_trusted":     true,
		"completion_source":               "durable_plan_execute_terminal_completion",
	}

	return mustJSONMap(trustPlanExecuteCompletionForTest(payload))
}

func trustPlanExecuteCompletionForTest(fields map[string]any) map[string]any {
	TrustObservedPlanExecuteCompletionFields(fields)

	return fields
}

func testPlanExecuteSourceSatisfiedEvidenceRefs(paths []string) []map[string]string {
	refs := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		refs = append(refs, map[string]string{"path": path, "kind": "harness_observed_source"})
	}

	return refs
}

func testPlanExecuteOutcomeOnlySourceSatisfiedJSONForAtom(atom FeatureAtomState) string {
	payload := map[string]any{}

	_ = json.Unmarshal([]byte(testPlanExecuteSourceSatisfiedJSONForAtom(atom)), &payload)
	delete(payload, "status")

	payload["outcome"] = "source_satisfied"

	return mustJSONMap(payload)
}

func testPlanExecuteContractFromPrompt(prompt string) (string, string, []string) {
	var (
		contractID   string
		snapshotHash string
		paths        []string
	)

	inRequired := false

	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "Active plan contract id:"):
			contractID = strings.TrimSpace(strings.TrimPrefix(line, "Active plan contract id:"))
		case strings.HasPrefix(line, "Active plan contract snapshot hash:"):
			snapshotHash = strings.TrimSpace(strings.TrimPrefix(line, "Active plan contract snapshot hash:"))
		case line == "required_edit_paths:":
			inRequired = true
		case inRequired && strings.HasPrefix(line, "- "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		case inRequired && line == "":
			inRequired = false
		case inRequired && strings.HasSuffix(line, ":"):
			inRequired = false
		}
	}

	return contractID, snapshotHash, normalizedStringList(paths)
}

func mustJSONMap(payload map[string]any) string {
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}

	return string(body)
}

func mkdirAndWriteFile(path string, content string) {
	ginkgo.GinkgoHelper()
	{

		err := os.MkdirAll(filepath.Dir(path), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}
	{

		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}

}

func validProductContextPlan() FeaturePlan {
	plan := validPlan()
	plan.BehavioralCoverageDraft = nil

	plan.SDDContract.Refs = nil
	plan.SDDContract.PRDRequirementRefs = []string{"REQ-1"}
	plan.SDDContract.ADRRefs = []string{"ADR-1"}
	plan.Atoms[0].SDDRefs = []string{"REQ-1", "ADR-1"}
	plan.Atoms[0].TDDObligations = []string{"TDD-1"}
	plan.CoverageContract = validCoverageContractRef()

	return plan
}

func validBehavioralCoverageDraft() *BehavioralCoverageDraft {
	return &BehavioralCoverageDraft{
		Authority: "standalone_provisional",
		Status:    "draft",
		Tier:      "contract_high",
		Requirements: []CoverageDraftRequirement{{
			ID:          "REQ-1",
			Pattern:     "event-driven",
			Statement:   "When label behavior changes, the system shall keep the short label visible.",
			Refs:        []string{"SDD-1"},
			Obligations: []string{"TDD-1"},
		}},
		ExampleRules: []CoverageDraftExampleRule{{
			ID:            "RULE-1",
			RequirementID: "REQ-1",
			Rule:          "Short labels render deterministically.",
			Examples: []CoverageDraftExample{{
				ID:               "EX-1",
				Description:      "short label request",
				ConcreteData:     map[string]string{"input": "label", "expected": "short label"},
				ConcreteDataRefs: []string{"fixtures/short-label.json"},
			}, {
				ID:               "EX-2",
				Description:      "blank label request",
				ConcreteData:     map[string]string{"input": "", "expected": "label"},
				ConcreteDataRefs: []string{"fixtures/blank-label.json"},
			}},
		}},
		Scenarios: []CoverageDraftScenario{{
			ID:               "BDD-1",
			RequirementID:    "REQ-1",
			RuleID:           "RULE-1",
			Given:            []string{"a short label request exists"},
			When:             []string{"the label renderer runs"},
			Then:             []string{"the short label is returned"},
			ConcreteData:     map[string]string{"input": "label", "expected": "short label"},
			ConcreteDataRefs: []string{"fixtures/short-label-scenario.json"},
			Obligations:      []string{"TDD-1"},
			Tags:             []string{"unit"},
		}},
		Invariants: []CoverageDraftInvariant{{
			ID:                    "INV-1",
			Statement:             "Short label rendering is deterministic.",
			PropertyBasedTestRefs: []string{"TestShortLabelDeterministic"},
			Obligations:           []string{"TDD-1"},
		}},
		FailureModes: []CoverageDraftFailureMode{{
			ID:                 "FM-1",
			Mode:               "blank label request",
			Mitigation:         "return a stable fallback label",
			RPN:                1,
			MitigationTestRefs: []string{"TestBlankLabelFallback"},
			Obligations:        []string{"TDD-1"},
		}},
		SurfaceInventory: []CoverageDraftSurfaceDecision{
			{ID: "SURF-1", Surface: "domain_logic", Status: "in_scope", Rationale: "label rendering is the requested behavior", Obligations: []string{"TDD-1"}},
			{ID: "SURF-2", Surface: "api_transport", Status: "not_required", Rationale: "no external transport boundary is changed"},
			{ID: "SURF-3", Surface: "persistence", Status: "not_required", Rationale: "label rendering has no durable state"},
		},
		TestObligations: []CoverageDraftTestObligation{{
			ID:             "TDD-1",
			Type:           "unit",
			Behavior:       "Short label changes",
			Owner:          "featureplanner",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
		}},
		Verification: []CoverageDraftVerification{{
			ID:          "VERIFY-1",
			Gate:        "required tests pass",
			Command:     "go test ./...",
			Obligations: []string{"TDD-1"},
		}},
		Items: []BehavioralCoverageItem{{
			ID:          "BCD-1",
			Behavior:    "Short label changes",
			Obligations: []string{"TDD-1"},
			Evidence:    []string{"standalone operator request"},
		}},
	}
}

func validProductWorkflowBrief() ProductWorkflowBrief {
	return ProductWorkflowBrief{
		EffortID:           "effort-1",
		TaskID:             "task-1",
		CoverageGatePassed: true,
		CoverageContract:   validCoverageContractRef(),
		PRDRequirements: []PRDRequirementBrief{{
			ID:          "REQ-1",
			Obligations: []string{"TDD-1"},
		}},
		AcceptedADRIDs: []string{"ADR-1"},
		TDDObligations: []TDDObligationBrief{{
			ID:             "TDD-1",
			Behavior:       "Short label changes",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
		CoverageTier:         "contract_high",
		FilesToModify:        []string{"internal/labels/labels.go", "internal/labels/labels_test.go"},
		ContextEvidencePaths: []string{"internal/labels/labels.go"},
		Tests:                []string{"go test ./..."},
	}
}

func validCoverageContractRef() *CoverageContractRef {
	return &CoverageContractRef{
		ContractID:   "cov-1",
		Digest:       "sha256:abc",
		EventID:      "evt-cov-1",
		Status:       "pass",
		EffortID:     "effort-1",
		CoverageTier: "contract_high",
	}
}

func validAcceptedBehavioralCoverage() *AcceptedBehavioralCoverage {
	return &AcceptedBehavioralCoverage{
		Authority: "product_workflow",
		Status:    "pass",
		Contract:  *validCoverageContractRef(),
		Tier:      "contract_high",
		Requirements: []AcceptedCoverageRequirement{{
			ID:          "REQ-1",
			Pattern:     "event-driven",
			Statement:   "When product behavior is requested, the system shall render it.",
			Refs:        []string{"BDD-1"},
			Obligations: []string{"TDD-1"},
		}},
		ExampleRules: []AcceptedCoverageExampleRule{{
			ID:            "RULE-1",
			RequirementID: "REQ-1",
			Rule:          "Accepted product coverage renders structured behavior.",
			Examples: []AcceptedCoverageExample{
				{ID: "EX-1", Description: "accepted detail present", ConcreteDataRefs: []string{"fixtures/accepted.json"}},
				{ID: "EX-2", Description: "draft omitted", ConcreteDataRefs: []string{"fixtures/no-draft.json"}, Negative: true},
			},
		}},
		Scenarios: []AcceptedCoverageScenario{{
			ID:               "BDD-1",
			RequirementID:    "REQ-1",
			Given:            []string{"product workflow accepted coverage exists"},
			When:             []string{"the plan is rendered"},
			Then:             []string{"product behavior is rendered"},
			ConcreteDataRefs: []string{"fixtures/accepted.json"},
			Obligations:      []string{"TDD-1"},
		}},
		DesignContracts: []AcceptedCoverageDesignContract{{
			ID:             "DBC-1",
			Boundary:       "product coverage authority",
			Preconditions:  []string{"product workflow accepted coverage exists"},
			Postconditions: []string{"feature planner receives read-only accepted coverage"},
			Invariants:     []string{"planner cannot author accepted coverage"},
			TestRefs:       []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
			Obligations:    []string{"TDD-1"},
		}},
		Invariants: []AcceptedCoverageInvariant{{
			ID:                    "INV-1",
			Statement:             "accepted coverage remains product-owned",
			PropertyBasedTestRefs: []string{"TestNormalizePlanProductWorkflowContextStampsAcceptedCoverageAuthority"},
			Obligations:           []string{"TDD-1"},
		}},
		FailureModes: []AcceptedCoverageFailureMode{{
			ID:                 "FM-1",
			Mode:               "planner authors accepted coverage",
			Mitigation:         "validator rejects accepted_coverage in submit_feature_plan",
			RPN:                72,
			MitigationTestRefs: []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
			Obligations:        []string{"TDD-1"},
		}},
		HAZOPDeviations: []AcceptedCoverageHAZOPDeviation{{
			ID:           "HAZOP-1",
			GuideWord:    "Other Than",
			WorkflowStep: "feature planner submission",
			Deviation:    "accepted coverage is reauthored by planner",
			Severity:     4,
			TestRefs:     []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
			Obligations:  []string{"TDD-1"},
		}},
		FaultTreeLeaves: []AcceptedCoverageFaultTreeLeaf{{
			ID:          "FTA-1",
			TopEvent:    "planner-owned coverage authority",
			Cause:       "accepted coverage accepted from model output",
			TestRefs:    []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
			Obligations: []string{"TDD-1"},
		}},
		DecisionTables: []AcceptedCoverageDecisionTable{{
			ID:         "DT-1",
			Conditions: []string{"brief has accepted coverage", "plan submits accepted coverage"},
			Rules: []AcceptedCoverageDecisionRule{{
				ID:         "DT-1-R1",
				Conditions: map[string]string{"brief has accepted coverage": "true", "plan submits accepted coverage": "false"},
				Expected:   "stamp accepted coverage",
			}},
			TestRefs:    []string{"TestNormalizePlanProductWorkflowContextStampsAcceptedCoverageAuthority"},
			Obligations: []string{"TDD-1"},
		}},
		BoundaryValues: []AcceptedCoverageBoundaryValue{{
			ID:          "BV-1",
			Parameter:   "accepted_coverage submission count",
			Classes:     []string{"zero", "one"},
			Values:      []string{"0", "1"},
			TestRefs:    []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
			Obligations: []string{"TDD-1"},
		}},
		StateMachines: []AcceptedCoverageStateMachine{{
			ID:     "SM-1",
			States: []string{"brief_loaded", "plan_validated", "plan_stamped"},
			Transitions: []AcceptedCoverageTransition{{
				ID:       "TR-1",
				From:     "plan_validated",
				Event:    "normalize",
				To:       "plan_stamped",
				TestRefs: []string{"TestNormalizePlanProductWorkflowContextStampsAcceptedCoverageAuthority"},
			}},
		}},
		FuzzTargets: []AcceptedCoverageFuzzTarget{{
			ID:          "FUZZ-1",
			Status:      "covered",
			Target:      "accepted coverage JSON parser",
			Generators:  []string{"coverage authority mutations"},
			Invariants:  []string{"product authority remains read-only"},
			TestRefs:    []string{"FuzzAcceptedCoverageParser"},
			Obligations: []string{"TDD-1"},
		}},
		RAGEvals: []AcceptedCoverageRAGEval{{
			ID:          "RAG-1",
			Status:      "measured",
			DatasetRefs: []string{"evals/accepted_coverage_context.jsonl"},
			Metrics:     map[string]float64{"groundedness": 0.97},
			Thresholds:  map[string]float64{"groundedness": 0.95},
			EvalRefs:    []string{"TestAcceptedCoveragePromptContextEval"},
			Obligations: []string{"TDD-1"},
		}},
		SecurityCases: []AcceptedCoverageSecurityCase{{
			ID:          "SEC-1",
			Status:      "tested",
			Risk:        "planner-authored coverage authority",
			Attack:      "model includes accepted_coverage in submit_feature_plan",
			Expected:    "plan is rejected",
			TestRefs:    []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
			Obligations: []string{"TDD-1"},
		}},
		PrivacyControls: []AcceptedCoveragePrivacyControl{{
			ID:            "PRIV-1",
			Status:        "tested",
			DataClass:     "raw prompt context",
			Policy:        "do not persist raw model-only context",
			RedactionRefs: []string{"redaction/accepted_coverage_context"},
			Retention:     "structured fields only",
			TestRefs:      []string{"TestBuildPlannerPromptRendersAcceptedCoverageAsReadOnlyContext"},
			Obligations:   []string{"TDD-1"},
		}},
		ProviderContracts: []AcceptedCoverageProviderContract{{
			ID:           "PACT-1",
			Status:       "tested",
			Provider:     "product-workflow",
			Consumer:     "feature-planner",
			ContractRefs: []string{"contracts/product_feature_planner.json"},
			TestRefs:     []string{"TestNormalizePlanProductWorkflowContextStampsAcceptedCoverageAuthority"},
			Obligations:  []string{"TDD-1"},
		}},
		Observability: []AcceptedCoverageObservability{{
			ID:               "OBS-1",
			Status:           "tested",
			Signal:           "feature_planner.accepted_coverage_context",
			InstrumentedPath: "internal/workflows/featureplanner/orchestrate.go",
			RequiredFields:   []string{"contract_id", "coverage_tier"},
			TestRefs:         []string{"TestBuildPlannerPromptRendersAcceptedCoverageAsReadOnlyContext"},
			Obligations:      []string{"TDD-1"},
		}},
		PerformanceBounds: []AcceptedCoveragePerformanceBound{{
			ID:              "PERF-1",
			Status:          "tested",
			Operation:       "accepted coverage prompt rendering",
			Bound:           "bounded deterministic context",
			MeasurementRefs: []string{"benchmarks/accepted_coverage_prompt.txt"},
			Obligations:     []string{"TDD-1"},
		}},
		E2EJourneys: []AcceptedCoverageE2EJourney{{
			ID:          "E2E-1",
			Status:      "scripted",
			Journey:     "product accepted coverage reaches feature planner prompt",
			Steps:       []string{"load product brief", "build planner prompt", "inspect accepted coverage context"},
			ScriptRefs:  []string{"internal/workflows/featureplanner/validate_test.go"},
			Obligations: []string{"TDD-1"},
		}},
		SurfaceInventory: []AcceptedCoverageSurfaceDecision{{
			ID:          "SURF-1",
			Surface:     "domain_logic",
			Status:      "in_scope",
			Rationale:   "accepted product behavior is in scope",
			Obligations: []string{"TDD-1"},
		}, {
			ID:        "SURF-2",
			Surface:   "api_transport",
			Status:    "not_required",
			Rationale: "no transport boundary changes",
		}},
		TestObligations: []AcceptedCoverageTestObligation{{
			ID:             "TDD-1",
			Type:           "contract",
			Behavior:       "accepted coverage is shown without planner authorship",
			Owner:          "featureplanner",
			Command:        "go test ./internal/workflows/featureplanner",
			RequiredBefore: "implementation",
		}},
		Verification: []AcceptedCoverageVerification{{
			ID:          "VERIFY-1",
			Gate:        "contract",
			Command:     "go test ./internal/workflows/featureplanner",
			Obligations: []string{"TDD-1"},
		}},
	}
}
