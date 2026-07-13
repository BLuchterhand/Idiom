package safepath

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Safe paths", func() {
	ginkgo.DescribeTable("resolves paths under a workspace root according to missing-path policy",
		func(pathForRoot func(string) string, opts Options, wantRel, wantErr string) {
			root := ginkgo.GinkgoT().TempDir()
			mustWriteFile(filepath.Join(root, "docs", "a.md"), "ok\n")

			_, relative, err := UnderRoot(root, pathForRoot(root), "test path", opts)
			if wantErr != "" {
				gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(wantErr)))
				return
			}

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(relative).To(gomega.Equal(wantRel))
		},
		ginkgo.Entry("relative existing path", func(string) string { return "docs/a.md" }, Options{}, "docs/a.md", ""),
		ginkgo.Entry("absolute existing path", func(root string) string { return filepath.Join(root, "docs", "a.md") }, Options{}, "docs/a.md", ""),
		ginkgo.Entry("empty path", func(string) string { return " " }, Options{}, "", "is empty"),
		ginkgo.Entry("path escaping the root", func(root string) string { return filepath.Join(root, "..", "outside.md") }, Options{}, "", "must stay under workspace root"),
		ginkgo.Entry("allowed missing path", func(string) string { return "docs/new.md" }, Options{AllowMissing: true}, "docs/new.md", ""),
		ginkgo.Entry("rejected missing path", func(string) string { return "docs/new.md" }, Options{}, "", "resolve real test path"),
	)

	ginkgo.It("returns slash-separated relative paths and rejects roots or escapes", func() {
		root := filepath.Clean(ginkgo.GinkgoT().TempDir())
		relative, ok := Rel(root, filepath.Join(root, "dir", "file.txt"))
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(relative).To(gomega.Equal("dir/file.txt"))

		_, ok = Rel(root, root)
		gomega.Expect(ok).To(gomega.BeFalse())
		_, ok = Rel(root, filepath.Dir(root))
		gomega.Expect(ok).To(gomega.BeFalse())
	})

	ginkgo.It("rejects symlinked ancestor directories", func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("symlink creation commonly requires elevated privileges on Windows")
		}

		root := ginkgo.GinkgoT().TempDir()
		targetDir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.Symlink(targetDir, filepath.Join(root, "linkdir"))).To(gomega.Succeed())

		err := RejectSymlinkAncestors(root, filepath.Join(root, "linkdir", "file.txt"), "write path")
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("must not traverse symlinked directories")))
	})

	ginkgo.It("rejects symlink targets while allowing missing direct targets", func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("symlink creation commonly requires elevated privileges on Windows")
		}

		root := ginkgo.GinkgoT().TempDir()
		target := filepath.Join(root, "target.txt")
		mustWriteFile(target, "ok\n")
		link := filepath.Join(root, "link.txt")
		gomega.Expect(os.Symlink(target, link)).To(gomega.Succeed())

		gomega.Expect(RejectSymlinkTarget(link, "write path")).To(
			gomega.MatchError(gomega.ContainSubstring("must not be a symlink")),
		)
		gomega.Expect(RejectSymlinkTarget(filepath.Join(root, "missing.txt"), "write path")).To(gomega.Succeed())
	})
})

func mustWriteFile(path, body string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
}
