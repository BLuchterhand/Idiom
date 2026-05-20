package autonomy

import (
	"slices"
	"strings"
	"testing"
	"time"
)

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
		TargetRef:    "internal/agent/runner.go",
		RequiredBy:   "required_edit_paths",
	}, occurredAt.Add(time.Second), 2))
	verifyOpened := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-verify",
		Kind:         ObligationVerify,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "post_write_verification",
	}, occurredAt.Add(2*time.Second), 3))
	writeGate := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:            GateWrite,
		Decision:        PolicyReject,
		InvariantID:     "write.required_edit_paths",
		ReasonCode:      "outside_required_edit_paths",
		ToolName:        "apply_patch",
		ObligationRefs:  []string{"obl-write-runner"},
		OpenObligations: []string{"obl-write-runner"},
		Source:          "audit_test",
	}, occurredAt.Add(3*time.Second), 4))
	verifyGate := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:               GateVerification,
		Decision:           PolicyReject,
		InvariantID:        "verification.post_write",
		ReasonCode:         "verification_required_after_write",
		ToolName:           "read_file",
		VerificationStatus: "pending",
		ObligationRefs:     []string{"obl-verify"},
		OpenObligations:    []string{"obl-verify"},
		Source:             "audit_test",
	}, occurredAt.Add(4*time.Second), 5))

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
	gate := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:            GateReadOnly,
		Decision:        PolicyObserve,
		InvariantID:     "sidecar.read_only_profile",
		ReasonCode:      "read_only_contract",
		ToolName:        "context_pack",
		ObligationRefs:  []string{"obl-evidence"},
		OpenObligations: []string{"obl-evidence"},
		Source:          "audit_test",
	}, occurredAt.Add(3*time.Second), 4))

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
	secondGate := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
		Gate:            GateReadOnly,
		Decision:        PolicyReject,
		InvariantID:     "sidecar.read_only_profile",
		ReasonCode:      "read_only_contract",
		ToolName:        "context_pack",
		ObligationRefs:  []string{"obl-evidence"},
		OpenObligations: []string{"obl-evidence"},
		Source:          "audit_test",
	}, secondRun.Add(2*time.Second), 4))
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
