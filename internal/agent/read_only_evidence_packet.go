package agent

import (
	"encoding/json"
	"slices"
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/workspace"
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
	if answer = strings.TrimSpace(stripDelegatedReadOnlyInternalPacket(answer)); answer != "" {
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
	var sourcePaths []string
	var candidatePaths []string
	var findings []ReadOnlyEvidenceFinding
	for _, packet := range packets {
		packet = normalizeReadOnlyEvidencePacket(packet)
		if !readOnlyEvidencePacketAnswerable(packet) {
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
			paths = append(paths, readPathsFromToolResult(result)...)
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
		if readOnlyEvidencePacketTransportLine(text) {
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
		strings.HasPrefix(lower, "read ") ||
		strings.HasPrefix(lower, "found ") ||
		strings.HasPrefix(lower, "located ")
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
