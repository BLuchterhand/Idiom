//go:build aix || dragonfly || freebsd || illumos || netbsd || openbsd || solaris

package commandexec

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
)

func configureDetachedProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

// Managed process-tree ownership relies on exit-without-reap primitives and a
// stable process-group identity. The release targets provide those contracts on
// Darwin, Linux, and Windows; fail before Start elsewhere instead of silently
// leaking descendants behind a direct-child-only lifecycle.
type managedProcessTree struct{}

func newManagedProcessTree(_ *exec.Cmd, _ int) (*managedProcessTree, error) {
	return nil, fmt.Errorf("managed process-tree ownership is not supported on %s", runtime.GOOS)
}

func (*managedProcessTree) attach() error         { return nil }
func (*managedProcessTree) abortAttach() error    { return nil }
func (*managedProcessTree) waitBeforeReap() error { return nil }
func (*managedProcessTree) terminate() error      { return nil }
func (*managedProcessTree) close() error          { return nil }
