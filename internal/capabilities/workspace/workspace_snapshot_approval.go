package workspace

import (
	"context"

	"idiom/internal/contracts/toolpolicy"
)

//go:generate go tool mockgen -source=workspace_snapshot_approval.go -destination=mock_workspace_snapshot_read_approver_test.go -package=workspace -typed

// WorkspaceSnapshotReadApprover is the host-owned approval boundary for
// Ask-classified snapshot reads. Tool requests and snapshot callers cannot
// mint approvals; the invocation owner must bind an approver.
type WorkspaceSnapshotReadApprover interface {
	ApproveWorkspaceSnapshotRead(context.Context, WorkspaceSnapshotReadApprovalRequest) (WorkspaceSnapshotReadApprovalDecision, error)
}

type WorkspaceSnapshotReadApprovalRequest struct {
	SessionID      string
	Purpose        WorkspaceSnapshotPurpose
	CanonicalID    string
	PolicyDecision toolpolicy.Decision
	Protected      bool
}

type WorkspaceSnapshotReadApprovalDecision struct {
	Approved bool
}

type workspaceSnapshotReadApproverContextKey struct{}

type boundWorkspaceSnapshotReadApprover struct {
	approver     WorkspaceSnapshotReadApprover
	session      executionSessionIdentity
	sessionBound bool
}

// WithWorkspaceSnapshotReadApprover binds host-owned operator routing to the
// execution session already present on ctx. A nil approver explicitly denies
// fallback to any other authority.
func WithWorkspaceSnapshotReadApprover(ctx context.Context, approver WorkspaceSnapshotReadApprover) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	session, bound := executionSessionIdentityFromContext(ctx)
	return context.WithValue(ctx, workspaceSnapshotReadApproverContextKey{}, boundWorkspaceSnapshotReadApprover{
		approver: approver, session: session, sessionBound: bound,
	})
}
