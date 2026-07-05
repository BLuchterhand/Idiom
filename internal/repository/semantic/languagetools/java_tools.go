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
	"idiom/internal/repository/semantic"
	semanticjava "idiom/internal/repository/semantic/java"
)

// -- Detection --------------------------------------------------------------

func (m *Manager) isJavaWorkspace() bool {
	if !m.Enabled() {
		return false
	}

	// Manifest-based detection comes first — it's cheap and authoritative.
	for _, manifest := range []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"} {
		if info, err := os.Stat(filepath.Join(m.Root(), manifest)); err == nil && !info.IsDir() {
			return true
		}
	}

	// Fall back to a shallow walk for stray `.java` files.
	found := false

	_ = filepath.WalkDir(m.Root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			if discovery.ShouldSkipDirName(name) && path != m.Root() {
				return filepath.SkipDir
			}

			// Java-specific skips — jdtls stores large caches in these.
			switch name {
			case "target", "build", ".gradle", ".mvn":
				if path != m.Root() {
					return filepath.SkipDir
				}
			}

			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		if strings.EqualFold(filepath.Ext(path), ".java") {
			found = true
			return filepath.SkipAll
		}

		return nil
	})

	return found
}

func (m *Manager) getJavaIndex() (*semantic.JavaIndex, error) {
	if !m.isJavaWorkspace() {
		return nil, fmt.Errorf("java-specific tools require a workspace with Java source files or a pom.xml / build.gradle manifest")
	}

	token := m.currentGoIndexToken()
	m.mu.Lock()
	cached := m.javaIndex
	cachedToken := m.javaIndexToken
	m.mu.Unlock()

	if cached != nil && (token == "" || token == cachedToken) {
		return cached, nil
	}

	index, err := semanticjava.BuildJavaIndex(m.Root())
	if err != nil {
		return nil, fmt.Errorf("build Java semantic index: %w", err)
	}

	if index == nil {
		return nil, fmt.Errorf("no Java files were indexed under the workspace")
	}

	m.mu.Lock()
	m.javaIndex = index
	m.javaIndexToken = token
	m.mu.Unlock()

	return index, nil
}

// -- Helpers ----------------------------------------------------------------

func (m *Manager) javaPathFilter(path string) string {
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

func (m *Manager) javaPathFilterForRequest(req ToolRequest) (string, error) {
	return m.languagePathFilterForRequest(req, m.javaPathFilter)
}

func (m *Manager) javaHandleDisplay(rel string) (string, error) {
	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}

	return m.handleDisplay(abs), nil
}

func javaDisplayName(qualified string, name string) string {
	qualified = strings.TrimSpace(qualified)
	if qualified != "" {
		return qualified
	}

	return strings.TrimSpace(name)
}

func filterJavaSymbolMatches(matches []semantic.JavaSymbolMatch, pathFilter string) []semantic.JavaSymbolMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semantic.JavaSymbolMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func filterJavaCallMatches(matches []semantic.JavaCallMatch, pathFilter string) []semantic.JavaCallMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semantic.JavaCallMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Caller.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func (m *Manager) selectJavaSymbolMatch(req ToolRequest) (semantic.JavaSymbolMatch, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return semantic.JavaSymbolMatch{}, fmt.Errorf("pattern is required")
	}

	index, err := m.getJavaIndex()
	if err != nil {
		return semantic.JavaSymbolMatch{}, err
	}

	pathFilter, err := m.javaPathFilterForRequest(req)
	if err != nil {
		return semantic.JavaSymbolMatch{}, err
	}

	matches := filterJavaSymbolMatches(index.FindSymbols(pattern, 12), pathFilter)
	if len(matches) == 0 {
		if pathFilter != "" {
			return semantic.JavaSymbolMatch{}, fmt.Errorf("no Java symbols matched %q under %s", pattern, pathFilter)
		}

		return semantic.JavaSymbolMatch{}, fmt.Errorf("no Java symbols matched %q", pattern)
	}

	return matches[0], nil
}

func javaReferencePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return pattern
	}

	if regexpSafeIdentifier(pattern) {
		return `\b` + pattern + `\b`
	}

	return regexpQuoteMeta(pattern)
}

func javaSearchGlob() string {
	return "*.java"
}

// -- Tools: overview / entrypoints -----------------------------------------

func (m *Manager) javaOverview() ToolResult {
	index, err := m.getJavaIndex()
	if err != nil {
		return ToolResult{Tool: "java_overview", Summary: "Java overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)

	summary := index.Summary()
	if summary == "" {
		summary = "Java semantic index is available, but no summary could be generated."
	}

	return ToolResult{Tool: "java_overview", Summary: "Built Java semantic overview for the workspace", Output: summary}
}

func (m *Manager) javaEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getJavaIndex()
	if err != nil {
		return ToolResult{Tool: "java_entrypoints", Summary: "Java entrypoints unavailable", Error: err.Error()}
	}

	paths := index.Entrypoints(goResultLimit(req.MaxDepth, 8))
	if len(paths) == 0 {
		return ToolResult{Tool: "java_entrypoints", Summary: "No Java entrypoint files found"}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		file := index.FileByPath(rel)

		display, err := m.javaHandleDisplay(rel)
		if err != nil {
			continue
		}

		line := display

		if file != nil {
			tags := make([]string, 0, 3)
			if file.HasMain {
				tags = append(tags, "main")
			}

			if file.IsTest {
				tags = append(tags, "test")
			}

			if len(tags) > 0 {
				line += " (" + strings.Join(tags, ", ") + ")"
			}

			line += fmt.Sprintf(" [methods=%d, types=%d]", file.Methods, file.Classes+file.Interfaces+file.Enums)
		}

		lines = append(lines, line)
	}

	return ToolResult{Tool: "java_entrypoints", Summary: fmt.Sprintf("Found %d likely Java anchor file(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

// -- Tools: find / read symbol ---------------------------------------------

func (m *Manager) javaFindSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "java_find_symbol", Summary: "Java symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getJavaIndex()
	if err != nil {
		return ToolResult{Tool: "java_find_symbol", Summary: "Java symbol search unavailable", Error: err.Error()}
	}

	matches := filterJavaSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.javaPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "java_find_symbol", Summary: fmt.Sprintf("No Java symbols matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.javaHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, javaDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "java_find_symbol", Summary: fmt.Sprintf("Found %d matching Java symbol(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) javaReadSymbol(req ToolRequest) ToolResult {
	if err := validateSymbolPattern("java_read_symbol", req.Pattern); err != nil {
		return ToolResult{Tool: "java_read_symbol", Summary: "Java symbol read rejected", Error: err.Error()}
	}

	match, err := m.selectJavaSymbolMatch(req)
	if err != nil {
		if result, ok := m.javaReadSymbolPathMiss(req); ok {
			return result
		}

		return ToolResult{Tool: "java_read_symbol", Summary: "Java symbol read rejected", Error: err.Error()}
	}

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "java_read_symbol", Summary: "Failed to resolve Java symbol path", Error: err.Error()}
	}

	m.rememberPath(abs)

	start := req.StartLine
	end := req.EndLine

	if start <= 0 || end <= 0 || start > end {
		start = match.StartLine
		end = match.EndLine

		if start <= 0 {
			start = match.Line - 2
		}

		if start < 1 {
			start = 1
		}

		if end < start {
			end = start + 32
		}
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "java_read_symbol", Summary: "Failed to read Java symbol snippet", Error: err.Error()}
	}

	return ToolResult{Tool: "java_read_symbol", Summary: fmt.Sprintf("Read %s in %s lines %d-%d", javaDisplayName(match.Qualified, match.Name), m.displayPath(abs), start, end), Output: snippet}
}

// -- Tools: related / workspace symbol -------------------------------------

func (m *Manager) javaRelatedFiles(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "java_related_files", Summary: "Java related-file search rejected", Error: "pattern is required"}
	}

	index, err := m.getJavaIndex()
	if err != nil {
		return ToolResult{Tool: "java_related_files", Summary: "Java related-file search unavailable", Error: err.Error()}
	}

	paths := filterRelatedPaths(index.RelevantFiles(strings.Fields(pattern), goResultLimit(req.MaxDepth, 8)), m.javaPathFilter(req.Path))

	return m.relatedFilesResult("java_related_files", "Java", pattern, paths, m.javaHandleDisplay)
}

func (m *Manager) javaWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "java_workspace_symbol", Summary: "Java workspace-symbol query rejected", Error: "pattern is required"}
	}

	if m.jdtlsBinary() != "" {
		pathFilter, err := m.javaPathFilterForRequest(req)
		if err == nil {
			if output, lspErr := m.javaWorkspaceSymbolsViaJdtls(ctx, pattern, pathFilter, goResultLimit(req.MaxDepth, 8)); lspErr == nil && strings.TrimSpace(output) != "" {
				return ToolResult{
					Tool:    "java_workspace_symbol",
					Summary: "Resolved Java workspace symbols via jdtls",
					Output:  output,
				}
			}
		}
	}

	result := m.javaFindSymbol(req)

	result.Tool = "java_workspace_symbol"

	if result.Error == "" && strings.HasPrefix(result.Summary, "Found ") {
		result.Summary = strings.Replace(result.Summary, "Java symbol", "Java workspace symbol", 1)
	}

	return result
}

// -- Tools: definition / references / implementations / calls --------------

func (m *Manager) javaDefinition(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectJavaSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "java_definition", Summary: "Java definition query rejected", Error: err.Error()}
	}

	if m.jdtlsBinary() != "" {
		if output, lspErr := m.javaDefinitionViaJdtls(ctx, match); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "java_definition",
				Summary: "Resolved Java definition via jdtls",
				Output:  output,
			}
		}
	}

	display, err := m.javaHandleDisplay(match.Path)
	if err != nil {
		return ToolResult{Tool: "java_definition", Summary: "Java definition unavailable", Error: err.Error()}
	}

	return ToolResult{Tool: "java_definition", Summary: "Resolved Java definition via semantic index", Output: fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, javaDisplayName(match.Qualified, match.Name))}
}

func (m *Manager) javaReferences(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectJavaSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "java_references", Summary: "Java reference query rejected", Error: err.Error()}
	}

	pathFilter, err := m.javaPathFilterForRequest(req)
	if err == nil && m.jdtlsBinary() != "" {
		if output, lspErr := m.javaReferencesViaJdtls(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "java_references",
				Summary: "Resolved Java references via jdtls",
				Output:  output,
			}
		}
	}

	searchReq := ToolRequest{
		Pattern: javaReferencePattern(match.Name),
		Path:    defaultString(req.Path, "."),
		Glob:    javaSearchGlob(),
	}
	result := m.searchContent(ctx, searchReq, "java_references")

	if result.Error == "" {
		result.Summary = "Resolved Java references via rg fallback"
	}

	return result
}

func (m *Manager) javaImplementations(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectJavaSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "java_implementations", Summary: "Java implementation query rejected", Error: err.Error()}
	}

	pathFilter, err := m.javaPathFilterForRequest(req)
	if err == nil && m.jdtlsBinary() != "" {
		if output, lspErr := m.javaImplementationsViaJdtls(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{
				Tool:    "java_implementations",
				Summary: "Resolved Java implementations via jdtls",
				Output:  output,
			}
		}
	}

	index, indexErr := m.getJavaIndex()
	if indexErr != nil {
		return ToolResult{Tool: "java_implementations", Summary: "Java implementation query unavailable", Error: indexErr.Error()}
	}

	impls := filterJavaSymbolMatches(index.Implementations(match.Name, 32), pathFilter)
	if len(impls) == 0 {
		return ToolResult{Tool: "java_implementations", Summary: fmt.Sprintf("No Java implementations found for %s", javaDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(impls))
	for _, impl := range impls {
		display, err := m.javaHandleDisplay(impl.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, impl.Line, impl.Kind, javaDisplayName(impl.Qualified, impl.Name)))
	}

	return ToolResult{
		Tool:    "java_implementations",
		Summary: fmt.Sprintf("Resolved %d Java implementation(s) via semantic fallback", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) javaCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectJavaSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "java_calls_from", Summary: "Java outgoing-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.javaPathFilterForRequest(req)
	if err == nil && m.jdtlsBinary() != "" {
		if payload, lspErr := m.javaCallHierarchyViaJdtls(ctx, match, pathFilter, false); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "java_calls_from",
				Summary:       "Resolved outgoing Java calls via jdtls call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, indexErr := m.getJavaIndex()
	if indexErr != nil {
		return ToolResult{Tool: "java_calls_from", Summary: "Java outgoing-call query unavailable", Error: indexErr.Error()}
	}

	matches := filterJavaCallMatches(index.FindCallsFrom(match.Name, 24), m.javaPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "java_calls_from", Summary: fmt.Sprintf("No outgoing Java calls matched %s", javaDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.javaHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, call.CalleeName))
	}

	return ToolResult{Tool: "java_calls_from", Summary: fmt.Sprintf("Found %d outgoing Java call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) javaCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectJavaSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "java_calls_into", Summary: "Java incoming-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.javaPathFilterForRequest(req)
	if err == nil && m.jdtlsBinary() != "" {
		if payload, lspErr := m.javaCallHierarchyViaJdtls(ctx, match, pathFilter, true); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{
				Tool:          "java_calls_into",
				Summary:       "Resolved incoming Java calls via jdtls call hierarchy",
				Output:        payload.Output,
				Relationships: payload.Relationships,
			}
		}
	}

	index, indexErr := m.getJavaIndex()
	if indexErr != nil {
		return ToolResult{Tool: "java_calls_into", Summary: "Java incoming-call query unavailable", Error: indexErr.Error()}
	}

	matches := filterJavaCallMatches(index.FindCallsInto(match.Name, 24), m.javaPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "java_calls_into", Summary: fmt.Sprintf("No incoming Java calls matched %s", javaDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.javaHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, javaDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "java_calls_into", Summary: fmt.Sprintf("Found %d incoming Java call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

// -- Tools: file outline ---------------------------------------------------

func (m *Manager) javaFileOutline(req ToolRequest) ToolResult {
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "java_file_outline", Summary: "Java file outline rejected", Error: err.Error()}
	}

	index, err := m.getJavaIndex()
	if err != nil {
		return ToolResult{Tool: "java_file_outline", Summary: "Java file outline unavailable", Error: err.Error()}
	}

	rel := filepath.ToSlash(m.displayPath(path))
	file := index.FileByPath(rel)

	if file == nil {
		return ToolResult{Tool: "java_file_outline", Summary: "Java file outline unavailable", Error: fmt.Sprintf("no Java file indexed at %s", rel)}
	}

	m.rememberPath(path)

	var b strings.Builder

	header := rel
	tags := make([]string, 0, 3)

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

	if file.Package != "" {
		fmt.Fprintf(&b, "package: %s\n", file.Package)
	}

	fmt.Fprintf(&b, "counts: classes=%d interfaces=%d enums=%d methods=%d fields=%d\n",
		file.Classes, file.Interfaces, file.Enums, file.Methods, file.Fields)

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
		symbols := append([]semantic.JavaSymbol(nil), file.Symbols...)
		slices.SortStableFunc(symbols, func(a, b semantic.JavaSymbol) int { return cmp.Compare(a.Line, b.Line) })
		b.WriteString("declarations:\n")

		for _, sym := range symbols {
			// Indent by scope depth: top-level = "-", nested = "  -", etc.
			depth := 0
			if strings.TrimSpace(sym.Scope) != "" {
				depth = strings.Count(sym.Scope, ".") + 1
			}

			indent := strings.Repeat("  ", depth)
			name := javaDisplayName(sym.Qualified, sym.Name)
			start := sym.StartLine

			if start <= 0 {
				start = sym.Line
			}

			end := sym.EndLine
			if end < start {
				end = start
			}

			line := fmt.Sprintf("%s- %s (%s) lines %d-%d", indent, name, sym.Kind, start, end)
			if len(sym.Annotations) > 0 {
				line += " @" + strings.Join(sym.Annotations, " @")
			}

			if sym.Extends != "" {
				line += " extends:" + sym.Extends
			}

			if len(sym.Implements) > 0 {
				line += " implements:" + strings.Join(sym.Implements, ",")
			}

			b.WriteString(line + "\n")
		}
	}

	summary := fmt.Sprintf("Outlined %d symbol(s) in %s. Read-symbol capability is available for a specific body.", len(file.Symbols), rel)

	return ToolResult{
		Tool:    "java_file_outline",
		Summary: summary,
		Output:  strings.TrimSpace(b.String()),
	}
}

// -- Tools: diagnostics -----------------------------------------------------

// javaDiagnostics runs a pragmatic diagnostics flow:
//   - If mvn or gradle is available *and* the project uses that build
//     system, invoke a compile-only goal and extract errors/warnings.
//   - Otherwise, invoke javac on the requested file(s) with -Xlint:all.
//   - If the workspace has no build system configured, fall back to
//     javac on a single file and report the result verbatim.
//
// jdtls-driven diagnostics (publishDiagnostics) are intentionally not
// wired in here — a full LSP diagnostics pull requires tracking push
// notifications over the client lifetime, which doesn't fit the single-
// request harness. That's a known LSP-depth gap documented in
// docs/java-tooling.md.
func (m *Manager) javaDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	files, err := m.javaDiagnosticTargets(req)
	if err != nil {
		return ToolResult{Tool: "java_diagnostics", Summary: "Java diagnostics unavailable", Error: err.Error()}
	}

	if len(files) == 0 {
		return ToolResult{Tool: "java_diagnostics", Summary: "No Java files available for diagnostics"}
	}

	if _, err := os.Stat(filepath.Join(m.Root(), "pom.xml")); err == nil {
		stdout, stderr, runErr := m.runCommandDetailed(ctx, m.Root(), "mvn", "-q", "-o", "compile", "-DskipTests")
		output := strings.TrimSpace(strings.TrimSpace(stdout) + "\n" + strings.TrimSpace(stderr))

		if runErr != nil && output == "" {
			output = runErr.Error()
		}

		if output == "" {
			output = "mvn compile: no warnings or errors"
		}

		return ToolResult{Tool: "java_diagnostics", Summary: "Collected Java diagnostics via mvn compile", Output: output}
	}

	if _, err := os.Stat(filepath.Join(m.Root(), "build.gradle")); err == nil {
		stdout, stderr, runErr := m.runCommandDetailed(ctx, m.Root(), "gradle", "--offline", "--quiet", "compileJava")
		output := strings.TrimSpace(strings.TrimSpace(stdout) + "\n" + strings.TrimSpace(stderr))

		if runErr != nil && output == "" {
			output = runErr.Error()
		}

		if output == "" {
			output = "gradle compileJava: no warnings or errors"
		}

		return ToolResult{Tool: "java_diagnostics", Summary: "Collected Java diagnostics via gradle compileJava", Output: output}
	}

	// javac fallback — one file at a time.
	lines := make([]string, 0, len(files))
	for _, rel := range files {
		abs, err := m.resolvePath(rel, "")
		if err != nil {
			continue
		}

		stdout, stderr, runErr := m.runCommandDetailed(ctx, m.Root(), "javac", "-Xlint:all", "-d", os.TempDir(), abs)
		chunk := strings.TrimSpace(strings.TrimSpace(stdout) + "\n" + strings.TrimSpace(stderr))

		if chunk == "" {
			if runErr != nil {
				chunk = fmt.Sprintf("%s: %s", rel, runErr.Error())
			} else {
				chunk = fmt.Sprintf("%s: no warnings or errors", rel)
			}
		}

		lines = append(lines, chunk)
	}

	return ToolResult{Tool: "java_diagnostics", Summary: fmt.Sprintf("Collected Java diagnostics via javac for %d file(s).", len(files)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) javaDiagnosticTargets(req ToolRequest) ([]string, error) {
	index, err := m.getJavaIndex()
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(req.Path) != "" {
		resolved, err := m.resolvePath(req.Path, "")
		if err != nil {
			return nil, err
		}

		info, err := os.Stat(resolved)
		if err != nil {
			return nil, err
		}

		if !info.IsDir() {
			rel := filepath.ToSlash(m.displayPath(resolved))
			if strings.ToLower(filepath.Ext(rel)) != ".java" {
				return nil, fmt.Errorf("%s is not a Java file", rel)
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

	files := index.DefaultFiles(goResultLimit(req.MaxDepth, 4))

	return files, nil
}
