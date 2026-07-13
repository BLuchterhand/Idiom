package implementationdispatch

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Workspace plan paths", func() {
	ginkgo.It("rejects unsafe paths and accepts workspace-relative paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		for _, path := range []string{"../outside.md", `C:\repo\outside.md`, "/tmp/outside.md"} {
			got, ok := WorkspaceFile(root, path)
			gomega.Expect(ok).To(gomega.BeFalse(), "path: %s; resolved: %s", path, got)
		}
		got, ok := WorkspaceFile(root, "docs/new.md")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(got).NotTo(gomega.BeEmpty())
	})
})
