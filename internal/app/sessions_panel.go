package app

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type sessionSummaryCacheEntry struct {
	modTime time.Time
	size    int64
	info    SessionInfo
}

// refreshSessionsList rescans the debug-log directory and caches the
// result on the model. Called when entering the Sessions tab and from
// `/resume`, so the list reflects sessions landed by the current
// process (or any sibling process) since the last view.
func (m *Model) refreshSessionsList() {
	start := time.Now()
	defer func() {
		m.logUISlowPath("ui.refresh_sessions", start, fmt.Sprintf("tab=%d entries=%d", m.panelTab, len(m.sessions.list)))
	}()

	dir := strings.TrimSpace(m.cfg.Chat.DebugLogDir)
	if dir == "" {
		dir = "logs/conversations"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			m.sessions.list = nil
			m.sessions.rawCache = nil
			m.sessions.rawCacheDir = dir
			m.sessions.rawCacheMTime = time.Time{}
			m.sessions.rawCacheValid = true
			m.sessions.summaryCache = nil
			return
		}
		m.sessions.list = nil
		return
	}
	if m.sessions.summaryCache == nil {
		m.sessions.summaryCache = make(map[string]sessionSummaryCacheEntry, len(entries))
	}
	nextCache := make(map[string]sessionSummaryCacheEntry, len(entries))
	sessions := make([]SessionInfo, 0, len(entries))
	var dirMTime time.Time
	if info, err := os.Stat(dir); err == nil {
		dirMTime = info.ModTime()
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		path := filepath.Join(dir, name)
		id := strings.TrimSuffix(name, ".jsonl")
		info, err := entry.Info()
		if err != nil {
			continue
		}
		size := info.Size()
		modTime := info.ModTime()

		session := SessionInfo{ID: id, Path: path}
		if cached, ok := m.sessions.summaryCache[path]; ok && cached.size == size && cached.modTime.Equal(modTime) {
			session = cached.info
		} else {
			session = summarizeSessionLog(path)
			session.ID = id
			session.Path = path
		}
		nextCache[path] = sessionSummaryCacheEntry{
			modTime: modTime,
			size:    size,
			info:    session,
		}
		sessions = append(sessions, session)
	}
	slices.SortFunc(sessions, func(a, b SessionInfo) int {
		return -cmp.Compare(a.ID, b.ID)
	})
	m.sessions.summaryCache = nextCache
	m.sessions.rawCache = append(m.sessions.rawCache[:0], sessions...)
	m.sessions.rawCacheDir = dir
	m.sessions.rawCacheMTime = dirMTime
	m.sessions.rawCacheValid = true
	// Filter out the live session's own log: resuming into yourself
	// adds nothing and invites confusion. Empty debugLogPath (pre-init
	// race) falls through harmlessly.
	currentID := m.sessionID()
	filtered := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		if s.ID == currentID {
			continue
		}
		filtered = append(filtered, s)
	}
	m.sessions.list = filtered
	if m.sessions.cursor < 0 {
		m.sessions.cursor = 0
	}
	if m.sessions.cursor >= len(m.sessions.list) {
		m.sessions.cursor = max(0, len(m.sessions.list)-1)
	}
}

// renderSessionsPanelContent rebuilds the Sessions viewport. Each row
// is a two-line entry — id + a shortened one-line first-prompt summary
// so the user can recognize a session without hunting through log
// files. When the panel has focus the cursor row is highlighted.
func (m *Model) renderSessionsPanelContent() {
	if !m.history.panelOpen {
		return
	}
	w := max(8, m.sessions.viewport.Width)
	selectedStart := -1
	selectedEnd := -1
	line := 0

	var b strings.Builder
	title := "◇ Sessions"
	if m.history.focused && m.panelTab == panelTabSessions {
		title = "◇ Sessions · ↑/↓ select · Enter resume"
	}
	b.WriteString(panelTitleStyle.Width(w).Render(title))
	b.WriteString("\n")
	line++

	if len(m.sessions.list) == 0 {
		msg := "No prior sessions found."
		for _, wl := range wrapLines(msg, max(1, w-2)) {
			b.WriteString(panelDetailStyle.Width(w).Render(wl))
			b.WriteString("\n")
		}
		m.sessions.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
		m.sessions.viewport.SetYOffset(0)
		return
	}

	for i, s := range m.sessions.list {
		summary := strings.TrimSpace(s.FirstPrompt)
		if summary == "" {
			summary = "(no user prompt recorded)"
		}
		msgCount := fmt.Sprintf("%d msg", s.UserMessages+s.AssistMessage)
		header := fmt.Sprintf("%s · %s", s.ID, msgCount)
		body := "  " + summary
		focused := i == m.sessions.cursor && m.history.focused && m.panelTab == panelTabSessions
		rowStart := line

		if focused {
			b.WriteString(panelFocusedStyle.Width(w).Render("▸ " + header))
		} else {
			b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("  " + header))
		}
		b.WriteString("\n")
		line++
		wrappedBody := wrapLines(body, max(1, w-2))
		for _, wl := range wrappedBody {
			if focused {
				b.WriteString(panelFocusedStyle.Width(w).Render(wl))
			} else {
				b.WriteString(panelDetailStyle.Width(w).Render(wl))
			}
			b.WriteString("\n")
		}
		line += len(wrappedBody)
		if i == m.sessions.cursor {
			selectedStart = rowStart
			selectedEnd = max(rowStart, line-1)
		}
	}

	m.sessions.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
	m.ensureSessionsCursorVisible(selectedStart, selectedEnd)
}

func (m *Model) ensureSessionsCursorVisible(start, end int) {
	if start < 0 || end < start {
		return
	}
	height := max(1, m.sessions.viewport.Height)
	top := m.sessions.viewport.YOffset
	bottom := top + height - 1
	switch {
	case start < top:
		m.sessions.viewport.SetYOffset(start)
	case end > bottom:
		m.sessions.viewport.SetYOffset(end - height + 1)
	}
}

// handleSessionsPanelKey consumes a key press while the Sessions tab
// has focus. Returns (true, cmd) if handled. j/k navigate, Enter
// resumes the selected session.
func (m *Model) handleSessionsPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Drop focus and re-render so the title's "↑/↓ select" hint
		// disappears, signalling the panel is no longer interactive.
		m.history.focused = false
		m.renderSessionsPanelContent()
		return true, m.waitForEventCmd()
	case tea.KeyUp:
		if m.sessions.cursor > 0 {
			m.sessions.cursor--
		}
		m.renderSessionsPanelContent()
		return true, m.waitForEventCmd()
	case tea.KeyDown:
		if m.sessions.cursor < len(m.sessions.list)-1 {
			m.sessions.cursor++
		}
		m.renderSessionsPanelContent()
		return true, m.waitForEventCmd()
	case tea.KeyEnter:
		if len(m.sessions.list) == 0 {
			return true, m.waitForEventCmd()
		}
		if m.sessions.cursor < 0 || m.sessions.cursor >= len(m.sessions.list) {
			return true, m.waitForEventCmd()
		}
		id := m.sessions.list[m.sessions.cursor].ID
		if err := m.ResumeSession(id); err != nil {
			m.status = "Resume failed: " + err.Error()
		} else {
			m.status = "Resumed session " + id + "."
			// Bounce the panel back to History so the resumed chat
			// (now populating the main viewport) is the focal surface.
			m.panelTab = panelTabHistory
			m.rerenderActivePanel()
		}
		return true, m.waitForEventCmd()
	}
	return false, nil
}
