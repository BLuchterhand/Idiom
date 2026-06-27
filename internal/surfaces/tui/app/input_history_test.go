package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/evidence/transcript"
	"idiom/internal/platform/config"
)

func TestInputHistoryNavigationUsesSubmittedPrompts(t *testing.T) {
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  textarea.New(),
	}
	m.recordInputHistory("first prompt")
	m.recordInputHistory("second prompt")

	m.input.SetValue("draft prompt")

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("expected up arrow to navigate input history")
	}

	if got := m.input.Value(); got != "second prompt" {
		t.Fatalf("first up should show newest prompt, got %q", got)
	}

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("expected second up arrow to navigate input history")
	}

	if got := m.input.Value(); got != "first prompt" {
		t.Fatalf("second up should show older prompt, got %q", got)
	}

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("expected down arrow to navigate input history")
	}

	if got := m.input.Value(); got != "second prompt" {
		t.Fatalf("down should move toward newer prompt, got %q", got)
	}

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("expected down arrow to restore draft after newest prompt")
	}

	if got := m.input.Value(); got != "draft prompt" {
		t.Fatalf("down past newest should restore draft, got %q", got)
	}
}

func TestInputHistoryNavigationUpdatesRenderedInputThroughModelUpdate(t *testing.T) {
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
	if !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}

	if value := got.input.Value(); value != "second prompt" {
		t.Fatalf("up arrow should set input value while streaming, got %q", value)
	}

	if view := got.View(); !strings.Contains(view, "second prompt") {
		t.Fatalf("rendered view should include recalled prompt, got:\n%s", view)
	}
}

func TestInputHistoryNavigationResizesRenderedInput(t *testing.T) {
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

	if got := m.input.Height(); got != 1 {
		t.Fatalf("initial input height = %d, want 1", got)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}

	if got.input.Height() <= 1 {
		t.Fatalf("history recall should resize wrapped input, height=%d", got.input.Height())
	}

	if view := got.View(); !strings.Contains(view, "wrapped") {
		t.Fatalf("rendered view should include wrapped recalled prompt, got:\n%s", view)
	}
}

func TestInputHistoryNavigationContinuesPastSlashSuggestions(t *testing.T) {
	m := &Model{
		events:       make(chan tea.Msg, 4),
		input:        textarea.New(),
		inputHistory: []string{"older prompt", "/pla"},
	}
	m.inputHistoryIndex = -1
	m.width = 80
	m.height = 24
	m.resize()

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("first up should recall slash-like history entry")
	}

	if got := m.input.Value(); got != "/pla" {
		t.Fatalf("first up recalled %q, want /pla", got)
	}

	if !m.slashSuggestions.visible {
		t.Fatal("slash suggestions should update for recalled slash-like entry")
	}

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("second up should continue input history navigation")
	}

	if got := m.input.Value(); got != "older prompt" {
		t.Fatalf("second up recalled %q, want older prompt", got)
	}
}

func TestClosedPanelFocusDoesNotBlockInputHistoryNavigation(t *testing.T) {
	m := &Model{
		events:            make(chan tea.Msg, 4),
		input:             textarea.New(),
		inputHistory:      []string{"prior prompt"},
		inputHistoryIndex: -1,
	}
	m.history.focused = true
	m.history.panelOpen = false

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("closed panel focus should not block input history navigation")
	}

	if got := m.input.Value(); got != "prior prompt" {
		t.Fatalf("input history should update the input, got %q", got)
	}
}

func TestClosingAlreadyClosedPanelClearsStaleFocus(t *testing.T) {
	m := &Model{ready: true}
	m.history.focused = true
	m.history.panelOpen = false

	m.setHistoryPanelOpen(false)

	if m.history.focused {
		t.Fatal("closed panel must not retain focus")
	}
}

func TestInputHistoryDoesNotOverrideFocusedPanelsOrSlashSuggestions(t *testing.T) {
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
	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("slash suggestion up arrow should be handled")
	}

	if got := m.input.Value(); got != "" {
		t.Fatalf("slash suggestion navigation should not alter input history value, got %q", got)
	}

	if m.slashSuggestions.selected != 0 {
		t.Fatalf("slash suggestion selection should move, got %d", m.slashSuggestions.selected)
	}

	m.slashSuggestions = slashSuggestionsState{}
	m.history.panelOpen = true
	m.history.focused = true
	m.history.entries = []historyEntry{{sender: "user"}, {sender: "agent"}}
	m.history.selected = 1

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("focused history panel up arrow should be handled")
	}

	if got := m.input.Value(); got != "" {
		t.Fatalf("focused panel navigation should not alter input history value, got %q", got)
	}

	if m.history.selected != 0 {
		t.Fatalf("history panel selection should move, got %d", m.history.selected)
	}
}

func TestInputHistoryRecordsSubmissionsAndResetsOnManualEdit(t *testing.T) {
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  textarea.New(),
	}
	m.recordInputHistory("repeat")
	m.recordInputHistory("repeat")
	m.recordInputHistory("next")

	if got := len(m.inputHistory); got != 2 {
		t.Fatalf("duplicate submissions should collapse, got %d entries", got)
	}

	if got := m.inputHistory[1]; got != "next" {
		t.Fatalf("newest history entry = %q, want next", got)
	}

	m.recordInputHistory("repeat")

	if got := strings.Join(m.inputHistory, "|"); got != "next|repeat" {
		t.Fatalf("re-submitted duplicate should move to newest slot, got %q", got)
	}

	m.inputHistoryIndex = 1
	m.inputHistoryDraft = "draft"

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); handled {
		t.Fatal("manual edit should pass through to textarea handling")
	}

	if m.inputHistoryIndex != -1 || m.inputHistoryDraft != "" {
		t.Fatalf("manual edit should reset history navigation, index=%d draft=%q", m.inputHistoryIndex, m.inputHistoryDraft)
	}
}

func TestLeakedTerminalMouseReportsDoNotReachTextarea(t *testing.T) {
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
		"[64;120;",
		"[M`!!",
		string([]rune{'[', 'M', '`', 128, '!'}),
	} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(leaked)})
		m = updated.(*Model)

		if got := m.input.Value(); got != "" {
			t.Fatalf("leaked mouse report %q reached textarea, got %q", leaked, got)
		}
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("normal input")})
	m = updated.(*Model)

	if got := m.input.Value(); got != "normal input" {
		t.Fatalf("normal text should still reach textarea, got %q", got)
	}
}

func TestLeakedMouseReportInMixedKeyRunesIsStripped(t *testing.T) {
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

	if got := m.input.Value(); got != "real text" {
		t.Fatalf("mixed mouse report chunk should preserve only real text, got %q", got)
	}
}

func TestLeakedTerminalMouseReportsDoNotResetInputHistoryNavigation(t *testing.T) {
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

	for _, leaked := range []string{"[<64;120;35M", "[64;120;35M", "[<64;", "[M`!!"} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(leaked)})
		m = updated.(*Model)

		if m.inputHistoryIndex != 1 || m.inputHistoryDraft != "draft" {
			t.Fatalf("leaked mouse report %q should not reset history navigation, index=%d draft=%q", leaked, m.inputHistoryIndex, m.inputHistoryDraft)
		}
	}
}

func TestMouseReportFilterDoesNotDropBracketedText(t *testing.T) {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  in,
		width:  120,
		height: 35,
	}

	text := "[Mary"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	m = updated.(*Model)

	if got := m.input.Value(); got != text {
		t.Fatalf("ordinary bracketed text should remain editable, got %q", got)
	}
}

func TestOutOfBoundsMouseShapedTextStillReachesTextarea(t *testing.T) {
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

	if got := m.input.Value(); got != text {
		t.Fatalf("out-of-bounds mouse-shaped text should remain editable, got %q", got)
	}

	text = "[65;999;999M"
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	m = updated.(*Model)

	if got := m.input.Value(); got != "[<64;999;999M"+text {
		t.Fatalf("out-of-bounds urxvt mouse-shaped text should remain editable, got %q", got)
	}
}

func TestPastedMouseReportTextStillReachesTextarea(t *testing.T) {
	in := textarea.New()
	in.Focus()
	m := &Model{
		events: make(chan tea.Msg, 4),
		input:  in,
	}

	pasted := "[<64;120;35M"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted), Paste: true})
	m = updated.(*Model)

	if got := m.input.Value(); got != pasted {
		t.Fatalf("pasted mouse-shaped text should remain editable, got %q", got)
	}
}

func TestEnterSubmitsLongFeaturePlannerCommandWithoutTextareaResidue(t *testing.T) {
	command := "/feature-planner FET 3.3c brewery cistern atom. " +
		"Use native/src/simulation/machines/machine.cpp and native/src/simulation/economy/fluid_network_system.h. " +
		"Run cd native/tests && scons -j10 && ./bin/colony_tests -sf=test_fluid_brewery_cascade.cpp"

	client := &scriptedLLMClient{responses: []string{`{"tool":"ask_planner_question","question_summary":"Need source facts","question_kind":"external_answer_required","blocking_claim":"Need accepted source evidence.","requested_decision":"Provide accepted source facts.","authority_required":"accepted_source","required_before":"plan_approval","affected_fields":["source_localization"]}`}}
	m := &Model{
		cfg:         config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		client:      client,
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
	if !ok {
		t.Fatalf("Update returned %T, want *Model", updated)
	}

	if value := got.input.Value(); value != "" {
		t.Fatalf("submitted Enter must not leave textarea residue, got %q", value)
	}

	if len(got.inputHistory) != 1 || got.inputHistory[0] != command {
		t.Fatalf("input history did not preserve exact command:\n got %#v\nwant %q", got.inputHistory, command)
	}

	if !strings.Contains(got.inputHistory[0], "-sf=test_fluid_brewery_cascade.cpp") {
		t.Fatalf("test filter flag was not preserved: %q", got.inputHistory[0])
	}

	select {
	case msg := <-got.events:
		if done, ok := msg.(streamDoneMsg); ok && done.err != nil {
			t.Fatalf("feature-planner command failed: %v", done.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for feature-planner command goroutine")
	}

	if len(client.messages) != 1 || len(client.messages[0]) == 0 {
		t.Fatalf("expected one planner request, got %#v", client.messages)
	}
	plannerPrompt := client.messages[0][len(client.messages[0])-1].Content
	if !strings.Contains(plannerPrompt, "User request:\n"+strings.TrimPrefix(command, "/feature-planner ")) {
		t.Fatalf("planner prompt lost exact objective:\n%s", plannerPrompt)
	}
	if !strings.Contains(plannerPrompt, "-sf=test_fluid_brewery_cascade.cpp") {
		t.Fatalf("planner prompt lost test filter flag:\n%s", plannerPrompt)
	}
}

func TestEnterSubmitsFullSlashCommandWhenSuggestionsAreStale(t *testing.T) {
	command := "/feature-planner implement atom with exact source facts"
	client := &scriptedLLMClient{responses: []string{`{"tool":"ask_planner_question","question_summary":"Need source facts","question_kind":"external_answer_required","blocking_claim":"Need accepted source evidence.","requested_decision":"Provide accepted source facts.","authority_required":"accepted_source","required_before":"plan_approval","affected_fields":["source_localization"]}`}}
	m := &Model{
		cfg:    config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		client: client,
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

	if value := got.input.Value(); value != "" {
		t.Fatalf("stale slash suggestions should yield to submit, got input %q", value)
	}
	if len(got.inputHistory) != 1 || got.inputHistory[0] != command {
		t.Fatalf("input history = %#v, want %q", got.inputHistory, command)
	}

	select {
	case msg := <-got.events:
		if done, ok := msg.(streamDoneMsg); ok && done.err != nil {
			t.Fatalf("feature-planner command failed: %v", done.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for feature-planner command goroutine")
	}
}

func TestInputHistoryCapsAtFiftyMostRecentUniqueEntries(t *testing.T) {
	m := &Model{}
	for i := 0; i < maxInputHistoryEntries+5; i++ {
		m.recordInputHistory(fmt.Sprintf("prompt-%02d", i))
	}

	if got := len(m.inputHistory); got != maxInputHistoryEntries {
		t.Fatalf("history length = %d, want %d", got, maxInputHistoryEntries)
	}

	if got := m.inputHistory[0]; got != "prompt-05" {
		t.Fatalf("oldest retained prompt = %q, want prompt-05", got)
	}

	if got := m.inputHistory[len(m.inputHistory)-1]; got != "prompt-54" {
		t.Fatalf("newest retained prompt = %q, want prompt-54", got)
	}
}

func TestInputHistoryLoadsFromRecordedSessionsOldestToNewestDeduped(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inputHistoryFromRecordedSessions: %v", err)
	}

	want := []string{"older prompt", "newer prompt", "repeat me"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestInputHistoryFromRecordedSessionsIgnoresMessageOnlyLogs(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inputHistoryFromRecordedSessions: %v", err)
	}

	want := []string{"recent submitted prompt"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestInputHistoryFromRecordedSessionsSkipsRedactedReplayPlaceholders(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

	writeSessionLog(t, dir, "20260612-100000.000.jsonl", []string{
		`{"type":"input_submission","content":"<redacted 42 chars>"}`,
		`{"type":"message","role":"user","content":"<redacted 30 chars>"}`,
		`{"type":"input_submission","content":"/feature-planner exact replayable command"}`,
	})

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	if err != nil {
		t.Fatalf("inputHistoryFromRecordedSessions: %v", err)
	}

	want := []string{"/feature-planner exact replayable command"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestLoadHistoryFromSessionSeedsInputHistory(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "session.jsonl", []string{
		`{"type":"message","role":"user","content":"first prompt"}`,
		`{"type":"message","role":"assistant","content":"first reply"}`,
		`{"type":"message","role":"user","content":"second prompt"}`,
	})

	model := &Model{}
	if err := model.LoadHistoryFromSession(path); err != nil {
		t.Fatalf("LoadHistoryFromSession: %v", err)
	}

	if got := strings.Join(model.inputHistory, "|"); got != "first prompt|second prompt" {
		t.Fatalf("input history = %q", got)
	}
}

func TestLoadHistoryFromSessionPrefersSubmittedInputHistory(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "session.jsonl", []string{
		`{"type":"input_submission","content":"/plan-execute fix the up arrow history"}`,
		`{"type":"message","role":"user","content":"fix the up arrow history"}`,
		`{"type":"message","role":"assistant","content":"done"}`,
	})

	model := &Model{}
	if err := model.LoadHistoryFromSession(path); err != nil {
		t.Fatalf("LoadHistoryFromSession: %v", err)
	}

	want := "/plan-execute fix the up arrow history"
	if got := strings.Join(model.inputHistory, "|"); got != want {
		t.Fatalf("input history = %q, want %q", got, want)
	}
}

func TestInputHistoryLoadsSubmittedInputBeforeLaneNormalizedUserMessages(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:  dir,
		Mode: transcript.DebugLogModeFull,
	})
	if err != nil {
		t.Fatalf("NewDebugLogger: %v", err)
	}

	model := &Model{debugLogger: logger}
	model.recordInputSubmission("/plan-execute fix the up arrow history")

	if err := logger.LogMessage("user", "fix the up arrow history"); err != nil {
		t.Fatalf("LogMessage: %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	if err != nil {
		t.Fatalf("inputHistoryFromRecordedSessions: %v", err)
	}

	want := []string{"/plan-execute fix the up arrow history"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("history = %#v, want %#v", got, want)
	}
}

func TestInputHistoryFromRecordedSessionsMissingDir(t *testing.T) {
	cfg := config.Config{Chat: config.Chat{DebugLogDir: filepath.Join(t.TempDir(), "missing")}}

	got, err := inputHistoryFromRecordedSessions(cfg, maxInputHistoryEntries)
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty history for missing dir, got %#v", got)
	}
}
