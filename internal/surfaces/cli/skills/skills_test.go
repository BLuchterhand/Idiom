package skills

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("idiom skills", func() {
	ginkgo.It("lists the embedded orchestrator workflow", func() {
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{"list"}, &stdout, &stderr)).To(gomega.Equal(0), stderr.String())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("orchestrator-workflow"))
	})

	ginkgo.It("installs a known skill into the default project target", func() {
		root := ginkgo.GinkgoT().TempDir()
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain(
			[]string{"install", "--project-root", root, "orchestrator-workflow"},
			&stdout,
			&stderr,
		)).To(gomega.Equal(0), stderr.String())
		body, err := os.ReadFile(filepath.Join(root, ".claude", "skills", "orchestrator-workflow", "SKILL.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(bytes.TrimSpace(body)).NotTo(gomega.BeEmpty())
		gomega.Expect(body).To(gomega.ContainSubstring("Orchestrator Workflow"))
	})

	ginkgo.It("installs into a custom target", func() {
		root := ginkgo.GinkgoT().TempDir()
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{
			"install",
			"--project-root", root,
			"--target", ".codex/skills",
			"orchestrator-workflow",
		}, &stdout, &stderr)).To(gomega.Equal(0))
		_, err := os.Stat(filepath.Join(root, ".codex", "skills", "orchestrator-workflow", "SKILL.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})

	ginkgo.It("refuses to overwrite a customized skill without force", func() {
		root := ginkgo.GinkgoT().TempDir()
		skillDir := filepath.Join(root, ".claude", "skills", "orchestrator-workflow")
		gomega.Expect(os.MkdirAll(skillDir, 0o755)).To(gomega.Succeed())
		sentinel := []byte("CUSTOMIZED-BY-OPERATOR")
		path := filepath.Join(skillDir, "SKILL.md")
		gomega.Expect(os.WriteFile(path, sentinel, 0o644)).To(gomega.Succeed())
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain(
			[]string{"install", "--project-root", root, "orchestrator-workflow"},
			&stdout,
			&stderr,
		)).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--force"))
		preserved, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(preserved).To(gomega.Equal(sentinel))
	})

	ginkgo.It("overwrites an existing skill when forced", func() {
		root := ginkgo.GinkgoT().TempDir()
		skillDir := filepath.Join(root, ".claude", "skills", "orchestrator-workflow")
		gomega.Expect(os.MkdirAll(skillDir, 0o755)).To(gomega.Succeed())
		path := filepath.Join(skillDir, "SKILL.md")
		gomega.Expect(os.WriteFile(path, []byte("OLD"), 0o644)).To(gomega.Succeed())
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain([]string{
			"install",
			"--project-root", root,
			"--force",
			"orchestrator-workflow",
		}, &stdout, &stderr)).To(gomega.Equal(0), stderr.String())
		body, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(body).NotTo(gomega.Equal([]byte("OLD")))
	})

	ginkgo.It("rejects an unknown embedded skill", func() {
		root := ginkgo.GinkgoT().TempDir()
		var stdout, stderr bytes.Buffer

		gomega.Expect(RunMain(
			[]string{"install", "--project-root", root, "no-such-skill"},
			&stdout,
			&stderr,
		)).To(gomega.Equal(1))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown skill"))
	})

	ginkgo.DescribeTable("rejects unsafe skill names",
		func(name string) {
			root := ginkgo.GinkgoT().TempDir()
			var stdout, stderr bytes.Buffer
			gomega.Expect(RunMain(
				[]string{"install", "--project-root", root, name},
				&stdout,
				&stderr,
			)).To(gomega.Equal(2))
		},
		ginkgo.Entry("parent traversal", "../../etc/passwd"),
		ginkgo.Entry("path separator", "a/b"),
		ginkgo.Entry("parent directory", ".."),
		ginkgo.Entry("NUL byte", "x\x00y"),
	)
})
