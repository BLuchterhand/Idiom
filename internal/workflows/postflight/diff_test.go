package postflight

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Diff parsing", func() {
	It("extracts modified, added, and deleted files", func() {
		diff := ParseDiff(sampleGitDiff)
		Expect(diff.Files).To(HaveLen(3))

		modified, added, deleted := diff.Files[0], diff.Files[1], diff.Files[2]
		Expect(modified.Path).To(Equal("foo/bar.go"))
		Expect(modified.Added).To(BeFalse())
		Expect(modified.Deleted).To(BeFalse())
		Expect(modified.AddedLines).To(HaveLen(1))
		Expect(modified.AddedLines[0].Body).To(Equal("// added comment"))
		Expect(added.Path).To(Equal("foo/new.go"))
		Expect(added.Added).To(BeTrue())
		Expect(added.Deleted).To(BeFalse())
		Expect(added.AddedLines).To(HaveLen(2))
		Expect(deleted.Deleted).To(BeTrue())
		Expect(deleted.Path).To(Equal("foo/gone.go"))
	})

	// TestParseDiffTracksPostImageLineNumbers verifies the load-bearing
	// fix from the Phase 4/5 review: AddedLines must carry the actual
	// post-image line number of each `+` line, parsed from the
	// surrounding `@@ -A,B +C,D @@` hunk header. The CLI prints
	// `<path>:<line>` and operators click those — index-in-block
	// numbers point at the wrong line in a multi-hunk file.
	It("tracks post-image line numbers across hunks", func() {
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
		diff := ParseDiff(body)
		Expect(diff.Files).To(HaveLen(1))
		Expect(diff.Files[0].AddedLines).To(Equal([]AddedLine{
			{Line: 2, Body: "// added at line 2"},
			{Line: 12, Body: "// added at line 12"},
		}))
	})

	// TestParseDiffHandlesSingleLineHunkHeader verifies that the
	// shorter `@@ -N +M @@` form (no comma+count, used when only one
	// line is in the hunk) is recognized. git diff emits this for
	// minimal hunks and an over-strict regex would lose them.
	It("handles a single-line hunk header", func() {
		body := `--- a/foo.go
+++ b/foo.go
@@ -5 +5 @@
+only-line
`
		diff := ParseDiff(body)
		Expect(diff.Files).To(HaveLen(1))
		Expect(diff.Files[0].AddedLines).To(HaveLen(1))
		Expect(diff.Files[0].AddedLines[0].Line).To(Equal(5))
	})

	// TestParseDiffContextLinesAdvanceCounter verifies the line-tracking
	// math: a `+` line preceded by N context lines starts at hunk-start
	// + N. A bug in counter advancement would shift every line number
	// by the count of preceding context lines.
	It("advances the post-image counter for context lines", func() {
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
		diff := ParseDiff(body)
		Expect(diff.Files).To(HaveLen(1))
		Expect(diff.Files[0].AddedLines).To(HaveLen(1))
		Expect(diff.Files[0].AddedLines[0].Line).To(Equal(13))
	})

	// TestParseDiffDeletionLinesDoNotAdvanceCounter verifies the other
	// half of the line-tracking math: `-` lines are pre-image-only
	// and must NOT advance the post-image counter, otherwise a hunk
	// with a delete-then-add pattern would report wrong line numbers.
	It("does not advance the post-image counter for deletion lines", func() {
		body := `--- a/foo.go
+++ b/foo.go
@@ -1,4 +1,3 @@
 keep
-delete-me-1
-delete-me-2
+replace-at-line-2
`
		diff := ParseDiff(body)
		Expect(diff.Files[0].AddedLines).To(HaveLen(1))
		Expect(diff.Files[0].AddedLines[0].Line).To(Equal(2))
	})

	// TestParseDiffOldPathPopulatedForRenameSemantics verifies that
	// OldPath is set from the `--- a/<X>` line whenever the pre-image
	// isn't /dev/null. Idiom doesn't currently produce rename diffs,
	// but downstream tooling could grow to use OldPath for cross-
	// referencing the pre/post sides.
	It("preserves pre-image paths for rename semantics", func() {
		diff := ParseDiff(sampleGitDiff)
		Expect(diff.Files[0].OldPath).To(Equal("foo/bar.go"))
		Expect(diff.Files[1].OldPath).To(BeEmpty())
		Expect(diff.Files[2].OldPath).To(Equal("foo/gone.go"))
	})

	// TestParseDiffOrphanPlusPlusPlusIsIgnored verifies the parser
	// hardening from the review: a `+++` line that appears without a
	// preceding `---` (corrupt diff) is ignored rather than synthesizing
	// a half-populated FileChange. Earlier code created a FileChange
	// with Path set but OldPath/Added unset, which would let allowlist
	// or marker checks fire on garbage paths.
	It("ignores orphan post-image headers", func() {
		diff := ParseDiff("+++ b/orphan.go\n+content\n--- a/real.go\n+++ b/real.go\n@@ -1 +1 @@\n+real-add\n")
		Expect(diff.Files).To(HaveLen(1))
		Expect(diff.Files[0].Path).To(Equal("real.go"))
	})

	// TestModifiedPathsOmitsDeletions verifies the path-list helper
	// excludes deletions. The allowlist check uses this — a deletion
	// is a "removal," not a "write outside the allowlist," so blending
	// them in would produce false positives.
	It("omits deletions from modified paths", func() {
		Expect(ParseDiff(sampleGitDiff).ModifiedPaths()).To(ConsistOf("foo/bar.go", "foo/new.go"))
	})

	// TestParseDiffStripsGitPrefixesAndTabTimestamps verifies the path
	// normalization: both `a/` and `b/` prefixes are stripped, and the
	// POSIX `diff -u`'s tab+timestamp suffix is dropped so paths
	// compare cleanly against project paths.
	It("strips git prefixes and tab-separated timestamps", func() {
		body := "--- a/foo/bar.go\t2026-05-09 10:00:00.000000000 -0400\n+++ b/foo/bar.go\t2026-05-09 10:01:00.000000000 -0400\n+x\n"
		diff := ParseDiff(body)
		Expect(diff.Files).To(HaveLen(1))
		Expect(diff.Files[0].Path).To(Equal("foo/bar.go"))
	})

	// TestParseDiffEmptyBodyProducesEmptyDiff verifies the no-changes
	// path: an empty (or whitespace-only) string parses to an empty
	// Diff with no error. The postflight pipeline treats "no changes"
	// as "no checks have anything to flag" rather than failing, so the
	// empty case must be representable without panic.
	It("returns an empty diff for an empty body", func() {
		diff := ParseDiff("")
		Expect(diff.Files).To(BeEmpty())
		Expect(diff.ModifiedPaths()).To(BeEmpty())
	})

	// TestParseDiffIgnoresUnknownMetadataLines verifies that `index`
	// lines, `new file mode`, hunk headers `@@`, and other git metadata
	// don't pollute AddedLines — only actual content additions count.
	// A regression here would make the question-marker check trigger
	// on `index 0000000` (which contains the substring "QUESTION" if
	// the hash happens to look that way — unlikely, but the parser
	// should be deterministic regardless).
	It("ignores unknown metadata lines", func() {
		body := "diff --git a/x b/x\nindex 0..1 100644\n--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n line\n+real-add\n"
		diff := ParseDiff(body)
		Expect(diff.Files).To(HaveLen(1))
		Expect(diff.Files[0].AddedLines).To(Equal([]AddedLine{{Line: 2, Body: "real-add"}}))
	})
})
