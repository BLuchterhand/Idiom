package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe(
	"Guarded implementation shell admission dispatch",
	ginkgo.Label("e2e", "integration", "race-contract"),
	func() {
		ginkgo.It("denies a mutating request before approval and executes fresh typed write and verification requests", func() {
			const (
				allowedPath         = "internal/allowed.go"
				outOfScopePath      = "internal/outside.txt"
				originalOutsideBody = "must remain unchanged\n"
				verificationMarker  = "verification request reached workspace dispatch"
			)
			verificationCommand := "go test -run '^TestVerificationSentinel$' -v ./..."
			mutatingCommand := "printf changed > " + outOfScopePath
			allowedBody := "package internal\n\nconst Allowed = true\n"

			root := newGoModuleTestWorkspace()
			gomega.Expect(writeTestFile(root, allowedPath, "package internal\n")).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, outOfScopePath, originalOutsideBody)).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, "verification/dispatch_test.go", strings.Join([]string{
				"package verification",
				"",
				"import \"testing\"",
				"",
				"func TestVerificationSentinel(t *testing.T) {",
				fmt.Sprintf("\tt.Log(%q)", verificationMarker),
				"}",
			}, "\n"))).To(gomega.Succeed())

			replies := []string{
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20}`, allowedPath),
				fmt.Sprintf(`{"tool":"shell","command":%q}`, mutatingCommand),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"record the authorized typed mutation"}`, allowedPath, "package internal\n", allowedBody),
				fmt.Sprintf(`{"tool":"shell","command":%q}`, verificationCommand),
				"Implemented the authorized typed change. Changed paths: internal/allowed.go. Verification passed.",
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
			var (
				events          []RunEvent
				results         []ToolResultEvent
				preExecuteCalls []workspace.ToolRequest
			)
			runner := &Runner{
				Client:    client,
				Workspace: manager,
				Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4, MaxModelCalls: 6},
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					Lane:                 "implement",
					TaskFamily:           "implementation",
					Contract:             "implementation_result",
					Objective:            "Exercise guarded shell admission while completing one authorized typed write.",
					PermissionProfile:    "guarded_workspace_write",
					AllowedTools:         []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile, workspace.ToolNameShell},
					AllowedEditPaths:     []string{allowedPath},
					VerificationPlan:     []string{verificationCommand},
					VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
				},
				OnEvent: func(event RunEvent) {
					events = append(events, event)
				},
				PreExecute: func(_ int, req workspace.ToolRequest) *workspace.ToolResult {
					preExecuteCalls = append(preExecuteCalls, req)
					if req.Tool == workspace.ToolNameShell && workspace.AnalyzeShellCommand(req.Command).SideEffect != "read_only" {
						return &workspace.ToolResult{
							Tool:    workspace.ToolNameShell,
							Summary: "Write approval required",
							Error:   "write approval required before running shell",
						}
					}
					return nil
				},
			}

			_, err := runner.Run(
				context.Background(),
				[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
				llama.ChatOptions{MaxTokens: 2048},
				nil,
				nil,
				func(event ToolResultEvent) {
					results = append(results, event)
				},
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(modelRequests).To(gomega.HaveLen(len(replies)), "the allowed verification must come from a fresh model request")
			gomega.Expect(results).To(gomega.HaveLen(4))
			gomega.Expect(preExecuteCalls).To(gomega.HaveLen(3))
			gomega.Expect(preExecuteCalls[0].Tool).To(gomega.Equal(workspace.ToolNameReadFile))
			gomega.Expect(preExecuteCalls[1].Tool).To(gomega.Equal(workspace.ToolNameEditFile))
			gomega.Expect(preExecuteCalls[2]).To(gomega.Equal(workspace.ToolRequest{Tool: workspace.ToolNameShell, Command: verificationCommand}))
			for _, request := range preExecuteCalls {
				gomega.Expect(request.Command).NotTo(gomega.Equal(mutatingCommand), "categorically denied shell mutation must not reach the generic approval hook")
			}

			denied := results[1]
			gomega.Expect(denied.Request.Tool).To(gomega.Equal(workspace.ToolNameShell))
			gomega.Expect(denied.Request.Command).To(gomega.Equal(mutatingCommand))
			gomega.Expect(denied.Result.Summary).To(gomega.Equal("Mutation-capable shell rejected"))
			gomega.Expect(denied.Result.Error).To(gomega.ContainSubstring("typed write tools"))
			gomega.Expect(denied.Result.Facts).NotTo(gomega.BeNil())
			gomega.Expect(denied.Result.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionPolicyDenied))
			gomega.Expect(denied.Result.ChangedPaths).To(gomega.BeEmpty())

			committed := results[2]
			gomega.Expect(committed.Request.Tool).To(gomega.Equal(workspace.ToolNameEditFile))
			gomega.Expect(committed.Result.Error).To(gomega.BeEmpty())

			verified := results[3]
			gomega.Expect(verified.Request.Tool).To(gomega.Equal(workspace.ToolNameShell))
			gomega.Expect(verified.Request.Command).To(gomega.Equal(verificationCommand))
			gomega.Expect(verified.Result.Error).To(gomega.BeEmpty())
			gomega.Expect(verified.Result.Output).To(gomega.ContainSubstring(verificationMarker), "normal workspace dispatch must return the real command output")
			gomega.Expect(workspace.AnalyzeVerificationResult(verified.Result).Successful).To(gomega.BeTrue())

			outsideBody, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(outOfScopePath)))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(outsideBody)).To(gomega.Equal(originalOutsideBody), "the denied request must not reach workspace execution")

			var sawPolicyDenialEvent bool
			for _, event := range events {
				if event.Role == "failure_feedback" && strings.Contains(event.Content, "Mutation-capable shell rejected") {
					sawPolicyDenialEvent = true
				}
			}
			gomega.Expect(sawPolicyDenialEvent).To(gomega.BeTrue(), "events=%#v", events)
		},
		)
	},
)
