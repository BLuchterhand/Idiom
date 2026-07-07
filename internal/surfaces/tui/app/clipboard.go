package app

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"idiom/internal/platform/hosttools"
	"idiom/internal/runtime/llama"
)

type clipboardCommandSpec struct {
	name string
	args []string
}

func (m *Model) enableMouseSelectionMode() tea.Cmd {
	m.mouseSelectionMode = true
	m.status = "Selection mode on. Drag to select terminal text; Esc or Ctrl+O restores mouse."
	return tea.DisableMouse
}

func (m *Model) copyConversationTarget(rawTarget string) (tea.Cmd, bool) {
	target, content, err := m.copyConversationContent(rawTarget)
	if err != nil {
		m.status = err.Error()
		return nil, true
	}

	cmd, err := clipboardCopyCmd(target, content)
	if err != nil {
		m.status = "Copy failed: " + err.Error()
		return nil, true
	}

	m.status = "Copying " + target + " to clipboard..."
	return cmd, true
}

func (m *Model) copyConversationContent(rawTarget string) (string, string, error) {
	target := strings.TrimSpace(strings.ToLower(rawTarget))
	if target == "" {
		target = "last"
	}

	switch target {
	case "last", "assistant", "response":
		content, ok := lastMessageContent(m.messages, "assistant")
		if !ok {
			return "", "", fmt.Errorf("No assistant response to copy.")
		}

		return "last assistant response", content, nil
	case "all", "transcript":
		content := formatConversationTranscript(m.messages)
		if content == "" {
			return "", "", fmt.Errorf("No conversation transcript to copy.")
		}

		return "conversation transcript", content, nil
	case "visible", "view", "window":
		content := cleanVisibleViewportText(m.viewport.View())
		if strings.TrimSpace(content) == "" {
			return "", "", fmt.Errorf("No visible conversation text to copy.")
		}

		return "visible conversation window", content, nil
	case "selected", "history":
		content, ok := m.selectedHistoryContent()
		if !ok {
			return "", "", fmt.Errorf("No selected history entry to copy.")
		}

		return "selected history entry", content, nil
	default:
		return "", "", fmt.Errorf("Usage: /copy [last|all|visible|selected]")
	}
}

func clipboardCopyCmd(target, content string) (tea.Cmd, error) {
	spec, err := clipboardCommandForRuntime(runtime.GOOS)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(spec.name, spec.args...)
	cmd.Stdin = strings.NewReader(content)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return clipboardCopyDoneMsg{
			target: target,
			bytes:  len([]byte(content)),
			err:    err,
		}
	}), nil
}

func clipboardCommandForRuntime(goos string) (clipboardCommandSpec, error) {
	switch goos {
	case "darwin":
		return clipboardCommandFromCandidates([]clipboardCommandSpec{{name: "pbcopy"}})
	case "linux", "freebsd", "openbsd", "netbsd":
		return clipboardCommandFromCandidates([]clipboardCommandSpec{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		})
	case "windows":
		return clipboardCommandFromCandidates([]clipboardCommandSpec{{name: "clip.exe"}})
	default:
		return clipboardCommandSpec{}, fmt.Errorf("clipboard copy is unsupported on %s", goos)
	}
}

func clipboardCommandFromCandidates(candidates []clipboardCommandSpec) (clipboardCommandSpec, error) {
	var tried []string

	for _, candidate := range candidates {
		tried = append(tried, candidate.name)

		path := hosttools.FirstAvailable(candidate.name)
		if path != "" {
			candidate.name = path
			return candidate, nil
		}
	}

	return clipboardCommandSpec{}, fmt.Errorf("no clipboard command found (%s)", strings.Join(tried, ", "))
}

func cleanVisibleViewportText(view string) string {
	lines := strings.Split(ansi.Strip(view), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n")
}

func lastMessageContent(messages []llama.ChatMessage, role string) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != role {
			continue
		}

		content := strings.TrimSpace(msg.Content)
		if content != "" {
			return content, true
		}
	}

	return "", false
}

func formatConversationTranscript(messages []llama.ChatMessage) string {
	var b strings.Builder

	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}

		if b.Len() > 0 {
			b.WriteString("\n\n")
		}

		b.WriteString(copyRoleLabel(msg.Role))
		b.WriteString(":\n")
		b.WriteString(content)
	}

	return strings.TrimSpace(b.String())
}

func copyRoleLabel(role string) string {
	switch role {
	case "user":
		return "You"
	case "assistant":
		return "Assistant"
	case "tool":
		return "Tool"
	case "operator":
		return "Operator"
	default:
		return titleDisplay(role)
	}
}

func (m *Model) selectedHistoryContent() (string, bool) {
	if m == nil || !m.history.panelOpen || !m.history.focused || m.history.selected < 0 || m.history.selected >= len(m.history.entries) {
		return "", false
	}

	entry := m.history.entries[m.history.selected]
	content := strings.TrimSpace(strings.Join(entry.contents, "\n\n"))
	if content == "" {
		return "", false
	}

	return content, true
}
