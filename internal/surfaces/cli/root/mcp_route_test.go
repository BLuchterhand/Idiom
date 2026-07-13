package root

import (
	"bytes"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("MCP command routing", func() {
	ginkgo.It("exposes mcp serve and requires the stdio transport", func() {
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"mcp", "serve", "--profile", "discovery"}, &stdout, &stderr)

		gomega.Expect(code).To(gomega.Equal(2), "stdout=%s stderr=%s", stdout.String(), stderr.String())
		gomega.Expect(stderr.String()).To(gomega.Or(
			gomega.ContainSubstring("requires --stdio"),
			gomega.ContainSubstring("--stdio"),
		))
	})
})
