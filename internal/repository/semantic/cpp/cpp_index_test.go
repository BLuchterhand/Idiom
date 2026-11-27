package cpp

import (
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
		callers := index.CallsInto(index.ResolveSymbol("helper", ""), "", 8)
		Expect(callers.Callers).NotTo(BeEmpty())
		Expect(callers.Total).To(Equal(len(callers.Callers)))
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

		index, err := BuildCppIndexWithOptions(root, BuildOptions{Scope: "native"})
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())
		Expect(index.Files).To(HaveLen(1))
		Expect(index.Files[0].Path).To(Equal("native/sim.cpp"))
		Expect(index.FindSymbols("tool", 8)).To(BeEmpty())
	})
	It("keeps definitions after a hex digit separator and records call sites only as calls", func() {
		root := GinkgoT().TempDir()
		mustWriteFile(filepath.Join(root, "core", "clock.cpp"), strings.TrimSpace(`
namespace bt::world {
namespace {
int mask(int tick) {
    DayPhase out;
    out.day_index = static_cast<int>((tick / 2) & 0xFFFF'FFFFu);
    out.month = static_cast<int>(tick % 12);
    return out.day_index;
}

int blend_dir(int a, int b) {
    return a + b;
}
}  // namespace

int glide(int a, int b) {
    int out = 0;
    out = blend_dir(a, b);
    return out + mask(a);
}
}  // namespace bt::world
`)+"\n")

		index, err := BuildCppIndexWithOptions(root, BuildOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())

		file := index.FileByPath("core/clock.cpp")
		Expect(file).NotTo(BeNil())

		lines := map[string][]int{}
		for _, symbol := range file.Symbols {
			lines[symbol.Qualified] = append(lines[symbol.Qualified], symbol.StartLine)
		}

		Expect(lines["bt::world::mask"]).To(Equal([]int{3}))
		Expect(lines["bt::world::blend_dir"]).To(Equal([]int{10}), "blend_dir starts at its definition, not at the call site")
		Expect(lines["bt::world::glide"]).To(Equal([]int{15}))
	})

	It("tells digit separators from character literals", func() {
		code := blankCppCommentsAndLiterals("a = 1'000 + 0xFFFF'FFFFu + u8'x' + 'y';\n")
		Expect(code).To(Equal("a = 1'000 + 0xFFFF'FFFFu + u8' ' + ' ';\n"))
	})
})

func mustWriteFile(path string, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}
