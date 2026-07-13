//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package comprehension

import (
	"errors"
	"os"
)

func tryLockRankerStateProcess(*os.File) (bool, func(), error) {
	return false, nil, errors.New("cross-process ranker state locking is unsupported on this platform")
}
