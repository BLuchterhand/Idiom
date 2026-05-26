package refinecmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupStaleRefineStateRemovesOldTitleDirs(t *testing.T) {
	root := t.TempDir()
	refineDir := filepath.Join(root, ".idiom", "refine")
	oldDir := filepath.Join(refineDir, "old-task", "iter-1")
	freshDir := filepath.Join(refineDir, "fresh-task", "iter-1")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	if err := os.MkdirAll(freshDir, 0o755); err != nil {
		t.Fatalf("mkdir fresh: %v", err)
	}
	oldFile := filepath.Join(oldDir, "draft.md")
	freshFile := filepath.Join(freshDir, "draft.md")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(freshFile, []byte("fresh"), 0o644); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	// Backdate the old file beyond the threshold (10 days).
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, old, old); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	var logBuf bytes.Buffer
	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)

	if _, err := os.Stat(filepath.Join(refineDir, "old-task")); !os.IsNotExist(err) {
		t.Fatalf("old-task title-dir should have been removed; stat err=%v", err)
	}
	if _, err := os.Stat(freshFile); err != nil {
		t.Fatalf("fresh-task should be preserved: %v", err)
	}
}

func TestCleanupStaleRefineStatePreservesActiveTitlesWithMixedAges(t *testing.T) {
	// A title with ANY recent file survives. Backdating one iter's
	// files doesn't trigger removal as long as a later iter is fresh.
	root := t.TempDir()
	refineDir := filepath.Join(root, ".idiom", "refine")
	mixed := filepath.Join(refineDir, "mixed-task")
	for _, d := range []string{filepath.Join(mixed, "iter-1"), filepath.Join(mixed, "iter-2")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	oldFile := filepath.Join(mixed, "iter-1", "draft.md")
	freshFile := filepath.Join(mixed, "iter-2", "draft.md")
	if err := os.WriteFile(oldFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(freshFile, []byte("y"), 0o644); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	var logBuf bytes.Buffer
	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)

	if _, err := os.Stat(mixed); err != nil {
		t.Fatalf("mixed-task should survive (has recent iter-2 file): %v", err)
	}
}

func TestCleanupStaleRefineStateMissingDirIsNoOp(t *testing.T) {
	root := t.TempDir()
	var logBuf bytes.Buffer
	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)
	if strings.Contains(logBuf.String(), "refine cleanup:") {
		t.Fatalf("expected silent no-op on missing dir; got: %s", logBuf.String())
	}
}

func TestCleanupStaleRefineStateRemovesEmptyTitleDirs(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, ".idiom", "refine", "empty-title")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var logBuf bytes.Buffer
	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatalf("empty title-dir should have been removed; stat err=%v", err)
	}
}
