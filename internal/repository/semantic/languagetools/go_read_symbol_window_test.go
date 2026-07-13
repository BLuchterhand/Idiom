package languagetools

import (
	semanticgo "idiom/internal/repository/semantic/golang"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Go read-symbol windows", func() {
	DescribeTable("selects the requested or inferred line range",
		func(match semanticgo.SymbolMatch, requestedStart, requestedEnd, expectedStart, expectedEnd int) {
			start, end := goReadSymbolWindow(match, requestedStart, requestedEnd)
			Expect(start).To(Equal(expectedStart))
			Expect(end).To(Equal(expectedEnd))
		},
		Entry("uses the symbol extent", semanticgo.SymbolMatch{Line: 16, StartLine: 16, EndLine: 132}, 0, 0, 16, 132),
		Entry("honors an explicit request", semanticgo.SymbolMatch{Line: 16, StartLine: 16, EndLine: 132}, 50, 70, 50, 70),
		Entry("centers a fallback window", semanticgo.SymbolMatch{Line: 200}, 0, 0, 188, 220),
		Entry("caps long symbols", semanticgo.SymbolMatch{Line: 16, StartLine: 16, EndLine: 516}, 0, 0, 16, 135),
	)
})
