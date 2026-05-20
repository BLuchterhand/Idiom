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

	decisionRequired := requireContract && ledger.Coverage.DecisionTablesTotal > 0
	decisionPass := ledger.Coverage.DecisionTablesTotal == 0 || ledger.Coverage.DecisionTablesEnumerated == ledger.Coverage.DecisionTablesTotal
	record("decision_table", decisionRequired, decisionPass, fmt.Sprintf("expected decision_tables_enumerated == decision_tables_total (%d/%d)", ledger.Coverage.DecisionTablesEnumerated, ledger.Coverage.DecisionTablesTotal), "tdd.json", "coverage_decision_table_incomplete")

	boundaryRequired := requireContract && (hasGateName("boundary_value", ledger.Coverage.GatesPassed) || hasGateName("boundary_value", ledger.Coverage.GatesBlocked))
	boundaryPass := !hasGateName("boundary_value", ledger.Coverage.GatesBlocked)
	record("boundary_value", boundaryRequired, boundaryPass, "boundary value gate must not be blocked when configured", "tdd.json", "coverage_boundary_value_incomplete")

	mutationRequired := requireContract && ledger.Coverage.MutationMinScore > 0
	mutationUnavailable := hasUnavailableMutationBlocker(ledger.Coverage.GatesBlocked)
	mutationPass := false
	if mutationUnavailable {
		mutationPass = true
	} else if ledger.Coverage.MutationScore != nil {
		mutationPass = *ledger.Coverage.MutationScore >= float64(ledger.Coverage.MutationMinScore)
	}
	record("mutation", mutationRequired, mutationPass, fmt.Sprintf("expected mutation_score >= mutation_min_score or unavailable-tool blocker (min=%d)", ledger.Coverage.MutationMinScore), "tdd.json", "coverage_mutation_incomplete")

	sort.Slice(out.Criteria, func(i, j int) bool { return out.Criteria[i].Key < out.Criteria[j].Key })
	sort.Slice(out.Findings, func(i, j int) bool {
		if out.Findings[i].Criterion == out.Findings[j].Criterion {
			return out.Findings[i].Kind < out.Findings[j].Kind
		}
		return out.Findings[i].Criterion < out.Findings[j].Criterion
	})
	sort.Slice(out.Blockers, func(i, j int) bool {
		if out.Blockers[i].Criterion == out.Blockers[j].Criterion {
			return out.Blockers[i].Kind < out.Blockers[j].Kind
		}
		return out.Blockers[i].Criterion < out.Blockers[j].Criterion
	})
	return out
}

func BuildKnownTDDObligationIDs(ledger TDDLedger, records []ImplementationLedgerRecord, artifactTaskIDs ...string) map[string]bool {
	ids := knownTDDObligationIDs(ledger)
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
