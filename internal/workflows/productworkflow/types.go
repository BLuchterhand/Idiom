package productworkflow

import (
	"encoding/json"
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

const (
	WorkflowStateFileName = "workflow_state.json"
)

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
	SchemaVersion           string                           `json:"schema_version"`
	EffortID                string                           `json:"effort_id"`
	TaskID                  string                           `json:"task_id"`
	Title                   string                           `json:"title"`
	CreatedAt               string                           `json:"created_at"`
	UpdatedAt               string                           `json:"updated_at"`
	StateRevision           int                              `json:"state_revision"`
	LastEventID             string                           `json:"last_event_id"`
	WorkspaceFingerprint    string                           `json:"workspace_fingerprint"`
	StatusBucket            string                           `json:"status_bucket"`
	CurrentPhase            string                           `json:"current_phase"`
	CurrentState            string                           `json:"current_state"`
	ArtifactPaths           map[string]string                `json:"artifact_paths"`
	Context                 WorkflowContext                  `json:"context"`
	Coverage                CoverageState                    `json:"coverage"`
	OpenObligations         []string                         `json:"open_obligations"`
	OpenQuestions           []string                         `json:"open_questions"`
	Blockers                []string                         `json:"blockers"`
	Privacy                 PrivacyState                     `json:"privacy"`
	SessionIDs              map[string]string                `json:"session_ids"`
	FolderPath              string                           `json:"folder_path"`
	PriorFolderPaths        []string                         `json:"prior_folder_paths"`
	LastReconciliationAt    string                           `json:"last_reconciliation_at"`
	ReconciliationMismatch  bool                             `json:"reconciliation_mismatch"`
	NextValidActions        []string                         `json:"next_valid_actions"`
	DurabilityLevel         string                           `json:"durability_level"`
	SourceSatisfiedDelivery *SourceSatisfiedDeliveryCloseout `json:"source_satisfied_delivery,omitempty"`
}

type SourceSatisfiedDeliveryCloseout struct {
	EffortID         string                                    `json:"effort_id"`
	TaskID           string                                    `json:"task_id"`
	Outcome          plancontract.PlanExecuteTerminalOutcome   `json:"outcome"`
	SessionID        string                                    `json:"session_id"`
	CompletionDigest string                                    `json:"completion_digest"`
	EvidencePaths    []string                                  `json:"evidence_paths"`
	Evidence         []plancontract.SourceSatisfiedEvidenceRef `json:"evidence"`
	RecordedAt       string                                    `json:"recorded_at"`
}

type WorkflowContext struct {
	ContextPackID   string            `json:"context_pack_id"`
	FreshnessState  string            `json:"freshness_state"`
	ReferencedPaths []string          `json:"referenced_paths"`
	PathAliases     map[string]string `json:"path_aliases,omitempty"`
	UnresolvedGaps  []UnresolvedGap   `json:"unresolved_gaps"`
	RefreshPolicy   string            `json:"refresh_policy"`
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
	Gate      string                `json:"gate"`
	Status    ProductWorkflowStatus `json:"status"`
	CheckedAt string                `json:"checked_at"`
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
	SchemaVersion      string                      `json:"schema_version"`
	EffortID           string                      `json:"effort_id"`
	TaskID             string                      `json:"task_id"`
	CoverageTier       string                      `json:"coverage_tier"`
	Coverage           TDDCoverage                 `json:"coverage"`
	BehavioralCoverage *BehavioralCoverageContract `json:"behavioral_coverage,omitempty"`
	Obligations        []TDDObligation             `json:"obligations"`
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
	StateMachinesTotal       int      `json:"state_machines_total"`
	StateMachinesCovered     int      `json:"state_machines_covered"`
	DecisionTablesTotal      int      `json:"decision_tables_total"`
	DecisionTablesEnumerated int      `json:"decision_tables_enumerated"`
	BoundaryValuesTotal      int      `json:"boundary_values_total"`
	BoundaryValuesTested     int      `json:"boundary_values_tested"`
	FaultTreeLeavesTotal     int      `json:"fault_tree_leaves_total"`
	FaultTreeLeavesTested    int      `json:"fault_tree_leaves_tested"`
	PostMortemIncidentsTotal int      `json:"post_mortem_incidents_total"`
	PostMortemCatchingTests  int      `json:"post_mortem_catching_tests"`
	FormalModelsTotal        int      `json:"formal_models_total"`
	FormalModelsVerified     int      `json:"formal_models_verified"`
	FuzzTargetsTotal         int      `json:"fuzz_targets_total"`
	FuzzTargetsCovered       int      `json:"fuzz_targets_covered"`
	RAGEvalsTotal            int      `json:"rag_evals_total"`
	RAGEvalsMeasured         int      `json:"rag_evals_measured"`
	SecurityCasesTotal       int      `json:"security_cases_total"`
	SecurityCasesTested      int      `json:"security_cases_tested"`
	PrivacyControlsTotal     int      `json:"privacy_controls_total"`
	PrivacyControlsTested    int      `json:"privacy_controls_tested"`
	ProviderContractsTotal   int      `json:"provider_contracts_total"`
	ProviderContractsTested  int      `json:"provider_contracts_tested"`
	ObservabilityTotal       int      `json:"observability_total"`
	ObservabilityTested      int      `json:"observability_tested"`
	PerformanceBoundsTotal   int      `json:"performance_bounds_total"`
	PerformanceBoundsTested  int      `json:"performance_bounds_tested"`
	E2EJourneysTotal         int      `json:"e2e_journeys_total"`
	E2EJourneysScripted      int      `json:"e2e_journeys_scripted"`
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

type ResearchCardsDocument struct {
	SchemaVersion string                     `json:"schema_version"`
	ResearcherID  string                     `json:"researcher_id"`
	Alignment     string                     `json:"alignment"`
	Cards         []EvidenceCard             `json:"cards"`
	Conflicts     []ResearchEvidenceConflict `json:"conflicts,omitempty"`
}

type ResearchEvidenceConflict struct {
	Kind          string   `json:"kind"`
	ClaimKey      string   `json:"claim_key"`
	CardIDs       []string `json:"card_ids"`
	ResearcherIDs []string `json:"researcher_ids"`
	Message       string   `json:"message"`
}

type ResearchEvidenceMergeResult struct {
	Cards         []EvidenceCard             `json:"cards"`
	MergedCardIDs []string                   `json:"merged_card_ids"`
	Conflicts     []ResearchEvidenceConflict `json:"conflicts"`
	Findings      []ProfileFinding           `json:"findings"`
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
	RecordType              ImplementationLedgerRecordType `json:"record_type"`
	TaskID                  string                         `json:"task_id"`
	EffortID                string                         `json:"effort_id"`
	Timestamp               string                         `json:"timestamp"`
	ComplexityTier          string                         `json:"complexity_tier,omitempty"`
	Phase                   string                         `json:"phase,omitempty"`
	ContextPackID           string                         `json:"context_pack_id,omitempty"`
	Implementor             *ImplementorAssignment         `json:"implementor,omitempty"`
	Reviewer                *ReviewerAssignment            `json:"reviewer,omitempty"`
	Scope                   *AtomScope                     `json:"scope,omitempty"`
	RequiredVerification    []string                       `json:"required_verification,omitempty"`
	ReportPaths             *ReportPaths                   `json:"report_paths,omitempty"`
	StandardImplementor     string                         `json:"standard_implementor,omitempty"`
	FallbackActor           string                         `json:"fallback_actor,omitempty"`
	Reason                  string                         `json:"reason,omitempty"`
	AllowedScope            string                         `json:"allowed_scope,omitempty"`
	CompensatingChecks      []string                       `json:"compensating_checks,omitempty"`
	Actor                   string                         `json:"actor,omitempty"`
	ImplementorID           string                         `json:"implementor_id,omitempty"`
	ReviewerID              string                         `json:"reviewer_id,omitempty"`
	ChangedPaths            []string                       `json:"changed_paths,omitempty"`
	CommitHash              string                         `json:"commit_hash,omitempty"`
	CodeChanges             bool                           `json:"code_changes,omitempty"`
	VerificationCommandsRun []string                       `json:"verification_commands_run,omitempty"`
	VerificationStatus      string                         `json:"verification_status,omitempty"`
	TDDObligationsSatisfied []string                       `json:"tdd_obligations_satisfied,omitempty"`
	Commands                []string                       `json:"commands,omitempty"`
	Status                  string                         `json:"status,omitempty"`
	Model                   string                         `json:"model,omitempty"`
	ReasoningEffort         string                         `json:"reasoning_effort,omitempty"`
	Decision                string                         `json:"decision,omitempty"`
	Findings                []ImplementationLedgerFinding  `json:"findings,omitempty"`
	State                   string                         `json:"state,omitempty"`
	CloseReason             string                         `json:"close_reason,omitempty"`
	OpenBlockers            []string                       `json:"open_blockers,omitempty"`
	Extra                   map[string]any                 `json:"-"`
}

type ImplementationLedgerFinding struct {
	Kind     string `json:"kind,omitempty"`
	Source   string `json:"source,omitempty"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message,omitempty"`
	Path     string `json:"path,omitempty"`
}

func (f *ImplementationLedgerFinding) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*f = ImplementationLedgerFinding{
			Kind:    "legacy_text",
			Message: strings.TrimSpace(text),
		}
		return nil
	}

	type finding ImplementationLedgerFinding
	var out finding
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*f = ImplementationLedgerFinding(out)
	return nil
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

type PRDDocument struct {
	FrontMatter  PRDFrontMatter
	Requirements []PRDRequirement
}

type PRDFrontMatter struct {
	SchemaVersion     string
	PRDID             string
	Status            string
	TaskID            string
	CreatedAt         string
	AcceptedAt        string
	AcceptedBy        string
	AcceptanceProfile string
}

type PRDRequirement struct {
	ID                   string
	Priority             string
	Statement            string
	EvidenceRefs         []string
	TDDObligations       []string
	OwnerAlignment       string
	Testability          string
	Metric               string
	OpenQuestions        []string
	OpenQuestionsPresent bool
}

type ADRIndex struct {
	SchemaVersion string          `json:"schema_version"`
	EffortID      string          `json:"effort_id"`
	ADRs          []ADRIndexEntry `json:"adrs"`
}

type ADRIndexEntry struct {
	ADRID        string   `json:"adr_id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Path         string   `json:"path"`
	CoverageTier string   `json:"coverage_tier"`
	PRDRefs      []string `json:"prd_refs"`
	CreatedAt    string   `json:"created_at"`
	Supersedes   []string `json:"supersedes,omitempty"`
	SupersededBy []string `json:"superseded_by,omitempty"`
}

type ResearchProfile struct {
	SchemaVersion string       `json:"schema_version"`
	Name          string       `json:"name"`
	ProfileID     string       `json:"profile_id,omitempty"`
	Researchers   []Researcher `json:"researchers"`
	MergePolicy   MergePolicy  `json:"merge_policy"`
}

type Researcher struct {
	ID                  string   `json:"id"`
	Alignment           string   `json:"alignment"`
	Question            string   `json:"question"`
	RequiredSources     []string `json:"required_sources"`
	AllowedCapabilities []string `json:"allowed_capabilities"`
	Output              string   `json:"output"`
}

type MergePolicy struct {
	Conflicts         string `json:"conflicts"`
	UnsupportedClaims string `json:"unsupported_claims"`
	StaleLocalContext string `json:"stale_local_context"`
}

type ResearchRunPlan struct {
	SelectedProfileID string            `json:"selected_profile_id"`
	SelectedProfile   string            `json:"selected_profile"`
	ProfileCount      int               `json:"profile_count"`
	RunCount          int               `json:"run_count"`
	ResearchOutputDir string            `json:"research_output_dir"`
	Runs              []ResearchRunSpec `json:"runs"`
	Findings          []ProfileFinding  `json:"findings"`
}

type ResearchRunSpec struct {
	ResearcherID        string                    `json:"researcher_id"`
	Alignment           string                    `json:"alignment"`
	Question            string                    `json:"question"`
	RequiredSources     []string                  `json:"required_sources"`
	AllowedCapabilities []string                  `json:"allowed_capabilities"`
	Output              string                    `json:"output"`
	OutputPath          string                    `json:"output_path"`
	OutputContract      ResearchRunOutputContract `json:"output_contract"`
}

type ResearchRunOutputContract struct {
	SchemaVersion string `json:"schema_version"`
}

type ResearchArtifactPromptPlan struct {
	SelectedProfileID string                          `json:"selected_profile_id"`
	SelectedProfile   string                          `json:"selected_profile"`
	ProfileCount      int                             `json:"profile_count"`
	RunCount          int                             `json:"run_count"`
	ResearchOutputDir string                          `json:"research_output_dir"`
	ResearchPromptDir string                          `json:"research_prompt_dir"`
	Runs              []ResearchArtifactPromptRunSpec `json:"runs"`
	Findings          []ProfileFinding                `json:"findings"`
}

type ResearchArtifactPromptRunSpec struct {
	ResearcherID        string                    `json:"researcher_id"`
	Alignment           string                    `json:"alignment"`
	Question            string                    `json:"question"`
	RequiredSources     []string                  `json:"required_sources"`
	AllowedCapabilities []string                  `json:"allowed_capabilities"`
	OutputPath          string                    `json:"output_path"`
	OutputContract      ResearchRunOutputContract `json:"output_contract"`
	PromptPath          string                    `json:"prompt_path"`
	EventsPath          string                    `json:"events_path"`
	ReportPath          string                    `json:"report_path"`
	Command             string                    `json:"command"`
}

type PRDDraftPromptPlan struct {
	EvidenceLedgerPath string                    `json:"evidence_ledger_path"`
	TDDLedgerPath      string                    `json:"tdd_ledger_path,omitempty"`
	PromptPath         string                    `json:"prompt_path"`
	PRDOutputPath      string                    `json:"prd_output_path"`
	EventsPath         string                    `json:"events_path"`
	ReportPath         string                    `json:"report_path"`
	Command            string                    `json:"command"`
	EvidenceCardCount  int                       `json:"evidence_card_count"`
	EvidenceCardIDs    []string                  `json:"evidence_card_ids"`
	EvidenceSummaries  []PRDEvidenceCardSummary  `json:"evidence_summaries"`
	TDDObligationIDs   []string                  `json:"tdd_obligation_ids,omitempty"`
	TDDObligations     []PRDTDDObligationSummary `json:"tdd_obligations,omitempty"`
	Findings           []ProfileFinding          `json:"findings"`
}

type PRDEvidenceCardSummary struct {
	CardID      string `json:"card_id"`
	Kind        string `json:"kind"`
	Confidence  string `json:"confidence"`
	Freshness   string `json:"freshness"`
	Claim       string `json:"claim"`
	Uncertainty string `json:"uncertainty"`
}

type PRDTDDObligationSummary struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Behavior string `json:"behavior"`
	Owner    string `json:"owner"`
	Command  string `json:"command"`
}

type ADRDraftPromptPlan struct {
	EvidenceLedgerPath    string                     `json:"evidence_ledger_path"`
	PRDInputPath          string                     `json:"prd_input_path"`
	PromptPath            string                     `json:"prompt_path"`
	ADROutputPath         string                     `json:"adr_output_path"`
	EventsPath            string                     `json:"events_path"`
	ReportPath            string                     `json:"report_path"`
	Command               string                     `json:"command"`
	EvidenceCardCount     int                        `json:"evidence_card_count"`
	EvidenceCardIDs       []string                   `json:"evidence_card_ids"`
	EvidenceSummaries     []ADREvidenceCardSummary   `json:"evidence_summaries"`
	PRDRequirementCount   int                        `json:"prd_requirement_count"`
	PRDRequirementIDs     []string                   `json:"prd_requirement_ids"`
	PRDRequirementSummary []ADRPRDRequirementSummary `json:"prd_requirement_summaries"`
	Findings              []ProfileFinding           `json:"findings"`
}

type TDDObligationSynthesisPlan struct {
	EffortID     string                     `json:"effort_id"`
	TaskID       string                     `json:"task_id"`
	CoverageTier string                     `json:"coverage_tier"`
	Obligations  []TDDObligationPlanItem    `json:"obligations"`
	Findings     []TDDObligationPlanFinding `json:"findings"`
}

type TDDObligationPlanItem struct {
	PRDRequirementRef      string   `json:"prd_requirement_ref"`
	ADRRef                 string   `json:"adr_ref"`
	ExistingObligationRefs []string `json:"existing_obligation_refs"`
	EvidenceRefs           []string `json:"evidence_refs,omitempty"`
	CoverageTier           string   `json:"coverage_tier"`
	SuggestedType          string   `json:"suggested_type"`
	OwnerPath              string   `json:"owner_path"`
	Command                string   `json:"command"`
	BlockerReason          string   `json:"blocker_reason,omitempty"`
}

type TDDObligationPlanFinding struct {
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	File     string `json:"file"`
	Severity string `json:"severity"`
}

type ADREvidenceCardSummary struct {
	CardID      string `json:"card_id"`
	Kind        string `json:"kind"`
	Confidence  string `json:"confidence"`
	Freshness   string `json:"freshness"`
	Claim       string `json:"claim"`
	Uncertainty string `json:"uncertainty"`
}

type ADRPRDRequirementSummary struct {
	ID                string   `json:"id"`
	Priority          string   `json:"priority"`
	Statement         string   `json:"statement"`
	Evidence          int      `json:"evidence_ref_count"`
	Obligations       int      `json:"tdd_obligation_count"`
	EvidenceRefs      []string `json:"evidence_ref_ids,omitempty"`
	TDDObligationRefs []string `json:"tdd_obligation_ids,omitempty"`
}

type ADRDocument struct {
	FrontMatter  ADRFrontMatter
	Sections     ADRSections
	BodyMarkdown string
}

type ADRSections struct {
	HasContext                   bool
	HasDecision                  bool
	HasAlternativesConsidered    bool
	HasEvidence                  bool
	HasConsequences              bool
	HasCompatibilityAndMigration bool
	HasTDDObligations            bool
	HasRollback                  bool
	HasOpenQuestions             bool
}

type ADRFrontMatter struct {
	SchemaVersion     string
	ADRID             string
	Status            string
	PRDRefs           []string
	EvidenceRefs      []string
	TDDObligations    []string
	CoverageTier      string
	AcceptedAt        string
	AcceptedBy        string
	AcceptanceProfile string
	Supersedes        []string
	SupersededBy      []string
}
