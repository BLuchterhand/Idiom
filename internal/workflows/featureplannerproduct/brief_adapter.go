package featureplannerproduct

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/productworkflow"
)

func LoadBriefFromEffort(effortRoot string) (*featureplanner.ProductWorkflowBrief, error) {
	if strings.TrimSpace(effortRoot) == "" {
		return nil, fmt.Errorf("direct product feature plan is missing effort root")
	}

	handoff, err := productworkflow.LoadImplementationHandoff(effortRoot)
	if err != nil {
		return nil, fmt.Errorf("load product workflow implementation handoff: %w", err)
	}

	return BriefFromImplementationHandoff(handoff), nil
}

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
	if delivery := handoff.VerifiedSourceSatisfiedDelivery(); delivery != nil &&
		strings.TrimSpace(handoff.StatusBucket) == productworkflow.WorkflowStatusBucketClosed &&
		strings.TrimSpace(handoff.CurrentPhase) == string(productworkflow.PhaseComplete) &&
		strings.TrimSpace(handoff.CurrentState) == productworkflow.WorkflowStateClosed &&
		strings.TrimSpace(delivery.EffortID) == strings.TrimSpace(handoff.EffortID) &&
		strings.TrimSpace(delivery.TaskID) == strings.TrimSpace(handoff.TaskID) &&
		productworkflow.ValidateSourceSatisfiedDeliveryCloseout(*delivery) == nil {
		brief.SourceSatisfiedDelivery = &productworkflow.SourceSatisfiedDeliveryCloseout{
			EffortID:         strings.TrimSpace(delivery.EffortID),
			TaskID:           strings.TrimSpace(delivery.TaskID),
			Outcome:          delivery.Outcome,
			SessionID:        strings.TrimSpace(delivery.SessionID),
			CompletionDigest: strings.TrimSpace(delivery.CompletionDigest),
			EvidencePaths:    append([]string(nil), delivery.EvidencePaths...),
			Evidence:         append([]plancontract.SourceSatisfiedEvidenceRef(nil), delivery.Evidence...),
			RecordedAt:       strings.TrimSpace(delivery.RecordedAt),
		}
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

// FinalizeSourceSatisfiedDeliveryFromFeatureState projects a finalized plan
// whose every atom closed through typed source-satisfied verification into the
// product workflow's durable post-planning delivery authority. Artifact
// creation is completed first when the effort is still in TDD synthesis; that
// phase remains distinct from the delivery outcome recorded afterward.
func FinalizeSourceSatisfiedDeliveryFromFeatureState(
	brief *featureplanner.ProductWorkflowBrief,
	state featureplanner.FeatureState,
) (bool, error) {
	if brief == nil {
		return false, fmt.Errorf("source-satisfied product delivery requires product workflow context")
	}
	if strings.TrimSpace(brief.EffortRoot) == "" || strings.TrimSpace(brief.EffortID) == "" || strings.TrimSpace(brief.TaskID) == "" {
		return false, fmt.Errorf("source-satisfied product delivery requires effort root, effort_id, and task_id")
	}
	completion, candidate, err := sourceSatisfiedCompletionFromFinalFeatureState(state)
	if err != nil {
		return false, err
	}
	if !candidate {
		return false, nil
	}
	if strings.TrimSpace(state.EffortID) != strings.TrimSpace(brief.EffortID) {
		return false, fmt.Errorf(
			"source-satisfied feature state effort_id %q does not match product effort_id %q",
			strings.TrimSpace(state.EffortID),
			strings.TrimSpace(brief.EffortID),
		)
	}
	if strings.TrimSpace(state.TaskID) == "" || strings.TrimSpace(state.TaskID) != strings.TrimSpace(brief.TaskID) {
		return false, fmt.Errorf(
			"source-satisfied feature state task_id %q does not match product task_id %q",
			strings.TrimSpace(state.TaskID),
			strings.TrimSpace(brief.TaskID),
		)
	}
	if stateRoot := strings.TrimSpace(state.EffortRoot); stateRoot == "" || filepath.Clean(stateRoot) != filepath.Clean(brief.EffortRoot) {
		return false, fmt.Errorf(
			"source-satisfied feature state effort_root %q does not match product effort_root %q",
			stateRoot,
			strings.TrimSpace(brief.EffortRoot),
		)
	}
	if strings.TrimSpace(brief.CurrentPhase) != string(productworkflow.PhaseComplete) {
		completed, err := productworkflow.CompleteEffort(brief.EffortRoot)
		if err != nil {
			return false, fmt.Errorf("complete source-satisfied product artifacts: %w", err)
		}
		if completed.Status != productworkflow.ProductWorkflowStatusOK {
			return false, fmt.Errorf(
				"complete source-satisfied product artifacts: %s",
				strings.Join(completed.Blockers, "; "),
			)
		}
		refreshed, err := LoadBriefFromEffort(brief.EffortRoot)
		if err != nil {
			return false, fmt.Errorf("reload completed source-satisfied product effort: %w", err)
		}
		if strings.TrimSpace(refreshed.EffortID) != strings.TrimSpace(brief.EffortID) ||
			strings.TrimSpace(refreshed.TaskID) != strings.TrimSpace(brief.TaskID) {
			return false, fmt.Errorf(
				"source-satisfied product identity changed while completing artifacts: expected %s/%s, found %s/%s",
				strings.TrimSpace(brief.EffortID),
				strings.TrimSpace(brief.TaskID),
				strings.TrimSpace(refreshed.EffortID),
				strings.TrimSpace(refreshed.TaskID),
			)
		}
		brief = refreshed
	}
	if _, err := productworkflow.RecordSourceSatisfiedDelivery(productworkflow.SourceSatisfiedDeliveryRequest{
		EffortRoot:       brief.EffortRoot,
		ExpectedEffortID: brief.EffortID,
		ExpectedTaskID:   brief.TaskID,
		Completion:       completion,
	}); err != nil {
		return false, err
	}

	return true, nil
}

func sourceSatisfiedCompletionFromFinalFeatureState(
	state featureplanner.FeatureState,
) (plancontract.PlanExecuteTerminalCompletionRecord, bool, error) {
	if strings.TrimSpace(state.Status) != featureplanner.FeatureStateComplete {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, nil
	}

	candidate := false
	for _, atom := range state.Atoms {
		if atom.State == featureplanner.AtomStateSourceSatisfied {
			candidate = true
		}
	}
	for _, closure := range state.VerificationClosures {
		if closure.Status == featureplanner.VerificationClosureStatusSourceSatisfied ||
			strings.TrimSpace(closure.Outcome) == string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied) {
			candidate = true
		}
	}
	if !candidate {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, nil
	}
	if strings.TrimSpace(state.TaskID) == "" {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf("source-satisfied feature state is missing durable task_id")
	}

	atomsByID := make(map[string]featureplanner.FeatureAtomState, len(state.Atoms))
	allAtomsSourceSatisfied := true
	for _, atom := range state.Atoms {
		atomID := strings.TrimSpace(atom.ID)
		if atomID == "" {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf("source-satisfied feature state contains an atom without identity")
		}
		if _, exists := atomsByID[atomID]; exists {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf("source-satisfied feature state duplicates atom %q", atomID)
		}
		if atom.State != featureplanner.AtomStateSourceSatisfied {
			if atom.State != featureplanner.AtomStateComplete {
				return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
					"source-satisfied candidate contains nonterminal atom %q in state %q",
					atomID,
					atom.State,
				)
			}
			allAtomsSourceSatisfied = false
		}
		atomsByID[atomID] = atom
	}
	if len(atomsByID) == 0 {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf("source-satisfied product delivery requires finalized atoms")
	}

	closuresByAtom := make(map[string]featureplanner.VerificationClosure, len(state.VerificationClosures))
	closureEventIDs := make(map[string]struct{}, len(state.VerificationClosures))
	for _, closure := range state.VerificationClosures {
		atomID := strings.TrimSpace(closure.AtomID)
		if _, exists := atomsByID[atomID]; !exists {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied verification closure references stale or unknown atom %q",
				atomID,
			)
		}
		if _, exists := closuresByAtom[atomID]; exists {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied feature state has duplicate verification closures for atom %q",
				atomID,
			)
		}
		eventID := strings.TrimSpace(closure.EventID)
		if _, exists := closureEventIDs[eventID]; exists {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied feature state duplicates verification closure event_id %q",
				eventID,
			)
		}
		closureEventIDs[eventID] = struct{}{}
		closuresByAtom[atomID] = closure
	}
	if err := featureplanner.ValidateDurableState(state); err != nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
			"validate finalized source-satisfied feature state: %w",
			err,
		)
	}
	for atomID, atom := range atomsByID {
		closure, ok := closuresByAtom[atomID]
		sourceClosure := ok && closure.Status == featureplanner.VerificationClosureStatusSourceSatisfied &&
			strings.TrimSpace(closure.Outcome) == string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied)
		if atom.State == featureplanner.AtomStateSourceSatisfied && !sourceClosure {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q is missing matching source-satisfied verification authority",
				atomID,
			)
		}
		if atom.State != featureplanner.AtomStateSourceSatisfied && sourceClosure {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied verification authority for atom %q does not match atom state %q",
				atomID,
				atom.State,
			)
		}
	}
	if !allAtomsSourceSatisfied {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, nil
	}
	if state.ReleaseReadiness.Status != featureplanner.ReleaseReadinessStatusReady {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
			"source-satisfied feature state is not release-ready: %s",
			state.ReleaseReadiness.Status,
		)
	}
	readiness := featureplanner.EvaluateReleaseReadiness(state, state.ReleaseReadiness.DecidedAt)
	if readiness.Status != featureplanner.ReleaseReadinessStatusReady {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
			"source-satisfied feature state fails current release-readiness evaluation: %s",
			strings.Join(readiness.Blockers, "; "),
		)
	}

	var requiredPaths []string
	var evidencePaths []string
	var evidence []plancontract.SourceSatisfiedEvidenceRef
	for _, atom := range state.Atoms {
		atomID := strings.TrimSpace(atom.ID)
		closure, ok := closuresByAtom[atomID]
		if !ok {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q is missing its verification closure",
				atomID,
			)
		}
		if closure.Status != featureplanner.VerificationClosureStatusSourceSatisfied ||
			strings.TrimSpace(closure.Outcome) != string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied) {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q has non-source verification authority %q/%q",
				atomID,
				closure.Status,
				strings.TrimSpace(closure.Outcome),
			)
		}
		if len(plancontract.UniqueScopePaths(closure.ChangedPaths)) > 0 {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q verification closure contains changed paths",
				atomID,
			)
		}

		atomRequiredPaths := plancontract.UniqueScopePaths(atom.RequiredEditPaths)
		if len(atomRequiredPaths) == 0 || !slices.Equal(atomRequiredPaths, atom.RequiredEditPaths) {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q required edit paths are missing or not normalized",
				atomID,
			)
		}
		atomEvidencePaths := plancontract.UniqueScopePaths(closure.SourceSatisfiedEvidencePaths)
		if len(atomEvidencePaths) == 0 || !slices.Equal(atomEvidencePaths, closure.SourceSatisfiedEvidencePaths) {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q evidence paths are missing or not normalized",
				atomID,
			)
		}
		atomEvidence := plancontract.NormalizeSourceSatisfiedEvidenceRefs(closure.SourceSatisfiedEvidence)
		if len(atomEvidence) == 0 || !slices.Equal(atomEvidence, closure.SourceSatisfiedEvidence) {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q evidence refs are missing or not normalized",
				atomID,
			)
		}
		pathViolations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
			RequiredPaths:        atomRequiredPaths,
			SourceSatisfiedPaths: atomEvidencePaths,
			Evidence:             atomEvidence,
			RequireEvidence:      true,
		})
		if len(atomEvidencePaths) != len(atomRequiredPaths) || len(atomEvidence) != len(atomEvidencePaths) || len(pathViolations) > 0 {
			return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
				"source-satisfied atom %q evidence must exactly cover its required edit paths with current-state refs",
				atomID,
			)
		}
		requiredPaths = append(requiredPaths, atomRequiredPaths...)
		evidencePaths = append(evidencePaths, atomEvidencePaths...)
		evidence = append(evidence, atomEvidence...)
	}

	requiredPaths = plancontract.UniqueScopePaths(requiredPaths)
	evidencePaths = plancontract.UniqueScopePaths(evidencePaths)
	evidence = plancontract.NormalizeSourceSatisfiedEvidenceRefs(evidence)
	if len(requiredPaths) != len(evidencePaths) || len(evidencePaths) != len(evidence) {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, false, fmt.Errorf(
			"source-satisfied feature state does not preserve exact required path and evidence coverage",
		)
	}

	return plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SchemaVersion:                plancontract.PlanExecuteTerminalCompletionSchemaVersion,
		Type:                         plancontract.PlanExecuteTerminalCompletionRecordType,
		TS:                           strings.TrimSpace(state.ReleaseReadiness.DecidedAt),
		SessionID:                    strings.TrimSpace(state.SessionID),
		Phase:                        plancontract.PlanExecuteProgressPhaseComplete,
		Status:                       plancontract.PlanExecuteTerminalStatusSourceSatisfied,
		TerminalClass:                plancontract.PlanExecuteTerminalClassSuccess,
		Outcome:                      plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
		RequiredEditPaths:            requiredPaths,
		SourceSatisfiedEvidencePaths: evidencePaths,
		SourceSatisfiedEvidence:      evidence,
	}), true, nil
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(req.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(scenario.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(dc.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(invariant.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(mode.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(deviation.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(leaf.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(table.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(boundary.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(machine.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(incident.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(model.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(target.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(eval.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(securityCase.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(privacy.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(provider.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(signal.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(bound.Status) {
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
		if !productworkflow.BehavioralCoverageStatusIsAccepted(journey.Status) {
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
	for _, surface := range contract.SurfaceInventory {
		out.SurfaceInventory = append(out.SurfaceInventory, featureplanner.AcceptedCoverageSurfaceDecision{
			ID:           surface.ID,
			Surface:      surface.Surface,
			Status:       surface.Status,
			Rationale:    surface.Rationale,
			EvidenceRefs: append([]string(nil), surface.EvidenceRefs...),
			Obligations:  append([]string(nil), surface.Obligations...),
			QuestionRefs: append([]string(nil), surface.QuestionRefs...),
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
