package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	thoughtPanelMaxEntries = 12
	thoughtPanelMaxChars   = 8000
	thoughtPanelMaxRows    = 8
)

type thoughtPanelEntry struct {
	step int
	text string
}

type thoughtPanelState struct {
	entries   []thoughtPanelEntry
	expanded  bool
	scrollOff int
}

func (p *thoughtPanelState) reset() {
	if p == nil {
		return
	}

	p.entries = nil
	p.expanded = false
	p.scrollOff = 0
}

func (p *thoughtPanelState) visible() bool {
	return p != nil && len(p.entries) > 0
}

func (p *thoughtPanelState) append(step int, text string) {
	if p == nil {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if p.contains(step, text) {
		return
	}

	wasAtBottom := p.atBottom()

	p.entries = append(p.entries, thoughtPanelEntry{step: step, text: text})
	if len(p.entries) > thoughtPanelMaxEntries {
		p.entries = append([]thoughtPanelEntry(nil), p.entries[len(p.entries)-thoughtPanelMaxEntries:]...)
	}

	for p.totalChars() > thoughtPanelMaxChars && len(p.entries) > 1 {
		p.entries = append([]thoughtPanelEntry(nil), p.entries[1:]...)
	}

	if p.totalChars() > thoughtPanelMaxChars && len(p.entries) == 1 {
		p.entries[0].text = truncateRunes(p.entries[0].text, thoughtPanelMaxChars)
	}

	p.expanded = true
	if wasAtBottom {
		p.scrollToBottom()
	} else {
		p.clampScroll()
	}
}

func (p *thoughtPanelState) contains(step int, text string) bool {
	if p == nil {
		return false
	}

	for _, entry := range p.entries {
		if entry.step == step && entry.text == text {
			return true
		}
	}

	return false
}

func (p *thoughtPanelState) totalChars() int {
	if p == nil {
		return 0
	}

	total := 0
	for _, entry := range p.entries {
		total += len(entry.text)
	}

	return total
}

func (p *thoughtPanelState) scroll(delta int) bool {
	if p == nil || !p.expanded {
		return false
	}

	before := p.scrollOff
	p.scrollOff += delta
	p.clampScroll()

	return p.scrollOff != before
}

func (p *thoughtPanelState) atBottom() bool {
	if p == nil {
		return true
	}

	return p.scrollOff >= p.maxScrollOff()
}

func (p *thoughtPanelState) scrollToBottom() {
	if p == nil {
		return
	}

	p.scrollOff = p.maxScrollOff()
}

func (p *thoughtPanelState) clampScroll() {
	if p == nil {
		return
	}

	if p.scrollOff < 0 {
		p.scrollOff = 0
	}

	if maxOff := p.maxScrollOff(); p.scrollOff > maxOff {
		p.scrollOff = maxOff
	}
}

func (p *thoughtPanelState) maxScrollOff() int {
	if p == nil {
		return 0
	}

	rows := allThoughtPanelRows(p.entries)
	if len(rows) <= thoughtPanelMaxRows {
		return 0
	}

	return len(rows) - thoughtPanelMaxRows
}

func renderThoughtPanel(p thoughtPanelState, width int) string {
	if len(p.entries) == 0 {
		return ""
	}

	indicator := "▸"
	if p.expanded {
		indicator = "▾"
	}

	title := fmt.Sprintf("%s thoughts · %d", indicator, len(p.entries))
	if !p.expanded {
		return thoughtPanelHeaderStyle.Width(max(1, width)).Render(title)
	}

	rows := thoughtPanelRows(p.entries, thoughtPanelMaxRows, p.scrollOff)
	body := strings.Join(rows, "\n")
	if body == "" {
		return thoughtPanelHeaderStyle.Width(max(1, width)).Render(title)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		thoughtPanelHeaderStyle.Width(max(1, width)).Render(title),
		thoughtPanelBodyStyle.Width(max(1, width)).Render(body),
	)
}

func allThoughtPanelRows(entries []thoughtPanelEntry) []string {
	var rows []string
	for _, entry := range entries {
		text := strings.TrimSpace(entry.text)
		if text == "" {
			continue
		}

		prefix := fmt.Sprintf("[%d] ", entry.step)
		if entry.step <= 0 {
			prefix = ""
		}

		for _, line := range strings.Split(text, "\n") {
			line = collapseWhitespace(line)
			if line == "" {
				continue
			}

			rows = append(rows, prefix+line)
			prefix = "    "
		}
	}

	return rows
}

func thoughtPanelRows(entries []thoughtPanelEntry, maxRows int, scrollOff int) []string {
	if maxRows <= 0 {
		return nil
	}

	rows := allThoughtPanelRows(entries)
	if len(rows) <= maxRows {
		return rows
	}

	if scrollOff < 0 {
		scrollOff = 0
	}

	maxOff := len(rows) - maxRows
	if scrollOff > maxOff {
		scrollOff = maxOff
	}

	end := scrollOff + maxRows
	window := append([]string(nil), rows[scrollOff:end]...)
	if scrollOff > 0 {
		window[0] = fmt.Sprintf("%d earlier thought line(s) hidden", scrollOff)
	}

	if end < len(rows) {
		window[len(window)-1] = fmt.Sprintf("%d later thought line(s) hidden", len(rows)-end)
	}

	return window
}
