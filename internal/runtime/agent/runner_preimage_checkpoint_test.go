package agent

import (
	"os"
	"path/filepath"
	"testing"

	"idiom/internal/capabilities/workspace"
)

func TestSnapshotWritePreimagesCapturesEditTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	store := NewPreimageCheckpointStore(root)
	result, blocked := snapshotWritePreimages(store, workspace.ToolRequest{Tool: "edit_file", Path: "README.md"})

	if blocked || result.Error != "" {
		t.Fatalf("snapshot blocked unexpectedly: blocked=%v result=%#v", blocked, result)
	}

	if !store.Has("README.md") {
		t.Fatal("expected edit target preimage to be captured")
	}
}

func TestSnapshotWritePreimagesCapturesApplyPatchTargets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	store := NewPreimageCheckpointStore(root)
	patch := "*** Begin Patch\n" +
		"*** Update File: a.txt\n" +
		"@@\n" +
		"-before\n" +
		"+after\n" +
		"*** Add File: b.txt\n" +
		"+new\n" +
		"*** End Patch"
	result, blocked := snapshotWritePreimages(store, workspace.ToolRequest{Tool: "apply_patch", Content: patch})

	if blocked || result.Error != "" {
		t.Fatalf("snapshot blocked unexpectedly: blocked=%v result=%#v", blocked, result)
	}

	for _, path := range []string{"a.txt", "b.txt"} {
		if !store.Has(path) {
			t.Fatalf("expected apply_patch target %s preimage to be captured", path)
		}
	}
}

func TestSnapshotWritePreimagesRejectsUnsafeTarget(t *testing.T) {
	store := NewPreimageCheckpointStore(t.TempDir())
	result, blocked := snapshotWritePreimages(store, workspace.ToolRequest{Tool: "write_file", Path: "../outside.txt"})

	if !blocked || result.Error == "" {
		t.Fatalf("expected unsafe write target to be blocked, blocked=%v result=%#v", blocked, result)
	}
}
