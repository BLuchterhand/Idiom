package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper to write a brief + diff + working-tree file the question-
// marker check can read.
func setupPostflightProject(t *testing.T, briefBody, diffBody string, files map[string]string) (root, briefPath, diffPath string) {
	t.Helper()
	root = t.TempDir()
	briefPath = filepath.Join(root, "brief.md")
	if err := os.WriteFile(briefPath, []byte(briefBody), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}
	diffPath = filepath.Join(root, "lane.diff")
	if err := os.WriteFile(diffPath, []byte(diffBody), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}
	for relpath, body := range files {
		full := filepath.Join(root, relpath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relpath, err)
		}
	}
	return root, briefPath, diffPath
}

const cleanDiff = `diff --git a/src/foo.go b/src/foo.go
--- a/src/foo.go
+++ b/src/foo.go
@@ -1,2 +1,3 @@
 package src
+// real fix
 func Foo() {}
`

const cleanBrief = `# Atom
Files To Modify
- src/foo.go
`

// TestLanePostflightCleanDiffPasses is the canonical happy path:
// a diff that touches only allowlisted paths, contains no
// QUESTION markers in the working tree, and adds no narrating
// test comments returns exit 0 with a PASS summary.
func TestLanePostflightCleanDiffPasses(t *testing.T) {
	root, briefPath, diffPath := setupPostflightProject(t, cleanBrief, cleanDiff, map[string]string{
		"src/foo.go": "package src\n// real fix\nfunc Foo() {}\n",
	})

	var stdout, stderr bytes.Buffer
	code := runLaneMainTest(t, []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
	if code != 0 {
		t.Fatalf("clean diff must exit 0, got %d (stderr=%s, stdout=%s)", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "PASS") {
		t.Fatalf("output should announce PASS, got: %s", stdout.String())
	}
}

// TestLanePostflightUnresolvedQuestionMarkerFailsExitThree
// verifies the canonical failure-mode contract: the operator-
// distinguishable exit code 3 (not 1) signals "checks ran and
// found problems," vs 1 ("checks couldn't even run"). Colony's
// lane loop and CI scripts depend on this distinction.
func TestLanePostflightUnresolvedQuestionMarkerFailsExitThree(t *testing.T) {
	root, briefPath, diffPath := setupPostflightProject(t, cleanBrief, cleanDiff, map[string]string{
		"src/foo.go": "package src\n// <<QUESTION>>: did this resolve?\nfunc Foo() {}\n",
	})

	var stdout, stderr bytes.Buffer
	code := runLaneMainTest(t, []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
	if code != 3 {
		t.Fatalf("question marker must exit 3, got %d (stdout=%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "unresolved_question_marker") {
		t.Fatalf("output should name the check that failed: %s", stdout.String())
	}
}

// TestLanePostflightAllowlistMismatchFails verifies the brief-
// extraction path: required_edit_paths come from the brief
// (the same logic the runner uses), and a write outside that
// scope fails the postflight.
func TestLanePostflightAllowlistMismatchFails(t *testing.T) {
	briefBody := `# Atom
Files To Modify
- src/foo.go
`
	diffBody := `diff --git a/src/wrong.go b/src/wrong.go
--- a/src/wrong.go
+++ b/src/wrong.go
@@ -1 +1,2 @@
 package src
+// out of scope
`
	root, briefPath, diffPath := setupPostflightProject(t, briefBody, diffBody, map[string]string{
		"src/wrong.go": "package src\n// out of scope\n",
	})
	var stdout, stderr bytes.Buffer
	code := runLaneMainTest(t, []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
	if code != 3 {
		t.Fatalf("allowlist mismatch must exit 3, got %d (stdout=%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "file_allowlist_mismatch") {
		t.Fatalf("output should name the check: %s", stdout.String())
	}
}

// TestLanePostflightExplicitRequiredEditPathsBeatsBrief verifies
// the precedence: a CLI-supplied --required-edit-paths overrides
// the brief extraction. Operators triaging a false positive can
// pass an explicit list to confirm the diff is in scope without
// editing the brief.
func TestLanePostflightExplicitRequiredEditPathsBeatsBrief(t *testing.T) {
	briefBody := `# Atom
Files To Modify
- src/should-not-match.go
`
	diffBody := `diff --git a/src/foo.go b/src/foo.go
--- a/src/foo.go
+++ b/src/foo.go
@@ -1 +1,2 @@
 package src
+// fix
`
	root, briefPath, diffPath := setupPostflightProject(t, briefBody, diffBody, map[string]string{
		"src/foo.go": "package src\n// fix\n",
	})
	var stdout, stderr bytes.Buffer
	args := []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--required-edit-paths", "src/foo.go", "--project-root", root}
	code := runLaneMainTest(t, args, &stdout, &stderr, &laneFakeRunner{})
	if code != 0 {
		t.Fatalf("explicit required_edit_paths must beat brief; got code=%d stdout=%s", code, stdout.String())
	}
}

// TestLanePostflightSkipFlagDisablesNamedCheck verifies the
// operator escape hatch: --skip removes a noisy check from the
// pipeline without disabling the rest. Useful when one check has
// a known false positive on a particular diff.
func TestLanePostflightSkipFlagDisablesNamedCheck(t *testing.T) {
	briefBody := cleanBrief
	diffBody := cleanDiff
	root, briefPath, diffPath := setupPostflightProject(t, briefBody, diffBody, map[string]string{
		// File contains a question marker, but we'll skip that check.
		"src/foo.go": "package src\n// <<QUESTION>>: skip me\nfunc Foo() {}\n",
	})
	var stdout, stderr bytes.Buffer
	args := []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root, "--skip", "unresolved_question_marker"}
	code := runLaneMainTest(t, args, &stdout, &stderr, &laneFakeRunner{})
	if code != 0 {
		t.Fatalf("--skip must drop the named check; got code=%d stdout=%s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "unresolved_question_marker") {
		t.Fatalf("skipped check leaked into output: %s", stdout.String())
	}
}

// TestLanePostflightJSONEmitsFindingsAndSummary verifies the
// machine-readable output format: one JSONL line per finding plus
// a final summary line. The orchestrator's lane loop parses these
// to decide whether to retry or surface the diff to a human.
func TestLanePostflightJSONEmitsFindingsAndSummary(t *testing.T) {
	root, briefPath, diffPath := setupPostflightProject(t, cleanBrief, cleanDiff, map[string]string{
		"src/foo.go": "package src\n// <<QUESTION>>: trigger\nfunc Foo() {}\n",
	})
	var stdout, stderr bytes.Buffer
	args := []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root, "--json"}
	code := runLaneMainTest(t, args, &stdout, &stderr, &laneFakeRunner{})
	if code != 3 {
		t.Fatalf("expected exit 3, got %d (stdout=%s)", code, stdout.String())
	}
	var summary map[string]any
	var sawFinding bool
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("non-JSON line in --json mode: %q (err=%v)", line, err)
		}
		switch event["type"] {
		case "idiom.lane.postflight.finding":
			sawFinding = true
		case "idiom.lane.postflight.summary":
			summary = event
		}
	}
	if !sawFinding {
		t.Fatalf("expected at least one finding event in JSON output: %s", stdout.String())
	}
	if summary == nil {
		t.Fatalf("expected a summary event: %s", stdout.String())
	}
	if pass, _ := summary["pass"].(bool); pass {
		t.Fatalf("summary.pass should be false when error findings exist: %v", summary)
	}
}

// TestLanePostflightMissingDiffFlagExitsTwo verifies the missing-
// flag contract: --diff is mandatory; absent it, the wrapper
// returns the conventional flag-error exit (2) rather than
// crashing or silently no-op'ing.
func TestLanePostflightMissingDiffFlagExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runLaneMainTest(t, []string{"postflight"}, &stdout, &stderr, &laneFakeRunner{})
	if code != 2 {
		t.Fatalf("missing --diff must exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--diff is required") {
		t.Fatalf("error must explain the missing flag: %s", stderr.String())
	}
}

// TestLanePostflightMissingDiffFileExitsOne verifies the IO-
// failure path is exit 1, distinct from the "checks found
// problems" exit 3. A diff path that doesn't exist is a setup
// problem, not a check failure, and the calling shell should be
// able to tell them apart.
func TestLanePostflightMissingDiffFileExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"postflight", "--diff", "/does/not/exist.diff"}
	code := runLaneMainTest(t, args, &stdout, &stderr, &laneFakeRunner{})
	if code != 1 {
		t.Fatalf("missing diff file must exit 1 (IO), got %d", code)
	}
	if !strings.Contains(stderr.String(), "read diff") {
		t.Fatalf("error must reference the diff read: %s", stderr.String())
	}
}

// TestLanePostflightProjectCheckLoadsAndFails is the integration
// test for the .idiom/postflight/checks/ loader: a project that
// declares a `kind: "shell"` rule with a non-zero command sees
// that check fail the postflight alongside the generic ones.
func TestLanePostflightProjectCheckLoadsAndFails(t *testing.T) {
	briefBody := cleanBrief
	diffBody := cleanDiff
	root, briefPath, diffPath := setupPostflightProject(t, briefBody, diffBody, map[string]string{
		"src/foo.go": "package src\n// real fix\nfunc Foo() {}\n",
	})
	dir := filepath.Join(root, ".idiom", "postflight", "checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rule := `{"name": "always-fail", "kind": "shell", "command": "false"}`
	if err := os.WriteFile(filepath.Join(dir, "always-fail.json"), []byte(rule), 0o644); err != nil {
		t.Fatalf("write rule: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runLaneMainTest(t, []string{"postflight", "--diff", diffPath, "--brief", briefPath, "--project-root", root}, &stdout, &stderr, &laneFakeRunner{})
	if code != 3 {
		t.Fatalf("project check failure must exit 3, got %d (stdout=%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "always-fail") {
		t.Fatalf("output should mention the project check that failed: %s", stdout.String())
	}
}
