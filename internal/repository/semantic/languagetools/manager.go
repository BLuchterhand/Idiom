package languagetools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/failures"
	semanticcpp "idiom/internal/repository/semantic/cpp"
	semanticgo "idiom/internal/repository/semantic/golang"
	semanticjava "idiom/internal/repository/semantic/java"
	"idiom/internal/repository/semantic/language"
	semanticrust "idiom/internal/repository/semantic/rust"
	semanticts "idiom/internal/repository/semantic/typescript"
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
	MaxDirectoryEntries() int
	MaxOutputBytes() int
	WritesEnabled() bool
	ResolvePath(path string, workdir string) (string, error)
	DisplayPath(path string) string
	HandleDisplay(abs string) string
	RememberPath(abs string)
	PrimeDiscoveredPaths(paths []string)
	PathForHandle(handle string) (string, error)
	RouteLanguageTool(ctx context.Context, req RouteLanguageToolRequest) ToolResult
	SearchContent(ctx context.Context, req SearchContentRequest) ToolResult
	RunCommand(ctx context.Context, dir string, extraEnv []string, name string, args ...string) commandexec.Result
}

type Manager struct {
	host Host

	mu             sync.Mutex
	goIndex        *semanticgo.GoIndex
	goIndexToken   string
	cppIndex       *semanticcpp.CppIndex
	cppIndexToken  string
	rustIndex      *semanticrust.RustIndex
	rustIndexToken string
	tsIndex        *semanticts.TsIndex
	tsIndexToken   string
	javaIndex      *semanticjava.JavaIndex
	javaIndexToken string
	goplsPath      string
	goplsChecked   bool
}

func NewManager(host Host) *Manager {
	return &Manager{host: host}
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

func (m *Manager) searchContent(ctx context.Context, req ToolRequest, toolName string) ToolResult {
	if m == nil || m.host == nil {
		return ToolResult{
			Tool:    toolName,
			Summary: "Language tool host unavailable",
			Error:   "language tool host unavailable",
			Failure: failures.New(failures.Unavailable, reasonLanguageToolUnavailable, "language tool host unavailable"),
		}
	}

	result := m.host.SearchContent(ctx, SearchContentRequest{
		Pattern: req.Pattern,
		Path:    req.Path,
		Glob:    req.Glob,
		Workdir: req.Workdir,
	})

	result.Tool = toolName

	return result
}

func (m *Manager) routeLanguageTool(ctx context.Context, req ToolRequest) ToolResult {
	if m == nil || m.host == nil {
		return ToolResult{
			Tool:    req.Tool,
			Summary: "Language tool host unavailable",
			Error:   "language tool host unavailable",
			Failure: failures.New(failures.Unavailable, reasonLanguageToolUnavailable, "language tool host unavailable"),
		}
	}

	return m.host.RouteLanguageTool(ctx, RouteLanguageToolRequest{
		Tool:      req.Tool,
		Path:      req.Path,
		Workdir:   req.Workdir,
		Pattern:   req.Pattern,
		StartLine: req.StartLine,
		EndLine:   req.EndLine,
		MaxDepth:  req.MaxDepth,
		Recursive: req.Recursive,
		Limit:     req.Limit,
	})
}

func (m *Manager) runCommandResult(ctx context.Context, dir string, extraEnv []string, name string, args ...string) commandexec.Result {
	if m == nil || m.host == nil {
		return commandexec.Result{ExitCode: 1, Err: fmt.Errorf("language tool host unavailable")}
	}

	return m.host.RunCommand(ctx, dir, extraEnv, name, args...)
}

func (m *Manager) runCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	result := m.runCommandResult(ctx, dir, nil, name, args...)
	output := result.DisplayProjection(m.maxOutputBytes()).Text

	if err := result.FailureError(""); err != nil {
		return output, err
	}

	return output, nil
}

func (m *Manager) ActiveLanguages() []language.LanguageSupport {
	return language.ActiveLanguages(m.Root())
}

type ToolRequest struct {
	Tool         string
	ID           string
	Path         string
	Workdir      string
	StartLine    int
	EndLine      int
	Pattern      string
	Capability   string
	Language     string
	Resolution   string
	Glob         string
	MaxDepth     int
	Limit        int
	Recursive    bool
	Destination  string
	Declarations []string
	Moves        []GoDeclarationMove
}

type SearchContentRequest struct {
	Pattern string
	Path    string
	Glob    string
	Workdir string
}

type RouteLanguageToolRequest struct {
	Tool      string
	Path      string
	Workdir   string
	Pattern   string
	StartLine int
	EndLine   int
	MaxDepth  int
	Recursive bool
	Limit     int
}

type GoDeclarationMove struct {
	Destination  string
	Pattern      string
	Declarations []string
}

type ToolResult struct {
	Tool          string
	Summary       string
	Output        string
	Error         string
	Failure       error
	Paths         []string
	ChangedPaths  []string
	Facts         *toolfacts.ToolResultFacts
	PrecisionTier string
	Relationships []ToolRelationship
}

func diagnosticToolFacts(paths []string) *toolfacts.ToolResultFacts {
	if len(paths) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(path), "./"))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}

	if len(out) == 0 {
		return nil
	}

	return &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: out}
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
