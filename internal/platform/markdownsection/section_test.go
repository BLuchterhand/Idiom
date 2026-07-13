package markdownsection

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Markdown sections", func() {
	ginkgo.It("returns the requested section including nested headings", func() {
		markdown := strings.Join([]string{
			"# Title",
			"intro",
			"## Required Edits",
			"- one",
			"### Nested",
			"- nested",
			"## Next",
			"done",
		}, "\n")

		gomega.Expect(ByHeading(markdown, "required edits")).To(gomega.Equal("- one\n### Nested\n- nested"))
	})

	ginkgo.It("ignores heading-like text inside code fences", func() {
		markdown := strings.Join([]string{
			"```md",
			"## Required Edits",
			"ignored",
			"```",
			"## Required Edits",
			"kept",
		}, "\n")

		gomega.Expect(ByHeading(markdown, "required edits")).To(gomega.Equal("kept"))
	})

	ginkgo.It("matches headings with qualifiers", func() {
		markdown := "## Required Edits (phase 1)\nbody\n## Other\nnext"
		gomega.Expect(ByHeading(markdown, "required edits")).To(gomega.Equal("body"))
	})
})
