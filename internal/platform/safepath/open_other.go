//go:build !windows

package safepath

import (
	"fmt"
	"os"
)

func validateOpenedFile(file *os.File, _ string, _ string, expected os.FileInfo, label string) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", label, err)
	}
	if expected == nil || !os.SameFile(expected, opened) {
		return fmt.Errorf("%s changed while opening", label)
	}
	return nil
}

func samePath(left string, right string) bool {
	return CleanPath(left) == CleanPath(right)
}
