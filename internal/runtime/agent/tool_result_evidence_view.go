package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent/agentutil"
)

type toolResultEvidenceView struct {
	results []workspace.ToolResult
}

func newToolResultEvidenceView(results []workspace.ToolResult) toolResultEvidenceView {
	return toolResultEvidenceView{results: append([]workspace.ToolResult(nil), results...)}
}

func (view toolResultEvidenceView) ReadPaths() map[string]struct{} {
	paths := make(map[string]struct{}, len(view.results))
	for _, result := range view.results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, path := range agentutil.ReadPathsFromToolResult(result) {
			normalized := agentutil.NormalizeRunnerPath(path)
			paths[normalized] = struct{}{}
		}
	}

	return paths
}

func (view toolResultEvidenceView) SourceEvidenceForPath(path string) planThenExecuteSourceEvidence {
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" || agentutil.IsNonSourceEvidenceArtifactPath(path) {
		return planThenExecuteSourceEvidence{}
	}

	best := planThenExecuteSourceEvidence{Path: path}
	for _, result := range view.results {
		evidence := planThenExecuteToolResultSourceEvidence(result, path)
		if redirectPath := implementationTargetWriteReadyRedirectedReadPath(result); redirectPath == path {
			if tool, ok := implementationTargetWriteReadyRedirectedReadTool(result); ok {
				evidence = planThenExecuteSourceEvidence{
					Path:   path,
					Level:  sourceEvidenceSourceBodyPending,
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

func (view toolResultEvidenceView) HasSourceBody(path string) bool {
	return sourceEvidenceHasBody(view.SourceEvidenceForPath(path))
}

func (view toolResultEvidenceView) SourceBodyPaths() []string {
	seen := make(map[string]struct{})

	var paths []string

	for _, result := range view.results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, path := range agentutil.ReadPathsFromToolResult(result) {
			normalized := agentutil.NormalizeRunnerPath(path)
			if normalized == "" {
				continue
			}

			if agentutil.IsNonSourceEvidenceArtifactPath(normalized) {
				continue
			}

			if !view.HasSourceBody(normalized) {
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
