package languagetools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("C++ workspace tools", func() {
	It("provides syntax-index discovery and navigation", func() {
		root := GinkgoT().TempDir()
		writeCppFixture(filepath.Join(root, "include", "entity_manager.h"), `
#pragma once
struct EntityManager {
    void tick();
};

union EntityValue {
    int id;
    double weight;
};

template <typename T>
class EntityBox {
public:
    T value;
};

template <typename T>
struct Widget {
    T data;
    Widget() : data{} {}
};
`)
		writeCppFixture(filepath.Join(root, "src", "entity_manager.cpp"), `
#include "entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
    helper();
}
`)
		writeCppFixture(filepath.Join(root, "src", "main.cpp"), `
#include "entity_manager.h"

int helper();

int main() {
    EntityManager manager;
    manager.tick();
    return helper();
}
`)
		writeCppFixture(filepath.Join(root, "include", "system_base.h"), `
#pragma once
struct SystemBase {
    virtual ~SystemBase() = default;
    virtual void process_cycle();
};
`)
		writeCppFixture(filepath.Join(root, "src", "system_base.cpp"), `
#include "system_base.h"
void SystemBase::process_cycle() {}
`)
		writeCppFixture(filepath.Join(root, "include", "worker_system.h"), `
#pragma once
#include "system_base.h"
struct WorkerSystem : SystemBase {
    void process_cycle() override;
};
`)
		writeCppFixture(filepath.Join(root, "src", "worker_system.cpp"), `
#include "worker_system.h"
void WorkerSystem::process_cycle() {}
`)
		writeCppCompileCommands(root, []string{
			filepath.Join("src", "entity_manager.cpp"),
			filepath.Join("src", "main.cpp"),
			filepath.Join("src", "system_base.cpp"),
			filepath.Join("src", "worker_system.cpp"),
		})

		manager := workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			EnableWrites:          true,
			CommandTimeoutSeconds: 30,
			MaxFileBytes:          65536,
		})

		Expect(languageDetects(root, "cpp")).To(BeTrue())
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_overview"}), "Indexed 7 C/C++ files")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_find_symbol", Pattern: "tick"}), "entity_manager.cpp")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_find_symbol", Pattern: "struct EntityManager"}), "EntityManager")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_read_symbol", Pattern: "struct EntityManager"}), "struct EntityManager")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_find_symbol", Pattern: "union EntityValue"}), "EntityValue")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_read_symbol", Pattern: "union EntityValue"}), "union EntityValue")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_find_symbol", Pattern: "template <typename T> class EntityBox"}), "EntityBox")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_read_symbol", Pattern: "template <typename T> class EntityBox"}), "class EntityBox")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_extract_body", Pattern: "EntityManager::tick"}), "helper();")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_blast_radius", Pattern: "entity_manager.h"}), "entity_manager.cpp", "main.cpp")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_references", Pattern: "helper", Path: "."}), "entity_manager.cpp", "helper();")
		outline := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_file_outline", Path: filepath.Join("src", "entity_manager.cpp")})
		expectToolSuccess(outline, "file: src/entity_manager.cpp", "entity_manager.h", "helper", "tick")
		Expect(outline.Summary).To(ContainSubstring("Outlined"))
	})

	It("answers exactly, echoes the resolved symbol, and fails closed on ambiguity", func() {
		root := GinkgoT().TempDir()
		writeCppFixture(filepath.Join(root, "include", "pair.h"), `
#pragma once
struct Widget { void offer(); };
struct Gadget { void offer(); };
void helper();
void helper_extra();
`)
		writeCppFixture(filepath.Join(root, "src", "pair.cpp"), `
#include "pair.h"
void Widget::offer() { helper(); }
void Gadget::offer() { helper_extra(); }
void helper() {}
void helper_extra() {}
`)
		writeCppFixture(filepath.Join(root, "src", "main.cpp"), `
#include "pair.h"
int main() { helper(); return 0; }
`)

		manager := workspace.NewManager(config.Workspace{
			Root: root, CWD: root, EnableShell: true, EnableWrites: true, CommandTimeoutSeconds: 30, MaxFileBytes: 65536,
		})

		ambiguous := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_calls_into", Pattern: "offer"})
		Expect(ambiguous.Code).To(Equal("ambiguous_symbol"))
		Expect(ambiguous.Output).To(ContainSubstring("Widget::offer"))
		Expect(ambiguous.Output).To(ContainSubstring("Gadget::offer"))

		missing := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_calls_into", Pattern: "help"})
		Expect(missing.Code).To(Equal("symbol_not_found"))

		exact := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_calls_into", Pattern: "helper"})
		Expect(exact.Error).To(BeEmpty())
		Expect(exact.Summary).To(ContainSubstring(`for helper (requested "helper")`))
		Expect(exact.Output).To(ContainSubstring("pair.cpp"))
		Expect(exact.Output).To(ContainSubstring("main.cpp"))
		Expect(exact.Output).NotTo(ContainSubstring("helper_extra"))
		Expect(strings.Split(exact.Output, "\n")).To(HaveLen(2), "helper has two callers; the helper_extra caller is not one")

		freshness, ok := exact.Freshness.(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(freshness["index_key"]).NotTo(BeNil())
		Expect(freshness).To(HaveKey("phase_ms"))
		Expect(freshness["collector"]).To(Equal("cpp-syntax-index/2"))

		blast := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_blast_radius", Pattern: "Widget"})
		Expect(blast.Error).To(BeEmpty())
		Expect(blast.Summary).To(ContainSubstring(`symbol Widget (requested "Widget")`))
		Expect(blast.Output).To(ContainSubstring("main.cpp"))
	})

	It("uses clangd for precise navigation when available", func() {
		if _, err := exec.LookPath("clangd"); err != nil {
			Skip("clangd not installed")
		}

		root := GinkgoT().TempDir()
		writeCppFixture(filepath.Join(root, "include", "entity_manager.h"), `
#pragma once
struct EntityManager {
    void tick();
};
`)
		writeCppFixture(filepath.Join(root, "src", "entity_manager.cpp"), `
#include "entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
    helper();
}
`)
		writeCppFixture(filepath.Join(root, "src", "main.cpp"), `
#include "entity_manager.h"

int helper();

int main() {
    EntityManager manager;
    manager.tick();
    return helper();
}
`)
		writeCppFixture(filepath.Join(root, "include", "system_base.h"), `
#pragma once
struct SystemBase {
    virtual ~SystemBase() = default;
    virtual void process_cycle();
};
`)
		writeCppFixture(filepath.Join(root, "src", "system_base.cpp"), `
#include "system_base.h"
void SystemBase::process_cycle() {}
`)
		writeCppFixture(filepath.Join(root, "include", "worker_system.h"), `
#pragma once
#include "system_base.h"
struct WorkerSystem : SystemBase {
    void process_cycle() override;
};
`)
		writeCppFixture(filepath.Join(root, "src", "worker_system.cpp"), `
#include "worker_system.h"
void WorkerSystem::process_cycle() {}
`)
		writeCppCompileCommands(root, []string{
			filepath.Join("src", "entity_manager.cpp"),
			filepath.Join("src", "main.cpp"),
			filepath.Join("src", "system_base.cpp"),
			filepath.Join("src", "worker_system.cpp"),
		})

		manager := workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			EnableWrites:          true,
			CommandTimeoutSeconds: 30,
			MaxFileBytes:          65536,
		})

		workspaceSymbols := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_workspace_symbol", Pattern: "EntityManager"})
		expectToolSuccess(workspaceSymbols, "entity_manager.h")
		Expect(workspaceSymbols.Summary).To(ContainSubstring("clangd"))
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_definition", Pattern: "EntityManager::tick"}), "entity_manager.cpp")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_declaration", Pattern: "EntityManager::tick"}), "entity_manager.h")
		references := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_references", Pattern: "helper", Path: "."})
		expectToolSuccess(references, "helper();")
		Expect(references.Summary).To(ContainSubstring("clangd"))
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_implementations", Pattern: "SystemBase::process_cycle"}), "worker_system.cpp")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_calls_from", Pattern: "EntityManager::tick"}), "helper")
		incoming := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "cpp_calls_into", Pattern: "helper"})
		Expect(incoming.Error).To(BeEmpty())
		Expect(incoming.Output).To(Or(ContainSubstring("main"), ContainSubstring("tick")))
	})
})

func writeCppFixture(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}

func writeCppCompileCommands(root string, files []string) {
	GinkgoHelper()
	entries := make([]map[string]string, 0, len(files))
	for _, rel := range files {
		abs := filepath.Join(root, rel)

		entries = append(entries, map[string]string{
			"directory": root,
			"command":   "clang++ -Iinclude -std=c++20 -c " + filepath.ToSlash(abs),
			"file":      filepath.ToSlash(abs),
		})
	}

	data, err := json.Marshal(entries)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(root, "compile_commands.json"), append(data, '\n'), 0o644)).To(Succeed())
}
