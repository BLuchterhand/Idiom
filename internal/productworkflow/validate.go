package productworkflow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func ParseEffortIndexJSON(data []byte) (EffortIndex, error) {
	var idx EffortIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return EffortIndex{}, fmt.Errorf("parse effort index: %w", err)
	}
	if err := validateEffortIndex(idx); err != nil {
		return EffortIndex{}, err
	}
	return idx, nil
}

func ParseWorkflowStateJSON(data []byte) (WorkflowState, error) {
	var ws WorkflowState
	if err := json.Unmarshal(data, &ws); err != nil {
		return WorkflowState{}, fmt.Errorf("parse workflow state: %w", err)
	}
	if err := validateWorkflowState(ws); err != nil {
		return WorkflowState{}, err
	}
	return ws, nil
}

func ParseTDDLedgerJSON(data []byte) (TDDLedger, error) {
	var ledger TDDLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return TDDLedger{}, fmt.Errorf("parse tdd ledger: %w", err)
	}
	if err := validateTDDLedger(ledger); err != nil {
		return TDDLedger{}, err
	}
	return ledger, nil
}

func ParseEvidenceCardsJSONL(data []byte) ([]EvidenceCard, error) {
	var cards []EvidenceCard
	if err := scanJSONL(data, "evidence card", func(line int, text []byte) error {
		var card EvidenceCard
		if err := json.Unmarshal(text, &card); err != nil {
			return fmt.Errorf("parse evidence card line %d: %w", line, err)
		}
		if err := validateEvidenceCard(card, line); err != nil {
			return err
		}
		cards = append(cards, card)
		return nil
	}); err != nil {
		return nil, err
	}
	return cards, nil
}

func ParseResearchCardsJSON(data []byte) (ResearchCardsDocument, error) {
	var doc ResearchCardsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return ResearchCardsDocument{}, fmt.Errorf("parse research cards: %w", err)
	}
	if err := validateResearchCardsDocument(doc); err != nil {
		return ResearchCardsDocument{}, err
	}
	return doc, nil
}

func ParseTraceabilityMatrixJSON(data []byte) (TraceabilityMatrix, error) {
	var matrix TraceabilityMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		return TraceabilityMatrix{}, fmt.Errorf("parse traceability matrix: %w", err)
	}
	if err := validateTraceabilityMatrix(matrix); err != nil {
		return TraceabilityMatrix{}, err
	}
	return matrix, nil
}

func ParseImplementationLedgerJSONL(data []byte) ([]ImplementationLedgerRecord, error) {
	var records []ImplementationLedgerRecord
	if err := scanJSONL(data, "implementation ledger record", func(line int, text []byte) error {
		var rec ImplementationLedgerRecord
		if err := json.Unmarshal(text, &rec); err != nil {
			return fmt.Errorf("parse implementation ledger line %d: %w", line, err)
		}
		records = append(records, rec)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := validateImplementationLedger(records); err != nil {
		return nil, err
	}
	return records, nil
}

func scanJSONL(data []byte, label string, visit func(line int, text []byte) error) error {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	line := 0
	records := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		records++
		if err := visit(line, []byte(text)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s JSONL: %w", label, err)
	}
	if records == 0 {
		return fmt.Errorf("%s JSONL contains no records", label)
	}
	return nil
}

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
	if len(ws.NextValidActions) == 0 {
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
	"manual":              true,
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

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
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
		require(&errs, rec.RecordType, fmt.Sprintf("implementation ledger line %d missing record_type", line))
		require(&errs, rec.TaskID, fmt.Sprintf("implementation ledger line %d missing task_id", line))
		require(&errs, rec.EffortID, fmt.Sprintf("implementation ledger line %d missing effort_id", line))
		switch rec.RecordType {
		case "atom_planned":
			errs = append(errs, validatePlannedAtom(line, rec)...)
		case "implementation_completed", "fallback_completion":
			completed[rec.TaskID] = true
		case "verification_completed":
			verified[rec.TaskID] = true
		case "review_completed":
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
		case "post_effort_investigation_completed":
			investigated[rec.TaskID] = true
		case "atom_closed":
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
		case "fallback_exception":
			require(&errs, rec.StandardImplementor, fmt.Sprintf("implementation ledger line %d: fallback_exception missing standard_implementor", line))
			require(&errs, rec.FallbackActor, fmt.Sprintf("implementation ledger line %d: fallback_exception missing fallback_actor", line))
			require(&errs, rec.Reason, fmt.Sprintf("implementation ledger line %d: fallback_exception missing reason", line))
			require(&errs, rec.AllowedScope, fmt.Sprintf("implementation ledger line %d: fallback_exception missing allowed_scope", line))
		}
		if rec.CommitHash != "" && (rec.RecordType == "implementation_completed" || rec.RecordType == "fallback_completion" || rec.RecordType == "partial_implementation_recorded") {
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

func ParsePRDMarkdown(data []byte) (PRDDocument, error) {
	fields, body, err := parseFrontMatterRaw(data)
	if err != nil {
		return PRDDocument{}, fmt.Errorf("parse PRD: %w", err)
	}
	fm := PRDFrontMatter{
		SchemaVersion:     fields["schema_version"],
		PRDID:             fields["prd_id"],
		Status:            fields["status"],
		TaskID:            fields["task_id"],
		CreatedAt:         fields["created_at"],
		AcceptedAt:        fields["accepted_at"],
		AcceptedBy:        fields["accepted_by"],
		AcceptanceProfile: fields["acceptance_profile"],
	}
	doc := PRDDocument{FrontMatter: fm}
	doc.Requirements = parsePRDRequirements(body)
	if err := validatePRD(doc); err != nil {
		return PRDDocument{}, err
	}
	return doc, nil
}

func ParseADRIndexJSON(data []byte) (ADRIndex, error) {
	var idx ADRIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return ADRIndex{}, fmt.Errorf("parse ADR index: %w", err)
	}
	if err := validateADRIndex(idx); err != nil {
		return ADRIndex{}, err
	}
	return idx, nil
}

func ParseResearchProfileJSON(data []byte) (ResearchProfile, error) {
	var profile ResearchProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return ResearchProfile{}, fmt.Errorf("parse research profile: %w", err)
	}
	if err := validateResearchProfile(profile); err != nil {
		return ResearchProfile{}, err
	}
	return profile, nil
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

func ParseADRMarkdown(data []byte) (ADRDocument, error) {
	fields, body, err := parseFrontMatterRaw(data)
	if err != nil {
		return ADRDocument{}, fmt.Errorf("parse ADR: %w", err)
	}
	fm := ADRFrontMatter{
		SchemaVersion:     fields["schema_version"],
		ADRID:             fields["adr_id"],
		Status:            fields["status"],
		PRDRefs:           parseFrontMatterList(fields, "prd_refs"),
		EvidenceRefs:      parseFrontMatterList(fields, "evidence_refs"),
		TDDObligations:    parseFrontMatterList(fields, "tdd_obligations"),
		CoverageTier:      fields["coverage_tier"],
		AcceptedAt:        fields["accepted_at"],
		AcceptedBy:        fields["accepted_by"],
		AcceptanceProfile: fields["acceptance_profile"],
		Supersedes:        parseFrontMatterList(fields, "supersedes"),
		SupersededBy:      parseFrontMatterList(fields, "superseded_by"),
	}
	sections := ADRSections{
		HasContext:                   strings.Contains(body, "## Context"),
		HasDecision:                  strings.Contains(body, "## Decision"),
		HasAlternativesConsidered:    strings.Contains(body, "## Alternatives Considered") || strings.Contains(body, "## Alternatives"),
		HasEvidence:                  strings.Contains(body, "## Evidence"),
		HasConsequences:              strings.Contains(body, "## Consequences"),
		HasCompatibilityAndMigration: strings.Contains(body, "## Compatibility and Migration") || strings.Contains(body, "## Compatibility"),
		HasTDDObligations:            strings.Contains(body, "## TDD Obligations"),
		HasRollback:                  strings.Contains(body, "## Rollback"),
		HasOpenQuestions:             strings.Contains(body, "## Open Questions"),
	}
	doc := ADRDocument{FrontMatter: fm, Sections: sections}
	if err := validateADR(doc); err != nil {
		return ADRDocument{}, err
	}
	return doc, nil
}

func parseFrontMatterRaw(data []byte) (map[string]string, string, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return nil, "", errors.New("front matter delimiter --- not found at start")
	}
	end := strings.Index(text[3:], "---")
	if end < 0 {
		return nil, "", errors.New("closing front matter delimiter --- not found")
	}
	fmText := text[3 : end+3]
	body := text[end+6:]
	return parseYAMLFieldsMultiline(fmText), body, nil
}

func parseYAMLFieldsMultiline(text string) map[string]string {
	result := map[string]string{}
	lines := strings.Split(text, "\n")
	var currentKey string
	var listItems []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && currentKey != "" {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			listItems = append(listItems, item)
			continue
		}
		if currentKey != "" && len(listItems) > 0 {
			result[currentKey] = "[" + strings.Join(listItems, ", ") + "]"
		}
		currentKey = ""
		listItems = nil
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		if val == "" {
			currentKey = key
			continue
		}
		result[key] = val
	}
	if currentKey != "" && len(listItems) > 0 {
		result[currentKey] = "[" + strings.Join(listItems, ", ") + "]"
	}
	return result
}

func parseFrontMatterList(fields map[string]string, key string) []string {
	val := fields[key]
	if val == "" {
		return nil
	}
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func parsePRDRequirements(body string) []PRDRequirement {
	var reqs []PRDRequirement
	lines := strings.Split(body, "\n")
	var current *PRDRequirement
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **id**:") {
			if current != nil {
				reqs = append(reqs, *current)
			}
			current = &PRDRequirement{}
			current.ID = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **id**:"))
		} else if current != nil {
			if strings.HasPrefix(trimmed, "- **priority**:") {
				current.Priority = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **priority**:"))
			} else if strings.HasPrefix(trimmed, "- **statement**:") {
				current.Statement = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **statement**:"))
			} else if strings.HasPrefix(trimmed, "- **evidence_refs**:") {
				current.EvidenceRefs = parseInlineArray(trimmed, "- **evidence_refs**:")
			} else if strings.HasPrefix(trimmed, "- **tdd_obligations**:") {
				current.TDDObligations = parseInlineArray(trimmed, "- **tdd_obligations**:")
			} else if strings.HasPrefix(trimmed, "- **owner_alignment**:") {
				current.OwnerAlignment = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **owner_alignment**:"))
			} else if strings.HasPrefix(trimmed, "- **testability**:") {
				current.Testability = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **testability**:"))
			} else if strings.HasPrefix(trimmed, "- **metric**:") {
				current.Metric = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **metric**:"))
			} else if strings.HasPrefix(trimmed, "- **open_questions**:") {
				current.OpenQuestions = parseInlineArray(trimmed, "- **open_questions**:")
				current.OpenQuestionsPresent = true
			}
		}
	}
	if current != nil {
		reqs = append(reqs, *current)
	}
	return reqs
}

func parseInlineArray(line, prefix string) []string {
	val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
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

type ADRCrossLinkError struct {
	ADRPath string
	Field   string
	Value   string
	Message string
}

func ValidateADRCrossLinks(
	adrs []ParsedADR,
	prdRequirementIDs map[string]bool,
	evidenceCardIDs map[string]bool,
	tddObligationIDs map[string]bool,
) []ADRCrossLinkError {
	var errs []ADRCrossLinkError
	for _, a := range adrs {
		adrRef := adrCrossLinkReference(a)
		for _, ref := range a.Doc.FrontMatter.PRDRefs {
			if prdRequirementIDs != nil && !prdRequirementIDs[ref] {
				errs = append(errs, ADRCrossLinkError{
					ADRPath: a.RelPath,
					Field:   "prd_refs",
					Value:   ref,
					Message: fmt.Sprintf("ADR %s: prd_ref %q not found in parsed PRD requirement IDs", adrRef, ref),
				})
			}
		}
		for _, ref := range a.Doc.FrontMatter.EvidenceRefs {
			if evidenceCardIDs != nil && !evidenceCardIDs[ref] {
				errs = append(errs, ADRCrossLinkError{
					ADRPath: a.RelPath,
					Field:   "evidence_refs",
					Value:   ref,
					Message: fmt.Sprintf("ADR %s: evidence_ref %q not found in parsed evidence card IDs", adrRef, ref),
				})
			}
		}
		for _, ref := range a.Doc.FrontMatter.TDDObligations {
			if tddObligationIDs != nil && !tddObligationIDs[ref] {
				errs = append(errs, ADRCrossLinkError{
					ADRPath: a.RelPath,
					Field:   "tdd_obligations",
					Value:   ref,
					Message: fmt.Sprintf("ADR %s: tdd_obligation %q not found in parsed TDD obligation IDs", adrRef, ref),
				})
			}
		}
	}
	return errs
}

func adrCrossLinkReference(adr ParsedADR) string {
	if ref := strings.TrimSpace(adr.RelPath); ref != "" {
		return ref
	}
	if ref := strings.TrimSpace(adr.Doc.FrontMatter.ADRID); ref != "" {
		return ref
	}
	return "(unknown)"
}

type PRDTDDObligationLinkError struct {
	RequirementID string
	TDDObligation string
	Message       string
}

func ValidatePRDTDDObligationCrossLinks(
	requirements []PRDRequirement,
	tddObligationIDs map[string]bool,
) []PRDTDDObligationLinkError {
	var errs []PRDTDDObligationLinkError
	for _, req := range requirements {
		if strings.ToLower(req.Priority) != "must" {
			continue
		}
		for _, tddID := range req.TDDObligations {
			if tddObligationIDs != nil && !tddObligationIDs[tddID] {
				errs = append(errs, PRDTDDObligationLinkError{
					RequirementID: req.ID,
					TDDObligation: tddID,
					Message: fmt.Sprintf(
						"PRD must-priority requirement %s references TDD obligation %q not found in current or historical TDD obligation IDs",
						req.ID, tddID,
					),
				})
			}
		}
	}
	return errs
}

type ParsedADR struct {
	Doc         ADRDocument
	RelPath     string
	RawMarkdown string
}

type ADRSupersessionLinkError struct {
	Code    string
	ADRPath string
	ADRID   string
	Field   string
	Ref     string
	Message string
}

func ValidateADRSupersessionLinks(adrs []ParsedADR) []ADRSupersessionLinkError {
	var errs []ADRSupersessionLinkError
	byID := make(map[string]ParsedADR, len(adrs))
	for _, a := range adrs {
		byID[a.Doc.FrontMatter.ADRID] = a
	}
	for _, a := range adrs {
		id := a.Doc.FrontMatter.ADRID
		checkList := func(field string, refs []string) {
			seen := map[string]bool{}
			for _, ref := range refs {
				if seen[ref] {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_duplicate_ref",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s has duplicate %s reference %q", id, field, ref),
					})
					continue
				}
				seen[ref] = true
				if ref == id {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_self_ref",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s has self-reference in %s", id, field),
					})
					continue
				}
				peer, ok := byID[ref]
				if !ok {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_broken_ref",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s references missing ADR %q in %s", id, ref, field),
					})
					continue
				}
				if field == "supersedes" && !containsString(peer.Doc.FrontMatter.SupersededBy, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s supersedes %s but %s does not list %s in superseded_by", id, ref, ref, id),
					})
				}
				if field == "superseded_by" && !containsString(peer.Doc.FrontMatter.Supersedes, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s lists superseded_by %s but %s does not list %s in supersedes", id, ref, ref, id),
					})
				}
			}
		}
		checkList("supersedes", a.Doc.FrontMatter.Supersedes)
		checkList("superseded_by", a.Doc.FrontMatter.SupersededBy)
	}
	return errs
}

func ValidateADRIndexSupersessionLinks(idx ADRIndex) []ADRSupersessionLinkError {
	var errs []ADRSupersessionLinkError
	byID := make(map[string]ADRIndexEntry, len(idx.ADRs))
	for _, entry := range idx.ADRs {
		byID[entry.ADRID] = entry
	}
	for _, entry := range idx.ADRs {
		id := entry.ADRID
		checkList := func(field string, refs []string) {
			seen := map[string]bool{}
			for _, ref := range refs {
				if seen[ref] {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_duplicate_ref",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s has duplicate %s reference %q", id, field, ref),
					})
					continue
				}
				seen[ref] = true
				if ref == id {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_self_ref",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s has self-reference in %s", id, field),
					})
					continue
				}
				peer, ok := byID[ref]
				if !ok {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_broken_ref",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s references missing ADR %q in %s", id, ref, field),
					})
					continue
				}
				if field == "supersedes" && !containsString(peer.SupersededBy, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s supersedes %s but %s does not list %s in superseded_by", id, ref, ref, id),
					})
				}
				if field == "superseded_by" && !containsString(peer.Supersedes, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s lists superseded_by %s but %s does not list %s in supersedes", id, ref, ref, id),
					})
				}
			}
		}
		checkList("supersedes", entry.Supersedes)
		checkList("superseded_by", entry.SupersededBy)
	}
	return errs
}

type ADRIndexFrontMatterConsistencyError struct {
	Code    string
	Path    string
	ADRID   string
	Field   string
	Message string
}

func ValidateADRIndexFrontMatterConsistency(idx ADRIndex, adrs []ParsedADR) []ADRIndexFrontMatterConsistencyError {
	var errs []ADRIndexFrontMatterConsistencyError
	byPath := make(map[string]ParsedADR, len(adrs))
	for _, adr := range adrs {
		relPath := filepath.ToSlash(strings.TrimSpace(adr.RelPath))
		if relPath != "" {
			byPath[relPath] = adr
		}
	}
	for _, entry := range idx.ADRs {
		entryPath := filepath.ToSlash(filepath.Join("adrs", strings.TrimSpace(entry.Path)))
		adr, ok := byPath[entryPath]
		if !ok {
			continue
		}
		appendMismatch := func(field, indexValue, markdownValue string) {
			errs = append(errs, ADRIndexFrontMatterConsistencyError{
				Code:    "adr_index_frontmatter_mismatch",
				Path:    "adrs/index.json",
				ADRID:   entry.ADRID,
				Field:   field,
				Message: fmt.Sprintf("ADR index entry %s field %s=%q diverges from %s front matter value %q", entry.ADRID, field, indexValue, entryPath, markdownValue),
			})
		}
		compareString := func(field, indexValue, markdownValue string) {
			indexValue = strings.TrimSpace(indexValue)
			markdownValue = strings.TrimSpace(markdownValue)
			if indexValue != markdownValue {
				appendMismatch(field, indexValue, markdownValue)
			}
		}
		compareList := func(field string, indexValues, markdownValues []string) {
			indexCanonical := canonicalStringSet(indexValues)
			markdownCanonical := canonicalStringSet(markdownValues)
			if strings.Join(indexCanonical, "\x00") != strings.Join(markdownCanonical, "\x00") {
				appendMismatch(field, strings.Join(indexCanonical, ","), strings.Join(markdownCanonical, ","))
			}
		}
		compareString("adr_id", entry.ADRID, adr.Doc.FrontMatter.ADRID)
		compareString("status", entry.Status, adr.Doc.FrontMatter.Status)
		compareString("coverage_tier", entry.CoverageTier, adr.Doc.FrontMatter.CoverageTier)
		compareList("prd_refs", entry.PRDRefs, adr.Doc.FrontMatter.PRDRefs)
		compareList("supersedes", entry.Supersedes, adr.Doc.FrontMatter.Supersedes)
		compareList("superseded_by", entry.SupersededBy, adr.Doc.FrontMatter.SupersededBy)
	}
	return errs
}

func canonicalStringSet(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

type TraceabilityLinkError struct {
	RowIndex int
	Field    string
	Value    string
	Message  string
}

func ValidateTraceabilityCrossLinks(
	matrix TraceabilityMatrix,
	evidenceCardIDs map[string]bool,
	prdRequirementIDs map[string]bool,
	adrIDs map[string]bool,
	tddObligationIDs map[string]bool,
	briefPaths map[string]bool,
	reportPaths map[string]bool,
) []TraceabilityLinkError {
	var errs []TraceabilityLinkError
	for i, row := range matrix.Matrix {
		if evidenceCardIDs != nil && !evidenceCardIDs[row.EvidenceCard] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "evidence_card",
				Value:    row.EvidenceCard,
				Message:  fmt.Sprintf("traceability row %d: evidence_card %q not found in evidence cards", i, row.EvidenceCard),
			})
		}
		if prdRequirementIDs != nil && !prdRequirementIDs[row.PRDRequirement] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "prd_requirement",
				Value:    row.PRDRequirement,
				Message:  fmt.Sprintf("traceability row %d: prd_requirement %q not found in PRD requirements", i, row.PRDRequirement),
			})
		}
		if adrIDs != nil && !adrIDs[row.ADR] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "adr",
				Value:    row.ADR,
				Message:  fmt.Sprintf("traceability row %d: adr %q not found in ADR index", i, row.ADR),
			})
		}
		if tddObligationIDs != nil && !tddObligationIDs[row.TDDObligation] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "tdd_obligation",
				Value:    row.TDDObligation,
				Message:  fmt.Sprintf("traceability row %d: tdd_obligation %q not found in current or historical TDD obligations", i, row.TDDObligation),
			})
		}
		if briefPaths != nil && !briefPaths[row.Brief] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "brief",
				Value:    row.Brief,
				Message:  fmt.Sprintf("traceability row %d: brief %q not found in briefs directory", i, row.Brief),
			})
		}
		if reportPaths != nil && !reportPaths[row.Report] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "report",
				Value:    row.Report,
				Message:  fmt.Sprintf("traceability row %d: report %q not found in reports directory", i, row.Report),
			})
		}
	}
	return errs
}

type ArtifactState string

const (
	ArtifactStateDraft      ArtifactState = "draft"
	ArtifactStateAudited    ArtifactState = "audited"
	ArtifactStateAccepted   ArtifactState = "accepted"
	ArtifactStateBlocked    ArtifactState = "blocked"
	ArtifactStateDispatched ArtifactState = "dispatched"
)

var validArtifactStates = map[ArtifactState]bool{
	ArtifactStateDraft:      true,
	ArtifactStateAudited:    true,
	ArtifactStateAccepted:   true,
	ArtifactStateBlocked:    true,
	ArtifactStateDispatched: true,
}

func ValidateArtifactState(status string) error {
	if status == "" {
		return errors.New("artifact status is empty")
	}
	if !validArtifactStates[ArtifactState(status)] {
		return fmt.Errorf("invalid artifact status %q; valid states: draft, audited, accepted, blocked, dispatched", status)
	}
	return nil
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

func require(errs *[]error, value string, message string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, errors.New(message))
	}
}

func isValidCommitHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

var allowedComplexityTiers = map[string]bool{
	"C0": true, "C1": true, "C2": true, "C3": true, "C4": true,
}

func ParseImplementationReportJSON(data []byte) (ImplementationReport, error) {
	var report ImplementationReport
	if err := json.Unmarshal(data, &report); err != nil {
		return ImplementationReport{}, fmt.Errorf("parse implementation report: %w", err)
	}
	if err := validateImplementationReport(report); err != nil {
		return ImplementationReport{}, err
	}
	return report, nil
}

func ParseReviewReportJSON(data []byte) (ReviewReport, error) {
	var report ReviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		return ReviewReport{}, fmt.Errorf("parse review report: %w", err)
	}
	if err := validateReviewReport(report); err != nil {
		return ReviewReport{}, err
	}
	return report, nil
}

func ParseInvestigationReportJSON(data []byte) (InvestigationReport, error) {
	var report InvestigationReport
	if err := json.Unmarshal(data, &report); err != nil {
		return InvestigationReport{}, fmt.Errorf("parse investigation report: %w", err)
	}
	if err := validateInvestigationReport(report); err != nil {
		return InvestigationReport{}, err
	}
	return report, nil
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
	if r.Verification.Status != "" && r.Verification.Status != "pending" {
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

func validateReviewReport(r ReviewReport) error {
	var errs []error
	require(&errs, r.SchemaVersion, "review report missing schema_version")
	require(&errs, r.EffortID, "review report missing effort_id")
	require(&errs, r.TaskID, "review report missing task_id")
	require(&errs, r.Reviewer.AgentID, "review report missing reviewer.agent_id")
	require(&errs, r.Reviewer.Model, "review report missing reviewer.model")
	require(&errs, r.Reviewer.ReasoningEffort, "review report missing reviewer.reasoning_effort")
	require(&errs, r.Status, "review report missing status")
	if r.Status != "" && r.Status != "pending" {
		require(&errs, r.ReviewedAt, "review report missing reviewed_at (required when status is not pending)")
		if len(r.CommandsReviewed) == 0 {
			errs = append(errs, errors.New("review report missing commands_reviewed (required when status is not pending)"))
		}
		if len(r.Checks) == 0 {
			errs = append(errs, errors.New("review report missing checks (required when status is not pending)"))
		}
	}
	if !r.Reviewer.IndependentFromImplementor {
		errs = append(errs, errors.New("review report reviewer.independent_from_implementor must be true"))
	}
	return errors.Join(errs...)
}

func validateInvestigationReport(r InvestigationReport) error {
	var errs []error
	require(&errs, r.SchemaVersion, "investigation report missing schema_version")
	require(&errs, r.EffortID, "investigation report missing effort_id")
	require(&errs, r.TaskID, "investigation report missing task_id")
	require(&errs, r.Status, "investigation report missing status")
	if r.Status == "complete" {
		require(&errs, r.CompletedAt, "investigation report missing completed_at (required when status is complete)")
		require(&errs, r.NextValidAction, "investigation report missing next_valid_action (required when status is complete)")
		if len(r.Scope) == 0 {
			errs = append(errs, errors.New("investigation report missing scope (required when status is complete)"))
		}
		if len(r.Tests) == 0 {
			errs = append(errs, errors.New("investigation report missing tests (required when status is complete)"))
		}
		if len(r.Coverage) == 0 {
			errs = append(errs, errors.New("investigation report missing coverage (required when status is complete)"))
		}
		if len(r.Traceability) == 0 {
			errs = append(errs, errors.New("investigation report missing traceability (required when status is complete)"))
		}
		if len(r.PrivacyAudit) == 0 {
			errs = append(errs, errors.New("investigation report missing privacy_audit (required when status is complete)"))
		}
		if len(r.Blockers) > 0 {
			errs = append(errs, errors.New("investigation report status is complete but blockers is non-empty"))
		}
	}
	if len(r.PrivacyAudit) > 0 {
		if err := validatePrivacyAudit(r.PrivacyAudit); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var privacyAuditStoredTrue = []string{
	"raw_prompts_stored",
	"raw_transcripts_stored",
	"file_bodies_stored",
	"secrets_stored",
	"customer_identifying_data_stored",
}

var privacyAuditNoFalse = []string{
	"no_raw_prompts",
	"no_raw_transcripts",
	"no_file_bodies",
	"no_secrets",
	"no_customer_data",
}

func validatePrivacyAudit(audit map[string]any) error {
	var errs []error
	for _, key := range privacyAuditStoredTrue {
		if val, ok := audit[key]; ok {
			if b, _ := val.(bool); b {
				errs = append(errs, fmt.Errorf("investigation report privacy_audit: %s must not be true", key))
			}
		}
	}
	for _, key := range privacyAuditNoFalse {
		if val, ok := audit[key]; ok {
			if b, _ := val.(bool); !b {
				errs = append(errs, fmt.Errorf("investigation report privacy_audit: %s must not be false", key))
			}
		}
	}
	return errors.Join(errs...)
}

var privacyForbiddenClaims = []string{
	"raw_prompts_stored",
	"raw_transcripts_stored",
	"file_bodies_stored",
	"secrets_stored",
	"customer_identifying_data_stored",
}

func validateReportPrivacyNote(note string) error {
	if note == "" {
		return nil
	}
	lower := strings.ToLower(note)
	for _, claim := range privacyForbiddenClaims {
		if strings.Contains(lower, claim+": true") || strings.Contains(lower, claim+"\":true") {
			return fmt.Errorf("implementation report privacy_note claims %s was stored", claim)
		}
	}
	return nil
}

func ClassifyReportFileName(name string) (reportType string, known bool) {
	if strings.HasSuffix(name, "-implementation.json") {
		return "implementation", true
	}
	if strings.HasSuffix(name, "-review.json") {
		return "review", true
	}
	if strings.HasSuffix(name, "-investigation.json") {
		return "investigation", true
	}
	return "", false
}
