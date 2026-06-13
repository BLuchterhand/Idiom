package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func implementationScopedReadFileRequestInExplicitScope(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) bool {
	if !implementationScopedEditSurfaceActive(brief, results) {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	return readRequestTargetsRequiredEditPath(req, implementationScopedExplicitReadScope(brief))
}

func (r *Runner) preflightScopedImplementationLargeWriteFileReplacement(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || r.Workspace == nil {
		return verificationInterceptResult{}
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameWriteFile {
		return verificationInterceptResult{}
	}

	if briefIsDocDeliverableLane(r.RuntimeBrief) {
		return verificationInterceptResult{}
	}

	if !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) {
		return verificationInterceptResult{}
	}

	relPath := scopedImplementationRequiredPath(req.Path, r.RuntimeBrief.RequiredEditPaths)
	if relPath == "" || writePathIsNonSource(relPath) {
		return verificationInterceptResult{}
	}

	absPath := filepath.Join(r.Workspace.Root(), filepath.FromSlash(relPath))
	info, err := os.Stat(absPath)

	if err != nil || info.IsDir() {
		return verificationInterceptResult{}
	}

	lineCount := 0
	byteCount := info.Size()

	if byteCount <= scopedImplementationLargeReadByteThreshold {
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			return verificationInterceptResult{}
		}

		lineCount = countTextLines(data)
		byteCount = int64(len(data))
	}

	if lineCount <= scopedImplementationLargeReadLineThreshold && byteCount <= scopedImplementationLargeReadByteThreshold {
		return verificationInterceptResult{}
	}

	detail := fmt.Sprintf("%d bytes", byteCount)
	if lineCount > 0 {
		detail = fmt.Sprintf("%d lines, %d bytes", lineCount, byteCount)
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    workspace.ToolNameWriteFile,
			Summary: fmt.Sprintf("Rejected large existing write_file replacement for %s", relPath),
			Error: fmt.Sprintf(
				"write_file would replace existing large scoped source path %s (%s). Use apply_patch or edit_file with old_text/new_text or start_line/end_line/content for a bounded edit. write_file is reserved here for missing/new files and non-source deliverables.",
				relPath,
				detail,
			),
		},
		Reason: fmt.Sprintf("rejected range-less full-file replacement on large scoped source path %s", relPath),
	}
}

func scopedImplementationRequiredPath(path string, required []string) string {
	cleaned := cleanRelativeRunnerPath(path)
	if cleaned == "" {
		return ""
	}

	for _, candidate := range required {
		if cleanRelativeRunnerPath(candidate) == cleaned {
			return cleaned
		}
	}

	return ""
}

func cleanRelativeRunnerPath(path string) string {
	normalized := normalizeRunnerPath(path)
	if normalized == "" || filepath.IsAbs(normalized) {
		return ""
	}

	cleaned := filepath.ToSlash(filepath.Clean(normalized))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}

	return cleaned
}

func countTextLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}

	lines := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		lines++
	}

	return lines
}

func (r *Runner) preflightScopedImplementationBoundedSourceRead(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || r.Workspace == nil {
		return verificationInterceptResult{}
	}

	if !implementationScopedReadFileRequestInExplicitScope(r.RuntimeBrief, results, req) {
		return verificationInterceptResult{}
	}

	if req.StartLine > 0 || req.EndLine > 0 {
		return verificationInterceptResult{}
	}

	relPath := normalizeRunnerPath(req.Path)
	if relPath == "" || strings.HasPrefix(relPath, "../") || relPath == ".." || filepath.IsAbs(relPath) {
		return verificationInterceptResult{}
	}

	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || strings.HasPrefix(relPath, "../") || relPath == ".." {
		return verificationInterceptResult{}
	}

	absPath := filepath.Join(r.Workspace.Root(), filepath.FromSlash(relPath))

	data, err := os.ReadFile(absPath)
	if err != nil {
		return verificationInterceptResult{}
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) <= scopedImplementationLargeReadLineThreshold && len(data) <= scopedImplementationLargeReadByteThreshold {
		return verificationInterceptResult{}
	}

	start, end, selector := scopedImplementationBoundedReadWindow(r.RuntimeBrief, relPath, lines)
	output := scopedImplementationBoundedReadOutput(r.RuntimeBrief, relPath, lines, start, end, selector)
	r.Workspace.MarkPathObserved(relPath)

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool: workspace.ToolNameReadFile,
			Summary: fmt.Sprintf(
				"Read %s lines %d-%d (bounded large required-path source; file has %d lines total; full range-less read omitted)",
				relPath, start, end, len(lines),
			),
			Output: output,
		},
		Reason: fmt.Sprintf("bounded range-less read_file on large scoped path %s to lines %d-%d of %d", relPath, start, end, len(lines)),
	}
}

func scopedImplementationBoundedReadWindow(brief workerruntime.WorkerRuntimeBrief, path string, lines []string) (int, int, string) {
	total := len(lines)
	if total <= 0 {
		return 1, 1, "empty file fallback"
	}

	terms := scopedImplementationSourceSelectionTerms(brief, path)
	if line := firstLineMatchingAnyTerm(lines, terms); line > 0 {
		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		return start, end, fmt.Sprintf("first task/path term match near line %d", line)
	}

	if line := firstDeclarationLine(lines); line > 0 {
		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		return start, end, fmt.Sprintf("first declaration fallback near line %d", line)
	}

	end := scopedImplementationBoundedReadWindowLines
	if end > total {
		end = total
	}

	return 1, end, "top-of-file fallback"
}

func scopedImplementationBoundedReadOutput(brief workerruntime.WorkerRuntimeBrief, path string, lines []string, start int, end int, selector string) string {
	total := len(lines)

	var b strings.Builder

	fmt.Fprintf(&b, "Bounded source packet for %s\n", path)
	fmt.Fprintf(&b, "- file_lines: %d\n", total)
	fmt.Fprintf(&b, "- returned_lines: %d-%d\n", start, end)
	fmt.Fprintf(&b, "- selector: %s\n", selector)
	fmt.Fprintf(&b, "- reason: range-less read_file on a large scoped required/evidence path was bounded to preserve context.\n")
	fmt.Fprintf(&b, "- next: request a smaller read_file range or a code_navigate read_symbol for the exact edit-safety fact you still need.\n")

	for _, suggestion := range scopedImplementationSuggestedReadRanges(brief, path, lines, start, end) {
		fmt.Fprintf(&b, "- suggested_next_read: %s\n", suggestion)
	}

	b.WriteString("\nSource lines:\n")

	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, truncateScopedImplementationBoundedReadLine(lines[i]))
	}

	return strings.TrimRight(b.String(), "\n")
}

func truncateScopedImplementationBoundedReadLine(line string) string {
	if len(line) <= scopedImplementationBoundedReadLineCharCap {
		return line
	}

	return fmt.Sprintf("%s... [truncated %d chars]", line[:scopedImplementationBoundedReadLineCharCap], len(line)-scopedImplementationBoundedReadLineCharCap)
}

func scopedImplementationSuggestedReadRanges(brief workerruntime.WorkerRuntimeBrief, path string, lines []string, returnedStart int, returnedEnd int) []string {
	terms := scopedImplementationSourceSelectionTerms(brief, path)
	total := len(lines)
	seen := map[string]struct{}{
		fmt.Sprintf("%d-%d", returnedStart, returnedEnd): {},
	}

	var suggestions []string

	add := func(line int, why string) {
		if line <= 0 || line > total || len(suggestions) >= 3 {
			return
		}

		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		key := fmt.Sprintf("%d-%d", start, end)

		if _, ok := seen[key]; ok {
			return
		}

		seen[key] = struct{}{}

		suggestions = append(suggestions, fmt.Sprintf(
			`{"tool":"read_file","path":%q,"start_line":%d,"end_line":%d,"named_gap":%q,"why_not_answering":"need bounded current source before the edit"}`,
			path, start, end, why,
		))
	}

	for _, line := range allDeclarationLinesMatchingAnyTerm(lines, terms, 6) {
		add(line, "inspect task-relevant declaration in large scoped file")
	}

	for _, line := range allLinesMatchingAnyTerm(lines, terms, 6) {
		add(line, "inspect task-relevant source range in large scoped file")
	}

	if len(suggestions) == 0 {
		add(1, "inspect file header/imports before selecting a narrower symbol or range")
	}

	return suggestions
}

func scopedImplementationSourceSelectionTerms(brief workerruntime.WorkerRuntimeBrief, path string) []string {
	seen := map[string]struct{}{}

	var terms []string

	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		term = strings.Trim(term, "`'\".,:;()[]{}")

		if len(term) < 4 || scopedImplementationSourceSelectionStopWord(term) {
			return
		}

		if _, ok := seen[term]; ok {
			return
		}

		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	text := strings.Join([]string{
		brief.Objective,
		strings.Join(brief.RequiredNext, " "),
		strings.Join(brief.SearchPolicy, " "),
		strings.Join(brief.Constraints, " "),
	}, " ")

	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 && !strings.Contains(match[1], "/") {
			add(match[1])
		}
	}

	for _, token := range codeShapedTokenPattern.FindAllString(text, -1) {
		add(token)
	}

	for _, token := range codeShapedSnakeCasePattern.FindAllString(text, -1) {
		add(token)

		for _, part := range strings.Split(token, "_") {
			add(part)
		}
	}

	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		add(token)
	}

	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	add(stem)

	for _, part := range strings.FieldsFunc(stem, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}) {
		add(part)
	}

	add(filepath.Base(path))

	if len(terms) > 16 {
		return terms[:16]
	}

	return terms
}

func scopedImplementationSourceSelectionStopWord(term string) bool {
	switch term {
	case "this", "that", "with", "from", "into", "file", "files", "path", "paths",
		"source", "read", "edit", "edits", "write", "writes", "scope", "scoped",
		"required", "implementation", "implement", "contract", "evidence", "exact",
		"task", "test", "tests", "need", "needs", "before", "after", "current", "main":
		return true
	default:
		return false
	}
}

func firstLineMatchingAnyTerm(lines []string, terms []string) int {
	for _, line := range allLinesMatchingAnyTerm(lines, terms, 1) {
		return line
	}

	return 0
}

func allLinesMatchingAnyTerm(lines []string, terms []string, max int) []int {
	if max <= 0 || len(terms) == 0 {
		return nil
	}

	var out []int

	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				out = append(out, i+1)
				break
			}
		}

		if len(out) >= max {
			return out
		}
	}

	return out
}

func firstDeclarationLine(lines []string) int {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") ||
			strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "const ") ||
			strings.HasPrefix(trimmed, "var ") ||
			strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "def ") {

			return i + 1
		}
	}

	return 0
}

func allDeclarationLinesMatchingAnyTerm(lines []string, terms []string, max int) []int {
	if max <= 0 || len(terms) == 0 {
		return nil
	}

	var out []int

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isDecl := strings.HasPrefix(trimmed, "func ") ||
			strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "const ") ||
			strings.HasPrefix(trimmed, "var ") ||
			strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "def ")

		if !isDecl {
			continue
		}

		lower := strings.ToLower(trimmed)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				out = append(out, i+1)
				break
			}
		}

		if len(out) >= max {
			return out
		}
	}

	return out
}

func centeredLineWindow(line int, total int, size int) (int, int) {
	if total <= 0 {
		return 1, 1
	}

	if size <= 0 {
		size = scopedImplementationBoundedReadWindowLines
	}

	if size > total {
		size = total
	}

	start := line - size/3
	if start < 1 {
		start = 1
	}

	end := start + size - 1
	if end > total {
		end = total
		start = end - size + 1

		if start < 1 {
			start = 1
		}
	}

	return start, end
}

// maxToolStepsExhaustedReason mirrors OpenCode's max-steps.txt: when the
// per-turn tool-step budget is spent, tools are disabled and the model must
// synthesize a text-only reply now from the evidence already gathered. The
// wording is deliberately blunt about both constraints — no more tool calls,
// and no empty responses — to steer small local models away from the
// "keep probing" loop that produced the empty-answer failure in
// logs/conversations/20260421-053044.479.jsonl.
