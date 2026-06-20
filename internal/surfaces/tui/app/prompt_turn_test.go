package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/runtime/llama"
)

// Regression tests that lock in the fix for the duplicate-assistant-message
// bug.
//
// History: sendPromptAsync used to append an empty assistant placeholder to
// m.messages (for the streaming spinner) and then re-emit the final reply
// via assistantReplyMsg, which filled that placeholder. But RunPromptOnce
// already owns m.messages bookkeeping end-to-end (appends user before the
// agent loop, appends assistant reply via finishPromptOnceReply after), so
// the reply ended up recorded twice. The rendered transcript looked like
//
//     [assistant: reply] [user: prompt] [assistant: reply]
//
// — the user's question sandwiched between two identical copies of the
// response. The fix made RunPromptOnce the single authority for m.messages
// and moved the spinner affordance into renderMessages, driven by
// m.streaming, so no empty placeholder is needed.

func TestRunStandardPromptOnceBookkeepingProducesUserThenAssistant(t *testing.T) {
	// finishPromptOnceReply is the second half of runStandardPromptOnce.
	// The first half appends the user message before calling the agent;
	// this test simulates that pair of appends and asserts the final
	// m.messages shape — exactly [user, assistant], no placeholder.
	m := &Model{}

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "hi"})
	m.finishPromptOnceReply("hello there")

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d: %#v", len(m.messages), m.messages)
	}

	if m.messages[0].Role != "user" || m.messages[0].Content != "hi" {
		t.Errorf("messages[0] = %#v, want {user, hi}", m.messages[0])
	}

	if m.messages[1].Role != "assistant" || m.messages[1].Content != "hello there" {
		t.Errorf("messages[1] = %#v, want {assistant, hello there}", m.messages[1])
	}
}

func TestSequentialTurnsDoNotAccumulateGhostPlaceholders(t *testing.T) {
	// Two back-to-back turns should leave messages = [u1, a1, u2, a2] —
	// never 5 or 6 entries (which would indicate a ghost placeholder is
	// being carried across turns).
	m := &Model{}

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "first"})
	m.finishPromptOnceReply("one")

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "second"})
	m.finishPromptOnceReply("two")

	if len(m.messages) != 4 {
		t.Fatalf("expected 4 messages after two turns, got %d: %#v", len(m.messages), m.messages)
	}

	expected := []llama.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "one"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "two"},
	}
	for i, want := range expected {
		got := m.messages[i]
		if got.Role != want.Role || got.Content != want.Content {
			t.Errorf("messages[%d] = %#v, want %#v", i, got, want)
		}
	}
}

func TestFinishPromptOnceReplyReturnsReplyVerbatim(t *testing.T) {
	// Callers (sendPromptAsync, the CLI entry points) rely on the return
	// value; losing it would silently break non-interactive use.
	m := &Model{}
	got := m.finishPromptOnceReply("final text")

	if got != "final text" {
		t.Errorf("finishPromptOnceReply = %q, want %q", got, "final text")
	}
}

func TestFinishPromptOnceReplySuppressesNestedPlanExecuteTranscript(t *testing.T) {
	m := &Model{
		suppressPromptTranscript: true,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "outer request"},
		},
	}

	got := m.finishPromptOnceReply("internal planner blocker")
	if got != "internal planner blocker" {
		t.Fatalf("finishPromptOnceReply = %q", got)
	}

	if len(m.messages) != 1 || m.messages[0].Content != "outer request" {
		t.Fatalf("suppressed nested reply should not append to visible transcript: %#v", m.messages)
	}
}

func TestFinishPromptOnceReplyRejectsToolCallXML(t *testing.T) {
	m := &Model{}
	got := m.finishPromptOnceReply("<invoke name=\"read_file\">\n<parameter name=\"path\">foo.go</parameter>\n</invoke>")

	if got == "<invoke name=\"read_file\">\n<parameter name=\"path\">foo.go</parameter>\n</invoke>" {
		t.Fatal("finishPromptOnceReply should replace tool-call XML with clean message")
	}

	if strings.Contains(got, "<invoke") || strings.Contains(got, "</invoke>") {
		t.Fatalf("reply still contains invoke XML: %q", got)
	}

	if len(got) < 10 {
		t.Fatalf("expected substantive replacement, got %q", got)
	}

	// Verify clean prose also passes through unchanged
	clean := m.finishPromptOnceReply("The lane transitions back to main state after implementation success.")
	if clean != "The lane transitions back to main state after implementation success." {
		t.Fatalf("clean prose should pass through unchanged, got %q", clean)
	}
}

// renderMessages spinner behaviour — the second half of the fix.

func newRenderModel(t *testing.T) *Model {
	t.Helper()

	return &Model{
		ready:    true,
		viewport: viewport.New(80, 20),
		spinner:  spinner.New(),
	}
}

func TestRenderMessagesShowsSpinnerWhileStreaming(t *testing.T) {
	// With the fix, the working affordance is rendered inline based on
	// m.streaming — no empty-assistant placeholder required in messages.
	m := newRenderModel(t)

	m.streaming = true
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "what's up"},
	}
	m.renderMessages(true)

	view := m.viewport.View()
	if !strings.Contains(view, "Qwen is preparing a response") {
		t.Fatalf("expected working affordance while streaming, got:\n%s", view)
	}
}

func TestRenderMessagesHidesSpinnerWhenNotStreaming(t *testing.T) {
	// Once the turn ends, m.streaming flips off and the inline affordance
	// must disappear — otherwise we'd leak a "working..." line under
	// every completed reply.
	m := newRenderModel(t)

	m.streaming = false
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "done"},
		{Role: "assistant", Content: "reply"},
	}
	m.renderMessages(true)

	view := m.viewport.View()
	if strings.Contains(view, "Qwen is preparing a response") {
		t.Fatalf("working affordance must not render when streaming=false, got:\n%s", view)
	}
}

func TestRenderMessagesSpinnerDoesNotDuplicateWithEmptyAssistant(t *testing.T) {
	// Belt-and-braces regression: if an empty-content assistant slot ever
	// slipped back into m.messages (e.g. from a future streaming-deltas
	// feature), the inline-spinner path must still render exactly once.
	// Under the old code, the empty slot itself produced a spinner too,
	// leading to a double affordance.
	m := newRenderModel(t)

	m.streaming = true
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: ""}, // hypothetical leftover
	}
	m.renderMessages(true)

	view := m.viewport.View()
	count := strings.Count(view, "Qwen is preparing a response")

	if count != 1 {
		t.Fatalf("expected exactly one working affordance, got %d in:\n%s", count, view)
	}
}

func TestRenderMessagesUsesActiveEvidenceInsteadOfGenericSpinner(t *testing.T) {
	m := newRenderModel(t)

	m.cfg.TUI.EvidencePanel = true
	m.streaming = true
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "inspect the status rendering"},
	}
	m.turnEvidence = &turnEvidence{
		expanded: true,
		tools: []evidenceRow{{
			step:     1,
			toolName: "read_file",
			target:   "internal/surfaces/tui/app/rendering.go",
			running:  true,
			glyph:    "…",
		}},
	}

	m.renderMessages(true)

	view := m.viewport.View()
	if !strings.Contains(view, "rendering.go") {
		t.Fatalf("expected live tool evidence to stay visible, got:\n%s", view)
	}

	if strings.Contains(view, "Qwen is preparing a response") {
		t.Fatalf("generic spinner should not render below active evidence, got:\n%s", view)
	}
}

func TestRenderMessagesShowsThoughtPanelWhileStreaming(t *testing.T) {
	m := newRenderModel(t)

	m.streaming = true
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "inspect the status rendering"},
	}
	m.thoughts.append(1, "I should inspect rendering.go before changing the TUI.")

	m.renderMessages(true)

	view := m.viewport.View()
	if !strings.Contains(view, "Qwen is preparing a response") {
		t.Fatalf("expected working affordance while streaming, got:\n%s", view)
	}

	if !strings.Contains(view, "thoughts · 1") || !strings.Contains(view, "inspect rendering.go") {
		t.Fatalf("expected thought panel under streaming affordance, got:\n%s", view)
	}
}

func TestRenderMessagesShowsThoughtPanelWithActiveEvidence(t *testing.T) {
	m := newRenderModel(t)

	m.cfg.TUI.EvidencePanel = true
	m.streaming = true
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "inspect the status rendering"},
	}
	m.turnEvidence = &turnEvidence{
		expanded: true,
		tools: []evidenceRow{{
			step:     1,
			toolName: "read_file",
			target:   "internal/surfaces/tui/app/rendering.go",
			running:  true,
			glyph:    "…",
		}},
	}
	m.thoughts.append(1, "The render path should stay local to the TUI.")

	m.renderMessages(true)

	view := m.viewport.View()
	if !strings.Contains(view, "rendering.go") || !strings.Contains(view, "thoughts · 1") {
		t.Fatalf("expected thought panel to remain visible with active evidence, got:\n%s", view)
	}

	if strings.Contains(view, "Qwen is preparing a response") {
		t.Fatalf("generic spinner should still be suppressed below active evidence, got:\n%s", view)
	}
}

func TestEmptyEnterCollapsesThoughtPanel(t *testing.T) {
	m := newRenderModel(t)
	m.streaming = true
	m.events = make(chan tea.Msg, 1)
	m.thoughts.append(1, "First line of model reasoning.")

	if !m.thoughts.expanded {
		t.Fatal("thought panel should expand when a thought arrives")
	}

	_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("empty Enter should be handled when thought panel is visible")
	}

	if m.thoughts.expanded {
		t.Fatal("empty Enter should collapse the thought panel")
	}

	m.renderMessages(true)
	view := m.viewport.View()
	if !strings.Contains(view, "▸ thoughts · 1") {
		t.Fatalf("expected collapsed thought panel header, got:\n%s", view)
	}

	if strings.Contains(view, "First line of model reasoning.") {
		t.Fatalf("collapsed thought panel should hide body text, got:\n%s", view)
	}
}

func TestMouseClickCollapsesThoughtPanel(t *testing.T) {
	m := newRenderModel(t)
	m.streaming = true
	m.events = make(chan tea.Msg, 1)
	m.thoughts.append(1, "First line of model reasoning.")
	m.renderMessages(true)

	row := -1
	for i, line := range strings.Split(m.viewport.View(), "\n") {
		if strings.Contains(line, "thoughts · 1") {
			row = i
			break
		}
	}

	if row < 0 {
		t.Fatalf("thought panel header not visible:\n%s", m.viewport.View())
	}

	_, handled := m.handleMouse(tea.MouseMsg{
		Type:   tea.MouseLeft,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      2,
		Y:      row + 2,
	})
	if !handled {
		t.Fatal("thought panel click should be handled")
	}

	if m.thoughts.expanded {
		t.Fatal("mouse click should collapse the thought panel")
	}
}

func TestThoughtPanelBoundsEntriesAndRows(t *testing.T) {
	var panel thoughtPanelState
	for i := 1; i <= thoughtPanelMaxEntries+3; i++ {
		panel.append(i, "line a\nline b")
	}

	if len(panel.entries) != thoughtPanelMaxEntries {
		t.Fatalf("entries = %d, want %d", len(panel.entries), thoughtPanelMaxEntries)
	}

	got := renderThoughtPanel(panel, 80)
	if !strings.Contains(got, "earlier thought line(s) hidden") {
		t.Fatalf("expected row overflow summary, got:\n%s", got)
	}

	if strings.Contains(got, "[1]") {
		t.Fatalf("oldest thoughts should be dropped from bounded panel, got:\n%s", got)
	}
}

func TestThoughtPanelAutoScrollsOnlyWhenAtBottom(t *testing.T) {
	var panel thoughtPanelState
	for i := 1; i <= thoughtPanelMaxEntries; i++ {
		panel.append(i, "thought line "+string(rune('a'+i-1)))
	}

	atBottom := renderThoughtPanel(panel, 80)
	if !strings.Contains(atBottom, "[12] thought line l") {
		t.Fatalf("bottom view should include latest thought, got:\n%s", atBottom)
	}

	if !panel.scroll(-3) {
		t.Fatal("expected panel to scroll up from bottom")
	}

	panel.append(13, "newest while reading earlier")
	held := renderThoughtPanel(panel, 80)
	if strings.Contains(held, "[13] newest while reading earlier") {
		t.Fatalf("panel should preserve scrolled window when not at bottom, got:\n%s", held)
	}

	if !strings.Contains(held, "later thought line(s) hidden") {
		t.Fatalf("scrolled panel should show later hidden summary, got:\n%s", held)
	}

	panel.scrollToBottom()
	panel.append(14, "newest after returning to bottom")
	following := renderThoughtPanel(panel, 80)
	if !strings.Contains(following, "[14] newest after returning to bottom") {
		t.Fatalf("panel should follow new thoughts again at bottom, got:\n%s", following)
	}
}

func TestMouseWheelScrollsThoughtPanelWithoutMainViewportScroll(t *testing.T) {
	m := newRenderModel(t)
	m.streaming = true
	m.events = make(chan tea.Msg, 1)
	for i := 1; i <= thoughtPanelMaxEntries; i++ {
		m.thoughts.append(i, "thought line "+string(rune('a'+i-1)))
	}
	m.renderMessages(true)

	_, bodyStart, _, ok := thoughtPanelViewportRows(m.viewport.View())
	if !ok {
		t.Fatalf("thought panel not visible:\n%s", m.viewport.View())
	}

	beforeYOffset := m.viewport.YOffset
	_, handled := m.handleMouse(tea.MouseMsg{
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
		X:      2,
		Y:      bodyStart + 2,
	})
	if !handled {
		t.Fatal("wheel over thought panel should be handled")
	}

	if m.thoughts.scrollOff == 0 {
		t.Fatal("wheel up over thought panel should move the panel scroll offset")
	}

	if m.viewport.YOffset != beforeYOffset {
		t.Fatalf("main viewport should not scroll when thought panel handles wheel: got %d want %d", m.viewport.YOffset, beforeYOffset)
	}
}

func TestThoughtPanelSkipsDuplicateEntries(t *testing.T) {
	var panel thoughtPanelState

	panel.append(2, "same thought")
	panel.append(2, "same thought")

	if len(panel.entries) != 1 {
		t.Fatalf("duplicate thought should be skipped, got %d entries", len(panel.entries))
	}
}

func TestBeginAgentHistoryTurnResetsThoughtPanel(t *testing.T) {
	m := newRenderModel(t)
	m.thoughts.append(1, "stale thought from a previous turn")

	m.beginAgentHistoryTurn()

	if m.thoughts.visible() {
		t.Fatalf("new user-visible turn should reset stale thoughts, got %#v", m.thoughts.entries)
	}
}

func TestRenderMessagesDoesNotDuplicateAssistantReply(t *testing.T) {
	// Direct regression test for the reported bug: given the m.messages
	// shape the fixed turn bookkeeping produces ([user, assistant]), the
	// rendered view should contain the reply text exactly once — never
	// wrapping the user message with two copies.
	m := newRenderModel(t)

	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "please explain"},
		{Role: "assistant", Content: "this is a unique sentinel string xyzzy42"},
	}
	m.renderMessages(true)

	view := m.viewport.View()
	if n := strings.Count(view, "xyzzy42"); n != 1 {
		t.Fatalf("assistant reply should render exactly once, got %d occurrences:\n%s", n, view)
	}
}

func TestRenderMessagesEmptyStateOnlyWithoutStreaming(t *testing.T) {
	// The "No messages yet" hint should appear only when the conversation
	// is genuinely idle. Showing it alongside the working affordance
	// would look broken right after the user sends the first prompt
	// (since the user message isn't yet appended to m.messages until
	// runStandardPromptOnce picks up the turn).
	m := newRenderModel(t)

	m.streaming = true
	m.renderMessages(true)

	view := m.viewport.View()
	if strings.Contains(view, "No messages yet") {
		t.Fatalf("empty-state hint must not render while streaming, got:\n%s", view)
	}

	if !strings.Contains(view, "Qwen is preparing a response") {
		t.Fatalf("expected working affordance on fresh streaming turn, got:\n%s", view)
	}
}
