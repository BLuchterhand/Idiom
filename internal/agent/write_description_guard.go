package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/workspace"
)

// buildTryExample returns compact JSON that re-issues the original
// ToolRequest with a description field added.
func buildTryExample(req workspace.ToolRequest) string {
	example := map[string]any{
		"tool":        req.Tool,
		"description": "<one sentence on what this change is and why>",
	}

	switch req.Tool {
	case "apply_patch":
		// apply_patch takes a unified diff in "content" rather than a path.
		example["content"] = req.Content
	case "edit_file":
		example["path"] = req.Path
		// Preserve the active edit shape when present.
		if req.OldText != "" || req.NewText != "" {
			example["old_text"] = req.OldText
			example["new_text"] = req.NewText
		} else if req.StartLine != 0 || req.EndLine != 0 {
			example["start_line"] = req.StartLine
			example["end_line"] = req.EndLine
			example["content"] = req.Content
		}
	case "write_file", "append_file":
		example["path"] = req.Path
		if req.Content != "" {
			example["content"] = req.Content
		}
	}

	data, err := json.Marshal(example)
	if err != nil {
		return fmt.Sprintf(`{"tool":%q,"description":"<one sentence on what this change is and why>"}`, req.Tool)
	}
	return string(data)
}

// isDescribedWriteTool reports whether the given tool must carry a
// non-empty Description. The subset is the diff-producing writes —
// mkdir creates no content and go_move_declarations already conveys
// intent through its structured args, so neither is required to carry
// a prose description.
func isDescribedWriteTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "edit_file", "write_file", "append_file", "apply_patch":
		return true
	}
	return false
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
	if !isDescribedWriteTool(req.Tool) {
		return nil
	}
	if strings.TrimSpace(req.Description) != "" {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s requires a \"description\" field — one sentence on what this change is and why.\n", req.Tool)
	b.WriteString("The plan panel labels each write with this description so the user can scan intent at a glance; missing it leaves the plan blank.\n")
	fmt.Fprintf(&b, "Try: reissue the same call with \"description\":\"<one sentence>\". Example: %s\n", buildTryExample(req))
	b.WriteString("Once set, the call executes normally.")
	return &workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Missing write description",
		Error:   strings.TrimSpace(b.String()),
	}
}
