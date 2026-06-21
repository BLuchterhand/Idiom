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

func TestEmitThinkingTraceSplitsReasoningSegments(t *testing.T) {
	var got []ThoughtEvent

	runner := &Runner{
		OnThought: func(event ThoughtEvent) {
			got = append(got, event)
		},
	}

	runner.emitThinkingTrace(4, "model_reply", llama.ChatMessage{
		Content:          "visible answer",
		ReasoningContent: "first thought\n\nsecond thought\n\nthird thought",
	}, llama.ChatOptions{}, false)

	want := []string{"first thought", "second thought", "third thought"}
	if len(got) != len(want) {
		t.Fatalf("OnThought calls = %d, want %d: %#v", len(got), len(want), got)
	}

	for i, event := range got {
		if event.Step != 4 || event.Text != want[i] {
			t.Fatalf("thought[%d] = %#v, want step 4 text %q", i, event, want[i])
		}
	}
}

func TestEmitThinkingTraceExtractsThinkTagContent(t *testing.T) {
	var got []ThoughtEvent

	runner := &Runner{
		OnThought: func(event ThoughtEvent) {
			got = append(got, event)
		},
	}

	runner.emitThinkingTrace(5, "model_reply", llama.ChatMessage{
		Content: "prefix <think>first inline thought\nsecond inline thought</think> visible answer <think>third inline thought</think>",
	}, llama.ChatOptions{}, false)

	want := []string{"first inline thought", "second inline thought", "third inline thought"}
	if len(got) != len(want) {
		t.Fatalf("OnThought calls = %d, want %d: %#v", len(got), len(want), got)
	}

	for i, event := range got {
		if event.Step != 5 || event.Text != want[i] {
			t.Fatalf("thought[%d] = %#v, want step 5 text %q", i, event, want[i])
		}
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
