package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/taskledger"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Terminal answer authority", func() {
	ginkgo.It("asks the model to synthesize before using typed closeout status", func() {
		client := llamamocks.NewMockLLMClient(gomock.NewController(ginkgo.GinkgoT()))
		client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()
		client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()
		client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, messages []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
				gomega.Expect(messages).NotTo(gomega.BeEmpty())
				gomega.Expect(messages[len(messages)-1].Content).To(gomega.ContainSubstring("Typed closeout status"))
				return llama.ChatMessage{
					Role:    "assistant",
					Content: "The model-owned final answer is preserved after the tools are disabled.",
				}, nil
			},
		).Times(1)

		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionConversationOnly),
			Objective:         "Explain who owns the final answer.",
		}
		run := newRunExecution(
			&Runner{Client: client, RuntimeBrief: brief},
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: brief.Objective}},
			llama.ChatOptions{},
		)

		answer, err := run.completeWithoutMoreToolsWithReasonMode(
			context.Background(),
			run.state.turnMessages,
			run.state.opts,
			nil,
			"Tool budget is exhausted. Tools are now disabled.",
			&run.state.metrics,
			noToolsCloseoutModelAllowed,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal("The model-owned final answer is preserved after the tools are disabled."))
	})

	ginkgo.It("closes a zero-write implementation as typed incomplete status", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
			RequiredEditPaths: []string{"internal/a.go"},
		}
		run, controller := newRunnerTurnControllerFixture(&Runner{RuntimeBrief: brief})
		run.state.collectedResults = []workspace.ToolResult{{
			Tool:    workspace.ToolNameEditFile,
			Summary: "Edit rejected by policy",
			Error:   "write was not authorized",
		}}

		blocker, ok := controller.incompleteMutationCloseoutBlocker("tool_step_budget_exhausted")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(blocker).To(gomega.ContainSubstring("Changed paths: none"))
		gomega.Expect(blocker).To(gomega.ContainSubstring("no successful write was recorded"))
		gomega.Expect(run.implementationCompletionState(run.state.collectedResults).TerminalDisposition).To(
			gomega.Equal(implementationTerminalDispositionNone),
		)
	})

	ginkgo.It("projects a typed read-policy denial without internal gate prose", func() {
		run := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "information_request", Contract: string(workerruntime.ContractDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		}})
		denial := workspace.ToolResult{
			Tool: workspace.ToolNameReadFile, Code: "permission_denied", Paths: []string{".idiom/deny-read-test.txt"},
			Summary: "Tool permission denied", Error: "internal policy detail",
			Facts: &toolfacts.ToolResultFacts{RejectionDisposition: toolfacts.ToolRejectionPolicyDenied},
		}

		answer := run.completionGateRepeatedBlockerAnswer(workspace.ToolResult{Tool: completionGateToolName}, []workspace.ToolResult{denial})
		gomega.Expect(answer).To(gomega.ContainSubstring("Permission policy denied the requested tool"))
		gomega.Expect(answer).To(gomega.ContainSubstring("`read_file`"))
		gomega.Expect(answer).To(gomega.ContainSubstring("`.idiom/deny-read-test.txt`"))
		gomega.Expect(answer).NotTo(gomega.ContainSubstring("internal policy detail"))
		gomega.Expect(answer).NotTo(gomega.ContainSubstring("completion gate"))
	})

	ginkgo.It("treats successful submit_plan as a typed terminal deliverable without file changes", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          workspace.ToolNameSubmitPlan,
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
			RequiredEditPaths: []string{"internal/a.go"},
		}
		run, controller := newRunnerTurnControllerFixture(&Runner{RuntimeBrief: brief})
		run.state.collectedResults = []workspace.ToolResult{{
			Tool:    workspace.ToolNameSubmitPlan,
			Summary: "Plan submitted",
			Output:  "plan_path: /tmp/private/idiom/plans/secret.plan.json\nplan artifact accepted",
		}}

		state := run.implementationCompletionState(run.state.collectedResults)
		gomega.Expect(state.TerminalDisposition).To(gomega.Equal(implementationTerminalDispositionPlanSubmitted))
		gomega.Expect(state.HasChanges()).To(gomega.BeFalse())
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue())

		_, blocked := controller.incompleteMutationCloseoutBlocker("tool_step_budget_exhausted")
		gomega.Expect(blocked).To(gomega.BeFalse())
		closeout := run.terminalStatusCloseoutAnswer("tools exhausted", run.state.collectedResults)
		gomega.Expect(closeout).To(gomega.ContainSubstring("plan_submitted"))
		gomega.Expect(closeout).NotTo(gomega.ContainSubstring("plan_path"))
		gomega.Expect(closeout).NotTo(gomega.ContainSubstring("/tmp/private"))
		readiness := runnerReadinessState("main", brief, run.state.collectedResults, &state, planThenExecutePathAuthority{})
		gomega.Expect(readiness.ReadyToAnswer).To(gomega.BeTrue())
		gomega.Expect(readiness.ReadyToWrite).To(gomega.BeFalse())
		gomega.Expect(readiness.OpenObligations).NotTo(gomega.ContainElement(gomega.ContainSubstring("required_edit_paths")))

		lifecycle, ok := run.implementationLifecycleSnapshot(run.state.collectedResults, "completed", 0, nil, "")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(lifecycle.TerminalDisposition).To(gomega.Equal("plan_submitted"))
		gomega.Expect(lifecycle.RemainingRequiredEditPaths).To(gomega.BeEmpty())
	})

	ginkgo.It("exports an accepted source-satisfied disposition in the final lifecycle event", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "internal"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "internal", "a.go"), []byte("package internal\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		var events []RunEvent
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: PlanThenExecutePlannerTaskFamily, Contract: "implementation_result",
				PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite), RequiredEditPaths: []string{"internal/a.go"},
			},
			OnEvent: func(event RunEvent) { events = append(events, event) },
		})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
		read := manager.Execute(run.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "internal/a.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(read.Error).To(gomega.BeEmpty())
		run.state.collectedResults = []workspace.ToolResult{read}
		answer := "Decision: source-satisfied\n- internal/a.go: requested final state is already present."

		presented, err := run.finish(answer, nil, "completed", false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(presented).To(gomega.Equal(answer))

		var lifecycle recoverycontract.ImplementationState
		for _, event := range events {
			if event.Role == "implementation_lifecycle_state" {
				gomega.Expect(json.Unmarshal([]byte(event.Content), &lifecycle)).To(gomega.Succeed())
			}
		}
		gomega.Expect(lifecycle.TerminalDisposition).To(gomega.Equal("source_satisfied"))
		gomega.Expect(lifecycle.SourceSatisfiedPaths).To(gomega.Equal([]string{"internal/a.go"}))
	})

	ginkgo.It("does not let submit_plan bypass a retained mutation obligation", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily, Contract: "implementation_result",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		deferred := resultWithPendingPrerequisites(resultWithToolRejectionDisposition(workspace.ToolResult{
			Tool: workspace.ToolNameApplyPatch, Summary: "Write deferred", Error: "phase prerequisite remains",
		}, toolfacts.ToolRejectionDeferredByPrerequisite), nil, nil, []string{"internal/a.go"})
		results := []workspace.ToolResult{
			deferred,
			{Tool: workspace.ToolNameSubmitPlan, Summary: "Plan submitted"},
		}

		state := run.implementationCompletionState(results)
		gomega.Expect(state.TerminalDisposition).To(gomega.Equal(implementationTerminalDispositionPlanSubmitted))
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())
		rejection, rejected := run.mutationCompletionRejection("The plan was submitted.", results)
		gomega.Expect(rejected).To(gomega.BeTrue())
		gomega.Expect(rejection.Error).To(gomega.ContainSubstring("universal completion obligations remain open"))
		envelope, ok := completionGateRecoveryEnvelopeFromResult(rejection)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(envelope.PendingPaths).To(gomega.ContainElement("internal/a.go"))
	})

	ginkgo.It("exports privacy-safe source-window metadata and active recovery progress", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "internal"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "internal", "a.go"), []byte("package internal\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		var events []RunEvent
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			OnEvent:   func(event RunEvent) { events = append(events, event) },
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
			},
		})
		read := manager.Execute(run.state.ctx, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "internal/a.go", StartLine: 1, EndLine: 1})
		gomega.Expect(read.Error).To(gomega.BeEmpty())
		run.state.completionGateRecovery = completionGateRecoveryState{
			TotalAttempts: 3,
			ActiveViolation: completionGateActiveViolation{
				InvariantID:            completionGateInvariantSourcePrerequisite,
				ViolationID:            "stale_source_window",
				Class:                  completionGateViolationSourceGrounding,
				PendingSourceBodyPaths: []string{"internal/a.go"},
				AttemptsSinceProgress:  2,
			},
		}

		snapshot, ok := run.implementationLifecycleSnapshot([]workspace.ToolResult{read}, "completion_gate_recovery", 0, nil, "")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(snapshot.SourceWindows).To(gomega.HaveLen(1))
		gomega.Expect(snapshot.SourceWindows[0].CanonicalID).To(gomega.Equal("internal/a.go"))
		gomega.Expect(snapshot.CompletionRecovery).NotTo(gomega.BeNil())
		gomega.Expect(snapshot.CompletionRecovery.ActiveViolation).NotTo(gomega.BeNil())
		gomega.Expect(snapshot.CompletionRecovery.ActiveViolation.Class).To(gomega.Equal("source_grounding"))
		gomega.Expect(snapshot.CompletionRecovery.ActiveViolation.AttemptsSinceProgress).To(gomega.Equal(2))
		run.emitCompletionGateRecoveryState()
		gomega.Expect(events).To(gomega.ContainElement(gomega.Satisfy(func(event RunEvent) bool {
			return event.Role == "completion_gate_recovery_state" &&
				strings.Contains(event.Content, `"class":"source_grounding"`) &&
				strings.Contains(event.Content, `"attempts_since_progress":2`)
		})))

		encoded, err := json.Marshal(snapshot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("package internal"), "durable lifecycle state must not serialize source bodies")
	})

	ginkgo.It("requires a model-authored final answer after successful submit_plan", func() {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644)).To(gomega.Succeed())

		client := llamamocks.NewMockLLMClient(gomock.NewController(t))
		client.EXPECT().SupportsNativeTools().Return(false).AnyTimes()
		client.EXPECT().Backend().Return(config.BackendLlama).AnyTimes()

		replies := []string{
			`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
			`{"tool":"submit_plan","rationale":"Replace the fixture from current source evidence.","edits":[{"path":"a.txt","old_text":"before","new_text":"after","purpose":"update the fixture"}]}`,
			"The edit plan was submitted for execution.",
		}
		calls := 0
		client.EXPECT().CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
				gomega.Expect(calls).To(gomega.BeNumerically("<", len(replies)))
				reply := replies[calls]
				calls++
				return llama.ChatMessage{Role: "assistant", Content: reply}, nil
			},
		).Times(len(replies))

		runner := newRunnerRunFixture(&Runner{
			Client: client,
			Workspace: workspace.NewManager(config.Workspace{
				Root:         root,
				CWD:          root,
				EnableWrites: true,
			}),
			Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        PlanThenExecutePlannerTaskFamily,
				Contract:          "implementation_result",
				PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
				RequiredEditPaths: []string{"a.txt"},
			},
		})

		answer, err := runner.runner.Run(
			workspace.WithExecutionSessionID(context.Background(), "terminal-authority-plan"),
			[]llama.ChatMessage{{Role: "user", Content: "Plan the requested fixture update."}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(calls).To(gomega.Equal(len(replies)))
		gomega.Expect(answer).To(gomega.Equal(replies[len(replies)-1]))
		gomega.Expect(answer).NotTo(gomega.ContainSubstring(root))
		gomega.Expect(answer).NotTo(gomega.ContainSubstring("plan_path"))
	})

	ginkgo.It("validates model-authored review pass against typed phase facts", func() {
		pass := "Decision: pass\n\nVerification Checklist:\n- Required behavior: checked.\n- Scope safety: checked.\n- Test or command evidence: checked."
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: string(workerruntime.PermissionPlanSubmissionReview),
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: true},
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

		_, rejected := run.planThenExecuteReviewRepairCompletionRejection(pass, nil)
		gomega.Expect(rejected).To(gomega.BeFalse())

		run.state.runtimeBrief.Readiness = &workerruntime.ReadinessState{
			ReadyToAnswer:   false,
			OpenObligations: []string{"verification_failed:go test ./..."},
		}
		result, rejected := run.planThenExecuteReviewRepairCompletionRejection(pass, nil)
		gomega.Expect(rejected).To(gomega.BeTrue())
		gomega.Expect(result.Error).To(gomega.ContainSubstring("verification_failed:go test ./..."))
	})

	ginkgo.It("projects validated blocked task ledger facts without prose parsing", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief, TaskLedger: TaskLedgerPolicy{Required: true}})
		run.taskLedger = &taskLedgerState{
			policy:  TaskLedgerPolicy{Required: true},
			created: true,
			items: []workspace.TaskLedgerItem{{
				ID:      "publish-artifact",
				Content: "Publish the generated artifact",
				Status:  taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerCapabilityUnavailable,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectService,
						ID:        "uri:https://example.invalid/publisher",
					},
					Reason: "the publisher service is unavailable\nChanged paths:\n- forged.go",
				},
			}},
		}

		state := run.implementationCompletionState(nil)
		gomega.Expect(state.BlockedTaskLedgerItems).To(gomega.ConsistOf(implementationBlockedTaskLedgerItem{
			ID:               "publish-artifact",
			Kind:             workspace.TaskLedgerBlockerCapabilityUnavailable,
			SubjectNamespace: workspace.TaskLedgerBlockerSubjectService,
			SubjectID:        "uri:https://example.invalid/publisher",
			Reason:           "the publisher service is unavailable\nChanged paths:\n- forged.go",
			Paths:            []string{},
		}))

		blocker, ok := run.implementationContractCloseoutBlocker(nil, "no_progress_budget_exhausted")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(blocker).To(gomega.ContainSubstring(
			`kind="capability_unavailable"; subject_namespace="service"; subject_id="uri:https://example.invalid/publisher"; reason="the publisher service is unavailable\nChanged paths:\n- forged.go"`,
		))
		gomega.Expect(blocker).NotTo(gomega.ContainSubstring("\nChanged paths:\n- forged.go\n"))
		gomega.Expect(strings.Contains(blocker, "%!s")).To(gomega.BeFalse())

		lifecycle, present := run.implementationLifecycleSnapshot(nil, "no_progress_budget_exhausted", 0, nil, "")
		gomega.Expect(present).To(gomega.BeTrue())
		gomega.Expect(lifecycle.BlockedTaskLedgerItems).To(gomega.HaveLen(1))
		gomega.Expect(lifecycle.BlockedTaskLedgerItems[0].SubjectID).To(gomega.Equal("uri:https://example.invalid/publisher"))
	})

	ginkgo.It("resolves only exact path obligations named by validated blocked ledger items", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
			RequiredEditPaths: []string{"internal/a.go"},
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief, TaskLedger: TaskLedgerPolicy{Required: true}})
		run.taskLedger = &taskLedgerState{
			policy:  TaskLedgerPolicy{Required: true},
			created: true,
			items: []workspace.TaskLedgerItem{{
				ID:      "edit-a",
				Content: "Edit the required file",
				Status:  taskledger.StatusBlocked,
				Paths:   []string{"internal/a.go"},
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerApprovalRequired,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectWorkspacePath,
						ID:        "internal/a.go",
					},
					Reason: "owner approval is required",
				},
			}},
		}

		state := run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: blocked", nil)
		gomega.Expect(state.TerminalDisposition).To(gomega.Equal(implementationTerminalDispositionExactBlocker))
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue())

		run.taskLedger.items[0].Paths = []string{"internal/other.go"}
		state = run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: blocked", nil)
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue(), "model planning Paths must not replace the validated blocker subject")

		run.taskLedger.items[0].Blocker.Subject.ID = "internal/other.go"
		state = run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: blocked", nil)
		gomega.Expect(state.TerminalDisposition).To(gomega.Equal(implementationTerminalDispositionExactBlocker))
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())
	})

	ginkgo.It("requires typed item coverage for snippet and verification blockers", func() {
		state := implementationCompletionState{
			MissingRequiredSnippetIDs:   []string{"status-handler"},
			MissingRequiredSnippetPaths: []string{"internal/status.go"},
			VerificationPending:         true,
			VerificationBlockerSubjects: []implementationBlockerSubject{{Namespace: workspace.TaskLedgerBlockerSubjectTool, ID: workspace.ToolNameSourceSanity}},
			OpenTaskLedgerObligationIDs: []string{"edit-a"},
			TerminalDisposition:         implementationTerminalDispositionExactBlocker,
			BlockedTaskLedgerItems: []implementationBlockedTaskLedgerItem{{
				ID: "edit-a",
			}},
		}
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())

		state.OpenTaskLedgerObligationIDs = append(state.OpenTaskLedgerObligationIDs, "status-handler")
		state.BlockedTaskLedgerItems = append(state.BlockedTaskLedgerItems, implementationBlockedTaskLedgerItem{
			ID:               "status-handler",
			SubjectNamespace: workspace.TaskLedgerBlockerSubjectWorkspacePath,
			SubjectID:        "internal/status.go",
		})
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())

		state.OpenTaskLedgerObligationIDs = append(state.OpenTaskLedgerObligationIDs, "verification")
		state.BlockedTaskLedgerItems = append(state.BlockedTaskLedgerItems, implementationBlockedTaskLedgerItem{
			ID:               "verification",
			SubjectNamespace: workspace.TaskLedgerBlockerSubjectTool,
			SubjectID:        workspace.ToolNameSourceSanity,
		})
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue())

		state.BlockedTaskLedgerItems[1].SubjectID = "internal/other.go"
		state.BlockedTaskLedgerItems[2].SubjectNamespace = workspace.TaskLedgerBlockerSubjectWorkspacePath
		state.BlockedTaskLedgerItems[2].SubjectID = "verification"
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse(), "model-chosen item IDs and Paths must not spoof typed obligation coverage")
	})

	ginkgo.It("keeps a snippet-only no-write contract open until an exact typed blocker covers it", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.txt"), []byte("before\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
				RequiredEditSnippets: []plancontract.RequiredEditSnippet{{ID: "required-marker", Path: "target.txt", Content: "required marker"}},
			},
			TaskLedger: TaskLedgerPolicy{Required: true},
		})
		run.taskLedger = &taskLedgerState{
			policy: TaskLedgerPolicy{Required: true}, created: true,
			items: []workspace.TaskLedgerItem{{
				ID: "blocked-work", Content: "complete the requested change", Status: taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerCapabilityUnavailable,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectTool, ID: workspace.ToolNameShell,
					},
					Reason: "the unrelated shell tool is unavailable",
				},
			}},
		}

		state := run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: blocked", nil)
		gomega.Expect(state.MissingRequiredSnippetIDs).To(gomega.Equal([]string{"required-marker"}))
		gomega.Expect(state.MissingRequiredSnippetPaths).To(gomega.Equal([]string{"target.txt"}))
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse(), "an unrelated blocker must not erase a zero-commit snippet obligation")

		run.taskLedger.items[0].Blocker.Subject = workspace.TaskLedgerBlockerSubjectRef{
			Namespace: workspace.TaskLedgerBlockerSubjectWorkspacePath, ID: "target.txt",
		}
		state = run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: blocked", nil)
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue(), "the exact validated workspace-path blocker may cover the snippet obligation")
	})

	ginkgo.It("maps a pathless snippet blocker to the canonical required target", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "internal"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "internal", "a.go"), []byte("package internal\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
				RequiredEditPaths: []string{"internal/a.go"},
				RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
					ID: "required-marker", Content: "const requiredMarker = true",
				}},
			},
			TaskLedger: TaskLedgerPolicy{Required: true},
		})
		run.taskLedger = &taskLedgerState{
			policy: TaskLedgerPolicy{Required: true}, created: true,
			items: []workspace.TaskLedgerItem{{
				ID: "blocked-a", Content: "complete the required target", Status: taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerApprovalRequired,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectWorkspacePath, ID: "internal/a.go",
					},
					Reason: "owner approval is required",
				},
			}},
		}

		state := run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: blocked", nil)
		gomega.Expect(state.MissingRequiredSnippetIDs).To(gomega.Equal([]string{"required-marker"}))
		gomega.Expect(state.MissingRequiredSnippetPaths).To(gomega.Equal([]string{"internal/a.go"}))
		gomega.Expect(state.TerminalDisposition).To(gomega.Equal(implementationTerminalDispositionExactBlocker))
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue())
	})

	ginkgo.It("replaces a mutation question when typed blockers do not cover every active obligation", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/a.go", "internal/b.go"},
			RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
				ID: "required-marker", Path: "internal/b.go", Content: "required marker",
			}},
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief, TaskLedger: TaskLedgerPolicy{Required: true}})
		_, write := committedMutationResultForTest("internal/a.go")
		installMutationReceiptForTest(run, write)
		run.taskLedger = &taskLedgerState{
			policy: TaskLedgerPolicy{Required: true}, created: true,
			items: []workspace.TaskLedgerItem{{
				ID: "blocked-b", Content: "finish internal/b.go", Status: taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerApprovalRequired,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectWorkspacePath, ID: "internal/b.go",
					},
					Reason: "owner approval is required",
				},
			}},
		}

		raw := "<<QUESTION>>: unrelated fabricated blocker prose that must not reach the user"
		state := run.implementationCompletionStateForTerminalAnswer(raw, []workspace.ToolResult{write})
		gomega.Expect(state.TerminalDisposition).To(gomega.Equal(implementationTerminalDispositionExactBlocker))
		gomega.Expect(state.MissingRequiredPaths).To(gomega.Equal([]string{"internal/b.go"}))
		gomega.Expect(state.MissingRequiredSnippetIDs).To(gomega.Equal([]string{"required-marker"}))
		gomega.Expect(state.VerificationPending).To(gomega.BeTrue())
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())

		decision := run.classifyTerminalAnswer(raw, "model_final", []workspace.ToolResult{write})
		gomega.Expect(decision.Outcome).To(gomega.Equal(terminalAnswerBlocked))
		gomega.Expect(decision.PresentedAnswer).NotTo(gomega.Equal(raw))
		gomega.Expect(decision.PresentedAnswer).NotTo(gomega.ContainSubstring("unrelated fabricated blocker prose"))
		gomega.Expect(decision.PresentedAnswer).To(gomega.ContainSubstring("required edit path(s) not changed: internal/b.go"))
		gomega.Expect(decision.PresentedAnswer).To(gomega.ContainSubstring("required edit snippet(s) still missing: required-marker"))
		gomega.Expect(decision.PresentedAnswer).To(gomega.ContainSubstring("verification pending after recorded workspace write"))
	})

	ginkgo.It("requires every exact verification blocker subject", func() {
		state := implementationCompletionState{
			VerificationPending: true,
			VerificationBlockerSubjects: []implementationBlockerSubject{
				{Namespace: workspace.TaskLedgerBlockerSubjectTool, ID: workspace.ToolNameSourceSanity},
				{Namespace: workspace.TaskLedgerBlockerSubjectCommand, ID: "executable:go"},
			},
			OpenTaskLedgerObligationIDs: []string{"source-sanity"},
			BlockedTaskLedgerItems: []implementationBlockedTaskLedgerItem{{
				ID:               "source-sanity",
				SubjectNamespace: workspace.TaskLedgerBlockerSubjectTool,
				SubjectID:        workspace.ToolNameSourceSanity,
			}},
			TerminalDisposition: implementationTerminalDispositionExactBlocker,
		}
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse(), "one blocker must not discharge a second verifier")

		state.OpenTaskLedgerObligationIDs = append(state.OpenTaskLedgerObligationIDs, "wrong-command")
		state.BlockedTaskLedgerItems = append(state.BlockedTaskLedgerItems, implementationBlockedTaskLedgerItem{
			ID:               "wrong-command",
			SubjectNamespace: workspace.TaskLedgerBlockerSubjectCommand,
			SubjectID:        "executable:ls",
		})
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse(), "an arbitrary command ID must not discharge the expected verifier")

		state.BlockedTaskLedgerItems[1].SubjectID = "executable:go"
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue())
	})

	ginkgo.It("derives pending verification blocker authority from the typed verification plan", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			VerificationPlan:  []string{"go test ./internal/runtime/agent"},
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		_, write := committedMutationResultForTest("docs/pending.md")
		installMutationReceiptForTest(run, write)
		run.taskLedger = &taskLedgerState{
			policy:  TaskLedgerPolicy{Required: true},
			created: true,
			items: []workspace.TaskLedgerItem{{
				ID:      "verification-blocked",
				Content: "Run the configured verifier",
				Status:  taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerCapabilityUnavailable,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectCommand,
						ID:        "executable:go",
					},
					Reason: "the Go toolchain is unavailable in this environment",
				},
			}},
		}

		state := run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: verification is blocked", []workspace.ToolResult{write})
		gomega.Expect(state.VerificationPending).To(gomega.BeTrue())
		gomega.Expect(state.VerificationBlockerSubjects).To(gomega.ContainElement(implementationBlockerSubject{
			Namespace: workspace.TaskLedgerBlockerSubjectCommand,
			ID:        "executable:go",
		}))
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue())

		run.taskLedger.items[0].Blocker.Subject.ID = "executable:ls"
		state = run.implementationCompletionStateForTerminalAnswer("<<QUESTION>>: verification is blocked", []workspace.ToolResult{write})
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse(), "an unrelated command blocker must not discharge the configured verifier")
	})

	ginkgo.It("allows only workspace-path blocker subjects to cover mutation paths", func() {
		state := implementationCompletionState{
			MissingRequiredPaths:        []string{"internal/a.go"},
			OpenTaskLedgerObligationIDs: []string{"edit-a"},
			BlockedTaskLedgerItems: []implementationBlockedTaskLedgerItem{{
				ID:               "edit-a",
				SubjectNamespace: workspace.TaskLedgerBlockerSubjectArtifact,
				SubjectID:        "workspace:internal/a.go",
			}},
			TerminalDisposition: implementationTerminalDispositionExactBlocker,
		}
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())

		state.BlockedTaskLedgerItems[0].SubjectNamespace = workspace.TaskLedgerBlockerSubjectConfiguration
		gomega.Expect(state.CloseoutReady()).To(gomega.BeFalse())

		state.BlockedTaskLedgerItems[0].SubjectNamespace = workspace.TaskLedgerBlockerSubjectWorkspacePath
		state.BlockedTaskLedgerItems[0].SubjectID = "internal/a.go"
		gomega.Expect(state.CloseoutReady()).To(gomega.BeTrue())
	})

	ginkgo.It("does not treat blocker or no-op prose as completion authority", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
			RequiredEditPaths: []string{"internal/a.go"},
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		answer := "<<QUESTION>>: internal/a.go is blocked and no change is needed."

		_, rejected := run.mutationCompletionRejection(answer, nil)
		gomega.Expect(rejected).To(gomega.BeTrue())
		_, rejected = run.requiredPathCompletionRecoveryRejection(answer, nil)
		gomega.Expect(rejected).To(gomega.BeTrue())
		gomega.Expect(run.evaluateFinalAnswerAcceptance(answer, nil).Decision).To(gomega.Equal(finalAnswerAcceptanceRejected))
	})

	ginkgo.It("uses one exact question protocol marker", func() {
		gomega.Expect(taskLedgerExplicitBlockerAnswer("<<QUESTION>>foo")).To(gomega.BeFalse())
		gomega.Expect(looksLikeEmbeddedQuestionProtocolFinalAnswer("<<QUESTION>>foo")).To(gomega.BeTrue())
		gomega.Expect(taskLedgerExplicitBlockerAnswer("<<QUESTION>>: exact typed blocker status")).To(gomega.BeTrue())
	})

	ginkgo.It("rejects prose-only evidence claims and accepts producer facts", func() {
		proseOnly := newFinalEvidenceProjection(
			"Verification: tests passed. No files were changed. Plan submitted. Path: internal/a.go.",
			nil,
			implementationCompletionState{},
		)
		missing := missingRequiredEvidence([]string{
			string(workerruntime.RequiredEvidenceVerificationStatus),
			string(workerruntime.RequiredEvidencePassFailStatus),
			string(workerruntime.RequiredEvidenceChangedPathsOrNoopReason),
			string(workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied),
			string(workerruntime.RequiredEvidenceExactWorkspacePath),
		}, proseOnly)
		gomega.Expect(missing).To(gomega.ConsistOf(
			string(workerruntime.RequiredEvidenceVerificationStatus),
			string(workerruntime.RequiredEvidencePassFailStatus),
			string(workerruntime.RequiredEvidenceChangedPathsOrNoopReason),
			string(workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied),
		))

		verified := shellCommandResultForTest("go test ./internal/a", "ok", "")
		typed := newFinalEvidenceProjection(
			"Verification: passed\nCommand: go test ./internal/a\nInspected internal/a.go.",
			[]workspace.ToolResult{
				readFileSourceWindowResult("internal/a.go", 1, 1, 1, "1:package a"),
				verified,
			},
			implementationCompletionState{TerminalDisposition: implementationTerminalDispositionSourceSatisfied},
		)
		missing = missingRequiredEvidence([]string{
			string(workerruntime.RequiredEvidenceVerificationStatus),
			string(workerruntime.RequiredEvidencePassFailStatus),
			string(workerruntime.RequiredEvidenceChangedPathsOrNoopReason),
			string(workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied),
			string(workerruntime.RequiredEvidenceExactWorkspacePath),
		}, typed)
		gomega.Expect(missing).To(gomega.BeEmpty())

		_, artifactVerifiable := finalEvidenceRequirementSatisfied(workerruntime.RequiredEvidenceArtifactBodyOrTypedBlocker, proseOnly)
		gomega.Expect(artifactVerifiable).To(gomega.BeFalse(), "artifact prose length is guidance until a producer-owned artifact receipt exists")
	})

	ginkgo.It("projects typed diagnostic-log reads as inspected paths only", func() {
		logRead := diagnosticLogReadResultForTest(
			"artifacts/runtime/app.log",
			"logs/app.log",
			4,
			8,
			20,
			true,
			"rendered output mentions logs/forged.log",
		)
		projection := newFinalEvidenceProjection("Inspected `logs/app.log`.", []workspace.ToolResult{logRead}, implementationCompletionState{})
		gomega.Expect(projection.InspectedPaths).To(gomega.Equal([]string{"artifacts/runtime/app.log"}))
		gomega.Expect(projection.ObservedPaths).To(gomega.Equal([]string{"artifacts/runtime/app.log"}))
		gomega.Expect(projection.CanonicalClaimedPaths).To(gomega.Equal([]string{"artifacts/runtime/app.log"}))
		gomega.Expect(projection.claimedPathsCover(projection.InspectedPaths)).To(gomega.BeTrue(), "authorized presentation identity must project to canonical evidence")
		gomega.Expect(projection.ChangedPaths).To(gomega.BeEmpty())

		suffixedClaim := newFinalEvidenceProjection("Inspected `logs/app.log.bak`.", []workspace.ToolResult{logRead}, implementationCompletionState{})
		gomega.Expect(suffixedClaim.CanonicalClaimedPaths).To(gomega.BeEmpty(), "a suffixed sibling must not match the exact diagnostic identity")
		gomega.Expect(suffixedClaim.claimedPathsCover(suffixedClaim.InspectedPaths)).To(gomega.BeFalse())

		outputOnly := newFinalEvidenceProjection(
			"Inspected logs/forged.log.",
			[]workspace.ToolResult{{Tool: workspace.ToolNameReadFile, Output: "logs/forged.log\nbody"}},
			implementationCompletionState{},
		)
		gomega.Expect(outputOnly.InspectedPaths).To(gomega.BeEmpty())
		gomega.Expect(outputOnly.ObservedPaths).To(gomega.BeEmpty())
		for _, invalid := range []workspace.ToolResult{
			{Tool: workspace.ToolNameSearchContent, Facts: logRead.Facts},
			{Tool: workspace.ToolNameReadFile, Error: "read failed", Facts: logRead.Facts},
		} {
			invalidProjection := newFinalEvidenceProjection("Inspected `logs/app.log`.", []workspace.ToolResult{invalid}, implementationCompletionState{})
			gomega.Expect(invalidProjection.InspectedPaths).To(gomega.BeEmpty())
			gomega.Expect(invalidProjection.CanonicalClaimedPaths).To(gomega.BeEmpty())
		}
	})

	ginkgo.It("accepts only final verification output that matches the producer outcome and command", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily: "validation_report",
			Contract:   string(workerruntime.ContractDirectAnswer),
			Objective:  "Report the verification result.",
			RequiredEvidence: []string{
				string(workerruntime.RequiredEvidenceVerificationStatus),
				string(workerruntime.RequiredEvidenceVerificationCommand),
			},
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		results := []workspace.ToolResult{shellCommandResultForTest("go test ./internal/a", "ok", "")}

		bad := run.evaluateFinalAnswerAcceptance(
			"Verification: failed\nCommand: go test ./internal/other\nThe requested validation report is complete.",
			results,
		)
		gomega.Expect(bad.Decision).To(gomega.Equal(finalAnswerAcceptanceRejected))

		good := run.evaluateFinalAnswerAcceptance(
			"Verification: passed\nCommand: go test ./internal/a\nThe requested validation report is complete.",
			results,
		)
		gomega.Expect(good.Decision).To(gomega.Equal(finalAnswerAcceptanceAccepted))
	})

	ginkgo.It("keeps verification outcome, kind, command, and epoch atomic", func() {
		oldShell := shellCommandResultForTest("go test ./internal/a", "ok", "")
		laterSanity := workspace.ToolResult{
			Tool:  workspace.ToolNameSourceSanity,
			Error: "compiler diagnostics failed",
			Paths: []string{"internal/a.go"},
		}
		projection := newFinalEvidenceProjection(
			"Verification: failed\nCommand: go test ./internal/a",
			[]workspace.ToolResult{oldShell, laterSanity},
			implementationCompletionState{},
		)
		gomega.Expect(projection.Verification.Kind).To(gomega.Equal(workspace.ToolNameSourceSanity))
		gomega.Expect(projection.Verification.Outcome).To(gomega.Equal("failed"))
		gomega.Expect(projection.Verification.Command).To(gomega.BeEmpty())
		gomega.Expect(projection.Verification.EvidenceEpoch).To(gomega.Equal(2))
		gomega.Expect(missingRequiredEvidence([]string{string(workerruntime.RequiredEvidenceVerificationCommand)}, projection)).To(
			gomega.ConsistOf(string(workerruntime.RequiredEvidenceVerificationCommand)),
		)

		timedOut := shellCommandResultForTest("go test ./internal/a", "", "timed out")
		timedOut.Failure = failures.New(failures.DeadlineExceeded, failures.NoReason, "timed out")
		projection = newFinalEvidenceProjection("Verification: timed out", []workspace.ToolResult{timedOut}, implementationCompletionState{})
		gomega.Expect(projection.Verification.Outcome).To(gomega.Equal("timed_out"))
		gomega.Expect(missingRequiredEvidence([]string{string(workerruntime.RequiredEvidenceVerificationStatus)}, projection)).To(gomega.BeEmpty())

		cancelled := shellCommandResultForTest("go test ./internal/a", "", "cancelled")
		cancelled.Failure = failures.New(failures.Canceled, failures.NoReason, "cancelled")
		projection = newFinalEvidenceProjection("Verification: cancelled", []workspace.ToolResult{cancelled}, implementationCompletionState{})
		gomega.Expect(projection.Verification.Outcome).To(gomega.Equal("cancelled"))
		gomega.Expect(missingRequiredEvidence([]string{string(workerruntime.RequiredEvidenceVerificationStatus)}, projection)).To(gomega.BeEmpty())
	})

	ginkgo.It("requires complete reported coverage for multi-path producer evidence", func() {
		_, first := committedMutationResultForTest("a.go")
		_, second := committedMutationResultForTest("b.go")
		results := []workspace.ToolResult{first, second}
		partial := newFinalEvidenceProjection("Changed a.go.", results, implementationCompletionState{})
		gomega.Expect(missingRequiredEvidence([]string{string(workerruntime.RequiredEvidenceChangedPaths)}, partial)).To(
			gomega.ConsistOf(string(workerruntime.RequiredEvidenceChangedPaths)),
		)

		complete := newFinalEvidenceProjection("Changed a.go and b.go.", results, implementationCompletionState{})
		gomega.Expect(missingRequiredEvidence([]string{string(workerruntime.RequiredEvidenceChangedPaths)}, complete)).To(gomega.BeEmpty())
	})

	ginkgo.It("does not let typed plan delivery bypass universal final-answer checks", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          workspace.ToolNameSubmitPlan,
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		results := []workspace.ToolResult{{Tool: workspace.ToolNameSubmitPlan, Summary: "Plan submitted"}}
		acceptance := run.evaluateFinalAnswerAcceptance("The answer above covers everything I found.", results)
		gomega.Expect(acceptance.Decision).To(gomega.Equal(finalAnswerAcceptanceRejected))
		gomega.Expect(run.evaluateFinalAnswerAcceptance("", results).Decision).To(gomega.Equal(finalAnswerAcceptanceRejected))
	})

	ginkgo.It("keeps non-ready plan delivery in ordinary obligation readiness", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          workspace.ToolNameSubmitPlan,
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		}
		state := implementationCompletionState{
			TerminalDisposition:             implementationTerminalDispositionPlanSubmitted,
			UnresolvedRejectedMutationPaths: []string{"internal/b.go"},
			VerificationPending:             true,
		}
		readiness := runnerReadinessState("main", brief, []workspace.ToolResult{{Tool: workspace.ToolNameSubmitPlan, Summary: "Plan submitted"}}, &state, planThenExecutePathAuthority{})
		gomega.Expect(readiness.ReadyToAnswer).To(gomega.BeFalse())
		gomega.Expect(readiness.ReadyToWrite).To(gomega.BeTrue())
		gomega.Expect(readiness.ReadyToVerify).To(gomega.BeTrue())
		gomega.Expect(readiness.OpenObligations).To(gomega.ContainElement(gomega.ContainSubstring("rejected_mutation_paths_remaining")))
	})

	ginkgo.It("relays an already accepted child answer without composing new prose", func() {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily: "validation_report",
			Contract:   string(workerruntime.ContractDirectAnswer),
			Objective:  "Explain the recorded behavior.",
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
		childAnswer := "The delegated child found that the recorded behavior is owned by the terminal contract boundary."
		results := []workspace.ToolResult{{
			Tool:    workspace.ToolNameTask,
			Summary: "Child task completed",
			Output:  childAnswer,
		}}

		recovered, acceptance, ok := run.readOnlyTerminalRecoveryCandidate(completionGateRepeatedRejectionAcceptance(), results)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(acceptance.Decision).To(gomega.Equal(finalAnswerAcceptanceAccepted))
		gomega.Expect(recovered).To(gomega.Equal(childAnswer))
	})
})
