package redaction

import (
	"encoding/json"
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
		ginkgo.Entry("apiKey colon with OpenAI key value", `"apiKey": "`+openAIKeyShort+`"`, `"apiKey": "<redacted>"`),
		ginkgo.Entry("quoted JSON credential", `{"password":"private value with spaces","ok":true}`, `{"password":"<redacted>","ok":true}`),
		ginkgo.Entry("quoted assignment", `password="private value with spaces" next`, `password=<redacted> next`),
		ginkgo.Entry("environment variable", `SERVICE_API_KEY=private-value next`, `SERVICE_API_KEY=<redacted> next`),
		ginkgo.Entry("modern provider key", `key=`+strings.Join([]string{"sk", "proj", "abcdefghijklmno", "pqrstuvwxyz1234"}, "-"), `key=<redacted-secret>`),
		ginkgo.Entry("URL credentials", `https://name:private-value@example.test/api`, `https://<redacted>@example.test/api`),
		ginkgo.Entry("basic authentication", `Authorization: Basic YWJjZGVmZw==`, `Authorization: Basic <redacted>`),
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

	ginkgo.It("redacts typed JSON collections without mutating input or losing integer precision", func() {
		type entry struct {
			Authorization string `json:"authorization"`
			Count         int64  `json:"count"`
		}
		input := map[string]any{"headers": []entry{{Authorization: "private value", Count: 9007199254740993}}, "prompt_tokens": 12}
		data, err := MarshalJSON(input)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(json.Valid(data)).To(gomega.BeTrue())
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring("private value"))
		gomega.Expect(string(data)).To(gomega.ContainSubstring(`"count":9007199254740993`))
		gomega.Expect(string(data)).To(gomega.ContainSubstring(`"prompt_tokens":12`))
		gomega.Expect(input["headers"].([]entry)[0].Authorization).To(gomega.Equal("private value"))
	})

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
