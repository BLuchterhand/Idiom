package productworkflow

import (
	"fmt"
	"path/filepath"
	"strings"
)


type ADRCrossLinkError struct {
	ADRPath string
	Field   string
	Value   string
	Message string
}


func ValidateADRCrossLinks(
	adrs []ParsedADR,
	prdRequirementIDs map[string]bool,
	evidenceCardIDs map[string]bool,
	tddObligationIDs map[string]bool,
) []ADRCrossLinkError {
	var errs []ADRCrossLinkError
	for _, a := range adrs {
		adrRef := adrCrossLinkReference(a)
		for _, ref := range a.Doc.FrontMatter.PRDRefs {
			if prdRequirementIDs != nil && !prdRequirementIDs[ref] {
				errs = append(errs, ADRCrossLinkError{
					ADRPath: a.RelPath,
					Field:   "prd_refs",
					Value:   ref,
					Message: fmt.Sprintf("ADR %s: prd_ref %q not found in parsed PRD requirement IDs", adrRef, ref),
				})
			}
		}
		for _, ref := range a.Doc.FrontMatter.EvidenceRefs {
			if evidenceCardIDs != nil && !evidenceCardIDs[ref] {
				errs = append(errs, ADRCrossLinkError{
					ADRPath: a.RelPath,
					Field:   "evidence_refs",
					Value:   ref,
					Message: fmt.Sprintf("ADR %s: evidence_ref %q not found in parsed evidence card IDs", adrRef, ref),
				})
			}
		}
		for _, ref := range a.Doc.FrontMatter.TDDObligations {
			if tddObligationIDs != nil && !tddObligationIDs[ref] {
				errs = append(errs, ADRCrossLinkError{
					ADRPath: a.RelPath,
					Field:   "tdd_obligations",
					Value:   ref,
					Message: fmt.Sprintf("ADR %s: tdd_obligation %q not found in parsed TDD obligation IDs", adrRef, ref),
				})
			}
		}
	}
	return errs
}


func adrCrossLinkReference(adr ParsedADR) string {
	if ref := strings.TrimSpace(adr.RelPath); ref != "" {
		return ref
	}
	if ref := strings.TrimSpace(adr.Doc.FrontMatter.ADRID); ref != "" {
		return ref
	}
	return "(unknown)"
}


type PRDTDDObligationLinkError struct {
	RequirementID string
	TDDObligation string
	Message       string
}


func ValidatePRDTDDObligationCrossLinks(
	requirements []PRDRequirement,
	tddObligationIDs map[string]bool,
) []PRDTDDObligationLinkError {
	var errs []PRDTDDObligationLinkError
	for _, req := range requirements {
		if strings.ToLower(req.Priority) != "must" {
			continue
		}
		for _, tddID := range req.TDDObligations {
			if tddObligationIDs != nil && !tddObligationIDs[tddID] {
				errs = append(errs, PRDTDDObligationLinkError{
					RequirementID: req.ID,
					TDDObligation: tddID,
					Message: fmt.Sprintf(
						"PRD must-priority requirement %s references TDD obligation %q not found in current or historical TDD obligation IDs",
						req.ID, tddID,
					),
				})
			}
		}
	}
	return errs
}


type ParsedADR struct {
	Doc         ADRDocument
	RelPath     string
	RawMarkdown string
}


type ADRSupersessionLinkError struct {
	Code    string
	ADRPath string
	ADRID   string
	Field   string
	Ref     string
	Message string
}


func ValidateADRSupersessionLinks(adrs []ParsedADR) []ADRSupersessionLinkError {
	var errs []ADRSupersessionLinkError
	byID := make(map[string]ParsedADR, len(adrs))
	for _, a := range adrs {
		byID[a.Doc.FrontMatter.ADRID] = a
	}
	for _, a := range adrs {
		id := a.Doc.FrontMatter.ADRID
		checkList := func(field string, refs []string) {
			seen := map[string]bool{}
			for _, ref := range refs {
				if seen[ref] {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_duplicate_ref",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s has duplicate %s reference %q", id, field, ref),
					})
					continue
				}
				seen[ref] = true
				if ref == id {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_self_ref",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s has self-reference in %s", id, field),
					})
					continue
				}
				peer, ok := byID[ref]
				if !ok {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_broken_ref",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s references missing ADR %q in %s", id, ref, field),
					})
					continue
				}
				if field == "supersedes" && !containsString(peer.Doc.FrontMatter.SupersededBy, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s supersedes %s but %s does not list %s in superseded_by", id, ref, ref, id),
					})
				}
				if field == "superseded_by" && !containsString(peer.Doc.FrontMatter.Supersedes, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: a.RelPath,
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR %s lists superseded_by %s but %s does not list %s in supersedes", id, ref, ref, id),
					})
				}
			}
		}
		checkList("supersedes", a.Doc.FrontMatter.Supersedes)
		checkList("superseded_by", a.Doc.FrontMatter.SupersededBy)
	}
	return errs
}


func ValidateADRIndexSupersessionLinks(idx ADRIndex) []ADRSupersessionLinkError {
	var errs []ADRSupersessionLinkError
	byID := make(map[string]ADRIndexEntry, len(idx.ADRs))
	for _, entry := range idx.ADRs {
		byID[entry.ADRID] = entry
	}
	for _, entry := range idx.ADRs {
		id := entry.ADRID
		checkList := func(field string, refs []string) {
			seen := map[string]bool{}
			for _, ref := range refs {
				if seen[ref] {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_duplicate_ref",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s has duplicate %s reference %q", id, field, ref),
					})
					continue
				}
				seen[ref] = true
				if ref == id {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_self_ref",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s has self-reference in %s", id, field),
					})
					continue
				}
				peer, ok := byID[ref]
				if !ok {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_broken_ref",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s references missing ADR %q in %s", id, ref, field),
					})
					continue
				}
				if field == "supersedes" && !containsString(peer.SupersededBy, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s supersedes %s but %s does not list %s in superseded_by", id, ref, ref, id),
					})
				}
				if field == "superseded_by" && !containsString(peer.Supersedes, id) {
					errs = append(errs, ADRSupersessionLinkError{
						Code:    "adr_supersession_non_reciprocal",
						ADRPath: "adrs/index.json",
						ADRID:   id,
						Field:   field,
						Ref:     ref,
						Message: fmt.Sprintf("ADR index entry %s lists superseded_by %s but %s does not list %s in supersedes", id, ref, ref, id),
					})
				}
			}
		}
		checkList("supersedes", entry.Supersedes)
		checkList("superseded_by", entry.SupersededBy)
	}
	return errs
}


type ADRIndexFrontMatterConsistencyError struct {
	Code    string
	Path    string
	ADRID   string
	Field   string
	Message string
}


func ValidateADRIndexFrontMatterConsistency(idx ADRIndex, adrs []ParsedADR) []ADRIndexFrontMatterConsistencyError {
	var errs []ADRIndexFrontMatterConsistencyError
	byPath := make(map[string]ParsedADR, len(adrs))
	for _, adr := range adrs {
		relPath := filepath.ToSlash(strings.TrimSpace(adr.RelPath))
		if relPath != "" {
			byPath[relPath] = adr
		}
	}
	for _, entry := range idx.ADRs {
		entryPath := filepath.ToSlash(filepath.Join("adrs", strings.TrimSpace(entry.Path)))
		adr, ok := byPath[entryPath]
		if !ok {
			continue
		}
		appendMismatch := func(field, indexValue, markdownValue string) {
			errs = append(errs, ADRIndexFrontMatterConsistencyError{
				Code:    "adr_index_frontmatter_mismatch",
				Path:    "adrs/index.json",
				ADRID:   entry.ADRID,
				Field:   field,
				Message: fmt.Sprintf("ADR index entry %s field %s=%q diverges from %s front matter value %q", entry.ADRID, field, indexValue, entryPath, markdownValue),
			})
		}
		compareString := func(field, indexValue, markdownValue string) {
			indexValue = strings.TrimSpace(indexValue)
			markdownValue = strings.TrimSpace(markdownValue)
			if indexValue != markdownValue {
				appendMismatch(field, indexValue, markdownValue)
			}
		}
		compareList := func(field string, indexValues, markdownValues []string) {
			indexCanonical := canonicalStringSet(indexValues)
			markdownCanonical := canonicalStringSet(markdownValues)
			if strings.Join(indexCanonical, "\x00") != strings.Join(markdownCanonical, "\x00") {
				appendMismatch(field, strings.Join(indexCanonical, ","), strings.Join(markdownCanonical, ","))
			}
		}
		compareString("adr_id", entry.ADRID, adr.Doc.FrontMatter.ADRID)
		compareString("status", entry.Status, adr.Doc.FrontMatter.Status)
		compareString("coverage_tier", entry.CoverageTier, adr.Doc.FrontMatter.CoverageTier)
		compareList("prd_refs", entry.PRDRefs, adr.Doc.FrontMatter.PRDRefs)
		compareList("supersedes", entry.Supersedes, adr.Doc.FrontMatter.Supersedes)
		compareList("superseded_by", entry.SupersededBy, adr.Doc.FrontMatter.SupersededBy)
	}
	return errs
}


type TraceabilityLinkError struct {
	RowIndex int
	Field    string
	Value    string
	Message  string
}


func ValidateTraceabilityCrossLinks(
	matrix TraceabilityMatrix,
	evidenceCardIDs map[string]bool,
	prdRequirementIDs map[string]bool,
	adrIDs map[string]bool,
	tddObligationIDs map[string]bool,
	briefPaths map[string]bool,
	reportPaths map[string]bool,
) []TraceabilityLinkError {
	var errs []TraceabilityLinkError
	for i, row := range matrix.Matrix {
		if evidenceCardIDs != nil && !evidenceCardIDs[row.EvidenceCard] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "evidence_card",
				Value:    row.EvidenceCard,
				Message:  fmt.Sprintf("traceability row %d: evidence_card %q not found in evidence cards", i, row.EvidenceCard),
			})
		}
		if prdRequirementIDs != nil && !prdRequirementIDs[row.PRDRequirement] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "prd_requirement",
				Value:    row.PRDRequirement,
				Message:  fmt.Sprintf("traceability row %d: prd_requirement %q not found in PRD requirements", i, row.PRDRequirement),
			})
		}
		if adrIDs != nil && !adrIDs[row.ADR] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "adr",
				Value:    row.ADR,
				Message:  fmt.Sprintf("traceability row %d: adr %q not found in ADR index", i, row.ADR),
			})
		}
		if tddObligationIDs != nil && !tddObligationIDs[row.TDDObligation] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "tdd_obligation",
				Value:    row.TDDObligation,
				Message:  fmt.Sprintf("traceability row %d: tdd_obligation %q not found in current or historical TDD obligations", i, row.TDDObligation),
			})
		}
		if briefPaths != nil && !briefPaths[row.Brief] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "brief",
				Value:    row.Brief,
				Message:  fmt.Sprintf("traceability row %d: brief %q not found in briefs directory", i, row.Brief),
			})
		}
		if reportPaths != nil && !reportPaths[row.Report] {
			errs = append(errs, TraceabilityLinkError{
				RowIndex: i,
				Field:    "report",
				Value:    row.Report,
				Message:  fmt.Sprintf("traceability row %d: report %q not found in reports directory", i, row.Report),
			})
		}
	}
	return errs
}

