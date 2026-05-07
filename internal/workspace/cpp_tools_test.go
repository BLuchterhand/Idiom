package workspace

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestCppWorkspaceTools(t *testing.T) {
	root := t.TempDir()
	writeCppFixture(t, filepath.Join(root, "include", "entity_manager.h"), `
#pragma once
struct EntityManager {
    void tick();
};

union EntityValue {
    int id;
    double weight;
};
`)
	writeCppFixture(t, filepath.Join(root, "src", "entity_manager.cpp"), `
#include "entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
    helper();
}
`)
	writeCppFixture(t, filepath.Join(root, "src", "main.cpp"), `
#include "entity_manager.h"

int helper();

int main() {
    EntityManager manager;
    manager.tick();
    return helper();
}
`)
	writeCppFixture(t, filepath.Join(root, "include", "system_base.h"), `
#pragma once
struct SystemBase {
    virtual ~SystemBase() = default;
    virtual void process_cycle();
};
`)
	writeCppFixture(t, filepath.Join(root, "src", "system_base.cpp"), `
#include "system_base.h"
void SystemBase::process_cycle() {}
`)
	writeCppFixture(t, filepath.Join(root, "include", "worker_system.h"), `
#pragma once
#include "system_base.h"
struct WorkerSystem : SystemBase {
    void process_cycle() override;
};
`)
	writeCppFixture(t, filepath.Join(root, "src", "worker_system.cpp"), `
#include "worker_system.h"
void WorkerSystem::process_cycle() {}
`)
	writeCppCompileCommands(t, root, []string{
		filepath.Join("src", "entity_manager.cpp"),
		filepath.Join("src", "main.cpp"),
		filepath.Join("src", "system_base.cpp"),
		filepath.Join("src", "worker_system.cpp"),
	})

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
	})

	if !mgr.isCppWorkspace() {
		t.Fatal("expected temp fixture to count as C++ workspace")
	}
	if result := mgr.cppOverview(); result.Error != "" || !strings.Contains(result.Output, "Indexed 7 C/C++ files") {
		t.Fatalf("unexpected cppOverview result: %#v", result)
	}
	find := mgr.cppFindSymbol(ToolRequest{Tool: "cpp_find_symbol", Pattern: "tick"})
	if find.Error != "" || !strings.Contains(find.Output, "entity_manager.cpp") {
		t.Fatalf("unexpected cppFindSymbol result: %#v", find)
	}
	findDecl := mgr.cppFindSymbol(ToolRequest{Tool: "cpp_find_symbol", Pattern: "struct EntityManager"})
	if findDecl.Error != "" || !strings.Contains(findDecl.Output, "EntityManager") {
		t.Fatalf("expected declaration-shaped cppFindSymbol query to find EntityManager, got %#v", findDecl)
	}
	readDecl := mgr.cppReadSymbol(ToolRequest{Tool: "cpp_read_symbol", Pattern: "struct EntityManager"})
	if readDecl.Error != "" || !strings.Contains(readDecl.Output, "struct EntityManager") {
		t.Fatalf("expected declaration-shaped cppReadSymbol to return EntityManager snippet, got %#v", readDecl)
	}
	findUnion := mgr.cppFindSymbol(ToolRequest{Tool: "cpp_find_symbol", Pattern: "union EntityValue"})
	if findUnion.Error != "" || !strings.Contains(findUnion.Output, "EntityValue") {
		t.Fatalf("expected declaration-shaped cppFindSymbol query to find EntityValue union, got %#v", findUnion)
	}
	readUnion := mgr.cppReadSymbol(ToolRequest{Tool: "cpp_read_symbol", Pattern: "union EntityValue"})
	if readUnion.Error != "" || !strings.Contains(readUnion.Output, "union EntityValue") {
		t.Fatalf("expected declaration-shaped cppReadSymbol to return EntityValue union, got %#v", readUnion)
	}
	body := mgr.cppExtractBody(ToolRequest{Tool: "cpp_extract_body", Pattern: "EntityManager::tick"})
	if body.Error != "" || !strings.Contains(body.Output, "helper();") {
		t.Fatalf("unexpected cppExtractBody result: %#v", body)
	}
	blast := mgr.cppBlastRadius(ToolRequest{Tool: "cpp_blast_radius", Pattern: "entity_manager.h"})
	if blast.Error != "" || !strings.Contains(blast.Output, "entity_manager.cpp") || !strings.Contains(blast.Output, "main.cpp") {
		t.Fatalf("unexpected cppBlastRadius result: %#v", blast)
	}
	refs := mgr.cppReferences(context.Background(), ToolRequest{Tool: "cpp_references", Pattern: "helper", Path: "."})
	if refs.Error != "" || !strings.Contains(refs.Output, "entity_manager.cpp") || !strings.Contains(refs.Output, "helper();") {
		t.Fatalf("unexpected cppReferences result: %#v", refs)
	}
	outline := mgr.cppFileOutline(ToolRequest{Tool: "cpp_file_outline", Path: filepath.Join("src", "entity_manager.cpp")})
	if outline.Error != "" {
		t.Fatalf("unexpected cppFileOutline error: %#v", outline)
	}
	if !strings.Contains(outline.Output, "file: src/entity_manager.cpp") {
		t.Fatalf("cppFileOutline missing file header: %#v", outline)
	}
	if !strings.Contains(outline.Output, `entity_manager.h`) {
		t.Fatalf("cppFileOutline missing include: %#v", outline)
	}
	if !strings.Contains(outline.Output, "helper") || !strings.Contains(outline.Output, "tick") {
		t.Fatalf("cppFileOutline missing symbols: %#v", outline)
	}
	if !strings.Contains(outline.Summary, "Outlined") {
		t.Fatalf("cppFileOutline summary: %#v", outline)
	}
}

func TestCppWorkspaceToolsWithClangd(t *testing.T) {
	if _, err := exec.LookPath("clangd"); err != nil {
		t.Skip("clangd not installed")
	}

	root := t.TempDir()
	writeCppFixture(t, filepath.Join(root, "include", "entity_manager.h"), `
#pragma once
struct EntityManager {
    void tick();
};
`)
	writeCppFixture(t, filepath.Join(root, "src", "entity_manager.cpp"), `
#include "entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
    helper();
}
`)
	writeCppFixture(t, filepath.Join(root, "src", "main.cpp"), `
#include "entity_manager.h"

int helper();

int main() {
    EntityManager manager;
    manager.tick();
    return helper();
}
`)
	writeCppFixture(t, filepath.Join(root, "include", "system_base.h"), `
#pragma once
struct SystemBase {
    virtual ~SystemBase() = default;
    virtual void process_cycle();
};
`)
	writeCppFixture(t, filepath.Join(root, "src", "system_base.cpp"), `
#include "system_base.h"
void SystemBase::process_cycle() {}
`)
	writeCppFixture(t, filepath.Join(root, "include", "worker_system.h"), `
#pragma once
#include "system_base.h"
struct WorkerSystem : SystemBase {
    void process_cycle() override;
};
`)
	writeCppFixture(t, filepath.Join(root, "src", "worker_system.cpp"), `
#include "worker_system.h"
void WorkerSystem::process_cycle() {}
`)
	writeCppCompileCommands(t, root, []string{
		filepath.Join("src", "entity_manager.cpp"),
		filepath.Join("src", "main.cpp"),
		filepath.Join("src", "system_base.cpp"),
		filepath.Join("src", "worker_system.cpp"),
	})

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
	})

	ws := mgr.cppWorkspaceSymbol(context.Background(), ToolRequest{Tool: "cpp_workspace_symbol", Pattern: "EntityManager"})
	if ws.Error != "" || !strings.Contains(ws.Summary, "clangd") || !strings.Contains(ws.Output, "entity_manager.h") {
		t.Fatalf("unexpected cppWorkspaceSymbol result: %#v", ws)
	}

	def := mgr.cppDefinition(context.Background(), ToolRequest{Tool: "cpp_definition", Pattern: "EntityManager::tick"})
	if def.Error != "" || !strings.Contains(def.Output, "entity_manager.cpp") {
		t.Fatalf("unexpected cppDefinition result: %#v", def)
	}

	decl := mgr.cppDeclaration(context.Background(), ToolRequest{Tool: "cpp_declaration", Pattern: "EntityManager::tick"})
	if decl.Error != "" || !strings.Contains(decl.Output, "entity_manager.h") {
		t.Fatalf("unexpected cppDeclaration result: %#v", decl)
	}

	refs := mgr.cppReferences(context.Background(), ToolRequest{Tool: "cpp_references", Pattern: "helper", Path: "."})
	if refs.Error != "" || !strings.Contains(refs.Summary, "clangd") || !strings.Contains(refs.Output, "helper();") {
		t.Fatalf("unexpected cppReferences result: %#v", refs)
	}

	impls := mgr.cppImplementations(context.Background(), ToolRequest{Tool: "cpp_implementations", Pattern: "SystemBase::process_cycle"})
	if impls.Error != "" || !strings.Contains(impls.Output, "worker_system.cpp") {
		t.Fatalf("unexpected cppImplementations result: %#v", impls)
	}

	outgoing := mgr.cppCallsFrom(context.Background(), ToolRequest{Tool: "cpp_calls_from", Pattern: "EntityManager::tick"})
	if outgoing.Error != "" || !strings.Contains(outgoing.Output, "helper") {
		t.Fatalf("unexpected cppCallsFrom result: %#v", outgoing)
	}

	incoming := mgr.cppCallsInto(context.Background(), ToolRequest{Tool: "cpp_calls_into", Pattern: "helper"})
	if incoming.Error != "" || (!strings.Contains(incoming.Output, "main") && !strings.Contains(incoming.Output, "tick")) {
		t.Fatalf("unexpected cppCallsInto result: %#v", incoming)
	}
}

func writeCppFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeCppCompileCommands(t *testing.T, root string, files []string) {
	t.Helper()
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
	if err != nil {
		t.Fatalf("marshal compile_commands: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "compile_commands.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write compile_commands.json: %v", err)
	}
}
