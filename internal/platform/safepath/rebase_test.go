package safepath

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("roots spelled through a symlinked ancestor", func() {
	var (
		alias   string
		real    string
		outside string
	)

	ginkgo.BeforeEach(func() {
		base := ginkgo.GinkgoT().TempDir()
		real = filepath.Join(base, "real")
		outside = filepath.Join(base, "outside")
		alias = filepath.Join(base, "alias")

		mustWriteFile(filepath.Join(real, "nested", "source.go"), "package source\n")
		mustWriteFile(filepath.Join(outside, "secret.go"), "package secret\n")
		gomega.Expect(os.Symlink(real, alias)).To(gomega.Succeed())
		gomega.Expect(os.Symlink(outside, filepath.Join(real, "escape"))).To(gomega.Succeed())
		gomega.Expect(os.Symlink(filepath.Join(real, "nested"), filepath.Join(real, "inner"))).To(gomega.Succeed())
	})

	ginkgo.It("opens and walks the root when both are given through the alias", func() {
		opened, err := OpenRootUnderRoot(alias, filepath.Join(alias, "nested"), "test directory", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		opened.Close()

		var paths []string
		err = WalkDirUnderRoot(alias, alias, "test workspace", func(path string, entry os.DirEntry, walkErr error) error {
			if entry.Name() == "escape" {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(paths).To(gomega.ContainElement(filepath.Join(alias, "nested", "source.go")))
	})

	ginkgo.It("still rejects a symlink inside the root that escapes it", func() {
		for _, root := range []string{real, alias} {
			_, err := OpenRootUnderRoot(root, filepath.Join(root, "escape"), "test directory", Options{})
			gomega.Expect(err).To(gomega.HaveOccurred(), root)

			err = WalkDirUnderRoot(root, filepath.Join(root, "escape"), "test workspace", func(string, os.DirEntry, error) error { return nil })
			gomega.Expect(err).To(gomega.HaveOccurred(), root)
		}
	})

	ginkgo.It("treats an in-root symlink alias the same through the canonical and the aliased root", func() {
		_, canonicalErr := OpenRootUnderRoot(real, filepath.Join(real, "inner"), "test directory", Options{})
		_, aliasErr := OpenRootUnderRoot(alias, filepath.Join(alias, "inner"), "test directory", Options{})
		gomega.Expect(aliasErr == nil).To(gomega.Equal(canonicalErr == nil))

		_, rejected := OpenRootUnderRoot(alias, filepath.Join(alias, "inner"), "test directory", Options{RejectSymlinkTarget: true})
		gomega.Expect(rejected).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects a path outside both spellings of the root", func() {
		_, err := OpenRootUnderRoot(alias, outside, "test directory", Options{})
		gomega.Expect(err).To(gomega.HaveOccurred())
	})
})
