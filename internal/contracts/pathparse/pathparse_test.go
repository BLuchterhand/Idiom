package pathparse

import (
	"slices"
	"strings"
	"testing"
)

// TestMentionedWorkspacePathsRejectsBacktickProseTokens guards the
// regression from the Phase 6.5 / Phase 5b dispatch debugging: the
// embedded plan-execute preamble has prose like “start_line`/`end_line“
// that the extractor used to pick up as a fake required_edit_path
// because outer-trim left the inner backticks intact. Reject any
// candidate containing a backtick so prose-tokens never reach the
// downstream consumers (required_edit_paths, discovery handles).
func TestMentionedWorkspacePathsRejectsBacktickProseTokens(t *testing.T) {
	// The exact prose shape the planner preamble produces. The outer
	// double-backticks are markdown emphasis; the inner backticks are
	// the prose tokens. Without the gate, the extractor turns this
	// into the string `start_line`/`end_line` and treats it as a
	// path candidate (it contains "/", so LooksLike returns true).
	text := "Each edit: `path` + (`old_text`+`new_text` | `start_line`+`end_line`+`content`)"
	got := MentionedWorkspacePaths(text)

	for _, p := range got {
		if strings.Contains(p, "`") {
			t.Fatalf("backtick-prose token leaked into extracted paths: %q (full set: %v)", p, got)
		}
	}
}

func TestPlanFileScopeHeadingsReturnsStableVocabularyCopy(t *testing.T) {
	want := []string{
		"expected file scope",
		"implementation file scope",
		"file scope",
		"files to modify",
		"files to edit",
		"write scope",
		"edit scope",
	}

	got := PlanFileScopeHeadings()
	if !slices.Equal(got, want) {
		t.Fatalf("PlanFileScopeHeadings() = %#v, want %#v", got, want)
	}

	got[0] = "mutated"
	if got := PlanFileScopeHeadings(); !slices.Equal(got, want) {
		t.Fatalf("PlanFileScopeHeadings returned mutable shared backing storage: %#v", got)
	}
}

func TestHasWorkspaceFileExtensionIncludesAstro(t *testing.T) {
	if !HasWorkspaceFileExtension("src/pages/contact.astro") {
		t.Fatal(".astro should be recognized as a workspace file extension")
	}
}

func TestLooksLikeWindowsAbsolutePath(t *testing.T) {
	for _, path := range []string{"C:/repo/file.go", `d:\repo\file.go`} {
		if !LooksLikeWindowsAbsolutePath(path) {
			t.Fatalf("LooksLikeWindowsAbsolutePath(%q) = false, want true", path)
		}
	}

	for _, path := range []string{"1:/repo/file.go", "internal/C:/file.go", "C:relative/file.go", "/repo/file.go", "repo/file.go", ""} {
		if LooksLikeWindowsAbsolutePath(path) {
			t.Fatalf("LooksLikeWindowsAbsolutePath(%q) = true, want false", path)
		}
	}
}

func TestIsAbsolutePath(t *testing.T) {
	for _, path := range []string{"/repo/file.go", "C:/repo/file.go", `d:\repo\file.go`} {
		if !IsAbsolutePath(path) {
			t.Fatalf("IsAbsolutePath(%q) = false, want true", path)
		}
	}

	for _, path := range []string{"repo/file.go", "./repo/file.go", "C:relative/file.go", ""} {
		if IsAbsolutePath(path) {
			t.Fatalf("IsAbsolutePath(%q) = true, want false", path)
		}
	}
}

func TestNormalizeWorkspaceRelativePath(t *testing.T) {
	cases := map[string]string{
		`pkg\file.go`:       "pkg/file.go",
		"./pkg/../pkg.go":   "pkg.go",
		"pkg//nested//x.go": "pkg/nested/x.go",
	}
	for input, want := range cases {
		if got := NormalizeWorkspaceRelativePath(input); got != want {
			t.Fatalf("NormalizeWorkspaceRelativePath(%q) = %q, want %q", input, got, want)
		}
	}

	for _, path := range []string{"", ".", "..", "../outside.go", "/tmp/outside.go", `C:\tmp\outside.go`, "./C:/tmp/outside.go", "pkg/../..", "pkg/\x00file.go"} {
		if got := NormalizeWorkspaceRelativePath(path); got != "" {
			t.Fatalf("NormalizeWorkspaceRelativePath(%q) = %q, want rejection", path, got)
		}
	}
}

// TestScopeToBriefBodyExtractsMarkedRegion verifies the brief-body
// scoping primitive Phase 6.5 added: when the lane wrapper has
// wrapped the project brief in IDIOM:BRIEF-BODY-* markers, every
// extractor that calls ScopeToBriefBody narrows its scan to the
// marked region — that's how prose tokens in the embedded preamble
// (`start_line`/`end_line` etc.) stop leaking into required_edit_paths.
func TestScopeToBriefBodyExtractsMarkedRegion(t *testing.T) {
	composed := "preamble has `start_line`/`end_line` prose\n" +
		"<!-- IDIOM:BRIEF-BODY-START -->\n" +
		"internal/runtime/agent/runner.go\n" +
		"<!-- IDIOM:BRIEF-BODY-END -->\n" +
		"postamble has cmd/idiom/extra.go\n"
	scoped := ScopeToBriefBody(composed)

	if !strings.Contains(scoped, "internal/runtime/agent/runner.go") {
		t.Fatalf("scoped region should contain the brief body: %q", scoped)
	}

	if strings.Contains(scoped, "preamble") || strings.Contains(scoped, "postamble") {
		t.Fatalf("scoped region must drop preamble/postamble: %q", scoped)
	}
}

// TestScopeToBriefBodyFallsBackWhenMarkersAbsent verifies the
// back-compat path: a prompt without markers (chat session, direct
// API call) returns unchanged so the existing extractors still
// scan the whole text.
func TestScopeToBriefBodyFallsBackWhenMarkersAbsent(t *testing.T) {
	plain := "no markers here, internal/runtime/agent/runner.go"
	if got := ScopeToBriefBody(plain); got != plain {
		t.Fatalf("unmarked input must pass through unchanged; got %q", got)
	}
}

// TestMentionedWorkspacePathsScopesToBriefBody is the integration
// check: when markers are present, prose tokens in the surrounding
// preamble must NOT appear in the extracted path set, even if they
// pass the LooksLike gate (e.g. tokens that contain "/").
func TestMentionedWorkspacePathsScopesToBriefBody(t *testing.T) {
	composed := "preamble cites cmd/idiom/preamble_only.go\n" +
		"<!-- IDIOM:BRIEF-BODY-START -->\n" +
		"Files To Modify: internal/runtime/agent/runner.go\n" +
		"<!-- IDIOM:BRIEF-BODY-END -->\n"
	got := MentionedWorkspacePaths(composed)
	wantSeen := false

	for _, p := range got {
		if p == "cmd/idiom/preamble_only.go" {
			t.Fatalf("preamble path leaked into extracted set: %v", got)
		}

		if p == "internal/runtime/agent/runner.go" {
			wantSeen = true
		}
	}

	if !wantSeen {
		t.Fatalf("brief-body path missing from extracted set: %v", got)
	}
}

// TestLooksLikeWorkspacePathTokenRejectsBackticks is the unit-level
// counterpart that pins the gate's contract directly.
func TestLooksLikeWorkspacePathTokenRejectsBackticks(t *testing.T) {
	bogus := []string{
		"start_line`/`end_line",
		"`path`/foo.go",
		"foo.go`",
		"a`b/c.go",
	}
	for _, c := range bogus {
		if LooksLikeWorkspacePathToken(c) {
			t.Errorf("LooksLikeWorkspacePathToken(%q) should be false (contains backtick)", c)
		}
	}

	// Sanity check: legitimate paths still pass.
	if !LooksLikeWorkspacePathToken("internal/runtime/agent/runner.go") {
		t.Fatal("backtick gate must not break legitimate paths")
	}
}

func TestLooksLikeWorkspacePathTokenRejectsSlashSeparatedSymbolProse(t *testing.T) {
	for _, candidate := range []string{"RIVAL/NEMESIS", "LONER/GREGARIOUS", "API/V1", "exclusive/missing"} {
		if LooksLikeWorkspacePathToken(candidate) {
			t.Fatalf("LooksLikeWorkspacePathToken(%q) should be false for slash-separated prose", candidate)
		}
	}

	if !LooksLikeWorkspacePathToken("internal/API.md") {
		t.Fatal("uppercase path segments with a real file extension should still pass")
	}

	if !LooksLikeWorkspacePathToken("internal/capabilities/workspace") {
		t.Fatal("lowercase extensionless workspace paths should still pass")
	}
}

func TestMentionedWorkspacePathsUnderHeadingsRejectsSlashProseInListRows(t *testing.T) {
	text := strings.Join([]string{
		"Files To Modify:",
		"- native/tests/test_funeral.cpp — Add STRANGER, BEST_FRIEND, and RIVAL/NEMESIS coverage",
	}, "\n")
	got := MentionedWorkspacePathsUnderHeadings(text, "files to modify")
	want := []string{"native/tests/test_funeral.cpp"}

	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected only %#v, got %#v", want, got)
	}
}

func TestMentionedWorkspacePathsNormalizesEscapedNewlinePrefix(t *testing.T) {
	text := `"\ninternal/runtime/agent/runner_child_tasks.go" internal/runtime/agent/runner.go`
	got := MentionedWorkspacePaths(text)
	want := []string{
		"internal/runtime/agent/runner_child_tasks.go",
		"internal/runtime/agent/runner.go",
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d normalized paths, got %#v", len(want), got)
	}

	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected normalized paths %#v, got %#v", want, got)
		}
	}
}

func TestMentionedWorkspacePathsHandlesBacktickedPathBeforePeriod(t *testing.T) {
	text := "`cmd/idiom/product_workflow_test.go`."
	got := MentionedWorkspacePaths(text)
	want := []string{"cmd/idiom/product_workflow_test.go"}

	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestMentionedWorkspacePathsInInlineListStopsAfterPathSentence(t *testing.T) {
	text := "`cmd/idiom/product_workflow.go`, `cmd/idiom/product_workflow_test.go`. Add tests for mutually exclusive/missing mode flags and run `go test ./cmd/idiom`."
	got := MentionedWorkspacePathsInInlineList(text)
	want := []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"}

	if len(got) != len(want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}

	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected %#v, got %#v", want, got)
		}
	}
}

func TestMentionedWorkspaceFilesInInlineListRejectsDirectoryLikeTargets(t *testing.T) {
	text := "`cmd/idiom`, `cmd/idiom/product_workflow.go`, `Makefile`. Run `go test ./cmd/idiom`."
	got := MentionedWorkspaceFilesInInlineList(text)
	want := []string{"cmd/idiom/product_workflow.go", "Makefile"}

	if len(got) != len(want) {
		t.Fatalf("expected file-only paths %#v, got %#v", want, got)
	}

	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected file-only paths %#v, got %#v", want, got)
		}
	}
}

func TestMentionedWorkspacePathsCoversCommonSourceExtensions(t *testing.T) {
	text := strings.Join([]string{
		"native/src/system.cpp",
		"native/include/system.hpp:12",
		"scripts/player.gd",
		"src/app.tsx",
		"pkg/service.py",
		"src/main.rs",
		"src/App.java",
		"tools/build.sh",
	}, " ")
	got := MentionedWorkspacePaths(text)
	want := []string{
		"native/src/system.cpp",
		"native/include/system.hpp",
		"scripts/player.gd",
		"src/app.tsx",
		"pkg/service.py",
		"src/main.rs",
		"src/App.java",
		"tools/build.sh",
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d paths, got %#v", len(want), got)
	}

	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected paths %#v, got %#v", want, got)
		}
	}
}

func TestMentionedWorkspacePathsUnderHeadings(t *testing.T) {
	text := strings.Join([]string{
		"# Narrow atom",
		"",
		"Files-to-Modify:",
		"- native/src/item_lifecycle.cpp",
		"- scripts/player.gd",
		"",
		"Evidence:",
		"- docs/design/spec.md",
	}, "\n")
	got := MentionedWorkspacePathsUnderHeadings(text, "files to modify", "files-to-modify")
	want := []string{"native/src/item_lifecycle.cpp", "scripts/player.gd"}

	if len(got) != len(want) {
		t.Fatalf("expected %d paths, got %#v", len(want), got)
	}

	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected paths %#v, got %#v", want, got)
		}
	}
}

func TestMentionedWorkspacePathsUnderHeadingsOnlyUsesFirstTableCell(t *testing.T) {
	text := strings.Join([]string{
		"# Narrow atom",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"| --- | --- |",
		"| native/tests/test_funeral.cpp | Add STRANGER, BEST_FRIEND, and RIVAL/NEMESIS coverage |",
		"| `native/src/simulation/creatures/social_welfare.cpp` | Adjust unburied penalty scaling |",
		"",
		"## Evidence",
		"- docs/design/spec.md",
	}, "\n")
	got := MentionedWorkspacePathsUnderHeadings(text, "files to modify")
	want := []string{"native/tests/test_funeral.cpp", "native/src/simulation/creatures/social_welfare.cpp"}

	if len(got) != len(want) {
		t.Fatalf("expected %d paths, got %#v", len(want), got)
	}

	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected paths %#v, got %#v", want, got)
		}
	}

	for _, value := range got {
		if value == "RIVAL/NEMESIS" {
			t.Fatalf("table prose leaked into path set: %#v", got)
		}
	}
}
