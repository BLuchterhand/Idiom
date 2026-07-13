package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Runner turn recorder completion progress", ginkgo.Label("completion_recovery_state"), func() {
	ginkgo.It("observes edit mutation progress before callbacks and next-action projection", func() {
		const target = "internal/runtime/agent/runner_core.go"
		var order []string
		run := newRunnerRunFixture(&Runner{
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
				RequiredEditPaths: []string{target},
			},
			OnEvent: func(event RunEvent) {
				switch event.Role {
				case "completion_gate_recovery_progress", "next_action_state":
					order = append(order, event.Role)
				}
			},
		})
		run.state.recordCompletionGateRejection(completionGateRejectionWithRecovery(
			"Mutation completion gate rejected final answer",
			"required edit path remains",
			newCompletionGateRecoveryEnvelope(
				completionGateInvariantMutationRequiredPaths,
				"required_edit_paths_remaining",
				completionGateViolationMutation,
				[]string{target},
			),
		))
		run.state.setCompletionGateRecoveryMessage("write the required path")

		recorder := runnerTurnRecorder{
			runner: run,
			onToolResult: func(ToolResultEvent) {
				gomega.Expect(run.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
				order = append(order, "tool_result_callback")
			},
		}
		request, result := committedMutationResultForTest(target)
		recorder.recordWithOptions(
			2,
			request,
			result,
			"tool_result",
			runnerRecordOptions{SetLastToolKey: true},
		)

		gomega.Expect(run.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
		gomega.Expect(run.state.completionGateRecovery.TotalAttempts).To(gomega.Equal(1))
		gomega.Expect(order).To(gomega.Equal([]string{
			"completion_gate_recovery_progress",
			"tool_result_callback",
			"next_action_state",
		}))
	})

	ginkgo.It("observes task-ledger contraction after the ledger state is applied", func() {
		policy := TaskLedgerPolicy{Required: true}
		ledger := newTaskLedgerStateFixture(policy)
		initial := ledger.apply(workspace.ToolRequest{
			Tool: workspace.ToolNameTaskLedger,
			Todos: []workspace.TaskLedgerItem{
				{ID: "edit", Content: "Complete the required edit", Status: "pending"},
			},
		})
		gomega.Expect(initial.Error).To(gomega.BeEmpty())

		rejection, rejected := ledger.completionRejection("done", workerruntime.WorkerRuntimeBrief{})
		gomega.Expect(rejected).To(gomega.BeTrue())

		var order []string
		run := newRunnerRunFixture(&Runner{
			TaskLedger: policy,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
			},
			OnEvent: func(event RunEvent) {
				switch event.Role {
				case "completion_gate_recovery_progress", "next_action_state":
					order = append(order, event.Role)
				}
			},
		})
		run.taskLedger = ledger
		run.state.recordCompletionGateRejection(rejection)
		run.state.setCompletionGateRecoveryMessage("finish the task ledger item")

		completedRequest := workspace.ToolRequest{
			Tool: workspace.ToolNameTaskLedger,
			Todos: []workspace.TaskLedgerItem{
				{ID: "edit", Content: "Complete the required edit", Status: "completed"},
			},
		}
		completedResult := ledger.apply(completedRequest, run.state.collectedResults)
		gomega.Expect(completedResult.Error).To(gomega.BeEmpty())

		recorder := runnerTurnRecorder{
			runner: run,
			onToolResult: func(ToolResultEvent) {
				gomega.Expect(run.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
				order = append(order, "tool_result_callback")
			},
		}
		recorder.record(3, completedRequest, completedResult, "task_ledger_result", runnerRecordOptions{})

		gomega.Expect(run.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
		gomega.Expect(run.state.completionGateRecovery.TotalAttempts).To(gomega.Equal(1))
		gomega.Expect(order).To(gomega.Equal([]string{
			"completion_gate_recovery_progress",
			"tool_result_callback",
			"next_action_state",
		}))
	})

	ginkgo.It("observes pathless source answerability only after appending the source result", func() {
		const target = "internal/feature/mode.go"
		var order []string
		run := newRunnerRunFixture(&Runner{
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
				Contract:          string(workerruntime.ContractDirectAnswer),
				Objective:         "What does internal/feature/mode.go define?",
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{workspace.ToolNameReadFile},
				Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
			},
			OnEvent: func(event RunEvent) {
				switch event.Role {
				case "completion_gate_recovery_progress", "next_action_state":
					order = append(order, event.Role)
				}
			},
		})
		assessment := sourceGroundingMissingEvidenceAssessment("objective evidence is still missing")
		run.state.recordCompletionGateRejection(annotateSourceGroundingAssessment(workspace.ToolResult{
			Tool:    completionGateToolName,
			Summary: "Completion gate rejected missing evidence",
			Error:   assessment.Message(),
		}, assessment))
		run.state.setCompletionGateRecoveryMessage(assessment.RecoveryPrompt(""))

		targetResult := readFileSourceWindowResult(
			target,
			1,
			5,
			5,
			"package feature\n\nconst StartupMode = \"safe\"\n",
		)
		recorder := runnerTurnRecorder{
			runner: run,
			onToolResult: func(ToolResultEvent) {
				gomega.Expect(run.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
				order = append(order, "tool_result_callback")
			},
		}
		recorder.record(
			2,
			workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: target},
			targetResult,
			"tool_result",
			runnerRecordOptions{},
		)

		gomega.Expect(run.state.collectedResults).To(gomega.HaveLen(1))
		gomega.Expect(genericReadOnlyEvidenceReady(run.state.runtimeBrief, run.state.collectedResults)).To(gomega.BeTrue())
		gomega.Expect(run.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
		gomega.Expect(run.state.completionGateRecovery.TotalAttempts).To(gomega.Equal(1))
		gomega.Expect(order).To(gomega.Equal([]string{
			"completion_gate_recovery_progress",
			"tool_result_callback",
			"next_action_state",
		}))
	})
})
