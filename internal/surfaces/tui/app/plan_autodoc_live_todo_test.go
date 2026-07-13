package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	planartifact "idiom/internal/contracts/planartifact"

	"idiom/internal/capabilities/workspace"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("record submitted plan appends checklist", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", Purpose: "add the new handler", OldText: "old", NewText: "new"},
			{Path: "b.go", Purpose: "wire the route", OldText: "old2", NewText: "new2"},
		},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan: %v", err))

	got := string(plan)
	gomega.Expect(strings.Contains(got, "### Plan")).To(gomega.BeTrue(), fmt.Sprintf("missing Plan heading:\n%s", got))

	gomega.Expect(strings.Contains(got, "- [ ] add the new handler (a.go)")).To(gomega.BeTrue(), fmt.Sprintf("missing first edit line:\n%s", got))

	gomega.Expect(strings.Contains(got, "- [ ] wire the route (b.go)")).To(gomega.BeTrue(), fmt.Sprintf("missing second edit line:\n%s", got))

	gomega.Expect(strings.Contains(got, "<!-- pid:pe-0 -->")).To(gomega.BeTrue(), fmt.Sprintf("missing pid anchor for pe-0:\n%s", got))

	gomega.Expect(strings.Contains(got, "<!-- pid:pe-1 -->")).To(gomega.BeTrue(), fmt.Sprintf("missing pid anchor for pe-1:\n%s", got))

	gomega.Expect(strings.Contains(got, "anchored replacement")).To(gomega.BeTrue(), fmt.Sprintf("missing basic edit details:\n%s", got))

	gomega.Expect(got).To(gomega.ContainSubstring("### Approval"), fmt.Sprintf("missing approval controls:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("<!-- plan-approval:current -->"), fmt.Sprintf("missing approval controls:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("<!-- plan-approval:all -->"), fmt.Sprintf("missing approval controls:\n%s", got))

	gomega.Expect(m.plan.submittedEdits).NotTo(gomega.BeNil(), fmt.Sprint("planSubmittedEdits should be initialized"))

	gomega.Expect(len(m.plan.submittedEdits)).To(gomega.Equal(2), fmt.Sprintf("want 2 submitted edits, got %d", len(m.plan.submittedEdits)))
})

var _ = ginkgo.It("record submitted plan replaces existing section", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	first := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []planartifact.PlanEdit{{Path: "old.go", Purpose: "old edit", OldText: "a", NewText: "b"}},
	}
	m.recordSubmittedPlan(first, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	second := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "new.go", Purpose: "new edit 1", OldText: "c", NewText: "d"},
			{Path: "new2.go", Purpose: "new edit 2", OldText: "e", NewText: "f"},
		},
	}
	m.recordSubmittedPlan(second, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "old.go")).To(gomega.BeFalse(), fmt.Sprintf("old edit should have been replaced:\n%s", got))

	gomega.Expect(got).To(gomega.And(
		gomega.ContainSubstring("new edit 1"),
		gomega.ContainSubstring("new edit 2"),
	), fmt.Sprintf("missing new edits after replacement:\n%s", got))
})

var _ = ginkgo.It("record submitted plan skips errors", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []planartifact.PlanEdit{{Path: "a.go", Purpose: "x", OldText: "a", NewText: "b"}},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Error: "rejected"})

	_, err := os.Stat(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "plan should not exist after error")
})

var _ = ginkgo.It("record submitted plan skips suppressed review noop plan", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []planartifact.PlanEdit{{Purpose: "verification pass"}},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{
		Tool:    "review_repair_noop_plan",
		Summary: "review-repair submit_plan ignored: no new corrective plan",
	})

	_, err := os.Stat(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "suppressed review noop plan should not create plan UI")
})

var _ = ginkgo.It("record submitted plan uses plan artifact", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &planartifact.PlanArtifact{
			Edits: []planartifact.PlanEdit{
				{Path: "x.go", Purpose: "from artifact", OldText: "o", NewText: "n"},
			},
		},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(strings.Contains(got, "from artifact")).To(gomega.BeTrue(), fmt.Sprintf("artifact edits should populate plan:\n%s", got))
})

var _ = ginkgo.It("record submitted plan skips non submit plan", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go"}
	m.recordSubmittedPlan(req, workspace.ToolResult{Summary: "ok"})

	{
		_, err := os.Stat(filepath.Join(root, ".planning"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprint("non-submit_plan should not create planning dir"))
	}
})

var _ = ginkgo.It("attach write to submitted edit", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", Purpose: "first edit", OldText: "a", NewText: "b"},
			{Path: "b.go", Purpose: "second edit", OldText: "c", NewText: "d"},
		},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	ok := m.attachWriteToSubmittedEdit("a.go", "diff-abc123")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("should match a.go"))

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)

	gomega.Expect(got).To(gomega.ContainSubstring("- [x] first edit (a.go)"), fmt.Sprintf("first edit should be checked off with diff anchor:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("<!-- pid:pe-0 --> <!-- id:diff-abc123 -->"), fmt.Sprintf("first edit should be checked off with diff anchor:\n%s", got))

	gomega.Expect(got).To(gomega.ContainSubstring("- [ ] second edit (b.go)"), fmt.Sprintf("second edit should remain unchecked:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("<!-- pid:pe-1 -->"), fmt.Sprintf("second edit should remain unchecked:\n%s", got))
})

var _ = ginkgo.It("attach write to submitted edit no match", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []planartifact.PlanEdit{{Path: "a.go", Purpose: "x", OldText: "a", NewText: "b"}},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	gomega.Expect(m.attachWriteToSubmittedEdit("z.go", "diff-xyz")).To(gomega.BeFalse(), fmt.Sprint("should not match z.go"))
})

var _ = ginkgo.It("update plan item checkbox in progress", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotSession(t, root)

	req := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "do the thing", Status: "pending", Paths: []string{"a.go"}},
		},
	}
	m.recordTaskLedger(req, workspace.ToolResult{Tool: "task_ledger", Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(strings.Contains(string(plan), "- [ ] do the thing")).To(gomega.BeTrue(), fmt.Sprintf("initial item should be unchecked:\n%s", string(plan)))

	update := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "do the thing", Status: "in_progress", Paths: []string{"a.go"}},
		},
	}
	m.recordTaskLedger(update, workspace.ToolResult{Tool: "task_ledger", Summary: "ok"})

	plan, _ = os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))

	got := string(plan)

	gomega.Expect(strings.Contains(got, "- [~] do the thing")).To(gomega.BeTrue(), fmt.Sprintf("item should show in-progress [~]:\n%s", got))
})

var _ = ginkgo.It("plan edit label fallbacks", func() {
	cases := []struct {
		edit planartifact.PlanEdit
		want string
	}{
		{planartifact.PlanEdit{Path: "a.go", Purpose: "fix the bug"}, "fix the bug (a.go)"},
		{planartifact.PlanEdit{Purpose: "no path given"}, "no path given"},
		{planartifact.PlanEdit{Path: "a.go"}, "a.go"},
	}
	for _, tc := range cases {
		got := planEditLabel(tc.edit)
		gomega.Expect(got).To(gomega.Equal(tc.want), fmt.Sprintf("planEditLabel(%+v) = %q, want %q", tc.edit, got, tc.want))
	}
})

var _ = ginkgo.It("cut plan section", func() {
	content := "## Session X\n\n### Plan\n- [ ] a\n- [ ] b\n\n### Other\nstuff\n"
	before, found := cutPlanSection(content, "### Plan")

	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprint("should find section"))

	gomega.Expect(strings.Contains(before, "### Plan")).To(gomega.BeFalse(), fmt.Sprintf("before should not contain ### Plan:\n%s", before))

	gomega.Expect(strings.Contains(before, "## Session X")).To(gomega.BeTrue(), fmt.Sprintf("before should keep heading:\n%s", before))

	gomega.Expect(strings.Contains(before, "### Other")).To(gomega.BeTrue(), fmt.Sprintf("before should keep ### Other:\n%s", before))
})

var _ = ginkgo.It("cut plan section tolerates crlf and trailing heading space", func() {
	content := "## Session X\r\n\r\n### Plan  \r\n- [ ] a\r\n\r\n### Approval  \r\n- [ ] old\r\n\r\n### Other\r\nstuff\r\n"
	withoutPlan, found := cutPlanSection(content, "### Plan")
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprint("should find Plan section"))

	withoutApproval, found := cutPlanSection(withoutPlan, "### Approval")
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprint("should find Approval section"))

	gomega.Expect(withoutApproval).To(gomega.Not(gomega.ContainSubstring("### Plan")), fmt.Sprintf("old generated sections were not cut:\n%s", withoutApproval))
	gomega.Expect(withoutApproval).To(gomega.Not(gomega.ContainSubstring("### Approval")), fmt.Sprintf("old generated sections were not cut:\n%s", withoutApproval))
	gomega.Expect(withoutApproval).To(gomega.Not(gomega.ContainSubstring("old")), fmt.Sprintf("old generated sections were not cut:\n%s", withoutApproval))

	gomega.Expect(strings.Contains(withoutApproval, "### Other")).To(gomega.BeTrue(), fmt.Sprintf("following section was not preserved:\n%s", withoutApproval))
})

var _ = ginkgo.It("cut plan section no heading", func() {
	content := "## Session X\n\n### Planned\n- [ ] a\n"
	before, found := cutPlanSection(content, "### Plan")

	gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprint("should not find section"))

	gomega.Expect(before).To(gomega.Equal(content), fmt.Sprintf("should return content unchanged:\n%s", before))
})

var _ = ginkgo.It("plan checkbox counts", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)
	writeFile(t, root, ".planning/sessA.md", "## Session\n### Planned\n- [ ] pending\n- [~] active\n- [x] done\n- [x] also done\n")

	checked, total := m.planCheckboxCounts()
	gomega.Expect(total).To(gomega.Equal(4), fmt.Sprintf("want total=4, got %d", total))

	gomega.Expect(checked).To(gomega.Equal(2), fmt.Sprintf("want checked=2, got %d", checked))
})

var _ = ginkgo.It("plan panel auto show needed", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)

	gomega.Expect(m.planPanelAutoShowNeeded()).To(gomega.BeFalse(), fmt.Sprint("should not auto-show when no plan file exists"))

	writeFile(t, root, ".planning/sessA.md", "## Session\n")

	m.planContent = ""
	m.planMTime = time.Time{}

	gomega.Expect(m.planPanelAutoShowNeeded()).To(gomega.BeFalse(), fmt.Sprint("should not auto-show with empty content"))

	writeFile(t, root, ".planning/sessA.md", "## Session\n- [ ] a task\n")

	m.planContent = ""
	m.planMTime = time.Time{}

	gomega.Expect(m.planPanelAutoShowNeeded()).To(gomega.BeTrue(), fmt.Sprint("should auto-show with checkbox items"))
})

var _ = ginkgo.It("plan panel auto show needed plan execute", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)

	m.forcedTaskFamily = "plan_then_execute_planner"

	writeFile(t, root, ".planning/sessA.md", "## Session\n### Plan\n- [ ] do it\n")

	m.planContent = ""
	m.planMTime = time.Time{}

	gomega.Expect(m.planPanelAutoShowNeeded()).To(gomega.BeTrue(), fmt.Sprint("plan-execute should always auto-show when plan exists"))

	m.forcedTaskFamily = ""
	gomega.Expect(m.planPanelAutoShowNeeded()).To(gomega.BeTrue(), fmt.Sprint("non-plan-execute should still auto-show with checkbox items"))
})

var _ = ginkgo.It("tool intent", func() {
	cases := []struct {
		tool string
		path string
		want string
	}{
		{"edit_file", "x.go", "apply the planned change in x.go"},
		{"edit_file", "", "apply the planned code change"},
		{"write_file", "new.go", "create or replace new.go"},
		{"append_file", "log.txt", "append content to log.txt"},
		{"apply_patch", "a.go", "apply a patch to a.go"},
		{"mkdir", "dir/sub", "create directory dir/sub"},
		{"unknown_tool", "f.go", ""},
	}
	for _, tc := range cases {
		got := toolIntent(tc.tool, tc.path)
		gomega.Expect(got).To(gomega.Equal(tc.want), fmt.Sprintf("toolIntent(%q, %q) = %q, want %q", tc.tool, tc.path, got, tc.want))
	}
})
