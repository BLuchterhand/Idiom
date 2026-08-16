package agent

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Textual MCP arguments", func() {
	assertArguments := func(result ParseResult) {
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(result.Request.Tool).To(gomega.Equal("mcp:exa/web_search_exa"))

		var args map[string]any
		gomega.Expect(json.Unmarshal(result.Request.Args, &args)).To(gomega.Succeed())
		gomega.Expect(args["query"]).To(gomega.Equal("current date"))
		gomega.Expect(args["num_results"]).To(gomega.Equal(float64(10)))
	}

	ginkgo.It("preserves arbitrary fields in key-value MCP calls", func() {
		assertArguments(ParseWithAllTokenizers("tool: mcp:exa/web_search_exa\nquery: current date\nnum_results: 10"))
	})

	ginkgo.It("preserves arbitrary fields in MCP function calls", func() {
		assertArguments(ParseWithAllTokenizers(`tool: mcp:exa/web_search_exa(query="current date", num_results=10)`))
	})

	ginkgo.It("does not broaden arbitrary fields for workspace tools", func() {
		result := ParseWithAllTokenizers("tool: read_file\npath: README.md\nnum_results: 10")
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(result.Unsafe).To(gomega.BeTrue())
	})

})
