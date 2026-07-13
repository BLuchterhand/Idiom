package languagetools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/hosttools"
	"idiom/internal/platform/textutil"
	semanticgo "idiom/internal/repository/semantic/golang"
)

func (m *Manager) goOverview() ToolResult {
	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_overview", Summary: "Go overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)

	summary := index.Summary()
	if summary == "" {
		summary = "Go semantic index is available, but no summary could be generated."
	}

	return ToolResult{
		Tool:    "go_overview",
		Summary: "Built Go semantic overview for the workspace",
		Output:  summary,
	}
}

func (m *Manager) goEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_entrypoints", Summary: "Go entrypoints unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(goResultLimit(req.MaxDepth, 6))
	if len(paths) == 0 {
		return ToolResult{Tool: "go_entrypoints", Summary: "No Go entrypoints found"}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		file := index.FileByPath(rel)

		display, err := m.goHandleDisplay(rel)
		if err != nil {
			continue
		}

		line := display
		if file != nil {
			line += fmt.Sprintf(" (package %s, funcs=%d, methods=%d, types=%d)", file.Package, file.Funcs, file.Methods, file.Types)
		}

		lines = append(lines, line)
	}

	return ToolResult{
		Tool:    "go_entrypoints",
		Summary: fmt.Sprintf("Found %d likely Go entrypoint file(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goFindSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_find_symbol", Summary: "Go symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_find_symbol", Summary: "Go symbol search unavailable", Error: err.Error()}
	}

	matches := filterSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_find_symbol", Summary: fmt.Sprintf("No Go symbols matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.goHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, goQualifiedSymbol(match)))
	}

	return ToolResult{
		Tool:    "go_find_symbol",
		Summary: fmt.Sprintf("Found %d matching Go symbol(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goFindFunction(req ToolRequest) ToolResult {
	return m.goFindSymbolsByKind(req, "func", "go_find_function", "function")
}

func (m *Manager) goFindType(req ToolRequest) ToolResult {
	return m.goFindSymbolsByKind(req, "type", "go_find_type", "type")
}

func (m *Manager) goFindConst(req ToolRequest) ToolResult {
	return m.goFindSymbolsByKind(req, "const", "go_find_const", "constant")
}

func (m *Manager) goFindSymbolsByKind(req ToolRequest, kind string, toolName string, label string) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: toolName, Summary: "Go symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: toolName, Summary: "Go symbol search unavailable", Error: err.Error()}
	}

	matches := filterSymbolMatches(index.FindSymbolsByKind(pattern, kind, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: toolName, Summary: fmt.Sprintf("No Go %s matched %q", label, pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.goHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, goQualifiedSymbol(match)))
	}

	return ToolResult{
		Tool:    toolName,
		Summary: fmt.Sprintf("Found %d matching Go %s(s).", len(lines), label),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goReadSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_read_symbol", Summary: "Go symbol read rejected", Error: "pattern is required"}
	}

	if err := validateSymbolPattern("go_read_symbol", pattern); err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Go symbol read rejected", Error: err.Error()}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Go symbol read unavailable", Error: err.Error()}
	}

	pathFilter, err := m.goPathFilterForRequest(req)
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Go symbol read rejected", Error: err.Error()}
	}

	matches := filterSymbolMatches(index.FindSymbols(pattern, 4), pathFilter)
	if len(matches) == 0 {
		if result, ok := m.goReadSymbolPathMiss(req, pattern, pathFilter, index); ok {
			return result
		}

		return ToolResult{Tool: "go_read_symbol", Summary: fmt.Sprintf("No Go symbols matched %q", pattern)}
	}

	match := matches[0]

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Failed to resolve Go symbol path", Error: err.Error()}
	}

	m.rememberPath(abs)

	start, end := goReadSymbolWindow(match, req.StartLine, req.EndLine)
	displayPath := m.displayPath(abs)
	summary := fmt.Sprintf("Read %s in %s lines %d-%d", goQualifiedSymbol(match), displayPath, start, end)

	if req.StartLine == 0 && req.EndLine == 0 && match.EndLine > 0 && end < match.EndLine {
		summary = fmt.Sprintf("%s (truncated from lines %d-%d; full-body adapter: go_extract_body)", summary, match.StartLine, match.EndLine)
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Failed to read Go symbol snippet", Error: err.Error()}
	}

	return ToolResult{
		Tool:    "go_read_symbol",
		Summary: summary,
		Output:  snippet,
		Paths:   []string{displayPath},
		SourceWindows: []SourceWindowPlan{{
			Path: displayPath, StartLine: start, EndLine: end,
			Truncated: req.StartLine == 0 && req.EndLine == 0 && match.EndLine > 0 && end < match.EndLine,
		}},
	}
}

// goReadSymbolWindow picks the line range to return for a go_read_symbol
// call. When the model passes an explicit StartLine/EndLine, honor that.
// Otherwise prefer the symbol's actual extent (match.StartLine / EndLine
// from the index) capped at goReadSymbolMaxLines so very large functions
// don't dump hundreds of lines into the model's context. Falls back to a
// window centered on match.Line only when the symbol has no recorded
// extent — that's the legacy behavior for index entries that pre-date
// extent tracking.
//
// Previous bug: this function used `match.Line - 12` to `match.Line + 20`
// regardless of where the symbol started. For a function declared at
// line 16 (with imports above), the read returned lines 4-36 — the
// imports plus the function signature, not the function body. Live
// run 20260425-161904 surfaced this when go_read_symbol on
// `Runner.runChildTask` returned imports while go_extract_body returned
// the actual 117-line function body. Fixed by reading from the symbol's
// real start line.
func goReadSymbolWindow(match semanticgo.SymbolMatch, requestedStart, requestedEnd int) (int, int) {
	if requestedStart > 0 && requestedEnd >= requestedStart {
		return requestedStart, requestedEnd
	}

	const goReadSymbolMaxLines = 120

	if match.StartLine > 0 {
		start := match.StartLine
		end := match.EndLine

		if end < start {
			end = start
		}

		if end == 0 {
			end = start + goReadSymbolMaxLines
		}

		if end-start+1 > goReadSymbolMaxLines {
			end = start + goReadSymbolMaxLines - 1
		}

		return start, end
	}

	start := match.Line - 12
	if start < 1 {
		start = 1
	}

	end := match.Line + 20

	return start, end
}

func (m *Manager) goFindPackage(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_find_package", Summary: "Go package search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_find_package", Summary: "Go package search unavailable", Error: err.Error()}
	}

	matches := filterPackageMatches(index.FindPackages(pattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_find_package", Summary: fmt.Sprintf("No Go packages matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("%s (package %s, files=%d, funcs=%d, methods=%d, types=%d)", match.Path, match.Name, match.Files, match.Funcs, match.Methods, match.Types))
	}

	return ToolResult{
		Tool:    "go_find_package",
		Summary: fmt.Sprintf("Found %d matching Go package(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goRelatedFiles(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_related_files", Summary: "Go related-file search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_related_files", Summary: "Go related-file search unavailable", Error: err.Error()}
	}

	paths := filterRelatedPaths(index.RelevantFiles(strings.Fields(pattern), goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(paths) == 0 {
		return ToolResult{Tool: "go_related_files", Summary: fmt.Sprintf("No related Go files matched %q", pattern)}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		display, err := m.goHandleDisplay(rel)
		if err != nil {
			continue
		}

		lines = append(lines, display)
	}

	return ToolResult{
		Tool:    "go_related_files",
		Summary: fmt.Sprintf("Found %d related Go file(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_workspace_symbol", Summary: "Go workspace-symbol search rejected", Error: "pattern is required"}
	}

	if gopls := m.goplsBinary(); gopls != "" {
		output, err := m.runGoPLSCommand(ctx, "workspace_symbol", pattern)
		formatted := m.formatGoplsWorkspaceSymbolOutput(output)
		result := ToolResult{
			Tool:    "go_workspace_symbol",
			Summary: "Resolved Go workspace symbols with gopls",
			Output:  formatted,
		}

		if err != nil {
			fallback := m.goFindSymbol(req)

			fallback.Tool = "go_workspace_symbol"

			if fallback.Error == "" {
				fallback.Summary = "Resolved Go workspace symbols via semantic fallback after gopls failure"
				return fallback
			}

			result.Summary = "gopls workspace-symbol search failed"
			result.Error = err.Error()
		}

		if strings.TrimSpace(result.Output) == "" && result.Error == "" {
			fallback := m.goFindSymbol(req)

			fallback.Tool = "go_workspace_symbol"

			if fallback.Error == "" && strings.TrimSpace(fallback.Output) != "" {
				fallback.Summary = "Resolved Go workspace symbols via semantic fallback after empty gopls result"
				return fallback
			}

			result.Summary = fmt.Sprintf("No Go workspace symbols matched %q", pattern)
		}

		return result
	}

	fallback := m.goFindSymbol(req)

	fallback.Tool = "go_workspace_symbol"

	if fallback.Error == "" {
		fallback.Summary += " (semantic fallback; gopls unavailable)"
	} else {
		fallback.Error = "gopls is not installed and semantic fallback did not find a match: " + fallback.Error
	}

	return fallback
}

func (m *Manager) goDefinition(ctx context.Context, req ToolRequest) ToolResult {
	return m.runGoLocationQuery(ctx, "go_definition", "definition", req, "Resolved Go definition")
}

func (m *Manager) goReferences(ctx context.Context, req ToolRequest) ToolResult {
	return m.runGoLocationQuery(ctx, "go_references", "references", req, "Resolved Go references")
}

func (m *Manager) goImplementations(ctx context.Context, req ToolRequest) ToolResult {
	return m.runGoLocationQuery(ctx, "go_implementations", "implementation", req, "Resolved Go implementations")
}

func (m *Manager) goCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "go_calls_from", Summary: "Go outgoing-call query rejected", Error: err.Error()}
	}

	if gopls := m.goplsBinary(); gopls != "" {
		output, err := m.runGoPLSCommand(ctx, "call_hierarchy", m.goSymbolPosition(match))
		result := ToolResult{
			Tool:    "go_calls_from",
			Summary: "Resolved outgoing calls with gopls",
			Output:  m.formatGoCallHierarchy(output, "callee["),
		}

		if err != nil {
			result.Summary = "gopls outgoing-call query failed"
			result.Error = err.Error()

			return result
		}

		if strings.TrimSpace(result.Output) != "" {
			return result
		}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_calls_from", Summary: "Go outgoing-call query unavailable", Error: err.Error()}
	}

	matches := filterCallMatches(index.FindCallsFrom(match.Name, 16), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_calls_from", Summary: fmt.Sprintf("No outgoing Go calls matched %s", goQualifiedSymbol(match))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		callerDisplay, err := m.goHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		line := fmt.Sprintf("%s:%d -> %s", callerDisplay, call.Line, call.CalleeName)
		if call.CalleeTarget.Path != "" {
			targetDisplay, err := m.goHandleDisplay(call.CalleeTarget.Path)
			if err == nil {
				line = fmt.Sprintf("%s:%d -> %s %s", callerDisplay, call.Line, targetDisplay, goQualifiedSymbol(call.CalleeTarget))
			}
		}

		lines = append(lines, line)
	}

	return ToolResult{
		Tool:    "go_calls_from",
		Summary: fmt.Sprintf("Found %d outgoing Go call(s) via semantic fallback.", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "go_calls_into", Summary: "Go incoming-call query rejected", Error: err.Error()}
	}

	if gopls := m.goplsBinary(); gopls != "" {
		output, err := m.runGoPLSCommand(ctx, "call_hierarchy", m.goSymbolPosition(match))
		result := ToolResult{
			Tool:    "go_calls_into",
			Summary: "Resolved incoming calls with gopls",
			Output:  m.formatGoCallHierarchy(output, "caller["),
		}

		if err != nil {
			result.Summary = "gopls incoming-call query failed"
			result.Error = err.Error()

			return result
		}

		if strings.TrimSpace(result.Output) != "" {
			return result
		}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_calls_into", Summary: "Go incoming-call query unavailable", Error: err.Error()}
	}

	matches := filterCallMatches(index.FindCallsInto(match.Name, 16), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_calls_into", Summary: fmt.Sprintf("No incoming Go calls matched %s", goQualifiedSymbol(match))}
	}

	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.goHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, goQualifiedSymbol(match)))
	}

	return ToolResult{
		Tool:    "go_calls_into",
		Summary: fmt.Sprintf("Found %d incoming Go call(s) via semantic fallback.", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goExtractBody(req ToolRequest) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "go_extract_body", Summary: "Go body extraction rejected", Error: err.Error()}
	}

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "go_extract_body", Summary: "Failed to resolve Go symbol path", Error: err.Error()}
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
		return ToolResult{Tool: "go_extract_body", Summary: "Failed to extract Go body", Error: err.Error()}
	}

	displayPath := m.displayPath(abs)

	return ToolResult{
		Tool:          "go_extract_body",
		Summary:       fmt.Sprintf("Extracted %s from %s lines %d-%d", goQualifiedSymbol(match), displayPath, start, end),
		Output:        body,
		Paths:         []string{displayPath},
		SourceWindows: []SourceWindowPlan{{Path: displayPath, StartLine: start, EndLine: end}},
	}
}

func (m *Manager) goImporters(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_importers", Summary: "Go importer query rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_importers", Summary: "Go importer query unavailable", Error: err.Error()}
	}

	packages := filterPackageMatches(index.FindPackages(pattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(packages) == 0 && index.Module != "" {
		modulePattern := strings.TrimPrefix(pattern, index.Module)

		modulePattern = strings.TrimPrefix(modulePattern, "/")

		if modulePattern != pattern {
			packages = filterPackageMatches(index.FindPackages(modulePattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
		}
	}

	if len(packages) == 0 {
		return ToolResult{Tool: "go_importers", Summary: fmt.Sprintf("No Go packages matched %q", pattern)}
	}

	targets := make(map[string]string, len(packages))
	for _, pkg := range packages {
		targets[m.goImportPath(pkg.Path, index.Module)] = pkg.Path
	}

	lines := make([]string, 0)

	for _, file := range index.Files {
		for _, imp := range file.Imports {
			targetPath, ok := targets[imp]
			if !ok {
				continue
			}

			display, err := m.goHandleDisplay(file.Path)
			if err != nil {
				continue
			}

			lines = append(lines, fmt.Sprintf("%s imports %s", display, targetPath))

			break
		}

		if len(lines) >= m.maxDirectoryEntries() {
			break
		}
	}

	if len(lines) == 0 {
		return ToolResult{Tool: "go_importers", Summary: fmt.Sprintf("No local Go importers found for %q", pattern)}
	}

	slices.Sort(lines)

	return ToolResult{
		Tool:    "go_importers",
		Summary: fmt.Sprintf("Found %d Go importer file(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	files, err := m.goDiagnosticTargets(req)
	if err != nil {
		return ToolResult{Tool: "go_diagnostics", Summary: "Go diagnostics unavailable", Error: err.Error()}
	}

	if len(files) == 0 {
		return ToolResult{Tool: "go_diagnostics", Summary: "No Go files available for diagnostics"}
	}

	lines := make([]string, 0, len(files))
	failures := make([]string, 0)

	if m.goplsBinary() != "" {
		compiled := make(map[string]struct{})

		for _, rel := range files {
			output, err := m.runGoPLSCommand(ctx, "check", rel)

			output = m.formatGoLocationOutput(output)

			if err != nil {
				line := fmt.Sprintf("%s: %s", rel, err.Error())
				if strings.TrimSpace(output) != "" {
					line += "\n" + output
				}

				lines = append(lines, line)
				failures = append(failures, line)
			} else if strings.TrimSpace(output) == "" {
				lines = append(lines, fmt.Sprintf("%s: no warnings or errors", rel))
			} else {
				lines = append(lines, output)
				failures = append(failures, output)
			}

			if pkg, ok := goPackageForDiagnosticFile(rel); ok {
				if _, seen := compiled[pkg]; seen {
					continue
				}

				compiled[pkg] = struct{}{}

				line, compileErr := m.goTestCompileDiagnostic(ctx, pkg)

				lines = append(lines, line)

				if compileErr != nil {
					failures = append(failures, line)
				}
			}
		}

		result := ToolResult{
			Tool:    "go_diagnostics",
			Summary: fmt.Sprintf("Collected Go diagnostics for %d file(s).", len(files)),
			Output:  strings.Join(lines, "\n"),
			Facts:   diagnosticToolFacts(files),
		}
		if len(failures) > 0 {
			result.Error = strings.Join(failures, "\n")
		}

		return result
	}

	output, err := m.runCommand(ctx, m.Root(), "go", "build", "./...")
	result := ToolResult{
		Tool:    "go_diagnostics",
		Summary: "Collected Go build diagnostics via go build fallback",
		Output:  strings.TrimSpace(output),
		Facts:   diagnosticToolFacts(files),
	}

	if err != nil {
		result.Error = err.Error()
	}

	for _, pkg := range goPackagesForDiagnosticFiles(files) {
		line, compileErr := m.goTestCompileDiagnostic(ctx, pkg)
		if strings.TrimSpace(result.Output) == "" {
			result.Output = line
		} else {
			result.Output += "\n" + line
		}

		if compileErr != nil {
			if strings.TrimSpace(result.Error) == "" {
				result.Error = line
			} else {
				result.Error += "\n" + line
			}
		}
	}

	return result
}

func goPackagesForDiagnosticFiles(files []string) []string {
	seen := make(map[string]struct{})

	var out []string

	for _, file := range files {
		pkg, ok := goPackageForDiagnosticFile(file)
		if !ok {
			continue
		}

		if _, exists := seen[pkg]; exists {
			continue
		}

		seen[pkg] = struct{}{}
		out = append(out, pkg)
	}

	slices.Sort(out)

	return out
}

func goPackageForDiagnosticFile(file string) (string, bool) {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if !strings.HasSuffix(strings.ToLower(filepath.Base(file)), ".go") {
		return "", false
	}

	dir := filepath.ToSlash(filepath.Dir(file))
	if dir == "" || dir == "." {
		return ".", true
	}

	return "./" + strings.TrimPrefix(dir, "./"), true
}

func (m *Manager) goTestCompileDiagnostic(ctx context.Context, pkg string) (string, error) {
	output, err := m.runCommand(ctx, m.Root(), "go", "test", pkg, "-run", "^$")

	output = strings.TrimSpace(output)

	if err != nil {
		if output == "" {
			output = err.Error()
		}

		return fmt.Sprintf("%s: go test compile failed\n%s", pkg, output), err
	}

	if output == "" {
		output = "ok"
	}

	return fmt.Sprintf("%s: go test compile passed\n%s", pkg, output), nil
}

func (m *Manager) runGoLocationQuery(ctx context.Context, toolName string, goplsCommand string, req ToolRequest, successSummary string) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		if goSymbolNoMatchError(err) {
			return ToolResult{Tool: toolName, Summary: "Go location query found no matching symbol", Output: err.Error()}
		}

		return ToolResult{Tool: toolName, Summary: "Go location query rejected", Error: err.Error()}
	}

	var goplsErr error

	if m.goplsBinary() != "" {
		args := []string{goplsCommand, m.goSymbolPosition(match)}
		if toolName == "go_references" {
			args = []string{goplsCommand, "-d", m.goSymbolPosition(match)}
		}

		output, err := m.runGoPLSCommand(ctx, args...)
		result := ToolResult{
			Tool:    toolName,
			Summary: successSummary,
			Output:  m.formatGoLocationOutput(output),
		}

		if err != nil {
			goplsErr = err
		} else if strings.TrimSpace(result.Output) != "" {
			return result
		}
	}

	switch toolName {
	case "go_definition":
		display, err := m.goHandleDisplay(match.Path)
		if err != nil {
			return ToolResult{Tool: toolName, Summary: "Go definition unavailable", Error: err.Error()}
		}

		return ToolResult{
			Tool:    toolName,
			Summary: goLocationFallbackSummary("Resolved Go definition via semantic fallback", goplsErr),
			Output:  goLocationFallbackOutput(fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, goQualifiedSymbol(match)), goplsErr),
		}
	case "go_references":
		refReq := ToolRequest{Pattern: "\\b" + match.Name + "\\b", Path: ".", Glob: "*.go"}
		if strings.TrimSpace(req.Path) != "" {
			refReq.Path = req.Path
		}

		result := m.searchContent(ctx, refReq, toolName)
		if result.Error == "" {
			result.Summary = goLocationFallbackSummary("Resolved Go references via semantic/rg fallback", goplsErr)
			result.Output = goLocationFallbackOutput(result.Output, goplsErr)
		}

		return result
	case "go_implementations":
		return ToolResult{Tool: toolName, Summary: "Go implementations unavailable", Error: "gopls is required for implementation queries in this harness"}
	default:
		return ToolResult{Tool: toolName, Summary: "Unsupported Go location query", Error: "unsupported query"}
	}
}

func goSymbolNoMatchError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(err.Error()))

	return strings.HasPrefix(lower, "no go symbols matched ")
}

func goLocationFallbackSummary(summary string, goplsErr error) string {
	if goplsErr == nil {
		return summary
	}

	return summary + " (gopls failed)"
}

func goLocationFallbackOutput(output string, goplsErr error) string {
	if goplsErr == nil {
		return output
	}

	if strings.TrimSpace(output) == "" {
		return "gopls_error: " + goplsErr.Error()
	}

	return output + "\n\ngopls_error: " + goplsErr.Error()
}

func (m *Manager) runGoPLSCommand(ctx context.Context, args ...string) (string, error) {
	gopls := m.goplsBinary()
	if gopls == "" {
		return "", fmt.Errorf("gopls is not installed")
	}

	cacheRoot := filepath.Join(os.TempDir(), "idiom-gopls")

	_ = os.MkdirAll(filepath.Join(cacheRoot, "xdg"), 0o755)
	_ = os.MkdirAll(filepath.Join(cacheRoot, "gocache"), 0o755)
	_ = os.MkdirAll(filepath.Join(cacheRoot, "home"), 0o755)

	result := m.runCommandResult(
		ctx,
		m.Root(),
		[]string{
			"HOME=" + filepath.Join(cacheRoot, "home"),
			"XDG_CACHE_HOME=" + filepath.Join(cacheRoot, "xdg"),
			"GOCACHE=" + filepath.Join(cacheRoot, "gocache"),
		},
		gopls,
		args...,
	)

	if result.Err != nil {
		return result.Output, result.Err
	}

	return result.Stdout, nil
}

func (m *Manager) isGoWorkspace() bool {
	if !m.Enabled() {
		return false
	}

	info, err := os.Stat(filepath.Join(m.Root(), "go.mod"))

	return err == nil && !info.IsDir()
}

func (m *Manager) getGoIndex() (*semanticgo.GoIndex, error) {
	if !m.isGoWorkspace() {
		return nil, fmt.Errorf("go-specific tools require a workspace with go.mod at the root")
	}

	token := m.currentGoIndexToken()
	m.mu.Lock()

	cached := m.goIndex
	cachedToken := m.goIndexToken
	m.mu.Unlock()

	if cached != nil && (token == "" || token == cachedToken) {
		return cached, nil
	}

	index, err := semanticgo.BuildGoIndex(m.Root())
	if err != nil {
		return nil, fmt.Errorf("build Go semantic index: %w", err)
	}

	if index == nil {
		return nil, fmt.Errorf("no Go files were indexed under the workspace")
	}

	m.mu.Lock()

	m.goIndex = index
	m.goIndexToken = token
	m.mu.Unlock()

	return index, nil
}

func (m *Manager) currentGoIndexToken() string {
	if !m.Enabled() {
		return ""
	}

	snapshot, err := gitstate.Current(context.Background(), m.Root())
	if err != nil {
		return ""
	}

	return snapshot.Token()
}

func (m *Manager) goplsBinary() string {
	m.mu.Lock()

	if m.goplsChecked {
		path := m.goplsPath
		m.mu.Unlock()

		return path
	}

	m.mu.Unlock()

	path := hosttools.FirstAvailable("gopls")
	if path == "" {
		path = m.findGoTool("gopls")
	}

	m.mu.Lock()

	m.goplsPath = path
	m.goplsChecked = true
	m.mu.Unlock()

	return path
}

func (m *Manager) findGoTool(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	if gobin, err := m.runCommand(context.Background(), m.Root(), "go", "env", "GOBIN"); err == nil && gobin != "" {
		candidate := filepath.Join(gobin, name)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate
		}
	}

	if gopath, err := m.runCommand(context.Background(), m.Root(), "go", "env", "GOPATH"); err == nil && gopath != "" {
		candidate := filepath.Join(gopath, "bin", name)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate
		}
	}

	return ""
}

func (m *Manager) goHandleDisplay(rel string) (string, error) {
	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}

	return m.handleDisplay(abs), nil
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

	lines := textutil.SplitWorkspaceFileLines(data)

	if start <= 0 {
		start = 1
	}

	if end <= 0 || end > len(lines) {
		end = len(lines)
	}

	start, end, _ = textutil.ClampRequestedLineRange(len(lines), start, end, start, end)
	if start > end {
		return "", fmt.Errorf("start_line must be <= end_line")
	}

	var b strings.Builder

	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

func (m *Manager) goPathFilter(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	resolved, err := m.resolvePath(path, "")
	if err != nil {
		return ""
	}

	return filepath.ToSlash(m.displayPath(resolved))
}

func filterSymbolMatches(matches []semanticgo.SymbolMatch, pathFilter string) []semanticgo.SymbolMatch {
	if pathFilter == "" {
		return matches
	}

	filtered := make([]semanticgo.SymbolMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			filtered = append(filtered, match)
		}
	}

	return filtered
}

func filterPackageMatches(matches []semanticgo.PackageMatch, pathFilter string) []semanticgo.PackageMatch {
	if pathFilter == "" {
		return matches
	}

	filtered := make([]semanticgo.PackageMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			filtered = append(filtered, match)
		}
	}

	return filtered
}

func filterRelatedPaths(paths []string, pathFilter string) []string {
	if pathFilter == "" {
		return paths
	}

	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if pathMatchesFilter(path, pathFilter) {
			filtered = append(filtered, path)
		}
	}

	return filtered
}

func pathMatchesFilter(path string, filter string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	filter = filepath.ToSlash(strings.TrimSpace(filter))

	if path == "" || filter == "" {
		return false
	}

	if path == filter {
		return true
	}

	return strings.HasPrefix(path, filter+"/")
}

func goQualifiedSymbol(match semanticgo.SymbolMatch) string {
	if match.Kind == "method" && strings.TrimSpace(match.Receiver) != "" {
		return match.Receiver + "." + match.Name
	}

	return match.Name
}

func goResultLimit(requested int, fallback int) int {
	if requested > 0 {
		return requested
	}

	return fallback
}

func (m *Manager) selectGoSymbolMatch(req ToolRequest) (semanticgo.SymbolMatch, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return semanticgo.SymbolMatch{}, fmt.Errorf("pattern is required")
	}

	index, err := m.getGoIndex()
	if err != nil {
		return semanticgo.SymbolMatch{}, err
	}

	pathFilter, err := m.goPathFilterForRequest(req)
	if err != nil {
		return semanticgo.SymbolMatch{}, err
	}

	matches := filterSymbolMatches(index.FindSymbols(pattern, 12), pathFilter)
	if len(matches) == 0 {
		if pathFilter != "" {
			return semanticgo.SymbolMatch{}, fmt.Errorf("no Go symbols matched %q under %s", pattern, pathFilter)
		}

		return semanticgo.SymbolMatch{}, fmt.Errorf("no Go symbols matched %q", pattern)
	}

	return matches[0], nil
}

func (m *Manager) goPathFilterForRequest(req ToolRequest) (string, error) {
	if strings.TrimSpace(req.Path) != "" {
		return m.goPathFilter(req.Path), nil
	}

	if strings.TrimSpace(req.ID) == "" {
		return "", nil
	}

	path, err := m.pathForHandle(req.invocationContext, req.ID)
	if err != nil {
		return "", err
	}

	return filepath.ToSlash(m.displayPath(path)), nil
}

func (m *Manager) goSymbolPosition(match semanticgo.SymbolMatch) string {
	line := match.Line
	if line <= 0 {
		line = 1
	}

	column := match.Column
	if column <= 0 {
		column = 1
	}

	return fmt.Sprintf("%s:%d:%d", filepath.ToSlash(match.Path), line, column)
}

func (m *Manager) formatGoLocationOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	formatted := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if rewritten, ok := m.rewriteGoplsWorkspaceSymbolLine(line); ok {
			formatted = append(formatted, rewritten)
		} else {
			formatted = append(formatted, line)
		}

		if len(formatted) >= m.maxDirectoryEntries() {
			break
		}
	}

	return strings.Join(formatted, "\n")
}

func (m *Manager) formatGoCallHierarchy(output string, prefix string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	formatted := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if prefix != "" && !strings.HasPrefix(line, prefix) {
			continue
		}

		if idx := strings.Index(line, " in "); idx >= 0 {
			suffix := line[idx+4:]
			parts := strings.Fields(suffix)

			if len(parts) > 0 {
				if abs, err := m.resolvePath(parts[0], ""); err == nil {
					line = strings.Replace(line, " in "+parts[0], " in "+m.handleDisplay(abs), 1)
				}
			}
		}

		formatted = append(formatted, line)
		if len(formatted) >= m.maxDirectoryEntries() {
			break
		}
	}

	return strings.Join(formatted, "\n")
}

func filterCallMatches(matches []semanticgo.CallMatch, pathFilter string) []semanticgo.CallMatch {
	if pathFilter == "" {
		return matches
	}

	filtered := make([]semanticgo.CallMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Caller.Path, pathFilter) || pathMatchesFilter(match.CalleeTarget.Path, pathFilter) {
			filtered = append(filtered, match)
		}
	}

	return filtered
}

func (m *Manager) goDiagnosticTargets(req ToolRequest) ([]string, error) {
	index, err := m.getGoIndex()
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
			if filepath.Ext(rel) != ".go" {
				return nil, fmt.Errorf("%s is not a Go file", rel)
			}

			return []string{rel}, nil
		}

		filter, err := filepath.Rel(m.Root(), resolved)
		if err != nil {
			return nil, err
		}

		filter = filepath.ToSlash(strings.TrimSpace(filter))
		if filter == "." {
			filter = ""
		}

		files := make([]string, 0)

		for _, file := range index.Files {
			if filter == "" || pathMatchesFilter(file.Path, filter) {
				files = append(files, file.Path)
			}
		}

		slices.Sort(files)

		limit := goResultLimit(req.MaxDepth, 8)
		if limit > 0 && len(files) > limit {
			files = files[:limit]
		}

		return files, nil
	}

	if strings.TrimSpace(req.Path) != "" {
		return resolveTarget(req.Path)
	}

	if strings.TrimSpace(req.ID) != "" {
		path, err := m.pathForHandle(req.invocationContext, req.ID)
		if err != nil {
			return nil, err
		}

		return resolveTarget(path)
	}

	files := index.IssueHuntFiles(goResultLimit(req.MaxDepth, 4))
	if len(files) == 0 {
		files = index.DefaultFiles(goResultLimit(req.MaxDepth, 4))
	}

	return files, nil
}

func (m *Manager) goImportPath(pkgPath string, module string) string {
	pkgPath = filepath.ToSlash(strings.TrimSpace(pkgPath))
	module = strings.TrimSpace(module)

	if module == "" {
		return pkgPath
	}

	if pkgPath == "." {
		return module
	}

	return module + "/" + pkgPath
}

func (m *Manager) formatGoplsWorkspaceSymbolOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	formatted := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if rewritten, ok := m.rewriteGoplsWorkspaceSymbolLine(line); ok {
			formatted = append(formatted, rewritten)
		} else {
			formatted = append(formatted, line)
		}

		if len(formatted) >= m.maxDirectoryEntries() {
			break
		}
	}

	return strings.Join(formatted, "\n")
}

func (m *Manager) rewriteGoplsWorkspaceSymbolLine(line string) (string, bool) {
	path, rest, ok := splitGoplsLocation(line)
	if !ok {
		return "", false
	}

	abs, err := m.resolvePath(path, "")
	if err != nil {
		return "", false
	}

	return m.handleDisplay(abs) + ":" + rest, true
}

func splitGoplsLocation(line string) (string, string, bool) {
	first := strings.Index(line, ":")
	if first <= 0 {
		return "", "", false
	}

	second := strings.Index(line[first+1:], ":")
	if second <= 0 {
		return "", "", false
	}

	second += first + 1
	if !allDigits(line[first+1 : second]) {
		return "", "", false
	}

	third := strings.Index(line[second+1:], ":")
	if third <= 0 {
		rangeText := line[second+1:]
		if !allDigitsOrRange(rangeText) {
			return "", "", false
		}

		return line[:first], line[first+1:], true
	}

	third += second + 1
	if !allDigitsOrRange(line[second+1 : third]) {
		return "", "", false
	}

	return line[:first], line[first+1:], true
}

func allDigits(text string) bool {
	if text == "" {
		return false
	}

	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return false
		}
	}

	return true
}

func allDigitsOrRange(text string) bool {
	if text == "" {
		return false
	}

	for _, ch := range text {
		switch {
		case ch >= '0' && ch <= '9':
		case ch == '-':
		default:
			return false
		}
	}

	return true
}
