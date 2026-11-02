package languagetools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"idiom/internal/platform/hosttools"
	"idiom/internal/platform/safepath"
	"idiom/internal/repository/discovery"
	semanticcpp "idiom/internal/repository/semantic/cpp"
)

func (m *Manager) isCppWorkspace() bool {
	if !m.Enabled() {
		return false
	}

	if info, err := os.Stat(filepath.Join(m.Root(), "compile_commands.json")); err == nil && !info.IsDir() {
		return true
	}

	found := false

	_ = safepath.WalkDirUnderRoot(m.Root(), m.Root(), "C++ workspace detection", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if discovery.ShouldSkipDirName(d.Name()) && path != m.Root() {
				return filepath.SkipDir
			}

			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		if isCppExtension(filepath.Ext(path)) {
			found = true
			return filepath.SkipAll
		}

		return nil
	})

	return found
}

func (m *Manager) getCppIndex() (*semanticcpp.CppIndex, error) {
	if !m.isCppWorkspace() {
		return nil, fmt.Errorf("c++-specific tools require a workspace with C/C++ source files or compile_commands.json")
	}

	token := m.currentGoIndexToken()
	m.mu.Lock()

	cached := m.cppIndex
	cachedToken := m.cppIndexToken
	m.mu.Unlock()

	if cached != nil && (token == "" || token == cachedToken) {
		return cached, nil
	}

	index, err := semanticcpp.BuildCppIndex(m.Root())
	if err != nil {
		return nil, fmt.Errorf("build C++ semantic index: %w", err)
	}

	if index == nil {
		return nil, fmt.Errorf("no C/C++ files were indexed under the workspace")
	}

	m.mu.Lock()

	m.cppIndex = index
	m.cppIndexToken = token
	m.mu.Unlock()

	return index, nil
}

func (m *Manager) cppOverview() ToolResult {
	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_overview", Summary: "C++ overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)

	summary := index.Summary()
	if summary == "" {
		summary = "C++ semantic index is available, but no summary could be generated."
	}

	return ToolResult{Tool: "cpp_overview", Summary: "Built C++ semantic overview for the workspace", Output: summary}
}

func (m *Manager) cppEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_entrypoints", Summary: "C++ entrypoints unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(goResultLimit(req.MaxDepth, 6))
	if len(paths) == 0 {
		return ToolResult{Tool: "cpp_entrypoints", Summary: "No C++ entrypoint files found"}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		file := index.FileByPath(rel)

		display, err := m.cppHandleDisplay(rel)
		if err != nil {
			continue
		}

		line := display
		if file != nil {
			line += fmt.Sprintf(" (funcs=%d, methods=%d, types=%d, includes=%d)", file.Funcs, file.Methods, file.Types, len(file.Includes))
		}

		lines = append(lines, line)
	}

	return ToolResult{Tool: "cpp_entrypoints", Summary: fmt.Sprintf("Found %d likely C++ anchor file(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) cppFindSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_find_symbol", Summary: "C++ symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_find_symbol", Summary: "C++ symbol search unavailable", Error: err.Error()}
	}

	matches := filterCppSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.cppPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "cpp_find_symbol", Summary: fmt.Sprintf("No C++ symbols matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.cppHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, cppDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "cpp_find_symbol", Summary: fmt.Sprintf("Found %d matching C++ symbol(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) cppReadSymbol(req ToolRequest) ToolResult {
	if err := validateSymbolPattern("cpp_read_symbol", req.Pattern); err != nil {
		return ToolResult{Tool: "cpp_read_symbol", Summary: "C++ symbol read rejected", Error: err.Error()}
	}

	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		if result, ok := m.cppReadSymbolPathMiss(req); ok {
			return result
		}

		return ToolResult{Tool: "cpp_read_symbol", Summary: "C++ symbol read rejected", Error: err.Error()}
	}

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "cpp_read_symbol", Summary: "Failed to resolve C++ symbol path", Error: err.Error()}
	}

	m.rememberPath(abs)

	start := req.StartLine
	end := req.EndLine

	if start <= 0 || end <= 0 || start > end {
		start = match.Line - 12
		if start < 1 {
			start = 1
		}

		end = match.Line + 24
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "cpp_read_symbol", Summary: "Failed to read C++ symbol snippet", Error: err.Error()}
	}

	displayPath := m.displayPath(abs)

	return ToolResult{
		Tool:          "cpp_read_symbol",
		Summary:       fmt.Sprintf("Read %s in %s lines %d-%d", cppDisplayName(match.Qualified, match.Name), displayPath, start, end),
		Output:        snippet,
		Paths:         []string{displayPath},
		SourceWindows: []SourceWindowPlan{{Path: displayPath, StartLine: start, EndLine: end}},
	}
}

func (m *Manager) cppRelatedFiles(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_related_files", Summary: "C++ related-file search rejected", Error: "pattern is required"}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_related_files", Summary: "C++ related-file search unavailable", Error: err.Error()}
	}

	paths := filterRelatedPaths(index.RelevantFiles(strings.Fields(pattern), goResultLimit(req.MaxDepth, 8)), m.cppPathFilter(req.Path))

	return m.relatedFilesResult("cpp_related_files", "C++", pattern, paths, m.cppHandleDisplay)
}

func (m *Manager) cppWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_workspace_symbol", Summary: "C++ workspace-symbol query rejected", Error: "pattern is required"}
	}

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.cppWorkspaceSymbolsViaClangd(ctx, pattern, pathFilter, goResultLimit(req.MaxDepth, 8)); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "cpp_workspace_symbol",
				Summary: "Resolved C++ workspace symbols via clangd",
				Output:  output,
			}
		}
	}

	result := m.cppFindSymbol(req)

	result.Tool = "cpp_workspace_symbol"

	if result.Error == "" && strings.HasPrefix(result.Summary, "Found ") {
		result.Summary = strings.Replace(result.Summary, "C++ symbol", "C++ workspace symbol", 1)
	}

	return result
}

func (m *Manager) cppDefinition(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "cpp_definition", Summary: "C++ definition query rejected", Error: err.Error()}
	}

	if output, err := m.cppDefinitionViaClangd(ctx, match); err == nil && strings.TrimSpace(output) != "" {
		if !cppLocationOutputOnlyHeaders(output) {
			return ToolResult{
				Tool:    "cpp_definition",
				Summary: "Resolved C++ definition via clangd",
				Output:  output,
			}
		}

		if fallback, fallbackErr := m.cppDefinitionFallbackMatch(match); fallbackErr == nil {
			display, displayErr := m.cppHandleDisplay(fallback.Path)
			if displayErr == nil {
				return ToolResult{
					Tool:    "cpp_definition",
					Summary: "Resolved C++ definition via semantic index fallback",
					Output:  fmt.Sprintf("%s:%d %s %s", display, fallback.Line, fallback.Kind, cppDisplayName(fallback.Qualified, fallback.Name)),
				}
			}
		}

		return ToolResult{
			Tool:    "cpp_definition",
			Summary: "Resolved C++ definition via clangd",
			Output:  output,
		}
	}

	display, err := m.cppHandleDisplay(match.Path)
	if err != nil {
		return ToolResult{Tool: "cpp_definition", Summary: "C++ definition unavailable", Error: err.Error()}
	}

	return ToolResult{Tool: "cpp_definition", Summary: "Resolved C++ definition via semantic index", Output: fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, cppDisplayName(match.Qualified, match.Name))}
}

func (m *Manager) cppDeclaration(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "cpp_declaration", Summary: "C++ declaration query rejected", Error: err.Error()}
	}

	if output, err := m.cppDeclarationViaClangd(ctx, match); err == nil && strings.TrimSpace(output) != "" {
		return ToolResult{
			Tool:    "cpp_declaration",
			Summary: "Resolved C++ declaration via clangd",
			Output:  output,
		}
	}

	if fallback, err := m.cppDeclarationFallbackMatch(match); err == nil {
		display, displayErr := m.cppHandleDisplay(fallback.Path)
		if displayErr == nil {
			return ToolResult{
				Tool:    "cpp_declaration",
				Summary: "Resolved C++ declaration via semantic index fallback",
				Output:  fmt.Sprintf("%s:%d %s %s", display, fallback.Line, fallback.Kind, cppDisplayName(fallback.Qualified, fallback.Name)),
			}
		}
	}

	display, err := m.cppHandleDisplay(match.Path)
	if err != nil {
		return ToolResult{Tool: "cpp_declaration", Summary: "C++ declaration unavailable", Error: err.Error()}
	}

	return ToolResult{Tool: "cpp_declaration", Summary: "Resolved C++ declaration via semantic index fallback", Output: fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, cppDisplayName(match.Qualified, match.Name))}
}

func (m *Manager) cppImplementations(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "cpp_implementations", Summary: "C++ implementation query rejected", Error: err.Error()}
	}

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.cppImplementationsViaClangd(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "cpp_implementations",
				Summary: "Resolved C++ implementations via clangd",
				Output:  output,
			}
		}
	}

	if output, err := m.cppImplementationFallback(match, pathFilter); err == nil && strings.TrimSpace(output) != "" {
		return ToolResult{
			Tool:    "cpp_implementations",
			Summary: "Resolved C++ implementations via semantic fallback",
			Output:  output,
		}
	}

	return ToolResult{Tool: "cpp_implementations", Summary: "C++ implementations unavailable", Error: "clangd is required for implementation queries in this harness"}
}

func (m *Manager) cppReferences(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "cpp_references", Summary: "C++ reference query rejected", Error: err.Error()}
	}

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.cppReferencesViaClangd(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "cpp_references",
				Summary: "Resolved C++ references via clangd",
				Output:  output,
			}
		}
	}

	searchReq := ToolRequest{
		Pattern: cppReferencePattern(match.Name),
		Path:    defaultString(req.Path, "."),
		Glob:    cppSearchGlob(),
	}
	result := m.searchContent(ctx, searchReq, "cpp_references")

	if result.Error == "" {
		result.Summary = "Resolved C++ references via semantic/rg fallback"
	}

	return result
}

func (m *Manager) cppCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "cpp_calls_from", Summary: "C++ outgoing-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err == nil {
		if payload, lspErr := m.cppCallHierarchyViaClangd(ctx, match, pathFilter, false); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "cpp_calls_from",
				Summary:       "Resolved outgoing C++ calls via clangd call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_calls_from", Summary: "C++ outgoing-call query unavailable", Error: err.Error()}
	}

	matches := filterCppCallMatches(index.FindCallsFrom(match.Name, 24), m.cppPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "cpp_calls_from", Summary: fmt.Sprintf("No outgoing C++ calls matched %s", cppDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.cppHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, call.CalleeName))
	}

	return ToolResult{Tool: "cpp_calls_from", Summary: fmt.Sprintf("Found %d outgoing C++ call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) cppCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "cpp_calls_into", Summary: "C++ incoming-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err == nil {
		if payload, lspErr := m.cppCallHierarchyViaClangd(ctx, match, pathFilter, true); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "cpp_calls_into",
				Summary:       "Resolved incoming C++ calls via clangd call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_calls_into", Summary: "C++ incoming-call query unavailable", Error: err.Error()}
	}

	matches := filterCppCallMatches(index.FindCallsInto(match.Name, 24), m.cppPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "cpp_calls_into", Summary: fmt.Sprintf("No incoming C++ calls matched %s", cppDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.cppHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, cppDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "cpp_calls_into", Summary: fmt.Sprintf("Found %d incoming C++ call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) cppExtractBody(req ToolRequest) ToolResult {
	match, err := m.selectCppSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "cpp_extract_body", Summary: "C++ body extraction rejected", Error: err.Error()}
	}

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "cpp_extract_body", Summary: "Failed to resolve C++ symbol path", Error: err.Error()}
	}

	m.rememberPath(abs)

	start := match.StartLine
	end := match.EndLine

	if req.StartLine > 0 {
		start = req.StartLine
	}

	if req.EndLine > 0 {
		end = req.EndLine
	}

	body, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "cpp_extract_body", Summary: "Failed to extract C++ body", Error: err.Error()}
	}

	displayPath := m.displayPath(abs)

	return ToolResult{
		Tool:          "cpp_extract_body",
		Summary:       fmt.Sprintf("Extracted %s from %s lines %d-%d", cppDisplayName(match.Qualified, match.Name), displayPath, start, end),
		Output:        body,
		Paths:         []string{displayPath},
		SourceWindows: []SourceWindowPlan{{Path: displayPath, StartLine: start, EndLine: end}},
	}
}

func (m *Manager) cppIncludes(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_includes", Summary: "C++ include query rejected", Error: "pattern is required"}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_includes", Summary: "C++ include query unavailable", Error: err.Error()}
	}

	deps := filterRelatedPaths(index.Dependencies(pattern), m.cppPathFilter(req.Path))
	if len(deps) == 0 {
		return ToolResult{Tool: "cpp_includes", Summary: fmt.Sprintf("No C++ include dependencies matched %q", pattern)}
	}

	lines := make([]string, 0, len(deps))
	for _, rel := range deps {
		display, err := m.cppHandleDisplay(rel)
		if err != nil {
			continue
		}

		lines = append(lines, display)
	}

	return ToolResult{Tool: "cpp_includes", Summary: fmt.Sprintf("Found %d direct/transitive include dependency file(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) cppWhoIncludes(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_who_includes", Summary: "C++ reverse-include query rejected", Error: "pattern is required"}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_who_includes", Summary: "C++ reverse-include query unavailable", Error: err.Error()}
	}

	deps := filterRelatedPaths(index.WhoIncludes(pattern), m.cppPathFilter(req.Path))
	if len(deps) == 0 {
		return ToolResult{Tool: "cpp_who_includes", Summary: fmt.Sprintf("No C++ includers matched %q", pattern)}
	}

	lines := make([]string, 0, len(deps))
	for _, rel := range deps {
		display, err := m.cppHandleDisplay(rel)
		if err != nil {
			continue
		}

		lines = append(lines, display)
	}

	return ToolResult{Tool: "cpp_who_includes", Summary: fmt.Sprintf("Found %d direct/transitive includer file(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) cppBlastRadius(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_blast_radius", Summary: "C++ blast-radius query rejected", Error: "pattern is required"}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_blast_radius", Summary: "C++ blast-radius query unavailable", Error: err.Error()}
	}

	files := filterRelatedPaths(index.BlastRadius(pattern), m.cppPathFilter(req.Path))
	if len(files) == 0 {
		return ToolResult{Tool: "cpp_blast_radius", Summary: fmt.Sprintf("No C++ translation units were affected by %q", pattern)}
	}

	lines := make([]string, 0, len(files))
	for _, rel := range files {
		display, err := m.cppHandleDisplay(rel)
		if err != nil {
			continue
		}

		lines = append(lines, display)
	}

	return ToolResult{Tool: "cpp_blast_radius", Summary: fmt.Sprintf("Found %d affected C++ translation unit(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) cppDeadSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_dead_symbol", Summary: "C++ dead-symbol query rejected", Error: "pattern is required"}
	}

	refs := m.cppReferences(ctx, req)
	if refs.Error != "" {
		refs.Tool = "cpp_dead_symbol"
		refs.Summary = "C++ dead-symbol query failed"

		return refs
	}

	lines := nonEmptySearchLines(refs.Output)
	switch len(lines) {
	case 0:
		return ToolResult{Tool: "cpp_dead_symbol", Summary: fmt.Sprintf("No C++ references matched %q; the symbol is likely dead.", pattern)}
	case 1:
		return ToolResult{Tool: "cpp_dead_symbol", Summary: fmt.Sprintf("Only one C++ reference matched %q; inspect the definition before removing it.", pattern), Output: strings.Join(lines, "\n")}
	default:
		return ToolResult{Tool: "cpp_dead_symbol", Summary: fmt.Sprintf("Found %d C++ references to %q; the symbol is not dead.", len(lines), pattern), Output: strings.Join(lines, "\n")}
	}
}

func (m *Manager) cppFileOutline(req ToolRequest) ToolResult {
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "cpp_file_outline", Summary: "C++ file outline rejected", Error: err.Error()}
	}

	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{Tool: "cpp_file_outline", Summary: "C++ file outline unavailable", Error: err.Error()}
	}

	rel := filepath.ToSlash(m.displayPath(path))
	file := index.FileByPath(rel)

	if file == nil {
		return ToolResult{Tool: "cpp_file_outline", Summary: "C++ file outline unavailable", Error: fmt.Sprintf("no C/C++ file indexed at %s", rel)}
	}

	m.rememberPath(path)

	var b strings.Builder

	header := rel
	tags := make([]string, 0, 3)

	if file.IsHeader {
		tags = append(tags, "header")
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
	fmt.Fprintf(&b, "counts: funcs=%d methods=%d types=%d\n", file.Funcs, file.Methods, file.Types)

	if len(file.Includes) > 0 {
		b.WriteString("includes:\n")

		includes := append([]string(nil), file.Includes...)
		slices.Sort(includes)

		for _, inc := range includes {
			fmt.Fprintf(&b, "- %s\n", inc)
		}
	}

	if len(file.Symbols) == 0 {
		b.WriteString("declarations: (none indexed)\n")
	} else if len(file.Symbols) > 200 {
		fmt.Fprintf(&b, "declarations: omitted for large file (%d symbols).\n", len(file.Symbols))
	} else {
		symbols := append([]semanticcpp.CppSymbol(nil), file.Symbols...)
		sort.SliceStable(symbols, func(i, j int) bool {
			return symbols[i].Line < symbols[j].Line
		})
		b.WriteString("declarations:\n")

		for _, sym := range symbols {
			name := cppDisplayName(sym.Qualified, sym.Name)
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
		Tool:    "cpp_file_outline",
		Summary: summary,
		Output:  strings.TrimSpace(b.String()),
	}
}

func (m *Manager) cppDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	clangd := m.clangdBinary()
	if clangd == "" {
		return ToolResult{Tool: "cpp_diagnostics", Summary: "C++ diagnostics unavailable", Error: "clangd is not installed"}
	}

	compileDir := m.compileCommandsDir()
	if compileDir == "" {
		return ToolResult{Tool: "cpp_diagnostics", Summary: "C++ diagnostics unavailable", Error: "compile_commands.json was not found under the workspace"}
	}

	files, err := m.cppDiagnosticTargets(req)
	if err != nil {
		return ToolResult{Tool: "cpp_diagnostics", Summary: "C++ diagnostics unavailable", Error: err.Error()}
	}

	if len(files) == 0 {
		return ToolResult{Tool: "cpp_diagnostics", Summary: "No C++ files available for diagnostics"}
	}

	lines := make([]string, 0, len(files))
	for _, rel := range files {
		output, err := m.runClangdCheck(ctx, compileDir, rel)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: %s", rel, err.Error()))
			if strings.TrimSpace(output) != "" {
				lines = append(lines, strings.TrimSpace(output))
			}

			continue
		}

		output = strings.TrimSpace(output)
		if output == "" {
			lines = append(lines, fmt.Sprintf("%s: no warnings or errors", rel))
			continue
		}

		lines = append(lines, output)
	}

	return ToolResult{Tool: "cpp_diagnostics", Summary: fmt.Sprintf("Collected C++ diagnostics for %d file(s).", len(files)), Output: strings.Join(lines, "\n"), Facts: diagnosticToolFacts(files)}
}

func (m *Manager) selectCppSymbolMatch(req ToolRequest) (semanticcpp.CppSymbolMatch, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return semanticcpp.CppSymbolMatch{}, fmt.Errorf("pattern is required")
	}

	index, err := m.getCppIndex()
	if err != nil {
		return semanticcpp.CppSymbolMatch{}, err
	}

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err != nil {
		return semanticcpp.CppSymbolMatch{}, err
	}

	matches := filterCppSymbolMatches(index.FindSymbols(pattern, 12), pathFilter)
	if len(matches) == 0 {
		if pathFilter != "" {
			return semanticcpp.CppSymbolMatch{}, fmt.Errorf("no C++ symbols matched %q under %s", pattern, pathFilter)
		}

		return semanticcpp.CppSymbolMatch{}, fmt.Errorf("no C++ symbols matched %q", pattern)
	}

	return matches[0], nil
}

func (m *Manager) cppPathFilter(path string) string {
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

func (m *Manager) cppDeclarationFallbackMatch(match semanticcpp.CppSymbolMatch) (semanticcpp.CppSymbolMatch, error) {
	index, err := m.getCppIndex()
	if err != nil {
		return semanticcpp.CppSymbolMatch{}, err
	}

	patterns := []string{match.Qualified, match.Name}
	seen := make(map[string]struct{})
	candidates := make([]semanticcpp.CppSymbolMatch, 0, 8)

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}

		for _, candidate := range index.FindSymbols(pattern, 24) {
			key := candidate.Path + ":" + strconv.Itoa(candidate.Line) + ":" + candidate.Name
			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}

			if candidate.Name != match.Name {
				continue
			}

			candidates = append(candidates, candidate)
		}
	}

	if len(candidates) == 0 {
		return semanticcpp.CppSymbolMatch{}, fmt.Errorf("no declaration fallback candidates found")
	}

	sort.Slice(candidates, func(i, j int) bool {
		iHeader := cppIsHeaderPath(candidates[i].Path)
		jHeader := cppIsHeaderPath(candidates[j].Path)

		if iHeader != jHeader {
			return iHeader
		}

		iSamePath := candidates[i].Path == match.Path
		jSamePath := candidates[j].Path == match.Path

		if iSamePath != jSamePath {
			return !iSamePath
		}

		if candidates[i].Path != candidates[j].Path {
			return candidates[i].Path < candidates[j].Path
		}

		return candidates[i].Line < candidates[j].Line
	})

	return candidates[0], nil
}

func (m *Manager) cppDefinitionFallbackMatch(match semanticcpp.CppSymbolMatch) (semanticcpp.CppSymbolMatch, error) {
	index, err := m.getCppIndex()
	if err != nil {
		return semanticcpp.CppSymbolMatch{}, err
	}

	patterns := []string{match.Qualified, match.Name}
	seen := make(map[string]struct{})
	candidates := make([]semanticcpp.CppSymbolMatch, 0, 8)

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}

		for _, candidate := range index.FindSymbols(pattern, 32) {
			key := candidate.Path + ":" + strconv.Itoa(candidate.Line) + ":" + candidate.Name
			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}

			if candidate.Name != match.Name {
				continue
			}

			candidates = append(candidates, candidate)
		}
	}

	if len(candidates) == 0 {
		return semanticcpp.CppSymbolMatch{}, fmt.Errorf("no definition fallback candidates found")
	}

	wantQualified := strings.TrimSpace(match.Qualified)

	sort.Slice(candidates, func(i, j int) bool {
		iHeader := cppIsHeaderPath(candidates[i].Path)
		jHeader := cppIsHeaderPath(candidates[j].Path)

		if iHeader != jHeader {
			return !iHeader
		}

		iExact := wantQualified != "" && strings.TrimSpace(candidates[i].Qualified) == wantQualified
		jExact := wantQualified != "" && strings.TrimSpace(candidates[j].Qualified) == wantQualified

		if iExact != jExact {
			return iExact
		}

		if candidates[i].Path != candidates[j].Path {
			return candidates[i].Path < candidates[j].Path
		}

		return candidates[i].Line < candidates[j].Line
	})

	return candidates[0], nil
}

func (m *Manager) cppImplementationFallback(match semanticcpp.CppSymbolMatch, pathFilter string) (string, error) {
	index, err := m.getCppIndex()
	if err != nil {
		return "", err
	}

	candidates := filterCppSymbolMatches(index.FindSymbols(match.Name, 64), pathFilter)

	type impl struct {
		match semanticcpp.CppSymbolMatch
	}

	impls := make([]impl, 0, len(candidates))
	seen := make(map[string]struct{})

	for _, candidate := range candidates {
		if candidate.Name != match.Name {
			continue
		}

		if candidate.Kind != "method" && candidate.Kind != "func" && candidate.Kind != "function" {
			continue
		}

		if candidate.Path == match.Path && candidate.Line == match.Line {
			continue
		}

		if strings.TrimSpace(candidate.Qualified) == strings.TrimSpace(match.Qualified) {
			continue
		}

		key := candidate.Path + ":" + strconv.Itoa(candidate.Line) + ":" + candidate.Name
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		impls = append(impls, impl{match: candidate})
	}

	if len(impls) == 0 {
		return "", fmt.Errorf("no semantic implementation candidates found")
	}

	sort.Slice(impls, func(i, j int) bool {
		iHeader := cppIsHeaderPath(impls[i].match.Path)
		jHeader := cppIsHeaderPath(impls[j].match.Path)

		if iHeader != jHeader {
			return !iHeader
		}

		if impls[i].match.Path != impls[j].match.Path {
			return impls[i].match.Path < impls[j].match.Path
		}

		return impls[i].match.Line < impls[j].match.Line
	})

	lines := make([]string, 0, len(impls))
	for _, candidate := range impls {
		display, err := m.cppHandleDisplay(candidate.match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, candidate.match.Line, candidate.match.Kind, cppDisplayName(candidate.match.Qualified, candidate.match.Name)))
		if len(lines) >= m.maxDirectoryEntries() {
			break
		}
	}

	return strings.Join(lines, "\n"), nil
}

func (m *Manager) cppPathFilterForRequest(req ToolRequest) (string, error) {
	return m.languagePathFilterForRequest(req, m.cppPathFilter)
}

func (m *Manager) cppHandleDisplay(rel string) (string, error) {
	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}

	return m.handleDisplay(abs), nil
}

func filterCppSymbolMatches(matches []semanticcpp.CppSymbolMatch, pathFilter string) []semanticcpp.CppSymbolMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semanticcpp.CppSymbolMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func filterCppCallMatches(matches []semanticcpp.CppCallMatch, pathFilter string) []semanticcpp.CppCallMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semanticcpp.CppCallMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Caller.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func cppReferencePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return pattern
	}

	if regexpSafeIdentifier(pattern) {
		return `\b` + pattern + `\b`
	}

	return regexpQuoteMeta(pattern)
}

func cppSearchGlob() string {
	return "*.{h,hpp,hh,hxx,ipp,inl,tpp,c,cc,cpp,cxx}"
}

func regexpSafeIdentifier(pattern string) bool {
	for _, r := range pattern {
		if !(r == '_' || r == ':' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}

	return true
}

func regexpQuoteMeta(pattern string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`.`, `\.`,
		`+`, `\+`,
		`*`, `\*`,
		`?`, `\?`,
		`(`, `\(`,
		`)`, `\)`,
		`[`, `\[`,
		`]`, `\]`,
		`{`, `\{`,
		`}`, `\}`,
		`^`, `\^`,
		`$`, `\$`,
		`|`, `\|`,
	)

	return replacer.Replace(pattern)
}

func nonEmptySearchLines(output string) []string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "context:") || strings.HasPrefix(line, "targeted edit template:") {
			continue
		}

		out = append(out, line)
	}

	return out
}

func (m *Manager) clangdBinary() string {
	return hosttools.FirstAvailable("clangd")
}

func (m *Manager) compileCommandsDir() string {
	candidates := []string{
		m.Root(),
		filepath.Join(m.Root(), "build"),
		filepath.Join(m.Root(), "out"),
		filepath.Join(m.Root(), "native"),
		filepath.Join(m.Root(), "native", "build"),
	}
	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, "compile_commands.json")); err == nil && !info.IsDir() {
			return dir
		}
	}

	return ""
}

func (m *Manager) runClangdCheck(ctx context.Context, compileDir string, rel string) (string, error) {
	result := m.runCommandResult(ctx, m.Root(), nil, "clangd", "--compile-commands-dir="+compileDir, "--check="+rel, "--log=error")

	return result.Output, result.Err
}

func (m *Manager) cppDiagnosticTargets(req ToolRequest) ([]string, error) {
	index, err := m.getCppIndex()
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
			if !isCppExtension(filepath.Ext(rel)) {
				return nil, fmt.Errorf("%s is not a C/C++ file", rel)
			}

			return []string{rel}, nil
		}

		filter := filepath.ToSlash(m.displayPath(resolved))
		files := make([]string, 0)

		for _, file := range index.Files {
			if pathMatchesFilter(file.Path, filter) && !file.IsHeader {
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
		if file != nil && file.IsHeader {
			continue
		}

		out = append(out, path)
	}

	return out, nil
}

func isCppExtension(ext string) bool {
	_, ok := map[string]struct{}{
		".c": {}, ".cc": {}, ".cpp": {}, ".cxx": {},
		".h": {}, ".hh": {}, ".hpp": {}, ".hxx": {}, ".ipp": {}, ".inl": {}, ".tpp": {},
	}[strings.ToLower(ext)]

	return ok
}

func cppIsHeaderPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp":
		return true
	default:
		return false
	}
}

func cppLocationOutputOnlyHeaders(output string) bool {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	seen := false

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		for _, loc := range fields[1:] {
			idx := strings.LastIndex(loc, ":")
			if idx <= 0 {
				continue
			}

			seen = true

			if !cppIsHeaderPath(loc[:idx]) {
				return false
			}

			break
		}
	}

	return seen
}

func cppDisplayName(qualified string, name string) string {
	qualified = strings.TrimSpace(qualified)
	if qualified != "" {
		return qualified
	}

	return strings.TrimSpace(name)
}
