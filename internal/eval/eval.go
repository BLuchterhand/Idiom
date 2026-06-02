package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/autonomy"
	"idiom/internal/util"
)

type Record struct {
	Type string
	Data map[string]any
}

type PhaseExpectation struct {
	Lane  string `json:"lane,omitempty"`
	Phase string `json:"phase,omitempty"`
	State string `json:"state,omitempty"`
}

type RoutePlanExpectation struct {
	InitialLane   string `json:"initial_lane,omitempty"`
	FollowupLane  string `json:"followup_lane,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Contract      string `json:"contract,omitempty"`
	AgentMode     string `json:"agent_mode,omitempty"`
	WorkflowKind  string `json:"workflow_kind,omitempty"`
	WorkflowPhase string `json:"workflow_phase,omitempty"`
	Thoroughness  string `json:"thoroughness,omitempty"`
}

type ReplanExpectation struct {
	FromLane string `json:"from_lane,omitempty"`
	ToLane   string `json:"to_lane,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type LanguageCapabilityExpectation struct {
	Language string `json:"language,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

type VerificationPlanExpectation struct {
	Coverage                string   `json:"coverage,omitempty"`
	Confidence              string   `json:"confidence,omitempty"`
	RiskLevel               string   `json:"risk_level,omitempty"`
	UsesFullSuite           *bool    `json:"uses_full_suite,omitempty"`
	RequireCommands         []string `json:"require_commands,omitempty"`
	RequireExpectedFailures []string `json:"require_expected_failures,omitempty"`
}

type ReadinessExpectation struct {
	Lane             string   `json:"lane,omitempty"`
	Ready            *bool    `json:"ready,omitempty"`
	Confidence       string   `json:"confidence,omitempty"`
	Action           string   `json:"action,omitempty"`
	MinEvidencePaths *int     `json:"min_evidence_paths,omitempty"`
	ReasonContains   []string `json:"reason_contains,omitempty"`
}

type DeepeningExpectation struct {
	Lane             string   `json:"lane,omitempty"`
	Attempted        *bool    `json:"attempted,omitempty"`
	Exhausted        *bool    `json:"exhausted,omitempty"`
	Action           string   `json:"action,omitempty"`
	MinEvidencePaths *int     `json:"min_evidence_paths,omitempty"`
	ReasonContains   []string `json:"reason_contains,omitempty"`
}

type FinalAnswerCriticExpectation struct {
	Contract        string   `json:"contract,omitempty"`
	Passed          *bool    `json:"passed,omitempty"`
	Action          string   `json:"action,omitempty"`
	Repaired        *bool    `json:"repaired,omitempty"`
	RequireFindings []string `json:"require_findings,omitempty"`
}

type EffortPolicyExpectation struct {
	Lane                   string `json:"lane,omitempty"`
	Level                  string `json:"level,omitempty"`
	MinAnalysisToolBudget  *int   `json:"min_analysis_tool_budget,omitempty"`
	MinImplementToolBudget *int   `json:"min_implement_tool_budget,omitempty"`
	MinDeepeningPasses     *int   `json:"min_deepening_passes,omitempty"`
	MinMaxOutputTokens     *int   `json:"min_max_output_tokens,omitempty"`
	ConfidenceFloor        string `json:"confidence_floor,omitempty"`
	BudgetExhaustionMode   string `json:"budget_exhaustion_mode,omitempty"`
}

type LongHorizonStateExpectation struct {
	Action                     string   `json:"action,omitempty"`
	MemoryKind                 string   `json:"memory_kind,omitempty"`
	SourceLane                 string   `json:"source_lane,omitempty"`
	Stage                      *int     `json:"stage,omitempty"`
	FocusPath                  string   `json:"focus_path,omitempty"`
	RequireRemaining           []string `json:"require_remaining,omitempty"`
	RequireOpenGaps            []string `json:"require_open_gaps,omitempty"`
	RequireAttemptedSearches   []string `json:"require_attempted_searches,omitempty"`
	RequireChosenTargets       []string `json:"require_chosen_targets,omitempty"`
	RequireVerificationHistory []string `json:"require_verification_history,omitempty"`
	RequireNextActions         []string `json:"require_next_actions,omitempty"`
	NextContains               []string `json:"next_contains,omitempty"`
	Resumable                  *bool    `json:"resumable,omitempty"`
}

type WorkflowStateExpectation struct {
	Action              string   `json:"action,omitempty"`
	Kind                string   `json:"kind,omitempty"`
	Phase               string   `json:"phase,omitempty"`
	SourceLane          string   `json:"source_lane,omitempty"`
	RequireTargetRefs   []string `json:"require_target_refs,omitempty"`
	RequireEditable     []string `json:"require_editable_targets,omitempty"`
	RequireEvidence     []string `json:"require_evidence_paths,omitempty"`
	RequireReadOnly     []string `json:"require_read_only_evidence,omitempty"`
	RequireDestinations []string `json:"require_allowed_destinations,omitempty"`
	RequireVerification []string `json:"require_verification,omitempty"`
	RequireNextActions  []string `json:"require_next_actions,omitempty"`
	Resumable           *bool    `json:"resumable,omitempty"`
}

type BudgetLedgerExpectation struct {
	Lane              string `json:"lane,omitempty"`
	StopReason        string `json:"stop_reason,omitempty"`
	MinModelCalls     *int   `json:"min_model_calls,omitempty"`
	MinTotalTokens    *int   `json:"min_total_tokens,omitempty"`
	MinEvidencePaths  *int   `json:"min_evidence_paths,omitempty"`
	BudgetExhausted   *bool  `json:"budget_exhausted,omitempty"`
	UncertaintyPolicy string `json:"uncertainty_policy,omitempty"`
}

type EvidenceDeltaExpectation struct {
	Lane           string `json:"lane,omitempty"`
	MinNewPaths    *int   `json:"min_new_paths,omitempty"`
	MarginalValue  string `json:"marginal_value,omitempty"`
	MinToolResults *int   `json:"min_tool_results,omitempty"`
	MaxFailedTools *int   `json:"max_failed_tools,omitempty"`
}

type ClaimConfidenceExpectation struct {
	Lane             string `json:"lane,omitempty"`
	Confidence       string `json:"confidence,omitempty"`
	MinEvidencePaths *int   `json:"min_evidence_paths,omitempty"`
	MaxUnresolved    *int   `json:"max_unresolved,omitempty"`
	CalibrationBasis string `json:"calibration_basis,omitempty"`
}

type ContextCompactionExpectation struct {
	MinDroppedMessages  *int     `json:"min_dropped_messages,omitempty"`
	MinRetainedMessages *int     `json:"min_retained_messages,omitempty"`
	MaxHistory          *int     `json:"max_history,omitempty"`
	RequirePinnedPaths  []string `json:"require_pinned_paths,omitempty"`
	PinnedMemoryKind    string   `json:"pinned_memory_kind,omitempty"`
	StrategyContains    []string `json:"strategy_contains,omitempty"`
}

type RouteAmbiguityExpectation struct {
	SelectedLane     string   `json:"selected_lane,omitempty"`
	Ambiguous        *bool    `json:"ambiguous,omitempty"`
	RequireSignals   []string `json:"require_signals,omitempty"`
	TieBreakContains []string `json:"tie_break_contains,omitempty"`
}

type VerificationFeedbackExpectation struct {
	Confidence             string   `json:"confidence,omitempty"`
	RequireFailedCommands  []string `json:"require_failed_commands,omitempty"`
	RequireFailureClasses  []string `json:"require_failure_classes,omitempty"`
	RecommendationContains []string `json:"recommendation_contains,omitempty"`
}

type ToolFailureConfidenceExpectation struct {
	Lane             string `json:"lane,omitempty"`
	Tool             string `json:"tool,omitempty"`
	ConfidenceImpact string `json:"confidence_impact,omitempty"`
}

type ControlRailsExpectation struct {
	Lane                  string   `json:"lane,omitempty"`
	FollowupLane          string   `json:"followup_lane,omitempty"`
	Contract              string   `json:"contract,omitempty"`
	AgentMode             string   `json:"agent_mode,omitempty"`
	WorkflowKind          string   `json:"workflow_kind,omitempty"`
	WorkflowPhase         string   `json:"workflow_phase,omitempty"`
	TaskFamily            string   `json:"task_family,omitempty"`
	PermissionProfile     string   `json:"permission_profile,omitempty"`
	RequireAllowedTools   []string `json:"require_allowed_tools,omitempty"`
	RequireTargetRefs     []string `json:"require_target_refs,omitempty"`
	RequireEditPaths      []string `json:"require_edit_paths,omitempty"`
	RequireEvidence       []string `json:"require_evidence,omitempty"`
	RequireTools          []string `json:"require_tools,omitempty"`
	RequireForbiddenTools []string `json:"require_forbidden_tools,omitempty"`
	RequireReadinessGate  []string `json:"require_readiness_gate,omitempty"`
	RequireSearchPolicy   []string `json:"require_search_policy,omitempty"`
	VerificationContract  string   `json:"verification_contract,omitempty"`
	RequireStopConditions []string `json:"require_stop_conditions,omitempty"`
	RequireFinalCritic    []string `json:"require_final_critic,omitempty"`
	DynamicInstructions   *bool    `json:"dynamic_instruction_attachment,omitempty"`
	FailureToEval         *bool    `json:"failure_to_eval,omitempty"`
}

type ContractReplayExpectation struct {
	RequirePacks            []string `json:"require_packs,omitempty"`
	RequireEnforcementModes []string `json:"require_enforcement_modes,omitempty"`
	RequireAtoms            []string `json:"require_atoms,omitempty"`
	MaxViolations           *int     `json:"max_violations,omitempty"`
	ForbidViolationIDs      []string `json:"forbid_violation_ids,omitempty"`
}

type Scenario struct {
	Name                         string                            `json:"name"`
	PromptContains               []string                          `json:"prompt_contains,omitempty"`
	PromptHistoryContains        []string                          `json:"prompt_history_contains,omitempty"`
	FinalAssistantContains       []string                          `json:"final_assistant_contains,omitempty"`
	ForbidFinalAssistantContains []string                          `json:"forbid_final_assistant_contains,omitempty"`
	RequireTools                 []string                          `json:"require_tools,omitempty"`
	RequireRejectedTools         []string                          `json:"require_rejected_tools,omitempty"`
	ForbidTools                  []string                          `json:"forbid_tools,omitempty"`
	RequireDiscoveryTools        []string                          `json:"require_discovery_tools,omitempty"`
	RequireDiscoveryClasses      []string                          `json:"require_discovery_classes,omitempty"`
	MaxHeavyDiscoveryRequests    *int                              `json:"max_heavy_discovery_requests,omitempty"`
	RequirePhases                []PhaseExpectation                `json:"require_phases,omitempty"`
	RequireRoutePlan             *RoutePlanExpectation             `json:"require_route_plan,omitempty"`
	RequireReplan                *ReplanExpectation                `json:"require_replan,omitempty"`
	MaxBroadToolRequests         *int                              `json:"max_broad_tool_requests,omitempty"`
	MaxRepeatedDeadEnds          *int                              `json:"max_repeated_dead_ends,omitempty"`
	MaxSummaryRejections         *int                              `json:"max_summary_rejections,omitempty"`
	RequireVerification          string                            `json:"require_verification,omitempty"`
	RequireVerificationPlan      *VerificationPlanExpectation      `json:"require_verification_plan,omitempty"`
	RequireReadiness             *ReadinessExpectation             `json:"require_readiness,omitempty"`
	RequireDeepening             *DeepeningExpectation             `json:"require_deepening,omitempty"`
	RequireFinalAnswerCritic     *FinalAnswerCriticExpectation     `json:"require_final_answer_critic,omitempty"`
	RequireEffortPolicy          *EffortPolicyExpectation          `json:"require_effort_policy,omitempty"`
	RequireLongHorizonState      *LongHorizonStateExpectation      `json:"require_long_horizon_state,omitempty"`
	RequireWorkflowState         *WorkflowStateExpectation         `json:"require_workflow_state,omitempty"`
	RequireBudgetLedger          *BudgetLedgerExpectation          `json:"require_budget_ledger,omitempty"`
	RequireEvidenceDelta         *EvidenceDeltaExpectation         `json:"require_evidence_delta,omitempty"`
	RequireClaimConfidence       *ClaimConfidenceExpectation       `json:"require_claim_confidence,omitempty"`
	RequireContextCompaction     *ContextCompactionExpectation     `json:"require_context_compaction,omitempty"`
	RequireRouteAmbiguity        *RouteAmbiguityExpectation        `json:"require_route_ambiguity,omitempty"`
	RequireVerificationFeedback  *VerificationFeedbackExpectation  `json:"require_verification_feedback,omitempty"`
	RequireToolFailureConfidence *ToolFailureConfidenceExpectation `json:"require_tool_failure_confidence,omitempty"`
	RequireControlRails          *ControlRailsExpectation          `json:"require_control_rails,omitempty"`
	RequireContractReplay        *ContractReplayExpectation        `json:"require_contract_replay,omitempty"`
	RequireAssistantForEachUser  bool                              `json:"require_assistant_for_each_user,omitempty"`
	RequireChangedFiles          []string                          `json:"require_changed_files,omitempty"`
	RequireEvidencePaths         []string                          `json:"require_evidence_paths,omitempty"`
	RequireLanguageCapabilities  []LanguageCapabilityExpectation   `json:"require_language_capabilities,omitempty"`
	RequireApproval              *bool                             `json:"require_approval,omitempty"`
	RequirePlanExecution         *PlanExecutionExpectation         `json:"require_plan_execution,omitempty"`
	// RequireRunEvents asserts each named event appears at least once,
	// matched against either a type:"message" role (runner-emitted
	// RunEvents like plan_then_execute_write_guard) or a top-level
	// record type (orchestrator-emitted events like
	// idiom.plan_execute.preflight_failed). One field, two valid
	// sources — generic seam for gate coverage without per-event typed
	// expectations.
	RequireRunEvents []string `json:"require_run_events,omitempty"`
}

type PlanExecutionExpectation struct {
	RequireHandoffID            bool     `json:"require_handoff_id,omitempty"`
	RequireApprovalScopeID      bool     `json:"require_approval_scope_id,omitempty"`
	RequireNoDrift              bool     `json:"require_no_drift,omitempty"`
	RequireDrift                bool     `json:"require_drift,omitempty"`
	RequireDriftReasons         []string `json:"require_drift_reasons,omitempty"`
	RequirePacketStates         []string `json:"require_packet_states,omitempty"`
	RequireExecutionPermissions []string `json:"require_execution_permissions,omitempty"`
}

type PlanExecutionHandoffSnapshot struct {
	PlanID              string `json:"plan_id,omitempty"`
	HandoffID           string `json:"handoff_id,omitempty"`
	ApprovalScopeID     string `json:"approval_scope_id,omitempty"`
	ApprovalStatus      string `json:"approval_status,omitempty"`
	ExecutionPermission string `json:"approval_execution_permission,omitempty"`
	PacketState         string `json:"packet_state,omitempty"`
	Reason              string `json:"reason,omitempty"`
}

type PlanExecutionDriftSnapshot struct {
	Path            string `json:"path,omitempty"`
	Reason          string `json:"reason,omitempty"`
	HandoffID       string `json:"handoff_id,omitempty"`
	ApprovalScopeID string `json:"approval_scope_id,omitempty"`
	PacketState     string `json:"packet_state,omitempty"`
}

type PhaseSnapshot struct {
	Lane   string `json:"lane"`
	Phase  string `json:"phase"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type RoutePlanSnapshot struct {
	InitialLane   string `json:"initial_lane,omitempty"`
	FollowupLane  string `json:"followup_lane,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Contract      string `json:"contract,omitempty"`
	AgentMode     string `json:"agent_mode,omitempty"`
	WorkflowKind  string `json:"workflow_kind,omitempty"`
	WorkflowPhase string `json:"workflow_phase,omitempty"`
	Thoroughness  string `json:"thoroughness,omitempty"`
	Raw           string `json:"raw,omitempty"`
}

type ReplanSnapshot struct {
	FromLane string `json:"from_lane,omitempty"`
	ToLane   string `json:"to_lane,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Raw      string `json:"raw,omitempty"`
}

type LanguageCapabilitySnapshot struct {
	Language string   `json:"language,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	Signals  []string `json:"signals,omitempty"`
}

type VerificationPlanSnapshot struct {
	Commands         []string `json:"commands,omitempty"`
	Reasons          []string `json:"reasons,omitempty"`
	UsesFullSuite    bool     `json:"uses_full_suite,omitempty"`
	Coverage         string   `json:"coverage,omitempty"`
	Confidence       string   `json:"confidence,omitempty"`
	RiskLevel        string   `json:"risk_level,omitempty"`
	ExpectedFailures []string `json:"expected_failures,omitempty"`
	RepairPolicy     string   `json:"repair_policy,omitempty"`
	Raw              string   `json:"raw,omitempty"`
}

type ReadinessSnapshot struct {
	Lane          string   `json:"lane,omitempty"`
	Ready         bool     `json:"ready"`
	Confidence    string   `json:"confidence,omitempty"`
	Action        string   `json:"action,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	EvidencePaths []string `json:"evidence_paths,omitempty"`
	Raw           string   `json:"raw,omitempty"`
}

type DeepeningSnapshot struct {
	Lane          string   `json:"lane,omitempty"`
	Attempt       int      `json:"attempt,omitempty"`
	Attempted     bool     `json:"attempted"`
	Exhausted     bool     `json:"exhausted,omitempty"`
	Action        string   `json:"action,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	EvidencePaths []string `json:"evidence_paths,omitempty"`
	Raw           string   `json:"raw,omitempty"`
}

type FinalAnswerCriticSnapshot struct {
	Contract      string   `json:"contract,omitempty"`
	Passed        bool     `json:"passed"`
	Action        string   `json:"action,omitempty"`
	Repaired      bool     `json:"repaired,omitempty"`
	Findings      []string `json:"findings,omitempty"`
	EvidencePaths []string `json:"evidence_paths,omitempty"`
	Raw           string   `json:"raw,omitempty"`
}

type EffortPolicySnapshot struct {
	Lane                 string `json:"lane,omitempty"`
	Level                string `json:"level,omitempty"`
	MaxToolSteps         int    `json:"max_tool_steps,omitempty"`
	AnalysisToolBudget   int    `json:"analysis_tool_budget,omitempty"`
	ImplementToolBudget  int    `json:"implement_tool_budget,omitempty"`
	DeepeningPasses      int    `json:"deepening_passes,omitempty"`
	TimeBudgetSeconds    int    `json:"time_budget_seconds,omitempty"`
	MaxOutputTokens      int    `json:"max_output_tokens,omitempty"`
	ReasoningBudget      int    `json:"reasoning_budget,omitempty"`
	ConfidenceFloor      string `json:"confidence_floor,omitempty"`
	BudgetExhaustionMode string `json:"budget_exhaustion_mode,omitempty"`
	Raw                  string `json:"raw,omitempty"`
}

type LongHorizonStateSnapshot struct {
	Action              string   `json:"action,omitempty"`
	MemoryKind          string   `json:"memory_kind,omitempty"`
	SourceLane          string   `json:"source_lane,omitempty"`
	Stage               int      `json:"stage,omitempty"`
	FocusPath           string   `json:"focus_path,omitempty"`
	Changed             []string `json:"changed,omitempty"`
	Remaining           []string `json:"remaining,omitempty"`
	OpenGaps            []string `json:"open_gaps,omitempty"`
	AttemptedSearches   []string `json:"attempted_searches,omitempty"`
	ChosenTargets       []string `json:"chosen_targets,omitempty"`
	VerificationHistory []string `json:"verification_history,omitempty"`
	NextActions         []string `json:"next_actions,omitempty"`
	NextStep            string   `json:"next_step,omitempty"`
	Verification        string   `json:"verification,omitempty"`
	Resumable           bool     `json:"resumable"`
	Reason              string   `json:"reason,omitempty"`
	Raw                 string   `json:"raw,omitempty"`
}

type WorkflowStateSnapshot struct {
	Action              string   `json:"action,omitempty"`
	Kind                string   `json:"kind,omitempty"`
	Phase               string   `json:"phase,omitempty"`
	SourceLane          string   `json:"source_lane,omitempty"`
	Objective           string   `json:"objective,omitempty"`
	TargetRefs          []string `json:"target_refs,omitempty"`
	EditableTargets     []string `json:"editable_targets,omitempty"`
	ReadOnlyEvidence    []string `json:"read_only_evidence,omitempty"`
	EvidencePaths       []string `json:"evidence_paths,omitempty"`
	AllowedDestinations []string `json:"allowed_destinations,omitempty"`
	Verification        []string `json:"verification,omitempty"`
	NextActions         []string `json:"next_actions,omitempty"`
	Resumable           bool     `json:"resumable"`
	Reason              string   `json:"reason,omitempty"`
	Raw                 string   `json:"raw,omitempty"`
}

type BudgetLedgerSnapshot struct {
	Lane                   string   `json:"lane,omitempty"`
	Level                  string   `json:"level,omitempty"`
	UsedModelCalls         int      `json:"used_model_calls,omitempty"`
	UsedDeepeningPasses    int      `json:"used_deepening_passes,omitempty"`
	MaxDeepeningPasses     int      `json:"max_deepening_passes,omitempty"`
	UsedToolResults        int      `json:"used_tool_results,omitempty"`
	PromptTokens           int      `json:"prompt_tokens,omitempty"`
	CompletionTokens       int      `json:"completion_tokens,omitempty"`
	TotalTokens            int      `json:"total_tokens,omitempty"`
	NewEvidencePaths       int      `json:"new_evidence_paths,omitempty"`
	TotalEvidencePaths     int      `json:"total_evidence_paths,omitempty"`
	StopReason             string   `json:"stop_reason,omitempty"`
	BudgetExhausted        bool     `json:"budget_exhausted,omitempty"`
	NextAllowedActions     []string `json:"next_allowed_actions,omitempty"`
	MarginalEvidencePolicy string   `json:"marginal_evidence_policy,omitempty"`
	UserVisibleUncertainty string   `json:"user_visible_uncertainty,omitempty"`
	Raw                    string   `json:"raw,omitempty"`
}

type EvidenceDeltaSnapshot struct {
	Lane             string   `json:"lane,omitempty"`
	Attempt          int      `json:"attempt,omitempty"`
	NewEvidencePaths []string `json:"new_evidence_paths,omitempty"`
	TotalPaths       int      `json:"total_paths,omitempty"`
	ToolResults      int      `json:"tool_results,omitempty"`
	SuccessfulTools  int      `json:"successful_tools,omitempty"`
	FailedTools      int      `json:"failed_tools,omitempty"`
	MarginalValue    string   `json:"marginal_value,omitempty"`
	Raw              string   `json:"raw,omitempty"`
}

type ClaimConfidenceSnapshot struct {
	Lane                  string   `json:"lane,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	SupportedClaims       int      `json:"supported_claims,omitempty"`
	EvidencePaths         []string `json:"evidence_paths,omitempty"`
	UnresolvedUncertainty []string `json:"unresolved_uncertainty,omitempty"`
	Contradictions        []string `json:"contradictions,omitempty"`
	CalibrationBasis      string   `json:"calibration_basis,omitempty"`
	Raw                   string   `json:"raw,omitempty"`
}

type ContextCompactionSnapshot struct {
	DroppedMessages  int      `json:"dropped_messages,omitempty"`
	RetainedMessages int      `json:"retained_messages,omitempty"`
	MaxHistory       int      `json:"max_history,omitempty"`
	PinnedPaths      []string `json:"pinned_paths,omitempty"`
	PinnedMemoryKind string   `json:"pinned_memory_kind,omitempty"`
	Strategy         string   `json:"strategy,omitempty"`
	Raw              string   `json:"raw,omitempty"`
}

type RouteAmbiguitySnapshot struct {
	SelectedLane string   `json:"selected_lane,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Signals      []string `json:"signals,omitempty"`
	Ambiguous    bool     `json:"ambiguous"`
	TieBreak     string   `json:"tie_break,omitempty"`
	Raw          string   `json:"raw,omitempty"`
}

type VerificationFeedbackSnapshot struct {
	RiskLevel             string   `json:"risk_level,omitempty"`
	Coverage              string   `json:"coverage,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	Commands              []string `json:"commands,omitempty"`
	FailedCommands        []string `json:"failed_commands,omitempty"`
	FailureClasses        []string `json:"failure_classes,omitempty"`
	LearnedRecommendation string   `json:"learned_recommendation,omitempty"`
	Raw                   string   `json:"raw,omitempty"`
}

type ToolFailureConfidenceSnapshot struct {
	Lane             string `json:"lane,omitempty"`
	Tool             string `json:"tool,omitempty"`
	Action           string `json:"action,omitempty"`
	ConfidenceImpact string `json:"confidence_impact,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Raw              string `json:"raw,omitempty"`
}

type ControlRailsSnapshot struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	AgentMode            string   `json:"agent_mode,omitempty"`
	WorkflowKind         string   `json:"workflow_kind,omitempty"`
	WorkflowPhase        string   `json:"workflow_phase,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	TargetRefs           []string `json:"target_refs,omitempty"`
	RequiredEditPaths    []string `json:"required_edit_paths,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string `json:"forbidden_tools,omitempty"`
	ReadinessGate        []string `json:"readiness_gate,omitempty"`
	SearchPolicy         []string `json:"search_policy,omitempty"`
	VerificationContract string   `json:"verification_contract,omitempty"`
	StopConditions       []string `json:"stop_conditions,omitempty"`
	FinalCritic          []string `json:"final_critic,omitempty"`
	DynamicInstructions  bool     `json:"dynamic_instruction_attachment,omitempty"`
	FailureToEval        bool     `json:"failure_to_eval"`
	Raw                  string   `json:"raw,omitempty"`
}

type ContractReplayReport struct {
	SchemaVersion    string                    `json:"schema_version"`
	TotalEvents      int                       `json:"total_events"`
	AllClear         bool                      `json:"all_clear"`
	Packs            map[string]int            `json:"packs,omitempty"`
	EnforcementModes map[string]int            `json:"enforcement_modes,omitempty"`
	Atoms            map[string]int            `json:"atoms,omitempty"`
	Violations       []ContractReplayViolation `json:"violations,omitempty"`
	Replay           autonomy.ReplayReport     `json:"replay"`
	Audit            autonomy.EventAuditReport `json:"audit"`
}

type ContractReplayViolation struct {
	ID          string   `json:"id"`
	Severity    string   `json:"severity,omitempty"`
	Evidence    string   `json:"evidence,omitempty"`
	EventIDs    []string `json:"event_ids,omitempty"`
	ContractIDs []string `json:"contract_ids,omitempty"`
}

type Metrics struct {
	LastUser                  string                          `json:"last_user"`
	UserPrompts               []string                        `json:"user_prompts,omitempty"`
	FinalAssistant            string                          `json:"final_assistant"`
	WorkspaceRoot             string                          `json:"workspace_root,omitempty"`
	UserTurns                 int                             `json:"user_turns"`
	AssistantTurns            int                             `json:"assistant_turns"`
	DurationMillis            int64                           `json:"duration_millis,omitempty"`
	ToolRequests              int                             `json:"tool_requests"`
	BroadToolRequests         int                             `json:"broad_tool_requests"`
	RepeatedDeadEndRejections int                             `json:"repeated_dead_end_rejections"`
	RepeatedToolRejections    int                             `json:"repeated_tool_rejections"`
	RejectedActions           int                             `json:"rejected_actions,omitempty"`
	CompletionGateRejections  int                             `json:"completion_gate_rejections,omitempty"`
	TurnSummarySeen           bool                            `json:"turn_summary_seen,omitempty"`
	TypedBlockers             int                             `json:"typed_blockers,omitempty"`
	SourceSatisfiedOutcomes   int                             `json:"source_satisfied_outcomes,omitempty"`
	LowValueActions           int                             `json:"low_value_actions,omitempty"`
	LiveContractTimeouts      int                             `json:"live_contract_timeouts,omitempty"`
	SummaryRejections         int                             `json:"summary_rejections"`
	SummaryRejectionLanes     []string                        `json:"summary_rejection_lanes,omitempty"`
	Tools                     map[string]int                  `json:"tools"`
	SuccessfulTools           map[string]int                  `json:"successful_tools,omitempty"`
	RejectedTools             map[string]int                  `json:"rejected_tools,omitempty"`
	DiscoveryTools            map[string]int                  `json:"discovery_tools,omitempty"`
	DiscoveryClasses          map[string]int                  `json:"discovery_classes,omitempty"`
	DiscoveryScopes           map[string]int                  `json:"discovery_scopes,omitempty"`
	DiscoveryCosts            map[string]int                  `json:"discovery_costs,omitempty"`
	HeavyDiscoveryRequests    int                             `json:"heavy_discovery_requests,omitempty"`
	Phases                    []PhaseSnapshot                 `json:"phases,omitempty"`
	RoutePlans                []RoutePlanSnapshot             `json:"route_plans,omitempty"`
	Replans                   []ReplanSnapshot                `json:"replans,omitempty"`
	VerificationPlans         []VerificationPlanSnapshot      `json:"verification_plans,omitempty"`
	ReadinessChecks           []ReadinessSnapshot             `json:"readiness_checks,omitempty"`
	DeepeningAttempts         []DeepeningSnapshot             `json:"deepening_attempts,omitempty"`
	FinalAnswerCritics        []FinalAnswerCriticSnapshot     `json:"final_answer_critics,omitempty"`
	EffortPolicies            []EffortPolicySnapshot          `json:"effort_policies,omitempty"`
	LongHorizonStates         []LongHorizonStateSnapshot      `json:"long_horizon_states,omitempty"`
	WorkflowStates            []WorkflowStateSnapshot         `json:"workflow_states,omitempty"`
	BudgetLedgers             []BudgetLedgerSnapshot          `json:"budget_ledgers,omitempty"`
	EvidenceDeltas            []EvidenceDeltaSnapshot         `json:"evidence_deltas,omitempty"`
	ClaimConfidences          []ClaimConfidenceSnapshot       `json:"claim_confidences,omitempty"`
	ContextCompactions        []ContextCompactionSnapshot     `json:"context_compactions,omitempty"`
	RouteAmbiguities          []RouteAmbiguitySnapshot        `json:"route_ambiguities,omitempty"`
	VerificationFeedback      []VerificationFeedbackSnapshot  `json:"verification_feedback,omitempty"`
	ToolFailureConfidence     []ToolFailureConfidenceSnapshot `json:"tool_failure_confidence,omitempty"`
	ControlRails              []ControlRailsSnapshot          `json:"control_rails,omitempty"`
	ContractReplay            *ContractReplayReport           `json:"contract_replay,omitempty"`
	VerificationChecks        int                             `json:"verification_checks"`
	VerificationPassed        bool                            `json:"verification_passed"`
	VerificationFailed        bool                            `json:"verification_failed"`
	ApprovalRequired          bool                            `json:"approval_required"`
	PlanExecutionHandoffs     []PlanExecutionHandoffSnapshot  `json:"plan_execution_handoffs,omitempty"`
	PlanExecutionDrift        []PlanExecutionDriftSnapshot    `json:"plan_execution_drift,omitempty"`
	ExecutorCompletes         int                             `json:"executor_completes,omitempty"`
	ChangedFiles              []string                        `json:"changed_files,omitempty"`
	EvidencePaths             []string                        `json:"evidence_paths,omitempty"`
	LiveContractRollbacks     []LiveContractRollbackSnapshot  `json:"live_contract_rollbacks,omitempty"`
	LiveContractFileSnapshots []LiveContractFileSnapshot      `json:"live_contract_file_snapshots,omitempty"`
	LanguageCapabilities      []LanguageCapabilitySnapshot    `json:"language_capabilities,omitempty"`
	MessageRoles              map[string]int                  `json:"message_roles,omitempty"`
	RecordTypes               map[string]int                  `json:"record_types,omitempty"`
	ErrorCount                int                             `json:"error_count,omitempty"`
	Errors                    []string                        `json:"errors,omitempty"`
}

type LiveContractRollbackSnapshot struct {
	Status             string   `json:"status,omitempty"`
	BaselineTree       string   `json:"baseline_tree,omitempty"`
	PostResetTree      string   `json:"post_reset_tree,omitempty"`
	ChangedBeforeReset []string `json:"changed_before_reset,omitempty"`
	ChangedAfterReset  []string `json:"changed_after_reset,omitempty"`
}

type LiveContractFileSnapshot struct {
	Path      string `json:"path,omitempty"`
	Exists    bool   `json:"exists,omitempty"`
	SizeBytes int    `json:"size_bytes,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Content   string `json:"content,omitempty"`
}

type Report struct {
	Scenario string   `json:"scenario"`
	LogPath  string   `json:"log_path"`
	Passed   bool     `json:"passed"`
	Findings []string `json:"findings,omitempty"`
	Metrics  Metrics  `json:"metrics"`
}

func (m Metrics) TotalErrorEvents() int {
	if m.ErrorCount > 0 {
		return m.ErrorCount
	}
	return len(m.Errors)
}

func LoadRecords(path string) ([]Record, error) {
	records := make([]Record, 0, 64)
	err := util.ScanFile(filepath.Clean(path), func(raw json.RawMessage) error {
		payload := make(map[string]any)
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("decode log record: %w", err)
		}
		records = append(records, normalizeLogRecord(payload))
		return nil
	}, 64*1024*1024)
	if err != nil {
		return nil, err
	}
	return records, nil
}

func normalizeLogRecord(payload map[string]any) Record {
	recType, _ := payload["type"].(string)
	if recType != "" {
		return Record{Type: recType, Data: payload}
	}
	kind, _ := payload["kind"].(string)
	if kind == "" {
		return Record{Data: payload}
	}
	data := make(map[string]any)
	if nested, ok := payload["payload"].(map[string]any); ok {
		for k, v := range nested {
			data[k] = v
		}
	} else if value, ok := payload["payload"]; ok {
		data["payload"] = value
	}
	for _, key := range []string{"ts", "session_id"} {
		if v, ok := payload[key]; ok {
			data[key] = v
		}
	}
	data["kind"] = kind
	data["type"] = kind
	return Record{Type: kind, Data: data}
}

func LoadScenario(path string) (Scenario, error) {
	var scenario Scenario
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return scenario, fmt.Errorf("read scenario: %w", err)
	}
	if err := json.Unmarshal(data, &scenario); err != nil {
		return scenario, fmt.Errorf("decode scenario: %w", err)
	}
	return scenario, nil
}

func WriteScenario(path string, scenario Scenario) error {
	data, err := json.MarshalIndent(scenario, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scenario: %w", err)
	}
	if err := os.WriteFile(filepath.Clean(path), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write scenario: %w", err)
	}
	return nil
}

func containsStringFold(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func stringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	if value, ok := data[key].(string); ok {
		return value
	}
	return ""
}

func boolField(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	value, ok := data[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	default:
		return false
	}
}

func timestampField(data map[string]any, key string) time.Time {
	if data == nil {
		return time.Time{}
	}
	value, ok := data[key].(string)
	if !ok {
		return time.Time{}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func intField(data map[string]any, key string) int {
	if data == nil {
		return 0
	}
	value, ok := data[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}
