package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// briefPreflightTestDeps locates the brief-preflight.py source on disk and
// verifies python3 is on PATH. Returns the absolute script path. The tests
// run the script directly (rather than going through runBriefLint's
// embedded-script bootstrap) so that cmd.Dir can be set to a fake workspace
// — that's how resolve_workspace_root(Path.cwd()) sees the right root.
func briefPreflightTestDeps(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available on PATH")
	}
	scriptPath, err := filepath.Abs("brief-preflight.py")
	if err != nil {
		t.Fatalf("resolve brief-preflight.py path: %v", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("brief-preflight.py not found at %s: %v", scriptPath, err)
	}
	return scriptPath
}

// runPreflight invokes brief-preflight.py with the brief at briefPath, using
// workspace as the subprocess cwd. The cwd matters: resolve_workspace_root
// walks up from Path.cwd() looking for .git, which is how the script
// decides where to verify named files. Returns (exitCode, combinedOutput).
func runPreflight(t *testing.T, scriptPath, briefPath, workspace string) (int, string) {
	t.Helper()
	cmd := exec.Command("python3", scriptPath, briefPath)
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	exit := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exit = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("preflight subprocess failed: %v (output=%s)", err, out)
	}
	return exit, string(out)
}

// makeWorkspace builds a temp directory with a .git marker so that
// resolve_workspace_root picks it as the workspace root. Additional files
// keyed by relative path are written under it.
func makeWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func writeLintBrief(t *testing.T, dir, content string) string {
	t.Helper()
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}
	return briefPath
}

// TestBriefLintParsesParentheticalHeading verifies the parser bug fix: the
// canonical brief skeleton heading "## Files to Modify (EXHAUSTIVE
// allowlist)" must be recognized as the file-allowlist section. Before the
// fix, normalize_heading produced "files to modify (exhaustive allowlist)"
// which never matched ALLOWLIST_HEADINGS, so every brief following the
// template silently passed lint with zero rows parsed.
func TestBriefLintParsesParentheticalHeading(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
		"## Verified Anchors\n\n"+
		"Confirmed: `Existing` is at line 2.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected lint OK on a well-formed brief, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "1 file-allowlist row(s)") {
		t.Fatalf("parser must recognize parenthetical heading and parse the row: %s", out)
	}
}

// TestBriefLintBlocksProseAnchorInAllowlist confirms the new prose-anchor
// rule fires on "near top of file" in a Files-to-Modify row — the exact
// shape that wedged the falldetect brief while it still passed
// has_exact_anchor_for_path (which accepts a separate "line N" mention as
// sufficient).
func TestBriefLintBlocksProseAnchorInAllowlist(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` near top of file (line 2) |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected lint FAIL on prose anchor, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "prose anchor in allowlist row") {
		t.Fatalf("stderr should name the prose-anchor failure: %s", out)
	}
	if !strings.Contains(out, "near top of file") {
		t.Fatalf("stderr should echo the offending phrase: %s", out)
	}
}

// TestBriefLintWarnsOnBranchingInRequiredEdit confirms branching language
// inside a ### Edit section emits a WARN but does not block. Required Edits
// should be unconditional; branching forces the planner to design at run
// time.
func TestBriefLintWarnsOnBranchingInRequiredEdit(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
		"## Required Edits\n\n"+
		"### Edit 1 — `bar.go` add Added\n\n"+
		"Either add the function alongside Existing or define it in a new file.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("branching should WARN, not FAIL, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "branching language in Required Edit") {
		t.Fatalf("stderr should carry the branching WARN: %s", out)
	}
}

// TestBriefLintBlocksDiscoveryMandate confirms the new discovery-mandate
// rule fires on imperative "run X and use the output" phrasing. The
// brief-drafter owns discovery; the lane should not be asked to run lookup
// tools and consume their output.
func TestBriefLintBlocksDiscoveryMandate(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
		"## Discovery\n\nFirst, run idiom tool lookup and inline the output here.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected lint FAIL on discovery mandate, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "discovery-mandate") {
		t.Fatalf("stderr should carry the discovery-mandate failure: %s", out)
	}
}

// TestBriefLintAnchorVerificationFileExists confirms file-existence
// verification passes when the named file is on disk under the resolved
// workspace root.
func TestBriefLintAnchorVerificationFileExists(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK when named file exists, got exit=%d output=%s", exit, out)
	}
	if strings.Contains(out, "anchor verification skipped") {
		t.Fatalf("verification should run when the file exists: %s", out)
	}
}

// TestBriefLintAnchorVerificationFileMissing confirms file-existence
// verification BLOCKS when the named file is absent under the resolved
// workspace root but at least one other named file does exist (i.e. lint
// is in the right repo; one specific path is stale).
func TestBriefLintAnchorVerificationFileMissing(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n"+
		"| `internal/foo/missing.go` | ADD `func Other()` at line 1 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL when a named file is missing, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "file not found in workspace") {
		t.Fatalf("stderr should name the missing-file failure: %s", out)
	}
	if !strings.Contains(out, "internal/foo/missing.go") {
		t.Fatalf("stderr should name the specific missing path: %s", out)
	}
}

// TestBriefLintAnchorVerificationOldTextMatches confirms a labeled
// `old_text:` block that exactly matches the file passes verification.
func TestBriefLintAnchorVerificationOldTextMatches(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\n\nfunc Existing() int {\n\treturn 0\n}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | MODIFY `Existing` body at line 4 |\n\n"+
		"## Current state\n\nLine 4 returns 0.\n\n"+
		"## Required Edits\n\n"+
		"### Edit 1 — `bar.go` change return value\n\n"+
		"old_text:\n"+
		"```text\n"+
		"func Existing() int {\n"+
		"\treturn 0\n"+
		"}\n"+
		"```\n\n"+
		"new_text:\n"+
		"```text\n"+
		"func Existing() int {\n"+
		"\treturn 1\n"+
		"}\n"+
		"```\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK when old_text matches file, got exit=%d output=%s", exit, out)
	}
	if strings.Contains(out, "does not match") {
		t.Fatalf("verification should not report a mismatch: %s", out)
	}
}

// TestBriefLintAnchorVerificationOldTextStale confirms a labeled
// `old_text:` block that does NOT appear in the named file fails
// verification — the canonical stale-anchor catch.
func TestBriefLintAnchorVerificationOldTextStale(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\n\nfunc Existing() int {\n\treturn 0\n}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | MODIFY `Existing` body at line 4 |\n\n"+
		"## Current state\n\nLine 4 returns 0.\n\n"+
		"## Required Edits\n\n"+
		"### Edit 1 — `bar.go` change return value\n\n"+
		"old_text:\n"+
		"```text\n"+
		"func Existing() int {\n"+
		"\treturn 42\n"+
		"}\n"+
		"```\n\n"+
		"new_text:\n"+
		"```text\n"+
		"func Existing() int {\n"+
		"\treturn 1\n"+
		"}\n"+
		"```\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL on stale old_text, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "does not match") {
		t.Fatalf("stderr should name the stale-anchor failure: %s", out)
	}
}

// TestBriefLintAnchorVerificationWorkspaceSkipsCleanly confirms that when
// no named file exists under the resolved workspace root (e.g. lint is
// running outside the target repo), verification skips with a WARN rather
// than flooding existence failures.
func TestBriefLintAnchorVerificationWorkspaceSkipsCleanly(t *testing.T) {
	script := briefPreflightTestDeps(t)
	// Workspace has a .git marker but none of the brief's named paths.
	ws := makeWorkspace(t, map[string]string{
		"unrelated.go": "package main\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `native/src/foo.cpp` | ADD `void bar()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 of native/src/foo.cpp.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK with workspace-skip WARN, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "anchor verification skipped") {
		t.Fatalf("stderr should carry the workspace-skip WARN: %s", out)
	}
}
