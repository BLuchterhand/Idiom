package productworkflow

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

func validateEffortIndex(idx EffortIndex) error {
	var errs []error

	require(&errs, idx.SchemaVersion, "effort index missing schema_version")
	require(&errs, idx.UpdatedAt, "effort index missing updated_at")

	if len(idx.Efforts) == 0 {
		errs = append(errs, errors.New("effort index missing efforts"))
	}

	for i, entry := range idx.Efforts {
		prefix := fmt.Sprintf("effort index entry %d", i)
		require(&errs, entry.EffortID, prefix+" missing effort_id")
		require(&errs, entry.Title, prefix+" missing title")
		require(&errs, entry.StatusBucket, prefix+" missing status_bucket")
		require(&errs, entry.CurrentPhase, prefix+" missing current_phase")
		require(&errs, entry.CurrentState, prefix+" missing current_state")
		require(&errs, entry.FolderPath, prefix+" missing folder_path")
		require(&errs, entry.TaskID, prefix+" missing task_id")
		if entry.StatusBucket != "" && !validStatusBuckets[entry.StatusBucket] {
			errs = append(errs, fmt.Errorf("%s invalid status_bucket %q", prefix, entry.StatusBucket))
		}
		if entry.CurrentState != "" {
			if _, ok := validStates[entry.CurrentState]; !ok {
				errs = append(errs, fmt.Errorf("%s invalid current_state %q", prefix, entry.CurrentState))
			}
		}
		if consistencyBlocker := validateStateBucketConsistency(entry.StatusBucket, entry.CurrentState); consistencyBlocker != nil {
			errs = append(errs, fmt.Errorf("%s %s: %s", prefix, consistencyBlocker.Code, consistencyBlocker.Message))
		}
	}

	return errors.Join(errs...)
}

func validateWorkflowState(ws WorkflowState) error {
	var errs []error

	require(&errs, ws.SchemaVersion, "workflow state missing schema_version")
	require(&errs, ws.EffortID, "workflow state missing effort_id")
	require(&errs, ws.TaskID, "workflow state missing task_id")
	require(&errs, ws.Title, "workflow state missing title")
	require(&errs, ws.StatusBucket, "workflow state missing status_bucket")
	require(&errs, ws.CurrentPhase, "workflow state missing current_phase")
	require(&errs, ws.CurrentState, "workflow state missing current_state")
	require(&errs, ws.FolderPath, "workflow state missing folder_path")
	require(&errs, ws.DurabilityLevel, "workflow state missing durability_level")
	require(&errs, ws.Context.ContextPackID, "workflow state missing context.context_pack_id")
	require(&errs, ws.Coverage.CoverageTier, "workflow state missing coverage.coverage_tier")
	require(&errs, ws.Privacy.RedactionStatus, "workflow state missing privacy.redaction_status")

	if ws.StatusBucket != "" && !validStatusBuckets[ws.StatusBucket] {
		errs = append(errs, fmt.Errorf("workflow state invalid status_bucket %q", ws.StatusBucket))
	}
	if ws.CurrentState != "" {
		if _, ok := validStates[ws.CurrentState]; !ok {
			errs = append(errs, fmt.Errorf("workflow state invalid current_state %q", ws.CurrentState))
		}
	}
	if consistencyBlocker := validateStateBucketConsistency(ws.StatusBucket, ws.CurrentState); consistencyBlocker != nil {
		errs = append(errs, fmt.Errorf("workflow state %s: %s", consistencyBlocker.Code, consistencyBlocker.Message))
	}
	if len(ws.NextValidActions) == 0 && !(ws.StatusBucket == WorkflowStatusBucketClosed && ws.CurrentState == WorkflowStateClosed) {
		errs = append(errs, errors.New("workflow state missing next_valid_actions"))
	}

	if len(ws.ArtifactPaths) == 0 {
		errs = append(errs, errors.New("workflow state missing artifact_paths"))
	}

	if err := validatePrivacyFlags("workflow state privacy", ws.Privacy); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

var allowedCoverageTiers = map[string]bool{
	"documentation":     true,
	"local_low":         true,
	"behavior_standard": true,
	"contract_high":     true,
	"formal_optional":   true,
	"unit":              true,
}

var coverageTierPromptOrder = []string{"documentation", "unit", "local_low", "behavior_standard", "contract_high", "formal_optional"}

func AllowedCoverageTiers() []string {
	return orderedAllowedValues(allowedCoverageTiers, coverageTierPromptOrder)
}

var allowedObligationTypes = map[string]bool{
	"characterization":    true,
	"regression":          true,
	"acceptance":          true,
	"contract":            true,
	"source_sanity":       true,
	"eval":                true,
	"audit":               true,
	"property_based":      true,
	"fmea_negative":       true,
	"hazop_deviation":     true,
	"fault_tree":          true,
	"decision_table":      true,
	"boundary_value":      true,
	"mutation_score":      true,
	"post_mortem":         true,
	"formal_verification": true,
	"fuzz":                true,
	"fuzz_target":         true,
	"rag_eval":            true,
	"llm_security":        true,
	"security":            true,
	"privacy":             true,
	"provider_contract":   true,
	"observability":       true,
	"performance":         true,
	"scripted_e2e":        true,
	"e2e_journey":         true,
	"manual":              true,
}

func AllowedObligationTypes() []string {
	return orderedAllowedValues(allowedObligationTypes, nil)
}

func orderedAllowedValues(values map[string]bool, preferred []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range preferred {
		if !values[value] {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	var rest []string
	for value, allowed := range values {
		if !allowed {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		rest = append(rest, value)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func validateTDDLedger(ledger TDDLedger) error {
	var errs []error

	require(&errs, ledger.SchemaVersion, "tdd ledger missing schema_version")
	require(&errs, ledger.EffortID, "tdd ledger missing effort_id")
	require(&errs, ledger.TaskID, "tdd ledger missing task_id")
	require(&errs, ledger.CoverageTier, "tdd ledger missing coverage_tier")

	if ledger.CoverageTier != "" && !allowedCoverageTiers[ledger.CoverageTier] {
		errs = append(errs, fmt.Errorf("tdd ledger invalid coverage_tier %q", ledger.CoverageTier))
	}

	if len(ledger.Obligations) == 0 {
		errs = append(errs, errors.New("tdd ledger missing obligations"))
	}

	errs = append(errs, validateTDDCoverage(ledger.Coverage)...)
	if ledger.BehavioralCoverage != nil {
		if err := ValidateBehavioralCoverageContract(*ledger.BehavioralCoverage); err != nil {
			errs = append(errs, err)
		}
		if err := ValidateBehavioralCoverageProjection(*ledger.BehavioralCoverage, ledger.Coverage); err != nil {
			errs = append(errs, err)
		}
	}

	for i, obligation := range ledger.Obligations {
		prefix := fmt.Sprintf("tdd obligation %d", i)
		require(&errs, obligation.ID, prefix+" missing id")
		require(&errs, obligation.Type, prefix+" missing type")
		require(&errs, obligation.Behavior, prefix+" missing behavior")
		require(&errs, obligation.Owner, prefix+" missing owner")
		require(&errs, obligation.Command, prefix+" missing command")
		require(&errs, obligation.RequiredBefore, prefix+" missing required_before")
		require(&errs, obligation.Status, prefix+" missing status")

		if obligation.Type != "" && !allowedObligationTypes[obligation.Type] {
			errs = append(errs, fmt.Errorf("%s invalid type %q", prefix, obligation.Type))
		}
	}

	return errors.Join(errs...)
}

func validateTDDCoverage(coverage TDDCoverage) []error {
	var errs []error

	if coverage.MutationMinScore <= 0 {
		errs = append(errs, errors.New("tdd ledger missing positive mutation_min_score"))
	}

	counters := []struct {
		name  string
		value int
	}{
		{"ears_total", coverage.EARSTotal},
		{"ears_mapped", coverage.EARSMapped},
		{"bdd_total", coverage.BDDTotal},
		{"bdd_with_concrete_data", coverage.BDDWithConcreteData},
		{"fmea_modes_total", coverage.FMEAModesTotal},
		{"fmea_modes_mitigated", coverage.FMEAModesMitigated},
		{"hazop_deviations_total", coverage.HAZOPDeviationsTotal},
		{"hazop_deviations_tested", coverage.HAZOPDeviationsTested},
		{"invariants_total", coverage.InvariantsTotal},
		{"invariants_with_pbt", coverage.InvariantsWithPBT},
		{"state_machines_total", coverage.StateMachinesTotal},
		{"state_machines_covered", coverage.StateMachinesCovered},
		{"decision_tables_total", coverage.DecisionTablesTotal},
		{"decision_tables_enumerated", coverage.DecisionTablesEnumerated},
		{"boundary_values_total", coverage.BoundaryValuesTotal},
		{"boundary_values_tested", coverage.BoundaryValuesTested},
		{"fault_tree_leaves_total", coverage.FaultTreeLeavesTotal},
		{"fault_tree_leaves_tested", coverage.FaultTreeLeavesTested},
		{"post_mortem_incidents_total", coverage.PostMortemIncidentsTotal},
		{"post_mortem_catching_tests", coverage.PostMortemCatchingTests},
		{"formal_models_total", coverage.FormalModelsTotal},
		{"formal_models_verified", coverage.FormalModelsVerified},
		{"fuzz_targets_total", coverage.FuzzTargetsTotal},
		{"fuzz_targets_covered", coverage.FuzzTargetsCovered},
		{"rag_evals_total", coverage.RAGEvalsTotal},
		{"rag_evals_measured", coverage.RAGEvalsMeasured},
		{"security_cases_total", coverage.SecurityCasesTotal},
		{"security_cases_tested", coverage.SecurityCasesTested},
		{"privacy_controls_total", coverage.PrivacyControlsTotal},
		{"privacy_controls_tested", coverage.PrivacyControlsTested},
		{"provider_contracts_total", coverage.ProviderContractsTotal},
		{"provider_contracts_tested", coverage.ProviderContractsTested},
		{"observability_total", coverage.ObservabilityTotal},
		{"observability_tested", coverage.ObservabilityTested},
		{"performance_bounds_total", coverage.PerformanceBoundsTotal},
		{"performance_bounds_tested", coverage.PerformanceBoundsTested},
		{"e2e_journeys_total", coverage.E2EJourneysTotal},
		{"e2e_journeys_scripted", coverage.E2EJourneysScripted},
	}
	for _, c := range counters {
		if c.value < 0 {
			errs = append(errs, fmt.Errorf("tdd ledger coverage.%s must be non-negative, got %d", c.name, c.value))
		}
	}

	if coverage.EARSMapped > coverage.EARSTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.ears_mapped (%d) exceeds ears_total (%d)", coverage.EARSMapped, coverage.EARSTotal))
	}

	if coverage.BDDWithConcreteData > coverage.BDDTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.bdd_with_concrete_data (%d) exceeds bdd_total (%d)", coverage.BDDWithConcreteData, coverage.BDDTotal))
	}

	if coverage.FMEAModesMitigated > coverage.FMEAModesTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.fmea_modes_mitigated (%d) exceeds fmea_modes_total (%d)", coverage.FMEAModesMitigated, coverage.FMEAModesTotal))
	}

	if coverage.HAZOPDeviationsTested > coverage.HAZOPDeviationsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.hazop_deviations_tested (%d) exceeds hazop_deviations_total (%d)", coverage.HAZOPDeviationsTested, coverage.HAZOPDeviationsTotal))
	}

	if coverage.InvariantsWithPBT > coverage.InvariantsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.invariants_with_pbt (%d) exceeds invariants_total (%d)", coverage.InvariantsWithPBT, coverage.InvariantsTotal))
	}

	if coverage.StateMachinesCovered > coverage.StateMachinesTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.state_machines_covered (%d) exceeds state_machines_total (%d)", coverage.StateMachinesCovered, coverage.StateMachinesTotal))
	}

	if coverage.DecisionTablesEnumerated > coverage.DecisionTablesTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.decision_tables_enumerated (%d) exceeds decision_tables_total (%d)", coverage.DecisionTablesEnumerated, coverage.DecisionTablesTotal))
	}

	if coverage.BoundaryValuesTested > coverage.BoundaryValuesTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.boundary_values_tested (%d) exceeds boundary_values_total (%d)", coverage.BoundaryValuesTested, coverage.BoundaryValuesTotal))
	}

	if coverage.FaultTreeLeavesTested > coverage.FaultTreeLeavesTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.fault_tree_leaves_tested (%d) exceeds fault_tree_leaves_total (%d)", coverage.FaultTreeLeavesTested, coverage.FaultTreeLeavesTotal))
	}

	if coverage.PostMortemCatchingTests > coverage.PostMortemIncidentsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.post_mortem_catching_tests (%d) exceeds post_mortem_incidents_total (%d)", coverage.PostMortemCatchingTests, coverage.PostMortemIncidentsTotal))
	}

	if coverage.FormalModelsVerified > coverage.FormalModelsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.formal_models_verified (%d) exceeds formal_models_total (%d)", coverage.FormalModelsVerified, coverage.FormalModelsTotal))
	}
	if coverage.FuzzTargetsCovered > coverage.FuzzTargetsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.fuzz_targets_covered (%d) exceeds fuzz_targets_total (%d)", coverage.FuzzTargetsCovered, coverage.FuzzTargetsTotal))
	}
	if coverage.RAGEvalsMeasured > coverage.RAGEvalsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.rag_evals_measured (%d) exceeds rag_evals_total (%d)", coverage.RAGEvalsMeasured, coverage.RAGEvalsTotal))
	}
	if coverage.SecurityCasesTested > coverage.SecurityCasesTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.security_cases_tested (%d) exceeds security_cases_total (%d)", coverage.SecurityCasesTested, coverage.SecurityCasesTotal))
	}
	if coverage.PrivacyControlsTested > coverage.PrivacyControlsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.privacy_controls_tested (%d) exceeds privacy_controls_total (%d)", coverage.PrivacyControlsTested, coverage.PrivacyControlsTotal))
	}
	if coverage.ProviderContractsTested > coverage.ProviderContractsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.provider_contracts_tested (%d) exceeds provider_contracts_total (%d)", coverage.ProviderContractsTested, coverage.ProviderContractsTotal))
	}
	if coverage.ObservabilityTested > coverage.ObservabilityTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.observability_tested (%d) exceeds observability_total (%d)", coverage.ObservabilityTested, coverage.ObservabilityTotal))
	}
	if coverage.PerformanceBoundsTested > coverage.PerformanceBoundsTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.performance_bounds_tested (%d) exceeds performance_bounds_total (%d)", coverage.PerformanceBoundsTested, coverage.PerformanceBoundsTotal))
	}
	if coverage.E2EJourneysScripted > coverage.E2EJourneysTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.e2e_journeys_scripted (%d) exceeds e2e_journeys_total (%d)", coverage.E2EJourneysScripted, coverage.E2EJourneysTotal))
	}

	if coverage.MutationScore != nil {
		if *coverage.MutationScore < 0 || *coverage.MutationScore > 100 {
			errs = append(errs, fmt.Errorf("tdd ledger coverage.mutation_score must be 0..100, got %.1f", *coverage.MutationScore))
		}

		if *coverage.MutationScore < float64(coverage.MutationMinScore) && !containsString(coverage.GatesBlocked, "mutation") {
			errs = append(errs, fmt.Errorf("tdd ledger coverage.mutation_score (%.1f) below mutation_min_score (%d) without mutation in gates_blocked", *coverage.MutationScore, coverage.MutationMinScore))
		}
	}

	return errs
}

func validateEvidenceCard(card EvidenceCard, line int) error {
	var errs []error

	prefix := fmt.Sprintf("evidence card line %d", line)
	require(&errs, card.CardID, prefix+" missing card_id")
	require(&errs, card.Kind, prefix+" missing kind")
	require(&errs, card.SourceHandle, prefix+" missing source_handle")
	require(&errs, card.Claim, prefix+" missing claim")
	require(&errs, card.Confidence, prefix+" missing confidence")
	require(&errs, card.Freshness, prefix+" missing freshness")
	require(&errs, card.ResearcherID, prefix+" missing researcher_id")
	require(&errs, card.Timestamp, prefix+" missing timestamp")

	if len(card.EvidenceRefs) == 0 {
		errs = append(errs, fmt.Errorf("%s missing evidence_refs", prefix))
	}

	return errors.Join(errs...)
}

func validateResearchCardsDocument(doc ResearchCardsDocument) error {
	var errs []error

	require(&errs, doc.SchemaVersion, "research cards missing schema_version")
	require(&errs, doc.ResearcherID, "research cards missing researcher_id")
	require(&errs, doc.Alignment, "research cards missing alignment")

	if len(doc.Cards) == 0 {
		errs = append(errs, errors.New("research cards missing cards"))
	}

	for i, card := range doc.Cards {
		if err := validateEvidenceCard(card, i+1); err != nil {
			errs = append(errs, err)
		}

		if strings.TrimSpace(doc.ResearcherID) != "" &&
			strings.TrimSpace(card.ResearcherID) != "" &&
			card.ResearcherID != doc.ResearcherID {

			errs = append(errs, fmt.Errorf("research card %s researcher_id %q does not match document researcher_id %q", card.CardID, card.ResearcherID, doc.ResearcherID))
		}
	}

	for i, conflict := range doc.Conflicts {
		prefix := fmt.Sprintf("research conflict %d", i)
		require(&errs, conflict.Kind, prefix+" missing kind")
		require(&errs, conflict.ClaimKey, prefix+" missing claim_key")
		require(&errs, conflict.Message, prefix+" missing message")

		if len(conflict.CardIDs) == 0 {
			errs = append(errs, fmt.Errorf("%s missing card_ids", prefix))
		}
	}

	return errors.Join(errs...)
}

func validateTraceabilityMatrix(matrix TraceabilityMatrix) error {
	var errs []error

	require(&errs, matrix.SchemaVersion, "traceability matrix missing schema_version")
	require(&errs, matrix.EffortID, "traceability matrix missing effort_id")
	require(&errs, matrix.TaskID, "traceability matrix missing task_id")
	require(&errs, matrix.CreatedAt, "traceability matrix missing created_at")

	if len(matrix.Matrix) == 0 {
		errs = append(errs, errors.New("traceability matrix missing matrix rows"))
	}

	for i, row := range matrix.Matrix {
		prefix := fmt.Sprintf("traceability row %d", i)
		require(&errs, row.EvidenceCard, prefix+" missing evidence_card")
		require(&errs, row.PRDRequirement, prefix+" missing prd_requirement")
		require(&errs, row.ADR, prefix+" missing adr")
		require(&errs, row.TDDObligation, prefix+" missing tdd_obligation")
		require(&errs, row.Brief, prefix+" missing brief")
		require(&errs, row.Report, prefix+" missing report")
	}

	return errors.Join(errs...)
}

func validateImplementationLedger(records []ImplementationLedgerRecord) error {
	var errs []error

	completed := map[string]bool{}
	verified := map[string]bool{}
	reviewed := map[string]bool{}
	investigated := map[string]bool{}

	for i, rec := range records {
		line := i + 1
		require(&errs, string(rec.RecordType), fmt.Sprintf("implementation ledger line %d missing record_type", line))
		if rec.RecordType != "" && !KnownImplementationLedgerRecordType(rec.RecordType) {
			errs = append(errs, fmt.Errorf("implementation ledger line %d: unknown record_type %q", line, rec.RecordType))
		}
		require(&errs, rec.TaskID, fmt.Sprintf("implementation ledger line %d missing task_id", line))
		require(&errs, rec.EffortID, fmt.Sprintf("implementation ledger line %d missing effort_id", line))

		switch rec.RecordType {
		case LedgerRecordAtomPlanned:
			errs = append(errs, validatePlannedAtom(line, rec)...)
		case LedgerRecordImplementationCompleted, LedgerRecordFallbackCompletion:
			completed[rec.TaskID] = true
		case LedgerRecordVerificationCompleted:
			verified[rec.TaskID] = true
		case LedgerRecordReviewCompleted:
			reviewed[rec.TaskID] = true
			if rec.ReviewerID == "" {
				errs = append(errs, fmt.Errorf("implementation ledger line %d: review_completed missing reviewer_id", line))
			}

			if rec.Model == "" {
				errs = append(errs, fmt.Errorf("implementation ledger line %d: review_completed missing model", line))
			}

			if rec.ReasoningEffort == "" {
				errs = append(errs, fmt.Errorf("implementation ledger line %d: review_completed missing reasoning_effort", line))
			}
		case LedgerRecordPostEffortInvestigationCompleted:
			investigated[rec.TaskID] = true
		case LedgerRecordAtomClosed:
			tid := rec.TaskID
			if !completed[tid] {
				errs = append(errs, fmt.Errorf("implementation ledger: atom_closed for %q without implementation_completed or fallback_completion", tid))
			}

			if !verified[tid] {
				errs = append(errs, fmt.Errorf("implementation ledger: atom_closed for %q without verification_completed", tid))
			}

			if !reviewed[tid] {
				errs = append(errs, fmt.Errorf("implementation ledger: atom_closed for %q without review_completed", tid))
			}

			if !investigated[tid] {
				errs = append(errs, fmt.Errorf("implementation ledger: atom_closed for %q without post_effort_investigation_completed", tid))
			}

			if len(rec.OpenBlockers) > 0 {
				errs = append(errs, fmt.Errorf("implementation ledger: atom_closed for %q with open blockers", tid))
			}
		case LedgerRecordFallbackException:
			require(&errs, rec.StandardImplementor, fmt.Sprintf("implementation ledger line %d: fallback_exception missing standard_implementor", line))
			require(&errs, rec.FallbackActor, fmt.Sprintf("implementation ledger line %d: fallback_exception missing fallback_actor", line))
			require(&errs, rec.Reason, fmt.Sprintf("implementation ledger line %d: fallback_exception missing reason", line))
			require(&errs, rec.AllowedScope, fmt.Sprintf("implementation ledger line %d: fallback_exception missing allowed_scope", line))
		}

		if rec.CommitHash != "" && RequiresImplementationCommitHash(rec.RecordType) {
			if !isValidCommitHash(rec.CommitHash) {
				errs = append(errs, fmt.Errorf("implementation ledger line %d: commit_hash must be 40 hex chars, got %q", line, rec.CommitHash))
			}
		}
	}

	return errors.Join(errs...)
}

func validatePlannedAtom(line int, rec ImplementationLedgerRecord) []error {
	var errs []error

	require(&errs, rec.ComplexityTier, fmt.Sprintf("implementation ledger line %d: atom_planned missing complexity_tier", line))
	require(&errs, rec.ContextPackID, fmt.Sprintf("implementation ledger line %d: atom_planned missing context_pack_id", line))

	if rec.Implementor == nil {
		errs = append(errs, fmt.Errorf("implementation ledger line %d: atom_planned missing implementor", line))
	} else {
		require(&errs, rec.Implementor.ID, fmt.Sprintf("implementation ledger line %d: atom_planned implementor missing id", line))
		require(&errs, rec.Implementor.Runtime, fmt.Sprintf("implementation ledger line %d: atom_planned implementor missing runtime", line))
		require(&errs, rec.Implementor.Model, fmt.Sprintf("implementation ledger line %d: atom_planned implementor missing model", line))
		require(&errs, rec.Implementor.Command, fmt.Sprintf("implementation ledger line %d: atom_planned implementor missing command", line))
	}

	if rec.Reviewer == nil {
		errs = append(errs, fmt.Errorf("implementation ledger line %d: atom_planned missing reviewer", line))
	} else {
		require(&errs, rec.Reviewer.ID, fmt.Sprintf("implementation ledger line %d: atom_planned reviewer missing id", line))
		require(&errs, rec.Reviewer.Model, fmt.Sprintf("implementation ledger line %d: atom_planned reviewer missing model", line))
		require(&errs, rec.Reviewer.ReasoningEffort, fmt.Sprintf("implementation ledger line %d: atom_planned reviewer missing reasoning_effort", line))
		require(&errs, rec.Reviewer.Runtime, fmt.Sprintf("implementation ledger line %d: atom_planned reviewer missing runtime", line))
	}

	if rec.Implementor != nil && rec.Reviewer != nil && rec.Implementor.ID == rec.Reviewer.ID {
		errs = append(errs, fmt.Errorf("implementation ledger line %d: implementor and reviewer have same id %q", line, rec.Implementor.ID))
	}

	if rec.Scope == nil {
		errs = append(errs, fmt.Errorf("implementation ledger line %d: atom_planned missing scope", line))
	} else {
		if len(rec.Scope.ExpectedFiles) == 0 {
			errs = append(errs, fmt.Errorf("implementation ledger line %d: atom_planned scope missing expected_files", line))
		}

		if len(rec.Scope.Forbidden) == 0 && len(rec.Scope.ForbiddenPatterns) == 0 {
			errs = append(errs, fmt.Errorf("implementation ledger line %d: atom_planned scope missing forbidden files", line))
		}
	}

	if len(rec.RequiredVerification) == 0 {
		errs = append(errs, fmt.Errorf("implementation ledger line %d: atom_planned missing required_verification", line))
	}

	if rec.ReportPaths == nil {
		errs = append(errs, fmt.Errorf("implementation ledger line %d: atom_planned missing report_paths", line))
	} else {
		require(&errs, rec.ReportPaths.Implementation, fmt.Sprintf("implementation ledger line %d: atom_planned missing implementation report path", line))
		require(&errs, rec.ReportPaths.Review, fmt.Sprintf("implementation ledger line %d: atom_planned missing review report path", line))
		require(&errs, rec.ReportPaths.Investigation, fmt.Sprintf("implementation ledger line %d: atom_planned missing investigation report path", line))
	}

	return errs
}

func validatePrivacyFlags(context string, privacy PrivacyState) error {
	var errs []error

	if privacy.RawPromptsStored != nil && *privacy.RawPromptsStored {
		errs = append(errs, fmt.Errorf("%s: raw_prompts_stored must not be true", context))
	}

	if privacy.RawTranscriptsStored != nil && *privacy.RawTranscriptsStored {
		errs = append(errs, fmt.Errorf("%s: raw_transcripts_stored must not be true", context))
	}

	if privacy.FileBodiesStored != nil && *privacy.FileBodiesStored {
		errs = append(errs, fmt.Errorf("%s: file_bodies_stored must not be true", context))
	}

	if privacy.SecretsStored != nil && *privacy.SecretsStored {
		errs = append(errs, fmt.Errorf("%s: secrets_stored must not be true", context))
	}

	return errors.Join(errs...)
}

func validateResearchProfile(profile ResearchProfile) error {
	var errs []error

	require(&errs, profile.SchemaVersion, "research profile missing schema_version")

	if strings.TrimSpace(profile.Name) == "" && strings.TrimSpace(profile.ProfileID) == "" {
		errs = append(errs, errors.New("research profile missing name or profile_id"))
	}

	if len(profile.Researchers) == 0 {
		errs = append(errs, errors.New("research profile missing researchers"))
	}

	for i, r := range profile.Researchers {
		prefix := fmt.Sprintf("research profile researcher %d", i)
		require(&errs, r.ID, prefix+" missing id")
		require(&errs, r.Alignment, prefix+" missing alignment")
		require(&errs, r.Question, prefix+" missing question")

		if len(r.AllowedCapabilities) == 0 {
			errs = append(errs, fmt.Errorf("%s missing allowed_capabilities", prefix))
		}

		require(&errs, r.Output, prefix+" missing output")
	}

	require(&errs, profile.MergePolicy.Conflicts, "research profile missing merge_policy.conflicts")
	require(&errs, profile.MergePolicy.UnsupportedClaims, "research profile missing merge_policy.unsupported_claims")
	require(&errs, profile.MergePolicy.StaleLocalContext, "research profile missing merge_policy.stale_local_context")

	return errors.Join(errs...)
}

func validatePRD(doc PRDDocument) error {
	var errs []error

	require(&errs, doc.FrontMatter.SchemaVersion, "PRD front matter missing schema_version")
	require(&errs, doc.FrontMatter.PRDID, "PRD front matter missing prd_id")
	require(&errs, doc.FrontMatter.Status, "PRD front matter missing status")
	require(&errs, doc.FrontMatter.TaskID, "PRD front matter missing task_id")
	require(&errs, doc.FrontMatter.CreatedAt, "PRD front matter missing created_at")

	if doc.FrontMatter.Status != "" {
		if err := ValidateArtifactState(doc.FrontMatter.Status); err != nil {
			errs = append(errs, fmt.Errorf("PRD front matter: %w", err))
		}
	}

	if err := validateAcceptanceMetadata(
		"PRD front matter",
		doc.FrontMatter.Status,
		doc.FrontMatter.AcceptedAt,
		doc.FrontMatter.AcceptedBy,
		doc.FrontMatter.AcceptanceProfile,
	); err != nil {
		errs = append(errs, err)
	}

	if len(doc.Requirements) == 0 {
		errs = append(errs, errors.New("PRD has no requirements"))
	}

	for i, req := range doc.Requirements {
		prefix := fmt.Sprintf("PRD requirement %d (%s)", i, req.ID)
		require(&errs, req.ID, prefix+" missing id")
		require(&errs, req.Priority, prefix+" missing priority")
		require(&errs, req.Statement, prefix+" missing statement")
		require(&errs, req.OwnerAlignment, prefix+" missing owner_alignment")
		require(&errs, req.Testability, prefix+" missing testability")
		require(&errs, req.Metric, prefix+" missing metric")

		if len(req.EvidenceRefs) == 0 {
			errs = append(errs, fmt.Errorf("%s missing evidence_refs", prefix))
		}

		if len(req.TDDObligations) == 0 {
			errs = append(errs, fmt.Errorf("%s missing tdd_obligations", prefix))
		}

		if !req.OpenQuestionsPresent {
			errs = append(errs, fmt.Errorf("%s missing open_questions", prefix))
		}
	}

	return errors.Join(errs...)
}

func validateADRIndex(idx ADRIndex) error {
	var errs []error

	require(&errs, idx.SchemaVersion, "ADR index missing schema_version")
	require(&errs, idx.EffortID, "ADR index missing effort_id")

	if len(idx.ADRs) == 0 {
		errs = append(errs, errors.New("ADR index missing adrs"))
	}

	for i, entry := range idx.ADRs {
		prefix := fmt.Sprintf("ADR index entry %d", i)
		require(&errs, entry.ADRID, prefix+" missing adr_id")
		require(&errs, entry.Status, prefix+" missing status")

		if entry.Status != "" {
			if err := ValidateArtifactState(entry.Status); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", prefix, err))
			}
		}

		require(&errs, entry.Path, prefix+" missing path")

		if len(entry.PRDRefs) == 0 {
			errs = append(errs, fmt.Errorf("%s missing prd_refs", prefix))
		}

		if entry.CoverageTier != "" && !allowedCoverageTiers[entry.CoverageTier] {
			errs = append(errs, fmt.Errorf("%s invalid coverage_tier %q", prefix, entry.CoverageTier))
		}
	}

	for _, linkErr := range ValidateADRIndexSupersessionLinks(idx) {
		errs = append(errs, errors.New(linkErr.Message))
	}

	return errors.Join(errs...)
}

func validateADR(doc ADRDocument) error {
	var errs []error

	require(&errs, doc.FrontMatter.SchemaVersion, "ADR front matter missing schema_version")
	require(&errs, doc.FrontMatter.ADRID, "ADR front matter missing adr_id")
	require(&errs, doc.FrontMatter.Status, "ADR front matter missing status")

	if doc.FrontMatter.Status != "" {
		if err := ValidateArtifactState(doc.FrontMatter.Status); err != nil {
			errs = append(errs, fmt.Errorf("ADR front matter: %w", err))
		}
	}

	if err := validateAcceptanceMetadata(
		"ADR front matter",
		doc.FrontMatter.Status,
		doc.FrontMatter.AcceptedAt,
		doc.FrontMatter.AcceptedBy,
		doc.FrontMatter.AcceptanceProfile,
	); err != nil {
		errs = append(errs, err)
	}

	if len(doc.FrontMatter.PRDRefs) == 0 {
		errs = append(errs, errors.New("ADR front matter missing prd_refs"))
	}

	if len(doc.FrontMatter.EvidenceRefs) == 0 {
		errs = append(errs, errors.New("ADR front matter missing evidence_refs"))
	}

	if len(doc.FrontMatter.TDDObligations) == 0 {
		errs = append(errs, errors.New("ADR front matter missing tdd_obligations"))
	}

	require(&errs, doc.FrontMatter.CoverageTier, "ADR front matter missing coverage_tier")

	if doc.FrontMatter.CoverageTier != "" && !allowedCoverageTiers[doc.FrontMatter.CoverageTier] {
		errs = append(errs, fmt.Errorf("ADR front matter invalid coverage_tier %q", doc.FrontMatter.CoverageTier))
	}

	if !doc.Sections.HasContext {
		errs = append(errs, errors.New("ADR body missing ## Context section"))
	}

	if !doc.Sections.HasDecision {
		errs = append(errs, errors.New("ADR body missing ## Decision section"))
	}

	if !doc.Sections.HasAlternativesConsidered {
		errs = append(errs, errors.New("ADR body missing ## Alternatives Considered (or Alternatives) section"))
	}

	if !doc.Sections.HasEvidence {
		errs = append(errs, errors.New("ADR body missing ## Evidence section"))
	}

	if !doc.Sections.HasConsequences {
		errs = append(errs, errors.New("ADR body missing ## Consequences section"))
	}

	if !doc.Sections.HasCompatibilityAndMigration {
		errs = append(errs, errors.New("ADR body missing ## Compatibility and Migration (or Compatibility) section"))
	}

	if !doc.Sections.HasTDDObligations {
		errs = append(errs, errors.New("ADR body missing ## TDD Obligations section"))
	}

	if !doc.Sections.HasRollback {
		errs = append(errs, errors.New("ADR body missing ## Rollback section"))
	}

	if !doc.Sections.HasOpenQuestions {
		errs = append(errs, errors.New("ADR body missing ## Open Questions section"))
	}

	return errors.Join(errs...)
}

func validateAcceptanceMetadata(context, status, acceptedAt, acceptedBy, acceptanceProfile string) error {
	if status != string(ArtifactStateAccepted) {
		return nil
	}

	var errs []error

	require(&errs, acceptedAt, context+" missing accepted_at for accepted artifact")
	require(&errs, acceptedBy, context+" missing accepted_by for accepted artifact")
	require(&errs, acceptanceProfile, context+" missing acceptance_profile for accepted artifact")

	return errors.Join(errs...)
}

func validateImplementationReport(r ImplementationReport) error {
	var errs []error

	require(&errs, r.SchemaVersion, "implementation report missing schema_version")
	require(&errs, r.TaskID, "implementation report missing task_id")
	require(&errs, r.EffortID, "implementation report missing effort_id")
	require(&errs, r.ImplementorID, "implementation report missing implementor_id")
	require(&errs, r.Runtime, "implementation report missing runtime")
	require(&errs, r.Model, "implementation report missing model")
	require(&errs, r.ComplexityTier, "implementation report missing complexity_tier")

	if r.ComplexityTier != "" && !allowedComplexityTiers[r.ComplexityTier] {
		errs = append(errs, fmt.Errorf("implementation report invalid complexity_tier %q", r.ComplexityTier))
	}

	require(&errs, r.ScopeStatement, "implementation report missing scope_statement")
	require(&errs, r.ReviewerID, "implementation report missing reviewer_id")
	require(&errs, r.Verification.Status, "implementation report missing verification.status")
	require(&errs, r.PrivacyNote, "implementation report missing privacy_note")

	if r.OpenBlockers == nil {
		errs = append(errs, errors.New("implementation report missing open_blockers (must be present, may be empty)"))
	}

	if r.Verification.Status != "" && !ImplementationVerificationStatusAllowed(r.Verification.Status) {
		errs = append(errs, fmt.Errorf("implementation report invalid verification.status %q", r.Verification.Status))
	}

	if r.Verification.Status != "" && r.Verification.Status != string(ProductWorkflowStatusPending) {
		if len(r.Verification.Commands) == 0 {
			errs = append(errs, fmt.Errorf("implementation report verification.status %q requires verification.commands", r.Verification.Status))
		}
	}

	if r.CodeChanges && len(r.ChangedPaths) == 0 {
		errs = append(errs, errors.New("implementation report code_changes is true but changed_paths is empty"))
	}

	if err := validateReportPrivacyNote(r.PrivacyNote); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func ImplementationVerificationStatusAllowed(status string) bool {
	switch strings.TrimSpace(status) {
	case string(ProductWorkflowStatusPending),
		"pending_reviewer",
		string(ProductWorkflowStatusPass),
		string(ProductWorkflowStatusPassed),
		"passed_after_codex_review",
		"passed_after_orchestrator_review",
		"passed_full_suite_and_product_gates",
		"passed_full_suite_and_product_gates_after_final_review_repair",
		string(ProductWorkflowStatusFail),
		string(ProductWorkflowStatusFailed),
		string(ProductWorkflowStatusBlocked),
		string(ProductWorkflowStatusComplete):
		return true
	default:
		return false
	}
}

func validateReviewReport(r ReviewReport) error {
	var errs []error

	require(&errs, r.SchemaVersion, "review report missing schema_version")
	require(&errs, r.EffortID, "review report missing effort_id")
	require(&errs, r.TaskID, "review report missing task_id")
	require(&errs, r.Reviewer.AgentID, "review report missing reviewer.agent_id")
	require(&errs, r.Reviewer.Model, "review report missing reviewer.model")
	require(&errs, r.Reviewer.ReasoningEffort, "review report missing reviewer.reasoning_effort")
	require(&errs, r.Status, "review report missing status")

	if r.Status != "" && !ReviewReportStatusAllowed(r.Status) {
		errs = append(errs, fmt.Errorf("review report invalid status %q", r.Status))
	}

	if r.Status != "" && r.Status != string(ProductWorkflowStatusPending) {
		require(&errs, r.ReviewedAt, "review report missing reviewed_at (required when status is not pending)")

		if len(r.CommandsReviewed) == 0 {
			errs = append(errs, errors.New("review report missing commands_reviewed (required when status is not pending)"))
		}

		if len(r.Checks) == 0 {
			errs = append(errs, errors.New("review report missing checks (required when status is not pending)"))
		}
	}

	for name, status := range r.Checks {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, errors.New("review report checks contains empty check name"))
			continue
		}
		if !KnownReviewCheckStatus(status) {
			errs = append(errs, fmt.Errorf("review report check %q has unsupported status %q", name, status))
		}
	}

	if !r.Reviewer.IndependentFromImplementor {
		errs = append(errs, errors.New("review report reviewer.independent_from_implementor must be true"))
	}

	return errors.Join(errs...)
}

func ReviewReportStatusAllowed(status string) bool {
	switch strings.TrimSpace(status) {
	case string(ProductWorkflowStatusPending),
		string(ProductWorkflowStatusPass),
		string(ProductWorkflowStatusPassed),
		string(ProductWorkflowStatusPassAfterRepair),
		string(ProductWorkflowStatusFail),
		string(ProductWorkflowStatusFailed),
		string(ProductWorkflowStatusBlocked),
		string(ProductWorkflowStatusComplete):
		return true
	default:
		return false
	}
}

func validateInvestigationReport(r InvestigationReport) error {
	var errs []error

	require(&errs, r.SchemaVersion, "investigation report missing schema_version")
	require(&errs, r.EffortID, "investigation report missing effort_id")
	require(&errs, r.TaskID, "investigation report missing task_id")
	require(&errs, r.Status, "investigation report missing status")

	if r.Status != "" && !InvestigationReportStatusAllowed(r.Status) {
		errs = append(errs, fmt.Errorf("investigation report invalid status %q", r.Status))
	}

	if r.Status == string(ProductWorkflowStatusComplete) {
		require(&errs, r.CompletedAt, "investigation report missing completed_at (required when status is complete)")
		require(&errs, r.NextValidAction, "investigation report missing next_valid_action (required when status is complete)")

		if !r.Scope.HasContent() {
			errs = append(errs, errors.New("investigation report missing scope (required when status is complete)"))
		}

		if !r.Tests.HasContent() {
			errs = append(errs, errors.New("investigation report missing tests (required when status is complete)"))
		}

		if !r.Coverage.HasContent() {
			errs = append(errs, errors.New("investigation report missing coverage (required when status is complete)"))
		}

		if !r.Traceability.HasContent() {
			errs = append(errs, errors.New("investigation report missing traceability (required when status is complete)"))
		}

		if !r.PrivacyAudit.HasContent() {
			errs = append(errs, errors.New("investigation report missing privacy_audit (required when status is complete)"))
		}

		if len(r.Blockers) > 0 {
			errs = append(errs, errors.New("investigation report status is complete but blockers is non-empty"))
		}
	}

	if r.PrivacyAudit.HasContent() {
		if err := validatePrivacyAudit(r.PrivacyAudit); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func InvestigationReportStatusAllowed(status string) bool {
	switch strings.TrimSpace(status) {
	case string(ProductWorkflowStatusPlanned),
		string(ProductWorkflowStatusPending),
		string(ProductWorkflowStatusComplete),
		string(ProductWorkflowStatusBlocked),
		string(ProductWorkflowStatusPass),
		string(ProductWorkflowStatusPassed),
		string(ProductWorkflowStatusFail),
		string(ProductWorkflowStatusFailed):
		return true
	default:
		return false
	}
}

func validatePrivacyAudit(audit InvestigationPrivacyAudit) error {
	var errs []error

	for _, claim := range investigationPrivacyStoredClaims(audit) {
		if claim.value != nil && *claim.value {
			errs = append(errs, fmt.Errorf("investigation report privacy_audit: %s must not be true", claim.key))
		}
	}

	for _, claim := range investigationPrivacyNoClaims(audit) {
		if claim.value != nil && !*claim.value {
			errs = append(errs, fmt.Errorf("investigation report privacy_audit: %s must not be false", claim.key))
		}
	}

	return errors.Join(errs...)
}

type investigationPrivacyBoolClaim struct {
	key   string
	value *bool
}

func investigationPrivacyStoredClaims(audit InvestigationPrivacyAudit) []investigationPrivacyBoolClaim {
	return []investigationPrivacyBoolClaim{
		{key: "raw_prompts_stored", value: audit.RawPromptsStored},
		{key: "raw_transcripts_stored", value: audit.RawTranscriptsStored},
		{key: "transcripts_stored", value: audit.TranscriptsStored},
		{key: "file_bodies_stored", value: audit.FileBodiesStored},
		{key: "secrets_stored", value: audit.SecretsStored},
		{key: "customer_identifying_data_stored", value: audit.CustomerIdentifyingDataStored},
		{key: "tui_transcript_bodies_stored", value: audit.TUITranscriptBodiesStored},
		{key: "generated_brief_bodies_stored_in_durable_reports", value: audit.GeneratedBriefBodiesStoredDurableReports},
		{key: "ranker_weight_payloads_stored", value: audit.RankerWeightPayloadsStored},
	}
}

func investigationPrivacyNoClaims(audit InvestigationPrivacyAudit) []investigationPrivacyBoolClaim {
	return []investigationPrivacyBoolClaim{
		{key: "no_raw_prompts", value: audit.NoRawPrompts},
		{key: "no_raw_transcripts", value: audit.NoRawTranscripts},
		{key: "no_file_bodies", value: audit.NoFileBodies},
		{key: "no_secrets", value: audit.NoSecrets},
		{key: "no_customer_data", value: audit.NoCustomerData},
	}
}

var privacyForbiddenClaims = []string{
	"raw_prompts_stored",
	"raw_transcripts_stored",
	"transcripts_stored",
	"file_bodies_stored",
	"secrets_stored",
	"customer_identifying_data_stored",
	"tui_transcript_bodies_stored",
	"generated_brief_bodies_stored_in_durable_reports",
	"ranker_weight_payloads_stored",
}

func validateReportPrivacyNote(note string) error {
	if note == "" {
		return nil
	}

	lower := strings.ToLower(note)
	for _, claim := range privacyForbiddenClaims {
		if privacyForbiddenTrueClaimPattern(claim).MatchString(lower) {
			return fmt.Errorf("implementation report privacy_note claims %s was stored", claim)
		}
	}

	return nil
}

func privacyForbiddenTrueClaimPattern(claim string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:^|[^a-z0-9_])"?` + regexp.QuoteMeta(claim) + `"?\s*:\s*true(?:[^a-z0-9_]|$)`)
}
