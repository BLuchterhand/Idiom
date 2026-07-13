package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/workflows/planexecute"
)

const planExecuteSessionIdentitySpecTimeout = 10 * time.Second

var _ = ginkgo.It("keeps nested plan execute identity on a delayed tool callback after TUI state restoration", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(planExecuteSessionIdentitySpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	sibling := filepath.Join(parent, "sibling")
	gomega.Expect(os.MkdirAll(root, 0o755)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(sibling, 0o755)).To(gomega.Succeed())

	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 1
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.SessionID = "outer-session"
	cfg.MCP = nil

	delayedStarted := make(chan struct{})
	releaseDelayed := make(chan struct{})
	delayedResult := make(chan workspace.ToolResult, 1)
	observedSession := make(chan string, 1)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseDelayed) })
	}
	ginkgo.DeferCleanup(release)

	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	var execution *promptExecutionSession
	client.EXPECT().
		CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(callCtx context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
			go func() {
				close(delayedStarted)
				<-releaseDelayed
				delayedResult <- execution.workspace.Execute(callCtx, workspace.ToolRequest{
					Tool:    workspace.ToolNameSystemSearchAccess,
					Path:    "../sibling",
					Purpose: "verify delayed nested session attribution",
				})
			}()

			return llama.ChatMessage{Role: "assistant", Content: "Nested prompt completed."}, nil
		}).
		Times(1)
	client.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "session-identity-model"}}, nil).AnyTimes()
	client.EXPECT().Backend().Return("mock").AnyTimes()
	client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()

	model := New(cfg)
	ginkgo.DeferCleanup(func() { _ = model.Close() })
	model.client = client
	execution = newDetachedPromptExecutionSession(context.Background(), model)

	nestedCfg := cfg
	nestedCfg.Workspace.SessionID = "nested-session"
	approver := NewMockSystemSearchAccessApprover(gomock.NewController(t))
	approver.EXPECT().
		ApproveSystemSearchAccess(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req workspace.SystemSearchAccessRequest) (workspace.SystemSearchAccessDecision, error) {
			observedSession <- req.SessionID

			return workspace.SystemSearchAccessDecision{Approved: false}, nil
		}).
		Times(1)
	runnerCtx := workspace.WithSystemSearchAccessApprover(context.Background(), approver)
	runner := execution.newPlanExecutePromptRunner(runnerCtx)
	answer, err := runner(nestedCfg, "Answer once for the nested invocation.", planexecute.RunOptions{
		TaskFamily:     string(autonomy.TaskFamilyConversation),
		UseActiveModel: true,
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(answer).To(gomega.Equal("Nested prompt completed."))
	gomega.Expect(delayedStarted).To(gomega.BeClosed())

	// The nested prompt has returned and applyPlanExecutePromptRunnerState has
	// restored the TUI-owned config. The shared Manager was never rewritten.
	gomega.Expect(execution.cfg.Workspace.SessionID).To(gomega.Equal("outer-session"))
	gomega.Expect(execution.workspace.SessionID()).To(gomega.Equal("outer-session"))

	release()
	gomega.Eventually(ctx, observedSession).Should(gomega.Receive(gomega.Equal("nested-session")))
	gomega.Eventually(ctx, delayedResult).Should(gomega.Receive())
})

var _ = ginkgo.It("rebinds mutation approval to each nested plan-execute session without leaking an outer grant", ginkgo.Label("development-scenario", "e2e", "race-contract"), ginkgo.SpecTimeout(planExecuteSessionIdentitySpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.SessionID = "outer-session"
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	type nestedMutationOutcome struct {
		request workspace.ToolRequest
		result  workspace.ToolResult
	}
	requests := []workspace.ToolRequest{
		{Tool: workspace.ToolNameWriteFile, Path: "nested-approved.txt", Content: "approved\n", Description: "approve nested write"},
		{Tool: workspace.ToolNameWriteFile, Path: "nested-denied.txt", Content: "denied\n", Description: "deny independent nested write"},
	}
	outcomes := make(chan nestedMutationOutcome, len(requests))
	clientEntered := make(chan struct{}, len(requests))
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	var execution *promptExecutionSession
	for _, request := range requests {
		request := request
		client.EXPECT().
			CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(callCtx context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
				clientEntered <- struct{}{}
				result := execution.workspace.Execute(callCtx, request)
				outcomes <- nestedMutationOutcome{request: request, result: result}

				return llama.ChatMessage{Role: "assistant", Content: "Nested prompt completed."}, nil
			}).
			Times(1)
	}
	client.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "nested-approval-model"}}, nil).AnyTimes()
	client.EXPECT().Backend().Return("mock").AnyTimes()
	client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()

	model := New(cfg)
	ginkgo.DeferCleanup(func() { _ = model.Close() })
	model.client = client
	model.workspace.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameWriteFile: {{Pattern: "*", Decision: toolpolicy.Ask}},
	})
	execution = newPromptExecutionSession(
		workspace.WithExecutionSessionID(context.Background(), "outer-session"),
		model,
		make(chan tea.Msg, 16),
		true,
	)

	receivePending := func() *pendingApproval {
		for {
			var event tea.Msg
			gomega.Eventually(ctx, func() tea.Msg {
				select {
				case event = <-execution.events:
					return event
				case outcome := <-outcomes:
					ginkgo.Fail(fmt.Sprintf("nested mutation returned before approval: %s", outcome.result.Error))
					return nil
				default:
					return nil
				}
			}).ShouldNot(gomega.BeNil())
			if pending, ok := event.(approvalPendingMsg); ok {
				return pending.pending
			}
		}
	}
	runNested := func(sessionID string) <-chan error {
		result := make(chan error, 1)
		nestedConfig := cfg
		nestedConfig.Workspace.SessionID = sessionID
		go func() {
			_, err := execution.newPlanExecutePromptRunner(execution.promptExecutionContext)(nestedConfig, "Execute one nested request.", planexecute.RunOptions{
				TaskFamily:     string(autonomy.TaskFamilyConversation),
				UseActiveModel: true,
			}, io.Discard)
			result <- err
		}()

		return result
	}

	approvedRun := runNested("nested-approved-session")
	gomega.Eventually(ctx, clientEntered).Should(gomega.Receive())
	approvedPending := receivePending()
	gomega.Expect(approvedPending.summary()).To(gomega.Equal("write_file: nested-approved.txt"))
	approvedPending.response <- approvalResponse{allow: true}
	var approved nestedMutationOutcome
	gomega.Eventually(ctx, outcomes).Should(gomega.Receive(&approved))
	gomega.Expect(approved.request).To(gomega.Equal(requests[0]))
	gomega.Expect(approved.result.Error).To(gomega.BeEmpty())
	gomega.Eventually(ctx, approvedRun).Should(gomega.Receive(gomega.Succeed()))

	deniedRun := runNested("nested-denied-session")
	gomega.Eventually(ctx, clientEntered).Should(gomega.Receive())
	deniedPending := receivePending()
	gomega.Expect(deniedPending).NotTo(gomega.BeIdenticalTo(approvedPending))
	gomega.Expect(deniedPending.summary()).To(gomega.Equal("write_file: nested-denied.txt"))
	approvedPending.response <- approvalResponse{allow: true}
	gomega.Consistently(outcomes, 100*time.Millisecond).ShouldNot(gomega.Receive(), "a late approval from the prior nested session must not release the current request")
	deniedPending.response <- approvalResponse{}
	var denied nestedMutationOutcome
	gomega.Eventually(ctx, outcomes).Should(gomega.Receive(&denied))
	gomega.Expect(denied.request).To(gomega.Equal(requests[1]))
	gomega.Expect(denied.result.Error).To(gomega.ContainSubstring("operator did not approve"))
	gomega.Eventually(ctx, deniedRun).Should(gomega.Receive(gomega.Succeed()))

	body, err := os.ReadFile(filepath.Join(root, "nested-approved.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(body).To(gomega.Equal([]byte("approved\n")))
	_, err = os.Stat(filepath.Join(root, "nested-denied.txt"))
	gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
	gomega.Expect(execution.cfg.Workspace.SessionID).To(gomega.Equal("outer-session"))
})

var _ = ginkgo.It("keeps an old turn approver isolated from a newer TUI turn and closes its event stream safely", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(planExecuteSessionIdentitySpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	sibling := filepath.Join(parent, "sibling")
	gomega.Expect(os.MkdirAll(root, 0o755)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(sibling, 0o755)).To(gomega.Succeed())

	cfg := config.Default()
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.SessionID = "shared-manager-session"
	cfg.MCP = nil

	model := New(cfg)
	ginkgo.DeferCleanup(func() { _ = model.Close() })
	oldCtx, cancelOld := context.WithCancel(context.Background())
	newCtx, cancelNew := context.WithCancel(context.Background())
	ginkgo.DeferCleanup(cancelOld)
	ginkgo.DeferCleanup(cancelNew)
	oldEvents := make(chan tea.Msg, 8)
	newEvents := make(chan tea.Msg, 8)
	oldSession := newPromptExecutionSession(oldCtx, model, oldEvents, false)
	_ = newPromptExecutionSession(newCtx, model, newEvents, false)

	result := make(chan workspace.ToolResult, 1)
	go func() {
		result <- model.workspace.Execute(oldSession.promptExecutionContext, workspace.ToolRequest{
			Tool:    workspace.ToolNameSystemSearchAccess,
			Path:    "../sibling",
			Purpose: "exercise old-turn approval routing",
		})
	}()

	var oldEvent tea.Msg
	gomega.Eventually(ctx, oldEvents).Should(gomega.Receive(&oldEvent))
	gomega.Expect(oldEvent).To(gomega.BeAssignableToTypeOf(approvalPendingMsg{}))
	gomega.Consistently(newEvents, 100*time.Millisecond).ShouldNot(gomega.Receive())

	cancelOld()
	var canceledResult workspace.ToolResult
	gomega.Eventually(ctx, result).Should(gomega.Receive(&canceledResult))
	gomega.Expect(canceledResult.Error).NotTo(gomega.BeEmpty())

	oldSession.closeUIEvents()
	late := model.workspace.Execute(oldSession.promptExecutionContext, workspace.ToolRequest{
		Tool:    workspace.ToolNameSystemSearchAccess,
		Path:    "../sibling",
		Purpose: "prove a closed old-turn stream cannot route into the new turn",
	})
	gomega.Expect(late.Error).NotTo(gomega.BeEmpty())
	gomega.Consistently(newEvents, 100*time.Millisecond).ShouldNot(gomega.Receive())
})
