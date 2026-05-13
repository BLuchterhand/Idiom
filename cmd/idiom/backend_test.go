package main

import (
	"bytes"
	"context"
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

// TestRunBackendListMissingFileExitsOne verifies the unified exit
// code: a missing backends.json is exit 1 across list/start/stop/
// status. Earlier list returned 0 ("informational"), but mixing
// exit codes for the same condition forced shell pipelines to
// special-case list — which Phase 4/5 review flagged as a UX bug.
func TestRunBackendListMissingFileExitsOne(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"list", "--project-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("missing backends.json must exit 1 (parity with start/stop/status), got %d", code)
	}
	if !strings.Contains(stderr.String(), "no backends.json at") {
		t.Fatalf("stderr should explain the missing file; got: %s", stderr.String())
	}
}

// TestRunBackendListRejectsBothFileAndProjectRoot verifies the
// mutual-exclusion contract added in the Phase 4/5 review fix.
// Earlier the list command silently ignored --project-root when
// --file was set; now passing both is a flag error (exit 2) so
// the operator gets clear feedback rather than wondering which
// flag won.
func TestRunBackendListRejectsBothFileAndProjectRoot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"list", "--file", "/x.json", "--project-root", "/y"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("--file + --project-root must exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "not both") {
		t.Fatalf("error should explain the conflict; got: %s", stderr.String())
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
// exit code. The Phase 4c subcommand (`proxy`) is now wired, so
// the unknown-subcommand probe uses a clearly-bogus name.
func TestRunBackendUnknownSubcommandExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"definitely-not-a-subcommand"}, &stdout, &stderr)
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

// withStubbedProxyServe swaps in a stub ServeProxy callback so the
// proxy CLI tests exercise flag parsing and config translation
// without binding a real port. The stub captures the resolved
// ProxyConfig the CLI computed so tests can assert on Listen /
// DefaultAlias / Routes after the call.
func withStubbedProxyServe(t *testing.T, serve func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error) {
	t.Helper()
	orig := backendServeProxy
	backendServeProxy = serve
	t.Cleanup(func() { backendServeProxy = orig })
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

func TestRunBackendStartUsesDefaultProfileWhenConfigured(t *testing.T) {
	root := t.TempDir()
	body := `{
		"default_profile": "qwen-pair",
		"profiles": {
			"qwen-pair": {"models": [{"alias": "qwen3.6-35b", "port": 47780}]},
			"qwen-27b-pair": {"models": [{"alias": "qwen3.6-27b", "port": 47783}]}
		}
	}`
	writeBackendsForRoot(t, root, body)
	var got []string
	withStubbedBackendOps(t,
		func(p backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
			for _, m := range p.Models {
				got = append(got, m.Alias)
			}
			return []backends.LaunchResult{{Alias: "qwen3.6-35b", PID: 1001, Port: 47780, Ready: true}}
		}, nil, nil)

	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"start", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("default-profile start must exit 0, got %d stderr=%s", code, stderr.String())
	}
	if len(got) != 1 || got[0] != "qwen3.6-35b" {
		t.Fatalf("launched aliases = %v, want default qwen-pair only", got)
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

// proxyCapturedConfig is what the proxy CLI tests assert on: the
// final ProxyConfig the CLI handed to ServeProxy after applying
// schema + flag overrides. Pulling it out into a helper keeps the
// test bodies focused on the contract (what got resolved) rather
// than the closure plumbing.
type proxyCapturedConfig struct {
	cfg    backends.ProxyConfig
	called bool
}

func captureProxyServe(t *testing.T) *proxyCapturedConfig {
	cap := &proxyCapturedConfig{}
	withStubbedProxyServe(t, func(_ context.Context, cfg backends.ProxyConfig, _ backends.ProxyOptions, ready func(string)) error {
		cap.cfg = cfg
		cap.called = true
		if ready != nil {
			ready("127.0.0.1:fake")
		}
		return nil
	})
	return cap
}

// TestRunBackendProxyServesProfileWithReadyLine is the canonical
// happy path: a complete profile resolves into a ProxyConfig that
// matches the schema and the CLI prints the LISTEN line from the
// ready callback. Exit 0 + STOPPED line confirm graceful shutdown.
func TestRunBackendProxyServesProfileWithReadyLine(t *testing.T) {
	root := t.TempDir()
	body := `{
  "profiles": {
    "qwen-pair": {
      "models": [
        {"alias": "qwen3.6-35b", "binary": "mlx_lm.server", "model_path": "/abs/q35", "port": 47780},
        {"alias": "qwen3-coder-30b", "binary": "mlx_lm.server", "model_path": "/abs/qcoder", "port": 47781}
      ],
      "proxy": {"listen": 47680, "default_alias": "qwen3.6-35b"}
    }
  }
}`
	writeBackendsForRoot(t, root, body)
	cap := captureProxyServe(t)

	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"proxy", "--project-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("proxy must exit 0 on clean shutdown, got %d (stderr=%s)", code, stderr.String())
	}
	if !cap.called {
		t.Fatal("ServeProxy stub was not invoked")
	}
	if cap.cfg.Listen != 47680 || cap.cfg.DefaultAlias != "qwen3.6-35b" || len(cap.cfg.Routes) != 2 {
		t.Fatalf("resolved ProxyConfig wrong: %+v", cap.cfg)
	}
	if !strings.Contains(stdout.String(), "LISTEN 127.0.0.1:fake") || !strings.Contains(stdout.String(), "STOPPED proxy") {
		t.Fatalf("stdout missing LISTEN/STOPPED markers: %s", stdout.String())
	}
}

// TestRunBackendProxyListenFlagOverridesSchema verifies the flag
// override contract: --listen wins over profile.proxy.listen so an
// operator can run the proxy on a different port without editing
// .idiom/backends.json.
func TestRunBackendProxyListenFlagOverridesSchema(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","model_path":"/m","port":1000}],"proxy":{"listen":47680,"default_alias":"a"}}}}`)
	cap := captureProxyServe(t)

	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"proxy", "--project-root", root, "--listen", "55555"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected 0, got %d (stderr=%s)", code, stderr.String())
	}
	if cap.cfg.Listen != 55555 {
		t.Fatalf("--listen override should win: got %d", cap.cfg.Listen)
	}
}

// TestRunBackendProxyDefaultFlagOverridesSchema covers the analog
// override for default_alias. A misnamed override surfaces as exit
// 2 with a helpful message rather than silently 404'ing requests.
func TestRunBackendProxyDefaultFlagOverridesSchema(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","model_path":"/m","port":1},{"alias":"c","binary":"b","model_path":"/n","port":2}],"proxy":{"listen":1234,"default_alias":"a"}}}}`)

	cap := captureProxyServe(t)
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"proxy", "--project-root", root, "--default", "c"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected 0, got %d (stderr=%s)", code, stderr.String())
	}
	if cap.cfg.DefaultAlias != "c" {
		t.Fatalf("--default override should win: got %q", cap.cfg.DefaultAlias)
	}

	// Bogus override path — must reject before serving.
	withStubbedProxyServe(t, func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
		t.Fatal("ServeProxy must not be called with a bogus --default")
		return nil
	})
	stdout.Reset()
	stderr.Reset()
	code = runBackendMain([]string{"proxy", "--project-root", root, "--default", "no-such"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("bogus --default must exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "not a registered alias") {
		t.Fatalf("error must explain mismatch: %s", stderr.String())
	}
}

// TestRunBackendProxyMissingListenExitsTwo covers the validation
// shim: a profile without proxy.listen and no --listen override
// must surface a clear flag error rather than binding port 0.
func TestRunBackendProxyMissingListenExitsTwo(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","model_path":"/m","port":1}]}}}`)
	withStubbedProxyServe(t, func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
		t.Fatal("ServeProxy must not be called when listen is unconfigured")
		return nil
	})

	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"proxy", "--project-root", root}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("missing listen must exit 2, got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "proxy.listen") {
		t.Fatalf("error must point at proxy.listen: %s", stderr.String())
	}
}

// TestRunBackendProxyMissingBackendsJSONExitsOne verifies the
// IO/setup-error contract is consistent with start/stop/status: a
// missing schema file is exit 1, the same code shell pipelines
// already special-case for the other backend subcommands.
func TestRunBackendProxyMissingBackendsJSONExitsOne(t *testing.T) {
	withStubbedProxyServe(t, func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
		t.Fatal("ServeProxy must not be called when backends.json is missing")
		return nil
	})
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"proxy", "--project-root", t.TempDir()}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("missing backends.json must exit 1, got %d", code)
	}
}

// TestRunBackendProxyBuildConfigFailureExitsOne verifies the
// downstream BuildProxyConfig validation surface (e.g. a model
// with no model_path) becomes exit 1, matching the "schema
// loaded but unusable" failure class.
func TestRunBackendProxyBuildConfigFailureExitsOne(t *testing.T) {
	root := t.TempDir()
	writeBackendsForRoot(t, root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","port":1}],"proxy":{"listen":1}}}}`)
	withStubbedProxyServe(t, func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
		t.Fatal("ServeProxy must not be called when BuildProxyConfig fails")
		return nil
	})

	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"proxy", "--project-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("BuildProxyConfig failure must exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "model_path") {
		t.Fatalf("error must explain the validation failure: %s", stderr.String())
	}
}
