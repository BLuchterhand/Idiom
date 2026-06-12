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
	result := mgr.Execute(context.Background(), ToolRequest{Tool: "git_diff", Path: "../escape"})

	if result.Error == "" {
		t.Fatal("expected path-traversal rejection")
	}
}
