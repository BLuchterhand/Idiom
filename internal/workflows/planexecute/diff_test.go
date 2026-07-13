package planexecute

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"strings"
)

// TestUnifiedDiffIdenticalReturnsEmpty verifies that no spurious hunks
// are emitted when the before and after payloads match byte-for-byte.
// This is the canonical "no-op write" path: a user editing a file and
// hitting save without changes should not clutter the plan log.
var _ = ginkgo.It("unified diff identical returns empty", func() {
	got := UnifiedDiff("a.txt", "hello\nworld\n", "hello\nworld\n")
	gomega.Expect(got != "").To(gomega.BeFalse(), fmt.Sprintf("identical inputs should return empty, got:\n%s", got))
})

var _ = ginkgo.It("unified diff headers include path", func() {
	got := UnifiedDiff("pkg/foo.go", "a\n", "b\n")
	gomega.Expect(strings.Contains(got, "--- a/pkg/foo.go")).To(gomega.BeTrue(), fmt.Sprintf("missing --- header: %q", got))
	gomega.Expect(strings.Contains(got, "+++ b/pkg/foo.go")).To(gomega.BeTrue(), fmt.Sprintf("missing +++ header: %q", got))
})

var _ = ginkgo.It("unified diff simple replace", func() {
	got := UnifiedDiff("x", "one\ntwo\nthree\n", "one\nTWO\nthree\n")
	want := "--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"
	gomega.Expect(got != want).To(gomega.BeFalse(), fmt.Sprintf("diff mismatch\n got:\n%s\nwant:\n%s", got, want))
})

var _ = ginkgo.It("unified diff pure addition", func() {
	got := UnifiedDiff("x", "", "hello\n")
	gomega.Expect(strings.Contains(got, "+hello")).To(gomega.BeTrue(), fmt.Sprintf("should include added line: %q", got))
	gomega.Expect(got).NotTo(gomega.And(
		gomega.ContainSubstring("-"),
		gomega.Not(gomega.ContainSubstring("--- a/x")),
	), fmt.Sprintf("should not emit removal lines for pure add: %q", got))
	// Before side has zero lines; header should encode that as "0,0".
	gomega.Expect(strings.Contains(got, "@@ -0,0 +1,1 @@")).To(gomega.BeTrue(), fmt.Sprintf("pure-add header malformed: %q", got))
})

var _ = ginkgo.It("unified diff pure deletion", func() {
	got := UnifiedDiff("x", "hello\n", "")
	gomega.Expect(strings.Contains(got, "-hello")).To(gomega.BeTrue(), fmt.Sprintf("should emit removal: %q", got))
	gomega.Expect(strings.Contains(got, "@@ -1,1 +0,0 @@")).To(gomega.BeTrue(), fmt.Sprintf("pure-del header malformed: %q", got))
})

// TestUnifiedDiffMissingTrailingNewline locks in the GNU-diff convention
// that when a final-line change is accompanied by a missing trailing
// newline on one side, the "\ No newline at end of file" marker is
// emitted. Without the marker the plan-panel view cannot distinguish
// "added a newline" from "replaced the last line".
var _ = ginkgo.It("unified diff missing trailing newline", func() {
	// Both sides change the last line; before lacks the trailing NL so
	// the removal side must carry the "No newline" annotation.
	got := UnifiedDiff("x", "one\ntwo", "one\nTWO\n")
	gomega.Expect(strings.Contains(got, "\\ No newline at end of file")).To(gomega.BeTrue(), fmt.Sprintf("missing trailing-newline marker: %q", got))
	gomega.Expect(got).To(gomega.ContainSubstring("-two"), fmt.Sprintf("expected both removal and addition lines: %q", got))
	gomega.Expect(got).To(gomega.ContainSubstring("+TWO"), fmt.Sprintf("expected both removal and addition lines: %q", got))
})

// TestUnifiedDiffCoalescesNearbyEdits verifies the ctx=3 hunk merger:
// two edits 4 lines apart should produce a single hunk rather than
// two neighboring hunks that duplicate their shared context.
var _ = ginkgo.It("unified diff coalesces nearby edits", func() {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\n"
	after := "a\nB\nc\nd\ne\nf\ng\nH\ni\n"
	got := UnifiedDiff("x", before, after)
	hunks := strings.Count(got, "@@ ")
	gomega.Expect(hunks != 1).To(gomega.BeFalse(), fmt.Sprintf("expected edits within 2*ctx to merge into 1 hunk, got %d hunks:\n%s", hunks, got))
})

// TestUnifiedDiffSplitsFarApartEdits verifies that edits far enough
// apart do produce separate hunks; otherwise the single-hunk output
// would drag unrelated chunks of the file into the plan view.
var _ = ginkgo.It("unified diff splits far apart edits", func() {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\np\n"
	after := "A\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\nP\n"
	got := UnifiedDiff("x", before, after)
	hunks := strings.Count(got, "@@ ")
	gomega.Expect(hunks != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 hunks for far-apart edits, got %d:\n%s", hunks, got))
})

var _ = ginkgo.It("unified diff empty both returns empty", func() {
	if got := UnifiedDiff("x", "", ""); got != "" {
		ginkgo.Fail(fmt.Sprintf("empty/empty should return empty, got %q", got))
	}
})

var _ = ginkgo.It("split lines handles edge cases", func() {
	tests := []struct {
		in      string
		wantLen int
		wantNL  bool
	}{
		{"", 0, false},
		{"\n", 1, true},
		{"a", 1, false},
		{"a\n", 1, true},
		{"a\nb", 2, false},
		{"a\nb\n", 2, true},
	}
	for _, tc := range tests {
		lines, hadNL := splitLines(tc.in)
		gomega.Expect(lines).To(gomega.HaveLen(tc.wantLen), fmt.Sprintf("splitLines(%q) = (%v, %v), want (len=%d, nl=%v)", tc.in, lines, hadNL, tc.wantLen, tc.wantNL))
		gomega.Expect(hadNL).To(gomega.Equal(tc.wantNL), fmt.Sprintf("splitLines(%q) = (%v, %v), want (len=%d, nl=%v)", tc.in, lines, hadNL, tc.wantLen, tc.wantNL))

	}
})
