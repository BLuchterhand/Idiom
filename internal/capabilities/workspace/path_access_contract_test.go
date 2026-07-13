package workspace

import (
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("workspace path containment", func() {
	ginkgo.It("rejects an in-workspace path whose resolved target escapes through a symlink", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package secret\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		access := NewPathAccess(config.Workspace{Root: root, CWD: root})
		_, err := access.ResolvePath(filepath.Join("linked", "secret.go"), "")
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("escapes workspace root through a symlink")))
	})

	ginkgo.It("preserves an in-workspace symlink whose resolved target remains in the workspace", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "docs"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("guide\n"), 0o644)).To(gomega.Succeed())
		if err := os.Symlink("docs", filepath.Join(root, "linked-docs")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		access := NewPathAccess(config.Workspace{Root: root, CWD: root})
		resolved, err := access.ResolvePath(filepath.Join("linked-docs", "guide.md"), "")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(resolved).To(gomega.Equal(filepath.Join(root, "linked-docs", "guide.md")))
	})
})
