package productworkflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeResearchEvidenceDeterministicOrderingAndDedup(t *testing.T) {
	existing := []EvidenceCard{
		researchMergeTestCard("EV-002", "technical", "Second claim"),
	}
	additions := []EvidenceCard{
		researchMergeTestCard("EV-001", "north_star", "First claim"),
		researchMergeTestCard("EV-002", "technical", "Second claim"),
	}
	result := MergeResearchEvidence(existing, additions, MergePolicy{})
	if len(result.Cards) != 2 {
		t.Fatalf("expected 2 merged cards, got %d: %#v", len(result.Cards), result.Cards)
	}
	if result.Cards[0].CardID != "EV-001" || result.Cards[1].CardID != "EV-002" {
		t.Fatalf("expected deterministic card order EV-001, EV-002; got %#v", result.Cards)
	}
	if len(result.MergedCardIDs) != 2 || result.MergedCardIDs[0] != "EV-001" || result.MergedCardIDs[1] != "EV-002" {
		t.Fatalf("expected deterministic merged IDs, got %#v", result.MergedCardIDs)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("expected no conflicts for exact duplicate, got %#v", result.Conflicts)
	}
}

func TestMergeResearchEvidencePreservesDuplicateCardConflicts(t *testing.T) {
	existing := []EvidenceCard{
		researchMergeTestCard("EV-001", "north_star", "First claim"),
	}
	additions := []EvidenceCard{
		researchMergeTestCard("EV-001", "technical", "Different claim"),
	}
	result := MergeResearchEvidence(existing, additions, MergePolicy{})
	if len(result.Cards) != 1 {
		t.Fatalf("expected original card to be retained once, got %#v", result.Cards)
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %#v", result.Conflicts)
	}
	conflict := result.Conflicts[0]
	if conflict.Kind != "duplicate_card_id_conflict" || conflict.ClaimKey != "EV-001" {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
	if len(conflict.ResearcherIDs) != 2 || conflict.ResearcherIDs[0] != "north_star" || conflict.ResearcherIDs[1] != "technical" {
		t.Fatalf("expected both researcher IDs preserved, got %#v", conflict.ResearcherIDs)
	}
}

func TestMergeResearchCardDocumentsPreservesExplicitConflicts(t *testing.T) {
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
	if len(result.Conflicts) != 1 {
		t.Fatalf("expected explicit conflict to be preserved, got %#v", result.Conflicts)
	}
	if result.Conflicts[0].Kind != "researcher_disagreement" {
		t.Fatalf("unexpected conflict kind: %#v", result.Conflicts[0])
	}
}

func TestMergeResearchEvidenceFlagsUnsupportedAndStaleCards(t *testing.T) {
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
	if !staleFound || !unsupportedFound {
		t.Fatalf("expected stale and unsupported findings, got %#v", result.Findings)
	}
}

func TestMergeResearchEvidenceCanBlockUnsupportedClaimsByPolicy(t *testing.T) {
	unsupported := researchMergeTestCard("EV-006", "product", "Unsupported claim")
	unsupported.Kind = "unsupported_claim"
	result := MergeResearchEvidence(nil, []EvidenceCard{unsupported}, MergePolicy{UnsupportedClaims: "block_research_merge"})
	if len(result.Findings) != 1 {
		t.Fatalf("expected one finding, got %#v", result.Findings)
	}
	if result.Findings[0].Severity != "error" {
		t.Fatalf("expected error severity for block_research_merge policy, got %#v", result.Findings[0])
	}
}

func TestDiscoverResearchCardDocumentsRejectsTraversalDirectory(t *testing.T) {
	_, findings, err := DiscoverResearchCardDocuments(t.TempDir(), "../outside")
	if err != nil {
		t.Fatalf("expected typed finding, got error %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != "invalid_research_output_dir" || findings[0].Severity != "error" {
		t.Fatalf("expected invalid_research_output_dir error finding, got %#v", findings)
	}
}

func TestDiscoverResearchCardDocumentsRejectsSymlinkDirectoryOutsideEffort(t *testing.T) {
	effortRoot := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(effortRoot, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, findings, err := DiscoverResearchCardDocuments(effortRoot, "linked")
	if err != nil {
		t.Fatalf("expected typed finding, got error %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != "research_output_dir_outside_effort" || findings[0].Severity != "error" {
		t.Fatalf("expected research_output_dir_outside_effort error finding, got %#v", findings)
	}
}

func TestDiscoverResearchCardDocumentsRejectsSymlinkFiles(t *testing.T) {
	effortRoot := t.TempDir()
	outputDir := filepath.Join(effortRoot, "research", "outputs")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output dir: %v", err)
	}
	target := filepath.Join(effortRoot, "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(outputDir, "linked.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, findings, err := DiscoverResearchCardDocuments(effortRoot, "research/outputs")
	if err != nil {
		t.Fatalf("expected typed finding, got error %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != "research_output_symlink" || findings[0].Severity != "error" {
		t.Fatalf("expected research_output_symlink error finding, got %#v", findings)
	}
}

func TestParseResearchCardsJSONRejectsInvalidShape(t *testing.T) {
	_, err := ParseResearchCardsJSON([]byte(`{"schema_version":"idiom.research_cards.v1","researcher_id":"technical","alignment":"technical","cards":[]}`))
	if err == nil {
		t.Fatal("expected empty cards document to fail")
	}
}

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
