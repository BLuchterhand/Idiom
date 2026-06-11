package comprehension

import (
	"fmt"
	"strings"
)

// Compaction notes:
//
// CompactPackProjection is the canonical model-facing projection of a
// PackResult. It is derived from the full PackResult by the shared
// NewCompactPackProjection builder. All model-facing surfaces MUST use
// this type and the shared RenderCompactPackProjection renderer.
//
// The compact projection is localization evidence, not source-body
// evidence. The model must still verify exact anchors with
// code_navigate, read_symbol, extract_body, or bounded read_file
// before source writes.

// tokenEstimateDivisor is the conservative characters-per-token
// divisor used by estimateTokens. 3.5 ≈ conservative for English +
// code; the contract is "approximate cap", not byte-exact budget.
const tokenEstimateDivisor = 3.5

// CompactPackProjection is the canonical model-facing projection of a
// PackResult. It is derived from the full PackResult by the shared
// compact-projection builder.
type CompactPackProjection struct {
	PackID           string              `json:"pack_id"`
	Fresh            bool                `json:"fresh"`
	Quality          CompactPackQuality  `json:"quality"`
	CandidateFiles   []CompactPackFile   `json:"candidate_files"`
	CandidateSymbols []CompactPackSymbol `json:"candidate_symbols"`
	Tests            []CompactPackTest   `json:"tests"`
	UnresolvedGaps   []string            `json:"unresolved_gaps"`
	// Budget tracking (set by builder, not from PackResult)
	BudgetTokens    int `json:"budget_tokens,omitempty"`
	TotalCandidates int `json:"total_candidates"`
	OmittedFiles    int `json:"omitted_files"`
	OmittedSymbols  int `json:"omitted_symbols"`
	OmittedTests    int `json:"omitted_tests"`
}

// CompactPackQuality carries localization quality metadata.
type CompactPackQuality struct {
	LocalizationConfidence string   `json:"localization_confidence"`
	SourceOwnerFound       bool     `json:"source_owner_found"`
	EvidencePaths          []string `json:"evidence_paths"`
	Reasons                []string `json:"reasons"`
}

// CompactPackFile is a compact file candidate entry.
type CompactPackFile struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// CompactPackSymbol is a compact symbol candidate entry.
type CompactPackSymbol struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Path          string `json:"path"`
	Kind          string `json:"kind"`
	StartLine     int    `json:"start_line"`
	Reason        string `json:"reason"`
}

// CompactPackTest is a compact test candidate entry.
type CompactPackTest struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Symbol  string `json:"symbol"`
	Reason  string `json:"reason"`
}

// NewCompactPackProjection builds a CompactPackProjection from a full
// PackResult. It copies the quality, candidate files/symbols/tests,
// gaps, and budget fields into the compact form.
func NewCompactPackProjection(result PackResult) CompactPackProjection {
	files := make([]CompactPackFile, len(result.CandidateFiles))
	for i, f := range result.CandidateFiles {
		files[i] = CompactPackFile{
			Path:   f.Path,
			Role:   f.Role,
			Reason: f.Reason,
		}
	}
	symbols := make([]CompactPackSymbol, len(result.CandidateSymbols))
	for i, s := range result.CandidateSymbols {
		symbols[i] = CompactPackSymbol{
			Name:          s.QualifiedName,
			QualifiedName: s.QualifiedName,
			Path:          s.Path,
			Kind:          s.Kind,
			StartLine:     s.StartLine,
			Reason:        s.Reason,
		}
	}
	tests := make([]CompactPackTest, len(result.Tests))
	for i, t := range result.Tests {
		tests[i] = CompactPackTest{
			Command: t.Command,
			Path:    t.Path,
			Symbol:  t.Symbol,
			Reason:  t.Reason,
		}
	}
	gaps := make([]string, len(result.UnresolvedGaps))
	copy(gaps, result.UnresolvedGaps)

	total := len(files) + len(symbols) + len(tests)
	return CompactPackProjection{
		PackID: result.PackID,
		Fresh:  result.Fresh,
		Quality: CompactPackQuality{
			LocalizationConfidence: result.Quality.LocalizationConfidence,
			SourceOwnerFound:       result.Quality.SourceOwnerFound,
			EvidencePaths:          append([]string(nil), result.Quality.EvidencePaths...),
			Reasons:                append([]string(nil), result.Quality.Reasons...),
		},
		CandidateFiles:   files,
		CandidateSymbols: symbols,
		Tests:            tests,
		UnresolvedGaps:   gaps,
		BudgetTokens:     result.BudgetTokens,
		TotalCandidates:  total,
	}
}

// RenderCompactPackProjection renders a CompactPackProjection as
// model-facing text with budget enforcement.
//
// When budgetTokens > 0, the renderer enforces budget as a hard output
// cap using progressive measurement: mandatory header and evidence_note
// are rendered first; then each file, symbol, test, and gap is rendered
// one at a time, with token estimate checked after each. Omitted-item
// counts are populated on the returned projection struct.
//
// When budgetTokens <= 0, no budget enforcement is applied and all
// items are rendered (existing unbounded behavior). The returned
// projection has zero omitted counts.
//
// Edge case: if the mandatory header + evidence_note together exceed
// budget, the header and evidence_note are still returned (they are
// mandatory) with a truncation note and all items omitted. The output
// MAY exceed budget in this case — the contract guarantees the header
// is preserved.
//
// Token counting uses estimateTokens (chars / 3.5) as a conservative
// approximation. The contract is "approximate cap", not byte-exact.
func RenderCompactPackProjection(p CompactPackProjection, budgetTokens int) (string, CompactPackProjection) {
	var b strings.Builder

	// Mandatory header: summary line, pack_id, fresh, quality.
	b.WriteString("context_pack_summary:\n")
	if strings.TrimSpace(p.PackID) != "" {
		b.WriteString("pack_id: " + strings.TrimSpace(p.PackID) + "\n")
	}
	fmt.Fprintf(&b, "fresh: %t\n", p.Fresh)
	writeCompactQuality(&b, p.Quality)

	// Unbounded path — render everything, no omitted counts.
	if budgetTokens <= 0 {
		writeCompactFileList(&b, "candidate_files", p.CandidateFiles, len(p.CandidateFiles))
		writeCompactSymbolList(&b, p.CandidateSymbols, len(p.CandidateSymbols))
		writeCompactTestList(&b, p.Tests, len(p.Tests))
		if len(p.UnresolvedGaps) > 0 {
			writeCompactGaps(&b, p.UnresolvedGaps, len(p.UnresolvedGaps))
		}
		b.WriteString("evidence_note: context_pack is localization evidence, not source-body evidence.\n")
		return strings.TrimSpace(b.String()), p
	}

	// evidenceNote is a fixed mandatory trailer.
	const evidenceNote = "evidence_note: context_pack is localization evidence, not source-body evidence.\n"
	evidenceTokens := estimateTokens(evidenceNote)

	// omittedMinReserve reserves budget for at least a minimal omitted
	// summary ("omitted:\n" + one omitted-count line) so truncation is
	// always visible. 35 tokens ≈ 122 chars.
	const omittedMinReserve = 35

	// If mandatory header + evidence_note + omitted reserve together
	// exceed budget, return header + truncation note + evidence_note
	// and omit all items. Use > so that exact equality still renders
	// the omitted summary (items get zero allocation).
	if estimateTokens(b.String())+evidenceTokens+omittedMinReserve > budgetTokens {
		b.WriteString("truncation_note: budget exhausted by header; all candidates omitted.\n")
		b.WriteString(evidenceNote)
		p.OmittedFiles = len(p.CandidateFiles)
		p.OmittedSymbols = len(p.CandidateSymbols)
		p.OmittedTests = len(p.Tests)
		out := strings.TrimSpace(b.String())
		// Record token savings: compute full unbounded rendering
		// and add the delta to the atomic counter.
		fullOut, _ := RenderCompactPackProjection(p, 0)
		recordEstimatedFullTokensSaved(out, fullOut)
		return out, p
	}

	// Effective budget for candidate items: budget minus both the
	// mandatory evidence_note and a reserve for the omitted summary.
	itemBudget := budgetTokens - evidenceTokens - omittedMinReserve

	// Render files, symbols, tests, and gaps progressively.
	renderedFiles := renderFileSection(&b, p.CandidateFiles, itemBudget)
	renderedSymbols := renderSymbolSection(&b, p.CandidateSymbols, itemBudget)
	renderedTests := renderTestSection(&b, p.Tests, itemBudget)
	renderedGaps := renderGapSection(&b, p.UnresolvedGaps, itemBudget)

	p.OmittedFiles = len(p.CandidateFiles) - renderedFiles
	p.OmittedSymbols = len(p.CandidateSymbols) - renderedSymbols
	p.OmittedTests = len(p.Tests) - renderedTests

	// Omitted summary. At least one omitted line is guaranteed to fit
	// because omittedMinReserve was reserved from the item budget.
	// Additional lines are rendered only if they fit within the
	// remaining budget.
	if p.OmittedFiles > 0 || p.OmittedSymbols > 0 || p.OmittedTests > 0 || renderedGaps < len(p.UnresolvedGaps) {
		b.WriteString("omitted:\n")
		remaining := budgetTokens - estimateTokens(b.String()) - evidenceTokens
		if remaining < 0 {
			remaining = 0
		}

		if p.OmittedFiles > 0 {
			line := fmt.Sprintf("- candidate_files: %d omitted (rendered %d of %d)\n",
				p.OmittedFiles, renderedFiles, len(p.CandidateFiles))
			if remaining <= 0 || estimateTokens(line) <= remaining {
				b.WriteString(line)
				remaining -= estimateTokens(line)
			}
		}
		if p.OmittedSymbols > 0 {
			line := fmt.Sprintf("- candidate_symbols: %d omitted (rendered %d of %d)\n",
				p.OmittedSymbols, renderedSymbols, len(p.CandidateSymbols))
			if remaining <= 0 || estimateTokens(line) <= remaining {
				b.WriteString(line)
				remaining -= estimateTokens(line)
			}
		}
		if p.OmittedTests > 0 {
			line := fmt.Sprintf("- tests: %d omitted (rendered %d of %d)\n",
				p.OmittedTests, renderedTests, len(p.Tests))
			if remaining <= 0 || estimateTokens(line) <= remaining {
				b.WriteString(line)
				remaining -= estimateTokens(line)
			}
		}
		gapOmitted := len(p.UnresolvedGaps) - renderedGaps
		if gapOmitted > 0 {
			line := fmt.Sprintf("- context_gaps: %d omitted (rendered %d of %d)\n",
				gapOmitted, renderedGaps, len(p.UnresolvedGaps))
			if remaining <= 0 || estimateTokens(line) <= remaining {
				b.WriteString(line)
			}
		}
	}

	b.WriteString(evidenceNote)
	out := strings.TrimSpace(b.String())
	// Record token savings: compute full unbounded rendering
	// and add the delta to the atomic counter.
	fullOut, _ := RenderCompactPackProjection(p, 0)
	recordEstimatedFullTokensSaved(out, fullOut)
	return out, p
}

// checking. Returns the number of files actually rendered.
func renderFileSection(b *strings.Builder, files []CompactPackFile, budgetTokens int) int {
	if len(files) == 0 {
		return 0
	}
	if budgetTokens < 1 {
		budgetTokens = 1
	}
	var sectionBuf strings.Builder
	sectionBuf.WriteString("candidate_files:\n")
	rendered := 0
	for _, f := range files {
		var itemBuf strings.Builder
		renderOneCompactFile(&itemBuf, f)
		candidate := b.String() + sectionBuf.String() + itemBuf.String()
		if estimateTokens(candidate) > budgetTokens {
			break
		}
		sectionBuf.WriteString(itemBuf.String())
		rendered++
	}
	if rendered > 0 {
		b.WriteString(sectionBuf.String())
	}
	return rendered
}

// renderSymbolSection renders candidate_symbols with progressive budget
// checking. Returns the number of symbols actually rendered.
func renderSymbolSection(b *strings.Builder, symbols []CompactPackSymbol, budgetTokens int) int {
	if len(symbols) == 0 {
		return 0
	}
	if budgetTokens < 1 {
		budgetTokens = 1
	}
	var sectionBuf strings.Builder
	sectionBuf.WriteString("candidate_symbols:\n")
	rendered := 0
	for _, s := range symbols {
		var itemBuf strings.Builder
		renderOneCompactSymbol(&itemBuf, s)
		candidate := b.String() + sectionBuf.String() + itemBuf.String()
		if estimateTokens(candidate) > budgetTokens {
			break
		}
		sectionBuf.WriteString(itemBuf.String())
		rendered++
	}
	if rendered > 0 {
		b.WriteString(sectionBuf.String())
	}
	return rendered
}

// renderTestSection renders tests with progressive budget checking.
// Returns the number of tests actually rendered.
func renderTestSection(b *strings.Builder, tests []CompactPackTest, budgetTokens int) int {
	if len(tests) == 0 {
		return 0
	}
	if budgetTokens < 1 {
		budgetTokens = 1
	}
	var sectionBuf strings.Builder
	sectionBuf.WriteString("tests:\n")
	rendered := 0
	for _, t := range tests {
		var itemBuf strings.Builder
		renderOneCompactTest(&itemBuf, t)
		candidate := b.String() + sectionBuf.String() + itemBuf.String()
		if estimateTokens(candidate) > budgetTokens {
			break
		}
		sectionBuf.WriteString(itemBuf.String())
		rendered++
	}
	if rendered > 0 {
		b.WriteString(sectionBuf.String())
	}
	return rendered
}

// renderGapSection renders context_gaps with progressive budget
// checking. Returns the number of gaps actually rendered.
func renderGapSection(b *strings.Builder, gaps []string, budgetTokens int) int {
	if len(gaps) == 0 {
		return 0
	}
	if budgetTokens < 1 {
		budgetTokens = 1
	}
	var sectionBuf strings.Builder
	sectionBuf.WriteString("context_gaps:\n")
	rendered := 0
	for _, g := range gaps {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		var itemBuf strings.Builder
		itemBuf.WriteString("- " + g + "\n")
		candidate := b.String() + sectionBuf.String() + itemBuf.String()
		if estimateTokens(candidate) > budgetTokens {
			break
		}
		sectionBuf.WriteString(itemBuf.String())
		rendered++
	}
	if rendered > 0 {
		b.WriteString(sectionBuf.String())
	}
	return rendered
}

// renderOneCompactFile writes a single candidate_file line.
func renderOneCompactFile(b *strings.Builder, f CompactPackFile) {
	path := strings.TrimSpace(f.Path)
	if path == "" {
		return
	}
	detail := firstNonEmptyCompactString(f.Role, f.Reason)
	if detail != "" {
		fmt.Fprintf(b, "- %s: %s\n", path, truncateCompactLine(detail, 180))
	} else {
		fmt.Fprintf(b, "- %s\n", path)
	}
}

// renderOneCompactSymbol writes a single candidate_symbol line.
func renderOneCompactSymbol(b *strings.Builder, s CompactPackSymbol) {
	path := strings.TrimSpace(s.Path)
	name := firstNonEmptyCompactString(s.Name, s.QualifiedName, s.Kind)
	if path == "" && name == "" {
		return
	}
	lineSuffix := ""
	if s.StartLine > 0 {
		lineSuffix = fmt.Sprintf(" line %d", s.StartLine)
	}
	reason := strings.TrimSpace(s.Reason)
	if name != "" {
		if reason != "" {
			fmt.Fprintf(b, "- %s in %s%s: %s\n",
				truncateCompactLine(name, 120), path, lineSuffix,
				truncateCompactLine(reason, 100))
		} else {
			fmt.Fprintf(b, "- %s in %s%s\n",
				truncateCompactLine(name, 120), path, lineSuffix)
		}
	} else {
		fmt.Fprintf(b, "- %s%s\n", path, lineSuffix)
	}
}

// renderOneCompactTest writes a single test line.
func renderOneCompactTest(b *strings.Builder, t CompactPackTest) {
	path := strings.TrimSpace(t.Path)
	if path == "" {
		return
	}
	detail := firstNonEmptyCompactString(t.Command, t.Reason)
	if detail != "" {
		fmt.Fprintf(b, "- %s: %s\n", path, truncateCompactLine(detail, 180))
	} else {
		fmt.Fprintf(b, "- %s\n", path)
	}
}

// estimateTokens returns a conservative token count estimate using
// characters / 3.5. This is backend-agnostic; the contract is
// "approximate cap", not a precise guarantee.
func estimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return int(float64(len(text)) / tokenEstimateDivisor)
}

// recordEstimatedFullTokensSaved computes the estimated token savings
// from compact projection (full unbounded rendering minus actual
// budget-enforced rendering) and atomically adds the delta to the
// process-local counter. It is a no-op when actual >= full.
func recordEstimatedFullTokensSaved(actualOutput, fullOutput string) {
	fullTokens := estimateTokens(fullOutput)
	actualTokens := estimateTokens(actualOutput)
	if fullTokens > actualTokens {
		estimatedFullTokensSaved.Add(int64(fullTokens - actualTokens))
	}
}

func writeCompactQuality(b *strings.Builder, q CompactPackQuality) {
	b.WriteString("packet_quality:\n")
	confidence := strings.TrimSpace(q.LocalizationConfidence)
	if confidence != "" {
		b.WriteString("- localization_confidence: " + confidence + "\n")
	} else {
		b.WriteString("- localization_confidence: unknown\n")
	}
	fmt.Fprintf(b, "- source_owner_found: %t\n", q.SourceOwnerFound)
	if len(q.EvidencePaths) > 0 {
		b.WriteString("- strongest_evidence_paths:\n")
		for _, path := range q.EvidencePaths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			b.WriteString("  - " + path + "\n")
		}
	}
	if len(q.Reasons) > 0 {
		b.WriteString("- reasons: " + truncateCompactLine(strings.Join(q.Reasons, ", "), 180) + "\n")
	}
}

func writeCompactFileList(b *strings.Builder, label string, files []CompactPackFile, max int) {
	if len(files) == 0 {
		return
	}
	b.WriteString(label + ":\n")
	for i, file := range files {
		if i >= max {
			break
		}
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		detail := firstNonEmptyCompactString(file.Role, file.Reason)
		if detail != "" {
			fmt.Fprintf(b, "- %s: %s\n", path, truncateCompactLine(detail, 180))
		} else {
			fmt.Fprintf(b, "- %s\n", path)
		}
	}
}

func writeCompactSymbolList(b *strings.Builder, symbols []CompactPackSymbol, max int) {
	if len(symbols) == 0 {
		return
	}
	b.WriteString("candidate_symbols:\n")
	for i, sym := range symbols {
		if i >= max {
			break
		}
		path := strings.TrimSpace(sym.Path)
		name := firstNonEmptyCompactString(sym.Name, sym.QualifiedName, sym.Kind)
		if path == "" && name == "" {
			continue
		}
		lineSuffix := ""
		if sym.StartLine > 0 {
			lineSuffix = fmt.Sprintf(" line %d", sym.StartLine)
		}
		reason := strings.TrimSpace(sym.Reason)
		if name != "" {
			if reason != "" {
				fmt.Fprintf(b, "- %s in %s%s: %s\n",
					truncateCompactLine(name, 120), path, lineSuffix,
					truncateCompactLine(reason, 100))
			} else {
				fmt.Fprintf(b, "- %s in %s%s\n",
					truncateCompactLine(name, 120), path, lineSuffix)
			}
		} else {
			fmt.Fprintf(b, "- %s%s\n", path, lineSuffix)
		}
	}
}

func writeCompactTestList(b *strings.Builder, tests []CompactPackTest, max int) {
	if len(tests) == 0 {
		return
	}
	b.WriteString("tests:\n")
	for i, test := range tests {
		if i >= max {
			break
		}
		path := strings.TrimSpace(test.Path)
		if path == "" {
			continue
		}
		detail := firstNonEmptyCompactString(test.Command, test.Reason)
		if detail != "" {
			fmt.Fprintf(b, "- %s: %s\n", path, truncateCompactLine(detail, 180))
		} else {
			fmt.Fprintf(b, "- %s\n", path)
		}
	}
}

func writeCompactGaps(b *strings.Builder, gaps []string, limit int) {
	if len(gaps) == 0 {
		return
	}
	b.WriteString("context_gaps:\n")
	for i, gap := range gaps {
		if i >= limit {
			break
		}
		gap = strings.TrimSpace(gap)
		if gap == "" {
			continue
		}
		b.WriteString("- " + gap + "\n")
	}
}

func firstNonEmptyCompactString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncateCompactLine(value string, limit int) string {
	if limit > 0 && len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}
