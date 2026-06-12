package app

import (
	"fmt"
	"strings"
)

type tuiRequestClass string

const (
	tuiRequestClassCommand       tuiRequestClass = "command"
	tuiRequestClassConversation  tuiRequestClass = "conversation"
	tuiRequestClassInvestigation tuiRequestClass = "investigation"
	tuiRequestClassVerification  tuiRequestClass = "verification"
	tuiRequestClassReview        tuiRequestClass = "review"
	tuiRequestClassLocate        tuiRequestClass = "locate"
)

type tuiRequestClassification struct {
	Class tuiRequestClass
}

func classifyTUIRequest(text string) tuiRequestClassification {
	text = strings.TrimSpace(text)
	if text == "" {
		return tuiRequestClassification{Class: tuiRequestClassConversation}
	}

	if strings.HasPrefix(text, "/") {
		return tuiRequestClassification{Class: tuiRequestClassCommand}
	}

	words := plannerWordText(text)
	if tuiRequestHasReadOnlyConstraint(words) {
		return tuiRequestClassification{Class: tuiRequestClassConversation}
	}

	if plannerWordsContainAnyWholePhrase(words, "run tests", "run the tests", "go test", "verify", "check whether") {
		return tuiRequestClassification{Class: tuiRequestClassVerification}
	}

	if plannerWordsContainAnyWholePhrase(words, "review", "security review", "performance review", "code review") {
		return tuiRequestClassification{Class: tuiRequestClassReview}
	}

	if plannerWordsContainDirectiveVerb(words, "find", "locate") {
		return tuiRequestClassification{Class: tuiRequestClassLocate}
	}

	if plannerWordsContainAnyWholePhrase(words,
		"debug", "diagnose", "investigate", "root cause", "why did", "what happened", "explain",
	) {

		return tuiRequestClassification{Class: tuiRequestClassInvestigation}
	}

	return tuiRequestClassification{Class: tuiRequestClassConversation}
}

func tuiRequestHasReadOnlyConstraint(words string) bool {
	return plannerWordsContainAnyWholePhrase(words,
		"do not edit",
		"dont edit",
		"don t edit",
		"do not change",
		"dont change",
		"don t change",
		"without changing",
		"read only",
		"read-only",
		"just explain",
		"explain only",
		"diagnose only",
		"investigate only",
		"no changes",
		"no edits",
	)
}

func (c tuiRequestClassification) statusText() string {
	return fmt.Sprintf("Request classified: %s.", c.Class)
}

func (m *Model) updateTUIRequestStatus(text string) {
	classification := classifyTUIRequest(text)
	if classification.Class == tuiRequestClassCommand {
		return
	}

	m.status = classification.statusText()
}
