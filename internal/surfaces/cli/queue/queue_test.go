package queue

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/runtime/runner"
)

// startDaemonForCLI spins up an in-process runner daemon bound to a
// short Unix socket path (macOS sun_path cap) and returns the path
// for CLI tests to point at via --socket. Cleanup unbinds the socket
// when the test ends.
func startDaemonForCLI(t *testing.T, cfg runner.Config) string {
	t.Helper()

	if cfg.Pools == nil {
		cfg = runner.DefaultConfig()
	}

	dir, err := os.MkdirTemp("/tmp", "idr-cli-")
	if err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "s")
	srv := runner.NewServer(cfg, sock)

	if err := srv.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	t.Cleanup(srv.Stop)

	return sock
}

func TestQueueUsageOnNoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{}, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("usage exit code = %d, want 0", code)
	}

	if !strings.Contains(stderr.String(), "subcommands:") {
		t.Fatalf("usage missing subcommands: %s", stderr.String())
	}
}

func TestQueuePingHitsLiveDaemon(t *testing.T) {
	sock := startDaemonForCLI(t, runner.DefaultConfig())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"ping", "--socket", sock}, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("ping exit=%d stderr=%s", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "ok (uptime") {
		t.Fatalf("expected 'ok (uptime ...)' in stdout, got %q", stdout.String())
	}
}

func TestQueuePingFailsWithoutDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "idr-cli-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	missing := filepath.Join(dir, "missing.sock")

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"ping", "--socket", missing}, &stdout, &stderr, nil)

	if code == 0 {
		t.Fatalf("expected nonzero exit, got 0; stderr=%s", stderr.String())
	}
}

func TestQueueStatusJSONShape(t *testing.T) {
	sock := startDaemonForCLI(t, runner.Config{
		Pools: map[string]runner.PoolConfig{
			"qwen-35b":   {MaxInFlight: 1},
			"qwen-coder": {MaxInFlight: 2},
		},
	})

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"status", "--socket", sock, "--json"}, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{`"pools"`, `"qwen-35b"`, `"qwen-coder"`, `"max_in_flight": 2`} {
		if !strings.Contains(out, want) {
			t.Fatalf("status JSON missing %q: %s", want, out)
		}
	}
}

func TestQueueStatusTableShape(t *testing.T) {
	sock := startDaemonForCLI(t, runner.DefaultConfig())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"status", "--socket", sock}, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "POOL") {
		t.Fatalf("table header missing: %s", stdout.String())
	}

	if !strings.Contains(stdout.String(), "default") {
		t.Fatalf("default pool row missing: %s", stdout.String())
	}
}

func TestQueueRunRequiresCommand(t *testing.T) {
	sock := startDaemonForCLI(t, runner.DefaultConfig())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"run", "--socket", sock}, &stdout, &stderr, nil)

	if code != 2 {
		t.Fatalf("expected exit 2 for missing command, got %d", code)
	}

	if !strings.Contains(stderr.String(), "command required") {
		t.Fatalf("error message wrong: %s", stderr.String())
	}
}

func TestQueueRunExecutesUnderSlot(t *testing.T) {
	sock := startDaemonForCLI(t, runner.DefaultConfig())

	var stdout, stderr bytes.Buffer

	// `true` exits 0; success means we acquired a slot and ran the
	// command. The granted-slot side effect is observable via status,
	// but a clean exit code already proves the path: dial → enqueue
	// → granted → exec → release.
	code := RunMainWithTelemetry([]string{
		"run", "--socket", sock, "--quiet",
		"--", "/usr/bin/true",
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("run /usr/bin/true exit=%d stderr=%s", code, stderr.String())
	}
}

func TestQueueRunPropagatesExitCode(t *testing.T) {
	sock := startDaemonForCLI(t, runner.DefaultConfig())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{
		"run", "--socket", sock, "--quiet",
		"--", "/bin/sh", "-c", "exit 7",
	}, &stdout, &stderr, nil)

	if code != 7 {
		t.Fatalf("expected exact child exit code 7, got %d; stderr=%s", code, stderr.String())
	}
}

func TestQueueRunStreamsChildStdinStdoutAndStderr(t *testing.T) {
	sock := startDaemonForCLI(t, runner.DefaultConfig())

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_, _ = writeEnd.WriteString("payload\n")
	_ = writeEnd.Close()

	origStdin := os.Stdin
	os.Stdin = readEnd
	t.Cleanup(func() {
		os.Stdin = origStdin
		_ = readEnd.Close()
	})

	var stdout, stderr bytes.Buffer
	code := RunMainWithTelemetry([]string{
		"run", "--socket", sock, "--quiet",
		"--", "/bin/sh", "-c", `read value; printf 'out=%s\n' "$value"; printf 'err=%s\n' "$value" >&2`,
	}, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("queue run exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "out=payload") {
		t.Fatalf("stdout missing child output: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "err=payload") {
		t.Fatalf("stderr missing child output: %q", stderr.String())
	}
}

func TestQueueRunReleasesSlotAfterExit(t *testing.T) {
	sock := startDaemonForCLI(t, runner.Config{
		Pools: map[string]runner.PoolConfig{"default": {MaxInFlight: 1}},
	})
	// Run two sequential `true` invocations; if the first didn't
	// release, the second would block (timeout-fail this test) since
	// max_in_flight=1.
	for i := range 2 {
		var stdout, stderr bytes.Buffer

		done := make(chan int, 1)

		go func() {
			done <- RunMainWithTelemetry([]string{
				"run", "--socket", sock, "--quiet",
				"--", "/usr/bin/true",
			}, &stdout, &stderr, nil)
		}()

		select {
		case code := <-done:
			if code != 0 {
				t.Fatalf("iter %d exit=%d stderr=%s", i, code, stderr.String())
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("iter %d timed out — slot likely not released", i)
		}
	}

	// Final state: clean queue.
	snap, err := runner.Status(sock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if len(snap.InFlight)+len(snap.Waiting) != 0 {
		t.Fatalf("expected empty queue after both runs, got %+v", snap)
	}
}

func TestQueueCancelOnUnknownIDFails(t *testing.T) {
	sock := startDaemonForCLI(t, runner.DefaultConfig())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"cancel", "--socket", sock, "j-does-not-exist"}, &stdout, &stderr, nil)

	if code == 0 {
		t.Fatalf("expected nonzero exit, got 0")
	}

	if !strings.Contains(stderr.String(), "unknown job_id") {
		t.Fatalf("error should mention unknown job_id: %s", stderr.String())
	}
}

// TestQueueRunExportsHeldPoolsEnvForChildren verifies that
// `idiom queue run --pool X -- <cmd>` sets IDIOM_QUEUE_HELD_POOLS=X
// in the child's environment. Without this, a child that calls
// runner.MaybeAcquire on the same pool self-deadlocks (see
// BUG_QUEUE_DUAL_ACQUIRE_DEADLOCK.md for the original repro).
func TestQueueRunExportsHeldPoolsEnvForChildren(t *testing.T) {
	sock := startDaemonForCLI(t, runner.Config{
		Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}},
	})
	// Child writes its IDIOM_QUEUE_HELD_POOLS to a file we read back.
	outPath := filepath.Join(t.TempDir(), "held.txt")
	args := []string{
		"run", "--socket", sock, "--pool", "qwen-coder", "--quiet",
		"--label", "parent",
		"--", "/bin/sh", "-c", `printenv IDIOM_QUEUE_HELD_POOLS > "$0"`, outPath,
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(args, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("queue run exit=%d stderr=%s", code, stderr.String())
	}

	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read child output: %v", err)
	}

	got := strings.TrimSpace(string(body))
	if got != "qwen-coder" {
		t.Fatalf("child saw IDIOM_QUEUE_HELD_POOLS=%q, want %q", got, "qwen-coder")
	}
}

func TestQueueRunAppendsHeldPoolForNestedAncestor(t *testing.T) {
	// Outer ancestor (simulated via direct env) holds pool A.
	// `idiom queue run --pool B -- <cmd>` runs WITHIN that ancestor.
	// The child should see IDIOM_QUEUE_HELD_POOLS=A:B — the existing
	// ancestor pool is preserved and the new pool is appended,
	// keeping chained wrappers composable.
	sock := startDaemonForCLI(t, runner.Config{
		Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}},
	})
	t.Setenv(runner.HeldPoolsEnv, "qwen-35b")

	outPath := filepath.Join(t.TempDir(), "held.txt")
	args := []string{
		"run", "--socket", sock, "--pool", "qwen-coder", "--quiet",
		"--label", "nested",
		"--", "/bin/sh", "-c", `printenv IDIOM_QUEUE_HELD_POOLS > "$0"`, outPath,
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(args, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("queue run exit=%d stderr=%s", code, stderr.String())
	}

	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read child output: %v", err)
	}

	got := strings.TrimSpace(string(body))
	if got != "qwen-35b:qwen-coder" {
		t.Fatalf("child saw IDIOM_QUEUE_HELD_POOLS=%q, want qwen-35b:qwen-coder", got)
	}
}

func TestQueueRunSkipsDuplicateHeldPool(t *testing.T) {
	// If the same pool is already in IDIOM_QUEUE_HELD_POOLS (e.g.
	// `idiom queue run --pool X` inside an env that already declares
	// X is held), don't append a duplicate. Keeps the env value
	// bounded under chained wrappers and ensures
	// PoolHeldByAncestor's first-match works either way.
	sock := startDaemonForCLI(t, runner.Config{
		Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}},
	})
	t.Setenv(runner.HeldPoolsEnv, "qwen-coder")

	outPath := filepath.Join(t.TempDir(), "held.txt")
	args := []string{
		"run", "--socket", sock, "--pool", "qwen-coder", "--quiet",
		"--label", "dup",
		"--", "/bin/sh", "-c", `printenv IDIOM_QUEUE_HELD_POOLS > "$0"`, outPath,
	}

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(args, &stdout, &stderr, nil)

	if code != 0 {
		t.Fatalf("queue run exit=%d stderr=%s", code, stderr.String())
	}

	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read child output: %v", err)
	}

	got := strings.TrimSpace(string(body))
	if got != "qwen-coder" {
		t.Fatalf("child saw IDIOM_QUEUE_HELD_POOLS=%q, want qwen-coder (no dup)", got)
	}
}
