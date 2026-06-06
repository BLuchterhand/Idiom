package productworkflow

import (
	"fmt"
	"sort"
	"strings"
)

func EvaluateCoverageGate(prd PRDDocument, adrIndex ADRIndex, adrs []ParsedADR, ledger TDDLedger, traceability TraceabilityMatrix, knownTDDIDs ...map[string]bool) CoverageGateEvaluation {
	out := CoverageGateEvaluation{
		EffortID:     strings.TrimSpace(ledger.EffortID),
		TaskID:       strings.TrimSpace(ledger.TaskID),
		CoverageTier: strings.TrimSpace(ledger.CoverageTier),
		Status:       "pass",
		Criteria:     []CoverageGateCriterion{},
		Findings:     []CoverageGateFinding{},
		Blockers:     []CoverageGateBlocker{},
	}
	if out.CoverageTier == "" {
		out.CoverageTier = "unit"
	}

	record := func(key string, required bool, pass bool, detail string, file string, findingKind string) {
		status := "pass"
		if !required {
			status = "not_applicable"
		}
		if required && !pass {
			status = "blocked"
			out.Status = "blocked"
			out.Findings = append(out.Findings, CoverageGateFinding{
				Kind:      findingKind,
				Criterion: key,
				Message:   detail,
				File:      file,
				Severity:  "error",
			})
			out.Blockers = append(out.Blockers, CoverageGateBlocker{
				Kind:      findingKind,
				Criterion: key,
				Message:   detail,
				File:      file,
			})
		}
		out.Criteria = append(out.Criteria, CoverageGateCriterion{
			Key:      key,
			Required: required,
			Status:   status,
			Details:  detail,
		})
	}

	requireBehavior := tierRequiresBehaviorCoverage(out.CoverageTier)
	requireContract := tierRequiresContractCoverage(out.CoverageTier)

	tddIDs := knownTDDObligationIDs(ledger, knownTDDIDs...)
	prdTDDLinkErrs := ValidatePRDTDDObligationCrossLinks(prd.Requirements, tddIDs)
	adrTDDLinkErrs := ValidateADRCrossLinks(acceptedParsedADRs(adrs), nil, nil, tddIDs)
	appendPRDTDDObligationLinkBlockers(&out, prdTDDLinkErrs)
	appendADRTDDObligationLinkBlockers(&out, adrTDDLinkErrs)
	evidencePass := len(prdTDDLinkErrs) == 0 && len(adrTDDLinkErrs) == 0 && evaluateEvidenceAndAcceptanceCoverage(prd, adrIndex, adrs, traceability)
	record("evidence_acceptance", true, evidencePass, "all requirements and accepted artifacts must map evidence and traceability", "evidence/traceability_matrix.json", "coverage_evidence_acceptance_incomplete")

	// TDD Evidence: fail closed if obligations exist but none are satisfied by verification records.
	tddEvidencePass := len(tddIDs) == 0 || evaluateTDDEvidenceCoverage(ledger, knownTDDIDs...)
	record("tdd_evidence", true, tddEvidencePass, fmt.Sprintf("expected all %d TDD obligations to have verification evidence", len(tddIDs)), "tdd.json", "coverage_tdd_evidence_missing")

	earsPass := ledger.Coverage.EARSTotal > 0 && ledger.Coverage.EARSMapped == ledger.Coverage.EARSTotal
	record("ears", requireBehavior, earsPass, fmt.Sprintf("expected ears_mapped == ears_total (%d/%d)", ledger.Coverage.EARSMapped, ledger.Coverage.EARSTotal), "tdd.json", "coverage_ears_incomplete")

	bddPass := ledger.Coverage.BDDTotal > 0 && ledger.Coverage.BDDWithConcreteData == ledger.Coverage.BDDTotal
	record("bdd", requireBehavior, bddPass, fmt.Sprintf("expected bdd_with_concrete_data == bdd_total (%d/%d)", ledger.Coverage.BDDWithConcreteData, ledger.Coverage.BDDTotal), "tdd.json", "coverage_bdd_incomplete")

	contractPass := gatePasses("contract", ledger.Coverage.GatesPassed, ledger.Coverage.GatesBlocked)
	record("contract", requireBehavior, contractPass, "required contract coverage gate must be passed", "tdd.json", "coverage_contract_incomplete")

	fmeaPass := ledger.Coverage.FMEAModesTotal == 0 || ledger.Coverage.FMEAModesMitigated == ledger.Coverage.FMEAModesTotal
	record("fmea", requireContract, fmeaPass, fmt.Sprintf("expected fmea_modes_mitigated == fmea_modes_total (%d/%d)", ledger.Coverage.FMEAModesMitigated, ledger.Coverage.FMEAModesTotal), "tdd.json", "coverage_fmea_incomplete")

	hazopPass := ledger.Coverage.HAZOPDeviationsTotal == 0 || ledger.Coverage.HAZOPDeviationsTested == ledger.Coverage.HAZOPDeviationsTotal
	record("hazop", requireContract, hazopPass, fmt.Sprintf("expected hazop_deviations_tested == hazop_deviations_total (%d/%d)", ledger.Coverage.HAZOPDeviationsTested, ledger.Coverage.HAZOPDeviationsTotal), "tdd.json", "coverage_hazop_incomplete")

	pbtPass := ledger.Coverage.InvariantsTotal == 0 || ledger.Coverage.InvariantsWithPBT == ledger.Coverage.InvariantsTotal
	if requireContract && ledger.Coverage.InvariantsTotal == 0 {
		// Property-based coverage must fail closed when invariants_total is zero
		// because no invariant inventory means property-based obligations were not planned.
		pbtPass = false
		record("pbt", requireContract, pbtPass, fmt.Sprintf("expected invariants_with_pbt == invariants_total (%d/%d), but invariants_total is zero (no property-based obligations planned)", ledger.Coverage.InvariantsWithPBT, ledger.Coverage.InvariantsTotal), "tdd.json", "coverage_pbt_incomplete")
	} else {
		record("pbt", requireContract, pbtPass, fmt.Sprintf("expected invariants_with_pbt == invariants_total (%d/%d)", ledger.Coverage.InvariantsWithPBT, ledger.Coverage.InvariantsTotal), "tdd.json", "coverage_pbt_incomplete")
	}

	stateMachinePass := ledger.Coverage.StateMachinesTotal == 0 || ledger.Coverage.StateMachinesCovered == ledger.Coverage.StateMachinesTotal
	if requireContract && ledger.Coverage.StateMachinesTotal == 0 {
		stateMachinePass = false
		record("state_machine", requireContract, stateMachinePass, fmt.Sprintf("expected state_machines_covered == state_machines_total (%d/%d), but state_machines_total is zero (no state-machine obligations planned)", ledger.Coverage.StateMachinesCovered, ledger.Coverage.StateMachinesTotal), "tdd.json", "coverage_state_machine_incomplete")
	} else {
		record("state_machine", requireContract, stateMachinePass, fmt.Sprintf("expected state_machines_covered == state_machines_total (%d/%d)", ledger.Coverage.StateMachinesCovered, ledger.Coverage.StateMachinesTotal), "tdd.json", "coverage_state_machine_incomplete")
	}

	decisionRequired := requireContract && ledger.Coverage.DecisionTablesTotal > 0
	decisionPass := ledger.Coverage.DecisionTablesTotal == 0 || ledger.Coverage.DecisionTablesEnumerated == ledger.Coverage.DecisionTablesTotal
	record("decision_table", decisionRequired, decisionPass, fmt.Sprintf("expected decision_tables_enumerated == decision_tables_total (%d/%d)", ledger.Coverage.DecisionTablesEnumerated, ledger.Coverage.DecisionTablesTotal), "tdd.json", "coverage_decision_table_incomplete")

	boundaryConfigured := hasGateName("boundary_value", ledger.Coverage.GatesPassed) || hasGateName("boundary_value", ledger.Coverage.GatesBlocked)
	boundaryRequired := requireContract && (ledger.Coverage.BoundaryValuesTotal > 0 || boundaryConfigured)
	boundaryPass := ledger.Coverage.BoundaryValuesTotal == 0 || ledger.Coverage.BoundaryValuesTested == ledger.Coverage.BoundaryValuesTotal
	if hasGateName("boundary_value", ledger.Coverage.GatesBlocked) {
		boundaryPass = false
	}
	if requireContract && boundaryConfigured && ledger.Coverage.BoundaryValuesTotal == 0 {
		boundaryPass = false
		record("boundary_value", boundaryRequired, boundaryPass, fmt.Sprintf("expected boundary_values_tested == boundary_values_total (%d/%d), but boundary_values_total is zero (boundary-value gate configured without numeric-bound inventory)", ledger.Coverage.BoundaryValuesTested, ledger.Coverage.BoundaryValuesTotal), "tdd.json", "coverage_boundary_value_incomplete")
	} else {
		record("boundary_value", boundaryRequired, boundaryPass, fmt.Sprintf("expected boundary_values_tested == boundary_values_total (%d/%d)", ledger.Coverage.BoundaryValuesTested, ledger.Coverage.BoundaryValuesTotal), "tdd.json", "coverage_boundary_value_incomplete")
	}

	faultTreeConfigured := hasGateName("fault_tree", ledger.Coverage.GatesPassed) || hasGateName("fault_tree", ledger.Coverage.GatesBlocked)
	faultTreeRequired := requireContract && (ledger.Coverage.FaultTreeLeavesTotal > 0 || faultTreeConfigured)
	faultTreePass := ledger.Coverage.FaultTreeLeavesTotal == 0 || ledger.Coverage.FaultTreeLeavesTested == ledger.Coverage.FaultTreeLeavesTotal
	if hasGateName("fault_tree", ledger.Coverage.GatesBlocked) {
		faultTreePass = false
	}
	if requireContract && faultTreeConfigured && ledger.Coverage.FaultTreeLeavesTotal == 0 {
		faultTreePass = false
		record("fault_tree", faultTreeRequired, faultTreePass, fmt.Sprintf("expected fault_tree_leaves_tested == fault_tree_leaves_total (%d/%d), but fault_tree_leaves_total is zero (fault-tree gate configured without leaf inventory)", ledger.Coverage.FaultTreeLeavesTested, ledger.Coverage.FaultTreeLeavesTotal), "tdd.json", "coverage_fault_tree_incomplete")
	} else {
		record("fault_tree", faultTreeRequired, faultTreePass, fmt.Sprintf("expected fault_tree_leaves_tested == fault_tree_leaves_total (%d/%d)", ledger.Coverage.FaultTreeLeavesTested, ledger.Coverage.FaultTreeLeavesTotal), "tdd.json", "coverage_fault_tree_incomplete")
	}

	postMortemConfigured := hasGateName("post_mortem", ledger.Coverage.GatesPassed) || hasGateName("post_mortem", ledger.Coverage.GatesBlocked)
	postMortemRequired := requireContract && (ledger.Coverage.PostMortemIncidentsTotal > 0 || postMortemConfigured)
	postMortemPass := ledger.Coverage.PostMortemIncidentsTotal == 0 || ledger.Coverage.PostMortemCatchingTests == ledger.Coverage.PostMortemIncidentsTotal
	if hasGateName("post_mortem", ledger.Coverage.GatesBlocked) {
		postMortemPass = false
	}
	if requireContract && postMortemConfigured && ledger.Coverage.PostMortemIncidentsTotal == 0 {
		postMortemPass = false
		record("post_mortem", postMortemRequired, postMortemPass, fmt.Sprintf("expected post_mortem_catching_tests == post_mortem_incidents_total (%d/%d), but post_mortem_incidents_total is zero (post-mortem gate configured without incident inventory)", ledger.Coverage.PostMortemCatchingTests, ledger.Coverage.PostMortemIncidentsTotal), "tdd.json", "coverage_post_mortem_incomplete")
	} else {
		record("post_mortem", postMortemRequired, postMortemPass, fmt.Sprintf("expected post_mortem_catching_tests == post_mortem_incidents_total (%d/%d)", ledger.Coverage.PostMortemCatchingTests, ledger.Coverage.PostMortemIncidentsTotal), "tdd.json", "coverage_post_mortem_incomplete")
	}

	formalConfigured := hasGateName("formal_verification", ledger.Coverage.GatesPassed) || hasGateName("formal_verification", ledger.Coverage.GatesBlocked)
	formalRequired := requireContract && (ledger.Coverage.FormalModelsTotal > 0 || formalConfigured)
	formalPass := ledger.Coverage.FormalModelsTotal == 0 || ledger.Coverage.FormalModelsVerified == ledger.Coverage.FormalModelsTotal
	if hasGateName("formal_verification", ledger.Coverage.GatesBlocked) {
		formalPass = false
	}
	if requireContract && formalConfigured && ledger.Coverage.FormalModelsTotal == 0 {
		formalPass = false
		record("formal_verification", formalRequired, formalPass, fmt.Sprintf("expected formal_models_verified == formal_models_total (%d/%d), but formal_models_total is zero (formal verification gate configured without model inventory)", ledger.Coverage.FormalModelsVerified, ledger.Coverage.FormalModelsTotal), "tdd.json", "coverage_formal_verification_incomplete")
	} else {
		record("formal_verification", formalRequired, formalPass, fmt.Sprintf("expected formal_models_verified == formal_models_total (%d/%d)", ledger.Coverage.FormalModelsVerified, ledger.Coverage.FormalModelsTotal), "tdd.json", "coverage_formal_verification_incomplete")
	}

	mutationRequired := requireContract && ledger.Coverage.MutationMinScore > 0
	mutationUnavailable := hasUnavailableMutationBlocker(ledger.Coverage.GatesBlocked)
	mutationPass := false
	if mutationUnavailable {
		mutationPass = true
	} else if ledger.Coverage.MutationScore != nil {
		mutationPass = *ledger.Coverage.MutationScore >= float64(ledger.Coverage.MutationMinScore)
	}
	record("mutation", mutationRequired, mutationPass, fmt.Sprintf("expected mutation_score >= mutation_min_score or unavailable-tool blocker (min=%d)", ledger.Coverage.MutationMinScore), "tdd.json", "coverage_mutation_incomplete")

	sortCoverageGateEvaluation(&out)
	return out
}

func BuildKnownTDDObligationIDs(ledger TDDLedger, records []ImplementationLedgerRecord, artifactTaskIDs ...string) map[string]bool {
	ids := knownTDDObligationIDs(ledger)
	for id := range BuildSatisfiedTDDObligationIDs(ledger, records, artifactTaskIDs...) {
		ids[id] = true
	}
	return ids
}

func BuildSatisfiedTDDObligationIDs(ledger TDDLedger, records []ImplementationLedgerRecord, artifactTaskIDs ...string) map[string]bool {
	ids := map[string]bool{}
	allowedTasks := map[string]bool{}
	for _, taskID := range artifactTaskIDs {
		trimmed := strings.TrimSpace(taskID)
		if trimmed != "" {
			allowedTasks[trimmed] = true
		}
	}
	for _, rec := range records {
		if rec.RecordType != "verification_completed" {
			continue
		}
		if strings.TrimSpace(ledger.EffortID) != "" && strings.TrimSpace(rec.EffortID) != strings.TrimSpace(ledger.EffortID) {
			continue
		}
		if len(allowedTasks) > 0 && !allowedTasks[strings.TrimSpace(rec.TaskID)] {
			continue
		}
		for _, id := range rec.TDDObligationsSatisfied {
			trimmed := strings.TrimSpace(id)
			if trimmed != "" {
				ids[trimmed] = true
			}
		}
	}
	return ids
}

func CoverageGateHasBlockers(eval CoverageGateEvaluation) bool {
	return strings.EqualFold(strings.TrimSpace(eval.Status), "blocked") || len(eval.Blockers) > 0
}

func CoverageGateHasOnlyTDDEvidenceBlockers(eval CoverageGateEvaluation) bool {
	if !CoverageGateHasBlockers(eval) || len(eval.Blockers) == 0 {
		return false
	}
	for _, blocker := range eval.Blockers {
		if strings.TrimSpace(blocker.Kind) != "coverage_tdd_evidence_missing" {
			return false
		}
		if criterion := strings.TrimSpace(blocker.Criterion); criterion != "" && criterion != "tdd_evidence" {
			return false
		}
	}
	return true
}

func EvaluateCoverageGateWithAuthorityPreflight(prd PRDDocument, adrIndex ADRIndex, adrs []ParsedADR, ledger TDDLedger, traceability TraceabilityMatrix, lintFindings []LintSummaryFinding, knownTDDIDs ...map[string]bool) CoverageGateEvaluation {
	eval := EvaluateCoverageGate(prd, adrIndex, adrs, ledger, traceability, knownTDDIDs...)
	return ApplyCoverageGateAuthorityPreflight(eval, lintFindings)
}

func BuildCoverageGateEvaluatedEvent(eval CoverageGateEvaluation, ledger TDDLedger) CoverageGateEvaluatedEvent {
	effortID := strings.TrimSpace(eval.EffortID)
	if effortID == "" {
		effortID = strings.TrimSpace(ledger.EffortID)
	}
	taskID := strings.TrimSpace(eval.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(ledger.TaskID)
	}
	gateResult := strings.TrimSpace(eval.Status)
	if gateResult == "" {
		gateResult = "blocked"
	}
	return CoverageGateEvaluatedEvent{
		Event:    "coverage_gate_evaluated",
		EffortID: effortID,
		TaskID:   taskID,
		Coverage: CoverageGateEventCoverage{
			EARSCoveragePct:       coveragePercent(ledger.Coverage.EARSMapped, ledger.Coverage.EARSTotal),
			BDDCoveragePct:        coveragePercent(ledger.Coverage.BDDWithConcreteData, ledger.Coverage.BDDTotal),
			FMEAMitigationPct:     coveragePercent(ledger.Coverage.FMEAModesMitigated, ledger.Coverage.FMEAModesTotal),
			HAZOPTestedPct:        coveragePercent(ledger.Coverage.HAZOPDeviationsTested, ledger.Coverage.HAZOPDeviationsTotal),
			InvariantPBTPct:       coveragePercent(ledger.Coverage.InvariantsWithPBT, ledger.Coverage.InvariantsTotal),
			StateMachinePct:       coveragePercent(ledger.Coverage.StateMachinesCovered, ledger.Coverage.StateMachinesTotal),
			DecisionTablePct:      coveragePercent(ledger.Coverage.DecisionTablesEnumerated, ledger.Coverage.DecisionTablesTotal),
			BoundaryValuePct:      coveragePercent(ledger.Coverage.BoundaryValuesTested, ledger.Coverage.BoundaryValuesTotal),
			FaultTreeCoveragePct:  coveragePercent(ledger.Coverage.FaultTreeLeavesTested, ledger.Coverage.FaultTreeLeavesTotal),
			PostMortemCoveragePct: coveragePercent(ledger.Coverage.PostMortemCatchingTests, ledger.Coverage.PostMortemIncidentsTotal),
			FormalVerificationPct: coveragePercent(ledger.Coverage.FormalModelsVerified, ledger.Coverage.FormalModelsTotal),
			MutationScore:         ledger.Coverage.MutationScore,
		},
		GateResult:     gateResult,
		BlockedReasons: coverageGateBlockedReasons(eval.Blockers),
		Waivers:        []string{},
	}
}

func coveragePercent(numerator int, denominator int) *float64 {
	if denominator <= 0 {
		return nil
	}
	pct := (float64(numerator) / float64(denominator)) * 100
	return &pct
}

func coverageGateBlockedReasons(blockers []CoverageGateBlocker) []string {
	reasons := make([]string, 0, len(blockers))
	seen := map[string]bool{}
	for _, blocker := range blockers {
		reason := strings.TrimSpace(blocker.Criterion)
		kind := strings.TrimSpace(blocker.Kind)
		switch {
		case reason != "" && kind != "":
			reason += ":" + kind
		case reason == "":
			reason = kind
		}
		if reason == "" || seen[reason] {
			continue
		}
		seen[reason] = true
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	return reasons
}

func ApplyCoverageGateAuthorityPreflight(eval CoverageGateEvaluation, lintFindings []LintSummaryFinding) CoverageGateEvaluation {
	authorityFindings := coverageGateAuthorityFindings(lintFindings)
	status := "pass"
	if len(authorityFindings) > 0 {
		status = "blocked"
		eval.Status = "blocked"
	}
	eval.Criteria = append(eval.Criteria, CoverageGateCriterion{
		Key:      "authority_preflight",
		Required: true,
		Status:   status,
		Details:  "canonical product lint/audit traceability, acceptance, and supersession validations must pass before coverage evaluation",
	})
	for _, finding := range authorityFindings {
		if coverageGateAlreadyHasAuthorityFinding(eval, finding) {
			continue
		}
		message := strings.TrimSpace(finding.Message)
		if message == "" {
			message = strings.TrimSpace(finding.Code)
		}
		eval.Findings = append(eval.Findings, CoverageGateFinding{
			Kind:      strings.TrimSpace(finding.Code),
			Criterion: "authority_preflight",
			Message:   message,
			File:      strings.TrimSpace(finding.Path),
			Severity:  "error",
		})
		eval.Blockers = append(eval.Blockers, CoverageGateBlocker{
			Kind:      strings.TrimSpace(finding.Code),
			Criterion: "authority_preflight",
			Message:   message,
			File:      strings.TrimSpace(finding.Path),
		})
	}
	sortCoverageGateEvaluation(&eval)
	return eval
}

func coverageGateAuthorityFindings(findings []LintSummaryFinding) []LintSummaryFinding {
	out := make([]LintSummaryFinding, 0, len(findings))
	for _, finding := range findings {
		if coverageGateAuthorityFinding(finding) {
			out = append(out, finding)
		}
	}
	return out
}

func coverageGateAuthorityFinding(finding LintSummaryFinding) bool {
	code := strings.TrimSpace(finding.Code)
	relPath := strings.ReplaceAll(strings.TrimSpace(finding.Path), "\\", "/")
	if strings.HasPrefix(code, "adr_supersession_") {
		return true
	}
	if code == "adr_index_frontmatter_mismatch" {
		return true
	}
	switch {
	case relPath == "prd.md":
		return code == "parse_error" || code == "broken_link"
	case relPath == "adrs/index.json" || strings.HasPrefix(relPath, "adrs/"):
		return code == "parse_error" || code == "broken_link"
	case relPath == "evidence/traceability_matrix.json":
		return code == "parse_error" || code == "broken_link" || code == "missing_file"
	case relPath == "tdd.json":
		return code == "parse_error" || code == "broken_link"
	default:
		return false
	}
}

func coverageGateAlreadyHasAuthorityFinding(eval CoverageGateEvaluation, finding LintSummaryFinding) bool {
	code := strings.TrimSpace(finding.Code)
	relPath := strings.TrimSpace(finding.Path)
	message := strings.TrimSpace(finding.Message)
	for _, existing := range eval.Findings {
		if strings.TrimSpace(existing.Kind) == code &&
			strings.TrimSpace(existing.Criterion) == "authority_preflight" &&
			strings.TrimSpace(existing.File) == relPath &&
			strings.TrimSpace(existing.Message) == message {
			return true
		}
	}
	return false
}

func sortCoverageGateEvaluation(eval *CoverageGateEvaluation) {
	if eval == nil {
		return
	}
	sort.Slice(eval.Criteria, func(i, j int) bool { return eval.Criteria[i].Key < eval.Criteria[j].Key })
	sort.Slice(eval.Findings, func(i, j int) bool {
		if eval.Findings[i].Criterion == eval.Findings[j].Criterion {
			return eval.Findings[i].Kind < eval.Findings[j].Kind
		}
		return eval.Findings[i].Criterion < eval.Findings[j].Criterion
	})
	sort.Slice(eval.Blockers, func(i, j int) bool {
		if eval.Blockers[i].Criterion == eval.Blockers[j].Criterion {
			return eval.Blockers[i].Kind < eval.Blockers[j].Kind
		}
		return eval.Blockers[i].Criterion < eval.Blockers[j].Criterion
	})
}

func appendPRDTDDObligationLinkBlockers(out *CoverageGateEvaluation, errs []PRDTDDObligationLinkError) {
	if out == nil || len(errs) == 0 {
		return
	}
	out.Status = "blocked"
	for _, linkErr := range errs {
		reqID := strings.TrimSpace(linkErr.RequirementID)
		tddID := strings.TrimSpace(linkErr.TDDObligation)
		message := strings.TrimSpace(linkErr.Message)
		if message == "" {
			message = fmt.Sprintf("PRD must-priority requirement %s references TDD obligation %q not found in current or historical TDD obligation IDs", reqID, tddID)
		}
		out.Findings = append(out.Findings, CoverageGateFinding{
			Kind:      "coverage_prd_tdd_obligation_unknown",
			Criterion: "evidence_acceptance",
			Message:   message,
			File:      "prd.md",
			Severity:  "error",
		})
		out.Blockers = append(out.Blockers, CoverageGateBlocker{
			Kind:      "coverage_prd_tdd_obligation_unknown",
			Criterion: "evidence_acceptance",
			Message:   message,
			File:      "prd.md",
		})
	}
}

func appendADRTDDObligationLinkBlockers(out *CoverageGateEvaluation, errs []ADRCrossLinkError) {
	if out == nil || len(errs) == 0 {
		return
	}
	out.Status = "blocked"
	for _, linkErr := range errs {
		if strings.TrimSpace(linkErr.Field) != "tdd_obligations" {
			continue
		}
		message := strings.TrimSpace(linkErr.Message)
		if message == "" {
			message = fmt.Sprintf("ADR %s: tdd_obligation %q not found in parsed TDD obligation IDs", strings.TrimSpace(linkErr.ADRPath), strings.TrimSpace(linkErr.Value))
		}
		out.Findings = append(out.Findings, CoverageGateFinding{
			Kind:      "coverage_adr_tdd_obligation_unknown",
			Criterion: "evidence_acceptance",
			Message:   message,
			File:      strings.TrimSpace(linkErr.ADRPath),
			Severity:  "error",
		})
		out.Blockers = append(out.Blockers, CoverageGateBlocker{
			Kind:      "coverage_adr_tdd_obligation_unknown",
			Criterion: "evidence_acceptance",
			Message:   message,
			File:      strings.TrimSpace(linkErr.ADRPath),
		})
	}
}

func acceptedParsedADRs(adrs []ParsedADR) []ParsedADR {
	out := make([]ParsedADR, 0, len(adrs))
	for _, adr := range adrs {
		if isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			out = append(out, adr)
		}
	}
	return out
}

func tierRequiresBehaviorCoverage(tier string) bool {
	switch strings.TrimSpace(strings.ToLower(tier)) {
	case "behavior_standard", "contract_high", "formal_optional":
		return true
	default:
		return false
	}
}

func tierRequiresContractCoverage(tier string) bool {
	switch strings.TrimSpace(strings.ToLower(tier)) {
	case "contract_high", "formal_optional":
		return true
	default:
		return false
	}
}

func gatePasses(name string, passed []string, blocked []string) bool {
	return hasGateName(name, passed) && !hasGateName(name, blocked)
}

func hasGateName(name string, values []string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), name) {
			return true
		}
	}
	return false
}

func hasUnavailableMutationBlocker(blocked []string) bool {
	for _, v := range blocked {
		token := strings.ToLower(strings.TrimSpace(v))
		if token == "mutation_unavailable_tool" || token == "unavailable_tool:mutation" || token == "mutation:unavailable_tool" {
			return true
		}
	}
	return false
}

func knownTDDObligationIDs(ledger TDDLedger, extra ...map[string]bool) map[string]bool {
	ids := map[string]bool{}
	for _, obligation := range ledger.Obligations {
		id := strings.TrimSpace(obligation.ID)
		if id != "" {
			ids[id] = true
		}
	}
	for _, provided := range extra {
		for id, ok := range provided {
			trimmed := strings.TrimSpace(id)
			if ok && trimmed != "" {
				ids[trimmed] = true
			}
		}
	}
	return ids
}

func evaluateEvidenceAndAcceptanceCoverage(prd PRDDocument, adrIndex ADRIndex, adrs []ParsedADR, traceability TraceabilityMatrix) bool {
	if !isAcceptedArtifact(prd.FrontMatter.Status) {
		return false
	}
	if len(adrIndex.ADRs) == 0 || len(adrs) == 0 {
		return false
	}
	acceptedADRIDs := map[string]bool{}
	for _, adr := range adrs {
		if !isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			continue
		}
		if strings.TrimSpace(adr.Doc.FrontMatter.ADRID) == "" {
			continue
		}
		if len(adr.Doc.FrontMatter.EvidenceRefs) == 0 {
			return false
		}
		acceptedADRIDs[adr.Doc.FrontMatter.ADRID] = true
	}
	if len(acceptedADRIDs) == 0 {
		return false
	}
	traceReqs := map[string]bool{}
	traceADRs := map[string]bool{}
	for _, row := range traceability.Matrix {
		traceReqs[strings.TrimSpace(row.PRDRequirement)] = true
		traceADRs[strings.TrimSpace(row.ADR)] = true
	}
	for _, req := range prd.Requirements {
		reqID := strings.TrimSpace(req.ID)
		if reqID == "" {
			continue
		}
		if len(req.EvidenceRefs) == 0 {
			return false
		}
		if len(req.TDDObligations) == 0 {
			return false
		}
		if !traceReqs[reqID] {
			return false
		}
	}
	for _, adr := range adrs {
		adrID := strings.TrimSpace(adr.Doc.FrontMatter.ADRID)
		if !acceptedADRIDs[adrID] {
			continue
		}
		if !traceADRs[adrID] {
			return false
		}
	}
	return true
}

func evaluateTDDEvidenceCoverage(ledger TDDLedger, knownTDDIDs ...map[string]bool) bool {
	allTDDIDs := knownTDDObligationIDs(ledger, knownTDDIDs...)
	if len(allTDDIDs) == 0 {
		return true
	}

	// The extra maps passed here are satisfied obligation IDs from verification
	// records. Ledger obligations are known obligations, not evidence.
	if len(knownTDDIDs) == 0 {
		return false
	}

	for id := range allTDDIDs {
		found := false
		for _, extra := range knownTDDIDs {
			if extra[id] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
