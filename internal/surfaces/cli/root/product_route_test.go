package root

import (
	"bytes"

	productcmd "idiom/internal/surfaces/cli/product"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Product command routing", func() {
	ginkgo.It("delegates workflow help to the product CLI owner", func() {
		var stdout, stderr bytes.Buffer
		code := productcmd.RunMainWithTelemetry([]string{"help"}, &stdout, &stderr, nil)

		gomega.Expect(code).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(stdout.String()).NotTo(gomega.BeEmpty())
	})
})
