package taskledger

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Task-ledger status", func() {
	ginkgo.DescribeTable("normalizes and validates status values",
		func(input string, expected Status, expectedValid bool) {
			actual, valid := Parse(input)
			gomega.Expect(actual).To(gomega.Equal(expected))
			gomega.Expect(valid).To(gomega.Equal(expectedValid))
		},
		ginkgo.Entry("pending", " pending ", StatusPending, true),
		ginkgo.Entry("in progress", "IN-PROGRESS", StatusInProgress, true),
		ginkgo.Entry("completed", "completed", StatusCompleted, true),
		ginkgo.Entry("blocked", "blocked", StatusBlocked, true),
		ginkgo.Entry("cancelled", "cancelled", StatusCancelled, true),
		ginkgo.Entry("unknown", "waiting", Status("waiting"), false),
	)

	ginkgo.It("keeps values and lifecycle predicates aligned", func() {
		gomega.Expect(Values()).To(gomega.Equal([]string{"pending", "in_progress", "completed", "blocked", "cancelled"}))
		gomega.Expect(StatusPending.Open()).To(gomega.BeTrue())
		gomega.Expect(StatusInProgress.Open()).To(gomega.BeTrue())
		gomega.Expect(StatusBlocked.Open()).To(gomega.BeFalse())
		gomega.Expect(StatusCompleted.Finished()).To(gomega.BeTrue())
		gomega.Expect(StatusCancelled.Finished()).To(gomega.BeTrue())
		gomega.Expect(StatusBlocked.Finished()).To(gomega.BeFalse())
	})
})
