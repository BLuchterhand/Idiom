//go:build plan9 || js || wasip1

package commandexec

import (
	"fmt"
	"os/exec"
	"runtime"
)

func configureDetachedProcessGroup(_ *exec.Cmd) error {
	return fmt.Errorf("detached process-group ownership is not supported on %s", runtime.GOOS)
}

type managedProcessTree struct{}

func newManagedProcessTree(_ *exec.Cmd, _ int) (*managedProcessTree, error) {
	return nil, fmt.Errorf("managed process-tree ownership is not supported on %s", runtime.GOOS)
}

func (*managedProcessTree) attach() error         { return nil }
func (*managedProcessTree) abortAttach() error    { return nil }
func (*managedProcessTree) waitBeforeReap() error { return nil }
func (*managedProcessTree) terminate() error      { return nil }
func (*managedProcessTree) close() error          { return nil }
