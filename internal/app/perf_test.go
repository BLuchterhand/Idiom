package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/llama"
)

func TestRenderedMessageBody_PerMessageCache_AppendOnly(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "first body"},
		},
	}

	first := m.renderedMessageBody()
	if !strings.Contains(first, "first body") {
		t.Fatalf("initial render missing first body:\n%s", first)
	}
	if len(m.perMsgRenderCache) != 1 {
		t.Fatalf("expected 1 cached entry after first render, got %d", len(m.perMsgRenderCache))
	}
	if m.perMsgRenderLen != 1 {
		t.Fatalf("expected perMsgRenderLen=1, got %d", m.perMsgRenderLen)
	}

	cachedFirst := m.perMsgRenderCache[0]

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "second body"})
	updated := m.renderedMessageBody()
	if !strings.Contains(updated, "first body") {
		t.Fatalf("updated render missing first body:\n%s", updated)
	}
	if !strings.Contains(updated, "second body") {
		t.Fatalf("updated render missing second body:\n%s", updated)
	}

	if len(m.perMsgRenderCache) != 2 {
		t.Fatalf("expected 2 cached entries after append, got %d", len(m.perMsgRenderCache))
	}
	if m.perMsgRenderCache[0] != cachedFirst {
		t.Fatalf("first message cache entry changed after append — should have been reused")
	}
}

func TestRenderedMessageBody_PerMessageCache_ClearsOnInsert(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "user msg"},
			{Role: "assistant", Content: ""},
		},
	}

	m.renderedMessageBody()
	if len(m.perMsgRenderCache) != 2 {
		t.Fatalf("expected 2 cached entries, got %d", len(m.perMsgRenderCache))
	}

	m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: "tool_output | result"})
	if len(m.messages) != 3 {
		t.Fatalf("expected 3 messages after insert, got %d", len(m.messages))
	}

	result := m.renderedMessageBody()
	if !strings.Contains(result, "tool_output") {
		t.Fatalf("render after insert missing tool name:\n%s", result)
	}
	if !strings.Contains(result, "user msg") {
		t.Fatalf("render after insert missing user msg:\n%s", result)
	}
	if len(m.perMsgRenderCache) != 3 {
		t.Fatalf("expected 3 cached entries after insert rebuild, got %d", len(m.perMsgRenderCache))
	}
}

func TestRenderedMessageBody_PerMessageCache_ClearsOnWidthChange(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "some content"},
		},
	}

	m.renderedMessageBody()
	if len(m.perMsgRenderCache) != 1 {
		t.Fatalf("expected 1 cached entry, got %d", len(m.perMsgRenderCache))
	}
	cached := m.perMsgRenderCache[0]

	m.viewport.Width = 60
	m.renderedMessageBody()

	if len(m.perMsgRenderCache) != 1 {
		t.Fatalf("expected 1 cached entry after resize, got %d", len(m.perMsgRenderCache))
	}
	if m.perMsgRenderCache[0] == cached {
		t.Fatalf("cache entry should have been re-rendered after width change")
	}
}

func TestRenderedMessageBody_PerMessageCache_MultipleAppends(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{},
	}

	for i := 0; i < 5; i++ {
		m.messages = append(m.messages, llama.ChatMessage{
			Role:    "assistant",
			Content: "msg " + string(rune('A'+i)),
		})
		m.renderedMessageBody()
	}

	if len(m.perMsgRenderCache) != 5 {
		t.Fatalf("expected 5 cached entries after 5 appends, got %d", len(m.perMsgRenderCache))
	}
	if m.perMsgRenderLen != 5 {
		t.Fatalf("expected perMsgRenderLen=5, got %d", m.perMsgRenderLen)
	}

	for i := 0; i < 5; i++ {
		expected := "msg " + string(rune('A'+i))
		if !strings.Contains(m.perMsgRenderCache[i], expected) {
			t.Errorf("cache entry %d missing %q", i, expected)
		}
	}
}

func TestRenderedMessageBody_PerMessageCache_ClearsOnContentMutation(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original"},
			{Role: "assistant", Content: "reply"},
		},
	}

	m.renderedMessageBody()
	if m.perMsgRenderLen != 2 {
		t.Fatalf("expected perMsgRenderLen=2, got %d", m.perMsgRenderLen)
	}

	m.messages[1].Content = "updated reply"
	result := m.renderedMessageBody()
	if !strings.Contains(result, "updated reply") {
		t.Fatalf("expected updated content in render:\n%s", result)
	}
}

func TestInvalidateMessageRenderCache_ClearsPerMessageCache(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "body"},
		},
	}

	m.renderedMessageBody()
	if len(m.perMsgRenderCache) != 1 {
		t.Fatalf("expected 1 cached entry, got %d", len(m.perMsgRenderCache))
	}
	if m.perMsgRenderLen != 1 {
		t.Fatalf("expected perMsgRenderLen=1, got %d", m.perMsgRenderLen)
	}

	m.invalidateMessageRenderCache()
	if m.perMsgRenderCache != nil {
		t.Fatalf("expected perMsgRenderCache to be nil after invalidation")
	}
	if m.perMsgRenderLen != 0 {
		t.Fatalf("expected perMsgRenderLen=0 after invalidation, got %d", m.perMsgRenderLen)
	}
}

func TestHandleToolTrace_CompactMode_SkipsMainRender(t *testing.T) {
	m := &Model{
		ready:     true,
		viewport:  viewport.New(48, 12),
		events:    make(chan tea.Msg, 256),
		messages:  []llama.ChatMessage{{Role: "user", Content: "hi"}},
		traceMode: traceDisplayCompact,
		currentTurnHistoryActive: true,
		currentTurnHistoryIndex:  1,
		history: historyState{
			entries: []historyEntry{
				{sender: "user", summary: "hi"},
				{sender: "agent", inProgress: true, summary: "working…"},
			},
			selected:  1,
			panelOpen: true,
			viewport:  viewport.New(20, 10),
		},
	}

	m.renderMessages(true)
	contentBefore := m.viewport.View()

	m.handleToolTrace(toolTraceMsg{
		step:    1,
		text:    "read_file | path.go",
		running: true,
	})

	contentAfter := m.viewport.View()
	if contentBefore != contentAfter {
		t.Fatalf("compact trace mode should not change main viewport content")
	}

	entry := &m.history.entries[1]
	found := false
	for _, tc := range entry.toolCalls {
		if strings.Contains(tc.line, "read_file") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected tool trace to be recorded in history entry in compact mode, calls: %+v", entry.toolCalls)
	}
}

func TestHandleToolTrace_DetailMode_Renders(t *testing.T) {
	m := &Model{
		ready:     true,
		viewport:  viewport.New(48, 12),
		events:    make(chan tea.Msg, 256),
		messages:  []llama.ChatMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: ""}},
		traceMode: traceDisplayDetail,
	}

	m.renderMessages(true)

	m.handleToolTrace(toolTraceMsg{
		step:    1,
		text:    "read_file | some output",
		running: false,
	})

	view := m.viewport.View()
	if !strings.Contains(view, "read_file") {
		t.Fatalf("detail trace mode should render tool in main viewport:\n%s", view)
	}
}

func TestRenderPlanLine_UsesPrecompiledStyles(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		contains string
	}{
		{"unchecked checkbox", "- [ ] do something", "[ ]"},
		{"checked checkbox", "- [x] done", "[x]"},
		{"in progress", "- [~] working", "[~]"},
		{"heading 1", "# Title", "Title"},
		{"heading 2", "## Section", "Section"},
		{"plain text", "just a line", "just a line"},
		{"strips comment anchor", "- [ ] task <!-- id:abc -->", "task"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := renderPlanLine(tc.input, 40)
			if !strings.Contains(result, tc.contains) {
				t.Errorf("renderPlanLine(%q) missing %q in:\n%s", tc.input, tc.contains, result)
			}
			if strings.Contains(result, "<!--") {
				t.Errorf("renderPlanLine should strip HTML comments, got:\n%s", result)
			}
		})
	}
}

func TestParsePlanItems(t *testing.T) {
	content := "# Plan\n- [ ] step one\n- [x] step two\n"
	items := parsePlanItems(content)
	if len(items) != 2 {
		t.Fatalf("expected 2 plan items, got %d", len(items))
	}
	if items[0].Checked || !items[1].Checked {
		t.Errorf("first should be unchecked, second checked")
	}
}

func TestCurrentPlanItems_CacheHitOnFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/plan.md"
	content := "# Plan\n- [ ] step one\n"
	os.WriteFile(path, []byte(content), 0o644)

	m := &Model{planPath: path}
	items1 := m.currentPlanItems()
	if len(items1) != 1 {
		t.Fatalf("first call: expected 1 item, got %d", len(items1))
	}
	if m.plan.cachedItemsContent != content {
		t.Errorf("cache content not set")
	}

	items2 := m.currentPlanItems()
	if len(items2) != 1 {
		t.Fatalf("cached call: expected 1 item, got %d", len(items2))
	}
	if items1[0].RawText != items2[0].RawText {
		t.Errorf("cached items should match")
	}
}

func TestCurrentPlanItems_ReparsesOnFileChange(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/plan.md"
	os.WriteFile(path, []byte("# Old\n- [ ] old\n"), 0o644)

	m := &Model{planPath: path}
	items1 := m.currentPlanItems()
	if len(items1) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items1))
	}

	newContent := "# New\n- [x] done\n"
	os.WriteFile(path, []byte(newContent), 0o644)
	modTime := time.Now().Add(2 * time.Second)
	os.Chtimes(path, modTime, modTime)

	items2 := m.currentPlanItems()
	if len(items2) != 1 {
		t.Fatalf("expected 1 item after change, got %d", len(items2))
	}
	if items2[0].RawText != "- [x] done" {
		t.Errorf("expected re-parsed item, got %q", items2[0].RawText)
	}
	if !items2[0].Checked {
		t.Errorf("expected checked item after re-parse")
	}
}

func TestRenderHistoryPanelContent_PrecompiledStyles(t *testing.T) {
	m := &Model{
		history: historyState{
			panelOpen: true,
			focused:  true,
			selected: 0,
			expanded: true,
			entries: []historyEntry{
				{
					sender:   "agent",
					contents: []string{"agent response text"},
					toolCalls: []historyToolCall{
						{line: "read_file | result", running: false},
					},
					summary: "test summary",
				},
			},
			viewport: viewport.New(40, 20),
		},
	}

	m.renderHistoryPanelContent()
	view := m.history.viewport.View()
	if !strings.Contains(view, "test summary") {
		t.Fatalf("history panel should contain summary:\n%s", view)
	}
	if !strings.Contains(view, "agent response text") {
		t.Fatalf("history panel should contain expanded content:\n%s", view)
	}
	if !strings.Contains(view, "read_file") {
		t.Fatalf("history panel should contain tool call:\n%s", view)
	}
}

func TestRenderMessages_SpinnerStillUpdatesDuringStreaming(t *testing.T) {
	m := &Model{
		ready:     true,
		viewport:  viewport.New(48, 12),
		events:    make(chan tea.Msg, 256),
		streaming: true,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}

	m.renderMessages(false)
	view1 := m.viewport.View()
	if !strings.Contains(view1, "Qwen is working") {
		t.Fatalf("streaming view should show working indicator:\n%s", view1)
	}

	m.workingFrame = 3
	m.renderMessages(false)
	view2 := m.viewport.View()
	if !strings.Contains(view2, "Qwen is working") {
		t.Fatalf("streaming view should still show working indicator:\n%s", view2)
	}
	if view1 == view2 {
		t.Fatalf("spinner animation should update between frames")
	}
}

func TestRenderMessages_CacheHitOnSpinnerTick(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}

	first := m.renderedMessageBody()
	hash1 := m.messageRenderHash

	second := m.renderedMessageBody()
	hash2 := m.messageRenderHash

	if first != second {
		t.Fatalf("same messages should produce identical cached output")
	}
	if hash1 != hash2 {
		t.Fatalf("hash should be stable for identical input")
	}
	if m.messageRenderCache != first {
		t.Fatalf("cache should be reused on second call")
	}
}

func TestMessageRenderFingerprint_DetectsChanges(t *testing.T) {
	msgs1 := []llama.ChatMessage{{Role: "user", Content: "hello"}}
	msgs2 := []llama.ChatMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "world"}}
	msgs3 := []llama.ChatMessage{{Role: "user", Content: "different"}}

	h1 := messageRenderFingerprint(msgs1)
	h2 := messageRenderFingerprint(msgs2)
	h3 := messageRenderFingerprint(msgs3)

	if h1 == h2 {
		t.Fatalf("fingerprints should differ when message count changes")
	}
	if h1 == h3 {
		t.Fatalf("fingerprints should differ when content changes")
	}

	h1again := messageRenderFingerprint(msgs1)
	if h1 != h1again {
		t.Fatalf("fingerprint should be deterministic for same input")
	}
}

func TestRenderMessages_EmptyMessagesShowsPlaceholder(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{},
	}

	m.renderMessages(false)
	view := m.viewport.View()
	if !strings.Contains(view, "No messages yet") {
		t.Fatalf("empty state should show placeholder:\n%s", view)
	}
}

func TestRenderMessages_ClearMessagesShowsPlaceholder(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{{Role: "user", Content: "hi"}},
	}

	m.renderMessages(true)
	if m.perMsgRenderLen != 1 {
		t.Fatalf("expected perMsgRenderLen=1, got %d", m.perMsgRenderLen)
	}

	m.messages = nil
	m.renderMessages(true)
	view := m.viewport.View()
	if !strings.Contains(view, "No messages yet") {
		t.Fatalf("cleared messages should show placeholder:\n%s", view)
	}
	if m.perMsgRenderLen != 0 {
		t.Fatalf("expected perMsgRenderLen=0 after clear, got %d", m.perMsgRenderLen)
	}
}

func TestRenderedMessageBody_AppendOnlyPreservesExistingCache(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}

	m.renderedMessageBody()
	cached0 := m.perMsgRenderCache[0]
	cached1 := m.perMsgRenderCache[1]

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "next question"})
	m.renderedMessageBody()

	if m.perMsgRenderCache[0] != cached0 {
		t.Fatalf("cache[0] should be preserved after append")
	}
	if m.perMsgRenderCache[1] != cached1 {
		t.Fatalf("cache[1] should be preserved after append")
	}
	if len(m.perMsgRenderCache) != 3 {
		t.Fatalf("expected 3 cache entries, got %d", len(m.perMsgRenderCache))
	}
}

func TestRenderedMessageBody_InsertClearsCache(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: ""},
		},
	}

	m.renderedMessageBody()
	if len(m.perMsgRenderCache) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m.perMsgRenderCache))
	}

	m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: "tool_result"})
	_ = m.renderedMessageBody()

	if m.perMsgRenderLen != 3 {
		t.Fatalf("expected perMsgRenderLen=3 after insert, got %d", m.perMsgRenderLen)
	}

	if !strings.Contains(m.perMsgRenderCache[1], "tool_result") {
		t.Fatalf("inserted tool message should be at index 1, got:\n%s", m.perMsgRenderCache[1])
	}
}
