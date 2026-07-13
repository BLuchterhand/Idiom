package workspace

import "context"

// withWorkspaceMutationCheckpoint binds deterministic package-level test
// coordination to one tool invocation. It is intentionally private and never
// populated from ToolRequest or any model-controlled input.
func withWorkspaceMutationCheckpoint(ctx context.Context, checkpoint workspaceMutationCheckpointFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, workspaceMutationCheckpointContextKey{}, checkpoint)
}

// withWorkspaceReadCheckpoint binds deterministic test coordination to one
// invocation. It is private and cannot be populated from a tool request.
func withWorkspaceReadCheckpoint(ctx context.Context, checkpoint workspaceReadCheckpointFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, workspaceReadCheckpointContextKey{}, checkpoint)
}

func withMutationRollbackCheckpoint(ctx context.Context, checkpoint mutationRollbackCheckpointFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mutationRollbackCheckpointContextKey{}, checkpoint)
}
