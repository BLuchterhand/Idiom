//go:build darwin

package commandexec

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

func managedProcessExitedBeforeAttach(pid int) (bool, error) {
	for {
		info, err := darwinWaitid(pid, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}

		switch info.Code {
		case 0:
			return false, nil
		case darwinChildExited, darwinChildKilled, darwinChildDumped:
			return true, nil
		default:
			return false, fmt.Errorf("observe managed process %d before attachment: unexpected child state code %d", pid, info.Code)
		}
	}
}

func waitManagedProcessExit(pid int) error {
	for {
		_, err := darwinWaitid(pid, unix.WEXITED|unix.WNOWAIT)
		if errors.Is(err, unix.EINTR) {
			continue
		}

		return err
	}
}

func waitManagedTerminalProcessLifecycle(pid int, onStop func() error) error {
	for {
		info, err := darwinWaitid(
			pid,
			unix.WEXITED|unix.WSTOPPED|unix.WCONTINUED|unix.WNOWAIT,
		)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}

		switch info.Code {
		case darwinChildExited, darwinChildKilled, darwinChildDumped:
			return nil
		case darwinChildStopped:
			if err := consumeDarwinChildStatus(pid, unix.WUNTRACED); err != nil {
				return err
			}
			if err := onStop(); err != nil {
				return err
			}
		case darwinChildContinued:
			if err := consumeDarwinChildStatus(pid, unix.WCONTINUED); err != nil {
				return err
			}
		case darwinChildTrapped:
			if err := consumeDarwinChildStatus(pid, unix.WUNTRACED); err != nil {
				return err
			}
		default:
			return fmt.Errorf("observe managed process %d: unexpected child state code %d", pid, info.Code)
		}
	}
}

const (
	darwinProcessIDType = 1

	darwinChildExited    = 1
	darwinChildKilled    = 2
	darwinChildDumped    = 3
	darwinChildTrapped   = 4
	darwinChildStopped   = 5
	darwinChildContinued = 6
)

type darwinSiginfo struct {
	Signo  int32
	Errno  int32
	Code   int32
	PID    int32
	UID    uint32
	Status int32
	Addr   uintptr
	Value  uintptr
	Band   int64
	Pad    [7]uint64
}

func darwinWaitid(pid int, options int) (darwinSiginfo, error) {
	var info darwinSiginfo
	_, _, errno := darwinLibcSyscall6(
		libcWaitidAddr,
		darwinProcessIDType,
		uintptr(uint32(pid)),
		uintptr(unsafe.Pointer(&info)),
		uintptr(options),
		0,
		0,
	)
	if errno != 0 {
		return darwinSiginfo{}, errno
	}

	return info, nil
}

func consumeDarwinChildStatus(pid int, option int) error {
	for {
		var status unix.WaitStatus
		waitedPID, err := unix.Wait4(pid, &status, unix.WNOHANG|option, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("consume managed process %d state: %w", pid, err)
		}
		if waitedPID != pid {
			return fmt.Errorf("consume managed process %d state: wait returned pid %d", pid, waitedPID)
		}

		return nil
	}
}

//go:linkname darwinLibcSyscall6 syscall.syscall6
func darwinLibcSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err unix.Errno)

var libcWaitidAddr uintptr

//go:cgo_import_dynamic libc_waitid waitid "/usr/lib/libSystem.B.dylib"
