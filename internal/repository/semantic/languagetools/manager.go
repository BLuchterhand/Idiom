package languagetools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"idiom/internal/platform/failures"
	"idiom/internal/repository/semantic"
	"idiom/internal/repository/semantic/language"
)

type Capability = language.Capability
type LanguageSupport = language.LanguageSupport

const (
	CapFileOutline     = language.CapFileOutline
	CapFindSymbol      = language.CapFindSymbol
	CapWorkspaceSymbol = language.CapWorkspaceSymbol
	CapDefinition      = language.CapDefinition
	CapReferences      = language.CapReferences
	CapCallsFrom       = language.CapCallsFrom
	CapCallsInto       = language.CapCallsInto
	CapReadSymbol      = language.CapReadSymbol
	CapExtractBody     = language.CapExtractBody
	CapRelatedFiles    = language.CapRelatedFiles
	CapImplementations = language.CapImplementations
	CapDiagnostics     = language.CapDiagnostics
	CapOverview        = language.CapOverview
	CapEntrypoints     = language.CapEntrypoints
)

const (
	PrecisionTierLSP         = "lsp"
	PrecisionTierSyntaxIndex = "syntax-index"
	PrecisionTierRGFallback  = "rg-fallback"
	PrecisionTierUnavailable = "unavailable"
)

type Host interface {
	Enabled() bool
	Root() string
	CWD() string
	MaxDirectoryEntries() int
	MaxFileBytes() int64
	MaxOutputBytes() int
	WritesEnabled() bool
	ResolvePath(path string, workdir string) (string, error)
	DisplayPath(path string) string
	HandleDisplay(abs string) string
	RememberPath(abs string)
	PrimeDiscoveredPaths(paths []string)
	PathForHandle(handle string) (string, error)
	ReadFileLines(path string, start int, end int) (string, error)
	Execute(ctx context.Context, req ToolRequest) ToolResult
	RunCommandDetailed(ctx context.Context, dir string, name string, args ...string) (string, string, error)
	RunCommandDetailedWithEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error)
	CurrentIndexToken() string
}

type Manager struct {
	host Host

	mu            sync.Mutex
	goIndex       *semantic.GoIndex
	goIndexToken  string
	cppIndex      *semantic.CppIndex
	cppIndexToken string
	goplsPath     string
	goplsChecked  bool

	tsIndex   *semantic.TsIndex
	tsToken   string
	rustIndex *semantic.RustIndex
	rustToken string
	javaIndex *semantic.JavaIndex
	javaToken string
}

func NewManager(host Host) *Manager {
	return &Manager{host: host}
}

func (m *Manager) SetGoplsBinaryForTesting(path string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.goplsPath = path
	m.goplsChecked = true
}

func (m *Manager) Enabled() bool {
	return m != nil && m.host != nil && m.host.Enabled()
}

func (m *Manager) Root() string {
	if m == nil || m.host == nil {
		return ""
	}
	return m.host.Root()
}

func (m *Manager) maxDirectoryEntries() int {
	if m == nil || m.host == nil || m.host.MaxDirectoryEntries() <= 0 {
		return 80
	}
	return m.host.MaxDirectoryEntries()
}

func (m *Manager) maxFileBytes() int64 {
	if m == nil || m.host == nil || m.host.MaxFileBytes() <= 0 {
		return 1 << 20
	}
	return m.host.MaxFileBytes()
}

func (m *Manager) maxOutputBytes() int {
	if m == nil || m.host == nil || m.host.MaxOutputBytes() <= 0 {
		return 1 << 20
	}
	return m.host.MaxOutputBytes()
}

func (m *Manager) resolvePath(path string, workdir string) (string, error) {
	if m == nil || m.host == nil {
		return "", fmt.Errorf("language tool host unavailable")
	}
	return m.host.ResolvePath(path, workdir)
}

func (m *Manager) displayPath(path string) string {
	if m == nil || m.host == nil {
		return filepath.ToSlash(path)
	}
	return m.host.DisplayPath(path)
}

func (m *Manager) handleDisplay(abs string) string {
	if m == nil || m.host == nil {
		return filepath.ToSlash(abs)
	}
	return m.host.HandleDisplay(abs)
}

func (m *Manager) rememberPath(abs string) string {
	if m != nil && m.host != nil {
		m.host.RememberPath(abs)
		return m.host.HandleDisplay(abs)
	}
	return filepath.ToSlash(abs)
}

func (m *Manager) PrimeDiscoveredPaths(paths []string) {
	if m != nil && m.host != nil {
		m.host.PrimeDiscoveredPaths(paths)
	}
}

func (m *Manager) pathForHandle(handle string) (string, error) {
	if m == nil || m.host == nil {
		return "", fmt.Errorf("language tool host unavailable")
	}
	return m.host.PathForHandle(handle)
}

func (m *Manager) execute(ctx context.Context, req ToolRequest) ToolResult {
	if m == nil || m.host == nil {
		return ToolResult{
			Tool:    req.Tool,
			Summary: "Language tool host unavailable",
			Error:   "language tool host unavailable",
			Failure: failures.New(failures.Unavailable, reasonLanguageToolUnavailable, "language tool host unavailable"),
		}
	}
	return m.host.Execute(ctx, req)
}

func (m *Manager) searchContent(ctx context.Context, req ToolRequest, toolName string) ToolResult {
	req.Tool = "search_content"
	result := m.execute(ctx, req)
	result.Tool = toolName
	return result
}

func (m *Manager) runCommandDetailed(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	if m == nil || m.host == nil {
		return "", "", fmt.Errorf("language tool host unavailable")
	}
	return m.host.RunCommandDetailed(ctx, dir, name, args...)
}

func (m *Manager) runCommandDetailedWithEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, string, error) {
	if m == nil || m.host == nil {
		return "", "", fmt.Errorf("language tool host unavailable")
	}
	return m.host.RunCommandDetailedWithEnv(ctx, dir, extraEnv, name, args...)
}

func (m *Manager) runCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	stdout, stderr, err := m.runCommandDetailed(ctx, dir, name, args...)
	output := strings.TrimSpace(stdout)
	errText := strings.TrimSpace(stderr)
	if errText != "" {
		if output != "" {
			output += "\n"
		}
		output += errText
	}
	if max := m.maxOutputBytes(); max > 0 && len(output) > max {
		output = output[:max] + "\n...[truncated]"
	}
	if err != nil {
		return output, err
	}
	return output, nil
}

func (m *Manager) ActiveLanguages() []language.LanguageSupport {
	return language.ActiveLanguages(m.Root())
}

type ToolRequest struct {
	Tool              string
	ID                string
	Key               string
	Handle            string
	Path              string
	Root              string
	EffortRoot        string
	StartLine         int
	EndLine           int
	Pattern           string
	Query             string
	Status            string
	Symbol            string
	Task              string
	Purpose           string
	Capability        string
	Operation         string
	Action            string
	Language          string
	Glob              string
	Workdir           string
	Command           string
	Prompt            string
	Agent             string
	Mode              string
	Content           string
	OldText           string
	NewText           string
	ReplaceAll        bool
	Recursive         bool
	MaxDepth          int
	Limit             int
	BudgetTokens      int
	Resolution        string
	IncludeEvidence   bool
	IncludeSource     bool
	IncludeFiles      bool
	Refresh           bool
	OperatorView      bool
	FreshnessPolicy   string
	FreshnessMaxFiles int
	Category          string
	Component         string
	Expected          string
	Observed          string
	Workaround        string
	Impact            string
	Evidence          []string
	Confidence        string
	Severity          string
	SuggestedFix      string
	SessionID         string
	FailureID         string
	PacketPath        string
	OutputPath        string
	RequiredTests     []string
	RequiredEditPaths []string
	ForbiddenPaths    []string
	Destination       string
	Declarations      []string
	Moves             []GoDeclarationMove
	Description       string
	TaskID            string
}

type GoDeclarationMove struct {
	Destination  string
	Pattern      string
	Declarations []string
}

type ToolResult struct {
	Tool          string
	Code          string
	Summary       string
	Output        string
	Error         string
	Failure       error
	PrecisionTier string
	Relationships []ToolRelationship
	PayloadType   string
	Payload       json.RawMessage
}

type ToolRelationship struct {
	Kind         string
	SourcePath   string
	SourceLine   int
	SourceSymbol string
	TargetPath   string
	TargetLine   int
	TargetSymbol string
	Evidence     string
}

type ToolDiscoveryProfile struct {
	Class       string
	Scope       string
	Cost        string
	ReadOnly    bool
	SideEffects string
}

func discoveryProfile(class string, scope string, cost string, readOnly bool) ToolDiscoveryProfile {
	sideEffects := "read"
	if !readOnly {
		sideEffects = "write"
	}
	return ToolDiscoveryProfile{Class: class, Scope: scope, Cost: cost, ReadOnly: readOnly, SideEffects: sideEffects}
}

func scopeFromPathAndPattern(path string, pattern string) string {
	switch {
	case strings.TrimSpace(path) != "" && strings.TrimSpace(pattern) != "":
		return "targeted"
	case strings.TrimSpace(path) != "":
		return "bounded"
	case strings.TrimSpace(pattern) != "":
		return "workspace"
	default:
		return "workspace"
	}
}

type callHierarchyResult struct {
	Output        string
	Relationships []ToolRelationship
}

func (m *Manager) pathWithinWorkspace(path string) bool {
	_, ok := m.relativeWorkspacePath(path)
	return ok
}

func (m *Manager) relativeWorkspacePath(path string) (string, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	root := filepath.Clean(strings.TrimSpace(m.Root()))
	if path == "" || root == "" {
		return "", false
	}
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !strings.HasPrefix(rel, `..\`) {
		return rel, true
	}
	evaluatedRoot, rootErr := filepath.EvalSymlinks(root)
	evaluatedPath, pathErr := filepath.EvalSymlinks(path)
	if rootErr != nil || pathErr != nil {
		return "", false
	}
	rel, err := filepath.Rel(evaluatedRoot, evaluatedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`) {
		return "", false
	}
	return rel, true
}

func LanguageForFile(path string) (LanguageSupport, bool) {
	return language.LanguageForFile(path)
}

func LanguageForName(name string) (LanguageSupport, bool) {
	return language.LanguageForName(name)
}

func sourceSanityCoversPath(path string) bool {
	if _, ok := language.LanguageForFile(path); ok {
		return true
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".gd":
		return true
	default:
		return false
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func readFileLinesDirect(path string, start, end int) (string, error) {
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
	if start > end {
		return "", fmt.Errorf("start_line must be <= end_line")
	}
	var b strings.Builder
	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (m *Manager) callRelationshipFromAbs(sourceAbs string, sourceLine int, sourceSymbol string, targetAbs string, targetLine int, targetSymbol string, evidence string) ToolRelationship {
	return ToolRelationship{
		Kind:         "calls",
		SourcePath:   filepath.ToSlash(m.displayPath(sourceAbs)),
		SourceLine:   sourceLine,
		SourceSymbol: sourceSymbol,
		TargetPath:   filepath.ToSlash(m.displayPath(targetAbs)),
		TargetLine:   targetLine,
		TargetSymbol: targetSymbol,
		Evidence:     evidence,
	}
}
