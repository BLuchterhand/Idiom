package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/workspace"
)

// snapshotModel builds a Model wired to a real workspace rooted at root
// so capturePlanSnapshot can read pre-edit file state from disk. Uses
// a fixed session id ("sessA") via the debug-log path, same as the
// sibling failure-log tests.
func snapshotModel(t *testing.T, root string) *Model {
	t.Helper()
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	return &Model{
		workspace:    ws,
		debugLogPath: filepath.Join(root, "logs", "conversations", "sessA.jsonl"),
	}
}

func readDiffEntries(t *testing.T, path string) []planDiffEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open diffs log: %v", err)
	}
	defer f.Close()
	var out []planDiffEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec planDiffEntry
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode diff record %q: %v", line, err)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan diffs log: %v", err)
	}
	return out
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestRecordWriteSuccessCapturesDiffAndPlanEntry exercises the full
// snapshot → execute → record pipeline end-to-end: pre-write snapshot
// of a file, on-disk mutation, and a post-write recordWriteSuccess
// that lands (a) a plan-diff JSONL record with a unified diff, and
// (b) a checked-off plan markdown line pointing at it by id.
func TestRecordWriteSuccessCapturesDiffAndPlanEntry(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "hello\n")
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "README.md",
		Description: "change greeting",
	}
	m.capturePlanSnapshot(5, req)
	if _, ok := m.plan.writeSnapshots[5]; !ok {
		t.Fatalf("snapshot was not captured for step 5")
	}

	// Simulate the tool executing and mutating the file.
	writeFile(t, root, "README.md", "bye\n")

	res := workspace.ToolResult{Tool: "edit_file", Summary: "Edited README.md"}
	m.recordWriteSuccess(5, req, res)

	if _, ok := m.plan.writeSnapshots[5]; ok {
		t.Errorf("snapshot map should be cleared after record")
	}

	diffs := readDiffEntries(t, filepath.Join(root, ".planning", "sessA.diffs.jsonl"))
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff record, got %d", len(diffs))
	}
	entry := diffs[0]
	if entry.Tool != "edit_file" || entry.Path != "README.md" {
		t.Errorf("unexpected entry metadata: %#v", entry)
	}
	if entry.Description != "change greeting" {
		t.Errorf("description not propagated: %q", entry.Description)
	}
	if !strings.Contains(entry.Diff, "-hello") || !strings.Contains(entry.Diff, "+bye") {
		t.Errorf("diff missing expected lines:\n%s", entry.Diff)
	}
	if entry.ID == "" {
		t.Error("diff entry missing id")
	}

	planBytes, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	plan := string(planBytes)
	if !strings.Contains(plan, "## Session sessA") {
		t.Errorf("plan missing session heading:\n%s", plan)
	}
	if !strings.Contains(plan, "- [x] change greeting") {
		t.Errorf("plan missing checked-off description line:\n%s", plan)
	}
	if !strings.Contains(plan, "<!-- id:"+entry.ID+" -->") {
		t.Errorf("plan line missing id anchor for %q:\n%s", entry.ID, plan)
	}
}

// TestRecordWriteSuccessIgnoresFailures verifies no plan line / diff
// entry is written when the tool returned an error. Failed writes
// belong in the failures log, not the success-only plan document.
func TestRecordWriteSuccessIgnoresFailures(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "x.go", "pkg\n")
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Description: "nope"}
	m.capturePlanSnapshot(1, req)
	m.recordWriteSuccess(1, req, workspace.ToolResult{Tool: "edit_file", Error: "boom"})

	diffs := filepath.Join(root, ".planning", "sessA.diffs.jsonl")
	if _, err := os.Stat(diffs); !os.IsNotExist(err) {
		t.Fatalf("failed write should not produce diff log; stat err = %v", err)
	}
	plan := filepath.Join(root, ".planning", "sessA.md")
	if _, err := os.Stat(plan); !os.IsNotExist(err) {
		t.Fatalf("failed write should not produce plan file; stat err = %v", err)
	}
	if _, ok := m.plan.writeSnapshots[1]; ok {
		t.Errorf("snapshot still present after failed recordWriteSuccess — would leak into next step")
	}
}

// TestRecordWriteSuccessIgnoresNonWriteTools guards against read-tool
// results bleeding into the plan panel. A read_file event should never
// produce a plan line or diff record.
func TestRecordWriteSuccessIgnoresNonWriteTools(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	req := workspace.ToolRequest{Tool: "read_file", Path: "x.go"}
	m.recordWriteSuccess(1, req, workspace.ToolResult{Tool: "read_file", Summary: "read x.go"})

	if _, err := os.Stat(filepath.Join(root, ".planning")); !os.IsNotExist(err) {
		t.Fatalf("read tool should not create .planning; stat err = %v", err)
	}
}

// TestRecordWriteSuccessWithNoSessionIDNoOps ensures the very-first-
// turn race (PreExecute fires before initDebugLogger) does not crash
// and does not write to a stray file at the workspace root.
func TestRecordWriteSuccessWithNoSessionIDNoOps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "x.go", "pkg\n")
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	m := &Model{workspace: ws} // no debugLogPath → empty session id

	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Description: "no id"}
	m.capturePlanSnapshot(1, req) // should be a no-op, not panic
	writeFile(t, root, "x.go", "changed\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Tool: "edit_file", Summary: "ok"})

	if _, err := os.Stat(filepath.Join(root, ".planning")); !os.IsNotExist(err) {
		t.Fatalf("no session id should not create .planning; err = %v", err)
	}
}

// TestRecordWriteSuccessNilModelNoop verifies the nil-receiver guard —
// identical to the failure-log nil-safety test.
func TestRecordWriteSuccessNilModelNoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil model must not panic; recovered: %v", r)
		}
	}()
	var m *Model
	m.capturePlanSnapshot(1, workspace.ToolRequest{Tool: "edit_file", Path: "x"})
	m.recordWriteSuccess(1, workspace.ToolRequest{Tool: "edit_file"}, workspace.ToolResult{Summary: "ok"})
}

// TestCapturePlanSnapshotMissingFileRecordsEmptyBaseline verifies that
// write_file on a brand-new path records a snapshot with Existed=false
// so the diff renders as pure additions rather than getting skipped.
func TestCapturePlanSnapshotMissingFileRecordsEmptyBaseline(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	req := workspace.ToolRequest{Tool: "write_file", Path: "new.md", Description: "create file"}
	m.capturePlanSnapshot(2, req)
	snaps, ok := m.plan.writeSnapshots[2]
	if !ok {
		t.Fatalf("missing-path write should still record a snapshot")
	}
	if len(snaps) != 1 || snaps[0].Existed {
		t.Errorf("snapshot Existed should be false for a new file: %#v", snaps)
	}
	if snaps[0].Before != "" {
		t.Errorf("missing-file baseline should be empty, got %q", snaps[0].Before)
	}
}

// TestSessionHeadingWrittenOnce locks in the idempotence of the session
// heading. Two successful writes in the same session should add one
// heading, not two, regardless of in-memory state between calls.
func TestSessionHeadingWrittenOnce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "a\n")
	writeFile(t, root, "b.md", "b\n")
	m := snapshotModel(t, root)

	m.capturePlanSnapshot(1, workspace.ToolRequest{Tool: "edit_file", Path: "a.md", Description: "edit a"})
	writeFile(t, root, "a.md", "A\n")
	m.recordWriteSuccess(1, workspace.ToolRequest{Tool: "edit_file", Path: "a.md", Description: "edit a"}, workspace.ToolResult{Summary: "ok"})

	m.capturePlanSnapshot(2, workspace.ToolRequest{Tool: "edit_file", Path: "b.md", Description: "edit b"})
	writeFile(t, root, "b.md", "B\n")
	m.recordWriteSuccess(2, workspace.ToolRequest{Tool: "edit_file", Path: "b.md", Description: "edit b"}, workspace.ToolResult{Summary: "ok"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	count := strings.Count(string(plan), "## Session sessA")
	if count != 1 {
		t.Errorf("want 1 session heading, got %d. plan:\n%s", count, string(plan))
	}
	if strings.Count(string(plan), "- [x]") != 2 {
		t.Errorf("want 2 checked items, plan:\n%s", string(plan))
	}
}

// TestPlanLineLabelFallbacks covers the fallback ladder: description
// only, description+path, tool-only, tool+path. A blank label in the
// plan would be user-hostile so every branch produces readable text.
func TestPlanLineLabelFallbacks(t *testing.T) {
	cases := []struct {
		name string
		req  workspace.ToolRequest
		path string
		want string
	}{
		{"desc+path", workspace.ToolRequest{Tool: "edit_file", Description: "d"}, "x.go", "d (x.go)"},
		{"desc only", workspace.ToolRequest{Tool: "edit_file", Description: "d"}, "", "d"},
		{"path only", workspace.ToolRequest{Tool: "edit_file"}, "x.go", "apply the planned change in x.go"},
		{"tool only", workspace.ToolRequest{Tool: "mkdir"}, "", "create a directory"},
	}
	for _, tc := range cases {
		if got := planLineLabel(tc.req, tc.path); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRecordWriteSuccessMultiplePathsPerCall exercises the apply_patch
// path where a single tool call touches multiple files — each path
// should get its own snapshot, its own diff record, and its own plan
// line. This is the critical correctness check for the snapshot-slice
// data shape; collapsing to a single snapshot would silently drop
// the second file's diff.
func TestRecordWriteSuccessMultiplePathsPerCall(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "alpha\n")
	writeFile(t, root, "b.txt", "beta\n")
	m := snapshotModel(t, root)

	// Simulate capturePlanSnapshot producing two entries by bypassing
	// rollbackPathsForRequest — that helper only returns multi-path
	// results for apply_patch, but its parser needs a real patch and
	// we're testing the record flow, not the patch parser.
	m.plan.writeSnapshots = map[int][]planWriteSnapshot{
		3: {
			{Path: "a.txt", Before: "alpha\n", Existed: true},
			{Path: "b.txt", Before: "beta\n", Existed: true},
		},
	}
	writeFile(t, root, "a.txt", "ALPHA\n")
	writeFile(t, root, "b.txt", "BETA\n")

	req := workspace.ToolRequest{Tool: "apply_patch", Description: "bulk rename"}
	m.recordWriteSuccess(3, req, workspace.ToolResult{Tool: "apply_patch", Summary: "patched 2 files"})

	diffs := readDiffEntries(t, filepath.Join(root, ".planning", "sessA.diffs.jsonl"))
	if len(diffs) != 2 {
		t.Fatalf("want 2 diff records, got %d: %#v", len(diffs), diffs)
	}
	paths := map[string]bool{}
	for _, d := range diffs {
		paths[d.Path] = true
		if d.ID == "" {
			t.Errorf("diff entry %s missing id", d.Path)
		}
	}
	if !paths["a.txt"] || !paths["b.txt"] {
		t.Errorf("missing expected paths, got %v", paths)
	}

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	// Each file touched produces one plan line.
	if got := strings.Count(string(plan), "- [x]"); got != 2 {
		t.Errorf("want 2 plan lines, got %d. plan:\n%s", got, string(plan))
	}
}

// TestNewPlanEntryIDDistinct is a cheap collision check — IDs must be
// unique across a session so clicks resolve to the right diff record.
func TestNewPlanEntryIDDistinct(t *testing.T) {
	seen := map[string]bool{}
	hexIDRe := regexp.MustCompile(`^[0-9a-f]+$`)
	for range 64 {
		id := newPlanEntryID()
		if seen[id] {
			t.Fatalf("duplicate id %q after %d draws", id, len(seen))
		}
		seen[id] = true
		if !hexIDRe.MatchString(id) {
			t.Errorf("id %q not hex/safe", id)
		}
	}
}
