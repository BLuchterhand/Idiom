package app

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/transcript"
	"qwen-bubbles/internal/workspace"
	"regexp"
	"strings"
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
		panelVisible:   true,
	}
	m.controller = llama.NewServerController(cfg.LlamaServer, m.client)
	if classifierURL := cfg.RouteClassifierBaseURL(); classifierURL != "" {
		m.classifierClient = llama.NewClient(classifierURL, 0)
	}
	if cfg.RouteClassifier.AutoStartServer {
		if m.classifierClient == nil {
			m.classifierClient = llama.NewClient(cfg.RouteClassifierBaseURL(), 0)
		}
		m.classifierController = llama.NewServerController(cfg.RouteClassifierServerConfig(), m.classifierClient)
	}
	m.workspace = workspace.NewManager(cfg.Workspace)
	m.runner = &agent.Runner{
		Client:       m.client,
		Workspace:    m.workspace,
		MaxToolSteps: cfg.Chat.MaxToolSteps,
		AgentName:    "main",
	}
	if err := m.reloadPromptStack(); err != nil {
		m.setContextError("Prompt context failed: " + err.Error())
	}
	m.initDebugLogger()
	m.loadPersistedStructuralRefactorStage()
	m.loadPersistedTaskMemory()
	m.loadPersistedWorkflowState()
	return m
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
		case tea.KeyCtrlP:
			m.panelVisible = !m.panelVisible
			if m.panelVisible {
				m.status = "Execution panel expanded."
			} else {
				m.status = "Execution panel collapsed."
			}
			m.resize()
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
			if m.phaseCard.State == phaseStateActive {
				m.phaseCard.State = phaseStateFailed
				if strings.TrimSpace(m.phaseCard.Detail) == "" {
					m.phaseCard.Detail = msg.err.Error()
				}
			}
			m.appendExecutionProgress("request failed: " + msg.err.Error())
			m.removePendingAssistant()
		} else {
			m.clearRuntimeError()
			m.status = "Response complete."
			m.appendExecutionProgress("request complete")
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
	case rollbackCompleteMsg:
		if msg.err != nil {
			m.setRuntimeError("Rollback failed: " + msg.err.Error())
			m.setPhaseCard(analysisLaneImplement, "rollback", phaseStateFailed, msg.err.Error())
			m.appendExecutionProgress("rollback failed: " + msg.err.Error())
		} else {
			m.clearRuntimeError()
			m.status = "Rollback complete."
			m.messages = append(m.messages, llama.ChatMessage{Role: "tool", Content: msg.summary})
			m.logMessage("tool", msg.summary)
			m.setPhaseCard(analysisLaneImplement, "rollback", phaseStateDone, "Last bounded change was restored and re-verified.")
			m.setExecutionPlanSummary(analysisLaneImplement, "Last bounded change was rolled back and re-verified.", nil)
			if m.lastJournal != nil && m.lastJournal.Refactor != nil {
				m.clearPersistedStructuralRefactorStage()
			}
			m.lastJournal = nil
		}
		m.renderMessages(false)
	case exploreCacheInvalidatedMsg:
		m.invalidateExploreCache(msg.reason)
	case toolTraceMsg:
		m.appendExecutionProgress(firstToolTraceLine(msg.text))
		if m.traceMode == traceDisplayDetail {
			m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: msg.text})
			m.logMessage("tool", msg.text)
		} else {
			m.recordCompactTrace(msg.text)
		}
		m.renderMessages(false)
	case phaseCardMsg:
		m.applyPhaseCardUpdate(phaseCardState(msg))
		m.renderMessages(false)
	case writeApprovalProposalMsg:
		proposal := msg.proposal
		m.pendingWrite = &proposal
		m.status = proposal.statusText()
		m.applyPhaseCardUpdate(phaseCardState{
			Lane:   analysisLaneImplement,
			Phase:  "approval",
			Detail: "Awaiting scoped write approval for " + strings.Join(proposal.Paths, ", "),
			State:  phaseStateBlocked,
		})
		m.setExecutionPlanSummary(
			analysisLaneImplement,
			"Waiting for scoped write approval before applying the bounded plan.",
			proposalPanelSteps(&proposal),
		)
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

func (m *Model) runMainAgent(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	rails deterministicControlRailsSnapshot,
) (string, error) {
	mainGuard := newMainWriteGuard(m.workspace, prompt)
	if instruction := controlRailRuntimeInstruction(rails); instruction != "" {
		history = append(append([]llama.ChatMessage(nil), history...), llama.ChatMessage{Role: "system", Content: instruction})
	}
	runner := &agent.Runner{
		Client:           m.client,
		Workspace:        m.workspace,
		MaxToolSteps:     m.cfg.Chat.MaxToolSteps,
		AgentName:        "main",
		PermissionPolicy: controlRailPermissionPolicy(rails),
		CompletionGate:   controlRailCompletionGate(rails),
		RuntimeBrief: buildWorkerRuntimeBrief(
			prompt,
			rails,
			implementationTargetPacket{},
			"",
			nil,
			agent.RunPolicy{MaxToolSteps: m.cfg.Chat.MaxToolSteps},
			nil,
			nil,
			nil,
		),
		OnEvent: func(event agent.RunEvent) {
			m.logMessage(event.Role, event.Content)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if blocked := controlRailPreExecute(rails, req); blocked != nil {
				return blocked
			}
			return mainGuard.PreExecute(req)
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
			if shouldInvalidateExploreCacheForTool(event.Result) {
				m.events <- exploreCacheInvalidatedMsg{
					reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", event.Result.Tool),
				}
			}
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(event.Result)}
		},
	)
	if err == nil {
		m.lastAssistantReasoningContent = strings.TrimSpace(runner.LastAssistantMessage.ReasoningContent)
	}
	return answer, err
}

func (m *Model) tryDirectConfigFieldUpdate(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	packet implementationTargetPacket,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil {
		return directImplementationShortcut{}
	}
	field, desiredValue, ok := configFieldUpdateTarget(prompt)
	if !ok {
		return directImplementationShortcut{}
	}
	targets := make([]string, 0, len(packet.PrimaryPaths))
	for _, path := range packet.PrimaryPaths {
		path = normalizeGuardedPath(path)
		if !strings.HasSuffix(strings.ToLower(path), ".json") {
			continue
		}
		targets = append(targets, path)
	}
	if len(targets) == 0 {
		return directImplementationShortcut{}
	}
	step := startStep
	plannedReqs := make([]workspace.ToolRequest, 0, len(targets))
	changedPaths := make([]string, 0, len(targets))
	for _, path := range targets {
		searchReq := workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: `"` + regexp.QuoteMeta(field) + `"\s*:`,
			Path:    path,
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       marshalStructuredExecutionRequest(searchReq),
			Request:   searchReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("search_content:%s:%s", path, field),
		})
		searchResult := mgr.Execute(ctx, searchReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: searchResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(searchResult)}
		step++
		if searchResult.Error != "" {
			return directImplementationShortcut{}
		}
		line, ok := firstSearchMatchLine(searchResult.Output, path)
		if !ok {
			return directImplementationShortcut{}
		}
		readReq := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: line,
			EndLine:   line,
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       marshalStructuredExecutionRequest(readReq),
			Request:   readReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("read_file:%s:%d", path, line),
		})
		readResult := mgr.Execute(ctx, readReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: readResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(readResult)}
		step++
		if readResult.Error != "" {
			return directImplementationShortcut{}
		}
		lineText, ok := extractReadFileLine(readResult.Output, line)
		if !ok {
			return directImplementationShortcut{}
		}
		replacement, ok := replaceJSONFieldLine(lineText, field, desiredValue)
		if !ok {
			return directImplementationShortcut{}
		}
		plannedReqs = append(plannedReqs, workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      path,
			StartLine: line,
			EndLine:   line,
			Content:   replacement,
		})
		if err := preflightStructuredExecutionRequest(mgr, plannedReqs[len(plannedReqs)-1]); err != nil {
			m.logMessage("implement_config_update", "direct config field update rejected by preflight: "+err.Error())
			return directImplementationShortcut{}
		}
	}
	if proposal := buildWriteApprovalProposalForRequests(ctx, mgr, prompt, guard, plannedReqs); proposal != nil {
		m.logMessage("implement_config_update", "staged guarded write approval for exact scope: "+strings.Join(proposal.Paths, ", "))
		return directImplementationShortcut{
			terminal: true,
			proposal: proposal,
		}
	}
	for _, req := range plannedReqs {
		if guard != nil {
			if blocked := guard.PreExecute(req); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(req, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				return directImplementationShortcut{}
			}
		}
		m.logMessage("implement_config_update", fmt.Sprintf("direct config field update proposed for %s line %d", req.Path, req.StartLine))
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       marshalStructuredExecutionRequest(req),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("edit_file:%s:%d-config-field", req.Path, req.StartLine),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			return directImplementationShortcut{}
		}
		editResult := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: editResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(editResult)}
		if writeToolResultChanged(req, editResult) && shouldInvalidateExploreCacheForTool(editResult) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", editResult.Tool),
			}
		}
		step++
		if editResult.Error != "" {
			return directImplementationShortcut{}
		}
		if writeToolResultChanged(req, editResult) {
			changedPaths = append(changedPaths, req.Path)
		}
	}
	if len(changedPaths) == 0 {
		return directImplementationShortcut{}
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      fmt.Sprintf("Updated %s to %s in %s.", field, desiredValue, strings.Join(uniqueNonEmptyStrings(changedPaths), ", ")),
		nextStep:     step,
	}
}

func configFieldUpdateTarget(prompt string) (string, string, bool) {
	normalized := strings.ToLower(prompt)
	if !containsAny(normalized, "config", ".json", "settings", "both files") {
		return "", "", false
	}
	fieldMatches := regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\b`).FindAllString(prompt, -1)
	field := ""
	for _, match := range fieldMatches {
		if strings.Contains(match, "_") {
			field = match
			break
		}
	}
	if field == "" {
		return "", "", false
	}
	valueMatch := regexp.MustCompile(`(?i)\b(?:is|to)\s+("([^"\\]|\\.)*"|-?\d+(?:\.\d+)?|true|false|null)\b`).FindStringSubmatch(prompt)
	if len(valueMatch) < 2 {
		return "", "", false
	}
	return field, strings.TrimSpace(valueMatch[1]), true
}

func (m *Model) newWorkspaceManager() *workspace.Manager {
	mgr := workspace.NewManager(m.cfg.Workspace)
	if m.workspace != nil {
		_ = mgr.SetWritePolicy(m.workspace.WritePolicy())
	}
	return mgr
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
	workspaceMode, thoroughness := m.currentModeDetails()
	_ = m.debugLogger.LogSessionStart(transcript.SessionStart{
		AppName:             m.cfg.AppName,
		ContextSources:      m.contextSourceNames(),
		SystemPrompt:        m.systemPrompt(),
		WorkspaceRoot:       m.workspace.Root(),
		WorkspaceCWD:        m.workspace.CWD(),
		WorkspaceMode:       workspaceMode,
		ExploreThoroughness: thoroughness,
	})

	if m.contextError != "" {
		m.logError("context", m.contextError)
	}
	if m.runtimeError != "" {
		m.logError("runtime", m.runtimeError)
	}
}

func looksLikeMarkerPatternDefinition(text string) bool {
	if !strings.Contains(text, "return \"") || !strings.Contains(text, "|") {
		return false
	}
	return containsAny(
		text,
		"todo|fixme",
		"fixme|hack",
		"bug|panic\\(",
		"panic\\(|slow",
		"error|regression",
		"deprecated",
	)
}
