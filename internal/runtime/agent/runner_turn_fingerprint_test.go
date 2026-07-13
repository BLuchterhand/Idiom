package agent

import (
	"context"
	"fmt"
	"strings"
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

var _ = ginkgo.It("DiagnosticLogFailureDoesNotPoisonCurrentRequestFingerprint", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	gomega.Expect(writeTestFile(root, "README.md", "# Project overview\n\nhello\n")).To(gomega.Succeed())

	replies := []llama.ChatMessage{
		{Content: `{"tool":"read_file","path":"README.md","start_line":1,"end_line":3}`},
		{Content: `{"tool":"read_file","path":"logs/conversations/recent.jsonl"}`},
		{Content: `{"tool":"read_file","path":"README.md","start_line":1,"end_line":3}`},
		{Content: "The project overview says hello (`README.md`)."},
	}
	var (
		replyMu    sync.Mutex
		replyIndex int
	)
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()
	client.EXPECT().Backend().Return("test").AnyTimes()
	client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, []llama.ChatMessage, llama.ChatOptions) (llama.ChatMessage, error) {
			replyMu.Lock()
			defer replyMu.Unlock()
			if replyIndex >= len(replies) {
				return llama.ChatMessage{}, fmt.Errorf("unexpected model request %d", replyIndex+1)
			}
			reply := replies[replyIndex]
			replyIndex++
			return reply, nil
		},
	).AnyTimes()

	results := make([]workspace.ToolResult, 0, 4)
	events := make([]RunEvent, 0, 8)
	runner := &Runner{
		Client: client,
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			Objective:         "What does the project overview say?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{workspace.ToolNameReadFile},
		},
		SeedResults: []workspace.ToolResult{
			readFileSourceWindowResult("logs/conversations/recent.jsonl", 1, 51, 211, "1:{\"type\":\"message\"}\n"),
			readFileSourceWindowResult("logs/conversations/recent.jsonl", 52, 131, 211, "52:{\"type\":\"message\"}\n"),
			readFileSourceWindowResult("logs/conversations/recent.jsonl", 132, 211, 211, "132:{\"type\":\"message\"}\n"),
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What does the project overview say?"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	successfulOverviewReads := 0
	for _, result := range results {
		if result.Tool != workspace.ToolNameReadFile || result.Error != "" {
			continue
		}
		for _, path := range newToolResultEvidenceRecord(result).ReadPaths() {
			if NormalizeRunnerPath(path) == "README.md" {
				successfulOverviewReads++
				break
			}
		}
	}
	gomega.Expect(successfulOverviewReads).To(gomega.Equal(2),
		"the diagnostic-log rejection must not poison the prior README request fingerprint; results=%#v events=%s",
		results,
		formatRunEventsForTest(events),
	)
	var staleFingerprintNudge string
	for _, event := range events {
		if event.Role == "rejection_retry_nudge" && strings.Contains(event.Content, "Diagnostic log read coverage exhausted") {
			staleFingerprintNudge = event.Content
			break
		}
	}
	gomega.Expect(staleFingerprintNudge).To(gomega.BeEmpty(),
		"the prior request must not inherit the diagnostic-log rejection; events=%s",
		formatRunEventsForTest(events),
	)
})
