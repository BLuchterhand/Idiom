//go:build linux

package commandexec

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func normalizeManagedProcessGroupKillError(_ int, err error) error {
	if errors.Is(err, unix.ESRCH) {
		return os.ErrProcessDone
	}

	return err
}
