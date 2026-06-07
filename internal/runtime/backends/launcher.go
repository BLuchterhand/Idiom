package backends

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/failures"
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
//	  [--max-tokens N] [--timeout N for vmlx-compatible launchers]
//	  <extra_args...>
//
// Direct `vmlx` binaries use the native shape instead:
//
//	vmlx serve <model_path> --host 127.0.0.1 --port <port>
//	  [--prefix-cache-size N] [--prefix-cache-max-bytes N]
//	  [--max-tokens N] [--timeout N]
//	  <extra_args...>
//
// The cache/token flags are emitted only when the schema field is
// non-zero so a launcher that doesn't recognize them isn't given
// an unfamiliar flag.
func BuildLaunchSpec(m Model) (LaunchSpec, error) {
	if isHostedModelKind(m.Kind) {
		return LaunchSpec{}, fmt.Errorf("backends: hosted model %q (kind=%s) cannot be launched locally", m.Alias, strings.TrimSpace(m.Kind))
	}
	if strings.TrimSpace(m.Binary) == "" {
		return LaunchSpec{}, fmt.Errorf("backends: model %q has empty binary", m.Alias)
	}
	if m.Port <= 0 {
		return LaunchSpec{}, fmt.Errorf("backends: model %q has invalid port %d", m.Alias, m.Port)
	}
	argv := []string{m.Binary}
	directVmlx := launchSpecUsesDirectVmlxServe(m)
	if path := strings.TrimSpace(m.ModelPath); path != "" {
		if directVmlx {
			argv = append(argv, "serve", expandHome(path))
		} else {
			argv = append(argv, "--model", expandHome(path))
		}
	}
	argv = append(argv, "--host", modelHost(m), "--port", strconv.Itoa(m.Port))
	if m.CacheSize > 0 {
		if directVmlx {
			argv = append(argv, "--prefix-cache-size", strconv.Itoa(m.CacheSize))
		} else {
			argv = append(argv, "--prompt-cache-size", strconv.Itoa(m.CacheSize))
		}
	}
	if m.CacheBytes > 0 {
		if directVmlx {
			argv = append(argv, "--prefix-cache-max-bytes", strconv.FormatInt(m.CacheBytes, 10))
		} else {
			argv = append(argv, "--prompt-cache-bytes", strconv.FormatInt(m.CacheBytes, 10))
		}
	}
	if m.MaxTokens > 0 {
		argv = append(argv, "--max-tokens", strconv.Itoa(m.MaxTokens))
	}
	if m.RequestTimeoutSeconds > 0 && launchSpecSupportsRequestTimeout(m) {
		argv = append(argv, "--timeout", strconv.Itoa(m.RequestTimeoutSeconds))
	}
	argv = append(argv, m.ExtraArgs...)
	return LaunchSpec{Alias: m.Alias, Argv: argv, Port: m.Port}, nil
}

func launchSpecSupportsRequestTimeout(m Model) bool {
	kind := strings.ToLower(strings.TrimSpace(m.Kind))
	if kind == "vmlx" {
		return true
	}
	base := strings.ToLower(filepath.Base(strings.TrimSpace(m.Binary)))
	return strings.Contains(base, "vmlx")
}

func launchSpecUsesDirectVmlxServe(m Model) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(m.Binary)))
	return base == "vmlx"
}

func isHostedModelKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "codex", "deepseek", "minimax", "openai_responses", "zai":
		return true
	default:
		return false
	}
}

// pidFilePath returns the canonical PID file path for an alias
// under runDir. Empty runDir disables PID file tracking entirely;
// callers must check the empty case before reading or writing.
func pidFilePath(runDir, alias string) string {
	if strings.TrimSpace(runDir) == "" {
		return ""
	}
	return filepath.Join(runDir, "backend-"+sanitizeAlias(alias)+".pid")
}

// writePIDFile records the spawned backend's PID under runDir so
// subsequent Stop/Status calls can locate the process directly
// instead of going through `lsof`. Best-effort: a write failure
// surfaces on the LaunchResult but doesn't unwind the spawn,
// because the backend is already running and the lsof fallback
// still works.
func writePIDFile(runDir, alias string, pid int) error {
	path := pidFilePath(runDir, alias)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("backends: create run dir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("backends: write pid file %q: %w", path, err)
	}
	return nil
}

// readPIDFile returns the PID stored at runDir/backend-<alias>.pid
// or 0 when the file is absent / unparseable / stale. A stale file
// (PID no longer alive) is removed so subsequent calls don't keep
// re-validating it. isAlive may be nil — callers that only want
// the on-disk value can pass nil and check liveness themselves.
func readPIDFile(runDir, alias string, isAlive func(int) bool) (int, error) {
	path := pidFilePath(runDir, alias)
	if path == "" {
		return 0, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("backends: read pid file %q: %w", path, err)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(body)))
	if parseErr != nil || pid <= 0 {
		_ = os.Remove(path)
		return 0, nil
	}
	if isAlive != nil && !isAlive(pid) {
		_ = os.Remove(path)
		return 0, nil
	}
	return pid, nil
}

// removePIDFile deletes the alias's PID file. Used after a clean
// stop so a subsequent status doesn't report the now-dead process
// as up. Missing-file errors are intentionally swallowed.
func removePIDFile(runDir, alias string) {
	path := pidFilePath(runDir, alias)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// modelHost returns the host the backend should bind to (for the
// launcher's --host flag) or the proxy should dial (for backend
// addressing). Schema field wins; "127.0.0.1" is the fallback so
// the most common local-only setup needs no schema entry.
func modelHost(m Model) string {
	if h := strings.TrimSpace(m.Host); h != "" {
		return h
	}
	return "127.0.0.1"
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
	// RunDir is where per-alias PID files land. When empty, PID file
	// tracking is disabled and Stop/Status fall back to lsof. The
	// CLI defaults this to <project-root>/.idiom/run so multi-project
	// dev setups don't share PID-file state across workspaces.
	RunDir string
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
	// Best-effort PID file write. The process is already running, so a
	// write failure shouldn't fail the spawn — Stop will fall back to
	// lsof when the file is missing. We surface the failure on the
	// LaunchResult so the operator knows the safety net is degraded.
	pidFileErr := writePIDFile(opts.RunDir, spec.Alias, pid)
	deadline := time.Now().Add(opts.ReadyTimeout)
	ready := opts.PortReady(spec.Port, deadline)
	return LaunchResult{
		Alias:   spec.Alias,
		PID:     pid,
		Port:    spec.Port,
		LogPath: logPath,
		Ready:   ready,
		Err:     pidFileErr,
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

// realSpawn / realStopSignal / realPIDForPort live in
// launcher_unix.go and launcher_windows.go because Setpgid (used to
// detach the child) and SIGTERM/SIGKILL (used by Stop) aren't
// available on all platforms. The Stop function below uses
// realStopSignal indirectly via opts.Kill.

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

// Signal is the portable-ish process signal we accept on Stop.
// On Unix it maps to syscall.SIGTERM/SIGKILL; on Windows the
// stub launcher errors out and these constants are unused.
type Signal int

const (
	SignalTerm Signal = 15 // SIGTERM on Unix
	SignalKill Signal = 9  // SIGKILL on Unix
)

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
	// RunDir is the directory holding per-alias PID files. When set
	// (and when a PID file exists for the alias), that PID is the
	// source of truth — Stop targets the spawned process even if
	// something else has since taken over the port. When the file
	// is absent, Stop falls back to PIDForPort so backends launched
	// out-of-band still get cleaned up.
	RunDir string
	// IsAlive reports whether a PID is still a live process.
	// Production uses `kill(pid, 0)`; nil means "use the default."
	IsAlive func(pid int) bool
	// PIDForPort returns the PID of the process listening on the
	// given port, or 0 if nothing's listening. Production uses
	// `lsof -ti :<port>`; tests inject a stub.
	PIDForPort func(port int) (int, error)
	// Kill sends a signal to the given PID. Production uses the
	// platform-specific realStopSignal (Unix: syscall.Kill; Windows:
	// stub error); tests inject a stub that records the call.
	Kill func(pid int, sig Signal) error
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
		opts.Kill = realStopSignal
	}
	if opts.IsAlive == nil {
		opts.IsAlive = realIsAlive
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

// resolvePID picks which PID a stop / status call should target for
// an alias: the recorded PID file when present and still alive,
// otherwise whatever is listening on the port. The PID file is the
// source of truth when set so a backend that crashes and gets
// replaced on its port doesn't trick stop into killing the wrong
// process.
type resolvedPID struct {
	PID         int
	FromPIDFile bool
}

func resolvePID(m Model, runDir string, isAlive func(int) bool, pidForPort func(int) (int, error)) (resolvedPID, error) {
	pid, err := readPIDFile(runDir, m.Alias, isAlive)
	if err != nil {
		return resolvedPID{}, err
	}
	if pid > 0 {
		return resolvedPID{PID: pid, FromPIDFile: true}, nil
	}
	pid, err = pidForPort(m.Port)
	if err != nil {
		return resolvedPID{}, err
	}
	return resolvedPID{PID: pid}, nil
}

func stopOne(m Model, opts StopOptions) StopResult {
	resolved, err := resolvePID(m, opts.RunDir, opts.IsAlive, opts.PIDForPort)
	if err != nil {
		return StopResult{Alias: m.Alias, Port: m.Port, Err: err}
	}
	pid := resolved.PID
	if pid <= 0 {
		// Nothing listening; report Stopped=true since the desired
		// end-state ("port is free") is already satisfied. Clean up
		// any stray PID file so a future Status doesn't report a
		// ghost process.
		removePIDFile(opts.RunDir, m.Alias)
		return StopResult{Alias: m.Alias, Port: m.Port, Stopped: true}
	}
	if err := opts.Kill(pid, SignalTerm); err != nil {
		return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Err: fmt.Errorf("backends: SIGTERM pid %d: %w", pid, err)}
	}
	// Poll for liveness rather than port-freeing: with the PID-file
	// path, "is the spawned process gone" is the question we actually
	// care about. Port-only polling could be fooled by a fast restart
	// from another supervisor grabbing the slot.
	deadline := time.Now().Add(opts.GraceTimeout)
	for time.Now().Before(deadline) {
		if !opts.IsAlive(pid) {
			removePIDFile(opts.RunDir, m.Alias)
			return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Stopped: true}
		}
		if !resolved.FromPIDFile && !portStillOwnedByPID(m.Port, pid, opts.PIDForPort) {
			removePIDFile(opts.RunDir, m.Alias)
			return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Stopped: true}
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Re-verify before SIGKILL: if the original PID is already gone,
	// we must NOT signal it — Linux can recycle the PID for an
	// unrelated process and SIGKILL would land on whatever is now in
	// that slot (the user's shell, another backend, etc). This is
	// the documented "Process kills: the pid > 0 rule" hazard.
	if !opts.IsAlive(pid) {
		removePIDFile(opts.RunDir, m.Alias)
		return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Stopped: true}
	}
	if !resolved.FromPIDFile && !portStillOwnedByPID(m.Port, pid, opts.PIDForPort) {
		removePIDFile(opts.RunDir, m.Alias)
		return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Stopped: true}
	}
	if err := opts.Kill(pid, SignalKill); err != nil {
		return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Err: fmt.Errorf("backends: SIGKILL pid %d: %w", pid, err)}
	}
	removePIDFile(opts.RunDir, m.Alias)
	return StopResult{Alias: m.Alias, Port: m.Port, PID: pid, Stopped: true}
}

func portStillOwnedByPID(port int, pid int, pidForPort func(int) (int, error)) bool {
	current, err := pidForPort(port)
	return err == nil && current == pid
}

// pidForPortViaLsof is the cross-platform PID lookup. Shells out
// to `lsof -ti :<port>` and parses the first line. Returns (0, nil)
// when nothing's listening or when `lsof` itself isn't available
// (the binary-not-found case is not an error — callers will fall
// back to the next strategy).
func pidForPortViaLsof(port int) (int, error) {
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port))
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return 0, errLsofUnavailable
		}
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

// errLsofUnavailable signals "the lsof binary isn't on PATH." On
// Linux, realPIDForPort falls through to a /proc-based lookup; on
// other platforms the caller treats this as "no PID found."
var errLsofUnavailable = failures.New(failures.Unavailable, reasonBackendLsofUnavailable, "backends: lsof not available")

// StatusResult is one row of the status table.
type StatusResult struct {
	Alias string
	Port  int
	Up    bool
	PID   int
}

// Status reports which backends in a profile are currently
// listening. It uses the same PID resolution Stop does (PID file
// preferred when present + alive; lsof fallback otherwise) so the
// two agree on what counts as "up."
func Status(prof Profile, opts StopOptions) []StatusResult {
	if opts.PIDForPort == nil {
		opts.PIDForPort = realPIDForPort
	}
	if opts.IsAlive == nil {
		opts.IsAlive = realIsAlive
	}
	results := make([]StatusResult, 0, len(prof.Models))
	for _, m := range prof.Models {
		resolved, _ := resolvePID(m, opts.RunDir, opts.IsAlive, opts.PIDForPort)
		pid := resolved.PID
		results = append(results, StatusResult{
			Alias: m.Alias,
			Port:  m.Port,
			Up:    pid > 0,
			PID:   pid,
		})
	}
	return results
}
