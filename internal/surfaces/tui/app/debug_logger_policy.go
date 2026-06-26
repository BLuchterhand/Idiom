package app

import (
	"path/filepath"
	"strings"

	"idiom/internal/platform/util"
)

func suppressDebugLoggerForTestProcess(dir string) bool {
	if !util.RunningUnderGoTest() {
		return false
	}

	dir = strings.TrimSpace(dir)
	if dir == "" {
		return true
	}

	return filepath.Clean(dir) == filepath.Clean("logs/conversations")
}
