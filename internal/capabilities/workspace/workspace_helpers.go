package workspace

import (
	"strings"
)

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

func isNoMatchExit(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "exit status 1")
}
