package workspace

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	goscan "go/scanner"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/util"
	"idiom/internal/repository/comprehension"
	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic/languagetools"
)

// exploreBlockedToolNames are write tools blocked in explore mode.
// The descriptions are used in the rejection message shown to the model.
var exploreBlockedToolNames = map[string]string{
	ToolNameGoMoveDeclarations: "moving declarations",
	ToolNameMkdir:              "creating directories",
	ToolNameEditFile:           "editing files",
	ToolNameWriteFile:          "writing files",
	ToolNameAppendFile:         "appending files",
	ToolNameApplyPatch:         "applying patches",
}

func exploreModeWriteBlocked(tool string, mode Mode) (string, bool) {
	if mode != ModeExplore {
		return "", false
	}

	desc, ok := exploreBlockedToolNames[tool]
	if !ok {
		return "", false
	}

	return "explore mode is read-only; leave explore mode before " + desc, true
}

// Manager coordinates workspace tool dispatch. It is intentionally being
// decomposed into smaller capability-specific types:
//
//	ToolSurface   — tool manifest, classification, and allowed-tools computation
//	                (see tool_class.go, tool_manifest.go, capabilities.go)
//	PathTracker   — file handle assignment, path observation, and handle resolution
//	LanguageTools — semantic navigation, language indexes, and LSP integration
//	ExternalTools — MCP registry and external tool registration
//	                (see externaltool/)
//	EditRecovery  — edit-recovery state tracking
//
// Tool policy and external-tool execution now live in focused subpackages.
// Future work should continue extracting the remaining subsystems once their
// Manager and ToolRequest coupling has been reduced.
type Manager struct {
	cfg config.Workspace

	mode                       Mode
	thoroughness               ExploreThoroughness
	writePolicy                WritePolicy
	bypassReadBeforeEdit       bool
	planThenExecutePlannerMode bool
	mu                         sync.Mutex
	nextHandle                 int
	pathToHandle               map[string]string
	handleToPath               map[string]string
	observedPaths              map[string]struct{}
	permissionRules            map[string][]ToolRule
	protectedWritePaths        map[string]struct{}
	mcpRegistry                *mcp.Registry
	externalToolsRegistry      *ExternalToolRegistry
	languageTools              *languagetools.Manager
	editRecoveryRequired       map[string]editRecoveryState
}

type editRecoveryState struct {
	line   int
	detail string
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

type listedEntry struct {
	path  string
	isDir bool
}

const searchContentLineCap = 240
const listFilesDisplayCap = 80

func NewManager(cfg config.Workspace) *Manager {
	cfg.Root = config.CanonicalizeExistingPath(cfg.Root)
	cfg.CWD = config.CanonicalizeExistingPath(cfg.CWD)

	m := &Manager{
		cfg:                  cfg,
		mode:                 ModeNormal,
		thoroughness:         ThoroughnessMedium,
		writePolicy:          WritePolicyGuarded,
		nextHandle:           1,
		pathToHandle:         make(map[string]string),
		handleToPath:         make(map[string]string),
		observedPaths:        make(map[string]struct{}),
		editRecoveryRequired: make(map[string]editRecoveryState),
	}

	m.languageTools = languagetools.NewManager(languageToolHost{manager: m})

	return m
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

// SetPlanThenExecutePlannerMode toggles the workspace into plan-then-
// execute planner mode. Activates the submit_plan tool surface and
// hides per-edit write tools (edit_file, write_file, append_file,
// apply_patch, delegate_edit, mkdir, go_move_declarations) from the
// manifest the runner advertises to the model. Off by default; the
// `idiom plan-execute` orchestrator turns it on for the planner phase
// only — the executor phase uses a fresh Manager that leaves it off.
func (m *Manager) SetPlanThenExecutePlannerMode(active bool) {
	m.planThenExecutePlannerMode = active
}

// PlanThenExecutePlannerMode reports whether SetPlanThenExecutePlannerMode
// has been called with true.
func (m *Manager) PlanThenExecutePlannerMode() bool {
	return m.planThenExecutePlannerMode
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

func (m *Manager) SessionID() string {
	if m == nil {
		return ""
	}

	return m.cfg.SessionID
}

func (m *Manager) SetSessionID(sessionID string) {
	if m == nil {
		return
	}

	m.cfg.SessionID = strings.TrimSpace(sessionID)
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

// AvailableExternalToolNames returns project external tools whose command
// can be resolved from the manager's configured workspace directory.
func (m *Manager) AvailableExternalToolNames() []string {
	if m == nil {
		return nil
	}

	registry := m.ExternalToolsRegistry()
	if registry == nil {
		return nil
	}

	return registry.AvailableNames(m.externalToolBaseDir())
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
			"- Do not invent file paths from memory. Resolve a path-discovery capability, discovered handle, or structured symbol/code navigation route before naming a new file.\n"+
			"- After an orientation call, narrow with structured language-aware routes or discovered handles first. Exact-text search and bounded reads are fallback capabilities for prose, config, or ranges a semantic probe cannot cover.\n"+
			"- Paths are relative to the working directory unless absolute.\n"+
			"- Prefer anchored edit capabilities for localized changes; full-body writes overwrite and are only safe when the active contract permits them.\n"+
			"%s"+
			"- shell runs with the same OS permissions as this process. Prefer the dedicated tools where one exists.\n"+
			"- After tool results, either answer directly or take one exact evidence step tied to a named gap.",
		m.cfg.Root,
		m.cfg.CWD,
		m.writePolicyInstructions(),
	)
}

func (m *Manager) writePolicyInstructions() string {
	switch m.WritePolicy() {
	case WritePolicyAuto:
		return "- Write policy is auto: file tools may create, rewrite, or append within the workspace when the task requires it. Still prefer precise anchored edits and avoid unnecessary rewrites.\n"
	default:
		return "- Write policy is guarded: prefer anchored edits for existing files. Avoid whole-file rewrites of existing files, avoid state-changing shell commands for edits, and only create/append/mkdir when the user clearly requested that broader write scope.\n"
	}
}

func (m *Manager) exploreInstructions() string {
	return fmt.Sprintf(
		"You are in EXPLORE mode — a READ-ONLY investigation session. Do not change files or system state.\n\n"+
			"Workspace root: %s\nWorking directory: %s\nThoroughness: %s\n\n"+
			"Rules:\n"+
			"- Start broad once, then narrow with structured language-aware routes or discovered handles first. Exact-text search and bounded reads are fallback capabilities for prose, config, or ranges a semantic probe cannot cover.\n"+
			"- Do not invent file paths. Resolve a path-discovery capability first.\n"+
			"- shell is limited to read-only utilities (ls, find, rg, grep, awk, sed, cat, head, tail, wc, file, pwd, git status/log/diff/show/grep).\n"+
			"- When you have enough evidence, answer directly. Continue only for one exact missing claim and one exact probe.",
		m.cfg.Root,
		m.cfg.CWD,
		m.ExploreThoroughness(),
	)
}

func (m *Manager) toolManifestTools(readOnly bool) []string {
	return ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger:             !readOnly,
		IncludeTask:                   true,
		IncludeWriteTools:             !readOnly,
		IncludePlanThenExecute:        m.planThenExecutePlannerMode && !readOnly,
		PlanThenExecutePlannerSurface: m.planThenExecutePlannerMode && !readOnly,
		IncludeGo:                     m.isGoWorkspace(),
		IncludeCpp:                    m.isCppWorkspace(),
		IncludePython:                 m.isPythonWorkspace(),
		IncludeTypescript:             m.isTsWorkspace(),
		IncludeRust:                   m.isRustWorkspace(),
		IncludeJava:                   m.isJavaWorkspace(),
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

		slices.Sort(names)

		tools = append(tools, names...)
	}

	if registry := m.ExternalToolsRegistry(); registry != nil {
		names := registry.AvailableNames(m.externalToolBaseDir())
		slices.Sort(names)

		tools = append(tools, names...)
	}

	return tools
}

func (m *Manager) externalToolBaseDir() string {
	if m == nil {
		return ""
	}

	if strings.TrimSpace(m.cfg.CWD) != "" {
		return m.cfg.CWD
	}

	return m.cfg.Root
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
	defs = appendExternalToolDefinitions(defs, m.ExternalToolsRegistry(), allowedSet, m.externalToolBaseDir())

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

	slices.Sort(names)

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
func appendExternalToolDefinitions(defs []ToolDefinition, extReg *ExternalToolRegistry, allowedSet map[string]struct{}, baseDir string) []ToolDefinition {
	if extReg == nil {
		return defs
	}

	extDefs := extReg.DefinitionsForBaseDir(baseDir)
	slices.SortFunc(extDefs, func(a, b ToolDefinition) int { return cmp.Compare(a.Name, b.Name) })

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

// Execute dispatches a tool request and wraps the dispatch in a per-tool
// wall-clock timeout so a misbehaving backend (e.g., a hung clangd query
// or runaway external tool) cannot stall the lane indefinitely. On
// timeout the runner sees a synthesized "tool timed out" result that
// flows through the normal failure-streak guard, and the runaway
// goroutine is left to drain in the background rather than blocking the
// loop.
func (m *Manager) Execute(ctx context.Context, req ToolRequest) (result ToolResult) {
	defer func() {
		result = annotateToolFailure(result)
		result = annotateLanguagePrecisionTier(result)
		result = annotateLanguageRelationships(result)
	}()

	if m.EvaluateConfiguredToolRule(req) == ToolRuleDeny {
		return m.toolRuleDeniedResult(req)
	}

	timeout := toolTimeoutForRequest(req)
	if timeout <= 0 {
		return m.executeDispatch(ctx, req)
	}

	return m.executeWithTimeout(ctx, req, timeout)
}

func (m *Manager) toolRuleDeniedResult(req ToolRequest) ToolResult {
	tool := strings.TrimSpace(req.Tool)
	target := extractToolRuleTarget(tool, req)

	if target == "" {
		target = "<request>"
	}

	return toolPermissionPolicyResult(tool, "Tool request denied by workspace tool_rules", fmt.Sprintf("workspace tool_rules deny %s on %s", tool, target))
}

// executeWithTimeout runs the tool dispatch in a goroutine and races it
// against a context deadline. If the dispatch returns first, its result
// is used; if the deadline trips first, a synthesized timeout result is
// returned and the goroutine is left to drain. Panics inside the
// dispatch goroutine are recovered into a "tool panicked" result so a
// single bad tool can never bring down the runner.
func (m *Manager) executeWithTimeout(ctx context.Context, req ToolRequest, timeout time.Duration) ToolResult {
	return m.executeWithTimeoutFn(ctx, req, timeout, func(timeoutCtx context.Context) ToolResult {
		return m.executeDispatch(timeoutCtx, req)
	})
}

// executeWithTimeoutFn is the testable core of executeWithTimeout. It
// runs fn in a goroutine, races it against the deadline derived from
// ctx + timeout, and synthesizes "Tool timed out" / "Tool canceled" /
// "Tool panicked" results for the bad-path cases.
func (m *Manager) executeWithTimeoutFn(ctx context.Context, req ToolRequest, timeout time.Duration, fn func(context.Context) ToolResult) ToolResult {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan ToolResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- ToolResult{
					Tool:    req.Tool,
					Summary: "Tool panicked",
					Error:   fmt.Sprintf("%s panicked while executing: %v", strings.TrimSpace(req.Tool), r),
					Failure: failures.New(failures.Internal, failures.NoReason, fmt.Sprintf("%s panicked while executing", strings.TrimSpace(req.Tool))),
				}
			}
		}()

		done <- fn(timeoutCtx)
	}()

	select {
	case res := <-done:
		return res
	case <-timeoutCtx.Done():
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return ToolResult{
				Tool:    req.Tool,
				Summary: "Tool timed out",
				Error:   fmt.Sprintf("%s did not return within %s; the runner is moving on. Treat this as a tool stall and pick a different approach (e.g., a different tool, narrower input, or a per-path blocker).", strings.TrimSpace(req.Tool), timeout),
				Failure: failures.New(failures.DeadlineExceeded, failures.NoReason, fmt.Sprintf("%s did not return within %s", strings.TrimSpace(req.Tool), timeout)),
			}
		}

		return ToolResult{
			Tool:    req.Tool,
			Summary: "Tool canceled",
			Error:   "the run was canceled before this tool returned",
			Failure: failures.New(failures.Canceled, failures.NoReason, "the run was canceled before this tool returned"),
		}
	}
}

// toolTimeoutFor returns the per-tool wall-clock budget used by
// executeWithTimeout. Heavy index/scan tools get a larger budget; the
// rest use a conservative 30s default. Returning 0 disables the
// timeout for tools that legitimately need to stream long-running
// output (none today).
func toolTimeoutForRequest(req ToolRequest) time.Duration {
	tool := strings.TrimSpace(req.Tool)
	if tool == "context_status" && req.Refresh {
		return toolTimeoutFor("context_index")
	}

	return toolTimeoutFor(tool)
}

func toolTimeoutFor(tool string) time.Duration {
	spec, ok := builtInToolSurfaceMetadata(tool)
	if !ok {
		return 30 * time.Second
	}

	// shell has no outer timeout — the shell handler enforces its own
	// CommandTimeoutSeconds. Skip the outer timeout to avoid double-bounding.
	if tool == "shell" {
		return 0
	}

	if spec.Timeout > 0 {
		return spec.Timeout
	}

	return 30 * time.Second
}

func (m *Manager) executeDispatch(ctx context.Context, req ToolRequest) (result ToolResult) {
	tool := strings.TrimSpace(req.Tool)
	mode := m.Mode()

	if isLanguageToolName(tool) {
		return m.executeLanguageTool(ctx, req)
	}

	switch tool {
	case ToolNamePwd:
		return ToolResult{
			Tool:    tool,
			Summary: "Printed current working directory",
			Output:  m.cfg.CWD,
		}
	case ToolNameListFiles:
		return m.listFiles(req)
	case ToolNameSearchFiles:
		return m.searchFiles(req)
	case ToolNameSearchContent, ToolNameRg:
		return m.searchContent(ctx, req, tool)
	case ToolNameReadFile:
		return m.readFile(req)
	case ToolNameFileStats:
		return m.fileStats(req)
	case ToolNameCodeNavigate:
		return m.executeLanguageTool(ctx, req)
	case ToolNameReadOnlyCapability:
		return m.readOnlyCapability(ctx, req)
	case ToolNameSourceSanity:
		return m.sourceSanity(ctx, req)
	case ToolNameHarnessAudit:
		return m.harnessAudit(req)
	case ToolNameHarnessFeedback:
		return m.harnessFeedback(req)
	case ToolNameRecoveryInspect:
		return m.recoveryInspect(ctx, req)
	case ToolNameRecoveryStatus:
		return m.recoveryStatus(ctx, req)
	case ToolNameRecoveryDecompose:
		return m.recoveryDecompose(ctx, req)
	case ToolNameProductEfforts:
		return m.productEfforts(req)
	case ToolNameProductStatus:
		return m.productStatus(req)
	case ToolNameSubmitPlan:
		return m.submitPlan(req)
	case ToolNameContextIndex:
		return m.contextIndex(ctx, req)
	case ToolNameContextStatus:
		return m.contextStatus(ctx, req)
	case ToolNameContextQuery:
		return m.contextQuery(ctx, req)
	case ToolNameContextZoom:
		return m.contextZoom(ctx, req)
	case ToolNameContextPack:
		return m.contextPack(ctx, req)
	case ToolNameContextImpact:
		return m.contextImpact(ctx, req)
	case ToolNameContextTracePack:
		return m.contextTracePack(ctx, req)
	case ToolNameDiscoveryQuery:
		return m.discoveryQuery(ctx, req)
	case ToolNameStructuralSearch:
		return m.structuralSearch(ctx, req)
	case ToolNameDependencyGraph:
		return m.dependencyGraph(ctx, req)
	case ToolNameHistoryRange:
		return m.historyRange(ctx, req)
	case ToolNameHistorySymbol:
		return m.historySymbol(ctx, req)
	case ToolNameBlameRange:
		return m.blameRange(ctx, req)
	case ToolNameCtagsSymbols:
		return m.ctagsSymbols(ctx, req)
	case ToolNameSemgrepScan:
		return m.semgrepScan(ctx, req)
	case ToolNameJoernScan:
		return m.joernScan(ctx, req)
	case ToolNameGoMoveDeclarations:
		if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
			return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
		}

		return m.executeLanguageTool(ctx, req)
	case ToolNameMkdir:
		if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
			return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
		}

		return m.makeDir(req)
	case ToolNameEditFile:
		if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
			return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
		}

		return m.editFile(req)
	case ToolNameWriteFile:
		if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
			return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
		}

		return m.writeFile(req, false)
	case ToolNameAppendFile:
		if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
			return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
		}

		return m.writeFile(req, true)
	case ToolNameApplyPatch:
		if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
			return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
		}

		return m.applyPatch(req)
	case ToolNameShell:
		return m.runShell(ctx, req)
	case ToolNameGitStatus:
		return m.gitStatus(ctx, req)
	case ToolNameGitDiff:
		return m.gitDiff(ctx, req)
	default:
		if strings.HasPrefix(tool, "mcp:") {
			return m.executeMCP(ctx, req)
		}

		if extReg := m.ExternalToolsRegistry(); extReg != nil && extReg.Has(tool) {
			return m.executeExternal(ctx, req)
		}

		// Generic recovery message: list nearby-named tools so the
		// model can self-correct after a typo or hallucinated name.
		// Helpful across any project — the suggestion list is built
		// from the live tool surface (built-ins + external + MCP).
		return toolUnsupportedResult(tool, "Tool request rejected", m.unsupportedToolError(tool))
	}
}

// unsupportedToolError returns a model-actionable error for an
// unknown tool name. Lists up to N nearby-named tools (Levenshtein
// or substring) so the model can self-correct without further
// orchestrator intervention. Generic — the suggestion list is built
// from the live tool surface (built-ins + external + MCP), no
// project-specific assumptions.
func (m *Manager) unsupportedToolError(tool string) string {
	candidates := m.knownToolNames()
	suggestions := closestToolNames(tool, candidates, 5)

	if len(suggestions) == 0 {
		return fmt.Sprintf(
			"unsupported tool %q. No registered tools match this name; check the runtime brief's allowed_tools list.",
			tool,
		)
	}

	quoted := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		quoted = append(quoted, fmt.Sprintf("%q", s))
	}

	return fmt.Sprintf(
		"unsupported tool %q. Did you mean one of: %s? If none match, check the runtime brief's allowed_tools list.",
		tool, strings.Join(quoted, ", "),
	)
}

// knownToolNames returns the union of built-in, external, and MCP
// tool names known to this manager. Used by error messages to
// suggest corrections.
func (m *Manager) knownToolNames() []string {
	names := append([]string(nil), builtinToolNamesForSuggest()...)
	if reg := m.ExternalToolsRegistry(); reg != nil {
		names = append(names, reg.AvailableNames(m.externalToolBaseDir())...)
	}

	if mcpReg := m.MCPRegistry(); mcpReg != nil {
		for name := range mcpReg.Tools() {
			names = append(names, name)
		}
	}

	return names
}

// builtinToolNamesForSuggest reuses the manifest inventory so unsupported-tool
// recovery does not drift away from the exported built-in surface.
func builtinToolNamesForSuggest() []string {
	return toolManifestToolsAcrossBuiltInSurfaces()
}

// closestToolNames returns up to maxN candidates closest to target by
// case-insensitive prefix match first, then substring containment,
// then short Levenshtein distance. Generic string-similarity — no
// project-specific tool naming assumptions.
func closestToolNames(target string, candidates []string, maxN int) []string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" || len(candidates) == 0 {
		return nil
	}

	type scored struct {
		name string
		rank int
	}

	scoredList := make([]scored, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))

	for _, c := range candidates {
		if _, dup := seen[c]; dup {
			continue
		}

		seen[c] = struct{}{}

		lc := strings.ToLower(c)

		switch {
		case lc == target:
			scoredList = append(scoredList, scored{c, 0})
		case strings.HasPrefix(lc, target) || strings.HasPrefix(target, lc):
			scoredList = append(scoredList, scored{c, 1})
		case strings.Contains(lc, target) || strings.Contains(target, lc):
			scoredList = append(scoredList, scored{c, 2})
		default:
			d := levenshteinDistance(target, lc)
			// Only suggest if edit distance is reasonable for the
			// target length — avoids spurious "did you mean ls?" on a
			// long fabricated name.
			cap := len(target) / 2
			if cap < 3 {
				cap = 3
			}

			if d <= cap {
				scoredList = append(scoredList, scored{c, 10 + d})
			}
		}
	}

	sort.SliceStable(scoredList, func(i, j int) bool {
		return scoredList[i].rank < scoredList[j].rank
	})

	out := make([]string, 0, maxN)
	for _, s := range scoredList {
		out = append(out, s.name)
		if len(out) >= maxN {
			break
		}
	}

	return out
}

// levenshteinDistance — standard iterative two-row implementation,
// case-insensitive (callers pre-lowercase). Used by closestToolNames.
func levenshteinDistance(a, b string) int {
	if a == b {
		return 0
	}

	if len(a) == 0 {
		return len(b)
	}

	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i

		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}

		prev, curr = curr, prev
	}

	return prev[len(b)]
}

// executeExternal dispatches a project-defined external tool registered
// via SetExternalToolsRegistry. The registered command is invoked via
// exec.Command (no shell expansion); each Args entry is template-rendered
// against req.Args before invocation. See external_tool.go for the
// template syntax.
func (m *Manager) executeExternal(ctx context.Context, req ToolRequest) ToolResult {
	registry := m.ExternalToolsRegistry()
	if registry == nil {
		return toolUnavailableResult(req.Tool, "External tools unavailable", "external tools registry not configured")
	}

	if !registry.Has(req.Tool) {
		return toolUnsupportedResult(req.Tool, "Unknown external tool", "external tool not registered: "+req.Tool)
	}

	if ok, reason := registry.Availability(req.Tool, m.externalToolBaseDir()); !ok {
		return toolUnavailableResult(req.Tool, "External tool unavailable", "external tool "+req.Tool+" unavailable: "+reason)
	}

	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	output, err := registry.Invoke(ctx, req.Tool, args, m.cfg.CWD)
	out := ToolResult{Tool: req.Tool, Summary: "External tool " + req.Tool, Output: output}

	if err != nil {
		out.Error = err.Error()
		out.Failure = failures.Wrap(failures.Internal, reasonToolInternalFailure, "external tool failed", err)

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
		return toolUnavailableResult(req.Tool, "MCP unavailable", "mcp registry not configured")
	}

	if !registry.Has(req.Tool) {
		return toolUnsupportedResult(req.Tool, "Unknown MCP tool", "mcp tool not registered: "+req.Tool)
	}

	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	result, err := registry.Call(ctx, req.Tool, args)
	if err != nil {
		return toolFailureFromMaybeTypedErrorResult(req.Tool, "MCP call failed", err, failures.Internal, reasonToolInternalFailure)
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
	diffPath := ""

	if req.Path != "" {
		// Validate the path resolves under the workspace before passing to git.
		resolved, err := m.resolvePath(req.Path, req.Workdir)
		if err != nil {
			return ToolResult{Tool: "git_diff", Summary: "Failed to resolve path", Error: err.Error()}
		}

		diffPath = resolved

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
	} else if diffPath != "" {
		m.clearEditRecovery(diffPath)
	} else {
		m.clearAllEditRecovery()
	}

	return result
}

func ParseToolRequest(text string) (ToolRequest, string, bool, error) {
	payload, ok := extractToolPayload(text)
	if !ok {
		payload, ok = extractTaggedJSONObjectToolPayload(text)
	}

	if !ok {
		var embeddedErr error

		payload, ok, embeddedErr = extractLineAnchoredToolJSONObjectPayload(text)

		if ok {
			if embeddedErr != nil {
				return ToolRequest{}, payload, true, embeddedErr
			}
		}
	}

	if !ok {
		var embeddedErr error

		payload, ok, embeddedErr = extractBareToolJSONObjectPayload(text)

		if ok {
			if embeddedErr != nil {
				return ToolRequest{}, payload, true, embeddedErr
			}
		}
	}

	if !ok {
		var err error

		payload, ok, err = extractQwenXMLToolPayload(text)

		if !ok && err == nil {
			payload, ok, err = extractDeepSeekXMLToolPayload(text)
		}

		if !ok {
			if candidate := strings.TrimSpace(text); strings.HasPrefix(candidate, "{") && strings.HasSuffix(candidate, "}") && strings.Contains(candidate, `"tool"`) {
				payload, ok = candidate, true
			} else {
				return ToolRequest{}, "", false, nil
			}
		}

		if err != nil {
			return ToolRequest{}, payload, true, err
		}
	}

	req, err := DecodeToolRequestJSON([]byte(payload))
	if err != nil {
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

func extractTaggedJSONObjectToolPayload(text string) (string, bool) {
	for _, tag := range []string{"tool", "tool_code"} {
		open := "<" + tag + ">"
		close := "</" + tag + ">"
		start := strings.Index(text, open)

		if start < 0 {
			continue
		}

		start += len(open)

		end := strings.Index(text[start:], close)

		if end >= 0 {
			end += start
		} else {
			end = len(text)
		}

		if object, ok := extractFirstJSONObject(text[start:end]); ok {
			return object, true
		}
	}

	return "", false
}

// DecodeToolRequestJSON decodes a built-in tool request payload. It accepts a
// small, safe repair surface for common model JSON-schema slips: typed fields
// emitted as decimal/boolean strings, stringified non-plan structured fields,
// and singleton values for array-only fields. Native tool-calls and fenced JSON
// requests both route through this helper so repair behavior stays consistent.
func DecodeToolRequestJSON(payload []byte) (ToolRequest, error) {
	if nested, ok, err := decodeNestedToolRequestJSON(payload); ok || err != nil {
		return nested, err
	}

	return decodeFlatToolRequestJSON(payload)
}

func decodeFlatToolRequestJSON(payload []byte) (ToolRequest, error) {
	var req ToolRequest

	stringifiedFields := stringifiedStructuredToolFields(payload)

	if _, ok := stringifiedFields["edits"]; ok {
		if repaired, ok := repairToolRequestJSONPayload(payload); ok {
			var repairedReq ToolRequest

			if repairErr := json.Unmarshal(repaired, &repairedReq); repairErr == nil && !toolRequestLostStringifiedStructuredField(repairedReq, stringifiedFields) {
				normalizeToolRequestAliases(&repairedReq)
				repairEmbeddedQwenXMLToolParameters(&repairedReq)

				return repairedReq, nil
			}
		}

		return ToolRequest{}, errors.New("json: cannot unmarshal string into Go struct field ToolRequest.edits of type []workspace.PlanEdit")
	}

	if err := json.Unmarshal(payload, &req); err != nil {
		if repaired, ok := repairToolRequestJSONPayload(payload); ok {
			if repairErr := json.Unmarshal(repaired, &req); repairErr == nil {
				if toolRequestLostStringifiedStructuredField(req, stringifiedFields) {
					return ToolRequest{}, err
				}

				normalizeToolRequestAliases(&req)
				repairEmbeddedQwenXMLToolParameters(&req)

				return req, nil
			}
		}

		return ToolRequest{}, err
	}

	if repaired, ok := repairToolRequestJSONPayload(payload); ok {
		var repairedReq ToolRequest

		if repairErr := json.Unmarshal(repaired, &repairedReq); repairErr == nil {
			if !toolRequestLostStringifiedStructuredField(repairedReq, stringifiedFields) {
				req = repairedReq
			}
		}
	}

	normalizeToolRequestAliases(&req)
	repairEmbeddedQwenXMLToolParameters(&req)

	return req, nil
}

func decodeNestedToolRequestJSON(payload []byte) (ToolRequest, bool, error) {
	var envelope struct {
		Tool    string          `json:"tool"`
		Request json.RawMessage `json:"request"`
		Input   json.RawMessage `json:"input"`
	}

	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ToolRequest{}, false, nil
	}

	outerTool := strings.TrimSpace(envelope.Tool)
	request := bytes.TrimSpace(envelope.Request)

	if len(request) == 0 {
		request = bytes.TrimSpace(envelope.Input)
	}

	if outerTool == "" || len(request) == 0 {
		return ToolRequest{}, false, nil
	}

	if len(request) > 0 && request[0] == '"' {
		var encoded string

		if err := json.Unmarshal(request, &encoded); err != nil {
			return ToolRequest{}, true, fmt.Errorf("decode nested request: %w", err)
		}

		request = []byte(strings.TrimSpace(encoded))
	}

	if len(request) == 0 || request[0] != '{' {
		return ToolRequest{}, false, nil
	}

	nested, err := decodeFlatToolRequestJSON(request)
	if err != nil {
		return ToolRequest{}, true, fmt.Errorf("decode nested request: %w", err)
	}

	if strings.TrimSpace(nested.Tool) == "" {
		nested.Tool = outerTool
	} else if outerTool == CodeNavigateToolName && codeNavigateCapabilityAlias(nested.Tool) != "" {
		if strings.TrimSpace(nested.Capability) == "" {
			nested.Capability = codeNavigateCapabilityAlias(nested.Tool)
		}

		nested.Tool = outerTool
	} else if strings.TrimSpace(nested.Tool) != outerTool {
		return ToolRequest{}, true, fmt.Errorf("nested request tool %q does not match envelope tool %q", nested.Tool, outerTool)
	}

	normalizeToolRequestAliases(&nested)
	repairEmbeddedQwenXMLToolParameters(&nested)

	return nested, true, nil
}

func codeNavigateCapabilityAlias(tool string) string {
	return CodeNavigateCapabilityAlias(tool)
}

func CodeNavigateCapabilityAlias(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}

	spec, err := languagetools.CodeNavigationSpecFor(value)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(spec.Name)
}

func CodeNavigateResolutionCapabilityAlias(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || ContextGraphResolutionOnly(value) {
		return ""
	}

	return CodeNavigateCapabilityAlias(value)
}

func ContextGraphResolutionOnly(value string) bool {
	key := strings.ToLower(strings.TrimSpace(value))

	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")

	switch key {
	case "repository", "package", "file", "symbol", "summary", "any":
		return true
	default:
		return false
	}
}

var stringifiedJSONToolFieldNames = []string{
	"edits",
	"evidence",
	"required_tests",
	"required_edit_paths",
	"forbidden_paths",
	"source_satisfied_paths",
	"declarations",
	"moves",
	"todos",
	"items",
	"plan_artifact",
}

var stringArrayToolFieldNames = []string{
	"evidence",
	"required_tests",
	"required_edit_paths",
	"source_satisfied_paths",
	"forbidden_paths",
	"declarations",
}

var singletonArrayToolFieldNames = []string{
	"edits",
	"moves",
	"todos",
	"items",
}

func stringifiedJSONToolFieldAllowedStarts(field string) string {
	switch field {
	case "plan_artifact":
		return "{"
	default:
		if stringifiedJSONToolFieldIsArray(field) {
			return "[{"
		}

		return ""
	}
}

func stringifiedJSONToolFieldIsArray(field string) bool {
	return slices.Contains(stringifiedJSONToolFieldNames, field) && field != "plan_artifact"
}

func stringifiedStructuredToolFields(payload []byte) map[string]string {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil
	}

	out := make(map[string]string)

	for _, field := range stringifiedJSONToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		var encoded string

		if err := json.Unmarshal(raw, &encoded); err == nil && strings.TrimSpace(encoded) != "" {
			out[field] = strings.TrimSpace(encoded)
		}
	}

	return out
}

func toolRequestLostStringifiedStructuredField(req ToolRequest, fields map[string]string) bool {
	if len(fields) == 0 {
		return false
	}

	for field, value := range fields {
		if decodedStructuredToolFieldEmpty(req, field) && !stringifiedStructuredFieldRepresentsEmptyValue(field, value) {
			return true
		}
	}

	return false
}

func decodedStructuredToolFieldEmpty(req ToolRequest, field string) bool {
	switch field {
	case "edits":
		return len(req.Edits) == 0
	case "required_tests":
		return len(req.RequiredTests) == 0
	case "required_edit_paths":
		return len(req.RequiredEditPaths) == 0
	case "source_satisfied_paths":
		return len(req.SourceSatisfiedPaths) == 0
	case "forbidden_paths":
		return len(req.ForbiddenPaths) == 0
	case "declarations":
		return len(req.Declarations) == 0
	case "moves":
		return len(req.Moves) == 0
	case "todos":
		return len(req.Todos) == 0
	case "items":
		return len(req.Items) == 0
	case "plan_artifact":
		return req.PlanArtifact == nil
	default:
		return false
	}
}

func stringifiedStructuredFieldRepresentsEmptyValue(field string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	switch field {
	case "plan_artifact":
		var obj map[string]json.RawMessage

		if err := json.Unmarshal([]byte(value), &obj); err != nil {
			return false
		}

		return len(obj) == 0
	default:
		if !stringifiedJSONToolFieldIsArray(field) {
			return false
		}

		var arr []json.RawMessage

		if err := json.Unmarshal([]byte(value), &arr); err != nil {
			return false
		}

		return len(arr) == 0
	}
}

func repairEmbeddedQwenXMLToolParameters(req *ToolRequest) {
	if req == nil || strings.TrimSpace(req.Rationale) == "" || !strings.Contains(req.Rationale, "<parameter=") {
		return
	}

	for _, param := range qwenXMLParameterPattern.FindAllStringSubmatch(req.Rationale, -1) {
		if len(param) != 3 {
			continue
		}

		name := strings.TrimSpace(param[1])

		raw, err := qwenXMLParameterRaw(name, param[2])
		if err != nil {
			continue
		}

		switch name {
		case "edits":
			if len(req.Edits) == 0 {
				var edits []PlanEdit

				if err := json.Unmarshal(raw, &edits); err == nil {
					req.Edits = edits
				}
			}
		case "required_tests":
			if len(req.RequiredTests) == 0 {
				var values []string

				if err := json.Unmarshal(raw, &values); err == nil {
					req.RequiredTests = values
				}
			}
		case "required_edit_paths":
			if len(req.RequiredEditPaths) == 0 {
				var values []string

				if err := json.Unmarshal(raw, &values); err == nil {
					req.RequiredEditPaths = values
				}
			}
		case "rationale":
			var value string

			if err := json.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
				req.Rationale = strings.TrimSpace(value)
			}
		}
	}

	if idx := strings.Index(req.Rationale, "<parameter="); idx >= 0 {
		req.Rationale = strings.TrimSpace(req.Rationale[:idx])
	}
}

func repairToolRequestJSONPayload(payload []byte) ([]byte, bool) {
	current := payload
	repaired := false

	for range 4 {
		stepRepaired := false

		for _, repair := range []func([]byte) ([]byte, bool){
			repairMalformedToolNameObjectPayload,
			repairRawJSONControlChars,
			repairStringifiedJSONToolFields,
			repairObjectEvidenceToolField,
			repairSourceSatisfiedPathsToolField,
			repairSingletonArrayToolFields,
			repairStringArrayToolFields,
			repairNumericStringToolFields,
			repairBoolStringToolFields,
		} {
			next, ok := repair(current)
			if !ok {
				continue
			}

			current = next
			repaired = true
			stepRepaired = true
		}

		if !stepRepaired {
			break
		}
	}

	if !repaired {
		return nil, false
	}

	return current, true
}

func repairMalformedToolNameObjectPayload(payload []byte) ([]byte, bool) {
	text := strings.TrimSpace(string(payload))
	if text == "" {
		return nil, false
	}

	matches := malformedToolNameObjectPattern.FindStringSubmatch(text)
	if len(matches) != 3 {
		return nil, false
	}

	tool := strings.TrimSpace(matches[1])
	body := strings.TrimSpace(matches[2])

	if tool == "" || body == "" {
		return nil, false
	}

	repaired := fmt.Sprintf(`{"tool":%q,%s}`, tool, body)
	if !json.Valid([]byte(repaired)) {
		return nil, false
	}

	return []byte(repaired), true
}

func repairRawJSONControlChars(payload []byte) ([]byte, bool) {
	repaired := escapeRawControlCharsInJSONStringLiterals(string(payload))
	if repaired == string(payload) {
		return nil, false
	}

	if !json.Valid([]byte(repaired)) {
		return nil, false
	}

	return []byte(repaired), true
}

func repairStringifiedJSONToolFields(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	repaired := false

	for _, field := range stringifiedJSONToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		var encoded string

		if err := json.Unmarshal(raw, &encoded); err != nil {
			continue
		}

		decoded := strings.TrimSpace(encoded)
		if decoded == "" {
			continue
		}

		if json.Valid([]byte(decoded)) {
			obj[field] = json.RawMessage(decoded)
			repaired = true

			continue
		}

		if escaped, ok := repairStringifiedJSONControlChars(decoded); ok {
			obj[field] = escaped
			repaired = true

			continue
		}

		if stringifiedStructuredFieldHasTopLevelJSONStart(field, decoded) {
			if extracted, ok := extractLeadingStringifiedJSONToolFieldValue(field, decoded); ok {
				obj[field] = extracted
				repaired = true
			}

			continue
		}

		if extracted, ok := extractStringifiedJSONToolFieldValue(field, decoded); ok {
			obj[field] = extracted
			repaired = true
		}
	}

	if !repaired {
		return nil, false
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairObjectEvidenceToolField(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	raw, ok := obj["evidence"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, false
	}

	encoded, err := json.Marshal([]string{string(bytes.TrimSpace(raw))})
	if err != nil {
		return nil, false
	}

	obj["evidence"] = encoded

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairSourceSatisfiedPathsToolField(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	raw, ok := obj["source_satisfied_paths"]
	if !ok {
		return nil, false
	}

	paths, ok := decodeSourceSatisfiedPathsField(raw, true)
	if !ok {
		return nil, false
	}

	encoded, err := json.Marshal(paths)
	if err != nil {
		return nil, false
	}

	obj["source_satisfied_paths"] = encoded

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func decodeSourceSatisfiedPathsField(raw json.RawMessage, allowPathKeyedObject bool) ([]string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}

	var paths []string

	switch trimmed[0] {
	case '"':
		var value string

		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, false
		}

		value = strings.TrimSpace(value)
		if value == "" {
			return nil, false
		}

		return []string{value}, true
	case '{':
		return decodeSourceSatisfiedPathsObject(trimmed, allowPathKeyedObject)
	case '[':
		var items []json.RawMessage

		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, false
		}

		for _, item := range items {
			itemPaths, ok := decodeSourceSatisfiedPathsField(item, false)
			if !ok {
				return nil, false
			}

			paths = append(paths, itemPaths...)
		}
	default:
		return nil, false
	}

	paths = util.Unique(paths)
	if len(paths) == 0 {
		return nil, false
	}

	return paths, true
}

func decodeSourceSatisfiedPathsObject(raw json.RawMessage, allowPathKeyedObject bool) ([]string, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}

	for _, key := range []string{"path", "file"} {
		if value, ok := decodeStringObjectField(obj, key); ok {
			return []string{value}, true
		}
	}

	for _, key := range []string{"paths", "files", "source_satisfied_paths"} {
		if rawPaths, ok := obj[key]; ok {
			return decodeSourceSatisfiedPathsField(rawPaths, true)
		}
	}

	if !allowPathKeyedObject {
		return nil, false
	}

	paths := make([]string, 0, len(obj))
	for key := range obj {
		key = strings.TrimSpace(key)
		if key != "" {
			paths = append(paths, key)
		}
	}

	paths = util.Unique(paths)
	if len(paths) == 0 {
		return nil, false
	}

	sort.Strings(paths)

	return paths, true
}

func decodeStringObjectField(obj map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := obj[key]
	if !ok {
		return "", false
	}

	var value string

	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}

	value = strings.TrimSpace(value)

	return value, value != ""
}

func stringifiedStructuredFieldHasTopLevelJSONStart(field string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	allowedStarts := stringifiedJSONToolFieldAllowedStarts(field)
	if allowedStarts == "" {
		return false
	}

	return strings.ContainsRune(allowedStarts, rune(value[0]))
}

func extractLeadingStringifiedJSONToolFieldValue(field string, value string) (json.RawMessage, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}

	allowedStarts := stringifiedJSONToolFieldAllowedStarts(field)
	if allowedStarts == "" {
		return nil, false
	}

	if !strings.ContainsRune(allowedStarts, rune(value[0])) {
		return nil, false
	}

	end, ok := balancedJSONEnd(value, 0)
	if !ok || !safeTrailingStringifiedJSONText(value[end:]) {
		return nil, false
	}

	candidate := strings.TrimSpace(value[:end])
	if json.Valid([]byte(candidate)) {
		return json.RawMessage(candidate), true
	}

	if escaped, ok := repairStringifiedJSONControlChars(candidate); ok {
		return escaped, true
	}

	return nil, false
}

func safeTrailingStringifiedJSONText(tail string) bool {
	if strings.TrimSpace(tail) == "" {
		return true
	}

	return strings.HasPrefix(tail, "\n\n") || strings.HasPrefix(strings.TrimSpace(tail), "```")
}

func extractStringifiedJSONToolFieldValue(field string, value string) (json.RawMessage, bool) {
	allowedStarts := stringifiedJSONToolFieldAllowedStarts(field)
	if allowedStarts == "" {
		return nil, false
	}

	return extractBalancedJSONToolValue(value, allowedStarts)
}

func extractBalancedJSONToolValue(value string, allowedStarts string) (json.RawMessage, bool) {
	for i := 0; i < len(value); i++ {
		if !strings.ContainsRune(allowedStarts, rune(value[i])) {
			continue
		}

		end, ok := balancedJSONEnd(value, i)
		if !ok {
			continue
		}

		candidate := strings.TrimSpace(value[i:end])
		if json.Valid([]byte(candidate)) {
			return json.RawMessage(candidate), true
		}

		if escaped, ok := repairStringifiedJSONControlChars(candidate); ok {
			return escaped, true
		}
	}

	return nil, false
}

func repairStringifiedJSONControlChars(value string) (json.RawMessage, bool) {
	repaired := escapeRawControlCharsInJSONStringLiterals(value)
	if repaired == value {
		return nil, false
	}

	repaired = strings.TrimSpace(repaired)
	if !json.Valid([]byte(repaired)) {
		return nil, false
	}

	return json.RawMessage(repaired), true
}

func escapeRawControlCharsInJSONStringLiterals(value string) string {
	var b strings.Builder

	b.Grow(len(value))

	inString := false
	escaped := false
	changed := false

	for _, r := range value {
		if !inString {
			if r == '"' {
				inString = true
			}

			b.WriteRune(r)

			continue
		}

		if escaped {
			escaped = false

			b.WriteRune(r)

			continue
		}

		switch r {
		case '\\':
			escaped = true

			b.WriteRune(r)
		case '"':
			inString = false

			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)

			changed = true
		case '\r':
			b.WriteString(`\r`)

			changed = true
		case '\t':
			b.WriteString(`\t`)

			changed = true
		default:
			if r >= 0 && r < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, r))

				changed = true

				continue
			}

			b.WriteRune(r)
		}
	}

	if !changed {
		return value
	}

	return b.String()
}

func balancedJSONEnd(value string, start int) (int, bool) {
	if start < 0 || start >= len(value) {
		return 0, false
	}

	var stack []byte

	inString := false
	escaped := false

	for i := start; i < len(value); i++ {
		ch := value[i]

		if inString {
			if escaped {
				escaped = false
				continue
			}

			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}

			continue
		}

		switch ch {
		case '"':
			inString = true
		case '[', '{':
			stack = append(stack, ch)
		case ']':
			if len(stack) == 0 || stack[len(stack)-1] != '[' {
				return 0, false
			}

			stack = stack[:len(stack)-1]
		case '}':
			if len(stack) == 0 || stack[len(stack)-1] != '{' {
				return 0, false
			}

			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			return i + 1, true
		}
	}

	return 0, false
}

func repairSingletonArrayToolFields(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	repaired := false

	for _, field := range singletonArrayToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			continue
		}

		if !json.Valid(trimmed) {
			continue
		}

		wrapped, err := json.Marshal([]json.RawMessage{trimmed})
		if err != nil {
			continue
		}

		obj[field] = wrapped
		repaired = true
	}

	if !repaired {
		return nil, false
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairStringArrayToolFields(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage

	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}

	repaired := false

	for _, field := range stringArrayToolFieldNames {
		raw, ok := obj[field]
		if !ok {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}

		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		wrapped, err := json.Marshal([]string{value})
		if err != nil {
			continue
		}

		obj[field] = wrapped
		repaired = true
	}

	if !repaired {
		return nil, false
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}

	return out, true
}

func normalizeToolRequestAliases(req *ToolRequest) {
	if req == nil {
		return
	}

	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Symbol) != "" {
		req.Pattern = req.Symbol
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Operation) != "" {
		req.Capability = req.Operation
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Action) != "" && toolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = req.Action
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Resolution) != "" && toolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = CodeNavigateResolutionCapabilityAlias(req.Resolution)
	}

	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Query) != "" && toolRequestUsesPatternAlias(req.Tool) {
		req.Pattern = req.Query
	}
}

func toolRequestUsesCapabilityAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case ToolNameCodeNavigate, ToolNameReadOnlyCapability:
		return true
	default:
		return false
	}
}

func toolRequestUsesPatternAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case ToolNameSearchContent, ToolNameRg, ToolNameSearchFiles:
		return true
	default:
		return false
	}
}

func repairNumericStringToolFields(payload []byte) ([]byte, bool) {
	var value any

	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, false
	}

	repaired, invalid := repairNumericStringToolValue(value)
	if invalid || !repaired {
		return nil, false
	}

	out, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairBoolStringToolFields(payload []byte) ([]byte, bool) {
	var value any

	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, false
	}

	repaired, invalid := repairBoolStringToolValue(value)
	if invalid || !repaired {
		return nil, false
	}

	out, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}

	return out, true
}

func repairBoolStringToolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case map[string]any:
		repaired := false

		for key, raw := range typed {
			if boolToolField(key) {
				text, ok := raw.(string)
				if !ok {
					continue
				}

				parsed, ok := parseStrictBoolString(text)
				if !ok {
					return repaired, true
				}

				typed[key] = parsed
				repaired = true

				continue
			}

			childRepaired, invalid := repairBoolStringToolValue(raw)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	case []any:
		repaired := false

		for _, item := range typed {
			childRepaired, invalid := repairBoolStringToolValue(item)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	default:
		return false, false
	}
}

func parseStrictBoolString(text string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func repairNumericStringToolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case map[string]any:
		repaired := false

		for key, raw := range typed {
			if numericToolField(key) {
				text, ok := raw.(string)
				if !ok {
					continue
				}

				text = strings.TrimSpace(text)
				if text == "" || strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
					return repaired, true
				}

				parsed, err := strconv.Atoi(text)
				if err != nil {
					return repaired, true
				}

				typed[key] = parsed
				repaired = true

				continue
			}

			childRepaired, invalid := repairNumericStringToolValue(raw)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	case []any:
		repaired := false

		for _, item := range typed {
			childRepaired, invalid := repairNumericStringToolValue(item)
			if invalid {
				return repaired, true
			}

			repaired = repaired || childRepaired
		}

		return repaired, false
	default:
		return false, false
	}
}

func boolToolField(field string) bool {
	switch field {
	case "replace_all", "recursive", "include_evidence", "include_source", "include_files", "refresh":
		return true
	default:
		return false
	}
}

func numericToolField(field string) bool {
	switch field {
	case "start_line", "end_line", "max_depth", "limit", "budget_tokens", "freshness_max_files":
		return true
	default:
		return false
	}
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
		b.WriteString(formatToolResultOutputForModel(result))
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func formatToolResultOutputForModel(result ToolResult) string {
	output := strings.TrimSpace(result.Output)
	if strings.TrimSpace(result.Tool) == "context_pack" {
		if compact := compactContextPackOutputForModel(output); compact != "" {
			return compact
		}
	}

	return output
}

// compactContextPackOutputForModel renders a PackResult JSON output as
// model-facing compact text using the shared CompactPackProjection and
// RenderCompactPackProjection from the comprehension package.
func compactContextPackOutputForModel(output string) string {
	var result comprehension.PackResult

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return ""
	}

	if len(result.CandidateFiles) == 0 && len(result.CandidateSymbols) == 0 && len(result.Tests) == 0 {
		return ""
	}

	// Use the pre-computed CompactProjection from Pack() when available;
	// fall back to building a fresh projection for pre-Atom-5 cached
	// packs that lack the field.
	projection := result.CompactProjection
	if projection.PackID == "" {
		projection = comprehension.NewCompactPackProjection(result)
	}

	rendered, _ := comprehension.RenderCompactPackProjection(projection, result.BudgetTokens)

	// Preserve artifact_path from the full PackResult for operator
	// convenience. It is inserted after the pack_id line so it stays
	// close to the identity header.
	if artifact := strings.TrimSpace(result.ArtifactPath); artifact != "" {
		rendered = insertArtifactPathAfterPackID(rendered, artifact)
	}

	return rendered
}

// insertArtifactPathAfterPackID inserts an artifact_path line after the
// pack_id line in a rendered compact projection. If no pack_id line is
// found, the artifact path is prepended as a simple note.
func insertArtifactPathAfterPackID(rendered string, artifactPath string) string {
	const packIDPrefix = "pack_id: "

	idx := strings.Index(rendered, packIDPrefix)

	if idx < 0 {
		// No pack_id line; prepend as a note after the summary header.
		headerEnd := strings.Index(rendered, "\n")
		if headerEnd < 0 {
			return "artifact_path: " + artifactPath + "\n" + rendered
		}

		return rendered[:headerEnd+1] + "artifact_path: " + artifactPath + "\n" + rendered[headerEnd+1:]
	}

	// Find end of the pack_id line.
	endIdx := strings.Index(rendered[idx:], "\n")
	if endIdx < 0 {
		return rendered + "\nartifact_path: " + artifactPath
	}

	insertPos := idx + endIdx + 1

	return rendered[:insertPos] + "artifact_path: " + artifactPath + "\n" + rendered[insertPos:]
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

	if payload, ok := extractPlainToolPrefixPayload(trimmed); ok {
		return payload, true
	}

	if payload, ok := extractCallingToolPayload(trimmed); ok {
		return payload, true
	}

	if strings.HasPrefix(trimmed, "{") {
		if object, ok := extractFirstJSONObject(trimmed); ok {
			if bareJSONObjectIsToolPayload(object) {
				return object, true
			}

			return "", false
		}

		if strings.Contains(trimmed, `"tool"`) {
			return trimmed, true
		}
	}

	return "", false
}

func bareJSONObjectIsToolPayload(payload string) bool {
	data := []byte(payload)
	if !json.Valid(data) {
		if repaired, ok := repairRawJSONControlChars(data); ok {
			data = repaired
		}
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal(data, &fields); err != nil {
		return false
	}

	if _, ok := fields["tool"]; ok {
		return true
	}

	if _, ok := fields["capability"]; ok {
		return false
	}

	var req ToolRequest

	if err := json.Unmarshal(data, &req); err != nil {
		return false
	}

	return bareJSONMissingToolPayloadIsReadOnly(req)
}

func bareJSONMissingToolPayloadIsReadOnly(req ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "" {
		return true
	}

	if len(req.Todos) > 0 || len(req.Items) > 0 {
		return false
	}

	if strings.TrimSpace(req.Command) != "" ||
		strings.TrimSpace(req.Prompt) != "" ||
		strings.TrimSpace(req.Agent) != "" ||
		strings.TrimSpace(req.Destination) != "" ||
		len(req.Declarations) > 0 ||
		len(req.Moves) > 0 ||
		strings.HasPrefix(strings.TrimSpace(req.Pattern), "tag:") ||
		strings.TrimSpace(req.OldText) != "" ||
		strings.TrimSpace(req.NewText) != "" ||
		strings.TrimSpace(req.Content) != "" ||
		len(req.Edits) > 0 ||
		req.PlanArtifact != nil ||
		strings.TrimSpace(req.Rationale) != "" {

		return false
	}

	if strings.TrimSpace(req.Glob) != "" && strings.TrimSpace(req.Pattern) != "" {
		return true
	}

	return req.StartLine != 0 || req.EndLine != 0 || looksLikeFileHandleID(req.ID)
}

func looksLikeFileHandleID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) < 2 || id[0] != 'f' {
		return false
	}

	for _, r := range id[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// extractCallingToolPayload detects the [Calling tool: name({json})]
// format that some models emit in textual-transport mode. The JSON object
// inside the envelope already contains a "tool" field — the extractor
// just needs to unwrap the outer brackets and return the JSON payload
// for standard DecodeToolRequestJSON decoding.
// Uses extractFirstJSONObject for bracket-depth-aware, string-escape-aware
// extraction, consistent with the other extractors in the chain.
func extractCallingToolPayload(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	// Match case-insensitive [Calling tool: prefix.
	lower := strings.ToLower(trimmed)

	const prefix = "[calling tool:"

	idx := strings.Index(lower, prefix)

	if idx < 0 {
		return "", false
	}

	// Skip past the prefix. The format is: TOOL_NAME(JSON_OBJECT)].
	rest := strings.TrimSpace(trimmed[idx+len(prefix):])
	// Find the first '{' to locate the JSON payload, then delegate to
	// extractFirstJSONObject for bracket-depth-aware, string-escape-aware
	// extraction.
	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return "", false
	}

	payload, ok := extractFirstJSONObject(rest[braceIdx:])
	if !ok {
		return "", false
	}

	// Must contain "tool" key to distinguish from prose JSON.
	if !strings.Contains(payload, `"tool"`) {
		return "", false
	}

	return payload, true
}

func extractPlainToolPrefixPayload(text string) (string, bool) {
	const prefix = "tool"

	if len(text) <= len(prefix) || !strings.HasPrefix(text, prefix) {
		return "", false
	}

	rest := text[len(prefix):]
	if rest == "" {
		return "", false
	}

	first := rest[0]
	if first != '{' && first != '\n' && first != '\r' && first != '\t' && first != ' ' && first != ':' {
		return "", false
	}

	rest = strings.TrimLeft(rest, " \t\r\n:")
	if !strings.HasPrefix(rest, "{") || !strings.Contains(rest, `"tool"`) {
		return "", false
	}

	object, ok := extractFirstJSONObject(rest)

	return object, ok
}

var (
	qwenXMLFunctionPattern         = regexp.MustCompile(`(?s)^<function=([^>\s]+)>(.*)</function>$`)
	qwenXMLParameterPattern        = regexp.MustCompile(`(?s)<parameter=([^>\s]+)>(.*?)</parameter>`)
	deepSeekXMLInvokePattern       = regexp.MustCompile(`(?is)<[^>\s]*(?:invoke|function)\s+name\s*=\s*["']([^"']+)["'][^>]*>(.*?)</[^>]*(?:invoke|function)\s*>`)
	deepSeekXMLParameterPattern    = regexp.MustCompile(`(?is)<[^>\s]*(?:parameter)\s+name\s*=\s*["']([^"']+)["'][^>]*>(.*?)</[^>]*(?:parameter)\s*>`)
	lineAnchoredToolJSONPattern    = regexp.MustCompile(`(?m)(^|\n)\s*tool\s*:\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*(\{)`)
	malformedToolNameObjectPattern = regexp.MustCompile(`(?s)^\{\s*"tool"\s*:\s*"([A-Za-z_][A-Za-z0-9_.-]*)"\s*:\s*\{(.*)\}\s*\}$`)
	knownBareJSONToolNamesOnce     sync.Once
	knownBareJSONToolNames         map[string]struct{}
)

func extractLineAnchoredToolJSONObjectPayload(text string) (string, bool, error) {
	match := lineAnchoredToolJSONPattern.FindStringSubmatchIndex(text)
	if len(match) < 8 {
		return "", false, nil
	}

	tool := strings.TrimSpace(text[match[4]:match[5]])
	if tool == "" {
		return "", true, fmt.Errorf("line-anchored tool request is missing tool name")
	}

	objectStart := match[6]
	object, ok := extractFirstJSONObject(text[objectStart:])

	if !ok {
		return "", true, fmt.Errorf("line-anchored tool request is missing JSON arguments object")
	}

	payload, err := normalizeExplicitToolJSONObjectPayload("line-anchored", tool, object)
	if err != nil {
		return object, true, err
	}

	return payload, true, nil
}

func extractBareToolJSONObjectPayload(text string) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "{") {
		return "", false, nil
	}

	tool, rest, ok := splitBareToolJSONObjectPrefix(trimmed)
	if !ok || !knownBareToolJSONObjectName(tool) {
		return "", false, nil
	}

	object, ok := extractFirstJSONObject(rest)
	if !ok {
		return "", true, fmt.Errorf("bare tool request %q is missing JSON arguments object", tool)
	}

	payload, err := normalizeExplicitToolJSONObjectPayload("bare", tool, object)
	if err != nil {
		return object, true, err
	}

	return payload, true, nil
}

func splitBareToolJSONObjectPrefix(text string) (string, string, bool) {
	toolEnd := -1

	for i, r := range text {
		if r == '_' || r == '.' || r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || (i > 0 && '0' <= r && r <= '9') {
			continue
		}

		toolEnd = i

		break
	}

	if toolEnd <= 0 {
		return "", "", false
	}

	tool := strings.TrimSpace(text[:toolEnd])
	rest := strings.TrimSpace(text[toolEnd:])

	if tool == "" || rest == "" || !strings.HasPrefix(rest, "{") {
		return "", "", false
	}

	return tool, rest, true
}

func knownBareToolJSONObjectName(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	knownBareJSONToolNamesOnce.Do(func() {
		knownBareJSONToolNames = make(map[string]struct{})

		for _, name := range toolManifestToolsAcrossBuiltInSurfaces() {
			if name = strings.TrimSpace(name); name != "" {
				knownBareJSONToolNames[name] = struct{}{}
			}
		}
	})

	_, ok := knownBareJSONToolNames[tool]

	return ok
}

func normalizeExplicitToolJSONObjectPayload(source string, tool string, object string) (string, error) {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return "", err
	}

	if rawTool, exists := fields["tool"]; exists {
		var explicit string

		if err := json.Unmarshal(rawTool, &explicit); err != nil {
			return "", fmt.Errorf("%s tool request has non-string tool field", source)
		}

		if explicit = strings.TrimSpace(explicit); explicit != "" && explicit != tool {
			return "", fmt.Errorf("%s tool name %q does not match JSON tool %q", source, tool, explicit)
		}
	}

	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	payload, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}

	return string(payload), nil
}

func extractQwenXMLToolPayload(text string) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (!strings.Contains(trimmed, "<function=") && !strings.Contains(trimmed, "<tool_call>") && !strings.Contains(trimmed, "<tool_")) {
		return "", false, nil
	}

	if payload, ok, err := extractQwenXMLPrefixedToolPayload(trimmed); ok || err != nil {
		return payload, ok, err
	}

	envelope, ok := trimQwenXMLToolEnvelope(trimmed)
	if !ok {
		return "", false, nil
	}

	if strings.HasPrefix(strings.TrimSpace(envelope), "<function=") {
		return extractQwenXMLFunctionPayload(envelope)
	}

	if object, ok := extractFirstJSONObject(envelope); ok {
		payload, err := normalizeQwenJSONToolPayload(object)
		if err != nil {
			return object, true, err
		}

		return payload, true, nil
	}

	if payload, ok, err := extractQwenXMLNamedToolPayload(envelope); ok || err != nil {
		return payload, ok, err
	}

	return extractQwenXMLFunctionPayload(envelope)
}

func extractDeepSeekXMLToolPayload(text string) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, nil
	}

	lower := strings.ToLower(trimmed)
	if !strings.Contains(lower, "invoke") && !strings.Contains(lower, "dsml") && !strings.Contains(lower, "tool_calls") {
		return "", false, nil
	}

	match := deepSeekXMLInvokePattern.FindStringSubmatch(trimmed)
	if len(match) != 3 {
		if strings.Contains(lower, "dsml") || strings.Contains(lower, "tool_calls") || strings.Contains(lower, "_invoke") {
			return trimmed, true, fmt.Errorf("malformed DeepSeek XML tool request")
		}

		return "", false, nil
	}

	tool := strings.TrimSpace(match[1])
	if tool == "" || !xmlToolNameIsSafe(tool) {
		return trimmed, true, fmt.Errorf("DeepSeek XML tool request is missing a safe tool name")
	}

	if !IsReadTool(tool) {
		return trimmed, true, fmt.Errorf("DeepSeek XML tool request %q is not a read/search/discovery tool", tool)
	}

	fields := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	for _, param := range deepSeekXMLParameterPattern.FindAllStringSubmatch(match[2], -1) {
		if len(param) != 3 {
			continue
		}

		name := strings.TrimSpace(param[1])
		if name == "" {
			continue
		}

		if !xmlToolNameIsSafe(name) {
			return trimmed, true, fmt.Errorf("DeepSeek XML tool request has unsafe parameter name %q", name)
		}

		if name == "operation" {
			name = "capability"
		}

		raw, err := qwenXMLParameterRaw(name, param[2])
		if err != nil {
			return trimmed, true, err
		}

		fields[name] = raw
	}

	data, err := json.Marshal(fields)
	if err != nil {
		return trimmed, true, err
	}

	return string(data), true, nil
}

func extractQwenXMLPrefixedToolPayload(text string) (string, bool, error) {
	text = strings.TrimSpace(text)

	start := strings.Index(text, "<tool_")

	for start >= 0 {
		tagStart := start + len("<tool_")
		tagEnd := strings.Index(text[tagStart:], ">")

		if tagEnd < 0 {
			return "", true, fmt.Errorf("malformed prefixed XML tool request")
		}

		tagEnd += tagStart

		tool := strings.TrimSpace(text[tagStart:tagEnd])

		if tool == "call" || tool == "code" || tool == "name" || !xmlToolNameIsSafe(tool) {
			next := strings.Index(text[tagEnd+1:], "<tool_")
			if next < 0 {
				return "", false, nil
			}

			start = tagEnd + 1 + next

			continue
		}

		closeTag := "</tool_" + tool + ">"
		closeStart := strings.Index(text[tagEnd+1:], closeTag)

		if closeStart < 0 {
			body := qwenXMLUnclosedPrefixedToolBody(text[tagEnd+1:])
			if payload, ok, err := extractQwenXMLPrefixedToolBodyPayload(tool, body); ok || err != nil {
				return payload, ok, err
			}

			return "", false, nil
		}

		closeStart += tagEnd + 1

		body := text[tagEnd+1 : closeStart]

		if payload, ok, err := extractQwenXMLPrefixedToolBodyPayload(tool, body); ok || err != nil {
			return payload, ok, err
		}

		if !strings.Contains(body, "<parameter=") {
			next := strings.Index(text[closeStart+len(closeTag):], "<tool_")
			if next < 0 {
				return "", false, nil
			}

			start = closeStart + len(closeTag) + next

			continue
		}

		return "", true, fmt.Errorf("prefixed XML tool request %q is missing close tag", tool)
	}

	return "", false, nil
}

func extractQwenXMLPrefixedToolBodyPayload(tool string, body string) (string, bool, error) {
	if object, ok := extractFirstJSONObject(body); ok {
		// Only treat the extracted JSON as a tool call if it contains
		// fields that match Qwen's actual tool-call schema ("name" or
		// "arguments"). Without this guard, any JSON inside <tool_NAME>
		// tags — including descriptive prose — is misclassified as a
		// tool invocation, causing false-positive parse rejections.
		if !qwenXMLObjectLooksLikeToolCall(object) {
			return "", false, nil
		}

		payload, err := normalizeExplicitToolJSONObjectPayload("prefixed XML", tool, object)
		if err != nil {
			return object, true, err
		}

		return payload, true, nil
	}

	if strings.Contains(body, "<parameter=") {
		payload, ok, err := extractQwenXMLFunctionPayload("<function=" + tool + ">" + body + "</function>")
		if ok && err == nil {
			return payload, true, nil
		}

		return "", true, fmt.Errorf("prefixed XML tool request %q is missing close tag", tool)
	}

	return "", false, nil
}

func qwenXMLUnclosedPrefixedToolBody(body string) string {
	body = strings.TrimSpace(body)
	for _, marker := range []string{"</tool_call>", "<tool_"} {
		if idx := strings.Index(body, marker); idx >= 0 {
			body = strings.TrimSpace(body[:idx])
		}
	}

	return body
}

// qwenXMLObjectLooksLikeToolCall checks whether a JSON object extracted
// from inside <tool_NAME> tags looks like a genuine Qwen tool call rather
// than descriptive prose. It accepts objects that contain recognized
// tool-parameter field names (drawn from the ToolRequest struct) or the
// standard Qwen call-envelope fields ("tool", "name", "arguments").
//
// This prevents false positives where Qwen writes prose like
//
//	<tool_note>{"key":"evidence","content":"found X"}</tool_note>
//
// where the JSON describes what a tool does rather than invoking one,
// while still accepting legitimate prefixed Qwen tool calls like
//
//	<tool_code_navigate>{"path":"foo.go","capability":"read_symbol","symbol":"Bar"}</tool_code_navigate>
//
// where the JSON carries tool parameters even though the tool name
// comes from the XML tag.
func qwenXMLObjectLooksLikeToolCall(object string) bool {
	if strings.TrimSpace(object) == "" {
		return false
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return false
	}

	// Standard Qwen call-envelope fields.
	if _, ok := fields["name"]; ok {
		return true
	}

	if _, ok := fields["arguments"]; ok {
		return true
	}

	if _, ok := fields["tool"]; ok {
		return true
	}

	// Recognized tool-parameter fields from the ToolRequest struct.
	// These strongly indicate the JSON is tool arguments, not prose.
	for _, param := range qwenXMLToolParameterFields {
		if _, ok := fields[param]; ok {
			return true
		}
	}

	return false
}

// qwenXMLToolParameterFields lists ToolRequest JSON field names that
// strongly indicate a tool invocation when found inside <tool_NAME>
// tags. Fields that are common in prose (e.g. "content", "description",
// "summary", "name" without arguments) are intentionally excluded.
var qwenXMLToolParameterFields = []string{
	"path", "pattern", "symbol", "capability", "command", "prompt",
	"task", "query", "rationale", "edits", "old_text", "new_text",
	"agent", "language", "start_line", "end_line", "replace_all",
	"recursive", "operation", "action", "resolution", "glob",
	"include_evidence", "include_source", "include_files", "refresh",
	"category", "component", "expected", "observed", "workaround",
	"impact", "evidence", "suggested_fix", "required_tests",
	"required_edit_paths", "forbidden_paths", "destination",
	"declarations", "moves", "todos", "items", "named_gap",
	"why_not_answering", "budget_tokens", "limit", "max_depth",
	"mode", "freshness_policy", "freshness_max_files",
}

func xmlToolNameIsSafe(tool string) bool {
	if strings.TrimSpace(tool) == "" {
		return false
	}

	for _, r := range tool {
		if r == '_' || r == '-' || r == '.' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			continue
		}

		return false
	}

	return true
}

func extractQwenXMLNamedToolPayload(envelope string) (string, bool, error) {
	tool, ok := extractQwenXMLTagValue(envelope, "tool_name")
	if !ok {
		tool, ok = extractQwenXMLTagValue(envelope, "name")
	}

	tool = strings.TrimSpace(tool)
	if !ok || tool == "" {
		return "", false, nil
	}

	arguments, ok := extractQwenXMLTagValue(envelope, "arguments")
	if !ok {
		arguments, ok = extractQwenXMLTagValue(envelope, "args")
	}

	if !ok {
		return envelope, true, fmt.Errorf("qwen XML named tool request is missing arguments")
	}

	var args map[string]json.RawMessage

	arguments = strings.TrimSpace(arguments)

	if arguments == "" {
		args = map[string]json.RawMessage{}
	} else if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		if escaped, repaired := repairStringifiedJSONControlChars(arguments); repaired {
			if repairErr := json.Unmarshal(escaped, &args); repairErr == nil {
				err = nil
			}
		}

		if err != nil {
			return envelope, true, fmt.Errorf("qwen XML named tool arguments must decode to an object: %w", err)
		}
	}

	fields := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	for key, value := range args {
		if strings.TrimSpace(key) == "" || key == "tool" {
			continue
		}

		fields[key] = value
	}

	data, err := json.Marshal(fields)
	if err != nil {
		return envelope, true, err
	}

	return string(data), true, nil
}

func extractQwenXMLTagValue(envelope string, tag string) (string, bool) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", false
	}

	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(envelope, open)

	if start < 0 {
		return extractQwenXMLSplitTagValue(envelope, tag)
	}

	start += len(open)

	end := strings.Index(envelope[start:], close)

	if end < 0 {
		return extractQwenXMLSplitTagValue(envelope, tag)
	}

	return envelope[start : start+end], true
}

func extractQwenXMLSplitTagValue(envelope string, tag string) (string, bool) {
	pattern := regexp.MustCompile(`(?s)<\s*` + regexp.QuoteMeta(tag) + `\s*>(.*?)<\s*/\s*` + regexp.QuoteMeta(tag) + `\s*>`)
	match := pattern.FindStringSubmatch(envelope)

	if len(match) != 2 {
		return "", false
	}

	return match[1], true
}

func extractQwenXMLFunctionPayload(envelope string) (string, bool, error) {
	envelope = strings.TrimSpace(envelope)

	match := qwenXMLFunctionPattern.FindStringSubmatch(envelope)

	if len(match) != 3 {
		return envelope, true, fmt.Errorf("malformed qwen XML tool request")
	}

	tool := strings.TrimSpace(match[1])
	if tool == "" {
		return envelope, true, fmt.Errorf("qwen XML tool request is missing function name")
	}

	fields := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw

	for _, param := range qwenXMLParameterPattern.FindAllStringSubmatch(match[2], -1) {
		if len(param) != 3 {
			continue
		}

		name := strings.TrimSpace(param[1])
		if name == "" {
			continue
		}

		if name == "operation" {
			name = "capability"
		}

		raw, err := qwenXMLParameterRaw(name, param[2])
		if err != nil {
			return envelope, true, err
		}

		fields[name] = raw
	}

	data, err := json.Marshal(fields)
	if err != nil {
		return envelope, true, err
	}

	return string(data), true, nil
}

func normalizeQwenJSONToolPayload(payload string) (string, error) {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		return payload, err
	}

	if _, ok := fields["tool"]; ok {
		return payload, nil
	}

	name := qwenJSONToolName(fields)
	if name == "" {
		return payload, nil
	}

	normalized := map[string]json.RawMessage{}
	toolRaw, _ := json.Marshal(name)

	normalized["tool"] = toolRaw

	if args, ok, err := qwenJSONToolArguments(fields); err != nil {
		return payload, err
	} else if ok {
		for key, value := range args {
			if strings.TrimSpace(key) == "" || key == "tool" {
				continue
			}

			normalized[key] = value
		}
	} else {
		for key, value := range fields {
			switch key {
			case "name", "function", "tool", "arguments", "args":
				continue
			default:
				normalized[key] = value
			}
		}
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		return payload, err
	}

	return string(data), nil
}

func qwenJSONToolName(fields map[string]json.RawMessage) string {
	for _, key := range []string{"name", "function"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}

		var name string

		if err := json.Unmarshal(raw, &name); err == nil {
			if name = strings.TrimSpace(name); name != "" {
				return name
			}
		}
	}

	return ""
}

func qwenJSONToolArguments(fields map[string]json.RawMessage) (map[string]json.RawMessage, bool, error) {
	for _, key := range []string{"arguments", "args"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}

		var args map[string]json.RawMessage

		if err := json.Unmarshal(raw, &args); err == nil {
			return args, true, nil
		}

		var encoded string

		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, true, fmt.Errorf("qwen JSON tool arguments must be an object or encoded JSON object")
		}

		encoded = strings.TrimSpace(encoded)
		if encoded == "" {
			return map[string]json.RawMessage{}, true, nil
		}

		if err := json.Unmarshal([]byte(encoded), &args); err != nil {
			if escaped, ok := repairStringifiedJSONControlChars(encoded); ok {
				if err := json.Unmarshal(escaped, &args); err == nil {
					return args, true, nil
				}
			}

			return nil, true, fmt.Errorf("qwen JSON tool arguments must decode to an object: %w", err)
		}

		return args, true, nil
	}

	return nil, false, nil
}

func trimQwenXMLToolEnvelope(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "<tool_call>"); idx >= 0 {
		text = strings.TrimSpace(text[idx+len("<tool_call>"):])
		if end := strings.Index(text, "</tool_call>"); end >= 0 {
			text = strings.TrimSpace(text[:end])
		}

		return text, true
	}

	if envelope, ok := extractQwenXMLTagValue(text, "tool_call"); ok {
		return strings.TrimSpace(envelope), true
	}

	if strings.HasPrefix(text, "<function=") {
		return text, true
	}

	return "", false
}

func qwenXMLParameterRaw(name string, value string) (json.RawMessage, error) {
	value = trimQwenXMLParameterValue(value)

	trimmed := strings.TrimSpace(value)

	switch strings.TrimSpace(name) {
	case "edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths", "declarations", "moves", "todos", "items":
		if trimmed == "" {
			return json.RawMessage("[]"), nil
		}

		if !json.Valid([]byte(trimmed)) {
			if escaped, ok := repairStringifiedJSONControlChars(trimmed); ok {
				return escaped, nil
			}

			return nil, fmt.Errorf("qwen XML parameter %q must be valid JSON array/object content", name)
		}

		return json.RawMessage(trimmed), nil
	case "plan_artifact":
		if trimmed == "" {
			return json.RawMessage("null"), nil
		}

		if !json.Valid([]byte(trimmed)) {
			if escaped, ok := repairStringifiedJSONControlChars(trimmed); ok {
				return escaped, nil
			}

			return nil, fmt.Errorf("qwen XML parameter %q must be valid JSON object content", name)
		}

		return json.RawMessage(trimmed), nil
	case "replace_all", "recursive", "include_evidence", "include_source", "include_files", "refresh":
		parsed, err := strconv.ParseBool(strings.ToLower(trimmed))
		if err != nil {
			return nil, fmt.Errorf("qwen XML parameter %q must be boolean: %w", name, err)
		}

		if parsed {
			return json.RawMessage("true"), nil
		}

		return json.RawMessage("false"), nil
	case "start_line", "end_line", "max_depth", "limit", "budget_tokens", "freshness_max_files":
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("qwen XML parameter %q must be integer: %w", name, err)
		}

		return json.RawMessage(strconv.Itoa(parsed)), nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}

		return raw, nil
	}
}

func trimQwenXMLParameterValue(value string) string {
	value = strings.TrimPrefix(value, "\n")
	value = strings.TrimSuffix(value, "\n")

	return value
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
	case ToolNameListFiles:
		return !IsScopedInspectionRequest(req)
	case ToolNameShell:
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

func isStructuredNarrowingTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	// Language tools (e.g. go_diagnostics) are always narrowing
	// regardless of whether they also have a built-in surface spec entry.
	if isLanguageToolName(tool) {
		return true
	}

	if metadata, ok := builtInToolSurfaceMetadata(tool); ok {
		return metadata.Narrowing
	}

	return false
}

func IsNarrowingRequest(req ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case ToolNameListFiles:
		return IsScopedInspectionRequest(req)
	case ToolNameReadFile:
		return true
	case ToolNameShell:
		return isNarrowingShell(req.Command)
	default:
		return isStructuredNarrowingTool(req.Tool)
	}
}

func (m *Manager) ShouldClearBroadInspectionLock(req ToolRequest, result ToolResult) bool {
	if result.Error != "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case ToolNameListFiles:
		return IsScopedInspectionRequest(req)
	case ToolNameReadFile:
		path, _, err := m.resolveReadTarget(req)
		if err != nil {
			return false
		}

		return m.isRelevantExplorationPath(path)
	case ToolNameShell:
		return isNarrowingShell(req.Command)
	default:
		return isStructuredNarrowingTool(req.Tool)
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

		slices.SortFunc(children, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })

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

	pattern, interpretedPattern := searchFilesPattern(req)
	if pattern == "" {
		err := "pattern is required"
		// Detect when the model may have put a glob in the path field
		// instead of the pattern field — common confusion with **/*.go.
		if looksLikeGlob(strings.TrimSpace(req.Path)) ||
			looksLikeGlob(strings.TrimSpace(req.Glob)) {

			err = "pattern is required — the pattern field expects a filename substring, not a glob. If you meant to match file extensions, use e.g. pattern: '.go' instead of a glob path."
		}

		return ToolResult{Tool: "search_files", Summary: "File search rejected", Error: err}
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 4
	}

	query := strings.ToLower(pattern)
	includeLogDirs := searchIntentTargetsLogs(req.Path, pattern, req.Glob)
	rootDepth := strings.Count(filepath.Clean(base), string(filepath.Separator))

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
			display: m.displayPath(path),
		}
		if info, err := d.Info(); err == nil {
			match.modTime = info.ModTime()
			match.size = info.Size()
		}

		matches = append(matches, match)
		if !includeLogDirs && len(matches) >= m.cfg.MaxDirectoryEntries {
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
		tokenHits := m.tokenMatchedPaths(pattern, 5)
		suggestions := append([]string{}, tokenHits...)

		for _, p := range m.findPathSuggestions(pattern, 5) {
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

		sortSearchFileSuggestions(suggestions)

		output := ""
		summary := fmt.Sprintf("No file matches for %q", pattern)

		if len(suggestions) > 0 {
			summary = fmt.Sprintf("No file matches for %q. Closest paths in this workspace: %s.", pattern, strings.Join(suggestions, ", "))
			if note := searchFileSuggestionEvidenceShapeSummary(suggestions); note != "" {
				summary += " " + note
			}

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

		if interpretedPattern != "" {
			summary += " " + interpretedPattern
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
		slices.SortFunc(matches, func(a, b searchFileMatch) int {
			if rankDiff := cmp.Compare(searchFileKindRank(classifySearchFilePath(a.display)), searchFileKindRank(classifySearchFilePath(b.display))); rankDiff != 0 {
				return rankDiff
			}

			return cmp.Compare(a.display, b.display)
		})
	}

	displayLimit := len(matches)
	if includeLogDirs && displayLimit > 24 {
		displayLimit = 24
	}

	output := make([]string, 0, displayLimit+1)
	for _, match := range matches[:displayLimit] {
		if includeLogDirs && !match.modTime.IsZero() {
			output = append(output, fmt.Sprintf("%s (modified %s; size %s)", m.handleDisplay(match.path), match.modTime.Format(time.RFC3339), formatBytes(match.size)))
			continue
		}

		output = append(output, m.handleDisplay(match.path))
	}

	summary := fmt.Sprintf("Found %d matching file(s).", len(matches))
	if includeLogDirs {
		if displayLimit < len(matches) {
			output = append(output, fmt.Sprintf("... omitted %d older log-like match(es)", len(matches)-displayLimit))
			summary = fmt.Sprintf("Found %d matching log-like file(s), newest first with modified time and size (showing first %d).", len(matches), displayLimit)
		} else {
			summary = fmt.Sprintf("Found %d matching log-like file(s), newest first with modified time and size.", len(matches))
		}
	} else if note := searchFileEvidenceShapeSummary(matches); note != "" {
		summary += " " + note
	}

	if interpretedPattern != "" {
		summary += " " + interpretedPattern
	}

	return ToolResult{
		Tool:    "search_files",
		Summary: summary,
		Output:  strings.Join(output, "\n"),
	}
}

type searchFileMatch struct {
	path    string
	display string
	modTime time.Time
	size    int64
}

type searchFileKind string

const (
	searchFileKindSource    searchFileKind = "source"
	searchFileKindTest      searchFileKind = "test"
	searchFileKindConfig    searchFileKind = "config"
	searchFileKindDocs      searchFileKind = "docs"
	searchFileKindFixture   searchFileKind = "fixtures"
	searchFileKindGenerated searchFileKind = "generated"
	searchFileKindLog       searchFileKind = "logs"
	searchFileKindOther     searchFileKind = "other"
)

func classifySearchFilePath(displayPath string) searchFileKind {
	clean := filepath.ToSlash(strings.TrimSpace(displayPath))
	lower := strings.ToLower(clean)
	name := strings.ToLower(filepath.Base(lower))
	segments := strings.Split(lower, "/")

	for _, segment := range segments {
		switch segment {
		case ".idiom", "logs", "log":
			return searchFileKindLog
		case "testdata", "fixtures", "fixture":
			return searchFileKindFixture
		case "generated", "generated-logs", "vendor", "node_modules":
			return searchFileKindGenerated
		case "docs", "doc":
			return searchFileKindDocs
		}
	}

	if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".log") {
		return searchFileKindLog
	}

	if strings.HasPrefix(name, "readme") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".mdx") {
		return searchFileKindDocs
	}

	if strings.HasSuffix(name, ".gen.go") || strings.HasSuffix(name, ".pb.go") || strings.Contains(lower, "/generated/") {
		return searchFileKindGenerated
	}

	if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".spec.tsx") || strings.HasSuffix(name, ".test.tsx") {
		return searchFileKindTest
	}

	switch filepath.Ext(name) {
	case ".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".swift", ".kt":
		return searchFileKindSource
	case ".json", ".yaml", ".yml", ".toml", ".mod", ".sum", ".lock":
		return searchFileKindConfig
	default:
		return searchFileKindOther
	}
}

func searchFileKindRank(kind searchFileKind) int {
	switch kind {
	case searchFileKindSource:
		return 0
	case searchFileKindTest:
		return 1
	case searchFileKindConfig:
		return 2
	case searchFileKindDocs:
		return 3
	case searchFileKindFixture:
		return 4
	case searchFileKindGenerated:
		return 5
	case searchFileKindLog:
		return 6
	default:
		return 7
	}
}

func searchFileKindIsCodeLike(kind searchFileKind) bool {
	return kind == searchFileKindSource || kind == searchFileKindTest
}

func searchFileEvidenceShapeSummary(matches []searchFileMatch) string {
	if len(matches) == 0 {
		return ""
	}

	kindCounts := make(map[searchFileKind]int)
	codeLike := 0

	for _, match := range matches {
		kind := classifySearchFilePath(match.display)
		kindCounts[kind]++

		if searchFileKindIsCodeLike(kind) {
			codeLike++
		}
	}

	if codeLike > 0 {
		if codeLike == len(matches) {
			return "All matches are code-like paths."
		}

		return "Code-like paths are ranked first; remaining matches include " + searchFileKindList(kindCounts, false) + "."
	}

	return "No code-like file matches; matches are " + searchFileKindList(kindCounts, true) + " paths."
}

func sortSearchFileSuggestions(suggestions []string) {
	slices.SortStableFunc(suggestions, func(a, b string) int {
		if rankDiff := cmp.Compare(searchFileKindRank(classifySearchFilePath(a)), searchFileKindRank(classifySearchFilePath(b))); rankDiff != 0 {
			return rankDiff
		}

		return cmp.Compare(a, b)
	})
}

func searchFileSuggestionEvidenceShapeSummary(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}

	matches := make([]searchFileMatch, 0, len(suggestions))
	for _, suggestion := range suggestions {
		matches = append(matches, searchFileMatch{display: suggestion})
	}

	return strings.Replace(searchFileEvidenceShapeSummary(matches), "matches are", "suggestions are", 1)
}

func searchFileKindList(kindCounts map[searchFileKind]int, includeCodeLike bool) string {
	var kinds []string

	for _, kind := range []searchFileKind{
		searchFileKindSource,
		searchFileKindTest,
		searchFileKindConfig,
		searchFileKindDocs,
		searchFileKindFixture,
		searchFileKindGenerated,
		searchFileKindLog,
		searchFileKindOther,
	} {
		if kindCounts[kind] == 0 {
			continue
		}

		if !includeCodeLike && searchFileKindIsCodeLike(kind) {
			continue
		}

		kinds = append(kinds, string(kind))
	}

	if len(kinds) == 0 {
		return "other"
	}

	return strings.Join(kinds, ", ")
}

func searchFilesPattern(req ToolRequest) (pattern string, interpreted string) {
	pattern = strings.TrimSpace(req.Pattern)
	if pattern != "" {
		return pattern, ""
	}

	glob := strings.TrimSpace(req.Glob)
	if glob != "" {
		return glob, fmt.Sprintf("Interpreted glob %q as the file search pattern.", glob)
	}

	query := strings.TrimSpace(req.Query)
	if query != "" {
		return query, fmt.Sprintf("Interpreted query %q as the file search pattern.", query)
	}

	return "", ""
}

func (m *Manager) readFile(req ToolRequest) ToolResult {
	path, description, err := m.resolveReadTarget(req)
	if err != nil {
		if recoveredPath, ok := m.closestRecoverableReadPath(req.Path, req.Workdir); ok {
			recoveredReq := req

			recoveredReq.Path = recoveredPath
			recoveredReq.ID = ""

			result := m.readFile(recoveredReq)

			if strings.TrimSpace(result.Error) == "" {
				result.Summary = fmt.Sprintf("Recovered missing read_file path %q as %q; %s", strings.TrimSpace(req.Path), recoveredPath, result.Summary)
				return result
			}
		}

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
						errMsg = readFileRecoveryError(fmt.Sprintf("parent directory %q does not exist in this workspace, so %q cannot exist either.", parent, req.Path))
					}
				}
			}
		}

		result := ToolResult{Tool: ToolNameReadFile, Summary: "Failed to resolve file target", Error: errMsg}
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
		errMsg := readFileRecoveryError(err.Error())
		if os.IsNotExist(err) && strings.TrimSpace(req.Path) != "" {
			parent := filepath.Dir(strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/"))
			if parent != "." && parent != "/" && parent != "" {
				if parentAbs, parentErr := m.resolvePath(parent, req.Workdir); parentErr == nil {
					if _, statErr := os.Stat(parentAbs); os.IsNotExist(statErr) {
						errMsg = readFileRecoveryError(fmt.Sprintf("parent directory %q does not exist in this workspace, so %q cannot exist either. (os: %s)", parent, req.Path, err.Error()))
					}
				}
			}
		}

		result := ToolResult{
			Tool:    ToolNameReadFile,
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

	lines := splitWorkspaceFileLines(data)
	start := req.StartLine
	end := req.EndLine
	requestedStart := start
	requestedEnd := end
	// Reversed ranges (start > end) get auto-swapped rather than
	// silently clamped to one line or rejected with an error. Smaller
	// planners (local Qwen 35B) emit (start=356, end=1) when they
	// want "tail of file" and don't self-correct on a strict error;
	// they just retry the same shape. Swapping returns useful content
	// and the summary suffix tells the model what we did so future
	// calls can use the canonical order.
	rangeWasSwapped := false

	if start > 0 && end > 0 && start > end {
		start, end = end, start
		requestedStart, requestedEnd = start, end
		rangeWasSwapped = true
	}

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

	if len(lines) == 0 {
		m.MarkPathObserved(m.displayPath(path))

		return ToolResult{
			Tool:    ToolNameReadFile,
			Summary: fmt.Sprintf("Read %s lines 0-0 (total lines: 0)", description),
			Output:  "",
		}
	}

	start, end, clampedToFileBounds := clampRequestedLineRange(len(lines), start, end, requestedStart, requestedEnd)
	if start > end {
		return ToolResult{Tool: ToolNameReadFile, Summary: "Invalid line range", Error: "start_line must be <= end_line"}
	}

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
	} else if openEndedLogRange {
		truncated = true
	}

	var b strings.Builder

	for i := start - 1; i < end && i < len(lines); i++ {
		line := lines[i]
		if logLike {
			line = util.TruncateBytes(strings.TrimRight(line, "\r\n"), defaultLogLineCharCap, "...[line truncated]")
		}

		fmt.Fprintf(&b, "%d:%s\n", i+1, line)
	}

	m.MarkPathObserved(m.displayPath(path))
	m.clearEditRecoveryAfterRead(path, start, end)

	summary := fmt.Sprintf("Read %s lines %d-%d", description, start, end)
	if tailRead {
		summary += fmt.Sprintf(" (file has %d lines total; defaulted to recent log tail; pass start_line/end_line for a different window)", len(lines))
	} else if openEndedLogRange {
		summary += fmt.Sprintf(" (file has %d lines total; open-ended log range capped to %d lines; pass an explicit end_line for a different bounded window)", len(lines), defaultLogReadLineCap)
	} else if clampedToFileBounds {
		summary += fmt.Sprintf(" (requested range %d-%d was clamped to file bounds; file has %d lines total)", displayRequestedLine(requestedStart, start), displayRequestedLine(requestedEnd, end), len(lines))
	} else if rangeWasSwapped {
		summary += fmt.Sprintf(" (file has %d lines total; start_line was greater than end_line; auto-swapped to lines %d-%d; pass start_line <= end_line on future calls)", len(lines), start, end)
	} else if truncated {
		summary += fmt.Sprintf(" (file has %d lines total; output was capped; pass start_line/end_line for a different range)", len(lines))
	} else {
		summary += fmt.Sprintf(" (total lines: %d)", len(lines))
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
		line = util.TruncateBytes(strings.TrimRight(line, "\r\n"), defaultLogLineCharCap, "...[line truncated]")
		fmt.Fprintf(&b, "%d:%s\n", start+i, line)
	}

	m.MarkPathObserved(m.displayPath(path))
	m.clearEditRecoveryAfterRead(path, start, start+len(lines)-1)

	return ToolResult{
		Tool:    "read_file",
		Summary: fmt.Sprintf("Read %s lines %d-%d (file has %d lines total; defaulted to recent log tail; pass start_line/end_line for a different window)", description, start, start+len(lines)-1, totalLines),
		Output:  strings.TrimRight(b.String(), "\n"),
	}, true
}

type fileStatsReport struct {
	Path             string           `json:"path"`
	Recursive        bool             `json:"recursive,omitempty"`
	MaxDepth         int              `json:"max_depth,omitempty"`
	Glob             string           `json:"glob,omitempty"`
	Files            int              `json:"files"`
	Directories      int              `json:"directories,omitempty"`
	Bytes            int64            `json:"bytes"`
	Lines            int              `json:"lines"`
	LineCountErrors  int              `json:"line_count_errors,omitempty"`
	Truncated        bool             `json:"truncated,omitempty"`
	Limit            int              `json:"limit,omitempty"`
	Entries          []fileStatsEntry `json:"entries,omitempty"`
	EntriesTruncated bool             `json:"entries_truncated,omitempty"`
}

type fileStatsEntry struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Lines int    `json:"lines,omitempty"`
	Error string `json:"error,omitempty"`
}

const fileStatsDefaultLimit = 1000
const fileStatsMaxLimit = 10000
const fileStatsEntryDisplayCap = 50

func (m *Manager) fileStats(req ToolRequest) ToolResult {
	requestPath := strings.TrimSpace(req.Path)
	if requestPath == "" {
		requestPath = "."
	}

	target, err := m.resolvePath(requestPath, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "file_stats", Summary: "Failed to resolve path", Error: err.Error()}
	}

	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Tool: "file_stats", Summary: "Failed to stat path", Error: err.Error()}
	}

	limit := req.Limit
	if limit <= 0 {
		limit = fileStatsDefaultLimit
	}

	if limit > fileStatsMaxLimit {
		limit = fileStatsMaxLimit
	}

	report := fileStatsReport{
		Path:      m.displayPath(target),
		Recursive: req.Recursive,
		MaxDepth:  req.MaxDepth,
		Glob:      strings.TrimSpace(req.Glob),
		Limit:     limit,
	}

	if !info.IsDir() {
		entry := m.fileStatsEntry(target, info)

		report.Files = 1
		report.Bytes = entry.Bytes
		report.Lines = entry.Lines

		if entry.Error != "" {
			report.LineCountErrors = 1
		}

		report.Entries = []fileStatsEntry{entry}

		return fileStatsResult(report)
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		if req.Recursive {
			maxDepth = 8
		} else {
			maxDepth = 1
		}
	}

	report.MaxDepth = maxDepth

	err = filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if filepath.Clean(path) == filepath.Clean(target) {
			return nil
		}

		rel, err := filepath.Rel(target, path)
		if err != nil {
			return nil
		}

		depth := pathDepth(rel)

		if entry.IsDir() {
			if m.shouldSkipListEntry(target, path, true) {
				return filepath.SkipDir
			}

			if depth > maxDepth {
				return filepath.SkipDir
			}

			report.Directories++

			return nil
		}

		if depth > maxDepth || m.shouldSkipListEntry(target, path, false) {
			return nil
		}

		if !fileStatsGlobMatch(report.Glob, rel) {
			return nil
		}

		if report.Files >= limit {
			report.Truncated = true
			return filepath.SkipAll
		}

		info, err := entry.Info()
		if err != nil {
			return nil
		}

		stats := m.fileStatsEntry(path, info)
		report.Files++

		report.Bytes += stats.Bytes
		report.Lines += stats.Lines

		if stats.Error != "" {
			report.LineCountErrors++
		}

		if len(report.Entries) < fileStatsEntryDisplayCap {
			report.Entries = append(report.Entries, stats)
		} else {
			report.EntriesTruncated = true
		}

		return nil
	})
	if err != nil {
		return ToolResult{Tool: "file_stats", Summary: "Failed to collect file stats", Error: err.Error()}
	}

	return fileStatsResult(report)
}

func (m *Manager) fileStatsEntry(path string, info os.FileInfo) fileStatsEntry {
	entry := fileStatsEntry{
		Path:  m.displayPath(path),
		Bytes: info.Size(),
	}

	lines, err := countFileNewlines(path)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}

	entry.Lines = lines

	return entry
}

func fileStatsResult(report fileStatsReport) ToolResult {
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "file_stats", Summary: "Failed to encode file stats", Error: err.Error()}
	}

	summary := fmt.Sprintf("Counted %d file(s), %d line(s), %s under %s", report.Files, report.Lines, formatBytes(report.Bytes), report.Path)
	if report.Truncated {
		summary += fmt.Sprintf(" (truncated at %d files)", report.Limit)
	}

	return ToolResult{
		Tool:    "file_stats",
		Summary: summary,
		Output:  string(output),
	}
}

func countFileNewlines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	buf := make([]byte, 32*1024)
	count := 0

	var (
		total int64
		last  byte
	)

	for {
		n, err := file.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			count += bytes.Count(chunk, []byte{'\n'})
			total += int64(n)

			last = chunk[n-1]
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return 0, err
		}
	}

	if total > 0 && last != '\n' {
		count++
	}

	return count, nil
}

func pathDepth(rel string) int {
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == "." || cleaned == "" {
		return 0
	}

	return strings.Count(cleaned, "/") + 1
}

func fileStatsGlobMatch(glob string, rel string) bool {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return true
	}

	slashRel := filepath.ToSlash(rel)
	if ok, err := path.Match(glob, slashRel); err == nil && ok {
		return true
	}

	if ok, err := path.Match(glob, path.Base(slashRel)); err == nil && ok {
		return true
	}

	if ok, err := filepath.Match(glob, filepath.Base(rel)); err == nil && ok {
		return true
	}

	return false
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

	count := bytes.Count(data, []byte{'\n'})
	if !bytes.HasSuffix(data, []byte{'\n'}) {
		count++
	}

	return count
}

func (m *Manager) makeDir(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult("mkdir", "Write request rejected", "workspace writes are disabled")
	}

	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return toolPathResolutionFailureResult("mkdir", "Failed to resolve path", err)
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return toolWriteFailureResult("mkdir", "Failed to create directory", err)
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
		return toolWriteDisabledResult(toolName, "Write request rejected", "workspace writes are disabled")
	}

	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return toolPathResolutionFailureResult(toolName, "Failed to resolve path", err)
	}

	updated := []byte(req.Content)

	if appendMode {
		current, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return toolFailureFromErrorResult(toolName, "Failed to read existing file", err, failures.Internal, reasonToolInternalFailure)
		}

		updated = append(append([]byte(nil), current...), []byte(req.Content)...)
	} else if current, err := os.ReadFile(path); err == nil {
		if IsCompletedPlanningArtifact(path, current) {
			return withToolFailure(ToolResult{
				Tool:    toolName,
				Summary: "Write request rejected",
				Error:   fmt.Sprintf("write_file refuses to overwrite completed planning artifact %s. The available contract outcomes are a bounded anchored update or explicit user approval to reopen/replace the completed artifact.", m.displayPath(path)),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}
	} else if !os.IsNotExist(err) {
		return toolFailureFromErrorResult(toolName, "Failed to read existing file", err, failures.Internal, reasonToolInternalFailure)
	}

	if rejected := m.rejectInvalidPlannedWrite(toolName, "Write request rejected", path, updated); rejected != nil {
		return *rejected
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return toolWriteFailureResult(toolName, "Failed to create parent directory", err)
	}

	if appendMode {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return toolWriteFailureResult(toolName, "Failed to open file", err)
		}
		defer file.Close()

		if _, err := file.WriteString(req.Content); err != nil {
			return toolWriteFailureResult(toolName, "Failed to append file", err)
		}
	} else {
		if err := os.WriteFile(path, updated, 0o644); err != nil {
			return toolWriteFailureResult(toolName, "Failed to write file", err)
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
		return toolWriteDisabledResult("edit_file", "Edit request rejected", "workspace writes are disabled")
	}

	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		result := toolPathResolutionFailureResult("edit_file", "Failed to resolve path", err)
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
		return withToolFailure(ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("edit_file requires you to have observed %s in this session before editing it.", m.displayPath(path)),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
	}

	if rejected := m.rejectPendingEditRecovery("edit_file", path); rejected != nil {
		return *rejected
	}

	content, err := os.ReadFile(path)
	if err != nil {
		result := toolFailureFromErrorResult("edit_file", "Failed to read file", err, failures.Internal, reasonToolInternalFailure)
		if strings.TrimSpace(req.Path) != "" {
			result.Output = formatPathSuggestions(m.findPathSuggestions(req.Path, 5))
		}

		return result
	}

	current := string(content)

	if req.StartLine > 0 || req.EndLine > 0 {
		if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.OldText) != "" {
			// Some model/tool-call adapters include line metadata alongside
			// the exact-match shape. Prefer the explicit old_text/new_text
			// replacement instead of treating empty line-range content as a
			// deletion or ignoring new_text.
		} else if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.NewText) != "" {
			return withToolFailure(ToolResult{
				Tool:    "edit_file",
				Summary: "Edit request rejected",
				Error: fmt.Sprintf(
					"edit_file line-range edits use the `content` field, not `new_text`. "+
						"Either set content to the replacement lines for %s lines %d-%d, "+
						"or provide old_text with new_text and omit the line range.",
					m.displayPath(path), req.StartLine, req.EndLine,
				),
			}, failures.InvalidArgument, reasonToolInvalidArgument)
		} else {
			return m.editFileByLineRange(path, current, req)
		}
	}

	if strings.TrimSpace(req.OldText) == "" {
		// The model's malformed shape is almost always one of three:
		//   (1) it passed `content` thinking edit_file replaces the
		//       whole file.
		//   (2) it forgot to include old_text with the match form.
		//   (3) it meant the range form but dropped start_line/end_line.
		// Give a targeted hint based on which fields were actually provided.
		// Existing source/test files should recover to bounded edit shapes;
		// full-body writes remain a separate primitive for missing/new files
		// or non-source deliverables.
		display := m.displayPath(path)
		hasContent := strings.TrimSpace(req.Content) != ""

		var example string

		if hasContent && editRecoveryPathLooksSourceLike(path) {
			example = fmt.Sprintf(
				"edit_file needs either old_text (for an exact-match replace) or start_line+end_line (for a range replace). "+
					"You provided `content` with neither.\n"+
					"For an existing source/test file, use a bounded edit_file shape instead of whole-file replacement:\n"+
					"  (a) exact-match: use path %q, old_text copied from the file, complete new_text, and a concrete description.\n"+
					"  (b) line range: use path %q, concrete start_line/end_line values, complete replacement content, and a concrete description.\n"+
					"Do not re-submit the same call shape.",
				display, display,
			)
		} else if hasContent {
			example = fmt.Sprintf(
				"edit_file needs either old_text (for an exact-match replace) or start_line+end_line (for a range replace). "+
					"You provided `content` with neither.\n"+
					"If you want a full-body write for a missing/new file or non-source deliverable, use write_file instead:\n"+
					"  use path %q, complete file content, and a concrete description.\n"+
					"If you want a PARTIAL edit, pick one of these two edit_file shapes:\n"+
					"  (a) exact-match: use path %q, old_text copied from the file, complete new_text, and a concrete description.\n"+
					"  (b) line range: use path %q, concrete start_line/end_line values, complete replacement content, and a concrete description.\n"+
					"Do not re-submit the same call shape.",
				display, display, display,
			)
		} else {
			example = fmt.Sprintf(
				"edit_file requires old_text (and new_text) OR start_line/end_line (and content). You provided neither.\n"+
					"Valid shapes:\n"+
					"  (a) exact-match: use path %q, old_text copied from the file, complete new_text, and a concrete description.\n"+
					"  (b) line range: use path %q, concrete start_line/end_line values, complete replacement content, and a concrete description.",
				display, display,
			)
		}

		return toolInvalidArgumentResult("edit_file", "Edit request rejected", example)
	}

	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		if util.ContainsFold(current, req.OldText) {
			return withToolFailure(ToolResult{
				Tool:    "edit_file",
				Summary: "Edit request rejected",
				Error:   fmt.Sprintf("old_text did not appear in %s, but a case-insensitive match exists. Preserve exact casing from the file when copying anchors, or switch to the start_line/end_line+content form.", m.displayPath(path)),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}

		return withToolFailure(ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text did not appear in %s. Copy the exact substring from the file (including whitespace) or switch to the start_line/end_line+content form.", m.displayPath(path)),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
	}

	if matchCount > 1 && !req.ReplaceAll {
		return withToolFailure(ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("old_text matched %d locations in %s. Either extend old_text with surrounding context so it matches exactly one place, or set replace_all=true.", matchCount, m.displayPath(path)),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
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
		return toolWriteFailureResult("edit_file", "Failed to write edited file", err)
	}

	m.invalidateGoIndex()
	m.invalidateCppIndex()

	return ToolResult{
		Tool:    "edit_file",
		Summary: fmt.Sprintf("Edited %s", m.displayPath(path)),
		Output:  fmt.Sprintf("replaced occurrences: %d", replaced),
	}
}

func editRecoveryPathLooksSourceLike(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	for _, suffix := range []string{
		".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".kt", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs",
		".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".lua", ".sql",
		".html", ".css", ".scss", ".sass", ".vue", ".svelte", ".templ",
		".json", ".yaml", ".yml", ".toml", ".xml",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

func (m *Manager) editFileByLineRange(path string, current string, req ToolRequest) ToolResult {
	startLine := req.StartLine
	endLine := req.EndLine

	if startLine <= 0 {
		return toolInvalidArgumentResult("edit_file", "Edit request rejected", "start_line must be >= 1 (line numbers are 1-indexed).")
	}

	if endLine == 0 {
		endLine = startLine
	}

	if endLine < startLine {
		return toolInvalidArgumentResult("edit_file", "Edit request rejected", fmt.Sprintf("end_line (%d) must be >= start_line (%d). Swap them if you meant the other direction, or set end_line=start_line for a single-line edit.", endLine, startLine))
	}

	spans := lineSpans(current)
	if len(spans) == 0 {
		return toolInvalidArgumentResult("edit_file", "Edit request rejected", "cannot apply a line edit to an empty file")
	}

	if startLine > len(spans) || endLine > len(spans) {
		return withToolFailure(ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error:   fmt.Sprintf("requested lines %d-%d are outside %s (file has %d lines)", startLine, endLine, m.displayPath(path), len(spans)),
		}, failures.OutOfRange, reasonToolInvalidArgument)
	}

	startOffset := spans[startLine-1].start
	endOffset := spans[endLine-1].end
	replacedRange := current[startOffset:endOffset]
	// Empty or whitespace-only content with a line range is the most common
	// edit_file footgun. The model often calls edit_file with a line range
	// thinking "insert after line N" — but the line-range form REPLACES those
	// lines, so blank content silently deletes them. Reject this generically
	// when the target range contains real code/text; intentional deletion can
	// use the exact old_text/new_text form where the removed content is explicit.
	if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.NewText) == "" &&
		strings.TrimSpace(replacedRange) != "" {

		return withToolFailure(ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error: fmt.Sprintf(
				"empty or whitespace-only content with line range %d-%d would DELETE non-empty lines from %s. "+
					"If you intended to INSERT new content, use the substring-anchored form instead: "+
					"set old_text to a unique existing snippet (no line numbers needed) and set new_text to "+
					"old_text + your additions. If you genuinely intended deletion, use old_text/new_text "+
					"so the deleted text is explicit.",
				startLine, endLine, m.displayPath(path),
			),
		}, failures.InvalidArgument, reasonToolInvalidArgument)
	}

	if lineRangeReplacementDuplicatesPrecedingContext(current, spans, startLine, req.Content) {
		return withToolFailure(ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error: fmt.Sprintf(
				"line-range replacement for %s lines %d-%d appears to duplicate context immediately before the selected range. "+
					"Line-range content must contain only the final replacement for the selected lines. "+
					"For inserts, use old_text/new_text with a unique existing snippet and append the new content to new_text.",
				m.displayPath(path), startLine, endLine,
			),
		}, failures.InvalidArgument, reasonToolInvalidArgument)
	}

	replacement := req.Content
	if replacement != "" && strings.HasSuffix(replacedRange, "\n") &&
		!strings.HasSuffix(replacement, "\n") {

		replacement += lineEndingSuffix(replacedRange)
	}

	updated := current[:startOffset] + replacement + current[endOffset:]
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
		return toolWriteFailureResult("edit_file", "Failed to write edited file", err)
	}

	m.invalidateGoIndex()
	m.invalidateCppIndex()

	return ToolResult{
		Tool:    "edit_file",
		Summary: fmt.Sprintf("Edited %s lines %d-%d", m.displayPath(path), startLine, endLine),
		Output:  fmt.Sprintf("replaced lines: %d-%d", startLine, endLine),
	}
}

func lineEndingSuffix(text string) string {
	if strings.HasSuffix(text, "\r\n") {
		return "\r\n"
	}

	return "\n"
}

func lineRangeReplacementDuplicatesPrecedingContext(current string, spans []lineSpan, startLine int, replacement string) bool {
	if startLine <= 1 || strings.TrimSpace(replacement) == "" || len(spans) == 0 {
		return false
	}

	contextStartLine := startLine - 8
	if contextStartLine < 1 {
		contextStartLine = 1
	}

	context := current[spans[contextStartLine-1].start:spans[startLine-2].end]
	contextLines := util.NonEmptyTrimmedLines(context)
	replacementLines := util.NonEmptyTrimmedLines(replacement)
	maxCompare := min(len(contextLines), len(replacementLines), 6)

	for n := maxCompare; n >= 3; n-- {
		if slices.Equal(contextLines[len(contextLines)-n:], replacementLines[:n]) {
			return true
		}
	}

	return false
}

func (m *Manager) rejectInvalidPlannedWrite(toolName string, summary string, path string, contents []byte) *ToolResult {
	if m == nil {
		return nil
	}

	before, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		result := toolFailureFromErrorResult(toolName, summary, fmt.Errorf("preflight could not read existing %s: %w", m.displayPath(path), err), failures.Internal, reasonToolInternalFailure)
		return &result
	}

	if lineNo, line, ok := NewSuspiciousAdjacentDuplicateCodeLine(path, before, contents); ok {
		result := withToolFailure(ToolResult{
			Tool:    toolName,
			Summary: summary,
			Error: fmt.Sprintf(
				"preflight rejected %s because it would introduce adjacent duplicate code line %d: %q. "+
					"Combine adjacent edits into one replacement or include the existing line in old_text instead of re-declaring it.",
				m.displayPath(path), lineNo, line,
			),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

		return &result
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, contents, parser.AllErrors); err != nil {
			line := syntaxErrorLine(err)
			m.noteEditRecoveryRequired(path, line, err.Error())

			result := withToolFailure(ToolResult{
				Tool:    toolName,
				Summary: summary,
				Error:   m.formatSyntaxPreflightError(path, contents, line, err),
			}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

			return &result
		}
	case ".json":
		if !json.Valid(contents) {
			result := withToolFailure(ToolResult{
				Tool:    toolName,
				Summary: summary,
				Error:   fmt.Sprintf("preflight JSON validation failed for %s", m.displayPath(path)),
			}, failures.InvalidArgument, reasonToolInvalidArgument)

			return &result
		}
	}

	return nil
}

// IsCompletedPlanningArtifact reports whether a markdown artifact already
// declares a closed status. Whole-file replacement of these files is rejected
// by write_file/submit_plan; bounded edit_file updates remain available.
func IsCompletedPlanningArtifact(path string, content []byte) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".md" && ext != ".markdown" {
		return false
	}

	if !looksLikePlanningArtifactPath(path) {
		return false
	}

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if i >= 120 {
			break
		}

		if statusLineMarksCompleted(line) {
			return true
		}
	}

	return false
}

func looksLikePlanningArtifactPath(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	for _, segment := range strings.Split(normalized, "/") {
		switch segment {
		case ".planning", "planning", "plans", "campaigns", "atoms", "atom", "specs", "spec", "tasks", "tickets", "work-packages", "work_packages":
			return true
		}
	}

	base := strings.TrimSuffix(filepath.Base(normalized), filepath.Ext(normalized))

	return strings.Contains(base, "spec") || strings.Contains(base, "atom") || strings.Contains(base, "task")
}

func statusLineMarksCompleted(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(lower, "status") {
		return false
	}

	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, token := range tokens {
		switch token {
		case "complete", "completed", "accepted", "done", "shipped":
			return true
		}
	}

	return false
}

// NewSuspiciousAdjacentDuplicateCodeLine reports the first adjacent duplicate
// assignment/declaration line that appears in after but not before. It is a
// source-hygiene invariant, not a style lint: adjacent duplicate assignments
// almost always mean an anchored edit reintroduced nearby context.
func NewSuspiciousAdjacentDuplicateCodeLine(path string, before []byte, after []byte) (int, string, bool) {
	if !isSourceLikePath(path) {
		return 0, "", false
	}

	beforeCounts := adjacentDuplicateCodeLineCounts(string(before))
	beforeTotal := adjacentDuplicateBaselineLineCount(string(before))
	seen := make(map[string]int)
	seenTotal := 0
	previous := ""

	for i, raw := range strings.Split(string(after), "\n") {
		line := suspiciousDuplicateCodeLine(raw)
		if line == "" {
			previous = ""
			continue
		}

		if line == previous {
			seen[line]++

			seenTotal++

			if seenTotal > beforeTotal && seen[line] > beforeCounts[line] {
				return i + 1, line, true
			}
		}

		previous = line
	}

	return 0, "", false
}

func adjacentDuplicateCodeLineCounts(content string) map[string]int {
	counts := make(map[string]int)
	previous := ""

	for _, raw := range strings.Split(content, "\n") {
		line := suspiciousDuplicateCodeLine(raw)
		if line == "" {
			previous = ""
			continue
		}

		if line == previous {
			counts[line]++
		}

		previous = line
	}

	return counts
}

func adjacentDuplicateBaselineLineCount(content string) int {
	count := 0
	previous := ""

	for _, raw := range strings.Split(content, "\n") {
		line := duplicateCodeLineBaseline(raw)
		if line == "" {
			previous = ""
			continue
		}

		if line == previous {
			count++
		}

		previous = line
	}

	return count
}

func suspiciousDuplicateCodeLine(line string) string {
	line = duplicateCodeLineBaseline(line)
	if line == "" {
		return ""
	}

	if !hasAssignmentOperator(line) {
		return ""
	}

	return line
}

func duplicateCodeLineBaseline(line string) string {
	line = strings.TrimSpace(line)
	if len(line) < 8 {
		return ""
	}

	for _, prefix := range []string{"//", "#", "/*", "*", "*/", "import ", "package ", "#include"} {
		if strings.HasPrefix(line, prefix) {
			return ""
		}
	}

	return line
}

func hasAssignmentOperator(line string) bool {
	for i, r := range line {
		if r != '=' {
			continue
		}

		prev := rune(0)
		next := rune(0)

		if i > 0 {
			prev = rune(line[i-1])
		}

		if i+1 < len(line) {
			next = rune(line[i+1])
		}

		if prev == '=' || prev == '!' || prev == '<' || prev == '>' || next == '=' || next == '>' {
			continue
		}

		return true
	}

	return false
}

func isSourceLikePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx",
		".m", ".mm", ".rs", ".java", ".kt", ".kts", ".swift", ".cs",
		".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".php", ".gd",
		".sh", ".bash", ".zsh":
		return true
	default:
		return false
	}
}

func (m *Manager) rejectPendingEditRecovery(toolName string, path string) *ToolResult {
	state, ok := m.pendingEditRecovery(path)
	if !ok {
		return nil
	}

	display := m.displayPath(path)
	lineHint := ""

	if state.line > 0 {
		lineHint = fmt.Sprintf(" around line %d", state.line)
	}

	result := withToolFailure(ToolResult{
		Tool:    toolName,
		Summary: "Edit request rejected",
		Error: fmt.Sprintf(
			"previous syntax preflight failure on %s still needs evidence refresh%s. Required recovery evidence is a bounded failing-region read or path diff before retrying the anchored edit. Last validation error: %s",
			display,
			lineHint,
			state.detail,
		),
	}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

	return &result
}

func (m *Manager) noteEditRecoveryRequired(path string, line int, detail string) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.editRecoveryRequired == nil {
		m.editRecoveryRequired = make(map[string]editRecoveryState)
	}

	m.editRecoveryRequired[normalizePathKey(path)] = editRecoveryState{line: line, detail: strings.TrimSpace(detail)}
}

func (m *Manager) pendingEditRecovery(path string) (editRecoveryState, bool) {
	if m == nil {
		return editRecoveryState{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.editRecoveryRequired == nil {
		return editRecoveryState{}, false
	}

	state, ok := m.editRecoveryRequired[normalizePathKey(path)]

	return state, ok
}

func (m *Manager) clearEditRecovery(path string) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.editRecoveryRequired, normalizePathKey(path))
}

func (m *Manager) clearAllEditRecovery() {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for key := range m.editRecoveryRequired {
		delete(m.editRecoveryRequired, key)
	}
}

func (m *Manager) clearEditRecoveryAfterRead(path string, startLine int, endLine int) {
	state, ok := m.pendingEditRecovery(path)
	if !ok {
		return
	}

	if state.line > 0 && (startLine > state.line || endLine < state.line) {
		return
	}

	m.clearEditRecovery(path)
}

func (m *Manager) formatSyntaxPreflightError(path string, contents []byte, line int, err error) string {
	display := m.displayPath(path)

	var b strings.Builder

	if line > 0 {
		fmt.Fprintf(&b, "preflight syntax validation failed for %s at line %d: %v", display, line, err)
	} else {
		fmt.Fprintf(&b, "preflight syntax validation failed for %s: %v", display, err)
	}

	if context := plannedWriteLineContext(contents, line, 2); context != "" {
		b.WriteString("\n")
		b.WriteString(context)
	}

	if line > 0 {
		fmt.Fprintf(&b, "\nBefore retrying the anchored edit on %s, refresh evidence for the failing region around line %d with a bounded source read or path diff.", display, line)
	} else {
		fmt.Fprintf(&b, "\nBefore retrying the anchored edit on %s, refresh evidence with a bounded source read or path diff.", display)
	}

	return b.String()
}

func syntaxErrorLine(err error) int {
	var list goscan.ErrorList

	if errors.As(err, &list) && len(list) > 0 {
		return list[0].Pos.Line
	}

	var scanErr *goscan.Error

	if errors.As(err, &scanErr) {
		return scanErr.Pos.Line
	}

	return 0
}

func plannedWriteLineContext(contents []byte, line int, radius int) string {
	if line <= 0 {
		return ""
	}

	lines := strings.Split(string(contents), "\n")
	if line > len(lines) {
		return ""
	}

	if radius < 0 {
		radius = 0
	}

	start := line - radius
	if start < 1 {
		start = 1
	}

	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder

	b.WriteString("planned content around syntax error:\n")

	for i := start; i <= end; i++ {
		marker := " "
		if i == line {
			marker = ">"
		}

		fmt.Fprintf(&b, "%s %d:%s\n", marker, i, util.TruncateBytes(strings.TrimRight(lines[i-1], "\r\n"), searchContentLineCap, "...[line truncated]"))
	}

	return strings.TrimRight(b.String(), "\n")
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
		return toolPermissionPolicyResult("shell", "Shell request rejected", "shell execution is disabled")
	}

	workdir, err := m.resolveWorkdir(req.Workdir)
	if err != nil {
		return toolPathResolutionFailureResult("shell", "Failed to resolve workdir", err)
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return toolInvalidArgumentResult("shell", "Shell request rejected", "command is required")
	}

	if isDirectoryOnlyShellCommand(command) {
		return withToolFailure(ToolResult{
			Tool:    "shell",
			Summary: "Shell request rejected",
			Error:   "directory changes inside shell commands do not persist; set workdir or include the command to run in that directory",
		}, failures.InvalidArgument, reasonToolInvalidArgument)
	}

	if m.Mode() == ModeExplore {
		if err := validateExploreShellCommand(command); err != nil {
			return withToolFailure(ToolResult{
				Tool:    "shell",
				Summary: "Shell request rejected",
				Error:   "explore mode only allows read-only inspection shell commands: " + err.Error(),
			}, failures.PermissionDenied, reasonToolPermissionPolicy)
		}
	}

	stdout, stderr, execErr := m.runCommandDetailed(ctx, workdir, m.cfg.Shell, "-lc", command)
	rawOutput := combineCommandOutput(stdout, stderr)
	output := m.truncateCommandOutput(rawOutput)
	result := ToolResult{
		Tool:    "shell",
		Summary: fmt.Sprintf("Ran shell command in %s", m.displayPath(workdir)),
		Output:  "$ " + command + "\n" + strings.TrimSpace(output),
	}

	if execErr != nil {
		result.Error = execErr.Error()
	} else if reason := zeroSelectedTestFailureReason(command, rawOutput); reason != "" {
		result.Error = reason
	} else if m.Mode() != ModeExplore {
		m.invalidateGoIndex()
		m.invalidateCppIndex()
	}

	return result
}

func zeroSelectedTestFailureReason(command string, output string) string {
	if !shellCommandLooksLikeTest(command) {
		return ""
	}

	if signal := zeroSelectedTestSignal(output); signal != "" {
		return "verification command selected zero tests despite exiting 0 (" + signal + "). Check the test filter/name and rerun a command that executes at least one test."
	}

	return ""
}

func shellCommandLooksLikeTest(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}

	for _, needle := range []string{
		"go test",
		"cargo test",
		"npm test",
		"yarn test",
		"pnpm test",
		"pytest",
		"doctest",
		"ctest",
		"jest",
		"vitest",
		"colony_tests",
		"run_tests.py",
		"run_tests.sh",
		"run_tests",
		"-tc=",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}

	return false
}

func zeroSelectedTestSignal(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "test cases: 0 |"):
		return "doctest reported test cases: 0"
	case strings.Contains(lower, "0 tests selected"):
		return "reported 0 tests selected"
	case strings.Contains(lower, "collected 0 items"):
		return "pytest collected 0 items"
	case strings.Contains(lower, "no tests ran"):
		return "reported no tests ran"
	case strings.Contains(lower, "ran 0 tests"):
		return "reported ran 0 tests"
	default:
		return ""
	}
}

func isDirectoryOnlyShellCommand(command string) bool {
	segments := splitShellSegments(command)
	if len(segments) == 0 {
		return false
	}

	for _, segment := range segments {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "cd", "pushd", "popd":
			continue
		default:
			return false
		}
	}

	return true
}

func (m *Manager) runCommand(parent context.Context, dir, name string, args ...string) (string, error) {
	stdout, stderr, err := m.runCommandDetailed(parent, dir, name, args...)
	output := m.truncateCommandOutput(combineCommandOutput(stdout, stderr))

	if err != nil {
		return output, err
	}

	return output, nil
}

func combineCommandOutput(stdout string, stderr string) string {
	output := strings.TrimSpace(stdout)
	errText := strings.TrimSpace(stderr)

	if errText != "" {
		if output != "" {
			output += "\n"
		}

		output += errText
	}

	return output
}

func (m *Manager) truncateCommandOutput(output string) string {
	if m.cfg.MaxOutputBytes > 0 && len(output) > m.cfg.MaxOutputBytes {
		return output[:m.cfg.MaxOutputBytes] + "\n...[truncated]"
	}

	return output
}

func (m *Manager) runCommandDetailed(parent context.Context, dir, name string, args ...string) (string, string, error) {
	return m.runCommandDetailedWithEnv(parent, dir, nil, name, args...)
}

func (m *Manager) runCommandDetailedWithEnv(parent context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error) {
	timeoutSeconds := workspaceCommandTimeoutSeconds(m.cfg.CommandTimeoutSeconds)
	timeout := timeDurationSeconds(timeoutSeconds)
	ctx, cancel := context.WithTimeout(parent, timeout)

	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)

	cmd.Dir = dir
	cmd.Env = envWithCanonicalPWD(os.Environ(), dir, extraEnv)

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("command timed out after %ds", timeoutSeconds)
	}

	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("command failed: %w", err)
	}

	return stdout.String(), stderr.String(), nil
}

func workspaceCommandTimeoutSeconds(seconds int) int {
	if seconds <= 0 {
		return 120
	}

	return seconds
}

func envWithCanonicalPWD(base []string, dir string, extra []string) []string {
	env := append([]string(nil), base...)

	env = append(env, extra...)
	dir = strings.TrimSpace(dir)

	if dir == "" {
		return env
	}

	env = setEnvValue(env, "PWD", config.CanonicalizeExistingPath(dir))

	return env
}

func setEnvValue(env []string, key string, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false

	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}

			continue
		}

		out = append(out, entry)
	}

	if !replaced {
		out = append(out, prefix+value)
	}

	return out
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
	if strings.TrimSpace(req.Path) != "" {
		pathFromRequest, err := m.resolvePath(req.Path, req.Workdir)
		if err != nil {
			return "", "", err
		}

		if strings.TrimSpace(req.ID) != "" {
			pathFromHandle, err := m.pathForHandle(req.ID)
			if err == nil && filepath.Clean(pathFromHandle) == filepath.Clean(pathFromRequest) {
				return pathFromHandle, m.handleDisplay(pathFromHandle), nil
			}

			// Models sometimes combine a stale file handle with the correct
			// explicit path after a new scratchpad/output path has been
			// introduced. For read-only file access, the explicit path is the
			// safer user-visible target; using the handle would read the wrong
			// file, and rejecting the call commonly causes an identical retry.
			return m.resolveExplicitReadPath(pathFromRequest)
		}

		return m.resolveExplicitReadPath(pathFromRequest)
	}

	if strings.TrimSpace(req.ID) != "" {
		path, err := m.pathForHandle(req.ID)
		if err != nil {
			return "", "", err
		}

		return path, m.handleDisplay(path), nil
	}

	return "", "", fmt.Errorf("read_file requires id or path")
}

func (m *Manager) resolveExplicitReadPath(path string) (string, string, error) {
	if !m.hasDiscoveredPath(path) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return "", "", fmt.Errorf("%s", readFileRecoveryError(
				fmt.Sprintf("path %q was not found.", m.displayPath(path)),
				m.notFoundRecoveryHint(path),
			))
		}

		if info.IsDir() {
			return "", "", fmt.Errorf("%s", readFileRecoveryError(fmt.Sprintf("path %q is a directory and cannot be read as a file.", m.displayPath(path))))
		}

		m.rememberPath(path)
	}

	return path, m.handleDisplay(path), nil
}

const readFileRecoveryCapabilities = "Available recovery capabilities: file-name search, structured symbol/code navigation, or exact text search followed by a returned handle"

func readFileRecoveryError(base string, facts ...string) string {
	parts := []string{strings.TrimSpace(base)}
	for _, fact := range facts {
		fact = strings.TrimSpace(fact)
		if fact != "" {
			parts = append(parts, fact)
		}
	}

	parts = append(parts, readFileRecoveryCapabilities)

	return strings.Join(parts, " ")
}

func (m *Manager) closestRecoverableReadPath(query string, workdir string) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" || !m.Enabled() {
		return "", false
	}

	queryKey := normalizedReadPathBasename(query)
	if queryKey == "" {
		return "", false
	}

	queryParent := normalizedReadPathParent(query)
	suggestions := m.findPathSuggestions(query, 5)

	var match string

	for _, suggestion := range suggestions {
		candidate := suggestedWorkspacePath(suggestion)
		if candidate == "" || normalizeWorkspaceSlash(candidate) == normalizeWorkspaceSlash(query) {
			continue
		}

		if normalizedReadPathParent(candidate) != queryParent {
			continue
		}

		if normalizedReadPathBasename(candidate) != queryKey {
			continue
		}

		resolved, err := m.resolvePath(candidate, workdir)
		if err != nil {
			continue
		}

		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			continue
		}

		display := m.displayPath(resolved)
		if match != "" && match != display {
			return "", false
		}

		match = display
	}

	if match == "" {
		return "", false
	}

	return match, true
}

func suggestedWorkspacePath(suggestion string) string {
	fields := strings.Fields(strings.TrimSpace(suggestion))
	if len(fields) >= 2 && isFileHandleToken(fields[0]) {
		return strings.Join(fields[1:], " ")
	}

	return strings.TrimSpace(suggestion)
}

func isFileHandleToken(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != 'f' {
		return false
	}

	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func normalizedReadPathBasename(path string) string {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(strings.TrimSpace(path))))

	var b strings.Builder

	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}

	return b.String()
}

func normalizedReadPathParent(path string) string {
	path = normalizeWorkspaceSlash(path)

	parent := filepath.ToSlash(filepath.Dir(path))

	if parent == "." {
		return ""
	}

	return strings.ToLower(parent)
}

func normalizeWorkspaceSlash(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	return strings.TrimPrefix(path, "./")
}

// notFoundRecoveryHint inspects the parent directory of a missing
// path and returns a one-line hint pointing at the closest
// existing entries by name (case-insensitive substring match).
// Generic — purely string-similarity over directory entries; no
// project-specific assumptions about path layout.
//
// Returns "" when the parent doesn't exist or has no useful
// suggestions, so callers can concatenate the hint without worrying
// about empty messages dangling.
func (m *Manager) notFoundRecoveryHint(path string) string {
	if path == "" {
		return ""
	}

	parent := filepath.Dir(path)
	if parent == "" || parent == "." || parent == path {
		return ""
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		grandparent := filepath.Dir(parent)
		if grandparent != "" && grandparent != parent {
			return fmt.Sprintf("Parent directory %q does not exist either; closest existing parent: %q.", m.displayPath(parent), m.displayPath(grandparent))
		}

		return ""
	}

	target := strings.ToLower(filepath.Base(path))
	if target == "" {
		return ""
	}

	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		candidates = append(candidates, e.Name())
	}

	suggestions := closestToolNames(target, candidates, 3)
	if len(suggestions) == 0 {
		return fmt.Sprintf("Parent directory %q exists but contains no matching name.", m.displayPath(parent))
	}

	quoted := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		quoted = append(quoted, fmt.Sprintf("%q", filepath.Join(m.displayPath(parent), s)))
	}

	return fmt.Sprintf("Closest existing entries in %q: %s.", m.displayPath(parent), strings.Join(quoted, ", "))
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

	if rel, ok := m.relativeWorkspacePath(path); ok && rel != "." {
		return rel
	}

	return path
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
		slices.Sort(suggestions)

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

	slices.SortFunc(scoredList, func(a, b scored) int {
		if c := cmp.Compare(a.priority, b.priority); c != 0 {
			return c
		}

		if c := cmp.Compare(a.distance, b.distance); c != 0 {
			return c
		}

		return cmp.Compare(a.path, b.path)
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

	slices.Sort(langs)

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

	slices.Sort(out)

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
// ("internal/runtime/agent/ (5 files)") for the real directories that share the
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

			curr[i] = min(del, ins, sub)
		}

		prev, curr = curr, prev
	}

	return prev[la]
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
