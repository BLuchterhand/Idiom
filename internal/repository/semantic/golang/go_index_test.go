package golang

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Go semantic index", func() {
	It("indexes files, declarations, calls, packages, and entrypoints", func() {
		root := GinkgoT().TempDir()
		writeSemanticFile(filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")
		writeSemanticFile(filepath.Join(root, "cmd", "demo", "main.go"), `package main

import "example.com/demo/internal/service"

func main() {
	service.Run()
}
`)
		writeSemanticFile(filepath.Join(root, "internal", "service", "service.go"), `package service

type Runner struct{}

const DefaultName = "demo"

func Run() {
	helper()
}

func helper() {}

func (Runner) Start() {
	Run()
}
`)
		writeSemanticFile(filepath.Join(root, "internal", "service", "service_test.go"), `package service

func TestRun() {
	Run()
}
`)

		idx, err := BuildGoIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).NotTo(BeNil())
		Expect(idx.Module).To(Equal("example.com/demo"))
		Expect(idx.Files).To(HaveLen(3))
		Expect(idx.Summary()).To(And(ContainSubstring("Go module: example.com/demo"), ContainSubstring("Representative symbols")))

		symbols := idx.FindSymbols("Run", 5)
		Expect(symbols).NotTo(BeEmpty())
		Expect(symbols[0].Name).To(Equal("Run"))
		symbols = idx.FindSymbols("type Runner struct", 5)
		Expect(symbols).NotTo(BeEmpty())
		Expect(symbols[0].Name).To(Equal("Runner"))
		symbols = idx.FindSymbols("func (Runner) Start", 5)
		Expect(symbols).NotTo(BeEmpty())
		Expect(symbols[0].Name).To(Equal("Start"))

		types := idx.FindSymbolsByKind("Runner", "type", 2)
		Expect(types).To(HaveLen(1))
		Expect(types[0].Kind).To(Equal("type"))
		packages := idx.FindPackages("service", 2)
		Expect(packages).To(HaveLen(1))
		Expect(packages[0].Path).To(Equal("internal/service"))
		calls := idx.FindCallsFrom("Run", 4)
		Expect(calls).NotTo(BeEmpty())
		Expect(calls[0].CalleeName).To(Equal("helper"))
		Expect(len(idx.FindCallsInto("Run", 8))).To(BeNumerically(">=", 2))
		defaultFiles := idx.DefaultFiles(2)
		Expect(defaultFiles).NotTo(BeEmpty())
		Expect(defaultFiles[0]).To(Equal("cmd/demo/main.go"))
		Expect(idx.RelevantFiles([]string{"runner"}, 2)).NotTo(BeEmpty())
		Expect(idx.RelevantFiles([]string{"runner"}, 2)[0]).To(Equal("internal/service/service.go"))
	})

	It("returns no index for an empty root", func() {
		idx, err := BuildGoIndex("")
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(BeNil())
	})

	It("skips discovery-noise directories", func() {
		root := GinkgoT().TempDir()
		writeSemanticFile(filepath.Join(root, "go.mod"), "module example.com/noise\n")
		writeSemanticFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "app.go"), "package app\nfunc Real() {}\n")
		writeSemanticFile(filepath.Join(root, ".claude", "worktrees", "old", "hidden.go"), "package hidden\nfunc Hidden() {}\n")
		writeSemanticFile(filepath.Join(root, ".planning", "scratch.go"), "package scratch\nfunc Scratch() {}\n")

		idx, err := BuildGoIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).NotTo(BeNil())
		Expect(idx.Files).To(HaveLen(1))
		Expect(idx.Files[0].Path).To(Equal("internal/surfaces/tui/app/app.go"))
		Expect(idx.FindSymbols("Hidden", 5)).To(BeEmpty())
	})
})

func writeSemanticFile(path string, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}
