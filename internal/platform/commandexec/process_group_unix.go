//go:build darwin || linux

package commandexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

func configureDetachedProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

// managedProcessTree owns a child process group from the instant the child is
// started until every member has been terminated. The direct child is observed
// without reaping, so its PID continues to reserve the process-group identity
// until terminate has addressed the complete group.
type managedProcessTree struct {
	cmd *exec.Cmd

	attachOnce       sync.Once
	attachDone       chan struct{}
	attachErr        error
	pgid             int
	readProcessGroup func(int) (int, error)

	terminateOnce  sync.Once
	terminateErr   error
	terminateGroup func(int) error

	teardownDone chan struct{}
	teardownMu   sync.Mutex
	teardownErr  error

	terminalFD             int
	originalForegroundPGID int
}

func newManagedProcessTree(cmd *exec.Cmd, terminalFD int) (*managedProcessTree, error) {
	tree := &managedProcessTree{
		cmd:              cmd,
		attachDone:       make(chan struct{}),
		readProcessGroup: syscall.Getpgid,
		terminateGroup: func(pgid int) error {
			return syscall.Kill(-pgid, syscall.SIGKILL)
		},
		teardownDone: make(chan struct{}),
		terminalFD:   -1,
	}

	if terminalFD >= 0 {
		foregroundPGID, err := unix.IoctlGetInt(terminalFD, unix.TIOCGPGRP)
		if err != nil {
			return nil, fmt.Errorf("read terminal foreground process group: %w", err)
		}
		if foregroundPGID <= 1 {
			return nil, fmt.Errorf("read terminal foreground process group: invalid process group %d", foregroundPGID)
		}
		tree.terminalFD = terminalFD
		tree.originalForegroundPGID = syscall.Getpgrp()
		if tree.originalForegroundPGID <= 1 {
			return nil, fmt.Errorf(
				"read foreground wrapper process group: invalid process group %d",
				tree.originalForegroundPGID,
			)
		}
		if tree.originalForegroundPGID == foregroundPGID {
			cmd.SysProcAttr = &syscall.SysProcAttr{Foreground: true, Ctty: terminalFD}
		} else {
			// Preserve an already-background wrapper. Its child remains a
			// background job until the shell foregrounds the visible wrapper.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	return tree, nil
}

func (tree *managedProcessTree) attach() error {
	if tree == nil {
		return nil
	}

	tree.attachOnce.Do(func() {
		if tree.cmd == nil || tree.cmd.Process == nil || tree.cmd.Process.Pid <= 1 {
			tree.attachErr = errors.New("managed process has no started child")
			close(tree.attachDone)
			close(tree.teardownDone)
			return
		}

		pid := tree.cmd.Process.Pid
		// Setpgid makes the child PID the process-group identity at Start.
		// Publish that expected identity before validating it so any
		// post-Start attach failure still terminates and drains the group.
		tree.pgid = pid
		readProcessGroup := tree.readProcessGroup
		if readProcessGroup == nil {
			readProcessGroup = syscall.Getpgid
		}
		pgid, err := readProcessGroup(pid)
		if err != nil {
			if errors.Is(err, syscall.ESRCH) {
				exited, verifyErr := managedProcessExitedBeforeAttach(pid)
				if verifyErr == nil && exited {
					close(tree.attachDone)
					go tree.observeExit(pid)
					return
				}
				if verifyErr != nil {
					tree.attachErr = errors.Join(
						fmt.Errorf("read managed process group: %w", err),
						fmt.Errorf("verify managed process %d exited before attachment: %w", pid, verifyErr),
					)
					tree.finishAttachFailure(pid)
					return
				}
			}
			tree.attachErr = fmt.Errorf("read managed process group: %w", err)
			tree.finishAttachFailure(pid)
			return
		}
		if pgid != pid {
			tree.attachErr = fmt.Errorf("managed process group %d does not match leader %d", pgid, pid)
			tree.finishAttachFailure(pid)
			return
		}

		close(tree.attachDone)
		go tree.observeExit(pid)
	})

	return tree.attachErr
}

func (tree *managedProcessTree) finishAttachFailure(leaderPID int) {
	close(tree.attachDone)
	terminateErr := tree.terminate()
	terminationFailure := normalizeManagedTerminationError(terminateErr)
	var directErr error
	var quiescenceErr error
	if terminationFailure == nil {
		directErr = normalizeManagedTerminationError(tree.cmd.Process.Kill())
		quiescenceErr = waitManagedProcessGroupQuiescence(tree.pgid, leaderPID)
	}

	tree.teardownMu.Lock()
	tree.teardownErr = errors.Join(
		terminationFailure,
		directErr,
		quiescenceErr,
	)
	tree.teardownMu.Unlock()
	close(tree.teardownDone)
}

func (tree *managedProcessTree) abortAttach() error {
	if tree == nil {
		return nil
	}

	tree.attachOnce.Do(func() {
		close(tree.attachDone)
		close(tree.teardownDone)
	})

	return nil
}

func (tree *managedProcessTree) observeExit(pid int) {
	var observeErr error
	if tree.terminalFD >= 0 {
		observeErr = waitManagedTerminalProcessLifecycle(pid, tree.handleStoppedForegroundProcess)
	} else {
		observeErr = waitManagedProcessExit(pid)
	}
	terminateErr := tree.terminate()
	terminationFailure := normalizeManagedTerminationError(terminateErr)
	var quiescenceErr error
	if terminationFailure == nil {
		quiescenceErr = waitManagedProcessGroupQuiescence(tree.pgid, pid)
	}
	restoreErr := tree.restoreForeground()

	tree.teardownMu.Lock()
	tree.teardownErr = errors.Join(
		observeErr,
		terminationFailure,
		quiescenceErr,
		restoreErr,
	)
	tree.teardownMu.Unlock()
	close(tree.teardownDone)
}

func (tree *managedProcessTree) terminate() error {
	if tree == nil {
		return nil
	}

	<-tree.attachDone
	tree.terminateOnce.Do(func() {
		if tree.pgid > 1 {
			terminateGroup := tree.terminateGroup
			if terminateGroup == nil {
				terminateGroup = func(pgid int) error {
					return syscall.Kill(-pgid, syscall.SIGKILL)
				}
			}
			groupErr := normalizeManagedProcessGroupKillError(tree.pgid, terminateGroup(tree.pgid))
			if normalizeManagedTerminationError(groupErr) == nil {
				tree.terminateErr = groupErr
				return
			}

			// A failed group signal cannot be followed by an unbounded group
			// snapshot wait. Kill the direct leader so the observer and the one
			// owning cmd.Wait can still complete, while preserving the group
			// failure as the lifecycle result.
			var directErr error
			if tree.cmd != nil && tree.cmd.Process != nil {
				directErr = normalizeManagedTerminationError(tree.cmd.Process.Kill())
			}
			tree.terminateErr = errors.Join(groupErr, directErr)
			return
		}

		if tree.cmd != nil && tree.cmd.Process != nil {
			tree.terminateErr = tree.cmd.Process.Kill()
		}
	})

	return tree.terminateErr
}

func (tree *managedProcessTree) waitBeforeReap() error {
	if tree == nil {
		return nil
	}

	<-tree.teardownDone
	tree.teardownMu.Lock()
	defer tree.teardownMu.Unlock()

	return tree.teardownErr
}

func (tree *managedProcessTree) close() error {
	return nil
}

func normalizeManagedTerminationError(err error) error {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}

	return err
}

var foregroundRestoreMu sync.Mutex

func (tree *managedProcessTree) restoreForeground() error {
	if tree == nil || tree.terminalFD < 0 || tree.originalForegroundPGID <= 1 {
		return nil
	}
	foregroundPGID, err := tree.terminalForegroundProcessGroup()
	if err != nil {
		return err
	}
	if foregroundPGID != tree.pgid {
		return nil
	}

	return tree.setTerminalForegroundProcessGroup(tree.originalForegroundPGID)
}

func (tree *managedProcessTree) handleStoppedForegroundProcess() error {
	if tree.originalForegroundPGID <= 1 {
		return fmt.Errorf(
			"stop foreground wrapper process group: invalid process group %d",
			tree.originalForegroundPGID,
		)
	}
	if err := tree.restoreForeground(); err != nil {
		return err
	}
	if err := syscall.Kill(-tree.originalForegroundPGID, syscall.SIGTSTP); err != nil {
		return fmt.Errorf("stop foreground wrapper process group: %w", err)
	}

	// Execution resumes here after the parent shell continues the visible
	// wrapper job. A foreground resume gives the wrapper's process group the
	// terminal first; a background resume leaves the shell in the foreground.
	// Hand the terminal back only in the former case, then continue the hidden
	// managed child group in either mode.
	foregroundPGID, err := tree.terminalForegroundProcessGroup()
	if err != nil {
		return err
	}
	if foregroundPGID == tree.originalForegroundPGID {
		if err := tree.setTerminalForegroundProcessGroup(tree.pgid); err != nil {
			return err
		}
	}
	if err := syscall.Kill(-tree.pgid, syscall.SIGCONT); err != nil {
		return fmt.Errorf("continue managed foreground process group: %w", err)
	}

	return nil
}

func (tree *managedProcessTree) terminalForegroundProcessGroup() (int, error) {
	foregroundPGID, err := unix.IoctlGetInt(tree.terminalFD, unix.TIOCGPGRP)
	if err != nil {
		return 0, fmt.Errorf("read terminal foreground process group: %w", err)
	}

	return foregroundPGID, nil
}

func (tree *managedProcessTree) setTerminalForegroundProcessGroup(pgid int) error {
	if pgid <= 1 {
		return fmt.Errorf("set terminal foreground process group: invalid process group %d", pgid)
	}

	// tcsetpgrp from the temporarily-background caller would otherwise deliver
	// SIGTTOU. Block it only on this locked OS thread and restore the exact prior
	// mask after the ioctl. One terminal can have only one foreground owner.
	foregroundRestoreMu.Lock()
	defer foregroundRestoreMu.Unlock()

	return withSIGTTOUBlocked(func() error {
		if err := unix.IoctlSetPointerInt(tree.terminalFD, unix.TIOCSPGRP, pgid); err != nil {
			return fmt.Errorf("set terminal foreground process group %d: %w", pgid, err)
		}

		return nil
	})
}
