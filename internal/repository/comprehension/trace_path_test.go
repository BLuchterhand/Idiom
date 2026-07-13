package comprehension

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("normalize trace path rejects traversal and normalizes backslashes", func() {
	root := GinkgoT().TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		Expect(err).NotTo(HaveOccurred(), err)
	}
	{
		err := os.WriteFile(filepath.Join(root, "pkg", "runner.go"), []byte("package pkg\n"), 0o644)
		Expect(err).NotTo(HaveOccurred(), err)
	}

	outside := filepath.Join(filepath.Dir(root), "outside.go")
	{
		err := os.WriteFile(outside, []byte("package outside\n"), 0o644)
		Expect(err).NotTo(HaveOccurred(), err)
	}
	{
		got, ok := normalizeTracePath(root, `pkg\runner.go`)
		Expect(ok).To(BeTrue(), "normalizeTracePath(backslash path) = %q, %v; want pkg/runner.go, true", got, ok)
		Expect(got).To(BeEquivalentTo("pkg/runner.go"), "normalizeTracePath(backslash path) = %q, %v; want pkg/runner.go, true", got, ok)
	}
	{
		got, ok := normalizeTracePath(root, "../outside.go")
		Expect(ok).To(BeFalse(), "normalizeTracePath(traversal) = %q, true; want rejection", got)
	}
})
