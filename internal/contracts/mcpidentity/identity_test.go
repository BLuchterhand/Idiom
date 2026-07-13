package mcpidentity

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("MCP identity", func() {
	ginkgo.DescribeTable("normalizes server names",
		func(value string, expected string, expectError bool) {
			actual, err := NormalizeServerName(value)
			if expectError {
				gomega.Expect(err).To(gomega.HaveOccurred())
			} else {
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
			}
			gomega.Expect(actual).To(gomega.Equal(expected))
		},
		ginkgo.Entry("trims whitespace", " docs ", "docs", false),
		ginkgo.Entry("rejects an empty name", "  ", "", true),
		ginkgo.Entry("rejects the reserved separator", "team/docs", "", true),
	)

	ginkgo.It("round-trips qualified tool names", func() {
		qualified := QualifiedToolName(" docs ", " summarize ")
		gomega.Expect(qualified).To(gomega.Equal("mcp:docs/summarize"))

		server, tool, ok := ParseQualifiedToolName(qualified)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(server).To(gomega.Equal("docs"))
		gomega.Expect(tool).To(gomega.Equal("summarize"))
	})

	ginkgo.DescribeTable("rejects invalid qualified tool names",
		func(value string) {
			_, _, ok := ParseQualifiedToolName(value)
			gomega.Expect(ok).To(gomega.BeFalse())
		},
		ginkgo.Entry("unqualified", "plain_tool"),
		ginkgo.Entry("missing tool", "mcp:only-server"),
		ginkgo.Entry("missing server", "mcp:/tool"),
		ginkgo.Entry("empty tool", "mcp:server/"),
	)
})
