package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	goscan "go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"idiom/internal/platform/failures"
	"idiom/internal/platform/textutil"
)

type editRecoveryState struct {
	line   int
	detail string
}

type WriteGuard struct {
	mu                   sync.Mutex
	editRecoveryRequired map[string]editRecoveryState
}

func NewWriteGuard() WriteGuard {
	return WriteGuard{editRecoveryRequired: make(map[string]editRecoveryState)}
}

func (g *WriteGuard) RejectInvalidPlannedWrite(toolName string, summary string, path string, contents []byte, displayPath func(string) string) *ToolResult {
	if g == nil {
		return nil
	}

	before, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		result := toolFailureFromErrorResult(toolName, summary, fmt.Errorf("preflight could not read existing %s: %w", displayPathValue(displayPath, path), err), failures.Internal, reasonToolInternalFailure)
		return &result
	}

	return g.RejectPlannedWrite(toolName, summary, path, before, contents, displayPath)
}

func (g *WriteGuard) RejectPlannedWrite(toolName string, summary string, path string, before []byte, contents []byte, displayPath func(string) string) *ToolResult {
	if g == nil {
		return nil
	}

	if lineNo, line, ok := NewSuspiciousAdjacentDuplicateCodeLine(path, before, contents); ok {
		result := withToolFailure(ToolResult{
			Tool:    toolName,
			Summary: summary,
			Error: fmt.Sprintf(
				"preflight rejected %s because it would introduce adjacent duplicate code line %d: %q. "+
					"Combine adjacent edits into one replacement or include the existing line in old_text instead of re-declaring it.",
				displayPathValue(displayPath, path), lineNo, line,
			),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

		return &result
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, contents, parser.AllErrors); err != nil {
			line := syntaxErrorLine(err)
			g.NoteEditRecoveryRequired(path, line, err.Error())

			result := withToolFailure(ToolResult{
				Tool:    toolName,
				Summary: summary,
				Error:   formatSyntaxPreflightError(path, contents, line, err, displayPath),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

			return &result
		}
	case ".json":
		if !json.Valid(contents) {
			result := withToolFailure(ToolResult{
				Tool:    toolName,
				Summary: summary,
				Error:   fmt.Sprintf("preflight JSON validation failed for %s", displayPathValue(displayPath, path)),
			}, failures.InvalidArgument, reasonToolInvalidArgument)

			return &result
		}
	}

	return nil
}

func (g *WriteGuard) RejectPendingEditRecovery(toolName string, path string, displayPath func(string) string) *ToolResult {
	state, ok := g.PendingEditRecovery(path)
	if !ok {
		return nil
	}

	lineHint := ""
	if state.line > 0 {
		lineHint = fmt.Sprintf(" around line %d", state.line)
	}

	result := withToolFailure(ToolResult{
		Tool:    toolName,
		Summary: "Edit request rejected",
		Error: fmt.Sprintf(
			"previous syntax preflight failure on %s still needs evidence refresh%s. Required recovery evidence is a bounded failing-region read or path diff before retrying the anchored edit. Last validation error: %s",
			displayPathValue(displayPath, path),
			lineHint,
			state.detail,
		),
	}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

	return &result
}

func (g *WriteGuard) RejectUnobservedEdit(toolName string, path string, requestPath string, bypass bool, hasObserved func(string) bool, displayPath func(string) string) *ToolResult {
	if g == nil || bypass {
		return nil
	}

	display := displayPathValue(displayPath, path)
	if hasObserved != nil && (hasObserved(display) || hasObserved(requestPath)) {
		return nil
	}

	result := withToolFailure(ToolResult{
		Tool:    toolName,
		Summary: "Edit request rejected",
		Error:   fmt.Sprintf("edit_file requires you to have observed %s in this session before editing it.", display),
	}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

	return &result
}

// IsCompletedPlanningArtifact reports whether a markdown artifact already
// declares a closed status. Whole-file replacement of these files is rejected
// by write_file/submit_plan; bounded edit_file updates remain available.
func IsCompletedPlanningArtifact(path string, content []byte) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".md" && ext != ".markdown" {
		return false
	}

	if !looksLikePlanningArtifactPath(path) {
		return false
	}

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if i >= 120 {
			break
		}

		if statusLineMarksCompleted(line) {
			return true
		}
	}

	return false
}

func looksLikePlanningArtifactPath(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	for _, segment := range strings.Split(normalized, "/") {
		switch segment {
		case ".planning", "planning", "plans", "campaigns", "atoms", "atom", "specs", "spec", "tasks", "tickets", "work-packages", "work_packages":
			return true
		}
	}

	base := strings.TrimSuffix(filepath.Base(normalized), filepath.Ext(normalized))

	return strings.Contains(base, "spec") || strings.Contains(base, "atom") || strings.Contains(base, "task")
}

func statusLineMarksCompleted(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(lower, "status") {
		return false
	}

	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, token := range tokens {
		switch token {
		case "complete", "completed", "accepted", "done", "shipped":
			return true
		}
	}

	return false
}

// NewSuspiciousAdjacentDuplicateCodeLine reports the first adjacent duplicate
// assignment/declaration line that appears in after but not before. It is a
// source-hygiene invariant, not a style lint: adjacent duplicate assignments
// almost always mean an anchored edit reintroduced nearby context.
func NewSuspiciousAdjacentDuplicateCodeLine(path string, before []byte, after []byte) (int, string, bool) {
	if !isSourceLikePath(path) {
		return 0, "", false
	}

	beforeCounts := adjacentDuplicateCodeLineCounts(string(before))
	beforeTotal := adjacentDuplicateBaselineLineCount(string(before))
	seen := make(map[string]int)
	seenTotal := 0
	previous := ""

	for i, raw := range strings.Split(string(after), "\n") {
		line := suspiciousDuplicateCodeLine(raw)
		if line == "" {
			previous = ""
			continue
		}

		if line == previous {
			seen[line]++

			seenTotal++

			if seenTotal > beforeTotal && seen[line] > beforeCounts[line] {
				return i + 1, line, true
			}
		}

		previous = line
	}

	return 0, "", false
}

func adjacentDuplicateCodeLineCounts(content string) map[string]int {
	counts := make(map[string]int)
	previous := ""

	for _, raw := range strings.Split(content, "\n") {
		line := suspiciousDuplicateCodeLine(raw)
		if line == "" {
			previous = ""
			continue
		}

		if line == previous {
			counts[line]++
		}

		previous = line
	}

	return counts
}

func adjacentDuplicateBaselineLineCount(content string) int {
	count := 0
	previous := ""

	for _, raw := range strings.Split(content, "\n") {
		line := duplicateCodeLineBaseline(raw)
		if line == "" {
			previous = ""
			continue
		}

		if line == previous {
			count++
		}

		previous = line
	}

	return count
}

func suspiciousDuplicateCodeLine(line string) string {
	line = duplicateCodeLineBaseline(line)
	if line == "" {
		return ""
	}

	if !hasAssignmentOperator(line) {
		return ""
	}

	return line
}

func duplicateCodeLineBaseline(line string) string {
	line = strings.TrimSpace(line)
	if len(line) < 8 {
		return ""
	}

	for _, prefix := range []string{"//", "#", "/*", "*", "*/", "import ", "package ", "#include"} {
		if strings.HasPrefix(line, prefix) {
			return ""
		}
	}

	return line
}

func hasAssignmentOperator(line string) bool {
	for i, r := range line {
		if r != '=' {
			continue
		}

		prev := rune(0)
		next := rune(0)

		if i > 0 {
			prev = rune(line[i-1])
		}

		if i+1 < len(line) {
			next = rune(line[i+1])
		}

		if prev == '=' || prev == '!' || prev == '<' || prev == '>' || next == '=' || next == '>' {
			continue
		}

		return true
	}

	return false
}

func isSourceLikePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx",
		".m", ".mm", ".rs", ".java", ".kt", ".kts", ".swift", ".cs",
		".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".php", ".gd",
		".sh", ".bash", ".zsh":
		return true
	default:
		return false
	}
}

func (g *WriteGuard) NoteEditRecoveryRequired(path string, line int, detail string) {
	if g == nil {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.editRecoveryRequired == nil {
		g.editRecoveryRequired = make(map[string]editRecoveryState)
	}

	g.editRecoveryRequired[normalizePathKey(path)] = editRecoveryState{line: line, detail: strings.TrimSpace(detail)}
}

func (g *WriteGuard) PendingEditRecovery(path string) (editRecoveryState, bool) {
	if g == nil {
		return editRecoveryState{}, false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.editRecoveryRequired == nil {
		return editRecoveryState{}, false
	}

	state, ok := g.editRecoveryRequired[normalizePathKey(path)]

	return state, ok
}

func (g *WriteGuard) ClearEditRecovery(path string) {
	if g == nil {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.editRecoveryRequired, normalizePathKey(path))
}

func (g *WriteGuard) ClearAllEditRecovery() {
	if g == nil {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	for key := range g.editRecoveryRequired {
		delete(g.editRecoveryRequired, key)
	}
}

func (g *WriteGuard) ClearEditRecoveryAfterRead(path string, startLine int, endLine int) {
	state, ok := g.PendingEditRecovery(path)
	if !ok {
		return
	}

	if state.line > 0 && (startLine > state.line || endLine < state.line) {
		return
	}

	g.ClearEditRecovery(path)
}

func formatSyntaxPreflightError(path string, contents []byte, line int, err error, displayPath func(string) string) string {
	display := displayPathValue(displayPath, path)

	var b strings.Builder

	if line > 0 {
		fmt.Fprintf(&b, "preflight syntax validation failed for %s at line %d: %v", display, line, err)
	} else {
		fmt.Fprintf(&b, "preflight syntax validation failed for %s: %v", display, err)
	}

	if context := plannedWriteLineContext(contents, line, 2); context != "" {
		b.WriteString("\n")
		b.WriteString(context)
	}

	if line > 0 {
		fmt.Fprintf(&b, "\nBefore retrying the anchored edit on %s, refresh evidence for the failing region around line %d with a bounded source read or path diff.", display, line)
	} else {
		fmt.Fprintf(&b, "\nBefore retrying the anchored edit on %s, refresh evidence with a bounded source read or path diff.", display)
	}

	return b.String()
}

func displayPathValue(displayPath func(string) string, path string) string {
	if displayPath == nil {
		return path
	}

	return displayPath(path)
}

func syntaxErrorLine(err error) int {
	var list goscan.ErrorList

	if errors.As(err, &list) && len(list) > 0 {
		return list[0].Pos.Line
	}

	var scanErr *goscan.Error

	if errors.As(err, &scanErr) {
		return scanErr.Pos.Line
	}

	return 0
}

func plannedWriteLineContext(contents []byte, line int, radius int) string {
	if line <= 0 {
		return ""
	}

	lines := strings.Split(string(contents), "\n")
	if line > len(lines) {
		return ""
	}

	if radius < 0 {
		radius = 0
	}

	start := line - radius
	if start < 1 {
		start = 1
	}

	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder

	b.WriteString("planned content around syntax error:\n")

	for i := start; i <= end; i++ {
		marker := " "
		if i == line {
			marker = ">"
		}

		fmt.Fprintf(&b, "%s %d:%s\n", marker, i, textutil.TruncateBytes(strings.TrimRight(lines[i-1], "\r\n"), searchContentLineCap, lineTruncationMarker))
	}

	return strings.TrimRight(b.String(), "\n")
}
