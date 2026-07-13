//go:build darwin

package commandexec

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func normalizeManagedProcessGroupKillError(pgid int, err error) error {
	if errors.Is(err, unix.ESRCH) {
		return os.ErrProcessDone
	}
	if !errors.Is(err, unix.EPERM) {
		return err
	}

	// Darwin reports EPERM when killpg addresses a group whose only remaining
	// member is the observed zombie leader. Confirm that no descendant remains
	// before treating that kernel result as completed teardown.
	processes, inspectErr := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if inspectErr != nil {
		return errors.Join(err, inspectErr)
	}
	for _, process := range processes {
		if process.Proc.P_pid != int32(pgid) {
			return err
		}
	}

	return os.ErrProcessDone
}
