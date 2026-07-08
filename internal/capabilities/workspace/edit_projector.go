package workspace

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/platform/failures"
	"idiom/internal/platform/textutil"
)

type editProjection struct {
	updated     string
	shouldWrite bool
	result      ToolResult
}

func projectEditFile(path string, display string, current string, req ToolRequest) editProjection {
	if req.StartLine > 0 || req.EndLine > 0 {
		if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.OldText) != "" {
			// Some model/tool-call adapters include line metadata alongside
			// the exact-match shape. Prefer the explicit old_text/new_text
			// replacement instead of treating empty line-range content as a
			// deletion or ignoring new_text.
		} else if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.NewText) != "" {
			return editProjection{result: withToolFailure(ToolResult{
				Tool:    ToolNameEditFile,
				Summary: "Edit request rejected",
				Error: fmt.Sprintf(
					"edit_file line-range edits use the `content` field, not `new_text`. "+
						"Either set content to the replacement lines for %s lines %d-%d, "+
						"or provide old_text with new_text and omit the line range.",
					display, req.StartLine, req.EndLine,
				),
			}, failures.InvalidArgument, reasonToolInvalidArgument)}
		} else {
			return projectLineRangeEdit(display, current, req)
		}
	}

	if strings.TrimSpace(req.OldText) == "" {
		displayError := missingEditAnchorError(path, display, req)
		return editProjection{result: toolInvalidArgumentResult(ToolNameEditFile, "Edit request rejected", displayError)}
	}

	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		if containsSearchContentLineTruncationMarker(req.OldText) {
			return editProjection{result: withToolFailure(ToolResult{
				Tool:    ToolNameEditFile,
				Summary: "Edit request rejected",
				Error:   fmt.Sprintf("old_text for %s contains a search_content truncation marker. Truncated search output is not an exact edit anchor; use read_file with the match line's start_line/end_line to copy exact source, or use edit_file start_line/end_line+content.", display),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)}
		}

		if textutil.ContainsFold(current, req.OldText) {
			return editProjection{result: withToolFailure(ToolResult{
				Tool:    ToolNameEditFile,
				Summary: "Edit request rejected",
				Error:   fmt.Sprintf("old_text did not appear in %s, but a case-insensitive match exists. Preserve exact casing from the file when copying anchors, or switch to the start_line/end_line+content form.", display),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)}
		}

		return editProjection{result: withToolFailure(ToolResult{
			Tool:    ToolNameEditFile,
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text did not appear in %s. Copy the exact substring from the file (including whitespace) or switch to the start_line/end_line+content form.", display),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)}
	}

	if matchCount > 1 && !req.ReplaceAll {
		return editProjection{result: withToolFailure(ToolResult{
			Tool:    ToolNameEditFile,
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text matched %d locations in %s. Either extend old_text with surrounding context so it matches exactly one place, use start_line/end_line+content for a listed range, or set replace_all=true when every occurrence is intended.", matchCount, display),
			Output:  formatOldTextOccurrenceGuidance(display, current, req.OldText, matchCount),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)}
	}

	replaced := 1
	updated := strings.Replace(current, req.OldText, req.NewText, 1)

	if req.ReplaceAll {
		replaced = matchCount
		updated = strings.ReplaceAll(current, req.OldText, req.NewText)
	}

	if updated == current {
		return editProjection{result: ToolResult{
			Tool:    ToolNameEditFile,
			Summary: fmt.Sprintf("No-op edit for %s", display),
			Output:  "replacement produced no content change",
		}}
	}

	return editProjection{
		updated:     updated,
		shouldWrite: true,
		result: ToolResult{
			Tool:         ToolNameEditFile,
			Summary:      fmt.Sprintf("Edited %s", display),
			Output:       fmt.Sprintf("replaced occurrences: %d", replaced),
			ChangedPaths: []string{display},
		},
	}
}

func missingEditAnchorError(path string, display string, req ToolRequest) string {
	hasContent := strings.TrimSpace(req.Content) != ""

	if hasContent && editRecoveryPathLooksSourceLike(path) {
		return fmt.Sprintf(
			"edit_file needs either old_text (for an exact-match replace) or start_line+end_line (for a range replace). "+
				"You provided `content` with neither.\n"+
				"For an existing source/test file, use a bounded edit_file shape instead of whole-file replacement:\n"+
				"  (a) exact-match: use path %q, old_text copied from the file, complete new_text, and a concrete description.\n"+
				"  (b) line range: use path %q, concrete start_line/end_line values, complete replacement content, and a concrete description.\n"+
				"Do not re-submit the same call shape.",
			display, display,
		)
	}

	if hasContent {
		return fmt.Sprintf(
			"edit_file needs either old_text (for an exact-match replace) or start_line+end_line (for a range replace). "+
				"You provided `content` with neither.\n"+
				"If you want a full-body write for a missing/new file or non-source deliverable, use write_file instead:\n"+
				"  use path %q, complete file content, and a concrete description.\n"+
				"If you want a PARTIAL edit, pick one of these two edit_file shapes:\n"+
				"  (a) exact-match: use path %q, old_text copied from the file, complete new_text, and a concrete description.\n"+
				"  (b) line range: use path %q, concrete start_line/end_line values, complete replacement content, and a concrete description.\n"+
				"Do not re-submit the same call shape.",
			display, display, display,
		)
	}

	return fmt.Sprintf(
		"edit_file requires old_text (and new_text) OR start_line/end_line (and content). You provided neither.\n"+
			"Valid shapes:\n"+
			"  (a) exact-match: use path %q, old_text copied from the file, complete new_text, and a concrete description.\n"+
			"  (b) line range: use path %q, concrete start_line/end_line values, complete replacement content, and a concrete description.",
		display, display,
	)
}

func editRecoveryPathLooksSourceLike(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	for _, suffix := range []string{
		".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".kt", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs",
		".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".lua", ".sql",
		".html", ".css", ".scss", ".sass", ".vue", ".svelte", ".templ",
		".json", ".yaml", ".yml", ".toml", ".xml",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

func projectLineRangeEdit(display string, current string, req ToolRequest) editProjection {
	startLine := req.StartLine
	endLine := req.EndLine

	if startLine <= 0 {
		return editProjection{result: toolInvalidArgumentResult(ToolNameEditFile, "Edit request rejected", "start_line must be >= 1 (line numbers are 1-indexed).")}
	}

	if endLine == 0 {
		endLine = startLine
	}

	if endLine < startLine {
		return editProjection{result: toolInvalidArgumentResult(ToolNameEditFile, "Edit request rejected", fmt.Sprintf("end_line (%d) must be >= start_line (%d). Swap them if you meant the other direction, or set end_line=start_line for a single-line edit.", endLine, startLine))}
	}

	spans := lineSpans(current)
	if len(spans) == 0 {
		return editProjection{result: toolInvalidArgumentResult(ToolNameEditFile, "Edit request rejected", "cannot apply a line edit to an empty file")}
	}

	if startLine > len(spans) || endLine > len(spans) {
		return editProjection{result: withToolFailure(ToolResult{
			Tool:    ToolNameEditFile,
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("requested lines %d-%d are outside %s (file has %d lines)", startLine, endLine, display, len(spans)),
		}, failures.OutOfRange, reasonToolInvalidArgument)}
	}

	startOffset := spans[startLine-1].start
	endOffset := spans[endLine-1].end
	replacedRange := current[startOffset:endOffset]
	if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.NewText) == "" &&
		strings.TrimSpace(replacedRange) != "" {

		return editProjection{result: withToolFailure(ToolResult{
			Tool:    ToolNameEditFile,
			Summary: "Edit request rejected",
			Error: fmt.Sprintf(
				"empty or whitespace-only content with line range %d-%d would DELETE non-empty lines from %s. "+
					"If you intended to INSERT new content, use the substring-anchored form instead: "+
					"set old_text to a unique existing snippet (no line numbers needed) and set new_text to "+
					"old_text + your additions. If you genuinely intended deletion, use old_text/new_text "+
					"so the deleted text is explicit.",
				startLine, endLine, display,
			),
		}, failures.InvalidArgument, reasonToolInvalidArgument)}
	}

	if lineRangeReplacementDuplicatesPrecedingContext(current, spans, startLine, req.Content) {
		return editProjection{result: withToolFailure(ToolResult{
			Tool:    ToolNameEditFile,
			Summary: "Edit request rejected",
			Error: fmt.Sprintf(
				"line-range replacement for %s lines %d-%d appears to duplicate context immediately before the selected range. "+
					"Line-range content must contain only the final replacement for the selected lines. "+
					"For inserts, use old_text/new_text with a unique existing snippet and append the new content to new_text.",
				display, startLine, endLine,
			),
		}, failures.InvalidArgument, reasonToolInvalidArgument)}
	}

	replacement := req.Content
	if replacement != "" && strings.HasSuffix(replacedRange, "\n") &&
		!strings.HasSuffix(replacement, "\n") {

		replacement += lineEndingSuffix(replacedRange)
	}

	updated := current[:startOffset] + replacement + current[endOffset:]
	if updated == current {
		return editProjection{result: ToolResult{
			Tool:    ToolNameEditFile,
			Summary: fmt.Sprintf("No-op edit for %s", display),
			Output:  "replacement produced no content change",
		}}
	}

	return editProjection{
		updated:     updated,
		shouldWrite: true,
		result: ToolResult{
			Tool:         ToolNameEditFile,
			Summary:      fmt.Sprintf("Edited %s lines %d-%d", display, startLine, endLine),
			Output:       fmt.Sprintf("replaced lines: %d-%d", startLine, endLine),
			ChangedPaths: []string{display},
		},
	}
}

func lineEndingSuffix(text string) string {
	if strings.HasSuffix(text, "\r\n") {
		return "\r\n"
	}

	return "\n"
}

func lineRangeReplacementDuplicatesPrecedingContext(current string, spans []lineSpan, startLine int, replacement string) bool {
	if startLine <= 1 || strings.TrimSpace(replacement) == "" || len(spans) == 0 {
		return false
	}

	contextStartLine := startLine - 8
	if contextStartLine < 1 {
		contextStartLine = 1
	}

	context := current[spans[contextStartLine-1].start:spans[startLine-2].end]
	contextLines := textutil.NonEmptyTrimmedLines(context)
	replacementLines := textutil.NonEmptyTrimmedLines(replacement)
	maxCompare := min(len(contextLines), len(replacementLines), 6)

	for n := maxCompare; n >= 3; n-- {
		if slices.Equal(contextLines[len(contextLines)-n:], replacementLines[:n]) {
			return true
		}
	}

	return false
}

type lineSpan struct {
	start int
	end   int
}

func lineSpans(text string) []lineSpan {
	if text == "" {
		return nil
	}

	spans := make([]lineSpan, 0, strings.Count(text, "\n")+1)
	start := 0

	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}

		spans = append(spans, lineSpan{start: start, end: i + 1})
		start = i + 1
	}

	if start < len(text) {
		spans = append(spans, lineSpan{start: start, end: len(text)})
	}

	return spans
}

func formatOldTextOccurrenceGuidance(displayPath, content, oldText string, matchCount int) string {
	lines := oldTextOccurrenceLineNumbers(content, oldText, 8)
	if len(lines) == 0 {
		return ""
	}

	showing := ""
	if matchCount > len(lines) {
		showing = fmt.Sprintf(" (showing first %d)", len(lines))
	}

	return fmt.Sprintf(
		"matching start line(s)%s: %s\nUse read_file on %s around the listed line(s) for exact anchors. If all occurrences should change, retry with replace_all=true.",
		showing,
		formatLineNumbers(lines),
		displayPath,
	)
}

func oldTextOccurrenceLineNumbers(content, oldText string, limit int) []int {
	if content == "" || oldText == "" || limit <= 0 {
		return nil
	}

	spans := lineSpans(content)
	if len(spans) == 0 {
		return nil
	}

	lines := make([]int, 0, min(strings.Count(content, oldText), limit))
	offset := 0

	for len(lines) < limit {
		index := strings.Index(content[offset:], oldText)
		if index < 0 {
			break
		}

		absolute := offset + index
		lines = append(lines, lineNumberForOffset(spans, absolute))
		offset = absolute + len(oldText)
	}

	return lines
}

func lineNumberForOffset(spans []lineSpan, offset int) int {
	for i, span := range spans {
		if offset >= span.start && offset < span.end {
			return i + 1
		}
	}

	return len(spans)
}

func formatLineNumbers(lines []int) string {
	if len(lines) == 0 {
		return ""
	}

	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, strconv.Itoa(line))
	}

	return strings.Join(parts, ", ")
}

func containsSearchContentLineTruncationMarker(text string) bool {
	return strings.Contains(text, lineTruncationMarker) || strings.Contains(text, "[line truncated]")
}
