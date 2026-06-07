package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent/writeguard"
)

// isDescribedWriteTool reports whether the given tool must carry a
// non-empty Description. The subset is the diff-producing writes —
// mkdir creates no content and go_move_declarations already conveys
// intent through its structured args, so neither is required to carry
// a prose description.
func isDescribedWriteTool(tool string) bool {
	return writeguard.IsDescribedWriteTool(tool)
}

// checkWriteDescription returns a synthesized guard result when a
// described-write tool was invoked without a Description. The result
// mirrors the discovery-redirect style: short explanation, a concrete
// "Try:" reshaping the call, and the note that a retry with the field
// populated will execute normally.
//
// Returns nil when the request is not a described write or already has
// a description.
func checkWriteDescription(req workspace.ToolRequest) *workspace.ToolResult {
	return writeguard.CheckDescription(req)
}
