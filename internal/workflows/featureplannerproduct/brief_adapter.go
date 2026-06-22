package featureplannerproduct

import (
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/productworkflow"
)

func BriefFromImplementationHandoff(handoff productworkflow.ImplementationHandoff) *featureplanner.ProductWorkflowBrief {
	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:                  handoff.EffortID,
		TaskID:                    handoff.TaskID,
		EffortRoot:                handoff.EffortRoot,
		ArtifactRootPath:          handoff.ArtifactRootPath,
		StatusBucket:              handoff.StatusBucket,
		CurrentPhase:              handoff.CurrentPhase,
		CurrentState:              handoff.CurrentState,
		NextValidActions:          append([]string(nil), handoff.NextValidActions...),
		CoverageTier:              handoff.CoverageTier,
		CoverageGatePassed:        handoff.CoverageGatePassed,
		CoverageContract:          coverageContractRefFromHandoff(handoff.CoverageContract),
		AcceptedCoverage:          acceptedCoverageFromHandoff(handoff.AcceptedCoverage),
		CoverageBlockers:          append([]string(nil), handoff.CoverageBlockers...),
		CoverageBlockerKinds:      append([]string(nil), handoff.CoverageBlockerKinds...),
		AcceptedADRIDs:            append([]string(nil), handoff.AcceptedADRIDs...),
		AcceptedADRPaths:          append([]string(nil), handoff.AcceptedADRPaths...),
		FilesToModify:             append([]string(nil), handoff.FilesToModify...),
		ContextEvidencePaths:      append([]string(nil), handoff.ContextEvidencePaths...),
		RequiredBehavior:          append([]string(nil), handoff.RequiredBehavior...),
		Tests:                     append([]string(nil), handoff.Tests...),
		Acceptance:                append([]string(nil), handoff.Acceptance...),
		Constraints:               append([]string(nil), handoff.Constraints...),
		Decisions:                 append([]string(nil), handoff.Decisions...),
		Blockers:                  append([]string(nil), handoff.Blockers...),
		AuthorizedNewPackageRoots: append([]string(nil), handoff.AuthorizedNewPackageRoots...),
	}
	for _, req := range handoff.PRDRequirements {
		brief.PRDRequirements = append(brief.PRDRequirements, featureplanner.PRDRequirementBrief{
			ID:          req.ID,
			Priority:    req.Priority,
			Statement:   req.Statement,
			Testability: req.Testability,
			Metric:      req.Metric,
			Obligations: append([]string(nil), req.Obligations...),
		})
	}

	for _, obligation := range handoff.TDDObligations {
		brief.TDDObligations = append(brief.TDDObligations, featureplanner.TDDObligationBrief{
			ID:             obligation.ID,
			Behavior:       obligation.Behavior,
			Command:        obligation.Command,
			RequiredBefore: obligation.RequiredBefore,
			Status:         obligation.Status,
		})
	}

	return brief
}

func coverageContractRefFromHandoff(ref *productworkflow.ImplementationHandoffCoverageContract) *featureplanner.CoverageContractRef {
	if ref == nil {
		return nil
	}
	return &featureplanner.CoverageContractRef{
		ContractID:   ref.ContractID,
		Digest:       ref.Digest,
		EventID:      ref.EventID,
		Status:       ref.Status,
		EffortID:     ref.EffortID,
		CoverageTier: ref.CoverageTier,
	}
}

func acceptedCoverageFromHandoff(coverage *productworkflow.ImplementationHandoffAcceptedCoverage) *featureplanner.AcceptedBehavioralCoverage {
	if coverage == nil {
		return nil
	}

	ref := coverageContractRefFromHandoff(&coverage.Contract)
	out := &featureplanner.AcceptedBehavioralCoverage{
		Authority: "product_workflow",
		Status:    coverage.Status,
		Tier:      coverage.Contract.CoverageTier,
	}
	if ref != nil {
		out.Contract = *ref
	}

	contract := coverage.Behavioral
	for _, req := range contract.EARS {
		if !acceptedProductCoverageStatus(req.Status) {
			continue
		}
		out.Requirements = append(out.Requirements, featureplanner.AcceptedCoverageRequirement{
			ID:          req.ID,
			Pattern:     req.Pattern,
			Statement:   req.Statement,
			Refs:        append([]string(nil), req.MappedRefs...),
			Obligations: append([]string(nil), req.Obligations...),
		})
	}
	for _, rule := range contract.ExampleRules {
		mapped := featureplanner.AcceptedCoverageExampleRule{
			ID:            rule.ID,
			RequirementID: rule.RequirementID,
			Rule:          rule.Rule,
			Questions:     append([]string(nil), rule.Questions...),
		}
		for _, example := range rule.Examples {
			mapped.Examples = append(mapped.Examples, featureplanner.AcceptedCoverageExample{
				ID:               example.ID,
				Description:      example.Description,
				ConcreteDataRefs: append([]string(nil), example.ConcreteDataRefs...),
				Negative:         example.Negative,
			})
		}
		out.ExampleRules = append(out.ExampleRules, mapped)
	}
	for _, scenario := range contract.BDDScenarios {
		if !acceptedProductCoverageStatus(scenario.Status) {
			continue
		}
		out.Scenarios = append(out.Scenarios, featureplanner.AcceptedCoverageScenario{
			ID:               scenario.ID,
			RequirementID:    scenario.RequirementID,
			RuleID:           scenario.RuleID,
			Given:            append([]string(nil), scenario.Given...),
			When:             append([]string(nil), scenario.When...),
			Then:             append([]string(nil), scenario.Then...),
			ConcreteDataRefs: append([]string(nil), scenario.ConcreteDataRefs...),
			Obligations:      append([]string(nil), scenario.Obligations...),
			Tags:             append([]string(nil), scenario.Tags...),
		})
	}
	for _, dc := range contract.DesignContracts {
		if !acceptedProductCoverageStatus(dc.Status) {
			continue
		}
		out.DesignContracts = append(out.DesignContracts, featureplanner.AcceptedCoverageDesignContract{
			ID:             dc.ID,
			Boundary:       dc.Boundary,
			Preconditions:  append([]string(nil), dc.Preconditions...),
			Postconditions: append([]string(nil), dc.Postconditions...),
			Invariants:     append([]string(nil), dc.Invariants...),
			TestRefs:       append([]string(nil), dc.TestRefs...),
			Obligations:    append([]string(nil), dc.Obligations...),
		})
	}
	for _, invariant := range contract.Invariants {
		if !acceptedProductCoverageStatus(invariant.Status) {
			continue
		}
		out.Invariants = append(out.Invariants, featureplanner.AcceptedCoverageInvariant{
			ID:                    invariant.ID,
			Statement:             invariant.Statement,
			PropertyBasedTestRefs: append([]string(nil), invariant.PropertyBasedTestRefs...),
			Obligations:           append([]string(nil), invariant.Obligations...),
		})
	}
	for _, mode := range contract.FailureModes {
		if !acceptedProductCoverageStatus(mode.Status) {
			continue
		}
		out.FailureModes = append(out.FailureModes, featureplanner.AcceptedCoverageFailureMode{
			ID:                 mode.ID,
			Mode:               mode.Mode,
			Mitigation:         mode.Mitigation,
			RPN:                mode.RPN,
			MitigationTestRefs: append([]string(nil), mode.MitigationTestRefs...),
			Obligations:        append([]string(nil), mode.Obligations...),
		})
	}
	for _, deviation := range contract.HAZOPDeviations {
		if !acceptedProductCoverageStatus(deviation.Status) {
			continue
		}
		out.HAZOPDeviations = append(out.HAZOPDeviations, featureplanner.AcceptedCoverageHAZOPDeviation{
			ID:           deviation.ID,
			GuideWord:    deviation.GuideWord,
			WorkflowStep: deviation.WorkflowStep,
			Deviation:    deviation.Deviation,
			Severity:     deviation.Severity,
			TestRefs:     append([]string(nil), deviation.TestRefs...),
			Obligations:  append([]string(nil), deviation.Obligations...),
		})
	}
	for _, leaf := range contract.FaultTreeLeaves {
		if !acceptedProductCoverageStatus(leaf.Status) {
			continue
		}
		out.FaultTreeLeaves = append(out.FaultTreeLeaves, featureplanner.AcceptedCoverageFaultTreeLeaf{
			ID:          leaf.ID,
			TopEvent:    leaf.TopEvent,
			Cause:       leaf.Cause,
			TestRefs:    append([]string(nil), leaf.TestRefs...),
			Obligations: append([]string(nil), leaf.Obligations...),
		})
	}
	for _, table := range contract.DecisionTables {
		if !acceptedProductCoverageStatus(table.Status) {
			continue
		}
		mapped := featureplanner.AcceptedCoverageDecisionTable{
			ID:          table.ID,
			Conditions:  append([]string(nil), table.Conditions...),
			TestRefs:    append([]string(nil), table.TestRefs...),
			Obligations: append([]string(nil), table.Obligations...),
		}
		for _, rule := range table.Rules {
			mapped.Rules = append(mapped.Rules, featureplanner.AcceptedCoverageDecisionRule{
				ID:         rule.ID,
				Conditions: copyStringMap(rule.Conditions),
				Expected:   rule.Expected,
			})
		}
		out.DecisionTables = append(out.DecisionTables, mapped)
	}
	for _, boundary := range contract.BoundaryValues {
		if !acceptedProductCoverageStatus(boundary.Status) {
			continue
		}
		out.BoundaryValues = append(out.BoundaryValues, featureplanner.AcceptedCoverageBoundaryValue{
			ID:          boundary.ID,
			Parameter:   boundary.Parameter,
			Classes:     append([]string(nil), boundary.Classes...),
			Values:      append([]string(nil), boundary.Values...),
			TestRefs:    append([]string(nil), boundary.TestRefs...),
			Obligations: append([]string(nil), boundary.Obligations...),
		})
	}
	for _, machine := range contract.StateMachines {
		if !acceptedProductCoverageStatus(machine.Status) {
			continue
		}
		mapped := featureplanner.AcceptedCoverageStateMachine{
			ID:     machine.ID,
			States: append([]string(nil), machine.States...),
		}
		for _, transition := range machine.Transitions {
			mapped.Transitions = append(mapped.Transitions, featureplanner.AcceptedCoverageTransition{
				ID:       transition.ID,
				From:     transition.From,
				Event:    transition.Event,
				To:       transition.To,
				TestRefs: append([]string(nil), transition.TestRefs...),
			})
		}
		out.StateMachines = append(out.StateMachines, mapped)
	}
	for _, incident := range contract.PostMortems {
		if !acceptedProductCoverageStatus(incident.Status) {
			continue
		}
		out.PostMortems = append(out.PostMortems, featureplanner.AcceptedCoveragePostMortem{
			ID:               incident.ID,
			Incident:         incident.Incident,
			CatchingTestRefs: append([]string(nil), incident.CatchingTestRefs...),
			Obligations:      append([]string(nil), incident.Obligations...),
		})
	}
	for _, model := range contract.FormalModels {
		if !acceptedProductCoverageStatus(model.Status) {
			continue
		}
		out.FormalModels = append(out.FormalModels, featureplanner.AcceptedCoverageFormalModel{
			ID:               model.ID,
			Model:            model.Model,
			VerificationRefs: append([]string(nil), model.VerificationRefs...),
			Obligations:      append([]string(nil), model.Obligations...),
		})
	}
	for _, target := range contract.FuzzTargets {
		if !acceptedProductCoverageStatus(target.Status) {
			continue
		}
		out.FuzzTargets = append(out.FuzzTargets, featureplanner.AcceptedCoverageFuzzTarget{
			ID:          target.ID,
			Status:      target.Status,
			Target:      target.Target,
			Generators:  append([]string(nil), target.Generators...),
			Invariants:  append([]string(nil), target.Invariants...),
			TestRefs:    append([]string(nil), target.TestRefs...),
			Obligations: append([]string(nil), target.Obligations...),
		})
	}
	for _, eval := range contract.RAGEvals {
		if !acceptedProductCoverageStatus(eval.Status) {
			continue
		}
		out.RAGEvals = append(out.RAGEvals, featureplanner.AcceptedCoverageRAGEval{
			ID:          eval.ID,
			Status:      eval.Status,
			DatasetRefs: append([]string(nil), eval.DatasetRefs...),
			Metrics:     copyFloat64Map(eval.Metrics),
			Thresholds:  copyFloat64Map(eval.Thresholds),
			EvalRefs:    append([]string(nil), eval.EvalRefs...),
			Obligations: append([]string(nil), eval.Obligations...),
		})
	}
	for _, securityCase := range contract.SecurityCases {
		if !acceptedProductCoverageStatus(securityCase.Status) {
			continue
		}
		out.SecurityCases = append(out.SecurityCases, featureplanner.AcceptedCoverageSecurityCase{
			ID:          securityCase.ID,
			Status:      securityCase.Status,
			Risk:        securityCase.Risk,
			Attack:      securityCase.Attack,
			Expected:    securityCase.Expected,
			TestRefs:    append([]string(nil), securityCase.TestRefs...),
			Obligations: append([]string(nil), securityCase.Obligations...),
		})
	}
	for _, privacy := range contract.PrivacyControls {
		if !acceptedProductCoverageStatus(privacy.Status) {
			continue
		}
		out.PrivacyControls = append(out.PrivacyControls, featureplanner.AcceptedCoveragePrivacyControl{
			ID:            privacy.ID,
			Status:        privacy.Status,
			DataClass:     privacy.DataClass,
			Policy:        privacy.Policy,
			RedactionRefs: append([]string(nil), privacy.RedactionRefs...),
			Retention:     privacy.Retention,
			TestRefs:      append([]string(nil), privacy.TestRefs...),
			Obligations:   append([]string(nil), privacy.Obligations...),
		})
	}
	for _, provider := range contract.ProviderContracts {
		if !acceptedProductCoverageStatus(provider.Status) {
			continue
		}
		out.ProviderContracts = append(out.ProviderContracts, featureplanner.AcceptedCoverageProviderContract{
			ID:           provider.ID,
			Status:       provider.Status,
			Provider:     provider.Provider,
			Consumer:     provider.Consumer,
			ContractRefs: append([]string(nil), provider.ContractRefs...),
			TestRefs:     append([]string(nil), provider.TestRefs...),
			Obligations:  append([]string(nil), provider.Obligations...),
		})
	}
	for _, signal := range contract.Observability {
		if !acceptedProductCoverageStatus(signal.Status) {
			continue
		}
		out.Observability = append(out.Observability, featureplanner.AcceptedCoverageObservability{
			ID:               signal.ID,
			Status:           signal.Status,
			Signal:           signal.Signal,
			InstrumentedPath: signal.InstrumentedPath,
			RequiredFields:   append([]string(nil), signal.RequiredFields...),
			TestRefs:         append([]string(nil), signal.TestRefs...),
			Obligations:      append([]string(nil), signal.Obligations...),
		})
	}
	for _, bound := range contract.PerformanceBounds {
		if !acceptedProductCoverageStatus(bound.Status) {
			continue
		}
		out.PerformanceBounds = append(out.PerformanceBounds, featureplanner.AcceptedCoveragePerformanceBound{
			ID:              bound.ID,
			Status:          bound.Status,
			Operation:       bound.Operation,
			Bound:           bound.Bound,
			MeasurementRefs: append([]string(nil), bound.MeasurementRefs...),
			Obligations:     append([]string(nil), bound.Obligations...),
		})
	}
	for _, journey := range contract.E2EJourneys {
		if !acceptedProductCoverageStatus(journey.Status) {
			continue
		}
		out.E2EJourneys = append(out.E2EJourneys, featureplanner.AcceptedCoverageE2EJourney{
			ID:          journey.ID,
			Status:      journey.Status,
			Journey:     journey.Journey,
			Steps:       append([]string(nil), journey.Steps...),
			ScriptRefs:  append([]string(nil), journey.ScriptRefs...),
			Obligations: append([]string(nil), journey.Obligations...),
		})
	}
	for _, obligation := range contract.TestObligations {
		out.TestObligations = append(out.TestObligations, featureplanner.AcceptedCoverageTestObligation{
			ID:             obligation.ID,
			Type:           obligation.Type,
			Behavior:       obligation.Behavior,
			Owner:          obligation.Owner,
			Command:        obligation.Command,
			RequiredBefore: obligation.RequiredBefore,
		})
	}
	for _, verification := range contract.Verification {
		out.Verification = append(out.Verification, featureplanner.AcceptedCoverageVerification{
			ID:          verification.ID,
			Gate:        verification.Gate,
			Command:     verification.Command,
			Obligations: append([]string(nil), verification.Obligations...),
		})
	}
	out.Mutation = featureplanner.AcceptedCoverageMutation{
		Requirement:  contract.Mutation.Requirement,
		MinScore:     contract.Mutation.MinScore,
		Score:        contract.Mutation.Score,
		WaiverReason: contract.Mutation.WaiverReason,
	}
	for _, feedback := range contract.Feedback {
		out.Feedback = append(out.Feedback, featureplanner.AcceptedCoverageFeedback{
			ID:             feedback.ID,
			Outcome:        feedback.Outcome,
			ObligationRefs: append([]string(nil), feedback.ObligationRefs...),
		})
	}
	for _, waiver := range contract.Waivers {
		out.Waivers = append(out.Waivers, featureplanner.AcceptedCoverageWaiver{
			ID:        waiver.ID,
			Technique: waiver.Technique,
			Reason:    waiver.Reason,
		})
	}
	for _, question := range contract.OpenQuestions {
		out.Questions = append(out.Questions, featureplanner.AcceptedCoverageQuestion{
			ID:         question.ID,
			Question:   question.Question,
			Status:     question.Status,
			Resolution: question.Resolution,
		})
	}

	return out
}

func acceptedProductCoverageStatus(status string) bool {
	return productworkflow.BehavioralCoverageStatusIsAccepted(status)
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyFloat64Map(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
