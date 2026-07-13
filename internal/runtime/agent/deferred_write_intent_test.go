package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/taskledger"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.It("retains every typed multi-path prerequisite deferral until a fresh successful write resolves it", func() {
	const (
		busyPath      = "internal/surfaces/tui/app/busy_status.go"
		renderingPath = "internal/surfaces/tui/app/rendering.go"
		runnerPath    = "internal/runtime/agent/runner_core.go"
		busyTestPath  = "internal/surfaces/tui/app/busy_status_test.go"
	)
	wantAll := []string{runnerPath, busyPath, busyTestPath, renderingPath}

	firstRequest := workspace.ToolRequest{
		Tool: workspace.ToolNameApplyPatch,
		Content: strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: " + busyPath,
			"@@",
			"-const busyLabel = \"Busy\"",
			"+const busyLabel = \"Working\"",
			"*** Update File: " + renderingPath,
			"@@",
			"-const renderLabel = \"Render\"",
			"+const renderLabel = \"Rendering\"",
			"*** Update File: " + runnerPath,
			"@@",
			"-const runnerLabel = \"Runner\"",
			"+const runnerLabel = \"Agent runner\"",
			"*** Update File: " + busyTestPath,
			"@@",
			"-const busyTestLabel = \"Busy test\"",
			"+const busyTestLabel = \"Working test\"",
			"*** End Patch",
		}, "\n"),
	}
	firstDeferred := resultWithRejectedRequestTarget(firstRequest, resultWithPendingPrerequisites(resultWithToolRejectionDisposition(workspace.ToolResult{
		Tool:    workspace.ToolNameApplyPatch,
		Summary: "Write rejected before git history evidence",
		Error:   "git history evidence remains pending",
	}, toolfacts.ToolRejectionDeferredByPrerequisite), nil, nil, wantAll))

	if firstDeferred.Facts == nil || !slices.Equal(firstDeferred.Facts.RejectedWritePaths, wantAll) {
		ginkgo.Fail("multi-path deferral did not retain every canonical write path", 1)
	}

	secondRequest := workspace.ToolRequest{
		Tool: workspace.ToolNameApplyPatch,
		Content: strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: " + busyPath,
			"@@",
			"-const busyDetail = \"pending\"",
			"+const busyDetail = \"active\"",
			"*** End Patch",
		}, "\n"),
	}
	secondDeferred := resultWithRejectedRequestTarget(secondRequest, resultWithPendingPrerequisites(resultWithToolRejectionDisposition(workspace.ToolResult{
		Tool:    workspace.ToolNameApplyPatch,
		Summary: "Write rejected before git history evidence",
		Error:   "git history evidence remains pending",
	}, toolfacts.ToolRejectionDeferredByPrerequisite), nil, nil, []string{busyPath}))

	results := []workspace.ToolResult{firstDeferred, secondDeferred}
	if pending := unresolvedRejectedMutationPaths(results, nil); !slices.Equal(pending, wantAll) {
		ginkgo.Fail("later same-path intent overwrote the original multi-path obligation", 1)
	}

	results = append(results,
		workspace.ToolResult{Tool: workspace.ToolNameApplyPatch, Summary: "Patch request produced no changes"},
		workspace.ToolResult{Tool: workspace.ToolNameApplyPatch, Summary: "Patch hunk did not apply", Error: "context mismatch"},
	)
	if pending := unresolvedRejectedMutationPaths(results, nil); !slices.Equal(pending, wantAll) {
		ginkgo.Fail("failed or no-op fresh requests must not resolve deferred mutation paths", 1)
	}

	for _, path := range []string{busyPath, renderingPath, runnerPath, busyTestPath} {
		_, committed := committedMutationResultForTest(path)
		results = append(results, committed)
	}
	if pending := unresolvedRejectedMutationPaths(results, nil); len(pending) != 0 {
		ginkgo.Fail("fresh successful changed-path evidence did not resolve prior deferred intent", 1)
	}
})

var _ = ginkgo.It("resolves only producer-committed paths from a partial write result that also reports an error", func() {
	request := workspace.ToolRequest{
		Tool: workspace.ToolNameApplyPatch,
		Content: strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: internal/feature/dir/../a.go",
			"@@",
			"-const a = false",
			"+const a = true",
			"*** Update File: internal/feature/b.go",
			"@@",
			"-const b = false",
			"+const b = true",
			"*** End Patch",
		}, "\n"),
	}
	rejected := resultWithRejectedRequestTarget(request, resultWithToolRejectionDisposition(workspace.ToolResult{
		Tool:    workspace.ToolNameApplyPatch,
		Summary: "Write deferred before prerequisites",
		Error:   "source evidence pending",
		Facts: &toolfacts.ToolResultFacts{
			RejectedWritePaths: []string{"internal/feature/a.go", "internal/feature/b.go"},
		},
	}, toolfacts.ToolRejectionDeferredByPrerequisite))

	_, partial := committedMutationResultForTest("internal/feature/a.go")
	partial.Error = "b.go context mismatch after a.go committed"
	partial.Facts = &toolfacts.ToolResultFacts{RejectionDisposition: toolfacts.ToolRejectionNotRetained}
	gomega.Expect(partial.CommitReceipt()).NotTo(gomega.BeNil())

	pending := unresolvedRejectedMutationPaths([]workspace.ToolResult{rejected, partial}, nil)
	if !slices.Equal(pending, []string{"internal/feature/b.go"}) {
		ginkgo.Fail("partial commit must clear only its canonical committed path", 1)
	}
})

var _ = ginkgo.It("clears an identical deferred submit plan only after exact path history evidence", func() {
	req := workspace.ToolRequest{
		Tool: workspace.ToolNameSubmitPlan,
		Edits: []planartifact.PlanEdit{{
			Path:    "internal/feature/dir/../target.go",
			OldText: "return before",
			NewText: "return after",
			Purpose: "update target behavior",
		}},
	}
	deferred := workspace.ToolResult{
		Tool:    workspace.ToolNameSubmitPlan,
		Code:    planThenExecuteSubmitPlanHistoryDeferredCode,
		Summary: "submit_plan rejected: edit paths lack git history evidence",
		Error:   "exact history evidence remains pending",
		Facts: &toolfacts.ToolResultFacts{
			DeferredPlanPaths:    []string{"internal/feature/target.go"},
			PendingHistoryPaths:  []string{"internal/feature/target.go"},
			RejectionDisposition: toolfacts.ToolRejectionDeferredByPrerequisite,
		},
	}

	results := []workspace.ToolResult{deferred, gitHistoryRangeResult("internal/feature/unrelated.go")}
	if deferredPlanPrerequisitesSatisfied(req, results) {
		ginkgo.Fail("unrelated git history must not release an identical deferred submit_plan", 1)
	}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		RequiredEvidence: []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	if pending := modelProtocolPendingGitHistoryPaths(brief, results, planThenExecutePathAuthority{}); !slices.Equal(pending, []string{"internal/feature/target.go"}) {
		ginkgo.Fail("unscoped model protocol omitted the typed deferred-plan history path", 1)
	}

	results = append(results, gitHistoryRangeResult("internal/feature/target.go"))
	if !deferredPlanPrerequisitesSatisfied(req, results) {
		ginkgo.Fail("exact path-scoped history should release an identical deferred submit_plan", 1)
	}
	if completed := modelProtocolCompletedGitHistoryPaths(brief, results); !slices.Equal(completed, []string{"internal/feature/target.go"}) {
		ginkgo.Fail("unscoped model protocol did not contract the typed deferred-plan history path", 1)
	}
	canonical := req
	canonical.Edits = append([]planartifact.PlanEdit(nil), req.Edits...)
	canonical.Edits[0].Path = "internal/feature/target.go"
	var turn runnerTurnState
	if turn.bindRequest(req) != turn.bindRequest(canonical) {
		ginkgo.Fail("equivalent submit_plan paths produced different request fingerprints", 1)
	}
})

var _ = ginkgo.It("canonicalizes prerequisite identities before facts, comparison, and fingerprint release", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "internal/feature/target.go", "package feature\n\nconst target = \"before\"\n"); err != nil {
		ginkgo.Fail("write target fixture: "+err.Error(), 1)
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{workspace.ToolNameReadFile, workspace.ToolNameEditFile},
		},
	})
	aliasRequest := workspace.ToolRequest{
		Tool:        workspace.ToolNameEditFile,
		Path:        "internal/feature/dir/../target.go",
		OldText:     `target = "before"`,
		NewText:     `target = "after"`,
		Description: "update the target",
	}
	deferred, ok := runner.implementationWritePrerequisiteDeferralResult(aliasRequest, nil)
	if !ok || deferred.Facts == nil || !slices.Equal(deferred.Facts.PendingSourceBodyPaths, []string{"internal/feature/target.go"}) {
		ginkgo.Fail("deferral facts did not use the canonical workspace-relative path", 1)
	}
	recorded := resultWithRejectedRequestTarget(aliasRequest, deferred)
	if recorded.Facts == nil || !slices.Equal(recorded.Facts.RejectedWritePaths, []string{"internal/feature/target.go"}) {
		ginkgo.Fail("rejected intent ledger did not use the canonical workspace-relative path", 1)
	}
	canonicalWrite := aliasRequest
	canonicalWrite.Path = "internal/feature/target.go"
	read := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile,
		Path: canonicalWrite.Path,
	})
	gomega.Expect(read.Error).To(gomega.BeEmpty())
	if !runner.deferredRequestPrerequisitesSatisfied(aliasRequest, []workspace.ToolResult{recorded, read}) {
		ginkgo.Fail("canonical source evidence did not satisfy the equivalent deferred request", 1)
	}
	committed := runner.runner.Workspace.Execute(runner.state.ctx, canonicalWrite)
	gomega.Expect(committed.Error).To(gomega.BeEmpty())
	gomega.Expect(committed.CommitReceipt()).NotTo(gomega.BeNil())
	resolvedLedger := []workspace.ToolResult{recorded, committed}
	if pending := unresolvedRejectedMutationPaths(resolvedLedger, nil); len(pending) != 0 {
		ginkgo.Fail("canonical ChangedPaths did not resolve equivalent aliased rejected intent", 1)
	}

	canonicalRequest := aliasRequest
	canonicalRequest.Path = "internal/feature/target.go"
	var turn runnerTurnState
	aliasFingerprint := turn.bindRequest(aliasRequest)
	canonicalFingerprint := turn.bindRequest(canonicalRequest)
	if aliasFingerprint != canonicalFingerprint {
		ginkgo.Fail("equivalent workspace paths produced different request fingerprints", 1)
	}

	budget := newRunnerRequestBudgetState(0)
	budget.recordRejectedFingerprint(aliasFingerprint, aliasRequest, deferred)
	if !budget.clearRejectedFingerprint(canonicalFingerprint, toolfacts.ToolRejectionDeferredByPrerequisite) {
		ginkgo.Fail("canonical equivalent request could not release its deferred fingerprint", 1)
	}

	escapeRequest := canonicalRequest
	escapeRequest.Path = "../outside.go"
	if result, deferred := runner.implementationWritePrerequisiteDeferralResult(escapeRequest, nil); deferred {
		ginkgo.Fail("escaping path must not become a prerequisite obligation: "+result.Summary, 1)
	}
	if paths, valid := canonicalRunnerWorkspaceRelativePaths([]string{"../outside.go"}); valid || len(paths) != 0 {
		ginkgo.Fail("escaping path passed canonical prerequisite validation", 1)
	}
})

var _ = ginkgo.It("accepts an exact history blocker without classifying deferred submit_plan as a correction", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
			RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
	})
	runner.taskLedger = newTaskLedgerStateFixture(TaskLedgerPolicy{Required: true})
	runner.taskLedger.created = true
	runner.taskLedger.items = []workspace.TaskLedgerItem{{
		ID:      "history-blocker-a",
		Content: "establish safe history rationale for a.go",
		Status:  taskledger.StatusBlocked,
		Blocker: &workspace.TaskLedgerBlocker{
			Kind: workspace.TaskLedgerBlockerApprovalRequired,
			Subject: workspace.TaskLedgerBlockerSubjectRef{
				Namespace: workspace.TaskLedgerBlockerSubjectWorkspacePath,
				ID:        "a.go",
			},
			Reason: "git history shows the requested change would reverse compatibility behavior for existing callers",
		},
	}}
	deferred := workspace.ToolResult{
		Tool:    workspace.ToolNameSubmitPlan,
		Code:    planThenExecuteSubmitPlanHistoryDeferredCode,
		Summary: "submit_plan rejected: edit paths lack git history evidence",
		Error:   "history shows the proposed edit may be unsafe",
		Facts: &toolfacts.ToolResultFacts{
			DeferredPlanPaths:    []string{"a.go"},
			PendingHistoryPaths:  []string{"a.go"},
			RejectionDisposition: toolfacts.ToolRejectionDeferredByPrerequisite,
		},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		deferred,
	}

	state := derivePlanExecutePlanningState(runner.state.runtimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		ginkgo.Fail("typed history deferral was misclassified as submit correction: "+string(state.Mode), 1)
	}
	answer := "<<QUESTION>>: the typed task ledger records the exact history blocker."
	if result, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		ginkgo.Fail("exact path-specific history blocker was rejected: "+result.Error, 1)
	}
})

var _ = ginkgo.It("keeps a typed plan prerequisite ahead of an older empty-plan recovery", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		{
			Tool:    workspace.ToolNameSubmitPlan,
			Code:    workspace.SubmitPlanEmptyEditsCode,
			Summary: "submit_plan rejected",
			Error:   "submit_plan requires at least one edit in the edits array",
		},
		{
			Tool:    workspace.ToolNameSubmitPlan,
			Code:    planThenExecuteSubmitPlanHistoryDeferredCode,
			Summary: "submit_plan rejected: edit paths lack git history evidence",
			Error:   "history evidence is required for a.go",
			Facts: &toolfacts.ToolResultFacts{
				DeferredPlanPaths:    []string{"a.go"},
				PendingHistoryPaths:  []string{"a.go"},
				RejectionDisposition: toolfacts.ToolRejectionDeferredByPrerequisite,
			},
		},
	}

	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		ginkgo.Fail("older empty-plan recovery remained active after a newer typed prerequisite deferral")
	}
	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		ginkgo.Fail("typed deferred plan did not own the current history-pending phase: " + string(state.Mode))
	}
	if !slices.Equal(state.SelectedEditPaths, []string{"a.go"}) {
		ginkgo.Fail(fmt.Sprintf("history-pending paths = %#v, want [a.go]", state.SelectedEditPaths))
	}
})
