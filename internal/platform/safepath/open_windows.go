//go:build windows

package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func validateOpenedFile(file *os.File, canonicalRoot string, _ string, expected os.FileInfo, label string) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", label, err)
	}
	if expected == nil || !os.SameFile(expected, opened) {
		return fmt.Errorf("%s changed while opening", label)
	}

	finalPath, err := finalPathForHandle(syscall.Handle(file.Fd()))
	if err != nil {
		return fmt.Errorf("resolve opened %s handle: %w", label, err)
	}
	finalPath = CleanPath(finalPath)
	if !samePath(canonicalRoot, finalPath) {
		if _, ok := Rel(canonicalRoot, finalPath); !ok {
			return fmt.Errorf("opened %s handle escapes workspace root", label)
		}
	}

	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return fmt.Errorf("inspect opened %s handle: %w", label, err)
	}
	if info.FileAttributes&fileAttributeReparsePoint != 0 {
		return fmt.Errorf("opened %s handle is a reparse point", label)
	}
	return nil
}

func finalPathForHandle(handle syscall.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for attempt := 0; attempt < 4; attempt++ {
		length, _, callErr := getFinalPathNameProc.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if length == 0 {
			if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
				return "", errno
			}
			return "", os.ErrInvalid
		}
		if length < uintptr(len(buffer)) {
			return cleanWindowsPathWithoutLongName(syscall.UTF16ToString(buffer[:length])), nil
		}
		buffer = make([]uint16, length+1)
	}
	return "", syscall.Errno(122)
}

func samePath(left string, right string) bool {
	left = filepath.Clean(CleanPath(left))
	right = filepath.Clean(CleanPath(right))
	return strings.EqualFold(left, right)
}
