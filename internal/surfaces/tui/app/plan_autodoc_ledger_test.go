package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/taskledger"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

// TestRecordTaskLedgerAppendsPlannedItems covers the happy-path triage
// flow: a task_ledger submission lands a "### Planned" section with one
// unchecked line per item, each carrying its tid anchor and a parsed
// paths note so a human reader can see the scope at a glance.
var _ = ginkgo.It("record task ledger appends planned items", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "rename Qwen to Idiom in config", Status: "pending", Paths: []string{"config.go", "cmd/idiom/main.go"}},
			{ID: "t2", Content: "update docs", Status: "pending", Paths: []string{"README.md"}},
		},
	}
	m.recordTaskLedger(req, workspace.ToolResult{Tool: "task_ledger", Summary: "updated ledger"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan: %v", err))

	got := string(plan)
	gomega.Expect(strings.Contains(got, "### Planned")).To(gomega.BeTrue(), fmt.Sprintf("missing Planned heading:\n%s", got))

	gomega.Expect(strings.Contains(got, "- [ ] rename Qwen to Idiom in config")).To(gomega.BeTrue(), fmt.Sprintf("missing t1 line:\n%s", got))

	gomega.Expect(strings.Contains(got, "(paths: config.go, cmd/idiom/main.go)")).To(gomega.BeTrue(), fmt.Sprintf("missing paths note on t1:\n%s", got))

	gomega.Expect(strings.Contains(got, "<!-- tid:t1 -->")).To(gomega.BeTrue(), fmt.Sprintf("missing tid anchor on t1:\n%s", got))

	gomega.Expect(strings.Contains(got, "<!-- tid:t2 -->")).To(gomega.BeTrue(), fmt.Sprintf("missing tid anchor on t2:\n%s", got))
})

// TestRecordTaskLedgerOnlyAppendsNewItems locks in the dedupe rule:
// re-submitting the same ledger (with status changes but the same ids)
// does not append duplicate "- [ ]" lines. The in-memory rendered-id
// set is the contract; without it the plan would grow unboundedly on
// each task_ledger update.
var _ = ginkgo.It("record task ledger only appends new items", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	first := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "do x", Status: "pending", Paths: []string{"a.go"}},
		},
	}
	m.recordTaskLedger(first, workspace.ToolResult{Summary: "ok"})

	second := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "do x", Status: "in_progress", Paths: []string{"a.go"}},
			{ID: "t2", Content: "do y", Status: "pending", Paths: []string{"b.go"}},
		},
	}
	m.recordTaskLedger(second, workspace.ToolResult{Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)
	// Only one `### Planned` heading even across two submissions.
	{
		n := strings.Count(got, "### Planned")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("want 1 Planned heading across two submissions, got %d:\n%s", n, got))
	}

	// t1 appears once; t2 appears once.
	{
		n := strings.Count(got, "<!-- tid:t1 -->")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("t1 duplicated, want 1, got %d", n))
	}

	{
		n := strings.Count(got, "<!-- tid:t2 -->")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("t2 missing or duplicated, want 1, got %d", n))
	}
})

// TestRecordWriteSuccessAttachesToPlannedItem is the core P3 contract:
// a write to a path listed in a planned item checks that item's
// checkbox in place and annotates it with the diff id — no "Unplanned"
// entry created.
var _ = ginkgo.It("record write success attaches to planned item", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")

	m := snapshotSession(t, root)

	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "bump a.go", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", OldText: "package a\n", NewText: "package a\n// changed\n", Description: "bump"}
	executeAndRecordPlanWrite(t, m, 1, req)

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "### Unplanned")).To(gomega.BeFalse(), fmt.Sprintf("planned attach should not fall back to Unplanned:\n%s", got))

	// The tid line should now be checked off.
	gomega.Expect(strings.Contains(got, "- [x] bump a.go")).To(gomega.BeTrue(), fmt.Sprintf("planned item not checked off in place:\n%s", got))

	// And it should carry the diff-id anchor so the UI can resolve a click.
	gomega.Expect(got).To(gomega.ContainSubstring("<!-- tid:t1 -->"), fmt.Sprintf("checked line missing anchors:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("<!-- id:"), fmt.Sprintf("checked line missing anchors:\n%s", got))

	// There must be exactly one checkbox line for t1 — never duplicated.
	{
		n := strings.Count(got, "<!-- tid:t1 -->")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("want 1 tid line for t1, got %d", n))
	}
})

// TestRecordWriteSuccessUnplannedFallback verifies that a write to a
// path not mentioned by any ledger item lands under "### Unplanned".
// The Unplanned bucket is how the UI signals that the triage missed
// this file, so emitting it correctly is load-bearing for P3's intent.
var _ = ginkgo.It("record write success unplanned fallback", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")
	writeFile(t, root, "b.go", "package b\n")

	m := snapshotSession(t, root)

	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "edit a", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	// Write to b.go — not in any planned paths.
	req := workspace.ToolRequest{Tool: "edit_file", Path: "b.go", OldText: "package b\n", NewText: "package b\n// drive-by\n", Description: "drive-by"}
	executeAndRecordPlanWrite(t, m, 1, req)

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "### Unplanned")).To(gomega.BeTrue(), fmt.Sprintf("expected Unplanned section for drive-by write:\n%s", got))

	gomega.Expect(strings.Contains(got, "- [x] drive-by (b.go)")).To(gomega.BeTrue(), fmt.Sprintf("Unplanned line missing:\n%s", got))

	// Planned item t1 must remain unchecked (we did not write a.go).
	gomega.Expect(strings.Contains(got, "- [ ] edit a")).To(gomega.BeTrue(), fmt.Sprintf("unrelated planned item should still be unchecked:\n%s", got))
})

// TestAttachCompletesItemSoSecondWriteDoesNotDoubleCheck guards the
// ledger state machine: once an item is attached/completed, a second
// write to the same path should fall back to "Unplanned" rather than
// silently re-checking the already-done item.
var _ = ginkgo.It("attach completes item so second write does not double check", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")

	m := snapshotSession(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "touch a", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "write_file", Path: "a.go", Content: "package a\n// v1\n", Description: "first touch"}
	executeAndRecordPlanWrite(t, m, 1, req)

	req2 := workspace.ToolRequest{Tool: "write_file", Path: "a.go", Content: "package a\n// v2\n", Description: "second touch"}
	executeAndRecordPlanWrite(t, m, 2, req2)

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "### Unplanned")).To(gomega.BeTrue(), fmt.Sprintf("second touch should land under Unplanned:\n%s", got))

	gomega.Expect(strings.Contains(got, "second touch (a.go)")).To(gomega.BeTrue(), fmt.Sprintf("second-touch label missing:\n%s", got))

	{
		n := strings.Count(got, "<!-- tid:t1 -->")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("planned item should still be a single line, got %d:\n%s", n, got))
	}
})

// TestMultiPathItemStaysUncheckedUntilAllPathsHit verifies that a
// ledger item listing multiple paths only flips to [x] after every
// listed path has been written. A write to the first path must attach
// without falling to Unplanned and without prematurely marking the
// item completed. The second write lands on the same tid line,
// appending a second diff-id anchor and finally checking the box.
var _ = ginkgo.It("multi path item stays unchecked until all paths hit", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")
	writeFile(t, root, "b.go", "package b\n")

	m := snapshotSession(t, root)

	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "rename across files", Status: "pending", Paths: []string{"a.go", "b.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	// First write: a.go. Item must attach but stay unchecked.
	req1 := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", OldText: "package a\n", NewText: "package a\n// renamed\n", Description: "rename a"}
	executeAndRecordPlanWrite(t, m, 1, req1)

	planPath := filepath.Join(root, ".planning", "sessA.md")
	got, _ := os.ReadFile(planPath)

	gomega.Expect(strings.Contains(string(got), "### Unplanned")).To(gomega.BeFalse(), fmt.Sprintf("first write should attach, not fall to Unplanned:\n%s", got))

	gomega.Expect(strings.Contains(string(got), "- [ ] rename across files")).To(gomega.BeTrue(), fmt.Sprintf("multi-path item should still be unchecked after first write:\n%s", got))

	{
		n := strings.Count(string(got), "<!-- id:")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("want 1 diff-id anchor after first write, got %d:\n%s", n, got))
	}

	// Second write: b.go. Now every listed path is hit — flip to [x].
	req2 := workspace.ToolRequest{Tool: "edit_file", Path: "b.go", OldText: "package b\n", NewText: "package b\n// renamed\n", Description: "rename b"}
	executeAndRecordPlanWrite(t, m, 2, req2)

	got, _ = os.ReadFile(planPath)
	gomega.Expect(strings.Contains(string(got), "### Unplanned")).To(gomega.BeFalse(), fmt.Sprintf("second write of multi-path item should not fall to Unplanned:\n%s", got))

	gomega.Expect(strings.Contains(string(got), "- [x] rename across files")).To(gomega.BeTrue(), fmt.Sprintf("multi-path item should flip to [x] after all paths hit:\n%s", got))

	{
		n := strings.Count(string(got), "<!-- id:")
		gomega.Expect(n).To(gomega.Equal(2), fmt.Sprintf("want 2 diff-id anchors after both writes, got %d:\n%s", n, got))
	}

	// Still only one tid line — the two writes share the same plan row.
	{
		n := strings.Count(string(got), "<!-- tid:t1 -->")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("tid line should remain single, got %d:\n%s", n, got))
	}
})

// TestPlannedItemLineStatus covers the completed/cancelled rendering
// path — if a resumed session carries over a ledger with already-done
// items, their boxes must render checked on first appearance.
var _ = ginkgo.It("planned item line status", func() {
	cases := map[string]string{
		"pending":     "- [ ] ",
		"in_progress": "- [~] ",
		"completed":   "- [x] ",
		"cancelled":   "- [x] ",
	}
	for status, prefix := range cases {
		line := plannedItemLine(workspace.TaskLedgerItem{ID: "x", Content: "c", Status: taskledger.Status(status)})
		gomega.Expect(strings.HasPrefix(line, prefix)).To(gomega.BeTrue(), fmt.Sprintf("status %s: got %q, want prefix %q", status, line, prefix))
	}
})

// TestRecordTaskLedgerIgnoresFailures verifies that a task_ledger call
// that errored (e.g. the model sent malformed todos) does not pollute
// the plan file with a phantom Planned section.
var _ = ginkgo.It("record task ledger ignores failures", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	m.recordTaskLedger(
		workspace.ToolRequest{Tool: "task_ledger", Todos: []workspace.TaskLedgerItem{{ID: "t1", Content: "x", Status: "pending"}}},
		workspace.ToolResult{Tool: "task_ledger", Error: "validation failed"},
	)

	{
		_, err := os.Stat(filepath.Join(root, ".planning"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("errored ledger should not create .planning; err = %v", err))
	}
})

// TestRecordTaskLedgerSkipsNonLedgerTools ensures only task_ledger
// events trigger the ledger path.
var _ = ginkgo.It("record task ledger skips non ledger tools", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	m.recordTaskLedger(workspace.ToolRequest{Tool: "edit_file"}, workspace.ToolResult{Summary: "ok"})

	{
		_, err := os.Stat(filepath.Join(root, ".planning"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("non-ledger event should not create .planning; err = %v", err))
	}
})

// TestAttachByExplicitTaskIDWhenPathsMissing exercises the primary
// fix for the "planned item fell to Unplanned after resume" bug: the
// model passes task_id on the write call, and the planned item gets
// checked off in place even though the ledger entry carried no
// `paths` and the write's path doesn't match anything listed.
var _ = ginkgo.It("attach by explicit task id when paths missing", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			// Paths intentionally empty — this is the failure mode
			// the fix targets.
			{ID: "garbage-test-1", Content: "asdfghjkl;'", Status: "pending"},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{
		Tool:        "write_file",
		Path:        "garbage.txt",
		Content:     "asdfghjkl;'\n",
		Description: "write the garbage content",
		TaskID:      "garbage-test-1",
	}
	executeAndRecordPlanWrite(t, m, 1, req)

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "### Unplanned")).To(gomega.BeFalse(), fmt.Sprintf("explicit task_id should prevent Unplanned fallback:\n%s", got))

	gomega.Expect(strings.Contains(got, "- [x] asdfghjkl;'")).To(gomega.BeTrue(), fmt.Sprintf("planned item should be checked off in place:\n%s", got))

	{
		n := strings.Count(got, "<!-- tid:garbage-test-1 -->")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("want 1 tid line, got %d:\n%s", n, got))
	}
})

// TestAttachByExplicitTaskIDWinsOverPathMismatch guards the priority
// order: when task_id is set, we attach to that item even if the
// write's path happens to coincide with a different pending item's
// paths list. The model's declared intent beats the path heuristic.
var _ = ginkgo.It("attach by explicit task id wins over path mismatch", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t-primary", Content: "the real work", Status: "pending"},
			{ID: "t-coincident", Content: "unrelated edit of x.go", Status: "pending", Paths: []string{"x.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	writeFile(t, root, "x.go", "package x\n")

	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "x.go",
		OldText:     "package x\n",
		NewText:     "package x\n// done\n",
		Description: "primary work happens to touch x.go",
		TaskID:      "t-primary",
	}
	executeAndRecordPlanWrite(t, m, 1, req)

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "- [x] the real work")).To(gomega.BeTrue(), fmt.Sprintf("t-primary should be checked via task_id:\n%s", got))

	gomega.Expect(strings.Contains(got, "- [ ] unrelated edit of x.go")).To(gomega.BeTrue(), fmt.Sprintf("t-coincident should remain unchecked — task_id beats path match:\n%s", got))
})

// TestHeuristicAttachSinglePathsLessItem covers the backstop: the
// model omitted task_id AND forgot to list paths, but there's exactly
// one pending paths-less item. We take that as "the only reasonable
// candidate" and attach to it rather than silently creating Unplanned.
var _ = ginkgo.It("heuristic attach single paths less item", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "lonely", Content: "do the thing", Status: "pending"},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "write_file", Path: "out.txt", Content: "done\n", Description: "the thing"}
	executeAndRecordPlanWrite(t, m, 1, req)

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "### Unplanned")).To(gomega.BeFalse(), fmt.Sprintf("heuristic should attach rather than fall to Unplanned:\n%s", got))

	gomega.Expect(strings.Contains(got, "- [x] do the thing")).To(gomega.BeTrue(), fmt.Sprintf("paths-less item should be checked via heuristic:\n%s", got))
})

// TestHeuristicDoesNotFireWhenAmbiguous guards against false positives:
// if there are multiple paths-less pending items, the heuristic
// refuses to guess and we fall through to Unplanned. Silently picking
// "the first one" would mis-attribute work, which is worse than the
// Unplanned signal.
var _ = ginkgo.It("heuristic does not fire when ambiguous", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "task a", Status: "pending"},
			{ID: "b", Content: "task b", Status: "pending"},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "write_file", Path: "out.txt", Content: "done\n", Description: "which?"}
	executeAndRecordPlanWrite(t, m, 1, req)

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "### Unplanned")).To(gomega.BeTrue(), fmt.Sprintf("ambiguous paths-less items should fall to Unplanned:\n%s", got))

	gomega.Expect(got).To(gomega.ContainSubstring("- [ ] task a"), fmt.Sprintf("both planned items should remain unchecked:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("- [ ] task b"), fmt.Sprintf("both planned items should remain unchecked:\n%s", got))
})

// TestRecordTaskLedgerAcceptsItemsField covers the alternate field
// name: some call sites use `Items` instead of `Todos` on ToolRequest.
// Both should work — dropping one would silently lose coverage.
var _ = ginkgo.It("record task ledger accepts items field", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	req := workspace.ToolRequest{
		Tool:  "task_ledger",
		Items: []workspace.TaskLedgerItem{{ID: "t1", Content: "c", Status: "pending"}},
	}
	m.recordTaskLedger(req, workspace.ToolResult{Summary: "ok"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan: %v", err))

	gomega.Expect(strings.Contains(string(plan), "<!-- tid:t1 -->")).To(gomega.BeTrue(), fmt.Sprintf("Items field should be honored: %s", plan))
})
