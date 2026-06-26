package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/platform/config"
)

// sessionsPanelModel builds a Model wired to a debug-log dir so the
// panel rendering and key handlers can run without depending on the
// rest of the app's lifecycle.
func sessionsPanelModel(t *testing.T, dir string) *Model {
	t.Helper()

	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}
	m := &Model{cfg: cfg}

	m.history.panelOpen = true
	m.history.focused = true
	m.panelTab = panelTabSessions
	m.sessions.viewport = viewport.New(40, 12)

	return m
}

// TestRefreshSessionsListExcludesCurrentSession verifies the live
// session's own log is filtered out of the list — resuming into
// yourself is meaningless and would invite duplicate-state confusion.
func TestRefreshSessionsListExcludesCurrentSession(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "old1.jsonl", []string{`{"type":"message","role":"user","content":"first task"}`})
	writeSessionLog(t, dir, "live.jsonl", []string{`{"type":"message","role":"user","content":"current"}`})
	writeSessionLog(t, dir, "old2.jsonl", []string{`{"type":"message","role":"user","content":"second"}`})

	m := sessionsPanelModel(t, dir)

	m.debugLogPath = filepath.Join(dir, "live.jsonl")
	m.refreshSessionsList()

	if len(m.sessions.list) != 2 {
		t.Fatalf("want 2 sessions (live filtered), got %d", len(m.sessions.list))
	}

	for _, s := range m.sessions.list {
		if s.ID == "live" {
			t.Errorf("live session should be filtered out of the list")
		}
	}
}

func TestRefreshSessionsListHidesLogsWithoutUserMessages(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "human.jsonl", []string{
		`{"type":"message","role":"user","content":"real prompt"}`,
		`{"type":"message","role":"assistant","content":"real answer"}`,
	})
	writeSessionLog(t, dir, "test-startup.jsonl", []string{
		`{"type":"session_start","app_name":"idiom-test"}`,
		`{"type":"server","running":true}`,
		`{"type":"message","role":"ui_trace","content":"ui.refresh_sessions duration_ms=55"}`,
	})

	m := sessionsPanelModel(t, dir)
	m.refreshSessionsList()

	if len(m.sessions.list) != 1 {
		t.Fatalf("expected only the human session, got %#v", m.sessions.list)
	}
	if m.sessions.list[0].ID != "human" {
		t.Fatalf("test-only session should be hidden, got %#v", m.sessions.list)
	}
}

// TestRenderSessionsPanelShowsSummaryNotJustID verifies the rendered
// panel surfaces each session's first user prompt — without a summary
// the user can't tell sessions apart by id alone.
func TestRenderSessionsPanelShowsSummaryNotJustID(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "20260101-000000.jsonl", []string{
		`{"type":"message","role":"user","content":"refactor the eval harness"}`,
		`{"type":"message","role":"assistant","content":"ok"}`,
	})

	m := sessionsPanelModel(t, dir)
	m.refreshSessionsList()
	m.renderSessionsPanelContent()

	got := m.sessions.viewport.View()
	if !strings.Contains(got, "20260101-000000") {
		t.Errorf("rendered panel missing session id:\n%s", got)
	}

	if !strings.Contains(got, "refactor the eval harness") {
		t.Errorf("rendered panel missing summary:\n%s", got)
	}

	if !strings.Contains(got, "Sessions") {
		t.Errorf("rendered panel missing title:\n%s", got)
	}
}

// TestRenderSessionsPanelEmptyShowsPlaceholder protects the
// no-sessions case (e.g. brand-new install). A blank panel would
// look broken; the placeholder confirms the panel is alive but empty.
func TestRenderSessionsPanelEmptyShowsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	m := sessionsPanelModel(t, dir)
	m.refreshSessionsList()
	m.renderSessionsPanelContent()

	got := m.sessions.viewport.View()
	if !strings.Contains(got, "No prior sessions") {
		t.Errorf("empty panel should surface a placeholder, got:\n%s", got)
	}
}

func TestSessionsPanelKeepsCursorVisibleWhenNavigating(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("20260101-0000%02d.jsonl", i)
		writeSessionLog(t, dir, name, []string{fmt.Sprintf(`{"type":"message","role":"user","content":"session prompt %02d"}`, i)})
	}

	m := sessionsPanelModel(t, dir)

	m.sessions.viewport = viewport.New(40, 5)
	m.refreshSessionsList()
	m.renderSessionsPanelContent()

	for i := 0; i < 8; i++ {
		handled, _ := m.handleSessionsPanelKey(tea.KeyMsg{Type: tea.KeyDown})
		if !handled {
			t.Fatalf("key down %d was not handled", i)
		}
	}

	selected := m.sessions.list[m.sessions.cursor].ID
	if got := m.sessions.viewport.View(); !strings.Contains(got, selected) {
		t.Fatalf("selected session %q scrolled off-screen:\n%s", selected, got)
	}

	if m.sessions.viewport.YOffset == 0 {
		t.Fatal("expected sessions viewport to scroll after navigating beyond visible rows")
	}
}

// TestSessionsCursorClampsAfterRefresh guards a subtle bug class: if
// the underlying session list shrinks (file deleted) between renders,
// a stale cursor would index out of bounds. refreshSessionsList must
// clamp the cursor to the new bounds.
func TestSessionsCursorClampsAfterRefresh(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"x"}`})
	writeSessionLog(t, dir, "b.jsonl", []string{`{"type":"message","role":"user","content":"y"}`})

	m := sessionsPanelModel(t, dir)
	m.refreshSessionsList()

	m.sessions.cursor = 1

	if err := os.Remove(filepath.Join(dir, "b.jsonl")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	m.refreshSessionsList()

	if m.sessions.cursor >= len(m.sessions.list) {
		t.Errorf("cursor not clamped after list shrank: cursor=%d len=%d", m.sessions.cursor, len(m.sessions.list))
	}
}

func TestRefreshSessionsListReusesCachedSummaryForUnchangedLog(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"original prompt"}`})
	m := sessionsPanelModel(t, dir)

	m.refreshSessionsList()

	cached := m.sessions.summaryCache[path]

	cached.info.FirstPrompt = "cached prompt"
	m.sessions.summaryCache[path] = cached

	m.refreshSessionsList()

	if len(m.sessions.list) != 1 {
		t.Fatalf("expected one session, got %d", len(m.sessions.list))
	}

	if got := m.sessions.list[0].FirstPrompt; got != "cached prompt" {
		t.Fatalf("expected unchanged log to reuse cached summary, got %q", got)
	}
}

func TestRefreshSessionsListReloadsChangedLogSummary(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"first prompt"}`})
	m := sessionsPanelModel(t, dir)

	m.refreshSessionsList()

	writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"updated prompt"}`})

	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	m.refreshSessionsList()

	if len(m.sessions.list) != 1 {
		t.Fatalf("expected one session, got %d", len(m.sessions.list))
	}

	if got := m.sessions.list[0].FirstPrompt; got != "updated prompt" {
		t.Fatalf("expected changed log to be re-summarized, got %q", got)
	}
}
