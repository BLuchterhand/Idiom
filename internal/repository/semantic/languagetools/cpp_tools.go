package languagetools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/hosttools"
	semanticcpp "idiom/internal/repository/semantic/cpp"
)

// getCppIndex loads the index for tools that only need files and includes,
// within ctx's envelope. A nil ctx is a follow-up inside a tool that already
// loaded the index (fallbacks): it reuses that index, partial or not, rather
// than start a build with no envelope.
func (m *Manager) getCppIndex(ctx context.Context) (*semanticcpp.CppIndex, error) {
	if ctx == nil {
		m.mu.Lock()
		memo := m.cppIndex
		m.mu.Unlock()

		if memo != nil {
			return memo, nil
		}

		ctx = context.Background()
	}

	index, _, err := m.loadCppIndex(ctx)

	return index, err
}

// cppIndexBudget is how long one call may build: the caller's tool envelope
// minus what must still run after the build, capped by IDIOM_CPP_INDEX_BUDGET
// (default cppIndexBudgetCap). After the build come a second Git snapshot,
// which costs about what the first one (snapshotCost) did on this machine and
// load, then the checkpoint write and the answer (cppIndexReserve). The build
// therefore works as long as the caller waits instead of stopping at a fixed
// guess, and an unfinished build resumes on the next call.
func cppIndexBudget(parent context.Context, snapshotCost time.Duration) time.Duration {
	budget := cppBudgetSetting("IDIOM_CPP_INDEX_BUDGET", cppIndexBudgetCap)
	if deadline, ok := parent.Deadline(); ok {
		budget = min(budget, max(time.Millisecond, time.Until(deadline)-cppIndexReserve-snapshotCost))
	}

	return budget
}

// loadCppIndex returns the C++ syntax index of the live working tree with its
// identity and cost. A build that runs out of budget saves its parsed files so
// the next call resumes, and answers now from the files parsed so far:
// info.Partial is set and every result built from it is marked partial.
func (m *Manager) loadCppIndex(parent context.Context) (*semanticcpp.CppIndex, cppIndexInfo, error) {
	if !m.Enabled() {
		return nil, cppIndexInfo{}, fmt.Errorf("c++-specific tools require a workspace with C/C++ source files or compile_commands.json")
	}

	info := m.cppSnapshotOutsideDeadline(parent)
	budget := cppIndexBudget(parent, info.Git)
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	info.Budget = budget

	m.mu.Lock()

	cached, cachedInfo := m.cppIndex, m.cppIndexInfo
	m.mu.Unlock()

	var disk *cppDiskCache
	var previous *semanticcpp.CppIndex
	var changed map[string]bool
	resume := false

	if cached != nil && info.Key != "" && info.Key == cachedInfo.Key {
		if !cachedInfo.Partial {
			cachedInfo.Memoized = true
			cachedInfo.Git = info.Git
			cachedInfo.Budget = budget

			return cached, cachedInfo, nil
		}

		// A partial index of this snapshot resumes in memory.
		previous, resume, info.Cache = cached, true, "resumed"
	} else if cached != nil && info.Key == "" && cachedInfo.Key == "" && cachedInfo.Partial {
		// Without a Git key nothing proves the tree is unchanged, so every
		// retained file is checked by content hash and reparsed only if it differs.
		previous, changed, info.Cache = cached, cppAllPaths(cached), "resumed_verified"
	}

	if info.Key != "" {
		var err error
		disk, err = openCppDiskCache()
		if err != nil {
			info.CacheError = err.Error()
		} else {
			defer disk.root.Close()
			started := time.Now()
			header, loaded, err := disk.read(info.Key, true)
			if previous != nil {
				// Resuming in memory; the disk is only written below.
			} else if err == nil {
				disk.touch(info.Key)
				loaded.Root = m.Root()
				if header.Complete {
					info.Build, info.Symbols = loaded.Timings, header.Info.Symbols
					info.Cache, info.Load = "disk_hit", time.Since(started)
					m.mu.Lock()
					m.cppIndex, m.cppIndexInfo = loaded, info
					m.mu.Unlock()
					return loaded, info, nil
				}
				previous, resume, info.Cache = loaded, true, "resumed"
			} else {
				previous, changed = disk.nearest(ctx, info)
				if previous != nil {
					info.Cache = "incremental"
				}
			}
			info.Load = time.Since(started)
		}
	}
	index, err := semanticcpp.BuildCppIndexWithOptions(m.Root(), semanticcpp.BuildOptions{Context: ctx, Previous: previous, ChangedPaths: changed, Resume: resume})
	if index != nil {
		info.Build = index.Timings
		for _, file := range index.Files {
			info.Symbols += len(file.Symbols)
		}
		if disk != nil {
			// Refuse to label a build as current if the workspace changed during
			// collection. Reuse is only valid for the exact content snapshot.
			after := m.cppSnapshotOutsideDeadline(parent)
			if after.Key == info.Key {
				if saveErr := disk.store(info, index, err == nil); saveErr != nil {
					info.CacheError = saveErr.Error()
				} else {
					info.Checkpoint = err != nil
				}
				if index.WalkPending != nil {
					// A walk its deadline stopped resumes from memory on the next
					// call of this snapshot, as a stopped parse does.
					info.Partial = true
					m.mu.Lock()
					m.cppIndex, m.cppIndexInfo = index, info
					m.mu.Unlock()
				}
			} else if after.GitError != "" {
				info.CacheError = "could not verify index snapshot: " + after.GitError
				if err == nil {
					return nil, info, fmt.Errorf("verify C++ index snapshot: %s", after.GitError)
				}
			} else if err == nil {
				return nil, info, fmt.Errorf("C++ workspace changed during index build; retry against a stable tree")
			}
		}
	}
	var budgetErr *semanticcpp.BudgetError
	if errors.As(err, &budgetErr) && index != nil && index.WalkComplete && len(index.Files) > 0 {
		info.Partial, info.TotalFiles = true, len(index.Paths)
		err = nil
	}

	if err != nil {
		return nil, info, fmt.Errorf("build C++ semantic index: %w", err)
	}

	if index == nil {
		return nil, info, fmt.Errorf("no C/C++ files were indexed under the workspace")
	}

	m.mu.Lock()

	m.cppIndex = index
	m.cppIndexInfo = info
	m.mu.Unlock()

	notePartialCppIndex(parent, info)

	return index, info, nil
}

// cppSnapshotOutsideDeadline keys the index with its own timeout instead of the
// caller's deadline (see cppSnapshotTimeout); caller values still pass through.
func (m *Manager) cppSnapshotOutsideDeadline(parent context.Context) cppIndexInfo {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), cppSnapshotTimeout)
	defer cancel()

	return cppSnapshot(ctx, m.Root())
}

// cppAllPaths marks every file of an index for a content-hash check.
func cppAllPaths(index *semanticcpp.CppIndex) map[string]bool {
	paths := make(map[string]bool, len(index.Files))
	for _, file := range index.Files {
		paths[file.Path] = true
	}

	return paths
}

// resolveCppTarget loads the index and resolves req.Pattern to exactly one
// symbol owner. It returns a ready failure result when the index cannot be
// built within budget or the symbol is missing or ambiguous: tools never answer
// for a guessed symbol.
func (m *Manager) resolveCppTarget(ctx context.Context, tool string, rejected string, req ToolRequest) (*semanticcpp.CppIndex, semanticcpp.CppResolution, cppIndexInfo, string, *ToolResult) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return nil, semanticcpp.CppResolution{}, cppIndexInfo{}, "", &ToolResult{Tool: tool, Summary: rejected, Error: "pattern is required"}
	}

	index, info, err := m.loadCppIndex(ctx)
	if err != nil {
		if failure, ok := cppBudgetFailure(tool, rejected, err, info); ok {
			return nil, semanticcpp.CppResolution{}, info, "", &failure
		}

		return nil, semanticcpp.CppResolution{}, info, "", &ToolResult{Tool: tool, Summary: rejected, Error: err.Error()}
	}

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err != nil {
		return nil, semanticcpp.CppResolution{}, info, "", &ToolResult{Tool: tool, Summary: rejected, Error: err.Error()}
	}

	resolution := index.ResolveSymbol(pattern, pathFilter)
	if resolution.Status != semanticcpp.ResolutionResolved {
		failure := cppResolutionFailure(tool, rejected, resolution, info.freshness(0, false, cppAnsweredBySyntax))

		return nil, resolution, info, pathFilter, &failure
	}

	return index, resolution, info, pathFilter, nil
}

// cppPreferredDefinition picks the indexed entry a single-symbol tool acts on:
// the type itself, else a function or method with a body in a source file, else
// a function or method in a source file, else the first entry.
func cppPreferredDefinition(definitions []semanticcpp.CppSymbolMatch) semanticcpp.CppSymbolMatch {
	for _, match := range definitions {
		switch match.Kind {
		case "class", "struct", "enum", "union", "namespace":
			return match
		}
	}

	for _, match := range definitions {
		if (match.Kind == "method" || match.Kind == "func") && !cppIsHeaderPath(match.Path) && match.EndLine > match.StartLine {
			return match
		}
	}

	for _, match := range definitions {
		if (match.Kind == "method" || match.Kind == "func") && !cppIsHeaderPath(match.Path) {
			return match
		}
	}

	return definitions[0]
}

// cppResolved is the exact resolution a single-symbol tool answered from.
type cppResolved struct {
	resolution semanticcpp.CppResolution
	info       cppIndexInfo
	started    time.Time
}

// resolveCppMatch resolves req.Pattern to exactly one owner and returns the
// entry to act on, or a ready fail-closed result (ambiguous_symbol,
// symbol_not_found, cpp_index_budget_exceeded) instead of a guessed symbol.
func (m *Manager) resolveCppMatch(ctx context.Context, tool string, rejected string, req ToolRequest) (semanticcpp.CppSymbolMatch, cppResolved, *ToolResult) {
	started := time.Now()

	_, resolution, info, _, failure := m.resolveCppTarget(ctx, tool, rejected, req)
	if failure != nil {
		return semanticcpp.CppSymbolMatch{}, cppResolved{}, failure
	}

	return cppPreferredDefinition(resolution.Definitions), cppResolved{resolution: resolution, info: info, started: started}, nil
}

// annotate echoes the requested and resolved symbol and attaches the index
// identity to a successful result.
func (r cppResolved) annotate(result ToolResult) ToolResult {
	if result.Error != "" {
		return result
	}

	answeredBy := cppAnsweredBySyntax
	if strings.Contains(result.Summary, "clangd") {
		answeredBy = cppAnsweredByClangd
	}

	result.Summary = fmt.Sprintf("%s [resolved %s, requested %q]", result.Summary, r.resolution.Owner, r.resolution.Requested)
	result.Freshness = r.info.freshness(time.Since(r.started), false, answeredBy)

	return result
}

const cppCallResultLimit = 24

const cppDeclaredTypeWarning = "incoming call sites are resolved from declared types in the syntax index (parameters, locals, fields, base classes); calls whose receiver type cannot be read are listed as unresolved, not as callers"

// cppCallResult formats a call query answer with the resolved owner, the total
// found and the index identity.
func (m *Manager) cppCallResult(tool string, direction string, res semanticcpp.CppResolution, calls []semanticcpp.CppCallMatch, total int, info cppIndexInfo, started time.Time, warnings []string) ToolResult {
	subject := fmt.Sprintf("%s (requested %q)", res.Owner, res.Requested)

	lines := make([]string, 0, len(calls))

	for _, call := range calls {
		display, err := m.cppHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, call.CalleeName))
	}

	summary := fmt.Sprintf("Found %d %s C++ call(s) for %s.", total, direction, subject)
	if total > len(lines) {
		summary = fmt.Sprintf("Found %d %s C++ call(s) for %s; showing %d.", total, direction, subject, len(lines))
	}

	if total == 0 {
		summary = fmt.Sprintf("No %s C++ calls matched %s.", direction, subject)
	}

	return ToolResult{
		Tool:      tool,
		Summary:   summary,
		Output:    strings.Join(lines, "\n"),
		Freshness: info.freshness(time.Since(started), false, cppAnsweredBySyntax),
		Warnings:  warnings,
	}
}

func (m *Manager) cppOverview(ctx context.Context) ToolResult {
	started := time.Now()

	index, info, err := m.loadCppIndex(ctx)
	if err != nil {
		if failure, ok := cppBudgetFailure("cpp_overview", "C++ overview unavailable", err, info); ok {
			return failure
		}

		return ToolResult{Tool: "cpp_overview", Summary: "C++ overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)

	summary := index.Summary()
	if summary == "" {
		summary = "C++ semantic index is available, but no summary could be generated."
	}

	return ToolResult{Tool: "cpp_overview", Summary: "Built C++ semantic overview for the workspace", Output: summary, Freshness: info.freshness(time.Since(started), false, cppAnsweredBySyntax)}
}

func (m *Manager) cppEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getCppIndex(req.invocationContext)
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

func (m *Manager) cppFindSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_find_symbol", Summary: "C++ symbol search rejected", Error: "pattern is required"}
	}

	index, info, err := m.loadCppIndex(ctx)
	if err != nil {
		if failure, ok := cppBudgetFailure("cpp_find_symbol", "C++ symbol search unavailable", err, info); ok {
			return failure
		}
		return ToolResult{Tool: "cpp_find_symbol", Summary: "C++ symbol search unavailable", Error: err.Error()}
	}

	matches := filterCppSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.cppPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "cpp_find_symbol", Summary: fmt.Sprintf("No C++ symbols matched %q", pattern), Freshness: info.freshness(0, false, cppAnsweredBySyntax)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.cppHandleDisplay(match.Path)
		if err != nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, cppDisplayName(match.Qualified, match.Name)))
	}

	return ToolResult{Tool: "cpp_find_symbol", Summary: fmt.Sprintf("Found %d matching C++ symbol(s).", len(lines)), Output: strings.Join(lines, "\n"), Freshness: info.freshness(0, false, cppAnsweredBySyntax)}
}

func (m *Manager) cppReadSymbol(ctx context.Context, req ToolRequest) ToolResult {
	if err := validateSymbolPattern("cpp_read_symbol", req.Pattern); err != nil {
		return ToolResult{Tool: "cpp_read_symbol", Summary: "C++ symbol read rejected", Error: err.Error()}
	}

	match, resolved, failure := m.resolveCppMatch(ctx, "cpp_read_symbol", "C++ symbol read rejected", req)
	if failure != nil {
		if failure.Code == cppCodeNotFound {
			if result, ok := m.cppReadSymbolPathMiss(req); ok {
				return result
			}
		}

		return *failure
	}

	return resolved.annotate(m.cppReadSymbolMatch(req, match))
}

func (m *Manager) cppReadSymbolMatch(req ToolRequest, match semanticcpp.CppSymbolMatch) ToolResult {

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

	index, err := m.getCppIndex(req.invocationContext)
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

	result := m.cppFindSymbol(ctx, req)

	result.Tool = "cpp_workspace_symbol"

	if result.Error == "" && strings.HasPrefix(result.Summary, "Found ") {
		result.Summary = strings.Replace(result.Summary, "C++ symbol", "C++ workspace symbol", 1)
	}

	return result
}

func (m *Manager) cppDefinition(ctx context.Context, req ToolRequest) ToolResult {
	match, resolved, failure := m.resolveCppMatch(ctx, "cpp_definition", "C++ definition query rejected", req)
	if failure != nil {
		return *failure
	}

	return resolved.annotate(m.cppDefinitionMatch(ctx, req, match))
}

func (m *Manager) cppDefinitionMatch(ctx context.Context, req ToolRequest, match semanticcpp.CppSymbolMatch) ToolResult {

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
	match, resolved, failure := m.resolveCppMatch(ctx, "cpp_declaration", "C++ declaration query rejected", req)
	if failure != nil {
		return *failure
	}

	return resolved.annotate(m.cppDeclarationMatch(ctx, req, match))
}

func (m *Manager) cppDeclarationMatch(ctx context.Context, req ToolRequest, match semanticcpp.CppSymbolMatch) ToolResult {

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
	match, resolved, failure := m.resolveCppMatch(ctx, "cpp_implementations", "C++ implementation query rejected", req)
	if failure != nil {
		return *failure
	}

	return resolved.annotate(m.cppImplementationsMatch(ctx, req, match))
}

func (m *Manager) cppImplementationsMatch(ctx context.Context, req ToolRequest, match semanticcpp.CppSymbolMatch) ToolResult {

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
	match, resolved, failure := m.resolveCppMatch(ctx, "cpp_references", "C++ reference query rejected", req)
	if failure != nil {
		return *failure
	}

	references, _ := m.cppReferencesMatch(ctx, req, match)

	return resolved.annotate(references)
}

// cppReferenceBackend names the backend that produced reference evidence, so a
// consumer can tell clangd's index from a name-based text search, and both from
// no evidence at all.
type cppReferenceBackend int

const (
	cppReferencesNoEvidence cppReferenceBackend = iota
	cppReferencesByClangd
	cppReferencesByTextSearch
)

func (m *Manager) cppReferencesMatch(ctx context.Context, req ToolRequest, match semanticcpp.CppSymbolMatch) (ToolResult, cppReferenceBackend) {
	var warnings []string

	pathFilter, err := m.cppPathFilterForRequest(req)
	if err == nil {
		clangdCtx, cancel := context.WithTimeout(ctx, cppBudgetSetting("IDIOM_CPP_CLANGD_BUDGET", cppDefaultClangdBudget))
		output, lspErr := m.cppReferencesViaClangd(clangdCtx, match, pathFilter)
		cancel()
		switch {
		case lspErr != nil:
			warnings = append(warnings, fmt.Sprintf("clangd references unavailable (%v); answered by name-based text search", lspErr))
		case strings.TrimSpace(output) != "":
			return ToolResult{
				Tool:    "cpp_references",
				Summary: "Resolved C++ references via clangd",
				Output:  output,
			}, cppReferencesByClangd
		default:
			warnings = append(warnings, "clangd returned no references, not even the symbol's own declaration; answered by name-based text search")
		}
	}

	searchReq := ToolRequest{
		Pattern: cppReferencePattern(match.Name),
		Path:    defaultString(req.Path, "."),
		Glob:    cppSearchGlob(),
	}
	result := m.searchContent(ctx, searchReq, "cpp_references")
	result.Warnings = append(warnings, result.Warnings...)

	if result.Error != "" {
		return result, cppReferencesNoEvidence
	}

	// The symbol was resolved from a file under this scope, so a text search
	// that finds not even its definition is broken evidence. Path-name
	// suggestions in its output are not references.
	if result.Facts == nil || result.Facts.SearchHitCount == 0 {
		result.Summary = "No C++ references found by any backend"
		result.Output = ""

		return result, cppReferencesNoEvidence
	}

	result.Summary = "Resolved C++ references via name-based text search fallback"

	return result, cppReferencesByTextSearch
}

func (m *Manager) cppCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	return m.cppCalls(ctx, req, false)
}

func (m *Manager) cppCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	return m.cppCalls(ctx, req, true)
}

// cppCalls answers cpp_calls_into (incoming) and cpp_calls_from (outgoing) from
// one path: resolve the requested symbol exactly, try clangd within its own
// budget, then answer from the syntax index.
func (m *Manager) cppCalls(ctx context.Context, req ToolRequest, incoming bool) ToolResult {
	tool, direction, rejected := "cpp_calls_from", "outgoing", "C++ outgoing-call query rejected"
	if incoming {
		tool, direction, rejected = "cpp_calls_into", "incoming", "C++ incoming-call query rejected"
	}

	index, resolution, info, pathFilter, failure := m.resolveCppTarget(ctx, tool, rejected, req)
	if failure != nil {
		return *failure
	}

	started := time.Now()
	var warnings []string

	if incoming {
		warnings = append(warnings, cppDeclaredTypeWarning)
	}

	clangdBudget := cppBudgetSetting("IDIOM_CPP_CLANGD_BUDGET", cppDefaultClangdBudget)
	clangdCtx, cancel := context.WithTimeout(ctx, clangdBudget)
	// LSP shutdown can outlive its request context. Keep that cleanup off the
	// query's critical path; cancellation still terminates the owned process.
	type hierarchyAttempt struct {
		payload callHierarchyResult
		err     error
	}
	attempt := make(chan hierarchyAttempt, 1)
	go func() {
		payload, err := m.cppCallHierarchyViaClangd(clangdCtx, cppPreferredDefinition(resolution.Definitions), pathFilter, incoming)
		attempt <- hierarchyAttempt{payload: payload, err: err}
	}()
	var payload callHierarchyResult
	var lspErr error
	select {
	case result := <-attempt:
		payload, lspErr = result.payload, result.err
	case <-clangdCtx.Done():
		lspErr = clangdCtx.Err()
	}

	timedOut := clangdCtx.Err() != nil
	cancel()

	if lspErr == nil && strings.TrimSpace(payload.Output) != "" {
		return ToolResult{
			Tool:          tool,
			Summary:       fmt.Sprintf("Resolved %s C++ calls for %s (requested %q) via clangd call hierarchy", direction, resolution.Owner, resolution.Requested),
			Output:        payload.Output,
			Relationships: payload.Relationships,
			Freshness:     info.freshness(time.Since(started), false, cppAnsweredByClangd),
		}
	}

	if timedOut {
		warnings = append(warnings, fmt.Sprintf("clangd call hierarchy exceeded its %s budget; answered from the syntax index", clangdBudget))
	}

	if incoming {
		callers := index.CallsInto(resolution, pathFilter, cppCallResultLimit)

		return m.cppIncomingResult(resolution, callers, info, started, warnings)
	}

	matches, total := index.CallsFromDefinitions(resolution.Definitions, cppCallResultLimit)

	return m.cppCallResult(tool, direction, resolution, matches, total, info, started, warnings)
}

// cppIncomingResult formats resolved callers and appends the calls whose
// receiver could not be resolved as a separate, labelled section.
func (m *Manager) cppIncomingResult(res semanticcpp.CppResolution, callers semanticcpp.CppCallers, info cppIndexInfo, started time.Time, warnings []string) ToolResult {
	result := m.cppCallResult("cpp_calls_into", "incoming", res, callers.Callers, callers.Total, info, started, warnings)

	if callers.UnresolvedTotal == 0 {
		return result
	}

	lines := make([]string, 0, len(callers.Unresolved)+1)
	lines = append(lines, fmt.Sprintf("unresolved (%d call(s) to %q whose receiver type is not readable; not counted as callers):", callers.UnresolvedTotal, res.Name))

	for _, call := range callers.Unresolved {
		display, err := m.cppHandleDisplay(call.Caller.Path)
		if err != nil {
			display = call.Caller.Path
		}

		lines = append(lines, fmt.Sprintf("%s:%d -> %s [%s]", display, call.Line, call.CalleeName, call.Reason))
	}

	if callers.UnresolvedTotal > len(callers.Unresolved) {
		lines = append(lines, fmt.Sprintf("... %d more unresolved", callers.UnresolvedTotal-len(callers.Unresolved)))
	}

	result.Summary += fmt.Sprintf(" %d unresolved (receiver type unknown).", callers.UnresolvedTotal)

	if result.Output != "" {
		result.Output += "\n\n"
	}

	result.Output += strings.Join(lines, "\n")

	return result
}

func (m *Manager) cppExtractBody(ctx context.Context, req ToolRequest) ToolResult {
	match, resolved, failure := m.resolveCppMatch(ctx, "cpp_extract_body", "C++ body extraction rejected", req)
	if failure != nil {
		return *failure
	}

	return resolved.annotate(m.cppExtractBodyMatch(req, match))
}

func (m *Manager) cppExtractBodyMatch(req ToolRequest, match semanticcpp.CppSymbolMatch) ToolResult {

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

	index, err := m.getCppIndex(req.invocationContext)
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

func (m *Manager) cppWhoIncludes(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "cpp_who_includes", Summary: "C++ reverse-include query rejected", Error: "pattern is required"}
	}

	index, info, err := m.loadCppIndex(ctx)
	if err != nil {
		if failure, ok := cppBudgetFailure("cpp_who_includes", "C++ reverse-include query unavailable", err, info); ok {
			return failure
		}
		return ToolResult{Tool: "cpp_who_includes", Summary: "C++ reverse-include query unavailable", Error: err.Error()}
	}

	deps := filterRelatedPaths(index.WhoIncludes(pattern), m.cppPathFilter(req.Path))
	if len(deps) == 0 {
		return ToolResult{Tool: "cpp_who_includes", Summary: fmt.Sprintf("No C++ includers matched %q", pattern), Freshness: info.freshness(0, false, cppAnsweredBySyntax)}
	}

	lines := make([]string, 0, len(deps))
	for _, rel := range deps {
		display, err := m.cppHandleDisplay(rel)
		if err != nil {
			continue
		}

		lines = append(lines, display)
	}

	return ToolResult{Tool: "cpp_who_includes", Summary: fmt.Sprintf("Found %d direct/transitive includer file(s).", len(lines)), Output: strings.Join(lines, "\n"), Freshness: info.freshness(0, false, cppAnsweredBySyntax)}
}

func (m *Manager) cppBlastRadius(ctx context.Context, req ToolRequest) ToolResult {
	const (
		tool     = "cpp_blast_radius"
		rejected = "C++ blast-radius query rejected"
	)

	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: tool, Summary: rejected, Error: "pattern is required"}
	}

	index, info, err := m.loadCppIndex(ctx)
	if err != nil {
		if failure, ok := cppBudgetFailure(tool, rejected, err, info); ok {
			return failure
		}

		return ToolResult{Tool: tool, Summary: "C++ blast-radius query unavailable", Error: err.Error()}
	}

	started := time.Now()

	// A path pattern names files; anything else must resolve to exactly one
	// symbol, whose defining files are then the change set.
	targets := index.MatchFiles(pattern)
	subject := fmt.Sprintf("file(s) %s", strings.Join(targets, ", "))
	includeSelf := false

	if len(targets) == 0 {
		pathFilter, filterErr := m.cppPathFilterForRequest(req)
		if filterErr != nil {
			return ToolResult{Tool: tool, Summary: rejected, Error: filterErr.Error()}
		}

		resolution := index.ResolveSymbol(pattern, pathFilter)
		if resolution.Status != semanticcpp.ResolutionResolved {
			return cppResolutionFailure(tool, rejected, resolution, info.freshness(0, false, cppAnsweredBySyntax))
		}

		seen := map[string]struct{}{}

		for _, definition := range resolution.Definitions {
			if _, ok := seen[definition.Path]; !ok {
				seen[definition.Path] = struct{}{}
				targets = append(targets, definition.Path)
			}
		}

		slices.Sort(targets)

		subject = fmt.Sprintf("symbol %s (requested %q) defined in %s", resolution.Owner, resolution.Requested, strings.Join(targets, ", "))
		includeSelf = true
	}

	files := filterRelatedPaths(index.BlastRadiusOfFiles(targets, includeSelf), m.cppPathFilter(req.Path))
	freshness := info.freshness(time.Since(started), false, cppAnsweredBySyntax)

	if len(files) == 0 {
		return ToolResult{Tool: tool, Summary: fmt.Sprintf("No C++ translation units were affected by %s", subject), Freshness: freshness}
	}

	lines := make([]string, 0, len(files))
	for _, rel := range files {
		display, err := m.cppHandleDisplay(rel)
		if err != nil {
			continue
		}

		lines = append(lines, display)
	}

	return ToolResult{
		Tool:      tool,
		Summary:   fmt.Sprintf("Found %d affected C++ translation unit(s) for %s.", len(lines), subject),
		Output:    strings.Join(lines, "\n"),
		Freshness: freshness,
	}
}

func (m *Manager) cppFileOutline(req ToolRequest) ToolResult {
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "cpp_file_outline", Summary: "C++ file outline rejected", Error: err.Error()}
	}

	index, err := m.getCppIndex(req.invocationContext)
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
	index, err := m.getCppIndex(nil)
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
	index, err := m.getCppIndex(nil)
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
	index, err := m.getCppIndex(nil)
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
		// search_content appends a numbered source snippet after "context:"
		// for a single hit; snippet lines are not matches.
		if strings.HasPrefix(line, "context:") {
			break
		}

		if line == "" || strings.HasPrefix(line, "targeted edit template:") {
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
	index, err := m.getCppIndex(req.invocationContext)
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
