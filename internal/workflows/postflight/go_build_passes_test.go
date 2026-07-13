package postflight

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Go build postflight check", func() {
	It("reports compile errors", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gobuildpasses_test\n\ngo 1.21\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { undefinedFunc() }\n"), 0o644)).To(Succeed())

		findings, err := GoBuildPasses{}.Run(Diff{Files: []FileChange{{Path: "main.go"}}}, RunContext{ProjectRoot: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).NotTo(BeEmpty())
		Expect(findings[0].Check).To(Equal("go_build_passes"))
		Expect(findings[0].Severity).To(Equal(SeverityError))
		Expect(strings.ToLower(findings[0].Message)).To(ContainSubstring("undefined"))
	})

	It("passes a valid Go module", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gobuildpasses_valid\n\ngo 1.21\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)).To(Succeed())

		findings, err := GoBuildPasses{}.Run(Diff{Files: []FileChange{{Path: "main.go"}}}, RunContext{ProjectRoot: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(BeEmpty())
	})

	It("does nothing for a non-Go diff", func() {
		findings, err := GoBuildPasses{}.Run(Diff{Files: []FileChange{{Path: "README.md"}}}, RunContext{ProjectRoot: "/tmp"})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(BeNil())
	})

	It("does nothing without a go.mod", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)).To(Succeed())

		findings, err := GoBuildPasses{}.Run(Diff{Files: []FileChange{{Path: "main.go"}}}, RunContext{ProjectRoot: dir})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(BeNil())
	})
})
