package cliflag

import (
	"flag"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("MultiFlag", func() {
	ginkgo.It("collects repeated flag values in order", func() {
		var values MultiFlag
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		flags.Var(&values, "f", "test flag")

		gomega.Expect(flags.Parse([]string{"-f", "a", "-f", "b", "-f", "c"})).To(gomega.Succeed())
		gomega.Expect(values).To(gomega.Equal(MultiFlag{"a", "b", "c"}))
		gomega.Expect(values.String()).To(gomega.Equal("a,b,c"))
	})
})
