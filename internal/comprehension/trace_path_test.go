package comprehension

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeTracePathRejectsTraversalAndNormalizesBackslashes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "runner.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := normalizeTracePath(root, `pkg\runner.go`); !ok || got != "pkg/runner.go" {
		t.Fatalf("normalizeTracePath(backslash path) = %q, %v; want pkg/runner.go, true", got, ok)
	}
	if got, ok := normalizeTracePath(root, "../outside.go"); ok {
		t.Fatalf("normalizeTracePath(traversal) = %q, true; want rejection", got)
	}
}
