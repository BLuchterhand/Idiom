package agent

import (
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/textutil"
)

type toolResultEvidenceProjection struct {
	results []workspace.ToolResult
}

type readOnlyDelegatedEvidenceFact struct {
	Summary         string
	Output          string
	Packet          ReadOnlyEvidencePacket
	HasPacket       bool
	SourceBodyPaths []string
	InspectedPaths  []string
	CandidatePaths  []string
	FindingEvidence bool
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
	if path == "" || IsNonSourceEvidenceArtifactPath(path) || readOnlyExternalDocumentBodyPath(path) {
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

func (projection toolResultEvidenceProjection) SourceBodyPathsFromResult(result workspace.ToolResult) []string {
	if strings.TrimSpace(result.Error) != "" {
		return nil
	}

	single := toolResultEvidenceProjection{results: []workspace.ToolResult{result}}
	var paths []string

	for _, path := range ReadPathsFromToolResult(result) {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || IsNonSourceEvidenceArtifactPath(normalized) || readOnlyExternalDocumentBodyPath(normalized) || !single.HasSourceBody(normalized) {
			continue
		}
		paths = textutil.AppendUnique(paths, normalized)
	}

	return paths
}

func (projection toolResultEvidenceProjection) SourceBodyPaths() []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, result := range projection.results {
		for _, path := range projection.SourceBodyPathsFromResult(result) {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
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

func (projection toolResultEvidenceProjection) ReadOnlyEvidencePackets() []ReadOnlyEvidencePacket {
	packets := make([]ReadOnlyEvidencePacket, 0, 2)

	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		if fact.HasPacket {
			packets = append(packets, fact.Packet)
		}
	}

	return packets
}

func (projection toolResultEvidenceProjection) ReadOnlyDelegatedEvidenceFacts() []readOnlyDelegatedEvidenceFact {
	facts := make([]readOnlyDelegatedEvidenceFact, 0, 2)

	for _, result := range projection.results {
		if fact, ok := readOnlyDelegatedEvidenceFactFromResult(result); ok {
			facts = append(facts, fact)
		}
	}

	return facts
}

func readOnlyDelegatedEvidenceFactFromResult(result workspace.ToolResult) (readOnlyDelegatedEvidenceFact, bool) {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
		return readOnlyDelegatedEvidenceFact{}, false
	}

	fact := readOnlyDelegatedEvidenceFact{
		Summary: strings.TrimSpace(result.Summary),
		Output:  strings.TrimSpace(result.Output),
	}

	if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
		fact.HasPacket = true
		fact.Packet = packet
		fact.SourceBodyPaths = append([]string(nil), packet.SourceBodyPaths...)
		fact.InspectedPaths = append([]string(nil), packet.InspectedPaths...)
		fact.CandidatePaths = append([]string(nil), packet.CandidatePaths...)
		return fact, true
	}

	fact.SourceBodyPaths = sourceBodyEvidencePathsFromDelegatedPacket(result.Output)
	fact.FindingEvidence = delegatedTaskOutputHasFindingEvidence(result.Output)

	return fact, true
}

func (projection toolResultEvidenceProjection) ReadOnlyEvidencePacketCandidatePaths() []string {
	paths := make([]string, 0, 8)

	for _, result := range projection.results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		tool := strings.TrimSpace(result.Tool)
		switch {
		case projection.DiscoveryCandidateTool(tool), tool == workspace.ToolNameContextPack:
			paths = append(paths, projection.CandidatePathsFromResult(result, 16)...)
		case workspace.IsSearchToolName(tool):
			paths = append(paths, projection.CandidatePathsFromResult(result, 64)...)
		default:
			paths = append(paths, ReadPathsFromToolResult(result)...)
		}
	}

	return readOnlyEvidencePacketSourceLikePaths(paths, 16)
}

func (projection toolResultEvidenceProjection) ReadOnlySourceBodyEvidencePaths() []string {
	paths := nonArtifactEvidencePaths(projection.SourceBodyPaths())

	var delegated []string
	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		delegated = append(delegated, fact.SourceBodyPaths...)
	}

	for _, raw := range delegated {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath("", path) {
			continue
		}

		paths = append(paths, path)
	}

	return uniqueDelegatedDiscoveryPaths(paths)
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

func contextPackCandidatePathsFromResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0, max)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack || strings.TrimSpace(result.Error) != "" || result.Facts == nil {
			continue
		}

		for _, raw := range result.Facts.CandidatePaths {
			path := NormalizeRunnerPath(raw)
			if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
				continue
			}

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)

			if len(paths) >= max {
				return paths
			}
		}
	}

	return paths
}

func (toolResultEvidenceProjection) GeneratedArtifactOnlyReadResult(result workspace.ToolResult) bool {
	paths := ReadPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !IsNonSourceEvidenceArtifactPath(path) {
			return false
		}
	}

	return true
}

func (projection toolResultEvidenceProjection) DiagnosticArtifactReadForPath(path string) bool {
	needle := normalizeDiagnosticArtifactEvidencePath(path)
	if needle == "" || !IsDiagnosticLogArtifactPath(needle) {
		return false
	}

	for _, result := range projection.results {
		if strings.TrimSpace(result.Error) != "" ||
			strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile ||
			!projection.GeneratedArtifactOnlyReadResult(result) {

			continue
		}

		for _, readPath := range ReadPathsFromToolResult(result) {
			if normalizeDiagnosticArtifactEvidencePath(readPath) == needle {
				return true
			}
		}
	}

	return false
}

func normalizeDiagnosticArtifactEvidencePath(path string) string {
	path = strings.TrimSpace(strings.Trim(path, "`'\" "))
	if path == "" {
		return ""
	}

	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")

	return path
}

func (toolResultEvidenceProjection) ExternalDocumentBodyPathsFromResult(result workspace.ToolResult) []string {
	if !concreteSourceEvidenceResult(result) {
		return nil
	}

	var paths []string

	for _, raw := range ReadPathsFromToolResult(result) {
		path := normalizeReadOnlyExternalDocumentPath(raw)
		if !readOnlyExternalDocumentBodyPath(path) {
			continue
		}
		paths = textutil.AppendUnique(paths, path)
	}

	return paths
}

func (projection toolResultEvidenceProjection) ExternalDocumentBodyMatches(path string) []string {
	path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	if path == "" {
		return nil
	}

	var matches []string

	for _, result := range projection.results {
		for _, candidate := range projection.ExternalDocumentBodyPathsFromResult(result) {
			if !externalDocumentPathMatchesCitation(path, candidate) {
				continue
			}
			matches = textutil.AppendUnique(matches, candidate)
		}
	}

	return uniqueSortedRunnerPaths(matches)
}

func externalDocumentPathMatchesCitation(path string, candidate string) bool {
	path = strings.Trim(path, "`'\"()[]{}<>,;")
	path = filepath.ToSlash(strings.TrimPrefix(path, "./"))
	candidate = normalizeReadOnlyExternalDocumentPath(candidate)
	if path == "" || candidate == "" {
		return false
	}

	pathLower := strings.ToLower(path)
	candidateLower := strings.ToLower(candidate)
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || pathparse.LooksLikeWindowsAbsolutePath(path) {
		return pathLower == candidateLower
	}

	if strings.Contains(path, "/") {
		return strings.HasSuffix(candidateLower, "/"+pathLower)
	}

	return strings.ToLower(filepath.Base(candidateLower)) == pathLower
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

	return nil
}
