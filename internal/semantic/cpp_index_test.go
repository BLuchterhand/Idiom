package semantic

import (
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

	index, err := BuildCppIndex(root)
	if err != nil {
		t.Fatalf("BuildCppIndex: %v", err)
	}
	if index == nil {
		t.Fatal("expected non-nil C++ index")
	}
	if got := index.Summary(); !strings.Contains(got, "Indexed 3 C/C++ files") {
		t.Fatalf("unexpected summary: %q", got)
	}
	if matches := index.FindSymbols("tick", 8); len(matches) == 0 {
		t.Fatal("expected tick symbol match")
	}
	if matches := index.FindSymbols("struct EntityManager", 8); len(matches) == 0 || matches[0].Name != "EntityManager" {
		t.Fatalf("expected declaration-shaped struct search to find EntityManager, got %#v", matches)
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
