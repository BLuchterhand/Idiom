package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/backends"
)

const sampleBackendsForCLI = `{
  "profiles": {
    "qwen-pair": {
      "models": [
        {"alias": "qwen3.6-35b", "binary": "mlx_lm.server", "port": 47780, "max_tokens": 8192},
        {"alias": "qwen3-coder-30b", "binary": "mlx_lm.server", "port": 47781, "max_tokens": 8192}
      ],
      "proxy": {"listen": 47680, "default_alias": "qwen3.6-35b", "default_coder": "qwen3-coder-30b"}
    }
  }
}`

// TestRunBackendListPrintsProfilesFromProjectRoot is the canonical
// "happy path" for the visibility command: a project with a valid
// backends.json gets a deterministic dump of profiles, models, and
// the resolved coder alias the lane wrapper would pick. Stable
// ordering matters because operators copy this output into bug
// reports — randomized map iteration would noise that signal.
func TestRunBackendListPrintsProfilesFromProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(sampleBackendsForCLI), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"list", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("backend list expected exit 0, got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	for _, marker := range []string{
		`profile "qwen-pair"`,
		`coder_alias="qwen3-coder-30b"`,
		`alias="qwen3.6-35b"`,
		`alias="qwen3-coder-30b"`,
		`listen=47680`,
	} {
		if !strings.Contains(out, marker) {
			t.Fatalf("backend list missing %q; full output:\n%s", marker, out)
		}
	}
}

// TestRunBackendListMissingFileExitsZero verifies the "no
// backends.json yet" path is non-fatal: a project that hasn't
// migrated to backends.json should still be able to run
// `idiom backend list` without an error exit, since the lane
// wrapper falls back to the hardcoded alias in that state.
func TestRunBackendListMissingFileExitsZero(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"list", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("missing backends.json must exit 0 (informational), got %d", code)
	}
	if !strings.Contains(stderr.String(), "no backends.json at") {
		t.Fatalf("stderr should explain the missing file; got: %s", stderr.String())
	}
}

// TestRunBackendListMalformedFileExitsOne verifies that a present-
// but-broken backends.json is loud, not silent. The user needs to
// see a parse error so they know to fix the file rather than
// wonder why the coder alias keeps falling back.
func TestRunBackendListMalformedFileExitsOne(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(`{`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"list", "--project-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("malformed backends.json must exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "backends:") {
		t.Fatalf("error must be tagged with package name; got: %s", stderr.String())
	}
}

// TestRunBackendUnknownSubcommandExitsTwo verifies the dispatch
// rejects unknown subcommands with the conventional flag-error
// exit code. Phase 4c will add `proxy`; until that ships,
// invoking it (or any typo) must surface a clear "unknown
// subcommand" error rather than silently falling through to list.
func TestRunBackendUnknownSubcommandExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"proxy"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("unknown subcommand must exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Fatalf("error must name the subcommand as unknown; got: %s", stderr.String())
	}
}

// TestRunBackendListExplicitFileFlag verifies the --file escape
// hatch: tooling that wants to dump a specific backends.json
// without changing cwd or installing a project root can pass an
// explicit path. This is the path debugging scripts and CI
// inspectors will use.
func TestRunBackendListExplicitFileFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom-backends.json")
	if err := os.WriteFile(path, []byte(sampleBackendsForCLI), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"list", "--file", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--file form expected exit 0, got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `profile "qwen-pair"`) {
		t.Fatalf("expected profile header in output: %s", stdout.String())
	}
}

// withStubbedBackendOps is a test helper that swaps in the stub
// Launch/Stop/Status callbacks and restores the originals on
// cleanup. Required because backend.go calls Launch/Stop/Status
// through package-level vars; tests must not let stubs leak
// across test cases.
func withStubbedBackendOps(t *testing.T, launch func(backends.Profile, backends.LauncherOptions) []backends.LaunchResult, stop func(backends.Profile, backends.StopOptions) []backends.StopResult, status func(backends.Profile, backends.StopOptions) []backends.StatusResult) {
	t.Helper()
	origLaunch, origStop, origStatus := backendLaunch, backendStop, backendStatus
	if launch != nil {
		backendLaunch = launch
	}
	if stop != nil {
		backendStop = stop
	}
	if status != nil {
		backendStatus = status
	}
	t.Cleanup(func() {
		backendLaunch, backendStop, backendStatus = origLaunch, origStop, origStatus
	})
}

// writeBackendsForRoot is a small helper that drops a sample
// backends.json into <root>/.idiom/ for the CLI tests below.
// Centralizing it avoids a copy-paste tax across tests and keeps
// the stub schema in one spot.
func writeBackendsForRoot(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestRunBackendStartReportsReadyForEachModel is the canonical
// happy path: a profile with two models that both come up clean
// produces two READY lines and exit 0. The stub Launch returns
// successful results so the CLI exercises only the formatting
// and exit-code-translation code path.
func TestRunBackendStartReportsReadyForEachModel(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, sampleBackendsForCLI)
	withStubbedBackendOps(t,
		func(_ backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
			return []backends.LaunchResult{
				{Alias: "qwen3.6-35b", PID: 1001, Port: 47780, Ready: true, LogPath: "/tmp/a.log"},
				{Alias: "qwen3-coder-30b", PID: 1002, Port: 47781, Ready: true, LogPath: "/tmp/b.log"},
			}
		}, nil, nil)
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"start", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("clean start must exit 0, got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"READY qwen3.6-35b", "READY qwen3-coder-30b", "pid=1001", "pid=1002"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
}

// TestRunBackendStartFailsWhenAnyModelTimesOut verifies the exit-
// code contract: if any backend fails to reach Ready, `start`
// exits non-zero so a calling shell knows to investigate. Other
// successful backends still report READY in the same run.
func TestRunBackendStartFailsWhenAnyModelTimesOut(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, sampleBackendsForCLI)
	withStubbedBackendOps(t,
		func(_ backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
			return []backends.LaunchResult{
				{Alias: "qwen3.6-35b", PID: 1001, Port: 47780, Ready: true, LogPath: "/tmp/a.log"},
				{Alias: "qwen3-coder-30b", PID: 1002, Port: 47781, Ready: false, LogPath: "/tmp/b.log"},
			}
		}, nil, nil)
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"start", "--project-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("partial failure must exit 1, got %d", code)
	}
	if !strings.Contains(stdout.String(), "TIMEOUT qwen3-coder-30b") {
		t.Fatalf("output should explain the timing-out backend: %s", stdout.String())
	}
}

// TestRunBackendStartTreatsAlreadyRunningAsNonFailure verifies
// the idempotent contract: a backend that's already listening on
// its port is reported as a NOTE (not READY, not FAIL), and the
// command still exits 0 since the desired end-state ("port has
// the right server") is satisfied.
func TestRunBackendStartTreatsAlreadyRunningAsNonFailure(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, sampleBackendsForCLI)
	withStubbedBackendOps(t,
		func(_ backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
			return []backends.LaunchResult{
				{Alias: "qwen3.6-35b", Port: 47780, Ready: true, Err: makeAlreadyRunningErr()},
				{Alias: "qwen3-coder-30b", Port: 47781, Ready: true, PID: 999, LogPath: "/tmp/x.log"},
			}
		}, nil, nil)
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"start", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("already-running must not fail; got code=%d stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "NOTE qwen3.6-35b") {
		t.Fatalf("already-running should report NOTE: %s", stdout.String())
	}
}

// TestRunBackendStartMissingBackendsJSONExitsOne verifies the
// IO/setup-error contract: exit 1 distinguishes "couldn't even
// load the schema" from "schema loaded but spawn failed" (which
// would be exit 1 too — both setup-class failures, distinct from
// flag-parse errors at exit 2).
func TestRunBackendStartMissingBackendsJSONExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"start", "--project-root", t.TempDir()}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("missing backends.json must exit 1, got %d", code)
	}
}

// TestRunBackendStartMultiProfileWithoutFlagExitsTwo verifies the
// disambiguation contract: a backends.json with multiple profiles
// must be told which one to launch via --profile. Picking one
// arbitrarily would risk launching the wrong stack.
func TestRunBackendStartMultiProfileWithoutFlagExitsTwo(t *testing.T) {
	root := t.TempDir()
	body := `{"profiles": {"a": {"models": []}, "b": {"models": []}}}`
	writeBackendsForRoot(t, root, body)
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"start", "--project-root", root}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("multi-profile without --profile must exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "multiple profiles") {
		t.Fatalf("error should explain ambiguity: %s", stderr.String())
	}
}

// TestRunBackendStopReportsResultsPerModel is the happy path for
// stop: each model's stop result becomes one output line, and
// any result carrying an error toggles the exit code.
func TestRunBackendStopReportsResultsPerModel(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, sampleBackendsForCLI)
	withStubbedBackendOps(t, nil,
		func(_ backends.Profile, _ backends.StopOptions) []backends.StopResult {
			return []backends.StopResult{
				{Alias: "qwen3.6-35b", Port: 47780, PID: 1001, Stopped: true},
				{Alias: "qwen3-coder-30b", Port: 47781, PID: 0, Stopped: true},
			}
		}, nil)
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"stop", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("clean stop must exit 0, got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "STOPPED qwen3.6-35b") || !strings.Contains(out, "FREE qwen3-coder-30b") {
		t.Fatalf("output should distinguish stopped vs already-free: %s", out)
	}
}

// TestRunBackendStatusFormatsUpAndDownLines verifies the visibility
// command. UP lines carry the PID; DOWN lines don't. Operators
// pipe this output into shell scripts, so the format is part of
// the contract.
func TestRunBackendStatusFormatsUpAndDownLines(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, sampleBackendsForCLI)
	withStubbedBackendOps(t, nil, nil,
		func(_ backends.Profile, _ backends.StopOptions) []backends.StatusResult {
			return []backends.StatusResult{
				{Alias: "qwen3.6-35b", Port: 47780, Up: true, PID: 5555},
				{Alias: "qwen3-coder-30b", Port: 47781, Up: false},
			}
		})
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"status", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status must exit 0, got %d (stderr=%s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "UP   qwen3.6-35b") || !strings.Contains(out, "pid=5555") {
		t.Fatalf("UP line missing or malformed: %s", out)
	}
	if !strings.Contains(out, "DOWN qwen3-coder-30b") {
		t.Fatalf("DOWN line missing: %s", out)
	}
}

// makeAlreadyRunningErr produces an error whose .Error() string
// starts with "backends: port" so the CLI's already-running
// detection has something realistic to format. The CLI picks the
// "NOTE" branch on Err != nil && Ready == true.
func makeAlreadyRunningErr() error {
	return &alreadyRunningErr{}
}

type alreadyRunningErr struct{}

func (e *alreadyRunningErr) Error() string {
	return "backends: port 47780 already in use; assuming qwen3.6-35b is already running"
}
