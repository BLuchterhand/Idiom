package safepath

import (
	"errors"
	"fmt"
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

	ginkgo.It("reads a file through an established root handle", func() {
		root := ginkgo.GinkgoT().TempDir()
		filePath := filepath.Join(root, "nested", "source.go")
		mustWriteFile(filePath, "package source\n")

		canonical, err := CanonicalExistingPath(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		opened, err := OpenRootPath(canonical, "test root")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer opened.Close()

		data, err := ReadFileFromRoot(opened, canonical, filepath.ToSlash(filepath.Join("nested", "source.go")), "test file", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(data)).To(gomega.Equal("package source\n"))
	})

	ginkgo.It("rejects a symlinked file even when its target stays in the root", func() {
		root := ginkgo.GinkgoT().TempDir()
		target := filepath.Join(root, "real.h")
		link := filepath.Join(root, "alias.h")
		mustWriteFile(target, "int real;\n")
		if err := os.Symlink(target, link); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		canonical, err := CanonicalExistingPath(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		opened, err := OpenRootPath(canonical, "test root")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer opened.Close()

		_, err = ReadFileFromRoot(opened, canonical, "alias.h", "test file", Options{
			RejectSymlinkAncestors: true,
			RejectSymlinkTarget:    true,
		})
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects a symlinked ancestor before opening its file", func() {
		root := ginkgo.GinkgoT().TempDir()
		realDir := filepath.Join(root, "real")
		aliasDir := filepath.Join(root, "alias")
		gomega.Expect(os.Mkdir(realDir, 0o755)).To(gomega.Succeed())
		mustWriteFile(filepath.Join(realDir, "source.h"), "int source;\n")
		if err := os.Symlink(realDir, aliasDir); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		canonical, err := CanonicalExistingPath(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		opened, err := OpenRootPath(canonical, "test root")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer opened.Close()

		_, err = ReadFileFromRoot(opened, canonical, filepath.ToSlash(filepath.Join("alias", "source.h")), "test file", Options{
			RejectSymlinkAncestors: true,
			RejectSymlinkTarget:    true,
		})
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects a FIFO instead of reading it", func() {
		root := ginkgo.GinkgoT().TempDir()
		fifo := filepath.Join(root, "source.h")
		if err := createNamedFIFO(fifo); err != nil {
			ginkgo.Skip(fmt.Sprintf("named FIFOs unavailable: %v", err))
		}

		canonical, err := CanonicalExistingPath(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		opened, err := OpenRootPath(canonical, "test root")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer opened.Close()

		_, err = ReadFileFromRoot(opened, canonical, "source.h", "test file", Options{})
		gomega.Expect(err).To(gomega.HaveOccurred())
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
