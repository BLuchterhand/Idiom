package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("External web answerability", func() {
	const sourceURL = "https://example.com/widget-cache"

	brief := func() workerruntime.WorkerRuntimeBrief {
		return workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "How does the widget cache invalidate stale entries?",
		}
	}

	bodyResult := func() workspace.ToolResult {
		return workspace.ToolResult{
			Tool:   "mcp:exa/web_fetch_exa",
			Output: "The widget cache invalidates stale entries when the recorded freshness timestamp expires.",
			Facts: &toolfacts.ToolResultFacts{ExternalWebSources: []toolfacts.ExternalWebSourceEvidence{{
				Kind:         toolfacts.ExternalWebSourceBody,
				URL:          sourceURL,
				CanonicalURL: sourceURL,
			}}},
		}
	}

	ginkgo.It("requires a fetched body and keeps search candidates non-answerable", func() {
		candidate := workspace.ToolResult{
			Tool: "mcp:exa/web_search_exa",
			Facts: &toolfacts.ToolResultFacts{ExternalWebSources: []toolfacts.ExternalWebSourceEvidence{{
				Kind:         toolfacts.ExternalWebSourceCandidate,
				URL:          sourceURL,
				CanonicalURL: sourceURL,
			}}},
		}

		candidateLedger := readOnlyAnswerabilityForResults(brief(), []workspace.ToolResult{candidate})
		gomega.Expect(candidateLedger.Answerable()).To(gomega.BeFalse())

		bodyLedger := readOnlyAnswerabilityForResults(brief(), []workspace.ToolResult{bodyResult()})
		gomega.Expect(bodyLedger.Answerable()).To(gomega.BeTrue())
		gomega.Expect(bodyLedger.RelevantExternalWebSources).To(gomega.Equal([]string{sourceURL}))
	})

	ginkgo.It("requires the final answer to cite the fetched URL", func() {
		results := []workspace.ToolResult{bodyResult()}
		closeout := ReadOnlyCloseout{
			Brief:    brief(),
			Evidence: readOnlyAnswerabilityForResults(brief(), results),
			Results:  results,
		}

		cited := closeout.FinalAnswerDecision("The widget cache invalidates stale entries when the freshness timestamp expires. Source: " + sourceURL)
		gomega.Expect(cited.BlockerKind).To(gomega.BeEmpty())

		uncited := closeout.FinalAnswerDecision("The widget cache invalidates stale entries when the freshness timestamp expires.")
		gomega.Expect(uncited.BlockerKind).To(gomega.Equal("answer_objective_unmet:term_mismatch"))

		briefWithSearch := brief()
		briefWithSearch.AllowedTools = []string{workspace.ToolNameSearchContent}
		runtime := runExecution{}
		uncitedAssessment := runtime.readOnlyEvidenceSourceGroundingAssessment(
			briefWithSearch,
			"The widget cache invalidates stale entries when the freshness timestamp expires.",
			newToolResultEvidenceProjection(results),
		)
		gomega.Expect(uncitedAssessment.BlocksFinalAnswer()).To(gomega.BeTrue())

		citedAssessment := runtime.readOnlyEvidenceSourceGroundingAssessment(
			briefWithSearch,
			"The widget cache invalidates stale entries when the freshness timestamp expires. Source: "+sourceURL,
			newToolResultEvidenceProjection(results),
		)
		gomega.Expect(citedAssessment.BlocksFinalAnswer()).To(gomega.BeFalse())
	})

	ginkgo.It("canonicalizes URL citations before matching source identity", func() {
		ledger := readOnlyAnswerabilityForResults(brief(), []workspace.ToolResult{bodyResult()})
		gomega.Expect(answerCitesRelevantExternalWebSource("See https://EXAMPLE.com/widget-cache#freshness.", ledger)).To(gomega.BeTrue())
		gomega.Expect(answerCitesRelevantExternalWebSource("See https://example.com/other.", ledger)).To(gomega.BeFalse())
	})
})
