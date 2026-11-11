//go:build !windows

package safepath

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("handle-bound path swaps", func() {
	ginkgo.It("rejects a path outside the workspace even when it resolves to the root", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		alias := filepath.Join(outside, "workspace-alias")
		gomega.Expect(os.Symlink(root, alias)).To(gomega.Succeed())

		opened, err := OpenRootUnderRoot(root, alias, "test directory", Options{})
		if opened != nil {
			_ = opened.Close()
		}
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects an external symlink installed after validation and root open", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		inside := filepath.Join(root, "inside")
		mustWriteFile(filepath.Join(inside, "secret.txt"), "workspace data\n")
		mustWriteFile(filepath.Join(outside, "secret.txt"), "outside data\n")

		target, err := prepareOpenTarget(root, filepath.Join(inside, "secret.txt"), "test file", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		rootHandle, err := openTargetRoot(target)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer rootHandle.Close()

		moved := filepath.Join(root, "inside-original")
		gomega.Expect(os.Rename(inside, moved)).To(gomega.Succeed())
		gomega.Expect(os.Symlink(outside, inside)).To(gomega.Succeed())

		opened, err := openPreparedFile(rootHandle, target)
		if opened != nil {
			_ = opened.Close()
		}
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects a directory replaced by an external symlink before descent", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		child := filepath.Join(root, "child")
		mustWriteFile(filepath.Join(child, "workspace.txt"), "workspace data\n")
		mustWriteFile(filepath.Join(outside, "external.txt"), "outside data\n")

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
				return os.Symlink(outside, child)
			}
			return nil
		})

		gomega.Expect(swapped).To(gomega.BeTrue())
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(sawExternal).To(gomega.BeFalse())
	})
})
