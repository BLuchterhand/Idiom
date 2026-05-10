package postflight

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLoadProjectChecksEmptyDirReturnsNil verifies the silent
// fallback: a project that hasn't authored any postflight rules
// yet should produce no checks and no error. The pipeline runs
// the generic-only set in that case.
func TestLoadProjectChecksEmptyDirReturnsNil(t *testing.T) {
	root := t.TempDir()
	checks, err := LoadProjectChecks(root)
	if err != nil {
		t.Fatalf("missing checks dir must not error: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("empty project must produce no project checks, got %d", len(checks))
	}
}

// TestLoadProjectChecksParsesShellRule verifies the on-disk schema
// round-trips through the loader: a `kind: "shell"` rule with a
// command and args becomes a runnable Check whose Name matches the
// declared name.
func TestLoadProjectChecksParsesShellRule(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "postflight", "checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"name": "lint", "kind": "shell", "command": "true"}`
	if err := os.WriteFile(filepath.Join(dir, "lint.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	checks, err := LoadProjectChecks(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 project check, got %d", len(checks))
	}
	if checks[0].Name() != "lint" {
		t.Fatalf("name decoded wrong: %q", checks[0].Name())
	}
}

// TestLoadProjectChecksUsesFilenameWhenNameOmitted verifies the
// convenience: a check that omits `name` falls back to the JSON
// filename (without extension). Project authors don't need to
// keep filename and JSON `name` field in sync if they don't want
// to.
func TestLoadProjectChecksUsesFilenameWhenNameOmitted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "postflight", "checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"kind": "shell", "command": "true"}`
	if err := os.WriteFile(filepath.Join(dir, "filename-fallback.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	checks, err := LoadProjectChecks(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(checks) != 1 || checks[0].Name() != "filename-fallback" {
		t.Fatalf("filename fallback failed; got %+v", checks)
	}
}

// TestLoadProjectChecksMalformedJSONErrors verifies a present-but-
// broken project check surfaces a parse error rather than silently
// dropping the file. A misconfigured rule that no-ops would let
// real bugs slip through.
func TestLoadProjectChecksMalformedJSONErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "postflight", "checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"name":`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadProjectChecks(root); err == nil {
		t.Fatalf("malformed JSON must error, got nil")
	}
}

// TestProjectShellCheckPassesWithZeroExit is the canonical happy
// path: a `kind: "shell"` rule whose command exits 0 produces no
// findings (the pipeline records an absence of failure rather
// than a positive "ok" finding).
func TestProjectShellCheckPassesWithZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX `true`")
	}
	pc := &projectCheckRunner{spec: ProjectCheck{Name: "always-ok", Kind: "shell", Command: "true"}}
	findings, err := pc.Run(Diff{}, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("zero-exit shell check produces no findings, got %v", findings)
	}
}

// TestProjectShellCheckFailsWithNonZeroExit is the canonical
// failure path: a non-zero exit becomes an error finding by
// default. This is how project-defined lint rules signal "this
// diff has a problem."
func TestProjectShellCheckFailsWithNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX `false`")
	}
	pc := &projectCheckRunner{spec: ProjectCheck{Name: "always-fail", Kind: "shell", Command: "false"}}
	findings, err := pc.Run(Diff{}, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != SeverityError {
		t.Fatalf("non-zero exit must produce an error finding, got %v", findings)
	}
	if !strings.Contains(findings[0].Message, "exit") {
		t.Fatalf("message should reference the exit code: %s", findings[0].Message)
	}
}

// TestProjectShellCheckRespectsFailOnNonZeroFalse verifies the
// non-blocking variant: a project that wants advisory rules
// (e.g. an experimental check) sets `fail_on_non_zero: false` and
// the non-zero exit becomes a warning instead of an error.
func TestProjectShellCheckRespectsFailOnNonZeroFalse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX `false`")
	}
	falseVal := false
	pc := &projectCheckRunner{spec: ProjectCheck{Name: "advisory", Kind: "shell", Command: "false", FailOnNonZero: &falseVal}}
	findings, err := pc.Run(Diff{}, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != SeverityWarning {
		t.Fatalf("fail_on_non_zero=false must yield a warning, got %v", findings)
	}
}

// TestProjectShellCheckEmptyCommandIsConfigError verifies that a
// rule with an empty `command` field surfaces a clear
// configuration-error finding rather than spawning an empty
// process or silently no-op'ing.
func TestProjectShellCheckEmptyCommandIsConfigError(t *testing.T) {
	pc := &projectCheckRunner{spec: ProjectCheck{Name: "broken", Kind: "shell"}}
	findings, err := pc.Run(Diff{}, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != SeverityError {
		t.Fatalf("empty command must produce a config error, got %v", findings)
	}
	if !strings.Contains(findings[0].Message, "empty") {
		t.Fatalf("message should call out the empty command: %s", findings[0].Message)
	}
}

// TestProjectAstGrepKindReservedReturnsWarning verifies the
// reserved-kind contract: a project that declares an `ast_grep`
// rule today gets a clear warning that the rule is being skipped,
// rather than the rule silently no-op'ing or the loader rejecting
// the whole file.
func TestProjectAstGrepKindReservedReturnsWarning(t *testing.T) {
	pc := &projectCheckRunner{spec: ProjectCheck{Name: "future", Kind: "ast_grep"}}
	findings, err := pc.Run(Diff{}, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != SeverityWarning {
		t.Fatalf("ast_grep must be a reserved-warning, got %v", findings)
	}
}

// TestProjectUnknownKindIsConfigError verifies that a rule with
// an unrecognized `kind` field surfaces an error finding so the
// project author sees the typo immediately.
func TestProjectUnknownKindIsConfigError(t *testing.T) {
	pc := &projectCheckRunner{spec: ProjectCheck{Name: "typo", Kind: "shel"}}
	findings, err := pc.Run(Diff{}, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != SeverityError {
		t.Fatalf("unknown kind must produce error finding, got %v", findings)
	}
}

// TestSubstituteShellArgsExpandsKnownPlaceholders verifies the
// placeholder substitution contract: `{project_root}` and
// `{paths}` resolve to runtime values; unknown placeholders pass
// through unchanged so the schema can grow new placeholders
// without breaking older configs.
func TestSubstituteShellArgsExpandsKnownPlaceholders(t *testing.T) {
	d := Diff{Files: []FileChange{{Path: "a.go"}, {Path: "b.go"}}}
	ctx := RunContext{ProjectRoot: "/proj"}
	args := []string{"--root", "{project_root}", "--paths", "{paths}", "--unknown", "{not_a_placeholder}"}
	out := substituteShellArgs(args, ctx, d)
	want := []string{"--root", "/proj", "--paths", "a.go b.go", "--unknown", "{not_a_placeholder}"}
	if len(out) != len(want) {
		t.Fatalf("length mismatch: got %v, want %v", out, want)
	}
	for i := range out {
		if out[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q", i, out[i], want[i])
		}
	}
}

// TestRunAggregatesFindingsAndComputesPass is the integration test
// for the pipeline orchestrator: a mix of passing and failing
// checks produces a Result whose Findings include all of them and
// whose Pass reflects whether any were severity=error.
func TestRunAggregatesFindingsAndComputesPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package x\n// <<QUESTION>>: marker\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := Diff{Files: []FileChange{{Path: "a.go"}, {Path: "b.go"}}}
	ctx := RunContext{ProjectRoot: root, RequiredEditPaths: []string{"a.go"}}
	res := Run(d, ctx, GenericChecks())
	if res.Pass {
		t.Fatalf("expected fail (question marker + allowlist mismatch); got Pass=true findings=%v", res.Findings)
	}
	checks := map[string]bool{}
	for _, f := range res.Findings {
		checks[f.Check] = true
	}
	if !checks["unresolved_question_marker"] || !checks["file_allowlist_mismatch"] {
		t.Fatalf("expected both checks to fire, got %v", checks)
	}
}

// TestFilterChecksRespectsSkipList verifies the operator escape
// hatch: `--skip <name,...>` removes the named checks from the
// pipeline so a known-noisy rule can be silenced without
// disabling all postflight.
func TestFilterChecksRespectsSkipList(t *testing.T) {
	in := GenericChecks()
	out := FilterChecks(in, []string{"narrating_test_comments"})
	if len(out) != len(in)-1 {
		t.Fatalf("expected one fewer check, got %d (in=%d)", len(out), len(in))
	}
	for _, c := range out {
		if c.Name() == "narrating_test_comments" {
			t.Fatalf("skipped check leaked through: %v", out)
		}
	}
}

// TestRunRecordsCheckErrorsAsFindings verifies the partial-
// failure path: a check that returns an error doesn't crash the
// pipeline; instead its error becomes a finding so other checks
// still run and produce useful signal.
func TestRunRecordsCheckErrorsAsFindings(t *testing.T) {
	check := errorCheck{}
	res := Run(Diff{}, RunContext{}, []Check{check})
	if res.Pass {
		t.Fatalf("error-returning check must fail the pipeline")
	}
	if len(res.Findings) != 1 || res.Findings[0].Severity != SeverityError {
		t.Fatalf("expected one error finding, got %v", res.Findings)
	}
}

type errorCheck struct{}

func (errorCheck) Name() string { return "errors_always" }
func (errorCheck) Run(Diff, RunContext) ([]Finding, error) {
	return nil, &runtimeError{msg: "synthetic"}
}

type runtimeError struct{ msg string }

func (r *runtimeError) Error() string { return r.msg }
