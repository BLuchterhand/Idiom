package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/runtime/llama"
)

var _ = ginkgo.It("EmitThinkingTraceNotifiesThoughtObserverWithoutTelemetry", func() {
	var got ThoughtEvent
	calls := 0

	runner := newRunnerRunFixture(&Runner{
		OnThought: func(event ThoughtEvent) {
			calls++
			got = event
		},
	})

	runner.emitThinkingTrace(3, "model_reply", llama.ChatMessage{
		Content:          "visible answer",
		ReasoningContent: "inspect the render path",
	}, llama.ChatOptions{}, false)

	gomega.Expect(calls).To(gomega.Equal(1))
	gomega.Expect(got).To(gomega.Equal(ThoughtEvent{
		Step: 3,
		Text: "inspect the render path",
	}))
})

var _ = ginkgo.It("EmitThinkingTraceSplitsReasoningSegments", func() {
	var got []ThoughtEvent

	runner := newRunnerRunFixture(&Runner{
		OnThought: func(event ThoughtEvent) {
			got = append(got, event)
		},
	})

	runner.emitThinkingTrace(4, "model_reply", llama.ChatMessage{
		Content:          "visible answer",
		ReasoningContent: "first thought\n\nsecond thought\n\nthird thought",
	}, llama.ChatOptions{}, false)

	gomega.Expect(got).To(gomega.Equal([]ThoughtEvent{
		{Step: 4, Text: "first thought"},
		{Step: 4, Text: "second thought"},
		{Step: 4, Text: "third thought"},
	}))
})

var _ = ginkgo.It("EmitThinkingTraceExtractsThinkTagContent", func() {
	var got []ThoughtEvent

	runner := newRunnerRunFixture(&Runner{
		OnThought: func(event ThoughtEvent) {
			got = append(got, event)
		},
	})

	runner.emitThinkingTrace(5, "model_reply", llama.ChatMessage{
		Content: "prefix <think>first inline thought\nsecond inline thought</think> visible answer <think>third inline thought</think>",
	}, llama.ChatOptions{}, false)

	gomega.Expect(got).To(gomega.Equal([]ThoughtEvent{
		{Step: 5, Text: "first inline thought"},
		{Step: 5, Text: "second inline thought"},
		{Step: 5, Text: "third inline thought"},
	}))
})

var _ = ginkgo.It("EmitThinkingTraceSkipsEmptyThoughtObserverPayload", func() {
	calls := 0

	runner := newRunnerRunFixture(&Runner{
		OnThought: func(ThoughtEvent) {
			calls++
		},
	})

	runner.emitThinkingTrace(1, "model_reply", llama.ChatMessage{
		Content: "visible answer",
	}, llama.ChatOptions{}, false)

	gomega.Expect(calls).To(gomega.BeZero())
})

var _ = ginkgo.It("EmitThinkingTraceSkipsPreservedHistoryThoughtObserverPayload", func() {
	calls := 0

	runner := newRunnerRunFixture(&Runner{
		OnThought: func(ThoughtEvent) {
			calls++
		},
	})

	runner.emitThinkingTrace(1, "assistant_turn_preserved", llama.ChatMessage{
		ReasoningContent: "already emitted reasoning",
	}, llama.ChatOptions{}, true)

	gomega.Expect(calls).To(gomega.BeZero(), "preserved history should not re-emit UI thoughts")
})
