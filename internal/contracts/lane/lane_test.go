package lane

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Lane event vocabulary", func() {
	ginkgo.DescribeTable("declared event types",
		func(eventType EventType) {
			gomega.Expect(KnownEventType(eventType)).To(gomega.BeTrue())
			gomega.Expect(EventName(eventType)).To(gomega.Equal(EventPrefix + string(eventType)))
		},
		ginkgo.Entry("start", EventStart),
		ginkgo.Entry("run ledger", EventRunLedger),
		ginkgo.Entry("degraded", EventDegraded),
		ginkgo.Entry("atom intake", EventAtomIntake),
	)

	ginkgo.It("rejects undeclared event types", func() {
		gomega.Expect(KnownEventType(EventType("surprise"))).To(gomega.BeFalse())
	})
})
