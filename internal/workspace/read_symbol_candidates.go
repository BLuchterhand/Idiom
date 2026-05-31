package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/semantic"
)

const (
	readSymbolCandidateSearchLimit = 64
	readSymbolCandidateOutputLimit = 6
)

type readSymbolCandidate struct {
	Path    string
	Line    int
	Kind    string
	Display string
}

func readSymbolPathMissResult(tool, language, pattern, requestedPath string, candidates []readSymbolCandidate) (ToolResult, bool) {
	candidates = prioritizeReadSymbolCandidates(candidates, requestedPath)
	if len(candidates) == 0 {
		return ToolResult{}, false
	}
	count := len(candidates)
	if len(candidates) > readSymbolCandidateOutputLimit {
		candidates = candidates[:readSymbolCandidateOutputLimit]
	}

	lines := []string{
		fmt.Sprintf("requested_symbol: %s", pattern),
		fmt.Sprintf("requested_path: %s", requestedPath),
		"path_filter_status: no source-body match in requested path",
		"candidate_anchor_status: anchors only; read one candidate path to inspect source body",
		"candidate_anchors:",
	}
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf("- %s:%d %s %s", candidate.Path, candidate.Line, candidate.Kind, candidate.Display))
	}
	lines = append(lines, "candidate_requests:")
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf(
			`- {"tool":"code_navigate","capability":"read_symbol","path":%q,"pattern":%q}`,
			candidate.Path,
			pattern,
		))
	}
	return ToolResult{
		Tool:    tool,
		Summary: fmt.Sprintf("%s symbol read path-filter miss; found %d candidate anchor(s) for %q", language, count, pattern),
		Output:  strings.Join(lines, "\n"),
	}, true
}

func prioritizeReadSymbolCandidates(candidates []readSymbolCandidate, requestedPath string) []readSymbolCandidate {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]readSymbolCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Path = cleanCandidatePath(candidate.Path)
		candidate.Display = strings.TrimSpace(candidate.Display)
		if candidate.Path == "" || candidate.Line <= 0 || candidate.Display == "" {
			continue
		}
		key := fmt.Sprintf("%s:%d:%s:%s", candidate.Path, candidate.Line, candidate.Kind, candidate.Display)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func cleanCandidatePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func (m *Manager) goReadSymbolPathMiss(req ToolRequest, pattern, pathFilter string, index *semantic.GoIndex) (ToolResult, bool) {
	if strings.TrimSpace(pathFilter) == "" || index == nil {
		return ToolResult{}, false
	}
	candidates := make([]readSymbolCandidate, 0, readSymbolCandidateOutputLimit)
	for _, match := range index.FindSymbols(pattern, readSymbolCandidateSearchLimit) {
		if pathMatchesFilter(match.Path, pathFilter) {
			continue
		}
		candidates = append(candidates, readSymbolCandidate{Path: match.Path, Line: match.Line, Kind: match.Kind, Display: goQualifiedSymbol(match)})
	}
	return readSymbolPathMissResult("go_read_symbol", "Go", pattern, pathFilter, candidates)
}

func (m *Manager) pythonReadSymbolPathMiss(req ToolRequest) (ToolResult, bool) {
	pattern := strings.TrimSpace(req.Pattern)
	pathFilter, err := m.pythonPathFilterForRequest(req)
	if err != nil || pathFilter == "" {
		return ToolResult{}, false
	}
	index, err := m.getPythonIndex()
	if err != nil {
		return ToolResult{}, false
	}
	candidates := make([]readSymbolCandidate, 0, readSymbolCandidateOutputLimit)
	for _, match := range index.FindSymbols(pattern, readSymbolCandidateSearchLimit) {
		if pathMatchesFilter(match.Path, pathFilter) {
			continue
		}
		candidates = append(candidates, readSymbolCandidate{Path: match.Path, Line: match.Line, Kind: match.Kind, Display: pythonDisplayName(match.Qualified, match.Name)})
	}
	return readSymbolPathMissResult("python_read_symbol", "Python", pattern, pathFilter, candidates)
}

func (m *Manager) cppReadSymbolPathMiss(req ToolRequest) (ToolResult, bool) {
	pattern := strings.TrimSpace(req.Pattern)
	pathFilter, err := m.cppPathFilterForRequest(req)
	if err != nil || pathFilter == "" {
		return ToolResult{}, false
	}
	index, err := m.getCppIndex()
	if err != nil {
		return ToolResult{}, false
	}
	candidates := make([]readSymbolCandidate, 0, readSymbolCandidateOutputLimit)
	for _, match := range index.FindSymbols(pattern, readSymbolCandidateSearchLimit) {
		if pathMatchesFilter(match.Path, pathFilter) {
			continue
		}
		candidates = append(candidates, readSymbolCandidate{Path: match.Path, Line: match.Line, Kind: match.Kind, Display: cppDisplayName(match.Qualified, match.Name)})
	}
	return readSymbolPathMissResult("cpp_read_symbol", "C++", pattern, pathFilter, candidates)
}

func (m *Manager) tsReadSymbolPathMiss(req ToolRequest) (ToolResult, bool) {
	pattern := strings.TrimSpace(req.Pattern)
	pathFilter, err := m.tsPathFilterForRequest(req)
	if err != nil || pathFilter == "" {
		return ToolResult{}, false
	}
	index, err := m.getTsIndex()
	if err != nil {
		return ToolResult{}, false
	}
	candidates := make([]readSymbolCandidate, 0, readSymbolCandidateOutputLimit)
	for _, match := range index.FindSymbols(pattern, readSymbolCandidateSearchLimit) {
		if pathMatchesFilter(match.Path, pathFilter) {
			continue
		}
		candidates = append(candidates, readSymbolCandidate{Path: match.Path, Line: match.Line, Kind: match.Kind, Display: tsDisplayName(match.Qualified, match.Name)})
	}
	return readSymbolPathMissResult("ts_read_symbol", "TypeScript", pattern, pathFilter, candidates)
}

func (m *Manager) rustReadSymbolPathMiss(req ToolRequest) (ToolResult, bool) {
	pattern := strings.TrimSpace(req.Pattern)
	pathFilter, err := m.rustPathFilterForRequest(req)
	if err != nil || pathFilter == "" {
		return ToolResult{}, false
	}
	index, err := m.getRustIndex()
	if err != nil {
		return ToolResult{}, false
	}
	candidates := make([]readSymbolCandidate, 0, readSymbolCandidateOutputLimit)
	for _, match := range index.FindSymbols(pattern, readSymbolCandidateSearchLimit) {
		if pathMatchesFilter(match.Path, pathFilter) {
			continue
		}
		candidates = append(candidates, readSymbolCandidate{Path: match.Path, Line: match.Line, Kind: match.Kind, Display: rustDisplayName(match.Qualified, match.Name)})
	}
	return readSymbolPathMissResult("rust_read_symbol", "Rust", pattern, pathFilter, candidates)
}

func (m *Manager) javaReadSymbolPathMiss(req ToolRequest) (ToolResult, bool) {
	pattern := strings.TrimSpace(req.Pattern)
	pathFilter, err := m.javaPathFilterForRequest(req)
	if err != nil || pathFilter == "" {
		return ToolResult{}, false
	}
	index, err := m.getJavaIndex()
	if err != nil {
		return ToolResult{}, false
	}
	candidates := make([]readSymbolCandidate, 0, readSymbolCandidateOutputLimit)
	for _, match := range index.FindSymbols(pattern, readSymbolCandidateSearchLimit) {
		if pathMatchesFilter(match.Path, pathFilter) {
			continue
		}
		candidates = append(candidates, readSymbolCandidate{Path: match.Path, Line: match.Line, Kind: match.Kind, Display: javaDisplayName(match.Qualified, match.Name)})
	}
	return readSymbolPathMissResult("java_read_symbol", "Java", pattern, pathFilter, candidates)
}
