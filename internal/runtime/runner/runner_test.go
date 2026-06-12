package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"idiom/internal/platform/failures"
)

// startTestServer binds the daemon to a short-path Unix socket and
// returns it plus the socket path. macOS caps sun_path near 104
// bytes; t.TempDir lives under /var/folders/... which already eats
// most of that, so we mkdir under /tmp directly to keep paths short.
func startTestServer(t *testing.T, cfg Config) (*Server, string) {
	t.Helper()

	if cfg.Pools == nil {
		cfg = DefaultConfig()
	}

	dir, err := os.MkdirTemp("/tmp", "idr-")
	if err != nil {
		t.Fatalf("mkdir tmp socket dir: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "s")
	srv := NewServer(cfg, sock)

	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	t.Cleanup(srv.Stop)

	return srv, sock
}

func TestPingReturnsUptime(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())

	uptime, err := Ping(sock)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}

	if uptime < 0 {
		t.Fatalf("uptime should be non-negative, got %v", uptime)
	}
}

func TestStatusEmptyDaemonReportsDefaultPool(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())

	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if len(snap.Pools) != 1 || snap.Pools[0].Name != "default" {
		t.Fatalf("expected single default pool, got %+v", snap.Pools)
	}

	if snap.Pools[0].MaxInFlight != 1 {
		t.Fatalf("default pool max=%d, want 1", snap.Pools[0].MaxInFlight)
	}

	if len(snap.InFlight)+len(snap.Waiting) != 0 {
		t.Fatalf("expected empty queues, got in_flight=%d waiting=%d", len(snap.InFlight), len(snap.Waiting))
	}
}

func TestAcquireGrantedImmediatelyWhenSlotFree(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	lease, err := Acquire(ctx, sock, "default", "first", "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.Close()

	select {
	case <-lease.Granted():
	case <-time.After(time.Second):
		t.Fatal("did not get granted within 1s")
	}

	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if len(snap.InFlight) != 1 || snap.InFlight[0].ID != lease.JobID {
		t.Fatalf("expected our job in flight, got %+v", snap.InFlight)
	}
}

func TestAcquireQueuesWhenFullThenPromotesOnRelease(t *testing.T) {
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 1}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	first, err := Acquire(ctx, sock, "default", "first", "test")
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}

	<-first.Granted()

	second, err := Acquire(ctx, sock, "default", "second", "test")
	if err != nil {
		t.Fatalf("acquire second: %v", err)
	}
	defer second.Close()

	select {
	case <-second.Granted():
		t.Fatal("second should not be granted while first holds the slot")
	case <-time.After(150 * time.Millisecond):
	}

	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if len(snap.Waiting) != 1 || snap.Waiting[0].ID != second.JobID {
		t.Fatalf("expected second waiting at pos 1, got waiting=%+v", snap.Waiting)
	}

	if snap.Waiting[0].Position != 1 {
		t.Fatalf("position=%d want 1", snap.Waiting[0].Position)
	}

	// Releasing first should promote second.
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	select {
	case <-second.Granted():
	case <-time.After(time.Second):
		t.Fatal("second was not promoted within 1s of releasing first")
	}
}

func TestPositionUpdatesEmittedAsQueueShifts(t *testing.T) {
	_, sock := startTestServer(t, Config{
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
		if pos != 1 {
			t.Fatalf("third got position %d after shift, want 1", pos)
		}
	case <-time.After(time.Second):
		t.Fatal("third never received a position update after first released")
	}
}

func TestUnknownPoolRejected(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)

	defer cancel()

	_, err := Acquire(ctx, sock, "nonexistent", "x", "t")
	if err == nil {
		t.Fatal("expected error for unknown pool")
	}

	if !contains(err.Error(), "unknown pool") {
		t.Fatalf("error should mention unknown pool, got %v", err)
	}
}

func TestNamedPoolsAreIsolated(t *testing.T) {
	_, sock := startTestServer(t, Config{
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
			t.Fatalf("job in pool %s never granted (pools should be independent)", l.Pool)
		}
	}
}

func TestCancelRemovesWaitingJob(t *testing.T) {
	_, sock := startTestServer(t, Config{
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
		t.Fatalf("cancel: %v", err)
	}

	snap, _ := Status(sock)
	if len(snap.Waiting) != 0 {
		t.Fatalf("expected empty waiting list after cancel, got %+v", snap.Waiting)
	}
}

func TestCancelRefusesInFlightJob(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	lease, _ := Acquire(ctx, sock, "default", "x", "t")
	<-lease.Granted()

	defer lease.Close()

	err := Cancel(sock, lease.JobID)
	if err == nil {
		t.Fatal("expected error cancelling in-flight job")
	}

	if !contains(err.Error(), "in-flight") {
		t.Fatalf("error should mention in-flight, got %v", err)
	}
}

func TestClientDisconnectWhileWaitingFreesQueueSlot(t *testing.T) {
	_, sock := startTestServer(t, Config{
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
	if len(snap.Waiting) != 0 {
		t.Fatalf("expected empty waiting after disconnect, got %+v", snap.Waiting)
	}
}

func TestConcurrencyCapHonored(t *testing.T) {
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 2}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	const total = 6

	leases := make([]*Lease, 0, total)

	for i := range total {
		l, err := Acquire(ctx, sock, "default", fmt.Sprintf("j%d", i), "t")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}

		leases = append(leases, l)
	}

	t.Cleanup(func() {
		for _, l := range leases {
			l.Close()
		}
	})

	time.Sleep(100 * time.Millisecond)

	snap, _ := Status(sock)
	if len(snap.InFlight) != 2 {
		t.Fatalf("in-flight=%d want 2 (cap)", len(snap.InFlight))
	}

	if len(snap.Waiting) != total-2 {
		t.Fatalf("waiting=%d want %d", len(snap.Waiting), total-2)
	}

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

	t.Fatalf("after release: in-flight=%d waiting=%d (want 2/%d)", len(snap.InFlight), len(snap.Waiting), total-4)
}

func TestParallelAcquireAndStatusAreSafe(t *testing.T) {
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 4}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Go(func() {
			l, err := Acquire(ctx, sock, "default", fmt.Sprintf("p%d", i), "t")
			if err != nil {
				t.Errorf("acquire: %v", err)

				return
			}
			defer l.Close()

			select {
			case <-l.Granted():
			case <-ctx.Done():
				t.Errorf("never granted")
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

	snap, _ := Status(sock)
	if len(snap.InFlight)+len(snap.Waiting) != 0 {
		t.Fatalf("expected drained queue, got %+v", snap)
	}
}

func TestDaemonUnreachableErrorWraps(t *testing.T) {
	// Path that doesn't exist; client must surface ErrDaemonUnreachable.
	dir, err := os.MkdirTemp("/tmp", "idr-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "missing")

	_, err = Status(sock)
	if err == nil {
		t.Fatal("expected error against missing daemon")
	}

	if !errors.Is(err, ErrDaemonUnreachable) {
		t.Fatalf("error should wrap ErrDaemonUnreachable, got %v", err)
	}

	if got := failures.CodeOf(err); got != failures.Unavailable {
		t.Fatalf("daemon unreachable code = %v, want %v", got, failures.Unavailable)
	}
}

func TestLoadConfigDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadConfig(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatalf("missing file should be OK, got %v", err)
	}

	if len(cfg.Pools) != 1 {
		t.Fatalf("default config should have 1 pool, got %d", len(cfg.Pools))
	}
}

func TestLoadConfigParsesPools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.json")
	body := `{"pools":{"qwen-35b":{"max_in_flight":1},"qwen-coder":{"max_in_flight":2}}}`

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Pools["qwen-coder"].MaxInFlight != 2 {
		t.Fatalf("expected qwen-coder cap=2, got %+v", cfg.Pools)
	}
}

func TestLoadConfigRejectsBadCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	body := `{"pools":{"x":{"max_in_flight":0}}}`

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error on max_in_flight=0")
	}
}

func TestDefaultSocketPathHonorsEnvOverride(t *testing.T) {
	t.Setenv("IDIOM_RUNNER_SOCKET", "/tmp/explicit.sock")

	if got := DefaultSocketPath(); got != "/tmp/explicit.sock" {
		t.Fatalf("env override ignored, got %q", got)
	}
}

func TestDefaultSocketPathPrefersHomeIdiom(t *testing.T) {
	// With HOME set, the canonical path is $HOME/.idiom/runner.sock —
	// stable across launchd / shell / SSH sessions so the daemon and
	// clients always resolve the same path.
	t.Setenv("IDIOM_RUNNER_SOCKET", "")
	t.Setenv("HOME", "/var/empty/test")
	t.Setenv("XDG_RUNTIME_DIR", "/should-not-be-used")
	t.Setenv("TMPDIR", "/also-not-used")

	if got := DefaultSocketPath(); got != "/var/empty/test/.idiom/runner.sock" {
		t.Fatalf("HOME fallback wrong, got %q", got)
	}
}

func TestDefaultSocketPathFallsBackToTmp(t *testing.T) {
	t.Setenv("IDIOM_RUNNER_SOCKET", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "/var/tmp/test")

	if got := DefaultSocketPath(); got != "/var/tmp/test/idiom-runner.sock" {
		t.Fatalf("tmp fallback wrong, got %q", got)
	}
}

func TestDefaultSocketPathFallsBackToXDG(t *testing.T) {
	// Without HOME but with XDG_RUNTIME_DIR, the Linux convention wins.
	t.Setenv("IDIOM_RUNNER_SOCKET", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("TMPDIR", "/tmp")

	if got := DefaultSocketPath(); got != "/run/user/1000/idiom-runner.sock" {
		t.Fatalf("XDG fallback wrong, got %q", got)
	}
}

func TestDefaultConfigPathHonorsEnvOverride(t *testing.T) {
	t.Setenv("IDIOM_RUNNER_CONFIG", "/some/explicit.json")

	if got := DefaultConfigPath(); got != "/some/explicit.json" {
		t.Fatalf("env override ignored, got %q", got)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "idr-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "s")
	srv := NewServer(DefaultConfig(), sock)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- srv.Run(ctx)
	}()

	// Wait for the listener to bind.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestSilentClientIsTimedOut: a client that connects but never sends
// must not pin a server goroutine indefinitely. The server applies a
// short read deadline before the first action; after it expires the
// connection closes with a structured error.
func TestSilentClientIsTimedOut(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())

	conn, err := dial(sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
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
}

// TestProtocolErrorsOnGarbage gives a tour of the error channel: an
// unknown action should produce a structured error response that the
// caller can match on.
func TestProtocolErrorsOnGarbage(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())

	conn, err := dial(sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(`{"action":"bogus"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp Response

	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Error == "" {
		t.Fatal("expected error in response")
	}

	if !contains(resp.Error, "unknown action") {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// TestStatusReportsAgeAndIdleFlag verifies #81: in-flight jobs
// carry AgeSeconds and an Idle flag set when age exceeds the
// pool's IdleThresholdSeconds. The threshold defaults to
// DefaultIdleThresholdSeconds (600s); we set a tiny 1s threshold
// here and sleep briefly so the test stays fast.
func TestStatusReportsAgeAndIdleFlag(t *testing.T) {
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{
			"fast-idle": {MaxInFlight: 1, IdleThresholdSeconds: 1},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

	lease, err := Acquire(ctx, sock, "fast-idle", "first", "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.Close()

	<-lease.Granted()

	// First snapshot: just-granted, age=0 (or close), not idle.
	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if len(snap.InFlight) != 1 {
		t.Fatalf("expected 1 in-flight job, got %d", len(snap.InFlight))
	}

	if snap.InFlight[0].Idle {
		t.Fatalf("expected Idle=false at age=0, got %+v", snap.InFlight[0])
	}

	// Sleep past the threshold, snap again — should now be flagged.
	time.Sleep(1100 * time.Millisecond)

	snap, err = Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if !snap.InFlight[0].Idle {
		t.Fatalf("expected Idle=true after >1s, got %+v", snap.InFlight[0])
	}

	if snap.InFlight[0].AgeSeconds < 1 {
		t.Fatalf("expected AgeSeconds>=1, got %d", snap.InFlight[0].AgeSeconds)
	}
}

// TestStatusIdleThresholdDefaultsTo600s confirms PoolConfig with
// IdleThresholdSeconds=0 (or unset) falls back to the default
// 10-minute window — a job that just started doesn't get
// erroneously flagged as idle.
func TestStatusIdleThresholdDefaultsTo600s(t *testing.T) {
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{
			"default": {MaxInFlight: 1}, // no IdleThresholdSeconds
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	lease, err := Acquire(ctx, sock, "default", "first", "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lease.Close()

	<-lease.Granted()

	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if snap.InFlight[0].Idle {
		t.Fatalf("expected Idle=false on fresh job under default 600s threshold, got %+v", snap.InFlight[0])
	}
}
