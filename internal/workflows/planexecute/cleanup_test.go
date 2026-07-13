package planexecute

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("cleanup stale plan artifacts", func() {
	root := ginkgo.GinkgoT().TempDir()
	dir := filepath.Join(root, ".idiom", "plans")

	gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed(), "mkdir plan directory")

	stale := filepath.Join(dir, "stale.plan.json")
	recent := filepath.Join(dir, "recent.plan.json")
	pointer := filepath.Join(dir, "last.plan.json")

	for _, path := range []string{stale, recent, pointer} {
		gomega.Expect(os.WriteFile(path, []byte("{}"), 0o644)).To(gomega.Succeed(), "seed %s", path)
	}

	old := time.Now().Add(-10 * 24 * time.Hour)
	young := time.Now().Add(-1 * time.Hour)
	ancient := time.Now().Add(-365 * 24 * time.Hour)

	gomega.Expect(os.Chtimes(stale, old, old)).To(gomega.Succeed(), "age stale plan")
	gomega.Expect(os.Chtimes(recent, young, young)).To(gomega.Succeed(), "age recent plan")
	gomega.Expect(os.Chtimes(pointer, ancient, ancient)).To(gomega.Succeed(), "age plan pointer")

	var logBuf bytes.Buffer

	cleanupStalePlanArtifacts(root, 7*24*time.Hour, &logBuf)

	_, err := os.Stat(stale)
	gomega.Expect(err).To(gomega.MatchError(os.ErrNotExist), "stale plan should have been removed")

	_, err = os.Stat(recent)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "recent plan should have survived")

	_, err = os.Stat(pointer)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "last.plan.json must survive regardless of mtime")
})

var _ = ginkgo.It("find latest plan artifact rejects symlinked plan dir", func() {
	root := ginkgo.GinkgoT().TempDir()
	outside := ginkgo.GinkgoT().TempDir()

	gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed(), "mkdir .idiom")
	gomega.Expect(os.WriteFile(filepath.Join(outside, "latest.plan.json"), []byte(`{"session_id":"s1"}`), 0o644)).To(
		gomega.Succeed(),
		"seed outside plan",
	)
	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "plans")); err != nil {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}

	_, err := findLatestPlanArtifactForTest(root, time.Time{}, "s1")
	gomega.Expect(err).To(
		gomega.MatchError(gomega.ContainSubstring("symlink")),
		"FindLatestPlanArtifact should reject symlinked plan dir",
	)
})
