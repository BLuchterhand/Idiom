package agent

import (
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

func objectiveAsksImplementationShape(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "
	return containsAny(lower,
		" what would it take ",
		" what needs to change ",
		" what would need to change ",
		" how would ",
		" how should ",
	)
}
