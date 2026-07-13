package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("prd draft prompts", func() {
	ginkgo.It("PRDDraftPromptPlanDeterministic", func() {
		root := ginkgo.GinkgoT().TempDir()
		evidence := []EvidenceCard{
			{CardID: "EV-002", Kind: "finding", Claim: "second claim", Confidence: "medium", Freshness: "current"},
			{CardID: "EV-001", Kind: "unsupported_claim", Claim: "first claim", Confidence: "unsupported", Freshness: "stale", Uncertainty: "needs follow-up"},
		}
		tdd := TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001", Type: "acceptance", Behavior: "first behavior", Owner: "test.js", Command: "npm test"}}}
		plan1 := PlanPRDDraftPrompt(root, "prd.prompt.md", "prd.md", evidence, tdd)
		plan2 := PlanPRDDraftPrompt(root, "prd.prompt.md", "prd.md", evidence, tdd)
		gomega.Expect(canonicalPRDDraftPlan(plan1) != canonicalPRDDraftPlan(plan2)).To(gomega.BeFalse(), fmt.Sprintf("expected deterministic PRD draft prompt plan"))
		gomega.Expect(plan1.Command != `idiom artifact --prompt-file prd.prompt.md --output prd.md --json-out events/prd-draft.events.jsonl --report-out reports/prd-draft.report.json --no-tools --mechanical-repair --validator 'idiom product validate-artifact --kind prd --path "$IDIOM_ARTIFACT" --tdd-ledger tdd.json'`).To(gomega.BeFalse(), fmt.Sprintf("unexpected command recipe: %q", plan1.Command))

		prompt := RenderPRDDraftPromptText(plan1)
		assertContains(prompt, "PRD parser/audit contract")
		assertContains(prompt, "Do not use markdown tables for requirements")
		assertContains(prompt, "- **tdd_obligations**:")
		assertContains(prompt, "Inline array fields must use bracket syntax")
		assertContains(prompt, "tdd_obligations values must be IDs")
		assertContains(prompt, "Known TDD obligations (1 total):")
		assertContains(prompt, "TDD-001")
		assertContains(prompt, "Preserve unsupported claims and conflicts as open questions or explicitly labeled inference.")
		assertContains(prompt, "Do not claim acceptance, approval, or final decision status.")
		assertContains(prompt, "Evidence cards (2 total):")
		assertContains(prompt, "- EV-001 |")
	})
	ginkgo.It("PRDDraftPromptPlanRejectsUnsafePaths", func() {
		root := ginkgo.GinkgoT().TempDir()
		plan := PlanPRDDraftPrompt(root, "../bad.prompt.md", "prd.md", nil, TDDLedger{})
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_prd_prompt_path")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_prd_prompt_path finding, got %#v", plan.Findings))

		plan = PlanPRDDraftPrompt(root, "prd.prompt.md", "../prd.md", nil, TDDLedger{})
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_prd_output_path")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_prd_output_path finding, got %#v", plan.Findings))

	})
	ginkgo.It("PRDDraftPromptPlanRejectsSymlinkPromptPath", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		{

			err := os.MkdirAll(filepath.Join(root, "prompts"), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir: %v", err))
		}

		if err := os.Symlink(filepath.Join(outside, "prd.prompt.md"), filepath.Join(root, "prompts", "prd.prompt.md")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		plan := PlanPRDDraftPrompt(root, "prompts/prd.prompt.md", "prd.md", nil, TDDLedger{})
		gomega.Expect(!hasFindingKind(plan.Findings, "research_prompt_symlink")).To(gomega.BeFalse(), fmt.Sprintf("expected research_prompt_symlink finding, got %#v", plan.Findings))

	})
	ginkgo.It("PRDDraftPromptPlanRejectsSymlinkPromptAncestor", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()

		if err := os.Symlink(outside, filepath.Join(root, "prompts")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		plan := PlanPRDDraftPrompt(root, "prompts/prd.prompt.md", "prd.md", nil, TDDLedger{})
		gomega.Expect(!hasFindingKind(plan.Findings, "research_prompt_path_outside_effort")).To(gomega.BeFalse(), fmt.Sprintf("expected research_prompt_path_outside_effort finding, got %#v", plan.Findings))

	})
})

func canonicalPRDDraftPlan(plan PRDDraftPromptPlan) string {
	out := strings.Join([]string{plan.EvidenceLedgerPath, plan.PromptPath, plan.PRDOutputPath, plan.EventsPath, plan.ReportPath, plan.Command}, "|")

	out += "|" + strings.Join(plan.EvidenceCardIDs, ",")
	out += "|" + strings.Join(plan.TDDObligationIDs, ",")

	for _, summary := range plan.EvidenceSummaries {
		out += "|" + summary.CardID + ":" + summary.Kind + ":" + summary.Claim
	}

	for _, finding := range plan.Findings {
		out += "|" + finding.Kind + ":" + finding.File + ":" + finding.Severity
	}

	return out
}
