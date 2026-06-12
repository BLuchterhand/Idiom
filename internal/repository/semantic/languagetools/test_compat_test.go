package languagetools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

type Manager = workspace.Manager
type ToolRequest = workspace.ToolRequest
type ToolResult = workspace.ToolResult
type Capability = language.Capability
type LanguageSupport = language.LanguageSupport
type ToolRelationship = workspace.ToolRelationship

type LSPPosition = languagetools.LSPPosition
type LSPRange = languagetools.LSPRange
type LSPLocation = languagetools.LSPLocation
type LSPLocationLink = languagetools.LSPLocationLink
type LSPCallHierarchyItem = languagetools.LSPCallHierarchyItem
type LSPOutgoingCall = languagetools.LSPOutgoingCall

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

	PrecisionTierLSP         = workspace.PrecisionTierLSP
	PrecisionTierSyntaxIndex = workspace.PrecisionTierSyntaxIndex
	PrecisionTierRGFallback  = workspace.PrecisionTierRGFallback
	PrecisionTierUnavailable = workspace.PrecisionTierUnavailable
)

type codeNavigationSpec struct {
	Name            string
	Capability      Capability
	RequiresPattern bool
	RequiresPath    bool
	Cost            string
	Class           string
}

func NewManager(cfg config.Workspace) *Manager {
	return workspace.NewManager(cfg)
}

func LanguageRegistry() []LanguageSupport {
	return language.LanguageRegistry()
}

func LanguageForName(name string) (LanguageSupport, bool) {
	return language.LanguageForName(name)
}

func LanguageForExtension(ext string) (LanguageSupport, bool) {
	return language.LanguageForExtension(ext)
}

func FormatToolResultForModel(result ToolResult) string {
	return workspace.FormatToolResultForModel(result)
}

func FormatToolTrace(result ToolResult) string {
	return workspace.FormatToolTrace(result)
}

func codeNavigationSpecFor(value string) (codeNavigationSpec, error) {
	spec, err := languagetools.CodeNavigationSpecFor(value)
	if err != nil {
		return codeNavigationSpec{}, err
	}

	return codeNavigationSpec{
		Name:            spec.Name,
		Capability:      spec.Capability,
		RequiresPattern: spec.RequiresPattern,
		RequiresPath:    spec.RequiresPath,
		Cost:            spec.Cost,
		Class:           spec.Class,
	}, nil
}

func codeNavigationCapabilityNames() []string {
	return languagetools.CodeNavigationCapabilityNames()
}

func codeNavigationCapabilityList() string {
	return languagetools.CodeNavigationCapabilityList()
}

func validateSymbolPattern(tool, pattern string) error {
	return languagetools.ValidateSymbolPattern(tool, pattern)
}

func parseLSPLocations(raw json.RawMessage, serverName string) ([]LSPLocation, error) {
	return languagetools.ParseLSPLocations(raw, serverName)
}

func pylspFileURI(path string) string {
	return languagetools.PylspFileURI(path)
}

func RustToolNames() []string {
	return languagetools.RustToolNames()
}

func languageDetects(root string, name string) bool {
	lang, ok := language.LanguageForName(name)
	return ok && lang.Detect != nil && lang.Detect(root)
}

func executeTool(mgr *Manager, tool string, req ToolRequest) ToolResult {
	return executeToolCtx(context.Background(), mgr, tool, req)
}

func executeToolCtx(ctx context.Context, mgr *Manager, tool string, req ToolRequest) ToolResult {
	req.Tool = tool
	return mgr.Execute(ctx, req)
}

func writeTestFile(t testFataler, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type testFataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func writeWorkspaceFile(t testFataler, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
