package autonomy

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Autonomy events", func() {
	It("serializes a stable contract-compiled lifecycle payload", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 9, 30, 0, 0, time.FixedZone("EDT", -4*60*60))

		event, err := NewContractCompiledEvent(contract, occurredAt, 1)
		Expect(err).NotTo(HaveOccurred())

		reconstructed, err := NewContractCompiledEvent(contract, occurredAt, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(reconstructed.EventID).To(Equal(event.EventID))
		Expect(event.SchemaVersion).To(Equal(EventSchemaVersion))
		Expect(event.Effect).To(Equal(EventEffectObserveOnly))
		Expect(event.Type).To(Equal(EventContractCompiled))
		Expect(event.ContractID).To(Equal(contract.ContractID))
		Expect(event.OccurredAt.Location()).To(Equal(time.UTC))
		Expect(event.Correlation.LaneRunID).To(Equal("lane-123"))
		Expect(event.Correlation.PhaseRunID).To(Equal("phase-456"))
		Expect(event.Lifecycle).NotTo(BeNil())
		Expect(event.Lifecycle.Summary).NotTo(BeNil())
		Expect(event.Lifecycle.Summary.TargetRoleCounts).To(HaveKeyWithValue(string(PathRoleRequiredEdit), 1))

		encoded, err := json.Marshal(event)
		Expect(err).NotTo(HaveOccurred())

		jsonText := string(encoded)
		for _, forbidden := range []string{"next_action", "safe_alternatives", "must_call_tool"} {
			Expect(jsonText).NotTo(ContainSubstring(forbidden))
		}

		var roundTrip Event

		Expect(json.Unmarshal(encoded, &roundTrip)).To(Succeed())
		Expect(roundTrip.Validate()).To(Succeed())
	})

	It("serializes sampled model-protocol evidence authority for audit", func() {
		contract := newEventTestPhaseContract()
		grounded := []string{"README.md", "/external/reference.md"}
		event, err := NewContractPresentedEvent(contract, ContractLifecycleEvent{
			Reason:       "turn_phase_contract",
			ProtocolHash: "sha256:protocol-evidence",
			ModelProtocol: &ModelProtocolEvent{
				SchemaVersion:                 ModelProtocolProjectionSchemaVersion,
				ContractID:                    contract.ContractID,
				ProtocolHash:                  "sha256:protocol-evidence",
				PhaseID:                       "read_only_evidence_pending",
				GroundedSourceBodyPaths:       grounded,
				GroundedSourceBodyPathTotal:   3,
				GroundedSourceBodyPathOmitted: 1,
				CandidateOnlyPaths:            []string{"internal/candidate.go"},
				CandidateOnlyPathTotal:        1,
				SourceBodyObligations:         []string{"internal/pending.go"},
				SourceBodyObligationTotal:     2,
				SourceBodyObligationOmitted:   1,
			},
		}, time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC), 1)
		Expect(err).NotTo(HaveOccurred())

		grounded[0] = "mutated.md"
		protocol := event.Lifecycle.ModelProtocol
		Expect(protocol.SchemaVersion).To(Equal(ModelProtocolProjectionSchemaVersion))
		Expect(protocol.GroundedSourceBodyPaths).To(ConsistOf("README.md", "/external/reference.md"))
		Expect(protocol.GroundedSourceBodyPathTotal).To(Equal(3))
		Expect(protocol.GroundedSourceBodyPathOmitted).To(Equal(1))

		encoded, err := json.Marshal(event)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`"schema_version":"idiom.model_protocol_projection.v2"`))
		Expect(string(encoded)).To(ContainSubstring(`"grounded_source_body_paths"`))
		Expect(string(encoded)).To(ContainSubstring(`"candidate_only_paths"`))
		Expect(string(encoded)).To(ContainSubstring(`"grounded_source_body_path_omitted":1`))

		var roundTrip Event
		Expect(json.Unmarshal(encoded, &roundTrip)).To(Succeed())
		roundTrip.Normalize()
		Expect(roundTrip.Validate()).To(Succeed())
		Expect(roundTrip.Lifecycle.ModelProtocol.GroundedSourceBodyPaths).To(ConsistOf("README.md", "/external/reference.md"))
		Expect(roundTrip.Lifecycle.ModelProtocol.CandidateOnlyPaths).To(Equal([]string{"internal/candidate.go"}))
		Expect(roundTrip.Lifecycle.ModelProtocol.SourceBodyObligationTotal).To(Equal(2))
		Expect(roundTrip.Lifecycle.ModelProtocol.SourceBodyObligationOmitted).To(Equal(1))
	})

	It("preserves event ordering and returns deep copies from the memory sink", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 14, 0, 0, 0, time.UTC)

		first, err := NewContractCompiledEvent(contract, occurredAt, 1)
		Expect(err).NotTo(HaveOccurred())

		second, err := NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "obl-verify",
			Kind:         ObligationVerify,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "post_write_verification",
		}, occurredAt.Add(time.Second), 2)
		Expect(err).NotTo(HaveOccurred())

		sink := newMemoryEventSink()
		Expect(sink.Emit(first)).To(Succeed())
		Expect(sink.Emit(second)).To(Succeed())

		events := sink.Events()
		Expect(events).To(HaveLen(2))

		events[0].ContractID = "mutated"
		Expect(sink.Events()[0].ContractID).To(Equal(contract.ContractID))

		duplicateSequence, err := NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "obl-final",
			Kind:         ObligationFinalAnswer,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "completion_gate",
		}, occurredAt.Add(2*time.Second), 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(sink.Emit(duplicateSequence)).To(MatchError(ContainSubstring("sequence must increase")))

		backwardTimestamp, err := NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "obl-evidence",
			Kind:         ObligationEvidence,
			Status:       ObligationOpen,
			Owner:        OwnerLane,
			RequiredBy:   "runtime_brief",
		}, occurredAt.Add(-time.Second), 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(sink.Emit(backwardTimestamp)).To(MatchError(ContainSubstring("timestamp moved backwards")))
	})

	It("validates policy-event correlation for a read-only sidecar", func() {
		contract := newEventTestReadOnlySidecarContract()
		occurredAt := time.Date(2026, 5, 13, 15, 0, 0, 0, time.UTC)

		event, err := NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
			ToolCallID:        "tool-001",
			ToolName:          "context_pack",
			ActionClass:       ActionClass("broad_inspection"),
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
		Expect(err).NotTo(HaveOccurred())
		Expect(event.Correlation.SidecarRequestID).To(Equal("req-001"))
		Expect(event.Correlation.ToolCallID).To(Equal("tool-001"))
		Expect(event.Policy).NotTo(BeNil())
		Expect(event.Policy.Decision).To(Equal(PolicyObserve))
		Expect(event.Policy.SideEffect).To(Equal(SideEffectReadOnly))
		Expect(event.Policy.ActionClass).To(Equal(ActionClassSearch))
		Expect(event.Policy.Transport).To(Equal(ToolTransportSidecarCall))

		encoded, err := json.Marshal(event)
		Expect(err).NotTo(HaveOccurred())

		jsonText := string(encoded)
		for _, want := range []string{`"type":"tool_policy_decision"`, `"effect":"observe_only"`, `"sidecar_request_id":"req-001"`, `"action_class":"search"`, `"transport":"sidecar_call"`, `"capability":"semantic_discovery"`} {
			Expect(jsonText).To(ContainSubstring(want))
		}

		_, err = NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
			ToolCallID: "tool-other",
			ToolName:   "context_pack",
			Decision:   PolicyObserve,
		}, occurredAt, 2)
		Expect(err).To(MatchError(ContainSubstring("correlation.tool_call_id")))

		_, err = NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
			ToolCallID:      "tool-001",
			ToolName:        "context_pack",
			ActionClass:     ActionClass("invented_action"),
			Decision:        PolicyObserve,
			Transport:       ToolTransportSidecarCall,
			Capability:      CapabilitySemanticDiscovery,
			SideEffect:      SideEffectReadOnly,
			ReasonCode:      "observe_only_passive_policy",
			ArgumentsDigest: "sha256:example",
		}, occurredAt, 3)
		Expect(err).To(MatchError(ContainSubstring("policy.action_class")))
	})

	It("serializes a typed gate-decision payload and deep-copies its slices", func() {
		contract := newEventTestPhaseContract()
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
		Expect(err).NotTo(HaveOccurred())

		observedFacts[0] = "mutated"
		safeAlternatives[0] = "mutated"

		Expect(event.Type).To(Equal(EventGateDecision))
		Expect(event.Gate).NotTo(BeNil())
		Expect(event.Gate.Gate).To(Equal(GateWrite))
		Expect(event.Gate.Decision).To(Equal(PolicyReject))
		Expect(event.Gate.InvariantID).To(Equal("write.required_edit_paths"))
		Expect(event.Gate.PackID).To(Equal(ContractPackWriteRequiredPaths))
		Expect(event.Gate.EnforcementMode).To(Equal(EnforcementModeEnforce))
		Expect(event.Gate.ObservedFacts).To(HaveLen(2))
		Expect(event.Gate.ObservedFacts).NotTo(ContainElement(ContractObservedFact("mutated")))
		Expect(event.Gate.SafeAlternatives).To(HaveLen(2))
		Expect(event.Gate.SafeAlternatives[0]).NotTo(Equal("mutated"))

		sink := newMemoryEventSink()
		Expect(sink.Emit(event)).To(Succeed())

		stored := sink.Events()

		stored[0].Gate.ObservedFacts[0] = "mutated"
		stored[0].Gate.SafeAlternatives[0] = "mutated"

		storedAgain := sink.Events()

		Expect(storedAgain[0].Gate.ObservedFacts[0]).NotTo(Equal(ContractObservedFact("mutated")))
		Expect(storedAgain[0].Gate.SafeAlternatives[0]).NotTo(Equal("mutated"))

		encoded, err := json.Marshal(event)
		Expect(err).NotTo(HaveOccurred())

		jsonText := string(encoded)
		for _, want := range []string{`"type":"gate_decision"`, `"gate":"write_gate"`, `"decision":"reject"`, `"pack_id":"write/required_paths"`, `"enforcement_mode":"enforce"`, `"observed_facts":[`, `"safe_alternatives":["apply_patch within required paths","ask the operator for expanded scope"]`, `"open_obligations":["obl-write-runner"]`} {
			Expect(jsonText).To(ContainSubstring(want))
		}

		for _, forbidden := range []string{"old_text", "new_text", "file_body", "must_call_tool"} {
			Expect(jsonText).NotTo(ContainSubstring(forbidden))
		}
	})

	It("rejects invalid gate packs and enforcement modes", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 15, 35, 0, 0, time.UTC)

		_, err := NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:            GateWrite,
			Decision:        PolicyReject,
			PackID:          ContractPackID("write/unknown"),
			EnforcementMode: EnforcementModeEnforce,
		}, occurredAt, 1)
		Expect(err).To(MatchError(ContainSubstring("gate.pack_id is invalid")))

		_, err = NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:            GateWrite,
			Decision:        PolicyReject,
			PackID:          ContractPackWriteRequiredPaths,
			EnforcementMode: EnforcementMode("warn"),
		}, occurredAt, 2)
		Expect(err).To(MatchError(ContainSubstring("gate.enforcement_mode is invalid")))
	})

	It("rejects unsafe observed facts and recovery alternatives", func() {
		contract := newEventTestPhaseContract()
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
		Expect(err).To(MatchError(ContainSubstring("gate.observed_facts must contain redaction-safe metadata only")))

		_, err = NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:             GateWrite,
			Decision:         PolicyReject,
			PackID:           ContractPackWriteRequiredPaths,
			EnforcementMode:  EnforcementModeEnforce,
			SafeAlternatives: []string{"paste the raw transcript:\nfull content"},
		}, occurredAt, 2)
		Expect(err).To(MatchError(ContainSubstring("gate.safe_alternatives must contain redaction-safe recovery options only")))
	})

	It("covers the read-only sidecar contract", func() {
		contract := newEventTestReadOnlySidecarContract()
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
		Expect(err).NotTo(HaveOccurred())
		Expect(event.Correlation.SidecarRequestID).To(Equal("req-001"))
		Expect(event.Correlation.ToolCallID).To(Equal("tool-001"))
		Expect(event.Gate).NotTo(BeNil())
		Expect(event.Gate.Gate).To(Equal(GateReadOnly))
		Expect(event.Gate.Decision).To(Equal(PolicyObserve))
		Expect(contract.Capabilities.Allowed).NotTo(ContainElement(CapabilityApplyPatch))
		Expect(contract.Capabilities.Forbidden).To(ContainElement(CapabilitySourceEdit))
		Expect(event.Metadata.ClientClassifications).To(ContainElement(ClientToolingVendor))
		Expect(event.Metadata.ProductSignals).To(ContainElement(ProductLocalEvidenceExport))
	})

	It("serializes passive redacted guard-friction payloads", func() {
		contract := newEventTestPhaseContract()
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
		Expect(err).NotTo(HaveOccurred())
		Expect(event.Type).To(Equal(EventGuardFriction))
		Expect(event.GuardFriction).NotTo(BeNil())
		Expect(event.GuardFriction.Schema).To(Equal(GuardFrictionSchemaVersion))
		Expect(event.GuardFriction.ContractID).To(Equal(contract.ContractID))
		Expect(event.GuardFriction.Lane).To(Equal("implement"))
		Expect(event.GuardFriction.Phase).To(Equal("execute"))

		encoded, err := json.Marshal(event)
		Expect(err).NotTo(HaveOccurred())

		jsonText := string(encoded)
		for _, want := range []string{`"type":"guard_friction"`, `"schema":"idiom.guard_friction.v1"`, `"guard_surface":"completion_gate"`, `"safe_alternatives":["\u003c\u003cQUESTION\u003e\u003e:","Decision: source-satisfied","submit_plan"]`} {
			Expect(jsonText).To(ContainSubstring(want))
		}

		for _, forbidden := range []string{"raw_prompt", "raw_transcript", "file_body", "old_text", "new_text", "secret="} {
			Expect(jsonText).NotTo(ContainSubstring(forbidden))
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
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.GuardFriction.RetryOutcome).To(Equal(GuardFrictionRetryNotApplicable))
		Expect(stored.GuardFriction.Avoidability).To(Equal(GuardFrictionAvoidabilityUnknown))
	})

	It("carries client classification and product metadata on obligation events", func() {
		contract := newEventTestPhaseContract()
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
		Expect(err).NotTo(HaveOccurred())

		encoded, err := json.Marshal(event)
		Expect(err).NotTo(HaveOccurred())

		var roundTrip Event

		Expect(json.Unmarshal(encoded, &roundTrip)).To(Succeed())
		Expect(roundTrip.Validate()).To(Succeed())

		for _, want := range allClientClassifications() {
			Expect(roundTrip.Metadata.ClientClassifications).To(ContainElement(want))
		}

		for _, want := range allProductSignals() {
			Expect(roundTrip.Metadata.ProductSignals).To(ContainElement(want))
		}
	})

	It("rejects invalid event payload shapes", func() {
		contract := newEventTestPhaseContract()
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

		Expect(err).To(MatchError(ContainSubstring("obligation_resolved requires status resolved")))

		_, err = NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:     GateKind("unknown_gate"),
			Decision: PolicyAllow,
		}, time.Date(2026, 5, 13, 17, 30, 0, 0, time.UTC), 2)
		Expect(err).To(MatchError(ContainSubstring("gate.gate is invalid")))

		_, err = NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:     GateLoop,
			Decision: PolicyStop,
			Attempts: -1,
		}, time.Date(2026, 5, 13, 17, 45, 0, 0, time.UTC), 3)
		Expect(err).To(MatchError(ContainSubstring("gate.attempts cannot be negative")))

		_, err = NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
			InvariantID:          "completion.submit_plan_or_terminal",
			ProtocolHash:         "protocol-abc",
			Subject:              GuardFrictionSubject("raw_prompt"),
			Decision:             GuardFrictionDecisionReject,
			GuardSurface:         GuardFrictionSurfaceCompletionGate,
			ModelContractVisible: true,
		}, time.Date(2026, 5, 13, 17, 50, 0, 0, time.UTC), 4)
		Expect(err).To(MatchError(ContainSubstring("guard_friction.subject is invalid")))

		_, err = NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
			InvariantID:          "completion.submit_plan_or_terminal",
			Subject:              GuardFrictionSubjectToolCall,
			Decision:             GuardFrictionDecisionReject,
			GuardSurface:         GuardFrictionSurfaceCompletionGate,
			ModelContractVisible: true,
		}, time.Date(2026, 5, 13, 17, 55, 0, 0, time.UTC), 5)
		Expect(err).To(MatchError(ContainSubstring("protocol_hash is required")))

		_, err = NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
			InvariantID:          "completion.submit_plan_or_terminal",
			ProtocolHash:         "protocol-abc",
			Subject:              GuardFrictionSubjectToolCall,
			Decision:             GuardFrictionDecisionReject,
			GuardSurface:         GuardFrictionSurfaceCompletionGate,
			FirstInvalidAction:   "raw_prompt: user said to paste a transcript",
			ModelContractVisible: true,
		}, time.Date(2026, 5, 13, 17, 56, 0, 0, time.UTC), 6)
		Expect(err).To(MatchError(ContainSubstring("redaction-safe metadata")))
	})
})

func newEventTestPhaseContract() AutonomyContract {
	GinkgoHelper()
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
	Expect(err).NotTo(HaveOccurred())

	return contract
}

func newEventTestReadOnlySidecarContract() AutonomyContract {
	GinkgoHelper()
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
	Expect(err).NotTo(HaveOccurred())

	return contract
}
