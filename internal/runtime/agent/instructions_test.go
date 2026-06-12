package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionAttacherDefaultsToAgentsOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("Claude-specific guidance\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	if attachment := NewInstructionAttacher(root).OnFileTouch("src/main.go"); attachment != nil {
		t.Fatalf("default instruction attachment should not load CLAUDE.md, got %#v", attachment)
	}
}

func TestInstructionAttacherCanOptIntoClaudeCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("Claude-specific guidance\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	attachment := NewInstructionAttacher(root, "CLAUDE.md").OnFileTouch("src/main.go")
	if attachment == nil || !strings.HasSuffix(attachment.Path, "CLAUDE.md") || !strings.Contains(attachment.Content, "Claude-specific guidance") {
		t.Fatalf("expected explicit CLAUDE.md attachment, got %#v", attachment)
	}
}
