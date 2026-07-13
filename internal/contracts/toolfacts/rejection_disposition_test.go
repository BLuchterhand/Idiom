package toolfacts

import (
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.DescribeTable("classifies dispositions that carry rejected write intent",
	func(disposition ToolRejectionDisposition, want bool) {
		gomega.Expect(disposition.CarriesRejectedWriteIntent()).To(gomega.Equal(want))
	},
	ginkgo.Entry("phase deferred", ToolRejectionDeferredByPhase, true),
	ginkgo.Entry("prerequisite deferred", ToolRejectionDeferredByPrerequisite, true),
	ginkgo.Entry("not retained", ToolRejectionNotRetained, false),
	ginkgo.Entry("invalid request", ToolRejectionInvalidRequest, false),
	ginkgo.Entry("policy denied", ToolRejectionPolicyDenied, false),
	ginkgo.Entry("empty", ToolRejectionDisposition(""), false),
)

var _ = ginkgo.It("clones rejection disposition and owns path storage", func() {
	original := &ToolResultFacts{
		RejectedWritePaths:     []string{"internal/feature/status.go"},
		DeferredPlanPaths:      []string{"internal/feature/plan.go"},
		PendingSourceBodyPaths: []string{"internal/feature/source.go"},
		PendingHistoryPaths:    []string{"internal/feature/history.go"},
		RejectionDisposition:   ToolRejectionDeferredByPhase,
	}
	cloned := original.Clone()

	gomega.Expect(cloned).NotTo(gomega.BeNil())
	gomega.Expect(cloned.RejectionDisposition).To(gomega.Equal(ToolRejectionDeferredByPhase))
	cloned.RejectedWritePaths[0] = "internal/feature/other.go"
	gomega.Expect(original.RejectedWritePaths).To(gomega.Equal([]string{"internal/feature/status.go"}))
	cloned.DeferredPlanPaths[0] = "internal/feature/other-plan.go"
	gomega.Expect(original.DeferredPlanPaths).To(gomega.Equal([]string{"internal/feature/plan.go"}))
	cloned.PendingSourceBodyPaths[0] = "internal/feature/other-source.go"
	gomega.Expect(original.PendingSourceBodyPaths).To(gomega.Equal([]string{"internal/feature/source.go"}))
	cloned.PendingHistoryPaths[0] = "internal/feature/other-history.go"
	gomega.Expect(original.PendingHistoryPaths).To(gomega.Equal([]string{"internal/feature/history.go"}))
})

var _ = ginkgo.DescribeTable("classifies phase-local rejection dispositions",
	func(disposition ToolRejectionDisposition, want bool) {
		gomega.Expect(disposition.PhaseLocal()).To(gomega.Equal(want))
	},
	ginkgo.Entry("deferred by phase", ToolRejectionDeferredByPhase, true),
	ginkgo.Entry("deferred by prerequisite", ToolRejectionDeferredByPrerequisite, false),
	ginkgo.Entry("invalid request", ToolRejectionInvalidRequest, false),
	ginkgo.Entry("policy denied", ToolRejectionPolicyDenied, false),
)
