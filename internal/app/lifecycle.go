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
	"qwen-bubbles/internal/frontier"
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
	// OpenCode alignment: unify write-permission UX across lanes. The main
	// path now uses the same implementWriteGuard + approval flow as the
	// implement lane, so a user whose model decides to make an edit gets the
	// same interactive approval proposal instead of a terse "switch lanes"
	// refusal. In auto-write mode the guard short-circuits to a nil block so
	// writes pass through; in guarded mode the approval proposal fires.
	writeGuard := newImplementWriteGuard(m.workspace, prompt)
	var pendingApproval *writeApprovalProposal
	guardBlocked := func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult {
		if proposal := buildWriteApprovalProposal(ctx, m.workspace, prompt, req, writeGuard, blocked); proposal != nil {
			pendingApproval = proposal
			m.events <- writeApprovalProposalMsg{proposal: *proposal}
			return approvalRequiredToolResult(*proposal)
		}
		return blocked
	}
	if instruction := controlRailRuntimeInstruction(rails); instruction != "" {
		history = append(append([]llama.ChatMessage(nil), history...), llama.ChatMessage{Role: "system", Content: instruction})
	}
	// OpenCode-adoption fix C+F: non-implement lanes get an explicit plan-mode
	// posture reminder (mirrors OpenCode `plan.txt`) plus a nudge toward the
	// `task` tool for open-ended exploratory prompts (mirrors `default.txt` /
	// `anthropic.txt`). Kept separate from RuntimeInstruction so verification-
	// only / other task-family reminders compose cleanly alongside it.
	if reminder := controlRailPlanModeReminder(rails); reminder != "" {
		history = append(history, llama.ChatMessage{Role: "system", Content: reminder})
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
			// Fix 6 (per-tool permission granularity) — glob rules in
			// config.Workspace.ToolRules run BEFORE the write guard so the
			// operator can grant or deny specific patterns ahead of the
			// legacy binary gate. Allow short-circuits; Deny hard-blocks;
			// Ask falls through to the approval flow below.
			switch m.workspace.EvaluateToolRule(req) {
			case workspace.ToolRuleAllow:
				return nil
			case workspace.ToolRuleDeny:
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Tool request blocked by tool_rules",
					Error:   "a workspace tool_rules policy denies this request. Edit your config or ask the user to approve a different approach.",
				}
			case workspace.ToolRuleAsk:
				result := guardBlocked(req, workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Guarded write policy blocked broad write scope",
					Error:   "tool_rules policy requires explicit approval for this request",
				})
				return &result
			}
			if blocked := writeGuard.PreExecute(req); blocked != nil {
				result := guardBlocked(req, *blocked)
				return &result
			}
			return nil
		},
	}
	// OpenCode-adoption fix E (grounding on non-implement lanes). Track
	// every workspace path observed via a successful tool result so we can
	// reconcile the final reply against real evidence, mirroring what Fix 3
	// already does for the implement lane. Prevents plan / explore /
	// architecture answers from citing paths the agent never actually read.
	evidencePaths := make(map[string]struct{})
	recordEvidencePath := func(result workspace.ToolResult) {
		if strings.TrimSpace(result.Error) != "" {
			return
		}
		if path := extractPinnablePathFromSummary(result.Summary); path != "" {
			evidencePaths[path] = struct{}{}
		}
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
			recordEvidencePath(event.Result)
			if shouldInvalidateExploreCacheForTool(event.Result) {
				m.events <- exploreCacheInvalidatedMsg{
					reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", event.Result.Tool),
				}
			}
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(event.Result)}
		},
	)
	if err != nil {
		return "", err
	}
	if pendingApproval != nil {
		return pendingApproval.renderSummary(), nil
	}
	m.lastAssistantReasoningContent = strings.TrimSpace(runner.LastAssistantMessage.ReasoningContent)
	return answer, err
}

// pinPathsFromToolResult attaches any file a successful tool result touched
// to the session pin set. Pinned files are re-injected as a reference
// snapshot on every prompt so the model can consult live contents without
// a fresh read_file call each turn. Errors and no-op results are skipped
// so we don't pin hallucinated paths.
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
			// Strip a leading handle like "f5 " that the manager emits.
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
