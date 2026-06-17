package agent

import (
	"idiom/internal/contracts/pathparse"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent/agentutil"
	"strings"
)

// AnswerComposer is the presentation boundary between deterministic evidence
// packets and user-facing prose. Runner/discovery code can stay telemetry-rich;
// final answers should read like an explanation, not like an eval artifact.
type AnswerComposer struct{}

type AnswerComposerInput struct {
	Question      string
	EvidencePaths []string
	Summaries     []delegatedDiscoverySummary
}

func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool) {
	question := strings.TrimSpace(input.Question)
	paths := uniqueDelegatedDiscoveryPaths(input.EvidencePaths)
	lines := decoratedDiscoveryFindingLines(input.Summaries, 4)

	if len(input.Summaries) == 0 || len(lines) == 0 {
		return "", false
	}

	return composeGenericEvidenceAnswer(question, paths, lines), true
}

func composeGenericEvidenceAnswer(question string, paths []string, lines []string) string {
	var b strings.Builder

	if len(paths) > 0 {
		b.WriteString("The strongest implementation evidence is in ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
	} else {
		b.WriteString("The inspected implementation evidence is enough to give a bounded answer.")
	}

	if strings.TrimSpace(question) != "" {
		b.WriteString(" Use those findings as the source of truth for `")
		b.WriteString(strings.TrimSpace(question))
		b.WriteString("`.")
	}

	b.WriteString("\n\n")
	b.WriteString("What this establishes:\n")

	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func decoratedDiscoveryFindingLines(summaries []delegatedDiscoverySummary, limit int) []string {
	raw := delegatedDiscoveryFindingLines(summaries, limit)
	lines := make([]string, 0, len(raw))

	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		name, text, ok := strings.Cut(line, ":")
		if ok {
			line = humanDiscoveryLayerName(name) + ": " + cleanDiscoveryFindingText(text)
		} else {
			line = cleanDiscoveryFindingText(line)
		}

		if line == "" || strings.Contains(strings.ToLower(line), "open a returned handle") || decoratedDiscoveryFindingLineIsToolInventory(line) {
			continue
		}

		lines = append(lines, line)
		if limit > 0 && len(lines) >= limit {
			return lines
		}
	}

	return lines
}

func decoratedDiscoveryFindingLineIsToolInventory(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}

	if strings.HasPrefix(lower, "symbol index: indexed symbols ") ||
		strings.HasPrefix(lower, "symbol index: no ") ||
		strings.HasPrefix(lower, "dependency graph: dependency graph unavailable") {

		return true
	}

	if strings.HasPrefix(lower, "text anchors: found ") &&
		strings.Contains(lower, " matching line") {

		return true
	}

	if strings.HasPrefix(lower, "related files: found ") &&
		(strings.Contains(lower, " related ") || strings.Contains(lower, " file")) {

		return true
	}

	return false
}

func humanDiscoveryLayerName(name string) string {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return "Text anchors"
	case "dependency_graph":
		return "Dependency graph"
	case "related_files":
		return "Related files"
	case "go_read_symbol":
		return "Symbol read"
	case "go_workspace_symbol", "ctags_symbols":
		return "Symbol index"
	case "structural_search":
		return "Structural search"
	case "history_range", "history_symbol":
		return "History"
	case "blame_range":
		return "Blame"
	case "semgrep_scan":
		return "Static scan"
	case "joern_scan":
		return "Code property graph"
	default:
		return strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	}
}

func cleanDiscoveryFindingText(text string) string {
	text = strings.TrimSpace(text)
	for _, suffix := range []string{
		" Bounded-read capability is available on returned handles, or narrow the pattern to see more.",
		" Open a returned handle with read_file.",
		" Bounded-read capability is available on a returned handle to inspect a file.",
	} {
		text = strings.ReplaceAll(text, suffix, ".")
	}

	text = strings.ReplaceAll(text, "  ", " ")

	return strings.TrimSpace(text)
}

func formatInlinePathList(paths []string, max int) string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if max > 0 && len(paths) > max {
		paths = paths[:max]
	}

	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		path = cleanInlineEvidencePath(path)
		if path == "" {
			continue
		}

		parts = append(parts, "`"+path+"`")
	}

	return strings.Join(parts, ", ")
}

func cleanInlineEvidencePath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "`", ""))
	path = strings.TrimRight(path, ".,;:")

	return strings.TrimSpace(path)
}

func routeSummaryFromToolOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "route_summary:"); ok {
			return strings.TrimSpace(value)
		}
	}

	return ""
}


func sourceLikePathsFromText(text string, max int) []string {
	if max <= 0 {
		max = 6
	}

	paths := make([]string, 0, max)
	seen := map[string]struct{}{}

	for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" || agentutil.IsNonSourceEvidenceArtifactPath(path) || !delegatedDiscoveryLooksLikeEvidencePath(path) {
			continue
		}

		if strings.Contains(path, "testdata/swe/generated-logs") {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)

		if len(paths) >= max {
			break
		}
	}

	return paths
}


func conversationFallbackEvidenceAnchor(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return ""
	}

	if routeSummary := routeSummaryFromToolOutput(result.Output); routeSummary != "" {
		return routeSummary
	}

	summary := strings.TrimSpace(result.Summary)
	if summary == "" || isHarnessRejectionSummary(summary) {
		return ""
	}

	switch strings.TrimSpace(result.Tool) {
	case "search_content":
		if paths := sourceLikePathsFromText(result.Output, 6); len(paths) > 0 {
			return "Search hits included " + formatInlinePathList(paths, 6)
		}

		return summary
	case "context_pack", "context_query", "context_zoom", "code_navigate", "read_file", workspace.ToolNamePrefetch, "search_files":
		if agentutil.ReadStyleSourceBodyTool(result.Tool) {
			paths := agentutil.ReadPathsFromToolResult(result)
			if len(paths) > 0 && len(nonArtifactEvidencePaths(paths)) == 0 {
				return ""
			}
		}

		return summary
	default:
		return ""
	}
}


func isDeterministicFallbackAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	return strings.HasPrefix(lower, "partial findings from this turn")
}


func looksLikeSubstantiveFinalAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	if len(answer) < 80 {
		return false
	}

	lower := strings.ToLower(answer)
	if strings.HasPrefix(lower, "for `") && strings.Contains(lower, "the gathered evidence points to these implementation surfaces") {
		return false
	}

	if strings.Contains(lower, "\nevidence:\n- text anchors:") {
		return false
	}

	return strings.Contains(answer, ".") || strings.Contains(answer, "\n- ")
}


func stripInternalEvidenceAppendix(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	cut := len(answer)
	for _, marker := range []string{
		"\n\nAdditional inspected paths:",
		"\nAdditional inspected paths:",
		"\n\nExact probes already inspected:",
		"\nExact probes already inspected:",
		"\n\nInspected paths:",
		"\nInspected paths:",
		"\n\nKey findings:",
		"\nKey findings:",
		"\n\nConfidence:",
		"\nConfidence:",
		"\n\nSource: ",
		"\nSource: ",
	} {
		if idx := strings.Index(answer, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}

	return strings.TrimSpace(answer[:cut])
}


func cleanedSubstantiveFinalAnswer(answer string) (string, bool) {
	cleaned := stripInternalEvidenceAppendix(answer)
	if looksLikeLowValueSourceInventoryNonAnswer(cleaned) || !looksLikeSubstantiveFinalAnswer(cleaned) || finalAnswerNeedsDecoration(cleaned) || looksLikeGenericEvidenceComposerAnswer(cleaned) {
		return "", false
	}

	return cleaned, true
}


func genericEvidenceAnswerIncludesLineOrientedSourceBody(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if !strings.Contains(lower, "what this establishes:") {
		return false
	}

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)

		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))

		if line == "" {
			continue
		}

		colon := strings.IndexByte(line, ':')
		if colon <= 0 || colon > 6 {
			continue
		}

		lineNumber := strings.TrimSpace(line[:colon])
		if lineNumber == "" {
			continue
		}

		allDigits := true

		for _, ch := range lineNumber {
			if ch < '0' || ch > '9' {
				allDigits = false
				break
			}
		}

		if allDigits {
			return true
		}
	}

	return false
}


func looksLikeSourceBodyFallbackAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))

	return strings.Contains(lower, "the inspected source in") &&
		strings.Contains(lower, "no files were changed")
}


func looksLikeGenericEvidenceComposerAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		strings.Contains(lower, "what this establishes:") ||
		strings.Contains(lower, "the retained source context identifies") ||
		looksLikeSourceBodyFallbackAnswer(answer)
}


func finalAnswerNeedsDecoration(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	if looksLikeRawToolResultTranscript(answer) {
		return true
	}

	if looksLikeLineOrientedSourceSnippetAnswer(answer) {
		return true
	}

	if looksLikeTextualToolCall(answer) {
		return true
	}

	if looksLikeStructuredAnswerEnvelope(lower) {
		return true
	}

	if strings.Contains(lower, "<invoke") ||
		strings.HasPrefix(lower, "what i found:") ||
		strings.HasPrefix(lower, "partial findings from this turn") ||
		strings.HasPrefix(lower, "## grounded answer") ||
		strings.HasPrefix(lower, "question:") ||
		strings.Contains(lower, "source: delegated read-only evidence packet") ||
		strings.Contains(lower, "the gathered evidence points to these implementation surfaces") ||
		strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "additional inspected paths:") ||
		strings.Contains(lower, "exact probes already inspected:") {

		return true
	}

	return agentutil.ContainsAny(lower,
		"\nevidence:\n- text anchors:",
		"### evidence highlights",
		"### decision guidance",
		"discovery packet",
		"relevant source hits",
		"evidence used:",
		"inspected paths:",
		"key findings:",
		"text_anchor_search:",
		"go_read_symbol:",
		"go_workspace_symbol:",
		"dependency_graph:",
		"related_files:",
		".idiom/discovery/queries/",
	)
}


func bestObjectiveAnswerableTaskResultOutput(objective string, results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if !ReadOnlyTaskResultAnswerableForObjective(result, objective) {
			continue
		}

		output := strings.TrimSpace(result.Output)
		if output == "" {
			continue
		}

		return output, true
	}

	return "", false
}


func presentableExactProbeResult(result workspace.ToolResult) bool {
	switch strings.TrimSpace(result.Tool) {
	case "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_extract_body", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return true
	case "code_navigate", "read_only_capability":
		return agentutil.RoutedSourceReadOutputHasBody(result.Output) && toolResultHasNonArtifactReadPath(result)
	default:
		return false
	}
}


func stripPresentableSourceObjectiveEcho(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if !strings.HasPrefix(trimmed, "For `") {
		return trimmed
	}

	rest := strings.TrimPrefix(trimmed, "For `")
	end := strings.Index(rest, "`, ")

	if end < 0 {
		return trimmed
	}

	return strings.TrimSpace(rest[end+len("`, "):])
}

