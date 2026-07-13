package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("research evidence merge", func() {
	ginkgo.It("MergeResearchEvidenceDeterministicOrderingAndDedup", func() {
		existing := []EvidenceCard{
			researchMergeTestCard("EV-002", "technical", "Second claim"),
		}
		additions := []EvidenceCard{
			researchMergeTestCard("EV-001", "north_star", "First claim"),
			researchMergeTestCard("EV-002", "technical", "Second claim"),
		}
		result := MergeResearchEvidence(existing, additions, MergePolicy{})
		gomega.Expect(len(result.Cards) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected 2 merged cards, got %d: %#v", len(result.Cards), result.Cards))
		gomega.Expect(result.Cards[0].CardID).To(gomega.Equal("EV-001"), fmt.Sprintf("expected deterministic card order EV-001, EV-002; got %#v", result.Cards))
		gomega.Expect(result.Cards[1].CardID).To(gomega.Equal("EV-002"), fmt.Sprintf("expected deterministic card order EV-001, EV-002; got %#v", result.Cards))
		gomega.Expect(result.MergedCardIDs).To(gomega.HaveLen(2), fmt.Sprintf("expected deterministic merged IDs, got %#v", result.MergedCardIDs))
		gomega.Expect(result.MergedCardIDs[0]).To(gomega.Equal("EV-001"), fmt.Sprintf("expected deterministic merged IDs, got %#v", result.MergedCardIDs))
		gomega.Expect(result.MergedCardIDs[1]).To(gomega.Equal("EV-002"), fmt.Sprintf("expected deterministic merged IDs, got %#v", result.MergedCardIDs))
		gomega.Expect(len(result.Conflicts) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no conflicts for exact duplicate, got %#v", result.Conflicts))

	})
	ginkgo.It("MergeResearchEvidencePreservesDuplicateCardConflicts", func() {
		existing := []EvidenceCard{
			researchMergeTestCard("EV-001", "north_star", "First claim"),
		}
		additions := []EvidenceCard{
			researchMergeTestCard("EV-001", "technical", "Different claim"),
		}
		result := MergeResearchEvidence(existing, additions, MergePolicy{})
		gomega.Expect(len(result.Cards) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected original card to be retained once, got %#v", result.Cards))
		gomega.Expect(len(result.Conflicts) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected 1 conflict, got %#v", result.Conflicts))

		conflict := result.Conflicts[0]
		gomega.Expect(conflict.Kind).To(gomega.Equal("duplicate_card_id_conflict"), fmt.Sprintf("unexpected conflict: %#v", conflict))
		gomega.Expect(conflict.ClaimKey).To(gomega.Equal("EV-001"), fmt.Sprintf("unexpected conflict: %#v", conflict))
		gomega.Expect(conflict.ResearcherIDs).To(gomega.HaveLen(2), fmt.Sprintf("expected both researcher IDs preserved, got %#v", conflict.ResearcherIDs))
		gomega.Expect(conflict.ResearcherIDs[0]).To(gomega.Equal("north_star"), fmt.Sprintf("expected both researcher IDs preserved, got %#v", conflict.ResearcherIDs))
		gomega.Expect(conflict.ResearcherIDs[1]).To(gomega.Equal("technical"), fmt.Sprintf("expected both researcher IDs preserved, got %#v", conflict.ResearcherIDs))

	})
	ginkgo.It("MergeResearchCardDocumentsPreservesExplicitConflicts", func() {
		doc := ResearchCardsDocument{
			SchemaVersion: "idiom.research_cards.v1",
			ResearcherID:  "technical",
			Alignment:     "technical",
			Cards:         []EvidenceCard{researchMergeTestCard("EV-003", "technical", "Third claim")},
			Conflicts: []ResearchEvidenceConflict{{
				Kind:          "researcher_disagreement",
				ClaimKey:      "REQ-1",
				CardIDs:       []string{"EV-003", "EV-002"},
				ResearcherIDs: []string{"technical", "product"},
				Message:       "technical and product disagree on priority",
			}},
		}
		result := MergeResearchCardDocuments(nil, []ResearchCardsDocument{doc}, MergePolicy{})
		gomega.Expect(len(result.Conflicts) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected explicit conflict to be preserved, got %#v", result.Conflicts))
		gomega.Expect(result.Conflicts[0].Kind != "researcher_disagreement").To(gomega.BeFalse(), fmt.Sprintf("unexpected conflict kind: %#v", result.Conflicts[0]))

	})
	ginkgo.It("MergeResearchEvidenceFlagsUnsupportedAndStaleCards", func() {
		stale := researchMergeTestCard("EV-004", "technical", "Stale claim")

		stale.Freshness = "stale"

		unsupported := researchMergeTestCard("EV-005", "product", "Unsupported claim")

		unsupported.Kind = "unsupported_claim"

		result := MergeResearchEvidence(nil, []EvidenceCard{stale, unsupported}, MergePolicy{
			UnsupportedClaims: "block_prd_audit",
			StaleLocalContext: "refresh_or_mark_gap",
		})

		var staleFound, unsupportedFound bool

		for _, finding := range result.Findings {
			if finding.Kind == "stale_local_context" && finding.File == "EV-004" && finding.Severity == "warning" {
				staleFound = true
			}

			if finding.Kind == "unsupported_claim" && finding.File == "EV-005" && finding.Severity == "warning" {
				unsupportedFound = true
			}
		}
		gomega.Expect(staleFound).To(gomega.BeTrue(), fmt.Sprintf("expected stale and unsupported findings, got %#v", result.Findings))
		gomega.Expect(unsupportedFound).To(gomega.BeTrue(), fmt.Sprintf("expected stale and unsupported findings, got %#v", result.Findings))

	})
	ginkgo.It("MergeResearchEvidenceCanBlockUnsupportedClaimsByPolicy", func() {
		unsupported := researchMergeTestCard("EV-006", "product", "Unsupported claim")

		unsupported.Kind = "unsupported_claim"

		result := MergeResearchEvidence(nil, []EvidenceCard{unsupported}, MergePolicy{UnsupportedClaims: "block_research_merge"})
		gomega.Expect(len(result.Findings) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one finding, got %#v", result.Findings))
		gomega.Expect(result.Findings[0].Severity != "error").To(gomega.BeFalse(), fmt.Sprintf("expected error severity for block_research_merge policy, got %#v", result.Findings[0]))

	})
	ginkgo.It("DiscoverResearchCardDocumentsRejectsTraversalDirectory", func() {
		_, findings, err := DiscoverResearchCardDocuments(ginkgo.GinkgoT().TempDir(), "../outside")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected typed finding, got error %v", err))
		gomega.Expect(findings).To(gomega.HaveLen(1), fmt.Sprintf("expected invalid_research_output_dir error finding, got %#v", findings))
		gomega.Expect(findings[0].Kind).To(gomega.Equal("invalid_research_output_dir"), fmt.Sprintf("expected invalid_research_output_dir error finding, got %#v", findings))
		gomega.Expect(findings[0].Severity).To(gomega.Equal("error"), fmt.Sprintf("expected invalid_research_output_dir error finding, got %#v", findings))

	})
	ginkgo.It("DiscoverResearchCardDocumentsRejectsSymlinkDirectoryOutsideEffort", func() {
		effortRoot := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()

		if err := os.Symlink(outside, filepath.Join(effortRoot, "linked")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		_, findings, err := DiscoverResearchCardDocuments(effortRoot, "linked")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected typed finding, got error %v", err))
		gomega.Expect(findings).To(gomega.HaveLen(1), fmt.Sprintf("expected research_output_dir_outside_effort error finding, got %#v", findings))
		gomega.Expect(findings[0].Kind).To(gomega.Equal("research_output_dir_outside_effort"), fmt.Sprintf("expected research_output_dir_outside_effort error finding, got %#v", findings))
		gomega.Expect(findings[0].Severity).To(gomega.Equal("error"), fmt.Sprintf("expected research_output_dir_outside_effort error finding, got %#v", findings))

	})
	ginkgo.It("DiscoverResearchCardDocumentsRejectsSymlinkFiles", func() {
		effortRoot := ginkgo.GinkgoT().TempDir()
		outputDir := filepath.Join(effortRoot, "research", "outputs")
		{

			err := os.MkdirAll(outputDir, 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir output dir: %v", err))
		}

		target := filepath.Join(effortRoot, "target.json")
		{
			err := os.WriteFile(target, []byte(`{}`), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
		}

		if err := os.Symlink(target, filepath.Join(outputDir, "linked.json")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		_, findings, err := DiscoverResearchCardDocuments(effortRoot, "research/outputs")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected typed finding, got error %v", err))
		gomega.Expect(findings).To(gomega.HaveLen(1), fmt.Sprintf("expected research_output_symlink error finding, got %#v", findings))
		gomega.Expect(findings[0].Kind).To(gomega.Equal("research_output_symlink"), fmt.Sprintf("expected research_output_symlink error finding, got %#v", findings))
		gomega.Expect(findings[0].Severity).To(gomega.Equal("error"), fmt.Sprintf("expected research_output_symlink error finding, got %#v", findings))

	})
	ginkgo.It("ParseResearchCardsJSONRejectsInvalidShape", func() {
		_, err := ParseResearchCardsJSON([]byte(`{"schema_version":"idiom.research_cards.v1","researcher_id":"technical","alignment":"technical","cards":[]}`))
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected empty cards document to fail"))

	})
})

func researchMergeTestCard(id, researcher, claim string) EvidenceCard {
	return EvidenceCard{
		CardID:       id,
		Kind:         "finding",
		SourceHandle: "file://README.md",
		Claim:        claim,
		Confidence:   "high",
		Freshness:    "current",
		Uncertainty:  "",
		EvidenceRefs: []string{"README.md:1"},
		ResearcherID: researcher,
		Timestamp:    "2026-05-18T00:00:00Z",
	}
}
