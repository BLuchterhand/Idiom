package workspace

import (
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("context enrichers", func() {
	ginkgo.It("normalize graph output path rejects backslash traversal", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(normalizeGraphOutputPath(`..\secret.go`, root)).To(
			gomega.BeEmpty(),
			"expected backslash traversal to be rejected",
		)

		gomega.Expect(normalizeGraphOutputPath(`internal\capabilities\workspace\manager.go`, root)).To(
			gomega.Equal("internal/capabilities/workspace/manager.go"),
			"expected backslash path to normalize",
		)
	})
})
