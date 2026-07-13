package runrecord

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Run IDs", func() {
	ginkgo.DescribeTable("accepts valid IDs",
		func(id string) {
			gomega.Expect(ValidateRunID(id)).To(gomega.Succeed())
		},
		ginkgo.Entry("timestamp", "20260621-120000"),
		ginkgo.Entry("hyphen", "run_ok-1"),
		ginkgo.Entry("underscore", "run_ok_2"),
	)

	ginkgo.DescribeTable("rejects unsafe or malformed IDs",
		func(id string) {
			gomega.Expect(ValidateRunID(id)).To(gomega.HaveOccurred())
		},
		ginkgo.Entry("empty", ""),
		ginkgo.Entry("parent traversal", "../escape"),
		ginkgo.Entry("forward slash", "a/b"),
		ginkgo.Entry("backslash", `a\b`),
		ginkgo.Entry("dot", "run.ok_2"),
		ginkgo.Entry("space", "space here"),
		ginkgo.Entry("too long", strings.Repeat("a", 129)),
	)

	ginkgo.It("generates a valid ID", func() {
		gomega.Expect(ValidateRunID(NewRunID())).To(gomega.Succeed())
	})
})
