package productworkflow

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
	Status          ProductWorkflowStatus      `json:"status"`
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
	Status          ProductWorkflowStatus                    `json:"status"`
	WorkflowState   WorkflowState                            `json:"workflow_state"`
	EvidenceLinks   ExecutionFeedbackEvidenceLinkSummary     `json:"evidence_links"`
	Findings        []ExecutionFeedbackFinding               `json:"findings"`
	Blockers        []ExecutionFeedbackReconciliationBlocker `json:"blockers"`
	NextValidAction string                                   `json:"next_valid_action"`
}
