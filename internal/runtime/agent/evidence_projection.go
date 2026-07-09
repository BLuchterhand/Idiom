package agent

import (
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/sourcekind"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

type toolResultEvidenceProjection struct {
	records []toolResultEvidenceRecord
}

type toolResultEvidenceRecord struct {
	result              workspace.ToolResult
	tool                string
	summary             string
	output              string
	failed              bool
	sourceBodies        []toolfacts.SourceBodyEvidence
	readPaths           []string
	changedPaths        []string
	changedSourcePaths  []string
	candidateAnchorOnly bool
}

type readOnlyDelegatedEvidenceFact struct {
	Summary         string
	Output          string
	Packet          ReadOnlyEvidencePacket
	HasPacket       bool
	SourceBodyPaths []string
	InspectedPaths  []string
	CandidatePaths  []string
}

type readOnlyEvidenceObservation struct {
	Kind            readOnlyEvidenceKind
	Tool            string
	Source          string
	Path            string
	CandidatePaths  []string
	HitCount        int
	HighCardinality bool
	Summary         string
	Output          string
}

func newToolResultEvidenceProjection(results []workspace.ToolResult) toolResultEvidenceProjection {
	records := make([]toolResultEvidenceRecord, 0, len(results))
	for _, result := range results {
		records = append(records, newToolResultEvidenceRecord(result))
	}

	return toolResultEvidenceProjection{records: records}
}

func newToolResultEvidenceRecord(result workspace.ToolResult) toolResultEvidenceRecord {
	record := toolResultEvidenceRecord{
		result:              result,
		tool:                strings.TrimSpace(result.Tool),
		summary:             strings.TrimSpace(result.Summary),
		output:              result.Output,
		failed:              strings.TrimSpace(result.Error) != "",
		sourceBodies:        sourceBodyFactsFromResult(result),
		candidateAnchorOnly: CandidateAnchorOnlyText(result.Summary + "\n" + result.Output),
	}
	record.readPaths = record.deriveReadPaths()
	record.changedPaths = record.deriveChangedPaths()
	record.changedSourcePaths = record.deriveChangedSourcePaths()
	return record
}

func (record toolResultEvidenceRecord) deriveReadPaths() []string {
	if record.candidateAnchorOnly && len(record.result.Paths) == 0 && len(record.sourceBodies) == 0 {
		return nil
	}

	hasTypedReadPath := len(record.result.Paths) > 0 || len(record.sourceBodies) > 0
	if (record.tool == workspace.ToolNameCodeNavigate || record.tool == workspace.ToolNameReadOnlyCapability) &&
		!hasTypedReadPath && !routedSourceReadOutputHasBody(record.output) {

		return nil
	}

	seen := map[string]struct{}{}
	var paths []string

	candidates := append([]string{}, record.result.Paths...)
	for _, fact := range record.sourceBodies {
		candidates = append(candidates, fact.Path)
	}

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		normalized := NormalizeRunnerPath(candidate)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, candidate)
	}

	return paths
}

func (record toolResultEvidenceRecord) ReadPaths() []string {
	return append([]string(nil), record.readPaths...)
}

func (record toolResultEvidenceRecord) ChangedPathList() []string {
	return append([]string(nil), record.changedPaths...)
}

func (record toolResultEvidenceRecord) ChangedSourcePathList() []string {
	return append([]string(nil), record.changedSourcePaths...)
}

func (record toolResultEvidenceRecord) deriveChangedPaths() []string {
	if record.failed {
		return nil
	}

	var paths []string
	for _, rawPath := range record.result.ChangedPaths {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		paths = textutil.AppendUnique(paths, path)
	}

	return paths
}

func (record toolResultEvidenceRecord) deriveChangedSourcePaths() []string {
	var paths []string
	for _, path := range record.changedPaths {
		if !sourcekind.CoversSourceSanity(path) {
			continue
		}

		paths = textutil.AppendUnique(paths, path)
	}

	return paths
}

func (record toolResultEvidenceRecord) SourceBodyFact(path string) (toolfacts.SourceBodyEvidence, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return toolfacts.SourceBodyEvidence{}, false
	}

	for _, fact := range record.sourceBodies {
		if NormalizeRunnerPath(fact.Path) == path {
			return fact, true
		}
	}

	return toolfacts.SourceBodyEvidence{}, false
}

func routedSourceReadOutputHasBody(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}

	if CandidateAnchorOnlyText(output) {
		return false
	}

	if !strings.Contains(output, "route_summary:") {
		return true
	}

	seenRouteSummary := false

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route_summary:") {
			seenRouteSummary = true
			continue
		}

		if !seenRouteSummary || line == "" {
			continue
		}

		if routedSourceReadOutputHeaderLine(line) {
			continue
		}

		return true
	}

	return false
}

func routedSourceReadOutputHeaderLine(line string) bool {
	return strings.Contains(line, "capability:") ||
		strings.Contains(line, "language:") ||
		strings.Contains(line, "routed_tool:")
}

func (projection toolResultEvidenceProjection) ReadPaths() map[string]struct{} {
	paths := make(map[string]struct{}, len(projection.records))
	for _, record := range projection.records {
		if record.failed {
			continue
		}
		for _, path := range record.ReadPaths() {
			normalized := NormalizeRunnerPath(path)
			if normalized != "" {
				paths[normalized] = struct{}{}
			}
		}
	}
	return paths
}

func sourceBodyFactsFromResult(result workspace.ToolResult) []toolfacts.SourceBodyEvidence {
	if result.Facts != nil && len(result.Facts.SourceBodies) > 0 {
		facts := toolfacts.NormalizeSourceBodies(result.Facts.SourceBodies)
		if len(facts) > 0 {
			return facts
		}
	}

	return nil
}

func sourceBodyFactHasRange(fact toolfacts.SourceBodyEvidence) bool {
	return fact.StartLine > 0 && fact.EndLine >= fact.StartLine
}

func sourceBodyFactCoversWholeFile(fact toolfacts.SourceBodyEvidence) bool {
	if fact.TotalLines == 0 && fact.StartLine == 0 && fact.EndLine == 0 {
		return true
	}

	return fact.TotalLines > 0 && fact.StartLine == 1 && fact.EndLine >= fact.TotalLines
}

func sourceBodyReadRangeFromFact(fact toolfacts.SourceBodyEvidence) (planThenExecuteReadRange, bool) {
	if !sourceBodyFactHasRange(fact) {
		return planThenExecuteReadRange{}, false
	}

	return planThenExecuteReadRange{
		start: fact.StartLine,
		end:   fact.EndLine,
		total: fact.TotalLines,
	}, true
}

func (projection toolResultEvidenceProjection) SourceEvidenceForPath(path string) planThenExecuteSourceEvidence {
	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) || readOnlyExternalDocumentBodyPath(path) {
		return planThenExecuteSourceEvidence{}
	}
	best := planThenExecuteSourceEvidence{Path: path}
	for _, record := range projection.records {
		evidence := record.SourceEvidenceForPath(path)
		if redirectPath := implementationTargetWriteReadyRedirectedReadPath(record.result); redirectPath == path {
			if tool, ok := implementationTargetWriteReadyRedirectedReadTool(record.result); ok {
				evidence = planThenExecuteSourceEvidence{
					Path:   path,
					Level:  planThenExecuteEvidenceSourceBodyPending,
					Source: record.tool,
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

func (record toolResultEvidenceRecord) SourceEvidenceForPath(path string) planThenExecuteSourceEvidence {
	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) || record.failed {
		return planThenExecuteSourceEvidence{}
	}

	text := strings.Join([]string{record.result.Summary, record.result.Output}, "\n")
	evidence := planThenExecuteSourceEvidence{Path: path}

	if strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
		evidence.Level = planThenExecuteEvidencePathSeen
		evidence.Source = record.tool
		evidence.Detail = "path mentioned by tool result"
	}

	sourceMatched := false
	var sourceFact toolfacts.SourceBodyEvidence
	hasSourceFact := false

	for _, readPath := range record.readPaths {
		if IsNonSourceEvidenceArtifactPath(readPath) {
			continue
		}

		if NormalizeRunnerPath(readPath) == path {
			sourceMatched = true
			if fact, ok := record.SourceBodyFact(path); ok {
				sourceFact = fact
				hasSourceFact = true
			}
			break
		}
	}

	if !sourceMatched {
		return evidence
	}

	evidence.Source = record.tool
	evidence.Level = planThenExecuteEvidencePathSeen
	evidence.Detail = "typed path seen"

	if !hasSourceFact {
		if record.CodeNavigateIsOutline() || strings.HasSuffix(record.tool, "_file_outline") {
			evidence.Detail = "file outline"
		}
		return evidence
	}

	evidence.Level, evidence.Detail = planThenExecuteReadFileEvidenceLevel(sourceFact, path)

	switch {
	case record.tool == workspace.ToolNameReadFile || record.tool == workspace.ToolNamePrefetch:
	case record.tool == workspace.ToolNameCodeNavigate || record.tool == workspace.ToolNameReadOnlyCapability:
		if record.CodeNavigateIsOutline() {
			evidence.Level = planThenExecuteEvidencePathSeen
			evidence.Detail = "file outline"
		} else if sourceFact.Truncated {
			evidence.Level = planThenExecuteEvidenceBodyWindow
			evidence.Detail = "truncated routed source body"
		} else {
			evidence.Level = planThenExecuteEvidenceAnchorReady
			evidence.Detail = "routed source body"
		}
	case strings.HasSuffix(record.tool, "_file_outline"):
		evidence.Level = planThenExecuteEvidencePathSeen
		evidence.Detail = "file outline"
	case strings.HasSuffix(record.tool, "_read_symbol"), strings.HasSuffix(record.tool, "_extract_body"):
		if sourceFact.Truncated {
			evidence.Level = planThenExecuteEvidenceBodyWindow
			evidence.Detail = "truncated symbol/body read"
		} else {
			evidence.Level = planThenExecuteEvidenceAnchorReady
			evidence.Detail = "symbol/body read"
		}
	}

	return evidence
}

func (record toolResultEvidenceRecord) CodeNavigateIsOutline() bool {
	output := strings.TrimSpace(record.output)
	if output == "" {
		return false
	}
	summary := strings.ToLower(record.summary)

	if strings.Contains(summary, "outline") && !record.candidateAnchorOnly {
		return true
	}

	lower := strings.ToLower(output)
	for _, marker := range []string{"\nfn ", "\nstruct ", "\nenum ", "\nimpl ", "\ntrait "} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return strings.Contains(lower, "capability: file_outline")
}

func (record toolResultEvidenceRecord) SourceBodyPaths() []string {
	if record.failed {
		return nil
	}

	var paths []string
	for _, rawPath := range record.readPaths {
		path := NormalizeRunnerPath(rawPath)
		if path == "" || IsNonSourceEvidenceArtifactPath(path) || readOnlyExternalDocumentBodyPath(path) || !sourceEvidenceHasBody(record.SourceEvidenceForPath(path)) {
			continue
		}
		paths = textutil.AppendUnique(paths, path)
	}

	return paths
}

func (projection toolResultEvidenceProjection) SourceBodyPaths() []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, record := range projection.records {
		for _, path := range record.SourceBodyPaths() {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths
}

func (projection toolResultEvidenceProjection) ReadOnlyEvidenceObservations() []readOnlyEvidenceObservation {
	var observations []readOnlyEvidenceObservation
	for _, record := range projection.records {
		observations = append(observations, record.ReadOnlyEvidenceObservations()...)
	}
	return observations
}

func (projection toolResultEvidenceProjection) ValidationStatusEvidenceLedgerPaths() []string {
	var paths []string
	add := func(path string) {
		normalized := NormalizeRunnerPath(path)
		if normalized != "" {
			paths = textutil.AppendUnique(paths, normalized)
		}
	}

	for _, path := range projection.ChangedPathList() {
		add(path)
	}

	for _, record := range projection.records {
		if record.failed {
			continue
		}
		if record.tool != workspace.ToolNameSourceSanity && !semanticlanguage.IsDiagnosticToolName(record.tool) {
			continue
		}
		for _, path := range record.ReadPaths() {
			add(path)
		}
		if record.result.Facts != nil {
			for _, path := range record.result.Facts.DiagnosticEvidencePaths {
				add(path)
			}
		}
	}

	return paths
}

func (projection toolResultEvidenceProjection) MentionedEvidencePathsForObjective(objective string, max int) []string {
	var paths []string

	for _, record := range projection.records {
		if !record.AcceptedMentionedEvidenceForObjective(objective) {
			continue
		}

		paths = textutil.Merge(paths, pathparse.MentionedWorkspacePaths(record.summary))
		paths = textutil.Merge(paths, pathparse.MentionedWorkspacePaths(record.output))
		paths = textutil.Merge(paths, pathparse.MentionedWorkspacePaths(record.result.Error))

		if len(paths) >= max {
			break
		}
	}

	return closurePromptEvidencePaths(paths, max)
}

func (record toolResultEvidenceRecord) AcceptedMentionedEvidenceForObjective(objective string) bool {
	if record.failed {
		return false
	}

	diagnostic := record.result.Facts != nil && len(readOnlySourceLikePaths(record.result.Facts.DiagnosticEvidencePaths, 8)) > 0
	if len(record.SourceBodyPaths()) > 0 || diagnostic {
		return true
	}

	fact, ok := readOnlyDelegatedEvidenceFactFromResult(record.result)
	if !ok || fact.Output == "" {
		return false
	}

	return readOnlyDelegatedEvidenceDecisionForObjective(objective, fact).AcceptedMentionedEvidenceForObjective
}

func (record toolResultEvidenceRecord) ReadOnlyEvidenceObservations() []readOnlyEvidenceObservation {
	if record.failed {
		return nil
	}

	if record.tool == "" {
		return nil
	}

	var observations []readOnlyEvidenceObservation

	if record.GeneratedArtifactOnlyRead() {
		return []readOnlyEvidenceObservation{{
			Kind:           readOnlyEvidenceGeneratedArtifact,
			Tool:           record.tool,
			CandidatePaths: readOnlySourceLikePaths(record.readPaths, 8),
			Summary:        record.summary,
			Output:         record.output,
		}}
	}

	if record.result.Facts != nil {
		paths := readOnlySourceLikePaths(record.result.Facts.DiagnosticEvidencePaths, 8)
		if len(paths) > 0 {
			observations = append(observations, readOnlyEvidenceObservation{
				Kind:           readOnlyEvidenceDiagnostic,
				Tool:           record.tool,
				CandidatePaths: paths,
				Summary:        record.summary,
				Output:         record.output,
			})
		}
	}

	if featurePlannerPlanArtifactResult(record.result) {
		observations = append(observations, readOnlyEvidenceObservation{
			Kind:           readOnlyEvidencePlanArtifact,
			Tool:           record.tool,
			CandidatePaths: featurePlannerPlanArtifactProposedPaths(record.result),
			Summary:        record.summary,
			Output:         record.output,
		})
		return observations
	}

	for _, path := range record.SourceBodyPaths() {
		observations = append(observations, readOnlyEvidenceObservation{
			Kind:    readOnlyEvidenceSourceBody,
			Tool:    record.tool,
			Source:  record.tool,
			Path:    path,
			Summary: record.summary,
			Output:  record.output,
		})
	}

	for _, fact := range record.sourceBodies {
		path := normalizeReadOnlyExternalDocumentPath(fact.Path)
		if !readOnlyExternalDocumentBodyPath(path) {
			continue
		}

		observations = append(observations, readOnlyEvidenceObservation{
			Kind:    readOnlyEvidenceExternalDocument,
			Tool:    record.tool,
			Source:  record.tool,
			Path:    path,
			Summary: record.summary,
			Output:  record.output,
		})
	}

	if hasReadOnlyBodyObservation(observations) {
		return observations
	}

	if workspace.IsReadTool(record.tool) {
		paths := readOnlySourceLikePaths(record.readPaths, 8)
		if len(paths) > 0 {
			observations = append(observations, readOnlyEvidenceObservation{
				Kind:           readOnlyEvidenceOutline,
				Tool:           record.tool,
				CandidatePaths: paths,
				Summary:        record.summary,
				Output:         record.output,
			})
		}
	}

	switch {
	case record.tool == workspace.ToolNameTask:
		return observations
	case record.tool == workspace.ToolNameContextPack:
		discovery := discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: record.tool}, record.result, 16)
		observations = append(observations, readOnlyEvidenceObservation{
			Kind:           readOnlyEvidenceContextPacket,
			Tool:           record.tool,
			CandidatePaths: readOnlySourceLikePaths(discovery.Candidates, 16),
			Summary:        record.summary,
			Output:         record.output,
		})
	case workspace.IsSearchToolName(record.tool):
		discovery := discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: record.tool}, record.result, 128)
		observations = append(observations, readOnlyEvidenceObservation{
			Kind:            readOnlyEvidenceSearchHit,
			Tool:            record.tool,
			CandidatePaths:  readOnlySourceLikePaths(discovery.Candidates, 128),
			HitCount:        discovery.SearchHitCount,
			HighCardinality: discovery.HighCardinalitySearch,
			Summary:         record.summary,
			Output:          record.output,
		})
	case toolResultEvidenceDiscoveryCandidateTool(record.tool):
		discovery := discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: record.tool}, record.result, 16)
		candidates := readOnlySourceLikePaths(discovery.Candidates, 16)
		if len(candidates) > 0 {
			observations = append(observations, readOnlyEvidenceObservation{
				Kind:           readOnlyEvidenceSearchHit,
				Tool:           record.tool,
				CandidatePaths: candidates,
				Summary:        record.summary,
				Output:         record.output,
			})
		}
	default:
		if observation, ok := record.readOnlyOutlineObservation(); ok {
			observations = append(observations, observation)
		}
	}

	return observations
}

func hasReadOnlyBodyObservation(observations []readOnlyEvidenceObservation) bool {
	for _, observation := range observations {
		switch observation.Kind {
		case readOnlyEvidenceSourceBody, readOnlyEvidenceExternalDocument:
			return true
		}
	}
	return false
}

func (record toolResultEvidenceRecord) readOnlyOutlineObservation() (readOnlyEvidenceObservation, bool) {
	if record.failed || len(record.SourceBodyPaths()) > 0 {
		return readOnlyEvidenceObservation{}, false
	}

	text := strings.ToLower(strings.TrimSpace(record.result.Summary + "\n" + record.result.Output))
	if text == "" {
		return readOnlyEvidenceObservation{}, false
	}

	if !ContainsAny(text, "file_outline", "capability: outline", "outlined ", "top-level declaration") {
		return readOnlyEvidenceObservation{}, false
	}

	candidates := readOnlySourceLikePaths(record.readPaths, 8)
	if len(candidates) == 0 {
		candidates = sourceLikePathsFromText(record.result.Summary+"\n"+record.result.Output, 8)
	}
	if len(candidates) == 0 {
		return readOnlyEvidenceObservation{}, false
	}

	return readOnlyEvidenceObservation{
		Kind:           readOnlyEvidenceOutline,
		Tool:           record.tool,
		CandidatePaths: candidates,
		Summary:        record.summary,
		Output:         record.output,
	}, true
}

func (projection toolResultEvidenceProjection) CandidatePaths() []string {
	bodySet := make(map[string]struct{})
	for _, path := range projection.SourceBodyPaths() {
		bodySet[path] = struct{}{}
	}

	seen := make(map[string]struct{})
	var paths []string

	for _, record := range projection.records {
		for _, path := range record.CandidatePaths(128) {
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

func (projection toolResultEvidenceProjection) ReadOnlyDelegatedEvidenceFacts() []readOnlyDelegatedEvidenceFact {
	facts := make([]readOnlyDelegatedEvidenceFact, 0, 2)

	for _, record := range projection.records {
		if fact, ok := readOnlyDelegatedEvidenceFactFromResult(record.result); ok {
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

	return readOnlyDelegatedEvidenceFact{}, false
}

func (projection toolResultEvidenceProjection) ReadOnlyEvidencePacketCandidatePaths() []string {
	paths := make([]string, 0, 8)

	for _, record := range projection.records {
		if record.failed {
			continue
		}

		switch {
		case toolResultEvidenceDiscoveryCandidateTool(record.tool), record.tool == workspace.ToolNameContextPack:
			paths = append(paths, record.CandidatePaths(16)...)
		case workspace.IsSearchToolName(record.tool):
			paths = append(paths, record.CandidatePaths(64)...)
		default:
			paths = append(paths, record.ReadPaths()...)
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

func (record toolResultEvidenceRecord) CandidatePaths(max int) []string {
	if record.failed {
		return nil
	}

	switch {
	case record.tool == workspace.ToolNameContextPack:
		return contextPackCandidatePathsFromResults([]workspace.ToolResult{record.result}, max)
	case record.tool == workspace.ToolNameDiscoveryQuery:
		if paths := delegatedDiscoveryQueryEvidencePaths([]workspace.ToolResult{record.result}, max); len(paths) > 0 {
			return paths
		}
		return sourceLikePathsFromText(record.summary+"\n"+record.output, max)
	case toolResultEvidenceDiscoveryCandidateTool(record.tool):
		return sourceLikePathsFromText(record.summary+"\n"+record.output, max)
	case workspace.IsSearchToolName(record.tool):
		return sourceLikePathsFromText(record.summary+"\n"+record.output, max)
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

func (record toolResultEvidenceRecord) GeneratedArtifactOnlyRead() bool {
	if len(record.readPaths) == 0 {
		return false
	}

	for _, path := range record.readPaths {
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

	for _, record := range projection.records {
		if record.failed ||
			record.tool != workspace.ToolNameReadFile ||
			!record.GeneratedArtifactOnlyRead() {

			continue
		}

		for _, readPath := range record.readPaths {
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

func (projection toolResultEvidenceProjection) ExternalDocumentBodyMatches(path string) []string {
	path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	if path == "" {
		return nil
	}

	var matches []string

	for _, observation := range projection.ReadOnlyEvidenceObservations() {
		if observation.Kind != readOnlyEvidenceExternalDocument || !externalDocumentPathMatchesCitation(path, observation.Path) {
			continue
		}
		matches = textutil.AppendUnique(matches, observation.Path)
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

func toolResultEvidenceDiscoveryCandidateTool(tool string) bool {
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

	for _, record := range projection.records {
		for _, path := range record.ChangedPathList() {
			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	return paths
}

func (projection toolResultEvidenceProjection) ChangedSourcePathList() []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, record := range projection.records {
		for _, path := range record.ChangedSourcePathList() {
			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func (projection toolResultEvidenceProjection) HasChangedSourcePath(path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	return slices.Contains(projection.ChangedSourcePathList(), path)
}

func (projection toolResultEvidenceProjection) SingleChangedSourcePath() (string, bool) {
	paths := projection.ChangedSourcePathList()
	if len(paths) != 1 {
		return "", false
	}

	return paths[0], true
}
