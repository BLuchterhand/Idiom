package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/workspace"
)

// modelForFailureLog returns a Model wired with a workspace rooted at
// root and a debug log path that yields the fixed session id "sessA".
// This sidesteps the full New()/initDebugLogger path so the test stays
// focused on the failure-log behavior.
func modelForFailureLog(t *testing.T, root string) *Model {
	t.Helper()
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	return &Model{
		workspace:    ws,
		debugLogPath: filepath.Join(root, "logs", "conversations", "sessA.jsonl"),
	}
}

func readFailureLines(t *testing.T, path string) []failureRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open failures log: %v", err)
	}
	defer f.Close()
	var out []failureRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec failureRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode failure record %q: %v", line, err)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan failures log: %v", err)
	}
	return out
}

func TestRecordWriteFailureAppendsForWriteTools(t *testing.T) {
	root := t.TempDir()
	m := modelForFailureLog(t, root)

	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "README.md",
		Description: "change greeting",
		Content:     "bye",
	}
	res := workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "edit failed",
		Error:   "line range out of bounds",
	}
	m.recordWriteFailure(7, req, res)

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	recs := readFailureLines(t, path)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d: %#v", len(recs), recs)
	}
	got := recs[0]
	if got.Tool != "edit_file" {
		t.Errorf("Tool = %q", got.Tool)
	}
	if got.Path != "README.md" {
		t.Errorf("Path = %q", got.Path)
	}
	if got.Description != "change greeting" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Error != "line range out of bounds" {
		t.Errorf("Error = %q", got.Error)
	}
	if got.Step != 7 {
		t.Errorf("Step = %d", got.Step)
	}
	if got.Timestamp == "" {
		t.Errorf("Timestamp should be populated")
	}
	// Args should be the full ToolRequest serialization — useful for
	// reproducing the failed call from the log alone.
	if !strings.Contains(got.Args, `"tool":"edit_file"`) {
		t.Errorf("Args should round-trip the request, got: %q", got.Args)
	}
	if !strings.Contains(got.Args, `"description":"change greeting"`) {
		t.Errorf("Args should include description, got: %q", got.Args)
	}
}

func TestRecordWriteFailureIgnoresSuccess(t *testing.T) {
	root := t.TempDir()
	m := modelForFailureLog(t, root)
	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file", Path: "x"}, workspace.ToolResult{Tool: "edit_file", Summary: "ok"})

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful write should not create failures log; stat err = %v", err)
	}
}

func TestRecordWriteFailureIgnoresReadTools(t *testing.T) {
	root := t.TempDir()
	m := modelForFailureLog(t, root)
	m.recordWriteFailure(1,
		workspace.ToolRequest{Tool: "read_file", Path: "x"},
		workspace.ToolResult{Tool: "read_file", Error: "file not found"},
	)
	m.recordWriteFailure(2,
		workspace.ToolRequest{Tool: "search_content", Pattern: "foo"},
		workspace.ToolResult{Tool: "search_content", Error: "no hits"},
	)
	m.recordWriteFailure(3,
		workspace.ToolRequest{Tool: "shell", Command: "false"},
		workspace.ToolResult{Tool: "shell", Error: "exit 1"},
	)

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("non-write-tool failures should not populate the log; stat err = %v", err)
	}
}

func TestRecordWriteFailureAppendsMultipleRecords(t *testing.T) {
	root := t.TempDir()
	m := modelForFailureLog(t, root)
	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file", Path: "a"}, workspace.ToolResult{Tool: "edit_file", Error: "first"})
	m.recordWriteFailure(2, workspace.ToolRequest{Tool: "write_file", Path: "b"}, workspace.ToolResult{Tool: "write_file", Error: "second"})
	m.recordWriteFailure(3, workspace.ToolRequest{Tool: "apply_patch"}, workspace.ToolResult{Tool: "apply_patch", Error: "third"})

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	recs := readFailureLines(t, path)
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	if recs[0].Error != "first" || recs[1].Error != "second" || recs[2].Error != "third" {
		t.Fatalf("records out of order: %#v", recs)
	}
}

// TestRecordWriteFailureSafeWithoutSessionID guards against the very
// first-turn race where a failure comes in before initDebugLogger has
// assigned debugLogPath. The call must not panic, must not create a
// stray file at the workspace root, and must silently drop.
func TestRecordWriteFailureSafeWithoutSessionID(t *testing.T) {
	root := t.TempDir()
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	m := &Model{workspace: ws}
	// Must not panic.
	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file", Path: "x"}, workspace.ToolResult{Tool: "edit_file", Error: "boom"})
	// .planning dir should not exist since no session id meant no path.
	if _, err := os.Stat(filepath.Join(root, ".planning")); !os.IsNotExist(err) {
		t.Fatalf("no session id should not create .planning; stat err = %v", err)
	}
}

func TestRecordWriteFailureNilModelNoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil model must not panic; recovered: %v", r)
		}
	}()
	var m *Model
	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file"}, workspace.ToolResult{Error: "boom"})
}

// TestRecordWriteFailureTruncatesLargeArgs guards against megabyte-sized
// write_file Content blowing up the failures log. The record must cap
// Args at maxFailureArgsLen and append a clear truncation marker so the
// reader knows the payload was elided — not silently dropped.
func TestRecordWriteFailureTruncatesLargeArgs(t *testing.T) {
	root := t.TempDir()
	m := modelForFailureLog(t, root)

	huge := strings.Repeat("x", maxFailureArgsLen*2)
	req := workspace.ToolRequest{Tool: "write_file", Path: "big.txt", Description: "why", Content: huge}
	res := workspace.ToolResult{Tool: "write_file", Error: "disk full"}
	m.recordWriteFailure(1, req, res)

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	recs := readFailureLines(t, path)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	if len(recs[0].Args) > maxFailureArgsLen+len("…[truncated]") {
		t.Errorf("Args not truncated, len = %d", len(recs[0].Args))
	}
	if !strings.HasSuffix(recs[0].Args, "…[truncated]") {
		t.Errorf("truncated Args should end with marker, got suffix: %q", recs[0].Args[max(0, len(recs[0].Args)-32):])
	}
}

func TestTruncateForLogShortUnchanged(t *testing.T) {
	if got := truncateForLog("hello", 100); got != "hello" {
		t.Errorf("short string should pass through unchanged, got %q", got)
	}
}

func TestTruncateForLogExactBoundary(t *testing.T) {
	s := strings.Repeat("a", 8)
	if got := truncateForLog(s, 8); got != s {
		t.Errorf("string at exact cap should pass through unchanged, got %q", got)
	}
}

// TestRecordWriteFailureIncludesGuardSyntheticResult covers the flow
// where the description guard itself produces the failure. The guard
// doesn't hit Workspace.Execute, but the failure still needs to be
// captured in the log for the "failures with useful info" promise.
func TestRecordWriteFailureIncludesGuardSyntheticResult(t *testing.T) {
	root := t.TempDir()
	m := modelForFailureLog(t, root)

	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go"}
	// Description missing — simulate what the runner would pass to
	// recordWriteFailure when the guard fires.
	guarded := workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "Missing write description",
		Error:   "edit_file requires a \"description\" field …",
	}
	m.recordWriteFailure(2, req, guarded)

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	recs := readFailureLines(t, path)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	if !strings.Contains(recs[0].Summary, "Missing write description") {
		t.Errorf("guard summary not preserved: %q", recs[0].Summary)
	}
}
