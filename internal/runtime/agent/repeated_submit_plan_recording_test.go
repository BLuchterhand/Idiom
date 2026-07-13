package agent

import (
	"context"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.It("records an identical rejected submit_plan before requesting a correction", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()
	client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()

	const invalidPlan = `{"tool":"submit_plan","rationale":"Create the generated implementation.","edits":[{"path":"outside.go","replace_all":true,"new_text":"package outside\n","purpose":"create an out-of-scope file"}]}`
	replies := []string{
		invalidPlan,
		invalidPlan,
		`{"tool":"submit_plan","rationale":"Create the authorized generated implementation.","edits":[{"path":"generated/new.go","replace_all":true,"new_text":"package generated\n\nconst Value = 1\n","purpose":"create the authorized implementation"}]}`,
		"The corrected implementation plan was submitted.",
	}
	calls := 0
	client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
			if calls >= len(replies) {
				return llama.ChatMessage{}, fmt.Errorf("unexpected model request %d", calls+1)
			}
			reply := replies[calls]
			calls++
			return llama.ChatMessage{Role: "assistant", Content: reply}, nil
		},
	).Times(len(replies))

	var submitResults []workspace.ToolResult
	runner := &Runner{
		Client: client,
		Workspace: workspace.NewManager(config.Workspace{
			Root: root, CWD: root, EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6, MaxModelCalls: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane: "plan_execute", TaskFamily: PlanThenExecutePlannerTaskFamily, Contract: "implementation_result",
			Objective: "Plan creation of generated/new.go.", PermissionProfile: "guarded_workspace_write",
			AllowedTools:       []string{workspace.ToolNameSubmitPlan},
			RequiredEditPaths:  []string{"generated/new.go"},
			AuthorizedNewFiles: []string{"generated/new.go"},
		},
	}

	answer, err := runner.Run(
		workspace.WithExecutionSessionID(context.Background(), "repeated-submit-recording"),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{}, nil, nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == workspace.ToolNameSubmitPlan {
				submitResults = append(submitResults, event.Result)
			}
		},
	)

	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(answer).To(gomega.Equal(replies[len(replies)-1]))
	gomega.Expect(calls).To(gomega.Equal(len(replies)))
	gomega.Expect(submitResults).To(gomega.HaveLen(3))
	gomega.Expect(submitResults[1].Summary).To(gomega.Equal("Repeated invalid submit_plan rejected"))
	gomega.Expect(submitResults[1].Facts).NotTo(gomega.BeNil())
	gomega.Expect(submitResults[1].Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionInvalidRequest))
	gomega.Expect(submitResults[2].Error).To(gomega.BeEmpty())
})
