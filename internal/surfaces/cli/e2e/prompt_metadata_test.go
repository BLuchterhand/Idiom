package e2e

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Prompt metadata", ginkgo.Label("integration", "contract"), func() {
	ginkgo.It("does not leak the raw prompt", func() {
		const secret = "RAW_PROMPT_SHOULD_NOT_LEAK"

		payload := map[string]any{
			"turns": e2ePromptMetadataList([]string{"first", secret}),
			"user":  e2ePromptMetadata(secret),
		}

		data, err := json.Marshal(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring(secret))
		gomega.Expect(string(data)).To(gomega.ContainSubstring("sha256:"))
		gomega.Expect(string(data)).To(gomega.ContainSubstring(`"content_length":`))
	})
})
