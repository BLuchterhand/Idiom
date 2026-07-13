package textutil

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("String slice utilities", func() {
	ginkgo.DescribeTable("appends only new non-empty values",
		func(input []string, value string, want []string) {
			gomega.Expect(AppendUnique(input, value)).To(gomega.Equal(want))
		},
		ginkgo.Entry("new value", []string{"a", "b"}, "c", []string{"a", "b", "c"}),
		ginkgo.Entry("duplicate", []string{"a", "b"}, "a", []string{"a", "b"}),
		ginkgo.Entry("nil input", nil, "x", []string{"x"}),
		ginkgo.Entry("empty value", []string{"a"}, "", []string{"a"}),
	)

	ginkgo.Describe("LimitSlice", func() {
		ginkgo.It("returns a copy when the slice is at or below the limit", func() {
			for _, limit := range []int{3, 10} {
				original := []string{"a", "b", "c"}
				result := LimitSlice(original, limit)
				gomega.Expect(result).To(gomega.Equal(original))
				gomega.Expect(&result[0]).NotTo(gomega.BeIdenticalTo(&original[0]))
			}
		})

		ginkgo.It("truncates values beyond a positive limit", func() {
			gomega.Expect(LimitSlice([]string{"a", "b", "c", "d", "e"}, 3)).To(gomega.Equal([]string{"a", "b", "c"}))
		})

		ginkgo.DescribeTable("leaves the slice unbounded for a non-positive limit",
			func(limit int) {
				gomega.Expect(LimitSlice([]string{"a", "b"}, limit)).To(gomega.Equal([]string{"a", "b"}))
			},
			ginkgo.Entry("zero", 0),
			ginkgo.Entry("negative", -5),
		)

		ginkgo.It("preserves nil and empty inputs", func() {
			gomega.Expect(LimitSlice(nil, 5)).To(gomega.BeNil())
			gomega.Expect(LimitSlice([]string{}, 5)).To(gomega.BeEmpty())
		})
	})

	ginkgo.It("normalizes unique values in first-seen order", func() {
		gomega.Expect(Unique([]string{"a", "  b  ", "a", "", "b", "c"})).To(gomega.Equal([]string{"a", "b", "c"}))
		gomega.Expect(Unique(nil)).To(gomega.BeNil())
		gomega.Expect(Unique([]string{})).To(gomega.BeNil())
	})

	ginkgo.It("merges slices into normalized first-seen order", func() {
		gomega.Expect(Merge([]string{"a", "b"}, []string{"b", "c"}, nil)).To(gomega.Equal([]string{"a", "b", "c"}))
		gomega.Expect(Merge()).To(gomega.BeNil())
	})

	ginkgo.It("returns normalized values in lexical order", func() {
		gomega.Expect(Sorted([]string{"c", "a", "b", "a", ""})).To(gomega.Equal([]string{"a", "b", "c"}))
	})

	ginkgo.DescribeTable("removes selected values",
		func(values, remove, want []string) {
			gomega.Expect(Remove(values, remove)).To(gomega.Equal(want))
		},
		ginkgo.Entry("selected values", []string{"a", "b", "c", "d"}, []string{"b", "d"}, []string{"a", "c"}),
		ginkgo.Entry("empty removal set", []string{"a", "b"}, nil, []string{"a", "b"}),
		ginkgo.Entry("nil values", nil, []string{"a"}, nil),
	)

	ginkgo.Describe("Prefix", func() {
		ginkgo.It("uses the original backing array when truncating", func() {
			original := []string{"a", "b", "c", "d", "e"}
			result := Prefix(original, 3)
			gomega.Expect(result).To(gomega.Equal([]string{"a", "b", "c"}))
			gomega.Expect(&result[0]).To(gomega.BeIdenticalTo(&original[0]))
		})

		ginkgo.DescribeTable("returns the original slice when the maximum does not truncate",
			func(maximum int) {
				original := []string{"a", "b"}
				result := Prefix(original, maximum)
				gomega.Expect(result).To(gomega.Equal(original))
				gomega.Expect(&result[0]).To(gomega.BeIdenticalTo(&original[0]))
			},
			ginkgo.Entry("above length", 10),
			ginkgo.Entry("zero", 0),
			ginkgo.Entry("negative", -1),
		)
	})
})
