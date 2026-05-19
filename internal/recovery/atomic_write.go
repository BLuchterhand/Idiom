package recovery

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func writeRecoveryTempFileExclusive(tmpPath string, body []byte) error {
	file, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	wrote := false
	defer func() {
		if !wrote {
			_ = os.Remove(tmpPath)
		}
	}()
	n, err := file.Write(body)
	if err != nil {
		_ = file.Close()
		return err
	}
	if n != len(body) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	wrote = true
	return nil
}

func ensureRecoveryWritePath(root string, fullPath string) error {
	root = strings.TrimSpace(root)
	fullPath = strings.TrimSpace(fullPath)
	if root == "" {
		return fmt.Errorf("workspace root is empty")
	}
	if fullPath == "" {
		return fmt.Errorf("recovery output path is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return fmt.Errorf("resolve recovery output path: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	absPath = filepath.Clean(absPath)
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("recovery output path must stay under workspace root")
	}
	if info, err := os.Lstat(absPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("recovery output path must not be a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect recovery output path: %w", err)
	}
	parentRel := filepath.Dir(rel)
	if parentRel == "." {
		return nil
	}
	current := absRoot
	for _, part := range strings.Split(parentRel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect recovery output path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("recovery output path must not traverse symlinked directories")
		}
	}
	return nil
}
