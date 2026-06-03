package workspace

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/semantic"
	"idiom/internal/semantic/languagetools"
)

type languageToolHost struct {
	manager *Manager
}

func (h languageToolHost) Enabled() bool {
	return h.manager != nil && h.manager.Enabled()
}

func (h languageToolHost) Root() string {
	if h.manager == nil {
		return ""
	}
	return h.manager.cfg.Root
}

func (h languageToolHost) CWD() string {
	if h.manager == nil {
		return ""
	}
	return h.manager.cfg.CWD
}

func (h languageToolHost) MaxDirectoryEntries() int {
	if h.manager == nil {
		return 0
	}
	return h.manager.cfg.MaxDirectoryEntries
}

func (h languageToolHost) MaxFileBytes() int64 {
	if h.manager == nil {
		return 0
	}
	return int64(h.manager.cfg.MaxFileBytes)
}

func (h languageToolHost) MaxOutputBytes() int {
	if h.manager == nil {
		return 0
	}
	return h.manager.cfg.MaxOutputBytes
}

func (h languageToolHost) WritesEnabled() bool {
	return h.manager != nil && h.manager.cfg.EnableWrites
}

func (h languageToolHost) ResolvePath(path string, workdir string) (string, error) {
	return h.manager.resolvePath(path, workdir)
}

func (h languageToolHost) DisplayPath(path string) string {
	return h.manager.displayPath(path)
}

func (h languageToolHost) HandleDisplay(abs string) string {
	return h.manager.handleDisplay(abs)
}

func (h languageToolHost) RememberPath(abs string) {
	h.manager.rememberPath(abs)
}

func (h languageToolHost) PrimeDiscoveredPaths(paths []string) {
	h.manager.PrimeDiscoveredPaths(paths)
}

func (h languageToolHost) PathForHandle(handle string) (string, error) {
	return h.manager.pathForHandle(handle)
}

func (h languageToolHost) ReadFileLines(path string, start int, end int) (string, error) {
	return h.manager.readFileLines(path, start, end)
}

func (h languageToolHost) Execute(ctx context.Context, req languagetools.ToolRequest) languagetools.ToolResult {
	return toLanguageToolResult(h.manager.Execute(ctx, fromLanguageToolRequest(req)))
}

func (h languageToolHost) RunCommandDetailed(ctx context.Context, dir string, name string, args ...string) (string, string, error) {
	return h.manager.runCommandDetailed(ctx, dir, name, args...)
}

func (h languageToolHost) RunCommandDetailedWithEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error) {
	return h.manager.runCommandDetailedWithEnv(ctx, dir, extraEnv, name, args...)
}

func (h languageToolHost) CurrentIndexToken() string {
	return h.manager.currentWorkspaceIndexToken()
}

func (m *Manager) executeLanguageTool(ctx context.Context, req ToolRequest) ToolResult {
	if m.languageTools == nil {
		m.languageTools = languagetools.NewManager(languageToolHost{manager: m})
	}
	return fromLanguageToolResult(m.languageTools.Dispatch(ctx, toLanguageToolRequest(req)))
}

func (m *Manager) resetLanguageTools() {
	m.languageTools = languagetools.NewManager(languageToolHost{manager: m})
}

func (m *Manager) invalidateGoIndex() {
	m.resetLanguageTools()
}

func (m *Manager) invalidateCppIndex() {
	m.resetLanguageTools()
}

func (m *Manager) isCppWorkspace() bool {
	return m.languageDetects("cpp")
}

func (m *Manager) isGoWorkspace() bool {
	return m.languageDetects("go")
}

func (m *Manager) isPythonWorkspace() bool {
	return m.languageDetects("python")
}

func (m *Manager) isTsWorkspace() bool {
	return m.languageDetects("typescript")
}

func (m *Manager) isRustWorkspace() bool {
	return m.languageDetects("rust")
}

func (m *Manager) isJavaWorkspace() bool {
	return m.languageDetects("java")
}

func (m *Manager) languageDetects(name string) bool {
	if !m.Enabled() {
		return false
	}
	lang, ok := LanguageForName(name)
	return ok && lang.Detect != nil && lang.Detect(m.cfg.Root)
}

func validateSymbolPattern(tool, pattern string) error {
	return languagetools.ValidateSymbolPattern(tool, pattern)
}

func (m *Manager) currentWorkspaceIndexToken() string {
	if !m.Enabled() {
		return ""
	}
	head, err := exec.Command("git", "-C", m.cfg.Root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	status, err := exec.Command("git", "-C", m.cfg.Root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(head)) + "\n" + strings.TrimSpace(string(status))
}

type codeNavigationSpec struct {
	Name            string
	Capability      Capability
	RequiresPattern bool
	RequiresPath    bool
	Cost            string
	Class           string
}

type LSPPosition = languagetools.LSPPosition
type LSPRange = languagetools.LSPRange
type LSPLocation = languagetools.LSPLocation
type LSPLocationLink = languagetools.LSPLocationLink
type LSPSymbolInfo = languagetools.LSPSymbolInfo
type LSPCallHierarchyItem = languagetools.LSPCallHierarchyItem
type LSPIncomingCall = languagetools.LSPIncomingCall
type LSPOutgoingCall = languagetools.LSPOutgoingCall

func parseLSPLocations(raw json.RawMessage, serverName string) ([]LSPLocation, error) {
	return languagetools.ParseLSPLocations(raw, serverName)
}

func pylspFileURI(path string) string {
	return languagetools.PylspFileURI(path)
}

func codeNavigationSpecFor(value string) (codeNavigationSpec, error) {
	spec, err := languagetools.CodeNavigationSpecFor(value)
	if err != nil {
		return codeNavigationSpec{}, err
	}
	return codeNavigationSpec{
		Name:            spec.Name,
		Capability:      Capability(spec.Capability),
		RequiresPattern: spec.RequiresPattern,
		RequiresPath:    spec.RequiresPath,
		Cost:            spec.Cost,
		Class:           spec.Class,
	}, nil
}

func goReadSymbolWindow(match semantic.SymbolMatch, requestedStart, requestedEnd int) (int, int) {
	return languagetools.GoReadSymbolWindow(match, requestedStart, requestedEnd)
}

func (m *Manager) setGoplsBinaryForTesting(path string) {
	if m.languageTools == nil {
		m.languageTools = languagetools.NewManager(languageToolHost{manager: m})
	}
	m.languageTools.SetGoplsBinaryForTesting(path)
}

func (m *Manager) formatPylspOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	if m.languageTools == nil {
		m.languageTools = languagetools.NewManager(languageToolHost{manager: m})
	}
	result := m.languageTools.FormatPylspOutgoingCalls(callerPath, callerName, calls, pathFilter)
	return callHierarchyResult{
		Output:        result.Output,
		Relationships: fromLanguageToolRelationships(result.Relationships),
	}
}

func (m *Manager) dispatchLanguageTool(ctx context.Context, tool string, req ToolRequest) ToolResult {
	req.Tool = tool
	return m.executeLanguageTool(ctx, req)
}

func (m *Manager) cppOverview() ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_overview", ToolRequest{})
}

func (m *Manager) cppEntrypoints(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_entrypoints", req)
}

func (m *Manager) cppFindSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_find_symbol", req)
}

func (m *Manager) cppWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "cpp_workspace_symbol", req)
}

func (m *Manager) cppReadSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_read_symbol", req)
}

func (m *Manager) cppRelatedFiles(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_related_files", req)
}

func (m *Manager) cppDefinition(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "cpp_definition", req)
}

func (m *Manager) cppDeclaration(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "cpp_declaration", req)
}

func (m *Manager) cppImplementations(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "cpp_implementations", req)
}

func (m *Manager) cppReferences(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "cpp_references", req)
}

func (m *Manager) cppCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "cpp_calls_from", req)
}

func (m *Manager) cppCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "cpp_calls_into", req)
}

func (m *Manager) cppExtractBody(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_extract_body", req)
}

func (m *Manager) cppBlastRadius(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_blast_radius", req)
}

func (m *Manager) cppFileOutline(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "cpp_file_outline", req)
}

func (m *Manager) pythonOverview() ToolResult {
	return m.dispatchLanguageTool(context.Background(), "python_overview", ToolRequest{})
}

func (m *Manager) pythonEntrypoints(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "python_entrypoints", req)
}

func (m *Manager) pythonFindSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "python_find_symbol", req)
}

func (m *Manager) pythonWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "python_workspace_symbol", req)
}

func (m *Manager) pythonReadSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "python_read_symbol", req)
}

func (m *Manager) pythonRelatedFiles(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "python_related_files", req)
}

func (m *Manager) pythonDefinition(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "python_definition", req)
}

func (m *Manager) pythonImplementations(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "python_implementations", req)
}

func (m *Manager) pythonReferences(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "python_references", req)
}

func (m *Manager) pythonCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "python_calls_from", req)
}

func (m *Manager) pythonCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "python_calls_into", req)
}

func (m *Manager) pythonFileOutline(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "python_file_outline", req)
}

func (m *Manager) pythonDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "python_diagnostics", req)
}

func (m *Manager) tsOverview() ToolResult {
	return m.dispatchLanguageTool(context.Background(), "ts_overview", ToolRequest{})
}

func (m *Manager) tsEntrypoints(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "ts_entrypoints", req)
}

func (m *Manager) tsFindSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "ts_find_symbol", req)
}

func (m *Manager) tsWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "ts_workspace_symbol", req)
}

func (m *Manager) tsReadSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "ts_read_symbol", req)
}

func (m *Manager) tsRelatedFiles(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "ts_related_files", req)
}

func (m *Manager) tsDefinition(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "ts_definition", req)
}

func (m *Manager) tsImplementations(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "ts_implementations", req)
}

func (m *Manager) tsReferences(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "ts_references", req)
}

func (m *Manager) tsCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "ts_calls_from", req)
}

func (m *Manager) tsCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "ts_calls_into", req)
}

func (m *Manager) tsFileOutline(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "ts_file_outline", req)
}

func (m *Manager) rustOverview() ToolResult {
	return m.dispatchLanguageTool(context.Background(), "rust_overview", ToolRequest{})
}

func (m *Manager) rustEntrypoints(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "rust_entrypoints", req)
}

func (m *Manager) rustFindSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "rust_find_symbol", req)
}

func (m *Manager) rustWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "rust_workspace_symbol", req)
}

func (m *Manager) rustReadSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "rust_read_symbol", req)
}

func (m *Manager) rustRelatedFiles(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "rust_related_files", req)
}

func (m *Manager) rustDefinition(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "rust_definition", req)
}

func (m *Manager) rustImplementations(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "rust_implementations", req)
}

func (m *Manager) rustReferences(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "rust_references", req)
}

func (m *Manager) rustCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "rust_calls_from", req)
}

func (m *Manager) rustCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "rust_calls_into", req)
}

func (m *Manager) rustFileOutline(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "rust_file_outline", req)
}

func (m *Manager) rustDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "rust_diagnostics", req)
}

func (m *Manager) ExecuteRustTool(ctx context.Context, req ToolRequest) ToolResult {
	return m.executeLanguageTool(ctx, req)
}

func (m *Manager) javaOverview() ToolResult {
	return m.dispatchLanguageTool(context.Background(), "java_overview", ToolRequest{})
}

func (m *Manager) javaEntrypoints(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "java_entrypoints", req)
}

func (m *Manager) javaFindSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "java_find_symbol", req)
}

func (m *Manager) javaWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "java_workspace_symbol", req)
}

func (m *Manager) javaReadSymbol(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "java_read_symbol", req)
}

func (m *Manager) javaRelatedFiles(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "java_related_files", req)
}

func (m *Manager) javaDefinition(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "java_definition", req)
}

func (m *Manager) javaImplementations(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "java_implementations", req)
}

func (m *Manager) javaReferences(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "java_references", req)
}

func (m *Manager) javaCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "java_calls_from", req)
}

func (m *Manager) javaCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "java_calls_into", req)
}

func (m *Manager) javaFileOutline(req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(context.Background(), "java_file_outline", req)
}

func (m *Manager) javaDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	return m.dispatchLanguageTool(ctx, "java_diagnostics", req)
}

func (m *Manager) goDiagnosticTargets(req ToolRequest) ([]string, error) {
	if m.languageTools == nil {
		m.languageTools = languagetools.NewManager(languageToolHost{manager: m})
	}
	return m.languageTools.GoDiagnosticTargets(toLanguageToolRequest(req))
}

func toLanguageToolRequest(req ToolRequest) languagetools.ToolRequest {
	moves := make([]languagetools.GoDeclarationMove, 0, len(req.Moves))
	for _, move := range req.Moves {
		moves = append(moves, languagetools.GoDeclarationMove{
			Destination:  move.Destination,
			Pattern:      move.Pattern,
			Declarations: append([]string(nil), move.Declarations...),
		})
	}
	return languagetools.ToolRequest{
		Tool:              req.Tool,
		ID:                req.ID,
		Key:               req.Key,
		Handle:            req.Handle,
		Path:              req.Path,
		Root:              req.Root,
		EffortRoot:        req.EffortRoot,
		StartLine:         req.StartLine,
		EndLine:           req.EndLine,
		Pattern:           req.Pattern,
		Query:             req.Query,
		Status:            req.Status,
		Symbol:            req.Symbol,
		Task:              req.Task,
		Purpose:           req.Purpose,
		Capability:        req.Capability,
		Operation:         req.Operation,
		Action:            req.Action,
		Language:          req.Language,
		Glob:              req.Glob,
		Workdir:           req.Workdir,
		Command:           req.Command,
		Prompt:            req.Prompt,
		Agent:             req.Agent,
		Mode:              req.Mode,
		Content:           req.Content,
		OldText:           req.OldText,
		NewText:           req.NewText,
		ReplaceAll:        req.ReplaceAll,
		Recursive:         req.Recursive,
		MaxDepth:          req.MaxDepth,
		Limit:             req.Limit,
		BudgetTokens:      req.BudgetTokens,
		Resolution:        req.Resolution,
		IncludeEvidence:   req.IncludeEvidence,
		IncludeSource:     req.IncludeSource,
		IncludeFiles:      req.IncludeFiles,
		Refresh:           req.Refresh,
		OperatorView:      req.OperatorView,
		FreshnessPolicy:   req.FreshnessPolicy,
		FreshnessMaxFiles: req.FreshnessMaxFiles,
		Category:          req.Category,
		Component:         req.Component,
		Expected:          req.Expected,
		Observed:          req.Observed,
		Workaround:        req.Workaround,
		Impact:            req.Impact,
		Evidence:          append([]string(nil), req.Evidence...),
		Confidence:        req.Confidence,
		Severity:          req.Severity,
		SuggestedFix:      req.SuggestedFix,
		SessionID:         req.SessionID,
		FailureID:         req.FailureID,
		PacketPath:        req.PacketPath,
		OutputPath:        req.OutputPath,
		RequiredTests:     append([]string(nil), req.RequiredTests...),
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
		ForbiddenPaths:    append([]string(nil), req.ForbiddenPaths...),
		Destination:       req.Destination,
		Declarations:      append([]string(nil), req.Declarations...),
		Moves:             moves,
		Description:       req.Description,
		TaskID:            req.TaskID,
	}
}

func fromLanguageToolRequest(req languagetools.ToolRequest) ToolRequest {
	return ToolRequest{
		Tool:        req.Tool,
		Path:        filepath.ToSlash(req.Path),
		Workdir:     req.Workdir,
		Pattern:     req.Pattern,
		Query:       req.Query,
		Symbol:      req.Symbol,
		Glob:        req.Glob,
		MaxDepth:    req.MaxDepth,
		StartLine:   req.StartLine,
		EndLine:     req.EndLine,
		Capability:  req.Capability,
		Command:     req.Command,
		Recursive:   req.Recursive,
		Limit:       req.Limit,
		Description: req.Description,
	}
}

func fromLanguageToolResult(result languagetools.ToolResult) ToolResult {
	return ToolResult{
		Tool:          result.Tool,
		Code:          result.Code,
		Summary:       result.Summary,
		Output:        result.Output,
		Error:         result.Error,
		Failure:       result.Failure,
		PrecisionTier: result.PrecisionTier,
		Relationships: fromLanguageToolRelationships(result.Relationships),
		PayloadType:   result.PayloadType,
		Payload:       result.Payload,
	}
}

func toLanguageToolResult(result ToolResult) languagetools.ToolResult {
	return languagetools.ToolResult{
		Tool:          result.Tool,
		Code:          result.Code,
		Summary:       result.Summary,
		Output:        result.Output,
		Error:         result.Error,
		Failure:       result.Failure,
		PrecisionTier: result.PrecisionTier,
		Relationships: toLanguageToolRelationships(result.Relationships),
		PayloadType:   result.PayloadType,
		Payload:       result.Payload,
	}
}

func fromLanguageToolRelationships(in []languagetools.ToolRelationship) []ToolRelationship {
	out := make([]ToolRelationship, 0, len(in))
	for _, rel := range in {
		out = append(out, ToolRelationship{
			Kind:         rel.Kind,
			SourcePath:   rel.SourcePath,
			SourceLine:   rel.SourceLine,
			SourceSymbol: rel.SourceSymbol,
			TargetPath:   rel.TargetPath,
			TargetLine:   rel.TargetLine,
			TargetSymbol: rel.TargetSymbol,
			Evidence:     rel.Evidence,
		})
	}
	return out
}

func toLanguageToolRelationships(in []ToolRelationship) []languagetools.ToolRelationship {
	out := make([]languagetools.ToolRelationship, 0, len(in))
	for _, rel := range in {
		out = append(out, languagetools.ToolRelationship{
			Kind:         rel.Kind,
			SourcePath:   rel.SourcePath,
			SourceLine:   rel.SourceLine,
			SourceSymbol: rel.SourceSymbol,
			TargetPath:   rel.TargetPath,
			TargetLine:   rel.TargetLine,
			TargetSymbol: rel.TargetSymbol,
			Evidence:     rel.Evidence,
		})
	}
	return out
}
