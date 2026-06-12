package postflight

import (
	"strings"
	"testing"
)

const sampleGitDiff = `diff --git a/foo/bar.go b/foo/bar.go
index abc..def 100644
--- a/foo/bar.go
+++ b/foo/bar.go
@@ -1,3 +1,4 @@
 package foo

+// added comment
 func Bar() {}
diff --git a/foo/new.go b/foo/new.go
new file mode 100644
--- /dev/null
+++ b/foo/new.go
@@ -0,0 +1,2 @@
+package foo
+func New() {}
diff --git a/foo/gone.go b/foo/gone.go
deleted file mode 100644
--- a/foo/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package foo
-func Gone() {}
`

// TestParseDiffExtractsModifiedAddedAndDeleted is the canonical
// happy path: a three-file diff with one modify, one new file, one
// deletion must round-trip through the parser with the right Added/
// Deleted flags and the right post-image path strings (with the
// `b/` prefix stripped). This is the load-bearing parse contract;
// every check downstream depends on these fields being correct.
func TestParseDiffExtractsModifiedAddedAndDeleted(t *testing.T) {
	d := ParseDiff(sampleGitDiff)
	if len(d.Files) != 3 {
		t.Fatalf("expected 3 files, got %d (%+v)", len(d.Files), d.Files)
	}

	mod, added, deleted := d.Files[0], d.Files[1], d.Files[2]
	if mod.Path != "foo/bar.go" || mod.Added || mod.Deleted {
		t.Fatalf("modify file decoded wrong: %+v", mod)
	}

	if len(mod.AddedLines) != 1 || mod.AddedLines[0].Body != "// added comment" {
		t.Fatalf("expected one added line for modify, got %v", mod.AddedLines)
	}

	if added.Path != "foo/new.go" || !added.Added || added.Deleted {
		t.Fatalf("added file decoded wrong: %+v", added)
	}

	if len(added.AddedLines) != 2 {
		t.Fatalf("expected 2 added lines on new file, got %d", len(added.AddedLines))
	}

	if !deleted.Deleted {
		t.Fatalf("deleted file should have Deleted=true: %+v", deleted)
	}

	// Doc contract: deletions populate Path from the pre-image so
	// callers can identify the removed file without separately
	// consulting OldPath. Catches the regression where Path was
	// left empty for /dev/null post-image.
	if deleted.Path != "foo/gone.go" {
		t.Fatalf("deleted file Path should fall back to pre-image: %+v", deleted)
	}
}

// TestParseDiffTracksPostImageLineNumbers verifies the load-bearing
// fix from the Phase 4/5 review: AddedLines must carry the actual
// post-image line number of each `+` line, parsed from the
// surrounding `@@ -A,B +C,D @@` hunk header. The CLI prints
// `<path>:<line>` and operators click those — index-in-block
// numbers point at the wrong line in a multi-hunk file.
func TestParseDiffTracksPostImageLineNumbers(t *testing.T) {
	body := `--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package x
+// added at line 2
 func A() {}
 func B() {}
@@ -10,2 +11,3 @@
 func C() {}
+// added at line 12
 func D() {}
`
	d := ParseDiff(body)

	if len(d.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(d.Files))
	}

	got := d.Files[0].AddedLines
	if len(got) != 2 {
		t.Fatalf("expected 2 added lines, got %d (%+v)", len(got), got)
	}

	if got[0].Line != 2 || got[0].Body != "// added at line 2" {
		t.Fatalf("first added line wrong: %+v", got[0])
	}

	if got[1].Line != 12 || got[1].Body != "// added at line 12" {
		t.Fatalf("second added line wrong: %+v", got[1])
	}
}

// TestParseDiffHandlesSingleLineHunkHeader verifies that the
// shorter `@@ -N +M @@` form (no comma+count, used when only one
// line is in the hunk) is recognized. git diff emits this for
// minimal hunks and an over-strict regex would lose them.
func TestParseDiffHandlesSingleLineHunkHeader(t *testing.T) {
	body := `--- a/foo.go
+++ b/foo.go
@@ -5 +5 @@
+only-line
`
	d := ParseDiff(body)

	if len(d.Files) != 1 || len(d.Files[0].AddedLines) != 1 {
		t.Fatalf("parse failed for single-line hunk: %+v", d)
	}

	if d.Files[0].AddedLines[0].Line != 5 {
		t.Fatalf("expected line 5, got %d", d.Files[0].AddedLines[0].Line)
	}
}

// TestParseDiffContextLinesAdvanceCounter verifies the line-tracking
// math: a `+` line preceded by N context lines starts at hunk-start
// + N. A bug in counter advancement would shift every line number
// by the count of preceding context lines.
func TestParseDiffContextLinesAdvanceCounter(t *testing.T) {
	body := `--- a/foo.go
+++ b/foo.go
@@ -10,5 +10,6 @@
 line 10
 line 11
 line 12
+inserted at line 13
 line 13
 line 14
`
	d := ParseDiff(body)

	if len(d.Files) != 1 || len(d.Files[0].AddedLines) != 1 {
		t.Fatalf("parse failed: %+v", d)
	}

	if got := d.Files[0].AddedLines[0].Line; got != 13 {
		t.Fatalf("context lines should advance counter: got line %d, want 13", got)
	}
}

// TestParseDiffDeletionLinesDoNotAdvanceCounter verifies the other
// half of the line-tracking math: `-` lines are pre-image-only
// and must NOT advance the post-image counter, otherwise a hunk
// with a delete-then-add pattern would report wrong line numbers.
func TestParseDiffDeletionLinesDoNotAdvanceCounter(t *testing.T) {
	body := `--- a/foo.go
+++ b/foo.go
@@ -1,4 +1,3 @@
 keep
-delete-me-1
-delete-me-2
+replace-at-line-2
`
	d := ParseDiff(body)

	if len(d.Files[0].AddedLines) != 1 {
		t.Fatalf("expected 1 added line: %+v", d.Files[0].AddedLines)
	}

	if got := d.Files[0].AddedLines[0].Line; got != 2 {
		t.Fatalf("delete lines should not advance counter: got line %d, want 2", got)
	}
}

// TestParseDiffOldPathPopulatedForRenameSemantics verifies that
// OldPath is set from the `--- a/<X>` line whenever the pre-image
// isn't /dev/null. Idiom doesn't currently produce rename diffs,
// but downstream tooling could grow to use OldPath for cross-
// referencing the pre/post sides.
func TestParseDiffOldPathPopulatedForRenameSemantics(t *testing.T) {
	d := ParseDiff(sampleGitDiff)
	if d.Files[0].OldPath != "foo/bar.go" {
		t.Fatalf("OldPath should be set for modify: %+v", d.Files[0])
	}

	if d.Files[1].OldPath != "" {
		t.Fatalf("OldPath should be empty for added (pre-image /dev/null): %+v", d.Files[1])
	}

	if d.Files[2].OldPath != "foo/gone.go" {
		t.Fatalf("OldPath should be set for delete: %+v", d.Files[2])
	}
}

// TestParseDiffOrphanPlusPlusPlusIsIgnored verifies the parser
// hardening from the review: a `+++` line that appears without a
// preceding `---` (corrupt diff) is ignored rather than synthesizing
// a half-populated FileChange. Earlier code created a FileChange
// with Path set but OldPath/Added unset, which would let allowlist
// or marker checks fire on garbage paths.
func TestParseDiffOrphanPlusPlusPlusIsIgnored(t *testing.T) {
	body := "+++ b/orphan.go\n+content\n--- a/real.go\n+++ b/real.go\n@@ -1 +1 @@\n+real-add\n"
	d := ParseDiff(body)

	if len(d.Files) != 1 {
		t.Fatalf("orphan +++ must not produce a FileChange; got %v", d.Files)
	}

	if d.Files[0].Path != "real.go" {
		t.Fatalf("expected real.go FileChange, got %+v", d.Files[0])
	}
}

// TestModifiedPathsOmitsDeletions verifies the path-list helper
// excludes deletions. The allowlist check uses this — a deletion
// is a "removal," not a "write outside the allowlist," so blending
// them in would produce false positives.
func TestModifiedPathsOmitsDeletions(t *testing.T) {
	d := ParseDiff(sampleGitDiff)
	paths := d.ModifiedPaths()

	for _, p := range paths {
		if strings.Contains(p, "gone.go") {
			t.Fatalf("ModifiedPaths must not include the deleted file; got %v", paths)
		}
	}

	if len(paths) != 2 {
		t.Fatalf("expected modify+added (2 paths), got %v", paths)
	}
}

// TestParseDiffStripsGitPrefixesAndTabTimestamps verifies the path
// normalization: both `a/` and `b/` prefixes are stripped, and the
// POSIX `diff -u`'s tab+timestamp suffix is dropped so paths
// compare cleanly against project paths.
func TestParseDiffStripsGitPrefixesAndTabTimestamps(t *testing.T) {
	body := "--- a/foo/bar.go\t2026-05-09 10:00:00.000000000 -0400\n+++ b/foo/bar.go\t2026-05-09 10:01:00.000000000 -0400\n+x\n"
	d := ParseDiff(body)

	if len(d.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(d.Files))
	}

	if d.Files[0].Path != "foo/bar.go" {
		t.Fatalf("path should strip prefix and timestamp, got %q", d.Files[0].Path)
	}
}

// TestParseDiffEmptyBodyProducesEmptyDiff verifies the no-changes
// path: an empty (or whitespace-only) string parses to an empty
// Diff with no error. The postflight pipeline treats "no changes"
// as "no checks have anything to flag" rather than failing, so the
// empty case must be representable without panic.
func TestParseDiffEmptyBodyProducesEmptyDiff(t *testing.T) {
	d := ParseDiff("")
	if len(d.Files) != 0 {
		t.Fatalf("expected empty diff, got %+v", d)
	}

	if len(d.ModifiedPaths()) != 0 {
		t.Fatalf("expected no modified paths from empty diff, got %v", d.ModifiedPaths())
	}
}

// TestParseDiffIgnoresUnknownMetadataLines verifies that `index`
// lines, `new file mode`, hunk headers `@@`, and other git metadata
// don't pollute AddedLines — only actual content additions count.
// A regression here would make the question-marker check trigger
// on `index 0000000` (which contains the substring "QUESTION" if
// the hash happens to look that way — unlikely, but the parser
// should be deterministic regardless).
func TestParseDiffIgnoresUnknownMetadataLines(t *testing.T) {
	body := "diff --git a/x b/x\nindex 0..1 100644\n--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n line\n+real-add\n"
	d := ParseDiff(body)

	if len(d.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(d.Files))
	}

	if len(d.Files[0].AddedLines) != 1 || d.Files[0].AddedLines[0].Body != "real-add" {
		t.Fatalf("metadata leaked into AddedLines: %v", d.Files[0].AddedLines)
	}
}
