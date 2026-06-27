package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

var syncDirFunc = syncDir

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

func IsPostCommit(err error) bool {
	var postCommit *PostCommitError
	return errors.As(err, &postCommit) && postCommit.Committed()
}

func WriteFile(path string, body []byte, perm os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" {
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

	if err := tmp.Chmod(targetPerm); err != nil {
		_ = tmp.Close()
		return err
	}

	n, err := tmp.Write(body)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if n != len(body) {
		_ = tmp.Close()
		return io.ErrShortWrite
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	renamed = true

	if err := syncDirFunc(dir); err != nil && !dirSyncUnsupported(err) {
		return &PostCommitError{Op: "atomic write committed but parent directory sync failed", Err: err}
	}

	return nil
}

func dirSyncUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return errors.Is(err, syscall.EINVAL) ||
			errors.Is(err, syscall.ENOTSUP) ||
			errors.Is(err, syscall.EPERM) ||
			errors.Is(err, syscall.EACCES)
	}
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)
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
