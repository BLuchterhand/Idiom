package readapproval

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Read approval decisions", func() {
	ginkgo.DescribeTable("renders outcomes",
		func(outcome Outcome, want string) {
			gomega.Expect(outcome.String()).To(gomega.Equal(want))
		},
		ginkgo.Entry("approved", OutcomeApproved, "approved"),
		ginkgo.Entry("prompt required", OutcomePromptRequired, "prompt_required"),
		ginkgo.Entry("denied", OutcomeDenied, "denied"),
		ginkgo.Entry("unknown", Outcome(99), "unknown"),
	)

	ginkgo.Describe("session-scoped decisions", func() {
		ginkgo.It("records decisions by path and lets later decisions override earlier ones", func() {
			decisions := NewSessionDecisions()

			_, found := decisions.Lookup("docs/foo.md")
			gomega.Expect(found).To(gomega.BeFalse())

			decisions.Record(Decision{
				Outcome: OutcomeApproved,
				Reason:  ReasonOperatorApprovedLive,
				Source:  SourceOperator,
				At:      time.Now(),
				Path:    "docs/foo.md",
			})

			got, found := decisions.Lookup("docs/foo.md")
			gomega.Expect(found).To(gomega.BeTrue())
			gomega.Expect(got.Outcome).To(gomega.Equal(OutcomeApproved))

			decisions.Record(Decision{
				Outcome: OutcomeDenied,
				Reason:  ReasonOperatorDeniedLive,
				Source:  SourceOperator,
				At:      time.Now(),
				Path:    "docs/foo.md",
			})

			got, found = decisions.Lookup("docs/foo.md")
			gomega.Expect(found).To(gomega.BeTrue())
			gomega.Expect(got.Outcome).To(gomega.Equal(OutcomeDenied))
		})

		ginkgo.It("reports approved and denied path sets independently", func() {
			decisions := NewSessionDecisions()
			decisions.Record(Decision{Outcome: OutcomeApproved, Path: "a.md"})
			decisions.Record(Decision{Outcome: OutcomeApproved, Path: "b.md"})
			decisions.Record(Decision{Outcome: OutcomeDenied, Path: "c.md"})

			gomega.Expect(decisions.ApprovedPathSet()).To(gomega.Equal(map[string]struct{}{
				"a.md": {},
				"b.md": {},
			}))
			gomega.Expect(decisions.DeniedPathSet()).To(gomega.Equal(map[string]struct{}{
				"c.md": {},
			}))
		})
	})
})
