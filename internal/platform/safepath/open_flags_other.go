//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !hurd && !illumos && !linux && !netbsd && !openbsd && !solaris

package safepath

import "os"

func safeReadOpenFlags() int {
	return os.O_RDONLY
}
