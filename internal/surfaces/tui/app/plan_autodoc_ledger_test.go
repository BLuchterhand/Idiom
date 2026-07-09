package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/taskledger"
)

// TestRecordTaskLedgerAppendsPlannedItems covers the happy-path triage
// flow: a task_ledger submission lands a "### Planned" section with one
// unchecked line per item, each carrying its tid anchor and a parsed
// paths note so a human reader can see the scope at a glance.
func TestRecordTaskLedgerAppendsPlannedItems(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "rename Qwen to Idiom in config", Status: "pending", Paths: []string{"config.go", "cmd/idiom/main.go"}},
			{ID: "t2", Content: "update docs", Status: "pending", Paths: []string{"README.md"}},
		},
	}
	m.recordTaskLedger(req, workspace.ToolResult{Tool: "task_ledger", Summary: "updated ledger"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}

	got := string(plan)
	if !strings.Contains(got, "### Planned") {
		t.Errorf("missing Planned heading:\n%s", got)
	}

	if !strings.Contains(got, "- [ ] rename Qwen to Idiom in config") {
		t.Errorf("missing t1 line:\n%s", got)
	}

	if !strings.Contains(got, "(paths: config.go, cmd/idiom/main.go)") {
		t.Errorf("missing paths note on t1:\n%s", got)
	}

	if !strings.Contains(got, "<!-- tid:t1 -->") {
		t.Errorf("missing tid anchor on t1:\n%s", got)
	}

	if !strings.Contains(got, "<!-- tid:t2 -->") {
		t.Errorf("missing tid anchor on t2:\n%s", got)
	}
}

// TestRecordTaskLedgerOnlyAppendsNewItems locks in the dedupe rule:
// re-submitting the same ledger (with status changes but the same ids)
// does not append duplicate "- [ ]" lines. The in-memory rendered-id
// set is the contract; without it the plan would grow unboundedly on
// each task_ledger update.
func TestRecordTaskLedgerOnlyAppendsNewItems(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
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
	if n := strings.Count(got, "### Planned"); n != 1 {
		t.Errorf("want 1 Planned heading across two submissions, got %d:\n%s", n, got)
	}

	// t1 appears once; t2 appears once.
	if n := strings.Count(got, "<!-- tid:t1 -->"); n != 1 {
		t.Errorf("t1 duplicated, want 1, got %d", n)
	}

	if n := strings.Count(got, "<!-- tid:t2 -->"); n != 1 {
		t.Errorf("t2 missing or duplicated, want 1, got %d", n)
	}
}

// TestRecordWriteSuccessAttachesToPlannedItem is the core P3 contract:
// a write to a path listed in a planned item checks that item's
// checkbox in place and annotates it with the diff id — no "Unplanned"
// entry created.
func TestRecordWriteSuccessAttachesToPlannedItem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "pkg a\n")

	m := snapshotModel(t, root)

	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "bump a.go", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", Description: "bump"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "a.go", "pkg a; changed\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Tool: "edit_file", Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	if strings.Contains(got, "### Unplanned") {
		t.Errorf("planned attach should not fall back to Unplanned:\n%s", got)
	}

	// The tid line should now be checked off.
	if !strings.Contains(got, "- [x] bump a.go") {
		t.Errorf("planned item not checked off in place:\n%s", got)
	}

	// And it should carry the diff-id anchor so the UI can resolve a click.
	if !strings.Contains(got, "<!-- tid:t1 -->") || !strings.Contains(got, "<!-- id:") {
		t.Errorf("checked line missing anchors:\n%s", got)
	}

	// There must be exactly one checkbox line for t1 — never duplicated.
	if n := strings.Count(got, "<!-- tid:t1 -->"); n != 1 {
		t.Errorf("want 1 tid line for t1, got %d", n)
	}
}

// TestRecordWriteSuccessUnplannedFallback verifies that a write to a
// path not mentioned by any ledger item lands under "### Unplanned".
// The Unplanned bucket is how the UI signals that the triage missed
// this file, so emitting it correctly is load-bearing for P3's intent.
func TestRecordWriteSuccessUnplannedFallback(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "pkg a\n")
	writeFile(t, root, "b.go", "pkg b\n")

	m := snapshotModel(t, root)

	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "edit a", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	// Write to b.go — not in any planned paths.
	req := workspace.ToolRequest{Tool: "edit_file", Path: "b.go", Description: "drive-by"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "b.go", "pkg b; drive-by\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	if !strings.Contains(got, "### Unplanned") {
		t.Errorf("expected Unplanned section for drive-by write:\n%s", got)
	}

	if !strings.Contains(got, "- [x] drive-by (b.go)") {
		t.Errorf("Unplanned line missing:\n%s", got)
	}

	// Planned item t1 must remain unchecked (we did not write a.go).
	if !strings.Contains(got, "- [ ] edit a") {
		t.Errorf("unrelated planned item should still be unchecked:\n%s", got)
	}
}

// TestAttachCompletesItemSoSecondWriteDoesNotDoubleCheck guards the
// ledger state machine: once an item is attached/completed, a second
// write to the same path should fall back to "Unplanned" rather than
// silently re-checking the already-done item.
func TestAttachCompletesItemSoSecondWriteDoesNotDoubleCheck(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "pkg a\n")

	m := snapshotModel(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "touch a", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", Description: "first touch"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "a.go", "v1\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	req2 := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", Description: "second touch"}
	m.capturePlanSnapshot(2, req2)
	writeFile(t, root, "a.go", "v2\n")
	m.recordWriteSuccess(2, req2, workspace.ToolResult{Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	if !strings.Contains(got, "### Unplanned") {
		t.Errorf("second touch should land under Unplanned:\n%s", got)
	}

	if !strings.Contains(got, "second touch (a.go)") {
		t.Errorf("second-touch label missing:\n%s", got)
	}

	if n := strings.Count(got, "<!-- tid:t1 -->"); n != 1 {
		t.Errorf("planned item should still be a single line, got %d:\n%s", n, got)
	}
}

// TestMultiPathItemStaysUncheckedUntilAllPathsHit verifies that a
// ledger item listing multiple paths only flips to [x] after every
// listed path has been written. A write to the first path must attach
// without falling to Unplanned and without prematurely marking the
// item completed. The second write lands on the same tid line,
// appending a second diff-id anchor and finally checking the box.
func TestMultiPathItemStaysUncheckedUntilAllPathsHit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "pkg a\n")
	writeFile(t, root, "b.go", "pkg b\n")

	m := snapshotModel(t, root)

	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "rename across files", Status: "pending", Paths: []string{"a.go", "b.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	// First write: a.go. Item must attach but stay unchecked.
	req1 := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", Description: "rename a"}
	m.capturePlanSnapshot(1, req1)
	writeFile(t, root, "a.go", "pkg a; renamed\n")
	m.recordWriteSuccess(1, req1, workspace.ToolResult{Summary: "ok"})

	planPath := filepath.Join(root, ".planning", "sessA.md")
	got, _ := os.ReadFile(planPath)

	if strings.Contains(string(got), "### Unplanned") {
		t.Fatalf("first write should attach, not fall to Unplanned:\n%s", got)
	}

	if !strings.Contains(string(got), "- [ ] rename across files") {
		t.Errorf("multi-path item should still be unchecked after first write:\n%s", got)
	}

	if n := strings.Count(string(got), "<!-- id:"); n != 1 {
		t.Errorf("want 1 diff-id anchor after first write, got %d:\n%s", n, got)
	}

	// Second write: b.go. Now every listed path is hit — flip to [x].
	req2 := workspace.ToolRequest{Tool: "edit_file", Path: "b.go", Description: "rename b"}
	m.capturePlanSnapshot(2, req2)
	writeFile(t, root, "b.go", "pkg b; renamed\n")
	m.recordWriteSuccess(2, req2, workspace.ToolResult{Summary: "ok"})

	got, _ = os.ReadFile(planPath)
	if strings.Contains(string(got), "### Unplanned") {
		t.Fatalf("second write of multi-path item should not fall to Unplanned:\n%s", got)
	}

	if !strings.Contains(string(got), "- [x] rename across files") {
		t.Errorf("multi-path item should flip to [x] after all paths hit:\n%s", got)
	}

	if n := strings.Count(string(got), "<!-- id:"); n != 2 {
		t.Errorf("want 2 diff-id anchors after both writes, got %d:\n%s", n, got)
	}

	// Still only one tid line — the two writes share the same plan row.
	if n := strings.Count(string(got), "<!-- tid:t1 -->"); n != 1 {
		t.Errorf("tid line should remain single, got %d:\n%s", n, got)
	}
}

// TestPlannedItemLineStatus covers the completed/cancelled rendering
// path — if a resumed session carries over a ledger with already-done
// items, their boxes must render checked on first appearance.
func TestPlannedItemLineStatus(t *testing.T) {
	cases := map[string]string{
		"pending":     "- [ ] ",
		"in_progress": "- [~] ",
		"completed":   "- [x] ",
		"cancelled":   "- [x] ",
	}
	for status, prefix := range cases {
		line := plannedItemLine(workspace.TaskLedgerItem{ID: "x", Content: "c", Status: taskledger.Status(status)})
		if !strings.HasPrefix(line, prefix) {
			t.Errorf("status %s: got %q, want prefix %q", status, line, prefix)
		}
	}
}

// TestRecordTaskLedgerIgnoresFailures verifies that a task_ledger call
// that errored (e.g. the model sent malformed todos) does not pollute
// the plan file with a phantom Planned section.
func TestRecordTaskLedgerIgnoresFailures(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.recordTaskLedger(
		workspace.ToolRequest{Tool: "task_ledger", Todos: []workspace.TaskLedgerItem{{ID: "t1", Content: "x", Status: "pending"}}},
		workspace.ToolResult{Tool: "task_ledger", Error: "validation failed"},
	)

	if _, err := os.Stat(filepath.Join(root, ".planning")); !os.IsNotExist(err) {
		t.Fatalf("errored ledger should not create .planning; err = %v", err)
	}
}

// TestRecordTaskLedgerSkipsNonLedgerTools ensures only task_ledger
// events trigger the ledger path.
func TestRecordTaskLedgerSkipsNonLedgerTools(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.recordTaskLedger(workspace.ToolRequest{Tool: "edit_file"}, workspace.ToolResult{Summary: "ok"})

	if _, err := os.Stat(filepath.Join(root, ".planning")); !os.IsNotExist(err) {
		t.Fatalf("non-ledger event should not create .planning; err = %v", err)
	}
}

// TestAttachByExplicitTaskIDWhenPathsMissing exercises the primary
// fix for the "planned item fell to Unplanned after resume" bug: the
// model passes task_id on the write call, and the planned item gets
// checked off in place even though the ledger entry carried no
// `paths` and the write's path doesn't match anything listed.
func TestAttachByExplicitTaskIDWhenPathsMissing(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
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
		Description: "write the garbage content",
		TaskID:      "garbage-test-1",
	}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "garbage.txt", "asdfghjkl;'\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	if strings.Contains(got, "### Unplanned") {
		t.Errorf("explicit task_id should prevent Unplanned fallback:\n%s", got)
	}

	if !strings.Contains(got, "- [x] asdfghjkl;'") {
		t.Errorf("planned item should be checked off in place:\n%s", got)
	}

	if n := strings.Count(got, "<!-- tid:garbage-test-1 -->"); n != 1 {
		t.Errorf("want 1 tid line, got %d:\n%s", n, got)
	}
}

// TestAttachByExplicitTaskIDWinsOverPathMismatch guards the priority
// order: when task_id is set, we attach to that item even if the
// write's path happens to coincide with a different pending item's
// paths list. The model's declared intent beats the path heuristic.
func TestAttachByExplicitTaskIDWinsOverPathMismatch(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t-primary", Content: "the real work", Status: "pending"},
			{ID: "t-coincident", Content: "unrelated edit of x.go", Status: "pending", Paths: []string{"x.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	writeFile(t, root, "x.go", "pkg x\n")

	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "x.go",
		Description: "primary work happens to touch x.go",
		TaskID:      "t-primary",
	}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "x.go", "pkg x; done\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	if !strings.Contains(got, "- [x] the real work") {
		t.Errorf("t-primary should be checked via task_id:\n%s", got)
	}

	if !strings.Contains(got, "- [ ] unrelated edit of x.go") {
		t.Errorf("t-coincident should remain unchecked — task_id beats path match:\n%s", got)
	}
}

// TestHeuristicAttachSinglePathsLessItem covers the backstop: the
// model omitted task_id AND forgot to list paths, but there's exactly
// one pending paths-less item. We take that as "the only reasonable
// candidate" and attach to it rather than silently creating Unplanned.
func TestHeuristicAttachSinglePathsLessItem(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "lonely", Content: "do the thing", Status: "pending"},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "write_file", Path: "out.txt", Description: "the thing"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "out.txt", "done\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	if strings.Contains(got, "### Unplanned") {
		t.Errorf("heuristic should attach rather than fall to Unplanned:\n%s", got)
	}

	if !strings.Contains(got, "- [x] do the thing") {
		t.Errorf("paths-less item should be checked via heuristic:\n%s", got)
	}
}

// TestHeuristicDoesNotFireWhenAmbiguous guards against false positives:
// if there are multiple paths-less pending items, the heuristic
// refuses to guess and we fall through to Unplanned. Silently picking
// "the first one" would mis-attribute work, which is worse than the
// Unplanned signal.
func TestHeuristicDoesNotFireWhenAmbiguous(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "task a", Status: "pending"},
			{ID: "b", Content: "task b", Status: "pending"},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "write_file", Path: "out.txt", Description: "which?"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "out.txt", "done\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	if !strings.Contains(got, "### Unplanned") {
		t.Errorf("ambiguous paths-less items should fall to Unplanned:\n%s", got)
	}

	if !strings.Contains(got, "- [ ] task a") || !strings.Contains(got, "- [ ] task b") {
		t.Errorf("both planned items should remain unchecked:\n%s", got)
	}
}

// TestRecordTaskLedgerAcceptsItemsField covers the alternate field
// name: some call sites use `Items` instead of `Todos` on ToolRequest.
// Both should work — dropping one would silently lose coverage.
func TestRecordTaskLedgerAcceptsItemsField(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	req := workspace.ToolRequest{
		Tool:  "task_ledger",
		Items: []workspace.TaskLedgerItem{{ID: "t1", Content: "c", Status: "pending"}},
	}
	m.recordTaskLedger(req, workspace.ToolResult{Summary: "ok"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}

	if !strings.Contains(string(plan), "<!-- tid:t1 -->") {
		t.Errorf("Items field should be honored: %s", plan)
	}
}
