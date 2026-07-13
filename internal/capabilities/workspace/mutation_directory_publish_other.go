//go:build !darwin && !linux

package workspace

import (
	"os"
)

const workspaceMutationNoReplaceSupported = false

func publishWorkspaceMutationPath(_ *os.Root, _, _ string) error {
	return errWorkspaceMutationDirectoryPublishUnsupported
}
