package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"qwen-bubbles/internal/llama"
)

func TestRenderMessagesWrapsLongContentInsideViewport(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(32, 12),
		messages: []llama.ChatMessage{{
			Role:    "assistant",
			Content: "This sentence is intentionally long enough to wrap instead of disappearing past the right edge.",
		}},
	}

	m.renderMessages(true)

	if got := m.viewport.TotalLineCount(); got <= 2 {
		t.Fatalf("expected long content to wrap across multiple viewport lines, got %d line(s)", got)
	}
	if view := m.viewport.View(); !strings.Contains(view, "edge.") {
		t.Fatalf("expected wrapped viewport to preserve text near the right edge, got:\n%s", view)
	}
}
