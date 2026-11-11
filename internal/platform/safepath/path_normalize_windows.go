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

const (
	fileAttributeReparsePoint = 0x00000400
	fileFlagBackupSemantics   = 0x02000000
	openExisting              = 3
	shareReadWriteDelete      = 7
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	getLongPathNameProc   = kernel32.NewProc("GetLongPathNameW")
	getFinalPathNameProc  = kernel32.NewProc("GetFinalPathNameByHandleW")
	getFileAttributesProc = kernel32.NewProc("GetFileAttributesW")
	createFileProc        = kernel32.NewProc("CreateFileW")
)

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if len(path) >= 3 && path[0] == '/' && isASCIILetter(path[1]) && path[2] == '/' {
		path = string(path[1]) + ":" + path[2:]
	}
	path = cleanWindowsPathWithoutLongName(path)
	if filepath.IsAbs(path) {
		return expandWindowsLongPath(path)
	}
	return path
}

func canonicalExistingPath(path string) (string, error) {
	name, err := syscall.UTF16PtrFromString(extendedWindowsPath(filepath.Clean(path)))
	if err != nil {
		return "", err
	}
	handle, _, callErr := createFileProc.Call(uintptr(unsafe.Pointer(name)), 0, shareReadWriteDelete, 0, openExisting, fileFlagBackupSemantics, 0)
	if handle == uintptr(syscall.InvalidHandle) {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return "", errno
		}
		return "", os.ErrPermission
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

	buffer := make([]uint16, 512)
	for attempt := 0; attempt < 4; attempt++ {
		length, _, finalErr := getFinalPathNameProc.Call(handle, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if length == 0 {
			if errno, ok := finalErr.(syscall.Errno); ok && errno != 0 {
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

func isSymlinkOrReparsePoint(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	name, err := syscall.UTF16PtrFromString(extendedWindowsPath(filepath.Clean(path)))
	if err != nil {
		return false
	}
	attributes, _, _ := getFileAttributesProc.Call(uintptr(unsafe.Pointer(name)))
	return attributes == ^uintptr(0) || attributes&fileAttributeReparsePoint != 0
}

func expandWindowsLongPath(path string) string {
	current := filepath.Clean(path)
	missing := make([]string, 0, 4)
	for {
		if expanded, ok := getWindowsLongPath(current); ok {
			for i := len(missing) - 1; i >= 0; i-- {
				expanded = filepath.Join(expanded, missing[i])
			}
			return filepath.Clean(expanded)
		}
		parent := filepath.Dir(current)
		if parent == current || parent == "." || parent == "" {
			return filepath.Clean(path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func getWindowsLongPath(path string) (string, bool) {
	name, err := syscall.UTF16PtrFromString(extendedWindowsPath(path))
	if err != nil {
		return "", false
	}
	buffer := make([]uint16, 512)
	for attempt := 0; attempt < 4; attempt++ {
		length, _, _ := getLongPathNameProc.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if length == 0 {
			return "", false
		}
		if length < uintptr(len(buffer)) {
			return cleanWindowsPathWithoutLongName(syscall.UTF16ToString(buffer[:length])), true
		}
		buffer = make([]uint16, length+1)
	}
	return "", false
}

func extendedWindowsPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	if len(path) >= 3 && path[1] == ':' && filepath.IsAbs(path) {
		return `\\?\` + path
	}
	return path
}

func cleanWindowsPathWithoutLongName(path string) string {
	if strings.HasPrefix(strings.ToUpper(path), `\\?\UNC\`) {
		path = `\\` + path[len(`\\?\UNC\`):]
	} else if isExtendedDrivePath(path) {
		path = path[len(`\\?\`):]
	}
	return filepath.Clean(path)
}

func validatePathNamespace(path string) error {
	upper := strings.ToUpper(path)
	if strings.HasPrefix(upper, `\\.\`) || strings.HasPrefix(upper, `\??\`) || strings.HasPrefix(upper, `\\??\`) || strings.HasPrefix(upper, `\DEVICE\`) {
		return fmt.Errorf("unsupported Windows device path namespace")
	}
	if strings.HasPrefix(upper, `\\?\`) {
		if isExtendedDrivePath(path) || strings.HasPrefix(upper, `\\?\UNC\`) && validExtendedUNC(path) {
			return nil
		}
		return fmt.Errorf("unsupported Windows extended path namespace")
	}
	return nil
}

func isExtendedDrivePath(path string) bool {
	return len(path) >= 7 && strings.HasPrefix(path, `\\?\`) && isASCIILetter(path[4]) && path[5] == ':' && (path[6] == '\\' || path[6] == '/')
}

func validExtendedUNC(path string) bool {
	parts := strings.Split(path[len(`\\?\UNC\`):], `\`)
	return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
