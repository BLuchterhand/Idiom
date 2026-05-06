package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
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
	"idiom/internal/promptstack"
	"idiom/internal/transcript"
	"idiom/internal/workspace"

	"encoding/json"
	"sort"
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
	sort.Slice(out, func(i, j int) bool {
		return out[i].OutputChars > out[j].OutputChars
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
	sort.Strings(names)
	out := make([]transcript.DiscoveryAggregate, 0, len(names))
	for _, name := range names {
		out = append(out, transcript.DiscoveryAggregate{Name: name, Calls: counts[name]})
	}
	return out
}

type Model struct {
	cfg        config.Config
	client     *llama.Client
	controller *llama.ServerController
	// implementerClient + implementerController are set only when the
	// config has a separate Implementer llama-server. Nil when the app
	// runs in single-model mode. Used by runChildTask to route write-
	// enabled child work onto the implementation model.
	implementerClient     *llama.Client
	implementerController *llama.ServerController
	workspace             *workspace.Manager
	mcpRegistry           *mcp.Registry
	runner                *agent.Runner
	events                chan tea.Msg

	width    int
	height   int
	ready    bool
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	messages                      []llama.ChatMessage
	promptStack                   promptstack.Stack
	activeProfiles                []string
	streaming                     bool
	bootingServer                 bool
	serverUp                      bool
	managedServer                 bool
	modelID                       string
	status                        string
	contextError                  string
	runtimeError                  string
	workingFrame                  int
	debugLogger                   *transcript.DebugLogger
	debugLogPath                  string
	sessionStartedAt              time.Time
	sessionAgg                    sessionAggregate
	eventStream                   *jsonEventStream
	lastAssistantReasoningContent string
	agentModeName                 string
	forcedTaskFamily              string
	traceMode                     traceDisplayMode

	workspaceOverviewInjected bool

	pinnedFiles *pinnedFileRegistry

	approvalMu          sync.Mutex
	approvalSerial      sync.Mutex
	approvalPending     *pendingApproval
	detailPanelVisible  bool
	rollbackHistory     []rollbackEntry
	instructionAttacher *agent.InstructionAttacher
	mdRenderer          *glamour.TermRenderer
	mdRendererWrapWidth int
	messageRenderCache  string
	messageRenderWidth  int
	messageRenderHash   uint64
	messageRenderMD     bool

	// History panel
	historyEntries    []historyEntry
	historySelected   int
	historyExpanded   bool
	historyPanelOpen  bool
	historyFocused    bool
	historyViewport   viewport.Model
	summaryClient     *llama.Client
	summaryController *llama.ServerController

	// Side-panel tabs
	panelTab      int // panelTabHistory | panelTabPlan
	planViewport  viewport.Model
	planPath      string
	planMTime     time.Time
	planContent   string
	planLoadError string

	// planWriteSnapshots holds the pre-edit file contents captured during
	// PreExecute for write-class tools. Keyed by step number (one key
	// per tool call) with a slice of snapshots because apply_patch may
	// touch multiple files in one call. Entries are consumed and cleared
	// after the matching ToolResult event records its diff.
	planWriteSnapshots map[int][]planWriteSnapshot
	// planSessionHeadingWritten is set once per live session after the
	// `## Session …` heading has been appended to the plan file. Keeping
	// it in-memory avoids repeated disk scans on every successful write.
	planSessionHeadingWritten bool
	// planLedgerItems mirrors the most recent task_ledger submission,
	// keyed by item id. recordWriteSuccess consults it to find a
	// planned item that mentions the written path and check it off in
	// place instead of adding a fresh "Unplanned" line.
	planLedgerItems map[string]workspace.TaskLedgerItem
	// planLedgerRenderedIDs tracks which ledger item ids have already
	// been rendered into the plan file, so repeated task_ledger calls
	// only append newly-introduced items rather than duplicating the
	// whole list on each update.
	planLedgerRenderedIDs map[string]bool
	// planLedgerHits records which paths of each ledger item have
	// already been attached to a successful write. Keyed by item id,
	// inner map keyed by path. Used so multi-path items only flip to
	// `[x]` after every listed path is accounted for, and so a second
	// write to the same path doesn't double-attach.
	planLedgerHits map[string]map[string]bool
	// planCursor is the index into the parsed plan-item list that the
	// selection highlight points at. Reset when the plan reloads or a
	// new session starts. Only meaningful when the plan panel is
	// focused and no diff overlay is visible.
	planCursor int
	// planOverlayID holds the diff id currently displayed in the
	// overlay, or empty when no overlay is visible. Acts as the mode
	// switch between "plan list" and "diff overlay" navigation.
	planOverlayID string
	// planOverlayBody caches the rendered overlay body so redraws
	// don't re-read and re-colorize the diff on every tick.
	planOverlayBody string

	// sessionsViewport renders the third side-panel tab: a selectable
	// list of prior sessions (id + one-line summary). Enter resumes
	// the highlighted session.
	sessionsViewport viewport.Model
	// sessionsList caches the last ListRecordedSessions snapshot so
	// ↑/↓ navigation doesn't re-scan the debug-log dir on every key.
	// Refreshed on Ctrl+T into the Sessions tab and on /resume.
	sessionsList []SessionInfo
	// sessionsRawCache stores the newest-first directory scan before
	// filtering out the current live session. Reused while the debug-log
	// directory mtime is unchanged so opening/focusing the Sessions tab
	// does not rescan every JSONL file.
	sessionsRawCache      []SessionInfo
	sessionsRawCacheDir   string
	sessionsRawCacheMTime time.Time
	sessionsRawCacheValid bool
	sessionsSummaryCache  map[string]sessionSummaryCacheEntry
	// sessionsCursor is the index into sessionsList that Enter would
	// resume. Clamped during render when the list shrinks under it.
	sessionsCursor int

	// pendingExec carries a tea.Cmd (typically tea.ExecProcess) that a slash
	// command wants to run, but can't dispatch itself because handleSlashCommand
	// doesn't return tea.Cmd. The Update loop consumes and clears this after
	// the slash handler returns.
	pendingExec tea.Cmd

	// Tool-call names collected during the current agent turn; consumed when
	// streamDoneMsg fires and cleared before the next turn begins.
	currentTurnTools          []string
	currentTurnHistoryIndex   int
	currentTurnHistoryActive  bool
	historyRenderPending      bool
	historyRenderLast         time.Time
	historyRenderScrollBottom bool
}

// pinnedFilesRegistry returns the lazy-initialized per-session pin set.
func (m *Model) pinnedFilesRegistry() *pinnedFileRegistry {
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
	step    int
	text    string
	intent  string
	running bool
}

// planRefreshMsg requests a re-render of the plan panel. Emitted after
// any plan-mutating tool result (write success, task_ledger update,
// write failure) so the user sees the updated plan without having to
// toggle the tab or focus the panel.
type planRefreshMsg struct{}

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
	_ = m.controller.Stop(ctx)
}

func (m *Model) chatOptions() llama.ChatOptions {
	return llama.ChatOptions{
		Model:             m.modelID,
		Temperature:       m.cfg.Chat.Temperature,
		TopP:              m.cfg.Chat.TopP,
		TopK:              m.cfg.Chat.TopK,
		MinP:              m.cfg.Chat.MinP,
		PresencePenalty:   m.cfg.Chat.PresencePenalty,
		RepetitionPenalty: m.cfg.Chat.RepetitionPenalty,
		EnableThinking:    cloneBoolPtr(m.cfg.Chat.EnableThinking),
		PreserveThinking:  m.cfg.Chat.PreserveThinking,
		MaxTokens:         m.cfg.Chat.MaxTokens,
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

	models, err := m.client.ListModels(ctx)
	if err == nil && len(models) > 0 {
		m.events <- serverStatusMsg{running: true, modelID: models[0].ID}
		return
	}

	if m.cfg.Chat.AutoStartServer {
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
		m.events <- infoMsg{text: "Implementer ready: " + models[0].ID}
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
	m.events <- infoMsg{text: "Implementer ready: " + id}
}

func (m *Model) handleSlashCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "/quit", "/exit":
		return true
	case "/help":
		m.status = "Commands: /save /clear /system <text> /temp <value> /preset [list|name] /server <start|stop|restart|status> /profile <list|add|remove> /context /workspace /pwd /debuglog [mode redacted|full] /trace [compact|detail] /mode [build|plan|general] /approve /deny /undo /panel /plan /resume [id] /help | Ctrl+T cycles panel tabs | transcript: PgUp/PgDn or mouse wheel"
	case "/clear":
		m.messages = nil
		m.status = "Cleared chat."
	case "/save":
		m.saveTranscriptAsync()
	case "/context":
		m.status = "Active context: " + m.contextSummary()
	case "/workspace":
		m.status = "Workspace: " + m.workspaceSummary()
	case "/pwd":
		m.status = "Current workspace directory: " + m.displayPath(m.workspace.CWD())
	case "/debuglog":
		switch {
		case len(fields) == 1 && m.debugLogPath == "":
			m.status = "Debug conversation logging is unavailable."
		case len(fields) == 1:
			m.status = "Debug log: " + m.displayPath(m.debugLogPath) + " | mode: " + firstNonEmptyString(m.cfg.Chat.DebugLogMode, "redacted")
		case len(fields) == 3 && fields[1] == "mode":
			switch fields[2] {
			case "redacted", "full":
				m.cfg.Chat.DebugLogMode = fields[2]
				if m.debugLogger != nil {
					m.debugLogger.SetMode(transcript.DebugLogMode(fields[2]))
				}
				m.status = "Debug log mode set to " + fields[2] + "."
			default:
				m.status = "Usage: /debuglog [mode redacted|full]"
			}
		default:
			m.status = "Usage: /debuglog [mode redacted|full]"
		}
	case "/trace":
		switch {
		case len(fields) == 1:
			m.status = "Current trace mode: " + m.traceSummary()
		case len(fields) == 2:
			switch fields[1] {
			case string(traceDisplayCompact):
				m.traceMode = traceDisplayCompact
				m.status = "Trace mode set to compact."
			case string(traceDisplayDetail):
				m.traceMode = traceDisplayDetail
				m.status = "Trace mode set to detail."
			default:
				m.status = "Usage: /trace [compact|detail]"
			}
		default:
			m.status = "Usage: /trace [compact|detail]"
		}
	case "/mode":
		switch {
		case len(fields) == 1:
			m.status = "Current mode: " + m.modeSummary()
		case len(fields) == 2:
			name := strings.ToLower(strings.TrimSpace(fields[1]))
			mode, ok := agent.ModeByName(name)
			if !ok {
				m.status = "Usage: /mode [build|plan|general]"
				return false
			}
			m.agentModeName = mode.Name
			m.applyModePermissions(mode)
			m.status = "Agent mode set to " + mode.Name + "."
		default:
			m.status = "Usage: /mode [build|plan|general]"
		}
	case "/approve":
		if summary, ok := m.resolveApproval(true); ok {
			m.status = "Approved: " + summary
		} else {
			m.status = "No pending tool approval."
		}
	case "/deny":
		if summary, ok := m.resolveApproval(false); ok {
			m.status = "Denied: " + summary
		} else {
			m.status = "No pending tool approval."
		}
	case "/undo":
		summary, err := m.undoLast()
		if err != nil {
			m.status = "Undo failed: " + err.Error()
		} else {
			m.status = summary
		}
	case "/panel":
		m.setHistoryPanelOpen(!m.historyPanelOpen)
		if m.historyPanelOpen {
			m.status = "Panel open. Ctrl+T cycles tabs · Tab focuses · ↑↓ select · Enter expand · PgUp/PgDn or j/k scroll"
		} else {
			m.status = "Panel closed."
		}
	case "/resume":
		// /resume with no arg opens the Sessions tab and focuses it
		// so ↑/↓/Enter pick from the list. With an arg it's a direct
		// shortcut that swaps into the named session immediately.
		if len(fields) < 2 {
			m.setHistoryPanelOpen(true)
			m.panelTab = panelTabSessions
			m.refreshSessionsList()
			m.historyFocused = true
			m.renderSessionsPanelContent()
			if len(m.sessionsList) == 0 {
				m.status = "No prior sessions found."
			} else {
				m.status = "Pick a session: ↑/↓ select · Enter resume · Esc cancel."
			}
			break
		}
		id := fields[1]
		if err := m.ResumeSession(id); err != nil {
			m.status = "Resume failed: " + err.Error()
		} else {
			m.status = "Resumed session " + id + "."
			m.rerenderActivePanel()
		}
	case "/plan":
		m.setHistoryPanelOpen(true)
		m.panelTab = panelTabPlan
		cmd, status, err := m.prepareEditPlanCmd()
		m.rerenderActivePanel()
		if err != nil {
			m.status = "Plan editor failed: " + err.Error()
		} else {
			m.status = status
			m.pendingExec = cmd
		}
	case "/system":
		prompt := strings.TrimSpace(strings.TrimPrefix(raw, "/system"))
		switch {
		case prompt == "":
			m.status = "Usage: /system <prompt>"
		default:
			m.cfg.Chat.SystemPrompt = prompt
			if err := m.reloadPromptStack(); err != nil {
				m.setContextError("Prompt context failed: " + err.Error())
			} else {
				m.clearContextError()
				m.status = "Updated inline system prompt."
			}
		}
	case "/temp":
		if len(fields) != 2 {
			m.status = "Usage: /temp <value>"
			return false
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			m.status = "Temperature must be a number."
			return false
		}
		m.cfg.Chat.Temperature = value
		m.cfg.Chat.Preset = "custom"
		m.status = fmt.Sprintf("Temperature set to %.2f", value)
	case "/preset":
		switch {
		case len(fields) == 1:
			m.status = "Current preset: " + fallback(strings.TrimSpace(m.cfg.Chat.Preset), "custom") + " | " + m.samplingSummary()
		case len(fields) == 2 && fields[1] == "list":
			m.status = "Presets: " + joinOrNone(config.BuiltinChatPresetNames()) + " | current: " + fallback(strings.TrimSpace(m.cfg.Chat.Preset), "custom")
		case len(fields) == 2:
			if err := config.ApplyBuiltinChatPreset(&m.cfg.Chat, fields[1]); err != nil {
				m.status = err.Error()
				return false
			}
			m.status = fmt.Sprintf("Applied preset %q. %s", m.cfg.Chat.Preset, m.samplingSummary())
		default:
			m.status = "Usage: /preset [list|name]"
		}
	case "/server":
		if len(fields) < 2 {
			m.status = "Usage: /server <start|stop|restart|status>"
			return false
		}
		switch fields[1] {
		case "start":
			m.startServerAsync()
		case "stop":
			m.stopServerAsync()
		case "restart":
			m.restartServerAsync()
		case "status":
			state := "down"
			if m.serverUp {
				state = "up"
			}
			m.status = fmt.Sprintf("Server %s on %s. Model: %s", state, m.cfg.BaseURL(), fallback(m.modelID, "unknown"))
		default:
			m.status = "Usage: /server <start|stop|restart|status>"
		}
	case "/profile":
		if len(fields) < 2 {
			m.status = "Usage: /profile <list|add|remove>"
			return false
		}
		switch fields[1] {
		case "list":
			names, err := promptstack.ListProfiles(m.cfg.Chat.ProfilesDir)
			if err != nil {
				m.setContextError("Profile listing failed: " + err.Error())
				return false
			}
			m.clearContextError()
			m.status = fmt.Sprintf("Profiles available: %s | active: %s", joinOrNone(names), joinOrNone(m.activeProfiles))
		case "add":
			if len(fields) != 3 {
				m.status = "Usage: /profile add <name>"
				return false
			}
			name := fields[2]
			if slices.Contains(m.activeProfiles, name) {
				m.status = "Profile already active: " + name
				return false
			}
			nextProfiles := append(append([]string(nil), m.activeProfiles...), name)
			if err := m.reloadPromptStackWithProfiles(nextProfiles); err != nil {
				m.setContextError("Profile add failed: " + err.Error())
				return false
			}
			m.activeProfiles = nextProfiles
			m.clearContextError()
			m.status = fmt.Sprintf("Activated profile %q. Context: %s", name, m.contextSummary())
		case "remove":
			if len(fields) != 3 {
				m.status = "Usage: /profile remove <name>"
				return false
			}
			name := fields[2]
			if !slices.Contains(m.activeProfiles, name) {
				m.status = "Profile is not active: " + name
				return false
			}
			nextProfiles := make([]string, 0, len(m.activeProfiles)-1)
			for _, profile := range m.activeProfiles {
				if profile != name {
					nextProfiles = append(nextProfiles, profile)
				}
			}
			if err := m.reloadPromptStackWithProfiles(nextProfiles); err != nil {
				m.setContextError("Profile remove failed: " + err.Error())
				return false
			}
			m.activeProfiles = nextProfiles
			m.clearContextError()
			m.status = fmt.Sprintf("Removed profile %q. Context: %s", name, m.contextSummary())
		default:
			m.status = "Usage: /profile <list|add|remove>"
		}
	default:
		m.status = "Unknown command. Use /help."
	}

	return false
}

func (m *Model) startServerAsync() {
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
			m.events <- serverStatusMsg{running: true, modelID: "local-model", owned: true}
			return
		}

		m.events <- serverStatusMsg{running: true, modelID: models[0].ID, owned: true}
	}()
}

func (m *Model) stopServerAsync() {
	m.status = "Stopping llama-server."

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		err := m.controller.Stop(ctx)
		m.events <- serverStatusMsg{running: false, err: err}
	}()
}

func (m *Model) restartServerAsync() {
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
			m.events <- serverStatusMsg{running: true, modelID: "local-model", owned: true}
			return
		}
		m.events <- serverStatusMsg{running: true, modelID: models[0].ID, owned: true}
	}()
}

// renderHistoryPanelContent rebuilds the history viewport content from
// m.historyEntries. Call whenever entry data or selection changes.
func (m *Model) renderHistoryPanelContent() {
	if !m.historyPanelOpen {
		return
	}
	w := maxInt(8, m.historyViewport.Width)

	var b strings.Builder
	b.WriteString(panelTitleStyle.Width(w).Render("◈ History"))
	b.WriteString("\n")

	for i, entry := range m.historyEntries {
		selected := i == m.historySelected
		expanded := selected && (m.historyExpanded || entry.inProgress)

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
		case selected && m.historyFocused:
			style = panelFocusedStyle
		case selected:
			style = panelUnfocusedStyle
		default:
			style = panelEntryStyle
		}
		for j, wl := range wrapLines(entry.summary, maxInt(1, w-len([]rune(head)))) {
			row := indent + wl
			if j == 0 {
				row = head + wl
			}
			b.WriteString(style.Width(w).Render(row))
			b.WriteString("\n")
		}

		if expanded {
			for _, c := range entry.contents {
				for _, wl := range wrapLines(c, maxInt(1, w-2)) {
					b.WriteString(panelDetailStyle.Width(w).Render(wl))
					b.WriteString("\n")
				}
			}
			for _, tc := range entry.toolCalls {
				for j, wl := range wrapLines(tc.line, maxInt(1, w-4)) {
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
				if strings.TrimSpace(tc.intent) != "" {
					for _, wl := range wrapLines("why: "+tc.intent, maxInt(1, w-4)) {
						b.WriteString(panelDetailStyle.Width(w).Render("  " + wl))
						b.WriteString("\n")
					}
				}
			}
			b.WriteString("\n")
		}
	}

	if len(m.historyEntries) == 0 {
		b.WriteString(panelDetailStyle.Width(w).Render("No history yet."))
		b.WriteString("\n")
		b.WriteString(panelDetailStyle.Width(w).Render("Entries appear as"))
		b.WriteString("\n")
		b.WriteString(panelDetailStyle.Width(w).Render("messages are sent."))
	}

	m.historyViewport.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m *Model) resize() {
	start := time.Now()
	defer m.logUISlowPath("ui.resize", start, fmt.Sprintf("panel_open=%t tab=%d size=%dx%d", m.historyPanelOpen, m.panelTab, m.width, m.height))

	headerHeight := 1
	dividerCount := 2 // one after header, one before input
	inputHeight := 5
	footerHeight := 4 // compact bar + up to 3 conditional lines
	contentHeight := m.height - headerHeight - dividerCount - inputHeight - footerHeight
	if contentHeight < 5 {
		contentHeight = 5
	}

	var mainWidth, panelWidth int
	if m.historyPanelOpen {
		panelWidth = m.width / 3
		mainWidth = maxInt(24, m.width-panelWidth-1) // 1 for the │ divider
	} else {
		mainWidth = maxInt(24, m.width)
	}

	if !m.ready {
		m.viewport = viewport.New(mainWidth, contentHeight)
		m.ready = true
	}
	mainWidthChanged := m.viewport.Width != mainWidth
	m.viewport.Width = mainWidth
	m.viewport.Height = contentHeight

	if m.historyPanelOpen {
		// Reserve 1 row for the tab header strip above the active viewport.
		panelContentHeight := maxInt(3, contentHeight-1)
		m.historyViewport.Width = panelWidth
		m.historyViewport.Height = panelContentHeight
		m.planViewport.Width = panelWidth
		m.planViewport.Height = panelContentHeight
		m.sessionsViewport.Width = panelWidth
		m.sessionsViewport.Height = panelContentHeight
	}

	m.input.SetWidth(m.width)
	m.ensureMarkdownRenderer(maxInt(20, mainWidth-2))
	if mainWidthChanged {
		m.invalidateMessageRenderCache()
	}
	m.renderMessages(false)
	if m.historyPanelOpen {
		m.rerenderActivePanel()
	}
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
}

func (m *Model) setHistoryPanelOpen(open bool) {
	start := time.Now()
	defer m.logUISlowPath("ui.toggle_panel", start, fmt.Sprintf("open=%t tab=%d", open, m.panelTab))

	if m.historyPanelOpen == open && m.ready {
		if open {
			m.rerenderActivePanel()
		}
		return
	}
	m.historyPanelOpen = open
	m.historyFocused = false
	if !open {
		m.historyRenderPending = false
		m.historyRenderScrollBottom = false
	}
	if open && len(m.historyEntries) > 0 {
		m.historySelected = len(m.historyEntries) - 1
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
	if len(m.promptStack.Sources) == 0 {
		return "none"
	}

	parts := make([]string, 0, len(m.promptStack.Sources))
	for _, source := range m.promptStack.Sources {
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
	message.Role = fallback(strings.TrimSpace(message.Role), "assistant")
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
	if len(m.promptStack.Sources) == 0 {
		return nil
	}

	names := make([]string, 0, len(m.promptStack.Sources))
	for _, source := range m.promptStack.Sources {
		names = append(names, source.DisplayName)
	}
	return names
}

func (m *Model) logMessage(role, content string) {
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
					Agent:            firstNonEmptyString(payload.Agent, payload.Label),
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
	m.logMessage(event.Role, event.Content)
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
		})
	}
	return true
}

func (m *Model) writeSessionSummary() {
	if m.debugLogger == nil {
		return
	}
	if m.sessionAgg.turns == 0 && m.sessionAgg.modelCalls == 0 && m.sessionAgg.toolCalls == 0 {
		return
	}
	ctxSize := m.cfg.LlamaServer.CtxSize
	peakPct := 0.0
	if ctxSize > 0 && m.sessionAgg.peakPromptTokens > 0 {
		peakPct = float64(m.sessionAgg.peakPromptTokens) / float64(ctxSize)
	}
	duration := int64(0)
	if !m.sessionStartedAt.IsZero() {
		duration = time.Since(m.sessionStartedAt).Milliseconds()
	}
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
