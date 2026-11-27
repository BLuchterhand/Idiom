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
	if !normalizedPathWithin(canonicalRoot, finalPath) {
		return fmt.Errorf("opened %s handle escapes workspace root", label)
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

// normalizedPathWithin reports whether path is root or lies under it. Both
// must be normalized final paths (GetFinalPathNameByHandleW with
// FILE_NAME_NORMALIZED, as CanonicalExistingPath and finalPathForHandle
// return), which already carry long component names, so a case-insensitive
// lexical compare is exact. Expanding them again through CleanPath called
// GetLongPathNameW, which reopens every component, five times per handle: 62%
// of the walk's time (G907).
func normalizedPathWithin(root string, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if strings.EqualFold(root, path) {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return len(path) > len(prefix) && strings.EqualFold(path[:len(prefix)], prefix)
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
