package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/agent"
	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/projectartifacts"
	"idiom/internal/promptstack"
	"idiom/internal/transcript"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

// backendsModelToLlamaServer converts a backends.Model entry into a
// config.LlamaServer suitable for both client construction and the
// llama.ServerController launcher. Fields the backends schema doesn't
// carry (CtxSize, Threads, Reasoning*, LogFile, etc.) inherit from
// `defaults` so a sparsely-declared backends.json entry still produces
// a working spec — the project's existing config.json fields fill the
// rest.
//
// out.Model resolves in this priority order: the Model entry's hosted/API
// model id, then ModelPath (direct-mode file path), then the Model entry's
// Alias (proxy-mode identifier — overrides any inherited path because the alias
// is what the proxy routes by), then the inherited defaults.
//
// When backends.Model.Host is empty, the host falls back to defaults
// or 127.0.0.1 — every shipped config uses 127.0.0.1 and a missing
// host in backends.json shouldn't be a hard error.
func backendsModelToLlamaServer(m backends.Model, defaults config.LlamaServer) config.LlamaServer {
	out := defaults
	if k := strings.TrimSpace(m.Kind); k != "" {
		out.Kind = k
	}
	if b := strings.TrimSpace(m.Binary); b != "" {
		out.Binary = b
	}
	if h := strings.TrimSpace(m.Host); h != "" {
		out.Host = h
	} else if strings.TrimSpace(out.Host) == "" {
		out.Host = "127.0.0.1"
	}
	if m.Port > 0 {
		out.Port = m.Port
		if strings.TrimSpace(m.BaseURL) == "" {
			out.BaseURL = ""
		}
	}
	if model := strings.TrimSpace(m.Model); model != "" {
		out.Model = model
	} else if mp := strings.TrimSpace(m.ModelPath); mp != "" {
		out.Model = mp
	} else if alias := strings.TrimSpace(m.Alias); alias != "" {
		// Alias wins over an inherited model path. Proxy-fronted setups
		// declare an alias and let the proxy route by that name; falling
		// through to the inherited path here would silently send the
		// planner's model identifier to the proxy and route the request
		// to the wrong backend.
		out.Model = alias
	}
	if alias := strings.TrimSpace(m.Alias); alias != "" {
		out.DisplayName = alias
	}
	if baseURL := strings.TrimSpace(m.BaseURL); baseURL != "" {
		out.BaseURL = baseURL
	}
	if apiKeyEnv := strings.TrimSpace(m.APIKeyEnv); apiKeyEnv != "" {
		out.APIKeyEnv = apiKeyEnv
	}
	if effort := strings.TrimSpace(m.ReasoningEffort); effort != "" {
		out.ReasoningEffort = effort
	}
	if verbosity := strings.TrimSpace(m.Verbosity); verbosity != "" {
		out.Verbosity = verbosity
	}
	if len(m.ExtraArgs) > 0 {
		out.ExtraArgs = append([]string(nil), m.ExtraArgs...)
	}
	return out
}

// roleSpec resolves a backends.json role to a config.LlamaServer, or
// returns (defaults, false) when the role isn't declared. Errors from
// backends.json parsing surface to stderr (the harness boots with the
// fallback rather than crashing on a malformed sidecar file).
//
// When `defaults.Model` names a specific alias in backends.json (e.g.,
// the caller passed `--model qwen3.6-35b-nothink`), that explicit
// choice overrides the role-default lookup. Direct-mode wiring then
// connects to that alias's backend (host + port from the alias entry)
// while keeping the actual loaded model path on out.Model so mlx-lm
// recognizes it. This is how setups with two backends sharing the same
// weights but different chat-template-args disambiguate.
func roleSpec(projectRoot, role string, defaults config.LlamaServer) (config.LlamaServer, bool) {
	file, found, err := backends.LoadFromProjectRoot(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lifecycle: backends role %q resolution failed: %v\n", role, err)
		return defaults, false
	}
	if !found {
		return defaults, false
	}
	prof, profOK := file.Profile("")
	if !profOK {
		return defaults, false
	}
	// Explicit-alias override: when defaults.Model names a registered
	// backend alias, that backend wins over the role default.
	if explicit := strings.TrimSpace(defaults.Model); explicit != "" {
		if model, ok := prof.ModelByAlias(explicit); ok {
			if explicitAliasCanOverrideRole(model, role) {
				return backendsModelToLlamaServer(model, defaults), true
			}
		}
		if model, ok := file.ModelByAlias(explicit); ok {
			if explicitAliasCanOverrideRole(model, role) {
				return backendsModelToLlamaServer(model, defaults), true
			}
		}
	}
	alias, ok := prof.AliasForRole(role)
	if !ok {
		return defaults, false
	}
	model, _ := prof.ModelByAlias(alias)
	return backendsModelToLlamaServer(model, defaults), true
}

func explicitAliasCanOverrideRole(model backends.Model, role string) bool {
	if role != backends.RoleExecutor || len(model.Roles) == 0 {
		return true
	}
	for _, r := range model.Roles {
		if strings.EqualFold(strings.TrimSpace(r), backends.RoleExecutor) {
			return true
		}
	}
	return false
}

// sameLlamaTarget reports whether two LlamaServer specs would route
// to functionally identical backends. Used to decide whether to skip
// building a separate implementer client when backends.json's executor
// role resolves to the same target as the planner — in that case
// delegate_edit's direct-dispatch fallback gives identical behavior
// without the extra round-trip.
func sameLlamaTarget(a, b config.LlamaServer) bool {
	return a.EndpointBaseURL() == b.EndpointBaseURL() && a.Model == b.Model && strings.EqualFold(a.Kind, b.Kind) && a.APIKeyEnvName() == b.APIKeyEnvName()
}

// llamaServerBaseURL renders the endpoint URL for a LlamaServer spec.
// Centralised so planner / implementer / classifier wiring all build URLs the
// same way across local and hosted backends.
func llamaServerBaseURL(spec config.LlamaServer) string {
	return spec.EndpointBaseURL()
}

type serverStatusMsg struct {
	running   bool
	modelID   string
	displayID string
	err       error
	owned     bool
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
	in.SetHeight(1)
	in.KeyMap.InsertNewline.SetEnabled(false)
	in.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorAccent)
	in.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(colorDim)

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(colorSpinner)

	// Per-HTTP-call timeout on the primary and implementer llama clients.
	// Bounds a single stuck chat request so it can't hang forever. This is
	// decoupled from the turn-wide soft budget (Chat.MaxDurationSeconds)
	// that the runner enforces by emitting a graceful "wrap up" message
	// before cancelling the turn.
	callTimeout := time.Duration(cfg.Chat.RequestTimeoutSeconds) * time.Second

	// Each role (planner / executor / classifier) is resolved through
	// backends.json first. When a role is declared there, the spec
	// overrides the matching cfg.* field and that single source feeds
	// both the client URL and the launcher controller. When the role
	// isn't declared, the cfg.* field is the source of truth and the
	// behavior is identical to the pre-roles wiring.
	projectRoot := strings.TrimSpace(cfg.Workspace.Root)
	if projectRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectRoot = cwd
		}
	}

	plannerSpec, _ := roleSpec(projectRoot, backends.RolePlanner, cfg.LlamaServer)
	plannerClient := llama.NewClientForServer(plannerSpec, callTimeout)

	// Executor inherits from cfg.Implementer when set, falling back to
	// cfg.LlamaServer otherwise so a project can declare just the
	// executor in backends.json without also setting up an implementer
	// block in config.json.
	implementerEnabled := cfg.Implementer != nil
	executorDefaults := cfg.LlamaServer
	if implementerEnabled {
		executorDefaults = *cfg.Implementer
	}
	executorSpec, executorFromBackends := roleSpec(projectRoot, backends.RoleExecutor, executorDefaults)
	if executorFromBackends {
		// Only enable the implementer client when the resolved executor
		// target is actually distinct from the planner. Single-model
		// projects (lone-model fallback in AliasForRole) resolve both
		// roles to the same target; building a separate client would
		// add a round-trip on every delegate_edit for no benefit. The
		// runner's direct-dispatch fallback handles that case.
		if !sameLlamaTarget(executorSpec, plannerSpec) {
			implementerEnabled = true
		} else {
			// Keep executorSpec aligned with the planner so resolveModelID
			// renders the right label if status code reads it.
			executorSpec = plannerSpec
		}
	}

	classifierDefaults := cfg.RouteClassifier.LlamaServerSpec()
	classifierSpec, classifierFromBackends := roleSpec(projectRoot, backends.RoleClassifier, classifierDefaults)

	var summaryClient llama.LLMClient
	var summaryController *llama.ServerController
	classifierBaseURL := strings.TrimSpace(cfg.RouteClassifier.BaseURL)
	if classifierFromBackends {
		classifierBaseURL = llamaServerBaseURL(classifierSpec)
	}
	if classifierFromBackends && sameLlamaTarget(classifierSpec, plannerSpec) {
		// The classifier role often aliases the active planner model in
		// .idiom/backends.json. Reuse the already-managed planner client so
		// lazy classifier startup does not try to bind the planner port twice.
		summaryClient = plannerClient
	} else if classifierBaseURL != "" {
		timeout := time.Duration(cfg.RouteClassifier.TimeoutSeconds) * time.Second
		classifierSpec.BaseURL = classifierBaseURL
		classifierClient := llama.NewClientForServer(classifierSpec, timeout)
		summaryClient = classifierClient
		if cfg.RouteClassifier.AutoStartServer && classifierSpec.IsLaunchable() && classifierSpec.Binary != "" {
			summaryController = llama.NewServerController(classifierSpec, classifierClient)
		}
	}

	var implementerClient *llama.Client
	var implementerController *llama.ServerController
	if implementerEnabled {
		implementerClient = llama.NewClientForServer(executorSpec, callTimeout)
		if executorSpec.IsLaunchable() {
			implementerController = llama.NewServerController(executorSpec, implementerClient)
		}
	}

	m := &Model{
		cfg:                   cfg,
		plannerSpec:           plannerSpec,
		executorSpec:          executorSpec,
		client:                plannerClient,
		implementerClient:     implementerClient,
		implementerController: implementerController,
		summaryClient:         summaryClient,
		summaryController:     summaryController,
		events:                make(chan tea.Msg, 256),
		input:                 in,
		spinner:               spin,
		activeProfiles:        append([]string(nil), cfg.Chat.DefaultProfiles...),
		status:                "Booting up.",
		bootingServer:         false,
		traceMode:             traceDisplayCompact,
		agentModeName:         strings.TrimSpace(cfg.Chat.AgentMode),
	}
	if plannerSpec.IsLaunchable() {
		m.controller = llama.NewServerController(plannerSpec, plannerClient)
	}
	m.workspace = workspace.NewManager(cfg.Workspace)
	if cfg.Chat.AttachInstructionFiles {
		if candidates, explicit := instructionAttachmentCandidates(cfg.Chat); explicit {
			if len(candidates) > 0 {
				m.instructionAttacher = agent.NewInstructionAttacher(m.workspace.Root(), candidates...)
			}
		} else {
			m.instructionAttacher = agent.NewInstructionAttacher(m.workspace.Root())
		}
	}
	mode := agent.ModeOrDefault(m.agentModeName)
	m.workspace.SetPermissionRules(mode.PermissionRules)
	mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 30*time.Second)
	registry, mcpFailures := setupMCPRegistry(mcpCtx, cfg.MCP, cfg.Workspace.AllowPrivateNetwork)
	mcpCancel()
	if registry != nil {
		m.workspace.SetMCPRegistry(registry)
		m.mcpRegistry = registry
	}
	if len(mcpFailures) > 0 {
		m.setContextError("MCP setup: " + strings.Join(mcpFailures, "; "))
	}
	if extReg, extErr := setupExternalToolsRegistry(cfg.Workspace); extErr != nil {
		m.setContextError("External tools setup: " + extErr.Error())
	} else if extReg != nil {
		m.workspace.SetExternalToolsRegistry(extReg)
	}
	m.runner = &agent.Runner{
		Client:            m.client,
		ImplementerClient: m.implementerClient,
		Workspace:         m.workspace,
		MaxToolSteps:      modeStepCap(cfg.Chat.MaxToolSteps, mode),
		AgentName:         "main",
	}
	if err := m.reloadPromptStack(); err != nil {
		m.setContextError("Prompt context failed: " + err.Error())
	}
	m.initDebugLogger()
	return m
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
func modeStepCap(configured int, mode agent.Mode) int {
	if mode.StepCap > 0 && (configured <= 0 || mode.StepCap < configured) {
		return mode.StepCap
	}
	return configured
}

func modeRunPolicy(mode agent.Mode, cfg config.Chat) agent.RunPolicy {
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
	if cfg.MaxDurationSeconds > 0 {
		policy.MaxDuration = time.Duration(cfg.MaxDurationSeconds) * time.Second
	}
	return policy
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.waitForEventCmd(),
		func() tea.Msg {
			go m.bootstrap()
			return nil
		},
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
	case streamDoneMsg:
		cmd, done := m.handleStreamDone(msg)
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

func (m *Model) ensureServerReady(ctx context.Context) error {
	if m.plannerSpec.IsHosted() {
		m.serverUp = true
		m.modelID = resolveModelID(m.plannerSpec, m.plannerSpec.Model)
		m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, m.plannerSpec.Model)
		m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID})
		return nil
	}
	models, err := m.client.ListModels(ctx)
	if err == nil && len(models) > 0 {
		m.serverUp = true
		m.modelID = resolveModelID(m.plannerSpec, models[0].ID)
		m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, models[0].ID)
		m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID})
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
		m.modelID = resolveModelID(m.plannerSpec, "local-model")
		m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, "local-model")
		m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID, owned: true})
		return nil
	}
	m.serverUp = true
	m.modelID = resolveModelID(m.plannerSpec, models[0].ID)
	m.modelDisplayID = resolveModelDisplayID(m.plannerSpec, models[0].ID)
	m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID, displayID: m.modelDisplayID, owned: true})
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
//
// RunPromptOnce is the single owner of m.messages bookkeeping: it appends
// the user message before the agent loop and the assistant reply after.
// This function therefore must NOT append a chat placeholder or re-emit the
// reply — doing so causes the assistant content to appear twice, once
// before the user message (from the filled placeholder) and once after
// (from finishPromptOnceReply). The streaming spinner is rendered inline
// based on m.streaming; the side-panel uses its own non-chat placeholder.
func (m *Model) sendPromptAsync(prompt string) {
	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = busyStatusForPrompt(prompt)
	m.beginAgentHistoryTurn()
	go func() {
		// The outer ctx intentionally has no deadline. Per-call HTTP timeouts
		// live on the llama client (Chat.RequestTimeoutSeconds), and the
		// turn-wide soft budget lives on the runner (Chat.MaxDurationSeconds).
		// Binding both with a single WithTimeout caused a hung call to
		// consume the entire turn budget and produced "context deadline
		// exceeded" mid-generation; see logs/conversations/20260423-163650.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := m.RunPromptOnce(ctx, prompt)
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) sendPlanExecuteCommandAsync(objective string) {
	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = busyStatusForPrompt(objective)
	m.beginAgentHistoryTurn()
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := m.runPlanExecuteCommandOnce(ctx, objective)
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) sendImplementCommandAsync(objective string) {
	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = busyStatusForPrompt(objective)
	m.beginAgentHistoryTurn()
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := m.runImplementCommandOnce(ctx, objective)
		m.events <- streamDoneMsg{err: err}
	}()
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
	switch strings.TrimSpace(req.Tool) {
	case "context_pack":
		if target == "" {
			target = strings.TrimSpace(req.Task)
		}
		if target == "" {
			return "build a ranked context packet before choosing exact reads"
		}
		return "build a ranked context packet for " + truncateRunes(collapseWhitespace(target), 96)
	case "context_query", "context_zoom", "context_impact", "dependency_graph":
		if target == "" {
			return "trace code relationships without broad file reads"
		}
		return "trace code relationships around " + target
	case "discovery_query":
		purpose := strings.TrimSpace(req.Purpose)
		if purpose == "" {
			purpose = "repository discovery"
		}
		if target == "" {
			return "run structured " + purpose + " discovery"
		}
		return "run structured " + purpose + " discovery around " + target
	case "list_files":
		if target == "" {
			target = "the current workspace"
		}
		return "map " + target + " before choosing what to inspect"
	case "search_files":
		if target == "" {
			return "find candidate files for the next inspection step"
		}
		return "find candidate files matching " + target
	case "search_content":
		if target == "" {
			return "locate references before opening files"
		}
		return "locate references to " + target
	case "read_file":
		if target == "" {
			return "inspect source context needed for this turn"
		}
		return "inspect " + target + " for the relevant context"
	case "go_file_outline", "rust_file_outline", "ts_file_outline", "python_file_outline", "java_file_outline", "cpp_file_outline":
		if target == "" {
			return "scan the file structure before reading details"
		}
		return "scan " + target + " to find the right symbol or section"
	case "go_read_symbol", "rust_read_symbol", "ts_read_symbol", "python_read_symbol", "java_read_symbol", "cpp_read_symbol":
		if target == "" {
			return "read the exact declaration instead of a broad file slice"
		}
		return "read the exact declaration around " + target
	case "edit_file", "write_file", "append_file", "apply_patch":
		if target == "" {
			return "apply the planned code change"
		}
		return "apply the planned change in " + target
	case "task_ledger":
		return "keep the visible plan synchronized with the work"
	case "shell":
		if target == "" {
			return "inspect or verify state with a command"
		}
		if historyShellCommandLooksLikeVerification(target) {
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
// Mode in New() and /mode) to block/ask per-tool; the Mode's StepCap has
// already been folded into m.runner.MaxToolSteps.
func (m *Model) runAgentTurn(ctx context.Context, prompt string, history []llama.ChatMessage, opts llama.ChatOptions, plan promptExecutionPlan) (string, error) {
	mode := m.currentMode()
	steps := modeStepCap(m.cfg.Chat.MaxToolSteps, mode)
	policy := modeRunPolicy(mode, m.cfg.Chat)
	// Conversation and read-only lanes: raise context compaction
	// thresholds. Structured discovery tools return compact results
	// but the default 40-message limit compacts every ~13 tool calls,
	// destroying working memory before the model can synthesize.
	if plan.route.Family == "conversation" || plan.route.Contract == "direct_answer" {
		budget := NewContextBudget(m.compactionContextSize())
		maxMsgs, maxChars := budget.ConvMaxMessages, budget.ConvMaxChars
		policy.MaxContextMessages = maxMsgs
		policy.MaxContextChars = maxChars
	}
	// Conversation and read-only lanes: do not cap tool steps or
	// model calls. The model owns the decision of when evidence is
	// sufficient. The doom-loop guard catches repeated identical
	// calls; genuine investigation should not be preempted by a
	// budget wall.
	if plan.route.Family == "conversation" || plan.route.Family == "direct_answer" ||
		plan.route.Contract == "direct_answer" || plan.route.Contract == "grounded_evidence_summary" {
		policy.MaxModelCalls = 0
		policy.MaxToolSteps = 0
		steps = 0
	}
	runner := &agent.Runner{
		Client:              m.client,
		ImplementerClient:   m.implementerClient,
		Workspace:           m.workspace,
		MaxToolSteps:        steps,
		Policy:              policy,
		AgentName:           "main",
		RuntimeBrief:        plan.runtimeBrief,
		InstructionAttacher: m.instructionAttacher,
		ContextSize:         m.cfg.LlamaServer.CtxSize,
		OnEvent: func(event agent.RunEvent) {
			m.logRunEvent(event)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			return m.preExecuteToolRequest(step, req, mode)
		},
		DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
			return m.requestApproval(req, mode.Name)
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
			if !m.planExecuteActive {
				m.events <- toolTraceMsg{
					step:       event.Step,
					text:       formatToolRequestTrace(event),
					intent:     toolRequestIntent(event),
					busyStatus: busyStatusForToolRequest(event, prompt),
					auditKind:  historyToolAuditKind(event),
					running:    true,
				}
			}
		},
		func(event agent.ToolResultEvent) {
			m.logToolResult(event)
			m.pinPathsFromToolResult(event.Result)
			m.recordWriteFailure(event.Step, event.Request, event.Result)
			m.recordWriteSuccess(event.Step, event.Request, event.Result)
			m.recordTaskLedger(event.Request, event.Result)
			m.recordSubmittedPlan(event.Request, event.Result)
			if workspace.IsWorkspaceWriteTool(event.Request.Tool) && strings.TrimSpace(event.Result.Error) == "" {
				if err := m.reloadPromptStack(); err != nil {
					m.setContextError("Prompt context failed: " + err.Error())
				}
			}
			if !m.planExecuteActive {
				m.events <- toolTraceMsg{
					step:        event.Step,
					text:        workspace.FormatToolTrace(event.Result),
					busyStatus:  busyStatusAfterToolResult(event),
					outcome:     historyToolAuditOutcome(event),
					auditEvents: historyAuditEventsForToolResult(event),
				}
			}
			if workspace.IsWorkspaceWriteTool(event.Request.Tool) || event.Request.Tool == "task_ledger" || strings.TrimSpace(event.Result.Tool) == "submit_plan" {
				m.events <- planRefreshMsg{}
			}
		},
	)
	if err != nil {
		return "", err
	}
	m.lastAssistantReasoningContent = strings.TrimSpace(runner.LastAssistantMessage.ReasoningContent)
	return answer, err
}

func (m *Model) preExecuteToolRequest(step int, req workspace.ToolRequest, mode agent.Mode) *workspace.ToolResult {
	if m == nil || m.workspace == nil {
		return nil
	}
	switch m.workspace.EvaluateToolRule(req) {
	case workspace.ToolRuleAllow:
		m.recordRollbackSnapshot(req)
		m.capturePlanSnapshot(step, req)
		return nil
	case workspace.ToolRuleDeny:
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Tool request blocked by permission rules",
			Error:   fmt.Sprintf("permission rules deny %s under agent mode %q", req.Tool, mode.Name),
		}
	case workspace.ToolRuleAsk:
		if m.requestApproval(req, mode.Name) {
			m.recordRollbackSnapshot(req)
			m.capturePlanSnapshot(step, req)
			return nil
		}
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Tool request denied by user",
			Error:   fmt.Sprintf("user denied %s under agent mode %q", req.Tool, mode.Name),
		}
	}
	return nil
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
		Mode:          transcript.DebugLogMode(util.FirstNonEmpty(m.cfg.Chat.DebugLogMode, "redacted")),
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
	prompt, _ := m.composedSystemPrompt()
	return prompt
}

func (m *Model) systemPromptSources() []promptstack.Source {
	_, sources := m.composedSystemPrompt()
	return sources
}

func (m *Model) composedSystemPrompt() (string, []promptstack.Source) {
	if m == nil {
		return "", nil
	}
	key := m.composedPromptKey()
	if m.composedPromptCacheKey == key {
		return m.composedPrompt, append([]promptstack.Source(nil), m.composedPromptSources...)
	}
	mode := m.currentMode()
	overlayModel := util.FirstNonEmpty(m.plannerSpec.Model, m.cfg.LlamaServer.Model)
	stack := promptstack.AppendOverlays(
		m.promptStack,
		promptstack.ModelCompatibilityOverlays(m.cfg.Chat.ModelFamily, overlayModel),
	)
	sources := append([]promptstack.Source(nil), stack.Sources...)
	parts := make([]string, 0, 4)
	if base := strings.TrimSpace(stack.Text); base != "" {
		parts = append(parts, base)
	}
	modeFragments := loadModeSystemPromptFragments(mode, m.projectRoot(), sources)
	if extras := strings.TrimSpace(modeFragments.Text); extras != "" {
		parts = append(parts, extras)
		sources = append(sources, modeFragments.Sources...)
	}
	prompt := strings.Join(parts, "\n\n")
	m.composedPrompt = prompt
	m.composedPromptSources = append([]promptstack.Source(nil), sources...)
	m.composedPromptCacheKey = key
	return prompt, append([]promptstack.Source(nil), sources...)
}

func (m *Model) composedPromptKey() string {
	if m == nil {
		return ""
	}
	return strings.Join([]string{
		m.currentMode().Name,
		strings.TrimSpace(m.cfg.Chat.ModelFamily),
		strings.TrimSpace(util.FirstNonEmpty(m.plannerSpec.Model, m.cfg.LlamaServer.Model)),
	}, "\x00")
}

func (m *Model) invalidateComposedPrompt() {
	if m == nil {
		return
	}
	m.composedPrompt = ""
	m.composedPromptSources = nil
	m.composedPromptCacheKey = ""
}

// projectRoot returns the directory that Mode.SystemPromptFiles paths
// resolve against. Falls back to the workspace root so a Mode that names
// "prompts/plan-mode.md" finds the repo-level prompts/ directory.
func (m *Model) projectRoot() string {
	if m == nil {
		return ""
	}
	if root := strings.TrimSpace(m.cfg.Chat.ProjectRoot); root != "" {
		return root
	}
	if m.workspace != nil {
		return m.workspace.Root()
	}
	return ""
}

type systemPromptFragments struct {
	Text    string
	Sources []promptstack.Source
}

func loadModeSystemPromptFragments(mode agent.Mode, projectRoot string, existing []promptstack.Source) systemPromptFragments {
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
				DisplayName: "mode:" + mode.Name + ":" + filepath.Base(path),
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

// reloadPromptStack rebuilds the prompt stack from the active config and
// profiles. Delegates to promptstack; kept here so callers can trigger a
// refresh after /system or /profile commands.
func (m *Model) reloadPromptStack() error {
	return m.reloadPromptStackWithProfiles(m.activeProfiles)
}

func (m *Model) reloadPromptStackWithProfiles(profiles []string) error {
	chat := m.cfg.Chat
	if strings.TrimSpace(chat.ProjectRoot) != "" {
		if _, err := projectartifacts.Ensure(chat.ProjectRoot); err != nil {
			return err
		}
	}
	stack, err := promptstack.Build(chat, profiles)
	if err != nil {
		if !m.cfg.Chat.AllowMissingPrompts {
			return err
		}
		lenientStack, lenientErr := promptstack.BuildLenient(chat, profiles)
		if lenientErr != nil {
			return err
		}
		lenientStack.Warnings = append([]string{"prompt context degraded: " + err.Error()}, lenientStack.Warnings...)
		stack = lenientStack
	}
	m.promptStack = stack
	m.invalidateComposedPrompt()
	if len(stack.Warnings) > 0 {
		m.setContextError(strings.Join(stack.Warnings, "; "))
	}
	return nil
}
