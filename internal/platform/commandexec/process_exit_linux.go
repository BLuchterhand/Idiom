//go:build linux

package commandexec

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	linuxChildExited    = 1
	linuxChildKilled    = 2
	linuxChildDumped    = 3
	linuxChildTrapped   = 4
	linuxChildStopped   = 5
	linuxChildContinued = 6
)

func managedProcessExitedBeforeAttach(pid int) (bool, error) {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}

		switch info.Code {
		case 0:
			return false, nil
		case linuxChildExited, linuxChildKilled, linuxChildDumped:
			return true, nil
		default:
			return false, fmt.Errorf("observe managed process %d before attachment: unexpected child state code %d", pid, info.Code)
		}
	}
}

func waitManagedProcessExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}

		return err
	}
}

func waitManagedTerminalProcessLifecycle(pid int, onStop func() error) error {
	for {
		var info unix.Siginfo
		err := unix.Waitid(
			unix.P_PID,
			pid,
			&info,
			unix.WEXITED|unix.WSTOPPED|unix.WCONTINUED|unix.WNOWAIT,
			nil,
		)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}

		switch info.Code {
		case linuxChildExited, linuxChildKilled, linuxChildDumped:
			return nil
		case linuxChildStopped:
			if err := consumeLinuxChildStatus(pid, unix.WUNTRACED); err != nil {
				return err
			}
			if err := onStop(); err != nil {
				return err
			}
		case linuxChildContinued:
			if err := consumeLinuxChildStatus(pid, unix.WCONTINUED); err != nil {
				return err
			}
		case linuxChildTrapped:
			if err := consumeLinuxChildStatus(pid, unix.WUNTRACED); err != nil {
				return err
			}
		default:
			return fmt.Errorf("observe managed process %d: unexpected child state code %d", pid, info.Code)
		}
	}
}

func consumeLinuxChildStatus(pid int, option int) error {
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
