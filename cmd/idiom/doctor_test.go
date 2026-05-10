package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunDoctorMainEmitsTextReport verifies the human-readable format
// — a status-prefixed line per check and a summary line. Test pins
// only the structural anchors (status icons, "summary:", "next steps:")
// so check copy can evolve without breaking the contract.
func TestRunDoctorMainEmitsTextReport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)         // doctor uses cwd for path resolution
	t.Setenv("PATH", "") // force backend check to fail so we exercise remedy emission

	var stdout, stderr bytes.Buffer
	code := runDoctorMain(nil, &stdout, &stderr)
	out := stdout.String()
	if code != 1 {
		t.Fatalf("expected exit 1 (backend missing is a fail), got %d; stdout=%s stderr=%s", code, out, stderr.String())
	}
	for _, marker := range []string{"[FAIL]", "summary:", "next steps:"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("doctor text output missing %q; got: %s", marker, out)
		}
	}
}

// TestRunDoctorMainJSONFormat verifies --json emits one parseable JSON
// object with the expected top-level shape. Wrappers (CI, dashboards)
// consume this; structural drift breaks every consumer.
func TestRunDoctorMainJSONFormat(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Stage a config so checkConfigFile passes — we want non-fail
	// results too, not just failures, to verify the JSON encoder handles
	// every status.
	cfg := filepath.Join(dir, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	_ = runDoctorMain([]string{"--json"}, &stdout, &stderr)

	var got struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
			Remedy string `json:"remedy"`
		} `json:"checks"`
		Summary struct {
			Pass int `json:"pass"`
			Warn int `json:"warn"`
			Fail int `json:"fail"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("doctor --json output not parseable: %v\noutput: %s", err, stdout.String())
	}
	if len(got.Checks) == 0 {
		t.Fatalf("expected at least one check in JSON output")
	}
	total := got.Summary.Pass + got.Summary.Warn + got.Summary.Fail
	if total != len(got.Checks) {
		t.Fatalf("summary counts (%d) don't match check count (%d)", total, len(got.Checks))
	}
}

// TestCheckBackendsFailsWhenPathEmpty forces an empty PATH and verifies
// the backend check returns "fail" with a remedy that names the
// supported runners. Doctor's first job is to get a fresh user from
// "broken" to "fixed" with one actionable line.
func TestCheckBackendsFailsWhenPathEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH manipulation differs on windows; backend check is unix-host today")
	}
	t.Setenv("PATH", "")
	r := checkBackends()
	if r.status != "fail" {
		t.Fatalf("expected fail with empty PATH, got %s (%s)", r.status, r.detail)
	}
	if r.remedy == "" {
		t.Fatalf("backend fail must carry a remedy line")
	}
	if !strings.Contains(r.remedy, "mlx_lm.server") {
		t.Fatalf("remedy should name a candidate runner; got: %s", r.remedy)
	}
}

// TestCheckConfigFileMissing verifies a missing config produces a fail
// pointing at `idiom init`. This is the path a fresh user hits before
// running init at all.
func TestCheckConfigFileMissing(t *testing.T) {
	dir := t.TempDir()
	r := checkConfigFile(filepath.Join(dir, "nonexistent.json"))
	if r.status != "fail" {
		t.Fatalf("expected fail for missing config, got %s", r.status)
	}
	if !strings.Contains(r.remedy, "idiom init") {
		t.Fatalf("remedy should point at idiom init; got: %s", r.remedy)
	}
}

// TestCheckConfigFileInvalidJSON verifies a malformed config surfaces
// the parse error as fail with a syntax-fix remedy.
func TestCheckConfigFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "config.json")
	if err := os.WriteFile(bad, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("stage bad config: %v", err)
	}
	r := checkConfigFile(bad)
	if r.status != "fail" {
		t.Fatalf("expected fail for malformed config, got %s", r.status)
	}
	if !strings.Contains(r.detail, "not valid JSON") {
		t.Fatalf("detail should call out the JSON parse failure; got: %s", r.detail)
	}
}

// TestCheckWorkspaceWritablePassesWhenWritable verifies the happy path
// — a config with a writable workspace.root produces a pass with the
// path in the detail line.
func TestCheckWorkspaceWritablePassesWhenWritable(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
	r := checkWorkspaceWritable(cfg)
	if r.status != "pass" {
		t.Fatalf("expected pass with writable workspace, got %s (%s)", r.status, r.detail)
	}
	if !strings.Contains(r.detail, dir) {
		t.Fatalf("expected detail to name the workspace root; got: %s", r.detail)
	}
}

// TestCheckWorkspaceWritableFailsOnReadOnly verifies the failure path
// surfaces both detail and remedy. Tested by chmod'ing the workspace
// to read-only; skipped on platforms where the test process retains
// override permissions (root, some CI envs).
func TestCheckWorkspaceWritableFailsOnReadOnly(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod doesn't constrain writes")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
	r := checkWorkspaceWritable(cfg)
	if r.status != "fail" {
		t.Fatalf("expected fail for read-only workspace, got %s (%s)", r.status, r.detail)
	}
	if r.remedy == "" {
		t.Fatalf("remedy should be set on fail")
	}
}
