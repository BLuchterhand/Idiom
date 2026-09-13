//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package runner

import (
	"errors"
	"os"
)

func lockSocketStartupFile(_ *os.File) error {
	return errors.New("queue socket startup locking is unsupported on this platform")
}
