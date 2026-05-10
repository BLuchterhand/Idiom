package backends

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// LaunchSpec is the argv recipe a Launcher hands to os/exec to
// start one backend. It's separated from Launch() so callers can
// inspect (and tests can assert) the constructed argv without
// actually spawning a process.
type LaunchSpec struct {
	Alias string
	Argv  []string
	Port  int
}

// LaunchResult captures the outcome of one spawn attempt. PID is
// the spawned process; LogPath is where stdout+stderr were
// redirected. Ready is true once the listening port responds.
type LaunchResult struct {
	Alias   string
	PID     int
	Port    int
	LogPath string
	Ready   bool
	Err     error
}

// BuildLaunchSpec converts a Model declaration into the argv we
// hand to exec. Defaults match what Colony's start_local_qwen.sh
// passes to mlx_lm.server; projects with a non-MLX launcher can
// supply the full argv via extra_args (which is appended verbatim
// after the schema-derived flags).
//
// Argv shape:
//
//	<binary> --model <model_path> --host 127.0.0.1 --port <port>
//	  [--prompt-cache-size N] [--prompt-cache-bytes N]
//	  [--max-tokens N]
//	  <extra_args...>
//
// The cache/token flags are emitted only when the schema field is
// non-zero so a launcher that doesn't recognize them isn't given
// an unfamiliar flag.
func BuildLaunchSpec(m Model) (LaunchSpec, error) {
	if strings.TrimSpace(m.Binary) == "" {
		return LaunchSpec{}, fmt.Errorf("backends: model %q has empty binary", m.Alias)
	}
	if m.Port <= 0 {
		return LaunchSpec{}, fmt.Errorf("backends: model %q has invalid port %d", m.Alias, m.Port)
	}
	argv := []string{m.Binary}
	if path := strings.TrimSpace(m.ModelPath); path != "" {
		argv = append(argv, "--model", expandHome(path))
	}
	argv = append(argv, "--host", "127.0.0.1", "--port", strconv.Itoa(m.Port))
	if m.CacheSize > 0 {
		argv = append(argv, "--prompt-cache-size", strconv.Itoa(m.CacheSize))
	}
	if m.CacheBytes > 0 {
		argv = append(argv, "--prompt-cache-bytes", strconv.FormatInt(m.CacheBytes, 10))
	}
	if m.MaxTokens > 0 {
		argv = append(argv, "--max-tokens", strconv.Itoa(m.MaxTokens))
	}
	argv = append(argv, m.ExtraArgs...)
	return LaunchSpec{Alias: m.Alias, Argv: argv, Port: m.Port}, nil
}

// expandHome resolves a leading "~/" against the current user's
// home directory. Other tilde forms (`~user/...`) are intentionally
// not handled — the schema is local-developer-shaped.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

// LauncherOptions tunes the spawn machinery. Tests inject a no-op
// spawner / instant ready-check so they don't actually start
// processes. Production callers pass nil and get the real spawner.
type LauncherOptions struct {
	// LogDir is where stdout/stderr land. When empty, falls back to
	// the OS temp dir so backends produce no observable filesystem
	// effect outside this process tree's lifetime.
	LogDir string
	// ReadyTimeout caps how long Launch waits for a backend's
	// port to start accepting connections. Zero means "use the
	// default of 90s" (matches Colony's start_local_qwen.sh).
	ReadyTimeout time.Duration
	// Spawner is the callback that actually starts the process.
	// Nil means "use the real os/exec spawner." Tests inject a
	// stub that records argv without spawning.
	Spawner func(spec LaunchSpec, logPath string) (pid int, err error)
	// PortReady is the readiness probe; nil means "TCP-connect to
	// 127.0.0.1:<port>." Tests inject an instant-ok variant.
	PortReady func(port int, deadline time.Time) bool
}

// Launch starts every model in the profile and returns one
// LaunchResult per model in the same order. A spawn failure for
// one backend doesn't stop the others — each result carries its
// own error so the operator sees every failure in one report.
func Launch(prof Profile, opts LauncherOptions) []LaunchResult {
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 90 * time.Second
	}
	if opts.Spawner == nil {
		opts.Spawner = realSpawn
	}
	if opts.PortReady == nil {
		opts.PortReady = portTCPReady
	}
	logDir := opts.LogDir
	if strings.TrimSpace(logDir) == "" {
		logDir = os.TempDir()
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		// Surface the directory failure once, then attribute it to
		// every model so the operator sees a clear root cause.
		results := make([]LaunchResult, 0, len(prof.Models))
		for _, m := range prof.Models {
			results = append(results, LaunchResult{
				Alias: m.Alias,
				Port:  m.Port,
				Err:   fmt.Errorf("backends: create log dir %q: %w", logDir, err),
			})
		}
		return results
	}

	results := make([]LaunchResult, 0, len(prof.Models))
	for _, m := range prof.Models {
		results = append(results, launchOne(m, logDir, opts))
	}
	return results
}

func launchOne(m Model, logDir string, opts LauncherOptions) LaunchResult {
	spec, err := BuildLaunchSpec(m)
	if err != nil {
		return LaunchResult{Alias: m.Alias, Port: m.Port, Err: err}
	}
	// Skip the spawn if the port already accepts traffic — we treat
	// that as "this backend is already running" and report the
	// existing state. The operator should run `idiom backend stop`
	// first if they want to restart cleanly.
	if opts.PortReady(spec.Port, time.Now().Add(50*time.Millisecond)) {
		return LaunchResult{
			Alias: spec.Alias,
			Port:  spec.Port,
			Ready: true,
			Err:   fmt.Errorf("backends: port %d already in use; assuming %s is already running", spec.Port, spec.Alias),
		}
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("idiom-backend-%s.log", sanitizeAlias(spec.Alias)))
	pid, err := opts.Spawner(spec, logPath)
	if err != nil {
		return LaunchResult{Alias: spec.Alias, Port: spec.Port, LogPath: logPath, Err: err}
	}
	deadline := time.Now().Add(opts.ReadyTimeout)
	ready := opts.PortReady(spec.Port, deadline)
	return LaunchResult{
		Alias:   spec.Alias,
		PID:     pid,
		Port:    spec.Port,
		LogPath: logPath,
		Ready:   ready,
	}
}

// sanitizeAlias makes an alias safe for use in a filename: lowercase
// alphanumerics and dots/dashes only. Other chars become `_`.
func sanitizeAlias(alias string) string {
	var b strings.Builder
	for _, r := range alias {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "backend"
	}
	return b.String()
}

// realSpawn is the production launcher: open the log file, build
// the command, set Setpgid so the child survives idiom exit, and
// kick it off. Returns the PID; ready-check happens in the caller.
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
	if cmd.Process != nil {
		_ = cmd.Process.Release()
		return cmd.Process.Pid, nil
	}
	return 0, fmt.Errorf("backends: spawn %s: no process state after Start", spec.Alias)
}

// portTCPReady returns true when 127.0.0.1:<port> accepts a TCP
// connection at any point before deadline. Polls every 200ms.
// "Listens on the port" is the simplest cross-launcher readiness
// signal — health-check endpoints differ per backend.
func portTCPReady(port int, deadline time.Time) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		dialer := &net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// StopResult captures the outcome of one stop attempt against a
// backend identified by its listening port.
type StopResult struct {
	Alias   string
	Port    int
	PID     int
	Stopped bool
	Err     error
}

// StopOptions tunes Stop. Tests inject stubs to avoid actually
// killing processes.
type StopOptions struct {
	// PIDForPort returns the PID of the process listening on the
	// given port, or 0 if nothing's listening. Production uses
	// `lsof -ti :<port>`; tests inject a stub.
	PIDForPort func(port int) (int, error)
	// Kill sends a signal to the given PID. Production uses
	// syscall.Kill; tests inject a stub that records the call.
	Kill func(pid int, sig syscall.Signal) error
	// GraceTimeout is how long Stop waits between SIGTERM and
	// SIGKILL. Zero means "use the default of 5s."
	GraceTimeout time.Duration
}

// Stop signals every model's listening process. SIGTERM first;
// after GraceTimeout, SIGKILL if the port is still bound. Returns
// one result per model; an aborted stop attempt for one model
// doesn't stop the others.
func Stop(prof Profile, opts StopOptions) []StopResult {
	if opts.PIDForPort == nil {
		opts.PIDForPort = realPIDForPort
	}
	if opts.Kill == nil {
		opts.Kill = syscall.Kill
	}
	if opts.GraceTimeout == 0 {
		opts.GraceTimeout = 5 * time.Second
	}
	results := make([]StopResult, 0, len(prof.Models))
	for _, m := range prof.Models {
		results = append(results, stopOne(m, opts))
	}
	return results
}

func stopOne(m Model, opts StopOptions) StopResult {
	pid, err := opts.PIDForPort(m.Port)
	if err != nil {
		return StopResult{Alias: m.Alias, Port: m.Port, Err: err}
	}
	if pid <= 0 {
		// Nothing listening; report Stopped=true since the desired
		// end-state ("port is free") is already satisfied.
		return StopResult{Alias: m.Alias, Port: m.Port, Stopped: true}
	}
	if err := opts.Kill(pid, syscall.SIGTERM); err != nil {
		return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Err: fmt.Errorf("backends: SIGTERM pid %d: %w", pid, err)}
	}
	deadline := time.Now().Add(opts.GraceTimeout)
	for time.Now().Before(deadline) {
		alive, _ := opts.PIDForPort(m.Port)
		if alive == 0 {
			return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Stopped: true}
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Last-resort SIGKILL. We don't follow up — if SIGKILL doesn't
	// clear the port within a fraction of a second, the process is
	// stuck in kernel space and idiom can't help.
	if err := opts.Kill(pid, syscall.SIGKILL); err != nil {
		return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Err: fmt.Errorf("backends: SIGKILL pid %d: %w", pid, err)}
	}
	return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Stopped: true}
}

// realPIDForPort shells out to `lsof -ti :<port>` to find the
// listener PID. Returns (0, nil) when nothing is listening.
func realPIDForPort(port int) (int, error) {
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port))
	out, err := cmd.Output()
	if err != nil {
		// `lsof` exits non-zero when nothing's listening; that's the
		// "no PID found" path, not an error.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0, nil
		}
		return 0, fmt.Errorf("backends: lsof %d: %w", port, err)
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	if line == "" {
		return 0, nil
	}
	pid, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("backends: parse lsof output %q: %w", line, err)
	}
	return pid, nil
}

// StatusResult is one row of the status table.
type StatusResult struct {
	Alias string
	Port  int
	Up    bool
	PID   int
}

// Status reports which backends in a profile are currently
// listening. It uses the same PID lookup Stop does so the two
// agree on what counts as "up."
func Status(prof Profile, opts StopOptions) []StatusResult {
	if opts.PIDForPort == nil {
		opts.PIDForPort = realPIDForPort
	}
	results := make([]StatusResult, 0, len(prof.Models))
	for _, m := range prof.Models {
		pid, _ := opts.PIDForPort(m.Port)
		results = append(results, StatusResult{
			Alias: m.Alias,
			Port:  m.Port,
			Up:    pid > 0,
			PID:   pid,
		})
	}
	return results
}
