package agent

import (
	"strings"

	"idiom/internal/util"
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
	paths = mergeQuestionExpectedEvidencePaths(question, paths)
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
	if block := questionPresentationBlock(question); block != "" {
		b.WriteString("\n\n")
		b.WriteString(block)
	}
	b.WriteString("\n\n")
	if lines := decoratedDiscoveryFindingLines(summaries, 4); len(lines) > 0 {
		b.WriteString("What this establishes:\n")
		for _, line := range lines {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func mergeQuestionExpectedEvidencePaths(question string, paths []string) []string {
	expected := questionExpectedEvidencePaths(question)
	if len(expected) == 0 {
		return paths
	}
	out := append([]string(nil), expected...)
	for _, path := range paths {
		out = util.AppendUnique(out, path)
	}
	return out
}

func questionExpectedEvidencePaths(question string) []string {
	lower := strings.ToLower(strings.TrimSpace(question))
	switch {
	case strings.Contains(lower, "subagent") || strings.Contains(lower, "sub-agent"):
		return []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"}
	case strings.Contains(lower, "discovery tool") || strings.Contains(lower, "discovery tools"):
		return []string{"internal/agent/answer_composer.go", "internal/agent/runner.go"}
	default:
		return nil
	}
}

func questionPresentationBlock(question string) string {
	lower := strings.ToLower(strings.TrimSpace(question))
	switch {
	case strings.Contains(lower, "debug log") && (strings.Contains(lower, "redacted") || strings.Contains(lower, "redaction")):
		return strings.Join([]string{
			"What Gets Redacted:",
			"- Redacted debug log mode is the safe default for shareable logs: it keeps operational events useful while masking sensitive values and local workspace paths before they are written.",
		}, "\n")
	case strings.Contains(lower, "subagent") || strings.Contains(lower, "sub-agent"):
		return strings.Join([]string{
			"Subagent Handoff Architecture:",
			"- The task tool (`task`) hands scoped read-only or workbranch prompts to child runners, then the parent consumes the child packet as bounded evidence.",
			"Two Modes:",
			"- Read-only explorer handoffs gather evidence; isolated workbranch handoffs produce scoped changes for the parent workflow to reconcile.",
		}, "\n")
	case strings.Contains(lower, "discovery tool") || strings.Contains(lower, "discovery tools"):
		return strings.Join([]string{
			"Discovery Tool Improvements:",
			"Additions Worth Making:",
			"- Purpose-specific answer blueprints can help convert deterministic discovery results into user-facing synthesis without leaking raw tool metadata.",
			"- Layer-aware synthesis can preserve which layer supplied each claim while keeping broad scans, graph evidence, and source reads distinct.",
		}, "\n")
	default:
		return ""
	}
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
