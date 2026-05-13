package runner

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMaybeAcquireNoOpWhenDaemonAbsent(t *testing.T) {
	// No daemon = no-op. MaybeAcquire must not block or error; entry
	// points proceed un-gated. Pointing at a path that doesn't exist
	// exercises the "no socket file" branch of socketReachable.
	dir := t.TempDir()
	missing := filepath.Join(dir, "definitely-not-a-socket.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	release, err := MaybeAcquire(ctx, missing, "qwen-35b", "label", "test", nil)
	if err != nil {
		t.Fatalf("expected no error when daemon absent, got %v", err)
	}
	if release == nil {
		t.Fatalf("release must be non-nil even on no-op path")
	}
	// Must return well under the dial timeout (200ms).
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("no-op path took too long: %v", elapsed)
	}
	release() // idempotent on no-op
}

func TestMaybeAcquireGrantsImmediatelyWhenSlotFree(t *testing.T) {
	_, sock := startTestServer(t, DefaultConfig())
	var statusBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := MaybeAcquire(ctx, sock, "default", "test-label", "test-client", &statusBuf)
	if err != nil {
		t.Fatalf("MaybeAcquire: %v", err)
	}
	if release == nil {
		t.Fatalf("release must be non-nil")
	}
	// Confirm the daemon sees us in flight.
	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(snap.InFlight) != 1 {
		t.Fatalf("expected 1 in-flight job, got %d", len(snap.InFlight))
	}
	if snap.InFlight[0].Label != "test-label" {
		t.Fatalf("label not propagated: got %q", snap.InFlight[0].Label)
	}
	release()
	// After release, daemon should show no in-flight jobs.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap, err = Status(sock)
		if err != nil {
			t.Fatalf("status post-release: %v", err)
		}
		if len(snap.InFlight) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected release to drop in-flight count to 0; snap=%+v", snap)
}

func TestMaybeAcquireSerializesUnderPoolCap(t *testing.T) {
	// Two concurrent MaybeAcquire calls against a pool with
	// max_in_flight=1 must serialize: the second blocks until the
	// first's release. This is the load-bearing invariant for the
	// "two `idiom lane plan-execute` runs don't both hammer the MLX
	// backend at once" fix.
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{"default": {MaxInFlight: 1}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	firstReleased := make(chan struct{})
	secondGranted := make(chan struct{})

	// First acquire blocks the slot.
	release1, err := MaybeAcquire(ctx, sock, "default", "first", "test-1", nil)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Second acquire must wait until first releases.
	go func() {
		release2, err := MaybeAcquire(ctx, sock, "default", "second", "test-2", nil)
		if err != nil {
			return
		}
		close(secondGranted)
		release2()
	}()

	// Briefly verify second is NOT yet granted.
	select {
	case <-secondGranted:
		t.Fatalf("second acquire should not be granted while first holds the slot")
	case <-time.After(150 * time.Millisecond):
		// Expected: still waiting.
	}

	release1()
	close(firstReleased)

	select {
	case <-secondGranted:
		// Expected: second now granted post-release.
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire never got promoted after first released")
	}
}

func TestMaybeAcquireSkipsWhenAncestorHoldsPool(t *testing.T) {
	// Reentrancy guard: when IDIOM_QUEUE_HELD_POOLS lists the pool,
	// MaybeAcquire returns a no-op release without dialing. This is
	// the load-bearing behavior that prevents `idiom queue run`
	// self-deadlock when the wrapped command invokes another idiom
	// subcommand that itself calls MaybeAcquire on the same pool.
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{"qwen-coder": {MaxInFlight: 1}},
	})
	// Hold the slot via a direct Acquire to simulate an ancestor's
	// outstanding lease.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	parent, err := Acquire(ctx, sock, "qwen-coder", "parent", "test-parent")
	if err != nil {
		t.Fatalf("parent Acquire: %v", err)
	}
	defer parent.Close()
	<-parent.Granted()

	// Child sees IDIOM_QUEUE_HELD_POOLS in env and must NOT dial.
	t.Setenv(HeldPoolsEnv, "qwen-coder")
	childCtx, childCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer childCancel()
	start := time.Now()
	release, err := MaybeAcquire(childCtx, sock, "qwen-coder", "child", "test-child", nil)
	if err != nil {
		t.Fatalf("child MaybeAcquire: %v", err)
	}
	// Sub-millisecond return; if it tried to dial the busy pool it
	// would block until the context cancels (~500ms) or longer.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("reentrant skip took too long (%v); did it actually dial?", elapsed)
	}
	release()

	// The parent's slot is still held — confirms the child did NOT
	// touch the daemon's counters.
	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(snap.InFlight) != 1 || snap.InFlight[0].Label != "parent" {
		t.Fatalf("expected only parent in flight, got %+v", snap.InFlight)
	}
	if len(snap.Waiting) != 0 {
		t.Fatalf("child must not appear as a waiter; got %+v", snap.Waiting)
	}
}

func TestMaybeAcquireSkipsCrossPoolNestedAcquire(t *testing.T) {
	// Cross-pool nested acquire: IDIOM_QUEUE_HELD_POOLS lists pool
	// A; nested call requests pool B. The previous behavior was to
	// acquire pool B normally, but that created a dining-philosophers
	// deadlock when two sessions each held one pool and waited for
	// the other (observed 2026-05-10: Colony held qwen-35b waiting on
	// qwen-coder; idiom held qwen-coder waiting on qwen-35b).
	// Current behavior: skip the nested acquire — the ancestor's
	// outer lease is enough serialization for this session, and
	// avoiding the daemon entirely cannot deadlock.
	_, sock := startTestServer(t, Config{
		Pools: map[string]PoolConfig{
			"qwen-35b":   {MaxInFlight: 1},
			"qwen-coder": {MaxInFlight: 1},
		},
	})
	t.Setenv(HeldPoolsEnv, "qwen-35b")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	release, err := MaybeAcquire(ctx, sock, "qwen-coder", "nested", "test-nested", nil)
	if err != nil {
		t.Fatalf("MaybeAcquire on cross-pool nested: %v", err)
	}
	defer release()
	// Sub-millisecond return; if it tried to dial the daemon it
	// would take longer than 50ms even on a localhost socket.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cross-pool reentrant skip took too long (%v); did it actually dial?", elapsed)
	}
	snap, err := Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// The daemon must show ZERO in-flight — the nested call did not
	// acquire qwen-coder. Pool accounting is intentionally permissive
	// for nested calls; the outer wrapper is responsible for picking
	// the right pool.
	if len(snap.InFlight) != 0 {
		t.Fatalf("expected zero in-flight (nested call must not acquire), got %+v", snap.InFlight)
	}
	if len(snap.Waiting) != 0 {
		t.Fatalf("nested call must not appear as a waiter; got %+v", snap.Waiting)
	}
}

func TestPoolHeldByAncestorParsesColonList(t *testing.T) {
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
		if got != c.want {
			t.Errorf("PoolHeldByAncestor(%q, %q) = %v, want %v", c.held, c.pool, got, c.want)
		}
	}
}

func TestPoolForModelAliasMapping(t *testing.T) {
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
		if got != c.want {
			t.Errorf("PoolForModelAlias(%q) = %q, want %q", c.alias, got, c.want)
		}
	}
}
