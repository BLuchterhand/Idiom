package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fix 4 (workspace overview) — exercises docs/opencode-fixes-design.md §4.

func TestOverviewSystemPromptReturnsEmptyForMissingDirectory(t *testing.T) {
	if got := OverviewSystemPrompt(filepath.Join(t.TempDir(), "nonexistent")); got != "" {
		t.Fatalf("expected empty overview for missing dir, got %q", got)
	}
	if got := OverviewSystemPrompt(""); got != "" {
		t.Fatalf("expected empty overview for empty root")
	}
}

func TestOverviewSystemPromptListsTopLevelLayout(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "internal", "bubble"))
	mustMkdir(t, filepath.Join(dir, "internal", "workspace"))
	mustMkdir(t, filepath.Join(dir, "cmd", "idiom"))
	mustWrite(t, filepath.Join(dir, "go.mod"), "module sample\n")
	mustWrite(t, filepath.Join(dir, "README.md"), "hi\n")
	mustWrite(t, filepath.Join(dir, ".gitignore"), "/tmp\n")
	mustWrite(t, filepath.Join(dir, "internal/bubble/bubble.go"), "package bubble\n")
	mustWrite(t, filepath.Join(dir, "internal/bubble/bubble_test.go"), "package bubble\n")
	mustWrite(t, filepath.Join(dir, "internal/workspace/manager.go"), "package workspace\n")
	mustWrite(t, filepath.Join(dir, "cmd/idiom/main.go"), "package main\n")
	mustMkdir(t, filepath.Join(dir, "node_modules")) // should be skipped
	mustWrite(t, filepath.Join(dir, "node_modules/leftpad.js"), "module.exports={}\n")
	mustWrite(t, filepath.Join(dir, ".claude", "worktrees", "old", "main.go"), "package main\n")
	mustWrite(t, filepath.Join(dir, ".planning", "notes.md"), "noise\n")
	mustWrite(t, filepath.Join(dir, "testdata", "swe", "representative-suite.json"), "{}\n")

	got := OverviewSystemPrompt(dir)
	if got == "" {
		t.Fatal("expected non-empty overview")
	}
	for _, want := range []string{
		"Workspace overview",
		"go.mod",
		"README.md",
		".gitignore",
		"internal/",
		"bubble.go",
		"cmd/",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected overview to mention %q, got:\n%s", want, got)
		}
	}
	for _, noisy := range []string{"node_modules", ".claude", ".planning", "representative-suite.json"} {
		if strings.Contains(got, noisy) {
			t.Fatalf("overview should skip %s, got:\n%s", noisy, got)
		}
	}
	if len(got) > workspaceOverviewMaxContentBytes+64 {
		t.Fatalf("overview exceeds configured cap: len=%d", len(got))
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
