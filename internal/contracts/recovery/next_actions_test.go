package recovery

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Recovery next actions", func() {
	ginkgo.DescribeTable("parses canonical and legacy spellings",
		func(input string, want NextAction) {
			got, ok := ParseNextAction(input)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(got).To(gomega.Equal(want))
		},
		ginkgo.Entry("exact", "retry_plan_execute", NextActionRetryPlanExecute),
		ginkgo.Entry("legacy hyphenated", "retry-plan-execute", NextActionRetryPlanExecute),
	)

	ginkgo.It("rejects unknown actions", func() {
		_, ok := ParseNextAction("retry-plan-typo")
		gomega.Expect(ok).To(gomega.BeFalse())
	})

	ginkgo.It("parses every declared action", func() {
		for _, action := range NextActions() {
			parsed, ok := ParseNextAction(string(action))
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(parsed).To(gomega.Equal(action))
		}
	})

	ginkgo.DescribeTable("canonicalizes surrounding whitespace, case, and hyphens",
		func(input string) {
			parsed, ok := ParseNextAction(input)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(parsed).To(gomega.Equal(NextActionRetryPlanExecute))
		},
		ginkgo.Entry("upper snake case", " \tRETRY_PLAN_EXECUTE\n"),
		ginkgo.Entry("lower kebab case", " \tretry-plan-execute\n"),
		ginkgo.Entry("title kebab case", " \tRetry-Plan-Execute\n"),
	)
})
