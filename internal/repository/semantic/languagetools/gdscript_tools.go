package languagetools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/platform/safepath"
	semanticgdscript "idiom/internal/repository/semantic/gdscript"
)

func (m *Manager) gdscriptReferences(_ context.Context, req ToolRequest) ToolResult {
	if strings.TrimSpace(req.Pattern) == "" {
		return ToolResult{Tool: "gdscript_references", Summary: "GDScript reference query rejected", Error: "pattern is required"}
	}
	index, err := m.gdscriptIndex()
	if err != nil {
		return ToolResult{Tool: "gdscript_references", Summary: "GDScript reference query unavailable", Error: err.Error()}
	}
	targetPath, err := m.gdscriptTargetPath(req.Path)
	if err != nil {
		return ToolResult{Tool: "gdscript_references", Summary: "GDScript reference query rejected", Error: err.Error()}
	}
	results := index.FindReferences(req.Pattern, targetPath, goResultLimit(req.MaxDepth, 50))
	lines := make([]string, 0, len(results))
	for _, reference := range results {
		line := fmt.Sprintf("%s:%d %s %s", reference.Path, reference.Line, reference.Kind, reference.Name)
		if reference.TargetPath != "" {
			line += " -> " + reference.TargetPath
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ToolResult{Tool: "gdscript_references", Summary: fmt.Sprintf("No GDScript references matched %q", req.Pattern), PrecisionTier: PrecisionTierSyntaxIndex}
	}
	return ToolResult{Tool: "gdscript_references", Summary: fmt.Sprintf("Found %d GDScript reference(s).", len(lines)), Output: strings.Join(lines, "\n"), PrecisionTier: PrecisionTierSyntaxIndex}
}

func (m *Manager) gdscriptCallsInto(_ context.Context, req ToolRequest) ToolResult {
	if strings.TrimSpace(req.Pattern) == "" {
		return ToolResult{Tool: "gdscript_calls_into", Summary: "GDScript calls-into query rejected", Error: "pattern is required"}
	}
	index, err := m.gdscriptIndex()
	if err != nil {
		return ToolResult{Tool: "gdscript_calls_into", Summary: "GDScript calls-into query unavailable", Error: err.Error()}
	}
	targetPath, err := m.gdscriptTargetPath(req.Path)
	if err != nil {
		return ToolResult{Tool: "gdscript_calls_into", Summary: "GDScript calls-into query rejected", Error: err.Error()}
	}
	calls := index.FindCallsInto(req.Pattern, targetPath, goResultLimit(req.MaxDepth, 50))
	lines := make([]string, 0, len(calls.Resolved)+len(calls.UnresolvedCandidates)+1)
	for _, call := range calls.Resolved {
		qualifier := ""
		if call.Qualifier != "" {
			qualifier = call.Qualifier + "."
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s -> %s%s (%s)", call.Caller.Path, call.Line, call.Caller.Qualified, qualifier, call.Target, call.Kind))
	}
	if len(calls.UnresolvedCandidates) > 0 {
		lines = append(lines, "Unresolved candidates:")
		for _, call := range calls.UnresolvedCandidates {
			qualifier := ""
			if call.Qualifier != "" {
				qualifier = call.Qualifier + "."
			}
			lines = append(lines, fmt.Sprintf("%s:%d %s -> %s%s (%s)", call.Caller.Path, call.Line, call.Caller.Qualified, qualifier, call.Target, call.Kind))
		}
	}
	if len(lines) == 0 {
		return ToolResult{Tool: "gdscript_calls_into", Summary: fmt.Sprintf("No GDScript callers matched %q", req.Pattern), PrecisionTier: PrecisionTierSyntaxIndex}
	}
	return ToolResult{Tool: "gdscript_calls_into", Summary: fmt.Sprintf("Found %d GDScript caller(s).", len(lines)), Output: strings.Join(lines, "\n"), PrecisionTier: PrecisionTierSyntaxIndex}
}

func (m *Manager) gdscriptIndex() (*semanticgdscript.Index, error) {
	if !m.Enabled() {
		return nil, fmt.Errorf("GDScript tools require an enabled workspace")
	}
	index, err := semanticgdscript.BuildIndex(m.Root())
	if err != nil {
		return nil, fmt.Errorf("build GDScript syntax index: %w", err)
	}
	if len(index.Files) == 0 {
		return nil, fmt.Errorf("no GDScript files were indexed under the workspace")
	}
	return index, nil
}

func (m *Manager) gdscriptTargetPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return "", nil
	}
	path = safepath.CleanPath(path)
	if filepath.IsAbs(path) {
		root, err := filepath.Abs(m.Root())
		if err != nil {
			return "", fmt.Errorf("resolve GDScript workspace root: %w", err)
		}
		rel, ok := safepath.Rel(root, path)
		if !ok {
			return "", fmt.Errorf("GDScript target path must stay under workspace root")
		}
		return rel, nil
	}
	rel := filepath.ToSlash(filepath.Clean(path))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("GDScript target path must stay under workspace root")
	}
	return rel, nil
}
