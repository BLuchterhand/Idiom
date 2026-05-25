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
			ChangedPaths:    []string{"internal/autonomy/replay.go"},
			ObservedFacts: []ContractObservedFact{
				"path_ref:internal/autonomy/replay.go",
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
			ChangedPaths:    []string{"internal/autonomy/replay.go"},
			ObservedFacts: []ContractObservedFact{
				"path_ref:internal/autonomy/replay.go",
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
