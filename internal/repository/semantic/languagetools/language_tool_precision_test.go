package languagetools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/language"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Language-tool precision conformance", func() {
	It("registers every standard capability with the canonical prefix", func() {
		for _, supportedLanguage := range language.LanguageRegistry() {
			for _, capability := range standardLanguageCapabilities {
				tool := supportedLanguage.Tool(capability)
				Expect(tool).NotTo(BeEmpty(), "%s capability %s", supportedLanguage.Name, capability)
				Expect(tool).To(HavePrefix(languageToolPrefix(supportedLanguage.Name)))
			}
		}
	})

	It("resolves TypeScript aliases and declaration extensions", func() {
		for _, name := range []string{"javascript", "jsx", "mjs", "cjs", "tsx", "mts", "cts", "d.ts", ".d.mts", ".d.cts"} {
			supportedLanguage, ok := language.LanguageForName(name)
			Expect(ok).To(BeTrue(), "alias %q", name)
			Expect(supportedLanguage.Name).To(Equal("typescript"))
		}
		for _, extension := range []string{".d.ts", ".d.mts", ".d.cts"} {
			supportedLanguage, ok := language.LanguageForExtension(extension)
			Expect(ok).To(BeTrue(), "extension %q", extension)
			Expect(supportedLanguage.Name).To(Equal("typescript"))
		}
	})

	It("returns a valid precision tier for every language capability", func() {
		for _, fixture := range []languagePrecisionFixture{
			newGoPrecisionFixture(), newCppPrecisionFixture(), newPythonPrecisionFixture(),
			newTsPrecisionFixture(), newRustPrecisionFixture(), newJavaPrecisionFixture(),
		} {
			supportedLanguage, ok := languageByName(fixture.name)
			Expect(ok).To(BeTrue(), "language %q", fixture.name)
			for capability, tool := range supportedLanguage.Tools {
				request, ok := fixture.requests[capability]
				Expect(ok).To(BeTrue(), "%s capability %s (%s)", fixture.name, capability, tool)
				request.Tool = tool
				result := fixture.manager.Execute(context.Background(), request)
				Expect(result.Tool).To(Equal(tool))
				Expect(validPrecisionTier(result.PrecisionTier)).To(BeTrue(), "%s capability %s", fixture.name, capability)
			}
		}
	})

	It("shows precision tiers to the model and trace", func() {
		result := workspace.ToolResult{Tool: "go_find_symbol", Summary: "Found 1 matching Go symbol", PrecisionTier: workspace.PrecisionTierSyntaxIndex}
		Expect(workspace.FormatToolResultForModel(result)).To(ContainSubstring("precision_tier: syntax-index"))
		Expect(workspace.FormatToolTrace(result)).To(ContainSubstring("precision_tier=syntax-index"))
	})

	It("annotates supplemental discovery tools but not write tools", func() {
		goFixture := newGoPrecisionFixture()
		cppFixture := newCppPrecisionFixture()
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

		for _, test := range tests {
			result := test.mgr.Execute(context.Background(), test.req)
			Expect(result.PrecisionTier).NotTo(BeEmpty(), "tool %q", test.name)
			Expect(validPrecisionTier(result.PrecisionTier)).To(BeTrue(), "tool %q", test.name)
		}

		result := goFixture.manager.Execute(context.Background(), workspace.ToolRequest{Tool: "go_move_declarations"})
		Expect(result.PrecisionTier).To(BeEmpty())
	})
})

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

func newGoPrecisionFixture() languagePrecisionFixture {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writePrecisionFile(filepath.Join(root, "go.mod"), "module example.test\n\ngo 1.22\n")
	writePrecisionFile(filepath.Join(root, "main.go"), `
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

func newCppPrecisionFixture() languagePrecisionFixture {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writeCppFixture(filepath.Join(root, "include", "system_base.h"), `
#pragma once
struct SystemBase {
    virtual void run();
};
struct WorkerSystem : SystemBase {
    void run() override;
};
`)
	writeCppFixture(filepath.Join(root, "src", "worker_system.cpp"), `
#include "system_base.h"
int helper() { return 1; }
void SystemBase::run() {}
void WorkerSystem::run() { helper(); }
`)
	writeCppFixture(filepath.Join(root, "src", "main.cpp"), `
#include "system_base.h"
int helper();
int main() {
    WorkerSystem worker;
    worker.run();
    return helper();
}
`)
	writeCppCompileCommands(root, []string{filepath.Join("src", "worker_system.cpp"), filepath.Join("src", "main.cpp")})

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

func newPythonPrecisionFixture() languagePrecisionFixture {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writePythonToolFixture(filepath.Join(root, "pyproject.toml"), "[project]\nname='demo'\n")
	writePythonToolFixture(filepath.Join(root, "src", "demo", "engine.py"), `
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

func newTsPrecisionFixture() languagePrecisionFixture {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writeTsTestFixture(filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020","module":"commonjs","strict":true}}`)
	writeTsTestFixture(filepath.Join(root, "src", "service.ts"), `
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

func newRustPrecisionFixture() languagePrecisionFixture {
	GinkgoHelper()
	manager, _ := newRustFixtureManager()

	return languagePrecisionFixture{
		name:    "rust",
		manager: manager,
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

func newJavaPrecisionFixture() languagePrecisionFixture {
	GinkgoHelper()
	root := buildJavaTempWorkspace()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableShell: true, CommandTimeoutSeconds: 30, MaxFileBytes: 65536, MaxDirectoryEntries: 64})

	return languagePrecisionFixture{
		name:    "java",
		manager: manager,
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

func writePrecisionFile(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}
