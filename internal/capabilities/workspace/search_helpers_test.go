package workspace

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"idiom/internal/platform/config"
)

var _ = Describe("content search walker", func() {
	It("preserves scoped matches while skipping noisy directories", func() {
		requireContentSearchBackend()
		root := GinkgoT().TempDir()
		for _, dir := range []string{"src", "bin", ".git"} {
			Expect(os.MkdirAll(filepath.Join(root, dir), 0o755)).To(Succeed())
		}
		Expect(os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("needle-42\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "bin", "generated.go"), []byte("needle-42\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, ".git", "config.go"), []byte("needle-42\n"), 0o644)).To(Succeed())

		manager := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 5, MaxDirectoryEntries: 10})
		result := manager.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSearchContent,
			Path:    ".",
			Pattern: "needle-[0-9]+",
			Glob:    "**/*.go",
		})

		Expect(result.Error).To(BeEmpty(), result.Output)
		Expect(result.Output).To(ContainSubstring("src/main.go"))
		Expect(result.Output).NotTo(ContainSubstring("bin/generated.go"))
		Expect(result.Output).NotTo(ContainSubstring(".git/config.go"))
	})

	It("keeps Go regexp matching and exact line formatting", func() {
		requireContentSearchBackend()
		root := GinkgoT().TempDir()
		path := filepath.Join(root, "main.go")
		Expect(os.WriteFile(path, []byte("first\nneedle-7\nthird\nfourth\nfifth\nsixth\nseventh\neighth\nninth\nneedle-word\n"), 0o644)).To(Succeed())

		manager := NewManager(config.Workspace{Root: root, CWD: root, CommandTimeoutSeconds: 5, MaxDirectoryEntries: 10})
		result := manager.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameSearchContent,
			Path:    ".",
			Pattern: "needle-[0-9]+",
			Glob:    "*.go",
		})

		Expect(result.Error).To(BeEmpty(), result.Output)
		Expect(result.Summary).To(ContainSubstring("Found 1 matching line"))
		Expect(result.Output).To(ContainSubstring("main.go:2:needle-7"))
		Expect(result.Output).NotTo(ContainSubstring("main.go:10:needle-word"))
	})
})
