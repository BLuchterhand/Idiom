package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options configures UnderRoot validation for paths that may be missing,
// symlinked, or created later by an atomic writer.
type Options struct {
	AllowMissing           bool
	RejectSymlinkAncestors bool
	RejectSymlinkTarget    bool
}

// UnderRoot resolves path under root and returns the absolute path plus clean
// slash-separated relative path. Options control whether missing paths and
// symlink traversal are accepted.
func UnderRoot(root string, path string, label string, opts Options) (string, string, error) {
	absRoot, err := cleanAbsRoot(root)
	if err != nil {
		return "", "", err
	}

	path, err = CleanPathChecked(strings.TrimSpace(path))
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", label, err)
	}
	if path == "" {
		return "", "", fmt.Errorf("%s is empty", label)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(absRoot, path)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", label, err)
	}
	absPath = CleanPath(filepath.Clean(absPath))

	relPath, ok := Rel(absRoot, absPath)
	if !ok {
		return "", "", fmt.Errorf("%s must stay under workspace root", label)
	}
	if opts.RejectSymlinkAncestors {
		if err := RejectSymlinkAncestors(absRoot, absPath, label); err != nil {
			return "", "", err
		}
	}
	if opts.RejectSymlinkTarget {
		if err := RejectSymlinkTarget(absPath, label); err != nil {
			return "", "", err
		}
	}
	if !opts.AllowMissing {
		realPath, err := CanonicalExistingPath(absPath)
		if err != nil {
			return "", "", fmt.Errorf("resolve real %s: %w", label, err)
		}
		realRoot, err := CanonicalExistingPath(absRoot)
		if err != nil {
			return "", "", fmt.Errorf("resolve real workspace root: %w", err)
		}
		if _, ok := Rel(realRoot, realPath); !ok {
			return "", "", fmt.Errorf("%s symlink target escapes workspace root", label)
		}
	}

	return absPath, relPath, nil
}

func Rel(absRoot string, absPath string) (string, bool) {
	rel, err := filepath.Rel(CleanPath(absRoot), CleanPath(absPath))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}

	return filepath.ToSlash(filepath.Clean(rel)), true
}

func RejectSymlinkAncestors(absRoot string, absPath string, label string) error {
	rel, ok := Rel(absRoot, absPath)
	if !ok {
		return fmt.Errorf("%s must stay under workspace root", label)
	}

	parentRel := filepath.Dir(filepath.FromSlash(rel))
	if parentRel == "." {
		return nil
	}

	current := filepath.Clean(absRoot)
	for _, part := range strings.Split(parentRel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s parent: %w", label, err)
		}
		if isSymlinkOrReparsePoint(current, info) {
			return fmt.Errorf("%s must not traverse symlinked directories", label)
		}
	}

	return nil
}

func RejectSymlinkTarget(path string, label string) error {
	if info, err := os.Lstat(path); err == nil && isSymlinkOrReparsePoint(path, info) {
		return fmt.Errorf("%s must not be a symlink", label)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s: %w", label, err)
	}

	return nil
}

func cleanAbsRoot(root string) (string, error) {
	var err error
	root, err = CleanPathChecked(strings.TrimSpace(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if root == "" {
		return "", fmt.Errorf("workspace root is empty")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	return CleanPath(filepath.Clean(absRoot)), nil
}
