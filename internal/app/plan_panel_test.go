package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPlanContentUsesCacheUntilFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("first body"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	m := &Model{planPath: path}
	if got := m.loadPlanContent(); got != "first body" {
		t.Fatalf("first load = %q, want %q", got, "first body")
	}

	m.planContent = "cached body"
	if got := m.loadPlanContent(); got != "cached body" {
		t.Fatalf("expected unchanged plan to reuse cache, got %q", got)
	}

	if err := os.WriteFile(path, []byte("second body"), 0o644); err != nil {
		t.Fatalf("rewrite plan: %v", err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if got := m.loadPlanContent(); got != "second body" {
		t.Fatalf("expected changed plan to reload from disk, got %q", got)
	}
}

func TestLoadPlanContentClearsCacheWhenFileDisappears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	m := &Model{planPath: path}
	if got := m.loadPlanContent(); got != "body" {
		t.Fatalf("loadPlanContent = %q, want %q", got, "body")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := m.loadPlanContent(); got != "" {
		t.Fatalf("expected missing plan to return empty string, got %q", got)
	}
	if m.planContent != "" {
		t.Fatalf("expected missing plan to clear cached content, got %q", m.planContent)
	}
	if !m.planMTime.IsZero() {
		t.Fatalf("expected missing plan to clear cached mtime, got %v", m.planMTime)
	}
}
