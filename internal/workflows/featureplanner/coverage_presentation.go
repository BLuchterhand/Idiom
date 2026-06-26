package featureplanner

import (
	"fmt"
	"sort"
	"strings"
)

const (
	CoverageSectionRequirements            = "Requirements"
	CoverageSectionSurfaceInventory        = "Behavior Surface Inventory"
	CoverageSectionRulesAndExamples        = "Rules & Examples"
	CoverageSectionScenarios               = "Scenarios"
	CoverageSectionE2EJourneys             = "E2E Journeys"
	CoverageSectionInvariants              = "Invariants"
	CoverageSectionFuzzAndPropertyCoverage = "Fuzz & Property Coverage"
	CoverageSectionFailureModes            = "Failure Modes"
	CoverageSectionSecurityPrivacyRAGEvals = "Security, Privacy & RAG Evals"
	CoverageSectionProviderContracts       = "Provider Contracts"
	CoverageSectionStateModel              = "State Model"
	CoverageSectionObservability           = "Observability"
	CoverageSectionPerformanceBounds       = "Performance Bounds"
	CoverageSectionTestObligations         = "Test Obligations"
	CoverageSectionVerificationGates       = "Verification Gates"
	CoverageSectionWaiversAndOpenQuestions = "Waivers/Open Questions"
)

type CoveragePresentationSection struct {
	Label string
	Rows  []CoveragePresentationRow
}

type CoveragePresentationRow struct {
	Text string
}

type coveragePresentationSectionSpec struct {
	Label string
	Rows  []string
}

type coverageRequirementPresentation struct {
	ID        string
	Statement string
	Pattern   string
}

type coverageExampleRulePresentation struct {
	ID            string
	RequirementID string
	Rule          string
	Examples      []coverageExamplePresentation
	Questions     []string
}

type coverageExamplePresentation struct {
	ID                  string
	Description         string
	ConcreteDataPresent bool
	Negative            bool
}

type coverageScenarioPresentation struct {
	ID                  string
	Given               []string
	When                []string
	Then                []string
	ConcreteDataPresent bool
	Obligations         []string
}

type coverageSurfacePresentation struct {
	ID           string
	Surface      string
	Status       string
	Rationale    string
	EvidenceRefs []string
	Obligations  []string
	QuestionRefs []string
}

func AcceptedCoveragePresentation(coverage *AcceptedBehavioralCoverage) []CoveragePresentationSection {
	if coverage == nil {
		return nil
	}

	return buildCoveragePresentationSections([]coveragePresentationSectionSpec{
		{Label: CoverageSectionRequirements, Rows: acceptedCoverageRequirementRows(*coverage)},
		{Label: CoverageSectionSurfaceInventory, Rows: acceptedCoverageSurfaceRows(*coverage)},
		{Label: CoverageSectionRulesAndExamples, Rows: acceptedCoverageRulesAndExamplesRows(*coverage)},
		{Label: CoverageSectionScenarios, Rows: acceptedCoverageScenarioRows(*coverage)},
		{Label: CoverageSectionE2EJourneys, Rows: acceptedCoverageE2EJourneyRows(*coverage)},
		{Label: CoverageSectionInvariants, Rows: acceptedCoverageInvariantRows(*coverage)},
		{Label: CoverageSectionFuzzAndPropertyCoverage, Rows: acceptedCoverageFuzzRows(*coverage)},
		{Label: CoverageSectionFailureModes, Rows: acceptedCoverageFailureRows(*coverage)},
		{Label: CoverageSectionSecurityPrivacyRAGEvals, Rows: acceptedCoverageSecurityPrivacyRAGEvalRows(*coverage)},
		{Label: CoverageSectionProviderContracts, Rows: acceptedCoverageProviderContractRows(*coverage)},
		{Label: CoverageSectionStateModel, Rows: acceptedCoverageStateModelRows(*coverage)},
		{Label: CoverageSectionObservability, Rows: acceptedCoverageObservabilityRows(*coverage)},
		{Label: CoverageSectionPerformanceBounds, Rows: acceptedCoveragePerformanceRows(*coverage)},
		{Label: CoverageSectionTestObligations, Rows: acceptedCoverageTestObligationRows(*coverage)},
		{Label: CoverageSectionVerificationGates, Rows: acceptedCoverageVerificationRows(*coverage)},
		{Label: CoverageSectionWaiversAndOpenQuestions, Rows: acceptedCoverageWaiverQuestionRows(*coverage)},
	})
}

func buildCoveragePresentationSections(specs []coveragePresentationSectionSpec) []CoveragePresentationSection {
	return buildCoveragePresentationSectionsFromSpecs(specs, false)
}

func buildCoveragePresentationSectionsIncludingEmpty(specs []coveragePresentationSectionSpec) []CoveragePresentationSection {
	return buildCoveragePresentationSectionsFromSpecs(specs, true)
}

func buildCoveragePresentationSectionsFromSpecs(specs []coveragePresentationSectionSpec, includeEmpty bool) []CoveragePresentationSection {
	sections := make([]CoveragePresentationSection, 0, len(specs))
	for _, spec := range specs {
		rows := coverageRows(spec.Rows)
		if len(rows) == 0 && !includeEmpty {
			continue
		}
		sections = append(sections, CoveragePresentationSection{
			Label: spec.Label,
			Rows:  rows,
		})
	}

	return sections
}

func CoverageRowsBySection(sections []CoveragePresentationSection, label string) []string {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil
	}

	for _, section := range sections {
		if section.Label != label {
			continue
		}

		rows := make([]string, 0, len(section.Rows))
		for _, row := range section.Rows {
			if text := strings.TrimSpace(row.Text); text != "" {
				rows = append(rows, text)
			}
		}

		return rows
	}

	return nil
}

func coverageRows(values []string) []CoveragePresentationRow {
	values = normalizedStringList(values)
	rows := make([]CoveragePresentationRow, 0, len(values))
	for _, value := range values {
		rows = append(rows, CoveragePresentationRow{Text: value})
	}

	return rows
}

func acceptedCoverageRequirementRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	if authority := strings.TrimSpace(coverage.Authority); authority != "" {
		values = append(values, "Accepted coverage authority: "+authority)
	}
	if status := strings.TrimSpace(coverage.Status); status != "" {
		values = append(values, "Accepted coverage status: "+status)
	}
	values = append(values, coverageRequirementRows(acceptedCoverageRequirements(coverage.Requirements))...)

	return values
}

func acceptedCoverageRulesAndExamplesRows(coverage AcceptedBehavioralCoverage) []string {
	return coverageExampleRuleRows(acceptedCoverageExampleRules(coverage.ExampleRules))
}

func acceptedCoverageSurfaceRows(coverage AcceptedBehavioralCoverage) []string {
	surfaces := make([]coverageSurfacePresentation, 0, len(coverage.SurfaceInventory))
	for _, surface := range coverage.SurfaceInventory {
		surfaces = append(surfaces, coverageSurfacePresentation{
			ID:           surface.ID,
			Surface:      surface.Surface,
			Status:       surface.Status,
			Rationale:    surface.Rationale,
			EvidenceRefs: append([]string(nil), surface.EvidenceRefs...),
			Obligations:  append([]string(nil), surface.Obligations...),
			QuestionRefs: append([]string(nil), surface.QuestionRefs...),
		})
	}
	return coverageSurfaceRows(surfaces)
}

func acceptedCoverageScenarioRows(coverage AcceptedBehavioralCoverage) []string {
	return coverageScenarioRows(acceptedCoverageScenarios(coverage.Scenarios))
}

func coverageSurfaceRows(surfaces []coverageSurfacePresentation) []string {
	var values []string
	for _, surface := range surfaces {
		label := firstCoverageText(surface.ID, surface.Surface, "surface")
		if text := strings.TrimSpace(surface.Surface); text != "" && text != label {
			label += ": " + text
		}
		if status := strings.TrimSpace(surface.Status); status != "" {
			label += " [" + status + "]"
		}
		if rationale := strings.TrimSpace(surface.Rationale); rationale != "" {
			label += " - " + rationale
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Surface "+label+" obligation", surface.Obligations)...)
		values = append(values, prefixedCoverageValues("Surface "+label+" evidence", surface.EvidenceRefs)...)
		values = append(values, prefixedCoverageValues("Surface "+label+" question", surface.QuestionRefs)...)
	}
	return values
}

func coverageRequirementRows(requirements []coverageRequirementPresentation) []string {
	var values []string
	for _, requirement := range requirements {
		label := firstCoverageText(requirement.ID, requirement.Statement, "requirement")
		if statement := strings.TrimSpace(requirement.Statement); statement != "" && statement != label {
			label += ": " + statement
		}
		if pattern := strings.TrimSpace(requirement.Pattern); pattern != "" {
			label += " (" + pattern + ")"
		}
		values = append(values, label)
	}

	return values
}

func coverageExampleRuleRows(rules []coverageExampleRulePresentation) []string {
	var values []string
	for _, rule := range rules {
		label := firstCoverageText(rule.ID, rule.Rule, "rule")
		if text := strings.TrimSpace(rule.Rule); text != "" && text != label {
			label += ": " + text
		}
		if requirement := strings.TrimSpace(rule.RequirementID); requirement != "" {
			label += " [requirement: " + requirement + "]"
		}
		values = append(values, label)
		for _, example := range rule.Examples {
			exampleLabel := firstCoverageText(example.ID, example.Description, "example")
			if description := strings.TrimSpace(example.Description); description != "" && description != exampleLabel {
				exampleLabel += ": " + description
			}
			if example.ConcreteDataPresent {
				exampleLabel += " (concrete data)"
			}
			if example.Negative {
				exampleLabel += " (negative)"
			}
			values = append(values, "Example "+exampleLabel)
		}
		values = append(values, prefixedCoverageValues("Rule "+label+" question", rule.Questions)...)
	}

	return values
}

func coverageScenarioRows(scenarios []coverageScenarioPresentation) []string {
	var values []string
	for _, scenario := range scenarios {
		label := firstCoverageText(scenario.ID, "scenario")
		var parts []string
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
		if scenario.ConcreteDataPresent {
			label += " (concrete data)"
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Scenario "+label+" obligation", scenario.Obligations)...)
	}

	return values
}

func acceptedCoverageRequirements(requirements []AcceptedCoverageRequirement) []coverageRequirementPresentation {
	out := make([]coverageRequirementPresentation, 0, len(requirements))
	for _, requirement := range requirements {
		out = append(out, coverageRequirementPresentation{
			ID:        requirement.ID,
			Statement: requirement.Statement,
			Pattern:   requirement.Pattern,
		})
	}

	return out
}

func acceptedCoverageExampleRules(rules []AcceptedCoverageExampleRule) []coverageExampleRulePresentation {
	out := make([]coverageExampleRulePresentation, 0, len(rules))
	for _, rule := range rules {
		examples := make([]coverageExamplePresentation, 0, len(rule.Examples))
		for _, example := range rule.Examples {
			examples = append(examples, coverageExamplePresentation{
				ID:                  example.ID,
				Description:         example.Description,
				ConcreteDataPresent: len(example.ConcreteDataRefs) > 0,
				Negative:            example.Negative,
			})
		}
		out = append(out, coverageExampleRulePresentation{
			ID:            rule.ID,
			RequirementID: rule.RequirementID,
			Rule:          rule.Rule,
			Examples:      examples,
			Questions:     rule.Questions,
		})
	}

	return out
}

func acceptedCoverageScenarios(scenarios []AcceptedCoverageScenario) []coverageScenarioPresentation {
	out := make([]coverageScenarioPresentation, 0, len(scenarios))
	for _, scenario := range scenarios {
		out = append(out, coverageScenarioPresentation{
			ID:                  scenario.ID,
			Given:               scenario.Given,
			When:                scenario.When,
			Then:                scenario.Then,
			ConcreteDataPresent: len(scenario.ConcreteDataRefs) > 0,
			Obligations:         scenario.Obligations,
		})
	}

	return out
}

func draftCoverageRequirements(requirements []CoverageDraftRequirement) []coverageRequirementPresentation {
	out := make([]coverageRequirementPresentation, 0, len(requirements))
	for _, requirement := range requirements {
		out = append(out, coverageRequirementPresentation{
			ID:        requirement.ID,
			Statement: requirement.Statement,
			Pattern:   requirement.Pattern,
		})
	}

	return out
}

func draftCoverageExampleRules(rules []CoverageDraftExampleRule) []coverageExampleRulePresentation {
	out := make([]coverageExampleRulePresentation, 0, len(rules))
	for _, rule := range rules {
		examples := make([]coverageExamplePresentation, 0, len(rule.Examples))
		for _, example := range rule.Examples {
			examples = append(examples, coverageExamplePresentation{
				ID:                  example.ID,
				Description:         example.Description,
				ConcreteDataPresent: len(example.ConcreteData) > 0,
				Negative:            example.Negative,
			})
		}
		out = append(out, coverageExampleRulePresentation{
			ID:            rule.ID,
			RequirementID: rule.RequirementID,
			Rule:          rule.Rule,
			Examples:      examples,
			Questions:     rule.Questions,
		})
	}

	return out
}

func draftCoverageScenarios(scenarios []CoverageDraftScenario) []coverageScenarioPresentation {
	out := make([]coverageScenarioPresentation, 0, len(scenarios))
	for _, scenario := range scenarios {
		out = append(out, coverageScenarioPresentation{
			ID:                  scenario.ID,
			Given:               scenario.Given,
			When:                scenario.When,
			Then:                scenario.Then,
			ConcreteDataPresent: len(scenario.ConcreteData) > 0,
			Obligations:         scenario.Obligations,
		})
	}

	return out
}

func acceptedCoverageE2EJourneyRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, journey := range coverage.E2EJourneys {
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

	return values
}

func acceptedCoverageInvariantRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	if tier := strings.TrimSpace(coverage.Tier); tier != "" {
		values = append(values, "Accepted coverage tier: "+tier)
	}
	for _, contract := range coverage.DesignContracts {
		label := firstCoverageText(contract.ID, contract.Boundary, "design contract")
		if boundary := strings.TrimSpace(contract.Boundary); boundary != "" && boundary != label {
			label += ": " + boundary
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Design contract "+label+" precondition", contract.Preconditions)...)
		values = append(values, prefixedCoverageValues("Design contract "+label+" postcondition", contract.Postconditions)...)
		values = append(values, prefixedCoverageValues("Design contract "+label+" invariant", contract.Invariants)...)
	}
	for _, invariant := range coverage.Invariants {
		label := firstCoverageText(invariant.ID, invariant.Statement, "invariant")
		if statement := strings.TrimSpace(invariant.Statement); statement != "" && statement != label {
			label += ": " + statement
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Invariant "+label+" PBT", invariant.PropertyBasedTestRefs)...)
		values = append(values, prefixedCoverageValues("Invariant "+label+" obligation", invariant.Obligations)...)
	}

	return values
}

func acceptedCoverageFuzzRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, target := range coverage.FuzzTargets {
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

	return values
}

func acceptedCoverageFailureRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, mode := range coverage.FailureModes {
		label := firstCoverageText(mode.ID, mode.Mode, "failure mode")
		if text := strings.TrimSpace(mode.Mode); text != "" && text != label {
			label += ": " + text
		}
		if mitigation := strings.TrimSpace(mode.Mitigation); mitigation != "" {
			label += " -> " + mitigation
		}
		if mode.RPN > 0 {
			label += fmt.Sprintf(" (RPN %d)", mode.RPN)
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Failure mode "+label+" mitigation test", mode.MitigationTestRefs)...)
	}
	for _, deviation := range coverage.HAZOPDeviations {
		label := firstCoverageText(deviation.ID, deviation.Deviation, "HAZOP deviation")
		if guide := strings.TrimSpace(deviation.GuideWord); guide != "" {
			label += " [" + guide + "]"
		}
		if text := strings.TrimSpace(deviation.Deviation); text != "" && text != label {
			label += ": " + text
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("HAZOP "+label+" test", deviation.TestRefs)...)
	}
	for _, leaf := range coverage.FaultTreeLeaves {
		label := firstCoverageText(leaf.ID, leaf.Cause, "fault-tree leaf")
		if cause := strings.TrimSpace(leaf.Cause); cause != "" && cause != label {
			label += ": " + cause
		}
		if top := strings.TrimSpace(leaf.TopEvent); top != "" {
			label += " [top event: " + top + "]"
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Fault tree "+label+" test", leaf.TestRefs)...)
	}

	return values
}

func acceptedCoverageSecurityPrivacyRAGEvalRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, securityCase := range coverage.SecurityCases {
		label := securityCaseLabel(securityCase.ID, securityCase.Status, securityCase.Risk, securityCase.Attack, securityCase.Expected)
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Security "+label+" test", securityCase.TestRefs)...)
		values = append(values, prefixedCoverageValues("Security "+label+" obligation", securityCase.Obligations)...)
	}
	for _, privacy := range coverage.PrivacyControls {
		label := privacyControlLabel(privacy.ID, privacy.Status, privacy.DataClass, privacy.Policy, privacy.Retention)
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Privacy "+label+" redaction", privacy.RedactionRefs)...)
		values = append(values, prefixedCoverageValues("Privacy "+label+" test", privacy.TestRefs)...)
		values = append(values, prefixedCoverageValues("Privacy "+label+" obligation", privacy.Obligations)...)
	}
	for _, eval := range coverage.RAGEvals {
		label := coverageLabelWithStatus(firstCoverageText(eval.ID, "rag eval"), eval.Status)
		values = append(values, label)
		values = append(values, prefixedCoverageValues("RAG "+label+" dataset", eval.DatasetRefs)...)
		values = append(values, floatMapCoverageValues("RAG "+label+" metric", eval.Metrics)...)
		values = append(values, floatMapCoverageValues("RAG "+label+" threshold", eval.Thresholds)...)
		values = append(values, prefixedCoverageValues("RAG "+label+" eval", eval.EvalRefs)...)
		values = append(values, prefixedCoverageValues("RAG "+label+" obligation", eval.Obligations)...)
	}

	return values
}

func acceptedCoverageProviderContractRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, contract := range coverage.ProviderContracts {
		label := providerContractLabel(contract.ID, contract.Status, contract.Provider, contract.Consumer)
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Provider contract "+label+" contract", contract.ContractRefs)...)
		values = append(values, prefixedCoverageValues("Provider contract "+label+" test", contract.TestRefs)...)
		values = append(values, prefixedCoverageValues("Provider contract "+label+" obligation", contract.Obligations)...)
	}

	return values
}

func acceptedCoverageStateModelRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, machine := range coverage.StateMachines {
		label := firstCoverageText(machine.ID, "state machine")
		if len(machine.States) > 0 {
			label += " states: " + strings.Join(normalizedStringList(machine.States), ", ")
		}
		values = append(values, label)
		for _, transition := range machine.Transitions {
			transitionLabel := strings.TrimSpace(transition.From) + " --" + strings.TrimSpace(transition.Event) + "--> " + strings.TrimSpace(transition.To)
			values = append(values, "Transition: "+strings.TrimSpace(transitionLabel))
			values = append(values, prefixedCoverageValues("Transition "+transitionLabel+" test", transition.TestRefs)...)
		}
		values = append(values, prefixedCoverageValues("State machine "+label+" obligation", machine.Obligations)...)
	}
	for _, table := range coverage.DecisionTables {
		label := firstCoverageText(table.ID, "decision table")
		if len(table.Conditions) > 0 {
			label += " conditions: " + strings.Join(normalizedStringList(table.Conditions), ", ")
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Decision table "+label+" test", table.TestRefs)...)
	}
	for _, boundary := range coverage.BoundaryValues {
		label := firstCoverageText(boundary.ID, boundary.Parameter, "boundary values")
		if parameter := strings.TrimSpace(boundary.Parameter); parameter != "" && parameter != label {
			label += ": " + parameter
		}
		if len(boundary.Values) > 0 {
			label += " values: " + strings.Join(normalizedStringList(boundary.Values), ", ")
		}
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Boundary "+label+" test", boundary.TestRefs)...)
	}

	return values
}

func acceptedCoverageObservabilityRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, signal := range coverage.Observability {
		label := observabilityLabel(signal.ID, signal.Status, signal.Signal, signal.InstrumentedPath)
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Observability "+label+" required field", signal.RequiredFields)...)
		values = append(values, prefixedCoverageValues("Observability "+label+" test", signal.TestRefs)...)
		values = append(values, prefixedCoverageValues("Observability "+label+" obligation", signal.Obligations)...)
	}

	return values
}

func acceptedCoveragePerformanceRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, bound := range coverage.PerformanceBounds {
		label := performanceBoundLabel(bound.ID, bound.Status, bound.Operation, bound.Bound)
		values = append(values, label)
		values = append(values, prefixedCoverageValues("Performance "+label+" measurement", bound.MeasurementRefs)...)
		values = append(values, prefixedCoverageValues("Performance "+label+" obligation", bound.Obligations)...)
	}

	return values
}

func acceptedCoverageTestObligationRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, obligation := range coverage.TestObligations {
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

	return values
}

func acceptedCoverageVerificationRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, gate := range coverage.Verification {
		label := firstCoverageText(gate.ID, gate.Gate, gate.Command, "verification gate")
		if gateName := strings.TrimSpace(gate.Gate); gateName != "" && gateName != label {
			label += ": " + gateName
		}
		if command := strings.TrimSpace(gate.Command); command != "" {
			label += " (" + command + ")"
		}
		values = append(values, label)
	}
	if requirement := strings.TrimSpace(coverage.Mutation.Requirement); requirement != "" {
		label := "Mutation: " + requirement
		if coverage.Mutation.MinScore > 0 {
			label += fmt.Sprintf(" min=%d", coverage.Mutation.MinScore)
		}
		if coverage.Mutation.Score != nil {
			label += fmt.Sprintf(" score=%.1f", *coverage.Mutation.Score)
		}
		if reason := strings.TrimSpace(coverage.Mutation.WaiverReason); reason != "" {
			label += " waiver=" + reason
		}
		values = append(values, label)
	}

	return values
}

func acceptedCoverageWaiverQuestionRows(coverage AcceptedBehavioralCoverage) []string {
	var values []string
	for _, waiver := range coverage.Waivers {
		label := firstCoverageText(waiver.ID, waiver.Technique, "waiver")
		if technique := strings.TrimSpace(waiver.Technique); technique != "" && technique != label {
			label += ": " + technique
		}
		if reason := strings.TrimSpace(waiver.Reason); reason != "" {
			label += " - " + reason
		}
		values = append(values, label)
	}
	for _, question := range coverage.Questions {
		label := firstCoverageText(question.ID, question.Question, "coverage question")
		if text := strings.TrimSpace(question.Question); text != "" && text != label {
			label += ": " + text
		}
		if status := strings.TrimSpace(question.Status); status != "" {
			label += " [" + status + "]"
		}
		values = append(values, "Coverage question: "+label)
	}
	for _, incident := range coverage.PostMortems {
		label := firstCoverageText(incident.ID, incident.Incident, "post-mortem")
		if incidentText := strings.TrimSpace(incident.Incident); incidentText != "" && incidentText != label {
			label += ": " + incidentText
		}
		values = append(values, "Post-mortem coverage: "+label)
	}
	for _, model := range coverage.FormalModels {
		label := firstCoverageText(model.ID, model.Model, "formal model")
		if modelText := strings.TrimSpace(model.Model); modelText != "" && modelText != label {
			label += ": " + modelText
		}
		values = append(values, "Formal coverage: "+label)
	}

	return values
}

func firstCoverageText(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}

	return ""
}

func prefixedCoverageValues(label string, values []string) []string {
	values = normalizedStringList(values)
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, label+": "+value)
	}

	return out
}

func coverageLabelWithStatus(label string, status string) string {
	label = strings.TrimSpace(label)
	status = strings.TrimSpace(status)
	if label == "" {
		label = "coverage item"
	}
	if status == "" {
		return label
	}

	return label + " [" + status + "]"
}

func securityCaseLabel(id string, status string, risk string, attack string, expected string) string {
	label := firstCoverageText(id, risk, attack, "security case")
	if risk := strings.TrimSpace(risk); risk != "" && risk != label {
		label += ": " + risk
	}
	if attack := strings.TrimSpace(attack); attack != "" {
		label += " attack=" + attack
	}
	if expected := strings.TrimSpace(expected); expected != "" {
		label += " expected=" + expected
	}

	return coverageLabelWithStatus(label, status)
}

func privacyControlLabel(id string, status string, dataClass string, policy string, retention string) string {
	label := firstCoverageText(id, dataClass, policy, "privacy control")
	if dataClass := strings.TrimSpace(dataClass); dataClass != "" && dataClass != label {
		label += ": " + dataClass
	}
	if policy := strings.TrimSpace(policy); policy != "" {
		label += " policy=" + policy
	}
	if retention := strings.TrimSpace(retention); retention != "" {
		label += " retention=" + retention
	}

	return coverageLabelWithStatus(label, status)
}

func providerContractLabel(id string, status string, provider string, consumer string) string {
	label := firstCoverageText(id, provider, consumer, "provider contract")
	if provider := strings.TrimSpace(provider); provider != "" && provider != label {
		label += ": " + provider
	}
	if consumer := strings.TrimSpace(consumer); consumer != "" {
		label += " -> " + consumer
	}

	return coverageLabelWithStatus(label, status)
}

func observabilityLabel(id string, status string, signal string, path string) string {
	label := firstCoverageText(id, signal, path, "observability")
	if signal := strings.TrimSpace(signal); signal != "" && signal != label {
		label += ": " + signal
	}
	if path := strings.TrimSpace(path); path != "" {
		label += " at " + path
	}

	return coverageLabelWithStatus(label, status)
}

func performanceBoundLabel(id string, status string, operation string, bound string) string {
	label := firstCoverageText(id, operation, bound, "performance bound")
	if operation := strings.TrimSpace(operation); operation != "" && operation != label {
		label += ": " + operation
	}
	if bound := strings.TrimSpace(bound); bound != "" {
		label += " <= " + bound
	}

	return coverageLabelWithStatus(label, status)
}

func floatMapCoverageValues(label string, values map[string]float64) []string {
	if len(values) == 0 {
		return nil
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s: %s=%g", label, key, values[key]))
	}

	return out
}
