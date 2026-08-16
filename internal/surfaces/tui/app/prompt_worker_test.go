package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/promptstack"
	"idiom/internal/workflows/planexecute"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

// waitForPromptWorkerResult drives the same typed events the Bubble Tea loop
// would process. The worker result is guaranteed to be last by the private
// worker-event relay; the context is only an interruptible test failure fuse.
func waitForPromptWorkerResult(ctx context.Context, model *Model) *Model {
	ginkgo.GinkgoHelper()
	model, result := drivePromptWorkerResult(ctx, model)
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prompt worker failed: %v", result.err))

	return model
}

func drivePromptWorkerResult(ctx context.Context, model *Model) (*Model, promptWorkerResultMsg) {
	ginkgo.GinkgoHelper()

	for {
		msg := receivePromptWorkerEvent(ctx, model.events)
		updated, _ := model.Update(msg)
		model = updated.(*Model)

		if result, ok := msg.(promptWorkerResultMsg); ok {
			return model, result
		}
	}
}

func receivePromptWorkerEvent(ctx context.Context, events <-chan tea.Msg) tea.Msg {
	ginkgo.GinkgoHelper()

	var msg tea.Msg
	gomega.Eventually(ctx, events).Should(gomega.Receive(&msg))

	return msg
}

func closePromptWorkerModel(ctx context.Context, model *Model) {
	ginkgo.GinkgoHelper()

	closed := make(chan error, 1)
	go func() { closed <- model.Close() }()
	gomega.Eventually(ctx, closed).Should(gomega.Receive(gomega.Succeed()))
}

func executeLatestRollbackForTest(ctx context.Context, model *Model) (string, error) {
	ginkgo.GinkgoHelper()
	if model == nil || model.workspace == nil {
		return "", fmt.Errorf("workspace is unavailable")
	}

	registry := model.ensureRollbackRegistry()
	entry, err := registry.beginLatest()
	if err != nil {
		return "", err
	}
	result, summary, err := executeUndo(ctx, model.workspace, entry)
	registry.finish(entry.id, result.Status == workspace.MutationRollbackComplete)
	model.rollbackHistory = registry.snapshot()

	return summary, err
}

func promptWorkerServerHelperScript(startedPath string, stoppedPath string) string {
	ginkgo.GinkgoHelper()

	executable, err := os.Executable()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return fmt.Sprintf(
		"#!/bin/sh\nPROMPT_WORKER_SERVER_HELPER=1 PROMPT_WORKER_STARTED_PATH=%q PROMPT_WORKER_STOPPED_PATH=%q exec %q -test.run='^TestPromptWorkerServerHelperProcess$'\n",
		startedPath,
		stoppedPath,
		executable,
	)
}

func TestPromptWorkerServerHelperProcess(t *testing.T) {
	if os.Getenv("PROMPT_WORKER_SERVER_HELPER") != "1" {
		return
	}

	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopped)

	startedPath := os.Getenv("PROMPT_WORKER_STARTED_PATH")
	if err := os.WriteFile(startedPath, []byte("started"), 0o600); err != nil {
		t.Fatalf("write started marker: %v", err)
	}

	<-stopped
	if stoppedPath := os.Getenv("PROMPT_WORKER_STOPPED_PATH"); stoppedPath != "" {
		if err := os.WriteFile(stoppedPath, []byte("stopped"), 0o600); err != nil {
			t.Fatalf("write stopped marker: %v", err)
		}
	}
}

var _ = ginkgo.It("detaches every mutable execution snapshot collection", func() {
	historyOverride := []llama.ChatMessage{{Role: "user", Content: "history"}}
	provenance := &planartifact.PlanEditAnchorProvenance{Path: "before.go"}
	pins := newPinnedFileRegistry()
	thinking := true
	implementer := &config.LlamaServer{ExtraArgs: []string{"--implementer-before"}}
	live := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{ExtraArgs: []string{"--planner-before"}},
			Implementer: implementer,
			Chat: config.Chat{
				SystemPromptFiles: []string{"system-before"},
				EnableThinking:    &thinking,
			},
			Workspace: config.Workspace{
				ToolRules:     map[string][]config.ToolRule{"shell": {{Pattern: "before", Decision: "allow"}}},
				ExternalTools: []config.ExternalToolDef{{Name: "before", Args: []string{"arg-before"}}},
			},
			MCP: []config.MCPServer{{Name: "before", Args: []string{"mcp-before"}, Headers: map[string]string{"header": "before"}}},
		},
		plannerSpec:  config.LlamaServer{ExtraArgs: []string{"planner-spec-before"}},
		executorSpec: config.LlamaServer{ExtraArgs: []string{"executor-spec-before"}},
		events:       make(chan tea.Msg, 16),
		messages: []llama.ChatMessage{{
			Role:      "assistant",
			Content:   "before",
			ToolCalls: []llama.ToolCall{{ID: "original"}},
		}},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeSnapshotState: promptRuntimeSnapshotState{
				activeProfiles:         []string{"profile-a"},
				requestHistoryOverride: &historyOverride,
			},
			promptRuntimeCompletionState: promptRuntimeCompletionState{
				promptStack: promptstack.Stack{
					Sources:  []promptstack.Source{{DisplayName: "source-a"}},
					Warnings: []string{"warning-a"},
				},
				composedPromptSources: []promptstack.Source{{DisplayName: "composed-a"}},
				sessionAgg: sessionAggregate{
					toolCallCounts: map[string]int{"read_file": 1},
					discoveryCosts: map[string]int{"low": 1},
				},
				lastTurnSourceResults: []workspace.ToolResult{{
					Tool:  workspace.ToolNameReadFile,
					Paths: []string{"before.go"},
				}},
				plan: planRuntimeState{
					ledgerItems: map[string]workspace.TaskLedgerItem{
						"task": {ID: "task", Paths: []string{"before.go"}},
					},
					ledgerHits: map[string]map[string]bool{
						"task": {"before.go": true},
					},
					submittedEdits: map[string]planartifact.PlanEdit{
						"edit": {Path: "before.go", AnchorProvenance: provenance},
					},
				},
				rollbackHistory: []rollbackEntry{{sessionID: "before-session", paths: []string{"before.go"}}},
			},
		},
		planUI: planUIState{
			collapsedHeadings: map[string]bool{"heading": true},
			renderedRows:      []planRenderedRow{{SourceLineIdx: 1}},
		},
		thoughts: thoughtPanelState{
			entries: []thoughtPanelEntry{{step: 1, text: "before"}},
		},
		laneSwitchPopup: laneSwitchPopupState{
			options:     []laneSwitchOption{{key: "before"}},
			optionRowYs: []int{1},
		},
		pinnedFiles: pins,
	}

	worker := newDetachedPromptExecutionSession(context.Background(), live)

	worker.cfg.LlamaServer.ExtraArgs[0] = "--planner-worker"
	worker.cfg.Implementer.ExtraArgs[0] = "--implementer-worker"
	worker.cfg.Chat.SystemPromptFiles[0] = "system-worker"
	*worker.cfg.Chat.EnableThinking = false
	worker.cfg.Workspace.ToolRules["shell"][0].Pattern = "worker"
	worker.cfg.Workspace.ExternalTools[0].Args[0] = "arg-worker"
	worker.cfg.MCP[0].Args[0] = "mcp-worker"
	worker.cfg.MCP[0].Headers["header"] = "worker"
	worker.plannerSpec.ExtraArgs[0] = "planner-spec-worker"
	worker.executorSpec.ExtraArgs[0] = "executor-spec-worker"
	worker.messages[0].ToolCalls[0].ID = "worker"
	worker.activeProfiles[0] = "worker"
	worker.promptStack.Sources[0].DisplayName = "worker"
	worker.promptStack.Warnings[0] = "worker"
	worker.composedPromptSources[0].DisplayName = "worker"
	worker.sessionAgg.toolCallCounts["read_file"] = 9
	worker.sessionAgg.discoveryCosts["low"] = 9
	(*worker.requestHistoryOverride)[0].Content = "worker"
	worker.lastTurnSourceResults[0].Paths[0] = "worker.go"
	item := worker.plan.ledgerItems["task"]
	item.Paths[0] = "worker.go"
	worker.plan.ledgerItems["task"] = item
	worker.plan.ledgerHits["task"]["worker.go"] = true
	worker.plan.submittedEdits["edit"] = planartifact.PlanEdit{
		Path:             "worker.go",
		AnchorProvenance: &planartifact.PlanEditAnchorProvenance{Path: "worker.go"},
	}
	worker.rollbackHistory[0].paths[0] = "worker.go"

	gomega.Expect(live.cfg.LlamaServer.ExtraArgs).To(gomega.Equal([]string{"--planner-before"}))
	gomega.Expect(live.cfg.Implementer.ExtraArgs).To(gomega.Equal([]string{"--implementer-before"}))
	gomega.Expect(live.cfg.Chat.SystemPromptFiles).To(gomega.Equal([]string{"system-before"}))
	gomega.Expect(*live.cfg.Chat.EnableThinking).To(gomega.BeTrue())
	gomega.Expect(live.cfg.Workspace.ToolRules["shell"][0].Pattern).To(gomega.Equal("before"))
	gomega.Expect(live.cfg.Workspace.ExternalTools[0].Args).To(gomega.Equal([]string{"arg-before"}))
	gomega.Expect(live.cfg.MCP[0].Args).To(gomega.Equal([]string{"mcp-before"}))
	gomega.Expect(live.cfg.MCP[0].Headers["header"]).To(gomega.Equal("before"))
	gomega.Expect(live.plannerSpec.ExtraArgs).To(gomega.Equal([]string{"planner-spec-before"}))
	gomega.Expect(live.executorSpec.ExtraArgs).To(gomega.Equal([]string{"executor-spec-before"}))
	gomega.Expect(live.messages[0].ToolCalls[0].ID).To(gomega.Equal("original"))
	gomega.Expect(live.activeProfiles).To(gomega.Equal([]string{"profile-a"}))
	gomega.Expect(live.promptStack.Sources[0].DisplayName).To(gomega.Equal("source-a"))
	gomega.Expect(live.promptStack.Warnings[0]).To(gomega.Equal("warning-a"))
	gomega.Expect(live.composedPromptSources[0].DisplayName).To(gomega.Equal("composed-a"))
	gomega.Expect(live.sessionAgg.toolCallCounts["read_file"]).To(gomega.Equal(1))
	gomega.Expect(live.sessionAgg.discoveryCosts["low"]).To(gomega.Equal(1))
	gomega.Expect((*live.requestHistoryOverride)[0].Content).To(gomega.Equal("history"))
	gomega.Expect(live.lastTurnSourceResults[0].Paths[0]).To(gomega.Equal("before.go"))
	gomega.Expect(live.plan.ledgerItems["task"].Paths[0]).To(gomega.Equal("before.go"))
	gomega.Expect(live.plan.ledgerHits["task"]).NotTo(gomega.HaveKey("worker.go"))
	gomega.Expect(live.plan.submittedEdits["edit"].Path).To(gomega.Equal("before.go"))
	gomega.Expect(live.plan.submittedEdits["edit"].AnchorProvenance.Path).To(gomega.Equal("before.go"))
	gomega.Expect(live.planUI.collapsedHeadings["heading"]).To(gomega.BeTrue())
	gomega.Expect(live.planUI.renderedRows[0].SourceLineIdx).To(gomega.Equal(1))
	gomega.Expect(live.thoughts.entries[0].text).To(gomega.Equal("before"))
	gomega.Expect(live.laneSwitchPopup.options[0].key).To(gomega.Equal("before"))
	gomega.Expect(live.laneSwitchPopup.optionRowYs[0]).To(gomega.Equal(1))
	gomega.Expect(live.rollbackHistory[0].paths).To(gomega.Equal([]string{"before.go"}))
	gomega.Expect(worker.events).NotTo(gomega.BeIdenticalTo(live.events))
	gomega.Expect(worker.pinnedFiles).To(gomega.BeIdenticalTo(live.pinnedFiles), "pinned registry is the explicitly synchronized shared runtime dependency")
	gomega.Expect(worker.rollbackRegistry).To(gomega.BeIdenticalTo(live.rollbackRegistry), "rollback registry must survive a canceled worker completion")
})

var _ = ginkgo.DescribeTable("retains workspace rollback authority when a committed worker is canceled before completion projection",
	func(ctx ginkgo.SpecContext, markPostCommitFailure bool) {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "canceled-worker", EnableWrites: true}
		live := &Model{
			cfg:                  config.Config{Workspace: workspaceConfig},
			workspace:            workspace.NewManager(workspaceConfig),
			events:               make(chan tea.Msg, 16),
			backgroundOperations: newBackgroundOperationGroup(),
		}
		ginkgo.DeferCleanup(live.backgroundOperations.shutdown)

		committed := make(chan struct{})
		live.startPromptExecution(func(workerCtx context.Context, worker *promptExecutionSession) (string, error) {
			result := worker.workspace.Execute(workerCtx, workspace.ToolRequest{
				Tool:        workspace.ToolNameWriteFile,
				Path:        "committed.txt",
				Content:     "committed before cancellation\n",
				Description: "retain rollback authority across cancellation",
			})
			gomega.Expect(result.Error).To(gomega.BeEmpty())
			if markPostCommitFailure {
				result.Error = "synthetic post-commit projection failure"
			}
			worker.recordMutationRollback(workerCtx, result)
			close(committed)
			<-workerCtx.Done()

			return "", workerCtx.Err()
		})

		gomega.Eventually(ctx, committed).Should(gomega.BeClosed())
		live.cancelActivePromptExecution()
		gomega.Expect(live.rollbackHistory).To(gomega.BeEmpty(), "canceled worker completion must not be required to project rollback authority")
		gomega.Expect(live.ensureRollbackRegistry().snapshot()).To(gomega.HaveLen(1))

		summary, err := executeLatestRollbackForTest(ctx, live)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(summary).To(gomega.ContainSubstring("committed.txt"))
		_, err = os.Stat(filepath.Join(root, "committed.txt"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
		gomega.Expect(live.ensureRollbackRegistry().snapshot()).To(gomega.BeEmpty())
	},
	ginkgo.Entry("after a successful commit", false),
	ginkgo.Entry("after a committed result reports a later failure", true),
)

var _ = ginkgo.It("retains and surfaces a committed mutation whose rollback authority is unavailable", ginkgo.Label("race-contract"), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "visible-unavailable", EnableWrites: true}
	events := make(chan tea.Msg, 8)
	model := &Model{
		cfg:       config.Config{Workspace: workspaceConfig},
		workspace: workspace.NewManager(workspaceConfig),
		events:    events,
	}
	session := newPromptExecutionSession(context.Background(), model, events, true)
	result := model.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "unavailable.txt",
		Content:     "must remain visible\n",
		Description: "commit without attributed rollback identity",
	})
	gomega.Expect(result.Error).To(gomega.BeEmpty())

	session.recordMutationRollback(context.Background(), result)
	entries := model.ensureRollbackRegistry().snapshot()
	gomega.Expect(entries).To(gomega.HaveLen(1))
	gomega.Expect(entries[0].rollback).To(gomega.BeNil())
	gomega.Expect(entries[0].unavailableReason).To(gomega.Equal(rollbackUnattributed))
	var visible tea.Msg
	gomega.Eventually(ctx, events).Should(gomega.Receive(&visible))
	gomega.Expect(visible).To(gomega.Equal(infoMsg{text: "Undo unavailable for committed mutation: " + rollbackUnattributed}))

	gomega.Expect(model.slashUndo("/undo", []string{"/undo"})).To(gomega.BeFalse())
	gomega.Expect(model.status).To(gomega.Equal("Undo failed: latest committed mutation cannot be undone: " + rollbackUnattributed))
	body, err := os.ReadFile(filepath.Join(root, "unavailable.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(body).To(gomega.Equal([]byte("must remain visible\n")))
	gomega.Expect(model.ensureRollbackRegistry().snapshot()).To(gomega.HaveLen(1), "unavailable history must remain visible instead of falling back to a host preimage")
})

var _ = ginkgo.It("records rollback against the actual nested execution identity", ginkgo.Label("race-contract"), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "outer-identity", EnableWrites: true}
	model := &Model{
		cfg:       config.Config{Workspace: workspaceConfig},
		workspace: workspace.NewManager(workspaceConfig),
		events:    make(chan tea.Msg, 8),
	}
	session := newPromptExecutionSession(context.Background(), model, model.events, true)
	nestedCtx := workspace.WithExecutionSessionID(session.promptExecutionContext, "nested-identity")
	result := model.workspace.Execute(nestedCtx, workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "nested.txt",
		Content:     "nested commit\n",
		Description: "bind rollback to the nested invocation",
	})
	gomega.Expect(result.Error).To(gomega.BeEmpty())

	session.recordMutationRollback(nestedCtx, result)
	entries := model.ensureRollbackRegistry().snapshot()
	gomega.Expect(entries).To(gomega.HaveLen(1))
	gomega.Expect(entries[0].sessionID).To(gomega.Equal("nested-identity"))
	summary, err := executeLatestRollbackForTest(ctx, model)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(summary).To(gomega.Equal("Undone: restored nested.txt"))
	_, err = os.Stat(filepath.Join(root, "nested.txt"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
})

var _ = ginkgo.It("retains rollback authority when its owned context is canceled before a reverse effect", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "cancelable-undo", EnableWrites: true}
	events := make(chan tea.Msg, 16)
	manager := workspace.NewManager(workspaceConfig)
	model := &Model{
		cfg:       config.Config{Workspace: workspaceConfig},
		workspace: manager,
		events:    events,
	}
	session := newPromptExecutionSession(context.Background(), model, events, true)
	committed := manager.Execute(session.promptExecutionContext, workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "undo-me.txt",
		Content:     "committed\n",
		Description: "create an undo capability",
	})
	gomega.Expect(committed.Error).To(gomega.BeEmpty())
	session.recordMutationRollback(session.promptExecutionContext, committed)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := executeLatestRollbackForTest(canceledCtx, model)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("context canceled")))
	gomega.Expect(model.ensureRollbackRegistry().snapshot()).To(gomega.HaveLen(1), "pre-effect cancellation must retain the rollback capability")
	body, err := os.ReadFile(filepath.Join(root, "undo-me.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(body).To(gomega.Equal([]byte("committed\n")))

	summary, err := executeLatestRollbackForTest(ctx, model)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(summary).To(gomega.Equal("Undone: restored undo-me.txt"))
})

var _ = ginkgo.It("projects an asynchronously completed slash undo back onto live UI state", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "async-complete", EnableWrites: true}
	events := make(chan tea.Msg, 8)
	model := &Model{
		cfg:                  config.Config{Workspace: workspaceConfig},
		workspace:            workspace.NewManager(workspaceConfig),
		events:               events,
		backgroundOperations: newBackgroundOperationGroup(),
	}
	ginkgo.DeferCleanup(model.backgroundOperations.shutdown)
	session := newPromptExecutionSession(context.Background(), model, events, true)
	result := model.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "async.txt",
		Content:     "committed\n",
		Description: "exercise asynchronous undo completion",
	})
	gomega.Expect(result.Error).To(gomega.BeEmpty())
	session.recordMutationRollback(session.promptExecutionContext, result)

	model.slashUndo("/undo", []string{"/undo"})
	gomega.Expect(model.status).To(gomega.Equal("Undoing latest workspace mutation..."))
	var completed tea.Msg
	gomega.Eventually(ctx, events).Should(gomega.Receive(&completed))
	completion, ok := completed.(undoCompletedMsg)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("undo event = %T", completed))
	_, _ = model.handleUndoCompleted(completion)
	gomega.Expect(model.status).To(gomega.Equal("Undone: restored async.txt"))
	gomega.Expect(model.rollbackHistory).To(gomega.BeEmpty())
	_, err := os.Stat(filepath.Join(root, "async.txt"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
})

var _ = ginkgo.It("refreshes prompt and plan projections after an effectful undo", ginkgo.Label("development-scenario", "integration", "race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	promptPath := filepath.Join(root, "PROMPT.md")
	gomega.Expect(os.WriteFile(promptPath, []byte("original operator contract\n"), 0o644)).To(gomega.Succeed())
	workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "prompt-refresh", EnableWrites: true}
	cfg := config.Config{
		Chat:      config.Chat{SystemPromptFiles: []string{promptPath}},
		Workspace: workspaceConfig,
	}
	events := make(chan tea.Msg, 8)
	model := &Model{
		cfg:                  cfg,
		workspace:            workspace.NewManager(workspaceConfig),
		events:               events,
		backgroundOperations: newBackgroundOperationGroup(),
	}
	ginkgo.DeferCleanup(model.backgroundOperations.shutdown)
	gomega.Expect(model.reloadPromptStackWithProfiles(nil)).To(gomega.Succeed())
	gomega.Expect(model.promptStack.Text).To(gomega.ContainSubstring("original operator contract"))
	session := newPromptExecutionSession(context.Background(), model, events, true)
	result := model.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "PROMPT.md",
		Content:     "superseded operator contract\n",
		Description: "exercise prompt invalidation after undo",
	})
	gomega.Expect(result.Error).To(gomega.BeEmpty())
	session.recordMutationRollback(session.promptExecutionContext, result)
	gomega.Expect(model.reloadPromptStackWithProfiles(nil)).To(gomega.Succeed())
	gomega.Expect(model.promptStack.Text).To(gomega.ContainSubstring("superseded operator contract"))

	model.slashUndo("/undo", []string{"/undo"})
	var event tea.Msg
	gomega.Eventually(ctx, events).Should(gomega.Receive(&event))
	completion, ok := event.(undoCompletedMsg)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("undo event = %T", event))
	_, _ = model.handleUndoCompleted(completion)
	gomega.Expect(model.promptStack.Text).To(gomega.ContainSubstring("original operator contract"))
	gomega.Expect(model.promptStack.Text).NotTo(gomega.ContainSubstring("superseded operator contract"))
	nextTurn := newPromptExecutionSession(context.Background(), model, make(chan tea.Msg, 1), true)
	gomega.Expect(nextTurn.systemPrompt()).To(gomega.ContainSubstring("original operator contract"))
	gomega.Expect(nextTurn.systemPrompt()).NotTo(gomega.ContainSubstring("superseded operator contract"))
})

var _ = ginkgo.It("keeps the newest asynchronous undo presentation when completions arrive out of order", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "undo-order", EnableWrites: true}
	events := make(chan tea.Msg, 8)
	model := &Model{
		cfg:                  config.Config{Workspace: workspaceConfig},
		workspace:            workspace.NewManager(workspaceConfig),
		events:               events,
		backgroundOperations: newBackgroundOperationGroup(),
	}
	ginkgo.DeferCleanup(model.backgroundOperations.shutdown)
	session := newPromptExecutionSession(context.Background(), model, events, true)
	for _, path := range []string{"first.txt", "second.txt"} {
		result := model.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{
			Tool:        workspace.ToolNameWriteFile,
			Path:        path,
			Content:     path + "\n",
			Description: "create ordered undo authority",
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		session.recordMutationRollback(session.promptExecutionContext, result)
	}

	model.slashUndo("/undo", []string{"/undo"})
	var firstEvent tea.Msg
	gomega.Eventually(ctx, events).Should(gomega.Receive(&firstEvent))
	firstCompletion, ok := firstEvent.(undoCompletedMsg)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("first undo event = %T", firstEvent))
	gomega.Expect(firstCompletion.summary).To(gomega.ContainSubstring("second.txt"))

	model.slashUndo("/undo", []string{"/undo"})
	var secondEvent tea.Msg
	gomega.Eventually(ctx, events).Should(gomega.Receive(&secondEvent))
	secondCompletion, ok := secondEvent.(undoCompletedMsg)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("second undo event = %T", secondEvent))
	gomega.Expect(secondCompletion.summary).To(gomega.ContainSubstring("first.txt"))
	gomega.Expect(secondCompletion.generation).To(gomega.BeNumerically(">", firstCompletion.generation))

	_, _ = model.handleUndoCompleted(secondCompletion)
	gomega.Expect(model.status).To(gomega.Equal(secondCompletion.summary))
	_, _ = model.handleUndoCompleted(firstCompletion)
	gomega.Expect(model.status).To(gomega.Equal(secondCompletion.summary), "an older completion must not overwrite the newest operation presentation")
})

var _ = ginkgo.It("keeps a newer unavailable-undo status ahead of an older queued completion", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspaceConfig := config.Workspace{Root: root, CWD: root, SessionID: "undo-failed-order", EnableWrites: true}
	events := make(chan tea.Msg, 4)
	model := &Model{
		cfg:                  config.Config{Workspace: workspaceConfig},
		workspace:            workspace.NewManager(workspaceConfig),
		events:               events,
		backgroundOperations: newBackgroundOperationGroup(),
	}
	ginkgo.DeferCleanup(model.backgroundOperations.shutdown)
	session := newPromptExecutionSession(context.Background(), model, events, true)
	result := model.workspace.Execute(session.promptExecutionContext, workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        "completed.txt",
		Content:     "completed\n",
		Description: "queue an older undo completion",
	})
	gomega.Expect(result.Error).To(gomega.BeEmpty())
	session.recordMutationRollback(session.promptExecutionContext, result)
	model.slashUndo("/undo", []string{"/undo"})
	var event tea.Msg
	gomega.Eventually(ctx, events).Should(gomega.Receive(&event))
	completion, ok := event.(undoCompletedMsg)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("undo event = %T", event))

	model.ensureRollbackRegistry().append(rollbackEntry{
		paths:             []string{"unavailable.txt"},
		unavailableReason: rollbackUnavailableEvidence,
	})
	model.slashUndo("/undo", []string{"/undo"})
	failureStatus := model.status
	gomega.Expect(failureStatus).To(gomega.ContainSubstring("latest committed mutation cannot be undone"))
	_, _ = model.handleUndoCompleted(completion)
	gomega.Expect(model.status).To(gomega.Equal(failureStatus))
})

var _ = ginkgo.It("refreshes prompt state after partial rollback progress", ginkgo.Label("race-contract"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	promptPath := filepath.Join(root, "PROMPT.md")
	gomega.Expect(os.WriteFile(promptPath, []byte("before partial rollback\n"), 0o644)).To(gomega.Succeed())
	model := &Model{
		cfg: config.Config{Chat: config.Chat{SystemPromptFiles: []string{promptPath}}},
	}
	gomega.Expect(model.reloadPromptStackWithProfiles(nil)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(promptPath, []byte("restored by partial rollback\n"), 0o644)).To(gomega.Succeed())
	model.undoPresentationGeneration = 7

	_, _ = model.handleUndoCompleted(undoCompletedMsg{
		generation: 7,
		result: workspace.MutationRollbackResult{
			Status:  workspace.MutationRollbackPartial,
			Effects: []workspace.MutationRollbackEffect{{CanonicalID: "PROMPT.md", Kind: workspace.MutationRollbackRestoredFile}},
		},
		err: errors.New("later rollback target remained stale"),
	})
	gomega.Expect(model.promptStack.Text).To(gomega.ContainSubstring("restored by partial rollback"))
	gomega.Expect(model.status).To(gomega.ContainSubstring("Undo failed"))
})

var _ = ginkgo.It("publishes worker progress and transcript before the final result", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	live := &Model{events: make(chan tea.Msg, 8)}
	live.startPromptExecution(func(_ context.Context, worker *promptExecutionSession) (string, error) {
		worker.queueUIEvent(infoMsg{text: "working"})
		worker.appendPromptTranscriptMessage(llama.ChatMessage{Role: "assistant", Content: "done"})

		return "done", nil
	})

	first := receivePromptWorkerEvent(ctx, live.events)
	second := receivePromptWorkerEvent(ctx, live.events)
	third := receivePromptWorkerEvent(ctx, live.events)
	gomega.Expect(first).To(gomega.BeAssignableToTypeOf(infoMsg{}))
	gomega.Expect(second).To(gomega.BeAssignableToTypeOf(promptTranscriptMsg{}))
	result, ok := third.(promptWorkerResultMsg)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("final event = %T, want promptWorkerResultMsg", third))
	gomega.Expect(result.answer).To(gomega.Equal("done"))

	select {
	case trailing := <-live.events:
		ginkgo.Fail(fmt.Sprintf("worker event %T trailed the final result", trailing))
	default:
	}
})

var _ = ginkgo.It("preserves live backend state changed after the worker snapshot", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	live := New(featurePlannerTestConfig(t.TempDir()))
	defer live.Close()
	live.events = make(chan tea.Msg, 16)
	live.backendRuntimeState = backendRuntimeState{
		modelID:        "snapshot-model",
		modelDisplayID: "Snapshot Model",
	}

	snapshot := make(chan backendRuntimeState, 1)
	release := make(chan struct{})
	live.startPromptExecution(func(workerCtx context.Context, worker *promptExecutionSession) (string, error) {
		snapshot <- worker.backendRuntimeState
		select {
		case <-release:
		case <-workerCtx.Done():
			return "", workerCtx.Err()
		}
		worker.reportServerStatus(serverStatusMsg{
			running:   true,
			modelID:   "stale-worker-model",
			displayID: "Stale Worker Model",
			owned:     true,
		})
		worker.contextError = "worker completion transferred"

		return "complete", nil
	})

	var snapshotState backendRuntimeState
	gomega.Eventually(ctx, snapshot).Should(gomega.Receive(&snapshotState))
	gomega.Expect(snapshotState).To(gomega.Equal(backendRuntimeState{
		modelID:        "snapshot-model",
		modelDisplayID: "Snapshot Model",
	}))

	live.applyModelSwap("live-model")
	updated, _ := live.Update(serverStatusMsg{
		running:   true,
		modelID:   "live-status-model",
		displayID: "Live Status Model",
		owned:     true,
	})
	live = updated.(*Model)
	live.modelThinkingFloor = true
	close(release)
	live = waitForPromptWorkerResult(ctx, live)

	gomega.Expect(live.contextError).To(gomega.Equal("worker completion transferred"), "worker-owned completion state should still project")
	gomega.Expect(live.serverUp).To(gomega.BeTrue())
	gomega.Expect(live.managedServer).To(gomega.BeTrue())
	gomega.Expect(live.modelID).To(gomega.Equal("live-status-model"))
	gomega.Expect(live.modelDisplayID).To(gomega.Equal("Live Status Model"))
	gomega.Expect(live.modelThinkingFloor).To(gomega.BeTrue())
	gomega.Expect(live.modelSwapped).To(gomega.BeTrue())
})

var _ = ginkgo.It("projects prompt-started server ownership and stops the managed controller on close", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	startedPath := filepath.Join(root, "server-started")
	stoppedPath := filepath.Join(root, "server-stopped")
	scriptPath := filepath.Join(root, "managed-server")
	modelPath := filepath.Join(root, "model.gguf")
	logPath := filepath.Join(root, "managed-server.log")

	script := promptWorkerServerHelperScript(startedPath, stoppedPath)
	gomega.Expect(os.WriteFile(scriptPath, []byte(script), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(modelPath, []byte("model fixture"), 0o644)).To(gomega.Succeed())

	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := os.Stat(startedPath); err != nil {
			http.Error(w, "server process not started", http.StatusServiceUnavailable)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"prompt-started-model"}]}`)
	}))
	defer models.Close()

	cfg := featurePlannerTestConfig(root)
	cfg.Chat.AutoStartServer = true
	cfg.LlamaServer = config.LlamaServer{
		Kind:        config.BackendLlama,
		Model:       modelPath,
		DisplayName: "Prompt Started Model",
		BaseURL:     models.URL,
		Binary:      scriptPath,
		LogFile:     logPath,
	}
	live := New(cfg)
	ginkgo.DeferCleanup(func() { _ = live.Close() })
	gomega.Expect(live.controller).NotTo(gomega.BeNil())

	live.startPromptExecution(func(ctx context.Context, worker *promptExecutionSession) (string, error) {
		if err := worker.ensureServerReady(ctx); err != nil {
			return "", err
		}

		return "ready", nil
	})
	live = waitForPromptWorkerResult(ctx, live)

	gomega.Expect(live.serverUp).To(gomega.BeTrue())
	gomega.Expect(live.managedServer).To(gomega.BeTrue())
	gomega.Expect(live.modelID).To(gomega.Equal("prompt-started-model"))
	gomega.Expect(live.modelDisplayID).To(gomega.Equal("Prompt Started Model"))
	started, err := os.ReadFile(startedPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(started).To(gomega.Equal([]byte("started")))
	_, err = os.Stat(stoppedPath)
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())

	closePromptWorkerModel(ctx, live)
	stopped, err := os.ReadFile(stoppedPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(stopped).To(gomega.Equal([]byte("stopped")))
})

var _ = ginkgo.It("joins admitted background operations before Close returns", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	live := &Model{backgroundOperations: newBackgroundOperationGroup()}
	started := make(chan struct{})
	exited := make(chan struct{})
	accepted := live.backgroundOperations.start(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(exited)
	})
	gomega.Expect(accepted).To(gomega.BeTrue())
	gomega.Eventually(ctx, started).Should(gomega.BeClosed())

	closePromptWorkerModel(ctx, live)
	gomega.Expect(exited).To(gomega.BeClosed())
	gomega.Expect(live.backgroundOperations.start(func(context.Context) {})).To(gomega.BeFalse(), "Close must permanently close background-operation admission")
})

var _ = ginkgo.It("prevents implementer bootstrap from starting after Close", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	startedPath := filepath.Join(root, "implementer-started")
	scriptPath := filepath.Join(root, "implementer-server")
	modelPath := filepath.Join(root, "implementer.gguf")
	logPath := filepath.Join(root, "implementer.log")
	script := promptWorkerServerHelperScript(startedPath, "")
	gomega.Expect(os.WriteFile(scriptPath, []byte(script), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(modelPath, []byte("model fixture"), 0o644)).To(gomega.Succeed())

	client := llama.NewClient("http://127.0.0.1:1", time.Second)
	executorSpec := config.LlamaServer{
		Kind:    config.BackendLlama,
		Model:   modelPath,
		BaseURL: "http://127.0.0.1:1",
		Binary:  scriptPath,
		LogFile: logPath,
	}
	live := &Model{
		cfg: config.Config{Chat: config.Chat{
			AutoStartServer:        true,
			ServerStartTimeoutSecs: 1,
		}},
		plannerSpec:           config.LlamaServer{Kind: config.BackendDeepSeek, Model: "hosted-planner"},
		executorSpec:          executorSpec,
		implementerClient:     client,
		implementerController: llama.NewServerController(executorSpec, client),
		events:                make(chan tea.Msg, 1),
		backgroundOperations:  newBackgroundOperationGroup(),
	}
	bootstrap := live.bootstrapCommand()

	closePromptWorkerModel(ctx, live)
	result := bootstrap()
	gomega.Expect(result).To(gomega.BeAssignableToTypeOf(serverStatusMsg{}))
	_, err := os.Stat(startedPath)
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "closed background-operation admission must prevent a post-Close implementer start")
})

var _ = ginkgo.It("rejects a stale async server result while retaining ownership of the actual old controller", ginkgo.Label("development-scenario", "integration", "contract", "race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	startedPath := filepath.Join(root, "old-server-started")
	stoppedPath := filepath.Join(root, "old-server-stopped")
	scriptPath := filepath.Join(root, "old-server")
	modelPath := filepath.Join(root, "old-model.gguf")
	logPath := filepath.Join(root, "old-server.log")
	script := promptWorkerServerHelperScript(startedPath, stoppedPath)
	gomega.Expect(os.WriteFile(scriptPath, []byte(script), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(modelPath, []byte("model fixture"), 0o644)).To(gomega.Succeed())

	readyProbe := make(chan struct{})
	releaseReady := make(chan struct{})
	var probeOnce sync.Once
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-releaseReady:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"id":"stale-old-model"}]}`)
		default:
			if _, err := os.Stat(startedPath); err != nil {
				http.Error(w, "old server process not started", http.StatusServiceUnavailable)

				return
			}
			probeOnce.Do(func() { close(readyProbe) })
			http.Error(w, "hold old readiness", http.StatusServiceUnavailable)
		}
	}))
	defer models.Close()

	cfg := featurePlannerTestConfig(root)
	cfg.Chat.AutoStartServer = true
	cfg.LlamaServer = config.LlamaServer{
		Kind:        config.BackendLlama,
		Model:       modelPath,
		DisplayName: "Old Local Model",
		BaseURL:     models.URL,
		Binary:      scriptPath,
		LogFile:     logPath,
	}
	cfg.Backends = []byte(`{
  "default_profile": "swap",
  "profiles": {
    "swap": {
      "models": [
        {"alias": "glm-5.1", "kind": "zai", "model": "glm-5.1", "base_url": "https://api.z.ai/api/paas/v4", "api_key_env": "ZAI_API_KEY"},
        {"alias": "other-model", "kind": "zai", "model": "other-model", "base_url": "https://api.z.ai/api/paas/v4", "api_key_env": "ZAI_API_KEY"}
      ]
    }
  }
}`)
	live := New(cfg)
	ginkgo.DeferCleanup(func() { _ = live.Close() })
	oldController := live.controller
	writeTestBackendsJSON(t, root)

	live.startServerAsync()
	gomega.Eventually(ctx, readyProbe).Should(gomega.BeClosed())
	live.applyModelSwap("glm-5.1")
	close(releaseReady)

	msg := receivePromptWorkerEvent(ctx, live.events)
	status, ok := msg.(serverStatusMsg)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("async old server event = %T, want serverStatusMsg", msg))
	gomega.Expect(status.source).NotTo(gomega.BeNil())
	updated, _ := live.Update(status)
	live = updated.(*Model)

	gomega.Expect(live.modelID).To(gomega.Equal("glm-5.1"))
	gomega.Expect(live.modelDisplayID).To(gomega.Equal("glm-5.1"))
	gomega.Expect(live.controller).To(gomega.BeNil(), "hosted replacement should remain selected")
	gomega.Expect(live.managedServer).To(gomega.BeFalse(), "managedServer describes only the current backend")
	gomega.Expect(live.bootingServer).To(gomega.BeFalse())
	gomega.Expect(live.managedServers.snapshot()).To(gomega.ContainElement(oldController), "the actual old controller must remain Close-owned")
	started, err := os.ReadFile(startedPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(started).To(gomega.Equal([]byte("started")))
	gomega.Expect(oldController.IsRunning()).To(gomega.BeTrue(), "the stale result must not orphan or prematurely stop the old process")

	closePromptWorkerModel(ctx, live)
	stopped, err := os.ReadFile(stoppedPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(stopped).To(gomega.Equal([]byte("stopped")))
})

var _ = ginkgo.It("finishes with a visible failure when the live event stream is unavailable", func() {
	t := ginkgo.GinkgoT()
	live := New(featurePlannerTestConfig(t.TempDir()))
	defer live.Close()
	live.events = nil

	live.sendPromptAsync("must fail before execution")

	gomega.Expect(live.streaming).To(gomega.BeFalse())
	gomega.Expect(live.runtimeError).To(gomega.ContainSubstring(errPromptExecutionEventsUnavailable.Error()))
	gomega.Expect(live.status).To(gomega.Equal(live.runtimeError))
})

var _ = ginkgo.It("keeps live rendering independent while worker state mutates", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	live := &Model{
		events:   make(chan tea.Msg, 256),
		messages: []llama.ChatMessage{{Role: "assistant", Content: "stable live transcript"}},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{
				plan: planRuntimeState{
					ledgerHits: map[string]map[string]bool{"task": {"stable.go": true}},
				},
			},
		},
	}
	started := make(chan struct{})
	mutationsDone := make(chan struct{})
	release := make(chan struct{})

	live.startPromptExecution(func(workerCtx context.Context, worker *promptExecutionSession) (string, error) {
		close(started)
		for i := 0; i < 64; i++ {
			worker.messages = append(worker.messages, llama.ChatMessage{Role: "assistant", Content: fmt.Sprintf("worker-%d", i)})
			worker.plan.ledgerHits["task"][fmt.Sprintf("worker-%d.go", i)] = true
		}
		close(mutationsDone)
		select {
		case <-release:
		case <-workerCtx.Done():
			return "", workerCtx.Err()
		}
		worker.appendPromptTranscriptMessage(llama.ChatMessage{Role: "assistant", Content: "complete"})

		return "complete", nil
	})

	gomega.Eventually(ctx, started).Should(gomega.BeClosed())
	for {
		live.renderMessages(false)
		select {
		case <-mutationsDone:
			close(release)
			live = waitForPromptWorkerResult(ctx, live)
			gomega.Expect(live.messages).To(gomega.HaveLen(2))
			gomega.Expect(live.messages[1].Content).To(gomega.Equal("complete"))

			return
		case <-ctx.Done():
			ginkgo.Fail(fmt.Sprintf("rendering stress interrupted: %v", ctx.Err()))
		default:
		}
	}
})

var _ = ginkgo.It("transfers generic approval ownership to Update", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	live := &Model{events: make(chan tea.Msg, 16)}
	live.startPromptExecution(func(_ context.Context, worker *promptExecutionSession) (string, error) {
		approved := worker.requestApproval(workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: "date"}, "implement")
		if approved {
			return "approved", nil
		}

		return "approval ownership violation", nil
	})

	for live.currentApproval() == nil {
		msg := receivePromptWorkerEvent(ctx, live.events)
		updated, _ := live.Update(msg)
		live = updated.(*Model)
	}

	_, resolved := live.resolveApproval(true)
	gomega.Expect(resolved).To(gomega.BeTrue())
	live, result := drivePromptWorkerResult(ctx, live)
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.answer).To(gomega.Equal("approved"))
	gomega.Expect(live.currentApproval()).To(gomega.BeNil())
})

var _ = ginkgo.It("clears generic approval ownership when a worker is canceled", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	live := &Model{events: make(chan tea.Msg, 16)}
	live.startPromptExecution(func(_ context.Context, worker *promptExecutionSession) (string, error) {
		worker.requestApproval(workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: "date"}, "implement")

		return "canceled", nil
	})

	for live.currentApproval() == nil {
		msg := receivePromptWorkerEvent(ctx, live.events)
		updated, _ := live.Update(msg)
		live = updated.(*Model)
	}
	pending := live.currentApproval()

	live.cancelActivePromptExecution()
	gomega.Expect(live.currentApproval()).To(gomega.BeNil(), "cancel/join must clear the exact abandoned approval")

	clearProjected := false
	for len(live.events) > 0 {
		msg := <-live.events
		if clear, ok := msg.(approvalClearedMsg); ok {
			clearProjected = true
			gomega.Expect(clear.pending).To(gomega.BeIdenticalTo(pending))
		}
		updated, _ := live.Update(msg)
		live = updated.(*Model)
	}
	gomega.Expect(clearProjected).To(gomega.BeTrue(), "worker cancellation should project identity-checked approval cleanup when the event loop remains available")
	gomega.Expect(live.currentApproval()).To(gomega.BeNil())
})

var _ = ginkgo.It("transfers plan approval ownership to Update", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	live := New(featurePlannerTestConfig(t.TempDir()))
	defer live.Close()
	live.events = make(chan tea.Msg, 16)
	live.startPromptExecution(func(ctx context.Context, worker *promptExecutionSession) (string, error) {
		decision := worker.requestPlanExecuteApproval(ctx, planexecute.PlanApprovalRequest{Paths: []string{"target.go"}})
		if decision.Approved && decision.Blanket {
			return "blanket approved", nil
		}

		return "plan approval ownership violation", nil
	})

	for live.currentPlanApproval() == nil {
		msg := receivePromptWorkerEvent(ctx, live.events)
		updated, _ := live.Update(msg)
		live = updated.(*Model)
	}

	_, resolved := live.resolvePlanApproval(true, true)
	gomega.Expect(resolved).To(gomega.BeTrue())
	live, result := drivePromptWorkerResult(ctx, live)
	gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.answer).To(gomega.Equal("blanket approved"))
	gomega.Expect(live.currentPlanApproval()).To(gomega.BeNil())
	gomega.Expect(live.planExecuteApproveAllEdits).To(gomega.BeTrue())
})
