package runner

import (
	"fmt"
	"os"
)

// The lock file is persistent: unlinking it on release would let contenders
// lock different inodes. The OS releases the advisory lock when this file
// closes, including after a process crash. Only startup holds it; an active
// daemon is detected through its socket while the next startup holds the lock.
func acquireSocketStartupLock(socketPath string) (*os.File, error) {
	path := socketPath + ".lock"
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("runner: socket startup lock %q must be a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("runner: inspect socket startup lock: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("runner: open socket startup lock: %w", err)
	}
	if err := lockSocketStartupFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("runner: acquire socket startup lock: %w", err)
	}
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		_ = file.Close()
		return nil, fmt.Errorf("runner: socket startup lock identity changed")
	}
	return file, nil
}
