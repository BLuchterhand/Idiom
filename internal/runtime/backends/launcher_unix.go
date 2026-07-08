//go:build !windows

package backends

import (
	"context"
	"errors"
	"fmt"
	"syscall"

	"idiom/internal/platform/commandexec"
)

// realSpawn is the production launcher on Unix-likes. commandexec owns
// process construction, log redirection, and process-group setup; the backend
// launcher owns only the backend-specific argv, PID, and readiness contract.
func realSpawn(spec LaunchSpec, logPath string) (int, error) {
	if len(spec.Argv) == 0 {
		return 0, fmt.Errorf("backends: spawn %s: empty argv", spec.Alias)
	}

	pid, err := commandexec.StartDetached(context.Background(), commandexec.Spec{
		Name:          spec.Argv[0],
		Args:          append([]string(nil), spec.Argv[1:]...),
		OutputLogPath: logPath,
	})
	if err != nil {
		return 0, fmt.Errorf("backends: spawn %s: %w", spec.Alias, err)
	}

	if pid > 0 {
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
