package workspace

import (
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("tool timeout floor from the environment", func() {
	req := ToolRequest{Tool: ToolNameContextImpact}

	ginkgo.It("raises a built-in tool's budget", func() {
		ginkgo.GinkgoT().Setenv(toolTimeoutFloorEnv, "240")
		gomega.Expect(ToolTimeoutForRequest(req)).To(gomega.Equal(240 * time.Second))
	})

	ginkgo.It("never lowers a budget and ignores junk", func() {
		ginkgo.GinkgoT().Setenv(toolTimeoutFloorEnv, "")
		base := ToolTimeoutForRequest(req)

		ginkgo.GinkgoT().Setenv(toolTimeoutFloorEnv, "1")
		gomega.Expect(ToolTimeoutForRequest(req)).To(gomega.Equal(base))

		ginkgo.GinkgoT().Setenv(toolTimeoutFloorEnv, "soon")
		gomega.Expect(ToolTimeoutForRequest(req)).To(gomega.Equal(base))
	})

	ginkgo.It("leaves a tool with no outer timeout alone", func() {
		ginkgo.GinkgoT().Setenv(toolTimeoutFloorEnv, "240")
		gomega.Expect(ToolTimeoutForRequest(ToolRequest{Tool: ToolNameShell})).To(gomega.BeZero())
	})
})
