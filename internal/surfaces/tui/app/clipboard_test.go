package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/runtime/llama"
)

func TestCopyConversationContentLastAssistant(t *testing.T) {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "follow-up"},
			{Role: "assistant", Content: "  "},
		},
	}

	target, content, err := m.copyConversationContent("last")
	if err != nil {
		t.Fatalf("copyConversationContent returned error: %v", err)
	}

	if target != "last assistant response" || content != "first answer" {
		t.Fatalf("target/content = %q/%q, want last assistant response/first answer", target, content)
	}
}

func TestCopyConversationContentTranscriptFormatsMessages(t *testing.T) {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: "answer"},
			{Role: "tool", Content: ""},
		},
	}

	_, content, err := m.copyConversationContent("all")
	if err != nil {
		t.Fatalf("copyConversationContent returned error: %v", err)
	}

	want := "You:\nquestion\n\nAssistant:\nanswer"
	if content != want {
		t.Fatalf("transcript content = %q, want %q", content, want)
	}
}

func TestCopyConversationContentVisibleStripsANSI(t *testing.T) {
	vp := viewport.New(40, 10)
	vp.SetContent("\x1b[31mred\x1b[0m\nplain")

	m := &Model{viewport: vp}
	_, content, err := m.copyConversationContent("visible")
	if err != nil {
		t.Fatalf("copyConversationContent returned error: %v", err)
	}

	if strings.Contains(content, "\x1b") || content != "red\nplain" {
		t.Fatalf("visible content = %q, want ANSI-free red/plain text", content)
	}
}

func TestCopyConversationContentSelectedHistoryRequiresFocusedPanel(t *testing.T) {
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
	if err != nil {
		t.Fatalf("copyConversationContent returned error: %v", err)
	}

	if content != "second\n\nthird" {
		t.Fatalf("selected content = %q, want joined selected entry", content)
	}

	m.history.focused = false
	if _, _, err := m.copyConversationContent("selected"); err == nil {
		t.Fatal("selected copy should require a focused history panel")
	}
}

func TestClipboardCommandForRuntimeUsesPlatformCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses POSIX mode bits")
	}

	binDir := t.TempDir()
	pbcopy := filepath.Join(binDir, "pbcopy")
	writeClipboardExecutable(t, pbcopy)
	t.Setenv("PATH", binDir)

	spec, err := clipboardCommandForRuntime("darwin")
	if err != nil {
		t.Fatalf("clipboardCommandForRuntime returned error: %v", err)
	}

	if spec.name != pbcopy || len(spec.args) != 0 {
		t.Fatalf("spec = %#v, want pbcopy without args", spec)
	}
}

func TestClipboardCommandForRuntimeFallsBackOnLinux(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses POSIX mode bits")
	}

	binDir := t.TempDir()
	xclip := filepath.Join(binDir, "xclip")
	writeClipboardExecutable(t, xclip)
	t.Setenv("PATH", binDir)

	spec, err := clipboardCommandForRuntime("linux")
	if err != nil {
		t.Fatalf("clipboardCommandForRuntime returned error: %v", err)
	}

	if spec.name != xclip || strings.Join(spec.args, " ") != "-selection clipboard" {
		t.Fatalf("spec = %#v, want xclip clipboard command", spec)
	}
}

func writeClipboardExecutable(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func TestSelectionModeToggleDisablesAndRestoresMouse(t *testing.T) {
	m := &Model{}

	cmd := m.enableMouseSelectionMode()
	if cmd == nil || !m.mouseSelectionMode {
		t.Fatal("enableMouseSelectionMode should enter selection mode and return a command")
	}

	cmd, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled || cmd == nil {
		t.Fatal("Esc should leave selection mode and return a mouse-enable command")
	}

	if m.mouseSelectionMode {
		t.Fatal("selection mode should be off after Esc")
	}
}
