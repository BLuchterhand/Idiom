package runner

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	queuecontract "idiom/internal/contracts/queue"
)

var _ = ginkgo.It("maybe acquire no op when daemon absent", func() {
	// No daemon = no-op. MaybeAcquire must not block or error; entry
	// points proceed un-gated. Pointing at a path that doesn't exist
	// exercises the "no socket file" branch of socketReachable.

	dir := ginkgo.GinkgoT().TempDir()
	missing := filepath.Join(dir, "definitely-not-a-socket.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)

	defer cancel()

	start := time.Now()

	result := MaybeAcquire(ctx, missing, "qwen-35b", "label", "test", nil)
	gomega.Expect(result.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected no error when daemon absent, got %v", result.Err))

	gomega.Expect(result.State != queuecontract.AdmissionStateUnavailable).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateUnavailable))

	gomega.Expect(result.Release).NotTo(gomega.BeNil(), fmt.Sprintf("release must be non-nil even on no-op path"))

	// Must return well under the dial timeout (200ms).
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		ginkgo.Fail(fmt.Sprintf("no-op path took too long: %v", elapsed))
	}

	result.Release() // idempotent on no-op
})

var _ = ginkgo.It("maybe acquire reports unavailable when daemon absent", func() {
	dir := ginkgo.GinkgoT().TempDir()
	missing := filepath.Join(dir, "definitely-not-a-socket.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)

	defer cancel()

	result := MaybeAcquire(ctx, missing, "qwen-35b", "label", "test", nil)
	gomega.Expect(result.State != queuecontract.AdmissionStateUnavailable).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateUnavailable))

	gomega.Expect(result.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unavailable opportunistic queue should not return an error, got %v", result.Err))

	gomega.Expect(result.Release).NotTo(gomega.BeNil(), fmt.Sprint("Release must be non-nil for unavailable no-op path"))

	result.Release()
})

var _ = ginkgo.It("maybe acquire grants immediately when slot free", func() {
	_, sock := startTestServer(DefaultConfig())

	var statusBuf bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	result := MaybeAcquire(ctx, sock, "default", "test-label", "test-client", &statusBuf)
	gomega.Expect(result.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("MaybeAcquire: %v", result.Err))

	gomega.Expect(result.State != queuecontract.AdmissionStateAcquired).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateAcquired))

	gomega.Expect(result.Release).NotTo(gomega.BeNil(), fmt.Sprintf("release must be non-nil"))

	// Confirm the daemon sees us in flight.
	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(len(snap.InFlight) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 in-flight job, got %d", len(snap.InFlight)))

	gomega.Expect(snap.InFlight[0].Label != "test-label").To(gomega.BeFalse(), fmt.Sprintf("label not propagated: got %q", snap.InFlight[0].Label))

	result.Release()

	// After release, daemon should show no in-flight jobs.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap, err = Status(sock)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status post-release: %v", err))

		if len(snap.InFlight) == 0 {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}
	ginkgo.Fail(fmt.Sprintf("expected release to drop in-flight count to 0; snap=%+v", snap))
})

var _ = ginkgo.It("maybe acquire reports acquired when slot granted", func() {
	_, sock := startTestServer(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	result := MaybeAcquire(ctx, sock, "default", "typed-label", "typed-client", nil)
	gomega.Expect(result.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("MaybeAcquire: %v", result.Err))

	gomega.Expect(result.State != queuecontract.AdmissionStateAcquired).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateAcquired))

	gomega.Expect(result.Release).NotTo(gomega.BeNil(), fmt.Sprint("Release must be non-nil"))

	result.Release()
})

var _ = ginkgo.It("maybe acquire serializes under pool cap", func() {
	// Two concurrent MaybeAcquire calls against a pool with
	// max_in_flight=1 must serialize: the second blocks until the
	// first's release. This is the load-bearing invariant for the
	// "two `idiom lane plan-execute` runs don't both hammer the MLX
	// backend at once" fix.

	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 1}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	secondResult := make(chan MaybeAcquireResult, 1)

	// First acquire blocks the slot.
	result1 := MaybeAcquire(ctx, sock, "default", "first", "test-1", nil)
	gomega.Expect(result1.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("first acquire: %v", result1.Err))

	gomega.Expect(result1.State != queuecontract.AdmissionStateAcquired).To(gomega.BeFalse(), fmt.Sprintf("first state = %q, want acquired", result1.State))

	// Second acquire must wait until first releases.
	go func() {
		result2 := MaybeAcquire(ctx, sock, "default", "second", "test-2", nil)
		secondResult <- result2
	}()

	// Briefly verify second is NOT yet granted.
	select {
	case result2 := <-secondResult:
		if result2.Release != nil {
			result2.Release()
		}
		ginkgo.Fail(fmt.Sprintf("second acquire should not be granted while first holds the slot"))
	case <-time.After(150 * time.Millisecond):
		// Expected: still waiting.
	}

	result1.Release()

	select {
	case result2 := <-secondResult:
		gomega.Expect(result2.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("second acquire: %v", result2.Err))
		gomega.Expect(result2.State != queuecontract.AdmissionStateAcquired).To(gomega.BeFalse(), fmt.Sprintf("second state = %q, want acquired", result2.State))
		result2.Release()
	case <-time.After(2 * time.Second):
		ginkgo.Fail(fmt.Sprint("second acquire never got promoted after first released"))
	}
})

// Reentrancy guard: when IDIOM_QUEUE_HELD_POOLS lists the pool,
// MaybeAcquire returns a no-op release without dialing. This is
// the load-bearing behavior that prevents `idiom queue run`
// self-deadlock when the wrapped command invokes another idiom
// subcommand that itself calls MaybeAcquire on the same pool.
var _ = ginkgo.It("maybe acquire skips when ancestor holds pool", func() {

	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"qwen-coder": {MaxInFlight: 1}},
	})
	// Hold the slot via a direct Acquire to simulate an ancestor's
	// outstanding lease.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	parent, err := Acquire(ctx, sock, "qwen-coder", "parent", "test-parent")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parent Acquire: %v", err))
	defer parent.Close()

	<-parent.Granted()

	// Child sees IDIOM_QUEUE_HELD_POOLS in env and must NOT dial.
	ginkgo.GinkgoT().Setenv(HeldPoolsEnv, "qwen-coder")

	childCtx, childCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)

	defer childCancel()

	start := time.Now()

	result := MaybeAcquire(childCtx, sock, "qwen-coder", "child", "test-child", nil)
	gomega.Expect(result.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("child MaybeAcquire: %v", result.Err))

	gomega.Expect(result.State != queuecontract.AdmissionStateNestedSamePool).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateNestedSamePool))

	// Sub-millisecond return; if it tried to dial the busy pool it
	// would block until the context cancels (~500ms) or longer.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		ginkgo.Fail(fmt.Sprintf("reentrant skip took too long (%v); did it actually dial?", elapsed))
	}

	result.Release()

	// The parent's slot is still held — confirms the child did NOT
	// touch the daemon's counters.
	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(snap.InFlight).To(gomega.HaveLen(1))
	gomega.Expect(snap.InFlight[0].Label).To(gomega.Equal("parent"))

	gomega.Expect(len(snap.Waiting) != 0).To(gomega.BeFalse(), fmt.Sprintf("child must not appear as a waiter; got %+v", snap.Waiting))
})

var _ = ginkgo.It("maybe acquire reports nested same pool state", func() {
	ginkgo.GinkgoT().Setenv(HeldPoolsEnv, "qwen-coder")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result := MaybeAcquire(ctx, "unused.sock", "qwen-coder", "child", "test-child", nil)
	gomega.Expect(result.State != queuecontract.AdmissionStateNestedSamePool).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateNestedSamePool))
	gomega.Expect(result.Reason != string(queuecontract.ReasonClassNestedSamePool)).To(gomega.BeFalse(), fmt.Sprintf("Reason = %q, want %q", result.Reason, queuecontract.ReasonClassNestedSamePool))
	result.Release()
})

// Cross-pool nested acquire: IDIOM_QUEUE_HELD_POOLS lists pool
// A; nested call requests pool B. The previous behavior was to
// acquire pool B normally, but that created a dining-philosophers
// deadlock when two sessions each held one pool and waited for
// the other (observed 2026-05-10: Colony held qwen-35b waiting on
// qwen-coder; idiom held qwen-coder waiting on qwen-35b).
// Current behavior: skip the nested acquire — the ancestor's
// outer lease is enough serialization for this session, and
// avoiding the daemon entirely cannot deadlock.
var _ = ginkgo.It("maybe acquire skips cross pool nested acquire", func() {

	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{
			"qwen-35b":   {MaxInFlight: 1},
			"qwen-coder": {MaxInFlight: 1},
		},
	})
	ginkgo.GinkgoT().Setenv(HeldPoolsEnv, "qwen-35b")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	start := time.Now()

	result := MaybeAcquire(ctx, sock, "qwen-coder", "nested", "test-nested", nil)
	gomega.Expect(result.Err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("MaybeAcquire on cross-pool nested: %v", result.Err))
	gomega.Expect(result.State != queuecontract.AdmissionStateNestedCrossPool).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateNestedCrossPool))
	defer result.Release()

	// Sub-millisecond return; if it tried to dial the daemon it
	// would take longer than 50ms even on a localhost socket.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		ginkgo.Fail(fmt.Sprintf("cross-pool reentrant skip took too long (%v); did it actually dial?", elapsed))
	}

	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(len(snap.InFlight) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected zero in-flight (nested call must not acquire), got %+v", snap.InFlight))

	gomega.Expect(len(snap.Waiting) != 0).To(gomega.BeFalse(), fmt.Sprintf("nested call must not appear as a waiter; got %+v", snap.Waiting))
})

var _ = ginkgo.It("maybe acquire reports nested cross pool state", func() {
	ginkgo.GinkgoT().Setenv(HeldPoolsEnv, "qwen-35b")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result := MaybeAcquire(ctx, "unused.sock", "qwen-coder", "child", "test-child", nil)
	gomega.Expect(result.State != queuecontract.AdmissionStateNestedCrossPool).To(gomega.BeFalse(), fmt.Sprintf("State = %q, want %q", result.State, queuecontract.AdmissionStateNestedCrossPool))
	gomega.Expect(result.Reason != string(queuecontract.ReasonClassNestedCrossPool)).To(gomega.BeFalse(), fmt.Sprintf("Reason = %q, want %q", result.Reason, queuecontract.ReasonClassNestedCrossPool))
	result.Release()
})

var _ = ginkgo.It("pool held by ancestor parses colon list", func() {
	cases := []struct {
		held string
		pool string
		want bool
	}{
		{"", "qwen-35b", false},
		{"qwen-35b", "qwen-35b", true},
		{"qwen-coder", "qwen-35b", false},
		{"qwen-35b:qwen-coder", "qwen-coder", true},
		{"qwen-35b:qwen-coder", "qwen-35b", true},
		{"qwen-35b:qwen-coder", "other", false},
		{" qwen-35b : qwen-coder ", "qwen-coder", true}, // whitespace-tolerant
		{"qwen-35b", "", false},
	}
	for _, c := range cases {
		got := PoolHeldByAncestor(c.held, c.pool)
		gomega.Expect(got != c.want).To(gomega.BeFalse(), fmt.Sprintf("PoolHeldByAncestor(%q, %q) = %v, want %v", c.held, c.pool, got, c.want))
	}
})

var _ = ginkgo.It("pool for model alias mapping", func() {
	cases := []struct {
		alias string
		want  string
	}{
		{"qwen3.6-35b", "qwen-35b"},
		{"qwen3.6-27b", "qwen-27b"},
		{"qwen3.6-27b-planner", "qwen-27b"},
		{"qwen3-coder-30b", "qwen-coder"},
		{"Qwen3-CODER-30B-A3B", "qwen-coder"},
		{"qwen-planner-7b", "qwen-35b"}, // "planner" alias still maps to planner pool
		{"llama3-70b", ""},              // unknown family: caller falls back to "default"
		{"", ""},
	}
	for _, c := range cases {
		got := PoolForModelAlias(c.alias)
		gomega.Expect(got != c.want).To(gomega.BeFalse(), fmt.Sprintf("PoolForModelAlias(%q) = %q, want %q", c.alias, got, c.want))
	}
})
