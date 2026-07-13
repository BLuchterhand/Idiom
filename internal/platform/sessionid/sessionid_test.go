package sessionid

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Session IDs", func() {
	ginkgo.It("accepts IDs produced by the generator", func() {
		gomega.Expect(Validate(New())).To(gomega.Succeed())
	})

	ginkgo.DescribeTable("rejects path components and punctuation",
		func(id string) {
			gomega.Expect(Validate(id)).To(gomega.HaveOccurred())
			normalized, ok := Normalize(id)
			gomega.Expect(ok).To(gomega.BeFalse())
			gomega.Expect(normalized).To(gomega.BeEmpty())
		},
		ginkgo.Entry("empty", ""),
		ginkgo.Entry("blank", " "),
		ginkgo.Entry("current directory", "."),
		ginkgo.Entry("parent directory", ".."),
		ginkgo.Entry("forward slash", "a/b"),
		ginkgo.Entry("backslash", `a\b`),
		ginkgo.Entry("double dot", "a..b"),
		ginkgo.Entry("dot", "with.dot"),
		ginkgo.Entry("space", "with space"),
		ginkgo.Entry("at sign", "with@sign"),
	)

	ginkgo.It("trims a valid ID during normalization", func() {
		normalized, ok := Normalize(" session_123 ")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(normalized).To(gomega.Equal("session_123"))
	})
})
