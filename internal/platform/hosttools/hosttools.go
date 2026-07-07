package hosttools

import (
	"context"
	"os/exec"
	"strings"

	"idiom/internal/platform/commandexec"
)

const DefaultVersionOutputLimit = 64 * 1024

func FirstAvailable(names ...string) string {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		if path, err := exec.LookPath(name); err == nil && path != "" {
			return path
		}
	}

	return ""
}

func VersionOutput(ctx context.Context, path string, maxOutputBytes int) (string, error) {
	if maxOutputBytes <= 0 {
		maxOutputBytes = DefaultVersionOutputLimit
	}

	result := commandexec.Run(ctx, commandexec.Spec{
		Name:           path,
		Args:           []string{"--version"},
		MaxOutputBytes: maxOutputBytes,
		MergeOutput:    true,
	})
	if result.Err != nil {
		return "", result.Err
	}

	return result.Stdout, nil
}

func VersionOutputContains(ctx context.Context, path string, marker string) bool {
	marker = strings.ToLower(strings.TrimSpace(marker))
	if marker == "" {
		return false
	}

	output, err := VersionOutput(ctx, path, DefaultVersionOutputLimit)
	if err != nil {
		return false
	}

	return strings.Contains(strings.ToLower(output), marker)
}
