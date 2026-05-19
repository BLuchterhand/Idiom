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

func TestRequestMessagesDoesNotCompactOperatorMessages(t *testing.T) {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original implementation goal"},
			{Role: "operator", Content: "recover status: ready\npacket: .idiom/recovery/secret/packet.json"},
		},
	}
	for i := 0; i < defaultCompactionThreshold+1; i++ {
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: "assistant turn"})
	}

	msgs := m.requestMessages()
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "recover status:") ||
			strings.Contains(msg.Content, ".idiom/recovery/secret/packet.json") {
			t.Fatalf("operator status content must not be compacted or replayed: %#v", msgs)
		}
	}
	if len(m.messages) < 2 || m.messages[1].Role != "operator" {
		t.Fatalf("request payload filtering must not mutate transcript messages: %#v", m.messages)
	}
}

func TestRenderedMessageBodyCacheInvalidatesWhenMessagesChange(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{{Role: "assistant", Content: "first body"}},
	}

	first := m.renderedMessageBody()
	if !strings.Contains(first, "first body") {
		t.Fatalf("expected initial render to include first body, got:\n%s", first)
	}

	m.messageRenderCache = "cached sentinel"
	m.messageRenderWidth = m.viewport.Width
	m.messageRenderHash = messageRenderFingerprint(m.messages)
	m.messageRenderMD = false
	if got := m.renderedMessageBody(); got != "cached sentinel" {
		t.Fatalf("expected unchanged transcript to reuse cache, got:\n%s", got)
	}

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "second body"})
	updated := m.renderedMessageBody()
	if strings.Contains(updated, "cached sentinel") {
		t.Fatalf("expected cache to be invalidated after message change, got:\n%s", updated)
	}
	if !strings.Contains(updated, "second body") {
		t.Fatalf("expected updated render to include new message, got:\n%s", updated)
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
