package discovery

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Discovery filtering", func() {
	It("skips the .idiom directory by name", func() {
		Expect(ShouldSkipDirName(".idiom")).To(BeTrue())
	})

	It("never skips the workspace root but skips its .idiom child", func() {
		root := GinkgoT().TempDir()
		Expect(ShouldSkipPath(root, root, true)).To(BeFalse())
		Expect(ShouldSkipPath(root, filepath.Join(root, ".idiom"), true)).To(BeTrue())
	})

	It("recognizes explicit targets inside .idiom as noisy", func() {
		Expect(RelTargetsNoisyDir(".idiom/discovery/context.md")).To(BeTrue())
	})

	It("excludes .idiom content from ripgrep discovery", func() {
		Expect(RipgrepExcludeGlobs()).To(ContainElement("!**/.idiom/**"))
	})
})
