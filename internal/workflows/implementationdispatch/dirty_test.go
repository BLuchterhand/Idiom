package implementationdispatch

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Dirty-scope dispatch authority", func() {
	ginkgo.It("marks only the plan document containing the named dirty path", func() {
		docs := []PlanDocument{
			{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
			{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
		}
		got := PlanDocumentsWithDirtyAllowance(
			"implement atom and explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go for this run",
			docs,
		)
		gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeTrue())
		gomega.Expect(got[1].AllowDirtyAtomScope).To(gomega.BeFalse())
	})

	ginkgo.DescribeTable("rejecting non-authoritative dirty-path language",
		func(prompt string) {
			docs := []PlanDocument{{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}}}
			gomega.Expect(PlanDocumentsWithDirtyAllowance(prompt, docs)[0].AllowDirtyAtomScope).To(gomega.BeFalse())
		},
		ginkgo.Entry("quoted guidance", "atom intake said next: finish, commit, or explicitly allow the dirty scoped paths before dispatch"),
		ginkgo.Entry("negated authority", "do not explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go"),
		ginkgo.Entry("reported next action", "atom intake blocked; next: allow dirty path internal/contracts/writerroute/decision_test.go"),
		ginkgo.Entry("without flag", "rerun without --allow-dirty"),
		ginkgo.Entry("conditional guidance", "avoid --allow-dirty unless the user says so"),
	)

	ginkgo.It("allows every scoped plan document when the broad flag is explicit", func() {
		docs := []PlanDocument{
			{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
			{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
		}
		got := PlanDocumentsWithDirtyAllowance("rerun with --allow-dirty for these atom scopes", docs)
		gomega.Expect(got).To(gomega.HaveEach(gomega.HaveField("AllowDirtyAtomScope", true)))
	})

	ginkgo.It("rejects dirty-path authority that escapes the workspace", func() {
		docs := []PlanDocument{{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}}}
		got := PlanDocumentsWithDirtyAllowance("explicitly allow dirty scoped path ../internal/contracts/writerroute/decision_test.go", docs)
		gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse())
	})
})
