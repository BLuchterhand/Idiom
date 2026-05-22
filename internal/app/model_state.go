package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/mcp"
	"idiom/internal/planexecute"
	"idiom/internal/promptstack"
	"idiom/internal/transcript"
	"idiom/internal/util"
	"idiom/internal/workspace"

	"cmp"
	"encoding/json"
)

// sessionAggregate is rolled forward each turn from runner telemetry so
// the end-of-session summary record can report totals/peaks without
// re-parsing the JSONL log. Updated on the UI goroutine via logRunEvent
// and logToolResult, both of which run sequentially per turn.
type sessionAggregate struct {
	turns            int
	modelCalls       int
	toolCalls        int
	promptTokens     int
	completionTokens int
	totalTokens      int
	peakPromptTokens int
	compactions      int
	toolOutputBytes  map[string]int
	toolCallCounts   map[string]int
	discoveryClasses map[string]int
	discoveryCosts   map[string]int
}

func (a *sessionAggregate) recordModelCall(promptTokens, completionTokens, totalTokens int) {
	a.modelCalls++
	a.promptTokens += promptTokens
	a.completionTokens += completionTokens
	a.totalTokens += totalTokens
	if promptTokens > a.peakPromptTokens {
		a.peakPromptTokens = promptTokens
	}
}

func (a *sessionAggregate) recordToolResult(tool string, outputChars int) {
	a.toolCalls++
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "<unknown>"
	}
	if a.toolOutputBytes == nil {
		a.toolOutputBytes = make(map[string]int)
	}
	if a.toolCallCounts == nil {
		a.toolCallCounts = make(map[string]int)
	}
	a.toolCallCounts[tool]++
	a.toolOutputBytes[tool] += outputChars
}

func (a *sessionAggregate) recordToolRequest(profile workspace.ToolDiscoveryProfile) {
	if !profile.Discovery {
		return
	}
	if a.discoveryClasses == nil {
		a.discoveryClasses = make(map[string]int)
	}
	if a.discoveryCosts == nil {
		a.discoveryCosts = make(map[string]int)
	}
	if class := strings.TrimSpace(profile.Class); class != "" {
		a.discoveryClasses[class]++
	}
	if cost := strings.TrimSpace(profile.Cost); cost != "" {
		a.discoveryCosts[cost]++
	}
}

func (a *sessionAggregate) topToolsByOutput(n int) []transcript.ToolOutputAggregate {
	if len(a.toolOutputBytes) == 0 {
		return nil
	}
	out := make([]transcript.ToolOutputAggregate, 0, len(a.toolOutputBytes))
	for tool, bytes := range a.toolOutputBytes {
		out = append(out, transcript.ToolOutputAggregate{
			Tool:        tool,
			Calls:       a.toolCallCounts[tool],
			OutputChars: bytes,
		})
	}
	slices.SortFunc(out, func(a, b transcript.ToolOutputAggregate) int {
		return -cmp.Compare(a.OutputChars, b.OutputChars)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func discoveryAggregates(counts map[string]int) []transcript.DiscoveryAggregate {
	if len(counts) == 0 {
		return nil
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]transcript.DiscoveryAggregate, 0, len(names))
	for _, name := range names {
		out = append(out, transcript.DiscoveryAggregate{Name: name, Calls: counts[name]})
	}
	return out
}

type Model struct {
	cfg        config.Config
	client     llama.LLMClient
	controller *llama.ServerController
	// plannerSpec + executorSpec are the resolved LlamaServer specs for
	// the planner and executor roles. They mirror cfg.LlamaServer /
	// cfg.Implementer when no .idiom/backends.json declares those roles
	// and reflect the backends.json entry otherwise. Status messages
	// and resolveModelID lookups should read these instead of cfg.* so
	// they stay correct under both wiring sources.
	plannerSpec  config.LlamaServer
	executorSpec config.LlamaServer
	// implementerClient + implementerController are set only when the
	// config has a separate executor/implementer llama-server. Nil when
	// the app runs in single-model mode. Direct implementation turns,
	// plan-execute execution, and write-enabled child work use this route
	// when the executor is reachable; otherwise they fall back to the
	// active model.
	implementerClient     llama.LLMClient
	implementerController *llama.ServerController
	workspace             *workspace.Manager
	mcpRegistry           *mcp.Registry
	runner                *agent.Runner
	events                chan tea.Msg
	eventWaitArmed        bool

	width    int
	height   int
	ready    bool
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	messages                       []llama.ChatMessage
	promptStack                    promptstack.Stack
	composedPrompt                 string
	composedPromptSources          []promptstack.Source
	composedPromptCacheKey         string
	activeProfiles                 []string
	streaming                      bool
	bootingServer                  bool
	serverUp                       bool
	managedServer                  bool
	modelID                        string
	modelDisplayID                 string
	status                         string
	contextError                   string
	runtimeError                   string
	workingFrame                   int
	debugLogger                    *transcript.DebugLogger
	debugLogPath                   string
	sessionStartedAt               time.Time
	sessionAgg                     sessionAggregate
	sessionSummaryWritten          bool
	eventStream                    *jsonEventStream
	lastAssistantReasoningContent  string
	agentModeName                  string
	forcedTaskFamily               string
	suppressPromptTranscript       bool
	plannerOwnedVerification       bool
	maxTokensOverride              int
	disableThinkingOverride        bool
	forcedReadinessOpenObligations []string
	planExecuteActive              bool
	planExecuteScopeContract       planexecute.PlanScopeContract
	modelThinkingFloor             bool
	modelSwapped                   bool
	traceMode                      traceDisplayMode
	planPhaseText                  string

	workspaceOverviewInjected bool

	pinnedFiles *PinnedFileRegistry

	approvalMu          sync.Mutex
	approvalSerial      sync.Mutex
	approvalPending     *pendingApproval
	detailPanelVisible  bool
	modelPopup          modelPopupState
	rollbackHistory     []rollbackEntry
	instructionAttacher *agent.InstructionAttacher
	mdRenderer          *glamour.TermRenderer
	mdRendererWrapWidth int
	messageRenderCache  string
	messageRenderWidth  int
	messageRenderHash   uint64
	messageRenderMD     bool
	perMsgRenderCache   []string
	perMsgRenderHashes  []uint64
	perMsgRenderLen     int

	summaryClient     llama.LLMClient
	summaryController *llama.ServerController

	panelTab      int // panelTabHistory | panelTabPlan
	planViewport  viewport.Model
	planPath      string
	planMTime     time.Time
	planContent   string
	planLoadError string

	plan     planState
	sessions sessionsState
	history  historyState

	// pendingExec carries a tea.Cmd (typically tea.ExecProcess) that a slash
	// command wants to run, but can't dispatch itself because handleSlashCommand
	// doesn't return tea.Cmd. The Update loop consumes and clears this after
	// the slash handler returns.
	pendingExec tea.Cmd

	// Tool-call names collected during the current agent turn; consumed when
	// streamDoneMsg fires and cleared before the next turn begins.
	currentTurnTools         []string
	currentTurnBusyStatus    string
	currentTurnHistoryIndex  int
	currentTurnHistoryActive bool
}

// pinnedFilesRegistry returns the lazy-initialized per-session pin set.
func (m *Model) pinnedFilesRegistry() *PinnedFileRegistry {
	if m == nil {
		return nil
	}
	if m.pinnedFiles == nil {
		m.pinnedFiles = newPinnedFileRegistry()
	}
	return m.pinnedFiles
}

type infoMsg struct {
	text string
}

type historyPanelRefreshMsg struct{}

type toolTraceMsg struct {
	step        int
	text        string
	intent      string
	busyStatus  string
	auditKind   string
	outcome     string
	auditEvents []historyAuditEvent
	running     bool
}

type planRefreshMsg struct{}

type planPhaseMsg struct {
	phase  string
	detail string
}

func formatPlanPhaseText(phase, detail string) string {
	label := ""
	switch strings.TrimSpace(phase) {
	case "planning":
		label = "Planning"
	case "executing":
		label = "Executing"
	case "reviewing":
		label = "Reviewing"
	default:
		label = strings.Title(phase)
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return label + "..."
	}
	return label + ": " + detail
}

type traceDisplayMode string

const (
	traceDisplayCompact traceDisplayMode = "compact"
	traceDisplayDetail  traceDisplayMode = "detail"
)

func (m *Model) Close() {
	m.writeSessionSummary()
	if m.debugLogger != nil {
		_ = m.debugLogger.Close()
	}
	if m.mcpRegistry != nil {
		_ = m.mcpRegistry.Close()
		m.mcpRegistry = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if m.summaryController != nil {
		_ = m.summaryController.Stop(ctx)
	}
	if m.implementerController != nil {
		_ = m.implementerController.Stop(ctx)
	}
	if !m.managedServer {
		return
	}
	if m.controller != nil {
		_ = m.controller.Stop(ctx)
	}
}

func (m *Model) chatOptions() llama.ChatOptions {
	enableThinking := util.ClonePtr(m.cfg.Chat.EnableThinking)
	preserveThinking := m.cfg.Chat.PreserveThinking
	chatTemplateKwargs := false
	if enableThinking != nil && !*enableThinking {
		chatTemplateKwargs = true
	}
	if m.disableThinkingOverride {
		thinkingOff := false
		enableThinking = &thinkingOff
		preserveThinking = false
		chatTemplateKwargs = true
	}
	return llama.ChatOptions{
		Model:              m.modelID,
		Temperature:        m.cfg.Chat.Temperature,
		TopP:               m.cfg.Chat.TopP,
		TopK:               m.cfg.Chat.TopK,
		MinP:               m.cfg.Chat.MinP,
		PresencePenalty:    m.cfg.Chat.PresencePenalty,
		RepetitionPenalty:  m.cfg.Chat.RepetitionPenalty,
		EnableThinking:     enableThinking,
		PreserveThinking:   preserveThinking,
		ChatTemplateKwargs: chatTemplateKwargs,
		ModelThinkingFloor: m.modelThinkingFloor,
		MaxTokens:          m.cfg.Chat.MaxTokens,
	}
}

func (m *Model) samplingSummary() string {
	return samplingSummaryForOptions(m.chatOptions())
}

func samplingSummaryForOptions(opts llama.ChatOptions) string {
	thinking := "auto"
	if opts.EnableThinking != nil {
		if *opts.EnableThinking {
			thinking = "on"
		} else {
			thinking = "off"
		}
	}
	return fmt.Sprintf(
		"temp=%.2f top_p=%.2f top_k=%d min_p=%.2f presence=%.2f repeat=%.2f thinking=%s preserve=%t max_tokens=%d",
		opts.Temperature,
		opts.TopP,
		opts.TopK,
		opts.MinP,
		opts.PresencePenalty,
		opts.RepetitionPenalty,
		thinking,
		opts.PreserveThinking,
		opts.MaxTokens,
	)
}

// resolveModelID picks the model identifier used in chat requests.
// llama-server's /v1/models reports the actually-loaded GGUF, so reported
// is trusted. mlx_lm.server may report default catalog models before the
// configured local model, but chat requests still need the configured model id
// verbatim. Returning a basename for local MLX paths makes the server treat it
// as a Hugging Face repo id.
func resolveModelID(spec config.LlamaServer, reported string) string {
	if spec.IsHosted() {
		if name := strings.TrimSpace(spec.Model); name != "" {
			return name
		}
	}
	if spec.IsMLX() {
		if name := strings.TrimSpace(spec.Model); name != "" {
			return name
		}
	}
	return reported
}

func resolveModelDisplayID(spec config.LlamaServer, reported string) string {
	if name := strings.TrimSpace(spec.DisplayName); name != "" {
		return name
	}
	return resolveModelID(spec, reported)
}

func (m *Model) activeModelDisplayID() string {
	if m == nil {
		return ""
	}
	return util.Fallback(strings.TrimSpace(m.modelDisplayID), strings.TrimSpace(m.modelID))
}

func (m *Model) bootstrap() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// Kick off the implementer server in parallel when configured; its
	// readiness is independent of the planner and failures here must not
	// block main-server bootstrap. Child tasks silently fall back to the
	// planner client when the implementer isn't ready yet.
	if m.implementerController != nil {
		go m.startImplementerAsync()
	}

	if m.plannerSpec.IsHosted() {
		modelID := resolveModelID(m.plannerSpec, m.plannerSpec.Model)
		m.events <- serverStatusMsg{running: true, modelID: modelID, displayID: resolveModelDisplayID(m.plannerSpec, m.plannerSpec.Model)}
		return
	}

	models, err := m.client.ListModels(ctx)
	if err == nil && len(models) > 0 {
		modelID := resolveModelID(m.plannerSpec, models[0].ID)
		m.events <- serverStatusMsg{running: true, modelID: modelID, displayID: resolveModelDisplayID(m.plannerSpec, models[0].ID)}
		return
	}

	if m.cfg.Chat.AutoStartServer && m.controller != nil {
		m.startServerAsync()
		return
	}

	m.events <- infoMsg{text: "No server detected. Use /server start or Ctrl+R."}
}

// startImplementerAsync brings up the secondary (implementer) llama-server
// when configured. Runs detached from main bootstrap: on failure we just
// log, child tasks fall back to the planner client. Assumes the caller
// already checked m.implementerController != nil.
func (m *Model) startImplementerAsync() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.ServerStartTimeoutSecs)*time.Second)
	defer cancel()

	if models, err := m.implementerClient.ListModels(ctx); err == nil && len(models) > 0 {
		m.events <- infoMsg{text: "Implementer ready: " + resolveModelDisplayID(m.executorSpec, models[0].ID)}
		return
	}
	if !m.cfg.Chat.AutoStartServer {
		return
	}
	if err := m.implementerController.Start(ctx); err != nil {
		m.logError("implementer", "start failed: "+err.Error())
		return
	}
	if err := m.implementerController.WaitUntilReady(ctx); err != nil {
		m.logError("implementer", "ready wait failed: "+err.Error())
		return
	}
	models, err := m.implementerClient.ListModels(context.Background())
	id := "local-model"
	if err == nil && len(models) > 0 {
		id = models[0].ID
	}
	m.events <- infoMsg{text: "Implementer ready: " + resolveModelDisplayID(m.executorSpec, id)}
}

func (m *Model) handleSlashCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return false
	}

	handler, ok := slashCommands[fields[0]]
	if !ok {
		m.status = "Unknown command. Use /help."
		return false
	}
	return handler(m, raw, fields)
}

func (m *Model) startServerAsync() {
	if m.controller == nil {
		m.status = fmt.Sprintf("Backend %s is hosted at %s; local server controls are unavailable.", m.plannerSpec.Kind, m.plannerSpec.EndpointBaseURL())
		return
	}
	if m.bootingServer {
		return
	}
	m.bootingServer = true
	m.status = "Starting llama-server."

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.ServerStartTimeoutSecs)*time.Second)
		defer cancel()

		if err := m.controller.Start(ctx); err != nil {
			m.events <- serverStatusMsg{err: err}
			return
		}
		if err := m.controller.WaitUntilReady(ctx); err != nil {
			m.events <- serverStatusMsg{err: err}
			return
		}

		models, err := m.client.ListModels(context.Background())
		if err != nil || len(models) == 0 {
			modelID := resolveModelID(m.plannerSpec, "local-model")
			m.events <- serverStatusMsg{running: true, modelID: modelID, displayID: resolveModelDisplayID(m.plannerSpec, "local-model"), owned: true}
			return
		}

		modelID := resolveModelID(m.plannerSpec, models[0].ID)
		m.events <- serverStatusMsg{running: true, modelID: modelID, displayID: resolveModelDisplayID(m.plannerSpec, models[0].ID), owned: true}
	}()
}

func (m *Model) stopServerAsync() {
	if m.controller == nil {
		m.status = fmt.Sprintf("Backend %s is hosted at %s; local server controls are unavailable.", m.plannerSpec.Kind, m.plannerSpec.EndpointBaseURL())
		return
	}
	m.status = "Stopping llama-server."

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		err := m.controller.Stop(ctx)
		m.events <- serverStatusMsg{running: false, err: err}
	}()
}

func (m *Model) restartServerAsync() {
	if m.controller == nil {
		m.status = fmt.Sprintf("Backend %s is hosted at %s; local server controls are unavailable.", m.plannerSpec.Kind, m.plannerSpec.EndpointBaseURL())
		return
	}
	m.bootingServer = true
	m.status = "Restarting llama-server."

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.ServerStartTimeoutSecs)*time.Second)
		defer cancel()

		if err := m.controller.Restart(ctx); err != nil {
			m.events <- serverStatusMsg{err: err}
			return
		}
		if err := m.controller.WaitUntilReady(ctx); err != nil {
			m.events <- serverStatusMsg{err: err}
			return
		}

		models, err := m.client.ListModels(context.Background())
		if err != nil || len(models) == 0 {
			modelID := resolveModelID(m.plannerSpec, "local-model")
			m.events <- serverStatusMsg{running: true, modelID: modelID, displayID: resolveModelDisplayID(m.plannerSpec, "local-model"), owned: true}
			return
		}
		modelID := resolveModelID(m.plannerSpec, models[0].ID)
		m.events <- serverStatusMsg{running: true, modelID: modelID, displayID: resolveModelDisplayID(m.plannerSpec, models[0].ID), owned: true}
	}()
}

// renderHistoryPanelContent rebuilds the history viewport content from
// m.history.entries. Call whenever entry data or selection changes.
func (m *Model) renderHistoryPanelContent() {
	if !m.history.panelOpen {
		return
	}
	w := max(8, m.history.viewport.Width)

	var b strings.Builder
	b.WriteString(panelTitleStyle.Width(w).Render("◈ History"))
	b.WriteString("\n")

	for i, entry := range m.history.entries {
		selected := i == m.history.selected
		expanded := selected && (m.history.expanded || entry.inProgress)

		prefix := "  "
		if selected && expanded {
			prefix = "▾ "
		} else if selected {
			prefix = "▸ "
		}

		who := "You"
		if entry.sender == "agent" {
			who = "Agent"
		}
		head := fmt.Sprintf("%s%d %s · ", prefix, i+1, who)
		indent := strings.Repeat(" ", len([]rune(head)))

		var style lipgloss.Style
		switch {
		case selected && m.history.focused:
			style = panelFocusedStyle
		case selected:
			style = panelUnfocusedStyle
		default:
			style = panelEntryStyle
		}
		for j, wl := range wrapLines(entry.summary, max(1, w-len([]rune(head)))) {
			row := indent + wl
			if j == 0 {
				row = head + wl
			}
			b.WriteString(style.Width(w).Render(row))
			b.WriteString("\n")
		}

		if expanded {
			for _, c := range entry.contents {
				for _, wl := range wrapLines(c, max(1, w-2)) {
					b.WriteString(panelDetailStyle.Width(w).Render(wl))
					b.WriteString("\n")
				}
			}
			for _, event := range entry.auditEvents {
				line := event.auditLine()
				if line == "" {
					continue
				}
				for j, wl := range wrapLines(line, max(1, w-4)) {
					prefix := "- "
					if j > 0 {
						prefix = "  "
					}
					b.WriteString(panelDetailStyle.Width(w).Render(prefix + wl))
					b.WriteString("\n")
				}
			}
			for _, tc := range entry.toolCalls {
				for j, wl := range wrapLines(tc.line, max(1, w-4)) {
					prefix := "▶ "
					if tc.running {
						prefix = "… "
					}
					if j > 0 {
						prefix = "  "
					}
					b.WriteString(panelDetailStyle.Width(w).Render(prefix + wl))
					b.WriteString("\n")
				}
				meta := joinNonEmpty(" · ", keyValue("type", tc.kind), keyValue("outcome", tc.outcome))
				if meta != "" {
					for _, wl := range wrapLines(meta, max(1, w-4)) {
						b.WriteString(panelDetailStyle.Width(w).Render("  " + wl))
						b.WriteString("\n")
					}
				}
				if strings.TrimSpace(tc.intent) != "" {
					for _, wl := range wrapLines("reason: "+tc.intent, max(1, w-4)) {
						b.WriteString(panelDetailStyle.Width(w).Render("  " + wl))
						b.WriteString("\n")
					}
				}
			}
			b.WriteString("\n")
		}
	}

	if len(m.history.entries) == 0 {
		b.WriteString(panelDetailStyle.Width(w).Render("No history yet."))
		b.WriteString("\n")
		b.WriteString(panelDetailStyle.Width(w).Render("Entries appear as"))
		b.WriteString("\n")
		b.WriteString(panelDetailStyle.Width(w).Render("messages are sent."))
	}

	m.history.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m *Model) resize() {
	start := time.Now()
	defer m.logUISlowPath("ui.resize", start, fmt.Sprintf("panel_open=%t tab=%d size=%dx%d", m.history.panelOpen, m.panelTab, m.width, m.height))

	headerHeight := 1
	dividerCount := 2 // one after header, one before input
	m.input.SetWidth(m.width)
	inputHeight := m.syncInputHeight()
	footerHeight := m.footerLineCount()
	contentHeight := max(m.height-headerHeight-dividerCount-inputHeight-footerHeight, 5)

	var mainWidth, panelWidth int
	if m.history.panelOpen {
		panelWidth = m.width / 3
		mainWidth = max(24, m.width-panelWidth-1) // 1 for the │ divider
	} else {
		mainWidth = max(24, m.width)
	}

	if !m.ready {
		m.viewport = viewport.New(mainWidth, contentHeight)
		m.ready = true
	}
	mainWidthChanged := m.viewport.Width != mainWidth
	m.viewport.Width = mainWidth
	m.viewport.Height = contentHeight

	if m.history.panelOpen {
		// Reserve 1 row for the tab header strip above the active viewport.
		panelContentHeight := max(3, contentHeight-1)
		m.history.viewport.Width = panelWidth
		m.history.viewport.Height = panelContentHeight
		m.planViewport.Width = panelWidth
		m.planViewport.Height = panelContentHeight
		m.sessions.viewport.Width = panelWidth
		m.sessions.viewport.Height = panelContentHeight
	}

	m.ensureMarkdownRenderer(max(20, mainWidth-2))
	if mainWidthChanged {
		m.invalidateMessageRenderCache()
	}
	m.renderMessages(false)
	if m.history.panelOpen {
		m.rerenderActivePanel()
	}
}

func (m *Model) syncInputHeight() int {
	height := m.desiredInputHeight()
	m.input.SetHeight(height)
	return height
}

func (m *Model) desiredInputHeight() int {
	const maxInputHeight = 8
	width := max(1, m.input.Width())
	value := m.input.Value()
	rows := 1
	if value != "" {
		rows = 0
		for _, line := range strings.Split(value, "\n") {
			rows += max(1, len(wrapLines(line, width)))
		}
	}
	limit := maxInputHeight
	if m.height > 0 {
		limit = min(limit, max(1, m.height/3))
	}
	return min(max(1, rows), limit)
}

func (m *Model) footerLineCount() int {
	count := 0
	if m.currentApproval() != nil {
		count++
	}
	if m.errorBanner() != "" {
		count++
	}
	return count
}

func (m *Model) ensureMarkdownRenderer(wrapWidth int) {
	if wrapWidth < 20 {
		wrapWidth = 20
	}
	if m.mdRenderer != nil && m.mdRendererWrapWidth == wrapWidth {
		return
	}
	m.mdRenderer, _ = glamour.NewTermRenderer(
		glamour.WithStyles(mochaStyleConfig),
		glamour.WithWordWrap(wrapWidth),
	)
	m.mdRendererWrapWidth = wrapWidth
	m.invalidateMessageRenderCache()
}

func (m *Model) invalidateMessageRenderCache() {
	m.messageRenderCache = ""
	m.messageRenderWidth = 0
	m.messageRenderHash = 0
	m.messageRenderMD = false
	m.perMsgRenderCache = nil
	m.perMsgRenderHashes = nil
	m.perMsgRenderLen = 0
}

func (m *Model) setHistoryPanelOpen(open bool) {
	start := time.Now()
	defer m.logUISlowPath("ui.toggle_panel", start, fmt.Sprintf("open=%t tab=%d", open, m.panelTab))

	if m.history.panelOpen == open && m.ready {
		if open {
			m.rerenderActivePanel()
		}
		return
	}
	m.history.panelOpen = open
	m.history.focused = false
	if !open {
		m.history.renderPending = false
		m.history.renderScrollBottom = false
	}
	if open && len(m.history.entries) > 0 {
		m.history.selected = len(m.history.entries) - 1
	}
	if open && m.panelTab == panelTabSessions {
		m.refreshSessionsList()
	}
	m.resize()
}

func (m *Model) logUISlowPath(name string, start time.Time, detail string) {
	if m == nil {
		return
	}
	duration := time.Since(start)
	if duration < 50*time.Millisecond {
		return
	}
	content := fmt.Sprintf("%s duration_ms=%d", name, duration.Milliseconds())
	if strings.TrimSpace(detail) != "" {
		content += " " + strings.TrimSpace(detail)
	}
	m.logMessage("ui_trace", content)
}

func (m *Model) workingDots() string {
	return strings.Repeat(".", m.workingFrame)
}

func (m *Model) contextSummary() string {
	sources := m.systemPromptSources()
	if len(sources) == 0 {
		return "none"
	}

	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, source.DisplayName)
	}
	return strings.Join(parts, ", ")
}

func (m *Model) displayPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	clean := filepath.Clean(path)
	root := ""
	if m.workspace != nil {
		root = strings.TrimSpace(m.workspace.Root())
	}
	if root != "" {
		root = filepath.Clean(root)
		if filepath.IsAbs(clean) {
			if rel, err := filepath.Rel(root, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if rel == "." {
					return "."
				}
				return "./" + filepath.ToSlash(rel)
			}
		}
	}

	if filepath.IsAbs(clean) {
		return clean
	}
	if clean == "." {
		return "."
	}
	clean = filepath.ToSlash(clean)
	if strings.HasPrefix(clean, "./") || strings.HasPrefix(clean, "../") {
		return clean
	}
	return "./" + clean
}

func (m *Model) workspaceSummary() string {
	if m.workspace == nil || !m.workspace.Enabled() {
		return "disabled"
	}
	return fmt.Sprintf("root=%s cwd=%s", m.displayPath(m.workspace.Root()), m.displayPath(m.workspace.CWD()))
}

func (m *Model) modeSummary() string {
	mode := m.currentMode()
	return "agent=" + mode.Name
}

func (m *Model) traceSummary() string {
	return string(m.traceMode)
}

// applyModePermissions is a hook for /mode to refresh any workspace-level
// policy tied to the active Mode. Runner step-cap is rebuilt from the
// current Mode on every new turn via runAgentTurn.
func (m *Model) applyModePermissions(mode agent.Mode) {
	if m == nil || m.workspace == nil {
		return
	}
	m.workspace.SetPermissionRules(mode.PermissionRules)
}

func firstToolTraceLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	return strings.TrimSpace(text)
}

func (m *Model) pendingAssistantIndex() int {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" && strings.TrimSpace(m.messages[i].Content) == "" {
			return i
		}
	}
	return -1
}

func (m *Model) insertBeforePendingAssistant(message llama.ChatMessage) {
	idx := m.pendingAssistantIndex()
	if idx == -1 {
		m.messages = append(m.messages, message)
		return
	}
	m.messages = append(m.messages[:idx], append([]llama.ChatMessage{message}, m.messages[idx:]...)...)
}

func (m *Model) setPendingAssistantMessage(message llama.ChatMessage) {
	message.Role = util.Fallback(strings.TrimSpace(message.Role), "assistant")
	idx := m.pendingAssistantIndex()
	if idx == -1 {
		m.messages = append(m.messages, message)
		return
	}
	m.messages[idx] = message
}

func (m *Model) appendPendingAssistantContent(content string) {
	idx := m.pendingAssistantIndex()
	if idx == -1 {
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: content})
		return
	}
	m.messages[idx].Content += content
}

func (m *Model) removePendingAssistant() {
	idx := m.pendingAssistantIndex()
	if idx == -1 {
		return
	}
	m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
}

func (m *Model) contextSourceNames() []string {
	sources := m.systemPromptSources()
	if len(sources) == 0 {
		return nil
	}

	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.DisplayName)
	}
	return names
}

func (m *Model) logMessage(role, content string) {
	if m != nil && m.suppressPromptTranscript {
		if m.debugLogger != nil {
			redacted := content
			switch strings.TrimSpace(role) {
			case "user", "assistant":
				redacted = fmt.Sprintf("suppressed nested %s message (%d chars)", strings.TrimSpace(role), len(content))
			}
			_ = m.debugLogger.LogRunEvent("internal_"+strings.TrimSpace(role), redacted)
		}
		return
	}
	m.emitJSONMessage(role, content)
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogMessage(role, content)
}

func (m *Model) takeLastAssistantReasoningContent() string {
	reasoning := strings.TrimSpace(m.lastAssistantReasoningContent)
	m.lastAssistantReasoningContent = ""
	return reasoning
}

func (m *Model) logToolRequest(event agent.ToolRequestEvent) {
	m.emitJSONToolRequest(event)
	profile := workspace.DiscoveryProfileForRequest(event.Request)
	if event.DiscoveryClass != "" {
		profile.Class = event.DiscoveryClass
	}
	if event.DiscoveryScope != "" {
		profile.Scope = event.DiscoveryScope
	}
	if event.DiscoveryCost != "" {
		profile.Cost = event.DiscoveryCost
	}
	if !profile.Discovery {
		profile = workspace.ToolDiscoveryProfile{}
	}
	m.sessionAgg.recordToolRequest(profile)
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogToolRequestDetail(transcript.ToolRequestDetail{
		Step:           event.Step,
		Agent:          event.Agent,
		Raw:            event.Raw,
		Tool:           event.Request.Tool,
		WorkspaceMode:  string(event.Mode),
		Broad:          event.Broad,
		Narrowing:      event.Narrowing,
		Action:         event.Action,
		DiscoveryClass: profile.Class,
		DiscoveryScope: profile.Scope,
		DiscoveryCost:  profile.Cost,
	})
}

func (m *Model) logToolResult(event agent.ToolResultEvent) {
	m.emitJSONToolResult(event)
	output := event.Result.Output
	chars := len(output)
	lines := 0
	if chars > 0 {
		lines = strings.Count(output, "\n")
		if !strings.HasSuffix(output, "\n") {
			lines++
		}
	}
	m.sessionAgg.recordToolResult(event.Result.Tool, chars)
	if m.debugLogger == nil {
		return
	}
	profile := profileForToolResult(event)
	_ = m.debugLogger.LogToolResultDetail(transcript.ToolResultDetail{
		Step:           event.Step,
		Agent:          event.Agent,
		Tool:           event.Result.Tool,
		Summary:        event.Result.Summary,
		Error:          event.Result.Error,
		OutputChars:    chars,
		OutputLines:    lines,
		Path:           strings.TrimSpace(event.Request.Path),
		StartLine:      event.Request.StartLine,
		EndLine:        event.Request.EndLine,
		DiscoveryClass: profile.Class,
		DiscoveryScope: profile.Scope,
		DiscoveryCost:  profile.Cost,
		ExtraFields:    workspace.ToolTelemetryFields(event.Result.Tool, event.Result.Output),
	})
}

func profileForToolResult(event agent.ToolResultEvent) workspace.ToolDiscoveryProfile {
	req := event.Request
	if strings.TrimSpace(req.Tool) == "" {
		req.Tool = event.Result.Tool
	}
	profile := workspace.DiscoveryProfileForRequest(req)
	if !profile.Discovery {
		return workspace.ToolDiscoveryProfile{}
	}
	return profile
}

func (m *Model) logRunEvent(event agent.RunEvent) {
	if audit, ok := historyAuditEventFromRunEvent(event); ok {
		m.enqueueHistoryAuditEvent(audit)
	}
	switch event.Role {
	case "model_call":
		var payload struct {
			Step             int            `json:"step"`
			Label            string         `json:"label"`
			Agent            string         `json:"agent"`
			PromptTokens     int            `json:"prompt_tokens"`
			CompletionTokens int            `json:"completion_tokens"`
			TotalTokens      int            `json:"total_tokens"`
			CtxSize          int            `json:"ctx_size"`
			MessageCount     int            `json:"message_count"`
			RoleChars        map[string]int `json:"role_chars"`
			ChosenTool       string         `json:"chosen_tool"`
			DurationMillis   int64          `json:"duration_ms"`
		}
		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			m.sessionAgg.recordModelCall(payload.PromptTokens, payload.CompletionTokens, payload.TotalTokens)
			if m.debugLogger != nil {
				_ = m.debugLogger.LogModelCall(transcript.ModelCall{
					Step:             payload.Step,
					Agent:            util.FirstNonEmpty(payload.Agent, payload.Label),
					PromptTokens:     payload.PromptTokens,
					CompletionTokens: payload.CompletionTokens,
					TotalTokens:      payload.TotalTokens,
					CtxSize:          payload.CtxSize,
					MessageCount:     payload.MessageCount,
					RoleChars:        payload.RoleChars,
					ChosenTool:       payload.ChosenTool,
					DurationMillis:   payload.DurationMillis,
				})
			}
		}
		// Skip the legacy LogMessage path; the typed record replaces it.
		return
	case "completion_status":
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			payload["type"] = "completion_status"
			m.emitJSONEvent(payload)
		}
		if m.debugLogger != nil {
			_ = m.debugLogger.LogMessage(event.Role, event.Content)
		}
		return
	case "budget_ledger":
		var payload struct {
			Lane                     string `json:"lane"`
			UsedModelCalls           int    `json:"used_model_calls"`
			MaxModelCalls            int    `json:"max_model_calls"`
			UsedToolResults          int    `json:"used_tool_results"`
			MaxToolSteps             int    `json:"max_tool_steps"`
			PromptTokens             int    `json:"prompt_tokens"`
			CompletionTokens         int    `json:"completion_tokens"`
			TotalTokens              int    `json:"total_tokens"`
			ProgressTurns            int    `json:"progress_turns"`
			RejectedActions          int    `json:"rejected_actions"`
			CompletionGateRejections int    `json:"completion_gate_rejections"`
			ContextCompactions       int    `json:"context_compactions"`
			DurationMillis           int64  `json:"duration_millis"`
			StopReason               string `json:"stop_reason"`
			BudgetExhausted          bool   `json:"budget_exhausted"`
		}
		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			m.sessionAgg.turns++
			m.sessionAgg.compactions += payload.ContextCompactions
			if m.debugLogger != nil {
				_ = m.debugLogger.LogTurnSummary(transcript.TurnSummary{
					Lane:                     payload.Lane,
					UsedModelCalls:           payload.UsedModelCalls,
					MaxModelCalls:            payload.MaxModelCalls,
					UsedToolResults:          payload.UsedToolResults,
					MaxToolSteps:             payload.MaxToolSteps,
					PromptTokens:             payload.PromptTokens,
					CompletionTokens:         payload.CompletionTokens,
					TotalTokens:              payload.TotalTokens,
					ProgressTurns:            payload.ProgressTurns,
					RejectedActions:          payload.RejectedActions,
					CompletionGateRejections: payload.CompletionGateRejections,
					ContextCompactions:       payload.ContextCompactions,
					DurationMillis:           payload.DurationMillis,
					StopReason:               payload.StopReason,
					BudgetExhausted:          payload.BudgetExhausted,
				})
			}
		}
		return
	case "phase":
		var payload struct {
			Lane   string `json:"lane"`
			Phase  string `json:"phase"`
			State  string `json:"state"`
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			if m.debugLogger != nil {
				_ = m.debugLogger.LogPhase(payload.Lane, payload.Phase, payload.State, payload.Detail)
			}
		}
		return
	case "child_tool_request":
		if m.logNestedToolRequest(event.Content) {
			return
		}
	case "child_tool_result":
		if m.logNestedToolResult(event.Content) {
			return
		}
	}
	if shouldLogRunnerEventAsRunEvent(event.Role) {
		if m.debugLogger != nil {
			_ = m.debugLogger.LogRunEvent(event.Role, event.Content)
		}
		return
	}
	m.logMessage(event.Role, event.Content)
}

func shouldLogRunnerEventAsRunEvent(role string) bool {
	return strings.TrimSpace(role) != ""
}

func (m *Model) logNestedToolRequest(content string) bool {
	var payload agent.ToolRequestEvent
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return false
	}
	profile := workspace.DiscoveryProfileForRequest(payload.Request)
	if payload.DiscoveryClass != "" {
		profile.Class = payload.DiscoveryClass
	}
	if payload.DiscoveryScope != "" {
		profile.Scope = payload.DiscoveryScope
	}
	if payload.DiscoveryCost != "" {
		profile.Cost = payload.DiscoveryCost
	}
	if !profile.Discovery {
		profile = workspace.ToolDiscoveryProfile{}
	}
	m.sessionAgg.recordToolRequest(profile)
	if m.debugLogger != nil {
		_ = m.debugLogger.LogToolRequestDetail(transcript.ToolRequestDetail{
			Step:           payload.Step,
			Agent:          payload.Agent,
			Raw:            payload.Raw,
			Tool:           payload.Request.Tool,
			WorkspaceMode:  string(payload.Mode),
			Broad:          payload.Broad,
			Narrowing:      payload.Narrowing,
			Action:         payload.Action,
			DiscoveryClass: profile.Class,
			DiscoveryScope: profile.Scope,
			DiscoveryCost:  profile.Cost,
		})
	}
	return true
}

func (m *Model) logNestedToolResult(content string) bool {
	var payload agent.ToolResultEvent
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return false
	}
	output := payload.Result.Output
	chars := len(output)
	lines := 0
	if chars > 0 {
		lines = strings.Count(output, "\n")
		if !strings.HasSuffix(output, "\n") {
			lines++
		}
	}
	m.sessionAgg.recordToolResult(payload.Result.Tool, chars)
	if m.debugLogger != nil {
		profile := profileForToolResult(payload)
		_ = m.debugLogger.LogToolResultDetail(transcript.ToolResultDetail{
			Step:           payload.Step,
			Agent:          payload.Agent,
			Tool:           payload.Result.Tool,
			Summary:        payload.Result.Summary,
			Error:          payload.Result.Error,
			OutputChars:    chars,
			OutputLines:    lines,
			Path:           strings.TrimSpace(payload.Request.Path),
			StartLine:      payload.Request.StartLine,
			EndLine:        payload.Request.EndLine,
			DiscoveryClass: profile.Class,
			DiscoveryScope: profile.Scope,
			DiscoveryCost:  profile.Cost,
			ExtraFields:    workspace.ToolTelemetryFields(payload.Result.Tool, payload.Result.Output),
		})
	}
	return true
}

func (m *Model) writeSessionSummary() {
	if m.sessionSummaryWritten {
		return
	}
	m.sessionSummaryWritten = true
	ctxSize := m.cfg.LlamaServer.CtxSize
	peakPct := 0.0
	if ctxSize > 0 && m.sessionAgg.peakPromptTokens > 0 {
		peakPct = float64(m.sessionAgg.peakPromptTokens) / float64(ctxSize)
	}
	duration := int64(0)
	if !m.sessionStartedAt.IsZero() {
		duration = time.Since(m.sessionStartedAt).Milliseconds()
	}
	if m.debugLogger != nil {
		_ = m.debugLogger.LogSessionSummary(transcript.SessionSummary{
			Turns:            m.sessionAgg.turns,
			ModelCalls:       m.sessionAgg.modelCalls,
			ToolCalls:        m.sessionAgg.toolCalls,
			PromptTokens:     m.sessionAgg.promptTokens,
			CompletionTokens: m.sessionAgg.completionTokens,
			TotalTokens:      m.sessionAgg.totalTokens,
			PeakPromptTokens: m.sessionAgg.peakPromptTokens,
			CtxSize:          ctxSize,
			PeakCtxPct:       peakPct,
			Compactions:      m.sessionAgg.compactions,
			TopToolsByOutput: m.sessionAgg.topToolsByOutput(10),
			DiscoveryByClass: discoveryAggregates(m.sessionAgg.discoveryClasses),
			DiscoveryByCost:  discoveryAggregates(m.sessionAgg.discoveryCosts),
			DurationMillis:   duration,
		})
	}
	// Mirror to the stdout JSON event stream so wrappers (idiom lane,
	// queue, etc.) can correlate aggregate token usage with the
	// dispatch they spawned. Only fires when --json is on; debug-log
	// path above is the always-on persistence layer.
	m.emitJSONEvent(map[string]any{
		"type":               "session_summary",
		"turns":              m.sessionAgg.turns,
		"model_calls":        m.sessionAgg.modelCalls,
		"tool_calls":         m.sessionAgg.toolCalls,
		"prompt_tokens":      m.sessionAgg.promptTokens,
		"completion_tokens":  m.sessionAgg.completionTokens,
		"total_tokens":       m.sessionAgg.totalTokens,
		"peak_prompt_tokens": m.sessionAgg.peakPromptTokens,
		"ctx_size":           ctxSize,
		"peak_ctx_pct":       peakPct,
		"compactions":        m.sessionAgg.compactions,
		"duration_ms":        duration,
	})
}

func (m *Model) logError(kind, message string) {
	m.emitJSONError(kind, message)
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogError(kind, message)
}

func (m *Model) logServerEvent(msg serverStatusMsg) {
	if m.debugLogger == nil {
		return
	}

	errText := ""
	if msg.err != nil {
		errText = msg.err.Error()
	}
	_ = m.debugLogger.LogServer(msg.running, msg.modelID, msg.owned, errText)
}

func (m *Model) logArtifact(kind, path string) {
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogArtifact(kind, path)
}

func (m *Model) setContextError(message string) {
	m.contextError = message
	m.status = message
	m.logError("context", message)
}

func (m *Model) SetForcedTaskFamily(family string) {
	m.forcedTaskFamily = strings.TrimSpace(family)
	// Plan-then-execute planner phase needs the workspace manifest to
	// swap per-edit write tools for submit_plan so the model's tool
	// spec actually exposes the only write the planner is allowed to
	// call. Detect via the canonical task-family constant and toggle
	// the manager flag here, where forced family is set.
	if m.workspace != nil {
		switch m.forcedTaskFamily {
		case "plan_then_execute_planner", "plan_then_execute_review_repair":
			m.workspace.SetPlanThenExecutePlannerMode(true)
		default:
			m.workspace.SetPlanThenExecutePlannerMode(false)
		}
	}
}

func (m *Model) SetPlannerOwnedVerification(enabled bool) {
	m.plannerOwnedVerification = enabled
}

func (m *Model) SetMaxTokensOverride(maxTokens int) {
	if maxTokens > 0 {
		m.maxTokensOverride = maxTokens
		return
	}
	m.maxTokensOverride = 0
}

func (m *Model) SetDisableThinkingOverride(disabled bool) {
	m.disableThinkingOverride = disabled
}

func (m *Model) SetForcedReadinessOpenObligations(obligations []string) {
	m.forcedReadinessOpenObligations = append([]string(nil), obligations...)
}

func (m *Model) SetPlanExecuteScopeContract(contract planexecute.PlanScopeContract) {
	m.planExecuteActive = true
	m.planExecuteScopeContract = contract.Normalized()
}

func (m *Model) clearContextError() {
	m.contextError = ""
}

func (m *Model) setRuntimeError(message string) {
	m.runtimeError = message
	m.status = message
	m.logError("runtime", message)
}

func (m *Model) clearRuntimeError() {
	m.runtimeError = ""
}

func (m *Model) clearRuntimeErrorIfServerRelated() {
	if strings.HasPrefix(m.runtimeError, "Server error:") {
		m.runtimeError = ""
	}
}

func (m *Model) errorBanner() string {
	switch {
	case m.contextError != "" && m.runtimeError != "":
		return m.contextError + " | " + m.runtimeError
	case m.contextError != "":
		return m.contextError
	default:
		return m.runtimeError
	}
}
