package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
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

// Fix 4 (workspace overview) — integration check that requestMessages
// injects the overview exactly once per session. See
// docs/opencode-fixes-design.md §4.
func TestRequestMessagesInjectsWorkspaceOverviewExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
	m := &Model{
		workspace: mgr,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first prompt"},
		},
	}
	first := m.requestMessages()
	if !containsSystemContent(first, "Workspace overview") {
		t.Fatalf("first call should include the workspace overview, got:\n%#v", first)
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: "ok"}, llama.ChatMessage{Role: "user", Content: "second"})
	second := m.requestMessages()
	if containsSystemContent(second, "Workspace overview") {
		t.Fatalf("second call must not re-inject the overview, got:\n%#v", second)
	}
}

func containsSystemContent(messages []llama.ChatMessage, needle string) bool {
	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}
