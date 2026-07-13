package workflowstate

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Workflow state projections", func() {
	ginkgo.It("defines projection vocabulary for every supported workflow", func() {
		vocabularies := projectionVocabularies()
		gomega.Expect(vocabularies).To(gomega.HaveLen(9))

		byID := map[WorkflowID]projectionVocabulary{}
		for _, vocabulary := range vocabularies {
			gomega.Expect(vocabulary.WorkflowID).NotTo(gomega.BeEmpty())
			gomega.Expect(byID).NotTo(gomega.HaveKey(vocabulary.WorkflowID))
			gomega.Expect(vocabulary.States).NotTo(gomega.BeEmpty())
			gomega.Expect(vocabulary.States).To(gomega.ContainElement(StateTerminal))
			gomega.Expect(vocabulary.TerminalStates).NotTo(gomega.BeEmpty())
			byID[vocabulary.WorkflowID] = vocabulary
		}

		gomega.Expect(byID).To(gomega.HaveKey(WorkflowDirectAnswer))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowPlanExecutePlanner))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowPlanExecuteExecutor))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowImplement))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowRecovery))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowSidecarToolCall))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowProductDispatch))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowAtomIntake))
		gomega.Expect(byID).To(gomega.HaveKey(WorkflowPromptContextPacket))
	})

	ginkgo.DescribeTable("declares emitted nonterminal states",
		func(workflowID WorkflowID, state StateID) {
			vocabulary, found := projectionVocabularyByWorkflowID(workflowID)
			gomega.Expect(found).To(gomega.BeTrue())
			gomega.Expect(vocabulary.States).To(gomega.ContainElement(state))
		},
		ginkgo.Entry("plan-execute dispatchable", WorkflowPlanExecutePlanner, StateDispatchable),
		ginkgo.Entry("atom intake dispatchable", WorkflowAtomIntake, StateDispatchable),
		ginkgo.Entry("prompt context dispatchable", WorkflowPromptContextPacket, StateDispatchable),
		ginkgo.Entry("recovery inspecting", WorkflowRecovery, StateInspecting),
	)

	ginkgo.It("declares every emitted terminal state in at least one workflow", func() {
		seen := map[TerminalState]bool{}
		for _, vocabulary := range projectionVocabularies() {
			for _, terminal := range vocabulary.TerminalStates {
				seen[terminal] = true
			}
		}
		for _, terminal := range []TerminalState{
			TerminalStateComplete,
			TerminalStateSourceSatisfied,
			TerminalStateBlocked,
			TerminalStateStaleNeedsRebase,
			TerminalStateInvalidScope,
			TerminalStateMissingScope,
			TerminalStateBlockedDirtyFiles,
			TerminalStateFailedRefineDraft,
			TerminalStateDirtyScope,
			TerminalStateVerificationFailed,
			TerminalStateRecoveryOpen,
		} {
			gomega.Expect(seen).To(gomega.HaveKeyWithValue(terminal, true))
		}
	})

	ginkgo.DescribeTable("rejects inconsistent terminal projections",
		func(workflowID WorkflowID, state StateID, terminalState TerminalState, dispatchable bool) {
			gomega.Expect(ValidateStateProjection(workflowID, state, terminalState, dispatchable)).To(gomega.HaveOccurred())
		},
		ginkgo.Entry("missing terminal state", WorkflowPlanExecutePlanner, StateTerminal, TerminalState(""), false),
		ginkgo.Entry("dispatchable terminal", WorkflowPlanExecutePlanner, StateTerminal, TerminalStateBlocked, true),
		ginkgo.Entry("terminal state on nonterminal projection", WorkflowRecovery, StateInspecting, TerminalStateRecoveryOpen, false),
	)

	ginkgo.DescribeTable("enforces state and dispatchability vocabulary",
		func(workflowID WorkflowID, state StateID, terminalState TerminalState, dispatchable, wantError bool) {
			err := ValidateStateProjection(workflowID, state, terminalState, dispatchable)
			if wantError {
				gomega.Expect(err).To(gomega.HaveOccurred())
				return
			}
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		},
		ginkgo.Entry("valid dispatchable state", WorkflowPlanExecutePlanner, StateDispatchable, TerminalState(""), true, false),
		ginkgo.Entry("dispatchable state missing flag", WorkflowPlanExecutePlanner, StateDispatchable, TerminalState(""), false, true),
		ginkgo.Entry("non-dispatchable state with flag", WorkflowRecovery, StateInspecting, TerminalState(""), true, true),
		ginkgo.Entry("unknown workflow", WorkflowID("unknown"), StateDispatchable, TerminalState(""), true, true),
		ginkgo.Entry("unknown state", WorkflowRecovery, StateID("unknown"), TerminalState(""), false, true),
		ginkgo.Entry("terminal invalid for workflow", WorkflowPlanExecutePlanner, StateTerminal, TerminalStateDirtyScope, false, true),
	)
})
