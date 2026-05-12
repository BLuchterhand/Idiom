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
// out.Model resolves in this priority order: the Model entry's
// ModelPath (direct-mode file path), then the Model entry's Alias
// (proxy-mode identifier — overrides any inherited path because the
// alias is what the proxy routes by), then the inherited defaults.
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
	}
	if mp := strings.TrimSpace(m.ModelPath); mp != "" {
		out.Model = mp
	} else if alias := strings.TrimSpace(m.Alias); alias != "" {
		// Alias wins over an inherited model path. Proxy-fronted setups
		// declare an alias and let the proxy route by that name; falling
		// through to the inherited path here would silently send the
		// planner's model identifier to the proxy and route the request
		// to the wrong backend.
		out.Model = alias
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
			return backendsModelToLlamaServer(model, defaults), true
		}
	}
	alias, ok := prof.AliasForRole(role)
	if !ok {
		return defaults, false
	}
	model, _ := prof.ModelByAlias(alias)
	return backendsModelToLlamaServer(model, defaults), true
}

// sameLlamaTarget reports whether two LlamaServer specs would route
// to functionally identical backends. Used to decide whether to skip
// building a separate implementer client when backends.json's executor
// role resolves to the same target as the planner — in that case
// delegate_edit's direct-dispatch fallback gives identical behavior
// without the extra round-trip.
func sameLlamaTarget(a, b config.LlamaServer) bool {
	return a.Host == b.Host && a.Port == b.Port && a.Model == b.Model && a.Kind == b.Kind
}

// llamaServerBaseURL renders the http://host:port URL for a
// LlamaServer spec. Centralised so the planner / implementer /
// classifier wiring all build URLs the same way.
func llamaServerBaseURL(spec config.LlamaServer) string {
	return fmt.Sprintf("http://%s:%d", spec.Host, spec.Port)
}

type serverStatusMsg struct {
	running bool
	modelID string
	err     error
	owned   bool
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

	plannerSpec, _ := roleSpec(projectRoot, backends.RolePlanner, cfg.LlamaServer)

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

	var summaryClient *llama.Client
	var summaryController *llama.ServerController
	classifierBaseURL := strings.TrimSpace(cfg.RouteClassifier.BaseURL)
	if classifierFromBackends {
		classifierBaseURL = llamaServerBaseURL(classifierSpec)
	}
	if classifierBaseURL != "" {
		timeout := time.Duration(cfg.RouteClassifier.TimeoutSeconds) * time.Second
		summaryClient = llama.NewClient(classifierBaseURL, timeout).WithBackend(classifierSpec.Kind)
		if cfg.RouteClassifier.AutoStartServer && classifierSpec.Binary != "" {
			summaryController = llama.NewServerController(classifierSpec, summaryClient)
		}
	}

	var implementerClient *llama.Client
	var implementerController *llama.ServerController
	if implementerEnabled {
		implementerClient = llama.NewClient(llamaServerBaseURL(executorSpec), callTimeout).WithBackend(executorSpec.Kind)
		implementerController = llama.NewServerController(executorSpec, implementerClient)
	}

	m := &Model{
		cfg:                   cfg,
		plannerSpec:           plannerSpec,
		executorSpec:          executorSpec,
		client:                llama.NewClient(llamaServerBaseURL(plannerSpec), callTimeout).WithBackend(plannerSpec.Kind),
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
	m.controller = llama.NewServerController(plannerSpec, m.client)
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
		// ── Plan panel navigation (consumes key, returns early) ──────────────
		if m.historyPanelOpen && m.historyFocused && m.panelTab == panelTabPlan {
			if handled, cmd := m.handlePlanPanelKey(msg); handled {
				return m, cmd
			}
		}
		// ── Sessions panel navigation (consumes key, returns early) ──────────
		if m.historyPanelOpen && m.historyFocused && m.panelTab == panelTabSessions {
			if handled, cmd := m.handleSessionsPanelKey(msg); handled {
				return m, cmd
			}
		}
		// ── History panel navigation (consumes key, returns early) ────────────
		if m.historyPanelOpen && m.historyFocused {
			switch msg.Type {
			case tea.KeyUp:
				if m.historySelected > 0 {
					m.historySelected--
					m.historyExpanded = false
					m.renderHistoryPanelContent()
				}
				return m, waitForEvent(m.events)
			case tea.KeyDown:
				if m.historySelected < len(m.historyEntries)-1 {
					m.historySelected++
					m.historyExpanded = false
					m.renderHistoryPanelContent()
				}
				return m, waitForEvent(m.events)
			case tea.KeyEnter:
				m.historyExpanded = !m.historyExpanded
				m.renderHistoryPanelContent()
				return m, waitForEvent(m.events)
			case tea.KeyEsc:
				m.historyFocused = false
				m.renderHistoryPanelContent()
				return m, waitForEvent(m.events)
			case tea.KeyPgUp, tea.KeyPgDown:
				m.historyViewport, _ = m.historyViewport.Update(msg)
				return m, waitForEvent(m.events)
			case tea.KeyRunes:
				// j / k line-scroll the panel when focused (vim-style).
				switch string(msg.Runes) {
				case "j":
					m.historyViewport.ScrollDown(1)
					return m, waitForEvent(m.events)
				case "k":
					m.historyViewport.ScrollUp(1)
					return m, waitForEvent(m.events)
				}
			}
		}
		// ── Global key bindings ───────────────────────────────────────────────
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
		case tea.KeyCtrlB:
			// Ctrl+B toggles the history panel open/closed.
			// Panel opens unfocused — Tab moves focus in, Esc moves focus back.
			if !m.historyPanelOpen {
				m.panelTab = panelTabHistory
			}
			m.setHistoryPanelOpen(!m.historyPanelOpen)
			if m.historyPanelOpen {
				m.status = "History panel open. Tab focuses it. Ctrl+T cycles tabs."
			} else {
				m.status = "Panel closed."
			}
			return m, waitForEvent(m.events)
		case tea.KeyCtrlT:
			// Ctrl+T cycles the side-panel tabs. Opens the panel if closed.
			if !m.historyPanelOpen {
				m.setHistoryPanelOpen(true)
			}
			m.panelTab = (m.panelTab + 1) % panelTabCount
			if m.panelTab == panelTabSessions {
				m.refreshSessionsList()
			}
			m.rerenderActivePanel()
			return m, waitForEvent(m.events)
		case tea.KeyTab:
			// Tab toggles keyboard focus between input and side panel.
			// Re-render the active tab so its focus hint / cursor
			// highlight updates — not just the history viewport.
			if m.historyPanelOpen {
				m.historyFocused = !m.historyFocused
				m.rerenderActivePanel()
			}
			return m, waitForEvent(m.events)
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
				if m.pendingExec != nil {
					cmd := m.pendingExec
					m.pendingExec = nil
					m.renderMessages(true)
					return m, cmd
				}
			} else if !m.streaming {
				m.appendHistoryMessage("user", text, nil)
				m.sendPromptAsync(text)
			}
			m.renderMessages(true)
		}
	case tea.MouseMsg:
		// Route mouse events to whichever viewport is under the cursor.
		// When the panel is open, anything past the main viewport's width
		// (plus the 1-col divider) belongs to the history panel.
		// Return early so scroll/click events do not reach the textarea
		// via the fall-through m.input.Update at the bottom of Update.
		if m.historyPanelOpen && msg.X >= m.viewport.Width+1 {
			m.historyViewport, _ = m.historyViewport.Update(msg)
		} else {
			m.updateViewport(msg)
		}
		return m, waitForEvent(m.events)
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
	case streamDoneMsg:
		m.streaming = false
		m.workingFrame = 0
		if msg.err != nil {
			m.failAgentHistoryTurn(msg.err)
			m.setRuntimeError("Chat failed: " + msg.err.Error())
		} else {
			m.clearRuntimeError()
			m.status = "Response complete."
			m.recordAgentTurn()
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
		if m.traceMode == traceDisplayDetail && !msg.running {
			m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: msg.text})
			m.logMessage("tool", msg.text)
		}
		// Always reflect tool activity in the history panel regardless of trace mode.
		m.recordToolTraceInHistory(msg.step, msg.text, msg.intent, msg.running)
		m.renderMessages(false)
	case historySummaryMsg:
		if msg.index >= 0 && msg.index < len(m.historyEntries) {
			m.historyEntries[msg.index].summary = msg.summary
			m.requestHistoryPanelRender(false, false)
		}
	case historyPanelRefreshMsg:
		if m.historyRenderPending {
			m.flushHistoryPanelRender()
		}
	case planEditorDoneMsg:
		if msg.err != nil {
			m.status = "Editor exited with error: " + msg.err.Error()
		} else {
			m.status = "Plan updated: " + m.displayPath(m.planPath)
		}
		m.renderPlanPanelContent()
	case planRefreshMsg:
		// Re-render is self-gating: renderPlanPanelContent no-ops when
		// the panel is closed, so firing this on every write/ledger
		// event is cheap in the common case.
		m.renderPlanPanelContent()
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
		m.modelID = resolveModelID(m.plannerSpec, models[0].ID)
		m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID})
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
		m.modelID = resolveModelID(m.plannerSpec, "local-model")
		m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID, owned: true})
		return nil
	}
	m.serverUp = true
	m.modelID = resolveModelID(m.plannerSpec, models[0].ID)
	m.logServerEvent(serverStatusMsg{running: true, modelID: m.modelID, owned: true})
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

	target := truncateRunes(collapseWhitespace(toolRequestTraceTarget(req)), 96)
	switch strings.TrimSpace(req.Tool) {
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
			return "inspect source context before deciding the next step"
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
		return "inspect or verify state by running " + target
	case "child_task":
		if target == "" {
			return "delegate a bounded subtask while continuing the main task"
		}
		return "delegate a bounded subtask about " + target
	}

	if action := strings.TrimSpace(event.Action); action != "" {
		return "take the next " + action + " step"
	}
	if target != "" {
		return "gather context from " + target
	}
	return "gather context for the next step"
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
	_ = plan
	_ = prompt
	mode := m.currentMode()
	steps := modeStepCap(m.cfg.Chat.MaxToolSteps, mode)
	runner := &agent.Runner{
		Client:              m.client,
		ImplementerClient:   m.implementerClient,
		Workspace:           m.workspace,
		MaxToolSteps:        steps,
		Policy:              modeRunPolicy(mode, m.cfg.Chat),
		AgentName:           "main",
		RuntimeBrief:        plan.runtimeBrief,
		InstructionAttacher: m.instructionAttacher,
		ContextSize:         m.cfg.LlamaServer.CtxSize,
		OnEvent: func(event agent.RunEvent) {
			m.logRunEvent(event)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			switch m.workspace.EvaluateToolRule(req) {
			case workspace.ToolRuleAllow:
				m.recordRollbackSnapshot(req)
				m.capturePlanSnapshot(step, req)
				return nil
			case workspace.ToolRuleDeny:
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Tool request blocked by Mode permission rules",
					Error:   fmt.Sprintf("agent mode %q denies this request", mode.Name),
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
			m.events <- toolTraceMsg{step: event.Step, text: formatToolRequestTrace(event), intent: toolRequestIntent(event), running: true}
		},
		func(event agent.ToolResultEvent) {
			m.logToolResult(event)
			m.pinPathsFromToolResult(event.Result)
			m.recordWriteFailure(event.Step, event.Request, event.Result)
			m.recordWriteSuccess(event.Step, event.Request, event.Result)
			m.recordTaskLedger(event.Request, event.Result)
			if workspace.IsWorkspaceWriteTool(event.Request.Tool) && strings.TrimSpace(event.Result.Error) == "" {
				if err := m.reloadPromptStack(); err != nil {
					m.setContextError("Prompt context failed: " + err.Error())
				}
			}
			m.events <- toolTraceMsg{step: event.Step, text: workspace.FormatToolTrace(event.Result)}
			// Poke the UI only when this tool could have mutated the
			// plan (write tools + task_ledger). Other tool results
			// don't change the plan file, so re-rendering would be
			// wasted work.
			if workspace.IsWorkspaceWriteTool(event.Request.Tool) || event.Request.Tool == "task_ledger" {
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
	stack := promptstack.AppendOverlays(
		m.promptStack,
		promptstack.ModelCompatibilityOverlays(m.cfg.Chat.ModelFamily, m.cfg.LlamaServer.Model),
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
		strings.TrimSpace(m.cfg.LlamaServer.Model),
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

// loadModeSystemPrompts reads each file named in Mode.SystemPromptFiles
// and joins their contents into a single trailing system-prompt section.
// Files already present in the prompt stack are skipped so the same
// baseline guidance is not sent twice when both config and Mode include it.
// Unknown or unreadable files are skipped silently so a Mode can list
// optional fragments without breaking when they're absent.
func loadModeSystemPrompts(mode agent.Mode, projectRoot string, existing []promptstack.Source) string {
	return loadModeSystemPromptFragments(mode, projectRoot, existing).Text
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
