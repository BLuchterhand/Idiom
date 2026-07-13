package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/config"
)

// sessionsPanelModel builds a Model wired to a debug-log dir so the
// panel rendering and key handlers can run without depending on the
// rest of the app's lifecycle.
func sessionsPanelModel(t ginkgo.FullGinkgoTInterface, dir string) *Model {
	ginkgo.GinkgoHelper()

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
var _ = ginkgo.It("refresh sessions list excludes current session", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeSessionLog(t, dir, "old1.jsonl", []string{`{"type":"message","role":"user","content":"first task"}`})
	writeSessionLog(t, dir, "live.jsonl", []string{`{"type":"message","role":"user","content":"current"}`})
	writeSessionLog(t, dir, "old2.jsonl", []string{`{"type":"message","role":"user","content":"second"}`})

	m := sessionsPanelModel(t, dir)

	m.debugLogPath = filepath.Join(dir, "live.jsonl")
	m.refreshSessionsList()

	gomega.Expect(len(m.sessions.list)).To(gomega.Equal(2), fmt.Sprintf("want 2 sessions (live filtered), got %d", len(m.sessions.list)))

	for _, s := range m.sessions.list {
		gomega.Expect(s.ID).NotTo(gomega.Equal("live"), fmt.Sprintf("live session should be filtered out of the list"))
	}
})

var _ = ginkgo.It("refresh sessions list hides logs without user messages", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(len(m.sessions.list)).To(gomega.Equal(1), fmt.Sprintf("expected only the human session, got %#v", m.sessions.list))
	gomega.Expect(m.sessions.list[0].ID).To(gomega.Equal("human"), fmt.Sprintf("test-only session should be hidden, got %#v", m.sessions.list))
})

// TestRenderSessionsPanelShowsSummaryNotJustID verifies the rendered
// panel surfaces each session's first user prompt — without a summary
// the user can't tell sessions apart by id alone.
var _ = ginkgo.It("render sessions panel shows summary not just id", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeSessionLog(t, dir, "20260101-000000.jsonl", []string{
		`{"type":"message","role":"user","content":"refactor the eval harness"}`,
		`{"type":"message","role":"assistant","content":"ok"}`,
	})

	m := sessionsPanelModel(t, dir)
	m.refreshSessionsList()
	m.renderSessionsPanelContent()

	got := m.sessions.viewport.View()
	gomega.Expect(strings.Contains(got, "20260101-000000")).To(gomega.BeTrue(), fmt.Sprintf("rendered panel missing session id:\n%s", got))

	gomega.Expect(strings.Contains(got, "refactor the eval harness")).To(gomega.BeTrue(), fmt.Sprintf("rendered panel missing summary:\n%s", got))

	gomega.Expect(strings.Contains(got, "Sessions")).To(gomega.BeTrue(), fmt.Sprintf("rendered panel missing title:\n%s", got))
})

// TestRenderSessionsPanelEmptyShowsPlaceholder protects the
// no-sessions case (e.g. brand-new install). A blank panel would
// look broken; the placeholder confirms the panel is alive but empty.
var _ = ginkgo.It("render sessions panel empty shows placeholder", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	m := sessionsPanelModel(t, dir)
	m.refreshSessionsList()
	m.renderSessionsPanelContent()

	got := m.sessions.viewport.View()
	gomega.Expect(strings.Contains(got, "No prior sessions")).To(gomega.BeTrue(), fmt.Sprintf("empty panel should surface a placeholder, got:\n%s", got))
})

var _ = ginkgo.It("sessions panel keeps cursor visible when navigating", func() {
	t := ginkgo.GinkgoT()
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
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprintf("key down %d was not handled", i))
	}

	selected := m.sessions.list[m.sessions.cursor].ID
	{
		got := m.sessions.viewport.View()
		gomega.Expect(strings.Contains(got, selected)).To(gomega.BeTrue(), fmt.Sprintf("selected session %q scrolled off-screen:\n%s", selected, got))
	}

	gomega.Expect(m.sessions.viewport.YOffset).NotTo(gomega.Equal(0), fmt.Sprint("expected sessions viewport to scroll after navigating beyond visible rows"))
})

// TestSessionsCursorClampsAfterRefresh guards a subtle bug class: if
// the underlying session list shrinks (file deleted) between renders,
// a stale cursor would index out of bounds. refreshSessionsList must
// clamp the cursor to the new bounds.
var _ = ginkgo.It("sessions cursor clamps after refresh", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"x"}`})
	writeSessionLog(t, dir, "b.jsonl", []string{`{"type":"message","role":"user","content":"y"}`})

	m := sessionsPanelModel(t, dir)
	m.refreshSessionsList()

	m.sessions.cursor = 1

	{
		err := os.Remove(filepath.Join(dir, "b.jsonl"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("remove: %v", err))
	}

	m.refreshSessionsList()

	gomega.Expect(m.sessions.cursor >= len(m.sessions.list)).To(gomega.BeFalse(), fmt.Sprintf("cursor not clamped after list shrank: cursor=%d len=%d", m.sessions.cursor, len(m.sessions.list)))
})

var _ = ginkgo.It("refresh sessions list reuses cached summary for unchanged log", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"original prompt"}`})
	m := sessionsPanelModel(t, dir)

	m.refreshSessionsList()

	cached := m.sessions.summaryCache[path]

	cached.info.FirstPrompt = "cached prompt"
	m.sessions.summaryCache[path] = cached

	m.refreshSessionsList()

	gomega.Expect(len(m.sessions.list)).To(gomega.Equal(1), fmt.Sprintf("expected one session, got %d", len(m.sessions.list)))

	{
		got := m.sessions.list[0].FirstPrompt
		gomega.Expect(got).To(gomega.Equal("cached prompt"), fmt.Sprintf("expected unchanged log to reuse cached summary, got %q", got))
	}
})

var _ = ginkgo.It("refresh sessions list reloads changed log summary", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"first prompt"}`})
	m := sessionsPanelModel(t, dir)

	m.refreshSessionsList()

	writeSessionLog(t, dir, "a.jsonl", []string{`{"type":"message","role":"user","content":"updated prompt"}`})

	modTime := time.Now().Add(2 * time.Second)
	{
		err := os.Chtimes(path, modTime, modTime)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("chtimes: %v", err))
	}

	m.refreshSessionsList()

	gomega.Expect(len(m.sessions.list)).To(gomega.Equal(1), fmt.Sprintf("expected one session, got %d", len(m.sessions.list)))

	{
		got := m.sessions.list[0].FirstPrompt
		gomega.Expect(got).To(gomega.Equal("updated prompt"), fmt.Sprintf("expected changed log to be re-summarized, got %q", got))
	}
})
