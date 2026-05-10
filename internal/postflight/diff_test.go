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
	if len(mod.AddedLines) != 1 || mod.AddedLines[0] != "// added comment" {
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
	if len(d.Files[0].AddedLines) != 1 || d.Files[0].AddedLines[0] != "real-add" {
		t.Fatalf("metadata leaked into AddedLines: %v", d.Files[0].AddedLines)
	}
}
