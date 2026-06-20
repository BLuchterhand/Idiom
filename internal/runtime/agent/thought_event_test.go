package agent

import (
	"testing"

	"idiom/internal/runtime/llama"
)

func TestEmitThinkingTraceNotifiesThoughtObserverWithoutTelemetry(t *testing.T) {
	var got ThoughtEvent
	calls := 0

	runner := &Runner{
		OnThought: func(event ThoughtEvent) {
			calls++
			got = event
		},
	}

	runner.emitThinkingTrace(3, "model_reply", llama.ChatMessage{
		Content:          "visible answer",
		ReasoningContent: "inspect the render path",
	}, llama.ChatOptions{}, false)

	if calls != 1 {
		t.Fatalf("OnThought calls = %d, want 1", calls)
	}

	if got.Step != 3 || got.Text != "inspect the render path" {
		t.Fatalf("thought event = %#v", got)
	}
}

func TestEmitThinkingTraceSkipsEmptyThoughtObserverPayload(t *testing.T) {
	calls := 0

	runner := &Runner{
		OnThought: func(ThoughtEvent) {
			calls++
		},
	}

	runner.emitThinkingTrace(1, "model_reply", llama.ChatMessage{
		Content: "visible answer",
	}, llama.ChatOptions{}, false)

	if calls != 0 {
		t.Fatalf("OnThought calls = %d, want 0", calls)
	}
}

func TestEmitThinkingTraceSkipsPreservedHistoryThoughtObserverPayload(t *testing.T) {
	calls := 0

	runner := &Runner{
		OnThought: func(ThoughtEvent) {
			calls++
		},
	}

	runner.emitThinkingTrace(1, "assistant_turn_preserved", llama.ChatMessage{
		ReasoningContent: "already emitted reasoning",
	}, llama.ChatOptions{}, true)

	if calls != 0 {
		t.Fatalf("preserved history should not re-emit UI thoughts, got %d call(s)", calls)
	}
}
