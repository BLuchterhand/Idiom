package languagetools_test

import (
	"context"
	"path/filepath"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Go declaration-shaped queries", func() {
	It("accepts a declaration pattern for find-symbol", func() {
		dir := GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "go.mod"), "module sample\n\ngo 1.22\n")
		writeTestFile(filepath.Join(dir, "runner.go"), `package sample

type Runner struct{}
`)

		manager := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
		result := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "go_find_symbol", Pattern: "type Runner struct"})
		Expect(result.Error).To(BeEmpty())
		Expect(result.Output).To(ContainSubstring("Runner"))
	})

	It("accepts a declaration pattern for read-symbol", func() {
		dir := GinkgoT().TempDir()
		writeTestFile(filepath.Join(dir, "go.mod"), "module sample\n\ngo 1.22\n")
		writeTestFile(filepath.Join(dir, "runner.go"), `package sample

type Runner struct{}

func (r *Runner) Run() {}
`)

		manager := workspace.NewManager(config.Workspace{Root: dir, CWD: dir})
		result := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "go_read_symbol", Pattern: "type Runner struct"})
		Expect(result.Error).To(BeEmpty())
		Expect(result.Output).To(ContainSubstring("type Runner struct"))
	})
})
