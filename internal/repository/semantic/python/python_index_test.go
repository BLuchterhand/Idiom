package python

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Python semantic index", func() {
	It("indexes files, symbols, calls, imports, and entrypoints", func() {
		root := GinkgoT().TempDir()
		writePythonFixture(filepath.Join(root, "pyproject.toml"), `
[project]
name = "demo"
`)
		writePythonFixture(filepath.Join(root, "src", "demo", "__init__.py"), "")
		writePythonFixture(filepath.Join(root, "src", "demo", "core.py"), `
import math

class Engine:
    def __init__(self, name):
        self.name = name

    def tick(self):
        return math.floor(1.5)

def helper(x):
    return Engine(x).tick()

if __name__ == "__main__":
    helper("demo")
`)
		writePythonFixture(filepath.Join(root, "src", "demo", "app.py"), `
from .core import Engine, helper

def run():
    e = Engine("app")
    return e.tick() + helper("inner")
`)
		writePythonFixture(filepath.Join(root, "tests", "test_core.py"), `
from demo.core import Engine

def test_engine():
    assert Engine("x").tick() is not None
`)

		index, err := BuildPythonIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())
		Expect(len(index.Files)).To(BeNumerically(">=", 4))

		core := index.FileByPath("src/demo/core.py")
		Expect(core).NotTo(BeNil())
		Expect(core.HasMain).To(BeTrue())
		Expect(core.Classes).To(Equal(1))
		Expect(core.Funcs).To(BeNumerically(">=", 1))
		Expect(core.Methods).To(BeNumerically(">=", 2))

		matches := index.FindSymbols("tick", 10)
		Expect(matches).NotTo(BeEmpty())

		foundTick := false
		for _, match := range matches {
			if match.Name == "tick" && strings.Contains(match.Path, "core.py") {
				foundTick = true
				break
			}
		}
		Expect(foundTick).To(BeTrue())
		Expect(index.FindSymbols("Engine", 10)).NotTo(BeEmpty())

		entrypoints := index.DefaultFiles(4)
		Expect(entrypoints).NotTo(BeEmpty())
		foundAnchor := false
		for _, path := range entrypoints {
			if strings.Contains(path, "core.py") || strings.Contains(path, "app.py") {
				foundAnchor = true
				break
			}
		}
		Expect(foundAnchor).To(BeTrue())
		Expect(index.RelevantFiles([]string{"engine"}, 4)).NotTo(BeEmpty())
		Expect(index.Forward).To(HaveKey("src/demo/app.py"))
		Expect(index.FindCallsFrom("helper", 16)).NotTo(BeEmpty())
		Expect(index.FindCallsInto("tick", 16)).NotTo(BeEmpty())
		Expect(index.Summary()).To(ContainSubstring("Indexed"))
	})

	It("resolves relative, dotted, multi, and dynamic imports", func() {
		byModule := map[string]string{
			"demo":      "src/demo/__init__.py",
			"demo.core": "src/demo/core.py",
			"demo.app":  "src/demo/app.py",
		}
		byRel := map[string]string{
			"src/demo/__init__.py": "src/demo/__init__.py",
			"src/demo/core.py":     "src/demo/core.py",
			"src/demo/app.py":      "src/demo/app.py",
		}

		Expect(resolvePythonModule("src/demo/app.py", ".core", byModule, byRel)).To(Equal("src/demo/core.py"))
		Expect(resolvePythonModule("src/demo/app.py", "demo.core", byModule, byRel)).To(Equal("src/demo/core.py"))
		Expect(resolvePythonModule("src/demo/app.py", "math", byModule, byRel)).To(BeEmpty())

		raw, resolved := parsePythonImports("src/demo/app.py", `
from . import plugin
import os, demo.core as core
import importlib

def load():
    return importlib.import_module("demo.plugin")
`, map[string]string{
			"demo":        "src/demo/__init__.py",
			"demo.core":   "src/demo/core.py",
			"demo.plugin": "src/demo/plugin.py",
		}, map[string]string{
			"src/demo/__init__.py": "src/demo/__init__.py",
			"src/demo/core.py":     "src/demo/core.py",
			"src/demo/plugin.py":   "src/demo/plugin.py",
		})
		Expect(raw).To(And(ContainElement(".plugin"), ContainElement("demo.core"), ContainElement("demo.plugin")))
		Expect(resolved).To(And(ContainElement("src/demo/plugin.py"), ContainElement("src/demo/core.py")))
	})

	DescribeTable("strips comments without altering hashes inside strings",
		func(input, expected string) {
			Expect(stripPythonComment(input)).To(Equal(expected))
		},
		Entry("trailing comment", `x = 1 # trailing`, `x = 1 `),
		Entry("hash in double-quoted string", `s = "has # inside"`, `s = "has # inside"`),
		Entry("hash in single-quoted string", `s = 'also # inside'`, `s = 'also # inside'`),
		Entry("whole-line comment", `# whole line comment`, ``),
		Entry("plain code", `plain code no comment`, `plain code no comment`),
	)
})

func writePythonFixture(path string, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())

	trimmed := strings.TrimLeft(content, "\n")
	Expect(os.WriteFile(path, []byte(trimmed), 0o644)).To(Succeed())
}
