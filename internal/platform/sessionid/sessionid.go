package sessionid

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/platform/shortid"
)

const MaxLength = 128

// New returns a safe single-component session id suitable for filesystem paths.
func New() string {
	return shortid.New()
}

func Normalize(value string) (string, bool) {
	normalized := strings.TrimSpace(value)
	if err := Validate(normalized); err != nil {
		return "", false
	}
	return normalized, true
}

func Validate(value string) error {
	if value == "" {
		return fmt.Errorf("session id is required")
	}
	if len(value) > MaxLength {
		return fmt.Errorf("invalid session id %q: length exceeds %d characters", value, MaxLength)
	}
	if strings.TrimSpace(value) != value || value == "." || value == ".." || filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) || strings.Contains(value, "..") {
		return fmt.Errorf("invalid session id %q: must be a single safe path component", value)
	}

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("invalid session id %q: allowed characters are A-Z, a-z, 0-9, underscore, and dash", value)
		}
	}

	return nil
}

func Valid(value string) bool {
	return Validate(value) == nil
}
