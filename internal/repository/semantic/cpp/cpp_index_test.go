package cpp

import (
	"idiom/internal/repository/semantic/internal/indexsupport"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCppIndexAndQueries(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "include", "entity_manager.h"), strings.TrimSpace(`
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
`)+"\n")
	mustWriteFile(t, filepath.Join(root, "src", "entity_manager.cpp"), strings.TrimSpace(`
#include "entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
    helper();
}
`)+"\n")
	mustWriteFile(t, filepath.Join(root, "src", "main.cpp"), strings.TrimSpace(`
#include "entity_manager.h"

int helper();

int main() {
    EntityManager manager;
    manager.tick();
    return helper();
}
`)+"\n")
	mustWriteFile(t, filepath.Join(root, "include", "box.h"), strings.TrimSpace(`
#pragma once
template <typename T>
class Box {
public:
    T value;
    Box(T v);
    T get();
};
`)+"\n")
	mustWriteFile(t, filepath.Join(root, "src", "box.cpp"), strings.TrimSpace(`
#include "box.h"

template <typename T>
Box<T>::Box(T v) : value(v) {}

template <typename T>
T Box<T>::get() {
    return value;
}

template class Box<int>;
template class Box<double>;
`)+"\n")

	index, err := BuildCppIndex(root)
	if err != nil {
		t.Fatalf("BuildCppIndex: %v", err)
	}
	if index == nil {
		t.Fatal("expected non-nil C++ index")
	}
	if got := index.Summary(); !strings.Contains(got, "Indexed 5 C/C++ files") {
		t.Fatalf("unexpected summary: %q", got)
	}
	if matches := index.FindSymbols("tick", 8); len(matches) == 0 {
		t.Fatal("expected tick symbol match")
	}
	if matches := index.FindSymbols("struct EntityManager", 8); len(matches) == 0 || matches[0].Name != "EntityManager" {
		t.Fatalf("expected declaration-shaped struct search to find EntityManager, got %#v", matches)
	}
	if matches := index.FindSymbols("union EntityValue", 8); len(matches) == 0 || matches[0].Name != "EntityValue" || matches[0].Kind != "union" {
		t.Fatalf("expected declaration-shaped union search to find EntityValue, got %#v", matches)
	}
	if matches := index.FindSymbols("template <typename T> class EntityBox", 8); len(matches) == 0 || matches[0].Name != "EntityBox" {
		t.Fatalf("expected declaration-shaped template class search to find EntityBox, got %#v", matches)
	}
	if calls := index.FindCallsFrom("main", 8); len(calls) == 0 {
		t.Fatal("expected outgoing calls from main")
	}
	if callers := index.FindCallsInto("helper", 8); len(callers) == 0 {
		t.Fatal("expected incoming calls into helper")
	}
	if deps := index.WhoIncludes("entity_manager.h"); len(deps) == 0 {
		t.Fatal("expected reverse include graph for entity_manager.h")
	}
	if blast := index.BlastRadius("entity_manager.h"); len(blast) < 2 {
		t.Fatalf("expected blast radius to include translation units, got %#v", blast)
	}
	// Template declaration-shaped search: "template <typename T> class Box"
	if matches := index.FindSymbols("template <typename T> class Box", 8); len(matches) == 0 || matches[0].Name != "Box" {
		t.Fatalf("expected declaration-shaped template search to find Box, got %#v", matches)
	}
	if matches := index.FindSymbols("template <typename T> struct Widget", 8); len(matches) != 0 {
		t.Fatalf("expected declaration-shaped template search for Widget to return no match, got %#v", matches)
	}
}

func TestBuildCppIndexWithOptionsScopesWalk(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "native", "sim.cpp"), "int tick() { return 1; }\n")
	mustWriteFile(t, filepath.Join(root, "tools", "tool.cpp"), "int tool() { return 2; }\n")

	index, err := BuildCppIndexWithOptions(root, indexsupport.IndexOptions{Scope: "native"})
	if err != nil {
		t.Fatalf("BuildCppIndexWithOptions: %v", err)
	}
	if index == nil || len(index.Files) != 1 || index.Files[0].Path != "native/sim.cpp" {
		t.Fatalf("expected scoped C++ index to contain only native/sim.cpp, got %#v", index)
	}
	if matches := index.FindSymbols("tool", 8); len(matches) != 0 {
		t.Fatalf("expected scoped C++ index to exclude out-of-scope symbols, got %#v", matches)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
