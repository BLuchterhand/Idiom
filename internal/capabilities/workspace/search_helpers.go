package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"idiom/internal/platform/util"
	"idiom/internal/repository/discovery"
)

func (m *Manager) searchContent(parent context.Context, req ToolRequest, toolName string) ToolResult {
	workdir, err := m.resolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: toolName, Summary: "Failed to resolve workdir", Error: err.Error()}
	}

	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: toolName, Summary: "Content search rejected", Error: "pattern is required"}
	}

	if scope := strings.TrimSpace(req.Path); scope != "" && scope != "." {
		resolvedScope, err := m.resolvePath(scope, req.Workdir)
		if err != nil {
			return ToolResult{Tool: toolName, Summary: "Search scope not found", Output: formatPathSuggestions(m.findPathSuggestions(scope, 5))}
		}

		if _, err := os.Stat(resolvedScope); err != nil {
			result := ToolResult{Tool: toolName, Summary: "Search scope not found"}
			if !os.IsNotExist(err) {
				result.Summary = "Search scope unavailable"
				result.Error = err.Error()

				return result
			}

			result.Output = formatPathSuggestions(m.findPathSuggestions(scope, 5))

			return result
		}
	}

	backend, err := resolveContentSearchBackend()
	if err != nil {
		return ToolResult{
			Tool:    toolName,
			Summary: "Content search unavailable",
			Error:   err.Error(),
		}
	}

	args := backend.args(req, m.defaultSearchExcludeGlobs(req.Path, req.Pattern, req.Glob))

	stdout, stderr, err := m.runCommandDetailed(parent, workdir, backend.bin, args...)
	if err != nil && !isNoMatchExit(err) {
		output := strings.TrimSpace(strings.TrimSpace(stdout) + "\n" + strings.TrimSpace(stderr))
		errMsg := err.Error()

		if trimmed := strings.TrimSpace(stderr); trimmed != "" {
			errMsg = fmt.Sprintf("%s: %s", errMsg, trimmed)
		}

		return ToolResult{
			Tool:    toolName,
			Summary: "Content search failed",
			Output:  strings.TrimSpace(output),
			Error:   errMsg,
		}
	}

	if isNoMatchExit(err) || strings.TrimSpace(stdout) == "" {
		// Zero-hit enrichment. Common confused-mode: model pastes a
		// symbol name as a search_content pattern but the actual
		// definition lives in a file whose *name* contains that term
		// (e.g. searching content for "prompt_routing" in a repo whose
		// file is literally internal/surfaces/tui/app/prompt_routing.go). Offer
		// path suggestions that match the pattern as a name so the
		// model switches to a targeted read_file. Intentionally
		// conservative: we only surface name-match suggestions for
		// identifier-shaped patterns (letters/digits/_), avoiding
		// noise on full regexes where the pattern is not a name.
		summary := fmt.Sprintf("No content matches for %q", pattern)
		output := ""

		if looksLikeBareIdentifier(pattern) {
			suggestions := m.findPathSuggestions(pattern, 5)
			if len(suggestions) > 0 {
				summary = fmt.Sprintf("No content matches for %q, but these paths contain it as part of their name — the symbol may be defined there: %s.", pattern, strings.Join(suggestions, ", "))
				output = "Paths whose name contains the pattern:\n" + strings.Join(suggestions, "\n")
			}
		}

		return ToolResult{
			Tool:    toolName,
			Summary: summary,
			Output:  output,
		}
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// Hard display cap. Logs show 100+ hits routinely in monorepos, and
	// the model re-ingests the full result on every subsequent turn.
	// Capping the rendered view at 50 lets the model scan the top hits,
	// see the total, and narrow on demand.
	const searchContentDisplayCap = 50

	type searchContentMatch struct {
		formatted string
		display   string
		line      int
		termScore int
		pathScore int
	}

	matches := make([]searchContentMatch, 0, len(lines))
	singlePath := ""
	singleLine := 0
	totalMatches := 0
	rankingTerms := searchPatternRankingTerms(pattern)

	for _, line := range lines {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}

		totalMatches++

		absPath := filepath.Clean(filepath.Join(workdir, parts[0]))
		handle := m.rememberPath(absPath)
		display := m.displayPath(absPath)
		lineNumber, _ := strconv.Atoi(parts[1])

		matches = append(matches, searchContentMatch{
			formatted: fmt.Sprintf("%s %s:%s:%s", handle, display, parts[1], util.TruncateBytes(strings.TrimRight(parts[2], "\r\n"), searchContentLineCap, "...[line truncated]")),
			display:   display,
			line:      lineNumber,
			termScore: searchContentTermScore(display, parts[2], rankingTerms),
			pathScore: searchContentPathPriority(display),
		})

		if len(lines) == 1 {
			if lineNumber > 0 {
				singlePath = display
				singleLine = lineNumber
			}
		}

		if totalMatches >= m.cfg.MaxDirectoryEntries {
			break
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].termScore != matches[j].termScore {
			return matches[i].termScore > matches[j].termScore
		}

		if matches[i].pathScore != matches[j].pathScore {
			return matches[i].pathScore < matches[j].pathScore
		}

		if matches[i].display != matches[j].display {
			return matches[i].display < matches[j].display
		}

		return matches[i].line < matches[j].line
	})

	formatted := make([]string, 0, min(len(matches), searchContentDisplayCap)+1)
	capped := false

	for _, match := range matches {
		if len(formatted) >= searchContentDisplayCap {
			capped = true
			break
		}

		formatted = append(formatted, match.formatted)
	}

	if capped {
		remaining := totalMatches - searchContentDisplayCap

		formatted = append(formatted, fmt.Sprintf("... and %d more matching line(s) omitted.", remaining))
	}

	summary := fmt.Sprintf("Found %d matching line(s).", totalMatches)
	if capped {
		summary = fmt.Sprintf("Found %d matching line(s) (showing first %d).", totalMatches, searchContentDisplayCap)
	}

	if len(formatted) == 1 && singlePath != "" && singleLine > 0 {
		summary = fmt.Sprintf("Found 1 matching line at %s:%d.", singlePath, singleLine)
		if snippet := m.searchContextSnippet(filepath.Join(m.cfg.Root, singlePath), singleLine, 2); snippet != "" {
			formatted = append(formatted, "context:\n"+snippet)
		}
	}

	return ToolResult{
		Tool:    toolName,
		Summary: summary,
		Output:  strings.Join(formatted, "\n"),
	}
}

func searchPatternRankingTerms(pattern string) []*regexp.Regexp {
	parts := strings.Split(pattern, "|")
	out := make([]*regexp.Regexp, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		re, err := regexp.Compile("(?i)" + part)
		if err == nil {
			out = append(out, re)
		}
	}

	return out
}

func searchContentTermScore(path string, line string, terms []*regexp.Regexp) int {
	score := 0
	target := path + "\n" + line

	for _, term := range terms {
		if term != nil && term.MatchString(target) {
			score++
		}
	}

	return score
}

func searchContentPathPriority(path string) int {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(lower, "_test.go"):
		return 3
	case strings.Contains(lower, "/testdata/"):
		return 4
	case strings.HasSuffix(lower, ".md") || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/"):
		return 5
	case strings.HasPrefix(lower, "internal/runtime/agent/"), strings.HasPrefix(lower, "internal/capabilities/workspace/"), strings.HasPrefix(lower, "cmd/"):
		return 0
	case strings.HasPrefix(lower, "internal/"), strings.HasPrefix(lower, "pkg/"), strings.HasPrefix(lower, "src/"):
		return 1
	default:
		return 2
	}
}

func (m *Manager) defaultSearchExcludeGlobs(path string, pattern string, glob string) []string {
	if searchPathTargetsSkippedDir(path) {
		return nil
	}

	excludes := discovery.RipgrepExcludeGlobs()
	if searchIntentTargetsLogs(path, pattern, glob) {
		filtered := excludes[:0]
		for _, exclude := range excludes {
			if exclude == "!**/logs/**" {
				continue
			}

			filtered = append(filtered, exclude)
		}

		excludes = filtered
	}

	return excludes
}

func searchIntentTargetsLogs(path string, pattern string, glob string) bool {
	combined := strings.ToLower(strings.TrimSpace(path) + " " + strings.TrimSpace(pattern) + " " + strings.TrimSpace(glob))
	if strings.Contains(combined, "logs") || strings.Contains(combined, "log/") || strings.Contains(combined, ".log") || strings.Contains(combined, "jsonl") {
		return true
	}

	return false
}

func matchesSearchFilePattern(rel string, name string, query string) bool {
	if strings.Contains(rel, query) || strings.Contains(name, query) {
		return true
	}

	if strings.ContainsAny(query, "*?[") {
		if ok, err := recursivePathGlobMatch(query, rel); err == nil && ok {
			return true
		}

		if ok, err := path.Match(query, name); err == nil && ok {
			return true
		}

		if ok, err := path.Match(query, rel); err == nil && ok {
			return true
		}

		trimmed := strings.Trim(query, "*?")
		if trimmed != "" && (strings.Contains(rel, trimmed) || strings.Contains(name, trimmed)) {
			return true
		}
	}

	return false
}

func recursivePathGlobMatch(pattern string, rel string) (bool, error) {
	pattern = path.Clean(filepath.ToSlash(strings.TrimSpace(pattern)))
	rel = path.Clean(filepath.ToSlash(strings.TrimSpace(rel)))

	if pattern == "" || pattern == "." || rel == "" || rel == "." || !strings.Contains(pattern, "**") {
		return false, nil
	}

	var b strings.Builder

	b.WriteString("^")

	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '*' {
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					b.WriteString("(?:.*/)?")

					i++
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}

			continue
		}

		if ch == '?' {
			b.WriteString("[^/]")

			continue
		}

		b.WriteString(regexp.QuoteMeta(string(ch)))
	}

	b.WriteString("$")

	return regexp.MatchString(b.String(), rel)
}

func searchPathTargetsSkippedDir(path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if cleaned == "" || cleaned == "." {
		return false
	}

	first := strings.Trim(strings.Split(cleaned, "/")[0], "/")
	if first == "" || first == "." {
		return false
	}

	return discovery.RelTargetsNoisyDir(cleaned) || discovery.ShouldSkipRelFile(cleaned)
}

func (m *Manager) searchContextSnippet(path string, lineNumber int, radius int) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	spans := lineSpans(string(content))
	if len(spans) == 0 || lineNumber <= 0 || lineNumber > len(spans) {
		return ""
	}

	startLine := lineNumber - radius
	if startLine < 1 {
		startLine = 1
	}

	endLine := lineNumber + radius
	if endLine > len(spans) {
		endLine = len(spans)
	}

	var b strings.Builder

	for i := startLine; i <= endLine; i++ {
		segment := string(content[spans[i-1].start:spans[i-1].end])

		segment = strings.TrimRight(segment, "\n")
		segment = util.TruncateBytes(segment, searchContentLineCap, "...[line truncated]")
		b.WriteString(fmt.Sprintf("%d:%s\n", i, segment))
	}

	return strings.TrimSpace(b.String())
}

// FileLineCount returns the number of lines in the file at the given
// path and true on success. Returns (0, false) if the path can't be
// resolved, stat'd, or read. Cheap enough to call from redirect
// decisions — used to skip the outline detour for tiny files where
// reading the whole range costs less than outline+read_symbol.
func (m *Manager) FileLineCount(path string) (int, bool) {
	if m == nil {
		return 0, false
	}

	resolved, err := m.resolvePath(strings.TrimSpace(path), "")
	if err != nil {
		return 0, false
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return 0, false
	}

	// Count newlines; add 1 unless the file is empty or ends with a
	// newline (the trailing newline doesn't introduce another line).
	if len(data) == 0 {
		return 0, true
	}

	count := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		count++
	}

	return count, true
}

func (m *Manager) readFileLines(path string, start, end int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	lines := splitWorkspaceFileLines(data)

	if start <= 0 {
		start = 1
	}

	if end <= 0 || end > len(lines) {
		end = len(lines)
	}

	start, end, _ = clampRequestedLineRange(len(lines), start, end, start, end)
	if start > end {
		return "", fmt.Errorf("start_line must be <= end_line")
	}

	var b strings.Builder

	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

func splitWorkspaceFileLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}

	return lines
}

func clampRequestedLineRange(totalLines int, start int, end int, requestedStart int, requestedEnd int) (int, int, bool) {
	if totalLines <= 0 {
		return start, end, false
	}

	clamped := false

	if end > totalLines {
		end = totalLines
		clamped = true
	}

	if start > totalLines {
		span := 0
		if requestedStart > 0 && requestedEnd >= requestedStart {
			span = requestedEnd - requestedStart
		} else if end >= start {
			span = end - start
		}

		start = totalLines - span
		if start < 1 {
			start = 1
		}

		clamped = true
	}

	if start > end {
		start = end
		clamped = true
	}

	return start, end, clamped
}

func displayRequestedLine(requested int, fallback int) int {
	if requested > 0 {
		return requested
	}

	return fallback
}
