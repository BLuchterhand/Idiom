package app

import (
	"path/filepath"
	"strings"
	"unicode"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent"
)

const defaultBusyStatus = "Qwen is preparing a response"

func busyStatusForPrompt(prompt string) string {
	if topic := genericBusyTopic(prompt); topic != "" {
		return "Qwen is orienting around your request"
	}
	return "Qwen is considering its options"
}

func busyStatusForToolRequest(event agent.ToolRequestEvent, fallback string) string {
	req := event.Request
	tool := strings.TrimSpace(req.Tool)
	topic := busyTopicFromRequest(req)
	if topic == "" {
		topic = genericBusyTopic(fallback)
	}
	if isRelationshipBusyTool(tool) {
		if left, right, ok := genericBusyTopicPair(relationshipTopicSource(req)); ok {
			return "Qwen is finding intersections between " + left + " and " + right
		}
	}

	switch tool {
	case "context_pack", "discovery_query":
		if topic == "" {
			return "Qwen is mapping the relevant code"
		}
		return "Qwen is mapping code related to " + topic
	case "context_query", "context_zoom", "context_impact", "dependency_graph":
		if topic == "" {
			return "Qwen is finding code relationships"
		}
		return "Qwen is tracing code relationships for " + topic
	case "list_files", "search_files", "search_content", "structural_search", "ctags_symbols":
		if topic == "" {
			return "Qwen is searching the workspace"
		}
		return "Qwen is searching for code related to " + topic
	case "read_file", "file_stats", "go_file_outline", "rust_file_outline", "ts_file_outline", "python_file_outline", "java_file_outline", "cpp_file_outline":
		if topic == "" {
			return "Qwen is reading the relevant code"
		}
		return "Qwen is reading code related to " + topic
	case "code_navigate", "go_read_symbol", "rust_read_symbol", "ts_read_symbol", "python_read_symbol", "java_read_symbol", "cpp_read_symbol":
		if topic == "" {
			return "Qwen is inspecting a source symbol"
		}
		return "Qwen is inspecting " + topic
	case "task", "child_task":
		if topic == "" {
			return "Qwen is delegating a focused check"
		}
		return "Qwen is delegating a focused check on " + topic
	case "task_ledger":
		return "Qwen is updating the work plan"
	case "shell":
		return busyStatusForCommand(req.Command)
	case "submit_plan":
		return "Qwen is preparing an implementation plan"
	}

	if workspace.IsWorkspaceWriteTool(tool) {
		if topic == "" {
			return "Qwen is applying the planned change"
		}
		return "Qwen is applying a change related to " + topic
	}
	if action := genericBusyTopic(event.Action); action != "" {
		return "Qwen is working through " + action
	}
	if topic != "" {
		return "Qwen is gathering context on " + topic
	}
	return "Qwen is preparing the next step"
}

func busyStatusAfterToolResult(event agent.ToolResultEvent) string {
	tool := strings.TrimSpace(event.Request.Tool)
	if workspace.IsWorkspaceWriteTool(tool) {
		if strings.TrimSpace(event.Result.Error) != "" {
			return "Qwen is reviewing the failed change"
		}
		return "Qwen is reviewing the change"
	}
	if tool == "submit_plan" {
		return "Qwen is reviewing the plan"
	}
	if tool == "shell" {
		return "Qwen is reviewing the command output"
	}
	return defaultBusyStatus
}

func busyStatusForCommand(command string) string {
	command = strings.TrimSpace(command)
	switch {
	case command == "":
		return "Qwen is checking workspace state"
	case strings.HasPrefix(command, "go test"), strings.Contains(command, " go test "):
		return "Qwen is running tests"
	case strings.HasPrefix(command, "make test"):
		return "Qwen is running tests"
	case strings.HasPrefix(command, "make build"), strings.HasPrefix(command, "go build"):
		return "Qwen is checking the build"
	case strings.HasPrefix(command, "git status"), strings.HasPrefix(command, "git diff"):
		return "Qwen is checking git state"
	case strings.HasPrefix(command, "rg "), strings.Contains(command, " rg "):
		if topic := genericBusyTopic(command); topic != "" {
			return "Qwen is searching for code related to " + topic
		}
		return "Qwen is searching the workspace"
	default:
		return "Qwen is checking the requested command"
	}
}

func busyTopicFromRequest(req workspace.ToolRequest) string {
	switch {
	case strings.TrimSpace(req.Symbol) != "":
		return genericBusyTopic(req.Symbol)
	case strings.TrimSpace(req.Query) != "":
		return genericBusyTopic(req.Query)
	case strings.TrimSpace(req.Task) != "":
		return genericBusyTopic(req.Task)
	case strings.TrimSpace(req.Purpose) != "":
		return genericBusyTopic(req.Purpose)
	case strings.TrimSpace(req.Pattern) != "":
		return genericBusyTopic(req.Pattern)
	case strings.TrimSpace(req.Description) != "":
		return genericBusyTopic(req.Description)
	case strings.TrimSpace(req.NamedGap) != "":
		return genericBusyTopic(req.NamedGap)
	case strings.TrimSpace(req.Path) != "":
		return genericBusyTopic(pathTopic(req.Path))
	case strings.TrimSpace(req.Glob) != "":
		return genericBusyTopic(pathTopic(req.Glob))
	case strings.TrimSpace(req.Handle) != "":
		return genericBusyTopic(req.Handle)
	case strings.TrimSpace(req.Prompt) != "":
		return genericBusyTopic(req.Prompt)
	case strings.TrimSpace(req.Agent) != "":
		return genericBusyTopic(req.Agent)
	default:
		return ""
	}
}

func isRelationshipBusyTool(tool string) bool {
	switch tool {
	case "context_query", "context_zoom", "context_impact", "dependency_graph", "discovery_query":
		return true
	default:
		return false
	}
}

func relationshipTopicSource(req workspace.ToolRequest) string {
	for _, candidate := range []string{req.Query, req.Task, req.Purpose, req.Pattern, req.Prompt} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func pathTopic(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.Trim(base, ".*")
	if base == "" || base == "." || base == string(filepath.Separator) {
		return path
	}
	return base
}

func genericBusyTopic(raw string) string {
	words := genericTopicWords(raw, 5)
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}

func genericBusyTopicPair(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	lower := strings.ToLower(raw)
	for _, sep := range []string{" and ", " with ", " vs ", " versus "} {
		idx := strings.Index(lower, sep)
		if idx <= 0 || idx+len(sep) >= len(raw) {
			continue
		}
		left := strings.Join(genericTopicWords(raw[:idx], 4), " ")
		right := strings.Join(genericTopicWords(raw[idx+len(sep):], 4), " ")
		if left != "" && right != "" {
			return left, right, true
		}
	}
	return "", "", false
}

func genericTopicWords(raw string, limit int) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || limit <= 0 {
		return nil
	}

	raw = strings.ReplaceAll(raw, "_", " ")
	raw = strings.ReplaceAll(raw, "-", " ")
	raw = strings.ReplaceAll(raw, "/", " ")
	raw = strings.ReplaceAll(raw, ".", " ")

	var b strings.Builder
	var last rune
	for _, r := range raw {
		if unicode.IsUpper(r) && unicode.IsLower(last) {
			b.WriteRune(' ')
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ')
		}
		last = r
	}

	out := make([]string, 0, limit)
	for _, word := range strings.Fields(b.String()) {
		if shouldSkipBusyTopicWord(word) {
			continue
		}
		out = append(out, word)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func shouldSkipBusyTopicWord(word string) bool {
	if len(word) <= 2 {
		return true
	}
	switch word {
	case "the", "and", "for", "with", "from", "into", "onto", "about", "around", "related",
		"this", "that", "these", "those", "your", "you", "can", "could", "would", "should",
		"please", "pls", "fix", "make", "change", "update", "add", "remove", "ensure",
		"proper", "properly", "request", "requests", "code", "file", "files", "path",
		"check", "checking", "read", "reading",
		"search", "searching", "find", "finding", "inspect", "inspecting", "work",
		"working", "response", "answer", "implement", "implementation", "caller", "callers",
		"callee", "callees", "reference", "references", "definition", "definitions":
		return true
	default:
		return false
	}
}
