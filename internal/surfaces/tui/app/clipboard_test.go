package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/runtime/llama"
)

var _ = ginkgo.It("copy conversation content last assistant", func() {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "follow-up"},
			{Role: "assistant", Content: "  "},
		},
	}

	target, content, err := m.copyConversationContent("last")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("copyConversationContent returned error: %v", err))

	gomega.Expect(target).To(gomega.Equal("last assistant response"), fmt.Sprintf("target/content = %q/%q, want last assistant response/first answer", target, content))
	gomega.Expect(content).To(gomega.Equal("first answer"), fmt.Sprintf("target/content = %q/%q, want last assistant response/first answer", target, content))
})

var _ = ginkgo.It("copy conversation content transcript formats messages", func() {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: "answer"},
			{Role: "tool", Content: ""},
		},
	}

	_, content, err := m.copyConversationContent("all")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("copyConversationContent returned error: %v", err))

	want := "You:\nquestion\n\nAssistant:\nanswer"
	gomega.Expect(content).To(gomega.Equal(want), fmt.Sprintf("transcript content = %q, want %q", content, want))
})

var _ = ginkgo.It("copy conversation content visible strips ansi", func() {
	vp := viewport.New(40, 10)
	vp.SetContent("\x1b[31mred\x1b[0m\nplain")

	m := &Model{viewport: vp}
	_, content, err := m.copyConversationContent("visible")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("copyConversationContent returned error: %v", err))

	gomega.Expect(content).To(gomega.Not(gomega.ContainSubstring("\x1b")), fmt.Sprintf("visible content = %q, want ANSI-free red/plain text", content))
	gomega.Expect(content).To(gomega.Equal("red\nplain"), fmt.Sprintf("visible content = %q, want ANSI-free red/plain text", content))
})

var _ = ginkgo.It("copy conversation content selected history requires focused panel", func() {
	m := &Model{
		history: historyState{
			panelOpen: true,
			focused:   true,
			selected:  1,
			entries: []historyEntry{
				{contents: []string{"first"}},
				{contents: []string{"second", "third"}},
			},
		},
	}

	_, content, err := m.copyConversationContent("selected")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("copyConversationContent returned error: %v", err))

	gomega.Expect(content).To(gomega.Equal("second\n\nthird"), fmt.Sprintf("selected content = %q, want joined selected entry", content))

	m.history.focused = false
	{
		_, _, err := m.copyConversationContent("selected")
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("selected copy should require a focused history panel"))
	}
})

var _ = ginkgo.It("clipboard command for runtime uses platform command", func() {
	t := ginkgo.GinkgoT()
	if runtime.GOOS == "windows" {
		ginkgo.Skip(fmt.Sprint("executable fixture uses POSIX mode bits"))
	}

	binDir := t.TempDir()
	pbcopy := filepath.Join(binDir, "pbcopy")
	writeClipboardExecutable(t, pbcopy)
	t.Setenv("PATH", binDir)

	spec, err := clipboardCommandForRuntime("darwin")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("clipboardCommandForRuntime returned error: %v", err))

	gomega.Expect(spec.name).To(gomega.Equal(pbcopy), fmt.Sprintf("spec = %#v, want pbcopy without args", spec))
	gomega.Expect(len(spec.args)).To(gomega.Equal(0), fmt.Sprintf("spec = %#v, want pbcopy without args", spec))
})

var _ = ginkgo.It("clipboard command for runtime falls back on linux", func() {
	t := ginkgo.GinkgoT()
	if runtime.GOOS == "windows" {
		ginkgo.Skip(fmt.Sprint("executable fixture uses POSIX mode bits"))
	}

	binDir := t.TempDir()
	xclip := filepath.Join(binDir, "xclip")
	writeClipboardExecutable(t, xclip)
	t.Setenv("PATH", binDir)

	spec, err := clipboardCommandForRuntime("linux")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("clipboardCommandForRuntime returned error: %v", err))

	gomega.Expect(spec.name).To(gomega.Equal(xclip), fmt.Sprintf("spec = %#v, want xclip clipboard command", spec))
	gomega.Expect(strings.Join(spec.args, " ")).To(gomega.Equal("-selection clipboard"), fmt.Sprintf("spec = %#v, want xclip clipboard command", spec))
})

func writeClipboardExecutable(t ginkgo.FullGinkgoTInterface, path string) {
	ginkgo.GinkgoHelper()

	{
		err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write executable %s: %v", path, err))
	}
}

var _ = ginkgo.It("selection mode toggle disables and restores mouse", func() {
	m := &Model{}

	cmd := m.enableMouseSelectionMode()
	gomega.Expect(cmd).To(gomega.Not(gomega.BeNil()), fmt.Sprint("enableMouseSelectionMode should enter selection mode and return a command"))
	gomega.Expect(m.mouseSelectionMode).To(gomega.BeTrue(), fmt.Sprint("enableMouseSelectionMode should enter selection mode and return a command"))

	cmd, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("Esc should leave selection mode and return a mouse-enable command"))
	gomega.Expect(cmd).To(gomega.Not(gomega.BeNil()), fmt.Sprint("Esc should leave selection mode and return a mouse-enable command"))

	gomega.Expect(m.mouseSelectionMode).To(gomega.BeFalse(), fmt.Sprint("selection mode should be off after Esc"))
})
