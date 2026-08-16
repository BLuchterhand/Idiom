package workspace

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"idiom/internal/contracts/toolfacts"
)

var _ = ginkgo.Describe("external web evidence", func() {
	ginkgo.It("records search results as candidate source identities", func() {
		parts := []json.RawMessage{json.RawMessage(`{"type":"text","text":"{\"results\":[{\"title\":\"Example article\",\"url\":\"https://example.com/article\",\"publisher\":\"Example\"}]}"}`)}

		got := externalWebSourceEvidenceForMCP("mcp:exa/web_search_exa", "", parts)

		gomega.Expect(got).To(gomega.Equal([]toolfacts.ExternalWebSourceEvidence{{
			Kind:         toolfacts.ExternalWebSourceCandidate,
			URL:          "https://example.com/article",
			CanonicalURL: "https://example.com/article",
			Title:        "Example article",
			Publisher:    "Example",
		}}))
	})

	ginkgo.It("records fetched source bodies separately from search candidates", func() {
		parts := []json.RawMessage{json.RawMessage(`{"type":"text","text":"Source: [Example article](https://example.com/article)\nFetched body"}`)}

		got := externalWebSourceEvidenceForMCP("mcp:exa/web_fetch_exa", "https://example.com/article", parts)

		gomega.Expect(got).To(gomega.Equal([]toolfacts.ExternalWebSourceEvidence{{
			Kind:         toolfacts.ExternalWebSourceBody,
			URL:          "https://example.com/article",
			CanonicalURL: "https://example.com/article",
			Title:        "Example article",
		}}))
	})

	ginkgo.It("does not classify non-web MCP output as web source evidence", func() {
		got := externalWebSourceEvidenceForMCP("mcp:other/echo", "", []json.RawMessage{json.RawMessage(`{"type":"text","text":"https://example.com"}`)})

		gomega.Expect(got).To(gomega.BeEmpty())
	})

	ginkgo.It("does not promote outbound links from a fetched body", func() {
		parts := []json.RawMessage{json.RawMessage(`{"type":"text","text":"Fetched https://example.com/article and linked https://example.com/other"}`)}

		got := externalWebSourceEvidenceForMCP("mcp:exa/web_fetch_exa", "https://example.com/article", parts)

		gomega.Expect(got).To(gomega.Equal([]toolfacts.ExternalWebSourceEvidence{{
			Kind:         toolfacts.ExternalWebSourceBody,
			URL:          "https://example.com/article",
			CanonicalURL: "https://example.com/article",
		}}))
	})
})
