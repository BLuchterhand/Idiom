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

	"idiom/internal/config"
	"idiom/internal/discovery"
	"idiom/internal/mcp"
	"idiom/internal/semantic"
)

var exploreBlockedTools = map[string]string{
	"go_move_declarations": "moving declarations",
	"mkdir":                "creating directories",
	"edit_file":            "editing files",
	"write_file":           "writing files",
	"append_file":          "appending files",
	"apply_patch":          "applying patches",
}

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
	mcpRegistry                *mcp.Registry
	externalToolsRegistry      *ExternalToolRegistry
	goIndex                    *semantic.GoIndex
	goIndexToken               string
	cppIndex                   *semantic.CppIndex
	cppIndexToken              string
	goplsPath                  string
	goplsChecked               bool
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

type ToolRequest struct {
	Tool              string              `json:"tool"`
	ID                string              `json:"id,omitempty"`
	Handle            string              `json:"handle,omitempty"`
	Path              string              `json:"path,omitempty"`
	StartLine         int                 `json:"start_line,omitempty"`
	EndLine           int                 `json:"end_line,omitempty"`
	Pattern           string              `json:"pattern,omitempty"`
	Query             string              `json:"query,omitempty"`
	Symbol            string              `json:"symbol,omitempty"`
	Task              string              `json:"task,omitempty"`
	Purpose           string              `json:"purpose,omitempty"`
	Capability        string              `json:"capability,omitempty"`
	Language          string              `json:"language,omitempty"`
	Glob              string              `json:"glob,omitempty"`
	Workdir           string              `json:"workdir,omitempty"`
	Command           string              `json:"command,omitempty"`
	Prompt            string              `json:"prompt,omitempty"`
	Agent             string              `json:"agent,omitempty"`
	Mode              string              `json:"mode,omitempty"`
	Content           string              `json:"content,omitempty"`
	OldText           string              `json:"old_text,omitempty"`
	NewText           string              `json:"new_text,omitempty"`
	ReplaceAll        bool                `json:"replace_all,omitempty"`
	Recursive         bool                `json:"recursive,omitempty"`
	MaxDepth          int                 `json:"max_depth,omitempty"`
	Limit             int                 `json:"limit,omitempty"`
	BudgetTokens      int                 `json:"budget_tokens,omitempty"`
	Resolution        string              `json:"resolution,omitempty"`
	IncludeEvidence   bool                `json:"include_evidence,omitempty"`
	IncludeSource     bool                `json:"include_source,omitempty"`
	IncludeFiles      bool                `json:"include_files,omitempty"`
	Refresh           bool                `json:"refresh,omitempty"`
	FreshnessPolicy   string              `json:"freshness_policy,omitempty"`
	FreshnessMaxFiles int                 `json:"freshness_max_files,omitempty"`
	Category          string              `json:"category,omitempty"`
	Component         string              `json:"component,omitempty"`
	Expected          string              `json:"expected,omitempty"`
	Observed          string              `json:"observed,omitempty"`
	Workaround        string              `json:"workaround,omitempty"`
	Impact            string              `json:"impact,omitempty"`
	Evidence          []string            `json:"evidence,omitempty"`
	Confidence        string              `json:"confidence,omitempty"`
	Severity          string              `json:"severity,omitempty"`
	SuggestedFix      string              `json:"suggested_fix,omitempty"`
	SessionID         string              `json:"session_id,omitempty"`
	FailureID         string              `json:"failure_id,omitempty"`
	PacketPath        string              `json:"packet_path,omitempty"`
	OutputPath        string              `json:"output_path,omitempty"`
	RequiredTests     []string            `json:"required_tests,omitempty"`
	RequiredEditPaths []string            `json:"required_edit_paths,omitempty"`
	ForbiddenPaths    []string            `json:"forbidden_paths,omitempty"`
	Destination       string              `json:"destination,omitempty"`
	Declarations      []string            `json:"declarations,omitempty"`
	Moves             []GoDeclarationMove `json:"moves,omitempty"`
	Todos             []TaskLedgerItem    `json:"todos,omitempty"`
	Items             []TaskLedgerItem    `json:"items,omitempty"`
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
	// Edits carries the structured edit list for a submit_plan call
	// (the planner phase of plan-then-execute mode). Each entry is
	// one edit the executor phase should dispatch in order.
	Edits []PlanEdit `json:"edits,omitempty"`
	// Rationale is the planner's free-text explanation of WHY the
	// plan looks the way it does. Captured alongside the plan so the
	// executor (and any reviewer) can read the plan as a single
	// document.
	Rationale string `json:"rationale,omitempty"`
	// PackID is the content-addressed handle of the comprehension
	// graph_views cache row the planner used when drafting the plan.
	// Optional — when present, the executor can rehydrate the
	// planner's packet via comprehension.LoadView instead of
	// re-discovering the same files.
	PackID string `json:"pack_id,omitempty"`
	// PlanArtifact carries the durable structured submit_plan schema.
	// It is optional so legacy callers can keep sending rationale,
	// pack_id, and edits at the top level; submit_plan normalizes that
	// legacy shape into the same persisted artifact contract.
	PlanArtifact *PlanArtifact `json:"plan_artifact,omitempty"`
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
	PrecisionTier string             `json:"precision_tier,omitempty"`
	Relationships []ToolRelationship `json:"relationships,omitempty"`
}

type ToolRelationship struct {
	Kind         string `json:"kind,omitempty"`
	SourcePath   string `json:"source_path,omitempty"`
	SourceLine   int    `json:"source_line,omitempty"`
	SourceSymbol string `json:"source_symbol,omitempty"`
	TargetPath   string `json:"target_path,omitempty"`
	TargetLine   int    `json:"target_line,omitempty"`
	TargetSymbol string `json:"target_symbol,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
}

type listedEntry struct {
	path  string
	isDir bool
}

const searchContentLineCap = 240
const listFilesDisplayCap = 80

func NewManager(cfg config.Workspace) *Manager {
	cfg.Root = config.CanonicalizeExistingPath(cfg.Root)
	cfg.CWD = config.CanonicalizeExistingPath(cfg.CWD)
	return &Manager{
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
			"- Do not invent file paths from memory. Resolve a path-discovery capability, discovered handle, or structured symbol/navigation route before naming a new file.\n"+
			"- After an orientation call, narrow with structured language-aware routes or discovered handles first. Exact-text search and bounded reads are fallback capabilities for prose, config, or ranges a semantic probe cannot cover.\n"+
			"- Paths are relative to the working directory unless absolute.\n"+
			"- Prefer anchored edit capabilities for small localized changes; whole-file writes overwrite.\n"+
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
		result = annotateLanguagePrecisionTier(result)
		result = annotateLanguageRelationships(result)
	}()
	timeout := toolTimeoutForRequest(req)
	if timeout <= 0 {
		return m.executeDispatch(ctx, req)
	}
	return m.executeWithTimeout(ctx, req, timeout)
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
			}
		}
		return ToolResult{
			Tool:    req.Tool,
			Summary: "Tool canceled",
			Error:   "the run was canceled before this tool returned",
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
	switch tool {
	// Indexing/build paths can legitimately take a couple of minutes on
	// a cold repo or after a major refactor.
	case "context_index", "context_build":
		return 5 * time.Minute
	// Security scans are heavy by design.
	case "semgrep_scan", "joern_scan":
		return 5 * time.Minute
	// Trace runs execute a user-supplied command — the caller is
	// expected to keep that bounded but we still allow generous time.
	case "context_trace_pack":
		return 5 * time.Minute
	// Universal Ctags + LSP-backed queries can be slow on big files.
	case "ctags_symbols":
		return 90 * time.Second
	// Shell commands are user-bounded; the workspace already enforces
	// CommandTimeoutSeconds inside the shell handler. Skip the outer
	// timeout to avoid double-bounding.
	case "shell":
		return 0
	}
	return 30 * time.Second
}

func (m *Manager) executeDispatch(ctx context.Context, req ToolRequest) (result ToolResult) {
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
	case "file_stats":
		return m.fileStats(req)
	case "code_navigate":
		return m.codeNavigate(ctx, req)
	case "read_only_capability":
		return m.readOnlyCapability(ctx, req)
	case "source_sanity":
		return m.sourceSanity(ctx, req)
	case "harness_audit":
		return m.harnessAudit(req)
	case "harness_feedback", "idiom_feedback":
		return m.harnessFeedback(req)
	case "recovery_inspect":
		return m.recoveryInspect(ctx, req)
	case "recovery_status":
		return m.recoveryStatus(ctx, req)
	case "recovery_decompose":
		return m.recoveryDecompose(ctx, req)
	case "submit_plan":
		return m.submitPlan(req)
	case "context_index":
		return m.contextIndex(ctx, req)
	case "context_status":
		return m.contextStatus(ctx, req)
	case "context_query":
		return m.contextQuery(ctx, req)
	case "context_zoom":
		return m.contextZoom(ctx, req)
	case "context_pack":
		return m.contextPack(ctx, req)
	case "context_impact":
		return m.contextImpact(ctx, req)
	case "context_trace_pack":
		return m.contextTracePack(ctx, req)
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
		if blocked, ok := exploreBlockedTools[req.Tool]; ok && mode == ModeExplore {
			return ToolResult{Tool: req.Tool, Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before " + blocked}
		}
		return m.goMoveDeclarations(req)
	case "mkdir":
		if blocked, ok := exploreBlockedTools[req.Tool]; ok && mode == ModeExplore {
			return ToolResult{Tool: req.Tool, Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before " + blocked}
		}
		return m.makeDir(req)
	case "edit_file":
		if blocked, ok := exploreBlockedTools[req.Tool]; ok && mode == ModeExplore {
			return ToolResult{Tool: req.Tool, Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before " + blocked}
		}
		return m.editFile(req)
	case "write_file":
		if blocked, ok := exploreBlockedTools[req.Tool]; ok && mode == ModeExplore {
			return ToolResult{Tool: req.Tool, Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before " + blocked}
		}
		return m.writeFile(req, false)
	case "append_file":
		if blocked, ok := exploreBlockedTools[req.Tool]; ok && mode == ModeExplore {
			return ToolResult{Tool: req.Tool, Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before " + blocked}
		}
		return m.writeFile(req, true)
	case "apply_patch":
		if blocked, ok := exploreBlockedTools[req.Tool]; ok && mode == ModeExplore {
			return ToolResult{Tool: req.Tool, Summary: "Tool request rejected", Error: "explore mode is read-only; leave explore mode before " + blocked}
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
		// Generic recovery message: list nearby-named tools so the
		// model can self-correct after a typo or hallucinated name.
		// Helpful across any project — the suggestion list is built
		// from the live tool surface (built-ins + external + MCP).
		return ToolResult{
			Tool:    tool,
			Summary: "Tool request rejected",
			Error:   m.unsupportedToolError(tool),
		}
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
		return ToolResult{Tool: req.Tool, Summary: "External tools unavailable", Error: "external tools registry not configured"}
	}
	if !registry.Has(req.Tool) {
		return ToolResult{Tool: req.Tool, Summary: "Unknown external tool", Error: "external tool not registered: " + req.Tool}
	}
	if ok, reason := registry.Availability(req.Tool, m.externalToolBaseDir()); !ok {
		return ToolResult{Tool: req.Tool, Summary: "External tool unavailable", Error: "external tool " + req.Tool + " unavailable: " + reason}
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
		var err error
		payload, ok, err = extractQwenXMLToolPayload(text)
		if !ok {
			return ToolRequest{}, "", false, nil
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

// DecodeToolRequestJSON decodes a built-in tool request payload. It accepts a
// small, safe repair surface for common model JSON-schema slips: typed fields
// emitted as decimal/boolean strings, stringified non-plan structured fields,
// and singleton values for array-only fields. Native tool-calls and fenced JSON
// requests both route through this helper so repair behavior stays consistent.
func DecodeToolRequestJSON(payload []byte) (ToolRequest, error) {
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

func stringifiedStructuredToolFields(payload []byte) map[string]string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, field := range []string{
		"edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths",
		"declarations", "moves", "todos", "items", "plan_artifact",
	} {
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
	if _, ok := fields["edits"]; ok && len(req.Edits) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("edits", fields["edits"])
	}
	if _, ok := fields["required_tests"]; ok && len(req.RequiredTests) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("required_tests", fields["required_tests"])
	}
	if _, ok := fields["required_edit_paths"]; ok && len(req.RequiredEditPaths) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("required_edit_paths", fields["required_edit_paths"])
	}
	if _, ok := fields["forbidden_paths"]; ok && len(req.ForbiddenPaths) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("forbidden_paths", fields["forbidden_paths"])
	}
	if _, ok := fields["declarations"]; ok && len(req.Declarations) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("declarations", fields["declarations"])
	}
	if _, ok := fields["moves"]; ok && len(req.Moves) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("moves", fields["moves"])
	}
	if _, ok := fields["todos"]; ok && len(req.Todos) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("todos", fields["todos"])
	}
	if _, ok := fields["items"]; ok && len(req.Items) == 0 {
		return !stringifiedStructuredFieldRepresentsEmptyValue("items", fields["items"])
	}
	if _, ok := fields["plan_artifact"]; ok && req.PlanArtifact == nil {
		return !stringifiedStructuredFieldRepresentsEmptyValue("plan_artifact", fields["plan_artifact"])
	}
	return false
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
	case "edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths", "declarations", "moves", "todos", "items":
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(value), &arr); err != nil {
			return false
		}
		return len(arr) == 0
	default:
		return false
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
			repairStringifiedJSONToolFields,
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

func repairStringifiedJSONToolFields(payload []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}
	repaired := false
	for _, field := range []string{
		"edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths",
		"declarations", "moves", "todos", "items", "plan_artifact",
	} {
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

func stringifiedStructuredFieldHasTopLevelJSONStart(field string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	switch field {
	case "plan_artifact":
		return value[0] == '{'
	case "edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths", "declarations", "moves", "todos", "items":
		return value[0] == '[' || value[0] == '{'
	default:
		return false
	}
}

func extractLeadingStringifiedJSONToolFieldValue(field string, value string) (json.RawMessage, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	allowedStarts := "{"
	switch field {
	case "plan_artifact":
		allowedStarts = "{"
	case "edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths", "declarations", "moves", "todos", "items":
		allowedStarts = "[{"
	default:
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
	switch field {
	case "plan_artifact":
		return extractBalancedJSONToolValue(value, "{")
	case "edits", "evidence", "required_tests", "required_edit_paths", "forbidden_paths", "declarations", "moves", "todos", "items":
		return extractBalancedJSONToolValue(value, "[{")
	default:
		return nil, false
	}
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
	for _, field := range []string{"edits", "moves", "todos", "items"} {
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
	for _, field := range []string{"evidence", "required_tests", "required_edit_paths", "forbidden_paths", "declarations"} {
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

	if payload, ok := extractPlainToolPrefixPayload(trimmed); ok {
		return payload, true
	}

	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"tool"`) {
		if object, ok := extractFirstJSONObject(trimmed); ok {
			return object, true
		}
		return trimmed, true
	}

	return "", false
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
	qwenXMLFunctionPattern  = regexp.MustCompile(`(?s)^<function=([^>\s]+)>(.*)</function>$`)
	qwenXMLParameterPattern = regexp.MustCompile(`(?s)<parameter=([^>\s]+)>(.*?)</parameter>`)
)

func extractQwenXMLToolPayload(text string) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (!strings.Contains(trimmed, "<function=") && !strings.Contains(trimmed, "<tool_call>")) {
		return "", false, nil
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
	return extractQwenXMLFunctionPayload(envelope)
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
	if strings.HasPrefix(text, "<tool_call>") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "<tool_call>"))
		text = strings.TrimSpace(strings.TrimSuffix(text, "</tool_call>"))
		return text, true
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
	if strings.HasPrefix(value, "\n") {
		value = value[1:]
	}
	if strings.HasSuffix(value, "\n") {
		value = value[:len(value)-1]
	}
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
		"code_navigate", "read_only_capability", "source_sanity", "harness_audit", "harness_feedback", "idiom_feedback", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "context_trace_pack", "discovery_query",
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
		"code_navigate", "read_only_capability", "source_sanity", "harness_audit", "harness_feedback", "idiom_feedback", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "context_trace_pack", "discovery_query",
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
		slices.SortFunc(matches, func(a, b searchFileMatch) int { return cmp.Compare(a.display, b.display) })
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
		errMsg := err.Error() + ". Verify the path with search_files, a structured symbol/navigation route, or search_content before retrying read_file."
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
			Tool:    "read_file",
			Summary: fmt.Sprintf("Read %s lines 0-0 (total lines: 0)", description),
			Output:  "",
		}
	}
	start, end, clampedToFileBounds := clampRequestedLineRange(len(lines), start, end, requestedStart, requestedEnd)
	if start > end {
		return ToolResult{Tool: "read_file", Summary: "Invalid line range", Error: "start_line must be <= end_line"}
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
			line = truncateSearchContentLine(line, defaultLogLineCharCap)
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
		line = truncateSearchContentLine(line, defaultLogLineCharCap)
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
	var total int64
	var last byte
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
	if err := validateSymbolPattern("go_read_symbol", pattern); err != nil {
		return ToolResult{Tool: "go_read_symbol", Summary: "Go symbol read rejected", Error: err.Error()}
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
		summary = fmt.Sprintf("%s (truncated from lines %d-%d; full-body adapter: go_extract_body)", summary, match.StartLine, match.EndLine)
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
			fallback := m.goFindSymbol(req)
			fallback.Tool = "go_workspace_symbol"
			if fallback.Error == "" && strings.TrimSpace(fallback.Output) != "" {
				fallback.Summary = "Resolved Go workspace symbols via semantic fallback after empty gopls result"
				return fallback
			}
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
	slices.Sort(lines)
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
	failures := make([]string, 0)
	if m.goplsBinary() != "" {
		compiled := make(map[string]struct{})
		for _, rel := range files {
			output, err := m.runGoPLSCommand(ctx, "check", rel)
			output = m.formatGoLocationOutput(output)
			if err != nil {
				line := fmt.Sprintf("%s: %s", rel, err.Error())
				if strings.TrimSpace(output) != "" {
					line += "\n" + output
				}
				lines = append(lines, line)
				failures = append(failures, line)
			} else if strings.TrimSpace(output) == "" {
				lines = append(lines, fmt.Sprintf("%s: no warnings or errors", rel))
			} else {
				lines = append(lines, output)
				failures = append(failures, output)
			}
			if pkg, ok := goTestCompilePackageForDiagnosticFile(rel); ok {
				if _, seen := compiled[pkg]; seen {
					continue
				}
				compiled[pkg] = struct{}{}
				line, compileErr := m.goTestCompileDiagnostic(ctx, pkg)
				lines = append(lines, line)
				if compileErr != nil {
					failures = append(failures, line)
				}
			}
		}
		result := ToolResult{
			Tool:    "go_diagnostics",
			Summary: fmt.Sprintf("Collected Go diagnostics for %d file(s).", len(files)),
			Output:  strings.Join(lines, "\n"),
		}
		if len(failures) > 0 {
			result.Error = strings.Join(failures, "\n")
		}
		return result
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
	for _, pkg := range goTestCompilePackagesForDiagnosticFiles(files) {
		line, compileErr := m.goTestCompileDiagnostic(ctx, pkg)
		if strings.TrimSpace(result.Output) == "" {
			result.Output = line
		} else {
			result.Output += "\n" + line
		}
		if compileErr != nil {
			if strings.TrimSpace(result.Error) == "" {
				result.Error = line
			} else {
				result.Error += "\n" + line
			}
		}
	}
	return result
}

func goTestCompilePackagesForDiagnosticFiles(files []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, file := range files {
		pkg, ok := goTestCompilePackageForDiagnosticFile(file)
		if !ok {
			continue
		}
		if _, exists := seen[pkg]; exists {
			continue
		}
		seen[pkg] = struct{}{}
		out = append(out, pkg)
	}
	slices.Sort(out)
	return out
}

func goTestCompilePackageForDiagnosticFile(file string) (string, bool) {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if !strings.HasSuffix(strings.ToLower(filepath.Base(file)), "_test.go") {
		return "", false
	}
	dir := filepath.ToSlash(filepath.Dir(file))
	if dir == "" || dir == "." {
		return ".", true
	}
	return "./" + strings.TrimPrefix(dir, "./"), true
}

func (m *Manager) goTestCompileDiagnostic(ctx context.Context, pkg string) (string, error) {
	output, err := m.runCommand(ctx, m.cfg.Root, "go", "test", pkg, "-run", "^$")
	output = strings.TrimSpace(output)
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return fmt.Sprintf("%s: go test compile failed\n%s", pkg, output), err
	}
	if output == "" {
		output = "ok"
	}
	return fmt.Sprintf("%s: go test compile passed\n%s", pkg, output), nil
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
	lines := splitWorkspaceFileLines(data)
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

func splitWorkspaceFileLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
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
		slices.Sort(files)
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
	} else if current, err := os.ReadFile(path); err == nil {
		if IsCompletedPlanningArtifact(path, current) {
			return ToolResult{
				Tool:    toolName,
				Summary: "Write request rejected",
				Error:   fmt.Sprintf("write_file refuses to overwrite completed planning artifact %s. The available contract outcomes are a bounded anchored update or explicit user approval to reopen/replace the completed artifact.", m.displayPath(path)),
			}
		}
	} else if !os.IsNotExist(err) {
		return ToolResult{Tool: toolName, Summary: "Failed to read existing file", Error: err.Error()}
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
	if rejected := m.rejectPendingEditRecovery("edit_file", path); rejected != nil {
		return *rejected
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
		if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.OldText) != "" {
			// Some model/tool-call adapters include line metadata alongside
			// the exact-match shape. Prefer the explicit old_text/new_text
			// replacement instead of treating empty line-range content as a
			// deletion or ignoring new_text.
		} else if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.NewText) != "" {
			return ToolResult{
				Tool:    "edit_file",
				Summary: "Edit request rejected",
				Error: fmt.Sprintf(
					"edit_file line-range edits use the `content` field, not `new_text`. "+
						"Either set content to the replacement lines for %s lines %d-%d, "+
						"or provide old_text with new_text and omit the line range.",
					m.displayPath(path), req.StartLine, req.EndLine,
				),
			}
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
		// whole-file replacement remains a separate primitive for new, small,
		// or non-source deliverables.
		display := m.displayPath(path)
		hasContent := strings.TrimSpace(req.Content) != ""
		var example string
		if hasContent && editRecoveryPathLooksSourceLike(path) {
			example = fmt.Sprintf(
				"edit_file needs either old_text (for an exact-match replace) or start_line+end_line (for a range replace). "+
					"You provided `content` with neither.\n"+
					"For an existing source/test file, use a bounded edit_file shape instead of whole-file replacement:\n"+
					"  (a) exact-match:  {\"tool\":\"edit_file\",\"path\":%q,\"old_text\":\"<substring present in file>\",\"new_text\":\"<replacement>\",\"description\":\"<one sentence on what this change is and why>\"}\n"+
					"  (b) line range:   {\"tool\":\"edit_file\",\"path\":%q,\"start_line\":<N>,\"end_line\":<M>,\"content\":\"<replacement lines>\",\"description\":\"<one sentence on what this change is and why>\"}\n"+
					"Do not re-submit the same call shape.",
				display, display,
			)
		} else if hasContent {
			example = fmt.Sprintf(
				"edit_file needs either old_text (for an exact-match replace) or start_line+end_line (for a range replace). "+
					"You provided `content` with neither.\n"+
					"If you want to REPLACE THE WHOLE FILE for a new, small, or non-source deliverable, use write_file instead:\n"+
					"  {\"tool\":\"write_file\",\"path\":%q,\"content\":\"<full file contents>\",\"description\":\"<one sentence on what this file is for and why it is being written>\"}\n"+
					"If you want a PARTIAL edit, pick one of these two edit_file shapes:\n"+
					"  (a) exact-match:  {\"tool\":\"edit_file\",\"path\":%q,\"old_text\":\"<substring present in file>\",\"new_text\":\"<replacement>\",\"description\":\"<one sentence on what this change is and why>\"}\n"+
					"  (b) line range:   {\"tool\":\"edit_file\",\"path\":%q,\"start_line\":<N>,\"end_line\":<M>,\"content\":\"<replacement lines>\",\"description\":\"<one sentence on what this change is and why>\"}\n"+
					"Do not re-submit the same call shape.",
				display, display, display,
			)
		} else {
			example = fmt.Sprintf(
				"edit_file requires old_text (and new_text) OR start_line/end_line (and content). You provided neither.\n"+
					"Valid shapes:\n"+
					"  (a) exact-match:  {\"tool\":\"edit_file\",\"path\":%q,\"old_text\":\"<substring present in file>\",\"new_text\":\"<replacement>\",\"description\":\"<one sentence on what this change is and why>\"}\n"+
					"  (b) line range:   {\"tool\":\"edit_file\",\"path\":%q,\"start_line\":<N>,\"end_line\":<M>,\"content\":\"<replacement lines>\",\"description\":\"<one sentence on what this change is and why>\"}",
				display, display,
			)
		}
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: example}
	}

	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		if containsCaseFoldedSubstring(current, req.OldText) {
			return ToolResult{
				Tool:    "edit_file",
				Summary: "Edit request rejected",
				Error:   fmt.Sprintf("old_text did not appear in %s, but a case-insensitive match exists. Preserve exact casing from the file when copying anchors, or switch to the start_line/end_line+content form.", m.displayPath(path)),
			}
		}
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
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "start_line must be >= 1 (line numbers are 1-indexed)."}
	}
	if endLine == 0 {
		endLine = startLine
	}
	if endLine < startLine {
		return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: fmt.Sprintf("end_line (%d) must be >= start_line (%d). Swap them if you meant the other direction, or set end_line=start_line for a single-line edit.", endLine, startLine)}
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
	replacedRange := current[startOffset:endOffset]
	// Empty or whitespace-only content with a line range is the most common
	// edit_file footgun. The model often calls edit_file with a line range
	// thinking "insert after line N" — but the line-range form REPLACES those
	// lines, so blank content silently deletes them. Reject this generically
	// when the target range contains real code/text; intentional deletion can
	// use the exact old_text/new_text form where the removed content is explicit.
	if strings.TrimSpace(req.Content) == "" && strings.TrimSpace(req.NewText) == "" &&
		strings.TrimSpace(replacedRange) != "" {
		return ToolResult{
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
		}
	}
	if lineRangeReplacementDuplicatesPrecedingContext(current, spans, startLine, req.Content) {
		return ToolResult{
			Tool:    "edit_file",
			Summary: "Edit request rejected",
			Error: fmt.Sprintf(
				"line-range replacement for %s lines %d-%d appears to duplicate context immediately before the selected range. "+
					"Line-range content must contain only the final replacement for the selected lines. "+
					"For inserts, use old_text/new_text with a unique existing snippet and append the new content to new_text.",
				m.displayPath(path), startLine, endLine,
			),
		}
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
		return ToolResult{Tool: "edit_file", Summary: "Failed to write edited file", Error: err.Error()}
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
	contextLines := normalizedNonEmptyLines(context)
	replacementLines := normalizedNonEmptyLines(replacement)
	maxCompare := min(len(contextLines), len(replacementLines), 6)
	for n := maxCompare; n >= 3; n-- {
		if equalStringSlices(contextLines[len(contextLines)-n:], replacementLines[:n]) {
			return true
		}
	}
	return false
}

func normalizedNonEmptyLines(text string) []string {
	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func equalStringSlices(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsCaseFoldedSubstring(content string, oldText string) bool {
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return false
	}
	return strings.Contains(strings.ToLower(content), strings.ToLower(oldText))
}

func (m *Manager) rejectInvalidPlannedWrite(toolName string, summary string, path string, contents []byte) *ToolResult {
	if m == nil {
		return nil
	}
	before, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return &ToolResult{
			Tool:    toolName,
			Summary: summary,
			Error:   fmt.Sprintf("preflight could not read existing %s: %s", m.displayPath(path), err.Error()),
		}
	}
	if lineNo, line, ok := NewSuspiciousAdjacentDuplicateCodeLine(path, before, contents); ok {
		return &ToolResult{
			Tool:    toolName,
			Summary: summary,
			Error: fmt.Sprintf(
				"preflight rejected %s because it would introduce adjacent duplicate code line %d: %q. "+
					"Combine adjacent edits into one replacement or include the existing line in old_text instead of re-declaring it.",
				m.displayPath(path), lineNo, line,
			),
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, contents, parser.AllErrors); err != nil {
			line := syntaxErrorLine(err)
			m.noteEditRecoveryRequired(path, line, err.Error())
			return &ToolResult{
				Tool:    toolName,
				Summary: summary,
				Error:   m.formatSyntaxPreflightError(path, contents, line, err),
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
	return &ToolResult{
		Tool:    toolName,
		Summary: "Edit request rejected",
		Error: fmt.Sprintf(
			"previous syntax preflight failure on %s still needs evidence refresh%s. Required recovery evidence is a bounded failing-region read or path diff before retrying the anchored edit. Last validation error: %s",
			display,
			lineHint,
			state.detail,
		),
	}
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
		fmt.Fprintf(&b, "%s %d:%s\n", marker, i, truncateSearchContentLine(lines[i-1], searchContentLineCap))
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
	if isDirectoryOnlyShellCommand(command) {
		return ToolResult{
			Tool:    "shell",
			Summary: "Shell request rejected",
			Error:   "directory changes inside shell commands do not persist; set workdir or include the command to run in that directory",
		}
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
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
			return "", "", fmt.Errorf(
				"path %q was not found. %s Available recovery capabilities: file-name search, structured symbol/navigation, or exact text search followed by a returned handle.",
				m.displayPath(path),
				m.notFoundRecoveryHint(path),
			)
		}
		if info.IsDir() {
			return "", "", fmt.Errorf("path %q is a directory; use list_files or search_files to choose a file", m.displayPath(path))
		}
		m.rememberPath(path)
	}
	return path, m.handleDisplay(path), nil
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
		// Parent itself doesn't exist — suggest exploring further up.
		grandparent := filepath.Dir(parent)
		if grandparent != "" && grandparent != parent {
			return fmt.Sprintf("Parent directory %q does not exist either; try list_files %q first.", m.displayPath(parent), m.displayPath(grandparent))
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
		return fmt.Sprintf("Parent directory %q exists but contains no matching name; try list_files %q to see what's there.", m.displayPath(parent), m.displayPath(parent))
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
