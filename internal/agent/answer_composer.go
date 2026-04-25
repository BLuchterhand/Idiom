package agent

import (
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
	if len(input.Summaries) == 0 {
		return "", false
	}
	return composeGenericEvidenceAnswer(question, input.Summaries, paths), true
}

func composeGenericEvidenceAnswer(question string, summaries []delegatedDiscoverySummary, paths []string) string {
	var b strings.Builder
	b.WriteString("## Grounded Answer\n\n")
	if strings.TrimSpace(question) != "" {
		b.WriteString("For `")
		b.WriteString(strings.TrimSpace(question))
		b.WriteString("`, the inspected evidence points to the following implementation surfaces and findings.\n\n")
	} else {
		b.WriteString("The inspected evidence points to the following implementation surfaces and findings.\n\n")
	}
	if lines := decoratedDiscoveryFindingLines(summaries, 4); len(lines) > 0 {
		b.WriteString("### Evidence Highlights\n")
		for _, line := range lines {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("### Decision Guidance\n")
	b.WriteString("- Answer from the strongest evidence already gathered; continue only if one exact missing claim remains and one exact follow-up probe would materially change the answer.\n")
	if len(paths) > 0 {
		b.WriteString("\nPrimary files: ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
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
		if line == "" || strings.Contains(strings.ToLower(line), "open a returned handle") {
			continue
		}
		lines = append(lines, line)
		if limit > 0 && len(lines) >= limit {
			return lines
		}
	}
	return lines
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
		" Use read_file on a returned handle, or narrow the pattern to see more.",
		" Open a returned handle with read_file.",
		" Use read_file on a returned handle to inspect a file.",
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
		parts = append(parts, "`"+path+"`")
	}
	return strings.Join(parts, ", ")
}
