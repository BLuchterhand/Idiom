package recovery

import (
	"fmt"
	"strings"

	"idiom/internal/platform/safepath"
)

func ensureRecoveryWritePath(root string, fullPath string) error {
	root = strings.TrimSpace(root)
	fullPath = strings.TrimSpace(fullPath)

	if root == "" {
		return fmt.Errorf("workspace root is empty")
	}
	if fullPath == "" {
		return fmt.Errorf("recovery output path is empty")
	}

	absPath, _, err := safepath.UnderRoot(root, fullPath, "recovery output path", safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return err
	}

	if err := safepath.RejectSymlinkTarget(absPath+".tmp", "recovery temp file"); err != nil {
		return err
	}
	return nil
}
