package instructions

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Instruction attacher candidates", func() {
	var root string

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "src"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("Claude-specific guidance\n"), 0o644)).To(Succeed())
	})

	It("does not load CLAUDE.md by default", func() {
		Expect(NewAttacher(root).OnFileTouch("src/main.go")).To(BeNil())
	})

	It("loads CLAUDE.md when explicitly configured", func() {
		attachment := NewAttacher(root, "CLAUDE.md").OnFileTouch("src/main.go")
		Expect(attachment).NotTo(BeNil())
		Expect(attachment.Path).To(HaveSuffix("CLAUDE.md"))
		Expect(attachment.Content).To(ContainSubstring("Claude-specific guidance"))
	})
})
