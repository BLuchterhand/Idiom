package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("ADR draft persistence", func() {
	ginkgo.It("replaces matching decisions, preserves unchanged bodies, and appends new decisions in index order", func() {
		root := ginkgo.GinkgoT().TempDir()
		_, err := InitializeEffort(CreateEffortOptions{
			EffortRoot: root,
			EffortID:   "effort-adr-merge",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		draftContext, err := LoadPRDDraftContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		prd := validPRDDoc()
		prd.FrontMatter.PRDID = draftContext.Identity.PRDID
		prd.FrontMatter.TaskID = draftContext.Identity.TaskID
		prd.FrontMatter.CreatedAt = draftContext.Identity.CreatedAt
		intake, err := PersistPRDDraft(PRDDraftPersistenceRequest{
			EffortRoot:         root,
			Document:           prd,
			ExpectedGeneration: draftContext.Generation,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(intake.Passed).To(gomega.BeTrue())
		_, err = SubmitPRDForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, intake, err = ApprovePRDGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(intake.Passed).To(gomega.BeTrue())

		existing := ADRIndex{
			SchemaVersion: "idiom.adr_index.v1",
			EffortID:      "effort-adr-merge",
			ADRs: []ADRIndexEntry{
				{ADRID: "ADR-001", Title: "Original one", Status: string(ArtifactStateDraft), Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}},
				{ADRID: "ADR-002", Title: "Unchanged two", Status: string(ArtifactStateDraft), Path: "ADR-002.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}},
			},
		}
		original := validADRDoc()
		original.BodyMarkdown = validADRBodyWithDecision("Use the original decision.")
		unchanged := validADRDoc()
		unchanged.FrontMatter.ADRID = "ADR-002"
		unchanged.BodyMarkdown = validADRBodyWithDecision("Keep the unchanged decision.")
		generationContext, err := LoadADRGenerationContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = PersistADRDrafts(ADRDraftPersistenceRequest{
			EffortRoot:         root,
			Documents:          []ADRDocument{original, unchanged},
			IndexEntries:       existing.ADRs,
			ExpectedGeneration: generationContext.Generation,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		refined := validADRDoc()
		refined.BodyMarkdown = validADRBodyWithDecision("Use the refined decision.")
		third := validADRDoc()
		third.FrontMatter.ADRID = "ADR-003"
		third.BodyMarkdown = validADRBodyWithDecision("Use the newly appended decision.")
		refinementContext, err := LoadADRDraftContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		result, err := PersistADRDrafts(ADRDraftPersistenceRequest{
			EffortRoot: root,
			Documents:  []ADRDocument{refined, third},
			IndexEntries: []ADRIndexEntry{
				{ADRID: "ADR-001", Title: "Refined one", Status: string(ArtifactStateDraft), Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}},
				{ADRID: "ADR-003", Title: "New three", Status: string(ArtifactStateDraft), Path: "ADR-003.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}},
			},
			ExpectedGeneration: refinementContext.Generation,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.IndexEntries).To(gomega.HaveExactElements(
			gomega.And(gomega.HaveField("ADRID", "ADR-001"), gomega.HaveField("Title", "Refined one")),
			gomega.And(gomega.HaveField("ADRID", "ADR-002"), gomega.HaveField("Title", "Unchanged two")),
			gomega.And(gomega.HaveField("ADRID", "ADR-003"), gomega.HaveField("Title", "New three")),
		))
		gomega.Expect(result.Documents).To(gomega.HaveExactElements(
			gomega.And(gomega.HaveField("FrontMatter", gomega.HaveField("ADRID", "ADR-001")), gomega.HaveField("BodyMarkdown", gomega.ContainSubstring("refined decision"))),
			gomega.And(gomega.HaveField("FrontMatter", gomega.HaveField("ADRID", "ADR-002")), gomega.HaveField("BodyMarkdown", gomega.ContainSubstring("unchanged decision"))),
			gomega.And(gomega.HaveField("FrontMatter", gomega.HaveField("ADRID", "ADR-003")), gomega.HaveField("BodyMarkdown", gomega.ContainSubstring("newly appended decision"))),
		))

		indexBody, err := os.ReadFile(filepath.Join(root, "adrs", "index.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		persistedIndex, err := ParseADRIndexJSON(indexBody)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(persistedIndex.ADRs).To(gomega.HaveExactElements(
			gomega.HaveField("ADRID", "ADR-001"),
			gomega.HaveField("ADRID", "ADR-002"),
			gomega.HaveField("ADRID", "ADR-003"),
		))
		unchangedBody, err := os.ReadFile(filepath.Join(root, "adrs", "ADR-002.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(unchangedBody)).To(gomega.ContainSubstring("Keep the unchanged decision."))
	})
})

func validADRBodyWithDecision(decision string) string {
	return fmt.Sprintf(`## Context

The workflow needs a durable decision.

## Decision

%s

## Alternatives Considered

Keep the previous decision.

## Evidence

- E1: The requirement contract.

## Consequences

The decision remains auditable.

## Compatibility and Migration

No compatibility change is required.

## TDD Obligations

- T1: Exercise the decision path.

## Rollback

Restore the prior ADR body.

## Open Questions

- None.
`, decision)
}
