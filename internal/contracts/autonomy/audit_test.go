package autonomy

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.DescribeTable("open phase-evidence obligations",
	func(state PhaseEvidenceStateEvent, want bool) {
		gomega.Expect(PhaseEvidenceStateHasOpenObligation(state)).To(gomega.Equal(want))
	},
	ginkgo.Entry("recognizes closed evidence", PhaseEvidenceStateEvent{AcceptanceRefs: []string{"ACC-1"}, VerificationStatus: "passed"}, false),
	ginkgo.Entry("recognizes a TDD obligation", PhaseEvidenceStateEvent{TDDObligationRefs: []string{"TDD-1"}}, true),
	ginkgo.Entry("recognizes a residual blocker", PhaseEvidenceStateEvent{ResidualBlockers: []string{"blocked"}}, true),
	ginkgo.Entry("recognizes a recovery packet", PhaseEvidenceStateEvent{RecoveryPacketRefs: []string{"recovery-1"}}, true),
	ginkgo.Entry("recognizes an open product-workflow recovery", PhaseEvidenceStateEvent{ProductWorkflowState: "recovery_open"}, true),
	ginkgo.Entry("normalizes a failed verification status", PhaseEvidenceStateEvent{VerificationStatus: "FAILED"}, true),
	ginkgo.Entry("recognizes a pre-write verification status", PhaseEvidenceStateEvent{VerificationStatus: "pre_write"}, true),
)

func assertAuditGroupExists(groups []AuditGroup, want AuditGroup) {
	ginkgo.GinkgoHelper()

	gomega.Expect(groups).To(gomega.ContainElement(gomega.And(
		gomega.HaveField("ContractID", want.ContractID),
		gomega.HaveField("InvariantID", want.InvariantID),
		gomega.HaveField("Lane", want.Lane),
		gomega.HaveField("Phase", want.Phase),
		gomega.HaveField("AdapterRoute", want.AdapterRoute),
		gomega.HaveField("TerminalState", want.TerminalState),
		gomega.HaveField("EventCount", gomega.BeNumerically(">", 0)),
	)), "missing audit group %#v", want)
}

var _ = ginkgo.Describe("Autonomy event auditing", func() {
	ginkgo.It("reports lifecycle-only coverage", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 18, 0, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))

		report := AuditEvents([]Event{compiled}, EventAuditOptions{})

		gomega.Expect(report.SchemaVersion).To(gomega.Equal(AuditReportSchemaVersion), "unexpected report identity: %#v", report)
		gomega.Expect(report.TotalEvents).To(gomega.Equal(1), "unexpected report identity: %#v", report)

		gomega.Expect(report.LifecycleCoverage.Compiled).To(gomega.Equal(1), "expected lifecycle coverage, got %#v", report.LifecycleCoverage)
		gomega.Expect(report.LifecycleCoverage.Total).To(gomega.Equal(1), "expected lifecycle coverage, got %#v", report.LifecycleCoverage)

		gomega.Expect(report.PolicyCoverage.Total).To(gomega.Equal(0), "lifecycle-only audit should not synthesize policy/obligation coverage: policy=%#v obligation=%#v", report.PolicyCoverage, report.ObligationCoverage)
		gomega.Expect(report.ObligationCoverage.Total).To(gomega.Equal(0), "lifecycle-only audit should not synthesize policy/obligation coverage: policy=%#v obligation=%#v", report.PolicyCoverage, report.ObligationCoverage)

		gomega.Expect(report.LifecycleCoverage.LifecycleOnlyContracts).To(gomega.HaveLen(1), "expected lifecycle-only contract %q, got %#v", contract.ContractID, report.LifecycleCoverage.LifecycleOnlyContracts)
		gomega.Expect(report.LifecycleCoverage.LifecycleOnlyContracts[0]).To(gomega.Equal(contract.ContractID), "expected lifecycle-only contract %q, got %#v", contract.ContractID, report.LifecycleCoverage.LifecycleOnlyContracts)

		gomega.Expect(len(report.Findings) != 0).To(gomega.BeFalse(), "lifecycle-only coverage should not report reference findings, got %#v", report.Findings)
	})

	ginkgo.It("joins policy and obligation coverage", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 18, 30, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
		policy := mustAutonomyEvent(NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
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

		gomega.Expect(report.PolicyCoverage.DecisionEvents).To(gomega.Equal(1), "expected joined policy coverage, got %#v", report.PolicyCoverage)
		gomega.Expect(report.PolicyCoverage.DecisionCounts[string(PolicyAllow)]).To(gomega.Equal(1), "expected joined policy coverage, got %#v", report.PolicyCoverage)
		gomega.Expect(report.PolicyCoverage.ToolCounts["apply_patch"]).To(gomega.Equal(1), "expected joined policy coverage, got %#v", report.PolicyCoverage)
		gomega.Expect(!slices.Contains(report.PolicyCoverage.ContractsWithPolicy, contract.ContractID)).To(gomega.BeFalse(), "expected contract policy coverage for %q, got %#v", contract.ContractID, report.PolicyCoverage.ContractsWithPolicy)

		gomega.Expect(report.ObligationCoverage.Opened).To(gomega.Equal(1), "expected resolved obligation coverage, got %#v", report.ObligationCoverage)
		gomega.Expect(report.ObligationCoverage.Resolved).To(gomega.Equal(1), "expected resolved obligation coverage, got %#v", report.ObligationCoverage)
		gomega.Expect(report.ObligationCoverage.UnresolvedObligations).To(gomega.BeEmpty(), "expected resolved obligation coverage, got %#v", report.ObligationCoverage)
		gomega.Expect(!slices.Contains(report.ObligationCoverage.ContractsWithObligations, contract.ContractID)).To(gomega.BeFalse(), "expected contract obligation coverage for %q, got %#v", contract.ContractID, report.ObligationCoverage.ContractsWithObligations)

		gomega.Expect(report.Contracts).To(gomega.HaveLen(1), "expected per-contract joined coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].PolicyTools["apply_patch"]).To(gomega.Equal(1), "expected per-contract joined coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].ObligationStatusCounts[string(ObligationResolved)]).To(gomega.Equal(1), "expected per-contract joined coverage, got %#v", report.Contracts)

		gomega.Expect(report.AllClear).To(gomega.BeTrue(), "expected all-clear joined coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
		gomega.Expect(report.Findings).To(gomega.BeEmpty(), "expected all-clear joined coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	})

	ginkgo.It("groups events by contract, invariant, lane, phase, adapter, and terminal state", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 18, 35, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
		policy := mustAutonomyEvent(NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
			ToolCallID:  "tool-apply-1",
			ToolName:    "apply_patch",
			Capability:  CapabilityApplyPatch,
			ActionClass: ActionClassPerEditWrite,
			SideEffect:  SideEffectAdditive,
			Decision:    PolicyAllow,
			InvariantID: "write.required_edit_paths",
		}, occurredAt.Add(time.Second), 2))
		gate := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:             GateVerification,
			Decision:         PolicyReject,
			PackID:           ContractPackVerificationPostWrite,
			EnforcementMode:  EnforcementModeObserveOnly,
			InvariantID:      "verification.post_write",
			ReasonCode:       "verification_required_after_write",
			ToolName:         "go test ./...",
			Source:           "audit_test",
			ObservedFacts:    []ContractObservedFact{"verification_gate_decision"},
			SafeAlternatives: []string{"go test ./..."},
		},

			occurredAt.Add(2*time.Second), 3))
		friction := mustAutonomyEvent(NewGuardFrictionEvent(contract, ContractGuardFrictionEvent{
			Step:                 3,
			Lane:                 "implement",
			Phase:                "execute",
			PhaseID:              "implement.execute",
			ProtocolHash:         "protocol-test",
			InvariantID:          "completion.readiness",
			Subject:              GuardFrictionSubjectFinalAnswer,
			Decision:             GuardFrictionDecisionReject,
			GuardSurface:         GuardFrictionSurfaceCompletionGate,
			FirstInvalidAction:   "final_answer",
			VisibleActionClasses: []string{"final_answer", "blocker"},
			SafeAlternatives:     []string{"go test ./..."},
			ModelContractVisible: true,
			RetryOutcome:         GuardFrictionRetryNotApplicable,
			Avoidability:         GuardFrictionAvoidabilityUnknown,
		}, occurredAt.Add(3*time.Second), 4))
		turnPhase := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
			Reason: "turn_phase_contract",
			TurnPhaseContract: &TurnPhaseContractEvent{
				ContractID:   contract.ContractID,
				PhaseID:      "implement.execute",
				Phase:        "execute",
				InvariantIDs: []string{"lifecycle.closeout"},
				TypedRoute: TypedRouteContractEvent{
					RouteSource: "lane",
				},
				LifecycleState: LifecycleStateEvent{
					TerminalStates: []string{"complete"},
				},
			},
		}, occurredAt.Add(4*time.Second), 5))
		modelExchange := mustAutonomyEvent(NewModelExchangeEvent(contract, ContractModelExchangeEvent{
			Kind:         "permission_snapshot",
			ContractID:   contract.ContractID,
			ProtocolHash: "protocol-test",
			PhaseID:      "implement.execute",
			ModelCallID:  "model-call-1",
			InvariantID:  "tool.authorization",
			PermissionSnapshot: &PermissionSnapshot{
				ContractID:   contract.ContractID,
				ProtocolHash: "protocol-test",
				CapturedBy:   "harness",
			},
		}, occurredAt.Add(5*time.Second), 6))

		report := AuditEvents([]Event{compiled, policy, gate, friction, turnPhase, modelExchange}, EventAuditOptions{})

		gomega.Expect(report.AuditGroupCoverage.Total < 5).To(gomega.BeFalse(), "expected grouped audit coverage, got %#v", report.AuditGroupCoverage)

		assertAuditGroupExists(report.AuditGroupCoverage.Groups, AuditGroup{
			ContractID:    contract.ContractID,
			InvariantID:   "write.required_edit_paths",
			Lane:          "implement",
			Phase:         "execute",
			AdapterRoute:  "apply_patch",
			TerminalState: "unknown",
		})

		assertAuditGroupExists(report.AuditGroupCoverage.Groups, AuditGroup{
			ContractID:    contract.ContractID,
			InvariantID:   "verification.post_write",
			Lane:          "implement",
			Phase:         "execute",
			AdapterRoute:  "go test ./...",
			TerminalState: string(PolicyReject),
		})

		assertAuditGroupExists(report.AuditGroupCoverage.Groups, AuditGroup{
			ContractID:    contract.ContractID,
			InvariantID:   "completion.readiness",
			Lane:          "implement",
			Phase:         "implement.execute",
			AdapterRoute:  string(GuardFrictionSurfaceCompletionGate),
			TerminalState: string(GuardFrictionDecisionReject),
		})

		assertAuditGroupExists(report.AuditGroupCoverage.Groups, AuditGroup{
			ContractID:    contract.ContractID,
			InvariantID:   "lifecycle.closeout",
			Lane:          "implement",
			Phase:         "implement.execute",
			AdapterRoute:  "unknown",
			TerminalState: "complete",
		})

		assertAuditGroupExists(report.AuditGroupCoverage.Groups, AuditGroup{
			ContractID:    contract.ContractID,
			InvariantID:   "tool.authorization",
			Lane:          "implement",
			Phase:         "implement.execute",
			AdapterRoute:  "permission_snapshot",
			TerminalState: "unknown",
		})

	})

	ginkgo.It("reports gate coverage and unresolved obligations", func() {
		contract := newEventTestPhaseContract()
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
		writeGate := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:            GateWrite,
			Decision:        PolicyReject,
			PackID:          ContractPackWriteRequiredPaths,
			EnforcementMode: EnforcementModeObserveOnly,
			InvariantID:     "write.required_edit_paths",
			ReasonCode:      "outside_required_edit_paths",
			ToolName:        "apply_patch",
			ObligationRefs:  []string{"obl-write-runner"},
			OpenObligations: []string{"obl-write-runner"},
			Source:          "audit_test",
			ObservedFacts:   []ContractObservedFact{"write_gate_decision"},
		},

			occurredAt.Add(3*time.Second), 4))
		verifyGate := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:               GateVerification,
			Decision:           PolicyReject,
			PackID:             ContractPackVerificationPostWrite,
			EnforcementMode:    EnforcementModeObserveOnly,
			InvariantID:        "verification.post_write",
			ReasonCode:         "verification_required_after_write",
			ToolName:           "read_file",
			VerificationStatus: "pending",
			ObligationRefs:     []string{"obl-verify"},
			OpenObligations:    []string{"obl-verify"},
			Source:             "audit_test",
			ObservedFacts:      []ContractObservedFact{"verification_gate_decision"},
		},

			occurredAt.Add(4*time.Second), 5))

		report := AuditEvents([]Event{compiled, writeOpened, verifyOpened, writeGate, verifyGate}, EventAuditOptions{})

		gomega.Expect(report.GateCoverage.DecisionEvents).To(gomega.Equal(2), "expected gate decision coverage, got %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.KindCounts[string(GateWrite)]).To(gomega.Equal(1), "expected gate decision coverage, got %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.KindCounts[string(GateVerification)]).To(gomega.Equal(1), "expected gate decision coverage, got %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.DecisionCounts[string(PolicyReject)]).To(gomega.Equal(2), "expected gate decision coverage, got %#v", report.GateCoverage)

		for _, want := range []string{"obl-write-runner", "obl-verify"} {
			gomega.Expect(!slices.Contains(report.GateCoverage.OpenObligationRefs, want)).To(gomega.BeFalse(), "gate coverage missing open obligation %q: %#v", want, report.GateCoverage.OpenObligationRefs)
		}
		gomega.Expect(!slices.Contains(report.GateCoverage.ContractsWithGateEvents, contract.ContractID)).To(gomega.BeFalse(), "expected contract gate coverage for %q, got %#v", contract.ContractID, report.GateCoverage.ContractsWithGateEvents)

		gomega.Expect(report.ObligationCoverage.UnresolvedObligations).To(gomega.HaveLen(2), "expected unresolved obligations to be explicit and all_clear=false, got all_clear=%v obligation=%#v", report.AllClear, report.ObligationCoverage)
		gomega.Expect(report.AllClear).To(gomega.BeFalse(), "expected unresolved obligations to be explicit and all_clear=false, got all_clear=%v obligation=%#v", report.AllClear, report.ObligationCoverage)

		gomega.Expect(len(report.LifecycleCoverage.LifecycleOnlyContracts) != 0).To(gomega.BeFalse(), "gate-bearing contract should not be lifecycle-only: %#v", report.LifecycleCoverage.LifecycleOnlyContracts)

		gomega.Expect(report.Contracts).To(gomega.HaveLen(1), "expected per-contract gate coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].GateKindCounts[string(GateWrite)]).To(gomega.Equal(1), "expected per-contract gate coverage, got %#v", report.Contracts)
	})

	ginkgo.It("counts readiness and completion coverage", func() {
		contract := newEventTestPhaseContract()
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

		gomega.Expect(report.ReadinessCoverage.Total).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.StateUpdatedEvents).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.ReadyToAnswerEvents).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.ReadyToWriteEvents).To(gomega.Equal(0), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.ReadyToVerifyEvents).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.EventsWithClosedClaims).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.EventsWithOpenObligations).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.EventsWithEvidenceRefs).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)
		gomega.Expect(report.ReadinessCoverage.EventsWithFurtherReadCriteria).To(gomega.Equal(1), "expected readiness coverage counts, got %#v", report.ReadinessCoverage)

		gomega.Expect(!slices.Contains(report.ReadinessCoverage.ContractsWithReadiness, contract.ContractID)).To(gomega.BeFalse(), "expected contract readiness coverage for %q, got %#v", contract.ContractID, report.ReadinessCoverage.ContractsWithReadiness)

		gomega.Expect(report.CompletionCoverage.Total).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.DecisionEvents).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.DecisionCounts[string(PolicyAllow)]).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.VerificationStatusCounts["passed"]).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.EventsWithProtocolHash).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.EventsMissingProtocolHash).To(gomega.Equal(0), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.EventsWithChangedPaths).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.EventsWithRequiredPathStatus).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)

		gomega.Expect(!slices.Contains(report.CompletionCoverage.ContractsWithCompletion, contract.ContractID)).To(gomega.BeFalse(), "expected contract completion coverage for %q, got %#v", contract.ContractID, report.CompletionCoverage.ContractsWithCompletion)

		gomega.Expect(report.Contracts).To(gomega.HaveLen(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].ReadinessEvents[string(EventReadinessStateUpdated)]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].ReadinessSignalCounts["ready_to_answer"]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].ReadinessSignalCounts["ready_to_verify"]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].ReadinessSignalCounts["closed_claims"]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].CompletionEvents[string(EventCompletionGateDecision)]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].CompletionDecisions[string(PolicyAllow)]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].CompletionVerifications["passed"]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].CompletionSignals["protocol_hash_present"]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].CompletionSignals["changed_paths"]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].CompletionSignals["required_path_status"]).To(gomega.Equal(1), "expected per-contract readiness/completion coverage, got %#v", report.Contracts)

		gomega.Expect(len(report.LifecycleCoverage.LifecycleOnlyContracts) != 0).To(gomega.BeFalse(), "readiness/completion-bearing contract should not be lifecycle-only: %#v", report.LifecycleCoverage.LifecycleOnlyContracts)

		gomega.Expect(report.AllClear).To(gomega.BeTrue(), "expected all-clear readiness/completion coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
		gomega.Expect(report.Findings).To(gomega.BeEmpty(), "expected all-clear readiness/completion coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	})

	ginkgo.It("flags completion allow with remaining obligations while keeping a missing protocol hash coverage-only", func() {
		contract := newEventTestPhaseContract()
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

		gomega.Expect(report.CompletionCoverage.DecisionEvents).To(gomega.Equal(2), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.DecisionCounts[string(PolicyAllow)]).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.DecisionCounts[string(PolicyReject)]).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.EventsWithRemainingObligations).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.EventsWithProtocolHash).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)
		gomega.Expect(report.CompletionCoverage.EventsMissingProtocolHash).To(gomega.Equal(1), "expected completion coverage counts, got %#v", report.CompletionCoverage)

		gomega.Expect(report.CompletionCoverage.AllowWithRemainingObligationsRefs).To(gomega.HaveLen(1), "expected allow-with-remaining-obligations ref, got %#v", report.CompletionCoverage.AllowWithRemainingObligationsRefs)
		gomega.Expect(report.CompletionCoverage.AllowWithRemainingObligationsRefs[0].EventID).To(gomega.Equal(allowWithRemaining.EventID), "expected allow-with-remaining-obligations ref, got %#v", report.CompletionCoverage.AllowWithRemainingObligationsRefs)

		gomega.Expect(report.CompletionCoverage.MissingProtocolHashRefs).To(gomega.HaveLen(1), "expected missing protocol hash ref, got %#v", report.CompletionCoverage.MissingProtocolHashRefs)
		gomega.Expect(report.CompletionCoverage.MissingProtocolHashRefs[0].EventID).To(gomega.Equal(missingProtocolHash.EventID), "expected missing protocol hash ref, got %#v", report.CompletionCoverage.MissingProtocolHashRefs)

		gomega.Expect(!hasEventAuditFinding(report, "contract_event_completion_allow_with_remaining_obligations")).To(gomega.BeFalse(), "expected completion allow finding, got %#v", report.Findings)

		gomega.Expect(hasEventAuditFinding(report, "contract_event_completion_missing_protocol_hash")).To(gomega.BeFalse(), "missing completion protocol hash should remain coverage-only, got %#v", report.Findings)

		gomega.Expect(report.AllClear).To(gomega.BeFalse(), "completion allow with remaining obligations should not be all-clear")
	})

	ginkgo.It("counts extended gate-decision fields when available", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 18, 47, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
		first := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:             GateCompletion,
			Decision:         PolicyReject,
			PackID:           ContractPackCompletionReadiness,
			EnforcementMode:  EnforcementModeEnforce,
			InvariantID:      "completion.readiness",
			ReasonCode:       "verification_required",
			Source:           "audit_test",
			ObservedFacts:    []ContractObservedFact{"completion_ready=false"},
			SafeAlternatives: []string{"<<QUESTION>>:"},
		},

			occurredAt.Add(time.Second), 2))
		second := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:            GateReadOnly,
			Decision:        PolicyObserve,
			PackID:          ContractPackCompletionReadiness,
			EnforcementMode: EnforcementModeObserveOnly,
			InvariantID:     "sidecar.read_only_profile",
			ReasonCode:      "read_only_contract",
			Source:          "audit_test",
		},

			occurredAt.Add(2*time.Second), 3))

		report := AuditEvents([]Event{compiled, first, second}, EventAuditOptions{})

		gomega.Expect(report.GateCoverage.DecisionCountsByPackID[string(ContractPackCompletionReadiness)]).To(gomega.Equal(2), "expected gate decisions counted by pack and enforcement mode, got %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.DecisionCountsByEnforcementMode["enforce"]).To(gomega.Equal(1), "expected gate decisions counted by pack and enforcement mode, got %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.DecisionCountsByEnforcementMode["observe_only"]).To(gomega.Equal(1), "expected gate decisions counted by pack and enforcement mode, got %#v", report.GateCoverage)

		gomega.Expect(report.GateCoverage.EventsWithObservedFacts).To(gomega.Equal(1), "expected observed_fact and safe_alternative event counts, got %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.EventsWithSafeAlternatives).To(gomega.Equal(1), "expected observed_fact and safe_alternative event counts, got %#v", report.GateCoverage)

		gomega.Expect(report.GateCoverage.MissingPackIDRefs).To(gomega.BeEmpty(), "complete extended fields should not be flagged: %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.MissingEnforcementModeRefs).To(gomega.BeEmpty(), "complete extended fields should not be flagged: %#v", report.GateCoverage)
		gomega.Expect(report.GateCoverage.MissingSafeAlternativesRefs).To(gomega.BeEmpty(), "complete extended fields should not be flagged: %#v", report.GateCoverage)
	})

	ginkgo.It("flags missing extended gate fields when available", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 18, 48, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
		missingPackAndAlternative := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:            GateCompletion,
			Decision:        PolicyReject,
			EnforcementMode: EnforcementModeEnforce,
			InvariantID:     "completion.readiness",
			ReasonCode:      "verification_required",
			Source:          "audit_test",
			ObservedFacts:   []ContractObservedFact{"completion_ready=false"},
		},

			occurredAt.Add(time.Second), 2))
		missingMode := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:        GateWrite,
			Decision:    PolicyAllow,
			PackID:      ContractPackWriteRequiredPaths,
			InvariantID: "write.scope",
			ReasonCode:  "in_scope",
			Source:      "audit_test",
		},

			occurredAt.Add(2*time.Second), 3))

		report := AuditEvents([]Event{compiled, missingPackAndAlternative, missingMode}, EventAuditOptions{})

		gomega.Expect(report.GateCoverage.MissingPackIDRefs).To(gomega.HaveLen(1), "expected missing pack_id ref, got %#v", report.GateCoverage.MissingPackIDRefs)
		gomega.Expect(report.GateCoverage.MissingPackIDRefs[0].Reference).To(gomega.Equal("pack_id"), "expected missing pack_id ref, got %#v", report.GateCoverage.MissingPackIDRefs)

		gomega.Expect(report.GateCoverage.MissingEnforcementModeRefs).To(gomega.HaveLen(1), "expected missing enforcement_mode ref, got %#v", report.GateCoverage.MissingEnforcementModeRefs)
		gomega.Expect(report.GateCoverage.MissingEnforcementModeRefs[0].Reference).To(gomega.Equal("enforcement_mode"), "expected missing enforcement_mode ref, got %#v", report.GateCoverage.MissingEnforcementModeRefs)

		gomega.Expect(report.GateCoverage.MissingSafeAlternativesRefs).To(gomega.HaveLen(1), "expected missing safe_alternatives ref, got %#v", report.GateCoverage.MissingSafeAlternativesRefs)
		gomega.Expect(report.GateCoverage.MissingSafeAlternativesRefs[0].Reference).To(gomega.Equal("safe_alternatives"), "expected missing safe_alternatives ref, got %#v", report.GateCoverage.MissingSafeAlternativesRefs)

		for _, want := range []string{
			"contract_event_gate_missing_pack_id",
			"contract_event_gate_missing_enforcement_mode",
			"contract_event_gate_missing_safe_alternatives",
		} {
			gomega.Expect(!hasEventAuditFinding(report, want)).To(gomega.BeFalse(), "expected finding %q, got %#v", want, report.Findings)
		}
	})

	ginkgo.It("flags gate obligation references that were not open at the gate sequence", func() {
		contract := newEventTestPhaseContract()
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
			PackID:          ContractPackCompletionReadiness,
			EnforcementMode: EnforcementModeObserveOnly,
			InvariantID:     "sidecar.read_only_profile",
			ReasonCode:      "read_only_contract",
			ToolName:        "context_pack",
			ObligationRefs:  []string{"obl-evidence"},
			OpenObligations: []string{"obl-evidence"},
			Source:          "audit_test",
			ObservedFacts:   []ContractObservedFact{"sidecar_read_only_gate"},
		},

			occurredAt.Add(3*time.Second), 4))

		report := AuditEvents([]Event{compiled, opened, resolved, gate}, EventAuditOptions{})

		gomega.Expect(len(report.ObligationCoverage.UnresolvedObligations) != 0).To(gomega.BeFalse(), "obligation should be resolved before the gate, got %#v", report.ObligationCoverage)

		gomega.Expect(report.GateCoverage.InconsistentOpenObligationRefs).To(gomega.HaveLen(1), "expected inconsistent gate open-obligation ref, got %#v", report.GateCoverage.InconsistentOpenObligationRefs)
		gomega.Expect(report.GateCoverage.InconsistentOpenObligationRefs[0].Reference).To(gomega.Equal("obl-evidence"), "expected inconsistent gate open-obligation ref, got %#v", report.GateCoverage.InconsistentOpenObligationRefs)

		gomega.Expect(!hasEventAuditFinding(report, "contract_event_gate_open_obligation_inconsistent")).To(gomega.BeFalse(), "expected gate inconsistency finding, got %#v", report.Findings)

		gomega.Expect(report.AllClear).To(gomega.BeFalse(), "inconsistent gate open-obligation refs should not be all-clear")
	})

	ginkgo.It("groups guard friction without failing all-clear", func() {
		contract := newEventTestPhaseContract()
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
		gomega.Expect(coverage.Total).To(gomega.Equal(2), "expected guard friction totals, got %#v", coverage)
		gomega.Expect(coverage.AvoidableFriction).To(gomega.Equal(1), "expected guard friction totals, got %#v", coverage)
		gomega.Expect(coverage.UnclassifiedFriction).To(gomega.Equal(1), "expected guard friction totals, got %#v", coverage)

		gomega.Expect(coverage.LaneCounts["implement"]).To(gomega.Equal(2), "expected lane/phase friction grouping, got %#v", coverage)
		gomega.Expect(coverage.PhaseCounts["planner.submit_ready"]).To(gomega.Equal(2), "expected lane/phase friction grouping, got %#v", coverage)

		gomega.Expect(report.Contracts).To(gomega.HaveLen(1), "expected contract guard friction grouping, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].GuardFrictionCounts[string(EventGuardFriction)]).To(gomega.Equal(2), "expected contract guard friction grouping, got %#v", report.Contracts)

		gomega.Expect(coverage.AvoidabilityCounts[string(GuardFrictionAvoidabilityUnknown)]).To(gomega.Equal(1), "expected avoidability grouping, got %#v", coverage.AvoidabilityCounts)
		gomega.Expect(coverage.AvoidabilityCounts[string(GuardFrictionAvoidabilityToolSurfaceGap)]).To(gomega.Equal(1), "expected avoidability grouping, got %#v", coverage.AvoidabilityCounts)

		gomega.Expect(coverage.FirstInvalidActionCounts["search_content"]).To(gomega.Equal(1), "expected action/retry/surface grouping, got %#v", coverage)
		gomega.Expect(coverage.RetryOutcomeCounts[string(GuardFrictionRetrySuccess)]).To(gomega.Equal(1), "expected action/retry/surface grouping, got %#v", coverage)
		gomega.Expect(coverage.GuardSurfaceCounts[string(GuardFrictionSurfaceCompletionGate)]).To(gomega.Equal(1), "expected action/retry/surface grouping, got %#v", coverage)

		gomega.Expect(coverage.SafeAlternativeCoverage.EventsWithSafeAlternatives).To(gomega.Equal(1), "expected safe alternative coverage, got %#v", coverage.SafeAlternativeCoverage)
		gomega.Expect(coverage.SafeAlternativeCoverage.EventsMissingSafeAlternatives).To(gomega.Equal(1), "expected safe alternative coverage, got %#v", coverage.SafeAlternativeCoverage)
		gomega.Expect(coverage.SafeAlternativeCoverage.SafeAlternativeCounts["submit_plan"]).To(gomega.Equal(1), "expected safe alternative coverage, got %#v", coverage.SafeAlternativeCoverage)
		gomega.Expect(coverage.SafeAlternativeCoverage.VisibleActionClassCounts["source_satisfied"]).To(gomega.Equal(2), "expected safe alternative coverage, got %#v", coverage.SafeAlternativeCoverage)
		gomega.Expect(coverage.SafeAlternativeCoverage.VisibleToolCounts["submit_plan"]).To(gomega.Equal(1), "expected safe alternative coverage, got %#v", coverage.SafeAlternativeCoverage)

		gomega.Expect(coverage.TopContracts).To(gomega.HaveLen(1), "expected top contract friction grouping, got %#v", coverage.TopContracts)
		gomega.Expect(coverage.TopContracts[0].ContractID).To(gomega.Equal(contract.ContractID), "expected top contract friction grouping, got %#v", coverage.TopContracts)
		gomega.Expect(coverage.TopContracts[0].AvoidableFriction).To(gomega.Equal(1), "expected top contract friction grouping, got %#v", coverage.TopContracts)
		gomega.Expect(coverage.TopContracts[0].UnclassifiedFriction).To(gomega.Equal(1), "expected top contract friction grouping, got %#v", coverage.TopContracts)

		gomega.Expect(coverage.UnclassifiedRefs).To(gomega.HaveLen(1), "expected unclassified friction ref for unknown avoidability, got %#v", coverage.UnclassifiedRefs)
		gomega.Expect(coverage.UnclassifiedRefs[0].EventID).To(gomega.Equal(first.EventID), "expected unclassified friction ref for unknown avoidability, got %#v", coverage.UnclassifiedRefs)

		gomega.Expect(report.AllClear).To(gomega.BeTrue(), "guard friction activity should not fail all_clear, all_clear=%v findings=%#v", report.AllClear, report.Findings)
		gomega.Expect(report.Findings).To(gomega.BeEmpty(), "guard friction activity should not fail all_clear, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	})

	ginkgo.It("reports model-exchange rollback coverage", func() {
		contract := newEventTestPhaseContract()
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
		gomega.Expect(coverage.Total).To(gomega.Equal(2), "expected model-exchange rollback coverage, got %#v", coverage)
		gomega.Expect(coverage.TracePolicyEvents).To(gomega.Equal(1), "expected model-exchange rollback coverage, got %#v", coverage)
		gomega.Expect(coverage.RollbackControlEvents).To(gomega.Equal(1), "expected model-exchange rollback coverage, got %#v", coverage)
		gomega.Expect(coverage.RollbackActive).To(gomega.BeTrue(), "expected model-exchange rollback coverage, got %#v", coverage)
		gomega.Expect(report.RollbackActive).To(gomega.BeTrue(), "expected model-exchange rollback coverage, got %#v", coverage)

		gomega.Expect(coverage.KindCounts["trace_store_policy"]).To(gomega.Equal(1), "expected model-exchange kind/payload counts, got %#v", coverage)
		gomega.Expect(coverage.KindCounts["rollback_control"]).To(gomega.Equal(1), "expected model-exchange kind/payload counts, got %#v", coverage)
		gomega.Expect(coverage.PayloadCounts["trace_store_policy"]).To(gomega.Equal(1), "expected model-exchange kind/payload counts, got %#v", coverage)
		gomega.Expect(coverage.PayloadCounts["rollback_control"]).To(gomega.Equal(1), "expected model-exchange kind/payload counts, got %#v", coverage)

		gomega.Expect(coverage.RollbackRefs).To(gomega.HaveLen(1), "expected rollback ref, got %#v", coverage.RollbackRefs)
		gomega.Expect(coverage.RollbackRefs[0].Reference).To(gomega.Equal("model_exchange_disabled"), "expected rollback ref, got %#v", coverage.RollbackRefs)

		gomega.Expect(coverage.RedactionStatusCounts["pass"]).To(gomega.Equal(1), "expected redaction status coverage, audit=%#v privacy=%#v", coverage.RedactionStatusCounts, report.PrivacyChecks.RedactionStatusCounts)
		gomega.Expect(report.PrivacyChecks.RedactionStatusCounts["pass"]).To(gomega.Equal(1), "expected redaction status coverage, audit=%#v privacy=%#v", coverage.RedactionStatusCounts, report.PrivacyChecks.RedactionStatusCounts)

		gomega.Expect(report.Contracts).To(gomega.HaveLen(1), "expected per-contract model-exchange counts, got %#v", report.Contracts)
		gomega.Expect(report.Contracts[0].ModelExchangeCounts[string(EventModelExchange)]).To(gomega.Equal(2), "expected per-contract model-exchange counts, got %#v", report.Contracts)
		gomega.Expect(!slices.Contains(report.ModelExchangeCoverage.ContractsWithModelExchange, contract.ContractID)).To(gomega.BeFalse(), "expected contract model-exchange coverage for %q, got %#v", contract.ContractID, report.ModelExchangeCoverage.ContractsWithModelExchange)
	})

	ginkgo.It("orders repeated contract runs by occurrence before sequence", func() {
		contract := newEventTestPhaseContract()
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
			PackID:          ContractPackCompletionReadiness,
			EnforcementMode: EnforcementModeObserveOnly,
			InvariantID:     "sidecar.read_only_profile",
			ReasonCode:      "read_only_contract",
			ToolName:        "context_pack",
			ObligationRefs:  []string{"obl-evidence"},
			OpenObligations: []string{"obl-evidence"},
			Source:          "audit_test",
			ObservedFacts:   []ContractObservedFact{"second_run_gate"},
		},

			secondRun.Add(2*time.Second), 4))
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

		gomega.Expect(len(report.GateCoverage.InconsistentOpenObligationRefs) != 0).To(gomega.BeFalse(), "restarted sequence numbers from a later run should not make open refs inconsistent: %#v", report.GateCoverage.InconsistentOpenObligationRefs)

		gomega.Expect(len(report.ObligationCoverage.UnmatchedObligations) != 0).To(gomega.BeFalse(), "restarted sequence numbers from a later run should not make resolutions unmatched: %#v", report.ObligationCoverage.UnmatchedObligations)

		gomega.Expect(report.ObligationCoverage.UnresolvedObligations).To(gomega.BeEmpty(), "expected all-clear repeated-run audit, all_clear=%v obligations=%#v findings=%#v", report.AllClear, report.ObligationCoverage, report.Findings)
		gomega.Expect(report.AllClear).To(gomega.BeTrue(), "expected all-clear repeated-run audit, all_clear=%v obligations=%#v findings=%#v", report.AllClear, report.ObligationCoverage, report.Findings)
	})

	ginkgo.It("reports a missing contract ID", func() {
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

		gomega.Expect(len(report.ReferenceCoverage.MissingContractIDEvents) != 1).To(gomega.BeFalse(), "expected one missing contract id event, got %#v", report.ReferenceCoverage)

		gomega.Expect(!hasEventAuditFinding(report, "contract_event_missing_contract_id")).To(gomega.BeFalse(), "expected missing-contract finding, got %#v", report.Findings)

		gomega.Expect(report.AllClear).To(gomega.BeFalse(), "missing contract id should not be all clear")
	})

	ginkgo.It("reports stale and unmatched contract references", func() {
		contract := newEventTestPhaseContract()
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

		gomega.Expect(report.ReferenceCoverage.StaleContractReferences).To(gomega.HaveLen(1), "expected stale contract reference, got %#v", report.ReferenceCoverage.StaleContractReferences)
		gomega.Expect(report.ReferenceCoverage.StaleContractReferences[0].EventID).To(gomega.Equal("event-after-stop"), "expected stale contract reference, got %#v", report.ReferenceCoverage.StaleContractReferences)

		gomega.Expect(report.ReferenceCoverage.UnmatchedContractReferences).To(gomega.HaveLen(1), "expected unmatched contract reference, got %#v", report.ReferenceCoverage.UnmatchedContractReferences)
		gomega.Expect(report.ReferenceCoverage.UnmatchedContractReferences[0].Reference).To(gomega.Equal("contract_unknown"), "expected unmatched contract reference, got %#v", report.ReferenceCoverage.UnmatchedContractReferences)

		for _, want := range []string{"contract_event_stale_contract_reference", "contract_event_unmatched_contract_reference"} {
			gomega.Expect(!hasEventAuditFinding(report, want)).To(gomega.BeFalse(), "expected finding %q, got %#v", want, report.Findings)
		}
	})

	ginkgo.It("reports unmatched sidecar tool-call correlation", func() {
		contract := newEventTestReadOnlySidecarContract()
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

		gomega.Expect(report.SidecarToolCorrelation.SidecarEventCount).NotTo(gomega.Equal(0), "expected sidecar and tool-call coverage, got %#v", report.SidecarToolCorrelation)
		gomega.Expect(report.SidecarToolCorrelation.ToolCallEventCount).NotTo(gomega.Equal(0), "expected sidecar and tool-call coverage, got %#v", report.SidecarToolCorrelation)

		gomega.Expect(report.SidecarToolCorrelation.UnmatchedCorrelations).To(gomega.HaveLen(1), "expected sidecar/tool mismatch, got %#v", report.SidecarToolCorrelation.UnmatchedCorrelations)
		gomega.Expect(report.SidecarToolCorrelation.UnmatchedCorrelations[0].Reason).To(gomega.ContainSubstring("sidecar_tool"), "expected sidecar/tool mismatch, got %#v", report.SidecarToolCorrelation.UnmatchedCorrelations)

		gomega.Expect(!hasEventAuditFinding(report, "contract_event_sidecar_tool_correlation_mismatch")).To(gomega.BeFalse(), "expected sidecar correlation finding, got %#v", report.Findings)
	})

	ginkgo.It("flags a turn-phase contradictory tool action", func() {
		contract := newEventTestPhaseContract()
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
		readPolicy := mustAutonomyEvent(NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
			ToolName:    "read_file",
			ActionClass: ActionClassRead,
			Capability:  CapabilityBoundedRead,
			Decision:    PolicyReject,
			ReasonCode:  "tool_surface",
		}, occurredAt.Add(2*time.Second), 3))

		report := AuditEvents([]Event{compiled, presented, readPolicy}, EventAuditOptions{})

		gomega.Expect(report.TurnPhaseCoverage.Total).To(gomega.Equal(1), "expected turn phase coverage, got %#v", report.TurnPhaseCoverage)
		gomega.Expect(report.TurnPhaseCoverage.PhaseCounts["plan_execute.submit_ready.textual"]).To(gomega.Equal(1), "expected turn phase coverage, got %#v", report.TurnPhaseCoverage)

		gomega.Expect(report.TurnPhaseCoverage.Contradictions).To(gomega.HaveLen(1), "expected read action contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
		gomega.Expect(report.TurnPhaseCoverage.Contradictions[0].ToolName).To(gomega.Equal("read_file"), "expected read action contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
		gomega.Expect(report.TurnPhaseCoverage.Contradictions[0].ActionClass).To(gomega.Equal("read"), "expected read action contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)

		gomega.Expect(hasEventAuditFinding(report, "turn_phase_contract_contradiction")).To(gomega.BeTrue(), "expected contradiction finding and all_clear=false, all_clear=%v findings=%#v", report.AllClear, report.Findings)
		gomega.Expect(report.AllClear).To(gomega.BeFalse(), "expected contradiction finding and all_clear=false, all_clear=%v findings=%#v", report.AllClear, report.Findings)

		finding := eventAuditFindingByID(report, "turn_phase_contract_contradiction")
		gomega.Expect(finding).NotTo(gomega.BeNil(), "expected contradiction finding details, got %#v", finding)
		gomega.Expect(finding.DetailCount).To(gomega.Equal(1), "expected contradiction finding details, got %#v", finding)
		gomega.Expect(finding.Details).To(gomega.HaveLen(1), "expected contradiction finding details, got %#v", finding)
		detail := finding.Details[0]
		gomega.Expect(detail.ToolName).To(gomega.Equal("read_file"), "unexpected contradiction detail: %#v", detail)
		gomega.Expect(detail.ActionClass).To(gomega.Equal("read"), "unexpected contradiction detail: %#v", detail)
		gomega.Expect(detail.PhaseID).To(gomega.Equal("plan_execute.submit_ready.textual"), "unexpected contradiction detail: %#v", detail)
	})

	ginkgo.It("allows textual callable tools when their action is expressible", func() {
		contract := newEventTestPhaseContract()
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

		gomega.Expect(len(report.TurnPhaseCoverage.Contradictions) != 0).To(gomega.BeFalse(), "textual callable tools should reconcile when their action class is expressible, got %#v", report.TurnPhaseCoverage.Contradictions)

		gomega.Expect(hasEventAuditFinding(report, "turn_phase_contract_contradiction")).To(gomega.BeFalse(), "unexpected contradiction finding for textual callable tools: %#v", report.Findings)
	})

	ginkgo.It("allows a textual read tool when read is expressible", func() {
		contract := newEventTestPhaseContract()
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

		gomega.Expect(len(report.TurnPhaseCoverage.Contradictions) != 0).To(gomega.BeFalse(), "textual read tool should reconcile through its action class, got %#v", report.TurnPhaseCoverage.Contradictions)

		gomega.Expect(hasEventAuditFinding(report, "turn_phase_contract_contradiction")).To(gomega.BeFalse(), "unexpected contradiction finding for textual read tool: %#v", report.Findings)
	})

	ginkgo.It("uses callable tools to determine textual manifest visibility", func() {
		contract := newEventTestPhaseContract()
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

		gomega.Expect(len(report.TurnPhaseCoverage.Contradictions) != 0).To(gomega.BeFalse(), "hidden/base-only task should not be audited as manifest-visible, got %#v", report.TurnPhaseCoverage.Contradictions)

		gomega.Expect(hasEventAuditFinding(report, "turn_phase_contract_contradiction")).To(gomega.BeFalse(), "unexpected contradiction finding for hidden/base-only task: %#v", report.Findings)
	})

	ginkgo.It("flags readiness-forcing incidents", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 20, 24, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
		readiness := mustAutonomyEvent(NewReadinessStateEvent(contract, ContractReadinessEvent{
			ReadyToAnswer:   true,
			OpenObligations: []string{"source-body:internal/runtime/agent/runner.go"},
		}, occurredAt.Add(time.Second), 2))
		presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
			Reason: "turn_phase_contract",
			TurnPhaseContract: &TurnPhaseContractEvent{
				SchemaVersion: "idiom.turn_phase_contract.v1",
				PhaseID:       "read_only.ready_but_source_pending",
				HiddenTools:   []string{"read_file", "context_pack"},
				ActionPolicy: TurnActionClassPolicyEvent{
					AllowedActionClasses: []string{"final_answer", "blocker"},
				},
				EvidenceState: PhaseEvidenceStateEvent{
					SourceBodyObligations: []string{"internal/runtime/agent/runner.go"},
				},
				EnforcementMode: "observe_only",
			},
		}, occurredAt.Add(2*time.Second), 3))
		modelProbe := mustAutonomyEvent(NewModelExchangeEvent(contract, ContractModelExchangeEvent{
			Kind:         "model_response",
			ContractID:   contract.ContractID,
			ProtocolHash: "sha256:phase",
			PhaseID:      "read_only.ready_but_source_pending",
			ModelResponse: &ModelResponse{
				SchemaVersion: ModelResponseSchemaVersion,
				ContractID:    contract.ContractID,
				ProtocolHash:  "sha256:phase",
				Kind:          ModelResponseRequestContext,
				ContextRequest: &ContextRequest{
					MissingRefs: []string{"source_body:internal/runtime/agent/runner.go"},
					ReasonCode:  "missing_source_body",
				},
			},
		}, occurredAt.Add(3*time.Second), 4))
		completion := mustAutonomyEvent(NewCompletionGateDecisionEvent(contract, ContractCompletionEvent{
			Decision:     PolicyAllow,
			InvariantID:  "completion.readiness",
			ProtocolHash: "sha256:phase",
		}, occurredAt.Add(4*time.Second), 5))

		report := AuditEvents([]Event{compiled, readiness, presented, modelProbe, completion}, EventAuditOptions{})

		gomega.Expect(len(report.ReadinessForcing.ForcedNoToolsTurns) != 1).To(gomega.BeFalse(), "expected forced no-tools readiness incident, got %#v", report.ReadinessForcing)
		gomega.Expect(len(report.ReadinessForcing.UsefulToolsHiddenAfterReadiness) != 1).To(gomega.BeFalse(), "expected hidden useful tools readiness incident, got %#v", report.ReadinessForcing)
		gomega.Expect(len(report.ReadinessForcing.HostComposedAfterRelevantProbes) != 1).To(gomega.BeFalse(), "expected host-composed-after-probe incident, got %#v", report.ReadinessForcing)

		for _, id := range []string{
			"readiness_forced_no_tools_turn",
			"readiness_useful_tools_hidden",
			"readiness_host_composed_after_probe",
		} {
			gomega.Expect(!hasEventAuditFinding(report, id)).To(gomega.BeFalse(), "expected finding %s, got %#v", id, report.Findings)
		}
	})

	ginkgo.It("clears readiness open-obligation state after resolution", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 20, 25, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
		opened := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "obl-source",
			Kind:         ObligationEvidence,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "source_body",
		}, occurredAt.Add(time.Second), 2))
		readiness := mustAutonomyEvent(NewReadinessStateEvent(contract, ContractReadinessEvent{
			ReadyToAnswer:   true,
			OpenObligations: []string{"obl-source"},
		}, occurredAt.Add(2*time.Second), 3))
		resolved := mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
			ObligationID: "obl-source",
			Kind:         ObligationEvidence,
			Status:       ObligationResolved,
			Owner:        OwnerHost,
			RequiredBy:   "source_body",
			EvidenceRef:  "read-file:runner.go",
		}, occurredAt.Add(3*time.Second), 4))
		presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
			Reason: "turn_phase_contract",
			TurnPhaseContract: &TurnPhaseContractEvent{
				SchemaVersion: "idiom.turn_phase_contract.v1",
				PhaseID:       "read_only.after_resolution",
				HiddenTools:   []string{"read_file"},
				EvidenceState: PhaseEvidenceStateEvent{
					SourceBodyObligations: []string{"internal/runtime/agent/runner.go"},
				},
				EnforcementMode: "observe_only",
			},
		}, occurredAt.Add(4*time.Second), 5))

		report := AuditEvents([]Event{compiled, opened, readiness, resolved, presented}, EventAuditOptions{})

		gomega.Expect(len(report.ReadinessForcing.UsefulToolsHiddenAfterReadiness) != 0).To(gomega.BeFalse(), "resolved obligation should clear readiness forcing state, got %#v", report.ReadinessForcing)
	})

	ginkgo.It("clears a relevant probe after a context packet", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 20, 26, 0, 0, time.UTC)
		compiled := mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1))
		modelProbe := mustAutonomyEvent(NewModelExchangeEvent(contract, ContractModelExchangeEvent{
			Kind:         "model_response",
			ContractID:   contract.ContractID,
			ProtocolHash: "sha256:phase",
			PhaseID:      "read_only.context_missing",
			ModelResponse: &ModelResponse{
				SchemaVersion: ModelResponseSchemaVersion,
				ContractID:    contract.ContractID,
				ProtocolHash:  "sha256:phase",
				Kind:          ModelResponseRequestContext,
				ContextRequest: &ContextRequest{
					MissingRefs: []string{"source_body:internal/runtime/agent/runner.go"},
					ReasonCode:  "missing_source_body",
				},
			},
		}, occurredAt.Add(time.Second), 2))
		contextPacket := mustAutonomyEvent(NewModelExchangeEvent(contract, ContractModelExchangeEvent{
			Kind:         "context_packet",
			ContractID:   contract.ContractID,
			ProtocolHash: "sha256:phase",
			PhaseID:      "read_only.context_satisfied",
			ContextPacket: &ContextPacket{
				SchemaVersion: ContextPacketSchemaVersion,
				ContractID:    contract.ContractID,
				ProtocolHash:  "sha256:phase",
				EvidenceRefs:  []string{"read-file:runner.go"},
				SchemaNames:   []string{ContextPacketSchemaVersion},
			},
		}, occurredAt.Add(2*time.Second), 3))
		completion := mustAutonomyEvent(NewCompletionGateDecisionEvent(contract, ContractCompletionEvent{
			Decision:     PolicyAllow,
			InvariantID:  "completion.readiness",
			ProtocolHash: "sha256:phase",
		}, occurredAt.Add(3*time.Second), 4))

		report := AuditEvents([]Event{compiled, modelProbe, contextPacket, completion}, EventAuditOptions{})

		gomega.Expect(len(report.ReadinessForcing.HostComposedAfterRelevantProbes) != 0).To(gomega.BeFalse(), "context packet should clear relevant-probe forcing state, got %#v", report.ReadinessForcing)
	})

	ginkgo.It("flags a tool action when no manifest tools are allowed", func() {
		contract := newEventTestPhaseContract()
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
		policy := mustAutonomyEvent(NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
			ToolName:    "read_file",
			ActionClass: ActionClassRead,
			Capability:  CapabilityBoundedRead,
			Decision:    PolicyReject,
		}, occurredAt.Add(2*time.Second), 3))

		report := AuditEvents([]Event{compiled, presented, policy}, EventAuditOptions{})

		gomega.Expect(report.TurnPhaseCoverage.Contradictions).To(gomega.HaveLen(1), "expected no-manifest tool contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
		gomega.Expect(report.TurnPhaseCoverage.Contradictions[0].ToolName).To(gomega.Equal("read_file"), "expected no-manifest tool contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
	})

	ginkgo.It("reconciles a turn-phase surface summary", func() {
		contract := newEventTestPhaseContract()
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

		gomega.Expect(report.TurnPhaseCoverage.SurfaceCounts["next_action_state"] != 1).To(gomega.BeFalse(), "expected next_action_state surface coverage, got %#v", report.TurnPhaseCoverage.SurfaceCounts)

		gomega.Expect(len(report.TurnPhaseCoverage.Contradictions) == 0).To(gomega.BeFalse(), "expected surface contradictions, got none")

		var sawInvalidRead bool

		for _, issue := range report.TurnPhaseCoverage.Contradictions {
			gomega.Expect(issue.SurfacePair).To(gomega.Equal("turn_phase_contract,next_action_state"), "expected surface-pair attribution, got %#v", issue)
			gomega.Expect(issue.SurfaceID).To(gomega.Equal("next_action_state"), "expected surface-pair attribution, got %#v", issue)

			if issue.InvariantID == "turn_phase.action.invalid_advertised" && issue.ActionClass == "read" {
				sawInvalidRead = true
			}
		}

		gomega.Expect(!sawInvalidRead).To(gomega.BeFalse(), "expected invalid read contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)

		gomega.Expect(hasEventAuditFinding(report, "turn_phase_contract_contradiction")).To(gomega.BeTrue(), "expected contradiction finding and all_clear=false, all_clear=%v findings=%#v", report.AllClear, report.Findings)
		gomega.Expect(report.AllClear).To(gomega.BeFalse(), "expected contradiction finding and all_clear=false, all_clear=%v findings=%#v", report.AllClear, report.Findings)
	})

	ginkgo.It("reconciles the model-protocol surface", func() {
		contract := newEventTestPhaseContract()
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
				SchemaVersion:          ModelProtocolProjectionSchemaVersion,
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

		gomega.Expect(report.TurnPhaseCoverage.SurfaceCounts["model_protocol"] != 1).To(gomega.BeFalse(), "expected model_protocol surface coverage, got %#v", report.TurnPhaseCoverage.SurfaceCounts)

		gomega.Expect(len(report.TurnPhaseCoverage.Contradictions) != 1).To(gomega.BeFalse(), "expected one model-protocol contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)

		issue := report.TurnPhaseCoverage.Contradictions[0]
		gomega.Expect(issue.SurfacePair).To(gomega.Equal("turn_phase_contract,model_protocol"), "expected invalid read model-protocol contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
		gomega.Expect(issue.InvariantID).To(gomega.Equal("turn_phase.action.invalid_advertised"), "expected invalid read model-protocol contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
		gomega.Expect(issue.ActionClass).To(gomega.Equal("read"), "expected invalid read model-protocol contradiction, got %#v", report.TurnPhaseCoverage.Contradictions)
	})

	ginkgo.It("allows a native submit-only turn-phase surface", func() {
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

		gomega.Expect(ReconcileTurnPhaseSurface(phase, surface)).To(gomega.BeEmpty())
	})

	ginkgo.It("flags turn-phase protocol-hash parity failures", func() {
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

		gomega.Expect(ReconcileTurnPhaseSurface(phase, baseSurface)).To(gomega.BeEmpty())

		missingSurface := baseSurface

		missingSurface.ProtocolHash = ""

		missingIssues := ReconcileTurnPhaseSurface(phase, missingSurface)

		gomega.Expect(!hasTurnPhaseInvariant(missingIssues, "turn_phase.identity.protocol_hash", "missing protocol_hash")).To(gomega.BeFalse(), "expected missing protocol hash issue, got %#v", missingIssues)

		mismatchedSurface := baseSurface

		mismatchedSurface.ProtocolHash = "sha256:other"

		mismatchIssues := ReconcileTurnPhaseSurface(phase, mismatchedSurface)

		gomega.Expect(!hasTurnPhaseInvariant(mismatchIssues, "turn_phase.identity.protocol_hash", "differs")).To(gomega.BeFalse(), "expected mismatched protocol hash issue, got %#v", mismatchIssues)

		mismatchedContract := baseSurface

		mismatchedContract.ContractID = "contract-other"

		contractIssues := ReconcileTurnPhaseSurface(phase, mismatchedContract)

		gomega.Expect(!hasTurnPhaseInvariant(contractIssues, "turn_phase.identity.contract_id", "differs")).To(gomega.BeFalse(), "expected mismatched contract id issue, got %#v", contractIssues)
	})

	ginkgo.It("flags turn-phase runtime parser parity failures", func() {
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
			gomega.Expect(!hasTurnPhaseInvariant(issues, want, "differs")).To(gomega.BeFalse(), "expected %s issue, got %#v", want, issues)
		}
	})

	ginkgo.It("flags completion-gate protocol-hash parity failures", func() {
		contract := newEventTestPhaseContract()
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
		missing := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:            GateCompletion,
			Decision:        PolicyReject,
			PackID:          ContractPackCompletionReadiness,
			EnforcementMode: EnforcementModeObserveOnly,
			InvariantID:     "completion.readiness",
			ObservedFacts:   []ContractObservedFact{"completion_gate_missing_hash"},
		},

			occurredAt.Add(2*time.Second), 3))
		mismatch := mustAutonomyEvent(NewGateDecisionEvent(contract, ContractGateEvent{
			Gate:            GateCompletion,
			Decision:        PolicyReject,
			PackID:          ContractPackCompletionReadiness,
			EnforcementMode: EnforcementModeObserveOnly,
			InvariantID:     "completion.readiness",
			ProtocolHash:    "sha256:other",
			ObservedFacts:   []ContractObservedFact{"completion_gate_mismatched_hash"},
		},

			occurredAt.Add(3*time.Second), 4))

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

		gomega.Expect(missingHash).To(gomega.BeFalse(), "expected only mismatched completion-gate protocol hash issue, got %#v", report.TurnPhaseCoverage.Contradictions)
		gomega.Expect(mismatchedHash).To(gomega.BeTrue(), "expected only mismatched completion-gate protocol hash issue, got %#v", report.TurnPhaseCoverage.Contradictions)
	})

	ginkgo.It("blocks review pass when obligations have failed", func() {
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

		gomega.Expect(!sawReviewPass).To(gomega.BeFalse(), "expected invalid review_pass contradiction, got %#v", issues)
	})

	ginkgo.It("carries classification and product signals in all-clear coverage", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 20, 30, 0, 0, time.UTC)
		events := []Event{
			mustAutonomyEvent(NewContractCompiledEvent(contract, occurredAt, 1)),
			mustAutonomyEvent(NewPolicyEvent(EventToolPolicyDecision, contract, ContractPolicyEvent{
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

		gomega.Expect(report.AllClear).To(gomega.BeTrue(), "expected all-clear coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)
		gomega.Expect(report.Findings).To(gomega.BeEmpty(), "expected all-clear coverage, all_clear=%v findings=%#v", report.AllClear, report.Findings)

		for _, want := range allClientClassifications() {
			gomega.Expect(report.MetadataCoverage.ClientClassificationCounts[string(want)] == 0).To(gomega.BeFalse(), "audit report missing client classification %q in %#v", want, report.MetadataCoverage.ClientClassificationCounts)
		}

		for _, want := range allProductSignals() {
			gomega.Expect(report.MetadataCoverage.ProductSignalCounts[string(want)] == 0).To(gomega.BeFalse(), "audit report missing product signal %q in %#v", want, report.MetadataCoverage.ProductSignalCounts)
		}
	})

	ginkgo.It("reports replay intent, missing payloads, and privacy failures", func() {
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

		gomega.Expect(report.ReplayMetrics.MissingRequiredPayloadCounts["gate"]).To(gomega.Equal(1), "expected missing gate payload count, classification=%#v metrics=%#v", report.ReplayClassification, report.ReplayMetrics)
		gomega.Expect(report.ReplayClassification.MissingRequiredPayloadEvents).To(gomega.Equal(1), "expected missing gate payload count, classification=%#v metrics=%#v", report.ReplayClassification, report.ReplayMetrics)

		gomega.Expect(report.PrivacyChecks.ForbiddenFieldCount).NotTo(gomega.Equal(0), "expected privacy forbidden-field detection, got %#v", report.PrivacyChecks)
		gomega.Expect(report.PrivacyChecks.FieldCounts["policy.arguments_summary"]).NotTo(gomega.Equal(0), "expected privacy forbidden-field detection, got %#v", report.PrivacyChecks)

		gomega.Expect(!hasEventAuditFinding(report, "contract_event_privacy_forbidden_raw_content")).To(gomega.BeFalse(), "expected privacy finding, got %#v", report.Findings)

		finding := eventAuditFindingByID(report, "contract_event_privacy_forbidden_raw_content")
		gomega.Expect(finding).NotTo(gomega.BeNil(), "expected privacy finding details, got %#v", finding)
		gomega.Expect(finding.DetailCount).NotTo(gomega.Equal(0), "expected privacy finding details, got %#v", finding)
		gomega.Expect(finding.Details).NotTo(gomega.BeEmpty(), "expected privacy finding details, got %#v", finding)
		gomega.Expect(finding.Details[0].Field).To(gomega.Equal("policy.arguments_summary"), "unexpected privacy detail: %#v", finding.Details[0])
		gomega.Expect(finding.Details[0].Reason).NotTo(gomega.BeEmpty(), "unexpected privacy detail: %#v", finding.Details[0])

		gomega.Expect(report.ReplayClassification.Classification != ReplayClassificationNonReplayable).To(gomega.BeFalse(), "privacy markers should make replay non-replayable, got %#v", report.ReplayClassification)
	})

	ginkgo.It("normalizes unsafe required-next prose for replay privacy", func() {
		contract := newEventTestPhaseContract()
		occurredAt := time.Date(2026, 5, 13, 21, 0, 0, 0, time.UTC)
		presented := mustAutonomyEvent(NewContractPresentedEvent(contract, ContractLifecycleEvent{
			Reason: "turn_phase_contract",
			TurnPhaseContract: &TurnPhaseContractEvent{
				SchemaVersion: "idiom.turn_phase_contract.v1",
				PhaseID:       "main",
				RequiredNext: []string{
					"raw_prompt: customer-acme token=SECRET " + strings.Repeat("x", 520),
					"source_body_required",
				},
			},
		}, occurredAt, 1))

		got := presented.Lifecycle.TurnPhaseContract.RequiredNext
		gomega.Expect(got).To(gomega.HaveLen(2), "unsafe required_next should be replaced with stable ref, got %#v", got)
		gomega.Expect(got[0]).NotTo(gomega.BeEmpty(), "unsafe required_next should be replaced with stable ref, got %#v", got)
		gomega.Expect(got[0]).To(gomega.HavePrefix("sanitized_required_next:"), "unsafe required_next should be replaced with stable ref, got %#v", got)
		gomega.Expect(got[1]).To(gomega.Equal("source_body_required"), "unsafe required_next should be replaced with stable ref, got %#v", got)
		gomega.Expect(strings.Join(got, "\n")).NotTo(gomega.ContainSubstring("customer-acme"), "required_next leaked raw content after normalization: %#v", got)
		gomega.Expect(strings.Join(got, "\n")).NotTo(gomega.ContainSubstring("SECRET"), "required_next leaked raw content after normalization: %#v", got)

		report := AuditEvents([]Event{presented}, EventAuditOptions{})
		gomega.Expect(report.PrivacyChecks.FieldCounts["lifecycle.turn_phase_contract.required_next"]).To(gomega.Equal(0), "normalized required_next should not trigger privacy finding, privacy=%#v findings=%#v", report.PrivacyChecks, report.Findings)
		gomega.Expect(hasEventAuditFinding(report, "contract_event_privacy_forbidden_raw_content")).To(gomega.BeFalse(), "normalized required_next should not trigger privacy finding, privacy=%#v findings=%#v", report.PrivacyChecks, report.Findings)
	})

	ginkgo.It("preserves finding specification order and fields", func() {
		state := eventAuditState{report: EventAuditReport{
			ReferenceCoverage: ContractReferenceCoverage{
				MissingContractIDEvents: []EventAuditRef{
					{EventID: "event-missing-contract-id"},
				},
				UnmatchedContractReferences: []EventAuditRef{
					{EventID: "event-unmatched-contract", ContractID: "contract-unmatched"},
				},
				StaleContractReferences: []EventAuditRef{
					{EventID: "event-stale-contract", ContractID: "contract-stale"},
				},
				UnmatchedParentReferences: []EventAuditRef{
					{EventID: "event-unmatched-parent", ContractID: "contract-child"},
				},
				MissingExpectedContracts: []string{"contract-expected"},
			},
			SidecarToolCorrelation: SidecarToolCorrelationCoverage{
				UnmatchedCorrelations: []SidecarToolCorrelationIssue{
					{EventID: "event-sidecar", ContractID: "contract-sidecar"},
				},
			},
			GateCoverage: GateEventCoverage{
				InconsistentOpenObligationRefs: []EventAuditRef{
					{EventID: "event-gate-open", ContractID: "contract-gate"},
				},
				MissingPackIDRefs: []EventAuditRef{
					{EventID: "event-gate-pack", ContractID: "contract-gate", Reference: "pack_id"},
				},
				MissingEnforcementModeRefs: []EventAuditRef{
					{EventID: "event-gate-mode", ContractID: "contract-gate"},
				},
				MissingSafeAlternativesRefs: []EventAuditRef{
					{EventID: "event-gate-safe", ContractID: "contract-gate"},
				},
			},
			CompletionCoverage: CompletionEventCoverage{
				AllowWithRemainingObligationsRefs: []EventAuditRef{
					{EventID: "event-completion-remaining", ContractID: "contract-completion"},
				},
			},
			ObligationCoverage: ObligationEventCoverage{
				UnmatchedObligations: []ObligationAuditRef{
					{EventID: "event-obligation-unmatched", ContractID: "contract-obligation"},
				},
			},
			TurnPhaseCoverage: TurnPhaseEventCoverage{
				Contradictions: manyTurnPhaseContradictions(26),
			},
			ModelExchangeCoverage: ModelExchangeCoverage{
				EventsMissingProtocolHash: []EventAuditRef{
					{EventID: "event-model-hash", ContractID: "contract-model"},
				},
				EventsMissingHarnessPayload: []EventAuditRef{
					{EventID: "event-model-payload", ContractID: "contract-model"},
				},
			},
			ReadinessForcing: ReadinessForcingCoverage{
				ForcedNoToolsTurns: []EventAuditRef{
					{EventID: "event-forced-no-tools", ContractID: "contract-readiness"},
				},
				UsefulToolsHiddenAfterReadiness: []EventAuditRef{
					{EventID: "event-hidden-tools", ContractID: "contract-readiness"},
				},
				HostComposedAfterRelevantProbes: []EventAuditRef{
					{EventID: "event-host-composed", ContractID: "contract-readiness"},
				},
			},
			PrivacyChecks: ReplayPrivacyChecks{
				Refs: manyReplayPrivacyRefs(26),
			},
		}}

		state.addFindings()

		want := []EventAuditFinding{
			{ID: "contract_event_missing_contract_id", Severity: "high", Evidence: "1 autonomy event(s) lack contract_id", EventIDs: []string{"event-missing-contract-id"}},
			{ID: "contract_event_unmatched_contract_reference", Severity: "medium", Evidence: "1 contract reference(s) have no matching lifecycle event or expected contract", ContractIDs: []string{"contract-unmatched"}},
			{ID: "contract_event_stale_contract_reference", Severity: "medium", Evidence: "1 event(s) reference a stopped or superseded contract", EventIDs: []string{"event-stale-contract"}, ContractIDs: []string{"contract-stale"}},
			{ID: "contract_event_unmatched_parent_reference", Severity: "low", Evidence: "1 parent contract reference(s) have no matching lifecycle event or expected contract", ContractIDs: []string{"contract-child"}},
			{ID: "contract_event_expected_contract_missing", Severity: "medium", Evidence: "1 expected contract(s) had no events", ContractIDs: []string{"contract-expected"}},
			{ID: "contract_event_sidecar_tool_correlation_mismatch", Severity: "medium", Evidence: "1 sidecar/tool-call correlation issue(s)", EventIDs: []string{"event-sidecar"}, ContractIDs: []string{"contract-sidecar"}},
			{ID: "contract_event_gate_open_obligation_inconsistent", Severity: "medium", Evidence: "1 gate open-obligation reference(s) were not open at the gate sequence", EventIDs: []string{"event-gate-open"}, ContractIDs: []string{"contract-gate"}},
			{ID: "contract_event_gate_missing_pack_id", Severity: "medium", Evidence: "1 typed gate event(s) lack pack_id", EventIDs: []string{"event-gate-pack"}, ContractIDs: []string{"contract-gate", "pack_id"}},
			{ID: "contract_event_gate_missing_enforcement_mode", Severity: "medium", Evidence: "1 typed gate event(s) lack enforcement_mode", EventIDs: []string{"event-gate-mode"}, ContractIDs: []string{"contract-gate"}},
			{ID: "contract_event_gate_missing_safe_alternatives", Severity: "medium", Evidence: "1 enforced reject/deny/stop gate event(s) lack safe_alternatives", EventIDs: []string{"event-gate-safe"}, ContractIDs: []string{"contract-gate"}},
			{ID: "contract_event_completion_allow_with_remaining_obligations", Severity: "medium", Evidence: "1 completion allow event(s) reported remaining obligations", EventIDs: []string{"event-completion-remaining"}, ContractIDs: []string{"contract-completion"}},
			{ID: "contract_event_unmatched_obligation_reference", Severity: "low", Evidence: "1 obligation resolution event(s) had no matching opened event", EventIDs: []string{"event-obligation-unmatched"}, ContractIDs: []string{"contract-obligation"}},
			{ID: "turn_phase_contract_contradiction", Severity: "high", Evidence: "26 turn phase contract contradiction(s)", EventIDs: turnPhaseEventIDs(26), ContractIDs: []string{"contract-turn"}, DetailCount: 26},
			{ID: "model_exchange_missing_protocol_hash", Severity: "high", Evidence: "1 model_exchange event(s) lack protocol_hash", EventIDs: []string{"event-model-hash"}, ContractIDs: []string{"contract-model"}},
			{ID: "model_exchange_missing_typed_payload", Severity: "high", Evidence: "1 model_exchange event(s) lack a typed payload", EventIDs: []string{"event-model-payload"}, ContractIDs: []string{"contract-model"}},
			{ID: "readiness_forced_no_tools_turn", Severity: "medium", Evidence: "1 turn phase event(s) allowed final answer while useful tools were unavailable and source-body obligations remained", EventIDs: []string{"event-forced-no-tools"}, ContractIDs: []string{"contract-readiness"}},
			{ID: "readiness_useful_tools_hidden", Severity: "medium", Evidence: "1 turn phase event(s) hid useful read/search tools after readiness with open obligations", EventIDs: []string{"event-hidden-tools"}, ContractIDs: []string{"contract-readiness"}},
			{ID: "readiness_host_composed_after_probe", Severity: "medium", Evidence: "1 completion event(s) allowed a terminal answer after the model requested missing context", EventIDs: []string{"event-host-composed"}, ContractIDs: []string{"contract-readiness"}},
			{ID: "contract_event_privacy_forbidden_raw_content", Severity: "high", Evidence: "26 forbidden raw-content field(s) or value marker(s) in autonomy event metadata", EventIDs: privacyEventIDs(26), ContractIDs: []string{"contract-privacy"}, DetailCount: 26},
		}

		gomega.Expect(len(state.report.Findings) != len(want)).To(gomega.BeFalse(), "findings len = %d, want %d: %#v", len(state.report.Findings), len(want), state.report.Findings)

		for i, wantFinding := range want {
			got := state.report.Findings[i]
			gomega.Expect(got.ID).To(gomega.Equal(wantFinding.ID), "finding[%d] = %#v, want %#v", i, got, wantFinding)
			gomega.Expect(got.Severity).To(gomega.Equal(wantFinding.Severity), "finding[%d] = %#v, want %#v", i, got, wantFinding)
			gomega.Expect(got.Evidence).To(gomega.Equal(wantFinding.Evidence), "finding[%d] = %#v, want %#v", i, got, wantFinding)
			gomega.Expect(got.DetailCount).To(gomega.Equal(wantFinding.DetailCount), "finding[%d] = %#v, want %#v", i, got, wantFinding)
			gomega.Expect(slices.Equal(got.EventIDs, wantFinding.EventIDs)).To(gomega.BeTrue(), "finding[%d] = %#v, want %#v", i, got, wantFinding)
			gomega.Expect(slices.Equal(got.ContractIDs, wantFinding.ContractIDs)).To(gomega.BeTrue(), "finding[%d] = %#v, want %#v", i, got, wantFinding)
		}

		gomega.Expect(len(state.report.Findings[12].Details) != 25).To(gomega.BeFalse(), "turn phase details cap not preserved: got %d", len(state.report.Findings[12].Details))
		detail := state.report.Findings[12].Details[0]
		gomega.Expect(detail.ToolName).To(gomega.Equal("read_file"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(detail.ActionClass).To(gomega.Equal("read"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(detail.PhaseID).To(gomega.Equal("phase-00"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(detail.ModelCallID).To(gomega.Equal("call-00"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(detail.SurfaceID).To(gomega.Equal("surface"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(detail.SurfacePair).To(gomega.Equal("contract,surface"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(detail.InvariantID).To(gomega.Equal("invariant"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(detail.Reason).To(gomega.Equal("invalid read"), "turn phase detail not preserved: %#v", detail)
		gomega.Expect(len(state.report.Findings[18].Details) != 25).To(gomega.BeFalse(), "privacy details cap not preserved: got %d", len(state.report.Findings[18].Details))
		detail = state.report.Findings[18].Details[0]
		gomega.Expect(detail.Field).To(gomega.Equal("metadata.raw.00"), "privacy detail not preserved: %#v", detail)
		gomega.Expect(detail.Reason).To(gomega.Equal("raw content"), "privacy detail not preserved: %#v", detail)
		gomega.Expect(detail.Type).To(gomega.Equal("event_type"), "privacy detail not preserved: %#v", detail)
		gomega.Expect(detail.Sequence).To(gomega.Equal(int64(100)), "privacy detail not preserved: %#v", detail)
		gomega.Expect(detail.ContractID).To(gomega.Equal("contract-privacy"), "privacy detail not preserved: %#v", detail)
	})
})

func manyTurnPhaseContradictions(count int) []TurnPhaseContradiction {
	out := make([]TurnPhaseContradiction, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, TurnPhaseContradiction{
			EventID:     fmt.Sprintf("event-turn-%02d", i),
			Type:        "event_type",
			Sequence:    int64(i + 1),
			ContractID:  "contract-turn",
			PhaseID:     fmt.Sprintf("phase-%02d", i),
			ModelCallID: fmt.Sprintf("call-%02d", i),
			SurfaceID:   "surface",
			SurfacePair: "contract,surface",
			InvariantID: "invariant",
			ToolName:    "read_file",
			ActionClass: "read",
			Reason:      "invalid read",
		})
	}
	return out
}

func manyReplayPrivacyRefs(count int) []ReplayPrivacyRef {
	out := make([]ReplayPrivacyRef, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, ReplayPrivacyRef{
			EventID:    fmt.Sprintf("event-privacy-%02d", i),
			Type:       "event_type",
			Sequence:   int64(100 + i),
			ContractID: "contract-privacy",
			Field:      fmt.Sprintf("metadata.raw.%02d", i),
			Reason:     "raw content",
		})
	}
	return out
}

func turnPhaseEventIDs(count int) []string {
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, fmt.Sprintf("event-turn-%02d", i))
	}
	return out
}

func privacyEventIDs(count int) []string {
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, fmt.Sprintf("event-privacy-%02d", i))
	}
	return out
}

func mustAutonomyEvent(event Event, err error) Event {
	if err != nil {
		panic(err)
	}

	return event
}

func hasEventAuditFinding(report EventAuditReport, id string) bool {
	return eventAuditFindingByID(report, id) != nil
}

func eventAuditFindingByID(report EventAuditReport, id string) *EventAuditFinding {
	for _, finding := range report.Findings {
		if finding.ID == id {
			finding := finding
			return &finding
		}
	}

	return nil
}

func hasTurnPhaseInvariant(issues []TurnPhaseContradiction, invariantID string, reasonContains string) bool {
	for _, issue := range issues {
		if issue.InvariantID == invariantID && strings.Contains(issue.Reason, reasonContains) {
			return true
		}
	}

	return false
}
