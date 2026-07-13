package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/runtime/agent"
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

var _ = ginkgo.It("run standard prompt once bookkeeping produces user then assistant", func() {
	// finishPromptOnceReply is the second half of runStandardPromptOnce.
	// The first half appends the user message before calling the agent;
	// this test simulates that pair of appends and asserts the final
	// m.messages shape — exactly [user, assistant], no placeholder.
	m := newDetachedPromptExecutionSession(context.Background(), &Model{})

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "hi"})
	m.finishPromptOnceReply("hello there")

	gomega.Expect(len(m.messages)).To(gomega.Equal(2), fmt.Sprintf("expected 2 messages, got %d: %#v", len(m.messages), m.messages))

	gomega.Expect(m.messages[0].Role).To(gomega.Equal("user"), fmt.Sprintf("messages[0] = %#v, want {user, hi}", m.messages[0]))
	gomega.Expect(m.messages[0].Content).To(gomega.Equal("hi"), fmt.Sprintf("messages[0] = %#v, want {user, hi}", m.messages[0]))

	gomega.Expect(m.messages[1].Role).To(gomega.Equal("assistant"), fmt.Sprintf("messages[1] = %#v, want {assistant, hello there}", m.messages[1]))
	gomega.Expect(m.messages[1].Content).To(gomega.Equal("hello there"), fmt.Sprintf("messages[1] = %#v, want {assistant, hello there}", m.messages[1]))
})

var _ = ginkgo.It("sequential turns do not accumulate ghost placeholders", func() {
	// Two back-to-back turns should leave messages = [u1, a1, u2, a2] —
	// never 5 or 6 entries (which would indicate a ghost placeholder is
	// being carried across turns).
	m := newDetachedPromptExecutionSession(context.Background(), &Model{})

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "first"})
	m.finishPromptOnceReply("one")

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "second"})
	m.finishPromptOnceReply("two")

	gomega.Expect(len(m.messages)).To(gomega.Equal(4), fmt.Sprintf("expected 4 messages after two turns, got %d: %#v", len(m.messages), m.messages))

	expected := []llama.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "one"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "two"},
	}
	for i, want := range expected {
		got := m.messages[i]
		gomega.Expect(got.Role).To(gomega.Equal(want.Role), fmt.Sprintf("messages[%d] = %#v, want %#v", i, got, want))
		gomega.Expect(got.Content).To(gomega.Equal(want.Content), fmt.Sprintf("messages[%d] = %#v, want %#v", i, got, want))
	}
})

var _ = ginkgo.It("last turn source results only seed continuing objectives", func() {
	m := &Model{}
	results := []workspace.ToolResult{{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Read docs/decisioning-north-star.md lines 1-6",
		Output:  "The model owns strategy; the harness owns contracts.",
	}}

	m.SetLastTurnSourceResults(
		"Would it fit our north star to replace this with model prose?",
		results,
	)

	{
		got := m.LastTurnSourceResultsForObjective("Could you elaborate on that risk?")
		gomega.Expect(len(got)).To(gomega.Equal(1), fmt.Sprintf("continuing follow-up should inherit prior source evidence, got %#v", got))
	}

	{
		got := m.LastTurnSourceResultsForObjective("we need a better and more generic name than qwen for the agent in this project")
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("unrelated naming turn must not inherit prior source evidence, got %#v", got))
	}
})

var _ = ginkgo.It("finish prompt once reply returns reply verbatim", func() {
	// Callers (sendPromptAsync, the CLI entry points) rely on the return
	// value; losing it would silently break non-interactive use.
	m := newDetachedPromptExecutionSession(context.Background(), &Model{})
	got := m.finishPromptOnceReply("final text")

	gomega.Expect(got).To(gomega.Equal("final text"), fmt.Sprintf("finishPromptOnceReply = %q, want %q", got, "final text"))
})

var _ = ginkgo.It("finish prompt once reply suppresses nested plan execute transcript", func() {
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		promptRuntimeState: promptRuntimeState{promptRuntimeSnapshotState: promptRuntimeSnapshotState{
			suppressPromptTranscript: true,
		}},
		messages: []llama.ChatMessage{
			{Role: "user", Content: "outer request"},
		},
	})

	got := m.finishPromptOnceReply("internal planner blocker")
	gomega.Expect(got).To(gomega.Equal("internal planner blocker"), fmt.Sprintf("finishPromptOnceReply = %q", got))

	gomega.Expect(len(m.messages)).To(gomega.Equal(1), fmt.Sprintf("suppressed nested reply should not append to visible transcript: %#v", m.messages))
	gomega.Expect(m.messages[0].Content).To(gomega.Equal("outer request"), fmt.Sprintf("suppressed nested reply should not append to visible transcript: %#v", m.messages))
})

var _ = ginkgo.It("accepted plan execute lifecycle reports canonical and presented paths to its caller", func() {
	var observed []string
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		promptRuntimeState: promptRuntimeState{promptRuntimeSnapshotState: promptRuntimeSnapshotState{
			planExecuteActive: true,
			planExecuteSourceSatisfiedObserver: func(paths []string) {
				observed = append(observed, paths...)
			},
			suppressPromptTranscript: true,
		}},
	})

	reply := plancontract.SourceSatisfiedDecision + "\n- alias/feature.go: the current state already contains the requested behavior."
	gomega.Expect(m.finishPromptOnceReply(reply)).To(gomega.Equal(reply))
	gomega.Expect(observed).To(gomega.BeEmpty(), "final-answer prose must not mint trusted source-satisfied evidence")
	state := recoverycontract.ImplementationState{
		TerminalDisposition:  "source_satisfied",
		SourceSatisfiedPaths: []string{"real/feature.go"},
		CanonicalMutationScope: &recoverycontract.CanonicalMutationScopeProjection{
			Presentations: []recoverycontract.CanonicalMutationScopePresentationProjection{{
				PresentedPath: "alias/feature.go", CanonicalID: "real/feature.go", Source: "required_path",
			}},
		},
	}
	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	m.observePlanExecuteSourceSatisfiedLifecycle(agent.RunEvent{Role: "implementation_lifecycle_state", Content: string(body)})
	gomega.Expect(observed).To(gomega.ConsistOf("real/feature.go", "alias/feature.go"))
})

var _ = ginkgo.It("finish prompt once reply rejects tool call xml", func() {
	m := newDetachedPromptExecutionSession(context.Background(), &Model{})
	got := m.finishPromptOnceReply("<invoke name=\"read_file\">\n<parameter name=\"path\">foo.go</parameter>\n</invoke>")

	gomega.Expect(got).NotTo(gomega.Equal("<invoke name=\"read_file\">\n<parameter name=\"path\">foo.go</parameter>\n</invoke>"), fmt.Sprint("finishPromptOnceReply should replace tool-call XML with clean message"))

	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("<invoke")), fmt.Sprintf("reply still contains invoke XML: %q", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("</invoke>")), fmt.Sprintf("reply still contains invoke XML: %q", got))

	gomega.Expect(len(got) < 10).To(gomega.BeFalse(), fmt.Sprintf("expected substantive replacement, got %q", got))

	// Verify clean prose also passes through unchanged
	clean := m.finishPromptOnceReply("The lane transitions back to main state after implementation success.")
	gomega.Expect(clean).To(gomega.Equal("The lane transitions back to main state after implementation success."), fmt.Sprintf("clean prose should pass through unchanged, got %q", clean))
})

// renderMessages spinner behaviour — the second half of the fix.

func newRenderModel(t ginkgo.FullGinkgoTInterface) *Model {
	ginkgo.GinkgoHelper()

	return &Model{
		ready:    true,
		viewport: viewport.New(80, 20),
		spinner:  spinner.New(),
	}
}

var _ = ginkgo.It("render messages shows spinner while streaming", func() {
	t := ginkgo.GinkgoT()
	// With the fix, the working affordance is rendered inline based on
	// m.streaming — no empty-assistant placeholder required in messages.
	m := newRenderModel(t)

	m.streaming = true
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "what's up"},
	}
	m.renderMessages(true)

	view := m.viewport.View()
	gomega.Expect(strings.Contains(view, "Qwen is preparing a response")).To(gomega.BeTrue(), fmt.Sprintf("expected working affordance while streaming, got:\n%s", view))
})

var _ = ginkgo.It("render messages hides spinner when not streaming", func() {
	t := ginkgo.GinkgoT()
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
	gomega.Expect(strings.Contains(view, "Qwen is preparing a response")).To(gomega.BeFalse(), fmt.Sprintf("working affordance must not render when streaming=false, got:\n%s", view))
})

var _ = ginkgo.It("render messages spinner does not duplicate with empty assistant", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(count).To(gomega.Equal(1), fmt.Sprintf("expected exactly one working affordance, got %d in:\n%s", count, view))
})

var _ = ginkgo.It("render messages uses active evidence instead of generic spinner", func() {
	t := ginkgo.GinkgoT()
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
	gomega.Expect(strings.Contains(view, "rendering.go")).To(gomega.BeTrue(), fmt.Sprintf("expected live tool evidence to stay visible, got:\n%s", view))

	gomega.Expect(strings.Contains(view, "Qwen is preparing a response")).To(gomega.BeFalse(), fmt.Sprintf("generic spinner should not render below active evidence, got:\n%s", view))
})

var _ = ginkgo.It("render messages shows thought panel while streaming", func() {
	t := ginkgo.GinkgoT()
	m := newRenderModel(t)

	m.streaming = true
	m.messages = []llama.ChatMessage{
		{Role: "user", Content: "inspect the status rendering"},
	}
	m.thoughts.append(1, "I should inspect rendering.go before changing the TUI.")

	m.renderMessages(true)

	view := m.viewport.View()
	gomega.Expect(strings.Contains(view, "Qwen is preparing a response")).To(gomega.BeTrue(), fmt.Sprintf("expected working affordance while streaming, got:\n%s", view))

	gomega.Expect(view).To(gomega.ContainSubstring("thoughts · 1"), fmt.Sprintf("expected thought panel under streaming affordance, got:\n%s", view))
	gomega.Expect(view).To(gomega.ContainSubstring("inspect rendering.go"), fmt.Sprintf("expected thought panel under streaming affordance, got:\n%s", view))
})

var _ = ginkgo.It("render messages shows thought panel with active evidence", func() {
	t := ginkgo.GinkgoT()
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
	gomega.Expect(view).To(gomega.ContainSubstring("rendering.go"), fmt.Sprintf("expected thought panel to remain visible with active evidence, got:\n%s", view))
	gomega.Expect(view).To(gomega.ContainSubstring("thoughts · 1"), fmt.Sprintf("expected thought panel to remain visible with active evidence, got:\n%s", view))

	gomega.Expect(strings.Contains(view, "Qwen is preparing a response")).To(gomega.BeFalse(), fmt.Sprintf("generic spinner should still be suppressed below active evidence, got:\n%s", view))
})

var _ = ginkgo.It("empty enter collapses thought panel", func() {
	t := ginkgo.GinkgoT()
	m := newRenderModel(t)
	m.streaming = true
	m.events = make(chan tea.Msg, 1)
	m.thoughts.append(1, "First line of model reasoning.")

	gomega.Expect(m.thoughts.expanded).To(gomega.BeTrue(), fmt.Sprint("thought panel should expand when a thought arrives"))

	_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("empty Enter should be handled when thought panel is visible"))

	gomega.Expect(m.thoughts.expanded).To(gomega.BeFalse(), fmt.Sprint("empty Enter should collapse the thought panel"))

	m.renderMessages(true)
	view := m.viewport.View()
	gomega.Expect(strings.Contains(view, "▸ thoughts · 1")).To(gomega.BeTrue(), fmt.Sprintf("expected collapsed thought panel header, got:\n%s", view))

	gomega.Expect(strings.Contains(view, "First line of model reasoning.")).To(gomega.BeFalse(), fmt.Sprintf("collapsed thought panel should hide body text, got:\n%s", view))
})

var _ = ginkgo.It("mouse click collapses thought panel", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(row < 0).To(gomega.BeFalse(), fmt.Sprintf("thought panel header not visible:\n%s", m.viewport.View()))

	_, handled := m.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      2,
		Y:      row + 2,
	})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("thought panel click should be handled"))

	gomega.Expect(m.thoughts.expanded).To(gomega.BeFalse(), fmt.Sprint("mouse click should collapse the thought panel"))
})

var _ = ginkgo.It("thought panel retains many entries while rendering bounded window", func() {
	var panel thoughtPanelState
	for i := 1; i <= 24; i++ {
		panel.append(i, "thought line "+string(rune('a'+i-1)))
	}

	gomega.Expect(len(panel.entries)).To(gomega.Equal(24), fmt.Sprintf("entries = %d, want full current-turn feed", len(panel.entries)))

	got := renderThoughtPanel(&panel, 80)
	gomega.Expect(strings.Contains(got, "earlier thought line(s) hidden")).To(gomega.BeTrue(), fmt.Sprintf("expected row overflow summary, got:\n%s", got))

	gomega.Expect(strings.Contains(got, "[1]")).To(gomega.BeFalse(), fmt.Sprintf("oldest thoughts should scroll out of the bounded window without being dropped, got:\n%s", got))

	panel.scroll(-1000)
	scrolled := renderThoughtPanel(&panel, 80)
	gomega.Expect(strings.Contains(scrolled, "[1] thought line a")).To(gomega.BeTrue(), fmt.Sprintf("oldest thoughts should remain scrollable, got:\n%s", scrolled))
})

var _ = ginkgo.It("thought panel auto scrolls only when at bottom", func() {
	var panel thoughtPanelState
	for i := 1; i <= 12; i++ {
		panel.append(i, "thought line "+string(rune('a'+i-1)))
	}

	atBottom := renderThoughtPanel(&panel, 80)
	gomega.Expect(strings.Contains(atBottom, "[12] thought line l")).To(gomega.BeTrue(), fmt.Sprintf("bottom view should include latest thought, got:\n%s", atBottom))

	gomega.Expect(panel.scroll(-3)).To(gomega.BeTrue(), fmt.Sprint("expected panel to scroll up from bottom"))

	panel.append(13, "newest while reading earlier")
	held := renderThoughtPanel(&panel, 80)
	gomega.Expect(strings.Contains(held, "[13] newest while reading earlier")).To(gomega.BeFalse(), fmt.Sprintf("panel should preserve scrolled window when not at bottom, got:\n%s", held))

	gomega.Expect(strings.Contains(held, "later thought line(s) hidden")).To(gomega.BeTrue(), fmt.Sprintf("scrolled panel should show later hidden summary, got:\n%s", held))

	panel.scrollToBottom()
	panel.append(14, "newest after returning to bottom")
	following := renderThoughtPanel(&panel, 80)
	gomega.Expect(strings.Contains(following, "[14] newest after returning to bottom")).To(gomega.BeTrue(), fmt.Sprintf("panel should follow new thoughts again at bottom, got:\n%s", following))
})

var _ = ginkgo.It("thought panel auto scrolls past four long wrapped thoughts", func() {
	var panel thoughtPanelState
	for i := 1; i <= 9; i++ {
		panel.append(i, strings.Repeat("long wrapped thought "+string(rune('a'+i-1))+" ", 8))
	}

	got := renderThoughtPanel(&panel, 36)
	gomega.Expect(strings.Contains(got, "thought i")).To(gomega.BeTrue(), fmt.Sprintf("bottom view should include latest wrapped thought after more than four entries, got:\n%s", got))

	lines := strings.Split(got, "\n")
	gomega.Expect(len(lines) > thoughtPanelMaxRows+1).To(gomega.BeFalse(), fmt.Sprintf("thought panel should render a bounded window, got %d line(s):\n%s", len(lines), got))
})

var _ = ginkgo.It("thought panel handler follows newest after many thoughts", func() {
	t := ginkgo.GinkgoT()
	m := newRenderModel(t)
	m.streaming = true
	m.viewport.Width = 36
	m.viewport.Height = 12

	for i := 1; i <= 9; i++ {
		m.handleThought(thoughtMsg{
			step: i,
			text: strings.Repeat("handler thought "+string(rune('a'+i-1))+" ", 8),
		})
	}

	view := m.viewport.View()
	gomega.Expect(strings.Contains(view, "thoughts · 9")).To(gomega.BeTrue(), fmt.Sprintf("expected all handled thoughts to be retained, got:\n%s", view))

	gomega.Expect(strings.Contains(view, "thought i")).To(gomega.BeTrue(), fmt.Sprintf("handler/render loop should follow newest thought past the first five, got:\n%s", view))

	gomega.Expect(strings.Contains(view, "thought a")).To(gomega.BeFalse(), fmt.Sprintf("newest-following view should scroll older thoughts out of the bounded panel body, got:\n%s", view))
})

var _ = ginkgo.It("mouse wheel scrolls thought panel without main viewport scroll", func() {
	t := ginkgo.GinkgoT()
	m := newRenderModel(t)
	m.streaming = true
	m.events = make(chan tea.Msg, 1)
	for i := 1; i <= 12; i++ {
		m.thoughts.append(i, "thought line "+string(rune('a'+i-1)))
	}
	m.renderMessages(true)

	_, bodyStart, _, ok := thoughtPanelViewportRows(m.viewport.View())
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("thought panel not visible:\n%s", m.viewport.View()))

	beforeYOffset := m.viewport.YOffset
	_, handled := m.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
		X:      2,
		Y:      bodyStart + 2,
	})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("wheel over thought panel should be handled"))

	gomega.Expect(m.thoughts.scrollOff).NotTo(gomega.Equal(0), fmt.Sprint("wheel up over thought panel should move the panel scroll offset"))

	gomega.Expect(m.viewport.YOffset).To(gomega.Equal(beforeYOffset), fmt.Sprintf("main viewport should not scroll when thought panel handles wheel: got %d want %d", m.viewport.YOffset, beforeYOffset))
})

var _ = ginkgo.It("thought panel skips duplicate entries", func() {
	var panel thoughtPanelState

	panel.append(2, "same thought")
	panel.append(2, "same thought")

	gomega.Expect(len(panel.entries)).To(gomega.Equal(1), fmt.Sprintf("duplicate thought should be skipped, got %d entries", len(panel.entries)))
})

var _ = ginkgo.It("begin agent history turn resets thought panel", func() {
	t := ginkgo.GinkgoT()
	m := newRenderModel(t)
	m.thoughts.append(1, "stale thought from a previous turn")

	m.beginAgentHistoryTurn()

	gomega.Expect(m.thoughts.visible()).To(gomega.BeFalse(), fmt.Sprintf("new user-visible turn should reset stale thoughts, got %#v", m.thoughts.entries))
})

var _ = ginkgo.It("render messages does not duplicate assistant reply", func() {
	t := ginkgo.GinkgoT()
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
	{
		n := strings.Count(view, "xyzzy42")
		gomega.Expect(n).To(gomega.Equal(1), fmt.Sprintf("assistant reply should render exactly once, got %d occurrences:\n%s", n, view))
	}
})

var _ = ginkgo.It("render messages empty state only without streaming", func() {
	t := ginkgo.GinkgoT()
	// The "No messages yet" hint should appear only when the conversation
	// is genuinely idle. Showing it alongside the working affordance
	// would look broken right after the user sends the first prompt
	// (since the user message isn't yet appended to m.messages until
	// runStandardPromptOnce picks up the turn).
	m := newRenderModel(t)

	m.streaming = true
	m.renderMessages(true)

	view := m.viewport.View()
	gomega.Expect(strings.Contains(view, "No messages yet")).To(gomega.BeFalse(), fmt.Sprintf("empty-state hint must not render while streaming, got:\n%s", view))

	gomega.Expect(strings.Contains(view, "Qwen is preparing a response")).To(gomega.BeTrue(), fmt.Sprintf("expected working affordance on fresh streaming turn, got:\n%s", view))
})
