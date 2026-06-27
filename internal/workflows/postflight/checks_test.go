package postflight

import (
	"strings"
	"testing"
)

// TestUnresolvedQuestionMarkerFlagsAddedDiffLine is the load-bearing
// "model left a question marker in the diff" detection. The brief
// runner expects `<<QUESTION>>:` to live in chat output as a typed
// blocker; a marker that survives into committed code means
// either the model misused the marker or a copy-paste error left
// an unresolved blocker in the patch.
func TestUnresolvedQuestionMarkerFlagsAddedDiffLine(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path:       "src/broken.go",
		AddedLines: []AddedLine{{Line: 2, Body: "// <<QUESTION>>: did the model resolve this?"}},
	}}}

	findings, err := UnresolvedQuestionMarker{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d (%+v)", len(findings), findings)
	}

	if findings[0].Severity != SeverityError {
		t.Fatalf("question marker should be SeverityError, got %s", findings[0].Severity)
	}

	if findings[0].Path != "src/broken.go" || findings[0].Line != 2 {
		t.Fatalf("path/line wrong: %+v", findings[0])
	}
}

// TestUnresolvedQuestionMarkerIgnoresPreExistingMarkerInTouchedFile
// captures the false-positive boundary: the check is about what the
// lane introduced, not every old marker example already present in
// a file that received an unrelated change.
func TestUnresolvedQuestionMarkerIgnoresPreExistingMarkerInTouchedFile(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path:       "docs/protocol.md",
		AddedLines: []AddedLine{{Line: 12, Body: "New prose about the protocol."}},
	}}}

	findings, err := UnresolvedQuestionMarker{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 0 {
		t.Fatalf("pre-existing marker outside added lines must not trigger, got %v", findings)
	}
}

// TestUnresolvedQuestionMarkerSkipsDeletedFiles verifies the check
// ignores deleted files. A deletion is by definition not adding a
// new QUESTION marker.
func TestUnresolvedQuestionMarkerSkipsDeletedFiles(t *testing.T) {
	d := Diff{Files: []FileChange{{Path: "gone.go", Deleted: true}}}

	findings, err := UnresolvedQuestionMarker{}.Run(d, RunContext{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 0 {
		t.Fatalf("expected no findings for deleted file, got %v", findings)
	}
}

// TestUnresolvedQuestionMarkerCleanFileNoFindings verifies the
// quiet path: a diff that does not add the marker produces zero
// findings. A check that fires on every diff isn't useful — the
// signal must be specific.
func TestUnresolvedQuestionMarkerCleanFileNoFindings(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path:       "ok.go",
		AddedLines: []AddedLine{{Line: 2, Body: "func OK() {}"}},
	}}}
	findings, err := UnresolvedQuestionMarker{}.Run(d, RunContext{})

	if err != nil || len(findings) != 0 {
		t.Fatalf("clean file should produce no findings; got err=%v findings=%v", err, findings)
	}
}

// TestFileAllowlistMismatchFlagsOutOfScopeWrite is the canonical
// "model wrote outside the brief's required_edit_paths" detection.
// This is the single highest-signal postflight check — when the
// brief allowlists three files and the diff touches a fourth, the
// patch is almost certainly wrong.
func TestFileAllowlistMismatchFlagsOutOfScopeWrite(t *testing.T) {
	d := Diff{Files: []FileChange{
		{Path: "a/in_scope.go"},
		{Path: "a/out_of_scope.go"},
	}}
	ctx := RunContext{RequiredEditPaths: []string{"a/in_scope.go"}}

	findings, err := FileAllowlistMismatch{}.Run(d, ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d (%+v)", len(findings), findings)
	}

	if findings[0].Path != "a/out_of_scope.go" || findings[0].Severity != SeverityError {
		t.Fatalf("finding wrong: %+v", findings[0])
	}
}

// TestFileAllowlistMismatchNoOpsWithoutAllowlist verifies the
// graceful path: when the caller doesn't supply required_edit_paths
// (e.g. a brief without a Files-To-Modify section), the check
// returns nothing rather than flagging every file. Better to be
// silent than wrong.
func TestFileAllowlistMismatchNoOpsWithoutAllowlist(t *testing.T) {
	d := Diff{Files: []FileChange{{Path: "a.go"}, {Path: "b.go"}}}

	findings, err := FileAllowlistMismatch{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 0 {
		t.Fatalf("absent allowlist must produce no findings, got %v", findings)
	}
}

// TestFileAllowlistMismatchNormalizesPathSeparators verifies the
// path-comparison contract: callers pass paths with forward
// slashes (`a/b.go`); the diff also uses forward slashes after
// stripGitPrefix; but the normalization helper should handle both
// `./` prefixes and trailing slashes so allowlist entries are
// forgiving of common typos.
func TestFileAllowlistMismatchNormalizesPathSeparators(t *testing.T) {
	d := Diff{Files: []FileChange{{Path: "a/b.go"}}}
	// The allowlist entry has a leading ./ (a common copy-paste from
	// terminal output) and a trailing slash on a different entry.
	ctx := RunContext{RequiredEditPaths: []string{"./a/b.go", "other/dir/"}}
	findings, err := FileAllowlistMismatch{}.Run(d, ctx)

	if err != nil || len(findings) != 0 {
		t.Fatalf("normalized allowlist must accept ./ prefix; got err=%v findings=%v", err, findings)
	}
}

// TestNarratingTestCommentsFlagsAddedNarration is the canonical
// detection: a `_test.go` file that adds a comment matching one of
// the narratingPhrase patterns must produce a warning. The inputs
// here use case-and-spacing variants (capital "T", trailing
// whitespace, leading spaces) of the real-world phrases the
// regex was designed to catch — earlier this test fed the regex
// its exact pattern verbatim, which made the assertion tautological.
func TestNarratingTestCommentsFlagsAddedNarration(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path: "foo/bar_test.go",
		AddedLines: []AddedLine{
			{Line: 1, Body: "//   This Test verifies   that Foo returns 7"},
			{Line: 2, Body: "func TestFooReturnsSeven(t *testing.T) {"},
			{Line: 3, Body: "\tif Foo() != 7 { t.Fatal() }"},
		},
	}}}

	findings, err := NarratingTestComments{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for the narrating comment, got %d (%+v)", len(findings), findings)
	}

	if findings[0].Severity != SeverityWarning {
		t.Fatalf("narration is a warning, not error: %s", findings[0].Severity)
	}

	if findings[0].Line != 1 {
		t.Fatalf("finding should carry the post-image line number: %+v", findings[0])
	}
}

// TestNarratingTestCommentsBorderlinePhrasesDoNotFire verifies the
// false-positive ceiling: phrases that resemble narration but
// aren't exactly the patterns the regex targets must NOT trigger.
// Catches the regex getting too greedy in a future edit (e.g.
// dropping the leading anchor or broadening the alternation).
func TestNarratingTestCommentsBorderlinePhrasesDoNotFire(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path: "foo/bar_test.go",
		AddedLines: []AddedLine{
			// Gerund, not "verifies that"
			{Line: 1, Body: "// verifying invariants below"},
			// Buried in the middle of a sentence
			{Line: 2, Body: "// when we test the parser, make sure to seed"},
			// Question, not assertion
			{Line: 3, Body: "// does this test cover the empty case?"},
			// Cross-reference; legitimate context
			{Line: 4, Body: "// see issue #123 for a related test"},
			// TODO that mentions tests
			{Line: 5, Body: "// TODO: add a test that handles negative input"},
		},
	}}}

	findings, err := NarratingTestComments{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 0 {
		t.Fatalf("borderline phrases must not trigger; got %v", findings)
	}
}

// TestNarratingTestCommentsCarriesPostImageLineNumber verifies the
// load-bearing fix from the Phase 4/5 review: the Line field
// reflects the actual post-image file line (parsed from the hunk
// header), not the index within AddedLines. A diff with non-
// contiguous adds and non-zero starting line previously produced
// off-by-many line numbers.
func TestNarratingTestCommentsCarriesPostImageLineNumber(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path: "foo/bar_test.go",
		AddedLines: []AddedLine{
			{Line: 42, Body: "// This test verifies parsing"},
			{Line: 99, Body: "// makes sure the result is non-nil"},
		},
	}}}

	findings, err := NarratingTestComments{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	if findings[0].Line != 42 {
		t.Fatalf("first finding line wrong: %+v", findings[0])
	}

	if findings[1].Line != 99 {
		t.Fatalf("second finding line wrong: %+v", findings[1])
	}
}

// TestNarratingTestCommentsIgnoresNonTestFiles verifies the
// scoping: the heuristic only fires on test files. A narrating
// comment in production code might still be redundant, but Phase
// 5 is scoped to test files only — production-code narration is
// noisier to flag and reusing the heuristic there would risk
// false positives on legitimate documentation.
func TestNarratingTestCommentsIgnoresNonTestFiles(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path:       "foo/bar.go",
		AddedLines: []AddedLine{{Line: 1, Body: "// This function ensures that the Foo invariant holds"}},
	}}}

	findings, err := NarratingTestComments{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 0 {
		t.Fatalf("non-test file must not trigger narration check, got %v", findings)
	}
}

// TestNarratingTestCommentsAcceptsLegitimateComments verifies the
// false-positive ceiling: comments that aren't narration (a
// cross-reference, a TODO note, a copyright header) must NOT
// trigger. If this case starts failing, the regex got too greedy
// and the check is producing noise.
func TestNarratingTestCommentsAcceptsLegitimateComments(t *testing.T) {
	d := Diff{Files: []FileChange{{
		Path: "foo/bar_test.go",
		AddedLines: []AddedLine{
			{Line: 1, Body: "// see issue #123 for context"},
			{Line: 2, Body: "// TODO(2026-06-01): tighten the bound here"},
			{Line: 3, Body: "// Copyright 2026 Acme"},
		},
	}}}

	findings, err := NarratingTestComments{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 0 {
		t.Fatalf("non-narrating comments must not trigger, got %v", findings)
	}
}

// TestNarratingTestCommentsRecognizesPythonAndCppTests verifies
// the heuristic isn't Go-only: `test_foo.py` and `foo_test.cpp`
// also fire, since postflight runs against multi-language repos.
func TestNarratingTestCommentsRecognizesPythonAndCppTests(t *testing.T) {
	d := Diff{Files: []FileChange{
		{Path: "tests/test_foo.py", AddedLines: []AddedLine{{Line: 1, Body: "# This test verifies parsing"}}},
		{Path: "tests/foo_test.cpp", AddedLines: []AddedLine{{Line: 1, Body: "// Make sure the parser handles UTF-8"}}},
	}}

	findings, err := NarratingTestComments{}.Run(d, RunContext{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (py + cpp), got %d (%+v)", len(findings), findings)
	}

	want := map[string]bool{"tests/test_foo.py": false, "tests/foo_test.cpp": false}
	for _, f := range findings {
		if _, ok := want[f.Path]; !ok {
			t.Fatalf("unexpected path: %s", f.Path)
		}

		want[f.Path] = true
	}

	for path, seen := range want {
		if !seen {
			t.Fatalf("missing finding for %s", path)
		}
	}
}

// TestCommentBodyParsesMultipleSyntaxes is the unit-level guard
// for the comment extractor: each language family idiom touches
// must be recognized so the narration heuristic doesn't miss
// because the language used `#` instead of `//`.
func TestCommentBodyParsesMultipleSyntaxes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"// hello", "hello"},
		{"   //   hello   ", "hello"},
		{"# hello", "hello"},
		{"/* block */", "block"},
		{"/* block", "block"},
		{" * continuation", "continuation"},
		{"not a comment", ""},
	}
	for _, c := range cases {
		got := commentBody(c.in)
		if got != c.want {
			t.Fatalf("commentBody(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestGenericChecksReturnsCanonicalSet verifies the always-on set.
// Keeping it explicit prevents accidental check-set drift — any new
// generic check must be added here alongside the docs.
func TestGenericChecksReturnsCanonicalSet(t *testing.T) {
	got := GenericChecks()
	want := []string{"unresolved_question_marker", "file_allowlist_mismatch", "narrating_test_comments", "go_build_passes"}

	if len(got) != len(want) {
		t.Fatalf("expected %d generic checks, got %d", len(want), len(got))
	}

	for i, c := range got {
		if c.Name() != want[i] {
			t.Fatalf("generic check[%d] = %s, want %s", i, c.Name(), want[i])
		}
	}

	// trailing assertion for noise: ensure we don't accidentally
	// share state across instances by verifying repeated calls
	// produce equivalent slices.
	again := GenericChecks()
	if len(again) != len(got) {
		t.Fatalf("GenericChecks should be deterministic across calls")
	}

	if strings.TrimSpace(got[0].Name()) == "" {
		t.Fatalf("check name must not be empty")
	}
}
