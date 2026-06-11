package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func DiscoverResearchCardDocuments(effortRoot, relDir string) ([]ResearchCardsDocument, []ProfileFinding, error) {
	cleanRelDir, ok := cleanResearchOutputDir(relDir)
	if !ok {
		return nil, []ProfileFinding{{
			Kind:     "invalid_research_output_dir",
			Message:  fmt.Sprintf("research output directory %q must be effort-relative and must not contain path traversal", relDir),
			File:     relDir,
			Severity: ProductWorkflowSeverityError,
		}}, nil
	}
	dir := filepath.Join(effortRoot, cleanRelDir)
	if finding, ok := researchOutputDirBoundaryFinding(effortRoot, dir, cleanRelDir); ok {
		return nil, []ProfileFinding{finding}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []ProfileFinding{{
				Kind:     "missing_research_output_dir",
				Message:  fmt.Sprintf("research output directory %q not found", cleanRelDir),
				File:     cleanRelDir,
				Severity: ProductWorkflowSeverityError,
			}}, nil
		}
		return nil, nil, fmt.Errorf("read research output directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var docs []ResearchCardsDocument
	var findings []ProfileFinding
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		relPath := filepath.Join(cleanRelDir, name)
		if entry.Type()&os.ModeSymlink != 0 {
			findings = append(findings, ProfileFinding{
				Kind:     "research_output_symlink",
				Message:  fmt.Sprintf("research output file %q must not be a symlink", relPath),
				File:     relPath,
				Severity: ProductWorkflowSeverityError,
			})
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			findings = append(findings, ProfileFinding{
				Kind:     "non_json_file",
				Message:  fmt.Sprintf("skipping non-JSON file %q", relPath),
				File:     relPath,
				Severity: "warning",
			})
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			findings = append(findings, ProfileFinding{
				Kind:     "read_error",
				Message:  fmt.Sprintf("failed to read %q: %v", relPath, readErr),
				File:     relPath,
				Severity: ProductWorkflowSeverityError,
			})
			continue
		}
		doc, parseErr := ParseResearchCardsJSON(data)
		if parseErr != nil {
			findings = append(findings, ProfileFinding{
				Kind:     "validation_error",
				Message:  fmt.Sprintf("research output %q failed validation: %v", relPath, parseErr),
				File:     relPath,
				Severity: ProductWorkflowSeverityError,
			})
			continue
		}
		docs = append(docs, doc)
	}
	return docs, findings, nil
}

func MergeResearchCardDocuments(existing []EvidenceCard, docs []ResearchCardsDocument, policy MergePolicy) ResearchEvidenceMergeResult {
	var additions []EvidenceCard
	var conflicts []ResearchEvidenceConflict
	for _, doc := range docs {
		additions = append(additions, doc.Cards...)
		for _, conflict := range doc.Conflicts {
			conflicts = append(conflicts, normalizeResearchConflict(conflict))
		}
	}
	result := MergeResearchEvidence(existing, additions, policy)
	result.Conflicts = append(result.Conflicts, conflicts...)
	sortResearchMergeResult(&result)
	return result
}

func MergeResearchEvidence(existing []EvidenceCard, additions []EvidenceCard, policy MergePolicy) ResearchEvidenceMergeResult {
	byID := map[string]EvidenceCard{}
	cardConflicts := map[string]ResearchEvidenceConflict{}
	for _, card := range append(append([]EvidenceCard{}, existing...), additions...) {
		id := strings.TrimSpace(card.CardID)
		if id == "" {
			continue
		}
		current, ok := byID[id]
		if !ok {
			byID[id] = card
			continue
		}
		if canonicalEvidenceCard(current) == canonicalEvidenceCard(card) {
			continue
		}
		cardConflicts[id] = mergeConflictCards(cardConflicts[id], current, card)
	}

	cards := make([]EvidenceCard, 0, len(byID))
	mergedIDs := make([]string, 0, len(byID))
	for id, card := range byID {
		cards = append(cards, card)
		mergedIDs = append(mergedIDs, id)
	}
	conflicts := make([]ResearchEvidenceConflict, 0, len(cardConflicts))
	for _, conflict := range cardConflicts {
		conflicts = append(conflicts, normalizeResearchConflict(conflict))
	}
	result := ResearchEvidenceMergeResult{
		Cards:         cards,
		MergedCardIDs: mergedIDs,
		Conflicts:     conflicts,
		Findings:      researchEvidenceFindings(cards, policy),
	}
	sortResearchMergeResult(&result)
	return result
}

func researchEvidenceFindings(cards []EvidenceCard, policy MergePolicy) []ProfileFinding {
	var findings []ProfileFinding
	for _, card := range cards {
		if strings.TrimSpace(card.Freshness) != "current" {
			findings = append(findings, ProfileFinding{
				Kind:     "stale_local_context",
				Message:  fmt.Sprintf("evidence card %q freshness is %q", card.CardID, card.Freshness),
				File:     card.CardID,
				Severity: severityFromMergePolicy(policy.StaleLocalContext, "warning"),
			})
		}
		if strings.TrimSpace(card.Confidence) == "unsupported" || strings.TrimSpace(card.Kind) == "unsupported_claim" {
			findings = append(findings, ProfileFinding{
				Kind:     "unsupported_claim",
				Message:  fmt.Sprintf("evidence card %q is marked unsupported", card.CardID),
				File:     card.CardID,
				Severity: severityFromMergePolicy(policy.UnsupportedClaims, ProductWorkflowSeverityError),
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		return findings[i].File < findings[j].File
	})
	return findings
}

func cleanResearchOutputDir(relDir string) (string, bool) {
	raw := strings.TrimSpace(relDir)
	normalized := strings.ReplaceAll(filepath.ToSlash(raw), "\\", "/")
	if normalized == "" || filepath.IsAbs(raw) || strings.HasPrefix(normalized, "/") {
		return "", false
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func researchOutputDirBoundaryFinding(effortRoot, dir, relDir string) (ProfileFinding, bool) {
	rootEval, err := filepath.EvalSymlinks(effortRoot)
	if err != nil {
		return ProfileFinding{
			Kind:     "read_error",
			Message:  fmt.Sprintf("failed to resolve effort root %q: %v", effortRoot, err),
			File:     ".",
			Severity: ProductWorkflowSeverityError,
		}, true
	}
	dirEval, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return ProfileFinding{}, false
		}
		return ProfileFinding{
			Kind:     "read_error",
			Message:  fmt.Sprintf("failed to resolve research output directory %q: %v", relDir, err),
			File:     relDir,
			Severity: ProductWorkflowSeverityError,
		}, true
	}
	rootEval = filepath.Clean(rootEval)
	dirEval = filepath.Clean(dirEval)
	if dirEval == rootEval {
		return ProfileFinding{}, false
	}
	rel, err := filepath.Rel(rootEval, dirEval)
	if err != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return ProfileFinding{
			Kind:     "research_output_dir_outside_effort",
			Message:  fmt.Sprintf("research output directory %q resolves outside the effort root", relDir),
			File:     relDir,
			Severity: ProductWorkflowSeverityError,
		}, true
	}
	return ProfileFinding{}, false
}

func severityFromMergePolicy(policyValue, defaultSeverity string) string {
	normalized := strings.ToLower(strings.TrimSpace(policyValue))
	switch {
	case normalized == "":
		return defaultSeverity
	case strings.Contains(normalized, "block_prd_audit"):
		return "warning"
	case strings.Contains(normalized, "refresh_or_mark_gap"):
		return "warning"
	case strings.Contains(normalized, "mark_gap"):
		return "warning"
	case strings.Contains(normalized, "warn"):
		return "warning"
	case strings.Contains(normalized, "block"):
		return ProductWorkflowSeverityError
	default:
		return defaultSeverity
	}
}

func mergeConflictCards(existing ResearchEvidenceConflict, a, b EvidenceCard) ResearchEvidenceConflict {
	if strings.TrimSpace(existing.Kind) == "" {
		existing = ResearchEvidenceConflict{
			Kind:     "duplicate_card_id_conflict",
			ClaimKey: a.CardID,
			Message:  fmt.Sprintf("evidence card %q has conflicting payloads", a.CardID),
		}
	}
	existing.CardIDs = append(existing.CardIDs, a.CardID, b.CardID)
	existing.ResearcherIDs = append(existing.ResearcherIDs, a.ResearcherID, b.ResearcherID)
	return existing
}

func normalizeResearchConflict(conflict ResearchEvidenceConflict) ResearchEvidenceConflict {
	conflict.CardIDs = uniqueSortedStrings(conflict.CardIDs)
	conflict.ResearcherIDs = uniqueSortedStrings(conflict.ResearcherIDs)
	return conflict
}

func sortResearchMergeResult(result *ResearchEvidenceMergeResult) {
	sort.SliceStable(result.Cards, func(i, j int) bool {
		return result.Cards[i].CardID < result.Cards[j].CardID
	})
	result.MergedCardIDs = uniqueSortedStrings(result.MergedCardIDs)
	for i := range result.Conflicts {
		result.Conflicts[i] = normalizeResearchConflict(result.Conflicts[i])
	}
	sort.SliceStable(result.Conflicts, func(i, j int) bool {
		if result.Conflicts[i].Kind != result.Conflicts[j].Kind {
			return result.Conflicts[i].Kind < result.Conflicts[j].Kind
		}
		return result.Conflicts[i].ClaimKey < result.Conflicts[j].ClaimKey
	})
}

func uniqueSortedStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func canonicalEvidenceCard(card EvidenceCard) string {
	data, _ := json.Marshal(card)
	return string(data)
}
