package cliassets

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Embedded CLI assets", func() {
	ginkgo.It("exposes the expected prompt and skill bundles", func() {
		preloadKinds, err := BriefPreloadKinds()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(preloadKinds).To(gomega.ContainElement("generic"))

		body, err := LanePrompt("implement", "preamble.md")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.TrimSpace(body)).NotTo(gomega.BeEmpty())

		body, err = LaneRule("brief-skeleton.md")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(strings.TrimSpace(body)).NotTo(gomega.BeEmpty())

		skillNames, err := SkillNames()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(skillNames).To(gomega.ContainElement("orchestrator-workflow"))
	})
})
