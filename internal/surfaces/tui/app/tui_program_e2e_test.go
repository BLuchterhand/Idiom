package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/modepolicy"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

type programPromptSnapshot struct {
	messages             []llama.ChatMessage
	status               string
	streaming            bool
	approvalSummary      string
	approvalEventSummary string
}

type programToolApprovalOutcome struct {
	request workspace.ToolRequest
	result  workspace.ToolResult
}

type bubbleProgramResult struct {
	model tea.Model
	err   error
}

// test-double: observation-probe
// observedProgramModel adds an assertion boundary to a real Bubble Tea
// program without changing the application Model. The snapshot is taken by
// the event-loop goroutine after the actual completion message is applied.
type observedProgramModel struct {
	model     *Model
	completed chan<- programPromptSnapshot
	updates   chan<- programPromptSnapshot
}

func (m *observedProgramModel) Init() tea.Cmd {
	return m.model.Init()
}

func (m *observedProgramModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.model.Update(msg)
	m.model = updated.(*Model)
	snapshot := programPromptSnapshot{
		messages:  cloneChatMessages(m.model.messages),
		status:    m.model.status,
		streaming: m.model.streaming,
	}
	if pending := m.model.currentApproval(); pending != nil {
		snapshot.approvalSummary = pending.summary()
	}

	observedMsg := msg
	if wrapped, ok := msg.(eventMsg); ok {
		observedMsg = wrapped.msg
	}
	captureUpdate := false
	switch observedMsg := observedMsg.(type) {
	case approvalPendingMsg:
		captureUpdate = true
		snapshot.approvalEventSummary = observedMsg.pending.summary()
	case approvalAwaitingMsg:
		captureUpdate = true
		snapshot.approvalEventSummary = observedMsg.pending.summary()
	case approvalClearedMsg:
		captureUpdate = true
		snapshot.approvalEventSummary = observedMsg.pending.summary()
	case tea.KeyMsg:
		captureUpdate = observedMsg.Type == tea.KeyEnter
	}
	if captureUpdate && m.updates != nil {
		m.updates <- snapshot
	}

	completed := false
	switch msg := msg.(type) {
	case promptWorkerResultMsg:
		completed = true
	case eventMsg:
		_, completed = msg.msg.(promptWorkerResultMsg)
	}
	if completed {
		m.completed <- snapshot
	}

	return m, cmd
}

func receiveProgramPromptSnapshot(
	ctx context.Context,
	snapshots <-chan programPromptSnapshot,
	matches func(programPromptSnapshot) bool,
) programPromptSnapshot {
	ginkgo.GinkgoHelper()

	for {
		var snapshot programPromptSnapshot
		gomega.Eventually(ctx, snapshots).Should(gomega.Receive(&snapshot))
		if matches(snapshot) {
			return snapshot
		}
	}
}

func (m *observedProgramModel) View() string {
	return m.model.View()
}

var _ = ginkgo.It("runs the real Bubble Tea event loop through help and quit", ginkgo.Label("development-scenario", "e2e", "smoke"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	cfg := featurePlannerTestConfig(t.TempDir())
	cfg.LlamaServer = config.LlamaServer{
		Kind:        config.BackendDeepSeek,
		Model:       "event-loop-smoke",
		DisplayName: "event-loop-smoke",
		BaseURL:     "http://127.0.0.1:1",
	}

	model := New(cfg)
	defer model.Close()

	program := tea.NewProgram(
		model,
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
	)
	ginkgo.DeferCleanup(program.Quit)
	finished := make(chan bubbleProgramResult, 1)
	go func() {
		finalModel, err := program.Run()
		finished <- bubbleProgramResult{model: finalModel, err: err}
	}()

	program.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/help")})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	var result bubbleProgramResult
	gomega.Eventually(ctx, finished).Should(gomega.Receive(&result))
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
	finalModel, ok := result.model.(*Model)
	gomega.Expect(ok).To(gomega.BeTrue(), "Bubble Tea should return the TUI Model")
	gomega.Expect(finalModel.width).To(gomega.Equal(100))
	gomega.Expect(finalModel.height).To(gomega.Equal(30))
	gomega.Expect(finalModel.inputHistory).To(gomega.ContainElement("/help"))
	gomega.Expect(finalModel.messages).NotTo(gomega.BeEmpty())
	gomega.Expect(finalModel.messages[len(finalModel.messages)-1].Role).To(gomega.Equal("operator"))
	gomega.Expect(strings.Contains(finalModel.messages[len(finalModel.messages)-1].Content, "Commands:")).To(gomega.BeTrue())
})

var _ = ginkgo.It("completes an ordinary mocked prompt through the real Bubble Tea event loop", ginkgo.Label("development-scenario", "e2e", "smoke"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	cfg := featurePlannerTestConfig(t.TempDir())
	cfg.LlamaServer = config.LlamaServer{
		Kind:        config.BackendDeepSeek,
		Model:       "event-loop-prompt-smoke",
		DisplayName: "event-loop-prompt-smoke",
		BaseURL:     "http://127.0.0.1:1",
	}

	script := newScriptedLLMRecorder(t, scriptedLLMConfig{
		responses:         []string{"ordinary prompt completed"},
		expectedCallCount: 1,
		assertCallCount:   true,
	})
	model := New(cfg)
	defer model.Close()
	model.client = script.Mock(t)
	model.forcedTaskFamily = string(autonomy.TaskFamilyConversation)

	completed := make(chan programPromptSnapshot, 1)
	observed := &observedProgramModel{model: model, completed: completed}
	program := tea.NewProgram(
		observed,
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
	)
	ginkgo.DeferCleanup(program.Quit)
	finished := make(chan bubbleProgramResult, 1)
	go func() {
		finalModel, err := program.Run()
		finished <- bubbleProgramResult{model: finalModel, err: err}
	}()

	prompt := "Explain the current execution state."
	program.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(prompt)})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})

	var snapshot programPromptSnapshot
	gomega.Eventually(ctx, completed).Should(gomega.Receive(&snapshot))
	gomega.Expect(snapshot.streaming).To(gomega.BeFalse())
	gomega.Expect(snapshot.status).To(gomega.Equal("Response complete."))
	gomega.Expect(snapshot.messages).To(gomega.HaveLen(2))
	gomega.Expect(snapshot.messages[0]).To(gomega.Equal(llama.ChatMessage{Role: "user", Content: prompt}))
	gomega.Expect(snapshot.messages[1]).To(gomega.Equal(llama.ChatMessage{Role: "assistant", Content: "ordinary prompt completed"}))
	gomega.Expect(script.messages).To(gomega.HaveLen(1))
	gomega.Expect(script.messages[0]).To(gomega.ContainElement(llama.ChatMessage{Role: "user", Content: prompt}))

	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	var result bubbleProgramResult
	gomega.Eventually(ctx, finished).Should(gomega.Receive(&result))
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
	_, ok := result.model.(*observedProgramModel)
	gomega.Expect(ok).To(gomega.BeTrue(), "Bubble Tea should return the observing program Model")
})

var _ = ginkgo.It("cancels and joins an active prompt when the real Bubble Tea program quits", ginkgo.Label("development-scenario", "e2e", "smoke", "race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	cfg := featurePlannerTestConfig(t.TempDir())
	cfg.LlamaServer = config.LlamaServer{
		Kind:        config.BackendDeepSeek,
		Model:       "event-loop-cancel-smoke",
		DisplayName: "event-loop-cancel-smoke",
		BaseURL:     "http://127.0.0.1:1",
	}

	started := make(chan struct{})
	exited := make(chan struct{})
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	client.EXPECT().
		CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
			close(started)
			<-ctx.Done()
			close(exited)

			return llama.ChatMessage{}, ctx.Err()
		}).
		Times(1)
	client.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "event-loop-cancel-smoke"}}, nil).AnyTimes()
	client.EXPECT().Backend().Return("mock").AnyTimes()
	client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()

	model := New(cfg)
	ginkgo.DeferCleanup(func() { _ = model.Close() })
	model.client = client
	model.forcedTaskFamily = string(autonomy.TaskFamilyConversation)

	program := tea.NewProgram(
		model,
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
	)
	ginkgo.DeferCleanup(program.Quit)
	finished := make(chan bubbleProgramResult, 1)
	go func() {
		finalModel, err := program.Run()
		finished <- bubbleProgramResult{model: finalModel, err: err}
	}()

	prompt := "Block until the active program is closed."
	program.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(prompt)})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	gomega.Eventually(ctx, started).Should(gomega.BeClosed())
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	var result bubbleProgramResult
	gomega.Eventually(ctx, finished).Should(gomega.Receive(&result))
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
	_, ok := result.model.(*Model)
	gomega.Expect(ok).To(gomega.BeTrue(), "Bubble Tea should return the TUI Model")
	closed := make(chan error, 1)
	go func() { closed <- model.Close() }()
	gomega.Eventually(ctx, closed).Should(gomega.Receive(gomega.Succeed()))
	gomega.Expect(exited).To(gomega.BeClosed(), "Close must join the canceled worker before returning")
	model.promptExecutionMu.Lock()
	active := model.activePromptExecution
	model.promptExecutionMu.Unlock()
	gomega.Expect(active).To(gomega.BeNil())
})

var _ = ginkgo.It("keeps denied and approved write lifecycles isolated through the real Bubble Tea event loop", ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := featurePlannerTestConfig(root)
	cfg.LlamaServer = config.LlamaServer{
		Kind:        config.BackendDeepSeek,
		Model:       "event-loop-approval-smoke",
		DisplayName: "event-loop-approval-smoke",
		BaseURL:     "http://127.0.0.1:1",
	}

	model := New(cfg)
	ginkgo.DeferCleanup(func() { _ = model.Close() })
	model.debugLogPath = filepath.Join(root, "logs", "approval-lifecycle.jsonl")
	model.workspace.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameWriteFile: {{Pattern: "*", Decision: toolpolicy.Ask}},
	})

	completed := make(chan programPromptSnapshot, 1)
	updates := make(chan programPromptSnapshot, 16)
	observed := &observedProgramModel{model: model, completed: completed, updates: updates}
	program := tea.NewProgram(
		observed,
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
	)
	ginkgo.DeferCleanup(program.Quit)
	finished := make(chan bubbleProgramResult, 1)
	go func() {
		finalModel, err := program.Run()
		finished <- bubbleProgramResult{model: finalModel, err: err}
	}()

	program.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	deniedRequest := workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "denied.txt",
		Content:     "must not be written\n",
		Description: "exercise a denied write",
	}
	approvedRequest := workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "approved.txt",
		Content:     "approved write\n",
		Description: "exercise an independent approved write",
	}
	toolOutcomes := make(chan programToolApprovalOutcome, 2)
	model.startPromptExecution(func(workerCtx context.Context, session *promptExecutionSession) (string, error) {
		execute := func(step int, request workspace.ToolRequest) workspace.ToolResult {
			var result workspace.ToolResult
			if blocked := session.preExecuteToolRequest(step, request, modepolicy.BuildMode()); blocked != nil {
				result = *blocked
			} else {
				result = session.workspace.Execute(workerCtx, request)
			}
			session.recordMutationRollback(workerCtx, result)
			session.recordWriteFailure(step, request, result)
			session.recordWriteSuccess(step, request, result)
			toolOutcomes <- programToolApprovalOutcome{request: request, result: result}

			return result
		}

		execute(1, deniedRequest)
		execute(2, approvedRequest)
		session.appendPromptTranscriptMessage(llama.ChatMessage{
			Role:    "assistant",
			Content: "The independently approved write completed.",
		})

		return "The independently approved write completed.", nil
	})

	gomega.Eventually(ctx, func() string {
		pending := model.currentApproval()
		if pending == nil {
			return ""
		}

		return pending.summary()
	}).Should(gomega.Equal("write_file: denied.txt"))
	firstPending := model.currentApproval()
	gomega.Expect(firstPending).NotTo(gomega.BeNil())

	firstAwaiting := receiveProgramPromptSnapshot(ctx, updates, func(snapshot programPromptSnapshot) bool {
		return snapshot.approvalEventSummary == "write_file: denied.txt" &&
			snapshot.approvalSummary == "write_file: denied.txt" &&
			strings.HasPrefix(snapshot.status, "Awaiting approval (build mode)")
	})
	gomega.Expect(firstAwaiting.status).To(gomega.Equal("Awaiting approval (build mode): /approve or /deny"))

	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/deny")})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	var deniedOutcome programToolApprovalOutcome
	gomega.Eventually(ctx, toolOutcomes).Should(gomega.Receive(&deniedOutcome))
	gomega.Expect(deniedOutcome.request).To(gomega.Equal(deniedRequest))
	gomega.Expect(deniedOutcome.result.Summary).To(gomega.Equal("Mutation approval denied"))
	gomega.Expect(deniedOutcome.result.Error).To(gomega.ContainSubstring("operator did not approve"))
	gomega.Expect(deniedOutcome.result.ChangedPaths).To(gomega.BeEmpty())
	_, err := os.Stat(filepath.Join(root, deniedRequest.Path))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "denied write must not create its target")

	deniedSnapshot := receiveProgramPromptSnapshot(ctx, updates, func(snapshot programPromptSnapshot) bool {
		return snapshot.status == "Denied: write_file: denied.txt"
	})
	gomega.Expect(deniedSnapshot.approvalSummary).To(gomega.BeEmpty())

	gomega.Eventually(ctx, func() string {
		pending := model.currentApproval()
		if pending == nil {
			return ""
		}

		return pending.summary()
	}).Should(gomega.Equal("write_file: approved.txt"))
	secondPending := model.currentApproval()
	gomega.Expect(secondPending).NotTo(gomega.BeNil())
	gomega.Expect(secondPending).NotTo(gomega.BeIdenticalTo(firstPending))

	secondAwaiting := receiveProgramPromptSnapshot(ctx, updates, func(snapshot programPromptSnapshot) bool {
		return snapshot.approvalEventSummary == "write_file: approved.txt" &&
			snapshot.approvalSummary == "write_file: approved.txt" &&
			strings.HasPrefix(snapshot.status, "Awaiting approval (build mode)")
	})
	gomega.Expect(secondAwaiting.status).To(gomega.Equal("Awaiting approval (build mode): /approve or /deny"))

	program.Send(approvalAwaitingMsg{pending: firstPending})
	lateAwaiting := receiveProgramPromptSnapshot(ctx, updates, func(snapshot programPromptSnapshot) bool {
		return snapshot.approvalEventSummary == "write_file: denied.txt"
	})
	gomega.Expect(lateAwaiting.approvalSummary).To(gomega.Equal("write_file: approved.txt"))
	gomega.Expect(lateAwaiting.status).To(gomega.Equal("Awaiting approval (build mode): /approve or /deny"))

	program.Send(approvalClearedMsg{pending: firstPending})
	lateClear := receiveProgramPromptSnapshot(ctx, updates, func(snapshot programPromptSnapshot) bool {
		return snapshot.approvalEventSummary == "write_file: denied.txt"
	})
	gomega.Expect(lateClear.approvalSummary).To(gomega.Equal("write_file: approved.txt"))
	gomega.Expect(lateClear.status).To(gomega.Equal("Awaiting approval (build mode): /approve or /deny"))

	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/approve")})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	var approvedOutcome programToolApprovalOutcome
	gomega.Eventually(ctx, toolOutcomes).Should(gomega.Receive(&approvedOutcome))
	gomega.Expect(approvedOutcome.request).To(gomega.Equal(approvedRequest))
	gomega.Expect(approvedOutcome.result.Error).To(gomega.BeEmpty())
	gomega.Expect(approvedOutcome.result.ChangedPaths).To(gomega.Equal([]string{"approved.txt"}))
	approvedBody, err := os.ReadFile(filepath.Join(root, approvedRequest.Path))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(approvedBody).To(gomega.Equal([]byte(approvedRequest.Content)))
	_, err = os.Stat(filepath.Join(root, deniedRequest.Path))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "later approval must not revive the denied write")

	approvedSnapshot := receiveProgramPromptSnapshot(ctx, updates, func(snapshot programPromptSnapshot) bool {
		return snapshot.status == "Approved: write_file: approved.txt"
	})
	gomega.Expect(approvedSnapshot.approvalSummary).To(gomega.BeEmpty())

	var completion programPromptSnapshot
	gomega.Eventually(ctx, completed).Should(gomega.Receive(&completion))
	gomega.Expect(completion.streaming).To(gomega.BeFalse())
	gomega.Expect(completion.status).To(gomega.Equal("Response complete."))
	gomega.Expect(completion.approvalSummary).To(gomega.BeEmpty())
	gomega.Expect(completion.messages).NotTo(gomega.BeEmpty())
	gomega.Expect(completion.messages[len(completion.messages)-1]).To(gomega.Equal(llama.ChatMessage{
		Role:    "assistant",
		Content: "The independently approved write completed.",
	}))

	failureAudit, err := os.ReadFile(filepath.Join(root, planDirName, "approval-lifecycle.failures.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(failureAudit).To(gomega.ContainSubstring(`"path":"denied.txt"`))
	gomega.Expect(failureAudit).To(gomega.ContainSubstring("operator did not approve"))
	gomega.Expect(failureAudit).NotTo(gomega.ContainSubstring(`"path":"approved.txt"`))
	committedAudit, err := os.ReadFile(filepath.Join(root, planDirName, "approval-lifecycle.diffs.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(committedAudit).To(gomega.ContainSubstring(`"path":"approved.txt"`))
	gomega.Expect(committedAudit).NotTo(gomega.ContainSubstring(`"path":"denied.txt"`))
	gomega.Expect(model.rollbackHistory).To(gomega.HaveLen(1), "only the approved write should own rollback authority")
	gomega.Expect(model.rollbackHistory[0].paths).To(gomega.Equal([]string{"approved.txt"}))
	undoSummary, err := executeLatestRollbackForTest(ctx, model)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(undoSummary).To(gomega.ContainSubstring("approved.txt"))
	_, err = os.Stat(filepath.Join(root, approvedRequest.Path))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "workspace-owned undo must remove the approved new file")
	gomega.Expect(model.currentApproval()).To(gomega.BeNil())
	model.promptExecutionMu.Lock()
	active := model.activePromptExecution
	model.promptExecutionMu.Unlock()
	gomega.Expect(active).To(gomega.BeNil(), "completed worker must release active execution ownership")

	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	var result bubbleProgramResult
	gomega.Eventually(ctx, finished).Should(gomega.Receive(&result))
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
	_, ok := result.model.(*observedProgramModel)
	gomega.Expect(ok).To(gomega.BeTrue(), "Bubble Tea should return the observing program Model")
	closed := make(chan error, 1)
	go func() { closed <- model.Close() }()
	gomega.Eventually(ctx, closed).Should(gomega.Receive(gomega.Succeed()))
	gomega.Expect(model.currentApproval()).To(gomega.BeNil(), "program shutdown must not resurrect resolved approval ownership")
})

var _ = ginkgo.It("binds mutation approval to the exact prompt session without cross-session grants", ginkgo.Label("development-scenario", "integration", "race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	newSession := func(sessionID string) (*promptExecutionSession, *workspace.Manager, string) {
		root := ginkgo.GinkgoT().TempDir()
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			workspace.ToolNameWriteFile: {{Pattern: "*", Decision: toolpolicy.Ask}},
		})
		model := &Model{
			cfg:           config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}},
			workspace:     manager,
			agentModeName: "build",
		}
		executionCtx := workspace.WithExecutionSessionID(ctx, sessionID)
		return newPromptExecutionSession(executionCtx, model, make(chan tea.Msg, 8), true), manager, root
	}
	receivePending := func(events <-chan tea.Msg) *pendingApproval {
		for {
			var msg tea.Msg
			gomega.Eventually(ctx, events).Should(gomega.Receive(&msg))
			if pendingMsg, ok := msg.(approvalPendingMsg); ok {
				return pendingMsg.pending
			}
		}
	}

	sessionA, managerA, rootA := newSession("session-a")
	sessionB, managerB, rootB := newSession("session-b")
	wrongDecision, wrongErr := (&tuiMutationApprover{session: sessionA, sessionID: "session-a"}).ApproveWorkspaceMutation(
		workspace.WithExecutionSessionID(ctx, "session-b"),
		workspace.MutationApprovalRequest{
			SessionID: "session-b",
			Tool:      workspace.ToolNameWriteFile,
			Targets: []workspace.MutationRequestTarget{{
				RequestedPath: "wrong-session.txt",
				Identity:      workspace.CanonicalWorkspacePath{CanonicalID: "wrong-session.txt"},
			}},
		},
	)
	gomega.Expect(wrongErr).To(gomega.MatchError(gomega.ContainSubstring("does not match the prompt session")))
	gomega.Expect(wrongDecision.Approved).To(gomega.BeFalse())
	gomega.Expect(sessionA.events).To(gomega.HaveLen(0), "wrong-session approval must fail before reaching the UI")
	emptySession, emptyManager, emptyRoot := newSession("")
	emptyResult := emptyManager.Execute(emptySession.promptExecutionContext, workspace.ToolRequest{
		Tool: workspace.ToolNameWriteFile, Path: "empty.txt", Content: "empty\n", Description: "must remain unattributed",
	})
	gomega.Expect(emptyResult.Error).To(gomega.ContainSubstring("does not match the prompt session"))
	gomega.Expect(emptySession.events).To(gomega.HaveLen(0), "explicit-empty identity must fail before reaching the UI")
	_, err := os.Stat(filepath.Join(emptyRoot, "empty.txt"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())

	gomega.Expect(os.Mkdir(filepath.Join(rootA, "nested"), 0o755)).To(gomega.Succeed())
	requestA := workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: "a.txt", Workdir: "nested", Content: "a\n", Description: "write session A"}
	requestB := workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: "b.txt", Content: "b\n", Description: "write session B"}
	resultA := make(chan workspace.ToolResult, 1)
	resultB := make(chan workspace.ToolResult, 1)
	go func() { resultA <- managerA.Execute(sessionA.promptExecutionContext, requestA) }()
	go func() { resultB <- managerB.Execute(sessionB.promptExecutionContext, requestB) }()

	pendingA := receivePending(sessionA.events)
	pendingB := receivePending(sessionB.events)
	gomega.Expect(pendingA).NotTo(gomega.BeIdenticalTo(pendingB))
	gomega.Expect(pendingA.summary()).To(gomega.Equal("write_file: a.txt"))
	gomega.Expect(pendingB.summary()).To(gomega.Equal("write_file: b.txt"))

	pendingA.response <- approvalResponse{allow: true}
	var approved workspace.ToolResult
	gomega.Eventually(ctx, resultA).Should(gomega.Receive(&approved))
	gomega.Expect(approved.Error).To(gomega.BeEmpty())
	gomega.Expect(resultB).To(gomega.HaveLen(0), "approving session A must not release session B")
	pendingB.response <- approvalResponse{}
	var denied workspace.ToolResult
	gomega.Eventually(ctx, resultB).Should(gomega.Receive(&denied))
	gomega.Expect(denied.Error).To(gomega.ContainSubstring("operator did not approve"))

	bodyA, err := os.ReadFile(filepath.Join(rootA, "nested", "a.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(bodyA).To(gomega.Equal([]byte("a\n")))
	sessionA.recordMutationRollback(sessionA.promptExecutionContext, approved)
	gomega.Expect(sessionA.rollbackHistory).To(gomega.HaveLen(1))
	gomega.Expect(sessionA.rollbackHistory[0].paths).To(gomega.Equal([]string{"nested/a.txt"}))
	rollbackResult, err := managerA.RollbackMutation(sessionA.promptExecutionContext, sessionA.rollbackHistory[0].rollback)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(rollbackResult.Status).To(gomega.Equal(workspace.MutationRollbackComplete))
	_, err = os.Stat(filepath.Join(rootA, "nested", "a.txt"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "rollback must use the canonical workdir-relative target")
	_, err = os.Stat(filepath.Join(rootB, "b.txt"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
})
