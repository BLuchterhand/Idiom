package writeguard

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
)

// IsDescribedWriteTool reports whether the given tool must carry a non-empty
// Description. The subset is the diff-producing writes.
func IsDescribedWriteTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "edit_file", "write_file", "append_file", "apply_patch":
		return true
	}

	return false
}

// CheckDescription returns a synthesized guard result when a described-write
// tool was invoked without a Description.
func CheckDescription(req workspace.ToolRequest) *workspace.ToolResult {
	if !IsDescribedWriteTool(req.Tool) {
		return nil
	}

	if strings.TrimSpace(req.Description) != "" {
		return nil
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%s requires a \"description\" field — one sentence on what this change is and why.\n", req.Tool)
	b.WriteString("The plan panel labels each write with this description so the user can scan intent at a glance; missing it leaves the plan blank.\n")
	b.WriteString("Try: reissue the same call with the same concrete tool arguments plus a non-empty `description` field containing one specific sentence about what this change is and why. Do not copy placeholder descriptions or schema examples.\n")
	b.WriteString("Once set, the call executes normally.")

	return &workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Missing write description",
		Error:   strings.TrimSpace(b.String()),
	}
}
