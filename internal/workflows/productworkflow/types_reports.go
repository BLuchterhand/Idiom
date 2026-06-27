package productworkflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ImplementationReport struct {
	SchemaVersion  string            `json:"schema_version"`
	TaskID         string            `json:"task_id"`
	EffortID       string            `json:"effort_id"`
	ImplementorID  string            `json:"implementor_id"`
	Runtime        string            `json:"runtime"`
	Model          string            `json:"model"`
	CompletedAt    string            `json:"completed_at"`
	ComplexityTier string            `json:"complexity_tier"`
	ScopeStatement string            `json:"scope_statement"`
	CodeChanges    bool              `json:"code_changes"`
	ChangedPaths   []string          `json:"changed_paths"`
	Verification   VerificationState `json:"verification"`
	PrivacyNote    string            `json:"privacy_note"`
	OpenBlockers   []string          `json:"open_blockers"`
	ReviewerID     string            `json:"reviewer_id"`
}

type VerificationState struct {
	RunByImplementor bool     `json:"run_by_implementor"`
	Commands         []string `json:"commands"`
	Status           string   `json:"status"`
}

type ReviewReport struct {
	SchemaVersion           string                   `json:"schema_version"`
	EffortID                string                   `json:"effort_id"`
	TaskID                  string                   `json:"task_id"`
	Reviewer                ReviewerInfo             `json:"reviewer"`
	Status                  string                   `json:"status"`
	Findings                []ReviewFinding          `json:"findings"`
	Checks                  ReviewChecks             `json:"checks"`
	ReviewedAt              string                   `json:"reviewed_at"`
	CommandsReviewed        []string                 `json:"commands_reviewed"`
	FallbackExceptionReview *FallbackExceptionReview `json:"fallback_exception_review,omitempty"`
	CreatedAt               string                   `json:"created_at"`
}

type ReviewerInfo struct {
	AgentID                    string `json:"agent_id"`
	AgentPath                  string `json:"agent_path,omitempty"`
	Model                      string `json:"model"`
	ReasoningEffort            string `json:"reasoning_effort"`
	IndependentFromImplementor bool   `json:"independent_from_implementor"`
}

type ReviewFinding struct {
	Severity     string `json:"severity,omitempty"`
	Code         string `json:"code,omitempty"`
	Status       string `json:"status,omitempty"`
	Path         string `json:"path,omitempty"`
	Message      string `json:"message,omitempty"`
	Summary      string `json:"summary,omitempty"`
	RepairCommit string `json:"repair_commit,omitempty"`
}

func (f *ReviewFinding) UnmarshalJSON(data []byte) error {
	var message string
	if err := json.Unmarshal(data, &message); err == nil {
		f.Message = message
		return nil
	}

	type findingAlias ReviewFinding
	var parsed findingAlias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	*f = ReviewFinding(parsed)
	return nil
}

type ReviewChecks map[string]ReviewCheckStatus

type ReviewCheckStatus string

const (
	ReviewCheckStatusPass    ReviewCheckStatus = "pass"
	ReviewCheckStatusPassed  ReviewCheckStatus = "passed"
	ReviewCheckStatusFail    ReviewCheckStatus = "fail"
	ReviewCheckStatusFailed  ReviewCheckStatus = "failed"
	ReviewCheckStatusBlocked ReviewCheckStatus = "blocked"
	ReviewCheckStatusWarning ReviewCheckStatus = "warning"
	ReviewCheckStatusWarn    ReviewCheckStatus = "warn"
	ReviewCheckStatusPending ReviewCheckStatus = "pending"
	ReviewCheckStatusSkipped ReviewCheckStatus = "skipped"
)

func (s *ReviewCheckStatus) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		status, err := ParseReviewCheckStatus(text)
		if err != nil {
			return err
		}
		*s = status
		return nil
	}

	var flag bool
	if err := json.Unmarshal(data, &flag); err == nil {
		if flag {
			*s = ReviewCheckStatusPass
		} else {
			*s = ReviewCheckStatusFail
		}
		return nil
	}

	return fmt.Errorf("unsupported review check status %s", strings.TrimSpace(string(data)))
}

func ParseReviewCheckStatus(value string) (ReviewCheckStatus, error) {
	status := ReviewCheckStatus(strings.TrimSpace(value))
	if KnownReviewCheckStatus(status) {
		return status, nil
	}

	return "", fmt.Errorf("unsupported review check status %q", strings.TrimSpace(value))
}

func KnownReviewCheckStatus(status ReviewCheckStatus) bool {
	switch status {
	case ReviewCheckStatusPass,
		ReviewCheckStatusPassed,
		ReviewCheckStatusFail,
		ReviewCheckStatusFailed,
		ReviewCheckStatusBlocked,
		ReviewCheckStatusWarning,
		ReviewCheckStatusWarn,
		ReviewCheckStatusPending,
		ReviewCheckStatusSkipped:
		return true
	default:
		return false
	}
}

type FallbackExceptionReview struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

type InvestigationReport struct {
	SchemaVersion    string                    `json:"schema_version"`
	EffortID         string                    `json:"effort_id"`
	TaskID           string                    `json:"task_id"`
	Status           string                    `json:"status"`
	CreatedAt        string                    `json:"created_at"`
	Scope            InvestigationScope        `json:"scope"`
	Tests            InvestigationTests        `json:"tests"`
	Coverage         InvestigationCoverage     `json:"coverage"`
	Traceability     InvestigationTraceability `json:"traceability"`
	PrivacyAudit     InvestigationPrivacyAudit `json:"privacy_audit"`
	ReviewerFindings []string                  `json:"reviewer_findings"`
	Blockers         []string                  `json:"blockers"`
	NextValidAction  string                    `json:"next_valid_action"`
	CompletedAt      string                    `json:"completed_at"`
}

type InvestigationScope struct {
	ExpectedPaths                 []string          `json:"expected_paths,omitempty"`
	ExpectedFiles                 []string          `json:"expected_files,omitempty"`
	ChangedPaths                  []string          `json:"changed_paths,omitempty"`
	ForbiddenPaths                []string          `json:"forbidden_paths,omitempty"`
	ForbiddenPatterns             []string          `json:"forbidden_patterns,omitempty"`
	ForbiddenScopeTouched         *bool             `json:"forbidden_scope_touched,omitempty"`
	DocParityUpdated              *bool             `json:"doc_parity_updated,omitempty"`
	ObsidianNoChangeRationale     string            `json:"obsidian_no_change_rationale,omitempty"`
	ChangedPathsChecked           *bool             `json:"changed_paths_checked,omitempty"`
	OutOfScopeChanges             []string          `json:"out_of_scope_changes,omitempty"`
	DispatchOrModelExecutionAdded *bool             `json:"dispatch_or_model_execution_added,omitempty"`
	RuntimeStateMutationAdded     *bool             `json:"runtime_state_mutation_added,omitempty"`
	BroadTUIBehaviorAdded         *bool             `json:"broad_tui_behavior_added,omitempty"`
	StatusFolderMigrationAdded    *bool             `json:"status_folder_migration_added,omitempty"`
	LegacyDetails                 map[string]string `json:"legacy_details,omitempty"`
}

func (s *InvestigationScope) UnmarshalJSON(data []byte) error {
	type scopeAlias InvestigationScope
	var parsed scopeAlias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	*s = InvestigationScope(parsed)
	s.LegacyDetails = legacyStringDetails(data, []string{
		"expected_paths", "expected_files", "changed_paths", "forbidden_paths", "forbidden_patterns",
		"obsidian_no_change_rationale", "changed_paths_checked", "out_of_scope_changes",
		"forbidden_scope_touched", "doc_parity_updated",
		"dispatch_or_model_execution_added", "runtime_state_mutation_added",
		"broad_tui_behavior_added", "status_folder_migration_added",
	}, investigationScopeLegacyKeys)

	return nil
}

func (s InvestigationScope) HasContent() bool {
	return len(s.ExpectedPaths) > 0 ||
		len(s.ExpectedFiles) > 0 ||
		len(s.ChangedPaths) > 0 ||
		len(s.ForbiddenPaths) > 0 ||
		len(s.ForbiddenPatterns) > 0 ||
		s.ForbiddenScopeTouched != nil ||
		s.DocParityUpdated != nil ||
		s.ObsidianNoChangeRationale != "" ||
		s.ChangedPathsChecked != nil ||
		len(s.OutOfScopeChanges) > 0 ||
		s.DispatchOrModelExecutionAdded != nil ||
		s.RuntimeStateMutationAdded != nil ||
		s.BroadTUIBehaviorAdded != nil ||
		s.StatusFolderMigrationAdded != nil
}

type InvestigationTests struct {
	Status        string                     `json:"status,omitempty"`
	Commands      []InvestigationTestCommand `json:"commands,omitempty"`
	Blocked       []string                   `json:"blocked,omitempty"`
	Focused       string                     `json:"focused,omitempty"`
	FullSuite     string                     `json:"full_suite,omitempty"`
	ProductLint   string                     `json:"product_lint,omitempty"`
	Whitespace    string                     `json:"whitespace,omitempty"`
	LegacyDetails map[string]string          `json:"legacy_details,omitempty"`
}

func (t *InvestigationTests) UnmarshalJSON(data []byte) error {
	type testsAlias InvestigationTests
	var parsed testsAlias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	*t = InvestigationTests(parsed)
	t.LegacyDetails = legacyStringDetails(data, []string{
		"status", "commands", "blocked", "focused", "full_suite", "product_lint", "whitespace",
	}, investigationTestsLegacyKeys)

	return nil
}

func (t InvestigationTests) HasContent() bool {
	return t.Status != "" ||
		len(t.Commands) > 0 ||
		len(t.Blocked) > 0 ||
		t.Focused != "" ||
		t.FullSuite != "" ||
		t.ProductLint != "" ||
		t.Whitespace != ""
}

type InvestigationTestCommand struct {
	Command string `json:"command"`
	Status  string `json:"status,omitempty"`
}

func (c *InvestigationTestCommand) UnmarshalJSON(data []byte) error {
	var command string
	if err := json.Unmarshal(data, &command); err == nil {
		c.Command = command
		return nil
	}

	type commandAlias InvestigationTestCommand
	var parsed commandAlias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	*c = InvestigationTestCommand(parsed)
	return nil
}

type InvestigationCoverage struct {
	TDDObligationsPending   []string                     `json:"tdd_obligations_pending,omitempty"`
	TDDObligationsTotal     int                          `json:"tdd_obligations_total,omitempty"`
	TDDObligationsSatisfied InvestigationCoverageMeasure `json:"tdd_obligations_satisfied,omitempty"`
	ObligationsOpen         []string                     `json:"obligations_open,omitempty"`
	ObligationsSatisfied    []string                     `json:"obligations_satisfied,omitempty"`
	ImplementationCommit    string                       `json:"implementation_commit,omitempty"`
	ImplementationCommits   []string                     `json:"implementation_commits,omitempty"`
	ProductLintStatus       string                       `json:"product_lint_status,omitempty"`
	ProductAuditStatus      string                       `json:"product_audit_status,omitempty"`
	ProductStatusStatus     string                       `json:"product_status_status,omitempty"`
	CoverageGateStatus      string                       `json:"coverage_gate_status,omitempty"`
	RequiredCriteria        []string                     `json:"required_criteria,omitempty"`
	Blockers                []string                     `json:"blockers,omitempty"`
	RankerLearningStatus    string                       `json:"ranker_learning_adapter_status,omitempty"`
	Tier                    string                       `json:"tier,omitempty"`
	CoverageTier            string                       `json:"coverage_tier,omitempty"`
	LegacyDetails           map[string]string            `json:"legacy_details,omitempty"`
}

func (c *InvestigationCoverage) UnmarshalJSON(data []byte) error {
	type coverageAlias InvestigationCoverage
	var parsed coverageAlias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	*c = InvestigationCoverage(parsed)
	c.LegacyDetails = legacyStringDetails(data, []string{
		"tdd_obligations_pending", "tdd_obligations_total", "tdd_obligations_satisfied",
		"obligations_open", "obligations_satisfied", "implementation_commit",
		"implementation_commits", "product_lint_status", "product_audit_status",
		"product_status_status", "coverage_gate_status", "required_criteria", "blockers",
		"ranker_learning_adapter_status", "tier", "coverage_tier",
	}, investigationCoverageLegacyKeys)

	return nil
}

func (c InvestigationCoverage) HasContent() bool {
	return len(c.TDDObligationsPending) > 0 ||
		c.TDDObligationsTotal > 0 ||
		c.TDDObligationsSatisfied.HasContent() ||
		len(c.ObligationsOpen) > 0 ||
		len(c.ObligationsSatisfied) > 0 ||
		c.ImplementationCommit != "" ||
		len(c.ImplementationCommits) > 0 ||
		c.ProductLintStatus != "" ||
		c.ProductAuditStatus != "" ||
		c.ProductStatusStatus != "" ||
		c.CoverageGateStatus != "" ||
		len(c.RequiredCriteria) > 0 ||
		len(c.Blockers) > 0 ||
		c.RankerLearningStatus != "" ||
		c.Tier != "" ||
		c.CoverageTier != ""
}

type InvestigationCoverageMeasure struct {
	Values []string
	Count  int
	Set    bool
}

func (m *InvestigationCoverageMeasure) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*m = InvestigationCoverageMeasure{}
		return nil
	}

	var values []string
	if err := json.Unmarshal(data, &values); err == nil {
		m.Values = values
		m.Set = len(values) > 0
		return nil
	}

	var count int
	if err := json.Unmarshal(data, &count); err == nil {
		if count < 0 {
			return fmt.Errorf("coverage measure count must be non-negative, got %d", count)
		}
		m.Count = count
		m.Set = true
		return nil
	}

	return fmt.Errorf("unsupported coverage measure %s", strings.TrimSpace(string(data)))
}

func (m InvestigationCoverageMeasure) MarshalJSON() ([]byte, error) {
	if len(m.Values) > 0 {
		return json.Marshal(m.Values)
	}
	if m.Set {
		return json.Marshal(m.Count)
	}
	return []byte("null"), nil
}

func (m InvestigationCoverageMeasure) HasContent() bool {
	return len(m.Values) > 0 || m.Set
}

type InvestigationTraceability struct {
	ContextPackIDs        []string          `json:"context_pack_ids,omitempty"`
	ContextPackID         string            `json:"context_pack_id,omitempty"`
	ImplementationCommit  string            `json:"implementation_commit,omitempty"`
	ReviewRepairCommit    string            `json:"review_repair_commit,omitempty"`
	ImplementationCommits []string          `json:"implementation_commits,omitempty"`
	ProductLintStatus     string            `json:"product_lint_status,omitempty"`
	ProductAuditStatus    string            `json:"product_audit_status,omitempty"`
	ProductStatusStatus   string            `json:"product_status_status,omitempty"`
	ProductEffortsStatus  string            `json:"product_efforts_status,omitempty"`
	DesignRef             string            `json:"design_ref,omitempty"`
	ADRRefs               []string          `json:"adr_refs,omitempty"`
	PRDRefs               []string          `json:"prd_refs,omitempty"`
	Checked               *bool             `json:"checked,omitempty"`
	Gaps                  []string          `json:"gaps,omitempty"`
	LegacyDetails         map[string]string `json:"legacy_details,omitempty"`
}

func (t *InvestigationTraceability) UnmarshalJSON(data []byte) error {
	type traceabilityAlias InvestigationTraceability
	var parsed traceabilityAlias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	*t = InvestigationTraceability(parsed)
	t.LegacyDetails = legacyStringDetails(data, []string{
		"context_pack_ids", "context_pack_id", "implementation_commit", "review_repair_commit",
		"implementation_commits", "product_lint_status", "product_audit_status",
		"product_status_status", "product_efforts_status", "design_ref", "adr_refs",
		"prd_refs", "checked", "gaps",
	}, investigationTraceabilityLegacyKeys)

	return nil
}

func (t InvestigationTraceability) HasContent() bool {
	return len(t.ContextPackIDs) > 0 ||
		t.ContextPackID != "" ||
		t.ImplementationCommit != "" ||
		t.ReviewRepairCommit != "" ||
		len(t.ImplementationCommits) > 0 ||
		t.ProductLintStatus != "" ||
		t.ProductAuditStatus != "" ||
		t.ProductStatusStatus != "" ||
		t.ProductEffortsStatus != "" ||
		t.DesignRef != "" ||
		len(t.ADRRefs) > 0 ||
		len(t.PRDRefs) > 0 ||
		t.Checked != nil ||
		len(t.Gaps) > 0
}

type InvestigationPrivacyAudit struct {
	RawPromptsStored                         *bool    `json:"raw_prompts_stored,omitempty"`
	RawTranscriptsStored                     *bool    `json:"raw_transcripts_stored,omitempty"`
	TranscriptsStored                        *bool    `json:"transcripts_stored,omitempty"`
	FileBodiesStored                         *bool    `json:"file_bodies_stored,omitempty"`
	SecretsStored                            *bool    `json:"secrets_stored,omitempty"`
	CustomerIdentifyingDataStored            *bool    `json:"customer_identifying_data_stored,omitempty"`
	TUITranscriptBodiesStored                *bool    `json:"tui_transcript_bodies_stored,omitempty"`
	GeneratedBriefBodiesStoredDurableReports *bool    `json:"generated_brief_bodies_stored_in_durable_reports,omitempty"`
	RankerWeightPayloadsStored               *bool    `json:"ranker_weight_payloads_stored,omitempty"`
	NoRawPrompts                             *bool    `json:"no_raw_prompts,omitempty"`
	NoRawTranscripts                         *bool    `json:"no_raw_transcripts,omitempty"`
	NoFileBodies                             *bool    `json:"no_file_bodies,omitempty"`
	NoSecrets                                *bool    `json:"no_secrets,omitempty"`
	NoCustomerData                           *bool    `json:"no_customer_data,omitempty"`
	Checked                                  *bool    `json:"checked,omitempty"`
	ExposureFound                            *bool    `json:"exposure_found,omitempty"`
	SecretPatternScanStatus                  string   `json:"secret_pattern_scan_status,omitempty"`
	Findings                                 []string `json:"findings,omitempty"`
	Notes                                    []string `json:"notes,omitempty"`
}

func (a InvestigationPrivacyAudit) HasContent() bool {
	return a.RawPromptsStored != nil ||
		a.RawTranscriptsStored != nil ||
		a.TranscriptsStored != nil ||
		a.FileBodiesStored != nil ||
		a.SecretsStored != nil ||
		a.CustomerIdentifyingDataStored != nil ||
		a.TUITranscriptBodiesStored != nil ||
		a.GeneratedBriefBodiesStoredDurableReports != nil ||
		a.RankerWeightPayloadsStored != nil ||
		a.NoRawPrompts != nil ||
		a.NoRawTranscripts != nil ||
		a.NoFileBodies != nil ||
		a.NoSecrets != nil ||
		a.NoCustomerData != nil ||
		a.Checked != nil ||
		a.ExposureFound != nil ||
		a.SecretPatternScanStatus != "" ||
		len(a.Findings) > 0 ||
		len(a.Notes) > 0
}

var investigationScopeLegacyKeys = map[string]struct{}{
	"acceptance_metadata":                            {},
	"adr_prompt_rendering":                           {},
	"allowed_scope":                                  {},
	"artifact_recipe":                                {},
	"carry_forward_reconciliation":                   {},
	"cli_surface":                                    {},
	"durable_policy":                                 {},
	"durable_state_mutation_during_command":          {},
	"expected_paths_changed":                         {},
	"forbidden_paths_changed":                        {},
	"forbidden_paths_touched":                        {},
	"generated_brief_body_stored_in_durable_reports": {},
	"implementation_checkpoint":                      {},
	"merge_logic":                                    {},
	"obsidian_parity_updated":                        {},
	"plan_only_mutation_check":                       {},
	"prd_prompt_rendering":                           {},
	"product_workflow.go":                            {},
	"prompt_rendering":                               {},
	"ranker_weight_promotion_added":                  {},
	"run_planning":                                   {},
	"safety":                                         {},
	"scope.go":                                       {},
	"scope_status":                                   {},
	"supersession_links":                             {},
	"tests":                                          {},
	"types.go":                                       {},
	"validate.go":                                    {},
}

var investigationTestsLegacyKeys = map[string]struct{}{
	"cmd_idiom":               {},
	"commit_hash_validation":  {},
	"diff_check":              {},
	"durable_command":         {},
	"durable_gates":           {},
	"fixture_status_scan":     {},
	"focused_cli":             {},
	"focused_internal":        {},
	"focused_productworkflow": {},
	"json_jsonl":              {},
	"json_jsonl_validation":   {},
	"lint_integration":        {},
	"package_pair":            {},
	"per_atom_coverage":       {},
	"privacy_scan":            {},
	"privacy_secret_scan":     {},
	"product_audit":           {},
	"product_research":        {},
	"product_status":          {},
	"productworkflow":         {},
	"purity":                  {},
	"scope_closeout_gate":     {},
}

var investigationCoverageLegacyKeys = map[string]struct{}{
	"blocked_reason":                                 {},
	"brief_lint_exit":                                {},
	"covered_behaviors":                              {},
	"durable_render_status":                          {},
	"full_suite":                                     {},
	"human_readable_cli_coverage_status":             {},
	"obsidian_parity_status":                         {},
	"product_feedback_fixture_status":                {},
	"product_feedback_reconciliation_fixture_status": {},
	"product_workflow_metrics_status":                {},
	"product_workflow_test.go":                       {},
	"remaining_phase_4_work":                         {},
	"review_findings_resolved":                       {},
	"scope_test.go":                                  {},
	"validate_test.go":                               {},
}

var investigationTraceabilityLegacyKeys = map[string]struct{}{
	"README":                        {},
	"blocker_resolved":              {},
	"brief_to_product_workflow":     {},
	"brief_to_scope":                {},
	"brief_to_tests":                {},
	"brief_to_types":                {},
	"brief_to_validate":             {},
	"depends_on":                    {},
	"design_doc":                    {},
	"initial_implementation_commit": {},
	"ledger":                        {},
	"review_pack_ids":               {},
	"tdd_obligations_satisfied":     {},
}

func legacyStringDetails(data []byte, knownKeys []string, allowedLegacyKeys map[string]struct{}) map[string]string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	for _, key := range knownKeys {
		delete(raw, key)
	}

	if len(raw) == 0 {
		return nil
	}

	out := make(map[string]string, len(raw))
	for key, body := range raw {
		if _, ok := allowedLegacyKeys[key]; !ok {
			continue
		}

		var value string
		if err := json.Unmarshal(body, &value); err == nil && strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
