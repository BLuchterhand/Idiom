package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/taskledger"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Task ledger terminal disposition", func() {
	blockedLedger := func() *taskLedgerState {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
		ledger.created = true
		ledger.items = []workspace.TaskLedgerItem{
			{
				ID:      "api-contract",
				Content: "confirm the upstream API contract",
				Status:  taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerCapabilityUnavailable,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectService,
						ID:        "uri:https://api.example.test/contract",
					},
					Reason: "the workspace has no authenticated source for the upstream contract",
				},
			},
			{
				ID:      "regression-tests",
				Content: "add the denied-path regression tests",
				Status:  taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerApprovalRequired,
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectWorkspacePath,
						ID:        "testdata/denied-path-fixture",
					},
					Reason: "the fixture requires owner approval before it can be added",
				},
			},
		}
		return ledger
	}
	blockedTerminalAnswer := "<<QUESTION>>: Work remains blocked; see the typed task ledger for the authoritative blocker details."

	ginkgo.It("renders structured blocker subjects without formatting loss", func() {
		gomega.Expect(blockedLedger().blockedItems()).To(gomega.ContainElement(gomega.ContainSubstring(
			`subject_namespace="service"; subject_id="uri:https://api.example.test/contract"`,
		)))
	})

	ginkgo.It("requires the active workspace resolver when applying updates", func() {
		ledger := newTaskLedgerState(TaskLedgerPolicy{Required: true}, nil)
		result := ledger.apply(workspace.ToolRequest{
			Tool: workspace.ToolNameTaskLedger,
			Todos: []workspace.TaskLedgerItem{{
				ID:      "inspect",
				Content: "inspect the target",
				Status:  taskledger.StatusPending,
			}},
		})

		gomega.Expect(result.Error).To(gomega.ContainSubstring("active workspace authority"))
		gomega.Expect(ledger.created).To(gomega.BeFalse())
	})

	ginkgo.It("rejects explicit and generated duplicate IDs without mutating the ledger", func() {
		cases := []struct {
			name  string
			items []workspace.TaskLedgerItem
		}{
			{
				name: "explicit duplicates after normalization",
				items: []workspace.TaskLedgerItem{
					{ID: " repeated ", Content: "first", Status: taskledger.StatusPending},
					{ID: "repeated", Content: "second", Status: taskledger.StatusPending},
				},
			},
			{
				name: "generated ID collides with explicit ID",
				items: []workspace.TaskLedgerItem{
					{Content: "generated", Status: taskledger.StatusPending},
					{ID: "item-1", Content: "explicit", Status: taskledger.StatusPending},
				},
			},
		}

		for _, tc := range cases {
			ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
			ledger.created = true
			ledger.items = []workspace.TaskLedgerItem{{
				ID:      "existing",
				Content: "preserve the accepted state",
				Status:  taskledger.StatusInProgress,
			}}
			before := append([]workspace.TaskLedgerItem(nil), ledger.items...)

			result := ledger.apply(workspace.ToolRequest{Tool: workspace.ToolNameTaskLedger, Todos: tc.items})

			gomega.Expect(result.Error).To(gomega.ContainSubstring("duplicated"), tc.name)
			gomega.Expect(ledger.created).To(gomega.BeTrue(), tc.name)
			gomega.Expect(ledger.items).To(gomega.Equal(before), tc.name)
		}
	})

	ginkgo.It("derives forced-closeout ledger guidance from typed state", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
		ledger.created = true
		ledger.items = []workspace.TaskLedgerItem{
			{ID: "active", Content: "finish the edit\ndo not reinterpret this line", Status: taskledger.StatusInProgress},
			{ID: "pending", Content: "text mentioning in_progress is not lifecycle state", Status: taskledger.StatusPending},
		}

		hint := taskLedgerClosureHint(ledger)
		gomega.Expect(hint).To(gomega.ContainSubstring(`id="active"`))
		gomega.Expect(hint).To(gomega.ContainSubstring(`content="finish the edit\ndo not reinterpret this line"`))
		gomega.Expect(hint).To(gomega.ContainSubstring(`status="in_progress"`))
		gomega.Expect(hint).NotTo(gomega.ContainSubstring("id=\"pending\""))
		gomega.Expect(hint).NotTo(gomega.ContainSubstring("evidence sentence"))
		gomega.Expect(hint).NotTo(gomega.ContainSubstring("completed / blocked / cancelled"))

		ledger.items[0].Status = taskledger.StatusPending
		gomega.Expect(taskLedgerClosureHint(ledger)).To(gomega.BeEmpty())
	})

	ginkgo.It("rejects success prose that happens to contain blocker keywords", func() {
		ledger := blockedLedger()
		reply := "Verification was not blocked, and there were no permission or approval issues. The work is complete."
		_, rejected := ledger.completionRejection(reply, workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"})
		gomega.Expect(rejected).To(gomega.BeTrue())
	})

	ginkgo.It("requires only the explicit blocker response shape once every remaining item is structurally blocked", func() {
		ledger := blockedLedger()
		brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}

		_, rejected := ledger.completionRejection("Blocked by the typed task ledger.", brief)
		gomega.Expect(rejected).To(gomega.BeTrue())

		_, rejected = ledger.completionRejection(blockedTerminalAnswer, brief)
		gomega.Expect(rejected).To(gomega.BeFalse())
	})

	ginkgo.It("does not parse blocker authority back out of final prose", func() {
		ledger := blockedLedger()
		brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}
		modelIntent := "<<QUESTION>>: typed blockers remain open."
		_, rejected := ledger.completionRejection(modelIntent, brief)
		gomega.Expect(rejected).To(gomega.BeFalse())

		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief, TaskLedger: TaskLedgerPolicy{Required: true}})
		run.taskLedger = ledger
		presented := run.decorateFinalAnswer(FinalAnswerDecorateInput{
			Answer:     modelIntent,
			Brief:      brief,
			UserFacing: true,
		})
		gomega.Expect(presented).NotTo(gomega.Equal(modelIntent))
		gomega.Expect(presented).To(gomega.ContainSubstring(`id="api-contract"; kind="capability_unavailable"; subject_namespace="service"; subject_id="uri:https://api.example.test/contract"`))
		gomega.Expect(presented).To(gomega.ContainSubstring(`id="regression-tests"; kind="approval_required"; subject_namespace="workspace_path"; subject_id="testdata/denied-path-fixture"`))
	})

	ginkgo.It("renders safely quoted typed blocker details in recovery cards", func() {
		ledger := blockedLedger()
		ledger.items[0].Blocker.Reason = "workspace authority said \"denied\"\nretry after approval"
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		}
		run := newRunnerRunFixture(&Runner{RuntimeBrief: brief, TaskLedger: TaskLedgerPolicy{Required: true}})
		run.taskLedger = ledger

		card := run.renderRecoveryCardForRejectedActionWithTransport(
			workspace.ToolRequest{Tool: workspace.ToolNameTaskLedger},
			workspace.ToolResult{Tool: workspace.ToolNameTaskLedger, Summary: "Task ledger remains blocked", Error: "typed blockers remain"},
			nil,
			[]string{workspace.ToolNameTaskLedger},
			PlanExecutePlanningTransport{},
		)

		gomega.Expect(card).To(gomega.ContainSubstring(`typed task blockers: id="api-contract" kind="capability_unavailable" subject="service:uri:https://api.example.test/contract" reason="workspace authority said \"denied\"\nretry after approval"`))
		gomega.Expect(card).To(gomega.ContainSubstring(`id="regression-tests" kind="approval_required" subject="workspace_path:testdata/denied-path-fixture" reason="the fixture requires owner approval before it can be added"`))
		gomega.Expect(card).NotTo(gomega.ContainSubstring("workspace authority said \"denied\"\nretry after approval\n"))
	})

	ginkgo.It("rejects generic evidence in place of a typed blocker", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
		result := ledger.apply(workspace.ToolRequest{
			Tool: workspace.ToolNameTaskLedger,
			Todos: []workspace.TaskLedgerItem{{
				ID:       "api-contract",
				Content:  "confirm the upstream API contract",
				Status:   taskledger.StatusBlocked,
				Evidence: "cannot complete",
			}},
		})

		gomega.Expect(result.Error).To(gomega.ContainSubstring("requires blocker.kind, blocker.subject, and blocker.reason"))
		gomega.Expect(ledger.created).To(gomega.BeFalse())
	})

	ginkgo.It("rejects invalid typed blocker metadata", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
		result := ledger.apply(workspace.ToolRequest{
			Tool: workspace.ToolNameTaskLedger,
			Todos: []workspace.TaskLedgerItem{{
				ID:      "api-contract",
				Content: "confirm the upstream API contract",
				Status:  taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerKind("unspecified"),
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectService,
						ID:        "upstream-api",
					},
					Reason: "the workspace has no authenticated source for the upstream contract",
				},
			}},
		})

		gomega.Expect(result.Error).To(gomega.ContainSubstring("unsupported task ledger blocker kind"))
		gomega.Expect(ledger.created).To(gomega.BeFalse())
	})

	ginkgo.It("accepts and renders normalized typed blockers", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
		result := ledger.apply(workspace.ToolRequest{
			Tool: workspace.ToolNameTaskLedger,
			Todos: []workspace.TaskLedgerItem{{
				ID:      "api-contract",
				Content: "confirm the upstream API contract",
				Status:  taskledger.StatusBlocked,
				Blocker: &workspace.TaskLedgerBlocker{
					Kind: workspace.TaskLedgerBlockerKind(" Environment "),
					Subject: workspace.TaskLedgerBlockerSubjectRef{
						Namespace: workspace.TaskLedgerBlockerSubjectNamespace(" Environment-Variable "),
						ID:        "IDIOM_API_TOKEN",
					},
					Reason: " the environment variable is not configured ",
				},
			}},
		})

		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.Output).To(gomega.And(
			gomega.ContainSubstring(`blocker_kind="environment"`),
			gomega.ContainSubstring(`blocker_subject_namespace="environment_variable"`),
			gomega.ContainSubstring(`blocker_subject_id="IDIOM_API_TOKEN"`),
			gomega.ContainSubstring(`blocker_reason="the environment variable is not configured"`),
		))
		gomega.Expect(ledger.items[0].Blocker).To(gomega.Equal(&workspace.TaskLedgerBlocker{
			Kind: workspace.TaskLedgerBlockerEnvironment,
			Subject: workspace.TaskLedgerBlockerSubjectRef{
				Namespace: workspace.TaskLedgerBlockerSubjectEnvironmentVariable,
				ID:        "IDIOM_API_TOKEN",
			},
			Reason: "the environment variable is not configured",
		}))
	})

	ginkgo.It("keeps open work authoritative over typed blocked items", func() {
		ledger := blockedLedger()
		ledger.items = append(ledger.items, workspace.TaskLedgerItem{ID: "implementation", Content: "apply the edits", Status: taskledger.StatusPending})
		brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}

		_, rejected := ledger.completionRejection(blockedTerminalAnswer, brief)
		gomega.Expect(rejected).To(gomega.BeTrue())
	})

	ginkgo.It("keeps planning exits lane-specific", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
		ledger.created = true
		ledger.items = []workspace.TaskLedgerItem{{ID: "implementation", Content: "apply the edits", Status: taskledger.StatusPending}}
		reply := "Plan ready — awaiting approval."

		_, rejected := ledger.completionRejection(reply, workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"})
		gomega.Expect(rejected).To(gomega.BeTrue())
		_, rejected = ledger.completionRejection("<<QUESTION>>: implementation cannot proceed.", workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"})
		gomega.Expect(rejected).To(gomega.BeTrue(), "open work cannot be discharged as a generic blocker")
		_, rejected = ledger.completionRejection(reply, workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily, Contract: workspace.ToolNameSubmitPlan})
		gomega.Expect(rejected).To(gomega.BeFalse())
	})

	ginkgo.It("does not let a generic question replace a required ledger", func() {
		ledger := newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})

		_, rejected := ledger.completionRejection("<<QUESTION>>: cannot complete", workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"})
		gomega.Expect(rejected).To(gomega.BeTrue())
		gomega.Expect(ledger.created).To(gomega.BeFalse())
	})

	ginkgo.It("uses the same strict ledger contract in final acceptance", func() {
		ledger := blockedLedger()
		run := newRunnerRunFixture(&Runner{
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
			},
			TaskLedger: TaskLedgerPolicy{Required: true},
		})
		run.taskLedger = ledger

		falsePositive := run.evaluateFinalAnswerAcceptance("Verification was not blocked; no permission issues remain.", nil)
		gomega.Expect(falsePositive.BlockerKind).To(gomega.Equal(string(turnViolationRequiredEvidenceUnmet)))

		exact := run.evaluateFinalAnswerAcceptance(blockedTerminalAnswer, nil)
		gomega.Expect(exact.BlockerKind).NotTo(gomega.Equal(string(turnViolationRequiredEvidenceUnmet)))
	})
})
