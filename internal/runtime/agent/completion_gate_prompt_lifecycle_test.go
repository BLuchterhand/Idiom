package agent

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Completion-gate prompt lifecycle", ginkgo.Label("completion_recovery_state"), func() {
	ginkgo.It("resolves pathless missing evidence only when objective answerability advances", func() {
		const (
			targetPath    = "internal/feature/mode.go"
			unrelatedPath = "docs/unrelated.md"
		)
		state := runnerRunState{runtimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			Objective:         "What does internal/feature/mode.go define?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{workspace.ToolNameReadFile},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
		}}
		assessment := sourceGroundingMissingEvidenceAssessment("objective evidence is still missing")
		state.recordCompletionGateRejection(annotateSourceGroundingAssessment(workspace.ToolResult{
			Tool:    completionGateToolName,
			Summary: "Completion gate rejected missing evidence",
			Error:   assessment.Message(),
		}, assessment))

		gomega.Expect(state.completionGateRecovery.ActiveViolation.PendingObligationIDs).To(gomega.Equal(
			[]string{completionGateObligationReadOnlyAnswerability},
		))
		gomega.Expect(state.setCompletionGateRecoveryMessage(assessment.RecoveryPrompt(""))).NotTo(gomega.BeEmpty())

		unrelated := readFileSourceWindowResult(unrelatedPath, 1, 5, 5, "# Release notes\n")
		state.collectedResults = append(state.collectedResults, unrelated)
		progress := state.observeCompletionGateEvidenceProgress(
			workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: unrelatedPath},
			unrelated,
		)
		gomega.Expect(progress.MadeProgress()).To(gomega.BeFalse())
		gomega.Expect(state.completionGateRecovery.hasActiveViolation()).To(gomega.BeTrue())

		target := readFileSourceWindowResult(targetPath, 1, 5, 5, "package feature\n\nconst StartupMode = \"safe\"\n")
		gomega.Expect(genericReadOnlyEvidenceReady(state.runtimeBrief, state.collectedResults)).To(gomega.BeFalse())
		state.collectedResults = append(state.collectedResults, target)
		progress = state.observeCompletionGateEvidenceProgress(
			workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: targetPath},
			target,
		)

		gomega.Expect(progress.SatisfiedObligationIDs).To(gomega.Equal(
			[]string{completionGateObligationReadOnlyAnswerability},
		))
		gomega.Expect(progress.PendingObligationIDs).To(gomega.BeEmpty())
		gomega.Expect(state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
		gomega.Expect(state.completionGateRecovery.TotalAttempts).To(gomega.Equal(1))
		for _, message := range state.turnMessages {
			gomega.Expect(strings.TrimSpace(message.Content)).NotTo(gomega.HavePrefix(completionGateRecoveryMessageTag))
		}
	})

	ginkgo.It("projects guidance only while the typed violation remains active", func() {
		state := runnerRunState{}
		paths := []string{"internal/a.go", "internal/b.go"}
		state.recordCompletionGateRejection(completionGateRejectionWithRecovery(
			"Mutation completion gate rejected final answer",
			"required edit paths remain",
			newCompletionGateRecoveryEnvelope(
				completionGateInvariantMutationRequiredPaths,
				"required_edit_paths_remaining",
				completionGateViolationMutation,
				paths,
			),
		))

		gomega.Expect(state.completionGateRecovery.hasActiveViolation()).To(gomega.BeTrue())
		gomega.Expect(state.setCompletionGateRecoveryMessage("write both required paths")).NotTo(gomega.BeEmpty())

		firstRequest, firstResult := committedMutationResultForTest(paths[0])
		progress := state.observeCompletionGateEvidenceProgress(
			firstRequest,
			firstResult,
		)
		gomega.Expect(progress.PendingContractPaths).To(gomega.Equal([]string{paths[1]}))
		gomega.Expect(state.completionGateRecovery.hasActiveViolation()).To(gomega.BeTrue())
		gomega.Expect(state.setCompletionGateRecoveryMessage("write the remaining required path")).NotTo(gomega.BeEmpty())

		secondRequest, secondResult := committedMutationResultForTest(paths[1])
		progress = state.observeCompletionGateEvidenceProgress(
			secondRequest,
			secondResult,
		)
		gomega.Expect(progress.PendingContractPaths).To(gomega.BeEmpty())
		gomega.Expect(state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
		gomega.Expect(state.setCompletionGateRecoveryMessage("stale heuristic recovery")).To(gomega.BeEmpty())
		gomega.Expect(state.completionGateRecovery.TotalAttempts).To(gomega.Equal(1))

		for _, message := range state.turnMessages {
			gomega.Expect(strings.TrimSpace(message.Content)).NotTo(gomega.HavePrefix(completionGateRecoveryMessageTag))
		}
	})
})
