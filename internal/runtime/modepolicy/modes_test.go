package modepolicy

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/toolpolicy"
)

var _ = ginkgo.Describe("Agent mode policy", func() {
	ginkgo.It("round-trips every declared mode by name", func() {
		for _, wantMode := range DefaultModes() {
			mode, found := ModeByName(string(wantMode.Name))
			gomega.Expect(found).To(gomega.BeTrue())
			gomega.Expect(mode.Name).To(gomega.Equal(wantMode.Name))
		}
		_, found := ModeByName("not-a-mode")
		gomega.Expect(found).To(gomega.BeFalse())
	})

	ginkgo.DescribeTable("selects the requested mode or the build default",
		func(requested, want string) {
			gomega.Expect(string(ModeOrDefault(requested).Name)).To(gomega.Equal(want))
		},
		ginkgo.Entry("empty", "", "build"),
		ginkgo.Entry("plan", "plan", "plan"),
		ginkgo.Entry("unknown", "nonsense", "build"),
	)

	ginkgo.DescribeTable("applies mode-specific MCP defaults",
		func(modeName string, want toolpolicy.Decision) {
			mode, found := ModeByName(modeName)
			gomega.Expect(found).To(gomega.BeTrue())
			rules, found := mode.PermissionRules["mcp:*"]
			gomega.Expect(found).To(gomega.BeTrue())
			gomega.Expect(rules).To(gomega.HaveLen(1))
			gomega.Expect(rules[0].Decision).To(gomega.Equal(want))
		},
		ginkgo.Entry("build asks", "build", toolpolicy.Ask),
		ginkgo.Entry("plan allows", "plan", toolpolicy.Allow),
		ginkgo.Entry("general asks", "general", toolpolicy.Ask),
	)
})
