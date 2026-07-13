package recovery

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Recovery failure vocabulary", func() {
	ginkgo.It("recognizes declared lanes, classes, and statuses", func() {
		for _, lane := range []SourceLane{SourceLanePlanExecute, SourceLaneImplement} {
			gomega.Expect(KnownSourceLane(lane)).To(gomega.BeTrue())
		}
		for _, class := range FailureClasses() {
			gomega.Expect(KnownFailureClass(class)).To(gomega.BeTrue())
		}
		for _, status := range DirtyStatuses() {
			parsed, ok := ParseDirtyStatus(string(status))
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(parsed).To(gomega.Equal(status))
		}
		for _, status := range ScopeStatuses() {
			parsed, ok := ParseScopeStatus(string(status))
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(parsed).To(gomega.Equal(status))
		}

		gomega.Expect(KnownSourceLane(SourceLane("typo"))).To(gomega.BeFalse())
		gomega.Expect(KnownFailureClass(FailureClass("typo"))).To(gomega.BeFalse())
		_, ok := ParseDirtyStatus("typo")
		gomega.Expect(ok).To(gomega.BeFalse())
		_, ok = ParseScopeStatus("typo")
		gomega.Expect(ok).To(gomega.BeFalse())
	})

	ginkgo.It("includes every canonical failure class", func() {
		gomega.Expect(FailureClasses()).To(gomega.ContainElements(
			FailureClassPlannerFailed,
			FailureClassPlannerBlocked,
			FailureClassPlanMissing,
			FailureClassPlanInvalid,
			FailureClassScopeTooLarge,
			FailureClassExecutorEdit,
			FailureClassVerification,
			FailureClassReviewRepair,
			FailureClassStaleContext,
			FailureClassBudgetExhausted,
			FailureClassDirtyWorkspace,
			FailureClassNeedsHumanAnswer,
			FailureClassUnsafePolicyBlock,
			FailureClassUnknown,
		))
	})

	ginkgo.It("trims canonical inputs while remaining case-sensitive", func() {
		class, ok := ParseFailureClass(" \t" + string(FailureClassPlannerFailed) + "\n")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(class).To(gomega.Equal(FailureClassPlannerFailed))
		dirty, ok := ParseDirtyStatus(" \t" + string(DirtyStatusDirtyInScope) + "\n")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(dirty).To(gomega.Equal(DirtyStatusDirtyInScope))
		scope, ok := ParseScopeStatus(" \t" + string(ScopeStatusOutOfScope) + "\n")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(scope).To(gomega.Equal(ScopeStatusOutOfScope))

		for _, raw := range []string{"PLANNER_FAILED", "DIRTY_IN_SCOPE", "OUT_OF_SCOPE"} {
			_, ok = ParseFailureClass(raw)
			gomega.Expect(ok).To(gomega.BeFalse())
			_, ok = ParseDirtyStatus(raw)
			gomega.Expect(ok).To(gomega.BeFalse())
			_, ok = ParseScopeStatus(raw)
			gomega.Expect(ok).To(gomega.BeFalse())
		}
	})
})
