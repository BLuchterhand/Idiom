package featureplanner

import (
	"encoding/json"
	"fmt"
	"strings"

	plancontract "idiom/internal/contracts/plan"
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
	ActiveRunStateRunning = "running"
)

func ActiveRunStateAllowed(state string) bool {
	switch strings.TrimSpace(state) {
	case ActiveRunStateRunning:
		return true
	default:
		return false
	}
}

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
	PromptSummary           string                    `json:"prompt_summary,omitempty"`
	PromptFile              string                    `json:"prompt_file,omitempty"`
	Status                  string                    `json:"status"`
	PlanningLoop            PlanningLoop              `json:"planning_loop"`
	Plan                    FeaturePlan               `json:"plan,omitempty"`
	Atoms                   []FeatureAtomState        `json:"atoms,omitempty"`
	TDDPreExecutionEvidence []TDDPreExecutionEvidence `json:"tdd_pre_execution_evidence,omitempty"`
	VerificationClosures    []VerificationClosure     `json:"verification_closures,omitempty"`
	ReleaseReadiness        ReleaseReadinessDecision  `json:"release_readiness,omitempty"`
	AuthorityDeltas         []PlanAuthorityDelta      `json:"authority_deltas,omitempty"`
	RejectedPlanCandidates  []RejectedPlanCandidate   `json:"rejected_plan_candidates,omitempty"`
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

type RejectedPlanCandidate struct {
	Plan       FeaturePlan `json:"plan"`
	PlanHash   string      `json:"plan_hash"`
	Diagnostic string      `json:"diagnostic"`
	EventType  string      `json:"event_type"`
	RejectedAt string      `json:"rejected_at"`
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
	SchemaVersion      string                      `json:"schema_version,omitempty"`
	FeatureID          string                      `json:"feature_id,omitempty"`
	Tool               string                      `json:"tool,omitempty"`
	Artifact           FeaturePlanArtifact         `json:"artifact,omitempty"`
	Approval           FeaturePlanApproval         `json:"approval,omitempty"`
	SourceTask         SourceTask                  `json:"source_task,omitempty"`
	Context            PlanContext                 `json:"context,omitempty"`
	Summary            string                      `json:"summary"`
	TopLevelScope      TopLevelScope               `json:"top_level_scope"`
	SDDContract        SDDContract                 `json:"sdd_contract"`
	TDDContract        TDDContract                 `json:"tdd_contract"`
	Atoms              []FeatureAtomPlan           `json:"atoms"`
	CoverageGateResult *CoverageGateResult         `json:"coverage_gate_result,omitempty"`
	CoverageContract   *CoverageContractRef        `json:"coverage_contract,omitempty"`
	AcceptedCoverage   *AcceptedBehavioralCoverage `json:"accepted_coverage,omitempty"`
	// BehavioralCoverageDraft is provisional standalone planning evidence. Product
	// workflow context owns accepted coverage authority and supersedes this draft.
	BehavioralCoverageDraft *BehavioralCoverageDraft `json:"behavioral_coverage_draft,omitempty"`
}

type FeaturePlanArtifact struct {
	ArtifactID           string `json:"artifact_id,omitempty"`
	Revision             int    `json:"revision,omitempty"`
	Digest               string `json:"digest,omitempty"`
	WorkspaceFingerprint string `json:"workspace_fingerprint,omitempty"`
	CreatedAt            string `json:"created_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
}

type FeaturePlanApproval struct {
	Status               string `json:"status,omitempty"`
	Mode                 string `json:"mode,omitempty"`
	EventID              string `json:"event_id,omitempty"`
	Actor                string `json:"actor,omitempty"`
	Reason               string `json:"reason,omitempty"`
	ApprovedAt           string `json:"approved_at,omitempty"`
	PlanRevision         int    `json:"plan_revision,omitempty"`
	PlanDigest           string `json:"plan_digest,omitempty"`
	WorkspaceFingerprint string `json:"workspace_fingerprint,omitempty"`
}

type ReleaseReadinessStatus string

const (
	ReleaseReadinessStatusReady   ReleaseReadinessStatus = "ready"
	ReleaseReadinessStatusBlocked ReleaseReadinessStatus = "blocked"
)

func ReleaseReadinessStatusAllowed(status ReleaseReadinessStatus) bool {
	switch strings.TrimSpace(string(status)) {
	case string(ReleaseReadinessStatusReady), string(ReleaseReadinessStatusBlocked):
		return true
	default:
		return false
	}
}

type ReleaseReadinessDecision struct {
	Status                      ReleaseReadinessStatus `json:"status,omitempty"`
	EventID                     string                 `json:"event_id,omitempty"`
	PlanArtifactID              string                 `json:"plan_artifact_id,omitempty"`
	PlanRevision                int                    `json:"plan_revision,omitempty"`
	PlanDigest                  string                 `json:"plan_digest,omitempty"`
	WorkspaceFingerprint        string                 `json:"workspace_fingerprint,omitempty"`
	ApprovalEventID             string                 `json:"approval_event_id,omitempty"`
	VerificationClosureEventIDs []string               `json:"verification_closure_event_ids,omitempty"`
	OpenObligations             []string               `json:"open_obligations,omitempty"`
	Blockers                    []string               `json:"blockers,omitempty"`
	DecidedAt                   string                 `json:"decided_at,omitempty"`
}

type SourceSatisfiedCloseout struct {
	Tool          string                  `json:"tool,omitempty"`
	Summary       string                  `json:"summary"`
	Reason        string                  `json:"reason,omitempty"`
	EvidencePaths []string                `json:"evidence_paths"`
	TestPaths     []string                `json:"test_paths,omitempty"`
	Anchors       []SourceSatisfiedAnchor `json:"anchors"`
}

type SourceSatisfiedAnchor struct {
	Path string `json:"path"`
	Text string `json:"text"`
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

// CoverageContractRef is a read-only product workflow authority binding. The
// Feature Planner may cite it, but Product Workflow owns sufficiency decisions.
type CoverageContractRef struct {
	ContractID   string `json:"contract_id,omitempty"`
	Digest       string `json:"digest,omitempty"`
	EventID      string `json:"event_id,omitempty"`
	Status       string `json:"status,omitempty"`
	EffortID     string `json:"effort_id,omitempty"`
	CoverageTier string `json:"coverage_tier,omitempty"`
}

type AcceptedBehavioralCoverage struct {
	Authority         string                             `json:"authority,omitempty"`
	Status            string                             `json:"status,omitempty"`
	Contract          CoverageContractRef                `json:"contract,omitempty"`
	Tier              string                             `json:"tier,omitempty"`
	Requirements      []AcceptedCoverageRequirement      `json:"requirements,omitempty"`
	ExampleRules      []AcceptedCoverageExampleRule      `json:"example_rules,omitempty"`
	Scenarios         []AcceptedCoverageScenario         `json:"scenarios,omitempty"`
	DesignContracts   []AcceptedCoverageDesignContract   `json:"design_contracts,omitempty"`
	Invariants        []AcceptedCoverageInvariant        `json:"invariants,omitempty"`
	FailureModes      []AcceptedCoverageFailureMode      `json:"failure_modes,omitempty"`
	HAZOPDeviations   []AcceptedCoverageHAZOPDeviation   `json:"hazop_deviations,omitempty"`
	FaultTreeLeaves   []AcceptedCoverageFaultTreeLeaf    `json:"fault_tree_leaves,omitempty"`
	DecisionTables    []AcceptedCoverageDecisionTable    `json:"decision_tables,omitempty"`
	BoundaryValues    []AcceptedCoverageBoundaryValue    `json:"boundary_values,omitempty"`
	StateMachines     []AcceptedCoverageStateMachine     `json:"state_machines,omitempty"`
	PostMortems       []AcceptedCoveragePostMortem       `json:"post_mortems,omitempty"`
	FormalModels      []AcceptedCoverageFormalModel      `json:"formal_models,omitempty"`
	FuzzTargets       []AcceptedCoverageFuzzTarget       `json:"fuzz_targets,omitempty"`
	RAGEvals          []AcceptedCoverageRAGEval          `json:"rag_evals,omitempty"`
	SecurityCases     []AcceptedCoverageSecurityCase     `json:"security_cases,omitempty"`
	PrivacyControls   []AcceptedCoveragePrivacyControl   `json:"privacy_controls,omitempty"`
	ProviderContracts []AcceptedCoverageProviderContract `json:"provider_contracts,omitempty"`
	Observability     []AcceptedCoverageObservability    `json:"observability,omitempty"`
	PerformanceBounds []AcceptedCoveragePerformanceBound `json:"performance_bounds,omitempty"`
	E2EJourneys       []AcceptedCoverageE2EJourney       `json:"e2e_journeys,omitempty"`
	SurfaceInventory  []AcceptedCoverageSurfaceDecision  `json:"surface_inventory,omitempty"`
	TestObligations   []AcceptedCoverageTestObligation   `json:"test_obligations,omitempty"`
	Verification      []AcceptedCoverageVerification     `json:"verification,omitempty"`
	Mutation          AcceptedCoverageMutation           `json:"mutation,omitempty"`
	Feedback          []AcceptedCoverageFeedback         `json:"feedback,omitempty"`
	Waivers           []AcceptedCoverageWaiver           `json:"waivers,omitempty"`
	Questions         []AcceptedCoverageQuestion         `json:"questions,omitempty"`
}

type AcceptedCoverageRequirement struct {
	ID          string   `json:"id"`
	Pattern     string   `json:"pattern,omitempty"`
	Statement   string   `json:"statement"`
	Refs        []string `json:"refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageExampleRule struct {
	ID            string                    `json:"id"`
	RequirementID string                    `json:"requirement_id,omitempty"`
	Rule          string                    `json:"rule"`
	Examples      []AcceptedCoverageExample `json:"examples,omitempty"`
	Questions     []string                  `json:"questions,omitempty"`
}

type AcceptedCoverageExample struct {
	ID               string   `json:"id"`
	Description      string   `json:"description,omitempty"`
	ConcreteDataRefs []string `json:"concrete_data_refs,omitempty"`
	Negative         bool     `json:"negative,omitempty"`
}

type AcceptedCoverageScenario struct {
	ID               string   `json:"id"`
	RequirementID    string   `json:"requirement_id,omitempty"`
	RuleID           string   `json:"rule_id,omitempty"`
	Given            []string `json:"given,omitempty"`
	When             []string `json:"when,omitempty"`
	Then             []string `json:"then,omitempty"`
	ConcreteDataRefs []string `json:"concrete_data_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

type AcceptedCoverageDesignContract struct {
	ID             string   `json:"id"`
	Boundary       string   `json:"boundary,omitempty"`
	Preconditions  []string `json:"preconditions,omitempty"`
	Postconditions []string `json:"postconditions,omitempty"`
	Invariants     []string `json:"invariants,omitempty"`
	TestRefs       []string `json:"test_refs,omitempty"`
	Obligations    []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageInvariant struct {
	ID                    string   `json:"id"`
	Statement             string   `json:"statement,omitempty"`
	PropertyBasedTestRefs []string `json:"property_based_test_refs,omitempty"`
	Obligations           []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageFailureMode struct {
	ID                 string   `json:"id"`
	Mode               string   `json:"mode,omitempty"`
	Mitigation         string   `json:"mitigation,omitempty"`
	RPN                int      `json:"rpn,omitempty"`
	MitigationTestRefs []string `json:"mitigation_test_refs,omitempty"`
	Obligations        []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageHAZOPDeviation struct {
	ID           string   `json:"id"`
	GuideWord    string   `json:"guide_word,omitempty"`
	WorkflowStep string   `json:"workflow_step,omitempty"`
	Deviation    string   `json:"deviation,omitempty"`
	Severity     int      `json:"severity,omitempty"`
	TestRefs     []string `json:"test_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageFaultTreeLeaf struct {
	ID          string   `json:"id"`
	TopEvent    string   `json:"top_event,omitempty"`
	Cause       string   `json:"cause,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageDecisionTable struct {
	ID          string                         `json:"id"`
	Conditions  []string                       `json:"conditions,omitempty"`
	Rules       []AcceptedCoverageDecisionRule `json:"rules,omitempty"`
	TestRefs    []string                       `json:"test_refs,omitempty"`
	Obligations []string                       `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageDecisionRule struct {
	ID         string            `json:"id,omitempty"`
	Conditions map[string]string `json:"conditions,omitempty"`
	Expected   string            `json:"expected,omitempty"`
}

type AcceptedCoverageBoundaryValue struct {
	ID          string   `json:"id"`
	Parameter   string   `json:"parameter,omitempty"`
	Classes     []string `json:"classes,omitempty"`
	Values      []string `json:"values,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageStateMachine struct {
	ID          string                       `json:"id"`
	States      []string                     `json:"states,omitempty"`
	Transitions []AcceptedCoverageTransition `json:"transitions,omitempty"`
	Obligations []string                     `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageTransition struct {
	ID       string   `json:"id,omitempty"`
	From     string   `json:"from,omitempty"`
	Event    string   `json:"event,omitempty"`
	To       string   `json:"to,omitempty"`
	TestRefs []string `json:"test_refs,omitempty"`
}

type AcceptedCoveragePostMortem struct {
	ID               string   `json:"id"`
	Incident         string   `json:"incident,omitempty"`
	CatchingTestRefs []string `json:"catching_test_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageFormalModel struct {
	ID               string   `json:"id"`
	Model            string   `json:"model,omitempty"`
	VerificationRefs []string `json:"verification_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageFuzzTarget struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Target      string   `json:"target,omitempty"`
	Generators  []string `json:"generators,omitempty"`
	Invariants  []string `json:"invariants,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageRAGEval struct {
	ID          string             `json:"id"`
	Status      string             `json:"status,omitempty"`
	DatasetRefs []string           `json:"dataset_refs,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
	Thresholds  map[string]float64 `json:"thresholds,omitempty"`
	EvalRefs    []string           `json:"eval_refs,omitempty"`
	Obligations []string           `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageSecurityCase struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	Attack      string   `json:"attack,omitempty"`
	Expected    string   `json:"expected,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoveragePrivacyControl struct {
	ID            string   `json:"id"`
	Status        string   `json:"status,omitempty"`
	DataClass     string   `json:"data_class,omitempty"`
	Policy        string   `json:"policy,omitempty"`
	RedactionRefs []string `json:"redaction_refs,omitempty"`
	Retention     string   `json:"retention,omitempty"`
	TestRefs      []string `json:"test_refs,omitempty"`
	Obligations   []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageProviderContract struct {
	ID           string   `json:"id"`
	Status       string   `json:"status,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Consumer     string   `json:"consumer,omitempty"`
	ContractRefs []string `json:"contract_refs,omitempty"`
	TestRefs     []string `json:"test_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageObservability struct {
	ID               string   `json:"id"`
	Status           string   `json:"status,omitempty"`
	Signal           string   `json:"signal,omitempty"`
	InstrumentedPath string   `json:"instrumented_path,omitempty"`
	RequiredFields   []string `json:"required_fields,omitempty"`
	TestRefs         []string `json:"test_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoveragePerformanceBound struct {
	ID              string   `json:"id"`
	Status          string   `json:"status,omitempty"`
	Operation       string   `json:"operation,omitempty"`
	Bound           string   `json:"bound,omitempty"`
	MeasurementRefs []string `json:"measurement_refs,omitempty"`
	Obligations     []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageE2EJourney struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Journey     string   `json:"journey,omitempty"`
	Steps       []string `json:"steps,omitempty"`
	ScriptRefs  []string `json:"script_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type AcceptedCoverageTestObligation struct {
	ID             string `json:"id"`
	Type           string `json:"type,omitempty"`
	Behavior       string `json:"behavior"`
	Owner          string `json:"owner,omitempty"`
	Command        string `json:"command"`
	RequiredBefore string `json:"required_before,omitempty"`
}

type AcceptedCoverageVerification struct {
	ID          string   `json:"id"`
	Gate        string   `json:"gate"`
	Command     string   `json:"command,omitempty"`
	Obligations []string `json:"obligations,omitempty"`
}

type AcceptedCoverageMutation struct {
	Requirement  string   `json:"requirement,omitempty"`
	MinScore     int      `json:"min_score,omitempty"`
	Score        *float64 `json:"score,omitempty"`
	WaiverReason string   `json:"waiver_reason,omitempty"`
}

type AcceptedCoverageFeedback struct {
	ID             string   `json:"id"`
	Outcome        string   `json:"outcome,omitempty"`
	ObligationRefs []string `json:"obligation_refs,omitempty"`
}

type AcceptedCoverageWaiver struct {
	ID        string `json:"id"`
	Technique string `json:"technique"`
	Reason    string `json:"reason"`
}

type AcceptedCoverageQuestion struct {
	ID         string `json:"id"`
	Question   string `json:"question"`
	Status     string `json:"status,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

type AcceptedCoverageSurfaceDecision struct {
	ID           string   `json:"id"`
	Surface      string   `json:"surface"`
	Status       string   `json:"status"`
	Rationale    string   `json:"rationale,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
	QuestionRefs []string `json:"question_refs,omitempty"`
}

type BehavioralCoverageDraft struct {
	Authority         string                          `json:"authority,omitempty"` // "standalone_provisional"
	Status            string                          `json:"status,omitempty"`    // "draft", "provisional", "question_open", or "superseded"
	Tier              string                          `json:"tier,omitempty"`
	Requirements      []CoverageDraftRequirement      `json:"requirements,omitempty"`
	ExampleRules      []CoverageDraftExampleRule      `json:"example_rules,omitempty"`
	Scenarios         []CoverageDraftScenario         `json:"scenarios,omitempty"`
	Invariants        []CoverageDraftInvariant        `json:"invariants,omitempty"`
	FailureModes      []CoverageDraftFailureMode      `json:"failure_modes,omitempty"`
	StateMachines     []CoverageDraftStateMachine     `json:"state_machines,omitempty"`
	FuzzTargets       []CoverageDraftFuzzTarget       `json:"fuzz_targets,omitempty"`
	RAGEvals          []CoverageDraftRAGEval          `json:"rag_evals,omitempty"`
	SecurityCases     []CoverageDraftSecurityCase     `json:"security_cases,omitempty"`
	PrivacyControls   []CoverageDraftPrivacyControl   `json:"privacy_controls,omitempty"`
	ProviderContracts []CoverageDraftProviderContract `json:"provider_contracts,omitempty"`
	Observability     []CoverageDraftObservability    `json:"observability,omitempty"`
	PerformanceBounds []CoverageDraftPerformanceBound `json:"performance_bounds,omitempty"`
	E2EJourneys       []CoverageDraftE2EJourney       `json:"e2e_journeys,omitempty"`
	SurfaceInventory  []CoverageDraftSurfaceDecision  `json:"surface_inventory,omitempty"`
	TestObligations   []CoverageDraftTestObligation   `json:"test_obligations,omitempty"`
	Verification      []CoverageDraftVerification     `json:"verification,omitempty"`
	Waivers           []CoverageDraftWaiver           `json:"waivers,omitempty"`
	OpenQuestions     []CoverageDraftOpenQuestion     `json:"open_questions,omitempty"`
	Items             []BehavioralCoverageItem        `json:"items,omitempty"` // legacy compact draft entries
}

type BehavioralCoverageItem struct {
	ID          string   `json:"id"`
	Behavior    string   `json:"behavior"`
	Obligations []string `json:"tdd_obligations,omitempty"`
	Evidence    []string `json:"evidence,omitempty"`
}

type CoverageDraftRequirement struct {
	ID          string   `json:"id"`
	Pattern     string   `json:"pattern,omitempty"`
	Statement   string   `json:"statement"`
	Refs        []string `json:"refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftExampleRule struct {
	ID            string                 `json:"id"`
	RequirementID string                 `json:"requirement_id,omitempty"`
	Rule          string                 `json:"rule"`
	Examples      []CoverageDraftExample `json:"examples,omitempty"`
	Questions     []string               `json:"questions,omitempty"`
}

type CoverageDraftExample struct {
	ID               string            `json:"id"`
	Description      string            `json:"description,omitempty"`
	ConcreteData     map[string]string `json:"concrete_data,omitempty"`
	ConcreteDataRefs []string          `json:"concrete_data_refs,omitempty"`
	Negative         bool              `json:"negative,omitempty"`
}

func (example *CoverageDraftExample) UnmarshalJSON(data []byte) error {
	type alias CoverageDraftExample
	var raw struct {
		alias
		ConcreteData json.RawMessage `json:"concrete_data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*example = CoverageDraftExample(raw.alias)
	example.ConcreteData = normalizeCoverageConcreteData(raw.ConcreteData)
	return nil
}

type CoverageDraftScenario struct {
	ID               string            `json:"id"`
	RequirementID    string            `json:"requirement_id,omitempty"`
	RuleID           string            `json:"rule_id,omitempty"`
	Given            []string          `json:"given,omitempty"`
	When             []string          `json:"when,omitempty"`
	Then             []string          `json:"then,omitempty"`
	ConcreteData     map[string]string `json:"concrete_data,omitempty"`
	ConcreteDataRefs []string          `json:"concrete_data_refs,omitempty"`
	Obligations      []string          `json:"obligations,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
}

func (scenario *CoverageDraftScenario) UnmarshalJSON(data []byte) error {
	type alias CoverageDraftScenario
	var raw struct {
		alias
		ConcreteData json.RawMessage `json:"concrete_data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*scenario = CoverageDraftScenario(raw.alias)
	scenario.ConcreteData = normalizeCoverageConcreteData(raw.ConcreteData)
	return nil
}

func normalizeCoverageConcreteData(raw json.RawMessage) map[string]string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	out := map[string]string{}
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if normalizedKey := strings.TrimSpace(key); normalizedKey != "" {
				out[normalizedKey] = coverageConcreteDataValueString(item)
			}
		}
	case []any:
		for i, item := range typed {
			out[fmt.Sprintf("item_%d", i+1)] = coverageConcreteDataValueString(item)
		}
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			out["value"] = text
		}
	default:
		out["value"] = coverageConcreteDataValueString(typed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func coverageConcreteDataValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64, bool:
		return fmt.Sprint(typed)
	default:
		body, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(body)
	}
}

type CoverageDraftInvariant struct {
	ID                    string   `json:"id"`
	Statement             string   `json:"statement"`
	PropertyBasedTestRefs []string `json:"property_based_test_refs,omitempty"`
	Obligations           []string `json:"obligations,omitempty"`
}

type CoverageDraftFailureMode struct {
	ID                 string   `json:"id"`
	Mode               string   `json:"mode"`
	Mitigation         string   `json:"mitigation,omitempty"`
	RPN                int      `json:"rpn,omitempty"`
	MitigationTestRefs []string `json:"mitigation_test_refs,omitempty"`
	Obligations        []string `json:"obligations,omitempty"`
}

type CoverageDraftStateMachine struct {
	ID          string                    `json:"id"`
	States      []string                  `json:"states,omitempty"`
	Transitions []CoverageDraftTransition `json:"transitions,omitempty"`
	Obligations []string                  `json:"obligations,omitempty"`
}

type CoverageDraftFuzzTarget struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Target      string   `json:"target,omitempty"`
	Generators  []string `json:"generators,omitempty"`
	Invariants  []string `json:"invariants,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftRAGEval struct {
	ID          string             `json:"id"`
	Status      string             `json:"status,omitempty"`
	DatasetRefs []string           `json:"dataset_refs,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
	Thresholds  map[string]float64 `json:"thresholds,omitempty"`
	EvalRefs    []string           `json:"eval_refs,omitempty"`
	Obligations []string           `json:"tdd_obligations,omitempty"`
}

type CoverageDraftSecurityCase struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	Attack      string   `json:"attack,omitempty"`
	Expected    string   `json:"expected,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftPrivacyControl struct {
	ID            string   `json:"id"`
	Status        string   `json:"status,omitempty"`
	DataClass     string   `json:"data_class,omitempty"`
	Policy        string   `json:"policy,omitempty"`
	RedactionRefs []string `json:"redaction_refs,omitempty"`
	Retention     string   `json:"retention,omitempty"`
	TestRefs      []string `json:"test_refs,omitempty"`
	Obligations   []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftProviderContract struct {
	ID           string   `json:"id"`
	Status       string   `json:"status,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Consumer     string   `json:"consumer,omitempty"`
	ContractRefs []string `json:"contract_refs,omitempty"`
	TestRefs     []string `json:"test_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftObservability struct {
	ID               string   `json:"id"`
	Status           string   `json:"status,omitempty"`
	Signal           string   `json:"signal,omitempty"`
	InstrumentedPath string   `json:"instrumented_path,omitempty"`
	RequiredFields   []string `json:"required_fields,omitempty"`
	TestRefs         []string `json:"test_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftPerformanceBound struct {
	ID              string   `json:"id"`
	Status          string   `json:"status,omitempty"`
	Operation       string   `json:"operation,omitempty"`
	Bound           string   `json:"bound,omitempty"`
	MeasurementRefs []string `json:"measurement_refs,omitempty"`
	Obligations     []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftE2EJourney struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Journey     string   `json:"journey,omitempty"`
	Steps       []string `json:"steps,omitempty"`
	ScriptRefs  []string `json:"script_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type CoverageDraftSurfaceDecision struct {
	ID           string   `json:"id"`
	Surface      string   `json:"surface"`
	Status       string   `json:"status"`
	Rationale    string   `json:"rationale,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
	QuestionRefs []string `json:"question_refs,omitempty"`
}

type CoverageDraftTransition struct {
	ID       string   `json:"id,omitempty"`
	From     string   `json:"from"`
	Event    string   `json:"event"`
	To       string   `json:"to"`
	TestRefs []string `json:"test_refs,omitempty"`
}

type CoverageDraftTestObligation struct {
	ID             string `json:"id"`
	Type           string `json:"type,omitempty"`
	Behavior       string `json:"behavior"`
	Owner          string `json:"owner,omitempty"`
	Command        string `json:"command"`
	RequiredBefore string `json:"required_before,omitempty"`
}

type CoverageDraftVerification struct {
	ID          string   `json:"id"`
	Gate        string   `json:"gate"`
	Command     string   `json:"command,omitempty"`
	Obligations []string `json:"obligations,omitempty"`
}

type CoverageDraftWaiver struct {
	ID        string `json:"id"`
	Technique string `json:"technique"`
	Reason    string `json:"reason"`
}

type CoverageDraftOpenQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
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

type VerificationClosureStatus string

const (
	VerificationClosureStatusPassed          VerificationClosureStatus = "passed"
	VerificationClosureStatusSourceSatisfied VerificationClosureStatus = "source_satisfied"
)

func VerificationClosureStatusAllowed(status VerificationClosureStatus) bool {
	switch strings.TrimSpace(string(status)) {
	case string(VerificationClosureStatusPassed), string(VerificationClosureStatusSourceSatisfied):
		return true
	default:
		return false
	}
}

type VerificationClosure struct {
	EventID                      string                      `json:"event_id"`
	AtomID                       string                      `json:"atom_id"`
	Status                       VerificationClosureStatus   `json:"status"`
	Outcome                      string                      `json:"outcome,omitempty"`
	PlanArtifactID               string                      `json:"plan_artifact_id,omitempty"`
	PlanRevision                 int                         `json:"plan_revision,omitempty"`
	PlanDigest                   string                      `json:"plan_digest,omitempty"`
	ApprovalEventID              string                      `json:"approval_event_id,omitempty"`
	WorkspaceFingerprint         string                      `json:"workspace_fingerprint,omitempty"`
	ContractID                   string                      `json:"contract_id,omitempty"`
	ContractHash                 string                      `json:"contract_hash,omitempty"`
	ScopeContractID              string                      `json:"scope_contract_id,omitempty"`
	ScopeContractHash            string                      `json:"scope_contract_hash,omitempty"`
	ChangedPaths                 []string                    `json:"changed_paths,omitempty"`
	SourceSatisfiedEvidencePaths []string                    `json:"source_satisfied_evidence_paths,omitempty"`
	RequiredTestResults          []VerificationCommandResult `json:"required_test_results,omitempty"`
	TDDCommandResults            []VerificationCommandResult `json:"tdd_command_results,omitempty"`
	SourceSanity                 []VerificationCheckResult   `json:"source_sanity,omitempty"`
	Verification                 []VerificationCheckResult   `json:"verification,omitempty"`
	CreatedAt                    string                      `json:"created_at,omitempty"`
}

type VerificationCommandResult struct {
	ID                string   `json:"id,omitempty"`
	Command           string   `json:"command"`
	Status            string   `json:"status"`
	Passed            bool     `json:"passed"`
	Source            string   `json:"source,omitempty"`
	ObligationRefs    []string `json:"obligation_refs,omitempty"`
	TestArtifactPaths []string `json:"test_artifact_paths,omitempty"`
}

type VerificationCheckResult struct {
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	Command    string `json:"command,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"`
	Error      string `json:"error,omitempty"`
}

type PlanAuthorityDelta struct {
	QuestionID     string                `json:"question_id"`
	AuthorityKind  string                `json:"authority_kind"`
	EvidenceRefs   []string              `json:"evidence_refs"`
	AffectedPaths  []string              `json:"affected_paths,omitempty"`
	AffectedFields []string              `json:"affected_fields,omitempty"`
	SourceFacts    []SourceAuthorityFact `json:"source_facts,omitempty"`
	Expected       string                `json:"expected,omitempty"`
	Actual         string                `json:"actual,omitempty"`
	Revision       int                   `json:"revision"`
	CreatedAt      string                `json:"created_at"`
}

type SourceAuthorityFact = plancontract.SourceAuthorityFact

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
	AffectedFields      []string `json:"affected_fields,omitempty"`
	EvidenceRefs        []string `json:"evidence_refs,omitempty"`
	UserFacing          bool     `json:"user_facing,omitempty"`
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
