package autonomy

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Autonomy event replay", func() {
	It("accepts a valid write, verification, and completion trajectory", func() {
		contract := newEventTestPhaseContract()
		start := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
		events := []Event{
			mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:            GateWrite,
				Decision:        PolicyAllow,
				PackID:          ContractPackWriteRequiredPaths,
				EnforcementMode: EnforcementModeEnforce,
				InvariantID:     "write.required_edit_paths",
				ReasonCode:      "required_path_changed",
				ToolName:        "apply_patch",
				ChangedPaths:    []string{"internal/contracts/autonomy/replay.go"},
				ObservedFacts: []ContractObservedFact{
					"path_ref:internal/contracts/autonomy/replay.go",
					"tool_name:apply_patch",
				},
				Source: "replay_test",
			}, start, 1)),
			mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:               GateVerification,
				Decision:           PolicyAllow,
				PackID:             ContractPackVerificationPostWrite,
				EnforcementMode:    EnforcementModeEnforce,
				InvariantID:        "verification.post_write",
				ReasonCode:         "verification_passed",
				ToolName:           "go test",
				VerificationStatus: "passed",
				ObservedFacts: []ContractObservedFact{
					"status:verification_status=passed",
				},
				Source: "replay_test",
			}, start.Add(time.Second), 2)),
			mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:               GateCompletion,
				Decision:           PolicyAllow,
				PackID:             ContractPackCompletionReadiness,
				EnforcementMode:    EnforcementModeEnforce,
				InvariantID:        "completion.readiness",
				ReasonCode:         "ready",
				VerificationStatus: "passed",
				ObservedFacts: []ContractObservedFact{
					"status:verification_status=passed",
				},
				Source: "replay_test",
			}, start.Add(2*time.Second), 3)),
		}

		report := ReplayEvents(events, ReplayOptions{})

		Expect(report.SchemaVersion).To(Equal(ReplayReportSchemaVersion))
		Expect(report.TotalEvents).To(Equal(3))
		Expect(report.Violations).To(BeEmpty())
		Expect(report.AtomCounts).To(HaveKeyWithValue(string(ReplayAtomGateDecision), 3))
		Expect(report.AtomCounts).To(HaveKeyWithValue(string(ReplayAtomPackSeen), 3))
		Expect(report.AtomCounts).To(HaveKeyWithValue(string(ReplayAtomObservedFact), 4))
		Expect(report.AtomCounts).To(HaveKeyWithValue(string(ReplayAtomPathWritten), 1))
		Expect(report.AtomCounts).To(HaveKeyWithValue(string(ReplayAtomChangedPath), 1))
		Expect(report.AtomCounts).To(HaveKeyWithValue(string(ReplayAtomVerificationStatus), 2))
		Expect(report.PackCounts).To(HaveKeyWithValue(string(ContractPackWriteRequiredPaths), 1))
		Expect(report.PackCounts).To(HaveKeyWithValue(string(ContractPackVerificationPostWrite), 1))
		Expect(report.PackCounts).To(HaveKeyWithValue(string(ContractPackCompletionReadiness), 1))
		Expect(report.EnforcementModeCounts).To(HaveKeyWithValue(string(EnforcementModeEnforce), 3))

		encoded, err := json.Marshal(report)
		Expect(err).NotTo(HaveOccurred())

		for _, forbidden := range []string{"raw_prompt", "raw_transcript", "file_body", "old_text", "new_text"} {
			Expect(string(encoded)).NotTo(ContainSubstring(forbidden))
		}
	})

	It("treats not-required verification as resolved", func() {
		contract := newEventTestPhaseContract()
		start := time.Date(2026, 5, 24, 12, 3, 0, 0, time.UTC)
		events := []Event{
			mustAutonomyEvent(NewCompletionGateDecisionEvent(contract, ContractCompletionEvent{
				Decision:           PolicyAllow,
				InvariantID:        "completion.readiness",
				ProtocolHash:       "sha256:phase",
				VerificationStatus: "not_required",
				ChangedPaths:       []string{"temp.md"},
			}, start, 1)),
		}

		report := ReplayEvents(events, ReplayOptions{})

		Expect(report.Violations).To(BeEmpty())
	})

	It("flags missing structured gate fields", func() {
		contract := newEventTestPhaseContract()
		start := time.Date(2026, 5, 24, 12, 5, 0, 0, time.UTC)
		events := []Event{
			mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:        GateCompletion,
				Decision:    PolicyReject,
				InvariantID: "completion.readiness",
				ReasonCode:  "verification_required",
				Source:      "replay_test",
			}, start, 1)),
			mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:            GateCompletion,
				Decision:        PolicyReject,
				PackID:          ContractPackCompletionReadiness,
				EnforcementMode: EnforcementModeEnforce,
				InvariantID:     "completion.readiness",
				ReasonCode:      "verification_required",
				ObservedFacts: []ContractObservedFact{
					"status:verification_status=pending",
				},
				Source: "replay_test",
			}, start.Add(time.Second), 2)),
		}

		report := ReplayEvents(events, ReplayOptions{})

		for _, want := range []string{
			"replay_gate_missing_pack_id",
			"replay_gate_missing_enforcement_mode",
			"replay_gate_missing_observed_facts",
			"replay_gate_missing_safe_alternatives",
		} {
			Expect(hasReplayViolation(report, want)).To(BeTrue(), "violation %q", want)
		}
	})

	It("flags repeated identical gate decisions", func() {
		contract := newEventTestPhaseContract()
		start := time.Date(2026, 5, 24, 12, 10, 0, 0, time.UTC)

		var events []Event

		for i := 0; i < 3; i++ {
			events = append(events, mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:             GateCompletion,
				Decision:         PolicyReject,
				PackID:           ContractPackCompletionReadiness,
				EnforcementMode:  EnforcementModeEnforce,
				InvariantID:      "completion.readiness",
				ReasonCode:       "verification_required",
				ObservedFacts:    []ContractObservedFact{"status:verification_status=pending"},
				SafeAlternatives: []string{"run focused verification"},
				Source:           "replay_test",
			}, start.Add(time.Duration(i)*time.Second), int64(i+1))))
		}

		report := ReplayEvents(events, ReplayOptions{MaxRepeatedGateDecisions: 2})

		Expect(hasReplayViolation(report, "replay_gate_repeated_decision")).To(BeTrue())
		Expect(countReplayViolations(report, "replay_gate_repeated_decision")).To(Equal(1))
	})

	It("flags completion allowed with unresolved post-write verification", func() {
		contract := newEventTestPhaseContract()
		start := time.Date(2026, 5, 24, 12, 15, 0, 0, time.UTC)
		events := []Event{
			mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:            GateWrite,
				Decision:        PolicyAllow,
				PackID:          ContractPackWriteRequiredPaths,
				EnforcementMode: EnforcementModeEnforce,
				InvariantID:     "write.required_edit_paths",
				ReasonCode:      "required_path_changed",
				ToolName:        "apply_patch",
				ChangedPaths:    []string{"internal/contracts/autonomy/replay.go"},
				ObservedFacts: []ContractObservedFact{
					"path_ref:internal/contracts/autonomy/replay.go",
				},
				Source: "replay_test",
			}, start, 1)),
			mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:               GateCompletion,
				Decision:           PolicyAllow,
				PackID:             ContractPackCompletionReadiness,
				EnforcementMode:    EnforcementModeEnforce,
				InvariantID:        "completion.readiness",
				ReasonCode:         "ready",
				OpenObligations:    []string{"obl-verify"},
				VerificationStatus: "pending",
				ObservedFacts: []ContractObservedFact{
					"status:verification_status=pending",
					"obligation_ref:obl-verify",
				},
				Source: "replay_test",
			}, start.Add(time.Second), 2)),
		}

		report := ReplayEvents(events, ReplayOptions{})

		Expect(hasReplayViolation(report, "replay_completion_unresolved_post_write_verification")).To(BeTrue())
	})

	It("classifies full, redacted, partial, and non-replayable histories", func() {
		full := ReplayEvents(replayClassificationEvents(""), ReplayOptions{})
		Expect(full.ReplayClassification.Classification).To(Equal(ReplayClassificationFull))

		redacted := ReplayEvents(replayClassificationEvents("redacted"), ReplayOptions{})
		Expect(redacted.ReplayClassification.Classification).To(Equal(ReplayClassificationRedacted))
		Expect(redacted.ReplayClassification.RedactedEvents).To(BeNumerically(">", 0))

		partialGate := replayClassificationEvents("")[2]

		partialGate.Gate.VerificationStatus = ""
		partialGate.Gate.ObligationRefs = nil

		partial := ReplayEvents([]Event{partialGate}, ReplayOptions{})

		Expect(partial.ReplayClassification.Classification).To(Equal(ReplayClassificationPartial))
		Expect(partial.ReplayClassification.MissingSignalCounts["verification_status"]).To(BeNumerically(">", 0))

		nonReplayable := ReplayEvents([]Event{{
			SchemaVersion: EventSchemaVersion,
			EventID:       "event-missing-gate-payload",
			Type:          EventGateDecision,
			Effect:        EventEffectObserveOnly,
			OccurredAt:    time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC),
			Sequence:      1,
			ContractID:    "contract-missing-gate-payload",
		}}, ReplayOptions{})
		Expect(nonReplayable.ReplayClassification.Classification).To(Equal(ReplayClassificationNonReplayable))
		Expect(nonReplayable.ReplayMetrics.MissingRequiredPayloadCounts).To(HaveKeyWithValue("gate", 1))
	})

	It("reports churn and autonomy metrics", func() {
		contract := newEventTestPhaseContract()
		start := time.Date(2026, 5, 24, 13, 5, 0, 0, time.UTC)
		ready := mustAutonomyEvent(NewReadinessStateEvent(contract, ContractReadinessEvent{
			ReadyToAnswer: true,
			EvidenceRefs:  []string{"evidence:ready"},
		}, start, 1))

		var events []Event

		events = append(events, ready)

		for i := 0; i < 2; i++ {
			events = append(events, mustAutonomyEvent(NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
				ToolCallID:       "tool-search-" + string(rune('a'+i)),
				ToolName:         "search_content",
				Capability:       CapabilityLiteralSearch,
				Decision:         PolicyAllow,
				ReasonCode:       "zero_hits",
				Result:           "0 hits",
				ArgumentsSummary: "candidate_path_waste",
				PathRoles:        []PathRoleMatch{{Path: "tmp/nope.go", Role: PathRolePublicWorld}},
			}, start.Add(time.Duration(i+1)*time.Second), int64(i+2))))
		}

		for i := 0; i < 3; i++ {
			events = append(events, mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
				Gate:               GateCompletion,
				Decision:           PolicyReject,
				PackID:             ContractPackCompletionReadiness,
				EnforcementMode:    EnforcementModeEnforce,
				InvariantID:        "completion.readiness",
				ReasonCode:         "verification_required",
				ProtocolHash:       "sha256:metric",
				ObligationRefs:     []string{"obl-verify"},
				VerificationStatus: "pending",
				ObservedFacts:      []ContractObservedFact{"status:verification_status=pending"},
				SafeAlternatives:   []string{"run verification"},
				Source:             "replay_test",
			}, start.Add(time.Duration(i+3)*time.Second), int64(i+4))))
		}

		report := ReplayEvents(events, ReplayOptions{})
		metrics := report.ReplayMetrics

		Expect(metrics.RepeatedSameToolDecisions).To(Equal(1))
		Expect(metrics.RepeatedSameGateDecisions).To(Equal(2))
		Expect(metrics.RedirectStorms).To(Equal(1))
		Expect(metrics.BroadRediscoveryAfterReadiness).To(Equal(2))
		Expect(metrics.ZeroHitSearchStreaks).To(Equal(1))
		Expect(metrics.MaxZeroHitSearchStreak).To(Equal(2))
		Expect(metrics.CandidatePathWaste).To(Equal(2))
		Expect(metrics.PhaseGateFailCounts).To(HaveKeyWithValue("execute/completion_gate", 3))
	})

	It("treats resolved passive path roles as scoped", func() {
		contract := newEventTestPhaseContract()
		start := time.Date(2026, 5, 24, 13, 10, 0, 0, time.UTC)

		report := ReplayEvents([]Event{
			mustAutonomyEvent(NewPolicyEvent(EventPathRolesResolved, contract, ContractPolicyEvent{
				ToolCallID: "path-role-resolution",
				ToolName:   "path_role_resolver",
				Capability: CapabilitySourceEdit,
				Decision:   PolicyObserve,
				PathRoles: []PathRoleMatch{
					{Path: "dist/protocol-card.json", Role: PathRoleGeneratedArtifact, MatchedPattern: "dist/**"},
					{Path: "logs/verification/go-test.json", Role: PathRoleVerificationOut, MatchedPattern: "logs/verification/**"},
				},
				Source: "replay_test",
			}, start, 1)),
		}, ReplayOptions{})

		Expect(report.ReplayClassification.EventsWithRequiredPayload).To(Equal(1))
		Expect(report.ReplayMetrics.CandidatePathWaste).To(BeZero())
	})
})

func replayClassificationEvents(redactedValue string) []Event {
	GinkgoHelper()
	contract := newEventTestPhaseContract()
	start := time.Date(2026, 5, 24, 12, 55, 0, 0, time.UTC)
	observedFacts := []ContractObservedFact{
		"path_ref:internal/contracts/autonomy/replay.go",
		"protocol_hash:sha256:full",
	}

	if redactedValue != "" {
		observedFacts = append(observedFacts, ContractObservedFact("status:"+redactedValue))
	}

	return []Event{
		mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
			Reason:       "turn_phase_contract",
			ProtocolHash: "sha256:full",
			TurnPhaseContract: &TurnPhaseContractEvent{
				SchemaVersion: "idiom.turn_phase_contract.v1",
				PhaseID:       "implement.execute",
				ProtocolHash:  "sha256:full",
				RuntimeCall: RuntimeCallContractEvent{
					ToolTransportMode:        "native",
					ParserContract:           "native_tools",
					ExpressibleActionClasses: []string{"read", "per_edit_write", "verify"},
				},
			},
		}, start, 1)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "obl-verify",
			Kind:         ObligationVerify,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "post_write_verification",
		}, start.Add(time.Second), 2)),
		mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:               GateVerification,
			Decision:           PolicyAllow,
			PackID:             ContractPackVerificationPostWrite,
			EnforcementMode:    EnforcementModeEnforce,
			InvariantID:        "verification.post_write",
			ProtocolHash:       "sha256:full",
			ReasonCode:         "verification_passed",
			ObligationRefs:     []string{"obl-verify"},
			VerificationStatus: "passed",
			ObservedFacts:      observedFacts,
			Source:             "replay_test",
		}, start.Add(2*time.Second), 3)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
			ObligationID: "obl-verify",
			Kind:         ObligationVerify,
			Status:       ObligationResolved,
			Owner:        OwnerHost,
			RequiredBy:   "post_write_verification",
			EvidenceRef:  "event-verification-pass",
		}, start.Add(3*time.Second), 4)),
		mustAutonomyEvent(NewModelExchangeEvent(contract, ContractModelExchangeEvent{
			Kind:         "trace_store_policy",
			ContractID:   contract.ContractID,
			ProtocolHash: "sha256:full",
			TraceStorePolicy: &TraceStorePolicy{
				ContractID:      contract.ContractID,
				ProtocolHash:    "sha256:full",
				RetentionRef:    "retention:test",
				RedactionRef:    "redaction:metadata_only",
				RedactionStatus: "pass",
				AllowedFields:   []string{"event_id", "type", "contract_id", "protocol_hash", "refs"},
				ForbiddenFields: []string{"raw_prompt", "raw_transcript", "file_body"},
				StoreRefs:       []string{"role:autonomy_event"},
				CapturedBy:      "harness",
			},
		}, start.Add(4*time.Second), 5)),
	}
}

func hasReplayViolation(report ReplayReport, id string) bool {
	return countReplayViolations(report, id) > 0
}

func countReplayViolations(report ReplayReport, id string) int {
	count := 0

	for _, violation := range report.Violations {
		if violation.ID == id {
			count++
		}
	}

	return count
}
