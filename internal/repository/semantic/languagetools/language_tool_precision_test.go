package languagetools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/language"
)

type languagePrecisionFixture struct {
	name     string
	manager  *workspace.Manager
	requests map[language.Capability]workspace.ToolRequest
}

var standardLanguageCapabilities = []language.Capability{
	language.CapOverview,
	language.CapEntrypoints,
	language.CapFindSymbol,
	language.CapWorkspaceSymbol,
	language.CapDefinition,
	language.CapReferences,
	language.CapCallsFrom,
	language.CapCallsInto,
	language.CapReadSymbol,
	language.CapRelatedFiles,
	language.CapImplementations,
	language.CapDiagnostics,
	language.CapFileOutline,
}

func TestLanguageRegistryConformanceCapabilities(t *testing.T) {
	for _, lang := range language.LanguageRegistry() {
		for _, cap := range standardLanguageCapabilities {
			tool := lang.Tool(cap)
			if strings.TrimSpace(tool) == "" {
				t.Fatalf("%s missing standard capability %s", lang.Name, cap)
			}

			if !strings.HasPrefix(tool, languageToolPrefix(lang.Name)) {
				t.Fatalf("%s %s tool %q does not use expected language prefix", lang.Name, cap, tool)
			}
		}
	}
}

func TestLanguageRegistryTypeScriptAliases(t *testing.T) {
	for _, name := range []string{"javascript", "jsx", "mjs", "cjs", "tsx", "mts", "cts", "d.ts", ".d.mts", ".d.cts"} {
		lang, ok := language.LanguageForName(name)
		if !ok || lang.Name != "typescript" {
			t.Fatalf("language.LanguageForName(%q) = %#v, %v; want typescript", name, lang, ok)
		}
	}

	for _, ext := range []string{".d.ts", ".d.mts", ".d.cts"} {
		lang, ok := language.LanguageForExtension(ext)
		if !ok || lang.Name != "typescript" {
			t.Fatalf("language.LanguageForExtension(%q) = %#v, %v; want typescript", ext, lang, ok)
		}
	}
}

func TestLanguageToolPrecisionTierConformance(t *testing.T) {
	ctx := context.Background()

	for _, fixture := range []languagePrecisionFixture{
		newGoPrecisionFixture(t),
		newCppPrecisionFixture(t),
		newPythonPrecisionFixture(t),
		newTsPrecisionFixture(t),
		newRustPrecisionFixture(t),
		newJavaPrecisionFixture(t),
	} {
		t.Run(fixture.name, func(t *testing.T) {
			lang, ok := languageByName(fixture.name)
			if !ok {
				t.Fatalf("language %q not in registry", fixture.name)
			}

			for cap, tool := range lang.Tools {
				req, ok := fixture.requests[cap]
				if !ok {
					t.Fatalf("%s: missing conformance request for %s (%s)", fixture.name, cap, tool)
				}

				req.Tool = tool

				result := fixture.manager.Execute(ctx, req)

				if result.Tool != tool {
					t.Fatalf("%s %s: result tool mismatch: want %s got %s", fixture.name, cap, tool, result.Tool)
				}

				if !validPrecisionTier(result.PrecisionTier) {
					t.Fatalf("%s %s: invalid precision tier %q for result %#v", fixture.name, cap, result.PrecisionTier, result)
				}
			}
		})
	}
}

func TestLanguagePrecisionTierIsShownToModel(t *testing.T) {
	result := workspace.ToolResult{Tool: "go_find_symbol", Summary: "Found 1 matching Go symbol", PrecisionTier: workspace.PrecisionTierSyntaxIndex}
	rendered := workspace.FormatToolResultForModel(result)

	if !strings.Contains(rendered, "precision_tier: syntax-index") {
		t.Fatalf("model result missing precision tier:\n%s", rendered)
	}

	trace := workspace.FormatToolTrace(result)
	if !strings.Contains(trace, "precision_tier=syntax-index") {
		t.Fatalf("trace missing precision tier:\n%s", trace)
	}
}

func TestSupplementalLanguageDiscoveryToolsReceivePrecisionTier(t *testing.T) {
	goFixture := newGoPrecisionFixture(t)
	cppFixture := newCppPrecisionFixture(t)
	tests := []struct {
		name string
		mgr  *workspace.Manager
		req  workspace.ToolRequest
	}{
		{name: "go_find_function", mgr: goFixture.manager, req: workspace.ToolRequest{Tool: "go_find_function", Pattern: "helper"}},
		{name: "go_find_type", mgr: goFixture.manager, req: workspace.ToolRequest{Tool: "go_find_type", Pattern: "Worker"}},
		{name: "go_find_package", mgr: goFixture.manager, req: workspace.ToolRequest{Tool: "go_find_package", Pattern: "main"}},
		{name: "cpp_declaration", mgr: cppFixture.manager, req: workspace.ToolRequest{Tool: "cpp_declaration", Pattern: "WorkerSystem::run"}},
		{name: "cpp_includes", mgr: cppFixture.manager, req: workspace.ToolRequest{Tool: "cpp_includes", Path: filepath.Join("src", "worker_system.cpp")}},
		{name: "cpp_dead_symbol", mgr: cppFixture.manager, req: workspace.ToolRequest{Tool: "cpp_dead_symbol", Pattern: "NoSuchSymbol"}},
	}

	for _, tt := range tests {
		result := tt.mgr.Execute(context.Background(), tt.req)
		if result.PrecisionTier == "" || !validPrecisionTier(result.PrecisionTier) {
			t.Fatalf("%s precision tier = %q, want valid non-empty tier for result %#v", tt.name, result.PrecisionTier, result)
		}
	}

	result := goFixture.manager.Execute(context.Background(), workspace.ToolRequest{Tool: "go_move_declarations"})
	if result.PrecisionTier != "" {
		t.Fatalf("write tool received precision tier %q", result.PrecisionTier)
	}
}

func languageByName(name string) (language.LanguageSupport, bool) {
	for _, lang := range language.LanguageRegistry() {
		if lang.Name == name {
			return lang, true
		}
	}

	return language.LanguageSupport{}, false
}

func languageToolPrefix(name string) string {
	switch name {
	case "typescript":
		return "ts_"
	default:
		return name + "_"
	}
}

func validPrecisionTier(tier string) bool {
	switch tier {
	case workspace.PrecisionTierLSP, workspace.PrecisionTierSyntaxIndex, workspace.PrecisionTierRGFallback, workspace.PrecisionTierUnavailable:
		return true
	default:
		return false
	}
}

func newGoPrecisionFixture(t *testing.T) languagePrecisionFixture {
	t.Helper()

	root := t.TempDir()
	writePrecisionFile(t, filepath.Join(root, "go.mod"), "module example.test\n\ngo 1.22\n")
	writePrecisionFile(t, filepath.Join(root, "main.go"), `
package main

type Runner interface {
	Run()
}

type Worker struct{}

func (Worker) Run() {
	helper()
}

func helper() {}

func main() {
	Worker{}.Run()
}
`)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true, CommandTimeoutSeconds: 30, MaxFileBytes: 65536, MaxDirectoryEntries: 64})

	return languagePrecisionFixture{
		name:    "go",
		manager: mgr,
		requests: map[language.Capability]workspace.ToolRequest{
			language.CapOverview:        {},
			language.CapEntrypoints:     {},
			language.CapFindSymbol:      {Pattern: "helper"},
			language.CapWorkspaceSymbol: {Pattern: "helper"},
			language.CapDefinition:      {Pattern: "helper"},
			language.CapReferences:      {Pattern: "helper", Path: "."},
			language.CapCallsFrom:       {Pattern: "Run"},
			language.CapCallsInto:       {Pattern: "helper"},
			language.CapReadSymbol:      {Pattern: "helper"},
			language.CapExtractBody:     {Pattern: "helper"},
			language.CapRelatedFiles:    {Pattern: "helper"},
			language.CapImplementations: {Pattern: "Runner"},
			language.CapDiagnostics:     {},
			language.CapFileOutline:     {Path: "main.go"},
		},
	}
}

func newCppPrecisionFixture(t *testing.T) languagePrecisionFixture {
	t.Helper()

	root := t.TempDir()
	writeCppFixture(t, filepath.Join(root, "include", "system_base.h"), `
#pragma once
struct SystemBase {
    virtual void run();
};
struct WorkerSystem : SystemBase {
    void run() override;
};
`)
	writeCppFixture(t, filepath.Join(root, "src", "worker_system.cpp"), `
#include "system_base.h"
int helper() { return 1; }
void SystemBase::run() {}
void WorkerSystem::run() { helper(); }
`)
	writeCppFixture(t, filepath.Join(root, "src", "main.cpp"), `
#include "system_base.h"
int helper();
int main() {
    WorkerSystem worker;
    worker.run();
    return helper();
}
`)
	writeCppCompileCommands(t, root, []string{filepath.Join("src", "worker_system.cpp"), filepath.Join("src", "main.cpp")})

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true, CommandTimeoutSeconds: 30, MaxFileBytes: 65536, MaxDirectoryEntries: 64})

	return languagePrecisionFixture{
		name:    "cpp",
		manager: mgr,
		requests: map[language.Capability]workspace.ToolRequest{
			language.CapOverview:        {},
			language.CapEntrypoints:     {},
			language.CapFindSymbol:      {Pattern: "helper"},
			language.CapWorkspaceSymbol: {Pattern: "helper"},
			language.CapDefinition:      {Pattern: "helper"},
			language.CapReferences:      {Pattern: "helper", Path: "."},
			language.CapCallsFrom:       {Pattern: "main"},
			language.CapCallsInto:       {Pattern: "helper"},
			language.CapReadSymbol:      {Pattern: "helper"},
			language.CapExtractBody:     {Pattern: "helper"},
			language.CapRelatedFiles:    {Pattern: "helper"},
			language.CapImplementations: {Pattern: "SystemBase"},
			language.CapDiagnostics:     {},
			language.CapFileOutline:     {Path: filepath.Join("src", "worker_system.cpp")},
		},
	}
}

func newPythonPrecisionFixture(t *testing.T) languagePrecisionFixture {
	t.Helper()

	root := t.TempDir()
	writePythonToolFixture(t, filepath.Join(root, "pyproject.toml"), "[project]\nname='demo'\n")
	writePythonToolFixture(t, filepath.Join(root, "src", "demo", "engine.py"), `
class SystemBase:
    def run(self):
        return None

class WorkerSystem(SystemBase):
    def run(self):
        return helper()

def helper():
    return "ok"

if __name__ == "__main__":
    helper()
`)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true, CommandTimeoutSeconds: 30, MaxFileBytes: 65536, MaxDirectoryEntries: 64})

	return languagePrecisionFixture{
		name:    "python",
		manager: mgr,
		requests: map[language.Capability]workspace.ToolRequest{
			language.CapOverview:        {},
			language.CapEntrypoints:     {},
			language.CapFindSymbol:      {Pattern: "helper"},
			language.CapWorkspaceSymbol: {Pattern: "helper"},
			language.CapDefinition:      {Pattern: "helper"},
			language.CapReferences:      {Pattern: "helper", Path: "."},
			language.CapCallsFrom:       {Pattern: "run"},
			language.CapCallsInto:       {Pattern: "helper"},
			language.CapReadSymbol:      {Pattern: "helper"},
			language.CapRelatedFiles:    {Pattern: "helper"},
			language.CapImplementations: {Pattern: "SystemBase"},
			language.CapDiagnostics:     {Path: filepath.Join("src", "demo", "engine.py")},
			language.CapFileOutline:     {Path: filepath.Join("src", "demo", "engine.py")},
		},
	}
}

func newTsPrecisionFixture(t *testing.T) languagePrecisionFixture {
	t.Helper()

	root := t.TempDir()
	writeTsTestFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020","module":"commonjs","strict":true}}`)
	writeTsTestFixture(t, filepath.Join(root, "src", "service.ts"), `
export interface Service { run(): void }

export class WorkerService implements Service {
  run(): void { helper(); }
}

export function helper(): void {}

export function main(): void {
  const worker = new WorkerService();
  worker.run();
}
`)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true, CommandTimeoutSeconds: 30, MaxFileBytes: 65536, MaxDirectoryEntries: 64})

	return languagePrecisionFixture{
		name:    "typescript",
		manager: mgr,
		requests: map[language.Capability]workspace.ToolRequest{
			language.CapOverview:        {},
			language.CapEntrypoints:     {},
			language.CapFindSymbol:      {Pattern: "helper"},
			language.CapWorkspaceSymbol: {Pattern: "helper"},
			language.CapDefinition:      {Pattern: "helper"},
			language.CapReferences:      {Pattern: "helper", Path: "."},
			language.CapCallsFrom:       {Pattern: "main"},
			language.CapCallsInto:       {Pattern: "helper"},
			language.CapReadSymbol:      {Pattern: "helper"},
			language.CapRelatedFiles:    {Pattern: "helper"},
			language.CapImplementations: {Pattern: "Service"},
			language.CapDiagnostics:     {Path: filepath.Join("src", "service.ts")},
			language.CapFileOutline:     {Path: filepath.Join("src", "service.ts")},
		},
	}
}

func newRustPrecisionFixture(t *testing.T) languagePrecisionFixture {
	t.Helper()

	mgr, _ := newRustFixtureManager(t)

	return languagePrecisionFixture{
		name:    "rust",
		manager: mgr,
		requests: map[language.Capability]workspace.ToolRequest{
			language.CapOverview:        {},
			language.CapEntrypoints:     {},
			language.CapFindSymbol:      {Pattern: "helper"},
			language.CapWorkspaceSymbol: {Pattern: "helper"},
			language.CapDefinition:      {Pattern: "helper"},
			language.CapReferences:      {Pattern: "helper", Path: "."},
			language.CapCallsFrom:       {Pattern: "main"},
			language.CapCallsInto:       {Pattern: "helper"},
			language.CapReadSymbol:      {Pattern: "helper"},
			language.CapRelatedFiles:    {Pattern: "worker"},
			language.CapImplementations: {Pattern: "Runnable"},
			language.CapDiagnostics:     {},
			language.CapFileOutline:     {Path: filepath.Join("src", "worker.rs")},
		},
	}
}

func newJavaPrecisionFixture(t *testing.T) languagePrecisionFixture {
	t.Helper()

	root := buildJavaTempWorkspace(t)
	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true, CommandTimeoutSeconds: 30, MaxFileBytes: 65536, MaxDirectoryEntries: 64})

	return languagePrecisionFixture{
		name:    "java",
		manager: mgr,
		requests: map[language.Capability]workspace.ToolRequest{
			language.CapOverview:        {},
			language.CapEntrypoints:     {},
			language.CapFindSymbol:      {Pattern: "Helper"},
			language.CapWorkspaceSymbol: {Pattern: "Helper"},
			language.CapDefinition:      {Pattern: "Helper"},
			language.CapReferences:      {Pattern: "Helper", Path: "."},
			language.CapCallsFrom:       {Pattern: "main"},
			language.CapCallsInto:       {Pattern: "compute"},
			language.CapReadSymbol:      {Pattern: "Helper#run"},
			language.CapRelatedFiles:    {Pattern: "helper"},
			language.CapImplementations: {Pattern: "Service"},
			language.CapDiagnostics:     {},
			language.CapFileOutline:     {Path: filepath.Join("src", "main", "java", "com", "example", "App.java")},
		},
	}
}

func writePrecisionFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
