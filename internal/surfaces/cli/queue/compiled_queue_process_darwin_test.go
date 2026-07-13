//go:build darwin

package queue

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func observeCompiledQueueProcessExit(pid int) error {
	// Darwin's x/sys surface exposes waitid constants but not the wrapper.
	// Keep an aligned buffer larger than siginfo_t and invoke waitid directly;
	// WNOWAIT observes even an already-exited child while reserving its PID for
	// the group teardown that precedes cmd.Wait.
	var info [16]uintptr
	for {
		_, _, errno := syscall.Syscall6(
			syscall.SYS_WAITID,
			1, // P_PID
			uintptr(pid),
			uintptr(unsafe.Pointer(&info[0])),
			uintptr(unix.WEXITED|unix.WNOWAIT),
			0,
			0,
		)
		runtime.KeepAlive(&info)
		if errors.Is(errno, syscall.EINTR) {
			continue
		}
		if errno != 0 {
			return fmt.Errorf("observe queue process exit: %w", errno)
		}

		return nil
	}
}

func compiledQueueProcessSnapshot() ([]compiledQueueProcessIdentity, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}

	identities := make([]compiledQueueProcessIdentity, 0, len(processes))
	for i := range processes {
		identity := compiledQueueDarwinProcessIdentity(&processes[i])
		if identity.PID > 1 {
			identities = append(identities, identity)
		}
	}

	return identities, nil
}

func compiledQueueProcessIdentityForPID(pid int) (compiledQueueProcessIdentity, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EIO) {
			return compiledQueueProcessIdentity{}, os.ErrNotExist
		}

		return compiledQueueProcessIdentity{}, fmt.Errorf("read process %d identity: %w", pid, err)
	}
	identity := compiledQueueDarwinProcessIdentity(process)
	if identity.PID != pid {
		return compiledQueueProcessIdentity{}, os.ErrNotExist
	}

	return identity, nil
}

func compiledQueueDarwinProcessIdentity(process *unix.KinfoProc) compiledQueueProcessIdentity {
	return compiledQueueProcessIdentity{
		PID:        int(process.Proc.P_pid),
		ParentPID:  int(process.Eproc.Ppid),
		GroupID:    int(process.Eproc.Pgid),
		StartToken: fmt.Sprintf("%d:%d", process.Proc.P_starttime.Sec, process.Proc.P_starttime.Usec),
	}
}
