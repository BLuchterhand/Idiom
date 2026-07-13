package queue

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Queue admission", func() {
	ginkgo.DescribeTable("maps admission states to event names",
		func(state AdmissionState, want AdmissionEventType) {
			gomega.Expect(AdmissionEventName(state)).To(gomega.Equal(want))
		},
		ginkgo.Entry("acquired", AdmissionStateAcquired, AdmissionEventQueueAcquired),
		ginkgo.Entry("granted", AdmissionStateGranted, AdmissionEventQueueAcquired),
		ginkgo.Entry("nested in the same pool", AdmissionStateNestedSamePool, AdmissionEventQueueAcquired),
		ginkgo.Entry("nested across pools", AdmissionStateNestedCrossPool, AdmissionEventQueueDegraded),
		ginkgo.Entry("unavailable", AdmissionStateUnavailable, AdmissionEventQueueUnavailable),
		ginkgo.Entry("disabled", AdmissionStateDisabled, AdmissionEventQueueDisabled),
		ginkgo.Entry("degraded", AdmissionStateDegraded, AdmissionEventQueueDegraded),
		ginkgo.Entry("error", AdmissionStateError, AdmissionEventQueueError),
		ginkgo.Entry("unknown", AdmissionState("unknown"), AdmissionEventQueueError),
	)

	ginkgo.It("dispatches only acquired or same-pool nested work", func() {
		gomega.Expect(AdmissionStateAllowsDispatch(AdmissionStateAcquired)).To(gomega.BeTrue())
		gomega.Expect(AdmissionStateAllowsDispatch(AdmissionStateNestedSamePool)).To(gomega.BeTrue())
		gomega.Expect(AdmissionStateAllowsDispatch(AdmissionStateNestedCrossPool)).To(gomega.BeFalse())
		gomega.Expect(AdmissionStateAllowsDispatch(AdmissionStateDegraded)).To(gomega.BeFalse())
	})

	ginkgo.DescribeTable("classifies admission failure reasons",
		func(reason string, want ReasonClass) {
			gomega.Expect(ClassifyReason(reason)).To(gomega.Equal(want))
		},
		ginkgo.Entry("empty", "", ReasonClass("")),
		ginkgo.Entry("queue unavailable", "queue_unavailable: no daemon", ReasonClassQueueUnavailable),
		ginkgo.Entry("nested lease", "lease already held by parent", ReasonClassNestedLease),
		ginkgo.Entry("pool disabled", "pool_disabled by config", ReasonClassPoolDisabled),
		ginkgo.Entry("same-pool nesting", "nested_same_pool request", ReasonClassNestedSamePool),
		ginkgo.Entry("cross-pool nesting", "nested_cross_pool request", ReasonClassNestedCrossPool),
		ginkgo.Entry("unknown pool", "unknown pool qwen", ReasonClassUnknownPool),
		ginkgo.Entry("daemon unreachable", "daemon_unreachable: connection refused", ReasonClassDaemonUnreachable),
		ginkgo.Entry("canceled", "context canceled", ReasonClassTimeoutOrCanceled),
		ginkgo.Entry("fallback queue error", "unexpected queue failure", ReasonClassQueueError),
	)

	ginkgo.DescribeTable("classifies structured failure codes",
		func(code string, want ReasonClass) {
			gomega.Expect(ClassifyFailureCode(code)).To(gomega.Equal(want))
		},
		ginkgo.Entry("unavailable", "unavailable", ReasonClassQueueUnavailable),
		ginkgo.Entry("deadline exceeded", "deadline_exceeded", ReasonClassTimeoutOrCanceled),
		ginkgo.Entry("canceled", "canceled", ReasonClassTimeoutOrCanceled),
		ginkgo.Entry("invalid argument", "invalid_argument", ReasonClassUnknownPool),
		ginkgo.Entry("failed precondition", "failed_precondition", ReasonClassQueueError),
		ginkgo.Entry("unknown", "unknown", ReasonClass("")),
		ginkgo.Entry("empty", "", ReasonClass("")),
	)
})
