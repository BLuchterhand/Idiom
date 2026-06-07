package planexecute

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupStalePlanArtifacts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	stale := filepath.Join(dir, "stale.plan.json")
	recent := filepath.Join(dir, "recent.plan.json")
	pointer := filepath.Join(dir, "last.plan.json")
	for _, path := range []string{stale, recent, pointer} {
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}

	old := time.Now().Add(-10 * 24 * time.Hour)
	young := time.Now().Add(-1 * time.Hour)
	ancient := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}
	if err := os.Chtimes(recent, young, young); err != nil {
		t.Fatalf("chtimes recent: %v", err)
	}
	if err := os.Chtimes(pointer, ancient, ancient); err != nil {
		t.Fatalf("chtimes pointer: %v", err)
	}

	var logBuf bytes.Buffer
	CleanupStalePlanArtifacts(root, 7*24*time.Hour, &logBuf)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale plan should have been removed; stat err=%v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent plan should have survived; stat err=%v", err)
	}
	if _, err := os.Stat(pointer); err != nil {
		t.Fatalf("last.plan.json must survive regardless of mtime; stat err=%v", err)
	}
}
