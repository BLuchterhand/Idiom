//go:build linux

package commandexec

import (
	"errors"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func withSIGTTOUBlocked(action func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var mask unix.Sigset_t
	mask.Val[(uint64(syscall.SIGTTOU)-1)/64] = 1 << ((uint64(syscall.SIGTTOU) - 1) % 64)
	var previousMask unix.Sigset_t
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, &mask, &previousMask); err != nil {
		return err
	}

	actionErr := action()
	if err := unix.PthreadSigmask(unix.SIG_SETMASK, &previousMask, nil); err != nil {
		return errors.Join(actionErr, err)
	}

	return actionErr
}
