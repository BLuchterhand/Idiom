package safepath

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func writeResumeTree(root string) {
	for _, dir := range []string{"a", "a/x", "a/x/deep", "a/y", "b", "c/z"} {
		mustWriteFile(filepath.Join(root, filepath.FromSlash(dir), "f.txt"), dir+"\n")
		mustWriteFile(filepath.Join(root, filepath.FromSlash(dir), "g.txt"), dir+"\n")
	}
	mustWriteFile(filepath.Join(root, "top.txt"), "top\n")
}

func recordingWalk(paths *[]string) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, err error) error {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		*paths = append(*paths, path)
		return nil
	}
}

var _ = ginkgo.Describe("resumable walk", func() {
	ginkgo.It("resumes an interrupted walk without visiting a finished entry or directory again", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeResumeTree(root)

		var full []string
		gomega.Expect(WalkDirUnderRoot(root, root, "test workspace", recordingWalk(&full))).To(gomega.Succeed())

		var resumed []string
		var pending []WalkCursor
		calls := 0
		for {
			descents := 0
			stopAfterOne := func() bool {
				descents++
				return descents > 1
			}
			left, err := WalkDirUnderRootResumable(root, pending, "test workspace", stopAfterOne, recordingWalk(&resumed))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			calls++
			if left == nil {
				break
			}
			gomega.Expect(calls).To(gomega.BeNumerically("<", 20))
			pending = left
		}

		gomega.Expect(calls).To(gomega.BeNumerically(">", 2))
		gomega.Expect(resumed).To(gomega.Equal(full))
	})

	ginkgo.It("stops a resumed walk at SkipAll without walking the cursors after it", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeResumeTree(root)

		var seen []string
		pending := []WalkCursor{{Dir: "a/x"}, {Dir: "a", After: "x"}, {Dir: ".", After: "a"}}
		left, err := WalkDirUnderRootResumable(root, pending, "test workspace", nil, func(path string, entry fs.DirEntry, err error) error {
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			seen = append(seen, filepath.ToSlash(path))
			if !entry.IsDir() {
				return filepath.SkipAll
			}
			return nil
		})

		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(left).To(gomega.BeNil())
		gomega.Expect(seen).To(gomega.HaveLen(2), "a/x/deep then its first file, nothing after")
	})

	ginkgo.It("skips a resume cursor whose directory was deleted and walks the rest", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeResumeTree(root)
		gomega.Expect(os.RemoveAll(filepath.Join(root, "a", "x"))).To(gomega.Succeed())

		var seen []string
		pending := []WalkCursor{{Dir: "a/x/deep"}, {Dir: "a/x", After: "deep"}, {Dir: "a", After: "x"}, {Dir: ".", After: "a"}}
		left, err := WalkDirUnderRootResumable(root, pending, "test workspace", nil, recordingWalk(&seen))

		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(left).To(gomega.BeNil())
		gomega.Expect(seen).To(gomega.ContainElements(
			filepath.Join(root, "a", "y", "f.txt"), filepath.Join(root, "b", "g.txt"), filepath.Join(root, "c", "z", "f.txt")))
	})

	ginkgo.It("fails closed when a resume cursor's directory is now something else", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeResumeTree(root)
		gomega.Expect(os.RemoveAll(filepath.Join(root, "a", "x"))).To(gomega.Succeed())
		mustWriteFile(filepath.Join(root, "a", "x"), "now a file\n")

		var seen []string
		pending := []WalkCursor{{Dir: "a/x/deep"}, {Dir: "a", After: "x"}}
		_, err := WalkDirUnderRootResumable(root, pending, "test workspace", nil, recordingWalk(&seen))

		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(seen).To(gomega.BeEmpty())
	})

	// The two cursor-refusal specs below (and the junction one in
	// walk_reparse_windows_test.go) stay green with cursorDirectory and
	// openWalkRoot's real-directory check both weakened: os.Root and the opened
	// handle's identity check refuse the escape on their own (G907 break checks).
	ginkgo.It("refuses a resume cursor that leaves the root", func() {
		base := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(base, "root")
		writeResumeTree(root)
		mustWriteFile(filepath.Join(base, "outside", "secret.txt"), "outside\n")

		var seen []string
		for _, cursor := range []WalkCursor{{Dir: "../outside"}, {Dir: filepath.Join(base, "outside")}, {Dir: "a", After: "../x"}} {
			_, err := WalkDirUnderRootResumable(root, []WalkCursor{cursor}, "test workspace", nil, recordingWalk(&seen))
			gomega.Expect(err).To(gomega.HaveOccurred(), cursor.Dir)
		}
		gomega.Expect(seen).To(gomega.BeEmpty())
	})

	ginkgo.It("reads Info from the entry itself, not from the directory listing", func() {
		root := ginkgo.GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "growing.log"), "one\n")

		var size int64
		err := WalkDirUnderRoot(root, root, "test workspace", func(path string, entry fs.DirEntry, err error) error {
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			if entry.Name() != "growing.log" {
				return nil
			}
			// The entry grows after the directory was listed; Info must see it.
			gomega.Expect(os.WriteFile(path, []byte("one\ntwo\n"), 0o600)).To(gomega.Succeed())
			info, infoErr := entry.Info()
			gomega.Expect(infoErr).NotTo(gomega.HaveOccurred())
			size = info.Size()
			return nil
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(size).To(gomega.Equal(int64(len("one\ntwo\n"))))
	})
})
