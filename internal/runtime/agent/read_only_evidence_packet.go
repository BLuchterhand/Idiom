package agent

import (
	"idiom/internal/runtime/workerruntime"
	"unicode"
	"sort"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/agent/agentutil"
)

const readOnlyEvidencePacketPayloadType = "agent.read_only_evidence_packet.v1"

const ReadOnlyEvidencePacketSchema = readOnlyEvidencePacketPayloadType

type ReadOnlyEvidenceFinding struct {
	Path   string `json:"path,omitempty"`
	Text   string `json:"text,omitempty"`
	Source string `json:"source,omitempty"`
}

type ReadOnlyEvidencePacket struct {
	SchemaVersion   string                    `json:"schema_version"`
	Objective       string                    `json:"objective,omitempty"`
	Question        string                    `json:"question,omitempty"`
	SourceBodyPaths []string                  `json:"source_body_paths,omitempty"`
	InspectedPaths  []string                  `json:"inspected_paths,omitempty"`
	CandidatePaths  []string                  `json:"candidate_paths,omitempty"`
	Findings        []ReadOnlyEvidenceFinding `json:"findings,omitempty"`
	Gaps            []string                  `json:"gaps,omitempty"`
	Confidence      string                    `json:"confidence,omitempty"`
}

func buildReadOnlyEvidencePacket(objective string, answer string, results []workspace.ToolResult) (ReadOnlyEvidencePacket, bool) {
	objective = strings.TrimSpace(objective)

	if readOnlyToolControlGuidanceText(answer) {
		answer = ""
	}

	answer = sanitizeReadOnlyEvidencePacketAnswer(answer)

	sourceBodyPaths := readOnlyEvidencePacketSourceBodyPaths(results)
	candidatePaths := readOnlyEvidencePacketCandidatePaths(results)
	inspectedPaths := readOnlyEvidencePacketInspectedPaths(sourceBodyPaths, candidatePaths, answer)
	findings := readOnlyEvidencePacketFindings(answer, results, sourceBodyPaths, 6)
	packet := ReadOnlyEvidencePacket{
		SchemaVersion:   readOnlyEvidencePacketPayloadType,
		Objective:       objective,
		Question:        delegatedDisplayQuestion(objective),
		SourceBodyPaths: sourceBodyPaths,
		InspectedPaths:  inspectedPaths,
		CandidatePaths:  candidatePaths,
		Findings:        findings,
		Gaps:            readOnlyEvidencePacketGaps(answer, results),
		Confidence:      readOnlyEvidencePacketConfidence(answer, sourceBodyPaths, findings),
	}

	packet = normalizeReadOnlyEvidencePacket(packet)

	return packet, readOnlyEvidencePacketHasContent(packet)
}

func sanitizeReadOnlyEvidencePacketAnswer(answer string) string {
	if !readOnlyEvidencePacketPartialNonAnswer(answer) {
		return answer
	}

	var kept []string

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || readOnlyEvidencePacketTransportLine(line) {
			continue
		}

		kept = append(kept, raw)
	}

	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func readOnlyEvidencePacketPartialNonAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	return strings.HasPrefix(lower, "partial findings from this turn")
}

func readOnlyEvidencePacketToolResult(result workspace.ToolResult, packet ReadOnlyEvidencePacket) workspace.ToolResult {
	packet = normalizeReadOnlyEvidencePacket(packet)

	payload, err := json.Marshal(packet)

	if err != nil || !readOnlyEvidencePacketHasContent(packet) {
		return result
	}

	result.PayloadType = readOnlyEvidencePacketPayloadType
	result.Payload = payload

	return result
}

func readOnlyEvidencePacketFromResult(result workspace.ToolResult) (ReadOnlyEvidencePacket, bool) {
	if strings.TrimSpace(result.PayloadType) != readOnlyEvidencePacketPayloadType || len(result.Payload) == 0 {
		return ReadOnlyEvidencePacket{}, false
	}

	var packet ReadOnlyEvidencePacket

	if err := json.Unmarshal(result.Payload, &packet); err != nil {
		return ReadOnlyEvidencePacket{}, false
	}

	packet = normalizeReadOnlyEvidencePacket(packet)
	if !readOnlyEvidencePacketHasContent(packet) {
		return ReadOnlyEvidencePacket{}, false
	}

	return packet, true
}

func readOnlyEvidencePacketsFromResults(results []workspace.ToolResult) []ReadOnlyEvidencePacket {
	packets := make([]ReadOnlyEvidencePacket, 0, 2)

	for _, result := range results {
		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			packets = append(packets, packet)
		}
	}

	return packets
}

func renderReadOnlyEvidencePacketForToolOutput(packet ReadOnlyEvidencePacket, answer string) string {
	packet = normalizeReadOnlyEvidencePacket(packet)

	var b strings.Builder

	if question := strings.TrimSpace(packet.Question); question != "" {
		b.WriteString("Question: ")
		b.WriteString(question)
		b.WriteString("\n")
	}

	if answer = strings.TrimSpace(stripDelegatedReadOnlyInternalPacket(answer)); answer != "" && !readOnlyToolControlGuidanceText(answer) {
		b.WriteString("Grounded answer:\n")
		b.WriteString(answer)
		b.WriteString("\n")
	}

	if len(packet.Findings) > 0 {
		b.WriteString("Grounded findings:\n")

		for _, finding := range packet.Findings {
			text := strings.TrimSpace(finding.Text)
			if text == "" {
				continue
			}

			b.WriteString("- ")
			b.WriteString(text)

			if path := strings.TrimSpace(finding.Path); path != "" && !strings.Contains(text, path) {
				b.WriteString(" (")
				b.WriteString(path)
				b.WriteString(")")
			}

			b.WriteString("\n")
		}
	}

	if len(packet.SourceBodyPaths) > 0 {
		b.WriteString("Source body paths:\n")

		for _, path := range packet.SourceBodyPaths {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(packet.CandidatePaths) > 0 {
		b.WriteString("Candidate paths:\n")

		for _, path := range limitRunnerStrings(packet.CandidatePaths, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if confidence := strings.TrimSpace(packet.Confidence); confidence != "" {
		b.WriteString("Confidence: ")
		b.WriteString(confidence)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func synthesizeReadOnlyEvidencePacketAnswer(objective string, packets []ReadOnlyEvidencePacket) (string, bool) {
	var (
		sourcePaths    []string
		candidatePaths []string
		findings       []ReadOnlyEvidenceFinding
	)

	for _, packet := range packets {
		packet = normalizeReadOnlyEvidencePacket(packet)
		if !readOnlyEvidencePacketAnswerableForObjective(packet, objective) {
			continue
		}

		sourcePaths = mergeRunnerStateStrings(sourcePaths, packet.SourceBodyPaths)
		candidatePaths = mergeRunnerStateStrings(candidatePaths, packet.CandidatePaths, packet.InspectedPaths)

		for _, finding := range packet.Findings {
			if strings.TrimSpace(finding.Text) == "" {
				continue
			}

			if finding.Path != "" && !slices.Contains(packet.SourceBodyPaths, finding.Path) {
				continue
			}

			findings = append(findings, finding)
		}
	}

	sourcePaths = readOnlyEvidencePacketSourceLikePaths(sourcePaths, 8)
	if len(sourcePaths) == 0 || len(findings) == 0 {
		return "", false
	}

	paths := mergeRunnerStateStrings(sourcePaths, candidatePaths)

	paths = closurePromptEvidencePaths(paths, 8)

	if len(paths) == 0 {
		return "", false
	}

	var b strings.Builder

	if question := delegatedDisplayQuestion(objective); question != "" {
		b.WriteString("For `")
		b.WriteString(question)
		b.WriteString("`, ")
	}

	seen := map[string]struct{}{}

	for _, finding := range findings {
		text := strings.TrimSpace(finding.Text)
		if text == "" {
			continue
		}

		if _, ok := seen[text]; ok {
			continue
		}

		seen[text] = struct{}{}
		if len(seen) > 1 {
			b.WriteString(" ")
		}

		b.WriteString(readOnlyEvidencePacketSentence(text))

		if len(seen) >= 4 {
			break
		}
	}

	if len(seen) == 0 {
		return "", false
	}

	b.WriteString(" This is grounded in ")
	b.WriteString(formatInlinePathList(paths, 4))
	b.WriteString(".")

	return strings.TrimSpace(b.String()), true
}

func readOnlyEvidencePacketSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	switch text[len(text)-1:] {
	case ".", "!", "?":
		return text
	default:
		return text + "."
	}
}

func normalizeReadOnlyEvidencePacket(packet ReadOnlyEvidencePacket) ReadOnlyEvidencePacket {
	packet.SchemaVersion = readOnlyEvidencePacketPayloadType
	packet.Objective = strings.TrimSpace(packet.Objective)
	packet.Question = strings.TrimSpace(packet.Question)
	packet.Confidence = strings.TrimSpace(packet.Confidence)
	packet.SourceBodyPaths = readOnlyEvidencePacketSourceLikePaths(packet.SourceBodyPaths, 16)
	packet.InspectedPaths = readOnlyEvidencePacketSourceLikePaths(packet.InspectedPaths, 16)
	packet.CandidatePaths = readOnlyEvidencePacketSourceLikePaths(packet.CandidatePaths, 16)

	for i := range packet.Findings {
		findingPaths := readOnlyEvidencePacketSourceLikePaths([]string{packet.Findings[i].Path}, 1)
		if len(findingPaths) > 0 {
			packet.Findings[i].Path = findingPaths[0]
		} else {
			packet.Findings[i].Path = ""
		}

		packet.Findings[i].Text = strings.TrimSpace(packet.Findings[i].Text)
		packet.Findings[i].Source = strings.TrimSpace(packet.Findings[i].Source)
	}

	packet.Findings = readOnlyEvidencePacketUsefulFindings(packet.Findings)
	packet.Gaps = utilUniqueTrimmed(packet.Gaps, 8)

	return packet
}

func readOnlyEvidencePacketHasContent(packet ReadOnlyEvidencePacket) bool {
	return len(packet.SourceBodyPaths) > 0 || len(packet.InspectedPaths) > 0 || len(packet.CandidatePaths) > 0 || len(packet.Findings) > 0
}

func readOnlyEvidencePacketAnswerable(packet ReadOnlyEvidencePacket) bool {
	packet = normalizeReadOnlyEvidencePacket(packet)
	return len(packet.SourceBodyPaths) > 0 && len(packet.Findings) > 0
}

func readOnlyEvidencePacketAnswerableForObjective(packet ReadOnlyEvidencePacket, objective string) bool {
	packet = normalizeReadOnlyEvidencePacket(packet)
	if !readOnlyEvidencePacketAnswerable(packet) {
		return false
	}

	objective = strings.TrimSpace(objective)
	if objective == "" {
		return true
	}

	if objectiveAsksImplementationShape(objective) {
		return len(readOnlyEvidencePacketObjectiveSourceOwnerEvidence(packet, objective)) > 0
	}

	var findingText []string

	for _, finding := range packet.Findings {
		text := strings.TrimSpace(finding.Text)
		if text != "" {
			findingText = append(findingText, text)
		}
	}

	body := SubstantiveAnswerWithoutEchoedObjective(strings.Join(findingText, "\n"), objective)
	objectiveText := AnswerTextWithoutPathCitations(body)

	if readOnlyObjectiveAnswerEvidenceSupported(objectiveText, objective) {
		return true
	}

	if readOnlyEvidencePacketNamesObjectiveSource(packet, objective) &&
		readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(body, objective) {

		return true
	}

	if readOnlyEvidencePacketHasObjectiveScopedSourceSupport(packet, objective, findingText) {
		return true
	}

	return false
}

func readOnlyEvidencePacketHasObjectiveScopedSourceSupport(packet ReadOnlyEvidencePacket, objective string, findingText []string) bool {
	if len(findingText) == 0 {
		return false
	}

	if objectiveAsksImplementationShape(objective) {
		return len(readOnlyEvidencePacketObjectiveSourceOwnerEvidence(packet, objective)) > 0
	}

	for _, path := range packet.SourceBodyPaths {
		body := readOnlyEvidencePacketFindingBodyForPath(packet, path, findingText)
		if readOnlyEvidencePacketSourcePathScopedToObjective(path, objective, body) &&
			sourceBodyHasStrongObjectiveToken(body, objective) {

			return true
		}
	}

	return false
}

func readOnlyEvidencePacketObjectiveSourceOwnerEvidence(packet ReadOnlyEvidencePacket, objective string) []readOnlySourceOwnerEvidence {
	packet = normalizeReadOnlyEvidencePacket(packet)

	if !objectiveAsksImplementationShape(objective) {
		return nil
	}

	var findingText []string

	for _, finding := range packet.Findings {
		text := strings.TrimSpace(finding.Text)
		if text != "" {
			findingText = append(findingText, text)
		}
	}

	if len(findingText) == 0 {
		return nil
	}

	var evidence []readOnlySourceOwnerEvidence

	for _, path := range packet.SourceBodyPaths {
		body := readOnlyEvidencePacketFindingBodyForPath(packet, path, findingText)
		if !readOnlyEvidencePacketSourcePathScopedToObjective(path, objective, body) {
			continue
		}

		owner := readOnlySourceOwnerEvidenceForBody(path, "", objective, body)
		if !owner.OwnerEligible() {
			continue
		}

		evidence = append(evidence, owner)
	}

	return evidence
}

func readOnlyEvidencePacketSourcePathScopedToObjective(path string, objective string, body string) bool {
	path = normalizeReadOnlyEvidencePacketPath(path)
	if path == "" {
		return false
	}

	return implementationSourcePathOverlapsObjective(path, objective) ||
		sourceEvidencePathSharesObjectiveTerm(path, objective) ||
		candidateSourcePathStronglyOverlapsObjective(path, objective) ||
		sourceBodyHasStrongObjectiveToken(body, objective)
}

func readOnlyEvidencePacketFindingBodyForPath(packet ReadOnlyEvidencePacket, path string, fallback []string) string {
	path = normalizeReadOnlyEvidencePacketPath(path)

	var body []string

	for _, finding := range packet.Findings {
		findingPath := normalizeReadOnlyEvidencePacketPath(finding.Path)
		if findingPath != "" && findingPath != path {
			continue
		}

		text := strings.TrimSpace(finding.Text)
		if text == "" {
			continue
		}

		body = append(body, text)
	}

	if len(body) == 0 {
		body = fallback
	}

	return strings.Join(body, "\n")
}

func readOnlyEvidencePacketNamesObjectiveSource(packet ReadOnlyEvidencePacket, objective string) bool {
	objective = strings.ToLower(strings.TrimSpace(objective))
	if objective == "" {
		return false
	}

	for _, path := range packet.SourceBodyPaths {
		normalized := strings.ToLower(normalizeReadOnlyEvidencePacketPath(path))
		if normalized == "" {
			continue
		}

		base := strings.ToLower(filepath.Base(normalized))
		stem := strings.TrimSuffix(base, filepath.Ext(base))

		if strings.Contains(objective, normalized) ||
			(base != "" && strings.Contains(objective, base)) ||
			(stem != "" && strings.Contains(objective, stem)) {

			return true
		}
	}

	return false
}

func readOnlyEvidencePacketSourceBodyPaths(results []workspace.ToolResult) []string {
	paths := make([]string, 0, 4)

	paths = append(paths, concreteSourceEvidencePaths(results)...)
	paths = append(paths, sourceEvidenceBodyPaths(results)...)

	return readOnlyEvidencePacketSourceLikePaths(paths, 16)
}

func readOnlyEvidencePacketCandidatePaths(results []workspace.ToolResult) []string {
	paths := make([]string, 0, 8)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case "discovery_query", "context_query", "context_zoom", "context_impact", "structural_search":
			paths = append(paths, discoveryConsumptionCandidatesFromResult(result, 16)...)
			paths = append(paths, delegatedDiscoveryQueryEvidencePaths([]workspace.ToolResult{result}, 8)...)
		case "context_pack":
			paths = append(paths, contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, 16)...)
		case "search_content", "search_files":
			paths = append(paths, sourceLikePathsFromText(result.Summary+"\n"+result.Output, 64)...)
		default:
			paths = append(paths, agentutil.ReadPathsFromToolResult(result)...)
		}
	}

	return readOnlyEvidencePacketSourceLikePaths(paths, 16)
}

func readOnlyEvidencePacketInspectedPaths(sourceBodyPaths []string, candidatePaths []string, answer string) []string {
	paths := append([]string{}, sourceBodyPaths...)

	paths = append(paths, candidatePaths...)
	paths = append(paths, pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(answer))...)

	return readOnlyEvidencePacketSourceLikePaths(paths, 16)
}

func readOnlyEvidencePacketFindings(answer string, results []workspace.ToolResult, sourceBodyPaths []string, limit int) []ReadOnlyEvidenceFinding {
	if limit <= 0 {
		return nil
	}

	answer = stripNestedFallbackTemplate(stripDelegatedReadOnlyInternalPacket(answer))

	var findings []ReadOnlyEvidenceFinding

	seen := map[string]struct{}{}
	add := func(path string, text string, source string) {
		if len(findings) >= limit {
			return
		}

		text = strings.TrimSpace(strings.TrimPrefix(text, "- "))
		if readOnlyEvidencePacketTransportLine(text) || looksLikeLineOrientedSourceSnippetLine(text) {
			return
		}

		if source == workspace.ToolNameReadFile && lineHasLeadingSourceLineNumber(text) {
			return
		}

		if !delegatedReadOnlyFindingLineIsUseful(text) {
			return
		}

		if path == "" {
			for _, mentioned := range readOnlySourceLikePaths(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)), 4) {
				if slices.Contains(sourceBodyPaths, mentioned) {
					path = mentioned
					break
				}
			}
		}

		if path == "" && len(sourceBodyPaths) == 1 {
			path = sourceBodyPaths[0]
		}

		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path != "" && !slices.Contains(sourceBodyPaths, path) {
			return
		}

		key := path + "\x00" + text
		if _, ok := seen[key]; ok {
			return
		}

		seen[key] = struct{}{}

		findings = append(findings, ReadOnlyEvidenceFinding{Path: path, Text: text, Source: source})
	}

	for _, line := range delegatedReadOnlyFindingLines([]workspace.ToolResult{{Tool: "task", Output: answer}}, sourceBodyPaths, limit) {
		add("", line, "answer")
	}

	for _, result := range results {
		if len(findings) >= limit {
			break
		}

		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, line := range delegatedReadOnlyFindingLines([]workspace.ToolResult{result}, sourceBodyPaths, limit-len(findings)) {
			add("", line, strings.TrimSpace(result.Tool))
		}
	}

	return findings
}

func readOnlyEvidencePacketGaps(answer string, results []workspace.ToolResult) []string {
	var gaps []string

	answer = stripNestedFallbackTemplate(answer)

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "- "))
		lower := strings.ToLower(line)

		if strings.Contains(lower, "gap") || strings.Contains(lower, "missing claim") || strings.Contains(lower, "unsupported") {
			gaps = append(gaps, line)
		}
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			gaps = append(gaps, strings.TrimSpace(result.Error))
		}
	}

	return utilUniqueTrimmed(gaps, 4)
}

func readOnlyEvidencePacketConfidence(answer string, sourceBodyPaths []string, findings []ReadOnlyEvidenceFinding) string {
	lower := strings.ToLower(answer)
	switch {
	case strings.Contains(lower, "confidence: high"):
		return "high"
	case strings.Contains(lower, "confidence: low"):
		return "low"
	case strings.Contains(lower, "confidence: medium"):
		return "medium"
	case len(sourceBodyPaths) > 0 && len(findings) > 0:
		return "medium"
	case len(sourceBodyPaths) > 0:
		return "low"
	default:
		return ""
	}
}

func readOnlyEvidencePacketSourceLikePaths(paths []string, limit int) []string {
	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := normalizeReadOnlyEvidencePacketPath(raw)
		if path == "" || isNonSourceEvidenceArtifactPath(path) {
			continue
		}

		filtered = append(filtered, path)
	}

	return readOnlySourceLikePaths(filtered, limit)
}

func normalizeReadOnlyEvidencePacketPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	path := normalizeDelegatedDiscoveryEvidencePath(trimmed)

	if strings.HasPrefix(trimmed, "./") && strings.HasPrefix(path, "/") {
		path = strings.TrimPrefix(path, "/")
	}

	if strings.HasPrefix(trimmed, ".") && !strings.HasPrefix(trimmed, "./") && !strings.HasPrefix(path, ".") {
		path = "." + path
	}

	return normalizeRunnerPath(path)
}

func readOnlyEvidencePacketUsefulFindings(findings []ReadOnlyEvidenceFinding) []ReadOnlyEvidenceFinding {
	out := make([]ReadOnlyEvidenceFinding, 0, len(findings))
	seen := map[string]struct{}{}

	for _, finding := range findings {
		if strings.TrimSpace(finding.Text) == "" || readOnlyEvidencePacketTransportLine(finding.Text) {
			continue
		}

		key := finding.Path + "\x00" + finding.Text
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, finding)
	}

	return out
}

func readOnlyEvidencePacketTransportLine(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))

	return lower == "" ||
		strings.HasPrefix(lower, "partial findings from this turn") ||
		strings.HasPrefix(lower, "verified context:") ||
		strings.HasPrefix(lower, "grounded answer:") ||
		strings.HasPrefix(lower, "grounded findings:") ||
		strings.HasPrefix(lower, "read ")
}

func utilUniqueTrimmed(values []string, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)

		if limit > 0 && len(out) >= limit {
			break
		}
	}

	return out
}

func discoveryConsumptionCandidatesFromResult(result workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	if strings.TrimSpace(result.Error) != "" {
		return nil
	}

	if strings.TrimSpace(result.Tool) == workspace.ToolNameContextPack {
		return contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, max)
	}

	return promptEvidencePathsFromToolResults([]workspace.ToolResult{result}, max)
}


func readFileResultOnlyMentionsDiagnosticLogArtifacts(result workspace.ToolResult) bool {
	paths := agentutil.ReadPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !isNonSourceEvidenceArtifactPath(path) {
			return false
		}
	}

	return true
}


func isNonSourceEvidenceArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))

	lower = strings.TrimPrefix(lower, "./")

	switch {
	case lower == "":
		return false
	case isDiagnosticLogArtifactPath(lower):
		return true
	case lower == "transcripts" || strings.HasPrefix(lower, "transcripts/"):
		return true
	case lower == ".idiom" || strings.HasPrefix(lower, ".idiom/"):
		return true
	case lower == "idiom" || strings.HasPrefix(lower, "idiom/"):
		return true
	case lower == ".codex/discovery" || strings.HasPrefix(lower, ".codex/discovery/"):
		return true
	case lower == "testdata/swe/generated-logs" || strings.HasPrefix(lower, "testdata/swe/generated-logs/"):
		return true
	default:
		return false
	}
}


func delegatedDiscoveryPathPriority(path string) int {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(path, "internal/"):
		return 0
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
		return 1
	case strings.HasPrefix(path, "internal/"):
		return 2
	case strings.HasSuffix(path, "_test.go"):
		return 3
	default:
		return 4
	}
}


func delegatedDiscoveryLayerPriority(name string) int {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return 0
	case "go_read_symbol", "cpp_read_symbol", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return 1
	case "go_references", "cpp_references", "python_references", "ts_references", "rust_references", "java_references":
		return 2
	case "go_definition", "cpp_definition", "python_definition", "ts_definition", "rust_definition", "java_definition":
		return 3
	case "go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
		return 4
	case "related_files":
		return 5
	case workspace.ToolNameDependencyGraph:
		return 6
	case workspace.ToolNameCtagsSymbols:
		return 7
	case "target_listing":
		return 20
	case "project_behavior_digest", "project_context_digest":
		return 30
	default:
		return 10
	}
}


func delegatedDiscoveryLooksLikeEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	switch {
	case strings.HasPrefix(path, "search_content/"),
		strings.HasPrefix(path, "search_files/"),
		strings.HasPrefix(path, "read/"),
		strings.HasPrefix(path, "list_files/"):
		return false
	case pathparse.HasWorkspaceFileExtension(path):
		return true
	default:
		return false
	}
}


func preferNonTestEvidencePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	hasNonTestImplementation := false

	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			hasNonTestImplementation = true
			break
		}
	}

	if !hasNonTestImplementation {
		return paths
	}

	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		filtered = append(filtered, raw)
	}

	if len(filtered) == 0 {
		return paths
	}

	return filtered
}


func normalizeDelegatedDiscoveryEvidencePath(path string) string {
	path = normalizeWorkspacePathScanText(path)
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "`'\"()[]{}<>,.;")

	if path == "" {
		return ""
	}

	for _, ext := range []string{".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt"} {
		if idx := indexFold(path, ext); idx >= 0 {
			end := idx + len(ext)
			suffix := path[end:]

			switch {
			case suffix == "":
				return path[:end]
			case strings.HasPrefix(suffix, ":"):
				return path[:end]
			case strings.Trim(suffix, "`'\"") == "":
				return path[:end]
			}
		}
	}

	return path
}


func delegatedDiscoveryEvidenceLinePriority(line string) int {
	line = strings.TrimSpace(line)

	lower := strings.ToLower(line)

	switch {
	case line == "":
		return 5
	case strings.Contains(lower, "package "):
		return 5
	case closurePathScopedSymbolPattern.MatchString(line), closurePathCallPattern.MatchString(line):
		return 0
	case strings.Contains(lower, "func "):
		return 1
	case strings.Contains(lower, "type "):
		return 2
	default:
		return 3
	}
}


func delegatedDiscoveryQueryEvidencePaths(results []workspace.ToolResult, limit int) []string {
	if limit <= 0 {
		limit = 4
	}

	type discoveryPathCandidate struct {
		path  string
		score int
	}

	best := make(map[string]int)
	addCandidate := func(path string, layerName string, line string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" || strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, ".") {
			return
		}

		if !delegatedDiscoveryLooksLikeEvidencePath(path) {
			return
		}

		if !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go") {
			return
		}

		score := delegatedDiscoveryLayerPriority(layerName)*100 + delegatedDiscoveryEvidenceLinePriority(line)*10 + delegatedDiscoveryPathPriority(path)
		if prior, ok := best[path]; ok && prior <= score {
			return
		}

		best[path] = score
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameDiscoveryQuery || strings.TrimSpace(result.Output) == "" {
			continue
		}

		var summary struct {
			Evidence []struct {
				Name          string `json:"name"`
				Summary       string `json:"summary"`
				OutputExcerpt string `json:"output_excerpt"`
			} `json:"evidence"`
		}

		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}

		for _, evidence := range summary.Evidence {
			text := normalizeWorkspacePathScanText(strings.TrimSpace(evidence.Summary + "\n" + evidence.OutputExcerpt))
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				for _, path := range pathparse.MentionedWorkspacePaths(line) {
					addCandidate(path, evidence.Name, line)
				}

				for _, match := range delegatedPacketPathLinePattern.FindAllString(line, -1) {
					addCandidate(match, evidence.Name, line)
				}
			}
		}
	}

	candidates := make([]discoveryPathCandidate, 0, len(best))
	for path, score := range best {
		candidates = append(candidates, discoveryPathCandidate{path: path, score: score})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}

		return candidates[i].path < candidates[j].path
	})

	paths := make([]string, 0, limit)
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
		if len(paths) >= limit {
			return preferNonTestEvidencePaths(paths)
		}
	}

	return preferNonTestEvidencePaths(paths)
}


func delegatedDiscoveryLineIsBarePath(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}

	path := normalizeDelegatedDiscoveryEvidencePath(line)

	return path != "" && delegatedDiscoveryLooksLikeEvidencePath(path)
}


func delegatedDiscoveryFindingSummaryIsUseful(name string, text string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lower := strings.ToLower(strings.TrimSpace(text))

	if lower == "" {
		return false
	}

	if strings.Contains(lower, "context pack wrote") ||
		strings.Contains(lower, "no related go files matched") ||
		strings.Contains(lower, "no go symbols matched") ||
		strings.Contains(lower, "no symbols matched") ||
		strings.Contains(lower, "no matching symbol") ||
		strings.Contains(lower, "semantic fallback after gopls failure") ||
		strings.Contains(lower, "collected go dependency graph") {

		return false
	}

	if lowerName == workspace.ToolNameDependencyGraph && strings.HasPrefix(lower, "collected ") {
		return false
	}

	if lowerName == "text_anchor_search" && delegatedDiscoverySummaryIsBroadCountOnly(lower) {
		return false
	}

	return true
}


func delegatedDiscoveryFindingLines(summaries []delegatedDiscoverySummary, limit int) []string {
	var lines []string

	for _, summary := range summaries {
		for _, evidence := range summary.Evidence {
			name := strings.TrimSpace(evidence.Name)
			if name == "" || isDelegatedDiscoveryContextDigestLayer(name) || name == "target_listing" {
				continue
			}

			text := strings.TrimSpace(evidence.Summary)
			if text == "" || strings.TrimSpace(evidence.Error) != "" {
				continue
			}

			if !delegatedDiscoveryFindingSummaryIsUseful(name, text) {
				continue
			}

			lines = append(lines, name+": "+text)
			if limit > 0 && len(lines) >= limit {
				return lines
			}
		}
	}

	return lines
}


func uniqueDelegatedDiscoveryPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	fullByBase := make(map[string]string)
	ambiguousBase := make(map[string]bool)
	dirCounts := make(map[string]int)

	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if path == "" {
			continue
		}

		normalized = append(normalized, path)
		if strings.Contains(path, "/") {
			base := filepath.Base(path)
			dirCounts[filepath.Dir(path)]++

			if prior, ok := fullByBase[base]; ok && prior != path {
				ambiguousBase[base] = true
				continue
			}

			fullByBase[base] = path
		}
	}

	seen := make(map[string]struct{})
	unique := make([]string, 0, len(normalized))
	dominantDir := ""
	dominantCount := 0

	for dir, count := range dirCounts {
		if count > dominantCount || (count == dominantCount && (dominantDir == "" || dir < dominantDir)) {
			dominantDir = dir
			dominantCount = count
		}
	}

	for _, path := range normalized {
		if !strings.Contains(path, "/") {
			base := filepath.Base(path)
			if full, ok := fullByBase[base]; ok && !ambiguousBase[base] {
				path = full
			} else if dominantCount >= 2 {
				path = filepath.ToSlash(filepath.Join(dominantDir, base))
			}
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		unique = append(unique, path)
	}

	return unique
}


func delegatedDisplayQuestion(objective string) string {
	for _, line := range strings.Split(strings.TrimSpace(objective), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") ||
			strings.HasPrefix(lower, "scope guidance:") ||
			strings.HasPrefix(lower, "start from ") ||
			strings.HasPrefix(lower, "return inspected paths") ||
			strings.HasPrefix(lower, "- ") {

			continue
		}

		return line
	}

	return strings.TrimSpace(objective)
}


type delegatedDiscoverySummary struct {
	Purpose      string `json:"purpose"`
	ArtifactPath string `json:"artifact_path"`
	Evidence     []struct {
		Name          string `json:"name"`
		Tool          string `json:"tool"`
		Summary       string `json:"summary"`
		Error         string `json:"error,omitempty"`
		OutputExcerpt string `json:"output_excerpt,omitempty"`
	} `json:"evidence,omitempty"`
	Gaps []string `json:"gaps,omitempty"`
}


func delegatedReadOnlyFindingLineIsUseful(line string) bool {
	if _, ok := stripSourceEvidenceHandleLinePrefix(line); ok {
		return false
	}

	if line == "" ||
		delegatedDiscoveryLineIsBarePath(line) ||
		looksLikeLineOrientedSourceSnippetLine(line) ||
		sourceEvidenceLineIsBoilerplate(stripSourceEvidenceLinePrefix(line)) {

		return false
	}

	lower := strings.ToLower(line)
	if readOnlyToolControlGuidanceText(lower) ||
		strings.Contains(lower, "<<question>>") ||
		strings.Contains(lower, "completion gate") ||
		strings.Contains(lower, "missing completion evidence") ||
		strings.Contains(lower, "child task completed") ||
		strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "enough for a bounded answer") ||
		strings.Contains(lower, "safely expand beyond the inspected anchors") ||
		strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		lower == "context:" ||
		lower == "context" ||
		strings.Contains(lower, "top-level declaration") ||
		strings.HasPrefix(lower, "source-body evidence paths:") ||
		strings.HasPrefix(lower, "source-body evidence:") ||
		strings.HasPrefix(lower, "source body evidence paths:") ||
		strings.HasPrefix(lower, "source body evidence:") ||
		strings.HasPrefix(lower, "what this establishes:") ||
		strings.HasPrefix(lower, "outlined ") ||
		strings.HasPrefix(lower, "source:") ||
		strings.HasPrefix(lower, "question:") ||
		strings.HasPrefix(lower, "confidence:") {

		return false
	}

	return strings.Contains(line, ":") || strings.Contains(line, "`") || strings.Contains(line, ".")
}


func delegatedReadOnlyFindingLineSupportedByPaths(line string, supported map[string]struct{}) bool {
	mentionedAny := false
	supportedAny := false

	for _, path := range readOnlySourceLikePaths(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line)), 16) {
		mentionedAny = true

		if _, ok := supported[path]; ok {
			supportedAny = true
		}
	}

	return !mentionedAny || supportedAny
}


func delegatedReadOnlyFindingLines(results []workspace.ToolResult, supportedPaths []string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	supported := readOnlyPathSet(supportedPaths)

	var lines []string

	seen := map[string]struct{}{}

	for _, result := range results {
		output := compactOutput(result.Output, fallbackLineBudgetForTool(result.Tool))
		for _, raw := range strings.Split(output, "\n") {
			line := cleanDelegatedReadOnlyFindingLine(strings.TrimSpace(strings.TrimPrefix(raw, "- ")))
			if !delegatedReadOnlyFindingLineIsUseful(line) {
				continue
			}

			if !delegatedReadOnlyFindingLineSupportedByPaths(line, supported) {
				continue
			}

			if _, ok := seen[line]; ok {
				continue
			}

			seen[line] = struct{}{}
			lines = append(lines, line)

			if len(lines) >= limit {
				return lines
			}
		}
	}

	return lines
}


func sanitizeDelegatedReadOnlyResultOutput(result workspace.ToolResult) string {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return ""
	}

	if strings.TrimSpace(result.Tool) != workspace.ToolNameTask {
		return output
	}

	return stripDelegatedReadOnlyInternalPacket(output)
}


func normalizeDelegatedReadOnlyPacket(answer string) string {
	answer = strings.TrimSpace(normalizeWorkspacePathScanText(answer))
	if answer == "" {
		return ""
	}

	answer = stableHandlePathLinePattern.ReplaceAllString(answer, "$1")
	answer = stableHandleInlinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathLinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathQuotePattern.ReplaceAllString(answer, "$1")

	return answer
}


func delegatedReadOnlySourceBodyReadinessPaths(objective string, results []workspace.ToolResult) []string {
	paths := make([]string, 0, 4)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			if readOnlyEvidencePacketAnswerableForObjective(packet, objective) {
				paths = append(paths, packet.SourceBodyPaths...)
			}

			continue
		}

		sourcePaths := sourceBodyEvidencePathsFromDelegatedPacket(result.Output)
		if len(sourcePaths) == 0 && delegatedTaskOutputHasFindingEvidence(result.Output) {
			sourcePaths = groundedChildPacketEvidencePaths(objective, result.Output)
		}

		if len(sourcePaths) == 0 {
			continue
		}

		if childPacketContainsGroundedAnswerEvidence(objective, result.Output) || delegatedPacketAddressesObjective(objective, result.Output) {
			paths = append(paths, sourcePaths...)
		}
	}

	return readOnlySourceLikePaths(paths, 16)
}


func delegatedPacketNamesObjectiveSource(objective string, output string) bool {
	objective = strings.ToLower(strings.TrimSpace(objective))
	if objective == "" {
		return false
	}

	for _, path := range sourceBodyEvidencePathsFromDelegatedPacket(output) {
		normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if normalized == "" {
			continue
		}

		base := strings.ToLower(filepath.Base(normalized))
		if strings.Contains(objective, normalized) || (base != "" && strings.Contains(objective, base)) {
			return true
		}
	}

	return false
}


func delegatedPacketAddressesObjective(objective string, output string) bool {
	objective = strings.TrimSpace(objective)
	output = strings.TrimSpace(output)

	if objective == "" || output == "" {
		return false
	}

	body := SubstantiveAnswerWithoutEchoedObjective(stripDelegatedReadOnlyInternalPacket(output), objective)
	if strings.TrimSpace(body) == "" {
		return false
	}

	objectiveText := AnswerTextWithoutPathCitations(body)
	if readOnlyObjectiveAnswerEvidenceSupported(objectiveText, objective) {
		return true
	}

	if delegatedPacketNamesObjectiveSource(objective, output) &&
		readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(objectiveText, objective) {

		return true
	}

	return false
}


func childPacketContainsGroundedAnswerEvidence(objective string, output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}

	lower := strings.ToLower(output)
	if readOnlyToolControlGuidanceText(lower) {
		return false
	}

	if len(sourceBodyEvidencePathsFromDelegatedPacket(output)) == 0 {
		return false
	}

	if !containsAny(lower,
		"grounded answer",
		"core files:",
		"primary files:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"important flow:",
		"key findings:",
		"\n- read ",
		"\n- found ",
		"\n- located ",
		"\n- calls ",
		"\n- uses ",
		"\n- references ",
	) {

		return false
	}

	if strings.TrimSpace(objective) == "" {
		return true
	}

	return delegatedPacketAddressesObjective(objective, output)
}


func stripNestedFallbackTemplate(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || !strings.HasPrefix(trimmed, "Partial findings from this turn") {
		return s
	}

	// Drop the leading "Partial findings from this turn (...):" line plus
	// the immediately-following blank line, leaving just the substance.
	if idx := strings.Index(trimmed, "\n"); idx >= 0 {
		trimmed = strings.TrimLeft(trimmed[idx+1:], "\n")
	}

	// Drop the trailing "Recommended next step:" footer; that's
	// scaffolding for the user, not evidence.
	if idx := strings.Index(trimmed, "\nRecommended next step:"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}

	// Drop the inner "Verified context:" header. The outer fallback adds
	// its own header; nested verified-context labels make the answer read
	// like two stacked reports.
	trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed), "Verified context:"))
	// Drop a trailing "Final blocker:" if it's just a harness rejection --
	// that signal has already been recomputed by the outer template.
	if idx := strings.Index(trimmed, "\nFinal blocker:"); idx >= 0 {
		head := strings.TrimSpace(trimmed[:idx])
		tail := strings.TrimSpace(trimmed[idx+len("\nFinal blocker:"):])
		// Look one bullet ahead and drop the section if its content is a
		// known harness-only blocker; otherwise keep it.
		if firstBullet := firstBulletText(tail); isHarnessRejectionSummary(firstBullet) || isHarnessRejectionErrorText(tail) {
			trimmed = head
		}
	}

	return trimmed
}


func delegatedReadOnlyLiteralSymbolCandidate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)

	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")
	value = strings.TrimSpace(strings.TrimPrefix(value, "./"))

	if value == "" || pathparse.LooksLikeWorkspacePathToken(strings.ToLower(value)) {
		return "", false
	}

	if len(value) < 4 {
		return "", false
	}

	for i, r := range value {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return "", false
		}

		break
	}

	hasLetter := false

	for _, r := range value {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r), r == '_':
		default:
			return "", false
		}
	}

	if !hasLetter {
		return "", false
	}

	if !delegatedReadOnlyLooksCodeShapedSymbol(value) {
		return "", false
	}

	lower := strings.ToLower(value)
	if delegatedDiscoveryStopWord(lower, delegatedDiscoveryStopWordSymbol) {
		return "", false
	}

	return value, true
}


func nonArtifactEvidencePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || isNonSourceEvidenceArtifactPath(path) {
			continue
		}

		out = append(out, path)
	}

	return out
}


func planThenExecuteEvidenceRank(level PlanThenExecuteSourceEvidenceLevel) int {
	switch level {
	case planThenExecuteEvidenceAnchorReady, planThenExecuteEvidenceExactAnchorContract, planThenExecuteEvidenceNewFileContract:
		return 3
	case planThenExecuteEvidenceBodyWindow:
		return 2
	case planThenExecuteEvidenceSourceBodyPending, planThenExecuteEvidencePathSeen:
		return 1
	default:
		return 0
	}
}


type planThenExecuteSourceEvidence struct {
	Path   string
	Level  PlanThenExecuteSourceEvidenceLevel
	Source string
	Detail string
}


type PlanThenExecuteSourceEvidenceLevel string

const (
	planThenExecuteEvidenceNone                PlanThenExecuteSourceEvidenceLevel = ""
	planThenExecuteEvidencePathSeen            PlanThenExecuteSourceEvidenceLevel = "path_seen"
	planThenExecuteEvidenceSourceBodyPending   PlanThenExecuteSourceEvidenceLevel = "source_body_pending"
	planThenExecuteEvidenceBodyWindow          PlanThenExecuteSourceEvidenceLevel = "body_window"
	planThenExecuteEvidenceAnchorReady         PlanThenExecuteSourceEvidenceLevel = "anchor_ready"
	planThenExecuteEvidenceExactAnchorContract PlanThenExecuteSourceEvidenceLevel = "exact_anchor_contract"
	planThenExecuteEvidenceNewFileContract     PlanThenExecuteSourceEvidenceLevel = "new_file_contract"
)


func planThenExecuteRequiredSourceEvidenceForPath(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, path string) planThenExecuteSourceEvidence {
	path = normalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}
	}

	best := planThenExecuteSourceEvidence{Path: path}
	for _, result := range results {
		evidence := planThenExecuteToolResultSourceEvidence(result, path)
		if planThenExecuteEvidenceRank(evidence.Level) > planThenExecuteEvidenceRank(best.Level) {
			best = evidence
		}
	}

	return best
}

