package app

import (
	"path/filepath"
	"strings"
)

var defaultDebugLoggerSuppressed bool

func suppressDefaultDebugLogger(dir string) bool {
	if !defaultDebugLoggerSuppressed {
		return false
	}

	dir = strings.TrimSpace(dir)
	if dir == "" {
		return true
	}

	return filepath.Clean(dir) == filepath.Clean("logs/conversations")
}
