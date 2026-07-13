//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package productworkflow

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

func lockEffortFileProcess(root *os.Root, lockRelPath, purpose string) (func(), error) {
	file, err := openEffortLockFile(root, lockRelPath, purpose)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", strings.TrimSpace(purpose), err)
	}
	if err := validateEffortLockFile(root, file, lockRelPath, purpose); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
