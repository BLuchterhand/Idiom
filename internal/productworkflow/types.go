package productworkflow

type EffortIndex struct {
	SchemaVersion string             `json:"schema_version"`
	UpdatedAt     string             `json:"updated_at"`
	Efforts       []EffortIndexEntry `json:"efforts"`
}

type EffortIndexEntry struct {
	EffortID     string   `json:"effort_id"`
	Title        string   `json:"title"`
	StatusBucket string   `json:"status_bucket"`
	CurrentPhase string   `json:"current_phase"`
	CurrentState string   `json:"current_state"`
	FolderPath   string   `json:"folder_path"`
	TaskID       string   `json:"task_id"`
	CreatedAt    string   `json:"created_at"`
	PriorPaths   []string `json:"prior_paths"`
}

type WorkflowState struct {
	SchemaVersion          string            `json:"schema_version"`
	EffortID               string            `json:"effort_id"`
	TaskID                 string            `json:"task_id"`
	Title                  string            `json:"title"`
	CreatedAt              string            `json:"created_at"`
	UpdatedAt              string            `json:"updated_at"`
	StateRevision          int               `json:"state_revision"`
	LastEventID            string            `json:"last_event_id"`
	WorkspaceFingerprint   string            `json:"workspace_fingerprint"`
	StatusBucket           string            `json:"status_bucket"`
	CurrentPhase           string            `json:"current_phase"`
	CurrentState           string            `json:"current_state"`
	ArtifactPaths          map[string]string `json:"artifact_paths"`
	Context                WorkflowContext   `json:"context"`
	Coverage               CoverageState     `json:"coverage"`
	OpenObligations        []string          `json:"open_obligations"`
	OpenQuestions          []string          `json:"open_questions"`
	Blockers               []string          `json:"blockers"`
	Privacy                PrivacyState      `json:"privacy"`
	SessionIDs             map[string]string `json:"session_ids"`
	FolderPath             string            `json:"folder_path"`
	PriorFolderPaths       []string          `json:"prior_folder_paths"`
	LastReconciliationAt   string            `json:"last_reconciliation_at"`
	ReconciliationMismatch bool              `json:"reconciliation_mismatch"`
	NextValidActions       []string          `json:"next_valid_actions"`
	DurabilityLevel        string            `json:"durability_level"`
}

type WorkflowContext struct {
	ContextPackID   string          `json:"context_pack_id"`
	FreshnessState  string          `json:"freshness_state"`
	ReferencedPaths []string        `json:"referenced_paths"`
	UnresolvedGaps  []UnresolvedGap `json:"unresolved_gaps"`
	RefreshPolicy   string          `json:"refresh_policy"`
}

type UnresolvedGap struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

type CoverageState struct {
	CoverageTier     string         `json:"coverage_tier"`
	GateResults      []GateResult   `json:"gate_results"`
	BlockingCriteria []string       `json:"blocking_criteria"`
	AdvisoryCriteria []string       `json:"advisory_criteria"`
	Extra            map[string]any `json:"-"`
}

type GateResult struct {
	Gate      string `json:"gate"`
	Status    string `json:"status"`
	CheckedAt string `json:"checked_at"`
}

type PrivacyState struct {
	RedactionStatus      string   `json:"redaction_status"`
	FieldsExcluded       []string `json:"fields_excluded"`
	RawPromptsStored     *bool    `json:"raw_prompts_stored,omitempty"`
	RawTranscriptsStored *bool    `json:"raw_transcripts_stored,omitempty"`
	FileBodiesStored     *bool    `json:"file_bodies_stored,omitempty"`
	SecretsStored        *bool    `json:"secrets_stored,omitempty"`
}

type TDDLedger struct {
	SchemaVersion string          `json:"schema_version"`
	EffortID      string          `json:"effort_id"`
	TaskID        string          `json:"task_id"`
	CoverageTier  string          `json:"coverage_tier"`
	Coverage      TDDCoverage     `json:"coverage"`
	Obligations   []TDDObligation `json:"obligations"`
}

type TDDCoverage struct {
	EARSTotal                int      `json:"ears_total"`
	EARSMapped               int      `json:"ears_mapped"`
	BDDTotal                 int      `json:"bdd_total"`
	BDDWithConcreteData      int      `json:"bdd_with_concrete_data"`
	FMEAModesTotal           int      `json:"fmea_modes_total"`
	FMEAModesMitigated       int      `json:"fmea_modes_mitigated"`
	HAZOPDeviationsTotal     int      `json:"hazop_deviations_total"`
	HAZOPDeviationsTested    int      `json:"hazop_deviations_tested"`
	InvariantsTotal          int      `json:"invariants_total"`
	InvariantsWithPBT        int      `json:"invariants_with_pbt"`
	DecisionTablesTotal      int      `json:"decision_tables_total"`
	DecisionTablesEnumerated int      `json:"decision_tables_enumerated"`
	MutationScore            *float64 `json:"mutation_score"`
	MutationMinScore         int      `json:"mutation_min_score"`
	GatesPassed              []string `json:"gates_passed"`
	GatesBlocked             []string `json:"gates_blocked"`
}

type TDDObligation struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Behavior       string `json:"behavior"`
	Owner          string `json:"owner"`
	Command        string `json:"command"`
	RequiredBefore string `json:"required_before"`
	Status         string `json:"status"`
}

type EvidenceCard struct {
	CardID       string   `json:"card_id"`
	Kind         string   `json:"kind"`
	SourceHandle string   `json:"source_handle"`
	Claim        string   `json:"claim"`
	Confidence   string   `json:"confidence"`
	Freshness    string   `json:"freshness"`
	Uncertainty  string   `json:"uncertainty"`
	EvidenceRefs []string `json:"evidence_refs"`
	ResearcherID string   `json:"researcher_id"`
	Timestamp    string   `json:"timestamp"`
}

type TraceabilityMatrix struct {
	SchemaVersion string            `json:"schema_version"`
	EffortID      string            `json:"effort_id"`
	TaskID        string            `json:"task_id"`
	CreatedAt     string            `json:"created_at"`
	Matrix        []TraceabilityRow `json:"matrix"`
}

type TraceabilityRow struct {
	EvidenceCard   string `json:"evidence_card"`
	PRDRequirement string `json:"prd_requirement"`
	ADR            string `json:"adr"`
	TDDObligation  string `json:"tdd_obligation"`
	Brief          string `json:"brief"`
	Report         string `json:"report"`
}

type ImplementationLedgerRecord struct {
	RecordType              string                 `json:"record_type"`
	TaskID                  string                 `json:"task_id"`
	EffortID                string                 `json:"effort_id"`
	Timestamp               string                 `json:"timestamp"`
	ComplexityTier          string                 `json:"complexity_tier,omitempty"`
	Phase                   string                 `json:"phase,omitempty"`
	ContextPackID           string                 `json:"context_pack_id,omitempty"`
	Implementor             *ImplementorAssignment `json:"implementor,omitempty"`
	Reviewer                *ReviewerAssignment    `json:"reviewer,omitempty"`
	Scope                   *AtomScope             `json:"scope,omitempty"`
	RequiredVerification    []string               `json:"required_verification,omitempty"`
	ReportPaths             *ReportPaths           `json:"report_paths,omitempty"`
	StandardImplementor     string                 `json:"standard_implementor,omitempty"`
	FallbackActor           string                 `json:"fallback_actor,omitempty"`
	Reason                  string                 `json:"reason,omitempty"`
	AllowedScope            string                 `json:"allowed_scope,omitempty"`
	CompensatingChecks      []string               `json:"compensating_checks,omitempty"`
	Actor                   string                 `json:"actor,omitempty"`
	ImplementorID           string                 `json:"implementor_id,omitempty"`
	ReviewerID              string                 `json:"reviewer_id,omitempty"`
	ChangedPaths            []string               `json:"changed_paths,omitempty"`
	CodeChanges             bool                   `json:"code_changes,omitempty"`
	VerificationCommandsRun []string               `json:"verification_commands_run,omitempty"`
	VerificationStatus      string                 `json:"verification_status,omitempty"`
	Commands                []string               `json:"commands,omitempty"`
	Status                  string                 `json:"status,omitempty"`
	Model                   string                 `json:"model,omitempty"`
	ReasoningEffort         string                 `json:"reasoning_effort,omitempty"`
	Decision                string                 `json:"decision,omitempty"`
	Findings                []any                  `json:"findings,omitempty"`
	State                   string                 `json:"state,omitempty"`
	CloseReason             string                 `json:"close_reason,omitempty"`
	OpenBlockers            []string               `json:"open_blockers,omitempty"`
	Extra                   map[string]any         `json:"-"`
}

type ImplementorAssignment struct {
	ID      string `json:"id"`
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
	Command string `json:"command"`
}

type ReviewerAssignment struct {
	ID              string `json:"id"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	Runtime         string `json:"runtime"`
	Status          string `json:"status"`
}

type AtomScope struct {
	ExpectedFiles     []string `json:"expected_files"`
	AllowedFiles      []string `json:"allowed_files,omitempty"`
	Forbidden         []string `json:"forbidden,omitempty"`
	ForbiddenPatterns []string `json:"forbidden_patterns,omitempty"`
}

type ReportPaths struct {
	Implementation string `json:"implementation,omitempty"`
	Review         string `json:"review,omitempty"`
	Investigation  string `json:"investigation,omitempty"`
}

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
	SchemaVersion           string         `json:"schema_version"`
	EffortID                string         `json:"effort_id"`
	TaskID                  string         `json:"task_id"`
	Reviewer                ReviewerInfo   `json:"reviewer"`
	Status                  string         `json:"status"`
	Findings                []any          `json:"findings"`
	Checks                  map[string]any `json:"checks"`
	ReviewedAt              string         `json:"reviewed_at"`
	CommandsReviewed        []string       `json:"commands_reviewed"`
	FallbackExceptionReview map[string]any `json:"fallback_exception_review,omitempty"`
	CreatedAt               string         `json:"created_at"`
}

type ReviewerInfo struct {
	AgentID                    string `json:"agent_id"`
	Model                      string `json:"model"`
	ReasoningEffort            string `json:"reasoning_effort"`
	IndependentFromImplementor bool   `json:"independent_from_implementor"`
}

type InvestigationReport struct {
	SchemaVersion    string         `json:"schema_version"`
	EffortID         string         `json:"effort_id"`
	TaskID           string         `json:"task_id"`
	Status           string         `json:"status"`
	CreatedAt        string         `json:"created_at"`
	Scope            map[string]any `json:"scope"`
	Tests            map[string]any `json:"tests"`
	Coverage         map[string]any `json:"coverage"`
	Traceability     map[string]any `json:"traceability"`
	PrivacyAudit     map[string]any `json:"privacy_audit"`
	ReviewerFindings []any          `json:"reviewer_findings"`
	Blockers         []string       `json:"blockers"`
	NextValidAction  string         `json:"next_valid_action"`
	CompletedAt      string         `json:"completed_at"`
}
