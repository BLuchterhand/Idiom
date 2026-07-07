package implementationdispatch

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/atomintake"
)

type PlanDocumentAtom struct {
	Path            string
	Change          string
	Excerpt         string
	Anchors         []string
	TestObligations []string
}

func CanAtomizePlanDocuments(planDocuments []PlanDocument) bool {
	for _, doc := range planDocuments {
		if IsAtomizedPlanDocumentPath(doc.Path) {
			continue
		}

		if len(PlanDocumentAtoms(doc)) > 0 {
			return true
		}
	}

	return false
}

func PlanDocumentsShouldPreAtomize(planDocuments []PlanDocument) bool {
	for _, doc := range planDocuments {
		if IsAtomizedPlanDocumentPath(doc.Path) {
			continue
		}

		atoms := PlanDocumentAtoms(doc)
		if len(atoms) < 8 {
			continue
		}

		distinctFiles := map[string]struct{}{}

		for _, atom := range atoms {
			path := strings.TrimSpace(atom.Path)
			if path != "" {
				distinctFiles[path] = struct{}{}
			}
		}

		if len(distinctFiles) >= 6 {
			return true
		}
	}

	return false
}

func PlanDocumentAtoms(doc PlanDocument) []PlanDocumentAtom {
	if IsAtomizedPlanDocumentPath(doc.Path) {
		return nil
	}

	atoms := PlanDocumentAtomsFromText(doc.Content, doc.Path)
	if len(atoms) > 0 {
		return attachDesignDocTestObligations(atoms, doc.TestObligations, doc.Path)
	}

	for _, path := range doc.EditPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		atoms = append(atoms, PlanDocumentAtom{
			Path:    path,
			Change:  "Implement the source design document slice for this path.",
			Excerpt: path,
		})
	}

	return attachDesignDocTestObligations(atoms, doc.TestObligations, doc.Path)
}

func PlanDocumentAtomsFromText(text string, sourceRef string) []PlanDocumentAtom {
	headingSet := map[string]struct{}{}

	for _, heading := range pathparse.PlanFileScopeHeadings() {
		normalized := NormalizeMarkdownHeading(heading)
		if normalized != "" {
			headingSet[normalized] = struct{}{}
		}
	}

	var atoms []PlanDocumentAtom

	active := false
	sawScopeLine := false

	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		normalized := NormalizeMarkdownHeading(line)

		if _, ok := headingSet[normalized]; ok {
			active = true
			sawScopeLine = false

			continue
		}

		if !active {
			continue
		}

		if line == "" {
			if sawScopeLine {
				break
			}

			continue
		}

		if sawScopeLine && LooksLikeMarkdownHeading(line) {
			break
		}

		if cells, ok := markdownTableCells(line); ok {
			if len(cells) == 0 || markdownTableSeparatorCell(cells[0]) {
				continue
			}

			paths := pathparse.MentionedWorkspacePathsInMarkdownTableFirstCell(line)
			if len(paths) == 0 {
				continue
			}

			change := ""
			if len(cells) > 1 {
				change = strings.TrimSpace(cells[1])
			}

			sawScopeLine = true

			for _, path := range paths {
				path, ok := pathparse.NormalizeWorkspacePathToken(path)
				if !ok || path == sourceRef || !LooksLikeConcreteFile(path) {
					continue
				}

				atoms = append(atoms, PlanDocumentAtom{
					Path:    path,
					Change:  change,
					Excerpt: strings.TrimSpace(line),
				})
			}

			continue
		}

		paths := pathparse.MentionedWorkspacePaths(line)
		if len(paths) == 0 {
			continue
		}

		sawScopeLine = true

		for _, path := range paths {
			path, ok := pathparse.NormalizeWorkspacePathToken(path)
			if !ok || path == sourceRef || !LooksLikeConcreteFile(path) {
				continue
			}

			atoms = append(atoms, PlanDocumentAtom{
				Path:    path,
				Change:  strings.TrimSpace(line),
				Excerpt: strings.TrimSpace(line),
			})
		}
	}

	return atoms
}

func attachDesignDocTestObligations(atoms []PlanDocumentAtom, obligations []string, sourceRef string) []PlanDocumentAtom {
	if len(atoms) == 0 || len(obligations) == 0 {
		return atoms
	}

	for _, obligation := range obligations {
		obligation = strings.TrimSpace(obligation)
		if obligation == "" {
			continue
		}

		assigned := false

		for _, path := range ObligationConcretePaths(obligation, sourceRef) {
			matched := false

			for i := range atoms {
				if atoms[i].Path == path {
					atoms[i].TestObligations = appendUniqueStrings(atoms[i].TestObligations, obligation)
					matched = true
					assigned = true
				}
			}

			if !matched {
				atoms = append(atoms, PlanDocumentAtom{
					Path:            path,
					Change:          "Implement the source design document test obligation.",
					Excerpt:         obligation,
					TestObligations: []string{obligation},
				})
				assigned = true
			}
		}

		if assigned {
			continue
		}

		for i := range atoms {
			if atomMatchesTestObligation(atoms[i], obligation) {
				atoms[i].TestObligations = appendUniqueStrings(atoms[i].TestObligations, obligation)
				assigned = true
			}
		}

		if !assigned {
			for i := range atoms {
				if atomPathLooksTest(atoms[i].Path) {
					atoms[i].TestObligations = appendUniqueStrings(atoms[i].TestObligations, obligation)
				}
			}
		}
	}

	return atoms
}

func ObligationConcretePaths(obligation string, sourceRef string) []string {
	var paths []string

	for _, candidate := range pathparse.MentionedWorkspacePaths(obligation) {
		path, ok := pathparse.NormalizeWorkspacePathToken(candidate)
		if !ok || path == sourceRef || !LooksLikeConcreteFile(path) {
			continue
		}

		paths = appendUniqueStrings(paths, path)
	}

	return paths
}

func atomMatchesTestObligation(atom PlanDocumentAtom, obligation string) bool {
	path := strings.ToLower(strings.TrimSpace(atom.Path))
	if path == "" {
		return false
	}

	lower := strings.ToLower(obligation)
	dir := strings.Trim(filepath.ToSlash(filepath.Dir(path)), ".")

	if dir != "" && strings.Contains(lower, "./"+dir) {
		return true
	}

	if strings.Contains(lower, path) {
		return true
	}

	return atomPathLooksTest(path) && LineLooksLikeTestObligation(obligation)
}

func atomPathLooksTest(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))

	return strings.Contains(lower, "_test.") ||
		strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.HasSuffix(lower, "_test.go")
}

func markdownTableCells(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") || strings.Count(trimmed, "|") < 2 {
		return nil, false
	}

	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")

	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))

	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}

	return cells, len(cells) > 0
}

func markdownTableSeparatorCell(cell string) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return false
	}

	for _, r := range cell {
		if r != '-' && r != ':' && r != ' ' {
			return false
		}
	}

	return strings.Contains(cell, "-")
}

func MaterializePlanDocumentAtoms(root string, planDocuments []PlanDocument) ([]PlanDocument, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workspace root is unavailable")
	}

	var atomDocuments []PlanDocument

	for _, doc := range planDocuments {
		if IsAtomizedPlanDocumentPath(doc.Path) {
			atomDocuments = append(atomDocuments, doc)
			continue
		}

		atoms := PlanDocumentAtoms(doc)
		if len(atoms) == 0 {
			atomDocuments = append(atomDocuments, doc)
			continue
		}

		title := DocumentOutputTitle(doc.Path)
		dirRel := filepath.ToSlash(filepath.Join(".idiom", "atoms", title))
		dirAbs, ok := WorkspaceFile(root, dirRel)

		if !ok {
			return nil, fmt.Errorf("atom output path is outside the workspace: %s", dirRel)
		}

		if err := os.MkdirAll(dirAbs, 0o755); err != nil {
			return nil, err
		}

		for i, atom := range atoms {
			atom = PlanDocumentAtomWithWorkspaceShape(root, atom)

			rel := filepath.ToSlash(filepath.Join(dirRel, fmt.Sprintf("atom-%03d.md", i+1)))
			abs, ok := WorkspaceFile(root, rel)

			if !ok {
				return nil, fmt.Errorf("atom output path is outside the workspace: %s", rel)
			}

			if existing, ok := ReadPlanDocument(root, rel); ok &&
				PlanDocumentMatchesAtomPath(existing, atom.Path) &&
				PlanDocumentIsTerminalAtom(existing) {

				atomDocuments = append(atomDocuments, existing)
				continue
			}

			body := ComposePlanDocumentAtom(doc, atom, i+1, len(atoms))
			if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
				return nil, err
			}

			atomDoc, ok := ReadPlanDocument(root, rel)
			if !ok {
				return nil, fmt.Errorf("materialized atom was not readable: %s", rel)
			}

			atomDoc.AuthorizedNewFiles = AtomAuthorizedNewFiles(doc, atom.Path)
			atomDocuments = append(atomDocuments, atomDoc)
		}
	}

	return atomDocuments, nil
}

func PlanDocumentMatchesAtomPath(doc PlanDocument, atomPath string) bool {
	atomPath = strings.TrimSpace(atomPath)
	if atomPath == "" {
		return false
	}

	for _, path := range doc.EditPaths {
		if strings.TrimSpace(path) == atomPath {
			return true
		}
	}

	return false
}

func PlanDocumentIsTerminalAtom(doc PlanDocument) bool {
	_, ok := PlanDocumentTerminalAtomStatus(doc)
	return ok
}

func PlanDocumentTerminalAtomStatus(doc PlanDocument) (string, bool) {
	if !IsAtomizedPlanDocumentPath(doc.Path) {
		return "", false
	}

	return atomintake.SpecTerminalStatus(doc.Content)
}

func PlanDocumentAtomWithWorkspaceShape(root string, atom PlanDocumentAtom) PlanDocumentAtom {
	path := strings.TrimSpace(atom.Path)
	if path == "" || atomChangeLooksLikeNewFile(atom.Change) {
		return atom
	}

	abs, ok := WorkspaceFile(root, path)
	if !ok {
		return atom
	}

	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		atom.Anchors = PlanDocumentAtomCurrentStateAnchors(root, atom)
		return atom
	}

	change := strings.TrimSpace(atom.Change)
	if change == "" {
		change = "create this file from the source design document slice"
	}

	atom.Change = "NEW FILE: " + change

	return atom
}

func PlanDocumentAtomCurrentStateAnchors(root string, atom PlanDocumentAtom) []string {
	path := strings.TrimSpace(atom.Path)
	abs, ok := WorkspaceFile(root, path)

	if !ok {
		return nil
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(body), "\n")
	terms := planDocumentAtomAnchorTerms(atom)
	candidates := planDocumentAtomAnchorCandidates(lines, terms)

	if len(candidates) < 3 {
		candidates = appendAtomAnchorFallbacks(candidates, planDocumentAtomFallbackAnchorCandidates(lines), 3)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}

		return candidates[i].Line < candidates[j].Line
	})

	if len(candidates) > 3 {
		candidates = candidates[:3]
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Line < candidates[j].Line
	})

	anchors := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		anchors = append(anchors, fmt.Sprintf("- `%s:%d`: `%s`", path, candidate.Line, strings.ReplaceAll(candidate.Text, "`", "'")))
	}

	return anchors
}

type planDocumentAtomAnchorCandidate struct {
	Line  int
	Text  string
	Score int
}

func appendAtomAnchorFallbacks(candidates []planDocumentAtomAnchorCandidate, fallbacks []planDocumentAtomAnchorCandidate, limit int) []planDocumentAtomAnchorCandidate {
	seen := map[int]struct{}{}
	for _, candidate := range candidates {
		seen[candidate.Line] = struct{}{}
	}

	for _, fallback := range fallbacks {
		if len(candidates) >= limit {
			break
		}

		if _, ok := seen[fallback.Line]; ok {
			continue
		}

		candidates = append(candidates, fallback)
		seen[fallback.Line] = struct{}{}
	}

	return candidates
}

func planDocumentAtomAnchorCandidates(lines []string, terms map[string]struct{}) []planDocumentAtomAnchorCandidate {
	if len(terms) == 0 {
		return nil
	}

	var candidates []planDocumentAtomAnchorCandidate

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		score := 0
		lower := strings.ToLower(trimmed)

		for term := range terms {
			if strings.Contains(lower, term) {
				score++
			}
		}

		if score == 0 {
			continue
		}

		candidates = append(candidates, planDocumentAtomAnchorCandidate{
			Line:  i + 1,
			Text:  strings.TrimSpace(trimmed),
			Score: score,
		})
	}

	return candidates
}

func planDocumentAtomFallbackAnchorCandidates(lines []string) []planDocumentAtomAnchorCandidate {
	var candidates []planDocumentAtomAnchorCandidate

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		candidates = append(candidates, planDocumentAtomAnchorCandidate{
			Line:  i + 1,
			Text:  strings.TrimSpace(trimmed),
			Score: 1,
		})
		if len(candidates) >= 3 {
			return candidates
		}
	}

	return candidates
}

func planDocumentAtomAnchorTerms(atom PlanDocumentAtom) map[string]struct{} {
	text := strings.ToLower(strings.Join(append([]string{atom.Change, atom.Excerpt}, atom.TestObligations...), " "))
	terms := map[string]struct{}{}

	for _, raw := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		term := strings.Trim(raw, "_")
		if len(term) < 3 || ignoredAnchorTerm(term) {
			continue
		}

		terms[term] = struct{}{}
	}

	return terms
}

func ignoredAnchorTerm(term string) bool {
	switch term {
	case "the", "and", "for", "with", "this", "that", "from", "into", "file", "files", "path", "paths", "source", "design", "document", "doc", "scope", "slice", "atom", "edit", "edits", "modify", "change", "changes", "implementation", "implement":
		return true
	default:
		return false
	}
}

func atomChangeLooksLikeNewFile(change string) bool {
	lower := strings.ToLower(strings.TrimSpace(change))
	if lower == "" {
		return false
	}

	for _, prefix := range []string{
		"new file:",
		"new file target:",
		"new-file target:",
		"create file:",
		"create new file:",
		"add new file:",
		"write a new",
		"write new",
		"produce a new",
		"generate a new",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

func ComposePlanDocumentAtom(source PlanDocument, atom PlanDocumentAtom, index int, total int) string {
	path := strings.TrimSpace(atom.Path)
	change := strings.TrimSpace(atom.Change)

	if change == "" {
		change = "Implement this file's bounded slice from the source design document."
	}

	excerpt := strings.TrimSpace(atom.Excerpt)
	if excerpt == "" {
		excerpt = path
	}

	title := strings.TrimSuffix(filepath.Base(source.Path), filepath.Ext(source.Path))
	if title == "" || title == "." {
		title = "design-document"
	}

	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString(fmt.Sprintf(" atom %03d\n\n", index))
	b.WriteString("Status: pending\n\n")
	b.WriteString(fmt.Sprintf("<!-- idiom:design-doc-atom source=%q index=%d total=%d -->\n\n", source.Path, index, total))
	b.WriteString("## Files To Modify\n\n")
	b.WriteString("| File | Change |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| ")
	b.WriteString(path)
	b.WriteString(" | ")
	b.WriteString(strings.ReplaceAll(change, "\n", " "))
	b.WriteString(" |\n\n")
	b.WriteString("## Verified Current State\n\n")
	b.WriteString("- Source design document: `")
	b.WriteString(source.Path)
	b.WriteString("`.\n")
	b.WriteString("- The source design document lists `")
	b.WriteString(path)
	b.WriteString("` in its implementation scope.\n")

	if len(atom.Anchors) > 0 {
		b.WriteString("- Host current-state anchors captured before dispatch:\n")

		for _, anchor := range atom.Anchors {
			b.WriteString("  ")
			b.WriteString(anchor)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString("## Required Behavior\n\n")
	b.WriteString("Implement only this atom's bounded slice for `")
	b.WriteString(path)
	b.WriteString("`, preserving the source design document's intended behavior.\n\n")
	b.WriteString("Source scope row:\n\n")
	b.WriteString("> ")
	b.WriteString(strings.ReplaceAll(excerpt, "\n", "\n> "))
	b.WriteString("\n\n")

	if len(AtomAuthorizedNewFiles(source, path)) > 0 {
		b.WriteString(plancontract.PromptPlanExecutionContractMarker)
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("{\"authorized_new_files\":[{\"path\":%q}]}\n", path))
		b.WriteString(plancontract.PromptAtomExecutionContractMarker)
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("{\"atom_id\":\"atom-%03d\"}\n\n", index))
	}

	b.WriteString("Dispatch contract:\n\n")
	b.WriteString("- If the Host current-state anchors already show this atom's requested final state, close the refined brief as `Status: source-satisfied - do not dispatch` and cite the exact path evidence; do not invent a no-op edit.\n")
	b.WriteString("- If this atom has Design Doc Test Obligations, source-satisfied closeout must cite matching test evidence as well as source evidence.\n")
	b.WriteString("- If work remains for an existing file, state executable edit shape only: exact `old_text`/`new_text` blocks or `start_line`/`end_line` plus replacement `content`. Do not use prose-only mutation sites such as \"insert after\", \"append near\", or \"update around\" without that edit shape.\n")
	b.WriteString("- If this is a new file, provide the full file body as `replace_all:true` with empty `old_text` and complete `new_text`.\n\n")

	if len(atom.TestObligations) > 0 {
		b.WriteString("## Design Doc Test Obligations\n\n")
		b.WriteString("These obligations were extracted from the source design document and are active for this atom:\n\n")

		for _, obligation := range atom.TestObligations {
			obligation = strings.TrimSpace(obligation)
			if obligation == "" {
				continue
			}

			b.WriteString("- ")
			b.WriteString(obligation)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	b.WriteString("## Acceptance\n\n")
	b.WriteString("- The requested behavior is implemented for `")
	b.WriteString(path)
	b.WriteString("`.\n")

	if len(atom.TestObligations) > 0 {
		b.WriteString("- The Design Doc Test Obligations for this atom are represented in source and named in closeout evidence.\n")
	}

	b.WriteString("- No files outside this atom's Files To Modify list are edited.\n\n")
	b.WriteString("## Source Design Document\n\n")
	b.WriteString("Reference only. This section preserves design intent; the active write scope remains this atom's Files To Modify list.\n\n")
	b.WriteString(QuoteReference(textutil.TruncateWithByteCount(source.Content, 30000)))

	return b.String()
}

func AtomAuthorizedNewFiles(doc PlanDocument, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	authorizedPaths := append([]string{}, doc.AuthorizedNewFiles...)
	authorizedPaths = append(authorizedPaths, plancontract.AuthorizedNewFilePathsFromPrompt(doc.Content)...)

	for _, authorized := range appendUniqueStrings(nil, authorizedPaths...) {
		if strings.TrimSpace(authorized) == path {
			return []string{path}
		}
	}

	return nil
}

func DocumentOutputTitle(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "plan-document"
	}

	path = strings.TrimSuffix(path, filepath.Ext(path))

	var b strings.Builder

	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	title := strings.Trim(b.String(), "-")
	if title == "" {
		title = "plan-document"
	}

	title = limitDocumentOutputTitle(title)

	return "tui-" + title
}

func limitDocumentOutputTitle(title string) string {
	const maxTitleBytes = 80

	if len(title) <= maxTitleBytes {
		return title
	}

	h := fnv.New64a()

	_, _ = h.Write([]byte(title))

	suffix := fmt.Sprintf("%016x", h.Sum64())
	prefixLen := maxTitleBytes - len(suffix) - 1

	if prefixLen < 1 {
		return suffix
	}

	prefix := strings.Trim(title[:prefixLen], "-._")
	if prefix == "" {
		return suffix
	}

	return prefix + "-" + suffix
}
