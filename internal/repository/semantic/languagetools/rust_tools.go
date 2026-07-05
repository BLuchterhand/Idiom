package languagetools

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic"
	semanticrust "idiom/internal/repository/semantic/rust"
)

// -- Detection --------------------------------------------------------------

func (m *Manager) isRustWorkspace() bool {
	if !m.Enabled() {
		return false
	}

	if info, err := os.Stat(filepath.Join(m.Root(), "Cargo.toml")); err == nil && !info.IsDir() {
		return true
	}

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

			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		if strings.EqualFold(filepath.Ext(path), ".rs") {
			found = true
			return filepath.SkipAll
		}

		return nil
	})

	return found
}

func (m *Manager) getRustIndex() (*semantic.RustIndex, error) {
	if !m.isRustWorkspace() {
		return nil, fmt.Errorf("rust-specific tools require a workspace with Rust source files or Cargo.toml")
	}

	token := m.currentGoIndexToken()
	m.mu.Lock()
	cached := m.rustIndex
	cachedToken := m.rustIndexToken
	m.mu.Unlock()

	if cached != nil && (token == "" || token == cachedToken) {
		return cached, nil
	}

	index, err := semanticrust.BuildRustIndex(m.Root())
	if err != nil {
		return nil, fmt.Errorf("build Rust semantic index: %w", err)
	}

	if index == nil {
		return nil, fmt.Errorf("no Rust files were indexed under the workspace")
	}

	m.mu.Lock()
	m.rustIndex = index
	m.rustIndexToken = token
	m.mu.Unlock()

	return index, nil
}

// -- Tool: rust_overview ----------------------------------------------------

func (m *Manager) rustOverview() ToolResult {
	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_overview", Summary: "Rust overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)

	summary := index.Summary()
	if summary == "" {
		summary = "Rust semantic index is available, but no summary could be generated."
	}

	return ToolResult{Tool: "rust_overview", Summary: "Built Rust semantic overview for the workspace", Output: summary}
}

// -- Tool: rust_entrypoints -------------------------------------------------

func (m *Manager) rustEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_entrypoints", Summary: "Rust entrypoints unavailable", Error: err.Error()}
	}

	paths := index.EntrypointFiles(goResultLimit(req.MaxDepth, 6))
	if len(paths) == 0 {
		return ToolResult{Tool: "rust_entrypoints", Summary: "No Rust entrypoint files found"}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		file := index.FileByPath(rel)

		display, err := m.rustHandleDisplay(rel)
		if err != nil {
			continue
		}

		line := display

		if file != nil {
			tags := make([]string, 0, 3)
			if file.HasMain || file.IsBinRoot {
				tags = append(tags, "main")
			}

			if file.IsLibRoot {
				tags = append(tags, "lib-root")
			}

			if file.HasTest {
				tags = append(tags, "#[test]")
			}

			if file.HasBench {
				tags = append(tags, "#[bench]")
			}

			if len(tags) > 0 {
				line += " [" + strings.Join(tags, ",") + "]"
			}

			line += fmt.Sprintf(" (fns=%d, methods=%d, types=%d, traits=%d, impls=%d)", file.Funcs, file.Methods, file.Types, file.Traits, file.Impls)
		}

		lines = append(lines, line)
	}

	return ToolResult{Tool: "rust_entrypoints", Summary: fmt.Sprintf("Found %d likely Rust anchor file(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

// -- Tool: rust_find_symbol -------------------------------------------------

func (m *Manager) rustFindSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "rust_find_symbol", Summary: "Rust symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_find_symbol", Summary: "Rust symbol search unavailable", Error: err.Error()}
	}

	matches := filterRustSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.rustPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "rust_find_symbol", Summary: fmt.Sprintf("No Rust symbols matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.rustHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, rustDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "rust_find_symbol", Summary: fmt.Sprintf("Found %d matching Rust symbol(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

// -- Tool: rust_workspace_symbol --------------------------------------------

func (m *Manager) rustWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "rust_workspace_symbol", Summary: "Rust workspace-symbol query rejected", Error: "pattern is required"}
	}

	pathFilter, err := m.rustPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.rustWorkspaceSymbolsViaRustAnalyzer(ctx, pattern, pathFilter, goResultLimit(req.MaxDepth, 8)); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{Tool: "rust_workspace_symbol", Summary: "Resolved Rust workspace symbols via rust-analyzer", Output: output}
		}
	}

	result := m.rustFindSymbol(req)

	result.Tool = "rust_workspace_symbol"

	if result.Error == "" && strings.HasPrefix(result.Summary, "Found ") {
		result.Summary = strings.Replace(result.Summary, "Rust symbol", "Rust workspace symbol", 1)
	}

	return result
}

// -- Tool: rust_read_symbol -------------------------------------------------

func (m *Manager) rustReadSymbol(req ToolRequest) ToolResult {
	if err := validateSymbolPattern("rust_read_symbol", req.Pattern); err != nil {
		return ToolResult{Tool: "rust_read_symbol", Summary: "Rust symbol read rejected", Error: err.Error()}
	}

	match, err := m.selectRustSymbolMatch(req)
	if err != nil {
		if result, ok := m.rustReadSymbolPathMiss(req); ok {
			return result
		}

		return ToolResult{Tool: "rust_read_symbol", Summary: "Rust symbol read rejected", Error: err.Error()}
	}

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "rust_read_symbol", Summary: "Failed to resolve Rust symbol path", Error: err.Error()}
	}

	m.rememberPath(abs)

	start := req.StartLine
	end := req.EndLine

	if start <= 0 || end <= 0 || start > end {
		start = match.StartLine
		end = match.EndLine

		if start <= 0 {
			start = match.Line
		}

		if end < start {
			end = start + 24
		}
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "rust_read_symbol", Summary: "Failed to read Rust symbol snippet", Error: err.Error()}
	}

	return ToolResult{Tool: "rust_read_symbol", Summary: fmt.Sprintf("Read %s in %s lines %d-%d", rustDisplayName(match.Qualified, match.Name), m.displayPath(abs), start, end), Output: snippet}
}

// -- Tool: rust_related_files -----------------------------------------------

func (m *Manager) rustRelatedFiles(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "rust_related_files", Summary: "Rust related-file search rejected", Error: "pattern is required"}
	}

	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_related_files", Summary: "Rust related-file search unavailable", Error: err.Error()}
	}

	pathFilter := m.rustPathFilter(req.Path)
	collected := filterRelatedPaths(index.RelevantFiles(strings.Fields(pattern), goResultLimit(req.MaxDepth, 8)), pathFilter)

	// Augment with module-tree siblings when the caller's path points at a
	// specific .rs file.
	if strings.TrimSpace(req.Path) != "" {
		abs, resolveErr := m.resolvePath(req.Path, "")
		if resolveErr == nil {
			rel := filepath.ToSlash(m.displayPath(abs))
			for _, sibling := range index.ModuleSiblings(rel, 8) {
				if !slices.Contains(collected, sibling) {
					collected = append(collected, sibling)
				}
			}
		}
	}

	slices.Sort(collected)

	return m.relatedFilesResult("rust_related_files", "Rust", pattern, collected, m.rustHandleDisplay)
}

// -- Tool: rust_file_outline ------------------------------------------------

func (m *Manager) rustFileOutline(req ToolRequest) ToolResult {
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "rust_file_outline", Summary: "Rust file outline rejected", Error: err.Error()}
	}

	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_file_outline", Summary: "Rust file outline unavailable", Error: err.Error()}
	}

	rel := filepath.ToSlash(m.displayPath(path))
	file := index.FileByPath(rel)

	if file == nil {
		return ToolResult{Tool: "rust_file_outline", Summary: "Rust file outline unavailable", Error: fmt.Sprintf("no Rust file indexed at %s", rel)}
	}

	m.rememberPath(path)

	var b strings.Builder

	header := rel
	tags := make([]string, 0, 4)

	if file.IsBinRoot || file.HasMain {
		tags = append(tags, "bin-root")
	}

	if file.IsLibRoot {
		tags = append(tags, "lib-root")
	}

	if file.IsTest {
		tags = append(tags, "test")
	}

	if file.HasBench {
		tags = append(tags, "bench")
	}

	if len(tags) > 0 {
		header += " (" + strings.Join(tags, ", ") + ")"
	}

	fmt.Fprintf(&b, "file: %s\n", header)
	fmt.Fprintf(&b, "counts: fns=%d methods=%d types=%d traits=%d impls=%d macros=%d\n", file.Funcs, file.Methods, file.Types, file.Traits, file.Impls, file.Macros)

	if len(file.Modules) > 0 {
		b.WriteString("mod:\n")

		mods := append([]string(nil), file.Modules...)
		slices.Sort(mods)

		for _, mod := range mods {
			fmt.Fprintf(&b, "- %s\n", mod)
		}
	}

	if len(file.Uses) > 0 && len(file.Uses) <= 32 {
		b.WriteString("use:\n")

		uses := append([]string(nil), file.Uses...)
		slices.Sort(uses)

		for _, use := range uses {
			fmt.Fprintf(&b, "- %s\n", use)
		}
	}

	if len(file.Symbols) == 0 {
		b.WriteString("declarations: (none indexed)\n")
	} else if len(file.Symbols) > 200 {
		fmt.Fprintf(&b, "declarations: omitted for large file (%d symbols).\n", len(file.Symbols))
	} else {
		symbols := append([]semantic.RustSymbol(nil), file.Symbols...)
		slices.SortStableFunc(symbols, func(a, b semantic.RustSymbol) int { return cmp.Compare(a.Line, b.Line) })
		b.WriteString("declarations:\n")

		for _, sym := range symbols {
			name := rustDisplayName(sym.Qualified, sym.Name)
			start := sym.StartLine

			if start <= 0 {
				start = sym.Line
			}

			end := sym.EndLine
			if end < start {
				end = start
			}

			line := fmt.Sprintf("- %s (%s) lines %d-%d", name, sym.Kind, start, end)
			if scope := strings.TrimSpace(sym.Scope); scope != "" && scope != name {
				line += " scope:" + scope
			}

			b.WriteString(line + "\n")
		}
	}

	summary := fmt.Sprintf("Outlined %d symbol(s) in %s. Read-symbol capability is available for a specific body.", len(file.Symbols), rel)

	return ToolResult{Tool: "rust_file_outline", Summary: summary, Output: strings.TrimSpace(b.String())}
}

// -- Tool: rust_definition --------------------------------------------------

func (m *Manager) rustDefinition(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectRustSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "rust_definition", Summary: "Rust definition query rejected", Error: err.Error()}
	}

	if output, err := m.rustDefinitionViaRustAnalyzer(ctx, match); err == nil && strings.TrimSpace(output) != "" {
		return ToolResult{Tool: "rust_definition", Summary: "Resolved Rust definition via rust-analyzer", Output: output}
	}

	display, err := m.rustHandleDisplay(match.Path)
	if err != nil {
		return ToolResult{Tool: "rust_definition", Summary: "Rust definition unavailable", Error: err.Error()}
	}

	return ToolResult{Tool: "rust_definition", Summary: "Resolved Rust definition via semantic index", Output: fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, rustDisplayName(match.Qualified, match.Name))}
}

// -- Tool: rust_references --------------------------------------------------

func (m *Manager) rustReferences(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectRustSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "rust_references", Summary: "Rust reference query rejected", Error: err.Error()}
	}

	pathFilter, err := m.rustPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.rustReferencesViaRustAnalyzer(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{Tool: "rust_references", Summary: "Resolved Rust references via rust-analyzer", Output: output}
		}
	}

	// Fallback to rg. Same shape the cpp tools use.
	searchReq := ToolRequest{
		Pattern: rustReferencePattern(match.Name),
		Path:    defaultString(req.Path, "."),
		Glob:    rustSearchGlob(),
	}
	result := m.searchContent(ctx, searchReq, "rust_references")

	if result.Error == "" {
		result.Summary = "Resolved Rust references via rg fallback"
	}

	return result
}

// -- Tool: rust_calls_from / rust_calls_into --------------------------------

func (m *Manager) rustCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectRustSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "rust_calls_from", Summary: "Rust outgoing-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.rustPathFilterForRequest(req)
	if err == nil {
		if payload, lspErr := m.rustCallHierarchyViaRustAnalyzer(ctx, match, pathFilter, false); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{Tool: "rust_calls_from", Summary: "Resolved outgoing Rust calls via rust-analyzer", Output: payload.Output, Relationships: payload.Relationships}
		}
	}

	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_calls_from", Summary: "Rust outgoing-call query unavailable", Error: err.Error()}
	}

	matches := filterRustCallMatches(index.FindCallsFrom(match.Name, 24), m.rustPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "rust_calls_from", Summary: fmt.Sprintf("No outgoing Rust calls matched %s", rustDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.rustHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, call.CalleeName))
	}

	return ToolResult{Tool: "rust_calls_from", Summary: fmt.Sprintf("Found %d outgoing Rust call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

func (m *Manager) rustCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectRustSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "rust_calls_into", Summary: "Rust incoming-call query rejected", Error: err.Error()}
	}

	pathFilter, err := m.rustPathFilterForRequest(req)
	if err == nil {
		if payload, lspErr := m.rustCallHierarchyViaRustAnalyzer(ctx, match, pathFilter, true); lspErr == nil && strings.TrimSpace(payload.Output) != "" {
			return ToolResult{Tool: "rust_calls_into", Summary: "Resolved incoming Rust calls via rust-analyzer", Output: payload.Output, Relationships: payload.Relationships}
		}
	}

	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_calls_into", Summary: "Rust incoming-call query unavailable", Error: err.Error()}
	}

	matches := filterRustCallMatches(index.FindCallsInto(match.Name, 24), m.rustPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "rust_calls_into", Summary: fmt.Sprintf("No incoming Rust calls matched %s", rustDisplayName(match.Qualified, match.Name))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.rustHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, rustDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "rust_calls_into", Summary: fmt.Sprintf("Found %d incoming Rust call(s).", len(lines)), Output: strings.Join(lines, "\n")}
}

// -- Tool: rust_implementations ---------------------------------------------

func (m *Manager) rustImplementations(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectRustSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "rust_implementations", Summary: "Rust implementation query rejected", Error: err.Error()}
	}

	pathFilter, err := m.rustPathFilterForRequest(req)
	if err == nil {
		if output, lspErr := m.rustImplementationsViaRustAnalyzer(ctx, match, pathFilter); lspErr == nil && strings.TrimSpace(output) != "" {
			return ToolResult{Tool: "rust_implementations", Summary: "Resolved Rust implementations via rust-analyzer", Output: output}
		}
	}

	// Semantic fallback: look up trait implementers by name.
	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{Tool: "rust_implementations", Summary: "Rust implementations unavailable", Error: err.Error()}
	}

	impls := filterRustSymbolMatches(index.TraitImplementers(match.Name, 24), m.rustPathFilter(req.Path))
	if len(impls) == 0 {
		return ToolResult{Tool: "rust_implementations", Summary: fmt.Sprintf("No Rust implementations matched %q", match.Name)}
	}

	lines := make([]string, 0, len(impls))
	for _, candidate := range impls {
		display, err := m.rustHandleDisplay(candidate.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d impl %s", display, candidate.Line, candidate.Qualified))
	}

	return ToolResult{Tool: "rust_implementations", Summary: fmt.Sprintf("Found %d Rust trait impl(s) via semantic fallback.", len(lines)), Output: strings.Join(lines, "\n")}
}

// -- Tool: rust_diagnostics -------------------------------------------------

func (m *Manager) rustDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	if _, err := exec.LookPath("cargo"); err != nil {
		return ToolResult{Tool: "rust_diagnostics", Summary: "Rust diagnostics unavailable", Error: "cargo is not installed"}
	}

	workdir := m.Root()
	if req.Path != "" {
		if abs, err := m.resolvePath(req.Path, ""); err == nil {
			info, statErr := os.Stat(abs)
			if statErr == nil && info.IsDir() {
				workdir = abs
			} else if statErr == nil {
				workdir = filepath.Dir(abs)
			}
		}
	}

	stdout, stderr, err := m.runCommandDetailed(ctx, workdir, "cargo", "check", "--quiet", "--message-format=short")
	output := strings.TrimSpace(strings.TrimSpace(stdout) + "\n" + strings.TrimSpace(stderr))

	output = strings.TrimSpace(output)

	if err != nil && output == "" {
		return ToolResult{Tool: "rust_diagnostics", Summary: "Rust diagnostics failed", Error: err.Error()}
	}

	if output == "" {
		output = "no warnings or errors"
	}

	summary := fmt.Sprintf("Ran cargo check in %s", m.displayPath(workdir))

	return ToolResult{Tool: "rust_diagnostics", Summary: summary, Output: output}
}

// -- Shared helpers ---------------------------------------------------------

func (m *Manager) selectRustSymbolMatch(req ToolRequest) (semantic.RustSymbolMatch, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return semantic.RustSymbolMatch{}, fmt.Errorf("pattern is required")
	}

	index, err := m.getRustIndex()
	if err != nil {
		return semantic.RustSymbolMatch{}, err
	}

	pathFilter, err := m.rustPathFilterForRequest(req)
	if err != nil {
		return semantic.RustSymbolMatch{}, err
	}

	matches := filterRustSymbolMatches(index.FindSymbols(pattern, 12), pathFilter)
	if len(matches) == 0 {
		if pathFilter != "" {
			return semantic.RustSymbolMatch{}, fmt.Errorf("no Rust symbols matched %q under %s", pattern, pathFilter)
		}

		return semantic.RustSymbolMatch{}, fmt.Errorf("no Rust symbols matched %q", pattern)
	}

	return matches[0], nil
}

func (m *Manager) rustPathFilter(path string) string {
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

func (m *Manager) rustPathFilterForRequest(req ToolRequest) (string, error) {
	return m.languagePathFilterForRequest(req, m.rustPathFilter)
}

func (m *Manager) rustHandleDisplay(rel string) (string, error) {
	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}

	return m.handleDisplay(abs), nil
}

func filterRustSymbolMatches(matches []semantic.RustSymbolMatch, pathFilter string) []semantic.RustSymbolMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semantic.RustSymbolMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func filterRustCallMatches(matches []semantic.RustCallMatch, pathFilter string) []semantic.RustCallMatch {
	if pathFilter == "" {
		return matches
	}

	out := make([]semantic.RustCallMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Caller.Path, pathFilter) {
			out = append(out, match)
		}
	}

	return out
}

func rustReferencePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return pattern
	}

	if regexpSafeIdentifier(pattern) {
		return `\b` + pattern + `\b`
	}

	return regexpQuoteMeta(pattern)
}

func rustSearchGlob() string { return "*.rs" }

func rustDisplayName(qualified, name string) string {
	qualified = strings.TrimSpace(qualified)
	if qualified != "" {
		return qualified
	}

	return strings.TrimSpace(name)
}

// ExecuteRustTool dispatches a tool call to the Rust surface. Exported so
// the centralized manager.Execute switch can delegate with a single line
// instead of listing every rust_* case. Callers should only reach this
// when req.Tool begins with "rust_". Returns a result with a clear error
// when the tool name is not recognized.
func (m *Manager) ExecuteRustTool(ctx context.Context, req ToolRequest) ToolResult {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "rust_overview":
		return m.rustOverview()
	case "rust_entrypoints":
		return m.rustEntrypoints(req)
	case "rust_find_symbol":
		return m.rustFindSymbol(req)
	case "rust_workspace_symbol":
		return m.rustWorkspaceSymbol(ctx, req)
	case "rust_read_symbol":
		return m.rustReadSymbol(req)
	case "rust_related_files":
		return m.rustRelatedFiles(req)
	case "rust_file_outline":
		return m.rustFileOutline(req)
	case "rust_definition":
		return m.rustDefinition(ctx, req)
	case "rust_references":
		return m.rustReferences(ctx, req)
	case "rust_calls_from":
		return m.rustCallsFrom(ctx, req)
	case "rust_calls_into":
		return m.rustCallsInto(ctx, req)
	case "rust_implementations":
		return m.rustImplementations(ctx, req)
	case "rust_diagnostics":
		return m.rustDiagnostics(ctx, req)
	default:
		return ToolResult{Tool: tool, Summary: "Tool request rejected", Error: fmt.Sprintf("unsupported Rust tool %q", tool)}
	}
}

// RustToolNames returns the full list of rust_* tools this package
// exposes. tool_manifest.go consumes this when IncludeRust is set.
func RustToolNames() []string {
	return []string{
		"rust_overview",
		"rust_entrypoints",
		"rust_find_symbol",
		"rust_workspace_symbol",
		"rust_read_symbol",
		"rust_related_files",
		"rust_file_outline",
		"rust_definition",
		"rust_references",
		"rust_calls_from",
		"rust_calls_into",
		"rust_implementations",
		"rust_diagnostics",
	}
}
