//go:build !windows

package backends

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// realSpawn is the production launcher on Unix-likes: open the log
// file, build the command, set Setpgid so the child survives idiom
// exit, and kick it off. Returns the PID; ready-check happens in
// the caller.
func realSpawn(spec LaunchSpec, logPath string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("backends: open log %q: %w", logPath, err)
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return 0, fmt.Errorf("backends: spawn %s: %w", spec.Alias, err)
	}
	// Detach the file handle from this process: the child still
	// owns it via fork+exec, so closing here doesn't affect the
	// child's writes. Keeping it open would tie idiom's lifetime
	// to the child's stdout buffer.
	_ = logFile.Close()
	// Release the Go-side cmd state so we don't accumulate zombie
	// metadata across invocations. The child's exit will be reaped
	// by init once idiom exits.
	//
	// Capture Pid BEFORE Release: on non-Windows, os.Process.Release
	// resets p.Pid to -1 (documented Go quirk). Returning Pid after
	// Release would hand callers -1, and any later syscall.Kill(-1, …)
	// would broadcast to every process the user owns. That's how this
	// detail used to nuke the whole login session from a passing test.
	if cmd.Process != nil {
		pid := cmd.Process.Pid
		_ = cmd.Process.Release()
		return pid, nil
	}
	return 0, fmt.Errorf("backends: spawn %s: no process state after Start", spec.Alias)
}

// realStopSignal sends a signal to a PID using the host's syscall
// signal layer. The Signal-typed parameter avoids exposing
// syscall.Signal to callers so the stop pipeline stays portable.
func realStopSignal(pid int, sig Signal) error {
	if pid <= 0 {
		// kill(0, …) signals every process in the caller's process
		// group; kill(-1, …) broadcasts to every user-owned process.
		// Both are catastrophic from a launcher context. Refuse the
		// call rather than risk a session nuke.
		return fmt.Errorf("backends: refusing to signal pid %d (must be > 0)", pid)
	}
	return syscall.Kill(pid, syscall.Signal(sig))
}

// realIsAlive reports whether a PID is still a live process by
// sending signal 0, the standard POSIX liveness probe. Returns false
// for any pid <= 0 — see realStopSignal's guard for why those
// arguments are catastrophic and must never reach syscall.Kill.
func realIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

// realPIDForPort is the production listener-PID lookup. Tries lsof
// first because it works across every Unix idiom targets; when lsof
// isn't installed (some minimal Linux containers), falls through to
// the /proc-based lookup which only the Linux build implements.
func realPIDForPort(port int) (int, error) {
	pid, err := pidForPortViaLsof(port)
	if !errors.Is(err, errLsofUnavailable) {
		return pid, err
	}
	pid, procErr := pidForPortViaProc(port)
	if errors.Is(procErr, errProcUnavailable) {
		// Both strategies failed; surface the original lsof
		// "not found" so the operator knows what to install.
		return 0, errLsofUnavailable
	}
	return pid, procErr
}
