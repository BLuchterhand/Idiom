package queue

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"strings"
)

var _ = ginkgo.Describe("queue idle", func() {
	ginkgo.It("format job age renders units correctly", func() {
		cases := []struct {
			seconds int
			idle    bool
			want    string
		}{
			{0, false, "0s"},
			{5, false, "5s"},
			{59, false, "59s"},
			{60, false, "1m0s"},
			{125, false, "2m5s"},
			{3599, false, "59m59s"},
			{3600, false, "1h0m"},
			{7325, false, "2h2m"},
			{-1, false, "0s"},
		}
		for _, c := range cases {
			got := formatJobAge(c.seconds, c.idle)
			gomega.Expect(got).To(gomega.Equal(c.want), fmt.Sprintf("formatJobAge(%d, %v) = %q, want %q", c.seconds, c.idle, got, c.want))

		}
	})
	ginkgo.It("format job age appends idle suffix", func() {
		got := formatJobAge(900, true)
		gomega.Expect(strings.HasSuffix(got, " IDLE")).To(gomega.BeTrue(), fmt.Sprintf("idle suffix missing: got %q", got))
		gomega.Expect(strings.HasPrefix(got, "15m")).To(gomega.BeTrue(), fmt.Sprintf("expected 15m prefix, got %q", got))

	})
	ginkgo.It("format job age omits idle suffix when false", func() {
		got := formatJobAge(900, false)
		gomega.Expect(got).NotTo(gomega.ContainSubstring("IDLE"), fmt.Sprintf("idle suffix should be absent: got %q", got))

	})
})
