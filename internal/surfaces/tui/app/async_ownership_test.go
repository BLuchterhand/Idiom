package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
)

const asyncOwnershipSpecTimeout = 10 * time.Second

var _ = ginkgo.It("keeps classifier launch admission independent between Models", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	controller := gomock.NewController(t)
	firstClient := llamamocks.NewMockLLMClient(controller)
	secondClient := llamamocks.NewMockLLMClient(controller)
	firstUnavailable := errors.New("first classifier unavailable")
	secondUnavailable := errors.New("second classifier unavailable")
	firstClient.EXPECT().ListModels(gomock.Any()).Return(nil, firstUnavailable).Times(2)
	secondClient.EXPECT().ListModels(gomock.Any()).Return(nil, secondUnavailable).Times(2)
	classifierConfig := config.RouteClassifier{
		Enabled:             true,
		AutoStartServer:     true,
		StartTimeoutSeconds: 1,
	}
	first := &Model{cfg: config.Config{RouteClassifier: classifierConfig}, summaryClient: firstClient}
	second := &Model{cfg: config.Config{RouteClassifier: classifierConfig}, summaryClient: secondClient}

	firstExecution := snapshotHistorySummaryExecution(first)
	secondExecution := snapshotHistorySummaryExecution(second)

	gomega.Expect(firstExecution.server.state).NotTo(gomega.BeIdenticalTo(secondExecution.server.state))
	gomega.Expect(firstExecution.server.ensure(ctx)).To(gomega.MatchError(firstUnavailable))
	gomega.Expect(secondExecution.server.ensure(ctx)).To(gomega.MatchError(secondUnavailable))
	_, firstCanLaunchAgain := firstExecution.server.state.begin()
	_, secondCanLaunchAgain := secondExecution.server.state.begin()
	gomega.Expect(firstCanLaunchAgain).To(gomega.BeFalse())
	gomega.Expect(secondCanLaunchAgain).To(gomega.BeFalse())
})

var _ = ginkgo.It("cancels a blocked classifier probe before Close can launch its server", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	probeStarted := make(chan struct{})
	probeCanceled := make(chan struct{})
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	client.EXPECT().ListModels(gomock.Any()).DoAndReturn(func(callCtx context.Context) ([]llama.ModelInfo, error) {
		close(probeStarted)
		<-callCtx.Done()
		close(probeCanceled)

		return nil, callCtx.Err()
	}).Times(1)

	root := t.TempDir()
	marker := filepath.Join(root, "classifier-started")
	binary := filepath.Join(root, "classifier-server")
	script := "#!/bin/sh\nprintf started > '" + marker + "'\nwhile :; do sleep 1; done\n"
	gomega.Expect(os.WriteFile(binary, []byte(script), 0o755)).To(gomega.Succeed())
	serverConfig := config.LlamaServer{
		Kind:    config.BackendMLX,
		Binary:  binary,
		Model:   "classifier-test-model",
		LogFile: filepath.Join(root, "classifier.log"),
		Host:    "127.0.0.1",
		Port:    1,
		CtxSize: 1024,
	}
	model := &Model{
		cfg: config.Config{RouteClassifier: config.RouteClassifier{
			Enabled:             true,
			AutoStartServer:     true,
			StartTimeoutSeconds: 30,
		}},
		summaryClient:     client,
		summaryController: llama.NewServerController(serverConfig, llama.NewClient("http://127.0.0.1:1", time.Second)),
		events:            make(chan tea.Msg),
		history: historyState{entries: []historyEntry{{
			revision: 1,
			sender:   "user",
			contents: []string{"summarize this entry"},
		}}},
	}
	ginkgo.DeferCleanup(func() { _ = model.Close() })

	model.startHistorySummary(0, model.history.entries[0])
	gomega.Eventually(ctx, probeStarted).Should(gomega.BeClosed())
	closed := make(chan error, 1)
	go func() { closed <- model.Close() }()
	gomega.Eventually(ctx, closed).Should(gomega.Receive(gomega.Succeed()))
	gomega.Expect(probeCanceled).To(gomega.BeClosed())
	_, statErr := os.Stat(marker)
	gomega.Expect(statErr).To(gomega.MatchError(os.ErrNotExist))
})

var _ = ginkgo.It("correlates history summaries to structural and entry revisions", ginkgo.Label("race-contract"), func() {
	model := &Model{
		history: historyState{
			revision: 7,
			entries: []historyEntry{{
				revision: 2,
				sender:   "user",
				contents: []string{"original question"},
				summary:  "fallback",
			}},
		},
	}
	originalEpoch := model.history.revision
	model.beginAgentHistoryTurn()
	gomega.Expect(model.history.revision).To(gomega.Equal(originalEpoch), "tail append must preserve existing entry indexes")

	_, _ = model.handleHistorySummary(historySummaryMsg{
		index:           0,
		historyRevision: originalEpoch,
		entryRevision:   2,
		summary:         "classified question",
	})
	gomega.Expect(model.history.entries[0].summary).To(gomega.Equal("classified question"))

	model.history.entries[0].contents = append(model.history.entries[0].contents, "follow-up")
	model.markHistoryEntryChanged(&model.history.entries[0])
	_, _ = model.handleHistorySummary(historySummaryMsg{
		index:           0,
		historyRevision: originalEpoch,
		entryRevision:   2,
		summary:         "stale content result",
	})
	gomega.Expect(model.history.entries[0].summary).To(gomega.Equal("classified question"))

	model.history.revision++
	_, _ = model.handleHistorySummary(historySummaryMsg{
		index:           0,
		historyRevision: originalEpoch,
		entryRevision:   model.history.entries[0].revision,
		summary:         "stale structural result",
	})
	gomega.Expect(model.history.entries[0].summary).To(gomega.Equal("classified question"))
})

var _ = ginkgo.It("cancels an admitted delayed history refresh during Close", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	events := make(chan tea.Msg, 1)
	model := &Model{
		events:                   events,
		streaming:                true,
		currentTurnHistoryActive: true,
		panelTab:                 panelTabHistory,
		history: historyState{
			panelOpen:  true,
			renderLast: time.Now().Add(time.Hour),
		},
	}
	ginkgo.DeferCleanup(func() { _ = model.Close() })

	model.requestHistoryPanelRender(false, false)
	gomega.Expect(model.history.renderPending).To(gomega.BeTrue())
	closed := make(chan error, 1)
	go func() { closed <- model.Close() }()
	gomega.Eventually(ctx, closed).Should(gomega.Receive(gomega.Succeed()))
	gomega.Expect(events).NotTo(gomega.Receive())
})

var _ = ginkgo.It("snapshots transcript save input and cancels its blocked result send on Close", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	saveDir := t.TempDir()
	model := &Model{
		cfg:      config.Config{Chat: config.Chat{SaveDir: saveDir}},
		events:   make(chan tea.Msg),
		messages: []llama.ChatMessage{{Role: "user", Content: "saved snapshot"}},
	}
	ginkgo.DeferCleanup(func() { _ = model.Close() })

	model.saveTranscriptAsync()
	updated, _ := model.Update(promptTranscriptMsg{
		message: llama.ChatMessage{Role: "assistant", Content: "later mutation"},
	})
	model = updated.(*Model)
	var savedPath string
	gomega.Eventually(ctx, func() string {
		matches, _ := filepath.Glob(filepath.Join(saveDir, "*.md"))
		if len(matches) == 1 {
			return matches[0]
		}

		return ""
	}).ShouldNot(gomega.BeEmpty())
	matches, err := filepath.Glob(filepath.Join(saveDir, "*.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(matches).To(gomega.HaveLen(1))
	savedPath = matches[0]

	closed := make(chan error, 1)
	go func() { closed <- model.Close() }()
	gomega.Eventually(ctx, closed).Should(gomega.Receive(gomega.Succeed()))
	body, err := os.ReadFile(savedPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(string(body)).To(gomega.ContainSubstring("saved snapshot"))
	gomega.Expect(string(body)).NotTo(gomega.ContainSubstring("later mutation"))
})

var _ = ginkgo.It("rejects a manual compaction result after the transcript changes", ginkgo.Label("development-scenario", "integration", "contract", "race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	started := make(chan struct{})
	release := make(chan struct{})
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	client.EXPECT().CompleteChat(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (string, error) {
		close(started)
		<-release

		return "manual digest", nil
	}).Times(1)
	model := &Model{
		cfg:    config.Config{LlamaServer: config.LlamaServer{CtxSize: 10000}},
		client: client,
		events: make(chan tea.Msg, 1),
		messages: []llama.ChatMessage{
			{Role: "user", Content: "original goal"},
			{Role: "assistant", Content: "older work"},
			{Role: "user", Content: "latest request"},
		},
	}
	var releaseOnce sync.Once
	releaseCompaction := func() {
		releaseOnce.Do(func() { close(release) })
	}
	ginkgo.DeferCleanup(func() {
		releaseCompaction()
		_ = model.Close()
	})

	model.compactHistoryAsync()
	gomega.Eventually(ctx, started).Should(gomega.BeClosed())
	model.slashClear("", nil)
	releaseCompaction()
	var done compactHistoryDoneMsg
	gomega.Eventually(ctx, model.events).Should(gomega.Receive(&done))
	_, _ = model.handleCompactHistoryDone(done)
	gomega.Expect(model.messages).To(gomega.BeEmpty())
	gomega.Expect(model.messagesRevision).To(gomega.Equal(uint64(1)))
	gomega.Expect(model.Close()).To(gomega.Succeed())
})

var _ = ginkgo.It("cancels automatic compaction with the active prompt context", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	started := make(chan struct{})
	canceled := make(chan struct{})
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	client.EXPECT().CompleteChat(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(callCtx context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (string, error) {
		close(started)
		<-callCtx.Done()
		close(canceled)

		return "", callCtx.Err()
	}).Times(1)
	history := make([]llama.ChatMessage, 40)
	history[0] = llama.ChatMessage{Role: "user", Content: "preserve this goal"}
	for index := 1; index < len(history); index++ {
		history[index] = llama.ChatMessage{Role: "assistant", Content: "prior work"}
	}
	model := &Model{
		cfg:      config.Config{LlamaServer: config.LlamaServer{CtxSize: 10000}},
		client:   client,
		events:   make(chan tea.Msg, 8),
		messages: history,
	}
	ginkgo.DeferCleanup(func() { _ = model.Close() })
	model.startPromptExecution(func(turnCtx context.Context, worker *promptExecutionSession) (string, error) {
		_ = worker.requestMessages(turnCtx)

		return "", turnCtx.Err()
	})
	gomega.Eventually(ctx, started).Should(gomega.BeClosed())
	closed := make(chan error, 1)
	go func() { closed <- model.Close() }()
	gomega.Eventually(ctx, closed).Should(gomega.Receive(gomega.Succeed()))
	gomega.Expect(canceled).To(gomega.BeClosed())
	model.promptExecutionMu.Lock()
	active := model.activePromptExecution
	model.promptExecutionMu.Unlock()
	gomega.Expect(active).To(gomega.BeNil())
})

var _ = ginkgo.It("cancels session-owned server readiness work with the prompt context", ginkgo.Label("development-scenario", "integration", "contract", "race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	probeStarted := make(chan struct{})
	probeCanceled := make(chan struct{})
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	client.EXPECT().ListModels(gomock.Any()).DoAndReturn(func(callCtx context.Context) ([]llama.ModelInfo, error) {
		close(probeStarted)
		<-callCtx.Done()
		close(probeCanceled)

		return nil, callCtx.Err()
	}).Times(1)
	turnCtx, cancel := context.WithCancel(context.Background())
	ginkgo.DeferCleanup(cancel)
	session := newDetachedPromptExecutionSession(turnCtx, &Model{client: client})
	completed := make(chan error, 1)
	go func() {
		completed <- session.ensureServerReady(session.promptExecutionContext)
	}()

	gomega.Eventually(ctx, probeStarted).Should(gomega.BeClosed())
	cancel()
	var readinessErr error
	gomega.Eventually(ctx, completed).Should(gomega.Receive(&readinessErr))
	gomega.Expect(readinessErr).To(gomega.HaveOccurred())
	gomega.Expect(probeCanceled).To(gomega.BeClosed())
})
