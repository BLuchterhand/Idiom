package safepath

import (
	"strings"
)

// CleanPath normalizes platform-specific path syntax before containment
// checks. On Windows this also expands existing short-name path components
// and accepts the /c/... form emitted by Git Bash.
func CleanPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return cleanPath(path)
}

// CleanPathChecked normalizes a path after rejecting unsupported platform
// namespace forms that must never participate in containment checks.
func CleanPathChecked(path string) (string, error) {
	if err := validatePathNamespace(strings.TrimSpace(path)); err != nil {
		return "", err
	}
	return CleanPath(path), nil
}

// CanonicalExistingPath returns the filesystem's canonical name for an
// existing path, following symbolic links and other filesystem redirects.
// Callers must still compare the result with an approved root.
func CanonicalExistingPath(path string) (string, error) {
	cleaned, err := CleanPathChecked(path)
	if err != nil {
		return "", err
	}
	return canonicalExistingPath(cleaned)
}
