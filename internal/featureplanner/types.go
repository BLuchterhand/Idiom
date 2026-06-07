package featureplanner

import (
	"encoding/json"
	"fmt"
	"strings"
)

const SchemaVersion = "idiom.feature_planner.state.v1"
const (
	FeatureStateSchemaVersion    = "idiom.feature_state.v1"
	FeaturePlanSchemaVersion     = "idiom.feature_plan.v2"
	FeatureAtomSchemaVersion     = "idiom.feature_atom.v1"
	PlannerQuestionSchemaVersion = "idiom.feature_planner_question.v1"
	FeatureEventSchemaVersion    = "idiom.feature_event.v1"
	DefaultFeatureStateName      = "feature_state.json"
	DefaultFeaturePlanName       = "feature_plan.json"
	DefaultEventLogName          = "events.jsonl"
	InvariantDispatchAuthority   = "feature.dispatch_authority"
)

const (
	FeatureStateDraftingPlan        = "drafting-plan"
	FeatureStatePlanning            = "planning"
	FeatureStatePlanningRepair      = "planning-repair"
	FeatureStatePlanningQuestion    = "planning-question-open"
	FeatureStatePlanningStuck       = "planning-stuck"
	FeatureStatePlanReview          = "plan-review"
	FeatureStateAwaitingApproval    = "awaiting-approval"
	FeatureStateAtomizing           = "atomizing"
	FeatureStateReplanning          = "replanning"
	FeatureStateReady               = "ready"
	FeatureStateDispatching         = "dispatching"
	FeatureStateVerifying           = "verifying"
	FeatureStateReviewing           = "reviewing"
	FeatureStateReconciling         = "reconciling"
	FeatureStateNeedsContextRefresh = "needs-context-refresh"
	FeatureStateRecoveryOpen        = "recovery-open"
	FeatureStateBlocked             = "blocked"
	FeatureStateReleaseReview       = "release-review"
	FeatureStateReleased            = "released"
	FeatureStateClosedWithRisk      = "closed-with-risk"
	FeatureStateCanceled            = "canceled"
	FeatureStateInterrupted         = "interrupted"
	FeatureStateComplete            = "complete"
	FeatureStateFailed              = "failed"
	FeatureStateSourceSatisfied     = "source-satisfied"
	FeatureStateMalformedOutput     = "malformed-structured-output"
	FeatureStateStaleEditAnchor     = "stale-edit-anchor"
)

const (
	AtomStatePlanned             = "planned"
	AtomStateQueued              = "queued"
	AtomStateDispatchable        = "dispatchable"
	AtomStateDispatching         = "dispatching"
	AtomStateImplementing        = "implementing"
	AtomStateQuestionForPlanner  = "question-for-planner"
	AtomStateQuestionForOperator = "question-for-operator"
	AtomStateSourceSatisfied     = "source-satisfied"
	AtomStateMalformedOutput     = "malformed-structured-output"
	AtomStateStaleEditAnchor     = "stale-edit-anchor"
	AtomStateVerifying           = "verifying"
	AtomStateReviewing           = "reviewing"
	AtomStateNeedsContextRefresh = "needs-context-refresh"
	AtomStateComplete            = "complete"
	AtomStateStaleNeedsRebase    = "stale-needs-rebase"
	AtomStateBlockedDirtyFiles   = "blocked-dirty-files"
	AtomStateMissingScope        = "missing-scope"
	AtomStateInvalidScope        = "invalid-scope"
	AtomStateFailedRefineDraft   = "failed-refine-draft"
	AtomStateRecoveryOpen        = "recovery-open"
	AtomStateBlocked             = "blocked"
	AtomStateSkipped             = "skipped"
	AtomStateFailed              = "failed"
	AtomStateCanceled            = "canceled"
	AtomStateInterrupted         = "interrupted"
	AtomStateBlockedMissingTDD   = "blocked-missing-tdd-baseline"
)

const (
	ToolSubmitFeaturePlan   = "submit_feature_plan"
	ToolAskPlannerQuestion  = "ask_planner_question"
	ToolQuestionForPlanner  = "question_for_planner"
	ToolPlanExecuteComplete = "plan_execute_completed"
)

const (
	EventProductSourceSatisfied = "idiom.feature_planner.product_source_satisfied"
)

type FeatureState struct {
	SchemaVersion           string                    `json:"schema_version"`
	FeatureID               string                    `json:"feature_id,omitempty"`
	Title                   string                    `json:"title,omitempty"`
	Revision                int                       `json:"revision,omitempty"`
	WorkspaceFingerprint    string                    `json:"workspace_fingerprint,omitempty"`
	GitHead                 string                    `json:"git_head,omitempty"`
	ContextIndexID          string                    `json:"context_index_id,omitempty"`
	ContextFreshness        string                    `json:"context_freshness,omitempty"`
	PlanPath                string                    `json:"plan_path,omitempty"`
	AtomLedgerPath          string                    `json:"atom_ledger_path,omitempty"`
	EventsPath              string                    `json:"events_path,omitempty"`
	ReleaseContractPath     string                    `json:"release_contract_path,omitempty"`
	PlannerRevision         int                       `json:"planner_revision,omitempty"`
	EffortID                string                    `json:"effort_id,omitempty"`
	EffortRoot              string                    `json:"effort_root,omitempty"`
	SessionID               string                    `json:"session_id"`
	CreatedAt               string                    `json:"created_at"`
	UpdatedAt               string                    `json:"updated_at"`
	PromptSHA256            string                    `json:"prompt_sha256,omitempty"`
	PromptFile              string                    `json:"prompt_file,omitempty"`
	Status                  string                    `json:"status"`
	PlanningLoop            PlanningLoop              `json:"planning_loop"`
	Plan                    FeaturePlan               `json:"plan,omitempty"`
	Atoms                   []FeatureAtomState        `json:"atoms,omitempty"`
	TDDPreExecutionEvidence []TDDPreExecutionEvidence `json:"tdd_pre_execution_evidence,omitempty"`
	OpenQuestions           []PlannerQuestion         `json:"open_questions,omitempty"`
	ActiveRuns              []ActiveRun               `json:"active_runs,omitempty"`
	Leases                  []Lease                   `json:"leases,omitempty"`
	Continuation            Continuation              `json:"continuation"`
	OpenRecoveryPackets     []string                  `json:"open_recovery_packets,omitempty"`
	OpenObligations         []string                  `json:"open_obligations,omitempty"`
	DerivedNextValidActions []string                  `json:"derived_next_valid_actions,omitempty"`
	Blockers                []string                  `json:"blockers,omitempty"`
	LastError               string                    `json:"last_error,omitempty"`
}

type PlanningLoop struct {
	Iteration           int      `json:"iteration"`
	MaxIterations       int      `json:"max_iterations"`
	State               string   `json:"state"`
	LastPlanRevision    int      `json:"last_plan_revision,omitempty"`
	OpenQuestionIDs     []string `json:"open_question_ids,omitempty"`
	StuckReason         string   `json:"stuck_reason,omitempty"`
	LastProgressEventID string   `json:"last_progress_event_id,omitempty"`
}

type Continuation struct {
	NextAtomID     string   `json:"next_atom_id,omitempty"`
	ResumeAction   string   `json:"resume_action,omitempty"`
	ResumeAtomID   string   `json:"resume_atom_id,omitempty"`
	ResumeEventID  string   `json:"resume_event_id,omitempty"`
	ResumeReason   string   `json:"resume_reason,omitempty"`
	CompletedAtoms []string `json:"completed_atoms,omitempty"`
	BlockedAtoms   []string `json:"blocked_atoms,omitempty"`
}

type FeaturePlan struct {
	SchemaVersion      string              `json:"schema_version,omitempty"`
	FeatureID          string              `json:"feature_id,omitempty"`
	Tool               string              `json:"tool,omitempty"`
	SourceTask         SourceTask          `json:"source_task,omitempty"`
	Context            PlanContext         `json:"context,omitempty"`
	Summary            string              `json:"summary"`
	TopLevelScope      TopLevelScope       `json:"top_level_scope"`
	SDDContract        SDDContract         `json:"sdd_contract"`
	TDDContract        TDDContract         `json:"tdd_contract"`
	Atoms              []FeatureAtomPlan   `json:"atoms"`
	CoverageGateResult *CoverageGateResult `json:"coverage_gate_result,omitempty"`
}

type SourceTask struct {
	Kind    string `json:"kind,omitempty"`
	Path    string `json:"path,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type PlanContext struct {
	PackID         string   `json:"pack_id,omitempty"`
	IndexID        string   `json:"index_id,omitempty"`
	Freshness      string   `json:"freshness,omitempty"`
	UnresolvedGaps []string `json:"unresolved_gaps,omitempty"`
}

type TopLevelScope struct {
	Objective       string   `json:"objective"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
	NonGoals        []string `json:"non_goals,omitempty"`
}

type SDDContract struct {
	Refs               []string `json:"refs,omitempty"`                 // legacy — treated as acceptance criteria in v2
	Acceptance         []string `json:"acceptance,omitempty"`           // acceptance criteria strings
	PRDRequirementRefs []string `json:"prd_requirement_refs,omitempty"` // PRD requirement IDs
	ADRRefs            []string `json:"adr_refs,omitempty"`             // ADR IDs
}

func (contract *SDDContract) UnmarshalJSON(data []byte) error {
	var raw struct {
		Refs               []string        `json:"refs,omitempty"`
		Acceptance         json.RawMessage `json:"acceptance,omitempty"`
		PRDRequirementRefs []string        `json:"prd_requirement_refs,omitempty"`
		ADRRefs            []string        `json:"adr_refs,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	acceptance, err := decodeAcceptanceList(raw.Acceptance)
	if err != nil {
		return err
	}
	contract.Refs = raw.Refs
	contract.Acceptance = acceptance
	contract.PRDRequirementRefs = raw.PRDRequirementRefs
	contract.ADRRefs = raw.ADRRefs
	return nil
}

func decodeAcceptanceList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return nonEmptyFeaturePlannerStrings(list), nil
	}
	var scalar string
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return splitFeaturePlannerAcceptance(scalar), nil
	}
	return nil, fmt.Errorf("sdd_contract.acceptance must be an array of strings or a scalar acceptance string")
}

func splitFeaturePlannerAcceptance(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func nonEmptyFeaturePlannerStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

type TDDContract struct {
	CoverageTier                 string          `json:"coverage_tier"`
	RequiredBeforeImplementation bool            `json:"required_before_implementation"`
	Obligations                  []TDDObligation `json:"obligations"`
	CoverageGateEventID          string          `json:"coverage_gate_event_id,omitempty"` // plan-level binding to upstream coverage gate
}

type TDDObligation struct {
	ID             string `json:"id"`
	Behavior       string `json:"behavior"`
	Command        string `json:"command"`
	RequiredBefore string `json:"required_before"`
}

func (obligation *TDDObligation) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID             string          `json:"id"`
		Behavior       string          `json:"behavior"`
		Command        string          `json:"command"`
		RequiredBefore json.RawMessage `json:"required_before"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	requiredBefore, err := decodeRequiredBefore(raw.RequiredBefore)
	if err != nil {
		return err
	}
	obligation.ID = raw.ID
	obligation.Behavior = raw.Behavior
	obligation.Command = raw.Command
	obligation.RequiredBefore = requiredBefore
	return nil
}

func decodeRequiredBefore(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var scalar string
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return scalar, nil
	}
	var singleton []string
	if err := json.Unmarshal(raw, &singleton); err == nil && len(singleton) == 1 {
		return singleton[0], nil
	}
	return "", fmt.Errorf("required_before must be the scalar string \"implementation\"")
}

// CoverageGateResult carries the pass/fail status from the upstream
// product workflow coverage gate. It is a plain data struct with no
// import of productworkflow.
type CoverageGateResult struct {
	Status       string `json:"status"`        // "pass" or "blocked"
	EffortID     string `json:"effort_id"`     // product workflow effort identity
	CoverageTier string `json:"coverage_tier"` // coverage tier at time of evaluation
	CheckedAt    string `json:"checked_at"`    // ISO 8601 timestamp
}

type FeatureAtomPlan struct {
	SchemaVersion          string             `json:"schema_version,omitempty"`
	FeatureID              string             `json:"feature_id,omitempty"`
	ID                     string             `json:"id"`
	AtomID                 string             `json:"atom_id,omitempty"`
	Title                  string             `json:"title"`
	Summary                string             `json:"summary"`
	Dependencies           []string           `json:"dependencies,omitempty"`
	RequiredEditPaths      []string           `json:"required_edit_paths,omitempty"`
	AllowedEditPaths       []string           `json:"allowed_edit_paths,omitempty"`
	EvidencePaths          []string           `json:"evidence_paths,omitempty"`
	ForbiddenPaths         []string           `json:"forbidden_paths,omitempty"`
	SDDRefs                []string           `json:"sdd_refs,omitempty"`
	EditSetObligations     []string           `json:"edit_set_obligations,omitempty"`
	TDDObligations         []string           `json:"tdd_obligations,omitempty"`
	RequiredTests          []string           `json:"required_tests,omitempty"`
	TestFirst              TestFirstPlan      `json:"test_first"`
	PlannedTDDSnapshot     PlannedTDDSnapshot `json:"planned_tdd_snapshot,omitempty"`
	Prompt                 string             `json:"prompt,omitempty"`
	RecoveryPolicyRef      string             `json:"recovery_policy_ref,omitempty"`
	ImplementationRequired bool               `json:"implementation_required_after_characterization,omitempty"`
	RuntimeEnvelope        RuntimeEnvelope    `json:"runtime_envelope,omitempty"`
	RollbackNote           string             `json:"rollback_note,omitempty"`
}

type TestFirstPlan struct {
	ObligationRefs             []string `json:"obligation_refs,omitempty"`
	Commands                   []string `json:"commands,omitempty"`
	BaselineRequired           bool     `json:"baseline_required"`
	BaselineStatus             string   `json:"baseline_status,omitempty"`
	TestArtifactPaths          []string `json:"test_artifact_paths,omitempty"`
	PreExecutionEventIDs       []string `json:"pre_execution_event_ids,omitempty"`
	ExpectedPreExecutionResult string   `json:"expected_pre_execution_result,omitempty"`
	CoverageGateEventID        string   `json:"coverage_gate_event_id,omitempty"`
}

type PlannedTDDSnapshot struct {
	SnapshotID                   string   `json:"snapshot_id,omitempty"`
	SnapshotDigest               string   `json:"snapshot_digest,omitempty"`
	ObligationIDs                []string `json:"obligation_ids,omitempty"`
	TestCommands                 []string `json:"test_commands,omitempty"`
	TestArtifactPaths            []string `json:"test_artifact_paths,omitempty"`
	ExpectedPreExecutionResults  []string `json:"expected_pre_execution_results,omitempty"`
	CreatedBeforeDispatchEventID string   `json:"created_before_dispatch_event_id,omitempty"`
}

type TDDPreExecutionEvidence struct {
	EventID                    string   `json:"event_id"`
	AtomID                     string   `json:"atom_id"`
	ObligationRefs             []string `json:"obligation_refs,omitempty"`
	Commands                   []string `json:"commands,omitempty"`
	TestArtifactPaths          []string `json:"test_artifact_paths,omitempty"`
	ExpectedPreExecutionResult string   `json:"expected_pre_execution_result,omitempty"`
	CoverageGateEventID        string   `json:"coverage_gate_event_id,omitempty"`
	SnapshotID                 string   `json:"snapshot_id,omitempty"`
	SnapshotDigest             string   `json:"snapshot_digest,omitempty"`
	CreatedAt                  string   `json:"created_at,omitempty"`
}

type RuntimeEnvelope struct {
	ContractID       string   `json:"contract_id,omitempty"`
	ProtocolHash     string   `json:"protocol_hash,omitempty"`
	QueueMode        string   `json:"queue_mode,omitempty"`
	QueueAdmissionID string   `json:"queue_admission_id,omitempty"`
	ToolTransport    string   `json:"tool_transport,omitempty"`
	ParserContract   string   `json:"parser_contract,omitempty"`
	ResponseFormat   string   `json:"response_format,omitempty"`
	VisibleTools     []string `json:"visible_tools,omitempty"`
	CallableTools    []string `json:"callable_tools,omitempty"`
}

type FeatureAtomState struct {
	FeatureAtomPlan
	State        string   `json:"state"`
	Attempt      int      `json:"attempt"`
	LastRunID    string   `json:"last_run_id,omitempty"`
	LastQuestion string   `json:"last_question,omitempty"`
	Blockers     []string `json:"blockers,omitempty"`
	CompletedAt  string   `json:"completed_at,omitempty"`
}

type PlannerQuestion struct {
	SchemaVersion       string   `json:"schema_version,omitempty"`
	QuestionID          string   `json:"question_id,omitempty"`
	FeatureID           string   `json:"feature_id,omitempty"`
	ID                  string   `json:"id"`
	AtomID              string   `json:"atom_id,omitempty"`
	LaneSessionID       string   `json:"lane_session_id,omitempty"`
	PlannerRevisionSeen int      `json:"planner_revision_seen,omitempty"`
	QuestionKind        string   `json:"question_kind,omitempty"`
	QuestionSummary     string   `json:"question_summary"`
	BlockingClaim       string   `json:"blocking_claim,omitempty"`
	RequestedDecision   string   `json:"requested_decision,omitempty"`
	AuthorityRequired   string   `json:"authority_required,omitempty"`
	AnswerSourceKind    string   `json:"answer_source_kind,omitempty"`
	RawQuestionRef      string   `json:"raw_question_ref,omitempty"`
	BlockingContractIDs []string `json:"blocking_contract_ids,omitempty"`
	AffectedPaths       []string `json:"affected_paths,omitempty"`
	EvidenceRefs        []string `json:"evidence_refs,omitempty"`
	AskedAt             string   `json:"asked_at,omitempty"`
	RequiredBefore      string   `json:"required_before,omitempty"`
	Status              string   `json:"status"`
	Answer              string   `json:"answer,omitempty"`
}

type ActiveRun struct {
	RunID                   string   `json:"run_id"`
	AtomID                  string   `json:"atom_id"`
	Actor                   string   `json:"actor,omitempty"`
	LaneSessionID           string   `json:"lane_session_id,omitempty"`
	LaneFamily              string   `json:"lane_family,omitempty"`
	InvocationContractID    string   `json:"invocation_contract_id,omitempty"`
	RuntimeEnvelopeID       string   `json:"runtime_envelope_id,omitempty"`
	PlanExecuteProgressPath string   `json:"plan_execute_progress_path,omitempty"`
	ProgressRevision        int      `json:"progress_revision,omitempty"`
	ProgressSHA256          string   `json:"progress_sha256,omitempty"`
	ResultSource            string   `json:"result_source,omitempty"`
	State                   string   `json:"state"`
	StartedAt               string   `json:"started_at"`
	UpdatedAt               string   `json:"updated_at,omitempty"`
	HeartbeatAt             string   `json:"heartbeat_at,omitempty"`
	HeartbeatSource         string   `json:"heartbeat_source,omitempty"`
	EvidenceDeltaSinceEvent []string `json:"evidence_delta_since_event,omitempty"`
	OwnedLeases             []Lease  `json:"owned_leases,omitempty"`
}

type Lease struct {
	LeaseID         string `json:"lease_id"`
	AtomID          string `json:"atom_id,omitempty"`
	Owner           string `json:"owner,omitempty"`
	Fence           int    `json:"fence,omitempty"`
	State           string `json:"state,omitempty"`
	LeaseKind       string `json:"lease_kind,omitempty"`
	Scope           string `json:"scope,omitempty"`
	OwnerFeatureID  string `json:"owner_feature_id,omitempty"`
	OwnerAtomID     string `json:"owner_atom_id,omitempty"`
	OwnerSession    string `json:"owner_session,omitempty"`
	FencingRevision int    `json:"fencing_revision,omitempty"`
	AcquiredAt      string `json:"acquired_at"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	ReleasedAt      string `json:"released_at,omitempty"`
	ReleaseReason   string `json:"release_reason,omitempty"`
}

type Event struct {
	SchemaVersion         string           `json:"schema_version,omitempty"`
	EventID               string           `json:"event_id,omitempty"`
	IdempotencyKey        string           `json:"idempotency_key,omitempty"`
	Type                  string           `json:"type"`
	SessionID             string           `json:"session_id"`
	FeatureID             string           `json:"feature_id,omitempty"`
	FeatureRevisionBefore int              `json:"feature_revision_before,omitempty"`
	FeatureRevisionAfter  int              `json:"feature_revision_after,omitempty"`
	FeatureFrom           string           `json:"feature_from,omitempty"`
	FeatureTo             string           `json:"feature_to,omitempty"`
	AtomID                string           `json:"atom_id,omitempty"`
	AtomFrom              string           `json:"atom_from,omitempty"`
	AtomTo                string           `json:"atom_to,omitempty"`
	AtomDeltas            []AtomDelta      `json:"atom_deltas,omitempty"`
	Action                string           `json:"action,omitempty"`
	Actor                 string           `json:"actor,omitempty"`
	ContractID            string           `json:"contract_id,omitempty"`
	InvariantID           string           `json:"invariant_id,omitempty"`
	LeaseDeltas           []LeaseDelta     `json:"lease_deltas,omitempty"`
	ActiveRunDeltas       []ActiveRunDelta `json:"active_run_deltas,omitempty"`
	ArtifactDeltas        []ArtifactDelta  `json:"artifact_deltas,omitempty"`
	CreatedAt             string           `json:"created_at,omitempty"`
	Message               string           `json:"message,omitempty"`
	Fields                map[string]any   `json:"fields,omitempty"`
}

type AtomDelta struct {
	AtomID   string `json:"atom_id"`
	AtomFrom string `json:"atom_from,omitempty"`
	AtomTo   string `json:"atom_to,omitempty"`
	Op       string `json:"op"`
}

type LeaseDelta struct {
	Op                    string `json:"op"`
	LeaseID               string `json:"lease_id"`
	BeforeFencingRevision int    `json:"before_fencing_revision,omitempty"`
	AfterFencingRevision  int    `json:"after_fencing_revision,omitempty"`
	ResultingState        string `json:"resulting_state,omitempty"`
	LeaseKind             string `json:"lease_kind,omitempty"`
	Scope                 string `json:"scope,omitempty"`
	OwnerFeatureID        string `json:"owner_feature_id,omitempty"`
	OwnerAtomID           string `json:"owner_atom_id,omitempty"`
	OwnerSession          string `json:"owner_session,omitempty"`
	AcquiredAt            string `json:"acquired_at,omitempty"`
	ExpiresAt             string `json:"expires_at,omitempty"`
	ReleasedAt            string `json:"released_at,omitempty"`
	ReleaseReason         string `json:"release_reason,omitempty"`
}

type ActiveRunDelta struct {
	Op                      string   `json:"op"`
	AtomID                  string   `json:"atom_id"`
	LaneSessionID           string   `json:"lane_session_id"`
	LaneFamily              string   `json:"lane_family,omitempty"`
	InvocationContractID    string   `json:"invocation_contract_id,omitempty"`
	RuntimeEnvelopeID       string   `json:"runtime_envelope_id,omitempty"`
	ProgressPath            string   `json:"progress_path,omitempty"`
	BeforeProgressRevision  int      `json:"before_progress_revision,omitempty"`
	AfterProgressRevision   int      `json:"after_progress_revision,omitempty"`
	BeforeProgressSHA256    string   `json:"before_progress_sha256,omitempty"`
	AfterProgressSHA256     string   `json:"after_progress_sha256,omitempty"`
	BeforeState             string   `json:"before_state,omitempty"`
	AfterState              string   `json:"after_state,omitempty"`
	ResultSource            string   `json:"result_source,omitempty"`
	StartedAt               string   `json:"started_at,omitempty"`
	HeartbeatAt             string   `json:"heartbeat_at,omitempty"`
	HeartbeatSource         string   `json:"heartbeat_source,omitempty"`
	EvidenceDeltaSinceEvent []string `json:"evidence_delta_since_event,omitempty"`
	OwnedLeaseIDs           []string `json:"owned_lease_ids,omitempty"`
}

type ArtifactDelta struct {
	Op           string `json:"op"`
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
	ArtifactKind string `json:"artifact_kind"`
}
