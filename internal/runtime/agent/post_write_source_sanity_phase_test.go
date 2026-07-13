package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.It("executes a post-write source read unchanged before a fresh source-sanity request", ginkgo.Label("e2e", "integration", "completion_recovery_development"), func() {
	const target = "internal/labels/labels.go"
	root := newGoModuleTestWorkspace()
	gomega.Expect(writeTestFile(root, target, strings.Join([]string{
		"package labels",
		"",
		`const Label = "old"`,
	}, "\n"))).To(gomega.Succeed())

	replies := []string{
		fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"update the label"}`, target, `const Label = "old"`, `const Label = "new"`),
		fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":3,"named_gap":"re-read the changed source"}`, target),
		fmt.Sprintf(`{"tool":"source_sanity","path":%q}`, target),
		"Updated the label.\n\nChanged paths:\n- " + target + "\n\nVerification: source sanity passed for the changed Go file.",
	}
	var modelRequests [][]llama.ChatMessage
	client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
	client.EXPECT().SupportsNativeTools().Return(true).AnyTimes()
	client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
	client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, messages []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
			requestIndex := len(modelRequests)
			modelRequests = append(modelRequests, append([]llama.ChatMessage(nil), messages...))
			if requestIndex >= len(replies) {
				return llama.ChatMessage{}, fmt.Errorf("unexpected model request %d", requestIndex+1)
			}
			return llama.ChatMessage{Role: "assistant", Content: replies[requestIndex]}, nil
		},
	).AnyTimes()

	manager := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		Shell:        "/bin/sh",
		EnableWrites: true,
		EnableShell:  true,
	})
	manager.MarkPathObserved(target)
	seedSource := manager.Execute(context.Background(), workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: target, StartLine: 1, EndLine: 3,
	})
	gomega.Expect(seedSource.Error).To(gomega.BeEmpty())

	type observedResult struct {
		request workspace.ToolRequest
		result  workspace.ToolResult
	}
	var (
		events  []RunEvent
		results []observedResult
	)
	runner := &Runner{
		Client:      client,
		Workspace:   manager,
		SeedResults: []workspace.ToolResult{seedSource},
		Policy:      RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6, MaxModelCalls: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:                 "implement",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			Objective:            "Update the label.",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile, workspace.ToolNameSourceSanity, workspace.ToolNameShell},
			AllowedEditPaths:     []string{target},
			TargetRefs:           []string{target},
			RequiredEvidence:     []string{"changed_paths_or_noop_reason", "verification_status"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, observedResult{request: event.Request, result: event.Result})
		},
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(answer).To(gomega.Equal(replies[len(replies)-1]))
	gomega.Expect(modelRequests).To(gomega.HaveLen(len(replies)), "the exact read and source_sanity requests must each return through the normal model loop")

	var (
		executedReadIndex         = -1
		executedSourceSanityIndex = -1
		executedRead              *observedResult
		executedSourceSanity      *observedResult
	)
	for i := range results {
		observed := &results[i]
		switch observed.result.Tool {
		case workspace.ToolNameReadFile:
			if strings.TrimSpace(observed.result.Error) == "" {
				executedReadIndex = i
				executedRead = observed
			}
		case workspace.ToolNameSourceSanity:
			if strings.TrimSpace(observed.result.Error) == "" {
				executedSourceSanityIndex = i
				executedSourceSanity = observed
			}
		}
	}

	gomega.Expect(executedRead).NotTo(gomega.BeNil(), "the exact authorized post-write read must execute instead of being rejected or rewritten")
	gomega.Expect(executedRead.request.Tool).To(gomega.Equal(workspace.ToolNameReadFile))
	gomega.Expect(executedRead.request.Path).To(gomega.Equal(target))
	gomega.Expect(executedRead.result.Error).To(gomega.BeEmpty())
	gomega.Expect(executedRead.result.Output).To(gomega.ContainSubstring(`const Label = "new"`))

	gomega.Expect(executedSourceSanity).NotTo(gomega.BeNil(), "a fresh source_sanity request must execute through normal dispatch")
	gomega.Expect(executedSourceSanity.request.Tool).To(gomega.Equal(workspace.ToolNameSourceSanity))
	gomega.Expect(executedSourceSanity.request.Path).To(gomega.Equal(target))
	gomega.Expect(executedSourceSanityIndex).To(gomega.BeNumerically(">", executedReadIndex), "source_sanity must come from a later model request, after the exact read result")

	for _, event := range events {
		if event.Role == "tool_request_rewrite" {
			gomega.Expect(event.Content).NotTo(gomega.ContainSubstring(`"rewritten_tool":"source_sanity"`))
		}
	}
})
