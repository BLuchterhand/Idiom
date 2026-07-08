package workspace

import (
	"context"
	"fmt"

	"idiom/internal/platform/commandexec"
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

func (h languageToolHost) ResolvePath(path string, workdir string) (string, error) {
	if h.manager == nil {
		return "", fmt.Errorf("workspace root is not configured")
	}

	return h.manager.pathAccess.ResolvePath(path, workdir)
}

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

func (h languageToolHost) RouteLanguageTool(ctx context.Context, req languagetools.RouteLanguageToolRequest) languagetools.ToolResult {
	result := h.manager.Execute(ctx, ToolRequest{
		Tool:      req.Tool,
		Path:      req.Path,
		Workdir:   req.Workdir,
		Pattern:   req.Pattern,
		StartLine: req.StartLine,
		EndLine:   req.EndLine,
		MaxDepth:  req.MaxDepth,
		Recursive: req.Recursive,
		Limit:     req.Limit,
	})

	return toLanguageToolResult(result)
}

func (h languageToolHost) SearchContent(ctx context.Context, req languagetools.SearchContentRequest) languagetools.ToolResult {
	result := h.manager.Execute(ctx, ToolRequest{
		Tool:    ToolNameSearchContent,
		Pattern: req.Pattern,
		Path:    req.Path,
		Glob:    req.Glob,
		Workdir: req.Workdir,
	})

	return languagetools.ToolResult{
		Tool:    result.Tool,
		Summary: result.Summary,
		Output:  result.Output,
		Error:   result.Error,
		Failure: result.Failure,
	}
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) RunCommand(ctx context.Context, dir string, extraEnv []string, name string, args ...string) commandexec.Result {
	return h.manager.runCommandResult(ctx, commandexec.Spec{Mode: commandexec.ModeArgv, Name: name, Args: args, Dir: dir, Env: extraEnv})
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

func (m *Manager) languageDetects(name string) bool {
	if !m.Enabled() {
		return false
	}

	lang, ok := semanticlanguage.LanguageForName(name)

	return ok && lang.Detect != nil && lang.Detect(m.cfg.Root)
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
		Tool:         req.Tool,
		ID:           req.ID,
		Path:         req.Path,
		Workdir:      req.Workdir,
		StartLine:    req.StartLine,
		EndLine:      req.EndLine,
		Pattern:      req.Pattern,
		Capability:   req.Capability,
		Language:     req.Language,
		Resolution:   req.Resolution,
		Glob:         req.Glob,
		MaxDepth:     req.MaxDepth,
		Limit:        req.Limit,
		Recursive:    req.Recursive,
		Destination:  req.Destination,
		Declarations: append([]string(nil), req.Declarations...),
		Moves:        moves,
	}
}

func fromLanguageToolResult(result languagetools.ToolResult) ToolResult {
	return ToolResult{
		Tool:          result.Tool,
		Summary:       result.Summary,
		Output:        result.Output,
		Error:         result.Error,
		Failure:       result.Failure,
		Paths:         append([]string(nil), result.Paths...),
		ChangedPaths:  append([]string(nil), result.ChangedPaths...),
		Facts:         result.Facts.Clone(),
		PrecisionTier: result.PrecisionTier,
		Relationships: fromLanguageToolRelationships(result.Relationships),
	}
}

func toLanguageToolResult(result ToolResult) languagetools.ToolResult {
	return languagetools.ToolResult{
		Tool:          result.Tool,
		Summary:       result.Summary,
		Output:        result.Output,
		Error:         result.Error,
		Failure:       result.Failure,
		Paths:         append([]string(nil), result.Paths...),
		ChangedPaths:  append([]string(nil), result.ChangedPaths...),
		Facts:         result.Facts.Clone(),
		PrecisionTier: result.PrecisionTier,
		Relationships: toLanguageToolRelationships(result.Relationships),
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
