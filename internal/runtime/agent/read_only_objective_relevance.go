package agent

import (
	"idiom/internal/contracts/pathparse"
	"idiom/internal/capabilities/workspace"
	"unicode"
	"strconv"
	"regexp"
	"slices"
	"strings"
)

func sourceEvidenceObjectiveLineTerms(objective string) []string {
	lower := strings.ToLower(strings.TrimSpace(objective))
	terms := readOnlySearchHitObjectiveTerms(objective)
	add := func(values ...string) {
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" || slices.Contains(terms, value) {
				continue
			}

			terms = append(terms, value)
		}
	}
	words := objectiveWordSet(lower)

	if _, ok := words["up"]; ok {
		add("keyup", "up")
	}

	if _, ok := words["down"]; ok {
		add("keydown", "down")
	}

	if _, ok := words["enter"]; ok {
		add("keyenter", "enter")
	}

	if _, ok := words["message"]; ok {
		add("message")
	}

	if _, ok := words["messages"]; ok {
		add("message")
	}

	if _, ok := words["history"]; ok {
		add("history")
	}

	if _, ok := words["subagent"]; ok {
		add("child", "task", "delegate", "delegated", "delegation")
	}

	if _, ok := words["subagents"]; ok {
		add("child", "task", "delegate", "delegated", "delegation")
	}

	if _, ok := words["user"]; ok {
		add("user")
	}

	return uniqueSortedStrings(terms)
}

func sourceEvidenceLineObjectiveScore(line string, terms []string) int {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return 0
	}

	score := 0

	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}

		if strings.Contains(lower, term) {
			score++
		}
	}

	return score
}


func delegatedReadOnlyQualifiedSymbolCandidate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)

	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")

	if value == "" || !strings.Contains(value, ".") || pathparse.LooksLikeWorkspacePathToken(strings.ToLower(value)) {
		return "", false
	}

	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 2 {
			return "", false
		}

		hasLetter := false

		for _, r := range part {
			switch {
			case unicode.IsLetter(r):
				hasLetter = true
			case unicode.IsDigit(r), r == '_':
			default:
				return "", false
			}
		}

		if !hasLetter || (!delegatedReadOnlyLooksCodeShapedSymbol(part) && !delegatedReadOnlyLooksExportedTypeName(part)) {
			return "", false
		}
	}

	return value, true
}

func implementationTargetWriteReadyRedirectedReadTool(result workspace.ToolResult) (string, bool) {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return "", false
	}

	summary := strings.TrimSpace(result.Summary)
	lower := strings.ToLower(summary)

	if !strings.HasPrefix(lower, "redirected ") {
		return "", false
	}

	tail := summary[len("Redirected "):]
	idx := strings.Index(strings.ToLower(tail), " to ")

	if idx < 0 {
		return "", false
	}

	fields := strings.Fields(tail[idx+len(" to "):])
	if len(fields) == 0 {
		return "", false
	}

	tool := strings.Trim(fields[0], "`'\".,;:")
	if !implementationTargetWriteReadyRedirectedSourceTool(tool) {
		if !strings.HasSuffix(tool, "_file_outline") {
			return "", false
		}

		related, ok := implementationTargetWriteReadyRedirectedRelatedSourceTool(result)
		if !ok {
			return "", false
		}

		return related, true
	}

	return tool, true
}


func searchContentSummaryMatchCount(summary string) (int, bool) {
	match := regexp.MustCompile(`Found (\d+) matching line`).FindStringSubmatch(strings.TrimSpace(summary))
	if len(match) != 2 {
		return 0, false
	}

	count, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}

	return count, true
}

