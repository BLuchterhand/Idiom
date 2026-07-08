package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func gitWorkspace(t *testing.T) (*Manager, string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}

	dir := t.TempDir()

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed tracked.txt: %v", err)
	}

	for _, args := range [][]string{
		{"add", "tracked.txt"},
		{"commit", "-q", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	cfg := config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   100,
		EnableShell:           true,
		EnableWrites:          true,
	}

	return NewManager(cfg), dir
}

func TestGitStatusReportsModifiedAndUntracked(t *testing.T) {
	mgr, dir := gitWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("modify tracked: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("create new: %v", err)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_status"})
	if result.Error != "" {
		t.Fatalf("git_status error: %#v", result)
	}
	if result.Tool != ToolNameGitStatus || !strings.Contains(result.Summary, "Read git status in ") {
		t.Fatalf("git_status identity/summary = %#v", result)
	}

	if !strings.Contains(result.Output, "tracked.txt") {
		t.Fatalf("git_status output missing modified file: %q", result.Output)
	}

	if !strings.Contains(result.Output, "new.txt") {
		t.Fatalf("git_status output missing untracked file: %q", result.Output)
	}
}

func TestGitDiffShowsWorkingTreeChange(t *testing.T) {
	mgr, dir := gitWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("modify tracked: %v", err)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff"})
	if result.Error != "" {
		t.Fatalf("git_diff error: %#v", result)
	}
	if result.Tool != ToolNameGitDiff || !strings.Contains(result.Summary, "Read git diff for working tree in ") {
		t.Fatalf("git_diff identity/summary = %#v", result)
	}

	if !strings.Contains(result.Output, "-hello") || !strings.Contains(result.Output, "+changed") {
		t.Fatalf("git_diff missing expected hunk: %q", result.Output)
	}
}

func TestGitDiffScopedToPath(t *testing.T) {
	mgr, dir := gitWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatalf("create: %v", err)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff", Path: "tracked.txt"})
	if result.Error != "" {
		t.Fatalf("git_diff error: %#v", result)
	}

	if !strings.Contains(result.Output, "tracked.txt") {
		t.Fatalf("git_diff scoped output missing target: %q", result.Output)
	}

	if strings.Contains(result.Output, "other.txt") {
		t.Fatalf("git_diff scoped output should not include other.txt: %q", result.Output)
	}
}

func TestGitDiffRejectsPathTraversal(t *testing.T) {
	mgr, _ := gitWorkspace(t)
	mgr.writeGuard.NoteEditRecoveryRequired(filepath.Join(mgr.Root(), "tracked.txt"), 1, "needs recovery")

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff", Path: "../escape"})

	if result.Error == "" {
		t.Fatal("expected path-traversal rejection")
	}
	if _, ok := mgr.writeGuard.PendingEditRecovery(filepath.Join(mgr.Root(), "tracked.txt")); !ok {
		t.Fatal("path-rejected git_diff must not clear edit recovery")
	}
}

func TestGitDiffClearsEditRecoveryAfterSuccessfulDiff(t *testing.T) {
	mgr, dir := gitWorkspace(t)
	otherPath := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(otherPath, []byte("other\n"), 0o644); err != nil {
		t.Fatalf("create other: %v", err)
	}
	runWorkspaceGit(t, dir, "add", "other.txt")
	runWorkspaceGit(t, dir, "commit", "-q", "-m", "other")

	trackedPath, err := mgr.pathAccess.ResolvePath("tracked.txt", "")
	if err != nil {
		t.Fatalf("resolve tracked path: %v", err)
	}
	otherResolved, err := mgr.pathAccess.ResolvePath("other.txt", "")
	if err != nil {
		t.Fatalf("resolve other path: %v", err)
	}
	mgr.writeGuard.NoteEditRecoveryRequired(trackedPath, 1, "tracked needs recovery")
	mgr.writeGuard.NoteEditRecoveryRequired(otherResolved, 1, "other needs recovery")

	if err := os.WriteFile(trackedPath, []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("modify tracked: %v", err)
	}
	if err := os.WriteFile(otherPath, []byte("changed other\n"), 0o644); err != nil {
		t.Fatalf("modify other: %v", err)
	}

	scoped := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff", Path: "tracked.txt"})
	if scoped.Error != "" {
		t.Fatalf("scoped git_diff error: %#v", scoped)
	}
	if _, ok := mgr.writeGuard.PendingEditRecovery(trackedPath); ok {
		t.Fatal("successful scoped git_diff should clear target edit recovery")
	}
	if _, ok := mgr.writeGuard.PendingEditRecovery(otherResolved); !ok {
		t.Fatal("successful scoped git_diff must not clear unrelated edit recovery")
	}

	full := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff"})
	if full.Error != "" {
		t.Fatalf("full git_diff error: %#v", full)
	}
	if _, ok := mgr.writeGuard.PendingEditRecovery(otherResolved); ok {
		t.Fatal("successful full git_diff should clear all edit recovery")
	}
}

func runWorkspaceGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
