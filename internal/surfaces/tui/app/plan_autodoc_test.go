package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

// sessionForFailureLog returns a prompt session wired with a workspace rooted at
// root and a debug log path that yields the fixed session id "sessA".
// This sidesteps the full New()/initDebugLogger path so the test stays
// focused on the failure-log behavior.
func sessionForFailureLog(t ginkgo.FullGinkgoTInterface, root string) *promptExecutionSession {
	ginkgo.GinkgoHelper()

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})

	return newDetachedPromptExecutionSession(context.Background(), &Model{
		workspace:    ws,
		debugLogPath: filepath.Join(root, "logs", "conversations", "sessA.jsonl"),
	})
}

func readFailureLines(t ginkgo.FullGinkgoTInterface, path string) []failureRecord {
	ginkgo.GinkgoHelper()

	f, err := os.Open(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open failures log: %v", err))
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

		{
			err := json.Unmarshal([]byte(line), &rec)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode failure record %q: %v", line, err))
		}

		out = append(out, rec)
	}

	{
		err := scanner.Err()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("scan failures log: %v", err))
	}

	return out
}

var _ = ginkgo.It("record write failure appends for write tools", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := sessionForFailureLog(t, root)

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

	gomega.Expect(len(recs)).To(gomega.Equal(1), fmt.Sprintf("want 1 record, got %d: %#v", len(recs), recs))

	got := recs[0]
	gomega.Expect(got.Tool).To(gomega.Equal("edit_file"), fmt.Sprintf("Tool = %q", got.Tool))

	gomega.Expect(got.Path).To(gomega.Equal("README.md"), fmt.Sprintf("Path = %q", got.Path))

	gomega.Expect(got.Description).To(gomega.Equal("change greeting"), fmt.Sprintf("Description = %q", got.Description))

	gomega.Expect(got.Error).To(gomega.Equal("line range out of bounds"), fmt.Sprintf("Error = %q", got.Error))

	gomega.Expect(got.Step).To(gomega.Equal(7), fmt.Sprintf("Step = %d", got.Step))

	gomega.Expect(got.Timestamp).NotTo(gomega.Equal(""), fmt.Sprintf("Timestamp should be populated"))

	// Args should be the full ToolRequest serialization — useful for
	// reproducing the failed call from the log alone.
	gomega.Expect(strings.Contains(got.Args, `"tool":"edit_file"`)).To(gomega.BeTrue(), fmt.Sprintf("Args should round-trip the request, got: %q", got.Args))

	gomega.Expect(strings.Contains(got.Args, `"description":"change greeting"`)).To(gomega.BeTrue(), fmt.Sprintf("Args should include description, got: %q", got.Args))
})

var _ = ginkgo.It("record write failure ignores success", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := sessionForFailureLog(t, root)
	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file", Path: "x"}, workspace.ToolResult{Tool: "edit_file", Summary: "ok"})

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	{
		_, err := os.Stat(path)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("successful write should not create failures log; stat err = %v", err))
	}
})

var _ = ginkgo.It("record write failure ignores read tools", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := sessionForFailureLog(t, root)
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
	{
		_, err := os.Stat(path)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("non-write-tool failures should not populate the log; stat err = %v", err))
	}
})

var _ = ginkgo.It("record write failure appends multiple records", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := sessionForFailureLog(t, root)
	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file", Path: "a"}, workspace.ToolResult{Tool: "edit_file", Error: "first"})
	m.recordWriteFailure(2, workspace.ToolRequest{Tool: "write_file", Path: "b"}, workspace.ToolResult{Tool: "write_file", Error: "second"})
	m.recordWriteFailure(3, workspace.ToolRequest{Tool: "apply_patch"}, workspace.ToolResult{Tool: "apply_patch", Error: "third"})

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	recs := readFailureLines(t, path)

	gomega.Expect(len(recs)).To(gomega.Equal(3), fmt.Sprintf("want 3 records, got %d", len(recs)))

	gomega.Expect(recs[0].Error).To(gomega.Equal("first"), fmt.Sprintf("records out of order: %#v", recs))
	gomega.Expect(recs[1].Error).To(gomega.Equal("second"), fmt.Sprintf("records out of order: %#v", recs))
	gomega.Expect(recs[2].Error).To(gomega.Equal("third"), fmt.Sprintf("records out of order: %#v", recs))
})

// TestRecordWriteFailureSafeWithoutSessionID guards against the very
// first-turn race where a failure comes in before initDebugLogger has
// assigned debugLogPath. The call must not panic, must not create a
// stray file at the workspace root, and must silently drop.
var _ = ginkgo.It("record write failure safe without session id", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	m := newDetachedPromptExecutionSession(context.Background(), &Model{workspace: ws})
	// Must not panic.
	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file", Path: "x"}, workspace.ToolResult{Tool: "edit_file", Error: "boom"})

	// .planning dir should not exist since no session id meant no path.
	{
		_, err := os.Stat(filepath.Join(root, ".planning"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("no session id should not create .planning; stat err = %v", err))
	}
})

var _ = ginkgo.It("record write failure nil model noop", func() {
	defer func() {
		{
			r := recover()
			gomega.Expect(r).To(gomega.BeNil(), fmt.Sprintf("nil model must not panic; recovered: %v", r))
		}
	}()

	var m *promptExecutionSession

	m.recordWriteFailure(1, workspace.ToolRequest{Tool: "edit_file"}, workspace.ToolResult{Error: "boom"})
})

// TestRecordWriteFailureTruncatesLargeArgs guards against megabyte-sized
// write_file Content blowing up the failures log. The record must cap
// Args at maxFailureArgsLen and append a clear truncation marker so the
// reader knows the payload was elided — not silently dropped.
var _ = ginkgo.It("record write failure truncates large args", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := sessionForFailureLog(t, root)

	huge := strings.Repeat("x", maxFailureArgsLen*2)
	req := workspace.ToolRequest{Tool: "write_file", Path: "big.txt", Description: "why", Content: huge}
	res := workspace.ToolResult{Tool: "write_file", Error: "disk full"}
	m.recordWriteFailure(1, req, res)

	path := filepath.Join(root, ".planning", "sessA.failures.jsonl")
	recs := readFailureLines(t, path)

	gomega.Expect(len(recs)).To(gomega.Equal(1), fmt.Sprintf("want 1 record, got %d", len(recs)))

	gomega.Expect(len(recs[0].Args) > maxFailureArgsLen+len("…[truncated]")).To(gomega.BeFalse(), fmt.Sprintf("Args not truncated, len = %d", len(recs[0].Args)))

	gomega.Expect(strings.HasSuffix(recs[0].Args, "…[truncated]")).To(gomega.BeTrue(), fmt.Sprintf("truncated Args should end with marker, got suffix: %q", recs[0].Args[max(0, len(recs[0].Args)-32):]))
})

var _ = ginkgo.It("truncate for log short unchanged", func() {
	{
		got := truncateForLog("hello", 100)
		gomega.Expect(got).To(gomega.Equal("hello"), fmt.Sprintf("short string should pass through unchanged, got %q", got))
	}
})

var _ = ginkgo.It("truncate for log exact boundary", func() {
	s := strings.Repeat("a", 8)
	{
		got := truncateForLog(s, 8)
		gomega.Expect(got).To(gomega.Equal(s), fmt.Sprintf("string at exact cap should pass through unchanged, got %q", got))
	}
})

// TestRecordWriteFailureIncludesGuardSyntheticResult covers the flow
// where the description guard itself produces the failure. The guard
// doesn't hit Workspace.Execute, but the failure still needs to be
// captured in the log for the "failures with useful info" promise.
var _ = ginkgo.It("record write failure includes guard synthetic result", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := sessionForFailureLog(t, root)

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

	gomega.Expect(len(recs)).To(gomega.Equal(1), fmt.Sprintf("want 1 record, got %d", len(recs)))

	gomega.Expect(strings.Contains(recs[0].Summary, "Missing write description")).To(gomega.BeTrue(), fmt.Sprintf("guard summary not preserved: %q", recs[0].Summary))
})
