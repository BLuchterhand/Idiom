package agent

import (
	"context"

	"idiom/internal/capabilities/workspace"
)

func (state *runnerRunState) bindWorkspaceInvocationAuthority(runner *Runner) {
	if state == nil {
		return
	}
	if state.ctx == nil {
		state.ctx = context.Background()
	}
	if runner == nil || runner.Workspace == nil {
		return
	}

	if _, bound := workspace.ExecutionSessionIDFromContext(state.ctx); !bound {
		state.ctx = workspace.WithExecutionSessionID(state.ctx, runner.Workspace.SessionID())
	}

	var approver workspace.WorkspaceSnapshotReadApprover
	if runner.ReadApprovalGate.active() {
		approver = runnerWorkspaceSnapshotReadApprover{
			gate: runner.ReadApprovalGate,
			lane: state.runtimeBrief.TaskFamily,
		}
	}
	state.ctx = workspace.WithWorkspaceSnapshotReadApprover(state.ctx, approver)

	state.ctx = workspace.WithWorkspaceSnapshotAuditor(state.ctx, runnerWorkspaceSnapshotAuditor{
		onEvent: runner.OnEvent,
	})
}

type runnerWorkspaceSnapshotAuditor struct {
	onEvent func(RunEvent)
}

func (a runnerWorkspaceSnapshotAuditor) RecordWorkspaceSnapshot(_ context.Context, event workspace.WorkspaceSnapshotAuditEvent) {
	if a.onEvent != nil {
		a.onEvent(marshalRunEvent("workspace_snapshot_audit", event))
	}
}
