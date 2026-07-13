//go:build linux

package workspace

import (
	"os"

	"golang.org/x/sys/unix"
)

const workspaceMutationNoReplaceSupported = true

func publishWorkspaceMutationPath(parent *os.Root, temporary, target string) error {
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return unix.Renameat2(int(directory.Fd()), temporary, int(directory.Fd()), target, unix.RENAME_NOREPLACE)
}
