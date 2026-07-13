package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Mutation outcome lifecycle", func() {
	implementationBrief := func() workerruntime.WorkerRuntimeBrief {
		return workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			AllowedTools:         []string{workspace.ToolNameEditFile, workspace.ToolNameApplyPatch, workspace.ToolNameSourceSanity, workspace.ToolNameShell},
		}
	}

	ginkgo.It("arms verification and keeps repair writes available after a partial commit", func() {
		brief := implementationBrief()
		partial := workspace.ToolResult{Tool: workspace.ToolNameApplyPatch, Error: "internal/b.go failed after internal/a.go committed"}
		results := []workspace.ToolResult{partial}
		receipts := []workspace.MutationCommitProjection{{
			Effects:          []workspace.MutationCommitEffectProjection{{CanonicalID: "internal/a.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile}},
			PostimageFileIDs: []string{"internal/a.go"}, AffectedFileIDs: []string{"internal/a.go"},
			FailedTargets: []workspace.MutationTargetProjection{{CanonicalID: "internal/b.go", Role: workspace.MutationTargetFile, PreimageClass: workspace.MutationPreimageRequired}},
		}}
		ledger := postWriteVerificationLedgerForReceipts(results, brief, receipts)
		gomega.Expect(ledger.PendingPaths).To(gomega.Equal([]string{"internal/a.go"}))

		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		run.state.mutation.commitReceipts = receipts
		state := run.implementationCompletionState(results)
		gomega.Expect(state.ChangedPaths).To(gomega.Equal([]string{"internal/a.go"}))
		gomega.Expect(state.VerificationPending).To(gomega.BeTrue())
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief:                    brief,
			Results:                  results,
			BaseAllowedTools:         brief.AllowedTools,
			ImplementationCompletion: &state,
		})
		gomega.Expect(contract.AllowedTools).To(gomega.ContainElements(workspace.ToolNameEditFile, workspace.ToolNameApplyPatch, workspace.ToolNameSourceSanity))
		gomega.Expect(contract.TerminalOutcomes).To(gomega.ContainElement("<<QUESTION>>:"))
	})

	ginkgo.It("does not treat an error-free no-op as mutation progress", func() {
		brief := implementationBrief()
		noOp := workspace.ToolResult{
			Tool:    workspace.ToolNameApplyPatch,
			Summary: "apply_patch made no new changes",
			Output:  "all sections were redundant",
		}

		gomega.Expect(toolResultMadeProgress(workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch}, noOp)).To(gomega.BeFalse())
		gomega.Expect(newToolResultEvidenceRecord(noOp).ChangedPathList()).To(gomega.BeEmpty())
		gomega.Expect(postWriteVerificationLedgerForResults([]workspace.ToolResult{noOp}, brief).Pending()).To(gomega.BeFalse())
	})

	ginkgo.It("keeps every source and non-source path pending until relevant final-set evidence arrives", func() {
		brief := implementationBrief()
		write := workspace.ToolResult{
			Tool:         workspace.ToolNameApplyPatch,
			Summary:      "Applied source and documentation edits",
			ChangedPaths: []string{"internal/a.go", "README.md"},
		}
		receipts := []workspace.MutationCommitProjection{{
			Effects: []workspace.MutationCommitEffectProjection{
				{CanonicalID: "internal/a.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile},
				{CanonicalID: "README.md", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile},
			},
			PostimageFileIDs: []string{"internal/a.go", "README.md"},
			AffectedFileIDs:  []string{"internal/a.go", "README.md"},
		}}
		patchHygiene := shellCommandResultForTest("git diff --check -- README.md", "", "")
		patchHygiene.Facts.VerificationEvidence = []toolfacts.VerificationEvidence{{
			Kind:  toolfacts.VerificationEvidenceGitDiffCheck,
			Paths: []string{"README.md"},
		}}
		sourceSanity := workspace.ToolResult{
			Tool:    workspace.ToolNameSourceSanity,
			Summary: "Source sanity passed",
			Output:  "internal/a.go: no warnings or errors via go_diagnostics",
			Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/a.go"}},
		}

		gomega.Expect(postWriteVerificationLedgerForReceipts([]workspace.ToolResult{write}, brief, receipts).PendingPaths).
			To(gomega.ConsistOf("internal/a.go", "README.md"))
		gomega.Expect(postWriteVerificationLedgerForReceipts([]workspace.ToolResult{write, patchHygiene}, brief, receipts).PendingPaths).
			To(gomega.Equal([]string{"internal/a.go"}))
		gomega.Expect(postWriteVerificationLedgerForReceipts([]workspace.ToolResult{write, patchHygiene, sourceSanity}, brief, receipts).Pending()).
			To(gomega.BeFalse())
		gomega.Expect(sourceEditsNeedSemanticVerification(brief, []workspace.ToolResult{write, patchHygiene, sourceSanity})).To(gomega.BeFalse())
	})
})
