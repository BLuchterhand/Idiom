//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package comprehension

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func tryLockRankerStateProcess(file *os.File) (bool, func(), error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("try lock ranker state: %w", err)
	}

	return true, func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
