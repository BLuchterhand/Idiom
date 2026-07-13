package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("adr draft prompts", func() {
	ginkgo.It("ADRDraftPromptPlanDeterministic", func() {
		root := ginkgo.GinkgoT().TempDir()
		evidence := []EvidenceCard{
			{CardID: "EV-002", Kind: "finding", Claim: "second claim", Confidence: "medium", Freshness: "current"},
			{CardID: "EV-001", Kind: "unsupported_claim", Claim: "first claim", Confidence: "unsupported", Freshness: "stale", Uncertainty: "needs follow-up"},
		}
		prd := PRDDocument{Requirements: []PRDRequirement{
			{ID: "REQ-002", Priority: "should", Statement: "second requirement", EvidenceRefs: []string{"EV-002"}, TDDObligations: []string{"TDD-2"}},
			{ID: "REQ-001", Priority: "must", Statement: "first requirement", EvidenceRefs: []string{"EV-001"}, TDDObligations: []string{"TDD-1"}},
		}}
		plan1 := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", evidence, prd)
		plan2 := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", evidence, prd)
		gomega.Expect(canonicalADRDraftPlan(plan1) != canonicalADRDraftPlan(plan2)).To(gomega.BeFalse(), fmt.Sprintf("expected deterministic ADR draft prompt plan"))
		gomega.Expect(plan1.Command != `idiom artifact --prompt-file adrs/adr.prompt.md --output adrs/ADR-draft.md --json-out events/adr-draft.events.jsonl --report-out reports/adr-draft.report.json --no-tools --mechanical-repair --validator 'idiom product validate-artifact --kind adr --path "$IDIOM_ARTIFACT" --prd prd.md --tdd-ledger tdd.json'`).To(gomega.BeFalse(), fmt.Sprintf("unexpected command recipe: %q", plan1.Command))

		prompt := RenderADRDraftPromptText(plan1)
		assertContains(prompt, "ADR parser/audit contract")
		assertContains(prompt, "Keep acceptance metadata fields empty unless a human explicitly provided them")
		assertContains(prompt, "coverage_tier must be one of: documentation, unit, local_low, behavior_standard, contract_high, formal_optional")
		assertContains(prompt, "Do not use priority words such as must, should, or could as coverage tiers")
		assertContains(prompt, "Do not invent prose obligations or new IDs")
		assertContains(prompt, "Preserve every supplied PRD requirement statement semantically")
		assertContains(prompt, "include one concrete fenced code block for each required source file")
		assertContains(prompt, "tdd_obligations=[TDD-1]")
		assertContains(prompt, "Do not claim acceptance, approval, or supersession.")
		assertContains(prompt, "PRD requirements (2 total):")
		assertContains(prompt, "Evidence cards (2 total):")
	})
	ginkgo.It("ADRDraftPromptPlanRejectsUnsafePaths", func() {
		root := ginkgo.GinkgoT().TempDir()
		plan := PlanADRDraftPrompt(root, "../bad.prompt.md", "adrs/ADR-draft.md", nil, PRDDocument{})
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_adr_prompt_path")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_adr_prompt_path finding, got %#v", plan.Findings))

		plan = PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "../bad.md", nil, PRDDocument{})
		gomega.Expect(!hasFindingKind(plan.Findings, "invalid_adr_output_path")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_adr_output_path finding, got %#v", plan.Findings))

	})
	ginkgo.It("ADRDraftPromptPlanRejectsSymlinkPromptPath", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		{

			err := os.MkdirAll(filepath.Join(root, "adrs"), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir: %v", err))
		}

		if err := os.Symlink(filepath.Join(outside, "adr.prompt.md"), filepath.Join(root, "adrs", "adr.prompt.md")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		plan := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", nil, PRDDocument{})
		gomega.Expect(!hasFindingKind(plan.Findings, "research_prompt_symlink")).To(gomega.BeFalse(), fmt.Sprintf("expected research_prompt_symlink finding, got %#v", plan.Findings))

	})
	ginkgo.It("ADRDraftPromptPlanRejectsSymlinkPromptAncestor", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()

		if err := os.Symlink(outside, filepath.Join(root, "adrs")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		plan := PlanADRDraftPrompt(root, "adrs/adr.prompt.md", "adrs/ADR-draft.md", nil, PRDDocument{})
		gomega.Expect(!hasFindingKind(plan.Findings, "research_prompt_path_outside_effort")).To(gomega.BeFalse(), fmt.Sprintf("expected research_prompt_path_outside_effort finding, got %#v", plan.Findings))

	})
})

func canonicalADRDraftPlan(plan ADRDraftPromptPlan) string {
	out := strings.Join([]string{plan.EvidenceLedgerPath, plan.PRDInputPath, plan.PromptPath, plan.ADROutputPath, plan.EventsPath, plan.ReportPath, plan.Command}, "|")

	out += "|" + strings.Join(plan.EvidenceCardIDs, ",")
	out += "|" + strings.Join(plan.PRDRequirementIDs, ",")

	for _, summary := range plan.EvidenceSummaries {
		out += "|" + summary.CardID + ":" + summary.Kind + ":" + summary.Claim
	}

	for _, summary := range plan.PRDRequirementSummary {
		out += "|" + summary.ID + ":" + summary.Priority + ":" + summary.Statement
	}

	for _, finding := range plan.Findings {
		out += "|" + finding.Kind + ":" + finding.File + ":" + finding.Severity
	}

	return out
}
