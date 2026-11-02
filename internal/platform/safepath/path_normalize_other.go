//go:build !windows

package safepath

import (
	"os"
	"path/filepath"
)

func validatePathNamespace(string) error { return nil }

func cleanPath(path string) string {
	return filepath.Clean(path)
}

func canonicalExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func isSymlinkOrReparsePoint(path string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
