package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/promptstack"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/workflows/planexecute"

	tea "github.com/charmbracelet/bubbletea"
)

const promptWorkerEventBuffer = 256

var errPromptExecutionEventsUnavailable = errors.New("prompt execution event stream is not initialized")

// promptWorkerRun executes one complete interactive turn against a detached
// execution surface. The live Bubble Tea Model is never captured by a worker.
type promptWorkerRun func(context.Context, *promptExecutionSession) (string, error)

// promptExecutionLifecycle is the live Model's cancellation and join handle
// for one detached turn. Close cancels and joins this lifecycle before it
// tears down loggers, tool surfaces, or model controllers shared by the worker.
type promptExecutionLifecycle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// promptWorkerResultMsg is the final event emitted for an interactive turn.
// relayPromptExecution drains and closes the worker's private event stream
// before publishing this result, so no progress or projection event can trail
// completion.
type promptWorkerResultMsg struct {
	answer                          string
	completion                      promptRuntimeCompletionState
	promptConfigurationRevisionBase uint64
	contextErrorRevisionBase        uint64
	err                             error
	lifecycle                       *promptExecutionLifecycle
}

// promptTranscriptMsg projects a worker-owned transcript mutation onto the
// live Model. The worker keeps the same message in its private transcript so
// later model calls in the turn see the correct conversation history.
type promptTranscriptMsg struct {
	message       llama.ChatMessage
	beforePending bool
}

// approvalPendingMsg and planApprovalPendingMsg transfer pending-request
// ownership to Update while the detached worker waits on the response channel.
type approvalPendingMsg struct {
	pending *pendingApproval
}

type approvalClearedMsg struct {
	pending *pendingApproval
}

type planApprovalPendingMsg struct {
	pending *pendingPlanApproval
}

type planApprovalClearedMsg struct {
	pending *pendingPlanApproval
}

type laneSwitchPopupMsg struct {
	objective   string
	recommended string
}

// startPromptExecution snapshots all mutable execution state before launching
// the worker. Initial turn/panel state is still established synchronously by
// the caller on Update's goroutine.
func (m *Model) startPromptExecution(run promptWorkerRun) {
	if m == nil || run == nil {
		return
	}

	if m.events == nil {
		m.handleStreamDone(streamDoneMsg{err: errPromptExecutionEventsUnavailable})

		return
	}

	ctx, lifecycle, started := m.beginPromptExecution()
	if !started {
		return
	}
	session := newDetachedPromptExecutionSession(ctx, m)

	liveEvents := m.events
	go relayPromptExecution(ctx, liveEvents, session, run, lifecycle)
}

func (m *Model) beginPromptExecution() (context.Context, *promptExecutionLifecycle, bool) {
	m.promptExecutionMu.Lock()
	defer m.promptExecutionMu.Unlock()

	if active := m.activePromptExecution; active != nil {
		select {
		case <-active.done:
			m.activePromptExecution = nil
		default:
			return nil, nil, false
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	lifecycle := &promptExecutionLifecycle{cancel: cancel, done: make(chan struct{})}
	m.activePromptExecution = lifecycle

	return ctx, lifecycle, true
}

func (m *Model) finishPromptExecution(lifecycle *promptExecutionLifecycle) {
	if m == nil || lifecycle == nil {
		return
	}

	<-lifecycle.done
	m.promptExecutionMu.Lock()
	if m.activePromptExecution == lifecycle {
		m.activePromptExecution = nil
	}
	m.promptExecutionMu.Unlock()
}

func (m *Model) cancelActivePromptExecution() {
	if m == nil {
		return
	}

	m.promptExecutionMu.Lock()
	lifecycle := m.activePromptExecution
	m.promptExecutionMu.Unlock()
	if lifecycle == nil {
		return
	}

	approval := m.currentApproval()
	planApproval := m.currentPlanApproval()
	lifecycle.cancel()
	m.finishPromptExecution(lifecycle)
	_, _ = m.handleApprovalCleared(approvalClearedMsg{pending: approval})
	_, _ = m.handlePlanApprovalCleared(planApprovalClearedMsg{pending: planApproval})
}

// relayPromptExecution gives the worker a private event stream. The relay
// forwards live progress while execution is active, then drains the closed
// stream completely before publishing the result projection as the hard final
// event for that worker.
func relayPromptExecution(
	ctx context.Context,
	liveEvents chan<- tea.Msg,
	session *promptExecutionSession,
	run promptWorkerRun,
	lifecycle *promptExecutionLifecycle,
) {
	defer close(lifecycle.done)

	type outcome struct {
		answer     string
		completion promptRuntimeCompletionState
		err        error
	}

	completed := make(chan outcome, 1)
	go func() {
		executionCtx := ctx
		if session != nil && session.promptExecutionContext != nil {
			executionCtx = session.promptExecutionContext
		}
		answer, err := run(executionCtx, session)
		completed <- outcome{
			answer:     answer,
			completion: clonePromptRuntimeCompletionState(session.promptRuntimeCompletionState),
			err:        err,
		}
		session.closeUIEvents()
	}()

	forwarding := true
	for msg := range session.events {
		if !forwarding || ctx.Err() != nil {
			forwarding = false
			// Ownership cleanup is safe to enqueue after cancellation when the
			// event loop is still alive. Close also clears the exact pending
			// identities after joining, because a quitting program may no longer
			// consume this buffered event.
			if isPromptWorkerOwnershipCleanup(msg) {
				select {
				case liveEvents <- msg:
				default:
				}
			}
			continue
		}

		select {
		case liveEvents <- msg:
		case <-ctx.Done():
			forwarding = false
		}
	}

	result := <-completed
	if !forwarding || ctx.Err() != nil {
		return
	}

	select {
	case liveEvents <- promptWorkerResultMsg{
		answer:                          result.answer,
		completion:                      result.completion,
		promptConfigurationRevisionBase: session.promptConfigurationRevisionBase,
		contextErrorRevisionBase:        session.contextErrorRevisionBase,
		err:                             result.err,
		lifecycle:                       lifecycle,
	}:
	case <-ctx.Done():
	}
}

func isPromptWorkerOwnershipCleanup(msg tea.Msg) bool {
	switch msg.(type) {
	case approvalClearedMsg, planApprovalClearedMsg:
		return true
	default:
		return false
	}
}

// newDetachedPromptExecutionSession selects stable execution dependencies while
// cloning all turn state through promptRuntimeState. UI widgets, render caches,
// history panels, event-wait state, and live pending pointers remain owned by
// Update.
func newDetachedPromptExecutionSession(ctx context.Context, live *Model) *promptExecutionSession {
	workerEvents := make(chan tea.Msg, promptWorkerEventBuffer)

	return newPromptExecutionSession(ctx, live, workerEvents, true)
}

func newPromptExecutionSession(ctx context.Context, live *Model, events chan tea.Msg, projectTranscriptEvents bool) *promptExecutionSession {
	managedServers := live.managedServers
	if managedServers == nil {
		managedServers = newManagedServerRegistry()
		live.managedServers = managedServers
	}
	pinnedFiles := live.pinnedFiles
	if pinnedFiles == nil {
		pinnedFiles = newPinnedFileRegistry()
		live.pinnedFiles = pinnedFiles
	}
	rollbackRegistry := live.ensureRollbackRegistry()

	session := &promptExecutionSession{
		cfg:                    clonePromptWorkerConfig(live.cfg),
		client:                 live.client,
		controller:             live.controller,
		plannerSpec:            cloneLlamaServerSpec(live.plannerSpec),
		executorSpec:           cloneLlamaServerSpec(live.executorSpec),
		implementerClient:      live.implementerClient,
		backendRuntimeState:    live.backendRuntimeState,
		managedServers:         managedServers,
		workspace:              live.workspace,
		rollbackRegistry:       rollbackRegistry,
		events:                 events,
		promptExecutionContext: ctx,
		messages:               cloneChatMessages(live.messages),
		messagesRevision:       live.messagesRevision,
		promptRuntimeState:     clonePromptRuntimeState(live.promptRuntimeState),
		debugLogger:            live.debugLogger,
		debugLogPath:           live.debugLogPath,
		eventStream:            live.eventStream,
		agentModeName:          live.agentModeName,

		// PinnedFileRegistry is explicitly concurrency-safe and is runtime
		// infrastructure rather than presentation state. Sharing it preserves
		// operator pin/unpin actions that may arrive while a turn is active.
		pinnedFiles: pinnedFiles,

		instructionAttacher:             live.instructionAttacher,
		summaryClient:                   live.summaryClient,
		projectTranscriptEvents:         projectTranscriptEvents,
		promptConfigurationRevisionBase: live.promptConfigurationRevision,
		contextErrorRevisionBase:        live.contextErrorRevision,
	}
	session.rollbackHistory = rollbackRegistry.snapshot()
	executionCtx := ctx
	if _, bound := workspace.ExecutionSessionIDFromContext(executionCtx); !bound {
		executionSessionID := strings.TrimSpace(session.cfg.Workspace.SessionID)
		if executionSessionID == "" {
			executionSessionID = strings.TrimSpace(sessionlog.SessionIDFromPath(session.debugLogPath))
		}
		executionCtx = workspace.WithExecutionSessionID(executionCtx, executionSessionID)
	}
	executionCtx = workspace.WithSystemSearchAccessApprover(executionCtx, newTuiSystemSearchAccessApprover(session))
	executionSessionID, _ := workspace.ExecutionSessionIDFromContext(executionCtx)
	session.promptExecutionContext = workspace.WithMutationApprover(executionCtx, &tuiMutationApprover{
		session:   session,
		sessionID: executionSessionID,
	})

	return session
}

func (m *Model) handlePromptWorkerResult(msg promptWorkerResultMsg) (tea.Cmd, bool) {
	m.finishPromptExecution(msg.lifecycle)

	promptConfigurationChanged := m.promptConfigurationRevision != msg.promptConfigurationRevisionBase
	contextErrorChanged := m.contextErrorRevision != msg.contextErrorRevisionBase
	liveContextError := m.contextError
	completion := clonePromptRuntimeCompletionState(msg.completion)
	completion.rollbackHistory = m.ensureRollbackRegistry().snapshot()
	m.promptRuntimeCompletionState = completion
	if contextErrorChanged {
		m.contextError = liveContextError
	}
	if promptConfigurationChanged {
		// The operator may update /system or /profile while a turn is running.
		// Rebuild from the live configuration after projecting the worker so its
		// older prompt snapshot cannot roll back that command. Rebuilding also
		// incorporates instruction-file writes completed by the worker.
		if err := m.reloadPromptStackWithProfiles(m.activeProfiles); err != nil {
			m.setContextError("Prompt context failed: " + err.Error())
		}
	}

	return m.handleStreamDone(streamDoneMsg{err: msg.err})
}

func (m *Model) handlePromptTranscript(msg promptTranscriptMsg) (tea.Cmd, bool) {
	if msg.beforePending {
		m.insertBeforePendingAssistantOwned(cloneChatMessage(msg.message))
	} else {
		m.messages = append(m.messages, cloneChatMessage(msg.message))
		m.messagesRevision++
	}

	m.renderMessages(false)

	return nil, false
}

func (m *Model) handleApprovalPending(msg approvalPendingMsg) (tea.Cmd, bool) {
	m.approvalMu.Lock()
	m.approvalPending = msg.pending
	m.approvalMu.Unlock()

	return nil, false
}

func (m *Model) handleApprovalCleared(msg approvalClearedMsg) (tea.Cmd, bool) {
	m.approvalMu.Lock()
	if m.approvalPending == msg.pending {
		m.approvalPending = nil
	}
	m.approvalMu.Unlock()

	return nil, false
}

func (m *Model) handlePlanApprovalPending(msg planApprovalPendingMsg) (tea.Cmd, bool) {
	m.planApprovalMu.Lock()
	m.planApprovalPending = msg.pending
	m.planApprovalMu.Unlock()

	return nil, false
}

func (m *Model) handlePlanApprovalCleared(msg planApprovalClearedMsg) (tea.Cmd, bool) {
	m.planApprovalMu.Lock()
	if m.planApprovalPending == msg.pending {
		m.planApprovalPending = nil
	}
	m.planApprovalMu.Unlock()

	return nil, false
}

func (m *promptExecutionSession) appendPromptTranscriptMessage(message llama.ChatMessage) {
	message = cloneChatMessage(message)
	m.messages = append(m.messages, message)
	m.messagesRevision++
	if m.projectTranscriptEvents {
		m.queueUIEvent(promptTranscriptMsg{message: cloneChatMessage(message)})
	}
}

func (m *promptExecutionSession) insertBeforePendingAssistant(message llama.ChatMessage) {
	message = cloneChatMessage(message)
	idx := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" && strings.TrimSpace(m.messages[i].Content) == "" {
			idx = i

			break
		}
	}
	if idx < 0 {
		m.messages = append(m.messages, message)
	} else {
		m.messages = append(m.messages[:idx], append([]llama.ChatMessage{message}, m.messages[idx:]...)...)
	}
	m.messagesRevision++
	if m.projectTranscriptEvents {
		m.queueUIEvent(promptTranscriptMsg{
			message:       cloneChatMessage(message),
			beforePending: true,
		})
	}
}

func (m *promptExecutionSession) queueUIEvent(msg tea.Msg) {
	if m == nil || m.events == nil {
		return
	}
	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	if m.eventsClosed {
		return
	}
	if m.nonInteractive {
		select {
		case m.events <- msg:
		default:
		}

		return
	}

	m.events <- msg
}

func (m *promptExecutionSession) tryQueueUIEvent(msg tea.Msg) bool {
	if m == nil || m.events == nil {
		return false
	}

	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	if m.eventsClosed {
		return false
	}

	select {
	case m.events <- msg:
		return true
	default:
		return false
	}
}

func (m *promptExecutionSession) closeUIEvents() {
	if m == nil || m.events == nil {
		return
	}

	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	if m.eventsClosed {
		return
	}

	m.eventsClosed = true
	close(m.events)
}

func (m *promptExecutionSession) logMessage(role, content string) {
	if m == nil {
		return
	}
	if m.suppressPromptTranscript {
		if m.debugLogger != nil {
			redacted := content
			if normalizedRole := strings.TrimSpace(role); normalizedRole == "user" || normalizedRole == "assistant" {
				redacted = fmt.Sprintf("suppressed nested %s message (%d chars)", normalizedRole, len(content))
			}
			_ = m.debugLogger.LogRunEvent("internal_"+strings.TrimSpace(role), redacted)
		}

		return
	}

	m.eventStream.emitMessage(role, content)
	if m.debugLogger != nil {
		_ = m.debugLogger.LogMessage(role, content)
	}
}

func clonePromptWorkerConfig(cfg config.Config) config.Config {
	cloned := cfg
	cloned.LlamaServer.ExtraArgs = append([]string(nil), cfg.LlamaServer.ExtraArgs...)
	if cfg.Implementer != nil {
		implementer := *cfg.Implementer
		implementer.ExtraArgs = append([]string(nil), cfg.Implementer.ExtraArgs...)
		cloned.Implementer = &implementer
	}
	cloned.Chat.SystemPromptFiles = append([]string(nil), cfg.Chat.SystemPromptFiles...)
	cloned.Chat.ProjectPromptFiles = append([]string(nil), cfg.Chat.ProjectPromptFiles...)
	cloned.Chat.ProjectPromptCandidates = append([]string(nil), cfg.Chat.ProjectPromptCandidates...)
	cloned.Chat.DefaultProfiles = append([]string(nil), cfg.Chat.DefaultProfiles...)
	if cfg.Chat.EnableThinking != nil {
		enableThinking := *cfg.Chat.EnableThinking
		cloned.Chat.EnableThinking = &enableThinking
	}
	cloned.RouteClassifier.ExtraArgs = append([]string(nil), cfg.RouteClassifier.ExtraArgs...)
	cloned.Workspace.ToolRules = cloneConfigToolRules(cfg.Workspace.ToolRules)
	cloned.Workspace.ExternalTools = cloneConfigExternalTools(cfg.Workspace.ExternalTools)
	cloned.MCP = cloneConfigMCPServers(cfg.MCP)

	return cloned
}

func cloneLlamaServerSpec(spec config.LlamaServer) config.LlamaServer {
	spec.ExtraArgs = append([]string(nil), spec.ExtraArgs...)

	return spec
}

func cloneConfigToolRules(source map[string][]config.ToolRule) map[string][]config.ToolRule {
	if source == nil {
		return nil
	}

	cloned := make(map[string][]config.ToolRule, len(source))
	for tool, rules := range source {
		cloned[tool] = append([]config.ToolRule(nil), rules...)
	}

	return cloned
}

func cloneConfigExternalTools(source []config.ExternalToolDef) []config.ExternalToolDef {
	if source == nil {
		return nil
	}

	cloned := make([]config.ExternalToolDef, len(source))
	for i, tool := range source {
		tool.Args = append([]string(nil), tool.Args...)
		tool.InputSchema = append([]byte(nil), tool.InputSchema...)
		cloned[i] = tool
	}

	return cloned
}

func cloneConfigMCPServers(source []config.MCPServer) []config.MCPServer {
	if source == nil {
		return nil
	}

	cloned := make([]config.MCPServer, len(source))
	for i, server := range source {
		server.Args = append([]string(nil), server.Args...)
		server.Env = append([]string(nil), server.Env...)
		if server.Headers != nil {
			server.Headers = make(map[string]string, len(server.Headers))
			for key, value := range source[i].Headers {
				server.Headers[key] = value
			}
		}
		cloned[i] = server
	}

	return cloned
}

func clonePromptRuntimeState(state promptRuntimeState) promptRuntimeState {
	state.activeProfiles = append([]string(nil), state.activeProfiles...)
	state.requestHistoryOverride = cloneMessageHistoryPointer(state.requestHistoryOverride)
	state.forcedReadinessOpenObligations = append([]string(nil), state.forcedReadinessOpenObligations...)
	state.planExecuteScopeContract = state.planExecuteScopeContract.Normalized()
	state.planExecuteAuthorizedNewFiles = append([]string(nil), state.planExecuteAuthorizedNewFiles...)
	state.planExecuteWriteEnvelope = append([]string(nil), state.planExecuteWriteEnvelope...)
	state.promptRuntimeCompletionState = clonePromptRuntimeCompletionState(state.promptRuntimeCompletionState)

	return state
}

func clonePromptRuntimeCompletionState(state promptRuntimeCompletionState) promptRuntimeCompletionState {
	state.promptStack = clonePromptStack(state.promptStack)
	state.composedPromptSources = append([]promptstack.Source(nil), state.composedPromptSources...)
	state.sessionAgg = cloneSessionAggregate(state.sessionAgg)
	state.lastTurnSourceResults = cloneWorkspaceToolResults(state.lastTurnSourceResults)
	state.lastPlanExecuteRunResult = clonePlanExecuteRunResult(state.lastPlanExecuteRunResult)
	state.resumeImplementationState = cloneRecoveryImplementationState(state.resumeImplementationState)
	state.plan = clonePlanRuntimeState(state.plan)
	state.rollbackHistory = cloneRollbackHistory(state.rollbackHistory)

	return state
}

func cloneChatMessages(messages []llama.ChatMessage) []llama.ChatMessage {
	cloned := make([]llama.ChatMessage, len(messages))
	for i, message := range messages {
		cloned[i] = cloneChatMessage(message)
	}

	return cloned
}

func cloneChatMessage(message llama.ChatMessage) llama.ChatMessage {
	message.ToolCalls = append([]llama.ToolCall(nil), message.ToolCalls...)

	return message
}

func cloneMessageHistoryPointer(history *[]llama.ChatMessage) *[]llama.ChatMessage {
	if history == nil {
		return nil
	}

	cloned := cloneChatMessages(*history)

	return &cloned
}

func clonePromptStack(stack promptstack.Stack) promptstack.Stack {
	stack.Sources = append([]promptstack.Source(nil), stack.Sources...)
	stack.Warnings = append([]string(nil), stack.Warnings...)

	return stack
}

func cloneSessionAggregate(aggregate sessionAggregate) sessionAggregate {
	aggregate.toolOutputBytes = cloneStringIntMap(aggregate.toolOutputBytes)
	aggregate.toolCallCounts = cloneStringIntMap(aggregate.toolCallCounts)
	aggregate.discoveryClasses = cloneStringIntMap(aggregate.discoveryClasses)
	aggregate.discoveryCosts = cloneStringIntMap(aggregate.discoveryCosts)

	return aggregate
}

func cloneStringIntMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}

	cloned := make(map[string]int, len(source))
	for key, value := range source {
		cloned[key] = value
	}

	return cloned
}

func cloneStringBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}

	cloned := make(map[string]bool, len(source))
	for key, value := range source {
		cloned[key] = value
	}

	return cloned
}

func cloneNestedStringBoolMap(source map[string]map[string]bool) map[string]map[string]bool {
	if source == nil {
		return nil
	}

	cloned := make(map[string]map[string]bool, len(source))
	for key, value := range source {
		cloned[key] = cloneStringBoolMap(value)
	}

	return cloned
}

func clonePlanRuntimeState(state planRuntimeState) planRuntimeState {
	state.ledgerItems = cloneLedgerItems(state.ledgerItems)
	state.ledgerRenderedIDs = cloneStringBoolMap(state.ledgerRenderedIDs)
	state.ledgerHits = cloneNestedStringBoolMap(state.ledgerHits)
	state.submittedEdits = clonePlanEdits(state.submittedEdits)

	return state
}

func cloneLedgerItems(source map[string]workspace.TaskLedgerItem) map[string]workspace.TaskLedgerItem {
	if source == nil {
		return nil
	}

	cloned := make(map[string]workspace.TaskLedgerItem, len(source))
	for key, item := range source {
		item.Paths = append([]string(nil), item.Paths...)
		cloned[key] = item
	}

	return cloned
}

func clonePlanEdits(source map[string]planartifact.PlanEdit) map[string]planartifact.PlanEdit {
	if source == nil {
		return nil
	}

	cloned := make(map[string]planartifact.PlanEdit, len(source))
	for key, edit := range source {
		cloned[key] = clonePlanEdit(edit)
	}

	return cloned
}

func clonePlanEdit(edit planartifact.PlanEdit) planartifact.PlanEdit {
	if edit.AnchorProvenance != nil {
		provenance := *edit.AnchorProvenance
		edit.AnchorProvenance = &provenance
	}

	return edit
}

func cloneRollbackHistory(history []rollbackEntry) []rollbackEntry {
	cloned := make([]rollbackEntry, len(history))
	for i, entry := range history {
		entry.paths = append([]string(nil), entry.paths...)
		cloned[i] = entry
	}

	return cloned
}

func cloneWorkspaceToolResults(results []workspace.ToolResult) []workspace.ToolResult {
	cloned := make([]workspace.ToolResult, len(results))
	for i, result := range results {
		result.Paths = append([]string(nil), result.Paths...)
		result.ChangedPaths = append([]string(nil), result.ChangedPaths...)
		result.Relationships = append([]workspace.ToolRelationship(nil), result.Relationships...)
		result.Payload = append([]byte(nil), result.Payload...)
		result.Facts = result.Facts.Clone()
		cloned[i] = result
	}

	return cloned
}

func clonePlanExecuteRunResult(result *planexecute.RunResult) *planexecute.RunResult {
	if result == nil {
		return nil
	}

	cloned := *result
	if result.Completion != nil {
		completion := *result.Completion
		completion.RequiredEditPaths = append([]string(nil), completion.RequiredEditPaths...)
		completion.AllowedEditPaths = append([]string(nil), completion.AllowedEditPaths...)
		completion.WriteAllowedPaths = append([]string(nil), completion.WriteAllowedPaths...)
		completion.SourceSatisfiedEvidencePaths = append([]string(nil), completion.SourceSatisfiedEvidencePaths...)
		completion.SourceSatisfiedEvidence = append([]plancontract.SourceSatisfiedEvidenceRef(nil), completion.SourceSatisfiedEvidence...)
		completion.ChangedPaths = append([]string(nil), completion.ChangedPaths...)
		completion.RequiredPathCoverage = append([]plancontract.PlanExecuteRequiredPathCoverage(nil), completion.RequiredPathCoverage...)
		completion.SourceSanity = append([]plancontract.PhaseEvidenceCheck(nil), completion.SourceSanity...)
		completion.VerificationCommands = append([]plancontract.VerificationCommand(nil), completion.VerificationCommands...)
		completion.Verification = append([]plancontract.PhaseEvidenceCheck(nil), completion.Verification...)
		completion.VerificationSkips = append([]plancontract.VerificationSkip(nil), completion.VerificationSkips...)
		completion.VerificationRejects = append([]plancontract.VerificationReject(nil), completion.VerificationRejects...)
		completion.RequiredAcceptanceEvidence = append([]plancontract.AcceptanceEvidenceRequired(nil), completion.RequiredAcceptanceEvidence...)
		completion.RequiredAcceptanceEvidenceIDs = append([]string(nil), completion.RequiredAcceptanceEvidenceIDs...)
		completion.AcceptanceEvidenceIDs = append([]string(nil), completion.AcceptanceEvidenceIDs...)
		completion.AcceptanceEvidenceClaims = append([]plancontract.AcceptanceEvidenceClaim(nil), completion.AcceptanceEvidenceClaims...)
		completion.FailureInvariants = append([]plancontract.PlanExecuteTerminalInvariant(nil), completion.FailureInvariants...)
		completion.ValidationFailures = append([]string(nil), completion.ValidationFailures...)
		cloned.Completion = &completion
	}

	return &cloned
}
