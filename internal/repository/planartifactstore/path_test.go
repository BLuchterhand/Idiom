package planartifactstore

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan artifact paths", func() {
	DescribeTable("rejects invalid artifact names",
		func(name string) {
			_, _, err := StoreFilePath(GinkgoT().TempDir(), name)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("whitespace", " "),
		Entry("parent traversal", "../plan.json"),
		Entry("nested path", "nested/plan.json"),
		Entry("NUL byte", "bad\x00name.json"),
	)

	It("builds a contained artifact path", func() {
		root := GinkgoT().TempDir()
		full, rel, err := StoreFilePath(root, "plan.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(rel).To(Equal(filepath.ToSlash(filepath.Join(".idiom", "plans", "plan.json"))))
		Expect(strings.HasPrefix(full, filepath.Clean(root)+string(os.PathSeparator))).To(BeTrue())
	})

	It("builds a contained asset path", func() {
		_, rel, err := StoreAssetPath(GinkgoT().TempDir(), "evidence.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(rel).To(Equal(filepath.ToSlash(filepath.Join(".idiom", "plans", "assets", "evidence.json"))))
	})

	It("rejects a symlinked plans directory", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(Succeed())
		err := os.Symlink(GinkgoT().TempDir(), filepath.Join(root, ".idiom", "plans"))
		if err != nil {
			Skip("symlinks are unavailable: " + err.Error())
		}

		_, _, err = StoreDir(root)
		Expect(err).To(HaveOccurred())
	})
})
