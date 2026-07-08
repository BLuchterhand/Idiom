//go:build !windows

package commandexec

import (
	"os/exec"
	"syscall"
)

func configureDetachedProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
