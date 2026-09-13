package agent

import (
	"strings"

	conversationevidence "idiom/internal/evidence/conversation"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func conversationContextEvidenceForMessages(messages []llama.ChatMessage) conversationevidence.Evidence {
	userMessages := 0
	assistantMessages := 0
	for _, message := range messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}
	return conversationevidence.NewModelInputEvidence(userMessages, assistantMessages)
}

func (r *runExecution) emitConversationContextEvidence() {
	if r == nil || r.runner == nil || r.runner.OnEvent == nil {
		return
	}
	if !r.state.conversationContext.ValidForUserFacingUncertainty() {
		return
	}
	if !readOnlyDirectAnswerLane(r.state.runtimeBrief) {
		return
	}
	r.emitEvent(marshalRunEvent("conversation_context_evidence", r.state.conversationContext))
}

func continuityObjective(brief workerruntime.WorkerRuntimeBrief) bool {
	if !readOnlyDirectAnswerLane(brief) {
		return false
	}
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) ||
		len(brief.RequiredEditPaths) > 0 || len(brief.RequiredEditSnippets) > 0 || len(brief.AllowedEditPaths) > 0 {
		return false
	}

	objective := strings.ToLower(strings.Join(strings.Fields(brief.Objective), " "))
	if objective == "" {
		return false
	}
	if containsAnyContinuityTerm(objective, []string{
		"change the runner", "changed the runner", "change the implementation", "changed the implementation",
		"implementation failed", "implementation fail", "about the implementation", "inspect the", "inspect this", "repository", "repo",
		"source file", "test failure", "tests failed", "build failed",
	}) {
		return false
	}

	priorContextTerms := []string{
		"earlier exchange", "earlier turn", "previous answer", "previous response", "previous turn", "prior response", "last turn", "last message", "what happened earlier",
	}
	selfReferenceTerms := []string{
		"why did you ask me", "why did you tell me", "what did you ask me", "what did you tell me",
		"what did you say earlier", "what did you say previously", "do you remember",
	}

	return containsAnyContinuityTerm(objective, priorContextTerms) ||
		containsAnyContinuityTerm(objective, selfReferenceTerms)
}

func containsAnyContinuityTerm(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

// conversationContinuityQuestionAnswer is deliberately narrower than a
// generic uncertainty answer. It accepts only a user-input handoff that
// explicitly limits its claim to the model-input context and asks the user
// for the missing exchange. It must never be used to explain a repository,
// workflow, implementation, or external-research claim.
func conversationContinuityQuestionAnswer(answer string, brief workerruntime.WorkerRuntimeBrief, evidence conversationevidence.Evidence) bool {
	if !continuityObjective(brief) || !evidence.ValidForUserFacingUncertainty() {
		return false
	}

	trimmed := strings.TrimSpace(answer)
	if !strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		return false
	}

	body := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "<<QUESTION>>:")))
	if body == "" || !containsAnyContinuityTerm(body, []string{"context", "conversation", "earlier", "previous", "prior", "exchange"}) {
		return false
	}
	if !containsAnyContinuityTerm(body, []string{"can't verify", "cannot verify", "do not have", "don't have", "not available", "not in the available", "outside the available"}) {
		return false
	}
	if !containsAnyContinuityTerm(body, []string{"paste", "provide", "share", "repeat", "restate"}) {
		return false
	}
	if containsAnyContinuityTerm(body, []string{
		"http://", "https://", "www.", "`", "/", "\\", "[", "]", ".go", "line ",
		"i think", "probably", "likely", "the reason", "the cause", "the answer", "the issue",
		"implementation", "workflow", "source says", "evidence says",
	}) {
		return false
	}

	// A handoff cannot carry a causal or affirmative reconstruction alongside
	// the uncertainty claim. Those claims require a real conversation event.
	return !containsAnyContinuityTerm(body, []string{"because", "the reason was", "i asked", "i told", "i said"})
}
