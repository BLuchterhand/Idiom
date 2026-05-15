package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/workspace"
)

func TestRecordSubmittedPlanAppendsChecklist(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.go", Purpose: "add the new handler", OldText: "old", NewText: "new"},
			{Path: "b.go", Purpose: "wire the route", OldText: "old2", NewText: "new2"},
		},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	plan, err := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	got := string(plan)
	if !strings.Contains(got, "### Plan") {
		t.Errorf("missing Plan heading:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] add the new handler (a.go)") {
		t.Errorf("missing first edit line:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] wire the route (b.go)") {
		t.Errorf("missing second edit line:\n%s", got)
	}
	if !strings.Contains(got, "<!-- pid:pe-0 -->") {
		t.Errorf("missing pid anchor for pe-0:\n%s", got)
	}
	if !strings.Contains(got, "<!-- pid:pe-1 -->") {
		t.Errorf("missing pid anchor for pe-1:\n%s", got)
	}
	if m.plan.submittedEdits == nil {
		t.Fatal("planSubmittedEdits should be initialized")
	}
	if len(m.plan.submittedEdits) != 2 {
		t.Fatalf("want 2 submitted edits, got %d", len(m.plan.submittedEdits))
	}
}

func TestRecordSubmittedPlanReplacesExistingSection(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	first := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{{Path: "old.go", Purpose: "old edit", OldText: "a", NewText: "b"}},
	}
	m.recordSubmittedPlan(first, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	second := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "new.go", Purpose: "new edit 1", OldText: "c", NewText: "d"},
			{Path: "new2.go", Purpose: "new edit 2", OldText: "e", NewText: "f"},
		},
	}
	m.recordSubmittedPlan(second, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)
	if strings.Contains(got, "old.go") {
		t.Errorf("old edit should have been replaced:\n%s", got)
	}
	if strings.Contains(got, "new edit 1") && strings.Contains(got, "new edit 2") {
		return
	}
	t.Errorf("missing new edits after replacement:\n%s", got)
}

func TestRecordSubmittedPlanSkipsErrors(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{{Path: "a.go", Purpose: "x", OldText: "a", NewText: "b"}},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Error: "rejected"})

	if _, err := os.Stat(filepath.Join(root, ".planning", "sessA.md")); !os.IsNotExist(err) {
		plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
		t.Errorf("plan should not exist after error, got:\n%s", string(plan))
	}
}

func TestRecordSubmittedPlanUsesPlanArtifact(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{
				{Path: "x.go", Purpose: "from artifact", OldText: "o", NewText: "n"},
			},
		},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)
	if !strings.Contains(got, "from artifact") {
		t.Errorf("artifact edits should populate plan:\n%s", got)
	}
}

func TestRecordSubmittedPlanSkipsNonSubmitPlan(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go"}
	m.recordSubmittedPlan(req, workspace.ToolResult{Summary: "ok"})

	if _, err := os.Stat(filepath.Join(root, ".planning")); !os.IsNotExist(err) {
		t.Fatal("non-submit_plan should not create planning dir")
	}
}

func TestAttachWriteToSubmittedEdit(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.go", Purpose: "first edit", OldText: "a", NewText: "b"},
			{Path: "b.go", Purpose: "second edit", OldText: "c", NewText: "d"},
		},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	ok := m.attachWriteToSubmittedEdit("a.go", "diff-abc123")
	if !ok {
		t.Fatal("should match a.go")
	}

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)
	if !strings.Contains(got, "- [x] first edit (a.go) <!-- pid:pe-0 --> <!-- id:diff-abc123 -->") {
		t.Errorf("first edit should be checked off with diff anchor:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] second edit (b.go) <!-- pid:pe-1 -->") {
		t.Errorf("second edit should remain unchecked:\n%s", got)
	}
}

func TestAttachWriteToSubmittedEditNoMatch(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{{Path: "a.go", Purpose: "x", OldText: "a", NewText: "b"}},
	}
	m.recordSubmittedPlan(req, workspace.ToolResult{Tool: "submit_plan", Summary: "ok"})

	if m.attachWriteToSubmittedEdit("z.go", "diff-xyz") {
		t.Error("should not match z.go")
	}
}

func TestUpdatePlanItemCheckboxInProgress(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	req := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "do the thing", Status: "pending", Paths: []string{"a.go"}},
		},
	}
	m.recordTaskLedger(req, workspace.ToolResult{Tool: "task_ledger", Summary: "ok"})

	plan, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	if !strings.Contains(string(plan), "- [ ] do the thing") {
		t.Fatalf("initial item should be unchecked:\n%s", string(plan))
	}

	update := workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "do the thing", Status: "in_progress", Paths: []string{"a.go"}},
		},
	}
	m.recordTaskLedger(update, workspace.ToolResult{Tool: "task_ledger", Summary: "ok"})

	plan, _ = os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	got := string(plan)
	if !strings.Contains(got, "- [~] do the thing") {
		t.Errorf("item should show in-progress [~]:\n%s", got)
	}
}

func TestPlanEditLabelFallbacks(t *testing.T) {
	cases := []struct {
		edit workspace.PlanEdit
		want string
	}{
		{workspace.PlanEdit{Path: "a.go", Purpose: "fix the bug"}, "fix the bug (a.go)"},
		{workspace.PlanEdit{Purpose: "no path given"}, "no path given"},
		{workspace.PlanEdit{Path: "a.go"}, "a.go"},
	}
	for _, tc := range cases {
		got := planEditLabel(tc.edit)
		if got != tc.want {
			t.Errorf("planEditLabel(%+v) = %q, want %q", tc.edit, got, tc.want)
		}
	}
}

func TestCutPlanSection(t *testing.T) {
	content := "## Session X\n\n### Plan\n- [ ] a\n- [ ] b\n\n### Other\nstuff\n"
	before, found := cutPlanSection(content, "### Plan")
	if !found {
		t.Fatal("should find section")
	}
	if strings.Contains(before, "### Plan") {
		t.Errorf("before should not contain ### Plan:\n%s", before)
	}
	if !strings.Contains(before, "## Session X") {
		t.Errorf("before should keep heading:\n%s", before)
	}
	if !strings.Contains(before, "### Other") {
		t.Errorf("before should keep ### Other:\n%s", before)
	}
}

func TestCutPlanSectionNoHeading(t *testing.T) {
	content := "## Session X\n\n### Planned\n- [ ] a\n"
	before, found := cutPlanSection(content, "### Plan")
	if found {
		t.Error("should not find section")
	}
	if before != content {
		t.Errorf("should return content unchanged:\n%s", before)
	}
}

func TestPlanCheckboxCounts(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	writeFile(t, root, ".planning/sessA.md", "## Session\n### Planned\n- [ ] pending\n- [~] active\n- [x] done\n- [x] also done\n")

	checked, total := m.planCheckboxCounts()
	if total != 4 {
		t.Errorf("want total=4, got %d", total)
	}
	if checked != 2 {
		t.Errorf("want checked=2, got %d", checked)
	}
}

func TestPlanPanelAutoShowNeeded(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)

	if m.planPanelAutoShowNeeded() {
		t.Error("should not auto-show when no plan file exists")
	}

	writeFile(t, root, ".planning/sessA.md", "## Session\n")
	m.planContent = ""
	m.planMTime = m.planMTime

	if m.planPanelAutoShowNeeded() {
		t.Error("should not auto-show with empty content")
	}

	writeFile(t, root, ".planning/sessA.md", "## Session\n- [ ] a task\n")
	m.planContent = ""
	m.planMTime = m.planMTime

	if !m.planPanelAutoShowNeeded() {
		t.Error("should auto-show with checkbox items")
	}
}

func TestPlanPanelAutoShowNeededPlanExecute(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	m.forcedTaskFamily = "plan_then_execute_planner"

	writeFile(t, root, ".planning/sessA.md", "## Session\n### Plan\n- [ ] do it\n")
	m.planContent = ""
	m.planMTime = m.planMTime

	if !m.planPanelAutoShowNeeded() {
		t.Error("plan-execute should always auto-show when plan exists")
	}

	m.forcedTaskFamily = ""
	if !m.planPanelAutoShowNeeded() {
		t.Error("non-plan-execute should still auto-show with checkbox items")
	}
}

func TestToolIntent(t *testing.T) {
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
		if got != tc.want {
			t.Errorf("toolIntent(%q, %q) = %q, want %q", tc.tool, tc.path, got, tc.want)
		}
	}
}
