package safepath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("handle-bound paths", func() {
	ginkgo.It("reads a normal file through its validated handle", func() {
		root := ginkgo.GinkgoT().TempDir()
		filePath := filepath.Join(root, "nested", "source.go")
		mustWriteFile(filePath, "package source\n")

		data, err := ReadFileUnderRoot(root, filePath, "test file", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(data)).To(gomega.Equal("package source\n"))
	})

	ginkgo.It("opens a directory through a validated root handle", func() {
		root := ginkgo.GinkgoT().TempDir()
		directory := filepath.Join(root, "nested")
		gomega.Expect(os.Mkdir(directory, 0o755)).To(gomega.Succeed())

		opened, err := OpenRootUnderRoot(root, directory, "test directory", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer opened.Close()

		info, err := opened.Stat(".")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(info.IsDir()).To(gomega.BeTrue())
	})

	ginkgo.It("reads directory entries through the validated directory handle", func() {
		root := ginkgo.GinkgoT().TempDir()
		directory := filepath.Join(root, "nested")
		mustWriteFile(filepath.Join(directory, "z_source.go"), "package source\n")
		mustWriteFile(filepath.Join(directory, "a_source.go"), "package source\n")

		entries, err := ReadDirUnderRoot(root, directory, "test directory", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(2))
		gomega.Expect(entries[0].Name()).To(gomega.Equal("a_source.go"))
		gomega.Expect(entries[1].Name()).To(gomega.Equal("z_source.go"))
	})

	ginkgo.It("reads the authority root through its validated handle", func() {
		root := ginkgo.GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "z_source.go"), "package source\n")
		mustWriteFile(filepath.Join(root, "a_source.go"), "package source\n")

		entries, err := ReadDirUnderRoot(root, root, "test workspace", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(2))
		gomega.Expect(entries[0].Name()).To(gomega.Equal("a_source.go"))
		gomega.Expect(entries[1].Name()).To(gomega.Equal("z_source.go"))
	})

	ginkgo.It("walks descendants through opened directory handles in path order", func() {
		root := ginkgo.GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "nested", "source.go"), "package source\n")
		mustWriteFile(filepath.Join(root, "a.go"), "package source\n")

		var paths []string
		err := WalkDirUnderRoot(root, root, "test workspace", func(path string, entry os.DirEntry, walkErr error) error {
			gomega.Expect(walkErr).NotTo(gomega.HaveOccurred())
			paths = append(paths, path)
			return nil
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(paths).To(gomega.Equal([]string{
			root,
			filepath.Join(root, "a.go"),
			filepath.Join(root, "nested"),
			filepath.Join(root, "nested", "source.go"),
		}))
	})

	ginkgo.It("preserves not-exist errors for missing target parents and roots", func() {
		root := ginkgo.GinkgoT().TempDir()

		_, err := OpenFileUnderRoot(root, filepath.Join(root, "missing-ranker", "weights.json"), "ranker weights", Options{})
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(errors.Is(err, fs.ErrNotExist)).To(gomega.BeTrue())

		missingRoot := filepath.Join(root, "missing-workspace")
		_, err = OpenFileUnderRoot(missingRoot, filepath.Join(missingRoot, "weights.json"), "ranker weights", Options{})
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(errors.Is(err, fs.ErrNotExist)).To(gomega.BeTrue())
	})
})
