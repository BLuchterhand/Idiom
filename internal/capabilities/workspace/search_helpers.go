package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
)

func (m *Manager) searchContent(parent context.Context, req ToolRequest) ToolResult {
	toolName := strings.TrimSpace(req.Tool)
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: toolName, Summary: "Content search rejected", Error: "pattern is required"}
	}
	var matcher *regexp.Regexp
	if !req.FixedStrings {
		var err error
		matcher, err = regexp.Compile(pattern)
		if err != nil {
			return ToolResult{Tool: toolName, Summary: "Content search failed", Error: err.Error()}
		}
	}

	requestScope := defaultString(req.Path, ".")
	capability, err := m.pathAccess.openReadCapability(parent, req.executionSessionIdentity, requestScope, req.Workdir)
	if err != nil {
		result := ToolResult{Tool: toolName, Summary: "Failed to resolve search scope", Error: err.Error()}
		if os.IsNotExist(err) {
			result.Summary = "Search scope not found"
			result.Error = ""
			result.Output = formatPathSuggestions(m.findPathSuggestions(requestScope, 5))
		}
		return result
	}
	defer capability.Close()
	base := capability.Path()
	if capability.SelectedDirectoryOnly() {
		return selectedSystemSearchDirectoryDeniedResult(toolName, m.pathAccess.DisplayPath(base))
	}

	// Hard display cap. Logs show 100+ hits routinely in monorepos, and
	// the model re-ingests the full result on every subsequent turn.
	const searchContentDisplayCap = 50
	type searchContentMatch struct {
		formatted string
		display   string
		factPath  string
		line      int
		truncated bool
		termScore int
		pathScore int
		snippet   string
	}

	matches := make([]searchContentMatch, 0)
	rankingTerms := searchPatternRankingTerms(pattern)
	includeLogDirs := searchIntentTargetsLogs(req.Path, pattern, req.Glob)
	explicitHiddenRoot := searchFilesExplicitHiddenRoot(req.Path)
	limitReached := false
	matchLimit := m.cfg.MaxDirectoryEntries
	if matchLimit < 1 {
		matchLimit = 1
	}

	matchFile := func(filePath string, data []byte) {
		if bytes.IndexByte(data, 0) >= 0 {
			return
		}
		for index, rawLine := range textutil.SplitWorkspaceFileLines(data) {
			if len(matches) >= matchLimit {
				limitReached = true
				return
			}
			line := strings.TrimRight(rawLine, "\r\n")
			matched := strings.Contains(line, pattern)
			if !req.FixedStrings {
				matched = matcher.MatchString(line)
			}
			if !matched {
				continue
			}

			display := m.pathAccess.DisplayPath(filePath)
			factPath := ""
			if rel, ok := m.pathAccess.RelativeWorkspacePath(filePath); ok && rel != "." {
				factPath = filepath.ToSlash(rel)
			}
			lineNumber := index + 1
			renderedLine := textutil.TruncateBytes(line, searchContentLineCap, lineTruncationMarker)
			matches = append(matches, searchContentMatch{
				formatted: fmt.Sprintf("%s %s:%d:%s", m.rememberPath(filePath), display, lineNumber, renderedLine),
				display:   display,
				factPath:  factPath,
				line:      lineNumber,
				truncated: renderedLine != line,
				termScore: searchContentTermScore(display, line, rankingTerms),
				pathScore: searchContentPathPriority(display),
				snippet:   searchContextSnippetFromData(data, lineNumber, 2),
			})
		}
	}

	visit := func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := parent.Err(); err != nil {
			return err
		}
		entryPath := base
		if relative != "." {
			entryPath = filepath.Join(base, filepath.FromSlash(relative))
		}
		if entry.IsDir() {
			if m.shouldSkipSearchEntry(base, entryPath, true, includeLogDirs, explicitHiddenRoot) {
				return fs.SkipDir
			}
			return nil
		}
		if m.shouldSkipSearchEntry(base, entryPath, false, includeLogDirs, explicitHiddenRoot) || !searchContentGlobMatch(req.Glob, relative, entryPath) {
			return nil
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil || !entryInfo.Mode().IsRegular() {
			return nil
		}
		data, _, readErr := capability.ReadFile(relative)
		if readErr != nil {
			return readErr
		}
		matchFile(entryPath, data)
		if limitReached {
			return fs.SkipAll
		}
		return nil
	}

	if capability.Info().IsDir() {
		rootFS, fsErr := capability.FS()
		if fsErr != nil {
			return ToolResult{Tool: toolName, Summary: "Content search failed", Error: fsErr.Error()}
		}
		if walkErr := fs.WalkDir(rootFS, ".", visit); walkErr != nil {
			return ToolResult{Tool: toolName, Summary: "Content search failed", Error: walkErr.Error()}
		}
	} else if searchContentGlobMatch(req.Glob, ".", base) {
		data, readErr := capability.ReadAll()
		if readErr != nil {
			return ToolResult{Tool: toolName, Summary: "Content search failed", Error: readErr.Error()}
		}
		matchFile(base, data)
	}
	if err := capability.Validate(); err != nil {
		return ToolResult{Tool: toolName, Summary: "Read target changed while searching", Error: err.Error()}
	}

	if len(matches) == 0 {
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

	singlePath := ""
	singleLine := 0
	totalMatches := len(matches)
	if totalMatches == 1 {
		singlePath = matches[0].display
		singleLine = matches[0].line
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
	truncatedDisplayed := 0
	candidatePaths := make([]string, 0, min(len(matches), searchContentDisplayCap))

	for _, match := range matches {
		if len(formatted) >= searchContentDisplayCap {
			capped = true
			break
		}

		formatted = append(formatted, match.formatted)
		if match.factPath != "" {
			candidatePaths = append(candidatePaths, match.factPath)
		}
		if match.truncated {
			truncatedDisplayed++
		}
	}

	if capped {
		remaining := totalMatches - searchContentDisplayCap

		formatted = append(formatted, fmt.Sprintf("... and %d more matching line(s) omitted.", remaining))
	}

	if truncatedDisplayed > 0 {
		formatted = append(formatted, fmt.Sprintf("note: %d displayed matching line(s) were truncated; truncated output is not an exact old_text anchor. Use read_file around the shown path and line before copying exact source; if a write surface is available and the exact range is known, use that surface's line-range edit shape.", truncatedDisplayed))
	}

	summary := fmt.Sprintf("Found %d matching line(s).", totalMatches)
	if capped {
		summary = fmt.Sprintf("Found %d matching line(s) (showing first %d).", totalMatches, searchContentDisplayCap)
	}

	if len(formatted) == 1 && singlePath != "" && singleLine > 0 {
		summary = fmt.Sprintf("Found 1 matching line at %s:%d.", singlePath, singleLine)
		if snippet := matches[0].snippet; snippet != "" {
			formatted = append(formatted, "context:\n"+snippet)
		}
	}

	factPaths := discoveryFactCandidatePaths(candidatePaths)
	highCardinality := totalMatches >= searchContentDisplayCap
	discoveryStrength := toolfacts.DiscoveryStrength("")
	sourceOwnerFound := false
	if len(factPaths) > 0 {
		if highCardinality {
			discoveryStrength = toolfacts.DiscoveryStrengthCandidateOnly
		} else {
			discoveryStrength = toolfacts.DiscoveryStrengthSourceOwner
			sourceOwnerFound = true
		}
	}
	facts := &toolfacts.ToolResultFacts{
		CandidatePaths:        factPaths,
		HasDiscoveryTargets:   len(factPaths) > 0,
		DiscoveryStrength:     discoveryStrength,
		SourceOwnerFound:      sourceOwnerFound,
		SearchHitCount:        totalMatches,
		SearchHighCardinality: highCardinality,
	}

	return ToolResult{
		Tool:    toolName,
		Summary: summary,
		Output:  strings.Join(formatted, "\n"),
		Facts:   facts,
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

func searchContentGlobMatch(glob string, relative string, absolute string) bool {
	glob = filepath.ToSlash(strings.TrimSpace(glob))
	if glob == "" {
		return true
	}
	relative = filepath.ToSlash(filepath.Clean(relative))
	name := filepath.Base(absolute)
	if ok, err := path.Match(glob, name); err == nil && ok {
		return true
	}
	if ok, err := path.Match(glob, relative); err == nil && ok {
		return true
	}
	ok, err := recursivePathGlobMatch(glob, relative)
	return err == nil && ok
}

func searchContextSnippetFromData(content []byte, lineNumber int, radius int) string {
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
		segment = textutil.TruncateBytes(segment, searchContentLineCap, lineTruncationMarker)
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

	capability, err := m.pathAccess.openReadCapability(context.Background(), executionSessionIdentity{}, strings.TrimSpace(path), "")
	if err != nil {
		return 0, false
	}
	defer capability.Close()
	data, err := capability.ReadAll()
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
