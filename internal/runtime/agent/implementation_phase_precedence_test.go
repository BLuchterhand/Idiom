package agent

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	contractcompile "idiom/internal/contracts/compile"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Implementation phase precedence", func() {
	baseBrief := func() workerruntime.WorkerRuntimeBrief {
		return workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "apply_patch", "task_ledger", "source_sanity", "shell"},
		}
	}
	receiptBackedState := func(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, failedTargets ...string) (*runExecution, implementationCompletionState) {
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		run.state.mutation.bound = true
		run.state.mutation.canonicalScopeProjection.RequiredCoverageIDs = append([]string(nil), brief.RequiredEditPaths...)
		receipt := workspace.MutationCommitProjection{
			Effects:          []workspace.MutationCommitEffectProjection{{CanonicalID: "internal/a.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile}},
			PostimageFileIDs: []string{"internal/a.go"},
			AffectedFileIDs:  []string{"internal/a.go"},
		}
		for _, target := range failedTargets {
			receipt.FailedTargets = append(receipt.FailedTargets, workspace.MutationTargetProjection{
				CanonicalID: target,
				Role:        workspace.MutationTargetFile,
			})
		}
		run.state.mutation.commitReceipts = []workspace.MutationCommitProjection{receipt}
		return run, run.implementationCompletionState(results)
	}

	ginkgo.It("keeps task-ledger progress callable ahead of pending verification", func() {
		brief := baseBrief()
		brief.RequiredEditPaths = []string{"internal/a.go", "internal/b.go"}
		_, state := receiptBackedState(brief, nil)
		state.OpenTaskLedgerObligationIDs = []string{"finish-tests"}
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief: brief, BaseAllowedTools: brief.AllowedTools, ImplementationCompletion: &state,
		})

		gomega.Expect(contract.Phase).To(gomega.Equal("task_ledger_recovery"))
		gomega.Expect(contract.AllowedTools).To(gomega.ContainElement(workspace.ToolNameTaskLedger))
		gomega.Expect(strings.Join(contract.Constraints, "\n")).To(gomega.ContainSubstring("finish-tests"))
	})

	ginkgo.It("keeps exact rejected and snippet obligations ahead of pending verification", func() {
		brief := baseBrief()
		brief.RequiredEditPaths = []string{"internal/a.go", "internal/b.go"}
		for _, test := range []struct {
			name       string
			mutate     func(*implementationCompletionState)
			constraint string
		}{
			{name: "rejected path", mutate: func(state *implementationCompletionState) {
				state.UnresolvedRejectedMutationPaths = []string{"internal/b.go"}
			}, constraint: "unresolved_rejected_mutation_paths: internal/b.go"},
			{name: "required snippet", mutate: func(state *implementationCompletionState) {
				state.MissingRequiredSnippetIDs = []string{"status-handler"}
			}, constraint: "missing_required_edit_snippets: status-handler"},
		} {
			ginkgo.By(test.name)
			_, state := receiptBackedState(brief, nil)
			test.mutate(&state)
			contract := deriveTurnPhaseContract(TurnPhaseContractInput{
				Brief: brief, BaseAllowedTools: brief.AllowedTools, ImplementationCompletion: &state,
			})
			gomega.Expect(contract.Phase).To(gomega.Equal("write_recovery"))
			gomega.Expect(contract.AllowedTools).To(gomega.ContainElement(workspace.ToolNameEditFile))
			gomega.Expect(strings.Join(contract.Constraints, "\n")).To(gomega.ContainSubstring(test.constraint))
		}
	})

	ginkgo.It("prioritizes failed verification over a missing required path", func() {
		brief := baseBrief()
		brief.RequiredEditPaths = []string{"internal/a.go", "internal/b.go"}
		failed := shellCommandResultForTest("go test ./internal/...", "", "build failed")
		failed.Facts.DiagnosticEvidencePaths = []string{"internal/a.go"}
		results := []workspace.ToolResult{failed}
		receiptBackedRun, state := receiptBackedState(brief, results)
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief: brief, Results: results, BaseAllowedTools: brief.AllowedTools, ImplementationCompletion: &state,
		})

		gomega.Expect(contract.PhaseID).To(gomega.Equal(contractcompile.TurnPhaseImplementationVerifyReady))
		gomega.Expect(contract.Phase).To(gomega.Equal("verification_repair"))
		gomega.Expect(contract.DerivationReason).To(gomega.Equal("implementation_post_write_verification_failed"))

		readiness := runnerReadinessState("main", brief, results, &state, planThenExecutePathAuthority{})
		gomega.Expect(readiness.ReadyToAnswer).To(gomega.BeFalse())
		gomega.Expect(readiness.ReadyToWrite).To(gomega.BeTrue())
		gomega.Expect(readiness.ReadyToVerify).To(gomega.BeTrue())
		gomega.Expect(readiness.OpenObligations).To(gomega.ContainElement("verification_failed_after_write"))
		gomega.Expect(readiness.FurtherReadsRequire).To(gomega.ContainSubstring("failed verification"))

		prompt, ok := receiptBackedRun.completionGateRecoveryPrompt(results)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(prompt).To(gomega.ContainSubstring("verification"))
		gomega.Expect(prompt).To(gomega.ContainSubstring("failed"))
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("required edit path(s) remain unwritten"))
		gomega.Expect(receiptBackedRun.completionGateRepeatedViolation(results).Kind).To(gomega.Equal(turnViolationCompletionGateVerificationRepeated))
	})

	ginkgo.It("projects write and verification readiness together while required paths remain", func() {
		brief := baseBrief()
		brief.RequiredEditPaths = []string{"internal/a.go", "internal/b.go"}
		var results []workspace.ToolResult
		_, state := receiptBackedState(brief, results)
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief: brief, Results: results, BaseAllowedTools: brief.AllowedTools, ImplementationCompletion: &state,
		})
		gomega.Expect(contract.PhaseID).To(gomega.Equal(contractcompile.TurnPhaseImplementationWriteReady))
		gomega.Expect(contract.DerivationReason).To(gomega.Equal("implementation_required_paths_remaining"))
		gomega.Expect(contract.AllowedTools).To(gomega.ContainElements(workspace.ToolNameReadFile, workspace.ToolNameEditFile))

		readiness := runnerReadinessState("main", brief, results, &state, planThenExecutePathAuthority{})
		gomega.Expect(readiness.ReadyToAnswer).To(gomega.BeFalse())
		gomega.Expect(readiness.ReadyToWrite).To(gomega.BeTrue())
		gomega.Expect(readiness.ReadyToVerify).To(gomega.BeTrue())
		gomega.Expect(readiness.OpenObligations).To(gomega.ContainElements(
			"required_edit_paths_remaining: internal/b.go",
			"verification_pending_after_write",
		))
		gomega.Expect(readiness.FurtherReadsRequire).To(gomega.ContainSubstring("remaining required path"))
	})

	ginkgo.It("keeps permission-authorized inspection callable while verification is pending", func() {
		brief := baseBrief()
		brief.AllowedTools = append(brief.AllowedTools, workspace.ToolNameSearchContent)
		brief.RequiredEditPaths = []string{"internal/a.go"}
		_, state := receiptBackedState(brief, nil)
		inspection := permissionAuthorizedInspectionForTest(brief.AllowedTools...)
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief:                    brief,
			BaseAllowedTools:         brief.AllowedTools,
			ImplementationCompletion: &state,
			InspectionAuthority:      inspection,
		})

		gomega.Expect(contract.PhaseID).To(gomega.Equal(contractcompile.TurnPhaseImplementationVerifyReady))
		gomega.Expect(contract.AllowedTools).To(gomega.ContainElements(workspace.ToolNameReadFile, workspace.ToolNameEditFile, workspace.ToolNameSourceSanity, workspace.ToolNameShell))
		gomega.Expect(contract.ActionPolicy.AllowedActionClasses).To(gomega.ContainElements(actionRead, actionSearch, actionPerEditWrite, actionVerify))
		gomega.Expect(contract.ActionPolicy.InvalidActionClasses).NotTo(gomega.ContainElements(actionRead, actionSearch))
	})

	ginkgo.It("aligns phase readiness recovery and final rejection on unresolved mutation intent", func() {
		brief := baseBrief()
		rejected := resultWithRejectedRequestTarget(
			workspace.ToolRequest{Tool: workspace.ToolNameEditFile, Path: "internal/b.go"},
			resultWithToolRejectionDisposition(workspace.ToolResult{
				Tool:    workspace.ToolNameEditFile,
				Summary: "Tool hidden by active contract",
				Error:   "write was transiently unavailable",
			}, toolfacts.ToolRejectionDeferredByPhase))

		results := []workspace.ToolResult{rejected}
		run, state := receiptBackedState(brief, results, "internal/b.go")

		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief: brief, Results: results, BaseAllowedTools: brief.AllowedTools, ImplementationCompletion: &state,
		})
		gomega.Expect(contract.Phase).To(gomega.Equal("write_recovery"))

		readiness := runnerReadinessState("main", brief, results, &state, planThenExecutePathAuthority{})
		gomega.Expect(readiness.ReadyToWrite).To(gomega.BeTrue())
		gomega.Expect(readiness.ReadyToVerify).To(gomega.BeTrue())
		gomega.Expect(readiness.ReadyToAnswer).To(gomega.BeFalse())
		gomega.Expect(readiness.FurtherReadsRequire).To(gomega.ContainSubstring("unresolved rejected mutation path"))

		prompt, ok := run.completionGateRecoveryPrompt(results)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(prompt).To(gomega.ContainSubstring("internal/b.go"))
		gomega.Expect(prompt).To(gomega.ContainSubstring("write-ready"))

		acceptance := run.evaluateFinalAnswerAcceptance(
			"Changed paths:\n- internal/a.go\n\nVerification: pending.",
			results,
		)
		gomega.Expect(acceptance.Decision).To(gomega.Equal(finalAnswerAcceptanceRejected))
	})

	ginkgo.It("preserves typed rejected intent that has no failed receipt target", func() {
		brief := baseBrief()
		rejected := workspace.ToolResult{
			Tool:  workspace.ToolNameEditFile,
			Error: "write was transiently unavailable before workspace execution",
			Facts: &toolfacts.ToolResultFacts{
				RejectionDisposition: toolfacts.ToolRejectionDeferredByPhase,
				RejectedWritePaths:   []string{"internal/b.go"},
			},
		}
		run, _ := receiptBackedState(brief, []workspace.ToolResult{rejected})
		state := run.implementationCompletionState([]workspace.ToolResult{rejected})

		gomega.Expect(state.UnresolvedRejectedMutationPaths).To(gomega.Equal([]string{"internal/b.go"}))
		gomega.Expect(state.HasPendingRejectedMutation()).To(gomega.BeTrue())
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())
	})
})
