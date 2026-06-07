//go:build windows

package backends

import "fmt"

// Idiom's backend launcher relies on Setpgid (to detach children
// from the parent's process group so they survive idiom exit) and
// SIGTERM/SIGKILL (to stop them later). Both are POSIX. The Windows
// build keeps the package compilable but the launcher returns clear
// "not supported" errors so the operator sees a real diagnostic
// instead of a silent failure.

func realSpawn(spec LaunchSpec, logPath string) (int, error) {
	return 0, fmt.Errorf("backends: idiom backend start is not supported on Windows (POSIX-only launcher)")
}

func realStopSignal(pid int, sig Signal) error {
	return fmt.Errorf("backends: idiom backend stop is not supported on Windows (POSIX-only signal layer)")
}

// realIsAlive on Windows always returns false: the launcher refuses
// to spawn here, so there's no real PID to probe and assuming the
// process is dead lets the stop pipeline short-circuit cleanly.
func realIsAlive(pid int) bool {
	return false
}

// realPIDForPort on Windows mirrors the rest of the launcher: the
// signal-layer / spawn paths are unsupported, so PID lookup is too.
// Returning a clear error keeps `idiom backend status` honest
// rather than reporting every backend as DOWN.
func realPIDForPort(port int) (int, error) {
	return 0, fmt.Errorf("backends: PID lookup not supported on Windows (POSIX-only)")
}
