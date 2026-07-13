package intentloop

import (
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/planexecute"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Resume assessment", func() {
	ginkgo.It("reloads an incomplete cycle prompt and resumable child progress", func() {
		root := ginkgo.GinkgoT().TempDir()
		state, identity, prompt := resumeStateWithDispatchedCycle(root)
		state.Cycles[0].PreRunScopedFingerprint = "scope-fingerprint"

		progress := planexecute.NewProgressState("child-1", prompt, state.Cycles[0].CyclePromptPath, nil, nil, testNow())
		progress.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
		progress.Status = plancontract.PlanExecuteProgressStatusRunning
		progress.PlanPath = "plan.json"
		progress.Executor.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"a.go"}
		_, err := planexecute.SaveProgressState(root, &progress)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		assessment, err := AssessResume(ResumeAssessmentInput{
			Root:                     root,
			State:                    state,
			CurrentChildIdentity:     &identity,
			CurrentScopedFingerprint: "scope-fingerprint",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(assessment.Blocked).To(gomega.BeFalse())
		gomega.Expect(assessment.HasIncompleteCycle).To(gomega.BeTrue())
		gomega.Expect(assessment.CanResumeChild).To(gomega.BeTrue())
		gomega.Expect(assessment.CyclePrompt).To(gomega.Equal(prompt))
		gomega.Expect(assessment.ChildResumeID).To(gomega.Equal("child-1"))
		gomega.Expect(assessment.ChildProgressPath).NotTo(gomega.BeEmpty())
	})

	ginkgo.DescribeTable("uses plan-execute resume readiness",
		func(phase plancontract.PlanExecuteProgressPhase, status, executorStatus plancontract.PlanExecuteProgressStatus, wantResume bool) {
			root := ginkgo.GinkgoT().TempDir()
			state, identity, prompt := resumeStateWithDispatchedCycle(root)
			progress := planexecute.NewProgressState("child-1", prompt, state.Cycles[0].CyclePromptPath, nil, nil, testNow())
			progress.Phase = phase
			progress.Status = status
			progress.PlanPath = "plan.json"
			progress.Executor.Status = executorStatus
			progress.ChangedPaths = []string{"a.go"}
			_, err := planexecute.SaveProgressState(root, &progress)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			assessment, err := AssessResume(ResumeAssessmentInput{Root: root, State: state, CurrentChildIdentity: &identity})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(assessment.CanResumeChild).To(gomega.Equal(wantResume), "assessment=%#v", assessment)
		},
		ginkgo.Entry("review repair ready", plancontract.PlanExecuteProgressPhaseReviewRepair, plancontract.PlanExecuteProgressStatusRunning, plancontract.PlanExecuteProgressStatusCompleted, true),
		ginkgo.Entry("planning is not resumable", plancontract.PlanExecuteProgressPhasePlanning, plancontract.PlanExecuteProgressStatusRunning, plancontract.PlanExecuteProgressStatus(""), false),
		ginkgo.Entry("executor in progress is not review resumable", plancontract.PlanExecuteProgressPhaseExecutor, plancontract.PlanExecuteProgressStatusRunning, plancontract.PlanExecuteProgressStatusRunning, false),
	)

	ginkgo.It("blocks a cycle prompt hash mismatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
		promptPath, _, err := SaveCyclePrompt(root, state.SessionID, 1, "changed prompt")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		identity := testChildIdentity()
		state.Cycles = []CycleRecord{{
			CycleNumber: 1, Status: "dispatched", PlanExecuteSessionID: "child-1",
			CyclePromptPath: promptPath, CyclePromptSHA256: sha256Hex("original prompt"), ChildIdentity: &identity,
		}}

		assessment, err := AssessResume(ResumeAssessmentInput{Root: root, State: state, CurrentChildIdentity: &identity})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		expectResumeBlock(assessment, ResumeBlockPromptMismatch)
	})

	ginkgo.It("blocks a child-progress prompt mismatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		state, identity, _ := resumeStateWithDispatchedCycle(root)
		progress := planexecute.NewProgressState("child-1", "different prompt", state.Cycles[0].CyclePromptPath, nil, nil, testNow())
		_, err := planexecute.SaveProgressState(root, &progress)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		assessment, err := AssessResume(ResumeAssessmentInput{Root: root, State: state, CurrentChildIdentity: &identity})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		expectResumeBlock(assessment, ResumeBlockPromptMismatch)
	})

	ginkgo.It("blocks a child identity mismatch and identifies the field", func() {
		root := ginkgo.GinkgoT().TempDir()
		state, saved, _ := resumeStateWithDispatchedCycle(root)
		current := saved
		current.ToolManifestSHA256 = "different-tools"

		assessment, err := AssessResume(ResumeAssessmentInput{Root: root, State: state, CurrentChildIdentity: &current})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		expectResumeBlock(assessment, ResumeBlockChildIdentityMismatch)
		gomega.Expect(assessment.BlockDetail).To(gomega.Equal("tool_manifest_sha256"))
	})

	ginkgo.It("blocks external scoped changes", func() {
		root := ginkgo.GinkgoT().TempDir()
		state, identity, _ := resumeStateWithDispatchedCycle(root)
		state.Cycles[0].PostRunScopedFingerprint = "previous"

		assessment, err := AssessResume(ResumeAssessmentInput{
			Root: root, State: state, CurrentChildIdentity: &identity, CurrentScopedFingerprint: "current",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		expectResumeBlock(assessment, ResumeBlockExternalScopedChange)
	})

	ginkgo.It("ignores a completed last cycle", func() {
		_, _, incomplete := IncompleteCycle(IntentLoopState{Cycles: []CycleRecord{{Status: "completed"}}})
		gomega.Expect(incomplete).To(gomega.BeFalse())
	})

	ginkgo.It("reports no incomplete cycle for empty state", func() {
		assessment, err := AssessResume(ResumeAssessmentInput{Root: ginkgo.GinkgoT().TempDir(), State: IntentLoopState{}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(assessment.HasIncompleteCycle).To(gomega.BeFalse())
		gomega.Expect(assessment.Blocked).To(gomega.BeFalse())
	})

	ginkgo.It("blocks an unsafe cycle-prompt path", func() {
		root := ginkgo.GinkgoT().TempDir()
		identity := testChildIdentity()
		state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
		state.Cycles = []CycleRecord{{
			CycleNumber: 1, Status: "dispatched", PlanExecuteSessionID: "child-1",
			CyclePromptPath: "../outside.md", CyclePromptSHA256: sha256Hex("prompt"), ChildIdentity: &identity,
		}}

		assessment, err := AssessResume(ResumeAssessmentInput{Root: root, State: state, CurrentChildIdentity: &identity})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		expectResumeBlock(assessment, ResumeBlockPromptMismatch)
	})
})

func resumeStateWithDispatchedCycle(root string) (IntentLoopState, ChildRunIdentity, string) {
	prompt := "cycle prompt"
	state := NewState("resume-session", "original", IntentLoopContract{}, 3, testNow())
	promptPath, promptHash, err := SaveCyclePrompt(root, state.SessionID, 1, prompt)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	identity := testChildIdentity()
	state.Cycles = []CycleRecord{{
		CycleNumber: 1, Status: "dispatched", PlanExecuteSessionID: "child-1",
		CyclePromptPath: promptPath, CyclePromptSHA256: promptHash, ChildIdentity: &identity,
	}}
	return state, identity, prompt
}

func expectResumeBlock(assessment ResumeAssessment, reason string) {
	gomega.Expect(assessment.Blocked).To(gomega.BeTrue(), "assessment=%#v", assessment)
	gomega.Expect(assessment.BlockReason).To(gomega.Equal(reason))
}

func testChildIdentity() ChildRunIdentity {
	return ChildRunIdentity{
		WorkspaceRootSHA256:        "root",
		ScopeContractSHA256:        "scope",
		VerificationCommandsSHA256: "commands",
		ToolManifestSHA256:         "tools",
		BackendProfile:             "profile",
		PlannerModel:               "planner",
		ExecutorModel:              "executor",
	}
}
