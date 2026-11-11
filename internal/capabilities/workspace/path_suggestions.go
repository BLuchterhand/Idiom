package workspace

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/platform/safepath"
	"idiom/internal/repository/discovery"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

const readFileRecoveryCapabilities = "Available recovery capabilities: file-name search, structured symbol/code navigation, or exact text search followed by a returned handle"

func readFileRecoveryError(base string, facts ...string) string {
	parts := []string{strings.TrimSpace(base)}
	for _, fact := range facts {
		fact = strings.TrimSpace(fact)
		if fact != "" {
			parts = append(parts, fact)
		}
	}

	parts = append(parts, readFileRecoveryCapabilities)

	return strings.Join(parts, " ")
}

func (m *Manager) closestRecoverableReadPath(query string, workdir string) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" || !m.Enabled() {
		return "", false
	}

	queryKey := normalizedReadPathBasename(query)
	if queryKey == "" {
		return "", false
	}

	queryParent := normalizedReadPathParent(query)
	suggestions := m.findPathSuggestions(query, 5)

	var match string

	for _, suggestion := range suggestions {
		candidate := suggestedWorkspacePath(suggestion)
		if candidate == "" || normalizeWorkspaceSlash(candidate) == normalizeWorkspaceSlash(query) {
			continue
		}

		if normalizedReadPathParent(candidate) != queryParent {
			continue
		}

		if normalizedReadPathBasename(candidate) != queryKey {
			continue
		}

		resolved, err := m.pathAccess.ResolvePath(candidate, workdir)
		if err != nil {
			continue
		}

		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			continue
		}

		display := m.pathAccess.DisplayPath(resolved)
		if match != "" && match != display {
			return "", false
		}

		match = display
	}

	if match == "" {
		return "", false
	}

	return match, true
}

func suggestedWorkspacePath(suggestion string) string {
	fields := strings.Fields(strings.TrimSpace(suggestion))
	if len(fields) >= 2 && isFileHandleToken(fields[0]) {
		return strings.Join(fields[1:], " ")
	}

	return strings.TrimSpace(suggestion)
}

func isFileHandleToken(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != 'f' {
		return false
	}

	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func normalizedReadPathBasename(path string) string {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(strings.TrimSpace(path))))

	var b strings.Builder

	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}

	return b.String()
}

func normalizedReadPathParent(path string) string {
	path = normalizeWorkspaceSlash(path)

	parent := filepath.ToSlash(filepath.Dir(path))

	if parent == "." {
		return ""
	}

	return strings.ToLower(parent)
}

func normalizeWorkspaceSlash(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	return strings.TrimPrefix(path, "./")
}

// notFoundRecoveryHint inspects the parent directory of a missing
// path and returns a one-line hint pointing at the closest
// existing entries by name (case-insensitive substring match).
// Generic — purely string-similarity over directory entries; no
// project-specific assumptions about path layout.
//
// Returns "" when the parent doesn't exist or has no useful
// suggestions, so callers can concatenate the hint without worrying
// about empty messages dangling.
func (m *Manager) notFoundRecoveryHint(path string) string {
	if path == "" {
		return ""
	}

	parent := filepath.Dir(path)
	if parent == "" || parent == "." || parent == path {
		return ""
	}

	entries, err := safepath.ReadDirUnderRoot(m.cfg.Root, parent, "read recovery directory", safepath.Options{})
	if err != nil {
		grandparent := filepath.Dir(parent)
		if grandparent != "" && grandparent != parent {
			return fmt.Sprintf("Parent directory %q does not exist either; closest existing parent: %q.", m.pathAccess.DisplayPath(parent), m.pathAccess.DisplayPath(grandparent))
		}

		return ""
	}

	target := strings.ToLower(filepath.Base(path))
	if target == "" {
		return ""
	}

	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		candidates = append(candidates, e.Name())
	}

	suggestions := closestToolNames(target, candidates, 3)
	if len(suggestions) == 0 {
		return fmt.Sprintf("Parent directory %q exists but contains no matching name.", m.pathAccess.DisplayPath(parent))
	}

	quoted := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		quoted = append(quoted, fmt.Sprintf("%q", filepath.Join(m.pathAccess.DisplayPath(parent), s)))
	}

	return fmt.Sprintf("Closest existing entries in %q: %s.", m.pathAccess.DisplayPath(parent), strings.Join(quoted, ", "))
}

func (m *Manager) isRelevantExplorationPath(path string) bool {
	rel := m.pathAccess.DisplayPath(filepath.Clean(path))
	if rel == "" || rel == "." {
		return false
	}

	if discovery.RelTargetsNoisyDir(rel) || discovery.ShouldSkipRelFile(rel) {
		return false
	}

	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".md", ".json", ".yaml", ".yml", ".toml":
		return true
	}

	switch filepath.Base(rel) {
	case "README", "README.md", "go.mod", "go.sum", "Makefile":
		return true
	}

	return strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "internal"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "pkg"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "src"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "scripts"+string(filepath.Separator))
}

func (m *Manager) findPathSuggestions(query string, limit int) []string {
	if !m.Enabled() || limit <= 0 {
		return nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	raw := strings.ToLower(filepath.Base(query))
	stem := strings.TrimSuffix(raw, filepath.Ext(raw))
	terms := []string{raw}

	if stem != "" && stem != raw {
		terms = append(terms, stem)
	}

	// Phase 1 — substring match on base name / relative path. This catches
	// the common "user typed a typo in the filename" case.
	var suggestions []string

	candidates := m.collectWorkspacePathCandidates()
	for _, candidate := range candidates {
		match := false

		for _, term := range terms {
			if term == "" {
				continue
			}

			if strings.Contains(candidate.base, term) || strings.Contains(candidate.rel, term) {
				match = true
				break
			}
		}

		if !match {
			continue
		}

		suggestions = append(suggestions, m.handleDisplay(candidate.path))
		if len(suggestions) >= limit {
			break
		}
	}

	if len(suggestions) > 0 {
		slices.Sort(suggestions)

		return suggestions
	}

	// Phase 2 — Fix 2 (extended). Base-name substring found nothing, which
	// happens when the model fabricates both the directory AND the file name
	// (e.g. "internal/llm/llm.go" in a repo that has no "llm" anywhere).
	// Fall back to full-path edit distance against what we saw during the
	// walk. We prefer paths whose parent dir shares a prefix with the
	// query's parent dir so siblings of the missing package rise to the top.
	queryLower := strings.ToLower(strings.ReplaceAll(filepath.Clean(query), "\\", "/"))
	queryParent := strings.ToLower(filepath.Dir(queryLower))

	type scored struct {
		path     string
		distance int
		priority int
	}

	scoredList := make([]scored, 0, len(candidates))

	for _, candidate := range candidates {
		parent := filepath.Dir(candidate.rel)
		priority := 0

		if queryParent != "" && strings.HasPrefix(parent, firstPathSegment(queryParent)+"/") {
			priority = -2
		} else if queryParent != "" && strings.HasPrefix(parent, firstPathSegment(queryParent)) {
			priority = -1
		}

		distance := levenshtein(queryLower, candidate.rel)

		scoredList = append(scoredList, scored{path: candidate.rel, distance: distance, priority: priority})
	}

	slices.SortFunc(scoredList, func(a, b scored) int {
		if c := cmp.Compare(a.priority, b.priority); c != 0 {
			return c
		}

		if c := cmp.Compare(a.distance, b.distance); c != 0 {
			return c
		}

		return cmp.Compare(a.path, b.path)
	})

	// Guard against runaway noise: only return candidates whose distance is
	// small relative to the query length. Keeps "ls -la" suggestions from
	// polluting a request for "internal/llm/llm.go".
	maxDistance := len(queryLower) * 2 / 3
	if maxDistance < 6 {
		maxDistance = 6
	}

	for _, s := range scoredList {
		if len(suggestions) >= limit {
			break
		}

		if s.distance > maxDistance {
			break
		}

		suggestions = append(suggestions, s.path)
	}

	// Phase 3 — if the query's *parent* directory does not exist, also list
	// sibling directories of the closest real ancestor so the model sees
	// the real neighborhood rather than a totally empty "not found" error.
	if len(suggestions) < limit {
		for _, sibling := range m.siblingDirectoriesOfMissingAncestor(queryLower) {
			suggestions = append(suggestions, sibling)
			if len(suggestions) >= limit {
				break
			}
		}
	}

	return suggestions
}

// symbolSearchSuggestion returns a neutral note when a search_files
// zero-hit query looks more like a symbol name than a filename. It is
// descriptive rather than copy-paste-ready so evidence tools do not
// prescribe the next action directly.
func (m *Manager) symbolSearchSuggestion(pattern string) string {
	if m == nil {
		return ""
	}

	active := m.ActiveLanguages()
	if len(active) == 0 {
		return ""
	}

	var langs []string

	for _, lang := range active {
		tool := lang.Tool(semanticlanguage.CapWorkspaceSymbol)
		if tool == "" {
			tool = lang.Tool(semanticlanguage.CapFindSymbol)
		}

		if tool == "" {
			continue
		}

		langs = append(langs, lang.Name)
	}

	if len(langs) == 0 {
		return ""
	}

	slices.Sort(langs)

	return fmt.Sprintf("Pattern shape: bare identifier; this may refer to a symbol rather than a filename. Active language indexes: %s.", strings.Join(langs, ", "))
}

// tokenMatchedPaths splits a compound pattern like "task_tool" on
// common separators and returns up to `limit` workspace paths whose
// base name contains any of the tokens. Filters out trivial tokens
// (< 3 chars) to keep noise down. Used by the search_files zero-hit
// enrichment so a pattern like "task_tool" surfaces tools.go (via the
// "tool" token) when the whole-pattern fuzzy match comes up empty.
func (m *Manager) tokenMatchedPaths(pattern string, limit int) []string {
	if !m.Enabled() || limit <= 0 {
		return nil
	}

	tokens := splitIdentifierTokens(pattern)
	if len(tokens) == 0 {
		return nil
	}

	var out []string

	for _, candidate := range m.collectWorkspacePathCandidates() {
		for _, tok := range tokens {
			if !strings.Contains(candidate.base, tok) {
				continue
			}

			out = append(out, m.handleDisplay(candidate.path))
			break
		}

		if len(out) >= limit {
			break
		}
	}

	slices.Sort(out)

	return out
}

type workspacePathCandidate struct {
	path string
	rel  string
	base string
}

func (m *Manager) collectWorkspacePathCandidates() []workspacePathCandidate {
	scanned := 0
	scanCap := m.cfg.MaxDirectoryEntries * 10

	if scanCap <= 0 {
		scanCap = 4096
	}

	candidates := make([]workspacePathCandidate, 0, min(scanCap, 4096))

	_ = safepath.WalkDirUnderRoot(m.cfg.Root, m.cfg.Root, "workspace path suggestions", func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if m.shouldSkipListEntry(m.cfg.Root, path, true) {
				return filepath.SkipDir
			}

			return nil
		}

		if m.shouldSkipListEntry(m.cfg.Root, path, false) {
			return nil
		}

		scanned++
		if scanned > scanCap {
			return filepath.SkipAll
		}

		candidates = append(candidates, workspacePathCandidate{
			path: path,
			rel:  strings.ToLower(m.pathAccess.DisplayPath(path)),
			base: strings.ToLower(filepath.Base(path)),
		})

		return nil
	})

	return candidates
}

// splitIdentifierTokens breaks "task_tool.go", "TaskTool", "task-tool",
// and similar compounds into their constituent tokens, lower-cased,
// skipping tokens under 3 chars.
func splitIdentifierTokens(pattern string) []string {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return nil
	}

	// Normalize separators first, then insert breaks before capitals
	// so CamelCase also splits.
	norm := make([]rune, 0, len(p)*2)
	for i, r := range p {
		switch r {
		case '_', '-', '.', '/', ':':
			norm = append(norm, ' ')
		default:
			if i > 0 && r >= 'A' && r <= 'Z' {
				norm = append(norm, ' ')
			}

			norm = append(norm, r)
		}
	}

	raw := strings.Fields(strings.ToLower(string(norm)))

	var out []string

	seen := make(map[string]bool)

	for _, t := range raw {
		if len(t) < 3 || seen[t] {
			continue
		}

		seen[t] = true
		out = append(out, t)
	}

	return out
}

// looksLikeBareIdentifier reports whether a pattern is a single
// identifier-shaped token (letters, digits, underscore, dot, slash,
// colon). Used by the content-search zero-hit path so we only surface
// filename suggestions for queries the model likely intends as a
// symbol — full regex queries would produce misleading suggestions.
func looksLikeBareIdentifier(pattern string) bool {
	p := strings.TrimSpace(pattern)
	if p == "" || len(p) < 2 {
		return false
	}

	for _, r := range p {
		switch {
		case r == '_' || r == '.' || r == '/' || r == ':' || r == '-':
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		default:
			return false
		}
	}

	return true
}

// formatPathSuggestions wraps a suggestion list into a consistent
// "Did you mean:" block the model can parse at a glance. Returns the empty
// string when there are no suggestions so the tool's Output stays blank
// rather than advertising an empty block.
func formatPathSuggestions(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Did you mean one of these (nearest matches from the workspace):\n")

	for _, s := range suggestions {
		b.WriteString("- ")
		b.WriteString(s)
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// firstPathSegment returns the portion of path up to (but not including) the
// first "/". Used to rank sibling directories higher than random files.
func firstPathSegment(path string) string {
	if idx := strings.IndexByte(path, '/'); idx > 0 {
		return path[:idx]
	}

	return path
}

// siblingDirectoriesOfMissingAncestor returns human-readable directory hints
// ("internal/runtime/agent/ (5 files)") for the real directories that share the
// closest-existing ancestor with a missing path. Helps redirect a model that
// fabricated an entire directory (e.g. "internal/llm/") back toward the
// packages that actually exist.
func (m *Manager) siblingDirectoriesOfMissingAncestor(queryLower string) []string {
	if queryLower == "" {
		return nil
	}

	segments := strings.Split(strings.Trim(queryLower, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		ancestor := strings.Join(segments[:i], "/")
		ancestorAbs := m.cfg.Root

		if ancestor != "" {
			ancestorAbs = filepath.Join(m.cfg.Root, ancestor)
		}

		info, err := os.Stat(ancestorAbs)
		if err != nil || !info.IsDir() {
			continue
		}

		entries, err := safepath.ReadDirUnderRoot(m.cfg.Root, ancestorAbs, "read workspace directory", safepath.Options{})
		if err != nil {
			continue
		}

		hints := make([]string, 0, 6)

		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}

			child := filepath.Join(ancestorAbs, e.Name())
			if m.shouldSkipListEntry(m.cfg.Root, child, true) {
				continue
			}

			rel := e.Name()
			if ancestor != "" {
				rel = ancestor + "/" + e.Name()
			}

			hints = append(hints, rel+"/")
			if len(hints) >= 6 {
				break
			}
		}

		return hints
	}

	return nil
}

// levenshtein computes the case-sensitive edit distance between a and b.
// Small stand-alone implementation — avoids adding a dependency.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}

	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}

	if lb == 0 {
		return la
	}

	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, la+1)
	curr := make([]int, la+1)

	for i := 0; i <= la; i++ {
		prev[i] = i
	}

	for j := 1; j <= lb; j++ {
		curr[0] = j

		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost

			curr[i] = min(del, ins, sub)
		}

		prev, curr = curr, prev
	}

	return prev[la]
}
