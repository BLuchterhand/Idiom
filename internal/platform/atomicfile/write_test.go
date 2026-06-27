package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestWriteFilePreservesExistingPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	assertFileMode(t, path, 0o600)
}

func TestWriteFilePreservesExistingExecutableMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve POSIX execute bits")
	}

	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod seed file: %v", err)
	}

	if err := WriteFile(path, []byte("#!/bin/sh\necho ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	assertFileMode(t, path, 0o755)
}

func TestWriteFileUsesPermForNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	if err := WriteFile(path, []byte("new"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	assertFileMode(t, path, 0o640)
}

func TestWriteFileTreatsUnsupportedDirectorySyncAsSuccessfulAfterRename(t *testing.T) {
	old := syncDirFunc
	syncDirFunc = func(string) error { return syscall.EINVAL }
	t.Cleanup(func() { syncDirFunc = old })

	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile should ignore unsupported dir sync after rename: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestWriteFileReturnsDirectorySyncOperationalErrors(t *testing.T) {
	old := syncDirFunc
	syncDirFunc = func(string) error { return errors.New("disk disappeared") }
	t.Cleanup(func() { syncDirFunc = old })

	path := filepath.Join(t.TempDir(), "state.json")
	err := WriteFile(path, []byte("ok"), 0o644)
	if err == nil {
		t.Fatal("WriteFile should return operational dir sync errors as post-commit errors")
	}
	var postCommitErr *PostCommitError
	if !errors.As(err, &postCommitErr) || !postCommitErr.Committed() {
		t.Fatalf("WriteFile error should expose committed post-rename state, got %T %[1]v", err)
	}
	if !IsPostCommit(err) {
		t.Fatalf("IsPostCommit should recognize committed post-rename error: %v", err)
	}

	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("committed file should remain readable after dir sync error: %v", readErr)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode = %#o, want %#o", got, want)
	}
}
