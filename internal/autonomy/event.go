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
)

type EventEffect string

const EventEffectObserveOnly EventEffect = "observe_only"

type Event struct {
	SchemaVersion string                   `json:"schema_version"`
	EventID       string                   `json:"event_id"`
	Type          EventType                `json:"type"`
	Effect        EventEffect              `json:"effect"`
	OccurredAt    time.Time                `json:"occurred_at"`
	Sequence      int64                    `json:"sequence"`
	ContractID    string                   `json:"contract_id"`
	ParentID      string                   `json:"parent_id,omitempty"`
	Correlation   EventCorrelation         `json:"correlation,omitempty"`
	Lifecycle     *ContractLifecycleEvent  `json:"lifecycle,omitempty"`
	Policy        *ContractPolicyEvent     `json:"policy,omitempty"`
	Obligation    *ContractObligationEvent `json:"obligation,omitempty"`
	Readiness     *ContractReadinessEvent  `json:"readiness,omitempty"`
	Completion    *ContractCompletionEvent `json:"completion,omitempty"`
	Gate          *ContractGateEvent       `json:"gate,omitempty"`
	EvidenceRefs  []string                 `json:"evidence_refs,omitempty"`
	Metadata      ContractMetadata         `json:"metadata,omitempty"`
}

type EventInput struct {
	EventID      string
	Type         EventType
	Effect       EventEffect
	OccurredAt   time.Time
	Sequence     int64
	ContractID   string
	ParentID     string
	Correlation  EventCorrelation
	Lifecycle    *ContractLifecycleEvent
	Policy       *ContractPolicyEvent
	Obligation   *ContractObligationEvent
	Readiness    *ContractReadinessEvent
	Completion   *ContractCompletionEvent
	Gate         *ContractGateEvent
	EvidenceRefs []string
	Metadata     ContractMetadata
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
	Summary              *ContractSummary `json:"summary,omitempty"`
	OldContractID        string           `json:"old_contract_id,omitempty"`
	NewContractID        string           `json:"new_contract_id,omitempty"`
	Reason               string           `json:"reason,omitempty"`
	StateDelta           []string         `json:"state_delta,omitempty"`
	RailsHash            string           `json:"rails_hash,omitempty"`
	AllowedTools         []string         `json:"allowed_tools,omitempty"`
	HiddenTools          []string         `json:"hidden_tools,omitempty"`
	RequiredEvidence     []string         `json:"required_evidence,omitempty"`
	ReadinessGate        string           `json:"readiness_gate,omitempty"`
	VerificationContract string           `json:"verification_contract,omitempty"`
	InvariantID          string           `json:"invariant_id,omitempty"`
	RemainingObligations []string         `json:"remaining_obligations,omitempty"`
}

type ContractPolicyEvent struct {
	ToolCallID          string             `json:"tool_call_id,omitempty"`
	ToolName            string             `json:"tool_name,omitempty"`
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
	RemainingObligations []string           `json:"remaining_obligations,omitempty"`
	VerificationStatus   string             `json:"verification_status,omitempty"`
	ChangedPaths         []string           `json:"changed_paths,omitempty"`
	RequiredPathStatus   []string           `json:"required_path_status,omitempty"`
}

type ContractGateEvent struct {
	Gate                  GateKind           `json:"gate"`
	Decision              ToolPolicyDecision `json:"decision"`
	InvariantID           string             `json:"invariant_id,omitempty"`
	ReasonCode            string             `json:"reason_code,omitempty"`
	ToolCallID            string             `json:"tool_call_id,omitempty"`
	ToolName              string             `json:"tool_name,omitempty"`
	TargetRefs            []string           `json:"target_refs,omitempty"`
	ObligationRefs        []string           `json:"obligation_refs,omitempty"`
	OpenObligations       []string           `json:"open_obligations,omitempty"`
	ResolvedObligations   []string           `json:"resolved_obligations,omitempty"`
	ChangedPaths          []string           `json:"changed_paths,omitempty"`
	VerificationStatus    string             `json:"verification_status,omitempty"`
	Budget                *BudgetState       `json:"budget,omitempty"`
	LoopFingerprintDigest string             `json:"loop_fingerprint_digest,omitempty"`
	Attempts              int                `json:"attempts,omitempty"`
	Source                string             `json:"source,omitempty"`
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
	e.EvidenceRefs = compactStrings(e.EvidenceRefs)
	e.Metadata.normalize()
}

func (e Event) Validate() error {
	var problems []string
	if e.SchemaVersion != EventSchemaVersion {
		problems = append(problems, "schema_version must be "+EventSchemaVersion)
	}
	if e.EventID == "" {
		problems = append(problems, "event_id is required")
	}
	if !validEventType(e.Type) {
		problems = append(problems, "type is required and must be a known autonomy event type")
	}
	if e.Effect != EventEffectObserveOnly {
		problems = append(problems, "effect must be observe_only")
	}
	if e.OccurredAt.IsZero() {
		problems = append(problems, "occurred_at is required")
	}
	if e.Sequence <= 0 {
		problems = append(problems, "sequence must be positive")
	}
	if e.ContractID == "" {
		problems = append(problems, "contract_id is required")
	}
	e.validatePayload(&problems)
	e.validateCorrelation(&problems)
	for i, client := range e.Metadata.ClientClassifications {
		if !validClientClassification(client) {
			problems = append(problems, fmt.Sprintf("metadata.client_classifications[%d] is invalid", i))
		}
	}
	for i, signal := range e.Metadata.ProductSignals {
		if !validProductSignal(signal) {
			problems = append(problems, fmt.Sprintf("metadata.product_signals[%d] is invalid", i))
		}
	}
	if len(problems) > 0 {
		return EventValidationError{Problems: problems}
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

func (e Event) validatePayload(problems *[]string) {
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
	if payloads != 1 {
		*problems = append(*problems, "exactly one event payload is required")
	}
	switch e.Type {
	case EventContractCompiled, EventContractPresented, EventContractRecompiled, EventContractStopped:
		if e.Lifecycle == nil {
			*problems = append(*problems, "lifecycle payload is required for "+string(e.Type))
			return
		}
		if e.Lifecycle.Summary != nil && e.Lifecycle.Summary.ContractID != "" && e.Lifecycle.Summary.ContractID != e.ContractID {
			*problems = append(*problems, "lifecycle.summary.contract_id must match event contract_id")
		}
		if e.Type == EventContractRecompiled && (e.Lifecycle.OldContractID == "" || e.Lifecycle.NewContractID == "") {
			*problems = append(*problems, "contract_recompiled requires old_contract_id and new_contract_id")
		}
	case EventToolIntentClassified, EventPathRolesResolved, EventToolPolicyDecision, EventToolPolicyResult:
		if e.Policy == nil {
			*problems = append(*problems, "policy payload is required for "+string(e.Type))
			return
		}
		if e.Type == EventToolPolicyDecision && !validDecision(e.Policy.Decision) {
			*problems = append(*problems, "policy.decision is required for tool_policy_decision")
		}
		if e.Policy.SideEffect != "" && !validSideEffect(e.Policy.SideEffect) {
			*problems = append(*problems, "policy.side_effect is invalid")
		}
		for i, match := range e.Policy.PathRoles {
			if match.Role != "" && !validPathRole(match.Role) {
				*problems = append(*problems, fmt.Sprintf("policy.path_roles[%d].role is invalid", i))
			}
			if match.Freshness != "" && !validFreshness(match.Freshness) {
				*problems = append(*problems, fmt.Sprintf("policy.path_roles[%d].freshness is invalid", i))
			}
		}
	case EventObligationOpened, EventObligationResolved, EventObligationWaived:
		if e.Obligation == nil {
			*problems = append(*problems, "obligation payload is required for "+string(e.Type))
			return
		}
		if e.Obligation.ObligationID == "" {
			*problems = append(*problems, "obligation.obligation_id is required")
		}
		if !validObligationKind(e.Obligation.Kind) {
			*problems = append(*problems, "obligation.kind is invalid")
		}
		if !validObligationStatus(e.Obligation.Status) {
			*problems = append(*problems, "obligation.status is invalid")
		}
		if e.Obligation.Owner != "" && !validObligationOwner(e.Obligation.Owner) {
			*problems = append(*problems, "obligation.owner is invalid")
		}
		if e.Type == EventObligationOpened && e.Obligation.Status != ObligationOpen {
			*problems = append(*problems, "obligation_opened requires status open")
		}
		if e.Type == EventObligationResolved && e.Obligation.Status != ObligationResolved {
			*problems = append(*problems, "obligation_resolved requires status resolved")
		}
		if e.Type == EventObligationWaived && e.Obligation.Status != ObligationWaived {
			*problems = append(*problems, "obligation_waived requires status waived")
		}
	case EventReadinessStateUpdated:
		if e.Readiness == nil {
			*problems = append(*problems, "readiness payload is required for readiness_state_updated")
		}
	case EventCompletionGateDecision:
		if e.Completion == nil {
			*problems = append(*problems, "completion payload is required for completion_gate_decision")
			return
		}
		if !validDecision(e.Completion.Decision) {
			*problems = append(*problems, "completion.decision is required")
		}
	case EventGateDecision:
		if e.Gate == nil {
			*problems = append(*problems, "gate payload is required for gate_decision")
			return
		}
		if !validGateKind(e.Gate.Gate) {
			*problems = append(*problems, "gate.gate is invalid")
		}
		if !validDecision(e.Gate.Decision) {
			*problems = append(*problems, "gate.decision is required")
		}
		if e.Gate.Attempts < 0 {
			*problems = append(*problems, "gate.attempts cannot be negative")
		}
	}
}

func (e Event) validateCorrelation(problems *[]string) {
	if e.Policy != nil {
		if e.Correlation.ToolCallID != "" && e.Policy.ToolCallID != "" && e.Correlation.ToolCallID != e.Policy.ToolCallID {
			*problems = append(*problems, "correlation.tool_call_id must match policy.tool_call_id")
		}
		if e.Correlation.ToolName != "" && e.Policy.ToolName != "" && e.Correlation.ToolName != e.Policy.ToolName {
			*problems = append(*problems, "correlation.tool_name must match policy.tool_name")
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
	e.AllowedTools = compactStrings(e.AllowedTools)
	e.HiddenTools = compactStrings(e.HiddenTools)
	e.RequiredEvidence = compactStrings(e.RequiredEvidence)
	e.ReadinessGate = strings.TrimSpace(e.ReadinessGate)
	e.VerificationContract = strings.TrimSpace(e.VerificationContract)
	e.InvariantID = strings.TrimSpace(e.InvariantID)
	e.RemainingObligations = compactStrings(e.RemainingObligations)
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
	c.RemainingObligations = compactStrings(c.RemainingObligations)
	c.VerificationStatus = strings.TrimSpace(c.VerificationStatus)
	c.ChangedPaths = compactStrings(c.ChangedPaths)
	c.RequiredPathStatus = compactStrings(c.RequiredPathStatus)
}

func (g *ContractGateEvent) normalize() {
	g.InvariantID = strings.TrimSpace(g.InvariantID)
	g.ReasonCode = strings.TrimSpace(g.ReasonCode)
	g.ToolCallID = strings.TrimSpace(g.ToolCallID)
	g.ToolName = strings.TrimSpace(g.ToolName)
	g.TargetRefs = compactStrings(g.TargetRefs)
	g.ObligationRefs = compactStrings(g.ObligationRefs)
	g.OpenObligations = compactStrings(g.OpenObligations)
	g.ResolvedObligations = compactStrings(g.ResolvedObligations)
	g.ChangedPaths = compactStrings(g.ChangedPaths)
	g.VerificationStatus = strings.TrimSpace(g.VerificationStatus)
	if g.Budget != nil {
		budget := *g.Budget
		g.Budget = &budget
	}
	g.LoopFingerprintDigest = strings.TrimSpace(g.LoopFingerprintDigest)
	g.Source = strings.TrimSpace(g.Source)
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventContractCompiled, EventContractPresented, EventContractRecompiled, EventContractStopped,
		EventToolIntentClassified, EventPathRolesResolved, EventToolPolicyDecision, EventToolPolicyResult,
		EventObligationOpened, EventObligationResolved, EventObligationWaived, EventReadinessStateUpdated,
		EventCompletionGateDecision, EventGateDecision:
		return true
	default:
		return false
	}
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
	if in.Budget != nil {
		budget := *in.Budget
		out.Budget = &budget
	}
	return &out
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
