package workspace

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"idiom/internal/config"
	"idiom/internal/discovery"
	"idiom/internal/mcp"
	"idiom/internal/semantic"
)

	type Manager struct {
	cfg config.Workspace

	mode                  Mode
	thoroughness          ExploreThoroughness
	writePolicy           WritePolicy
	bypassReadBeforeEdit  bool
	mu                    sync.Mutex
	nextHandle            int
	pathToHandle          map[string]string
	handleToPath          map[string]string
	observedPaths         map[string]struct{}
	permissionRules       map[string][]ToolRule
	mcpRegistry           *mcp.Registry
	externalToolsRegistry *ExternalToolRegistry
	goIndex         *semantic.GoIndex
	goIndexToken    string
	cppIndex        *semantic.CppIndex
	cppIndexToken   string
	goplsPath       string
	goplsChecked    bool
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
	Query        string              `json:"query,omitempty"`
	Purpose      string              `json:"purpose,omitempty"`
	Language     string              `json:"language,omitempty"`
	Glob         string              `json:"glob,omitempty"`
	Workdir      string              `json:"workdir,omitempty"`
	Command      string              `json:"command,omitempty"`
	Prompt       string              `json:"prompt,omitempty"`
	Agent        string              `json:"agent,omitempty"`
	Mode         string              `json:"mode,omitempty"`
	Content      string              `json:"content,omitempty"`
	OldText      string              `json:"old_text,omitempty"`
	NewText      string              `json:"new_text,omitempty"`
	ReplaceAll   bool                `json:"replace_all,omitempty"`
	Recursive    bool                `json:"recursive,omitempty"`
	MaxDepth     int                 `json:"max_depth,omitempty"`
	Destination  string              `json:"destination,omitempty"`
	Declarations []string            `json:"declarations,omitempty"`
	Moves        []GoDeclarationMove `json:"moves,omitempty"`
	Todos        []TaskLedgerItem    `json:"todos,omitempty"`
	Items        []TaskLedgerItem    `json:"items,omitempty"`
	// NamedGap explains one exact unresolved claim a post-readiness
	// follow-up tool call is trying to verify or falsify. It should be
	// specific enough that the next discovery move is obvious (for
	// example, inspect one function body, one caller branch, or one
	// implementation boundary). Optional in general, but required by the
	// runner once answer-readiness or child-packet sufficiency has
	// already been reached and the model still wants another read/search
	// step.
	NamedGap string `json:"named_gap,omitempty"`
	// WhyNotAnswering is a short explanation for what would be materially
	// wrong or missing if the model answered now despite already reaching
	// a closure-biased state. Optional, and usually paired with NamedGap
	// on post-readiness reads.
	WhyNotAnswering string `json:"why_not_answering,omitempty"`
	// Description is the tactical intent for a write-tool call — why this
	// edit, in one short sentence. Required for edit_file, write_file,
	// append_file, and apply_patch. Used by the plan-panel autodoc to
	// label each write with its purpose.
	Description string `json:"description,omitempty"`
	// TaskID, when set on a write-tool call, declares "this write
	// fulfills planned task <id>". The plan-panel autodoc uses it to
	// attach the write to that ledger item even when the original
	// task_ledger entry didn't list `paths`. Optional; falls back to
	// path-matching and a single-pending heuristic when empty.
	TaskID string `json:"task_id,omitempty"`
	// Args carries the raw, model-emitted argument JSON for
	// `mcp:<server>/<tool>` calls. MCP-discovered tools have arbitrary
	// JSON-Schema parameters that do not map onto this struct's typed
	// fields, so the agent layer routes the raw bytes through unchanged
	// and Manager.Execute hands them to mcp.Registry.Call. Empty for
	// every built-in tool — they keep using the typed fields above.
	Args json.RawMessage `json:"-"`
}

type GoDeclarationMove struct {
	Destination  string   `json:"destination,omitempty"`
	Pattern      string   `json:"pattern,omitempty"`
	Declarations []string `json:"declarations,omitempty"`
}

type TaskLedgerItem struct {
	ID       string `json:"id,omitempty"`
	Content  string `json:"content,omitempty"`
	Status   string `json:"status,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	// Paths lists the workspace-relative files this item plans to
	// touch. The plan autodoc uses it to attach a completed write to
	// the planned item (checking its box in place) instead of adding a
	// duplicate entry under "Unplanned". Empty when the item is a
	// non-write task or the paths are not yet known.
	Paths []string `json:"paths,omitempty"`
}

type ToolResult struct {
	Tool          string
	Summary       string
	Output        string
	Error         string
	PrecisionTier string `json:"precision_tier,omitempty"`
}

type listedEntry struct {
	path  string
	isDir bool
}

const searchContentLineCap = 240
const listFilesDisplayCap = 80

func NewManager(cfg config.Workspace) *Manager {
	return &Manager{
		cfg:           cfg,
		mode:          ModeNormal,
		thoroughness:  ThoroughnessMedium,
		writePolicy:   WritePolicyGuarded,
		nextHandle:    1,
		pathToHandle:  make(map[string]string),
		handleToPath:  make(map[string]string),
		observedPaths: make(map[string]struct{}),
	}
}

// MarkPathObserved records that a caller has successfully read or enumerated
// the given path during this session. edit_file consults this set to enforce
// the read-before-edit invariant (Fix 1 in docs/opencode-fixes-design.md).
// Passing an empty path is a no-op.
func (m *Manager) MarkPathObserved(path string) {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.observedPaths == nil {
		m.observedPaths = make(map[string]struct{})
	}
	m.observedPaths[normalizePathKey(normalized)] = struct{}{}
}

// HasObservedPath reports whether MarkPathObserved has been called with the
// given path (or any case/separator variation of it) during this session.
func (m *Manager) HasObservedPath(path string) bool {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.observedPaths == nil {
		return false
	}
	_, ok := m.observedPaths[normalizePathKey(normalized)]
	return ok
}

func normalizePathKey(path string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	return strings.ToLower(cleaned)
}

func (m *Manager) SetBypassReadBeforeEdit(bypass bool) {
	m.bypassReadBeforeEdit = bypass
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

// SetMCPRegistry installs the registry of MCP-discovered tools so that
// (1) BuildToolDefinitions can fan them into the LLM tool spec and
// (2) Manager.Execute can dispatch `mcp:<server>/<tool>` calls.
// Called once at session startup from the app layer; passing nil is a
// no-op. Safe to call from any goroutine.
func (m *Manager) SetMCPRegistry(registry *mcp.Registry) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mcpRegistry = registry
}

// MCPRegistry returns the currently-installed MCP registry, or nil when
// none has been wired in. Reads are cheap; the returned pointer is the
// shared instance — do not mutate it.
func (m *Manager) MCPRegistry() *mcp.Registry {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mcpRegistry
}

// SetExternalToolsRegistry installs the registry of project-defined
// external shell tools so that (1) BuildToolDefinitions can fan them
// into the LLM tool spec and (2) Manager.Execute can dispatch them.
// Called once at session startup from the app layer; passing nil is a
// no-op. Safe to call from any goroutine.
func (m *Manager) SetExternalToolsRegistry(registry *ExternalToolRegistry) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.externalToolsRegistry = registry
}

// ExternalToolsRegistry returns the currently-installed external tools
// registry, or nil if none.
func (m *Manager) ExternalToolsRegistry() *ExternalToolRegistry {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.externalToolsRegistry
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
	if m.Mode() == ModeExplore {
		return m.exploreInstructions()
	}
	// OpenCode-aligned: tools are delivered via the OpenAI function-calling
	// channel (request body `tools` + assistant `tool_calls`), not via
	// prose instructions in the system prompt. We keep only a terse scope
	// note plus a few rules that *aren't* expressible in tool descriptions.
	return fmt.Sprintf(
		"Workspace root: %s\nWorking directory: %s\n\n"+
			"Rules:\n"+
			"- Do not invent file paths from memory. Use list_files, search_files, discovered handles, or language-specific symbol tools to discover real files first.\n"+
			"- After an orientation call, narrow with structured language tools or discovered handles first. Use search_content / read_file only when semantic probes do not land or when you need prose or exact ranges.\n"+
			"- Paths are relative to the working directory unless absolute.\n"+
			"- Prefer edit_file for small localized changes; write_file overwrites.\n"+
			"%s"+
			"- shell runs with the same OS permissions as this process. Prefer the dedicated tools where one exists.\n"+
			"- After tool results, either answer directly or take one exact next tool step.",
		m.cfg.Root,
		m.cfg.CWD,
		m.writePolicyInstructions(),
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
	return fmt.Sprintf(
		"You are in EXPLORE mode — a READ-ONLY investigation session. Do not change files or system state.\n\n"+
			"Workspace root: %s\nWorking directory: %s\nThoroughness: %s\n\n"+
			"Rules:\n"+
			"- Start broad once, then narrow with structured language tools or discovered handles first. Use search_content / read_file only when semantic probes do not land or when you need prose or exact ranges.\n"+
			"- Do not invent file paths. Use search tools first.\n"+
			"- shell is limited to read-only utilities (ls, find, rg, grep, awk, sed, cat, head, tail, wc, file, pwd, git status/log/diff/show/grep).\n"+
			"- When you have enough evidence, answer directly. Continue only for one exact missing claim and one exact probe.",
		m.cfg.Root,
		m.cfg.CWD,
		m.ExploreThoroughness(),
	)
}

func (m *Manager) toolManifestTools(readOnly bool) []string {
	return ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: !readOnly,
		IncludeTask:       true,
		IncludeWriteTools: !readOnly,
		IncludeGo:         m.isGoWorkspace(),
		IncludeCpp:        m.isCppWorkspace(),
		IncludePython:     m.isPythonWorkspace(),
		IncludeTypescript: m.isTsWorkspace(),
		IncludeRust:       m.isRustWorkspace(),
		IncludeJava:       m.isJavaWorkspace(),
	})
}

// AllowedTools returns the list of tool names this session is permitted
// to call. Used by the agent package to build OpenAI function-calling
// schemas for the request. Read-only is keyed off the workspace Mode
// (ModeExplore) so the model doesn't see write tools when the session
// can't use them. Names from the attached MCP registry (if any) are
// appended in deterministic order using their qualified `mcp:<server>/<tool>`
// form, matching what the model will see in the tool spec.
func (m *Manager) AllowedTools() []string {
	readOnly := m.Mode() == ModeExplore
	tools := m.toolManifestTools(readOnly)
	if registry := m.MCPRegistry(); registry != nil {
		names := make([]string, 0)
		for qualified := range registry.Tools() {
			names = append(names, qualified)
		}
		sort.Strings(names)
		tools = append(tools, names...)
	}
	if registry := m.ExternalToolsRegistry(); registry != nil {
		names := registry.Names()
		sort.Strings(names)
		tools = append(tools, names...)
	}
	return tools
}

// BuildToolDefinitions returns the OpenAI-shaped function specs the
// agent sends to the LLM. Built-in tools come from the package-level
// BuildToolDefinitions (hand-authored schemas under tool_schemas.go);
// MCP-discovered tools (when an MCP registry is wired in via
// SetMCPRegistry) are appended with the qualified name
// `mcp:<server>/<tool>`, the upstream description prefixed with
// `[mcp:<server>] ` for provenance, and the upstream JSON-Schema
// inputSchema passed through unchanged.
//
// allowed filters by name; an empty allowed slice means "every
// available tool". MCP tools respect the same allow-list.
func (m *Manager) BuildToolDefinitions(allowed []string) []ToolDefinition {
	defs := BuildToolDefinitions(allowed)
	var allowedSet map[string]struct{}
	if len(allowed) > 0 {
		allowedSet = make(map[string]struct{}, len(allowed))
		for _, name := range allowed {
			if name != "" {
				allowedSet[name] = struct{}{}
			}
		}
	}
	// Add project-defined external tools BEFORE the MCP early-return so
	// they reach the LLM tool spec even when no MCP servers are wired in.
	defs = appendExternalToolDefinitions(defs, m.ExternalToolsRegistry(), allowedSet)
	registry := m.MCPRegistry()
	if registry == nil {
		return defs
	}
	tools := registry.Tools()
	names := make([]string, 0, len(tools))
	for qualified := range tools {
		if allowedSet != nil {
			if _, ok := allowedSet[qualified]; !ok {
				continue
			}
		}
		names = append(names, qualified)
	}
	sort.Strings(names)
	for _, qualified := range names {
		tool := tools[qualified]
		server, _, _ := mcp.ParseQualifiedToolName(qualified)
		description := tool.Description
		if description == "" {
			description = "MCP tool from " + server
		}
		if server != "" {
			description = "[mcp:" + server + "] " + description
		}
		params := string(tool.InputSchema)
		if params == "" {
			params = `{"type":"object","properties":{}}`
		}
		defs = append(defs, ToolDefinition{
			Name:           qualified,
			Description:    description,
			ParametersJSON: params,
		})
	}
	return defs
}

// appendExternalToolDefinitions appends ToolDefinitions for every entry
// in the registry, filtered by allowedSet (when non-empty). Sorted for
// deterministic tool-spec order. Pulled out of BuildToolDefinitions so
// the externals merge runs whether or not an MCP registry is attached.
func appendExternalToolDefinitions(defs []ToolDefinition, extReg *ExternalToolRegistry, allowedSet map[string]struct{}) []ToolDefinition {
	if extReg == nil {
		return defs
	}
	extDefs := extReg.Definitions()
	sort.Slice(extDefs, func(i, j int) bool { return extDefs[i].Name < extDefs[j].Name })
	for _, ed := range extDefs {
		if allowedSet != nil {
			if _, ok := allowedSet[ed.Name]; !ok {
				continue
			}
		}
		defs = append(defs, ed)
	}
	return defs
}

func (m *Manager) Execute(ctx context.Context, req ToolRequest) (result ToolResult) {
	defer func() {
		result = annotateLanguagePrecisionTier(result)
	}()

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
	case "discovery_query":
		return m.discoveryQuery(ctx, req)
	case "structural_search":
		return m.structuralSearch(ctx, req)
	case "dependency_graph":
		return m.dependencyGraph(ctx, req)
	case "history_range":
		return m.historyRange(ctx, req)
	case "history_symbol":
		return m.historySymbol(ctx, req)
	case "blame_range":
		return m.blameRange(ctx, req)
	case "ctags_symbols":
		return m.ctagsSymbols(ctx, req)
	case "semgrep_scan":
		return m.semgrepScan(ctx, req)
	case "joern_scan":
		return m.joernScan(ctx, req)
	case "go_overview":
		return m.goOverview()
	case "go_entrypoints":
		return m.goEntrypoints(req)
	case "go_find_symbol":
		return m.goFindSymbol(req)
	case "go_find_function":
		return m.goFindFunction(req)
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
	case "cpp_file_outline":
		return m.cppFileOutline(req)
	case "python_overview":
		return m.pythonOverview()
	case "python_entrypoints":
		return m.pythonEntrypoints(req)
	case "python_find_symbol":
		return m.pythonFindSymbol(req)
	case "python_read_symbol":
		return m.pythonReadSymbol(req)
	case "python_related_files":
		return m.pythonRelatedFiles(req)
	case "python_definition":
		return m.pythonDefinition(ctx, req)
	case "python_implementations":
		return m.pythonImplementations(ctx, req)
	case "python_references":
		return m.pythonReferences(ctx, req)
	case "python_calls_from":
		return m.pythonCallsFrom(ctx, req)
	case "python_calls_into":
		return m.pythonCallsInto(ctx, req)
	case "python_workspace_symbol":
		return m.pythonWorkspaceSymbol(ctx, req)
	case "python_diagnostics":
		return m.pythonDiagnostics(ctx, req)
	case "python_file_outline":
		return m.pythonFileOutline(req)
	case "ts_overview":
		return m.tsOverview()
	case "ts_entrypoints":
		return m.tsEntrypoints(req)
	case "ts_find_symbol":
		return m.tsFindSymbol(req)
	case "ts_read_symbol":
		return m.tsReadSymbol(req)
	case "ts_related_files":
		return m.tsRelatedFiles(req)
	case "ts_definition":
		return m.tsDefinition(ctx, req)
	case "ts_implementations":
		return m.tsImplementations(ctx, req)
	case "ts_references":
		return m.tsReferences(ctx, req)
	case "ts_calls_from":
		return m.tsCallsFrom(ctx, req)
	case "ts_calls_into":
		return m.tsCallsInto(ctx, req)
	case "ts_workspace_symbol":
		return m.tsWorkspaceSymbol(ctx, req)
	case "ts_diagnostics":
		return m.tsDiagnostics(ctx, req)
	case "ts_file_outline":
		return m.tsFileOutline(req)
	case "rust_overview",
		"rust_entrypoints",
		"rust_find_symbol",
		"rust_workspace_symbol",
		"rust_read_symbol",
		"rust_related_files",
		"rust_file_outline",
		"rust_definition",
		"rust_references",
		"rust_calls_from",
		"rust_calls_into",
		"rust_implementations",
		"rust_diagnostics":
		return m.ExecuteRustTool(ctx, req)
	case "java_overview":
		return m.javaOverview()
	case "java_entrypoints":
		return m.javaEntrypoints(req)
	case "java_find_symbol":
		return m.javaFindSymbol(req)
	case "java_read_symbol":
		return m.javaReadSymbol(req)
	case "java_related_files":
		return m.javaRelatedFiles(req)
	case "java_workspace_symbol":
		return m.javaWorkspaceSymbol(ctx, req)
	case "java_definition":
		return m.javaDefinition(ctx, req)
	case "java_references":
		return m.javaReferences(ctx, req)
	case "java_implementations":
		return m.javaImplementations(ctx, req)
	case "java_calls_from":
		return m.javaCallsFrom(ctx, req)
	case "java_calls_into":
		return m.javaCallsInto(ctx, req)
	case "java_diagnostics":
		return m.javaDiagnostics(ctx, req)
	case "java_file_outline":
		return m.javaFileOutline(req)
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
	case "apply_patch":
		if mode == ModeExplore {
			return ToolResult{Tool: "apply_patch", Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before applying patches"}
		}
		return m.applyPatch(req)
	case "shell":
		return m.runShell(ctx, req)
	case "git_status":
		return m.gitStatus(ctx, req)
	case "git_diff":
		return m.gitDiff(ctx, req)
	default:
		if strings.HasPrefix(tool, "mcp:") {
			return m.executeMCP(ctx, req)
		}
		if extReg := m.ExternalToolsRegistry(); extReg != nil && extReg.Has(tool) {
			return m.executeExternal(ctx, req)
		}
		return ToolResult{
			Tool:    tool,
			Summary: "Tool request rejected",
			Error:   fmt.Sprintf("unsupported tool %q", tool),
		}
	}
}

// executeExternal dispatches a project-defined external tool registered
// via SetExternalToolsRegistry. The registered command is invoked via
// exec.Command (no shell expansion); each Args entry is template-rendered
// against req.Args before invocation. See external_tool.go for the
// template syntax.
func (m *Manager) executeExternal(ctx context.Context, req ToolRequest) ToolResult {
	registry := m.ExternalToolsRegistry()
	if registry == nil {
		return ToolResult{Tool: req.Tool, Summary: "External tools unavailable", Error: "external tools registry not configured"}
	}
	if !registry.Has(req.Tool) {
		return ToolResult{Tool: req.Tool, Summary: "Unknown external tool", Error: "external tool not registered: " + req.Tool}
	}
	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	output, err := registry.Invoke(ctx, req.Tool, args, m.cfg.CWD)
	out := ToolResult{Tool: req.Tool, Summary: "External tool " + req.Tool, Output: output}
	if err != nil {
		out.Error = err.Error()
		if output == "" {
			out.Output = ""
		}
	}
	return out
}

// executeMCP dispatches a `mcp:<server>/<tool>` call through the
// attached registry. Args are passed verbatim — they were captured raw
// in parseToolCall so the upstream JSON-Schema parameters survive
// round-trip without forced-typing through ToolRequest's fields. The
// returned content is flattened: text-typed parts contribute their
// .text directly, anything else is re-encoded as JSON so the model
// still sees something sensible.
func (m *Manager) executeMCP(ctx context.Context, req ToolRequest) ToolResult {
	registry := m.MCPRegistry()
	if registry == nil {
		return ToolResult{Tool: req.Tool, Summary: "MCP unavailable", Error: "mcp registry not configured"}
	}
	if !registry.Has(req.Tool) {
		return ToolResult{Tool: req.Tool, Summary: "Unknown MCP tool", Error: "mcp tool not registered: " + req.Tool}
	}
	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	result, err := registry.Call(ctx, req.Tool, args)
	if err != nil {
		return ToolResult{Tool: req.Tool, Summary: "MCP call failed", Error: err.Error()}
	}
	output := joinMCPContent(result.Content)
	summary := "MCP " + req.Tool
	out := ToolResult{Tool: req.Tool, Summary: summary, Output: output}
	if result.IsError {
		out.Error = output
		out.Output = ""
		out.Summary = "MCP tool reported an error"
	}
	return out
}

// joinMCPContent flattens the [{type,text}] content array MCP servers
// return. Text entries contribute their .text verbatim, separated by
// newlines. Non-text entries are passed through as JSON so callers can
// still see their shape rather than silently dropping them.
func joinMCPContent(parts []json.RawMessage) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for i, raw := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		var probe struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &probe); err == nil && probe.Type == "text" {
			b.WriteString(probe.Text)
			continue
		}
		b.Write(raw)
	}
	return b.String()
}

const (
	PrecisionTierLSP         = "lsp"
	PrecisionTierSyntaxIndex = "syntax-index"
	PrecisionTierRGFallback  = "rg-fallback"
	PrecisionTierUnavailable = "unavailable"
)

func annotateLanguagePrecisionTier(result ToolResult) ToolResult {
	if strings.TrimSpace(result.PrecisionTier) != "" || !isLanguageToolName(result.Tool) {
		return result
	}
	result.PrecisionTier = inferLanguagePrecisionTier(result)
	return result
}

func inferLanguagePrecisionTier(result ToolResult) string {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output + "\n" + result.Error))
	if strings.TrimSpace(result.Error) != "" {
		return PrecisionTierUnavailable
	}
	if strings.Contains(text, "unavailable") ||
		strings.Contains(text, "not installed") ||
		strings.Contains(text, " is required") ||
		strings.Contains(text, "requires ") {
		return PrecisionTierUnavailable
	}
	if strings.Contains(text, "rg fallback") || strings.Contains(text, "semantic/rg fallback") {
		return PrecisionTierRGFallback
	}
	for _, token := range []string{
		"gopls",
		"clangd",
		"python lsp",
		"pyright",
		"pylsp",
		"typescript-language-server",
		"rust-analyzer",
		"jdtls",
	} {
		if strings.Contains(text, token) {
			return PrecisionTierLSP
		}
	}
	return PrecisionTierSyntaxIndex
}

func isLanguageToolName(tool string) bool {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if tool == "" {
		return false
	}
	for _, lang := range LanguageRegistry() {
		for _, candidate := range lang.Tools {
			if strings.EqualFold(tool, strings.TrimSpace(candidate)) {
				return true
			}
		}
	}
	if _, ok := supplementalLanguageDiscoveryTools[tool]; ok {
		return true
	}
	return false
}

var supplementalLanguageDiscoveryTools = map[string]struct{}{
	"go_find_function": {},
	"go_find_type":     {},
	"go_find_const":    {},
	"go_find_package":  {},
	"go_extract_body":  {},
	"go_importers":     {},

	"cpp_declaration":  {},
	"cpp_extract_body": {},
	"cpp_includes":     {},
	"cpp_who_includes": {},
	"cpp_blast_radius": {},
	"cpp_dead_symbol":  {},
}

func (m *Manager) gitStatus(ctx context.Context, req ToolRequest) ToolResult {
	workdir, err := m.resolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: "git_status", Summary: "Failed to resolve workdir", Error: err.Error()}
	}
	output, execErr := m.runCommand(ctx, workdir, "git", "status", "--porcelain=v1", "--branch", "--untracked-files=normal")
	result := ToolResult{
		Tool:    "git_status",
		Summary: fmt.Sprintf("Read git status in %s", m.displayPath(workdir)),
		Output:  strings.TrimSpace(output),
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) gitDiff(ctx context.Context, req ToolRequest) ToolResult {
	workdir, err := m.resolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: "git_diff", Summary: "Failed to resolve workdir", Error: err.Error()}
	}
	args := []string{"diff", "--no-color"}
	if req.Path != "" {
		// Validate the path resolves under the workspace before passing to git.
		if _, err := m.resolvePath(req.Path, req.Workdir); err != nil {
			return ToolResult{Tool: "git_diff", Summary: "Failed to resolve path", Error: err.Error()}
		}
		args = append(args, "--", req.Path)
	}
	output, execErr := m.runCommand(ctx, workdir, "git", args...)
	scope := "working tree"
	if req.Path != "" {
		scope = req.Path
	}
	result := ToolResult{
		Tool:    "git_diff",
		Summary: fmt.Sprintf("Read git diff for %s in %s", scope, m.displayPath(workdir)),
		Output:  strings.TrimSpace(output),
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
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
		if repaired, ok := inferMissingToolRequest(req); ok {
			return repaired, payload, true, nil
		}
		return ToolRequest{}, payload, true, fmt.Errorf("tool request is missing tool")
	}
	return req, payload, true, nil
}

func inferMissingToolRequest(req ToolRequest) (ToolRequest, bool) {
	if strings.TrimSpace(req.Tool) != "" {
		return req, true
	}

	switch {
	case len(req.Todos) > 0 || len(req.Items) > 0:
		req.Tool = "task_ledger"
	case strings.TrimSpace(req.Prompt) != "" || strings.TrimSpace(req.Agent) != "":
		req.Tool = "task"
	case strings.TrimSpace(req.Command) != "":
		req.Tool = "shell"
	case strings.TrimSpace(req.Destination) != "" || len(req.Declarations) > 0 || len(req.Moves) > 0 || strings.HasPrefix(strings.TrimSpace(req.Pattern), "tag:"):
		req.Tool = "go_move_declarations"
	case strings.TrimSpace(req.OldText) != "" || strings.TrimSpace(req.NewText) != "" || (strings.TrimSpace(req.Content) != "" && (req.StartLine != 0 || req.EndLine != 0)):
		req.Tool = "edit_file"
	case strings.TrimSpace(req.Glob) != "" && strings.TrimSpace(req.Pattern) != "":
		req.Tool = "search_content"
	case strings.TrimSpace(req.ID) != "" || req.StartLine != 0 || req.EndLine != 0:
		req.Tool = "read_file"
	case strings.HasSuffix(strings.TrimSpace(req.Path), ".go") && strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Content) == "":
		req.Tool = "go_file_outline"
	default:
		return ToolRequest{}, false
	}

	return req, true
}

func FormatToolResultForModel(result ToolResult) string {
	var b strings.Builder
	b.WriteString("[workspace tool result]\n")
	b.WriteString("tool: " + result.Tool + "\n")
	b.WriteString("summary: " + result.Summary + "\n")
	if strings.TrimSpace(result.PrecisionTier) != "" {
		b.WriteString("precision_tier: " + result.PrecisionTier + "\n")
	}
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
	if strings.TrimSpace(result.PrecisionTier) != "" {
		b.WriteString(" | precision_tier=" + result.PrecisionTier)
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
		"discovery_query",
		"structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_importers", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_diagnostics",
		"cpp_overview", "cpp_entrypoints", "cpp_find_symbol", "cpp_read_symbol", "cpp_related_files",
		"cpp_definition", "cpp_declaration", "cpp_implementations", "cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics",
		"python_overview", "python_entrypoints", "python_find_symbol", "python_read_symbol", "python_related_files",
		"python_definition", "python_references", "python_implementations", "python_calls_from", "python_calls_into",
		"python_workspace_symbol", "python_diagnostics", "python_file_outline",
		"ts_overview", "ts_entrypoints", "ts_find_symbol", "ts_read_symbol", "ts_related_files",
		"ts_definition", "ts_implementations", "ts_references", "ts_calls_from", "ts_calls_into",
		"ts_workspace_symbol", "ts_diagnostics", "ts_file_outline",
		"rust_overview", "rust_entrypoints", "rust_find_symbol", "rust_read_symbol", "rust_related_files",
		"rust_definition", "rust_implementations", "rust_references", "rust_calls_from", "rust_calls_into",
		"rust_workspace_symbol", "rust_diagnostics", "rust_file_outline",
		"java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol", "java_related_files",
		"java_definition", "java_references", "java_implementations", "java_calls_from", "java_calls_into",
		"java_workspace_symbol", "java_diagnostics", "java_file_outline":
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
		"discovery_query",
		"structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_importers", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations", "go_calls_from", "go_calls_into", "go_extract_body", "go_diagnostics",
		"cpp_overview", "cpp_entrypoints", "cpp_find_symbol", "cpp_read_symbol", "cpp_related_files",
		"cpp_definition", "cpp_declaration", "cpp_implementations", "cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics",
		"python_overview", "python_entrypoints", "python_find_symbol", "python_read_symbol", "python_related_files",
		"python_definition", "python_references", "python_implementations", "python_calls_from", "python_calls_into",
		"python_workspace_symbol", "python_diagnostics", "python_file_outline",
		"ts_overview", "ts_entrypoints", "ts_find_symbol", "ts_read_symbol", "ts_related_files",
		"ts_definition", "ts_implementations", "ts_references", "ts_calls_from", "ts_calls_into",
		"ts_workspace_symbol", "ts_diagnostics", "ts_file_outline",
		"rust_overview", "rust_entrypoints", "rust_find_symbol", "rust_read_symbol", "rust_related_files",
		"rust_definition", "rust_implementations", "rust_references", "rust_calls_from", "rust_calls_into",
		"rust_workspace_symbol", "rust_diagnostics", "rust_file_outline",
		"java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol", "java_related_files",
		"java_definition", "java_references", "java_implementations", "java_calls_from", "java_calls_into",
		"java_workspace_symbol", "java_diagnostics", "java_file_outline":
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
		result := ToolResult{Tool: "list_files", Summary: "Failed to resolve path", Error: err.Error()}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}
		return result
	}

	info, err := os.Stat(base)
	if err != nil {
		errMsg := err.Error()
		if os.IsNotExist(err) && strings.TrimSpace(req.Path) != "" {
			errMsg = fmt.Sprintf("directory %q does not exist in this workspace. (os: %s)", req.Path, err.Error())
		}
		result := ToolResult{Tool: "list_files", Summary: "Failed to stat path", Error: errMsg}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}
		return result
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

	totalEntries := len(entries)
	displayEntries := entries
	capped := false
	if len(displayEntries) > listFilesDisplayCap {
		displayEntries = displayEntries[:listFilesDisplayCap]
		capped = true
	}
	formatted := make([]string, 0, len(displayEntries)+1)
	for _, entry := range displayEntries {
		formatted = append(formatted, m.formatListedEntry(entry))
	}
	if capped {
		formatted = append(formatted, fmt.Sprintf("... and %d more path(s) omitted.", totalEntries-listFilesDisplayCap))
	}

	summary := fmt.Sprintf("Listed %d path(s).", totalEntries)
	if capped {
		summary = fmt.Sprintf("Listed %d path(s) (showing first %d).", totalEntries, listFilesDisplayCap)
	}
	if m.IsExploreMode() {
		summary = fmt.Sprintf("Listed %d likely path(s), prioritized toward source files.", totalEntries)
		if capped {
			summary = fmt.Sprintf("Listed %d likely path(s), prioritized toward source files (showing first %d).", totalEntries, listFilesDisplayCap)
		}
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
	if filepath.Clean(path) == filepath.Clean(base) {
		return false
	}

	return discovery.ShouldSkipPath(base, path, isDir)
}

func (m *Manager) shouldSkipSearchEntry(base string, path string, isDir bool, includeLogDirs bool) bool {
	if includeLogDirs && isDir && filepath.Base(path) == "logs" {
		return false
	}
	if includeLogDirs && !isDir {
		rel, err := filepath.Rel(base, path)
		if err == nil && strings.HasPrefix(filepath.ToSlash(filepath.Clean(rel)), "logs/") {
			return discovery.ShouldSkipRelFile(rel)
		}
	}
	return m.shouldSkipListEntry(base, path, isDir)
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
	includeLogDirs := searchIntentTargetsLogs(req.Path, pattern, req.Glob)
	rootDepth := strings.Count(filepath.Clean(base), string(filepath.Separator))
	type searchFileMatch struct {
		path    string
		display string
		modTime time.Time
		size    int64
	}
	var matches []searchFileMatch

	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if m.shouldSkipSearchEntry(base, path, true, includeLogDirs) {
				return filepath.SkipDir
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if m.shouldSkipSearchEntry(base, path, false, includeLogDirs) {
			return nil
		}

		rel := strings.ToLower(m.displayPath(path))
		name := strings.ToLower(filepath.Base(path))
		if !matchesSearchFilePattern(rel, name, query) {
			return nil
		}

		match := searchFileMatch{
			path:    path,
			display: m.handleDisplay(path),
		}
		if info, err := d.Info(); err == nil {
			match.modTime = info.ModTime()
			match.size = info.Size()
		}
		matches = append(matches, match)
		if len(matches) >= m.cfg.MaxDirectoryEntries {
			return filepath.SkipAll
		}
		return nil
	})

	if len(matches) == 0 {
		// Zero-hit enrichment: the pattern didn't match any path, but
		// the model often invents filenames from concepts (e.g.
		// "task_tool.go" when the code lives in tools.go). Try three
		// shapes of suggestion in order: whole-pattern fuzzy match,
		// token-substring match on base names (so "task_tool" finds
		// tools.go via the "tool" token), and finally the generic
		// sibling-directory fallback inside findPathSuggestions. Cap
		// the final list at 5. Phrasing steers the model out of search
		// mode — these are paths to read, not patterns to re-search.
		suggestions := m.findPathSuggestions(pattern, 5)
		tokenHits := m.tokenMatchedPaths(pattern, 5)
		for _, p := range tokenHits {
			if len(suggestions) >= 5 {
				break
			}
			dup := false
			for _, existing := range suggestions {
				if existing == p {
					dup = true
					break
				}
			}
			if !dup {
				suggestions = append(suggestions, p)
			}
		}
		output := ""
		summary := fmt.Sprintf("No file matches for %q", pattern)
		if len(suggestions) > 0 {
			summary = fmt.Sprintf("No file matches for %q. Closest paths in this workspace: %s.", pattern, strings.Join(suggestions, ", "))
			output = "Closest real paths:\n" + strings.Join(suggestions, "\n")
		}
		// When the pattern looks like a symbol (bare identifier) and
		// the workspace has a language index, the model was likely
		// looking for a symbol, not a filename — search_files only
		// matches path text, so nothing was ever going to hit. Point
		// at the right tool. Mentioned alongside paths when both apply.
		if looksLikeBareIdentifier(pattern) {
			if symHint := m.symbolSearchSuggestion(pattern); symHint != "" {
				if output == "" {
					output = symHint
				} else {
					output = output + "\n\n" + symHint
				}
				summary += " The pattern also looks like a symbol name rather than a filename."
			}
		}
		return ToolResult{
			Tool:    "search_files",
			Summary: summary,
			Output:  output,
		}
	}

	if includeLogDirs {
		sort.SliceStable(matches, func(i, j int) bool {
			if !matches[i].modTime.Equal(matches[j].modTime) {
				return matches[i].modTime.After(matches[j].modTime)
			}
			return matches[i].display < matches[j].display
		})
	} else {
		sort.Slice(matches, func(i, j int) bool { return matches[i].display < matches[j].display })
	}
	displayLimit := len(matches)
	if includeLogDirs && displayLimit > 24 {
		displayLimit = 24
	}
	output := make([]string, 0, displayLimit+1)
	for _, match := range matches[:displayLimit] {
		if includeLogDirs && !match.modTime.IsZero() {
			output = append(output, fmt.Sprintf("%s (modified %s; size %s)", match.display, match.modTime.Format(time.RFC3339), formatBytes(match.size)))
			continue
		}
		output = append(output, match.display)
	}
	summary := fmt.Sprintf("Found %d matching file(s).", len(matches))
	if includeLogDirs {
		if displayLimit < len(matches) {
			output = append(output, fmt.Sprintf("... omitted %d older log-like match(es)", len(matches)-displayLimit))
			summary = fmt.Sprintf("Found %d matching log-like file(s), newest first with modified time and size (showing first %d).", len(matches), displayLimit)
		} else {
			summary = fmt.Sprintf("Found %d matching log-like file(s), newest first with modified time and size.", len(matches))
		}
	}
	return ToolResult{
		Tool:    "search_files",
		Summary: summary,
		Output:  strings.Join(output, "\n"),
	}
}

func (m *Manager) readFile(req ToolRequest) ToolResult {
	path, description, err := m.resolveReadTarget(req)
	if err != nil {
		errMsg := err.Error()
		// OpenCode-adoption fix D — when the requested path's parent
		// directory does not exist, say so explicitly instead of letting
		// the generic "path not found" repeat across sibling probes.
		// The Did-you-mean block already lists real siblings; this
		// additional framing tells the model it's probing under a
		// phantom directory.
		if strings.TrimSpace(req.Path) != "" {
			parent := filepath.Dir(strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/"))
			if parent != "." && parent != "/" && parent != "" {
				if parentAbs, parentErr := m.resolvePath(parent, req.Workdir); parentErr == nil {
					if _, statErr := os.Stat(parentAbs); os.IsNotExist(statErr) {
						errMsg = fmt.Sprintf("parent directory %q does not exist in this workspace, so %q cannot exist either. (inner: %s)", parent, req.Path, err.Error())
					}
				}
			}
		}
		result := ToolResult{Tool: "read_file", Summary: "Failed to resolve file target", Error: errMsg}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}
		return result
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// OpenCode-adoption fix D — when the parent directory doesn't exist,
		// say so explicitly instead of letting the bare "no such file"
		// repeat across sibling probes. The Did-you-mean output already
		// surfaces real siblings of the closest-existing ancestor.
		errMsg := err.Error() + ". Verify the path with search_files, a language-specific symbol tool, or search_content before retrying read_file."
		if os.IsNotExist(err) && strings.TrimSpace(req.Path) != "" {
			parent := filepath.Dir(strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/"))
			if parent != "." && parent != "/" && parent != "" {
				if parentAbs, parentErr := m.resolvePath(parent, req.Workdir); parentErr == nil {
					if _, statErr := os.Stat(parentAbs); os.IsNotExist(statErr) {
						errMsg = fmt.Sprintf("parent directory %q does not exist in this workspace, so %q cannot exist either. (os: %s)", parent, req.Path, err.Error())
					}
				}
			}
		}
		result := ToolResult{
			Tool:    "read_file",
			Summary: "Failed to read file",
			Error:   errMsg,
		}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}
		return result
	}
	logLike := isLogLikePath(m.displayPath(path))
	userRequestedRange := req.StartLine > 0 || req.EndLine > 0
	openEndedLogRange := logLike && userRequestedRange && req.EndLine <= 0
	if !userRequestedRange && logLike {
		if result, ok := m.readLogTail(path, description, defaultLogReadLineCap); ok {
			return result
		}
	}
	if len(data) > m.cfg.MaxFileBytes && !(logLike && userRequestedRange) {
		data = data[:m.cfg.MaxFileBytes]
	}

	lines := strings.Split(string(data), "\n")
	start := req.StartLine
	end := req.EndLine
	requestedStart := start
	requestedEnd := end
	if start <= 0 {
		start = 1
	}
	if logLike && start > len(lines) {
		start = len(lines) - defaultLogReadLineCap + 1
		if start < 1 {
			start = 1
		}
	}
	if openEndedLogRange && end <= 0 {
		end = start + defaultLogReadLineCap - 1
		if end > len(lines) {
			end = len(lines)
		}
	} else if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	start, end, clampedToFileBounds := clampRequestedLineRange(len(lines), start, end, requestedStart, requestedEnd)
	if start > end {
		return ToolResult{Tool: "read_file", Summary: "Invalid line range", Error: "start_line must be <= end_line"}
	}

	// Soft cap: when the caller did NOT specify a range and the file is
	// large, truncate to the first 400 lines and tell the model how to
	// continue. Prevents a read_file on runner.go (1720 lines) from
	// blowing most of the turn's context budget when an outline-first
	// approach would have been cheaper.
	truncated := false
	tailRead := false
	if !userRequestedRange && isLogLikePath(m.displayPath(path)) && len(lines) > defaultLogReadLineCap {
		start = len(lines) - defaultLogReadLineCap + 1
		if start < 1 {
			start = 1
		}
		end = len(lines)
		tailRead = true
		truncated = true
	} else if !userRequestedRange && end > defaultLargeReadLineCap && end-start+1 > defaultLargeReadLineCap {
		end = start + defaultLargeReadLineCap - 1
		if end > len(lines) {
			end = len(lines)
		}
		truncated = true
	} else if openEndedLogRange {
		truncated = true
	}

	var b strings.Builder
	for i := start - 1; i < end && i < len(lines); i++ {
		line := lines[i]
		if logLike {
			line = truncateSearchContentLine(line, defaultLogLineCharCap)
		}
		fmt.Fprintf(&b, "%d:%s\n", i+1, line)
	}

	m.MarkPathObserved(m.displayPath(path))
	summary := fmt.Sprintf("Read %s lines %d-%d", description, start, end)
	if tailRead {
		summary += fmt.Sprintf(" (file has %d lines total; defaulted to recent log tail; pass start_line/end_line for a different window)", len(lines))
	} else if openEndedLogRange {
		summary += fmt.Sprintf(" (file has %d lines total; open-ended log range capped to %d lines; pass an explicit end_line for a different bounded window)", len(lines), defaultLogReadLineCap)
	} else if clampedToFileBounds {
		summary += fmt.Sprintf(" (requested range %d-%d was clamped to file bounds; file has %d lines total)", displayRequestedLine(requestedStart, start), displayRequestedLine(requestedEnd, end), len(lines))
	} else if truncated {
		summary += fmt.Sprintf(" (file has %d lines total; output was capped; pass start_line/end_line for a different range)", len(lines))
	}
	return ToolResult{
		Tool:    "read_file",
		Summary: summary,
		Output:  strings.TrimRight(b.String(), "\n"),
	}
}

func (m *Manager) readLogTail(path string, description string, lineCap int) (ToolResult, bool) {
	if lineCap <= 0 {
		lineCap = defaultLogReadLineCap
	}
	lines, totalLines, err := readLastLines(path, lineCap)
	if err != nil || len(lines) == 0 {
		return ToolResult{}, false
	}
	start := totalLines - len(lines) + 1
	if start < 1 {
		start = 1
	}
	var b strings.Builder
	for i, line := range lines {
		line = truncateSearchContentLine(line, defaultLogLineCharCap)
		fmt.Fprintf(&b, "%d:%s\n", start+i, line)
	}
	m.MarkPathObserved(m.displayPath(path))
	return ToolResult{
		Tool:    "read_file",
		Summary: fmt.Sprintf("Read %s lines %d-%d (file has %d lines total; defaulted to recent log tail; pass start_line/end_line for a different window)", description, start, start+len(lines)-1, totalLines),
		Output:  strings.TrimRight(b.String(), "\n"),
	}, true
}

func readLastLines(path string, lineCap int) ([]string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil, 0, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > lineCap {
		lines = lines[len(lines)-lineCap:]
	}
	return lines, countFileLinesFromData(data), nil
}

func countFileLinesFromData(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	count := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		count++
	}
	if count == 0 {
		return 1
	}
	return count
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
		Summary: fmt.Sprintf("Found %d likely Go entrypoint file(s).", len(lines)),
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
		Summary: fmt.Sprintf("Found %d matching Go symbol(s).", len(lines)),
		Output:  strings.Join(lines, "\n"),
	}
}

func (m *Manager) goFindFunction(req ToolRequest) ToolResult {
	return m.goFindSymbolsByKind(req, "func", "go_find_function", "function")
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

	start, end := goReadSymbolWindow(match, req.StartLine, req.EndLine)
	summary := fmt.Sprintf("Read %s in %s lines %d-%d", goQualifiedSymbol(match), m.displayPath(abs), start, end)
	if req.StartLine == 0 && req.EndLine == 0 && match.EndLine > 0 && end < match.EndLine {
		summary = fmt.Sprintf("%s (truncated from lines %d-%d; use go_extract_body for the full body)", summary, match.StartLine, match.EndLine)
	}

	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Failed to read Go symbol snippet", Error: err.Error()}
	}
	return ToolResult{
		Tool:    "go_read_symbol",
		Summary: summary,
		Output:  snippet,
	}
}

// goReadSymbolWindow picks the line range to return for a go_read_symbol
// call. When the model passes an explicit StartLine/EndLine, honor that.
// Otherwise prefer the symbol's actual extent (match.StartLine / EndLine
// from the index) capped at goReadSymbolMaxLines so very large functions
// don't dump hundreds of lines into the model's context. Falls back to a
// window centered on match.Line only when the symbol has no recorded
// extent — that's the legacy behavior for index entries that pre-date
// extent tracking.
//
// Previous bug: this function used `match.Line - 12` to `match.Line + 20`
// regardless of where the symbol started. For a function declared at
// line 16 (with imports above), the read returned lines 4-36 — the
// imports plus the function signature, not the function body. Live
// run 20260425-161904 surfaced this when go_read_symbol on
// `Runner.runChildTask` returned imports while go_extract_body returned
// the actual 117-line function body. Fixed by reading from the symbol's
// real start line.
func goReadSymbolWindow(match semantic.SymbolMatch, requestedStart, requestedEnd int) (int, int) {
	if requestedStart > 0 && requestedEnd >= requestedStart {
		return requestedStart, requestedEnd
	}
	const goReadSymbolMaxLines = 120
	if match.StartLine > 0 {
		start := match.StartLine
		end := match.EndLine
		if end < start {
			end = start
		}
		if end == 0 {
			end = start + goReadSymbolMaxLines
		}
		if end-start+1 > goReadSymbolMaxLines {
			end = start + goReadSymbolMaxLines - 1
		}
		return start, end
	}
	start := match.Line - 12
	if start < 1 {
		start = 1
	}
	end := match.Line + 20
	return start, end
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
		Summary: fmt.Sprintf("Found %d related Go file(s).", len(lines)),
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
		formatted := m.formatGoplsWorkspaceSymbolOutput(output)
		result := ToolResult{
			Tool:    "go_workspace_symbol",
			Summary: "Resolved Go workspace symbols with gopls",
			Output:  formatted,
		}
		if err != nil {
			fallback := m.goFindSymbol(req)
			fallback.Tool = "go_workspace_symbol"
			if fallback.Error == "" {
				fallback.Summary = "Resolved Go workspace symbols via semantic fallback after gopls failure"
				return fallback
			}
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
	cacheRoot := filepath.Join(os.TempDir(), "idiom-gopls")
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
	if scope := strings.TrimSpace(req.Path); scope != "" && scope != "." {
		resolvedScope, err := m.resolvePath(scope, req.Workdir)
		if err != nil {
			return ToolResult{Tool: toolName, Summary: "Search scope not found", Output: formatPathSuggestions(m.findPathSuggestions(scope, 5))}
		}
		if _, err := os.Stat(resolvedScope); err != nil {
			result := ToolResult{Tool: toolName, Summary: "Search scope not found"}
			if !os.IsNotExist(err) {
				result.Summary = "Search scope unavailable"
				result.Error = err.Error()
				return result
			}
			result.Output = formatPathSuggestions(m.findPathSuggestions(scope, 5))
			return result
		}
	}

	backend, err := resolveContentSearchBackend()
	if err != nil {
		return ToolResult{
			Tool:    toolName,
			Summary: "Content search unavailable",
			Error:   err.Error(),
		}
	}
	args := backend.args(req, m.defaultSearchExcludeGlobs(req.Path, req.Pattern, req.Glob))

	stdout, stderr, err := m.runCommandDetailed(parent, workdir, backend.bin, args...)
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
		// Zero-hit enrichment. Common confused-mode: model pastes a
		// symbol name as a search_content pattern but the actual
		// definition lives in a file whose *name* contains that term
		// (e.g. searching content for "prompt_routing" in a repo whose
		// file is literally internal/app/prompt_routing.go). Offer
		// path suggestions that match the pattern as a name so the
		// model switches to a targeted read_file. Intentionally
		// conservative: we only surface name-match suggestions for
		// identifier-shaped patterns (letters/digits/_), avoiding
		// noise on full regexes where the pattern is not a name.
		summary := fmt.Sprintf("No content matches for %q", pattern)
		output := ""
		if looksLikeBareIdentifier(pattern) {
			suggestions := m.findPathSuggestions(pattern, 5)
			if len(suggestions) > 0 {
				summary = fmt.Sprintf("No content matches for %q, but these paths contain it as part of their name — the symbol may be defined there: %s.", pattern, strings.Join(suggestions, ", "))
				output = "Paths whose name contains the pattern:\n" + strings.Join(suggestions, "\n")
			}
		}
		return ToolResult{
			Tool:    toolName,
			Summary: summary,
			Output:  output,
		}
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// Hard display cap. Logs show 100+ hits routinely in monorepos, and
	// the model re-ingests the full result on every subsequent turn.
	// Capping the rendered view at 50 lets the model scan the top hits,
	// see the total, and narrow on demand.
	const searchContentDisplayCap = 50
	type searchContentMatch struct {
		formatted string
		display   string
		line      int
		termScore int
		pathScore int
	}
	matches := make([]searchContentMatch, 0, len(lines))
	singlePath := ""
	singleLine := 0
	totalMatches := 0
	rankingTerms := searchPatternRankingTerms(pattern)
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}

		totalMatches++

		absPath := filepath.Clean(filepath.Join(workdir, parts[0]))
		handle := m.rememberPath(absPath)
		display := m.displayPath(absPath)
		lineNumber, _ := strconv.Atoi(parts[1])
		matches = append(matches, searchContentMatch{
			formatted: fmt.Sprintf("%s %s:%s:%s", handle, display, parts[1], truncateSearchContentLine(parts[2], searchContentLineCap)),
			display:   display,
			line:      lineNumber,
			termScore: searchContentTermScore(display, parts[2], rankingTerms),
			pathScore: searchContentPathPriority(display),
		})
		if len(lines) == 1 {
			if lineNumber > 0 {
				singlePath = display
				singleLine = lineNumber
			}
		}
		if totalMatches >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].termScore != matches[j].termScore {
			return matches[i].termScore > matches[j].termScore
		}
		if matches[i].pathScore != matches[j].pathScore {
			return matches[i].pathScore < matches[j].pathScore
		}
		if matches[i].display != matches[j].display {
			return matches[i].display < matches[j].display
		}
		return matches[i].line < matches[j].line
	})
	formatted := make([]string, 0, min(len(matches), searchContentDisplayCap)+1)
	capped := false
	for _, match := range matches {
		if len(formatted) >= searchContentDisplayCap {
			capped = true
			break
		}
		formatted = append(formatted, match.formatted)
	}
	if capped {
		remaining := totalMatches - searchContentDisplayCap
		formatted = append(formatted, fmt.Sprintf("... and %d more matching line(s) omitted.", remaining))
	}

	summary := fmt.Sprintf("Found %d matching line(s).", totalMatches)
	if capped {
		summary = fmt.Sprintf("Found %d matching line(s) (showing first %d).", totalMatches, searchContentDisplayCap)
	}
	if len(formatted) == 1 && singlePath != "" && singleLine > 0 {
		summary = fmt.Sprintf("Found 1 matching line at %s:%d.", singlePath, singleLine)
		if snippet := m.searchContextSnippet(filepath.Join(m.cfg.Root, singlePath), singleLine, 2); snippet != "" {
			formatted = append(formatted, "context:\n"+snippet)
		}
	}

	return ToolResult{
		Tool:    toolName,
		Summary: summary,
		Output:  strings.Join(formatted, "\n"),
	}
}

func searchPatternRankingTerms(pattern string) []*regexp.Regexp {
	parts := strings.Split(pattern, "|")
	out := make([]*regexp.Regexp, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + part)
		if err == nil {
			out = append(out, re)
		}
	}
	return out
}

func searchContentTermScore(path string, line string, terms []*regexp.Regexp) int {
	score := 0
	target := path + "\n" + line
	for _, term := range terms {
		if term != nil && term.MatchString(target) {
			score++
		}
	}
	return score
}

func searchContentPathPriority(path string) int {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(lower, "_test.go"):
		return 3
	case strings.Contains(lower, "/testdata/"):
		return 4
	case strings.HasSuffix(lower, ".md") || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/"):
		return 5
	case strings.HasPrefix(lower, "internal/agent/"), strings.HasPrefix(lower, "internal/workspace/"), strings.HasPrefix(lower, "cmd/"):
		return 0
	case strings.HasPrefix(lower, "internal/"), strings.HasPrefix(lower, "pkg/"), strings.HasPrefix(lower, "src/"):
		return 1
	default:
		return 2
	}
}

func (m *Manager) defaultSearchExcludeGlobs(path string, pattern string, glob string) []string {
	if searchPathTargetsSkippedDir(path) {
		return nil
	}
	excludes := discovery.RipgrepExcludeGlobs()
	if searchIntentTargetsLogs(path, pattern, glob) {
		filtered := excludes[:0]
		for _, exclude := range excludes {
			if exclude == "!**/logs/**" {
				continue
			}
			filtered = append(filtered, exclude)
		}
		excludes = filtered
	}
	return excludes
}

func searchIntentTargetsLogs(path string, pattern string, glob string) bool {
	combined := strings.ToLower(strings.TrimSpace(path) + " " + strings.TrimSpace(pattern) + " " + strings.TrimSpace(glob))
	if strings.Contains(combined, "logs") || strings.Contains(combined, "log/") || strings.Contains(combined, ".log") || strings.Contains(combined, "jsonl") {
		return true
	}
	return false
}

func matchesSearchFilePattern(rel string, name string, query string) bool {
	if strings.Contains(rel, query) || strings.Contains(name, query) {
		return true
	}
	if strings.ContainsAny(query, "*?[") {
		if ok, err := path.Match(query, name); err == nil && ok {
			return true
		}
		if ok, err := path.Match(query, rel); err == nil && ok {
			return true
		}
		trimmed := strings.Trim(query, "*?")
		if trimmed != "" && (strings.Contains(rel, trimmed) || strings.Contains(name, trimmed)) {
			return true
		}
	}
	return false
}

func searchPathTargetsSkippedDir(path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if cleaned == "" || cleaned == "." {
		return false
	}
	first := strings.Trim(strings.Split(cleaned, "/")[0], "/")
	if first == "" || first == "." {
		return false
	}
	return discovery.RelTargetsNoisyDir(cleaned) || discovery.ShouldSkipRelFile(cleaned)
}

func truncateSearchContentLine(line string, max int) string {
	line = strings.TrimRight(line, "\r\n")
	if max <= 0 || len(line) <= max {
		return line
	}
	return line[:max] + "...[line truncated]"
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
		segment = truncateSearchContentLine(segment, searchContentLineCap)
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

// FileLineCount returns the number of lines in the file at the given
// path and true on success. Returns (0, false) if the path can't be
// resolved, stat'd, or read. Cheap enough to call from redirect
// decisions — used to skip the outline detour for tiny files where
// reading the whole range costs less than outline+read_symbol.
func (m *Manager) FileLineCount(path string) (int, bool) {
	if m == nil {
		return 0, false
	}
	resolved, err := m.resolvePath(strings.TrimSpace(path), "")
	if err != nil {
		return 0, false
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return 0, false
	}
	// Count newlines; add 1 unless the file is empty or ends with a
	// newline (the trailing newline doesn't introduce another line).
	if len(data) == 0 {
		return 0, true
	}
	count := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		count++
	}
	return count, true
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
	start, end, _ = clampRequestedLineRange(len(lines), start, end, start, end)
	if start > end {
		return "", fmt.Errorf("start_line must be <= end_line")
	}

	var b strings.Builder
	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func clampRequestedLineRange(totalLines int, start int, end int, requestedStart int, requestedEnd int) (int, int, bool) {
	if totalLines <= 0 {
		return start, end, false
	}
	clamped := false
	if end > totalLines {
		end = totalLines
		clamped = true
	}
	if start > totalLines {
		span := 0
		if requestedStart > 0 && requestedEnd >= requestedStart {
			span = requestedEnd - requestedStart
		} else if end >= start {
			span = end - start
		}
		start = totalLines - span
		if start < 1 {
			start = 1
		}
		clamped = true
	}
	if start > end {
		start = end
		clamped = true
	}
	return start, end, clamped
}

func displayRequestedLine(requested int, fallback int) int {
	if requested > 0 {
		return requested
	}
	return fallback
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

	updated := []byte(req.Content)
	if appendMode {
		current, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return ToolResult{Tool: toolName, Summary: "Failed to read existing file", Error: err.Error()}
		}
		updated = append(append([]byte(nil), current...), []byte(req.Content)...)
	}
	if rejected := m.rejectInvalidPlannedWrite(toolName, "Write request rejected", path, updated); rejected != nil {
		return *rejected
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
		if err := os.WriteFile(path, updated, 0o644); err != nil {
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
		result := ToolResult{Tool: "edit_file", Summary: "Failed to resolve path", Error: err.Error()}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}
		return result
	}
	// Fix 1 — read-before-edit invariant. The agent must have observed this
	// path during the session (via a successful read_file, search hit, or
	// listing) before edit_file will touch it. Prevents the hallucinated-path
	// retry loops documented in docs/opencode-fixes-design.md §1.
	if !m.bypassReadBeforeEdit && !m.HasObservedPath(m.displayPath(path)) && !m.HasObservedPath(req.Path) {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("edit_file requires you to have observed %s in this session before editing it.", m.displayPath(path)),
		}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		result := ToolResult{Tool: "edit_file", Summary: "Failed to read file", Error: err.Error()}
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}
		return result
	}

	current := string(content)
	if req.StartLine > 0 || req.EndLine > 0 {
		return m.editFileByLineRange(path, current, req)
	}
	if strings.TrimSpace(req.OldText) == "" {
		// The model's malformed shape is almost always one of three:
		//   (1) it passed `content` thinking edit_file replaces the
		//       whole file — that's what write_file does.
		//   (2) it forgot to include old_text with the match form.
		//   (3) it meant the range form but dropped start_line/end_line.
		// Give a targeted hint based on which fields were actually
		// provided, and name write_file explicitly for case (1) so
		// the model stops trying to coax edit_file into whole-file
		// replacement.
		display := m.displayPath(path)
		hasContent := strings.TrimSpace(req.Content) != ""
		var example string
		if hasContent {
			example = fmt.Sprintf(
				"edit_file needs either old_text (for an exact-match replace) or start_line+end_line (for a range replace). "+
					"You provided `content` with neither.\n"+
					"If you want to REPLACE THE WHOLE FILE, use write_file instead:\n"+
					"  {\"tool\":\"write_file\",\"path\":%q,\"content\":\"<full file contents>\"}\n"+
					"If you want a PARTIAL edit, pick one of these two edit_file shapes:\n"+
					"  (a) exact-match:  {\"tool\":\"edit_file\",\"path\":%q,\"old_text\":\"<substring present in file>\",\"new_text\":\"<replacement>\"}\n"+
					"  (b) line range:   {\"tool\":\"edit_file\",\"path\":%q,\"start_line\":<N>,\"end_line\":<M>,\"content\":\"<replacement lines>\"}\n"+
					"Do not re-submit the same call shape.",
				display, display, display,
			)
		} else {
			example = fmt.Sprintf(
				"edit_file requires old_text (and new_text) OR start_line/end_line (and content). You provided neither.\n"+
					"Valid shapes:\n"+
					"  (a) exact-match:  {\"tool\":\"edit_file\",\"path\":%q,\"old_text\":\"<substring present in file>\",\"new_text\":\"<replacement>\"}\n"+
					"  (b) line range:   {\"tool\":\"edit_file\",\"path\":%q,\"start_line\":<N>,\"end_line\":<M>,\"content\":\"<replacement lines>\"}",
				display, display,
			)
		}
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: example}
	}

	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text did not appear in %s. Copy the exact substring from the file (including whitespace) or switch to the start_line/end_line+content form.", m.displayPath(path)),
		}
	}
	if matchCount > 1 && !req.ReplaceAll {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text matched %d locations in %s. Either extend old_text with surrounding context so it matches exactly one place, or set replace_all=true.", matchCount, m.displayPath(path)),
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
	if rejected := m.rejectInvalidPlannedWrite("edit_file", "Edit request rejected", path, []byte(updated)); rejected != nil {
		return *rejected
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
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "start_line must be >= 1 (line numbers are 1-indexed)."}
	}
	if endLine == 0 {
		endLine = startLine
	}
	if endLine < startLine {
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: fmt.Sprintf("end_line (%d) must be >= start_line (%d). Swap them if you meant the other direction, or set end_line=start_line for a single-line edit.", endLine, startLine)}
	}

	// Empty content with a line range is the most common edit_file footgun.
	// The model often calls edit_file with a line range thinking "insert
	// after line N" — but the line-range form REPLACES those lines with
	// content, so empty content silently DELETES them. Reject this combo
	// with a directive pointing at the substring-anchored form.
	if req.Content == "" && req.NewText == "" {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error: fmt.Sprintf(
				"empty content with line range %d-%d would DELETE those lines from %s. "+
					"If you intended to INSERT new content, use the substring-anchored form instead: "+
					"set old_text to a unique existing snippet (no line numbers needed) and set new_text to "+
					"old_text + your additions. The substring form is safer because it cannot accidentally "+
					"delete content. If you genuinely intended deletion, set content explicitly to a single newline.",
				startLine, endLine, m.displayPath(path),
			),
		}
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
	if rejected := m.rejectInvalidPlannedWrite("edit_file", "Edit request rejected", path, []byte(updated)); rejected != nil {
		return *rejected
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

func (m *Manager) rejectInvalidPlannedWrite(toolName string, summary string, path string, contents []byte) *ToolResult {
	if m == nil {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, contents, parser.AllErrors); err != nil {
			return &ToolResult{
				Tool:    toolName,
				Summary: summary,
				Error:   fmt.Sprintf("preflight syntax validation failed for %s: %v", m.displayPath(path), err),
			}
		}
	case ".json":
		if !json.Valid(contents) {
			return &ToolResult{
				Tool:    toolName,
				Summary: summary,
				Error:   fmt.Sprintf("preflight JSON validation failed for %s", m.displayPath(path)),
			}
		}
	}
	return nil
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
			return "", "", fmt.Errorf("path %q was not found; use search_files, a language-specific symbol tool, or search_content and then read a returned handle", m.displayPath(path))
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

	if discovery.RelTargetsNoisyDir(rel) || discovery.ShouldSkipRelFile(rel) {
		return false
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
	if handle, ok := m.pathToHandle[path]; ok {
		m.mu.Unlock()
		return handle
	}

	handle := fmt.Sprintf("f%d", m.nextHandle)
	m.nextHandle++
	m.pathToHandle[path] = handle
	m.handleToPath[handle] = path
	m.mu.Unlock()
	// Fix 1: any path that passes through rememberPath has been confirmed by a
	// workspace tool (listing, search hit, outline, etc.). Mark it observed so
	// edit_file accepts it without a prior read_file.
	m.MarkPathObserved(m.displayPath(path))
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
		// Fix 1: paths confirmed to exist via list_files / search_files /
		// search_content / bootstrap priming count as "observed" for the
		// read-before-edit invariant. Reading the contents is not required
		// when the path's existence has been verified through another channel.
		m.MarkPathObserved(m.displayPath(resolved))
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

func (m *Manager) ResolveHandlePath(handle string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("workspace manager is nil")
	}
	return m.pathForHandle(handle)
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

	// Phase 1 — substring match on base name / relative path. This catches
	// the common "user typed a typo in the filename" case.
	var suggestions []string
	// Track everything we considered so phase 2 can compute edit distance
	// without re-walking the tree.
	type candidate struct {
		rel  string
		base string
	}
	var candidates []candidate
	scanned := 0
	scanCap := m.cfg.MaxDirectoryEntries * 10
	if scanCap <= 0 {
		scanCap = 4096
	}
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
		if m.shouldSkipListEntry(m.cfg.Root, path, false) {
			return nil
		}

		scanned++
		if scanned > scanCap {
			return filepath.SkipAll
		}

		rel := strings.ToLower(m.displayPath(path))
		base := strings.ToLower(filepath.Base(path))
		candidates = append(candidates, candidate{rel: rel, base: base})
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

	if len(suggestions) > 0 {
		sort.Strings(suggestions)
		return suggestions
	}

	// Phase 2 — Fix 2 (extended). Base-name substring found nothing, which
	// happens when the model fabricates both the directory AND the file name
	// (e.g. "internal/llm/llm.go" in a repo that has no "llm" anywhere).
	// Fall back to full-path edit distance against what we saw during the
	// walk. We prefer paths whose parent dir shares a prefix with the
	// query's parent dir so siblings of the missing package rise to the top.
	queryLower := strings.ToLower(strings.ReplaceAll(filepath.Clean(query), "\\", "/"))
	queryParent := strings.ToLower(filepath.Dir(queryLower))
	type scored struct {
		path     string
		distance int
		priority int
	}
	scoredList := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		parent := filepath.Dir(c.rel)
		priority := 0
		if queryParent != "" && strings.HasPrefix(parent, firstPathSegment(queryParent)+"/") {
			priority = -2
		} else if queryParent != "" && strings.HasPrefix(parent, firstPathSegment(queryParent)) {
			priority = -1
		}
		distance := levenshtein(queryLower, c.rel)
		scoredList = append(scoredList, scored{path: c.rel, distance: distance, priority: priority})
	}
	sort.Slice(scoredList, func(i, j int) bool {
		if scoredList[i].priority != scoredList[j].priority {
			return scoredList[i].priority < scoredList[j].priority
		}
		if scoredList[i].distance != scoredList[j].distance {
			return scoredList[i].distance < scoredList[j].distance
		}
		return scoredList[i].path < scoredList[j].path
	})
	// Guard against runaway noise: only return candidates whose distance is
	// small relative to the query length. Keeps "ls -la" suggestions from
	// polluting a request for "internal/llm/llm.go".
	maxDistance := len(queryLower) * 2 / 3
	if maxDistance < 6 {
		maxDistance = 6
	}
	for _, s := range scoredList {
		if len(suggestions) >= limit {
			break
		}
		if s.distance > maxDistance {
			break
		}
		suggestions = append(suggestions, s.path)
	}
	// Phase 3 — if the query's *parent* directory does not exist, also list
	// sibling directories of the closest real ancestor so the model sees
	// the real neighborhood rather than a totally empty "not found" error.
	if len(suggestions) < limit {
		for _, sibling := range m.siblingDirectoriesOfMissingAncestor(queryLower) {
			suggestions = append(suggestions, sibling)
			if len(suggestions) >= limit {
				break
			}
		}
	}
	return suggestions
}

// symbolSearchSuggestion returns a neutral note when a search_files
// zero-hit query looks more like a symbol name than a filename. It is
// descriptive rather than copy-paste-ready so evidence tools do not
// prescribe the next action directly.
func (m *Manager) symbolSearchSuggestion(pattern string) string {
	if m == nil {
		return ""
	}
	active := m.ActiveLanguages()
	if len(active) == 0 {
		return ""
	}
	var langs []string
	for _, lang := range active {
		tool := lang.Tool(CapWorkspaceSymbol)
		if tool == "" {
			tool = lang.Tool(CapFindSymbol)
		}
		if tool == "" {
			continue
		}
		langs = append(langs, lang.Name)
	}
	if len(langs) == 0 {
		return ""
	}
	sort.Strings(langs)
	return fmt.Sprintf("Pattern shape: bare identifier; this may refer to a symbol rather than a filename. Active language indexes: %s.", strings.Join(langs, ", "))
}

// tokenMatchedPaths splits a compound pattern like "task_tool" on
// common separators and returns up to `limit` workspace paths whose
// base name contains any of the tokens. Filters out trivial tokens
// (< 3 chars) to keep noise down. Used by the search_files zero-hit
// enrichment so a pattern like "task_tool" surfaces tools.go (via the
// "tool" token) when the whole-pattern fuzzy match comes up empty.
func (m *Manager) tokenMatchedPaths(pattern string, limit int) []string {
	if !m.Enabled() || limit <= 0 {
		return nil
	}
	tokens := splitIdentifierTokens(pattern)
	if len(tokens) == 0 {
		return nil
	}
	var out []string
	scanned := 0
	scanCap := m.cfg.MaxDirectoryEntries * 10
	if scanCap <= 0 {
		scanCap = 4096
	}
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
		if m.shouldSkipListEntry(m.cfg.Root, path, false) {
			return nil
		}
		scanned++
		if scanned > scanCap {
			return filepath.SkipAll
		}
		base := strings.ToLower(filepath.Base(path))
		for _, tok := range tokens {
			if strings.Contains(base, tok) {
				out = append(out, m.handleDisplay(path))
				if len(out) >= limit {
					return filepath.SkipAll
				}
				return nil
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// splitIdentifierTokens breaks "task_tool.go", "TaskTool", "task-tool",
// and similar compounds into their constituent tokens, lower-cased,
// skipping tokens under 3 chars.
func splitIdentifierTokens(pattern string) []string {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return nil
	}
	// Normalize separators first, then insert breaks before capitals
	// so CamelCase also splits.
	norm := make([]rune, 0, len(p)*2)
	for i, r := range p {
		switch r {
		case '_', '-', '.', '/', ':':
			norm = append(norm, ' ')
		default:
			if i > 0 && r >= 'A' && r <= 'Z' {
				norm = append(norm, ' ')
			}
			norm = append(norm, r)
		}
	}
	raw := strings.Fields(strings.ToLower(string(norm)))
	var out []string
	seen := make(map[string]bool)
	for _, t := range raw {
		if len(t) < 3 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// looksLikeBareIdentifier reports whether a pattern is a single
// identifier-shaped token (letters, digits, underscore, dot, slash,
// colon). Used by the content-search zero-hit path so we only surface
// filename suggestions for queries the model likely intends as a
// symbol — full regex queries would produce misleading suggestions.
func looksLikeBareIdentifier(pattern string) bool {
	p := strings.TrimSpace(pattern)
	if p == "" || len(p) < 2 {
		return false
	}
	for _, r := range p {
		switch {
		case r == '_' || r == '.' || r == '/' || r == ':' || r == '-':
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		default:
			return false
		}
	}
	return true
}

// defaultLargeReadLineCap bounds read_file when the caller did not ask for
// a specific line range. A small local model burns context quickly on
// full-file reads; 400 lines is roughly a screen or two and still large
// enough to see most real functions end-to-end. The cap is easily bypassed
// by passing explicit start_line/end_line in the request.
const defaultLargeReadLineCap = 400
const defaultLogReadLineCap = 80
const defaultLogLineCharCap = 240

func isLogLikePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(lower, "/logs/") ||
		strings.HasPrefix(lower, "logs/") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.Contains(filepath.Base(lower), "log")
}

func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

// formatPathSuggestions wraps a suggestion list into a consistent
// "Did you mean:" block the model can parse at a glance. Returns the empty
// string when there are no suggestions so the tool's Output stays blank
// rather than advertising an empty block.
func formatPathSuggestions(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Did you mean one of these (nearest matches from the workspace):\n")
	for _, s := range suggestions {
		b.WriteString("- ")
		b.WriteString(s)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// firstPathSegment returns the portion of path up to (but not including) the
// first "/". Used to rank sibling directories higher than random files.
func firstPathSegment(path string) string {
	if idx := strings.IndexByte(path, '/'); idx > 0 {
		return path[:idx]
	}
	return path
}

// siblingDirectoriesOfMissingAncestor returns human-readable directory hints
// ("internal/agent/ (5 files)") for the real directories that share the
// closest-existing ancestor with a missing path. Helps redirect a model that
// fabricated an entire directory (e.g. "internal/llm/") back toward the
// packages that actually exist.
func (m *Manager) siblingDirectoriesOfMissingAncestor(queryLower string) []string {
	if queryLower == "" {
		return nil
	}
	segments := strings.Split(strings.Trim(queryLower, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		ancestor := strings.Join(segments[:i], "/")
		ancestorAbs := m.cfg.Root
		if ancestor != "" {
			ancestorAbs = filepath.Join(m.cfg.Root, ancestor)
		}
		info, err := os.Stat(ancestorAbs)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(ancestorAbs)
		if err != nil {
			continue
		}
		hints := make([]string, 0, 6)
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			child := filepath.Join(ancestorAbs, e.Name())
			if m.shouldSkipListEntry(m.cfg.Root, child, true) {
				continue
			}
			rel := e.Name()
			if ancestor != "" {
				rel = ancestor + "/" + e.Name()
			}
			hints = append(hints, rel+"/")
			if len(hints) >= 6 {
				break
			}
		}
		return hints
	}
	return nil
}

// levenshtein computes the case-sensitive edit distance between a and b.
// Small stand-alone implementation — avoids adding a dependency.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}
	prev := make([]int, la+1)
	curr := make([]int, la+1)
	for i := 0; i <= la; i++ {
		prev[i] = i
	}
	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			curr[i] = minInt3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[la]
}

func minInt3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
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

