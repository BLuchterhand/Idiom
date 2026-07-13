package indexsupport

import (
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Semantic query normalization", func() {
	Describe("NormalizeSymbolPattern", func() {
		It("extracts a symbol from a matching declaration", func() {
			extractors := []*regexp.Regexp{regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`)}
			Expect(NormalizeSymbolPattern("func Hello", extractors)).To(Equal("Hello"))
		})

		It("preserves an unmatched pattern", func() {
			extractors := []*regexp.Regexp{regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`)}
			Expect(NormalizeSymbolPattern("Hello", extractors)).To(Equal("Hello"))
		})

		It("trims surrounding whitespace", func() {
			Expect(NormalizeSymbolPattern("  Hello  ", nil)).To(Equal("Hello"))
		})

		It("uses the first matching extractor", func() {
			extractors := []*regexp.Regexp{
				regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
				regexp.MustCompile(`^struct\s+([A-Za-z_]\w*)`),
			}
			Expect(NormalizeSymbolPattern("struct Foo", extractors)).To(Equal("Foo"))
		})
	})

	DescribeTable("strips line comments",
		func(input, expected string) {
			Expect(StripLineComment(input)).To(Equal(expected))
		},
		Entry("from a statement", "int x = 5; // comment", "int x = 5; "),
		Entry("when absent", "int x = 5;", "int x = 5;"),
		Entry("from empty input", "", ""),
		Entry("from a comment-only line", "// just a comment", ""),
	)

	DescribeTable("qualifies a symbol with a non-empty scope",
		func(scope, symbol, separator, expected string) {
			Expect(FirstQualified(scope, symbol, separator)).To(Equal(expected))
		},
		Entry("dot separator", "MyClass", "myMethod", ".", "MyClass.myMethod"),
		Entry("empty scope", "", "myMethod", ".", "myMethod"),
		Entry("whitespace scope", "  ", "myMethod", ".", "myMethod"),
		Entry("C++ separator", "ns", "func", "::", "ns::func"),
	)
})
