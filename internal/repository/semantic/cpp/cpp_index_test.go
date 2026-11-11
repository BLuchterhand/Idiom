package cpp

import (
	"idiom/internal/repository/semantic"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("C++ semantic index", func() {
	It("indexes declarations, calls, includes, templates, and blast radius", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "include", "entity_manager.h"), strings.TrimSpace(`
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
		mustWriteFile(filepath.Join(root, "src", "entity_manager.cpp"), strings.TrimSpace(`
#include "entity_manager.h"

int helper() {
    return 1;
}

void EntityManager::tick() {
	helper();
}
`)+"\n")
		mustWriteFile(filepath.Join(root, "src", "main.cpp"), strings.TrimSpace(`
#include "entity_manager.h"

int helper();

int main() {
    EntityManager manager;
    manager.tick();
	return helper();
}
`)+"\n")
		mustWriteFile(filepath.Join(root, "include", "box.h"), strings.TrimSpace(`
#pragma once
template <typename T>
class Box {
public:
    T value;
    Box(T v);
	T get();
};
`)+"\n")
		mustWriteFile(filepath.Join(root, "src", "box.cpp"), strings.TrimSpace(`
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
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())
		Expect(index.Summary()).To(ContainSubstring("Indexed 5 C/C++ files"))
		Expect(index.FindSymbols("tick", 8)).NotTo(BeEmpty())

		matches := index.FindSymbols("struct EntityManager", 8)
		Expect(matches).NotTo(BeEmpty())
		Expect(matches[0].Name).To(Equal("EntityManager"))
		matches = index.FindSymbols("union EntityValue", 8)
		Expect(matches).NotTo(BeEmpty())
		Expect(matches[0].Name).To(Equal("EntityValue"))
		Expect(matches[0].Kind).To(Equal("union"))
		matches = index.FindSymbols("template <typename T> class EntityBox", 8)
		Expect(matches).NotTo(BeEmpty())
		Expect(matches[0].Name).To(Equal("EntityBox"))

		mainCalls, _ := index.CallsFromDefinitions(index.ResolveSymbol("main", "").Definitions, 8)
		Expect(mainCalls).NotTo(BeEmpty())
		calls, total := index.CallsIntoName("helper", 8)
		Expect(calls).NotTo(BeEmpty())
		Expect(total).To(Equal(len(calls)))
		Expect(index.WhoIncludes("entity_manager.h")).NotTo(BeEmpty())
		Expect(len(index.BlastRadius("entity_manager.h"))).To(BeNumerically(">=", 2))
		matches = index.FindSymbols("template <typename T> class Box", 8)
		Expect(matches).NotTo(BeEmpty())
		Expect(matches[0].Name).To(Equal("Box"))
		Expect(index.FindSymbols("template <typename T> struct Widget", 8)).To(BeEmpty())
	})

	It("limits indexing to the configured scope", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "native", "sim.cpp"), "int tick() { return 1; }\n")
		mustWriteFile(filepath.Join(root, "tools", "tool.cpp"), "int tool() { return 2; }\n")

		index, err := BuildCppIndexWithOptions(root, semantic.IndexOptions{Scope: "native"})
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())
		Expect(index.Files).To(HaveLen(1))
		Expect(index.Files[0].Path).To(Equal("native/sim.cpp"))
		Expect(index.FindSymbols("tool", 8)).To(BeEmpty())
	})
})

func mustWriteFile(path string, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}
