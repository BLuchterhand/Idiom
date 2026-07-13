package agent

import (
	"context"

	"github.com/onsi/ginkgo/v2"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func mutationAnalysisManagerForTest() *workspace.Manager {
	root := ginkgo.GinkgoT().TempDir()
	return workspace.NewManager(config.Workspace{Root: root, CWD: root})
}

// newRunnerRunFixture creates the complete per-run state needed by focused
// runtime specs. Runner is immutable configuration; mutable evidence, tool
// surfaces, and lifecycle counters belong to runnerRunState.
func newRunnerRunFixture(base *Runner) *runExecution {
	return newRunExecution(base, context.Background(), nil, llama.ChatOptions{})
}

func rejectionRecoveryNudgeForTest(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) string {
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	return run.buildRejectionRecoveryNudge(req, results)
}

// newRunnerTurnControllerFixture wires the controller and recorder to the
// same run-local state, matching the ownership used by the production loop.
func newRunnerTurnControllerFixture(base *Runner) (*runExecution, *runnerTurnController) {
	run := newRunnerRunFixture(base)
	recorder := &runnerTurnRecorder{runner: run}
	controller := &runnerTurnController{runner: run, recorder: recorder}

	return run, controller
}

func newTaskLedgerStateFixture(policy TaskLedgerPolicy) *taskLedgerState {
	return newTaskLedgerState(policy, workspace.NewManager(config.Workspace{}))
}

func permissionAuthorizedInspectionForTest(tools ...string) workspace.PermissionAuthorizedInspectionProjection {
	manager := workspace.NewManager(config.Workspace{})
	return manager.ToolSurfaceSnapshot().PermissionAuthorizedInspection(tools)
}
