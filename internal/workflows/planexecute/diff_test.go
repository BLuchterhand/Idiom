package planexecute

import (
	"strings"
	"testing"
)

// TestUnifiedDiffIdenticalReturnsEmpty verifies that no spurious hunks
// are emitted when the before and after payloads match byte-for-byte.
// This is the canonical "no-op write" path: a user editing a file and
// hitting save without changes should not clutter the plan log.
func TestUnifiedDiffIdenticalReturnsEmpty(t *testing.T) {
	got := UnifiedDiff("a.txt", "hello\nworld\n", "hello\nworld\n")
	if got != "" {
		t.Fatalf("identical inputs should return empty, got:\n%s", got)
	}
}

func TestUnifiedDiffHeadersIncludePath(t *testing.T) {
	got := UnifiedDiff("pkg/foo.go", "a\n", "b\n")
	if !strings.Contains(got, "--- a/pkg/foo.go") {
		t.Errorf("missing --- header: %q", got)
	}

	if !strings.Contains(got, "+++ b/pkg/foo.go") {
		t.Errorf("missing +++ header: %q", got)
	}
}

func TestUnifiedDiffSimpleReplace(t *testing.T) {
	got := UnifiedDiff("x", "one\ntwo\nthree\n", "one\nTWO\nthree\n")
	want := "--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"

	if got != want {
		t.Fatalf("diff mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUnifiedDiffPureAddition(t *testing.T) {
	got := UnifiedDiff("x", "", "hello\n")
	if !strings.Contains(got, "+hello") {
		t.Errorf("should include added line: %q", got)
	}

	if strings.Contains(got, "-") && !strings.Contains(got, "--- a/x") {
		t.Errorf("should not emit removal lines for pure add: %q", got)
	}

	// Before side has zero lines; header should encode that as "0,0".
	if !strings.Contains(got, "@@ -0,0 +1,1 @@") {
		t.Errorf("pure-add header malformed: %q", got)
	}
}

func TestUnifiedDiffPureDeletion(t *testing.T) {
	got := UnifiedDiff("x", "hello\n", "")
	if !strings.Contains(got, "-hello") {
		t.Errorf("should emit removal: %q", got)
	}

	if !strings.Contains(got, "@@ -1,1 +0,0 @@") {
		t.Errorf("pure-del header malformed: %q", got)
	}
}

// TestUnifiedDiffMissingTrailingNewline locks in the GNU-diff convention
// that when a final-line change is accompanied by a missing trailing
// newline on one side, the "\ No newline at end of file" marker is
// emitted. Without the marker the plan-panel view cannot distinguish
// "added a newline" from "replaced the last line".
func TestUnifiedDiffMissingTrailingNewline(t *testing.T) {
	// Both sides change the last line; before lacks the trailing NL so
	// the removal side must carry the "No newline" annotation.
	got := UnifiedDiff("x", "one\ntwo", "one\nTWO\n")
	if !strings.Contains(got, "\\ No newline at end of file") {
		t.Errorf("missing trailing-newline marker: %q", got)
	}

	if !strings.Contains(got, "-two") || !strings.Contains(got, "+TWO") {
		t.Errorf("expected both removal and addition lines: %q", got)
	}
}

// TestUnifiedDiffCoalescesNearbyEdits verifies the ctx=3 hunk merger:
// two edits 4 lines apart should produce a single hunk rather than
// two neighboring hunks that duplicate their shared context.
func TestUnifiedDiffCoalescesNearbyEdits(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\n"
	after := "a\nB\nc\nd\ne\nf\ng\nH\ni\n"
	got := UnifiedDiff("x", before, after)
	hunks := strings.Count(got, "@@ ")

	if hunks != 1 {
		t.Errorf("expected edits within 2*ctx to merge into 1 hunk, got %d hunks:\n%s", hunks, got)
	}
}

// TestUnifiedDiffSplitsFarApartEdits verifies that edits far enough
// apart do produce separate hunks; otherwise the single-hunk output
// would drag unrelated chunks of the file into the plan view.
func TestUnifiedDiffSplitsFarApartEdits(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\np\n"
	after := "A\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\nP\n"
	got := UnifiedDiff("x", before, after)
	hunks := strings.Count(got, "@@ ")

	if hunks != 2 {
		t.Errorf("expected 2 hunks for far-apart edits, got %d:\n%s", hunks, got)
	}
}

func TestUnifiedDiffEmptyBothReturnsEmpty(t *testing.T) {
	if got := UnifiedDiff("x", "", ""); got != "" {
		t.Fatalf("empty/empty should return empty, got %q", got)
	}
}

func TestSplitLinesHandlesEdgeCases(t *testing.T) {
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
		if len(lines) != tc.wantLen || hadNL != tc.wantNL {
			t.Errorf("splitLines(%q) = (%v, %v), want (len=%d, nl=%v)", tc.in, lines, hadNL, tc.wantLen, tc.wantNL)
		}
	}
}
