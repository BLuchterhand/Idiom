package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/languagetools"
)

// Manager is the workspace session facade. It owns session configuration and
// composes smaller capability-specific collaborators:
//
//	ToolSurface   — tool manifest, classification, and allowed-tools computation
//	                (see tool_class.go, tool_manifest.go, capabilities.go)
//	PathTracker   — file handle assignment, path observation, and handle resolution
//	LanguageTools — semantic navigation, language indexes, and LSP integration
//	ExternalTools — MCP registry and external tool registration
//	                (see externaltool/)
//	EditRecovery  — edit-recovery state tracking
//
// Tool dispatch, read tools, write tools, shell/command execution, git, parser
// repair, and external/MCP calls live in focused workspace files.
type Manager struct {
	cfg config.Workspace

	mode                 Mode
	thoroughness         ExploreThoroughness
	bypassReadBeforeEdit bool
	mu                   sync.Mutex
	pathTracker          PathTracker
	pathAccess           PathAccess
	writeGuard           WriteGuard
	permissionRules      map[string][]toolpolicy.Rule
	protectedWritePaths  map[string]struct{}
	toolSurface          ToolSurface
	languageTools        atomic.Pointer[languagetools.Manager]
	mutationEpoch        atomic.Uint64
}

type Mode string

const (
	ModeNormal  Mode = "normal"
	ModeExplore Mode = "explore"
)

type ExploreThoroughness string

const (
	ThoroughnessMedium ExploreThoroughness = "medium"
)

func NewManager(cfg config.Workspace) *Manager {
	cfg.Root = config.CanonicalizeExistingPath(cfg.Root)
	cfg.CWD = config.CanonicalizeExistingPath(cfg.CWD)
	cfg.SessionID = strings.TrimSpace(cfg.SessionID)

	m := &Manager{
		cfg:          cfg,
		mode:         ModeNormal,
		thoroughness: ThoroughnessMedium,
		pathTracker:  NewPathTracker(),
		pathAccess:   NewPathAccess(cfg),
		writeGuard:   NewWriteGuard(),
	}

	m.languageTools.Store(languagetools.NewManager(languageToolHost{manager: m}))
	m.mutationEpoch.Store(1)
	m.pathAccess.mutationEpoch = &m.mutationEpoch

	return m
}

// MutationEpoch reports the workspace generation used to invalidate ephemeral
// source windows after any harness-observed mutation.
func (m *Manager) MutationEpoch() uint64 {
	if m == nil {
		return 0
	}
	return m.mutationEpoch.Load()
}

// MarkPathObserved records that a caller has successfully read or enumerated
// the given path during this session. edit_file consults this set to enforce
// the read-before-edit invariant (Fix 1 in docs/opencode-fixes-design.md).
// Passing an empty path is a no-op.
func (m *Manager) MarkPathObserved(path string) {
	if m == nil {
		return
	}

	m.pathTracker.MarkObserved(path)
}

// HasObservedPath reports whether MarkPathObserved has been called with the
// given path (or any case/separator variation of it) during this session.
func (m *Manager) HasObservedPath(path string) bool {
	if m == nil {
		return false
	}

	return m.pathTracker.HasObserved(path)
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

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.cfg.SessionID
}

func (m *Manager) SetSessionID(sessionID string) {
	if m == nil {
		return
	}

	sessionID = strings.TrimSpace(sessionID)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg.SessionID = sessionID
}

func (m *Manager) Enabled() bool {
	return m != nil && m.pathAccess.Enabled()
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
			"- Write policy is guarded: prefer anchored edits for existing files. Avoid whole-file rewrites of existing files, avoid state-changing shell commands for edits, and only create/append/mkdir when the user clearly requested that broader write scope.\n"+
			"- shell runs with the same OS permissions as this process. Prefer the dedicated tools where one exists.\n"+
			"- After tool results, either answer directly or take one exact evidence step tied to a named gap.",
		m.cfg.Root,
		m.cfg.CWD,
	)
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
		m.exploreThoroughness(),
	)
}

func (m *Manager) exploreThoroughness() ExploreThoroughness {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.thoroughness
}

func (m *Manager) toolSurfaceManifestOptions(readOnly bool) ToolSurfaceManifestOptions {
	return ToolSurfaceManifestOptions{
		ReadOnly:          readOnly,
		IncludeGo:         m.languageDetects("go"),
		IncludeCpp:        m.languageDetects("cpp"),
		IncludePython:     m.languageDetects("python"),
		IncludeTypescript: m.languageDetects("typescript"),
		IncludeRust:       m.languageDetects("rust"),
		IncludeJava:       m.languageDetects("java"),
	}
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

const (
	PrecisionTierLSP         = "lsp"
	PrecisionTierSyntaxIndex = "syntax-index"
	PrecisionTierRGFallback  = "rg-fallback"
	PrecisionTierUnavailable = "unavailable"
)

func annotateLanguagePrecisionTier(result ToolResult) ToolResult {
	if strings.TrimSpace(result.PrecisionTier) != "" || !languagetools.IsToolName(result.Tool) {
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

func (m *Manager) resolveReadTarget(req ToolRequest) (string, string, error) {
	if strings.TrimSpace(req.Path) != "" {
		pathFromRequest, err := m.pathAccess.ResolveReadPath(req.executionSessionIdentity, req.Path, req.Workdir)
		if err != nil {
			if resolved, ok := resolveUserGlobalConversationLogReadPath(req.Path); ok {
				pathFromRequest = resolved
			} else {
				return "", "", err
			}
		}

		if strings.TrimSpace(req.ID) != "" {
			pathFromHandle, err := m.resolveReadHandleTarget(req.executionSessionIdentity, req.ID)
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
		path, err := m.resolveReadHandleTarget(req.executionSessionIdentity, req.ID)
		if err != nil {
			return "", "", err
		}

		return path, m.handleDisplay(path), nil
	}

	return "", "", fmt.Errorf("read_file requires id or path")
}

func (m *Manager) resolveReadHandleTarget(identity executionSessionIdentity, handle string) (string, error) {
	path, err := m.pathTracker.PathForHandle(handle)
	if err != nil {
		return "", err
	}

	resolved, err := m.pathAccess.ResolveReadPath(identity, path, "")
	if err == nil {
		return resolved, nil
	}

	defaultReadable, ok := resolveUserGlobalConversationLogReadPath(path)
	if !ok {
		return "", err
	}
	if filepath.Clean(defaultReadable) == filepath.Clean(path) {
		return defaultReadable, nil
	}
	pathInfo, pathErr := os.Stat(path)
	defaultInfo, defaultErr := os.Stat(defaultReadable)
	if pathErr == nil && defaultErr == nil && os.SameFile(pathInfo, defaultInfo) {
		return defaultReadable, nil
	}

	return "", err
}

func (m *Manager) resolveExplicitReadPath(path string) (string, string, error) {
	if !m.pathTracker.HasDiscovered(path) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return "", "", fmt.Errorf("%s", readFileRecoveryError(
				fmt.Sprintf("path %q was not found.", m.pathAccess.DisplayPath(path)),
				m.notFoundRecoveryHint(path),
			))
		}

		if info.IsDir() {
			return "", "", fmt.Errorf("%s", readFileRecoveryError(fmt.Sprintf("path %q is a directory and cannot be read as a file.", m.pathAccess.DisplayPath(path))))
		}

		m.rememberPath(path)
	}

	return path, m.handleDisplay(path), nil
}

func (m *Manager) handleDisplay(path string) string {
	return fmt.Sprintf("%s %s", m.rememberPath(path), m.pathAccess.DisplayPath(path))
}

func (m *Manager) rememberPath(path string) string {
	handle, created := m.pathTracker.Remember(path)
	if !created {
		return handle
	}

	// Fix 1: any path that passes through rememberPath has been confirmed by a
	// workspace tool (listing, search hit, outline, etc.). Mark it observed so
	// edit_file accepts it without a prior read_file.
	m.MarkPathObserved(m.pathAccess.DisplayPath(filepath.Clean(path)))

	return handle
}

func (m *Manager) PrimeDiscoveredPaths(paths []string) {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		resolved, err := m.pathAccess.ResolvePath(path, "")
		if err != nil {
			continue
		}

		m.rememberPath(resolved)
		// Fix 1: paths confirmed to exist via list_files / search_files /
		// search_content / bootstrap priming count as "observed" for the
		// read-before-edit invariant. Reading the contents is not required
		// when the path's existence has been verified through another channel.
		m.MarkPathObserved(m.pathAccess.DisplayPath(resolved))
	}
}

func (m *Manager) ResolveHandlePath(handle string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("workspace manager is nil")
	}

	return m.pathTracker.PathForHandle(handle)
}
