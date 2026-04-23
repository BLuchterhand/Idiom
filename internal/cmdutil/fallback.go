package cmdutil

import "strings"

// Fallback returns alt if value is whitespace-only, otherwise value.
func Fallback(value string, alt string) string {
	if strings.TrimSpace(value) == "" {
		return alt
	}
	return value
}
