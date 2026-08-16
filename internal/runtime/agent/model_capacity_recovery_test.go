package agent

import (
	"errors"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/runtime/llama"
)

var _ = ginkgo.Describe("model capacity recovery", func() {
	ginkgo.It("recognizes backend allocation failures without treating ordinary model errors as capacity failures", func() {
		gomega.Expect(modelCapacityFailure(errors.New("chat request failed: [metal::malloc] Resource limit (499000) exceeded"))).To(gomega.BeTrue())
		gomega.Expect(modelCapacityFailure(errors.New("invalid request: malformed JSON schema"))).To(gomega.BeFalse())
	})

	ginkgo.It("removes preserved reasoning and proportionally reduces only the recovery request budget", func() {
		thinkingOn := true
		got := modelCapacityRecoveryOptions(llama.ChatOptions{
			EnableThinking:   &thinkingOn,
			PreserveThinking: true,
			ThinkingBudget:   4096,
			MaxTokens:        8192,
		})

		gomega.Expect(got.EnableThinking).NotTo(gomega.BeNil())
		gomega.Expect(*got.EnableThinking).To(gomega.BeFalse())
		gomega.Expect(got.PreserveThinking).To(gomega.BeFalse())
		gomega.Expect(got.ThinkingBudget).To(gomega.Equal(0))
		gomega.Expect(got.MaxTokens).To(gomega.Equal(2048))
		gomega.Expect(strings.TrimSpace(got.Model)).To(gomega.BeEmpty())
	})
})
