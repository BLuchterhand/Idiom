package textutil

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("String utilities", func() {
	ginkgo.DescribeTable("returns the first non-empty trimmed value",
		func(values []string, want string) {
			gomega.Expect(FirstNonEmpty(values...)).To(gomega.Equal(want))
		},
		ginkgo.Entry("later value", []string{"", "  ", "hello", "world"}, "hello"),
		ginkgo.Entry("all empty", []string{"", "  "}, ""),
		ginkgo.Entry("first value", []string{"first", "second"}, "first"),
	)

	ginkgo.DescribeTable("detects any substring",
		func(haystack string, needles []string, want bool) {
			gomega.Expect(ContainsAny(haystack, needles...)).To(gomega.Equal(want))
		},
		ginkgo.Entry("present", "hello world", []string{"world", "foo"}, true),
		ginkgo.Entry("absent", "hello", []string{"world", "foo"}, false),
		ginkgo.Entry("empty needle", "hello", []string{""}, true),
	)

	ginkgo.It("returns normalized unique non-empty values", func() {
		gomega.Expect(UniqueNonEmpty([]string{"a", "  b  ", "a", "", "b", "c"})).To(gomega.Equal([]string{"a", "b", "c"}))
		gomega.Expect(UniqueNonEmpty(nil)).To(gomega.BeEmpty())
	})

	ginkgo.DescribeTable("compares trimmed values without case",
		func(left, right string, want bool) {
			gomega.Expect(EqualTrimFold(left, right)).To(gomega.Equal(want))
		},
		ginkgo.Entry("equal", "  Hello  ", "hello", true),
		ginkgo.Entry("different", "hello", "world", false),
	)

	ginkgo.It("returns non-empty trimmed lines", func() {
		gomega.Expect(NonEmptyTrimmedLines("  a  \n\n  b  \n  c  ")).To(gomega.Equal([]string{"a", "b", "c"}))
		gomega.Expect(NonEmptyTrimmedLines("")).To(gomega.BeEmpty())
	})

	ginkgo.DescribeTable("uses a fallback for empty primary values",
		func(primary, alternative, want string) {
			gomega.Expect(Fallback(primary, alternative)).To(gomega.Equal(want))
		},
		ginkgo.Entry("primary", "hello", "alt", "hello"),
		ginkgo.Entry("empty", "", "alt", "alt"),
		ginkgo.Entry("whitespace", "   ", "alt", "alt"),
		ginkgo.Entry("both empty", "", "", ""),
	)

	ginkgo.DescribeTable("finds non-empty substrings without case",
		func(haystack, needle string, want bool) {
			gomega.Expect(ContainsFold(haystack, needle)).To(gomega.Equal(want))
		},
		ginkgo.Entry("lowercase", "Hello World", "hello", true),
		ginkgo.Entry("uppercase", "Hello World", "WORLD", true),
		ginkgo.Entry("absent", "Hello World", "foo", false),
		ginkgo.Entry("empty", "Hello World", "", false),
		ginkgo.Entry("whitespace", "Hello World", "   ", false),
	)
})
