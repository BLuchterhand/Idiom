//go:build !windows

package safepath

import (
	"syscall"
)

func createNamedFIFO(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
