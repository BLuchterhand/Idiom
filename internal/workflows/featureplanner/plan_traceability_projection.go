package featureplanner

import "strings"

type FeaturePlanTraceabilityReview struct {
	Behaviors     []FeaturePlanBehaviorReview
	Surfaces      []FeaturePlanSurfaceReview
	VariantCount  int
	InvariantRefs []string
	TestCaseRefs  []string
	TestFiles     []string
	Commands      []string
}

type FeaturePlanBehaviorReview struct {
	ID           string
	Behavior     string
	Requirements []string
	Acceptance   []string
	Scenarios    []string
	Variants     []string
	Invariants   []string
	TestCases    []string
	TestFiles    []string
	Commands     []string
	AtomTitles   []string
	Surfaces     []string
	MissingTests bool
}

type FeaturePlanSurfaceReview struct {
	ID                 string
	Surface            string
	Status             string
	Rationale          string
	Obligations        []string
	EvidenceRefs       []string
	QuestionRefs       []string
	MissingObligations bool
	OpenQuestion       bool
}

func FeaturePlanTraceabilityReviewForPlan(plan FeaturePlan) FeaturePlanTraceabilityReview {
	behaviorByID := make(map[string]*FeaturePlanBehaviorReview)
	var behaviorOrder []string
	ensureBehavior := func(id string, behavior string) *FeaturePlanBehaviorReview {
		id = strings.TrimSpace(id)
		behavior = strings.TrimSpace(behavior)
		key := id
		if key == "" {
			key = behavior
		}
		if key == "" {
			return nil
		}
		if existing := behaviorByID[key]; existing != nil {
			if existing.ID == "" {
				existing.ID = id
			}
			if existing.Behavior == "" {
				existing.Behavior = behavior
			}
			return existing
		}
		row := &FeaturePlanBehaviorReview{ID: id, Behavior: behavior}
		behaviorByID[key] = row
		behaviorOrder = append(behaviorOrder, key)
		return row
	}
	byObligation := func(id string) *FeaturePlanBehaviorReview {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil
		}
		if existing := behaviorByID[id]; existing != nil {
			return existing
		}
		return ensureBehavior(id, "")
	}

	for _, obligation := range plan.TDDContract.Obligations {
		if row := ensureBehavior(obligation.ID, obligation.Behavior); row != nil {
			row.Commands = append(row.Commands, obligation.Command)
		}
	}
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, surface := range draft.SurfaceInventory {
			reviewSurface := featurePlanSurfaceReviewFromDraft(surface)
			attachSurfaceTraceabilityReview(reviewSurface, byObligation)
			behaviorOrder = appendBehaviorOrderForSurfaceMissingObligations(behaviorOrder, behaviorByID, reviewSurface)
		}
		for _, item := range draft.Items {
			row := ensureBehavior(item.ID, item.Behavior)
			if row == nil {
				continue
			}
			for _, ref := range item.Obligations {
				if target := byObligation(ref); target != nil && target != row {
					target.Variants = append(target.Variants, firstCoverageText(item.ID, item.Behavior))
				}
			}
		}
		for _, obligation := range draft.TestObligations {
			if row := ensureBehavior(obligation.ID, obligation.Behavior); row != nil {
				row.Commands = append(row.Commands, obligation.Command)
				row.TestCases = append(row.TestCases, obligation.ID)
			}
		}
		attachDraftTraceabilityReview(draft, byObligation)
	}
	if accepted := plan.AcceptedCoverage; accepted != nil {
		for _, surface := range accepted.SurfaceInventory {
			attachSurfaceTraceabilityReview(featurePlanSurfaceReviewFromAccepted(surface), byObligation)
		}
		for _, obligation := range accepted.TestObligations {
			if row := ensureBehavior(obligation.ID, obligation.Behavior); row != nil {
				row.Commands = append(row.Commands, obligation.Command)
				row.TestCases = append(row.TestCases, obligation.ID)
			}
		}
		attachAcceptedTraceabilityReview(accepted, byObligation)
	}
	for _, atom := range plan.Atoms {
		title := firstCoverageText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		refs := normalizedStringList(append(append(append([]string(nil), atom.TDDObligations...), atom.TestFirst.ObligationRefs...), atom.PlannedTDDSnapshot.ObligationIDs...))
		for _, ref := range refs {
			row := byObligation(ref)
			if row == nil {
				continue
			}
			row.Acceptance = append(row.Acceptance, atom.SDDRefs...)
			row.TestFiles = append(row.TestFiles, atom.TestFirst.TestArtifactPaths...)
			row.TestFiles = append(row.TestFiles, atom.PlannedTDDSnapshot.TestArtifactPaths...)
			row.Commands = append(row.Commands, atom.TestFirst.Commands...)
			row.Commands = append(row.Commands, atom.RequiredTests...)
			row.Commands = append(row.Commands, atom.PlannedTDDSnapshot.TestCommands...)
			row.AtomTitles = append(row.AtomTitles, title)
		}
	}

	review := FeaturePlanTraceabilityReview{}
	for _, key := range behaviorOrder {
		row := behaviorByID[key]
		if row == nil {
			continue
		}
		row.Requirements = normalizedStringList(row.Requirements)
		row.Acceptance = normalizedStringList(row.Acceptance)
		row.Scenarios = normalizedStringList(row.Scenarios)
		row.Variants = normalizedStringList(row.Variants)
		row.Invariants = normalizedStringList(row.Invariants)
		row.TestCases = normalizedStringList(row.TestCases)
		row.TestFiles = normalizedStringList(row.TestFiles)
		row.Commands = normalizedStringList(row.Commands)
		row.AtomTitles = normalizedStringList(row.AtomTitles)
		row.Surfaces = normalizedStringList(row.Surfaces)
		row.MissingTests = len(row.TestCases) == 0
		review.Behaviors = append(review.Behaviors, *row)
		review.VariantCount += len(row.Variants)
		review.InvariantRefs = append(review.InvariantRefs, row.Invariants...)
		review.TestCaseRefs = append(review.TestCaseRefs, row.TestCases...)
		review.TestFiles = append(review.TestFiles, row.TestFiles...)
		review.Commands = append(review.Commands, row.Commands...)
	}
	review.InvariantRefs = normalizedStringList(review.InvariantRefs)
	review.TestCaseRefs = normalizedStringList(review.TestCaseRefs)
	review.TestFiles = normalizedStringList(review.TestFiles)
	review.Commands = normalizedStringList(review.Commands)
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, surface := range draft.SurfaceInventory {
			review.Surfaces = append(review.Surfaces, featurePlanSurfaceReviewFromDraft(surface))
		}
	}
	if accepted := plan.AcceptedCoverage; accepted != nil {
		for _, surface := range accepted.SurfaceInventory {
			review.Surfaces = append(review.Surfaces, featurePlanSurfaceReviewFromAccepted(surface))
		}
	}
	return review
}

func attachSurfaceTraceabilityReview(surface FeaturePlanSurfaceReview, byObligation func(string) *FeaturePlanBehaviorReview) {
	for _, obligation := range surface.Obligations {
		if row := byObligation(obligation); row != nil {
			row.Surfaces = append(row.Surfaces, featurePlanSurfaceLabel(surface))
		}
	}
}

func appendBehaviorOrderForSurfaceMissingObligations(order []string, rows map[string]*FeaturePlanBehaviorReview, surface FeaturePlanSurfaceReview) []string {
	if !surface.MissingObligations {
		return order
	}
	key := "surface:" + featurePlanSurfaceLabel(surface)
	if rows[key] != nil {
		return order
	}
	rows[key] = &FeaturePlanBehaviorReview{
		ID:           surface.ID,
		Behavior:     surface.Surface,
		Surfaces:     []string{featurePlanSurfaceLabel(surface)},
		MissingTests: true,
	}
	return append(order, key)
}

func featurePlanSurfaceReviewFromDraft(surface CoverageDraftSurfaceDecision) FeaturePlanSurfaceReview {
	status := normalizeStandaloneSurfaceStatus(surface.Status)
	obligations := normalizedStringList(surface.Obligations)
	return FeaturePlanSurfaceReview{
		ID:                 strings.TrimSpace(surface.ID),
		Surface:            strings.TrimSpace(surface.Surface),
		Status:             status,
		Rationale:          strings.TrimSpace(surface.Rationale),
		Obligations:        obligations,
		EvidenceRefs:       normalizedStringList(surface.EvidenceRefs),
		QuestionRefs:       normalizedStringList(surface.QuestionRefs),
		MissingObligations: status == "accepted" && len(obligations) == 0,
		OpenQuestion:       status == "open_question",
	}
}

func featurePlanSurfaceReviewFromAccepted(surface AcceptedCoverageSurfaceDecision) FeaturePlanSurfaceReview {
	status := normalizeStandaloneSurfaceStatus(surface.Status)
	obligations := normalizedStringList(surface.Obligations)
	return FeaturePlanSurfaceReview{
		ID:                 strings.TrimSpace(surface.ID),
		Surface:            strings.TrimSpace(surface.Surface),
		Status:             status,
		Rationale:          strings.TrimSpace(surface.Rationale),
		Obligations:        obligations,
		EvidenceRefs:       normalizedStringList(surface.EvidenceRefs),
		QuestionRefs:       normalizedStringList(surface.QuestionRefs),
		MissingObligations: status == "accepted" && len(obligations) == 0,
		OpenQuestion:       status == "open_question",
	}
}

func featurePlanSurfaceLabel(surface FeaturePlanSurfaceReview) string {
	label := firstCoverageText(surface.ID, surface.Surface, "surface")
	if surface.Surface != "" && surface.Surface != label {
		label += ": " + surface.Surface
	}
	if surface.Status != "" {
		label += " [" + surface.Status + "]"
	}
	return label
}

func attachDraftTraceabilityReview(draft *BehavioralCoverageDraft, byObligation func(string) *FeaturePlanBehaviorReview) {
	for _, scenario := range draft.Scenarios {
		label := firstCoverageText(scenario.ID, "scenario")
		for _, obligation := range scenario.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Requirements = append(row.Requirements, scenario.RequirementID)
				row.Scenarios = append(row.Scenarios, label)
				row.Variants = append(row.Variants, label)
			}
		}
	}
	for _, invariant := range draft.Invariants {
		label := firstCoverageText(invariant.ID, invariant.Statement, "invariant")
		for _, obligation := range invariant.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Invariants = append(row.Invariants, label)
			}
		}
	}
	for _, mode := range draft.FailureModes {
		label := firstCoverageText(mode.ID, mode.Mode, "failure mode")
		for _, obligation := range mode.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Variants = append(row.Variants, label)
			}
		}
	}
	for _, machine := range draft.StateMachines {
		label := firstCoverageText(machine.ID, "state machine")
		for _, obligation := range machine.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Variants = append(row.Variants, label)
			}
		}
	}
	for _, target := range draft.FuzzTargets {
		attachTraceabilityTestRefs(byObligation, target.Obligations, target.TestRefs, firstCoverageText(target.ID, target.Target, "fuzz target"), target.Invariants)
	}
	for _, eval := range draft.RAGEvals {
		attachTraceabilityTestRefs(byObligation, eval.Obligations, eval.EvalRefs, firstCoverageText(eval.ID, "rag eval"), nil)
	}
	for _, security := range draft.SecurityCases {
		attachTraceabilityTestRefs(byObligation, security.Obligations, security.TestRefs, firstCoverageText(security.ID, security.Risk, "security case"), nil)
	}
	for _, privacy := range draft.PrivacyControls {
		attachTraceabilityTestRefs(byObligation, privacy.Obligations, privacy.TestRefs, firstCoverageText(privacy.ID, privacy.Policy, "privacy control"), nil)
	}
	for _, contract := range draft.ProviderContracts {
		attachTraceabilityTestRefs(byObligation, contract.Obligations, contract.TestRefs, firstCoverageText(contract.ID, contract.Provider, "provider contract"), nil)
	}
	for _, signal := range draft.Observability {
		attachTraceabilityTestRefs(byObligation, signal.Obligations, signal.TestRefs, firstCoverageText(signal.ID, signal.Signal, "observability signal"), nil)
	}
	for _, bound := range draft.PerformanceBounds {
		for _, obligation := range bound.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Variants = append(row.Variants, firstCoverageText(bound.ID, bound.Operation, "performance bound"))
			}
		}
	}
	for _, journey := range draft.E2EJourneys {
		attachTraceabilityTestRefs(byObligation, journey.Obligations, journey.ScriptRefs, firstCoverageText(journey.ID, journey.Journey, "e2e journey"), nil)
	}
}

func attachAcceptedTraceabilityReview(coverage *AcceptedBehavioralCoverage, byObligation func(string) *FeaturePlanBehaviorReview) {
	for _, scenario := range coverage.Scenarios {
		label := firstCoverageText(scenario.ID, "scenario")
		for _, obligation := range scenario.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Requirements = append(row.Requirements, scenario.RequirementID)
				row.Scenarios = append(row.Scenarios, label)
				row.Variants = append(row.Variants, label)
			}
		}
	}
	for _, contract := range coverage.DesignContracts {
		attachTraceabilityTestRefs(byObligation, contract.Obligations, contract.TestRefs, firstCoverageText(contract.ID, contract.Boundary, "design contract"), contract.Invariants)
	}
	for _, invariant := range coverage.Invariants {
		label := firstCoverageText(invariant.ID, invariant.Statement, "invariant")
		for _, obligation := range invariant.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Invariants = append(row.Invariants, label)
				row.TestCases = append(row.TestCases, invariant.PropertyBasedTestRefs...)
			}
		}
	}
	for _, mode := range coverage.FailureModes {
		attachTraceabilityTestRefs(byObligation, mode.Obligations, mode.MitigationTestRefs, firstCoverageText(mode.ID, mode.Mode, "failure mode"), nil)
	}
	for _, deviation := range coverage.HAZOPDeviations {
		attachTraceabilityTestRefs(byObligation, deviation.Obligations, deviation.TestRefs, firstCoverageText(deviation.ID, deviation.Deviation, "hazop deviation"), nil)
	}
	for _, leaf := range coverage.FaultTreeLeaves {
		attachTraceabilityTestRefs(byObligation, leaf.Obligations, leaf.TestRefs, firstCoverageText(leaf.ID, leaf.Cause, "fault tree leaf"), nil)
	}
	for _, table := range coverage.DecisionTables {
		attachTraceabilityTestRefs(byObligation, table.Obligations, table.TestRefs, firstCoverageText(table.ID, "decision table"), nil)
	}
	for _, boundary := range coverage.BoundaryValues {
		attachTraceabilityTestRefs(byObligation, boundary.Obligations, boundary.TestRefs, firstCoverageText(boundary.ID, boundary.Parameter, "boundary value"), nil)
	}
	for _, machine := range coverage.StateMachines {
		label := firstCoverageText(machine.ID, "state machine")
		for _, obligation := range machine.Obligations {
			if row := byObligation(obligation); row != nil {
				row.Variants = append(row.Variants, label)
			}
		}
		for _, transition := range machine.Transitions {
			if len(transition.TestRefs) == 0 {
				continue
			}
			for _, obligation := range machine.Obligations {
				if row := byObligation(obligation); row != nil {
					row.TestCases = append(row.TestCases, transition.TestRefs...)
				}
			}
		}
	}
	for _, postMortem := range coverage.PostMortems {
		attachTraceabilityTestRefs(byObligation, postMortem.Obligations, postMortem.CatchingTestRefs, firstCoverageText(postMortem.ID, postMortem.Incident, "postmortem"), nil)
	}
	for _, target := range coverage.FuzzTargets {
		attachTraceabilityTestRefs(byObligation, target.Obligations, target.TestRefs, firstCoverageText(target.ID, target.Target, "fuzz target"), target.Invariants)
	}
	for _, eval := range coverage.RAGEvals {
		attachTraceabilityTestRefs(byObligation, eval.Obligations, eval.EvalRefs, firstCoverageText(eval.ID, "rag eval"), nil)
	}
	for _, security := range coverage.SecurityCases {
		attachTraceabilityTestRefs(byObligation, security.Obligations, security.TestRefs, firstCoverageText(security.ID, security.Risk, "security case"), nil)
	}
	for _, privacy := range coverage.PrivacyControls {
		attachTraceabilityTestRefs(byObligation, privacy.Obligations, privacy.TestRefs, firstCoverageText(privacy.ID, privacy.Policy, "privacy control"), nil)
	}
	for _, contract := range coverage.ProviderContracts {
		attachTraceabilityTestRefs(byObligation, contract.Obligations, contract.TestRefs, firstCoverageText(contract.ID, contract.Provider, "provider contract"), nil)
	}
	for _, signal := range coverage.Observability {
		attachTraceabilityTestRefs(byObligation, signal.Obligations, signal.TestRefs, firstCoverageText(signal.ID, signal.Signal, "observability signal"), nil)
	}
	for _, journey := range coverage.E2EJourneys {
		attachTraceabilityTestRefs(byObligation, journey.Obligations, journey.ScriptRefs, firstCoverageText(journey.ID, journey.Journey, "e2e journey"), nil)
	}
}

func attachTraceabilityTestRefs(byObligation func(string) *FeaturePlanBehaviorReview, obligations []string, testRefs []string, variant string, invariants []string) {
	for _, obligation := range obligations {
		row := byObligation(obligation)
		if row == nil {
			continue
		}
		row.Variants = append(row.Variants, variant)
		row.Invariants = append(row.Invariants, invariants...)
		row.TestCases = append(row.TestCases, testRefs...)
	}
}
