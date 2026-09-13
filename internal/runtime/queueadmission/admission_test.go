package queueadmission

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	queuecontract "idiom/internal/contracts/queue"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/runner"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Queue admission", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("IDIOM_QUEUE_HELD_POOLS", "")
	})

	It("allows dispatch and reports disabled when no pool resolves", func() {
		GinkgoT().Setenv("IDIOM_QUEUE_POOL", "")
		GinkgoT().Setenv("IDIOM_QUEUE_SOCKET", "")
		var stdout, stderr bytes.Buffer

		result := Acquire(config.Config{}, Options{
			Command:        "plan_execute",
			EventPrefix:    queuecontract.CommandEventPrefixPlanExecute,
			TelemetryEvent: "plan_execute_queue",
			Label:          "plan-execute:session",
			JSONEvents:     true,
		}, nil, &stdout, &stderr)
		result.Release()

		Expect(result.Allowed).To(BeTrue())
		Expect(result.State).To(Equal(queuecontract.AdmissionStateDisabled))
		Expect(stdout.String()).To(ContainSubstring(`"type":"` + queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueDisabled) + `"`))
		Expect(stdout.String()).To(ContainSubstring(`"reason_class":"pool_disabled"`))
		Expect(stderr.String()).To(BeEmpty())
	})

	It("blocks a required queue when its daemon is unavailable", func() {
		GinkgoT().Setenv("IDIOM_QUEUE_POOL", "qwen-coder")
		GinkgoT().Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(GinkgoT().TempDir(), "missing.sock"))
		var stdout, stderr bytes.Buffer

		result := Acquire(config.Config{}, Options{
			Command:     "plan_execute",
			EventPrefix: queuecontract.CommandEventPrefixPlanExecute,
			JSONEvents:  true,
			Mode:        queuecontract.AdmissionModeRequired,
		}, nil, &stdout, &stderr)
		result.Release()

		Expect(result.Allowed).To(BeFalse())
		Expect(result.State).To(Equal(queuecontract.AdmissionStateUnavailable))
		Expect(stdout.String()).To(ContainSubstring(`"type":"` + queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueUnavailable) + `"`))
	})

	It("holds the admitted slot until release even after the admission timeout", func() {
		// Keep the Unix socket short enough for macOS's sockaddr_un limit.
		dir, err := os.MkdirTemp("", "iq-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socket := filepath.Join(dir, "q.sock")
		server := runner.NewServer(runner.Config{Pools: map[string]runner.PoolConfig{
			"default": {MaxInFlight: 1},
		}}, socket)
		Expect(server.Start()).To(Succeed())
		DeferCleanup(server.Stop)
		GinkgoT().Setenv("IDIOM_QUEUE_POOL", "default")
		GinkgoT().Setenv("IDIOM_QUEUE_SOCKET", socket)

		result := Acquire(config.Config{}, Options{Mode: queuecontract.AdmissionModeRequired, WaitTimeout: 100 * time.Millisecond}, nil, io.Discard, io.Discard)
		DeferCleanup(result.Release)
		Expect(result.Allowed).To(BeTrue())
		Expect(result.State).To(Equal(queuecontract.AdmissionStateAcquired))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		DeferCleanup(cancel)
		waiting, err := runner.Acquire(ctx, socket, "default", "second", "test")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(waiting.Close)
		Consistently(waiting.Granted(), 200*time.Millisecond).ShouldNot(BeClosed())
		timedOut := Acquire(config.Config{}, Options{Mode: queuecontract.AdmissionModeRequired, WaitTimeout: 20 * time.Millisecond}, nil, io.Discard, io.Discard)
		DeferCleanup(timedOut.Release)
		Expect(timedOut.Allowed).To(BeFalse())
		Expect(timedOut.Err).To(MatchError(context.DeadlineExceeded))

		result.Release()
		Eventually(waiting.Granted()).Should(BeClosed())
	})

	It("blocks nested cross-pool bypass and emits a degraded event", func() {
		GinkgoT().Setenv("IDIOM_QUEUE_POOL", "qwen-coder")
		GinkgoT().Setenv("IDIOM_QUEUE_HELD_POOLS", "qwen-35b")
		GinkgoT().Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(GinkgoT().TempDir(), "missing.sock"))
		var stdout, stderr bytes.Buffer

		result := Acquire(config.Config{}, Options{
			Command:     "plan_execute",
			EventPrefix: queuecontract.CommandEventPrefixPlanExecute,
			JSONEvents:  true,
			Mode:        queuecontract.AdmissionModeRequired,
		}, nil, &stdout, &stderr)
		result.Release()

		Expect(result.Allowed).To(BeFalse())
		Expect(result.State).To(Equal(queuecontract.AdmissionStateNestedCrossPool))
		Expect(stdout.String()).NotTo(ContainSubstring(`"type":"` + queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueAcquired) + `"`))
		Expect(stdout.String()).To(ContainSubstring(`"type":"` + queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueDegraded) + `"`))
		Expect(stdout.String()).To(ContainSubstring(`"degraded":true`))
	})

	DescribeTable("normalizes shared queue reasons",
		func(reason string, expected queuecontract.ReasonClass) {
			Expect(queuecontract.ClassifyReason(reason)).To(Equal(expected))
		},
		Entry("nested same pool", "nested_same_pool", queuecontract.ReasonClassNestedSamePool),
		Entry("nested cross pool", "nested_cross_pool", queuecontract.ReasonClassNestedCrossPool),
		Entry("unknown pool", "unknown pool qwen-35b", queuecontract.ReasonClassUnknownPool),
		Entry("daemon unreachable", "daemon_unreachable", queuecontract.ReasonClassDaemonUnreachable),
		Entry("connection refused", "connection refused", queuecontract.ReasonClassDaemonUnreachable),
		Entry("deadline exceeded", "context deadline exceeded", queuecontract.ReasonClassTimeoutOrCanceled),
		Entry("queue error", "unexpected queue daemon error", queuecontract.ReasonClassQueueError),
	)

	DescribeTable("resolves the queue wait timeout",
		func(options Options, expected time.Duration) {
			Expect(queueAdmissionWaitTimeout(options)).To(Equal(expected))
		},
		Entry("uses the default", Options{}, defaultQueueAdmissionWaitTimeout),
		Entry("honors a positive override", Options{WaitTimeout: 2 * time.Second}, 2*time.Second),
		Entry("ignores a negative override", Options{WaitTimeout: -time.Second}, defaultQueueAdmissionWaitTimeout),
	)
})
