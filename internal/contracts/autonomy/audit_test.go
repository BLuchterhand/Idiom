package autonomy

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func contractGateEventHasExtendedAuditFields() bool {
	typ := reflect.TypeOf(ContractGateEvent{})
	for _, name := range []string{"PackID", "EnforcementMode", "ObservedFacts", "SafeAlternatives"} {
		if _, ok := typ.FieldByName(name); !ok {
			return false
		}
	}

	return true
}

func withGateAuditFields(t *testing.T, gate ContractGateEvent, packID, enforcementMode string, observedFacts, safeAlternatives []string) ContractGateEvent {
	t.Helper()

	value := reflect.ValueOf(&gate).Elem()
	setGateStringField(t, value, "PackID", packID)
	setGateStringField(t, value, "EnforcementMode", enforcementMode)
	setGateStringSliceField(t, value, "ObservedFacts", observedFacts)
	setGateStringSliceField(t, value, "SafeAlternatives", safeAlternatives)

	return gate
}

func setGateStringField(t *testing.T, value reflect.Value, name, fieldValue string) {
	t.Helper()

	field := value.FieldByName(name)
	if !field.IsValid() {
		return
	}

	if field.Kind() != reflect.String || !field.CanSet() {
		t.Fatalf("ContractGateEvent.%s is not a settable string field", name)
	}

	field.SetString(fieldValue)
}

func setGateStringSliceField(t *testing.T, value reflect.Value, name string, fieldValue []string) {
	t.Helper()

	field := value.FieldByName(name)
	if !field.IsValid() {
		return
	}

	if field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.String || !field.CanSet() {
		t.Fatalf("ContractGateEvent.%s is not a settable []string field", name)
	}

	slice := reflect.MakeSlice(field.Type(), len(fieldValue), len(fieldValue))
	for i, value := range fieldValue {
		slice.Index(i).SetString(value)
	}

	field.Set(slice)
}

func TestAuditEventsReportsLifecycleOnlyCoverage(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 0, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))

	report := AuditEvents([]Event{compiled}, EventAuditOptions{})

	if report.SchemaVersion != AuditReportSchemaVersion || report.TotalEvents != 1 {
		t.Fatalf("unexpected report identity: %#v", report)
	}

	if report.LifecycleCoverage.Compiled != 1 || report.LifecycleCoverage.Total != 1 {
		t.Fatalf("expected lifecycle coverage, got %#v", report.LifecycleCoverage)
	}

	if report.PolicyCoverage.Total != 0 || report.ObligationCoverage.Total != 0 {
		t.Fatalf("lifecycle-only audit should not synthesize policy/obligation coverage: policy=%#v obligation=%#v", report.PolicyCoverage, report.ObligationCoverage)
	}

	if len(report.LifecycleCoverage.LifecycleOnlyContracts) != 1 || report.LifecycleCoverage.LifecycleOnlyContracts[0] != contract.ContractID {
		t.Fatalf("expected lifecycle-only contract %q, got %#v", contract.ContractID, report.LifecycleCoverage.LifecycleOnlyContracts)
	}

	if len(report.Findings) != 0 {
		t.Fatalf("lifecycle-only coverage should not report reference findings, got %#v", report.Findings)
	}
}

func TestAuditEventsJoinsPolicyAndObligationCoverage(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 30, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	policy := mustAutonomyEvent(NewToolPolicyDecisionEvent(contract, ContractPolicyEvent{
		ToolCallID:        "tool-apply-1",
		ToolName:          "apply_patch",
		Capability:        CapabilityApplyPatch,
		SideEffect:        SideEffectAdditive,
		Decision:          PolicyAllow,
		ReasonCode:        "required_edit_path",
		ObligationsOpened: []string{"obl-verify"},
	}, occurredAt.Add(time.Second), 2))
	opened := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-verify",
		Kind:         ObligationVerify,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "post_write_verification",
	}, occurredAt.Add(2*time.Second), 3))
	resolved := mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
		ObligationID: "obl-verify",
		Kind:         ObligationVerify,
		Status:       ObligationResolved,
		Owner:        OwnerHost,
		RequiredBy:   "post_write_verification",
		EvidenceRef:  "event-test-pass",
	}, occurredAt.Add(3*time.Second), 4))

	report := AuditEvents([]Event{compiled, policy, opened, resolved}, EventAuditOptions{})

	if report.PolicyCoverage.DecisionEvents != 1 || report.PolicyCoverage.DecisionCounts[string(PolicyAllow)] != 1 || report.PolicyCoverage.ToolCounts["apply_patch"] != 1 {
		t.Fatalf("expected joined policy coverage, got %#v", report.PolicyCoverage)
	}

	if report.ObligationCoverage.Opened != 1 || report.ObligationCoverage.Resolved != 1 || len(report.ObligationCoverage.UnresolvedObligations) != 0 {
		t.Fatalf("expected resolved obligation coverage, got %#v", report.ObligationCoverage)
	}

	if len(report.Contracts) != 1 || report.Contracts[0].PolicyTools["apply_patch"] != 1 || report.Contracts[0].ObligationStatusCounts[string(ObligationResolved)] != 1 {
		t.Fatalf("expected per-contract joined coverage, got %#v", report.Contracts)
	}

	if !report.AllClear || len(report.Findings) != 0 {
		t.Fatalf("expected all-clear joined coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	}
}

func TestAuditEventsReportsGateCoverageAndUnresolvedObligations(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 45, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	writeOpened := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-write-runner",
		Kind:         ObligationWrite,
		Status:       ObligationOpen,
		Owner:        OwnerLane,
		TargetRef:    "internal/runtime/agent/runner.go",
		RequiredBy:   "required_edit_paths",
	}, occurredAt.Add(time.Second), 2))
	verifyOpened := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-verify",
		Kind:         ObligationVerify,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "post_write_verification",
	}, occurredAt.Add(2*time.Second), 3))
	writeGate := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:            GateWrite,
		Decision:        PolicyReject,
		InvariantID:     "write.required_edit_paths",
		ReasonCode:      "outside_required_edit_paths",
		ToolName:        "apply_patch",
		ObligationRefs:  []string{"obl-write-runner"},
		OpenObligations: []string{"obl-write-runner"},
		Source:          "audit_test",
	}, string(ContractPackWriteRequiredPaths), string(EnforcementModeObserveOnly), []string{"write_gate_decision"}, nil), occurredAt.Add(3*time.Second), 4))
	verifyGate := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:               GateVerification,
		Decision:           PolicyReject,
		InvariantID:        "verification.post_write",
		ReasonCode:         "verification_required_after_write",
		ToolName:           "read_file",
		VerificationStatus: "pending",
		ObligationRefs:     []string{"obl-verify"},
		OpenObligations:    []string{"obl-verify"},
		Source:             "audit_test",
	}, string(ContractPackVerificationPostWrite), string(EnforcementModeObserveOnly), []string{"verification_gate_decision"}, nil), occurredAt.Add(4*time.Second), 5))

	report := AuditEvents([]Event{compiled, writeOpened, verifyOpened, writeGate, verifyGate}, EventAuditOptions{})

	if report.GateCoverage.DecisionEvents != 2 ||
		report.GateCoverage.KindCounts[string(GateWrite)] != 1 ||
		report.GateCoverage.KindCounts[string(GateVerification)] != 1 ||
		report.GateCoverage.DecisionCounts[string(PolicyReject)] != 2 {

		t.Fatalf("expected gate decision coverage, got %#v", report.GateCoverage)
	}

	for _, want := range []string{"obl-write-runner", "obl-verify"} {
		if !slices.Contains(report.GateCoverage.OpenObligationRefs, want) {
			t.Fatalf("gate coverage missing open obligation %q: %#v", want, report.GateCoverage.OpenObligationRefs)
		}
	}

	if len(report.ObligationCoverage.UnresolvedObligations) != 2 || report.AllClear {
		t.Fatalf("expected unresolved obligations to be explicit and all_clear=false, got all_clear=%v obligation=%#v", report.AllClear, report.ObligationCoverage)
	}

	if len(report.LifecycleCoverage.LifecycleOnlyContracts) != 0 {
		t.Fatalf("gate-bearing contract should not be lifecycle-only: %#v", report.LifecycleCoverage.LifecycleOnlyContracts)
	}

	if len(report.Contracts) != 1 || report.Contracts[0].GateKindCounts[string(GateWrite)] != 1 {
		t.Fatalf("expected per-contract gate coverage, got %#v", report.Contracts)
	}
}

func TestAuditEventsCountsReadinessAndCompletionCoverage(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 46, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	readiness := mustAutonomyEvent(NewReadinessStateEvent(contract, ContractReadinessEvent{
		ReadyToAnswer:       true,
		ReadyToVerify:       true,
		ClosedClaims:        []string{"claim:source-body-read"},
		OpenObligations:     []string{"obl-verify"},
		EvidenceRefs:        []string{"event-read-source"},
		FurtherReadsRequire: "one exact missing claim",
	}, occurredAt.Add(time.Second), 2))
	completion := mustAutonomyEvent(NewCompletionGateDecisionEvent(contract, ContractCompletionEvent{
		Decision:           PolicyAllow,
		InvariantID:        "completion.readiness",
		ProtocolHash:       "sha256:phase",
		VerificationStatus: "passed",
		ChangedPaths:       []string{"internal/contracts/autonomy/audit.go"},
		RequiredPathStatus: []string{"internal/contracts/autonomy/audit.go:changed"},
	}, occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, readiness, completion}, EventAuditOptions{})

	if report.ReadinessCoverage.Total != 1 ||
		report.ReadinessCoverage.StateUpdatedEvents != 1 ||
		report.ReadinessCoverage.ReadyToAnswerEvents != 1 ||
		report.ReadinessCoverage.ReadyToWriteEvents != 0 ||
		report.ReadinessCoverage.ReadyToVerifyEvents != 1 ||
		report.ReadinessCoverage.EventsWithClosedClaims != 1 ||
		report.ReadinessCoverage.EventsWithOpenObligations != 1 ||
		report.ReadinessCoverage.EventsWithEvidenceRefs != 1 ||
		report.ReadinessCoverage.EventsWithFurtherReadCriteria != 1 {

		t.Fatalf("expected readiness coverage counts, got %#v", report.ReadinessCoverage)
	}

	if !slices.Contains(report.ReadinessCoverage.ContractsWithReadiness, contract.ContractID) {
		t.Fatalf("expected contract readiness coverage for %q, got %#v", contract.ContractID, report.ReadinessCoverage.ContractsWithReadiness)
	}

	if report.CompletionCoverage.Total != 1 ||
		report.CompletionCoverage.DecisionEvents != 1 ||
		report.CompletionCoverage.DecisionCounts[string(PolicyAllow)] != 1 ||
		report.CompletionCoverage.VerificationStatusCounts["passed"] != 1 ||
		report.CompletionCoverage.EventsWithProtocolHash != 1 ||
		report.CompletionCoverage.EventsMissingProtocolHash != 0 ||
		report.CompletionCoverage.EventsWithChangedPaths != 1 ||
		report.CompletionCoverage.EventsWithRequiredPathStatus != 1 {

		t.Fatalf("expected completion coverage counts, got %#v", report.CompletionCoverage)
	}

	if !slices.Contains(report.CompletionCoverage.ContractsWithCompletion, contract.ContractID) {
		t.Fatalf("expected contract completion coverage for %q, got %#v", contract.ContractID, report.CompletionCoverage.ContractsWithCompletion)
	}

	if len(report.Contracts) != 1 ||
		report.Contracts[0].ReadinessEvents[string(EventReadinessStateUpdated)] != 1 ||
		report.Contracts[0].ReadinessSignalCounts["ready_to_answer"] != 1 ||
		report.Contracts[0].ReadinessSignalCounts["ready_to_verify"] != 1 ||
		report.Contracts[0].ReadinessSignalCounts["closed_claims"] != 1 ||
		report.Contracts[0].CompletionEvents[string(EventCompletionGateDecision)] != 1 ||
		report.Contracts[0].CompletionDecisions[string(PolicyAllow)] != 1 ||
		report.Contracts[0].CompletionVerifications["passed"] != 1 ||
		report.Contracts[0].CompletionSignals["protocol_hash_present"] != 1 ||
		report.Contracts[0].CompletionSignals["changed_paths"] != 1 ||
		report.Contracts[0].CompletionSignals["required_path_status"] != 1 {

		t.Fatalf("expected per-contract readiness/completion coverage, got %#v", report.Contracts)
	}

	if len(report.LifecycleCoverage.LifecycleOnlyContracts) != 0 {
		t.Fatalf("readiness/completion-bearing contract should not be lifecycle-only: %#v", report.LifecycleCoverage.LifecycleOnlyContracts)
	}

	if !report.AllClear || len(report.Findings) != 0 {
		t.Fatalf("expected all-clear readiness/completion coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	}
}

func TestAuditEventsFlagsCompletionAllowWithRemainingObligationsButOnlyCountsMissingProtocolHash(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 46, 30, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	allowWithRemaining := mustAutonomyEvent(NewCompletionGateDecisionEvent(contract, ContractCompletionEvent{
		Decision:             PolicyAllow,
		InvariantID:          "completion.readiness",
		ProtocolHash:         "sha256:phase",
		RemainingObligations: []string{"obl-verify"},
		VerificationStatus:   "pending",
	}, occurredAt.Add(time.Second), 2))
	missingProtocolHash := mustAutonomyEvent(NewCompletionGateDecisionEvent(contract, ContractCompletionEvent{
		Decision:           PolicyReject,
		InvariantID:        "completion.readiness",
		VerificationStatus: "pending",
	}, occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, allowWithRemaining, missingProtocolHash}, EventAuditOptions{})

	if report.CompletionCoverage.DecisionEvents != 2 ||
		report.CompletionCoverage.DecisionCounts[string(PolicyAllow)] != 1 ||
		report.CompletionCoverage.DecisionCounts[string(PolicyReject)] != 1 ||
		report.CompletionCoverage.EventsWithRemainingObligations != 1 ||
		report.CompletionCoverage.EventsWithProtocolHash != 1 ||
		report.CompletionCoverage.EventsMissingProtocolHash != 1 {

		t.Fatalf("expected completion coverage counts, got %#v", report.CompletionCoverage)
	}

	if len(report.CompletionCoverage.AllowWithRemainingObligationsRefs) != 1 ||
		report.CompletionCoverage.AllowWithRemainingObligationsRefs[0].EventID != allowWithRemaining.EventID {

		t.Fatalf("expected allow-with-remaining-obligations ref, got %#v", report.CompletionCoverage.AllowWithRemainingObligationsRefs)
	}

	if len(report.CompletionCoverage.MissingProtocolHashRefs) != 1 ||
		report.CompletionCoverage.MissingProtocolHashRefs[0].EventID != missingProtocolHash.EventID {

		t.Fatalf("expected missing protocol hash ref, got %#v", report.CompletionCoverage.MissingProtocolHashRefs)
	}

	if !hasEventAuditFinding(report, "contract_event_completion_allow_with_remaining_obligations") {
		t.Fatalf("expected completion allow finding, got %#v", report.Findings)
	}

	if hasEventAuditFinding(report, "contract_event_completion_missing_protocol_hash") {
		t.Fatalf("missing completion protocol hash should remain coverage-only, got %#v", report.Findings)
	}

	if report.AllClear {
		t.Fatalf("completion allow with remaining obligations should not be all-clear")
	}
}

func TestAuditEventsCountsExtendedGateDecisionFieldsWhenPresent(t *testing.T) {
	if !contractGateEventHasExtendedAuditFields() {
		t.Skip("ContractGateEvent extended audit fields are not present in this checkout")
	}

	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 47, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	first := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:        GateCompletion,
		Decision:    PolicyReject,
		InvariantID: "completion.readiness",
		ReasonCode:  "verification_required",
		Source:      "audit_test",
	}, string(ContractPackCompletionReadiness), string(EnforcementModeEnforce), []string{"completion_ready=false"}, []string{"<<QUESTION>>:"}), occurredAt.Add(time.Second), 2))
	second := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:        GateReadOnly,
		Decision:    PolicyObserve,
		InvariantID: "sidecar.read_only_profile",
		ReasonCode:  "read_only_contract",
		Source:      "audit_test",
	}, string(ContractPackCompletionReadiness), string(EnforcementModeObserveOnly), nil, nil), occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, first, second}, EventAuditOptions{})

	if report.GateCoverage.DecisionCountsByPackID[string(ContractPackCompletionReadiness)] != 2 ||
		report.GateCoverage.DecisionCountsByEnforcementMode["enforce"] != 1 ||
		report.GateCoverage.DecisionCountsByEnforcementMode["observe_only"] != 1 {

		t.Fatalf("expected gate decisions counted by pack and enforcement mode, got %#v", report.GateCoverage)
	}

	if report.GateCoverage.EventsWithObservedFacts != 1 || report.GateCoverage.EventsWithSafeAlternatives != 1 {
		t.Fatalf("expected observed_fact and safe_alternative event counts, got %#v", report.GateCoverage)
	}

	if len(report.GateCoverage.MissingPackIDRefs) != 0 ||
		len(report.GateCoverage.MissingEnforcementModeRefs) != 0 ||
		len(report.GateCoverage.MissingSafeAlternativesRefs) != 0 {

		t.Fatalf("complete extended fields should not be flagged: %#v", report.GateCoverage)
	}
}

func TestAuditEventsFlagsMissingExtendedGateFieldsWhenPresent(t *testing.T) {
	if !contractGateEventHasExtendedAuditFields() {
		t.Skip("ContractGateEvent extended audit fields are not present in this checkout")
	}

	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 48, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	missingPackAndAlternative := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:        GateCompletion,
		Decision:    PolicyReject,
		InvariantID: "completion.readiness",
		ReasonCode:  "verification_required",
		Source:      "audit_test",
	}, "", string(EnforcementModeEnforce), []string{"completion_ready=false"}, nil), occurredAt.Add(time.Second), 2))
	missingMode := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:        GateWrite,
		Decision:    PolicyAllow,
		InvariantID: "write.scope",
		ReasonCode:  "in_scope",
		Source:      "audit_test",
	}, string(ContractPackWriteRequiredPaths), "", nil, nil), occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, missingPackAndAlternative, missingMode}, EventAuditOptions{})

	if len(report.GateCoverage.MissingPackIDRefs) != 1 ||
		report.GateCoverage.MissingPackIDRefs[0].Reference != "pack_id" {

		t.Fatalf("expected missing pack_id ref, got %#v", report.GateCoverage.MissingPackIDRefs)
	}

	if len(report.GateCoverage.MissingEnforcementModeRefs) != 1 ||
		report.GateCoverage.MissingEnforcementModeRefs[0].Reference != "enforcement_mode" {

		t.Fatalf("expected missing enforcement_mode ref, got %#v", report.GateCoverage.MissingEnforcementModeRefs)
	}

	if len(report.GateCoverage.MissingSafeAlternativesRefs) != 1 ||
		report.GateCoverage.MissingSafeAlternativesRefs[0].Reference != "safe_alternatives" {

		t.Fatalf("expected missing safe_alternatives ref, got %#v", report.GateCoverage.MissingSafeAlternativesRefs)
	}

	for _, want := range []string{
		"contract_event_gate_missing_pack_id",
		"contract_event_gate_missing_enforcement_mode",
		"contract_event_gate_missing_safe_alternatives",
	} {
		if !hasEventAuditFinding(report, want) {
			t.Fatalf("expected finding %q, got %#v", want, report.Findings)
		}
	}
}

func TestAuditEventsFlagsGateOpenObligationRefsNotOpenAtGateSequence(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 50, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	opened := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-evidence",
		Kind:         ObligationEvidence,
		Status:       ObligationOpen,
		Owner:        OwnerSidecar,
		RequiredBy:   "sidecar_request",
	}, occurredAt.Add(time.Second), 2))
	resolved := mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
		ObligationID: "obl-evidence",
		Kind:         ObligationEvidence,
		Status:       ObligationResolved,
		Owner:        OwnerSidecar,
		RequiredBy:   "sidecar_request",
		EvidenceRef:  "event-context-pack-result",
	}, occurredAt.Add(2*time.Second), 3))
	gate := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:            GateReadOnly,
		Decision:        PolicyObserve,
		InvariantID:     "sidecar.read_only_profile",
		ReasonCode:      "read_only_contract",
		ToolName:        "context_pack",
		ObligationRefs:  []string{"obl-evidence"},
		OpenObligations: []string{"obl-evidence"},
		Source:          "audit_test",
	}, string(ContractPackCompletionReadiness), string(EnforcementModeObserveOnly), []string{"sidecar_read_only_gate"}, nil), occurredAt.Add(3*time.Second), 4))

	report := AuditEvents([]Event{compiled, opened, resolved, gate}, EventAuditOptions{})

	if len(report.ObligationCoverage.UnresolvedObligations) != 0 {
		t.Fatalf("obligation should be resolved before the gate, got %#v", report.ObligationCoverage)
	}

	if len(report.GateCoverage.InconsistentOpenObligationRefs) != 1 ||
		report.GateCoverage.InconsistentOpenObligationRefs[0].Reference != "obl-evidence" {

		t.Fatalf("expected inconsistent gate open-obligation ref, got %#v", report.GateCoverage.InconsistentOpenObligationRefs)
	}

	if !hasEventAuditFinding(report, "contract_event_gate_open_obligation_inconsistent") {
		t.Fatalf("expected gate inconsistency finding, got %#v", report.Findings)
	}

	if report.AllClear {
		t.Fatalf("inconsistent gate open-obligation refs should not be all-clear")
	}
}

func TestAuditEventsGroupsGuardFrictionWithoutFailingAllClear(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 18, 52, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	first := mustAutonomyEvent(NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
		Step:                 4,
		PhaseID:              "planner.submit_ready",
		ModelCallID:          "call-1",
		SourceEventID:        "gate-1",
		ContractVersion:      "2",
		ProtocolHash:         "protocol-abc",
		InvariantID:          "completion.submit_plan_or_terminal",
		ObligationID:         "required_edit_paths.coverage",
		Subject:              GuardFrictionSubjectToolCall,
		Decision:             GuardFrictionDecisionReject,
		GuardSurface:         GuardFrictionSurfaceCompletionGate,
		FirstInvalidAction:   "search_content",
		VisibleActionClasses: []string{"submit_plan", "source_satisfied"},
		VisibleTools:         []string{"submit_plan"},
		SafeAlternatives:     []string{"submit_plan", "Decision: source-satisfied"},
		ModelContractVisible: true,
		RetryOutcome:         GuardFrictionRetryNotApplicable,
		Avoidability:         GuardFrictionAvoidabilityUnknown,
		EvidenceRefs:         []string{"conversation:record-id"},
	}, occurredAt.Add(time.Second), 2))
	second := mustAutonomyEvent(NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
		Step:                 5,
		PhaseID:              "planner.submit_ready",
		ModelCallID:          "call-2",
		SourceEventID:        "gate-2",
		ContractVersion:      "2",
		InvariantID:          "tool_surface.submit_plan",
		Subject:              GuardFrictionSubjectToolCall,
		Decision:             GuardFrictionDecisionNudge,
		GuardSurface:         GuardFrictionSurfaceToolPolicy,
		FirstInvalidAction:   "submit_plan",
		VisibleActionClasses: []string{"source_satisfied"},
		RetryNumber:          1,
		RetryOutcome:         GuardFrictionRetrySuccess,
		Avoidability:         GuardFrictionAvoidabilityToolSurfaceGap,
		EvidenceRefs:         []string{"telemetry:event-id"},
	}, occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, first, second}, EventAuditOptions{})

	coverage := report.GuardFrictionCoverage
	if coverage.Total != 2 || coverage.AvoidableFriction != 1 || coverage.UnclassifiedFriction != 1 {
		t.Fatalf("expected guard friction totals, got %#v", coverage)
	}

	if coverage.LaneCounts["implement"] != 2 || coverage.PhaseCounts["planner.submit_ready"] != 2 {
		t.Fatalf("expected lane/phase friction grouping, got %#v", coverage)
	}

	if coverage.AvoidabilityCounts[string(GuardFrictionAvoidabilityUnknown)] != 1 ||
		coverage.AvoidabilityCounts[string(GuardFrictionAvoidabilityToolSurfaceGap)] != 1 {

		t.Fatalf("expected avoidability grouping, got %#v", coverage.AvoidabilityCounts)
	}

	if coverage.FirstInvalidActionCounts["search_content"] != 1 ||
		coverage.RetryOutcomeCounts[string(GuardFrictionRetrySuccess)] != 1 ||
		coverage.GuardSurfaceCounts[string(GuardFrictionSurfaceCompletionGate)] != 1 {

		t.Fatalf("expected action/retry/surface grouping, got %#v", coverage)
	}

	if coverage.SafeAlternativeCoverage.EventsWithSafeAlternatives != 1 ||
		coverage.SafeAlternativeCoverage.EventsMissingSafeAlternatives != 1 ||
		coverage.SafeAlternativeCoverage.SafeAlternativeCounts["submit_plan"] != 1 ||
		coverage.SafeAlternativeCoverage.VisibleActionClassCounts["source_satisfied"] != 2 ||
		coverage.SafeAlternativeCoverage.VisibleToolCounts["submit_plan"] != 1 {

		t.Fatalf("expected safe alternative coverage, got %#v", coverage.SafeAlternativeCoverage)
	}

	if len(coverage.TopContracts) != 1 ||
		coverage.TopContracts[0].ContractID != contract.ContractID ||
		coverage.TopContracts[0].AvoidableFriction != 1 ||
		coverage.TopContracts[0].UnclassifiedFriction != 1 {

		t.Fatalf("expected top contract friction grouping, got %#v", coverage.TopContracts)
	}

	if len(coverage.UnclassifiedRefs) != 1 || coverage.UnclassifiedRefs[0].EventID != first.EventID {
		t.Fatalf("expected unclassified friction ref for unknown avoidability, got %#v", coverage.UnclassifiedRefs)
	}

	if !report.AllClear || len(report.Findings) != 0 {
		t.Fatalf("guard friction activity should not fail all_clear, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	}
}

func TestAuditEventsReportsModelExchangeRollbackCoverage(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 6, 3, 14, 15, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	tracePolicy := mustAutonomyEvent(NewModelExchangeEvent(contract, ContractModelExchangeEvent{
		Kind:         "trace_store_policy",
		ContractID:   contract.ContractID,
		ProtocolHash: "sha256:protocol",
		TraceStorePolicy: &TraceStorePolicy{
			ContractID:      contract.ContractID,
			ProtocolHash:    "sha256:protocol",
			RetentionRef:    "retention:test",
			RedactionRef:    "redaction:metadata_only",
			RedactionStatus: "pass",
			AllowedFields:   []string{"event_id", "type", "contract_id", "protocol_hash"},
			ForbiddenFields: []string{"raw_prompt", "raw_transcript", "file_body"},
			StoreRefs:       []string{"role:autonomy_event"},
			CapturedBy:      "harness",
		},
	}, occurredAt.Add(time.Second), 2))
	rollback := mustAutonomyEvent(NewModelExchangeEvent(contract, ContractModelExchangeEvent{
		Kind:         "rollback_control",
		ContractID:   contract.ContractID,
		ProtocolHash: "sha256:protocol",
		RollbackControl: &RollbackControl{
			ContractID:   contract.ContractID,
			ProtocolHash: "sha256:protocol",
			RollbackRef:  "env:IDIOM_DISABLE_RUNNER_MODEL_EXCHANGE_EVENTS",
			Decision:     "model_exchange_disabled",
			OperatorRef:  "operator:env",
			Source:       "runner_autonomy",
			PreviousMode: "observe_only_model_exchange",
			NewMode:      "disabled",
			Reason:       "rollback switch active",
			GuardRefs:    []string{"guard:typed_autonomy_events_remain_enabled"},
			AffectedPackIDs: []string{
				string(ContractPackCompletionReadiness),
				string(ContractPackWriteRequiredPaths),
			},
			AuthorizedBy: "harness",
		},
	}, occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, tracePolicy, rollback}, EventAuditOptions{})

	coverage := report.ModelExchangeCoverage
	if coverage.Total != 2 ||
		coverage.TracePolicyEvents != 1 ||
		coverage.RollbackControlEvents != 1 ||
		!coverage.RollbackActive ||
		!report.RollbackActive {

		t.Fatalf("expected model-exchange rollback coverage, got %#v", coverage)
	}

	if coverage.KindCounts["trace_store_policy"] != 1 ||
		coverage.KindCounts["rollback_control"] != 1 ||
		coverage.PayloadCounts["trace_store_policy"] != 1 ||
		coverage.PayloadCounts["rollback_control"] != 1 {

		t.Fatalf("expected model-exchange kind/payload counts, got %#v", coverage)
	}

	if len(coverage.RollbackRefs) != 1 || coverage.RollbackRefs[0].Reference != "model_exchange_disabled" {
		t.Fatalf("expected rollback ref, got %#v", coverage.RollbackRefs)
	}

	if coverage.RedactionStatusCounts["pass"] != 1 || report.PrivacyChecks.RedactionStatusCounts["pass"] != 1 {
		t.Fatalf("expected redaction status coverage, audit=%#v privacy=%#v", coverage.RedactionStatusCounts, report.PrivacyChecks.RedactionStatusCounts)
	}

	if len(report.Contracts) != 1 || report.Contracts[0].ModelExchangeCounts[string(EventModelExchange)] != 2 {
		t.Fatalf("expected per-contract model-exchange counts, got %#v", report.Contracts)
	}
}

func TestAuditEventsOrdersRepeatedContractRunsByOccurrenceBeforeSequence(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	firstRun := time.Date(2026, 5, 13, 18, 55, 0, 0, time.UTC)
	secondRun := firstRun.Add(time.Hour)
	openObligation := func(at time.Time, seq int64) Event {
		return mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "obl-evidence",
			Kind:         ObligationEvidence,
			Status:       ObligationOpen,
			Owner:        OwnerSidecar,
			RequiredBy:   "sidecar_request",
		}, at, seq))
	}
	resolveObligation := func(at time.Time, seq int64) Event {
		return mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
			ObligationID: "obl-evidence",
			Kind:         ObligationEvidence,
			Status:       ObligationResolved,
			Owner:        OwnerSidecar,
			RequiredBy:   "sidecar_request",
			EvidenceRef:  "event-context-pack-result",
		}, at, seq))
	}
	firstCompiled := mustAutonomyEvent(NewContractCompiledEvent(contract, firstRun, 1))
	firstOpened := openObligation(firstRun.Add(time.Second), 2)
	firstResolved := resolveObligation(firstRun.Add(2*time.Second), 3)
	secondCompiled := mustAutonomyEvent(NewContractCompiledEvent(contract, secondRun, 1))
	secondOpened := openObligation(secondRun.Add(time.Second), 2)
	secondGate := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:            GateReadOnly,
		Decision:        PolicyReject,
		InvariantID:     "sidecar.read_only_profile",
		ReasonCode:      "read_only_contract",
		ToolName:        "context_pack",
		ObligationRefs:  []string{"obl-evidence"},
		OpenObligations: []string{"obl-evidence"},
		Source:          "audit_test",
	}, string(ContractPackCompletionReadiness), string(EnforcementModeObserveOnly), []string{"second_run_gate"}, nil), secondRun.Add(2*time.Second), 4))
	secondResolved := resolveObligation(secondRun.Add(3*time.Second), 5)

	report := AuditEvents([]Event{
		secondGate,
		firstResolved,
		secondOpened,
		firstOpened,
		secondResolved,
		secondCompiled,
		firstCompiled,
	}, EventAuditOptions{})

	if len(report.GateCoverage.InconsistentOpenObligationRefs) != 0 {
		t.Fatalf("restarted sequence numbers from a later run should not make open refs inconsistent: %#v", report.GateCoverage.InconsistentOpenObligationRefs)
	}

	if len(report.ObligationCoverage.UnmatchedObligations) != 0 {
		t.Fatalf("restarted sequence numbers from a later run should not make resolutions unmatched: %#v", report.ObligationCoverage.UnmatchedObligations)
	}

	if len(report.ObligationCoverage.UnresolvedObligations) != 0 || !report.AllClear {
		t.Fatalf("expected all-clear repeated-run audit, all_clear=%v obligations=%#v findings=%#v", report.AllClear, report.ObligationCoverage, report.Findings)
	}
}

func TestAuditEventsReportsMissingContractID(t *testing.T) {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "event-missing-contract",
		Type:          EventToolPolicyDecision,
		Effect:        EventEffectObserveOnly,
		OccurredAt:    time.Date(2026, 5, 13, 19, 0, 0, 0, time.UTC),
		Sequence:      1,
		Policy: &ContractPolicyEvent{
			ToolCallID: "tool-read-1",
			ToolName:   "read_file",
			Decision:   PolicyAllow,
		},
	}

	report := AuditEvents([]Event{event}, EventAuditOptions{})

	if len(report.ReferenceCoverage.MissingContractIDEvents) != 1 {
		t.Fatalf("expected one missing contract id event, got %#v", report.ReferenceCoverage)
	}

	if !hasEventAuditFinding(report, "contract_event_missing_contract_id") {
		t.Fatalf("expected missing-contract finding, got %#v", report.Findings)
	}

	if report.AllClear {
		t.Fatalf("missing contract id should not be all clear")
	}
}

func TestAuditEventsReportsStaleAndUnmatchedContractReferences(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 19, 30, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	stopped := mustAutonomyEvent(NewEvent(EventInput{
		Type:       EventContractStopped,
		OccurredAt: occurredAt.Add(time.Second),
		Sequence:   2,
		ContractID: contract.ContractID,
		Lifecycle:  &ContractLifecycleEvent{Reason: "test stop"},
	}))
	afterStop := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "event-after-stop",
		Type:          EventToolPolicyDecision,
		Effect:        EventEffectObserveOnly,
		OccurredAt:    occurredAt.Add(2 * time.Second),
		Sequence:      3,
		ContractID:    contract.ContractID,
		Policy:        &ContractPolicyEvent{ToolCallID: "tool-stale", ToolName: "read_file", Decision: PolicyAllow},
	}
	unmatched := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "event-unknown-contract",
		Type:          EventObligationOpened,
		Effect:        EventEffectObserveOnly,
		OccurredAt:    occurredAt.Add(3 * time.Second),
		Sequence:      4,
		ContractID:    "contract_unknown",
		Obligation:    &ContractObligationEvent{ObligationID: "obl-unknown", Kind: ObligationEvidence, Status: ObligationOpen},
	}

	report := AuditEvents([]Event{compiled, stopped, afterStop, unmatched}, EventAuditOptions{})

	if len(report.ReferenceCoverage.StaleContractReferences) != 1 || report.ReferenceCoverage.StaleContractReferences[0].EventID != "event-after-stop" {
		t.Fatalf("expected stale contract reference, got %#v", report.ReferenceCoverage.StaleContractReferences)
	}

	if len(report.ReferenceCoverage.UnmatchedContractReferences) != 1 || report.ReferenceCoverage.UnmatchedContractReferences[0].Reference != "contract_unknown" {
		t.Fatalf("expected unmatched contract reference, got %#v", report.ReferenceCoverage.UnmatchedContractReferences)
	}

	for _, want := range []string{"contract_event_stale_contract_reference", "contract_event_unmatched_contract_reference"} {
		if !hasEventAuditFinding(report, want) {
			t.Fatalf("expected finding %q, got %#v", want, report.Findings)
		}
	}
}

func TestAuditEventsReportsUnmatchedSidecarToolCallCorrelation(t *testing.T) {
	contract := newEventTestReadOnlySidecarContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 0, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	correlation := CorrelationFromContract(contract)

	correlation.ToolCallID = "tool-001"
	correlation.ToolName = "apply_patch"

	badPolicy := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "event-sidecar-tool-mismatch",
		Type:          EventToolPolicyDecision,
		Effect:        EventEffectObserveOnly,
		OccurredAt:    occurredAt.Add(time.Second),
		Sequence:      2,
		ContractID:    contract.ContractID,
		Correlation:   correlation,
		Policy: &ContractPolicyEvent{
			ToolCallID: "tool-001",
			ToolName:   "apply_patch",
			Decision:   PolicyObserve,
		},
	}

	report := AuditEvents([]Event{compiled, badPolicy}, EventAuditOptions{})

	if report.SidecarToolCorrelation.SidecarEventCount == 0 || report.SidecarToolCorrelation.ToolCallEventCount == 0 {
		t.Fatalf("expected sidecar and tool-call coverage, got %#v", report.SidecarToolCorrelation)
	}

	if len(report.SidecarToolCorrelation.UnmatchedCorrelations) != 1 ||
		!strings.Contains(report.SidecarToolCorrelation.UnmatchedCorrelations[0].Reason, "sidecar_tool") {

		t.Fatalf("expected sidecar/tool mismatch, got %#v", report.SidecarToolCorrelation.UnmatchedCorrelations)
	}

	if !hasEventAuditFinding(report, "contract_event_sidecar_tool_correlation_mismatch") {
		t.Fatalf("expected sidecar correlation finding, got %#v", report.Findings)
	}
}

func TestAuditEventsFlagsTurnPhaseContradictoryToolAction(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 15, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason:       "turn_phase_contract",
		AllowedTools: []string{"submit_plan"},
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "plan_execute.submit_ready.textual",
			ModelCallID:   "call-1",
			Step:          2,
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses: []string{"submit_plan", "source_satisfied", "blocker"},
				InvalidActionClasses: []string{"read", "search", "verify", "per_edit_write"},
			},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "textual",
				ParserContract:           "submit_plan_json",
				ExpressibleActionClasses: []string{"submit_plan", "source_satisfied", "blocker"},
			},
			EnforcementMode: "observe_only",
		},
	}, occurredAt.Add(time.Second), 2))
	readPolicy := mustAutonomyEvent(NewToolPolicyDecisionEvent(contract, ContractPolicyEvent{
		ToolName:   "read_file",
		Capability: CapabilityBoundedRead,
		Decision:   PolicyReject,
		ReasonCode: "tool_surface",
	}, occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, presented, readPolicy}, EventAuditOptions{})

	if report.TurnPhaseCoverage.Total != 1 || report.TurnPhaseCoverage.PhaseCounts["plan_execute.submit_ready.textual"] != 1 {
		t.Fatalf("expected turn phase coverage, got %#v", report.TurnPhaseCoverage)
	}

	if len(report.TurnPhaseCoverage.Contradictions) != 1 ||
		report.TurnPhaseCoverage.Contradictions[0].ToolName != "read_file" ||
		report.TurnPhaseCoverage.Contradictions[0].ActionClass != "read" {

		t.Fatalf("expected read action contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
	}

	if !hasEventAuditFinding(report, "turn_phase_contract_contradiction") || report.AllClear {
		t.Fatalf("expected contradiction finding and all_clear=false, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	}
}

func TestAuditEventsAllowsTextualPhaseCallableToolsWhenActionIsExpressible(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 20, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason:       "turn_phase_contract",
		AllowedTools: []string{"submit_plan"},
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "plan_execute.submit_ready.textual",
			AllowedTools:  []string{"submit_plan"},
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses: []string{"submit_plan"},
			},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "textual",
				ParserContract:           "submit_plan_json",
				ExpressibleActionClasses: []string{"submit_plan"},
			},
			EnforcementMode: "observe_only",
		},
	}, occurredAt.Add(time.Second), 2))

	report := AuditEvents([]Event{compiled, presented}, EventAuditOptions{})

	if len(report.TurnPhaseCoverage.Contradictions) != 0 {
		t.Fatalf("textual callable tools should reconcile when their action class is expressible, got %#v", report.TurnPhaseCoverage.Contradictions)
	}

	if hasEventAuditFinding(report, "turn_phase_contract_contradiction") {
		t.Fatalf("unexpected contradiction finding for textual callable tools: %#v", report.Findings)
	}
}

func TestAuditEventsAllowsTextualReadToolWhenReadActionIsExpressible(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 22, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason:       "turn_phase_contract",
		AllowedTools: []string{"read_file"},
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "read_only_evidence_pending",
			AllowedTools:  []string{"read_file"},
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses: []string{"read", "final_answer", "blocker"},
				CallableToolNames:    []string{"read_file"},
			},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "textual",
				ParserContract:           "source_refresh",
				ExpectedVisibleOutput:    "visible_text_tool_request",
				ExpressibleActionClasses: []string{"read", "final_answer", "blocker"},
			},
			EnforcementMode: "observe_only",
		},
	}, occurredAt.Add(time.Second), 2))

	report := AuditEvents([]Event{compiled, presented}, EventAuditOptions{})

	if len(report.TurnPhaseCoverage.Contradictions) != 0 {
		t.Fatalf("textual read tool should reconcile through its action class, got %#v", report.TurnPhaseCoverage.Contradictions)
	}

	if hasEventAuditFinding(report, "turn_phase_contract_contradiction") {
		t.Fatalf("unexpected contradiction finding for textual read tool: %#v", report.Findings)
	}
}

func TestAuditEventsUsesCallableToolsForTextualManifestVisibility(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 23, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason:       "turn_phase_contract",
		AllowedTools: []string{"read_file", "task"},
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "generic",
			AllowedTools:  []string{"read_file", "task"},
			HiddenTools:   []string{"task"},
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses: []string{"read", "final_answer", "blocker"},
				CallableToolNames:    []string{"read_file", "task"},
			},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "textual",
				ParserContract:           "source_refresh",
				ExpectedVisibleOutput:    "visible_text_tool_request",
				ExpressibleActionClasses: []string{"read", "final_answer", "blocker"},
			},
			EnforcementMode: "observe_only",
		},
	}, occurredAt.Add(time.Second), 2))

	report := AuditEvents([]Event{compiled, presented}, EventAuditOptions{})

	if len(report.TurnPhaseCoverage.Contradictions) != 0 {
		t.Fatalf("hidden/base-only task should not be audited as manifest-visible, got %#v", report.TurnPhaseCoverage.Contradictions)
	}

	if hasEventAuditFinding(report, "turn_phase_contract_contradiction") {
		t.Fatalf("unexpected contradiction finding for hidden/base-only task: %#v", report.Findings)
	}
}

func TestTurnPhaseActionClassForToolCoversProjectionTools(t *testing.T) {
	cases := map[string]string{
		"read_file":            ActionClassToken(ActionClassRead),
		"read_only_capability": ActionClassToken(ActionClassRead),
		"context_status":       ActionClassToken(ActionClassSearch),
		"history_range":        ActionClassToken(ActionClassRead),
		"history_symbol":       ActionClassToken(ActionClassRead),
		"submit_plan":          ActionClassToken(ActionClassSubmitPlan),
		"apply_patch":          ActionClassToken(ActionClassPerEditWrite),
		"go_diagnostics":       ActionClassToken(ActionClassVerify),
		"task":                 ActionClassToken(ActionClassGenericProbe),
	}
	for tool, want := range cases {
		if got := TurnPhaseActionClassForTool(tool); got != want {
			t.Fatalf("TurnPhaseActionClassForTool(%q) = %q, want %q", tool, got, want)
		}

		if got := ActionClassToken(TurnPhaseActionClassForToolClass(tool)); got != want {
			t.Fatalf("TurnPhaseActionClassForToolClass(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestAuditEventsFlagsToolWhenNoManifestToolsAreAllowed(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 25, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason: "turn_phase_contract",
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "product.acceptance_closeout",
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses: []string{"final_answer", "blocker"},
				InvalidActionClasses: []string{"read", "search", "verify"},
			},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "none",
				ExpressibleActionClasses: []string{"final_answer", "blocker"},
			},
			EnforcementMode: "observe_only",
		},
	}, occurredAt.Add(time.Second), 2))
	policy := mustAutonomyEvent(NewToolPolicyDecisionEvent(contract, ContractPolicyEvent{
		ToolName:   "read_file",
		Capability: CapabilityBoundedRead,
		Decision:   PolicyReject,
	}, occurredAt.Add(2*time.Second), 3))

	report := AuditEvents([]Event{compiled, presented, policy}, EventAuditOptions{})

	if len(report.TurnPhaseCoverage.Contradictions) != 1 ||
		report.TurnPhaseCoverage.Contradictions[0].ToolName != "read_file" {

		t.Fatalf("expected no-manifest tool contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
	}
}

func TestAuditEventsReconcilesTurnPhaseSurfaceSummary(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 27, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason: "turn_phase_contract",
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "plan_execute.submit_ready.textual",
			ModelCallID:   "call-1",
			ProtocolHash:  "sha256:phase",
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses:   []string{"submit_plan", "source_satisfied", "blocker"},
				InvalidActionClasses:   []string{"read", "search", "verify", "per_edit_write"},
				TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
			},
			TerminalOutcomes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "textual",
				ParserContract:           "submit_plan_json",
				ResponseFormat:           "json",
				ExpressibleActionClasses: []string{"submit_plan", "source_satisfied", "blocker"},
			},
			EnforcementMode: "observe_only",
		},
		SurfaceSummaries: []TurnPhaseSurfaceSummaryEvent{{
			SchemaVersion:           TurnPhaseSurfaceSummarySchemaVersion,
			SurfaceID:               "next_action_state",
			ContractID:              contract.ContractID,
			PhaseID:                 "plan_execute.submit_ready.textual",
			ModelCallID:             "call-1",
			ProtocolHash:            "sha256:phase",
			AdvertisedActionClasses: []string{"read"},
			TerminalOutcomes:        []string{"submit_plan"},
			TerminalResponseShapes:  []string{"submit_plan"},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "textual",
				ParserContract:           "submit_plan_json",
				ResponseFormat:           "json",
				ExpressibleActionClasses: []string{"submit_plan", "source_satisfied", "blocker"},
			},
		}},
	}, occurredAt.Add(time.Second), 2))

	report := AuditEvents([]Event{compiled, presented}, EventAuditOptions{})

	if report.TurnPhaseCoverage.SurfaceCounts["next_action_state"] != 1 {
		t.Fatalf("expected next_action_state surface coverage, got %#v", report.TurnPhaseCoverage.SurfaceCounts)
	}

	if len(report.TurnPhaseCoverage.Contradictions) == 0 {
		t.Fatalf("expected surface contradictions, got none")
	}

	var sawInvalidRead bool

	for _, issue := range report.TurnPhaseCoverage.Contradictions {
		if issue.SurfacePair != "turn_phase_contract,next_action_state" || issue.SurfaceID != "next_action_state" {
			t.Fatalf("expected surface-pair attribution, got %#v", issue)
		}

		if issue.InvariantID == "turn_phase.action.invalid_advertised" && issue.ActionClass == "read" {
			sawInvalidRead = true
		}
	}

	if !sawInvalidRead {
		t.Fatalf("expected invalid read contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
	}

	if !hasEventAuditFinding(report, "turn_phase_contract_contradiction") || report.AllClear {
		t.Fatalf("expected contradiction finding and all_clear=false, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	}
}

func TestAuditEventsReconcilesModelProtocolSurface(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 28, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason:       "turn_phase_contract",
		ProtocolHash: "sha256:protocol",
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "plan_execute.submit_ready.textual",
			ModelCallID:   "call-1",
			ProtocolHash:  "sha256:protocol",
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses:   []string{"submit_plan", "source_satisfied", "blocker"},
				InvalidActionClasses:   []string{"read", "search", "verify", "per_edit_write"},
				TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
				CallableToolNames:      []string{"submit_plan"},
			},
			TerminalOutcomes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "textual",
				ParserContract:           "submit_plan_json",
				ResponseFormat:           "json",
				ExpressibleActionClasses: []string{"submit_plan", "source_satisfied", "blocker"},
			},
			EnforcementMode: "observe_only",
		},
		ModelProtocol: &ModelProtocolEvent{
			SchemaVersion:          "idiom.model_protocol_projection.v1",
			ContractID:             contract.ContractID,
			ProtocolHash:           "sha256:protocol",
			PhaseID:                "plan_execute.submit_ready.textual",
			ToolTransportMode:      "textual",
			ParserContract:         "submit_plan_json",
			ResponseFormat:         "json",
			CallableActionClasses:  []string{"submit_plan", "source_satisfied", "blocker", "read"},
			CallableTools:          []string{"submit_plan"},
			TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
		},
	}, occurredAt.Add(time.Second), 2))

	report := AuditEvents([]Event{compiled, presented}, EventAuditOptions{})

	if report.TurnPhaseCoverage.SurfaceCounts["model_protocol"] != 1 {
		t.Fatalf("expected model_protocol surface coverage, got %#v", report.TurnPhaseCoverage.SurfaceCounts)
	}

	if len(report.TurnPhaseCoverage.Contradictions) != 1 {
		t.Fatalf("expected one model-protocol contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
	}

	issue := report.TurnPhaseCoverage.Contradictions[0]
	if issue.SurfacePair != "turn_phase_contract,model_protocol" ||
		issue.InvariantID != "turn_phase.action.invalid_advertised" ||
		issue.ActionClass != "read" {

		t.Fatalf("expected invalid read model-protocol contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
	}
}

func TestReconcileTurnPhaseSurfaceAllowsNativeSubmitOnly(t *testing.T) {
	phase := TurnPhaseContractEvent{
		PhaseID:          "plan_execute.submit_ready.native_required",
		ModelCallID:      "call-native",
		ProtocolHash:     "sha256:native",
		AllowedTools:     []string{"submit_plan"},
		TerminalOutcomes: []string{"submit_plan"},
		ActionPolicy: TurnActionClassPolicyEvent{
			AllowedActionClasses:   []string{"submit_plan"},
			InvalidActionClasses:   []string{"read", "search", "source_satisfied", "blocker"},
			TerminalResponseShapes: []string{"submit_plan"},
			CallableToolNames:      []string{"submit_plan"},
		},
		RuntimeCall: RuntimeCallContractEvent{
			ToolTransportMode:        "native",
			ToolChoice:               "required",
			ParserContract:           "native_submit_plan",
			ExpressibleActionClasses: []string{"submit_plan"},
		},
	}
	surface := TurnPhaseSurfaceSummaryEvent{
		SurfaceID:               "runtime_brief",
		PhaseID:                 phase.PhaseID,
		ModelCallID:             phase.ModelCallID,
		ProtocolHash:            phase.ProtocolHash,
		AdvertisedActionClasses: []string{"submit_plan"},
		CallableTools:           []string{"submit_plan"},
		TerminalOutcomes:        []string{"submit_plan"},
		TerminalResponseShapes:  []string{"submit_plan"},
		RuntimeCall:             phase.RuntimeCall,
	}

	if issues := ReconcileTurnPhaseSurface(phase, surface); len(issues) != 0 {
		t.Fatalf("native submit-only surface should be clean, got %#v", issues)
	}
}

func TestReconcileTurnPhaseSurfaceFlagsProtocolHashParity(t *testing.T) {
	phase := TurnPhaseContractEvent{
		ContractID:       "contract-active",
		PhaseID:          "plan_execute.submit_ready.native_required",
		ModelCallID:      "call-native",
		ProtocolHash:     "sha256:phase",
		TerminalOutcomes: []string{"submit_plan"},
		ActionPolicy: TurnActionClassPolicyEvent{
			AllowedActionClasses:   []string{"submit_plan"},
			TerminalResponseShapes: []string{"submit_plan"},
			CallableToolNames:      []string{"submit_plan"},
		},
		RuntimeCall: RuntimeCallContractEvent{
			ToolTransportMode:        "native",
			ParserContract:           "native_submit_plan",
			ExpressibleActionClasses: []string{"submit_plan"},
		},
	}
	baseSurface := TurnPhaseSurfaceSummaryEvent{
		SurfaceID:               "runtime_brief",
		ContractID:              phase.ContractID,
		PhaseID:                 phase.PhaseID,
		ModelCallID:             phase.ModelCallID,
		ProtocolHash:            phase.ProtocolHash,
		AdvertisedActionClasses: []string{"submit_plan"},
		CallableTools:           []string{"submit_plan"},
		TerminalOutcomes:        []string{"submit_plan"},
		TerminalResponseShapes:  []string{"submit_plan"},
		RuntimeCall:             phase.RuntimeCall,
	}

	if issues := ReconcileTurnPhaseSurface(phase, baseSurface); len(issues) != 0 {
		t.Fatalf("matching protocol hash should be clean, got %#v", issues)
	}

	missingSurface := baseSurface

	missingSurface.ProtocolHash = ""

	missingIssues := ReconcileTurnPhaseSurface(phase, missingSurface)

	if !hasTurnPhaseInvariant(missingIssues, "turn_phase.identity.protocol_hash", "missing protocol_hash") {
		t.Fatalf("expected missing protocol hash issue, got %#v", missingIssues)
	}

	mismatchedSurface := baseSurface

	mismatchedSurface.ProtocolHash = "sha256:other"

	mismatchIssues := ReconcileTurnPhaseSurface(phase, mismatchedSurface)

	if !hasTurnPhaseInvariant(mismatchIssues, "turn_phase.identity.protocol_hash", "differs") {
		t.Fatalf("expected mismatched protocol hash issue, got %#v", mismatchIssues)
	}

	mismatchedContract := baseSurface

	mismatchedContract.ContractID = "contract-other"

	contractIssues := ReconcileTurnPhaseSurface(phase, mismatchedContract)

	if !hasTurnPhaseInvariant(contractIssues, "turn_phase.identity.contract_id", "differs") {
		t.Fatalf("expected mismatched contract id issue, got %#v", contractIssues)
	}
}

func TestReconcileTurnPhaseSurfaceFlagsRuntimeParserParity(t *testing.T) {
	phase := TurnPhaseContractEvent{
		PhaseID:          "plan_execute.required_source",
		ModelCallID:      "call-runtime",
		ProtocolHash:     "sha256:runtime",
		TerminalOutcomes: []string{"Decision: source-satisfied", "<<QUESTION>>:"},
		ActionPolicy: TurnActionClassPolicyEvent{
			AllowedActionClasses:   []string{"read", "source_satisfied", "blocker"},
			InvalidActionClasses:   []string{"submit_plan"},
			TerminalResponseShapes: []string{"Decision: source-satisfied", "<<QUESTION>>:"},
			CallableToolNames:      []string{"read_file"},
		},
		RuntimeCall: RuntimeCallContractEvent{
			ToolTransportMode:        "textual",
			ParserContract:           "source_refresh",
			ResponseFormat:           "json",
			ExpectedVisibleOutput:    "visible_text_tool_request",
			ExpressibleActionClasses: []string{"read", "source_satisfied", "blocker"},
		},
	}
	surface := TurnPhaseSurfaceSummaryEvent{
		SurfaceID:               "model_protocol",
		PhaseID:                 phase.PhaseID,
		ModelCallID:             phase.ModelCallID,
		ProtocolHash:            phase.ProtocolHash,
		AdvertisedActionClasses: []string{"read", "source_satisfied", "blocker"},
		CallableTools:           []string{"read_file"},
		TerminalOutcomes:        []string{"Decision: source-satisfied", "<<QUESTION>>:"},
		TerminalResponseShapes:  []string{"Decision: source-satisfied", "<<QUESTION>>:"},
		RuntimeCall: RuntimeCallContractEvent{
			ToolTransportMode:        "textual",
			ParserContract:           "submit_plan_json",
			ResponseFormat:           "text",
			ExpectedVisibleOutput:    "submit_plan",
			ExpressibleActionClasses: []string{"read", "source_satisfied", "blocker"},
		},
	}

	issues := ReconcileTurnPhaseSurface(phase, surface)
	for _, want := range []string{
		"turn_phase.runtime.parser_contract",
		"turn_phase.runtime.response_format",
		"turn_phase.runtime.expected_visible_output",
	} {
		if !hasTurnPhaseInvariant(issues, want, "differs") {
			t.Fatalf("expected %s issue, got %#v", want, issues)
		}
	}
}

func TestAuditEventsFlagsCompletionGateProtocolHashParity(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 29, 0, 0, time.UTC)
	compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
	presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
		Reason:       "turn_phase_contract",
		ProtocolHash: "sha256:phase",
		TurnPhaseContract: &TurnPhaseContractEvent{
			SchemaVersion: "idiom.turn_phase_contract.v1",
			PhaseID:       "plan_execute.submit_ready.native_required",
			ModelCallID:   "call-gate",
			ProtocolHash:  "sha256:phase",
			ActionPolicy: TurnActionClassPolicyEvent{
				AllowedActionClasses:   []string{"submit_plan"},
				TerminalResponseShapes: []string{"submit_plan"},
			},
			TerminalOutcomes: []string{"submit_plan"},
			RuntimeCall: RuntimeCallContractEvent{
				ToolTransportMode:        "native",
				ParserContract:           "native_submit_plan",
				ExpressibleActionClasses: []string{"submit_plan"},
			},
			EnforcementMode: "observe_only",
		},
	}, occurredAt.Add(time.Second), 2))
	missing := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:        GateCompletion,
		Decision:    PolicyReject,
		InvariantID: "completion.readiness",
	}, string(ContractPackCompletionReadiness), string(EnforcementModeObserveOnly), []string{"completion_gate_missing_hash"}, nil), occurredAt.Add(2*time.Second), 3))
	mismatch := mustAutonomyEvent(NewGateDecisionEvent(contract, withGateAuditFields(t, ContractGateEvent{
		Gate:         GateCompletion,
		Decision:     PolicyReject,
		InvariantID:  "completion.readiness",
		ProtocolHash: "sha256:other",
	}, string(ContractPackCompletionReadiness), string(EnforcementModeObserveOnly), []string{"completion_gate_mismatched_hash"}, nil), occurredAt.Add(3*time.Second), 4))

	report := AuditEvents([]Event{compiled, presented, missing, mismatch}, EventAuditOptions{})

	var missingHash, mismatchedHash bool

	for _, issue := range report.TurnPhaseCoverage.Contradictions {
		if issue.SurfaceID != "completion_gate" || issue.InvariantID != "turn_phase.identity.protocol_hash" {
			continue
		}

		if strings.Contains(issue.Reason, "missing") {
			missingHash = true
		}

		if strings.Contains(issue.Reason, "differs") {
			mismatchedHash = true
		}
	}

	if missingHash || !mismatchedHash {
		t.Fatalf("expected only mismatched completion-gate protocol hash issue, got %#v", report.TurnPhaseCoverage.Contradictions)
	}
}

func TestReconcileTurnPhaseSurfaceBlocksReviewPassWithFailedObligations(t *testing.T) {
	phase := TurnPhaseContractEvent{
		PhaseID:          "plan_execute.review_repair_terminal",
		ModelCallID:      "call-review",
		ProtocolHash:     "sha256:review",
		TerminalOutcomes: []string{"submit_plan", "<<QUESTION>>:"},
		ActionPolicy: TurnActionClassPolicyEvent{
			AllowedActionClasses:   []string{"submit_plan", "blocker"},
			InvalidActionClasses:   []string{"review_pass", "read", "search", "verify"},
			TerminalResponseShapes: []string{"submit_plan", "<<QUESTION>>:"},
			CallableToolNames:      []string{"submit_plan"},
		},
		RuntimeCall: RuntimeCallContractEvent{
			ToolTransportMode:        "native",
			ExpressibleActionClasses: []string{"submit_plan", "blocker"},
		},
		EvidenceState: PhaseEvidenceStateEvent{
			ResidualBlockers: []string{"source_sanity_failed:internal/example.go"},
		},
	}
	surface := TurnPhaseSurfaceSummaryEvent{
		SurfaceID:               "recovery_card",
		PhaseID:                 phase.PhaseID,
		ModelCallID:             phase.ModelCallID,
		ProtocolHash:            phase.ProtocolHash,
		AdvertisedActionClasses: []string{"review_pass", "submit_plan", "blocker"},
		TerminalOutcomes:        []string{"Decision: pass", "submit_plan", "<<QUESTION>>:"},
		TerminalResponseShapes:  []string{"Decision: pass", "submit_plan", "<<QUESTION>>:"},
		RuntimeCall:             phase.RuntimeCall,
	}

	issues := ReconcileTurnPhaseSurface(phase, surface)

	var sawReviewPass bool

	for _, issue := range issues {
		if issue.InvariantID == "turn_phase.action.invalid_advertised" && issue.ActionClass == "review_pass" {
			sawReviewPass = true
		}
	}

	if !sawReviewPass {
		t.Fatalf("expected invalid review_pass contradiction, got %#v", issues)
	}
}

func TestAuditEventsAllClearCoverageCarriesClassificationAndProductSignals(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	occurredAt := time.Date(2026, 5, 13, 20, 30, 0, 0, time.UTC)
	events := []Event{
		mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1)),
		mustAutonomyEvent(NewToolPolicyDecisionEvent(contract, ContractPolicyEvent{
			ToolCallID: "tool-test-1",
			ToolName:   "shell",
			Capability: CapabilityShellVerification,
			SideEffect: SideEffectReadOnly,
			Decision:   PolicyAllow,
		}, occurredAt.Add(time.Second), 2)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "obl-client-proof",
			Kind:         ObligationEvidence,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "product_success_fixture",
		}, occurredAt.Add(2*time.Second), 3)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
			ObligationID: "obl-client-proof",
			Kind:         ObligationEvidence,
			Status:       ObligationResolved,
			Owner:        OwnerHost,
			RequiredBy:   "product_success_fixture",
			EvidenceRef:  "event-product-proof",
		}, occurredAt.Add(3*time.Second), 4)),
	}

	report := AuditEvents(events, EventAuditOptions{ExpectedContractIDs: []string{contract.ContractID}})

	if !report.AllClear || len(report.Findings) != 0 {
		t.Fatalf("expected all-clear coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	}

	for _, want := range allClientClassifications() {
		if report.MetadataCoverage.ClientClassificationCounts[string(want)] == 0 {
			t.Fatalf("audit report missing client classification %q in %#v", want, report.MetadataCoverage.ClientClassificationCounts)
		}
	}

	for _, want := range allProductSignals() {
		if report.MetadataCoverage.ProductSignalCounts[string(want)] == 0 {
			t.Fatalf("audit report missing product signal %q in %#v", want, report.MetadataCoverage.ProductSignalCounts)
		}
	}
}

func TestAuditEventsReportsReplayIntentMissingPayloadsAndPrivacy(t *testing.T) {
	occurredAt := time.Date(2026, 5, 13, 20, 35, 0, 0, time.UTC)
	missingPayload := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "event-audit-missing-gate-payload",
		Type:          EventGateDecision,
		Effect:        EventEffectObserveOnly,
		OccurredAt:    occurredAt,
		Sequence:      1,
		ContractID:    "contract-audit-intent",
	}
	rawMarker := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "event-audit-raw-marker",
		Type:          EventToolPolicyDecision,
		Effect:        EventEffectObserveOnly,
		OccurredAt:    occurredAt.Add(time.Second),
		Sequence:      2,
		ContractID:    "contract-audit-intent",
		Policy: &ContractPolicyEvent{
			ToolCallID:       "tool-raw-marker",
			ToolName:         "search_content",
			Capability:       CapabilityLiteralSearch,
			Decision:         PolicyAllow,
			ArgumentsSummary: "raw_prompt: customer-acme token=SECRET",
		},
	}

	report := AuditEvents([]Event{missingPayload, rawMarker}, EventAuditOptions{})

	if report.ReplayMetrics.MissingRequiredPayloadCounts["gate"] != 1 ||
		report.ReplayClassification.MissingRequiredPayloadEvents != 1 {

		t.Fatalf("expected missing gate payload count, classification=%#v metrics=%#v", report.ReplayClassification, report.ReplayMetrics)
	}

	if report.PrivacyChecks.ForbiddenFieldCount == 0 ||
		report.PrivacyChecks.FieldCounts["policy.arguments_summary"] == 0 {

		t.Fatalf("expected privacy forbidden-field detection, got %#v", report.PrivacyChecks)
	}

	if !hasEventAuditFinding(report, "contract_event_privacy_forbidden_raw_content") {
		t.Fatalf("expected privacy finding, got %#v", report.Findings)
	}

	if report.ReplayClassification.Classification != ReplayClassificationNonReplayable {
		t.Fatalf("privacy markers should make replay non-replayable, got %#v", report.ReplayClassification)
	}
}

func mustAutonomyEvent(event Event, err error) Event {
	if err != nil {
		panic(err)
	}

	return event
}

func hasEventAuditFinding(report EventAuditReport, id string) bool {
	for _, finding := range report.Findings {
		if finding.ID == id {
			return true
		}
	}

	return false
}

func hasTurnPhaseInvariant(issues []TurnPhaseContradiction, invariantID string, reasonContains string) bool {
	for _, issue := range issues {
		if issue.InvariantID == invariantID && strings.Contains(issue.Reason, reasonContains) {
			return true
		}
	}

	return false
}
