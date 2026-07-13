package refine

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("cleanup stale refine state removes old title dirs", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	root := ginkgo.GinkgoT().TempDir()
	refineDir := filepath.Join(root, ".idiom", "refine")
	oldDir := filepath.Join(refineDir, "old-task", "iter-1")
	freshDir := filepath.Join(refineDir, "fresh-task", "iter-1")

	gomega.Expect(os.MkdirAll(oldDir, 0o755)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(freshDir, 0o755)).To(gomega.Succeed())

	oldFile := filepath.Join(oldDir, "draft.md")
	freshFile := filepath.Join(freshDir, "draft.md")

	gomega.Expect(os.WriteFile(oldFile, []byte("old"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(freshFile, []byte("fresh"), 0o644)).To(gomega.Succeed())

	// Backdate the old file beyond the threshold (10 days).
	old := time.Now().Add(-10 * 24 * time.Hour)
	gomega.Expect(os.Chtimes(oldFile, old, old)).To(gomega.Succeed())

	var logBuf bytes.Buffer

	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)

	_, err := os.Stat(filepath.Join(refineDir, "old-task"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "old-task title directory should be removed")
	_, err = os.Stat(freshFile)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "fresh-task should be preserved")
})
var _ = ginkgo.It("cleanup stale refine state preserves active titles with mixed ages", func() {
	// A title with ANY recent file survives. Backdating one iter's
	// files doesn't trigger removal as long as a later iter is fresh.
	root := ginkgo.GinkgoT().TempDir()
	refineDir := filepath.Join(root, ".idiom", "refine")
	mixed := filepath.Join(refineDir, "mixed-task")

	for _, d := range []string{filepath.Join(mixed, "iter-1"), filepath.Join(mixed, "iter-2")} {
		gomega.Expect(os.MkdirAll(d, 0o755)).To(gomega.Succeed())
	}

	oldFile := filepath.Join(mixed, "iter-1", "draft.md")
	freshFile := filepath.Join(mixed, "iter-2", "draft.md")

	gomega.Expect(os.WriteFile(oldFile, []byte("x"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(freshFile, []byte("y"), 0o644)).To(gomega.Succeed())

	old := time.Now().Add(-10 * 24 * time.Hour)
	gomega.Expect(os.Chtimes(oldFile, old, old)).To(gomega.Succeed())

	var logBuf bytes.Buffer

	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)

	_, err := os.Stat(mixed)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mixed-task should survive because iter-2 is recent")
})
var _ = ginkgo.It("cleanup stale refine state missing dir is no op", func() {
	root := ginkgo.GinkgoT().TempDir()

	var logBuf bytes.Buffer

	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)

	gomega.Expect(logBuf.String()).NotTo(gomega.ContainSubstring("refine cleanup:"))
})
var _ = ginkgo.It("cleanup stale refine state removes empty title dirs", func() {
	root := ginkgo.GinkgoT().TempDir()
	empty := filepath.Join(root, ".idiom", "refine", "empty-title")

	gomega.Expect(os.MkdirAll(empty, 0o755)).To(gomega.Succeed())

	var logBuf bytes.Buffer

	cleanupStaleRefineState(root, 7*24*time.Hour, &logBuf)

	_, err := os.Stat(empty)
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "empty title directory should be removed")
})
