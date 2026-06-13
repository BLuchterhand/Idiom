package autonomy

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

const EventSchemaVersion = "idiom.autonomy.event.v1"

type EventType string

const (
	EventContractCompiled       EventType = "contract_compiled"
	EventContractPresented      EventType = "contract_presented"
	EventContractRecompiled     EventType = "contract_recompiled"
	EventContractStopped        EventType = "contract_stopped"
	EventToolIntentClassified   EventType = "tool_intent_classified"
	EventPathRolesResolved      EventType = "path_roles_resolved"
	EventToolPolicyDecision     EventType = "tool_policy_decision"
	EventToolPolicyResult       EventType = "tool_policy_result"
	EventObligationOpened       EventType = "obligation_opened"
	EventObligationResolved     EventType = "obligation_resolved"
	EventObligationWaived       EventType = "obligation_waived"
	EventReadinessStateUpdated  EventType = "readiness_state_updated"
	EventCompletionGateDecision EventType = "completion_gate_decision"
	EventGateDecision           EventType = "gate_decision"
	EventGuardFriction          EventType = "guard_friction"
	EventModelExchange          EventType = "model_exchange"
)

type EventEffect string

const EventEffectObserveOnly EventEffect = "observe_only"

const GuardFrictionSchemaVersion = "idiom.guard_friction.v1"

type GuardFrictionSubject string

const (
	GuardFrictionSubjectToolCall          GuardFrictionSubject = "tool_call"
	GuardFrictionSubjectFinalAnswer       GuardFrictionSubject = "final_answer"
	GuardFrictionSubjectMalformedToolCall GuardFrictionSubject = "malformed_tool_call"
	GuardFrictionSubjectRouteTransition   GuardFrictionSubject = "route_transition"
)

type GuardFrictionDecision string

const (
	GuardFrictionDecisionNudge   GuardFrictionDecision = "nudge"
	GuardFrictionDecisionReject  GuardFrictionDecision = "reject"
	GuardFrictionDecisionStop    GuardFrictionDecision = "stop"
	GuardFrictionDecisionRecover GuardFrictionDecision = "recover"
	GuardFrictionDecisionBlock   GuardFrictionDecision = "block"
	GuardFrictionDecisionReopen  GuardFrictionDecision = "reopen"
)

type GuardFrictionSurface string

const (
	GuardFrictionSurfaceCompletionGate GuardFrictionSurface = "completion_gate"
	GuardFrictionSurfaceToolPolicy     GuardFrictionSurface = "tool_policy"
	GuardFrictionSurfaceParser         GuardFrictionSurface = "parser"
	GuardFrictionSurfaceRecovery       GuardFrictionSurface = "recovery"
	GuardFrictionSurfaceRunnerLoop     GuardFrictionSurface = "runner_loop"
	GuardFrictionSurfaceWriteGate      GuardFrictionSurface = "write_gate"
	GuardFrictionSurfaceVerification   GuardFrictionSurface = "verification_gate"
	GuardFrictionSurfaceBudget         GuardFrictionSurface = "budget_gate"
	GuardFrictionSurfaceReadiness      GuardFrictionSurface = "readiness_gate"
)

type GuardFrictionRetryOutcome string

const (
	GuardFrictionRetryNotApplicable    GuardFrictionRetryOutcome = "not_applicable"
	GuardFrictionRetrySuccess          GuardFrictionRetryOutcome = "success"
	GuardFrictionRetrySameFailure      GuardFrictionRetryOutcome = "same_failure"
	GuardFrictionRetryDifferentFailure GuardFrictionRetryOutcome = "different_failure"
)

type GuardFrictionAvoidability string

const (
	GuardFrictionAvoidabilityUnknown                     GuardFrictionAvoidability = "unknown"
	GuardFrictionAvoidabilityRealInvariant               GuardFrictionAvoidability = "real_invariant"
	GuardFrictionAvoidabilityContractNotVisible          GuardFrictionAvoidability = "contract_not_visible"
	GuardFrictionAvoidabilityContradictoryContract       GuardFrictionAvoidability = "contradictory_contract"
	GuardFrictionAvoidabilityToolSurfaceGap              GuardFrictionAvoidability = "tool_surface_gap"
	GuardFrictionAvoidabilityParserSurfaceGap            GuardFrictionAvoidability = "parser_surface_gap"
	GuardFrictionAvoidabilityRuntimeStateGap             GuardFrictionAvoidability = "runtime_state_gap"
	GuardFrictionAvoidabilityEvidenceTimingGap           GuardFrictionAvoidability = "evidence_timing_gap"
	GuardFrictionAvoidabilityModelIgnoredVisibleContract GuardFrictionAvoidability = "model_ignored_visible_contract"
)

type Event struct {
	SchemaVersion string                      `json:"schema_version"`
	EventID       string                      `json:"event_id"`
	Type          EventType                   `json:"type"`
	Effect        EventEffect                 `json:"effect"`
	OccurredAt    time.Time                   `json:"occurred_at"`
	Sequence      int64                       `json:"sequence"`
	ContractID    string                      `json:"contract_id"`
	ParentID      string                      `json:"parent_id,omitempty"`
	Correlation   EventCorrelation            `json:"correlation,omitempty"`
	Lifecycle     *ContractLifecycleEvent     `json:"lifecycle,omitempty"`
	Policy        *ContractPolicyEvent        `json:"policy,omitempty"`
	Obligation    *ContractObligationEvent    `json:"obligation,omitempty"`
	Readiness     *ContractReadinessEvent     `json:"readiness,omitempty"`
	Completion    *ContractCompletionEvent    `json:"completion,omitempty"`
	Gate          *ContractGateEvent          `json:"gate,omitempty"`
	GuardFriction *ContractGuardFrictionEvent `json:"guard_friction,omitempty"`
	ModelExchange *ContractModelExchangeEvent `json:"model_exchange,omitempty"`
	EvidenceRefs  []string                    `json:"evidence_refs,omitempty"`
	Metadata      ContractMetadata            `json:"metadata,omitempty"`
}

type EventInput struct {
	EventID       string
	Type          EventType
	Effect        EventEffect
	OccurredAt    time.Time
	Sequence      int64
	ContractID    string
	ParentID      string
	Correlation   EventCorrelation
	Lifecycle     *ContractLifecycleEvent
	Policy        *ContractPolicyEvent
	Obligation    *ContractObligationEvent
	Readiness     *ContractReadinessEvent
	Completion    *ContractCompletionEvent
	Gate          *ContractGateEvent
	GuardFriction *ContractGuardFrictionEvent
	ModelExchange *ContractModelExchangeEvent
	EvidenceRefs  []string
	Metadata      ContractMetadata
}

type EventCorrelation struct {
	Lane              string `json:"lane,omitempty"`
	LaneRunID         string `json:"lane_run_id,omitempty"`
	Phase             string `json:"phase,omitempty"`
	PhaseRunID        string `json:"phase_run_id,omitempty"`
	TaskFamily        string `json:"task_family,omitempty"`
	RuntimeContract   string `json:"runtime_contract,omitempty"`
	PermissionProfile string `json:"permission_profile,omitempty"`
	SidecarCaller     string `json:"sidecar_caller,omitempty"`
	SidecarProfile    string `json:"sidecar_profile,omitempty"`
	SidecarRequestID  string `json:"sidecar_request_id,omitempty"`
	SidecarTool       string `json:"sidecar_tool,omitempty"`
	ToolCallID        string `json:"tool_call_id,omitempty"`
	ToolName          string `json:"tool_name,omitempty"`
}

type ContractLifecycleEvent struct {
	Summary              *ContractSummary               `json:"summary,omitempty"`
	OldContractID        string                         `json:"old_contract_id,omitempty"`
	NewContractID        string                         `json:"new_contract_id,omitempty"`
	Reason               string                         `json:"reason,omitempty"`
	StateDelta           []string                       `json:"state_delta,omitempty"`
	RailsHash            string                         `json:"rails_hash,omitempty"`
	ProtocolHash         string                         `json:"protocol_hash,omitempty"`
	AllowedTools         []string                       `json:"allowed_tools,omitempty"`
	HiddenTools          []string                       `json:"hidden_tools,omitempty"`
	RequiredEvidence     []string                       `json:"required_evidence,omitempty"`
	ReadinessGate        string                         `json:"readiness_gate,omitempty"`
	VerificationContract string                         `json:"verification_contract,omitempty"`
	InvariantID          string                         `json:"invariant_id,omitempty"`
	RemainingObligations []string                       `json:"remaining_obligations,omitempty"`
	TurnPhaseContract    *TurnPhaseContractEvent        `json:"turn_phase_contract,omitempty"`
	ModelProtocol        *ModelProtocolEvent            `json:"model_protocol,omitempty"`
	SurfaceSummaries     []TurnPhaseSurfaceSummaryEvent `json:"surface_summaries,omitempty"`
}

type TurnPhaseContractEvent struct {
	SchemaVersion    string   `json:"schema_version,omitempty"`
	ContractID       string   `json:"contract_id,omitempty"`
	PhaseID          string   `json:"phase_id,omitempty"`
	PhaseEpoch       int      `json:"phase_epoch,omitempty"`
	ModelCallID      string   `json:"model_call_id,omitempty"`
	ProtocolHash     string   `json:"protocol_hash,omitempty"`
	Step             int      `json:"step,omitempty"`
	Phase            string   `json:"phase,omitempty"`
	DerivationReason string   `json:"derivation_reason,omitempty"`
	InvariantIDs     []string `json:"invariant_ids,omitempty"`

	PermissionProfile string   `json:"permission_profile,omitempty"`
	AllowedTools      []string `json:"allowed_tools,omitempty"`
	HiddenTools       []string `json:"hidden_tools,omitempty"`
	RequiredNext      []string `json:"required_next,omitempty"`
	InvalidNext       []string `json:"invalid_next,omitempty"`
	StopConditions    []string `json:"stop_conditions,omitempty"`
	TerminalOutcomes  []string `json:"terminal_outcomes,omitempty"`
	Constraints       []string `json:"constraints,omitempty"`

	ActionPolicy    TurnActionClassPolicyEvent `json:"action_policy,omitempty"`
	RuntimeCall     RuntimeCallContractEvent   `json:"runtime_call,omitempty"`
	EvidenceState   PhaseEvidenceStateEvent    `json:"evidence_state,omitempty"`
	EnforcementMode string                     `json:"enforcement_mode,omitempty"`
}

type ModelProtocolEvent struct {
	SchemaVersion            string   `json:"schema_version,omitempty"`
	ContractID               string   `json:"contract_id,omitempty"`
	ContractVersion          string   `json:"contract_version,omitempty"`
	ProtocolHash             string   `json:"protocol_hash,omitempty"`
	PhaseID                  string   `json:"phase_id,omitempty"`
	ParserMode               string   `json:"parser_mode,omitempty"`
	ToolTransportMode        string   `json:"tool_transport_mode,omitempty"`
	ParserTransportMode      string   `json:"parser_transport_mode,omitempty"`
	ParserContract           string   `json:"parser_contract,omitempty"`
	NativeToolPolicy         string   `json:"native_tool_policy,omitempty"`
	SupportsNativeTools      bool     `json:"supports_native_tools,omitempty"`
	ForceNativeTools         bool     `json:"force_native_tools,omitempty"`
	ToolChoice               string   `json:"tool_choice,omitempty"`
	ResponseFormat           string   `json:"response_format,omitempty"`
	ExpectedVisibleOutput    string   `json:"expected_visible_output,omitempty"`
	VisibleTools             []string `json:"visible_tools,omitempty"`
	CallableTools            []string `json:"callable_tools,omitempty"`
	CallableActionClasses    []string `json:"callable_action_classes,omitempty"`
	InvalidActionClasses     []string `json:"invalid_action_classes,omitempty"`
	TerminalResponseShapes   []string `json:"terminal_response_shapes,omitempty"`
	SourceBodyObligations    []string `json:"source_body_obligations,omitempty"`
	CompletedGitHistoryPaths []string `json:"completed_git_history_paths,omitempty"`
	PendingGitHistoryPaths   []string `json:"pending_git_history_paths,omitempty"`
	RequiredEditCoverage     []string `json:"required_edit_coverage,omitempty"`
	PathRoleSummary          []string `json:"path_role_summary,omitempty"`
	VerificationObligations  []string `json:"verification_obligations,omitempty"`
	SafeAlternatives         []string `json:"safe_alternatives,omitempty"`
	MalformedOutputPolicy    string   `json:"malformed_output_policy,omitempty"`
	RecoveryBudget           int      `json:"recovery_budget,omitempty"`
	ActiveInvariantID        string   `json:"active_invariant_id,omitempty"`
}

type TurnPhaseSurfaceSummaryEvent struct {
	SchemaVersion           string                   `json:"schema_version,omitempty"`
	SurfaceID               string                   `json:"surface_id,omitempty"`
	ContractID              string                   `json:"contract_id,omitempty"`
	PhaseID                 string                   `json:"phase_id,omitempty"`
	ModelCallID             string                   `json:"model_call_id,omitempty"`
	ProtocolHash            string                   `json:"protocol_hash,omitempty"`
	InvariantID             string                   `json:"invariant_id,omitempty"`
	AdvertisedActionClasses []string                 `json:"advertised_action_classes,omitempty"`
	CallableTools           []string                 `json:"callable_tools,omitempty"`
	TerminalOutcomes        []string                 `json:"terminal_outcomes,omitempty"`
	TerminalResponseShapes  []string                 `json:"terminal_response_shapes,omitempty"`
	InvalidActionClasses    []string                 `json:"invalid_action_classes,omitempty"`
	ObligationRefs          []string                 `json:"obligation_refs,omitempty"`
	RuntimeCall             RuntimeCallContractEvent `json:"runtime_call,omitempty"`
}

type TurnActionClassPolicyEvent struct {
	AllowedActionClasses   []string `json:"allowed_action_classes,omitempty"`
	InvalidActionClasses   []string `json:"invalid_action_classes,omitempty"`
	TerminalResponseShapes []string `json:"terminal_response_shapes,omitempty"`
	CallableToolNames      []string `json:"callable_tool_names,omitempty"`
}

type RuntimeCallContractEvent struct {
	BackendKind              string   `json:"backend_kind,omitempty"`
	ModelAlias               string   `json:"model_alias,omitempty"`
	ToolTransportMode        string   `json:"tool_transport_mode,omitempty"`
	ParserContract           string   `json:"parser_contract,omitempty"`
	ResponseFormat           string   `json:"response_format,omitempty"`
	Temperature              float64  `json:"temperature,omitempty"`
	TopP                     float64  `json:"top_p,omitempty"`
	TopK                     int      `json:"top_k,omitempty"`
	MinP                     float64  `json:"min_p,omitempty"`
	PresencePenalty          float64  `json:"presence_penalty,omitempty"`
	RepetitionPenalty        float64  `json:"repetition_penalty,omitempty"`
	ToolChoice               string   `json:"tool_choice,omitempty"`
	ThinkingPolicy           string   `json:"thinking_policy,omitempty"`
	MaxTokens                int      `json:"max_tokens,omitempty"`
	ExpectedVisibleOutput    string   `json:"expected_visible_output,omitempty"`
	ExpressibleActionClasses []string `json:"expressible_action_classes,omitempty"`
}

type PhaseEvidenceStateEvent struct {
	AcceptanceRefs           []string `json:"acceptance_refs,omitempty"`
	TDDObligationRefs        []string `json:"tdd_obligation_refs,omitempty"`
	VerificationEvidenceRefs []string `json:"verification_evidence_refs,omitempty"`
	VerificationStatus       string   `json:"verification_status,omitempty"`
	ProductWorkflowState     string   `json:"product_workflow_state,omitempty"`
	AtomStatus               string   `json:"atom_status,omitempty"`
	RecoveryPacketRefs       []string `json:"recovery_packet_refs,omitempty"`
	ResidualBlockers         []string `json:"residual_blockers,omitempty"`
	SourceBodyObligations    []string `json:"source_body_obligations,omitempty"`
	CompletedGitHistoryPaths []string `json:"completed_git_history_paths,omitempty"`
	PendingGitHistoryPaths   []string `json:"pending_git_history_paths,omitempty"`
}

type ContractPolicyEvent struct {
	ToolCallID          string             `json:"tool_call_id,omitempty"`
	ToolName            string             `json:"tool_name,omitempty"`
	ActionClass         ActionClass        `json:"action_class,omitempty"`
	ActionKey           string             `json:"action_key,omitempty"`
	Transport           ToolTransport      `json:"transport,omitempty"`
	ParserFailure       ParserFailureClass `json:"parser_failure,omitempty"`
	RepairSource        RepairSource       `json:"repair_source,omitempty"`
	Capability          Capability         `json:"capability,omitempty"`
	SideEffect          SideEffectClass    `json:"side_effect,omitempty"`
	Confidence          string             `json:"confidence,omitempty"`
	ArgumentsSummary    string             `json:"arguments_summary,omitempty"`
	ArgumentsDigest     string             `json:"arguments_digest,omitempty"`
	PathRoles           []PathRoleMatch    `json:"path_roles,omitempty"`
	ProtectedMatches    []string           `json:"protected_matches,omitempty"`
	RequiredEditMatches []string           `json:"required_edit_matches,omitempty"`
	OutsideWorkspace    []string           `json:"outside_workspace,omitempty"`
	Decision            ToolPolicyDecision `json:"decision,omitempty"`
	InvariantID         string             `json:"invariant_id,omitempty"`
	ReasonCode          string             `json:"reason_code,omitempty"`
	ApprovalRequired    bool               `json:"approval_required,omitempty"`
	Result              string             `json:"result,omitempty"`
	ErrorClass          string             `json:"error_class,omitempty"`
	ChangedPaths        []string           `json:"changed_paths,omitempty"`
	EvidencePaths       []string           `json:"evidence_paths,omitempty"`
	ObligationsOpened   []string           `json:"obligations_opened,omitempty"`
	ObligationsResolved []string           `json:"obligations_resolved,omitempty"`
	Source              string             `json:"source,omitempty"`
}

type PathRoleMatch struct {
	Path             string       `json:"path,omitempty"`
	Role             PathRoleKind `json:"role,omitempty"`
	MatchedPattern   string       `json:"matched_pattern,omitempty"`
	PacketID         string       `json:"packet_id,omitempty"`
	Freshness        Freshness    `json:"freshness,omitempty"`
	OutsideWorkspace bool         `json:"outside_workspace,omitempty"`
}

type ContractObligationEvent struct {
	ObligationID           string           `json:"obligation_id"`
	Kind                   ObligationKind   `json:"kind"`
	Status                 ObligationStatus `json:"status"`
	Owner                  ObligationOwner  `json:"owner,omitempty"`
	SourceEvent            string           `json:"source_event,omitempty"`
	TargetRef              string           `json:"target_ref,omitempty"`
	RequiredBy             string           `json:"required_by,omitempty"`
	Resolution             string           `json:"resolution,omitempty"`
	EvidenceEventID        string           `json:"evidence_event_id,omitempty"`
	EvidencePath           string           `json:"evidence_path,omitempty"`
	Command                string           `json:"command,omitempty"`
	WaiverReason           string           `json:"waiver_reason,omitempty"`
	ApprovedBy             string           `json:"approved_by,omitempty"`
	FinalAnswerRequirement string           `json:"final_answer_requirement,omitempty"`
}

type ContractReadinessEvent struct {
	ReadyToAnswer       bool     `json:"ready_to_answer"`
	ReadyToWrite        bool     `json:"ready_to_write"`
	ReadyToVerify       bool     `json:"ready_to_verify"`
	ClosedClaims        []string `json:"closed_claims,omitempty"`
	OpenObligations     []string `json:"open_obligations,omitempty"`
	EvidenceRefs        []string `json:"evidence_refs,omitempty"`
	FurtherReadsRequire string   `json:"further_reads_require,omitempty"`
}

type ContractCompletionEvent struct {
	Decision             ToolPolicyDecision `json:"decision"`
	InvariantID          string             `json:"invariant_id,omitempty"`
	ProtocolHash         string             `json:"protocol_hash,omitempty"`
	RemainingObligations []string           `json:"remaining_obligations,omitempty"`
	VerificationStatus   string             `json:"verification_status,omitempty"`
	ChangedPaths         []string           `json:"changed_paths,omitempty"`
	RequiredPathStatus   []string           `json:"required_path_status,omitempty"`
}

type ContractGateEvent struct {
	Gate                  GateKind               `json:"gate"`
	Decision              ToolPolicyDecision     `json:"decision"`
	PackID                ContractPackID         `json:"pack_id,omitempty"`
	EnforcementMode       EnforcementMode        `json:"enforcement_mode,omitempty"`
	InvariantID           string                 `json:"invariant_id,omitempty"`
	ProtocolHash          string                 `json:"protocol_hash,omitempty"`
	ReasonCode            string                 `json:"reason_code,omitempty"`
	ToolCallID            string                 `json:"tool_call_id,omitempty"`
	ToolName              string                 `json:"tool_name,omitempty"`
	TargetRefs            []string               `json:"target_refs,omitempty"`
	ObligationRefs        []string               `json:"obligation_refs,omitempty"`
	OpenObligations       []string               `json:"open_obligations,omitempty"`
	ResolvedObligations   []string               `json:"resolved_obligations,omitempty"`
	ChangedPaths          []string               `json:"changed_paths,omitempty"`
	ObservedFacts         []ContractObservedFact `json:"observed_facts,omitempty"`
	SafeAlternatives      []string               `json:"safe_alternatives,omitempty"`
	VerificationStatus    string                 `json:"verification_status,omitempty"`
	Budget                *BudgetState           `json:"budget,omitempty"`
	LoopFingerprintDigest string                 `json:"loop_fingerprint_digest,omitempty"`
	Attempts              int                    `json:"attempts,omitempty"`
	Source                string                 `json:"source,omitempty"`
}

type ContractGuardFrictionEvent struct {
	Schema                   string                    `json:"schema"`
	SessionID                string                    `json:"session_id,omitempty"`
	Step                     int                       `json:"step,omitempty"`
	Lane                     string                    `json:"lane,omitempty"`
	Phase                    string                    `json:"phase,omitempty"`
	PhaseID                  string                    `json:"phase_id,omitempty"`
	ModelCallID              string                    `json:"model_call_id,omitempty"`
	SourceEventID            string                    `json:"source_event_id,omitempty"`
	ContractID               string                    `json:"contract_id,omitempty"`
	ContractVersion          string                    `json:"contract_version,omitempty"`
	ProtocolHash             string                    `json:"protocol_hash,omitempty"`
	InvariantID              string                    `json:"invariant_id,omitempty"`
	ObligationID             string                    `json:"obligation_id,omitempty"`
	Subject                  GuardFrictionSubject      `json:"subject"`
	Decision                 GuardFrictionDecision     `json:"decision"`
	GuardSurface             GuardFrictionSurface      `json:"guard_surface"`
	FirstInvalidAction       string                    `json:"first_invalid_action,omitempty"`
	VisibleActionClasses     []string                  `json:"visible_action_classes,omitempty"`
	VisibleTools             []string                  `json:"visible_tools,omitempty"`
	SafeAlternatives         []string                  `json:"safe_alternatives,omitempty"`
	ModelContractVisible     bool                      `json:"model_contract_visible"`
	ContradictorySurfaceSeen bool                      `json:"contradictory_surface_seen,omitempty"`
	RetryNumber              int                       `json:"retry_number,omitempty"`
	RetryOutcome             GuardFrictionRetryOutcome `json:"retry_outcome"`
	Avoidability             GuardFrictionAvoidability `json:"avoidability"`
	EvidenceRefs             []string                  `json:"evidence_refs,omitempty"`
}

type EventSink interface {
	Emit(Event) error
}

type MemoryEventSink struct {
	mu     sync.Mutex
	events []Event
}

func NewEvent(input EventInput) (Event, error) {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       strings.TrimSpace(input.EventID),
		Type:          input.Type,
		Effect:        input.Effect,
		OccurredAt:    input.OccurredAt,
		Sequence:      input.Sequence,
		ContractID:    strings.TrimSpace(input.ContractID),
		ParentID:      strings.TrimSpace(input.ParentID),
		Correlation:   input.Correlation,
		Lifecycle:     copyLifecycleEvent(input.Lifecycle),
		Policy:        copyPolicyEvent(input.Policy),
		Obligation:    copyObligationEvent(input.Obligation),
		Readiness:     copyReadinessEvent(input.Readiness),
		Completion:    copyCompletionEvent(input.Completion),
		Gate:          copyGateEvent(input.Gate),
		GuardFriction: copyGuardFrictionEvent(input.GuardFriction),
		ModelExchange: copyModelExchangeEvent(input.ModelExchange),
		EvidenceRefs:  append([]string(nil), input.EvidenceRefs...),
		Metadata:      copyMetadata(input.Metadata),
	}
	event.Normalize()

	if event.EventID == "" {
		event.EventID = event.stableID()
	}

	if err := event.Validate(); err != nil {
		return Event{}, err
	}

	return event, nil
}

func NewContractCompiledEvent(contract AutonomyContract, occurredAt time.Time, sequence int64) (Event, error) {
	summary := contract.Summary()

	return NewEvent(EventInput{
		Type:        EventContractCompiled,
		OccurredAt:  occurredAt,
		Sequence:    sequence,
		ContractID:  contract.ContractID,
		ParentID:    contract.ParentID,
		Correlation: CorrelationFromContract(contract),
		Lifecycle:   &ContractLifecycleEvent{Summary: &summary},
		Metadata:    contract.Metadata,
	})
}

func NewContractPresentedEvent(contract AutonomyContract, lifecycle ContractLifecycleEvent, occurredAt time.Time, sequence int64) (Event, error) {
	return NewEvent(EventInput{
		Type:        EventContractPresented,
		OccurredAt:  occurredAt,
		Sequence:    sequence,
		ContractID:  contract.ContractID,
		ParentID:    contract.ParentID,
		Correlation: CorrelationFromContract(contract),
		Lifecycle:   &lifecycle,
		Metadata:    contract.Metadata,
	})
}

func NewToolPolicyDecisionEvent(contract AutonomyContract, policy ContractPolicyEvent, occurredAt time.Time, sequence int64) (Event, error) {
	return NewPolicyEvent(EventToolPolicyDecision, contract, policy, occurredAt, sequence)
}

func NewPolicyEvent(eventType EventType, contract AutonomyContract, policy ContractPolicyEvent, occurredAt time.Time, sequence int64) (Event, error) {
	correlation := CorrelationFromContract(contract)
	if policy.ToolCallID == "" {
		policy.ToolCallID = correlation.ToolCallID
	}

	if policy.ToolName == "" {
		policy.ToolName = correlation.ToolName
	}

	return NewEvent(EventInput{
		Type:        eventType,
		OccurredAt:  occurredAt,
		Sequence:    sequence,
		ContractID:  contract.ContractID,
		ParentID:    contract.ParentID,
		Correlation: correlation,
		Policy:      &policy,
		Metadata:    contract.Metadata,
	})
}

func NewObligationStateEvent(eventType EventType, contract AutonomyContract, obligation Obligation, occurredAt time.Time, sequence int64) (Event, error) {
	payload := ObligationEventFromObligation(obligation)

	return NewEvent(EventInput{
		Type:        eventType,
		OccurredAt:  occurredAt,
		Sequence:    sequence,
		ContractID:  contract.ContractID,
		ParentID:    contract.ParentID,
		Correlation: CorrelationFromContract(contract),
		Obligation:  &payload,
		Metadata:    contract.Metadata,
	})
}

func NewReadinessStateEvent(contract AutonomyContract, readiness ContractReadinessEvent, occurredAt time.Time, sequence int64) (Event, error) {
	return NewEvent(EventInput{
		Type:        EventReadinessStateUpdated,
		OccurredAt:  occurredAt,
		Sequence:    sequence,
		ContractID:  contract.ContractID,
		ParentID:    contract.ParentID,
		Correlation: CorrelationFromContract(contract),
		Readiness:   &readiness,
		Metadata:    contract.Metadata,
	})
}

func NewCompletionGateDecisionEvent(contract AutonomyContract, completion ContractCompletionEvent, occurredAt time.Time, sequence int64) (Event, error) {
	return NewEvent(EventInput{
		Type:        EventCompletionGateDecision,
		OccurredAt:  occurredAt,
		Sequence:    sequence,
		ContractID:  contract.ContractID,
		ParentID:    contract.ParentID,
		Correlation: CorrelationFromContract(contract),
		Completion:  &completion,
		Metadata:    contract.Metadata,
	})
}

func NewGateDecisionEvent(contract AutonomyContract, gate ContractGateEvent, occurredAt time.Time, sequence int64) (Event, error) {
	return NewEvent(EventInput{
		Type:        EventGateDecision,
		OccurredAt:  occurredAt,
		Sequence:    sequence,
		ContractID:  contract.ContractID,
		ParentID:    contract.ParentID,
		Correlation: CorrelationFromContract(contract),
		Gate:        &gate,
		Metadata:    contract.Metadata,
	})
}

func NewGuardFrictionEvent(contract AutonomyContract, friction ContractGuardFrictionEvent, occurredAt time.Time, sequence int64) (Event, error) {
	friction.ContractID = firstNonEmptyEventValue(friction.ContractID, contract.ContractID)
	friction.Lane = firstNonEmptyEventValue(friction.Lane, contract.Scope.Lane)
	friction.Phase = firstNonEmptyEventValue(friction.Phase, contract.Scope.Phase)

	if len(friction.EvidenceRefs) == 0 {
		friction.EvidenceRefs = nil
	}

	return NewEvent(EventInput{
		Type:          EventGuardFriction,
		OccurredAt:    occurredAt,
		Sequence:      sequence,
		ContractID:    contract.ContractID,
		ParentID:      contract.ParentID,
		Correlation:   CorrelationFromContract(contract),
		GuardFriction: &friction,
		EvidenceRefs:  friction.EvidenceRefs,
		Metadata:      contract.Metadata,
	})
}

func NewModelExchangeEvent(contract AutonomyContract, exchange ContractModelExchangeEvent, occurredAt time.Time, sequence int64) (Event, error) {
	exchange.ContractID = firstNonEmptyEventValue(exchange.ContractID, contract.ContractID)

	return NewEvent(EventInput{
		Type:          EventModelExchange,
		OccurredAt:    occurredAt,
		Sequence:      sequence,
		ContractID:    contract.ContractID,
		ParentID:      contract.ParentID,
		Correlation:   CorrelationFromContract(contract),
		ModelExchange: &exchange,
		EvidenceRefs:  modelExchangeEvidenceRefs(exchange),
		Metadata:      contract.Metadata,
	})
}

func firstNonEmptyEventValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func CorrelationFromContract(contract AutonomyContract) EventCorrelation {
	correlation := EventCorrelation{
		Lane:              contract.Scope.Lane,
		LaneRunID:         contract.Scope.LaneRunID,
		Phase:             contract.Scope.Phase,
		PhaseRunID:        contract.Scope.PhaseRunID,
		TaskFamily:        contract.Scope.TaskFamily,
		RuntimeContract:   contract.Scope.RuntimeContract,
		PermissionProfile: contract.Scope.PermissionProfile,
	}
	if contract.Scope.Sidecar != nil {
		correlation.SidecarCaller = contract.Scope.Sidecar.Caller
		correlation.SidecarProfile = contract.Scope.Sidecar.Profile
		correlation.SidecarRequestID = contract.Scope.Sidecar.RequestID
		correlation.SidecarTool = contract.Scope.Sidecar.Tool
	}

	if contract.Scope.ToolCall != nil {
		correlation.ToolCallID = contract.Scope.ToolCall.ToolCallID
		correlation.ToolName = contract.Scope.ToolCall.ToolName
	}

	correlation.normalize()

	return correlation
}

func ObligationEventFromObligation(obligation Obligation) ContractObligationEvent {
	payload := ContractObligationEvent{
		ObligationID:           obligation.ObligationID,
		Kind:                   obligation.Kind,
		Status:                 obligation.Status,
		Owner:                  obligation.Owner,
		SourceEvent:            obligation.SourceEvent,
		TargetRef:              obligation.TargetRef,
		RequiredBy:             obligation.RequiredBy,
		EvidenceEventID:        obligation.EvidenceRef,
		Command:                obligation.Command,
		WaiverReason:           obligation.WaiverReason,
		FinalAnswerRequirement: obligation.FinalAnswerRequirement,
	}
	payload.normalize()

	return payload
}

func NewMemoryEventSink() *MemoryEventSink {
	return &MemoryEventSink{}
}

func (s *MemoryEventSink) Emit(event Event) error {
	if s == nil {
		return fmt.Errorf("autonomy event sink is nil")
	}

	event.Normalize()

	if event.EventID == "" {
		event.EventID = event.stableID()
	}

	if err := event.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.events) > 0 {
		last := s.events[len(s.events)-1]
		if event.Sequence <= last.Sequence {
			return fmt.Errorf("autonomy event sequence must increase: got %d after %d", event.Sequence, last.Sequence)
		}

		if event.OccurredAt.Before(last.OccurredAt) {
			return fmt.Errorf("autonomy event timestamp moved backwards: got %s after %s", event.OccurredAt.Format(time.RFC3339Nano), last.OccurredAt.Format(time.RFC3339Nano))
		}
	}

	s.events = append(s.events, copyEvent(event))

	return nil
}

func (s *MemoryEventSink) Events() []Event {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Event, 0, len(s.events))
	for _, event := range s.events {
		out = append(out, copyEvent(event))
	}

	return out
}

func (e *Event) Normalize() {
	if e.SchemaVersion == "" {
		e.SchemaVersion = EventSchemaVersion
	}

	if e.Effect == "" {
		e.Effect = EventEffectObserveOnly
	}

	e.EventID = strings.TrimSpace(e.EventID)
	e.ContractID = strings.TrimSpace(e.ContractID)
	e.ParentID = strings.TrimSpace(e.ParentID)
	e.OccurredAt = e.OccurredAt.UTC()
	e.Correlation.normalize()

	if e.Lifecycle != nil {
		e.Lifecycle.normalize()
	}

	if e.Policy != nil {
		e.Policy.normalize()
	}

	if e.Obligation != nil {
		e.Obligation.normalize()
	}

	if e.Readiness != nil {
		e.Readiness.normalize()
	}

	if e.Completion != nil {
		e.Completion.normalize()
	}

	if e.Gate != nil {
		e.Gate.normalize()
	}

	if e.GuardFriction != nil {
		e.GuardFriction.normalize()
	}

	if e.ModelExchange != nil {
		e.ModelExchange.Normalize()
	}

	e.EvidenceRefs = compactStrings(e.EvidenceRefs)
	e.Metadata.normalize()
}

func (e Event) Validate() error {
	var problems validationProblems

	if e.SchemaVersion != EventSchemaVersion {
		problems.Add("schema_version must be " + EventSchemaVersion)
	}

	if e.EventID == "" {
		problems.Add("event_id is required")
	}

	if !validEventType(e.Type) {
		problems.Add("type is required and must be a known autonomy event type")
	}

	if e.Effect != EventEffectObserveOnly {
		problems.Add("effect must be observe_only")
	}

	if e.OccurredAt.IsZero() {
		problems.Add("occurred_at is required")
	}

	if e.Sequence <= 0 {
		problems.Add("sequence must be positive")
	}

	if e.ContractID == "" {
		problems.Add("contract_id is required")
	}

	e.validatePayload(&problems)
	e.validateCorrelation(&problems)

	for i, client := range e.Metadata.ClientClassifications {
		if !validClientClassification(client) {
			problems.Addf("metadata.client_classifications[%d] is invalid", i)
		}
	}

	for i, signal := range e.Metadata.ProductSignals {
		if !validProductSignal(signal) {
			problems.Addf("metadata.product_signals[%d] is invalid", i)
		}
	}

	if problems.Any() {
		return EventValidationError{Problems: problems.Messages()}
	}

	return nil
}

type EventValidationError struct {
	Problems []string
}

func (e EventValidationError) Error() string {
	return "invalid autonomy event: " + strings.Join(e.Problems, "; ")
}

func (e Event) stableID() string {
	copy := copyEvent(e)

	copy.EventID = ""

	encoded, err := json.Marshal(copy)
	if err != nil {
		panic(err)
	}

	sum := sha256.Sum256(encoded)

	return "event_" + hex.EncodeToString(sum[:])[:16]
}

func (e Event) validatePayload(problems *validationProblems) {
	payloads := 0
	if e.Lifecycle != nil {
		payloads++
	}

	if e.Policy != nil {
		payloads++
	}

	if e.Obligation != nil {
		payloads++
	}

	if e.Readiness != nil {
		payloads++
	}

	if e.Completion != nil {
		payloads++
	}

	if e.Gate != nil {
		payloads++
	}

	if e.GuardFriction != nil {
		payloads++
	}

	if e.ModelExchange != nil {
		payloads++
	}

	if payloads != 1 {
		problems.Add("exactly one event payload is required")
	}

	switch e.Type {
	case EventContractCompiled, EventContractPresented, EventContractRecompiled, EventContractStopped:
		if e.Lifecycle == nil {
			problems.Add("lifecycle payload is required for " + string(e.Type))
			return
		}

		if e.Lifecycle.Summary != nil && e.Lifecycle.Summary.ContractID != "" && e.Lifecycle.Summary.ContractID != e.ContractID {
			problems.Add("lifecycle.summary.contract_id must match event contract_id")
		}

		if e.Type == EventContractRecompiled && (e.Lifecycle.OldContractID == "" || e.Lifecycle.NewContractID == "") {
			problems.Add("contract_recompiled requires old_contract_id and new_contract_id")
		}
	case EventToolIntentClassified, EventPathRolesResolved, EventToolPolicyDecision, EventToolPolicyResult:
		if e.Policy == nil {
			problems.Add("policy payload is required for " + string(e.Type))
			return
		}

		if e.Type == EventToolPolicyDecision && !validDecision(e.Policy.Decision) {
			problems.Add("policy.decision is required for tool_policy_decision")
		}

		if e.Policy.SideEffect != "" && !validSideEffect(e.Policy.SideEffect) {
			problems.Add("policy.side_effect is invalid")
		}

		for i, match := range e.Policy.PathRoles {
			if match.Role != "" && !validPathRole(match.Role) {
				problems.Addf("policy.path_roles[%d].role is invalid", i)
			}

			if match.Freshness != "" && !validFreshness(match.Freshness) {
				problems.Addf("policy.path_roles[%d].freshness is invalid", i)
			}
		}
	case EventObligationOpened, EventObligationResolved, EventObligationWaived:
		if e.Obligation == nil {
			problems.Add("obligation payload is required for " + string(e.Type))
			return
		}

		if e.Obligation.ObligationID == "" {
			problems.Add("obligation.obligation_id is required")
		}

		if !validObligationKind(e.Obligation.Kind) {
			problems.Add("obligation.kind is invalid")
		}

		if !validObligationStatus(e.Obligation.Status) {
			problems.Add("obligation.status is invalid")
		}

		if e.Obligation.Owner != "" && !validObligationOwner(e.Obligation.Owner) {
			problems.Add("obligation.owner is invalid")
		}

		if e.Type == EventObligationOpened && e.Obligation.Status != ObligationOpen {
			problems.Add("obligation_opened requires status open")
		}

		if e.Type == EventObligationResolved && e.Obligation.Status != ObligationResolved {
			problems.Add("obligation_resolved requires status resolved")
		}

		if e.Type == EventObligationWaived && e.Obligation.Status != ObligationWaived {
			problems.Add("obligation_waived requires status waived")
		}
	case EventReadinessStateUpdated:
		if e.Readiness == nil {
			problems.Add("readiness payload is required for readiness_state_updated")
		}
	case EventCompletionGateDecision:
		if e.Completion == nil {
			problems.Add("completion payload is required for completion_gate_decision")
			return
		}

		if !validDecision(e.Completion.Decision) {
			problems.Add("completion.decision is required")
		}
	case EventGateDecision:
		if e.Gate == nil {
			problems.Add("gate payload is required for gate_decision")
			return
		}

		if !validGateKind(e.Gate.Gate) {
			problems.Add("gate.gate is invalid")
		}

		if e.Gate.PackID != "" && !validContractPackID(e.Gate.PackID) {
			problems.Add("gate.pack_id is invalid")
		}

		if e.Gate.EnforcementMode != "" && !validEnforcementMode(e.Gate.EnforcementMode) {
			problems.Add("gate.enforcement_mode is invalid")
		}

		if !validDecision(e.Gate.Decision) {
			problems.Add("gate.decision is required")
		}

		if e.Gate.Attempts < 0 {
			problems.Add("gate.attempts cannot be negative")
		}

		if e.Gate.Budget != nil {
			validateBudget(*e.Gate.Budget, problems)
		}

		e.Gate.validateObservedFacts(problems)
		e.Gate.validateRedactedFields(problems)
	case EventGuardFriction:
		if e.GuardFriction == nil {
			problems.Add("guard_friction payload is required for guard_friction")
			return
		}

		e.GuardFriction.validate(e.ContractID, problems)
	case EventModelExchange:
		if e.ModelExchange == nil {
			problems.Add("model_exchange payload is required for model_exchange")
			return
		}

		e.ModelExchange.validate(e.ContractID, problems)
	}
}

func modelExchangeEvidenceRefs(exchange ContractModelExchangeEvent) []string {
	refs := []string{}
	if exchange.SourceEventID != "" {
		refs = append(refs, "event:"+exchange.SourceEventID)
	}

	if exchange.SourceEventType != "" {
		refs = append(refs, "event_type:"+exchange.SourceEventType)
	}

	if exchange.InvariantID != "" {
		refs = append(refs, "invariant:"+exchange.InvariantID)
	}

	return compactStrings(refs)
}

func (e Event) validateCorrelation(problems *validationProblems) {
	if e.Policy != nil {
		if e.Correlation.ToolCallID != "" && e.Policy.ToolCallID != "" && e.Correlation.ToolCallID != e.Policy.ToolCallID {
			problems.Add("correlation.tool_call_id must match policy.tool_call_id")
		}

		if e.Correlation.ToolName != "" && e.Policy.ToolName != "" && e.Correlation.ToolName != e.Policy.ToolName {
			problems.Add("correlation.tool_name must match policy.tool_name")
		}
	}
}

func (c *EventCorrelation) normalize() {
	c.Lane = strings.TrimSpace(c.Lane)
	c.LaneRunID = strings.TrimSpace(c.LaneRunID)
	c.Phase = strings.TrimSpace(c.Phase)
	c.PhaseRunID = strings.TrimSpace(c.PhaseRunID)
	c.TaskFamily = strings.TrimSpace(c.TaskFamily)
	c.RuntimeContract = strings.TrimSpace(c.RuntimeContract)
	c.PermissionProfile = strings.TrimSpace(c.PermissionProfile)
	c.SidecarCaller = strings.TrimSpace(c.SidecarCaller)
	c.SidecarProfile = strings.TrimSpace(c.SidecarProfile)
	c.SidecarRequestID = strings.TrimSpace(c.SidecarRequestID)
	c.SidecarTool = strings.TrimSpace(c.SidecarTool)
	c.ToolCallID = strings.TrimSpace(c.ToolCallID)
	c.ToolName = strings.TrimSpace(c.ToolName)
}

func (e *ContractLifecycleEvent) normalize() {
	if e.Summary != nil {
		*e.Summary = copyContractSummary(*e.Summary)
	}

	e.OldContractID = strings.TrimSpace(e.OldContractID)
	e.NewContractID = strings.TrimSpace(e.NewContractID)
	e.Reason = strings.TrimSpace(e.Reason)
	e.StateDelta = compactStrings(e.StateDelta)
	e.RailsHash = strings.TrimSpace(e.RailsHash)
	e.ProtocolHash = strings.TrimSpace(e.ProtocolHash)
	e.AllowedTools = compactStrings(e.AllowedTools)
	e.HiddenTools = compactStrings(e.HiddenTools)
	e.RequiredEvidence = compactStrings(e.RequiredEvidence)
	e.ReadinessGate = strings.TrimSpace(e.ReadinessGate)
	e.VerificationContract = strings.TrimSpace(e.VerificationContract)
	e.InvariantID = strings.TrimSpace(e.InvariantID)
	e.RemainingObligations = compactStrings(e.RemainingObligations)

	if e.TurnPhaseContract != nil {
		e.TurnPhaseContract.normalize()
	}

	if e.ModelProtocol != nil {
		e.ModelProtocol.normalize()
	}

	for i := range e.SurfaceSummaries {
		e.SurfaceSummaries[i].normalize()
	}

	slices.SortFunc(e.SurfaceSummaries, func(a, b TurnPhaseSurfaceSummaryEvent) int {
		if c := cmp.Compare(a.SurfaceID, b.SurfaceID); c != 0 {
			return c
		}

		return cmp.Compare(a.ModelCallID, b.ModelCallID)
	})
}

func (p *TurnPhaseContractEvent) normalize() {
	p.SchemaVersion = strings.TrimSpace(p.SchemaVersion)
	p.ContractID = strings.TrimSpace(p.ContractID)
	p.PhaseID = strings.TrimSpace(p.PhaseID)
	p.ModelCallID = strings.TrimSpace(p.ModelCallID)
	p.ProtocolHash = strings.TrimSpace(p.ProtocolHash)
	p.Phase = strings.TrimSpace(p.Phase)
	p.DerivationReason = strings.TrimSpace(p.DerivationReason)
	p.InvariantIDs = compactStrings(p.InvariantIDs)
	p.PermissionProfile = strings.TrimSpace(p.PermissionProfile)
	p.AllowedTools = compactStrings(p.AllowedTools)
	p.HiddenTools = compactStrings(p.HiddenTools)
	p.RequiredNext = compactStrings(p.RequiredNext)
	p.InvalidNext = compactStrings(p.InvalidNext)
	p.StopConditions = compactStrings(p.StopConditions)
	p.TerminalOutcomes = compactStrings(p.TerminalOutcomes)
	p.Constraints = compactStrings(p.Constraints)
	p.ActionPolicy.normalize()
	p.RuntimeCall.normalize()
	p.EvidenceState.normalize()

	p.EnforcementMode = strings.TrimSpace(p.EnforcementMode)
}

func (p *ModelProtocolEvent) normalize() {
	p.SchemaVersion = strings.TrimSpace(p.SchemaVersion)
	p.ContractID = strings.TrimSpace(p.ContractID)
	p.ContractVersion = strings.TrimSpace(p.ContractVersion)
	p.ProtocolHash = strings.TrimSpace(p.ProtocolHash)
	p.PhaseID = strings.TrimSpace(p.PhaseID)
	p.ParserMode = strings.TrimSpace(p.ParserMode)
	p.ToolTransportMode = strings.TrimSpace(p.ToolTransportMode)
	p.ParserTransportMode = strings.TrimSpace(p.ParserTransportMode)
	p.ParserContract = strings.TrimSpace(p.ParserContract)
	p.NativeToolPolicy = strings.TrimSpace(p.NativeToolPolicy)
	p.ToolChoice = strings.TrimSpace(p.ToolChoice)
	p.ResponseFormat = strings.TrimSpace(p.ResponseFormat)
	p.ExpectedVisibleOutput = strings.TrimSpace(p.ExpectedVisibleOutput)
	p.VisibleTools = compactStrings(p.VisibleTools)
	p.CallableTools = compactStrings(p.CallableTools)
	p.CallableActionClasses = compactStrings(p.CallableActionClasses)
	p.InvalidActionClasses = compactStrings(p.InvalidActionClasses)
	p.TerminalResponseShapes = compactStrings(p.TerminalResponseShapes)
	p.SourceBodyObligations = compactStrings(p.SourceBodyObligations)
	p.CompletedGitHistoryPaths = compactStrings(p.CompletedGitHistoryPaths)
	p.PendingGitHistoryPaths = compactStrings(p.PendingGitHistoryPaths)
	p.RequiredEditCoverage = compactStrings(p.RequiredEditCoverage)
	p.PathRoleSummary = compactStrings(p.PathRoleSummary)
	p.VerificationObligations = compactStrings(p.VerificationObligations)
	p.SafeAlternatives = compactStrings(p.SafeAlternatives)
	p.MalformedOutputPolicy = strings.TrimSpace(p.MalformedOutputPolicy)
	p.ActiveInvariantID = strings.TrimSpace(p.ActiveInvariantID)
}

func (s *TurnPhaseSurfaceSummaryEvent) normalize() {
	s.SchemaVersion = strings.TrimSpace(s.SchemaVersion)
	s.SurfaceID = strings.TrimSpace(s.SurfaceID)
	s.ContractID = strings.TrimSpace(s.ContractID)
	s.PhaseID = strings.TrimSpace(s.PhaseID)
	s.ModelCallID = strings.TrimSpace(s.ModelCallID)
	s.ProtocolHash = strings.TrimSpace(s.ProtocolHash)
	s.InvariantID = strings.TrimSpace(s.InvariantID)
	s.AdvertisedActionClasses = compactStrings(s.AdvertisedActionClasses)
	s.CallableTools = compactStrings(s.CallableTools)
	s.TerminalOutcomes = compactStrings(s.TerminalOutcomes)
	s.TerminalResponseShapes = compactStrings(s.TerminalResponseShapes)
	s.InvalidActionClasses = compactStrings(s.InvalidActionClasses)
	s.ObligationRefs = compactStrings(s.ObligationRefs)
	s.RuntimeCall.normalize()
}

func (p *TurnActionClassPolicyEvent) normalize() {
	p.AllowedActionClasses = compactStrings(p.AllowedActionClasses)
	p.InvalidActionClasses = compactStrings(p.InvalidActionClasses)
	p.TerminalResponseShapes = compactStrings(p.TerminalResponseShapes)
	p.CallableToolNames = compactStrings(p.CallableToolNames)
}

func (r *RuntimeCallContractEvent) normalize() {
	r.BackendKind = strings.TrimSpace(r.BackendKind)
	r.ModelAlias = strings.TrimSpace(r.ModelAlias)
	r.ToolTransportMode = strings.TrimSpace(r.ToolTransportMode)
	r.ParserContract = strings.TrimSpace(r.ParserContract)
	r.ResponseFormat = strings.TrimSpace(r.ResponseFormat)
	r.ToolChoice = strings.TrimSpace(r.ToolChoice)
	r.ThinkingPolicy = strings.TrimSpace(r.ThinkingPolicy)
	r.ExpectedVisibleOutput = strings.TrimSpace(r.ExpectedVisibleOutput)
	r.ExpressibleActionClasses = compactStrings(r.ExpressibleActionClasses)
}

func (s *PhaseEvidenceStateEvent) normalize() {
	s.AcceptanceRefs = compactStrings(s.AcceptanceRefs)
	s.TDDObligationRefs = compactStrings(s.TDDObligationRefs)
	s.VerificationEvidenceRefs = compactStrings(s.VerificationEvidenceRefs)
	s.VerificationStatus = strings.TrimSpace(s.VerificationStatus)
	s.ProductWorkflowState = strings.TrimSpace(s.ProductWorkflowState)
	s.AtomStatus = strings.TrimSpace(s.AtomStatus)
	s.RecoveryPacketRefs = compactStrings(s.RecoveryPacketRefs)
	s.ResidualBlockers = compactStrings(s.ResidualBlockers)
	s.SourceBodyObligations = compactStrings(s.SourceBodyObligations)
	s.CompletedGitHistoryPaths = compactStrings(s.CompletedGitHistoryPaths)
	s.PendingGitHistoryPaths = compactStrings(s.PendingGitHistoryPaths)
}

func (p *ContractPolicyEvent) normalize() {
	p.ToolCallID = strings.TrimSpace(p.ToolCallID)
	p.ToolName = strings.TrimSpace(p.ToolName)
	p.Confidence = strings.TrimSpace(p.Confidence)
	p.ArgumentsSummary = strings.TrimSpace(p.ArgumentsSummary)
	p.ArgumentsDigest = strings.TrimSpace(p.ArgumentsDigest)

	for i := range p.PathRoles {
		p.PathRoles[i].normalize()
	}

	slices.SortFunc(p.PathRoles, func(a, b PathRoleMatch) int { return cmp.Compare(a.sortKey(), b.sortKey()) })

	p.ProtectedMatches = compactStrings(p.ProtectedMatches)
	p.RequiredEditMatches = compactStrings(p.RequiredEditMatches)
	p.OutsideWorkspace = compactStrings(p.OutsideWorkspace)
	p.InvariantID = strings.TrimSpace(p.InvariantID)
	p.ReasonCode = strings.TrimSpace(p.ReasonCode)
	p.Result = strings.TrimSpace(p.Result)
	p.ErrorClass = strings.TrimSpace(p.ErrorClass)
	p.ChangedPaths = compactStrings(p.ChangedPaths)
	p.EvidencePaths = compactStrings(p.EvidencePaths)
	p.ObligationsOpened = compactStrings(p.ObligationsOpened)
	p.ObligationsResolved = compactStrings(p.ObligationsResolved)
	p.Source = strings.TrimSpace(p.Source)
}

func (m *PathRoleMatch) normalize() {
	m.Path = strings.TrimSpace(m.Path)
	m.MatchedPattern = strings.TrimSpace(m.MatchedPattern)
	m.PacketID = strings.TrimSpace(m.PacketID)
}

func (m PathRoleMatch) sortKey() string {
	return strings.Join([]string{m.Path, string(m.Role), m.MatchedPattern, m.PacketID}, "\x00")
}

func (o *ContractObligationEvent) normalize() {
	o.ObligationID = strings.TrimSpace(o.ObligationID)
	o.SourceEvent = strings.TrimSpace(o.SourceEvent)
	o.TargetRef = strings.TrimSpace(o.TargetRef)
	o.RequiredBy = strings.TrimSpace(o.RequiredBy)
	o.Resolution = strings.TrimSpace(o.Resolution)
	o.EvidenceEventID = strings.TrimSpace(o.EvidenceEventID)
	o.EvidencePath = strings.TrimSpace(o.EvidencePath)
	o.Command = strings.TrimSpace(o.Command)
	o.WaiverReason = strings.TrimSpace(o.WaiverReason)
	o.ApprovedBy = strings.TrimSpace(o.ApprovedBy)
	o.FinalAnswerRequirement = strings.TrimSpace(o.FinalAnswerRequirement)
}

func (r *ContractReadinessEvent) normalize() {
	r.ClosedClaims = compactStrings(r.ClosedClaims)
	r.OpenObligations = compactStrings(r.OpenObligations)
	r.EvidenceRefs = compactStrings(r.EvidenceRefs)
	r.FurtherReadsRequire = strings.TrimSpace(r.FurtherReadsRequire)
}

func (c *ContractCompletionEvent) normalize() {
	c.InvariantID = strings.TrimSpace(c.InvariantID)
	c.ProtocolHash = strings.TrimSpace(c.ProtocolHash)
	c.RemainingObligations = compactStrings(c.RemainingObligations)
	c.VerificationStatus = strings.TrimSpace(c.VerificationStatus)
	c.ChangedPaths = compactStrings(c.ChangedPaths)
	c.RequiredPathStatus = compactStrings(c.RequiredPathStatus)
}

func (g *ContractGateEvent) normalize() {
	g.PackID = ContractPackID(strings.TrimSpace(string(g.PackID)))
	g.EnforcementMode = EnforcementMode(strings.TrimSpace(string(g.EnforcementMode)))
	g.InvariantID = strings.TrimSpace(g.InvariantID)
	g.ProtocolHash = strings.TrimSpace(g.ProtocolHash)
	g.ReasonCode = strings.TrimSpace(g.ReasonCode)
	g.ToolCallID = strings.TrimSpace(g.ToolCallID)
	g.ToolName = strings.TrimSpace(g.ToolName)
	g.TargetRefs = compactStrings(g.TargetRefs)
	g.ObligationRefs = compactStrings(g.ObligationRefs)
	g.OpenObligations = compactStrings(g.OpenObligations)
	g.ResolvedObligations = compactStrings(g.ResolvedObligations)
	g.ChangedPaths = compactStrings(g.ChangedPaths)
	g.ObservedFacts = compactObservedFacts(g.ObservedFacts)
	g.SafeAlternatives = compactStrings(g.SafeAlternatives)
	g.VerificationStatus = strings.TrimSpace(g.VerificationStatus)

	if g.Budget != nil {
		budget := *g.Budget

		g.Budget = &budget
	}

	g.LoopFingerprintDigest = strings.TrimSpace(g.LoopFingerprintDigest)
	g.Source = strings.TrimSpace(g.Source)
}

func (g *ContractGuardFrictionEvent) normalize() {
	if g.Schema == "" {
		g.Schema = GuardFrictionSchemaVersion
	}

	g.Schema = strings.TrimSpace(g.Schema)
	g.SessionID = strings.TrimSpace(g.SessionID)
	g.Lane = strings.TrimSpace(g.Lane)
	g.Phase = strings.TrimSpace(g.Phase)
	g.PhaseID = strings.TrimSpace(g.PhaseID)
	g.ModelCallID = strings.TrimSpace(g.ModelCallID)
	g.SourceEventID = strings.TrimSpace(g.SourceEventID)
	g.ContractID = strings.TrimSpace(g.ContractID)
	g.ContractVersion = strings.TrimSpace(g.ContractVersion)
	g.ProtocolHash = strings.TrimSpace(g.ProtocolHash)
	g.InvariantID = strings.TrimSpace(g.InvariantID)
	g.ObligationID = strings.TrimSpace(g.ObligationID)
	g.FirstInvalidAction = strings.TrimSpace(g.FirstInvalidAction)
	g.VisibleActionClasses = compactStrings(g.VisibleActionClasses)
	g.VisibleTools = compactStrings(g.VisibleTools)
	g.SafeAlternatives = compactStrings(g.SafeAlternatives)

	if g.RetryOutcome == "" {
		g.RetryOutcome = GuardFrictionRetryNotApplicable
	}

	if g.Avoidability == "" {
		g.Avoidability = GuardFrictionAvoidabilityUnknown
	}

	g.EvidenceRefs = compactStrings(g.EvidenceRefs)
}

func (g ContractGuardFrictionEvent) validate(eventContractID string, problems *validationProblems) {
	if g.Schema != GuardFrictionSchemaVersion {
		problems.Add("guard_friction.schema must be " + GuardFrictionSchemaVersion)
	}

	if g.ContractID != "" && eventContractID != "" && g.ContractID != eventContractID {
		problems.Add("guard_friction.contract_id must match event contract_id")
	}

	if !validGuardFrictionSubject(g.Subject) {
		problems.Add("guard_friction.subject is invalid")
	}

	if !validGuardFrictionDecision(g.Decision) {
		problems.Add("guard_friction.decision is invalid")
	}

	if !validGuardFrictionSurface(g.GuardSurface) {
		problems.Add("guard_friction.guard_surface is invalid")
	}

	if !validGuardFrictionRetryOutcome(g.RetryOutcome) {
		problems.Add("guard_friction.retry_outcome is invalid")
	}

	if !validGuardFrictionAvoidability(g.Avoidability) {
		problems.Add("guard_friction.avoidability is invalid")
	}

	if g.RetryNumber < 0 {
		problems.Add("guard_friction.retry_number cannot be negative")
	}

	if g.InvariantID == "" {
		problems.Add("guard_friction.invariant_id is required")
	}

	if g.ModelContractVisible && g.ProtocolHash == "" {
		problems.Add("guard_friction.protocol_hash is required when model_contract_visible is true")
	}

	g.validateRedactedFields(problems)
}

func (g ContractGateEvent) validateObservedFacts(problems *validationProblems) {
	for i, fact := range g.ObservedFacts {
		if !validObservedFactLabel(string(fact)) {
			problems.Addf("gate.observed_facts[%d] is invalid", i)
		}
	}
}

func (g ContractGateEvent) validateRedactedFields(problems *validationProblems) {
	values := []string{
		string(g.PackID),
		string(g.EnforcementMode),
		g.InvariantID,
		g.ProtocolHash,
		g.ReasonCode,
		g.ToolCallID,
		g.ToolName,
		g.VerificationStatus,
		g.LoopFingerprintDigest,
		g.Source,
	}

	values = append(values, g.TargetRefs...)
	values = append(values, g.ObligationRefs...)
	values = append(values, g.OpenObligations...)
	values = append(values, g.ResolvedObligations...)
	values = append(values, g.ChangedPaths...)

	for _, value := range values {
		if looksLikeRawContractEventContent(value) {
			problems.Add("gate fields must contain redaction-safe metadata only")
			return
		}
	}

	for _, fact := range g.ObservedFacts {
		if looksLikeRawContractEventContent(string(fact)) {
			problems.Add("gate.observed_facts must contain redaction-safe metadata only")
			return
		}
	}

	for _, alternative := range g.SafeAlternatives {
		if looksLikeRawContractEventContent(alternative) {
			problems.Add("gate.safe_alternatives must contain redaction-safe recovery options only")
			return
		}
	}
}

func (g ContractGuardFrictionEvent) validateRedactedFields(problems *validationProblems) {
	values := []string{
		g.SessionID,
		g.Lane,
		g.Phase,
		g.PhaseID,
		g.ModelCallID,
		g.SourceEventID,
		g.ContractID,
		g.ContractVersion,
		g.ProtocolHash,
		g.InvariantID,
		g.ObligationID,
		g.FirstInvalidAction,
	}

	values = append(values, g.VisibleActionClasses...)
	values = append(values, g.VisibleTools...)
	values = append(values, g.SafeAlternatives...)
	values = append(values, g.EvidenceRefs...)

	for _, value := range values {
		if looksLikeRawGuardFrictionContent(value) {
			problems.Add("guard_friction fields must contain redaction-safe metadata only")
			return
		}
	}
}

func looksLikeRawContractEventContent(value string) bool {
	return looksLikeRawGuardFrictionContent(value)
}

func validObservedFactLabel(fact string) bool {
	fact = strings.TrimSpace(fact)
	if !validContractLabel(fact, 160) {
		return false
	}

	prefix, rest, hasPrefix := strings.Cut(fact, ":")
	if !hasPrefix {
		return true
	}

	if rest == "" || strings.ContainsAny(prefix, "/.-=@") {
		return true
	}

	return validObservedFactKind(ObservedFactKind(prefix))
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventContractCompiled, EventContractPresented, EventContractRecompiled, EventContractStopped,
		EventToolIntentClassified, EventPathRolesResolved, EventToolPolicyDecision, EventToolPolicyResult,
		EventObligationOpened, EventObligationResolved, EventObligationWaived, EventReadinessStateUpdated,
		EventCompletionGateDecision, EventGateDecision, EventGuardFriction, EventModelExchange:
		return true
	default:
		return false
	}
}

func validGuardFrictionSubject(subject GuardFrictionSubject) bool {
	switch subject {
	case GuardFrictionSubjectToolCall, GuardFrictionSubjectFinalAnswer, GuardFrictionSubjectMalformedToolCall, GuardFrictionSubjectRouteTransition:
		return true
	default:
		return false
	}
}

func validGuardFrictionDecision(decision GuardFrictionDecision) bool {
	switch decision {
	case GuardFrictionDecisionNudge, GuardFrictionDecisionReject, GuardFrictionDecisionStop, GuardFrictionDecisionRecover, GuardFrictionDecisionBlock, GuardFrictionDecisionReopen:
		return true
	default:
		return false
	}
}

func validGuardFrictionSurface(surface GuardFrictionSurface) bool {
	switch surface {
	case GuardFrictionSurfaceCompletionGate, GuardFrictionSurfaceToolPolicy, GuardFrictionSurfaceParser, GuardFrictionSurfaceRecovery,
		GuardFrictionSurfaceRunnerLoop, GuardFrictionSurfaceWriteGate, GuardFrictionSurfaceVerification, GuardFrictionSurfaceBudget, GuardFrictionSurfaceReadiness:
		return true
	default:
		return false
	}
}

func validGuardFrictionRetryOutcome(outcome GuardFrictionRetryOutcome) bool {
	switch outcome {
	case GuardFrictionRetryNotApplicable, GuardFrictionRetrySuccess, GuardFrictionRetrySameFailure, GuardFrictionRetryDifferentFailure:
		return true
	default:
		return false
	}
}

func validGuardFrictionAvoidability(avoidability GuardFrictionAvoidability) bool {
	switch avoidability {
	case GuardFrictionAvoidabilityUnknown, GuardFrictionAvoidabilityRealInvariant, GuardFrictionAvoidabilityContractNotVisible,
		GuardFrictionAvoidabilityContradictoryContract, GuardFrictionAvoidabilityToolSurfaceGap, GuardFrictionAvoidabilityParserSurfaceGap,
		GuardFrictionAvoidabilityRuntimeStateGap, GuardFrictionAvoidabilityEvidenceTimingGap, GuardFrictionAvoidabilityModelIgnoredVisibleContract:
		return true
	default:
		return false
	}
}

func looksLikeRawGuardFrictionContent(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}

	lowered := strings.ToLower(trimmed)
	for _, marker := range []string{
		"\n",
		"raw_prompt",
		"raw prompt",
		"raw_transcript",
		"raw transcript",
		"transcript:",
		"file_body",
		"file body",
		"old_text",
		"new_text",
		"-----begin",
		"api_key",
		"secret=",
		"password=",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}

	return false
}

func compactObservedFacts(values []ContractObservedFact) []ContractObservedFact {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]ContractObservedFact, 0, len(values))

	for _, value := range values {
		trimmed := strings.TrimSpace(string(value))
		if trimmed == "" {
			continue
		}

		if _, ok := seen[trimmed]; ok {
			continue
		}

		seen[trimmed] = struct{}{}
		out = append(out, ContractObservedFact(trimmed))
	}

	if len(out) == 0 {
		return nil
	}

	slices.SortFunc(out, func(a, b ContractObservedFact) int {
		return cmp.Compare(string(a), string(b))
	})

	return out
}

func copyEvent(in Event) Event {
	return Event{
		SchemaVersion: in.SchemaVersion,
		EventID:       in.EventID,
		Type:          in.Type,
		Effect:        in.Effect,
		OccurredAt:    in.OccurredAt,
		Sequence:      in.Sequence,
		ContractID:    in.ContractID,
		ParentID:      in.ParentID,
		Correlation:   in.Correlation,
		Lifecycle:     copyLifecycleEvent(in.Lifecycle),
		Policy:        copyPolicyEvent(in.Policy),
		Obligation:    copyObligationEvent(in.Obligation),
		Readiness:     copyReadinessEvent(in.Readiness),
		Completion:    copyCompletionEvent(in.Completion),
		Gate:          copyGateEvent(in.Gate),
		GuardFriction: copyGuardFrictionEvent(in.GuardFriction),
		ModelExchange: copyModelExchangeEvent(in.ModelExchange),
		EvidenceRefs:  append([]string(nil), in.EvidenceRefs...),
		Metadata:      copyMetadata(in.Metadata),
	}
}

func copyLifecycleEvent(in *ContractLifecycleEvent) *ContractLifecycleEvent {
	if in == nil {
		return nil
	}

	out := *in
	if in.Summary != nil {
		summary := copyContractSummary(*in.Summary)

		out.Summary = &summary
	}

	out.StateDelta = append([]string(nil), in.StateDelta...)
	out.AllowedTools = append([]string(nil), in.AllowedTools...)
	out.HiddenTools = append([]string(nil), in.HiddenTools...)
	out.RequiredEvidence = append([]string(nil), in.RequiredEvidence...)
	out.RemainingObligations = append([]string(nil), in.RemainingObligations...)
	out.TurnPhaseContract = copyTurnPhaseContractEvent(in.TurnPhaseContract)
	out.ModelProtocol = copyModelProtocolEvent(in.ModelProtocol)
	out.SurfaceSummaries = copyTurnPhaseSurfaceSummaryEvents(in.SurfaceSummaries)

	return &out
}

func copyTurnPhaseContractEvent(in *TurnPhaseContractEvent) *TurnPhaseContractEvent {
	if in == nil {
		return nil
	}

	out := *in

	out.InvariantIDs = append([]string(nil), in.InvariantIDs...)
	out.AllowedTools = append([]string(nil), in.AllowedTools...)
	out.HiddenTools = append([]string(nil), in.HiddenTools...)
	out.RequiredNext = append([]string(nil), in.RequiredNext...)
	out.InvalidNext = append([]string(nil), in.InvalidNext...)
	out.StopConditions = append([]string(nil), in.StopConditions...)
	out.TerminalOutcomes = append([]string(nil), in.TerminalOutcomes...)
	out.Constraints = append([]string(nil), in.Constraints...)
	out.ActionPolicy.AllowedActionClasses = append([]string(nil), in.ActionPolicy.AllowedActionClasses...)
	out.ActionPolicy.InvalidActionClasses = append([]string(nil), in.ActionPolicy.InvalidActionClasses...)
	out.ActionPolicy.TerminalResponseShapes = append([]string(nil), in.ActionPolicy.TerminalResponseShapes...)
	out.ActionPolicy.CallableToolNames = append([]string(nil), in.ActionPolicy.CallableToolNames...)
	out.RuntimeCall.ExpressibleActionClasses = append([]string(nil), in.RuntimeCall.ExpressibleActionClasses...)
	out.EvidenceState.AcceptanceRefs = append([]string(nil), in.EvidenceState.AcceptanceRefs...)
	out.EvidenceState.TDDObligationRefs = append([]string(nil), in.EvidenceState.TDDObligationRefs...)
	out.EvidenceState.VerificationEvidenceRefs = append([]string(nil), in.EvidenceState.VerificationEvidenceRefs...)
	out.EvidenceState.RecoveryPacketRefs = append([]string(nil), in.EvidenceState.RecoveryPacketRefs...)
	out.EvidenceState.ResidualBlockers = append([]string(nil), in.EvidenceState.ResidualBlockers...)
	out.EvidenceState.SourceBodyObligations = append([]string(nil), in.EvidenceState.SourceBodyObligations...)
	out.EvidenceState.CompletedGitHistoryPaths = append([]string(nil), in.EvidenceState.CompletedGitHistoryPaths...)
	out.EvidenceState.PendingGitHistoryPaths = append([]string(nil), in.EvidenceState.PendingGitHistoryPaths...)

	return &out
}

func copyModelProtocolEvent(in *ModelProtocolEvent) *ModelProtocolEvent {
	if in == nil {
		return nil
	}

	out := *in

	out.VisibleTools = append([]string(nil), in.VisibleTools...)
	out.CallableTools = append([]string(nil), in.CallableTools...)
	out.CallableActionClasses = append([]string(nil), in.CallableActionClasses...)
	out.InvalidActionClasses = append([]string(nil), in.InvalidActionClasses...)
	out.TerminalResponseShapes = append([]string(nil), in.TerminalResponseShapes...)
	out.SourceBodyObligations = append([]string(nil), in.SourceBodyObligations...)
	out.CompletedGitHistoryPaths = append([]string(nil), in.CompletedGitHistoryPaths...)
	out.PendingGitHistoryPaths = append([]string(nil), in.PendingGitHistoryPaths...)
	out.RequiredEditCoverage = append([]string(nil), in.RequiredEditCoverage...)
	out.PathRoleSummary = append([]string(nil), in.PathRoleSummary...)
	out.VerificationObligations = append([]string(nil), in.VerificationObligations...)
	out.SafeAlternatives = append([]string(nil), in.SafeAlternatives...)

	return &out
}

func copyTurnPhaseSurfaceSummaryEvents(in []TurnPhaseSurfaceSummaryEvent) []TurnPhaseSurfaceSummaryEvent {
	if len(in) == 0 {
		return nil
	}

	out := make([]TurnPhaseSurfaceSummaryEvent, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].AdvertisedActionClasses = append([]string(nil), in[i].AdvertisedActionClasses...)
		out[i].CallableTools = append([]string(nil), in[i].CallableTools...)
		out[i].TerminalOutcomes = append([]string(nil), in[i].TerminalOutcomes...)
		out[i].TerminalResponseShapes = append([]string(nil), in[i].TerminalResponseShapes...)
		out[i].InvalidActionClasses = append([]string(nil), in[i].InvalidActionClasses...)
		out[i].ObligationRefs = append([]string(nil), in[i].ObligationRefs...)
		out[i].RuntimeCall.ExpressibleActionClasses = append([]string(nil), in[i].RuntimeCall.ExpressibleActionClasses...)
	}

	return out
}

func copyGuardFrictionEvent(in *ContractGuardFrictionEvent) *ContractGuardFrictionEvent {
	if in == nil {
		return nil
	}

	out := *in

	out.VisibleActionClasses = append([]string(nil), in.VisibleActionClasses...)
	out.VisibleTools = append([]string(nil), in.VisibleTools...)
	out.SafeAlternatives = append([]string(nil), in.SafeAlternatives...)
	out.EvidenceRefs = append([]string(nil), in.EvidenceRefs...)

	return &out
}

func copyPolicyEvent(in *ContractPolicyEvent) *ContractPolicyEvent {
	if in == nil {
		return nil
	}

	out := *in

	out.PathRoles = append([]PathRoleMatch(nil), in.PathRoles...)
	out.ProtectedMatches = append([]string(nil), in.ProtectedMatches...)
	out.RequiredEditMatches = append([]string(nil), in.RequiredEditMatches...)
	out.OutsideWorkspace = append([]string(nil), in.OutsideWorkspace...)
	out.ChangedPaths = append([]string(nil), in.ChangedPaths...)
	out.EvidencePaths = append([]string(nil), in.EvidencePaths...)
	out.ObligationsOpened = append([]string(nil), in.ObligationsOpened...)
	out.ObligationsResolved = append([]string(nil), in.ObligationsResolved...)

	return &out
}

func copyObligationEvent(in *ContractObligationEvent) *ContractObligationEvent {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyReadinessEvent(in *ContractReadinessEvent) *ContractReadinessEvent {
	if in == nil {
		return nil
	}

	out := *in

	out.ClosedClaims = append([]string(nil), in.ClosedClaims...)
	out.OpenObligations = append([]string(nil), in.OpenObligations...)
	out.EvidenceRefs = append([]string(nil), in.EvidenceRefs...)

	return &out
}

func copyCompletionEvent(in *ContractCompletionEvent) *ContractCompletionEvent {
	if in == nil {
		return nil
	}

	out := *in

	out.RemainingObligations = append([]string(nil), in.RemainingObligations...)
	out.ChangedPaths = append([]string(nil), in.ChangedPaths...)
	out.RequiredPathStatus = append([]string(nil), in.RequiredPathStatus...)

	return &out
}

func copyGateEvent(in *ContractGateEvent) *ContractGateEvent {
	if in == nil {
		return nil
	}

	out := *in

	out.TargetRefs = append([]string(nil), in.TargetRefs...)
	out.ObligationRefs = append([]string(nil), in.ObligationRefs...)
	out.OpenObligations = append([]string(nil), in.OpenObligations...)
	out.ResolvedObligations = append([]string(nil), in.ResolvedObligations...)
	out.ChangedPaths = append([]string(nil), in.ChangedPaths...)
	out.ObservedFacts = append([]ContractObservedFact(nil), in.ObservedFacts...)
	out.SafeAlternatives = append([]string(nil), in.SafeAlternatives...)

	if in.Budget != nil {
		budget := *in.Budget

		out.Budget = &budget
	}

	return &out
}

func copyModelExchangeEvent(in *ContractModelExchangeEvent) *ContractModelExchangeEvent {
	if in == nil {
		return nil
	}

	out := *in
	if in.ContextPacket != nil {
		payload := *in.ContextPacket

		payload.SummaryRefs = append([]string(nil), in.ContextPacket.SummaryRefs...)
		payload.TargetRefs = append([]string(nil), in.ContextPacket.TargetRefs...)
		payload.PathRefs = append([]string(nil), in.ContextPacket.PathRefs...)
		payload.SymbolRefs = append([]string(nil), in.ContextPacket.SymbolRefs...)
		payload.EvidenceRefs = append([]string(nil), in.ContextPacket.EvidenceRefs...)
		payload.ObligationRefs = append([]string(nil), in.ContextPacket.ObligationRefs...)
		payload.SchemaNames = append([]string(nil), in.ContextPacket.SchemaNames...)
		payload.RedactionMarkers = append([]string(nil), in.ContextPacket.RedactionMarkers...)
		out.ContextPacket = &payload
	}

	if in.PermissionSnapshot != nil {
		payload := *in.PermissionSnapshot

		payload.AllowedTools = append([]string(nil), in.PermissionSnapshot.AllowedTools...)
		payload.HiddenTools = append([]string(nil), in.PermissionSnapshot.HiddenTools...)
		payload.CapabilityRefs = append([]string(nil), in.PermissionSnapshot.CapabilityRefs...)
		payload.PathRoleRefs = append([]string(nil), in.PermissionSnapshot.PathRoleRefs...)
		out.PermissionSnapshot = &payload
	}

	if in.FailureDelta != nil {
		payload := *in.FailureDelta

		payload.EvidenceRefs = append([]string(nil), in.FailureDelta.EvidenceRefs...)
		out.FailureDelta = &payload
	}

	if in.ModelResponse != nil {
		payload := copyModelResponse(*in.ModelResponse)

		out.ModelResponse = &payload
	}

	if in.ValidationSignal != nil {
		payload := *in.ValidationSignal

		payload.EvidenceRefs = append([]string(nil), in.ValidationSignal.EvidenceRefs...)
		payload.CommandRefs = append([]string(nil), in.ValidationSignal.CommandRefs...)
		out.ValidationSignal = &payload
	}

	if in.TraceStorePolicy != nil {
		payload := *in.TraceStorePolicy

		payload.AllowedFields = append([]string(nil), in.TraceStorePolicy.AllowedFields...)
		payload.ForbiddenFields = append([]string(nil), in.TraceStorePolicy.ForbiddenFields...)
		payload.StoreRefs = append([]string(nil), in.TraceStorePolicy.StoreRefs...)
		out.TraceStorePolicy = &payload
	}

	if in.ReplayClassification != nil {
		payload := *in.ReplayClassification

		payload.AtomRefs = append([]string(nil), in.ReplayClassification.AtomRefs...)
		payload.ViolationRefs = append([]string(nil), in.ReplayClassification.ViolationRefs...)
		out.ReplayClassification = &payload
	}

	if in.PhaseGatePolicy != nil {
		payload := *in.PhaseGatePolicy

		payload.GateRefs = append([]string(nil), in.PhaseGatePolicy.GateRefs...)
		payload.TerminalResponseKinds = append([]string(nil), in.PhaseGatePolicy.TerminalResponseKinds...)
		out.PhaseGatePolicy = &payload
	}

	if in.RollbackControl != nil {
		payload := *in.RollbackControl

		payload.GuardRefs = append([]string(nil), in.RollbackControl.GuardRefs...)
		payload.AffectedPackIDs = append([]string(nil), in.RollbackControl.AffectedPackIDs...)
		out.RollbackControl = &payload
	}

	return &out
}

func copyModelResponse(in ModelResponse) ModelResponse {
	out := in
	if in.ProposedAction != nil {
		payload := *in.ProposedAction

		payload.TargetRefs = append([]string(nil), in.ProposedAction.TargetRefs...)
		payload.EvidenceRefs = append([]string(nil), in.ProposedAction.EvidenceRefs...)
		payload.ProposalRefs = append([]string(nil), in.ProposedAction.ProposalRefs...)
		out.ProposedAction = &payload
	}

	if in.ContextRequest != nil {
		payload := *in.ContextRequest

		payload.RequestRefs = append([]string(nil), in.ContextRequest.RequestRefs...)
		payload.MissingRefs = append([]string(nil), in.ContextRequest.MissingRefs...)
		out.ContextRequest = &payload
	}

	if in.ContextChallenge != nil {
		payload := *in.ContextChallenge

		payload.ChallengeRefs = append([]string(nil), in.ContextChallenge.ChallengeRefs...)
		payload.EvidenceRefs = append([]string(nil), in.ContextChallenge.EvidenceRefs...)
		out.ContextChallenge = &payload
	}

	if in.ValidationProposal != nil {
		payload := *in.ValidationProposal

		payload.ValidationRefs = append([]string(nil), in.ValidationProposal.ValidationRefs...)
		payload.CommandRefs = append([]string(nil), in.ValidationProposal.CommandRefs...)
		payload.EvidenceRefs = append([]string(nil), in.ValidationProposal.EvidenceRefs...)
		out.ValidationProposal = &payload
	}

	if in.Escalation != nil {
		payload := *in.Escalation

		payload.EscalationRefs = append([]string(nil), in.Escalation.EscalationRefs...)
		payload.BlockerRefs = append([]string(nil), in.Escalation.BlockerRefs...)
		payload.QuestionRefs = append([]string(nil), in.Escalation.QuestionRefs...)
		out.Escalation = &payload
	}

	return out
}

func copyContractSummary(in ContractSummary) ContractSummary {
	return ContractSummary{
		ContractID:             in.ContractID,
		ParentID:               in.ParentID,
		SchemaVersion:          in.SchemaVersion,
		Lane:                   in.Lane,
		TaskFamily:             in.TaskFamily,
		Phase:                  in.Phase,
		PermissionProfile:      in.PermissionProfile,
		AuthoritySources:       append([]string(nil), in.AuthoritySources...),
		TargetRoleCounts:       copyStringIntMap(in.TargetRoleCounts),
		CapabilitiesAllowed:    append([]Capability(nil), in.CapabilitiesAllowed...),
		CapabilitiesForbidden:  append([]Capability(nil), in.CapabilitiesForbidden...),
		ObligationCounts:       copyStringIntMap(in.ObligationCounts),
		ProtectedPathCount:     in.ProtectedPathCount,
		ToolPolicyCount:        in.ToolPolicyCount,
		PolicyDecisionCount:    in.PolicyDecisionCount,
		EvidenceReferenceCount: in.EvidenceReferenceCount,
	}
}

func copyStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}

	return out
}
