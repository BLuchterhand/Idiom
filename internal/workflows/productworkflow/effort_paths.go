package productworkflow

import (
	"path/filepath"
	"strings"
)

func cleanEffortRelativePath(relPath string) (string, bool) {
	if strings.TrimSpace(relPath) == "" || filepath.IsAbs(relPath) {
		return "", false
	}

	clean := filepath.Clean(relPath)
	slash := filepath.ToSlash(clean)
	if slash == "." || slash == ".." || strings.HasPrefix(slash, "../") {
		return "", false
	}

	return clean, true
}
