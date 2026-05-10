package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestRunBackendListUnknownSubcommandExitsTwo verifies the dispatch
// rejects unknown subcommands with the conventional flag-error
// exit code. Phase 4b adds start/stop/proxy; until they ship,
// invoking them must surface a clear "unknown subcommand" error
// rather than silently falling through to list.
func TestRunBackendListUnknownSubcommandExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBackendMain([]string{"start"}, &stdout, &stderr)
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
