package root

import (
	"bytes"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Coding-rules command routing", func() {
	ginkgo.It("routes help through the root command", func() {
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"coding-rules", "--help"}, &stdout, &stderr)

		gomega.Expect(code).To(gomega.Equal(0), "stdout=%s stderr=%s", stdout.String(), stderr.String())
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("idiom coding-rules check"))
	})
})
