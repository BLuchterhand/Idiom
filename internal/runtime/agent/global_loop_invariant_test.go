package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Global repeated-request invariant", func() {
	type loopCase struct {
		name          string
		brief         workerruntime.WorkerRuntimeBrief
		modelRequests int
	}

	for _, tc := range []loopCase{
		{
			name:          "conversation reads",
			modelRequests: 4,
			brief: workerruntime.WorkerRuntimeBrief{
				Lane: "conversation", TaskFamily: "conversation", Contract: "direct_answer",
				Objective: "Inspect target.go.", PermissionProfile: "read_only_workspace",
				AllowedTools: []string{workspace.ToolNameReadFile},
			},
		},
		{
			name:          "target-write-ready implementation reads",
			modelRequests: 3,
			brief: workerruntime.WorkerRuntimeBrief{
				Lane: "implement", TaskFamily: "implementation", Contract: "implementation_result",
				Objective: "Update target.go.", PermissionProfile: "guarded_workspace_write",
				AllowedTools:     []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile},
				AllowedEditPaths: []string{"target.go"},
				TargetRefs:       []string{"target.go"},
			},
		},
	} {
		tc := tc
		ginkgo.It("stops an identical "+tc.name+" request at the unchanged three-call bound", func() {
			root := ginkgo.GinkgoT().TempDir()
			gomega.Expect(writeTestFile(root, "target.go", "package sample\n\nconst value = 1\n")).To(gomega.Succeed())

			const repeatedRead = `{"tool":"read_file","path":"target.go","start_line":1,"end_line":3}`
			var (
				requestMu sync.Mutex
				requests  int
			)
			client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
			client.EXPECT().SupportsNativeTools().Return(true).AnyTimes()
			client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
			client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
					requestMu.Lock()
					defer requestMu.Unlock()
					requests++
					switch {
					case requests <= 3:
						return llama.ChatMessage{Role: "assistant", Content: repeatedRead}, nil
					case requests == 4:
						return llama.ChatMessage{Role: "assistant", Content: "Stopped after the repeated-request gate using the gathered target.go evidence."}, nil
					default:
						return llama.ChatMessage{}, fmt.Errorf("unexpected model request %d after the global loop bound", requests)
					}
				},
			).AnyTimes()

			manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			var (
				gateCalls       int
				gateCount       int
				successfulReads int
				events          []RunEvent
			)
			runner := &Runner{
				Client: client, Workspace: manager,
				Policy:       RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6, MaxModelCalls: 8, FingerprintNudgeAt: 2},
				RuntimeBrief: tc.brief,
				DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
					gateCalls++
					gateCount = count
					gomega.Expect(req.Tool).To(gomega.Equal(workspace.ToolNameReadFile))
					return false
				},
				OnEvent: func(event RunEvent) { events = append(events, event) },
			}
			_, err := runner.Run(
				context.Background(),
				[]llama.ChatMessage{{Role: "user", Content: tc.brief.Objective}},
				llama.ChatOptions{}, nil, nil,
				func(event ToolResultEvent) {
					if event.Result.Tool == workspace.ToolNameReadFile && event.Result.Error == "" {
						successfulReads++
					}
				},
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(gateCalls).To(gomega.Equal(1))
			gomega.Expect(gateCount).To(gomega.Equal(3))
			gomega.Expect(successfulReads).To(gomega.Equal(1), "the second identical request is rejected and the third reaches the global doom bound")
			requestMu.Lock()
			requestCount := requests
			requestMu.Unlock()
			gomega.Expect(requestCount).To(gomega.Equal(tc.modelRequests))
			gomega.Expect(countEventsByRole(events, "doom_loop_guard")).To(gomega.Equal(1))
			gomega.Expect(countEventsByRole(events, "implementation_target_write_ready_read_loop_policy_deferred")).To(gomega.Equal(0))
		})
	}
})
