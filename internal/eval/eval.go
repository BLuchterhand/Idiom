package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/util"
)

type Record struct {
	Type string
	Data map[string]any
}

const maxJSONLRecordBytes = 64 * 1024 * 1024

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

type Scenario struct {
	Name                         string                            `json:"name"`
	PromptContains               []string                          `json:"prompt_contains,omitempty"`
	FinalAssistantContains       []string                          `json:"final_assistant_contains,omitempty"`
	ForbidFinalAssistantContains []string                          `json:"forbid_final_assistant_contains,omitempty"`
	RequireTools                 []string                          `json:"require_tools,omitempty"`
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
	Contract string   `json:"contract,omitempty"`
	Passed   bool     `json:"passed"`
	Action   string   `json:"action,omitempty"`
	Repaired bool     `json:"repaired,omitempty"`
	Findings []string `json:"findings,omitempty"`
	Raw      string   `json:"raw,omitempty"`
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

type Metrics struct {
	LastUser                  string                          `json:"last_user"`
	FinalAssistant            string                          `json:"final_assistant"`
	WorkspaceRoot             string                          `json:"workspace_root,omitempty"`
	UserTurns                 int                             `json:"user_turns"`
	AssistantTurns            int                             `json:"assistant_turns"`
	DurationMillis            int64                           `json:"duration_millis,omitempty"`
	ToolRequests              int                             `json:"tool_requests"`
	BroadToolRequests         int                             `json:"broad_tool_requests"`
	RepeatedDeadEndRejections int                             `json:"repeated_dead_end_rejections"`
	RepeatedToolRejections    int                             `json:"repeated_tool_rejections"`
	SummaryRejections         int                             `json:"summary_rejections"`
	SummaryRejectionLanes     []string                        `json:"summary_rejection_lanes,omitempty"`
	Tools                     map[string]int                  `json:"tools"`
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
	VerificationChecks        int                             `json:"verification_checks"`
	VerificationPassed        bool                            `json:"verification_passed"`
	VerificationFailed        bool                            `json:"verification_failed"`
	ApprovalRequired          bool                            `json:"approval_required"`
	PlanExecutionHandoffs     []PlanExecutionHandoffSnapshot  `json:"plan_execution_handoffs,omitempty"`
	PlanExecutionDrift        []PlanExecutionDriftSnapshot    `json:"plan_execution_drift,omitempty"`
	ChangedFiles              []string                        `json:"changed_files,omitempty"`
	EvidencePaths             []string                        `json:"evidence_paths,omitempty"`
	LanguageCapabilities      []LanguageCapabilitySnapshot    `json:"language_capabilities,omitempty"`
	MessageRoles              map[string]int                  `json:"message_roles,omitempty"`
	RecordTypes               map[string]int                  `json:"record_types,omitempty"`
	Errors                    []string                        `json:"errors,omitempty"`
}

type Report struct {
	Scenario string   `json:"scenario"`
	LogPath  string   `json:"log_path"`
	Passed   bool     `json:"passed"`
	Findings []string `json:"findings,omitempty"`
	Metrics  Metrics  `json:"metrics"`
}

func LoadRecords(path string) ([]Record, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer file.Close()

	records := make([]Record, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), maxJSONLRecordBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		payload := make(map[string]any)
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			return nil, fmt.Errorf("decode log record: %w", err)
		}
		records = append(records, normalizeLogRecord(payload))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan log: %w", err)
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

func BuildSuggestedScenario(name string, records []Record) Scenario {
	metrics := Analyze(records)
	scenario := Scenario{
		Name:           strings.TrimSpace(name),
		PromptContains: nil,
		RequireTools:   suggestedTools(records),
		RequirePhases:  compactPhaseExpectations(metrics.Phases),
	}
	if strings.TrimSpace(metrics.LastUser) != "" {
		scenario.PromptContains = []string{metrics.LastUser}
	}
	switch {
	case metrics.VerificationPassed:
		scenario.RequireVerification = "passed"
	case metrics.VerificationFailed:
		scenario.RequireVerification = "failed"
	default:
		scenario.RequireVerification = "none"
	}
	if len(metrics.ChangedFiles) > 0 {
		scenario.RequireChangedFiles = append([]string(nil), metrics.ChangedFiles...)
	}
	if len(metrics.EvidencePaths) > 0 {
		scenario.RequireEvidencePaths = append([]string(nil), metrics.EvidencePaths...)
	}
	if len(metrics.LanguageCapabilities) > 0 {
		scenario.RequireLanguageCapabilities = compactLanguageCapabilityExpectations(metrics.LanguageCapabilities)
	}
	if len(metrics.VerificationPlans) > 0 {
		latest := metrics.VerificationPlans[len(metrics.VerificationPlans)-1]
		usesFullSuite := latest.UsesFullSuite
		scenario.RequireVerificationPlan = &VerificationPlanExpectation{
			Coverage:                latest.Coverage,
			Confidence:              latest.Confidence,
			RiskLevel:               latest.RiskLevel,
			UsesFullSuite:           &usesFullSuite,
			RequireCommands:         append([]string(nil), latest.Commands...),
			RequireExpectedFailures: append([]string(nil), latest.ExpectedFailures...),
		}
	}
	if len(metrics.ReadinessChecks) > 0 {
		latest := metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1]
		ready := latest.Ready
		minEvidencePaths := len(latest.EvidencePaths)
		scenario.RequireReadiness = &ReadinessExpectation{
			Lane:             latest.Lane,
			Ready:            &ready,
			Confidence:       latest.Confidence,
			Action:           latest.Action,
			MinEvidencePaths: &minEvidencePaths,
		}
	}
	if len(metrics.DeepeningAttempts) > 0 {
		latest := metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1]
		attempted := latest.Attempted
		exhausted := latest.Exhausted
		minEvidencePaths := len(latest.EvidencePaths)
		scenario.RequireDeepening = &DeepeningExpectation{
			Lane:             latest.Lane,
			Attempted:        &attempted,
			Exhausted:        &exhausted,
			Action:           latest.Action,
			MinEvidencePaths: &minEvidencePaths,
		}
	}
	if len(metrics.FinalAnswerCritics) > 0 {
		latest := metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1]
		passed := latest.Passed
		repaired := latest.Repaired
		scenario.RequireFinalAnswerCritic = &FinalAnswerCriticExpectation{
			Contract: latest.Contract,
			Passed:   &passed,
			Action:   latest.Action,
			Repaired: &repaired,
		}
	}
	if len(metrics.EffortPolicies) > 0 {
		latest := metrics.EffortPolicies[len(metrics.EffortPolicies)-1]
		minAnalysis := latest.AnalysisToolBudget
		minImplement := latest.ImplementToolBudget
		minDeepening := latest.DeepeningPasses
		minMaxOutput := latest.MaxOutputTokens
		scenario.RequireEffortPolicy = &EffortPolicyExpectation{
			Lane:                   latest.Lane,
			Level:                  latest.Level,
			MinAnalysisToolBudget:  &minAnalysis,
			MinImplementToolBudget: &minImplement,
			MinDeepeningPasses:     &minDeepening,
			MinMaxOutputTokens:     &minMaxOutput,
			ConfidenceFloor:        latest.ConfidenceFloor,
			BudgetExhaustionMode:   latest.BudgetExhaustionMode,
		}
	}
	if len(metrics.LongHorizonStates) > 0 {
		latest := metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1]
		stage := latest.Stage
		resumable := latest.Resumable
		scenario.RequireLongHorizonState = &LongHorizonStateExpectation{
			Action:                     latest.Action,
			MemoryKind:                 latest.MemoryKind,
			SourceLane:                 latest.SourceLane,
			Stage:                      &stage,
			FocusPath:                  latest.FocusPath,
			RequireRemaining:           append([]string(nil), latest.Remaining...),
			RequireOpenGaps:            append([]string(nil), latest.OpenGaps...),
			RequireAttemptedSearches:   append([]string(nil), latest.AttemptedSearches...),
			RequireChosenTargets:       append([]string(nil), latest.ChosenTargets...),
			RequireVerificationHistory: append([]string(nil), latest.VerificationHistory...),
			RequireNextActions:         append([]string(nil), latest.NextActions...),
			Resumable:                  &resumable,
		}
		if strings.TrimSpace(latest.NextStep) != "" {
			scenario.RequireLongHorizonState.NextContains = []string{latest.NextStep}
		}
	}
	if len(metrics.BudgetLedgers) > 0 {
		latest := metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1]
		minModelCalls := latest.UsedModelCalls
		minEvidencePaths := latest.TotalEvidencePaths
		budgetExhausted := latest.BudgetExhausted
		scenario.RequireBudgetLedger = &BudgetLedgerExpectation{
			Lane:              latest.Lane,
			StopReason:        latest.StopReason,
			MinModelCalls:     &minModelCalls,
			MinEvidencePaths:  &minEvidencePaths,
			BudgetExhausted:   &budgetExhausted,
			UncertaintyPolicy: latest.UserVisibleUncertainty,
		}
	}
	if len(metrics.EvidenceDeltas) > 0 {
		latest := metrics.EvidenceDeltas[len(metrics.EvidenceDeltas)-1]
		minNewPaths := len(latest.NewEvidencePaths)
		minToolResults := latest.ToolResults
		maxFailedTools := latest.FailedTools
		scenario.RequireEvidenceDelta = &EvidenceDeltaExpectation{
			Lane:           latest.Lane,
			MinNewPaths:    &minNewPaths,
			MarginalValue:  latest.MarginalValue,
			MinToolResults: &minToolResults,
			MaxFailedTools: &maxFailedTools,
		}
	}
	if len(metrics.ClaimConfidences) > 0 {
		latest := metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1]
		minEvidencePaths := len(latest.EvidencePaths)
		maxUnresolved := len(latest.UnresolvedUncertainty)
		scenario.RequireClaimConfidence = &ClaimConfidenceExpectation{
			Lane:             latest.Lane,
			Confidence:       latest.Confidence,
			MinEvidencePaths: &minEvidencePaths,
			MaxUnresolved:    &maxUnresolved,
			CalibrationBasis: latest.CalibrationBasis,
		}
	}
	if len(metrics.ContextCompactions) > 0 {
		latest := metrics.ContextCompactions[len(metrics.ContextCompactions)-1]
		minDropped := latest.DroppedMessages
		minRetained := latest.RetainedMessages
		maxHistory := latest.MaxHistory
		scenario.RequireContextCompaction = &ContextCompactionExpectation{
			MinDroppedMessages:  &minDropped,
			MinRetainedMessages: &minRetained,
			MaxHistory:          &maxHistory,
			RequirePinnedPaths:  append([]string(nil), latest.PinnedPaths...),
			PinnedMemoryKind:    latest.PinnedMemoryKind,
			StrategyContains:    nonEmptySingleton(latest.Strategy),
		}
	}
	if len(metrics.RouteAmbiguities) > 0 {
		latest := metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1]
		ambiguous := latest.Ambiguous
		scenario.RequireRouteAmbiguity = &RouteAmbiguityExpectation{
			SelectedLane:     latest.SelectedLane,
			Ambiguous:        &ambiguous,
			RequireSignals:   append([]string(nil), latest.Signals...),
			TieBreakContains: nonEmptySingleton(latest.TieBreak),
		}
	}
	if len(metrics.VerificationFeedback) > 0 {
		latest := metrics.VerificationFeedback[len(metrics.VerificationFeedback)-1]
		scenario.RequireVerificationFeedback = &VerificationFeedbackExpectation{
			Confidence:             latest.Confidence,
			RequireFailedCommands:  append([]string(nil), latest.FailedCommands...),
			RequireFailureClasses:  append([]string(nil), latest.FailureClasses...),
			RecommendationContains: nonEmptySingleton(latest.LearnedRecommendation),
		}
	}
	if len(metrics.ToolFailureConfidence) > 0 {
		latest := metrics.ToolFailureConfidence[len(metrics.ToolFailureConfidence)-1]
		scenario.RequireToolFailureConfidence = &ToolFailureConfidenceExpectation{
			Lane:             latest.Lane,
			Tool:             latest.Tool,
			ConfidenceImpact: latest.ConfidenceImpact,
		}
	}
	if len(metrics.ControlRails) > 0 {
		latest := metrics.ControlRails[len(metrics.ControlRails)-1]
		failureToEval := latest.FailureToEval
		scenario.RequireControlRails = &ControlRailsExpectation{
			Lane:                  latest.Lane,
			FollowupLane:          latest.FollowupLane,
			Contract:              latest.Contract,
			AgentMode:             latest.AgentMode,
			WorkflowKind:          latest.WorkflowKind,
			WorkflowPhase:         latest.WorkflowPhase,
			TaskFamily:            latest.TaskFamily,
			PermissionProfile:     latest.PermissionProfile,
			RequireTargetRefs:     append([]string(nil), latest.TargetRefs...),
			RequireEvidence:       append([]string(nil), latest.RequiredEvidence...),
			RequireTools:          append([]string(nil), latest.RequiredTools...),
			RequireForbiddenTools: append([]string(nil), latest.ForbiddenTools...),
			RequireReadinessGate:  append([]string(nil), latest.ReadinessGate...),
			RequireSearchPolicy:   append([]string(nil), latest.SearchPolicy...),
			VerificationContract:  latest.VerificationContract,
			RequireStopConditions: append([]string(nil), latest.StopConditions...),
			RequireFinalCritic:    append([]string(nil), latest.FinalCritic...),
			FailureToEval:         &failureToEval,
		}
	}
	if len(metrics.RoutePlans) > 0 {
		latest := metrics.RoutePlans[len(metrics.RoutePlans)-1]
		scenario.RequireRoutePlan = &RoutePlanExpectation{
			InitialLane:   latest.InitialLane,
			FollowupLane:  latest.FollowupLane,
			Reason:        latest.Reason,
			Contract:      latest.Contract,
			AgentMode:     latest.AgentMode,
			WorkflowKind:  latest.WorkflowKind,
			WorkflowPhase: latest.WorkflowPhase,
			Thoroughness:  latest.Thoroughness,
		}
	}
	if len(metrics.WorkflowStates) > 0 {
		latest := metrics.WorkflowStates[len(metrics.WorkflowStates)-1]
		resumable := latest.Resumable
		scenario.RequireWorkflowState = &WorkflowStateExpectation{
			Action:              latest.Action,
			Kind:                latest.Kind,
			Phase:               latest.Phase,
			SourceLane:          latest.SourceLane,
			RequireTargetRefs:   append([]string(nil), latest.TargetRefs...),
			RequireEditable:     append([]string(nil), latest.EditableTargets...),
			RequireEvidence:     append([]string(nil), latest.EvidencePaths...),
			RequireReadOnly:     append([]string(nil), latest.ReadOnlyEvidence...),
			RequireDestinations: append([]string(nil), latest.AllowedDestinations...),
			RequireVerification: append([]string(nil), latest.Verification...),
			RequireNextActions:  append([]string(nil), latest.NextActions...),
			Resumable:           &resumable,
		}
	}
	if len(metrics.Replans) > 0 {
		latest := metrics.Replans[len(metrics.Replans)-1]
		scenario.RequireReplan = &ReplanExpectation{
			FromLane: latest.FromLane,
			ToLane:   latest.ToLane,
			Reason:   latest.Reason,
		}
	}
	if metrics.ApprovalRequired {
		yes := true
		scenario.RequireApproval = &yes
	}
	if metrics.BroadToolRequests > 0 {
		max := metrics.BroadToolRequests
		scenario.MaxBroadToolRequests = &max
	}
	if metrics.HeavyDiscoveryRequests > 0 {
		max := metrics.HeavyDiscoveryRequests
		scenario.MaxHeavyDiscoveryRequests = &max
	}
	deadEnds := metrics.RepeatedDeadEndRejections + metrics.RepeatedToolRejections
	if deadEnds > 0 {
		scenario.MaxRepeatedDeadEnds = &deadEnds
	}
	if metrics.SummaryRejections > 0 {
		max := metrics.SummaryRejections
		scenario.MaxSummaryRejections = &max
	}
	return scenario
}

func nonEmptySingleton(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func suggestedTools(records []Record) []string {
	type requestInfo struct {
		Tool  string
		Broad bool
	}
	requests := make(map[int]requestInfo)
	tools := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, record := range records {
		switch record.Type {
		case "tool_request":
			step := intField(record.Data, "step")
			if step <= 0 {
				continue
			}
			requests[step] = requestInfo{
				Tool:  stringField(record.Data, "tool"),
				Broad: boolField(record.Data, "broad"),
			}
		case "tool_result":
			step := intField(record.Data, "step")
			req, ok := requests[step]
			if !ok || req.Broad || strings.TrimSpace(stringField(record.Data, "error")) != "" {
				continue
			}
			tool := strings.TrimSpace(req.Tool)
			if !isSuggestedScenarioTool(tool) {
				continue
			}
			if _, ok := seen[tool]; ok {
				continue
			}
			seen[tool] = struct{}{}
			tools = append(tools, tool)
		}
	}
	slices.Sort(tools)
	return tools
}

func isSuggestedScenarioTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "read_file", "search_content", "code_navigate", "source_sanity", "edit_file", "write_file", "append_file", "mkdir", "shell",
		"discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_read_symbol", "go_definition", "go_references",
		"go_implementations", "go_calls_from", "go_calls_into", "go_extract_body", "go_importers",
		"go_diagnostics", "go_workspace_symbol", "go_related_files", "go_find_package", "go_find_type", "go_find_const":
		return true
	default:
		return false
	}
}

func legacyDiscoveryProfile(tool string) (class string, scope string, cost string) {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "pwd":
		return "workspace_context", "targeted", "low"
	case "list_files":
		return "workspace_listing", "broad", "low"
	case "search_files":
		return "file_search", "targeted", "low"
	case "search_content", "rg":
		return "text_search", "targeted", "low"
	case "read_file":
		return "file_read", "targeted", "low"
	case "code_navigate":
		return "language_symbol", "targeted", "low"
	case "source_sanity":
		return "diagnostics", "targeted", "medium"
	case "discovery_query":
		return "composite_query", "targeted", "medium"
	case "structural_search":
		return "ast_search", "targeted", "medium"
	case "dependency_graph":
		return "dependency_graph", "broad", "medium"
	case "history_range", "history_symbol", "blame_range":
		return "history", "targeted", "medium"
	case "ctags_symbols":
		return "symbol_index", "targeted", "medium"
	case "semgrep_scan", "joern_scan":
		return "security_scan", "broad", "high"
	case "task":
		return "delegated_discovery", "broad", "high"
	case "shell":
		return "other", "", ""
	default:
		return legacyLanguageDiscoveryProfile(tool)
	}
}

func legacyLanguageDiscoveryProfile(tool string) (class string, scope string, cost string) {
	for _, prefix := range []string{"go_", "cpp_", "python_", "ts_", "rust_", "java_"} {
		if strings.HasPrefix(tool, prefix) {
			suffix := strings.TrimPrefix(tool, prefix)
			switch suffix {
			case "overview", "entrypoints":
				return "language_overview", "broad", "medium"
			case "find_symbol", "find_function", "find_type", "find_const", "find_package", "workspace_symbol", "file_outline", "read_symbol":
				return "language_symbol", "targeted", "low"
			case "definition", "declaration", "references", "implementations", "calls_from", "calls_into", "extract_body", "related_files", "importers", "includes", "who_includes", "blast_radius", "dead_symbol":
				return "language_relationship", "targeted", "medium"
			case "diagnostics":
				return "diagnostics", "targeted", "medium"
			}
		}
	}
	return "other", "", ""
}

func Analyze(records []Record) Metrics {
	metrics := Metrics{
		Tools:            make(map[string]int),
		DiscoveryTools:   make(map[string]int),
		DiscoveryClasses: make(map[string]int),
		DiscoveryScopes:  make(map[string]int),
		DiscoveryCosts:   make(map[string]int),
	}
	verifyAgentSeen := false
	verifyHasFailure := false
	laneSummaryTurns := 0
	lastLaneSummary := ""
	var firstTS time.Time
	var lastTS time.Time
	for _, record := range records {
		if ts := timestampField(record.Data, "ts"); !ts.IsZero() {
			if firstTS.IsZero() || ts.Before(firstTS) {
				firstTS = ts
			}
			if lastTS.IsZero() || ts.After(lastTS) {
				lastTS = ts
			}
		}
		if rtype := strings.TrimSpace(record.Type); rtype != "" {
			if metrics.RecordTypes == nil {
				metrics.RecordTypes = map[string]int{}
			}
			metrics.RecordTypes[rtype]++
		}
		switch record.Type {
		case "session_start":
			if root := strings.TrimSpace(stringField(record.Data, "workspace_root")); root != "" {
				metrics.WorkspaceRoot = root
			}
		case "message":
			role := stringField(record.Data, "role")
			content := stringField(record.Data, "content")
			if trimmedRole := strings.TrimSpace(role); trimmedRole != "" {
				if metrics.MessageRoles == nil {
					metrics.MessageRoles = map[string]int{}
				}
				metrics.MessageRoles[trimmedRole]++
			}
			if strings.Contains(strings.ToLower(content), "summary rejected") {
				metrics.SummaryRejections++
				if lane := summaryRejectionLane(role); lane != "" {
					metrics.SummaryRejectionLanes = append(metrics.SummaryRejectionLanes, lane)
				}
			}
			switch role {
			case "user":
				metrics.LastUser = content
				metrics.UserTurns++
			case "assistant":
				metrics.FinalAssistant = content
				metrics.AssistantTurns++
			case "locate_summary", "explore_summary", "review_summary", "architecture_summary":
				if strings.TrimSpace(content) != "" {
					lastLaneSummary = content
					laneSummaryTurns++
				}
			case "route_plan", "route_plan_final":
				if plan := parseRoutePlan(content); plan.Raw != "" {
					metrics.RoutePlans = append(metrics.RoutePlans, plan)
				}
			case "replan_recommendation":
				if replan := parseReplanRecommendation(content); replan.Raw != "" {
					metrics.Replans = append(metrics.Replans, replan)
				}
			case "verification_plan":
				if plan := parseVerificationPlan(content); plan.Raw != "" {
					metrics.VerificationPlans = append(metrics.VerificationPlans, plan)
				}
			case "readiness_check":
				if readiness := parseReadinessCheck(content); readiness.Raw != "" {
					metrics.ReadinessChecks = append(metrics.ReadinessChecks, readiness)
					metrics.EvidencePaths = append(metrics.EvidencePaths, readiness.EvidencePaths...)
				}
			case "deepening_attempt":
				if deepening := parseDeepeningAttempt(content); deepening.Raw != "" {
					metrics.DeepeningAttempts = append(metrics.DeepeningAttempts, deepening)
				}
			case "final_answer_critic":
				if critic := parseFinalAnswerCritic(content); critic.Raw != "" {
					metrics.FinalAnswerCritics = append(metrics.FinalAnswerCritics, critic)
				}
			case "effort_policy":
				if policy := parseEffortPolicy(content); policy.Raw != "" {
					metrics.EffortPolicies = append(metrics.EffortPolicies, policy)
				}
			case "long_horizon_state":
				if state := parseLongHorizonState(content); state.Raw != "" {
					metrics.LongHorizonStates = append(metrics.LongHorizonStates, state)
				}
			case "workflow_state":
				if state := parseWorkflowState(content); state.Raw != "" {
					metrics.WorkflowStates = append(metrics.WorkflowStates, state)
				}
			case "budget_ledger":
				if ledger := parseBudgetLedger(content); ledger.Raw != "" {
					metrics.BudgetLedgers = append(metrics.BudgetLedgers, ledger)
				}
			case "evidence_delta":
				if delta := parseEvidenceDelta(content); delta.Raw != "" {
					metrics.EvidenceDeltas = append(metrics.EvidenceDeltas, delta)
				}
			case "claim_confidence":
				if confidence := parseClaimConfidence(content); confidence.Raw != "" {
					metrics.ClaimConfidences = append(metrics.ClaimConfidences, confidence)
				}
			case "context_compaction":
				if compaction := parseContextCompaction(content); compaction.Raw != "" {
					metrics.ContextCompactions = append(metrics.ContextCompactions, compaction)
				}
			case "route_ambiguity":
				if ambiguity := parseRouteAmbiguity(content); ambiguity.Raw != "" {
					metrics.RouteAmbiguities = append(metrics.RouteAmbiguities, ambiguity)
				}
			case "verification_feedback":
				if feedback := parseVerificationFeedback(content); feedback.Raw != "" {
					metrics.VerificationFeedback = append(metrics.VerificationFeedback, feedback)
				}
			case "tool_failure_confidence":
				if failure := parseToolFailureConfidence(content); failure.Raw != "" {
					metrics.ToolFailureConfidence = append(metrics.ToolFailureConfidence, failure)
				}
			case "control_rails":
				if rails := parseControlRails(content); rails.Raw != "" {
					metrics.ControlRails = append(metrics.ControlRails, rails)
				}
			}
			if caps := parseLanguageCapabilities(content); len(caps) > 0 {
				metrics.LanguageCapabilities = mergeLanguageCapabilities(metrics.LanguageCapabilities, caps)
			}
		case "tool_request":
			metrics.ToolRequests++
			tool := stringField(record.Data, "tool")
			if tool != "" {
				metrics.Tools[tool]++
			}
			class := strings.TrimSpace(stringField(record.Data, "discovery_class"))
			scope := strings.TrimSpace(stringField(record.Data, "discovery_scope"))
			cost := strings.TrimSpace(stringField(record.Data, "discovery_cost"))
			if class == "shell" {
				class, scope, cost = "other", "", ""
			}
			if class == "" || scope == "" || cost == "" {
				fallbackClass, fallbackScope, fallbackCost := legacyDiscoveryProfile(tool)
				if class == "" {
					class = fallbackClass
				}
				if scope == "" {
					scope = fallbackScope
				}
				if cost == "" {
					cost = fallbackCost
				}
			}
			if class != "" && class != "other" {
				metrics.DiscoveryTools[tool]++
				metrics.DiscoveryClasses[class]++
				if scope != "" {
					metrics.DiscoveryScopes[scope]++
				}
				if cost != "" {
					metrics.DiscoveryCosts[cost]++
					if cost == "high" {
						metrics.HeavyDiscoveryRequests++
					}
				}
			}
			if boolField(record.Data, "broad") {
				metrics.BroadToolRequests++
			}
		case "tool_result":
			summary := strings.ToLower(stringField(record.Data, "summary"))
			metrics.EvidencePaths = append(metrics.EvidencePaths, evidencePathsFromText(stringField(record.Data, "summary"))...)
			if strings.Contains(summary, "repeated dead-end action rejected") {
				metrics.RepeatedDeadEndRejections++
			}
			if strings.Contains(summary, "repeated tool request rejected") {
				metrics.RepeatedToolRejections++
			}
			if errText := strings.TrimSpace(stringField(record.Data, "error")); errText != "" {
				metrics.Errors = append(metrics.Errors, errText)
			}
			if stringField(record.Data, "agent") == "implement_verify" {
				verifyAgentSeen = true
				metrics.VerificationChecks++
				if strings.TrimSpace(stringField(record.Data, "error")) != "" {
					verifyHasFailure = true
				}
			}
		case "phase":
			metrics.Phases = append(metrics.Phases, PhaseSnapshot{
				Lane:   stringField(record.Data, "lane"),
				Phase:  stringField(record.Data, "phase"),
				State:  stringField(record.Data, "state"),
				Detail: stringField(record.Data, "detail"),
			})
		case "idiom.plan_execute.executor_handoff_packet":
			metrics.PlanExecutionHandoffs = append(metrics.PlanExecutionHandoffs, PlanExecutionHandoffSnapshot{
				PlanID:              stringField(record.Data, "plan_id"),
				HandoffID:           stringField(record.Data, "handoff_id"),
				ApprovalScopeID:     stringField(record.Data, "approval_scope_id"),
				ApprovalStatus:      stringField(record.Data, "approval_status"),
				ExecutionPermission: stringField(record.Data, "approval_execution_permission"),
				PacketState:         stringField(record.Data, "packet_state"),
				Reason:              stringField(record.Data, "reason"),
			})
		case "idiom.plan_execute.plan_scope_drift":
			metrics.PlanExecutionDrift = append(metrics.PlanExecutionDrift, PlanExecutionDriftSnapshot{
				Path:            stringField(record.Data, "path"),
				Reason:          stringField(record.Data, "reason"),
				HandoffID:       stringField(record.Data, "handoff_id"),
				ApprovalScopeID: stringField(record.Data, "approval_scope_id"),
				PacketState:     stringField(record.Data, "packet_state"),
			})
		case "error":
			if message := strings.TrimSpace(stringField(record.Data, "message")); message != "" {
				metrics.Errors = append(metrics.Errors, message)
			}
		}

		if strings.Contains(strings.ToLower(stringField(record.Data, "summary")), "write approval required") ||
			strings.Contains(strings.ToLower(stringField(record.Data, "error")), "write approval required") {
			metrics.ApprovalRequired = true
		}
	}
	if metrics.AssistantTurns == 0 && lastLaneSummary != "" {
		metrics.FinalAssistant = lastLaneSummary
		metrics.AssistantTurns = laneSummaryTurns
	} else if metrics.AssistantTurns < metrics.UserTurns && laneSummaryTurns > 0 {
		metrics.AssistantTurns = min(metrics.UserTurns, metrics.AssistantTurns+laneSummaryTurns)
	}
	if hasPhaseState(metrics.Phases, "implement", "verify", "done") {
		metrics.VerificationPassed = true
	}
	if hasPhaseState(metrics.Phases, "implement", "verify", "failed") {
		metrics.VerificationFailed = true
	}
	if !metrics.VerificationPassed && !metrics.VerificationFailed && verifyAgentSeen {
		metrics.VerificationFailed = verifyHasFailure
		metrics.VerificationPassed = !verifyHasFailure && metrics.VerificationChecks > 0
	}
	metrics.ChangedFiles = extractChangedFiles(metrics.FinalAssistant)
	if !firstTS.IsZero() && !lastTS.IsZero() && !lastTS.Before(firstTS) {
		metrics.DurationMillis = lastTS.Sub(firstTS).Milliseconds()
	}
	metrics.Errors = util.UniqueNonEmpty(metrics.Errors)
	metrics.EvidencePaths = util.UniqueNonEmpty(metrics.EvidencePaths)
	metrics.SummaryRejectionLanes = util.UniqueNonEmpty(metrics.SummaryRejectionLanes)
	metrics.LanguageCapabilities = mergeLanguageCapabilities(nil, metrics.LanguageCapabilities)
	return metrics
}

func Evaluate(logPath string, records []Record, scenario Scenario) Report {
	metrics := Analyze(records)
	report := Report{
		Scenario: scenario.Name,
		LogPath:  logPath,
		Passed:   true,
		Metrics:  metrics,
	}
	addFinding := func(format string, args ...any) {
		report.Passed = false
		report.Findings = append(report.Findings, fmt.Sprintf(format, args...))
	}

	lastUserLower := strings.ToLower(metrics.LastUser)
	for _, required := range scenario.PromptContains {
		if !strings.Contains(lastUserLower, strings.ToLower(strings.TrimSpace(required))) {
			addFinding("last user prompt does not contain %q", required)
		}
	}
	finalAssistantLower := strings.ToLower(metrics.FinalAssistant)
	for _, required := range scenario.FinalAssistantContains {
		if !strings.Contains(finalAssistantLower, strings.ToLower(strings.TrimSpace(required))) {
			addFinding("final assistant response does not contain %q", required)
		}
	}
	for _, forbidden := range scenario.ForbidFinalAssistantContains {
		if strings.Contains(finalAssistantLower, strings.ToLower(strings.TrimSpace(forbidden))) {
			addFinding("final assistant response unexpectedly contains %q", forbidden)
		}
	}
	if scenario.RequireAssistantForEachUser && metrics.AssistantTurns < metrics.UserTurns {
		addFinding("assistant turns fewer than user turns: got %d assistant for %d user", metrics.AssistantTurns, metrics.UserTurns)
	}
	for _, tool := range scenario.RequireTools {
		if metrics.Tools[strings.TrimSpace(tool)] == 0 {
			addFinding("required tool %q was not used", tool)
		}
	}
	for _, tool := range scenario.ForbidTools {
		if metrics.Tools[strings.TrimSpace(tool)] > 0 {
			addFinding("forbidden tool %q was used", tool)
		}
	}
	for _, tool := range scenario.RequireDiscoveryTools {
		if metrics.DiscoveryTools[strings.TrimSpace(tool)] == 0 {
			addFinding("required discovery tool %q was not used", tool)
		}
	}
	for _, class := range scenario.RequireDiscoveryClasses {
		if metrics.DiscoveryClasses[strings.TrimSpace(class)] == 0 {
			addFinding("required discovery class %q was not used", class)
		}
	}
	if scenario.MaxHeavyDiscoveryRequests != nil && metrics.HeavyDiscoveryRequests > *scenario.MaxHeavyDiscoveryRequests {
		addFinding("heavy discovery requests exceeded limit: got %d want <= %d", metrics.HeavyDiscoveryRequests, *scenario.MaxHeavyDiscoveryRequests)
	}
	for _, phase := range scenario.RequirePhases {
		if !containsPhase(metrics.Phases, phase) {
			addFinding("required phase was not observed: lane=%q phase=%q state=%q", phase.Lane, phase.Phase, phase.State)
		}
	}
	if scenario.RequireRoutePlan != nil {
		if len(metrics.RoutePlans) == 0 {
			addFinding("required route plan was not observed")
		} else if !routePlanMatches(metrics.RoutePlans[len(metrics.RoutePlans)-1], *scenario.RequireRoutePlan) {
			latest := metrics.RoutePlans[len(metrics.RoutePlans)-1]
			addFinding("route plan mismatch: got initial=%q followup=%q reason=%q contract=%q thoroughness=%q",
				latest.InitialLane,
				latest.FollowupLane,
				latest.Reason,
				latest.Contract,
				latest.Thoroughness,
			)
		}
	}
	for _, role := range scenario.RequireRunEvents {
		trimmed := strings.TrimSpace(role)
		if trimmed == "" {
			continue
		}
		// Match either a type:"message" role (runner-emitted RunEvents) or a
		// top-level record type (orchestrator-emitted events such as
		// idiom.plan_execute.preflight_failed). One field, two valid sources.
		if metrics.MessageRoles[trimmed] == 0 && metrics.RecordTypes[trimmed] == 0 {
			addFinding("required run event %q was not observed in log", trimmed)
		}
	}
	if scenario.RequireReplan != nil {
		if len(metrics.Replans) == 0 {
			addFinding("required replan recommendation was not observed")
		} else if !replanMatches(metrics.Replans[len(metrics.Replans)-1], *scenario.RequireReplan) {
			latest := metrics.Replans[len(metrics.Replans)-1]
			addFinding("replan recommendation mismatch: got from=%q to=%q reason=%q", latest.FromLane, latest.ToLane, latest.Reason)
		}
	}
	if scenario.MaxBroadToolRequests != nil && metrics.BroadToolRequests > *scenario.MaxBroadToolRequests {
		addFinding("broad tool requests exceeded limit: got %d want <= %d", metrics.BroadToolRequests, *scenario.MaxBroadToolRequests)
	}
	totalDeadEnds := metrics.RepeatedDeadEndRejections + metrics.RepeatedToolRejections
	if scenario.MaxRepeatedDeadEnds != nil && totalDeadEnds > *scenario.MaxRepeatedDeadEnds {
		addFinding("repeated dead-end requests exceeded limit: got %d want <= %d", totalDeadEnds, *scenario.MaxRepeatedDeadEnds)
	}
	if scenario.MaxSummaryRejections != nil && metrics.SummaryRejections > *scenario.MaxSummaryRejections {
		addFinding("summary rejections exceeded limit: got %d want <= %d", metrics.SummaryRejections, *scenario.MaxSummaryRejections)
	}
	switch strings.ToLower(strings.TrimSpace(scenario.RequireVerification)) {
	case "", "any":
	case "passed":
		if !metrics.VerificationPassed {
			addFinding("verification did not pass")
		}
	case "failed":
		if !metrics.VerificationFailed {
			addFinding("verification did not fail")
		}
	case "none":
		if metrics.VerificationPassed || metrics.VerificationFailed || metrics.VerificationChecks > 0 {
			addFinding("verification unexpectedly ran")
		}
	default:
		addFinding("unsupported require_verification value %q", scenario.RequireVerification)
	}
	if scenario.RequireVerificationPlan != nil {
		if len(metrics.VerificationPlans) == 0 {
			addFinding("required verification plan was not observed")
		} else if !verificationPlanMatches(metrics.VerificationPlans[len(metrics.VerificationPlans)-1], *scenario.RequireVerificationPlan) {
			latest := metrics.VerificationPlans[len(metrics.VerificationPlans)-1]
			addFinding("verification plan mismatch: got coverage=%q confidence=%q risk=%q full_suite=%t commands=%v expected_failures=%v",
				latest.Coverage,
				latest.Confidence,
				latest.RiskLevel,
				latest.UsesFullSuite,
				latest.Commands,
				latest.ExpectedFailures,
			)
		}
	}
	if scenario.RequireReadiness != nil {
		if len(metrics.ReadinessChecks) == 0 {
			addFinding("required readiness check was not observed")
		} else if !readinessMatches(metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1], *scenario.RequireReadiness) {
			latest := metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1]
			addFinding("readiness check mismatch: got lane=%q ready=%t confidence=%q action=%q evidence_paths=%d reason=%q",
				latest.Lane,
				latest.Ready,
				latest.Confidence,
				latest.Action,
				len(latest.EvidencePaths),
				latest.Reason,
			)
		}
	}
	if scenario.RequireDeepening != nil {
		if len(metrics.DeepeningAttempts) == 0 {
			addFinding("required deepening attempt was not observed")
		} else if !deepeningMatches(metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1], *scenario.RequireDeepening) {
			latest := metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1]
			addFinding("deepening attempt mismatch: got lane=%q attempted=%t exhausted=%t action=%q evidence_paths=%d reason=%q",
				latest.Lane,
				latest.Attempted,
				latest.Exhausted,
				latest.Action,
				len(latest.EvidencePaths),
				latest.Reason,
			)
		}
	}
	if scenario.RequireFinalAnswerCritic != nil {
		if len(metrics.FinalAnswerCritics) == 0 {
			addFinding("required final answer critic was not observed")
		} else if !finalAnswerCriticMatches(metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1], *scenario.RequireFinalAnswerCritic) {
			latest := metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1]
			addFinding("final answer critic mismatch: got contract=%q passed=%t action=%q repaired=%t findings=%v",
				latest.Contract,
				latest.Passed,
				latest.Action,
				latest.Repaired,
				latest.Findings,
			)
		}
	}
	if scenario.RequireEffortPolicy != nil {
		if len(metrics.EffortPolicies) == 0 {
			addFinding("required effort policy was not observed")
		} else if !effortPolicyMatches(metrics.EffortPolicies[len(metrics.EffortPolicies)-1], *scenario.RequireEffortPolicy) {
			latest := metrics.EffortPolicies[len(metrics.EffortPolicies)-1]
			addFinding("effort policy mismatch: got lane=%q level=%q analysis_budget=%d implement_budget=%d deepening=%d confidence_floor=%q exhaustion=%q",
				latest.Lane,
				latest.Level,
				latest.AnalysisToolBudget,
				latest.ImplementToolBudget,
				latest.DeepeningPasses,
				latest.ConfidenceFloor,
				latest.BudgetExhaustionMode,
			)
		}
	}
	if scenario.RequireLongHorizonState != nil {
		if len(metrics.LongHorizonStates) == 0 {
			addFinding("required long-horizon state was not observed")
		} else if !longHorizonStateMatches(metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1], *scenario.RequireLongHorizonState) {
			latest := metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1]
			addFinding("long-horizon state mismatch: got action=%q kind=%q source_lane=%q stage=%d focus=%q remaining=%v targets=%v resumable=%t next=%q",
				latest.Action,
				latest.MemoryKind,
				latest.SourceLane,
				latest.Stage,
				latest.FocusPath,
				latest.Remaining,
				latest.ChosenTargets,
				latest.Resumable,
				latest.NextStep,
			)
		}
	}
	if scenario.RequireWorkflowState != nil {
		if len(metrics.WorkflowStates) == 0 {
			addFinding("required workflow state was not observed")
		} else if !workflowStateMatches(metrics.WorkflowStates[len(metrics.WorkflowStates)-1], *scenario.RequireWorkflowState) {
			latest := metrics.WorkflowStates[len(metrics.WorkflowStates)-1]
			addFinding("workflow state mismatch: got action=%q kind=%q phase=%q source_lane=%q targets=%v editable=%v evidence=%v read_only=%v resumable=%t",
				latest.Action,
				latest.Kind,
				latest.Phase,
				latest.SourceLane,
				latest.TargetRefs,
				latest.EditableTargets,
				latest.EvidencePaths,
				latest.ReadOnlyEvidence,
				latest.Resumable,
			)
		}
	}
	if scenario.RequireBudgetLedger != nil {
		if len(metrics.BudgetLedgers) == 0 {
			addFinding("required budget ledger was not observed")
		} else if !budgetLedgerMatches(metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1], *scenario.RequireBudgetLedger) {
			latest := metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1]
			addFinding("budget ledger mismatch: got lane=%q stop=%q model_calls=%d evidence=%d exhausted=%t",
				latest.Lane, latest.StopReason, latest.UsedModelCalls, latest.TotalEvidencePaths, latest.BudgetExhausted)
		}
	}
	if scenario.RequireEvidenceDelta != nil {
		if len(metrics.EvidenceDeltas) == 0 {
			addFinding("required evidence delta was not observed")
		} else if !evidenceDeltaMatches(metrics.EvidenceDeltas[len(metrics.EvidenceDeltas)-1], *scenario.RequireEvidenceDelta) {
			latest := metrics.EvidenceDeltas[len(metrics.EvidenceDeltas)-1]
			addFinding("evidence delta mismatch: got lane=%q new_paths=%d marginal=%q tools=%d failed=%d",
				latest.Lane, len(latest.NewEvidencePaths), latest.MarginalValue, latest.ToolResults, latest.FailedTools)
		}
	}
	if scenario.RequireClaimConfidence != nil {
		if len(metrics.ClaimConfidences) == 0 {
			addFinding("required claim confidence was not observed")
		} else if !claimConfidenceMatches(metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1], *scenario.RequireClaimConfidence) {
			latest := metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1]
			addFinding("claim confidence mismatch: got lane=%q confidence=%q evidence=%d unresolved=%d basis=%q",
				latest.Lane, latest.Confidence, len(latest.EvidencePaths), len(latest.UnresolvedUncertainty), latest.CalibrationBasis)
		}
	}
	if scenario.RequireContextCompaction != nil {
		if len(metrics.ContextCompactions) == 0 {
			addFinding("required context compaction was not observed")
		} else if !contextCompactionMatches(metrics.ContextCompactions[len(metrics.ContextCompactions)-1], *scenario.RequireContextCompaction) {
			latest := metrics.ContextCompactions[len(metrics.ContextCompactions)-1]
			addFinding("context compaction mismatch: got dropped=%d retained=%d max_history=%d pinned=%v memory=%q strategy=%q",
				latest.DroppedMessages,
				latest.RetainedMessages,
				latest.MaxHistory,
				latest.PinnedPaths,
				latest.PinnedMemoryKind,
				latest.Strategy,
			)
		}
	}
	if scenario.RequireRouteAmbiguity != nil {
		if len(metrics.RouteAmbiguities) == 0 {
			addFinding("required route ambiguity was not observed")
		} else if !routeAmbiguityMatches(metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1], *scenario.RequireRouteAmbiguity) {
			latest := metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1]
			addFinding("route ambiguity mismatch: got selected=%q ambiguous=%t signals=%v tie_break=%q",
				latest.SelectedLane, latest.Ambiguous, latest.Signals, latest.TieBreak)
		}
	}
	if scenario.RequireVerificationFeedback != nil {
		if len(metrics.VerificationFeedback) == 0 {
			addFinding("required verification feedback was not observed")
		} else if !verificationFeedbackMatches(metrics.VerificationFeedback[len(metrics.VerificationFeedback)-1], *scenario.RequireVerificationFeedback) {
			latest := metrics.VerificationFeedback[len(metrics.VerificationFeedback)-1]
			addFinding("verification feedback mismatch: got confidence=%q failed=%v recommendation=%q",
				latest.Confidence, latest.FailedCommands, latest.LearnedRecommendation)
		}
	}
	if scenario.RequireToolFailureConfidence != nil {
		if len(metrics.ToolFailureConfidence) == 0 {
			addFinding("required tool-failure confidence was not observed")
		} else if !toolFailureConfidenceMatches(metrics.ToolFailureConfidence[len(metrics.ToolFailureConfidence)-1], *scenario.RequireToolFailureConfidence) {
			latest := metrics.ToolFailureConfidence[len(metrics.ToolFailureConfidence)-1]
			addFinding("tool-failure confidence mismatch: got lane=%q tool=%q impact=%q",
				latest.Lane, latest.Tool, latest.ConfidenceImpact)
		}
	}
	if scenario.RequireControlRails != nil {
		if len(metrics.ControlRails) == 0 {
			addFinding("required control rails were not observed")
		} else if !controlRailsMatches(metrics.ControlRails[len(metrics.ControlRails)-1], *scenario.RequireControlRails) {
			latest := metrics.ControlRails[len(metrics.ControlRails)-1]
			addFinding("control rails mismatch: got lane=%q followup=%q contract=%q family=%q permission=%q targets=%v evidence=%v tools=%v verification=%q failure_to_eval=%t",
				latest.Lane,
				latest.FollowupLane,
				latest.Contract,
				latest.TaskFamily,
				latest.PermissionProfile,
				latest.TargetRefs,
				latest.RequiredEvidence,
				latest.RequiredTools,
				latest.VerificationContract,
				latest.FailureToEval,
			)
		}
	}
	for _, required := range scenario.RequireChangedFiles {
		if !containsStringFold(metrics.ChangedFiles, required) {
			addFinding("changed files do not include %q", required)
		}
	}
	for _, required := range scenario.RequireEvidencePaths {
		if !containsStringFold(metrics.EvidencePaths, required) {
			addFinding("evidence paths do not include %q", required)
		}
	}
	for _, required := range scenario.RequireLanguageCapabilities {
		if !languageCapabilityMatches(metrics.LanguageCapabilities, required) {
			addFinding("language capabilities do not include language=%q tool=%q signal=%q", required.Language, required.Tool, required.Signal)
		}
	}
	if scenario.RequireApproval != nil && metrics.ApprovalRequired != *scenario.RequireApproval {
		addFinding("approval requirement mismatch: got %t want %t", metrics.ApprovalRequired, *scenario.RequireApproval)
	}
	if scenario.RequirePlanExecution != nil {
		for _, finding := range evaluatePlanExecutionExpectation(metrics, *scenario.RequirePlanExecution) {
			addFinding("%s", finding)
		}
	}
	return report
}

func evaluatePlanExecutionExpectation(metrics Metrics, expected PlanExecutionExpectation) []string {
	var findings []string
	if expected.RequireHandoffID && !planExecutionHasHandoffID(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution handoff id was required but not observed")
	}
	if expected.RequireApprovalScopeID && !planExecutionHasApprovalScopeID(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution approval scope id was required but not observed")
	}
	if expected.RequireNoDrift && len(metrics.PlanExecutionDrift) > 0 {
		findings = append(findings, fmt.Sprintf("plan execution drift was forbidden but observed %d drift event(s)", len(metrics.PlanExecutionDrift)))
	}
	if expected.RequireDrift && len(metrics.PlanExecutionDrift) == 0 {
		findings = append(findings, "plan execution drift was required but not observed")
	}
	for _, reason := range expected.RequireDriftReasons {
		reason = normalizedCoverageKey(reason)
		if reason == "" {
			continue
		}
		if !planExecutionHasDriftReason(metrics.PlanExecutionDrift, reason) {
			findings = append(findings, fmt.Sprintf("plan execution drift reason %q was not observed", reason))
		}
	}
	for _, state := range expected.RequirePacketStates {
		state = normalizedCoverageKey(state)
		if state == "" {
			continue
		}
		if !planExecutionHasPacketState(metrics.PlanExecutionHandoffs, state) {
			findings = append(findings, fmt.Sprintf("plan execution packet state %q was not observed", state))
		}
	}
	for _, permission := range expected.RequireExecutionPermissions {
		permission = normalizedCoverageKey(permission)
		if permission == "" {
			continue
		}
		if !planExecutionHasPermission(metrics.PlanExecutionHandoffs, permission) {
			findings = append(findings, fmt.Sprintf("plan execution permission %q was not observed", permission))
		}
	}
	return findings
}

func planExecutionHasHandoffID(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if strings.TrimSpace(handoff.HandoffID) != "" {
			return true
		}
	}
	return false
}

func planExecutionHasApprovalScopeID(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if strings.TrimSpace(handoff.ApprovalScopeID) != "" {
			return true
		}
	}
	return false
}

func planExecutionHasDriftReason(drifts []PlanExecutionDriftSnapshot, reason string) bool {
	for _, drift := range drifts {
		if normalizedCoverageKey(drift.Reason) == reason {
			return true
		}
	}
	return false
}

func planExecutionHasPacketState(handoffs []PlanExecutionHandoffSnapshot, state string) bool {
	for _, handoff := range handoffs {
		if normalizedCoverageKey(handoff.PacketState) == state {
			return true
		}
	}
	return false
}

func planExecutionHasPermission(handoffs []PlanExecutionHandoffSnapshot, permission string) bool {
	for _, handoff := range handoffs {
		if normalizedCoverageKey(handoff.ExecutionPermission) == permission {
			return true
		}
	}
	return false
}

func containsPhase(phases []PhaseSnapshot, expected PhaseExpectation) bool {
	for _, phase := range phases {
		if expected.Lane != "" && !strings.EqualFold(phase.Lane, expected.Lane) {
			continue
		}
		if expected.Phase != "" && !strings.EqualFold(phase.Phase, expected.Phase) {
			continue
		}
		if expected.State != "" && !strings.EqualFold(phase.State, expected.State) {
			continue
		}
		return true
	}
	return false
}

func parseRoutePlan(content string) RoutePlanSnapshot {
	content = strings.TrimSpace(content)
	plan := RoutePlanSnapshot{Raw: content}
	if content == "" {
		return RoutePlanSnapshot{}
	}
	for _, field := range strings.Fields(content) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "initial":
			plan.InitialLane = value
		case "followup":
			plan.FollowupLane = value
		case "reason":
			plan.Reason = value
		case "contract":
			plan.Contract = value
		case "mode":
			plan.AgentMode = value
		case "workflow":
			plan.WorkflowKind = value
		case "phase":
			plan.WorkflowPhase = value
		case "thoroughness":
			plan.Thoroughness = value
		}
	}
	return plan
}

func routePlanMatches(actual RoutePlanSnapshot, expected RoutePlanExpectation) bool {
	if expected.InitialLane != "" && !strings.EqualFold(actual.InitialLane, expected.InitialLane) {
		return false
	}
	if expected.FollowupLane != "" && !strings.EqualFold(actual.FollowupLane, expected.FollowupLane) {
		return false
	}
	if expected.Reason != "" && !strings.EqualFold(actual.Reason, expected.Reason) {
		return false
	}
	if expected.Contract != "" && !strings.EqualFold(actual.Contract, expected.Contract) {
		return false
	}
	if expected.AgentMode != "" && !strings.EqualFold(actual.AgentMode, expected.AgentMode) {
		return false
	}
	if expected.WorkflowKind != "" && !strings.EqualFold(actual.WorkflowKind, expected.WorkflowKind) {
		return false
	}
	if expected.WorkflowPhase != "" && !strings.EqualFold(actual.WorkflowPhase, expected.WorkflowPhase) {
		return false
	}
	if expected.Thoroughness != "" && !strings.EqualFold(actual.Thoroughness, expected.Thoroughness) {
		return false
	}
	return true
}

func parseReplanRecommendation(content string) ReplanSnapshot {
	content = strings.TrimSpace(content)
	replan := ReplanSnapshot{Raw: content}
	if content == "" {
		return ReplanSnapshot{}
	}
	head, reason, hasReason := strings.Cut(content, ":")
	if hasReason {
		replan.Reason = strings.TrimSpace(reason)
	} else {
		head = content
	}
	from, to, ok := strings.Cut(strings.TrimSpace(head), "->")
	if ok {
		replan.FromLane = strings.TrimSpace(from)
		replan.ToLane = strings.TrimSpace(to)
	}
	return replan
}

func replanMatches(actual ReplanSnapshot, expected ReplanExpectation) bool {
	if expected.FromLane != "" && !strings.EqualFold(actual.FromLane, expected.FromLane) {
		return false
	}
	if expected.ToLane != "" && !strings.EqualFold(actual.ToLane, expected.ToLane) {
		return false
	}
	if expected.Reason != "" && !strings.Contains(strings.ToLower(actual.Reason), strings.ToLower(strings.TrimSpace(expected.Reason))) {
		return false
	}
	return true
}

func parseVerificationPlan(content string) VerificationPlanSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return VerificationPlanSnapshot{}
	}
	var plan VerificationPlanSnapshot
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return VerificationPlanSnapshot{}
	}
	plan.Raw = content
	plan.Commands = util.UniqueNonEmpty(plan.Commands)
	plan.Reasons = util.UniqueNonEmpty(plan.Reasons)
	plan.ExpectedFailures = util.UniqueNonEmpty(plan.ExpectedFailures)
	plan.Coverage = strings.TrimSpace(plan.Coverage)
	plan.Confidence = strings.TrimSpace(plan.Confidence)
	plan.RiskLevel = strings.TrimSpace(plan.RiskLevel)
	plan.RepairPolicy = strings.TrimSpace(plan.RepairPolicy)
	return plan
}

func verificationPlanMatches(actual VerificationPlanSnapshot, expected VerificationPlanExpectation) bool {
	if expected.Coverage != "" && !strings.EqualFold(actual.Coverage, expected.Coverage) {
		return false
	}
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	if expected.RiskLevel != "" && !strings.EqualFold(actual.RiskLevel, expected.RiskLevel) {
		return false
	}
	if expected.UsesFullSuite != nil && actual.UsesFullSuite != *expected.UsesFullSuite {
		return false
	}
	for _, command := range expected.RequireCommands {
		if !containsStringFold(actual.Commands, command) {
			return false
		}
	}
	for _, failure := range expected.RequireExpectedFailures {
		if !containsStringFold(actual.ExpectedFailures, failure) {
			return false
		}
	}
	return true
}

func parseReadinessCheck(content string) ReadinessSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ReadinessSnapshot{}
	}
	var readiness ReadinessSnapshot
	if err := json.Unmarshal([]byte(content), &readiness); err != nil {
		return ReadinessSnapshot{}
	}
	readiness.Raw = content
	readiness.Lane = strings.TrimSpace(readiness.Lane)
	readiness.Confidence = strings.TrimSpace(readiness.Confidence)
	readiness.Action = strings.TrimSpace(readiness.Action)
	readiness.Reason = strings.TrimSpace(readiness.Reason)
	readiness.EvidencePaths = util.UniqueNonEmpty(readiness.EvidencePaths)
	return readiness
}

func readinessMatches(actual ReadinessSnapshot, expected ReadinessExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Ready != nil && actual.Ready != *expected.Ready {
		return false
	}
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.MinEvidencePaths != nil && len(actual.EvidencePaths) < *expected.MinEvidencePaths {
		return false
	}
	reason := strings.ToLower(actual.Reason)
	for _, required := range expected.ReasonContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(reason, required) {
			return false
		}
	}
	return true
}

func parseDeepeningAttempt(content string) DeepeningSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return DeepeningSnapshot{}
	}
	var deepening DeepeningSnapshot
	if err := json.Unmarshal([]byte(content), &deepening); err != nil {
		return DeepeningSnapshot{}
	}
	deepening.Raw = content
	deepening.Lane = strings.TrimSpace(deepening.Lane)
	deepening.Action = strings.TrimSpace(deepening.Action)
	deepening.Reason = strings.TrimSpace(deepening.Reason)
	deepening.EvidencePaths = util.UniqueNonEmpty(deepening.EvidencePaths)
	return deepening
}

func deepeningMatches(actual DeepeningSnapshot, expected DeepeningExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Attempted != nil && actual.Attempted != *expected.Attempted {
		return false
	}
	if expected.Exhausted != nil && actual.Exhausted != *expected.Exhausted {
		return false
	}
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.MinEvidencePaths != nil && len(actual.EvidencePaths) < *expected.MinEvidencePaths {
		return false
	}
	reason := strings.ToLower(actual.Reason)
	for _, required := range expected.ReasonContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(reason, required) {
			return false
		}
	}
	return true
}

func parseFinalAnswerCritic(content string) FinalAnswerCriticSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return FinalAnswerCriticSnapshot{}
	}
	var critic FinalAnswerCriticSnapshot
	if err := json.Unmarshal([]byte(content), &critic); err != nil {
		return FinalAnswerCriticSnapshot{}
	}
	critic.Raw = content
	critic.Contract = strings.TrimSpace(critic.Contract)
	critic.Action = strings.TrimSpace(critic.Action)
	critic.Findings = util.UniqueNonEmpty(critic.Findings)
	return critic
}

func finalAnswerCriticMatches(actual FinalAnswerCriticSnapshot, expected FinalAnswerCriticExpectation) bool {
	if expected.Contract != "" && !strings.EqualFold(actual.Contract, expected.Contract) {
		return false
	}
	if expected.Passed != nil && actual.Passed != *expected.Passed {
		return false
	}
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.Repaired != nil && actual.Repaired != *expected.Repaired {
		return false
	}
	findings := strings.ToLower(strings.Join(actual.Findings, "\n"))
	for _, required := range expected.RequireFindings {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(findings, required) {
			return false
		}
	}
	return true
}

func parseEffortPolicy(content string) EffortPolicySnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return EffortPolicySnapshot{}
	}
	var policy EffortPolicySnapshot
	if err := json.Unmarshal([]byte(content), &policy); err != nil {
		return EffortPolicySnapshot{}
	}
	policy.Raw = content
	policy.Lane = strings.TrimSpace(policy.Lane)
	policy.Level = strings.TrimSpace(policy.Level)
	policy.ConfidenceFloor = strings.TrimSpace(policy.ConfidenceFloor)
	policy.BudgetExhaustionMode = strings.TrimSpace(policy.BudgetExhaustionMode)
	return policy
}

func effortPolicyMatches(actual EffortPolicySnapshot, expected EffortPolicyExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Level != "" && !strings.EqualFold(actual.Level, expected.Level) {
		return false
	}
	if expected.MinAnalysisToolBudget != nil && actual.AnalysisToolBudget < *expected.MinAnalysisToolBudget {
		return false
	}
	if expected.MinImplementToolBudget != nil && actual.ImplementToolBudget < *expected.MinImplementToolBudget {
		return false
	}
	if expected.MinDeepeningPasses != nil && actual.DeepeningPasses < *expected.MinDeepeningPasses {
		return false
	}
	if expected.MinMaxOutputTokens != nil && actual.MaxOutputTokens < *expected.MinMaxOutputTokens {
		return false
	}
	if expected.ConfidenceFloor != "" && !strings.EqualFold(actual.ConfidenceFloor, expected.ConfidenceFloor) {
		return false
	}
	if expected.BudgetExhaustionMode != "" && !strings.EqualFold(actual.BudgetExhaustionMode, expected.BudgetExhaustionMode) {
		return false
	}
	return true
}

func parseLongHorizonState(content string) LongHorizonStateSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return LongHorizonStateSnapshot{}
	}
	var state LongHorizonStateSnapshot
	if err := json.Unmarshal([]byte(content), &state); err != nil {
		return LongHorizonStateSnapshot{}
	}
	state.Raw = content
	state.Action = strings.TrimSpace(state.Action)
	state.MemoryKind = strings.TrimSpace(state.MemoryKind)
	state.SourceLane = strings.TrimSpace(state.SourceLane)
	state.FocusPath = strings.TrimSpace(state.FocusPath)
	state.Changed = util.UniqueNonEmpty(state.Changed)
	state.Remaining = util.UniqueNonEmpty(state.Remaining)
	state.OpenGaps = util.UniqueNonEmpty(state.OpenGaps)
	state.AttemptedSearches = util.UniqueNonEmpty(state.AttemptedSearches)
	state.ChosenTargets = util.UniqueNonEmpty(state.ChosenTargets)
	state.VerificationHistory = util.UniqueNonEmpty(state.VerificationHistory)
	state.NextActions = util.UniqueNonEmpty(state.NextActions)
	state.NextStep = strings.TrimSpace(state.NextStep)
	state.Verification = strings.TrimSpace(state.Verification)
	state.Reason = strings.TrimSpace(state.Reason)
	return state
}

func longHorizonStateMatches(actual LongHorizonStateSnapshot, expected LongHorizonStateExpectation) bool {
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.MemoryKind != "" && !strings.EqualFold(actual.MemoryKind, expected.MemoryKind) {
		return false
	}
	if expected.SourceLane != "" && !strings.EqualFold(actual.SourceLane, expected.SourceLane) {
		return false
	}
	if expected.Stage != nil && actual.Stage != *expected.Stage {
		return false
	}
	if expected.FocusPath != "" && !strings.EqualFold(actual.FocusPath, expected.FocusPath) {
		return false
	}
	for _, required := range expected.RequireRemaining {
		if !containsStringFold(actual.Remaining, required) {
			return false
		}
	}
	for _, required := range expected.RequireOpenGaps {
		if !containsStringFold(actual.OpenGaps, required) {
			return false
		}
	}
	for _, required := range expected.RequireAttemptedSearches {
		if !containsStringFold(actual.AttemptedSearches, required) {
			return false
		}
	}
	for _, required := range expected.RequireChosenTargets {
		if !containsStringFold(actual.ChosenTargets, required) {
			return false
		}
	}
	for _, required := range expected.RequireVerificationHistory {
		if !containsStringFold(actual.VerificationHistory, required) {
			return false
		}
	}
	for _, required := range expected.RequireNextActions {
		if !containsStringFold(actual.NextActions, required) {
			return false
		}
	}
	next := strings.ToLower(actual.NextStep)
	if next == "" {
		next = strings.ToLower(strings.Join(actual.NextActions, "\n"))
	}
	for _, required := range expected.NextContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(next, required) {
			return false
		}
	}
	if expected.Resumable != nil && actual.Resumable != *expected.Resumable {
		return false
	}
	return true
}

func parseWorkflowState(content string) WorkflowStateSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return WorkflowStateSnapshot{}
	}
	var state WorkflowStateSnapshot
	if err := json.Unmarshal([]byte(content), &state); err != nil {
		return WorkflowStateSnapshot{}
	}
	state.Raw = content
	state.Action = strings.TrimSpace(state.Action)
	state.Kind = strings.TrimSpace(state.Kind)
	state.Phase = strings.TrimSpace(state.Phase)
	state.SourceLane = strings.TrimSpace(state.SourceLane)
	state.Objective = strings.TrimSpace(state.Objective)
	state.TargetRefs = util.UniqueNonEmpty(state.TargetRefs)
	state.EditableTargets = util.UniqueNonEmpty(state.EditableTargets)
	state.ReadOnlyEvidence = util.UniqueNonEmpty(state.ReadOnlyEvidence)
	state.EvidencePaths = util.UniqueNonEmpty(state.EvidencePaths)
	state.AllowedDestinations = util.UniqueNonEmpty(state.AllowedDestinations)
	state.Verification = util.UniqueNonEmpty(state.Verification)
	state.NextActions = util.UniqueNonEmpty(state.NextActions)
	state.Reason = strings.TrimSpace(state.Reason)
	return state
}

func workflowStateMatches(actual WorkflowStateSnapshot, expected WorkflowStateExpectation) bool {
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.Kind != "" && !strings.EqualFold(actual.Kind, expected.Kind) {
		return false
	}
	if expected.Phase != "" && !strings.EqualFold(actual.Phase, expected.Phase) {
		return false
	}
	if expected.SourceLane != "" && !strings.EqualFold(actual.SourceLane, expected.SourceLane) {
		return false
	}
	for _, required := range expected.RequireTargetRefs {
		if !containsStringFold(actual.TargetRefs, required) {
			return false
		}
	}
	for _, required := range expected.RequireEditable {
		if !containsStringFold(actual.EditableTargets, required) {
			return false
		}
	}
	for _, required := range expected.RequireEvidence {
		if !containsStringFold(actual.EvidencePaths, required) {
			return false
		}
	}
	for _, required := range expected.RequireReadOnly {
		if !containsStringFold(actual.ReadOnlyEvidence, required) {
			return false
		}
	}
	for _, required := range expected.RequireDestinations {
		if !containsStringFold(actual.AllowedDestinations, required) {
			return false
		}
	}
	for _, required := range expected.RequireVerification {
		if !containsStringFold(actual.Verification, required) {
			return false
		}
	}
	for _, required := range expected.RequireNextActions {
		if !containsStringFold(actual.NextActions, required) {
			return false
		}
	}
	if expected.Resumable != nil && actual.Resumable != *expected.Resumable {
		return false
	}
	return true
}

func parseBudgetLedger(content string) BudgetLedgerSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return BudgetLedgerSnapshot{}
	}
	var ledger BudgetLedgerSnapshot
	if err := json.Unmarshal([]byte(content), &ledger); err != nil {
		return BudgetLedgerSnapshot{}
	}
	ledger.Raw = content
	ledger.Lane = strings.TrimSpace(ledger.Lane)
	ledger.Level = strings.TrimSpace(ledger.Level)
	ledger.StopReason = strings.TrimSpace(ledger.StopReason)
	ledger.NextAllowedActions = util.UniqueNonEmpty(ledger.NextAllowedActions)
	ledger.MarginalEvidencePolicy = strings.TrimSpace(ledger.MarginalEvidencePolicy)
	ledger.UserVisibleUncertainty = strings.TrimSpace(ledger.UserVisibleUncertainty)
	return ledger
}

func budgetLedgerMatches(actual BudgetLedgerSnapshot, expected BudgetLedgerExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.StopReason != "" && !strings.Contains(strings.ToLower(actual.StopReason), strings.ToLower(strings.TrimSpace(expected.StopReason))) {
		return false
	}
	if expected.MinModelCalls != nil && actual.UsedModelCalls < *expected.MinModelCalls {
		return false
	}
	if expected.MinTotalTokens != nil && actual.TotalTokens < *expected.MinTotalTokens {
		return false
	}
	if expected.MinEvidencePaths != nil && actual.TotalEvidencePaths < *expected.MinEvidencePaths {
		return false
	}
	if expected.BudgetExhausted != nil && actual.BudgetExhausted != *expected.BudgetExhausted {
		return false
	}
	if expected.UncertaintyPolicy != "" && !strings.Contains(strings.ToLower(actual.UserVisibleUncertainty), strings.ToLower(strings.TrimSpace(expected.UncertaintyPolicy))) {
		return false
	}
	return true
}

func parseEvidenceDelta(content string) EvidenceDeltaSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return EvidenceDeltaSnapshot{}
	}
	var delta EvidenceDeltaSnapshot
	if err := json.Unmarshal([]byte(content), &delta); err != nil {
		return EvidenceDeltaSnapshot{}
	}
	delta.Raw = content
	delta.Lane = strings.TrimSpace(delta.Lane)
	delta.NewEvidencePaths = util.UniqueNonEmpty(delta.NewEvidencePaths)
	delta.MarginalValue = strings.TrimSpace(delta.MarginalValue)
	return delta
}

func evidenceDeltaMatches(actual EvidenceDeltaSnapshot, expected EvidenceDeltaExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.MinNewPaths != nil && len(actual.NewEvidencePaths) < *expected.MinNewPaths {
		return false
	}
	if expected.MarginalValue != "" && !strings.EqualFold(actual.MarginalValue, expected.MarginalValue) {
		return false
	}
	if expected.MinToolResults != nil && actual.ToolResults < *expected.MinToolResults {
		return false
	}
	if expected.MaxFailedTools != nil && actual.FailedTools > *expected.MaxFailedTools {
		return false
	}
	return true
}

func parseClaimConfidence(content string) ClaimConfidenceSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ClaimConfidenceSnapshot{}
	}
	var confidence ClaimConfidenceSnapshot
	if err := json.Unmarshal([]byte(content), &confidence); err != nil {
		return ClaimConfidenceSnapshot{}
	}
	confidence.Raw = content
	confidence.Lane = strings.TrimSpace(confidence.Lane)
	confidence.Confidence = strings.TrimSpace(confidence.Confidence)
	confidence.EvidencePaths = util.UniqueNonEmpty(confidence.EvidencePaths)
	confidence.UnresolvedUncertainty = util.UniqueNonEmpty(confidence.UnresolvedUncertainty)
	confidence.Contradictions = util.UniqueNonEmpty(confidence.Contradictions)
	confidence.CalibrationBasis = strings.TrimSpace(confidence.CalibrationBasis)
	return confidence
}

func claimConfidenceMatches(actual ClaimConfidenceSnapshot, expected ClaimConfidenceExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	if expected.MinEvidencePaths != nil && len(actual.EvidencePaths) < *expected.MinEvidencePaths {
		return false
	}
	if expected.MaxUnresolved != nil && len(actual.UnresolvedUncertainty) > *expected.MaxUnresolved {
		return false
	}
	if expected.CalibrationBasis != "" && !strings.Contains(strings.ToLower(actual.CalibrationBasis), strings.ToLower(strings.TrimSpace(expected.CalibrationBasis))) {
		return false
	}
	return true
}

func parseContextCompaction(content string) ContextCompactionSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ContextCompactionSnapshot{}
	}
	var compaction ContextCompactionSnapshot
	if err := json.Unmarshal([]byte(content), &compaction); err != nil {
		return ContextCompactionSnapshot{}
	}
	compaction.Raw = content
	compaction.PinnedPaths = util.UniqueNonEmpty(compaction.PinnedPaths)
	compaction.PinnedMemoryKind = strings.TrimSpace(compaction.PinnedMemoryKind)
	compaction.Strategy = strings.TrimSpace(compaction.Strategy)
	return compaction
}

func contextCompactionMatches(actual ContextCompactionSnapshot, expected ContextCompactionExpectation) bool {
	if expected.MinDroppedMessages != nil && actual.DroppedMessages < *expected.MinDroppedMessages {
		return false
	}
	if expected.MinRetainedMessages != nil && actual.RetainedMessages < *expected.MinRetainedMessages {
		return false
	}
	if expected.MaxHistory != nil && actual.MaxHistory != *expected.MaxHistory {
		return false
	}
	for _, path := range expected.RequirePinnedPaths {
		if !containsStringFold(actual.PinnedPaths, path) {
			return false
		}
	}
	if expected.PinnedMemoryKind != "" && !strings.EqualFold(actual.PinnedMemoryKind, expected.PinnedMemoryKind) {
		return false
	}
	strategy := strings.ToLower(actual.Strategy)
	for _, required := range expected.StrategyContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(strategy, required) {
			return false
		}
	}
	return true
}

func parseRouteAmbiguity(content string) RouteAmbiguitySnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return RouteAmbiguitySnapshot{}
	}
	var ambiguity RouteAmbiguitySnapshot
	if err := json.Unmarshal([]byte(content), &ambiguity); err != nil {
		return RouteAmbiguitySnapshot{}
	}
	ambiguity.Raw = content
	ambiguity.SelectedLane = strings.TrimSpace(ambiguity.SelectedLane)
	ambiguity.Reason = strings.TrimSpace(ambiguity.Reason)
	ambiguity.Signals = util.UniqueNonEmpty(ambiguity.Signals)
	ambiguity.TieBreak = strings.TrimSpace(ambiguity.TieBreak)
	return ambiguity
}

func routeAmbiguityMatches(actual RouteAmbiguitySnapshot, expected RouteAmbiguityExpectation) bool {
	if expected.SelectedLane != "" && !strings.EqualFold(actual.SelectedLane, expected.SelectedLane) {
		return false
	}
	if expected.Ambiguous != nil && actual.Ambiguous != *expected.Ambiguous {
		return false
	}
	for _, signal := range expected.RequireSignals {
		if !containsStringFold(actual.Signals, signal) {
			return false
		}
	}
	tieBreak := strings.ToLower(actual.TieBreak)
	for _, required := range expected.TieBreakContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(tieBreak, required) {
			return false
		}
	}
	return true
}

func parseVerificationFeedback(content string) VerificationFeedbackSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return VerificationFeedbackSnapshot{}
	}
	var feedback VerificationFeedbackSnapshot
	if err := json.Unmarshal([]byte(content), &feedback); err != nil {
		return VerificationFeedbackSnapshot{}
	}
	feedback.Raw = content
	feedback.RiskLevel = strings.TrimSpace(feedback.RiskLevel)
	feedback.Coverage = strings.TrimSpace(feedback.Coverage)
	feedback.Confidence = strings.TrimSpace(feedback.Confidence)
	feedback.Commands = util.UniqueNonEmpty(feedback.Commands)
	feedback.FailedCommands = util.UniqueNonEmpty(feedback.FailedCommands)
	feedback.FailureClasses = util.UniqueNonEmpty(feedback.FailureClasses)
	feedback.LearnedRecommendation = strings.TrimSpace(feedback.LearnedRecommendation)
	return feedback
}

func verificationFeedbackMatches(actual VerificationFeedbackSnapshot, expected VerificationFeedbackExpectation) bool {
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	for _, command := range expected.RequireFailedCommands {
		if !containsStringFold(actual.FailedCommands, command) {
			return false
		}
	}
	for _, class := range expected.RequireFailureClasses {
		if !containsStringFold(actual.FailureClasses, class) {
			return false
		}
	}
	recommendation := strings.ToLower(actual.LearnedRecommendation)
	for _, required := range expected.RecommendationContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(recommendation, required) {
			return false
		}
	}
	return true
}

func parseToolFailureConfidence(content string) ToolFailureConfidenceSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ToolFailureConfidenceSnapshot{}
	}
	var failure ToolFailureConfidenceSnapshot
	if err := json.Unmarshal([]byte(content), &failure); err != nil {
		return ToolFailureConfidenceSnapshot{}
	}
	failure.Raw = content
	failure.Lane = strings.TrimSpace(failure.Lane)
	failure.Tool = strings.TrimSpace(failure.Tool)
	failure.Action = strings.TrimSpace(failure.Action)
	failure.ConfidenceImpact = strings.TrimSpace(failure.ConfidenceImpact)
	failure.Reason = strings.TrimSpace(failure.Reason)
	return failure
}

func toolFailureConfidenceMatches(actual ToolFailureConfidenceSnapshot, expected ToolFailureConfidenceExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Tool != "" && !strings.EqualFold(actual.Tool, expected.Tool) {
		return false
	}
	if expected.ConfidenceImpact != "" && !strings.EqualFold(actual.ConfidenceImpact, expected.ConfidenceImpact) {
		return false
	}
	return true
}

func parseControlRails(content string) ControlRailsSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ControlRailsSnapshot{}
	}
	var rails ControlRailsSnapshot
	if err := json.Unmarshal([]byte(content), &rails); err != nil {
		return ControlRailsSnapshot{}
	}
	rails.Raw = content
	rails.Lane = strings.TrimSpace(rails.Lane)
	rails.FollowupLane = strings.TrimSpace(rails.FollowupLane)
	rails.Contract = strings.TrimSpace(rails.Contract)
	rails.AgentMode = strings.TrimSpace(rails.AgentMode)
	rails.WorkflowKind = strings.TrimSpace(rails.WorkflowKind)
	rails.WorkflowPhase = strings.TrimSpace(rails.WorkflowPhase)
	rails.TaskFamily = strings.TrimSpace(rails.TaskFamily)
	rails.PermissionProfile = strings.TrimSpace(rails.PermissionProfile)
	rails.TargetRefs = util.UniqueNonEmpty(rails.TargetRefs)
	rails.RequiredEditPaths = util.UniqueNonEmpty(rails.RequiredEditPaths)
	rails.RequiredEvidence = util.UniqueNonEmpty(rails.RequiredEvidence)
	rails.RequiredTools = util.UniqueNonEmpty(rails.RequiredTools)
	rails.AllowedTools = util.UniqueNonEmpty(rails.AllowedTools)
	rails.ForbiddenTools = util.UniqueNonEmpty(rails.ForbiddenTools)
	rails.ReadinessGate = util.UniqueNonEmpty(rails.ReadinessGate)
	rails.SearchPolicy = util.UniqueNonEmpty(rails.SearchPolicy)
	rails.VerificationContract = strings.TrimSpace(rails.VerificationContract)
	rails.StopConditions = util.UniqueNonEmpty(rails.StopConditions)
	rails.FinalCritic = util.UniqueNonEmpty(rails.FinalCritic)
	return rails
}

func controlRailsMatches(actual ControlRailsSnapshot, expected ControlRailsExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.FollowupLane != "" && !strings.EqualFold(actual.FollowupLane, expected.FollowupLane) {
		return false
	}
	if expected.Contract != "" && !strings.EqualFold(actual.Contract, expected.Contract) {
		return false
	}
	if expected.AgentMode != "" && !strings.EqualFold(actual.AgentMode, expected.AgentMode) {
		return false
	}
	if expected.WorkflowKind != "" && !strings.EqualFold(actual.WorkflowKind, expected.WorkflowKind) {
		return false
	}
	if expected.WorkflowPhase != "" && !strings.EqualFold(actual.WorkflowPhase, expected.WorkflowPhase) {
		return false
	}
	if expected.TaskFamily != "" && !strings.EqualFold(actual.TaskFamily, expected.TaskFamily) {
		return false
	}
	if expected.PermissionProfile != "" && !strings.EqualFold(actual.PermissionProfile, expected.PermissionProfile) {
		return false
	}
	for _, required := range expected.RequireTargetRefs {
		if !containsStringFold(actual.TargetRefs, required) {
			return false
		}
	}
	for _, required := range expected.RequireEditPaths {
		if !containsStringFold(actual.RequiredEditPaths, required) {
			return false
		}
	}
	for _, required := range expected.RequireEvidence {
		if !containsStringFold(actual.RequiredEvidence, required) {
			return false
		}
	}
	for _, required := range expected.RequireTools {
		if !containsStringFold(actual.RequiredTools, required) {
			return false
		}
	}
	for _, required := range expected.RequireAllowedTools {
		if !containsStringFold(actual.AllowedTools, required) {
			return false
		}
	}
	for _, required := range expected.RequireForbiddenTools {
		if !containsStringFold(actual.ForbiddenTools, required) {
			return false
		}
	}
	for _, required := range expected.RequireReadinessGate {
		if !containsStringFold(actual.ReadinessGate, required) {
			return false
		}
	}
	for _, required := range expected.RequireSearchPolicy {
		if !containsStringFold(actual.SearchPolicy, required) {
			return false
		}
	}
	if expected.VerificationContract != "" && !strings.EqualFold(actual.VerificationContract, expected.VerificationContract) {
		return false
	}
	for _, required := range expected.RequireStopConditions {
		if !containsStringFold(actual.StopConditions, required) {
			return false
		}
	}
	for _, required := range expected.RequireFinalCritic {
		if !containsStringFold(actual.FinalCritic, required) {
			return false
		}
	}
	if expected.DynamicInstructions != nil && actual.DynamicInstructions != *expected.DynamicInstructions {
		return false
	}
	if expected.FailureToEval != nil && actual.FailureToEval != *expected.FailureToEval {
		return false
	}
	return true
}

func summaryRejectionLane(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch {
	case strings.Contains(role, "explore"):
		return "explore"
	case strings.Contains(role, "review"):
		return "review"
	case strings.Contains(role, "architecture"):
		return "architecture"
	case strings.Contains(role, "locate"):
		return "locate"
	default:
		return ""
	}
}

func parseLanguageCapabilities(content string) []LanguageCapabilitySnapshot {
	_, tail, ok := strings.Cut(content, "Language capability registry:")
	if !ok {
		return nil
	}
	caps := make([]LanguageCapabilitySnapshot, 0, 4)
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line == "" {
			continue
		}
		left := line
		signalsPart := ""
		if before, after, ok := strings.Cut(line, "|"); ok {
			left = strings.TrimSpace(before)
			signalsPart = strings.TrimSpace(after)
		}
		language := strings.TrimSpace(left)
		tools := []string(nil)
		if before, after, ok := strings.Cut(left, " tools:"); ok {
			language = strings.TrimSpace(before)
			tools = splitCommaList(after)
		}
		signals := []string(nil)
		if _, after, ok := strings.Cut(signalsPart, "signals:"); ok {
			signals = splitCommaList(after)
		}
		if language == "" {
			continue
		}
		caps = append(caps, LanguageCapabilitySnapshot{
			Language: language,
			Tools:    util.UniqueNonEmpty(tools),
			Signals:  util.UniqueNonEmpty(signals),
		})
	}
	return caps
}

func splitCommaList(text string) []string {
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func mergeLanguageCapabilities(existing []LanguageCapabilitySnapshot, incoming []LanguageCapabilitySnapshot) []LanguageCapabilitySnapshot {
	byLanguage := make(map[string]*LanguageCapabilitySnapshot)
	order := make([]string, 0, len(existing)+len(incoming))
	add := func(cap LanguageCapabilitySnapshot) {
		language := strings.TrimSpace(cap.Language)
		if language == "" {
			return
		}
		key := strings.ToLower(language)
		current, ok := byLanguage[key]
		if !ok {
			order = append(order, key)
			copy := LanguageCapabilitySnapshot{Language: language}
			byLanguage[key] = &copy
			current = &copy
		}
		current.Tools = util.UniqueNonEmpty(append(current.Tools, cap.Tools...))
		current.Signals = util.UniqueNonEmpty(append(current.Signals, cap.Signals...))
	}
	for _, cap := range existing {
		add(cap)
	}
	for _, cap := range incoming {
		add(cap)
	}
	slices.Sort(order)
	out := make([]LanguageCapabilitySnapshot, 0, len(order))
	for _, key := range order {
		cap := byLanguage[key]
		slices.Sort(cap.Tools)
		slices.Sort(cap.Signals)
		out = append(out, *cap)
	}
	return out
}

func compactLanguageCapabilityExpectations(caps []LanguageCapabilitySnapshot) []LanguageCapabilityExpectation {
	caps = mergeLanguageCapabilities(nil, caps)
	out := make([]LanguageCapabilityExpectation, 0, len(caps))
	for _, cap := range caps {
		expectation := LanguageCapabilityExpectation{Language: cap.Language}
		if len(cap.Tools) > 0 {
			expectation.Tool = cap.Tools[0]
		}
		if len(cap.Signals) > 0 {
			expectation.Signal = cap.Signals[0]
		}
		out = append(out, expectation)
	}
	return out
}

func languageCapabilityMatches(caps []LanguageCapabilitySnapshot, expected LanguageCapabilityExpectation) bool {
	language := strings.TrimSpace(expected.Language)
	tool := strings.TrimSpace(expected.Tool)
	signal := strings.TrimSpace(expected.Signal)
	for _, cap := range caps {
		if language != "" && !strings.EqualFold(cap.Language, language) {
			continue
		}
		if tool != "" && !containsStringFold(cap.Tools, tool) {
			continue
		}
		if signal != "" && !containsStringFold(cap.Signals, signal) {
			continue
		}
		return true
	}
	return false
}

func hasPhaseState(phases []PhaseSnapshot, lane string, phase string, state string) bool {
	return containsPhase(phases, PhaseExpectation{Lane: lane, Phase: phase, State: state})
}

func extractChangedFiles(text string) []string {
	lines := strings.Split(text, "\n")
	var changed []string
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inSection {
				break
			}
			continue
		}
		if strings.EqualFold(trimmed, "Changed files:") {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		changed = append(changed, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
	}
	return util.UniqueNonEmpty(changed)
}

func evidencePathsFromText(text string) []string {
	paths := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "`'\"()[]{}<>,:;!?")
		candidate = strings.TrimSuffix(candidate, ".")
		if idx := strings.LastIndex(candidate, ":"); idx > 0 {
			suffix := candidate[idx+1:]
			allDigits := suffix != ""
			for _, r := range suffix {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				candidate = candidate[:idx]
			}
		}
		if !looksLikeEvidencePath(candidate) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		paths = append(paths, candidate)
	}
	return paths
}

func looksLikeEvidencePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "://") {
		return false
	}
	if strings.HasSuffix(path, "/") {
		return false
	}
	if strings.Contains(path, "/") {
		return true
	}
	return strings.Contains(path, ".")
}

func compactPhaseExpectations(phases []PhaseSnapshot) []PhaseExpectation {
	seen := make(map[string]struct{})
	out := make([]PhaseExpectation, 0, len(phases))
	for _, phase := range phases {
		key := strings.Join([]string{phase.Lane, phase.Phase, phase.State}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, PhaseExpectation{Lane: phase.Lane, Phase: phase.Phase, State: phase.State})
	}
	return out
}

func sortedToolKeys(tools map[string]int) []string {
	keys := make([]string, 0, len(tools))
	for tool := range tools {
		keys = append(keys, tool)
	}
	slices.Sort(keys)
	return keys
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
