package app

import (
	"fmt"
	"strings"

	"idiom/internal/llama"
)

type tuiRequestClass string

const (
	tuiRequestClassCommand        tuiRequestClass = "command"
	tuiRequestClassConversation   tuiRequestClass = "conversation"
	tuiRequestClassInvestigation  tuiRequestClass = "investigation"
	tuiRequestClassVerification   tuiRequestClass = "verification"
	tuiRequestClassReview         tuiRequestClass = "review"
	tuiRequestClassLocate         tuiRequestClass = "locate"
	tuiRequestClassImplementation tuiRequestClass = "implementation"
	tuiRequestClassPlanExecute    tuiRequestClass = "plan-execute"
)

type tuiRequestClassification struct {
	Class                tuiRequestClass
	Reason               string
	Command              string
	RequiresConfirmation bool
}

func classifyTUIRequest(text string) tuiRequestClassification {
	text = strings.TrimSpace(text)
	if text == "" {
		return tuiRequestClassification{Class: tuiRequestClassConversation, Reason: "empty"}
	}
	if strings.HasPrefix(text, "/") {
		return tuiRequestClassification{Class: tuiRequestClassCommand, Reason: "typed_command"}
	}

	words := plannerWordText(text)
	if tuiRequestHasReadOnlyConstraint(words) {
		return tuiRequestClassification{Class: tuiRequestClassConversation, Reason: "read_only_constraint"}
	}
	if tuiRequestPrefersPlanExecute(words) && plannerWordsContainAnyWholePhrase(words,
		"implement", "fix", "repair", "change", "update", "edit", "write",
	) {
		return tuiRequestClassification{
			Class:                tuiRequestClassPlanExecute,
			Reason:               "explicit_plan_execute_mutation",
			Command:              "/plan-execute",
			RequiresConfirmation: true,
		}
	}
	if plannerPromptHasExplicitMutationIntent(words) {
		if tuiRequestPrefersPlanExecute(words) {
			return tuiRequestClassification{
				Class:                tuiRequestClassPlanExecute,
				Reason:               "explicit_plan_execute_mutation",
				Command:              "/plan-execute",
				RequiresConfirmation: true,
			}
		}
		return tuiRequestClassification{
			Class:                tuiRequestClassImplementation,
			Reason:               "explicit_mutation_intent",
			Command:              "/implement",
			RequiresConfirmation: true,
		}
	}
	if plannerWordsContainAnyWholePhrase(words, "run tests", "run the tests", "go test", "verify", "check whether") {
		return tuiRequestClassification{Class: tuiRequestClassVerification, Reason: "verification_request"}
	}
	if plannerWordsContainAnyWholePhrase(words, "review", "security review", "performance review", "code review") {
		return tuiRequestClassification{Class: tuiRequestClassReview, Reason: "review_request"}
	}
	if plannerWordsContainDirectiveVerb(words, "find", "locate") {
		return tuiRequestClassification{Class: tuiRequestClassLocate, Reason: "locate_request"}
	}
	if plannerWordsContainAnyWholePhrase(words,
		"debug", "diagnose", "investigate", "root cause", "why did", "what happened", "explain",
	) {
		return tuiRequestClassification{Class: tuiRequestClassInvestigation, Reason: "investigation_request"}
	}
	return tuiRequestClassification{Class: tuiRequestClassConversation, Reason: "default_conversation"}
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

func tuiRequestPrefersPlanExecute(words string) bool {
	return plannerWordsContainAnyWholePhrase(words,
		"plan execute",
		"plan then execute",
		"durable implementation atom",
		"implementation atom",
		"through plan execute",
		"through the plan execute",
	)
}

func (c tuiRequestClassification) statusText() string {
	label := string(c.Class)
	switch c.Class {
	case tuiRequestClassPlanExecute:
		label = "plan-execute"
	case tuiRequestClassImplementation:
		label = "implementation"
	}
	if c.RequiresConfirmation && c.Command != "" {
		return fmt.Sprintf("Request classified: %s. Confirm with %s <objective>.", label, c.Command)
	}
	return fmt.Sprintf("Request classified: %s.", label)
}

func (c tuiRequestClassification) confirmationQuestion() string {
	command := strings.TrimSpace(c.Command)
	if command == "" {
		command = "/implement"
	}
	lane := strings.TrimPrefix(command, "/")
	return fmt.Sprintf("Request classified: %s. Use `%s <objective>` to run the write-capable %s lane, or rephrase if you want a read-only answer.", c.Class, command, lane)
}

func (m *Model) handleTUIRequestPreflight(text string) bool {
	classification := classifyTUIRequest(text)
	if classification.Class == tuiRequestClassCommand {
		return false
	}
	m.status = classification.statusText()
	if !classification.RequiresConfirmation {
		return false
	}

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: strings.TrimSpace(text)})
	m.logMessage("user", text)
	m.logMessage("route_plan", "tui_preflight class="+string(classification.Class)+" reason="+classification.Reason)
	m.logMessage("route_confirmation", classification.statusText())

	reply := classification.confirmationQuestion()
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply})
	m.logMessage("assistant", reply)
	m.appendHistoryMessage("agent", reply, nil)
	return true
}
