package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/evidence/transcript"
	"idiom/internal/platform/config"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/instructions"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/promptstack"
	runtimesession "idiom/internal/runtime/session"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/projectartifacts"
)

type serverStatusMsg struct {
	running   bool
	modelID   string
	displayID string
	err       error
	owned     bool
	source    *serverStatusSource
}

func (m *promptExecutionSession) observePlanExecuteSourceSatisfiedLifecycle(event agent.RunEvent) {
	if m == nil || !m.planExecuteActive || m.planExecuteSourceSatisfiedObserver == nil ||
		event.Role != "implementation_lifecycle_state" {

		return
	}
	var state recoverycontract.ImplementationState
	if json.Unmarshal([]byte(event.Content), &state) != nil ||
		state.TerminalDisposition != "source_satisfied" || len(state.SourceSatisfiedPaths) == 0 {

		return
	}
	accepted := make(map[string]struct{}, len(state.SourceSatisfiedPaths))
	paths := append([]string(nil), state.SourceSatisfiedPaths...)
	for _, path := range state.SourceSatisfiedPaths {
		accepted[path] = struct{}{}
	}
	if state.CanonicalMutationScope != nil {
		for _, presentation := range state.CanonicalMutationScope.Presentations {
			if _, ok := accepted[presentation.CanonicalID]; ok {
				paths = append(paths, presentation.PresentedPath)
			}
		}
	}
	m.planExecuteSourceSatisfiedObserver(plancontract.UniqueScopePaths(paths))
}

// serverStatusSource correlates a detached worker's readiness result with the
// live backend selection it snapshotted. The revision catches alias-only model
// swaps that leave the controller and server spec unchanged.
type serverStatusSource struct {
	backendRevision uint64
	controller      *llama.ServerController
	kind            string
	endpoint        string
	model           string
	displayName     string
}

func (source serverStatusSource) matches(model *Model) bool {
	if model == nil {
		return false
	}

	current := currentServerStatusSource(model.backendRevision, model.controller, model.plannerSpec)

	return source.backendRevision == current.backendRevision &&
		source.controller == current.controller &&
		source.kind == current.kind &&
		source.endpoint == current.endpoint &&
		source.model == current.model &&
		source.displayName == current.displayName
}

type serverStartRequestedMsg struct {
	source *serverStatusSource
}

type streamDoneMsg struct {
	err error
}

type thoughtMsg struct {
	step int
	text string
}

type transcriptSavedMsg struct {
	path string
	err  error
}

type clipboardCopyDoneMsg struct {
	target string
	bytes  int
	err    error
}

func New(cfg config.Config) *Model {
	in := textarea.New()

	in.Placeholder = "Type a prompt. Enter sends. /help shows commands."
	in.Focus()

	in.ShowLineNumbers = false
	in.SetHeight(1)
	in.KeyMap.InsertNewline.SetEnabled(false)

	in.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorAccent)
	in.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(colorDim)

	spin := spinner.New()

	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(colorSpinner)
	agentModeName, _ := runprofile.ParseAgentMode(string(cfg.Chat.AgentMode))

	backendResources := runtimesession.ResolveBackendResources(cfg)
	for _, warning := range backendResources.Warnings {
		fmt.Fprintf(os.Stderr, "lifecycle: backends role %q resolution failed: %v\n", warning.Role, warning.Err)
	}

	m := &Model{
		cfg:                   cfg,
		plannerSpec:           backendResources.PlannerSpec,
		executorSpec:          backendResources.ExecutorSpec,
		client:                backendResources.PlannerClient,
		controller:            backendResources.PlannerController,
		implementerClient:     backendResources.ExecutorClient,
		implementerController: backendResources.ExecutorController,
		managedServers:        newManagedServerRegistry(),
		backgroundOperations:  newBackgroundOperationGroup(),
		summaryClient:         backendResources.ClassifierClient,
		summaryController:     backendResources.ClassifierController,
		summaryServerState:    newSummaryServerLaunchState(),
		events:                make(chan tea.Msg, 256),
		input:                 in,
		spinner:               spin,
		promptRuntimeState: promptRuntimeState{
			promptRuntimeSnapshotState: promptRuntimeSnapshotState{
				activeProfiles: append([]string(nil), cfg.Chat.DefaultProfiles...),
			},
		},
		status:        "Booting up.",
		bootingServer: false,
		agentModeName: agentModeName,
		traceMode:     traceDisplayCompact,
	}

	m.workspace = workspace.NewManager(cfg.Workspace)
	if cfg.Chat.AttachInstructionFiles {
		if candidates, explicit := instructionAttachmentCandidates(cfg.Chat); explicit {
			if len(candidates) > 0 {
				m.instructionAttacher = instructions.NewAttacher(m.workspace.Root(), candidates...)
			}
		} else {
			m.instructionAttacher = instructions.NewAttacher(m.workspace.Root())
		}
	}

	mode := modepolicy.ModeOrDefault(string(m.agentModeName))
	m.applyModePermissions(mode)

	m.toolSurfaceSetup = workspace.ConfigureToolSurface(context.Background(), cfg, workspace.ConfiguredToolSurfaceOptions{
		Manager: m.workspace,
		Runtime: true,
	})
	if failures := m.toolSurfaceSetup.MCPFailures; len(failures) > 0 {
		m.setContextError("MCP setup: " + strings.Join(failures, "; "))
	}
	if extErr := m.toolSurfaceSetup.ExternalToolsError; extErr != nil {
		m.setContextError("External tools setup: " + extErr.Error())
	}

	if err := m.reloadPromptStackWithProfiles(m.activeProfiles); err != nil {
		m.setContextError("Prompt context failed: " + err.Error())
	}

	if err := m.loadInputHistoryFromLogs(); err != nil {
		m.setContextError("Input history failed: " + err.Error())
	}

	m.initDebugLogger()

	return m
}

// UseConfigPlannerModel makes prompt-mode commands honor the explicit config
// planner target instead of project role aliases. Interactive TUI sessions keep
// role aliases as their default; per-command overrides such as -model need a
// stronger contract so nested workflows cannot drift back to local defaults.
func (m *Model) UseConfigPlannerModel() {
	if m == nil {
		return
	}

	spec := m.cfg.LlamaServer
	callTimeout := time.Duration(m.cfg.Chat.RequestTimeoutSeconds) * time.Second
	priorClient := m.client
	classifierReusedPlanner := m.summaryClient != nil && m.summaryClient == priorClient
	if m.managedServer && m.controller != nil {
		if m.managedServers == nil {
			m.managedServers = newManagedServerRegistry()
		}
		m.managedServers.add(m.controller)
	}

	m.backendRevision++
	m.bootingServer = false
	m.managedServer = false
	m.serverUp = false
	m.plannerSpec = spec
	client := llama.NewClientForServer(spec, callTimeout)
	m.client = client
	if spec.IsLaunchable() {
		m.controller = llama.NewServerController(spec, client)
	} else {
		m.controller = nil
	}
	if classifierReusedPlanner {
		m.summaryClient = client
		m.summaryController = nil
	}
	m.modelID = resolveModelID(spec, spec.Model)
	m.modelDisplayID = resolveModelDisplayID(spec, spec.Model)
	m.modelThinkingFloor = spec.IsHosted() || spec.IsMLX()
	m.invalidateComposedPrompt()
}

func instructionAttachmentCandidates(chat config.Chat) ([]string, bool) {
	if chat.ProjectPromptCandidates == nil {
		return nil, false
	}

	var candidates []string

	seen := map[string]struct{}{}

	for _, name := range chat.ProjectPromptCandidates {
		name = strings.TrimSpace(name)
		if name == "" || name != filepath.Base(name) {
			continue
		}

		switch name {
		case ".qwen.md", "QWEN.md", "IDIOM.md", "AGENTS.md", "CLAUDE.md":
		default:
			continue
		}

		if _, ok := seen[name]; ok {
			continue
		}

		seen[name] = struct{}{}
		candidates = append(candidates, name)
	}

	return candidates, true
}

// modeStepCap applies the Mode's StepCap on top of the config.MaxToolSteps
// ceiling. Zero StepCap means "no per-mode cap".
func modeStepCap(configured int, mode modepolicy.Mode) int {
	if mode.StepCap > 0 && (configured <= 0 || mode.StepCap < configured) {
		return mode.StepCap
	}

	return configured
}

func modeRunPolicy(mode modepolicy.Mode, cfg config.Chat) agent.RunPolicy {
	policy := agent.ModePolicy(mode)
	if cfg.MaxContextChars > 0 {
		policy.MaxContextChars = cfg.MaxContextChars
	}

	if cfg.MaxContextMessages > 0 {
		policy.MaxContextMessages = cfg.MaxContextMessages
	}

	if cfg.MaxModelCalls > 0 {
		policy.MaxModelCalls = cfg.MaxModelCalls
	}

	if cfg.MaxOperationalModelCalls > 0 {
		policy.MaxOperationalModelCalls = cfg.MaxOperationalModelCalls
	}

	return policy
}

func (m *Model) Init() tea.Cmd {
	bootstrap := m.bootstrapCommand()

	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.waitForEventCmd(),
		bootstrap,
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	rearmEventWait := false

	if wrapped, ok := msg.(eventMsg); ok {
		m.eventWaitArmed = false
		msg = wrapped.msg
		rearmEventWait = true
	}

	if filteredMsg, changed := m.filterTerminalMouseReportFragmentKey(msg); changed {
		if filteredMsg == nil {
			if rearmEventWait {
				cmds = append(cmds, m.waitForEventCmd())
			}

			return m, tea.Batch(cmds...)
		}

		msg = filteredMsg
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		cmd, done := m.handleWindowSize(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.KeyMsg:
		cmd, done := m.handleKeyPress(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.MouseMsg:
		cmd, done := m.handleMouse(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case spinner.TickMsg:
		cmd, done := m.handleSpinnerTick(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case serverStatusMsg:
		cmd, done := m.handleServerStatus(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case serverStartRequestedMsg:
		if msg.source == nil || msg.source.matches(m) {
			m.startServerAsync()
		}
	case streamDoneMsg:
		cmd, done := m.handleStreamDone(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case promptWorkerResultMsg:
		cmd, done := m.handlePromptWorkerResult(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case undoCompletedMsg:
		cmd, done := m.handleUndoCompleted(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case promptTranscriptMsg:
		cmd, done := m.handlePromptTranscript(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case approvalPendingMsg:
		cmd, done := m.handleApprovalPending(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case approvalAwaitingMsg:
		m.handleApprovalAwaiting(msg)
	case approvalClearedMsg:
		cmd, done := m.handleApprovalCleared(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case planApprovalPendingMsg:
		cmd, done := m.handlePlanApprovalPending(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case planApprovalClearedMsg:
		cmd, done := m.handlePlanApprovalCleared(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case laneSwitchPopupMsg:
		m.openLaneSwitchPopup(msg.objective, msg.recommended)
	case thoughtMsg:
		cmd, done := m.handleThought(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case transcriptSavedMsg:
		cmd, done := m.handleTranscriptSaved(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case clipboardCopyDoneMsg:
		cmd, done := m.handleClipboardCopyDone(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case compactHistoryDoneMsg:
		cmd, done := m.handleCompactHistoryDone(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case infoMsg:
		cmd, done := m.handleInfoMessage(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case toolTraceMsg:
		cmd, done := m.handleToolTrace(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case budgetLedgerMsg:
		cmd, done := m.handleBudgetLedger(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case evidenceCollapseMsg:
		cmd, done := m.handleEvidenceCollapse(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case historyAuditMsg:
		cmd, done := m.handleHistoryAudit(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case historySummaryMsg:
		cmd, done := m.handleHistorySummary(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case historyPanelRefreshMsg:
		cmd, done := m.handleHistoryPanelRefresh(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case planEditorDoneMsg:
		cmd, done := m.handlePlanEditorDone(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case planRefreshMsg:
		cmd, done := m.handlePlanRefresh(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case planPhaseMsg:
		cmd, done := m.handlePlanPhase(msg)
		if done {
			return m, cmd
		}

		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case planApprovalRequiredMsg:
		m.setHistoryPanelOpen(true)
		m.panelTab = panelTabPlan
		m.history.focused = true
		m.renderPlanPanelContent()
	case nil:
	default:
	}

	if scrollableViewportKey(msg) {
		m.updateViewport(msg)
	}

	var inputCmd tea.Cmd

	m.input, inputCmd = m.input.Update(msg)
	m.updateSlashSuggestions()

	oldInputHeight := m.input.Height()
	if m.ready && m.syncInputHeight() != oldInputHeight {
		m.resize()
	}

	cmds = append(cmds, inputCmd)
	if rearmEventWait {
		cmds = append(cmds, m.waitForEventCmd())
	}

	return m, tea.Batch(cmds...)
}

func (m *promptExecutionSession) ensureServerReady(ctx context.Context) error {
	if m.plannerSpec.IsHosted() {
		m.serverUp = true
		m.modelID = resolveModelID(m.plannerSpec, m.plannerSpec.Model)
		m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, m.plannerSpec.Model)
		m.reportServerStatus(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID})

		return nil
	}

	models, err := m.client.ListModels(ctx)
	if err == nil && len(models) > 0 {
		m.serverUp = true
		m.modelID = resolveModelID(m.plannerSpec, models[0].ID)
		m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, models[0].ID)
		m.reportServerStatus(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID})

		return nil
	}

	if m.modelSwapped {
		if err != nil {
			return fmt.Errorf("swapped model %q is unavailable at %s: %w", m.modelID, m.plannerSpec.EndpointBaseURL(), err)
		}

		return fmt.Errorf("swapped model %q is unavailable at %s: no models reported", m.modelID, m.plannerSpec.EndpointBaseURL())
	}

	if !m.cfg.Chat.AutoStartServer {
		return fmt.Errorf("no llama server detected and chat.auto_start_server is false")
	}

	if m.controller == nil {
		return fmt.Errorf("no launchable backend controller configured")
	}

	if err := m.controller.Start(ctx); err != nil {
		m.reportServerStatus(serverStatusMsg{err: err})

		return err
	}

	m.managedServer = true
	m.managedServers.add(m.controller)
	if err := m.controller.WaitUntilReady(ctx); err != nil {
		m.reportServerStatus(serverStatusMsg{err: err, owned: true})

		return err
	}

	models, err = m.client.ListModels(ctx)
	if err != nil || len(models) == 0 {
		m.serverUp = true
		m.modelID = resolveModelID(m.plannerSpec, "local-model")
		m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, "local-model")
		m.reportServerStatus(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID, owned: true})

		return nil
	}

	m.serverUp = true
	m.modelID = resolveModelID(m.plannerSpec, models[0].ID)
	m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, models[0].ID)
	m.reportServerStatus(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID, owned: true})

	return nil
}

func (m *promptExecutionSession) reportServerStatus(msg serverStatusMsg) {
	source := currentServerStatusSource(m.backendRevision, m.controller, m.plannerSpec)
	msg.source = &source
	m.queueUIEvent(msg)
}

func (m *Model) saveTranscriptAsync() {
	m.status = "Saving transcript."

	messages := transcriptMessages(m.messages)
	saveDir := m.cfg.Chat.SaveDir
	events := m.events
	if events == nil {
		path, err := transcript.SaveMarkdown(saveDir, messages)
		_, _ = m.handleTranscriptSaved(transcriptSavedMsg{path: path, err: err})

		return
	}

	_ = m.ensureBackgroundOperations().start(func(ctx context.Context) {
		path, err := transcript.SaveMarkdown(saveDir, messages)
		select {
		case events <- transcriptSavedMsg{path: path, err: err}:
		case <-ctx.Done():
		}
	})
}

func transcriptMessages(messages []llama.ChatMessage) []transcript.Message {
	out := make([]transcript.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, transcript.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	return out
}

// sendPromptAsync starts a detached interactive execution. runPromptOnce owns
// the worker's private transcript; typed projection events let Update apply
// the same messages to the live transcript without sharing mutable Model state.
func (m *Model) sendPromptAsync(prompt string) {
	m.streaming = true
	m.workingFrame = 0
	m.thoughts.reset()
	m.currentTurnBusyStatus = busyStatusForPrompt(prompt)
	m.beginAgentHistoryTurn()

	m.startPromptExecution(func(ctx context.Context, worker *promptExecutionSession) (string, error) {
		return worker.runPromptOnce(ctx, prompt)
	})
}

func (m *Model) sendPlanExecuteCommandAsync(objective string) {
	m.streaming = true
	m.workingFrame = 0
	m.thoughts.reset()
	m.currentTurnBusyStatus = busyStatusForPrompt(objective)
	m.beginAgentHistoryTurn()

	m.startPromptExecution(func(ctx context.Context, worker *promptExecutionSession) (string, error) {
		return worker.runPlanExecuteCommandOnce(ctx, objective)
	})
}

func (m *Model) sendImplementCommandAsync(objective string) {
	m.streaming = true
	m.workingFrame = 0
	m.thoughts.reset()
	m.currentTurnBusyStatus = busyStatusForPrompt(objective)
	m.beginAgentHistoryTurn()

	m.startPromptExecution(func(ctx context.Context, worker *promptExecutionSession) (string, error) {
		return worker.runImplementCommandOnce(ctx, objective)
	})
}

func formatToolRequestTrace(event agent.ToolRequestEvent) string {
	tool := strings.TrimSpace(event.Request.Tool)
	if tool == "" {
		tool = "tool"
	}

	target := toolRequestTraceTarget(event.Request)
	if target == "" {
		target = strings.TrimSpace(event.Action)
	}

	if target == "" {
		return tool + " | running"
	}

	return tool + " | running " + truncateRunes(collapseWhitespace(target), 120)
}

func toolRequestIntent(event agent.ToolRequestEvent) string {
	req := event.Request
	if desc := strings.TrimSpace(req.Description); desc != "" {
		return truncateRunes(collapseWhitespace(desc), 160)
	}

	if gap := strings.TrimSpace(req.NamedGap); gap != "" {
		return "resolve named evidence gap: " + truncateRunes(collapseWhitespace(gap), 120)
	}

	target := truncateRunes(collapseWhitespace(toolRequestTraceTarget(req)), 96)
	tool := strings.TrimSpace(req.Tool)
	if workspace.IsWorkspacePerEditWriteTool(tool) {
		if target == "" {
			return "apply the planned code change"
		}

		return "apply the planned change in " + target
	}

	if languagetools.ToolHasAnyCapability(tool, semanticlanguage.CapFileOutline) {
		if target == "" {
			return "scan the file structure before reading details"
		}

		return "scan " + target + " to find the right symbol or section"
	}

	if languagetools.ToolHasAnyCapability(tool, semanticlanguage.CapReadSymbol) {
		if target == "" {
			return "read the exact declaration instead of a broad file slice"
		}

		return "read the exact declaration around " + target
	}

	switch tool {
	case workspace.ToolNameContextPack:
		if target == "" {
			target = strings.TrimSpace(req.Task)
		}

		if target == "" {
			return "build a ranked context packet before choosing exact reads"
		}

		return "build a ranked context packet for " + truncateRunes(collapseWhitespace(target), 96)
	case workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameContextImpact, workspace.ToolNameDependencyGraph:
		if target == "" {
			return "trace code relationships without broad file reads"
		}

		return "trace code relationships around " + target
	case workspace.ToolNameDiscoveryQuery:
		purpose := strings.TrimSpace(req.Purpose)
		if purpose == "" {
			purpose = "repository discovery"
		}

		if target == "" {
			return "run structured " + purpose + " discovery"
		}

		return "run structured " + purpose + " discovery around " + target
	case workspace.ToolNameListFiles:
		if target == "" {
			target = "the current workspace"
		}

		return "map " + target + " before choosing what to inspect"
	case workspace.ToolNameSearchFiles:
		if target == "" {
			return "find candidate files for the next inspection step"
		}

		return "find candidate files matching " + target
	case workspace.ToolNameSearchContent:
		if target == "" {
			return "locate references before opening files"
		}

		return "locate references to " + target
	case workspace.ToolNameReadFile:
		if target == "" {
			return "inspect source context needed for this turn"
		}

		return "inspect " + target + " for the relevant context"
	case workspace.ToolNameTaskLedger:
		return "keep the visible plan synchronized with the work"
	case workspace.ToolNameShell:
		if target == "" {
			return "inspect or verify state with a command"
		}

		if workspace.AnalyzeShellCommand(target).BuildOrTest {
			return "verify the current state by running " + target
		}

		return "inspect workspace state by running " + target
	case "child_task":
		if target == "" {
			return "delegate a bounded subtask while continuing the main task"
		}

		return "delegate a bounded subtask about " + target
	}

	if action := strings.TrimSpace(event.Action); action != "" {
		return "perform the requested " + truncateRunes(collapseWhitespace(action), 96) + " tool step"
	}

	if target != "" {
		return "gather context from " + target
	}

	return "gather context needed for this turn"
}

func toolRequestTraceTarget(req workspace.ToolRequest) string {
	switch {
	case strings.TrimSpace(req.Path) != "":
		target := strings.TrimSpace(req.Path)
		if req.StartLine > 0 && req.EndLine > 0 {
			target += fmt.Sprintf(":%d-%d", req.StartLine, req.EndLine)
		} else if req.StartLine > 0 {
			target += fmt.Sprintf(":%d", req.StartLine)
		} else if req.EndLine > 0 {
			target += fmt.Sprintf(":1-%d", req.EndLine)
		}

		return target
	case strings.TrimSpace(req.Pattern) != "":
		target := strings.TrimSpace(req.Pattern)
		if strings.TrimSpace(req.Glob) != "" {
			target += " in " + strings.TrimSpace(req.Glob)
		}

		return target
	case strings.TrimSpace(req.Glob) != "":
		return strings.TrimSpace(req.Glob)
	case strings.TrimSpace(req.Command) != "":
		return strings.TrimSpace(req.Command)
	case strings.TrimSpace(req.Workdir) != "":
		return strings.TrimSpace(req.Workdir)
	case strings.TrimSpace(req.Destination) != "":
		return strings.TrimSpace(req.Destination)
	case len(req.Declarations) > 0:
		return strings.Join(req.Declarations, ", ")
	case len(req.Moves) > 0:
		return fmt.Sprintf("%d declaration move(s)", len(req.Moves))
	case len(req.Items) > 0:
		return fmt.Sprintf("%d task item(s)", len(req.Items))
	case len(req.Todos) > 0:
		return fmt.Sprintf("%d todo item(s)", len(req.Todos))
	case strings.TrimSpace(req.Agent) != "":
		return strings.TrimSpace(req.Agent)
	case strings.TrimSpace(req.Prompt) != "":
		return strings.TrimSpace(req.Prompt)
	default:
		return ""
	}
}

// runAgentTurn drives a single agent.Runner.Run invocation for the active
// Mode. Uses the workspace.Manager's active ToolRules (populated from the
// Mode in New() and /mode) to block/ask per-tool; each turn receives the
// current mode's step cap when its runner is constructed below.
func (m *promptExecutionSession) runAgentTurn(ctx context.Context, prompt string, history []llama.ChatMessage, opts llama.ChatOptions, plan promptExecutionPlan) (string, error) {
	mode := currentMode(m.agentModeName, m.cfg.Chat.AgentMode)
	steps := modeStepCap(m.cfg.Chat.MaxToolSteps, mode)
	policy := modeRunPolicy(mode, m.cfg.Chat)
	if m.nonInteractive && steps > 0 {
		policy.MaxToolSteps = steps
	}
	// Conversation and read-only lanes: raise context compaction
	// thresholds. Structured discovery tools return compact results
	// but the default 40-message limit compacts every ~13 tool calls,
	// destroying working memory before the model can synthesize.
	if plan.route.Family == string(autonomy.TaskFamilyConversation) || plan.route.Contract == string(workerruntime.ContractDirectAnswer) {
		budget := NewContextBudget(compactionContextSize(m.cfg.LlamaServer.CtxSize, m.cfg.RouteClassifier.CtxSize))
		maxMsgs, maxChars := budget.ConvMaxMessages, budget.ConvMaxChars

		policy.MaxContextMessages = maxMsgs
		policy.MaxContextChars = maxChars
	}

	// Conversation and read-only lanes: do not cap tool steps or
	// model calls. The model owns the decision of when evidence is
	// sufficient. The doom-loop guard catches repeated identical
	// calls; genuine investigation should not be preempted by a
	// budget wall.
	if shouldUnboundDirectPromptBudgets(m.nonInteractive, plan.route) {

		policy.MaxModelCalls = 0
		policy.MaxToolSteps = 0
		steps = 0
	}

	runtimeBrief := workerruntime.NormalizeRuntimeBrief(plan.runtimeBrief)
	seedResults := m.LastTurnSourceResultsForObjective(prompt)
	if activePlanSeed, ok := m.activeFeaturePlannerPlanSeedResult(); ok && (plan.route.Family == string(autonomy.TaskFamilyConversation) || plan.route.Contract == string(workerruntime.ContractDirectAnswer)) {
		seedResults = append(seedResults, activePlanSeed)
	}
	seedImplementationState := cloneRecoveryImplementationState(m.resumeImplementationState)
	m.resumeImplementationState = nil
	var lastAssistantReasoningContent string

	runner := &agent.Runner{
		Client:                  m.client,
		ImplementerClient:       m.implementerClient,
		Workspace:               m.workspace,
		MaxToolSteps:            steps,
		Policy:                  policy,
		AgentName:               "main",
		SeedResults:             seedResults,
		SeedImplementationState: seedImplementationState,
		RuntimeBrief:            runtimeBrief,
		InstructionAttacher:     m.instructionAttacher,
		ContextSize:             m.cfg.LlamaServer.CtxSize,
		ReadApprovalGate:        m.newReadApprovalGate(),
		OnEvent: func(event agent.RunEvent) {
			m.observePlanExecuteSourceSatisfiedLifecycle(event)
			m.logRunEvent(event)
		},
		OnThought: func(event agent.ThoughtEvent) {
			m.queueUIEvent(thoughtMsg{step: event.Step, text: event.Text})
		},
		OnAssistantMessage: func(message llama.ChatMessage) {
			lastAssistantReasoningContent = strings.TrimSpace(message.ReasoningContent)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			return m.preExecuteToolRequest(step, req, mode)
		},
		DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
			return m.requestApproval(req, string(mode.Name))
		},
	}
	// Collect source-body tool results from this turn to seed the next.
	var turnSourceResults []workspace.ToolResult

	collectSourceResult := func(result workspace.ToolResult) {
		if strings.TrimSpace(result.Error) != "" {
			return
		}

		tool := strings.TrimSpace(result.Tool)
		switch tool {
		case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability, workspace.ToolNameContextZoom:
			if strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != "" {
				turnSourceResults = append(turnSourceResults, result)
			}
		default:
			if strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body") {
				turnSourceResults = append(turnSourceResults, result)
			}
		}
	}

	_ = collectSourceResult // used in the onToolResult callback below

	answer, err := runner.Run(
		ctx,
		history,
		opts,
		func(status string) {
			m.queueUIEvent(infoMsg{text: status})
		},
		func(event agent.ToolRequestEvent) {
			m.logToolRequest(event)

			if !m.planExecuteActive {
				m.queueUIEvent(toolTraceMsg{
					step:       event.Step,
					text:       formatToolRequestTrace(event),
					intent:     toolRequestIntent(event),
					busyStatus: busyStatusForToolRequest(event, prompt),
					auditKind:  historyToolAuditKind(event),
					running:    true,
				})
			}
		},
		func(event agent.ToolResultEvent) {
			m.logToolResult(event)
			m.pinPathsFromToolResult(event.Result)
			m.recordMutationRollback(ctx, event.Result)
			m.recordWriteFailure(event.Step, event.Request, event.Result)
			m.recordWriteSuccess(event.Step, event.Request, event.Result)
			m.recordTaskLedger(event.Request, event.Result)
			m.recordSubmittedPlan(event.Request, event.Result)
			collectSourceResult(event.Result)

			if workspace.IsWorkspaceWriteTool(event.Request.Tool) && strings.TrimSpace(event.Result.Error) == "" {
				if err := m.reloadPromptStack(); err != nil {
					m.setContextError("Prompt context failed: " + err.Error())
				}
			}

			if !m.planExecuteActive {
				m.queueUIEvent(toolTraceMsg{
					step:        event.Step,
					text:        workspace.FormatToolTrace(event.Result),
					busyStatus:  busyStatusAfterToolResult(event),
					outcome:     historyToolAuditOutcome(event),
					auditEvents: historyAuditEventsForToolResult(event),
				})
			}

			if workspace.IsWorkspaceWriteTool(event.Request.Tool) || event.Request.Tool == workspace.ToolNameTaskLedger || strings.TrimSpace(event.Result.Tool) == workspace.ToolNameSubmitPlan {
				m.queueUIEvent(planRefreshMsg{})
			}
		},
	)
	if err != nil {
		if seedImplementationState != nil {
			switch {
			case agent.IsRecoveredMutationStateMismatch(err):
				m.emitMutationStateResumeAudit(mutationStateResumeAudit{
					Status:             "mismatch",
					Reason:             "fresh_scope_disagrees_with_persisted_projection",
					ScopePresent:       seedImplementationState.CanonicalMutationScope != nil,
					CommitReceiptCount: len(seedImplementationState.CommitReceipts),
				})
			case agent.IsRecoveredMutationStateInvalid(err):
				m.emitMutationStateResumeAudit(mutationStateResumeAudit{
					Status:             "rejected",
					Reason:             "runtime_projection_invalid",
					ScopePresent:       seedImplementationState.CanonicalMutationScope != nil,
					CommitReceiptCount: len(seedImplementationState.CommitReceipts),
				})
			}
		}
		return "", err
	}

	m.lastAssistantReasoningContent = lastAssistantReasoningContent
	// Store source-body results for follow-up turns only in conversation
	// and direct-answer lanes — these are the routes where the user may
	// ask a follow-up question without re-reading source files.
	if plan.route.Family == string(autonomy.TaskFamilyConversation) || plan.route.Family == string(autonomy.TaskFamilyDirectAnswer) || plan.route.Contract == string(workerruntime.ContractDirectAnswer) {
		m.SetLastTurnSourceResults(prompt, turnSourceResults)
	} else {
		m.ClearLastTurnSourceResults()
	}

	return answer, err
}

func shouldUnboundDirectPromptBudgets(nonInteractive bool, route turnRoutePlan) bool {
	if nonInteractive {
		return false
	}

	return route.Family == string(autonomy.TaskFamilyConversation) || route.Family == string(autonomy.TaskFamilyDirectAnswer) ||
		route.Contract == string(workerruntime.ContractDirectAnswer) || route.Contract == string(workerruntime.ContractGroundedEvidence)
}

func lastAssistantAnswer(history []llama.ChatMessage) (string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}

		content := strings.TrimSpace(msg.Content)
		if content != "" {
			return content, true
		}
	}

	return "", false
}

func (m *promptExecutionSession) preExecuteToolRequest(step int, req workspace.ToolRequest, mode modepolicy.Mode) *workspace.ToolResult {
	if m == nil || m.workspace == nil {
		return nil
	}

	switch m.workspace.EvaluateToolRule(req) {
	case toolpolicy.Allow:
		return nil
	case toolpolicy.Deny:
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Tool request blocked by permission rules",
			Error:   fmt.Sprintf("permission rules deny %s under agent mode %q", req.Tool, mode.Name),
		}
	case toolpolicy.Ask:
		if workspace.IsWorkspaceWriteTool(req.Tool) {
			return nil
		}
		if m.requestApproval(req, string(mode.Name)) {
			return nil
		}

		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Write approval required",
			Error:   fmt.Sprintf("write approval required before running %s under agent mode %q; request was not approved", req.Tool, mode.Name),
		}
	}

	return nil
}

// pinPathsFromToolResult attaches any file a successful tool result touched
// to the session pin set so it rides along in future turn payloads.
func (m *promptExecutionSession) pinPathsFromToolResult(result workspace.ToolResult) {
	if m == nil || strings.TrimSpace(result.Error) != "" {
		return
	}

	registry := m.pinnedFilesRegistry()
	if registry == nil {
		return
	}

	tool := strings.TrimSpace(result.Tool)
	if tool == workspace.ToolNameReadFile ||
		tool == workspace.ToolNameEditFile ||
		tool == workspace.ToolNameWriteFile ||
		tool == workspace.ToolNameAppendFile ||
		languagetools.ToolHasAnyCapability(tool, semanticlanguage.CapReadSymbol, semanticlanguage.CapExtractBody, semanticlanguage.CapFileOutline) {

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
	if suppressDefaultDebugLogger(m.cfg.Chat.DebugLogDir) {
		return
	}

	logger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:           m.cfg.Chat.DebugLogDir,
		Mode:          m.cfg.Chat.DebugLogMode,
		WorkspaceRoot: m.workspace.Root(),
		SessionID:     m.cfg.Workspace.SessionID,
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
		PromptSources:  transcriptPromptSources(m.systemPromptSources()),
		SystemPrompt:   m.systemPrompt(),
		WorkspaceRoot:  m.workspace.Root(),
		WorkspaceCWD:   m.workspace.CWD(),
		CtxSize:        m.cfg.LlamaServer.CtxSize,
	})
	m.sessionStartedAt = time.Now()

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

	derivation := derivePromptComposition(
		m.agentModeName,
		m.cfg.Chat.AgentMode,
		m.cfg.Chat.ModelFamily,
		m.plannerSpec.Model,
		m.cfg.LlamaServer.Model,
	)
	result := m.promptRuntimeCompletionState.resolveComposedPrompt(
		m.promptStack,
		derivation,
		func(existing []promptstack.Source) systemPromptFragments {
			return loadModeSystemPromptFragments(
				derivation.mode,
				resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace),
				existing,
			)
		},
	)

	return result.text
}

func (m *Model) systemPromptSources() []promptstack.Source {
	if m == nil {
		return nil
	}

	_ = m.systemPrompt()

	return append([]promptstack.Source(nil), m.composedPromptSources...)
}

func loadModeSystemPromptFragments(mode modepolicy.Mode, projectRoot string, existing []promptstack.Source) systemPromptFragments {
	if len(mode.SystemPromptFiles) == 0 {
		return systemPromptFragments{}
	}

	sections := make([]string, 0, len(mode.SystemPromptFiles))
	sources := make([]promptstack.Source, 0, len(mode.SystemPromptFiles))
	seen := make(map[string]struct{}, len(mode.SystemPromptFiles))
	seenBodies := make(map[string]struct{}, len(existing))

	for _, source := range existing {
		path := strings.TrimSpace(source.Path)
		if path == "" {
			continue
		}

		if !filepath.IsAbs(path) && projectRoot != "" {
			path = filepath.Join(projectRoot, path)
		}

		path = filepath.Clean(path)
		seen[path] = struct{}{}

		if data, err := os.ReadFile(path); err == nil {
			if body := strings.TrimSpace(string(data)); body != "" {
				seenBodies[body] = struct{}{}
			}
		}
	}

	for _, relOrAbs := range mode.SystemPromptFiles {
		path := strings.TrimSpace(relOrAbs)
		if path == "" {
			continue
		}

		if !filepath.IsAbs(path) && projectRoot != "" {
			path = filepath.Join(projectRoot, path)
		}

		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		trimmed := strings.TrimSpace(string(data))
		if trimmed != "" {
			if _, ok := seenBodies[trimmed]; ok {
				continue
			}

			seenBodies[trimmed] = struct{}{}
			sections = append(sections, trimmed)
			sources = append(sources, promptstack.Source{
				Kind:        "mode",
				DisplayName: "mode:" + string(mode.Name) + ":" + filepath.Base(path),
				Path:        path,
			})
		}
	}

	return systemPromptFragments{
		Text:    strings.Join(sections, "\n\n"),
		Sources: sources,
	}
}

func transcriptPromptSources(sources []promptstack.Source) []transcript.PromptSource {
	if len(sources) == 0 {
		return nil
	}

	out := make([]transcript.PromptSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, transcript.PromptSource{
			Kind:        source.Kind,
			DisplayName: source.DisplayName,
			Path:        source.Path,
		})
	}

	return out
}

func (m *Model) reloadPromptStackWithProfiles(profiles []string) error {
	chat := m.cfg.Chat
	if strings.TrimSpace(chat.ProjectRoot) != "" {
		if _, err := projectartifacts.Ensure(chat.ProjectRoot); err != nil {
			return err
		}
	}

	stack, err := buildPromptStack(chat, profiles)
	if err != nil {
		return err
	}
	m.promptRuntimeCompletionState.applyPromptStackBuild(stack)

	if len(stack.Warnings) > 0 {
		m.setContextError(strings.Join(stack.Warnings, "; "))
	}

	return nil
}
