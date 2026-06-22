package featureplanner

import (
	"fmt"
	"sort"
	"strings"
)

func FeaturePlanCoveragePresentation(state FeatureState, statePath string, plan FeaturePlan, candidate RejectedPlanCandidate) []CoveragePresentationSection {
	obligationsByID := planPresentationTDDObligationsByID(plan.TDDContract.Obligations)
	accepted := AcceptedCoveragePresentation(plan.AcceptedCoverage)

	sections := []CoveragePresentationSection{
		{Label: CoverageSectionRequirements, Rows: coverageRows(append(planPresentationRequirements(state, plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionRequirements)...))},
		{Label: CoverageSectionRulesAndExamples, Rows: coverageRows(append(planPresentationRulesAndExamples(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionRulesAndExamples)...))},
		{Label: CoverageSectionScenarios, Rows: coverageRows(append(planPresentationScenarios(plan, obligationsByID), AcceptedCoverageRowsBySection(accepted, CoverageSectionScenarios)...))},
		{Label: CoverageSectionE2EJourneys, Rows: coverageRows(append(planPresentationE2EJourneys(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionE2EJourneys)...))},
		{Label: CoverageSectionInvariants, Rows: coverageRows(append(planPresentationInvariants(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionInvariants)...))},
		{Label: CoverageSectionFuzzAndPropertyCoverage, Rows: coverageRows(append(planPresentationFuzzAndPropertyCoverage(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionFuzzAndPropertyCoverage)...))},
		{Label: CoverageSectionFailureModes, Rows: coverageRows(append(planPresentationFailureModes(state, plan, obligationsByID), AcceptedCoverageRowsBySection(accepted, CoverageSectionFailureModes)...))},
		{Label: CoverageSectionSecurityPrivacyRAGEvals, Rows: coverageRows(append(planPresentationSecurityPrivacyAndRAGEvals(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionSecurityPrivacyRAGEvals)...))},
		{Label: CoverageSectionProviderContracts, Rows: coverageRows(append(planPresentationProviderContracts(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionProviderContracts)...))},
		{Label: CoverageSectionStateModel, Rows: coverageRows(append(append(planPresentationStateModelDraftValues(plan), planPresentationStateModel(state, statePath, candidate)...), AcceptedCoverageRowsBySection(accepted, CoverageSectionStateModel)...))},
		{Label: CoverageSectionObservability, Rows: coverageRows(append(planPresentationObservability(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionObservability)...))},
		{Label: CoverageSectionPerformanceBounds, Rows: coverageRows(append(planPresentationPerformanceBounds(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionPerformanceBounds)...))},
		{Label: CoverageSectionTestObligations, Rows: coverageRows(append(planPresentationTestObligations(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionTestObligations)...))},
		{Label: CoverageSectionVerificationGates, Rows: coverageRows(append(planPresentationVerificationGates(plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionVerificationGates)...))},
		{Label: CoverageSectionWaiversAndOpenQuestions, Rows: coverageRows(append(planPresentationWaiversAndQuestions(state, plan), AcceptedCoverageRowsBySection(accepted, CoverageSectionWaiversAndOpenQuestions)...))},
	}

	for i := range sections {
		sections[i].Rows = uniqueCoveragePresentationRows(sections[i].Rows)
	}

	return sections
}

func FeaturePlanCoverageRowsBySection(sections []CoveragePresentationSection, label string) []string {
	return AcceptedCoverageRowsBySection(sections, label)
}

func planPresentationRequirements(state FeatureState, plan FeaturePlan) []string {
	var values []string
	if objective := firstCoverageText(plan.TopLevelScope.Objective, plan.SourceTask.Summary, state.PromptSummary); objective != "" {
		values = append(values, "Objective: "+objective)
	}
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, requirement := range draft.Requirements {
			label := firstCoverageText(requirement.ID, requirement.Statement, "requirement")
			if statement := strings.TrimSpace(requirement.Statement); statement != "" && statement != label {
				label += ": " + statement
			}
			if pattern := strings.TrimSpace(requirement.Pattern); pattern != "" {
				label += " (" + pattern + ")"
			}
			values = append(values, label)
		}
	}
	values = append(values, prefixedCoverageValues("Success criterion", plan.TopLevelScope.SuccessCriteria)...)
	values = append(values, prefixedCoverageValues("Non-goal", plan.TopLevelScope.NonGoals)...)
	values = append(values, prefixedCoverageValues("Proposed path", planPresentationPaths(plan))...)
	return values
}

func planPresentationRulesAndExamples(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, rule := range draft.ExampleRules {
			label := firstCoverageText(rule.ID, rule.Rule, "rule")
			if text := strings.TrimSpace(rule.Rule); text != "" && text != label {
				label += ": " + text
			}
			if req := strings.TrimSpace(rule.RequirementID); req != "" {
				label += " [requirement: " + req + "]"
			}
			values = append(values, label)
			for _, example := range rule.Examples {
				exampleLabel := firstCoverageText(example.ID, example.Description, "example")
				if desc := strings.TrimSpace(example.Description); desc != "" && desc != exampleLabel {
					exampleLabel += ": " + desc
				}
				if len(example.ConcreteData) > 0 {
					exampleLabel += " (concrete data)"
				}
				if example.Negative {
					exampleLabel += " (negative)"
				}
				values = append(values, "Example "+exampleLabel)
			}
			values = append(values, prefixedCoverageValues("Rule "+label+" question", rule.Questions)...)
		}
	}
	values = append(values, prefixedCoverageValues("Acceptance", append(append([]string(nil), plan.SDDContract.Acceptance...), plan.SDDContract.Refs...))...)
	values = append(values, prefixedCoverageValues("PRD requirement ref", plan.SDDContract.PRDRequirementRefs)...)
	values = append(values, prefixedCoverageValues("ADR ref", plan.SDDContract.ADRRefs)...)
	for _, atom := range plan.Atoms {
		title := firstCoverageText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		values = append(values, prefixedCoverageValues("Atom "+title+" acceptance ref", atom.SDDRefs)...)
		values = append(values, prefixedCoverageValues("Atom "+title+" edit-set obligation", atom.EditSetObligations)...)
	}
	return values
}

func planPresentationScenarios(plan FeaturePlan, obligationsByID map[string]TDDObligation) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, scenario := range draft.Scenarios {
			label := firstCoverageText(scenario.ID, "scenario")
			parts := []string{}
			if len(scenario.Given) > 0 {
				parts = append(parts, "Given "+strings.Join(normalizedStringList(scenario.Given), "; "))
			}
			if len(scenario.When) > 0 {
				parts = append(parts, "When "+strings.Join(normalizedStringList(scenario.When), "; "))
			}
			if len(scenario.Then) > 0 {
				parts = append(parts, "Then "+strings.Join(normalizedStringList(scenario.Then), "; "))
			}
			if len(parts) > 0 {
				label += ": " + strings.Join(parts, " / ")
			}
			if len(scenario.ConcreteData) > 0 {
				label += " (concrete data)"
			}
			values = append(values, label)
		}
	}
	for _, obligation := range plan.TDDContract.Obligations {
		id := strings.TrimSpace(obligation.ID)
		behavior := strings.TrimSpace(obligation.Behavior)
		if behavior == "" {
			continue
		}
		if id != "" {
			values = append(values, id+": "+behavior)
		} else {
			values = append(values, behavior)
		}
	}
	for _, atom := range plan.Atoms {
		title := firstCoverageText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		for _, behavior := range planPresentationAtomCoverageBehaviors(atom, obligationsByID) {
			values = append(values, "Atom "+title+": "+behavior)
		}
	}
	return values
}

func planPresentationInvariants(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		if authority := strings.TrimSpace(draft.Authority); authority != "" {
			values = append(values, "Coverage draft authority: "+authority)
		}
		if status := strings.TrimSpace(draft.Status); status != "" {
			values = append(values, "Coverage draft status: "+status)
		}
		if tier := strings.TrimSpace(draft.Tier); tier != "" {
			values = append(values, "Coverage draft tier: "+tier)
		}
		for _, invariant := range draft.Invariants {
			label := firstCoverageText(invariant.ID, invariant.Statement, "invariant")
			if statement := strings.TrimSpace(invariant.Statement); statement != "" && statement != label {
				label += ": " + statement
			}
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Invariant "+label+" obligation", invariant.Obligations)...)
		}
	}
	if tier := strings.TrimSpace(plan.TDDContract.CoverageTier); tier != "" {
		values = append(values, "Coverage tier: "+tier)
	}
	if plan.TDDContract.RequiredBeforeImplementation {
		values = append(values, "TDD contract required before implementation")
	}
	for _, obligation := range plan.TDDContract.Obligations {
		id := firstCoverageText(obligation.ID, obligation.Behavior, "planned test obligation")
		if requiredBefore := strings.TrimSpace(obligation.RequiredBefore); requiredBefore != "" {
			values = append(values, id+": required before "+requiredBefore)
		}
	}
	for _, atom := range plan.Atoms {
		title := firstCoverageText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		for _, invariant := range planPresentationAtomCoverageInvariants(atom) {
			values = append(values, "Atom "+title+": "+invariant)
		}
		values = append(values, prefixedCoverageValues("Atom "+title+" evidence path", atom.EvidencePaths)...)
		values = append(values, prefixedCoverageValues("Atom "+title+" forbidden path", atom.ForbiddenPaths)...)
		if atom.ImplementationRequired {
			values = append(values, "Atom "+title+": implementation required after characterization")
		}
	}
	return values
}

func planPresentationFailureModes(state FeatureState, plan FeaturePlan, obligationsByID map[string]TDDObligation) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, mode := range draft.FailureModes {
			label := firstCoverageText(mode.ID, mode.Mode, "failure mode")
			if text := strings.TrimSpace(mode.Mode); text != "" && text != label {
				label += ": " + text
			}
			if mitigation := strings.TrimSpace(mode.Mitigation); mitigation != "" {
				label += " -> " + mitigation
			}
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Failure mode "+label+" obligation", mode.Obligations)...)
		}
	}
	if count := len(normalizedStringList(state.Blockers)); count == 1 {
		values = append(values, "1 blocker recorded in state. Open the State path for diagnostic details.")
	} else if count > 1 {
		values = append(values, fmt.Sprintf("%d blockers recorded in state. Open the State path for diagnostic details.", count))
	}
	values = append(values, prefixedCoverageValues("Context gap", plan.Context.UnresolvedGaps)...)
	values = append(values, prefixedCoverageValues("Open state obligation", state.OpenObligations)...)
	for _, atom := range plan.Atoms {
		title := firstCoverageText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		for _, gap := range planPresentationAtomCoverageGaps(atom, obligationsByID) {
			values = append(values, "Atom "+title+": "+gap)
		}
	}
	return values
}

func planPresentationStateModel(state FeatureState, statePath string, candidate RejectedPlanCandidate) []string {
	values := []string{
		"Status: " + strings.TrimSpace(state.Status),
		"Session: " + strings.TrimSpace(state.SessionID),
		"State path: " + strings.TrimSpace(statePath),
	}
	if hash := strings.TrimSpace(candidate.PlanHash); hash != "" {
		values = append(values, "Candidate hash: "+hash)
	}
	if state.Revision > 0 {
		values = append(values, fmt.Sprintf("State revision: %d", state.Revision))
	}
	if state.PlannerRevision > 0 {
		values = append(values, fmt.Sprintf("Planner revision: %d", state.PlannerRevision))
	}
	if next := strings.TrimSpace(state.Continuation.NextAtomID); next != "" {
		values = append(values, "Next atom: "+next)
	}
	values = append(values, prefixedCoverageValues("Completed atom", state.Continuation.CompletedAtoms)...)
	values = append(values, prefixedCoverageValues("Blocked atom", state.Continuation.BlockedAtoms)...)
	return values
}

func planPresentationStateModelDraftValues(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, machine := range draft.StateMachines {
			label := firstCoverageText(machine.ID, "state machine")
			if len(machine.States) > 0 {
				label += " states: " + strings.Join(normalizedStringList(machine.States), ", ")
			}
			values = append(values, label)
			for _, transition := range machine.Transitions {
				transitionLabel := strings.TrimSpace(transition.From) + " --" + strings.TrimSpace(transition.Event) + "--> " + strings.TrimSpace(transition.To)
				values = append(values, "Transition: "+strings.TrimSpace(transitionLabel))
			}
			values = append(values, prefixedCoverageValues("State machine "+label+" obligation", machine.Obligations)...)
		}
	}
	return values
}

func planPresentationE2EJourneys(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, journey := range draft.E2EJourneys {
			label := firstCoverageText(journey.ID, journey.Journey, "e2e journey")
			if text := strings.TrimSpace(journey.Journey); text != "" && text != label {
				label += ": " + text
			}
			label = coverageLabelWithStatus(label, journey.Status)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("E2E "+label+" step", journey.Steps)...)
			values = append(values, prefixedCoverageValues("E2E "+label+" script", journey.ScriptRefs)...)
			values = append(values, prefixedCoverageValues("E2E "+label+" obligation", journey.Obligations)...)
		}
	}
	return values
}

func planPresentationFuzzAndPropertyCoverage(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, target := range draft.FuzzTargets {
			label := firstCoverageText(target.ID, target.Target, "fuzz target")
			if text := strings.TrimSpace(target.Target); text != "" && text != label {
				label += ": " + text
			}
			label = coverageLabelWithStatus(label, target.Status)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Fuzz "+label+" generator", target.Generators)...)
			values = append(values, prefixedCoverageValues("Fuzz "+label+" invariant", target.Invariants)...)
			values = append(values, prefixedCoverageValues("Fuzz "+label+" test", target.TestRefs)...)
			values = append(values, prefixedCoverageValues("Fuzz "+label+" obligation", target.Obligations)...)
		}
	}
	return values
}

func planPresentationSecurityPrivacyAndRAGEvals(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, securityCase := range draft.SecurityCases {
			label := securityCaseLabel(securityCase.ID, securityCase.Status, securityCase.Risk, securityCase.Attack, securityCase.Expected)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Security "+label+" test", securityCase.TestRefs)...)
			values = append(values, prefixedCoverageValues("Security "+label+" obligation", securityCase.Obligations)...)
		}
		for _, privacy := range draft.PrivacyControls {
			label := privacyControlLabel(privacy.ID, privacy.Status, privacy.DataClass, privacy.Policy, privacy.Retention)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Privacy "+label+" redaction", privacy.RedactionRefs)...)
			values = append(values, prefixedCoverageValues("Privacy "+label+" test", privacy.TestRefs)...)
			values = append(values, prefixedCoverageValues("Privacy "+label+" obligation", privacy.Obligations)...)
		}
		for _, eval := range draft.RAGEvals {
			label := coverageLabelWithStatus(firstCoverageText(eval.ID, "rag eval"), eval.Status)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("RAG "+label+" dataset", eval.DatasetRefs)...)
			values = append(values, floatMapCoverageValues("RAG "+label+" metric", eval.Metrics)...)
			values = append(values, floatMapCoverageValues("RAG "+label+" threshold", eval.Thresholds)...)
			values = append(values, prefixedCoverageValues("RAG "+label+" eval", eval.EvalRefs)...)
			values = append(values, prefixedCoverageValues("RAG "+label+" obligation", eval.Obligations)...)
		}
	}
	return values
}

func planPresentationProviderContracts(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, contract := range draft.ProviderContracts {
			label := providerContractLabel(contract.ID, contract.Status, contract.Provider, contract.Consumer)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Provider contract "+label+" contract", contract.ContractRefs)...)
			values = append(values, prefixedCoverageValues("Provider contract "+label+" test", contract.TestRefs)...)
			values = append(values, prefixedCoverageValues("Provider contract "+label+" obligation", contract.Obligations)...)
		}
	}
	return values
}

func planPresentationObservability(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, signal := range draft.Observability {
			label := observabilityLabel(signal.ID, signal.Status, signal.Signal, signal.InstrumentedPath)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Observability "+label+" required field", signal.RequiredFields)...)
			values = append(values, prefixedCoverageValues("Observability "+label+" test", signal.TestRefs)...)
			values = append(values, prefixedCoverageValues("Observability "+label+" obligation", signal.Obligations)...)
		}
	}
	return values
}

func planPresentationPerformanceBounds(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, bound := range draft.PerformanceBounds {
			label := performanceBoundLabel(bound.ID, bound.Status, bound.Operation, bound.Bound)
			values = append(values, label)
			values = append(values, prefixedCoverageValues("Performance "+label+" measurement", bound.MeasurementRefs)...)
			values = append(values, prefixedCoverageValues("Performance "+label+" obligation", bound.Obligations)...)
		}
	}
	return values
}

func planPresentationTestObligations(plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, obligation := range draft.TestObligations {
			label := firstCoverageText(obligation.ID, obligation.Behavior, obligation.Command, "test obligation")
			if behavior := strings.TrimSpace(obligation.Behavior); behavior != "" && behavior != label {
				label += " - " + behavior
			}
			if command := strings.TrimSpace(obligation.Command); command != "" {
				label += " (" + command + ")"
			}
			if typ := strings.TrimSpace(obligation.Type); typ != "" {
				label += " [" + typ + "]"
			}
			values = append(values, label)
		}
	}
	for _, obligation := range plan.TDDContract.Obligations {
		label := firstCoverageText(obligation.ID, obligation.Behavior, obligation.Command, "planned test obligation")
		if behavior := strings.TrimSpace(obligation.Behavior); behavior != "" && behavior != label {
			label += " - " + behavior
		}
		if command := strings.TrimSpace(obligation.Command); command != "" {
			label += " (" + command + ")"
		}
		values = append(values, label)
	}
	for _, atom := range plan.Atoms {
		title := firstCoverageText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		values = append(values, prefixedCoverageValues("Atom "+title+" TDD ref", append(append([]string(nil), atom.TDDObligations...), atom.TestFirst.ObligationRefs...))...)
		values = append(values, prefixedCoverageValues("Atom "+title+" test artifact", normalizedStringList(append(append([]string(nil), atom.TestFirst.TestArtifactPaths...), atom.PlannedTDDSnapshot.TestArtifactPaths...)))...)
		values = append(values, prefixedCoverageValues("Atom "+title+" required test", normalizedStringList(append(append(append([]string(nil), atom.TestFirst.Commands...), atom.RequiredTests...), atom.PlannedTDDSnapshot.TestCommands...)))...)
	}
	return values
}

func planPresentationVerificationGates(plan FeaturePlan) []string {
	var values []string
	if ref := plan.CoverageContract; ref != nil {
		if status := strings.TrimSpace(ref.Status); status != "" {
			values = append(values, "Coverage contract status: "+status)
		}
		if eventID := strings.TrimSpace(ref.EventID); eventID != "" {
			values = append(values, "Coverage contract event: "+eventID)
		}
		if digest := strings.TrimSpace(ref.Digest); digest != "" {
			values = append(values, "Coverage contract digest: "+digest)
		}
	}
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, gate := range draft.Verification {
			label := firstCoverageText(gate.ID, gate.Gate, gate.Command, "verification gate")
			if gateName := strings.TrimSpace(gate.Gate); gateName != "" && gateName != label {
				label += ": " + gateName
			}
			if command := strings.TrimSpace(gate.Command); command != "" {
				label += " (" + command + ")"
			}
			values = append(values, label)
		}
	}
	if eventID := strings.TrimSpace(plan.TDDContract.CoverageGateEventID); eventID != "" {
		values = append(values, "TDD contract coverage gate event: "+eventID)
	}
	if result := plan.CoverageGateResult; result != nil {
		values = append(values, "Coverage gate status: "+strings.TrimSpace(result.Status))
		if tier := strings.TrimSpace(result.CoverageTier); tier != "" {
			values = append(values, "Coverage gate tier: "+tier)
		}
		if checkedAt := strings.TrimSpace(result.CheckedAt); checkedAt != "" {
			values = append(values, "Coverage gate checked at: "+checkedAt)
		}
	}
	for _, atom := range plan.Atoms {
		title := firstCoverageText(atom.Title, atom.ID, atom.AtomID, "untitled atom")
		if atom.TestFirst.BaselineRequired {
			status := strings.TrimSpace(atom.TestFirst.BaselineStatus)
			if status == "" {
				status = "required"
			}
			values = append(values, "Atom "+title+" baseline: "+status)
		}
		values = append(values, prefixedCoverageValues("Atom "+title+" pre-execution event", atom.TestFirst.PreExecutionEventIDs)...)
		if gate := strings.TrimSpace(atom.TestFirst.CoverageGateEventID); gate != "" {
			values = append(values, "Atom "+title+" coverage gate event: "+gate)
		}
		if snapshot := strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotID); snapshot != "" {
			values = append(values, "Atom "+title+" TDD snapshot: "+snapshot)
		}
		if digest := strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotDigest); digest != "" {
			values = append(values, "Atom "+title+" TDD snapshot digest: "+digest)
		}
		if eventID := strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID); eventID != "" {
			values = append(values, "Atom "+title+" snapshot before dispatch event: "+eventID)
		}
	}
	return values
}

func planPresentationWaiversAndQuestions(state FeatureState, plan FeaturePlan) []string {
	var values []string
	if draft := plan.BehavioralCoverageDraft; draft != nil {
		for _, waiver := range draft.Waivers {
			label := firstCoverageText(waiver.ID, waiver.Technique, "waiver")
			if technique := strings.TrimSpace(waiver.Technique); technique != "" && technique != label {
				label += ": " + technique
			}
			if reason := strings.TrimSpace(waiver.Reason); reason != "" {
				label += " - " + reason
			}
			values = append(values, label)
		}
		for _, question := range draft.OpenQuestions {
			label := firstCoverageText(question.ID, question.Question, "coverage question")
			if text := strings.TrimSpace(question.Question); text != "" && text != label {
				label += ": " + text
			}
			values = append(values, "Coverage question: "+label)
		}
	}
	for _, question := range state.OpenQuestions {
		if !planPresentationQuestionOpen(question) {
			continue
		}
		summary := strings.TrimSpace(question.QuestionSummary)
		decision := strings.TrimSpace(question.RequestedDecision)
		switch {
		case summary != "" && decision != "":
			values = append(values, "Open question: "+summary+" - "+decision)
		case summary != "":
			values = append(values, "Open question: "+summary)
		case decision != "":
			values = append(values, "Open question: "+decision)
		}
	}
	values = append(values, prefixedCoverageValues("Recovery packet", state.OpenRecoveryPackets)...)
	return values
}

func planPresentationTDDObligationsByID(obligations []TDDObligation) map[string]TDDObligation {
	out := make(map[string]TDDObligation, len(obligations))
	for _, obligation := range obligations {
		id := strings.TrimSpace(obligation.ID)
		if id != "" {
			out[id] = obligation
		}
	}
	return out
}

func planPresentationAtomCoverageBehaviors(atom FeatureAtomPlan, obligationsByID map[string]TDDObligation) []string {
	var values []string
	for _, ref := range append(append([]string(nil), atom.TDDObligations...), atom.TestFirst.ObligationRefs...) {
		obligation, ok := obligationsByID[strings.TrimSpace(ref)]
		if ok && strings.TrimSpace(obligation.Behavior) != "" {
			values = append(values, obligation.Behavior)
		}
	}
	return normalizedStringList(values)
}

func planPresentationAtomCoverageGaps(atom FeatureAtomPlan, obligationsByID map[string]TDDObligation) []string {
	refs := normalizedStringList(append(append([]string(nil), atom.TDDObligations...), atom.TestFirst.ObligationRefs...))
	if len(refs) == 0 {
		return []string{"No linked TDD behavior"}
	}
	var gaps []string
	for _, ref := range refs {
		obligation, ok := obligationsByID[ref]
		if !ok {
			gaps = append(gaps, "Unmapped TDD ref: "+ref)
			continue
		}
		if strings.TrimSpace(obligation.Behavior) == "" {
			gaps = append(gaps, "TDD ref has no behavior: "+ref)
		}
	}
	return normalizedStringList(gaps)
}

func planPresentationAtomCoverageInvariants(atom FeatureAtomPlan) []string {
	var values []string
	values = append(values, prefixedCoverageValues("TDD refs", append(append([]string(nil), atom.TDDObligations...), atom.TestFirst.ObligationRefs...))...)
	values = append(values, prefixedCoverageValues("Acceptance refs", atom.SDDRefs)...)
	values = append(values, prefixedCoverageValues("Required edit paths", atom.RequiredEditPaths)...)
	values = append(values, prefixedCoverageValues("Allowed edit paths", atom.AllowedEditPaths)...)
	values = append(values, prefixedCoverageValues("Snapshot obligations", atom.PlannedTDDSnapshot.ObligationIDs)...)
	if atom.TestFirst.BaselineRequired {
		status := strings.TrimSpace(atom.TestFirst.BaselineStatus)
		if status == "" {
			status = "required"
		}
		values = append(values, "Baseline: "+status)
	}
	values = append(values, prefixedCoverageValues("Expected pre-execution result", append([]string(nil), atom.TestFirst.ExpectedPreExecutionResult))...)
	values = append(values, prefixedCoverageValues("Snapshot expected pre-execution results", atom.PlannedTDDSnapshot.ExpectedPreExecutionResults)...)
	return normalizedStringList(values)
}

func planPresentationPaths(plan FeaturePlan) []string {
	var paths []string
	for _, atom := range plan.Atoms {
		paths = append(paths, atom.RequiredEditPaths...)
		paths = append(paths, atom.AllowedEditPaths...)
		paths = append(paths, atom.TestFirst.TestArtifactPaths...)
	}
	return normalizedStringList(paths)
}

func planPresentationQuestionOpen(question PlannerQuestion) bool {
	switch strings.TrimSpace(question.Status) {
	case "", "open", "pending", "asked":
		return true
	default:
		return false
	}
}

func uniqueCoveragePresentationRows(rows []CoveragePresentationRow) []CoveragePresentationRow {
	seen := make(map[string]bool, len(rows))
	out := make([]CoveragePresentationRow, 0, len(rows))
	for _, row := range rows {
		text := strings.TrimSpace(row.Text)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		out = append(out, CoveragePresentationRow{Text: text})
	}
	return out
}

func sortedPlanPresentationKeys(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
