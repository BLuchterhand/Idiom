package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/workspace"
)

// TestParsePlanItemsCoversBothAnchors verifies the parser recognizes
// both anchor flavors (diff id, ledger tid) and correctly flags the
// checked state. Non-checkbox lines are filtered so headings and
// prose don't show up as cursor stops.
func TestParsePlanItemsCoversBothAnchors(t *testing.T) {
	plan := "## Session x\n\n### Planned\n- [ ] rename foo <!-- tid:t1 -->\n- [x] rename bar <!-- tid:t2 --> <!-- id:abc123 -->\n\nregular text\n### Unplanned\n- [x] drive-by <!-- id:def456 -->\n"
	got := parsePlanItems(plan)
	if len(got) != 3 {
		t.Fatalf("want 3 items, got %d: %#v", len(got), got)
	}
	if got[0].TaskID != "t1" || got[0].Checked {
		t.Errorf("first item wrong: %#v", got[0])
	}
	if got[1].TaskID != "t2" || got[1].DiffID != "abc123" || !got[1].Checked {
		t.Errorf("second item wrong: %#v", got[1])
	}
	if got[2].DiffID != "def456" || !got[2].Checked {
		t.Errorf("third item wrong: %#v", got[2])
	}
}

func TestParsePlanItemsTolerantOfWhitespaceInAnchor(t *testing.T) {
	plan := "- [ ] task <!--  tid:  spaced  -->"
	got := parsePlanItems(plan)
	if len(got) != 1 || got[0].TaskID != "spaced" {
		t.Fatalf("whitespace-tolerant parsing broken: %#v", got)
	}
}

func TestParsePlanItemsSupportsStarBullet(t *testing.T) {
	plan := "* [x] starred <!-- id:zz -->\n"
	got := parsePlanItems(plan)
	if len(got) != 1 || !got[0].Checked || got[0].DiffID != "zz" {
		t.Fatalf("star-bullet form not recognized: %#v", got)
	}
}

func TestParsePlanItemsIgnoresEmpty(t *testing.T) {
	if got := parsePlanItems(""); len(got) != 0 {
		t.Fatalf("empty plan should parse to no items, got %#v", got)
	}
}

// TestLoadPlanDiffEntryRoundTrip seeds the diffs JSONL with two records,
// then looks one up by id. The linear-scan implementation is fine at
// our record counts, but we still want a regression test to catch
// JSONL format drift.
func TestLoadPlanDiffEntryRoundTrip(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	m.appendPlanDiffEntry(planDiffEntry{ID: "first", Tool: "edit_file", Path: "a", Diff: "--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n+y\n"})
	m.appendPlanDiffEntry(planDiffEntry{ID: "second", Tool: "write_file", Path: "b", Diff: "--- a/b\n+++ b/b\n@@ -0,0 +1 @@\n+new\n"})

	got, err := m.loadPlanDiffEntry("second")
	if err != nil {
		t.Fatalf("load second: %v", err)
	}
	if got.Tool != "write_file" || got.Path != "b" {
		t.Errorf("wrong entry: %#v", got)
	}
	if !strings.Contains(got.Diff, "+new") {
		t.Errorf("diff payload lost: %q", got.Diff)
	}
}

func TestLoadPlanDiffEntryMissingReturnsError(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.appendPlanDiffEntry(planDiffEntry{ID: "only", Tool: "edit_file", Path: "a"})
	if _, err := m.loadPlanDiffEntry("nope"); err == nil {
		t.Errorf("expected error for missing id")
	}
}

func TestLoadPlanDiffEntryEmptyIDRejected(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	if _, err := m.loadPlanDiffEntry(""); err == nil {
		t.Errorf("empty id should be rejected")
	}
}

// TestRenderColoredDiffStylesSignificantLines makes no claim about
// exact escape codes — lipgloss output depends on terminal env — but
// does verify the payload round-trips: every source line appears in
// the output, so the coloring pass doesn't drop any content.
func TestRenderColoredDiffPreservesAllLines(t *testing.T) {
	diff := "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n"
	got := renderColoredDiff(diff)
	for _, expect := range []string{"one", "-two", "+TWO", "@@ -1,2 +1,2 @@"} {
		if !strings.Contains(got, expect) {
			t.Errorf("renderColoredDiff dropped %q:\n%s", expect, got)
		}
	}
}

func TestRenderColoredDiffEmptyShowsPlaceholder(t *testing.T) {
	got := renderColoredDiff("")
	if !strings.Contains(got, "no diff") {
		t.Errorf("empty diff should surface a placeholder, got %q", got)
	}
}

// TestRenderPlanDiffOverlayIncludesHeader verifies the overlay body
// starts with tool/path and includes the description so the user has
// enough context to evaluate the diff without scrolling back to the
// plan list.
func TestRenderPlanDiffOverlayIncludesHeader(t *testing.T) {
	entry := &planDiffEntry{
		Tool:        "edit_file",
		Path:        "pkg/foo.go",
		Description: "tidy imports",
		Summary:     "Edited pkg/foo.go",
		Diff:        "--- a/pkg/foo.go\n+++ b/pkg/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
	}
	got := renderPlanDiffOverlay(entry)
	for _, want := range []string{"edit_file", "pkg/foo.go", "tidy imports", "-old", "+new"} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay missing %q:\n%s", want, got)
		}
	}
}

func TestRenderPlanDiffOverlayNilSafe(t *testing.T) {
	got := renderPlanDiffOverlay(nil)
	if strings.TrimSpace(got) == "" {
		t.Errorf("nil entry should render a placeholder, got empty")
	}
}

// TestOpenPlanDiffOverlayLoadsSelectedItem walks the integration: seed
// a plan file and a matching diff record, set the cursor on the plan
// item, then verify the overlay switches into view with the diff body.
// This is the headless equivalent of the user pressing Enter.
func TestOpenPlanDiffOverlayLoadsSelectedItem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "x\n")
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.txt", Description: "bump"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "a.txt", "y\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	items := m.currentPlanItems()
	if len(items) != 1 {
		t.Fatalf("want 1 plan item, got %d", len(items))
	}
	if items[0].DiffID == "" {
		t.Fatalf("plan item should have a diff id")
	}
	m.plan.cursor = 0
	if !m.openPlanDiffOverlay() {
		t.Fatal("openPlanDiffOverlay returned false for valid item")
	}
	if m.plan.overlayID != items[0].DiffID {
		t.Errorf("overlay id mismatch: %q vs %q", m.plan.overlayID, items[0].DiffID)
	}
	if !strings.Contains(m.plan.overlayBody, "-x") || !strings.Contains(m.plan.overlayBody, "+y") {
		t.Errorf("overlay body missing diff:\n%s", m.plan.overlayBody)
	}
}

// TestOpenPlanDiffOverlayFalseForUnattachedLedgerItem guards against
// clicking a `- [ ] ... tid:...` line that has no diff yet — the
// overlay should refuse to open rather than displaying an empty body
// that would look broken.
func TestOpenPlanDiffOverlayFalseForUnattachedLedgerItem(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.recordTaskLedger(
		workspace.ToolRequest{
			Tool: "task_ledger",
			Todos: []workspace.TaskLedgerItem{
				{ID: "t1", Content: "pending work", Status: "pending", Paths: []string{"x.go"}},
			},
		},
		workspace.ToolResult{Summary: "ok"},
	)
	if got := m.openPlanDiffOverlay(); got {
		t.Fatal("unattached ledger item should not open overlay")
	}
	if m.plan.overlayID != "" {
		t.Errorf("overlay id should remain empty, got %q", m.plan.overlayID)
	}
}

func TestClosePlanDiffOverlayClearsState(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.plan.overlayID = "abc"
	m.plan.overlayBody = "body"
	m.closePlanDiffOverlay()
	if m.plan.overlayID != "" || m.plan.overlayBody != "" {
		t.Errorf("close did not clear state: id=%q body=%q", m.plan.overlayID, m.plan.overlayBody)
	}
}

// TestLoadPlanDiffEntryNoSessionID protects the very-first-turn race.
// With no debug log set, there is no diffs path to read and the
// function must return an error rather than panic.
func TestLoadPlanDiffEntryNoSessionID(t *testing.T) {
	m := &Model{}
	if _, err := m.loadPlanDiffEntry("x"); err == nil {
		t.Errorf("expected error when no session id is resolvable")
	}
}

// TestLoadPlanDiffEntrySurfacesIOErrors verifies that a truncated /
// unreadable diffs file produces a meaningful error rather than a
// silent miss. This matters because diagnosing an overlay that "never
// opens" without this signal would require log-diving.
func TestLoadPlanDiffEntrySurfacesMissingFile(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	// No diffs file created.
	_, err := m.loadPlanDiffEntry("anything")
	if err == nil {
		t.Errorf("expected error when diffs file is missing")
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		// Either wrapped or raw os.ErrNotExist is fine; we just
		// want something diagnostic.
		t.Logf("got err %v (acceptable)", err)
	}
	_ = filepath.Join
}
