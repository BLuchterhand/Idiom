package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstructionAttacherSkipsPromptStackSeededFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root rules"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	srcDir := filepath.Join(root, "src")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	touched := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(touched, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	attacher := NewInstructionAttacher(root)
	attacher.MarkAttached(filepath.Join(root, "AGENTS.md"))

	if got := attacher.OnFileTouch(touched); got != nil {
		t.Fatalf("expected seeded AGENTS.md to be skipped, got %#v", got)
	}
}

func TestInstructionAttacherStillAttachesUnseededNearestFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root rules"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	srcDir := filepath.Join(root, "src")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	touched := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(touched, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	attacher := NewInstructionAttacher(root)
	got := attacher.OnFileTouch(touched)
	if got == nil || got.Content != "root rules" {
		t.Fatalf("expected unseeded AGENTS.md attachment, got %#v", got)
	}
}
