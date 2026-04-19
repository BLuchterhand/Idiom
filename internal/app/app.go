package app

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/promptstack"
	"qwen-bubbles/internal/semantic"
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

	messages       []llama.ChatMessage
	promptStack    promptstack.Stack
	activeProfiles []string
	streaming      bool
	bootingServer  bool
	serverUp       bool
	managedServer  bool
	modelID        string
	status         string
	contextError   string
	runtimeError   string
	workingFrame   int
	debugLogger    *transcript.DebugLogger
	debugLogPath   string
	exploreCache   *exploreSessionCache
	pendingWrite   *writeApprovalProposal
	lastJournal    *executionJournal
	refactorStage  *structuralRefactorStage
	taskMemory     *longHorizonTaskMemory
	lastReplan     *analysisReplanRecommendationState
	traceMode      traceDisplayMode
	compactTrace   compactTraceState
	phaseCard      phaseCardState
	panelVisible   bool
	executionPanel executionPanelState
}

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

type assistantReplyMsg struct {
	text string
}

type transcriptSavedMsg struct {
	path string
	err  error
}

type infoMsg struct {
	text string
}

type exploreCacheInvalidatedMsg struct {
	reason string
}

type toolTraceMsg struct {
	text string
}

type phaseCardMsg struct {
	Lane   analysisLane
	Phase  string
	Detail string
	State  phaseDisplayState
	At     time.Time
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

type phaseCardState struct {
	Lane   analysisLane
	Phase  string
	Detail string
	State  phaseDisplayState
	At     time.Time
}

type structuralRefactorStage struct {
	Active       bool
	Stage        int
	SourceLane   string
	FocusPath    string
	Changed      []string
	Remaining    []string
	Strategy     []string
	NextStep     string
	Verification string
}

type longHorizonTaskMemory struct {
	Active              bool                     `json:"active"`
	Kind                string                   `json:"kind,omitempty"`
	Prompt              string                   `json:"prompt,omitempty"`
	Lane                string                   `json:"lane,omitempty"`
	Summary             string                   `json:"summary,omitempty"`
	OpenGaps            []string                 `json:"open_gaps,omitempty"`
	AttemptedSearches   []string                 `json:"attempted_searches,omitempty"`
	ChosenTargets       []string                 `json:"chosen_targets,omitempty"`
	VerificationHistory []string                 `json:"verification_history,omitempty"`
	NextActions         []string                 `json:"next_actions,omitempty"`
	FileFingerprints    map[string]taskFileStamp `json:"file_fingerprints,omitempty"`
	CreatedAt           time.Time                `json:"created_at,omitempty"`
	UpdatedAt           time.Time                `json:"updated_at,omitempty"`
}

type taskFileStamp struct {
	Size        int64 `json:"size"`
	ModTimeUnix int64 `json:"mod_time_unix"`
}

type executionPanelState struct {
	Request      string
	Lane         analysisLane
	FollowupLane analysisLane
	RouteReason  promptRouteReason
	Contract     answerContract
	Summary      string
	Steps        []string
	Progress     []string
	Verification verificationPlan
	UpdatedAt    time.Time
}

type writeApprovalProposalMsg struct {
	proposal writeApprovalProposal
}

type analysisLane string

const (
	analysisLaneMain         analysisLane = "main"
	analysisLaneLocate       analysisLane = "locate"
	analysisLaneExplore      analysisLane = "explore"
	analysisLaneReview       analysisLane = "review"
	analysisLaneArchitecture analysisLane = "architecture"
	analysisLaneImplement    analysisLane = "implement"
)

type promptRouteReason string

const (
	promptRouteReasonDirectConversation   promptRouteReason = "direct_conversation"
	promptRouteReasonDirectImplementation promptRouteReason = "direct_implementation"
	promptRouteReasonLocateRequest        promptRouteReason = "locate_request"
	promptRouteReasonExploreRequest       promptRouteReason = "explore_request"
	promptRouteReasonReviewRequest        promptRouteReason = "review_request"
	promptRouteReasonArchitectureRequest  promptRouteReason = "architecture_request"
)

type answerContract string

const (
	answerContractDirectConversation  answerContract = "direct_answer"
	answerContractLocateTargets       answerContract = "locate_targets"
	answerContractGroundedExplanation answerContract = "grounded_explanation"
	answerContractReviewFindings      answerContract = "review_findings"
	answerContractArchitecture        answerContract = "architecture_assessment"
	answerContractImplementation      answerContract = "implementation_result"
)

type promptExecutionPlan struct {
	lane         analysisLane
	followupLane analysisLane
	thoroughness workspace.ExploreThoroughness
	reason       promptRouteReason
	contract     answerContract
}

type analysisReplanRecommendationState struct {
	FromLane analysisLane
	ToLane   analysisLane
	Reason   string
	Summary  string
}

type laneContractPolicy struct {
	Contract  answerContract
	Expected  []string
	Forbidden []string
	Fallback  string
}

type writeApprovalKind string

const (
	writeApprovalProtectedPath writeApprovalKind = "protected_path"
	writeApprovalBroadScope    writeApprovalKind = "broad_scope"
)

type writeApprovalProposal struct {
	Prompt string
	Tool   string
	Kind   writeApprovalKind
	Paths  []string
	Reason string

	PlannedEdits []string
	PreviewHunks []string

	Risk           writeApprovalRisk
	ChangeKinds    []string
	RiskReasons    []string
	ImpactSummary  []string
	BlastRadius    []string
	DeepAnalysis   []string
	AffectedTests  []string
	ApprovalNotes  []string
	VerifyCoverage string
	VerifySteps    []string
	VerifyRisk     string
	VerifyExpected []string
	VerifyRepair   string
}

type writeApprovalGrant struct {
	Tool  string
	Paths map[string]struct{}
}

func (p writeApprovalProposal) bannerText() string {
	scope := "guarded write approval pending"
	switch p.Kind {
	case writeApprovalProtectedPath:
		scope = "protected path approval pending"
	case writeApprovalBroadScope:
		scope = "broad write approval pending"
	}
	if strings.TrimSpace(string(p.Risk)) != "" {
		scope += " [" + string(p.Risk) + " risk]"
	}
	return scope + ": " + strings.Join(p.Paths, ", ") + " | /approve writes | /deny writes"
}

func (p writeApprovalProposal) statusText() string {
	if strings.TrimSpace(string(p.Risk)) != "" {
		return "Pending " + string(p.Risk) + "-risk guarded write approval. Use /approve writes to rerun with the proposed scope, /deny writes to cancel, or /writes auto for broader autonomy."
	}
	return "Pending guarded write approval. Use /approve writes to rerun with the proposed scope, /deny writes to cancel, or /writes auto for broader autonomy."
}

func (p writeApprovalProposal) renderSummary() string {
	var b strings.Builder
	b.WriteString("Guarded write approval required.\n\n")
	switch p.Kind {
	case writeApprovalProtectedPath:
		b.WriteString("This request wants to modify a protected path under guarded writes.\n\n")
	case writeApprovalBroadScope:
		b.WriteString("This request wants to expand beyond the default guarded write scope.\n\n")
	default:
		b.WriteString("This request needs a broader write scope than guarded mode allows by default.\n\n")
	}
	b.WriteString("Proposed scope:\n")
	for _, path := range p.Paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteString("\n")
	}
	if strings.TrimSpace(p.Reason) != "" {
		b.WriteString("\nReason:\n- ")
		b.WriteString(strings.TrimSpace(p.Reason))
		b.WriteString("\n")
	}
	if len(p.PlannedEdits) > 0 {
		b.WriteString("\nProposed edits:\n")
		for _, line := range p.PlannedEdits {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.PreviewHunks) > 0 {
		b.WriteString("\nDiff preview:\n")
		for _, hunk := range p.PreviewHunks {
			hunk = strings.TrimSpace(hunk)
			if hunk == "" {
				continue
			}
			b.WriteString("```diff\n")
			b.WriteString(hunk)
			b.WriteString("\n```\n")
		}
	}
	if strings.TrimSpace(string(p.Risk)) != "" {
		b.WriteString("\nAssessed risk:\n- ")
		b.WriteString(string(p.Risk))
		b.WriteString("\n")
	}
	if len(p.ChangeKinds) > 0 {
		b.WriteString("\nChange classes:\n")
		for _, line := range p.ChangeKinds {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.RiskReasons) > 0 {
		b.WriteString("\nRisk drivers:\n")
		for _, line := range p.RiskReasons {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.ImpactSummary) > 0 {
		b.WriteString("\nImpact summary:\n")
		for _, line := range p.ImpactSummary {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.BlastRadius) > 0 {
		b.WriteString("\nBlast radius:\n")
		for _, line := range p.BlastRadius {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.DeepAnalysis) > 0 {
		b.WriteString("\nDeep analysis:\n")
		for _, line := range p.DeepAnalysis {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.AffectedTests) > 0 {
		b.WriteString("\nAffected test surfaces:\n")
		for _, line := range p.AffectedTests {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.ApprovalNotes) > 0 {
		b.WriteString("\nApproval guidance:\n")
		for _, line := range p.ApprovalNotes {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(p.VerifyCoverage) != "" {
		b.WriteString("\nVerification confidence:\n- ")
		b.WriteString(strings.TrimSpace(p.VerifyCoverage))
		b.WriteString("\n")
	}
	if strings.TrimSpace(p.VerifyRisk) != "" || len(p.VerifyExpected) > 0 || strings.TrimSpace(p.VerifyRepair) != "" {
		b.WriteString("\nVerification risk contract:\n")
		if strings.TrimSpace(p.VerifyRisk) != "" {
			b.WriteString("- risk: ")
			b.WriteString(strings.TrimSpace(p.VerifyRisk))
			b.WriteString("\n")
		}
		if len(p.VerifyExpected) > 0 {
			b.WriteString("- expected failure modes: ")
			b.WriteString(strings.Join(uniqueLimitedStrings(p.VerifyExpected, 4), ", "))
			b.WriteString("\n")
		}
		if strings.TrimSpace(p.VerifyRepair) != "" {
			b.WriteString("- repair policy: ")
			b.WriteString(truncateText(strings.TrimSpace(p.VerifyRepair), 180))
			b.WriteString("\n")
		}
	}
	if len(p.VerifySteps) > 0 {
		b.WriteString("\nRecommended verification:\n")
		for _, step := range p.VerifySteps {
			b.WriteString("- `")
			b.WriteString(step)
			b.WriteString("`\n")
		}
	}
	b.WriteString("\nNext step:\n- `/approve writes` to rerun this request with only that exact scope unlocked\n- `/deny writes` to clear the proposal\n- `/writes auto` if you want broader write autonomy")
	return b.String()
}

func (p writeApprovalProposal) approvalNote() string {
	return "Write approval granted for: " + strings.Join(p.Paths, ", ") + ". Re-running the request within that exact guarded scope."
}

func (p writeApprovalProposal) grant() *writeApprovalGrant {
	paths := make(map[string]struct{}, len(p.Paths))
	for _, path := range p.Paths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		paths[path] = struct{}{}
	}
	return &writeApprovalGrant{
		Tool:  strings.TrimSpace(p.Tool),
		Paths: paths,
	}
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
		m.setPendingAssistantContent(msg.text)
		m.logMessage("assistant", msg.text)
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

func (m *Model) View() string {
	if !m.ready {
		return "Loading UI..."
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Padding(0, 1)
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("109")).Padding(0, 1)
	approvalStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("136")).Padding(0, 1)
	errorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("160")).Padding(0, 1)

	serverState := "down"
	if m.serverUp {
		serverState = "up"
	}
	if m.bootingServer {
		serverState = m.spinner.View() + " booting"
	}

	header := titleStyle.Render(m.cfg.AppName) +
		statusStyle.Render(fmt.Sprintf("server:%s model:%s", serverState, fallback(m.modelID, "unknown")))
	body := m.bodyView()
	contextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Padding(0, 1)
	footer := helpStyle.Render("Enter send | PgUp/PgDn scroll | Mouse wheel scroll | Ctrl+P panel | Ctrl+S save | Ctrl+L clear | Ctrl+R restart | Ctrl+C quit | /help") + "\n" +
		contextStyle.Render("context: "+m.contextSummary()) + "\n" +
		contextStyle.Render("mode: "+m.modeSummary()) + "\n" +
		contextStyle.Render("writes: "+m.writeSummary()) + "\n" +
		contextStyle.Render("panel: "+m.panelSummary()) + "\n" +
		contextStyle.Render("trace: "+m.traceSummary()) + "\n" +
		contextStyle.Render("workspace: "+m.workspaceSummary()) + "\n" +
		statusStyle.Render(m.status)
	if card := m.phaseCardView(); card != "" {
		footer += "\n" + m.phaseCardStyle().Render(card)
	}
	if banner := m.writeApprovalBanner(); banner != "" {
		footer += "\n" + approvalStyle.Render(banner)
	}
	if banner := m.errorBanner(); banner != "" {
		footer += "\n" + errorStyle.Render(banner)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.input.View(), footer)
}

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

func (m *Model) RunPromptOnce(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if err := m.ensureServerReady(ctx); err != nil {
		return "", err
	}
	plan := classifyPromptExecutionPlan(prompt, m.workspace)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.logMessage("user", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	reply, err := m.runPromptPlan(ctx, prompt, m.requestMessages(), m.chatOptions(), plan)
	if err != nil {
		return "", err
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply})
	m.logMessage("assistant", reply)
	return reply, nil
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
		m.status = "Commands: /save /clear /system <text> /temp <value> /preset [list|name] /server <start|stop|restart|status> /profile <list|add|remove> /context /workspace /pwd /debuglog [mode redacted|full] /trace [compact|detail] /panel [show|hide|toggle] /mode [normal|explore [quick|medium|thorough]] /writes [guarded|auto] /approve writes /deny writes /undo /resume [refactor|task] /replan /help | transcript: PgUp/PgDn or mouse wheel"
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

func (m *Model) saveTranscriptAsync() {
	m.status = "Saving transcript."

	messages := append([]llama.ChatMessage(nil), m.messages...)
	go func() {
		path, err := transcript.SaveMarkdown(m.cfg.Chat.SaveDir, messages)
		m.events <- transcriptSavedMsg{path: path, err: err}
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

func (m *Model) canResumeStructuralRefactor() bool {
	return structuralRefactorStageResumable(m.currentStructuralRefactorStage())
}

func (m *Model) currentStructuralRefactorStage() *structuralRefactorStage {
	if m == nil {
		return nil
	}
	if m.lastJournal != nil && m.lastJournal.Refactor != nil {
		return m.lastJournal.Refactor
	}
	return m.refactorStage
}

func structuralRefactorStageResumable(stage *structuralRefactorStage) bool {
	if stage == nil || len(stage.Remaining) == 0 {
		return false
	}
	return !strings.Contains(strings.ToLower(stage.NextStep), "repair or roll back")
}

type persistedStructuralRefactorStage struct {
	Version       int                     `json:"version"`
	WorkspaceRoot string                  `json:"workspace_root,omitempty"`
	UpdatedAt     time.Time               `json:"updated_at"`
	Stage         structuralRefactorStage `json:"stage"`
}

const structuralRefactorStageStateVersion = 1

type persistedTaskMemory struct {
	Version       int                   `json:"version"`
	WorkspaceRoot string                `json:"workspace_root,omitempty"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Memory        longHorizonTaskMemory `json:"memory"`
}

const taskMemoryStateVersion = 1

func (m *Model) structuralRefactorStageStatePath() string {
	if m == nil || strings.TrimSpace(m.cfg.Chat.SaveDir) == "" {
		return ""
	}
	return filepath.Join(m.cfg.Chat.SaveDir, "state", "structural-refactor-stage.json")
}

func (m *Model) taskMemoryStatePath() string {
	if m == nil || strings.TrimSpace(m.cfg.Chat.SaveDir) == "" {
		return ""
	}
	return filepath.Join(m.cfg.Chat.SaveDir, "state", "task-memory.json")
}

func (m *Model) workspaceRootForState() string {
	if m == nil || m.workspace == nil {
		return ""
	}
	return filepath.Clean(m.workspace.Root())
}

func cloneStructuralRefactorStage(stage *structuralRefactorStage) *structuralRefactorStage {
	if stage == nil {
		return nil
	}
	cloned := *stage
	cloned.Changed = append([]string(nil), stage.Changed...)
	cloned.Remaining = append([]string(nil), stage.Remaining...)
	cloned.Strategy = append([]string(nil), stage.Strategy...)
	return &cloned
}

func cloneTaskMemory(memory *longHorizonTaskMemory) *longHorizonTaskMemory {
	if memory == nil {
		return nil
	}
	cloned := *memory
	cloned.OpenGaps = append([]string(nil), memory.OpenGaps...)
	cloned.AttemptedSearches = append([]string(nil), memory.AttemptedSearches...)
	cloned.ChosenTargets = append([]string(nil), memory.ChosenTargets...)
	cloned.VerificationHistory = append([]string(nil), memory.VerificationHistory...)
	cloned.NextActions = append([]string(nil), memory.NextActions...)
	if len(memory.FileFingerprints) > 0 {
		cloned.FileFingerprints = make(map[string]taskFileStamp, len(memory.FileFingerprints))
		for path, stamp := range memory.FileFingerprints {
			cloned.FileFingerprints[path] = stamp
		}
	}
	return &cloned
}

func (m *Model) loadPersistedStructuralRefactorStage() {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state persistedStructuralRefactorStage
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.Version != structuralRefactorStageStateVersion {
		return
	}
	if root := m.workspaceRootForState(); root != "" && state.WorkspaceRoot != "" && filepath.Clean(state.WorkspaceRoot) != root {
		return
	}
	stage := cloneStructuralRefactorStage(&state.Stage)
	if !structuralRefactorStageResumable(stage) {
		return
	}
	m.refactorStage = stage
}

func (m *Model) persistStructuralRefactorStage(stage *structuralRefactorStage) {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	if !structuralRefactorStageResumable(stage) {
		m.removePersistedStructuralRefactorStage()
		return
	}
	state := persistedStructuralRefactorStage{
		Version:       structuralRefactorStageStateVersion,
		WorkspaceRoot: m.workspaceRootForState(),
		UpdatedAt:     time.Now(),
		Stage:         *cloneStructuralRefactorStage(stage),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o644)
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

func (m *Model) logTaskMemoryState(action string, memory *longHorizonTaskMemory, reason string) {
	if m == nil {
		return
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return
	}
	payload := struct {
		Action              string   `json:"action"`
		MemoryKind          string   `json:"memory_kind,omitempty"`
		SourceLane          string   `json:"source_lane,omitempty"`
		OpenGaps            []string `json:"open_gaps,omitempty"`
		AttemptedSearches   []string `json:"attempted_searches,omitempty"`
		ChosenTargets       []string `json:"chosen_targets,omitempty"`
		VerificationHistory []string `json:"verification_history,omitempty"`
		NextActions         []string `json:"next_actions,omitempty"`
		Resumable           bool     `json:"resumable"`
		Reason              string   `json:"reason,omitempty"`
	}{
		Action:    action,
		Resumable: taskMemoryResumable(memory),
		Reason:    strings.TrimSpace(reason),
	}
	if memory != nil {
		payload.MemoryKind = strings.TrimSpace(memory.Kind)
		payload.SourceLane = strings.TrimSpace(memory.Lane)
		payload.OpenGaps = uniqueNonEmptyStrings(memory.OpenGaps)
		payload.AttemptedSearches = uniqueNonEmptyStrings(memory.AttemptedSearches)
		payload.ChosenTargets = uniqueNonEmptyStrings(memory.ChosenTargets)
		payload.VerificationHistory = uniqueNonEmptyStrings(memory.VerificationHistory)
		payload.NextActions = uniqueNonEmptyStrings(memory.NextActions)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("long_horizon_state", string(data))
}

func taskMemoryResumable(memory *longHorizonTaskMemory) bool {
	if memory == nil || !memory.Active {
		return false
	}
	return len(memory.NextActions) > 0 || len(memory.OpenGaps) > 0 || len(memory.ChosenTargets) > 0 || len(memory.VerificationHistory) > 0
}

func (m *Model) loadPersistedTaskMemory() {
	path := m.taskMemoryStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state persistedTaskMemory
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.Version != taskMemoryStateVersion {
		return
	}
	if root := m.workspaceRootForState(); root != "" && state.WorkspaceRoot != "" && filepath.Clean(state.WorkspaceRoot) != root {
		return
	}
	memory := cloneTaskMemory(&state.Memory)
	if !taskMemoryResumable(memory) {
		return
	}
	if stale, reason := m.taskMemoryStale(memory); stale {
		m.logTaskMemoryState("clear", memory, reason)
		m.removePersistedTaskMemory()
		return
	}
	m.taskMemory = memory
}

func (m *Model) persistTaskMemory(memory *longHorizonTaskMemory) {
	path := m.taskMemoryStatePath()
	if path == "" {
		return
	}
	if !taskMemoryResumable(memory) {
		m.removePersistedTaskMemory()
		return
	}
	cloned := cloneTaskMemory(memory)
	now := time.Now()
	if cloned.CreatedAt.IsZero() {
		cloned.CreatedAt = now
	}
	cloned.UpdatedAt = now
	cloned.FileFingerprints = m.taskMemoryFileFingerprints(cloned.ChosenTargets)
	state := persistedTaskMemory{
		Version:       taskMemoryStateVersion,
		WorkspaceRoot: m.workspaceRootForState(),
		UpdatedAt:     now,
		Memory:        *cloned,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err == nil {
		m.taskMemory = cloned
	}
}

func (m *Model) removePersistedTaskMemory() {
	path := m.taskMemoryStatePath()
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (m *Model) clearPersistedTaskMemory(reason string) {
	if m == nil {
		return
	}
	if m.taskMemory != nil {
		m.logTaskMemoryState("clear", m.taskMemory, reason)
	}
	m.taskMemory = nil
	m.removePersistedTaskMemory()
}

func (m *Model) rememberTaskMemory(memory *longHorizonTaskMemory, reason string) {
	if m == nil || !taskMemoryResumable(memory) {
		return
	}
	m.persistTaskMemory(memory)
	m.logTaskMemoryState("persist", memory, reason)
}

func (m *Model) taskMemoryFileFingerprints(paths []string) map[string]taskFileStamp {
	root := m.workspaceRootForState()
	if root == "" {
		return nil
	}
	out := make(map[string]taskFileStamp)
	for _, path := range uniqueNormalizedPaths(paths) {
		full := filepath.Join(root, filepath.FromSlash(path))
		stat, err := os.Stat(full)
		if err != nil || stat.IsDir() {
			continue
		}
		out[path] = taskFileStamp{Size: stat.Size(), ModTimeUnix: stat.ModTime().UnixNano()}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m *Model) taskMemoryStale(memory *longHorizonTaskMemory) (bool, string) {
	if memory == nil || len(memory.FileFingerprints) == 0 {
		return false, ""
	}
	root := m.workspaceRootForState()
	if root == "" {
		return false, ""
	}
	for path, expected := range memory.FileFingerprints {
		full := filepath.Join(root, filepath.FromSlash(path))
		stat, err := os.Stat(full)
		if err != nil {
			return true, "tracked task-memory target is no longer readable: " + path
		}
		actual := taskFileStamp{Size: stat.Size(), ModTimeUnix: stat.ModTime().UnixNano()}
		if actual != expected {
			return true, "tracked task-memory target changed outside the saved task state: " + path
		}
	}
	return false, ""
}

func (m *Model) removePersistedStructuralRefactorStage() {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (m *Model) clearPersistedStructuralRefactorStage() {
	if m == nil {
		return
	}
	m.refactorStage = nil
	m.removePersistedStructuralRefactorStage()
}

func looksLikeResumeStructuralRefactorPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return containsAny(normalized, "continue", "resume", "next stage", "next refactor") &&
		containsAny(normalized, "refactor", "structural", "architecture", "stage")
}

func looksLikeResumeTaskMemoryPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return containsAny(normalized, "continue", "resume", "follow up", "next step", "pick up") &&
		containsAny(normalized, "task", "investigation", "search", "repair", "evidence", "debug", "issue", "failure")
}

func (m *Model) resumeStructuralRefactorAsync(userPrompt string) {
	if m.streaming {
		m.status = "Wait for the current response to finish before resuming the refactor."
		return
	}
	stage := m.currentStructuralRefactorStage()
	if stage == nil {
		m.status = "No resumable structural refactor stage is available."
		return
	}
	if len(stage.Remaining) == 0 {
		m.status = "The last structural refactor stage has no remaining target files to resume."
		return
	}
	if !structuralRefactorStageResumable(stage) {
		m.status = "The last structural refactor stage must be repaired or rolled back before resuming."
		return
	}

	prompt := buildStructuralRefactorResumePrompt(stage, userPrompt)
	analysisSummary := buildStructuralRefactorResumeAnalysisSummary(stage)
	plan := promptExecutionPlan{
		lane:         analysisLaneImplement,
		thoroughness: workspace.ThoroughnessMedium,
		reason:       promptRouteReasonDirectImplementation,
	}
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.logLongHorizonState("resume", stage, "resuming next bounded structural refactor stage")
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Resuming the next bounded structural refactor stage.")
	m.status = "Resuming structural refactor stage."
	m.renderMessages(true)

	history := m.requestMessages()
	history = appendAnalysisSummary(history, "architecture", analysisSummary)
	opts := m.chatOptions()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runPromptPlan(ctx, prompt, history, opts, plan)
		if err == nil {
			m.events <- assistantReplyMsg{text: reply}
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) currentTaskMemory() *longHorizonTaskMemory {
	if m == nil {
		return nil
	}
	if stale, reason := m.taskMemoryStale(m.taskMemory); stale {
		m.clearPersistedTaskMemory(reason)
		return nil
	}
	return m.taskMemory
}

func (m *Model) resumeTaskMemoryAsync(userPrompt string) {
	if m.streaming {
		m.status = "Wait for the current response to finish before resuming the saved task."
		return
	}
	memory := m.currentTaskMemory()
	if !taskMemoryResumable(memory) {
		m.status = "No resumable investigation or repair task is available."
		return
	}
	prompt := buildTaskMemoryResumePrompt(memory, userPrompt)
	plan := promptExecutionPlan{
		lane:         laneForTaskMemory(memory),
		thoroughness: workspace.ThoroughnessMedium,
		reason:       routeReasonForLane(laneForTaskMemory(memory)),
		contract:     answerContractForLane(laneForTaskMemory(memory)),
	}
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(memory.ChosenTargets)
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.logTaskMemoryState("resume", memory, "resuming saved investigation or repair task")
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setPhaseCard(plan.lane, "resume", phaseStateActive, "Resuming saved task memory without repeating completed broad search.")
	m.status = "Resuming saved task memory."
	m.renderMessages(true)

	history := m.requestMessages()
	history = append(history, llama.ChatMessage{Role: "system", Content: renderTaskMemoryForModel(memory)})
	opts := m.chatOptions()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runPromptPlan(ctx, prompt, history, opts, plan)
		if err == nil {
			m.events <- assistantReplyMsg{text: reply}
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func laneForTaskMemory(memory *longHorizonTaskMemory) analysisLane {
	if memory == nil {
		return analysisLaneExplore
	}
	switch analysisLane(strings.TrimSpace(memory.Lane)) {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture, analysisLaneImplement:
		return analysisLane(strings.TrimSpace(memory.Lane))
	default:
		return analysisLaneExplore
	}
}

func buildTaskMemoryResumePrompt(memory *longHorizonTaskMemory, userPrompt string) string {
	if memory == nil {
		return ""
	}
	lines := []string{
		"Resume the saved long-horizon task from persisted memory.",
	}
	if strings.TrimSpace(userPrompt) != "" {
		lines = append(lines, "User resume request: "+strings.TrimSpace(userPrompt))
	}
	if strings.TrimSpace(memory.Prompt) != "" {
		lines = append(lines, "Original request: "+strings.TrimSpace(memory.Prompt))
	}
	if strings.TrimSpace(memory.Lane) != "" {
		lines = append(lines, "Prior lane: "+strings.TrimSpace(memory.Lane))
	}
	if len(memory.ChosenTargets) > 0 {
		lines = append(lines, "Previously validated targets: "+strings.Join(memory.ChosenTargets, ", "))
	}
	if len(memory.AttemptedSearches) > 0 {
		lines = append(lines, "Already attempted: "+strings.Join(memory.AttemptedSearches, " | "))
	}
	if len(memory.OpenGaps) > 0 {
		lines = append(lines, "Open evidence gaps: "+strings.Join(memory.OpenGaps, " | "))
	}
	if len(memory.VerificationHistory) > 0 {
		lines = append(lines, "Verification history: "+strings.Join(memory.VerificationHistory, " | "))
	}
	if len(memory.NextActions) > 0 {
		lines = append(lines, "Next actions: "+strings.Join(memory.NextActions, " | "))
	}
	lines = append(lines,
		"Rules:",
		"- Do not repeat broad search that is already recorded above.",
		"- Start with the next action or the smallest unresolved evidence gap.",
		"- Revalidate any cited target before relying on it.",
		"- Do not edit files unless the resumed lane is implementation or legally transitions to implementation.",
	)
	return strings.Join(lines, "\n")
}

func renderTaskMemoryForModel(memory *longHorizonTaskMemory) string {
	if memory == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Persisted task memory:\n")
	if strings.TrimSpace(memory.Kind) != "" {
		b.WriteString("- kind: " + strings.TrimSpace(memory.Kind) + "\n")
	}
	if strings.TrimSpace(memory.Summary) != "" {
		b.WriteString("- prior summary: " + truncateText(memory.Summary, 800) + "\n")
	}
	if len(memory.ChosenTargets) > 0 {
		b.WriteString("- chosen targets: " + strings.Join(memory.ChosenTargets, ", ") + "\n")
	}
	if len(memory.OpenGaps) > 0 {
		b.WriteString("- open gaps: " + strings.Join(memory.OpenGaps, " | ") + "\n")
	}
	if len(memory.NextActions) > 0 {
		b.WriteString("- next actions: " + strings.Join(memory.NextActions, " | ") + "\n")
	}
	return strings.TrimSpace(b.String())
}

func buildAnalysisTaskMemory(prompt string, lane analysisLane, summary string, evidencePaths []string, nextAction string) *longHorizonTaskMemory {
	targets := uniqueLimitedStrings(append(promptMentionedWorkspacePaths(summary), evidencePaths...), 16)
	attempts := taskMemoryAttemptsFromEvidence(targets)
	gaps := inferTaskMemoryOpenGaps(summary, lane)
	nextActions := uniqueNonEmptyStrings([]string{nextAction})
	if len(nextActions) == 0 {
		if len(gaps) > 0 {
			nextActions = append(nextActions, "resume with targeted validation of the unresolved evidence gaps")
		} else if len(targets) > 0 {
			nextActions = append(nextActions, "resume from the validated target set before widening search")
		}
	}
	return &longHorizonTaskMemory{
		Active:            true,
		Kind:              "investigation",
		Prompt:            strings.TrimSpace(prompt),
		Lane:              string(lane),
		Summary:           strings.TrimSpace(summary),
		OpenGaps:          gaps,
		AttemptedSearches: attempts,
		ChosenTargets:     targets,
		NextActions:       nextActions,
		CreatedAt:         time.Now(),
	}
}

func buildRepairTaskMemory(prompt string, changedPaths []string, verify implementationVerification, targetPacket implementationTargetPacket) *longHorizonTaskMemory {
	targets := uniqueLimitedStrings(append(uniqueNormalizedPaths(changedPaths), targetPacket.PrimaryPaths...), 16)
	history := verificationHistoryForMemory(verify)
	nextActions := []string{"repair failing verification before widening implementation scope"}
	if len(targets) > 0 {
		nextActions = append(nextActions, "start from changed target "+targets[0])
	}
	gaps := []string{"verification is failing for the latest bounded implementation"}
	gaps = append(gaps, verify.expectedFailures...)
	return &longHorizonTaskMemory{
		Active:              true,
		Kind:                "partial_repair",
		Prompt:              strings.TrimSpace(prompt),
		Lane:                string(analysisLaneImplement),
		Summary:             "Latest bounded implementation needs repair before the task can continue.",
		OpenGaps:            uniqueNonEmptyStrings(gaps),
		AttemptedSearches:   taskMemoryAttemptsFromEvidence(targets),
		ChosenTargets:       targets,
		VerificationHistory: history,
		NextActions:         uniqueNonEmptyStrings(nextActions),
		CreatedAt:           time.Now(),
	}
}

func taskMemoryAttemptsFromEvidence(paths []string) []string {
	paths = uniqueNormalizedPaths(paths)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, "validated target "+path)
	}
	return out
}

func inferTaskMemoryOpenGaps(summary string, lane analysisLane) []string {
	normalized := strings.ToLower(strings.TrimSpace(summary))
	gaps := make([]string, 0, 3)
	if reason, low := lowConfidenceReadinessReason(summary); low {
		gaps = append(gaps, reason)
	}
	if containsAny(normalized, "not found", "no match", "no matches") {
		gaps = append(gaps, "requested target was not found in the searched evidence")
	}
	if containsAny(normalized, "not enough evidence", "insufficient evidence", "unable to confirm", "cannot confirm") {
		gaps = append(gaps, "claim still needs narrower evidence before it can be treated as confirmed")
	}
	switch lane {
	case analysisLaneReview:
		if containsAny(normalized, "no bounded", "without a bounded", "no actionable finding") {
			gaps = append(gaps, "no bounded actionable finding was confirmed")
		}
	case analysisLaneArchitecture:
		if containsAny(normalized, "needs deeper validation", "deeper validation", "next validation") {
			gaps = append(gaps, "structural recommendation needs deeper validation before implementation")
		}
	}
	return uniqueNonEmptyStrings(gaps)
}

func verificationHistoryForMemory(verify implementationVerification) []string {
	history := make([]string, 0, len(verify.results)+len(verify.commands))
	for i, command := range verify.commands {
		status := "passed"
		if i < len(verify.results) && strings.TrimSpace(verify.results[i].Error) != "" {
			status = "failed: " + truncateText(verify.results[i].Error, 160)
		}
		history = append(history, command+" -> "+status)
	}
	if len(history) == 0 && verify.failed {
		history = append(history, "verification failed")
	}
	return uniqueNonEmptyStrings(history)
}

func taskMemoryNextActionForReplan(target analysisLane, reason string) string {
	if target == "" {
		return ""
	}
	return fmt.Sprintf("resume via %s lane: %s", target, strings.TrimSpace(reason))
}

func (m *Model) rememberAnalysisTaskMemory(prompt string, lane analysisLane, summary string, evidencePaths []string, nextAction string) {
	memory := buildAnalysisTaskMemory(prompt, lane, summary, evidencePaths, nextAction)
	m.rememberTaskMemory(memory, "analysis stopped with resumable evidence and next actions")
}

func (m *Model) rememberAnalysisReplan(from analysisLane, to analysisLane, reason string, summary string) {
	if m == nil || from == "" || to == "" || strings.TrimSpace(reason) == "" {
		return
	}
	m.lastReplan = &analysisReplanRecommendationState{
		FromLane: from,
		ToLane:   to,
		Reason:   strings.TrimSpace(reason),
		Summary:  strings.TrimSpace(summary),
	}
}

func (m *Model) runRecommendedReplanAsync() {
	if m.streaming {
		m.status = "Wait for the current response to finish before replanning."
		return
	}
	if m.lastReplan == nil || m.lastReplan.ToLane == "" {
		m.status = "No replan recommendation is available."
		return
	}
	replan := *m.lastReplan
	prompt := m.lastUserPrompt()
	if strings.TrimSpace(prompt) == "" {
		m.status = "No prior user prompt is available for replanning."
		return
	}
	plan := promptExecutionPlan{
		lane:         replan.ToLane,
		thoroughness: workspace.ThoroughnessMedium,
		reason:       routeReasonForLane(replan.ToLane),
		contract:     answerContractForLane(replan.ToLane),
	}
	replanPrompt := buildReplanPrompt(prompt, replan)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(replan.Summary))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(replanPrompt, plan)
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: replanPrompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", replanPrompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setPhaseCard(replan.ToLane, "replan", phaseStateActive, "Executing the last explicit replan recommendation.")
	m.status = "Running recommended replan through " + string(replan.ToLane) + " lane."
	m.renderMessages(true)

	history := m.requestMessages()
	if strings.TrimSpace(replan.Summary) != "" {
		history = appendAnalysisSummary(history, string(replan.FromLane), replan.Summary)
	}
	opts := m.chatOptions()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runPromptPlan(ctx, replanPrompt, history, opts, plan)
		if err == nil {
			m.events <- assistantReplyMsg{text: reply}
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) lastUserPrompt() string {
	if m == nil {
		return ""
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if normalizeHistoryRole(m.messages[i].Role) == "user" && strings.TrimSpace(m.messages[i].Content) != "" {
			return strings.TrimSpace(m.messages[i].Content)
		}
	}
	return ""
}

func routeReasonForLane(lane analysisLane) promptRouteReason {
	switch lane {
	case analysisLaneLocate:
		return promptRouteReasonLocateRequest
	case analysisLaneExplore:
		return promptRouteReasonExploreRequest
	case analysisLaneReview:
		return promptRouteReasonReviewRequest
	case analysisLaneArchitecture:
		return promptRouteReasonArchitectureRequest
	case analysisLaneImplement:
		return promptRouteReasonDirectImplementation
	default:
		return promptRouteReasonDirectConversation
	}
}

func buildReplanPrompt(originalPrompt string, replan analysisReplanRecommendationState) string {
	lines := []string{
		"Execute the prior replan recommendation explicitly.",
		"Original request: " + strings.TrimSpace(originalPrompt),
		"Recommended lane: " + string(replan.ToLane),
		"Reason: " + strings.TrimSpace(replan.Reason),
	}
	if strings.TrimSpace(replan.Summary) != "" {
		lines = append(lines, "Prior evidence summary:\n"+strings.TrimSpace(replan.Summary))
	}
	lines = append(lines, "Stay within the recommended lane contract and do not apply edits unless that lane explicitly transitions to implementation.")
	return strings.Join(lines, "\n\n")
}

func (m *Model) sendPromptAsync(prompt string) {
	if looksLikeResumeStructuralRefactorPrompt(prompt) && m.canResumeStructuralRefactor() {
		m.resumeStructuralRefactorAsync(prompt)
		return
	}
	if looksLikeResumeTaskMemoryPrompt(prompt) && taskMemoryResumable(m.currentTaskMemory()) {
		m.resumeTaskMemoryAsync(prompt)
		return
	}
	plan := classifyPromptExecutionPlan(prompt, m.workspace)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))
	}
	m.resetCompactTrace()
	m.resetExecutionPanel(prompt, plan)

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.logMessage("user", prompt)
	if summary := formatPromptExecutionPlan(plan); summary != "" {
		m.logMessage("route_plan", summary)
	}
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	switch plan.lane {
	case analysisLaneReview:
		m.setPhaseCard(analysisLaneReview, "triage", phaseStateActive, "Gathering bounded evidence and ranking likely findings.")
		if routeContinuesToImplement(plan) {
			m.status = "Review agent is investigating before Qwen applies changes."
		} else {
			m.status = "Review agent is triaging the codebase."
		}
	case analysisLaneExplore:
		if routeContinuesToImplement(plan) {
			m.status = "Explore agent is investigating before Qwen applies changes."
		} else {
			m.status = "Explore agent is investigating the codebase."
		}
	case analysisLaneLocate:
		m.status = "Locate agent is identifying the requested code."
	case analysisLaneImplement:
		m.setPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Consuming the target packet and preparing a bounded edit.")
		m.status = "Implementation agent is applying your requested change."
	default:
		m.setPhaseCard("", "", "", "")
		m.status = "Qwen is working on your request."
	}
	m.renderMessages(true)

	history := m.requestMessages()
	opts := m.chatOptions()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runPromptPlan(ctx, prompt, history, opts, plan)
		if err == nil {
			m.events <- assistantReplyMsg{text: reply}
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) resumeApprovedWriteAsync(proposal writeApprovalProposal) {
	note := proposal.approvalNote()
	m.messages = append(m.messages, llama.ChatMessage{Role: "tool", Content: note})
	m.logMessage("tool", note)
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: ""})
	m.streaming = true
	m.workingFrame = 0
	m.clearRuntimeError()
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		"Scoped approval granted. Resuming the bounded implementation plan.",
		proposalPanelSteps(&proposal),
	)
	m.setPhaseCard(analysisLaneImplement, "apply", phaseStateActive, "Scoped approval granted. Resuming the bounded implementation plan.")
	m.status = "Approved guarded write scope. Implementation agent is resuming."
	m.renderMessages(true)

	history := m.requestMessages()
	opts := m.chatOptions()
	grant := proposal.grant()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Chat.RequestTimeoutSeconds)*time.Second)
		defer cancel()

		reply, err := m.runImplementAgentWithGrant(ctx, proposal.Prompt, history, opts, grant)
		if err == nil {
			m.events <- assistantReplyMsg{text: reply}
		}
		m.events <- streamDoneMsg{err: err}
	}()
}

func promptMentionedWorkspacePaths(prompt string) []string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for _, token := range strings.Fields(prompt) {
		candidate := strings.Trim(token, "`'\"()[]{}<>,:;!?")
		candidate = strings.TrimPrefix(candidate, "@")
		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "./"))
		candidate = strings.TrimRight(candidate, ".")
		if !looksLikeWorkspacePathToken(candidate) {
			continue
		}
		candidate = filepath.Clean(candidate)
		if candidate == "." {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		paths = append(paths, candidate)
	}
	return paths
}

type implementationTarget struct {
	Path string
	Line int
}

type implementationTargetPacket struct {
	SourceLane       string
	RefactorStage    int
	PrimaryPaths     []string
	Anchors          []implementationTarget
	AnalogyPaths     []string
	LiteralFragments []string
	LocationHints    []string
	SymbolHints      []string
	StrategyHints    []string
	Symbols          []semantic.SymbolMatch
}

func buildImplementationTargetPacket(prompt string, history []llama.ChatMessage) implementationTargetPacket {
	packet := implementationTargetPacket{
		PrimaryPaths:     make([]string, 0, 4),
		Anchors:          make([]implementationTarget, 0, 4),
		AnalogyPaths:     make([]string, 0, 2),
		LiteralFragments: uniqueLimitedStrings(quotedPromptFragments(prompt), 3),
		LocationHints:    implementationLocationHints(prompt),
		SymbolHints:      implementationSymbolHints(prompt),
		StrategyHints:    make([]string, 0, 3),
	}
	initialSymbolHints := append([]string(nil), packet.SymbolHints...)
	packet.SymbolHints = packet.SymbolHints[:0]
	primarySeen := make(map[string]struct{})
	analogySeen := make(map[string]struct{})
	anchorSeen := make(map[string]struct{})
	symbolSeen := make(map[string]struct{})
	addPath := func(dst *[]string, seen map[string]struct{}, path string) {
		path = normalizeGuardedPath(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		*dst = append(*dst, path)
	}
	addSymbolHint := func(hint string) {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			return
		}
		if _, ok := symbolSeen[hint]; ok {
			return
		}
		symbolSeen[hint] = struct{}{}
		packet.SymbolHints = append(packet.SymbolHints, hint)
	}
	for _, hint := range initialSymbolHints {
		addSymbolHint(hint)
	}
	for _, path := range promptMentionedWorkspacePaths(prompt) {
		addPath(&packet.PrimaryPaths, primarySeen, path)
	}
	for _, path := range implementationAnalogyPaths(prompt) {
		addPath(&packet.AnalogyPaths, analogySeen, path)
	}
	if hasUILocationHint(strings.ToLower(prompt)) {
		addPath(&packet.PrimaryPaths, primarySeen, "internal/app/app.go")
	}
	for _, target := range analysisSummaryTargets(history) {
		key := fmt.Sprintf("%s:%d", normalizeGuardedPath(target.Path), target.Line)
		if _, ok := anchorSeen[key]; ok {
			continue
		}
		anchorSeen[key] = struct{}{}
		packet.Anchors = append(packet.Anchors, target)
		addPath(&packet.PrimaryPaths, primarySeen, target.Path)
	}
	summaryLane, summaryBody, ok := latestAnalysisSummary(history)
	if ok {
		packet.SourceLane = strings.TrimSpace(summaryLane)
		packet.RefactorStage = analysisSummaryRefactorStage(summaryBody)
		for _, path := range promptMentionedWorkspacePaths(summaryBody) {
			addPath(&packet.PrimaryPaths, primarySeen, path)
		}
		for _, hint := range implementationSymbolHints(summaryBody) {
			addSymbolHint(hint)
		}
		if summaryLane == "architecture" {
			packet.StrategyHints = append(packet.StrategyHints, analysisSummaryStrategyHints(summaryBody)...)
		}
	}
	packet.PrimaryPaths = uniqueLimitedStrings(packet.PrimaryPaths, 6)
	packet.AnalogyPaths = uniqueLimitedStrings(packet.AnalogyPaths, 4)
	packet.LocationHints = uniqueLimitedStrings(packet.LocationHints, 4)
	packet.SymbolHints = uniqueLimitedStrings(packet.SymbolHints, 6)
	packet.StrategyHints = uniqueLimitedStrings(packet.StrategyHints, 4)
	return packet
}

func enrichImplementationTargetPacket(mgr *workspace.Manager, packet implementationTargetPacket) implementationTargetPacket {
	if mgr == nil || !mgr.Enabled() {
		return packet
	}
	if len(packet.SymbolHints) == 0 && len(packet.Anchors) == 0 {
		return packet
	}
	idx, err := semantic.BuildGoIndex(mgr.Root())
	if err != nil || idx == nil {
		return packet
	}
	packet.Symbols = resolveImplementationSymbolAnchors(idx, packet)
	for _, symbol := range packet.Symbols {
		if symbol.Path != "" {
			packet.PrimaryPaths = uniqueLimitedStrings(append(packet.PrimaryPaths, normalizeGuardedPath(symbol.Path)), 6)
		}
	}
	return packet
}

func implementationTargetPaths(prompt string, history []llama.ChatMessage) []string {
	seen := make(map[string]struct{})
	packet := buildImplementationTargetPacket(prompt, history)
	paths := make([]string, 0, len(packet.PrimaryPaths))
	add := func(path string) {
		path = normalizeGuardedPath(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, path := range packet.PrimaryPaths {
		add(path)
	}
	return paths
}

func implementationAnalogyPaths(prompt string) []string {
	normalized := strings.ToLower(prompt)
	paths := promptMentionedWorkspacePaths(prompt)
	analogies := make([]string, 0, 2)
	for _, path := range paths {
		lowerPath := strings.ToLower(path)
		base := strings.ToLower(filepath.Base(path))
		if containsAny(
			normalized,
			"like @"+lowerPath,
			"like "+lowerPath,
			"like @"+base,
			"like "+base,
			"similar to @"+lowerPath,
			"similar to "+lowerPath,
			"similar to @"+base,
			"similar to "+base,
			"same as @"+lowerPath,
			"same as "+lowerPath,
			"same as @"+base,
			"same as "+base,
			"based on @"+lowerPath,
			"based on "+lowerPath,
			"based on @"+base,
			"based on "+base,
			"follow @"+lowerPath,
			"follow "+lowerPath,
			"follow @"+base,
			"follow "+base,
		) {
			analogies = append(analogies, path)
		}
	}
	return uniqueLimitedStrings(analogies, 4)
}

func implementationLocationHints(prompt string) []string {
	normalized := strings.ToLower(prompt)
	hints := make([]string, 0, 4)
	for _, hint := range []string{"top", "header", "footer", "badge", "window", "chat", "menu", "status", "banner"} {
		if strings.Contains(normalized, hint) {
			hints = append(hints, hint)
		}
	}
	return hints
}

func implementationSymbolHints(prompt string) []string {
	tokenPattern := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_./]*`)
	stopwords := map[string]struct{}{
		"add": {}, "and": {}, "app": {}, "banner": {}, "bit": {}, "build": {}, "can": {}, "change": {},
		"code": {}, "edit": {}, "file": {}, "fix": {}, "for": {}, "from": {}, "header": {}, "implement": {},
		"in": {}, "it": {}, "like": {}, "make": {}, "match": {}, "padding": {}, "please": {}, "remove": {},
		"render": {}, "same": {}, "small": {}, "status": {}, "text": {}, "the": {}, "this": {}, "to": {},
		"top": {}, "underneath": {}, "update": {}, "use": {}, "where": {}, "window": {}, "with": {}, "work": {},
	}
	seen := make(map[string]struct{})
	hints := make([]string, 0, 4)
	add := func(token string) {
		token = strings.Trim(token, "`'\"()[]{}<>,:;!?")
		token = strings.TrimPrefix(token, "@")
		token = strings.TrimRight(token, ".")
		if token == "" || looksLikeWorkspacePathToken(token) || strings.Contains(token, "/") {
			return
		}
		lower := strings.ToLower(token)
		if _, skip := stopwords[lower]; skip {
			return
		}
		looksSymbolic := strings.Contains(token, ".") ||
			token != lower ||
			strings.Contains(token, "_")
		if !looksSymbolic {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		hints = append(hints, token)
	}
	for _, fragment := range quotedPromptFragments(prompt) {
		for _, token := range tokenPattern.FindAllString(fragment, -1) {
			add(token)
		}
	}
	for _, token := range tokenPattern.FindAllString(prompt, -1) {
		add(token)
	}
	return uniqueLimitedStrings(hints, 6)
}

func resolveImplementationSymbolAnchors(idx *semantic.GoIndex, packet implementationTargetPacket) []semantic.SymbolMatch {
	if idx == nil {
		return nil
	}
	preferredPaths := make(map[string]int)
	for _, path := range packet.PrimaryPaths {
		preferredPaths[normalizeGuardedPath(path)] = 2
	}
	for _, path := range packet.AnalogyPaths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if preferredPaths[path] < 1 {
			preferredPaths[path] = 1
		}
	}
	anchorLines := make(map[string][]int)
	for _, anchor := range packet.Anchors {
		path := normalizeGuardedPath(anchor.Path)
		if path == "" || anchor.Line <= 0 {
			continue
		}
		anchorLines[path] = append(anchorLines[path], anchor.Line)
	}
	seen := make(map[string]struct{})
	matches := make([]semantic.SymbolMatch, 0, 6)
	add := func(match semantic.SymbolMatch) {
		if match.Path == "" || match.Name == "" {
			return
		}
		match.Path = normalizeGuardedPath(match.Path)
		if match.Path == "" {
			return
		}
		if boost := preferredPaths[match.Path]; boost > 0 {
			match.Score += boost * 20
		}
		for _, line := range anchorLines[match.Path] {
			if line >= match.StartLine && line <= match.EndLine {
				match.Score += 24
				break
			}
		}
		key := fmt.Sprintf("%s:%s:%d", match.Path, implementationQualifiedSymbol(match), match.Line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		matches = append(matches, match)
	}
	for path, lines := range anchorLines {
		file := idx.FileByPath(path)
		if file == nil {
			continue
		}
		for _, line := range lines {
			if symbol, ok := enclosingSymbolAtLine(file, line); ok {
				add(symbolMatchFromGoSymbol(file, symbol, 80))
			}
		}
	}
	for _, hint := range packet.SymbolHints {
		for _, match := range idx.FindSymbols(hint, 6) {
			add(match)
		}
	}
	slices.SortFunc(matches, func(a, b semantic.SymbolMatch) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.StartLine != b.StartLine {
			return a.StartLine - b.StartLine
		}
		return strings.Compare(implementationQualifiedSymbol(a), implementationQualifiedSymbol(b))
	})
	if len(matches) > 4 {
		matches = matches[:4]
	}
	return matches
}

func symbolMatchFromGoSymbol(file *semantic.GoFile, symbol semantic.GoSymbol, score int) semantic.SymbolMatch {
	pkg := ""
	path := symbol.SourceFile
	if file != nil {
		pkg = file.Package
		if path == "" {
			path = file.Path
		}
	}
	return semantic.SymbolMatch{
		Path:      path,
		Package:   pkg,
		Name:      symbol.Name,
		Kind:      symbol.Kind,
		Receiver:  symbol.Receiver,
		Line:      symbol.Line,
		Column:    symbol.Column,
		StartLine: symbol.StartLine,
		EndLine:   symbol.EndLine,
		Score:     score,
	}
}

func formatPromptExecutionPlan(plan promptExecutionPlan) string {
	contract := promptPlanAnswerContract(plan)
	if plan.lane == "" && plan.followupLane == "" && plan.reason == "" && contract == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("initial=")
	b.WriteString(string(plan.lane))
	if plan.followupLane != "" {
		b.WriteString(" followup=")
		b.WriteString(string(plan.followupLane))
	}
	if plan.reason != "" {
		b.WriteString(" reason=")
		b.WriteString(string(plan.reason))
	}
	if contract != "" {
		b.WriteString(" contract=")
		b.WriteString(string(contract))
	}
	if plan.thoroughness != "" {
		b.WriteString(" thoroughness=")
		b.WriteString(string(plan.thoroughness))
	}
	return b.String()
}

func promptPlanAnswerContract(plan promptExecutionPlan) answerContract {
	if plan.contract != "" {
		return plan.contract
	}
	return answerContractForLane(plan.lane)
}

func answerContractForLane(lane analysisLane) answerContract {
	switch lane {
	case analysisLaneLocate:
		return answerContractLocateTargets
	case analysisLaneExplore:
		return answerContractGroundedExplanation
	case analysisLaneReview:
		return answerContractReviewFindings
	case analysisLaneArchitecture:
		return answerContractArchitecture
	case analysisLaneImplement:
		return answerContractImplementation
	case analysisLaneMain:
		return answerContractDirectConversation
	default:
		return ""
	}
}

func laneContractPolicyForLane(lane analysisLane) laneContractPolicy {
	policy := laneContractPolicy{Contract: answerContractForLane(lane)}
	switch lane {
	case analysisLaneLocate:
		policy.Expected = []string{"exact file path", "line number or focused snippet", "why this is the best target"}
		policy.Forbidden = []string{"repo-wide speculation", "edits"}
		policy.Fallback = "If no exact target is found, say what was searched and name the next focused search."
	case analysisLaneExplore:
		policy.Expected = []string{"major files or packages involved", "what was verified", "what is inferred", "confidence", "next validation step"}
		policy.Forbidden = []string{"edits", "ungrounded architecture advice", "claims without cited paths"}
		policy.Fallback = "If evidence is weak, return the concrete paths inspected and the narrowest next read/search."
	case analysisLaneReview:
		policy.Expected = []string{"one strongest finding", "exact file path", "why it is a real issue or cleanup", "evidence", "confidence"}
		policy.Forbidden = []string{"structure-only advice", "multiple weak findings", "edits"}
		policy.Fallback = "If no bounded finding is supported, say the evidence is inconclusive and stop before editing."
	case analysisLaneArchitecture:
		policy.Expected = []string{"current structural shape", "top structural opportunities", "exact hotspot paths", "boundary or ownership direction", "confidence"}
		policy.Forbidden = []string{"bug-triage framing", "line-specific defect requirement", "edits inside the analysis lane"}
		policy.Fallback = "If model synthesis is weak, use deterministic hotspot evidence for a grounded structural summary."
	case analysisLaneImplement:
		policy.Expected = []string{"changed files", "outcome", "verification status"}
		policy.Forbidden = []string{"broad discovery after target packet", "unbounded refactor", "rewriting unrelated files"}
		policy.Fallback = "If the target is unclear, inspect the bounded target packet before widening."
	case analysisLaneMain:
		policy.Expected = []string{"direct answer"}
		policy.Forbidden = []string{"workspace writes unless routed to implement"}
	}
	return policy
}

func renderLaneContractPolicy(lane analysisLane) string {
	policy := laneContractPolicyForLane(lane)
	if policy.Contract == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Answer contract: ")
	b.WriteString(string(policy.Contract))
	if len(policy.Expected) > 0 {
		b.WriteString("\nExpected output:")
		for _, item := range policy.Expected {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	}
	if len(policy.Forbidden) > 0 {
		b.WriteString("\nForbidden:")
		for _, item := range policy.Forbidden {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	}
	if strings.TrimSpace(policy.Fallback) != "" {
		b.WriteString("\nFallback policy:\n- ")
		b.WriteString(strings.TrimSpace(policy.Fallback))
	}
	return b.String()
}

func routeContinuesToImplement(plan promptExecutionPlan) bool {
	return plan.followupLane == analysisLaneImplement
}

func analysisTransition(plan promptExecutionPlan, completedLane analysisLane) (analysisLane, bool) {
	if completedLane == "" || plan.lane != completedLane {
		return "", false
	}
	switch completedLane {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		if routeContinuesToImplement(plan) {
			return analysisLaneImplement, true
		}
	}
	return "", false
}

func analysisReplanRecommendation(completedLane analysisLane, summary string) (analysisLane, string, bool) {
	return analysisReplanRecommendationWithEvidence(completedLane, summary, nil)
}

func analysisReplanRecommendationWithEvidence(completedLane analysisLane, summary string, evidencePaths []string) (analysisLane, string, bool) {
	paths := uniqueLimitedStrings(append(promptMentionedWorkspacePaths(summary), evidencePaths...), 12)
	if len(paths) == 0 {
		return "", "", false
	}
	switch completedLane {
	case analysisLaneReview:
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneArchitecture) && !analysisReplyAcceptedForReplan(summary, paths, analysisLaneReview) {
			return analysisLaneArchitecture, "review produced structural evidence without a bounded finding", true
		}
	case analysisLaneExplore:
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneReview) {
			return analysisLaneReview, "explore produced review-shaped evidence", true
		}
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneArchitecture) {
			return analysisLaneArchitecture, "explore produced architecture-shaped evidence", true
		}
	case analysisLaneArchitecture:
		if analysisReplyAcceptedForReplan(summary, paths, analysisLaneReview) && !analysisReplyAcceptedForReplan(summary, paths, analysisLaneArchitecture) {
			return analysisLaneReview, "architecture produced defect-shaped evidence", true
		}
	}
	return "", "", false
}

func analysisReplyAcceptedForReplan(reply string, evidencePaths []string, lane analysisLane) bool {
	if analysisReplyGrounded(reply, evidencePaths) {
		return analysisReplyAccepted(reply, evidencePaths, lane)
	}
	if len(evidencePaths) == 0 {
		return false
	}
	return analysisReplyAccepted(reply, nil, lane)
}

func appendAnalysisReplanRecommendation(summary string, target analysisLane, reason string) string {
	if strings.TrimSpace(summary) == "" || target == "" || strings.TrimSpace(reason) == "" {
		return strings.TrimSpace(summary)
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(summary))
	b.WriteString("\n\nReplan recommendation:\n")
	b.WriteString("- next lane: ")
	b.WriteString(string(target))
	b.WriteString("\n")
	b.WriteString("- reason: ")
	b.WriteString(strings.TrimSpace(reason))
	return b.String()
}

func implementationQualifiedSymbol(match semantic.SymbolMatch) string {
	if strings.TrimSpace(match.Receiver) != "" {
		return match.Receiver + "." + match.Name
	}
	return match.Name
}

func renderImplementationTargetPacket(packet implementationTargetPacket) string {
	if len(packet.PrimaryPaths) == 0 && len(packet.Anchors) == 0 && len(packet.AnalogyPaths) == 0 && len(packet.LiteralFragments) == 0 && len(packet.Symbols) == 0 && len(packet.SymbolHints) == 0 {
		return ""
	}
	lines := []string{"[implementation target packet]", "Use these anchors before any broad repo discovery."}
	if lane := strings.TrimSpace(packet.SourceLane); lane != "" {
		lines = append(lines, "- derived from "+lane+" summary")
	}
	if packet.RefactorStage > 0 {
		lines = append(lines, fmt.Sprintf("- refactor stage: %d", packet.RefactorStage))
	}
	if len(packet.PrimaryPaths) > 0 {
		lines = append(lines, "- primary files: "+strings.Join(packet.PrimaryPaths, ", "))
	}
	if len(packet.Anchors) > 0 {
		anchors := make([]string, 0, len(packet.Anchors))
		for _, anchor := range packet.Anchors {
			anchors = append(anchors, fmt.Sprintf("%s:%d", anchor.Path, anchor.Line))
		}
		lines = append(lines, "- anchored lines: "+strings.Join(uniqueLimitedStrings(anchors, 4), ", "))
	}
	if len(packet.AnalogyPaths) > 0 {
		lines = append(lines, "- analogous references: "+strings.Join(packet.AnalogyPaths, ", "))
	}
	if len(packet.Symbols) > 0 {
		symbols := make([]string, 0, len(packet.Symbols))
		for _, match := range packet.Symbols {
			symbols = append(symbols, fmt.Sprintf("%s (%s:%d-%d)", implementationQualifiedSymbol(match), match.Path, match.StartLine, match.EndLine))
		}
		lines = append(lines, "- symbol anchors: "+strings.Join(uniqueLimitedStrings(symbols, 4), ", "))
	} else if len(packet.SymbolHints) > 0 {
		lines = append(lines, "- symbol hints: "+strings.Join(packet.SymbolHints, ", "))
	}
	if len(packet.LiteralFragments) > 0 {
		quoted := make([]string, 0, len(packet.LiteralFragments))
		for _, fragment := range packet.LiteralFragments {
			fragment = strings.TrimSpace(fragment)
			if fragment == "" {
				continue
			}
			quoted = append(quoted, strconv.Quote(fragment))
		}
		if len(quoted) > 0 {
			lines = append(lines, "- quoted anchors: "+strings.Join(quoted, ", "))
		}
	}
	if len(packet.LocationHints) > 0 {
		lines = append(lines, "- location hints: "+strings.Join(packet.LocationHints, ", "))
	}
	if len(packet.StrategyHints) > 0 {
		lines = append(lines, "- structural hints: "+strings.Join(packet.StrategyHints, " | "))
	}
	if stage := structuralRefactorStageHint(packet); stage != "" {
		lines = append(lines, "- structural stage: "+stage)
	}
	lines = append(lines, "- do not call list_files/search_files until you have consumed these anchors or proved they are insufficient")
	return strings.Join(lines, "\n")
}

func analysisSummaryTargets(history []llama.ChatMessage) []implementationTarget {
	if len(history) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	targets := make([]implementationTarget, 0, 4)
	add := func(path string, line int) {
		path = normalizeGuardedPath(path)
		if path == "" || line <= 0 {
			return
		}
		key := fmt.Sprintf("%s:%d", path, line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, implementationTarget{Path: path, Line: line})
	}
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != "system" || !strings.Contains(msg.Content, "subagent summary") {
			continue
		}
		for _, field := range strings.Fields(msg.Content) {
			candidate := strings.Trim(field, "`'\"()[]{}<>,:;!?.")
			idx := strings.LastIndex(candidate, ":")
			if idx <= 0 || idx >= len(candidate)-1 {
				continue
			}
			path := candidate[:idx]
			lineText := candidate[idx+1:]
			line, err := strconv.Atoi(lineText)
			if err != nil || !looksLikeWorkspacePathToken(path) {
				continue
			}
			add(path, line)
		}
		if len(targets) > 0 {
			break
		}
	}
	return targets
}

func latestAnalysisSummary(history []llama.ChatMessage) (string, string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != "system" || !strings.Contains(msg.Content, "subagent summary]") {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		firstLine := content
		body := ""
		if idx := strings.IndexByte(content, '\n'); idx >= 0 {
			firstLine = strings.TrimSpace(content[:idx])
			body = strings.TrimSpace(content[idx+1:])
		}
		lane := ""
		if strings.HasPrefix(firstLine, "[") && strings.HasSuffix(firstLine, "]") {
			label := strings.TrimSuffix(strings.TrimPrefix(firstLine, "["), "]")
			label = strings.TrimSpace(strings.TrimSuffix(label, "subagent summary"))
			lane = strings.TrimSpace(label)
		}
		if body == "" {
			body = content
		}
		if idx := strings.Index(body, "\n\n"); idx >= 0 {
			body = strings.TrimSpace(body[idx+2:])
		}
		return lane, body, true
	}
	return "", "", false
}

func analysisSummaryStrategyHints(summary string) []string {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	lines := strings.Split(summary, "\n")
	hints := make([]string, 0, 4)
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		lower := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 180 {
			trimmed = truncateText(trimmed, 180)
		}
		if strings.Contains(lower, "direction:") ||
			strings.Contains(lower, "suggested") ||
			strings.Contains(lower, "should ") ||
			strings.Contains(lower, "split ") ||
			strings.Contains(lower, "extract ") ||
			strings.Contains(lower, "separate ") ||
			strings.Contains(lower, "move ") {
			hints = append(hints, trimmed)
		}
	}
	return uniqueLimitedStrings(hints, 4)
}

func analysisSummaryRefactorStage(summary string) int {
	for _, match := range regexp.MustCompile(`(?i)\bstage\s*:?\s*(\d+)\b`).FindAllStringSubmatch(summary, -1) {
		if len(match) != 2 {
			continue
		}
		stage, err := strconv.Atoi(match[1])
		if err == nil && stage > 0 {
			return stage
		}
	}
	return 0
}

func looksLikeWorkspacePathToken(candidate string) bool {
	if candidate == "" || strings.Contains(candidate, "://") || strings.HasPrefix(candidate, "/") {
		return false
	}
	if strings.ContainsAny(candidate, "*?") {
		return false
	}
	if strings.Contains(candidate, "/") {
		return strings.Contains(candidate, ".")
	}
	switch candidate {
	case "README.md", "go.mod", "go.sum", "config.json", "Makefile":
		return true
	}
	return strings.HasSuffix(candidate, ".go") ||
		strings.HasSuffix(candidate, ".md") ||
		strings.HasSuffix(candidate, ".json") ||
		strings.HasSuffix(candidate, ".yaml") ||
		strings.HasSuffix(candidate, ".yml") ||
		strings.HasSuffix(candidate, ".toml") ||
		strings.HasSuffix(candidate, ".txt")
}

func (m *Model) requestMessages() []llama.ChatMessage {
	var messages []llama.ChatMessage
	if prompt := strings.TrimSpace(m.systemPrompt()); prompt != "" {
		messages = append(messages, llama.ChatMessage{Role: "system", Content: prompt})
	}

	history := m.messages
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Role == "assistant" && last.Content == "" {
			history = history[:len(history)-1]
		}
	}

	start := 0
	if limit := m.cfg.Chat.MaxHistory; limit > 0 && len(history) > limit {
		start = len(history) - limit
	}
	if start > 0 {
		pinned := make([]string, 0, 8)
		pinned = append(pinned, promptMentionedWorkspacePaths(m.executionPanel.Summary)...)
		if m.taskMemory != nil {
			pinned = append(pinned, m.taskMemory.ChosenTargets...)
		}
		if stage := m.currentStructuralRefactorStage(); stage != nil {
			pinned = append(pinned, stage.FocusPath)
			pinned = append(pinned, stage.Remaining...)
		}
		memoryKind := ""
		if m.taskMemory != nil {
			memoryKind = strings.TrimSpace(m.taskMemory.Kind)
		}
		m.logContextCompaction(contextCompactionSnapshot{
			DroppedMessages:  start,
			RetainedMessages: len(history) - start,
			MaxHistory:       m.cfg.Chat.MaxHistory,
			PinnedPaths:      uniqueLimitedStrings(pinned, 12),
			PinnedMemoryKind: memoryKind,
			Strategy:         "retain_recent_history_with_pinned_task_memory_and_refactor_targets",
		})
	}
	for _, msg := range history[start:] {
		messages = append(messages, llama.ChatMessage{
			Role:    normalizeHistoryRole(msg.Role),
			Content: msg.Content,
		})
	}
	return messages
}

func (m *Model) runPromptPlan(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	plan promptExecutionPlan,
) (string, error) {
	plan = m.normalizePromptPlanThoroughness(plan)
	effort := buildEffortPolicySnapshot(plan, m.cfg.Chat.MaxToolSteps, m.cfg.Chat.RequestTimeoutSeconds, m.cfg.Chat.MaxTokens, m.cfg.LlamaServer.ReasoningBudget)
	m.logEffortPolicy(effort)
	m.logRouteAmbiguity(buildRouteAmbiguitySnapshot(prompt, plan))
	switch plan.lane {
	case analysisLaneLocate:
		summary, err := m.runLocateAgent(ctx, prompt)
		if err != nil {
			return "", err
		}
		m.logMessage("locate_summary", summary)
		if next, ok := analysisTransition(plan, analysisLaneLocate); !ok || next != analysisLaneImplement {
			m.rememberAnalysisTaskMemory(prompt, analysisLaneLocate, summary, promptMentionedWorkspacePaths(summary), "resume locate with a narrower target search before editing")
			m.applyAnalysisAnswerCritic(summary, analysisLaneLocate, promptMentionedWorkspacePaths(summary))
			return summary, nil
		}
		m.events <- infoMsg{text: "Locate agent found the target. Qwen is applying the requested change."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Locate finished. Reusing the located target for the bounded edit.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "locate", summary), opts)
	case analysisLaneArchitecture:
		m.emitPhaseCard(analysisLaneArchitecture, "mapping", phaseStateActive, "Gathering structural evidence and ranking architecture hotspots.")
		outcome, err := m.runArchitectureSubagent(ctx, prompt, opts, plan.thoroughness)
		if err != nil {
			return "", err
		}
		summary := outcome.Summary
		m.logMessage("architecture_summary", summary)
		m.emitPhaseCard(analysisLaneArchitecture, "complete", phaseStateDone, "Finished architecture synthesis from grounded hotspot evidence.")
		if next, ok := analysisTransition(plan, analysisLaneArchitecture); !ok || next != analysisLaneImplement {
			nextAction := "resume architecture validation from the cited hotspots"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneArchitecture, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("architecture -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneArchitecture, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneArchitecture, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneArchitecture, outcome.EvidencePaths)
			return summary, nil
		}
		m.events <- infoMsg{text: "Architecture agent finished. Qwen is applying changes from that structural plan."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Architecture mapping finished. Preparing the bounded implementation plan.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "architecture", summary), opts)
	case analysisLaneReview:
		m.emitPhaseCard(analysisLaneReview, "triage", phaseStateActive, "Gathering evidence and ranking candidate files.")
		outcome, err := m.runReviewSubagent(ctx, prompt, opts, plan.thoroughness)
		if err != nil {
			return "", err
		}
		summary := outcome.Summary
		m.logMessage("review_summary", summary)
		if outcome.Finding != nil {
			m.emitPhaseCard(analysisLaneReview, "complete", phaseStateDone, "Confirmed a review finding with bounded evidence.")
		} else {
			m.emitPhaseCard(analysisLaneReview, "complete", phaseStateDone, "Finished review evidence gathering without a bounded finding.")
		}
		if next, ok := analysisTransition(plan, analysisLaneReview); !ok || next != analysisLaneImplement {
			nextAction := "resume review from the confirmed candidate evidence before widening search"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneReview, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("review -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneReview, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneReview, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneReview, outcome.EvidencePaths)
			return summary, nil
		}
		if outcome.Finding == nil {
			m.events <- infoMsg{text: "Review did not confirm a bounded actionable finding. Qwen is stopping before auto-editing."}
			nextAction := "resume review with narrower evidence before any implementation"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneReview, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("review -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneReview, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneReview, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneReview, outcome.EvidencePaths)
			return summary, nil
		}
		if !reviewFindingAllowsAutoApply(*outcome.Finding) {
			m.events <- infoMsg{text: "Review found a larger-scope change. Qwen is stopping at the plan instead of auto-editing."}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneReview, summary, outcome.EvidencePaths, "resume only after scoping or approving the larger change")
			m.applyAnalysisAnswerCritic(summary, analysisLaneReview, outcome.EvidencePaths)
			return summary, nil
		}
		m.events <- infoMsg{text: "Review agent finished. Qwen is applying changes from that review."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Review finished. Preparing the bounded implementation plan.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "review", summary), opts)
	case analysisLaneExplore:
		outcome, err := m.runExploreSubagent(ctx, prompt, opts, plan.thoroughness)
		if err != nil {
			return "", err
		}
		summary := outcome.Summary
		m.logMessage("explore_summary", summary)
		if next, ok := analysisTransition(plan, analysisLaneExplore); !ok || next != analysisLaneImplement {
			nextAction := "resume exploration from the cited evidence paths before broadening search"
			if target, reason, ok := analysisReplanRecommendationWithEvidence(analysisLaneExplore, summary, outcome.EvidencePaths); ok {
				m.logMessage("replan_recommendation", fmt.Sprintf("explore -> %s: %s", target, reason))
				m.rememberAnalysisReplan(analysisLaneExplore, target, reason, summary)
				summary = appendAnalysisReplanRecommendation(summary, target, reason)
				nextAction = taskMemoryNextActionForReplan(target, reason)
			}
			m.rememberAnalysisTaskMemory(prompt, analysisLaneExplore, summary, outcome.EvidencePaths, nextAction)
			m.applyAnalysisAnswerCritic(summary, analysisLaneExplore, outcome.EvidencePaths)
			return summary, nil
		}
		m.events <- infoMsg{text: "Explore agent finished. Qwen is applying changes from that summary."}
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Explore finished. Preparing the bounded implementation plan.")
		return m.runImplementAgent(ctx, prompt, appendAnalysisSummary(history, "explore", summary), opts)
	case analysisLaneImplement:
		m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Consuming the target packet and preparing a bounded edit.")
		return m.runImplementAgent(ctx, prompt, history, opts)
	default:
		return m.runMainAgent(ctx, prompt, history, opts)
	}
}

func (m *Model) runMainAgent(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
) (string, error) {
	mainGuard := newMainWriteGuard(m.workspace, prompt)
	runner := &agent.Runner{
		Client:       m.client,
		Workspace:    m.workspace,
		MaxToolSteps: m.cfg.Chat.MaxToolSteps,
		AgentName:    "main",
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			return mainGuard.PreExecute(req)
		},
	}
	return runner.Run(
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
}

func (m *Model) runImplementAgent(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
) (string, error) {
	return m.runImplementAgentWithGrant(ctx, prompt, history, opts, nil)
}

func (m *Model) runImplementAgentWithGrant(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	grant *writeApprovalGrant,
) (string, error) {
	implementWorkspace := m.newWorkspaceManager()
	journalRecorder := newExecutionJournalRecorder(implementWorkspace, prompt)
	m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Resolving anchored files, lines, and symbols before editing.")
	targetPacket := buildImplementationTargetPacket(prompt, history)
	targetPacket = enrichImplementationTargetPacket(implementWorkspace, targetPacket)
	writeGuard := newImplementWriteGuard(implementWorkspace, prompt)
	writeGuard.grant = grant
	targetPaths := append([]string(nil), targetPacket.PrimaryPaths...)
	implementWorkspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append(append([]string(nil), targetPaths...), targetPacket.AnalogyPaths...), 8))
	bootstrap := m.bootstrapImplementationEvidence(ctx, implementWorkspace, prompt, targetPacket, history)
	if len(bootstrap.messages) > 0 {
		history = append(history, bootstrap.messages...)
		for i, result := range bootstrap.results {
			m.logToolResult(agent.ToolResultEvent{Step: -(i + 1), Agent: "implement", Result: result})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		}
	}
	if rendered := renderImplementationTargetPacket(targetPacket); rendered != "" {
		history = append(history, llama.ChatMessage{Role: "system", Content: rendered})
		m.logMessage("implement_targets", rendered)
	}
	var pendingApproval *writeApprovalProposal
	preChangedPaths := make([]string, 0, 2)
	taskPrompt := buildImplementTaskPrompt(prompt)
	finalizeImplementation := func(summary string, changedPaths []string, verify implementationVerification) string {
		refactorStage := buildStructuralRefactorStage(targetPacket, changedPaths, verify)
		reply := composeImplementationSummary(summary, changedPaths, verify, refactorStage)
		reply = m.applyFinalAnswerCritic(reply, changedPaths, verify, refactorStage)
		m.rememberExecutionJournal(journalRecorder, changedPaths, reply, refactorStage)
		if verify.failed {
			m.rememberTaskMemory(buildRepairTaskMemory(prompt, changedPaths, verify, targetPacket), "implementation verification failed with resumable repair context")
		} else {
			m.clearPersistedTaskMemory("implementation completed successfully")
		}
		return reply
	}
	handleTerminalShortcut := func(shortcut directImplementationShortcut) (string, bool) {
		if shortcut.proposal != nil {
			pendingApproval = shortcut.proposal
			m.events <- writeApprovalProposalMsg{proposal: *shortcut.proposal}
		}
		if shortcut.terminal && pendingApproval != nil {
			return pendingApproval.renderSummary(), true
		}
		return "", false
	}
	guardBlocked := func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult {
		if proposal := buildWriteApprovalProposal(ctx, implementWorkspace, prompt, req, writeGuard, blocked); proposal != nil {
			pendingApproval = proposal
			m.events <- writeApprovalProposalMsg{proposal: *proposal}
			return approvalRequiredToolResult(*proposal)
		}
		return blocked
	}
	if shortcut := m.tryDirectPromptEdit(ctx, implementWorkspace, prompt, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
		verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
		if !verify.failed {
			return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
		}
		if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
			changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
			verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
			}
			preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
			history = append(history, llama.ChatMessage{Role: "system", Content: "Direct implementation edits already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
		} else {
			preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
			history = append(history, llama.ChatMessage{Role: "system", Content: "A direct implementation shortcut already changed the workspace:\n" + shortcut.summary})
		}
		for _, result := range verify.results {
			history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
		}
		taskPrompt = buildImplementRepairPrompt(prompt)
		m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
		m.events <- infoMsg{text: "Verification failed after the direct edit. Qwen is repairing the change."}
	} else if summary, ok := handleTerminalShortcut(shortcut); ok {
		return summary, nil
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectConfigFieldUpdate(ctx, implementWorkspace, prompt, targetPacket, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct config field update already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct config field update already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct config field update. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectLocateRemoval(ctx, implementWorkspace, prompt, history, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "Direct locate-based edits already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct locate-based removal already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct locate-based edit. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectUISpacingEdit(ctx, implementWorkspace, prompt, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct UI spacing edit already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct UI spacing edit already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct UI spacing edit. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryFocusedStructuredExecutionPlan(ctx, implementWorkspace, prompt, opts, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused structured edit plan already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused structured edit plan already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the focused structured edit plan. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryFocusedWindowRewrite(ctx, implementWorkspace, prompt, opts, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused window rewrite already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused window rewrite already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the focused rewrite. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryStructuredExecutionPlan(ctx, implementWorkspace, prompt, history, opts, targetPacket, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A structured execution plan already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A structured execution plan already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the structured execution plan. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if pendingApproval != nil {
		return pendingApproval.renderSummary(), nil
	}
	history = replaceSystemPrompt(history, buildImplementSystemPrompt(m.promptStack.Text, implementWorkspace.Instructions()))
	history = append(history, llama.ChatMessage{Role: "user", Content: taskPrompt})

	requestByStep := make(map[int]agent.ToolRequestEvent)
	resultByStep := make(map[int]agent.ToolResultEvent)
	targetGuard := newImplementTargetGuard(implementWorkspace, targetPacket, bootstrap.results)
	runner := &agent.Runner{
		Client:       m.client,
		Workspace:    implementWorkspace,
		MaxToolSteps: implementToolBudget(m.cfg.Chat.MaxToolSteps),
		AgentName:    "implement",
		SeedResults:  append([]workspace.ToolResult(nil), bootstrap.results...),
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if blocked := targetGuard.PreExecute(req); blocked != nil {
				return blocked
			}
			blocked := writeGuard.PreExecute(req)
			if blocked != nil {
				result := guardBlocked(req, *blocked)
				return &result
			}
			if result := captureExecutionJournalResult(journalRecorder, req); result != nil {
				return result
			}
			return nil
		},
	}
	m.events <- infoMsg{text: "Implementation agent is applying the requested change."}
	m.emitPhaseCard(analysisLaneImplement, "apply", phaseStateActive, "Executing the bounded implementation plan against the targeted files.")
	reply, err := runner.Run(
		ctx,
		history,
		opts,
		func(status string) {
			m.events <- infoMsg{text: "Implementation agent: " + status}
		},
		func(event agent.ToolRequestEvent) {
			requestByStep[event.Step] = event
			m.logToolRequest(event)
		},
		func(event agent.ToolResultEvent) {
			resultByStep[event.Step] = event
			m.logToolResult(event)
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

	changedPaths := uniqueNonEmptyStrings(append(preChangedPaths, changedPathsFromImplementEvents(requestByStep, resultByStep)...))
	if len(changedPaths) == 0 {
		m.emitPhaseCard(analysisLaneImplement, "complete", phaseStateDone, "No workspace changes were needed after consuming the target packet.")
		return buildImplementationNoopSummary(reply, resultByStep), nil
	}

	verify := m.runImplementationVerification(ctx, implementWorkspace, len(resultByStep)+1, changedPaths...)
	return finalizeImplementation(reply, changedPaths, verify), nil
}

type directImplementationShortcut struct {
	applied      bool
	changedPaths []string
	summary      string
	nextStep     int
	terminal     bool
	proposal     *writeApprovalProposal
}

type structuredExecutionPlan struct {
	Summary    string                    `json:"summary"`
	Operations []structuredExecutionEdit `json:"operations"`
}

type structuredExecutionEdit struct {
	Tool      string `json:"tool"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Content   string `json:"content,omitempty"`
	OldText   string `json:"old_text,omitempty"`
	NewText   string `json:"new_text,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type implementationBootstrap struct {
	results  []workspace.ToolResult
	messages []llama.ChatMessage
}

type implementTargetGuard struct {
	workspace         *workspace.Manager
	packet            implementationTargetPacket
	targetConsumed    bool
	bootstrappedReads map[string]struct{}
	bootstrapWindows  map[string][2]int
}

func newImplementTargetGuard(mgr *workspace.Manager, packet implementationTargetPacket, bootstrap []workspace.ToolResult) *implementTargetGuard {
	g := &implementTargetGuard{
		workspace:         mgr,
		packet:            packet,
		bootstrappedReads: make(map[string]struct{}),
		bootstrapWindows:  make(map[string][2]int),
	}
	for _, result := range bootstrap {
		if result.Tool != "read_file" || result.Error != "" {
			continue
		}
		path, startLine, endLine, ok := parseReadWindowSummary(result.Summary)
		if !ok {
			continue
		}
		if g.pathAllowed(path) {
			g.bootstrappedReads[path] = struct{}{}
			g.bootstrapWindows[path] = [2]int{startLine, endLine}
		}
	}
	return g
}

func (g *implementTargetGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || g.targetConsumed || len(g.packet.PrimaryPaths) == 0 {
		return nil
	}
	path := normalizeGuardedPath(req.Path)
	if req.Tool == "read_file" && path != "" && req.ID == "" && req.StartLine == 0 && req.EndLine == 0 {
		if _, ok := g.bootstrappedReads[path]; ok {
			return &workspace.ToolResult{
				Tool:    "read_file",
				Summary: "Implementation bootstrap already inspected this target",
				Error:   "the target packet already includes a focused read of this file. Use edit_file, a narrower read_file window, or search_content scoped to this path instead of rereading the whole file.",
			}
		}
	}
	if g.targeted(req.Tool, path) {
		g.targetConsumed = true
		return nil
	}
	switch req.Tool {
	case "list_files", "search_files", "go_overview", "go_entrypoints", "go_workspace_symbol", "go_find_package":
		if redirected, ok := g.redirectedTargetInspection(req); ok {
			return redirected
		}
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Implementation target packet blocked broad discovery",
			Error:   "implementation already has concrete target files. Read or search those files first before broad repo discovery.",
		}
	case "search_content", "read_file", "go_find_symbol", "go_read_symbol", "go_find_type", "go_find_const", "go_definition", "go_references", "go_implementations", "go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_related_files":
		if path == "" {
			if redirected, ok := g.redirectedTargetInspection(req); ok {
				return redirected
			}
			return &workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Implementation target packet requires scoped inspection",
				Error:   "implementation already has concrete target files. Scope this inspection to a target file before widening the search.",
			}
		}
		if !g.pathAllowed(path) {
			if redirected, ok := g.redirectedTargetInspection(req); ok {
				return redirected
			}
			return &workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Implementation target packet blocked unrelated path",
				Error:   fmt.Sprintf("implementation should start with the target packet files (%s) before inspecting %s.", strings.Join(g.packet.PrimaryPaths, ", "), path),
			}
		}
		g.targetConsumed = true
		return nil
	default:
		return nil
	}
}

func (g *implementTargetGuard) redirectedTargetInspection(req workspace.ToolRequest) (*workspace.ToolResult, bool) {
	if g == nil || g.workspace == nil || len(g.packet.PrimaryPaths) == 0 {
		return nil, false
	}
	g.workspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append([]string(nil), g.packet.PrimaryPaths...), 8))
	for _, fallback := range g.redirectedTargetRequests(req) {
		var result workspace.ToolResult
		switch fallback.Tool {
		case "read_file":
			readResult, ok := g.redirectedReadFileResult(fallback)
			if !ok {
				continue
			}
			result = readResult
		default:
			result = g.workspace.Execute(context.Background(), fallback)
			if result.Error != "" {
				continue
			}
			if fallback.Tool == "search_content" && strings.TrimSpace(result.Output) == "" {
				continue
			}
		}
		g.targetConsumed = true
		if fallback.Tool == "read_file" {
			if path, startLine, endLine, ok := parseReadWindowSummary(result.Summary); ok && g.pathAllowed(path) {
				g.bootstrappedReads[path] = struct{}{}
				g.bootstrapWindows[path] = [2]int{startLine, endLine}
			}
		}
		result.Summary = fmt.Sprintf(
			"Implementation target packet redirected %s to %s on %s (%s)",
			req.Tool,
			fallback.Tool,
			normalizeGuardedPath(fallback.Path),
			strings.TrimSpace(result.Summary),
		)
		return &result, true
	}
	return nil, false
}

func (g *implementTargetGuard) redirectedReadFileResult(req workspace.ToolRequest) (workspace.ToolResult, bool) {
	path := normalizeGuardedPath(req.Path)
	if path == "" {
		return workspace.ToolResult{}, false
	}
	candidates := []string{path}
	for _, base := range []string{strings.TrimSpace(g.workspace.CWD()), strings.TrimSpace(g.workspace.Root())} {
		if base == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(base, path))
	}
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		start := req.StartLine
		end := req.EndLine
		if start <= 0 {
			start = 1
		}
		if end <= 0 || end > len(lines) {
			end = len(lines)
		}
		if start > end {
			continue
		}
		var b strings.Builder
		for i := start - 1; i < end && i < len(lines); i++ {
			fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
		}
		return workspace.ToolResult{
			Tool:    "read_file",
			Summary: fmt.Sprintf("Read %s lines %d-%d", path, start, end),
			Output:  strings.TrimRight(b.String(), "\n"),
		}, true
	}
	return workspace.ToolResult{}, false
}

func (g *implementTargetGuard) redirectedTargetRequests(req workspace.ToolRequest) []workspace.ToolRequest {
	path := g.redirectTargetPath(req.Path)
	if path == "" {
		return nil
	}
	requests := make([]workspace.ToolRequest, 0, 3)
	if pattern := strings.TrimSpace(req.Pattern); pattern != "" {
		requests = append(requests, workspace.ToolRequest{
			Tool:    "search_content",
			Path:    path,
			Pattern: pattern,
			Glob:    req.Glob,
		})
	}
	startLine, endLine := g.redirectReadWindow(path)
	requests = append(requests, workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: startLine,
		EndLine:   endLine,
	})
	return requests
}

func (g *implementTargetGuard) redirectTargetPath(requestedPath string) string {
	requestedPath = normalizeGuardedPath(requestedPath)
	if g.pathAllowed(requestedPath) {
		for _, candidate := range g.packet.PrimaryPaths {
			candidate = normalizeGuardedPath(candidate)
			if candidate == requestedPath {
				return candidate
			}
		}
	}
	for _, anchor := range g.packet.Anchors {
		candidate := normalizeGuardedPath(anchor.Path)
		if candidate != "" {
			return candidate
		}
	}
	for _, candidate := range g.packet.PrimaryPaths {
		candidate = normalizeGuardedPath(candidate)
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func (g *implementTargetGuard) redirectReadWindow(path string) (int, int) {
	path = normalizeGuardedPath(path)
	if path == "" {
		return 1, 180
	}
	if window, ok := g.bootstrapWindows[path]; ok && window[0] > 0 && window[1] >= window[0] {
		return window[0], window[1]
	}
	for _, anchor := range g.packet.Anchors {
		if normalizeGuardedPath(anchor.Path) != path || anchor.Line <= 0 {
			continue
		}
		return maxInt(1, anchor.Line-2), anchor.Line + 18
	}
	return 1, 180
}

func (g *implementTargetGuard) targeted(tool string, path string) bool {
	if path == "" {
		return false
	}
	switch tool {
	case "read_file", "search_content", "edit_file", "write_file", "append_file", "go_read_symbol", "go_extract_body":
		return g.pathAllowed(path)
	default:
		return false
	}
}

func (g *implementTargetGuard) pathAllowed(path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	candidates := append([]string(nil), g.packet.PrimaryPaths...)
	candidates = append(candidates, g.packet.AnalogyPaths...)
	for _, candidate := range candidates {
		candidate = normalizeGuardedPath(candidate)
		if candidate == "" {
			continue
		}
		if path == candidate {
			return true
		}
		dir := filepath.Dir(candidate)
		if dir != "." && strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

type locateCandidate struct {
	Path    string
	Line    int
	Match   string
	Snippet string
	Region  string
	Score   int
}

type locateOutcome struct {
	Literal    string
	Best       locateCandidate
	Alternates []locateCandidate
}

func (m *Model) bootstrapImplementationEvidence(ctx context.Context, mgr *workspace.Manager, prompt string, packet implementationTargetPacket, history []llama.ChatMessage) implementationBootstrap {
	if mgr == nil {
		return implementationBootstrap{}
	}
	paths := append([]string(nil), packet.PrimaryPaths...)
	discovered := uniqueLimitedStrings(append(append([]string(nil), packet.PrimaryPaths...), packet.AnalogyPaths...), 8)
	if len(discovered) > 0 {
		mgr.PrimeDiscoveredPaths(discovered)
	}

	bootstrap := implementationBootstrap{}
	normalized := strings.ToLower(prompt)
	appendResult := func(result workspace.ToolResult) {
		bootstrap.results = append(bootstrap.results, result)
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
	}
	ensurePrimaryPathDiscovered := func(path string) string {
		path = normalizeGuardedPath(path)
		if path == "" {
			return ""
		}
		dir := filepath.Dir(path)
		if dir == "" {
			dir = "."
		}
		search := mgr.Execute(ctx, workspace.ToolRequest{
			Tool:    "search_files",
			Pattern: filepath.Base(path),
			Path:    dir,
		})
		appendResult(search)
		if search.Error != "" {
			return ""
		}
		files, _ := parseDiscoveredPaths(search.Output)
		return strings.TrimSpace(files[path])
	}
	readDiscoveredTarget := func(path string, startLine int, endLine int) workspace.ToolResult {
		path = normalizeGuardedPath(path)
		handle := ensurePrimaryPathDiscovered(path)
		req := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: startLine,
			EndLine:   endLine,
		}
		if handle != "" {
			req.ID = handle
			req.Path = ""
		}
		result := mgr.Execute(ctx, req)
		appendResult(result)
		return result
	}
	for _, symbol := range packet.Symbols {
		if normalizeGuardedPath(symbol.Path) == "" {
			continue
		}
		start := symbol.StartLine
		end := symbol.EndLine
		if start <= 0 || end <= 0 || end < start {
			start = maxInt(1, symbol.Line-6)
			end = symbol.Line + 24
		} else {
			start = maxInt(1, start-2)
			end += 4
		}
		read := readDiscoveredTarget(symbol.Path, start, end)
		if read.Error == "" {
			break
		}
	}
	for _, target := range packet.Anchors {
		read := readDiscoveredTarget(target.Path, maxInt(1, target.Line-3), target.Line+6)
		if read.Error == "" {
			break
		}
	}
	if len(packet.LiteralFragments) > 0 {
		oldText := strings.TrimSpace(packet.LiteralFragments[0])
		if oldText != "" {
			for _, path := range paths {
				search := mgr.Execute(ctx, workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: locateLiteralSearchPattern(oldText),
					Path:    path,
				})
				appendResult(search)
				line, ok := firstSearchMatchLine(search.Output, path)
				if search.Error != "" || !ok {
					continue
				}
				readDiscoveredTarget(path, maxInt(1, line-2), line+2)
				break
			}
		}
	}
	if hasUILocationHint(normalized) {
		for _, path := range paths {
			if normalizeGuardedPath(path) != "internal/app/app.go" {
				continue
			}
			for _, pattern := range uiImplementationBootstrapPatterns(normalized) {
				search := mgr.Execute(ctx, workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: pattern,
					Path:    path,
				})
				appendResult(search)
				line, ok := firstSearchMatchLine(search.Output, path)
				if search.Error != "" || !ok {
					continue
				}
				readDiscoveredTarget(path, maxInt(1, line-1), line+18)
				return bootstrap
			}
		}
	}
	for _, path := range packet.AnalogyPaths {
		read := readDiscoveredTarget(path, 1, 180)
		if read.Error == "" {
			break
		}
	}

	for _, target := range analysisSummaryTargets(history) {
		read := readDiscoveredTarget(target.Path, maxInt(1, target.Line-2), target.Line+2)
		if read.Error == "" {
			break
		}
	}
	if len(paths) > 1 {
		seenRead := make(map[string]struct{})
		for _, result := range bootstrap.results {
			if result.Tool != "read_file" || result.Error != "" {
				continue
			}
			path, _, _, ok := parseReadWindowSummary(result.Summary)
			if ok {
				seenRead[path] = struct{}{}
			}
		}
		readCount := 0
		for _, path := range paths {
			if _, ok := seenRead[normalizeGuardedPath(path)]; ok {
				readCount++
				if readCount >= minInt(2, len(paths)) {
					break
				}
				continue
			}
			read := readDiscoveredTarget(path, 1, 180)
			if read.Error == "" {
				readCount++
			}
			if readCount >= minInt(2, len(paths)) {
				break
			}
		}
		if readCount >= minInt(2, len(paths)) {
			return bootstrap
		}
	}
	if len(bootstrap.results) == 0 {
		for _, path := range paths {
			read := readDiscoveredTarget(path, 1, 180)
			if read.Error == "" {
				break
			}
		}
	}
	return bootstrap
}

func uiImplementationBootstrapPatterns(normalized string) []string {
	patterns := make([]string, 0, 4)
	add := func(pattern string) {
		for _, existing := range patterns {
			if existing == pattern {
				return
			}
		}
		patterns = append(patterns, pattern)
	}
	if containsAny(normalized, "top", "header", "banner", "window") {
		add("header :=|header \\+=")
	}
	if containsAny(normalized, "footer", "bottom", "underneath", "below") {
		add("footer :=|footer \\+=|return lipgloss\\.JoinVertical")
	}
	if containsAny(normalized, "badge", "chat", "menu") {
		add("case \"assistant\"|assistantStyle\\.Render|content = m\\.spinner\\.View")
	}
	if len(patterns) == 0 {
		add("header :=|footer :=|return lipgloss\\.JoinVertical")
	}
	return patterns
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

func (m *Model) locatePromptTarget(ctx context.Context, mgr *workspace.Manager, prompt string, literal string) (locateOutcome, error) {
	outcome := locateOutcome{Literal: literal}
	if mgr == nil {
		return outcome, fmt.Errorf("workspace access is unavailable")
	}

	step := 1
	candidates := make([]locateCandidate, 0, 8)
	scopes := locateSearchScopes(prompt)
	literalPattern := locateLiteralSearchPattern(literal)
	for _, scope := range scopes {
		req := workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: literalPattern,
			Path:    scope,
			Glob:    "*.go",
		}
		result := m.executeDeterministicLaneTool(ctx, "locate", mgr, step, req, false, true, fmt.Sprintf("search_content:%s:%s:*.go", scope, literalPattern))
		step++
		if result.Error != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		candidates = parseLocateCandidates(result.Output)
		if len(candidates) > 0 {
			break
		}
	}
	if len(candidates) == 0 {
		seen := make(map[string]struct{})
		for _, pattern := range locateFallbackPatterns(prompt, literal) {
			for _, scope := range scopes {
				req := workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: pattern,
					Path:    scope,
					Glob:    "*.go",
				}
				result := m.executeDeterministicLaneTool(ctx, "locate", mgr, step, req, false, true, fmt.Sprintf("search_content:%s:%s:*.go", scope, pattern))
				step++
				if result.Error != "" || strings.TrimSpace(result.Output) == "" {
					continue
				}
				for _, candidate := range parseLocateCandidates(result.Output) {
					key := fmt.Sprintf("%s:%d", candidate.Path, candidate.Line)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					candidates = append(candidates, candidate)
				}
			}
		}
	}
	if len(candidates) == 0 {
		return outcome, fmt.Errorf("could not locate %q in workspace source files", literal)
	}

	for i := range candidates {
		candidates[i].Region = classifyLocateRegion(candidates[i])
		candidates[i].Score = scoreLocateCandidate(prompt, candidates[i])
	}
	sortLocateCandidates(candidates)

	readLimit := 1
	if len(candidates) < readLimit {
		readLimit = len(candidates)
	}
	for i := 0; i < readLimit; i++ {
		candidate := candidates[i]
		req := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      candidate.Path,
			StartLine: maxInt(1, candidate.Line-3),
			EndLine:   candidate.Line + 3,
		}
		result := m.executeDeterministicLaneTool(ctx, "locate", mgr, step, req, false, true, fmt.Sprintf("read_file:%s", candidate.Path))
		step++
		if result.Error != "" {
			continue
		}
		candidates[i].Snippet = strings.TrimSpace(result.Output)
		candidates[i].Region = classifyLocateRegion(candidates[i])
		candidates[i].Score = scoreLocateCandidate(prompt, candidates[i])
	}
	sortLocateCandidates(candidates)

	outcome.Best = candidates[0]
	outcome.Alternates = selectLocateAlternates(candidates)
	return outcome, nil
}

func (m *Model) executeDeterministicLaneTool(
	ctx context.Context,
	agentName string,
	mgr *workspace.Manager,
	step int,
	req workspace.ToolRequest,
	broad bool,
	narrowing bool,
	action string,
) workspace.ToolResult {
	raw := marshalToolRequest(req)
	m.logToolRequest(agent.ToolRequestEvent{
		Step:      step,
		Agent:     agentName,
		Raw:       raw,
		Request:   req,
		Mode:      mgr.Mode(),
		Broad:     broad,
		Narrowing: narrowing,
		Action:    action,
	})
	result := mgr.Execute(ctx, req)
	m.logToolResult(agent.ToolResultEvent{Step: step, Agent: agentName, Result: result})
	if shouldInvalidateExploreCacheForTool(result) {
		m.events <- exploreCacheInvalidatedMsg{
			reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
		}
	}
	m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
	return result
}

func marshalToolRequest(req workspace.ToolRequest) string {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Sprintf("{\"tool\":%q}", req.Tool)
	}
	return string(payload)
}

func primaryLocateLiteral(prompt string) string {
	for _, fragment := range quotedPromptFragments(prompt) {
		trimmed := strings.TrimSpace(fragment)
		if trimmed == "" || looksLikeWorkspacePathToken(trimmed) {
			continue
		}
		return trimmed
	}

	lower := strings.ToLower(prompt)
	for _, marker := range []string{"shows ", "showing ", "says ", "text ", "label "} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		candidate := trimLocateLiteralTail(prompt[idx+len(marker):])
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func locateLiteralSearchPattern(literal string) string {
	return regexp.QuoteMeta(strings.TrimSpace(literal))
}

func trimLocateLiteralTail(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	stopWords := map[string]struct{}{
		"and":    {},
		"but":    {},
		"please": {},
		"so":     {},
		"that":   {},
		"which":  {},
	}
	parts := make([]string, 0, 4)
	for _, field := range strings.Fields(trimmed) {
		word := strings.Trim(field, "`'\"()[]{}<>,:;!?")
		if word == "" {
			continue
		}
		if _, stop := stopWords[strings.ToLower(word)]; stop {
			break
		}
		parts = append(parts, word)
		if len(parts) >= 4 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func locateSearchScopes(prompt string) []string {
	normalized := strings.ToLower(prompt)
	scopes := make([]string, 0, 5)
	seen := make(map[string]struct{})
	add := func(scope string) {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return
		}
		if _, ok := seen[scope]; ok {
			return
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}

	if containsAny(normalized, "bubbletea", "window", "header", "footer", "badge", "chat", "menu", "status", "banner") {
		add("internal/app")
	}
	for _, path := range promptMentionedWorkspacePaths(prompt) {
		add(path)
	}
	add("internal")
	add(".")
	return scopes
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

func parseLocateCandidates(output string) []locateCandidate {
	candidates := make([]locateCandidate, 0, 8)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "context:") || strings.HasPrefix(line, "targeted edit template:") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "f") {
			continue
		}
		location := strings.SplitN(parts[1], ":", 3)
		if len(location) < 3 {
			continue
		}
		path := strings.TrimSpace(location[0])
		if looksLikeLocateMetaPath(path) {
			continue
		}
		match := strings.TrimSpace(location[2])
		if looksLikeLocateMetaMatch(match) {
			continue
		}
		lineNumber, err := strconv.Atoi(strings.TrimSpace(location[1]))
		if err != nil {
			continue
		}
		candidates = append(candidates, locateCandidate{
			Path:  path,
			Line:  lineNumber,
			Match: match,
		})
	}
	return candidates
}

func looksLikeLocateMetaPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/logs/") ||
		strings.Contains(path, "/transcripts/") ||
		strings.Contains(path, "/.git/")
}

func looksLikeLocateMetaMatch(match string) bool {
	text := strings.ToLower(strings.TrimSpace(match))
	if containsAny(text, "workingframe", "func (m *model) workingdots", "strings.repeat") {
		return true
	}
	if strings.Contains(text, "workingstyle :=") && !strings.Contains(text, "qwen is working") {
		return true
	}
	return containsAny(
		text,
		"containsany(text",
		"strings.contains(text",
		"classifylocateregion",
		"scorelocatecandidate",
		"renderlocatesummary",
		"locatecandidate",
		"score +=",
		"separate similar match",
		"the top-of-window",
	)
}

func classifyLocateRegion(candidate locateCandidate) string {
	text := strings.ToLower(candidate.Path + "\n" + candidate.Match + "\n" + candidate.Snippet)
	switch {
	case containsAny(text, "header +=", "header :=", "workingstyle.render", "titlestyle.render", "serverstate :="):
		return "header"
	case containsAny(text, "footer :=", "footer +=", "helpstyle.render", "context:", "workspace:"):
		return "footer"
	case containsAny(text, "case \"assistant\"", "assistantstyle.render", "msg.role", "label = assistantstyle.render(\"qwen\")", "content = m.spinner.view()"):
		return "chat"
	case containsAny(text, "m.status =", "response complete.", "saving transcript.", "server ready on"):
		return "status"
	default:
		return ""
	}
}

func scoreLocateCandidate(prompt string, candidate locateCandidate) int {
	normalized := strings.ToLower(prompt)
	text := strings.ToLower(candidate.Path + "\n" + candidate.Match + "\n" + candidate.Snippet)
	region := classifyLocateRegion(candidate)
	score := 0

	if strings.Contains(candidate.Path, "internal/app/") {
		score += 6
	}
	if strings.HasSuffix(candidate.Path, "app.go") {
		score += 4
	}
	if strings.Contains(text, "qwen is working") {
		score += 2
	}
	if containsAny(text, "workingframe", "func (m *model) workingdots", "strings.repeat") {
		score -= 8
	}
	if strings.Contains(text, "workingstyle :=") {
		score -= 3
	}

	switch region {
	case "header":
		score += 12
	case "chat":
		score += 4
	case "footer":
		score += 2
	case "status":
		score += 1
	}

	if containsAny(normalized, "top", "top of the window", "header", "banner", "window") {
		switch region {
		case "header":
			score += 12
		case "footer", "status", "chat":
			score -= 6
		}
	}
	if containsAny(normalized, "footer", "bottom") {
		if region == "footer" {
			score += 10
		} else if region == "header" {
			score -= 4
		}
	}
	if containsAny(normalized, "badge", "chat", "menu") {
		if region == "chat" {
			score -= 10
		}
		if region == "header" {
			score += 4
		}
	}
	if strings.Contains(normalized, "not the one") && region == "chat" {
		score -= 8
	}
	return score
}

func sortLocateCandidates(candidates []locateCandidate) {
	slices.SortFunc(candidates, func(a, b locateCandidate) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		switch {
		case a.Line < b.Line:
			return -1
		case a.Line > b.Line:
			return 1
		default:
			return 0
		}
	})
}

func selectLocateAlternates(candidates []locateCandidate) []locateCandidate {
	if len(candidates) <= 1 {
		return nil
	}
	alternates := make([]locateCandidate, 0, 2)
	seenRegions := make(map[string]struct{})
	bestRegion := candidates[0].Region
	for _, candidate := range candidates[1:] {
		if len(alternates) >= 2 {
			break
		}
		if candidate.Region == "" || candidate.Region == bestRegion {
			continue
		}
		if _, ok := seenRegions[candidate.Region]; ok {
			continue
		}
		seenRegions[candidate.Region] = struct{}{}
		alternates = append(alternates, candidate)
	}
	return alternates
}

func renderLocateSummary(prompt string, outcome locateOutcome) string {
	best := outcome.Best
	if best.Region == "" {
		best.Region = classifyLocateRegion(best)
	}

	targetLabel := fmt.Sprintf("The code rendering %q", outcome.Literal)
	if containsAny(strings.ToLower(prompt), "top", "header", "window", "banner") && best.Region == "header" {
		targetLabel = fmt.Sprintf("The top-of-window %q banner", outcome.Literal)
	}

	var b strings.Builder
	b.WriteString(renderLaneContractPolicy(analysisLaneLocate))
	b.WriteString("\n\nBest target:\n")
	fmt.Fprintf(&b, "- `%s:%d`\n", best.Path, best.Line)
	fmt.Fprintf(&b, "- %s is in `%s:%d`.", targetLabel, best.Path, best.Line)
	if best.Region == "header" {
		b.WriteString(" It is appended to the Bubble Tea header during `Model.View()`.")
	}
	if strings.TrimSpace(best.Region) != "" {
		fmt.Fprintf(&b, "\n- region: %s", best.Region)
	}
	if strings.TrimSpace(best.Snippet) != "" {
		b.WriteString("\n\nSnippet:\n")
		b.WriteString(best.Snippet)
	}

	for _, alt := range outcome.Alternates {
		switch alt.Region {
		case "chat":
			fmt.Fprintf(&b, "\n\nSeparate similar match:\n- chat placeholder: `%s:%d`", alt.Path, alt.Line)
		case "status":
			fmt.Fprintf(&b, "\n\nSeparate similar match:\n- status text assignment: `%s:%d`", alt.Path, alt.Line)
		case "footer":
			fmt.Fprintf(&b, "\n\nSeparate similar match:\n- footer/help text: `%s:%d`", alt.Path, alt.Line)
		}
	}
	return strings.TrimSpace(b.String())
}

func quotedPromptFragments(prompt string) []string {
	fragments := make([]string, 0, 4)
	for i := 0; i < len(prompt); i++ {
		quote := prompt[i]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		start := i + 1
		end := start
		for end < len(prompt) && prompt[end] != quote {
			end++
		}
		if end >= len(prompt) {
			break
		}
		fragment := strings.TrimSpace(prompt[start:end])
		if fragment != "" {
			fragments = append(fragments, fragment)
		}
		i = end
	}
	return fragments
}

func firstSearchMatchLine(output string, path string) (int, bool) {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, path+":") {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		lineText := strings.TrimSpace(parts[1])
		fields := strings.Fields(lineText)
		if len(fields) == 0 {
			continue
		}
		if lineNumber, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			return lineNumber, true
		}
	}
	return 0, false
}

func extractReadFileLine(output string, targetLine int) (string, bool) {
	if targetLine <= 0 {
		return "", false
	}
	prefix := fmt.Sprintf("%d:", targetLine)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
	}
	return "", false
}

func looksLikeSingleLineConditionalWrapper(prevLine string, targetLine string, nextLine string) bool {
	prevLine = strings.TrimSpace(prevLine)
	targetLine = strings.TrimSpace(targetLine)
	nextLine = strings.TrimSpace(nextLine)
	if prevLine == "" || targetLine == "" || nextLine == "" {
		return false
	}
	return strings.HasPrefix(prevLine, "if ") &&
		strings.HasSuffix(prevLine, "{") &&
		nextLine == "}"
}

type implementationVerification struct {
	commands         []string
	reasons          []string
	results          []workspace.ToolResult
	failed           bool
	coverage         string
	confidence       string
	riskLevel        string
	expectedFailures []string
	repairPolicy     string
}

type unusedVariableDiagnostic struct {
	Path string
	Line int
	Name string
}

var unusedVariableDiagnosticPattern = regexp.MustCompile(`^(.+\.go):([0-9]+):([0-9]+): declared and not used: ([A-Za-z_][A-Za-z0-9_]*)$`)

func (m *Model) runImplementationVerification(ctx context.Context, mgr *workspace.Manager, startStep int, changedPaths ...string) implementationVerification {
	plan := implementationVerificationPlan(mgr, changedPaths)
	if len(plan.Commands) == 0 {
		return implementationVerification{}
	}
	m.setExecutionVerificationPlan(plan)
	out := implementationVerification{
		commands:         append([]string(nil), plan.Commands...),
		reasons:          append([]string(nil), plan.Reasons...),
		results:          make([]workspace.ToolResult, 0, len(plan.Commands)),
		coverage:         strings.TrimSpace(plan.Coverage),
		confidence:       strings.TrimSpace(plan.Confidence),
		riskLevel:        strings.TrimSpace(plan.RiskLevel),
		expectedFailures: append([]string(nil), plan.ExpectedFailures...),
		repairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateActive, fmt.Sprintf("Running %d verification command(s).", len(plan.Commands)))
	for i, command := range plan.Commands {
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateActive, fmt.Sprintf("Running check %d/%d: %s", i+1, len(plan.Commands), command))
		req := workspace.ToolRequest{Tool: "shell", Command: command, Workdir: "."}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      startStep + i,
			Agent:     "implement_verify",
			Raw:       fmt.Sprintf("{\"tool\":\"shell\",\"command\":%q,\"workdir\":\".\"}", command),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "shell_narrow:" + command,
		})
		result := mgr.Execute(ctx, req)
		out.results = append(out.results, result)
		m.logToolResult(agent.ToolResultEvent{Step: startStep + i, Agent: "implement_verify", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		if result.Error != "" {
			out.failed = true
		}
	}
	if out.failed {
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateFailed, "Verification failed. Review the failing check output before trusting the edit.")
	} else {
		detail := fmt.Sprintf("Verification passed with %d successful check(s).", len(out.results))
		if out.confidence != "" {
			detail += " Confidence: " + out.confidence
			if out.coverage != "" {
				detail += " (" + out.coverage + ")."
			} else {
				detail += "."
			}
		}
		if plan.UsesFullSuite {
			detail += " Full-suite escalation was required by the computed blast radius."
		}
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateDone, detail)
	}
	m.logVerificationFeedback(verificationFeedbackFromResult(plan, out))
	return out
}

func verificationFeedbackFromResult(plan verificationPlan, verify implementationVerification) verificationFeedbackSnapshot {
	failed := make([]string, 0)
	for i, result := range verify.results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		command := ""
		if i < len(verify.commands) {
			command = verify.commands[i]
		}
		if command == "" {
			command = result.Tool
		}
		failed = append(failed, command)
	}
	recommendation := "keep current verification plan"
	if verify.failed {
		recommendation = "repair failing checks before widening scope"
	} else if plan.UsesFullSuite {
		recommendation = "record full-suite success for similar high-risk surfaces"
	} else if strings.EqualFold(plan.Confidence, "low") {
		recommendation = "consider escalating to targeted tests when similar files change again"
	}
	return verificationFeedbackSnapshot{
		RiskLevel:             strings.TrimSpace(plan.RiskLevel),
		Coverage:              strings.TrimSpace(plan.Coverage),
		Confidence:            strings.TrimSpace(plan.Confidence),
		Commands:              append([]string(nil), verify.commands...),
		FailedCommands:        uniqueNonEmptyStrings(failed),
		LearnedRecommendation: recommendation,
	}
}

func parseUnusedVariableDiagnostics(verify implementationVerification) []unusedVariableDiagnostic {
	seen := make(map[string]struct{})
	diagnostics := make([]unusedVariableDiagnostic, 0, 4)
	for _, result := range verify.results {
		for _, text := range []string{result.Output, result.Error} {
			for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				match := unusedVariableDiagnosticPattern.FindStringSubmatch(line)
				if len(match) != 5 {
					continue
				}
				lineNumber, err := strconv.Atoi(match[2])
				if err != nil || lineNumber <= 0 {
					continue
				}
				diag := unusedVariableDiagnostic{
					Path: normalizeGuardedPath(match[1]),
					Line: lineNumber,
					Name: strings.TrimSpace(match[4]),
				}
				key := fmt.Sprintf("%s:%d:%s", diag.Path, diag.Line, diag.Name)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				diagnostics = append(diagnostics, diag)
			}
		}
	}
	return diagnostics
}

func looksLikeUnusedVariableDeclaration(line string, name string) bool {
	line = strings.TrimSpace(line)
	name = strings.TrimSpace(name)
	if line == "" || name == "" {
		return false
	}
	return strings.HasPrefix(line, name+":=") ||
		strings.HasPrefix(line, name+" :=") ||
		strings.HasPrefix(line, "var "+name+" =")
}

func implementationVerifyCommands(mgr *workspace.Manager) []string {
	return implementationVerificationPlan(mgr, nil).Commands
}

func changedPathsFromImplementEvents(requests map[int]agent.ToolRequestEvent, results map[int]agent.ToolResultEvent) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for step, resultEvent := range results {
		if resultEvent.Result.Error != "" {
			continue
		}
		req, ok := requests[step]
		if !ok {
			continue
		}
		switch req.Request.Tool {
		case "edit_file", "write_file", "append_file", "mkdir":
		default:
			continue
		}
		path := strings.TrimSpace(req.Request.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func composeImplementationSummary(reply string, changedPaths []string, verify implementationVerification, refactorStage *structuralRefactorStage) string {
	sections := make([]string, 0, 4)
	if strings.TrimSpace(reply) != "" {
		sections = append(sections, strings.TrimSpace(reply))
	}
	if len(changedPaths) > 0 {
		var b strings.Builder
		b.WriteString("Changed files:\n")
		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if len(verify.results) > 0 {
		var b strings.Builder
		b.WriteString("Verification:\n")
		if verify.confidence != "" {
			b.WriteString("- confidence: ")
			b.WriteString(verify.confidence)
			if verify.coverage != "" {
				b.WriteString(" (")
				b.WriteString(verify.coverage)
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		if strings.TrimSpace(verify.riskLevel) != "" {
			b.WriteString("- risk: ")
			b.WriteString(strings.TrimSpace(verify.riskLevel))
			b.WriteString("\n")
		}
		if len(verify.expectedFailures) > 0 {
			b.WriteString("- expected failure modes: ")
			b.WriteString(strings.Join(uniqueLimitedStrings(verify.expectedFailures, 4), ", "))
			b.WriteString("\n")
		}
		if strings.TrimSpace(verify.repairPolicy) != "" {
			b.WriteString("- repair policy: ")
			b.WriteString(truncateText(strings.TrimSpace(verify.repairPolicy), 180))
			b.WriteString("\n")
		}
		for i, result := range verify.results {
			command := fmt.Sprintf("check %d", i+1)
			if i < len(verify.commands) && strings.TrimSpace(verify.commands[i]) != "" {
				command = "`" + verify.commands[i] + "`"
			}
			status := "passed"
			if result.Error != "" {
				status = "failed"
			}
			b.WriteString("- ")
			b.WriteString(command)
			b.WriteString(": ")
			b.WriteString(status)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if refactorStage != nil && strings.TrimSpace(refactorStage.NextStep) != "" {
		var b strings.Builder
		b.WriteString("Refactor stage:\n")
		b.WriteString("- stage: ")
		b.WriteString(strconv.Itoa(refactorStage.Stage))
		b.WriteString("\n")
		if strings.TrimSpace(refactorStage.FocusPath) != "" {
			b.WriteString("- focus: ")
			b.WriteString(refactorStage.FocusPath)
			b.WriteString("\n")
		}
		if len(refactorStage.Remaining) > 0 {
			b.WriteString("- remaining: ")
			b.WriteString(strings.Join(refactorStage.Remaining, ", "))
			b.WriteString("\n")
		}
		b.WriteString("- next: ")
		b.WriteString(refactorStage.NextStep)
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	return strings.Join(sections, "\n\n")
}

type finalAnswerCriticResult struct {
	Contract string   `json:"contract,omitempty"`
	Passed   bool     `json:"passed"`
	Action   string   `json:"action,omitempty"`
	Repaired bool     `json:"repaired,omitempty"`
	Findings []string `json:"findings,omitempty"`
}

func (m *Model) applyFinalAnswerCritic(answer string, changedPaths []string, verify implementationVerification, refactorStage *structuralRefactorStage) string {
	critic := critiqueImplementationFinalAnswer(answer, changedPaths, verify)
	if critic.Passed {
		critic.Action = "accept"
		m.logFinalAnswerCritic(critic)
		return answer
	}
	critic.Action = "repair"
	critic.Repaired = true
	repaired := composeImplementationSummary("", changedPaths, verify, refactorStage)
	if strings.TrimSpace(repaired) == "" {
		repaired = "Implementation completed, but the final answer critic could not reconstruct a deterministic summary."
	}
	repairedCritic := critiqueImplementationFinalAnswer(repaired, changedPaths, verify)
	if !repairedCritic.Passed {
		critic.Findings = append(critic.Findings, prefixedCriticFindings("repair", repairedCritic.Findings)...)
		critic.Repaired = false
	}
	m.logFinalAnswerCritic(critic)
	return repaired
}

func (m *Model) applyAnalysisAnswerCritic(answer string, lane analysisLane, evidencePaths []string) {
	critic := critiqueAnalysisFinalAnswer(answer, lane, evidencePaths)
	if critic.Passed {
		critic.Action = "accept"
	} else {
		critic.Action = "warn"
	}
	m.logFinalAnswerCritic(critic)
}

func critiqueAnalysisFinalAnswer(answer string, lane analysisLane, evidencePaths []string) finalAnswerCriticResult {
	result := finalAnswerCriticResult{
		Contract: string(answerContractForLane(lane)),
		Passed:   true,
	}
	addFinding := func(finding string) {
		result.Passed = false
		result.Findings = append(result.Findings, finding)
	}
	trimmed := strings.TrimSpace(answer)
	normalized := strings.ToLower(trimmed)
	if trimmed == "" {
		addFinding("final answer is empty")
		return result
	}
	if lane != analysisLaneMain && lane != analysisLaneImplement && len(evidencePaths) == 0 {
		addFinding("missing evidence paths for analysis answer")
	}
	if lane == analysisLaneLocate && len(promptMentionedWorkspacePaths(answer)) == 0 && !containsAny(normalized, "not found", "no match") {
		addFinding("locate answer lacks concrete path or explicit no-match statement")
	}
	if lane == analysisLaneReview && !containsAny(normalized, "finding", "no bounded", "no actionable", "risk", "residual") {
		addFinding("review answer lacks finding or bounded-no-finding framing")
	}
	if lane == analysisLaneArchitecture && !containsAny(normalized, "structure", "architecture", "boundary", "package", "ownership", "hotspot") {
		addFinding("architecture answer lacks structural framing")
	}
	if _, low := lowConfidenceReadinessReason(answer); low && !containsAny(normalized, "not enough evidence", "insufficient evidence", "unable to confirm", "not found", "no bounded") {
		addFinding("low-confidence answer lacks explicit uncertainty framing")
	}
	return result
}

func critiqueImplementationFinalAnswer(answer string, changedPaths []string, verify implementationVerification) finalAnswerCriticResult {
	result := finalAnswerCriticResult{
		Contract: string(answerContractImplementation),
		Passed:   true,
	}
	addFinding := func(finding string) {
		result.Passed = false
		result.Findings = append(result.Findings, finding)
	}
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if strings.TrimSpace(answer) == "" {
		addFinding("final answer is empty")
		return result
	}
	if len(changedPaths) > 0 {
		if !strings.Contains(normalized, "changed files:") {
			addFinding("missing changed files section")
		}
		for _, path := range changedPaths {
			path = strings.TrimSpace(path)
			if path != "" && !strings.Contains(normalized, strings.ToLower(path)) {
				addFinding("missing changed file path: " + path)
			}
		}
	}
	if len(verify.results) > 0 {
		if !strings.Contains(normalized, "verification:") {
			addFinding("missing verification section")
		}
		for _, command := range verify.commands {
			command = strings.TrimSpace(command)
			if command != "" && !strings.Contains(normalized, strings.ToLower(command)) {
				addFinding("missing verification command: " + command)
			}
		}
		if strings.TrimSpace(verify.riskLevel) != "" && !strings.Contains(normalized, "risk:") {
			addFinding("missing verification risk")
		}
		if verify.failed && containsAny(normalized, "verification passed", "all tests passed", "tests passed", "checks passed") {
			addFinding("overconfident verification claim after failed verification")
		}
	}
	return result
}

func prefixedCriticFindings(prefix string, findings []string) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		finding = strings.TrimSpace(finding)
		if finding != "" {
			out = append(out, prefix+": "+finding)
		}
	}
	return out
}

func (m *Model) logFinalAnswerCritic(critic finalAnswerCriticResult) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(critic)
	if err != nil {
		return
	}
	m.logMessage("final_answer_critic", string(data))
}

func buildImplementationNoopSummary(reply string, results map[int]agent.ToolResultEvent) string {
	if strings.TrimSpace(reply) != "" && !strings.Contains(strings.ToLower(reply), "recommended next step") {
		return strings.TrimSpace(reply)
	}
	var lastSummary string
	var lastError string
	steps := make([]int, 0, len(results))
	for step := range results {
		steps = append(steps, step)
	}
	slices.Sort(steps)
	for _, step := range steps {
		result := results[step].Result
		if strings.TrimSpace(result.Summary) != "" {
			lastSummary = strings.TrimSpace(result.Summary)
		}
		if strings.TrimSpace(result.Error) != "" {
			lastError = strings.TrimSpace(result.Error)
		}
	}
	if lastSummary == "" {
		lastSummary = "implementation agent stopped before making a change"
	}
	if lastError == "" {
		lastError = "no file edits were applied"
	}
	return "Implementation agent did not complete the requested edit.\n\nLast step:\n- " + lastSummary + "\n\nBlocker:\n- " + lastError
}

func (m *Model) tryDirectPromptEdit(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil {
		return directImplementationShortcut{}
	}
	paths := promptMentionedWorkspacePaths(prompt)
	quoted := quotedPromptFragments(prompt)
	if len(paths) == 0 || len(quoted) < 2 {
		return directImplementationShortcut{}
	}

	oldText := strings.TrimSpace(quoted[0])
	newText := strings.TrimSpace(quoted[1])
	if oldText == "" || newText == "" || oldText == newText {
		return directImplementationShortcut{}
	}

	step := startStep
	for _, path := range paths {
		lineNumber := firstPromptEditLine(mgr, path, oldText)
		req := workspace.ToolRequest{
			Tool:    "edit_file",
			Path:    path,
			OldText: oldText,
			NewText: newText,
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
			Raw:       fmt.Sprintf("{\"tool\":\"edit_file\",\"path\":%q,\"old_text\":%q,\"new_text\":%q}", path, oldText, newText),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "edit_file:" + path,
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
		summary := fmt.Sprintf("Applied a targeted edit in %s by replacing %q with %q.", path, oldText, newText)
		if lineNumber > 0 {
			summary = fmt.Sprintf("Applied a targeted edit in %s line %d by replacing %q with %q.", path, lineNumber, oldText, newText)
		}
		return directImplementationShortcut{
			applied:      true,
			changedPaths: []string{path},
			summary:      summary,
			nextStep:     step,
		}
	}
	return directImplementationShortcut{}
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
		if shouldInvalidateExploreCacheForTool(editResult) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", editResult.Tool),
			}
		}
		step++
		if editResult.Error != "" {
			return directImplementationShortcut{}
		}
		changedPaths = append(changedPaths, req.Path)
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

func (m *Model) tryDeterministicVerificationRepair(
	ctx context.Context,
	mgr *workspace.Manager,
	verify implementationVerification,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil {
		return directImplementationShortcut{}
	}
	diagnostics := parseUnusedVariableDiagnostics(verify)
	if len(diagnostics) == 0 {
		return directImplementationShortcut{}
	}

	step := startStep
	changedPaths := make([]string, 0, len(diagnostics))
	repairs := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		readReq := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      diag.Path,
			StartLine: diag.Line,
			EndLine:   diag.Line,
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}", diag.Path, diag.Line, diag.Line),
			Request:   readReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("read_file:%s:%d", diag.Path, diag.Line),
		})
		readResult := mgr.Execute(ctx, readReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: readResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(readResult)}
		step++
		if readResult.Error != "" {
			continue
		}
		lineText, ok := extractReadFileLine(readResult.Output, diag.Line)
		if !ok || !looksLikeUnusedVariableDeclaration(lineText, diag.Name) {
			continue
		}

		editReq := workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      diag.Path,
			StartLine: diag.Line,
			EndLine:   diag.Line,
			Content:   "",
		}
		if guard != nil {
			if blocked := guard.PreExecute(editReq); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(editReq, result)
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
			Raw:       fmt.Sprintf("{\"tool\":\"edit_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d,\"content\":\"\"}", diag.Path, diag.Line, diag.Line),
			Request:   editReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("edit_file:%s:%d-remove-unused", diag.Path, diag.Line),
		})
		if capture := captureExecutionJournalResult(journal, editReq); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			continue
		}
		editResult := mgr.Execute(ctx, editReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: editResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(editResult)}
		if shouldInvalidateExploreCacheForTool(editResult) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", editResult.Tool),
			}
		}
		step++
		if editResult.Error != "" {
			continue
		}
		changedPaths = append(changedPaths, diag.Path)
		repairs = append(repairs, fmt.Sprintf("removed unused declaration %q in %s line %d", diag.Name, diag.Path, diag.Line))
	}

	if len(changedPaths) == 0 {
		return directImplementationShortcut{}
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      "Applied deterministic verification cleanup: " + strings.Join(repairs, "; ") + ".",
		nextStep:     step,
	}
}

func looksLikeUISpacingPrompt(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(normalized, "padding", "space", "spacing", "margin") &&
		containsAny(normalized, "under", "underneath", "below", "beneath") &&
		containsAny(normalized, "banner", "header")
}

func directUISpacingReplacement(output string, startLine int, endLine int) (int, string, bool) {
	for line := startLine; line <= endLine; line++ {
		lineText, ok := extractReadFileLine(output, line)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(lineText)
		if !strings.HasPrefix(trimmed, "body :=") {
			continue
		}
		if strings.Contains(trimmed, ".MarginTop(") || strings.Contains(trimmed, ".PaddingTop(") {
			return 0, "", false
		}
		idx := strings.Index(lineText, "body :=")
		if idx < 0 {
			continue
		}
		indent := lineText[:idx]
		expr := strings.TrimSpace(strings.TrimPrefix(trimmed, "body :="))
		if expr == "" {
			continue
		}
		return line, indent + "body := lipgloss.NewStyle().MarginTop(1).Render(" + expr + ")\n", true
	}
	return 0, "", false
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

func replaceJSONFieldLine(lineText string, field string, desiredValue string) (string, bool) {
	pattern := regexp.MustCompile(`^(\s*"` + regexp.QuoteMeta(field) + `"\s*:\s*)([^,\n]+)(,?\s*)$`)
	matches := pattern.FindStringSubmatch(lineText)
	if len(matches) != 4 {
		return "", false
	}
	currentValue := strings.TrimSpace(matches[2])
	if currentValue == desiredValue {
		return "", false
	}
	return matches[1] + desiredValue + matches[3] + "\n", true
}

func (m *Model) tryStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) == 0 {
		return directImplementationShortcut{}
	}
	feedback := ""
	var plan structuredExecutionPlan
	var rawReply string
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		plan, rawReply, err = m.proposeStructuredExecutionPlan(ctx, mgr, history, opts, prompt, packet, feedback)
		if err != nil {
			if attempt == 0 {
				feedback = "Previous reply was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_execution_plan", "structured plan rejected: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if len(plan.Operations) == 0 {
			if attempt == 0 {
				feedback = "Previous reply proposed no usable operations. Return at least one valid bounded edit or an explicit empty plan only if the request cannot be safely applied."
				continue
			}
			summary := strings.TrimSpace(plan.Summary)
			if summary == "" {
				summary = "no operations proposed"
			}
			m.logMessage("implement_execution_plan", "structured plan produced no operations: "+summary)
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if err := validateStructuredExecutionPlan(packet, plan); err != nil {
			if attempt == 0 {
				feedback = "Previous proposed plan was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_execution_plan", "structured plan rejected after policy validation: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		validationErr := ""
		reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
		for _, op := range plan.Operations {
			req := workspace.ToolRequest{
				Tool:      strings.TrimSpace(op.Tool),
				Path:      normalizeGuardedPath(op.Path),
				StartLine: op.StartLine,
				EndLine:   op.EndLine,
				Content:   op.Content,
				OldText:   op.OldText,
				NewText:   op.NewText,
			}
			if blocked := validateStructuredExecutionEdit(packet, req); blocked != nil {
				validationErr = blocked.Error
				break
			}
			reqs = append(reqs, req)
		}
		if validationErr == "" {
			if err := preflightStructuredExecutionPlan(mgr, reqs); err != nil {
				validationErr = err.Error()
			}
		}
		if validationErr == "" {
			break
		}
		if attempt == 0 {
			feedback = "Previous proposed plan was rejected: " + validationErr
			continue
		}
		m.logMessage("implement_execution_plan", "structured plan rejected after preflight: "+validationErr)
		if preview := previewReplyForLog(rawReply); preview != "" {
			m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
		}
		return directImplementationShortcut{}
	}
	m.logMessage("implement_execution_plan", fmt.Sprintf("structured plan proposed %d operation(s): %s", len(plan.Operations), firstNonEmptyString(strings.TrimSpace(plan.Summary), "no summary")))
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		firstNonEmptyString(strings.TrimSpace(plan.Summary), "Prepared a bounded structured edit plan."),
		formatStructuredExecutionPlanSteps(plan),
	)
	reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		reqs = append(reqs, workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		})
	}
	if proposal := buildWriteApprovalProposalForRequests(ctx, mgr, prompt, guard, reqs); proposal != nil {
		m.logMessage("implement_execution_plan", "staged guarded write approval for structured plan scope: "+strings.Join(proposal.Paths, ", "))
		return directImplementationShortcut{
			terminal: true,
			proposal: proposal,
		}
	}
	changedPaths := make([]string, 0, len(plan.Operations))
	appliedSummaries := make([]string, 0, len(plan.Operations)+1)
	step := startStep
	for _, op := range plan.Operations {
		req := workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		}
		if blocked := validateStructuredExecutionEdit(packet, req); blocked != nil {
			return directImplementationShortcut{}
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
				return directImplementationShortcut{}
			}
		}
		raw := marshalStructuredExecutionRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       raw,
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "structured_" + toolActionForStructuredPlan(req),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			return directImplementationShortcut{}
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
			return directImplementationShortcut{}
		}
		changedPaths = append(changedPaths, req.Path)
		if strings.TrimSpace(op.Reason) != "" {
			appliedSummaries = append(appliedSummaries, fmt.Sprintf("%s (%s)", req.Path, strings.TrimSpace(op.Reason)))
		} else {
			appliedSummaries = append(appliedSummaries, req.Path)
		}
	}
	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = "Applied a structured execution plan to the targeted files."
	}
	if len(appliedSummaries) > 0 {
		summary = strings.TrimSpace(summary + " Operations: " + strings.Join(appliedSummaries, "; ") + ".")
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      summary,
		nextStep:     step,
	}
}

func (m *Model) tryFocusedWindowRewrite(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	bootstrap implementationBootstrap,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) != 1 {
		m.logMessage("implement_focused_rewrite", "focused rewrite skipped: requires exactly one primary target file")
		return directImplementationShortcut{}
	}
	path, startLine, endLine, snippet, ok := focusedRewriteWindow(packet, bootstrap)
	if !ok {
		m.logMessage("implement_focused_rewrite", "focused rewrite skipped: no suitable bootstrapped read window")
		return directImplementationShortcut{}
	}
	reply, err := m.proposeFocusedWindowRewrite(ctx, mgr, prompt, opts, packet, path, startLine, endLine, snippet, false)
	if err != nil {
		m.logMessage("implement_focused_rewrite", "focused rewrite rejected: "+err.Error())
		return directImplementationShortcut{}
	}
	content := cleanFocusedRewriteReply(reply)
	if content == "" || content == snippet {
		retryReply, retryErr := m.proposeFocusedWindowRewrite(ctx, mgr, prompt, opts, packet, path, startLine, endLine, snippet, true)
		if retryErr != nil {
			m.logMessage("implement_focused_rewrite", "focused rewrite retry rejected: "+retryErr.Error())
			return directImplementationShortcut{}
		}
		content = cleanFocusedRewriteReply(retryReply)
		if content == "" || content == snippet {
			m.logMessage("implement_focused_rewrite", "focused rewrite produced no usable change")
			return directImplementationShortcut{}
		}
	}
	req := workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Content:   content,
	}
	if err := preflightStructuredExecutionRequest(mgr, req); err != nil {
		m.logMessage("implement_focused_rewrite", "focused rewrite rejected by preflight: "+err.Error())
		return directImplementationShortcut{}
	}
	if guard != nil {
		if blocked := guard.PreExecute(req); blocked != nil {
			result := *blocked
			if onBlocked != nil {
				result = onBlocked(req, result)
			}
			m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: result})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
			if isWriteApprovalRequiredResult(result) {
				return directImplementationShortcut{terminal: true, nextStep: startStep + 1}
			}
			return directImplementationShortcut{}
		}
	}
	raw := marshalStructuredExecutionRequest(req)
	m.logMessage("implement_focused_rewrite", fmt.Sprintf("focused rewrite proposed for %s:%d-%d", path, startLine, endLine))
	m.logToolRequest(agent.ToolRequestEvent{
		Step:      startStep,
		Agent:     "implement",
		Raw:       raw,
		Request:   req,
		Mode:      workspace.ModeNormal,
		Broad:     false,
		Narrowing: true,
		Action:    "focused_window_rewrite",
	})
	if capture := captureExecutionJournalResult(journal, req); capture != nil {
		m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: *capture})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
		return directImplementationShortcut{}
	}
	result := mgr.Execute(ctx, req)
	m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: result})
	m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
	if shouldInvalidateExploreCacheForTool(result) {
		m.events <- exploreCacheInvalidatedMsg{
			reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
		}
	}
	if result.Error != "" {
		return directImplementationShortcut{}
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: []string{path},
		summary:      fmt.Sprintf("Applied a focused rewrite to %s lines %d-%d.", path, startLine, endLine),
		nextStep:     startStep + 1,
	}
}

func (m *Model) tryFocusedStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	bootstrap implementationBootstrap,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) != 1 {
		m.logMessage("implement_focused_plan", "focused structured plan skipped: requires exactly one primary target file")
		return directImplementationShortcut{}
	}
	path, windowStart, windowEnd, snippet, ok := focusedRewriteWindow(packet, bootstrap)
	if !ok {
		m.logMessage("implement_focused_plan", "focused structured plan skipped: no suitable bootstrapped read window")
		return directImplementationShortcut{}
	}
	feedback := ""
	var plan structuredExecutionPlan
	var rawReply string
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		plan, rawReply, err = m.proposeFocusedStructuredExecutionPlan(ctx, mgr, prompt, opts, packet, path, windowStart, windowEnd, snippet, feedback)
		if err != nil {
			if attempt == 0 {
				feedback = "Previous reply was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_focused_plan", "focused structured plan rejected: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if len(plan.Operations) == 0 {
			if attempt == 0 {
				feedback = "Previous reply proposed no usable operations. Return at least one valid bounded edit within the current file window."
				continue
			}
			summary := strings.TrimSpace(plan.Summary)
			if summary == "" {
				summary = "no operations proposed"
			}
			m.logMessage("implement_focused_plan", "focused structured plan produced no operations: "+summary)
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if err := validateStructuredExecutionPlan(packet, plan); err != nil {
			if attempt == 0 {
				feedback = "Previous proposed edit was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_focused_plan", "focused structured plan rejected after policy validation: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		validationErr := ""
		for _, op := range plan.Operations {
			req := workspace.ToolRequest{
				Tool:      strings.TrimSpace(op.Tool),
				Path:      normalizeGuardedPath(op.Path),
				StartLine: op.StartLine,
				EndLine:   op.EndLine,
				Content:   op.Content,
				OldText:   op.OldText,
				NewText:   op.NewText,
			}
			if blocked := validateFocusedStructuredExecutionEdit(path, windowStart, windowEnd, snippet, req); blocked != nil {
				validationErr = blocked.Error
				break
			}
			if err := preflightStructuredExecutionRequest(mgr, req); err != nil {
				validationErr = err.Error()
				break
			}
		}
		if validationErr == "" {
			break
		}
		if attempt == 0 {
			feedback = "Previous proposed edit was rejected: " + validationErr
			continue
		}
		m.logMessage("implement_focused_plan", "focused structured plan op rejected: "+validationErr)
		if preview := previewReplyForLog(rawReply); preview != "" {
			m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
		}
		return directImplementationShortcut{}
	}
	m.logMessage("implement_focused_plan", fmt.Sprintf("focused structured plan proposed %d operation(s): %s", len(plan.Operations), firstNonEmptyString(strings.TrimSpace(plan.Summary), "no summary")))
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		firstNonEmptyString(strings.TrimSpace(plan.Summary), "Prepared a focused structured edit plan."),
		formatStructuredExecutionPlanSteps(plan),
	)
	reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		reqs = append(reqs, workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		})
	}
	if proposal := buildWriteApprovalProposalForRequests(ctx, mgr, prompt, guard, reqs); proposal != nil {
		m.logMessage("implement_focused_plan", "staged guarded write approval for focused plan scope: "+strings.Join(proposal.Paths, ", "))
		return directImplementationShortcut{
			terminal: true,
			proposal: proposal,
		}
	}

	step := startStep
	changedPaths := make([]string, 0, len(plan.Operations))
	appliedSummaries := make([]string, 0, len(plan.Operations)+1)
	for _, op := range plan.Operations {
		req := workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
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
				return directImplementationShortcut{}
			}
		}
		raw := marshalStructuredExecutionRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       raw,
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "focused_structured_" + toolActionForStructuredPlan(req),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			return directImplementationShortcut{}
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
			return directImplementationShortcut{}
		}
		changedPaths = append(changedPaths, req.Path)
		if strings.TrimSpace(op.Reason) != "" {
			appliedSummaries = append(appliedSummaries, fmt.Sprintf("%s (%s)", req.Path, strings.TrimSpace(op.Reason)))
		} else {
			appliedSummaries = append(appliedSummaries, req.Path)
		}
	}
	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Applied a focused structured edit plan to %s lines %d-%d.", path, windowStart, windowEnd)
	}
	if len(appliedSummaries) > 0 {
		summary = strings.TrimSpace(summary + " Operations: " + strings.Join(appliedSummaries, "; ") + ".")
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      summary,
		nextStep:     step,
	}
}

func (m *Model) proposeStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	prompt string,
	packet implementationTargetPacket,
	feedback string,
) (structuredExecutionPlan, string, error) {
	planMessages := append([]llama.ChatMessage(nil), history...)
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	planMessages = replaceSystemPrompt(planMessages, buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions))
	planMessages = append(planMessages, llama.ChatMessage{
		Role: "system",
		Content: "You are in STRUCTURED EXECUTION PLAN mode.\n" +
			"Do not request tools. Reply with ONLY one JSON object.\n" +
			"Schema:\n" +
			"{\"summary\":\"...\",\"operations\":[{\"tool\":\"edit_file\",\"path\":\"...\",\"start_line\":123,\"end_line\":123,\"content\":\"...\",\"reason\":\"...\"}]}\n" +
			"Rules:\n" + structuredExecutionPlanRules(packet) + "\n",
	})
	planMessages = append(planMessages, llama.ChatMessage{
		Role:    "user",
		Content: "Propose a bounded structured execution plan for this request.\n\nRequest:\n" + strings.TrimSpace(prompt) + "\n\nTarget packet:\n" + renderImplementationTargetPacket(packet),
	})
	if strings.TrimSpace(feedback) != "" {
		planMessages = append(planMessages, llama.ChatMessage{
			Role:    "system",
			Content: "Previous proposal feedback:\n" + strings.TrimSpace(feedback),
		})
	}
	planOpts := opts
	planOpts.Temperature = 0.2
	planOpts.TopP = 0.9
	planOpts.PreserveThinking = false
	thinking := false
	planOpts.EnableThinking = &thinking
	reply, err := m.client.CompleteChat(ctx, planMessages, planOpts)
	if err != nil {
		return structuredExecutionPlan{}, reply, err
	}
	plan, err := parseStructuredExecutionPlan(reply)
	return plan, reply, err
}

func (m *Model) proposeFocusedStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	path string,
	windowStart int,
	windowEnd int,
	snippet string,
	feedback string,
) (structuredExecutionPlan, string, error) {
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	messages := []llama.ChatMessage{{
		Role:    "system",
		Content: buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions),
	}, {
		Role: "system",
		Content: "You are in FOCUSED STRUCTURED EDIT mode.\n" +
			"Do not request tools. Reply with ONLY one JSON object.\n" +
			"Schema:\n" +
			"{\"summary\":\"...\",\"operations\":[{\"tool\":\"edit_file\",\"path\":\"" + path + "\",\"start_line\":" + strconv.Itoa(windowStart) + ",\"end_line\":" + strconv.Itoa(windowEnd) + ",\"content\":\"...\",\"reason\":\"...\"}]}\n" +
			"Rules:\n" + focusedStructuredExecutionPlanRules(packet, path, windowStart, windowEnd) + "\n",
	}, {
		Role: "user",
		Content: "Request:\n" + strings.TrimSpace(prompt) +
			"\n\nTarget packet:\n" + renderImplementationTargetPacket(packet) +
			fmt.Sprintf("\n\nFile window %s lines %d-%d:\n%s", path, windowStart, windowEnd, snippet),
	}}
	if strings.TrimSpace(feedback) != "" {
		messages = append(messages, llama.ChatMessage{
			Role:    "system",
			Content: "Previous proposal feedback:\n" + strings.TrimSpace(feedback),
		})
	}
	planOpts := opts
	planOpts.Temperature = 0.15
	planOpts.TopP = 0.85
	planOpts.PreserveThinking = false
	thinking := false
	planOpts.EnableThinking = &thinking
	reply, err := m.client.CompleteChat(ctx, messages, planOpts)
	if err != nil {
		return structuredExecutionPlan{}, reply, err
	}
	plan, err := parseStructuredExecutionPlan(reply)
	return plan, reply, err
}

func (m *Model) proposeFocusedWindowRewrite(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	path string,
	startLine int,
	endLine int,
	snippet string,
	forceChange bool,
) (string, error) {
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	messages := []llama.ChatMessage{{
		Role:    "system",
		Content: buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions),
	}, {
		Role: "system",
		Content: "You are in FOCUSED WINDOW REWRITE mode.\n" +
			"Do not request tools.\n" +
			"Reply with ONLY the replacement code for the specified line window.\n" +
			"Do not include markdown fences, commentary, or line numbers.\n" +
			"Keep the edit minimal and preserve surrounding behavior outside the requested change.\n" +
			"Preserve indentation and tab structure exactly.\n" +
			"Make sure the rewritten window remains syntactically valid when spliced back into the file.\n" +
			"Do not drop continued expressions, trailing operators, or closing delimiters that belong to the current block.\n",
	}, {
		Role: "user",
		Content: "Request:\n" + strings.TrimSpace(prompt) +
			"\n\nTarget packet:\n" + renderImplementationTargetPacket(packet) +
			fmt.Sprintf("\n\nRewrite only this file window from %s lines %d-%d:\n%s", path, startLine, endLine, snippet),
	}}
	if forceChange {
		messages = append(messages, llama.ChatMessage{
			Role: "system",
			Content: "Your previous attempt did not make a usable edit.\n" +
				"You must return a different replacement window than the current snippet.\n" +
				"Make the smallest possible valid change that satisfies the request.\n",
		})
	}
	rewriteOpts := opts
	rewriteOpts.Temperature = 0.2
	rewriteOpts.TopP = 0.9
	rewriteOpts.PreserveThinking = false
	thinking := false
	rewriteOpts.EnableThinking = &thinking
	reply, err := m.client.CompleteChat(ctx, messages, rewriteOpts)
	if err != nil {
		return "", err
	}
	return reply, nil
}

func parseStructuredExecutionPlan(reply string) (structuredExecutionPlan, error) {
	payload := strings.TrimSpace(reply)
	if strings.HasPrefix(payload, "```") {
		start := strings.Index(payload, "\n")
		end := strings.LastIndex(payload, "```")
		if start > 0 && end > start {
			payload = strings.TrimSpace(payload[start:end])
		}
	}
	if object, ok := extractFirstJSONObjectForPlan(payload); ok {
		payload = object
	}
	var plan structuredExecutionPlan
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		return structuredExecutionPlan{}, err
	}
	if len(plan.Operations) > 3 {
		plan.Operations = nil
	}
	return plan, nil
}

func extractFirstJSONObjectForPlan(text string) (string, bool) {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	if start < 0 {
		return "", false
	}
	text = text[start:]
	depth := 0
	inString := false
	escaped := false
	for i, r := range text {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && r == '{':
			depth++
		case !inString && r == '}':
			depth--
			if depth == 0 {
				return text[:i+1], true
			}
		}
	}
	return "", false
}

func focusedRewriteWindow(packet implementationTargetPacket, bootstrap implementationBootstrap) (string, int, int, string, bool) {
	for _, result := range bootstrap.results {
		if result.Tool != "read_file" || result.Error != "" {
			continue
		}
		path, startLine, endLine, ok := parseReadWindowSummary(result.Summary)
		if !ok || !focusedRewritePathAllowed(packet, path) {
			continue
		}
		if endLine < startLine || endLine-startLine > 80 {
			continue
		}
		snippet := cleanReadWindowSnippet(result.Output)
		if strings.TrimSpace(snippet) == "" {
			continue
		}
		return path, startLine, endLine, snippet, true
	}
	return "", 0, 0, "", false
}

func focusedRewritePathAllowed(packet implementationTargetPacket, path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	for _, candidate := range packet.PrimaryPaths {
		if normalizeGuardedPath(candidate) == path {
			return true
		}
	}
	return false
}

func parseReadWindowSummary(summary string) (string, int, int, bool) {
	matches := regexp.MustCompile(`Read (?:f\d+\s+)?(.+?) lines (\d+)-(\d+)`).FindStringSubmatch(strings.TrimSpace(summary))
	if len(matches) != 4 {
		return "", 0, 0, false
	}
	startLine, err := strconv.Atoi(matches[2])
	if err != nil {
		return "", 0, 0, false
	}
	endLine, err := strconv.Atoi(matches[3])
	if err != nil {
		return "", 0, 0, false
	}
	return normalizeGuardedPath(matches[1]), startLine, endLine, true
}

func cleanReadWindowSnippet(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(parts[0])); err != nil {
			continue
		}
		cleaned = append(cleaned, parts[1])
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func cleanFocusedRewriteReply(reply string) string {
	reply = strings.TrimSpace(reply)
	if strings.HasPrefix(reply, "```") {
		start := strings.Index(reply, "\n")
		end := strings.LastIndex(reply, "```")
		if start > 0 && end > start {
			reply = strings.TrimSpace(reply[start:end])
		}
	}
	if strings.HasPrefix(reply, "tool") {
		return ""
	}
	return strings.TrimSpace(reply)
}

func previewReplyForLog(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return ""
	}
	reply = strings.ReplaceAll(reply, "\n", "\\n")
	if len(reply) > 240 {
		reply = reply[:240] + "..."
	}
	return reply
}

func preflightStructuredExecutionRequest(mgr *workspace.Manager, req workspace.ToolRequest) error {
	if mgr == nil || req.Tool != "edit_file" {
		return nil
	}
	path, err := preflightResolvePath(mgr, req.Path, req.Workdir)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := applyStructuredExecutionEdit(string(content), req)
	if err != nil {
		return err
	}
	if updated == string(content) {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, updated, parser.AllErrors); err != nil {
			return fmt.Errorf("preflight syntax validation failed for %s: %w", req.Path, err)
		}
	case ".json":
		if !json.Valid([]byte(updated)) {
			return fmt.Errorf("preflight JSON validation failed for %s", req.Path)
		}
	default:
		return nil
	}
	return nil
}

func preflightStructuredExecutionPlan(mgr *workspace.Manager, reqs []workspace.ToolRequest) error {
	if mgr == nil || len(reqs) == 0 {
		return nil
	}
	order := make([]string, 0, len(reqs))
	byPath := make(map[string][]workspace.ToolRequest)
	for _, req := range reqs {
		path, err := preflightResolvePath(mgr, req.Path, req.Workdir)
		if err != nil {
			return err
		}
		if _, ok := byPath[path]; !ok {
			order = append(order, path)
		}
		reqCopy := req
		reqCopy.Path = path
		byPath[path] = append(byPath[path], reqCopy)
	}
	for _, path := range order {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := string(content)
		for _, req := range byPath[path] {
			next, err := applyStructuredExecutionEdit(updated, req)
			if err != nil {
				return err
			}
			updated = next
		}
		if updated == string(content) {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go":
			fset := token.NewFileSet()
			if _, err := parser.ParseFile(fset, path, updated, parser.AllErrors); err != nil {
				return fmt.Errorf("preflight syntax validation failed for %s: %w", path, err)
			}
		case ".json":
			if !json.Valid([]byte(updated)) {
				return fmt.Errorf("preflight JSON validation failed for %s", path)
			}
		}
	}
	return nil
}

func validateStructuredExecutionEdit(packet implementationTargetPacket, req workspace.ToolRequest) *workspace.ToolResult {
	path := normalizeGuardedPath(req.Path)
	if path == "" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: "structured execution plan requires a concrete path"}
	}
	allowed := false
	for _, candidate := range packet.PrimaryPaths {
		if normalizeGuardedPath(candidate) == path {
			allowed = true
			break
		}
	}
	if !allowed {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: fmt.Sprintf("structured execution plan may only edit target packet primary files, not %s", path)}
	}
	switch req.Tool {
	case "edit_file":
		if strings.TrimSpace(req.OldText) == "" && (req.StartLine <= 0 || req.EndLine < req.StartLine) {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: "edit_file requires old_text or a valid start_line/end_line range"}
		}
	case "write_file", "append_file":
		if strings.TrimSpace(req.Content) == "" {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: req.Tool + " requires content"}
		}
	default:
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: fmt.Sprintf("unsupported structured execution tool %q", req.Tool)}
	}
	return nil
}

func validateStructuredExecutionPlan(packet implementationTargetPacket, plan structuredExecutionPlan) error {
	if len(plan.Operations) == 0 {
		return nil
	}
	if implementationTargetPacketNeedsStructuralPolicy(packet) {
		if len(plan.Operations) > 2 {
			return fmt.Errorf("structural refactor plans may propose at most 2 operations")
		}
		touchedPaths := make(map[string]struct{})
		for _, op := range plan.Operations {
			path := normalizeGuardedPath(op.Path)
			if path != "" {
				touchedPaths[path] = struct{}{}
			}
			if !structuredReasonLooksStructural(op.Reason, packet.StrategyHints) {
				return fmt.Errorf("structural refactor plans require an operation reason grounded in the boundary or responsibility change")
			}
		}
		if len(touchedPaths) > 1 && !structuralPlanAllowsMultiFileStage(packet, plan) {
			return fmt.Errorf("structural refactor first stage should edit one primary file before widening")
		}
	}
	return nil
}

func implementationTargetPacketNeedsStructuralPolicy(packet implementationTargetPacket) bool {
	if strings.TrimSpace(packet.SourceLane) == "architecture" {
		return true
	}
	return len(packet.StrategyHints) > 0
}

func structuredReasonLooksStructural(reason string, strategyHints []string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	if containsAny(
		reason,
		"structur",
		"boundary",
		"boundaries",
		"responsibilit",
		"split",
		"extract",
		"separate",
		"move",
		"adapter",
		"interface",
		"ownership",
		"coupling",
		"orchestration",
	) {
		return true
	}
	for _, hint := range strategyHints {
		for _, token := range strings.Fields(strings.ToLower(hint)) {
			token = strings.Trim(token, "`'\"()[]{}<>,:;!?|")
			if len(token) < 6 {
				continue
			}
			if strings.Contains(reason, token) {
				return true
			}
		}
	}
	return false
}

func structuralRefactorStageHint(packet implementationTargetPacket) string {
	if !implementationTargetPacketNeedsStructuralPolicy(packet) || len(packet.PrimaryPaths) == 0 {
		return ""
	}
	first := normalizeGuardedPath(packet.PrimaryPaths[0])
	if first == "" {
		return ""
	}
	if len(packet.PrimaryPaths) == 1 {
		return "stage 1 edits " + first + " only, then verifies before any follow-up widening"
	}
	rest := uniqueLimitedStrings(packet.PrimaryPaths[1:], 3)
	if len(rest) == 0 {
		return "stage 1 edits " + first + " only, then verifies before any follow-up widening"
	}
	return "stage 1 edits " + first + " first; verify before widening to " + strings.Join(rest, ", ")
}

func structuralPlanAllowsMultiFileStage(packet implementationTargetPacket, plan structuredExecutionPlan) bool {
	if !implementationTargetPacketNeedsStructuralPolicy(packet) {
		return true
	}
	if !structuralHintsAllowMultiFileStage(packet.StrategyHints) {
		return false
	}
	for _, op := range plan.Operations {
		if !structuredReasonAllowsMultiFileStage(op.Reason) {
			return false
		}
	}
	return true
}

func structuralHintsAllowMultiFileStage(hints []string) bool {
	for _, hint := range hints {
		normalized := strings.ToLower(hint)
		if containsAny(normalized, "interface", "adapter", "move ", "moving ", "extract ", "extraction", "cross-file", "across files", "between files") {
			return true
		}
	}
	return false
}

func structuredReasonAllowsMultiFileStage(reason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	return containsAny(normalized, "interface", "adapter", "move ", "moving ", "extract ", "extraction", "cross-file", "across files", "boundary")
}

func buildStructuralRefactorStage(packet implementationTargetPacket, changedPaths []string, verify implementationVerification) *structuralRefactorStage {
	if !implementationTargetPacketNeedsStructuralPolicy(packet) {
		return nil
	}
	changed := uniqueNormalizedPaths(changedPaths)
	if len(changed) == 0 {
		return nil
	}
	primary := uniqueNormalizedPaths(packet.PrimaryPaths)
	changedSet := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		changedSet[path] = struct{}{}
	}
	remaining := make([]string, 0, len(primary))
	for _, path := range primary {
		if _, ok := changedSet[path]; ok {
			continue
		}
		remaining = append(remaining, path)
	}
	focus := changed[0]
	if len(primary) > 0 {
		if _, ok := changedSet[primary[0]]; ok {
			focus = primary[0]
		}
	}
	coverage := strings.TrimSpace(verify.coverage)
	confidence := strings.TrimSpace(verify.confidence)
	verification := ""
	switch {
	case verify.failed:
		verification = "failed"
	case confidence != "" && coverage != "":
		verification = confidence + " (" + coverage + ")"
	case confidence != "":
		verification = confidence
	case coverage != "":
		verification = coverage
	}
	next := structuralRefactorNextStep(focus, remaining, packet.StrategyHints, verify)
	stageNumber := packet.RefactorStage
	if stageNumber <= 0 {
		stageNumber = 1
	}
	return &structuralRefactorStage{
		Active:       true,
		Stage:        stageNumber,
		SourceLane:   strings.TrimSpace(packet.SourceLane),
		FocusPath:    focus,
		Changed:      changed,
		Remaining:    remaining,
		Strategy:     append([]string(nil), packet.StrategyHints...),
		NextStep:     next,
		Verification: verification,
	}
}

func structuralRefactorNextStep(focus string, remaining []string, strategyHints []string, verify implementationVerification) string {
	if verify.failed {
		return "repair or roll back the current structural stage before widening the refactor"
	}
	if len(remaining) > 0 {
		return "review verification, then continue the structural refactor with " + remaining[0]
	}
	if len(strategyHints) > 0 {
		return "review verification, then decide whether the structural hint is complete: " + truncateText(strategyHints[0], 120)
	}
	if strings.TrimSpace(focus) != "" {
		return "review verification, then decide whether another bounded stage is needed after " + focus
	}
	return "review verification, then decide whether another bounded structural stage is needed"
}

func buildStructuralRefactorResumePrompt(stage *structuralRefactorStage, userPrompt string) string {
	if stage == nil {
		return ""
	}
	target := ""
	if len(stage.Remaining) > 0 {
		target = normalizeGuardedPath(stage.Remaining[0])
	}
	nextStage := stage.Stage + 1
	lines := []string{
		fmt.Sprintf("Implement structural refactor stage %d as a bounded follow-up.", nextStage),
	}
	if strings.TrimSpace(userPrompt) != "" {
		lines = append(lines, "User resume request: "+strings.TrimSpace(userPrompt))
	}
	if strings.TrimSpace(stage.FocusPath) != "" {
		lines = append(lines, "Previous stage focused on @"+stage.FocusPath+".")
	}
	if len(stage.Changed) > 0 {
		lines = append(lines, "Previous changed files: "+strings.Join(stage.Changed, ", ")+".")
	}
	if target != "" {
		lines = append(lines, "Next bounded target: @"+target+".")
	}
	if len(stage.Strategy) > 0 {
		lines = append(lines, "Structural strategy: "+strings.Join(stage.Strategy, " | "))
	}
	lines = append(lines,
		"Rules:",
		"- Treat this as one bounded resume stage, not the whole refactor.",
		"- Edit the next bounded target first.",
		"- Do not widen beyond the target packet unless verification proves a necessary follow-up.",
	)
	return strings.Join(lines, "\n")
}

func buildStructuralRefactorResumeAnalysisSummary(stage *structuralRefactorStage) string {
	if stage == nil {
		return ""
	}
	nextStage := stage.Stage + 1
	target := ""
	if len(stage.Remaining) > 0 {
		target = normalizeGuardedPath(stage.Remaining[0])
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Refactor stage: %d\n", nextStage))
	if target != "" {
		b.WriteString("- ")
		b.WriteString(target)
		b.WriteString(" should receive the next bounded structural refactor stage.\n")
	}
	if len(stage.Changed) > 0 {
		b.WriteString("- Previous stage changed: ")
		b.WriteString(strings.Join(stage.Changed, ", "))
		b.WriteString(".\n")
	}
	if len(stage.Strategy) > 0 {
		b.WriteString("- Suggested direction: ")
		b.WriteString(strings.Join(stage.Strategy, " | "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(stage.NextStep) != "" {
		b.WriteString("- Next step: ")
		b.WriteString(stage.NextStep)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func validateFocusedStructuredExecutionEdit(path string, windowStart int, windowEnd int, snippet string, req workspace.ToolRequest) *workspace.ToolResult {
	path = normalizeGuardedPath(path)
	if path == "" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit requires a concrete path"}
	}
	req.Path = normalizeGuardedPath(req.Path)
	if req.Path != path {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit may only target %s, not %s", path, req.Path)}
	}
	if req.Tool != "edit_file" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit only supports edit_file, not %q", req.Tool)}
	}
	if strings.TrimSpace(req.OldText) != "" {
		if !strings.Contains(snippet, req.OldText) {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit old_text must appear in the bootstrapped window"}
		}
		if req.OldText == req.NewText {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit old_text/new_text pair is a no-op"}
		}
		return nil
	}
	if req.StartLine <= 0 || req.EndLine < req.StartLine {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit requires old_text/new_text or a valid start_line/end_line range"}
	}
	if req.StartLine < windowStart || req.EndLine > windowEnd {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit must stay within lines %d-%d", windowStart, windowEnd)}
	}
	if req.StartLine == windowStart && req.EndLine == windowEnd && req.Content == snippet {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit line-range content is identical to the current window"}
	}
	return nil
}

func marshalStructuredExecutionRequest(req workspace.ToolRequest) string {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Sprintf("{\"tool\":%q,\"path\":%q}", req.Tool, req.Path)
	}
	return string(payload)
}

func preflightResolvePath(mgr *workspace.Manager, path string, workdir string) (string, error) {
	base := mgr.CWD()
	if strings.TrimSpace(workdir) != "" {
		if filepath.IsAbs(workdir) {
			base = workdir
		} else {
			base = filepath.Join(base, workdir)
		}
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Clean(filepath.Join(base, path)), nil
}

func applyStructuredExecutionEdit(current string, req workspace.ToolRequest) (string, error) {
	if req.StartLine > 0 || req.EndLine > 0 {
		startLine := req.StartLine
		endLine := req.EndLine
		if startLine <= 0 {
			return "", fmt.Errorf("start_line must be greater than 0")
		}
		if endLine == 0 {
			endLine = startLine
		}
		if endLine < startLine {
			return "", fmt.Errorf("end_line must be greater than or equal to start_line")
		}
		spans := preflightLineSpans(current)
		if len(spans) == 0 {
			return "", fmt.Errorf("cannot apply a line edit to an empty file")
		}
		if startLine > len(spans) || endLine > len(spans) {
			return "", fmt.Errorf("requested lines %d-%d are outside the file (file has %d lines)", startLine, endLine, len(spans))
		}
		startOffset := spans[startLine-1].start
		endOffset := spans[endLine-1].end
		return current[:startOffset] + req.Content + current[endOffset:], nil
	}
	if strings.TrimSpace(req.OldText) == "" {
		return "", fmt.Errorf("old_text is required unless you provide start_line/end_line/content")
	}
	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		return "", fmt.Errorf("old_text did not appear in %s", req.Path)
	}
	if matchCount > 1 && !req.ReplaceAll {
		return "", fmt.Errorf("old_text matched %d locations in %s; provide a more specific old_text or set replace_all=true", matchCount, req.Path)
	}
	if req.ReplaceAll {
		return strings.ReplaceAll(current, req.OldText, req.NewText), nil
	}
	return strings.Replace(current, req.OldText, req.NewText, 1), nil
}

type preflightLineSpan struct {
	start int
	end   int
}

func preflightLineSpans(text string) []preflightLineSpan {
	if text == "" {
		return nil
	}
	spans := make([]preflightLineSpan, 0, strings.Count(text, "\n")+1)
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		spans = append(spans, preflightLineSpan{start: start, end: i + 1})
		start = i + 1
	}
	if start <= len(text) {
		spans = append(spans, preflightLineSpan{start: start, end: len(text)})
	}
	return spans
}

func toolActionForStructuredPlan(req workspace.ToolRequest) string {
	switch req.Tool {
	case "edit_file":
		return "edit_file:" + strings.TrimSpace(req.Path)
	case "write_file":
		return "write_file:" + strings.TrimSpace(req.Path)
	case "append_file":
		return "append_file:" + strings.TrimSpace(req.Path)
	default:
		return req.Tool
	}
}

type implementWriteGuard struct {
	mgr           *workspace.Manager
	policy        workspace.WritePolicy
	normalized    string
	shellOnly     bool
	explicitPaths map[string]struct{}
	writePaths    map[string]struct{}
	grant         *writeApprovalGrant
}

type mainWriteGuard struct {
	policy     workspace.WritePolicy
	normalized string
}

func newMainWriteGuard(mgr *workspace.Manager, prompt string) *mainWriteGuard {
	if mgr == nil {
		return nil
	}
	return &mainWriteGuard{
		policy:     mgr.WritePolicy(),
		normalized: strings.ToLower(prompt),
	}
}

func (g *mainWriteGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || g.policy == workspace.WritePolicyAuto {
		return nil
	}
	switch req.Tool {
	case "edit_file", "write_file", "append_file", "mkdir":
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Guarded write policy blocked main-lane mutation",
			Error:   "guarded writes keep the main lane read-first. Use an explicit implementation prompt so Qwen routes through the guarded implement lane, or switch to /writes auto for broader mutation.",
		}
	case "shell":
		if isGuardedImplementShellAllowed(req.Command) {
			return nil
		}
		reason := "guarded writes only allow read-only inspection and verification shell commands in the main lane. Use an explicit implementation prompt so Qwen routes through the guarded implement lane, or switch to /writes auto."
		if requestsShellOnlyMutation(g.normalized) {
			reason = "guarded writes do not permit shell-only mutations. Relax the request so Qwen can use file tools safely, or switch to /writes auto."
		}
		return &workspace.ToolResult{
			Tool:    "shell",
			Summary: "Guarded write policy blocked main-lane shell mutation",
			Error:   reason,
		}
	default:
		return nil
	}
}

func newImplementWriteGuard(mgr *workspace.Manager, prompt string) *implementWriteGuard {
	explicitPaths := make(map[string]struct{})
	for _, path := range promptMentionedWorkspacePaths(prompt) {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		explicitPaths[path] = struct{}{}
		explicitPaths[filepath.Base(path)] = struct{}{}
	}
	return &implementWriteGuard{
		mgr:           mgr,
		policy:        mgr.WritePolicy(),
		normalized:    strings.ToLower(prompt),
		shellOnly:     requestsShellOnlyMutation(strings.ToLower(prompt)),
		explicitPaths: explicitPaths,
		writePaths:    make(map[string]struct{}),
	}
}

func (g *implementWriteGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || g.policy == workspace.WritePolicyAuto {
		return nil
	}
	path := normalizeGuardedPath(req.Path)
	switch req.Tool {
	case "edit_file":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "edit_file",
				Summary: "Guarded write policy blocked shell-only edit",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with file edits. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		return nil
	case "write_file":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "write_file",
				Summary: "Guarded write policy blocked shell-only file creation",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with write_file. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		if g.fileExists(path) && !g.explicitRewriteRequested(path) {
			return &workspace.ToolResult{
				Tool:    "write_file",
				Summary: "Guarded write policy blocked rewrite",
				Error:   "guarded writes do not allow write_file on an existing file unless the prompt explicitly requests a full rewrite. Use edit_file for targeted edits or switch to /writes auto.",
			}
		}
		if !g.fileExists(path) && !g.explicitCreateRequested(path) {
			return &workspace.ToolResult{
				Tool:    "write_file",
				Summary: "Guarded write policy blocked file creation",
				Error:   "guarded writes only allow creating a new file when the prompt clearly requests creating that file. Ask explicitly to create it or switch to /writes auto.",
			}
		}
		return nil
	case "append_file":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "append_file",
				Summary: "Guarded write policy blocked shell-only append",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with append_file. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		if !g.explicitAppendRequested(path) {
			return &workspace.ToolResult{
				Tool:    "append_file",
				Summary: "Guarded write policy blocked append",
				Error:   "guarded writes only allow append_file when the prompt explicitly asks to append content. Use edit_file for precise changes or switch to /writes auto.",
			}
		}
		return nil
	case "mkdir":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "mkdir",
				Summary: "Guarded write policy blocked shell-only directory creation",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with mkdir. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		if !g.explicitCreateRequested(path) {
			return &workspace.ToolResult{
				Tool:    "mkdir",
				Summary: "Guarded write policy blocked directory creation",
				Error:   "guarded writes only allow mkdir when the prompt explicitly asks to create a directory. Ask explicitly or switch to /writes auto.",
			}
		}
		return nil
	case "shell":
		if isGuardedImplementShellAllowed(req.Command) {
			return nil
		}
		return &workspace.ToolResult{
			Tool:    "shell",
			Summary: "Guarded write policy blocked shell command",
			Error:   "guarded writes only allow read-only inspection and verification shell commands. Use file tools for edits, or switch to /writes auto for broader shell execution.",
		}
	default:
		return nil
	}
}

func (g *implementWriteGuard) guardPathScope(tool string, path string) *workspace.ToolResult {
	if path == "" {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Guarded write policy blocked request",
			Error:   "guarded writes require a concrete path for file changes.",
		}
	}
	if g.approvalGranted(tool, path) {
		g.writePaths[path] = struct{}{}
		return nil
	}
	if g.isProtectedPath(path) {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Guarded write policy blocked protected path",
			Error:   fmt.Sprintf("%s is a protected path under guarded writes. Approve the scoped proposal, or switch to /writes auto for broader autonomy.", path),
		}
	}
	if _, ok := g.writePaths[path]; ok {
		return nil
	}
	if len(g.writePaths) >= 3 && !g.explicitMultiFileRequested() {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Guarded write policy blocked broad write scope",
			Error:   "guarded writes stop after a small write set. For broader multi-file changes, ask explicitly for a multi-file refactor or switch to /writes auto.",
		}
	}
	g.writePaths[path] = struct{}{}
	return nil
}

func (g *implementWriteGuard) approvalGranted(tool string, path string) bool {
	if g == nil || g.grant == nil {
		return false
	}
	if strings.TrimSpace(g.grant.Tool) != "" && strings.TrimSpace(g.grant.Tool) != strings.TrimSpace(tool) {
		return false
	}
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	_, ok := g.grant.Paths[path]
	return ok
}

func (g *implementWriteGuard) fileExists(path string) bool {
	if g == nil || g.mgr == nil || path == "" {
		return false
	}
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(g.mgr.Root(), path)
	}
	info, err := os.Stat(fullPath)
	return err == nil && !info.IsDir()
}

func (g *implementWriteGuard) pathExplicitlyAuthorized(path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	if _, ok := g.explicitPaths[path]; ok {
		return true
	}
	if _, ok := g.explicitPaths[filepath.Base(path)]; ok {
		return true
	}
	return strings.Contains(g.normalized, strings.ToLower(path))
}

func (g *implementWriteGuard) explicitCreateRequested(path string) bool {
	if g == nil {
		return false
	}
	if !containsAny(g.normalized, "create", "new file", "add file", "add a file", "make a file", "create directory", "new directory", "mkdir") {
		return false
	}
	return path == "" || g.pathExplicitlyAuthorized(path) || !g.fileExists(path)
}

func (g *implementWriteGuard) explicitRewriteRequested(path string) bool {
	if g == nil {
		return false
	}
	if !containsAny(g.normalized, "rewrite", "full rewrite", "rewrite the file", "replace the entire", "replace the whole file", "overwrite") {
		return false
	}
	return path == "" || g.pathExplicitlyAuthorized(path)
}

func (g *implementWriteGuard) explicitAppendRequested(path string) bool {
	if g == nil {
		return false
	}
	if !containsAny(g.normalized, "append", "append to", "add to the end") {
		return false
	}
	return path == "" || g.pathExplicitlyAuthorized(path)
}

func (g *implementWriteGuard) explicitMultiFileRequested() bool {
	if g == nil {
		return false
	}
	return containsAny(g.normalized, "multiple files", "several files", "across files", "multi-file", "broad refactor", "whole repo", "codebase")
}

func (g *implementWriteGuard) isProtectedPath(path string) bool {
	path = normalizeGuardedPath(path)
	switch {
	case path == "", path == ".":
		return false
	case path == "config.json", path == "config.example.json":
		return true
	case strings.HasPrefix(path, "prompts/"), strings.HasPrefix(path, "scripts/"):
		return true
	case strings.HasPrefix(path, ".git/"), strings.HasPrefix(path, "logs/"), strings.HasPrefix(path, "models/"), strings.HasPrefix(path, "bin/"):
		return true
	default:
		return false
	}
}

func normalizeGuardedPath(path string) string {
	path = strings.TrimSpace(strings.TrimPrefix(path, "./"))
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func buildWriteApprovalProposal(ctx context.Context, mgr *workspace.Manager, prompt string, req workspace.ToolRequest, guard *implementWriteGuard, blocked workspace.ToolResult) *writeApprovalProposal {
	path := normalizeGuardedPath(req.Path)
	var proposal *writeApprovalProposal
	switch strings.TrimSpace(blocked.Summary) {
	case "Guarded write policy blocked protected path":
		if path == "" {
			return nil
		}
		proposal = &writeApprovalProposal{
			Prompt: prompt,
			Tool:   req.Tool,
			Kind:   writeApprovalProtectedPath,
			Paths:  []string{path},
			Reason: blocked.Error,
		}
	case "Guarded write policy blocked broad write scope":
		paths := make([]string, 0, len(guard.writePaths)+1)
		seen := make(map[string]struct{}, len(guard.writePaths)+1)
		for existing := range guard.writePaths {
			existing = normalizeGuardedPath(existing)
			if existing == "" {
				continue
			}
			if _, ok := seen[existing]; ok {
				continue
			}
			seen[existing] = struct{}{}
			paths = append(paths, existing)
		}
		if path != "" {
			if _, ok := seen[path]; !ok {
				paths = append(paths, path)
			}
		}
		slices.Sort(paths)
		if len(paths) == 0 {
			return nil
		}
		proposal = &writeApprovalProposal{
			Prompt: prompt,
			Tool:   req.Tool,
			Kind:   writeApprovalBroadScope,
			Paths:  paths,
			Reason: blocked.Error,
		}
	default:
		return nil
	}
	proposal.PlannedEdits = summarizeWriteApprovalEdits(mgr, []workspace.ToolRequest{req})
	proposal.PreviewHunks = previewWriteApprovalEdits(mgr, []workspace.ToolRequest{req})
	enrichWriteApprovalProposal(ctx, mgr, proposal, req)
	return proposal
}

func buildWriteApprovalProposalForPaths(ctx context.Context, mgr *workspace.Manager, prompt string, tool string, paths []string, guard *implementWriteGuard) *writeApprovalProposal {
	if guard == nil || guard.policy == workspace.WritePolicyAuto {
		return nil
	}
	normalizedPaths := uniqueNonEmptyStrings(paths)
	if len(normalizedPaths) == 0 {
		return nil
	}

	protectedPending := make([]string, 0, len(normalizedPaths))
	for _, path := range normalizedPaths {
		path = normalizeGuardedPath(path)
		if path == "" || guard.approvalGranted(tool, path) {
			continue
		}
		if guard.isProtectedPath(path) {
			protectedPending = append(protectedPending, path)
		}
	}
	if len(protectedPending) > 0 {
		reason := fmt.Sprintf("%s are protected paths under guarded writes. Approve the scoped proposal, or switch to /writes auto for broader autonomy.", strings.Join(protectedPending, ", "))
		proposal := &writeApprovalProposal{
			Prompt: prompt,
			Tool:   tool,
			Kind:   writeApprovalProtectedPath,
			Paths:  normalizedPaths,
			Reason: reason,
		}
		enrichWriteApprovalProposal(ctx, mgr, proposal, workspace.ToolRequest{Tool: tool, Path: normalizedPaths[0]})
		return proposal
	}

	union := make(map[string]struct{}, len(guard.writePaths)+len(normalizedPaths))
	for existing := range guard.writePaths {
		existing = normalizeGuardedPath(existing)
		if existing == "" {
			continue
		}
		union[existing] = struct{}{}
	}
	for _, path := range normalizedPaths {
		path = normalizeGuardedPath(path)
		if path == "" || guard.approvalGranted(tool, path) {
			continue
		}
		union[path] = struct{}{}
	}
	if len(union) <= 3 || guard.explicitMultiFileRequested() {
		return nil
	}
	scope := make([]string, 0, len(union))
	for path := range union {
		scope = append(scope, path)
	}
	slices.Sort(scope)
	proposal := &writeApprovalProposal{
		Prompt: prompt,
		Tool:   tool,
		Kind:   writeApprovalBroadScope,
		Paths:  scope,
		Reason: "guarded writes stop after a small write set. Approve this broader exact scope, or switch to /writes auto.",
	}
	enrichWriteApprovalProposal(ctx, mgr, proposal, workspace.ToolRequest{Tool: tool, Path: scope[0]})
	return proposal
}

func buildWriteApprovalProposalForRequests(ctx context.Context, mgr *workspace.Manager, prompt string, guard *implementWriteGuard, reqs []workspace.ToolRequest) *writeApprovalProposal {
	if len(reqs) == 0 {
		return nil
	}
	tool := strings.TrimSpace(reqs[0].Tool)
	paths := make([]string, 0, len(reqs))
	for _, req := range reqs {
		paths = append(paths, normalizeGuardedPath(req.Path))
	}
	proposal := buildWriteApprovalProposalForPaths(ctx, mgr, prompt, tool, paths, guard)
	if proposal == nil {
		return nil
	}
	proposal.PlannedEdits = summarizeWriteApprovalEdits(mgr, reqs)
	proposal.PreviewHunks = previewWriteApprovalEdits(mgr, reqs)
	return proposal
}

func summarizeWriteApprovalEdits(mgr *workspace.Manager, reqs []workspace.ToolRequest) []string {
	if mgr == nil || len(reqs) == 0 {
		return nil
	}
	lines := make([]string, 0, len(reqs))
	for _, req := range reqs {
		path := normalizeGuardedPath(req.Path)
		if path == "" {
			continue
		}
		fullPath, err := preflightResolvePath(mgr, path, req.Workdir)
		if err != nil {
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		current := string(content)
		if req.StartLine > 0 && req.EndLine >= req.StartLine {
			spans := preflightLineSpans(current)
			if req.StartLine > len(spans) || req.EndLine > len(spans) {
				continue
			}
			startOffset := spans[req.StartLine-1].start
			endOffset := spans[req.EndLine-1].end
			before := strings.TrimSpace(strings.TrimRight(current[startOffset:endOffset], "\n"))
			after := strings.TrimSpace(strings.TrimRight(req.Content, "\n"))
			lines = append(lines, fmt.Sprintf("%s:%d-%d %q -> %q", path, req.StartLine, req.EndLine, before, after))
			continue
		}
		if strings.TrimSpace(req.OldText) == "" {
			continue
		}
		after := strings.TrimSpace(req.NewText)
		lines = append(lines, fmt.Sprintf("%s replace %q -> %q", path, strings.TrimSpace(req.OldText), after))
	}
	return uniqueNonEmptyStrings(lines)
}

func previewWriteApprovalEdits(mgr *workspace.Manager, reqs []workspace.ToolRequest) []string {
	if mgr == nil || len(reqs) == 0 {
		return nil
	}
	hunks := make([]string, 0, len(reqs))
	for _, req := range reqs {
		path := normalizeGuardedPath(req.Path)
		if path == "" {
			continue
		}
		fullPath, err := preflightResolvePath(mgr, path, req.Workdir)
		if err != nil {
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		current := string(content)
		switch {
		case req.StartLine > 0 && req.EndLine >= req.StartLine:
			hunk, ok := previewLineRangeEdit(path, current, req.StartLine, req.EndLine, req.Content)
			if ok {
				hunks = append(hunks, hunk)
			}
		case strings.TrimSpace(req.OldText) != "":
			hunk, ok := previewReplaceEdit(path, current, req.OldText, req.NewText)
			if ok {
				hunks = append(hunks, hunk)
			}
		}
	}
	return uniqueNonEmptyStrings(hunks)
}

func previewLineRangeEdit(path string, current string, startLine int, endLine int, replacement string) (string, bool) {
	spans := preflightLineSpans(current)
	if startLine <= 0 || endLine < startLine || endLine > len(spans) {
		return "", false
	}
	contextBefore := ""
	if startLine > 1 {
		contextBefore, _ = extractReadFileLine(previewReadOutput(current, startLine-1, startLine-1), startLine-1)
	}
	contextAfter := ""
	if endLine < len(spans) {
		contextAfter, _ = extractReadFileLine(previewReadOutput(current, endLine+1, endLine+1), endLine+1)
	}
	beforeLines := previewLinesFromText(current[spans[startLine-1].start:spans[endLine-1].end])
	afterLines := previewLinesFromText(replacement)
	var b strings.Builder
	b.WriteString("--- ")
	b.WriteString(path)
	b.WriteString("\n+++ ")
	b.WriteString(path)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", startLine, maxInt(1, len(beforeLines)), startLine, maxInt(1, len(afterLines))))
	if contextBefore != "" {
		b.WriteString(" ")
		b.WriteString(contextBefore)
		b.WriteString("\n")
	}
	for _, line := range beforeLines {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range afterLines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if contextAfter != "" {
		b.WriteString(" ")
		b.WriteString(contextAfter)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func previewReplaceEdit(path string, current string, oldText string, newText string) (string, bool) {
	if strings.TrimSpace(oldText) == "" {
		return "", false
	}
	idx := strings.Index(current, oldText)
	if idx < 0 {
		return "", false
	}
	line := 1 + strings.Count(current[:idx], "\n")
	beforeLines := previewLinesFromText(oldText)
	afterLines := previewLinesFromText(newText)
	var b strings.Builder
	b.WriteString("--- ")
	b.WriteString(path)
	b.WriteString("\n+++ ")
	b.WriteString(path)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", line, maxInt(1, len(beforeLines)), line, maxInt(1, len(afterLines))))
	for _, lineText := range beforeLines {
		b.WriteString("-")
		b.WriteString(lineText)
		b.WriteString("\n")
	}
	for _, lineText := range afterLines {
		b.WriteString("+")
		b.WriteString(lineText)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func previewLinesFromText(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func previewReadOutput(content string, startLine int, endLine int) string {
	spans := preflightLineSpans(content)
	if startLine <= 0 || endLine < startLine || endLine > len(spans) {
		return ""
	}
	var b strings.Builder
	for line := startLine; line <= endLine; line++ {
		span := spans[line-1]
		text := strings.TrimRight(content[span.start:span.end], "\n")
		b.WriteString(fmt.Sprintf("%d | %s\n", line, text))
	}
	return b.String()
}

func enrichWriteApprovalProposal(ctx context.Context, mgr *workspace.Manager, proposal *writeApprovalProposal, req workspace.ToolRequest) {
	if proposal == nil {
		return
	}
	profile := analyzeWriteApprovalProposal(ctx, mgr, proposal, req)
	proposal.Risk = profile.Risk
	proposal.ChangeKinds = uniqueNonEmptyStrings(append(proposal.ChangeKinds, profile.ChangeKinds...))
	proposal.RiskReasons = uniqueNonEmptyStrings(append(proposal.RiskReasons, profile.RiskReasons...))
	proposal.ImpactSummary = uniqueNonEmptyStrings(append(proposal.ImpactSummary, profile.ImpactSummary...))
	proposal.BlastRadius = uniqueNonEmptyStrings(append(proposal.BlastRadius, profile.BlastRadius...))
	proposal.DeepAnalysis = uniqueNonEmptyStrings(append(proposal.DeepAnalysis, profile.DeepAnalysis...))
	proposal.AffectedTests = uniqueNonEmptyStrings(append(proposal.AffectedTests, profile.AffectedTests...))
	proposal.ApprovalNotes = uniqueNonEmptyStrings(append(proposal.ApprovalNotes, profile.ApprovalNotes...))
	proposal.VerifySteps = uniqueNonEmptyStrings(append(proposal.VerifySteps, profile.VerifySteps...))
	if plan := implementationVerificationPlan(mgr, proposal.Paths); strings.TrimSpace(plan.Confidence) != "" {
		coverage := strings.TrimSpace(plan.Coverage)
		confidence := strings.TrimSpace(plan.Confidence)
		if coverage != "" {
			proposal.VerifyCoverage = confidence + " (" + coverage + ")"
		} else {
			proposal.VerifyCoverage = confidence
		}
		proposal.VerifyRisk = strings.TrimSpace(plan.RiskLevel)
		proposal.VerifyExpected = uniqueNonEmptyStrings(append(proposal.VerifyExpected, plan.ExpectedFailures...))
		proposal.VerifyRepair = strings.TrimSpace(plan.RepairPolicy)
	}
}

func approvalPathRoleLines(paths []string) []string {
	lines := make([]string, 0, len(paths)+2)
	if len(paths) > 1 {
		lines = append(lines, fmt.Sprintf("requested scope spans %d files", len(paths)))
	}
	for _, path := range paths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		switch {
		case path == "config.json" || path == "config.example.json":
			lines = append(lines, fmt.Sprintf("%s controls runtime defaults and launch behavior for future sessions", path))
		case strings.HasPrefix(path, "prompts/"):
			lines = append(lines, fmt.Sprintf("%s changes baseline assistant behavior and can affect every future session", path))
		case strings.HasPrefix(path, "scripts/"):
			lines = append(lines, fmt.Sprintf("%s affects launcher or developer workflow entrypoints", path))
		case strings.HasPrefix(path, "internal/"):
			lines = append(lines, fmt.Sprintf("%s is core application code under guarded scope", path))
		}
	}
	return lines
}

func approvalVerificationCommands(mgr *workspace.Manager, paths []string) []string {
	return implementationVerificationPlan(mgr, paths).Commands
}

func approvalGoImpact(ctx context.Context, mgr *workspace.Manager, idx *semantic.GoIndex, proposal *writeApprovalProposal, req workspace.ToolRequest) ([]string, []string) {
	if idx == nil {
		return nil, nil
	}
	impact := make([]string, 0, len(proposal.Paths)+2)
	blast := make([]string, 0, 8)
	timeoutCtx := ctx
	if timeoutCtx == nil {
		timeoutCtx = context.Background()
	}
	var cancel context.CancelFunc
	timeoutCtx, cancel = context.WithTimeout(timeoutCtx, 3*time.Second)
	defer cancel()

	for _, path := range proposal.Paths {
		path = normalizeGuardedPath(path)
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		file := idx.FileByPath(path)
		if file == nil {
			continue
		}
		impact = append(impact, fmt.Sprintf("%s is in package %s with %d funcs/methods, %d types, and %d imports", path, file.Package, file.Funcs+file.Methods, file.Types, len(file.Imports)))
		if pkgDir := file.PathDir(); pkgDir != "" {
			importers := mgr.Execute(timeoutCtx, workspace.ToolRequest{Tool: "go_importers", Pattern: pkgDir})
			for _, line := range limitedResultLines(importers.Output, 3) {
				blast = append(blast, "importer: "+line)
			}
		}
		for _, related := range limitedRelatedFiles(idx, path, proposal.Prompt, 3) {
			if related == path {
				continue
			}
			blast = append(blast, "related file: "+related)
		}
		if len(blast) == 0 {
			for _, peer := range packagePeerFiles(idx, file.PathDir(), path, 3) {
				blast = append(blast, "package peer: "+peer)
			}
		}
		if symbol, ok := approvalTargetSymbol(timeoutCtx, mgr, idx, path, req); ok {
			impact = append(impact, fmt.Sprintf("target symbol likely falls inside %s (%s lines %d-%d)", symbolDisplayNameForApproval(symbol), path, symbol.StartLine, symbol.EndLine))
			callsInto := mgr.Execute(timeoutCtx, workspace.ToolRequest{Tool: "go_calls_into", Pattern: symbol.Name, Path: path})
			for _, line := range limitedResultLines(callsInto.Output, 2) {
				blast = append(blast, "incoming call: "+line)
			}
			callsFrom := mgr.Execute(timeoutCtx, workspace.ToolRequest{Tool: "go_calls_from", Pattern: symbol.Name, Path: path})
			for _, line := range limitedResultLines(callsFrom.Output, 2) {
				blast = append(blast, "outgoing call: "+line)
			}
		}
	}
	return impact, blast
}

func approvalTargetSymbol(ctx context.Context, mgr *workspace.Manager, idx *semantic.GoIndex, path string, req workspace.ToolRequest) (semantic.GoSymbol, bool) {
	file := idx.FileByPath(path)
	if file == nil {
		return semantic.GoSymbol{}, false
	}
	line := req.StartLine
	if line <= 0 {
		line = req.EndLine
	}
	if line <= 0 && strings.TrimSpace(req.OldText) != "" && mgr != nil {
		search := mgr.Execute(ctx, workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: req.OldText,
			Path:    path,
		})
		if resolved, ok := firstSearchMatchLine(search.Output, path); ok {
			line = resolved
		}
	}
	if line <= 0 {
		return semantic.GoSymbol{}, false
	}
	return enclosingSymbolAtLine(file, line)
}

func enclosingSymbolAtLine(file *semantic.GoFile, line int) (semantic.GoSymbol, bool) {
	if file == nil || line <= 0 {
		return semantic.GoSymbol{}, false
	}
	var best semantic.GoSymbol
	bestSpan := 1 << 30
	found := false
	for _, symbol := range file.Symbols {
		if symbol.StartLine <= 0 || symbol.EndLine <= 0 {
			continue
		}
		if line < symbol.StartLine || line > symbol.EndLine {
			continue
		}
		span := symbol.EndLine - symbol.StartLine
		if !found || span < bestSpan {
			best = symbol
			bestSpan = span
			found = true
		}
	}
	return best, found
}

func limitedRelatedFiles(idx *semantic.GoIndex, path string, prompt string, limit int) []string {
	if idx == nil || limit <= 0 {
		return nil
	}
	terms := []string{strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	if file := idx.FileByPath(path); file != nil {
		terms = append(terms, file.Package, file.PathDir())
	}
	terms = append(terms, approvalPromptTerms(prompt)...)
	return idx.RelevantFiles(terms, limit)
}

func packagePeerFiles(idx *semantic.GoIndex, pkgDir string, excludePath string, limit int) []string {
	if idx == nil || limit <= 0 {
		return nil
	}
	peers := make([]string, 0, limit)
	for _, file := range idx.Files {
		if file.Path == excludePath {
			continue
		}
		if file.PathDir() != pkgDir {
			continue
		}
		peers = append(peers, file.Path)
		if len(peers) >= limit {
			break
		}
	}
	return peers
}

func approvalPromptTerms(prompt string) []string {
	normalized := strings.ToLower(prompt)
	fields := strings.FieldsFunc(normalized, func(r rune) bool {
		return !(r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z'))
	})
	terms := make([]string, 0, 6)
	for _, field := range fields {
		if len(field) < 3 {
			continue
		}
		switch field {
		case "change", "verify", "write", "using", "shell", "only", "approval", "smoke", "test", "file", "path":
			continue
		}
		terms = append(terms, field)
		if len(terms) >= 6 {
			break
		}
	}
	return terms
}

func limitedResultLines(output string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	lines := make([]string, 0, limit)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return lines
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func symbolDisplayNameForApproval(symbol semantic.GoSymbol) string {
	if strings.TrimSpace(symbol.Receiver) == "" {
		return symbol.Name
	}
	return symbol.Receiver + "." + symbol.Name
}

func approvalRequiredToolResult(proposal writeApprovalProposal) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:    proposal.Tool,
		Summary: "Write approval required",
		Error:   proposal.bannerText(),
	}
}

func isWriteApprovalRequiredResult(result workspace.ToolResult) bool {
	return strings.TrimSpace(result.Summary) == "Write approval required"
}

func requestsShellOnlyMutation(normalized string) bool {
	return containsAny(
		normalized,
		"shell commands only",
		"using shell commands only",
		"use shell commands only",
		"via shell only",
		"through shell only",
	)
}

func isGuardedImplementShellAllowed(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "rg", "grep", "awk", "sed", "cat", "head", "tail", "wc", "file", "pwd", "ls", "find":
		return true
	case "git":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "status", "diff", "show", "grep", "log":
			return true
		default:
			return false
		}
	case "go":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "build", "vet", "fmt", "list":
			return true
		default:
			return false
		}
	case "cargo":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "check", "build", "fmt", "clippy":
			return true
		default:
			return false
		}
	case "pytest":
		return true
	case "make":
		if len(fields) < 2 {
			return false
		}
		return containsAny(strings.ToLower(fields[1]), "test", "build", "lint", "check", "verify")
	case "npm", "pnpm", "yarn":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "lint", "build", "check":
			return true
		case "run":
			if len(fields) < 3 {
				return false
			}
			return containsAny(strings.ToLower(fields[2]), "test", "lint", "build", "check", "verify")
		default:
			return false
		}
	case "uv":
		return strings.Contains(strings.ToLower(command), "test") || strings.Contains(strings.ToLower(command), "pytest")
	default:
		return false
	}
}

func firstPromptEditLine(mgr *workspace.Manager, path string, target string) int {
	if mgr == nil || strings.TrimSpace(path) == "" || strings.TrimSpace(target) == "" {
		return 0
	}
	fullPath := filepath.Join(mgr.Root(), path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return 0
	}
	for i, line := range strings.Split(string(content), "\n") {
		if strings.Contains(line, target) {
			return i + 1
		}
	}
	return 0
}

func replaceSystemPrompt(history []llama.ChatMessage, systemPrompt string) []llama.ChatMessage {
	cloned := append([]llama.ChatMessage(nil), history...)
	for i := range cloned {
		if normalizeHistoryRole(cloned[i].Role) == "system" {
			cloned[i].Content = systemPrompt
			return cloned
		}
	}
	return append([]llama.ChatMessage{{Role: "system", Content: systemPrompt}}, cloned...)
}

func (m *Model) runExploreSubagent(
	ctx context.Context,
	prompt string,
	opts llama.ChatOptions,
	thoroughness workspace.ExploreThoroughness,
) (exploreOutcome, error) {
	exploreWorkspace := m.newWorkspaceManager()
	if err := exploreWorkspace.SetExploreThoroughness(thoroughness); err != nil {
		return exploreOutcome{}, err
	}
	if err := exploreWorkspace.SetMode(workspace.ModeExplore); err != nil {
		return exploreOutcome{}, err
	}

	exploreRunner := &agent.Runner{
		Client:       m.client,
		Workspace:    exploreWorkspace,
		MaxToolSteps: exploreToolBudget(m.cfg.Chat.MaxToolSteps),
		AgentName:    "explore",
	}
	exploreSystemPrompt := buildSubagentSystemPrompt(m.promptStack.Text, exploreWorkspace.Instructions())
	bootstrap, err := m.bootstrapExploreEvidence(ctx, exploreWorkspace, prompt)
	if err != nil {
		return exploreOutcome{}, err
	}
	exploreRunner.InitialNeedsNarrowing = bootstrap.performedBroadReview
	if bootstrap.performedBroadReview {
		exploreRunner.InitialBroadInspects = 1
	}
	exploreRunner.SeedResults = append(exploreRunner.SeedResults, bootstrap.results...)
	exploreMessages := []llama.ChatMessage{
		{Role: "system", Content: exploreSystemPrompt},
		{Role: "user", Content: buildExploreTaskPrompt(prompt)},
	}
	exploreMessages = append(exploreMessages, bootstrap.messages...)

	m.events <- infoMsg{text: fmt.Sprintf("Explore agent is investigating (%s).", thoroughness)}
	if bootstrap.usedCache {
		m.events <- infoMsg{text: "Explore agent is reusing cached repo understanding for this session."}
	}
	reply, err := exploreRunner.Run(
		ctx,
		exploreMessages,
		opts,
		func(status string) {
			m.events <- infoMsg{text: "Explore agent: " + status}
		},
		func(event agent.ToolRequestEvent) {
			m.logToolRequest(event)
		},
		func(event agent.ToolResultEvent) {
			m.logToolResult(event)
		},
	)
	if err != nil {
		return exploreOutcome{}, err
	}
	requiredPaths := exploreEvidencePaths(bootstrap.results)
	evidencePaths := analysisEvidencePaths(requiredPaths, bootstrap.ledger)
	if answer, ok := cleanAnalysisReply(reply); ok {
		accepted := analysisReplyAcceptedWithEvidenceLedger(answer, requiredPaths, bootstrap.ledger, analysisLaneExplore)
		readiness := analysisReadinessAssessmentForReply(answer, requiredPaths, bootstrap.ledger, analysisLaneExplore, accepted, "fallback")
		m.logAnalysisReadiness(readiness)
		if readiness.Ready {
			return exploreOutcome{Summary: answer, EvidencePaths: evidencePaths}, nil
		}
		if accepted {
			m.logMessage(analysisSummaryLogKey(analysisLaneExplore), "explore summary rejected: failed readiness gate\n"+truncateText(answer, 480))
		} else {
			m.logMessage(analysisSummaryLogKey(analysisLaneExplore), "explore summary rejected: failed grounded explanation contract\n"+truncateText(reply, 480))
		}
	} else {
		m.logAnalysisReadiness(analysisReadinessAssessmentForInvalidReply(analysisLaneExplore, "fallback", "empty or tool-formatted response"))
		m.logMessage(analysisSummaryLogKey(analysisLaneExplore), "explore summary rejected: empty or tool-formatted response")
	}
	return exploreOutcome{Summary: buildExploreFallbackSummary(prompt, bootstrap), EvidencePaths: evidencePaths}, nil
}

func (m *Model) runReviewSubagent(
	ctx context.Context,
	prompt string,
	opts llama.ChatOptions,
	thoroughness workspace.ExploreThoroughness,
) (reviewOutcome, error) {
	reviewWorkspace := m.newWorkspaceManager()
	if err := reviewWorkspace.SetExploreThoroughness(thoroughness); err != nil {
		return reviewOutcome{}, err
	}
	if err := reviewWorkspace.SetMode(workspace.ModeExplore); err != nil {
		return reviewOutcome{}, err
	}

	bootstrap, err := m.bootstrapReviewEvidence(ctx, reviewWorkspace, prompt)
	if err != nil {
		return reviewOutcome{}, err
	}

	reviewMessages := []llama.ChatMessage{
		{Role: "system", Content: buildReviewSystemPrompt(m.promptStack.Text)},
		{Role: "user", Content: buildReviewTaskPrompt(prompt)},
	}
	reviewMessages = append(reviewMessages, bootstrap.messages...)

	m.events <- infoMsg{text: fmt.Sprintf("Review agent is triaging the codebase (%s).", thoroughness)}
	if bootstrap.usedCache {
		m.events <- infoMsg{text: "Review agent is reusing cached repo understanding for this session."}
	}
	if bootstrap.confirmed != nil {
		summary := formatReviewFinding(*bootstrap.confirmed)
		m.logMessage("review_confirmed", summary)
		return reviewOutcome{Summary: summary, Finding: bootstrap.confirmed, EvidencePaths: reviewCandidatePaths(bootstrap.candidates)}, nil
	}
	if bootstrap.councilBlocked && strings.TrimSpace(bootstrap.fallbackSummary) != "" {
		return reviewOutcome{Summary: strings.TrimSpace(bootstrap.fallbackSummary), EvidencePaths: reviewCandidatePaths(bootstrap.candidates)}, nil
	}
	summary, err := m.completeAnalysisSummary(
		ctx,
		reviewMessages,
		opts,
		bootstrap.fallbackSummary,
		reviewCandidatePaths(bootstrap.candidates),
		bootstrap.ledger,
		analysisLaneReview,
		func(ctx context.Context, req analysisDeepeningRequest) (analysisDeepeningResult, error) {
			return m.deepenReviewEvidence(ctx, reviewWorkspace, prompt, bootstrap.candidates, req)
		},
	)
	if err != nil {
		return reviewOutcome{}, err
	}
	return reviewOutcome{Summary: summary, EvidencePaths: reviewCandidatePaths(bootstrap.candidates)}, nil
}

func (m *Model) runArchitectureSubagent(
	ctx context.Context,
	prompt string,
	opts llama.ChatOptions,
	thoroughness workspace.ExploreThoroughness,
) (architectureOutcome, error) {
	architectureWorkspace := m.newWorkspaceManager()
	if err := architectureWorkspace.SetExploreThoroughness(thoroughness); err != nil {
		return architectureOutcome{}, err
	}
	if err := architectureWorkspace.SetMode(workspace.ModeExplore); err != nil {
		return architectureOutcome{}, err
	}

	bootstrap, err := m.bootstrapArchitectureEvidence(ctx, architectureWorkspace, prompt)
	if err != nil {
		return architectureOutcome{}, err
	}

	architectureMessages := []llama.ChatMessage{
		{Role: "system", Content: buildArchitectureSystemPrompt(m.promptStack.Text)},
		{Role: "user", Content: buildArchitectureTaskPrompt(prompt)},
	}
	architectureMessages = append(architectureMessages, bootstrap.messages...)

	m.events <- infoMsg{text: fmt.Sprintf("Architecture agent is mapping the codebase (%s).", thoroughness)}
	if bootstrap.usedCache {
		m.events <- infoMsg{text: "Architecture agent is reusing cached repo understanding for this session."}
	}
	summary, err := m.completeAnalysisSummary(
		ctx,
		architectureMessages,
		opts,
		bootstrap.fallbackSummary,
		architectureHotspotPaths(bootstrap.hotspots),
		bootstrap.ledger,
		analysisLaneArchitecture,
		func(ctx context.Context, req analysisDeepeningRequest) (analysisDeepeningResult, error) {
			return m.deepenArchitectureEvidence(ctx, architectureWorkspace, prompt, bootstrap.hotspots, req)
		},
	)
	if err != nil {
		return architectureOutcome{}, err
	}
	return architectureOutcome{Summary: summary, EvidencePaths: architectureHotspotPaths(bootstrap.hotspots)}, nil
}

type exploreBootstrap struct {
	messages             []llama.ChatMessage
	results              []workspace.ToolResult
	ledger               evidenceLedger
	performedBroadReview bool
	usedCache            bool
}

type reviewBootstrap struct {
	messages        []llama.ChatMessage
	results         []workspace.ToolResult
	ledger          evidenceLedger
	fallbackSummary string
	usedCache       bool
	candidates      []reviewCandidate
	councilBlocked  bool
	confirmed       *reviewFinding
}

type architectureBootstrap struct {
	messages        []llama.ChatMessage
	results         []workspace.ToolResult
	ledger          evidenceLedger
	fallbackSummary string
	usedCache       bool
	hotspots        []architectureHotspot
}

type exploreSessionCache struct {
	workspaceRoot string
	workspaceCWD  string
	stateToken    string
	repoProfile   string
	discovered    []string
	directories   []string
	goIndex       *semantic.GoIndex
}

type reviewCandidate struct {
	Path           string
	Score          int
	Reasons        []string
	Hits           []searchMatch
	CouncilVotes   int
	CouncilSignals []string
}

type searchMatch struct {
	Path string
	Line int
	Text string
}

type reviewFinding struct {
	Title      string
	Path       string
	Symbol     string
	Reason     string
	Evidence   []string
	Confidence string
	Scope      string
	Plan       []string
	NextStep   string
}

type architectureHotspot struct {
	Path             string
	Package          string
	Score            int
	FuncsMethods     int
	Types            int
	Imports          int
	LocalImports     int
	ImporterFiles    int
	PackageFiles     int
	BroadSurface     bool
	Responsibilities []string
	Reasons          []string
}

type reviewOutcome struct {
	Summary       string
	Finding       *reviewFinding
	EvidencePaths []string
}

type exploreOutcome struct {
	Summary       string
	EvidencePaths []string
}

type architectureOutcome struct {
	Summary       string
	EvidencePaths []string
}

func (m *Model) bootstrapExploreEvidence(
	ctx context.Context,
	exploreWorkspace *workspace.Manager,
	prompt string,
) (exploreBootstrap, error) {
	bootstrap := exploreBootstrap{
		messages: []llama.ChatMessage{{
			Role: "system",
			Content: "The harness already performed a source-focused overview and targeted bootstrap reads below. " +
				"Do not call list_files again unless the user explicitly asks for another broad overview. " +
				"Start from this evidence first; if you need more detail, prefer search_content, search_files, or read_file on discovered handles.",
		}},
	}
	discoveredFiles := make(map[string]struct{})
	discoveredDirs := make(map[string]struct{})
	level := exploreWorkspace.ExploreThoroughness()
	repoProfile := ""
	var goIndex *semantic.GoIndex
	step := -1
	runTool := func(req workspace.ToolRequest) workspace.ToolResult {
		raw := bootstrapRawToolRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "explore",
			Raw:       raw,
			Request:   req,
			Mode:      exploreWorkspace.Mode(),
			Broad:     workspace.IsBroadInspectionRequest(req),
			Narrowing: workspace.IsNarrowingRequest(req),
			Action:    agentToolActionClass(req),
		})
		result := exploreWorkspace.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{
			Step:   step,
			Agent:  "explore",
			Result: result,
		})
		bootstrap.results = append(bootstrap.results, result)
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		step--
		return result
	}

	if cache, ok := m.currentExploreCache(exploreWorkspace); ok {
		exploreWorkspace.PrimeDiscoveredPaths(cache.discovered)
		bootstrap.performedBroadReview = true
		bootstrap.usedCache = true
		repoProfile = cache.repoProfile
		goIndex = cache.goIndex
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Cached repository profile for this session:\n" + strings.TrimSpace(cache.repoProfile),
		})
		for _, path := range cache.discovered {
			discoveredFiles[path] = struct{}{}
		}
		for _, dir := range cache.directories {
			discoveredDirs[dir] = struct{}{}
		}
		m.logMessage("explore_cache", cache.repoProfile)
	} else {
		overview := runTool(workspace.ToolRequest{
			Tool:      "list_files",
			Path:      ".",
			Recursive: false,
			MaxDepth:  1,
		})
		if overview.Error == "" {
			bootstrap.performedBroadReview = true
		}
		if overview.Error != "" {
			return bootstrap, nil
		}

		overviewFiles, overviewDirs := parseDiscoveredPaths(overview.Output)
		for path := range overviewFiles {
			discoveredFiles[path] = struct{}{}
		}
		for dir := range overviewDirs {
			discoveredDirs[dir] = struct{}{}
		}

		readLimit := exploreBootstrapReadBudget(level)
		readCount := 0
		for _, path := range []string{"README.md", "go.mod", "config.json"} {
			if _, ok := overviewFiles[path]; !ok {
				continue
			}
			endLine := 120
			if path == "config.json" {
				endLine = 160
			}
			runTool(workspace.ToolRequest{
				Tool:      "read_file",
				Path:      path,
				StartLine: 1,
				EndLine:   endLine,
			})
			discoveredFiles[path] = struct{}{}
			readCount++
			if readCount >= readLimit {
				break
			}
		}

		if _, ok := overviewDirs["cmd"]; ok {
			mainSearch := runTool(workspace.ToolRequest{
				Tool:    "search_files",
				Pattern: "main.go",
				Path:    "cmd",
			})
			mainPaths := parseDiscoveredFileList(mainSearch.Output)
			if len(mainPaths) > 0 {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      mainPaths[0],
					StartLine: 1,
					EndLine:   160,
				})
				discoveredFiles[mainPaths[0]] = struct{}{}
			}
			for _, path := range mainPaths {
				discoveredFiles[path] = struct{}{}
			}
		}

		goIndex, err := semantic.BuildGoIndex(exploreWorkspace.Root())
		if err != nil {
			m.logMessage("explore_semantic_error", err.Error())
		}
		repoProfile = buildExploreRepoProfile(overview.Output, bootstrap.results)
		if goIndex != nil {
			semanticSummary := goIndex.Summary()
			if semanticSummary != "" {
				repoProfile += "\n\nGo semantic map:\n" + semanticSummary
				m.logMessage("explore_semantic", semanticSummary)
			}
		}
		repoProfile = appendLanguageCapabilityRegistry(repoProfile, exploreWorkspace.Root())
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Repository profile:\n" + repoProfile,
		})
		m.logMessage("explore_bootstrap", repoProfile)
	}

	searchPaths := prioritizedExploreSearchPaths(discoveredDirs, level)
	queryTerms := exploreQueryTerms(prompt)
	if goIndex != nil {
		semanticCandidates := make([]string, 0, 8)
		semanticCandidates = append(semanticCandidates, goIndex.DefaultFiles(exploreSemanticDefaultFileBudget(level))...)
		semanticCandidates = append(semanticCandidates, goIndex.RelevantFiles(queryTerms, exploreSemanticRelevantFileBudget(level))...)
		if looksLikeIssueHuntPrompt(prompt) {
			semanticCandidates = append(semanticCandidates, goIndex.IssueHuntFiles(exploreSemanticIssueFileBudget(level))...)
		}
		for _, path := range uniqueLimitedStrings(semanticCandidates, exploreSemanticTotalReadBudget(level)) {
			exploreWorkspace.PrimeDiscoveredPaths([]string{path})
			runTool(workspace.ToolRequest{
				Tool:      "read_file",
				Path:      path,
				StartLine: 1,
				EndLine:   200,
			})
			discoveredFiles[path] = struct{}{}
		}
	}
	if len(queryTerms) > 0 {
		for _, path := range uniqueLimitedStrings(searchPaths, exploreKeywordSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: strings.Join(queryTerms, "|"),
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			hitPaths := parseSearchMatchPaths(search.Output)
			for _, hit := range hitPaths {
				discoveredFiles[hit] = struct{}{}
			}
			for _, hit := range uniqueLimitedStrings(hitPaths, exploreKeywordReadBudget(level)) {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      hit,
					StartLine: 1,
					EndLine:   200,
				})
				discoveredFiles[hit] = struct{}{}
			}
		}
	}

	if looksLikeIssueHuntPrompt(prompt) {
		for _, path := range uniqueLimitedStrings(searchPaths, exploreIssueSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: issueSearchPattern(prompt),
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			hitPaths := parseSearchMatchPaths(search.Output)
			for _, hit := range hitPaths {
				discoveredFiles[hit] = struct{}{}
			}
			for _, hit := range uniqueLimitedStrings(hitPaths, exploreIssueReadBudget(level)) {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      hit,
					StartLine: 1,
					EndLine:   200,
				})
				discoveredFiles[hit] = struct{}{}
			}
		}
	}

	if strings.TrimSpace(repoProfile) != "" {
		m.setExploreCache(exploreWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
	}
	bootstrap.ledger = buildEvidenceLedger(prompt, bootstrap.results)
	return bootstrap, nil
}

func (m *Model) bootstrapReviewEvidence(
	ctx context.Context,
	reviewWorkspace *workspace.Manager,
	prompt string,
) (reviewBootstrap, error) {
	bootstrap := reviewBootstrap{
		messages: []llama.ChatMessage{{
			Role: "system",
			Content: "The harness already collected deterministic review evidence below. " +
				"Do not request tools in this review lane. Pick the single best issue supported by the evidence, " +
				"or say that the evidence is inconclusive.",
		}},
	}

	discoveredFiles := make(map[string]struct{})
	discoveredDirs := make(map[string]struct{})
	level := reviewWorkspace.ExploreThoroughness()
	repoProfile := ""
	var goIndex *semantic.GoIndex
	step := -1

	runTool := func(req workspace.ToolRequest) workspace.ToolResult {
		raw := bootstrapRawToolRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "review",
			Raw:       raw,
			Request:   req,
			Mode:      reviewWorkspace.Mode(),
			Broad:     workspace.IsBroadInspectionRequest(req),
			Narrowing: workspace.IsNarrowingRequest(req),
			Action:    agentToolActionClass(req),
		})
		result := reviewWorkspace.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{
			Step:   step,
			Agent:  "review",
			Result: result,
		})
		bootstrap.results = append(bootstrap.results, result)
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		step--
		return result
	}

	if cache, ok := m.currentExploreCache(reviewWorkspace); ok {
		reviewWorkspace.PrimeDiscoveredPaths(cache.discovered)
		bootstrap.usedCache = true
		repoProfile = cache.repoProfile
		goIndex = cache.goIndex
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Cached repository profile for this session:\n" + strings.TrimSpace(cache.repoProfile),
		})
		for _, path := range cache.discovered {
			discoveredFiles[path] = struct{}{}
		}
		for _, dir := range cache.directories {
			discoveredDirs[dir] = struct{}{}
		}
		m.logMessage("review_cache", cache.repoProfile)
	} else {
		overview := runTool(workspace.ToolRequest{
			Tool:      "list_files",
			Path:      ".",
			Recursive: false,
			MaxDepth:  1,
		})
		if overview.Error != "" {
			bootstrap.fallbackSummary = "Review bootstrap failed during the initial repository overview."
			return bootstrap, nil
		}

		overviewFiles, overviewDirs := parseDiscoveredPaths(overview.Output)
		for path := range overviewFiles {
			discoveredFiles[path] = struct{}{}
		}
		for dir := range overviewDirs {
			discoveredDirs[dir] = struct{}{}
		}

		readLimit := exploreBootstrapReadBudget(level)
		readCount := 0
		for _, path := range []string{"README.md", "go.mod", "config.json"} {
			if _, ok := overviewFiles[path]; !ok {
				continue
			}
			endLine := 120
			if path == "config.json" {
				endLine = 160
			}
			runTool(workspace.ToolRequest{
				Tool:      "read_file",
				Path:      path,
				StartLine: 1,
				EndLine:   endLine,
			})
			discoveredFiles[path] = struct{}{}
			readCount++
			if readCount >= readLimit {
				break
			}
		}

		if _, ok := overviewDirs["cmd"]; ok {
			mainSearch := runTool(workspace.ToolRequest{
				Tool:    "search_files",
				Pattern: "main.go",
				Path:    "cmd",
			})
			mainPaths := parseDiscoveredFileList(mainSearch.Output)
			if len(mainPaths) > 0 {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      mainPaths[0],
					StartLine: 1,
					EndLine:   160,
				})
				discoveredFiles[mainPaths[0]] = struct{}{}
			}
			for _, path := range mainPaths {
				discoveredFiles[path] = struct{}{}
			}
		}

		builtIndex, buildErr := semantic.BuildGoIndex(reviewWorkspace.Root())
		goIndex = builtIndex
		if buildErr != nil {
			m.logMessage("review_semantic_error", buildErr.Error())
		}
		repoProfile = buildExploreRepoProfile(overview.Output, nil)
		if goIndex != nil {
			semanticSummary := goIndex.Summary()
			if semanticSummary != "" {
				repoProfile += "\n\nGo semantic map:\n" + semanticSummary
				m.logMessage("review_semantic", semanticSummary)
			}
		}
		repoProfile = appendLanguageCapabilityRegistry(repoProfile, reviewWorkspace.Root())
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Repository profile:\n" + repoProfile,
		})
		m.logMessage("review_bootstrap", repoProfile)
	}

	searchPaths := prioritizedExploreSearchPaths(discoveredDirs, level)
	pattern := issueSearchPattern(prompt)
	matches := make([]searchMatch, 0, 24)
	if strings.TrimSpace(pattern) != "" {
		for _, path := range uniqueLimitedStrings(searchPaths, reviewSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: pattern,
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			for _, match := range parseSearchMatches(search.Output) {
				discoveredFiles[match.Path] = struct{}{}
				matches = append(matches, match)
			}
		}
	}

	rankedCandidates := rankReviewCandidates(prompt, goIndex, matches, reviewCandidateBudget(level))
	if len(rankedCandidates) == 0 {
		rankedCandidates = fallbackReviewCandidates(goIndex, reviewCandidateBudget(level))
	}
	candidates := reviewCouncilCandidates(prompt, goIndex, rankedCandidates, reviewCandidateBudget(level))
	bootstrap.candidates = candidates

	if len(candidates) == 0 {
		candidateBoard := buildReviewCandidateBoard(rankedCandidates)
		note := "Review council did not reach a 2-of-3 agreement on a candidate, so the review lane stopped before additional file reads."
		if candidateBoard != "" {
			bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
				Role:    "system",
				Content: note + "\n\nCandidate board:\n" + candidateBoard,
			})
			m.logMessage("review_council", note+"\n"+candidateBoard)
		} else {
			bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
				Role:    "system",
				Content: note,
			})
			m.logMessage("review_council", note)
		}
		if strings.TrimSpace(repoProfile) != "" {
			m.setExploreCache(reviewWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
		}
		bootstrap.councilBlocked = true
		bootstrap.ledger = deterministicEvidenceLedger(prompt, bootstrap.results, reviewCandidatePaths(rankedCandidates), "review_candidate_ranker")
		bootstrap.fallbackSummary = buildReviewFallbackSummary(prompt, rankedCandidates, candidateBoard, repoProfile, note, bootstrap.ledger)
		return bootstrap, nil
	}

	for _, candidate := range candidates {
		reviewWorkspace.PrimeDiscoveredPaths([]string{candidate.Path})
		discoveredFiles[candidate.Path] = struct{}{}
		start, end := reviewReadWindow(candidate)
		runTool(workspace.ToolRequest{
			Tool:      "read_file",
			Path:      candidate.Path,
			StartLine: start,
			EndLine:   end,
		})
	}

	candidateBoard := buildReviewCandidateBoard(candidates)
	if candidateBoard != "" {
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Review candidate ranking:\n" + candidateBoard,
		})
		m.logMessage("review_candidates", candidateBoard)
	}

	if finding := confirmReviewFinding(prompt, goIndex, candidates); finding != nil {
		bootstrap.confirmed = finding
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Deterministically confirmed review finding:\n" + formatReviewFinding(*finding),
		})
	}

	if strings.TrimSpace(repoProfile) != "" {
		m.setExploreCache(reviewWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
	}
	bootstrap.ledger = deterministicEvidenceLedger(prompt, bootstrap.results, reviewCandidatePaths(candidates), "review_candidate_ranker")
	bootstrap.fallbackSummary = buildReviewFallbackSummary(prompt, candidates, candidateBoard, repoProfile, "", bootstrap.ledger)
	return bootstrap, nil
}

func (m *Model) bootstrapArchitectureEvidence(
	ctx context.Context,
	architectureWorkspace *workspace.Manager,
	prompt string,
) (architectureBootstrap, error) {
	bootstrap := architectureBootstrap{
		messages: []llama.ChatMessage{{
			Role: "system",
			Content: "The harness already collected deterministic architecture evidence below. " +
				"Do not request tools in this architecture lane. Explain the current structure, identify the best structural opportunities, " +
				"and ground your answer in the supplied hotspot paths and repository profile.",
		}},
	}

	discoveredFiles := make(map[string]struct{})
	discoveredDirs := make(map[string]struct{})
	level := architectureWorkspace.ExploreThoroughness()
	repoProfile := ""
	var goIndex *semantic.GoIndex
	step := -1

	runTool := func(req workspace.ToolRequest) workspace.ToolResult {
		raw := bootstrapRawToolRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "architecture",
			Raw:       raw,
			Request:   req,
			Mode:      architectureWorkspace.Mode(),
			Broad:     workspace.IsBroadInspectionRequest(req),
			Narrowing: workspace.IsNarrowingRequest(req),
			Action:    agentToolActionClass(req),
		})
		result := architectureWorkspace.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{
			Step:   step,
			Agent:  "architecture",
			Result: result,
		})
		bootstrap.results = append(bootstrap.results, result)
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		step--
		return result
	}

	if cache, ok := m.currentExploreCache(architectureWorkspace); ok {
		architectureWorkspace.PrimeDiscoveredPaths(cache.discovered)
		bootstrap.usedCache = true
		repoProfile = cache.repoProfile
		goIndex = cache.goIndex
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Cached repository profile for this session:\n" + strings.TrimSpace(cache.repoProfile),
		})
		for _, path := range cache.discovered {
			discoveredFiles[path] = struct{}{}
		}
		for _, dir := range cache.directories {
			discoveredDirs[dir] = struct{}{}
		}
		m.logMessage("architecture_cache", cache.repoProfile)
	} else {
		overview := runTool(workspace.ToolRequest{
			Tool:      "list_files",
			Path:      ".",
			Recursive: false,
			MaxDepth:  1,
		})
		if overview.Error != "" {
			bootstrap.fallbackSummary = "Architecture bootstrap failed during the initial repository overview."
			return bootstrap, nil
		}

		overviewFiles, overviewDirs := parseDiscoveredPaths(overview.Output)
		for path := range overviewFiles {
			discoveredFiles[path] = struct{}{}
		}
		for dir := range overviewDirs {
			discoveredDirs[dir] = struct{}{}
		}

		readLimit := exploreBootstrapReadBudget(level)
		readCount := 0
		for _, path := range []string{"README.md", "go.mod", "config.json"} {
			if _, ok := overviewFiles[path]; !ok {
				continue
			}
			endLine := 120
			if path == "config.json" {
				endLine = 160
			}
			runTool(workspace.ToolRequest{
				Tool:      "read_file",
				Path:      path,
				StartLine: 1,
				EndLine:   endLine,
			})
			discoveredFiles[path] = struct{}{}
			readCount++
			if readCount >= readLimit {
				break
			}
		}

		if _, ok := overviewDirs["cmd"]; ok {
			mainSearch := runTool(workspace.ToolRequest{
				Tool:    "search_files",
				Pattern: "main.go",
				Path:    "cmd",
			})
			mainPaths := parseDiscoveredFileList(mainSearch.Output)
			if len(mainPaths) > 0 {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      mainPaths[0],
					StartLine: 1,
					EndLine:   160,
				})
				discoveredFiles[mainPaths[0]] = struct{}{}
			}
			for _, path := range mainPaths {
				discoveredFiles[path] = struct{}{}
			}
		}

		builtIndex, buildErr := semantic.BuildGoIndex(architectureWorkspace.Root())
		goIndex = builtIndex
		if buildErr != nil {
			m.logMessage("architecture_semantic_error", buildErr.Error())
		}
		repoProfile = buildExploreRepoProfile(overview.Output, nil)
		if goIndex != nil {
			semanticSummary := goIndex.Summary()
			if semanticSummary != "" {
				repoProfile += "\n\nGo semantic map:\n" + semanticSummary
				m.logMessage("architecture_semantic", semanticSummary)
			}
		}
		repoProfile = appendLanguageCapabilityRegistry(repoProfile, architectureWorkspace.Root())
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Repository profile:\n" + repoProfile,
		})
		m.logMessage("architecture_bootstrap", repoProfile)
	}

	searchPaths := prioritizedExploreSearchPaths(discoveredDirs, level)
	if pattern := architectureSearchPattern(prompt); strings.TrimSpace(pattern) != "" {
		for _, path := range uniqueLimitedStrings(searchPaths, architectureSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: pattern,
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			for _, hit := range uniqueLimitedStrings(parseSearchMatchPaths(search.Output), architectureSearchReadBudget(level)) {
				architectureWorkspace.PrimeDiscoveredPaths([]string{hit})
				discoveredFiles[hit] = struct{}{}
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      hit,
					StartLine: 1,
					EndLine:   220,
				})
			}
		}
	}

	hotspots := rankArchitectureHotspots(prompt, goIndex, architectureHotspotBudget(level))
	bootstrap.hotspots = hotspots
	for _, path := range uniqueLimitedStrings(append(architectureHotspotPaths(hotspots), architectureRelevantPaths(prompt, goIndex, architectureRelevantFileBudget(level))...), architectureReadBudget(level)) {
		architectureWorkspace.PrimeDiscoveredPaths([]string{path})
		discoveredFiles[path] = struct{}{}
		runTool(workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: 1,
			EndLine:   220,
		})
	}

	hotspotBoard := buildArchitectureHotspotBoard(hotspots)
	if hotspotBoard != "" {
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Architecture hotspot board:\n" + hotspotBoard,
		})
		m.logMessage("architecture_hotspots", hotspotBoard)
	}

	if strings.TrimSpace(repoProfile) != "" {
		m.setExploreCache(architectureWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
	}
	bootstrap.ledger = deterministicEvidenceLedger(prompt, bootstrap.results, architectureHotspotPaths(hotspots), "architecture_hotspot_ranker")
	bootstrap.fallbackSummary = buildArchitectureFallbackSummary(prompt, hotspots, hotspotBoard, repoProfile, "", bootstrap.ledger)
	return bootstrap, nil
}

func (m *Model) deepenReviewEvidence(
	ctx context.Context,
	reviewWorkspace *workspace.Manager,
	prompt string,
	candidates []reviewCandidate,
	req analysisDeepeningRequest,
) (analysisDeepeningResult, error) {
	paths := reviewCandidatePaths(candidates)
	if len(paths) == 0 {
		paths = req.RequiredPaths
	}
	pathLimit := 2
	readEndLine := 420
	if req.Attempt >= 2 {
		pathLimit = 4
		readEndLine = 720
	}
	paths = uniqueLimitedStrings(paths, pathLimit)
	if len(paths) == 0 {
		return analysisDeepeningResult{
			Lane:      string(analysisLaneReview),
			Attempted: false,
			Exhausted: true,
			Action:    "skip",
			Reason:    "review deepening had no candidate paths to inspect",
		}, nil
	}

	results := make([]workspace.ToolResult, 0, len(paths)*2)
	for i, path := range paths {
		reviewWorkspace.PrimeDiscoveredPaths([]string{path})
		results = append(results, m.executeAnalysisDeepeningTool(ctx, reviewWorkspace, analysisLaneReview, i+1, workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: 1,
			EndLine:   readEndLine,
		}))
		if pattern := issueSearchPattern(prompt); strings.TrimSpace(pattern) != "" {
			results = append(results, m.executeAnalysisDeepeningTool(ctx, reviewWorkspace, analysisLaneReview, i+101, workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: pattern,
				Path:    path,
			}))
		}
	}
	attempt := maxInt(req.Attempt, 1)
	return buildAnalysisDeepeningResult(prompt, analysisLaneReview, attempt, results, paths, fmt.Sprintf("readiness gate failed; retry with wider candidate reads (attempt %d)", attempt)), nil
}

func (m *Model) deepenArchitectureEvidence(
	ctx context.Context,
	architectureWorkspace *workspace.Manager,
	prompt string,
	hotspots []architectureHotspot,
	req analysisDeepeningRequest,
) (analysisDeepeningResult, error) {
	paths := architectureHotspotPaths(hotspots)
	if len(paths) == 0 {
		paths = req.RequiredPaths
	}
	pathLimit := 2
	readEndLine := 460
	if req.Attempt >= 2 {
		pathLimit = 4
		readEndLine = 760
	}
	paths = uniqueLimitedStrings(paths, pathLimit)
	if len(paths) == 0 {
		return analysisDeepeningResult{
			Lane:      string(analysisLaneArchitecture),
			Attempted: false,
			Exhausted: true,
			Action:    "skip",
			Reason:    "architecture deepening had no hotspot paths to inspect",
		}, nil
	}

	results := make([]workspace.ToolResult, 0, len(paths)*2)
	for i, path := range paths {
		architectureWorkspace.PrimeDiscoveredPaths([]string{path})
		results = append(results, m.executeAnalysisDeepeningTool(ctx, architectureWorkspace, analysisLaneArchitecture, i+1, workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: 1,
			EndLine:   readEndLine,
		}))
		results = append(results, m.executeAnalysisDeepeningTool(ctx, architectureWorkspace, analysisLaneArchitecture, i+101, workspace.ToolRequest{
			Tool:     "go_related_files",
			Pattern:  architectureRelatedPatternForPath(path),
			Path:     filepath.Dir(path),
			MaxDepth: 12,
		}))
	}
	attempt := maxInt(req.Attempt, 1)
	return buildAnalysisDeepeningResult(prompt, analysisLaneArchitecture, attempt, results, paths, fmt.Sprintf("readiness gate failed; retry with deeper hotspot evidence (attempt %d)", attempt)), nil
}

func architectureRelatedPatternForPath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return "architecture structure"
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	dir := filepath.Base(filepath.Dir(path))
	terms := []string{base}
	if dir != "." && dir != "" && dir != base {
		terms = append(terms, dir)
	}
	return strings.Join(uniqueNonEmptyStrings(terms), " ")
}

func (m *Model) executeAnalysisDeepeningTool(ctx context.Context, mgr *workspace.Manager, lane analysisLane, step int, req workspace.ToolRequest) workspace.ToolResult {
	raw := bootstrapRawToolRequest(req)
	agentName := string(lane) + "_deepening"
	m.logToolRequest(agent.ToolRequestEvent{
		Step:      -1000 - step,
		Agent:     agentName,
		Raw:       raw,
		Request:   req,
		Mode:      mgr.Mode(),
		Broad:     workspace.IsBroadInspectionRequest(req),
		Narrowing: workspace.IsNarrowingRequest(req),
		Action:    agentToolActionClass(req),
	})
	result := mgr.Execute(ctx, req)
	if strings.TrimSpace(result.Error) != "" {
		m.logToolFailureConfidence(toolFailureConfidenceSnapshot{
			Lane:             string(lane),
			Tool:             req.Tool,
			Action:           agentToolActionClass(req),
			ConfidenceImpact: "degrade_until_alternate_evidence_covers_gap",
			Reason:           truncateText(result.Error, 180),
		})
	}
	m.logToolResult(agent.ToolResultEvent{
		Step:   -1000 - step,
		Agent:  agentName,
		Result: result,
	})
	return result
}

func buildAnalysisDeepeningResult(prompt string, lane analysisLane, attempt int, results []workspace.ToolResult, seedPaths []string, reason string) analysisDeepeningResult {
	successful := make([]workspace.ToolResult, 0, len(results))
	failedTools := 0
	messages := make([]llama.ChatMessage, 0, len(results)+1)
	messages = append(messages, llama.ChatMessage{
		Role:    "system",
		Content: "Additional deterministic readiness deepening evidence follows. Prefer this evidence over the earlier low-confidence summary attempt.",
	})
	for _, result := range results {
		messages = append(messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		if result.Error == "" {
			successful = append(successful, result)
		} else {
			failedTools++
		}
	}
	ledger := deterministicEvidenceLedger(prompt, successful, seedPaths, "readiness_deepener")
	evidencePaths := analysisEvidencePaths(seedPaths, ledger)
	newPaths := evidenceDeltaPaths(evidencePaths, seedPaths)
	return analysisDeepeningResult{
		Lane:          string(lane),
		Attempt:       attempt,
		Attempted:     len(results) > 0,
		Exhausted:     len(successful) == 0,
		Action:        "retry_with_evidence",
		Reason:        reason,
		EvidencePaths: evidencePaths,
		NewPaths:      newPaths,
		ToolResults:   len(results),
		FailedTools:   failedTools,
		Messages:      messages,
		Ledger:        ledger,
	}
}

func evidenceDeltaPaths(after []string, before []string) []string {
	beforeSet := make(map[string]struct{}, len(before))
	for _, path := range before {
		path = normalizeGuardedPath(path)
		if path != "" {
			beforeSet[path] = struct{}{}
		}
	}
	out := make([]string, 0)
	for _, path := range after {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if _, ok := beforeSet[path]; ok {
			continue
		}
		out = append(out, path)
	}
	return uniqueNonEmptyStrings(out)
}

func evidenceMarginalValue(newPaths []string, failedTools int, totalTools int) string {
	switch {
	case len(newPaths) >= 2:
		return "high"
	case len(newPaths) == 1:
		return "medium"
	case totalTools > 0 && failedTools == totalTools:
		return "blocked_by_tool_failures"
	default:
		return "low"
	}
}

func architectureHotspotPaths(hotspots []architectureHotspot) []string {
	paths := make([]string, 0, len(hotspots))
	for _, hotspot := range hotspots {
		paths = append(paths, hotspot.Path)
	}
	return uniqueLimitedStrings(paths, len(paths))
}

func architectureRelevantPaths(prompt string, goIndex *semantic.GoIndex, limit int) []string {
	if goIndex == nil || limit == 0 {
		return nil
	}
	return goIndex.RelevantFiles(exploreQueryTerms(prompt), limit)
}

func rankArchitectureHotspots(prompt string, goIndex *semantic.GoIndex, limit int) []architectureHotspot {
	if goIndex == nil || limit == 0 {
		return nil
	}
	relevance := make(map[string]int)
	for rank, path := range architectureRelevantPaths(prompt, goIndex, limit*3) {
		relevance[path] = limit*3 - rank
	}

	hotspots := make([]architectureHotspot, 0, len(goIndex.Files))
	for _, file := range goIndex.Files {
		if file.IsTest {
			continue
		}

		pkg := goIndex.Package[file.PathDir()]
		funcsMethods := file.Funcs + file.Methods
		localImports := architectureLocalImportCount(file, goIndex.Module)
		importerFiles := len(goImporterFiles(goIndex, file.PathDir(), 32))
		responsibilities := architectureResponsibilitySignals(file)
		broadSurface := architectureBroadSurface(file.Path, funcsMethods, file.Types, len(file.Imports), importerFiles)
		score := 0
		reasons := make([]string, 0, 5)

		score += minInt(funcsMethods*3, 72)
		if funcsMethods >= 10 {
			reasons = append(reasons, fmt.Sprintf("%d funcs/methods", funcsMethods))
		}

		score += minInt(file.Types*4, 24)
		if file.Types >= 4 {
			reasons = append(reasons, fmt.Sprintf("%d types", file.Types))
		}

		score += minInt(len(file.Imports)*2, 24)
		if len(file.Imports) >= 8 {
			reasons = append(reasons, fmt.Sprintf("%d imports", len(file.Imports)))
		}
		score += minInt(localImports*2, 18)
		if localImports >= 4 {
			reasons = append(reasons, fmt.Sprintf("%d local package imports", localImports))
		}
		score += minInt(importerFiles*2, 20)
		if importerFiles >= 4 {
			reasons = append(reasons, fmt.Sprintf("%d workspace importer files", importerFiles))
		}

		score += architectureSourceDirPriority(file.SourceDir)
		if pkg.Files >= 4 {
			score += minInt(pkg.Files*3, 18)
			reasons = append(reasons, fmt.Sprintf("package %s spans %d files", pkg.Name, pkg.Files))
		}
		if file.HasMain {
			score += 8
			reasons = append(reasons, "entrypoint responsibilities")
		}
		if broadSurface {
			score += 10
			reasons = append(reasons, "mixed orchestration/integration surface")
		}
		if len(responsibilities) >= 3 {
			score += minInt(len(responsibilities)*6, 30)
			reasons = append(reasons, "mixed responsibilities: "+strings.Join(responsibilities, ", "))
		}
		if containsAny(strings.ToLower(file.Path), "/manager", "/workspace", "/runner", "/app", "/controller") && funcsMethods >= 12 {
			score += 8
			reasons = append(reasons, "manager-style broad surface")
		}
		if bonus := relevance[file.Path]; bonus > 0 {
			score += bonus * 4
			reasons = append(reasons, "matches the current architecture question")
		}
		if score == 0 {
			continue
		}

		hotspots = append(hotspots, architectureHotspot{
			Path:             file.Path,
			Package:          file.Package,
			Score:            score,
			FuncsMethods:     funcsMethods,
			Types:            file.Types,
			Imports:          len(file.Imports),
			LocalImports:     localImports,
			ImporterFiles:    importerFiles,
			PackageFiles:     pkg.Files,
			BroadSurface:     broadSurface,
			Responsibilities: responsibilities,
			Reasons:          uniqueLimitedStrings(reasons, 5),
		})
	}

	slices.SortFunc(hotspots, func(a, b architectureHotspot) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if a.FuncsMethods != b.FuncsMethods {
			return b.FuncsMethods - a.FuncsMethods
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(hotspots) > limit {
		hotspots = hotspots[:limit]
	}
	return hotspots
}

func architectureLocalImportCount(file semantic.GoFile, module string) int {
	module = strings.TrimSpace(module)
	if module == "" {
		return 0
	}
	count := 0
	for _, imp := range file.Imports {
		if strings.HasPrefix(strings.TrimSpace(imp), module+"/") {
			count++
		}
	}
	return count
}

func architectureResponsibilitySignals(file semantic.GoFile) []string {
	signalText := strings.ToLower(file.Path + " " + strings.Join(file.Imports, " "))
	for _, symbol := range file.Symbols {
		signalText += " " + strings.ToLower(symbol.Name)
		if symbol.Receiver != "" {
			signalText += " " + strings.ToLower(symbol.Receiver)
		}
	}
	type responsibilityRule struct {
		label string
		terms []string
	}
	rules := []responsibilityRule{
		{label: "ui", terms: []string{"render", "view", "style", "layout", "panel", "textarea", "viewport", "lipgloss", "bubbletea"}},
		{label: "routing", terms: []string{"route", "classify", "lane", "plan", "transition", "prompt"}},
		{label: "workspace", terms: []string{"workspace", "tool", "shell", "file", "path", "guard", "write"}},
		{label: "agent", terms: []string{"agent", "runner", "subagent", "llama", "chat", "model", "client"}},
		{label: "state", terms: []string{"state", "cache", "session", "journal", "rollback", "resume", "history"}},
		{label: "config", terms: []string{"config", "profile", "preset", "setting", "option"}},
		{label: "verification", terms: []string{"verify", "test", "diagnostic", "check", "build"}},
	}
	signals := make([]string, 0, len(rules))
	for _, rule := range rules {
		if containsAny(signalText, rule.terms...) {
			signals = append(signals, rule.label)
		}
	}
	return uniqueLimitedStrings(signals, 6)
}

func architectureBroadSurface(path string, funcsMethods int, types int, imports int, importerFiles int) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	switch {
	case funcsMethods >= 18 && types >= 3 && imports >= 8:
		return true
	case funcsMethods >= 14 && importerFiles >= 4 && imports >= 6:
		return true
	case containsAny(path, "/manager", "/workspace", "/runner", "/app", "/controller") && funcsMethods >= 12 && imports >= 6:
		return true
	default:
		return false
	}
}

func buildArchitectureHotspotBoard(hotspots []architectureHotspot) string {
	if len(hotspots) == 0 {
		return ""
	}
	lines := make([]string, 0, len(hotspots))
	for _, hotspot := range hotspots {
		line := fmt.Sprintf("- %s (score %d", hotspot.Path, hotspot.Score)
		if hotspot.Package != "" {
			line += ", package " + hotspot.Package
		}
		line += ")"
		if len(hotspot.Reasons) > 0 {
			line += ": " + strings.Join(hotspot.Reasons, ", ")
		}
		if hotspot.ImporterFiles > 0 || hotspot.LocalImports > 0 {
			line += fmt.Sprintf(" | coupling: fan-out %d local imports, fan-in %d importer files", hotspot.LocalImports, hotspot.ImporterFiles)
		}
		if len(hotspot.Responsibilities) > 0 {
			line += " | responsibilities: " + strings.Join(hotspot.Responsibilities, ", ")
		}
		line += " | direction: " + architectureHotspotDirection(hotspot)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func architectureHotspotDirection(hotspot architectureHotspot) string {
	switch {
	case hotspot.BroadSurface && hotspot.FuncsMethods >= 18 && hotspot.Types >= 4:
		return "split orchestration/state handling from helper and rendering concerns"
	case hotspot.BroadSurface && hotspot.ImporterFiles >= 4:
		return "extract an interface or subpackage so high-dependency callers stop relying on one large coordination surface"
	case hotspot.FuncsMethods >= 18:
		return "separate routing and coordination concerns from leaf helpers"
	case hotspot.Imports >= 10:
		return "introduce a narrower facade so callers do not depend on a broad manager surface"
	case hotspot.PackageFiles >= 5:
		return "re-evaluate package boundaries and move specialized responsibilities behind adapters"
	default:
		return "tighten the file boundary around one primary responsibility"
	}
}

func (m *Model) completeAnalysisSummary(
	ctx context.Context,
	messages []llama.ChatMessage,
	opts llama.ChatOptions,
	fallback string,
	requiredPaths []string,
	ledger evidenceLedger,
	lane analysisLane,
	deepener analysisDeepener,
) (string, error) {
	modelCalls := 0
	usedDeepening := 0
	usedToolResults := 0
	newEvidenceCount := 0
	logReadiness := func(readiness analysisReadinessAssessment) {
		m.logAnalysisReadiness(readiness)
		m.logClaimConfidence(claimConfidenceFromReadiness(readiness))
	}
	logDeepening := func(deepening analysisDeepeningResult) {
		m.logAnalysisDeepening(deepening)
		m.logEvidenceDelta(evidenceDeltaSnapshot{
			Lane:             strings.TrimSpace(deepening.Lane),
			Attempt:          deepening.Attempt,
			NewEvidencePaths: uniqueNonEmptyStrings(deepening.NewPaths),
			TotalPaths:       len(deepening.EvidencePaths),
			ToolResults:      deepening.ToolResults,
			SuccessfulTools:  maxInt(deepening.ToolResults-deepening.FailedTools, 0),
			FailedTools:      deepening.FailedTools,
			MarginalValue:    evidenceMarginalValue(deepening.NewPaths, deepening.FailedTools, deepening.ToolResults),
		})
		if deepening.Attempted {
			usedDeepening++
			usedToolResults += deepening.ToolResults
			newEvidenceCount += len(deepening.NewPaths)
		}
	}
	logBudget := func(stopReason string, evidencePaths []string, exhausted bool) {
		m.logBudgetLedger(budgetLedgerSnapshot{
			Lane:                   string(lane),
			Level:                  "analysis",
			UsedModelCalls:         modelCalls,
			UsedDeepeningPasses:    usedDeepening,
			MaxDeepeningPasses:     2,
			UsedToolResults:        usedToolResults,
			NewEvidencePaths:       newEvidenceCount,
			TotalEvidencePaths:     len(uniqueNonEmptyStrings(evidencePaths)),
			StopReason:             strings.TrimSpace(stopReason),
			BudgetExhausted:        exhausted,
			NextAllowedActions:     analysisBudgetNextActions(stopReason),
			MarginalEvidencePolicy: "continue while readiness is false and deterministic deepening adds evidence before bounded pass limit",
			UserVisibleUncertainty: uncertaintyForStopReason(stopReason),
		})
	}
	if deterministicAnswer, readiness, ok := deterministicArchitectureAnswer(fallback, requiredPaths, ledger, lane); ok {
		logReadiness(readiness)
		logBudget("ready_deterministic_architecture_evidence", readiness.EvidencePaths, false)
		return deterministicAnswer, nil
	}
	reply, err := m.client.CompleteChat(ctx, messages, opts)
	if err != nil {
		return "", err
	}
	modelCalls++
	retryMessagesBase := append([]llama.ChatMessage(nil), messages...)
	retryRequiredPaths := append([]string(nil), requiredPaths...)
	retryLedger := ledger
	if answer, ok := cleanAnalysisReply(reply); ok {
		accepted := analysisReplyAcceptedWithEvidenceLedger(answer, requiredPaths, ledger, lane)
		readiness := analysisReadinessAssessmentForReply(answer, requiredPaths, ledger, lane, accepted, "retry")
		logReadiness(readiness)
		if readiness.Ready {
			logBudget("ready_initial_answer", readiness.EvidencePaths, false)
			return answer, nil
		}
		if shouldDeepenAfterReadiness(readiness) && deepener != nil {
			deepening, err := deepener(ctx, analysisDeepeningRequest{
				Attempt:       1,
				Lane:          lane,
				Readiness:     readiness,
				RequiredPaths: analysisEvidencePaths(requiredPaths, ledger),
				Ledger:        ledger,
			})
			if err != nil {
				return "", err
			}
			logDeepening(deepening)
			if deepening.Attempted {
				retryMessagesBase = append(retryMessagesBase, deepening.Messages...)
				retryRequiredPaths = uniqueLimitedStrings(append(retryRequiredPaths, deepening.EvidencePaths...), 16)
				retryLedger = mergeEvidenceLedgers(retryLedger, deepening.Ledger)
			}
		}
		if accepted {
			m.logMessage(analysisSummaryLogKey(lane), "initial summary rejected: failed readiness gate\n"+truncateText(answer, 480))
		} else {
			m.logMessage(analysisSummaryLogKey(lane), "initial summary rejected: failed lane-specific grounding or contract\n"+truncateText(answer, 480))
		}
	} else {
		logReadiness(analysisReadinessAssessmentForInvalidReply(lane, "retry", "empty or tool-formatted response"))
		m.logMessage(analysisSummaryLogKey(lane), "initial summary rejected: empty or tool-formatted response")
	}

	retryMessages := append([]llama.ChatMessage(nil), retryMessagesBase...)
	requirement := analysisRetryRequirement(lane, analysisEvidencePaths(retryRequiredPaths, retryLedger))
	retryMessages = append(retryMessages, llama.ChatMessage{
		Role:    "system",
		Content: requirement,
	})
	retryReply, err := m.client.CompleteChat(ctx, retryMessages, opts)
	if err != nil {
		return "", err
	}
	modelCalls++
	if answer, ok := cleanAnalysisReply(retryReply); ok {
		accepted := analysisReplyAcceptedWithEvidenceLedger(answer, retryRequiredPaths, retryLedger, lane)
		nextAction := "fallback"
		if deepener != nil {
			nextAction = "retry"
		}
		readiness := analysisReadinessAssessmentForReply(answer, retryRequiredPaths, retryLedger, lane, accepted, nextAction)
		logReadiness(readiness)
		if readiness.Ready {
			logBudget("ready_after_retry", readiness.EvidencePaths, false)
			return answer, nil
		}
		if shouldDeepenAfterReadiness(readiness) && deepener != nil {
			deepening, err := deepener(ctx, analysisDeepeningRequest{
				Attempt:       2,
				Lane:          lane,
				Readiness:     readiness,
				RequiredPaths: analysisEvidencePaths(retryRequiredPaths, retryLedger),
				Ledger:        retryLedger,
			})
			if err != nil {
				return "", err
			}
			logDeepening(deepening)
			if deepening.Attempted {
				retryRequiredPaths = uniqueLimitedStrings(append(retryRequiredPaths, deepening.EvidencePaths...), 20)
				retryLedger = mergeEvidenceLedgers(retryLedger, deepening.Ledger)
				finalMessages := append([]llama.ChatMessage(nil), retryMessagesBase...)
				finalMessages = append(finalMessages, deepening.Messages...)
				finalMessages = append(finalMessages, llama.ChatMessage{
					Role:    "system",
					Content: analysisRetryRequirement(lane, analysisEvidencePaths(retryRequiredPaths, retryLedger)) + " This is the final bounded readiness attempt before the harness falls back to deterministic evidence.",
				})
				finalReply, err := m.client.CompleteChat(ctx, finalMessages, opts)
				if err != nil {
					return "", err
				}
				modelCalls++
				if finalAnswer, finalOK := cleanAnalysisReply(finalReply); finalOK {
					finalAccepted := analysisReplyAcceptedWithEvidenceLedger(finalAnswer, retryRequiredPaths, retryLedger, lane)
					finalReadiness := analysisReadinessAssessmentForReply(finalAnswer, retryRequiredPaths, retryLedger, lane, finalAccepted, "fallback")
					logReadiness(finalReadiness)
					if finalReadiness.Ready {
						logBudget("ready_after_final_deepening", finalReadiness.EvidencePaths, false)
						return finalAnswer, nil
					}
					m.logMessage(analysisSummaryLogKey(lane), "final summary rejected: failed readiness gate\n"+truncateText(finalAnswer, 480))
				} else {
					logReadiness(analysisReadinessAssessmentForInvalidReply(lane, "fallback", "empty or tool-formatted response"))
					m.logMessage(analysisSummaryLogKey(lane), "final summary rejected: empty or tool-formatted response")
				}
			}
		}
		if accepted {
			m.logMessage(analysisSummaryLogKey(lane), "retry summary rejected: failed readiness gate\n"+truncateText(answer, 480))
		} else {
			m.logMessage(analysisSummaryLogKey(lane), "retry summary rejected: failed lane-specific grounding or contract\n"+truncateText(answer, 480))
		}
	} else {
		logReadiness(analysisReadinessAssessmentForInvalidReply(lane, "fallback", "empty or tool-formatted response"))
		m.logMessage(analysisSummaryLogKey(lane), "retry summary rejected: empty or tool-formatted response")
	}
	if strings.TrimSpace(fallback) != "" {
		fallbackAnswer := strings.TrimSpace(fallback)
		fallbackAccepted := analysisReplyAcceptedWithEvidenceLedger(fallbackAnswer, retryRequiredPaths, retryLedger, lane)
		fallbackReadiness := analysisReadinessAssessmentForReply(fallbackAnswer, retryRequiredPaths, retryLedger, lane, fallbackAccepted, "answer")
		logReadiness(fallbackReadiness)
		logBudget("fallback_after_exhausted_readiness_attempts", fallbackReadiness.EvidencePaths, usedDeepening >= 2)
		return fallbackAnswer, nil
	}
	logBudget("no_usable_summary", analysisEvidencePaths(retryRequiredPaths, retryLedger), usedDeepening >= 2)
	return "", fmt.Errorf("analysis lane returned no usable summary")
}

func cleanAnalysisReply(reply string) (string, bool) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", false
	}
	if _, _, ok, err := workspace.ParseToolRequest(reply); err != nil || ok {
		return "", false
	}
	return reply, true
}

func deterministicArchitectureAnswer(fallback string, requiredPaths []string, ledger evidenceLedger, lane analysisLane) (string, analysisReadinessAssessment, bool) {
	if lane != analysisLaneArchitecture {
		return "", analysisReadinessAssessment{}, false
	}
	answer := strings.TrimSpace(fallback)
	if answer == "" {
		return "", analysisReadinessAssessment{}, false
	}
	accepted := analysisReplyAcceptedWithEvidenceLedger(answer, requiredPaths, ledger, lane)
	readiness := analysisReadinessAssessmentForReply(answer, requiredPaths, ledger, lane, accepted, "answer")
	if !readiness.Ready || readiness.Confidence != "high" {
		return "", analysisReadinessAssessment{}, false
	}
	return answer, readiness, true
}

type analysisReadinessAssessment struct {
	Lane          string   `json:"lane,omitempty"`
	Ready         bool     `json:"ready"`
	Confidence    string   `json:"confidence,omitempty"`
	Action        string   `json:"action,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	EvidencePaths []string `json:"evidence_paths,omitempty"`
}

type analysisDeepener func(context.Context, analysisDeepeningRequest) (analysisDeepeningResult, error)

type analysisDeepeningRequest struct {
	Attempt       int
	Lane          analysisLane
	Readiness     analysisReadinessAssessment
	RequiredPaths []string
	Ledger        evidenceLedger
}

type analysisDeepeningResult struct {
	Lane          string              `json:"lane,omitempty"`
	Attempt       int                 `json:"attempt,omitempty"`
	Attempted     bool                `json:"attempted"`
	Exhausted     bool                `json:"exhausted,omitempty"`
	Action        string              `json:"action,omitempty"`
	Reason        string              `json:"reason,omitempty"`
	EvidencePaths []string            `json:"evidence_paths,omitempty"`
	NewPaths      []string            `json:"new_paths,omitempty"`
	ToolResults   int                 `json:"tool_results,omitempty"`
	FailedTools   int                 `json:"failed_tools,omitempty"`
	Messages      []llama.ChatMessage `json:"-"`
	Ledger        evidenceLedger      `json:"-"`
}

func analysisReadinessAssessmentForReply(
	reply string,
	requiredPaths []string,
	ledger evidenceLedger,
	lane analysisLane,
	contractAccepted bool,
	notReadyAction string,
) analysisReadinessAssessment {
	evidencePaths := analysisEvidencePaths(requiredPaths, ledger)
	reason := "summary satisfies lane contract and evidence gate"
	confidence := "medium"
	if contractAccepted && len(evidencePaths) > 0 && analysisReplyGrounded(reply, evidencePaths) {
		confidence = "high"
	}
	if lowReason, low := lowConfidenceReadinessReason(reply); low {
		confidence = "low"
		reason = lowReason
	}
	if !contractAccepted {
		reason = "summary does not satisfy lane contract or evidence gate"
	}
	ready := contractAccepted && confidence != "low"
	action := "answer"
	if !ready {
		action = strings.TrimSpace(notReadyAction)
		if action == "" {
			action = "retry"
		}
	}
	return analysisReadinessAssessment{
		Lane:          string(lane),
		Ready:         ready,
		Confidence:    confidence,
		Action:        action,
		Reason:        reason,
		EvidencePaths: evidencePaths,
	}
}

func analysisReadinessAssessmentForInvalidReply(lane analysisLane, action string, reason string) analysisReadinessAssessment {
	action = strings.TrimSpace(action)
	if action == "" {
		action = "retry"
	}
	return analysisReadinessAssessment{
		Lane:       string(lane),
		Ready:      false,
		Confidence: "low",
		Action:     action,
		Reason:     strings.TrimSpace(reason),
	}
}

func claimConfidenceFromReadiness(readiness analysisReadinessAssessment) claimConfidenceSnapshot {
	unresolved := make([]string, 0, 2)
	if !readiness.Ready && strings.TrimSpace(readiness.Reason) != "" {
		unresolved = append(unresolved, readiness.Reason)
	}
	supportedClaims := 0
	if readiness.Ready {
		supportedClaims = 1
	}
	basis := "contract acceptance plus grounded evidence path coverage"
	if len(readiness.EvidencePaths) == 0 {
		basis = "contract acceptance without explicit evidence paths"
	}
	return claimConfidenceSnapshot{
		Lane:                  readiness.Lane,
		Confidence:            readiness.Confidence,
		SupportedClaims:       supportedClaims,
		EvidencePaths:         uniqueNonEmptyStrings(readiness.EvidencePaths),
		UnresolvedUncertainty: uniqueNonEmptyStrings(unresolved),
		CalibrationBasis:      basis,
	}
}

func analysisBudgetNextActions(stopReason string) []string {
	stopReason = strings.ToLower(strings.TrimSpace(stopReason))
	switch {
	case strings.Contains(stopReason, "ready"):
		return []string{"answer"}
	case strings.Contains(stopReason, "fallback"), strings.Contains(stopReason, "exhausted"):
		return []string{"answer_with_explicit_uncertainty", "persist_task_memory_if_needed"}
	default:
		return []string{"retry_with_more_evidence", "fallback_if_budget_exhausted"}
	}
}

func uncertaintyForStopReason(stopReason string) string {
	stopReason = strings.ToLower(strings.TrimSpace(stopReason))
	switch {
	case strings.Contains(stopReason, "fallback"), strings.Contains(stopReason, "exhausted"):
		return "state that bounded readiness budget was exhausted and identify unresolved gaps"
	case strings.Contains(stopReason, "no_usable"):
		return "state that no usable lane-conformant answer was produced"
	default:
		return ""
	}
}

func shouldDeepenAfterReadiness(readiness analysisReadinessAssessment) bool {
	if readiness.Ready {
		return false
	}
	action := strings.ToLower(strings.TrimSpace(readiness.Action))
	return action == "" || action == "retry"
}

func lowConfidenceReadinessReason(reply string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	switch {
	case containsAny(normalized, "not confident", "not enough evidence", "insufficient evidence", "insufficient context"):
		return "response explicitly reports insufficient evidence", true
	case containsAny(normalized, "can't determine", "cannot determine", "unable to determine", "unable to confirm", "cannot confirm"):
		return "response cannot determine or confirm the claim", true
	case containsAny(normalized, "need more evidence", "need to inspect", "would need to inspect", "would need more"):
		return "response asks for more evidence before it is ready", true
	default:
		return "", false
	}
}

func (m *Model) logAnalysisReadiness(readiness analysisReadinessAssessment) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(readiness)
	if err != nil {
		return
	}
	m.logMessage("readiness_check", string(data))
}

func (m *Model) logAnalysisDeepening(deepening analysisDeepeningResult) {
	if m == nil || m.debugLogger == nil {
		return
	}
	payload := struct {
		Lane          string   `json:"lane,omitempty"`
		Attempt       int      `json:"attempt,omitempty"`
		Attempted     bool     `json:"attempted"`
		Exhausted     bool     `json:"exhausted,omitempty"`
		Action        string   `json:"action,omitempty"`
		Reason        string   `json:"reason,omitempty"`
		EvidencePaths []string `json:"evidence_paths,omitempty"`
		NewPaths      []string `json:"new_paths,omitempty"`
		ToolResults   int      `json:"tool_results,omitempty"`
		FailedTools   int      `json:"failed_tools,omitempty"`
	}{
		Lane:          strings.TrimSpace(deepening.Lane),
		Attempt:       deepening.Attempt,
		Attempted:     deepening.Attempted,
		Exhausted:     deepening.Exhausted,
		Action:        strings.TrimSpace(deepening.Action),
		Reason:        strings.TrimSpace(deepening.Reason),
		EvidencePaths: uniqueNonEmptyStrings(deepening.EvidencePaths),
		NewPaths:      uniqueNonEmptyStrings(deepening.NewPaths),
		ToolResults:   deepening.ToolResults,
		FailedTools:   deepening.FailedTools,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("deepening_attempt", string(data))
}

func analysisReplyGrounded(reply string, requiredPaths []string) bool {
	if len(requiredPaths) == 0 {
		return true
	}
	normalized := strings.ToLower(reply)
	for _, path := range requiredPaths {
		path = strings.ToLower(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if strings.Contains(normalized, path) {
			return true
		}
	}
	return false
}

func analysisEvidencePaths(requiredPaths []string, ledger evidenceLedger) []string {
	paths := make([]string, 0, len(requiredPaths)+len(ledger.VerifiedPaths))
	paths = append(paths, requiredPaths...)
	paths = append(paths, ledger.VerifiedPaths...)
	return uniqueLimitedStrings(paths, 12)
}

func analysisReplyAcceptedWithEvidenceLedger(reply string, requiredPaths []string, ledger evidenceLedger, lane analysisLane) bool {
	return analysisReplyAccepted(reply, analysisEvidencePaths(requiredPaths, ledger), lane)
}

func analysisReplyAccepted(reply string, requiredPaths []string, lane analysisLane) bool {
	if !analysisReplyGrounded(reply, requiredPaths) {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(reply))
	switch lane {
	case analysisLaneArchitecture:
		return containsAny(
			normalized,
			"architecture",
			"structur",
			"boundary",
			"boundaries",
			"modular",
			"responsibilit",
			"opportunit",
			"coupling",
			"ownership",
			"refactor",
			"split",
			"decompose",
			"extension",
			"extensible",
		)
	case analysisLaneReview:
		return containsAny(
			normalized,
			"issue",
			"finding",
			"cleanup",
			"bug",
			"regression",
			"because",
			"evidence",
			"confidence",
			"next step",
		)
	case analysisLaneExplore:
		return containsAny(
			normalized,
			"verified",
			"confirmed",
			"evidence",
			"observed",
			"found",
			"read",
			"from ",
			"grounded",
		) && containsAny(
			normalized,
			"inferred",
			"inference",
			"likely",
			"appears",
			"uncertain",
			"confidence",
			"verified",
			"confirmed",
		)
	case analysisLaneLocate:
		return containsAny(normalized, "best target", "exact file path", "snippet", "line number") &&
			containsAny(normalized, "why", "reason", "region", "best target")
	default:
		return true
	}
}

func analysisSummaryLogKey(lane analysisLane) string {
	switch lane {
	case analysisLaneArchitecture:
		return "architecture_summary_rejected"
	case analysisLaneReview:
		return "review_summary_rejected"
	case analysisLaneExplore:
		return "explore_summary_rejected"
	default:
		return "analysis_summary_rejected"
	}
}

func analysisRetryRequirement(lane analysisLane, requiredPaths []string) string {
	subject := "analysis"
	pathLabel := "relevant"
	switch lane {
	case analysisLaneArchitecture:
		subject = "architecture"
		pathLabel = "hotspot"
	case analysisLaneReview:
		subject = "review"
		pathLabel = "candidate"
	case analysisLaneExplore:
		subject = "explore"
		pathLabel = "evidence"
	}
	requirement := "Do not emit tool requests in this lane. Reply now with a concise, non-empty " + subject + " summary based only on the provided evidence."
	if len(requiredPaths) > 0 {
		requirement += " Mention at least one exact " + pathLabel + " path from this set: " + strings.Join(requiredPaths, ", ") + "."
	}
	requirement += " The response is ready only if it states evidence-backed claims; do not present a low-confidence or insufficient-evidence answer as final."
	switch lane {
	case analysisLaneArchitecture:
		requirement += " Focus on structure, boundaries, extension points, or refactor direction rather than bug triage."
	case analysisLaneReview:
		requirement += " Prefer one concrete issue or cleanup candidate over a broad structural sweep."
	case analysisLaneExplore:
		requirement += " Include the major files or packages involved, and explicitly separate what was verified from what is inferred."
	}
	return requirement
}

type evidenceLedger struct {
	VerifiedPaths []string
	Tools         []string
	Errors        []string
	InferredGaps  []string
}

type languageCapability struct {
	Language string
	Signals  []string
	Tools    []string
}

func detectLanguageCapabilities(root string) []languageCapability {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	seenSignals := map[string]map[string]struct{}{
		"go":      {},
		"cpp":     {},
		"shell":   {},
		"config":  {},
		"docs":    {},
		"prompts": {},
		"tests":   {},
	}
	addSignal := func(key string, signal string) {
		if signal == "" {
			return
		}
		if _, ok := seenSignals[key]; !ok {
			seenSignals[key] = make(map[string]struct{})
		}
		seenSignals[key][signal] = struct{}{}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		addSignal("go", "go.mod")
	}
	visited := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if visited > 500 {
			return filepath.SkipAll
		}
		name := entry.Name()
		if entry.IsDir() {
			switch name {
			case ".git", "node_modules", "logs", "models", "tmp", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(name))
		if strings.HasPrefix(rel, "prompts/") {
			addSignal("prompts", rel)
		}
		if strings.HasPrefix(rel, "tests/") || strings.Contains(rel, "/tests/") || strings.HasSuffix(name, "_test.go") {
			addSignal("tests", rel)
		}
		switch ext {
		case ".go":
			addSignal("go", rel)
		case ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hh", ".hxx":
			addSignal("cpp", rel)
		case ".sh", ".bash", ".zsh":
			addSignal("shell", rel)
		case ".json", ".toml", ".yaml", ".yml":
			addSignal("config", rel)
		case ".md", ".mdx", ".rst":
			addSignal("docs", rel)
		}
		return nil
	})
	capabilities := make([]languageCapability, 0, 4)
	addCapability := func(key string, language string, tools []string) {
		signals := sortedStringKeys(seenSignals[key])
		if len(signals) == 0 {
			return
		}
		capabilities = append(capabilities, languageCapability{
			Language: language,
			Signals:  uniqueLimitedStrings(signals, 6),
			Tools:    append([]string(nil), tools...),
		})
	}
	addCapability("go", "go", []string{"go_overview", "go_find_symbol", "go_read_symbol", "go_diagnostics", "go_importers"})
	addCapability("cpp", "c/c++", []string{"cpp_overview", "cpp_find_symbol", "cpp_read_symbol", "clangd diagnostics"})
	addCapability("shell", "shell", []string{"search_content", "read_file", "shell syntax checks"})
	addCapability("config", "config", []string{"read_file", "structured JSON/TOML edits", "targeted validation"})
	addCapability("docs", "docs", []string{"search_content", "read_file", "markdown-aware edits"})
	addCapability("prompts", "prompts", []string{"read_file", "prompt profile inspection", "route contract validation"})
	addCapability("tests", "tests", []string{"go test", "shell test runners", "targeted regression checks"})
	return capabilities
}

func renderLanguageCapabilityRegistry(capabilities []languageCapability) string {
	if len(capabilities) == 0 {
		return ""
	}
	lines := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		line := "- " + capability.Language
		if len(capability.Tools) > 0 {
			line += " tools: " + strings.Join(capability.Tools, ", ")
		}
		if len(capability.Signals) > 0 {
			line += " | signals: " + strings.Join(uniqueLimitedStrings(capability.Signals, 4), ", ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func appendLanguageCapabilityRegistry(profile string, root string) string {
	registry := renderLanguageCapabilityRegistry(detectLanguageCapabilities(root))
	if strings.TrimSpace(registry) == "" {
		return profile
	}
	if strings.TrimSpace(profile) == "" {
		return "Language capability registry:\n" + registry
	}
	return profile + "\n\nLanguage capability registry:\n" + registry
}

func buildEvidenceLedger(prompt string, results []workspace.ToolResult) evidenceLedger {
	toolsSeen := make(map[string]struct{})
	tools := make([]string, 0, 6)
	errors := make([]string, 0, 3)
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool != "" {
			if _, ok := toolsSeen[tool]; !ok {
				toolsSeen[tool] = struct{}{}
				tools = append(tools, tool)
			}
		}
		if errText := strings.TrimSpace(result.Error); errText != "" {
			errors = append(errors, truncateText(errText, 140))
		}
	}
	ledger := evidenceLedger{
		VerifiedPaths: exploreEvidencePaths(results),
		Tools:         uniqueLimitedStrings(tools, 8),
		Errors:        uniqueLimitedStrings(errors, 4),
	}
	switch {
	case len(ledger.VerifiedPaths) == 0:
		ledger.InferredGaps = append(ledger.InferredGaps, "no concrete source path was captured in deterministic evidence")
	case strings.TrimSpace(prompt) != "":
		ledger.InferredGaps = append(ledger.InferredGaps, "relationships between cited files still need targeted symbol or call-flow validation")
	default:
		ledger.InferredGaps = append(ledger.InferredGaps, "implementation should verify the exact target before editing")
	}
	return ledger
}

func deterministicEvidenceLedger(prompt string, results []workspace.ToolResult, seedPaths []string, toolNames ...string) evidenceLedger {
	ledger := buildEvidenceLedger(prompt, results)
	if len(seedPaths) > 0 {
		ledger.VerifiedPaths = uniqueLimitedStrings(append(append([]string(nil), ledger.VerifiedPaths...), seedPaths...), 12)
	}
	if len(toolNames) > 0 {
		ledger.Tools = uniqueLimitedStrings(append(append([]string(nil), ledger.Tools...), toolNames...), 8)
	}
	if len(ledger.VerifiedPaths) > 0 && len(ledger.InferredGaps) > 0 {
		filtered := make([]string, 0, len(ledger.InferredGaps))
		for _, gap := range ledger.InferredGaps {
			if strings.Contains(strings.ToLower(gap), "no concrete source path") {
				continue
			}
			filtered = append(filtered, gap)
		}
		ledger.InferredGaps = filtered
	}
	if len(ledger.InferredGaps) == 0 {
		ledger.InferredGaps = append(ledger.InferredGaps, "relationships between cited files still need targeted symbol or call-flow validation")
	}
	return ledger
}

func mergeEvidenceLedgers(base evidenceLedger, extra evidenceLedger) evidenceLedger {
	return evidenceLedger{
		VerifiedPaths: uniqueLimitedStrings(append(append([]string(nil), base.VerifiedPaths...), extra.VerifiedPaths...), 16),
		Tools:         uniqueLimitedStrings(append(append([]string(nil), base.Tools...), extra.Tools...), 12),
		Errors:        uniqueLimitedStrings(append(append([]string(nil), base.Errors...), extra.Errors...), 6),
		InferredGaps:  uniqueLimitedStrings(append(append([]string(nil), base.InferredGaps...), extra.InferredGaps...), 8),
	}
}

func evidenceLedgerHasContent(ledger evidenceLedger) bool {
	return len(ledger.VerifiedPaths) > 0 || len(ledger.Tools) > 0 || len(ledger.Errors) > 0 || len(ledger.InferredGaps) > 0
}

func renderEvidenceLedger(ledger evidenceLedger) string {
	var b strings.Builder
	b.WriteString("Evidence ledger:\n")
	if len(ledger.VerifiedPaths) > 0 {
		b.WriteString("- verified paths: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.VerifiedPaths, 8), ", "))
		b.WriteString("\n")
	} else {
		b.WriteString("- verified paths: none captured\n")
	}
	if len(ledger.Tools) > 0 {
		b.WriteString("- tools: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.Tools, 8), ", "))
		b.WriteString("\n")
	}
	if len(ledger.Errors) > 0 {
		b.WriteString("- tool errors: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.Errors, 3), " | "))
		b.WriteString("\n")
	}
	if len(ledger.InferredGaps) > 0 {
		b.WriteString("- inferred gaps: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.InferredGaps, 3), " | "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func exploreEvidencePaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 8)
	add := func(path string) {
		path = normalizeGuardedPath(path)
		if path == "" || path == "." {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, result := range results {
		for _, path := range promptMentionedWorkspacePaths(result.Summary) {
			add(path)
		}
		switch result.Tool {
		case "list_files", "search_files":
			for _, path := range parseDiscoveredFileList(result.Output) {
				add(path)
			}
		case "search_content", "rg":
			for _, path := range parseSearchMatchPaths(result.Output) {
				add(path)
			}
		case "go_entrypoints", "go_find_symbol", "go_find_function", "go_find_type", "go_related_files", "go_importers":
			for _, path := range parseDiscoveredFileList(result.Output) {
				add(path)
			}
			for _, path := range parseSearchMatchPaths(result.Output) {
				add(path)
			}
		}
		if len(paths) >= 12 {
			break
		}
	}
	return uniqueLimitedStrings(paths, 12)
}

func buildExploreFallbackSummary(prompt string, bootstrap exploreBootstrap) string {
	ledger := bootstrap.ledger
	if !evidenceLedgerHasContent(ledger) {
		ledger = buildEvidenceLedger(prompt, bootstrap.results)
	}
	paths := ledger.VerifiedPaths
	var b strings.Builder
	b.WriteString("Grounded exploration summary.\n\n")
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("Request:\n")
		b.WriteString("- ")
		b.WriteString(truncateText(strings.TrimSpace(prompt), 180))
		b.WriteString("\n\n")
	}
	if rendered := renderEvidenceLedger(ledger); rendered != "" {
		b.WriteString(rendered)
		b.WriteString("\n\n")
	}
	b.WriteString("Verified evidence:\n")
	if len(paths) == 0 {
		b.WriteString("- The deterministic explore bootstrap ran, but it did not expose a concrete source path in the captured evidence.\n")
	} else {
		for _, path := range uniqueLimitedStrings(paths, 6) {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString(" was discovered or inspected during explore bootstrap.\n")
		}
	}
	b.WriteString("\nInference:\n")
	if len(paths) == 0 {
		b.WriteString("- Any code relationship beyond the bootstrap result needs another targeted search or read before implementation.\n")
	} else {
		b.WriteString("- Treat relationships between these files as inferred until a targeted read or symbol query confirms the exact call flow.\n")
	}
	b.WriteString("\nNext validation:\n")
	if len(paths) > 0 {
		b.WriteString("- Open the most relevant listed path and verify the specific symbol or behavior before making changes.")
	} else {
		b.WriteString("- Run a narrower search for the user-requested term, symbol, or behavior before making changes.")
	}
	return strings.TrimSpace(b.String())
}

func (m *Model) currentExploreCache(exploreWorkspace *workspace.Manager) (*exploreSessionCache, bool) {
	if m.exploreCache == nil {
		return nil, false
	}
	if m.exploreCache.workspaceRoot != exploreWorkspace.Root() || m.exploreCache.workspaceCWD != exploreWorkspace.CWD() {
		return nil, false
	}
	if token := currentWorkspaceStateToken(exploreWorkspace.Root()); token != "" && token != m.exploreCache.stateToken {
		return nil, false
	}
	return m.exploreCache, true
}

func (m *Model) setExploreCache(
	exploreWorkspace *workspace.Manager,
	repoProfile string,
	discoveredFiles map[string]struct{},
	discoveredDirs map[string]struct{},
	goIndex *semantic.GoIndex,
) {
	m.exploreCache = &exploreSessionCache{
		workspaceRoot: exploreWorkspace.Root(),
		workspaceCWD:  exploreWorkspace.CWD(),
		stateToken:    currentWorkspaceStateToken(exploreWorkspace.Root()),
		repoProfile:   strings.TrimSpace(repoProfile),
		discovered:    sortedStringKeys(discoveredFiles),
		directories:   sortedStringKeys(discoveredDirs),
		goIndex:       goIndex,
	}
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

func bootstrapRawToolRequest(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return req.Tool
	}
	return string(data)
}

func parseDiscoveredPaths(output string) (map[string]string, map[string]struct{}) {
	files := make(map[string]string)
	dirs := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		path, handle, isDir := parseDiscoveredPathLine(line)
		if path == "" {
			continue
		}
		if isDir {
			dirs[path] = struct{}{}
			continue
		}
		files[path] = handle
	}
	return files, dirs
}

func parseDiscoveredFileList(output string) []string {
	files, _ := parseDiscoveredPaths(output)
	return sortedStringMapKeys(files)
}

func parseDiscoveredPathLine(line string) (path string, handle string, isDir bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	if parts := strings.SplitN(line, " ", 2); len(parts) == 2 && looksLikeHandle(parts[0]) {
		handle = parts[0]
		path = strings.TrimSpace(parts[1])
	} else {
		path = line
	}

	if strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/"), handle, true
	}
	return path, handle, false
}

func firstHandleFromOutput(output string) string {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		_, handle, isDir := parseDiscoveredPathLine(line)
		if handle != "" && !isDir {
			return handle
		}
	}
	return ""
}

func parseSearchMatchPaths(output string) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || !looksLikeHandle(parts[0]) {
			continue
		}
		pathParts := strings.SplitN(parts[1], ":", 3)
		if len(pathParts) < 2 {
			continue
		}
		path := strings.TrimSpace(pathParts[0])
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func parseSearchMatches(output string) []searchMatch {
	matches := make([]searchMatch, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || !looksLikeHandle(parts[0]) {
			continue
		}
		pathParts := strings.SplitN(parts[1], ":", 3)
		if len(pathParts) < 3 {
			continue
		}
		lineNumber, err := strconv.Atoi(strings.TrimSpace(pathParts[1]))
		if err != nil {
			continue
		}
		match := searchMatch{
			Path: strings.TrimSpace(pathParts[0]),
			Line: lineNumber,
			Text: strings.TrimSpace(pathParts[2]),
		}
		if match.Path == "" {
			continue
		}
		matches = append(matches, match)
	}
	return matches
}

func looksLikeHandle(text string) bool {
	if len(text) < 2 || text[0] != 'f' {
		return false
	}
	for _, ch := range text[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func hasDirectory(dirs map[string]struct{}, name string) bool {
	_, ok := dirs[name]
	return ok
}

func buildExploreRepoProfile(overview string, results []workspace.ToolResult) string {
	var b strings.Builder
	b.WriteString("Top-level layout:\n")
	for _, line := range strings.Split(compactOutput(strings.TrimSpace(overview), 10), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	count := 0
	for _, result := range results {
		if result.Error != "" {
			continue
		}
		switch result.Tool {
		case "read_file", "search_content", "search_files":
		default:
			continue
		}
		if count == 0 {
			b.WriteString("\nKey evidence:\n")
		}
		b.WriteString("- ")
		b.WriteString(result.Summary)
		if output := compactOutput(result.Output, 4); output != "" {
			b.WriteString("\n")
			b.WriteString(output)
		}
		b.WriteString("\n")
		count++
		if count >= 4 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func compactOutput(output string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

func rankReviewCandidates(prompt string, goIndex *semantic.GoIndex, matches []searchMatch, limit int) []reviewCandidate {
	if limit == 0 {
		return nil
	}

	candidateMap := make(map[string]*reviewCandidate)
	ensureCandidate := func(path string) *reviewCandidate {
		path = strings.TrimSpace(path)
		if path == "" {
			return nil
		}
		if existing, ok := candidateMap[path]; ok {
			return existing
		}
		candidate := &reviewCandidate{Path: path}
		candidateMap[path] = candidate
		return candidate
	}
	addReason := func(candidate *reviewCandidate, reason string, score int) {
		if candidate == nil || strings.TrimSpace(reason) == "" {
			return
		}
		candidate.Score += score
		if !slices.Contains(candidate.Reasons, reason) {
			candidate.Reasons = append(candidate.Reasons, reason)
		}
	}

	matchCounts := make(map[string]int)
	for _, match := range matches {
		if !isMeaningfulReviewHit(match) {
			continue
		}
		candidate := ensureCandidate(match.Path)
		if candidate == nil {
			continue
		}
		candidate.Hits = append(candidate.Hits, match)
		matchCounts[match.Path]++
	}
	for path, count := range matchCounts {
		candidate := ensureCandidate(path)
		addReason(candidate, fmt.Sprintf("%d issue-marker hit(s)", count), 14+count*4)
	}

	if goIndex != nil {
		semanticCandidates := goIndex.IssueHuntFiles(limit * 2)
		semanticReason := "high-complexity Go hot path"
		if looksLikeCodeSmellPrompt(prompt) {
			semanticCandidates = goIndex.SmellHuntFiles(limit * 2)
			semanticReason = "high-complexity cleanup target"
		}
		for rank, path := range semanticCandidates {
			candidate := ensureCandidate(path)
			addReason(candidate, semanticReason, maxInt(6, 18-rank*2))
		}
		for rank, path := range goIndex.DefaultFiles(limit) {
			candidate := ensureCandidate(path)
			addReason(candidate, "key entrypoint or anchor file", maxInt(2, 8-rank))
		}
		for _, candidate := range candidateMap {
			file := goIndex.FileByPath(candidate.Path)
			if file == nil {
				continue
			}
			surface := file.Funcs + file.Methods
			switch {
			case surface >= 120:
				addReason(candidate, "very large method/function surface", 12)
			case surface >= 80:
				addReason(candidate, "large method/function surface", 8)
			case surface >= 40:
				addReason(candidate, "moderate method/function surface", 4)
			}
			if file.Types >= 8 {
				addReason(candidate, "many types in one file", 4)
			}
			if file.IsTest {
				candidate.Score -= 20
			}
		}
	}

	normalized := strings.ToLower(prompt)
	for _, candidate := range candidateMap {
		pathLower := strings.ToLower(candidate.Path)
		switch {
		case containsAny(pathLower, "manager", "runner", "controller", "client", "server", "app"):
			addReason(candidate, "central orchestration file", 4)
		case containsAny(pathLower, "config", "main"):
			addReason(candidate, "high-impact entry/config path", 2)
		}
		if containsAny(normalized, "code smell", "cleanup", "clean up", "refactor") && containsAny(pathLower, "manager", "app", "runner") {
			addReason(candidate, "likely cleanup target", 6)
		}
		if containsAny(normalized, "bug", "error", "issue", "problem") && len(candidate.Hits) > 0 {
			addReason(candidate, "bug-oriented markers present", 4)
		}
	}

	candidates := make([]reviewCandidate, 0, len(candidateMap))
	for _, candidate := range candidateMap {
		if candidate.Score <= 0 {
			continue
		}
		sort.Slice(candidate.Hits, func(i, j int) bool {
			if candidate.Hits[i].Path != candidate.Hits[j].Path {
				return candidate.Hits[i].Path < candidate.Hits[j].Path
			}
			return candidate.Hits[i].Line < candidate.Hits[j].Line
		})
		candidates = append(candidates, *candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Path < candidates[j].Path
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func reviewCouncilCandidates(prompt string, goIndex *semantic.GoIndex, candidates []reviewCandidate, limit int) []reviewCandidate {
	if len(candidates) == 0 || limit == 0 {
		return nil
	}

	issueHotPaths := make(map[string]struct{})
	defaultHotPaths := make(map[string]struct{})
	if goIndex != nil {
		semanticPaths := goIndex.IssueHuntFiles(limit * 3)
		if looksLikeCodeSmellPrompt(prompt) {
			semanticPaths = goIndex.SmellHuntFiles(limit * 3)
		}
		for _, path := range semanticPaths {
			issueHotPaths[path] = struct{}{}
		}
		for _, path := range goIndex.DefaultFiles(limit * 2) {
			defaultHotPaths[path] = struct{}{}
		}
	}

	filtered := make([]reviewCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		signals := make([]string, 0, 3)
		if len(candidate.Hits) > 0 {
			signals = append(signals, "markers")
		}
		if reviewHasSemanticSupport(goIndex, candidate.Path, issueHotPaths, defaultHotPaths) {
			signals = append(signals, "semantic")
		}
		if reviewPathRoleScore(prompt, candidate.Path) > 0 {
			signals = append(signals, "path-role")
		}
		candidate.CouncilVotes = len(signals)
		candidate.CouncilSignals = signals
		if candidate.CouncilVotes >= 2 {
			filtered = append(filtered, candidate)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CouncilVotes != filtered[j].CouncilVotes {
			return filtered[i].CouncilVotes > filtered[j].CouncilVotes
		}
		if filtered[i].Score != filtered[j].Score {
			return filtered[i].Score > filtered[j].Score
		}
		return filtered[i].Path < filtered[j].Path
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

func confirmReviewFinding(prompt string, goIndex *semantic.GoIndex, candidates []reviewCandidate) *reviewFinding {
	if goIndex == nil || len(candidates) == 0 {
		return nil
	}

	for _, candidate := range candidates {
		finding := confirmCandidateFinding(prompt, goIndex, candidate)
		if finding != nil {
			return finding
		}
	}
	return nil
}

func confirmCandidateFinding(prompt string, goIndex *semantic.GoIndex, candidate reviewCandidate) *reviewFinding {
	file := goIndex.FileByPath(candidate.Path)
	if file == nil {
		return nil
	}

	surface := file.Funcs + file.Methods
	exported := exportedSymbolCountInFile(file)
	longest := longestSymbolInFile(file)
	longestSpan := symbolSpan(longest)
	isSmellPrompt := looksLikeCodeSmellPrompt(prompt)

	if isSmellPrompt {
		switch {
		case surface >= 160 || (surface >= 120 && file.Types >= 12):
			evidence := []string{
				fmt.Sprintf("%s contains %d funcs/methods and %d types in a single file", candidate.Path, surface, file.Types),
			}
			if longest != nil && longestSpan >= 60 {
				evidence = append(evidence, fmt.Sprintf("%s spans lines %d-%d (%d lines)", reviewSymbolDisplayName(*longest), longest.StartLine, longest.EndLine, longestSpan))
			}
			if len(file.Imports) >= 18 {
				evidence = append(evidence, fmt.Sprintf("the file also imports %d packages", len(file.Imports)))
			}
			return &reviewFinding{
				Title:      "Overgrown source file",
				Path:       candidate.Path,
				Reason:     "This file is carrying too many responsibilities and is likely harder to maintain or change safely.",
				Evidence:   evidence,
				Confidence: "high",
				Scope:      "large",
				Plan: []string{
					"Identify one cohesive slice inside the file, starting with the longest helper cluster or bootstrap path.",
					"Extract that slice behind a narrow helper type or new package-level module without changing outward behavior.",
					"Move callers over incrementally and keep the remaining file focused on orchestration only.",
					"Verify the extracted seam with a focused build/test pass before attempting a second split.",
				},
				NextStep: "Review the split plan, choose the first extraction seam, and then implement that slice explicitly.",
			}
		case longest != nil && longestSpan >= 110:
			return &reviewFinding{
				Title:  "Oversized function body",
				Path:   candidate.Path,
				Symbol: reviewSymbolDisplayName(*longest),
				Reason: "The function body is unusually large and likely mixes orchestration with lower-level details.",
				Evidence: []string{
					fmt.Sprintf("%s spans lines %d-%d (%d lines)", reviewSymbolDisplayName(*longest), longest.StartLine, longest.EndLine, longestSpan),
					fmt.Sprintf("%s still lives inside a file with %d funcs/methods", candidate.Path, surface),
				},
				Confidence: "high",
				Scope:      "medium",
				Plan: []string{
					"Split the function into one or two helpers around the dominant sub-steps already visible in the body.",
					"Keep the public entrypoint intact and move only internal logic on the first pass.",
					"Re-run the focused build/test path after the extraction.",
				},
				NextStep: "If you want this fixed now, extract one helper seam first rather than rewriting the whole function.",
			}
		case exported >= 28 && surface >= 90:
			return &reviewFinding{
				Title:  "Excessive exported surface",
				Path:   candidate.Path,
				Reason: "This file exposes a very large public surface, which usually signals muddled responsibilities or weak boundaries.",
				Evidence: []string{
					fmt.Sprintf("%s exposes %d exported symbols", candidate.Path, exported),
					fmt.Sprintf("the same file also contains %d funcs/methods", surface),
				},
				Confidence: "medium",
				Scope:      "medium",
				Plan: []string{
					"Identify which exported symbols are truly part of the file's public contract.",
					"Move unrelated helpers or internal-only symbols behind narrower files or unexport them where possible.",
					"Re-check downstream callers after shrinking the surface.",
				},
				NextStep: "Narrow the public API before adding more behavior to this file.",
			}
		}
	}

	if len(candidate.Hits) > 0 {
		hit := firstNonMetaHit(candidate.Hits)
		if hit != nil {
			return &reviewFinding{
				Title:      "Suspicious marker-backed issue",
				Path:       candidate.Path,
				Reason:     "This file contains an explicit marker or panic/error pattern that should be reviewed before more work lands nearby.",
				Evidence:   []string{fmt.Sprintf("line %d: %s", hit.Line, truncateText(hit.Text, 140))},
				Confidence: "medium",
				Scope:      "small",
				NextStep:   "Inspect the cited line and decide whether it reflects dead code, a real bug path, or cleanup debt that should be resolved now.",
			}
		}
	}

	return nil
}

func formatReviewFinding(finding reviewFinding) string {
	var b strings.Builder
	b.WriteString("Finding: ")
	b.WriteString(strings.TrimSpace(finding.Title))
	b.WriteString("\n")
	b.WriteString("File: ")
	b.WriteString(strings.TrimSpace(finding.Path))
	b.WriteString("\n")
	if strings.TrimSpace(finding.Symbol) != "" {
		b.WriteString("Symbol: ")
		b.WriteString(strings.TrimSpace(finding.Symbol))
		b.WriteString("\n")
	}
	b.WriteString("Why it matters: ")
	b.WriteString(strings.TrimSpace(finding.Reason))
	b.WriteString("\n")
	if len(finding.Evidence) > 0 {
		b.WriteString("Evidence:\n")
		for _, line := range finding.Evidence {
			if strings.TrimSpace(line) == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(line))
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(finding.Confidence) != "" {
		b.WriteString("Confidence: ")
		b.WriteString(strings.TrimSpace(finding.Confidence))
		b.WriteString("\n")
	}
	if strings.TrimSpace(finding.Scope) != "" {
		b.WriteString("Scope: ")
		b.WriteString(strings.TrimSpace(finding.Scope))
		b.WriteString("\n")
	}
	if len(finding.Plan) > 0 {
		b.WriteString("Refactor plan:\n")
		for i, step := range finding.Plan {
			if strings.TrimSpace(step) == "" {
				continue
			}
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(step))
		}
	}
	if strings.TrimSpace(finding.NextStep) != "" {
		b.WriteString("Recommended next step: ")
		b.WriteString(strings.TrimSpace(finding.NextStep))
	}
	return strings.TrimSpace(b.String())
}

func reviewFindingAllowsAutoApply(finding reviewFinding) bool {
	return strings.EqualFold(strings.TrimSpace(finding.Scope), "small")
}

func longestSymbolInFile(file *semantic.GoFile) *semantic.GoSymbol {
	if file == nil {
		return nil
	}
	var best *semantic.GoSymbol
	bestSpan := 0
	for i := range file.Symbols {
		symbol := &file.Symbols[i]
		if symbol.Kind != "func" && symbol.Kind != "method" {
			continue
		}
		span := symbolSpan(symbol)
		if span > bestSpan {
			best = symbol
			bestSpan = span
		}
	}
	return best
}

func symbolSpan(symbol *semantic.GoSymbol) int {
	if symbol == nil || symbol.EndLine <= 0 || symbol.StartLine <= 0 || symbol.EndLine < symbol.StartLine {
		return 0
	}
	return symbol.EndLine - symbol.StartLine + 1
}

func exportedSymbolCountInFile(file *semantic.GoFile) int {
	if file == nil {
		return 0
	}
	count := 0
	for _, symbol := range file.Symbols {
		if symbol.Name == "" {
			continue
		}
		r := rune(symbol.Name[0])
		if r >= 'A' && r <= 'Z' {
			count++
		}
	}
	return count
}

func firstNonMetaHit(hits []searchMatch) *searchMatch {
	for i := range hits {
		if isMeaningfulReviewHit(hits[i]) {
			return &hits[i]
		}
	}
	return nil
}

func reviewSymbolDisplayName(symbol semantic.GoSymbol) string {
	if strings.TrimSpace(symbol.Receiver) != "" {
		return symbol.Receiver + "." + symbol.Name
	}
	return symbol.Name
}

func fallbackReviewCandidates(goIndex *semantic.GoIndex, limit int) []reviewCandidate {
	if goIndex == nil || limit == 0 {
		return nil
	}
	paths := uniqueLimitedStrings(append(goIndex.IssueHuntFiles(limit), goIndex.DefaultFiles(limit)...), limit)
	candidates := make([]reviewCandidate, 0, len(paths))
	for _, path := range paths {
		candidate := reviewCandidate{
			Path:    path,
			Score:   1,
			Reasons: []string{"likely high-value source file"},
		}
		if file := goIndex.FileByPath(path); file != nil {
			surface := file.Funcs + file.Methods
			switch {
			case surface >= 120:
				candidate.Score += 12
				candidate.Reasons = append(candidate.Reasons, "very large method/function surface")
			case surface >= 80:
				candidate.Score += 8
				candidate.Reasons = append(candidate.Reasons, "large method/function surface")
			case surface >= 40:
				candidate.Score += 4
				candidate.Reasons = append(candidate.Reasons, "moderate method/function surface")
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func reviewReadWindow(candidate reviewCandidate) (int, int) {
	if len(candidate.Hits) == 0 {
		return 1, 220
	}
	line := candidate.Hits[0].Line
	start := line - 24
	if start < 1 {
		start = 1
	}
	return start, line + 60
}

func buildReviewCandidateBoard(candidates []reviewCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		line := fmt.Sprintf("- %s (score %d)", candidate.Path, candidate.Score)
		if len(candidate.Reasons) > 0 {
			line += ": " + strings.Join(uniqueLimitedStrings(candidate.Reasons, 3), ", ")
		}
		if candidate.CouncilVotes > 0 && len(candidate.CouncilSignals) > 0 {
			line += fmt.Sprintf(" | council %d/3: %s", candidate.CouncilVotes, strings.Join(candidate.CouncilSignals, ", "))
		}
		if hit := firstNonMetaHit(candidate.Hits); hit != nil {
			line += fmt.Sprintf(" | first hit line %d: %s", hit.Line, truncateText(hit.Text, 80))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func buildReviewFallbackSummary(prompt string, candidates []reviewCandidate, candidateBoard string, repoProfile string, note string, ledger evidenceLedger) string {
	var b strings.Builder
	b.WriteString("Deterministic review assessment.\n\n")
	if strings.TrimSpace(note) != "" {
		b.WriteString(strings.TrimSpace(note))
		b.WriteString("\n\n")
	}
	if evidenceLedgerHasContent(ledger) {
		b.WriteString(renderEvidenceLedger(ledger))
		b.WriteString("\n\n")
	}
	b.WriteString("Confidence:\n")
	if len(candidates) > 0 {
		b.WriteString("- medium-high: ranked semantic evidence identifies concrete candidate files; validate the top candidate before making changes.\n\n")
	} else {
		b.WriteString("- medium: repository evidence was gathered, but no single review candidate dominated.\n\n")
	}
	if len(candidates) > 0 {
		top := candidates[0]
		label := "Most likely finding"
		if looksLikeCodeSmellPrompt(prompt) {
			label = "Most likely cleanup target"
		}
		b.WriteString(label)
		b.WriteString(":\n")
		b.WriteString("- ")
		b.WriteString(top.Path)
		if len(top.Reasons) > 0 {
			b.WriteString(" because ")
			b.WriteString(strings.Join(uniqueLimitedStrings(top.Reasons, 3), ", "))
		}
		b.WriteString("\n")
		if hit := firstNonMetaHit(top.Hits); hit != nil {
			b.WriteString(fmt.Sprintf("- first concrete evidence at line %d: %s\n\n", hit.Line, truncateText(hit.Text, 120)))
		} else {
			b.WriteString("- selected from semantic review signals rather than explicit marker hits\n\n")
		}
	}
	if strings.TrimSpace(candidateBoard) != "" {
		b.WriteString("Top ranked candidates:\n")
		b.WriteString(strings.TrimSpace(candidateBoard))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(repoProfile) != "" {
		b.WriteString("Repository profile:\n")
		b.WriteString(compactOutput(repoProfile, 14))
		b.WriteString("\n\n")
	}
	b.WriteString("Recommended next step:\n")
	b.WriteString("- inspect the top ranked candidate and either confirm one concrete finding or ask for a narrower review target")
	return strings.TrimSpace(b.String())
}

func buildArchitectureFallbackSummary(prompt string, hotspots []architectureHotspot, hotspotBoard string, repoProfile string, note string, ledger evidenceLedger) string {
	var b strings.Builder
	b.WriteString("Deterministic architecture assessment.\n\n")
	if strings.TrimSpace(note) != "" {
		b.WriteString(strings.TrimSpace(note))
		b.WriteString("\n\n")
	}
	if evidenceLedgerHasContent(ledger) {
		b.WriteString(renderEvidenceLedger(ledger))
		b.WriteString("\n\n")
	}
	b.WriteString("Confidence:\n")
	switch {
	case len(hotspots) > 0 && strings.TrimSpace(hotspotBoard) != "":
		b.WriteString("- high: Go semantic indexing, hotspot scoring, repository profile, and direct file reads converge on the same structural pressure points.\n\n")
	case len(hotspots) > 0:
		b.WriteString("- medium-high: hotspot scoring found concrete paths, but relationship evidence is still limited.\n\n")
	default:
		b.WriteString("- medium: repository structure was profiled, but no concrete hotspot dominated.\n\n")
	}
	if strings.TrimSpace(repoProfile) != "" {
		b.WriteString("Structural baseline:\n")
		b.WriteString(compactOutput(repoProfile, 14))
		b.WriteString("\n\n")
		if capabilities := architectureCapabilityNames(repoProfile); len(capabilities) > 0 {
			b.WriteString("Detected capability surface:\n")
			b.WriteString("- ")
			b.WriteString(strings.Join(uniqueLimitedStrings(capabilities, 6), ", "))
			b.WriteString("\n\n")
		}
	}
	if len(hotspots) > 0 {
		b.WriteString(architectureOpportunityHeading(repoProfile))
		b.WriteString(":\n")
		for _, hotspot := range hotspots[:minInt(3, len(hotspots))] {
			b.WriteString("- ")
			b.WriteString(hotspot.Path)
			if len(hotspot.Reasons) > 0 {
				b.WriteString(" because ")
				b.WriteString(strings.Join(uniqueLimitedStrings(hotspot.Reasons, 3), ", "))
			}
			b.WriteString("; suggested direction: ")
			b.WriteString(architectureHotspotDirection(hotspot))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(hotspotBoard) != "" {
		b.WriteString("Architecture hotspot board:\n")
		b.WriteString(strings.TrimSpace(hotspotBoard))
		b.WriteString("\n\n")
	}
	if looksLikeArchitecturePrompt(strings.ToLower(prompt)) {
		b.WriteString("Recommended next step:\n")
		b.WriteString("- validate the top hotspot boundaries and split one responsibility at a time instead of attempting a repo-wide refactor")
	} else {
		b.WriteString("Recommended next step:\n")
		b.WriteString("- inspect the top hotspot paths and confirm which structural boundary should change first")
	}
	return strings.TrimSpace(b.String())
}

func architectureOpportunityHeading(repoProfile string) string {
	capabilities := architectureCapabilityNames(repoProfile)
	has := func(name string) bool {
		for _, capability := range capabilities {
			if strings.EqualFold(capability, name) {
				return true
			}
		}
		return false
	}
	switch {
	case has("c/c++") && !has("go"):
		return "Highest-value C/C++ structural opportunities"
	case has("go") && !has("c/c++"):
		return "Highest-value Go structural opportunities"
	case has("prompts") && !has("go") && !has("c/c++"):
		return "Highest-value prompt-system structural opportunities"
	case has("docs") && !has("go") && !has("c/c++"):
		return "Highest-value documentation structural opportunities"
	default:
		return "Highest-value structural opportunities"
	}
}

func architectureCapabilityNames(repoProfile string) []string {
	_, tail, ok := strings.Cut(repoProfile, "Language capability registry:")
	if !ok {
		return nil
	}
	names := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		name := line
		if before, _, ok := strings.Cut(name, " tools:"); ok {
			name = before
		}
		if before, _, ok := strings.Cut(name, "|"); ok {
			name = before
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
}

func issueSearchPattern(prompt string) string {
	normalized := strings.ToLower(prompt)
	switch {
	case containsAny(normalized, "performance", "optimize", "optimization"):
		return "TODO|FIXME|panic\\(|slow|performance|alloc"
	case containsAny(normalized, "cleanup", "clean up", "code smell", "refactor"):
		return "TODO|FIXME|HACK|deprecated"
	case containsAny(normalized, "error", "bug", "issue", "problem", "regression"):
		return "TODO|FIXME|BUG|panic\\(|error|regression"
	default:
		return "TODO|FIXME|BUG|XXX|panic\\("
	}
}

func exploreBootstrapReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 3
	}
}

func exploreKeywordSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 3
	default:
		return 2
	}
}

func exploreKeywordReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessThorough:
		return 2
	default:
		return 1
	}
}

func exploreIssueSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 2
	}
}

func exploreIssueReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessThorough:
		return 2
	default:
		return 1
	}
}

func exploreSemanticDefaultFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 3
	default:
		return 2
	}
}

func exploreSemanticRelevantFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 2
	}
}

func exploreSemanticIssueFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 2
	}
}

func exploreSemanticTotalReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func prioritizedExploreSearchPaths(dirs map[string]struct{}, level workspace.ExploreThoroughness) []string {
	candidates := []string{"internal", "cmd", "pkg", "src", "scripts", "prompts", "tests", "test"}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if hasDirectory(dirs, candidate) {
			paths = append(paths, candidate)
		}
	}
	return uniqueLimitedStrings(paths, exploreSearchPathLimit(level))
}

func exploreSearchGlob(path string) string {
	switch path {
	case "internal", "cmd", "pkg", "src", "test", "tests":
		return "*.go"
	case "prompts":
		return "*.md"
	default:
		return ""
	}
}

func exploreSearchPathLimit(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func reviewSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func reviewCandidateBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func architectureHotspotBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 3
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func architectureReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 3
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func architectureRelevantFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func architectureSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func architectureSearchReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 3
	default:
		return 2
	}
}

func architectureSearchPattern(prompt string) string {
	normalized := strings.ToLower(prompt)
	switch {
	case containsAny(normalized, "language", "languages", "golang", "go", "extensible", "support", "supported"):
		return "GoIndex|semantic|clangd|workspace|language|languages|cpp|C\\+\\+"
	case containsAny(normalized, "structure", "architecture", "modular", "modularity", "boundary", "boundaries", "split", "decompose", "organize", "organization"):
		return "router|lane|workspace|manager|analysis|review|explore|implement|semantic"
	default:
		return ""
	}
}

func architectureSourceDirPriority(dir string) int {
	switch dir {
	case "internal":
		return 35
	case "cmd":
		return 26
	case "pkg":
		return 24
	case "src":
		return 20
	case ".":
		return 10
	default:
		return 14
	}
}

func exploreQueryTerms(prompt string) []string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, prompt)

	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "any": {}, "before": {}, "brief": {}, "can": {}, "cleanup": {},
		"code": {}, "codebase": {}, "deep": {}, "diagnose": {}, "explore": {}, "fast": {}, "file": {},
		"files": {}, "find": {}, "fix": {}, "for": {}, "from": {}, "harness": {}, "identify": {},
		"implement": {}, "inspect": {}, "investigate": {}, "issue": {}, "look": {}, "make": {},
		"optimization": {}, "optimize": {}, "performance": {}, "please": {}, "problem": {}, "project": {},
		"quick": {}, "repo": {}, "repository": {}, "review": {}, "scan": {}, "search": {}, "single": {},
		"test": {}, "the": {}, "this": {}, "that": {}, "through": {}, "thorough": {}, "topic": {}, "what": {}, "wrong": {},
		"bug": {}, "error": {}, "cleanups": {}, "smell": {}, "should": {}, "fixed": {},
	}

	terms := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, field := range strings.Fields(cleaned) {
		if len(field) < 3 {
			continue
		}
		if _, skip := stopwords[field]; skip {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
		if len(terms) >= 4 {
			break
		}
	}
	return terms
}

func sortedStringKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueLimitedStrings(values []string, limit int) []string {
	if limit == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
		if limit > 0 && len(unique) >= limit {
			break
		}
	}
	return unique
}

func appendAnalysisSummary(history []llama.ChatMessage, lane string, summary string) []llama.ChatMessage {
	if strings.TrimSpace(summary) == "" {
		return append([]llama.ChatMessage(nil), history...)
	}

	augmented := append([]llama.ChatMessage(nil), history...)
	augmented = append(augmented, llama.ChatMessage{
		Role: "system",
		Content: "[" + lane + " subagent summary]\n" +
			"Use this as initial orientation, but verify before editing.\n\n" +
			strings.TrimSpace(summary),
	})
	return augmented
}

func buildSubagentSystemPrompt(basePrompt string, workspaceInstructions string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	if prompt := strings.TrimSpace(workspaceInstructions); prompt != "" {
		sections = append(sections, prompt)
	}
	return strings.Join(sections, "\n\n")
}

func buildExploreTaskPrompt(prompt string) string {
	return "Investigate this request in read-only mode and return only a concise exploration summary.\n\n" +
		renderLaneContractPolicy(analysisLaneExplore) + "\n\n" +
		"Required output:\n" +
		"- likely files\n" +
		"- suspected issue or relevant finding\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Investigation rules:\n" +
		"- Do not ask for permission to edit.\n" +
		"- Do not propose broad directory listing loops.\n" +
		"- The harness may already provide an overview and bootstrap evidence. Use that first instead of repeating the same discovery step.\n" +
		"- If a top-level listing shows source directories or concrete files, inspect at least two concrete files before concluding the repo is empty or claiming there is nothing actionable.\n" +
		"- For open-ended bug hunts, prefer targeted content search after the first orientation step, then read concrete source files.\n" +
		"- Ignore repo metadata or generated-output files such as .git state, caches, logs, and model artifacts unless the request is explicitly about them.\n" +
		"- Never send both a file handle id and a path unless they refer to the same discovered file.\n" +
		"- In Go projects, prioritize README.md, go.mod, cmd/, internal/, and scripts/ before less relevant files.\n" +
		"- If you conclude there is no actionable issue, cite the concrete files you inspected.\n" +
		"Use the available tools to inspect, then summarize evidence clearly.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func buildReviewSystemPrompt(basePrompt string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in REVIEW mode.",
		"This is a read-only summary lane. No further tool calls are available.",
		"Base your answer only on the supplied evidence and pick the single best issue or cleanup candidate.",
		"Mention at least one exact candidate file path from the supplied evidence.",
		"Prefer one concrete, defensible finding over a vague sweep of multiple weak ideas.",
	)
	return strings.Join(sections, "\n\n")
}

func buildArchitectureSystemPrompt(basePrompt string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in ARCHITECTURE mode.",
		"This is a read-only structure-analysis lane. No further tool calls are available.",
		"Base your answer only on the supplied evidence and identify how the system is organized, where the main structural pressure points are, and what refactor direction is most defensible.",
		"Mention at least one exact hotspot file path from the supplied evidence.",
		"Prefer a small number of high-confidence structural opportunities over an ungrounded sweep.",
	)
	return strings.Join(sections, "\n\n")
}

func buildReviewTaskPrompt(prompt string) string {
	return "Review this request in read-only mode and return one concrete finding.\n\n" +
		renderLaneContractPolicy(analysisLaneReview) + "\n\n" +
		"Required output:\n" +
		"- finding title\n" +
		"- file or files involved\n" +
		"- why it is a real issue or worthwhile cleanup\n" +
		"- the specific evidence you relied on\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Review rules:\n" +
		"- Do not emit tool requests.\n" +
		"- Do not hedge with a broad list unless the evidence is genuinely inconclusive.\n" +
		"- Cite at least one exact candidate file path from the provided evidence.\n" +
		"- Prefer the strongest single issue supported by the evidence over multiple weak candidates.\n" +
		"- If the evidence is inconclusive, say that directly and identify the best next file to inspect.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func buildArchitectureTaskPrompt(prompt string) string {
	return "Analyze this request in read-only mode and return a grounded architecture summary.\n\n" +
		renderLaneContractPolicy(analysisLaneArchitecture) + "\n\n" +
		"Required output:\n" +
		"- current structural shape or extension path\n" +
		"- top 2-3 structural opportunities or pressure points\n" +
		"- the exact hotspot files involved\n" +
		"- why each hotspot matters\n" +
		"- suggested refactor or boundary direction\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Architecture rules:\n" +
		"- Do not emit tool requests.\n" +
		"- Ground the answer in the repository profile and hotspot evidence provided by the harness.\n" +
		"- Cite at least one exact hotspot file path from the provided evidence.\n" +
		"- Focus on structure, boundaries, ownership, extension points, and coupling instead of bug triage.\n" +
		"- If the evidence is inconclusive, say that directly and identify the next structural hotspot to inspect.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func buildImplementSystemPrompt(basePrompt string, workspaceInstructions string) string {
	sections := make([]string, 0, 3)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	if prompt := strings.TrimSpace(workspaceInstructions); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in IMPLEMENT mode.",
		"Make the smallest correct code change that satisfies the request.",
		"The harness may already provide an implementation target packet with exact files, lines, @file anchors, analogous references, or edit context. Use that first instead of restarting discovery.",
		"Prefer edit_file for localized changes and use start_line/end_line/content when quotes or indentation make exact text replacement brittle.",
		"Do not keep relisting directories once a target file or line is already known. Consume the target packet before any broader search.",
		"Once the edit is complete, answer normally. The harness will run verification automatically after your changes.",
	)
	return strings.Join(sections, "\n\n")
}

func buildImplementTaskPrompt(prompt string) string {
	return "Implement this request with the available workspace tools.\n\n" +
		renderLaneContractPolicy(analysisLaneImplement) + "\n\n" +
		"Implementation rules:\n" +
		"- Make the smallest correct change.\n" +
		"- Prefer edit_file over rewriting the whole file for localized edits.\n" +
		"- If the request names a file, uses @file anchors, or the harness already supplied target lines, do not restart discovery from the repo root.\n" +
		"- Treat analogous reference files as inspiration, not as the default write target.\n" +
		"- For structural refactors, change one bounded responsibility at a time and stay within the target packet files unless verification requires a follow-up.\n" +
		"- Once the required change is applied, answer briefly with what you changed.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func structuredExecutionPlanRules(packet implementationTargetPacket) string {
	lines := []string{
		"- Prefer edit_file.",
		"- Use only paths from the target packet primary files.",
		"- At most 3 operations.",
		"- Use anchored lines, symbol windows, and bootstrapped file excerpts before widening.",
		"- If you cannot propose a safe bounded plan, return {\"summary\":\"\",\"operations\":[]}.",
	}
	if implementationTargetPacketNeedsStructuralPolicy(packet) {
		lines[2] = "- At most 2 operations."
		lines = append(lines,
			"- This is a structural refactor request. Each operation reason must explain the boundary or responsibility change.",
			"- Treat this as the first stage of a structural refactor. Prefer editing only the first primary file, then verify before widening.",
			"- Multi-file operations are allowed only when the structural hints explicitly require a cross-file boundary move or interface extraction.",
			"- Prefer one bounded file split/extraction step over repo-wide rewrites.",
			"- Stay within the files and structural hints in the target packet unless the request explicitly demands a broader refactor.",
		)
	}
	return strings.Join(lines, "\n")
}

func focusedStructuredExecutionPlanRules(packet implementationTargetPacket, path string, windowStart int, windowEnd int) string {
	lines := []string{
		"- Use only edit_file operations.",
		"- Use only path " + path + ".",
		"- All line edits must stay within lines " + strconv.Itoa(windowStart) + "-" + strconv.Itoa(windowEnd) + ".",
		"- Prefer the narrowest possible line-range edit.",
		"- Use old_text/new_text only if that is safer than a line-range edit.",
		"- At most 2 operations.",
		"- If you cannot propose a safe bounded edit, return {\"summary\":\"\",\"operations\":[]}.",
	}
	if implementationTargetPacketNeedsStructuralPolicy(packet) {
		lines = append(lines,
			"- This focused edit is part of a structural refactor. Each operation reason must name the responsibility or boundary being tightened.",
			"- Treat this as the first stage of the structural refactor, not the whole refactor.",
			"- Preserve the rest of the file shape outside this focused boundary change.",
		)
	}
	return strings.Join(lines, "\n")
}

func buildImplementRepairPrompt(prompt string) string {
	return "Repair the already-applied change so verification passes while preserving the user's requested behavior change.\n\n" +
		"Repair rules:\n" +
		"- Do not undo the requested behavior change unless verification proves it is impossible.\n" +
		"- Use the supplied verification errors and recent edit context first.\n" +
		"- Prefer the smallest follow-up edit that restores a clean build/test result.\n\n" +
		"Original request:\n" + strings.TrimSpace(prompt)
}

func exploreToolBudget(maxToolSteps int) int {
	switch {
	case maxToolSteps <= 0:
		return 12
	case maxToolSteps < 6:
		return maxToolSteps
	default:
		return minInt(maxToolSteps, 12)
	}
}

func implementToolBudget(maxToolSteps int) int {
	switch {
	case maxToolSteps <= 0:
		return 14
	case maxToolSteps < 6:
		return maxToolSteps
	default:
		return minInt(maxToolSteps, 14)
	}
}

type effortPolicySnapshot struct {
	Lane                 string `json:"lane,omitempty"`
	Level                string `json:"level,omitempty"`
	MaxToolSteps         int    `json:"max_tool_steps,omitempty"`
	AnalysisToolBudget   int    `json:"analysis_tool_budget,omitempty"`
	ImplementToolBudget  int    `json:"implement_tool_budget,omitempty"`
	DeepeningPasses      int    `json:"deepening_passes,omitempty"`
	TimeBudgetSeconds    int    `json:"time_budget_seconds,omitempty"`
	MaxOutputTokens      int    `json:"max_output_tokens,omitempty"`
	ReasoningBudget      int    `json:"reasoning_budget,omitempty"`
	ConfidenceFloor      string `json:"confidence_floor,omitempty"`
	BudgetExhaustionMode string `json:"budget_exhaustion_mode,omitempty"`
}

type budgetLedgerSnapshot struct {
	Lane                   string   `json:"lane,omitempty"`
	Level                  string   `json:"level,omitempty"`
	UsedModelCalls         int      `json:"used_model_calls,omitempty"`
	UsedDeepeningPasses    int      `json:"used_deepening_passes,omitempty"`
	MaxDeepeningPasses     int      `json:"max_deepening_passes,omitempty"`
	UsedToolResults        int      `json:"used_tool_results,omitempty"`
	NewEvidencePaths       int      `json:"new_evidence_paths,omitempty"`
	TotalEvidencePaths     int      `json:"total_evidence_paths,omitempty"`
	StopReason             string   `json:"stop_reason,omitempty"`
	BudgetExhausted        bool     `json:"budget_exhausted,omitempty"`
	NextAllowedActions     []string `json:"next_allowed_actions,omitempty"`
	MarginalEvidencePolicy string   `json:"marginal_evidence_policy,omitempty"`
	UserVisibleUncertainty string   `json:"user_visible_uncertainty,omitempty"`
}

type evidenceDeltaSnapshot struct {
	Lane             string   `json:"lane,omitempty"`
	Attempt          int      `json:"attempt,omitempty"`
	NewEvidencePaths []string `json:"new_evidence_paths,omitempty"`
	TotalPaths       int      `json:"total_paths,omitempty"`
	ToolResults      int      `json:"tool_results,omitempty"`
	SuccessfulTools  int      `json:"successful_tools,omitempty"`
	FailedTools      int      `json:"failed_tools,omitempty"`
	MarginalValue    string   `json:"marginal_value,omitempty"`
}

type claimConfidenceSnapshot struct {
	Lane                  string   `json:"lane,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	SupportedClaims       int      `json:"supported_claims,omitempty"`
	EvidencePaths         []string `json:"evidence_paths,omitempty"`
	UnresolvedUncertainty []string `json:"unresolved_uncertainty,omitempty"`
	Contradictions        []string `json:"contradictions,omitempty"`
	CalibrationBasis      string   `json:"calibration_basis,omitempty"`
}

type contextCompactionSnapshot struct {
	DroppedMessages  int      `json:"dropped_messages,omitempty"`
	RetainedMessages int      `json:"retained_messages,omitempty"`
	MaxHistory       int      `json:"max_history,omitempty"`
	PinnedPaths      []string `json:"pinned_paths,omitempty"`
	PinnedMemoryKind string   `json:"pinned_memory_kind,omitempty"`
	Strategy         string   `json:"strategy,omitempty"`
}

type routeAmbiguitySnapshot struct {
	SelectedLane string   `json:"selected_lane,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Signals      []string `json:"signals,omitempty"`
	Ambiguous    bool     `json:"ambiguous"`
	TieBreak     string   `json:"tie_break,omitempty"`
}

type verificationFeedbackSnapshot struct {
	RiskLevel             string   `json:"risk_level,omitempty"`
	Coverage              string   `json:"coverage,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	Commands              []string `json:"commands,omitempty"`
	FailedCommands        []string `json:"failed_commands,omitempty"`
	LearnedRecommendation string   `json:"learned_recommendation,omitempty"`
}

type toolFailureConfidenceSnapshot struct {
	Lane             string `json:"lane,omitempty"`
	Tool             string `json:"tool,omitempty"`
	Action           string `json:"action,omitempty"`
	ConfidenceImpact string `json:"confidence_impact,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

func buildEffortPolicySnapshot(plan promptExecutionPlan, maxToolSteps int, timeoutSeconds int, maxOutputTokens int, reasoningBudget int) effortPolicySnapshot {
	level := effortLevelForPlan(plan)
	policy := effortPolicySnapshot{
		Lane:                 string(plan.lane),
		Level:                level,
		MaxToolSteps:         maxToolSteps,
		TimeBudgetSeconds:    timeoutSeconds,
		MaxOutputTokens:      maxOutputTokens,
		ReasoningBudget:      reasoningBudget,
		ConfidenceFloor:      confidenceFloorForEffort(level),
		BudgetExhaustionMode: "answer_with_explicit_uncertainty_or_fallback",
	}
	switch plan.lane {
	case analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		policy.AnalysisToolBudget = exploreToolBudget(maxToolSteps)
		policy.DeepeningPasses = 2
	case analysisLaneImplement:
		policy.ImplementToolBudget = implementToolBudget(maxToolSteps)
	case analysisLaneLocate:
		policy.AnalysisToolBudget = exploreToolBudget(maxToolSteps)
		if plan.followupLane == analysisLaneImplement {
			policy.ImplementToolBudget = implementToolBudget(maxToolSteps)
		}
	default:
		policy.AnalysisToolBudget = maxToolSteps
	}
	if plan.followupLane == analysisLaneImplement && policy.ImplementToolBudget == 0 {
		policy.ImplementToolBudget = implementToolBudget(maxToolSteps)
	}
	return policy
}

func effortLevelForPlan(plan promptExecutionPlan) string {
	if plan.thoroughness != "" {
		return string(plan.thoroughness)
	}
	switch plan.lane {
	case analysisLaneImplement:
		return "bounded"
	case analysisLaneMain:
		return "standard"
	default:
		return "medium"
	}
}

func confidenceFloorForEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "quick":
		return "low_with_explicit_uncertainty"
	case "thorough":
		return "high_or_exhausted_budget"
	case "bounded":
		return "verified_or_failed"
	default:
		return "medium"
	}
}

func buildRouteAmbiguitySnapshot(prompt string, plan promptExecutionPlan) routeAmbiguitySnapshot {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	signals := make([]string, 0, 5)
	if containsAny(normalized, "find ", "where ", "locate", "identify where") {
		signals = append(signals, "locate")
	}
	if containsAny(normalized, "explore", "investigate", "understand", "walkthrough", "how does") {
		signals = append(signals, "explore")
	}
	if containsAny(normalized, "review", "bug", "regression", "vulnerab", "security", "performance") {
		signals = append(signals, "review")
	}
	if containsAny(normalized, "architecture", "structure", "boundary", "package", "refactor", "modular") {
		signals = append(signals, "architecture")
	}
	if promptRequestsImplementation(normalized) || containsAny(normalized, "implement", "apply", "edit", "change", "update", "fix") {
		signals = append(signals, "implement")
	}
	signals = uniqueNonEmptyStrings(signals)
	ambiguous := len(signals) > 1
	tieBreak := ""
	if ambiguous {
		tieBreak = "ordered host route precedence selected " + string(plan.lane)
	}
	return routeAmbiguitySnapshot{
		SelectedLane: string(plan.lane),
		Reason:       string(plan.reason),
		Signals:      signals,
		Ambiguous:    ambiguous,
		TieBreak:     tieBreak,
	}
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

func (m *Model) logBudgetLedger(ledger budgetLedgerSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return
	}
	m.logMessage("budget_ledger", string(data))
}

func (m *Model) logEvidenceDelta(delta evidenceDeltaSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(delta)
	if err != nil {
		return
	}
	m.logMessage("evidence_delta", string(data))
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

func (m *Model) logRouteAmbiguity(ambiguity routeAmbiguitySnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(ambiguity)
	if err != nil {
		return
	}
	m.logMessage("route_ambiguity", string(data))
}

func (m *Model) logVerificationFeedback(feedback verificationFeedbackSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(feedback)
	if err != nil {
		return
	}
	m.logMessage("verification_feedback", string(data))
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

func (m *Model) bodyView() string {
	body := m.viewport.View()
	transcriptWidth, panelWidth, showPanel := m.bodyLayout()
	if !showPanel {
		return body
	}
	transcript := lipgloss.NewStyle().Width(transcriptWidth).Render(body)
	panel := m.executionPanelView(panelWidth, m.viewport.Height)
	return lipgloss.JoinHorizontal(lipgloss.Top, transcript, " ", panel)
}

func (m *Model) panelSummary() string {
	if !m.panelVisible {
		return "collapsed"
	}
	if _, _, show := m.bodyLayout(); !show {
		return "expanded (auto-hidden on narrow width)"
	}
	return "expanded"
}

func (m *Model) renderMessages(forceBottom bool) {
	if !m.ready {
		return
	}

	stickBottom := forceBottom || m.viewport.AtBottom()

	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("63")).Padding(0, 1).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("31")).Padding(0, 1).Bold(true)
	systemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("238")).Padding(0, 1).Bold(true)
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("94")).Padding(0, 1).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Padding(0, 1)

	var b strings.Builder
	for _, msg := range m.messages {
		var label string
		content := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case "user":
			label = userStyle.Render("You")
		case "assistant":
			label = assistantStyle.Render("Qwen")
			if m.streaming && content == "" {
				content = m.spinner.View() + " Qwen is working on your request" + m.workingDots()
			}
		case "tool":
			label = toolStyle.Render("Tool")
		default:
			label = systemStyle.Render(strings.Title(msg.Role))
		}
		b.WriteString(label)
		b.WriteString("\n")
		b.WriteString(bodyStyle.Render(content))
		b.WriteString("\n\n")
	}

	if len(m.messages) == 0 {
		b.WriteString(bodyStyle.Render("No messages yet. Type a prompt below or use /help."))
	}

	m.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
	if stickBottom {
		m.viewport.GotoBottom()
	}
}

func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
}

func (m *Model) workingDots() string {
	return strings.Repeat(".", m.workingFrame)
}

func (m *Model) reloadPromptStack() error {
	return m.reloadPromptStackWithProfiles(m.activeProfiles)
}

func (m *Model) reloadPromptStackWithProfiles(profiles []string) error {
	stack, err := promptstack.Build(m.cfg.Chat, profiles)
	if err != nil {
		return err
	}
	m.promptStack = stack
	m.logPromptContext()
	return nil
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

func (m *Model) writeApprovalBanner() string {
	if m.pendingWrite == nil {
		return ""
	}
	return m.pendingWrite.bannerText()
}

func (m *Model) modeSummary() string {
	if m.workspace == nil {
		return "normal"
	}

	mode := m.workspace.Mode()
	if mode != workspace.ModeExplore {
		return string(mode)
	}
	return fmt.Sprintf("%s(%s)", mode, m.workspace.ExploreThoroughness())
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

func (m *Model) phaseCardStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Padding(0, 1).Bold(true)
	switch m.phaseCard.State {
	case phaseStateDone:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("28"))
	case phaseStateBlocked:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("136"))
	case phaseStateFailed:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("160"))
	default:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62"))
	}
}

func (m *Model) phaseCardView() string {
	if strings.TrimSpace(m.phaseCard.Phase) == "" {
		return ""
	}
	header := string(m.phaseCard.Lane)
	if header == "" {
		header = "main"
	}
	state := string(m.phaseCard.State)
	if state == "" {
		state = string(phaseStateActive)
	}
	line := fmt.Sprintf("phase: %s / %s [%s", header, m.phaseCard.Phase, state)
	if !m.phaseCard.At.IsZero() {
		line += " | " + formatPhaseElapsed(time.Since(m.phaseCard.At))
	}
	line += "]"
	if strings.TrimSpace(m.phaseCard.Detail) == "" {
		return line
	}
	return line + "\n" + truncateText(strings.TrimSpace(m.phaseCard.Detail), 120)
}

func (m *Model) executionPanelView(width int, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	headingStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("151"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	sections := make([]string, 0, 6)
	current := make([]string, 0, 4)
	if strings.TrimSpace(m.executionPanel.Request) != "" {
		current = append(current, "request: "+truncateText(strings.TrimSpace(m.executionPanel.Request), 160))
	}
	if lane := strings.TrimSpace(string(m.executionPanel.Lane)); lane != "" {
		current = append(current, "lane: "+lane)
	}
	if followup := strings.TrimSpace(string(m.executionPanel.FollowupLane)); followup != "" {
		current = append(current, "followup: "+followup)
	}
	if reason := strings.TrimSpace(string(m.executionPanel.RouteReason)); reason != "" {
		current = append(current, "route: "+reason)
	}
	if contract := strings.TrimSpace(string(m.executionPanel.Contract)); contract != "" {
		current = append(current, "contract: "+contract)
	}
	if card := strings.TrimSpace(m.phaseCardView()); card != "" {
		current = append(current, strings.ReplaceAll(card, "\n", " | "))
	}
	if strings.TrimSpace(m.status) != "" {
		current = append(current, "status: "+truncateText(strings.TrimSpace(m.status), 160))
	}
	if len(current) > 0 {
		sections = append(sections, headingStyle.Render("Current")+"\n"+bodyStyle.Render(strings.Join(current, "\n")))
	}

	if summary := strings.TrimSpace(m.executionPanel.Summary); summary != "" || len(m.executionPanel.Steps) > 0 {
		lines := make([]string, 0, len(m.executionPanel.Steps)+1)
		if summary != "" {
			lines = append(lines, truncateText(summary, 180))
		}
		for _, step := range m.executionPanel.Steps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			lines = append(lines, "- "+truncateText(step, 180))
		}
		if len(lines) > 0 {
			sections = append(sections, headingStyle.Render("Plan")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
		}
	}

	if plan := m.executionPanel.Verification; len(plan.Commands) > 0 {
		lines := make([]string, 0, len(plan.Commands)+1)
		for _, command := range plan.Commands {
			lines = append(lines, "- "+command)
		}
		meta := ""
		switch {
		case strings.TrimSpace(plan.Confidence) != "" && strings.TrimSpace(plan.Coverage) != "":
			meta = fmt.Sprintf("confidence: %s (%s)", plan.Confidence, plan.Coverage)
		case strings.TrimSpace(plan.Confidence) != "":
			meta = "confidence: " + plan.Confidence
		case strings.TrimSpace(plan.Coverage) != "":
			meta = "coverage: " + plan.Coverage
		}
		if meta != "" {
			lines = append(lines, meta)
		}
		if strings.TrimSpace(plan.RiskLevel) != "" {
			lines = append(lines, "risk: "+strings.TrimSpace(plan.RiskLevel))
		}
		if len(plan.ExpectedFailures) > 0 {
			lines = append(lines, "expected failures: "+strings.Join(uniqueLimitedStrings(plan.ExpectedFailures, 3), ", "))
		}
		if strings.TrimSpace(plan.RepairPolicy) != "" {
			lines = append(lines, "repair: "+truncateText(strings.TrimSpace(plan.RepairPolicy), 140))
		}
		sections = append(sections, headingStyle.Render("Verification")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if proposal := m.pendingWrite; proposal != nil {
		lines := make([]string, 0, 4)
		lines = append(lines, "scope: "+strings.Join(proposal.Paths, ", "))
		for _, step := range proposalPanelSteps(proposal) {
			lines = append(lines, "- "+truncateText(step, 180))
		}
		sections = append(sections, headingStyle.Render("Approval")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if journal := m.lastJournal; journal != nil {
		lines := make([]string, 0, len(journal.Paths)+5)
		if strings.TrimSpace(journal.Summary) != "" {
			lines = append(lines, truncateText(strings.TrimSpace(journal.Summary), 180))
		}
		for _, path := range journal.Paths {
			lines = append(lines, "- "+path)
		}
		if stage := journal.Refactor; stage != nil && strings.TrimSpace(stage.NextStep) != "" {
			stageLine := "refactor stage"
			if stage.Stage > 0 {
				stageLine += fmt.Sprintf(" %d", stage.Stage)
			}
			if strings.TrimSpace(stage.FocusPath) != "" {
				stageLine += ": " + stage.FocusPath
			}
			if strings.TrimSpace(stage.Verification) != "" {
				stageLine += " [" + stage.Verification + "]"
			}
			lines = append(lines, stageLine)
			lines = append(lines, "next: "+truncateText(stage.NextStep, 180))
		}
		lines = append(lines, "rollback: /undo")
		sections = append(sections, headingStyle.Render("Latest")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if stage := m.currentStructuralRefactorStage(); stage != nil && structuralRefactorStageResumable(stage) && (m.lastJournal == nil || m.lastJournal.Refactor == nil) {
		stageLine := "stage"
		if stage.Stage > 0 {
			stageLine += fmt.Sprintf(" %d", stage.Stage)
		}
		if strings.TrimSpace(stage.FocusPath) != "" {
			stageLine += ": " + stage.FocusPath
		}
		lines := []string{stageLine}
		if strings.TrimSpace(stage.NextStep) != "" {
			lines = append(lines, "next: "+truncateText(stage.NextStep, 180))
		}
		lines = append(lines, "resume: /resume")
		sections = append(sections, headingStyle.Render("Refactor")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if len(m.executionPanel.Progress) > 0 {
		lines := make([]string, 0, len(m.executionPanel.Progress))
		for _, entry := range m.executionPanel.Progress {
			lines = append(lines, "- "+truncateText(entry, 180))
		}
		sections = append(sections, headingStyle.Render("Progress")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	content := titleStyle.Render("Execution Panel")
	if len(sections) > 0 {
		content += "\n\n" + strings.Join(sections, "\n\n")
	}
	panelStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(0, 1)
	return panelStyle.
		Width(maxInt(1, width-panelStyle.GetHorizontalFrameSize())).
		Height(maxInt(1, height-panelStyle.GetVerticalFrameSize())).
		Render(content)
}

func proposalPanelSteps(proposal *writeApprovalProposal) []string {
	if proposal == nil {
		return nil
	}
	steps := make([]string, 0, len(proposal.PlannedEdits)+len(proposal.VerifySteps)+1)
	for _, line := range proposal.PlannedEdits {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		steps = append(steps, line)
	}
	if len(steps) == 0 && strings.TrimSpace(proposal.Reason) != "" {
		steps = append(steps, strings.TrimSpace(proposal.Reason))
	}
	return uniqueNonEmptyStrings(steps)
}

func formatStructuredExecutionPlanSteps(plan structuredExecutionPlan) []string {
	steps := make([]string, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		path := normalizeGuardedPath(op.Path)
		if path == "" {
			path = "target file"
		}
		action := strings.TrimSpace(op.Tool)
		if action == "" {
			action = "edit_file"
		}
		line := action + " " + path
		if op.StartLine > 0 && op.EndLine >= op.StartLine {
			line += fmt.Sprintf(" lines %d-%d", op.StartLine, op.EndLine)
		}
		if reason := strings.TrimSpace(op.Reason); reason != "" {
			line += " (" + reason + ")"
		}
		steps = append(steps, line)
	}
	return uniqueNonEmptyStrings(steps)
}

func defaultExecutionPlanSummary(plan promptExecutionPlan) string {
	switch plan.lane {
	case analysisLaneReview:
		if routeContinuesToImplement(plan) {
			return "Reviewing bounded evidence before the implementation pass."
		}
		return "Reviewing bounded evidence and ranking likely findings."
	case analysisLaneArchitecture:
		if routeContinuesToImplement(plan) {
			return "Mapping structural boundaries before the implementation pass."
		}
		return "Mapping structural boundaries and ranking architecture hotspots."
	case analysisLaneExplore:
		if routeContinuesToImplement(plan) {
			return "Exploring the repository before the implementation pass."
		}
		return "Exploring the repository and gathering focused context."
	case analysisLaneLocate:
		return "Locating the exact code path before applying the requested edit."
	case analysisLaneImplement:
		return "Preparing a bounded implementation plan against the targeted files."
	default:
		return "Waiting for the response."
	}
}

func (m *Model) setPhaseCard(lane analysisLane, phase string, state phaseDisplayState, detail string) {
	m.applyPhaseCardUpdate(phaseCardState{
		Lane:   lane,
		Phase:  phase,
		Detail: detail,
		State:  state,
		At:     time.Now(),
	})
}

func (m *Model) emitPhaseCard(lane analysisLane, phase string, state phaseDisplayState, detail string) {
	m.events <- phaseCardMsg{
		Lane:   lane,
		Phase:  phase,
		Detail: detail,
		State:  state,
		At:     time.Now(),
	}
}

func (m *Model) logPhaseCard() {
	if m.debugLogger == nil || strings.TrimSpace(m.phaseCard.Phase) == "" {
		return
	}
	_ = m.debugLogger.LogPhase(string(m.phaseCard.Lane), m.phaseCard.Phase, string(m.phaseCard.State), m.phaseCard.Detail)
}

func (m *Model) applyPhaseCardUpdate(card phaseCardState) {
	m.phaseCard = card
	if card.Lane != "" {
		m.executionPanel.Lane = card.Lane
	}
	if strings.TrimSpace(card.Phase) != "" {
		lane := firstNonEmptyString(string(card.Lane), "main")
		state := firstNonEmptyString(string(card.State), string(phaseStateActive))
		entry := fmt.Sprintf("%s / %s [%s]", lane, card.Phase, state)
		if detail := strings.TrimSpace(card.Detail); detail != "" {
			entry += ": " + detail
		}
		m.appendExecutionProgress(entry)
	}
	m.logPhaseCard()
}

func (m *Model) resetExecutionPanel(prompt string, plan promptExecutionPlan) {
	m.executionPanel = executionPanelState{
		Request:      truncateText(strings.TrimSpace(prompt), 180),
		Lane:         plan.lane,
		FollowupLane: plan.followupLane,
		RouteReason:  plan.reason,
		Contract:     promptPlanAnswerContract(plan),
		Summary:      defaultExecutionPlanSummary(plan),
		UpdatedAt:    time.Now(),
	}
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

func (m *Model) setExecutionVerificationPlan(plan verificationPlan) {
	m.executionPanel.Verification = verificationPlan{
		Commands:         append([]string(nil), plan.Commands...),
		Reasons:          append([]string(nil), plan.Reasons...),
		UsesFullSuite:    plan.UsesFullSuite,
		Coverage:         strings.TrimSpace(plan.Coverage),
		Confidence:       strings.TrimSpace(plan.Confidence),
		RiskLevel:        strings.TrimSpace(plan.RiskLevel),
		ExpectedFailures: append([]string(nil), plan.ExpectedFailures...),
		RepairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	m.executionPanel.UpdatedAt = time.Now()
	m.logVerificationPlan(plan)
}

func (m *Model) logVerificationPlan(plan verificationPlan) {
	if m.debugLogger == nil {
		return
	}
	payload := struct {
		Commands         []string `json:"commands,omitempty"`
		Reasons          []string `json:"reasons,omitempty"`
		UsesFullSuite    bool     `json:"uses_full_suite,omitempty"`
		Coverage         string   `json:"coverage,omitempty"`
		Confidence       string   `json:"confidence,omitempty"`
		RiskLevel        string   `json:"risk_level,omitempty"`
		ExpectedFailures []string `json:"expected_failures,omitempty"`
		RepairPolicy     string   `json:"repair_policy,omitempty"`
	}{
		Commands:         append([]string(nil), plan.Commands...),
		Reasons:          append([]string(nil), plan.Reasons...),
		UsesFullSuite:    plan.UsesFullSuite,
		Coverage:         strings.TrimSpace(plan.Coverage),
		Confidence:       strings.TrimSpace(plan.Confidence),
		RiskLevel:        strings.TrimSpace(plan.RiskLevel),
		ExpectedFailures: append([]string(nil), plan.ExpectedFailures...),
		RepairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("verification_plan", string(data))
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

func (m *Model) rememberExecutionJournal(recorder *executionJournalRecorder, changedPaths []string, summary string, refactorStage *structuralRefactorStage) {
	if recorder == nil {
		return
	}
	m.lastJournal = recorder.Finalize(changedPaths, summary)
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

func (m *Model) newWorkspaceManager() *workspace.Manager {
	mgr := workspace.NewManager(m.cfg.Workspace)
	if m.workspace != nil {
		_ = mgr.SetWritePolicy(m.workspace.WritePolicy())
	}
	return mgr
}

type promptRouteProfile struct {
	normalized           string
	wantsLocate          bool
	wantsDirectImplement bool
	wantsImplementation  bool
	wantsExploration     bool
	wantsReview          bool
	wantsArchitecture    bool
	thoroughness         workspace.ExploreThoroughness
}

func classifyPromptExecutionPlan(prompt string, mgr *workspace.Manager) promptExecutionPlan {
	if mgr == nil || !mgr.Enabled() || mgr.Mode() != workspace.ModeNormal {
		return promptExecutionPlan{}
	}
	profile := buildPromptRouteProfile(prompt)
	lane, reason := promptInitialRoute(profile)
	thoroughness := profile.thoroughness
	if analysisLaneUsesThoroughness(lane) && thoroughness == "" {
		thoroughness = mgr.ExploreThoroughness()
	}
	return promptExecutionPlan{
		lane:         lane,
		followupLane: promptFollowupRoute(profile, lane),
		thoroughness: thoroughness,
		reason:       reason,
		contract:     answerContractForLane(lane),
	}
}

func (m *Model) normalizePromptPlanThoroughness(plan promptExecutionPlan) promptExecutionPlan {
	if !analysisLaneUsesThoroughness(plan.lane) || plan.thoroughness != "" {
		return plan
	}
	if m != nil && m.workspace != nil {
		plan.thoroughness = m.workspace.ExploreThoroughness()
	}
	if plan.thoroughness == "" {
		plan.thoroughness = workspace.ThoroughnessMedium
	}
	return plan
}

func analysisLaneUsesThoroughness(lane analysisLane) bool {
	switch lane {
	case analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		return true
	default:
		return false
	}
}

func buildPromptRouteProfile(prompt string) promptRouteProfile {
	normalized := strings.ToLower(prompt)
	thoroughness, wantsExploration := autoExploreThoroughness(prompt)
	return promptRouteProfile{
		normalized:           normalized,
		wantsLocate:          looksLikeLocatePrompt(prompt, normalized) || shouldRouteUILiteralEditThroughLocate(prompt, normalized),
		wantsDirectImplement: looksLikeDirectImplementationPrompt(prompt, normalized),
		wantsImplementation:  promptRequestsImplementation(prompt),
		wantsExploration:     wantsExploration,
		wantsReview:          looksLikeReviewPrompt(normalized),
		wantsArchitecture:    looksLikeArchitecturePrompt(normalized),
		thoroughness:         thoroughness,
	}
}

func promptInitialRoute(profile promptRouteProfile) (analysisLane, promptRouteReason) {
	switch {
	case profile.wantsLocate:
		return analysisLaneLocate, promptRouteReasonLocateRequest
	case profile.wantsArchitecture:
		return analysisLaneArchitecture, promptRouteReasonArchitectureRequest
	case profile.wantsExploration && profile.wantsReview:
		return analysisLaneReview, promptRouteReasonReviewRequest
	case profile.wantsExploration:
		return analysisLaneExplore, promptRouteReasonExploreRequest
	case profile.wantsDirectImplement:
		return analysisLaneImplement, promptRouteReasonDirectImplementation
	default:
		return analysisLaneMain, promptRouteReasonDirectConversation
	}
}

func promptFollowupRoute(profile promptRouteProfile, lane analysisLane) analysisLane {
	if !profile.wantsImplementation {
		return ""
	}
	switch lane {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		return analysisLaneImplement
	default:
		return ""
	}
}

func autoExploreThoroughness(prompt string) (workspace.ExploreThoroughness, bool) {
	normalized := strings.ToLower(prompt)
	if !looksLikeInvestigationPrompt(normalized) {
		return "", false
	}
	switch {
	case looksLikeNoEarlyStopPrompt(normalized):
		return workspace.ThoroughnessThorough, true
	case containsAny(normalized, "thorough", "deep", "exhaustive"):
		return workspace.ThoroughnessThorough, true
	case containsAny(normalized, "quick", "brief", "fast"):
		return workspace.ThoroughnessQuick, true
	default:
		return workspace.ThoroughnessMedium, true
	}
}

func looksLikeNoEarlyStopPrompt(normalized string) bool {
	return containsAny(
		normalized,
		"longer search",
		"give up early",
		"not super confident",
		"not confident",
		"do not stop",
		"don't stop",
		"continue until",
		"keep searching",
		"proper frontier",
		"frontier-grade",
		"whole codebase",
		"entire codebase",
		"literally the whole codebase",
		"comprehensive",
	)
}

func looksLikeInvestigationPrompt(normalized string) bool {
	if containsAny(normalized, "just explore", "what's wrong", "what is wrong") {
		return true
	}

	hasInvestigationVerb := containsAny(
		normalized,
		"explore",
		"investigate",
		"investigation",
		"inspect",
		"audit",
		"review",
		"look for",
		"look through",
		"search",
		"scan",
		"identify",
		"find",
		"analyze",
		"diagnose",
	)
	hasTarget := containsAny(
		normalized,
		"bug",
		"issue",
		"problem",
		"improvement",
		"improvements",
		"optimization",
		"optimize",
		"cleanup",
		"clean up",
		"regression",
		"performance",
		"code smell",
		"architecture",
		"architectural",
		"structure",
	)
	hasScope := containsAny(
		normalized,
		"codebase",
		"repo",
		"repository",
		"project",
		"harness",
		"code",
	)
	hasArchitectureQuestion := containsAny(
		normalized,
		"can you identify",
		"how does",
		"how do",
		"how are",
		"how is",
		"why does",
		"why is",
	)
	hasArchitectureTarget := containsAny(
		normalized,
		"support",
		"supported",
		"extensible",
		"extension",
		"architecture",
		"pipeline",
		"workflow",
		"language",
		"languages",
	)
	hasOpenEndedReviewQuestion := containsAny(
		normalized,
		"what can we",
		"what should we",
		"anything to",
		"where should we",
	)

	return (hasInvestigationVerb && hasTarget) ||
		(hasInvestigationVerb && hasScope) ||
		(hasOpenEndedReviewQuestion && hasTarget && hasScope) ||
		(hasArchitectureQuestion && hasScope && hasArchitectureTarget)
}

func looksLikeArchitecturePrompt(normalized string) bool {
	hasScope := containsAny(
		normalized,
		"codebase",
		"repo",
		"repository",
		"project",
		"harness",
		"system",
		"code",
	)
	hasStructureTarget := containsAny(
		normalized,
		"structure",
		"architecture",
		"architectural",
		"organize",
		"organization",
		"modular",
		"modularity",
		"boundary",
		"boundaries",
		"split",
		"decompose",
		"extensible",
		"extension",
		"support",
		"supported",
		"capability",
		"capabilities",
		"language",
		"languages",
		"pipeline",
		"workflow",
		"responsibility",
		"responsibilities",
	)
	hasArchitectureQuestion := containsAny(
		normalized,
		"how does",
		"how do",
		"how are",
		"how is",
		"how should",
		"why does",
		"why is",
		"what can we",
		"what should we",
		"where should we",
		"which language",
		"which languages",
	)
	hasStructuralImprovementRequest := containsAny(
		normalized,
		"architectural improvement",
		"architectural improvements",
		"architecture improvement",
		"architecture improvements",
		"structural improvement",
		"structural improvements",
		"package boundary",
		"package boundaries",
	)
	hasRefactorVerb := containsAny(
		normalized,
		"optimize",
		"optimization",
		"improve",
		"refactor",
		"cleanup",
		"clean up",
		"reorganize",
	)
	if hasStructuralImprovementRequest {
		return true
	}
	return (hasScope && hasStructureTarget && hasArchitectureQuestion) ||
		(hasScope && hasStructureTarget && hasRefactorVerb) ||
		(hasScope && hasStructureTarget && containsAny(normalized, "identify any", "analyze", "review"))
}

func looksLikeLocatePrompt(prompt string, normalized string) bool {
	hasLocateVerb := containsAny(
		normalized,
		"where is",
		"locate",
		"identify",
		"show me",
		"which code",
		"what code",
		"find the code",
		"find where",
	)
	hasVerificationGuard := containsAny(
		normalized,
		"identify it first",
		"show it first",
		"so i can verify",
		"before changing",
		"before you change it",
		"don't change it yet",
		"do not change it yet",
	)
	hasLiteral := primaryLocateLiteral(prompt) != ""
	hasUILocation := containsAny(
		normalized,
		"top",
		"header",
		"footer",
		"badge",
		"window",
		"chat",
		"menu",
		"status",
		"banner",
	)
	if hasVerificationGuard && (hasLiteral || hasUILocation) {
		return true
	}
	return hasLocateVerb && (hasLiteral || hasUILocation)
}

func hasUILocationHint(normalized string) bool {
	return containsAny(
		normalized,
		"top",
		"header",
		"footer",
		"badge",
		"window",
		"chat",
		"menu",
		"status",
		"banner",
	)
}

func shouldRouteUILiteralEditThroughLocate(prompt string, normalized string) bool {
	return promptRequestsImplementation(prompt) && primaryLocateLiteral(prompt) != "" && hasUILocationHint(normalized)
}

func promptRequestsImplementation(prompt string) bool {
	normalized := strings.ToLower(prompt)
	trimmed := strings.TrimSpace(normalized)
	if strings.HasPrefix(trimmed, "fix ") || strings.HasPrefix(trimmed, "patch ") || strings.HasPrefix(trimmed, "implement ") || strings.HasPrefix(trimmed, "refactor ") {
		return true
	}
	if primaryLocateLiteral(prompt) != "" && containsAny(normalized, "remove ", "delete ", "take it out") {
		return true
	}
	return containsAny(normalized,
		"fix it now",
		"fix this now",
		"please fix",
		"go fix",
		"patch it",
		"patch this",
		"apply the fix",
		"apply that fix",
		"apply this fix",
		"apply changes now",
		"write the fix",
		"write the patch",
		"edit the file",
		"edit those files",
		"update the code",
		"change the code",
		"implement the fix",
		"make the fix",
		"make the change",
		"create the file",
		"delete the file",
		"remove it",
		"remove this",
		"remove the code",
		"delete it",
		"refactor it",
		"refactor this",
		"then fix",
		"then fix it",
		"then update",
		"then implement it",
		"and fix it",
		"and update",
		"and implement it",
	)
}

func promptRequestsRemoval(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(
		normalized,
		"remove it",
		"remove this",
		"remove the",
		"delete it",
		"delete this",
		"delete the",
		"take it out",
	)
}

func looksLikeDirectImplementationPrompt(prompt string, normalized string) bool {
	if promptRequestsImplementation(prompt) {
		return true
	}
	if requestsShellOnlyMutation(normalized) && (len(promptMentionedWorkspacePaths(prompt)) > 0 || containsAny(normalized, "file ", "directory ", "folder ")) {
		return true
	}
	if containsAny(normalized, "use edit_file", "targeted edit", "code edit", "change the footer help text", "replace ", "rename ") {
		return len(promptMentionedWorkspacePaths(prompt)) > 0
	}
	hasEditVerb := containsAny(
		normalized,
		"create",
		"edit",
		"change",
		"replace",
		"update",
		"modify",
		"rename",
		"remove",
		"delete",
		"add",
		"write",
		"append",
		"mkdir",
	)
	hasConcreteTarget := len(promptMentionedWorkspacePaths(prompt)) > 0 ||
		containsAny(normalized, "line ", "function ", "method ", "text from", "text to") ||
		hasUILocationHint(normalized)
	return hasEditVerb && hasConcreteTarget && !looksLikeInvestigationPrompt(normalized)
}

func looksLikeReviewPrompt(normalized string) bool {
	return containsAny(
		normalized,
		"review",
		"audit",
		"bug",
		"issue",
		"problem",
		"code smell",
		"cleanup",
		"clean up",
		"refactor",
		"optimization",
		"optimize",
		"regression",
		"error",
		"identify any",
		"find one",
		"should be fixed",
	)
}

func looksLikeIssueHuntPrompt(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(
		normalized,
		"bug",
		"issue",
		"problem",
		"optimization",
		"optimize",
		"cleanup",
		"clean up",
		"regression",
		"performance",
		"code smell",
		"error",
	)
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
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

func (m *Model) systemPrompt() string {
	sections := make([]string, 0, 3)
	if prompt := strings.TrimSpace(m.promptStack.Text); prompt != "" {
		sections = append(sections, prompt)
	}
	if m.workspace != nil {
		if instructions := strings.TrimSpace(m.workspace.Instructions()); instructions != "" {
			sections = append(sections, instructions)
		}
	}
	return strings.Join(sections, "\n\n")
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
	idx := m.pendingAssistantIndex()
	if idx == -1 {
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: content})
		return
	}
	m.messages[idx].Content = content
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

func (m *Model) updateViewport(msg tea.Msg) {
	if !m.ready {
		return
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	_ = cmd
}

func scrollableViewportKey(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}

	switch keyMsg.Type {
	case tea.KeyPgUp, tea.KeyPgDown:
		return true
	default:
		return false
	}
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

func (m *Model) logPromptContext() {
	if m.debugLogger == nil {
		return
	}
	workspaceMode, thoroughness := m.currentModeDetails()
	_ = m.debugLogger.LogContext(m.contextSourceNames(), m.systemPrompt(), workspaceMode, thoroughness)
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

func agentToolActionClass(req workspace.ToolRequest) string {
	if workspace.IsBroadInspectionRequest(req) {
		return "broad_inspection"
	}
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return "read_file:" + firstNonEmptyString(strings.TrimSpace(req.ID), strings.TrimSpace(req.Path))
	case "edit_file":
		return "edit_file:" + strings.TrimSpace(req.Path)
	case "search_files", "search_content", "rg":
		return strings.TrimSpace(req.Tool) + ":" + strings.TrimSpace(req.Path)
	case "shell":
		fields := strings.Fields(strings.TrimSpace(req.Command))
		if len(fields) == 0 {
			return "shell"
		}
		return "shell:" + fields[0]
	default:
		return strings.TrimSpace(req.Tool)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func reviewCandidatePaths(candidates []reviewCandidate) []string {
	paths := make([]string, 0, len(candidates))
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		path := strings.TrimSpace(candidate.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func isMeaningfulReviewHit(match searchMatch) bool {
	text := strings.ToLower(strings.TrimSpace(match.Text))
	if text == "" {
		return false
	}
	if looksLikeMarkerPatternDefinition(text) || looksLikeBareMarkerLiteral(text) {
		return false
	}
	if containsAny(text,
		"\\\"tool\\\":",
		"\"tool\":\"",
		"available tools",
		"required output",
		"review rules",
		"request:",
		"review this request",
		"buildreviewsystemprompt",
		"buildreviewtaskprompt",
		"identify any",
		"find one",
		"code smell",
		"cleanup candidate",
		"likely cleanup target",
		"recommended next step",
		"return one concrete finding",
		"addreason(candidate",
		"containsany(",
		"issuesearchpattern(",
		"workspace access is enabled",
	) {
		return false
	}
	return containsAny(text, "todo", "fixme", "xxx", "hack", "deprecated", "panic(", "bug", "regression", "cleanup", "error")
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

func looksLikeBareMarkerLiteral(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasSuffix(trimmed, ",") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ","))
	}
	if len(trimmed) < 2 || trimmed[0] != '"' || trimmed[len(trimmed)-1] != '"' {
		return false
	}
	literal := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if literal == "" {
		return false
	}
	if strings.ContainsAny(literal, " \t") {
		return false
	}
	return containsAny(literal, "todo", "fixme", "xxx", "hack", "deprecated", "panic\\(", "bug", "regression", "cleanup", "error")
}

func reviewHasSemanticSupport(goIndex *semantic.GoIndex, path string, issueHotPaths map[string]struct{}, defaultHotPaths map[string]struct{}) bool {
	if goIndex == nil {
		return false
	}
	if _, ok := issueHotPaths[path]; ok {
		return true
	}
	if _, ok := defaultHotPaths[path]; ok {
		return true
	}
	file := goIndex.FileByPath(path)
	if file == nil {
		return false
	}
	surface := file.Funcs + file.Methods
	return surface >= 40 || file.Types >= 8 || file.HasMain
}

func reviewPathRoleScore(prompt string, path string) int {
	path = strings.ToLower(strings.TrimSpace(path))
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if path == "" {
		return 0
	}

	score := 0
	switch {
	case strings.HasSuffix(path, "_test.go"):
		score -= 2
	case path == "readme.md" || path == "go.mod" || path == "config.json":
		score -= 2
	}
	if strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "pkg/") || strings.HasPrefix(path, "src/") {
		score++
	}
	if containsAny(path, "/manager", "/runner", "/client", "/server", "/controller", "/workspace", "/app") {
		score += 2
	}
	if containsAny(path, "prompts/", "logs/", "models/", "transcript/") {
		score -= 2
	}
	if containsAny(prompt, "code smell", "cleanup", "clean up", "refactor") && containsAny(path, "/manager", "/runner", "/workspace", "/app") {
		score++
	}
	if containsAny(prompt, "bug", "issue", "problem", "error", "regression") && containsAny(path, "/client", "/server", "/runner", "/workspace") {
		score++
	}
	return score
}

func looksLikeCodeSmellPrompt(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return containsAny(normalized, "code smell", "cleanup", "clean up", "refactor")
}

func (m *Model) logToolResult(event agent.ToolResultEvent) {
	if m.debugLogger == nil {
		return
	}
	_ = m.debugLogger.LogToolResult(event.Step, event.Agent, event.Result.Tool, event.Result.Summary, event.Result.Error)
}

func shouldInvalidateExploreCacheForTool(result workspace.ToolResult) bool {
	if result.Error != "" {
		return false
	}
	switch result.Tool {
	case "mkdir", "edit_file", "write_file", "append_file", "shell":
		return true
	default:
		return false
	}
}

func (m *Model) invalidateExploreCache(reason string) {
	if m.exploreCache == nil {
		return
	}
	m.exploreCache = nil
	if strings.TrimSpace(reason) != "" {
		m.logMessage("explore_cache_invalidate", reason)
	}
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

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func normalizeHistoryRole(role string) string {
	switch role {
	case "user", "assistant", "system":
		return role
	case "tool":
		return "system"
	default:
		return "system"
	}
}
