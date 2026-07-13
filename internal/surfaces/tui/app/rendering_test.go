package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
)

var _ = ginkgo.It("render messages wraps long content inside viewport", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(32, 12),
		messages: []llama.ChatMessage{{
			Role:    "assistant",
			Content: "This sentence is intentionally long enough to wrap instead of disappearing past the right edge.",
		}},
	}

	m.renderMessages(true)

	{
		got := m.viewport.TotalLineCount()
		gomega.Expect(got).To(gomega.BeNumerically(">", 2), fmt.Sprintf("expected long content to wrap across multiple viewport lines, got %d line(s)", got))
	}

	{
		view := m.viewport.View()
		gomega.Expect(strings.Contains(view, "edge.")).To(gomega.BeTrue(), fmt.Sprintf("expected wrapped viewport to preserve text near the right edge, got:\n%s", view))
	}
})

var _ = ginkgo.It("request history override does not hide visible transcript", func() {
	override := []llama.ChatMessage{{Role: "user", Content: "nested plan-execute prompt"}}
	m := &Model{
		ready:    true,
		viewport: viewport.New(80, 12),
		promptRuntimeState: promptRuntimeState{promptRuntimeSnapshotState: promptRuntimeSnapshotState{
			requestHistoryOverride: &override,
		}},
		messages: []llama.ChatMessage{
			{Role: "user", Content: "prior visible question"},
			{Role: "assistant", Content: "prior visible answer"},
		},
	}

	payload := newDetachedPromptExecutionSession(context.Background(), m).requestMessages(context.Background())
	gomega.Expect(messagesContain(payload, "nested plan-execute prompt")).To(gomega.BeTrue(), fmt.Sprintf("nested payload should use isolated request history: %#v", payload))
	gomega.Expect(messagesContain(payload, "prior visible answer")).To(gomega.BeFalse(), fmt.Sprintf("visible transcript should not leak into isolated nested payload: %#v", payload))

	m.renderMessages(true)

	view := m.viewport.View()
	gomega.Expect(view).To(gomega.ContainSubstring("prior visible question"), fmt.Sprintf("visible transcript should remain rendered while nested history is isolated:\n%s", view))
	gomega.Expect(view).To(gomega.ContainSubstring("prior visible answer"), fmt.Sprintf("visible transcript should remain rendered while nested history is isolated:\n%s", view))
})

func messagesContain(messages []llama.ChatMessage, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}

	return false
}

var _ = ginkgo.It("protect control markers for markdown preserves question marker", func() {
	got := protectControlMarkersForMarkdown("<<QUESTION>>: missing exact anchor")
	gomega.Expect(strings.Contains(got, "`<<QUESTION>>`:")).To(gomega.BeTrue(), fmt.Sprintf("question marker should be protected from markdown HTML parsing, got %q", got))
})

var _ = ginkgo.It("view omits compact context footer below input", func() {
	cfg := config.Default()

	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil

	m := New(cfg)

	defer m.Close()

	m.ready = true
	m.width = 80
	m.contextError = ""
	m.viewport = viewport.New(80, 3)
	m.input.SetHeight(1)
	m.input.SetValue("hello")

	view := m.View()

	for _, removed := range []string{"mode:", "trace:", "ctx:"} {
		gomega.Expect(strings.Contains(view, removed)).To(gomega.BeFalse(), fmt.Sprintf("view should not render compact context footer containing %q:\n%s", removed, view))
	}

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	gomega.Expect(len(lines)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("input should be the last normal-state row, got:\n%s", view))
	gomega.Expect(lines[len(lines)-1]).To(gomega.ContainSubstring("hello"), fmt.Sprintf("input should be the last normal-state row, got:\n%s", view))
})

var _ = ginkgo.It("view renders status above input", func() {
	cfg := config.Default()

	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil

	m := New(cfg)

	defer m.Close()

	m.ready = true
	m.width = 80
	m.height = 12
	m.status = "Plan updated: ./.idiom/plans/last.plan.json"
	m.viewport = viewport.New(80, 4)
	m.input.SetHeight(1)
	m.input.SetValue("next prompt")

	view := m.View()
	gomega.Expect(strings.Contains(view, "Plan updated: ./.idiom/plans/last.plan.json")).To(gomega.BeTrue(), fmt.Sprintf("view should render operator status, got:\n%s", view))

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	gomega.Expect(len(lines) < 2).To(gomega.BeFalse(), fmt.Sprintf("expected status and input rows, got:\n%s", view))

	gomega.Expect(strings.Contains(lines[len(lines)-2], "Plan updated:")).To(gomega.BeTrue(), fmt.Sprintf("status should render immediately above input, got:\n%s", view))

	gomega.Expect(strings.Contains(lines[len(lines)-1], "next prompt")).To(gomega.BeTrue(), fmt.Sprintf("input should remain the bottom normal-state row, got:\n%s", view))
})

var _ = ginkgo.It("resize accounts for status line", func() {
	cfg := config.Default()

	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil

	m := New(cfg)

	defer m.Close()

	m.width = 80
	m.height = 14
	m.status = "Server up."

	m.resize()

	view := m.View()

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	{
		got := len(lines)
		gomega.Expect(got).To(gomega.Equal(m.height), fmt.Sprintf("view height = %d, want terminal height %d:\n%s", got, m.height, view))
	}

	gomega.Expect(strings.Contains(lines[len(lines)-2], "Server up.")).To(gomega.BeTrue(), fmt.Sprintf("status should consume one accounted row above input, got:\n%s", view))
})

var _ = ginkgo.It("resize uses full terminal height without footer", func() {
	cfg := config.Default()

	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil

	m := New(cfg)

	defer m.Close()

	m.width = 80
	m.height = 14

	m.resize()

	view := m.View()

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	{
		got := len(lines)
		gomega.Expect(got).To(gomega.Equal(m.height), fmt.Sprintf("view height = %d, want terminal height %d:\n%s", got, m.height, view))
	}

	gomega.Expect(strings.Contains(lines[len(lines)-1], "Type a prompt")).To(gomega.BeTrue(), fmt.Sprintf("input should be flush with bottom, got:\n%s", view))
})

var _ = ginkgo.It("input height grows for wrapped prompt", func() {
	cfg := config.Default()

	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil

	m := New(cfg)

	defer m.Close()

	m.width = 32
	m.height = 18
	m.input.SetValue(strings.Repeat("wrapped ", 12))

	m.resize()

	{
		got := m.input.Height()
		gomega.Expect(got).To(gomega.BeNumerically(">", 1), fmt.Sprintf("input height = %d, want growth for wrapped text", got))
	}

	lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
	{
		got := len(lines)
		gomega.Expect(got).To(gomega.Equal(m.height), fmt.Sprintf("view height = %d, want terminal height %d", got, m.height))
	}
})

var _ = ginkgo.It("input height uses textarea wrapped line count", func() {
	cfg := config.Default()

	cfg.Chat.DebugLogDir = ""
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil

	m := New(cfg)

	defer m.Close()

	m.width = 28
	m.height = 18
	m.input.SetValue("first visible words should remain above the final wrapped line")

	m.resize()

	wrappedRows := 0
	for _, line := range strings.Split(m.input.Value(), "\n") {
		wrappedRows += max(1, len(wrapLines(line, m.input.Width()-len(m.input.Prompt))))
	}

	{
		got, want := m.input.Height(), min(wrappedRows, m.height/3)
		gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("input height = %d, want prompt-aware wrapped row cap %d", got, want))
	}

	view := m.View()
	gomega.Expect(strings.Contains(view, "first visible")).To(gomega.BeTrue(), fmt.Sprintf("input view should retain the top wrapped line, got:\n%s", view))
})

// OpenCode-aligned: requestMessages no longer injects a static workspace
// overview. The model discovers the workspace on demand through tool
// calls (list_files, search_*, glob). We assert the absence so a future
// regression that re-introduces the static dump gets caught.
var _ = ginkgo.It("request messages omits static workspace overview", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
	m := &Model{
		workspace: mgr,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first prompt"},
		},
	}
	msgs := newDetachedPromptExecutionSession(context.Background(), m).requestMessages(context.Background())

	gomega.Expect(containsSystemContent(msgs, "Workspace overview")).To(gomega.BeFalse(), fmt.Sprintf("static workspace overview must not be injected; got:\n%#v", msgs))
})

var _ = ginkgo.It("request messages does not compact operator messages", func() {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original implementation goal"},
			{Role: "operator", Content: "recover status: ready\npacket: .idiom/recovery/secret/packet.json"},
		},
		cfg: config.Config{
			LlamaServer: config.LlamaServer{CtxSize: 8000},
		},
	}
	threshold := compactionThreshold(snapshotCompactionExecution(m))

	for i := 0; i < threshold+1; i++ {
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: "assistant turn"})
	}

	msgs := newDetachedPromptExecutionSession(context.Background(), m).requestMessages(context.Background())
	for _, msg := range msgs {
		gomega.Expect(msg.Content).To(gomega.Not(gomega.ContainSubstring("recover status:")), fmt.Sprintf("operator status content must not be compacted or replayed: %#v", msgs))
		gomega.Expect(msg.Content).To(gomega.Not(gomega.ContainSubstring(".idiom/recovery/secret/packet.json")), fmt.Sprintf("operator status content must not be compacted or replayed: %#v", msgs))
	}

	gomega.Expect(len(m.messages)).To(gomega.BeNumerically(">=", 2), fmt.Sprintf("request payload filtering must not mutate transcript messages: %#v", m.messages))
	gomega.Expect(m.messages[1].Role).To(gomega.Equal("operator"), fmt.Sprintf("request payload filtering must not mutate transcript messages: %#v", m.messages))
})

var _ = ginkgo.It("request messages drops assistant reasoning from cross turn history", func() {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first prompt"},
			{Role: "assistant", Content: "first answer", ReasoningContent: "hidden plan from prior turn"},
			{Role: "user", Content: "second prompt"},
		},
	}

	msgs := newDetachedPromptExecutionSession(context.Background(), m).requestMessages(context.Background())
	for _, msg := range msgs {
		gomega.Expect(msg.ReasoningContent).To(gomega.Equal(""), fmt.Sprintf("request payload must not replay cross-turn hidden reasoning: %#v", msgs))
	}

	gomega.Expect(m.messages[1].ReasoningContent).NotTo(gomega.Equal(""), fmt.Sprintf("request payload filtering must not mutate transcript history"))
})

var _ = ginkgo.It("slash clear drops invisible cross turn context", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	{
		err := os.WriteFile(filepath.Join(dir, "stale.go"), []byte("package stale\nconst Sentinel = \"STALE_PINNED_CONTEXT\"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write stale fixture: %v", err))
	}

	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
	m := &Model{
		workspace: mgr,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "prior question"},
			{Role: "assistant", Content: "prior answer mentioning canonicalMutationBlockerFinalAnswer"},
		},
	}
	m.pinnedFiles = newPinnedFileRegistry()
	m.pinnedFiles.Pin("stale.go")
	priorObjective := "should canonicalMutationBlockerFinalAnswer be replaced"
	m.SetLastTurnSourceResults(priorObjective, []workspace.ToolResult{{
		Tool:    workspace.ToolNameReadFile,
		Summary: "stale source read",
		Output:  "STALE_SEED_CONTEXT",
	}})

	m.slashClear("/new", []string{"/new"})
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "fresh unrelated naming question"})

	payload := newDetachedPromptExecutionSession(context.Background(), m).requestMessages(context.Background())
	for _, msg := range payload {
		gomega.Expect(msg.Content).To(gomega.Not(gomega.ContainSubstring("canonicalMutationBlockerFinalAnswer")), fmt.Sprintf("fresh session payload leaked cleared context in %#v", payload))
		gomega.Expect(msg.Content).To(gomega.Not(gomega.ContainSubstring("STALE_PINNED_CONTEXT")), fmt.Sprintf("fresh session payload leaked cleared context in %#v", payload))
		gomega.Expect(msg.Content).To(gomega.Not(gomega.ContainSubstring(pinnedFileSnapshotHeaderTag)), fmt.Sprintf("fresh session payload leaked cleared context in %#v", payload))
	}

	{
		seeds := m.LastTurnSourceResultsForObjective(priorObjective)
		gomega.Expect(len(seeds)).To(gomega.Equal(0), fmt.Sprintf("clear should drop cross-turn seed results, got %#v", seeds))
	}
})

var _ = ginkgo.It("rendered message body cache invalidates when messages change", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{{Role: "assistant", Content: "first body"}},
	}

	first := m.renderedMessageBody()
	gomega.Expect(strings.Contains(first, "first body")).To(gomega.BeTrue(), fmt.Sprintf("expected initial render to include first body, got:\n%s", first))

	m.messageRenderCache = "cached sentinel"
	m.messageRenderWidth = m.viewport.Width
	m.messageRenderHash = messageRenderFingerprint(m.messages)
	m.messageRenderMD = false

	{
		got := m.renderedMessageBody()
		gomega.Expect(got).To(gomega.Equal("cached sentinel"), fmt.Sprintf("expected unchanged transcript to reuse cache, got:\n%s", got))
	}

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "second body"})

	updated := m.renderedMessageBody()

	gomega.Expect(strings.Contains(updated, "cached sentinel")).To(gomega.BeFalse(), fmt.Sprintf("expected cache to be invalidated after message change, got:\n%s", updated))

	gomega.Expect(strings.Contains(updated, "second body")).To(gomega.BeTrue(), fmt.Sprintf("expected updated render to include new message, got:\n%s", updated))
})

var _ = ginkgo.It("ensure markdown renderer always updates width and invalidates cache", func() {
	m := &Model{
		ready:    true,
		viewport: viewport.New(80, 12),
		messages: []llama.ChatMessage{{Role: "assistant", Content: "test"}},
	}

	// First call at width 80.
	m.ensureMarkdownRenderer(80)
	gomega.Expect(m.mdRendererWrapWidth).To(gomega.Equal(80), fmt.Sprintf("expected wrap width 80, got %d", m.mdRendererWrapWidth))

	// Prime the render cache.
	_ = m.renderedMessageBody()

	// Call at a different width — should update wrap width and invalidate cache.
	m.ensureMarkdownRenderer(40)
	gomega.Expect(m.mdRendererWrapWidth).To(gomega.Equal(40), fmt.Sprintf("expected wrap width 40 after resize, got %d", m.mdRendererWrapWidth))
	gomega.Expect(m.messageRenderCache).To(gomega.Equal(""), fmt.Sprint("expected render cache to be invalidated after width change"))

	// Call again at the same width — must still recreate the renderer and
	// invalidate the cache.  The old code had a guard that skipped this,
	// leaving a stale glamour word-wrap cache that produced short lines.
	m.ensureMarkdownRenderer(40)
	gomega.Expect(m.mdRendererWrapWidth).To(gomega.Equal(40), fmt.Sprintf("expected wrap width 40 after second call at same width, got %d", m.mdRendererWrapWidth))
	gomega.Expect(m.messageRenderCache).To(gomega.Equal(""), fmt.Sprint("expected render cache to be invalidated even when same width is set again"))

	// Render a long line at a known width to verify no abnormally short lines.
	m.ensureMarkdownRenderer(40)
	m.messages = []llama.ChatMessage{{Role: "assistant", Content: "This is a deliberately long sentence that should wrap at roughly forty characters per line inside the glamour term renderer with the current word wrap setting."}}
	body := m.renderedMessageBody()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if len([]rune(trimmed)) > 45 {
			continue // reasonably long line, no issue
		}
		// Short lines are fine if they're blank or just contain a trailing part.
		if trimmed != "" && len([]rune(trimmed)) < 10 {
			gomega.Expect(trimmed).To(gomega.HavePrefix(" "), fmt.Sprintf("line %d is suspiciously short (%d chars): %q — possible stale glamour word-wrap cache", i, len([]rune(trimmed)), trimmed))
		}
	}
})

func containsSystemContent(messages []llama.ChatMessage, needle string) bool {
	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, needle) {
			return true
		}
	}

	return false
}
