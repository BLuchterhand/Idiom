package productworkflow

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("ordinary effort artifact writes", func() {
	ginkgo.DescribeTable("rejects root replacement at each final write boundary", ginkgo.Serial, func(
		boundary effortArtifactWriteBoundary,
		relPath string,
	) {
		parent := ginkgo.GinkgoT().TempDir()
		effortRoot := filepath.Join(parent, "effort")
		renamedRoot := filepath.Join(parent, "renamed-effort")
		gomega.Expect(os.Mkdir(effortRoot, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(effortRoot, productWorkflowMutationLockRelPath), nil, 0o600)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(effortRoot, "original-marker.txt"), []byte("original\n"), 0o644)).To(gomega.Succeed())
		if boundary == effortArtifactWriteBeforeAtomicReplacement {
			gomega.Expect(os.Mkdir(filepath.Join(effortRoot, "generated"), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(filepath.Join(effortRoot, relPath), []byte("original artifact\n"), 0o644)).To(gomega.Succeed())
		}
		originalBefore := snapshotEffortArtifactTree(effortRoot)

		originalHook := effortArtifactWriteBoundaryHook
		ginkgo.DeferCleanup(func() { effortArtifactWriteBoundaryHook = originalHook })
		swapped := false
		var replacementBefore map[string]effortArtifactTreeEntry
		effortArtifactWriteBoundaryHook = func(observed effortArtifactWriteBoundary) {
			if observed != boundary || swapped {
				return
			}
			gomega.Expect(os.Rename(effortRoot, renamedRoot)).To(gomega.Succeed())
			gomega.Expect(os.Mkdir(effortRoot, 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(filepath.Join(effortRoot, "replacement-marker.txt"), []byte("replacement\n"), 0o644)).To(gomega.Succeed())
			if boundary == effortArtifactWriteBeforeAtomicReplacement {
				gomega.Expect(os.Mkdir(filepath.Join(effortRoot, "generated"), 0o755)).To(gomega.Succeed())
				gomega.Expect(os.WriteFile(filepath.Join(effortRoot, relPath), []byte("replacement artifact\n"), 0o644)).To(gomega.Succeed())
			}
			replacementBefore = snapshotEffortArtifactTree(effortRoot)
			swapped = true
		}

		err := writeEffortArtifactAtomic(effortRoot, relPath, []byte("new artifact\n"), 0o644)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("path no longer names the locked effort root")))
		gomega.Expect(swapped).To(gomega.BeTrue())
		gomega.Expect(snapshotEffortArtifactTree(renamedRoot)).To(gomega.Equal(originalBefore), "the opened original tree must remain byte-for-byte unchanged")
		gomega.Expect(snapshotEffortArtifactTree(effortRoot)).To(gomega.Equal(replacementBefore), "the replacement tree must remain byte-for-byte unchanged")
	},
		ginkgo.Entry(
			"before preparing a missing parent",
			effortArtifactWriteBeforeParentPreparation,
			filepath.Join("new-parent", "artifact.md"),
		),
		ginkgo.Entry(
			"before atomically replacing an existing artifact",
			effortArtifactWriteBeforeAtomicReplacement,
			filepath.Join("generated", "artifact.md"),
		),
	)

	ginkgo.It("preserves opened-root authority for an enclosing transaction", func() {
		parent := ginkgo.GinkgoT().TempDir()
		effortRoot := filepath.Join(parent, "effort")
		renamedRoot := filepath.Join(parent, "renamed-effort")
		gomega.Expect(os.Mkdir(effortRoot, 0o755)).To(gomega.Succeed())
		root, err := os.OpenRoot(effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(root.Close)

		gomega.Expect(os.Rename(effortRoot, renamedRoot)).To(gomega.Succeed())
		gomega.Expect(os.Mkdir(effortRoot, 0o755)).To(gomega.Succeed())
		gomega.Expect(writeEffortArtifactAtomicRoot(root, "transaction-artifact.md", []byte("transaction\n"), 0o644)).To(gomega.Succeed())

		gomega.Expect(filepath.Join(renamedRoot, "transaction-artifact.md")).To(gomega.BeAnExistingFile())
		gomega.Expect(filepath.Join(effortRoot, "transaction-artifact.md")).NotTo(gomega.BeAnExistingFile())
	})
})

type effortArtifactTreeEntry struct {
	Mode os.FileMode
	Body string
}

func snapshotEffortArtifactTree(root string) map[string]effortArtifactTreeEntry {
	ginkgo.GinkgoHelper()

	snapshot := map[string]effortArtifactTreeEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := effortArtifactTreeEntry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.Body = string(body)
		}
		snapshot[filepath.ToSlash(relPath)] = entry

		return nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return snapshot
}
