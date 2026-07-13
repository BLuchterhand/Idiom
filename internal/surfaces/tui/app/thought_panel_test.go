package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("Thought panel behavior", ginkgo.Label("integration", "contract"), func() {
	ginkgo.It("renders model reasoning without replaying it into the transcript", func() {
		t := ginkgo.GinkgoT()
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

		client := &scriptedLLMRecorder{
			responses:          []string{"Visible answer."},
			reasoningResponses: []string{"I am checking how the thought panel renders."},
		}

		model := New(cfg)
		defer model.Close()

		model.client = client.Mock(t)
		model.ready = true
		model.streaming = true
		model.events = make(chan tea.Msg, 64)
		model.viewport = viewport.New(80, 20)
		model.input = textarea.New()
		model.spinner = spinner.New()

		answer, err := model.RunPromptOnce(context.Background(), "show the thought panel")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal("Visible answer."))
		gomega.Expect(drainThoughtPanelMessages(model)).To(gomega.BeTrue(), "expected RunPromptOnce to queue a thoughtMsg")

		model.renderMessages(true)
		view := model.viewport.View()
		gomega.Expect(view).To(gomega.And(
			gomega.ContainSubstring("thoughts · 1"),
			gomega.ContainSubstring("thought panel"),
			gomega.ContainSubstring("renders."),
		))
		gomega.Expect(model.messages).To(gomega.HaveLen(2), "want user and assistant only")

		for _, msg := range model.messages {
			gomega.Expect(msg.ReasoningContent).To(gomega.BeEmpty(), "reasoning must not be replayed through transcript messages")
			gomega.Expect(msg.Content).NotTo(gomega.ContainSubstring("thought panel renders"))
		}

		payload := newDetachedPromptExecutionSession(context.Background(), model).requestMessages(context.Background())
		for _, msg := range payload {
			gomega.Expect(msg.ReasoningContent).To(gomega.BeEmpty(), "reasoning must not be replayed through request payload")
			gomega.Expect(msg.Content).NotTo(gomega.ContainSubstring("thought panel renders"))
		}
	})

	ginkgo.It("emits one thought per model call across tool turns", func() {
		t := ginkgo.GinkgoT()
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

		client := &scriptedLLMRecorder{
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

		model.client = client.Mock(t)
		model.ready = true
		model.streaming = true
		model.events = make(chan tea.Msg, 64)
		model.viewport = viewport.New(80, 20)
		model.input = textarea.New()
		model.spinner = spinner.New()

		_, err := model.RunPromptOnce(context.Background(), "list then answer")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		drainThoughtPanelMessages(model)

		gomega.Expect(model.thoughts.entries).To(gomega.HaveLen(2), "want exactly two unique model-call thoughts")
		gomega.Expect([]string{model.thoughts.entries[0].text, model.thoughts.entries[1].text}).To(gomega.Equal([]string{
			"I need to list files first.",
			"I can now answer.",
		}))
	})

	ginkgo.It("keeps the current thought feed during a suppressed prompt run", func() {
		t := ginkgo.GinkgoT()
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

		client := &scriptedLLMRecorder{
			responses:          []string{"Nested visible answer."},
			reasoningResponses: []string{"Nested plan-execute phase thought."},
		}

		model := New(cfg)
		defer model.Close()

		model.client = client.Mock(t)
		model.ready = true
		model.streaming = true
		model.suppressPromptTranscript = true
		model.events = make(chan tea.Msg, 64)
		model.viewport = viewport.New(80, 20)
		model.input = textarea.New()
		model.spinner = spinner.New()
		model.thoughts.append(1, "Planner phase thought from the same user turn.")

		answer, err := model.RunPromptOnce(context.Background(), "suppressed executor phase")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal("Nested visible answer."))
		gomega.Expect(drainThoughtPanelMessages(model)).To(gomega.BeTrue(), "expected suppressed prompt run to queue a thoughtMsg")
		gomega.Expect(model.thoughts.entries).To(gomega.HaveLen(2), "want existing plus nested thought")
		gomega.Expect([]string{model.thoughts.entries[0].text, model.thoughts.entries[1].text}).To(gomega.Equal([]string{
			"Planner phase thought from the same user turn.",
			"Nested plan-execute phase thought.",
		}))
	})

	ginkgo.It("follows the newest segment of a long reasoning reply", func() {
		t := ginkgo.GinkgoT()
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

		client := &scriptedLLMRecorder{
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

		model.client = client.Mock(t)
		model.ready = true
		model.streaming = true
		model.events = make(chan tea.Msg, 64)
		model.viewport = viewport.New(52, 14)
		model.input = textarea.New()
		model.spinner = spinner.New()

		_, err := model.RunPromptOnce(context.Background(), "show all thought segments")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(drainThoughtPanelMessages(model)).To(gomega.BeTrue(), "expected RunPromptOnce to queue thought segments")
		gomega.Expect(model.thoughts.entries).To(gomega.HaveLen(9), "want all nine reasoning segments")

		model.renderMessages(true)
		view := model.viewport.View()
		gomega.Expect(view).To(gomega.ContainSubstring("ninth reasoning"))
		gomega.Expect(view).To(gomega.ContainSubstring("segment"))
		gomega.Expect(view).NotTo(gomega.ContainSubstring("first reasoning"), "the live viewport should follow the newest thought instead of staying at the feed header")
	})
})

func drainThoughtPanelMessages(model *Model) bool {
	found := false

	for {
		select {
		case msg := <-model.events:
			if _, ok := msg.(thoughtMsg); ok {
				_, _ = model.Update(msg)
				found = true
			}
		default:
			return found
		}
	}
}

func writeThoughtPanelFixtureFile(t ginkgo.FullGinkgoTInterface, root string) {
	ginkgo.GinkgoHelper()

	{
		err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Thought panel fixture\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write fixture README: %v", err))
	}
}
