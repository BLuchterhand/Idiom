package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/promptstack"
	"qwen-bubbles/internal/transcript"
	"qwen-bubbles/internal/workspace"
)

type serverStatusMsg struct {
	running bool
	modelID string
	err     error
	owned   bool
}

type streamDeltaMsg struct {
	text string
}

type streamDoneMsg struct {
	err error
}

type transcriptSavedMsg struct {
	path string
	err  error
}

func New(cfg config.Config) *Model {
	in := textarea.New()
	in.Placeholder = "Type a prompt. Enter sends. /help shows commands."
	in.Focus()
	in.ShowLineNumbers = false
	in.SetHeight(3)
	in.KeyMap.InsertNewline.SetEnabled(false)

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	m := &Model{
		cfg:            cfg,
		client:         llama.NewClient(cfg.BaseURL(), 0),
		events:         make(chan tea.Msg, 256),
		input:          in,
		spinner:        spin,
		activeProfiles: append([]string(nil), cfg.Chat.DefaultProfiles...),
		status:         "Booting up.",
		bootingServer:  false,
		traceMode:      traceDisplayCompact,
		agentModeName:  strings.TrimSpace(cfg.Chat.AgentMode),
	}
	m.controller = llama.NewServerController(cfg.LlamaServer, m.client)
	m.workspace = workspace.NewManager(cfg.Workspace)
	mode := agent.ModeOrDefault(m.agentModeName)
	m.workspace.SetPermissionRules(mode.PermissionRules)
	m.runner = &agent.Runner{
		Client:       m.client,
		Workspace:    m.workspace,
		MaxToolSteps: modeStepCap(cfg.Chat.MaxToolSteps, mode),
		AgentName:    "main",
	}
	if err := m.reloadPromptStack(); err != nil {
		m.setContextError("Prompt context failed: " + err.Error())
	}
	m.initDebugLogger()
	return m
}

// modeStepCap applies the Mode's StepCap on top of the config.MaxToolSteps
// ceiling. Zero StepCap means "no per-mode cap".
func modeStepCap(configured int, mode agent.Mode) int {
	if mode.StepCap > 0 && (configured <= 0 || mode.StepCap < configured) {
		return mode.StepCap
	}
	return configured
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		waitForEvent(m.events),
		func() tea.Msg {
			go m.bootstrap()
			return nil
		},
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyCtrlS:
			m.saveTranscriptAsync()
		case tea.KeyCtrlL:
			m.messages = nil
			m.status = "Cleared chat."
			m.renderMessages(true)
		case tea.KeyCtrlR:
			m.restartServerAsync()
		case tea.KeyEnter:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				break
			}
			m.input.SetValue("")
			if strings.HasPrefix(text, "/") {
				if quit := m.handleSlashCommand(text); quit {
					return m, tea.Quit
				}
			} else if !m.streaming {
				m.sendPromptAsync(text)
			}
			m.renderMessages(true)
		}
	case tea.MouseMsg:
		m.updateViewport(msg)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.streaming {
			m.workingFrame = (m.workingFrame + 1) % 4
			m.renderMessages(false)
		}
		cmds = append(cmds, cmd)
	case serverStatusMsg:
		m.bootingServer = false
		m.logServerEvent(msg)
		if msg.err != nil {
			m.serverUp = false
			m.setRuntimeError("Server error: " + msg.err.Error())
		} else {
			m.serverUp = msg.running
			if !msg.running {
				m.modelID = ""
			}
			m.modelID = msg.modelID
			m.managedServer = m.managedServer || msg.owned
			m.clearRuntimeErrorIfServerRelated()
			if msg.running {
				m.status = fmt.Sprintf("Server ready on %s. Model: %s", m.cfg.BaseURL(), msg.modelID)
			} else {
				m.status = "Server stopped."
			}
		}
	case streamDeltaMsg:
		m.appendPendingAssistantContent(msg.text)
		m.renderMessages(false)
	case assistantReplyMsg:
		m.setPendingAssistantMessage(llama.ChatMessage{Role: "assistant", Content: msg.text, ReasoningContent: msg.reasoningContent})
		if !msg.logged {
			m.logMessage("assistant", msg.text)
		}
		m.renderMessages(false)
	case streamDoneMsg:
		m.streaming = false
		m.workingFrame = 0
		if msg.err != nil {
			m.setRuntimeError("Chat failed: " + msg.err.Error())
			m.removePendingAssistant()
		} else {
			m.clearRuntimeError()
			m.status = "Response complete."
		}
		m.renderMessages(false)
	case transcriptSavedMsg:
		if msg.err != nil {
			m.setRuntimeError("Save failed: " + msg.err.Error())
		} else {
			m.clearRuntimeError()
			m.status = "Transcript saved to " + m.displayPath(msg.path)
			m.logArtifact("transcript", msg.path)
		}
	case infoMsg:
		m.status = msg.text
	case toolTraceMsg:
		if m.traceMode == traceDisplayDetail {
			m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: msg.text})
			m.logMessage("tool", msg.text)
		}
		m.renderMessages(false)
	case nil:
	default:
	}

	if scrollableViewportKey(msg) {
		m.updateViewport(msg)
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd, waitForEvent(m.events))
	return m, tea.Batch(cmds...)
}

func (m *Model) ensureServerReady(ctx context.Context) error {
	models, err := m.client.ListModels(ctx)
	if err == nil && len(models) > 0 {
		m.serverUp = true
		m.modelID = models[0].ID
		m.logServerEvent(serverStatusMsg{running: true, modelID: models[0].ID})
		return nil
	}
	if !m.cfg.Chat.AutoStartServer {
		return fmt.Errorf("no llama server detected and chat.auto_start_server is false")
	}
	if err := m.controller.Start(ctx); err != nil {
		m.logServerEvent(serverStatusMsg{err: err})
		return err
	}
	m.managedServer = true
	if err := m.controller.WaitUntilReady(ctx); err != nil {
		m.logServerEvent(serverStatusMsg{err: err})
		return err
	}
	models, err = m.client.ListModels(ctx)
	if err != nil || len(models) == 0 {
		m.serverUp = true
		m.modelID = "local-model"
		m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID, owned: true})
		return nil
	}
	m.serverUp = true
	m.modelID = models[0].ID
	m.logServerEvent(serverStatusMsg{running: true, modelID: models[0].ID, owned: true})
	return nil
}

func (m *Model) saveTranscriptAsync() {
	m.status = "Saving transcript."

	messages := append([]llama.ChatMessage(nil), m.messages...)
	go func() {
		path, err := transcript.SaveMarkdown(m.cfg.Chat.SaveDir, messages)
		m.events <- transcriptSavedMsg{path: path, err: err}
	}()
}

// sendPromptAsync is the interactive-mode wrapper around RunPromptOnce.
// Runs in a goroutine so the Bubble Tea loop keeps pumping events.
func (m *Model) sendPromptAsync(prompt string) {
	m.streaming = true
	m.workingFrame = 0
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), m.promptTimeout())
		defer cancel()
		reply, err := m.RunPromptOnce(ctx, prompt)
		if err != nil {
			m.events <- streamDoneMsg{err: err}
			return
		}
		m.events <- assistantReplyMsg{text: reply, logged: true, reasoningContent: m.takeLastAssistantReasoningContent()}
		m.events <- streamDoneMsg{}
	}()
}

func (m *Model) promptTimeout() time.Duration {
	if secs := m.cfg.Chat.RequestTimeoutSeconds; secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return time.Hour
}

// runAgentTurn drives a single agent.Runner.Run invocation for the active
// Mode. Uses the workspace.Manager's active ToolRules (populated from the
// Mode in New() and /mode) to block/ask per-tool; the Mode's StepCap has
// already been folded into m.runner.MaxToolSteps.
func (m *Model) runAgentTurn(ctx context.Context, prompt string, history []llama.ChatMessage, opts llama.ChatOptions, plan promptExecutionPlan) (string, error) {
	_ = plan
	_ = prompt
	mode := m.currentMode()
	steps := modeStepCap(m.cfg.Chat.MaxToolSteps, mode)
	runner := &agent.Runner{
		Client:       m.client,
		Workspace:    m.workspace,
		MaxToolSteps: steps,
		AgentName:    "main",
		OnEvent: func(event agent.RunEvent) {
			m.logMessage(event.Role, event.Content)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			switch m.workspace.EvaluateToolRule(req) {
			case workspace.ToolRuleAllow:
				return nil
			case workspace.ToolRuleDeny:
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Tool request blocked by Mode permission rules",
					Error:   fmt.Sprintf("agent mode %q denies this request", mode.Name),
				}
			case workspace.ToolRuleAsk:
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Tool request requires approval under Mode " + mode.Name,
					Error:   fmt.Sprintf("agent mode %q requires explicit approval for this request; this headless loop does not prompt interactively", mode.Name),
				}
			}
			return nil
		},
	}
	answer, err := runner.Run(
		ctx,
		history,
		opts,
		func(status string) {
			m.events <- infoMsg{text: status}
		},
		func(event agent.ToolRequestEvent) {
			m.logToolRequest(event)
		},
		func(event agent.ToolResultEvent) {
			m.logToolResult(event)
			m.pinPathsFromToolResult(event.Result)
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(event.Result)}
		},
	)
	if err != nil {
		return "", err
	}
	m.lastAssistantReasoningContent = strings.TrimSpace(runner.LastAssistantMessage.ReasoningContent)
	return answer, err
}

// pinPathsFromToolResult attaches any file a successful tool result touched
// to the session pin set so it rides along in future turn payloads.
func (m *Model) pinPathsFromToolResult(result workspace.ToolResult) {
	if m == nil || strings.TrimSpace(result.Error) != "" {
		return
	}
	registry := m.pinnedFilesRegistry()
	if registry == nil {
		return
	}
	switch strings.TrimSpace(result.Tool) {
	case "read_file", "edit_file", "write_file", "append_file",
		"go_read_symbol", "go_extract_body", "go_file_outline":
		if path := extractPinnablePathFromSummary(result.Summary); path != "" {
			registry.Pin(path)
		}
	}
}

// extractPinnablePathFromSummary pulls the workspace-relative path out of a
// tool result summary. Supports the common "Read <display> lines N-M" and
// "Edited <display>" formats. Returns empty when nothing parseable.
func extractPinnablePathFromSummary(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	prefixes := []string{"read ", "edited ", "wrote ", "appended ", "outlined ", "extracted "}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			rest := trimmed[len(prefix):]
			for _, boundary := range []string{" lines ", " between ", " at ", " ("} {
				if idx := strings.Index(rest, boundary); idx > 0 {
					rest = rest[:idx]
					break
				}
			}
			rest = strings.TrimSpace(rest)
			if rest == "" {
				return ""
			}
			fields := strings.Fields(rest)
			if len(fields) >= 2 && looksLikeFileHandle(fields[0]) {
				return strings.TrimSpace(strings.Join(fields[1:], " "))
			}
			return rest
		}
	}
	return ""
}

func looksLikeFileHandle(token string) bool {
	if len(token) < 2 || token[0] != 'f' {
		return false
	}
	for _, r := range token[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (m *Model) initDebugLogger() {
	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:           m.cfg.Chat.DebugLogDir,
		Mode:          transcript.DebugLogMode(firstNonEmptyString(m.cfg.Chat.DebugLogMode, "redacted")),
		WorkspaceRoot: m.workspace.Root(),
	})
	if err != nil {
		m.runtimeError = "Debug log failed: " + err.Error()
		m.status = m.runtimeError
		return
	}

	m.debugLogger = logger
	m.debugLogPath = logger.Path()
	_ = m.debugLogger.LogSessionStart(transcript.SessionStart{
		AppName:        m.cfg.AppName,
		ContextSources: m.contextSourceNames(),
		SystemPrompt:   m.systemPrompt(),
		WorkspaceRoot:  m.workspace.Root(),
		WorkspaceCWD:   m.workspace.CWD(),
	})

	if m.contextError != "" {
		m.logError("context", m.contextError)
	}
	if m.runtimeError != "" {
		m.logError("runtime", m.runtimeError)
	}
}

// systemPrompt returns the active composed system prompt for this session.
// The prompt stack is maintained by reloadPromptStack; Mode-specific prompt
// files are layered on top of the base stack.
func (m *Model) systemPrompt() string {
	if m == nil {
		return ""
	}
	base := strings.TrimSpace(m.promptStack.Text)
	mode := m.currentMode()
	extras := loadModeSystemPrompts(mode)
	parts := make([]string, 0, 2)
	if base != "" {
		parts = append(parts, base)
	}
	if extras != "" {
		parts = append(parts, extras)
	}
	return strings.Join(parts, "\n\n")
}

func loadModeSystemPrompts(mode agent.Mode) string {
	// The files named in mode.SystemPromptFiles are loaded by the caller's
	// promptstack. Here we only surface Mode-derived content when the stack
	// hasn't; in practice mode files are consumed via reloadPromptStack, so
	// this path is a defensive fallback and returns the empty string.
	return ""
}

// reloadPromptStack rebuilds the prompt stack from the active config and
// profiles. Delegates to promptstack; kept here so callers can trigger a
// refresh after /system or /profile commands.
func (m *Model) reloadPromptStack() error {
	return m.reloadPromptStackWithProfiles(m.activeProfiles)
}

func (m *Model) reloadPromptStackWithProfiles(profiles []string) error {
	stack, err := promptstack.Build(m.cfg.Chat, profiles)
	if err != nil {
		return err
	}
	m.promptStack = stack
	return nil
}
