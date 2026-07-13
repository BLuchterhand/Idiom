//go:build darwin

package commandexec

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

func managedProcessActiveForTest(pid int) bool {
	if pid <= 1 {
		return false
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err == nil {
		return process.Proc.P_stat != darwinProcessStateZombie
	}
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
		return false
	}

	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
