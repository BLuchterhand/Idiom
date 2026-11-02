package workspace

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/safepath"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/discovery"
)

type listedEntry struct {
	path  string
	isDir bool
}

const listFilesDisplayCap = 80
const defaultLogReadLineCap = 80

func (m *Manager) listFiles(ctx context.Context, req ToolRequest) ToolResult {
	capability, err := m.pathAccess.openReadCapability(ctx, req.executionSessionIdentity, defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		errText := err.Error()
		if errors.Is(err, fs.ErrNotExist) && strings.TrimSpace(req.Path) != "" {
			errText = fmt.Sprintf("directory %q does not exist in this workspace. (os: %s)", req.Path, err.Error())
		}
		result := ToolResult{Tool: ToolNameListFiles, Summary: "Failed to resolve path", Error: errText}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}
	defer capability.Close()
	base := capability.Path()

	info := capability.Info()
	if req.Recursive && capability.SelectedDirectoryOnly() {
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
		children, err := capability.ReadDir(".")
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
		rootFS, err := capability.FS()
		if err != nil {
			return ToolResult{Tool: ToolNameListFiles, Summary: "Failed to read directory", Error: err.Error()}
		}
		walkErr := fs.WalkDir(rootFS, ".", func(relative string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			entryPath := base
			if relative != "." {
				entryPath = filepath.Join(base, filepath.FromSlash(relative))
			}

			depth := pathDepth(relative)
			if depth > maxDepth {
				if d.IsDir() {
					return fs.SkipDir
				}

				return nil
			}

			if m.shouldSkipListEntry(base, entryPath, d.IsDir()) {
				if d.IsDir() {
					return fs.SkipDir
				}

				return nil
			}

			if !appendEntry(entryPath, d.IsDir()) {
				return fs.SkipAll
			}

			return nil
		})
		if walkErr != nil {
			return ToolResult{Tool: ToolNameListFiles, Summary: "Failed to read directory", Error: walkErr.Error()}
		}
	}
	if err := capability.Validate(); err != nil {
		return ToolResult{Tool: ToolNameListFiles, Summary: "Read target changed while listing", Error: err.Error()}
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

func (m *Manager) searchFiles(ctx context.Context, req ToolRequest) ToolResult {
	requestPath := defaultString(req.Path, ".")
	capability, err := m.pathAccess.openReadCapability(ctx, req.executionSessionIdentity, requestPath, req.Workdir)
	if err != nil {
		if resolved, ok := resolveUserGlobalConversationLogSearchPath(requestPath); ok {
			capability, err = m.pathAccess.openTrustedReadCapability(ctx, resolved)
		} else {
			return ToolResult{Tool: ToolNameSearchFiles, Summary: "Failed to resolve path", Error: err.Error()}
		}
	}
	if err != nil {
		return ToolResult{Tool: ToolNameSearchFiles, Summary: "Failed to resolve path", Error: err.Error()}
	}
	defer capability.Close()
	base := capability.Path()

	if capability.SelectedDirectoryOnly() {
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

	var matches []searchFileMatch

	visit := func(relative string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryPath := base
		if relative != "." {
			entryPath = filepath.Join(base, filepath.FromSlash(relative))
		}

		if d.IsDir() {
			if m.shouldSkipSearchEntry(base, entryPath, true, includeLogDirs, explicitHiddenRoot) {
				return fs.SkipDir
			}

			depth := pathDepth(relative)
			if depth > maxDepth {
				return fs.SkipDir
			}

			return nil
		}

		if m.shouldSkipSearchEntry(base, entryPath, false, includeLogDirs, explicitHiddenRoot) {
			return nil
		}

		rel := strings.ToLower(m.pathAccess.DisplayPath(entryPath))
		name := strings.ToLower(filepath.Base(entryPath))

		if !matchesSearchFilePattern(rel, name, query) {
			return nil
		}

		match := searchFileMatch{
			path:    entryPath,
			display: m.pathAccess.DisplayPath(entryPath),
		}
		if info, err := d.Info(); err == nil {
			match.modTime = info.ModTime()
			match.size = info.Size()
		}

		matches = append(matches, match)
		if !includeLogDirs && len(matches) >= m.cfg.MaxDirectoryEntries {
			return fs.SkipAll
		}

		return nil
	}
	if capability.Info().IsDir() {
		walkErr := safepath.WalkDirUnderRoot(
			capability.authorization.authorityPath,
			capability.authorization.canonical,
			"file search",
			func(walkPath string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return visit("", entry, walkErr)
				}
				relative, err := filepath.Rel(capability.authorization.canonical, walkPath)
				if err != nil {
					return err
				}
				return visit(filepath.ToSlash(relative), entry, nil)
			},
		)
		if walkErr != nil {
			return ToolResult{Tool: ToolNameSearchFiles, Summary: "File search failed", Error: walkErr.Error()}
		}
	} else {
		entry := fs.FileInfoToDirEntry(capability.Info())
		if visit(".", entry, nil) != nil {
			return ToolResult{Tool: ToolNameSearchFiles, Summary: "File search failed", Error: "failed to inspect selected file"}
		}
	}
	if err := capability.Validate(); err != nil {
		return ToolResult{Tool: ToolNameSearchFiles, Summary: "Read target changed while searching", Error: err.Error()}
	}

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
	mutationEpoch := m.MutationEpoch()
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

	capability, err := m.pathAccess.openReadCapability(ctx, req.executionSessionIdentity, path, "")
	if err != nil {
		if trusted, ok := resolveUserGlobalConversationLogReadPath(path); ok && filepath.Clean(trusted) == filepath.Clean(path) {
			capability, err = m.pathAccess.openTrustedReadCapability(ctx, path)
		}
	}
	if err != nil {
		errText := readFileRecoveryError(err.Error())
		if errors.Is(err, fs.ErrNotExist) {
			if recoveredErr, ok := m.missingReadParentRecoveryError(req, err.Error()); ok {
				errText = recoveredErr
			}
		}
		result := ToolResult{Tool: ToolNameReadFile, Summary: "Failed to open file", Error: errText}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}
		return result
	}
	defer capability.Close()
	path = capability.Path()
	userRequestedRange := req.StartLine > 0 || req.EndLine > 0
	artifactKind := classifyReadArtifact(m.pathAccess.DisplayPath(path))
	logLike := artifactKind == readArtifactLog
	if userRequestedRange {
		return m.readFileLineWindow(ctx, req, capability, path, description, logLike, mutationEpoch)
	}
	readLimit := m.cfg.MaxFileBytes
	if readLimit <= 0 {
		readLimit = workspaceSnapshotDefaultBodyByteLimit
	}
	data, err := capability.ReadAllLimit(ctx, readLimit)
	if err != nil {
		errMsg := readFileRecoveryError(err.Error())
		if errors.Is(err, errWorkspaceReadLimitExceeded) {
			errMsg = fmt.Sprintf("file exceeds configured read byte limit %d", readLimit)
		}
		if errors.Is(err, fs.ErrNotExist) {
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

	openEndedLogRange := logLike && userRequestedRange && req.EndLine <= 0

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
		result := ToolResult{
			Tool:    ToolNameReadFile,
			Summary: fmt.Sprintf("Read %s lines 0-0 (total lines: 0)", description),
			Output:  "",
			Paths:   []string{displayPath},
		}
		if logLike {
			if bindErr := m.attachDiagnosticLogReadFacts(&result, capability, 0, 0, 0, false); bindErr != nil {
				return ToolResult{Tool: ToolNameReadFile, Summary: "Failed to bind diagnostic-log read evidence", Error: bindErr.Error()}
			}
			return result
		}
		window, bindErr := m.sourceWindowFromReadCapability(capability, data, 0, 0, false, mutationEpoch)
		if bindErr != nil && m.pathAccess.PathWithinWorkspace(path) {
			return ToolResult{Tool: ToolNameReadFile, Summary: "Failed to bind source window", Error: bindErr.Error()}
		}
		if bindErr == nil && !logLike {
			attachWorkspaceSourceWindows(&result, window)
		}
		return result
	}

	start, end, clampedToFileBounds := textutil.ClampRequestedLineRange(len(lines), start, end, requestedStart, requestedEnd)
	if start > end {
		return ToolResult{Tool: ToolNameReadFile, Summary: "Invalid line range", Error: "start_line must be <= end_line"}
	}

	truncated := false
	tailRead := false

	if !userRequestedRange && artifactKind == readArtifactLog && len(lines) > defaultLogReadLineCap {
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
			completeLine := strings.TrimRight(line, "\r\n")
			line = textutil.TruncateBytes(completeLine, defaultLogLineCharCap, lineTruncationMarker)
			truncated = truncated || line != completeLine
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

	result := ToolResult{
		Tool:    ToolNameReadFile,
		Summary: summary,
		Output:  strings.TrimRight(b.String(), "\n"),
		Paths:   []string{displayPath},
	}
	if logLike {
		if bindErr := m.attachDiagnosticLogReadFacts(&result, capability, start, end, len(lines), tailRead || openEndedLogRange || truncated); bindErr != nil {
			return ToolResult{Tool: ToolNameReadFile, Summary: "Failed to bind diagnostic-log read evidence", Error: bindErr.Error()}
		}
		return result
	}
	window, bindErr := m.sourceWindowFromReadCapability(capability, data, start, end, tailRead || openEndedLogRange || truncated, mutationEpoch)
	if bindErr != nil && m.pathAccess.PathWithinWorkspace(path) {
		return ToolResult{Tool: ToolNameReadFile, Summary: "Failed to bind source window", Error: bindErr.Error()}
	}
	if bindErr == nil && !logLike {
		attachWorkspaceSourceWindows(&result, window)
	}
	return result
}

func (m *Manager) readFileLineWindow(
	ctx context.Context,
	req ToolRequest,
	capability *workspaceReadCapability,
	path string,
	description string,
	logLike bool,
	mutationEpoch uint64,
) ToolResult {
	start := req.StartLine
	end := req.EndLine
	requestedStart := start
	requestedEnd := end
	rangeWasSwapped := false
	if start > 0 && end > 0 && start > end {
		start, end = end, start
		requestedStart, requestedEnd = start, end
		rangeWasSwapped = true
	}
	if start <= 0 {
		start = 1
	}
	openEndedLogRange := logLike && req.EndLine <= 0
	windowLimit := m.cfg.MaxFileBytes
	if windowLimit <= 0 {
		windowLimit = workspaceSnapshotDefaultBodyByteLimit
	}
	maxWindowLines := 0
	if openEndedLogRange {
		maxWindowLines = defaultLogReadLineCap
	}
	read, err := capability.ReadLineWindow(ctx, start, end, windowLimit, maxWindowLines)
	if err != nil {
		errMsg := readFileRecoveryError(err.Error())
		if errors.Is(err, errWorkspaceReadLimitExceeded) {
			errMsg = fmt.Sprintf("requested line window exceeds configured read byte limit %d", windowLimit)
		}
		return ToolResult{Tool: ToolNameReadFile, Summary: "Failed to read file", Error: errMsg}
	}

	var output strings.Builder
	outputTruncated := false
	if read.totalLines > 0 {
		for index, line := range strings.Split(string(read.body), "\n") {
			if logLike {
				completeLine := strings.TrimRight(line, "\r")
				line = textutil.TruncateBytes(completeLine, defaultLogLineCharCap, lineTruncationMarker)
				outputTruncated = outputTruncated || line != completeLine
			}
			fmt.Fprintf(&output, "%d:%s\n", read.startLine+index, line)
		}
	}
	formattedOutput := strings.TrimRight(output.String(), "\n")
	if m.cfg.MaxOutputBytes > 0 && len(formattedOutput) > m.cfg.MaxOutputBytes {
		return ToolResult{
			Tool: ToolNameReadFile, Summary: "Failed to format file window",
			Error: fmt.Sprintf("formatted line window exceeds configured output byte limit %d", m.cfg.MaxOutputBytes),
		}
	}
	displayPath := m.pathAccess.DisplayPath(path)
	m.MarkPathObserved(displayPath)
	if read.totalLines > 0 {
		m.writeGuard.ClearEditRecoveryAfterRead(path, read.startLine, read.endLine)
	}
	summary := fmt.Sprintf("Read %s lines %d-%d", description, read.startLine, read.endLine)
	if read.totalLines == 0 {
		summary += " (total lines: 0)"
	} else if openEndedLogRange {
		summary += fmt.Sprintf(" (file has %d lines total; open-ended log range capped to %d lines; pass an explicit end_line for a different bounded window)", read.totalLines, defaultLogReadLineCap)
	} else if read.clamped {
		summary += fmt.Sprintf(" (requested range %d-%d was clamped to file bounds; file has %d lines total)", nonZeroOrDefaultInt(requestedStart, read.startLine), nonZeroOrDefaultInt(requestedEnd, read.endLine), read.totalLines)
	} else if rangeWasSwapped {
		summary += fmt.Sprintf(" (file has %d lines total; start_line was greater than end_line; auto-swapped to lines %d-%d; pass start_line <= end_line on future calls)", read.totalLines, read.startLine, read.endLine)
	} else {
		summary += fmt.Sprintf(" (total lines: %d)", read.totalLines)
	}
	result := ToolResult{
		Tool: ToolNameReadFile, Summary: summary,
		Output: formattedOutput, Paths: []string{displayPath},
	}
	if logLike {
		truncated := outputTruncated || openEndedLogRange && read.endLine < read.totalLines
		if bindErr := m.attachDiagnosticLogReadFacts(&result, capability, read.startLine, read.endLine, read.totalLines, truncated); bindErr != nil {
			return ToolResult{Tool: ToolNameReadFile, Summary: "Failed to bind diagnostic-log read evidence", Error: bindErr.Error()}
		}
		return result
	}
	window, bindErr := m.sourceWindowFromLineRead(capability, read, false, mutationEpoch)
	if bindErr != nil && m.pathAccess.PathWithinWorkspace(path) {
		return ToolResult{Tool: ToolNameReadFile, Summary: "Failed to bind source window", Error: bindErr.Error()}
	}
	if bindErr == nil {
		attachWorkspaceSourceWindows(&result, window)
	}
	return result
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

	parentAbs, err := m.pathAccess.ResolveReadPath(req.executionSessionIdentity, parent, req.Workdir)
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

func (m *Manager) fileStats(ctx context.Context, req ToolRequest) ToolResult {
	requestPath := strings.TrimSpace(req.Path)
	if requestPath == "" {
		requestPath = "."
	}

	capability, err := m.pathAccess.openReadCapability(ctx, req.executionSessionIdentity, requestPath, req.Workdir)
	if err != nil {
		if resolved, ok := resolveUserGlobalConversationLogSearchPath(requestPath); ok {
			capability, err = m.pathAccess.openTrustedReadCapability(ctx, resolved)
		} else {
			return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to resolve path", Error: err.Error()}
		}
	}
	if err != nil {
		return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to resolve path", Error: err.Error()}
	}
	defer capability.Close()
	target := capability.Path()
	info := capability.Info()
	if capability.SelectedDirectoryOnly() {
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
		data, readErr := capability.ReadAll()
		if readErr != nil {
			return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to collect file stats", Error: readErr.Error()}
		}
		entry := m.fileStatsEntryFromData(target, info, data)

		report.Files = 1
		report.Bytes = entry.Bytes
		report.Lines = entry.Lines

		if entry.Error != "" {
			report.LineCountErrors = 1
		}

		report.Entries = []fileStatsEntry{entry}
		if req.StartLine > 0 || req.EndLine > 0 {
			m.addFileStatsLineRange(data, &report, req.StartLine, req.EndLine)
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

	err = safepath.WalkDirUnderRoot(
		capability.authorization.authorityPath,
		capability.authorization.canonical,
		"file stats",
		func(walkPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			relative, err := filepath.Rel(capability.authorization.canonical, walkPath)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if relative == "." {
				return nil
			}

			entryPath := filepath.Join(target, filepath.FromSlash(relative))

			depth := pathDepth(relative)
			if entry.IsDir() {
				if m.shouldSkipListEntry(target, entryPath, true) {
					return fs.SkipDir

				}
				if depth > maxDepth {
					return fs.SkipDir
				}

				report.Directories++
				return nil
			}

			if depth > maxDepth || m.shouldSkipListEntry(target, entryPath, false) {
				return nil
			}
			if !fileStatsGlobMatch(report.Glob, relative) {
				return nil
			}
			if report.Files >= limit {
				report.Truncated = true
				return fs.SkipAll
			}

			data, openedInfo, readErr := capability.ReadFile(relative)
			stats := fileStatsEntry{Path: m.pathAccess.DisplayPath(entryPath)}
			if readErr != nil {
				stats.Error = readErr.Error()
				if entryInfo, infoErr := entry.Info(); infoErr == nil {
					stats.Bytes = entryInfo.Size()
				}
			} else {
				stats = m.fileStatsEntryFromData(entryPath, openedInfo, data)
			}
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
		},
	)
	if err != nil {
		return ToolResult{Tool: ToolNameFileStats, Summary: "Failed to collect file stats", Error: err.Error()}
	}
	if err := capability.Validate(); err != nil {
		return ToolResult{Tool: ToolNameFileStats, Summary: "Read target changed while collecting stats", Error: err.Error()}
	}

	return fileStatsResult(report)
}

func (m *Manager) addFileStatsLineRange(data []byte, report *fileStatsReport, start int, end int) {
	lines := textutil.SplitWorkspaceFileLines(data)
	if len(lines) == 0 {
		report.StartLine = 0
		report.EndLine = 0
		return
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
}

func (m *Manager) fileStatsEntryFromData(path string, info os.FileInfo, data []byte) fileStatsEntry {
	entry := fileStatsEntry{
		Path:  m.pathAccess.DisplayPath(path),
		Bytes: info.Size(),
	}
	entry.Lines = countNewlines(data)

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

func countNewlines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	count := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		count++
	}
	return count
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

type readArtifactKind uint8

const (
	readArtifactSource readArtifactKind = iota
	readArtifactLog
)

func classifyReadArtifact(filePath string) readArtifactKind {
	normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(filePath)))
	extension := path.Ext(normalized)
	if extension == ".log" || extension == ".jsonl" {
		return readArtifactLog
	}

	for _, component := range strings.Split(normalized, "/") {
		if component == "logs" {
			return readArtifactLog
		}
	}

	return readArtifactSource
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
