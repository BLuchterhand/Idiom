package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func TestWriteBeforeGitHistoryEvidenceRejectsExistingLogic(t *testing.T) {
	root := t.TempDir()
	markGitHistoryRequirementWorkspace(t, root)
	writeGitHistoryRequirementTestFile(t, root, "pkg/logic.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")

	runner := gitHistoryRequirementRunner(root, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/logic.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	summary, errorText, reject := runner.writeBeforeGitHistoryEvidenceRejection(req, nil)

	if !reject {
		t.Fatalf("expected existing-file write to require git history evidence")
	}

	if summary != "Write rejected before git history evidence" {
		t.Fatalf("unexpected summary %q", summary)
	}

	if !strings.Contains(errorText, "history_range") || !strings.Contains(errorText, "history_symbol") || !strings.Contains(errorText, "commit descriptions") {
		t.Fatalf("rejection should tell the model to read commit descriptions via history tools, got %q", errorText)
	}
}

func TestWriteBeforeGitHistoryEvidenceAcceptsHistoryRange(t *testing.T) {
	root := t.TempDir()
	markGitHistoryRequirementWorkspace(t, root)
	writeGitHistoryRequirementTestFile(t, root, "pkg/logic.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")

	runner := gitHistoryRequirementRunner(root, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/logic.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	results := []workspace.ToolResult{gitHistoryRangeResult("pkg/logic.go")}

	if _, _, reject := runner.writeBeforeGitHistoryEvidenceRejection(req, results); reject {
		t.Fatalf("did not expect write rejection after successful history_range evidence")
	}
}

func TestWriteBeforeGitHistoryEvidenceAcceptsPathScopedNotApplicable(t *testing.T) {
	root := t.TempDir()
	markGitHistoryRequirementWorkspace(t, root)
	writeGitHistoryRequirementTestFile(t, root, "pkg/logic.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")

	runner := gitHistoryRequirementRunner(root, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/logic.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	results := []workspace.ToolResult{gitHistoryNotApplicableResult("pkg/logic.go")}

	if _, _, reject := runner.writeBeforeGitHistoryEvidenceRejection(req, results); reject {
		t.Fatalf("did not expect write rejection after path-scoped history_not_applicable evidence")
	}
}

func TestWriteBeforeGitHistoryEvidenceDoesNotUseNotApplicableAcrossPaths(t *testing.T) {
	root := t.TempDir()
	markGitHistoryRequirementWorkspace(t, root)
	writeGitHistoryRequirementTestFile(t, root, "pkg/logic.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")
	writeGitHistoryRequirementTestFile(t, root, "pkg/other.go", "package pkg\n")

	runner := gitHistoryRequirementRunner(root, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/logic.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	results := []workspace.ToolResult{gitHistoryNotApplicableResult("pkg/other.go")}

	if _, _, reject := runner.writeBeforeGitHistoryEvidenceRejection(req, results); !reject {
		t.Fatalf("expected path-scoped history_not_applicable for another path not to satisfy pkg/logic.go")
	}
}

func TestWriteBeforeGitHistoryEvidenceSkipsNewFile(t *testing.T) {
	root := t.TempDir()
	runner := gitHistoryRequirementRunner(root, "implementation")
	req := workspace.ToolRequest{
		Tool:        "write_file",
		Path:        "pkg/new_logic.go",
		Content:     "package pkg\n",
		Description: "add new logic",
	}

	if _, _, reject := runner.writeBeforeGitHistoryEvidenceRejection(req, nil); reject {
		t.Fatalf("new-file writes should not require prior git history")
	}
}

func TestWriteBeforeGitHistoryEvidenceSkipsWorkspaceWithoutGitMetadata(t *testing.T) {
	root := t.TempDir()
	writeGitHistoryRequirementTestFile(t, root, "pkg/logic.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")

	runner := gitHistoryRequirementRunner(root, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/logic.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic in a fixture workspace",
	}

	if _, _, reject := runner.writeBeforeGitHistoryEvidenceRejection(req, nil); reject {
		t.Fatalf("workspace without git metadata should not be blocked on unavailable history evidence")
	}
}

func TestSubmitPlanHistoryGuardRejectsExistingEditWithoutHistory(t *testing.T) {
	root := t.TempDir()
	markGitHistoryRequirementWorkspace(t, root)
	writeGitHistoryRequirementTestFile(t, root, "pkg/logic.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")

	runner := gitHistoryRequirementRunner(root, PlanThenExecutePlannerTaskFamily)
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "pkg/logic.go",
			OldText: "return \"old\"",
			NewText: "return \"new\"",
		}},
	}
	guard := runner.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, nil)

	if !guard.Intercepted {
		t.Fatalf("expected submit_plan to be rejected without git history evidence")
	}

	if !strings.Contains(guard.Result.Error, "history_range") || !strings.Contains(guard.Result.Error, "commit-description") {
		t.Fatalf("guard should require commit-description history evidence, got %q", guard.Result.Error)
	}
}

func TestSubmitPlanHistoryGuardAcceptsHistorySymbol(t *testing.T) {
	root := t.TempDir()
	markGitHistoryRequirementWorkspace(t, root)
	writeGitHistoryRequirementTestFile(t, root, "pkg/logic.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")

	runner := gitHistoryRequirementRunner(root, PlanThenExecutePlannerTaskFamily)
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "pkg/logic.go",
			OldText: "return \"old\"",
			NewText: "return \"new\"",
		}},
	}
	results := []workspace.ToolResult{{
		Tool:    "history_symbol",
		Summary: "Read git history for Apply in pkg/logic.go",
		Output:  "commit 1234567\nAuthor: Example\n\n    Preserve Apply behavior for compatibility\n",
	}}

	if guard := runner.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, results); guard.Intercepted {
		t.Fatalf("did not expect submit_plan rejection after history_symbol evidence: %#v", guard.Result)
	}
}

func gitHistoryRequirementRunner(root string, family string) *Runner {
	brief := workerruntime.RuntimeBriefForTaskFamily(family, "main", []string{"read_file", "history_range", "history_symbol", "blame_range", "edit_file", "write_file", "apply_patch", "submit_plan"})

	brief.RequiredEvidence = append([]string(nil), brief.RequiredEvidence...)

	if !runnerContainsString(brief.RequiredEvidence, requiredEvidenceGitHistoryRationale) {
		brief.RequiredEvidence = append(brief.RequiredEvidence, requiredEvidenceGitHistoryRationale)
	}

	return &Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: brief,
	}
}

func gitHistoryRangeResult(path string) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:    "history_range",
		Summary: "Read git history for " + path + " lines 1-20",
		Output:  "commit 1234567\nAuthor: Example\n\n    Preserve established behavior for compatibility\n",
	}
}

func gitHistoryNotApplicableResult(path string) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:    "history_range",
		Code:    workspace.HistoryNotApplicableCode,
		Summary: "Git history not applicable for " + path,
		Output:  `{"status":"not_applicable","code":"` + workspace.HistoryNotApplicableCode + `","reason":"untracked_path","path":"` + path + `"}`,
	}
}

func TestPlanExecuteUnavailableHistoryToolNudgeWhenHistoryAlreadySatisfied(t *testing.T) {
	brief := workerruntime.RuntimeBriefForTaskFamily(PlanThenExecutePlannerTaskFamily, "main", []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"})
	brief.RequiredEditPaths = []string{"a.go"}
	brief.RequiredEvidence = []string{requiredEvidenceGitHistoryRationale}
	results := []workspace.ToolResult{
		gitHistoryRangeResult("a.go"),
	}

	summary, errorText, ok := planThenExecuteUnavailableHistoryToolNudge(
		workspace.ToolRequest{Tool: "history_range", Path: "a.go", StartLine: 1, EndLine: 10},
		brief,
		results,
		[]string{"read_file", "code_navigate"},
	)
	if !ok {
		t.Fatal("expected repeated history_range to get typed phase nudge")
	}

	if !strings.Contains(summary, "Git history tool not available") {
		t.Fatalf("summary = %q", summary)
	}

	for _, want := range []string{"already satisfied", "a.go"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("error text missing %q: %s", want, errorText)
		}
	}
}

func TestPlanThenExecuteHistoryPendingPathsMixedEvidenceStates(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go", "c.go", "d.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		gitHistoryRangeResult("a.go"),
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		gitHistoryNotApplicableResult("b.go"),
		{Tool: "read_file", Summary: "Read f3 c.go lines 1-10 (total lines: 10)", Output: "1:package c\n"},
	}

	pending := planThenExecuteHistoryPendingPaths(brief, results, nil)
	if len(pending) != 1 || pending[0] != "c.go" {
		t.Fatalf("pending paths = %#v, want only c.go", pending)
	}

	completed := modelProtocolCompletedGitHistoryPaths(brief, results)
	if len(completed) != 2 || completed[0] != "a.go" || completed[1] != "b.go" {
		t.Fatalf("completed paths = %#v, want a.go and b.go", completed)
	}
}

func TestAnswerHasGitHistoryNoopOrBlocker(t *testing.T) {
	accepted := []string{
		"<<QUESTION>>: history shows this change is unsafe",
		`source-satisfied: already implemented`,
		"Changed paths: none",
		"This is a noop because the requested behavior already exists.",
		"Only a new file is involved.",
	}
	for _, answer := range accepted {
		if !answerHasGitHistoryNoopOrBlocker(answer) {
			t.Fatalf("expected no-op/blocker answer to satisfy predicate: %q", answer)
		}
	}

	rejected := []string{
		"I changed the existing function.",
		"submit_plan will update the old code",
	}
	for _, answer := range rejected {
		if answerHasGitHistoryNoopOrBlocker(answer) {
			t.Fatalf("did not expect mutation answer to satisfy predicate: %q", answer)
		}
	}
}

func writeGitHistoryRequirementTestFile(t *testing.T, root string, rel string, body string) {
	t.Helper()

	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}

	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func markGitHistoryRequirementWorkspace(t *testing.T, root string) {
	t.Helper()

	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create .git marker: %v", err)
	}
}

func runnerContainsString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}

	return false
}
