package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkbranchManagerCreatesDiffsAndRemovesWorktree(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	manager := WorkbranchManager{
		Root:    root,
		Scratch: t.TempDir(),
		Prefix:  "codex/test",
	}
	checkout, err := manager.Create(context.Background(), "Refactor App.go!")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if checkout.Name != "refactor-app-go" || checkout.Branch != "codex/test/refactor-app-go" {
		t.Fatalf("unexpected checkout identity: %#v", checkout)
	}
	if _, err := os.Stat(filepath.Join(checkout.Path, "README.md")); err != nil {
		t.Fatalf("expected worktree checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(checkout.Path, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	diff, err := manager.Diff(context.Background(), checkout)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.ChangedPaths) != 1 || diff.ChangedPaths[0] != "README.md" {
		t.Fatalf("unexpected changed paths: %#v", diff)
	}
	if diff.Stat == "" {
		t.Fatalf("expected diff stat")
	}
	if err := manager.Remove(context.Background(), checkout); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(checkout.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree path removed, stat err=%v", err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
