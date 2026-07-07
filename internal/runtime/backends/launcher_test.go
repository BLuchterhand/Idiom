package backends

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBuildLaunchSpecMLXCanonical is the canonical happy path: a
// fully-populated Model produces the argv shape Colony's
// start_local_qwen.sh emits today, with cache/token flags only
// when the schema field is non-zero.
func TestBuildLaunchSpecMLXCanonical(t *testing.T) {
	m := Model{
		Alias:      "qwen3-coder-30b",
		Binary:     "mlx_lm.server",
		ModelPath:  "/abs/models/qwen3-coder-30b",
		Port:       47781,
		CacheSize:  4,
		CacheBytes: 21474836480,
		MaxTokens:  8192,
		ExtraArgs:  []string{"--temp", "0.7"},
	}

	spec, err := BuildLaunchSpec(m)
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}

	want := []string{
		"mlx_lm.server",
		"--model", "/abs/models/qwen3-coder-30b",
		"--host", "127.0.0.1",
		"--port", "47781",
		"--prompt-cache-size", "4",
		"--prompt-cache-bytes", "21474836480",
		"--max-tokens", "8192",
		"--temp", "0.7",
	}
	if len(spec.Argv) != len(want) {
		t.Fatalf("argv length mismatch: got %v\nwant %v", spec.Argv, want)
	}

	for i := range spec.Argv {
		if spec.Argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full: %v)", i, spec.Argv[i], want[i], spec.Argv)
		}
	}
}

func TestPIDForPortViaLsofParsesFirstPID(t *testing.T) {
	writeFakeLsof(t, "printf '4242\\n9999\\n'\n")

	pid, err := pidForPortViaLsof(47781)
	if err != nil {
		t.Fatalf("pidForPortViaLsof: %v", err)
	}
	if pid != 4242 {
		t.Fatalf("pid = %d, want 4242", pid)
	}
}

func TestPIDForPortViaLsofExitOneMeansNoListener(t *testing.T) {
	writeFakeLsof(t, "exit 1\n")

	pid, err := pidForPortViaLsof(47781)
	if err != nil {
		t.Fatalf("pidForPortViaLsof: %v", err)
	}
	if pid != 0 {
		t.Fatalf("pid = %d, want 0", pid)
	}
}

func TestPIDForPortViaLsofMissingBinaryUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	pid, err := pidForPortViaLsof(47781)
	if !errors.Is(err, errLsofUnavailable) {
		t.Fatalf("err = %v, want errLsofUnavailable", err)
	}
	if pid != 0 {
		t.Fatalf("pid = %d, want 0", pid)
	}
}

func TestPIDForPortViaLsofInvalidPIDReportsParseError(t *testing.T) {
	writeFakeLsof(t, "printf 'not-a-pid\\n'\n")

	_, err := pidForPortViaLsof(47781)
	if err == nil || !strings.Contains(err.Error(), "parse lsof output") {
		t.Fatalf("err = %v, want parse error", err)
	}
}

func writeFakeLsof(t *testing.T, body string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("fake lsof fixture uses a POSIX shell script")
	}

	binDir := t.TempDir()
	path := filepath.Join(binDir, "lsof")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake lsof: %v", err)
	}
	t.Setenv("PATH", binDir)
}

func TestBuildLaunchSpecVmlxIncludesRequestTimeout(t *testing.T) {
	m := Model{
		Alias:                 "qwen3.6-35b",
		Binary:                "/usr/local/bin/vmlx-idiom-launch.sh",
		ModelPath:             "/abs/models/qwen3.6-35b",
		Port:                  47780,
		MaxTokens:             8192,
		RequestTimeoutSeconds: 900,
	}

	spec, err := BuildLaunchSpec(m)
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}

	for i, arg := range spec.Argv {
		if arg == "--timeout" && i+1 < len(spec.Argv) {
			if spec.Argv[i+1] != "900" {
				t.Fatalf("--timeout = %q, want 900", spec.Argv[i+1])
			}

			return
		}
	}

	t.Fatalf("vmlx launcher should receive --timeout, got argv %v", spec.Argv)
}

func TestBuildLaunchSpecDirectVmlxUsesServeAndNativeFlags(t *testing.T) {
	m := Model{
		Alias:                 "minimax-m2.7-jang",
		Binary:                "/usr/local/bin/vmlx",
		ModelPath:             "/abs/models/minimax-m2.7-jang",
		Port:                  47784,
		CacheSize:             4,
		CacheBytes:            12884901888,
		MaxTokens:             8192,
		RequestTimeoutSeconds: 900,
		ExtraArgs:             []string{"--served-model-name", "minimax-m2.7-jang"},
	}

	spec, err := BuildLaunchSpec(m)
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}

	want := []string{
		"/usr/local/bin/vmlx",
		"serve", "/abs/models/minimax-m2.7-jang",
		"--host", "127.0.0.1",
		"--port", "47784",
		"--prefix-cache-size", "4",
		"--prefix-cache-max-bytes", "12884901888",
		"--max-tokens", "8192",
		"--timeout", "900",
		"--served-model-name", "minimax-m2.7-jang",
	}
	if len(spec.Argv) != len(want) {
		t.Fatalf("argv length mismatch: got %v\nwant %v", spec.Argv, want)
	}

	for i := range spec.Argv {
		if spec.Argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full: %v)", i, spec.Argv[i], want[i], spec.Argv)
		}
	}
}

func TestBuildLaunchSpecMlxServerOmitsRequestTimeout(t *testing.T) {
	m := Model{
		Alias:                 "qwen3.6-35b",
		Binary:                "mlx_lm.server",
		ModelPath:             "/abs/models/qwen3.6-35b",
		Port:                  47780,
		RequestTimeoutSeconds: 900,
	}

	spec, err := BuildLaunchSpec(m)
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}

	for _, arg := range spec.Argv {
		if arg == "--timeout" {
			t.Fatalf("raw mlx_lm.server must not receive unsupported --timeout: %v", spec.Argv)
		}
	}
}

// TestBuildLaunchSpecOmitsZeroFlags verifies the contract: schema
// fields left at the zero value don't produce argv entries. A
// non-MLX launcher that doesn't recognize cache flags shouldn't
// be given them just because the field exists in the struct.
func TestBuildLaunchSpecOmitsZeroFlags(t *testing.T) {
	m := Model{Alias: "minimal", Binary: "some-server", ModelPath: "/m", Port: 8080}

	spec, err := BuildLaunchSpec(m)
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}

	for _, flag := range []string{"--prompt-cache-size", "--prompt-cache-bytes", "--max-tokens", "--timeout"} {
		for _, arg := range spec.Argv {
			if arg == flag {
				t.Fatalf("zero field should omit %s, got argv %v", flag, spec.Argv)
			}
		}
	}
}

// TestBuildLaunchSpecExpandsHomeTilde verifies the local-developer
// convenience: a `~/models/foo` path resolves to the user's home
// directory before being passed to the launcher. Other tilde
// forms (`~user/...`) are intentionally unsupported.
func TestBuildLaunchSpecExpandsHomeTilde(t *testing.T) {
	m := Model{Alias: "tilde", Binary: "x", ModelPath: "~/models/foo", Port: 1}

	spec, err := BuildLaunchSpec(m)
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}

	for i, a := range spec.Argv {
		if a == "--model" && i+1 < len(spec.Argv) {
			if strings.HasPrefix(spec.Argv[i+1], "~") {
				t.Fatalf("tilde not expanded; got %q", spec.Argv[i+1])
			}

			if !strings.HasSuffix(spec.Argv[i+1], "models/foo") {
				t.Fatalf("expanded path looks wrong: %q", spec.Argv[i+1])
			}

			return
		}
	}

	t.Fatalf("no --model flag in argv: %v", spec.Argv)
}

// TestBuildLaunchSpecRejectsEmptyBinary verifies the schema
// validation surface: an empty `binary` is a configuration error
// the operator must fix, not something to silently work around.
func TestBuildLaunchSpecRejectsEmptyBinary(t *testing.T) {
	_, err := BuildLaunchSpec(Model{Alias: "bad", Port: 1234})
	if err == nil {
		t.Fatalf("empty binary must error")
	}

	if !strings.Contains(err.Error(), "empty binary") {
		t.Fatalf("error should name the field: %v", err)
	}
}

// TestBuildLaunchSpecRejectsInvalidPort verifies the same for
// invalid ports — zero and negative aren't reachable, so the
// schema must reject them before we waste a spawn attempt.
func TestBuildLaunchSpecRejectsInvalidPort(t *testing.T) {
	_, err := BuildLaunchSpec(Model{Alias: "bad", Binary: "x", Port: 0})
	if err == nil {
		t.Fatalf("zero port must error")
	}
}

// TestBuildLaunchSpecHonorsHostOverride verifies the Host field
// reaches the spawned --host flag. Without this, multi-machine
// setups (where mlx_lm.server runs on a different host) would bind
// to 127.0.0.1 and silently be unreachable from the proxy.
func TestBuildLaunchSpecHonorsHostOverride(t *testing.T) {
	spec, err := BuildLaunchSpec(Model{Alias: "x", Binary: "b", ModelPath: "/m", Host: "10.0.0.4", Port: 9000})
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}

	for i, a := range spec.Argv {
		if a == "--host" && i+1 < len(spec.Argv) {
			if spec.Argv[i+1] != "10.0.0.4" {
				t.Fatalf("--host should reflect Model.Host; got %q", spec.Argv[i+1])
			}

			return
		}
	}

	t.Fatalf("no --host flag in argv: %v", spec.Argv)
}

// TestLaunchSpawnsEachModelInProfile is the integration test for
// the whole-profile spawn: the stub spawner records each call,
// and Launch returns one result per model in declaration order.
func TestLaunchSpawnsEachModelInProfile(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "a", Binary: "x", Port: 1001},
		{Alias: "b", Binary: "y", Port: 1002},
	}}

	var spawns int32

	opts := LauncherOptions{
		LogDir: t.TempDir(),
		Spawner: func(spec LaunchSpec, _ string) (int, error) {
			atomic.AddInt32(&spawns, 1)

			return int(1000 + spec.Port), nil
		},
		PortReady: func(port int, _ time.Time) bool {
			// The pre-spawn "is the port already in use" probe
			// returns false; the post-spawn ready probe returns
			// true. The simplest stub: always return true for
			// post-spawn but false for pre-spawn — distinguish
			// by deadline distance.
			return port > 0 && time.Until(time.Unix(0, 0)) < 0 // post-spawn deadline is in the future
		},
		ReadyTimeout: 100 * time.Millisecond,
	}
	// Override PortReady to differentiate pre/post: pre-spawn check
	// uses a near-future deadline (50ms); post-spawn uses
	// ReadyTimeout. Use a counter instead.
	var probes int32

	opts.PortReady = func(port int, deadline time.Time) bool {
		n := atomic.AddInt32(&probes, 1)
		// Odd-numbered probes are the pre-spawn "already running"
		// check; we want them to return false. Even-numbered
		// probes are the post-spawn ready check; return true.
		return n%2 == 0
	}

	res := Launch(prof, opts)

	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}

	if got := atomic.LoadInt32(&spawns); got != 2 {
		t.Fatalf("expected 2 spawns, got %d", got)
	}

	for i, r := range res {
		if !r.Ready {
			t.Fatalf("result[%d] should be ready: %+v", i, r)
		}

		if r.PID == 0 {
			t.Fatalf("result[%d] should have PID: %+v", i, r)
		}
	}
}

// TestLaunchDetectsAlreadyRunningPort verifies the "port already
// in use" early-out: when something already listens on the port,
// Launch reports it as already-running rather than spawning a
// duplicate that would fail to bind.
func TestLaunchDetectsAlreadyRunningPort(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "a", Binary: "x", Port: 1234}}}

	var spawnCalled bool

	opts := LauncherOptions{
		LogDir:       t.TempDir(),
		ReadyTimeout: 100 * time.Millisecond,
		Spawner: func(LaunchSpec, string) (int, error) {
			spawnCalled = true
			return 1, nil
		},
		PortReady: func(int, time.Time) bool { return true }, // already up
	}
	res := Launch(prof, opts)

	if spawnCalled {
		t.Fatalf("Launch must not spawn when the port is already bound")
	}

	if len(res) != 1 || !res[0].Ready {
		t.Fatalf("expected one ready result for the already-running port: %+v", res)
	}

	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "already") {
		t.Fatalf("Err should explain the early-out: %+v", res[0])
	}
}

// TestLaunchPropagatesSpawnFailures verifies the partial-failure
// path: a spawn failure on one model doesn't halt the others —
// each result independently carries its own error so the
// operator sees every failure in one report.
func TestLaunchPropagatesSpawnFailures(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "a", Binary: "x", Port: 1001},
		{Alias: "b", Binary: "y", Port: 1002},
	}}
	opts := LauncherOptions{
		LogDir: t.TempDir(),
		Spawner: func(spec LaunchSpec, _ string) (int, error) {
			if spec.Alias == "a" {
				return 0, errors.New("synthetic")
			}

			return 42, nil
		},
		PortReady:    func(int, time.Time) bool { return false },
		ReadyTimeout: 50 * time.Millisecond,
	}
	res := Launch(prof, opts)

	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}

	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "synthetic") {
		t.Fatalf("first result should carry spawn error: %+v", res[0])
	}

	if res[1].Err != nil {
		t.Fatalf("second result should succeed despite first's failure: %+v", res[1])
	}
}

// TestStopSignalsListenerWithSIGTERMThenSIGKILL is the canonical
// stop path: the process listening on the port gets SIGTERM
// first, and after the grace timeout SIGKILL if it's still
// bound. This covers the most common case (cleanly stoppable
// process) and the stuck-process case in one test by toggling
// the PID lookup mid-test.
func TestStopSignalsListenerWithSIGTERMThenSIGKILL(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "stuck", Binary: "x", Port: 9999}}}

	var sigsSent []Signal

	opts := StopOptions{
		PIDForPort:   func(int) (int, error) { return 4242, nil },
		IsAlive:      func(int) bool { return true }, // simulate a stuck process → forces SIGKILL
		Kill:         func(_ int, sig Signal) error { sigsSent = append(sigsSent, sig); return nil },
		GraceTimeout: 50 * time.Millisecond,
	}
	res := Stop(prof, opts)

	if len(res) != 1 {
		t.Fatalf("expected 1 stop result, got %d", len(res))
	}

	if !res[0].Stopped {
		t.Fatalf("Stop should report Stopped=true after SIGKILL: %+v", res[0])
	}

	if len(sigsSent) != 2 || sigsSent[0] != SignalTerm || sigsSent[1] != SignalKill {
		t.Fatalf("expected SIGTERM then SIGKILL, got %v", sigsSent)
	}
}

// TestStopReturnsCleanlyWhenPortAlreadyFree verifies the no-op
// path: a port with no listener means the desired end-state
// (port-is-free) already holds, so Stop reports Stopped=true
// without sending any signal.
func TestStopReturnsCleanlyWhenPortAlreadyFree(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "nothing", Binary: "x", Port: 1}}}

	var killed bool

	opts := StopOptions{
		PIDForPort: func(int) (int, error) { return 0, nil },
		Kill:       func(int, Signal) error { killed = true; return nil },
	}
	res := Stop(prof, opts)

	if killed {
		t.Fatalf("Stop must not signal anything when the port is already free")
	}

	if !res[0].Stopped {
		t.Fatalf("Stop should report Stopped=true for an empty port: %+v", res[0])
	}
}

// TestStopSIGTERMCleanExitDoesNotEscalate verifies the happy
// path: when the port frees up after SIGTERM, Stop must NOT
// follow with SIGKILL. Sending SIGKILL after a clean exit could
// kill an unrelated process that grabbed the port in the
// intervening millisecond.
func TestStopSIGTERMCleanExitDoesNotEscalate(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "clean", Binary: "x", Port: 8080}}}

	var lookups int32

	opts := StopOptions{
		PIDForPort: func(int) (int, error) {
			n := atomic.AddInt32(&lookups, 1)
			if n == 1 {
				return 1234, nil // initial: alive
			}

			return 0, nil // post-SIGTERM: gone
		},
		Kill: func(_ int, sig Signal) error {
			if sig == SignalKill {
				t.Fatalf("clean exit must not escalate to SIGKILL")
			}

			return nil
		},
		IsAlive:      func(int) bool { return false },
		GraceTimeout: 200 * time.Millisecond,
	}
	res := Stop(prof, opts)

	if !res[0].Stopped {
		t.Fatalf("clean SIGTERM exit should report Stopped=true: %+v", res[0])
	}
}

// TestStatusReportsUpAndDownAccordingToPIDLookup is the
// canonical visibility path: each model's Up flag reflects
// whether something is listening on its port.
func TestStatusReportsUpAndDownAccordingToPIDLookup(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "running", Port: 100},
		{Alias: "down", Port: 200},
	}}
	opts := StopOptions{
		PIDForPort: func(port int) (int, error) {
			if port == 100 {
				return 7777, nil
			}

			return 0, nil
		},
	}
	res := Status(prof, opts)

	if len(res) != 2 {
		t.Fatalf("expected 2 status rows, got %d", len(res))
	}

	if !res[0].Up || res[0].PID != 7777 {
		t.Fatalf("running backend should be Up with its PID: %+v", res[0])
	}

	if res[1].Up {
		t.Fatalf("down backend should be Up=false: %+v", res[1])
	}
}

func TestStatusReportsHostedModelsWithoutPIDLookup(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "glm-5.2", Kind: "glm", Port: 0},
	}}
	calledPIDLookup := false
	opts := StopOptions{
		PIDForPort: func(port int) (int, error) {
			calledPIDLookup = true
			return 0, nil
		},
	}

	res := Status(prof, opts)

	if calledPIDLookup {
		t.Fatal("hosted models must not use local PID lookup")
	}

	if len(res) != 1 || !res[0].Hosted || res[0].Up || res[0].Kind != "glm" {
		t.Fatalf("unexpected hosted status: %+v", res)
	}
}

// TestStopReVerifiesPIDBeforeSIGKILL is the TOCTOU regression
// guard. The original PID quietly exits during the grace window;
// the kernel could recycle that number for any other process by
// the time we'd otherwise SIGKILL. Stop must notice the original
// PID is gone and skip the SIGKILL — failing this test means we
// could SIGKILL an unrelated user process.
func TestStopReVerifiesPIDBeforeSIGKILL(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "racy", Binary: "x", Port: 9000}}}

	var sigsSent []Signal
	// IsAlive returns true for the initial probe and the in-grace
	// polling, then false for the pre-SIGKILL re-verify. The Kill
	// stub records each signal so we can assert SIGKILL never fired.
	var aliveCalls int32

	opts := StopOptions{
		PIDForPort: func(int) (int, error) { return 1234, nil },
		IsAlive: func(int) bool {
			n := atomic.AddInt32(&aliveCalls, 1)
			return n < 2 // only the initial check sees alive
		},
		Kill:         func(_ int, sig Signal) error { sigsSent = append(sigsSent, sig); return nil },
		GraceTimeout: 50 * time.Millisecond,
	}
	res := Stop(prof, opts)

	for _, sig := range sigsSent {
		if sig == SignalKill {
			t.Fatalf("Stop must not SIGKILL a process that already exited; signals=%v", sigsSent)
		}
	}

	if !res[0].Stopped {
		t.Fatalf("clean SIGTERM exit must report Stopped=true: %+v", res[0])
	}
}

// TestStopUsesPIDFileWhenPresent verifies the PID-file-as-source-
// of-truth contract: when a recorded PID is alive, Stop targets it
// directly instead of asking lsof what's on the port. This is the
// load-bearing property that protects against "backend crashed,
// something else grabbed the port" — Stop kills the spawned
// process by PID rather than the squatter by port.
func TestStopUsesPIDFileWhenPresent(t *testing.T) {
	runDir := t.TempDir()
	if err := writePIDFile(runDir, "racy", 4242); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}

	prof := Profile{Models: []Model{{Alias: "racy", Binary: "x", Port: 9000}}}

	var killedPID int

	opts := StopOptions{
		RunDir:  runDir,
		IsAlive: func(pid int) bool { return pid == 4242 },
		PIDForPort: func(int) (int, error) {
			t.Fatal("PIDForPort must not be consulted when PID file is alive")

			return 0, nil
		},
		Kill: func(pid int, sig Signal) error {
			killedPID = pid
			return nil
		},
		GraceTimeout: 50 * time.Millisecond,
	}

	_ = Stop(prof, opts)

	if killedPID != 4242 {
		t.Fatalf("Stop should target PID-file PID 4242, got %d", killedPID)
	}

	// PID file must be gone after a successful stop so subsequent
	// Status reports don't show a ghost.
	if _, err := os.Stat(pidFilePath(runDir, "racy")); !os.IsNotExist(err) {
		t.Fatalf("PID file should be removed after stop; stat err=%v", err)
	}
}

// TestStopFallsBackToLsofWhenPIDFileMissing verifies the
// back-compat path: a backend started before PID file tracking
// existed (or one started out-of-band) still gets stopped via the
// lsof lookup.
func TestStopFallsBackToLsofWhenPIDFileMissing(t *testing.T) {
	runDir := t.TempDir() // empty — no PID file
	prof := Profile{Models: []Model{{Alias: "legacy", Binary: "x", Port: 9000}}}

	var killedPID int

	opts := StopOptions{
		RunDir:     runDir,
		IsAlive:    func(int) bool { return false }, // forces clean SIGTERM exit on first poll
		PIDForPort: func(int) (int, error) { return 7777, nil },
		Kill: func(pid int, sig Signal) error {
			if sig == SignalTerm {
				killedPID = pid
			}

			return nil
		},
		GraceTimeout: 50 * time.Millisecond,
	}

	_ = Stop(prof, opts)

	if killedPID != 7777 {
		t.Fatalf("Stop should target lsof PID 7777 when PID file is absent, got %d", killedPID)
	}
}

// TestReadPIDFileDiscardsStaleEntries verifies the self-cleaning
// property: a PID file naming a dead process is removed on read so
// subsequent calls don't keep re-validating the same stale value.
func TestReadPIDFileDiscardsStaleEntries(t *testing.T) {
	runDir := t.TempDir()
	if err := writePIDFile(runDir, "ghost", 99999); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}

	pid, err := readPIDFile(runDir, "ghost", func(int) bool { return false })
	if err != nil || pid != 0 {
		t.Fatalf("stale PID file should resolve to (0, nil); got (%d, %v)", pid, err)
	}

	if _, err := os.Stat(pidFilePath(runDir, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("stale PID file should be removed on read; stat err=%v", err)
	}
}

// TestReadPIDFileEmptyRunDirIsNoOp verifies the disabled path: an
// empty RunDir means PID file tracking is off; reading must be a
// silent (0, nil) so the lsof fallback can fire.
func TestReadPIDFileEmptyRunDirIsNoOp(t *testing.T) {
	pid, err := readPIDFile("", "anything", nil)
	if err != nil || pid != 0 {
		t.Fatalf("empty runDir should noop; got (%d, %v)", pid, err)
	}
}

// TestSanitizeAliasNormalizesUnsafeFilenameChars verifies the
// log-filename helper rejects characters that would break the
// filesystem (slashes, spaces, weird unicode) without losing
// useful structure (`-`, `.`, lowercase alphanumerics pass
// through unchanged).
func TestSanitizeAliasNormalizesUnsafeFilenameChars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"qwen3-coder-30b", "qwen3-coder-30b"},
		{"My Cool Model", "my_cool_model"},
		{"a/b/c", "a_b_c"},
		{"v3.6", "v3.6"},
		{"", "backend"},
	}
	for _, c := range cases {
		if got := sanitizeAlias(c.in); got != c.want {
			t.Fatalf("sanitizeAlias(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
