package productworkflow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"idiom/internal/platform/util"
)

// parseAndValidate unmarshals JSON data into T and validates the result.
// label is used in the error message, e.g. "effort index".
func parseAndValidate[T any](data []byte, label string, validate func(T) error) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("parse %s: %w", label, err)
	}
	if err := validate(v); err != nil {
		return v, err
	}
	return v, nil
}

func ParseEffortIndexJSON(data []byte) (EffortIndex, error) {
	return parseAndValidate(data, "effort index", validateEffortIndex)
}

func ParseWorkflowStateJSON(data []byte) (WorkflowState, error) {
	return parseAndValidate(data, "workflow state", validateWorkflowState)
}

func ParseTDDLedgerJSON(data []byte) (TDDLedger, error) {
	return parseAndValidate(data, "tdd ledger", validateTDDLedger)
}

func ParseEvidenceCardsJSONL(data []byte) ([]EvidenceCard, error) {
	var cards []EvidenceCard
	if err := scanJSONL(data, "evidence card", func(line int, text []byte) error {
		var card EvidenceCard
		if err := json.Unmarshal(text, &card); err != nil {
			return fmt.Errorf("parse evidence card line %d: %w", line, err)
		}
		if err := validateEvidenceCard(card, line); err != nil {
			return err
		}
		cards = append(cards, card)
		return nil
	}); err != nil {
		return nil, err
	}
	return cards, nil
}

func ParseResearchCardsJSON(data []byte) (ResearchCardsDocument, error) {
	return parseAndValidate(data, "research cards", validateResearchCardsDocument)
}

func ParseTraceabilityMatrixJSON(data []byte) (TraceabilityMatrix, error) {
	return parseAndValidate(data, "traceability matrix", validateTraceabilityMatrix)
}

func ParseImplementationLedgerJSONL(data []byte) ([]ImplementationLedgerRecord, error) {
	var records []ImplementationLedgerRecord
	if err := scanJSONL(data, "implementation ledger record", func(line int, text []byte) error {
		var rec ImplementationLedgerRecord
		if err := json.Unmarshal(text, &rec); err != nil {
			return fmt.Errorf("parse implementation ledger line %d: %w", line, err)
		}
		records = append(records, rec)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := validateImplementationLedger(records); err != nil {
		return nil, err
	}
	return records, nil
}

func scanJSONL(data []byte, label string, visit func(line int, text []byte) error) error {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	line := 0
	records := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		records++
		if err := visit(line, []byte(text)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s JSONL: %w", label, err)
	}
	if records == 0 {
		return fmt.Errorf("%s JSONL contains no records", label)
	}
	return nil
}

// UnwrapSingleMarkdownFence strips a single outer markdown code fence from
// model output, returning the inner content. Returns the original string
// unchanged when no fence is present or the fencing is ambiguous.
func UnwrapSingleMarkdownFence(value string) string {
	trimmed := strings.TrimSpace(value)
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 {
		return value
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		return value
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return value
	}
	body := strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	if body == "" {
		return value
	}
	return body
}

// RepairMarkdownFrontMatter applies a sequence of lightweight repairs to
// model output that may be wrapped in fences, quotes, or preamble text
// before the YAML front matter delimiter. Returns the repaired text.
func RepairMarkdownFrontMatter(raw string) string {
	// 1. Unwrap markdown code fence.
	repaired := UnwrapSingleMarkdownFence(raw)
	if repaired != raw {
		raw = repaired
	}

	// 2. Strip leading/trailing whitespace.
	repaired = strings.TrimSpace(raw)
	if repaired != raw {
		raw = repaired
	}

	// 3. Locate the first --- delimiter and skip any preamble text before it.
	const delim = "---"
	idx := strings.Index(raw, delim)
	if idx > 0 && raw[idx-1] == '\n' {
		raw = raw[idx:]
	}

	// 4. Strip surrounding double or single quotes.
	for _, q := range []string{"\"", "'"} {
		if strings.HasPrefix(raw, q) && strings.HasSuffix(raw, q) && len(raw) >= 2 {
			inner := raw[1 : len(raw)-1]
			if strings.HasPrefix(strings.TrimSpace(inner), delim) {
				raw = strings.TrimSpace(inner)
				break
			}
		}
	}

	return raw
}

// TryParsePRDMarkdown applies repair strategies and attempts to parse a PRD.
// Returns the parsed document, the repaired text that succeeded (empty on
// failure), and any parse error.
func TryParsePRDMarkdown(raw string) (PRDDocument, string, error) {
	repaired := RepairMarkdownFrontMatter(raw)
	doc, err := ParsePRDMarkdown([]byte(repaired))
	if err != nil {
		return PRDDocument{}, "", fmt.Errorf("parse PRD after repair: %w", err)
	}
	return doc, repaired, nil
}

// TryParseADRMarkdown applies repair strategies and attempts to parse a single ADR.
// Returns the parsed document, the repaired text, and any parse error.
func TryParseADRMarkdown(raw string) (ADRDocument, string, error) {
	repaired := RepairMarkdownFrontMatter(raw)
	doc, err := ParseADRMarkdown([]byte(repaired))
	if err != nil {
		return ADRDocument{}, "", fmt.Errorf("parse ADR after repair: %w", err)
	}
	return doc, repaired, nil
}

// ADRParseFailure records a single ADR chunk that failed to parse.
// ADRParseFailure records a single ADR chunk that failed to parse.
type ADRParseFailure struct {
	ChunkIndex int
	RawChunk   string
	Error      string
}

// TrySplitAndParseADRs splits model output on === separators, repairs each
// chunk, and parses each ADR. Returns successfully parsed ADRs, index entries,
// per-chunk failure details, and an error only when all chunks failed.
func TrySplitAndParseADRs(output string, prd PRDDocument) ([]ADRDocument, []ADRIndexEntry, []ADRParseFailure, error) {
	chunks := strings.Split(output, "===")
	var adrDocs []ADRDocument
	var indexEntries []ADRIndexEntry
	var failures []ADRParseFailure

	for i, chunk := range chunks {
		chunk = RepairMarkdownFrontMatter(chunk)
		if chunk == "" {
			continue
		}
		if !strings.HasPrefix(chunk, "---") {
			chunk = "---\n" + chunk
		}
		// Auto-assign adr_id if missing from front matter.
		if !strings.Contains(chunk[:min(len(chunk), 300)], "adr_id:") {
			// Find end of first line (the --- delimiter) and insert after it.
			if idx := strings.Index(chunk, "\n"); idx > 0 {
				adrID := fmt.Sprintf("ADR-%03d", len(adrDocs)+1)
				chunk = chunk[:idx+1] + "adr_id: " + adrID + "\n" + chunk[idx+1:]
			}
		}
		doc, err := ParseADRMarkdown([]byte(chunk))
		if err != nil {
			failures = append(failures, ADRParseFailure{
				ChunkIndex: i,
				RawChunk:   util.Truncate(chunk, 200),
				Error:      err.Error(),
			})
			continue
		}
		adrID := doc.FrontMatter.ADRID
		if adrID == "" {
			adrID = fmt.Sprintf("ADR-%03d", len(adrDocs)+1)
			doc.FrontMatter.ADRID = adrID
		}
		status := "draft"
		if doc.FrontMatter.Status != "" {
			status = doc.FrontMatter.Status
		}
		fileName := adrID + ".md"
		now := time.Now().UTC().Format(time.RFC3339)

		adrDocs = append(adrDocs, doc)
		indexEntries = append(indexEntries, ADRIndexEntry{
			ADRID:        adrID,
			Title:        adrID,
			Status:       status,
			Path:         fileName,
			CoverageTier: firstNonEmptyStr(doc.FrontMatter.CoverageTier, "unit"),
			PRDRefs:      doc.FrontMatter.PRDRefs,
			CreatedAt:    now,
		})
	}

	if len(adrDocs) == 0 && len(failures) > 0 {
		return nil, nil, failures, fmt.Errorf("all %d ADR chunks failed to parse", len(failures))
	}
	return adrDocs, indexEntries, failures, nil
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func ParsePRDMarkdown(data []byte) (PRDDocument, error) {
	fields, body, err := parseFrontMatterRaw(data)
	if err != nil {
		return PRDDocument{}, fmt.Errorf("parse PRD: %w", err)
	}
	fm := PRDFrontMatter{
		SchemaVersion:     fields["schema_version"],
		PRDID:             fields["prd_id"],
		Status:            fields["status"],
		TaskID:            fields["task_id"],
		CreatedAt:         fields["created_at"],
		AcceptedAt:        fields["accepted_at"],
		AcceptedBy:        fields["accepted_by"],
		AcceptanceProfile: fields["acceptance_profile"],
	}
	doc := PRDDocument{FrontMatter: fm}
	doc.Requirements = parsePRDRequirements(body)
	if err := validatePRD(doc); err != nil {
		return PRDDocument{}, err
	}
	return doc, nil
}

func ParseADRIndexJSON(data []byte) (ADRIndex, error) {
	return parseAndValidate(data, "ADR index", validateADRIndex)
}

func ParseResearchProfileJSON(data []byte) (ResearchProfile, error) {
	return parseAndValidate(data, "research profile", validateResearchProfile)
}

func ParseADRMarkdown(data []byte) (ADRDocument, error) {
	fields, body, err := parseFrontMatterRaw(data)
	if err != nil {
		return ADRDocument{}, fmt.Errorf("parse ADR: %w", err)
	}
	fm := ADRFrontMatter{
		SchemaVersion:     fields["schema_version"],
		ADRID:             fields["adr_id"],
		Status:            fields["status"],
		PRDRefs:           parseFrontMatterList(fields, "prd_refs"),
		EvidenceRefs:      parseFrontMatterList(fields, "evidence_refs"),
		TDDObligations:    parseFrontMatterList(fields, "tdd_obligations"),
		CoverageTier:      fields["coverage_tier"],
		AcceptedAt:        fields["accepted_at"],
		AcceptedBy:        fields["accepted_by"],
		AcceptanceProfile: fields["acceptance_profile"],
		Supersedes:        parseFrontMatterList(fields, "supersedes"),
		SupersededBy:      parseFrontMatterList(fields, "superseded_by"),
	}
	sections := ADRSections{
		HasContext:                   strings.Contains(body, "## Context"),
		HasDecision:                  strings.Contains(body, "## Decision"),
		HasAlternativesConsidered:    strings.Contains(body, "## Alternatives Considered") || strings.Contains(body, "## Alternatives"),
		HasEvidence:                  strings.Contains(body, "## Evidence"),
		HasConsequences:              strings.Contains(body, "## Consequences"),
		HasCompatibilityAndMigration: strings.Contains(body, "## Compatibility and Migration") || strings.Contains(body, "## Compatibility"),
		HasTDDObligations:            strings.Contains(body, "## TDD Obligations"),
		HasRollback:                  strings.Contains(body, "## Rollback"),
		HasOpenQuestions:             strings.Contains(body, "## Open Questions"),
	}
	doc := ADRDocument{FrontMatter: fm, Sections: sections, BodyMarkdown: strings.TrimSpace(body)}
	if err := validateADR(doc); err != nil {
		return ADRDocument{}, err
	}
	return doc, nil
}

// stripQuotes removes surrounding double or single quotes from a value.
func stripQuotes(v string) string {
	for _, q := range []string{"\"", "'"} {
		if strings.HasPrefix(v, q) && strings.HasSuffix(v, q) && len(v) >= 2 {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func parseFrontMatterRaw(data []byte) (map[string]string, string, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return nil, "", errors.New("front matter delimiter --- not found at start")
	}
	end := strings.Index(text[3:], "---")
	if end < 0 {
		return nil, "", errors.New("closing front matter delimiter --- not found")
	}
	fmText := text[3 : end+3]
	body := text[end+6:]
	return parseYAMLFieldsMultiline(fmText), body, nil
}

func parseYAMLFieldsMultiline(text string) map[string]string {
	result := map[string]string{}
	lines := strings.Split(text, "\n")
	var currentKey string
	var listItems []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && currentKey != "" {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			listItems = append(listItems, item)
			continue
		}
		if currentKey != "" && len(listItems) > 0 {
			result[currentKey] = "[" + strings.Join(listItems, ", ") + "]"
		}
		currentKey = ""
		listItems = nil
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		// Strip surrounding double or single quotes from scalar values.
		val = stripQuotes(val)
		if val == "" {
			currentKey = key
			continue
		}
		result[key] = val
	}
	if currentKey != "" && len(listItems) > 0 {
		result[currentKey] = "[" + strings.Join(listItems, ", ") + "]"
	}
	return result
}

func parseFrontMatterList(fields map[string]string, key string) []string {
	val := fields[key]
	if val == "" {
		return nil
	}
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func parsePRDRequirements(body string) []PRDRequirement {
	var reqs []PRDRequirement
	lines := strings.Split(body, "\n")
	var current *PRDRequirement
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **id**:") {
			if current != nil {
				reqs = append(reqs, *current)
			}
			current = &PRDRequirement{}
			current.ID = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **id**:"))
		} else if current != nil {
			if strings.HasPrefix(trimmed, "- **priority**:") {
				current.Priority = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **priority**:"))
			} else if strings.HasPrefix(trimmed, "- **statement**:") {
				current.Statement = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **statement**:"))
			} else if strings.HasPrefix(trimmed, "- **evidence_refs**:") {
				current.EvidenceRefs = parseInlineArray(trimmed, "- **evidence_refs**:")
			} else if strings.HasPrefix(trimmed, "- **tdd_obligations**:") {
				current.TDDObligations = parseInlineArray(trimmed, "- **tdd_obligations**:")
			} else if strings.HasPrefix(trimmed, "- **owner_alignment**:") {
				current.OwnerAlignment = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **owner_alignment**:"))
			} else if strings.HasPrefix(trimmed, "- **testability**:") {
				current.Testability = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **testability**:"))
			} else if strings.HasPrefix(trimmed, "- **metric**:") {
				current.Metric = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **metric**:"))
			} else if strings.HasPrefix(trimmed, "- **open_questions**:") {
				current.OpenQuestions = parseInlineArray(trimmed, "- **open_questions**:")
				current.OpenQuestionsPresent = true
			}
		}
	}
	if current != nil {
		reqs = append(reqs, *current)
	}
	return reqs
}

func parseInlineArray(line, prefix string) []string {
	val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

var allowedComplexityTiers = map[string]bool{
	"C0": true, "C1": true, "C2": true, "C3": true, "C4": true,
}

func ParseImplementationReportJSON(data []byte) (ImplementationReport, error) {
	return parseAndValidate(data, "implementation report", validateImplementationReport)
}

func ParseReviewReportJSON(data []byte) (ReviewReport, error) {
	return parseAndValidate(data, "review report", validateReviewReport)
}

func ParseInvestigationReportJSON(data []byte) (InvestigationReport, error) {
	return parseAndValidate(data, "investigation report", validateInvestigationReport)
}
