package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
)

const (
	reasoningAnswerSalvageMinChars = 40
	reasoningAnswerSalvageMaxChars = 4000
)

// salvageAnswerFromReasoning extracts answer prose from the thinking block
// when the model produced empty visible content but substantial reasoning.
// This happens with Qwen in thinking-enabled mode when the model puts its
// entire response into the reasoning block and leaves Content empty.
func (r *Runner) salvageAnswerFromReasoning(reply llama.ChatMessage, results []workspace.ToolResult) (string, bool) {
	if r == nil || strings.TrimSpace(reply.Content) != "" {
		return "", false
	}
	reasoning := strings.TrimSpace(reply.ReasoningContent)
	if reasoning == "" {
		return "", false
	}
	// Only apply to lanes that can produce a direct answer (conversation,
	// read-only). Mutation lanes need tool-call-shaped recovery, not prose.
	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}
	// Reasoning must be substantial enough to plausibly contain an answer.
	if len(reasoning) < 200 {
		return "", false
	}
	answer := extractAnswerProseFromReasoning(reasoning)
	if len(strings.TrimSpace(answer)) < reasoningAnswerSalvageMinChars {
		return "", false
	}
	if !reasoningAnswerCandidateIsClean(answer) {
		return "", false
	}
	if blockerKind, _ := r.finalAnswerBlockerKind(answer, results); blockerKind != "" {
		return "", false
	}
	return answer, true
}

// extractAnswerProseFromReasoning extracts the most likely answer prose
// from a model thinking block. Qwen thinking output typically contains
// internal monologue, code inspection, tool-call syntax, and a synthesis
// paragraph where the model works out what to tell the user. This function
// strips the scaffolding and returns the synthesis.
func extractAnswerProseFromReasoning(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}
	if sections, hasMarkers := markedReasoningAnswerSections(reasoning); hasMarkers {
		return selectMarkedReasoningAnswerSection(sections)
	}

	// Phase 1: strip fenced code blocks, Qwen XML tool calls,
	// tool-call wrappers, and internal-monologue lines.
	var cleaned strings.Builder
	inFence := false
	for _, line := range strings.Split(reasoning, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if looksLikeQwenThinkingToolCall(trimmed) {
			continue
		}
		if trimmed == "" {
			cleaned.WriteString("\n")
			continue
		}
		if looksLikeToolCallWrapper(trimmed) {
			continue
		}
		if looksLikeInternalMonologue(trimmed) {
			continue
		}
		cleaned.WriteString(line)
		cleaned.WriteString("\n")
	}
	text := cleaned.String()

	// Phase 2: score each paragraph by how answer-like it is and
	// select the best candidate.
	paragraphs := splitReasoningParagraphs(text)
	if len(paragraphs) == 0 {
		return ""
	}

	best := ""
	bestScore := 0
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if len(p) < 40 {
			continue
		}
		if looksLikeMetadataBlock(p) {
			continue
		}
		if looksLikeUnfinishedReasoningPlan(p) {
			continue
		}
		score := answerParagraphScore(p)
		if score > bestScore {
			bestScore = score
			best = p
		}
	}
	if !reasoningAnswerCandidateIsClean(best) {
		return ""
	}
	return best
}

func markedReasoningAnswerSections(reasoning string) ([]string, bool) {
	var sections []string
	var current strings.Builder
	active := false
	seenMarker := false
	for _, line := range strings.Split(reasoning, "\n") {
		if rest, ok := finalAnswerSectionMarkerRest(line); ok {
			seenMarker = true
			if active {
				sections = append(sections, current.String())
				current.Reset()
			}
			active = true
			if rest != "" {
				current.WriteString(rest)
				current.WriteString("\n")
			}
			continue
		}
		if active {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	if active {
		sections = append(sections, current.String())
	}
	return sections, seenMarker
}

func finalAnswerSectionMarkerRest(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{"[final answer]", "[answer]"} {
		if strings.HasPrefix(lower, marker) {
			rest := strings.TrimSpace(trimmed[len(marker):])
			rest = strings.TrimLeft(rest, ":.- ")
			return strings.TrimSpace(rest), true
		}
	}
	for _, marker := range []string{"final answer:", "answer:"} {
		if strings.HasPrefix(lower, marker) {
			return strings.TrimSpace(trimmed[len(marker):]), true
		}
	}
	return "", false
}

func selectMarkedReasoningAnswerSection(sections []string) string {
	var selected string
	var selectedKey string
	for _, section := range sections {
		candidate := strings.TrimSpace(section)
		if !reasoningAnswerCandidateIsClean(candidate) {
			return ""
		}
		key := normalizeReasoningAnswerForDedupe(candidate)
		if key == "" {
			return ""
		}
		if selected == "" {
			selected = candidate
			selectedKey = key
			continue
		}
		if key != selectedKey {
			return ""
		}
	}
	return selected
}

func normalizeReasoningAnswerForDedupe(answer string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(answer)), " "))
}

func reasoningAnswerCandidateIsClean(answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return false
	}
	if len(answer) > reasoningAnswerSalvageMaxChars {
		return false
	}
	if finalAnswerSectionMarkerCount(answer) > 0 {
		return false
	}
	if reasoningAnswerLooksTruncated(answer) {
		return false
	}
	if looksLikeTextualToolCall(answer) ||
		looksLikeRawToolResultTranscript(answer) ||
		looksLikeUnexecutedToolRequestAnswer(answer) ||
		looksLikeNonAnswerProse(answer) {
		return false
	}
	return true
}

func finalAnswerSectionMarkerCount(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if _, ok := finalAnswerSectionMarkerRest(line); ok {
			count++
		}
	}
	return count
}

func reasoningAnswerLooksTruncated(answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return true
	}
	last := answer[len(answer)-1]
	switch last {
	case '.', '!', '?', ')', ']', '`', '"', '\'':
		return false
	default:
		return true
	}
}

func splitReasoningParagraphs(text string) []string {
	raw := strings.Split(text, "\n\n")
	var out []string
	for _, block := range raw {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		var joined []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			joined = append(joined, line)
		}
		if len(joined) > 0 {
			out = append(out, strings.Join(joined, " "))
		}
	}
	return out
}

func answerParagraphScore(p string) int {
	score := 0
	lower := strings.ToLower(p)

	for _, word := range []string{
		"because", "therefore", "should", "would", "could",
		"recommend", "suggest", "however", "although",
		"benefit", "tradeoff", "alternative", "approach",
		"important", "useful", "valuable", "better",
		"evidence", "system", "struct", "field",
	} {
		if strings.Contains(lower, word) {
			score++
		}
	}

	if strings.Contains(lower, "you") || strings.Contains(lower, "your") {
		score += 3
	}

	if strings.HasSuffix(strings.TrimSpace(p), ".") ||
		strings.HasSuffix(strings.TrimSpace(p), "?") ||
		strings.HasSuffix(strings.TrimSpace(p), "!") {
		score += 2
	}

	for _, prefix := range []string{
		"now i", "let me", "i should", "i need to", "i can see",
		"i think", "i'll", "i will", "looking at", "checking",
		"first,", "next,", "then,", "finally,",
	} {
		if strings.HasPrefix(lower, prefix) {
			score -= 5
		}
	}

	if len(p) > 100 {
		score += 2
	}
	if len(p) > 200 {
		score += 1
	}

	return score
}

func looksLikeQwenThinkingToolCall(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "<") {
		return false
	}
	rest := trimmed[1:]
	if idx := strings.IndexAny(rest, " />"); idx > 0 {
		name := strings.TrimSpace(rest[:idx])
		if knownToolName(name) {
			return true
		}
	}
	if strings.HasPrefix(trimmed, "</") {
		return true
	}
	return false
}

func knownToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "read_file", "search_content", "search_files",
		"context_pack", "context_query", "context_zoom", "context_status",
		"code_navigate", "discovery_query", "structural_search",
		"edit_file", "write_file", "append_file", "apply_patch",
		"shell", "source_sanity", "submit_plan", "task",
		"task_ledger", "mkdir", "note":
		return true
	default:
		return false
	}
}

func looksLikeToolCallWrapper(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	if strings.Contains(trimmed, "<tool_call>") || strings.Contains(trimmed, "</tool_call>") {
		return true
	}
	if strings.Contains(trimmed, "<function=") || strings.Contains(trimmed, "</function>") {
		return true
	}
	if strings.HasPrefix(trimmed, "tool:") || strings.HasPrefix(trimmed, "TOOL:") {
		return true
	}
	if strings.HasPrefix(trimmed, `{"tool":`) {
		return true
	}
	return false
}

func looksLikeInternalMonologue(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{
		"now i", "let me", "i should", "i need to", "i can see",
		"i think", "i'll", "i will", "i'm going to", "i would",
		"i could", "i might", "i have", "i've", "i had", "i'd",
		"looking at", "checking", "i notice", "i see", "i found",
		"first,", "next,", "then,", "finally,", "also,",
		"this means", "so the", "so i", "so we",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func looksLikeMetadataBlock(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return true
	}
	if looksLikeStructuredInternalState(p) {
		return true
	}
	if !strings.Contains(p, " ") && len(p) < 20 {
		return true
	}
	if len(p) > 10 && strings.ToUpper(p) == p {
		return true
	}
	alpha := 0
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			alpha++
		}
	}
	if alpha < 3 && len(p) > 2 {
		return true
	}
	return false
}

func looksLikeUnfinishedReasoningPlan(p string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	if lower == "" {
		return true
	}
	taskFramingPrefixes := []string{
		"the user wants me",
		"the user asked me",
		"user wants me",
		"user asked me",
	}
	for _, prefix := range taskFramingPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return containsAny(lower,
				" i need to ",
				" i should ",
				" i have to ",
				" need to ",
				" should ",
				" find ",
				" inspect ",
				" check ",
				" debug ",
				" understand ",
				" gather ",
			)
		}
	}
	if strings.Contains(lower, " i need to ") || strings.Contains(lower, " i should ") {
		return !looksLikeGroundedAnswer(lower)
	}
	return false
}

func looksLikeGroundedAnswer(lower string) bool {
	return strings.Contains(lower, "root cause") ||
		strings.Contains(lower, "because") ||
		strings.Contains(lower, "evidence") ||
		strings.Contains(lower, ".go:") ||
		strings.Contains(lower, ".py:") ||
		strings.Contains(lower, ".ts:") ||
		strings.Contains(lower, ".rs:") ||
		strings.Contains(lower, ".java:") ||
		strings.Contains(lower, ".cpp:") ||
		strings.Contains(lower, ".h:")
}

func looksLikeStructuredInternalState(p string) bool {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"current_state:", "tool_manifest:", "allowed_tools:", "forbidden_tools:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	first := trimmed[0]
	if first != '{' && first != '[' {
		return false
	}
	hasInternalStateMarker := strings.Contains(lower, `"current_state"`) ||
		strings.Contains(lower, `"tool_manifest"`) ||
		strings.Contains(lower, `"allowed_tools"`) ||
		strings.Contains(lower, `"forbidden_tools"`)
	return hasInternalStateMarker
}
