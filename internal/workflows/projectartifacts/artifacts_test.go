package projectartifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Project discovery artifacts", func() {
	ginkgo.It("generates the expected artifact set and context", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeProjectFile(filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.23.0\n")
		writeProjectFile(filepath.Join(root, "README.md"), "# Demo\n\n## API\n\nAlways verify focused packages.\n")
		writeProjectFile(filepath.Join(root, "AGENTS.md"), "Prefer semantic tools.\nNever guess paths.\n")
		writeProjectFile(filepath.Join(root, "internal", "app", "app.go"), "package app\n\nfunc Run() {}\n")
		writeProjectFile(filepath.Join(root, "cmd", "demo", "main.go"), "package main\n\nfunc main() {}\n")

		contextPath, err := Ensure(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(contextPath).To(gomega.Equal(filepath.Join(root, filepath.FromSlash(contextRel))))
		for _, relative := range []string{
			discoveryDir + "/manifest.json",
			discoveryDir + "/modules.jsonl",
			discoveryDir + "/glossary.json",
			discoveryDir + "/workflows.json",
			discoveryDir + "/decisions.jsonl",
			discoveryDir + "/impact/root.json",
			contextRel,
		} {
			gomega.Expect(filepath.Join(root, filepath.FromSlash(relative))).To(gomega.BeAnExistingFile())
		}

		manifest := readManifest(filepath.Join(root, filepath.FromSlash(discoveryDir), "manifest.json"))
		gomega.Expect(manifest.Languages).To(gomega.Equal([]string{"go"}))
		data, err := os.ReadFile(contextPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		for _, want := range []string{
			"Generated Project Context", "Prompt Files", "Active Languages", "Module Map",
			"Workflow Recipes", "Decision Memory", "`cmd`", "`internal`",
		} {
			gomega.Expect(string(data)).To(gomega.ContainSubstring(want))
		}
	})

	ginkgo.It("does not regenerate when only generated artifacts changed", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeProjectFile(filepath.Join(root, "README.md"), "# Demo\n\nAlways verify.\n")
		contextPath, err := Ensure(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		first, err := os.Stat(contextPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		time.Sleep(20 * time.Millisecond)
		contextPath, err = Ensure(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		second, err := os.Stat(contextPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(second.ModTime()).To(gomega.Equal(first.ModTime()))
	})

	ginkgo.It("regenerates when project source changes", func() {
		root := ginkgo.GinkgoT().TempDir()
		sourcePath := filepath.Join(root, "README.md")
		writeProjectFile(sourcePath, "# Demo\n\nAlways verify.\n")
		contextPath, err := Ensure(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		firstContext, err := os.ReadFile(contextPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		firstManifest := readManifest(filepath.Join(root, filepath.FromSlash(discoveryDir), "manifest.json"))

		time.Sleep(20 * time.Millisecond)
		writeProjectFile(sourcePath, "# Demo\n\nAlways verify.\n\n## ADR\n\nNever skip focused verification.\n")
		contextPath, err = Ensure(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		secondContext, err := os.ReadFile(contextPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		secondManifest := readManifest(filepath.Join(root, filepath.FromSlash(discoveryDir), "manifest.json"))

		gomega.Expect(secondContext).NotTo(gomega.Equal(firstContext))
		gomega.Expect(secondManifest.Fingerprint).NotTo(gomega.Equal(firstManifest.Fingerprint))
		gomega.Expect(string(secondContext)).To(gomega.ContainSubstring("Never skip focused verification."))
	})
})

func writeProjectFile(path, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())
}

func readManifest(path string) Manifest {
	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var manifest Manifest
	gomega.Expect(json.Unmarshal(data, &manifest)).To(gomega.Succeed())
	return manifest
}
