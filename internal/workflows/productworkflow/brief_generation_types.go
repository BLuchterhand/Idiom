package productworkflow

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
	PromotionEvents       []BehavioralCoveragePromotionEvent
	CoverageEvaluation    CoverageGateEvaluation
	ExistingWorkspacePath map[string]bool
	adrGeneration         ADRGenerationToken
	tddGeneration         tddGenerationToken
	productEventsPath     string
	productEventsBody     []byte
	productEventsLoaded   bool
}

type BriefGenerationResult struct {
	Status               ProductWorkflowStatus `json:"status"`
	FilesToModify        []string              `json:"files_to_modify"`
	FileActions          map[string]string     `json:"file_actions,omitempty"`
	FilesToReadFirst     []string              `json:"files_to_read_first"`
	VerifiedCurrentState []string              `json:"verified_current_state"`
	RequiredBehavior     []string              `json:"required_behavior"`
	ImplementationShape  []string              `json:"implementation_shape,omitempty"`
	Constraints          []string              `json:"constraints"`
	Tests                []string              `json:"tests"`
	Acceptance           []string              `json:"acceptance"`
	Decisions            []string              `json:"decisions"`
	Blockers             []string              `json:"blockers"`
	RenderedBrief        string                `json:"rendered_brief"`
}
