//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package productworkflow

import (
	"errors"
	"os"
)

func lockEffortFileProcess(*os.Root, string, string) (func(), error) {
	return nil, errors.New("cross-process effort locking is unsupported on this platform")
}
