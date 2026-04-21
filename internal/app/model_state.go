package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/promptstack"
	"qwen-bubbles/internal/transcript"
	"qwen-bubbles/internal/workspace"
)

type Model struct {
	cfg        config.Config
	client     *llama.Client
	controller *llama.ServerController
	workspace  *workspace.Manager
	runner     *agent.Runner
	events     chan tea.Msg

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
	eventStream                   *jsonEventStream
	lastAssistantReasoningContent string
	agentModeName                 string
	traceMode                     traceDisplayMode

	workspaceOverviewInjected bool

	pinnedFiles *pinnedFileRegistry
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

type assistantReplyMsg struct {
	text             string
	logged           bool
	reasoningContent string
}

type infoMsg struct {
	text string
}

type toolTraceMsg struct {
	text string
}

type traceDisplayMode string

const (
	traceDisplayCompact traceDisplayMode = "compact"
	traceDisplayDetail  traceDisplayMode = "detail"
)

func (m *Model) Close() {
	if m.debugLogger != nil {
		_ = m.debugLogger.Close()
	}
	if !m.managedServer {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
	thinking := "auto"
	if m.cfg.Chat.EnableThinking != nil {
		if *m.cfg.Chat.EnableThinking {
			thinking = "on"
		} else {
			thinking = "off"
		}
	}
	return fmt.Sprintf(
		"temp=%.2f top_p=%.2f top_k=%d min_p=%.2f presence=%.2f repeat=%.2f thinking=%s preserve=%t max_tokens=%d",
		m.cfg.Chat.Temperature,
		m.cfg.Chat.TopP,
		m.cfg.Chat.TopK,
		m.cfg.Chat.MinP,
		m.cfg.Chat.PresencePenalty,
		m.cfg.Chat.RepetitionPenalty,
		thinking,
		m.cfg.Chat.PreserveThinking,
		m.cfg.Chat.MaxTokens,
	)
}

func (m *Model) bootstrap() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

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

func (m *Model) handleSlashCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "/quit", "/exit":
		return true
	case "/help":
		m.status = "Commands: /save /clear /system <text> /temp <value> /preset [list|name] /server <start|stop|restart|status> /profile <list|add|remove> /context /workspace /pwd /debuglog [mode redacted|full] /trace [compact|detail] /mode [build|plan|general] /help | transcript: PgUp/PgDn or mouse wheel"
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

func (m *Model) resize() {
	headerHeight := 1
	inputHeight := 5
	footerHeight := 6
	contentHeight := m.height - headerHeight - inputHeight - footerHeight
	if contentHeight < 5 {
		contentHeight = 5
	}
	viewportWidth := maxInt(24, m.width)

	if !m.ready {
		m.viewport = viewport.New(viewportWidth, contentHeight)
		m.ready = true
	}

	m.viewport.Width = viewportWidth
	m.viewport.Height = contentHeight
	m.input.SetWidth(m.width)
	m.renderMessages(false)
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
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogToolRequest(
		event.Step,
		event.Agent,
		event.Raw,
		event.Request.Tool,
		string(event.Mode),
		event.Broad,
		event.Narrowing,
		event.Action,
	)
}

func (m *Model) logToolResult(event agent.ToolResultEvent) {
	m.emitJSONToolResult(event)
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogToolResult(event.Step, event.Agent, event.Result.Tool, event.Result.Summary, event.Result.Error)
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
