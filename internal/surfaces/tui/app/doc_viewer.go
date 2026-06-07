package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/capabilities/workspace"
)

func (m *Model) currentPlanArtifactPath() string {
	root := ""
	if m != nil && m.workspace != nil {
		root = strings.TrimSpace(m.workspace.Root())
	}
	if root == "" && m != nil {
		root = strings.TrimSpace(m.cfg.Workspace.Root)
	}
	if root == "" {
		root = "."
	}
	return filepath.Join(root, ".idiom", "plans", "last.plan.json")
}

func (m *Model) loadDocViewerArtifact() (workspace.PlanArtifact, string, error) {
	path := m.currentPlanArtifactPath()
	info, err := os.Stat(path)
	if err != nil {
		m.docViewer.artifactPath = path
		m.docViewer.artifactMTime = time.Time{}
		m.docViewer.artifact = workspace.PlanArtifact{}
		m.docViewer.loaded = false
		if os.IsNotExist(err) {
			m.docViewer.loadError = ""
			return workspace.PlanArtifact{}, path, nil
		}
		m.docViewer.loadError = err.Error()
		return workspace.PlanArtifact{}, path, err
	}
	if m.docViewer.loaded && m.docViewer.artifactPath == path && info.ModTime().Equal(m.docViewer.artifactMTime) {
		return m.docViewer.artifact, path, nil
	}
	artifact, err := workspace.LoadPlanArtifact(path)
	if err != nil {
		m.docViewer.artifactPath = path
		m.docViewer.artifactMTime = info.ModTime()
		m.docViewer.artifact = workspace.PlanArtifact{}
		m.docViewer.loaded = false
		m.docViewer.loadError = err.Error()
		return workspace.PlanArtifact{}, path, err
	}
	m.docViewer.artifactPath = path
	m.docViewer.artifactMTime = info.ModTime()
	m.docViewer.artifact = artifact
	m.docViewer.loadError = ""
	m.docViewer.loaded = true
	if m.docViewer.expanded == nil {
		m.docViewer.expanded = make(map[int]bool)
	}
	if m.docViewer.cursor >= len(artifact.Edits) {
		m.docViewer.cursor = max(0, len(artifact.Edits)-1)
	}
	return artifact, path, nil
}

func (m *Model) renderDocViewerPanelContent() {
	if !m.history.panelOpen {
		return
	}
	w := max(8, m.docViewer.viewport.Width)
	var b strings.Builder
	title := "◇ Doc Viewer"
	if m.history.focused && m.panelTab == panelTabDocViewer {
		title = "◇ Doc Viewer · ↑/↓ select · Enter expand"
	}
	b.WriteString(panelTitleStyle.Width(w).Render(title))
	b.WriteString("\n")

	artifact, path, err := m.loadDocViewerArtifact()
	if err != nil {
		m.renderDocViewerEmpty(&b, w, path, "Load error: "+err.Error())
		m.docViewer.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
		return
	}
	if !m.docViewer.loaded {
		m.renderDocViewerEmpty(&b, w, path, "")
		m.docViewer.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
		return
	}

	m.renderDocViewerMetadata(&b, w, artifact, path)
	b.WriteString("\n")
	m.renderDocViewerEdits(&b, w, artifact.Edits)
	m.docViewer.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m *Model) renderDocViewerEmpty(b *strings.Builder, w int, path string, detail string) {
	lines := []string{
		"No plan-execute artifact yet.",
		"",
		"Expected:",
		"  " + m.displayPath(path),
	}
	if strings.TrimSpace(detail) != "" {
		lines = append(lines, "", detail)
	}
	for _, line := range lines {
		writeDocViewerWrappedLine(b, w, panelDetailStyle, line)
	}
}

func (m *Model) renderDocViewerMetadata(b *strings.Builder, w int, artifact workspace.PlanArtifact, path string) {
	rows := []string{
		"Plan: " + firstNonEmptyDocValue(artifact.PlanID, "(no plan id)"),
		"Schema: " + firstNonEmptyDocValue(artifact.SchemaVersion, "(unknown)"),
		"Created: " + firstNonEmptyDocValue(artifact.Created, "(unknown)"),
		"Session: " + firstNonEmptyDocValue(artifact.SessionID, "(none)"),
		"Contract: " + firstNonEmptyDocValue(artifact.Contract.Kind, "(none)") +
			" · permission " + firstNonEmptyDocValue(artifact.Contract.PermissionProfile, "(none)"),
		"Approval: " + firstNonEmptyDocValue(artifact.Approval.Status, "(none)") +
			" · " + firstNonEmptyDocValue(artifact.Approval.ExecutionPermission, "(none)"),
		"Handoff: " + firstNonEmptyDocValue(artifact.Handoff.Status, "(none)") +
			" · ready " + fmt.Sprint(artifact.Handoff.Ready),
		"Pack: " + firstNonEmptyDocValue(artifact.PackID, firstNonEmptyDocValue(artifact.Packet.PackID, "(none)")),
		"Path: " + m.displayPath(path),
	}
	for _, row := range rows {
		writeDocViewerWrappedLine(b, w, panelDetailStyle, row)
	}
	if rationale := strings.TrimSpace(artifact.Rationale); rationale != "" {
		writeDocViewerWrappedLine(b, w, panelDetailStyle, "Rationale: "+rationale)
	}
}

func (m *Model) renderDocViewerEdits(b *strings.Builder, w int, edits []workspace.PlanEdit) {
	if len(edits) == 0 {
		writeDocViewerWrappedLine(b, w, panelDetailStyle, "No edits in artifact.")
		return
	}
	if m.docViewer.cursor < 0 {
		m.docViewer.cursor = 0
	}
	if m.docViewer.cursor >= len(edits) {
		m.docViewer.cursor = len(edits) - 1
	}
	for i, edit := range edits {
		expanded := m.docViewer.expanded != nil && m.docViewer.expanded[i]
		prefix := "▸"
		if expanded {
			prefix = "▾"
		}
		line := fmt.Sprintf("%s Edit %d · %s · %s", prefix, i+1, editShapeLabel(edit), firstNonEmptyDocValue(edit.Path, "(no path)"))
		if purpose := strings.TrimSpace(edit.Purpose); purpose != "" {
			line += " · " + purpose
		}
		style := panelEntryStyle
		if m.history.focused && m.panelTab == panelTabDocViewer && i == m.docViewer.cursor {
			style = panelFocusedStyle
		}
		b.WriteString(style.Width(w).Render(line))
		b.WriteString("\n")
		if expanded {
			diff := renderColoredDiff(planEditUnifiedDiff(edit))
			for _, line := range strings.Split(diff, "\n") {
				writeDocViewerWrappedLine(b, w, panelDetailStyle, line)
			}
		}
	}
}

func (m *Model) handleDocViewerPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if m.docViewer.cursor > 0 {
			m.docViewer.cursor--
			m.renderDocViewerPanelContent()
		}
		return true, m.waitForEventCmd()
	case tea.KeyDown:
		artifact, _, _ := m.loadDocViewerArtifact()
		if m.docViewer.cursor < len(artifact.Edits)-1 {
			m.docViewer.cursor++
			m.renderDocViewerPanelContent()
		}
		return true, m.waitForEventCmd()
	case tea.KeyEnter:
		artifact, _, _ := m.loadDocViewerArtifact()
		if len(artifact.Edits) > 0 {
			if m.docViewer.expanded == nil {
				m.docViewer.expanded = make(map[int]bool)
			}
			m.docViewer.expanded[m.docViewer.cursor] = !m.docViewer.expanded[m.docViewer.cursor]
			m.renderDocViewerPanelContent()
		}
		return true, m.waitForEventCmd()
	case tea.KeyEsc:
		m.history.focused = false
		m.renderDocViewerPanelContent()
		return true, m.waitForEventCmd()
	case tea.KeyPgUp, tea.KeyPgDown:
		m.docViewer.viewport, _ = m.docViewer.viewport.Update(msg)
		return true, m.waitForEventCmd()
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "j":
			artifact, _, _ := m.loadDocViewerArtifact()
			if m.docViewer.cursor < len(artifact.Edits)-1 {
				m.docViewer.cursor++
				m.renderDocViewerPanelContent()
			}
			return true, m.waitForEventCmd()
		case "k":
			if m.docViewer.cursor > 0 {
				m.docViewer.cursor--
				m.renderDocViewerPanelContent()
			}
			return true, m.waitForEventCmd()
		}
	}
	return false, nil
}

func planEditUnifiedDiff(edit workspace.PlanEdit) string {
	var b strings.Builder
	path := firstNonEmptyDocValue(edit.Path, "unknown")
	b.WriteString("--- a/" + path + "\n")
	b.WriteString("+++ b/" + path + "\n")
	switch {
	case edit.ReplaceAll:
		b.WriteString("@@ replace all @@\n")
		writeDiffLines(&b, "+", edit.NewText)
	case edit.StartLine > 0 && edit.EndLine >= edit.StartLine:
		b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", edit.StartLine, edit.EndLine-edit.StartLine+1, edit.StartLine, len(splitDiffBody(edit.Content))))
		writeDiffLines(&b, "+", edit.Content)
	case edit.InsertAfterLine > 0:
		b.WriteString(fmt.Sprintf("@@ after line %d @@\n", edit.InsertAfterLine))
		writeDiffLines(&b, "+", edit.Content)
	case strings.TrimSpace(edit.OldText) != "":
		b.WriteString("@@ old_text @@\n")
		writeDiffLines(&b, "-", edit.OldText)
		writeDiffLines(&b, "+", edit.NewText)
	default:
		b.WriteString("@@ edit body unavailable @@\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeDiffLines(b *strings.Builder, prefix string, body string) {
	lines := splitDiffBody(body)
	if len(lines) == 0 {
		if prefix == "+" {
			b.WriteString("+\n")
		}
		return
	}
	for _, line := range lines {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteString("\n")
	}
}

func splitDiffBody(body string) []string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

func editShapeLabel(edit workspace.PlanEdit) string {
	switch {
	case edit.ReplaceAll:
		return "replace_all"
	case edit.InsertAfterLine > 0:
		return "insert_after_line"
	case edit.StartLine > 0 && edit.EndLine >= edit.StartLine:
		return "line_range"
	case strings.TrimSpace(edit.OldText) != "":
		return "old_text"
	default:
		return "edit"
	}
}

func writeDocViewerWrappedLine(b *strings.Builder, w int, style lipgloss.Style, line string) {
	for _, wl := range wrapLines(line, max(1, w-2)) {
		b.WriteString(style.Width(w).Render(wl))
		b.WriteString("\n")
	}
}

func firstNonEmptyDocValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
