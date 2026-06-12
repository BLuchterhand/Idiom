package agent

import (
	"context"
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
)

func (r *Runner) executeWorkspaceWithPreimage(ctx context.Context, store *PreimageCheckpointStore, req workspace.ToolRequest) workspace.ToolResult {
	if result, blocked := snapshotWritePreimages(store, req); blocked {
		return result
	}

	return r.Workspace.Execute(ctx, req)
}

func snapshotWritePreimages(store *PreimageCheckpointStore, req workspace.ToolRequest) (workspace.ToolResult, bool) {
	if store == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return workspace.ToolResult{}, false
	}

	paths, classified, err := workspace.WritePreimagePaths(req)
	if err != nil {
		return workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Preimage checkpoint failed",
			Error:   fmt.Sprintf("could not classify %s preimage paths: %v", strings.TrimSpace(req.Tool), err),
		}, true
	}

	if !classified {
		return workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Preimage checkpoint failed",
			Error:   fmt.Sprintf("workspace write tool %s has no preimage path classification", strings.TrimSpace(req.Tool)),
		}, true
	}

	for _, path := range paths {
		if err := store.Snapshot(path); err != nil {
			return workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Preimage checkpoint failed",
				Error:   fmt.Sprintf("could not snapshot %s before %s: %v", path, strings.TrimSpace(req.Tool), err),
			}, true
		}
	}

	return workspace.ToolResult{}, false
}
