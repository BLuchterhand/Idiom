package featureplanner

import (
	"fmt"
	"sort"
	"strings"

	"idiom/internal/workflows/productworkflow"
)

func validateStandaloneBehavioralCoverageDraftContract(plan FeaturePlan) []error {
	if plan.BehavioralCoverageDraft == nil {
		return nil
	}

	contract := standaloneBehavioralCoverageDraftContract(plan)
	if err := productworkflow.ValidateBehavioralCoverageContract(contract); err != nil {
		return []error{fmt.Errorf("behavioral_coverage_draft violates canonical behavioral coverage contract: %w", err)}
	}

	return nil
}

func standaloneBehavioralCoverageDraftContract(plan FeaturePlan) productworkflow.BehavioralCoverageContract {
	draft := plan.BehavioralCoverageDraft
	if draft == nil {
		return productworkflow.BehavioralCoverageContract{}
	}

	contract := productworkflow.BehavioralCoverageContract{
		SchemaVersion: "standalone_provisional.v1",
		ContractID:    firstNonEmpty(plan.FeatureID, "standalone-provisional"),
		TaskID:        firstNonEmpty(plan.FeatureID, "standalone-provisional"),
		CoverageTier:  firstNonEmpty(draft.Tier, plan.TDDContract.CoverageTier),
		SourceRefs:    standaloneDraftSourceRefs(plan),
	}

	for _, requirement := range draft.Requirements {
		contract.EARS = append(contract.EARS, productworkflow.EARSRequirementCoverage{
			ID:          strings.TrimSpace(requirement.ID),
			Pattern:     strings.TrimSpace(requirement.Pattern),
			Statement:   strings.TrimSpace(requirement.Statement),
			Status:      productworkflow.BehavioralCoverageStatusAccepted,
			Obligations: nonEmptyFeaturePlannerStrings(requirement.Obligations),
		})
	}

	for _, rule := range draft.ExampleRules {
		out := productworkflow.ExampleRuleCoverage{
			ID:            strings.TrimSpace(rule.ID),
			RequirementID: strings.TrimSpace(rule.RequirementID),
			Rule:          strings.TrimSpace(rule.Rule),
		}
		for _, example := range rule.Examples {
			out.Examples = append(out.Examples, productworkflow.ConcreteExampleCoverage{
				ID:               strings.TrimSpace(example.ID),
				Description:      strings.TrimSpace(example.Description),
				ConcreteDataRefs: standaloneConcreteDataRefs(example.ID, example.ConcreteDataRefs, example.ConcreteData),
				Negative:         example.Negative,
			})
		}
		contract.ExampleRules = append(contract.ExampleRules, out)
	}

	for _, scenario := range draft.Scenarios {
		contract.BDDScenarios = append(contract.BDDScenarios, productworkflow.BDDScenarioCoverage{
			ID:               strings.TrimSpace(scenario.ID),
			Status:           productworkflow.BehavioralCoverageStatusAccepted,
			RequirementID:    strings.TrimSpace(scenario.RequirementID),
			RuleID:           strings.TrimSpace(scenario.RuleID),
			Given:            nonEmptyFeaturePlannerStrings(scenario.Given),
			When:             nonEmptyFeaturePlannerStrings(scenario.When),
			Then:             nonEmptyFeaturePlannerStrings(scenario.Then),
			ConcreteDataRefs: standaloneConcreteDataRefs(scenario.ID, scenario.ConcreteDataRefs, scenario.ConcreteData),
			Obligations:      nonEmptyFeaturePlannerStrings(scenario.Obligations),
			Tags:             nonEmptyFeaturePlannerStrings(scenario.Tags),
		})
	}

	for _, invariant := range draft.Invariants {
		contract.Invariants = append(contract.Invariants, productworkflow.InvariantCoverage{
			ID:                    strings.TrimSpace(invariant.ID),
			Status:                productworkflow.BehavioralCoverageStatusAccepted,
			Statement:             strings.TrimSpace(invariant.Statement),
			PropertyBasedTestRefs: nonEmptyFeaturePlannerStrings(invariant.PropertyBasedTestRefs),
			Obligations:           nonEmptyFeaturePlannerStrings(invariant.Obligations),
		})
	}

	for _, mode := range draft.FailureModes {
		contract.FailureModes = append(contract.FailureModes, productworkflow.FailureModeCoverage{
			ID:                 strings.TrimSpace(mode.ID),
			Status:             productworkflow.BehavioralCoverageStatusAccepted,
			Mode:               strings.TrimSpace(mode.Mode),
			Mitigation:         strings.TrimSpace(mode.Mitigation),
			RPN:                mode.RPN,
			MitigationTestRefs: nonEmptyFeaturePlannerStrings(mode.MitigationTestRefs),
			Obligations:        nonEmptyFeaturePlannerStrings(mode.Obligations),
		})
	}

	for _, machine := range draft.StateMachines {
		out := productworkflow.StateMachineCoverage{
			ID:     strings.TrimSpace(machine.ID),
			Status: productworkflow.BehavioralCoverageStatusAccepted,
			States: nonEmptyFeaturePlannerStrings(machine.States),
		}
		for _, transition := range machine.Transitions {
			out.Transitions = append(out.Transitions, productworkflow.StateTransitionCoverage{
				ID:       strings.TrimSpace(transition.ID),
				From:     strings.TrimSpace(transition.From),
				Event:    strings.TrimSpace(transition.Event),
				To:       strings.TrimSpace(transition.To),
				TestRefs: nonEmptyFeaturePlannerStrings(transition.TestRefs),
			})
		}
		contract.StateMachines = append(contract.StateMachines, out)
	}

	for _, target := range draft.FuzzTargets {
		contract.FuzzTargets = append(contract.FuzzTargets, productworkflow.FuzzTargetCoverage{
			ID:          strings.TrimSpace(target.ID),
			Status:      standaloneDraftCoverageStatus(target.Status),
			Target:      strings.TrimSpace(target.Target),
			Generators:  nonEmptyFeaturePlannerStrings(target.Generators),
			Invariants:  nonEmptyFeaturePlannerStrings(target.Invariants),
			TestRefs:    nonEmptyFeaturePlannerStrings(target.TestRefs),
			Obligations: nonEmptyFeaturePlannerStrings(target.Obligations),
		})
	}
	for _, eval := range draft.RAGEvals {
		contract.RAGEvals = append(contract.RAGEvals, productworkflow.RAGEvaluationCoverage{
			ID:          strings.TrimSpace(eval.ID),
			Status:      standaloneDraftCoverageStatus(eval.Status),
			DatasetRefs: nonEmptyFeaturePlannerStrings(eval.DatasetRefs),
			Metrics:     eval.Metrics,
			Thresholds:  eval.Thresholds,
			EvalRefs:    nonEmptyFeaturePlannerStrings(eval.EvalRefs),
			Obligations: nonEmptyFeaturePlannerStrings(eval.Obligations),
		})
	}
	for _, securityCase := range draft.SecurityCases {
		contract.SecurityCases = append(contract.SecurityCases, productworkflow.SecurityCaseCoverage{
			ID:          strings.TrimSpace(securityCase.ID),
			Status:      standaloneDraftCoverageStatus(securityCase.Status),
			Risk:        strings.TrimSpace(securityCase.Risk),
			Attack:      strings.TrimSpace(securityCase.Attack),
			Expected:    strings.TrimSpace(securityCase.Expected),
			TestRefs:    nonEmptyFeaturePlannerStrings(securityCase.TestRefs),
			Obligations: nonEmptyFeaturePlannerStrings(securityCase.Obligations),
		})
	}
	for _, privacy := range draft.PrivacyControls {
		contract.PrivacyControls = append(contract.PrivacyControls, productworkflow.PrivacyControlCoverage{
			ID:            strings.TrimSpace(privacy.ID),
			Status:        standaloneDraftCoverageStatus(privacy.Status),
			DataClass:     strings.TrimSpace(privacy.DataClass),
			Policy:        strings.TrimSpace(privacy.Policy),
			RedactionRefs: nonEmptyFeaturePlannerStrings(privacy.RedactionRefs),
			Retention:     strings.TrimSpace(privacy.Retention),
			TestRefs:      nonEmptyFeaturePlannerStrings(privacy.TestRefs),
			Obligations:   nonEmptyFeaturePlannerStrings(privacy.Obligations),
		})
	}
	for _, provider := range draft.ProviderContracts {
		contract.ProviderContracts = append(contract.ProviderContracts, productworkflow.ProviderContractCoverage{
			ID:           strings.TrimSpace(provider.ID),
			Status:       standaloneDraftCoverageStatus(provider.Status),
			Provider:     strings.TrimSpace(provider.Provider),
			Consumer:     strings.TrimSpace(provider.Consumer),
			ContractRefs: nonEmptyFeaturePlannerStrings(provider.ContractRefs),
			TestRefs:     nonEmptyFeaturePlannerStrings(provider.TestRefs),
			Obligations:  nonEmptyFeaturePlannerStrings(provider.Obligations),
		})
	}
	for _, signal := range draft.Observability {
		contract.Observability = append(contract.Observability, productworkflow.ObservabilityCoverage{
			ID:               strings.TrimSpace(signal.ID),
			Status:           standaloneDraftCoverageStatus(signal.Status),
			Signal:           strings.TrimSpace(signal.Signal),
			InstrumentedPath: strings.TrimSpace(signal.InstrumentedPath),
			RequiredFields:   nonEmptyFeaturePlannerStrings(signal.RequiredFields),
			TestRefs:         nonEmptyFeaturePlannerStrings(signal.TestRefs),
			Obligations:      nonEmptyFeaturePlannerStrings(signal.Obligations),
		})
	}
	for _, bound := range draft.PerformanceBounds {
		contract.PerformanceBounds = append(contract.PerformanceBounds, productworkflow.PerformanceBoundCoverage{
			ID:              strings.TrimSpace(bound.ID),
			Status:          standaloneDraftCoverageStatus(bound.Status),
			Operation:       strings.TrimSpace(bound.Operation),
			Bound:           strings.TrimSpace(bound.Bound),
			MeasurementRefs: nonEmptyFeaturePlannerStrings(bound.MeasurementRefs),
			Obligations:     nonEmptyFeaturePlannerStrings(bound.Obligations),
		})
	}
	for _, journey := range draft.E2EJourneys {
		contract.E2EJourneys = append(contract.E2EJourneys, productworkflow.E2EJourneyCoverage{
			ID:          strings.TrimSpace(journey.ID),
			Status:      standaloneDraftCoverageStatus(journey.Status),
			Journey:     strings.TrimSpace(journey.Journey),
			Steps:       nonEmptyFeaturePlannerStrings(journey.Steps),
			ScriptRefs:  nonEmptyFeaturePlannerStrings(journey.ScriptRefs),
			Obligations: nonEmptyFeaturePlannerStrings(journey.Obligations),
		})
	}
	for _, surface := range draft.SurfaceInventory {
		contract.SurfaceInventory = append(contract.SurfaceInventory, productworkflow.BehavioralSurfaceCoverage{
			ID:           strings.TrimSpace(surface.ID),
			Surface:      strings.TrimSpace(surface.Surface),
			Status:       productworkflow.NormalizeBehavioralSurfaceStatus(surface.Status),
			Rationale:    strings.TrimSpace(surface.Rationale),
			EvidenceRefs: nonEmptyFeaturePlannerStrings(surface.EvidenceRefs),
			Obligations:  nonEmptyFeaturePlannerStrings(surface.Obligations),
			QuestionRefs: nonEmptyFeaturePlannerStrings(surface.QuestionRefs),
		})
	}

	for _, obligation := range draft.TestObligations {
		contract.TestObligations = append(contract.TestObligations, productworkflow.TestObligationCoverage{
			ID:             strings.TrimSpace(obligation.ID),
			Type:           strings.TrimSpace(obligation.Type),
			Behavior:       strings.TrimSpace(obligation.Behavior),
			Owner:          strings.TrimSpace(obligation.Owner),
			Command:        strings.TrimSpace(obligation.Command),
			RequiredBefore: strings.TrimSpace(obligation.RequiredBefore),
		})
	}
	for _, gate := range draft.Verification {
		contract.Verification = append(contract.Verification, productworkflow.VerificationGateCoverage{
			ID:          strings.TrimSpace(gate.ID),
			Gate:        strings.TrimSpace(gate.Gate),
			Command:     strings.TrimSpace(gate.Command),
			Obligations: nonEmptyFeaturePlannerStrings(gate.Obligations),
		})
	}
	for _, waiver := range draft.Waivers {
		contract.Waivers = append(contract.Waivers, productworkflow.BehavioralCoverageWaiver{
			ID:        strings.TrimSpace(waiver.ID),
			Technique: strings.TrimSpace(waiver.Technique),
			Reason:    strings.TrimSpace(waiver.Reason),
		})
	}

	return contract
}

func standaloneDraftSourceRefs(plan FeaturePlan) []string {
	var refs []string
	refs = append(refs, plan.SDDContract.Refs...)
	refs = append(refs, plan.SDDContract.PRDRequirementRefs...)
	refs = append(refs, plan.SDDContract.ADRRefs...)
	for _, acceptance := range plan.SDDContract.Acceptance {
		if id := acceptanceID(acceptance); id != "" {
			refs = append(refs, id)
		}
	}
	return nonEmptyFeaturePlannerStrings(refs)
}

func standaloneDraftCoverageStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "", "accepted", "planned", "covered", "tested", "measured", "scripted", "draft", "provisional":
		return productworkflow.BehavioralCoverageStatusAccepted
	case productworkflow.BehavioralCoverageStatusWaived:
		return productworkflow.BehavioralCoverageStatusWaived
	case productworkflow.BehavioralCoverageStatusNotRequired:
		return productworkflow.BehavioralCoverageStatusNotRequired
	default:
		return strings.TrimSpace(strings.ToLower(status))
	}
}

func standaloneConcreteDataRefs(id string, explicit []string, data map[string]string) []string {
	refs := nonEmptyFeaturePlannerStrings(explicit)
	if len(refs) > 0 {
		return refs
	}

	id = strings.TrimSpace(id)
	if id == "" {
		id = "example"
	}

	keys := make([]string, 0, len(data))
	for key, value := range data {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		refs = append(refs, fmt.Sprintf("inline:%s:%s", id, strings.TrimSpace(key)))
	}
	return refs
}
