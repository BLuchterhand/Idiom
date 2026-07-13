package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const productWorkflowMutationLockRelPath = ".idiom-product-workflow.lock"

var effortFileLocalLocks = struct {
	sync.Mutex
	byKey map[string]*effortFileLocalLock
}{byKey: make(map[string]*effortFileLocalLock)}

type effortFileLocalLock struct {
	mu   sync.Mutex
	refs int
}

type effortFileLock struct {
	root       *os.Root
	effortRoot string
	unlock     func()
	closeOnce  sync.Once
}

func acquireEffortFileLock(effortRoot, lockRelPath, purpose string) (*effortFileLock, error) {
	cleanLockPath, ok := cleanEffortRelativePath(lockRelPath)
	if !ok || filepath.Dir(cleanLockPath) != "." {
		return nil, fmt.Errorf("%s lock path must be a top-level effort-relative path", purpose)
	}

	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve effort lock root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)

	unlockLocal, err := lockEffortFileLocal(absRoot, cleanLockPath)
	if err != nil {
		return nil, err
	}
	root, _, err := openEffortArtifactRoot(effortRoot, cleanLockPath)
	if err != nil {
		unlockLocal()
		return nil, err
	}
	unlockProcess, err := lockEffortFileProcess(root, cleanLockPath, purpose)
	if err != nil {
		_ = root.Close()
		unlockLocal()
		return nil, err
	}

	return &effortFileLock{
		root:       root,
		effortRoot: absRoot,
		unlock: func() {
			unlockProcess()
			_ = root.Close()
			unlockLocal()
		},
	}, nil
}

func (lock *effortFileLock) ValidatePathIdentity() error {
	if lock == nil {
		return fmt.Errorf("effort file lock is not open")
	}

	return validateEffortRootPathIdentity(lock.root, lock.effortRoot, "locked effort root")
}

func (lock *effortFileLock) Close() {
	if lock == nil || lock.unlock == nil {
		return
	}
	lock.closeOnce.Do(lock.unlock)
}

func lockEffortFileLocal(effortRoot, lockRelPath string) (func(), error) {
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve effort lock root: %w", err)
	}
	key := filepath.Clean(absRoot) + "\x00" + filepath.Clean(lockRelPath)

	effortFileLocalLocks.Lock()
	entry := effortFileLocalLocks.byKey[key]
	if entry == nil {
		entry = &effortFileLocalLock{}
		effortFileLocalLocks.byKey[key] = entry
	}
	entry.refs++
	effortFileLocalLocks.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		effortFileLocalLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(effortFileLocalLocks.byKey, key)
		}
		effortFileLocalLocks.Unlock()
	}, nil
}

func openEffortLockFile(root *os.Root, lockRelPath, purpose string) (*os.File, error) {
	file, err := root.OpenFile(lockRelPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s lock: %w", strings.TrimSpace(purpose), err)
	}
	if err := validateEffortLockFile(root, file, lockRelPath, purpose); err != nil {
		_ = file.Close()
		return nil, err
	}

	return file, nil
}

func validateEffortLockFile(root *os.Root, file *os.File, lockRelPath, purpose string) error {
	pathInfo, err := root.Lstat(lockRelPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		if err != nil {
			return fmt.Errorf("inspect %s lock: %w", strings.TrimSpace(purpose), err)
		}
		return fmt.Errorf("%s lock must be a regular file and must not be a symlink", strings.TrimSpace(purpose))
	}

	fileInfo, err := file.Stat()
	if err != nil || !os.SameFile(pathInfo, fileInfo) {
		if err != nil {
			return fmt.Errorf("inspect open %s lock: %w", strings.TrimSpace(purpose), err)
		}
		return fmt.Errorf("%s lock changed while opening", strings.TrimSpace(purpose))
	}

	return nil
}

func validateEffortRootPathIdentity(root *os.Root, effortRoot, purpose string) error {
	if root == nil {
		return fmt.Errorf("%s is not open", strings.TrimSpace(purpose))
	}
	pathInfo, err := os.Lstat(effortRoot)
	if err != nil {
		return fmt.Errorf("inspect %s path: %w", strings.TrimSpace(purpose), err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return fmt.Errorf("%s path must be a directory and must not be a symlink", strings.TrimSpace(purpose))
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect open %s: %w", strings.TrimSpace(purpose), err)
	}
	if !os.SameFile(pathInfo, rootInfo) {
		return fmt.Errorf("%s path no longer names the locked effort root", strings.TrimSpace(purpose))
	}

	return nil
}
