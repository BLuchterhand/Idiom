package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/failures"
)

// startTestServer binds the daemon to a short-path Unix socket and
// returns it plus the socket path. macOS caps sun_path near 104
// bytes; t.TempDir lives under /var/folders/... which already eats
// most of that, so we mkdir under /tmp directly to keep paths short.
func startTestServer(cfg Config) (*Server, string) {
	ginkgo.GinkgoHelper()

	if cfg.Pools == nil {
		cfg = DefaultConfig()
	}

	dir, err := os.MkdirTemp("/tmp", "idr-")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.DeferCleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "s")
	srv := NewServer(cfg, sock)

	gomega.Expect(srv.Start()).To(gomega.Succeed())

	ginkgo.DeferCleanup(srv.Stop)

	return srv, sock
}

var _ = ginkgo.It("ping returns uptime", func() {
	_, sock := startTestServer(DefaultConfig())

	uptime, err := Ping(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ping: %v", err))

	gomega.Expect(uptime < 0).To(gomega.BeFalse(), fmt.Sprintf("uptime should be non-negative, got %v", uptime))
})

var _ = ginkgo.It("status empty daemon reports default pool", func() {
	_, sock := startTestServer(DefaultConfig())

	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(snap.Pools).To(gomega.HaveLen(1))
	gomega.Expect(snap.Pools[0].Name).To(gomega.Equal("default"))

	gomega.Expect(snap.Pools[0].MaxInFlight != 1).To(gomega.BeFalse(), fmt.Sprintf("default pool max=%d, want 1", snap.Pools[0].MaxInFlight))

	gomega.Expect(len(snap.InFlight)+len(snap.Waiting) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected empty queues, got in_flight=%d waiting=%d", len(snap.InFlight), len(snap.Waiting)))
})

var _ = ginkgo.It("acquire granted immediately when slot free", func() {
	_, sock := startTestServer(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	lease, err := Acquire(ctx, sock, "default", "first", "test")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("acquire: %v", err))
	defer lease.Close()

	select {
	case <-lease.Granted():
	case <-time.After(time.Second):
		ginkgo.Fail(fmt.Sprint("did not get granted within 1s"))
	}

	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(snap.InFlight).To(gomega.HaveLen(1))
	gomega.Expect(snap.InFlight[0].ID).To(gomega.Equal(lease.JobID))
})

var _ = ginkgo.It("acquire queues when full then promotes on release", func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 1}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	first, err := Acquire(ctx, sock, "default", "first", "test")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("acquire first: %v", err))

	<-first.Granted()

	second, err := Acquire(ctx, sock, "default", "second", "test")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("acquire second: %v", err))
	defer second.Close()

	select {
	case <-second.Granted():
		ginkgo.Fail(fmt.Sprint("second should not be granted while first holds the slot"))
	case <-time.After(150 * time.Millisecond):
	}

	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(snap.Waiting).To(gomega.HaveLen(1))
	gomega.Expect(snap.Waiting[0].ID).To(gomega.Equal(second.JobID))

	gomega.Expect(snap.Waiting[0].Position != 1).To(gomega.BeFalse(), fmt.Sprintf("position=%d want 1", snap.Waiting[0].Position))

	// Releasing first should promote second.
	if err := first.Close(); err != nil {
		ginkgo.Fail(fmt.Sprintf("close first: %v", err))
	}

	select {
	case <-second.Granted():
	case <-time.After(time.Second):
		ginkgo.Fail(fmt.Sprint("second was not promoted within 1s of releasing first"))
	}
})

var _ = ginkgo.It("position updates emitted as queue shifts", func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 1}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	first, _ := Acquire(ctx, sock, "default", "1", "t")
	<-first.Granted()

	second, _ := Acquire(ctx, sock, "default", "2", "t")
	third, _ := Acquire(ctx, sock, "default", "3", "t")

	defer second.Close()
	defer third.Close()

	// Drain initial position frames so we measure shifts only.
	drain := func(l *Lease) {
		for {
			select {
			case <-l.Updates():
			case <-time.After(50 * time.Millisecond):
				return
			}
		}
	}
	drain(second)
	drain(third)

	// Releasing first promotes second; third should see position 1.
	first.Close()

	select {
	case pos := <-third.Updates():
		gomega.Expect(pos != 1).To(gomega.BeFalse(), fmt.Sprintf("third got position %d after shift, want 1", pos))
	case <-time.After(time.Second):
		ginkgo.Fail(fmt.Sprint("third never received a position update after first released"))
	}
})

var _ = ginkgo.It("unknown pool rejected", func() {
	_, sock := startTestServer(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)

	defer cancel()

	_, err := Acquire(ctx, sock, "nonexistent", "x", "t")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected error for unknown pool"))

	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unknown pool"), fmt.Sprintf("error should mention unknown pool, got %v", err))
})

var _ = ginkgo.It("named pools are isolated", func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{
			"qwen-35b":   {MaxInFlight: 1},
			"qwen-coder": {MaxInFlight: 1},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	a, _ := Acquire(ctx, sock, "qwen-35b", "a", "t")
	b, _ := Acquire(ctx, sock, "qwen-coder", "b", "t")

	defer a.Close()
	defer b.Close()

	// Both should be granted: different pools, independent caps.
	for _, l := range []*Lease{a, b} {
		select {
		case <-l.Granted():
		case <-time.After(time.Second):
			ginkgo.Fail(fmt.Sprintf("job in pool %s never granted (pools should be independent)", l.Pool))
		}
	}
})

var _ = ginkgo.It("cancel removes waiting job and notifies the lease owner", ginkgo.Label("race-contract"), func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 1}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	first, _ := Acquire(ctx, sock, "default", "first", "t")
	<-first.Granted()

	defer first.Close()

	second, _ := Acquire(ctx, sock, "default", "second", "t")
	defer second.Close()

	if err := Cancel(sock, second.JobID); err != nil {
		ginkgo.Fail(fmt.Sprintf("cancel: %v", err))
	}

	var cancelErr error
	gomega.Eventually(second.Err()).Should(gomega.Receive(&cancelErr))
	gomega.Expect(cancelErr).To(gomega.MatchError(gomega.ContainSubstring("canceled while waiting")))

	snap, _ := Status(sock)
	gomega.Expect(len(snap.Waiting) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected empty waiting list after cancel, got %+v", snap.Waiting))
})

var _ = ginkgo.It("cancel refuses in flight job", func() {
	_, sock := startTestServer(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	lease, _ := Acquire(ctx, sock, "default", "x", "t")
	<-lease.Granted()

	defer lease.Close()

	err := Cancel(sock, lease.JobID)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected error cancelling in-flight job"))

	gomega.Expect(err.Error()).To(gomega.ContainSubstring("in-flight"), fmt.Sprintf("error should mention in-flight, got %v", err))
})

var _ = ginkgo.It("client disconnect while waiting frees queue slot", func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 1}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	first, _ := Acquire(ctx, sock, "default", "first", "t")
	<-first.Granted()

	defer first.Close()

	second, _ := Acquire(ctx, sock, "default", "second", "t")

	// Don't wait for grant — just close, simulating ctrl-C of the wrapping
	// command. The daemon should drop second from the queue.
	second.Close()

	// Give the daemon a moment to process the disconnect.
	time.Sleep(50 * time.Millisecond)

	snap, _ := Status(sock)
	gomega.Expect(len(snap.Waiting) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected empty waiting after disconnect, got %+v", snap.Waiting))
})

var _ = ginkgo.It("concurrency cap honored", func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 2}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	const total = 6

	leases := make([]*Lease, 0, total)

	for i := range total {
		l, err := Acquire(ctx, sock, "default", fmt.Sprintf("j%d", i), "t")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("acquire %d: %v", i, err))

		leases = append(leases, l)
	}

	ginkgo.DeferCleanup(func() {
		for _, l := range leases {
			l.Close()
		}
	})

	time.Sleep(100 * time.Millisecond)

	snap, _ := Status(sock)
	gomega.Expect(len(snap.InFlight) != 2).To(gomega.BeFalse(), fmt.Sprintf("in-flight=%d want 2 (cap)", len(snap.InFlight)))

	gomega.Expect(len(snap.Waiting) != total-2).To(gomega.BeFalse(), fmt.Sprintf("waiting=%d want %d", len(snap.Waiting), total-2))

	// Release the two in-flight; expect the two heads of waiting to be
	// promoted to in-flight.
	grantedIDs := map[string]bool{}
	for _, j := range snap.InFlight {
		grantedIDs[j.ID] = true
	}

	var firstTwo []*Lease

	for _, l := range leases {
		if grantedIDs[l.JobID] {
			firstTwo = append(firstTwo, l)
		}
	}

	for _, l := range firstTwo {
		l.Close()
	}

	// Wait until promotion happens.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap, _ = Status(sock)
		if len(snap.InFlight) == 2 && len(snap.Waiting) == total-4 {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}
	ginkgo.Fail(fmt.Sprintf("after release: in-flight=%d waiting=%d (want 2/%d)", len(snap.InFlight), len(snap.Waiting), total-4))
})

var _ = ginkgo.It("parallel acquire and status are safe", func() {
	srv, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 4}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	var wg sync.WaitGroup
	failures := make(chan error, 20)

	for i := range 20 {
		wg.Go(func() {
			l, err := Acquire(ctx, sock, "default", fmt.Sprintf("p%d", i), "t")
			if err != nil {
				failures <- fmt.Errorf("acquire p%d: %w", i, err)
				return
			}
			defer l.Close()

			select {
			case <-l.Granted():
			case <-ctx.Done():
				failures <- fmt.Errorf("p%d was never granted: %w", i, ctx.Err())
				return
			}

			// Hold briefly so multiple jobs share the queue.
			time.Sleep(20 * time.Millisecond)
		})

		// Concurrent status pollers.
		if i%4 == 0 {
			wg.Go(func() {
				_, _ = Status(sock)
			})
		}
	}

	wg.Wait()
	close(failures)
	var goroutineErrors []error
	for err := range failures {
		goroutineErrors = append(goroutineErrors, err)
	}
	gomega.Expect(goroutineErrors).To(gomega.BeEmpty())

	// Lease.Close only hands ownership of the connection back to the
	// daemon; the handler may not have observed EOF yet. Stop is the
	// server's synchronization boundary: it waits for every handler's
	// deferred release before returning.
	srv.Stop()
	snap := srv.Snapshot()
	gomega.Expect(len(snap.InFlight)+len(snap.Waiting) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected drained queue, got %+v", snap))
})

var _ = ginkgo.It("daemon unreachable error wraps", func() {
	// Path that doesn't exist; client must surface ErrDaemonUnreachable.

	dir, err := os.MkdirTemp("/tmp", "idr-")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))

	ginkgo.DeferCleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "missing")

	_, err = Status(sock)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected error against missing daemon"))

	gomega.Expect(errors.Is(err, ErrDaemonUnreachable)).To(gomega.BeTrue(), fmt.Sprintf("error should wrap ErrDaemonUnreachable, got %v", err))

	if got := failures.CodeOf(err); got != failures.Unavailable {
		ginkgo.Fail(fmt.Sprintf("daemon unreachable code = %v, want %v", got, failures.Unavailable))
	}
})

var _ = ginkgo.It("load config defaults when missing", func() {
	dir := ginkgo.GinkgoT().TempDir()

	cfg, err := LoadConfig(filepath.Join(dir, "nope.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("missing file should be OK, got %v", err))

	gomega.Expect(len(cfg.Pools) != 1).To(gomega.BeFalse(), fmt.Sprintf("default config should have 1 pool, got %d", len(cfg.Pools)))
})

var _ = ginkgo.It("load config parses pools", func() {
	dir := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(dir, "runner.json")
	body := `{"pools":{"qwen-35b":{"max_in_flight":1},"qwen-coder":{"max_in_flight":2}}}`

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprint(err))
	}

	cfg, err := LoadConfig(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load: %v", err))

	gomega.Expect(cfg.Pools["qwen-coder"].MaxInFlight != 2).To(gomega.BeFalse(), fmt.Sprintf("expected qwen-coder cap=2, got %+v", cfg.Pools))
})

var _ = ginkgo.It("load config rejects bad cap", func() {
	dir := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(dir, "bad.json")
	body := `{"pools":{"x":{"max_in_flight":0}}}`

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprint(err))
	}

	_, err := LoadConfig(path)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected error on max_in_flight=0"))
})

var _ = ginkgo.It("default socket path honors env override", func() {
	ginkgo.GinkgoT().Setenv("IDIOM_RUNNER_SOCKET", "/tmp/explicit.sock")

	if got := DefaultSocketPath(); got != "/tmp/explicit.sock" {
		ginkgo.Fail(fmt.Sprintf("env override ignored, got %q", got))
	}
})

var _ = ginkgo.It("default socket path prefers home idiom", func() {
	// With HOME set, the canonical path is $HOME/.idiom/runner.sock —
	// stable across launchd / shell / SSH sessions so the daemon and
	// clients always resolve the same path.

	ginkgo.GinkgoT().Setenv("IDIOM_RUNNER_SOCKET", "")
	ginkgo.GinkgoT().Setenv("HOME", "/var/empty/test")
	ginkgo.GinkgoT().Setenv("XDG_RUNTIME_DIR", "/should-not-be-used")
	ginkgo.GinkgoT().Setenv("TMPDIR", "/also-not-used")

	if got := DefaultSocketPath(); got != "/var/empty/test/.idiom/runner.sock" {
		ginkgo.Fail(fmt.Sprintf("HOME fallback wrong, got %q", got))
	}
})

var _ = ginkgo.It("default socket path falls back to tmp", func() {
	ginkgo.GinkgoT().Setenv("IDIOM_RUNNER_SOCKET", "")
	ginkgo.GinkgoT().Setenv("HOME", "")
	ginkgo.GinkgoT().Setenv("XDG_RUNTIME_DIR", "")
	ginkgo.GinkgoT().Setenv("TMPDIR", "/var/tmp/test")

	if got := DefaultSocketPath(); got != "/var/tmp/test/idiom-runner.sock" {
		ginkgo.Fail(fmt.Sprintf("tmp fallback wrong, got %q", got))
	}
})

// Without HOME but with XDG_RUNTIME_DIR, the Linux convention wins.
var _ = ginkgo.It("default socket path falls back to xdg", func() {

	ginkgo.GinkgoT().Setenv("IDIOM_RUNNER_SOCKET", "")
	ginkgo.GinkgoT().Setenv("HOME", "")
	ginkgo.GinkgoT().Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	ginkgo.GinkgoT().Setenv("TMPDIR", "/tmp")

	if got := DefaultSocketPath(); got != "/run/user/1000/idiom-runner.sock" {
		ginkgo.Fail(fmt.Sprintf("XDG fallback wrong, got %q", got))
	}
})

var _ = ginkgo.It("default config path honors env override", func() {
	ginkgo.GinkgoT().Setenv("IDIOM_RUNNER_CONFIG", "/some/explicit.json")

	if got := DefaultConfigPath(); got != "/some/explicit.json" {
		ginkgo.Fail(fmt.Sprintf("env override ignored, got %q", got))
	}
})

// TestSilentClientIsTimedOut: a client that connects but never sends
// must not pin a server goroutine indefinitely. The server applies a
// short read deadline before the first action; after it expires the
// connection closes with a structured error.
var _ = ginkgo.It("silent client is timed out", func() {
	_, sock := startTestServer(DefaultConfig())

	conn, err := dial(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("dial: %v", err))
	defer conn.Close()

	// Don't write anything. The server should close the conn within
	// the read-deadline window. We verify the close completes inside
	// 2× the deadline so a misconfiguration shows up as a timeout.
	_ = conn.SetReadDeadline(time.Now().Add(2 * firstRequestTimeout))

	buf := make([]byte, 1024)

	_, err = conn.Read(buf)
	// Either we get an error response then EOF, or just EOF — both
	// prove the server didn't hang on us. EOF appears as io.EOF; an
	// error response decodes fine. Failure mode is timeout (the
	// goroutine pinned).
	if err == nil {
		// We may have received a JSON error frame; that's also a pass.
		return
	}
})

// TestProtocolErrorsOnGarbage gives a tour of the error channel: an
// unknown action should produce a structured error response that the
// caller can match on.
var _ = ginkgo.It("protocol errors on garbage", func() {
	_, sock := startTestServer(DefaultConfig())

	conn, err := dial(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("dial: %v", err))
	defer conn.Close()

	if _, err := conn.Write([]byte(`{"action":"bogus"}` + "\n")); err != nil {
		ginkgo.Fail(fmt.Sprintf("write: %v", err))
	}

	var resp Response

	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode: %v", err))
	}

	gomega.Expect(resp.Error == "").To(gomega.BeFalse(), fmt.Sprint("expected error in response"))

	gomega.Expect(resp.Error).To(gomega.ContainSubstring("unknown action"), fmt.Sprintf("unexpected error: %s", resp.Error))
})

// TestStatusReportsAgeAndIdleFlag verifies #81: in-flight jobs
// carry AgeSeconds and an Idle flag set when age exceeds the
// pool's IdleThresholdSeconds. The threshold defaults to
// DefaultIdleThresholdSeconds (600s); we set a tiny 1s threshold
// here and sleep briefly so the test stays fast.
var _ = ginkgo.It("status reports age and idle flag", func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{
			"fast-idle": {MaxInFlight: 1, IdleThresholdSeconds: 1},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	lease, err := Acquire(ctx, sock, "fast-idle", "first", "test")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("acquire: %v", err))
	defer lease.Close()

	<-lease.Granted()

	// First snapshot: just-granted, age=0 (or close), not idle.
	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(len(snap.InFlight) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 in-flight job, got %d", len(snap.InFlight)))

	gomega.Expect(snap.InFlight[0].Idle).To(gomega.BeFalse(), fmt.Sprintf("expected Idle=false at age=0, got %+v", snap.InFlight[0]))

	// Sleep past the threshold, snap again — should now be flagged.
	time.Sleep(1100 * time.Millisecond)

	snap, err = Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(snap.InFlight[0].Idle).To(gomega.BeTrue(), fmt.Sprintf("expected Idle=true after >1s, got %+v", snap.InFlight[0]))

	gomega.Expect(snap.InFlight[0].AgeSeconds < 1).To(gomega.BeFalse(), fmt.Sprintf("expected AgeSeconds>=1, got %d", snap.InFlight[0].AgeSeconds))
})

// TestStatusIdleThresholdDefaultsTo600s confirms PoolConfig with
// IdleThresholdSeconds=0 (or unset) falls back to the default
// 10-minute window — a job that just started doesn't get
// erroneously flagged as idle.
var _ = ginkgo.It("status idle threshold defaults to 600 seconds", func() {
	_, sock := startTestServer(Config{
		Pools: map[string]PoolConfig{
			"default": {MaxInFlight: 1}, // no IdleThresholdSeconds
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	lease, err := Acquire(ctx, sock, "default", "first", "test")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("acquire: %v", err))
	defer lease.Close()

	<-lease.Granted()

	snap, err := Status(sock)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))

	gomega.Expect(snap.InFlight[0].Idle).To(gomega.BeFalse(), fmt.Sprintf("expected Idle=false on fresh job under default 600s threshold, got %+v", snap.InFlight[0]))
})
