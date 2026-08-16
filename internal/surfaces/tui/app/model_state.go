package app

import (
	"context"
	"errors"
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

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/evidence/transcript"
	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/instructions"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/promptstack"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/productworkflow"

	"cmp"
	"encoding/json"
)

// sessionAggregate is rolled forward each turn from runner telemetry so
// the end-of-session summary record can report totals/peaks without
// re-parsing the JSONL log. Updated on the UI goroutine via logRunEvent
// and logToolResult, both of which run sequentially per turn.
type sessionAggregate struct {
	turns                      int
	modelCalls                 int
	toolCalls                  int
	promptTokens               int
	completionTokens           int
	totalTokens                int
	promptCacheHitTokens       int
	promptCacheMissTokens      int
	promptCacheAccountingKnown bool
	reasoningTokens            int
	peakPromptTokens           int
	compactions                int
	toolOutputBytes            map[string]int
	toolCallCounts             map[string]int
	discoveryClasses           map[string]int
	discoveryCosts             map[string]int
}

func (a *sessionAggregate) recordModelCall(promptTokens, completionTokens, totalTokens, promptCacheHitTokens, promptCacheMissTokens int, promptCacheAccountingKnown bool, reasoningTokens int) {
	a.modelCalls++

	a.promptTokens += promptTokens
	a.completionTokens += completionTokens
	a.totalTokens += totalTokens
	a.promptCacheHitTokens += promptCacheHitTokens
	a.promptCacheMissTokens += promptCacheMissTokens

	a.promptCacheAccountingKnown = a.promptCacheAccountingKnown || promptCacheAccountingKnown

	a.reasoningTokens += reasoningTokens

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

// promptRuntimeState is the complete non-UI state boundary for one detached
// prompt turn. Snapshot state is private working context that is projected
// incrementally (for example transcript messages) or restored by the turn.
// Completion state is the durable result transferred back to Update as one
// value, so adding execution state cannot silently miss a parallel delta.
type promptRuntimeState struct {
	promptRuntimeSnapshotState
	promptRuntimeCompletionState
}

type promptRuntimeSnapshotState struct {
	activeProfiles                     []string
	sessionSummaryWritten              bool
	forcedTaskFamily                   string
	suppressPromptTranscript           bool
	requestHistoryOverride             *[]llama.ChatMessage
	nonInteractive                     bool
	plannerOwnedVerification           bool
	maxTokensOverride                  int
	disableThinkingOverride            bool
	forcedReadinessOpenObligations     []string
	suppressGreenfieldConfirmation     bool
	planExecuteActive                  bool
	planExecuteScopeContract           plancontract.PlanScopeContract
	planExecuteAuthorizedNewFiles      []string
	planExecuteWriteEnvelope           []string
	planExecuteWriteEnvelopeExplicit   bool
	planExecuteSourceSatisfiedObserver func([]string)
}

type promptRuntimeCompletionState struct {
	promptStack            promptstack.Stack
	composedPrompt         string
	composedPromptSources  []promptstack.Source
	composedPromptCacheKey string
	contextError           string
	sessionAgg             sessionAggregate

	lastAssistantReasoningContent string
	lastTurnSourceResults         []workspace.ToolResult
	lastTurnSourceObjective       string
	lastPlanExecuteRunResult      *planexecute.RunResult
	resumeImplementationState     *recoverycontract.ImplementationState

	plan            planRuntimeState
	planPath        string
	rollbackHistory []rollbackEntry

	planExecuteApproveAllEdits bool

	featurePlannerEffortRoot      string
	featurePlannerPhase           productworkflow.CreationPhase
	directFeaturePlannerSessionID string
}

// SetLastTurnSourceResults stores source-body tool results from the completed
// agent turn so a continuing follow-up can seed evidence grounding without
// re-reading files.
func (s *promptRuntimeCompletionState) SetLastTurnSourceResults(objective string, results []workspace.ToolResult) {
	const resultCap = 32
	if len(results) > resultCap {
		results = results[len(results)-resultCap:]
	}

	s.lastTurnSourceResults = append([]workspace.ToolResult(nil), results...)
	s.lastTurnSourceObjective = strings.TrimSpace(objective)
}

// LastTurnSourceResultsForObjective returns previous-turn source-body results
// only when the current objective is plausibly continuing that prior turn.
func (s *promptRuntimeCompletionState) LastTurnSourceResultsForObjective(objective string) []workspace.ToolResult {
	if len(s.lastTurnSourceResults) == 0 || !turnObjectiveContinuesSourceEvidence(s.lastTurnSourceObjective, objective) {
		return nil
	}

	return append([]workspace.ToolResult(nil), s.lastTurnSourceResults...)
}

// ClearLastTurnSourceResults resets the cross-turn seed results.
func (s *promptRuntimeCompletionState) ClearLastTurnSourceResults() {
	s.lastTurnSourceResults = nil
	s.lastTurnSourceObjective = ""
}

// backendRuntimeState is owned by the live Bubble Tea model. Detached prompt
// workers receive a value snapshot for turn execution, but their completion
// projection never writes these fields back: server status and /model can
// change them on Update while a turn is active.
type backendRuntimeState struct {
	backendRevision    uint64
	serverUp           bool
	managedServer      bool
	modelID            string
	modelDisplayID     string
	modelThinkingFloor bool
	modelSwapped       bool
}

// promptExecutionSession owns the complete non-UI state and dependencies for
// one prompt turn. Execution methods use this concrete type so adding
// a dependency on Bubble Tea presentation state fails at compile time instead
// of silently reading a zero field from a partially initialized Model clone.
type promptExecutionSession struct {
	cfg               config.Config
	client            llama.LLMClient
	controller        *llama.ServerController
	plannerSpec       config.LlamaServer
	executorSpec      config.LlamaServer
	implementerClient llama.LLMClient
	backendRuntimeState
	managedServers         *managedServerRegistry
	workspace              *workspace.Manager
	rollbackRegistry       *rollbackRegistry
	events                 chan tea.Msg
	eventsMu               sync.Mutex
	eventsClosed           bool
	promptExecutionContext context.Context
	promptRuntimeState

	messages                []llama.ChatMessage
	messagesRevision        uint64
	projectTranscriptEvents bool
	// promptConfigurationRevisionBase identifies the live /system and
	// /profile state this turn snapped. Completion projection uses it to avoid
	// restoring a prompt stack that the operator replaced while the turn ran.
	promptConfigurationRevisionBase uint64
	contextErrorRevisionBase        uint64

	debugLogger   *transcript.DebugLogger
	debugLogPath  string
	eventStream   *jsonEventStream
	agentModeName runprofile.AgentMode

	pinnedFiles *PinnedFileRegistry

	approvalSerial     sync.Mutex
	planApprovalMu     sync.Mutex
	planApprovalSerial sync.Mutex

	instructionAttacher *instructions.Attacher
	summaryClient       llama.LLMClient
}

type Model struct {
	cfg        config.Config
	client     llama.LLMClient
	controller *llama.ServerController
	// plannerSpec + executorSpec are the resolved LlamaServer specs for
	// the planner and executor roles. They mirror cfg.LlamaServer /
	// cfg.Implementer when no consolidated backend profile declares those roles
	// and reflect the consolidated profile entry otherwise. Status messages
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
	backendRuntimeState
	managedServers             *managedServerRegistry
	backgroundOperations       *backgroundOperationGroup
	workspace                  *workspace.Manager
	rollbackRegistry           *rollbackRegistry
	undoPresentationGeneration uint64
	toolSurfaceSetup           *workspace.ConfiguredToolSurface
	events                     chan tea.Msg
	eventWaitArmed             bool
	promptExecutionMu          sync.Mutex
	activePromptExecution      *promptExecutionLifecycle
	promptRuntimeState
	promptConfigurationRevision uint64
	contextErrorRevision        uint64

	width    int
	height   int
	ready    bool
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	// inputHistory stores previous user inputs for up/down arrow navigation.
	// inputHistoryIndex is the current position in inputHistory while navigating.
	// -1 means not navigating; inputHistoryDraft restores text typed before navigation.
	inputHistory      []string
	inputHistoryIndex int
	inputHistoryDraft string
	// messages is both model history and visible transcript. Workers keep a
	// deep-cloned copy and project mutations through promptTranscriptMsg so
	// Update can render progress without sharing slice backing storage.
	messages         []llama.ChatMessage
	messagesRevision uint64

	streaming                       bool
	turnEvidence                    *turnEvidence
	pastTurnContainers              []completedEvidence
	lastBudget                      *budgetData
	bootingServer                   bool
	status                          string
	runtimeError                    string
	workingFrame                    int
	thoughts                        thoughtPanelState
	debugLogger                     *transcript.DebugLogger
	debugLogPath                    string
	sessionStartedAt                time.Time
	eventStream                     *jsonEventStream
	pendingMutationStateResumeAudit *mutationStateResumeAudit
	agentModeName                   runprofile.AgentMode
	traceMode                       traceDisplayMode
	planPhaseText                   string
	mouseSelectionMode              bool

	pinnedFiles *PinnedFileRegistry

	approvalMu          sync.Mutex
	approvalPending     *pendingApproval
	planApprovalMu      sync.Mutex
	planApprovalPending *pendingPlanApproval
	laneSwitchPopup     laneSwitchPopupState
	// lastLaneSwitchSelection records the most recent lane command key
	// chosen from the lane-switch popup. It supports test assertions and
	// operator telemetry without coupling the popup to the dispatch path.
	lastLaneSwitchSelection string
	instructionAttacher     *instructions.Attacher
	mdRenderer              *glamour.TermRenderer
	mdRendererWrapWidth     int
	messageRenderCache      string
	messageRenderWidth      int
	messageRenderHash       uint64
	messageRenderMD         bool
	perMsgRenderCache       []string
	perMsgRenderHashes      []uint64
	perMsgRenderLen         int

	summaryClient      llama.LLMClient
	summaryController  *llama.ServerController
	summaryServerState *summaryServerLaunchState

	panelTab      int // panelTabHistory | panelTabPlan | panelTabDocViewer | panelTabSessions
	planViewport  viewport.Model
	planMTime     time.Time
	planContent   string
	planLoadError string

	planUI    planUIState
	docViewer docViewerState
	sessions  sessionsState
	history   historyState

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
	// slashSuggestions tracks the autocomplete state when the user types /
	slashSuggestions slashSuggestionsState
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

type planApprovalRequiredMsg struct{}

// ── Evidence container types ─────────────────────────────────────────────────

// budgetLedgerMsg carries per-turn budget data from logRunEvent to the UI
// event loop for the evidence container summary line.
type budgetLedgerMsg struct {
	totalTokens     int
	usedToolResults int
	maxToolSteps    int
	usedModelCalls  int
	maxModelCalls   int
	lane            string
}

// budgetData is the stored form of budgetLedgerMsg, attached to a
// completedEvidence container after streamDoneMsg.
type budgetData struct {
	totalTokens     int
	usedToolResults int
	maxToolSteps    int
	usedModelCalls  int
	maxModelCalls   int
	lane            string
}

// turnEvidence tracks the active turn's live evidence container.
type turnEvidence struct {
	tools         []evidenceRow
	expanded      bool
	userCollapsed bool
	finalized     bool
	scrollOff     int
	autoTail      bool
}

// completedEvidence stores a finished turn's evidence container with full
// tool rows so past containers remain independently expandable and scrollable.
type completedEvidence struct {
	tools     []evidenceRow
	summary   string
	expanded  bool
	scrollOff int
	budget    *budgetData
}

// evidenceRow is a single step in the evidence container — one tool call or
// one thinking block.
type evidenceRow struct {
	step      int
	toolName  string
	target    string
	intent    string
	auditKind string
	outcome   string
	summary   string
	running   bool
	glyph     string
	isThought bool
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
		label = titleDisplay(phase)
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

func (m *Model) Close() error {
	if m == nil {
		return nil
	}

	var closeErr error
	m.cancelActivePromptExecution()
	m.backgroundOperations.shutdown()

	if err := m.writeSessionSummary(); err != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("write session summary: %w", err))
	}

	if m.debugLogger != nil {
		if err := m.debugLogger.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close debug log: %w", err))
		}
	}

	if m.toolSurfaceSetup != nil {
		m.toolSurfaceSetup.Close()
		m.toolSurfaceSetup = nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	defer cancel()

	if m.summaryController != nil {
		if err := m.summaryController.Stop(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("stop classifier backend: %w", err))
		}
	}

	if m.implementerController != nil {
		if err := m.implementerController.Stop(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("stop executor backend: %w", err))
		}
	}

	managedControllers := m.managedServers.snapshot()
	if m.managedServer && m.controller != nil {
		alreadyRegistered := false
		for _, controller := range managedControllers {
			if controller == m.controller {
				alreadyRegistered = true
				break
			}
		}
		if !alreadyRegistered {
			managedControllers = append(managedControllers, m.controller)
		}
	}
	for _, controller := range managedControllers {
		if err := controller.Stop(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("stop managed planner backend: %w", err))
		}
	}

	return closeErr
}

func (m *Model) ensureBackgroundOperations() *backgroundOperationGroup {
	if m == nil {
		return nil
	}
	if m.backgroundOperations == nil {
		m.backgroundOperations = newBackgroundOperationGroup()
	}

	return m.backgroundOperations
}

func cloneChatBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (m *Model) samplingSummary() string {
	return samplingSummaryForOptions(chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride))
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

func (m *Model) bootstrapCommand() tea.Cmd {
	planner := snapshotPlannerServerOperation(m)
	implementer, startImplementer := snapshotImplementerServerOperation(m)
	backgroundOperations := m.ensureBackgroundOperations()

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		// Kick off the implementer server in parallel when configured; its
		// readiness is independent of the planner and failures here must not
		// block main-server bootstrap. Child tasks silently fall back to the
		// planner client when the implementer isn't ready yet.
		if startImplementer {
			_ = backgroundOperations.start(implementer.start)
		}

		if planner.spec.IsHosted() {
			modelID := resolveModelID(planner.spec, planner.spec.Model)

			return planner.status(serverStatusMsg{
				running:   true,
				modelID:   modelID,
				displayID: resolveModelDisplayID(planner.spec, planner.spec.Model),
			})
		}

		models, err := planner.client.ListModels(ctx)
		if err == nil && len(models) > 0 {
			modelID := resolveModelID(planner.spec, models[0].ID)

			return planner.status(serverStatusMsg{
				running:   true,
				modelID:   modelID,
				displayID: resolveModelDisplayID(planner.spec, models[0].ID),
			})
		}

		if planner.autoStart && planner.controller != nil {
			source := planner.source

			return serverStartRequestedMsg{source: &source}
		}

		return infoMsg{text: "No server detected. Use /server start or Ctrl+R."}
	}
}

func (m *Model) handleSlashCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return false
	}

	spec, ok := slashCommandSpecFor(fields[0])
	if !ok {
		m.status = "Unknown command. Use /help."
		return false
	}

	return spec.handler(m, raw, fields)
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
	operation := snapshotPlannerServerOperation(m)
	if !m.ensureBackgroundOperations().start(operation.start) {
		m.bootingServer = false
	}
}

func (m *Model) stopServerAsync() {
	if m.controller == nil {
		m.status = fmt.Sprintf("Backend %s is hosted at %s; local server controls are unavailable.", m.plannerSpec.Kind, m.plannerSpec.EndpointBaseURL())
		return
	}

	m.status = "Stopping llama-server."
	operation := snapshotPlannerServerOperation(m)
	_ = m.ensureBackgroundOperations().start(operation.stop)
}

func (m *Model) restartServerAsync() {
	if m.controller == nil {
		m.status = fmt.Sprintf("Backend %s is hosted at %s; local server controls are unavailable.", m.plannerSpec.Kind, m.plannerSpec.EndpointBaseURL())
		return
	}

	m.bootingServer = true
	m.status = "Restarting llama-server."
	operation := snapshotPlannerServerOperation(m)
	if !m.ensureBackgroundOperations().start(operation.restart) {
		m.bootingServer = false
	}
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

			for _, line := range historyAuditDigestLines(entry) {
				renderHistoryDetailLine(&b, w, "- ", line)
			}

			if m.traceMode == traceDisplayDetail {
				renderHistoryDetailLine(&b, w, "", "Details:")

				for _, event := range entry.auditEvents {
					if line := event.auditLine(); line != "" {
						renderHistoryDetailLine(&b, w, "- ", line)
					}
				}

				for _, tc := range entry.toolCalls {
					prefix := "▶ "
					if tc.running {
						prefix = "… "
					}

					renderHistoryDetailLine(&b, w, prefix, tc.line)

					meta := joinNonEmpty(" · ", keyValue("type", tc.kind), keyValue("outcome", tc.outcome))
					if meta != "" {
						renderHistoryDetailLine(&b, w, "  ", meta)
					}

					if strings.TrimSpace(tc.intent) != "" {
						renderHistoryDetailLine(&b, w, "  ", "reason: "+tc.intent)
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

func renderHistoryDetailLine(b *strings.Builder, width int, prefix string, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	for j, wl := range wrapLines(line, max(1, width-4)) {
		rowPrefix := prefix
		if j > 0 {
			rowPrefix = strings.Repeat(" ", len([]rune(prefix)))
		}

		b.WriteString(panelDetailStyle.Width(width).Render(rowPrefix + wl))
		b.WriteString("\n")
	}
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
		m.docViewer.viewport.Width = panelWidth
		m.docViewer.viewport.Height = panelContentHeight
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

	width := max(1, m.input.Width()-lipgloss.Width(m.input.Prompt))
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
	if m.operatorStatusText(m.width) != "" {
		count++
	}

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
		if !open {
			m.history.focused = false
		}

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
	mode := currentMode(m.agentModeName, m.cfg.Chat.AgentMode)
	return "agent=" + string(mode.Name)
}

func (m *Model) traceSummary() string {
	return string(m.traceMode)
}

// applyModePermissions is a hook for /mode to refresh any workspace-level
// policy tied to the active Mode. Runner step-cap is rebuilt from the
// current Mode on every new turn via runAgentTurn.
func (m *Model) applyModePermissions(mode modepolicy.Mode) {
	if m == nil || m.workspace == nil {
		return
	}

	m.workspace.SetProtectedWritePaths(protectedPromptWritePaths(m.cfg, mode))
	m.workspace.SetPermissionRules(mode.PermissionRules)
}

func (m *Model) SetNonInteractive(nonInteractive bool) {
	if m == nil {
		return
	}

	m.nonInteractive = nonInteractive
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
	message = cloneChatMessage(message)
	m.insertBeforePendingAssistantOwned(message)
}

func (m *Model) insertBeforePendingAssistantOwned(message llama.ChatMessage) {
	idx := m.pendingAssistantIndex()
	if idx == -1 {
		m.messages = append(m.messages, message)
		m.messagesRevision++
		return
	}

	m.messages = append(m.messages[:idx], append([]llama.ChatMessage{message}, m.messages[idx:]...)...)
	m.messagesRevision++
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

	m.eventStream.emitMessage(role, content)

	if m.debugLogger == nil {
		return
	}

	_ = m.debugLogger.LogMessage(role, content)
}

func (m *promptExecutionSession) logToolRequest(event agent.ToolRequestEvent) {
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

func (m *promptExecutionSession) logToolResult(event agent.ToolResultEvent) {
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
		Facts:          event.Result.Facts,
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

func (m *promptExecutionSession) logRunEvent(event agent.RunEvent) {
	if audit, ok := historyAuditEventFromRunEvent(event); ok {
		m.enqueueHistoryAuditEvent(audit)
	}

	switch event.Role {
	case "model_call":
		var payload struct {
			Step                       int            `json:"step"`
			Label                      string         `json:"label"`
			Agent                      string         `json:"agent"`
			PromptTokens               int            `json:"prompt_tokens"`
			CompletionTokens           int            `json:"completion_tokens"`
			TotalTokens                int            `json:"total_tokens"`
			PromptCacheHitTokens       int            `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens      int            `json:"prompt_cache_miss_tokens"`
			PromptCacheAccountingKnown bool           `json:"prompt_cache_accounting_known"`
			ReasoningTokens            int            `json:"reasoning_tokens"`
			CtxSize                    int            `json:"ctx_size"`
			MessageCount               int            `json:"message_count"`
			RoleChars                  map[string]int `json:"role_chars"`
			ChosenTool                 string         `json:"chosen_tool"`
			DurationMillis             int64          `json:"duration_ms"`
		}

		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			m.sessionAgg.recordModelCall(payload.PromptTokens, payload.CompletionTokens, payload.TotalTokens, payload.PromptCacheHitTokens, payload.PromptCacheMissTokens, payload.PromptCacheAccountingKnown, payload.ReasoningTokens)

			if m.debugLogger != nil {
				_ = m.debugLogger.LogModelCall(transcript.ModelCall{
					Step:                       payload.Step,
					Agent:                      textutil.FirstNonEmpty(payload.Agent, payload.Label),
					PromptTokens:               payload.PromptTokens,
					CompletionTokens:           payload.CompletionTokens,
					TotalTokens:                payload.TotalTokens,
					PromptCacheHitTokens:       payload.PromptCacheHitTokens,
					PromptCacheMissTokens:      payload.PromptCacheMissTokens,
					PromptCacheAccountingKnown: payload.PromptCacheAccountingKnown,
					ReasoningTokens:            payload.ReasoningTokens,
					CtxSize:                    payload.CtxSize,
					MessageCount:               payload.MessageCount,
					RoleChars:                  payload.RoleChars,
					ChosenTool:                 payload.ChosenTool,
					DurationMillis:             payload.DurationMillis,
				})
			}
		}

		// Skip the legacy LogMessage path; the typed record replaces it.
		return
	case "completion_status":
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			payload["type"] = "completion_status"
			m.eventStream.emit(payload)
		}

		if m.debugLogger != nil {
			_ = m.debugLogger.LogMessage(event.Role, event.Content)
		}

		return
	case "implementation_lifecycle_state":
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			payload["type"] = "implementation_lifecycle_state"
			m.eventStream.emit(payload)
		}

		if m.debugLogger != nil {
			_ = m.debugLogger.LogRunEvent(event.Role, event.Content)
		}

		return
	case mutationStateResumeEventRole:
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			payload["type"] = mutationStateResumeEventRole
			m.eventStream.emit(payload)
		}

		if m.debugLogger != nil {
			_ = m.debugLogger.LogRunEvent(event.Role, event.Content)
		}

		return
	case "budget_ledger":
		var payload struct {
			Lane                       string `json:"lane"`
			UsedModelCalls             int    `json:"used_model_calls"`
			MaxModelCalls              int    `json:"max_model_calls"`
			UsedToolResults            int    `json:"used_tool_results"`
			MaxToolSteps               int    `json:"max_tool_steps"`
			PromptTokens               int    `json:"prompt_tokens"`
			CompletionTokens           int    `json:"completion_tokens"`
			TotalTokens                int    `json:"total_tokens"`
			PromptCacheHitTokens       int    `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens      int    `json:"prompt_cache_miss_tokens"`
			PromptCacheAccountingKnown bool   `json:"prompt_cache_accounting_known"`
			ReasoningTokens            int    `json:"reasoning_tokens"`
			ProgressTurns              int    `json:"progress_turns"`
			RejectedActions            int    `json:"rejected_actions"`
			CompletionGateRejections   int    `json:"completion_gate_rejections"`
			ContextCompactions         int    `json:"context_compactions"`
			DurationMillis             int64  `json:"duration_millis"`
			StopReason                 string `json:"stop_reason"`
			BudgetExhausted            bool   `json:"budget_exhausted"`
		}

		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			m.sessionAgg.turns++

			m.sessionAgg.compactions += payload.ContextCompactions

			if m.debugLogger != nil {
				_ = m.debugLogger.LogTurnSummary(transcript.TurnSummary{
					Lane:                       payload.Lane,
					UsedModelCalls:             payload.UsedModelCalls,
					MaxModelCalls:              payload.MaxModelCalls,
					UsedToolResults:            payload.UsedToolResults,
					MaxToolSteps:               payload.MaxToolSteps,
					PromptTokens:               payload.PromptTokens,
					CompletionTokens:           payload.CompletionTokens,
					TotalTokens:                payload.TotalTokens,
					PromptCacheHitTokens:       payload.PromptCacheHitTokens,
					PromptCacheMissTokens:      payload.PromptCacheMissTokens,
					PromptCacheAccountingKnown: payload.PromptCacheAccountingKnown,
					ReasoningTokens:            payload.ReasoningTokens,
					ProgressTurns:              payload.ProgressTurns,
					RejectedActions:            payload.RejectedActions,
					CompletionGateRejections:   payload.CompletionGateRejections,
					ContextCompactions:         payload.ContextCompactions,
					DurationMillis:             payload.DurationMillis,
					StopReason:                 payload.StopReason,
					BudgetExhausted:            payload.BudgetExhausted,
				})
			}

			// Emit budget data to the UI event loop for the evidence
			// container summary line.
			m.queueUIEvent(budgetLedgerMsg{
				totalTokens:     payload.TotalTokens,
				usedToolResults: payload.UsedToolResults,
				maxToolSteps:    payload.MaxToolSteps,
				usedModelCalls:  payload.UsedModelCalls,
				maxModelCalls:   payload.MaxModelCalls,
				lane:            payload.Lane,
			})
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

func (m *promptExecutionSession) logNestedToolRequest(content string) bool {
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

func (m *promptExecutionSession) logNestedToolResult(content string) bool {
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

func (m *Model) writeSessionSummary() error {
	if m.sessionSummaryWritten {
		return nil
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

	promptCacheHitRate := 0.0

	if m.sessionAgg.promptCacheAccountingKnown {
		totalCachePrompt := m.sessionAgg.promptCacheHitTokens + m.sessionAgg.promptCacheMissTokens
		if totalCachePrompt > 0 {
			promptCacheHitRate = float64(m.sessionAgg.promptCacheHitTokens) / float64(totalCachePrompt)
		}
	}

	if m.debugLogger != nil {
		if err := m.debugLogger.LogSessionSummary(transcript.SessionSummary{
			Turns:                      m.sessionAgg.turns,
			ModelCalls:                 m.sessionAgg.modelCalls,
			ToolCalls:                  m.sessionAgg.toolCalls,
			PromptTokens:               m.sessionAgg.promptTokens,
			CompletionTokens:           m.sessionAgg.completionTokens,
			TotalTokens:                m.sessionAgg.totalTokens,
			PromptCacheHitTokens:       m.sessionAgg.promptCacheHitTokens,
			PromptCacheMissTokens:      m.sessionAgg.promptCacheMissTokens,
			PromptCacheAccountingKnown: m.sessionAgg.promptCacheAccountingKnown,
			PromptCacheHitRate:         promptCacheHitRate,
			ReasoningTokens:            m.sessionAgg.reasoningTokens,
			PeakPromptTokens:           m.sessionAgg.peakPromptTokens,
			CtxSize:                    ctxSize,
			PeakCtxPct:                 peakPct,
			Compactions:                m.sessionAgg.compactions,
			TopToolsByOutput:           m.sessionAgg.topToolsByOutput(10),
			DiscoveryByClass:           discoveryAggregates(m.sessionAgg.discoveryClasses),
			DiscoveryByCost:            discoveryAggregates(m.sessionAgg.discoveryCosts),
			DurationMillis:             duration,
		}); err != nil {
			return err
		}
	}

	// Mirror to the stdout JSON event stream so wrappers (idiom lane,
	// queue, etc.) can correlate aggregate token usage with the
	// dispatch they spawned. Only fires when --json is on; debug-log
	// path above is the always-on persistence layer.
	summary := map[string]any{
		"type":                          "session_summary",
		"turns":                         m.sessionAgg.turns,
		"model_calls":                   m.sessionAgg.modelCalls,
		"tool_calls":                    m.sessionAgg.toolCalls,
		"prompt_tokens":                 m.sessionAgg.promptTokens,
		"completion_tokens":             m.sessionAgg.completionTokens,
		"total_tokens":                  m.sessionAgg.totalTokens,
		"prompt_cache_hit_tokens":       m.sessionAgg.promptCacheHitTokens,
		"prompt_cache_miss_tokens":      m.sessionAgg.promptCacheMissTokens,
		"prompt_cache_accounting_known": m.sessionAgg.promptCacheAccountingKnown,
		"reasoning_tokens":              m.sessionAgg.reasoningTokens,
		"peak_prompt_tokens":            m.sessionAgg.peakPromptTokens,
		"ctx_size":                      ctxSize,
		"peak_ctx_pct":                  peakPct,
		"compactions":                   m.sessionAgg.compactions,
		"duration_ms":                   duration,
	}
	if m.sessionAgg.promptCacheAccountingKnown {
		if m.sessionAgg.promptCacheHitTokens+m.sessionAgg.promptCacheMissTokens > 0 {
			summary["prompt_cache_hit_rate"] = promptCacheHitRate
		}
	}

	m.eventStream.emit(summary)
	m.sessionSummaryWritten = true

	return nil
}

func (m *Model) logError(kind, message string) {
	m.eventStream.emitError(kind, message)

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
	if m.contextError != message {
		m.contextErrorRevision++
	}
	m.contextError = message
	m.status = message
	m.logError("context", message)
}

func (m *Model) SetForcedTaskFamily(family string) {
	m.forcedTaskFamily = strings.TrimSpace(family)
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

func turnObjectiveContinuesSourceEvidence(previous string, current string) bool {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	if previous == "" || current == "" {
		return false
	}

	lowerCurrent := strings.ToLower(current)
	for _, marker := range []string{
		" that ", " it ", " its ", " those ", " these ",
		" above ", " previous ", " earlier ", " same ", " follow up",
		"follow-up", " elaborate", " what about", " how about",
	} {
		if strings.Contains(" "+lowerCurrent+" ", marker) {
			return true
		}
	}

	previousTerms := turnObjectiveCarryTerms(previous)
	currentTerms := turnObjectiveCarryTerms(current)
	if len(previousTerms) == 0 || len(currentTerms) == 0 {
		return false
	}

	matches := 0
	for term := range currentTerms {
		if _, ok := previousTerms[term]; ok {
			matches++
		}
	}

	return matches >= 2
}

func turnObjectiveCarryTerms(text string) map[string]struct{} {
	stop := map[string]struct{}{
		"about": {}, "actual": {}, "answer": {}, "better": {}, "could": {}, "does": {},
		"generic": {}, "good": {}, "have": {}, "need": {}, "option": {}, "please": {},
		"project": {}, "question": {}, "request": {}, "should": {}, "than": {},
		"that": {}, "this": {}, "what": {}, "when": {}, "where": {}, "which": {},
		"with": {}, "would": {},
	}

	terms := make(map[string]struct{})
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		word = strings.TrimSpace(word)
		if len(word) < 4 {
			continue
		}

		if len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
			word = strings.TrimSuffix(word, "s")
		}

		if _, skip := stop[word]; skip {
			continue
		}

		terms[word] = struct{}{}
	}

	return terms
}

func (m *Model) SetPlanExecuteScope(contract plancontract.PlanScopeContract, authorizedNewFiles []string) {
	m.planExecuteActive = true
	m.planExecuteScopeContract = contract.Normalized()
	m.planExecuteAuthorizedNewFiles = appendUniqueBriefStrings(nil, authorizedNewFiles...)
}

func (m *Model) clearContextError() {
	if m.contextError != "" {
		m.contextErrorRevision++
	}
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
