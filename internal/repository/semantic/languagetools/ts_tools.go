package languagetools

// TypeScript/JavaScript structured tools. Mirrors cpp_tools.go at the
// same depth: all 13 canonical capabilities, a semantic-index-backed
// fallback, an LSP-backed path that uses typescript-language-server when
// available. No include-graph / blast-radius / dead-symbol extras - those
// are C++-specific.
//
// The index is cached per-Manager in a package-level map rather than on
// the Manager struct itself so we don't need to modify manager.go.

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/repository/discovery"
	semanticts "idiom/internal/repository/semantic/typescript"
)

// isTsWorkspace returns true if the workspace root looks like a
// TypeScript/JavaScript project: a tsconfig/jsconfig/package.json sits at
// the root, or any .ts/.tsx/.js/.jsx/.mjs/.cjs file is found on walk.
func (m *Manager) isTsWorkspace() bool {
	if !m.Enabled() {
		return false
	}

	for _, marker := range []string{"tsconfig.json", "jsconfig.json", "package.json"} {
		if info, err := os.Stat(filepath.Join(m.Root(), marker)); err == nil && !info.IsDir() {
			return true
		}
	}

	found := false

	_ = filepath.WalkDir(m.Root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if discovery.ShouldSkipDirName(d.Name()) && path != m.Root() {
				return filepath.SkipDir
			}

			if name := d.Name(); name == ".next" || name == "node_modules" {
				return filepath.SkipDir
			}

			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		if isTsExtension(filepath.Ext(path)) {
			found = true
			return filepath.SkipAll
		}

		return nil
	})

	return found
}

func (m *Manager) getTsIndex() (*semanticts.TsIndex, error) {
	if !m.isTsWorkspace() {
		return nil, fmt.Errorf("typescript-specific tools require a workspace with TS/JS source files or a tsconfig/jsconfig/package.json")
	}

	token := m.currentGoIndexToken()
	m.mu.Lock()
	cached := m.tsIndex
	cachedToken := m.tsIndexToken
	m.mu.Unlock()

	if cached != nil && (token == "" || token == cachedToken) {
		return cached, nil
	}

	index, err := semanticts.BuildTsIndex(m.Root())
	if err != nil {
		return nil, fmt.Errorf("build TypeScript semantic index: %w", err)
	}

	if index == nil {
		return nil, fmt.Errorf("no TypeScript/JavaScript files were indexed under the workspace")
	}

	m.mu.Lock()
	m.tsIndex = index
	m.tsIndexToken = token
	m.mu.Unlock()

	return index, nil
}

// InvalidateTsIndex forgets the cached index for this Manager. Intended
// for callers that know they mutated TS files and want the next query to
// rebuild. Safe to call concurrently.
func (m *Manager) InvalidateTsIndex() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.tsIndex = nil
	m.tsIndexToken = ""
	m.mu.Unlock()
}

func (m *Manager) tsOverview() ToolResult {
	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{Tool: "ts_overview", Summary: "TypeScript overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)

	summary := index.Summary()
	if summary == "" {
		summary = "TypeScript semantic index is available, but no summary could be generated."
	}

	return ToolResult{Tool: "ts_overview", Summary: "Built TypeScript semantic overview for the workspace", Output: summary}
}

func (m *Manager) tsEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{Tool: "ts_entrypoints", Summary: "TypeScript entrypoints unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(goResultLimit(req.MaxDepth, 6))
	if len(paths) == 0 {
		return ToolResult{Tool: "ts_entrypoints", Summary: "No TypeScript entrypoint files found"}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		file := index.FileByPath(rel)

		display, err := m.tsHandleDisplay(rel)
		if err != nil {
			continue
		}

		line := display
		if file != nil {
			line += fmt.Sprintf(" (funcs=%d, methods=%d, types=%d, imports=%d)", file.Funcs, file.Methods, file.Types, len(file.Imports))
		}

		lines = append(lines, line)
	}

	return ToolResult{Tool: "ts_entrypoints", Summary: fmt.Sprintf("Found %d likely TypeScript anchor file(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) tsFindSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "ts_find_symbol", Summary: "TypeScript symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{Tool: "ts_find_symbol", Summary: "TypeScript symbol search unavailable", Error: err.Error()}
	}

	matches := filterTsSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.tsPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "ts_find_symbol", Summary: fmt.Sprintf("No TypeScript symbols matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.tsHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, tsDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "ts_find_symbol", Summary: fmt.Sprintf("Found %d matching TypeScript symbol(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) tsReadSymbol(req ToolRequest) ToolResult {
	if err := validateSymbolPattern("ts_read_symbol", req.Pattern); err != nil {
		return ToolResult{Tool: "ts_read_symbol", Summary: "TypeScript symbol read rejected", Error: err.Error()}
	}

	match, err := m.selectTsSymbolMatch(req)
	if err != nil {
		if result, ok := m.tsReadSymbolPathMiss(req); ok {
			return result
		}

		return ToolResult{Tool: "ts_read_symbol", Summary: "TypeScript symbol read rejected", Error: err.Error()}
	}

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "ts_read_symbol", Summary: "Failed to resolve TypeScript symbol path", Error: err.Error()}
	}

	m.rememberPath(abs)

	start := req.StartLine
	end := req.EndLine

	if start <= 0 || end <= 0 || start > end {
		start = match.Line - 6
		if start < 1 {
			start = 1
		}

		end = match.Line + 24
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "ts_read_symbol", Summary: "Failed to read TypeScript symbol snippet", Error: err.Error()}
	}

	return ToolResult{Tool: "ts_read_symbol", Summary: fmt.Sprintf("Read %s in %s lines %d-%d", tsDisplayName(match.Qualified, match.Name), m.displayPath(abs), start, end), Output: snippet}
}

func (m *Manager) tsRelatedFiles(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "ts_related_files", Summary: "TypeScript related-file search rejected", Error: "pattern is required"}
	}

	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{Tool: "ts_related_files", Summary: "TypeScript related-file search unavailable", Error: err.Error()}
	}

	paths := filterRelatedPaths(index.RelevantFiles(strings.Fields(pattern), goResultLimit(req.MaxDepth, 8)), m.tsPathFilter(req.Path))

	return m.relatedFilesResult("ts_related_files", "TypeScript", pattern, paths, m.tsHandleDisplay)
}

func (m *Manager) tsWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "ts_workspace_symbol", Summary: "TypeScript workspace-symbol query rejected", Error: "pattern is required"}
	}

	pathFilter, err := m.tsPathFilterForRequest(req)
	if err == nil && m.tsserverBinary() != "" {
		if output, lspErr := m.tsWorkspaceSymbolsViaLSP(ctx, pattern, pathFilter, goResultLimit(req.MaxDepth, 8)); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "ts_workspace_symbol",
				Summary: "Resolved TypeScript workspace symbols via typescript-language-server",
				Output:  output,
			}
		}
	}

	result := m.tsFindSymbol(req)

	result.Tool = "ts_workspace_symbol"

	if result.Error == "" && strings.HasPrefix(result.Summary, "Found ") {
		result.Summary = strings.Replace(result.Summary, "TypeScript symbol", "TypeScript workspace symbol", 1)
	}

	return result
}

func (m *Manager) tsDefinition(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectTsSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "ts_definition", Summary: "TypeScript definition query rejected", Error: err.Error()}
	}

	if m.tsserverBinary() != "" {
		if output, err := m.tsDefinitionViaLSP(ctx, match); err == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "ts_definition",
				Summary: "Resolved TypeScript definition via typescript-language-server",
				Output:  output,
			}
		}
	}

	display, err := m.tsHandleDisplay(match.Path)
	if err != nil {
		return ToolResult{Tool: "ts_definition", Summary: "TypeScript definition unavailable", Error: err.Error()}
	}

	return ToolResult{Tool: "ts_definition", Summary: "Resolved TypeScript definition via semantic index", Output: fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, tsDisplayName(match.Qualified, match.Name))}
}

func (m *Manager) tsImplementations(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectTsSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "ts_implementations", Summary: "TypeScript implementation query rejected", Error: err.Error()}
	}

	pathFilter, _ := m.tsPathFilterForRequest(req)
	if m.tsserverBinary() != "" {
		if output, lspErr := m.tsImplementationsViaLSP(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "ts_implementations",
				Summary: "Resolved TypeScript implementations via typescript-language-server",
				Output:  output,
			}
		}
	}

	if output, err := m.tsImplementationFallback(match, pathFilter); err == nil && strings.TrimSpace(output) != "" {
		return ToolResult{
			Tool:    "ts_implementations",
			Summary: "Resolved TypeScript implementations via semantic fallback",
			Output:  output,
		}
	}

	return ToolResult{Tool: "ts_implementations", Summary: "TypeScript implementations unavailable", Error: "typescript-language-server is required for implementation queries"}
}

func (m *Manager) tsReferences(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectTsSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "ts_references", Summary: "TypeScript reference query rejected", Error: err.Error()}
	}

	pathFilter, _ := m.tsPathFilterForRequest(req)
	if m.tsserverBinary() != "" {
		if output, lspErr := m.tsReferencesViaLSP(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "ts_references",
				Summary: "Resolved TypeScript references via typescript-language-server",
				Output:  output,
			}
		}
	}

	searchReq := ToolRequest{
		Pattern: tsReferencePattern(match.Name),
		Path:    defaultString(req.Path, "."),
		Glob:    tsSearchGlob(),
	}
	result := m.searchContent(ctx, searchReq, "ts_references")

	if result.Error == "" {
		result.Summary = "Resolved TypeScript references via semantic/rg fallback"
	}

	return result
}

func (m *Manager) tsCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectTsSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "ts_calls_from", Summary: "TypeScript outgoing-call query rejected", Error: err.Error()}
	}

	pathFilter, _ := m.tsPathFilterForRequest(req)
	if m.tsserverBinary() != "" {
		if payload, lspErr := m.tsCallHierarchyViaLSP(ctx, match, pathFilter, false); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "ts_calls_from",
				Summary:       "Resolved outgoing TypeScript calls via typescript-language-server call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{Tool: "ts_calls_from", Summary: "TypeScript outgoing-call query unavailable", Error: err.Error()}
	}

	matches := filterTsCallMatches(index.FindCallsFrom(match.Name, 24), m.tsPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "ts_calls_from", Summary: fmt.Sprintf("No outgoing TypeScript calls matched %s", tsDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.tsHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, call.CalleeName))
	}

	return ToolResult{Tool: "ts_calls_from", Summary: fmt.Sprintf("Found %d outgoing TypeScript call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) tsCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectTsSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "ts_calls_into", Summary: "TypeScript incoming-call query rejected", Error: err.Error()}
	}

	pathFilter, _ := m.tsPathFilterForRequest(req)
	if m.tsserverBinary() != "" {
		if payload, lspErr := m.tsCallHierarchyViaLSP(ctx, match, pathFilter, true); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "ts_calls_into",
				Summary:       "Resolved incoming TypeScript calls via typescript-language-server call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{Tool: "ts_calls_into", Summary: "TypeScript incoming-call query unavailable", Error: err.Error()}
	}

	matches := filterTsCallMatches(index.FindCallsInto(match.Name, 24), m.tsPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "ts_calls_into", Summary: fmt.Sprintf("No incoming TypeScript calls matched %s", tsDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.tsHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, tsDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "ts_calls_into", Summary: fmt.Sprintf("Found %d incoming TypeScript call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) tsFileOutline(req ToolRequest) ToolResult {
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "ts_file_outline", Summary: "TypeScript file outline rejected", Error: err.Error()}
	}

	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{Tool: "ts_file_outline", Summary: "TypeScript file outline unavailable", Error: err.Error()}
	}

	rel := filepath.ToSlash(m.displayPath(path))
	file := index.FileByPath(rel)

	if file == nil {
		return ToolResult{Tool: "ts_file_outline", Summary: "TypeScript file outline unavailable", Error: fmt.Sprintf("no TypeScript/JavaScript file indexed at %s", rel)}
	}

	m.rememberPath(path)

	var b strings.Builder

	header := rel
	tags := make([]string, 0, 3)

	if file.IsDeclFile {
		tags = append(tags, "declaration")
	}

	if file.IsTSX {
		tags = append(tags, "jsx")
	}

	if file.IsJS {
		tags = append(tags, "javascript")
	}

	if file.HasMain {
		tags = append(tags, "has-entrypoint")
	}

	if file.IsTest {
		tags = append(tags, "test")
	}

	if len(tags) > 0 {
		header += " (" + strings.Join(tags, ", ") + ")"
	}

	fmt.Fprintf(&b, "file: %s\n", header)
	fmt.Fprintf(&b, "counts: funcs=%d methods=%d types=%d classes=%d\n", file.Funcs, file.Methods, file.Types, file.Classes)

	if len(file.Imports) > 0 {
		b.WriteString("imports:\n")

		imports := append([]string(nil), file.Imports...)
		slices.Sort(imports)

		for _, imp := range imports {
			fmt.Fprintf(&b, "- %s\n", imp)
		}
	}

	if len(file.Symbols) == 0 {
		b.WriteString("declarations: (none indexed)\n")
	} else if len(file.Symbols) > 200 {
		fmt.Fprintf(&b, "declarations: omitted for large file (%d symbols).\n", len(file.Symbols))
	} else {
		symbols := append([]semanticts.TsSymbol(nil), file.Symbols...)
		slices.SortStableFunc(symbols, func(a, b semanticts.TsSymbol) int { return cmp.Compare(a.Line, b.Line) })
		b.WriteString("declarations:\n")

		for _, sym := range symbols {
			name := tsDisplayName(sym.Qualified, sym.Name)
			start := sym.StartLine

			if start <= 0 {
				start = sym.Line
			}

			end := sym.EndLine
			if end < start {
				end = start
			}

			line := fmt.Sprintf("- %s (%s) lines %d-%d", name, sym.Kind, start, end)
			if sym.Exported {
				line += " exported"
			}

			scope := strings.TrimSpace(sym.Scope)
			if scope != "" && scope != name {
				line += " scope:" + scope
			}

			b.WriteString(line + "\n")
		}
	}

	summary := fmt.Sprintf("Outlined %d symbol(s) in %s. Read-symbol capability is available for a specific body.", len(file.Symbols), rel)

	return ToolResult{
		Tool:    "ts_file_outline",
		Summary: summary,
		Output:  strings.TrimSpace(b.String()),
	}
}

func (m *Manager) tsDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	if m.tsserverBinary() == "" {
		return ToolResult{Tool: "ts_diagnostics", Summary: "TypeScript diagnostics unavailable", Error: "typescript-language-server is not installed"}
	}

	files, err := m.tsDiagnosticTargets(req)
	if err != nil {
		return ToolResult{Tool: "ts_diagnostics", Summary: "TypeScript diagnostics unavailable", Error: err.Error()}
	}

	if len(files) == 0 {
		return ToolResult{Tool: "ts_diagnostics", Summary: "No TypeScript files available for diagnostics"}
	}

	lines := make([]string, 0, len(files))
	for _, rel := range files {
		output, err := m.tsDiagnosticsViaLSP(ctx, rel)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: %s", rel, err.Error()))
			continue
		}

		output = strings.TrimSpace(output)
		if output == "" {
			lines = append(lines, fmt.Sprintf("%s: no warnings or errors", rel))
			continue
		}

		lines = append(lines, output)
	}

	return ToolResult{Tool: "ts_diagnostics", Summary: fmt.Sprintf("Collected TypeScript diagnostics for %d file(s).", len(files)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) selectTsSymbolMatch(req ToolRequest) (semanticts.TsSymbolMatch, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return semanticts.TsSymbolMatch{}, fmt.Errorf("pattern is required")
	}

	index, err := m.getTsIndex()
	if err != nil {
		return semanticts.TsSymbolMatch{}, err
	}

	pathFilter, err := m.tsPathFilterForRequest(req)
	if err != nil {
		return semanticts.TsSymbolMatch{}, err
	}

	matches := filterTsSymbolMatches(index.FindSymbols(pattern, 12), pathFilter)
	if len(matches) == 0 {
		if pathFilter != "" {
			return semanticts.TsSymbolMatch{}, fmt.Errorf("no TypeScript symbols matched %q under %s", pattern, pathFilter)
		}

		return semanticts.TsSymbolMatch{}, fmt.Errorf("no TypeScript symbols matched %q", pattern)
	}

	return matches[0], nil
}

func (m *Manager) tsImplementationFallback(match semanticts.TsSymbolMatch, pathFilter string) (string, error) {
	index, err := m.getTsIndex()
	if err != nil {
		return "", err
	}

	candidates := filterTsSymbolMatches(index.FindSymbols(match.Name, 64), pathFilter)
	impls := make([]semanticts.TsSymbolMatch, 0, len(candidates))
	seen := make(map[string]struct{})

	for _, candidate := range candidates {
		if candidate.Name != match.Name {
			continue
		}

		if candidate.Kind != "method" && candidate.Kind != "function" && candidate.Kind != "class" {
			continue
		}

		if candidate.Path == match.Path && candidate.Line == match.Line {
			continue
		}

		key := fmt.Sprintf("%s:%d:%s", candidate.Path, candidate.Line, candidate.Name)
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		impls = append(impls, candidate)
	}

	if len(impls) == 0 {
		return "", fmt.Errorf("no semantic implementation candidates found")
	}

	slices.SortFunc(impls, func(a, b semanticts.TsSymbolMatch) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}

		return cmp.Compare(a.Line, b.Line)
	})

	lines := make([]string, 0, len(impls))
	for _, candidate := range impls {
		display, err := m.tsHandleDisplay(candidate.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, candidate.Line, candidate.Kind, tsDisplayName(candidate.Qualified, candidate.Name)))
		if len(lines) >= m.maxDirectoryEntries() {
			break
		}
	}

	return strings.Join(lines, "\n"), nil
}

func (m *Manager) tsPathFilter(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	resolved, err := m.resolvePath(path, "")
	if err != nil {
		return ""
	}

	if filepath.Clean(resolved) == filepath.Clean(m.Root()) {
		return ""
	}

	return filepath.ToSlash(m.displayPath(resolved))
}

func (m *Manager) tsPathFilterForRequest(req ToolRequest) (string, error) {
	return m.languagePathFilterForRequest(req, m.tsPathFilter)
}

func (m *Manager) tsHandleDisplay(rel string) (string, error) {
	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}

	return m.handleDisplay(abs), nil
}

func (m *Manager) tsDiagnosticTargets(req ToolRequest) ([]string, error) {
	index, err := m.getTsIndex()
	if err != nil {
		return nil, err
	}

	resolveTarget := func(path string) ([]string, error) {
		resolved, err := m.resolvePath(path, "")
		if err != nil {
			return nil, err
		}

		info, err := os.Stat(resolved)
		if err != nil {
			return nil, err
		}

		if !info.IsDir() {
			rel := filepath.ToSlash(m.displayPath(resolved))
			if !isTsExtension(filepath.Ext(rel)) {
				return nil, fmt.Errorf("%s is not a TypeScript/JavaScript file", rel)
			}

			return []string{rel}, nil
		}

		filter := filepath.ToSlash(m.displayPath(resolved))
		files := make([]string, 0)

		for _, file := range index.Files {
			if pathMatchesFilter(file.Path, filter) && !file.IsDeclFile {
				files = append(files, file.Path)
			}
		}

		slices.Sort(files)

		if limit := goResultLimit(req.MaxDepth, 8); len(files) > limit {
			files = files[:limit]
		}

		return files, nil
	}
	if strings.TrimSpace(req.Path) != "" {
		return resolveTarget(req.Path)
	}

	files := index.DefaultFiles(goResultLimit(req.MaxDepth, 4))
	out := make([]string, 0, len(files))

	for _, path := range files {
		file := index.FileByPath(path)
		if file != nil && file.IsDeclFile {
			continue
		}

		out = append(out, path)
	}

	return out, nil
}

func filterTsSymbolMatches(matches []semanticts.TsSymbolMatch, pathFilter string) []semanticts.TsSymbolMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semanticts.TsSymbolMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func filterTsCallMatches(matches []semanticts.TsCallMatch, pathFilter string) []semanticts.TsCallMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semanticts.TsCallMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Caller.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func tsReferencePattern(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}

	if regexpSafeIdentifier(name) {
		return `\b` + name + `\b`
	}

	return regexpQuoteMeta(name)
}

func tsSearchGlob() string {
	return "*.{ts,tsx,mts,cts,js,jsx,mjs,cjs}"
}

func isTsExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func tsDisplayName(qualified string, name string) string {
	qualified = strings.TrimSpace(qualified)
	if qualified != "" {
		return qualified
	}

	return strings.TrimSpace(name)
}
