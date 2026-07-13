package productworkflow

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("context freshness", func() {
	ginkgo.It("ContextFreshnessCurrentHelpersMatchVocabulary", func() {
		tests := []struct {
			value string
			want  bool
		}{
			{value: "current", want: true},
			{value: "fresh", want: true},
			{value: "verified", want: true},
			{value: " VERIFIED ", want: true},
			{value: "stale"},
			{value: ""},
		}

		for _, tt := range tests {
			{
				got := productWorkflowContextFreshnessCurrent(tt.value)
				gomega.Expect(got != tt.want).To(gomega.BeFalse(), fmt.Sprintf("productWorkflowContextFreshnessCurrent(%q) = %t, want %t", tt.value, got, tt.want))
			}

		}
	})
})
