package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/contracts/runprofile"
	"idiom/internal/evidence/transcript"
	"idiom/internal/platform/config"
)

var _ = ginkgo.It("input history navigation uses submitted prompts", func() {
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  textarea.New(),
	}
	m.recordInputHistory("first prompt")
	m.recordInputHistory("second prompt")

	m.input.SetValue("draft prompt")

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected up arrow to navigate input history"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("second prompt"), fmt.Sprintf("first up should show newest prompt, got %q", got))
	}

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected second up arrow to navigate input history"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("first prompt"), fmt.Sprintf("second up should show older prompt, got %q", got))
	}

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected down arrow to navigate input history"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("second prompt"), fmt.Sprintf("down should move toward newer prompt, got %q", got))
	}

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected down arrow to restore draft after newest prompt"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("draft prompt"), fmt.Sprintf("down past newest should restore draft, got %q", got))
	}
})

var _ = ginkgo.It("input history navigation updates rendered input through model update", func() {
	m := &Model{
		events:       make(chan tea.Msg, 4),
		input:        textarea.New(),
		inputHistory: []string{"first prompt", "second prompt"},
		streaming:    true,
	}
	m.inputHistoryIndex = -1
	m.input.SetValue("draft prompt")
	m.width = 80
	m.height = 24
	m.resize()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got, ok := updated.(*Model)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("Update returned %T, want *Model", updated))

	{
		value := got.input.Value()
		gomega.Expect(value).To(gomega.Equal("second prompt"), fmt.Sprintf("up arrow should set input value while streaming, got %q", value))
	}

	{
		view := got.View()
		gomega.Expect(strings.Contains(view, "second prompt")).To(gomega.BeTrue(), fmt.Sprintf("rendered view should include recalled prompt, got:\n%s", view))
	}
})

var _ = ginkgo.It("input history navigation resizes rendered input", func() {
	wrappedPrompt := strings.Repeat("wrapped ", 12)
	m := &Model{
		events:       make(chan tea.Msg, 4),
		input:        textarea.New(),
		inputHistory: []string{wrappedPrompt},
	}
	m.inputHistoryIndex = -1
	m.width = 32
	m.height = 18
	m.resize()

	{
		got := m.input.Height()
		gomega.Expect(got).To(gomega.Equal(1), fmt.Sprintf("initial input height = %d, want 1", got))
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got, ok := updated.(*Model)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("Update returned %T, want *Model", updated))

	gomega.Expect(got.input.Height() <= 1).To(gomega.BeFalse(), fmt.Sprintf("history recall should resize wrapped input, height=%d", got.input.Height()))

	{
		view := got.View()
		gomega.Expect(strings.Contains(view, "wrapped")).To(gomega.BeTrue(), fmt.Sprintf("rendered view should include wrapped recalled prompt, got:\n%s", view))
	}
})

var _ = ginkgo.It("input history navigation continues past slash suggestions", func() {
	m := &Model{
		events:       make(chan tea.Msg, 4),
		input:        textarea.New(),
		inputHistory: []string{"older prompt", "/pla"},
	}
	m.inputHistoryIndex = -1
	m.width = 80
	m.height = 24
	m.resize()

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("first up should recall slash-like history entry"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("/pla"), fmt.Sprintf("first up recalled %q, want /pla", got))
	}

	gomega.Expect(m.slashSuggestions.visible).To(gomega.BeTrue(), fmt.Sprint("slash suggestions should update for recalled slash-like entry"))

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("second up should continue input history navigation"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("older prompt"), fmt.Sprintf("second up recalled %q, want older prompt", got))
	}
})

var _ = ginkgo.It("closed panel focus does not block input history navigation", func() {
	m := &Model{
		events:            make(chan tea.Msg, 4),
		input:             textarea.New(),
		inputHistory:      []string{"prior prompt"},
		inputHistoryIndex: -1,
	}
	m.history.focused = true
	m.history.panelOpen = false

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("closed panel focus should not block input history navigation"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("prior prompt"), fmt.Sprintf("input history should update the input, got %q", got))
	}
})

var _ = ginkgo.It("closing already closed panel clears stale focus", func() {
	m := &Model{ready: true}
	m.history.focused = true
	m.history.panelOpen = false

	m.setHistoryPanelOpen(false)

	gomega.Expect(m.history.focused).To(gomega.BeFalse(), fmt.Sprint("closed panel must not retain focus"))
})

var _ = ginkgo.It("input history does not override focused panels or slash suggestions", func() {
	m := &Model{
		events:            make(chan tea.Msg, 4),
		input:             textarea.New(),
		inputHistory:      []string{"prior prompt"},
		inputHistoryIndex: -1,
		slashSuggestions: slashSuggestionsState{
			visible:  true,
			selected: 1,
			matches:  []slashSuggestionEntry{{command: "/one"}, {command: "/two"}},
		},
	}
	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("slash suggestion up arrow should be handled"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("slash suggestion navigation should not alter input history value, got %q", got))
	}

	gomega.Expect(m.slashSuggestions.selected).To(gomega.Equal(0), fmt.Sprintf("slash suggestion selection should move, got %d", m.slashSuggestions.selected))

	m.slashSuggestions = slashSuggestionsState{}
	m.history.panelOpen = true
	m.history.focused = true
	m.history.entries = []historyEntry{{sender: "user"}, {sender: "agent"}}
	m.history.selected = 1

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("focused history panel up arrow should be handled"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("focused panel navigation should not alter input history value, got %q", got))
	}

	gomega.Expect(m.history.selected).To(gomega.Equal(0), fmt.Sprintf("history panel selection should move, got %d", m.history.selected))
})

var _ = ginkgo.It("input history records submissions and resets on manual edit", func() {
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  textarea.New(),
	}
	m.recordInputHistory("repeat")
	m.recordInputHistory("repeat")
	m.recordInputHistory("next")

	{
		got := len(m.inputHistory)
		gomega.Expect(got).To(gomega.Equal(2), fmt.Sprintf("duplicate submissions should collapse, got %d entries", got))
	}

	{
		got := m.inputHistory[1]
		gomega.Expect(got).To(gomega.Equal("next"), fmt.Sprintf("newest history entry = %q, want next", got))
	}

	m.recordInputHistory("repeat")

	{
		got := strings.Join(m.inputHistory, "|")
		gomega.Expect(got).To(gomega.Equal("next|repeat"), fmt.Sprintf("re-submitted duplicate should move to newest slot, got %q", got))
	}

	m.inputHistoryIndex = 1
	m.inputHistoryDraft = "draft"

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		gomega.Expect(handled).To(gomega.BeFalse(), fmt.Sprint("manual edit should pass through to textarea handling"))
	}

	gomega.Expect(m.inputHistoryIndex).To(gomega.Equal(-1), fmt.Sprintf("manual edit should reset history navigation, index=%d draft=%q", m.inputHistoryIndex, m.inputHistoryDraft))
	gomega.Expect(m.inputHistoryDraft).To(gomega.Equal(""), fmt.Sprintf("manual edit should reset history navigation, index=%d draft=%q", m.inputHistoryIndex, m.inputHistoryDraft))
})

var _ = ginkgo.It("leaked terminal mouse reports do not reach textarea", func() {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events: make(chan tea.Msg, 8),
		input:  in,
		width:  120,
		height: 35,
	}

	for _, leaked := range []string{
		"[<64;120;35M",
		"[<65;120;35m",
		"[<64;120;35M[<65;120;35m",
		"[64;120;35M",
		"[65;120;35M",
		"[<64;",
		"[<64;1",
		"[<64;120;3",
		"[64;120;",
		"[M`!!",
		string([]rune{'[', 'M', '`', 128, '!'}),
	} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(leaked)})
		m = updated.(*Model)

		{
			got := m.input.Value()
			gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("leaked mouse report %q reached textarea, got %q", leaked, got))
		}
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("normal input")})
	m = updated.(*Model)

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("normal input"), fmt.Sprintf("normal text should still reach textarea, got %q", got))
	}
})

var _ = ginkgo.It("leaked mouse report in mixed key runes is stripped", func() {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  in,
		width:  160,
		height: 40,
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[65;149;34Mreal text")})
	m = updated.(*Model)

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("real text"), fmt.Sprintf("mixed mouse report chunk should preserve only real text, got %q", got))
	}
})

var _ = ginkgo.It("leaked terminal mouse reports do not reset input history navigation", func() {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events:            make(chan tea.Msg, 4),
		input:             in,
		inputHistoryIndex: 1,
		inputHistoryDraft: "draft",
		width:             120,
		height:            35,
	}

	for _, leaked := range []string{"[<64;120;35M", "[64;120;35M", "[<64;", "[<64;1", "[<64;120;3", "[M`!!"} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(leaked)})
		m = updated.(*Model)

		gomega.Expect(m.inputHistoryIndex).To(gomega.Equal(1), fmt.Sprintf("leaked mouse report %q should not reset history navigation, index=%d draft=%q", leaked, m.inputHistoryIndex, m.inputHistoryDraft))
		gomega.Expect(m.inputHistoryDraft).To(gomega.Equal("draft"), fmt.Sprintf("leaked mouse report %q should not reset history navigation, index=%d draft=%q", leaked, m.inputHistoryIndex, m.inputHistoryDraft))
	}
})

var _ = ginkgo.It("mouse report filter does not drop bracketed text", func() {
	for _, text := range []string{"[Mary", "[<", "[<3", "[<64"} {
		in := textarea.New()
		in.Focus()
		m := &Model{
			events: make(chan tea.Msg, 4),
			input:  in,
			width:  120,
			height: 35,
		}

		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
		m = updated.(*Model)

		{
			got := m.input.Value()
			gomega.Expect(got).To(gomega.Equal(text), fmt.Sprintf("ordinary bracketed text %q should remain editable, got %q", text, got))
		}
	}
})

var _ = ginkgo.It("mouse shaped bracketed text resets input history navigation", func() {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events:            make(chan tea.Msg, 4),
		input:             in,
		inputHistoryIndex: 1,
		inputHistoryDraft: "draft",
		width:             120,
		height:            35,
	}

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<3")})
		gomega.Expect(handled).To(gomega.BeFalse(), fmt.Sprint("ordinary bracketed text should pass through to textarea handling"))
	}

	gomega.Expect(m.inputHistoryIndex).To(gomega.Equal(-1), fmt.Sprintf("ordinary bracketed text should reset history navigation, index=%d draft=%q", m.inputHistoryIndex, m.inputHistoryDraft))
	gomega.Expect(m.inputHistoryDraft).To(gomega.Equal(""), fmt.Sprintf("ordinary bracketed text should reset history navigation, index=%d draft=%q", m.inputHistoryIndex, m.inputHistoryDraft))
})

var _ = ginkgo.It("out of bounds mouse shaped text still reaches textarea", func() {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  in,
		width:  80,
		height: 24,
	}

	text := "[<64;999;999M"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	m = updated.(*Model)

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal(text), fmt.Sprintf("out-of-bounds mouse-shaped text should remain editable, got %q", got))
	}

	text = "[65;999;999M"
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	m = updated.(*Model)

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal("[<64;999;999M"+text), fmt.Sprintf("out-of-bounds urxvt mouse-shaped text should remain editable, got %q", got))
	}
})

var _ = ginkgo.It("pasted mouse report text still reaches textarea", func() {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  in,
	}

	pasted := "[<64;120;35M"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted), Paste: true})
	m = updated.(*Model)

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal(pasted), fmt.Sprintf("pasted mouse-shaped text should remain editable, got %q", got))
	}
})

var _ = ginkgo.It("enter submits long feature planner command without textarea residue", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	command := "/feature-planner FET 3.3c brewery cistern atom. " +
		"Use native/src/simulation/machines/machine.cpp and native/src/simulation/economy/fluid_network_system.h. " +
		"Run cd native/tests && scons -j10 && ./bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"

	client := &scriptedLLMRecorder{responses: []string{`{"tool":"ask_planner_question","question_summary":"Need source facts","question_kind":"external_answer_required","blocking_claim":"Need accepted source evidence.","requested_decision":"Provide accepted source facts.","authority_required":"accepted_source","required_before":"plan_approval","affected_fields":["source_localization"]}`}}
	m := &Model{
		cfg:         config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		client:      client.Mock(t),
		events:      make(chan tea.Msg, 8),
		input:       textarea.New(),
		plannerSpec: config.LlamaServer{Model: "mock-model"},
	}
	m.input.SetValue(command)
	m.width = 100
	m.height = 24
	m.resize()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got, ok := updated.(*Model)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("Update returned %T, want *Model", updated))

	{
		value := got.input.Value()
		gomega.Expect(value).To(gomega.Equal(""), fmt.Sprintf("submitted Enter must not leave textarea residue, got %q", value))
	}

	gomega.Expect(len(got.inputHistory)).To(gomega.Equal(1), fmt.Sprintf("input history did not preserve exact command:\n got %#v\nwant %q", got.inputHistory, command))
	gomega.Expect(got.inputHistory[0]).To(gomega.Equal(command), fmt.Sprintf("input history did not preserve exact command:\n got %#v\nwant %q", got.inputHistory, command))

	gomega.Expect(strings.Contains(got.inputHistory[0], "-sf=test_fluid_brewery_cascade.cpp")).To(gomega.BeTrue(), fmt.Sprintf("test filter flag was not preserved: %q", got.inputHistory[0]))

	got = waitForPromptWorkerResult(ctx, got)

	gomega.Expect(len(client.messages)).To(gomega.Equal(1), fmt.Sprintf("expected one planner request, got %#v", client.messages))
	gomega.Expect(len(client.messages[0])).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("expected one planner request, got %#v", client.messages))
	plannerPrompt := client.messages[0][len(client.messages[0])-1].Content
	gomega.Expect(strings.Contains(plannerPrompt, "User request:\n"+strings.TrimPrefix(command, "/feature-planner "))).To(gomega.BeTrue(), fmt.Sprintf("planner prompt lost exact objective:\n%s", plannerPrompt))
	gomega.Expect(strings.Contains(plannerPrompt, "-sf=test_fluid_brewery_cascade.cpp")).To(gomega.BeTrue(), fmt.Sprintf("planner prompt lost test filter flag:\n%s", plannerPrompt))
})

var _ = ginkgo.It("enter submits full slash command when suggestions are stale", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	command := "/feature-planner implement atom with exact source facts"
	client := &scriptedLLMRecorder{responses: []string{`{"tool":"ask_planner_question","question_summary":"Need source facts","question_kind":"external_answer_required","blocking_claim":"Need accepted source evidence.","requested_decision":"Provide accepted source facts.","authority_required":"accepted_source","required_before":"plan_approval","affected_fields":["source_localization"]}`}}
	m := &Model{
		cfg:    config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		client: client.Mock(t),
		events: make(chan tea.Msg, 8),
		input:  textarea.New(),
		slashSuggestions: slashSuggestionsState{
			visible:  true,
			matches:  []slashSuggestionEntry{{command: "/feature-planner", usage: "<objective>"}},
			selected: 0,
		},
		plannerSpec: config.LlamaServer{Model: "mock-model"},
	}
	m.input.SetValue(command)
	m.width = 100
	m.height = 24
	m.resize()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(*Model)

	{
		value := got.input.Value()
		gomega.Expect(value).To(gomega.Equal(""), fmt.Sprintf("stale slash suggestions should yield to submit, got input %q", value))
	}
	gomega.Expect(len(got.inputHistory)).To(gomega.Equal(1), fmt.Sprintf("input history = %#v, want %q", got.inputHistory, command))
	gomega.Expect(got.inputHistory[0]).To(gomega.Equal(command), fmt.Sprintf("input history = %#v, want %q", got.inputHistory, command))

	waitForPromptWorkerResult(ctx, got)
})

var _ = ginkgo.It("input history caps at fifty most recent unique entries", func() {
	m := &Model{}
	for i := 0; i < maxInputHistoryEntries+5; i++ {
		m.recordInputHistory(fmt.Sprintf("prompt-%02d", i))
	}

	{
		got := len(m.inputHistory)
		gomega.Expect(got).To(gomega.Equal(maxInputHistoryEntries), fmt.Sprintf("history length = %d, want %d", got, maxInputHistoryEntries))
	}

	{
		got := m.inputHistory[0]
		gomega.Expect(got).To(gomega.Equal("prompt-05"), fmt.Sprintf("oldest retained prompt = %q, want prompt-05", got))
	}

	{
		got := m.inputHistory[len(m.inputHistory)-1]
		gomega.Expect(got).To(gomega.Equal("prompt-54"), fmt.Sprintf("newest retained prompt = %q, want prompt-54", got))
	}
})

var _ = ginkgo.It("input history loads from recorded sessions oldest to newest deduped", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}
	writeSessionLog(t, dir, "20260418-055543.504.jsonl", []string{
		`{"type":"input_submission","content":"older prompt"}`,
		`{"type":"message","role":"assistant","content":"ignored"}`,
		`{"type":"input_submission","content":"repeat me"}`,
	})
	writeSessionLog(t, dir, "20260419-100000.000.jsonl", []string{
		`{"type":"input_submission","content":"newer prompt"}`,
		`{"type":"input_submission","content":"repeat me"}`,
	})
	writeSessionLog(t, dir, "notes.txt", []string{
		`{"type":"message","role":"user","content":"not a session"}`,
	})

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("inputHistoryFromRecordedSessions: %v", err))

	want := []string{"older prompt", "newer prompt", "repeat me"}
	gomega.Expect(strings.Join(got, "\n")).To(gomega.Equal(strings.Join(want, "\n")), fmt.Sprintf("history = %#v, want %#v", got, want))
})

var _ = ginkgo.It("input history from recorded sessions ignores message only logs", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

	writeSessionLog(t, dir, "20200101-000000.000.jsonl", []string{
		`{"type":"message","role":"user","content":"ancient synthetic prompt"}`,
	})
	writeSessionLog(t, dir, "20260612-100000.000.jsonl", []string{
		`{"type":"input_submission","content":"recent submitted prompt"}`,
		`{"type":"message","role":"user","content":"normalized dispatch prompt"}`,
	})
	writeSessionLog(t, dir, "zz-test-process.jsonl", []string{
		`{"type":"message","role":"user","content":"lexically newest synthetic prompt"}`,
	})

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("inputHistoryFromRecordedSessions: %v", err))

	want := []string{"recent submitted prompt"}
	gomega.Expect(strings.Join(got, "\n")).To(gomega.Equal(strings.Join(want, "\n")), fmt.Sprintf("history = %#v, want %#v", got, want))
})

var _ = ginkgo.It("input history from recorded sessions skips redacted replay placeholders", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

	writeSessionLog(t, dir, "20260612-100000.000.jsonl", []string{
		`{"type":"input_submission","content":"<redacted 42 chars>"}`,
		`{"type":"message","role":"user","content":"<redacted 30 chars>"}`,
		`{"type":"input_submission","content":"/feature-planner exact replayable command"}`,
	})

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("inputHistoryFromRecordedSessions: %v", err))

	want := []string{"/feature-planner exact replayable command"}
	gomega.Expect(strings.Join(got, "\n")).To(gomega.Equal(strings.Join(want, "\n")), fmt.Sprintf("history = %#v, want %#v", got, want))
})

var _ = ginkgo.It("load history from session seeds input history", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "session.jsonl", []string{
		`{"type":"message","role":"user","content":"first prompt"}`,
		`{"type":"message","role":"assistant","content":"first reply"}`,
		`{"type":"message","role":"user","content":"second prompt"}`,
	})

	model := &Model{}
	{
		err := model.LoadHistoryFromSession(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	}

	{
		got := strings.Join(model.inputHistory, "|")
		gomega.Expect(got).To(gomega.Equal("first prompt|second prompt"), fmt.Sprintf("input history = %q", got))
	}
})

var _ = ginkgo.It("load history from session prefers submitted input history", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "session.jsonl", []string{
		`{"type":"input_submission","content":"/plan-execute fix the up arrow history"}`,
		`{"type":"message","role":"user","content":"fix the up arrow history"}`,
		`{"type":"message","role":"assistant","content":"done"}`,
	})

	model := &Model{}
	{
		err := model.LoadHistoryFromSession(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	}

	want := "/plan-execute fix the up arrow history"
	{
		got := strings.Join(model.inputHistory, "|")
		gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("input history = %q, want %q", got, want))
	}
})

var _ = ginkgo.It("input history loads submitted input before lane normalized user messages", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  dir,
		Mode: runprofile.DebugLogModeFull,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("NewDebugLogger: %v", err))

	model := &Model{debugLogger: logger}
	model.recordInputSubmission("/plan-execute fix the up arrow history")

	{
		err := logger.LogMessage("user", "fix the up arrow history")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LogMessage: %v", err))
	}

	{
		err := logger.Close()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Close: %v", err))
	}

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("inputHistoryFromRecordedSessions: %v", err))

	want := []string{"/plan-execute fix the up arrow history"}
	gomega.Expect(strings.Join(got, "\n")).To(gomega.Equal(strings.Join(want, "\n")), fmt.Sprintf("history = %#v, want %#v", got, want))
})

var _ = ginkgo.It("input history from recorded sessions missing dir", func() {
	t := ginkgo.GinkgoT()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: filepath.Join(t.TempDir(), "missing")}}

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("missing dir should not error: %v", err))

	gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("expected empty history for missing dir, got %#v", got))
})
