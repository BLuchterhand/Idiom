package workspace

import (
	"context"
	"fmt"
	"path/filepath"

	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
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

func (h languageToolHost) MaxDirectoryEntries() int {
	if h.manager == nil {
		return 0
	}

	return h.manager.cfg.MaxDirectoryEntries
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

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) ResolvePath(path string, workdir string) (string, error) {
	if h.manager == nil {
		return "", fmt.Errorf("workspace root is not configured")
	}

	return h.manager.pathAccess.ResolvePath(path, workdir)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) DisplayPath(path string) string {
	if h.manager == nil {
		return ""
	}

	return h.manager.pathAccess.DisplayPath(path)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) HandleDisplay(abs string) string {
	return h.manager.handleDisplay(abs)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) RememberPath(abs string) {
	h.manager.rememberPath(abs)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) PrimeDiscoveredPaths(paths []string) {
	h.manager.PrimeDiscoveredPaths(paths)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) PathForHandle(handle string) (string, error) {
	return h.manager.pathTracker.PathForHandle(handle)
}

func (h languageToolHost) Execute(ctx context.Context, req languagetools.ToolRequest) languagetools.ToolResult {
	return toLanguageToolResult(h.manager.Execute(ctx, fromLanguageToolRequest(req)))
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) RunCommandDetailedWithEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error) {
	return h.manager.runCommandDetailedWithEnv(ctx, dir, extraEnv, name, args...)
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

	lang, ok := semanticlanguage.LanguageForName(name)

	return ok && lang.Detect != nil && lang.Detect(m.cfg.Root)
}

type codeNavigationSpec struct {
	Name            string
	Capability      Capability
	RequiresPattern bool
	RequiresPath    bool
	Cost            string
	Class           string
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

func (m *Manager) setGoplsBinaryForTesting(path string) {
	if m.languageTools == nil {
		m.languageTools = languagetools.NewManager(languageToolHost{manager: m})
	}

	m.languageTools.SetGoplsBinaryForTesting(path)
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
