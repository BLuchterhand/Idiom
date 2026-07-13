package atomicfile

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

type PostCommitError struct {
	Op  string
	Err error
}

func (e *PostCommitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return strings.TrimSpace(e.Op)
	}
	return strings.TrimSpace(e.Op) + ": " + e.Err.Error()
}

func (e *PostCommitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *PostCommitError) Committed() bool {
	return e != nil
}

func WriteFile(path string, body []byte, perm os.FileMode) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("atomic write path is empty")
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	targetPerm := perm
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		targetPerm = info.Mode().Perm()
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+base+"-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeAndSyncTemp(tmp, body, targetPerm); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	renamed = true

	return syncCommittedDirectory(dir, syncDir)
}

// WriteFileRoot atomically replaces path beneath an already-open root. Temp
// creation, replacement, and parent-directory sync are all root-relative,
// subject to os.Root's documented platform limitations. Callers that prohibit
// in-root symlinks must validate parent components separately because os.Root
// intentionally permits symlinks whose targets remain beneath root.
func WriteFileRoot(root *os.Root, path string, body []byte, perm os.FileMode) error {
	return writeFileRoot(root, path, body, perm, nil, nil, true)
}

// WriteFileRootChecked prepares and syncs the replacement before invoking
// beforeCommit immediately before the root-relative rename. This lets an
// authority owner revalidate path identity and cancellation after all
// potentially slow preparation without exposing the temporary file.
func WriteFileRootChecked(root *os.Root, path string, body []byte, perm os.FileMode, beforeCommit func() error) error {
	if beforeCommit == nil {
		return fmt.Errorf("atomic write before-commit check is nil")
	}

	return writeFileRoot(root, path, body, perm, beforeCommit, nil, true)
}

// WriteFileRootCheckedObserved reports the exact committed path identity after
// the root-relative rename and validation, before parent-directory sync. The
// observer is evidence-only and cannot veto or alter the committed write.
func WriteFileRootCheckedObserved(root *os.Root, path string, body []byte, perm os.FileMode, beforeCommit func() error, observeCommit func(os.FileInfo)) error {
	if beforeCommit == nil {
		return fmt.Errorf("atomic write before-commit check is nil")
	}
	if observeCommit == nil {
		return fmt.Errorf("atomic write commit observer is nil")
	}
	return writeFileRoot(root, path, body, perm, beforeCommit, observeCommit, true)
}

// WriteFileRootCheckedExactMode atomically replaces path using the supplied
// mode even when a regular target already exists. This is intended for
// restoring an authoritative preimage whose permissions are part of the
// state being restored.
func WriteFileRootCheckedExactMode(root *os.Root, path string, body []byte, perm os.FileMode, beforeCommit func() error) error {
	if beforeCommit == nil {
		return fmt.Errorf("atomic write before-commit check is nil")
	}

	return writeFileRoot(root, path, body, perm, beforeCommit, nil, false)
}

func writeFileRoot(root *os.Root, path string, body []byte, perm os.FileMode, beforeCommit func() error, observeCommit func(os.FileInfo), preserveExistingMode bool) error {
	if root == nil {
		return fmt.Errorf("atomic write root is nil")
	}

	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("atomic write path must be root-relative")
	}
	path = filepath.Clean(path)
	if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(filepath.ToSlash(path), "../") {
		return fmt.Errorf("atomic write path must be root-relative")
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	targetPerm := perm
	if info, err := root.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("atomic write target %q must not be a symlink", filepath.ToSlash(path))
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("atomic write target %q must be a regular file", filepath.ToSlash(path))
		}
		if preserveExistingMode {
			targetPerm = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	tmp, tmpPath, err := createTempRoot(root, dir, base)
	if err != nil {
		return err
	}
	tmpInfo, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		_ = root.Remove(tmpPath)
		return fmt.Errorf("inspect atomic temp file: %w", err)
	}

	renamed := false
	defer func() {
		if !renamed {
			_ = root.Remove(tmpPath)
		}
	}()

	if err := writeAndSyncTemp(tmp, body, targetPerm); err != nil {
		return err
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	if err := root.Rename(tmpPath, path); err != nil {
		return err
	}
	renamed = true
	committedInfo, err := root.Lstat(path)
	if err != nil {
		return &PostCommitError{Op: "atomic write committed but target validation failed", Err: err}
	}
	if committedInfo.Mode()&os.ModeSymlink != 0 || !committedInfo.Mode().IsRegular() || !os.SameFile(tmpInfo, committedInfo) {
		return &PostCommitError{Op: "atomic write committed but target validation failed", Err: errors.New("target identity changed during replacement")}
	}
	if observeCommit != nil {
		observeCommit(committedInfo)
	}

	return syncCommittedRootDirectory(root, dir)
}

func writeAndSyncTemp(tmp *os.File, body []byte, perm os.FileMode) error {
	n, err := tmp.Write(body)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if n != len(body) {
		_ = tmp.Close()
		return io.ErrShortWrite
	}
	// Apply the authoritative mode after writing because Unix may clear setuid
	// or setgid bits when file contents change.
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	return nil
}

func createTempRoot(root *os.Root, dir string, base string) (*os.File, string, error) {
	const maxAttempts = 100

	for range maxAttempts {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, "", fmt.Errorf("generate atomic temp name: %w", err)
		}

		tmpPath := filepath.Join(dir, fmt.Sprintf(".%s-%x.tmp", base, suffix))
		tmp, err := root.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return tmp, tmpPath, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}

	return nil, "", fmt.Errorf("create atomic temp file for %q: too many name collisions", filepath.ToSlash(base))
}

func syncCommittedRootDirectory(root *os.Root, dir string) error {
	if err := SyncDirRoot(root, dir); err != nil {
		return &PostCommitError{Op: "atomic write committed but parent directory sync failed", Err: err}
	}
	return nil
}

// SyncDirRoot flushes changes to a directory beneath an already-open root.
// Platforms and filesystems that do not support directory sync retain the same
// best-effort behavior as WriteFile.
func SyncDirRoot(root *os.Root, dir string) error {
	if root == nil {
		return fmt.Errorf("directory sync root is nil")
	}

	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("directory sync path is empty")
	}
	dir = filepath.Clean(dir)
	if filepath.IsAbs(dir) || dir == ".." || strings.HasPrefix(filepath.ToSlash(dir), "../") {
		return fmt.Errorf("directory sync path must be root-relative")
	}

	err := syncRootDir(root, dir)
	if err == nil || dirSyncUnsupported(err) {
		return nil
	}
	return err
}

func syncRootDir(root *os.Root, dir string) error {
	handle, err := root.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent dir for sync: %w", err)
	}
	defer handle.Close()

	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}

func syncCommittedDirectory(dir string, syncDirectory func(string) error) error {
	err := syncDirectory(dir)
	if err == nil || dirSyncUnsupported(err) {
		return nil
	}

	return &PostCommitError{Op: "atomic write committed but parent directory sync failed", Err: err}
}

func dirSyncUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return errors.Is(err, syscall.EINVAL) ||
			errors.Is(err, errors.ErrUnsupported) ||
			errors.Is(err, syscall.EPERM) ||
			errors.Is(err, syscall.EACCES)
	}
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, errors.ErrUnsupported)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent dir for sync: %w", err)
	}
	defer handle.Close()

	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}
