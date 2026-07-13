package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
	"Implementation completion lifecycle",
	ginkgo.Label("e2e", "integration", "development-scenario", "race-contract"),
	func() {
		ginkgo.It("records and resolves every rejected multi-path write from typed facts", func() {
			const (
				firstPath     = "internal/feature/label.go"
				secondPath    = "internal/feature/status.go"
				unrelatedPath = "docs/notes.md"
			)
			patch := strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: " + firstPath,
				"@@",
				"-old label",
				"+new label",
				"*** Update File: " + secondPath,
				"@@",
				"-draft",
				"+ready",
				"*** End Patch",
			}, "\n")
			rejected := resultWithRejectedRequestTarget(workspace.ToolRequest{
				Tool:    workspace.ToolNameApplyPatch,
				Content: patch,
			}, resultWithToolRejectionDisposition(workspace.ToolResult{
				Tool:    workspace.ToolNameApplyPatch,
				Summary: "Tool not available in current runtime contract",
				Error:   "apply_patch is not in the current allowed tool surface",
				Facts: &toolfacts.ToolResultFacts{
					RejectedWritePaths: []string{firstPath, secondPath},
				},
			}, toolfacts.ToolRejectionDeferredByPhase))

			gomega.Expect(rejected.Facts).NotTo(gomega.BeNil())
			gomega.Expect(rejected.Facts.RejectedWritePaths).To(gomega.Equal([]string{firstPath, secondPath}))
			gomega.Expect(unresolvedRejectedMutationPaths([]workspace.ToolResult{rejected}, nil)).To(gomega.Equal([]string{firstPath, secondPath}))

			_, firstWrite := committedMutationResultForTest(firstPath)
			_, unrelatedWrite := committedMutationResultForTest(unrelatedPath)
			results := []workspace.ToolResult{
				rejected,
				firstWrite,
				unrelatedWrite,
				{Tool: workspace.ToolNameSourceSanity, Summary: "Source sanity passed"},
			}
			gomega.Expect(unresolvedRejectedMutationPaths(results, nil)).To(gomega.Equal([]string{secondPath}))

			_, secondWrite := committedMutationResultForTest(secondPath)
			results = append(results, secondWrite)
			gomega.Expect(unresolvedRejectedMutationPaths(results, nil)).To(gomega.BeEmpty())
		})

		ginkgo.It("keeps in-scope writes callable until the final changed set is verified", func() {
			root := newGoModuleTestWorkspace()
			const (
				firstPath  = "internal/feature/label.go"
				secondPath = "internal/feature/status.go"
			)

			gomega.Expect(writeTestFile(root, firstPath, strings.Join([]string{
				"package feature",
				"",
				`const Label = "old"`,
			}, "\n"))).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, secondPath, strings.Join([]string{
				"package feature",
				"",
				`const Status = "draft"`,
			}, "\n"))).To(gomega.Succeed())

			replies := []string{
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"update the first implementation path"}`, firstPath, `const Label = "old"`, `const Label = "new"`),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":3,"named_gap":"refresh source evidence after the first committed mutation"}`, secondPath),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"update the second implementation path"}`, secondPath, `const Status = "draft"`, `const Status = "ready"`),
				fmt.Sprintf(`{"tool":"source_sanity","path":%q}`, firstPath),
				fmt.Sprintf(`{"tool":"source_sanity","path":%q}`, secondPath),
				"Implemented both feature constants.\n\nChanged paths:\n- " + firstPath + "\n- " + secondPath + "\n\nVerification: source sanity passed for both changed Go files.",
			}
			type modelRequest struct {
				messages []llama.ChatMessage
				opts     llama.ChatOptions
			}

			var (
				requestMu     sync.Mutex
				modelRequests []modelRequest
			)
			client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
			client.EXPECT().SupportsNativeTools().Return(true).AnyTimes()
			client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
			client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (llama.ChatMessage, error) {
					capturedMessages := append([]llama.ChatMessage(nil), messages...)
					opts.Tools = append([]llama.Tool(nil), opts.Tools...)

					requestMu.Lock()
					requestIndex := len(modelRequests)
					modelRequests = append(modelRequests, modelRequest{messages: capturedMessages, opts: opts})
					requestMu.Unlock()

					if requestIndex >= len(replies) {
						lastMessage := messages[len(messages)-1]
						return llama.ChatMessage{}, fmt.Errorf(
							"unexpected model request %d after %s message: %s",
							requestIndex+1,
							lastMessage.Role,
							lastMessage.Content,
						)
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
			manager.MarkPathObserved(firstPath)
			manager.MarkPathObserved(secondPath)
			firstSource := manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: workspace.ToolNameReadFile, Path: firstPath, StartLine: 1, EndLine: 3,
			})
			secondSource := manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: workspace.ToolNameReadFile, Path: secondPath, StartLine: 1, EndLine: 3,
			})
			gomega.Expect(firstSource.Error).To(gomega.BeEmpty())
			gomega.Expect(secondSource.Error).To(gomega.BeEmpty())

			var (
				events  []RunEvent
				results []workspace.ToolResult
			)
			runner := &Runner{
				Client:      client,
				Workspace:   manager,
				Policy:      RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8, MaxModelCalls: 12},
				SeedResults: []workspace.ToolResult{firstSource, secondSource},
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					Lane:                 "implement",
					TaskFamily:           "implementation",
					Contract:             "implementation_result",
					Objective:            "Update both feature constants.",
					PermissionProfile:    "guarded_workspace_write",
					AllowedTools:         []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile, workspace.ToolNameSourceSanity, workspace.ToolNameShell},
					AllowedEditPaths:     []string{firstPath, secondPath},
					TargetRefs:           []string{firstPath, secondPath},
					TargetPacket:         []string{"exact current source is available for both target paths"},
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
					results = append(results, event.Result)
				},
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(answer).To(gomega.Equal(replies[len(replies)-1]))
			requestMu.Lock()
			requestCount := len(modelRequests)
			requestMu.Unlock()
			gomega.Expect(requestCount).To(gomega.Equal(len(replies)))

			firstBody, err := os.ReadFile(filepath.Join(root, firstPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			secondBody, err := os.ReadFile(filepath.Join(root, secondPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(firstBody)).To(gomega.ContainSubstring(`const Label = "new"`))
			gomega.Expect(string(secondBody)).To(gomega.ContainSubstring(`const Status = "ready"`))

			var secondPathAttempts, completedSecondPath int
			for _, result := range results {
				if result.Tool != workspace.ToolNameEditFile || !strings.Contains(result.Summary+result.Output+result.Error, secondPath) {
					continue
				}
				secondPathAttempts++
				if strings.TrimSpace(result.Error) == "" {
					if _, changed := newToolResultEvidenceProjection([]workspace.ToolResult{result}).ChangedPathSet()[secondPath]; changed {
						completedSecondPath++
					}
				}
			}
			gomega.Expect(secondPathAttempts).To(gomega.Equal(1), "the second path should execute once without a phase denial/retry")
			gomega.Expect(completedSecondPath).To(gomega.Equal(1))

			joinedEvents := eventRolesFor(events)
			gomega.Expect(joinedEvents).To(gomega.ContainElement("post_write_verification_required"))
			gomega.Expect(joinedEvents).NotTo(gomega.ContainElement("final_answer_blocker_override"))
		},
		)

		ginkgo.It("keeps unscoped multi-path writes callable through final-set verification", func() {
			root := newGoModuleTestWorkspace()
			const (
				firstPath  = "internal/feature/label.go"
				secondPath = "internal/feature/status.go"
			)

			gomega.Expect(writeTestFile(root, firstPath, strings.Join([]string{
				"package feature",
				"",
				`const Label = "old"`,
			}, "\n"))).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, secondPath, strings.Join([]string{
				"package feature",
				"",
				`const Status = "draft"`,
			}, "\n"))).To(gomega.Succeed())

			replies := []string{
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"update the first implementation path"}`, firstPath, `const Label = "old"`, `const Label = "new"`),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":3,"named_gap":"refresh source evidence after the first committed mutation"}`, secondPath),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"update the second implementation path"}`, secondPath, `const Status = "draft"`, `const Status = "ready"`),
				fmt.Sprintf(`{"tool":"source_sanity","path":%q}`, firstPath),
				fmt.Sprintf(`{"tool":"source_sanity","path":%q}`, secondPath),
				"Implemented both feature constants.\n\nChanged paths:\n- " + firstPath + "\n- " + secondPath + "\n\nVerification: source sanity passed for both changed Go files.",
			}
			type modelRequest struct {
				messages []llama.ChatMessage
				opts     llama.ChatOptions
			}

			var (
				requestMu     sync.Mutex
				modelRequests []modelRequest
			)
			client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
			client.EXPECT().SupportsNativeTools().Return(true).AnyTimes()
			client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
			client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (llama.ChatMessage, error) {
					capturedMessages := append([]llama.ChatMessage(nil), messages...)
					opts.Tools = append([]llama.Tool(nil), opts.Tools...)

					requestMu.Lock()
					requestIndex := len(modelRequests)
					modelRequests = append(modelRequests, modelRequest{messages: capturedMessages, opts: opts})
					requestMu.Unlock()

					if requestIndex >= len(replies) {
						lastMessage := messages[len(messages)-1]
						return llama.ChatMessage{}, fmt.Errorf(
							"unexpected model request %d after %s message: %s",
							requestIndex+1,
							lastMessage.Role,
							lastMessage.Content,
						)
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
			manager.MarkPathObserved(firstPath)
			manager.MarkPathObserved(secondPath)
			firstSource := manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: workspace.ToolNameReadFile, Path: firstPath, StartLine: 1, EndLine: 3,
			})
			secondSource := manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: workspace.ToolNameReadFile, Path: secondPath, StartLine: 1, EndLine: 3,
			})
			gomega.Expect(firstSource.Error).To(gomega.BeEmpty())
			gomega.Expect(secondSource.Error).To(gomega.BeEmpty())

			var (
				events  []RunEvent
				results []workspace.ToolResult
			)
			runner := &Runner{
				Client:      client,
				Workspace:   manager,
				Policy:      RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8, MaxModelCalls: 12},
				SeedResults: []workspace.ToolResult{firstSource, secondSource},
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					Lane:                 "implement",
					TaskFamily:           "implementation",
					Contract:             "implementation_result",
					Objective:            "Update both feature constants.",
					PermissionProfile:    "guarded_workspace_write",
					AllowedTools:         []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile, workspace.ToolNameSourceSanity, workspace.ToolNameShell},
					TargetRefs:           []string{firstPath, secondPath},
					TargetPacket:         []string{"exact current source is available for both target paths"},
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
					results = append(results, event.Result)
				},
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			requestMu.Lock()
			capturedRequests := append([]modelRequest(nil), modelRequests...)
			requestMu.Unlock()

			postFirstWriteTools := make([]string, 0, len(capturedRequests[1].opts.Tools))
			for _, tool := range capturedRequests[1].opts.Tools {
				postFirstWriteTools = append(postFirstWriteTools, tool.Function.Name)
			}
			gomega.Expect(postFirstWriteTools).To(gomega.ContainElements(
				workspace.ToolNameReadFile,
				workspace.ToolNameEditFile,
				workspace.ToolNameSourceSanity,
			))

			gomega.Expect(results).To(gomega.HaveLen(5), "unexpected tool results: %#v", results)
			resultTools := make([]string, 0, len(results))
			var committedPaths []string
			for _, result := range results {
				resultTools = append(resultTools, result.Tool)
				gomega.Expect(result.Error).To(gomega.BeEmpty())
				if result.Facts != nil {
					gomega.Expect(result.Facts.RejectionDisposition).To(gomega.BeEmpty())
				}
				if result.Tool == workspace.ToolNameEditFile {
					gomega.Expect(result.CommitReceipt()).NotTo(gomega.BeNil())
					for _, effect := range result.CommitReceipt().DurableProjection().Effects {
						committedPaths = append(committedPaths, effect.CanonicalID)
					}
				}
			}
			gomega.Expect(resultTools).To(gomega.Equal([]string{
				workspace.ToolNameEditFile,
				workspace.ToolNameReadFile,
				workspace.ToolNameEditFile,
				workspace.ToolNameSourceSanity,
				workspace.ToolNameSourceSanity,
			}))
			gomega.Expect(committedPaths).To(gomega.Equal([]string{firstPath, secondPath}))
			for index, path := range []string{firstPath, secondPath} {
				verification := workspace.AnalyzeVerificationResult(results[index+3])
				gomega.Expect(verification.Successful).To(gomega.BeTrue())
				gomega.Expect(verification.ScopePaths).To(gomega.ContainElement(path))
			}
			gomega.Expect(capturedRequests).To(gomega.HaveLen(len(replies)), "unexpected model request count: %d", len(capturedRequests))
			gomega.Expect(answer).To(gomega.Equal(replies[len(replies)-1]), "unexpected terminal answer: %q", answer)

			firstBody, err := os.ReadFile(filepath.Join(root, firstPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			secondBody, err := os.ReadFile(filepath.Join(root, secondPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(firstBody)).To(gomega.ContainSubstring(`const Label = "new"`))
			gomega.Expect(string(secondBody)).To(gomega.ContainSubstring(`const Status = "ready"`))

			joinedEvents := eventRolesFor(events)
			gomega.Expect(joinedEvents).To(gomega.ContainElement("post_write_verification_required"))
			gomega.Expect(joinedEvents).NotTo(gomega.ContainElements(
				"tool_rejected",
				"rejection_retry_blocked",
				"final_answer_blocker_override",
			))
		})

		ginkgo.It("requires a fresh model-issued shell verification after a script commit", func() {
			root := newGoModuleTestWorkspace()
			const targetPath = "scripts/check.sh"
			gomega.Expect(writeTestFile(root, targetPath, strings.Join([]string{
				"#!/usr/bin/env bash",
				"echo old",
			}, "\n"))).To(gomega.Succeed())

			verificationCommand := "bash -n " + targetPath
			replies := []string{
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"update the script output"}`, targetPath, "echo old", "echo ready"),
				fmt.Sprintf(`{"tool":"shell","command":%q,"workdir":"."}`, verificationCommand),
				"Updated the script output.\n\nChanged paths:\n- " + targetPath + "\n\nVerification: `" + verificationCommand + "` passed.",
			}
			type modelRequest struct {
				opts llama.ChatOptions
			}

			var (
				requestMu     sync.Mutex
				modelRequests []modelRequest
			)
			client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
			client.EXPECT().SupportsNativeTools().Return(true).AnyTimes()
			client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
			client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (llama.ChatMessage, error) {
					opts.Tools = append([]llama.Tool(nil), opts.Tools...)

					requestMu.Lock()
					requestIndex := len(modelRequests)
					modelRequests = append(modelRequests, modelRequest{opts: opts})
					requestMu.Unlock()

					if requestIndex >= len(replies) {
						lastMessage := messages[len(messages)-1]
						return llama.ChatMessage{}, fmt.Errorf(
							"unexpected model request %d after %s message: %s",
							requestIndex+1,
							lastMessage.Role,
							lastMessage.Content,
						)
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
			manager.MarkPathObserved(targetPath)
			source := manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: workspace.ToolNameReadFile, Path: targetPath, StartLine: 1, EndLine: 2,
			})
			gomega.Expect(source.Error).To(gomega.BeEmpty())

			var (
				toolRequests []ToolRequestEvent
				toolResults  []ToolResultEvent
			)
			runner := &Runner{
				Client:      client,
				Workspace:   manager,
				Policy:      RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5, MaxModelCalls: 8},
				SeedResults: []workspace.ToolResult{source},
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					Lane:                 "implement",
					TaskFamily:           "implementation",
					Contract:             "implementation_result",
					Objective:            "Update the script output and verify its syntax.",
					PermissionProfile:    "guarded_workspace_write",
					AllowedTools:         []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile, workspace.ToolNameShell},
					AllowedEditPaths:     []string{targetPath},
					TargetRefs:           []string{targetPath},
					TargetPacket:         []string{"exact current script source is available"},
					RequiredEvidence:     []string{"changed_paths_or_noop_reason", "verification_status"},
					VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
				},
			}

			answer, err := runner.Run(
				context.Background(),
				[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
				llama.ChatOptions{MaxTokens: 8192},
				nil,
				func(event ToolRequestEvent) {
					toolRequests = append(toolRequests, event)
				},
				func(event ToolResultEvent) {
					toolResults = append(toolResults, event)
				},
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			requestMu.Lock()
			capturedRequests := append([]modelRequest(nil), modelRequests...)
			requestMu.Unlock()
			gomega.Expect(capturedRequests).To(gomega.HaveLen(len(replies)))
			postCommitTools := make([]string, 0, len(capturedRequests[1].opts.Tools))
			for _, tool := range capturedRequests[1].opts.Tools {
				postCommitTools = append(postCommitTools, tool.Function.Name)
			}
			gomega.Expect(postCommitTools).To(gomega.ContainElement(workspace.ToolNameShell))

			gomega.Expect(toolRequests).To(gomega.HaveLen(2))
			gomega.Expect(toolRequests[0].Request.Tool).To(gomega.Equal(workspace.ToolNameEditFile))
			gomega.Expect(toolRequests[0].Request.Path).To(gomega.Equal(targetPath))
			gomega.Expect(toolRequests[1].Request.Tool).To(gomega.Equal(workspace.ToolNameShell))
			gomega.Expect(toolRequests[1].Request.Command).To(gomega.Equal(verificationCommand))

			gomega.Expect(toolResults).To(gomega.HaveLen(2))
			gomega.Expect(toolResults[0].Result.Tool).To(gomega.Equal(workspace.ToolNameEditFile))
			gomega.Expect(toolResults[0].Result.Error).To(gomega.BeEmpty())
			gomega.Expect(toolResults[0].Result.CommitReceipt()).NotTo(gomega.BeNil())
			gomega.Expect(toolResults[1].Result.Tool).To(gomega.Equal(workspace.ToolNameShell))
			gomega.Expect(toolResults[1].Result.Error).To(gomega.BeEmpty())
			verification := workspace.AnalyzeVerificationResult(toolResults[1].Result)
			gomega.Expect(verification.Successful).To(gomega.BeTrue())
			gomega.Expect(verification.ScopePaths).To(gomega.ContainElement(targetPath))

			gomega.Expect(answer).To(gomega.Equal(replies[len(replies)-1]))
			body, err := os.ReadFile(filepath.Join(root, targetPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(body)).To(gomega.ContainSubstring("echo ready"))
		})
	},
)
