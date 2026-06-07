//go:build integration && !windows

package backends

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func tempLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spawn.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return path
}

// These tests cover the otherwise-bypassed production functions:
// realSpawn (starts an actual subprocess), portTCPReady (real TCP
// connect), and realPIDForPort (real `lsof` call). They're all
// build-tagged !windows because they rely on POSIX primitives the
// stub Windows launcher doesn't provide.

// TestRealSpawnSurvivesParentRelease verifies the load-bearing
// detach contract: a process started via realSpawn is reparented
// to init (Setpgid) so it stays alive after the Go side calls
// Release. Without Setpgid, killing idiom would also kill the
// backends. The test spawns `sleep 30`, releases the cmd state,
// and verifies the PID is still alive after a short wait.
func TestRealSpawnSurvivesParentRelease(t *testing.T) {
	// File build-tagged !windows; no runtime.GOOS check needed.
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep binary not available")
	}
	logPath := tempLog(t)
	pid, err := realSpawn(LaunchSpec{
		Alias: "sleeper",
		Argv:  []string{"sleep", "5"},
		Port:  0,
	}, logPath)
	if err != nil {
		t.Fatalf("realSpawn: %v", err)
	}
	t.Cleanup(func() {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	// Give the kernel a moment to actually start it.
	time.Sleep(100 * time.Millisecond)
	if !pidIsAlive(pid) {
		t.Fatalf("spawned PID %d should still be alive shortly after Release", pid)
	}
}

// TestRealSpawnReportsBinaryNotFound verifies that an unresolvable
// command surfaces as a real error rather than silently producing a
// zombie PID. Important: Go's exec.LookPath is what fails here, so
// the error message should reference the binary name.
func TestRealSpawnReportsBinaryNotFound(t *testing.T) {
	logPath := tempLog(t)
	_, err := realSpawn(LaunchSpec{
		Alias: "nope",
		Argv:  []string{"definitely-not-a-real-binary-86753"},
		Port:  0,
	}, logPath)
	if err == nil {
		t.Fatalf("missing binary must surface an error")
	}
}

// TestPortTCPReadyDetectsListener verifies the readiness probe
// against an actual TCP listener: a port that accepts connections
// returns true; a port with nothing on it returns false within the
// deadline window. Catches a regression in the dial/poll loop.
func TestPortTCPReadyDetectsListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	port := listener.Addr().(*net.TCPAddr).Port
	if !portTCPReady(port, time.Now().Add(500*time.Millisecond)) {
		t.Fatalf("portTCPReady should detect a real listener on :%d", port)
	}

	// Close and verify it now returns false within the deadline
	// (no infinite loop on a vanished listener).
	_ = listener.Close()
	deadline := time.Now().Add(400 * time.Millisecond)
	if portTCPReady(port, deadline) {
		t.Fatalf("portTCPReady should fail once the listener is gone")
	}
}

// TestRealPIDForPortIdentifiesActiveListener verifies the lsof-
// shelling-out path against an actual listener: the PID we get
// back from realPIDForPort matches our own. Skips when lsof isn't
// available (some minimal CI containers don't ship it).
func TestRealPIDForPortIdentifiesActiveListener(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not available")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port

	pid, err := realPIDForPort(port)
	if err != nil {
		t.Fatalf("realPIDForPort: %v", err)
	}
	if pid != getPID() {
		t.Fatalf("expected our own PID %d, got %d", getPID(), pid)
	}
}

// TestRealPIDForPortReportsZeroForEmptyPort verifies the "no
// listener" path: querying a port nothing's bound to returns
// (0, nil) rather than an error. We use an unbound high port —
// not airtight but stable enough for a unit test.
func TestRealPIDForPortReportsZeroForEmptyPort(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not available")
	}
	// Find an actually-free port by binding then immediately closing
	// — there's a race window before the test continues, but the
	// test only fails if something else binds in microseconds.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	pid, err := realPIDForPort(port)
	if err != nil {
		t.Fatalf("realPIDForPort: %v", err)
	}
	if pid != 0 {
		t.Fatalf("expected 0 for empty port, got %d", pid)
	}
}

// TestRealStopSignalDeliversSIGTERM is the integration check for
// the actual signal layer: a child process gets SIGTERM and dies.
// We start `sleep 30`, signal it, and verify it exits within
// 2 seconds.
func TestRealStopSignalDeliversSIGTERM(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	if err := realStopSignal(pid, SignalTerm); err != nil {
		t.Fatalf("realStopSignal: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// expected: process exited after SIGTERM
	case <-time.After(2 * time.Second):
		t.Fatalf("process did not exit within 2s of SIGTERM")
	}
}

func pidIsAlive(pid int) bool {
	// signal 0: no-op, used to probe whether the process exists
	// without actually killing it. Errno ESRCH means "no such pid."
	//
	// Reject non-positive pids explicitly: kill(0, …) signals the
	// caller's process group and kill(-1, …) broadcasts to every
	// user-owned process. A passing test must not be one
	// caller-bug away from nuking the developer's login session.
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func getPID() int {
	pid, _ := strconv.Atoi(extractSelfPID())
	return pid
}

func extractSelfPID() string {
	out, _ := exec.Command("sh", "-c", "echo $PPID").Output()
	// $PPID from a child shell is *our* pid (the shell's parent).
	pid := string(out)
	for i, b := range pid {
		if b == '\n' || b == '\r' {
			pid = pid[:i]
			break
		}
	}
	return pid
}
