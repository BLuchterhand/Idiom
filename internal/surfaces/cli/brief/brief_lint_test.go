package brief

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// briefPreflightTestDeps materializes the embedded brief-preflight.py script
// and verifies python3 is on PATH. The tests run the script directly so
// cmd.Dir can be set to a fake workspace; that's how resolve_workspace_root
// sees the right root.
func briefPreflightTestDeps(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available on PATH")
	}
	scriptPath, cleanup, err := writeEmbeddedBriefPreflight()
	if err != nil {
		t.Fatalf("materialize brief-preflight.py: %v", err)
	}
	t.Cleanup(cleanup)
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

// withCanonicalSections appends stub `## Required Edits` and/or
// `## Acceptance` sections to a test brief body if absent, so the
// structural-completeness check added 2026-05-11 (Colony Bug 7) doesn't
// block specific-behavior tests that focus on a different lint dimension.
// Tests that want to verify the structural check itself should NOT use
// this helper.
func withCanonicalSections(body string) string {
	out := body
	if !strings.Contains(out, "## Required Edits") && !strings.Contains(out, "## Required Behavior") {
		out += "\n## Required Edits\n\nApply the edit named in the Files to Modify row.\n"
	}
	if !strings.Contains(out, "## Acceptance") {
		out += "\n## Acceptance\n\n- `go build ./...` succeeds.\n"
	}
	return out
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
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
		"## Verified Anchors\n\n"+
		"Confirmed: `Existing` is at line 2.\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected lint OK on a well-formed brief, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "1 file-allowlist row(s)") {
		t.Fatalf("parser must recognize parenthetical heading and parse the row: %s", out)
	}
}

func TestBriefLintParsesScopeFilesHeading(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"scripts/verify_atom.sh": "#!/usr/bin/env bash\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"## Scope\n\n"+
		"### Files\n"+
		"| File | Change |\n"+
		"| --- | --- |\n"+
		"| `scripts/verify_atom.sh` | MODIFY trace audit at line 1 |\n\n"+
		"## Verified Current State\n\nLine 1 is `#!/usr/bin/env bash`.\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected lint OK on Scope/Files allowlist, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "1 file-allowlist row(s)") {
		t.Fatalf("parser must recognize Scope/Files and parse the row: %s", out)
	}
}

func TestBriefLintBlocksScopeFilesBriefWithoutAnchoredSections(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"scripts/verify_atom.sh": "#!/usr/bin/env bash\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Scope\n\n"+
		"### Files\n"+
		"| File | Change |\n"+
		"| --- | --- |\n"+
		"| `scripts/verify_atom.sh` | Add the active testing coverage atom trace audit |\n\n"+
		"## Changes\n\n"+
		"1. Add a small helper block in `scripts/verify_atom.sh`.\n\n"+
		"## Tests\n\n"+
		"- `bash -n scripts/verify_atom.sh`\n\n"+
		"## Acceptance\n\n"+
		"1. `bash -n scripts/verify_atom.sh`\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected lint FAIL on Scope/Files brief missing anchored sections, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "required section missing for anchored-edits mode") {
		t.Fatalf("stderr should report anchored structural failure: %s", out)
	}
	if strings.Contains(out, "brief shape is unknown") {
		t.Fatalf("Scope/Files should be classified as anchored, not unknown: %s", out)
	}
}

func TestBriefLintBlocksUnknownBriefShape(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, nil)
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Scope\n\n"+
		"This is not an implementation allowlist.\n\n"+
		"## Acceptance\n\n"+
		"- `go test ./...`\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected lint FAIL on unknown brief shape, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "brief shape is unknown") {
		t.Fatalf("stderr should report unknown brief shape: %s", out)
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
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
		"## Required Edits\n\n"+
		"### Edit 1 — `bar.go` add Added\n\n"+
		"Either add the function alongside Existing or define it in a new file.\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("branching should WARN, not FAIL, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "branching language in Required Edit") {
		t.Fatalf("stderr should carry the branching WARN: %s", out)
	}
}

func TestBriefLintDoesNotWarnOnMultipleReadFirstAnchors(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\n\nfunc Existing() {}\n\n\n\n\nfunc Other() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Read First\n\n"+
		"- `internal/foo/bar.go` line 1 for package context.\n"+
		"- `internal/foo/bar.go` line 8 for nearby helper context.\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` after the verified helper context |\n\n"+
		"## Verified Current State\n\n"+
		"`internal/foo/bar.go` line 1 is `package foo` and line 8 is `func Other() {}`.\n\n"+
		"## Required Edits\n\n"+
		"old_text:\n"+
		"```text\n"+
		"func Other() {}\n"+
		"```\n\n"+
		"new_text:\n"+
		"```text\n"+
		"func Other() {}\n\n"+
		"func Added() {}\n"+
		"```\n\n"+
		"## Acceptance\n\n"+
		"- `go build ./...` succeeds.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("read-first anchors should not fail lint, got exit=%d output=%s", exit, out)
	}
	if strings.Contains(out, "structure-describing anchors") {
		t.Fatalf("read-first/current-state line evidence should not trigger edit-contract warning: %s", out)
	}
}

func TestBriefLintWarnsOnMultipleRequiredEditAnchors(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\n\nfunc Existing() {}\n\n\n\n\nfunc Other() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Read First\n\n"+
		"- `internal/foo/bar.go` line 1 for package context.\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` after the verified helper context |\n\n"+
		"## Verified Current State\n\n"+
		"`internal/foo/bar.go` line 1 is `package foo`.\n\n"+
		"## Required Edits\n\n"+
		"In `internal/foo/bar.go`, combine line 1 and line 8 into one replacement window.\n\n"+
		"## Acceptance\n\n"+
		"- `go build ./...` succeeds.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("structure anchor issue should WARN, not FAIL, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "structure-describing anchors") {
		t.Fatalf("required-edit line soup should still trigger the warning: %s", out)
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
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | ADD `func Added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n"))
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

// TestBriefLintAnchorVerificationSkipsExplicitNewFileRows confirms
// new-file wording in the Files-to-Modify action suppresses existence
// verification for paths that are intentionally being created, without
// requiring the action to start with the old magic "new file:" prefix.
func TestBriefLintAnchorVerificationSkipsExplicitNewFileRows(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | MODIFY `Existing` at line 2 |\n"+
		"| `scripts/new_guard.sh` | Create this new shell helper. |\n"+
		"| `.idiom/postflight/checks/new_guard.json` | New file target. Create the idiom project check JSON. |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK with explicit new-file rows skipped, got exit=%d output=%s", exit, out)
	}
	if strings.Contains(out, "file not found in workspace") {
		t.Fatalf("new-file rows should not be treated as stale paths: %s", out)
	}
}

func TestBriefLintPlanExecuteNewSourceFileRequiresInlineBody(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
		"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"Routing: idiom plan-execute\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `native/tests/test_new_pairwise.cpp` | Create new file: focused pairwise test owner. |\n\n"+
		"## Current state\n\n"+
		"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
		"## Required Behavior\n\n"+
		"Create `native/tests/test_new_pairwise.cpp` starting at line 1.\n\n"+
		"## Acceptance\n\n"+
		"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL for plan-execute new source file without body, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "plan-execute new-file body missing") {
		t.Fatalf("stderr should name missing new-file body failure: %s", out)
	}
}

func TestBriefLintPlanExecuteNewSourceFileAcceptsInlineBody(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
		"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"Routing: idiom plan-execute\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `native/tests/test_new_pairwise.cpp` | Create new file: focused pairwise test owner. |\n\n"+
		"## Current state\n\n"+
		"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
		"## Required Behavior\n\n"+
		"Create `native/tests/test_new_pairwise.cpp` starting at line 1 with this body:\n\n"+
		"```text\n"+
		"#include \"doctest.h\"\n"+
		"\n"+
		"using namespace colony;\n"+
		"\n"+
		"namespace {\n"+
		"int helper() { return 1; }\n"+
		"}\n"+
		"\n"+
		"TEST_SUITE(\"NewPairwise\") {\n"+
		"TEST_CASE(\"covers new pairwise owner\") {\n"+
		"    CHECK(helper() == 1);\n"+
		"}\n"+
		"}\n"+
		"```\n\n"+
		"## Acceptance\n\n"+
		"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK with inline new-file body, got exit=%d output=%s", exit, out)
	}
	if strings.Contains(out, "plan-execute new-file body missing") {
		t.Fatalf("inline body should satisfy new-file plan-execute gate: %s", out)
	}
}

func TestBriefLintPlanExecuteNewSourceFileAcceptsReplaceAllShape(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
		"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"Routing: idiom plan-execute\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `native/tests/test_new_pairwise.cpp` | Create focused pairwise test owner. |\n\n"+
		"## Current state\n\n"+
		"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
		"## Required Behavior\n\n"+
		"Use this explicit replacement shape for `native/tests/test_new_pairwise.cpp`:\n\n"+
		"```text\n"+
		"{\"path\":\"native/tests/test_new_pairwise.cpp\",\"replace_all\":true,\"old_text\":\"\",\"new_text\":\"#include \\\"doctest.h\\\"\\n\\nTEST_CASE(\\\"covers new pairwise owner\\\") {\\n    CHECK(1 == 1);\\n}\\n\",\"purpose\":\"Create the new focused pairwise test owner.\"}\n"+
		"```\n\n"+
		"## Acceptance\n\n"+
		"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK with explicit replace_all shape, got exit=%d output=%s", exit, out)
	}
}

func TestBriefLintImplementationNewSourceFileAllowsImplementationShape(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"native/tests/test_existing.cpp": "#include \"doctest.h\"\nTEST_CASE(\"existing\") {}\n",
		"native/tests/run_tests.py":      "#!/usr/bin/env python3\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"Routing: idiom implement\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `native/tests/test_new_pairwise.cpp` | Create new file: focused pairwise test owner. |\n\n"+
		"## Current state\n\n"+
		"`native/tests/test_existing.cpp:L1-L2` defines the nearest existing suite.\n\n"+
		"## Required Behavior\n\n"+
		"Create `native/tests/test_new_pairwise.cpp` with a doctest suite named NewPairwise.\n"+
		"This implementation-lane brief mentions `submit_plan` only to warn that the implementation route must not use it.\n\n"+
		"## Acceptance\n\n"+
		"- `python3 native/tests/run_tests.py -- -ts=NewPairwise`\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("implementation lane should not require a full new-file body, got exit=%d output=%s", exit, out)
	}
}

// TestBriefLintAnchorVerificationOldTextMatches confirms a labeled
// `old_text:` block that exactly matches the file passes verification.
func TestBriefLintAnchorVerificationOldTextMatches(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\n\nfunc Existing() int {\n\treturn 0\n}\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
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
		"```\n"))
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

func TestBriefLintBlocksCommandBackedMutationWithoutEditShape(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"docs/map.md":             "# old map\n",
		"scripts/generate_map.py": "print('new map')\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `docs/map.md` | MODIFY generated map at line 1 |\n\n"+
		"## Verified Current State\n\n"+
		"- `docs/map.md:1`: `# old map`\n\n"+
		"## Required Behavior\n\n"+
		"1. Run `python3 scripts/generate_map.py` to regenerate `docs/map.md`.\n\n"+
		"## Acceptance\n\n"+
		"- `git diff --check`\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL for command-backed mutation without edit shape, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "mutating command") || !strings.Contains(out, "explicit edit shape") {
		t.Fatalf("stderr should explain command-backed mutation contract, got: %s", out)
	}
}

func TestBriefLintAllowsDomainFunctionCallWithRefreshWording(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"tests/e2e/suites/stockpile_flow_suite.gd": "func _run_case() -> void:\n\tpass\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files To Modify\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `tests/e2e/suites/stockpile_flow_suite.gd` | MODIFY test case at line 1 |\n\n"+
		"## Verified Current State\n\n"+
		"- `tests/e2e/suites/stockpile_flow_suite.gd:1`: `func _run_case() -> void:`\n\n"+
		"## Required Behavior\n\n"+
		"1. Obtain the real stockpile panel, call `show_stockpile(stockpile_id)`, refresh panel data, and assert panel-visible fields reflect the mutated native state.\n\n"+
		"## Acceptance\n\n"+
		"- `git diff --check`\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("domain function calls with refresh wording should not be classified as shell-backed mutation, got exit=%d output=%s", exit, out)
	}
}

func TestBriefLintBlocksProseAppendWithoutEditShape(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"docs/tracker.md": "- [x] Atom 1 shipped.\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `docs/tracker.md` | MODIFY tracker at line 1 |\n\n"+
		"## Verified Current State\n\n"+
		"- `docs/tracker.md:1`: `- [x] Atom 1 shipped.`\n\n"+
		"## Required Behavior\n\n"+
		"1. Append the Atom 2 entry after line 1 in `docs/tracker.md`.\n\n"+
		"## Acceptance\n\n"+
		"- `git diff --check`\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL for prose append without edit shape, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "prose-only mutation site") || !strings.Contains(out, "old_text/new_text") {
		t.Fatalf("stderr should explain prose-anchor mutation contract, got: %s", out)
	}
}

func TestBriefLintAnchorVerificationOldTextAmbiguousFails(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"internal/foo/bar.go": "package foo\n\nfunc helper() {}\nfunc helper() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/foo/bar.go` | MODIFY first helper at line 3 |\n\n"+
		"## Current state\n\nLine 3 is `func helper() {}`.\n\n"+
		"## Required Edits\n\n"+
		"### Edit 1 — `bar.go` change first helper\n\n"+
		"old_text:\n"+
		"```text\n"+
		"func helper() {}\n"+
		"```\n\n"+
		"new_text:\n"+
		"```text\n"+
		"func helper() { return }\n"+
		"```\n\n"+
		"## Acceptance\n\n- `go test ./internal/foo` passes.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL on ambiguous old_text, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "anchor not unique") {
		t.Fatalf("stderr should name the ambiguous-anchor failure: %s", out)
	}
}

func TestBriefLintBlocksForeignTestMarkerForGDScript(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"ui/panel.gd": "extends Control\n\nfunc _ready() -> void:\n\tpass\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `ui/panel.gd` | MODIFY `_ready` at line 3 |\n\n"+
		"## Current state\n\nLine 3 is `func _ready() -> void:`.\n\n"+
		"## Required Edits\n\n"+
		"### Edit 1 — `panel.gd` wire ready\n\n"+
		"Change `_ready` using the verified line anchor.\n\n"+
		"## Acceptance\n\n"+
		"- Add a doctest-style `TEST_CASE` for this GDScript behavior.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL on GDScript/C++ test marker mismatch, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "language/profile mismatch") || !strings.Contains(out, "TEST_CASE") {
		t.Fatalf("stderr should report foreign test marker: %s", out)
	}
}

func TestBriefLintBlocksMissingAcceptanceCommandPath(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"scripts/existing.sh": "#!/usr/bin/env bash\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `scripts/existing.sh` | MODIFY line 1 |\n\n"+
		"## Current state\n\nLine 1 is `#!/usr/bin/env bash`.\n\n"+
		"## Required Edits\n\n"+
		"Apply the edit named in the Files to Modify row.\n\n"+
		"## Acceptance\n\n"+
		"- `bash scripts/missing.sh` passes.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected FAIL on missing command path, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "acceptance command references missing path") {
		t.Fatalf("stderr should report missing command path: %s", out)
	}
}

func TestBriefLintAllowsAcceptanceCommandForDeclaredNewFileTarget(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"scripts/run_scenario.sh": "#!/usr/bin/env bash\n",
	})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"Routing: idiom plan-execute\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `scripts/run_scenario.sh` | MODIFY validation at line 1 |\n"+
		"| `tests/scenarios/smoke/wild_wolf_seed.json` | New file target. Add a scenario fixture. |\n\n"+
		"## Verified Current State\n\n"+
		"- `scripts/run_scenario.sh:1`: `#!/usr/bin/env bash`\n\n"+
		"## Required Behavior\n\n"+
		"Use this explicit replacement shape for `tests/scenarios/smoke/wild_wolf_seed.json`:\n\n"+
		"```text\n"+
		"{\"path\":\"tests/scenarios/smoke/wild_wolf_seed.json\",\"replace_all\":true,\"old_text\":\"\",\"new_text\":\"{\\n  \\\"name\\\": \\\"wild_wolf_seed\\\",\\n  \\\"actions\\\": [\\n    {\\\"type\\\": \\\"debug_seed_animal\\\", \\\"species\\\": \\\"Wolf\\\"}\\n  ]\\n}\\n\",\"purpose\":\"Create the new scenario fixture.\"}\n"+
		"```\n\n"+
		"## Acceptance\n\n"+
		"- `bash scripts/run_scenario.sh tests/scenarios/smoke/wild_wolf_seed.json`\n"+
		"- `git diff --check -- scripts/run_scenario.sh tests/scenarios/smoke/wild_wolf_seed.json`\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK when commands reference a declared new-file target, got exit=%d output=%s", exit, out)
	}
	if strings.Contains(out, "acceptance command references missing path") {
		t.Fatalf("declared new-file target should satisfy command-path validation: %s", out)
	}
}

func TestBriefLintAllowsAcceptanceCommandForDeclaredNewFilePackageDir(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{})
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"Routing: idiom plan-execute\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `internal/contracts/writerroute/decision.go` | NEW FILE: add writer route package. |\n\n"+
		"## Verified Current State\n\n"+
		"- `internal/contracts/writerroute/decision.go` is a new package file.\n\n"+
		"## Required Behavior\n\n"+
		"Create `internal/contracts/writerroute/decision.go` with an explicit replacement shape:\n\n"+
		"```text\n"+
		"{\"path\":\"internal/contracts/writerroute/decision.go\",\"replace_all\":true,\"old_text\":\"\",\"new_text\":\"package writerroute\\n\\nconst SchemaVersion = \\\"idiom.writer_route.v1\\\"\\n\",\"purpose\":\"Create the new writer route decision package.\"}\n"+
		"```\n\n"+
		"## Acceptance\n\n"+
		"- `go test ./internal/contracts/writerroute`\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK when command references declared new-file package dir, got exit=%d output=%s", exit, out)
	}
	if strings.Contains(out, "acceptance command references missing path") {
		t.Fatalf("new-file package dir should satisfy command-path validation: %s", out)
	}
}

func TestBriefLintIgnoresAbsoluteAcceptanceCommandPath(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"scripts/existing.sh": "#!/usr/bin/env bash\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `scripts/existing.sh` | MODIFY line 1 |\n\n"+
		"## Current state\n\nLine 1 is `#!/usr/bin/env bash`.\n\n"+
		"## Required Edits\n\n"+
		"Apply the edit named in the Files to Modify row.\n\n"+
		"## Acceptance\n\n"+
		"- `bash /tmp/idiom-missing-helper.sh` remains an external harness command.\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK when command path is absolute/external, got exit=%d output=%s", exit, out)
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
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `native/src/foo.cpp` | ADD `void bar()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 of native/src/foo.cpp.\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected OK with workspace-skip WARN, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "anchor verification skipped") {
		t.Fatalf("stderr should carry the workspace-skip WARN: %s", out)
	}
}

// buildInlinedContent constructs a ```text fence with `lineCount` lines
// of ~25-char content each, for sizing the atom-size estimator near a
// known threshold.
func buildInlinedContent(lineCount int) string {
	var b strings.Builder
	b.WriteString("```text\n")
	for i := 0; i < lineCount; i++ {
		b.WriteString("    return 42 // synth line\n")
	}
	b.WriteString("```")
	return b.String()
}

// TestBriefLintAtomSizeWarnsAroundHalfMaxTokens verifies the new
// pre-dispatch atom-size estimator: an inlined-content payload that
// approaches half the planner's max_tokens cap emits a WARN with a
// decomposition hint (top-3 heaviest fences) but does not fail.
func TestBriefLintAtomSizeWarnsAroundHalfMaxTokens(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"cmd/idiom/lane.go": "package main\nfunc Existing() {}\n",
	})
	// ~600 lines × 25 chars × 1.2 / 4 = ~4500 tokens → between
	// WARN (4096) and FAIL (6553) at the default 8192 cap.
	brief := writeLintBrief(t, ws, withCanonicalSections("# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `cmd/idiom/lane.go` | ADD `func added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
		buildInlinedContent(600)+"\n"))
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 0 {
		t.Fatalf("expected exit=0 (WARN only), got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "atom-size estimate") {
		t.Fatalf("expected atom-size WARN in output: %s", out)
	}
	if !strings.Contains(out, "Heaviest inlined blocks") {
		t.Fatalf("expected decomposition hint in WARN: %s", out)
	}
}

// TestBriefLintAtomSizeFailsAtEightyPercentCap verifies a brief whose
// estimated output exceeds 80% of the planner's max_tokens cap is hard-
// failed (exit 1), not warned. This is the threshold where submit_plan
// will likely truncate mid-edit.
func TestBriefLintAtomSizeFailsAtEightyPercentCap(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"cmd/idiom/lane.go": "package main\nfunc Existing() {}\n",
	})
	// ~1100 lines × 25 chars × 1.2 / 4 = ~8200 tokens → past FAIL
	// threshold (6553 at 8192 cap).
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `cmd/idiom/lane.go` | ADD `func added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
		buildInlinedContent(1100)+"\n")
	exit, out := runPreflight(t, script, brief, ws)
	if exit != 1 {
		t.Fatalf("expected exit=1 (FAIL), got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(out, "submit_plan will likely truncate") {
		t.Fatalf("expected truncation-warning in FAIL message: %s", out)
	}
}

// TestBriefLintAtomSizeRespectsEnvOverride verifies that
// IDIOM_PLANNER_MAX_TOKENS overrides the default cap of 8192. A small
// cap (1024) should make even a tiny brief trip the FAIL threshold.
func TestBriefLintAtomSizeRespectsEnvOverride(t *testing.T) {
	script := briefPreflightTestDeps(t)
	ws := makeWorkspace(t, map[string]string{
		"cmd/idiom/lane.go": "package main\nfunc Existing() {}\n",
	})
	brief := writeLintBrief(t, ws, "# Brief\n\n"+
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n"+
		"| File | Change |\n"+
		"|------|--------|\n"+
		"| `cmd/idiom/lane.go` | ADD `func added()` at line 2 |\n\n"+
		"## Current state\n\nLine 2 is `func Existing() {}`.\n\n"+
		buildInlinedContent(200)+"\n")
	// 200 lines → ~1500 tokens. At cap=1024, threshold FAIL=819, so
	// this should fail.
	cmd := exec.Command("python3", script, brief)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "IDIOM_PLANNER_MAX_TOKENS=1024")
	out, err := cmd.CombinedOutput()
	exit := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exit = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("preflight subprocess failed: %v (output=%s)", err, out)
	}
	if exit != 1 {
		t.Fatalf("expected FAIL under cap=1024, got exit=%d output=%s", exit, out)
	}
	if !strings.Contains(string(out), "max_tokens=1024") {
		t.Fatalf("expected env override to show in failure message: %s", out)
	}
}
