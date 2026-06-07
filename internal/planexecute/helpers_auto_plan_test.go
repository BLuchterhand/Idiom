package planexecute

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/workspace"
)

func TestPlanExecuteExactPlanEditsParsesBlockquotedRequiredBehaviorNewFileBody(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "writerroute"), 0o755); err != nil {
		t.Fatalf("mkdir writerroute: %v", err)
	}
	prompt := []string{
		"# Objective",
		"",
		"## Referenced Plan Documents",
		"",
		"> # Refined Atom",
		">",
		"> ## Files To Modify",
		">",
		"> | File | Change |",
		"> |---|---|",
		"> | `internal/writerroute/decision.go` | NEW FILE: add route decision type |",
		">",
		"> ## Required Behavior",
		">",
		"> 1. Create `internal/writerroute/decision.go` with the following content:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> const SchemaVersion = \"idiom.writer_route.v1\"",
		"> ```",
	}
	edits, reason := planExecuteExactPlanEditsForWorkspace(strings.Join(prompt, "\n"), []string{"internal/writerroute/decision.go"}, root)
	if reason != "" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if len(edits) != 1 {
		t.Fatalf("edits = %d, want 1: %#v", len(edits), edits)
	}
	edit := edits[0]
	if edit.Path != "internal/writerroute/decision.go" || !edit.ReplaceAll || edit.OldText != "" {
		t.Fatalf("unexpected edit shape: %#v", edit)
	}
	if edit.NewText != "package writerroute\n\nconst SchemaVersion = \"idiom.writer_route.v1\"" {
		t.Fatalf("unexpected new_text: %q", edit.NewText)
	}
}

func TestPlanExecuteExactPlanEditsBindsImplicitBodyToNearestPathMention(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "writerroute"), 0o755); err != nil {
		t.Fatalf("mkdir writerroute: %v", err)
	}
	prompt := []string{
		"> ## Required Behavior",
		">",
		"> 1. Create `internal/writerroute/decision.go` with the following content:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> const SchemaVersion = \"idiom.writer_route.v1\"",
		"> ```",
		">",
		"> 2. Create `internal/writerroute/decision_test.go` with tests:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> import \"testing\"",
		"> ```",
	}
	edits, reason := planExecuteExactPlanEditsForWorkspace(strings.Join(prompt, "\n"), []string{"internal/writerroute/decision.go"}, root)
	if reason != "" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if len(edits) != 1 {
		t.Fatalf("edits = %d, want 1: %#v", len(edits), edits)
	}
	if strings.Contains(edits[0].NewText, "testing") {
		t.Fatalf("implicit body parser captured out-of-scope test block: %q", edits[0].NewText)
	}
}

func TestPlanExecuteExactPlanEditsDoesNotInferImplicitBodyForExistingPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "idiom"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/idiom: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "idiom", "lane.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write lane.go: %v", err)
	}
	prompt := []string{
		"## Referenced Plan Documents",
		"",
		"> ## Required Behavior",
		">",
		"> 1. Update `cmd/idiom/lane.go` with this illustrative insertion shape:",
		">",
		"> ```text",
		"> package writerroute",
		"> ```",
	}
	edits, reason := planExecuteExactPlanEditsForWorkspace(strings.Join(prompt, "\n"), []string{"cmd/idiom/lane.go"}, root)
	if len(edits) != 0 {
		t.Fatalf("implicit body parser must not replace existing paths: %#v", edits)
	}
	if reason == "" {
		t.Fatalf("expected skip reason for non-exact prompt")
	}
}

func TestPlanExecuteClassifyAutoExactPlanEditsDetectsAlreadyAppliedAndPending(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "applied.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write applied: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pending.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}

	result := planExecuteClassifyAutoExactPlanEdits(root, []workspace.PlanEdit{
		{Path: "applied.txt", OldText: "old\n", NewText: "new\n"},
		{Path: "pending.txt", OldText: "old\n", NewText: "new\n"},
	})

	if result.AppliedCount != 1 || result.PendingCount != 1 || result.UnknownCount != 0 {
		t.Fatalf("unexpected classification: %#v", result)
	}
	if len(result.AppliedPaths) != 1 || result.AppliedPaths[0] != "applied.txt" {
		t.Fatalf("applied paths = %#v", result.AppliedPaths)
	}
	if len(result.PendingPaths) != 1 || result.PendingPaths[0] != "pending.txt" {
		t.Fatalf("pending paths = %#v", result.PendingPaths)
	}
}

func TestPlanExecuteTryAutoExactPlanSourceSatisfiedWhenAllEditsAlreadyApplied(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new a\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	cfg := testPlanExecuteConfig(root)
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.txt` | Update a. |\n\n" +
		"## Required Edits\n\n" +
		"In `a.txt`, replace the text.\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n"

	result := planExecuteTryAutoExactPlan(cfg, prompt, []string{"a.txt"})

	if !result.SourceSatisfied || result.Submitted {
		t.Fatalf("expected source-satisfied without submission, got %#v", result)
	}
	if result.AppliedCount != 1 || len(result.AppliedPaths) != 1 || result.AppliedPaths[0] != "a.txt" {
		t.Fatalf("unexpected applied classification: %#v", result)
	}
}

func TestPlanExecuteNewFileAuthorizationIsPathAware(t *testing.T) {
	if !planExecuteNewFileAuthorizedForPath("Create the new helper with the following content.", "pkg/helper.go") {
		t.Fatal("explicit create/full-body language should authorize a new implementation file")
	}
	if !planExecuteNewFileAuthorizedForPath("Add regression tests for invalid routes.", "pkg/route_test.go") {
		t.Fatal("add-test language should authorize a missing test-like path")
	}
	if planExecuteNewFileAuthorizedForPath("Add score breakdown to the existing tooltip.", "project/scripts/economy/score_breakdown.gd") {
		t.Fatal("generic add language must not authorize an invented implementation path")
	}
}

func testPlanExecuteConfig(root string) config.Config {
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	return cfg
}
