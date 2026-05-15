package util

import (
	"path/filepath"
	"strings"
)

func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	return filepath.Clean(path)
}

func NormalizePathKey(path string) string {
	return strings.ToLower(NormalizePath(path))
}
