//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package languagetools

import (
	"errors"
	"os"
	"syscall"
)

func tryLockCppCache(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
