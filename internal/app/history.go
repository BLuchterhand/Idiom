package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/muesli/reflow/wordwrap"

	"idiom/internal/llama"
)

// wrapLines word-wraps s to at most width runes per line and returns each
// wrapped line as a slice element. Empty input returns a single empty string
// so callers can iterate uniformly.
func wrapLines(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrapped := wordwrap.String(s, width)
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// historyEntry represents one contiguous run of messages from a single sender.
// Consecutive same-sender messages collapse into one entry (contents grows);
// a new entry is created as soon as the sender changes.
type historyEntry struct {
	sender     string            // "user" or "agent"
	contents   []string          // each message in send order (newest appended)
	toolCalls  []historyToolCall // tool-trace first lines, agent entries only
	summary    string            // local fallback on append, overwritten by 9B when ready
	inProgress bool              // true while the current agent turn is still running
}

type historyToolCall struct {
	step    int
	line    string
	intent  string
	running bool
}

// historySummaryMsg is emitted by summarizeEntry when the 9B model returns.
type historySummaryMsg struct {
	index   int
	summary string
}

// appendHistoryMessage records a message from sender into the panel. If the
// most recent entry has the same sender, the message is folded into it;
// otherwise a new entry is created. Tool calls attach to the matching
// agent entry (user entries never carry tools).
//
// A local-fallback summary is computed synchronously so the panel always has
// something readable; if a route-classifier client is configured the 9B model
// is dispatched to produce a better summary, launching the server on demand
// when allowed.
func (m *Model) appendHistoryMessage(sender, content string, toolCalls []string) {
	content = strings.TrimSpace(content)
	if content == "" && len(toolCalls) == 0 {
		return
	}

	grouped := false
	if n := len(m.historyEntries); n > 0 && m.historyEntries[n-1].sender == sender && !m.historyEntries[n-1].inProgress {
		entry := &m.historyEntries[n-1]
		if content != "" {
			entry.contents = append(entry.contents, content)
		}
		entry.toolCalls = append(entry.toolCalls, historyToolCallsFromLines(toolCalls)...)
		entry.summary = localFallbackSummary(entry)
		grouped = true
	}

	if !grouped {
		entry := historyEntry{sender: sender, toolCalls: historyToolCallsFromLines(toolCalls)}
		if content != "" {
			entry.contents = []string{content}
		}
		entry.summary = localFallbackSummary(&entry)
		m.historyEntries = append(m.historyEntries, entry)
		m.historySelected = len(m.historyEntries) - 1
	}

	idx := len(m.historyEntries) - 1
	if m.cfg.RouteClassifier.Enabled && m.summaryClient != nil {
		go m.summarizeEntry(idx, m.historyEntries[idx])
	}

	m.renderHistoryPanelContent()
	if m.historyPanelOpen {
		m.historyViewport.GotoBottom()
	}
}

func historyToolCallsFromLines(lines []string) []historyToolCall {
	if len(lines) == 0 {
		return nil
	}
	out := make([]historyToolCall, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, historyToolCall{line: line})
	}
	return out
}

func (m *Model) beginAgentHistoryTurn() {
	m.currentTurnTools = nil
	m.currentTurnHistoryActive = true
	m.currentTurnHistoryIndex = len(m.historyEntries)

	entry := historyEntry{sender: "agent", inProgress: true}
	entry.summary = localFallbackSummary(&entry)
	m.historyEntries = append(m.historyEntries, entry)
	m.historySelected = len(m.historyEntries) - 1

	m.renderHistoryPanelContent()
	if m.historyPanelOpen {
		m.historyViewport.GotoBottom()
	}
}

func (m *Model) activeAgentHistoryEntry() (int, *historyEntry, bool) {
	if !m.currentTurnHistoryActive {
		return 0, nil, false
	}
	idx := m.currentTurnHistoryIndex
	if idx < 0 || idx >= len(m.historyEntries) {
		return 0, nil, false
	}
	entry := &m.historyEntries[idx]
	if entry.sender != "agent" || !entry.inProgress {
		return 0, nil, false
	}
	return idx, entry, true
}

func (m *Model) recordToolTraceInHistory(step int, text, intent string, running bool) {
	line := firstToolTraceLine(text)
	if line == "" {
		return
	}
	if _, entry, ok := m.activeAgentHistoryEntry(); ok {
		entry.upsertToolCall(step, line, intent, running)
		entry.summary = localFallbackSummary(entry)
		m.renderHistoryPanelContent()
		if m.historyPanelOpen {
			m.historyViewport.GotoBottom()
		}
		return
	}
	m.currentTurnTools = append(m.currentTurnTools, line)
}

func (entry *historyEntry) upsertToolCall(step int, line, intent string, running bool) {
	if step > 0 {
		for i := range entry.toolCalls {
			if entry.toolCalls[i].step == step {
				entry.toolCalls[i].line = line
				if strings.TrimSpace(intent) != "" {
					entry.toolCalls[i].intent = strings.TrimSpace(intent)
				}
				entry.toolCalls[i].running = running
				return
			}
		}
	}
	entry.toolCalls = append(entry.toolCalls, historyToolCall{
		step:    step,
		line:    line,
		intent:  strings.TrimSpace(intent),
		running: running,
	})
}

// recordAgentTurn flushes the just-completed agent response (last assistant
// message in m.messages) plus any tool calls accumulated during the turn
// into the history panel, then clears the tool-call buffer.
func (m *Model) recordAgentTurn() {
	var response string
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" {
			response = strings.TrimSpace(m.messages[i].Content)
			break
		}
	}
	m.finishAgentHistoryTurn(response)
}

func (m *Model) finishAgentHistoryTurn(response string) {
	response = strings.TrimSpace(response)
	if _, entry, ok := m.activeAgentHistoryEntry(); ok {
		if response != "" {
			entry.contents = append(entry.contents, response)
		}
		entry.inProgress = false
		m.currentTurnHistoryActive = false
		m.currentTurnTools = nil
		if response == "" && len(entry.toolCalls) == 0 {
			m.removeEmptyCurrentHistoryEntry()
			return
		}
		entry.summary = localFallbackSummary(entry)
		idx := m.currentTurnHistoryIndex
		if m.cfg.RouteClassifier.Enabled && m.summaryClient != nil {
			go m.summarizeEntry(idx, *entry)
		}
		m.renderHistoryPanelContent()
		if m.historyPanelOpen {
			m.historyViewport.GotoBottom()
		}
		return
	}

	tools := append([]string(nil), m.currentTurnTools...)
	m.currentTurnTools = nil
	if response == "" && len(tools) == 0 {
		return
	}
	m.appendHistoryMessage("agent", response, tools)
}

func (m *Model) failAgentHistoryTurn(err error) {
	if err == nil {
		return
	}
	if _, entry, ok := m.activeAgentHistoryEntry(); ok {
		entry.inProgress = false
		m.currentTurnHistoryActive = false
		m.currentTurnTools = nil
		entry.contents = append(entry.contents, "Chat failed: "+err.Error())
		entry.summary = localFallbackSummary(entry)
		m.renderHistoryPanelContent()
		if m.historyPanelOpen {
			m.historyViewport.GotoBottom()
		}
		return
	}
	m.currentTurnTools = nil
}

func (m *Model) removeEmptyCurrentHistoryEntry() {
	idx := m.currentTurnHistoryIndex
	if idx < 0 || idx >= len(m.historyEntries) {
		return
	}
	m.historyEntries = append(m.historyEntries[:idx], m.historyEntries[idx+1:]...)
	if m.historySelected >= len(m.historyEntries) {
		m.historySelected = len(m.historyEntries) - 1
	}
	if m.historySelected < 0 {
		m.historySelected = 0
	}
	m.renderHistoryPanelContent()
}

// localFallbackSummary produces a short readable summary without hitting any
// model. Used as the initial summary so the panel never shows bare "…".
func localFallbackSummary(entry *historyEntry) string {
	const maxSummary = 60
	if entry.inProgress && len(entry.contents) == 0 && len(entry.toolCalls) == 0 {
		return "working…"
	}
	if len(entry.contents) > 0 {
		first := strings.TrimSpace(entry.contents[0])
		first = collapseWhitespace(first)
		if first != "" {
			return truncateRunes(first, maxSummary)
		}
	}
	if len(entry.toolCalls) > 0 {
		names := make([]string, 0, len(entry.toolCalls))
		seen := make(map[string]struct{}, len(entry.toolCalls))
		for _, tc := range entry.toolCalls {
			name := tc.line
			if i := strings.Index(name, " | "); i > 0 {
				name = name[:i]
			}
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		if len(names) > 0 {
			return truncateRunes("tools: "+strings.Join(names, ", "), maxSummary)
		}
	}
	return "…"
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// stripThinkTags removes <think>…</think> blocks (including empty ones) from
// model output. Some llama.cpp templates emit the tags inline in `content`
// even when reasoning is disabled server-side, which leaks raw markup into
// the summary.
func stripThinkTags(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "</think>")
		if end < 0 {
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+len("</think>"):]
	}
	return s
}

// summarizeEntry runs the 9B model on a completed entry and emits a
// historySummaryMsg. Safe to call as a goroutine. If the route-classifier
// server is not reachable and auto-start is enabled, the server is launched
// first and the summary retried once it's ready.
func (m *Model) summarizeEntry(idx int, entry historyEntry) {
	_ = m.ensureSummaryServer() // auto-start if configured; completeChatWithFallback routes to m.client on failure

	var sb strings.Builder
	label := "User said"
	if entry.sender == "agent" {
		label = "Agent responded"
	}
	sb.WriteString(label)
	sb.WriteString(":\n")
	for _, c := range entry.contents {
		sb.WriteString(truncateRunes(c, 400))
		sb.WriteString("\n")
	}
	if len(entry.toolCalls) > 0 {
		names := make([]string, 0, len(entry.toolCalls))
		for _, tc := range entry.toolCalls {
			name := tc.line
			if i := strings.Index(name, " | "); i > 0 {
				name = name[:i]
			}
			names = append(names, strings.TrimSpace(name))
		}
		sb.WriteString("Tools: ")
		sb.WriteString(strings.Join(names, ", "))
		sb.WriteString("\n")
		intents := make([]string, 0, len(entry.toolCalls))
		for _, tc := range entry.toolCalls {
			if strings.TrimSpace(tc.intent) != "" {
				intents = append(intents, strings.TrimSpace(tc.intent))
			}
		}
		if len(intents) > 0 {
			sb.WriteString("Why: ")
			sb.WriteString(strings.Join(intents, "; "))
			sb.WriteString("\n")
		}
	}

	thinkingOff := false
	msgs := []llama.ChatMessage{
		{
			Role: "user",
			Content: "Summarize this message in 8 words or fewer. " +
				"Output only the summary — no preamble, no trailing punctuation.\n\n" +
				sb.String(),
		},
	}

	timeout := time.Duration(m.cfg.RouteClassifier.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	maxTokens := m.cfg.RouteClassifier.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 64
	}

	text, err := m.completeChatWithFallback(ctx, msgs, llama.ChatOptions{
		MaxTokens:      maxTokens,
		Temperature:    0.3,
		EnableThinking: &thinkingOff,
	})
	if err != nil {
		return
	}
	summary := strings.TrimSpace(stripThinkTags(text))
	if summary == "" {
		return
	}
	summary = truncateRunes(collapseWhitespace(summary), 60)
	m.events <- historySummaryMsg{index: idx, summary: summary}
}

// summaryServerOnce guards the one-time lazy spin-up of the route-classifier
// server. We only launch once per session even if summarizeEntry is called
// repeatedly before the server becomes ready.
var summaryServerOnce struct {
	sync.Mutex
	started bool
	ready   chan struct{}
}

// ensureSummaryServer returns nil as soon as the route-classifier endpoint is
// reachable. If it's not reachable and auto_start_server is enabled in the
// RouteClassifier config, a llama-server is spawned with those settings and
// the call blocks until it's ready (or errors). Subsequent calls reuse the
// same launch.
func (m *Model) ensureSummaryServer() error {
	if m.summaryClient == nil {
		return errSummaryDisabled
	}

	// Fast path: endpoint already responds.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err := m.summaryClient.ListModels(pingCtx)
	pingCancel()
	if err == nil {
		return nil
	}

	if !m.cfg.RouteClassifier.AutoStartServer {
		return err
	}

	summaryServerOnce.Lock()
	if !summaryServerOnce.started {
		summaryServerOnce.started = true
		summaryServerOnce.ready = make(chan struct{})
		go m.launchSummaryServer()
	}
	ready := summaryServerOnce.ready
	summaryServerOnce.Unlock()

	startTimeout := time.Duration(m.cfg.RouteClassifier.StartTimeoutSeconds) * time.Second
	if startTimeout <= 0 {
		startTimeout = 120 * time.Second
	}

	select {
	case <-ready:
	case <-time.After(startTimeout):
		return errSummaryStartTimeout
	}

	// Verify the endpoint actually responds post-launch.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer verifyCancel()
	_, err = m.summaryClient.ListModels(verifyCtx)
	return err
}

// launchSummaryServer starts llama-server with the RouteClassifier settings
// and closes summaryServerOnce.ready once it's responding. Safe to call as a
// goroutine — ensureSummaryServer guarantees a single invocation per session.
func (m *Model) launchSummaryServer() {
	defer func() {
		summaryServerOnce.Lock()
		if summaryServerOnce.ready != nil {
			// Always close so waiters unblock; they'll verify the endpoint.
			select {
			case <-summaryServerOnce.ready:
			default:
				close(summaryServerOnce.ready)
			}
		}
		summaryServerOnce.Unlock()
	}()

	if m.summaryController == nil {
		return
	}

	startTimeout := time.Duration(m.cfg.RouteClassifier.StartTimeoutSeconds) * time.Second
	if startTimeout <= 0 {
		startTimeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	if err := m.summaryController.Start(ctx); err != nil {
		return
	}
	_ = m.summaryController.WaitUntilReady(ctx)
}

// truncateRunes truncates s to at most max runes, appending "…" if cut.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// Sentinel errors kept private — only used to disambiguate failure modes
// within the summary pipeline. Callers treat any non-nil as "skip 9B, keep
// the local fallback".
var (
	errSummaryDisabled     = summaryError("route classifier disabled")
	errSummaryStartTimeout = summaryError("route classifier server did not become ready in time")
)

type summaryError string

func (e summaryError) Error() string { return string(e) }
