package agent

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("runner artifact path intersection", func() {
	ginkgo.It("preserves HTML verification coverage in the completion ledger", func() {
		result := workspace.ToolResult{
			Tool: workspace.ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{
				VerificationEvidence: []toolfacts.VerificationEvidence{{
					Kind:  toolfacts.VerificationEvidenceHTMLParse,
					Paths: []string{"web/index.html"},
				}},
			},
		}

		evidence := completionGateVerificationEvidenceForResult(result, []string{"web/index.html"}, nil)
		gomega.Expect(evidence.Successful).To(gomega.BeTrue(), fmt.Sprintf("HTML evidence should remain successful: %#v", evidence))
		gomega.Expect(evidence.CoveredPaths).To(gomega.Equal([]string{"web/index.html"}))
		gomega.Expect(evidence.ClearsAll).To(gomega.BeTrue())
	})

	ginkgo.It("preserves non-source and extensionless mutation paths", func() {
		got := intersectRunnerPaths(
			[]string{"web/index.html", "LICENSE", "internal/runtime/agent/runner.go"},
			[]string{"LICENSE", "web/index.html"},
		)

		gomega.Expect(got).To(gomega.ConsistOf("web/index.html", "LICENSE"))
	})

	ginkgo.It("keeps source-only evidence sets source filtered", func() {
		got := readOnlyPathSet([]string{"internal/runtime/agent/runner.go", "web/index.html", "LICENSE"})

		gomega.Expect(got).To(gomega.Equal(map[string]struct{}{
			"internal/runtime/agent/runner.go": {},
		}))
	})

	ginkgo.It("allows a fresh exact HTML verifier through the in-flight gate", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		}
		req := workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: workspace.HTMLParserVerificationCommand("web/index.html")}

		state := newRunnerExecutionState(false, 0)
		state.writesSinceVerification = 2
		gomega.Expect(workspace.AnalyzeVerificationRequest(req).Attempted).To(gomega.BeTrue(), fmt.Sprintf("fresh HTML request was not classified as verification: %#v", req))
		gomega.Expect(state.shouldRequireVerification(req, brief)).To(gomega.BeFalse(), "the exact model-authored HTML verifier must remain runnable after writes")

		spoof := workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: `python3 -c "print('HTML parsed OK: web/index.html')"`}
		gomega.Expect(workspace.AnalyzeVerificationRequest(spoof).Attempted).To(gomega.BeFalse())
		gomega.Expect(state.shouldRequireVerification(spoof, brief)).To(gomega.BeTrue(), "an HTML-like print command must not bypass the verification gate")
	})
})
