package safety

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreimageCheckpointStoreRestoresExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")

	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	store := NewPreimageCheckpointStore(root)
	if err := store.Snapshot("README.md"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("mutate file: %v", err)
	}

	if err := store.Restore("README.md"); err != nil {
		t.Fatalf("restore: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}

	if string(data) != "before\n" {
		t.Fatalf("restored content = %q", data)
	}
}

func TestPreimageCheckpointStoreRestoresMissingFileByDeletingIt(t *testing.T) {
	root := t.TempDir()
	store := NewPreimageCheckpointStore(root)

	if err := store.Snapshot("new.txt"); err != nil {
		t.Fatalf("snapshot missing file: %v", err)
	}

	path := filepath.Join(root, "new.txt")
	if err := os.WriteFile(path, []byte("created\n"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	if err := store.Restore("new.txt"); err != nil {
		t.Fatalf("restore missing preimage: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected restored missing file to be deleted, stat err=%v", err)
	}
}

func TestPreimageCheckpointStoreKeepsFirstSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")

	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	store := NewPreimageCheckpointStore(root)
	if err := store.Snapshot("README.md"); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	if err := os.WriteFile(path, []byte("intermediate\n"), 0o644); err != nil {
		t.Fatalf("mutate file: %v", err)
	}

	if err := store.Snapshot("README.md"); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	if err := store.Restore("README.md"); err != nil {
		t.Fatalf("restore: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}

	if string(data) != "before\n" {
		t.Fatalf("second snapshot overwrote first preimage: %q", data)
	}
}

func TestPreimageCheckpointStoreRejectsWorkspaceEscape(t *testing.T) {
	store := NewPreimageCheckpointStore(t.TempDir())
	if err := store.Snapshot("../outside.txt"); err == nil {
		t.Fatal("expected snapshot to reject workspace escape")
	}

	if err := store.Restore("/tmp/outside.txt"); err == nil {
		t.Fatal("expected restore to reject absolute path")
	}
}
