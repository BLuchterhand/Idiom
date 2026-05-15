package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		m.logUISlowPath("ui.refresh_sessions", start, fmt.Sprintf("tab=%d entries=%d", m.panelTab, len(m.sessionsList)))
	}()

	dir := strings.TrimSpace(m.cfg.Chat.DebugLogDir)
	if dir == "" {
		dir = "logs/conversations"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			m.sessionsList = nil
			m.sessionsRawCache = nil
			m.sessionsRawCacheDir = dir
			m.sessionsRawCacheMTime = time.Time{}
			m.sessionsRawCacheValid = true
			m.sessionsSummaryCache = nil
			return
		}
		m.sessionsList = nil
		return
	}
	if m.sessionsSummaryCache == nil {
		m.sessionsSummaryCache = make(map[string]sessionSummaryCacheEntry, len(entries))
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
		if cached, ok := m.sessionsSummaryCache[path]; ok && cached.size == size && cached.modTime.Equal(modTime) {
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
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID > sessions[j].ID
	})
	m.sessionsSummaryCache = nextCache
	m.sessionsRawCache = append(m.sessionsRawCache[:0], sessions...)
	m.sessionsRawCacheDir = dir
	m.sessionsRawCacheMTime = dirMTime
	m.sessionsRawCacheValid = true
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
	m.sessionsList = filtered
	if m.sessionsCursor < 0 {
		m.sessionsCursor = 0
	}
	if m.sessionsCursor >= len(m.sessionsList) {
		m.sessionsCursor = max(0, len(m.sessionsList)-1)
	}
}

// renderSessionsPanelContent rebuilds the Sessions viewport. Each row
// is a two-line entry — id + a shortened one-line first-prompt summary
// so the user can recognize a session without hunting through log
// files. When the panel has focus the cursor row is highlighted.
func (m *Model) renderSessionsPanelContent() {
	if !m.historyPanelOpen {
		return
	}
	w := max(8, m.sessionsViewport.Width)

	var b strings.Builder
	title := "◇ Sessions"
	if m.historyFocused && m.panelTab == panelTabSessions {
		title = "◇ Sessions · ↑/↓ select · Enter resume"
	}
	b.WriteString(panelTitleStyle.Width(w).Render(title))
	b.WriteString("\n")

	if len(m.sessionsList) == 0 {
		msg := "No prior sessions found."
		for _, wl := range wrapLines(msg, max(1, w-2)) {
			b.WriteString(panelDetailStyle.Width(w).Render(wl))
			b.WriteString("\n")
		}
		m.sessionsViewport.SetContent(strings.TrimRight(b.String(), "\n"))
		return
	}

	for i, s := range m.sessionsList {
		summary := strings.TrimSpace(s.FirstPrompt)
		if summary == "" {
			summary = "(no user prompt recorded)"
		}
		msgCount := fmt.Sprintf("%d msg", s.UserMessages+s.AssistMessage)
		header := fmt.Sprintf("%s · %s", s.ID, msgCount)
		body := "  " + summary
		focused := i == m.sessionsCursor && m.historyFocused && m.panelTab == panelTabSessions

		if focused {
			b.WriteString(panelFocusedStyle.Width(w).Render("▸ " + header))
		} else {
			b.WriteString(lipgloss.NewStyle().Bold(true).Width(w).Render("  " + header))
		}
		b.WriteString("\n")
		for _, wl := range wrapLines(body, max(1, w-2)) {
			if focused {
				b.WriteString(panelFocusedStyle.Width(w).Render(wl))
			} else {
				b.WriteString(panelDetailStyle.Width(w).Render(wl))
			}
			b.WriteString("\n")
		}
	}

	m.sessionsViewport.SetContent(strings.TrimRight(b.String(), "\n"))
}

// handleSessionsPanelKey consumes a key press while the Sessions tab
// has focus. Returns (true, cmd) if handled. j/k navigate, Enter
// resumes the selected session.
func (m *Model) handleSessionsPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Drop focus and re-render so the title's "↑/↓ select" hint
		// disappears, signalling the panel is no longer interactive.
		m.historyFocused = false
		m.renderSessionsPanelContent()
		return true, waitForEvent(m.events)
	case tea.KeyUp:
		if m.sessionsCursor > 0 {
			m.sessionsCursor--
			m.renderSessionsPanelContent()
		}
		return true, waitForEvent(m.events)
	case tea.KeyDown:
		if m.sessionsCursor < len(m.sessionsList)-1 {
			m.sessionsCursor++
			m.renderSessionsPanelContent()
		}
		return true, waitForEvent(m.events)
	case tea.KeyEnter:
		if len(m.sessionsList) == 0 {
			return true, waitForEvent(m.events)
		}
		if m.sessionsCursor < 0 || m.sessionsCursor >= len(m.sessionsList) {
			return true, waitForEvent(m.events)
		}
		id := m.sessionsList[m.sessionsCursor].ID
		if err := m.ResumeSession(id); err != nil {
			m.status = "Resume failed: " + err.Error()
		} else {
			m.status = "Resumed session " + id + "."
			// Bounce the panel back to History so the resumed chat
			// (now populating the main viewport) is the focal surface.
			m.panelTab = panelTabHistory
			m.rerenderActivePanel()
		}
		return true, waitForEvent(m.events)
	}
	return false, nil
}
