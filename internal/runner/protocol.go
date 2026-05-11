// Package runner implements idiom's local job-queue daemon: a
// long-running process listening on a Unix socket that gates work
// against a configurable per-pool concurrency cap. Any project on the
// machine can `idiom queue run -- <cmd>` to enqueue work, observe its
// position via `idiom queue status`, and have its slot released
// automatically when the wrapped command exits.
//
// The wire format is line-delimited JSON over the Unix socket. A
// connection is the lease: while the client holds the socket, the
// server holds the slot. Closing the socket (graceful or via crash)
// releases the slot and promotes the next waiter.
package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Action names sent on the first line of a connection. The server
// dispatches on these and ignores unknown actions with an error reply.
const (
	ActionAcquire = "acquire"
	ActionStatus  = "status"
	ActionPing    = "ping"
	ActionCancel  = "cancel"
)

// Request is the union of every shape a client may send on the first
// line of a connection. Only fields relevant to the chosen Action are
// inspected; the rest are ignored. Keeping a single struct (rather
// than per-action types) makes the protocol parsing trivial — one
// json.Decode per connection — at the cost of a few unused fields per
// request, which is fine over a Unix socket.
type Request struct {
	Action string `json:"action"`
	Pool   string `json:"pool,omitempty"`
	Label  string `json:"label,omitempty"`
	Client string `json:"client,omitempty"`
	JobID  string `json:"job_id,omitempty"`
}

// Response is the union of every shape the server emits. For the
// acquire action the server emits multiple Response messages over the
// life of the connection (one position update per shift, then a
// granted=true frame); for every other action it emits exactly one.
type Response struct {
	JobID    string         `json:"job_id,omitempty"`
	Pool     string         `json:"pool,omitempty"`
	Position int            `json:"position,omitempty"`
	Granted  bool           `json:"granted,omitempty"`
	Status   *StatusPayload `json:"status,omitempty"`
	Pong     bool           `json:"pong,omitempty"`
	Uptime   float64        `json:"uptime_seconds,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// StatusPayload is the snapshot returned by ActionStatus. Pools is a
// stable per-pool view (sorted by name) so callers don't need to
// re-sort for display; In-flight and waiting jobs are kept in
// dispatch / submission order respectively.
type StatusPayload struct {
	Pools    []PoolStatus `json:"pools"`
	InFlight []JobStatus  `json:"in_flight"`
	Waiting  []JobStatus  `json:"waiting"`
}

// PoolStatus reports the static cap and current usage of one pool.
// Free is computed at snapshot time so callers don't have to subtract.
type PoolStatus struct {
	Name        string `json:"name"`
	MaxInFlight int    `json:"max_in_flight"`
	InFlight    int    `json:"in_flight"`
	Waiting     int    `json:"waiting"`
}

// JobStatus is the per-job projection used in StatusPayload. Times are
// RFC3339 in UTC; clients format for display. Position is 0 for
// in-flight jobs (slot held) and 1-indexed for waiting jobs.
//
// AgeSeconds and Idle are set only for in-flight jobs (#81 idle
// detection). AgeSeconds is wall time since StartedAt; Idle is true
// when AgeSeconds exceeds the pool's IdleThresholdSeconds. The flag
// is purely observational — the daemon never auto-kills idle jobs,
// since "no MLX activity for N min" can be a legitimately long
// inference; the user decides whether to cancel.
type JobStatus struct {
	ID          string `json:"id"`
	Pool        string `json:"pool"`
	Label       string `json:"label,omitempty"`
	Client      string `json:"client,omitempty"`
	SubmittedAt string `json:"submitted_at"`
	StartedAt   string `json:"started_at,omitempty"`
	Position    int    `json:"position,omitempty"`
	AgeSeconds  int    `json:"age_seconds,omitempty"`
	Idle        bool   `json:"idle,omitempty"`
}

// Config is the on-disk shape of the runner config (see DefaultConfigPath).
// Pools is keyed by name; an unset config means a single "default"
// pool with max_in_flight=1, which is enough to gate one project's
// work without any setup.
type Config struct {
	Pools map[string]PoolConfig `json:"pools"`
}

// PoolConfig holds the per-pool cap. Future fields (priority classes,
// rate limits) plug in here; clients tolerate unknown fields.
//
// IdleThresholdSeconds gates the #81 idle-flag in JobStatus. 0 means
// "use DefaultIdleThresholdSeconds" — projects opt out by setting an
// explicit large value (e.g. 86400). The threshold is per-pool because
// long-running planner work (35B) tolerates more silent decode time
// than fast coder turns (30B coder).
type PoolConfig struct {
	MaxInFlight          int `json:"max_in_flight"`
	IdleThresholdSeconds int `json:"idle_threshold_seconds,omitempty"`
}

// DefaultIdleThresholdSeconds is the fallback idle-flag threshold
// (10 minutes). Empirical basis: the cross-pool deadlock that
// motivated #81 took 16 min to surface; a 10 min flag would have
// alerted at the 10 min mark, well inside the recovery window.
const DefaultIdleThresholdSeconds = 600

// DefaultConfig is what the daemon uses when no config file is found
// or when the file lists no pools. One pool, capacity one — the
// minimal working setup; users opt in to multi-pool by writing a
// config file.
func DefaultConfig() Config {
	return Config{
		Pools: map[string]PoolConfig{
			"default": {MaxInFlight: 1},
		},
	}
}

// LoadConfig reads and validates a runner config from disk. A missing
// file is not an error — callers receive DefaultConfig() so the
// daemon Just Works on first run. A present-but-invalid file IS an
// error, so misconfiguration is loud rather than silently fallen back.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return Config{}, fmt.Errorf("read runner config %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse runner config %q: %w", path, err)
	}
	if len(cfg.Pools) == 0 {
		cfg = DefaultConfig()
	}
	for name, pc := range cfg.Pools {
		if strings.TrimSpace(name) == "" {
			return Config{}, fmt.Errorf("runner config %q: pool with empty name", path)
		}
		if pc.MaxInFlight <= 0 {
			return Config{}, fmt.Errorf("runner config %q: pool %q has max_in_flight=%d (must be > 0)", path, name, pc.MaxInFlight)
		}
	}
	return cfg, nil
}

// DefaultSocketPath returns where the daemon listens by default.
// Resolution order:
//  1. IDIOM_RUNNER_SOCKET env override (explicit)
//  2. $HOME/.idiom/runner.sock — the canonical install location.
//     Stable across sessions (terminal vs launchd vs SSH), discoverable,
//     and lives in user-controlled space rather than per-process TMPDIRs
//     that differ between launchd-spawned daemons and shell-launched
//     clients.
//  3. XDG_RUNTIME_DIR/idiom-runner.sock (Linux convention)
//  4. TMPDIR/idiom-runner.sock (last-resort fallback for unusual envs)
//
// The HOME fallback is the load-bearing change: prior to that, a
// daemon spawned by launchd and a client spawned from a user shell
// resolved different TMPDIRs under macOS and never connected.
func DefaultSocketPath() string {
	if env := strings.TrimSpace(os.Getenv("IDIOM_RUNNER_SOCKET")); env != "" {
		return env
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".idiom", "runner.sock")
	}
	if dir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); dir != "" {
		return filepath.Join(dir, "idiom-runner.sock")
	}
	tmp := strings.TrimSpace(os.Getenv("TMPDIR"))
	if tmp == "" {
		tmp = "/tmp"
	}
	return filepath.Join(tmp, "idiom-runner.sock")
}

// DefaultConfigPath returns where the daemon looks for its config by
// default. Honors IDIOM_RUNNER_CONFIG; otherwise ~/.idiom/runner.json.
// Returns empty string if HOME is not set and the env var is also
// missing — callers should treat that as "no config, use defaults".
func DefaultConfigPath() string {
	if env := strings.TrimSpace(os.Getenv("IDIOM_RUNNER_CONFIG")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".idiom", "runner.json")
}

// FormatTime renders a time for the wire protocol. Callers shouldn't
// inline time.RFC3339 — keeping the format in one place lets us shift
// to RFC3339Nano or a custom format later without touching every site.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
