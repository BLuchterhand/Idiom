package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/runtime/llama"
)

var _ = ginkgo.It("rendered message body per message cache append only", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "first body"},
		},
	}

	first := m.renderedMessageBody()
	gomega.Expect(strings.Contains(first, "first body")).To(gomega.BeTrue(), fmt.Sprintf("initial render missing first body:\n%s", first))

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(1), fmt.Sprintf("expected 1 cached entry after first render, got %d", len(m.perMsgRenderCache)))

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(1), fmt.Sprintf("expected perMsgRenderLen=1, got %d", m.perMsgRenderLen))

	cachedFirst := m.perMsgRenderCache[0]

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "second body"})

	updated := m.renderedMessageBody()

	gomega.Expect(strings.Contains(updated, "first body")).To(gomega.BeTrue(), fmt.Sprintf("updated render missing first body:\n%s", updated))

	gomega.Expect(strings.Contains(updated, "second body")).To(gomega.BeTrue(), fmt.Sprintf("updated render missing second body:\n%s", updated))

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(2), fmt.Sprintf("expected 2 cached entries after append, got %d", len(m.perMsgRenderCache)))

	gomega.Expect(m.perMsgRenderCache[0]).To(gomega.Equal(cachedFirst), fmt.Sprintf("first message cache entry changed after append — should have been reused"))
})

var _ = ginkgo.It("rendered message body per message cache clears on insert", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "user msg"},
			{Role: "assistant", Content: ""},
		},
	}

	m.renderedMessageBody()

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(2), fmt.Sprintf("expected 2 cached entries, got %d", len(m.perMsgRenderCache)))

	m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: "tool_output | result"})

	gomega.Expect(len(m.messages)).To(gomega.Equal(3), fmt.Sprintf("expected 3 messages after insert, got %d", len(m.messages)))

	result := m.renderedMessageBody()
	gomega.Expect(strings.Contains(result, "tool_output")).To(gomega.BeTrue(), fmt.Sprintf("render after insert missing tool name:\n%s", result))

	gomega.Expect(strings.Contains(result, "user msg")).To(gomega.BeTrue(), fmt.Sprintf("render after insert missing user msg:\n%s", result))

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(3), fmt.Sprintf("expected 3 cached entries after insert rebuild, got %d", len(m.perMsgRenderCache)))
})

var _ = ginkgo.It("rendered message body per message cache clears on width change", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "some content"},
		},
	}

	m.renderedMessageBody()

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(1), fmt.Sprintf("expected 1 cached entry, got %d", len(m.perMsgRenderCache)))

	cached := m.perMsgRenderCache[0]

	m.viewport.Width = 60
	m.renderedMessageBody()

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(1), fmt.Sprintf("expected 1 cached entry after resize, got %d", len(m.perMsgRenderCache)))

	gomega.Expect(m.perMsgRenderCache[0]).NotTo(gomega.Equal(cached), fmt.Sprintf("cache entry should have been re-rendered after width change"))
})

var _ = ginkgo.It("rendered message body per message cache multiple appends", func() {
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

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(5), fmt.Sprintf("expected 5 cached entries after 5 appends, got %d", len(m.perMsgRenderCache)))

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(5), fmt.Sprintf("expected perMsgRenderLen=5, got %d", m.perMsgRenderLen))

	for i := 0; i < 5; i++ {
		expected := "msg " + string(rune('A'+i))
		gomega.Expect(strings.Contains(m.perMsgRenderCache[i], expected)).To(gomega.BeTrue(), fmt.Sprintf("cache entry %d missing %q", i, expected))
	}
})

var _ = ginkgo.It("rendered message body per message cache clears on content mutation", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original"},
			{Role: "assistant", Content: "reply"},
		},
	}

	m.renderedMessageBody()

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(2), fmt.Sprintf("expected perMsgRenderLen=2, got %d", m.perMsgRenderLen))

	m.messages[1].Content = "updated reply"

	result := m.renderedMessageBody()

	gomega.Expect(strings.Contains(result, "updated reply")).To(gomega.BeTrue(), fmt.Sprintf("expected updated content in render:\n%s", result))
})

var _ = ginkgo.It("invalidate message render cache clears per message cache", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "body"},
		},
	}

	m.renderedMessageBody()

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(1), fmt.Sprintf("expected 1 cached entry, got %d", len(m.perMsgRenderCache)))

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(1), fmt.Sprintf("expected perMsgRenderLen=1, got %d", m.perMsgRenderLen))

	m.invalidateMessageRenderCache()

	gomega.Expect(m.perMsgRenderCache).To(gomega.BeNil(), fmt.Sprintf("expected perMsgRenderCache to be nil after invalidation"))

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(0), fmt.Sprintf("expected perMsgRenderLen=0 after invalidation, got %d", m.perMsgRenderLen))
})

var _ = ginkgo.It("handle tool trace compact mode skips main render", func() {
	m := &Model{
		ready:                    true,
		viewport:                 viewport.New(48, 12),
		events:                   make(chan tea.Msg, 256),
		messages:                 []llama.ChatMessage{{Role: "user", Content: "hi"}},
		traceMode:                traceDisplayCompact,
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
	gomega.Expect(contentBefore).To(gomega.Equal(contentAfter), fmt.Sprintf("compact trace mode should not change main viewport content"))

	entry := &m.history.entries[1]
	found := false

	for _, tc := range entry.toolCalls {
		if strings.Contains(tc.line, "read_file") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("expected tool trace to be recorded in history entry in compact mode, calls: %+v", entry.toolCalls))
})

var _ = ginkgo.It("handle tool trace detail mode renders", func() {
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
	gomega.Expect(strings.Contains(view, "read_file")).To(gomega.BeTrue(), fmt.Sprintf("detail trace mode should render tool in main viewport:\n%s", view))
})

var _ = ginkgo.It("render plan line uses precompiled styles", func() {
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
		ginkgo.By(fmt.Sprint(tc.name), func() {
			result := (&Model{}).renderPlanLine(tc.input, -1, 40)
			gomega.Expect(strings.Contains(result, tc.contains)).To(gomega.BeTrue(), fmt.Sprintf("renderPlanLine(%q) missing %q in:\n%s", tc.input, tc.contains, result))

			gomega.Expect(strings.Contains(result, "<!--")).To(gomega.BeFalse(), fmt.Sprintf("renderPlanLine should strip HTML comments, got:\n%s", result))
		})
	}
})

var _ = ginkgo.It("parse plan items", func() {
	content := "# Plan\n- [ ] step one\n- [x] step two\n"
	rows := (&Model{}).currentPlanSelectableRowsForContent(content)

	var items []planItemMeta
	for _, row := range rows {
		if row.Kind == planSelectableItem {
			items = append(items, row.Item)
		}
	}

	gomega.Expect(len(items)).To(gomega.Equal(2), fmt.Sprintf("expected 2 plan items, got %d", len(items)))

	gomega.Expect(items[0].Checked).To(gomega.BeFalse(), fmt.Sprintf("first should be unchecked, second checked"))
	gomega.Expect(items[1].Checked).To(gomega.BeTrue(), fmt.Sprintf("first should be unchecked, second checked"))
})

var _ = ginkgo.It("render history panel content precompiled styles", func() {
	m := &Model{
		history: historyState{
			panelOpen: true,
			focused:   true,
			selected:  0,
			expanded:  true,
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
	gomega.Expect(strings.Contains(view, "test summary")).To(gomega.BeTrue(), fmt.Sprintf("history panel should contain summary:\n%s", view))

	gomega.Expect(strings.Contains(view, "agent response text")).To(gomega.BeTrue(), fmt.Sprintf("history panel should contain expanded content:\n%s", view))

	gomega.Expect(strings.Contains(view, "read_file")).To(gomega.BeTrue(), fmt.Sprintf("history panel should contain tool call:\n%s", view))
})

var _ = ginkgo.It("render messages spinner still updates during streaming", func() {
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
	gomega.Expect(strings.Contains(view1, "Qwen is preparing a response")).To(gomega.BeTrue(), fmt.Sprintf("streaming view should show working indicator:\n%s", view1))

	m.workingFrame = 3
	m.renderMessages(false)

	view2 := m.viewport.View()
	gomega.Expect(strings.Contains(view2, "Qwen is preparing a response")).To(gomega.BeTrue(), fmt.Sprintf("streaming view should still show working indicator:\n%s", view2))

	gomega.Expect(view1).NotTo(gomega.Equal(view2), fmt.Sprintf("spinner animation should update between frames"))
})

var _ = ginkgo.It("render messages cache hit on spinner tick", func() {
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

	gomega.Expect(first).To(gomega.Equal(second), fmt.Sprintf("same messages should produce identical cached output"))

	gomega.Expect(hash1).To(gomega.Equal(hash2), fmt.Sprintf("hash should be stable for identical input"))

	gomega.Expect(m.messageRenderCache).To(gomega.Equal(first), fmt.Sprintf("cache should be reused on second call"))
})

var _ = ginkgo.It("message render fingerprint detects changes", func() {
	msgs1 := []llama.ChatMessage{{Role: "user", Content: "hello"}}
	msgs2 := []llama.ChatMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "world"}}
	msgs3 := []llama.ChatMessage{{Role: "user", Content: "different"}}

	h1 := messageRenderFingerprint(msgs1)
	h2 := messageRenderFingerprint(msgs2)
	h3 := messageRenderFingerprint(msgs3)

	gomega.Expect(h1).NotTo(gomega.Equal(h2), fmt.Sprintf("fingerprints should differ when message count changes"))

	gomega.Expect(h1).NotTo(gomega.Equal(h3), fmt.Sprintf("fingerprints should differ when content changes"))

	h1again := messageRenderFingerprint(msgs1)
	gomega.Expect(h1).To(gomega.Equal(h1again), fmt.Sprintf("fingerprint should be deterministic for same input"))
})

var _ = ginkgo.It("render messages empty messages shows placeholder", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{},
	}

	m.renderMessages(false)

	view := m.viewport.View()
	gomega.Expect(strings.Contains(view, "No messages yet")).To(gomega.BeTrue(), fmt.Sprintf("empty state should show placeholder:\n%s", view))
})

var _ = ginkgo.It("render messages clear messages shows placeholder", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{{Role: "user", Content: "hi"}},
	}

	m.renderMessages(true)

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(1), fmt.Sprintf("expected perMsgRenderLen=1, got %d", m.perMsgRenderLen))

	m.messages = nil
	m.renderMessages(true)

	view := m.viewport.View()
	gomega.Expect(strings.Contains(view, "No messages yet")).To(gomega.BeTrue(), fmt.Sprintf("cleared messages should show placeholder:\n%s", view))

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(0), fmt.Sprintf("expected perMsgRenderLen=0 after clear, got %d", m.perMsgRenderLen))
})

var _ = ginkgo.It("rendered message body append only preserves existing cache", func() {
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

	gomega.Expect(m.perMsgRenderCache[0]).To(gomega.Equal(cached0), fmt.Sprintf("cache[0] should be preserved after append"))

	gomega.Expect(m.perMsgRenderCache[1]).To(gomega.Equal(cached1), fmt.Sprintf("cache[1] should be preserved after append"))

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(3), fmt.Sprintf("expected 3 cache entries, got %d", len(m.perMsgRenderCache)))
})

var _ = ginkgo.It("rendered message body insert clears cache", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: ""},
		},
	}

	m.renderedMessageBody()

	gomega.Expect(len(m.perMsgRenderCache)).To(gomega.Equal(2), fmt.Sprintf("expected 2 entries, got %d", len(m.perMsgRenderCache)))

	m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: "tool_result"})

	_ = m.renderedMessageBody()

	gomega.Expect(m.perMsgRenderLen).To(gomega.Equal(3), fmt.Sprintf("expected perMsgRenderLen=3 after insert, got %d", m.perMsgRenderLen))

	gomega.Expect(strings.Contains(m.perMsgRenderCache[1], "tool_result")).To(gomega.BeTrue(), fmt.Sprintf("inserted tool message should be at index 1, got:\n%s", m.perMsgRenderCache[1]))
})
