package autonomy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReplayEventsValidTrajectory(t *testing.T) {
	contract := newEventTestPhaseContract(t)
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

	if report.SchemaVersion != ReplayReportSchemaVersion || report.TotalEvents != 3 {
		t.Fatalf("unexpected report identity: %#v", report)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("valid trajectory should not violate replay checks: %#v", report.Violations)
	}
	if report.AtomCounts[string(ReplayAtomGateDecision)] != 3 ||
		report.AtomCounts[string(ReplayAtomPackSeen)] != 3 ||
		report.AtomCounts[string(ReplayAtomObservedFact)] != 4 ||
		report.AtomCounts[string(ReplayAtomPathWritten)] != 1 ||
		report.AtomCounts[string(ReplayAtomChangedPath)] != 1 ||
		report.AtomCounts[string(ReplayAtomVerificationStatus)] != 2 {
		t.Fatalf("unexpected atom counts: %#v", report.AtomCounts)
	}
	if report.PackCounts[string(ContractPackWriteRequiredPaths)] != 1 ||
		report.PackCounts[string(ContractPackVerificationPostWrite)] != 1 ||
		report.PackCounts[string(ContractPackCompletionReadiness)] != 1 {
		t.Fatalf("unexpected pack counts: %#v", report.PackCounts)
	}
	if report.EnforcementModeCounts[string(EnforcementModeEnforce)] != 3 {
		t.Fatalf("unexpected enforcement mode counts: %#v", report.EnforcementModeCounts)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, forbidden := range []string{"raw_prompt", "raw_transcript", "file_body", "old_text", "new_text"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("replay report leaked raw content marker %q: %s", forbidden, encoded)
		}
	}
}

func TestReplayEventsFlagsMissingGateFields(t *testing.T) {
	contract := newEventTestPhaseContract(t)
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
		if !hasReplayViolation(report, want) {
			t.Fatalf("expected violation %q, got %#v", want, report.Violations)
		}
	}
}

func TestReplayEventsFlagsRepeatedGateDecision(t *testing.T) {
	contract := newEventTestPhaseContract(t)
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

	if !hasReplayViolation(report, "replay_gate_repeated_decision") {
		t.Fatalf("expected repeated gate violation, got %#v", report.Violations)
	}
	if got := countReplayViolations(report, "replay_gate_repeated_decision"); got != 1 {
		t.Fatalf("expected one repeated gate violation, got %d: %#v", got, report.Violations)
	}
}

func TestReplayEventsFlagsCompletionAllowWithUnresolvedPostWriteVerification(t *testing.T) {
	contract := newEventTestPhaseContract(t)
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

	if !hasReplayViolation(report, "replay_completion_unresolved_post_write_verification") {
		t.Fatalf("expected post-write verification violation, got %#v", report.Violations)
	}
}

func TestReplayEventsClassifiesFullRedactedPartialAndNonReplayable(t *testing.T) {
	full := ReplayEvents(replayClassificationEvents(t, ""), ReplayOptions{})
	if full.ReplayClassification.Classification != ReplayClassificationFull {
		t.Fatalf("expected full replay classification, got %#v", full.ReplayClassification)
	}

	redacted := ReplayEvents(replayClassificationEvents(t, "redacted"), ReplayOptions{})
	if redacted.ReplayClassification.Classification != ReplayClassificationRedacted ||
		redacted.ReplayClassification.RedactedEvents == 0 {
		t.Fatalf("expected redacted replay classification, got %#v", redacted.ReplayClassification)
	}

	partialGate := replayClassificationEvents(t, "")[2]
	partialGate.Gate.VerificationStatus = ""
	partialGate.Gate.ObligationRefs = nil
	partial := ReplayEvents([]Event{partialGate}, ReplayOptions{})
	if partial.ReplayClassification.Classification != ReplayClassificationPartial ||
		partial.ReplayClassification.MissingSignalCounts["verification_status"] == 0 {
		t.Fatalf("expected partial replay classification from missing structured signals, got %#v", partial.ReplayClassification)
	}

	nonReplayable := ReplayEvents([]Event{{
		SchemaVersion: EventSchemaVersion,
		EventID:       "event-missing-gate-payload",
		Type:          EventGateDecision,
		Effect:        EventEffectObserveOnly,
		OccurredAt:    time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC),
		Sequence:      1,
		ContractID:    "contract-missing-gate-payload",
	}}, ReplayOptions{})
	if nonReplayable.ReplayClassification.Classification != ReplayClassificationNonReplayable ||
		nonReplayable.ReplayMetrics.MissingRequiredPayloadCounts["gate"] != 1 {
		t.Fatalf("expected non-replayable missing payload classification, got class=%#v metrics=%#v", nonReplayable.ReplayClassification, nonReplayable.ReplayMetrics)
	}
}

func TestReplayEventsReportsChurnAutonomyMetrics(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	start := time.Date(2026, 5, 24, 13, 5, 0, 0, time.UTC)
	ready := mustAutonomyEvent(NewReadinessStateEvent(contract, ContractReadinessEvent{
		ReadyToAnswer: true,
		EvidenceRefs:  []string{"evidence:ready"},
	}, start, 1))
	var events []Event
	events = append(events, ready)
	for i := 0; i < 2; i++ {
		events = append(events, mustAutonomyEvent(NewToolPolicyDecisionEvent(contract, ContractPolicyEvent{
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
	if metrics.RepeatedSameToolDecisions != 1 ||
		metrics.RepeatedSameGateDecisions != 2 ||
		metrics.RedirectStorms != 1 ||
		metrics.BroadRediscoveryAfterReadiness != 2 ||
		metrics.ZeroHitSearchStreaks != 1 ||
		metrics.MaxZeroHitSearchStreak != 2 ||
		metrics.CandidatePathWaste != 2 {
		t.Fatalf("unexpected replay metrics: %#v", metrics)
	}
	if metrics.PhaseGateFailCounts["execute/completion_gate"] != 3 {
		t.Fatalf("expected phase gate fail counts, got %#v", metrics.PhaseGateFailCounts)
	}
}

func replayClassificationEvents(t *testing.T, redactedValue string) []Event {
	t.Helper()
	contract := newEventTestPhaseContract(t)
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
