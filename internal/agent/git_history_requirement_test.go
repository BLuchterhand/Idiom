package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
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
