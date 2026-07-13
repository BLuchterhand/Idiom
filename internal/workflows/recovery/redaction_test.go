package recovery

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Recovery detail redaction", func() {
	ginkgo.It("removes transcript and file-body content", func() {
		redacted := RedactText("raw_prompt: operator text\nfile_body:\n```text\ncustomer source\npassword=secret\n```\ntranscript: hidden")

		forbiddenDetails := []string{"operator text", "customer source", "password=secret", "hidden"}
		for _, forbidden := range forbiddenDetails {
			gomega.Expect(redacted).NotTo(gomega.ContainSubstring(forbidden))
		}
		gomega.Expect(redacted).To(gomega.And(
			gomega.ContainSubstring("[redacted sensitive recovery detail]"),
			gomega.ContainSubstring("[redacted fenced recovery detail]"),
		))
	})

	ginkgo.DescribeTable("redacts shared-secret markers",
		func(input string) {
			redacted := RedactText(input)
			gomega.Expect(redacted).NotTo(gomega.ContainSubstring(input))
			gomega.Expect(redacted).To(gomega.ContainSubstring("[redacted sensitive recovery detail]"))
		},
		ginkgo.Entry("authorization bearer token", "Authorization: Bearer abc123"),
		ginkgo.Entry("AWS access key", "aws=AKIA1234567890ABCD"),
		ginkgo.Entry("GitHub personal access token", "github=ghp_1234567890abcdef"),
		ginkgo.Entry("API key", "api_key=secret-value"),
	)
})
