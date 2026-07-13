package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

// TestCurrentPlanSelectableRowsCoversItems verifies navigation rows recognize
// checkbox anchors and checked state while ignoring prose.
var _ = ginkgo.It("current plan selectable rows covers items", func() {
	plan := "## Session x\n\n### Planned\n- [ ] rename foo <!-- tid:t1 -->\n- [x] rename bar <!-- tid:t2 --> <!-- id:abc123 -->\n\nregular text\n### Unplanned\n- [x] drive-by <!-- id:def456 -->\n"
	rows := (&Model{}).currentPlanSelectableRowsForContent(plan)

	var got []planItemMeta
	for _, row := range rows {
		if row.Kind == planSelectableItem {
			got = append(got, row.Item)
		}
	}

	gomega.Expect(len(got)).To(gomega.Equal(3), fmt.Sprintf("want 3 items, got %d: %#v", len(got), got))

	gomega.Expect(got[0].TaskID).To(gomega.Equal("t1"), fmt.Sprintf("first item wrong: %#v", got[0]))
	gomega.Expect(got[0].Checked).To(gomega.BeFalse(), fmt.Sprintf("first item wrong: %#v", got[0]))

	gomega.Expect(got[1].TaskID).To(gomega.Equal("t2"), fmt.Sprintf("second item wrong: %#v", got[1]))
	gomega.Expect(got[1].DiffID).To(gomega.Equal("abc123"), fmt.Sprintf("second item wrong: %#v", got[1]))
	gomega.Expect(got[1].Checked).To(gomega.BeTrue(), fmt.Sprintf("second item wrong: %#v", got[1]))

	gomega.Expect(got[2].DiffID).To(gomega.Equal("def456"), fmt.Sprintf("third item wrong: %#v", got[2]))
	gomega.Expect(got[2].Checked).To(gomega.BeTrue(), fmt.Sprintf("third item wrong: %#v", got[2]))
})

var _ = ginkgo.It("parse plan item line tolerant of whitespace in anchor", func() {
	got, ok := parsePlanItemLine(0, "- [ ] task <!--  tid:  spaced  -->")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("whitespace-tolerant parsing broken: %#v", got))
	gomega.Expect(got.TaskID).To(gomega.Equal("spaced"), fmt.Sprintf("whitespace-tolerant parsing broken: %#v", got))
})

var _ = ginkgo.It("parse plan item line supports star bullet", func() {
	got, ok := parsePlanItemLine(0, "* [x] starred <!-- id:zz -->")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("star-bullet form not recognized: %#v", got))
	gomega.Expect(got.Checked).To(gomega.BeTrue(), fmt.Sprintf("star-bullet form not recognized: %#v", got))
	gomega.Expect(got.DiffID).To(gomega.Equal("zz"), fmt.Sprintf("star-bullet form not recognized: %#v", got))
})

var _ = ginkgo.It("parse plan item line supports plan approval action", func() {
	got, ok := parsePlanItemLine(0, "- [ ] Approve all edits <!-- plan-approval:all -->")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("plan approval action not recognized: %#v", got))
	gomega.Expect(got.PlanApprovalAction).To(gomega.Equal(planApprovalActionAll), fmt.Sprintf("plan approval action not recognized: %#v", got))
})

var _ = ginkgo.It("current plan selectable rows ignores empty", func() {
	{
		got := (&Model{}).currentPlanSelectableRowsForContent("")
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("empty plan should parse to no items, got %#v", got))
	}
})

// TestLoadPlanDiffEntryRoundTrip seeds the diffs JSONL with two records,
// then looks one up by id. The linear-scan implementation is fine at
// our record counts, but we still want a regression test to catch
// JSONL format drift.
var _ = ginkgo.It("load plan diff entry round trip", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)
	session := newDetachedPromptExecutionSession(context.Background(), m)

	session.appendPlanDiffEntry(planDiffEntry{ID: "first", Tool: "edit_file", Path: "a", Diff: "--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n+y\n"})
	session.appendPlanDiffEntry(planDiffEntry{ID: "second", Tool: "write_file", Path: "b", Diff: "--- a/b\n+++ b/b\n@@ -0,0 +1 @@\n+new\n"})

	got, err := m.loadPlanDiffEntry("second")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load second: %v", err))

	gomega.Expect(got.Tool).To(gomega.Equal("write_file"), fmt.Sprintf("wrong entry: %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("b"), fmt.Sprintf("wrong entry: %#v", got))

	gomega.Expect(strings.Contains(got.Diff, "+new")).To(gomega.BeTrue(), fmt.Sprintf("diff payload lost: %q", got.Diff))
})

var _ = ginkgo.It("load plan diff entry missing returns error", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)
	session := newDetachedPromptExecutionSession(context.Background(), m)
	session.appendPlanDiffEntry(planDiffEntry{ID: "only", Tool: "edit_file", Path: "a"})

	{
		_, err := m.loadPlanDiffEntry("nope")
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected error for missing id"))
	}
})

var _ = ginkgo.It("load plan diff entry empty id rejected", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)

	{
		_, err := m.loadPlanDiffEntry("")
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("empty id should be rejected"))
	}
})

// TestRenderColoredDiffStylesSignificantLines makes no claim about
// exact escape codes — lipgloss output depends on terminal env — but
// does verify the payload round-trips: every source line appears in
// the output, so the coloring pass doesn't drop any content.
var _ = ginkgo.It("render colored diff preserves all lines", func() {
	diff := "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n"
	got := renderColoredDiff(diff)

	for _, expect := range []string{"one", "-two", "+TWO", "@@ -1,2 +1,2 @@"} {
		gomega.Expect(strings.Contains(got, expect)).To(gomega.BeTrue(), fmt.Sprintf("renderColoredDiff dropped %q:\n%s", expect, got))
	}
})

var _ = ginkgo.It("render colored diff empty shows placeholder", func() {
	got := renderColoredDiff("")
	gomega.Expect(strings.Contains(got, "no diff")).To(gomega.BeTrue(), fmt.Sprintf("empty diff should surface a placeholder, got %q", got))
})

// TestRenderPlanDiffOverlayIncludesHeader verifies the overlay body
// starts with tool/path and includes the description so the user has
// enough context to evaluate the diff without scrolling back to the
// plan list.
var _ = ginkgo.It("render plan diff overlay includes header", func() {
	entry := &planDiffEntry{
		Tool:        "edit_file",
		Path:        "pkg/foo.go",
		Description: "tidy imports",
		Summary:     "Edited pkg/foo.go",
		Diff:        "--- a/pkg/foo.go\n+++ b/pkg/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
	}
	got := renderPlanDiffOverlay(entry)

	for _, want := range []string{"edit_file", "pkg/foo.go", "tidy imports", "-old", "+new"} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("overlay missing %q:\n%s", want, got))
	}
})

var _ = ginkgo.It("render plan diff overlay nil safe", func() {
	got := renderPlanDiffOverlay(nil)
	gomega.Expect(strings.TrimSpace(got)).NotTo(gomega.Equal(""), fmt.Sprintf("nil entry should render a placeholder, got empty"))
})

// TestOpenPlanDiffOverlayLoadsSelectedItem walks the integration: seed
// a plan file and a matching diff record, set the cursor on the plan
// item, then verify the overlay switches into view with the diff body.
// This is the headless equivalent of the user pressing Enter.
var _ = ginkgo.It("open plan diff overlay loads selected item", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.txt", "x\n")

	m := snapshotModel(t, root)
	session := newDetachedPromptExecutionSession(context.Background(), m)

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.txt", OldText: "x\n", NewText: "y\n", Description: "bump"}
	executeAndRecordPlanWrite(t, session, 1, req)

	rows := m.currentPlanSelectableRows()
	selected := -1
	diffID := ""
	for i, row := range rows {
		if row.Kind == planSelectableItem && row.Item.DiffID != "" {
			selected = i
			diffID = row.Item.DiffID
			break
		}
	}
	gomega.Expect(selected < 0).To(gomega.BeFalse(), fmt.Sprintf("plan item with diff id should be selectable: %#v", rows))

	m.planUI.cursor = selected
	m.activateSelectedPlanRow()

	gomega.Expect(m.planUI.overlayID).To(gomega.Equal(diffID), fmt.Sprintf("overlay id mismatch: %q vs %q", m.planUI.overlayID, diffID))

	gomega.Expect(m.planUI.overlayBody).To(gomega.ContainSubstring("-x"), fmt.Sprintf("overlay body missing diff:\n%s", m.planUI.overlayBody))
	gomega.Expect(m.planUI.overlayBody).To(gomega.ContainSubstring("+y"), fmt.Sprintf("overlay body missing diff:\n%s", m.planUI.overlayBody))
})

// TestOpenPlanDiffOverlayFalseForUnattachedLedgerItem guards against
// clicking a `- [ ] ... tid:...` line that has no diff yet — the
// overlay should refuse to open rather than displaying an empty body
// that would look broken.
var _ = ginkgo.It("open plan diff overlay false for unattached ledger item", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)
	session := newDetachedPromptExecutionSession(context.Background(), m)
	session.recordTaskLedger(
		workspace.ToolRequest{
			Tool: "task_ledger",
			Todos: []workspace.TaskLedgerItem{
				{ID: "t1", Content: "pending work", Status: "pending", Paths: []string{"x.go"}},
			},
		},
		workspace.ToolResult{Summary: "ok"},
	)

	rows := m.currentPlanSelectableRows()
	selected := -1
	for i, row := range rows {
		if row.Kind == planSelectableItem && row.Item.DiffID == "" {
			selected = i
			break
		}
	}
	gomega.Expect(selected < 0).To(gomega.BeFalse(), fmt.Sprintf("unattached ledger item should be selectable: %#v", rows))

	m.planUI.cursor = selected
	m.activateSelectedPlanRow()

	gomega.Expect(m.planUI.overlayID).To(gomega.Equal(""), fmt.Sprintf("overlay id should remain empty, got %q", m.planUI.overlayID))
	gomega.Expect(m.status).To(gomega.Equal("No diff attached to this item."), fmt.Sprintf("expected unattached item status, got %q", m.status))
})

var _ = ginkgo.It("close plan diff overlay clears state", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)

	m.planUI.overlayID = "abc"
	m.planUI.overlayBody = "body"
	m.closePlanDiffOverlay()

	gomega.Expect(m.planUI.overlayID).To(gomega.Equal(""), fmt.Sprintf("close did not clear state: id=%q body=%q", m.planUI.overlayID, m.planUI.overlayBody))
	gomega.Expect(m.planUI.overlayBody).To(gomega.Equal(""), fmt.Sprintf("close did not clear state: id=%q body=%q", m.planUI.overlayID, m.planUI.overlayBody))
})

// TestLoadPlanDiffEntryNoSessionID protects the very-first-turn race.
// With no debug log set, there is no diffs path to read and the
// function must return an error rather than panic.
var _ = ginkgo.It("load plan diff entry no session id", func() {
	m := &Model{}
	{
		_, err := m.loadPlanDiffEntry("x")
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected error when no session id is resolvable"))
	}
})

// TestLoadPlanDiffEntrySurfacesIOErrors verifies that a truncated /
// unreadable diffs file produces a meaningful error rather than a
// silent miss. This matters because diagnosing an overlay that "never
// opens" without this signal would require log-diving.
var _ = ginkgo.It("load plan diff entry surfaces missing file", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)
	// No diffs file created.
	_, err := m.loadPlanDiffEntry("anything")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected error when diffs file is missing"))

	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		// Either wrapped or raw os.ErrNotExist is fine; we just
		// want something diagnostic.
		fmt.Fprintf(ginkgo.GinkgoWriter, "got err %v (acceptable)", err)
	}

	_ = filepath.Join
})
