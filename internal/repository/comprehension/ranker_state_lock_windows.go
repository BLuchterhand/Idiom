//go:build windows

package comprehension

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func tryLockRankerStateProcess(file *os.File) (bool, func(), error) {
	overlapped := &windows.Overlapped{}
	handle := windows.Handle(file.Fd())
	err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err == windows.ERROR_LOCK_VIOLATION {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("try lock ranker state: %w", err)
	}

	return true, func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
