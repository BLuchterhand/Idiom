package implementationdispatch

import (
	"os"
	"strings"

	atomintakecontract "idiom/internal/contracts/atomintake"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/atomintake"
	"idiom/internal/workflows/planexecute"
)

type SourceSatisfiedRecord struct {
	DocumentPath    string
	EditPath        string
	Evidence        []string
	TestObligations []string
	TestEvidence    []string
}

type TerminalAtomRecord struct {
	DocumentPath string
	Status       string
}

type preflightSourceSatisfiedRecord struct {
	Path         string
	AtomState    atomintakecontract.IntakeState
	Blockers     []string
	LintBlockers []string
}

type SourceSatisfiedCloseout struct {
	Message string
	Count   int
}

func TryPlanDocumentsSourceSatisfiedCloseout(root string, planDocuments []PlanDocument) (SourceSatisfiedCloseout, bool) {
	records := PlanDocumentsSourceSatisfied(root, planDocuments)
	if len(records) == 0 || len(records) != len(planDocuments) {
		return SourceSatisfiedCloseout{}, false
	}

	if markRoot := strings.TrimSpace(root); markRoot != "" {
		for _, record := range records {
			var evidence []string

			evidence = append(evidence, record.Evidence...)
			evidence = append(evidence, record.TestEvidence...)
			CloseAtomDocument(markRoot, record.DocumentPath, "source-satisfied - do not dispatch", "Source-satisfied closeout recorded by TUI design-doc dispatch.", evidence)
		}
	}

	return SourceSatisfiedCloseout{
		Message: humanSourceSatisfiedMessage(records),
		Count:   len(records),
	}, true
}

func TryPreflightSourceSatisfiedCloseout(root string, results []PlanDocumentPreflightResult) (SourceSatisfiedCloseout, bool) {
	if len(results) == 0 {
		return SourceSatisfiedCloseout{}, false
	}

	records := make([]preflightSourceSatisfiedRecord, 0, len(results))
	for _, result := range results {
		record := preflightSourceSatisfiedRecord{
			Path:         result.Path,
			AtomState:    result.AtomState,
			Blockers:     append([]string(nil), result.Blockers...),
			LintBlockers: append([]string(nil), result.LintBlockers...),
		}
		if record.AtomState != atomintakecontract.StateSourceSatisfied {
			return SourceSatisfiedCloseout{}, false
		}
		records = append(records, record)
	}

	if markRoot := strings.TrimSpace(root); markRoot != "" {
		for _, record := range records {
			evidence := append([]string(nil), record.Blockers...)

			evidence = append(evidence, record.LintBlockers...)
			CloseAtomDocument(markRoot, record.Path, "source-satisfied - do not dispatch", "Atom intake reported this atom as source-satisfied before plan-execute dispatch.", evidence)
		}
	}

	return SourceSatisfiedCloseout{
		Message: humanPreflightSourceSatisfiedMessage(records),
		Count:   len(records),
	}, true
}

func PlanDocumentsSourceSatisfied(root string, planDocuments []PlanDocument) []SourceSatisfiedRecord {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}

	var records []SourceSatisfiedRecord

	for _, doc := range planDocuments {
		record, ok := PlanDocumentSourceSatisfied(root, doc)
		if !ok {
			return nil
		}

		records = append(records, record)
	}

	return records
}

func PlanDocumentsTerminalAtoms(planDocuments []PlanDocument) []TerminalAtomRecord {
	var records []TerminalAtomRecord

	for _, doc := range planDocuments {
		status, ok := PlanDocumentTerminalAtomStatus(doc)
		if !ok {
			return nil
		}

		records = append(records, TerminalAtomRecord{
			DocumentPath: doc.Path,
			Status:       status,
		})
	}

	return records
}

func PlanDocumentSourceSatisfied(root string, doc PlanDocument) (SourceSatisfiedRecord, bool) {
	if !IsAtomizedPlanDocumentPath(doc.Path) {
		return SourceSatisfiedRecord{}, false
	}

	atoms := PlanDocumentAtomsFromText(doc.Content, doc.Path)
	if len(atoms) != 1 {
		return SourceSatisfiedRecord{}, false
	}

	atom := atoms[0]

	atom.TestObligations = appendUniqueStrings(atom.TestObligations, doc.TestObligations...)

	path := strings.TrimSpace(atom.Path)

	if path == "" {
		return SourceSatisfiedRecord{}, false
	}

	scopeText := atomintake.SourceScopeRowText(doc.Content)

	if strings.TrimSpace(scopeText) == "" {
		scopeText = strings.Join([]string{atom.Change, atom.Excerpt}, " ")
	}

	sourceSatisfied, ok := atomintake.EvaluateSourceSatisfiedScope(atomintake.SourceSatisfiedScopeOptions{
		ProjectRoot: root,
		SpecPath:    doc.Path,
		SpecBody:    doc.Content,
		ScopePath:   path,
		ScopeText:   scopeText,
	})
	if !ok {
		return SourceSatisfiedRecord{}, false
	}

	testEvidence, ok := SourceSatisfiedTestEvidence(root, doc, atom)
	if !ok {
		return SourceSatisfiedRecord{}, false
	}

	return SourceSatisfiedRecord{
		DocumentPath:    doc.Path,
		EditPath:        sourceSatisfied.Path,
		Evidence:        sourceSatisfied.Evidence,
		TestObligations: atom.TestObligations,
		TestEvidence:    testEvidence,
	}, true
}

func SourceSatisfiedTestEvidence(root string, doc PlanDocument, atom PlanDocumentAtom) ([]string, bool) {
	obligations := appendUniqueStrings(append([]string(nil), atom.TestObligations...), doc.TestObligations...)
	if len(obligations) == 0 {
		return nil, true
	}

	var evidence []string

	for _, obligation := range obligations {
		obligation = strings.TrimSpace(obligation)
		if obligation == "" {
			continue
		}

		itemEvidence, ok := TestObligationEvidence(root, atom.Path, obligation)
		if !ok {
			return nil, false
		}

		evidence = append(evidence, itemEvidence)
	}

	return appendUniqueStrings(nil, evidence...), true
}

func TestObligationEvidence(root string, atomPath string, obligation string) (string, bool) {
	if LineLooksLikeVerificationCommand(obligation) {
		return "", false
	}

	paths := ObligationConcretePaths(obligation, "")
	if len(paths) == 0 {
		paths = []string{strings.TrimSpace(atomPath)}
	}

	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		abs, ok := WorkspaceFile(root, path)
		if !ok {
			continue
		}

		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}

		content := string(body)
		testNames := TestNamesFromText(obligation)

		for _, testName := range testNames {
			if evidence, ok := atomintake.LiteralSourceEvidence(path, content, testName); ok {
				return strings.Replace(evidence, "current source contains", "current test source contains", 1), true
			}
		}

		if len(testNames) > 0 {
			continue
		}

		if evidence, ok := atomintake.TermCoverageLineEvidence(path, content, obligation, "current test source covers design-doc test obligation", 2); ok {
			return evidence, true
		}
	}

	return "", false
}

func TestNamesFromText(text string) []string {
	var names []string

	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		if len(token) <= len("Test") || !strings.HasPrefix(token, "Test") {
			continue
		}

		next := token[len("Test")]
		if !((next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == '_') {
			continue
		}

		names = appendUniqueStrings(names, token)
	}

	return names
}

func humanSourceSatisfiedMessage(records []SourceSatisfiedRecord) string {
	var b strings.Builder

	b.WriteString("Decision: source-satisfied\n\n")
	b.WriteString("The referenced design-doc atom is already present in current source; plan-execute was not dispatched.")

	for _, record := range records {
		b.WriteString("\n- `")
		b.WriteString(record.EditPath)
		b.WriteString("`")

		if strings.TrimSpace(record.DocumentPath) != "" {
			b.WriteString(" from `")
			b.WriteString(record.DocumentPath)
			b.WriteString("`")
		}

		for _, evidence := range record.Evidence {
			evidence = strings.TrimSpace(evidence)
			if evidence == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(evidence)
		}

		if len(record.TestObligations) > 0 {
			b.WriteString("\n  - Test obligations:")

			for _, obligation := range record.TestObligations {
				obligation = strings.TrimSpace(obligation)
				if obligation == "" {
					continue
				}

				b.WriteString("\n    - ")
				b.WriteString(obligation)
			}
		}

		for _, evidence := range record.TestEvidence {
			evidence = strings.TrimSpace(evidence)
			if evidence == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(evidence)
		}
	}

	return strings.TrimSpace(b.String())
}

func HumanTerminalAtomMessage(records []TerminalAtomRecord) string {
	var b strings.Builder

	b.WriteString("Decision: source-satisfied\n\n")
	b.WriteString("The referenced design-doc atom is already closed in the atom tracker; plan-execute was not dispatched.")

	for _, record := range records {
		path := strings.TrimSpace(record.DocumentPath)
		if path == "" {
			path = "(unknown atom)"
		}

		status := strings.TrimSpace(record.Status)
		if status == "" {
			status = "closed"
		}

		b.WriteString("\n- `")
		b.WriteString(path)
		b.WriteString("`: ")
		b.WriteString(status)
	}

	return strings.TrimSpace(b.String())
}

func humanPreflightSourceSatisfiedMessage(records []preflightSourceSatisfiedRecord) string {
	var b strings.Builder

	b.WriteString("Decision: source-satisfied\n\n")
	b.WriteString("Atom intake classified the referenced brief as already satisfied; plan-execute was not dispatched.")

	for _, record := range records {
		path := strings.TrimSpace(record.Path)
		if path == "" {
			path = "(unknown brief)"
		}

		b.WriteString("\n- `")
		b.WriteString(path)
		b.WriteString("`")

		for _, blocker := range record.Blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(blocker)
		}
	}

	return strings.TrimSpace(b.String())
}

func HumanPendingVerificationCommandMessage(doc PlanDocument, commands []string) string {
	var b strings.Builder

	b.WriteString("<<QUESTION>>: Design-doc verification command obligations remain unproven, so this atom was not marked complete.")

	if strings.TrimSpace(doc.Path) != "" {
		b.WriteString("\n\nAtom: `")
		b.WriteString(strings.TrimSpace(doc.Path))
		b.WriteString("`")
	}

	b.WriteString("\n\nPending verification commands:")

	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}

		b.WriteString("\n- `")
		b.WriteString(command)
		b.WriteString("`")
	}

	return strings.TrimSpace(b.String())
}

func MarkCompletedAtomDocumentForResult(root string, doc PlanDocument, answer string, result *planexecute.RunResult) {
	root = strings.TrimSpace(root)
	if root == "" {
		return
	}

	if PlanExecuteResultBlocksAtomCompletion(result) ||
		(result == nil && AnswerLooksBlocked(answer)) {
		return
	}

	if len(UnprovenVerificationCommands(doc, answer)) > 0 {
		return
	}

	status := "completed"
	summary := "Completed by plan-execute design-doc atom dispatch."

	if strings.Contains(strings.ToLower(answer), "decision: source-satisfied") {
		status = "source-satisfied - do not dispatch"
		summary = "Plan-execute reported source-satisfied closeout for this design-doc atom."
	}

	CloseAtomDocument(root, doc.Path, status, summary, CloseoutEvidenceFromAnswer(answer))
}

func PlanExecuteResultBlocksAtomCompletion(result *planexecute.RunResult) bool {
	if result == nil {
		return false
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.FailureKind) != "" || strings.TrimSpace(result.FailureReason) != "" {
		return true
	}
	if result.Completion == nil {
		return false
	}

	record := plancontract.NormalizePlanExecuteTerminalCompletionRecord(*result.Completion)
	if len(record.ValidationFailures) > 0 {
		return true
	}

	terminalClass, _ := plancontract.ClassifyPlanExecuteTerminalCompletion(record)
	return terminalClass != plancontract.PlanExecuteTerminalClassSuccess
}

func AnswerLooksBlocked(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	return strings.HasPrefix(lower, "<<question>>") ||
		strings.Contains(lower, "<<question>>") ||
		strings.Contains(lower, "plan-execute could not produce a plan artifact") ||
		strings.Contains(lower, "planner phase did not produce a plan artifact") ||
		strings.Contains(lower, "planner phase reported a blocker") ||
		strings.Contains(lower, "required edit paths still need a plan") ||
		strings.Contains(lower, "blocked dispatch") ||
		strings.Contains(lower, "blocked before the planner ran") ||
		strings.Contains(lower, "automatic refine did not produce") ||
		strings.Contains(lower, "still blocked by preflight")
}

func CloseAtomDocument(root string, relPath string, status string, summary string, evidence []string) {
	relPath = strings.TrimSpace(relPath)
	if !IsAtomizedPlanDocumentPath(relPath) {
		return
	}

	abs, ok := WorkspaceFile(root, relPath)
	if !ok {
		return
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return
	}

	content := SetAtomStatusLine(string(body), status)

	content = SetAtomResultSection(content, status, summary, evidence)

	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return
	}
}

func SetAtomStatusLine(content string, status string) string {
	statusLine := "Status: " + strings.TrimSpace(status)
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.Trim(strings.TrimSpace(line), "* ")
		parts := strings.SplitN(trimmed, ":", 2)

		if len(parts) == 2 && textutil.EqualTrimFold(parts[0], "status") {
			lines[i] = statusLine
			return strings.Join(lines, "\n")
		}
	}

	if len(lines) > 0 {
		return lines[0] + "\n\n" + statusLine + "\n" + strings.Join(lines[1:], "\n")
	}

	return statusLine + "\n"
}

func SetAtomResultSection(content string, status string, summary string, evidence []string) string {
	sourceIdx := strings.Index(content, "\n## Source Design Document\n")
	prefix := content
	suffix := ""

	if sourceIdx >= 0 {
		prefix = content[:sourceIdx]
		suffix = content[sourceIdx:]
	}

	if resultIdx := strings.Index(prefix, "\n## Result\n"); resultIdx >= 0 {
		prefix = prefix[:resultIdx]
	}

	var b strings.Builder

	b.WriteString(strings.TrimRight(prefix, "\n"))
	b.WriteString("\n\n## Result\n\n")
	b.WriteString("Status: ")
	b.WriteString(strings.TrimSpace(status))
	b.WriteString("\n\n")

	if strings.TrimSpace(summary) != "" {
		b.WriteString(strings.TrimSpace(summary))
		b.WriteString("\n\n")
	}

	if len(evidence) > 0 {
		b.WriteString("Evidence:\n")

		for _, item := range evidence {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}

			b.WriteString("- ")
			b.WriteString(textutil.TruncateWithByteCount(item, 1000))
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	b.WriteString(strings.TrimLeft(suffix, "\n"))

	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}

	return b.String()
}

func CloseoutEvidenceFromAnswer(answer string) []string {
	var evidence []string

	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line == "" {
			continue
		}

		if strings.Contains(line, "`") || strings.Contains(strings.ToLower(line), "test obligation") || strings.Contains(strings.ToLower(line), "verification") {
			evidence = append(evidence, line)
		}

		if len(evidence) >= 12 {
			break
		}
	}

	return evidence
}

func UnprovenVerificationCommands(doc PlanDocument, answer string) []string {
	var pending []string

	for _, obligation := range doc.TestObligations {
		obligation = strings.TrimSpace(obligation)
		if obligation == "" || !LineLooksLikeVerificationCommand(obligation) {
			continue
		}

		if AnswerHasVerificationCommandEvidence(answer, obligation) {
			continue
		}

		pending = appendUniqueStrings(pending, strings.Trim(strings.TrimSpace(obligation), "`"))
	}

	return pending
}

func AnswerHasVerificationCommandEvidence(answer string, command string) bool {
	command = strings.ToLower(strings.Trim(strings.TrimSpace(command), "`"))
	if command == "" {
		return false
	}

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if !strings.Contains(line, command) {
			continue
		}

		for _, marker := range []string{"pass", "passed", "success", "succeeded", "exit 0", "ok "} {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}

	return false
}
