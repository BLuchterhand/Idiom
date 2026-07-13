package comprehension

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
)

const (
	rankerStateLockPath          = ".idiom/ranker/state.lock"
	rankerStateLockRetryInterval = 10 * time.Millisecond
)

var rankerStateLocalLocks = struct {
	sync.Mutex
	byRoot map[string]*rankerStateLocalLock
}{byRoot: make(map[string]*rankerStateLocalLock)}

type rankerStateLocalLock struct {
	token chan struct{}
	refs  int
}

type rankerStateLock struct {
	closeOnce sync.Once
	unlock    func()
}

func acquireRankerStateLock(ctx context.Context, root *os.Root, canonicalRoot string, processBlocked func() error) (*rankerStateLock, error) {
	if root == nil {
		return nil, errors.New("ranker state root is not open")
	}
	if ctx == nil {
		return nil, errors.New("ranker state lock context is required")
	}

	unlockLocal, err := lockRankerStateLocal(ctx, canonicalRoot)
	if err != nil {
		return nil, err
	}
	file, err := openRankerStateLockFile(root)
	if err != nil {
		unlockLocal()
		return nil, err
	}

	unlockProcess, err := lockRankerStateProcess(ctx, file, processBlocked)
	if err != nil {
		_ = file.Close()
		unlockLocal()
		return nil, err
	}
	if err := validateRankerStateLockFile(root, file); err != nil {
		unlockProcess()
		unlockLocal()
		return nil, err
	}

	return &rankerStateLock{unlock: func() {
		unlockProcess()
		unlockLocal()
	}}, nil
}

func (lock *rankerStateLock) Close() {
	if lock == nil || lock.unlock == nil {
		return
	}
	lock.closeOnce.Do(lock.unlock)
}

func lockRankerStateLocal(ctx context.Context, canonicalRoot string) (func(), error) {
	rankerStateLocalLocks.Lock()
	entry := rankerStateLocalLocks.byRoot[canonicalRoot]
	if entry == nil {
		entry = &rankerStateLocalLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		rankerStateLocalLocks.byRoot[canonicalRoot] = entry
	}
	entry.refs++
	rankerStateLocalLocks.Unlock()

	select {
	case <-ctx.Done():
		releaseRankerStateLocalReference(canonicalRoot, entry)
		return nil, ctx.Err()
	case <-entry.token:
		if err := ctx.Err(); err != nil {
			entry.token <- struct{}{}
			releaseRankerStateLocalReference(canonicalRoot, entry)
			return nil, err
		}
	}

	return func() {
		entry.token <- struct{}{}
		releaseRankerStateLocalReference(canonicalRoot, entry)
	}, nil
}

func releaseRankerStateLocalReference(canonicalRoot string, entry *rankerStateLocalLock) {
	rankerStateLocalLocks.Lock()
	defer rankerStateLocalLocks.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(rankerStateLocalLocks.byRoot, canonicalRoot)
	}
}

func lockRankerStateProcess(ctx context.Context, file *os.File, blocked func() error) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		acquired, unlock, err := tryLockRankerStateProcess(file)
		if err != nil {
			return nil, err
		}
		if acquired {
			return unlock, nil
		}
		if blocked != nil {
			if err := blocked(); err != nil {
				return nil, err
			}
		}

		timer := time.NewTimer(rankerStateLockRetryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func openRankerStateLockFile(root *os.Root) (*os.File, error) {
	if info, err := root.Lstat(rankerStateLockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("ranker state lock must be a regular file")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspect ranker state lock: %w", err)
	}

	file, err := root.OpenFile(rankerStateLockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open ranker state lock: %w", err)
	}
	if err := validateRankerStateLockFile(root, file); err != nil {
		_ = file.Close()
		return nil, err
	}

	return file, nil
}

func validateRankerStateLockFile(root *os.Root, file *os.File) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect open ranker state lock: %w", err)
	}
	current, err := root.Lstat(rankerStateLockPath)
	if err != nil {
		return fmt.Errorf("inspect ranker state lock path: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !opened.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return errors.New("ranker state lock identity changed while acquiring it")
	}

	return nil
}
