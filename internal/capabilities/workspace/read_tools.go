package workspace

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/discovery"
)

type listedEntry struct {
	path  string
	isDir bool
}

const listFilesDisplayCap = 80
const defaultLogReadLineCap = 80

func (m *Manager) listFiles(_ context.Context, req ToolRequest) ToolResult {
	base, err := m.pathAccess.ResolveReadPath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		result := ToolResult{Tool: ToolNameListFiles, Summary: "Failed to resolve path", Error: err.Error()}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	info, err := os.Stat(base)
	if err != nil {
		errMsg := err.Error()
		if os.IsNotExist(err) && strings.TrimSpace(req.Path) != "" {
			errMsg = fmt.Sprintf("directory %q does not exist in this workspace. (os: %s)", req.Path, err.Error())
		}

		result := ToolResult{Tool: ToolNameListFiles, Summary: "Failed to stat path", Error: errMsg}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}
	if req.Recursive && m.pathAccess.SelectedSystemSearchDirectoryAccess(base, info) {
		return selectedSystemSearchDirectoryDeniedResult(ToolNameListFiles, m.pathAccess.DisplayPath(base))
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}

	var entries []listedEntry

	appendEntry := func(path string, isDir bool) bool {
		entries = append(entries, listedEntry{path: path, isDir: isDir})
		return len(entries) < m.cfg.MaxDirectoryEntries
	}

	if !info.IsDir() {
		_ = appendEntry(base, false)
	} else if !req.Recursive {
		children, err := os.ReadDir(base)
		if err != nil {
			return ToolResult{Tool: ToolNameListFiles, Summary: "Failed to read directory", Error: err.Error()}
		}

		slices.SortFunc(children, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })

		_ = appendEntry(base, true)
		for _, child := range children {
			childPath := filepath.Join(base, child.Name())
			if m.shouldSkipListEntry(base, childPath, child.IsDir()) {
				continue
			}

			if !appendEntry(childPath, child.IsDir()) {
				break
			}
		}
	} else {
		rootDepth := strings.Count(filepath.Clean(base), string(filepath.Separator))

		_ = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}

				return nil
			}

			if m.shouldSkipListEntry(base, path, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}

				return nil
			}

			if !appendEntry(path, d.IsDir()) {
				return filepath.SkipAll
			}

			return nil
		})
	}

	m.sortExploreListEntries(entries)

	totalEntries := len(entries)
	displayEntries := entries
	capped := false

	if len(displayEntries) > listFilesDisplayCap {
		displayEntries = displayEntries[:listFilesDisplayCap]
		capped = true
	}

	formatted := make([]string, 0, len(displayEntries)+1)
	for _, entry := range displayEntries {
		formatted = append(formatted, m.formatListedEntry(entry))
	}

	if capped {
		formatted = append(formatted, fmt.Sprintf("... and %d more path(s) omitted.", totalEntries-listFilesDisplayCap))
	}

	summary := fmt.Sprintf("Listed %d path(s).", totalEntries)
	if capped {
		summary = fmt.Sprintf("Listed %d path(s) (showing first %d).", totalEntries, listFilesDisplayCap)
	}

	if m.IsExploreMode() {
		summary = fmt.Sprintf("Listed %d likely path(s), prioritized toward source files.", totalEntries)
		if capped {
			summary = fmt.Sprintf("Listed %d likely path(s), prioritized toward source files (showing first %d).", totalEntries, listFilesDisplayCap)
		}
	}

	return ToolResult{
		Tool:    ToolNameListFiles,
		Summary: summary,
		Output:  strings.Join(formatted, "\n"),
	}
}

func (m *Manager) formatListedEntry(entry listedEntry) string {
	rel := m.pathAccess.DisplayPath(entry.path)
	if entry.isDir {
		if rel != "." {
			rel += string(filepath.Separator)
		}

		return rel
	}

	return m.handleDisplay(entry.path)
}

func (m *Manager) shouldSkipListEntry(base string, path string, isDir bool) bool {
	if filepath.Clean(path) == filepath.Clean(base) {
		return false
	}

	return discovery.ShouldSkipPath(base, path, isDir)
}

func (m *Manager) shouldSkipSearchEntry(base string, path string, isDir bool, includeLogDirs bool, explicitHiddenRoot bool) bool {
	if explicitHiddenRoot {
		return false
	}

	if includeLogDirs && isDir && filepath.Base(path) == "logs" {
		return false
	}

	if includeLogDirs && !isDir {
		rel, err := filepath.Rel(base, path)
		if err == nil && strings.HasPrefix(filepath.ToSlash(filepath.Clean(rel)), "logs/") {
			return discovery.ShouldSkipRelFile(rel)
		}
	}

	return m.shouldSkipListEntry(base, path, isDir)
}

func searchFilesExplicitHiddenRoot(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if clean == "" || clean == "." {
		return false
	}

	for _, segment := range strings.Split(clean, "/") {
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
			return true
		}
	}

	return false
}

func (m *Manager) sortExploreListEntries(entries []listedEntry) {
	if !m.IsExploreMode() {
		return
	}

	sort.SliceStable(entries, func(i, j int) bool {
		left := m.exploreListPriority(entries[i])
		right := m.exploreListPriority(entries[j])

		if left != right {
			return left < right
		}

		return m.pathAccess.DisplayPath(entries[i].path) < m.pathAccess.DisplayPath(entries[j].path)
	})
}

func (m *Manager) exploreListPriority(entry listedEntry) int {
	rel := m.pathAccess.DisplayPath(entry.path)
	if rel == "." {
		return -1
	}

	base := filepath.Base(rel)
	if entry.isDir {
		switch rel {
		case "cmd", "internal", "pkg", "prompts", "scripts", "src", "test", "tests":
			return 0
		}

		if strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "internal"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "pkg"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "src"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "scripts"+string(filepath.Separator)) {

			return 1
		}
	}

	switch base {
	case "README", "README.md", "go.mod", "go.sum", "main.go", "config.json", "package.json", "Cargo.toml", "pyproject.toml", "Makefile":
		return 2
	}

	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".md", ".json", ".yaml", ".yml", ".toml":
		return 3
	}

	if strings.HasPrefix(base, ".") {
		return 6
	}

	return 4
}

func (m *Manager) searchFiles(_ context.Context, req ToolRequest) ToolResult {
	base, err := m.pathAccess.ResolveReadPath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		if resolved, ok := resolveUserGlobalConversationLogSearchPath(defaultString(req.Path, ".")); ok {
			base = resolved
		} else {
			return ToolResult{Tool: ToolNameSearchFiles, Summary: "Failed to resolve path", Error: err.Error()}
		}
	}

	if info, statErr := os.Stat(base); statErr == nil && m.pathAccess.SelectedSystemSearchDirectoryAccess(base, info) {
		return selectedSystemSearchDirectoryDeniedResult(ToolNameSearchFiles, m.pathAccess.DisplayPath(base))
	}

	pattern, interpretedPattern := searchFilesPattern(req)
	if pattern == "" {
		err := "pattern is required"
		// Detect when the model may have put a glob in the path field
		// instead of the pattern field — common confusion with **/*.go.
		if looksLikeGlob(strings.TrimSpace(req.Path)) ||
			looksLikeGlob(strings.TrimSpace(req.Glob)) {

			err = "pattern is required — the pattern field expects a filename substring, not a glob. If you meant to match file extensions, use e.g. pattern: '.go' instead of a glob path."
		}

		return ToolResult{Tool: ToolNameSearchFiles, Summary: "File search rejected", Error: err}
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 4
	}

	query := strings.ToLower(pattern)
	includeLogDirs := searchIntentTargetsLogs(req.Path, pattern, req.Glob)
	explicitHiddenRoot := searchFilesExplicitHiddenRoot(req.Path)
	rootDepth := strings.Count(filepath.Clean(base), string(filepath.Separator))

	var matches []searchFileMatch

	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if m.shouldSkipSearchEntry(base, path, true, includeLogDirs, explicitHiddenRoot) {
				return filepath.SkipDir
			}

			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxDepth {
				return filepath.SkipDir
			}

			return nil
		}

		if m.shouldSkipSearchEntry(base, path, false, includeLogDirs, explicitHiddenRoot) {
			return nil
		}

		rel := strings.ToLower(m.pathAccess.DisplayPath(path))
		name := strings.ToLower(filepath.Base(path))

		if !matchesSearchFilePattern(rel, name, query) {
			return nil
		}

		match := searchFileMatch{
			path:    path,
			display: m.pathAccess.DisplayPath(path),
		}
		if info, err := d.Info(); err == nil {
			match.modTime = info.ModTime()
			match.size = info.Size()
		}

		matches = append(matches, match)
		if !includeLogDirs && len(matches) >= m.cfg.MaxDirectoryEntries {
			return filepath.SkipAll
		}

		return nil
	})

	if len(matches) == 0 {
		// Zero-hit enrichment: the pattern didn't match any path, but
		// the model often invents filenames from concepts (e.g.
		// "task_tool.go" when the code lives in tools.go). Try three
		// shapes of suggestion in order: whole-pattern fuzzy match,
		// token-substring match on base names (so "task_tool" finds
		// tools.go via the "tool" token), and finally the generic
		// sibling-directory fallback inside findPathSuggestions. Cap
		// the final list at 5. Phrasing steers the model out of search
		// mode — these are paths to read, not patterns to re-search.
		tokenHits := m.tokenMatchedPaths(pattern, 5)
		suggestions := append([]string{}, tokenHits...)

		for _, p := range m.findPathSuggestions(pattern, 5) {
			if len(suggestions) >= 5 {
				break
			}

			dup := false

			for _, existing := range suggestions {
				if existing == p {
					dup = true
					break
				}
			}

			if !dup {
				suggestions = append(suggestions, p)
			}
		}

		sortSearchFileSuggestions(suggestions)

		output := ""
		summary := fmt.Sprintf("No file matches for %q", pattern)

		if len(suggestions) > 0 {
			summary = fmt.Sprintf("No file matches for %q. Closest paths in this workspace: %s.", pattern, strings.Join(suggestions, ", "))
			if note := searchFileSuggestionEvidenceShapeSummary(suggestions); note != "" {
				summary += " " + note
			}

			output = "Closest real paths:\n" + strings.Join(suggestions, "\n")
		}

		// When the pattern looks like a symbol (bare identifier) and
		// the workspace has a language index, the model was likely
		// looking for a symbol, not a filename — search_files only
		// matches path text, so nothing was ever going to hit. Point
		// at the right tool. Mentioned alongside paths when both apply.
		if looksLikeBareIdentifier(pattern) {
			if symHint := m.symbolSearchSuggestion(pattern); symHint != "" {
				if output == "" {
					output = symHint
				} else {
					output = output + "\n\n" + symHint
				}

				summary += " The pattern also looks like a symbol name rather than a filename."
			}
		}

		if interpretedPattern != "" {
			summary += " " + interpretedPattern
		}

		return ToolResult{
			Tool:    ToolNameSearchFiles,
			Summary: summary,
			Output:  output,
		}
	}

	if includeLogDirs {
		sort.SliceStable(matches, func(i, j int) bool {
			if !matches[i].modTime.Equal(matches[j].modTime) {
				return matches[i].modTime.After(matches[j].modTime)
			}

			return matches[i].display < matches[j].display
		})
	} else {
		slices.SortFunc(matches, func(a, b searchFileMatch) int {
			if rankDiff := cmp.Compare(searchFileKindRank(classifySearchFilePath(a.display)), searchFileKindRank(classifySearchFilePath(b.display))); rankDiff != 0 {
				return rankDiff
			}

			return cmp.Compare(a.display, b.display)
		})
	}

	displayLimit := len(matches)
	if includeLogDirs && displayLimit > 24 {
		displayLimit = 24
	}

	output := make([]string, 0, displayLimit+1)
	candidatePaths := make([]string, 0, displayLimit)
	for _, match := range matches[:displayLimit] {
		candidatePaths = append(candidatePaths, match.display)
		if includeLogDirs && !match.modTime.IsZero() {
			output = append(output, fmt.Sprintf("%s (modified %s; size %s)", m.handleDisplay(match.path), match.modTime.Format(time.RFC3339), formatBytes(match.size)))
			continue
		}

		output = append(output, m.handleDisplay(match.path))
	}

	summary := fmt.Sprintf("Found %d matching file(s).", len(matches))
	if includeLogDirs {
		if displayLimit < len(matches) {
			output = append(output, fmt.Sprintf("... omitted %d older log-like match(es)", len(matches)-displayLimit))
			summary = fmt.Sprintf("Found %d matching log-like file(s), newest first with modified time and size (showing first %d).", len(matches), displayLimit)
		} else {
			summary = fmt.Sprintf("Found %d matching log-like file(s), newest first with modified time and size.", len(matches))
		}
	} else if note := searchFileEvidenceShapeSummary(matches); note != "" {
		summary += " " + note
	}

	if interpretedPattern != "" {
		summary += " " + interpretedPattern
	}

	var facts *toolfacts.ToolResultFacts
	if factPaths := discoveryFactCandidatePaths(candidatePaths); len(factPaths) > 0 {
		facts = &toolfacts.ToolResultFacts{
			CandidatePaths:      factPaths,
			HasDiscoveryTargets: true,
			DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
			SourceOwnerFound:    true,
		}
	}

	return ToolResult{
		Tool:    ToolNameSearchFiles,
		Summary: summary,
		Output:  strings.Join(output, "\n"),
		Facts:   facts,
	}
}

type searchFileMatch struct {
	path    string
	display string
	modTime time.Time
	size    int64
}

type searchFileKind string

const (
	searchFileKindSource    searchFileKind = "source"
	searchFileKindTest      searchFileKind = "test"
	searchFileKindConfig    searchFileKind = "config"
	searchFileKindDocs      searchFileKind = "docs"
	searchFileKindFixture   searchFileKind = "fixtures"
	searchFileKindGenerated searchFileKind = "generated"
	searchFileKindLog       searchFileKind = "logs"
	searchFileKindOther     searchFileKind = "other"
)

func classifySearchFilePath(displayPath string) searchFileKind {
	clean := filepath.ToSlash(strings.TrimSpace(displayPath))
	lower := strings.ToLower(clean)
	name := strings.ToLower(filepath.Base(lower))
	segments := strings.Split(lower, "/")

	for _, segment := range segments {
		switch segment {
		case ".idiom", "logs", "log":
			return searchFileKindLog
		case "testdata", "fixtures", "fixture":
			return searchFileKindFixture
		case "generated", "generated-logs", "vendor", "node_modules":
			return searchFileKindGenerated
		case "docs", "doc":
			return searchFileKindDocs
		}
	}

	if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".log") {
		return searchFileKindLog
	}

	if strings.HasPrefix(name, "readme") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".mdx") {
		return searchFileKindDocs
	}

	if strings.HasSuffix(name, ".gen.go") || strings.HasSuffix(name, ".pb.go") || strings.Contains(lower, "/generated/") {
		return searchFileKindGenerated
	}

	if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".spec.tsx") || strings.HasSuffix(name, ".test.tsx") {
		return searchFileKindTest
	}

	switch filepath.Ext(name) {
	case ".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".swift", ".kt":
		return searchFileKindSource
	case ".json", ".yaml", ".yml", ".toml", ".mod", ".sum", ".lock":
		return searchFileKindConfig
	default:
		return searchFileKindOther
	}
}

func searchFileKindRank(kind searchFileKind) int {
	switch kind {
	case searchFileKindSource:
		return 0
	case searchFileKindTest:
		return 1
	case searchFileKindConfig:
		return 2
	case searchFileKindDocs:
		return 3
	case searchFileKindFixture:
		return 4
	case searchFileKindGenerated:
		return 5
	case searchFileKindLog:
		return 6
	default:
		return 7
	}
}

func searchFileKindIsCodeLike(kind searchFileKind) bool {
	return kind == searchFileKindSource || kind == searchFileKindTest
}

func searchFileEvidenceShapeSummary(matches []searchFileMatch) string {
	if len(matches) == 0 {
		return ""
	}

	kindCounts := make(map[searchFileKind]int)
	codeLike := 0

	for _, match := range matches {
		kind := classifySearchFilePath(match.display)
		kindCounts[kind]++

		if searchFileKindIsCodeLike(kind) {
			codeLike++
		}
	}

	if codeLike > 0 {
		if codeLike == len(matches) {
			return "All matches are code-like paths."
		}

		return "Code-like paths are ranked first; remaining matches include " + searchFileKindList(kindCounts, false) + "."
	}

	return "No code-like file matches; matches are " + searchFileKindList(kindCounts, true) + " paths."
}

func sortSearchFileSuggestions(suggestions []string) {
	slices.SortStableFunc(suggestions, func(a, b string) int {
		if rankDiff := cmp.Compare(searchFileKindRank(classifySearchFilePath(a)), searchFileKindRank(classifySearchFilePath(b))); rankDiff != 0 {
			return rankDiff
		}

		return cmp.Compare(a, b)
	})
}

func searchFileSuggestionEvidenceShapeSummary(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}

	matches := make([]searchFileMatch, 0, len(suggestions))
	for _, suggestion := range suggestions {
		matches = append(matches, searchFileMatch{display: suggestion})
	}

	return strings.Replace(searchFileEvidenceShapeSummary(matches), "matches are", "suggestions are", 1)
}

func searchFileKindList(kindCounts map[searchFileKind]int, includeCodeLike bool) string {
	var kinds []string

	for _, kind := range []searchFileKind{
		searchFileKindSource,
		searchFileKindTest,
		searchFileKindConfig,
		searchFileKindDocs,
		searchFileKindFixture,
		searchFileKindGenerated,
		searchFileKindLog,
		searchFileKindOther,
	} {
		if kindCounts[kind] == 0 {
			continue
		}

		if !includeCodeLike && searchFileKindIsCodeLike(kind) {
			continue
		}

		kinds = append(kinds, string(kind))
	}

	if len(kinds) == 0 {
		return "other"
	}

	return strings.Join(kinds, ", ")
}

func searchFilesPattern(req ToolRequest) (pattern string, interpreted string) {
	pattern = strings.TrimSpace(req.Pattern)
	if pattern != "" {
		return pattern, ""
	}

	glob := strings.TrimSpace(req.Glob)
	if glob != "" {
		return glob, fmt.Sprintf("Interpreted glob %q as the file search pattern.", glob)
	}

	return "", ""
}

func (m *Manager) readFile(ctx context.Context, req ToolRequest) ToolResult {
	path, description, err := m.resolveReadTarget(req)
	if err != nil {
		if recoveredPath, ok := m.closestRecoverableReadPath(req.Path, req.Workdir); ok {
			recoveredReq := req

			recoveredReq.Path = recoveredPath
			recoveredReq.ID = ""

			result := m.readFile(ctx, recoveredReq)

			if strings.TrimSpace(result.Error) == "" {
				result.Summary = fmt.Sprintf("Recovered missing read_file path %q as %q; %s", strings.TrimSpace(req.Path), recoveredPath, result.Summary)
				return result
			}
		}

		errMsg := err.Error()
		if recoveredErr, ok := m.missingReadParentRecoveryError(req, ""); ok {
			errMsg = recoveredErr
		}

		result := ToolResult{Tool: ToolNameReadFile, Summary: "Failed to resolve file target", Error: errMsg}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	data, err := os.ReadFile(path)
	if err != nil {
		errMsg := readFileRecoveryError(err.Error())
		if os.IsNotExist(err) {
			if recoveredErr, ok := m.missingReadParentRecoveryError(req, err.Error()); ok {
				errMsg = recoveredErr
			}
		}

		result := ToolResult{
			Tool:    ToolNameReadFile,
			Summary: "Failed to read file",
			Error:   errMsg,
		}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	logLike := isLogLikePath(m.pathAccess.DisplayPath(path))
	userRequestedRange := req.StartLine > 0 || req.EndLine > 0
	openEndedLogRange := logLike && userRequestedRange && req.EndLine <= 0

	if !userRequestedRange && logLike {
		if result, ok := m.readLogTail(path, description, defaultLogReadLineCap); ok {
			return result
		}
	}

	lines := textutil.SplitWorkspaceFileLines(data)
	start := req.StartLine
	end := req.EndLine
	requestedStart := start
	requestedEnd := end
	// Reversed ranges (start > end) get auto-swapped rather than
	// silently clamped to one line or rejected with an error. Smaller
	// planners (local Qwen 35B) emit (start=356, end=1) when they
	// want "tail of file" and don't self-correct on a strict error;
	// they just retry the same shape. Swapping returns useful content
	// and the summary suffix tells the model what we did so future
	// calls can use the canonical order.
	rangeWasSwapped := false

	if start > 0 && end > 0 && start > end {
		start, end = end, start
		requestedStart, requestedEnd = start, end
		rangeWasSwapped = true
	}

	if start <= 0 {
		start = 1
	}

	if logLike && start > len(lines) {
		start = len(lines) - defaultLogReadLineCap + 1
		if start < 1 {
			start = 1
		}
	}

	if openEndedLogRange && end <= 0 {
		end = start + defaultLogReadLineCap - 1
		if end > len(lines) {
			end = len(lines)
		}
	} else if end <= 0 || end > len(lines) {
		end = len(lines)
	}

	if len(lines) == 0 {
		displayPath := m.pathAccess.DisplayPath(path)
		m.MarkPathObserved(displayPath)

		return ToolResult{
			Tool:    ToolNameReadFile,
			Summary: fmt.Sprintf("Read %s lines 0-0 (total lines: 0)", description),
			Output:  "",
			Paths:   []string{displayPath},
			Facts: toolfacts.NewSourceBodyFacts(toolfacts.SourceBodyEvidence{
				Path:       displayPath,
				StartLine:  0,
				EndLine:    0,
				TotalLines: 0,
			}),
		}
	}

	start, end, clampedToFileBounds := textutil.ClampRequestedLineRange(len(lines), start, end, requestedStart, requestedEnd)
	if start > end {
		return ToolResult{Tool: ToolNameReadFile, Summary: "Invalid line range", Error: "start_line must be <= end_line"}
	}

	truncated := false
	tailRead := false

	if !userRequestedRange && isLogLikePath(m.pathAccess.DisplayPath(path)) && len(lines) > defaultLogReadLineCap {
		start = len(lines) - defaultLogReadLineCap + 1
		if start < 1 {
			start = 1
		}

		end = len(lines)
		tailRead = true
		truncated = true
	} else if openEndedLogRange {
		truncated = true
	}

	var b strings.Builder

	for i := start - 1; i < end && i < len(lines); i++ {
		line := lines[i]
		if logLike {
			line = textutil.TruncateBytes(strings.TrimRight(line, "\r\n"), defaultLogLineCharCap, lineTruncationMarker)
		}

		fmt.Fprintf(&b, "%d:%s\n", i+1, line)
	}

	displayPath := m.pathAccess.DisplayPath(path)
	m.MarkPathObserved(displayPath)
	m.writeGuard.ClearEditRecoveryAfterRead(path, start, end)

	summary := fmt.Sprintf("Read %s lines %d-%d", description, start, end)
	if tailRead {
		summary += fmt.Sprintf(" (file has %d lines total; defaulted to recent log tail; pass start_line/end_line for a different window)", len(lines))
	} else if openEndedLogRange {
		summary += fmt.Sprintf(" (file has %d lines total; open-ended log range capped to %d lines; pass an explicit end_line for a different bounded window)", len(lines), defaultLogReadLineCap)
	} else if clampedToFileBounds {
		summary += fmt.Sprintf(" (requested range %d-%d was clamped to file bounds; file has %d lines total)", nonZeroOrDefaultInt(requestedStart, start), nonZeroOrDefaultInt(requestedEnd, end), len(lines))
	} else if rangeWasSwapped {
		summary += fmt.Sprintf(" (file has %d lines total; start_line was greater than end_line; auto-swapped to lines %d-%d; pass start_line <= end_line on future calls)", len(lines), start, end)
	} else if truncated {
		summary += fmt.Sprintf(" (file has %d lines total; output was capped; pass start_line/end_line for a different range)", len(lines))
	} else {
		summary += fmt.Sprintf(" (total lines: %d)", len(lines))
	}

	return ToolResult{
		Tool:    ToolNameReadFile,
		Summary: summary,
		Output:  strings.TrimRight(b.String(), "\n"),
		Paths:   []string{displayPath},
		Facts: toolfacts.NewSourceBodyFacts(toolfacts.SourceBodyEvidence{
			Path:       displayPath,
			StartLine:  start,
			EndLine:    end,
			TotalLines: len(lines),
			Truncated:  tailRead || openEndedLogRange || truncated,
		}),
	}
}

func (m *Manager) missingReadParentRecoveryError(req ToolRequest, osDetail string) (string, bool) {
	requestPath := strings.TrimSpace(req.Path)
	if requestPath == "" {
		return "", false
	}

	parent := filepath.Dir(strings.ReplaceAll(requestPath, "\\", "/"))
	if parent == "." || parent == "/" || parent == "" {
		return "", false
	}

	parentAbs, err := m.pathAccess.ResolveReadPath(parent, req.Workdir)
	if err != nil {
		return "", false
	}

	if _, statErr := os.Stat(parentAbs); !os.IsNotExist(statErr) {
		return "", false
	}

	detail := fmt.Sprintf("parent directory %q does not exist in this workspace, so %q cannot exist either.", parent, req.Path)
	if strings.TrimSpace(osDetail) != "" {
		detail += fmt.Sprintf(" (os: %s)", osDetail)
	}

	return readFileRecoveryError(detail), true
}

func (m *Manager) readLogTail(path string, description string, lineCap int) (ToolResult, bool) {
	if lineCap <= 0 {
		lineCap = defaultLogReadLineCap
	}

	lines, totalLines, err := readLastLines(path, lineCap)
	if err != nil || len(lines) == 0 {
		return ToolResult{}, false
	}

	start := totalLines - len(lines) + 1
	if start < 1 {
		start = 1
	}

	var b strings.Builder

	for i, line := range lines {
		line = textutil.TruncateBytes(strings.TrimRight(line, "\r\n"), defaultLogLineCharCap, lineTruncationMarker)
		fmt.Fprintf(&b, "%d:%s\n", start+i, line)
	}

	displayPath := m.pathAccess.DisplayPath(path)
	m.MarkPathObserved(displayPath)
	m.writeGuard.ClearEditRecoveryAfterRead(path, start, start+len(lines)-1)

	return ToolResult{
		Tool:    ToolNameReadFile,
		Summary: fmt.Sprintf("Read %s lines %d-%d (file has %d lines total; defaulted to recent log tail; pass start_line/end_line for a different window)", description, start, start+len(lines)-1, totalLines),
		Output:  strings.TrimRight(b.String(), "\n"),
		Paths:   []string{displayPath},
		Facts: toolfacts.NewSourceBodyFacts(toolfacts.SourceBodyEvidence{
			Path:       displayPath,
			StartLine:  start,
			EndLine:    start + len(lines) - 1,
			TotalLines: totalLines,
			Truncated:  totalLines > len(lines),
		}),
	}, true
}

type fileStatsReport struct {
	Path                    string           `json:"path"`
	Recursive               bool             `json:"recursive,omitempty"`
	MaxDepth                int              `json:"max_depth,omitempty"`
	Glob                    string           `json:"glob,omitempty"`
	StartLine               int              `json:"start_line,omitempty"`
	EndLine                 int              `json:"end_line,omitempty"`
	Files                   int              `json:"files"`
	Directories             int              `json:"directories,omitempty"`
	Bytes                   int64            `json:"bytes"`
	Lines                   int              `json:"lines"`
	LineBytes               int              `json:"line_bytes,omitempty"`
	LineBytesWithNewlines   int              `json:"line_bytes_with_newlines,omitempty"`
	LineCharacters          int              `json:"line_characters,omitempty"`
	LineCharacterWithBreaks int              `json:"line_characters_with_newlines,omitempty"`
	LineCountErrors         int              `json:"line_count_errors,omitempty"`
	Truncated               bool             `json:"truncated,omitempty"`
	Limit                   int              `json:"limit,omitempty"`
	Entries                 []fileStatsEntry `json:"entries,omitempty"`
	EntriesTruncated        bool             `json:"entries_truncated,omitempty"`
}

type fileStatsEntry struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Lines int    `json:"lines,omitempty"`
	Error string `json:"error,omitempty"`
}

const fileStatsDefaultLimit = 1000
const fileStatsMaxLimit = 10000
const fileStatsEntryDisplayCap = 50

func (m *Manager) fileStats(_ context.Context, req ToolRequest) ToolResult {
	requestPath := strings.TrimSpace(req.Path)
	if requestPath == "" {
		requestPath = "."
	}

	target, err := m.pathAccess.ResolveReadPath(requestPath, req.Workdir)
	if err != nil {
		if resolved, ok := resolveUserGlobalConversationLogSearchPath(requestPath); ok {
			target = resolved
		} else {
			return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to resolve path", Error: err.Error()}
		}
	}

	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to stat path", Error: err.Error()}
	}
	if m.pathAccess.SelectedSystemSearchDirectoryAccess(target, info) {
		return selectedSystemSearchDirectoryDeniedResult(ToolNameFileStats, m.pathAccess.DisplayPath(target))
	}

	limit := req.Limit
	if limit <= 0 {
		limit = fileStatsDefaultLimit
	}

	if limit > fileStatsMaxLimit {
		limit = fileStatsMaxLimit
	}

	report := fileStatsReport{
		Path:      m.pathAccess.DisplayPath(target),
		Recursive: req.Recursive,
		MaxDepth:  req.MaxDepth,
		Glob:      strings.TrimSpace(req.Glob),
		StartLine: req.StartLine,
		EndLine:   req.EndLine,
		Limit:     limit,
	}

	if !info.IsDir() {
		entry := m.fileStatsEntry(target, info)

		report.Files = 1
		report.Bytes = entry.Bytes
		report.Lines = entry.Lines

		if entry.Error != "" {
			report.LineCountErrors = 1
		}

		report.Entries = []fileStatsEntry{entry}
		if req.StartLine > 0 || req.EndLine > 0 {
			if err := m.addFileStatsLineRange(target, &report, req.StartLine, req.EndLine); err != nil {
				return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to collect file line stats", Error: err.Error()}
			}
		}

		return fileStatsResult(report)
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		if req.Recursive {
			maxDepth = 8
		} else {
			maxDepth = 1
		}
	}

	report.MaxDepth = maxDepth

	err = filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if filepath.Clean(path) == filepath.Clean(target) {
			return nil
		}

		rel, err := filepath.Rel(target, path)
		if err != nil {
			return nil
		}

		depth := pathDepth(rel)

		if entry.IsDir() {
			if m.shouldSkipListEntry(target, path, true) {
				return filepath.SkipDir
			}

			if depth > maxDepth {
				return filepath.SkipDir
			}

			report.Directories++

			return nil
		}

		if depth > maxDepth || m.shouldSkipListEntry(target, path, false) {
			return nil
		}

		if !fileStatsGlobMatch(report.Glob, rel) {
			return nil
		}

		if report.Files >= limit {
			report.Truncated = true
			return filepath.SkipAll
		}

		info, err := entry.Info()
		if err != nil {
			return nil
		}

		stats := m.fileStatsEntry(path, info)
		report.Files++

		report.Bytes += stats.Bytes
		report.Lines += stats.Lines

		if stats.Error != "" {
			report.LineCountErrors++
		}

		if len(report.Entries) < fileStatsEntryDisplayCap {
			report.Entries = append(report.Entries, stats)
		} else {
			report.EntriesTruncated = true
		}

		return nil
	})
	if err != nil {
		return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to collect file stats", Error: err.Error()}
	}

	return fileStatsResult(report)
}

func (m *Manager) addFileStatsLineRange(path string, report *fileStatsReport, start int, end int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := textutil.SplitWorkspaceFileLines(data)
	if len(lines) == 0 {
		report.StartLine = 0
		report.EndLine = 0
		return nil
	}

	requestedStart := start
	requestedEnd := end
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	start, end, _ = textutil.ClampRequestedLineRange(len(lines), start, end, requestedStart, requestedEnd)

	report.StartLine = start
	report.EndLine = end
	for i := start - 1; i < end && i < len(lines); i++ {
		line := lines[i]
		report.LineBytes += len([]byte(line))
		report.LineCharacters += len([]rune(line))
		report.LineBytesWithNewlines += len([]byte(line)) + 1
		report.LineCharacterWithBreaks += len([]rune(line)) + 1
	}

	return nil
}

func (m *Manager) fileStatsEntry(path string, info os.FileInfo) fileStatsEntry {
	entry := fileStatsEntry{
		Path:  m.pathAccess.DisplayPath(path),
		Bytes: info.Size(),
	}

	lines, err := countFileNewlines(path)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}

	entry.Lines = lines

	return entry
}

func fileStatsResult(report fileStatsReport) ToolResult {
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to encode file stats", Error: err.Error()}
	}

	summary := fmt.Sprintf("Counted %d file(s), %d line(s), %s under %s", report.Files, report.Lines, formatBytes(report.Bytes), report.Path)
	if report.StartLine > 0 && report.EndLine >= report.StartLine {
		summary += fmt.Sprintf("; selected lines %d-%d: %d bytes, %d chars (%d bytes with newline separators)", report.StartLine, report.EndLine, report.LineBytes, report.LineCharacters, report.LineBytesWithNewlines)
	}
	if report.Truncated {
		summary += fmt.Sprintf(" (truncated at %d files)", report.Limit)
	}

	return ToolResult{
		Tool:    ToolNameFileStats,
		Summary: summary,
		Output:  string(output),
	}
}

func countFileNewlines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	buf := make([]byte, 32*1024)
	count := 0

	var (
		total int64
		last  byte
	)

	for {
		n, err := file.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			count += bytes.Count(chunk, []byte{'\n'})
			total += int64(n)

			last = chunk[n-1]
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return 0, err
		}
	}

	if total > 0 && last != '\n' {
		count++
	}

	return count, nil
}

func pathDepth(rel string) int {
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == "." || cleaned == "" {
		return 0
	}

	return strings.Count(cleaned, "/") + 1
}

func fileStatsGlobMatch(glob string, rel string) bool {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return true
	}

	slashRel := filepath.ToSlash(rel)
	if ok, err := path.Match(glob, slashRel); err == nil && ok {
		return true
	}

	if ok, err := path.Match(glob, path.Base(slashRel)); err == nil && ok {
		return true
	}

	if ok, err := filepath.Match(glob, filepath.Base(rel)); err == nil && ok {
		return true
	}

	return false
}

func readLastLines(path string, lineCap int) ([]string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}

	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil, 0, nil
	}

	lines := strings.Split(text, "\n")
	if len(lines) > lineCap {
		lines = lines[len(lines)-lineCap:]
	}

	return lines, countFileLinesFromData(data), nil
}

func countFileLinesFromData(data []byte) int {
	if len(data) == 0 {
		return 0
	}

	count := bytes.Count(data, []byte{'\n'})
	if !bytes.HasSuffix(data, []byte{'\n'}) {
		count++
	}

	return count
}
func isLogLikePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))

	return strings.Contains(lower, "/logs/") ||
		strings.HasPrefix(lower, "logs/") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.Contains(filepath.Base(lower), "log")
}

func formatBytes(size int64) string {
	const unit = 1024

	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	value := float64(size)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}

	return fmt.Sprintf("%.1f PB", value/unit)
}

// nonZeroOrDefaultInt returns value if > 0, otherwise fallback.
func nonZeroOrDefaultInt(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
