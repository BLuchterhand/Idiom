//go:build windows

package safepath

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("walk guarantees the listing-based walk relies on", func() {
	ginkgo.It("reports a junction to outside the root but never descends into it", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "inside", "workspace.txt"), "workspace\n")
		mustWriteFile(filepath.Join(outside, "secret.txt"), "outside\n")
		requireCanonicalWindowsFilesystem(root)
		mustMakeDirectoryJunction(filepath.Join(root, "escape"), outside)

		var seen []string
		var junctionType fs.FileMode
		err := WalkDirUnderRoot(root, root, "test workspace", func(path string, entry fs.DirEntry, walkErr error) error {
			gomega.Expect(walkErr).NotTo(gomega.HaveOccurred())
			seen = append(seen, filepath.Base(path))
			if entry.Name() == "escape" {
				junctionType = entry.Type()
				gomega.Expect(entry.IsDir()).To(gomega.BeFalse())
			}
			return nil
		})

		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(seen).To(gomega.ContainElements("escape", "workspace.txt"))
		gomega.Expect(seen).NotTo(gomega.ContainElement("secret.txt"))
		gomega.Expect(junctionType & (fs.ModeSymlink | fs.ModeIrregular)).NotTo(gomega.BeZero())
	})

	ginkgo.It("refuses a resume cursor that passes through a junction", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		mustWriteFile(filepath.Join(outside, "nested", "secret.txt"), "outside\n")
		requireCanonicalWindowsFilesystem(root)
		mustMakeDirectoryJunction(filepath.Join(root, "escape"), outside)

		for _, dir := range []string{"escape", "escape/nested"} {
			var seen []string
			_, err := WalkDirUnderRootResumable(root, []WalkCursor{{Dir: dir}}, "test workspace", nil, recordingWalk(&seen))
			gomega.Expect(err).To(gomega.HaveOccurred(), dir)
			gomega.Expect(seen).To(gomega.BeEmpty(), dir)
		}
	})

	ginkgo.It("refuses to list a directory handle that is a reparse point", func() {
		root := ginkgo.GinkgoT().TempDir()
		target := filepath.Join(root, "target")
		mustWriteFile(filepath.Join(target, "inside.txt"), "inside\n")
		requireCanonicalWindowsFilesystem(root)
		junction := filepath.Join(root, "alias")
		mustMakeDirectoryJunction(junction, target)
		canonicalRoot, _, err := canonicalExistingRoot(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Open the junction itself, not its target, as a swap after listing
		// would hand the walk; its identity is the junction's own.
		handle := openReparsePointHandle(filepath.Join(canonicalRoot, "alias"))
		file := os.NewFile(uintptr(handle), junction)
		defer file.Close()
		info, err := file.Stat()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		err = validateOpenedFile(file, canonicalRoot, junction, info, "test directory")
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("reparse point")))
	})

	ginkgo.It("compares normalized handle paths exactly against the root", func() {
		gomega.Expect(normalizedPathWithin(`C:\work\root`, `C:\work\root`)).To(gomega.BeTrue())
		gomega.Expect(normalizedPathWithin(`C:\work\root`, `c:\WORK\Root\src\a.cpp`)).To(gomega.BeTrue())
		gomega.Expect(normalizedPathWithin(`C:\`, `C:\work`)).To(gomega.BeTrue())
		gomega.Expect(normalizedPathWithin(`C:\work\root`, `C:\work\root2\a.cpp`)).To(gomega.BeFalse())
		gomega.Expect(normalizedPathWithin(`C:\work\root`, `C:\work`)).To(gomega.BeFalse())
		gomega.Expect(normalizedPathWithin(`C:\work\root`, `D:\work\root\a.cpp`)).To(gomega.BeFalse())
	})
})

func openReparsePointHandle(path string) syscall.Handle {
	const fileFlagOpenReparsePoint = 0x00200000
	name, err := syscall.UTF16PtrFromString(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|fileFlagOpenReparsePoint, 0)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return handle
}
