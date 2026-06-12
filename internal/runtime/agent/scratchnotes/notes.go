package scratchnotes

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	MaxKeyLength    = 80
	MaxContentBytes = 64 * 1024
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func ValidateKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("note key is required")
	}

	if len(key) > MaxKeyLength {
		return "", fmt.Errorf("note key is too long (max %d bytes)", MaxKeyLength)
	}

	if strings.Contains(key, "..") || strings.ContainsAny(key, `/\`) || filepath.IsAbs(key) {
		return "", fmt.Errorf("note key must be a slug, not a path")
	}

	if !keyPattern.MatchString(key) {
		return "", fmt.Errorf("note key must start with an ASCII letter or digit and contain only ASCII letters, digits, '.', '_' or '-'")
	}

	return key, nil
}

func ValidateContent(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("note content is required")
	}

	if len([]byte(raw)) > MaxContentBytes {
		return "", fmt.Errorf("note content is too large (max %d bytes)", MaxContentBytes)
	}

	return raw, nil
}

func WriteFile(workspaceRoot string, key string, content string) (string, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "", fmt.Errorf("workspace root is required for note writes")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	idiomDir := filepath.Join(absRoot, ".idiom")
	if err := ensureDirectory(idiomDir, 0o700, ".idiom"); err != nil {
		return "", err
	}

	noteDir := filepath.Join(idiomDir, "notes")
	if err := ensureDirectory(noteDir, 0o700, ".idiom/notes"); err != nil {
		return "", err
	}

	target := filepath.Join(noteDir, key+".md")
	if rel, err := filepath.Rel(noteDir, target); err != nil || rel != key+".md" {
		return "", fmt.Errorf("note path escaped notes directory")
	}

	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("note target is a symlink: %s", filepath.ToSlash(filepath.Join(".idiom", "notes", key+".md")))
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	tmp, err := os.CreateTemp(noteDir, "."+key+"-*.tmp")
	if err != nil {
		return "", err
	}

	tmpPath := tmp.Name()
	cleanup := true

	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}

	if _, err := tmp.WriteString(content + "\n"); err != nil {
		_ = tmp.Close()
		return "", err
	}

	if err := tmp.Close(); err != nil {
		return "", err
	}

	if err := os.Rename(tmpPath, target); err != nil {
		return "", err
	}

	cleanup = false

	return filepath.ToSlash(filepath.Join(".idiom", "notes", key+".md")), nil
}

func WriteAutoFindings(workspaceRoot string, objective string, summaries []string) (string, error) {
	var b strings.Builder

	b.WriteString("# Auto-captured findings\n\n")
	b.WriteString(fmt.Sprintf("Question: %s\n\n", objective))
	b.WriteString("## Inspected paths\n\n")

	for _, summary := range summaries {
		if strings.TrimSpace(summary) != "" {
			b.WriteString(fmt.Sprintf("- %s\n", summary))
		}
	}

	return WriteFile(workspaceRoot, "auto-findings", b.String())
}

func ensureDirectory(path string, perm os.FileMode, label string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; note writes require a real directory", label)
		}

		if !info.IsDir() {
			return fmt.Errorf("%s exists but is not a directory", label)
		}

		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Mkdir(path, perm); err != nil {
		return err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s was not created as a real directory", label)
	}

	return nil
}
