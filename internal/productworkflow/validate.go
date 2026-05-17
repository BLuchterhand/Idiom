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

var allowedCoverageTiers = map[string]bool{
	"documentation":     true,
	"local_low":         true,
	"behavior_standard": true,
	"contract_high":     true,
	"formal_optional":   true,
	"unit":              true,
}

var allowedObligationTypes = map[string]bool{
	"characterization": true,
	"regression":       true,
	"acceptance":       true,
	"contract":         true,
	"source_sanity":    true,
	"eval":             true,
	"audit":            true,
	"property_based":   true,
	"fmea_negative":    true,
	"hazop_deviation":  true,
	"decision_table":   true,
	"boundary_value":   true,
	"mutation_score":   true,
	"post_mortem":      true,
	"manual":           true,
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
		{"decision_tables_total", coverage.DecisionTablesTotal},
		{"decision_tables_enumerated", coverage.DecisionTablesEnumerated},
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
	if coverage.DecisionTablesEnumerated > coverage.DecisionTablesTotal {
		errs = append(errs, fmt.Errorf("tdd ledger coverage.decision_tables_enumerated (%d) exceeds decision_tables_total (%d)", coverage.DecisionTablesEnumerated, coverage.DecisionTablesTotal))
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

func ParsePRDMarkdown(data []byte) (PRDDocument, error) {
	fields, body, err := parseFrontMatterRaw(data)
	if err != nil {
		return PRDDocument{}, fmt.Errorf("parse PRD: %w", err)
	}
	fm := PRDFrontMatter{
		SchemaVersion: fields["schema_version"],
		PRDID:         fields["prd_id"],
		Status:        fields["status"],
		TaskID:        fields["task_id"],
		CreatedAt:     fields["created_at"],
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
		SchemaVersion: fields["schema_version"],
		ADRID:         fields["adr_id"],
		Status:        fields["status"],
		PRDRefs:       parseFrontMatterList(fields, "prd_refs"),
	}
	doc := ADRDocument{FrontMatter: fm, HasDecision: strings.Contains(body, "## Decision")}
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
	if len(doc.Requirements) == 0 {
		errs = append(errs, errors.New("PRD has no requirements"))
	}
	for i, req := range doc.Requirements {
		prefix := fmt.Sprintf("PRD requirement %d (%s)", i, req.ID)
		require(&errs, req.ID, prefix+" missing id")
		if len(req.EvidenceRefs) == 0 {
			errs = append(errs, fmt.Errorf("%s missing evidence_refs", prefix))
		}
		if len(req.TDDObligations) == 0 {
			errs = append(errs, fmt.Errorf("%s missing tdd_obligations", prefix))
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
	if len(doc.FrontMatter.PRDRefs) == 0 {
		errs = append(errs, errors.New("ADR front matter missing prd_refs"))
	}
	if !doc.HasDecision {
		errs = append(errs, errors.New("ADR body missing ## Decision section"))
	}
	return errors.Join(errs...)
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

func require(errs *[]error, value string, message string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, errors.New(message))
	}
}
