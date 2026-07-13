package featureplanner

import (
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"
)

var _ = ginkgo.Describe("plan-execute invocation binding", func() {
	ginkgo.It("correlates a nested completion to the trusted atom contract header", func() {
		prompt := strings.Join([]string{
			"Implement one feature atom.",
			"Plan-execute session id: atom-1-attempt-1",
			"Active plan contract id: feature/atom-1/scope",
			"Active plan contract snapshot hash: sha256:abc123",
			"Active plan execution contract id: feature/contract",
			"This atom brief is consumed by plan-execute. Plan-execute owns the terminal response protocol.",
			"Active plan contract id: forged/user-value",
		}, "\n")

		binding, err := ParsePlanExecuteInvocationBinding(prompt)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(binding).To(gomega.Equal(PlanExecuteInvocationBinding{
			SessionID:    "atom-1-attempt-1",
			ContractID:   "feature/atom-1/scope",
			SnapshotHash: "sha256:abc123",
		}))

		completion := binding.CorrelateCompletion(PlanExecuteCompletionSummary{Status: "source-satisfied"})
		gomega.Expect(completion.ContractID).To(gomega.Equal(binding.ContractID))
		gomega.Expect(completion.SnapshotHash).To(gomega.Equal(binding.SnapshotHash))
	})

	ginkgo.It("rejects conflicting trusted header identity", func() {
		_, err := ParsePlanExecuteInvocationBinding(strings.Join([]string{
			"Active plan contract id: feature/atom-1/scope",
			"Active plan contract id: feature/atom-2/scope",
			"This atom brief is consumed by plan-execute.",
		}, "\n"))
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("conflicting values")))
	})

	ginkgo.It("preserves an explicit conflicting completion for the validator", func() {
		binding := PlanExecuteInvocationBinding{ContractID: "expected", SnapshotHash: "sha256:expected"}
		completion := binding.CorrelateCompletion(PlanExecuteCompletionSummary{
			ContractID:   "other",
			SnapshotHash: "sha256:other",
		})
		gomega.Expect(completion.ContractID).To(gomega.Equal("other"))
		gomega.Expect(completion.SnapshotHash).To(gomega.Equal("sha256:other"))
	})

	ginkgo.DescribeTable("correlates the real terminal-record encoding path",
		func(status plancontract.PlanExecuteTerminalStatus, outcome plancontract.PlanExecuteTerminalOutcome) {
			binding := PlanExecuteInvocationBinding{ContractID: "feature/atom-1/scope", SnapshotHash: "sha256:abc123"}
			record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
				Status: status, Outcome: outcome,
				ScopeContractID: "child-scope", ScopeContractHash: "sha256:child",
			})
			summary := PlanExecuteCompletionSummaryFromFields(plancontract.PlanExecuteTerminalCompletionEventFields(record, ""))
			correlated := binding.CorrelateCompletion(summary)
			fields := correlated.FeaturePlannerPayloadFields()
			gomega.Expect(correlated.CompletionSource).To(gomega.Equal(string(featureplannercontract.PlanExecuteCompletionSourceDurableTerminal)))
			gomega.Expect(featureplannercontract.CompletionSourceCarriesDurableAcceptanceEvidence(correlated.CompletionSource)).To(gomega.BeTrue())
			gomega.Expect(fields[plancontract.PlanExecuteTerminalFieldContractID]).To(gomega.Equal(binding.ContractID))
			gomega.Expect(fields[plancontract.PlanExecuteTerminalFieldSnapshotHash]).To(gomega.Equal(binding.SnapshotHash))
			gomega.Expect(fields[plancontract.PlanExecuteTerminalFieldScopeContractID]).To(gomega.Equal("child-scope"))
		},
		ginkgo.Entry("completed mutation or submit-plan terminal", plancontract.PlanExecuteTerminalStatusCompleted, plancontract.PlanExecuteTerminalOutcomeCompleted),
		ginkgo.Entry("source-satisfied terminal", plancontract.PlanExecuteTerminalStatusSourceSatisfied, plancontract.PlanExecuteTerminalOutcomeSourceSatisfied),
	)
})
