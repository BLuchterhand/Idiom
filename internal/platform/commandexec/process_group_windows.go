//go:build windows

package commandexec

import (
	"errors"
	"os/exec"
)

func configureDetachedProcessGroup(cmd *exec.Cmd) error {
	return errors.New("detached process group is not supported on windows")
}
