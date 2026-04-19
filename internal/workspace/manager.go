package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/semantic"
)

type Manager struct {
	cfg config.Workspace

	mode          Mode
	thoroughness  ExploreThoroughness
	writePolicy   WritePolicy
	mu            sync.Mutex
	nextHandle    int
	pathToHandle  map[string]string
	handleToPath  map[string]string
	goIndex       *semantic.GoIndex
	goIndexToken  string
	cppIndex      *semantic.CppIndex
	cppIndexToken string
	goplsPath     string
	goplsChecked  bool
}

type Mode string

const (
	ModeNormal  Mode = "normal"
	ModeExplore Mode = "explore"
)

type ExploreThoroughness string

const (
	ThoroughnessQuick    ExploreThoroughness = "quick"
	ThoroughnessMedium   ExploreThoroughness = "medium"
	ThoroughnessThorough ExploreThoroughness = "thorough"
)

type WritePolicy string

const (
	WritePolicyGuarded WritePolicy = "guarded"
	WritePolicyAuto    WritePolicy = "auto"
)

type ToolRequest struct {
	Tool         string              `json:"tool"`
	ID           string              `json:"id,omitempty"`
	Path         string              `json:"path,omitempty"`
	StartLine    int                 `json:"start_line,omitempty"`
	EndLine      int                 `json:"end_line,omitempty"`
	Pattern      string              `json:"pattern,omitempty"`
	Glob         string              `json:"glob,omitempty"`
	Workdir      string              `json:"workdir,omitempty"`
	Command      string              `json:"command,omitempty"`
	Content      string              `json:"content,omitempty"`
	OldText      string              `json:"old_text,omitempty"`
	NewText      string              `json:"new_text,omitempty"`
	ReplaceAll   bool                `json:"replace_all,omitempty"`
	Recursive    bool                `json:"recursive,omitempty"`
	MaxDepth     int                 `json:"max_depth,omitempty"`
	Destination  string              `json:"destination,omitempty"`
	Declarations []string            `json:"declarations,omitempty"`
	Moves        []GoDeclarationMove `json:"moves,omitempty"`
}

type GoDeclarationMove struct {
	Destination  string   `json:"destination,omitempty"`
	Pattern      string   `json:"pattern,omitempty"`
	Declarations []string `json:"declarations,omitempty"`
}

type ToolResult struct {
	Tool    string
	Summary string
	Output  string
	Error   string
}

type listedEntry struct {
	path  string
	isDir bool
}

var exploreSkippedDirNames = map[string]struct{}{
	".cache":       {},
	".git":         {},
	".hg":          {},
	".svn":         {},
	"bin":          {},
	"build":        {},
	"dist":         {},
	"logs":         {},
	"models":       {},
	"node_modules": {},
	"tmp":          {},
	"transcripts":  {},
}

func NewManager(cfg config.Workspace) *Manager {
	return &Manager{
		cfg:          cfg,
		mode:         ModeNormal,
		thoroughness: ThoroughnessMedium,
		writePolicy:  WritePolicyGuarded,
		nextHandle:   1,
		pathToHandle: make(map[string]string),
		handleToPath: make(map[string]string),
	}
}

func (m *Manager) Root() string {
	return m.cfg.Root
}

func (m *Manager) CWD() string {
	return m.cfg.CWD
}

func (m *Manager) Enabled() bool {
	return m.cfg.Root != ""
}

func (m *Manager) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

func (m *Manager) IsExploreMode() bool {
	return m.Mode() == ModeExplore
}

func (m *Manager) SetMode(mode Mode) error {
	switch mode {
	case ModeNormal, ModeExplore:
	default:
		return fmt.Errorf("unsupported mode %q", mode)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
	return nil
}

func (m *Manager) ExploreThoroughness() ExploreThoroughness {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.thoroughness
}

func (m *Manager) SetExploreThoroughness(level ExploreThoroughness) error {
	switch level {
	case ThoroughnessQuick, ThoroughnessMedium, ThoroughnessThorough:
	default:
		return fmt.Errorf("unsupported explore thoroughness %q", level)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.thoroughness = level
	return nil
}

func (m *Manager) WritePolicy() WritePolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writePolicy
}

func (m *Manager) SetWritePolicy(policy WritePolicy) error {
	switch policy {
	case WritePolicyGuarded, WritePolicyAuto:
	default:
		return fmt.Errorf("unsupported write policy %q", policy)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.writePolicy = policy
	return nil
}

func (m *Manager) Instructions() string {
	if !m.Enabled() {
		return "Workspace tools are disabled because no workspace root is configured."
	}

	mode := m.Mode()
	if mode == ModeExplore {
		return m.exploreInstructions()
	}

	return fmt.Sprintf(
		"Workspace access is enabled.\n\n"+
			"Workspace root: %s\n"+
			"Current working directory: %s\n\n"+
			"When you need a tool, respond with ONLY a fenced block in this exact format:\n\n"+
			"```tool\n"+
			"{\"tool\":\"search_files\",\"pattern\":\"bubble\",\"path\":\".\"}\n"+
			"```\n\n"+
			"Available tools:\n"+
			"- pwd\n"+
			"- list_files {\"path\":\".\",\"recursive\":true,\"max_depth\":2}\n"+
			"- search_files {\"pattern\":\"bubble\",\"path\":\".\"}\n"+
			"- search_content {\"pattern\":\"Bubble Tea\",\"path\":\".\",\"glob\":\"*.go\"}\n"+
			"- read_file {\"id\":\"f12\",\"start_line\":1,\"end_line\":120}\n"+
			"- read_file {\"path\":\"README.md\",\"start_line\":1,\"end_line\":120} (allowed for an exact existing workspace file; handles are preferred)\n"+
			"- mkdir {\"path\":\"relative/dir\"}\n"+
			"- edit_file {\"path\":\"relative/path\",\"old_text\":\"exact text to replace\",\"new_text\":\"replacement text\"}\n"+
			"- edit_file {\"path\":\"relative/path\",\"start_line\":299,\"end_line\":299,\"content\":\"replacement line(s)\"}\n"+
			"- write_file {\"path\":\"relative/path\",\"content\":\"full file contents\"}\n"+
			"- append_file {\"path\":\"relative/path\",\"content\":\"text to append\"}\n"+
			"- shell {\"command\":\"awk 'NR<=80 {print}' README.md\",\"workdir\":\".\"}\n%s%s\n"+
			"Rules:\n"+
			"- Do not invent file paths from memory.\n"+
			"- First use search_files, search_content, or list_files to discover real files.\n"+
			"- After discovery, open files by handle with read_file {\"id\":\"fN\"} whenever possible.\n"+
			"- Raw read_file by path is allowed for an exact existing workspace file; if it fails, use the suggestions or search tools before retrying.\n"+
			"- Use list_files once to orient yourself, then narrow quickly with search_files, search_content, or read_file instead of repeatedly listing directories.\n"+
			"- Paths are relative to the current working directory unless absolute.\n"+
			"- Prefer edit_file for small localized changes to an existing file.\n"+
			"- When the target text contains quotes or tricky indentation, prefer edit_file with start_line/end_line/content after reading the file.\n"+
			"- write_file overwrites the file with the exact provided content.\n"+
			"%s"+
			"- shell runs in the workspace with the same OS permissions as this app process; it is a default cwd, not a sandbox.\n"+
			"- shell can run standard terminal utilities such as rg, awk, sed, git, and python3 when they are installed.\n"+
			"- After you receive tool results, decide whether to use another tool or answer normally.%s",
		m.cfg.Root,
		m.cfg.CWD,
		m.goDiscoveryInstructions(false),
		m.cppDiscoveryInstructions(false),
		m.writePolicyInstructions(),
		m.discoveryInstructions(),
	)
}

func (m *Manager) writePolicyInstructions() string {
	switch m.WritePolicy() {
	case WritePolicyAuto:
		return "- Write policy is auto: file tools may create, rewrite, or append within the workspace when the task requires it. Still prefer edit_file for precise edits and avoid unnecessary rewrites.\n"
	default:
		return "- Write policy is guarded: prefer edit_file for existing files. Avoid write_file for existing files, avoid state-changing shell commands for edits, and only create/append/mkdir when the user clearly requested that broader write scope.\n"
	}
}

func (m *Manager) exploreInstructions() string {
	thoroughness := m.ExploreThoroughness()

	return fmt.Sprintf(
		"You are in EXPLORE mode.\n\n"+
			"This is a READ-ONLY investigation session. You are STRICTLY PROHIBITED from changing files or system state.\n"+
			"Your job is to inspect the codebase, identify relevant evidence, and report findings clearly.\n\n"+
			"Workspace root: %s\n"+
			"Current working directory: %s\n"+
			"Explore thoroughness: %s\n\n"+
			"When you need a tool, respond with ONLY a fenced block in this exact format:\n\n"+
			"```tool\n"+
			"{\"tool\":\"search_content\",\"pattern\":\"TODO\",\"path\":\".\",\"glob\":\"*.go\"}\n"+
			"```\n\n"+
			"Available tools in explore mode:\n"+
			"- pwd\n"+
			"- list_files {\"path\":\".\",\"recursive\":true,\"max_depth\":2}\n"+
			"- search_files {\"pattern\":\"auth\",\"path\":\".\"}\n"+
			"- search_content {\"pattern\":\"token refresh\",\"path\":\".\",\"glob\":\"*.go\"}\n"+
			"- read_file {\"id\":\"f12\",\"start_line\":1,\"end_line\":160}\n"+
			"- read_file {\"path\":\"README.md\",\"start_line\":1,\"end_line\":120} (allowed for an exact existing workspace file; handles are preferred)\n"+
			"- shell {\"command\":\"git diff --stat\",\"workdir\":\".\"}\n%s%s\n"+
			"Explore mode rules:\n"+
			"- Read-only only: do not use mkdir, edit_file, write_file, append_file, package installers, or any state-changing command.\n"+
			"- Start broad once, then narrow quickly. After one broad orientation step, use search_files, search_content, or read_file on discovered handles.\n"+
			"- Do not invent file paths from memory.\n"+
			"- Prefer search_files and search_content over shell. Use read_file once you know what to inspect.\n"+
			"- In explore mode, shell is limited to read-only inspection commands such as ls, find, rg, grep, awk, sed, cat, head, tail, wc, file, pwd, and git status/log/diff/show/grep.\n"+
			"- Use shell only when the native search/read tools are insufficient.\n"+
			"- Adapt depth to the requested thoroughness: quick = minimal confirmation, medium = check a few likely paths, thorough = exhaust reasonable related paths before concluding.\n"+
			"- When you have enough evidence, answer normally with what you found instead of continuing to inspect.\n%s",
		m.cfg.Root,
		m.cfg.CWD,
		thoroughness,
		m.goDiscoveryInstructions(true),
		m.cppDiscoveryInstructions(true),
		m.discoveryInstructions(),
	)
}

func (m *Manager) goDiscoveryInstructions(explore bool) string {
	if !m.isGoWorkspace() {
		return ""
	}

	lines := []string{
		"Go-specific discovery tools:",
		"- go_overview {}",
		"- go_entrypoints {}",
		"- go_find_symbol {\"pattern\":\"Run\"}",
		"- go_find_type {\"pattern\":\"Runner\"}",
		"- go_find_const {\"pattern\":\"ModeExplore\"}",
		"- go_file_outline {\"path\":\"internal/app/app.go\"}",
		"- go_read_symbol {\"pattern\":\"Run\",\"path\":\"internal/agent\"}",
		"- go_find_package {\"pattern\":\"internal/app\"}",
		"- go_related_files {\"pattern\":\"explore cache\"}",
		"- go_definition {\"pattern\":\"Run\",\"path\":\"internal/app\"}",
		"- go_references {\"pattern\":\"Run\",\"path\":\"internal/agent\"}",
		"- go_implementations {\"pattern\":\"Run\",\"path\":\"internal/agent\"}",
		"- go_calls_from {\"pattern\":\"Run\",\"path\":\"internal/agent\"}",
		"- go_calls_into {\"pattern\":\"Run\",\"path\":\"internal/agent\"}",
		"- go_extract_body {\"pattern\":\"Run\",\"path\":\"internal/agent\"}",
		"- go_importers {\"pattern\":\"internal/app\"}",
		"- go_diagnostics {\"path\":\"internal/workspace/manager.go\"}",
		"- go_workspace_symbol {\"pattern\":\"Run\"} (uses gopls when available, otherwise falls back to the local semantic index)",
	}
	if explore {
		lines = append(lines,
			"Go-tool rules:",
			"- Prefer go_overview or go_entrypoints before broad shell inspection in Go repos.",
			"- Prefer go_find_symbol, go_file_outline, go_read_symbol, and go_find_package over raw grep when you already know the concept you are looking for.",
			"- Use go_workspace_symbol when you need broader symbol lookup across the module.",
		)
	} else {
		lines = append(lines,
			"- go_move_declarations {\"path\":\"internal/app/app.go\",\"destination\":\"internal/app/rendering.go\",\"pattern\":\"tag:rendering\"}",
			"- go_move_declarations {\"path\":\"internal/app/app.go\",\"moves\":[{\"destination\":\"internal/app/model_state.go\",\"pattern\":\"tag:model_state\"},{\"destination\":\"internal/app/rendering.go\",\"pattern\":\"tag:rendering\"}]}",
			"- go_move_declarations {\"path\":\"internal/app/app.go\",\"destination\":\"internal/app/rendering.go\",\"declarations\":[\"Model.View\",\"bodyView\",\"renderMessages\"]}",
		)
		lines = append(lines,
			"Go-tool rules:",
			"- Prefer the Go-specific tools over raw shell/grep for package, symbol, and entrypoint discovery in Go repos.",
			"- Use read_file on returned handles after go_find_symbol/go_entrypoints if you need surrounding source.",
			"- For large Go refactors, use go_file_outline to identify declaration boundaries, then use go_move_declarations for model-chosen batches instead of rewriting giant files by hand. The pattern form pattern:\"tag:<responsibility>\" moves all declarations from the outline with that responsibility tag.",
		)
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m *Manager) cppDiscoveryInstructions(explore bool) string {
	if !m.isCppWorkspace() {
		return ""
	}
	lines := []string{
		"C++-specific discovery tools:",
		"- cpp_overview {}",
		"- cpp_entrypoints {}",
		"- cpp_find_symbol {\"pattern\":\"EntityManager\"}",
		"- cpp_read_symbol {\"pattern\":\"EntityManager::tick\",\"path\":\"native/src\"}",
		"- cpp_related_files {\"pattern\":\"inventory transfer\"}",
		"- cpp_definition {\"pattern\":\"post_job\",\"path\":\"native/src\"}",
		"- cpp_declaration {\"pattern\":\"post_job\",\"path\":\"native/src\"}",
		"- cpp_implementations {\"pattern\":\"BaseSystem::tick\",\"path\":\"native/src\"}",
		"- cpp_references {\"pattern\":\"INVALID_ENTITY\",\"path\":\"native/src\"}",
		"- cpp_calls_from {\"pattern\":\"post_job\",\"path\":\"native/src\"}",
		"- cpp_calls_into {\"pattern\":\"post_job\",\"path\":\"native/src\"}",
		"- cpp_extract_body {\"pattern\":\"post_job\",\"path\":\"native/src\"}",
		"- cpp_includes {\"pattern\":\"entity_manager.h\"}",
		"- cpp_who_includes {\"pattern\":\"entity_manager.h\"}",
		"- cpp_blast_radius {\"pattern\":\"entity_manager.h\"}",
		"- cpp_dead_symbol {\"pattern\":\"unused_helper\"}",
		"- cpp_workspace_symbol {\"pattern\":\"EntityManager\"}",
		"- cpp_diagnostics {\"path\":\"native/src/simulation/entity_manager.cpp\"} (uses clangd --check when compile_commands.json is available)",
	}
	if explore {
		lines = append(lines,
			"C++-tool rules:",
			"- Prefer cpp_overview or cpp_entrypoints before broad shell inspection in C/C++ repos.",
			"- Prefer cpp_find_symbol, cpp_read_symbol, cpp_definition, cpp_declaration, cpp_implementations, cpp_includes, and cpp_blast_radius over raw grep once you know the concept you are tracing.",
			"- Use cpp_references and cpp_dead_symbol for grep-style evidence without falling back to shell.",
		)
	} else {
		lines = append(lines,
			"C++-tool rules:",
			"- Prefer the C++-specific tools over raw shell/grep for symbol, include-graph, and blast-radius discovery in C/C++ repos.",
			"- Use read_file on returned handles after cpp_find_symbol or cpp_entrypoints if you need surrounding source.",
		)
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m *Manager) Execute(ctx context.Context, req ToolRequest) ToolResult {
	tool := strings.TrimSpace(req.Tool)
	mode := m.Mode()
	switch tool {
	case "pwd":
		return ToolResult{
			Tool:    tool,
			Summary: "Printed current working directory",
			Output:  m.cfg.CWD,
		}
	case "list_files":
		return m.listFiles(req)
	case "search_files":
		return m.searchFiles(req)
	case "search_content", "rg":
		return m.searchContent(ctx, req, tool)
	case "read_file":
		return m.readFile(req)
	case "go_overview":
		return m.goOverview()
	case "go_entrypoints":
		return m.goEntrypoints(req)
	case "go_find_symbol":
		return m.goFindSymbol(req)
	case "go_find_type":
		return m.goFindType(req)
	case "go_find_const":
		return m.goFindConst(req)
	case "go_file_outline":
		return m.goFileOutline(req)
	case "go_read_symbol":
		return m.goReadSymbol(req)
	case "cpp_overview":
		return m.cppOverview()
	case "cpp_entrypoints":
		return m.cppEntrypoints(req)
	case "cpp_find_symbol":
		return m.cppFindSymbol(req)
	case "cpp_read_symbol":
		return m.cppReadSymbol(req)
	case "cpp_related_files":
		return m.cppRelatedFiles(req)
	case "cpp_definition":
		return m.cppDefinition(ctx, req)
	case "cpp_declaration":
		return m.cppDeclaration(ctx, req)
	case "cpp_implementations":
		return m.cppImplementations(ctx, req)
	case "cpp_references":
		return m.cppReferences(ctx, req)
	case "cpp_calls_from":
		return m.cppCallsFrom(ctx, req)
	case "cpp_calls_into":
		return m.cppCallsInto(ctx, req)
	case "cpp_extract_body":
		return m.cppExtractBody(req)
	case "cpp_includes":
		return m.cppIncludes(req)
	case "cpp_who_includes":
		return m.cppWhoIncludes(req)
	case "cpp_blast_radius":
		return m.cppBlastRadius(req)
	case "cpp_dead_symbol":
		return m.cppDeadSymbol(ctx, req)
	case "cpp_workspace_symbol":
		return m.cppWorkspaceSymbol(ctx, req)
	case "cpp_diagnostics":
		return m.cppDiagnostics(ctx, req)
	case "go_find_package":
		return m.goFindPackage(req)
	case "go_related_files":
		return m.goRelatedFiles(req)
	case "go_definition":
		return m.goDefinition(ctx, req)
	case "go_references":
		return m.goReferences(ctx, req)
	case "go_implementations":
		return m.goImplementations(ctx, req)
	case "go_calls_from":
		return m.goCallsFrom(ctx, req)
	case "go_calls_into":
		return m.goCallsInto(ctx, req)
	case "go_extract_body":
		return m.goExtractBody(req)
	case "go_importers":
		return m.goImporters(req)
	case "go_diagnostics":
		return m.goDiagnostics(ctx, req)
	case "go_workspace_symbol":
		return m.goWorkspaceSymbol(ctx, req)
	case "go_move_declarations":
		if mode == ModeExplore {
			return ToolResult{Tool: "go_move_declarations", Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before moving declarations"}
		}
		return m.goMoveDeclarations(req)
	case "mkdir":
		if mode == ModeExplore {
			return ToolResult{Tool: "mkdir", Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before creating directories"}
		}
		return m.makeDir(req)
	case "edit_file":
		if mode == ModeExplore {
			return ToolResult{Tool: "edit_file", Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before editing files"}
		}
		return m.editFile(req)
	case "write_file":
		if mode == ModeExplore {
			return ToolResult{Tool: "write_file", Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before writing files"}
		}
		return m.writeFile(req, false)
	case "append_file":
		if mode == ModeExplore {
			return ToolResult{Tool: "append_file", Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before appending files"}
		}
		return m.writeFile(req, true)
	case "shell":
		return m.runShell(ctx, req)
	default:
		return ToolResult{
			Tool:    tool,
			Summary: "Tool request rejected",
			Error:   fmt.Sprintf("unsupported tool %q", tool),
		}
	}
}

func ParseToolRequest(text string) (ToolRequest, string, bool, error) {
	payload, ok := extractToolPayload(text)
	if !ok {
		return ToolRequest{}, "", false, nil
	}

	var req ToolRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return ToolRequest{}, payload, true, fmt.Errorf("decode tool request: %w", err)
	}
	if strings.TrimSpace(req.Tool) == "" {
		return ToolRequest{}, payload, true, fmt.Errorf("tool request is missing tool")
	}
	return req, payload, true, nil
}

func FormatToolResultForModel(result ToolResult) string {
	var b strings.Builder
	b.WriteString("[workspace tool result]\n")
	b.WriteString("tool: " + result.Tool + "\n")
	b.WriteString("summary: " + result.Summary + "\n")
	if result.Error != "" {
		b.WriteString("status: error\n")
		b.WriteString("error:\n" + result.Error + "\n")
	} else {
		b.WriteString("status: ok\n")
	}
	if strings.TrimSpace(result.Output) != "" {
		b.WriteString("output:\n")
		b.WriteString(strings.TrimSpace(result.Output))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func FormatToolTrace(result ToolResult) string {
	var b strings.Builder
	b.WriteString(result.Tool)
	if result.Summary != "" {
		b.WriteString(" | " + result.Summary)
	}
	if result.Error != "" {
		b.WriteString("\nerror:\n" + result.Error)
	}
	if strings.TrimSpace(result.Output) != "" {
		b.WriteString("\noutput:\n" + strings.TrimSpace(result.Output))
	}
	return b.String()
}

func extractToolPayload(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	if strings.HasPrefix(trimmed, "```tool") {
		start := strings.Index(trimmed, "\n")
		if start == -1 {
			return "", false
		}
		rest := trimmed[start+1:]
		end := strings.Index(rest, "\n```")
		if end == -1 {
			return "", false
		}
		payload := strings.TrimSpace(rest[:end])
		if object, ok := extractFirstJSONObject(payload); ok {
			return object, true
		}
		return payload, true
	}

	start := strings.Index(trimmed, "```tool")
	if start >= 0 {
		return extractToolPayload(trimmed[start:])
	}

	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"tool"`) {
		if object, ok := extractFirstJSONObject(trimmed); ok {
			return object, true
		}
		return trimmed, true
	}

	return "", false
}

func extractFirstJSONObject(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || text[0] != '{' {
		return "", false
	}

	depth := 0
	inString := false
	escaped := false
	for i, r := range text {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && r == '{':
			depth++
		case !inString && r == '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[:i+1]), true
			}
		}
	}

	return "", false
}

func IsBroadInspectionRequest(req ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "list_files":
		return !IsScopedInspectionRequest(req)
	case "shell":
		return isBroadInspectionShell(req.Command)
	default:
		return false
	}
}

func IsScopedInspectionRequest(req ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "list_files" {
		return false
	}
	path := filepath.Clean(defaultString(req.Path, "."))
	return path != "." && path != string(filepath.Separator)
}

func IsNarrowingRequest(req ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "list_files":
		return IsScopedInspectionRequest(req)
	case "search_files", "search_content", "rg", "read_file",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_importers", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_diagnostics",
		"cpp_overview", "cpp_entrypoints", "cpp_find_symbol", "cpp_read_symbol", "cpp_related_files",
		"cpp_definition", "cpp_declaration", "cpp_implementations", "cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics":
		return true
	case "shell":
		return isNarrowingShell(req.Command)
	default:
		return false
	}
}

func (m *Manager) ShouldClearBroadInspectionLock(req ToolRequest, result ToolResult) bool {
	if result.Error != "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case "list_files":
		return IsScopedInspectionRequest(req)
	case "search_files", "search_content", "rg",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_importers", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations", "go_calls_from", "go_calls_into", "go_extract_body", "go_diagnostics",
		"cpp_overview", "cpp_entrypoints", "cpp_find_symbol", "cpp_read_symbol", "cpp_related_files",
		"cpp_definition", "cpp_declaration", "cpp_implementations", "cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics":
		return true
	case "read_file":
		path, _, err := m.resolveReadTarget(req)
		if err != nil {
			return false
		}
		return m.isRelevantExplorationPath(path)
	case "shell":
		return isNarrowingShell(req.Command)
	default:
		return false
	}
}

func isBroadInspectionShell(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "ls", "find", "tree":
		return true
	default:
		return false
	}
}

func isNarrowingShell(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "rg", "grep", "awk", "sed", "cat", "head", "tail":
		return true
	default:
		return false
	}
}

func validateExploreShellCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is required")
	}
	if strings.ContainsAny(command, "\n\r><`") || strings.Contains(command, "$(") {
		return fmt.Errorf("redirection, command substitution, and multiline shell are not allowed")
	}

	for _, segment := range splitShellSegments(command) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "ls", "find", "rg", "grep", "awk", "sed", "cat", "head", "tail", "wc", "file", "pwd", "tree":
			continue
		case "git":
			if len(fields) < 2 {
				return fmt.Errorf("git command requires a read-only subcommand")
			}
			switch fields[1] {
			case "status", "log", "diff", "show", "grep", "branch", "rev-parse":
				continue
			default:
				return fmt.Errorf("git %s is not allowed in explore mode", fields[1])
			}
		default:
			return fmt.Errorf("%s is not an allowed explore-mode shell command", fields[0])
		}
	}

	return nil
}

func splitShellSegments(command string) []string {
	replacer := strings.NewReplacer("&&", "|", "||", "|", ";", "|")
	normalized := replacer.Replace(command)
	parts := strings.Split(normalized, "|")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

func (m *Manager) listFiles(req ToolRequest) ToolResult {
	base, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "list_files", Summary: "Failed to resolve path", Error: err.Error()}
	}

	info, err := os.Stat(base)
	if err != nil {
		return ToolResult{Tool: "list_files", Summary: "Failed to stat path", Error: err.Error()}
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}

	var entries []listedEntry
	appendEntry := func(path string, isDir bool) bool {
		entries = append(entries, listedEntry{path: path, isDir: isDir})
		return len(entries) < m.cfg.MaxDirectoryEntries
	}

	if !info.IsDir() {
		_ = appendEntry(base, false)
	} else if !req.Recursive {
		children, err := os.ReadDir(base)
		if err != nil {
			return ToolResult{Tool: "list_files", Summary: "Failed to read directory", Error: err.Error()}
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		_ = appendEntry(base, true)
		for _, child := range children {
			childPath := filepath.Join(base, child.Name())
			if m.shouldSkipListEntry(base, childPath, child.IsDir()) {
				continue
			}
			if !appendEntry(childPath, child.IsDir()) {
				break
			}
		}
	} else {
		rootDepth := strings.Count(filepath.Clean(base), string(filepath.Separator))
		_ = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if m.shouldSkipListEntry(base, path, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !appendEntry(path, d.IsDir()) {
				return filepath.SkipAll
			}
			return nil
		})
	}

	m.sortExploreListEntries(entries)

	formatted := make([]string, 0, len(entries))
	for _, entry := range entries {
		formatted = append(formatted, m.formatListedEntry(entry))
	}

	summary := fmt.Sprintf("Listed %d path(s). Files include stable handles. Next step: use search_content/search_files or read_file on a returned handle.", len(formatted))
	if m.IsExploreMode() {
		summary = fmt.Sprintf("Listed %d likely path(s), prioritized toward source files. Files include stable handles. Next step: use search_content/search_files or read_file on a returned handle.", len(formatted))
	}

	return ToolResult{
		Tool:    "list_files",
		Summary: summary,
		Output:  strings.Join(formatted, "\n"),
	}
}

func (m *Manager) formatListedEntry(entry listedEntry) string {
	rel := m.displayPath(entry.path)
	if entry.isDir {
		if rel != "." {
			rel += string(filepath.Separator)
		}
		return rel
	}
	return m.handleDisplay(entry.path)
}

func (m *Manager) shouldSkipListEntry(base string, path string, isDir bool) bool {
	if !isDir || filepath.Clean(path) == filepath.Clean(base) {
		return false
	}

	rel := m.displayPath(path)
	if rel == "." {
		return false
	}

	_, skip := exploreSkippedDirNames[filepath.Base(rel)]
	return skip
}

func (m *Manager) sortExploreListEntries(entries []listedEntry) {
	if !m.IsExploreMode() {
		return
	}

	sort.SliceStable(entries, func(i, j int) bool {
		left := m.exploreListPriority(entries[i])
		right := m.exploreListPriority(entries[j])
		if left != right {
			return left < right
		}
		return m.displayPath(entries[i].path) < m.displayPath(entries[j].path)
	})
}

func (m *Manager) exploreListPriority(entry listedEntry) int {
	rel := m.displayPath(entry.path)
	if rel == "." {
		return -1
	}

	base := filepath.Base(rel)
	if entry.isDir {
		switch rel {
		case "cmd", "internal", "pkg", "prompts", "scripts", "src", "test", "tests":
			return 0
		}
		if strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "internal"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "pkg"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "src"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "scripts"+string(filepath.Separator)) {
			return 1
		}
	}

	switch base {
	case "README", "README.md", "go.mod", "go.sum", "main.go", "config.json", "package.json", "Cargo.toml", "pyproject.toml", "Makefile":
		return 2
	}

	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".md", ".json", ".yaml", ".yml", ".toml":
		return 3
	}

	if strings.HasPrefix(base, ".") {
		return 6
	}
	return 4
}

func (m *Manager) searchFiles(req ToolRequest) ToolResult {
	base, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "search_files", Summary: "Failed to resolve path", Error: err.Error()}
	}

	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "search_files", Summary: "File search rejected", Error: "pattern is required"}
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 4
	}

	query := strings.ToLower(pattern)
	rootDepth := strings.Count(filepath.Clean(base), string(filepath.Separator))
	var matches []string

	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		rel := strings.ToLower(m.displayPath(path))
		name := strings.ToLower(filepath.Base(path))
		if !strings.Contains(rel, query) && !strings.Contains(name, query) {
			return nil
		}

		matches = append(matches, m.handleDisplay(path))
		if len(matches) >= m.cfg.MaxDirectoryEntries {
			return filepath.SkipAll
		}
		return nil
	})

	if len(matches) == 0 {
		return ToolResult{
			Tool:    "search_files",
			Summary: fmt.Sprintf("No file matches for %q", pattern),
			Output:  "",
		}
	}

	sort.Strings(matches)
	return ToolResult{
		Tool:    "search_files",
		Summary: fmt.Sprintf("Found %d matching file(s). Open a returned handle with read_file.", len(matches)),
		Output:  strings.Join(matches, "\n"),
	}
}

func (m *Manager) readFile(req ToolRequest) ToolResult {
	path, description, err := m.resolveReadTarget(req)
	if err != nil {
		result := ToolResult{Tool: "read_file", Summary: "Failed to resolve file target", Error: err.Error()}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = strings.Join(m.findPathSuggestions(req.Path, 5), "\n")
		}
		return result
	}

	data, err := os.ReadFile(path)
	if err != nil {
		result := ToolResult{
			Tool:    "read_file",
			Summary: "Failed to read file",
			Error:   err.Error() + ". Verify the path with search_files or search_content before retrying read_file.",
		}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = strings.Join(m.findPathSuggestions(req.Path, 5), "\n")
		}
		return result
	}
	if len(data) > m.cfg.MaxFileBytes {
		data = data[:m.cfg.MaxFileBytes]
	}

	lines := strings.Split(string(data), "\n")
	start := req.StartLine
	end := req.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ToolResult{Tool: "read_file", Summary: "Invalid line range", Error: "start_line must be <= end_line"}
	}

	var b strings.Builder
	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
	}

	return ToolResult{
		Tool:    "read_file",
		Summary: fmt.Sprintf("Read %s lines %d-%d", description, start, end),
		Output:  strings.TrimRight(b.String(), "\n"),
	}
}

func (m *Manager) goOverview() ToolResult {
	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_overview", Summary: "Go overview unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(4)
	m.PrimeDiscoveredPaths(paths)
	summary := index.Summary()
	if summary == "" {
		summary = "Go semantic index is available, but no summary could be generated."
	}
	return ToolResult{
		Tool:    "go_overview",
		Summary: "Built Go semantic overview for the workspace",
		Output:  summary,
	}
}

func (m *Manager) goEntrypoints(req ToolRequest) ToolResult {
	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_entrypoints", Summary: "Go entrypoints unavailable", Error: err.Error()}
	}

	paths := index.DefaultFiles(goResultLimit(req.MaxDepth, 6))
	if len(paths) == 0 {
		return ToolResult{Tool: "go_entrypoints", Summary: "No Go entrypoints found"}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		file := index.FileByPath(rel)
		display, err := m.goHandleDisplay(rel)
		if err != nil {
			continue
		}
		line := display
		if file != nil {
			line += fmt.Sprintf(" (package %s, funcs=%d, methods=%d, types=%d)", file.Package, file.Funcs, file.Methods, file.Types)
		}
		lines = append(lines, line)
	}
	return ToolResult{
		Tool:    "go_entrypoints",
		Summary: fmt.Sprintf("Found %d likely Go entrypoint file(s). Open a returned handle with read_file.", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goFindSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_find_symbol", Summary: "Go symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_find_symbol", Summary: "Go symbol search unavailable", Error: err.Error()}
	}

	matches := filterSymbolMatches(index.FindSymbols(pattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_find_symbol", Summary: fmt.Sprintf("No Go symbols matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.goHandleDisplay(match.Path)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, goQualifiedSymbol(match)))
	}
	return ToolResult{
		Tool:    "go_find_symbol",
		Summary: fmt.Sprintf("Found %d matching Go symbol(s). Use read_file on a returned handle or go_read_symbol for a focused snippet.", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goFindType(req ToolRequest) ToolResult {
	return m.goFindSymbolsByKind(req, "type", "go_find_type", "type")
}

func (m *Manager) goFindConst(req ToolRequest) ToolResult {
	return m.goFindSymbolsByKind(req, "const", "go_find_const", "constant")
}

func (m *Manager) goFindSymbolsByKind(req ToolRequest, kind string, toolName string, label string) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: toolName, Summary: "Go symbol search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: toolName, Summary: "Go symbol search unavailable", Error: err.Error()}
	}

	matches := filterSymbolMatches(index.FindSymbolsByKind(pattern, kind, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: toolName, Summary: fmt.Sprintf("No Go %s matched %q", label, pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		display, err := m.goHandleDisplay(match.Path)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, goQualifiedSymbol(match)))
	}
	return ToolResult{
		Tool:    toolName,
		Summary: fmt.Sprintf("Found %d matching Go %s(s).", len(lines), label),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goReadSymbol(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_read_symbol", Summary: "Go symbol read rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Go symbol read unavailable", Error: err.Error()}
	}

	matches := filterSymbolMatches(index.FindSymbols(pattern, 4), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_read_symbol", Summary: fmt.Sprintf("No Go symbols matched %q", pattern)}
	}

	match := matches[0]
	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Failed to resolve Go symbol path", Error: err.Error()}
	}
	m.rememberPath(abs)

	start := req.StartLine
	end := req.EndLine
	if start <= 0 || end <= 0 || start > end {
		start = match.Line - 12
		if start < 1 {
			start = 1
		}
		end = match.Line + 20
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Failed to read Go symbol snippet", Error: err.Error()}
	}
	return ToolResult{
		Tool:    "go_read_symbol",
		Summary: fmt.Sprintf("Read %s in %s lines %d-%d", goQualifiedSymbol(match), m.displayPath(abs), start, end),
		Output:  snippet,
	}
}

func (m *Manager) goFindPackage(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_find_package", Summary: "Go package search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_find_package", Summary: "Go package search unavailable", Error: err.Error()}
	}

	matches := filterPackageMatches(index.FindPackages(pattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_find_package", Summary: fmt.Sprintf("No Go packages matched %q", pattern)}
	}

	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("%s (package %s, files=%d, funcs=%d, methods=%d, types=%d)", match.Path, match.Name, match.Files, match.Funcs, match.Methods, match.Types))
	}
	return ToolResult{
		Tool:    "go_find_package",
		Summary: fmt.Sprintf("Found %d matching Go package(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goRelatedFiles(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_related_files", Summary: "Go related-file search rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_related_files", Summary: "Go related-file search unavailable", Error: err.Error()}
	}

	paths := filterRelatedPaths(index.RelevantFiles(strings.Fields(pattern), goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(paths) == 0 {
		return ToolResult{Tool: "go_related_files", Summary: fmt.Sprintf("No related Go files matched %q", pattern)}
	}

	lines := make([]string, 0, len(paths))
	for _, rel := range paths {
		display, err := m.goHandleDisplay(rel)
		if err != nil {
			continue
		}
		lines = append(lines, display)
	}
	return ToolResult{
		Tool:    "go_related_files",
		Summary: fmt.Sprintf("Found %d related Go file(s). Open a returned handle with read_file.", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goWorkspaceSymbol(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_workspace_symbol", Summary: "Go workspace-symbol search rejected", Error: "pattern is required"}
	}

	if gopls := m.goplsBinary(); gopls != "" {
		output, err := m.runGoPLSCommand(ctx, "workspace_symbol", pattern)
		result := ToolResult{
			Tool:    "go_workspace_symbol",
			Summary: "Resolved Go workspace symbols with gopls",
			Output:  m.formatGoplsWorkspaceSymbolOutput(output),
		}
		if err != nil {
			result.Summary = "gopls workspace-symbol search failed"
			result.Error = err.Error()
		}
		if strings.TrimSpace(result.Output) == "" && result.Error == "" {
			result.Summary = fmt.Sprintf("No Go workspace symbols matched %q", pattern)
		}
		return result
	}

	fallback := m.goFindSymbol(req)
	fallback.Tool = "go_workspace_symbol"
	if fallback.Error == "" {
		fallback.Summary += " (semantic fallback; gopls unavailable)"
	} else {
		fallback.Error = "gopls is not installed and semantic fallback did not find a match: " + fallback.Error
	}
	return fallback
}

func (m *Manager) goDefinition(ctx context.Context, req ToolRequest) ToolResult {
	return m.runGoLocationQuery(ctx, "go_definition", "definition", req, "Resolved Go definition")
}

func (m *Manager) goReferences(ctx context.Context, req ToolRequest) ToolResult {
	return m.runGoLocationQuery(ctx, "go_references", "references", req, "Resolved Go references")
}

func (m *Manager) goImplementations(ctx context.Context, req ToolRequest) ToolResult {
	return m.runGoLocationQuery(ctx, "go_implementations", "implementation", req, "Resolved Go implementations")
}

func (m *Manager) goCallsFrom(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "go_calls_from", Summary: "Go outgoing-call query rejected", Error: err.Error()}
	}

	if gopls := m.goplsBinary(); gopls != "" {
		output, err := m.runGoPLSCommand(ctx, "call_hierarchy", m.goSymbolPosition(match))
		result := ToolResult{
			Tool:    "go_calls_from",
			Summary: "Resolved outgoing calls with gopls",
			Output:  m.formatGoCallHierarchy(output, "callee["),
		}
		if err != nil {
			result.Summary = "gopls outgoing-call query failed"
			result.Error = err.Error()
			return result
		}
		if strings.TrimSpace(result.Output) != "" {
			return result
		}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_calls_from", Summary: "Go outgoing-call query unavailable", Error: err.Error()}
	}
	matches := filterCallMatches(index.FindCallsFrom(match.Name, 16), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_calls_from", Summary: fmt.Sprintf("No outgoing Go calls matched %s", goQualifiedSymbol(match))}
	}
	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		callerDisplay, err := m.goHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}
		line := fmt.Sprintf("%s:%d -> %s", callerDisplay, call.Line, call.CalleeName)
		if call.CalleeTarget.Path != "" {
			targetDisplay, err := m.goHandleDisplay(call.CalleeTarget.Path)
			if err == nil {
				line = fmt.Sprintf("%s:%d -> %s %s", callerDisplay, call.Line, targetDisplay, goQualifiedSymbol(call.CalleeTarget))
			}
		}
		lines = append(lines, line)
	}
	return ToolResult{
		Tool:    "go_calls_from",
		Summary: fmt.Sprintf("Found %d outgoing Go call(s) via semantic fallback.", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goCallsInto(ctx context.Context, req ToolRequest) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "go_calls_into", Summary: "Go incoming-call query rejected", Error: err.Error()}
	}

	if gopls := m.goplsBinary(); gopls != "" {
		output, err := m.runGoPLSCommand(ctx, "call_hierarchy", m.goSymbolPosition(match))
		result := ToolResult{
			Tool:    "go_calls_into",
			Summary: "Resolved incoming calls with gopls",
			Output:  m.formatGoCallHierarchy(output, "caller["),
		}
		if err != nil {
			result.Summary = "gopls incoming-call query failed"
			result.Error = err.Error()
			return result
		}
		if strings.TrimSpace(result.Output) != "" {
			return result
		}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_calls_into", Summary: "Go incoming-call query unavailable", Error: err.Error()}
	}
	matches := filterCallMatches(index.FindCallsInto(match.Name, 16), m.goPathFilter(req.Path))
	if len(matches) == 0 {
		return ToolResult{Tool: "go_calls_into", Summary: fmt.Sprintf("No incoming Go calls matched %s", goQualifiedSymbol(match))}
	}
	lines := make([]string, 0, len(matches))
	for _, call := range matches {
		display, err := m.goHandleDisplay(call.Caller.Path)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d -> %s", display, call.Line, goQualifiedSymbol(match)))
	}
	return ToolResult{
		Tool:    "go_calls_into",
		Summary: fmt.Sprintf("Found %d incoming Go call(s) via semantic fallback.", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goExtractBody(req ToolRequest) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: "go_extract_body", Summary: "Go body extraction rejected", Error: err.Error()}
	}
	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return ToolResult{Tool: "go_extract_body", Summary: "Failed to resolve Go symbol path", Error: err.Error()}
	}
	m.rememberPath(abs)

	start := match.StartLine
	end := match.EndLine
	if req.StartLine > 0 {
		start = req.StartLine
	}
	if req.EndLine > 0 {
		end = req.EndLine
	}
	body, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "go_extract_body", Summary: "Failed to extract Go body", Error: err.Error()}
	}
	return ToolResult{
		Tool:    "go_extract_body",
		Summary: fmt.Sprintf("Extracted %s from %s lines %d-%d", goQualifiedSymbol(match), m.displayPath(abs), start, end),
		Output:  body,
	}
}

func (m *Manager) goImporters(req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "go_importers", Summary: "Go importer query rejected", Error: "pattern is required"}
	}

	index, err := m.getGoIndex()
	if err != nil {
		return ToolResult{Tool: "go_importers", Summary: "Go importer query unavailable", Error: err.Error()}
	}

	packages := filterPackageMatches(index.FindPackages(pattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
	if len(packages) == 0 && index.Module != "" {
		modulePattern := strings.TrimPrefix(pattern, index.Module)
		modulePattern = strings.TrimPrefix(modulePattern, "/")
		if modulePattern != pattern {
			packages = filterPackageMatches(index.FindPackages(modulePattern, goResultLimit(req.MaxDepth, 8)), m.goPathFilter(req.Path))
		}
	}
	if len(packages) == 0 {
		return ToolResult{Tool: "go_importers", Summary: fmt.Sprintf("No Go packages matched %q", pattern)}
	}

	targets := make(map[string]string, len(packages))
	for _, pkg := range packages {
		targets[m.goImportPath(pkg.Path, index.Module)] = pkg.Path
	}

	lines := make([]string, 0)
	for _, file := range index.Files {
		for _, imp := range file.Imports {
			targetPath, ok := targets[imp]
			if !ok {
				continue
			}
			display, err := m.goHandleDisplay(file.Path)
			if err != nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s imports %s", display, targetPath))
			break
		}
		if len(lines) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	if len(lines) == 0 {
		return ToolResult{Tool: "go_importers", Summary: fmt.Sprintf("No local Go importers found for %q", pattern)}
	}
	sort.Strings(lines)
	return ToolResult{
		Tool:    "go_importers",
		Summary: fmt.Sprintf("Found %d Go importer file(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goDiagnostics(ctx context.Context, req ToolRequest) ToolResult {
	files, err := m.goDiagnosticTargets(req)
	if err != nil {
		return ToolResult{Tool: "go_diagnostics", Summary: "Go diagnostics unavailable", Error: err.Error()}
	}
	if len(files) == 0 {
		return ToolResult{Tool: "go_diagnostics", Summary: "No Go files available for diagnostics"}
	}

	lines := make([]string, 0, len(files))
	if m.goplsBinary() != "" {
		for _, rel := range files {
			output, err := m.runGoPLSCommand(ctx, "check", rel)
			if err != nil {
				lines = append(lines, fmt.Sprintf("%s: %s", rel, err.Error()))
				if strings.TrimSpace(output) != "" {
					lines = append(lines, m.formatGoLocationOutput(output))
				}
				continue
			}
			output = m.formatGoLocationOutput(output)
			if strings.TrimSpace(output) == "" {
				lines = append(lines, fmt.Sprintf("%s: no warnings or errors", rel))
				continue
			}
			lines = append(lines, output)
		}
		return ToolResult{
			Tool:    "go_diagnostics",
			Summary: fmt.Sprintf("Collected Go diagnostics for %d file(s).", len(files)),
			Output:  strings.Join(lines, "\n"),
		}
	}

	output, err := m.runCommand(ctx, m.cfg.Root, "go", "build", "./...")
	result := ToolResult{
		Tool:    "go_diagnostics",
		Summary: "Collected Go build diagnostics via go build fallback",
		Output:  strings.TrimSpace(output),
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func (m *Manager) runGoLocationQuery(ctx context.Context, toolName string, goplsCommand string, req ToolRequest, successSummary string) ToolResult {
	match, err := m.selectGoSymbolMatch(req)
	if err != nil {
		return ToolResult{Tool: toolName, Summary: "Go location query rejected", Error: err.Error()}
	}
	if m.goplsBinary() != "" {
		args := []string{goplsCommand, m.goSymbolPosition(match)}
		if toolName == "go_references" {
			args = []string{goplsCommand, "-d", m.goSymbolPosition(match)}
		}
		output, err := m.runGoPLSCommand(ctx, args...)
		result := ToolResult{
			Tool:    toolName,
			Summary: successSummary,
			Output:  m.formatGoLocationOutput(output),
		}
		if err != nil {
			result.Summary = "gopls location query failed"
			result.Error = err.Error()
			return result
		}
		if strings.TrimSpace(result.Output) != "" {
			return result
		}
	}

	switch toolName {
	case "go_definition":
		display, err := m.goHandleDisplay(match.Path)
		if err != nil {
			return ToolResult{Tool: toolName, Summary: "Go definition unavailable", Error: err.Error()}
		}
		return ToolResult{
			Tool:    toolName,
			Summary: "Resolved Go definition via semantic fallback",
			Output:  fmt.Sprintf("%s:%d %s %s", display, match.Line, match.Kind, goQualifiedSymbol(match)),
		}
	case "go_references":
		refReq := ToolRequest{Pattern: "\\b" + match.Name + "\\b", Path: ".", Glob: "*.go"}
		if strings.TrimSpace(req.Path) != "" {
			refReq.Path = req.Path
		}
		result := m.searchContent(ctx, refReq, toolName)
		if result.Error == "" {
			result.Summary = "Resolved Go references via semantic/rg fallback"
		}
		return result
	case "go_implementations":
		return ToolResult{Tool: toolName, Summary: "Go implementations unavailable", Error: "gopls is required for implementation queries in this harness"}
	default:
		return ToolResult{Tool: toolName, Summary: "Unsupported Go location query", Error: "unsupported query"}
	}
}

func (m *Manager) runGoPLSCommand(ctx context.Context, args ...string) (string, error) {
	gopls := m.goplsBinary()
	if gopls == "" {
		return "", fmt.Errorf("gopls is not installed")
	}
	cacheRoot := filepath.Join(os.TempDir(), "qwen-bubbles-gopls")
	_ = os.MkdirAll(filepath.Join(cacheRoot, "xdg"), 0o755)
	_ = os.MkdirAll(filepath.Join(cacheRoot, "gocache"), 0o755)
	_ = os.MkdirAll(filepath.Join(cacheRoot, "home"), 0o755)
	stdout, stderr, err := m.runCommandDetailedWithEnv(
		ctx,
		m.cfg.Root,
		[]string{
			"HOME=" + filepath.Join(cacheRoot, "home"),
			"XDG_CACHE_HOME=" + filepath.Join(cacheRoot, "xdg"),
			"GOCACHE=" + filepath.Join(cacheRoot, "gocache"),
		},
		gopls,
		args...,
	)
	output := strings.TrimSpace(stdout)
	if err != nil {
		errText := strings.TrimSpace(stderr)
		if errText != "" {
			if output != "" {
				output += "\n"
			}
			output += errText
		}
		if len(output) > m.cfg.MaxOutputBytes {
			output = output[:m.cfg.MaxOutputBytes] + "\n...[truncated]"
		}
		return output, err
	}
	if len(output) > m.cfg.MaxOutputBytes {
		output = output[:m.cfg.MaxOutputBytes] + "\n...[truncated]"
	}
	return output, nil
}

func (m *Manager) searchContent(parent context.Context, req ToolRequest, toolName string) ToolResult {
	workdir, err := m.resolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: toolName, Summary: "Failed to resolve workdir", Error: err.Error()}
	}

	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: toolName, Summary: "Content search rejected", Error: "pattern is required"}
	}

	args := []string{"-n", "-H", "--hidden", "--no-heading", "--color", "never"}
	if req.Glob != "" {
		args = append(args, "-g", req.Glob)
	}
	args = append(args, pattern, defaultString(req.Path, "."))

	stdout, stderr, err := m.runCommandDetailed(parent, workdir, "rg", args...)
	if err != nil && !isNoMatchExit(err) {
		output := strings.TrimSpace(strings.TrimSpace(stdout) + "\n" + strings.TrimSpace(stderr))
		return ToolResult{
			Tool:    toolName,
			Summary: "Content search failed",
			Output:  strings.TrimSpace(output),
			Error:   err.Error(),
		}
	}
	if isNoMatchExit(err) || strings.TrimSpace(stdout) == "" {
		return ToolResult{
			Tool:    toolName,
			Summary: fmt.Sprintf("No content matches for %q", pattern),
		}
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	formatted := make([]string, 0, len(lines))
	singlePath := ""
	singleLine := 0
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			formatted = append(formatted, line)
			continue
		}

		absPath := filepath.Clean(filepath.Join(workdir, parts[0]))
		handle := m.rememberPath(absPath)
		display := m.displayPath(absPath)
		formatted = append(formatted, fmt.Sprintf("%s %s:%s:%s", handle, display, parts[1], parts[2]))
		if len(lines) == 1 {
			if lineNumber, convErr := strconv.Atoi(parts[1]); convErr == nil {
				singlePath = display
				singleLine = lineNumber
			}
		}
		if len(formatted) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}

	summary := fmt.Sprintf("Found %d matching line(s). Use read_file on a returned handle to inspect a file.", len(formatted))
	if len(formatted) == 1 && singlePath != "" && singleLine > 0 {
		summary = fmt.Sprintf("Found 1 matching line at %s:%d. For a targeted change, prefer edit_file with start_line/end_line/content.", singlePath, singleLine)
		if snippet := m.searchContextSnippet(filepath.Join(m.cfg.Root, singlePath), singleLine, 2); snippet != "" {
			formatted = append(formatted, "context:\n"+snippet)
		}
		formatted = append(formatted, fmt.Sprintf("targeted edit template:\n{\"tool\":\"edit_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d,\"content\":\"replacement line(s)\"}", singlePath, singleLine, singleLine))
	}

	return ToolResult{
		Tool:    toolName,
		Summary: summary,
		Output:  strings.Join(formatted, "\n"),
	}
}

func (m *Manager) searchContextSnippet(path string, lineNumber int, radius int) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	spans := lineSpans(string(content))
	if len(spans) == 0 || lineNumber <= 0 || lineNumber > len(spans) {
		return ""
	}
	startLine := lineNumber - radius
	if startLine < 1 {
		startLine = 1
	}
	endLine := lineNumber + radius
	if endLine > len(spans) {
		endLine = len(spans)
	}

	var b strings.Builder
	for i := startLine; i <= endLine; i++ {
		segment := string(content[spans[i-1].start:spans[i-1].end])
		segment = strings.TrimRight(segment, "\n")
		b.WriteString(fmt.Sprintf("%d:%s\n", i, segment))
	}
	return strings.TrimSpace(b.String())
}

func (m *Manager) isGoWorkspace() bool {
	if !m.Enabled() {
		return false
	}
	info, err := os.Stat(filepath.Join(m.cfg.Root, "go.mod"))
	return err == nil && !info.IsDir()
}

func (m *Manager) getGoIndex() (*semantic.GoIndex, error) {
	if !m.isGoWorkspace() {
		return nil, fmt.Errorf("go-specific tools require a workspace with go.mod at the root")
	}

	token := m.currentGoIndexToken()
	m.mu.Lock()
	cached := m.goIndex
	cachedToken := m.goIndexToken
	m.mu.Unlock()
	if cached != nil && (token == "" || token == cachedToken) {
		return cached, nil
	}

	index, err := semantic.BuildGoIndex(m.cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("build Go semantic index: %w", err)
	}
	if index == nil {
		return nil, fmt.Errorf("no Go files were indexed under the workspace")
	}

	m.mu.Lock()
	m.goIndex = index
	m.goIndexToken = token
	m.mu.Unlock()
	return index, nil
}

func (m *Manager) invalidateGoIndex() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.goIndex = nil
	m.goIndexToken = ""
}

func (m *Manager) currentGoIndexToken() string {
	if !m.Enabled() {
		return ""
	}
	head, err := exec.Command("git", "-C", m.cfg.Root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	status, err := exec.Command("git", "-C", m.cfg.Root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(head)) + "\n" + strings.TrimSpace(string(status))
}

func (m *Manager) goplsBinary() string {
	m.mu.Lock()
	if m.goplsChecked {
		path := m.goplsPath
		m.mu.Unlock()
		return path
	}
	m.mu.Unlock()

	path, _ := exec.LookPath("gopls")
	if path == "" {
		path = m.findGoTool("gopls")
	}

	m.mu.Lock()
	m.goplsPath = path
	m.goplsChecked = true
	m.mu.Unlock()
	return path
}

func (m *Manager) findGoTool(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	if gobin, err := m.goEnv("GOBIN"); err == nil && gobin != "" {
		candidate := filepath.Join(gobin, name)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate
		}
	}
	if gopath, err := m.goEnv("GOPATH"); err == nil && gopath != "" {
		candidate := filepath.Join(gopath, "bin", name)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate
		}
	}
	return ""
}

func (m *Manager) goEnv(key string) (string, error) {
	output, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (m *Manager) goHandleDisplay(rel string) (string, error) {
	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}
	return m.handleDisplay(abs), nil
}

func (m *Manager) readFileLines(path string, start, end int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > m.cfg.MaxFileBytes {
		data = data[:m.cfg.MaxFileBytes]
	}
	lines := strings.Split(string(data), "\n")
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", fmt.Errorf("start_line must be <= end_line")
	}

	var b strings.Builder
	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (m *Manager) goPathFilter(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	resolved, err := m.resolvePath(path, "")
	if err != nil {
		return ""
	}
	return filepath.ToSlash(m.displayPath(resolved))
}

func filterSymbolMatches(matches []semantic.SymbolMatch, pathFilter string) []semantic.SymbolMatch {
	if pathFilter == "" {
		return matches
	}
	filtered := make([]semantic.SymbolMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

func filterPackageMatches(matches []semantic.PackageMatch, pathFilter string) []semantic.PackageMatch {
	if pathFilter == "" {
		return matches
	}
	filtered := make([]semantic.PackageMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Path, pathFilter) {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

func filterRelatedPaths(paths []string, pathFilter string) []string {
	if pathFilter == "" {
		return paths
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if pathMatchesFilter(path, pathFilter) {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func pathMatchesFilter(path string, filter string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	filter = filepath.ToSlash(strings.TrimSpace(filter))
	if path == "" || filter == "" {
		return false
	}
	if path == filter {
		return true
	}
	return strings.HasPrefix(path, filter+"/")
}

func goQualifiedSymbol(match semantic.SymbolMatch) string {
	if match.Kind == "method" && strings.TrimSpace(match.Receiver) != "" {
		return match.Receiver + "." + match.Name
	}
	return match.Name
}

func goResultLimit(requested int, fallback int) int {
	if requested > 0 {
		return requested
	}
	return fallback
}

func (m *Manager) selectGoSymbolMatch(req ToolRequest) (semantic.SymbolMatch, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return semantic.SymbolMatch{}, fmt.Errorf("pattern is required")
	}

	index, err := m.getGoIndex()
	if err != nil {
		return semantic.SymbolMatch{}, err
	}

	pathFilter, err := m.goPathFilterForRequest(req)
	if err != nil {
		return semantic.SymbolMatch{}, err
	}

	matches := filterSymbolMatches(index.FindSymbols(pattern, 12), pathFilter)
	if len(matches) == 0 {
		if pathFilter != "" {
			return semantic.SymbolMatch{}, fmt.Errorf("no Go symbols matched %q under %s", pattern, pathFilter)
		}
		return semantic.SymbolMatch{}, fmt.Errorf("no Go symbols matched %q", pattern)
	}
	return matches[0], nil
}

func (m *Manager) goPathFilterForRequest(req ToolRequest) (string, error) {
	if strings.TrimSpace(req.Path) != "" {
		return m.goPathFilter(req.Path), nil
	}
	if strings.TrimSpace(req.ID) == "" {
		return "", nil
	}
	path, err := m.pathForHandle(req.ID)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(m.displayPath(path)), nil
}

func (m *Manager) goSymbolPosition(match semantic.SymbolMatch) string {
	line := match.Line
	if line <= 0 {
		line = 1
	}
	column := match.Column
	if column <= 0 {
		column = 1
	}
	return fmt.Sprintf("%s:%d:%d", filepath.ToSlash(match.Path), line, column)
}

func (m *Manager) formatGoLocationOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rewritten, ok := m.rewriteGoplsWorkspaceSymbolLine(line); ok {
			formatted = append(formatted, rewritten)
		} else {
			formatted = append(formatted, line)
		}
		if len(formatted) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	return strings.Join(formatted, "\n")
}

func (m *Manager) formatGoCallHierarchy(output string, prefix string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if prefix != "" && !strings.HasPrefix(line, prefix) {
			continue
		}
		if idx := strings.Index(line, " in "); idx >= 0 {
			suffix := line[idx+4:]
			parts := strings.Fields(suffix)
			if len(parts) > 0 {
				if abs, err := m.resolvePath(parts[0], ""); err == nil {
					line = strings.Replace(line, " in "+parts[0], " in "+m.handleDisplay(abs), 1)
				}
			}
		}
		formatted = append(formatted, line)
		if len(formatted) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	return strings.Join(formatted, "\n")
}

func filterCallMatches(matches []semantic.CallMatch, pathFilter string) []semantic.CallMatch {
	if pathFilter == "" {
		return matches
	}
	filtered := make([]semantic.CallMatch, 0, len(matches))
	for _, match := range matches {
		if pathMatchesFilter(match.Caller.Path, pathFilter) || pathMatchesFilter(match.CalleeTarget.Path, pathFilter) {
			filtered = append(filtered, match)
		}
	}
	return filtered
}

func (m *Manager) goDiagnosticTargets(req ToolRequest) ([]string, error) {
	index, err := m.getGoIndex()
	if err != nil {
		return nil, err
	}

	resolveTarget := func(path string) ([]string, error) {
		resolved, err := m.resolvePath(path, "")
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			rel := filepath.ToSlash(m.displayPath(resolved))
			if filepath.Ext(rel) != ".go" {
				return nil, fmt.Errorf("%s is not a Go file", rel)
			}
			return []string{rel}, nil
		}

		filter, err := filepath.Rel(m.cfg.Root, resolved)
		if err != nil {
			return nil, err
		}
		filter = filepath.ToSlash(strings.TrimSpace(filter))
		if filter == "." {
			filter = ""
		}
		files := make([]string, 0)
		for _, file := range index.Files {
			if filter == "" || pathMatchesFilter(file.Path, filter) {
				files = append(files, file.Path)
			}
		}
		sort.Strings(files)
		limit := goResultLimit(req.MaxDepth, 8)
		if limit > 0 && len(files) > limit {
			files = files[:limit]
		}
		return files, nil
	}

	if strings.TrimSpace(req.Path) != "" {
		return resolveTarget(req.Path)
	}
	if strings.TrimSpace(req.ID) != "" {
		path, err := m.pathForHandle(req.ID)
		if err != nil {
			return nil, err
		}
		return resolveTarget(path)
	}

	files := index.IssueHuntFiles(goResultLimit(req.MaxDepth, 4))
	if len(files) == 0 {
		files = index.DefaultFiles(goResultLimit(req.MaxDepth, 4))
	}
	return files, nil
}

func (m *Manager) goImportPath(pkgPath string, module string) string {
	pkgPath = filepath.ToSlash(strings.TrimSpace(pkgPath))
	module = strings.TrimSpace(module)
	if module == "" {
		return pkgPath
	}
	if pkgPath == "." {
		return module
	}
	return module + "/" + pkgPath
}

func (m *Manager) formatGoplsWorkspaceSymbolOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rewritten, ok := m.rewriteGoplsWorkspaceSymbolLine(line); ok {
			formatted = append(formatted, rewritten)
		} else {
			formatted = append(formatted, line)
		}
		if len(formatted) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	return strings.Join(formatted, "\n")
}

func (m *Manager) rewriteGoplsWorkspaceSymbolLine(line string) (string, bool) {
	path, rest, ok := splitGoplsLocation(line)
	if !ok {
		return "", false
	}
	abs, err := m.resolvePath(path, "")
	if err != nil {
		return "", false
	}
	return m.handleDisplay(abs) + ":" + rest, true
}

func splitGoplsLocation(line string) (string, string, bool) {
	first := strings.Index(line, ":")
	if first <= 0 {
		return "", "", false
	}
	second := strings.Index(line[first+1:], ":")
	if second <= 0 {
		return "", "", false
	}
	second += first + 1
	if !allDigits(line[first+1 : second]) {
		return "", "", false
	}
	third := strings.Index(line[second+1:], ":")
	if third <= 0 {
		rangeText := line[second+1:]
		if !allDigitsOrRange(rangeText) {
			return "", "", false
		}
		return line[:first], line[first+1:], true
	}
	third += second + 1
	if !allDigitsOrRange(line[second+1 : third]) {
		return "", "", false
	}
	return line[:first], line[first+1:], true
}

func allDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func allDigitsOrRange(text string) bool {
	if text == "" {
		return false
	}
	for _, ch := range text {
		switch {
		case ch >= '0' && ch <= '9':
		case ch == '-':
		default:
			return false
		}
	}
	return true
}

func (m *Manager) makeDir(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return ToolResult{Tool: "mkdir", Summary: "Write request rejected", Error: "workspace writes are disabled"}
	}
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "mkdir", Summary: "Failed to resolve path", Error: err.Error()}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return ToolResult{Tool: "mkdir", Summary: "Failed to create directory", Error: err.Error()}
	}
	m.invalidateGoIndex()
	m.invalidateCppIndex()
	return ToolResult{
		Tool:    "mkdir",
		Summary: fmt.Sprintf("Created directory %s", m.displayPath(path)),
		Output:  m.displayPath(path),
	}
}

func (m *Manager) writeFile(req ToolRequest, appendMode bool) ToolResult {
	toolName := "write_file"
	if appendMode {
		toolName = "append_file"
	}
	if !m.cfg.EnableWrites {
		return ToolResult{Tool: toolName, Summary: "Write request rejected", Error: "workspace writes are disabled"}
	}
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: toolName, Summary: "Failed to resolve path", Error: err.Error()}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ToolResult{Tool: toolName, Summary: "Failed to create parent directory", Error: err.Error()}
	}

	if appendMode {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return ToolResult{Tool: toolName, Summary: "Failed to open file", Error: err.Error()}
		}
		defer file.Close()
		if _, err := file.WriteString(req.Content); err != nil {
			return ToolResult{Tool: toolName, Summary: "Failed to append file", Error: err.Error()}
		}
	} else {
		if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
			return ToolResult{Tool: toolName, Summary: "Failed to write file", Error: err.Error()}
		}
	}

	action := "Wrote"
	if appendMode {
		action = "Appended"
	}
	m.invalidateGoIndex()
	m.invalidateCppIndex()
	return ToolResult{
		Tool:    toolName,
		Summary: fmt.Sprintf("%s %s", action, m.displayPath(path)),
		Output:  fmt.Sprintf("%s bytes: %d", action, len(req.Content)),
	}
}

func (m *Manager) editFile(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "workspace writes are disabled"}
	}
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "edit_file", Summary: "Failed to resolve path", Error: err.Error()}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Tool: "edit_file", Summary: "Failed to read file", Error: err.Error()}
	}

	current := string(content)
	if req.StartLine > 0 || req.EndLine > 0 {
		return m.editFileByLineRange(path, current, req)
	}
	if strings.TrimSpace(req.OldText) == "" {
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "old_text is required unless you provide start_line/end_line/content"}
	}

	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text did not appear in %s", m.displayPath(path)),
		}
	}
	if matchCount > 1 && !req.ReplaceAll {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text matched %d locations in %s; provide a more specific old_text or set replace_all=true", matchCount, m.displayPath(path)),
		}
	}

	replaced := 1
	updated := strings.Replace(current, req.OldText, req.NewText, 1)
	if req.ReplaceAll {
		replaced = matchCount
		updated = strings.ReplaceAll(current, req.OldText, req.NewText)
	}
	if updated == current {
		return ToolResult{
			Tool:    "edit_file",
			Summary: fmt.Sprintf("No-op edit for %s", m.displayPath(path)),
			Output:  "replacement produced no content change",
		}
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return ToolResult{Tool: "edit_file", Summary: "Failed to write edited file", Error: err.Error()}
	}

	m.invalidateGoIndex()
	m.invalidateCppIndex()
	return ToolResult{
		Tool:    "edit_file",
		Summary: fmt.Sprintf("Edited %s", m.displayPath(path)),
		Output:  fmt.Sprintf("replaced occurrences: %d", replaced),
	}
}

func (m *Manager) editFileByLineRange(path string, current string, req ToolRequest) ToolResult {
	startLine := req.StartLine
	endLine := req.EndLine
	if startLine <= 0 {
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "start_line must be greater than 0"}
	}
	if endLine == 0 {
		endLine = startLine
	}
	if endLine < startLine {
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "end_line must be greater than or equal to start_line"}
	}

	spans := lineSpans(current)
	if len(spans) == 0 {
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "cannot apply a line edit to an empty file"}
	}
	if startLine > len(spans) || endLine > len(spans) {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("requested lines %d-%d are outside %s (file has %d lines)", startLine, endLine, m.displayPath(path), len(spans)),
		}
	}

	startOffset := spans[startLine-1].start
	endOffset := spans[endLine-1].end
	updated := current[:startOffset] + req.Content + current[endOffset:]
	if updated == current {
		return ToolResult{
			Tool:    "edit_file",
			Summary: fmt.Sprintf("No-op edit for %s", m.displayPath(path)),
			Output:  "replacement produced no content change",
		}
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return ToolResult{Tool: "edit_file", Summary: "Failed to write edited file", Error: err.Error()}
	}

	m.invalidateGoIndex()
	return ToolResult{
		Tool:    "edit_file",
		Summary: fmt.Sprintf("Edited %s lines %d-%d", m.displayPath(path), startLine, endLine),
		Output:  fmt.Sprintf("replaced lines: %d-%d", startLine, endLine),
	}
}

type lineSpan struct {
	start int
	end   int
}

func lineSpans(text string) []lineSpan {
	if text == "" {
		return nil
	}
	spans := make([]lineSpan, 0, strings.Count(text, "\n")+1)
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		spans = append(spans, lineSpan{start: start, end: i + 1})
		start = i + 1
	}
	if start < len(text) {
		spans = append(spans, lineSpan{start: start, end: len(text)})
	}
	return spans
}

func (m *Manager) runShell(ctx context.Context, req ToolRequest) ToolResult {
	if !m.cfg.EnableShell {
		return ToolResult{Tool: "shell", Summary: "Shell request rejected", Error: "shell execution is disabled"}
	}
	workdir, err := m.resolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: "shell", Summary: "Failed to resolve workdir", Error: err.Error()}
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return ToolResult{Tool: "shell", Summary: "Shell request rejected", Error: "command is required"}
	}
	if m.Mode() == ModeExplore {
		if err := validateExploreShellCommand(command); err != nil {
			return ToolResult{
				Tool:    "shell",
				Summary: "Shell request rejected",
				Error:   "explore mode only allows read-only inspection shell commands: " + err.Error(),
			}
		}
	}

	output, execErr := m.runCommand(ctx, workdir, m.cfg.Shell, "-lc", command)
	result := ToolResult{
		Tool:    "shell",
		Summary: fmt.Sprintf("Ran shell command in %s", m.displayPath(workdir)),
		Output:  "$ " + command + "\n" + strings.TrimSpace(output),
	}
	if execErr != nil {
		result.Error = execErr.Error()
	} else if m.Mode() != ModeExplore {
		m.invalidateGoIndex()
		m.invalidateCppIndex()
	}
	return result
}

func (m *Manager) runCommand(parent context.Context, dir, name string, args ...string) (string, error) {
	stdout, stderr, err := m.runCommandDetailed(parent, dir, name, args...)
	output := strings.TrimSpace(stdout)
	errText := strings.TrimSpace(stderr)
	if errText != "" {
		if output != "" {
			output += "\n"
		}
		output += errText
	}
	if len(output) > m.cfg.MaxOutputBytes {
		output = output[:m.cfg.MaxOutputBytes] + "\n...[truncated]"
	}
	if err != nil {
		return output, err
	}
	return output, nil
}

func (m *Manager) runCommandDetailed(parent context.Context, dir, name string, args ...string) (string, string, error) {
	return m.runCommandDetailedWithEnv(parent, dir, nil, name, args...)
}

func (m *Manager) runCommandDetailedWithEnv(parent context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error) {
	timeout := timeDurationSeconds(m.cfg.CommandTimeoutSeconds)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("command timed out after %ds", m.cfg.CommandTimeoutSeconds)
	}
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("command failed: %w", err)
	}
	return stdout.String(), stderr.String(), nil
}

func (m *Manager) resolveWorkdir(workdir string) (string, error) {
	if !m.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}
	base := m.cfg.CWD
	if strings.TrimSpace(workdir) == "" || workdir == "." {
		return base, nil
	}
	return m.resolvePath(workdir, "")
}

func (m *Manager) resolvePath(path string, workdir string) (string, error) {
	if !m.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}

	var candidate string
	switch {
	case filepath.IsAbs(path):
		candidate = filepath.Clean(path)
	case workdir != "":
		base, err := m.resolveWorkdir(workdir)
		if err != nil {
			return "", err
		}
		candidate = filepath.Clean(filepath.Join(base, path))
	default:
		candidate = filepath.Clean(filepath.Join(m.cfg.CWD, path))
	}

	rel, err := filepath.Rel(m.cfg.Root, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace root", path)
	}
	return candidate, nil
}

func (m *Manager) resolveReadTarget(req ToolRequest) (string, string, error) {
	if strings.TrimSpace(req.ID) != "" {
		path, err := m.pathForHandle(req.ID)
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(req.Path) != "" {
			pathFromRequest, err := m.resolvePath(req.Path, req.Workdir)
			if err != nil {
				return "", "", err
			}
			if filepath.Clean(pathFromRequest) != filepath.Clean(path) {
				return "", "", fmt.Errorf(
					"read_file id %q resolves to %q but path %q resolves to %q; use either the discovered handle or the matching path, not conflicting values",
					strings.TrimSpace(req.ID),
					m.displayPath(path),
					req.Path,
					m.displayPath(pathFromRequest),
				)
			}
		}
		return path, m.handleDisplay(path), nil
	}

	if strings.TrimSpace(req.Path) == "" {
		return "", "", fmt.Errorf("read_file requires id or path")
	}

	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return "", "", err
	}
	if !m.hasDiscoveredPath(path) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return "", "", fmt.Errorf("path %q was not found; use search_files or search_content and then read a returned handle", m.displayPath(path))
		}
		if info.IsDir() {
			return "", "", fmt.Errorf("path %q is a directory; use list_files or search_files to choose a file", m.displayPath(path))
		}
		m.rememberPath(path)
	}
	return path, m.handleDisplay(path), nil
}

func (m *Manager) isRelevantExplorationPath(path string) bool {
	rel := m.displayPath(filepath.Clean(path))
	if rel == "" || rel == "." {
		return false
	}

	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 0 {
		if _, skip := exploreSkippedDirNames[parts[0]]; skip {
			return false
		}
	}

	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".md", ".json", ".yaml", ".yml", ".toml":
		return true
	}

	switch filepath.Base(rel) {
	case "README", "README.md", "go.mod", "go.sum", "Makefile":
		return true
	}

	return strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "internal"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "pkg"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "src"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "scripts"+string(filepath.Separator))
}

func (m *Manager) displayPath(path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(m.cfg.Root, path)
	if err != nil || rel == "." {
		return path
	}
	return rel
}

func (m *Manager) handleDisplay(path string) string {
	return fmt.Sprintf("%s %s", m.rememberPath(path), m.displayPath(path))
}

func (m *Manager) rememberPath(path string) string {
	path = filepath.Clean(path)

	m.mu.Lock()
	defer m.mu.Unlock()

	if handle, ok := m.pathToHandle[path]; ok {
		return handle
	}

	handle := fmt.Sprintf("f%d", m.nextHandle)
	m.nextHandle++
	m.pathToHandle[path] = handle
	m.handleToPath[handle] = path
	return handle
}

func (m *Manager) PrimeDiscoveredPaths(paths []string) {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		resolved, err := m.resolvePath(path, "")
		if err != nil {
			continue
		}
		m.rememberPath(resolved)
	}
}

func (m *Manager) hasDiscoveredPath(path string) bool {
	path = filepath.Clean(path)

	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.pathToHandle[path]
	return ok
}

func (m *Manager) pathForHandle(handle string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path, ok := m.handleToPath[strings.TrimSpace(handle)]
	if !ok {
		return "", fmt.Errorf("unknown file handle %q", handle)
	}
	return path, nil
}

func (m *Manager) findPathSuggestions(query string, limit int) []string {
	if !m.Enabled() || limit <= 0 {
		return nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	raw := strings.ToLower(filepath.Base(query))
	stem := strings.TrimSuffix(raw, filepath.Ext(raw))
	terms := []string{raw}
	if stem != "" && stem != raw {
		terms = append(terms, stem)
	}

	var suggestions []string
	scanned := 0
	_ = filepath.WalkDir(m.cfg.Root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if m.shouldSkipListEntry(m.cfg.Root, path, true) {
				return filepath.SkipDir
			}
			return nil
		}

		scanned++
		if scanned > m.cfg.MaxDirectoryEntries*10 {
			return filepath.SkipAll
		}

		rel := strings.ToLower(m.displayPath(path))
		base := strings.ToLower(filepath.Base(path))
		match := false
		for _, term := range terms {
			if term == "" {
				continue
			}
			if strings.Contains(base, term) || strings.Contains(rel, term) {
				match = true
				break
			}
		}
		if !match {
			return nil
		}

		suggestions = append(suggestions, m.handleDisplay(path))
		if len(suggestions) >= limit {
			return filepath.SkipAll
		}
		return nil
	})

	sort.Strings(suggestions)
	return suggestions
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func timeDurationSeconds(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}

func isNoMatchExit(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "exit status 1")
}

func (m *Manager) discoveryInstructions() string {
	type discoveryTool struct {
		Path        string
		Description string
		Example     string
	}

	tools := []discoveryTool{
		{
			Path:        "scripts/lookup.sh",
			Description: "enum and constant lookup",
			Example:     `./scripts/lookup.sh --enum AnimalState`,
		},
		{
			Path:        "scripts/trace_code.sh",
			Description: "code tracing for callers, refs, systems, jobs, and overrides",
			Example:     `./scripts/trace_code.sh --callers kill_animal`,
		},
		{
			Path:        "scripts/gdscript_trace.sh",
			Description: "GDScript wiring, refs, callers, and dependencies",
			Example:     `./scripts/gdscript_trace.sh --wiring scripts/world/thought_bubble_overlay.gd`,
		},
		{
			Path:        "scripts/boundary.sh",
			Description: "Godot/native boundary inventory",
			Example:     `./scripts/boundary.sh --inventory --json`,
		},
		{
			Path:        "scripts/validate_discovery_tools.py",
			Description: "discovery tool inventory and test-lane lookup",
			Example:     `python3 scripts/validate_discovery_tools.py --inventory`,
		},
	}

	var lines []string
	for _, tool := range tools {
		if !m.fileExists(tool.Path) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s. Example: `%s`", tool.Path, tool.Description, tool.Example))
	}

	if len(lines) == 0 {
		return ""
	}

	return "\n\nRepo-local discovery commands available in this workspace:\n" +
		strings.Join(lines, "\n") +
		"\nThese repo-local shell commands have the same native CLI capability surface here that Codex has when it runs them from the terminal." +
		"\nDo not treat them as the default path. Prefer generic workspace tools such as search_files, search_content, read_file, and ordinary shell commands first." +
		"\nUse these repo-local discovery commands only when the user explicitly asks for them, when a repo workflow clearly depends on them, or when they are the most direct way to answer a repository-specific question."
}

func (m *Manager) fileExists(rel string) bool {
	if !m.Enabled() {
		return false
	}
	_, err := os.Stat(filepath.Join(m.cfg.Root, rel))
	return err == nil
}
