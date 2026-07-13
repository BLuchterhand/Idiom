package plan

import (
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type terminalVocabularyCase struct {
	record        PlanExecuteTerminalCompletionRecord
	expectedClass PlanExecuteTerminalClass
	expectedOut   PlanExecuteTerminalOutcome
	expectedFlow  workflowstate.WorkflowID
	expectedState workflowstate.TerminalState
	known         []string
	terminal      []string
}

var _ = Describe("Plan-execute terminal projection", func() {
	It("does not mask an invalid planner-complete tuple", func() {
		workflowID, terminalState, dispatchable := planExecuteTerminalWorkflowProjection(PlanExecuteTerminalCompletionRecord{
			Phase: PlanExecuteProgressPhasePlanning, Status: PlanExecuteTerminalStatusCompleted,
			TerminalClass: PlanExecuteTerminalClassSuccess, Outcome: PlanExecuteTerminalOutcomeCompleted,
		})
		Expect(workflowID).To(Equal(workflowstate.WorkflowPlanExecutePlanner))
		Expect(terminalState).To(Equal(workflowstate.TerminalStateComplete))
		Expect(dispatchable).To(BeFalse())
		Expect(workflowstate.ValidateTerminalProjection(workflowID, terminalState)).To(HaveOccurred())
	})

	It("does not derive dirty scope from planner prose", func() {
		workflowID, terminalState, dispatchable := planExecuteTerminalWorkflowProjection(PlanExecuteTerminalCompletionRecord{
			Phase: PlanExecuteProgressPhasePlanning, Status: PlanExecuteTerminalStatusFailed,
			TerminalClass: PlanExecuteTerminalClassFailed, FailureKind: PlanExecuteFailurePlannerBlocked,
			FailureReason: "workspace has dirty files",
		})
		Expect(workflowID).To(Equal(workflowstate.WorkflowPlanExecutePlanner))
		Expect(terminalState).To(Equal(workflowstate.TerminalStateBlocked))
		Expect(dispatchable).To(BeFalse())
		Expect(workflowstate.ValidateTerminalProjection(workflowID, terminalState)).To(Succeed())
	})

	DescribeTable("centralizes status aliases across classification and projection",
		func(testCase terminalVocabularyCase) {
			class, outcome := ClassifyPlanExecuteTerminalCompletion(testCase.record)
			Expect(class).To(Equal(testCase.expectedClass))
			Expect(outcome).To(Equal(testCase.expectedOut))
			workflowID, terminalState, dispatchable := planExecuteTerminalWorkflowProjection(testCase.record)
			Expect(workflowID).To(Equal(testCase.expectedFlow))
			Expect(terminalState).To(Equal(testCase.expectedState))
			Expect(dispatchable).To(BeFalse())
			for _, status := range testCase.known {
				Expect(knownPlanExecuteTerminalStatus(status)).To(BeTrue())
			}
			for _, status := range testCase.terminal {
				Expect(PlanExecuteTerminalStatusIsTerminal(status)).To(BeTrue())
			}
		},
		Entry("completed status", terminalVocabularyCase{
			record:        PlanExecuteTerminalCompletionRecord{Phase: PlanExecuteProgressPhaseComplete, Status: PlanExecuteTerminalStatusCompleted},
			expectedClass: PlanExecuteTerminalClassSuccess, expectedOut: PlanExecuteTerminalOutcomeCompleted,
			expectedFlow: workflowstate.WorkflowPlanExecuteExecutor, expectedState: workflowstate.TerminalStateComplete,
			known:    []string{string(PlanExecuteTerminalStatusCompleted), planExecuteTerminalStatusComplete},
			terminal: []string{string(PlanExecuteTerminalStatusCompleted), planExecuteTerminalStatusSuccess, planExecuteTerminalStatusSucceeded},
		}),
		Entry("source-satisfied alias", terminalVocabularyCase{
			record:        PlanExecuteTerminalCompletionRecord{Phase: PlanExecuteProgressPhaseComplete, Status: PlanExecuteTerminalStatusSourceSatisfied},
			expectedClass: PlanExecuteTerminalClassSuccess, expectedOut: PlanExecuteTerminalOutcomeSourceSatisfied,
			expectedFlow: workflowstate.WorkflowPlanExecutePlanner, expectedState: workflowstate.TerminalStateSourceSatisfied,
			known: []string{string(PlanExecuteTerminalStatusSourceSatisfied)}, terminal: []string{string(PlanExecuteTerminalStatusSourceSatisfied)},
		}),
		Entry("failure alias", terminalVocabularyCase{
			record:        PlanExecuteTerminalCompletionRecord{Phase: PlanExecuteProgressPhaseComplete, Status: planExecuteTerminalStatusFailure},
			expectedClass: PlanExecuteTerminalClassFailed, expectedOut: PlanExecuteTerminalOutcomeFailed,
			expectedFlow: workflowstate.WorkflowPlanExecuteExecutor, expectedState: workflowstate.TerminalStateBlocked,
			known:    []string{string(PlanExecuteTerminalStatusFailed), planExecuteTerminalStatusFailure},
			terminal: []string{string(PlanExecuteTerminalStatusFailed), planExecuteTerminalStatusFailure},
		}),
		Entry("failed review overrides completion", terminalVocabularyCase{
			record:        PlanExecuteTerminalCompletionRecord{Phase: PlanExecuteProgressPhaseComplete, Status: PlanExecuteTerminalStatusCompleted, ReviewStatus: PlanExecuteReviewStatusFailed},
			expectedClass: PlanExecuteTerminalClassFailed, expectedOut: PlanExecuteTerminalOutcomeFailed,
			expectedFlow: workflowstate.WorkflowPlanExecuteExecutor, expectedState: workflowstate.TerminalStateBlocked,
			known: []string{string(PlanExecuteTerminalStatusCompleted)}, terminal: []string{string(PlanExecuteTerminalStatusCompleted)},
		}),
		Entry("running blocks", terminalVocabularyCase{
			record:        PlanExecuteTerminalCompletionRecord{Phase: PlanExecuteProgressPhase(planExecuteTerminalStatusRunning), Status: PlanExecuteTerminalStatus(planExecuteTerminalStatusRunning)},
			expectedClass: PlanExecuteTerminalClassRecoveryRequired, expectedOut: PlanExecuteTerminalOutcomeBlocked,
			expectedFlow: workflowstate.WorkflowPlanExecuteExecutor, expectedState: workflowstate.TerminalStateBlocked,
			known: []string{planExecuteTerminalStatusRunning},
		}),
	)

	It("blocks a completed status when review failed", func() {
		decision := DecidePlanExecuteTerminalCompletion(PlanExecuteTerminalCompletionRecord{
			Phase: PlanExecuteProgressPhaseComplete, Status: PlanExecuteTerminalStatusCompleted, ReviewStatus: PlanExecuteReviewStatusFailed,
		}, nil)
		Expect(decision.Class).To(Equal(PlanExecuteTerminalDecisionBlocked))
		Expect(decision.Reason).NotTo(BeEmpty())
	})

	It("rejects generic C++ source-sanity fallback", func() {
		failures := PlanExecuteTerminalSourceSanityValidationFailures(PlanExecuteTerminalCompletionRecord{
			ChangedPaths: []string{"native/sim.cpp"},
			SourceSanity: []PhaseEvidenceCheck{{Path: "native/sim.cpp", Status: PhaseEvidenceStatusPassed, Summary: "generic fallback reported no delimiter issues"}},
		})
		Expect(failures).To(Equal([]string{"source_sanity_degraded:native/sim.cpp"}))
	})

	It("uses recovery-contract next-action vocabulary", func() {
		for _, action := range recoverycontract.NextActions() {
			Expect(knownPlanExecuteTerminalNextAction(action)).To(BeTrue())
		}
		Expect(knownPlanExecuteTerminalNextAction(recoverycontract.NextAction("retry-plan-typo"))).To(BeFalse())
	})

	It("covers every terminal phase-evidence kind", func() {
		for _, kind := range []PhaseEvidenceKind{
			PhaseEvidenceKindSourceSanity, PhaseEvidenceKindVerification,
			PhaseEvidenceKindPatchHygiene, PhaseEvidenceKindSubmittedEdit,
		} {
			parsed, ok := ParsePhaseEvidenceKind(string(kind))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(kind))
			Expect(KnownPhaseEvidenceKind(kind)).To(BeTrue())
		}
		_, ok := ParsePhaseEvidenceKind("git_diff")
		Expect(ok).To(BeFalse())
	})

	It("covers every terminal phase-evidence status", func() {
		for _, status := range []PhaseEvidenceStatus{
			PhaseEvidenceStatusOK, PhaseEvidenceStatusPassed, PhaseEvidenceStatusFailed,
			PhaseEvidenceStatusFailure, PhaseEvidenceStatusError, PhaseEvidenceStatusMissing,
			PhaseEvidenceStatusSkipped, PhaseEvidenceStatusDegraded, PhaseEvidenceStatusBlocked,
			PhaseEvidenceStatusUnavailable, PhaseEvidenceStatusUnknown, PhaseEvidenceStatusApplied,
			PhaseEvidenceStatusPending,
		} {
			parsed, ok := ParsePhaseEvidenceStatus(string(status))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(status))
			Expect(KnownPhaseEvidenceStatus(status)).To(BeTrue())
		}
		_, ok := ParsePhaseEvidenceStatus("ready")
		Expect(ok).To(BeFalse())
	})
})
