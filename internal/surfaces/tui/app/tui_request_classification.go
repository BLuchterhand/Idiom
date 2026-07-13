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

func classifyTUIRequest(text string) tuiRequestClass {
	text = strings.TrimSpace(text)
	if text == "" {
		return tuiRequestClassConversation
	}

	if strings.HasPrefix(text, "/") {
		return tuiRequestClassCommand
	}

	words := plannerWordText(text)
	if tuiRequestHasReadOnlyConstraint(words) {
		return tuiRequestClassConversation
	}

	if plannerWordsContainAnyWholePhrase(words, "run tests", "run the tests", "go test", "verify", "check whether") {
		return tuiRequestClassVerification
	}

	if plannerWordsContainAnyWholePhrase(words, "review", "security review", "performance review", "code review") {
		return tuiRequestClassReview
	}

	if plannerWordsContainDirectiveVerb(words, "find", "locate") {
		return tuiRequestClassLocate
	}

	if plannerWordsContainAnyWholePhrase(words,
		"debug", "diagnose", "investigate", "root cause", "why did", "what happened", "explain",
	) {

		return tuiRequestClassInvestigation
	}

	return tuiRequestClassConversation
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

func (c tuiRequestClass) statusText() string {
	return fmt.Sprintf("Request classified: %s.", c)
}

func (m *Model) updateTUIRequestStatus(text string) {
	requestClass := classifyTUIRequest(text)
	if requestClass == tuiRequestClassCommand {
		return
	}

	m.status = requestClass.statusText()
}
