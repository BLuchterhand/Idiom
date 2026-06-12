package runner

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// HeldPoolsEnv names the env var that carries the colon-separated
// set of queue pools an ancestor process already holds a lease in.
// Set by `idiom queue run` before execing its child; consulted by
// MaybeAcquire to short-circuit nested same-pool acquires.
//
// Without this, the canonical wrapper pattern (`idiom queue run
// --pool X -- <wrapper>` where the wrapper invokes another idiom
// subcommand that itself calls MaybeAcquire on the same pool)
// self-deadlocks on single-cap pools: the parent's lease prevents
// the child from acquiring, and the parent waits for the child to
// finish. See BUG_QUEUE_DUAL_ACQUIRE_DEADLOCK.md for the original
// repro.
const HeldPoolsEnv = "IDIOM_QUEUE_HELD_POOLS"

type MaybeAcquireState string

const (
	MaybeAcquireStateAcquired    MaybeAcquireState = "acquired"
	MaybeAcquireStateUnavailable MaybeAcquireState = "unavailable"
	MaybeAcquireStateDegraded    MaybeAcquireState = "degraded"
	MaybeAcquireStateError       MaybeAcquireState = "error"
)

type MaybeAcquireResult struct {
	State   MaybeAcquireState
	Pool    string
	Reason  string
	Release func()
	Err     error
}

// PoolHeldByAncestor reports whether the given colon-separated
// HeldPoolsEnv value contains the named pool. Treats empty entries
// and whitespace-only entries as absent. Caller-side helper so the
// queue-run wrapper can dedupe before appending a fresh pool.
func PoolHeldByAncestor(heldPools, pool string) bool {
	pool = strings.TrimSpace(pool)
	if pool == "" {
		return false
	}

	for _, entry := range strings.Split(heldPools, ":") {
		if strings.TrimSpace(entry) == pool {
			return true
		}
	}

	return false
}

// MaybeAcquire is the best-effort wrapper around Acquire used by
// entry-point dispatch (idiom plan-execute / implement / lane). If
// the queue daemon isn't running OR no socket configured, it returns
// a no-op release function and nil error — the caller proceeds
// un-gated, matching legacy behavior. If the daemon IS reachable, it
// blocks until a lease is granted; the returned release function
// closes the lease.
//
// Reentrancy: if the IDIOM_QUEUE_HELD_POOLS env var lists ANY pool
// (the same one OR a different one), an ancestor process already
// holds a lease somewhere. We return immediately with a no-op release
// rather than re-acquiring. The same-pool case prevents
// self-deadlock on single-cap pools; the cross-pool case prevents
// the dining-philosophers deadlock where two sessions each hold one
// pool and wait for the other (observed 2026-05-10: Colony held
// qwen-35b waiting on qwen-coder; idiom held qwen-coder waiting on
// qwen-35b — neither could progress).
//
// socketPath empty → DefaultSocketPath() (XDG_RUNTIME_DIR or TMPDIR).
// pool empty → "default".
// label is a human-readable tag (atom title, session id) surfaced by
// `idiom queue status`. client identifies the caller (process + pid).
// statusOut, if non-nil, receives one line per position update while
// the caller is queued; pass nil to discard.
//
// The dial-with-timeout reachability check is sub-second; an offline
// daemon never blocks the caller. This is intentional: the gate is
// opportunistic — if you want a hard "queue is required" contract,
// run `idiom queue ping` separately first.
func MaybeAcquire(ctx context.Context, socketPath, pool, label, client string, statusOut io.Writer) (release func(), err error) {
	result := MaybeAcquireWithResult(ctx, socketPath, pool, label, client, statusOut)
	return result.Release, result.Err
}

func MaybeAcquireWithResult(ctx context.Context, socketPath, pool, label, client string, statusOut io.Writer) MaybeAcquireResult {
	// Reentrant short-circuit: if an ancestor process already holds
	// a lease in ANY pool, return a no-op release immediately.
	heldPools := os.Getenv(HeldPoolsEnv)
	if PoolHeldByAncestor(heldPools, pool) {
		if statusOut != nil {
			fmt.Fprintf(statusOut, "[idiom queue %s] ancestor holds slot in same pool; nested acquire is no-op\n", pool)
		}

		return MaybeAcquireResult{State: MaybeAcquireStateDegraded, Pool: pool, Reason: "nested_same_pool", Release: func() {}}
	}

	if strings.TrimSpace(heldPools) != "" {
		if statusOut != nil {
			fmt.Fprintf(statusOut, "[idiom queue %s] ancestor holds slot in different pool(s) (%s); skipping nested acquire to prevent cross-pool deadlock — wrap the outer command with the pool the inner inference actually uses\n", pool, heldPools)
		}

		return MaybeAcquireResult{State: MaybeAcquireStateDegraded, Pool: pool, Reason: "nested_cross_pool", Release: func() {}}
	}

	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}

	if !socketReachable(socketPath) {
		return MaybeAcquireResult{State: MaybeAcquireStateUnavailable, Pool: pool, Reason: "daemon_unreachable", Release: func() {}}
	}

	lease, err := Acquire(ctx, socketPath, pool, label, client)
	if err != nil {
		// Reachable but acquire failed (unknown pool, daemon error
		// mid-handshake). Surface so the caller can decide whether to
		// fail loud or proceed un-gated; current callers proceed.
		return MaybeAcquireResult{State: MaybeAcquireStateError, Pool: pool, Reason: err.Error(), Release: func() {}, Err: err}
	}

	for {
		select {
		case <-lease.Granted():
			closer := lease
			return MaybeAcquireResult{State: MaybeAcquireStateAcquired, Pool: lease.Pool, Release: func() { _ = closer.Close() }}
		case pos := <-lease.Updates():
			if statusOut != nil {
				fmt.Fprintf(statusOut, "[idiom queue %s] waiting at position %d\n", lease.Pool, pos)
			}
		case e := <-lease.Err():
			_ = lease.Close()
			if e != nil {
				return MaybeAcquireResult{State: MaybeAcquireStateError, Pool: lease.Pool, Reason: e.Error(), Release: func() {}, Err: e}
			}

			err := queueConnectionClosedError()

			return MaybeAcquireResult{State: MaybeAcquireStateError, Pool: lease.Pool, Reason: err.Error(), Release: func() {}, Err: err}
		case <-ctx.Done():
			_ = lease.Close()

			err := ctx.Err()

			return MaybeAcquireResult{State: MaybeAcquireStateError, Pool: lease.Pool, Reason: err.Error(), Release: func() {}, Err: err}
		}
	}
}

// socketReachable returns true when a Unix socket exists at path AND
// a daemon is actively listening (not a stale leftover file). The
// 200ms dial timeout keeps the gate sub-second on the no-daemon path.
func socketReachable(socketPath string) bool {
	if socketPath == "" {
		return false
	}

	if _, err := os.Stat(socketPath); err != nil {
		return false
	}

	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

// PoolForModelAlias maps a resolved llama_server model alias to the
// queue pool name. Heuristic: anything containing "coder" routes to
// the executor pool ("qwen-coder"); explicit local planner sizes route
// to their matching pool ("qwen-27b" / "qwen-35b"); empty result means
// the caller should fall back to "default".
//
// This mirrors the conventional pool layout in ~/.idiom/runner.json
// for projects running the qwen-pair backend profile. Projects with
// different pool names override via Workspace config or the
// IDIOM_QUEUE_POOL env var (consulted by the entry-point caller, not
// this helper).
func PoolForModelAlias(alias string) string {
	a := strings.ToLower(strings.TrimSpace(alias))
	if a == "" {
		return ""
	}

	if strings.Contains(a, "coder") {
		return "qwen-coder"
	}

	// Check the nothink variant FIRST — both contain "35b" so the
	// broader match below would otherwise capture nothink aliases
	// into qwen-35b (the thinking pool), causing the cross-pool
	// reentrancy guard to fire when the outer wrap targets
	// qwen-35b-nothink. Subset-of-superset ordering is load-bearing.
	if strings.Contains(a, "nothink") {
		return "qwen-35b-nothink"
	}

	if strings.Contains(a, "27b") {
		return "qwen-27b"
	}

	if strings.Contains(a, "35b") || strings.Contains(a, "planner") {
		return "qwen-35b"
	}

	return ""
}
