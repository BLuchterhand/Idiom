package init

import (
	"bytes"
	"os"
	"path/filepath"

	platformconfig "idiom/internal/platform/config"
	"idiom/internal/runtime/backends"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var expectedSeedFiles = []string{
	"config.json",
	"backends.json",
	"external_tools/README.md",
	"postflight/checks/README.md",
	"prompts/base.md",
	"prompts/lanes/README.md",
	"prompts/rules/README.md",
	"prompts/profiles/coding.md",
	"prompts/profiles/claude-qwen-worker.md",
}

var _ = ginkgo.Describe("idiom init", func() {
	ginkgo.It("creates the complete project skeleton in a fresh directory", func() {
		root := ginkgo.GinkgoT().TempDir()
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{root}, &stdout, &stderr)).To(gomega.Equal(0), stderr.String())
		for _, rel := range expectedSeedFiles {
			_, err := os.Stat(filepath.Join(root, ".idiom", rel))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "missing seed file %s", rel)
		}
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Initialized .idiom/"))
	})

	ginkgo.It("refuses to clobber an existing project configuration", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{root}, &stdout, &stderr)).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.SatisfyAll(
			gomega.ContainSubstring("already exists"),
			gomega.ContainSubstring("--merge"),
		))
	})

	ginkgo.It("merges missing project files without overwriting existing files", func() {
		root := ginkgo.GinkgoT().TempDir()
		dotIdiom := filepath.Join(root, ".idiom")
		gomega.Expect(os.MkdirAll(dotIdiom, 0o755)).To(gomega.Succeed())
		sentinel := []byte(`{"app_name": "PRESERVED-BY-MERGE"}`)
		gomega.Expect(os.WriteFile(filepath.Join(dotIdiom, "config.json"), sentinel, 0o644)).To(gomega.Succeed())
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{"--merge", root}, &stdout, &stderr)).To(gomega.Equal(0), stderr.String())
		preserved, err := os.ReadFile(filepath.Join(dotIdiom, "config.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(preserved).To(gomega.Equal(sentinel))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("(skipped existing"))
		_, err = os.Stat(filepath.Join(dotIdiom, "backends.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})

	ginkgo.It("creates the global user configuration skeleton directly under the home directory", func() {
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{"--global", "--merge"}, &stdout, &stderr)).To(gomega.Equal(0), stderr.String())
		globalDir := filepath.Join(home, ".idiom")
		for _, rel := range expectedSeedFiles {
			_, err := os.Stat(filepath.Join(globalDir, rel))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "missing global seed file %s", rel)
		}
		_, err := os.Stat(filepath.Join(globalDir, ".idiom"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())

		cfg, err := platformconfig.Load(filepath.Join(ginkgo.GinkgoT().TempDir(), "missing-config.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cfg.LlamaServer.Kind).To(gomega.Equal(platformconfig.BackendDeepSeek))
		gomega.Expect(cfg.Chat.SystemPromptFiles).NotTo(gomega.BeEmpty())
		gomega.Expect(cfg.Chat.SystemPromptFiles[0]).To(gomega.ContainSubstring(filepath.Join(".idiom", "prompts", "base.md")))

		backendFile, err := backends.Load(filepath.Join(globalDir, "backends.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(backendFile.DefaultProfile).To(gomega.Equal("deepseek-v4-flash"))
	})

	ginkgo.It("preserves an existing global configuration during merge", func() {
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)
		globalDir := filepath.Join(home, ".idiom")
		gomega.Expect(os.MkdirAll(globalDir, 0o755)).To(gomega.Succeed())
		sentinel := []byte(`{"app_name":"PRESERVED-GLOBAL"}`)
		gomega.Expect(os.WriteFile(filepath.Join(globalDir, "config.json"), sentinel, 0o644)).To(gomega.Succeed())
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{"--global", "--merge"}, &stdout, &stderr)).To(gomega.Equal(0), stderr.String())
		preserved, err := os.ReadFile(filepath.Join(globalDir, "config.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(preserved).To(gomega.Equal(sentinel))
		_, err = os.Stat(filepath.Join(globalDir, "backends.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})

	ginkgo.It("rejects a target directory in global mode", func() {
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{"--global", ginkgo.GinkgoT().TempDir()}, &stdout, &stderr)).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--global does not accept"))
	})

	ginkgo.It("seeds non-empty prompt templates", func() {
		root := ginkgo.GinkgoT().TempDir()
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{root}, &stdout, &stderr)).To(gomega.Equal(0), stderr.String())
		for _, rel := range []string{
			"prompts/base.md",
			"prompts/profiles/coding.md",
			"prompts/profiles/claude-qwen-worker.md",
		} {
			body, err := os.ReadFile(filepath.Join(root, ".idiom", rel))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(bytes.TrimSpace(body)).NotTo(gomega.BeEmpty(), "seeded %s is empty", rel)
		}
	})

	ginkgo.It("rejects a missing target directory as an operator error", func() {
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{"/definitely/no/such/path/idiom-init-test"}, &stdout, &stderr)).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("does not exist"))
	})
})
