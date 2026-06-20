package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/muesli/reflow/wordwrap"

	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
)

const historyPanelRenderThrottle = 75 * time.Millisecond

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
	sender      string              // "user" or "agent"
	contents    []string            // each message in send order (newest appended)
	toolCalls   []historyToolCall   // tool-trace first lines, agent entries only
	auditEvents []historyAuditEvent // lane, contract, recovery, privacy, and cache events
	summary     string              // local fallback on append, overwritten by 9B when ready
	inProgress  bool                // true while the current agent turn is still running
}

type historyToolCall struct {
	step    int
	line    string
	intent  string
	kind    string
	outcome string
	running bool
}

type historyAuditEvent struct {
	kind   string
	label  string
	detail string
	status string
	step   int
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

	if n := len(m.history.entries); n > 0 && m.history.entries[n-1].sender == sender && !m.history.entries[n-1].inProgress {
		entry := &m.history.entries[n-1]
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
		m.history.entries = append(m.history.entries, entry)
		m.history.selected = len(m.history.entries) - 1
	}

	idx := len(m.history.entries) - 1
	if m.cfg.RouteClassifier.Enabled && m.summaryClient != nil {
		go m.summarizeEntry(idx, m.history.entries[idx])
	}

	m.requestHistoryPanelRender(false, true)
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
	m.currentTurnHistoryIndex = len(m.history.entries)
	m.thoughts.reset()

	entry := historyEntry{sender: "agent", inProgress: true}
	if privacy := m.historyPrivacyAuditEvent(); privacy.label != "" {
		entry.auditEvents = append(entry.auditEvents, privacy)
	}

	entry.summary = localFallbackSummary(&entry)
	m.history.entries = append(m.history.entries, entry)
	m.history.selected = len(m.history.entries) - 1

	m.requestHistoryPanelRender(true, true)

	// Allocate the evidence container for this turn. Auto-expanded and
	// auto-scrolling to the tail during streaming.
	m.turnEvidence = &turnEvidence{expanded: true, autoTail: true}

	// Collapse all past evidence containers so the viewport stays clean
	// during the new turn.
	for i := range m.pastTurnContainers {
		m.pastTurnContainers[i].expanded = false
	}
}

func (m *Model) activeAgentHistoryEntry() (int, *historyEntry, bool) {
	if !m.currentTurnHistoryActive {
		return 0, nil, false
	}

	idx := m.currentTurnHistoryIndex
	if idx < 0 || idx >= len(m.history.entries) {
		return 0, nil, false
	}

	entry := &m.history.entries[idx]
	if entry.sender != "agent" || !entry.inProgress {
		return 0, nil, false
	}

	return idx, entry, true
}

func (m *Model) recordToolTraceInHistory(step int, text, intent string, running bool, kind string, outcome string, auditEvents []historyAuditEvent) {
	line := firstToolTraceLine(text)
	if line == "" && len(auditEvents) == 0 {
		return
	}

	if _, entry, ok := m.activeAgentHistoryEntry(); ok {
		if line != "" {
			entry.upsertToolCall(step, line, intent, running, kind, outcome)
		}

		entry.auditEvents = append(entry.auditEvents, auditEvents...)
		entry.summary = localFallbackSummary(entry)

		m.requestHistoryPanelRender(false, true)
	} else {
		m.currentTurnTools = append(m.currentTurnTools, line)
	}

	// Evidence container side-effect: populate turnEvidence from the same
	// event stream so the history panel and evidence container stay in sync.
	if m.turnEvidence == nil {
		return
	}

	if running {
		// Tool request: create a new evidence row.
		row := evidenceRow{
			step:      step,
			toolName:  parseToolNameFromTrace(text),
			target:    parseTargetFromTrace(text),
			intent:    strings.TrimSpace(intent),
			auditKind: strings.TrimSpace(kind),
			running:   true,
			glyph:     "…",
		}

		m.turnEvidence.tools = append(m.turnEvidence.tools, row)

		if m.turnEvidence.autoTail {
			m.turnEvidence.scrollOff = max(0, len(m.turnEvidence.tools)-15)
		}
	} else {
		// Tool result: update the existing row by step.
		for i := range m.turnEvidence.tools {
			if m.turnEvidence.tools[i].step == step && m.turnEvidence.tools[i].running {
				m.turnEvidence.tools[i].outcome = strings.TrimSpace(outcome)
				m.turnEvidence.tools[i].summary = line
				m.turnEvidence.tools[i].running = false
				m.turnEvidence.tools[i].glyph = resolveEvidenceGlyph(outcome, false)

				break
			}
		}
	}
}

func (entry *historyEntry) upsertToolCall(step int, line, intent string, running bool, kind string, outcome string) {
	if step > 0 {
		for i := range entry.toolCalls {
			if entry.toolCalls[i].step == step {
				entry.toolCalls[i].line = line
				if strings.TrimSpace(intent) != "" {
					entry.toolCalls[i].intent = strings.TrimSpace(intent)
				}

				if strings.TrimSpace(kind) != "" {
					entry.toolCalls[i].kind = strings.TrimSpace(kind)
				}

				if strings.TrimSpace(outcome) != "" {
					entry.toolCalls[i].outcome = strings.TrimSpace(outcome)
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
		kind:    strings.TrimSpace(kind),
		outcome: strings.TrimSpace(outcome),
		running: running,
	})
}

func (m *Model) recordHistoryAuditEvent(event historyAuditEvent) {
	event = cleanHistoryAuditEvent(event)
	if event.label == "" {
		return
	}

	if _, entry, ok := m.activeAgentHistoryEntry(); ok {
		entry.auditEvents = append(entry.auditEvents, event)
		entry.summary = localFallbackSummary(entry)

		m.requestHistoryPanelRender(false, true)
	}
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

		m.requestHistoryPanelRender(true, true)

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

		m.requestHistoryPanelRender(true, true)

		return
	}

	m.currentTurnTools = nil
}

func (m *Model) removeEmptyCurrentHistoryEntry() {
	idx := m.currentTurnHistoryIndex
	if idx < 0 || idx >= len(m.history.entries) {
		return
	}

	m.history.entries = append(m.history.entries[:idx], m.history.entries[idx+1:]...)
	if m.history.selected >= len(m.history.entries) {
		m.history.selected = len(m.history.entries) - 1
	}

	if m.history.selected < 0 {
		m.history.selected = 0
	}

	m.requestHistoryPanelRender(true, false)
}

func (m *Model) requestHistoryPanelRender(force bool, stickBottom bool) {
	if m == nil {
		return
	}

	if stickBottom {
		m.history.renderScrollBottom = true
	}

	if !m.history.panelOpen || m.panelTab != panelTabHistory {
		return
	}

	if force || !m.streaming || !m.currentTurnHistoryActive || m.history.focused {
		m.flushHistoryPanelRender()

		return
	}

	if m.history.renderLast.IsZero() || time.Since(m.history.renderLast) >= historyPanelRenderThrottle {
		m.flushHistoryPanelRender()

		return
	}

	if m.history.renderPending {
		return
	}

	m.history.renderPending = true

	delay := historyPanelRenderThrottle - time.Since(m.history.renderLast)

	if delay < 0 {
		delay = historyPanelRenderThrottle
	}

	go func() {
		time.Sleep(delay)

		m.events <- historyPanelRefreshMsg{}
	}()
}

func (m *Model) flushHistoryPanelRender() {
	if m == nil {
		return
	}

	m.history.renderPending = false
	m.renderHistoryPanelContent()

	if m.history.panelOpen && m.history.renderScrollBottom {
		m.history.viewport.GotoBottom()
	}

	m.history.renderScrollBottom = false
	m.history.renderLast = time.Now()
}

// localFallbackSummary produces a short readable summary without hitting any
// model. Used as the initial summary so the panel never shows bare "…".
func localFallbackSummary(entry *historyEntry) string {
	const maxSummary = 60

	if entry.inProgress && len(entry.contents) == 0 && len(entry.toolCalls) == 0 {
		if summary := historyAuditSummary(entry.auditEvents); summary != "" {
			return truncateRunes(summary, maxSummary)
		}

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

	if summary := historyAuditSummary(entry.auditEvents); summary != "" {
		return truncateRunes(summary, maxSummary)
	}

	return "…"
}

func historyAuditSummary(events []historyAuditEvent) string {
	for _, event := range events {
		if util.EqualTrimFold(event.kind, "privacy") {
			continue
		}

		label := strings.TrimSpace(event.label)
		if label == "" {
			continue
		}

		kind := strings.TrimSpace(event.kind)
		if kind == "" {
			return label
		}

		return kind + ": " + label
	}

	return ""
}

// resolveEvidenceGlyph maps a tool result outcome string to a display glyph.
func resolveEvidenceGlyph(outcome string, hasError bool) string {
	if hasError {
		return "✘"
	}

	o := strings.ToLower(strings.TrimSpace(outcome))
	switch {
	case o == "":
		return "✔"
	case strings.HasPrefix(o, "failed"):
		return "✘"
	case strings.HasPrefix(o, "completed"), strings.HasPrefix(o, "ok"):
		return "✔"
	case strings.HasPrefix(o, "denied"), strings.HasPrefix(o, "skipped"):
		return "◌"
	default:
		return "✔"
	}
}

// parseToolNameFromTrace extracts the tool name from a toolTraceMsg text line.
// Tool trace lines have the form "tool_name | running ..." or "tool_name | result ...".
func parseToolNameFromTrace(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, " | "); idx > 0 {
		return strings.TrimSpace(text[:idx])
	}

	return text
}

// parseTargetFromTrace extracts the target (path, command, etc.) from a
// toolTraceMsg text line. The target follows " | running " or " | ".
func parseTargetFromTrace(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, " | running "); idx > 0 {
		return strings.TrimSpace(text[idx+len(" | running "):])
	}

	if idx := strings.Index(text, " | "); idx > 0 {
		return strings.TrimSpace(text[idx+len(" | "):])
	}

	return text
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

	if len(entry.auditEvents) > 0 {
		events := make([]string, 0, len(entry.auditEvents))
		for _, event := range entry.auditEvents {
			if text := event.auditLine(); text != "" {
				events = append(events, text)
			}
		}

		if len(events) > 0 {
			sb.WriteString("Audit: ")
			sb.WriteString(strings.Join(events, "; "))
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
		MaxTokens:          maxTokens,
		Temperature:        0.3,
		EnableThinking:     &thinkingOff,
		ChatTemplateKwargs: true,
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
		m.logError("route_classifier", "start failed: "+err.Error())

		return
	}

	if err := m.summaryController.WaitUntilReady(ctx); err != nil {
		m.logError("route_classifier", "ready wait failed: "+err.Error())
	}
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
