package languagetools

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/repository/discovery"
	semanticpython "idiom/internal/repository/semantic/python"
)

// pythonProjectMarkers are the signals we use to distinguish a Python
// workspace from an incidental checkout that happens to contain a .py
// file. Any one of these promotes the workspace to "Python" for
// detection purposes. Absent all of them we still allow the tools to
// run if any .py / .pyi file exists under the root.
var pythonProjectMarkers = []string{
	"pyproject.toml",
	"setup.py",
	"setup.cfg",
	"requirements.txt",
	"Pipfile",
	"poetry.lock",
}

func (m *Manager) isPythonWorkspace() bool {
	if !m.Enabled() {
		return false
	}

	for _, marker := range pythonProjectMarkers {
		if info, err := os.Stat(filepath.Join(m.Root(), marker)); err == nil && !info.IsDir() {
			return true
		}
	}

	// Fallback: any .py under root (skipping the usual vendor dirs).
	found := false

	_ = filepath.WalkDir(m.Root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if discovery.ShouldSkipDirName(d.Name()) && path != m.Root() {
				return filepath.SkipDir
			}

			if _, skip := pythonWorkspaceSkipDirs[d.Name()]; skip && path != m.Root() {
				return filepath.SkipDir
			}

			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		if isPythonExtension(filepath.Ext(path)) {
			found = true
			return filepath.SkipAll
		}

		return nil
	})

	return found
}

var pythonWorkspaceSkipDirs = map[string]struct{}{
	".venv":         {},
	"venv":          {},
	"__pycache__":   {},
	".mypy_cache":   {},
	".pytest_cache": {},
	".tox":          {},
	"site-packages": {},
	".eggs":         {},
}

// getPythonIndex builds (or rebuilds) the Python semantic index on
// demand. We deliberately don't cache it on the Manager struct: adding
// Manager fields is out of scope for this change (the field set lives
// in manager.go which we can't edit). Python workspaces are usually
// small enough that a per-call rebuild is fine; if this ever shows up
// in profiles, a cache can be added in a follow-up alongside the
// integration-doc snippet that plumbs a field into Manager.
func (m *Manager) getPythonIndex() (*semanticpython.PythonIndex, error) {
	if !m.isPythonWorkspace() {
		return nil, fmt.Errorf("python-specific tools require a workspace with Python source files or a project file (pyproject.toml, setup.py, setup.cfg, requirements.txt)")
	}

	index, err := semanticpython.BuildPythonIndex(m.Root())
	if err != nil {
		return nil, fmt.Errorf("build Python semantic index: %w", err)
	}

	if index == nil {
		return nil, fmt.Errorf("no Python files were indexed under the workspace")
	}

	return index, nil
}

func (m *Manager) pythonOverview() ToolResult {
	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_overview", Summary: "Python overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)

	summary := index.Summary()
	if summary == "" {
		summary = "Python semantic index is available, but no summary could be generated."
	}

	return ToolResult{Tool: "python_overview", Summary: "Built Python semantic overview for the workspace", Output: summary}
}

func (m *Manager) pythonEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_entrypoints", Summary: "Python entrypoints unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(goResultLimit(req.MaxDepth, 6))
	if len(paths) == 0 {
		return ToolResult{Tool: "python_entrypoints", Summary: "No Python entrypoint files found"}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		file := index.FileByPath(rel)

		display, err := m.pythonHandleDisplay(rel)
		if err != nil {
			continue
		}

		line := display
		if file != nil {
			line += fmt.Sprintf(" (funcs=%d, methods=%d, classes=%d, imports=%d)", file.Funcs, file.Methods, file.Classes, len(file.Imports))
		}

		lines = append(lines, line)
	}

	return ToolResult{Tool: "python_entrypoints", Summary: fmt.Sprintf("Found %d likely Python anchor file(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) pythonFindSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "python_find_symbol", Summary: "Python symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_find_symbol", Summary: "Python symbol search unavailable", Error: err.Error()}
	}

	matches := filterPythonSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.pythonPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "python_find_symbol", Summary: fmt.Sprintf("No Python symbols matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.pythonHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, pythonDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "python_find_symbol", Summary: fmt.Sprintf("Found %d matching Python symbol(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) pythonReadSymbol(req ToolRequest) ToolResult {
	if err := validateSymbolPattern("python_read_symbol", req.Pattern); err != nil {
		return ToolResult{Tool: "python_read_symbol", Summary: "Python symbol read rejected", Error: err.Error()}
	}

	match, err := m.selectPythonSymbolMatch(req)
	if err != nil {
		if result, ok := m.pythonReadSymbolPathMiss(req); ok {
			return result
		}

		return ToolResult{Tool: "python_read_symbol", Summary: "Python symbol read rejected", Error: err.Error()}
	}

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "python_read_symbol", Summary: "Failed to resolve Python symbol path", Error: err.Error()}
	}

	m.rememberPath(abs)

	start := req.StartLine
	end := req.EndLine

	if start <= 0 || end <= 0 || start > end {
		start = match.StartLine
		if start <= 0 {
			start = match.Line
		}

		end = match.EndLine
		if end <= 0 {
			end = start + 24
		}
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "python_read_symbol", Summary: "Failed to read Python symbol snippet", Error: err.Error()}
	}

	displayPath := m.displayPath(abs)

	return ToolResult{
		Tool:          "python_read_symbol",
		Summary:       fmt.Sprintf("Read %s in %s lines %d-%d", pythonDisplayName(match.Qualified, match.Name), displayPath, start, end),
		Output:        snippet,
		Paths:         []string{displayPath},
		SourceWindows: []SourceWindowPlan{{Path: displayPath, StartLine: start, EndLine: end}},
	}
}

func (m *Manager) pythonRelatedFiles(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "python_related_files", Summary: "Python related-file search rejected", Error: "pattern is required"}
	}

	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_related_files", Summary: "Python related-file search unavailable", Error: err.Error()}
	}

	paths := filterRelatedPaths(index.RelevantFiles(strings.Fields(pattern), goResultLimit(req.MaxDepth, 8)), m.pythonPathFilter(req.Path))

	return m.relatedFilesResult("python_related_files", "Python", pattern, paths, m.pythonHandleDisplay)
}

func (m *Manager) pythonWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "python_workspace_symbol", Summary: "Python workspace-symbol query rejected", Error: "pattern is required"}
	}

	pathFilter, err := m.pythonPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.pythonWorkspaceSymbolsViaLSP(ctx, pattern, pathFilter, goResultLimit(req.MaxDepth, 8)); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "python_workspace_symbol",
				Summary: "Resolved Python workspace symbols via LSP",
				Output:  output,
			}
		}
	}

	result := m.pythonFindSymbol(req)

	result.Tool = "python_workspace_symbol"

	if result.Error == "" && strings.HasPrefix(result.Summary, "Found ") {
		result.Summary = strings.Replace(result.Summary, "Python symbol", "Python workspace symbol", 1)
	}

	return result
}

func (m *Manager) pythonDefinition(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectPythonSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "python_definition", Summary: "Python definition query rejected", Error: err.Error()}
	}

	if output, err := m.pythonDefinitionViaLSP(ctx, match); err == nil && strings.TrimSpace(output) != "" {
		return ToolResult{
			Tool:    "python_definition",
			Summary: "Resolved Python definition via LSP",
			Output:  output,
		}
	}

	display, err := m.pythonHandleDisplay(match.Path)
	if err != nil {
		return ToolResult{Tool: "python_definition", Summary: "Python definition unavailable", Error: err.Error()}
	}

	return ToolResult{Tool: "python_definition", Summary: "Resolved Python definition via semantic index", Output: fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, pythonDisplayName(match.Qualified, match.Name))}
}

func (m *Manager) pythonImplementations(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectPythonSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "python_implementations", Summary: "Python implementation query rejected", Error: err.Error()}
	}

	pathFilter, err := m.pythonPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.pythonImplementationsViaLSP(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "python_implementations",
				Summary: "Resolved Python implementations via LSP",
				Output:  output,
			}
		}
	}

	// Lightweight semantic fallback: subclass heuristic from the index.
	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_implementations", Summary: "Python implementations unavailable", Error: err.Error()}
	}

	impls := filterPythonSymbolMatches(index.Implementations(match.Name), pathFilter)
	if len(impls) == 0 {
		return ToolResult{Tool: "python_implementations", Summary: "Python implementations unavailable", Error: fmt.Sprintf("no implementations found for %q in the available Python symbol evidence", match.Name)}
	}

	lines := make([]string, 0, len(impls))
	for _, impl := range impls {
		display, err := m.pythonHandleDisplay(impl.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, impl.Line, impl.Kind, pythonDisplayName(impl.Qualified, impl.Name)))
	}

	return ToolResult{
		Tool:    "python_implementations",
		Summary: "Resolved Python implementations via semantic fallback",
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) pythonReferences(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectPythonSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "python_references", Summary: "Python reference query rejected", Error: err.Error()}
	}

	pathFilter, err := m.pythonPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.pythonReferencesViaLSP(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "python_references",
				Summary: "Resolved Python references via LSP",
				Output:  output,
			}
		}
	}

	searchReq := ToolRequest{
		Pattern: pythonReferencePattern(match.Name),
		Path:    defaultString(req.Path, "."),
		Glob:    pythonSearchGlob(),
	}
	result := m.searchContent(ctx, searchReq, "python_references")

	if result.Error == "" {
		result.Summary = "Resolved Python references via semantic/rg fallback"
	}

	return result
}

func (m *Manager) pythonCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectPythonSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "python_calls_from", Summary: "Python outgoing-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.pythonPathFilterForRequest(req)
	if err == nil {
		if payload, lspErr := m.pythonCallHierarchyViaLSP(ctx, match, pathFilter, false); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "python_calls_from",
				Summary:       "Resolved outgoing Python calls via LSP call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_calls_from", Summary: "Python outgoing-call query unavailable", Error: err.Error()}
	}

	matches := filterPythonCallMatches(index.FindCallsFrom(match.Name, 24), m.pythonPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "python_calls_from", Summary: fmt.Sprintf("No outgoing Python calls matched %s", pythonDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.pythonHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, call.CalleeName))
	}

	return ToolResult{Tool: "python_calls_from", Summary: fmt.Sprintf("Found %d outgoing Python call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) pythonCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectPythonSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "python_calls_into", Summary: "Python incoming-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.pythonPathFilterForRequest(req)
	if err == nil {
		if payload, lspErr := m.pythonCallHierarchyViaLSP(ctx, match, pathFilter, true); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "python_calls_into",
				Summary:       "Resolved incoming Python calls via LSP call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_calls_into", Summary: "Python incoming-call query unavailable", Error: err.Error()}
	}

	matches := filterPythonCallMatches(index.FindCallsInto(match.Name, 24), m.pythonPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "python_calls_into", Summary: fmt.Sprintf("No incoming Python calls matched %s", pythonDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.pythonHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, pythonDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "python_calls_into", Summary: fmt.Sprintf("Found %d incoming Python call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) pythonFileOutline(req ToolRequest) ToolResult {
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "python_file_outline", Summary: "Python file outline rejected", Error: err.Error()}
	}

	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{Tool: "python_file_outline", Summary: "Python file outline unavailable", Error: err.Error()}
	}

	rel := filepath.ToSlash(m.displayPath(path))
	file := index.FileByPath(rel)

	if file == nil {
		return ToolResult{Tool: "python_file_outline", Summary: "Python file outline unavailable", Error: fmt.Sprintf("no Python file indexed at %s", rel)}
	}

	m.rememberPath(path)

	var b strings.Builder

	header := rel
	tags := make([]string, 0, 3)

	if file.IsInit {
		tags = append(tags, "package-init")
	}

	if file.HasMain {
		tags = append(tags, "has-main")
	}

	if file.IsTest {
		tags = append(tags, "test")
	}

	if len(tags) > 0 {
		header += " (" + strings.Join(tags, ", ") + ")"
	}

	fmt.Fprintf(&b, "file: %s\n", header)
	fmt.Fprintf(&b, "counts: funcs=%d methods=%d classes=%d\n", file.Funcs, file.Methods, file.Classes)

	if len(file.RawImports) > 0 {
		b.WriteString("imports:\n")

		imports := append([]string(nil), file.RawImports...)
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
		symbols := append([]semanticpython.PythonSymbol(nil), file.Symbols...)
		slices.SortStableFunc(symbols, func(a, b semanticpython.PythonSymbol) int { return cmp.Compare(a.Line, b.Line) })
		b.WriteString("declarations:\n")

		for _, sym := range symbols {
			name := pythonDisplayName(sym.Qualified, sym.Name)
			start := sym.StartLine

			if start <= 0 {
				start = sym.Line
			}

			end := sym.EndLine
			if end < start {
				end = start
			}

			scope := strings.TrimSpace(sym.Scope)
			line := fmt.Sprintf("- %s (%s) lines %d-%d", name, sym.Kind, start, end)

			if scope != "" && scope != name {
				line += " scope:" + scope
			}

			b.WriteString(line + "\n")
		}
	}

	summary := fmt.Sprintf("Outlined %d symbol(s) in %s. Read-symbol capability is available for a specific body.", len(file.Symbols), rel)

	return ToolResult{
		Tool:    "python_file_outline",
		Summary: summary,
		Output:  strings.TrimSpace(b.String()),
	}
}

func (m *Manager) pythonDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	if binary, _ := m.pythonLSPBinary(); binary == "" {
		return ToolResult{Tool: "python_diagnostics", Summary: "Python diagnostics unavailable", Error: "no Python LSP server available on PATH (pyright-langserver or pylsp). Diagnostics not produced."}
	}

	files, err := m.pythonDiagnosticTargets(req)
	if err != nil {
		return ToolResult{Tool: "python_diagnostics", Summary: "Python diagnostics unavailable", Error: err.Error()}
	}

	if len(files) == 0 {
		return ToolResult{Tool: "python_diagnostics", Summary: "No Python files available for diagnostics"}
	}

	lines := make([]string, 0, len(files))
	for _, rel := range files {
		abs, err := m.resolvePath(rel, "")
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: %s", rel, err.Error()))
			continue
		}

		diagnostics, err := m.pythonDiagnosticsViaLSP(ctx, abs)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: %s", rel, err.Error()))
			continue
		}

		if len(diagnostics) == 0 {
			lines = append(lines, fmt.Sprintf("%s: no warnings or errors", rel))
			continue
		}

		for _, d := range diagnostics {
			severity := pythonDiagSeverity(d.Severity)
			line := fmt.Sprintf("%s:%d:%d %s %s", rel, d.Range.Start.Line+1, d.Range.Start.Character+1, severity, strings.TrimSpace(d.Message))

			lines = append(lines, line)
		}
	}

	return ToolResult{Tool: "python_diagnostics", Summary: fmt.Sprintf("Collected Python diagnostics for %d file(s).", len(files)), Output: strings.Join(lines, "\n"), Facts: diagnosticToolFacts(files)}
}

func pythonDiagSeverity(code int) string {
	switch code {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "diagnostic"
	}
}

func (m *Manager) pythonDiagnosticTargets(req ToolRequest) ([]string, error) {
	index, err := m.getPythonIndex()
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
			if !isPythonExtension(filepath.Ext(rel)) {
				return nil, fmt.Errorf("%s is not a Python file", rel)
			}

			return []string{rel}, nil
		}

		filter := filepath.ToSlash(m.displayPath(resolved))
		files := make([]string, 0)

		for _, file := range index.Files {
			if pathMatchesFilter(file.Path, filter) {
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

	return files, nil
}

func (m *Manager) selectPythonSymbolMatch(req ToolRequest) (semanticpython.PythonSymbolMatch, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return semanticpython.PythonSymbolMatch{}, fmt.Errorf("pattern is required")
	}

	index, err := m.getPythonIndex()
	if err != nil {
		return semanticpython.PythonSymbolMatch{}, err
	}

	pathFilter, err := m.pythonPathFilterForRequest(req)
	if err != nil {
		return semanticpython.PythonSymbolMatch{}, err
	}

	matches := filterPythonSymbolMatches(index.FindSymbols(pattern, 12), pathFilter)
	if len(matches) == 0 {
		if pathFilter != "" {
			return semanticpython.PythonSymbolMatch{}, fmt.Errorf("no Python symbols matched %q under %s", pattern, pathFilter)
		}

		return semanticpython.PythonSymbolMatch{}, fmt.Errorf("no Python symbols matched %q", pattern)
	}

	return matches[0], nil
}

func (m *Manager) pythonPathFilter(path string) string {
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

func (m *Manager) pythonPathFilterForRequest(req ToolRequest) (string, error) {
	return m.languagePathFilterForRequest(req, m.pythonPathFilter)
}

func (m *Manager) pythonHandleDisplay(rel string) (string, error) {
	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}

	return m.handleDisplay(abs), nil
}

func filterPythonSymbolMatches(matches []semanticpython.PythonSymbolMatch, pathFilter string) []semanticpython.PythonSymbolMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semanticpython.PythonSymbolMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func filterPythonCallMatches(matches []semanticpython.PythonCallMatch, pathFilter string) []semanticpython.PythonCallMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semanticpython.PythonCallMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Caller.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func pythonReferencePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return pattern
	}

	if regexpSafeIdentifier(pattern) {
		return `\b` + pattern + `\b`
	}

	return regexpQuoteMeta(pattern)
}

func pythonSearchGlob() string {
	return "*.{py,pyi}"
}

func isPythonExtension(ext string) bool {
	_, ok := map[string]struct{}{
		".py":  {},
		".pyi": {},
	}[strings.ToLower(ext)]

	return ok
}

func pythonDisplayName(qualified string, name string) string {
	qualified = strings.TrimSpace(qualified)
	if qualified != "" {
		return qualified
	}

	return strings.TrimSpace(name)
}
