package productworkflow

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

type CoverageGateEvaluation struct {
	EffortID     string                  `json:"effort_id"`
	TaskID       string                  `json:"task_id"`
	CoverageTier string                  `json:"coverage_tier"`
	Status       string                  `json:"status"`
	Criteria     []CoverageGateCriterion `json:"criteria"`
	Findings     []CoverageGateFinding   `json:"findings"`
	Blockers     []CoverageGateBlocker   `json:"blockers"`
}

type CoverageGateEvaluatedEvent struct {
	Event          string                    `json:"event"`
	EffortID       string                    `json:"effort_id"`
	TaskID         string                    `json:"task_id"`
	Coverage       CoverageGateEventCoverage `json:"coverage"`
	GateResult     string                    `json:"gate_result"`
	BlockedReasons []string                  `json:"blocked_reasons"`
	Waivers        []string                  `json:"waivers"`
}

type CoverageGateEventCoverage struct {
	EARSCoveragePct       *float64 `json:"ears_coverage_pct"`
	BDDCoveragePct        *float64 `json:"bdd_coverage_pct"`
	FMEAMitigationPct     *float64 `json:"fmea_mitigation_pct"`
	HAZOPTestedPct        *float64 `json:"hazop_tested_pct"`
	InvariantPBTPct       *float64 `json:"invariant_pbt_pct"`
	StateMachinePct       *float64 `json:"state_machine_pct"`
	DecisionTablePct      *float64 `json:"decision_table_pct"`
	BoundaryValuePct      *float64 `json:"boundary_value_pct"`
	FaultTreeCoveragePct  *float64 `json:"fault_tree_coverage_pct"`
	PostMortemCoveragePct *float64 `json:"post_mortem_coverage_pct"`
	FormalVerificationPct *float64 `json:"formal_verification_pct"`
	MutationScore         *float64 `json:"mutation_score"`
}

type CoverageGateCriterion struct {
	Key      string `json:"key"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
	Details  string `json:"details,omitempty"`
}

type CoverageGateFinding struct {
	Kind      string `json:"kind"`
	Criterion string `json:"criterion"`
	Message   string `json:"message"`
	File      string `json:"file,omitempty"`
	Severity  string `json:"severity"`
}

type CoverageGateBlocker struct {
	Kind      string `json:"kind"`
	Criterion string `json:"criterion"`
	Message   string `json:"message"`
	File      string `json:"file,omitempty"`
}

type ExecutionLaneEventRef struct {
	Lane      string `json:"lane"`
	Event     string `json:"event"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp,omitempty"`
}

type PlanExecuteProgressRef struct {
	Phase   string `json:"phase"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Updated string `json:"updated,omitempty"`
}

type VerificationRef struct {
	Status   string   `json:"status"`
	Commands []string `json:"commands"`
}

type EvidenceBundleRef struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type ExecutionFeedbackInput struct {
	EffortRoot             string
	WorkflowState          WorkflowState
	TDDLedger              TDDLedger
	LaneHistory            []ExecutionLaneEventRef
	PlanProgress           PlanExecuteProgressRef
	ProductEvidence        EvidenceBundleRef
	ReleaseEvidence        EvidenceBundleRef
	ChangedPaths           []string
	Verification           VerificationRef
	BriefPath              string
	TDDObligationRefs      []string
	ImplementationTaskRefs []string
}

type ExecutionFeedbackFinding struct {
	Kind     string `json:"kind"`
	Field    string `json:"field"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type ExecutionFeedbackBlocker struct {
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	NextValid string `json:"next_valid_action"`
}

type ExecutionFeedbackSummary struct {
	Status          string                     `json:"status"`
	EffortID        string                     `json:"effort_id"`
	TaskID          string                     `json:"task_id"`
	StateRevision   int                        `json:"state_revision,omitempty"`
	CurrentPhase    string                     `json:"current_phase"`
	CurrentState    string                     `json:"current_state"`
	LaneHistory     []ExecutionLaneEventRef    `json:"lane_history"`
	PlanPhase       string                     `json:"plan_phase"`
	PlanState       string                     `json:"plan_state"`
	PlanStatus      string                     `json:"plan_status"`
	ProductEvidence EvidenceBundleRef          `json:"product_evidence"`
	ReleaseEvidence EvidenceBundleRef          `json:"release_evidence"`
	BriefPath       string                     `json:"brief_path"`
	ChangedPaths    []string                   `json:"changed_paths"`
	Verification    VerificationRef            `json:"verification"`
	TDDObligations  []string                   `json:"tdd_obligations"`
	Findings        []ExecutionFeedbackFinding `json:"findings"`
	Blockers        []ExecutionFeedbackBlocker `json:"blockers"`
	NextValidAction string                     `json:"next_valid_action"`
}

type ExecutionFeedbackReconciliationInput struct {
	WorkflowState        WorkflowState
	KnownTDDObligations  []string
	Summary              ExecutionFeedbackSummary
	ReconciledAt         string
	SummaryReferencePath string
	ReconciledOutputPath string
}

type ExecutionFeedbackReconciliationBlocker struct {
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	NextValid string `json:"next_valid_action"`
}

type ExecutionFeedbackEvidenceLinkSummary struct {
	ProductEvidenceID        string   `json:"product_evidence_id"`
	ReleaseEvidenceID        string   `json:"release_evidence_id"`
	LinkedArtifactPathKeys   []string `json:"linked_artifact_path_keys"`
	LinkedArtifactPathCount  int      `json:"linked_artifact_path_count"`
	ChangedPathCount         int      `json:"changed_path_count"`
	TDDObligationCount       int      `json:"tdd_obligation_count"`
	VerificationCommandCount int      `json:"verification_command_count"`
}

type ExecutionFeedbackReconciliationResult struct {
	Status          string                                   `json:"status"`
	WorkflowState   WorkflowState                            `json:"workflow_state"`
	EvidenceLinks   ExecutionFeedbackEvidenceLinkSummary     `json:"evidence_links"`
	Findings        []ExecutionFeedbackFinding               `json:"findings"`
	Blockers        []ExecutionFeedbackReconciliationBlocker `json:"blockers"`
	NextValidAction string                                   `json:"next_valid_action"`
}

type BriefGenerationInput struct {
	EffortRoot            string
	ArtifactRootPath      string
	WorkflowState         WorkflowState
	PRD                   PRDDocument
	ADRIndex              ADRIndex
	ADRs                  []ParsedADR
	TDD                   TDDLedger
	Traceability          TraceabilityMatrix
	ImplementationRecords []ImplementationLedgerRecord
	CoverageEvaluation    CoverageGateEvaluation
	ExistingWorkspacePath map[string]bool
}

type BriefGenerationResult struct {
	Status               string            `json:"status"`
	FilesToModify        []string          `json:"files_to_modify"`
	FileActions          map[string]string `json:"file_actions,omitempty"`
	FilesToReadFirst     []string          `json:"files_to_read_first"`
	VerifiedCurrentState []string          `json:"verified_current_state"`
	RequiredBehavior     []string          `json:"required_behavior"`
	ImplementationShape  []string          `json:"implementation_shape,omitempty"`
	Constraints          []string          `json:"constraints"`
	Tests                []string          `json:"tests"`
	Acceptance           []string          `json:"acceptance"`
	Decisions            []string          `json:"decisions"`
	Blockers             []string          `json:"blockers"`
	RenderedBrief        string            `json:"rendered_brief"`
}

type ProductWorkflowMetrics struct {
	CurrentPhase          string         `json:"current_phase"`
	CurrentState          string         `json:"current_state"`
	StateRevision         int            `json:"state_revision"`
	NextValidActionCount  int            `json:"next_valid_action_count"`
	BlockerCount          int            `json:"blocker_count"`
	OpenObligationCount   int            `json:"open_obligation_count"`
	GateStatusCounts      map[string]int `json:"gate_status_counts"`
	TDDObligationTotal    int            `json:"tdd_obligation_total"`
	TDDStatusCounts       map[string]int `json:"tdd_status_counts"`
	TDDTypeCounts         map[string]int `json:"tdd_type_counts"`
	LedgerRecordCount     int            `json:"ledger_record_count"`
	LedgerAtomRecordCount int            `json:"ledger_atom_record_count"`
	LedgerTypeCounts      map[string]int `json:"ledger_type_counts"`
	ReportTypeCounts      map[string]int `json:"report_type_counts"`
	ReportStatusCounts    map[string]int `json:"report_status_counts"`
	ArtifactPathCount     int            `json:"artifact_path_count"`
	EvidenceCardCount     int            `json:"evidence_card_count"`
	TraceabilityRowCount  int            `json:"traceability_row_count"`
	PRDRequirementCount   int            `json:"prd_requirement_count"`
	ADRCount              int            `json:"adr_count"`
	EvidenceLinkCounts    map[string]int `json:"evidence_link_counts"`
}

