package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
)

func TestRenderMessagesWrapsLongContentInsideViewport(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(32, 12),
		messages: []llama.ChatMessage{{
			Role:    "assistant",
			Content: "This sentence is intentionally long enough to wrap instead of disappearing past the right edge.",
		}},
	}

	m.renderMessages(true)

	if got := m.viewport.TotalLineCount(); got <= 2 {
		t.Fatalf("expected long content to wrap across multiple viewport lines, got %d line(s)", got)
	}

	if view := m.viewport.View(); !strings.Contains(view, "edge.") {
		t.Fatalf("expected wrapped viewport to preserve text near the right edge, got:\n%s", view)
	}
}

func TestRequestHistoryOverrideDoesNotHideVisibleTranscript(t *testing.T) {
	override := []llama.ChatMessage{{Role: "user", Content: "nested plan-execute prompt"}}
	m := &Model{
		ready:                  true,
		viewport:               viewport.New(80, 12),
		requestHistoryOverride: &override,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "prior visible question"},
			{Role: "assistant", Content: "prior visible answer"},
		},
	}

	payload := m.requestMessages()
	if !messagesContain(payload, "nested plan-execute prompt") {
		t.Fatalf("nested payload should use isolated request history: %#v", payload)
	}
	if messagesContain(payload, "prior visible answer") {
		t.Fatalf("visible transcript should not leak into isolated nested payload: %#v", payload)
	}

	m.renderMessages(true)

	view := m.viewport.View()
	if !strings.Contains(view, "prior visible question") || !strings.Contains(view, "prior visible answer") {
		t.Fatalf("visible transcript should remain rendered while nested history is isolated:\n%s", view)
	}
}

func messagesContain(messages []llama.ChatMessage, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}

	return false
}

func TestProtectControlMarkersForMarkdownPreservesQuestionMarker(t *testing.T) {
	got := protectControlMarkersForMarkdown("<<QUESTION>>: missing exact anchor")
	if !strings.Contains(got, "`<<QUESTION>>`:") {
		t.Fatalf("question marker should be protected from markdown HTML parsing, got %q", got)
	}
}

func TestViewOmitsCompactContextFooterBelowInput(t *testing.T) {
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
		if strings.Contains(view, removed) {
			t.Fatalf("view should not render compact context footer containing %q:\n%s", removed, view)
		}
	}

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "hello") {
		t.Fatalf("input should be the last normal-state row, got:\n%s", view)
	}
}

func TestViewRendersStatusAboveInput(t *testing.T) {
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
	if !strings.Contains(view, "Plan updated: ./.idiom/plans/last.plan.json") {
		t.Fatalf("view should render operator status, got:\n%s", view)
	}

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected status and input rows, got:\n%s", view)
	}

	if !strings.Contains(lines[len(lines)-2], "Plan updated:") {
		t.Fatalf("status should render immediately above input, got:\n%s", view)
	}

	if !strings.Contains(lines[len(lines)-1], "next prompt") {
		t.Fatalf("input should remain the bottom normal-state row, got:\n%s", view)
	}
}

func TestResizeAccountsForStatusLine(t *testing.T) {
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
	if got := len(lines); got != m.height {
		t.Fatalf("view height = %d, want terminal height %d:\n%s", got, m.height, view)
	}

	if !strings.Contains(lines[len(lines)-2], "Server up.") {
		t.Fatalf("status should consume one accounted row above input, got:\n%s", view)
	}
}

func TestResizeUsesFullTerminalHeightWithoutFooter(t *testing.T) {
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
	if got := len(lines); got != m.height {
		t.Fatalf("view height = %d, want terminal height %d:\n%s", got, m.height, view)
	}

	if !strings.Contains(lines[len(lines)-1], "Type a prompt") {
		t.Fatalf("input should be flush with bottom, got:\n%s", view)
	}
}

func TestInputHeightGrowsForWrappedPrompt(t *testing.T) {
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

	if got := m.input.Height(); got <= 1 {
		t.Fatalf("input height = %d, want growth for wrapped text", got)
	}

	lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
	if got := len(lines); got != m.height {
		t.Fatalf("view height = %d, want terminal height %d", got, m.height)
	}
}

func TestInputHeightUsesTextareaWrappedLineCount(t *testing.T) {
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

	if got, want := m.input.Height(), min(wrappedRows, m.height/3); got != want {
		t.Fatalf("input height = %d, want prompt-aware wrapped row cap %d", got, want)
	}

	view := m.View()
	if !strings.Contains(view, "first visible") {
		t.Fatalf("input view should retain the top wrapped line, got:\n%s", view)
	}
}

// OpenCode-aligned: requestMessages no longer injects a static workspace
// overview. The model discovers the workspace on demand through tool
// calls (list_files, search_*, glob). We assert the absence so a future
// regression that re-introduces the static dump gets caught.
func TestRequestMessagesOmitsStaticWorkspaceOverview(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
	m := &Model{
		workspace: mgr,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first prompt"},
		},
	}
	msgs := m.requestMessages()

	if containsSystemContent(msgs, "Workspace overview") {
		t.Fatalf("static workspace overview must not be injected; got:\n%#v", msgs)
	}
}

func TestRequestMessagesDoesNotCompactOperatorMessages(t *testing.T) {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original implementation goal"},
			{Role: "operator", Content: "recover status: ready\npacket: .idiom/recovery/secret/packet.json"},
		},
		cfg: config.Config{
			LlamaServer: config.LlamaServer{CtxSize: 8000},
		},
	}
	threshold := m.compactionThreshold()

	for i := 0; i < threshold+1; i++ {
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: "assistant turn"})
	}

	msgs := m.requestMessages()
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "recover status:") ||
			strings.Contains(msg.Content, ".idiom/recovery/secret/packet.json") {

			t.Fatalf("operator status content must not be compacted or replayed: %#v", msgs)
		}
	}

	if len(m.messages) < 2 || m.messages[1].Role != "operator" {
		t.Fatalf("request payload filtering must not mutate transcript messages: %#v", m.messages)
	}
}

func TestRequestMessagesDropsAssistantReasoningFromCrossTurnHistory(t *testing.T) {
	m := &Model{
		messages: []llama.ChatMessage{
			{Role: "user", Content: "first prompt"},
			{Role: "assistant", Content: "first answer", ReasoningContent: "hidden plan from prior turn"},
			{Role: "user", Content: "second prompt"},
		},
	}

	msgs := m.requestMessages()
	for _, msg := range msgs {
		if msg.ReasoningContent != "" {
			t.Fatalf("request payload must not replay cross-turn hidden reasoning: %#v", msgs)
		}
	}

	if m.messages[1].ReasoningContent == "" {
		t.Fatalf("request payload filtering must not mutate transcript history")
	}
}

func TestSlashClearDropsInvisibleCrossTurnContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stale.go"), []byte("package stale\nconst Sentinel = \"STALE_PINNED_CONTEXT\"\n"), 0o644); err != nil {
		t.Fatalf("write stale fixture: %v", err)
	}

	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
	m := &Model{
		workspace: mgr,
		messages: []llama.ChatMessage{
			{Role: "user", Content: "prior question"},
			{Role: "assistant", Content: "prior answer mentioning canonicalMutationBlockerFinalAnswer"},
		},
	}
	m.pinnedFilesRegistry().Pin("stale.go")
	priorObjective := "should canonicalMutationBlockerFinalAnswer be replaced"
	m.SetLastTurnSourceResults(priorObjective, []workspace.ToolResult{{
		Tool:    workspace.ToolNameReadFile,
		Summary: "stale source read",
		Output:  "STALE_SEED_CONTEXT",
	}})

	m.slashClear("/new", []string{"/new"})
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "fresh unrelated naming question"})

	payload := m.requestMessages()
	for _, msg := range payload {
		if strings.Contains(msg.Content, "canonicalMutationBlockerFinalAnswer") ||
			strings.Contains(msg.Content, "STALE_PINNED_CONTEXT") ||
			strings.Contains(msg.Content, pinnedFileSnapshotHeaderTag) {
			t.Fatalf("fresh session payload leaked cleared context in %#v", payload)
		}
	}

	if seeds := m.LastTurnSourceResultsForObjective(priorObjective); len(seeds) != 0 {
		t.Fatalf("clear should drop cross-turn seed results, got %#v", seeds)
	}
}

func TestRenderedMessageBodyCacheInvalidatesWhenMessagesChange(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(48, 12),
		messages: []llama.ChatMessage{{Role: "assistant", Content: "first body"}},
	}

	first := m.renderedMessageBody()
	if !strings.Contains(first, "first body") {
		t.Fatalf("expected initial render to include first body, got:\n%s", first)
	}

	m.messageRenderCache = "cached sentinel"
	m.messageRenderWidth = m.viewport.Width
	m.messageRenderHash = messageRenderFingerprint(m.messages)
	m.messageRenderMD = false

	if got := m.renderedMessageBody(); got != "cached sentinel" {
		t.Fatalf("expected unchanged transcript to reuse cache, got:\n%s", got)
	}

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "second body"})

	updated := m.renderedMessageBody()

	if strings.Contains(updated, "cached sentinel") {
		t.Fatalf("expected cache to be invalidated after message change, got:\n%s", updated)
	}

	if !strings.Contains(updated, "second body") {
		t.Fatalf("expected updated render to include new message, got:\n%s", updated)
	}
}

func TestEnsureMarkdownRendererAlwaysUpdatesWidthAndInvalidatesCache(t *testing.T) {
	m := &Model{
		ready:    true,
		viewport: viewport.New(80, 12),
		messages: []llama.ChatMessage{{Role: "assistant", Content: "test"}},
	}

	// First call at width 80.
	m.ensureMarkdownRenderer(80)
	if m.mdRendererWrapWidth != 80 {
		t.Fatalf("expected wrap width 80, got %d", m.mdRendererWrapWidth)
	}

	// Prime the render cache.
	_ = m.renderedMessageBody()

	// Call at a different width — should update wrap width and invalidate cache.
	m.ensureMarkdownRenderer(40)
	if m.mdRendererWrapWidth != 40 {
		t.Fatalf("expected wrap width 40 after resize, got %d", m.mdRendererWrapWidth)
	}
	if m.messageRenderCache != "" {
		t.Fatal("expected render cache to be invalidated after width change")
	}

	// Call again at the same width — must still recreate the renderer and
	// invalidate the cache.  The old code had a guard that skipped this,
	// leaving a stale glamour word-wrap cache that produced short lines.
	m.ensureMarkdownRenderer(40)
	if m.mdRendererWrapWidth != 40 {
		t.Fatalf("expected wrap width 40 after second call at same width, got %d", m.mdRendererWrapWidth)
	}
	if m.messageRenderCache != "" {
		t.Fatal("expected render cache to be invalidated even when same width is set again")
	}

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
		if len([]rune(trimmed)) < 10 && trimmed != "" && !strings.HasPrefix(trimmed, " ") {
			t.Fatalf("line %d is suspiciously short (%d chars): %q — possible stale glamour word-wrap cache", i, len([]rune(trimmed)), trimmed)
		}
	}
}

func containsSystemContent(messages []llama.ChatMessage, needle string) bool {
	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, needle) {
			return true
		}
	}

	return false
}
