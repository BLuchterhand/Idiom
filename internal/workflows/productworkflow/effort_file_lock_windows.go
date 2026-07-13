//go:build windows

package productworkflow

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func lockEffortFileProcess(root *os.Root, lockRelPath, purpose string) (func(), error) {
	file, err := openEffortLockFile(root, lockRelPath, purpose)
	if err != nil {
		return nil, err
	}

	overlapped := &windows.Overlapped{}
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", strings.TrimSpace(purpose), err)
	}
	if err := validateEffortLockFile(root, file, lockRelPath, purpose); err != nil {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
		return nil, err
	}

	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
