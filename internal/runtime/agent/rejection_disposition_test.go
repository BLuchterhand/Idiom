package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Rejected mutation disposition", func() {
	const target = "internal/feature/status.go"

	request := workspace.ToolRequest{
		Tool:    workspace.ToolNameEditFile,
		Path:    target,
		OldText: `const Status = "draft"`,
		NewText: `const Status = "ready"`,
	}

	ginkgo.It("records paths only for dispositions that preserve mutation intent", func() {
		deferred := resultWithRejectedRequestTarget(request, resultWithToolRejectionDisposition(workspace.ToolResult{
			Tool:    request.Tool,
			Summary: "phase-local denial",
			Error:   "not callable in this phase",
			Facts: &toolfacts.ToolResultFacts{
				RejectedWritePaths: []string{target},
			},
		}, toolfacts.ToolRejectionDeferredByPhase))

		gomega.Expect(deferred.Facts).NotTo(gomega.BeNil())
		gomega.Expect(deferred.Facts.RejectedWritePaths).To(gomega.Equal([]string{target}))

		for _, disposition := range []toolfacts.ToolRejectionDisposition{
			toolfacts.ToolRejectionInvalidRequest,
			toolfacts.ToolRejectionPolicyDenied,
		} {
			rejected := resultWithRejectedRequestTarget(request, resultWithToolRejectionDisposition(workspace.ToolResult{
				Tool:    request.Tool,
				Summary: "denied",
				Error:   "request was not authorized",
				Facts: &toolfacts.ToolResultFacts{
					RejectedWritePaths: []string{target},
				},
			}, disposition))

			gomega.Expect(rejected.Facts.RejectionDisposition).To(gomega.Equal(disposition))
			gomega.Expect(rejected.Facts.RejectedWritePaths).To(gomega.BeEmpty())
		}

		unclassified := resultWithRejectedRequestTarget(request, workspace.ToolResult{
			Tool:    request.Tool,
			Summary: "failed",
			Error:   "unclassified failure",
		})

		gomega.Expect(unclassified.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionNotRetained))
		gomega.Expect(unclassified.Facts.RejectedWritePaths).To(gomega.BeEmpty())
	})

	ginkgo.It("resolves a rejected path only through a later successful change", func() {
		deferred := resultWithRejectedRequestTarget(request, resultWithToolRejectionDisposition(workspace.ToolResult{
			Tool:    request.Tool,
			Summary: "phase-local denial",
			Error:   "not callable in this phase",
			Facts: &toolfacts.ToolResultFacts{
				RejectedWritePaths: []string{target},
			},
		}, toolfacts.ToolRejectionDeferredByPhase))

		results := []workspace.ToolResult{
			{Tool: request.Tool, ChangedPaths: []string{target}},
			deferred,
			{Tool: workspace.ToolNameWriteFile, ChangedPaths: []string{"docs/notes.md"}},
			{Tool: workspace.ToolNameSourceSanity, Summary: "source sanity passed"},
		}
		gomega.Expect(unresolvedRejectedMutationPaths(results, nil)).To(gomega.Equal([]string{target}))

		_, committed := committedMutationResultForTest(target)
		results = append(results, committed)
		gomega.Expect(unresolvedRejectedMutationPaths(results, nil)).To(gomega.BeEmpty())
	})

	ginkgo.It("excludes policy denials from the unresolved mutation ledger", func() {
		denied := resultWithToolRejectionDisposition(workspace.ToolResult{
			Tool:    request.Tool,
			Summary: "scope denied",
			Error:   "target is outside the authorized scope",
			Facts: &toolfacts.ToolResultFacts{
				RejectedWritePaths: []string{target},
			},
		}, toolfacts.ToolRejectionPolicyDenied)

		gomega.Expect(unresolvedRejectedMutationPaths([]workspace.ToolResult{denied}, nil)).To(gomega.BeEmpty())
	})

	ginkgo.It("does not infer retained mutation intent from untyped failure prose", func() {
		untyped := workspace.ToolResult{
			Tool:    workspace.ToolNameEditFile,
			Summary: "edit failed for internal/target.go",
			Output:  "retry internal/target.go",
			Error:   "could not update internal/target.go",
		}
		gomega.Expect(unresolvedRejectedMutationPaths([]workspace.ToolResult{untyped}, nil)).To(gomega.BeEmpty())
	})

	ginkgo.It("retains phase-local intent only after workspace admission stamps canonical paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		request := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}
		phaseResult := func() workspace.ToolResult {
			return resultWithToolRejectionDisposition(workspace.ToolResult{Tool: request.Tool, Summary: "phase denied", Error: "not callable in this phase"}, toolfacts.ToolRejectionDeferredByPhase)
		}

		allowedManager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		allowed := newRunnerRunFixture(&Runner{Workspace: allowedManager, RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation", Contract: "implementation_result"}})
		admitted := allowed.bindRetainedMutationRejection(request, phaseResult())
		gomega.Expect(admitted.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionDeferredByPhase))
		gomega.Expect(admitted.Facts.RejectedWritePaths).To(gomega.Equal([]string{"target.go"}))

		askManager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, ToolRules: map[string][]config.ToolRule{
			workspace.ToolNameEditFile: {{Pattern: "target.go", Decision: "ask"}},
		}})
		ask := newRunnerRunFixture(&Runner{Workspace: askManager, RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation", Contract: "implementation_result"}})
		approvalRequired := ask.bindRetainedMutationRejection(request, phaseResult())
		gomega.Expect(approvalRequired.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionNotRetained))
		gomega.Expect(approvalRequired.Facts.RejectedWritePaths).To(gomega.BeEmpty())

		deniedManager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, ToolRules: map[string][]config.ToolRule{
			workspace.ToolNameEditFile: {{Pattern: "target.go", Decision: "deny"}},
		}})
		denied := newRunnerRunFixture(&Runner{Workspace: deniedManager, RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation", Contract: "implementation_result"}})
		policyDenied := denied.bindRetainedMutationRejection(request, phaseResult())
		gomega.Expect(policyDenied.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionNotRetained))
		gomega.Expect(policyDenied.Facts.RejectedWritePaths).To(gomega.BeEmpty())

		bound := newRunnerRunFixture(&Runner{Workspace: allowedManager, RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", RequiredEditPaths: []string{"target.go"},
		}})
		outsideRequest := request
		outsideRequest.Path = "outside.go"
		outside := bound.bindRetainedMutationRejection(outsideRequest, phaseResult())
		gomega.Expect(outside.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionNotRetained))
		gomega.Expect(outside.Facts.RejectedWritePaths).To(gomega.BeEmpty())

		unstamped := resultWithRejectedRequestTarget(request, phaseResult())
		gomega.Expect(unstamped.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionDeferredByPhase))
		gomega.Expect(unstamped.Facts.RejectedWritePaths).To(gomega.BeEmpty(), "the recorder must never synthesize retained authority")
	})

	ginkgo.It("clears only an exact phase-local fingerprint after the same tool becomes callable", func() {
		state := newRunnerRequestBudgetState(0)
		fingerprint := toolRequestKey(request)
		result := resultWithToolRejectionDisposition(workspace.ToolResult{
			Tool:    request.Tool,
			Summary: "arbitrary presentation text",
			Error:   "arbitrary presentation error",
		}, toolfacts.ToolRejectionDeferredByPhase)
		state.recordRejectedFingerprint(fingerprint, request, result)
		state.fingerprint.rejectedRetries[fingerprint] = 1

		gomega.Expect(state.clearStaleToolSurfaceRejection(request, fingerprint, []string{workspace.ToolNameSourceSanity})).To(gomega.BeFalse())
		gomega.Expect(state.clearStaleToolSurfaceRejection(
			workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch},
			fingerprint,
			[]string{workspace.ToolNameEditFile},
		)).To(gomega.BeFalse())
		gomega.Expect(state.clearStaleToolSurfaceRejection(request, fingerprint, []string{workspace.ToolNameEditFile})).To(gomega.BeTrue())
		_, rejectedPresent := state.fingerprint.rejected[fingerprint]
		_, retryPresent := state.fingerprint.rejectedRetries[fingerprint]
		gomega.Expect(rejectedPresent).To(gomega.BeFalse())
		gomega.Expect(retryPresent).To(gomega.BeFalse())
	})

	ginkgo.It("uses the typed phase disposition for early retry handling", func() {
		state := newRunnerRequestBudgetState(0)
		fingerprint := toolRequestKey(request)
		state.recordRejectedFingerprint(fingerprint, request, resultWithToolRejectionDisposition(workspace.ToolResult{
			Tool:    request.Tool,
			Summary: "presentation text with no recognized literal",
			Error:   "phase denied",
		}, toolfacts.ToolRejectionDeferredByPhase))

		decision := state.earlyRejectedFingerprintDecision(request, fingerprint)
		gomega.Expect(decision.Handled).To(gomega.BeTrue())
		gomega.Expect(strings.TrimSpace(decision.Nudge)).NotTo(gomega.BeEmpty())
	})

})
