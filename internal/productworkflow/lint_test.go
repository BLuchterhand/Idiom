package productworkflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLintEffortFolderRejectsSymlinkedRequiredFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "workflow_state.json")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "workflow_state.json")); err != nil {
		t.Fatal(err)
	}

	result := LintEffortFolder(root)
	if !lintSummaryHasFinding(result, "symlink_file", "workflow_state.json") {
		t.Fatalf("expected symlink_file finding for workflow_state.json, got %#v", result.Findings)
	}
}

func TestLintEffortFolderRejectsSymlinkedReportFile(t *testing.T) {
	root := t.TempDir()
	reportsDir := filepath.Join(root, "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "review.report.json")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(reportsDir, "review.report.json")); err != nil {
		t.Fatal(err)
	}

	result := LintEffortFolder(root)
	if !lintSummaryHasFinding(result, "symlink_file", filepath.Join("reports", "review.report.json")) {
		t.Fatalf("expected symlink_file finding for report, got %#v", result.Findings)
	}
}

func lintSummaryHasFinding(result LintSummaryResult, code, path string) bool {
	for _, finding := range result.Findings {
		if finding.Code == code && finding.Path == path {
			return true
		}
	}
	return false
}
