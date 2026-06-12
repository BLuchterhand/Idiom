package util

import (
	"fmt"
	"path/filepath"
	"strings"
)

// NormalizePath returns a cleaned, slash-normalized relative (or root-relative) path.
// It trims whitespace, replaces backslashes, cleans, and converts to forward slashes.
// It does NOT validate against path traversal — callers needing security checks
// should layer those on top.
func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")

	return filepath.ToSlash(filepath.Clean(path))
}

// NormalizeRelPath resolves a relative path against root and normalizes it.
// It rejects absolute paths and empty roots.
func NormalizeRelPath(root, relPath string) (string, error) {
	root = NormalizePath(root)
	if root == "" || root == "." {
		return "", fmt.Errorf("normalize rel path: root must be non-empty")
	}

	relPath = NormalizePath(relPath)
	if relPath == "" {
		return "", fmt.Errorf("normalize rel path: path is empty")
	}

	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("normalize rel path: path must be relative: %s", relPath)
	}

	return NormalizePath(filepath.Join(root, relPath)), nil
}
