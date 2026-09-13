package agent

import (
	"context"
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	conversationevidence "idiom/internal/evidence/conversation"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Conversation continuity handoff", func() {
	brief := func(objective string) workerruntime.WorkerRuntimeBrief {
		return workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         objective,
		}
	}

	evidence := conversationContextEvidenceForMessages([]llama.ChatMessage{
		{Role: "user", Content: "Why did you ask me to switch lanes?"},
	})

	ginkgo.It("records only the bounded model-input scope", func() {
		gomega.Expect(evidence.Scope).To(gomega.Equal("model_input"))
		gomega.Expect(evidence.MessageCount).To(gomega.Equal(1))
		gomega.Expect(evidence.UserMessageCount).To(gomega.Equal(1))
		gomega.Expect(evidence.AssistantMessageCount).To(gomega.Equal(0))
		gomega.Expect(evidence.ValidForUserFacingUncertainty()).To(gomega.BeTrue())
	})

	ginkgo.It("accepts only a scoped user-input handoff for a continuity question", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did you ask me to switch lanes?"), evidence)).To(gomega.BeTrue())
	})

	ginkgo.It("rejects a generic uncertainty answer without the typed handoff shape", func() {
		answer := "I cannot verify that from the conversation context available to me."
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did you ask me to switch lanes?"), evidence)).To(gomega.BeFalse())
	})

	ginkgo.It("rejects a handoff that smuggles in a causal claim", func() {
		answer := "<<QUESTION>>: I cannot verify the earlier exchange, but the reason was probably that implementation was required. Please paste it."
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did you ask me to switch lanes?"), evidence)).To(gomega.BeFalse())
	})

	ginkgo.It("rejects paths, citations, and substantive claims in the handoff", func() {
		for _, answer := range []string{
			"<<QUESTION>>: I cannot verify the earlier exchange. Please paste it; see internal/runtime/agent/runner.go.",
			"<<QUESTION>>: I cannot verify the earlier exchange. Please paste it; https://example.test/previous.",
			"<<QUESTION>>: I cannot verify the earlier exchange. The implementation probably required a lane switch; please paste it.",
		} {
			gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did you ask me to switch lanes?"), evidence)).To(gomega.BeFalse())
		}
	})

	ginkgo.It("does not authorize handoffs for repository or workflow questions", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did the runner build fail?"), evidence)).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("How do the conversation history controls work?"), evidence)).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did the previous implementation fail its tests?"), evidence)).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did you change the runner?"), evidence)).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Did you inspect the runner?"), evidence)).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("What did you say about the role of evidence?"), evidence)).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "read_only_workspace",
			Objective:         "Why did you ask me to switch lanes?",
		}, evidence)).To(gomega.BeFalse())
		withWorkflowObligation := brief("Why did you ask me to switch lanes?")
		withWorkflowObligation.RequiredEditPaths = []string{"target.go"}
		gomega.Expect(conversationContinuityQuestionAnswer(answer, withWorkflowObligation, evidence)).To(gomega.BeFalse())
	})

	ginkgo.It("does not authorize a handoff when the runner has no user context", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		withoutUser := conversationContextEvidenceForMessages([]llama.ChatMessage{{Role: "system", Content: "system"}})
		gomega.Expect(withoutUser.ValidForUserFacingUncertainty()).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did you ask me to switch lanes?"), withoutUser)).To(gomega.BeFalse())
	})

	ginkgo.It("does not authorize a handoff when prior assistant evidence is present", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		withPriorAssistant := conversationContextEvidenceForMessages([]llama.ChatMessage{
			{Role: "assistant", Content: "I asked you to switch lanes."},
			{Role: "user", Content: "Why?"},
		})
		gomega.Expect(withPriorAssistant.ValidForUserFacingUncertainty()).To(gomega.BeFalse())
		gomega.Expect(conversationContinuityQuestionAnswer(answer, brief("Why did you ask me to switch lanes?"), withPriorAssistant)).To(gomega.BeFalse())
	})

	ginkgo.It("rechecks context after an assistant tool turn is recorded", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		runner := &runExecution{
			runner: &Runner{},
			state: runnerRunState{
				runtimeBrief:        brief("Why did you ask me to switch lanes?"),
				conversationContext: evidence,
				turnMessages: []llama.ChatMessage{
					{Role: "user", Content: "Why did you ask me to switch lanes?"},
					{Role: "assistant", Content: "I asked you to switch lanes."},
				},
			},
		}

		acceptance := runner.evaluateFinalAnswerAcceptance(answer, nil)
		gomega.Expect(acceptance.BlockerKind).To(gomega.Equal(string(turnViolationReadOnlyProtocolBlocker)))
	})

	ginkgo.It("marks the handoff as a blocker rather than answerable evidence", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		runner := &runExecution{
			runner: &Runner{},
			state: runnerRunState{
				runtimeBrief:        brief("Why did you ask me to switch lanes?"),
				conversationContext: evidence,
			},
		}

		acceptance := runner.evaluateFinalAnswerAcceptance(answer, nil)
		gomega.Expect(acceptance.Decision).To(gomega.Equal(finalAnswerAcceptanceRejected))
		gomega.Expect(acceptance.BlockerKind).To(gomega.Equal(string(turnViolationUserVisibleBlocker)))
		gomega.Expect(acceptance.EvidencePaths).To(gomega.BeEmpty())
		gomega.Expect(runner.finalAnswerAcceptanceProjectsThroughCompletionGate(acceptance)).To(gomega.BeFalse())

		terminal := runner.classifyTerminalAnswer(answer, "completed", nil)
		gomega.Expect(terminal.Outcome).To(gomega.Equal(terminalAnswerBlocked))
		gomega.Expect(terminal.StopReason).To(gomega.Equal(string(turnViolationUserVisibleBlocker)))
		gomega.Expect(terminal.PresentedAnswer).To(gomega.Equal(answer))
		gomega.Expect(runner.state.runtimeBrief.Readiness).To(gomega.BeNil())
	})

	ginkgo.It("does not override an available source result", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		briefWithEvidence := brief("What did you say earlier about evidence?")
		runner := newRunnerRunFixture(&Runner{RuntimeBrief: briefWithEvidence})
		runner.state.conversationContext = evidence
		results := []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/runtime/agent/evidence_readonly.go",
				1,
				9,
				0,
				"1:package agent\n2:\n3:// Evidence controls answerability.\n4:func Answerable() bool { return true }\n",
			),
		}

		gomega.Expect(conversationContinuityQuestionAnswer(answer, briefWithEvidence, evidence)).To(gomega.BeTrue())
		gomega.Expect(readOnlyClosureDecisionForResults(briefWithEvidence, results).Answerable()).To(gomega.BeTrue())
		acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
		gomega.Expect(acceptance.Decision).To(gomega.Equal(finalAnswerAcceptanceRejected))
		gomega.Expect(acceptance.BlockerKind).To(gomega.Equal(string(turnViolationNonAnswerProse)))
		gomega.Expect(runner.finalAnswerAcceptanceProjectsThroughCompletionGate(acceptance)).To(gomega.BeFalse())
	})

	ginkgo.It("ends the actual runner after one scoped handoff and emits only context metadata", func() {
		answer := "<<QUESTION>>: I cannot verify that from the conversation context available to me. Please paste the earlier exchange."
		client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
		client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()
		client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
		client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			llama.ChatMessage{Role: "assistant", Content: answer}, nil,
		).Times(1)
		var contexts []conversationevidence.Evidence
		runner := &Runner{
			Client:       client,
			RuntimeBrief: brief("Why did you ask me to switch lanes?"),
			OnEvent: func(event RunEvent) {
				if event.Role != "conversation_context_evidence" {
					return
				}
				gomega.Expect(event.Content).NotTo(gomega.ContainSubstring("private user detail"))
				var contextEvidence conversationevidence.Evidence
				gomega.Expect(json.Unmarshal([]byte(event.Content), &contextEvidence)).To(gomega.Succeed())
				contexts = append(contexts, contextEvidence)
			},
		}

		got, err := runner.Run(context.Background(), []llama.ChatMessage{
			{Role: "user", Content: "Why did you ask me to switch lanes? private user detail"},
		}, llama.ChatOptions{}, nil, nil, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(got).To(gomega.Equal(answer))
		gomega.Expect(contexts).To(gomega.HaveLen(1))
		gomega.Expect(contexts[0].ValidForUserFacingUncertainty()).To(gomega.BeTrue())
	})
})
