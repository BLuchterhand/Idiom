package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

// snapshotModel builds live state wired to a real workspace rooted at root.
// Session-owned plan tests snapshot it through snapshotSession, while UI tests
// retain the live Model owner.
func snapshotModel(t ginkgo.FullGinkgoTInterface, root string) *Model {
	ginkgo.GinkgoHelper()

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

	return &Model{
		workspace:    ws,
		debugLogPath: filepath.Join(root, "logs", "conversations", "sessA.jsonl"),
	}
}

// snapshotSession gives plan bookkeeping tests the concrete execution owner.
func snapshotSession(t ginkgo.FullGinkgoTInterface, root string) *promptExecutionSession {
	ginkgo.GinkgoHelper()

	return newDetachedPromptExecutionSession(context.Background(), snapshotModel(t, root))
}

func readDiffEntries(t ginkgo.FullGinkgoTInterface, path string) []planDiffEntry {
	ginkgo.GinkgoHelper()

	f, err := os.Open(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open diffs log: %v", err))
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

		{
			err := json.Unmarshal([]byte(line), &rec)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode diff record %q: %v", line, err))
		}

		out = append(out, rec)
	}

	{
		err := scanner.Err()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("scan diffs log: %v", err))
	}

	return out
}

func writeFile(t ginkgo.FullGinkgoTInterface, root, rel, content string) {
	ginkgo.GinkgoHelper()

	abs := filepath.Join(root, rel)
	{
		err := os.MkdirAll(filepath.Dir(abs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir: %v", err))
	}

	{
		err := os.WriteFile(abs, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", rel, err))
	}
}

func executeAndRecordPlanWrite(t ginkgo.FullGinkgoTInterface, session *promptExecutionSession, step int, request workspace.ToolRequest) workspace.ToolResult {
	ginkgo.GinkgoHelper()
	if request.Tool == workspace.ToolNameEditFile {
		observed := session.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: request.Path})
		gomega.Expect(observed.Error).To(gomega.BeEmpty(), observed.Error)
	}
	result := session.workspace.Execute(session.promptExecutionContext, request)
	gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
	session.recordWriteSuccess(step, request, result)
	return result
}

// TestRecordWriteSuccessCapturesDiffAndPlanEntry exercises the full
// rooted execute → record pipeline end-to-end: owner-produced pre/post evidence
// and a post-write recordWriteSuccess
// that lands (a) a plan-diff JSONL record with a unified diff, and
// (b) a checked-off plan markdown line pointing at it by id.
var _ = ginkgo.It("record write success captures diff and plan entry", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "README.md", "hello\n")

	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "README.md",
		OldText:     "hello\n",
		NewText:     "bye\n",
		Description: "change greeting",
	}
	executeAndRecordPlanWrite(t, m, 5, req)

	diffs := readDiffEntries(t, filepath.Join(root, ".planning", "sessA.diffs.jsonl"))
	gomega.Expect(len(diffs)).To(gomega.Equal(1), fmt.Sprintf("want 1 diff record, got %d", len(diffs)))

	entry := diffs[0]
	gomega.Expect(entry.Tool).To(gomega.Equal("edit_file"), fmt.Sprintf("unexpected entry metadata: %#v", entry))
	gomega.Expect(entry.Path).To(gomega.Equal("README.md"), fmt.Sprintf("unexpected entry metadata: %#v", entry))

	gomega.Expect(entry.Description).To(gomega.Equal("change greeting"), fmt.Sprintf("description not propagated: %q", entry.Description))

	gomega.Expect(entry.Diff).To(gomega.ContainSubstring("-hello"), fmt.Sprintf("diff missing expected lines:\n%s", entry.Diff))
	gomega.Expect(entry.Diff).To(gomega.ContainSubstring("+bye"), fmt.Sprintf("diff missing expected lines:\n%s", entry.Diff))

	gomega.Expect(entry.ID).NotTo(gomega.Equal(""), fmt.Sprint("diff entry missing id"))

	planBytes, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan: %v", err))

	plan := string(planBytes)
	gomega.Expect(strings.Contains(plan, "## Session sessA")).To(gomega.BeTrue(), fmt.Sprintf("plan missing session heading:\n%s", plan))

	gomega.Expect(strings.Contains(plan, "- [x] change greeting")).To(gomega.BeTrue(), fmt.Sprintf("plan missing checked-off description line:\n%s", plan))

	gomega.Expect(strings.Contains(plan, "<!-- id:"+entry.ID+" -->")).To(gomega.BeTrue(), fmt.Sprintf("plan line missing id anchor for %q:\n%s", entry.ID, plan))
})

// TestRecordWriteSuccessIgnoresFailures verifies no plan line / diff
// entry is written when the tool returned an error. Failed writes
// belong in the failures log, not the success-only plan document.
var _ = ginkgo.It("record write success ignores failures", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "x.go", "package x\n")

	m := snapshotSession(t, root)

	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Description: "nope"}
	m.recordWriteSuccess(1, req, workspace.ToolResult{Tool: "edit_file", Error: "boom"})

	diffs := filepath.Join(root, ".planning", "sessA.diffs.jsonl")
	{
		_, err := os.Stat(diffs)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("failed write should not produce diff log; stat err = %v", err))
	}

	plan := filepath.Join(root, ".planning", "sessA.md")
	{
		_, err := os.Stat(plan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("failed write should not produce plan file; stat err = %v", err))
	}

})

// TestRecordWriteSuccessIgnoresNonWriteTools guards against read-tool
// results bleeding into the plan panel. A read_file event should never
// produce a plan line or diff record.
var _ = ginkgo.It("record write success ignores non write tools", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	req := workspace.ToolRequest{Tool: "read_file", Path: "x.go"}
	m.recordWriteSuccess(1, req, workspace.ToolResult{Tool: "read_file", Summary: "read x.go"})

	{
		_, err := os.Stat(filepath.Join(root, ".planning"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("read tool should not create .planning; stat err = %v", err))
	}
})

// TestRecordWriteSuccessWithNoSessionIDNoOps ensures the very-first-
// turn race (PreExecute fires before initDebugLogger) does not crash
// and does not write to a stray file at the workspace root.
var _ = ginkgo.It("record write success with no session id no ops", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "x.go", "package x\n")

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	m := newDetachedPromptExecutionSession(context.Background(), &Model{workspace: ws}) // no debugLogPath → empty session id

	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Description: "no id"}
	req.OldText = "package x\n"
	req.NewText = "package x\n// changed\n"
	observed := m.workspace.Execute(m.promptExecutionContext, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: req.Path})
	gomega.Expect(observed.Error).To(gomega.BeEmpty(), observed.Error)
	result := m.workspace.Execute(m.promptExecutionContext, req)
	gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
	m.recordWriteSuccess(1, req, result)

	{
		_, err := os.Stat(filepath.Join(root, ".planning"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("no session id should not create .planning; err = %v", err))
	}
})

// TestRecordWriteSuccessNilModelNoop verifies the nil-receiver guard —
// identical to the failure-log nil-safety test.
var _ = ginkgo.It("record write success nil model noop", func() {
	defer func() {
		{
			r := recover()
			gomega.Expect(r).To(gomega.BeNil(), fmt.Sprintf("nil model must not panic; recovered: %v", r))
		}
	}()

	var m *promptExecutionSession

	m.recordWriteSuccess(1, workspace.ToolRequest{Tool: "edit_file"}, workspace.ToolResult{Summary: "ok"})
})

// A new file uses the rooted transaction's missing preimage and renders a
// pure-addition diff without a speculative host read.
var _ = ginkgo.It("records a new file from workspace-owned commit evidence", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)
	req := workspace.ToolRequest{Tool: "write_file", Path: "new.md", Content: "new\n", Description: "create file"}
	result := executeAndRecordPlanWrite(t, m, 2, req)
	changes := result.CommitReceipt().FileChanges()
	gomega.Expect(changes).To(gomega.HaveLen(1))
	before, existed := changes[0].BeforeBody()
	gomega.Expect(existed).To(gomega.BeFalse())
	gomega.Expect(before).To(gomega.BeEmpty())
	diffs := readDiffEntries(t, filepath.Join(root, ".planning", "sessA.diffs.jsonl"))
	gomega.Expect(diffs).To(gomega.HaveLen(1))
	gomega.Expect(diffs[0].Diff).To(gomega.ContainSubstring("+new"))
})

// TestSessionHeadingWrittenOnce locks in the idempotence of the session
// heading. Two successful writes in the same session should add one
// heading, not two, regardless of in-memory state between calls.
var _ = ginkgo.It("session heading written once", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.md", "a\n")
	writeFile(t, root, "b.md", "b\n")

	m := snapshotSession(t, root)

	executeAndRecordPlanWrite(t, m, 1, workspace.ToolRequest{Tool: "write_file", Path: "a.md", Content: "A\n", Description: "edit a"})
	executeAndRecordPlanWrite(t, m, 2, workspace.ToolRequest{Tool: "write_file", Path: "b.md", Content: "B\n", Description: "edit b"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan: %v", err))

	count := strings.Count(string(plan), "## Session sessA")
	gomega.Expect(count).To(gomega.Equal(1), fmt.Sprintf("want 1 session heading, got %d. plan:\n%s", count, string(plan)))

	gomega.Expect(strings.Count(string(plan), "- [x]")).To(gomega.Equal(2), fmt.Sprintf("want 2 checked items, plan:\n%s", string(plan)))
})

// TestPlanLineLabelFallbacks covers the fallback ladder: description
// only, description+path, tool-only, tool+path. A blank label in the
// plan would be user-hostile so every branch produces readable text.
var _ = ginkgo.It("plan line label fallbacks", func() {
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
		{
			got := planLineLabel(tc.req, tc.path)
			gomega.Expect(got).To(gomega.Equal(tc.want), fmt.Sprintf("%s: got %q, want %q", tc.name, got, tc.want))
		}
	}
})

// TestRecordWriteSuccessMultiplePathsPerCall exercises the apply_patch
// path where a single tool call touches multiple files — each path
// should get its own snapshot, its own diff record, and its own plan
// line. This is the critical correctness check for the snapshot-slice
// data shape; collapsing to a single snapshot would silently drop
// the second file's diff.
var _ = ginkgo.It("record write success multiple paths per call", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.txt", "alpha\n")
	writeFile(t, root, "b.txt", "beta\n")

	m := snapshotSession(t, root)

	req := workspace.ToolRequest{Tool: "apply_patch", Description: "bulk rename", Content: "*** Begin Patch\n*** Update File: a.txt\n@@\n-alpha\n+ALPHA\n*** Update File: b.txt\n@@\n-beta\n+BETA\n*** End Patch"}
	executeAndRecordPlanWrite(t, m, 3, req)

	diffs := readDiffEntries(t, filepath.Join(root, ".planning", "sessA.diffs.jsonl"))
	gomega.Expect(len(diffs)).To(gomega.Equal(2), fmt.Sprintf("want 2 diff records, got %d: %#v", len(diffs), diffs))

	paths := map[string]bool{}
	for _, d := range diffs {
		paths[d.Path] = true
		gomega.Expect(d.ID).NotTo(gomega.Equal(""), fmt.Sprintf("diff entry %s missing id", d.Path))
	}

	gomega.Expect(paths["a.txt"]).To(gomega.BeTrue(), fmt.Sprintf("missing expected paths, got %v", paths))
	gomega.Expect(paths["b.txt"]).To(gomega.BeTrue(), fmt.Sprintf("missing expected paths, got %v", paths))

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	// Each file touched produces one plan line.
	{
		got := strings.Count(string(plan), "- [x]")
		gomega.Expect(got).To(gomega.Equal(2), fmt.Sprintf("want 2 plan lines, got %d. plan:\n%s", got, string(plan)))
	}
})

// TestNewPlanEntryIDDistinct is a cheap collision check — IDs must be
// unique across a session so clicks resolve to the right diff record.
var _ = ginkgo.It("new plan entry id distinct", func() {
	seen := map[string]bool{}
	hexIDRe := regexp.MustCompile(`^[0-9a-f]+$`)

	for range 64 {
		id := newPlanEntryID()
		gomega.Expect(seen[id]).To(gomega.BeFalse(), fmt.Sprintf("duplicate id %q after %d draws", id, len(seen)))

		seen[id] = true
		gomega.Expect(hexIDRe.MatchString(id)).To(gomega.BeTrue(), fmt.Sprintf("id %q not hex/safe", id))
	}
})
