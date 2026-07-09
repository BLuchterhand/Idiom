package agent

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/sourcekind"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/runtime/workerruntime"
)

type toolResultEvidenceProjection struct {
	results []workspace.ToolResult
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

	return toolResultEvidenceProjection{results: append([]workspace.ToolResult(nil), results...), records: records}
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

func (projection toolResultEvidenceProjection) bestAnswerableTaskResultOutput(brief workerruntime.WorkerRuntimeBrief) (string, bool) {
	objective := strings.TrimSpace(workerruntime.NormalizeRuntimeBrief(brief).Objective)
	facts := projection.ReadOnlyDelegatedEvidenceFacts()
	for i := len(facts) - 1; i >= 0; i-- {
		fact := facts[i]
		if !readOnlyDelegatedEvidenceDecisionForObjective(objective, fact).TaskResultAnswerable {
			continue
		}
		if output := strings.TrimSpace(fact.Output); output != "" {
			return output, true
		}
	}
	return "", false
}

func (projection toolResultEvidenceProjection) hasAnswerableDelegatedPacket(brief workerruntime.WorkerRuntimeBrief) bool {
	objective := strings.TrimSpace(workerruntime.NormalizeRuntimeBrief(brief).Objective)
	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		if readOnlyDelegatedEvidenceDecisionForObjective(objective, fact).TaskResultAnswerable {
			return true
		}
	}
	return false
}

func (projection toolResultEvidenceProjection) sourceBodyPacketAvailable() bool {
	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		if readOnlyDelegatedEvidenceDecisionForObjective("", fact).SourceBodyPacketAvailable {
			return true
		}
	}
	return false
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

func (projection toolResultEvidenceProjection) pinnedPaths(max int) []string {
	seen := make(map[string]struct{})

	var paths []string

	for _, record := range projection.records {
		for _, path := range recordPinnedWorkspacePaths(record) {
			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	paths = projection.prioritizedClosureEvidencePaths(paths, len(paths))

	return textutil.Prefix(paths, max)
}

func recordPinnedWorkspacePaths(record toolResultEvidenceRecord) []string {
	seen := make(map[string]struct{})
	var paths []string

	for _, field := range []string{record.summary, record.output, record.result.Error} {
		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(field)) {
			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	return paths
}

func (projection toolResultEvidenceProjection) directProbePaths(max int) []string {
	if max <= 0 {
		max = 8
	}

	seen := make(map[string]struct{}, max)

	var paths []string

	for _, record := range projection.records {
		if len(record.SourceBodyPaths()) == 0 {
			continue
		}

		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(record.summary)) {
			path = normalizeDelegatedDiscoveryEvidencePath(path)
			if path == "" || IsNonSourceEvidenceArtifactPath(path) || !delegatedDiscoveryLooksLikeEvidencePath(path) {
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

func (projection toolResultEvidenceProjection) promptEvidencePaths(max int) []string {
	if max <= 0 {
		max = 8
	}

	paths := make([]string, 0, max*2)
	directPaths := projection.directProbePaths(max)

	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		paths = append(paths, fact.SourceBodyPaths...)
		paths = append(paths, fact.InspectedPaths...)
	}

	paths = append(paths, projection.delegatedReadOnlyInspectedImplementationPaths()...)
	paths = append(paths, discoveryQueryCandidatePaths(projection.results, max*4)...)
	paths = append(paths, typedDiscoveryCandidatePathsFromResults(projection.results, max*4)...)
	paths = append(paths, projection.pinnedPaths(max*6)...)
	paths = projection.prioritizedClosureEvidencePaths(paths, max*3)

	return textutil.Prefix(uniqueDelegatedDiscoveryPaths(append(directPaths, closurePromptEvidencePaths(paths, max*2)...)), max)
}

func (projection toolResultEvidenceProjection) evidencePathsForBrief(brief workerruntime.WorkerRuntimeBrief, max int) []string {
	if max <= 0 {
		max = 8
	}

	if !runnerBriefRequiresMutation(brief) {
		if readOnlyDirectAnswerLane(brief) {
			return projection.readOnlyDirectAnswerPresentationEvidencePaths(brief, max)
		}

		return projection.promptEvidencePaths(max)
	}

	scope := projection.mutationEvidenceScope(brief)
	paths := projection.ChangedPathList()

	for _, path := range projection.directProbePaths(max * 2) {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || !scope[normalized] {
			continue
		}

		paths = append(paths, normalized)
	}

	for _, path := range brief.EvidencePaths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || !scope[normalized] {
			continue
		}

		paths = append(paths, normalized)
	}

	return closurePromptTargetEvidencePaths(paths, max)
}

func (projection toolResultEvidenceProjection) readOnlyDirectAnswerPresentationEvidencePaths(brief workerruntime.WorkerRuntimeBrief, max int) []string {
	if max <= 0 {
		max = 8
	}

	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), projection.results)
	paths := append([]string{}, ledger.RelevantSourceBodyPaths...)
	paths = textutil.Merge(paths, ledger.RelevantExternalDocumentPaths)

	if ObjectiveAsksImplementationShape(ledger.Objective) {
		paths = textutil.Merge(paths, ledger.ImplementationOwnerPaths())
	}

	if ledger.DiagnosticRelevant {
		paths = textutil.Merge(paths, readOnlySourceLikePaths(ledger.DiagnosticPaths, max*2))
	}

	return readOnlyEvidenceDisplayPaths(paths, max)
}

func (projection toolResultEvidenceProjection) pinnedPathsForBrief(brief workerruntime.WorkerRuntimeBrief, max int) []string {
	if max <= 0 {
		max = 8
	}

	if !runnerBriefRequiresMutation(brief) {
		if readOnlyDirectAnswerLane(brief) {
			return projection.readOnlyDirectAnswerPresentationEvidencePaths(brief, max)
		}

		return projection.promptEvidencePaths(max)
	}

	paths := append([]string{}, brief.RequiredEditPaths...)

	paths = append(paths, mapKeys(projection.ChangedPathSet())...)
	if implementationTargetWriteReadySurfaceActive(brief, projection.results) {
		paths = append(paths, projection.SourceBodyPaths()...)
	}
	paths = append(paths, projection.evidencePathsForBrief(brief, max*2)...)

	return closurePromptTargetEvidencePaths(paths, max)
}

func (projection toolResultEvidenceProjection) mutationEvidenceScope(brief workerruntime.WorkerRuntimeBrief) map[string]bool {
	scope := map[string]bool{}

	for _, values := range [][]string{
		brief.RequiredEditPaths,
		brief.TargetRefs,
		brief.ChangedPaths,
		brief.EvidencePaths,
		mapKeys(projection.ChangedPathSet()),
	} {
		for _, raw := range values {
			path := NormalizeRunnerPath(raw)
			if path != "" {
				scope[path] = true
			}
		}
	}

	return scope
}

func (projection toolResultEvidenceProjection) postChildClosureEvidencePaths(objective string, max int) []string {
	paths := make([]string, 0, max)
	followupAnchors := make([]string, 0, max)
	acceptedPacket := false

	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		decision := readOnlyDelegatedEvidenceDecisionForObjective(objective, fact)
		if len(decision.ClosureEvidencePaths) == 0 {
			followupAnchors = append(followupAnchors, decision.FollowupAnchors...)
			continue
		}

		acceptedPacket = true
		paths = append(paths, decision.ClosureEvidencePaths...)
	}

	if acceptedPacket {
		paths = append(paths, projection.pinnedPaths(max*4)...)
	}

	if len(paths) == 0 {
		paths = append(paths, followupAnchors...)
	}

	paths = projection.prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), max)

	return closurePromptEvidencePaths(paths, max)
}

func (projection toolResultEvidenceProjection) delegatedClosureEvidencePaths(objective string, max int) []string {
	paths := make([]string, 0, max*2)
	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		paths = append(paths, fact.SourceBodyPaths...)
		paths = append(paths, fact.InspectedPaths...)
		paths = append(paths, fact.CandidatePaths...)
	}

	paths = append(paths, projection.delegatedReadOnlyInspectedImplementationPaths()...)
	paths = append(paths, discoveryQueryCandidatePaths(projection.results, max*4)...)
	paths = append(paths, projection.pinnedPaths(max*4)...)
	paths = projection.prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), max)

	return closurePromptEvidencePaths(paths, max)
}

func (projection toolResultEvidenceProjection) acceptedDelegatedReadOnlyEvidencePaths(objective string) []string {
	var paths []string
	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		decision := readOnlyDelegatedEvidenceDecisionForObjective(objective, fact)
		if decision.TaskResultAnswerable {
			paths = textutil.Merge(paths, decision.LedgerSourcePaths, decision.InspectedPaths)
		}
	}

	return paths
}

func (projection toolResultEvidenceProjection) acceptedImplementationEvidencePaths() []string {
	paths := projection.ChangedPathList()

	paths = append(paths, projection.directProbePaths(8)...)

	return closurePromptTargetEvidencePaths(paths, 8)
}

func (projection toolResultEvidenceProjection) delegatedReadOnlyFallbackEvidencePaths(answerability readOnlyAnswerabilityLedger, max int) []string {
	paths := append([]string{}, answerability.RelevantSourceBodyPaths...)

	paths = append(paths, answerability.CandidatePaths...)
	paths = append(paths, projection.delegatedReadOnlyInspectedImplementationPaths()...)
	paths = append(paths, discoveryQueryCandidatePaths(projection.results, max)...)
	paths = append(paths, projection.pinnedPaths(max)...)
	paths = projection.prioritizedClosureEvidencePaths(paths, max)

	return closurePromptEvidencePaths(paths, max)
}

func (projection toolResultEvidenceProjection) delegatedReadOnlyInspectedImplementationPaths() []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, record := range projection.records {
		if record.failed || strings.TrimSpace(record.output) == "" {
			continue
		}

		if record.candidateAnchorOnly {
			continue
		}

		if !workspace.IsSourceBodyInspectionTool(record.tool) {
			continue
		}

		recordPaths := projection.prioritizedClosureEvidencePaths(recordPinnedWorkspacePaths(record), 4)
		for _, path := range recordPaths {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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

func closurePromptTargetEvidencePaths(paths []string, max int) []string {
	return filteredClosurePromptEvidencePaths(uniqueDelegatedDiscoveryPaths(paths), max)
}

func (projection toolResultEvidenceProjection) exactProbeEvidencePaths() []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, record := range projection.records {
		if record.failed || strings.TrimSpace(record.output) == "" {
			continue
		}

		if !workspace.IsLanguageSourceBodyTool(record.tool) {
			continue
		}

		path, _ := extractExactProbeAnchor(record.result)

		path = normalizeDelegatedDiscoveryEvidencePath(path)

		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return preferNonTestEvidencePaths(paths)
}

func (projection toolResultEvidenceProjection) closureProbeSuggestionLine(max int) string {
	candidates := projection.closureProbeCandidates(max)
	if len(candidates) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		quoted = append(quoted, "`"+candidate+"`")
	}

	return "Evidence-grounded exact probes to prefer: " + strings.Join(quoted, ", ") + "."
}

func (projection toolResultEvidenceProjection) closureProbeCandidates(max int) []string {
	if max <= 0 {
		max = 3
	}

	candidates := make([]string, 0, max)
	add := func(raw string) {
		if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw); ok {
			candidates = textutil.AppendUnique(candidates, symbol)
		}
	}

	for _, record := range projection.records {
		if record.failed {
			continue
		}

		for _, field := range []string{record.output, record.summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathLineSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])

					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}

			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[2])

					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}

			for _, line := range strings.Split(text, "\n") {
				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) > 1 {
						add(match[1])

						if len(candidates) >= max {
							return candidates[:max]
						}
					}
				}
			}

			for _, match := range goFuncDeclPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])

					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
		}
	}

	return candidates
}

func (projection toolResultEvidenceProjection) prioritizedClosureEvidencePaths(paths []string, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)

	if len(paths) <= 1 {
		return paths
	}

	sharedCounts, candidateCounts := projection.closureEvidenceProbeScores()

	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])

		if pi != pj {
			return pi < pj
		}

		ni := normalizeDelegatedDiscoveryEvidencePath(paths[i])
		nj := normalizeDelegatedDiscoveryEvidencePath(paths[j])

		if sharedCounts[ni] != sharedCounts[nj] {
			return sharedCounts[ni] > sharedCounts[nj]
		}

		if candidateCounts[ni] != candidateCounts[nj] {
			return candidateCounts[ni] > candidateCounts[nj]
		}

		return false
	})

	return textutil.Prefix(paths, max)
}

func (projection toolResultEvidenceProjection) closureEvidenceProbeScores() (map[string]int, map[string]int) {
	byPath := projection.closureProbeCandidatesByPath(6)
	probeFrequency := make(map[string]int)

	for _, candidates := range byPath {
		seen := make(map[string]struct{})

		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}

			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}
			probeFrequency[key]++
		}
	}

	sharedCounts := make(map[string]int, len(byPath))
	candidateCounts := make(map[string]int, len(byPath))

	for rawPath, candidates := range byPath {
		path := normalizeDelegatedDiscoveryEvidencePath(rawPath)
		if path == "" {
			continue
		}

		seen := make(map[string]struct{})

		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}

			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}
			candidateCounts[path]++

			if probeFrequency[key] > 1 {
				sharedCounts[path] += probeFrequency[key]
			}
		}
	}

	return sharedCounts, candidateCounts
}

func (projection toolResultEvidenceProjection) closureProbeCandidatesByPath(maxPerPath int) map[string][]string {
	if maxPerPath <= 0 {
		maxPerPath = 3
	}

	byPath := make(map[string][]string)
	add := func(path string, raw string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" {
			return
		}

		symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw)
		if !ok {
			return
		}

		current := byPath[path]
		if containsExactProbeCandidate(current, symbol) {
			return
		}

		if len(current) >= maxPerPath {
			return
		}

		byPath[path] = append(current, symbol)
	}

	for _, record := range projection.records {
		if record.failed {
			continue
		}

		for _, field := range []string{record.output, record.summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathScopedSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}

			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}

			for _, line := range strings.Split(text, "\n") {
				paths := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line))
				if len(paths) == 0 {
					continue
				}

				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) <= 1 {
						continue
					}

					for _, path := range paths {
						add(path, match[1])
					}
				}
			}
		}
	}

	return byPath
}

func (record toolResultEvidenceRecord) CandidatePaths(max int) []string {
	if record.failed {
		return nil
	}

	if paths := toolResultFactsCandidatePaths(record.result.Facts, max); len(paths) > 0 {
		return paths
	}

	switch {
	case record.tool == workspace.ToolNameDiscoveryQuery:
		return discoveryQueryCandidatePaths([]workspace.ToolResult{record.result}, max)
	case toolResultEvidenceDiscoveryCandidateTool(record.tool):
		return sourceLikePathsFromText(record.summary+"\n"+record.output, max)
	default:
		return nil
	}
}

func typedDiscoveryCandidatePathsFromResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0, max)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, path := range toolResultFactsCandidatePaths(result.Facts, max) {
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

func toolResultFactsCandidatePaths(facts *toolfacts.ToolResultFacts, max int) []string {
	if facts == nil {
		return nil
	}
	if max <= 0 {
		max = 4
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0, max)
	for _, raw := range facts.CandidatePaths {
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
