package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedFileRegistryDeduplicatesAndOrdersByRecency(t *testing.T) {
	reg := newPinnedFileRegistry()
	reg.Pin("internal/app/a.go")
	reg.Pin("internal/app/b.go")
	reg.Pin("internal/app/a.go") // re-pin should refresh, not duplicate
	pins := reg.Pins()
	if len(pins) != 2 {
		t.Fatalf("expected 2 unique pins, got %v", pins)
	}
	if pins[0] != "internal/app/a.go" {
		t.Fatalf("expected most-recent first, got %v", pins)
	}
}

func TestPinnedFileRegistryEvictsLRUBeyondCap(t *testing.T) {
	reg := newPinnedFileRegistry()
	for i := 0; i < pinnedFileMaxEntries+3; i++ {
		reg.Pin("file" + string(rune('a'+i)) + ".go")
	}
	if got := len(reg.Pins()); got != pinnedFileMaxEntries {
		t.Fatalf("expected pin cap to be enforced at %d, got %d", pinnedFileMaxEntries, got)
	}
}

func TestBuildSnapshotReadsCurrentContentAndRespectsByteCap(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.go")
	if err := os.WriteFile(small, []byte("package small\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatalf("write small: %v", err)
	}
	big := filepath.Join(dir, "big.go")
	large := strings.Repeat("// huge\n", 1000)
	if err := os.WriteFile(big, []byte(large), 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	reg := newPinnedFileRegistry()
	reg.Pin("small.go")
	reg.Pin("big.go")

	got := reg.BuildSnapshot(dir)
	if got == "" {
		t.Fatal("expected non-empty snapshot")
	}
	if !strings.Contains(got, "Pinned files snapshot") {
		t.Fatalf("expected header, got:\n%s", got)
	}
	if !strings.Contains(got, "small.go") || !strings.Contains(got, "big.go") {
		t.Fatalf("expected both files named in snapshot, got:\n%s", got)
	}
	if len(got) > pinnedFileSnapshotMaxBytes {
		t.Fatalf("snapshot exceeded byte cap: %d > %d", len(got), pinnedFileSnapshotMaxBytes)
	}
}

func TestBuildSnapshotIsEmptyWhenNoPinsExist(t *testing.T) {
	reg := newPinnedFileRegistry()
	if got := reg.BuildSnapshot(t.TempDir()); got != "" {
		t.Fatalf("expected empty snapshot when no pins, got:\n%s", got)
	}
}

func TestExtractPinnablePathFromSummaryCoversCommonShapes(t *testing.T) {
	cases := map[string]string{
		"Read f5 internal/app/app.go lines 1-120":  "internal/app/app.go",
		"Read internal/app/app.go lines 1-80":      "internal/app/app.go",
		"Edited internal/frontier/control.go":      "internal/frontier/control.go",
		"Wrote docs/note.md":                       "docs/note.md",
		"Outlined f3 internal/agent/runner.go":     "internal/agent/runner.go",
		"something unrelated":                      "",
		"":                                         "",
	}
	for input, want := range cases {
		if got := extractPinnablePathFromSummary(input); got != want {
			t.Fatalf("input=%q want=%q got=%q", input, want, got)
		}
	}
}
