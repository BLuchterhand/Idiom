package intentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	intentloopcontract "idiom/internal/contracts/intentloop"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Durable intent-loop state", func() {
	ginkgo.It("round-trips cycle artifacts and child identity", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("session-1", "original prompt", IntentLoopContract{AtomID: "impl"}, 3, stateTestNow())
		state.CurrentCycle = 1
		state.Cycles = []CycleRecord{{
			CycleNumber:          1,
			PlanExecuteSessionID: "child-1",
			CyclePromptPath:      ".idiom/intent-loop/sessions/session-1/cycle-001.prompt.md",
			CyclePromptSHA256:    "prompt-hash",
			Phase:                "planner",
			Status:               "completed",
			ChangedPaths:         []string{"internal/foo/foo.go"},
			MutationDeltas: []CycleMutationDelta{{
				Path:       "internal/foo/foo.go",
				Kind:       "modify",
				DiffSHA256: "sha256:diff",
				Additions:  3,
				Deletions:  1,
			}},
			RemainingFailures: []EvidenceGap{{
				Requirement: "verification:go vet ./...",
				Status:      "failed",
				Detail:      "vet failed",
				DetailHash:  sha256Prefixed("vet failed with full details"),
			}},
			ChildIdentity: &ChildRunIdentity{
				WorkspaceRootSHA256:        "root",
				ScopeContractSHA256:        "scope",
				VerificationCommandsSHA256: "commands",
				ToolManifestSHA256:         "tools",
				BackendProfile:             "profile",
				PlannerModel:               "planner",
				ExecutorModel:              "executor",
			},
		}}
		state.SeenFailureSets[GapFingerprint(state.Cycles[0].RemainingFailures)] = true

		relPath, err := SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(relPath).To(gomega.Equal(".idiom/intent-loop/sessions/session-1.json"))

		loaded, loadedPath, err := LoadState(root, "session-1")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(loadedPath).To(gomega.Equal(relPath))
		gomega.Expect(loaded.PromptSHA256).To(gomega.Equal(sha256Hex("original prompt")))
		gomega.Expect(loaded.Cycles).To(gomega.HaveLen(1))
		gomega.Expect(loaded.Cycles[0].MutationDeltas).To(gomega.HaveExactElements(gomega.HaveField("DiffSHA256", "sha256:diff")))
		gomega.Expect(loaded.Cycles[0].ChildIdentity).NotTo(gomega.BeNil())
		gomega.Expect(loaded.Cycles[0].ChildIdentity.ExecutorModel).To(gomega.Equal("executor"))
		gomega.Expect(loaded.SeenFailureSets).To(gomega.HaveLen(1))
	})

	ginkgo.It("rejects an invalid session ID before resolving its path", func() {
		_, _, err := StatePath(ginkgo.GinkgoT().TempDir(), "../bad")
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects a persisted state whose embedded session ID differs", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("other-session", "prompt", IntentLoopContract{}, 1, stateTestNow())
		fullPath, _, err := StatePath(root, "requested-session")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
		body, err := json.Marshal(state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.WriteFile(fullPath, append(body, '\n'), 0o600)).To(gomega.Succeed())

		_, _, err = LoadState(root, "requested-session")
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("rejects an unknown loop status", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("bad-status", "prompt", IntentLoopContract{}, 1, stateTestNow())
		state.Status = intentloopcontract.Status("typo")
		writeIntentLoopStateForTest(root, state)

		_, _, err := LoadState(root, state.SessionID)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid intent loop status")))
	})

	ginkgo.It("rejects an unknown cycle status", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("bad-cycle-status", "prompt", IntentLoopContract{}, 1, stateTestNow())
		state.Cycles = []CycleRecord{{CycleNumber: 1, PlanExecuteSessionID: "child", Status: intentloopcontract.CycleStatus("typo")}}
		writeIntentLoopStateForTest(root, state)

		_, _, err := LoadState(root, state.SessionID)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid intent loop cycle status")))
	})

	ginkgo.It("rejects terminal state with a dispatched child", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("terminal-with-dispatched-child", "prompt", IntentLoopContract{}, 1, stateTestNow())
		state.Status = intentloopcontract.StatusCompleted
		state.Cycles = []CycleRecord{{CycleNumber: 1, PlanExecuteSessionID: "child", Status: intentloopcontract.CycleStatusDispatched}}
		writeIntentLoopStateForTest(root, state)

		_, _, err := LoadState(root, state.SessionID)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid intent loop lifecycle")))
	})

	ginkgo.It("rejects multiple dispatched children", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("multiple-dispatched-children", "prompt", IntentLoopContract{}, 2, stateTestNow())
		state.Cycles = []CycleRecord{
			{CycleNumber: 1, PlanExecuteSessionID: "child-1", Status: intentloopcontract.CycleStatusDispatched},
			{CycleNumber: 2, PlanExecuteSessionID: "child-2", Status: intentloopcontract.CycleStatusDispatched},
		}
		writeIntentLoopStateForTest(root, state)

		_, _, err := LoadState(root, state.SessionID)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("multiple dispatched cycles")))
	})

	ginkgo.It("rejects a non-current dispatched child", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("non-current-dispatched-child", "prompt", IntentLoopContract{}, 2, stateTestNow())
		state.Cycles = []CycleRecord{
			{CycleNumber: 1, PlanExecuteSessionID: "child-1", Status: intentloopcontract.CycleStatusDispatched},
			{CycleNumber: 2, PlanExecuteSessionID: "child-2", Status: intentloopcontract.CycleStatusCompleted},
		}
		writeIntentLoopStateForTest(root, state)

		_, _, err := LoadState(root, state.SessionID)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("non-current dispatched cycle")))
	})

	ginkgo.It("refuses to save terminal state with a dispatched child", func() {
		state := NewState("save-terminal-with-dispatched-child", "prompt", IntentLoopContract{}, 1, stateTestNow())
		state.Status = intentloopcontract.StatusBlocked
		state.Cycles = []CycleRecord{{CycleNumber: 1, PlanExecuteSessionID: "child", Status: intentloopcontract.CycleStatusDispatched}}

		_, err := SaveState(ginkgo.GinkgoT().TempDir(), &state)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid intent loop lifecycle")))
	})
})

func writeIntentLoopStateForTest(root string, state IntentLoopState) {
	fullPath, _, err := StatePath(root, state.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.WriteFile(fullPath, append(body, '\n'), 0o600)).To(gomega.Succeed())
}

func stateTestNow() time.Time {
	return time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
}
