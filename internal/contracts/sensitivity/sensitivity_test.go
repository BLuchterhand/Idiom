package sensitivity

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Sensitive text policy", func() {
	awsAccessKeyLike := strings.Join([]string{"AKIA", "1234567890ABCD"}, "")
	githubTokenLike := strings.Join([]string{"ghp", "_", "1234567890abcdef"}, "")
	openAIKeyLike := strings.Join([]string{"sk", "-", "1234567890abcdef"}, "")

	ginkgo.DescribeTable("recognizes secret markers",
		func(input string) {
			gomega.Expect(ContainsSecretMarker(input)).To(gomega.BeTrue())
		},
		ginkgo.Entry("bearer authorization", "Authorization: Bearer abc123"),
		ginkgo.Entry("AWS access key", awsAccessKeyLike),
		ginkgo.Entry("GitHub token", githubTokenLike),
		ginkgo.Entry("token assignment", "token=abc123"),
		ginkgo.Entry("secret label", "secret abc123"),
		ginkgo.Entry("OpenAI key", openAIKeyLike),
	)

	ginkgo.DescribeTable("allows non-secret prose containing policy vocabulary",
		func(input string) {
			gomega.Expect(ContainsSecretMarker(input)).To(gomega.BeFalse())
		},
		ginkgo.Entry("token budget", "optional total budget tokens for the run"),
		ginkgo.Entry("authorization boundary", "authorization boundary documentation"),
		ginkgo.Entry("authorization URL", "Authorization: https://example.invalid/spec"),
		ginkgo.Entry("task-specific workaround", "task-specific workaround"),
	)

	ginkgo.DescribeTable("recognizes privacy-sensitive text",
		func(input string) {
			gomega.Expect(ContainsSensitiveText(input)).To(gomega.BeTrue())
		},
		ginkgo.Entry("raw prompt", "raw_prompt: summarize"),
		ginkgo.Entry("file body", "file_body includes source"),
		ginkgo.Entry("customer session", "customer-acme-session"),
		ginkgo.Entry("token", "token abc123"),
		ginkgo.Entry("absolute home path", "/Users/example/private/repo"),
		ginkgo.Entry("tilde workspace path", "~/workspace/customer"),
	)

	ginkgo.DescribeTable("allows benign secret-related prose",
		func(input string) {
			gomega.Expect(ContainsSecretMarker(input)).To(gomega.BeFalse())
		},
		ginkgo.Entry("token budget", "optional total budget tokens for the run"),
		ginkgo.Entry("task-specific workaround", "task-specific workaround"),
		ginkgo.Entry("authorization boundary", "authorization boundary documentation"),
	)
})
