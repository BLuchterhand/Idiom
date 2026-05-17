package productworkflow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
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

func validateTDDLedger(ledger TDDLedger) error {
	var errs []error
	require(&errs, ledger.SchemaVersion, "tdd ledger missing schema_version")
	require(&errs, ledger.EffortID, "tdd ledger missing effort_id")
	require(&errs, ledger.TaskID, "tdd ledger missing task_id")
	require(&errs, ledger.CoverageTier, "tdd ledger missing coverage_tier")
	if ledger.Coverage.MutationMinScore <= 0 {
		errs = append(errs, errors.New("tdd ledger missing positive mutation_min_score"))
	}
	if len(ledger.Obligations) == 0 {
		errs = append(errs, errors.New("tdd ledger missing obligations"))
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
	}
	return errors.Join(errs...)
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

func require(errs *[]error, value string, message string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, errors.New(message))
	}
}
