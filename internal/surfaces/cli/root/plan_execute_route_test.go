package root

import (
	"bytes"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Plan-execute command routing", func() {
	ginkgo.It("routes the command and reports missing required arguments", func() {
		var stdout, stderr bytes.Buffer
		code := runMain([]string{"plan-execute"}, &stdout, &stderr)

		gomega.Expect(code).To(gomega.Equal(2), "stdout=%s stderr=%s", stdout.String(), stderr.String())
	})
})
