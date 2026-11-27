package comprehension

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("node term match score", func() {
	It("ranks a namespace-qualified exact bare name above a namespace-token-only match", func() {
		exact := Node{Kind: "type", Path: "client/ambience.h", QualifiedName: "bt::client::Ambience",
			Properties: map[string]any{"name": "Ambience"}}
		nsOnly := Node{Kind: "type", Path: "client/wind_clipmap.h", QualifiedName: "bt::client::WindClipmap",
			Properties: map[string]any{"name": "WindClipmap"}}

		Expect(nodeTermMatchScore(exact, "ambience")).To(BeNumerically(">=", 80), "qualified node with bare name == term")

		e, n := nodeTermMatchScore(exact, "ambience"), nodeTermMatchScore(nsOnly, "client")
		Expect(e).To(BeNumerically(">", n), "exact bare-name match must outrank namespace-token-only match")
	})
})
