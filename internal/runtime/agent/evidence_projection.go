package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
)

type toolResultEvidenceProjection struct {
	results []workspace.ToolResult
}

func newToolResultEvidenceProjection(results []workspace.ToolResult) toolResultEvidenceProjection {
	return toolResultEvidenceProjection{results: append([]workspace.ToolResult(nil), results...)}
}

func (projection toolResultEvidenceProjection) ReadPaths() map[string]struct{} {
	paths := make(map[string]struct{}, len(projection.results))
	for _, result := range projection.results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, path := range ReadPathsFromToolResult(result) {
			normalized := NormalizeRunnerPath(path)
			if normalized != "" {
				paths[normalized] = struct{}{}
			}
		}
	}
	return paths
}

func (projection toolResultEvidenceProjection) SourceEvidenceForPath(path string) planThenExecuteSourceEvidence {
	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) {
		return planThenExecuteSourceEvidence{}
	}
	best := planThenExecuteSourceEvidence{Path: path}
	for _, result := range projection.results {
		evidence := planThenExecuteToolResultSourceEvidence(result, path)
		if redirectPath := implementationTargetWriteReadyRedirectedReadPath(result); redirectPath == path {
			if tool, ok := implementationTargetWriteReadyRedirectedReadTool(result); ok {
				evidence = planThenExecuteSourceEvidence{
					Path:   path,
					Level:  planThenExecuteEvidenceSourceBodyPending,
					Source: strings.TrimSpace(result.Tool),
					Detail: "redirected to " + tool,
				}
			}
		}
		if planThenExecuteEvidenceRank(evidence.Level) > planThenExecuteEvidenceRank(best.Level) {
			best = evidence
		}
	}
	return best
}

func (projection toolResultEvidenceProjection) HasSourceBody(path string) bool {
	return sourceEvidenceHasBody(projection.SourceEvidenceForPath(path))
}

func (projection toolResultEvidenceProjection) SourceBodyPaths() []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, result := range projection.results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, path := range ReadPathsFromToolResult(result) {
			normalized := NormalizeRunnerPath(path)
			if normalized == "" || IsNonSourceEvidenceArtifactPath(normalized) || !projection.HasSourceBody(normalized) {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			paths = append(paths, normalized)
		}
	}
	return paths
}

func (projection toolResultEvidenceProjection) CandidatePaths() []string {
	bodySet := make(map[string]struct{})
	for _, path := range projection.SourceBodyPaths() {
		bodySet[path] = struct{}{}
	}

	seen := make(map[string]struct{})
	var paths []string

	for _, result := range projection.results {
		for _, path := range projection.CandidatePathsFromResult(result, 128) {
			normalized := NormalizeRunnerPath(path)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			if _, ok := bodySet[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			paths = append(paths, normalized)
		}
	}

	return paths
}

func (projection toolResultEvidenceProjection) CandidatePathsFromResult(result workspace.ToolResult, max int) []string {
	if strings.TrimSpace(result.Error) != "" {
		return nil
	}

	tool := strings.TrimSpace(result.Tool)
	switch {
	case tool == workspace.ToolNameContextPack:
		return contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, max)
	case projection.DiscoveryCandidateTool(tool):
		return discoveryConsumptionCandidatesFromResult(result, max)
	case workspace.IsSearchToolName(tool):
		return sourceLikePathsFromText(result.Summary+"\n"+result.Output, max)
	default:
		return nil
	}
}

func (toolResultEvidenceProjection) DiscoveryCandidateTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	switch tool {
	case workspace.ToolNameDiscoveryQuery, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom,
		workspace.ToolNameContextImpact, workspace.ToolNameStructuralSearch:
		return true
	default:
		return strings.HasSuffix(tool, "_workspace_symbol")
	}
}

func (projection toolResultEvidenceProjection) ChangedPathSet() map[string]struct{} {
	list := projection.ChangedPathList()
	if len(list) == 0 {
		return nil
	}

	paths := make(map[string]struct{}, len(list))
	for _, path := range list {
		paths[path] = struct{}{}
	}

	return paths
}

func (projection toolResultEvidenceProjection) ChangedPathList() []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, result := range projection.results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, rawPath := range projection.ChangedPathsFromResult(result) {
			path := NormalizeRunnerPath(rawPath)
			if path == "" {
				continue
			}

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	return paths
}

func (toolResultEvidenceProjection) ChangedPathsFromResult(result workspace.ToolResult) []string {
	if len(result.ChangedPaths) > 0 {
		return append([]string(nil), result.ChangedPaths...)
	}

	switch strings.TrimSpace(result.Tool) {
	case workspace.ToolNameApplyPatch:
		return legacyChangedPathsFromPatchOutput(result.Output)
	case workspace.ToolNameDelegateEdit:
		return legacyChangedPathsFromPatchOutput(result.Output)
	default:
		return nil
	}
}

func legacyChangedPathsFromPatchOutput(output string) []string {
	var paths []string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "add "):
			paths = append(paths, legacyPatchOutputPath(strings.TrimPrefix(line, "add ")))
		case strings.HasPrefix(line, "delete "):
			paths = append(paths, legacyPatchOutputPath(strings.TrimPrefix(line, "delete ")))
		case strings.HasPrefix(line, "update+move "):
			rest := strings.TrimPrefix(line, "update+move ")
			source, destination, moved := strings.Cut(rest, " -> ")

			if moved {
				paths = append(paths, legacyPatchOutputPath(source), legacyPatchOutputPath(destination))
			} else {
				paths = append(paths, legacyPatchOutputPath(rest))
			}
		case strings.HasPrefix(line, "update "):
			paths = append(paths, legacyPatchOutputPath(strings.TrimPrefix(line, "update ")))
		}
	}

	return paths
}

func legacyPatchOutputPath(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, " ("); ok {
		value = before
	}

	return strings.TrimSpace(value)
}
