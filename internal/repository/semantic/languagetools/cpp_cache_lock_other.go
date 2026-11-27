//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package languagetools

import (
	"fmt"
	"os"
)

func tryLockCppCache(*os.File) (bool, error) {
	return false, fmt.Errorf("C++ cache publication locking is unsupported on this platform")
}
