//go:build windows

package safepath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("handle-bound path swaps", func() {
	ginkgo.It("rejects external file symlinks and directory junctions", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		mustWriteFile(filepath.Join(outside, "secret.txt"), "outside data\n")
		requireCanonicalWindowsFilesystem(root)
		outsideAlias := filepath.Join(outside, "workspace-alias")
		if output, err := makeDirectoryJunction(outsideAlias, root); err != nil {
			ginkgo.Skip(fmt.Sprintf("could not create a junction: %s", strings.TrimSpace(output)))
		}
		_, err := OpenRootUnderRoot(root, outsideAlias, "test directory", Options{})
		gomega.Expect(err).To(gomega.HaveOccurred())

		fileLink := filepath.Join(root, "secret.txt")
		if err := os.Symlink(filepath.Join(outside, "secret.txt"), fileLink); err == nil {
			_, readErr := ReadFileUnderRoot(root, fileLink, "test file", Options{})
			gomega.Expect(readErr).To(gomega.HaveOccurred())
		} else {
			ginkgo.By("file symlink unavailable: " + err.Error())
		}

		junction := filepath.Join(root, "linked")
		if output, err := makeDirectoryJunction(junction, outside); err != nil {
			ginkgo.Skip(fmt.Sprintf("could not create a junction: %s", strings.TrimSpace(output)))
		}
		_, err = ReadFileUnderRoot(root, filepath.Join(junction, "secret.txt"), "test file", Options{})
		gomega.Expect(err).To(gomega.HaveOccurred())
		_, err = OpenRootUnderRoot(root, junction, "test directory", Options{})
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects an external junction installed after validation and root open", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		inside := filepath.Join(root, "inside")
		mustWriteFile(filepath.Join(inside, "secret.txt"), "workspace data\n")
		mustWriteFile(filepath.Join(outside, "secret.txt"), "outside data\n")
		requireCanonicalWindowsFilesystem(root)

		target, err := prepareOpenTarget(root, filepath.Join(inside, "secret.txt"), "test file", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		rootHandle, err := openTargetRoot(target)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer rootHandle.Close()

		moved := filepath.Join(root, "inside-original")
		gomega.Expect(os.Rename(inside, moved)).To(gomega.Succeed())
		if output, err := makeDirectoryJunction(inside, outside); err != nil {
			ginkgo.Skip(fmt.Sprintf("could not create a junction: %s", strings.TrimSpace(output)))
		}

		opened, err := openPreparedFile(rootHandle, target)
		if opened != nil {
			_ = opened.Close()
		}
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects a directory replaced by an external junction before descent", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		child := filepath.Join(root, "child")
		mustWriteFile(filepath.Join(child, "workspace.txt"), "workspace data\n")
		mustWriteFile(filepath.Join(outside, "external.txt"), "outside data\n")
		requireCanonicalWindowsFilesystem(root)

		swapped := false
		sawExternal := false
		err := WalkDirUnderRoot(root, root, "test workspace", func(path string, entry os.DirEntry, walkErr error) error {
			if filepath.Base(path) == "external.txt" {
				sawExternal = true
			}
			if !swapped && path == child && entry != nil && entry.IsDir() {
				swapped = true
				if err := os.Rename(child, filepath.Join(root, "child-original")); err != nil {
					return err
				}
				if output, err := makeDirectoryJunction(child, outside); err != nil {
					ginkgo.Skip(fmt.Sprintf("could not create a junction: %s", strings.TrimSpace(output)))
				}
			}
			return nil
		})

		gomega.Expect(swapped).To(gomega.BeTrue())
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(sawExternal).To(gomega.BeFalse())
	})
})

func makeDirectoryJunction(path string, target string) (string, error) {
	command := `mklink /J "` + path + `" "` + target + `"`
	output, err := exec.Command("cmd.exe", "/c", command).CombinedOutput()
	return string(output), err
}
