package autonomy

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryEventSink struct {
	mu     sync.Mutex
	events []Event
}

func newMemoryEventSink() *memoryEventSink {
	return &memoryEventSink{}
}

func (s *memoryEventSink) Emit(event Event) error {
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

func (s *memoryEventSink) Events() []Event {
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

func TestContractCompiledEventSerializesStableLifecyclePayload(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 9, 30, 0, 0, time.FixedZone("EDT", -4*60*60))

	event, err := NewContractCompiledEvent(contract, occurredAt, 1)
	if err != nil {
		t.Fatalf("NewContractCompiledEvent returned error: %v", err)
	}

	reconstructed, err := NewContractCompiledEvent(contract, occurredAt, 1)
	if err != nil {
		t.Fatalf("reconstructed event returned error: %v", err)
	}

	if reconstructed.EventID != event.EventID {
		t.Fatalf("event id is not stable: got %q want %q", reconstructed.EventID, event.EventID)
	}

	if event.SchemaVersion != EventSchemaVersion || event.Effect != EventEffectObserveOnly {
		t.Fatalf("event should be observe-only v1: %#v", event)
	}

	if event.Type != EventContractCompiled || event.ContractID != contract.ContractID {
		t.Fatalf("event identity mismatch: %#v", event)
	}

	if event.OccurredAt.Location() != time.UTC {
		t.Fatalf("event timestamp should normalize to UTC, got %s", event.OccurredAt.Location())
	}

	if event.Correlation.LaneRunID != "lane-123" || event.Correlation.PhaseRunID != "phase-456" {
		t.Fatalf("lane/phase correlation missing: %#v", event.Correlation)
	}

	if event.Lifecycle == nil || event.Lifecycle.Summary == nil {
		t.Fatalf("contract_compiled event should carry lifecycle summary: %#v", event.Lifecycle)
	}

	if event.Lifecycle.Summary.TargetRoleCounts[string(PathRoleRequiredEdit)] != 1 {
		t.Fatalf("summary should include path-role counts: %#v", event.Lifecycle.Summary.TargetRoleCounts)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	jsonText := string(encoded)
	for _, forbidden := range []string{"next_action", "safe_alternatives", "must_call_tool"} {
		if strings.Contains(jsonText, forbidden) {
			t.Fatalf("passive event JSON should not prescribe strategy field %q: %s", forbidden, jsonText)
		}
	}

	var roundTrip Event

	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	if err := roundTrip.Validate(); err != nil {
		t.Fatalf("round-tripped event should validate: %v", err)
	}
}

func TestMemoryEventSinkPreservesOrderingAndCopiesEvents(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 14, 0, 0, 0, time.UTC)

	first, err := NewContractCompiledEvent(contract, occurredAt, 1)
	if err != nil {
		t.Fatalf("NewContractCompiledEvent returned error: %v", err)
	}

	second, err := NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-verify",
		Kind:         ObligationVerify,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "post_write_verification",
	}, occurredAt.Add(time.Second), 2)
	if err != nil {
		t.Fatalf("NewObligationStateEvent returned error: %v", err)
	}

	sink := newMemoryEventSink()
	if err := sink.Emit(first); err != nil {
		t.Fatalf("emit first event: %v", err)
	}

	if err := sink.Emit(second); err != nil {
		t.Fatalf("emit second event: %v", err)
	}

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("sink stored %d events, want 2", len(events))
	}

	events[0].ContractID = "mutated"
	if got := sink.Events()[0].ContractID; got != contract.ContractID {
		t.Fatalf("sink returned aliased event copy, got contract_id %q", got)
	}

	duplicateSequence, err := NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-final",
		Kind:         ObligationFinalAnswer,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "completion_gate",
	}, occurredAt.Add(2*time.Second), 2)
	if err != nil {
		t.Fatalf("duplicate-sequence event should construct: %v", err)
	}

	if err := sink.Emit(duplicateSequence); err == nil || !strings.Contains(err.Error(), "sequence must increase") {
		t.Fatalf("expected sequence ordering error, got %v", err)
	}

	backwardTimestamp, err := NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-evidence",
		Kind:         ObligationEvidence,
		Status:       ObligationOpen,
		Owner:        OwnerLane,
		RequiredBy:   "runtime_brief",
	}, occurredAt.Add(-time.Second), 3)
	if err != nil {
		t.Fatalf("backward-timestamp event should construct: %v", err)
	}

	if err := sink.Emit(backwardTimestamp); err == nil || !strings.Contains(err.Error(), "timestamp moved backwards") {
		t.Fatalf("expected timestamp ordering error, got %v", err)
	}
}

func TestToolPolicyDecisionEventValidatesReadOnlySidecarCorrelation(t *testing.T) {
	contract := newEventTestReadOnlySidecarContract(t)
	occurredAt := time.Date(2026, 5, 13, 15, 0, 0, 0, time.UTC)

	event, err := NewToolPolicyDecisionEvent(contract, ContractPolicyEvent{
		ToolCallID:        "tool-001",
		ToolName:          "context_pack",
		ActionClass:       ActionClassSearch,
		ActionKey:         "search:context_pack:semantic_discovery",
		Transport:         ToolTransportSidecarCall,
		Capability:        CapabilitySemanticDiscovery,
		SideEffect:        SideEffectReadOnly,
		Confidence:        "direct",
		ArgumentsDigest:   "sha256:example",
		Decision:          PolicyObserve,
		InvariantID:       "sidecar.read_only_profile",
		ReasonCode:        "observe_only_passive_policy",
		Source:            "p1.2_event_fixture",
		EvidencePaths:     []string{".idiom/comprehension/views/9836ef49bd679be5.json"},
		ObligationsOpened: []string{"obl-evidence"},
		PathRoles: []PathRoleMatch{
			{Path: "internal/contracts/autonomy/contract.go", Role: PathRoleEvidenceReadonly, MatchedPattern: "internal/contracts/autonomy/**", Freshness: FreshnessFresh},
			{Path: ".git/config", Role: PathRoleForbidden, MatchedPattern: ".git/**", Freshness: FreshnessUnknown},
		},
	}, occurredAt, 1)
	if err != nil {
		t.Fatalf("NewToolPolicyDecisionEvent returned error: %v", err)
	}

	if event.Correlation.SidecarRequestID != "req-001" || event.Correlation.ToolCallID != "tool-001" {
		t.Fatalf("sidecar/tool correlation missing: %#v", event.Correlation)
	}

	if event.Policy == nil || event.Policy.Decision != PolicyObserve || event.Policy.SideEffect != SideEffectReadOnly {
		t.Fatalf("policy payload mismatch: %#v", event.Policy)
	}

	if event.Policy.ActionClass != ActionClassSearch || event.Policy.Transport != ToolTransportSidecarCall {
		t.Fatalf("policy payload should preserve normalized action metadata: %#v", event.Policy)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal policy event: %v", err)
	}

	jsonText := string(encoded)
	for _, want := range []string{`"type":"tool_policy_decision"`, `"effect":"observe_only"`, `"sidecar_request_id":"req-001"`, `"action_class":"search"`, `"transport":"sidecar_call"`, `"capability":"semantic_discovery"`} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("policy event JSON missing %s: %s", want, jsonText)
		}
	}

	_, err = NewToolPolicyDecisionEvent(contract, ContractPolicyEvent{
		ToolCallID: "tool-other",
		ToolName:   "context_pack",
		Decision:   PolicyObserve,
	}, occurredAt, 2)
	if err == nil || !strings.Contains(err.Error(), "correlation.tool_call_id") {
		t.Fatalf("expected correlation mismatch validation error, got %v", err)
	}
}

func TestGateDecisionEventSerializesTypedGatePayload(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 15, 30, 0, 0, time.UTC)
	observedFacts := []ContractObservedFact{
		"status:verification_status=pending",
		"count:open_obligations=1",
	}
	safeAlternatives := []string{"apply_patch within required paths", "ask the operator for expanded scope"}

	event, err := NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:               GateWrite,
		Decision:           PolicyReject,
		PackID:             ContractPackWriteRequiredPaths,
		EnforcementMode:    EnforcementModeEnforce,
		InvariantID:        "write.required_edit_paths",
		ReasonCode:         "outside_required_edit_paths",
		ToolName:           "apply_patch",
		TargetRefs:         []string{"internal/runtime/agent/runner.go"},
		ObligationRefs:     []string{"obl-write-runner"},
		OpenObligations:    []string{"obl-write-runner"},
		ChangedPaths:       []string{"internal/runtime/agent/runner.go"},
		ObservedFacts:      observedFacts,
		SafeAlternatives:   safeAlternatives,
		VerificationStatus: "pending",
		Source:             "event_test",
	}, occurredAt, 1)
	if err != nil {
		t.Fatalf("NewGateDecisionEvent returned error: %v", err)
	}

	observedFacts[0] = "mutated"
	safeAlternatives[0] = "mutated"

	if event.Type != EventGateDecision || event.Gate == nil || event.Gate.Gate != GateWrite {
		t.Fatalf("gate event payload mismatch: %#v", event)
	}

	if event.Gate.Decision != PolicyReject || event.Gate.InvariantID != "write.required_edit_paths" {
		t.Fatalf("gate decision fields mismatch: %#v", event.Gate)
	}

	if event.Gate.PackID != ContractPackWriteRequiredPaths || event.Gate.EnforcementMode != EnforcementModeEnforce {
		t.Fatalf("gate pack/mode fields mismatch: %#v", event.Gate)
	}

	if len(event.Gate.ObservedFacts) != 2 || event.Gate.ObservedFacts[1] == "mutated" {
		t.Fatalf("gate observed facts should be copied and normalized: %#v", event.Gate.ObservedFacts)
	}

	if len(event.Gate.SafeAlternatives) != 2 || event.Gate.SafeAlternatives[0] == "mutated" {
		t.Fatalf("gate safe alternatives should be copied: %#v", event.Gate.SafeAlternatives)
	}

	sink := newMemoryEventSink()
	if err := sink.Emit(event); err != nil {
		t.Fatalf("emit gate event: %v", err)
	}

	stored := sink.Events()

	stored[0].Gate.ObservedFacts[0] = "mutated"
	stored[0].Gate.SafeAlternatives[0] = "mutated"

	storedAgain := sink.Events()

	if storedAgain[0].Gate.ObservedFacts[0] == "mutated" || storedAgain[0].Gate.SafeAlternatives[0] == "mutated" {
		t.Fatalf("memory sink should return deep-copied gate payloads: %#v", storedAgain[0].Gate)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal gate event: %v", err)
	}

	jsonText := string(encoded)
	for _, want := range []string{`"type":"gate_decision"`, `"gate":"write_gate"`, `"decision":"reject"`, `"pack_id":"write/required_paths"`, `"enforcement_mode":"enforce"`, `"observed_facts":[`, `"safe_alternatives":["apply_patch within required paths","ask the operator for expanded scope"]`, `"open_obligations":["obl-write-runner"]`} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("gate event JSON missing %s: %s", want, jsonText)
		}
	}

	for _, forbidden := range []string{"old_text", "new_text", "file_body", "must_call_tool"} {
		if strings.Contains(jsonText, forbidden) {
			t.Fatalf("gate event JSON should not leak raw edit body or strategy field %q: %s", forbidden, jsonText)
		}
	}
}

func TestGateDecisionEventRejectsInvalidPackAndEnforcementMode(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 15, 35, 0, 0, time.UTC)

	_, err := NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:            GateWrite,
		Decision:        PolicyReject,
		PackID:          ContractPackID("write/unknown"),
		EnforcementMode: EnforcementModeEnforce,
	}, occurredAt, 1)
	if err == nil || !strings.Contains(err.Error(), "gate.pack_id is invalid") {
		t.Fatalf("expected invalid pack_id validation error, got %v", err)
	}

	_, err = NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:            GateWrite,
		Decision:        PolicyReject,
		PackID:          ContractPackWriteRequiredPaths,
		EnforcementMode: EnforcementMode("warn"),
	}, occurredAt, 2)
	if err == nil || !strings.Contains(err.Error(), "gate.enforcement_mode is invalid") {
		t.Fatalf("expected invalid enforcement mode validation error, got %v", err)
	}
}

func TestGateDecisionEventRejectsUnsafeFactOrAlternative(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 15, 40, 0, 0, time.UTC)

	_, err := NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:            GateWrite,
		Decision:        PolicyReject,
		PackID:          ContractPackWriteRequiredPaths,
		EnforcementMode: EnforcementModeEnforce,
		ObservedFacts: []ContractObservedFact{
			"raw_prompt:user_asked_for_source",
		},
	}, occurredAt, 1)
	if err == nil || !strings.Contains(err.Error(), "gate.observed_facts must contain redaction-safe metadata only") {
		t.Fatalf("expected unsafe observed fact validation error, got %v", err)
	}

	_, err = NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:             GateWrite,
		Decision:         PolicyReject,
		PackID:           ContractPackWriteRequiredPaths,
		EnforcementMode:  EnforcementModeEnforce,
		SafeAlternatives: []string{"paste the raw transcript:\nfull content"},
	}, occurredAt, 2)
	if err == nil || !strings.Contains(err.Error(), "gate.safe_alternatives must contain redaction-safe recovery options only") {
		t.Fatalf("expected unsafe safe alternative validation error, got %v", err)
	}
}

func TestGateDecisionEventCoversReadOnlySidecarContract(t *testing.T) {
	contract := newEventTestReadOnlySidecarContract(t)
	occurredAt := time.Date(2026, 5, 13, 15, 45, 0, 0, time.UTC)

	event, err := NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:            GateReadOnly,
		Decision:        PolicyObserve,
		InvariantID:     "sidecar.read_only_profile",
		ReasonCode:      "read_only_contract",
		ToolCallID:      "tool-001",
		ToolName:        "context_pack",
		TargetRefs:      []string{"internal/contracts/autonomy/**"},
		ObligationRefs:  []string{"obl-evidence"},
		OpenObligations: []string{"obl-evidence"},
		Source:          "event_test",
	}, occurredAt, 1)
	if err != nil {
		t.Fatalf("NewGateDecisionEvent returned error: %v", err)
	}

	if event.Correlation.SidecarRequestID != "req-001" || event.Correlation.ToolCallID != "tool-001" {
		t.Fatalf("sidecar/tool correlation missing: %#v", event.Correlation)
	}

	if event.Gate == nil || event.Gate.Gate != GateReadOnly || event.Gate.Decision != PolicyObserve {
		t.Fatalf("read-only gate payload mismatch: %#v", event.Gate)
	}

	if containsCapability(contract.Capabilities.Allowed, CapabilityApplyPatch) || !containsCapability(contract.Capabilities.Forbidden, CapabilitySourceEdit) {
		t.Fatalf("read-only sidecar capability policy should forbid writes: %#v", contract.Capabilities)
	}

	if !containsClientClassification(event.Metadata.ClientClassifications, ClientToolingVendor) ||
		!containsProductSignal(event.Metadata.ProductSignals, ProductLocalEvidenceExport) {

		t.Fatalf("read-only gate event should carry generic metadata: %#v", event.Metadata)
	}
}

func TestGuardFrictionEventSerializesPassiveRedactedPayload(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 15, 50, 0, 0, time.UTC)

	event, err := NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
		SessionID:            "session-123",
		Step:                 4,
		PhaseID:              "plan_execute.submit_ready",
		ModelCallID:          "call-1",
		SourceEventID:        "gate-event-1",
		ContractVersion:      "2",
		ProtocolHash:         "protocol-abc",
		InvariantID:          "completion.submit_plan_or_terminal",
		ObligationID:         "required_edit_paths.coverage",
		Subject:              GuardFrictionSubjectToolCall,
		Decision:             GuardFrictionDecisionReject,
		GuardSurface:         GuardFrictionSurfaceCompletionGate,
		FirstInvalidAction:   "search_content",
		VisibleActionClasses: []string{"submit_plan", "source_satisfied", "blocker"},
		VisibleTools:         []string{"submit_plan"},
		SafeAlternatives:     []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
		ModelContractVisible: true,
		RetryOutcome:         GuardFrictionRetryNotApplicable,
		Avoidability:         GuardFrictionAvoidabilityUnknown,
		EvidenceRefs:         []string{"conversation:record-id", "telemetry:event-id"},
	}, occurredAt, 1)
	if err != nil {
		t.Fatalf("NewGuardFrictionEvent returned error: %v", err)
	}

	if event.Type != EventGuardFriction || event.GuardFriction == nil {
		t.Fatalf("guard friction event payload mismatch: %#v", event)
	}

	if event.GuardFriction.Schema != GuardFrictionSchemaVersion {
		t.Fatalf("guard friction schema mismatch: %#v", event.GuardFriction)
	}

	if event.GuardFriction.ContractID != contract.ContractID || event.GuardFriction.Lane != "implement" || event.GuardFriction.Phase != "execute" {
		t.Fatalf("guard friction event should inherit contract identity: %#v", event.GuardFriction)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal guard friction event: %v", err)
	}

	jsonText := string(encoded)
	for _, want := range []string{`"type":"guard_friction"`, `"schema":"idiom.guard_friction.v1"`, `"guard_surface":"completion_gate"`, `"safe_alternatives":["\u003c\u003cQUESTION\u003e\u003e:","Decision: source-satisfied","submit_plan"]`} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("guard friction JSON missing %s: %s", want, jsonText)
		}
	}

	for _, forbidden := range []string{"raw_prompt", "raw_transcript", "file_body", "old_text", "new_text", "secret="} {
		if strings.Contains(jsonText, forbidden) {
			t.Fatalf("guard friction JSON should not leak raw content marker %q: %s", forbidden, jsonText)
		}
	}

	event.GuardFriction.SafeAlternatives[0] = "mutated"

	stored, err := NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
		ProtocolHash:         "protocol-abc",
		InvariantID:          "completion.submit_plan_or_terminal",
		Subject:              GuardFrictionSubjectToolCall,
		Decision:             GuardFrictionDecisionReject,
		GuardSurface:         GuardFrictionSurfaceCompletionGate,
		VisibleActionClasses: []string{"submit_plan"},
		SafeAlternatives:     []string{"submit_plan"},
		ModelContractVisible: true,
	}, occurredAt, 2)
	if err != nil {
		t.Fatalf("second guard friction event returned error: %v", err)
	}

	if stored.GuardFriction.RetryOutcome != GuardFrictionRetryNotApplicable || stored.GuardFriction.Avoidability != GuardFrictionAvoidabilityUnknown {
		t.Fatalf("guard friction defaults not normalized: %#v", stored.GuardFriction)
	}
}

func TestObligationEventCarriesClientClassificationAndProductMetadata(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 16, 0, 0, 0, time.UTC)

	event, err := NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-client-proof",
		Kind:         ObligationEvidence,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		TargetRef:    "classification-fixture",
		RequiredBy:   "client_classification_product_success_fixture",
		SourceEvent:  "contract_compiled",
	}, occurredAt, 1)
	if err != nil {
		t.Fatalf("NewObligationStateEvent returned error: %v", err)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal obligation event: %v", err)
	}

	var roundTrip Event

	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal obligation event: %v", err)
	}

	if err := roundTrip.Validate(); err != nil {
		t.Fatalf("round-tripped obligation event should validate: %v", err)
	}

	for _, want := range allClientClassifications() {
		if !containsClientClassification(roundTrip.Metadata.ClientClassifications, want) {
			t.Fatalf("missing client classification %q in %#v", want, roundTrip.Metadata.ClientClassifications)
		}
	}

	for _, want := range allProductSignals() {
		if !containsProductSignal(roundTrip.Metadata.ProductSignals, want) {
			t.Fatalf("missing product signal %q in %#v", want, roundTrip.Metadata.ProductSignals)
		}
	}
}

func TestEventValidationRejectsInvalidPayloadShape(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	_, err := NewEvent(EventInput{
		Type:       EventObligationResolved,
		OccurredAt: time.Date(2026, 5, 13, 17, 0, 0, 0, time.UTC),
		Sequence:   1,
		ContractID: contract.ContractID,
		Obligation: &ContractObligationEvent{
			ObligationID: "obl-verify",
			Kind:         ObligationVerify,
			Status:       ObligationOpen,
		},
	})

	if err == nil || !strings.Contains(err.Error(), "obligation_resolved requires status resolved") {
		t.Fatalf("expected obligation status validation error, got %v", err)
	}

	_, err = NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:     GateKind("unknown_gate"),
		Decision: PolicyAllow,
	}, time.Date(2026, 5, 13, 17, 30, 0, 0, time.UTC), 2)
	if err == nil || !strings.Contains(err.Error(), "gate.gate is invalid") {
		t.Fatalf("expected invalid gate validation error, got %v", err)
	}

	_, err = NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:     GateLoop,
		Decision: PolicyStop,
		Attempts: -1,
	}, time.Date(2026, 5, 13, 17, 45, 0, 0, time.UTC), 3)
	if err == nil || !strings.Contains(err.Error(), "gate.attempts cannot be negative") {
		t.Fatalf("expected negative attempts validation error, got %v", err)
	}

	_, err = NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
		InvariantID:          "completion.submit_plan_or_terminal",
		ProtocolHash:         "protocol-abc",
		Subject:              GuardFrictionSubject("raw_prompt"),
		Decision:             GuardFrictionDecisionReject,
		GuardSurface:         GuardFrictionSurfaceCompletionGate,
		ModelContractVisible: true,
	}, time.Date(2026, 5, 13, 17, 50, 0, 0, time.UTC), 4)
	if err == nil || !strings.Contains(err.Error(), "guard_friction.subject is invalid") {
		t.Fatalf("expected invalid guard friction subject error, got %v", err)
	}

	_, err = NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
		InvariantID:          "completion.submit_plan_or_terminal",
		Subject:              GuardFrictionSubjectToolCall,
		Decision:             GuardFrictionDecisionReject,
		GuardSurface:         GuardFrictionSurfaceCompletionGate,
		ModelContractVisible: true,
	}, time.Date(2026, 5, 13, 17, 55, 0, 0, time.UTC), 5)
	if err == nil || !strings.Contains(err.Error(), "protocol_hash is required") {
		t.Fatalf("expected missing protocol hash validation error, got %v", err)
	}

	_, err = NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
		InvariantID:          "completion.submit_plan_or_terminal",
		ProtocolHash:         "protocol-abc",
		Subject:              GuardFrictionSubjectToolCall,
		Decision:             GuardFrictionDecisionReject,
		GuardSurface:         GuardFrictionSurfaceCompletionGate,
		FirstInvalidAction:   "raw_prompt: user said to paste a transcript",
		ModelContractVisible: true,
	}, time.Date(2026, 5, 13, 17, 56, 0, 0, time.UTC), 6)
	if err == nil || !strings.Contains(err.Error(), "redaction-safe metadata") {
		t.Fatalf("expected raw-content validation error, got %v", err)
	}
}

func newEventTestPhaseContract(t *testing.T) AutonomyContract {
	t.Helper()

	contract, err := NewContract(ContractInput{
		Scope: ContractScope{
			InvocationKind:    InvocationPhase,
			Lane:              "implement",
			LaneRunID:         "lane-123",
			Phase:             "execute",
			PhaseRunID:        "phase-456",
			TaskFamily:        "implementation",
			RuntimeContract:   "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
		Authority: []AuthoritySource{
			{Kind: "runtime_brief", Ref: "brief://lane-123", Freshness: FreshnessFresh},
			{Kind: "context_pack", Ref: "context_pack://9836ef49bd679be5", EvidenceRef: "ev-context-pack", Freshness: FreshnessFresh},
		},
		PathRoles: []PathRole{
			{Role: PathRoleRequiredEdit, PathPattern: "internal/contracts/autonomy/event.go", Freshness: FreshnessFresh, Reason: "P1.2 event definitions"},
			{Role: PathRoleEvidenceReadonly, PathPattern: "docs/decisioning-north-star.md", Freshness: FreshnessFresh, Reason: "north-star evidence"},
			{Role: PathRoleProtected, PathPattern: ".git/**", Freshness: FreshnessUnknown, Reason: "repository metadata"},
		},
		Capabilities: CapabilityPolicy{
			Allowed:          []Capability{CapabilityBoundedRead, CapabilityApplyPatch, CapabilityShellVerification},
			Forbidden:        []Capability{CapabilityExternalWrite, CapabilityMutatingShell},
			AllowedEffects:   []SideEffectClass{SideEffectReadOnly, SideEffectAdditive},
			ForbiddenEffects: []SideEffectClass{SideEffectDestructive, SideEffectOpenWorld},
		},
		Obligations: []Obligation{
			{ObligationID: "obl-verify", Kind: ObligationVerify, Status: ObligationOpen, Owner: OwnerHost, RequiredBy: "post_write_verification"},
		},
		Metadata: ContractMetadata{
			ClientClassifications: allClientClassifications(),
			ProductSignals:        allProductSignals(),
			Tags:                  []string{"p1.2", "event-fixture"},
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}

	return contract
}

func newEventTestReadOnlySidecarContract(t *testing.T) AutonomyContract {
	t.Helper()

	contract, err := NewContract(ContractInput{
		Scope: ContractScope{
			InvocationKind:    InvocationSidecarCall,
			TaskFamily:        "sidecar_discovery",
			RuntimeContract:   "grounded_evidence_summary",
			PermissionProfile: "read_only_workspace",
			Sidecar: &SidecarContext{
				Transport: "cli",
				Caller:    "external-agent",
				Profile:   "discovery",
				RequestID: "req-001",
				Tool:      "context_pack",
			},
			ToolCall: &ToolCallContext{
				ToolCallID: "tool-001",
				ToolName:   "context_pack",
				Capability: CapabilitySemanticDiscovery,
				SideEffect: SideEffectReadOnly,
			},
		},
		PathRoles: []PathRole{
			{Role: PathRoleEvidenceReadonly, PathPattern: "internal/contracts/autonomy/**", Freshness: FreshnessFresh, Reason: "read-only sidecar source evidence"},
			{Role: PathRoleForbidden, PathPattern: ".git/**", Freshness: FreshnessUnknown, Reason: "repository metadata"},
		},
		Capabilities: CapabilityPolicy{
			Allowed:          []Capability{CapabilitySemanticDiscovery, CapabilityBoundedRead, CapabilityLiteralSearch},
			Forbidden:        []Capability{CapabilitySourceEdit, CapabilityApplyPatch, CapabilityExternalWrite, CapabilityMutatingShell},
			AllowedEffects:   []SideEffectClass{SideEffectReadOnly},
			ForbiddenEffects: []SideEffectClass{SideEffectAdditive, SideEffectDestructive, SideEffectOpenWorld},
		},
		Obligations: []Obligation{
			{ObligationID: "obl-evidence", Kind: ObligationEvidence, Status: ObligationOpen, Owner: OwnerSidecar, RequiredBy: "sidecar_request", TargetRef: "context_pack"},
		},
		Metadata: ContractMetadata{
			ClientClassifications: []ClientClassification{ClientToolingVendor, ClientLocalModel, ClientOpenSource},
			ProductSignals:        []ProductSignal{ProductIntegrationProof, ProductSecurityPrivacy, ProductLocalEvidenceExport},
			Tags:                  []string{"p1.2", "read-only-sidecar"},
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}

	return contract
}
