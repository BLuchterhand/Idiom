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

func TestProtectControlMarkersForMarkdownPreservesQuestionMarker(t *testing.T) {
	got := protectControlMarkersForMarkdown("<<QUESTION>>: missing exact anchor")
	if !strings.Contains(got, "`<<QUESTION>>`:") {
		t.Fatalf("question marker should be protected from markdown HTML parsing, got %q", got)
	}
}

func TestViewOmitsCompactContextFooterBelowInput(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	m := New(cfg)
	defer m.Close()
	m.ready = true
	m.width = 80
	m.contextError = ""
	m.viewport = viewport.New(80, 3)
	m.input.SetHeight(1)
	m.input.SetValue("hello")

	view := m.View()

	for _, removed := range []string{"mode:", "trace:", "ctx:"} {
		if strings.Contains(view, removed) {
			t.Fatalf("view should not render compact context footer containing %q:\n%s", removed, view)
		}
	}
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "hello") {
		t.Fatalf("input should be the last normal-state row, got:\n%s", view)
	}
}

func TestResizeUsesFullTerminalHeightWithoutFooter(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	m := New(cfg)
	defer m.Close()
	m.width = 80
	m.height = 14

	m.resize()
	view := m.View()

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if got := len(lines); got != m.height {
		t.Fatalf("view height = %d, want terminal height %d:\n%s", got, m.height, view)
	}
	if !strings.Contains(lines[len(lines)-1], "Type a prompt") {
		t.Fatalf("input should be flush with bottom, got:\n%s", view)
	}
}

func TestInputHeightGrowsForWrappedPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	m := New(cfg)
	defer m.Close()
	m.width = 32
	m.height = 18
	m.input.SetValue(strings.Repeat("wrapped ", 12))

	m.resize()

	if got := m.input.Height(); got <= 1 {
		t.Fatalf("input height = %d, want growth for wrapped text", got)
	}
	lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
	if got := len(lines); got != m.height {
		t.Fatalf("view height = %d, want terminal height %d", got, m.height)
	}
}

func TestInputHeightUsesTextareaWrappedLineCount(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	m := New(cfg)
	defer m.Close()
	m.width = 28
	m.height = 18
	m.input.SetValue("first visible words should remain above the final wrapped line")

	m.resize()

	wrappedRows := 0
	for _, line := range strings.Split(m.input.Value(), "\n") {
		wrappedRows += max(1, len(wrapLines(line, m.input.Width()-len(m.input.Prompt))))
	}
	if got, want := m.input.Height(), min(wrappedRows, m.height/3); got != want {
		t.Fatalf("input height = %d, want prompt-aware wrapped row cap %d", got, want)
	}
	view := m.View()
	if !strings.Contains(view, "first visible") {
		t.Fatalf("input view should retain the top wrapped line, got:\n%s", view)
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

func TestRequestMessagesDropsAssistantReasoningFromCrossTurnHistory(t *testing.T) {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first prompt"},
			{Role: "assistant", Content: "first answer", ReasoningContent: "hidden plan from prior turn"},
			{Role: "user", Content: "second prompt"},
		},
	}

	msgs := m.requestMessages()
	for _, msg := range msgs {
		if msg.ReasoningContent != "" {
			t.Fatalf("request payload must not replay cross-turn hidden reasoning: %#v", msgs)
		}
	}
	if m.messages[1].ReasoningContent == "" {
		t.Fatalf("request payload filtering must not mutate transcript history")
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
