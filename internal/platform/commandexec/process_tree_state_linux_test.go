//go:build linux

package commandexec

import (
	"errors"
	"os"
	"syscall"
)

func managedProcessActiveForTest(pid int) bool {
	if pid <= 1 {
		return false
	}
	process, err := linuxProcessStatForPID(pid)
	if err == nil {
		return linuxProcessStateActive(process.state)
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
