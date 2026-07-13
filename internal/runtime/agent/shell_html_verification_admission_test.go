package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Guarded HTML verifier shell admission", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"web/index.html"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	ginkgo.It("admits the workspace-owned canonical HTML verification request", func() {
		request := workspace.ToolRequest{
			Tool:    workspace.ToolNameShell,
			Command: workspace.HTMLParserVerificationCommand("web/index.html"),
		}

		gomega.Expect(workspace.AnalyzeVerificationRequest(request).Attempted).To(gomega.BeTrue())
		_, _, rejected := guardedImplementationShellRejection(request, brief)
		gomega.Expect(rejected).To(gomega.BeFalse())
	})

	ginkgo.It("does not admit verification-looking Python through the canonical exception", func() {
		request := workspace.ToolRequest{
			Tool:    workspace.ToolNameShell,
			Command: `python3 -c 'print("HTML parsed OK: web/index.html")'`,
		}

		gomega.Expect(workspace.AnalyzeVerificationRequest(request).Attempted).To(gomega.BeFalse())
		summary, _, rejected := guardedImplementationShellRejection(request, brief)
		gomega.Expect(rejected).To(gomega.BeTrue())
		gomega.Expect(summary).To(gomega.Equal("Mutation-capable shell rejected"))
	})
})
