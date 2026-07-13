package intentloop

import (
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Artifact compaction", func() {
	ginkgo.It("preserves durable evidence and deletes only transient child files", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("cleanup-session", "original", IntentLoopContract{}, 3, staleCleanupTime())
		terminalPath := writeRelativeFile(root, ".idiom/plan-execute/sessions/child-1.terminal.json", `{"status":"completed"}`)
		progressPath := writeRelativeFile(root, ".idiom/plan-execute/progress/child-1.json", `{"status":"running"}`)
		transientPath := writeRelativeFile(root, ".idiom/intent-loop/sessions/cleanup-session/spill/output.tmp", "large duplicate")
		promptPath := writeRelativeFile(root, ".idiom/intent-loop/sessions/cleanup-session/cycle-001.prompt.md", "prompt")
		state.Cycles = []CycleRecord{{
			CycleNumber:            1,
			Status:                 "completed",
			PlanExecuteSessionID:   "child-1",
			CyclePromptPath:        promptPath,
			TerminalCompletionPath: terminalPath,
			ResumeStatePath:        progressPath,
			ChangedPaths:           []string{"internal/workflows/intentloop/resume.go"},
			FailureKind:            "verification_failed",
			MutationDeltas: []CycleMutationDelta{{
				Path:       "internal/workflows/intentloop/resume.go",
				Kind:       "modify",
				DiffSHA256: "sha256:diff",
			}},
			ChildIdentity: &ChildRunIdentity{BackendProfile: "profile"},
		}}

		report, err := CompactChildArtifacts(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(fileMissing(root, terminalPath)).To(gomega.BeFalse())
		gomega.Expect(fileMissing(root, progressPath)).To(gomega.BeFalse())
		gomega.Expect(fileMissing(root, promptPath)).To(gomega.BeFalse())
		gomega.Expect(fileMissing(root, transientPath)).To(gomega.BeTrue())
		gomega.Expect(report.ChildSnapshots).To(gomega.HaveLen(1))
		gomega.Expect(report.ChildSnapshots[0].TerminalCompletionSHA256).NotTo(gomega.BeEmpty())
		gomega.Expect(report.ChildSnapshots[0].ResumeStateSHA256).NotTo(gomega.BeEmpty())
		gomega.Expect(report.PreservedPaths).To(gomega.ContainElements(terminalPath, progressPath))
		gomega.Expect(report.DeletedPaths).To(gomega.ContainElement(".idiom/intent-loop/sessions/cleanup-session/spill"))
		gomega.Expect(state.Cycles[0].ChangedPaths[0]).NotTo(gomega.BeEmpty())
		gomega.Expect(state.Cycles[0].MutationDeltas[0].DiffSHA256).NotTo(gomega.BeEmpty())
		gomega.Expect(state.Cycles[0].ChildIdentity).NotTo(gomega.BeNil())
	})
})

func writeRelativeFile(root, relPath, body string) string {
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed(), "mkdir %s", relPath)
	gomega.Expect(os.WriteFile(fullPath, []byte(body), 0o600)).To(gomega.Succeed(), "write %s", relPath)
	return relPath
}

func fileMissing(root string, relPath string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath)))
	return os.IsNotExist(err)
}

func staleCleanupTime() time.Time {
	return time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
}
