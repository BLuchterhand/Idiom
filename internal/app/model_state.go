package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"os/exec"
	"path/filepath"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/promptstack"
	"qwen-bubbles/internal/transcript"
	"qwen-bubbles/internal/workspace"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Model struct {
	cfg                  config.Config
	client               *llama.Client
	controller           *llama.ServerController
	classifierClient     *llama.Client
	classifierController *llama.ServerController
	workspace            *workspace.Manager
	runner               *agent.Runner
	events               chan tea.Msg

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
	exploreCache                  *exploreSessionCache
	pendingWrite                  *writeApprovalProposal
	lastJournal                   *executionJournal
	lastAssistantReasoningContent string
	refactorStage                 *structuralRefactorStage
	taskMemory                    *longHorizonTaskMemory
	workflowState                 *agentWorkflowState
	lastReplan                    *analysisReplanRecommendationState
	agentMode                     agentDecisionMode
	traceMode                     traceDisplayMode
	compactTrace                  compactTraceState
	phaseCard                     phaseCardState
	panelVisible                  bool
	executionPanel                executionPanelState

	routeClassifierOfflineNotified bool
	routeClassifierStartMu         sync.Mutex
	managedRouteClassifier         bool
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

type rollbackCompleteMsg struct {
	summary string
	err     error
}

type traceDisplayMode string

const (
	traceDisplayCompact traceDisplayMode = "compact"
	traceDisplayDetail  traceDisplayMode = "detail"
)

type compactTraceState struct {
	HiddenCount int
	LastEvent   string
}

type phaseDisplayState string

const (
	phaseStateActive  phaseDisplayState = "active"
	phaseStateDone    phaseDisplayState = "done"
	phaseStateBlocked phaseDisplayState = "blocked"
	phaseStateFailed  phaseDisplayState = "failed"
)

func (m *Model) Close() {
	if m.debugLogger != nil {
		_ = m.debugLogger.Close()
	}
	if m.managedRouteClassifier && m.classifierController != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.classifierController.Stop(ctx)
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
		m.status = "Commands: /save /clear /system <text> /temp <value> /preset [list|name] /server <start|stop|restart|status> /profile <list|add|remove> /context /workspace /pwd /debuglog [mode redacted|full] /trace [compact|detail] /panel [show|hide|toggle] /mode [normal|explore [quick|medium|thorough]] /agent [auto|plan|build|review|explore] /workflow [list|name] /writes [guarded|auto] /approve writes /deny writes /undo /resume [refactor|task] /replan /help | transcript: PgUp/PgDn or mouse wheel"
	case "/clear":
		m.messages = nil
		m.status = "Cleared chat."
	case "/save":
		m.saveTranscriptAsync()
	case "/undo", "/rollback":
		if m.lastJournal == nil {
			m.status = "No rollback journal is available for the last bounded change."
			return false
		}
		m.rollbackLastJournalAsync()
	case "/resume":
		if len(fields) > 2 || (len(fields) == 2 && fields[1] != "refactor" && fields[1] != "task") {
			m.status = "Usage: /resume [refactor|task]"
			return false
		}
		if len(fields) == 2 && fields[1] == "task" {
			m.resumeTaskMemoryAsync("")
		} else if len(fields) == 2 && fields[1] == "refactor" {
			m.resumeStructuralRefactorAsync("")
		} else if m.canResumeStructuralRefactor() {
			m.resumeStructuralRefactorAsync("")
		} else {
			m.resumeTaskMemoryAsync("")
		}
	case "/replan":
		if len(fields) != 1 {
			m.status = "Usage: /replan"
			return false
		}
		m.runRecommendedReplanAsync()
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
	case "/panel":
		switch {
		case len(fields) == 1:
			m.status = "Execution panel is " + m.panelSummary() + "."
		case len(fields) == 2:
			switch fields[1] {
			case "show":
				m.panelVisible = true
				m.status = "Execution panel expanded."
				m.resize()
			case "hide":
				m.panelVisible = false
				m.status = "Execution panel collapsed."
				m.resize()
			case "toggle":
				m.panelVisible = !m.panelVisible
				if m.panelVisible {
					m.status = "Execution panel expanded."
				} else {
					m.status = "Execution panel collapsed."
				}
				m.resize()
			default:
				m.status = "Usage: /panel [show|hide|toggle]"
			}
		default:
			m.status = "Usage: /panel [show|hide|toggle]"
		}
	case "/mode":
		switch {
		case len(fields) == 1:
			m.status = "Current mode: " + m.modeSummary()
		case len(fields) >= 2:
			switch fields[1] {
			case string(workspace.ModeNormal):
				if len(fields) != 2 {
					m.status = "Usage: /mode normal"
					return false
				}
				if err := m.workspace.SetMode(workspace.ModeNormal); err != nil {
					m.setRuntimeError("Mode change failed: " + err.Error())
					return false
				}
				m.clearRuntimeError()
				m.logPromptContext()
				m.status = "Mode set to normal."
			case string(workspace.ModeExplore):
				level := m.workspace.ExploreThoroughness()
				if len(fields) == 3 {
					level = workspace.ExploreThoroughness(fields[2])
				} else if len(fields) > 3 {
					m.status = "Usage: /mode explore [quick|medium|thorough]"
					return false
				}
				if err := m.workspace.SetExploreThoroughness(level); err != nil {
					m.status = "Usage: /mode explore [quick|medium|thorough]"
					return false
				}
				if err := m.workspace.SetMode(workspace.ModeExplore); err != nil {
					m.setRuntimeError("Mode change failed: " + err.Error())
					return false
				}
				m.clearRuntimeError()
				m.logPromptContext()
				m.status = "Mode set to " + m.modeSummary() + "."
			default:
				m.status = "Usage: /mode [normal|explore [quick|medium|thorough]]"
			}
		}
	case "/agent":
		switch {
		case len(fields) == 1:
			m.status = "Current agent mode: " + string(m.currentAgentDecisionMode()) + "."
		case len(fields) == 2:
			mode, err := normalizeAgentDecisionMode(fields[1])
			if err != nil {
				m.status = "Usage: /agent [auto|plan|build|review|explore]"
				return false
			}
			m.agentMode = mode
			m.logPromptContext()
			m.status = "Agent mode set to " + string(mode) + "."
		default:
			m.status = "Usage: /agent [auto|plan|build|review|explore]"
		}
	case "/workflow":
		switch {
		case len(fields) == 1 || (len(fields) == 2 && fields[1] == "list"):
			m.status = "Workflow templates: latest-log, implement-plan, verify, frontier-audit, opencode-audit."
		case len(fields) == 2:
			prompt, ok := workflowCommandPrompt(fields[1])
			if !ok {
				m.status = "Unknown workflow. Use /workflow list."
				return false
			}
			if m.streaming {
				m.status = "Wait for the current response to finish before starting a workflow."
				return false
			}
			m.sendPromptAsync(prompt)
		default:
			m.status = "Usage: /workflow [list|latest-log|implement-plan|verify|frontier-audit|opencode-audit]"
		}
	case "/writes":
		switch {
		case len(fields) == 1:
			m.status = "Current write policy: " + m.writeSummary()
		case len(fields) == 2:
			policy := workspace.WritePolicy(fields[1])
			if err := m.workspace.SetWritePolicy(policy); err != nil {
				m.status = "Usage: /writes [guarded|auto]"
				return false
			}
			if policy == workspace.WritePolicyAuto {
				m.pendingWrite = nil
			}
			m.logPromptContext()
			m.status = "Write policy set to " + m.writeSummary() + "."
		default:
			m.status = "Usage: /writes [guarded|auto]"
		}
	case "/approve":
		if len(fields) != 2 || fields[1] != "writes" {
			m.status = "Usage: /approve writes"
			return false
		}
		if m.pendingWrite == nil {
			m.status = "There is no pending guarded write approval."
			return false
		}
		if m.streaming {
			m.status = "Wait for the current response to finish before approving writes."
			return false
		}
		proposal := *m.pendingWrite
		m.pendingWrite = nil
		m.resumeApprovedWriteAsync(proposal)
	case "/deny":
		if len(fields) != 2 || fields[1] != "writes" {
			m.status = "Usage: /deny writes"
			return false
		}
		if m.pendingWrite == nil {
			m.status = "There is no pending guarded write approval."
			return false
		}
		denied := *m.pendingWrite
		m.pendingWrite = nil
		m.status = "Cleared the pending guarded write approval."
		m.messages = append(m.messages, llama.ChatMessage{Role: "tool", Content: "Write approval denied for: " + strings.Join(denied.Paths, ", ")})
		m.logMessage("tool", "Write approval denied for: "+strings.Join(denied.Paths, ", "))
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

func (m *Model) rollbackLastJournalAsync() {
	journal := m.lastJournal
	if journal == nil {
		m.status = "No rollback journal is available for the last bounded change."
		return
	}
	m.clearRuntimeError()
	m.setPhaseCard(analysisLaneImplement, "rollback", phaseStateActive, "Restoring the last bounded change and rerunning verification.")
	m.status = "Rolling back the last bounded change."
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		rollbackWorkspace := m.newWorkspaceManager()
		if err := applyExecutionRollback(rollbackWorkspace, journal); err != nil {
			m.events <- rollbackCompleteMsg{err: err}
			return
		}
		verify := m.runImplementationVerification(ctx, rollbackWorkspace, 1, journal.Paths...)
		m.events <- rollbackCompleteMsg{summary: composeRollbackSummary(journal, verify)}
	}()
}

func (m *Model) workspaceRootForState() string {
	if m == nil || m.workspace == nil {
		return ""
	}
	return filepath.Clean(m.workspace.Root())
}

func (m *Model) logLongHorizonState(action string, stage *structuralRefactorStage, reason string) {
	if m == nil {
		return
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return
	}
	payload := struct {
		Action       string   `json:"action"`
		SourceLane   string   `json:"source_lane,omitempty"`
		Stage        int      `json:"stage,omitempty"`
		FocusPath    string   `json:"focus_path,omitempty"`
		Changed      []string `json:"changed,omitempty"`
		Remaining    []string `json:"remaining,omitempty"`
		NextStep     string   `json:"next_step,omitempty"`
		Verification string   `json:"verification,omitempty"`
		Resumable    bool     `json:"resumable"`
		Reason       string   `json:"reason,omitempty"`
	}{
		Action: action,
		Reason: strings.TrimSpace(reason),
	}
	if stage != nil {
		payload.SourceLane = strings.TrimSpace(stage.SourceLane)
		payload.Stage = stage.Stage
		payload.FocusPath = strings.TrimSpace(stage.FocusPath)
		payload.Changed = uniqueNonEmptyStrings(stage.Changed)
		payload.Remaining = uniqueNonEmptyStrings(stage.Remaining)
		payload.NextStep = strings.TrimSpace(stage.NextStep)
		payload.Verification = strings.TrimSpace(stage.Verification)
		payload.Resumable = structuralRefactorStageResumable(stage)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("long_horizon_state", string(data))
}

func (m *Model) runLocateAgent(ctx context.Context, prompt string) (string, error) {
	locateWorkspace := m.newWorkspaceManager()
	locateWorkspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))

	literal := primaryLocateLiteral(prompt)
	if literal == "" {
		return "", fmt.Errorf("locate request needs a quoted UI string or clearer target text")
	}

	m.events <- infoMsg{text: fmt.Sprintf("Locate agent is identifying where %q is rendered.", literal)}
	outcome, err := m.locatePromptTarget(ctx, locateWorkspace, prompt, literal)
	if err != nil {
		return "", err
	}
	summary := renderLocateSummary(prompt, outcome)
	if !analysisReplyAccepted(summary, []string{outcome.Best.Path}, analysisLaneLocate) {
		return "", fmt.Errorf("locate summary failed locate target contract")
	}
	return summary, nil
}

func locateFallbackPatterns(prompt string, literal string) []string {
	normalized := strings.ToLower(prompt)
	patterns := make([]string, 0, 6)
	add := func(pattern string) {
		pattern = strings.TrimSpace(strings.ToLower(pattern))
		if pattern == "" {
			return
		}
		for _, existing := range patterns {
			if existing == pattern {
				return
			}
		}
		patterns = append(patterns, pattern)
	}

	if hasUILocationHint(normalized) {
		if containsAny(normalized, "top", "top of the window", "window", "header", "banner") {
			add("header")
			add("banner")
		}
		if containsAny(normalized, "footer", "bottom") {
			add("footer")
		}
		if containsAny(normalized, "badge", "chat", "menu") {
			add("chat")
			add("badge")
			add("menu")
		}
		if strings.Contains(normalized, "status") {
			add("status")
		}
	}

	for _, field := range strings.Fields(strings.ToLower(strings.Trim(literal, "\"'` "))) {
		field = strings.Trim(field, ".,:;!?()[]{}")
		if len(field) < 4 {
			continue
		}
		add(field)
		if len(patterns) >= 6 {
			break
		}
	}
	return patterns
}

func (m *Model) tryDirectLocateRemoval(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	history []llama.ChatMessage,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || !promptRequestsRemoval(prompt) {
		return directImplementationShortcut{}
	}
	step := startStep
	for _, target := range analysisSummaryTargets(history) {
		readReq := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      target.Path,
			StartLine: maxInt(1, target.Line-1),
			EndLine:   target.Line + 1,
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}", target.Path, maxInt(1, target.Line-1), target.Line+1),
			Request:   readReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("read_file:%s:%d", target.Path, target.Line),
		})
		readResult := mgr.Execute(ctx, readReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: readResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(readResult)}
		step++
		startLine := target.Line
		endLine := target.Line
		if readResult.Error == "" {
			prevLine, _ := extractReadFileLine(readResult.Output, target.Line-1)
			currentLine, _ := extractReadFileLine(readResult.Output, target.Line)
			nextLine, _ := extractReadFileLine(readResult.Output, target.Line+1)
			if looksLikeSingleLineConditionalWrapper(prevLine, currentLine, nextLine) {
				startLine = target.Line - 1
				endLine = target.Line + 1
			}
		}
		req := workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      target.Path,
			StartLine: startLine,
			EndLine:   endLine,
			Content:   "",
		}
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
				continue
			}
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"edit_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d,\"content\":\"\"}", target.Path, startLine, endLine),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("edit_file:%s:%d-%d-remove", target.Path, startLine, endLine),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			continue
		}
		result := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		step++
		if result.Error != "" {
			continue
		}
		return directImplementationShortcut{
			applied:      true,
			changedPaths: []string{target.Path},
			summary:      fmt.Sprintf("Removed the located UI logic in %s lines %d-%d.", target.Path, startLine, endLine),
			nextStep:     step,
		}
	}
	return directImplementationShortcut{}
}

func (m *Model) tryDirectUISpacingEdit(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	packet implementationTargetPacket,
	bootstrap implementationBootstrap,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || !looksLikeUISpacingPrompt(prompt) {
		return directImplementationShortcut{}
	}
	path, startLine, endLine, _, ok := focusedRewriteWindow(packet, bootstrap)
	if !ok {
		return directImplementationShortcut{}
	}
	for _, result := range bootstrap.results {
		if result.Tool != "read_file" || result.Error != "" {
			continue
		}
		readPath, _, _, readOK := parseReadWindowSummary(result.Summary)
		if !readOK || readPath != path {
			continue
		}
		lineNumber, replacement, found := directUISpacingReplacement(result.Output, startLine, endLine)
		if !found {
			continue
		}
		req := workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      path,
			StartLine: lineNumber,
			EndLine:   lineNumber,
			Content:   replacement,
		}
		if err := preflightStructuredExecutionRequest(mgr, req); err != nil {
			m.logMessage("implement_ui_spacing", "direct UI spacing edit rejected by preflight: "+err.Error())
			return directImplementationShortcut{}
		}
		step := startStep
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
		m.logMessage("implement_ui_spacing", fmt.Sprintf("direct UI spacing edit proposed for %s line %d", path, lineNumber))
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       marshalStructuredExecutionRequest(req),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("edit_file:%s:%d-ui-spacing", path, lineNumber),
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
		if shouldInvalidateExploreCacheForTool(editResult) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", editResult.Tool),
			}
		}
		step++
		if editResult.Error != "" {
			return directImplementationShortcut{}
		}
		return directImplementationShortcut{
			applied:      true,
			changedPaths: []string{path},
			summary:      fmt.Sprintf("Applied a direct UI spacing edit in %s line %d.", path, lineNumber),
			nextStep:     step,
		}
	}
	return directImplementationShortcut{}
}

func currentWorkspaceStateToken(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}

	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	status, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(head)) + "\n" + strings.TrimSpace(string(status))
}

func (m *Model) logEffortPolicy(policy effortPolicySnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return
	}
	m.logMessage("effort_policy", string(data))
}

func (m *Model) logClaimConfidence(confidence claimConfidenceSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(confidence)
	if err != nil {
		return
	}
	m.logMessage("claim_confidence", string(data))
}

func (m *Model) logContextCompaction(compaction contextCompactionSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(compaction)
	if err != nil {
		return
	}
	m.logMessage("context_compaction", string(data))
}

func (m *Model) logToolFailureConfidence(snapshot toolFailureConfidenceSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	m.logMessage("tool_failure_confidence", string(data))
}

func (m *Model) resize() {
	headerHeight := 1
	inputHeight := 5
	footerHeight := 6
	contentHeight := m.height - headerHeight - inputHeight - footerHeight
	if contentHeight < 5 {
		contentHeight = 5
	}
	viewportWidth, _, _ := m.bodyLayout()
	if viewportWidth < 24 {
		viewportWidth = maxInt(24, m.width)
	}

	if !m.ready {
		m.viewport = viewport.New(viewportWidth, contentHeight)
		m.ready = true
	}

	m.viewport.Width = viewportWidth
	m.viewport.Height = contentHeight
	m.input.SetWidth(m.width)
	m.renderMessages(false)
}

func (m *Model) bodyLayout() (int, int, bool) {
	const (
		panelMinWidth      = 28
		transcriptMinWidth = 48
		panelGap           = 1
	)
	if !m.panelVisible || m.width <= 0 {
		return maxInt(1, m.width), 0, false
	}
	panelWidth := m.width / 3
	if panelWidth < panelMinWidth {
		panelWidth = panelMinWidth
	}
	maxPanelWidth := m.width - transcriptMinWidth - panelGap
	if maxPanelWidth < panelMinWidth {
		return maxInt(1, m.width), 0, false
	}
	if panelWidth > maxPanelWidth {
		panelWidth = maxPanelWidth
	}
	transcriptWidth := m.width - panelWidth - panelGap
	if transcriptWidth < transcriptMinWidth || panelWidth < panelMinWidth {
		return maxInt(1, m.width), 0, false
	}
	return transcriptWidth, panelWidth, true
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
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

func (m *Model) writeSummary() string {
	if m.workspace == nil {
		return string(workspace.WritePolicyGuarded)
	}
	return string(m.workspace.WritePolicy())
}

func (m *Model) modeSummary() string {
	agentMode := string(m.currentAgentDecisionMode())
	if m.workspace == nil {
		return "workspace=normal agent=" + agentMode
	}

	mode := m.workspace.Mode()
	workspaceMode := string(mode)
	if mode != workspace.ModeExplore {
		return "workspace=" + workspaceMode + " agent=" + agentMode
	}
	workspaceMode = fmt.Sprintf("%s(%s)", mode, m.workspace.ExploreThoroughness())
	return "workspace=" + workspaceMode + " agent=" + agentMode
}

func (m *Model) traceSummary() string {
	mode := string(m.traceMode)
	if m.traceMode != traceDisplayCompact {
		return mode
	}
	if m.compactTrace.HiddenCount == 0 {
		return mode + " (quiet)"
	}
	if strings.TrimSpace(m.compactTrace.LastEvent) == "" {
		return fmt.Sprintf("%s (%d hidden tool event(s))", mode, m.compactTrace.HiddenCount)
	}
	return fmt.Sprintf("%s (%d hidden; last: %s)", mode, m.compactTrace.HiddenCount, truncateText(m.compactTrace.LastEvent, 72))
}

func (m *Model) setExecutionPlanSummary(lane analysisLane, summary string, steps []string) {
	if lane != "" {
		m.executionPanel.Lane = lane
		m.executionPanel.Contract = answerContractForLane(lane)
	}
	m.executionPanel.Summary = strings.TrimSpace(summary)
	m.executionPanel.Steps = uniqueNonEmptyStrings(steps)
	m.executionPanel.UpdatedAt = time.Now()
}

func (m *Model) appendExecutionProgress(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(m.executionPanel.Progress) > 0 && m.executionPanel.Progress[len(m.executionPanel.Progress)-1] == line {
		return
	}
	const maxProgressEntries = 8
	m.executionPanel.Progress = append(m.executionPanel.Progress, line)
	if len(m.executionPanel.Progress) > maxProgressEntries {
		m.executionPanel.Progress = append([]string(nil), m.executionPanel.Progress[len(m.executionPanel.Progress)-maxProgressEntries:]...)
	}
	m.executionPanel.UpdatedAt = time.Now()
}

func (m *Model) rememberExecutionJournal(recorder *executionJournalRecorder, changedPaths []string, summary string, refactorStage *structuralRefactorStage, verify implementationVerification) {
	if recorder == nil {
		return
	}
	m.lastJournal = recorder.Finalize(changedPaths, summary)
	if m.lastJournal == nil && len(uniqueNormalizedPaths(changedPaths)) == 0 && implementationSummaryReportsNoop(summary) {
		m.lastJournal = &executionJournal{
			Prompt:      strings.TrimSpace(recorder.prompt),
			Summary:     strings.TrimSpace(summary),
			CreatedAt:   time.Now(),
			CommittedAt: time.Now(),
		}
	}
	if m.lastJournal != nil {
		m.lastJournal.VerificationCommands = append([]string(nil), verify.commands...)
		m.lastJournal.VerificationPassed = len(verify.results) > 0 && !verify.failed
		m.lastJournal.VerificationFailed = verify.failed
		m.lastJournal.VerificationConfidence = strings.TrimSpace(verify.confidence)
	}
	if m.lastJournal != nil && refactorStage != nil {
		cloned := cloneStructuralRefactorStage(refactorStage)
		m.lastJournal.Refactor = cloned
		if structuralRefactorStageResumable(cloned) {
			m.refactorStage = cloneStructuralRefactorStage(cloned)
			m.persistStructuralRefactorStage(cloned)
			m.logLongHorizonState("persist", cloned, "structural refactor has remaining bounded stages")
		} else {
			m.logLongHorizonState("clear", cloned, "structural refactor stage is no longer resumable")
			m.clearPersistedStructuralRefactorStage()
		}
	}
	if m.lastJournal != nil && strings.TrimSpace(summary) != "" {
		m.setExecutionPlanSummary(analysisLaneImplement, strings.TrimSpace(summary), changedPaths)
		if refactorStage != nil && strings.TrimSpace(refactorStage.NextStep) != "" {
			m.appendExecutionProgress("refactor next stage: " + refactorStage.NextStep)
		}
	}
}

func captureExecutionJournalResult(recorder *executionJournalRecorder, req workspace.ToolRequest) *workspace.ToolResult {
	if recorder == nil {
		return nil
	}
	if err := recorder.CaptureRequest(req); err != nil {
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Rollback journal capture failed",
			Error:   err.Error(),
		}
	}
	return nil
}

func formatPhaseElapsed(elapsed time.Duration) string {
	if elapsed < time.Second {
		return "<1s"
	}
	if elapsed < time.Minute {
		return fmt.Sprintf("%ds", int(elapsed.Round(time.Second)/time.Second))
	}
	minutes := int(elapsed / time.Minute)
	seconds := int((elapsed % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func (m *Model) resetCompactTrace() {
	m.compactTrace = compactTraceState{}
}

func (m *Model) recordCompactTrace(text string) {
	m.compactTrace.HiddenCount++
	first := strings.TrimSpace(text)
	if idx := strings.IndexByte(first, '\n'); idx >= 0 {
		first = first[:idx]
	}
	m.compactTrace.LastEvent = first
}

func (m *Model) currentModeDetails() (string, string) {
	if m.workspace == nil {
		return string(workspace.ModeNormal), string(workspace.ThoroughnessMedium)
	}
	return string(m.workspace.Mode()), string(m.workspace.ExploreThoroughness())
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

func (m *Model) setPendingAssistantContent(content string) {
	m.setPendingAssistantMessage(llama.ChatMessage{Role: "assistant", Content: content})
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
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogMessage(role, content)
}

func (m *Model) emitAssistantReply(reply string) {
	m.logMessage("assistant", reply)
	m.events <- assistantReplyMsg{text: reply, logged: true, reasoningContent: m.takeLastAssistantReasoningContent()}
}

func (m *Model) takeLastAssistantReasoningContent() string {
	reasoning := strings.TrimSpace(m.lastAssistantReasoningContent)
	m.lastAssistantReasoningContent = ""
	return reasoning
}

func (m *Model) logToolRequest(event agent.ToolRequestEvent) {
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
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogToolResult(event.Step, event.Agent, event.Result.Tool, event.Result.Summary, event.Result.Error)
}

func (m *Model) logError(kind, message string) {
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
