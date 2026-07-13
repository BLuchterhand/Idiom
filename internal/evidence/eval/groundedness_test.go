package eval

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("groundedness score returns fraction of matched tokens", func() {
	context := "The runner enforces the request budget through the budget ledger snapshot."
	high := "The runner enforces the request budget through the budget ledger."
	low := "Generally speaking, the answer depends on context."

	highScore := GroundednessScore(high, context)
	lowScore := GroundednessScore(low, context)

	gomega.Expect(highScore).To(gomega.BeNumerically(">", lowScore), fmt.Sprintf("expected grounded > generic; got high=%v low=%v", highScore, lowScore))

	gomega.Expect(highScore).To(gomega.BeNumerically(">=", 0.5), fmt.Sprintf("grounded response should clear 0.5, got %v", highScore))
})
var _ = ginkgo.It("groundedness score empty inputs", func() {
	gomega.Expect(GroundednessScore("", "anything")).To(gomega.BeEquivalentTo(0), fmt.Sprintf("empty response must score 0"))

	gomega.Expect(GroundednessScore("anything", "")).To(gomega.BeEquivalentTo(0), fmt.Sprintf("empty context must score 0"))
})
var _ = ginkgo.It("detect generic phrases flags known leaks", func() {
	text := "However, based on my knowledge, the runner generally speaking handles retries."
	hits := DetectGenericPhrases(text)

	gomega.Expect(hits).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected at least one generic phrase, got none"))
})
var _ = ginkgo.It("detect generic phrases ignores grounded answers", func() {
	text := "runner.go retries via the request budget ledger when the agent rejects a tool result."
	{
		hits := DetectGenericPhrases(text)
		gomega.Expect(hits).To(gomega.HaveLen(0), fmt.Sprintf("grounded answer flagged hits: %v", hits))
	}
})
var _ = ginkgo.It("generic phrase band classifies", func() {
	gomega.Expect(GenericPhraseBand(0.80, 0.75, 0.40)).To(gomega.BeEquivalentTo("boost"), fmt.Sprintf("0.80 should land in boost band"))

	gomega.Expect(GenericPhraseBand(0.30, 0.75, 0.40)).To(gomega.BeEquivalentTo("penalize"), fmt.Sprintf("0.30 should land in penalize band"))

	gomega.Expect(GenericPhraseBand(0.55, 0.75, 0.40)).To(gomega.BeEquivalentTo("no_op"), fmt.Sprintf("0.55 should land in no_op band"))
})
