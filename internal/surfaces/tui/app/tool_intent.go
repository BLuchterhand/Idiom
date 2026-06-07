package app

import "strings"

func toolIntent(toolName, target string) string {
	switch strings.TrimSpace(toolName) {
	case "list_files":
		if target == "" {
			target = "the current workspace"
		}
		return "map " + target + " before choosing what to inspect"
	case "search_files":
		if target == "" {
			return "find candidate files for the next inspection step"
		}
		return "find candidate files matching " + target
	case "search_content":
		if target == "" {
			return "locate references before opening files"
		}
		return "locate references to " + target
	case "read_file":
		if target == "" {
			return "inspect source context before deciding the next step"
		}
		return "inspect " + target + " for the relevant context"
	case "go_file_outline", "rust_file_outline", "ts_file_outline", "python_file_outline", "java_file_outline", "cpp_file_outline":
		if target == "" {
			return "scan the file structure before reading details"
		}
		return "scan " + target + " to find the right symbol or section"
	case "go_read_symbol", "rust_read_symbol", "ts_read_symbol", "python_read_symbol", "java_read_symbol", "cpp_read_symbol":
		if target == "" {
			return "read the exact declaration instead of a broad file slice"
		}
		return "read the exact declaration around " + target
	case "edit_file":
		if target == "" {
			return "apply the planned code change"
		}
		return "apply the planned change in " + target
	case "write_file":
		if target == "" {
			return "create or replace a file"
		}
		return "create or replace " + target
	case "append_file":
		if target == "" {
			return "append content to a file"
		}
		return "append content to " + target
	case "apply_patch":
		if target == "" {
			return "apply a patch"
		}
		return "apply a patch to " + target
	case "mkdir":
		if target == "" {
			return "create a directory"
		}
		return "create directory " + target
	case "go_move_declarations":
		if target == "" {
			return "move declarations"
		}
		return "move declarations in " + target
	case "task_ledger":
		return "keep the visible plan synchronized with the work"
	case "shell":
		if target == "" {
			return "inspect or verify state with a command"
		}
		return "inspect or verify state by running " + target
	case "child_task":
		if target == "" {
			return "delegate a bounded subtask while continuing the main task"
		}
		return "delegate a bounded subtask about " + target
	default:
		return ""
	}
}
