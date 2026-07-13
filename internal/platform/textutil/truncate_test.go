package textutil

import (
	"unicode/utf8"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Byte-safe truncation", func() {
	ginkgo.DescribeTable("truncates with a marker without splitting UTF-8",
		func(input string, maxBytes int, marker, want string) {
			got := TruncateBytes(input, maxBytes, marker)
			gomega.Expect(got).To(gomega.Equal(want))
			gomega.Expect(utf8.ValidString(got)).To(gomega.BeTrue())
		},
		ginkgo.Entry("shorter than limit", "hello", 10, "...", "hello"),
		ginkgo.Entry("exactly at limit", "hello", 5, "...", "hello"),
		ginkgo.Entry("longer than limit", "hello world", 8, "...", "hello..."),
		ginkgo.Entry("empty", "", 5, "...", ""),
		ginkgo.Entry("zero limit", "hello", 0, "...", "hello"),
		ginkgo.Entry("negative limit", "hello", -1, "...", "hello"),
		ginkgo.Entry("limit smaller than marker", "hello", 2, "...", "he"),
		ginkgo.Entry("limit equals marker", "hello", 3, "...", "hel"),
		ginkgo.Entry("custom marker", "hello world", 10, "…", "hello w…"),
		ginkgo.Entry("empty marker", "hello world", 5, "", "hello"),
		ginkgo.Entry("single-byte limit", "hello", 1, "...", "h"),
		ginkgo.Entry("emoji", "hi😀there", 6, "...", "hi..."),
		ginkgo.Entry("CJK", "hello世界", 10, "…", "hello…"),
		ginkgo.Entry("limit below first rune", "😀hello", 2, "...", ""),
	)

	ginkgo.DescribeTable("truncates with the default marker",
		func(input string, maxBytes int, want string) {
			got := Truncate(input, maxBytes)
			gomega.Expect(got).To(gomega.Equal(want))
			gomega.Expect(utf8.ValidString(got)).To(gomega.BeTrue())
		},
		ginkgo.Entry("shorter than limit", "hello", 10, "hello"),
		ginkgo.Entry("exactly at limit", "hello", 5, "hello"),
		ginkgo.Entry("longer than limit", "hello world", 8, "hello..."),
		ginkgo.Entry("empty", "", 5, ""),
		ginkgo.Entry("zero limit", "hello", 0, "hello"),
		ginkgo.Entry("negative limit", "hello", -1, "hello"),
		ginkgo.Entry("delegation", "abcdefghij", 5, "ab..."),
		ginkgo.Entry("multibyte", "ab😀cd", 6, "ab..."),
	)

	ginkgo.DescribeTable("reports omitted byte counts",
		func(input string, maxBytes int, want string) {
			gomega.Expect(TruncateWithByteCount(input, maxBytes)).To(gomega.Equal(want))
		},
		ginkgo.Entry("truncated ASCII", "hello world", 5, "hello\n\n[truncated 6 bytes]\n"),
		ginkgo.Entry("not truncated", "short", 10, "short"),
		ginkgo.Entry("truncated multibyte", "😀hello", 2, "\n\n[truncated 7 bytes]\n"),
	)
})
