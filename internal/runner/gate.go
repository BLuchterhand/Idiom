package runner

import (
	"context"
	"errors"
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
// Reentrancy: if the IDIOM_QUEUE_HELD_POOLS env var lists the target
// pool, an ancestor process already holds a lease in that pool. We
// return immediately with a no-op release rather than re-acquiring,
// which would deadlock on single-cap pools.
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
	// Reentrant short-circuit: if an ancestor process already holds
	// a lease in this pool, return a no-op release immediately. This
	// is what prevents `idiom queue run -- <wrapper>` from
	// self-deadlocking when the wrapper invokes another idiom
	// subcommand that itself calls MaybeAcquire on the same pool.
	if PoolHeldByAncestor(os.Getenv(HeldPoolsEnv), pool) {
		if statusOut != nil {
			fmt.Fprintf(statusOut, "[idiom queue %s] ancestor holds slot; nested acquire is no-op\n", pool)
		}
		return func() {}, nil
	}
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	if !socketReachable(socketPath) {
		return func() {}, nil
	}
	lease, err := Acquire(ctx, socketPath, pool, label, client)
	if err != nil {
		// Reachable but acquire failed (unknown pool, daemon error
		// mid-handshake). Surface so the caller can decide whether to
		// fail loud or proceed un-gated; current callers proceed.
		return func() {}, err
	}
	for {
		select {
		case <-lease.Granted():
			closer := lease
			return func() { _ = closer.Close() }, nil
		case pos := <-lease.Updates():
			if statusOut != nil {
				fmt.Fprintf(statusOut, "[idiom queue %s] waiting at position %d\n", lease.Pool, pos)
			}
		case e := <-lease.Err():
			_ = lease.Close()
			if e != nil {
				return func() {}, e
			}
			return func() {}, errors.New("queue connection closed before grant")
		case <-ctx.Done():
			_ = lease.Close()
			return func() {}, ctx.Err()
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
// the executor pool ("qwen-coder"); anything containing "35b" or
// "planner" routes to the planner pool ("qwen-35b"); empty result
// means the caller should fall back to "default".
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
	if strings.Contains(a, "35b") || strings.Contains(a, "planner") {
		return "qwen-35b"
	}
	return ""
}
