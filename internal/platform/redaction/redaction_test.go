package redaction

import (
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Redaction", func() {
	apiKeyName := strings.Join([]string{"api", "_key"}, "")
	openAIKeyShort := strings.Join([]string{"sk", "-", "abc123def456"}, "")
	openAIKeyLong := strings.Join([]string{"sk", "-", "abcdefghijklmnopqrstuv"}, "")
	multipleSecretInput := strings.Join([]string{apiKeyName, "=secret1 token=secret2"}, "")
	multipleSecretWant := strings.Join([]string{apiKeyName, "=<redacted> token=<redacted>"}, "")

	ginkgo.DescribeTable("redacts secrets while preserving surrounding text",
		func(input, want string) {
			gomega.Expect(RedactSecrets(input)).To(gomega.Equal(want))
		},
		ginkgo.Entry("no secrets", "hello world", "hello world"),
		ginkgo.Entry("empty string", "", ""),
		ginkgo.Entry("api_key assignment", apiKeyName+"=mysecret123", apiKeyName+"=<redacted>"),
		ginkgo.Entry("apiKey colon with OpenAI key value", `"apiKey": "`+openAIKeyShort+`"`, `"apiKey": "<redacted-secret>"`),
		ginkgo.Entry("token assignment", `token=abcdef`, `token=<redacted>`),
		ginkgo.Entry("secret assignment", `secret: value123`, `secret=<redacted>`),
		ginkgo.Entry("password assignment", `password=hunter2`, `password=<redacted>`),
		ginkgo.Entry("case-insensitive api_key", strings.ToUpper(apiKeyName)+"=SECRET", strings.ToUpper(apiKeyName)+"=<redacted>"),
		ginkgo.Entry("case-insensitive token", `Token: MyToken`, `Token=<redacted>`),
		ginkgo.Entry("case-insensitive password", `PASSWORD=topsecret`, `PASSWORD=<redacted>`),
		ginkgo.Entry("bearer token", `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload`, `Authorization: Bearer <redacted>`),
		ginkgo.Entry("case-insensitive bearer", `bearer abc123DEF`, `Bearer <redacted>`),
		ginkgo.Entry("OpenAI key", `key=`+openAIKeyLong, `key=<redacted-secret>`),
		ginkgo.Entry("short OpenAI-like key", `key=sk-short`, `key=sk-short`),
		ginkgo.Entry("multiple secrets", multipleSecretInput, multipleSecretWant),
		ginkgo.Entry("api-key with dash", `api-key=mykey`, `api-key=<redacted>`),
		ginkgo.Entry("surrounded by text", `the `+apiKeyName+`=secret123 here`, `the `+apiKeyName+`=<redacted> here`),
	)

	ginkgo.Describe("redacted value fingerprints", func() {
		ginkgo.DescribeTable("uses an empty placeholder for blank input",
			func(input string) {
				gomega.Expect(RedactedValue(input)).To(gomega.Equal("<empty>"))
			},
			ginkgo.Entry("empty", ""),
			ginkgo.Entry("whitespace", "   "),
		)

		ginkgo.It("is stable for the same normalized input", func() {
			gomega.Expect(RedactedValue("my-secret-key")).To(gomega.Equal(RedactedValue("my-secret-key")))
			gomega.Expect(RedactedValue("  secret  ")).To(gomega.Equal(RedactedValue("secret")))
		})

		ginkgo.It("distinguishes different inputs", func() {
			gomega.Expect(RedactedValue("secret-a")).NotTo(gomega.Equal(RedactedValue("secret-b")))
		})

		ginkgo.It("includes the normalized length and SHA-256 prefix", func() {
			gomega.Expect(RedactedValue("hello")).To(gomega.ContainSubstring("len=5"))
			gomega.Expect(RedactedValue("test")).To(gomega.And(
				gomega.HavePrefix("<redacted "),
				gomega.ContainSubstring("sha256:"),
			))
		})
	})
})
