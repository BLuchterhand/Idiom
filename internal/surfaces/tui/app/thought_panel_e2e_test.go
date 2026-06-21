package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
)

func TestThoughtPanelE2ERunPromptOnceRendersModelReasoningWithoutTranscriptReplay(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 1
	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.MCP = nil
	writeThoughtPanelFixtureFile(t, cfg.Workspace.Root)

	client := &scriptedLLMClient{
		responses:          []string{"Visible answer."},
		reasoningResponses: []string{"I am checking how the thought panel renders."},
	}

	model := New(cfg)
	defer model.Close()

	model.client = client
	model.runner.Client = client
	model.ready = true
	model.streaming = true
	model.events = make(chan tea.Msg, 64)
	model.viewport = viewport.New(80, 20)
	model.input = textarea.New()
	model.spinner = spinner.New()

	answer, err := model.RunPromptOnce(context.Background(), "show the thought panel")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}

	if answer != "Visible answer." {
		t.Fatalf("answer = %q", answer)
	}

	if !drainThoughtPanelMessages(model) {
		t.Fatal("expected RunPromptOnce to queue a thoughtMsg")
	}

	model.renderMessages(true)
	view := model.viewport.View()
	if !strings.Contains(view, "thoughts · 1") || !strings.Contains(view, "thought panel renders") {
		t.Fatalf("expected thought panel to render queued reasoning, got:\n%s", view)
	}

	if len(model.messages) != 2 {
		t.Fatalf("messages = %#v, want user and assistant only", model.messages)
	}

	for _, msg := range model.messages {
		if msg.ReasoningContent != "" || strings.Contains(msg.Content, "thought panel renders") {
			t.Fatalf("reasoning must not be replayed through transcript messages: %#v", model.messages)
		}
	}

	payload := model.requestMessages()
	for _, msg := range payload {
		if msg.ReasoningContent != "" || strings.Contains(msg.Content, "thought panel renders") {
			t.Fatalf("reasoning must not be replayed through request payload: %#v", payload)
		}
	}
}

func TestThoughtPanelE2EDoesNotEmitDuplicateThoughtsAcrossToolTurns(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 2
	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.Workspace.EnableWrites = false
	cfg.MCP = nil
	writeThoughtPanelFixtureFile(t, cfg.Workspace.Root)

	client := &scriptedLLMClient{
		responses: []string{
			`{"tool":"list_files","path":"."}`,
			"Visible answer after tool.",
		},
		reasoningResponses: []string{
			"I need to list files first.",
			"I can now answer.",
		},
	}

	model := New(cfg)
	defer model.Close()

	model.client = client
	model.runner.Client = client
	model.ready = true
	model.streaming = true
	model.events = make(chan tea.Msg, 64)
	model.viewport = viewport.New(80, 20)
	model.input = textarea.New()
	model.spinner = spinner.New()

	if _, err := model.RunPromptOnce(context.Background(), "list then answer"); err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}

	drainThoughtPanelMessages(model)

	if len(model.thoughts.entries) != 2 {
		t.Fatalf("thought entries = %#v, want exactly two unique model-call thoughts", model.thoughts.entries)
	}

	if model.thoughts.entries[0].text != "I need to list files first." || model.thoughts.entries[1].text != "I can now answer." {
		t.Fatalf("unexpected thought entries: %#v", model.thoughts.entries)
	}
}

func TestThoughtPanelE2ESuppressedPromptRunKeepsCurrentTurnThoughtFeed(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 1
	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.MCP = nil
	writeThoughtPanelFixtureFile(t, cfg.Workspace.Root)

	client := &scriptedLLMClient{
		responses:          []string{"Nested visible answer."},
		reasoningResponses: []string{"Nested plan-execute phase thought."},
	}

	model := New(cfg)
	defer model.Close()

	model.client = client
	model.runner.Client = client
	model.ready = true
	model.streaming = true
	model.suppressPromptTranscript = true
	model.events = make(chan tea.Msg, 64)
	model.viewport = viewport.New(80, 20)
	model.input = textarea.New()
	model.spinner = spinner.New()
	model.thoughts.append(1, "Planner phase thought from the same user turn.")

	answer, err := model.RunPromptOnce(context.Background(), "suppressed executor phase")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}

	if answer != "Nested visible answer." {
		t.Fatalf("answer = %q", answer)
	}

	if !drainThoughtPanelMessages(model) {
		t.Fatal("expected suppressed prompt run to queue a thoughtMsg")
	}

	if len(model.thoughts.entries) != 2 {
		t.Fatalf("thought entries = %#v, want existing plus nested thought", model.thoughts.entries)
	}

	if model.thoughts.entries[0].text != "Planner phase thought from the same user turn." ||
		model.thoughts.entries[1].text != "Nested plan-execute phase thought." {
		t.Fatalf("unexpected thought feed after suppressed prompt: %#v", model.thoughts.entries)
	}
}

func TestThoughtPanelE2ERendersNewestSegmentFromLongReasoningReply(t *testing.T) {
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 1
	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.MCP = nil
	writeThoughtPanelFixtureFile(t, cfg.Workspace.Root)

	client := &scriptedLLMClient{
		responses: []string{"Visible answer."},
		reasoningResponses: []string{strings.Join([]string{
			"first reasoning segment",
			"second reasoning segment",
			"third reasoning segment",
			"fourth reasoning segment",
			"fifth reasoning segment",
			"sixth reasoning segment",
			"seventh reasoning segment",
			"eighth reasoning segment",
			"ninth reasoning segment",
		}, "\n\n")},
	}

	model := New(cfg)
	defer model.Close()

	model.client = client
	model.runner.Client = client
	model.ready = true
	model.streaming = true
	model.events = make(chan tea.Msg, 64)
	model.viewport = viewport.New(52, 14)
	model.input = textarea.New()
	model.spinner = spinner.New()

	if _, err := model.RunPromptOnce(context.Background(), "show all thought segments"); err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}

	if !drainThoughtPanelMessages(model) {
		t.Fatal("expected RunPromptOnce to queue thought segments")
	}

	if len(model.thoughts.entries) != 9 {
		t.Fatalf("thought entries = %#v, want all nine reasoning segments", model.thoughts.entries)
	}

	model.renderMessages(true)
	view := model.viewport.View()
	if !strings.Contains(view, "thoughts · 9") || !strings.Contains(view, "ninth reasoning segment") {
		t.Fatalf("thought panel should follow newest reasoning segment, got:\n%s", view)
	}
}

var _ llama.LLMClient = (*scriptedLLMClient)(nil)

func drainThoughtPanelMessages(model *Model) bool {
	found := false

	for {
		select {
		case msg := <-model.events:
			if thought, ok := msg.(thoughtMsg); ok {
				model.handleThought(thought)
				found = true
			}
		default:
			return found
		}
	}
}

func writeThoughtPanelFixtureFile(t *testing.T, root string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Thought panel fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture README: %v", err)
	}
}
