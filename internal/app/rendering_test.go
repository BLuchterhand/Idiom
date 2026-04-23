package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/workspace"
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

// OpenCode-aligned: requestMessages no longer injects a static workspace
// overview. The model discovers the workspace on demand through tool
// calls (list_files, search_*, glob). We assert the absence so a future
// regression that re-introduces the static dump gets caught.
func TestRequestMessagesOmitsStaticWorkspaceOverview(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
	m := &Model{
		workspace: mgr,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first prompt"},
		},
	}
	msgs := m.requestMessages()
	if containsSystemContent(msgs, "Workspace overview") {
		t.Fatalf("static workspace overview must not be injected; got:\n%#v", msgs)
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
