//go:build darwin

package commandexec

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	darwinSignalBlock   = 1
	darwinSignalSetMask = 3
)

func withSIGTTOUBlocked(action func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	mask := uint32(1) << (uint32(syscall.SIGTTOU) - 1)
	var previousMask uint32
	_, _, blockErr := syscall.RawSyscall(
		syscall.SYS_SIGPROCMASK,
		darwinSignalBlock,
		uintptr(unsafe.Pointer(&mask)),
		uintptr(unsafe.Pointer(&previousMask)),
	)
	if blockErr != 0 {
		return blockErr
	}

	actionErr := action()
	_, _, restoreErr := syscall.RawSyscall(
		syscall.SYS_SIGPROCMASK,
		darwinSignalSetMask,
		uintptr(unsafe.Pointer(&previousMask)),
		0,
	)
	if restoreErr != 0 {
		return errors.Join(actionErr, restoreErr)
	}

	return actionErr
}
